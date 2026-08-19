package history

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

const maximumResponseBodySize = int64(8 << 20)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	uuidV7Pattern     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type Engine interface {
	QueryDeviceHistory(context.Context, telemetryhistorymodel.DeviceHistoryQuery) (telemetryhistorymodel.DeviceHistoryResponse, error)
	QueryDeviceHistoryAggregate(context.Context, telemetryhistorymodel.DeviceHistoryAggregateQuery) (telemetryhistorymodel.DeviceHistoryAggregateResponse, error)
}

type Config struct {
	BaseURL    string
	Database   string
	Table      string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type Client struct {
	endpoint   *url.URL
	database   string
	table      string
	username   string
	password   string
	httpClient *http.Client
}

type pointRow struct {
	ObservationID   string   `json:"observation_id"`
	PointID         string   `json:"point_id"`
	SensorID        *string  `json:"sensor_id"`
	TelemetryKey    string   `json:"telemetry_key"`
	PointType       string   `json:"point_type"`
	PointRevision   uint64   `json:"point_revision"`
	SampledAt       string   `json:"sampled_at"`
	ReceivedAt      string   `json:"received_at"`
	Acceptance      string   `json:"acceptance_status"`
	ValueType       string   `json:"value_type"`
	ValueJSON       string   `json:"value_json"`
	Unit            *string  `json:"unit"`
	Quality         string   `json:"quality"`
	QualityReasons  []string `json:"quality_reasons"`
	SourceEventID   string   `json:"source_event_id"`
	SourcePartition string   `json:"source_partition"`
	SourceOffset    int64    `json:"source_offset"`
}

type snapshotRow struct {
	SnapshotAt          string  `json:"snapshot_at"`
	ProjectionWatermark *string `json:"projection_watermark"`
}

type historyCursor struct {
	Version             int     `json:"v"`
	ScopeDigest         string  `json:"scope"`
	SnapshotAt          string  `json:"snapshotAt"`
	ProjectionWatermark *string `json:"projectionWatermark,omitempty"`
	LastTelemetryKey    string  `json:"lastTelemetryKey"`
	LastSampledAt       string  `json:"lastSampledAt"`
	LastObservationID   string  `json:"lastObservationId"`
}

func NewClient(config Config) (*Client, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("ClickHouse history base URL must be an HTTP(S) origin")
	}
	if !identifierPattern.MatchString(config.Database) || !identifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse history identifiers are invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Client{
		endpoint: endpoint, database: config.Database, table: config.Table,
		username: strings.TrimSpace(config.Username), password: config.Password, httpClient: client,
	}, nil
}

func (client *Client) QueryDeviceHistory(ctx context.Context, query telemetryhistorymodel.DeviceHistoryQuery) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	if client == nil || client.endpoint == nil || client.httpClient == nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, errors.New("ClickHouse history client is closed")
	}
	canonical, err := query.Canonical()
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, err
	}
	cursor, snapshotAt, watermark, err := client.resolveSnapshot(ctx, canonical)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, err
	}
	payload, err := client.execute(ctx, client.pointsQuery(canonical, snapshotAt, cursor))
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, err
	}
	return client.buildResponse(canonical, payload, snapshotAt, watermark)
}

func (client *Client) resolveSnapshot(ctx context.Context, query telemetryhistorymodel.DeviceHistoryQuery) (*historyCursor, time.Time, *time.Time, error) {
	if query.Cursor != nil {
		cursor, err := decodeCursor(*query.Cursor, query)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		snapshotAt, err := parseClickHouseTime(cursor.SnapshotAt)
		if err != nil {
			return nil, time.Time{}, nil, errors.New("history cursor snapshot is invalid")
		}
		var watermark *time.Time
		if cursor.ProjectionWatermark != nil {
			parsed, err := parseClickHouseTime(*cursor.ProjectionWatermark)
			if err != nil {
				return nil, time.Time{}, nil, errors.New("history cursor projection watermark is invalid")
			}
			watermark = &parsed
		}
		return cursor, snapshotAt, watermark, nil
	}
	payload, err := client.execute(ctx, client.snapshotQuery(query))
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	var row snapshotRow
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&row); err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("decode ClickHouse history snapshot: %w", err)
	}
	snapshotAt, err := parseClickHouseTime(row.SnapshotAt)
	if err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("decode ClickHouse history snapshot time: %w", err)
	}
	var watermark *time.Time
	if row.ProjectionWatermark != nil && strings.TrimSpace(*row.ProjectionWatermark) != "" {
		parsed, err := parseClickHouseTime(*row.ProjectionWatermark)
		if err != nil {
			return nil, time.Time{}, nil, fmt.Errorf("decode ClickHouse projection watermark: %w", err)
		}
		watermark = &parsed
	}
	return nil, snapshotAt, watermark, nil
}

func (client *Client) execute(ctx context.Context, query string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint.String(), strings.NewReader(query))
	if err != nil {
		return nil, fmt.Errorf("create ClickHouse history request: %w", err)
	}
	request.Header.Set("Accept", "application/x-ndjson")
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if client.username != "" {
		request.SetBasicAuth(client.username, client.password)
	}
	observability.InjectHTTP(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse history: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read ClickHouse history response: %w", err)
	}
	if int64(len(payload)) > maximumResponseBodySize {
		return nil, errors.New("ClickHouse history response exceeds 8 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("ClickHouse history query failed with status %d", response.StatusCode)
	}
	return payload, nil
}

func (client *Client) buildResponse(query telemetryhistorymodel.DeviceHistoryQuery, payload []byte, snapshotAt time.Time, watermark *time.Time) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	rows := make([]pointRow, 0, query.PageSize+1)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var row pointRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode ClickHouse history observation: %w", err)
		}
		rows = append(rows, row)
	}
	hasNext := len(rows) > query.PageSize
	if hasNext {
		rows = rows[:query.PageSize]
	}
	observations := make([]telemetryhistorymodel.DeviceHistoryObservation, 0, len(rows))
	for _, row := range rows {
		observation, err := decodePointRow(row)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, err
		}
		observations = append(observations, observation)
	}
	var nextCursor *string
	if hasNext && len(observations) > 0 {
		last := observations[len(observations)-1]
		encoded, err := encodeCursor(query, snapshotAt, watermark, last)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, err
		}
		nextCursor = &encoded
	}
	response := telemetryhistorymodel.DeviceHistoryResponse{
		SchemaVersion: 2, TenantID: query.TenantID, SiteID: query.SiteID, DeviceID: query.DeviceID,
		Observations: observations,
		Metadata: telemetryhistorymodel.DeviceHistoryMetadata{
			RequestedFrom: query.From, RequestedTo: query.To, ProjectionWatermark: watermark,
			PageSize: query.PageSize, ReturnedObservations: len(observations), NextCursor: nextCursor,
		},
	}
	if err := response.ValidateFor(query); err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("validate ClickHouse history response: %w", err)
	}
	return response, nil
}

func decodePointRow(row pointRow) (telemetryhistorymodel.DeviceHistoryObservation, error) {
	sampledAt, err := parseClickHouseTime(row.SampledAt)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryObservation{}, fmt.Errorf("decode ClickHouse history sampled time: %w", err)
	}
	receivedAt, err := parseClickHouseTime(row.ReceivedAt)
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryObservation{}, fmt.Errorf("decode ClickHouse history received time: %w", err)
	}
	value := json.RawMessage(row.ValueJSON)
	if len(value) == 0 || !json.Valid(value) {
		return telemetryhistorymodel.DeviceHistoryObservation{}, errors.New("ClickHouse history returned invalid typed JSON value")
	}
	return telemetryhistorymodel.DeviceHistoryObservation{
		ObservationID: row.ObservationID, TelemetryKey: row.TelemetryKey, PointID: row.PointID, SensorID: row.SensorID,
		PointType: telemetryhistorymodel.PointType(row.PointType), PointRevision: row.PointRevision,
		SampledAt: sampledAt, ReceivedAt: receivedAt, Acceptance: telemetryhistorymodel.Acceptance(row.Acceptance),
		ValueType: telemetryhistorymodel.ValueType(row.ValueType), Value: append(json.RawMessage(nil), value...), Unit: row.Unit,
		Quality: telemetryhistorymodel.Quality(row.Quality), QualityReasons: append([]string{}, row.QualityReasons...),
		SourcePosition: telemetryhistorymodel.SourcePosition{Partition: row.SourcePartition, Offset: row.SourceOffset, EventID: row.SourceEventID},
	}, nil
}

func (client *Client) snapshotQuery(query telemetryhistorymodel.DeviceHistoryQuery) string {
	return fmt.Sprintf(`WITH now64(3) AS snapshot_at
SELECT
  formatDateTime(snapshot_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS snapshot_at,
  if(count() = 0, CAST(NULL, 'Nullable(String)'), formatDateTime(max(projected_at), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC')) AS projection_watermark
FROM %s.%s
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
  AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND projected_at < snapshot_at
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_id IS NOT NULL
  AND point_revision IS NOT NULL
  AND value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')
  AND value_json IS NOT NULL
FORMAT JSONEachRow`, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.From), formatClickHouseTime(query.To))
}

func (client *Client) pointsQuery(query telemetryhistorymodel.DeviceHistoryQuery, snapshotAt time.Time, cursor *historyCursor) string {
	cursorPredicate := ""
	if cursor != nil {
		cursorPredicate = fmt.Sprintf(`
  AND (telemetry_key > '%s'
    OR (telemetry_key = '%s' AND sampled_at > parseDateTime64BestEffort('%s', 3, 'UTC'))
    OR (telemetry_key = '%s' AND sampled_at = parseDateTime64BestEffort('%s', 3, 'UTC') AND toString(observation_id) > '%s'))`,
			cursor.LastTelemetryKey, cursor.LastTelemetryKey, cursor.LastSampledAt, cursor.LastTelemetryKey, cursor.LastSampledAt, cursor.LastObservationID)
	}
	return fmt.Sprintf(`SELECT
  toString(observation_id) AS observation_id,
  toString(assumeNotNull(point_id)) AS point_id,
  if(sensor_id IS NULL, CAST(NULL, 'Nullable(String)'), toString(sensor_id)) AS sensor_id,
  telemetry_key,
  assumeNotNull(point_type) AS point_type,
  assumeNotNull(point_revision) AS point_revision,
  formatDateTime(sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS sampled_at,
  formatDateTime(received_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS received_at,
  acceptance_status,
  assumeNotNull(value_type) AS value_type,
  assumeNotNull(value_json) AS value_json,
  unit,
  quality,
  quality_reasons,
  toString(source_event_id) AS source_event_id,
  source_partition,
  source_offset
FROM %s.%s
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
  AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND projected_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_id IS NOT NULL
  AND point_type IS NOT NULL
  AND point_revision IS NOT NULL
  AND value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')
  AND value_json IS NOT NULL%s
ORDER BY telemetry_key, sampled_at, toString(observation_id)
LIMIT %d
FORMAT JSONEachRow`, client.database, client.table, query.TenantID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.From), formatClickHouseTime(query.To), formatClickHouseTime(snapshotAt), cursorPredicate, query.PageSize+1)
}

func encodeCursor(query telemetryhistorymodel.DeviceHistoryQuery, snapshotAt time.Time, watermark *time.Time, last telemetryhistorymodel.DeviceHistoryObservation) (string, error) {
	scope, err := query.CursorScopeDigest()
	if err != nil {
		return "", err
	}
	cursor := historyCursor{
		Version: 1, ScopeDigest: scope, SnapshotAt: formatClickHouseTime(snapshotAt),
		LastTelemetryKey: last.TelemetryKey, LastSampledAt: formatClickHouseTime(last.SampledAt), LastObservationID: last.ObservationID,
	}
	if watermark != nil {
		value := formatClickHouseTime(*watermark)
		cursor.ProjectionWatermark = &value
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCursor(encoded string, query telemetryhistorymodel.DeviceHistoryQuery) (*historyCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(payload) == 0 || len(payload) > telemetryhistorymodel.MaximumHistoryCursorBytes {
		return nil, errors.New("history cursor is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cursor historyCursor
	if err := decoder.Decode(&cursor); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, errors.New("history cursor is invalid")
	}
	scope, err := query.CursorScopeDigest()
	if err != nil || cursor.Version != 1 || cursor.ScopeDigest != scope || !telemetryKeyInQuery(cursor.LastTelemetryKey, query.Keys) {
		return nil, errors.New("history cursor does not match the query")
	}
	snapshotAt, err := parseClickHouseTime(cursor.SnapshotAt)
	if err != nil {
		return nil, errors.New("history cursor snapshot is invalid")
	}
	lastSampledAt, err := parseClickHouseTime(cursor.LastSampledAt)
	if err != nil || lastSampledAt.Before(query.From) || !lastSampledAt.Before(query.To) {
		return nil, errors.New("history cursor position is invalid")
	}
	if !uuidV7Pattern.MatchString(cursor.LastObservationID) {
		return nil, errors.New("history cursor observation identity is invalid")
	}
	cursor.SnapshotAt = formatClickHouseTime(snapshotAt)
	cursor.LastSampledAt = formatClickHouseTime(lastSampledAt)
	if cursor.ProjectionWatermark != nil {
		watermark, err := parseClickHouseTime(*cursor.ProjectionWatermark)
		if err != nil {
			return nil, errors.New("history cursor projection watermark is invalid")
		}
		canonical := formatClickHouseTime(watermark)
		cursor.ProjectionWatermark = &canonical
	}
	return &cursor, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func telemetryKeyInQuery(key string, keys []string) bool {
	for _, candidate := range keys {
		if key == candidate {
			return true
		}
	}
	return false
}

func quotedKeys(keys []string) string {
	ordered := append([]string(nil), keys...)
	sort.Strings(ordered)
	quoted := make([]string, len(ordered))
	for index, key := range ordered {
		quoted[index] = "'" + key + "'"
	}
	return strings.Join(quoted, ",")
}

func formatClickHouseTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func parseClickHouseTime(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05.000Z", time.RFC3339Nano} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid ClickHouse timestamp")
}
