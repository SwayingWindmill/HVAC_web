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
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const maximumResponseBodySize = int64(8 << 20)

var identifierPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,127}$`)

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
	ch         *clickhousehttp.Client
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
	endpoint, err := clickhousehttp.ParseOriginURL(config.BaseURL)
	if err != nil {
		return nil, errors.New("ClickHouse history base URL must be an HTTP(S) origin")
	}
	if !identifierPattern.MatchString(config.Database) || !identifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse history identifiers are invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = clickhousehttp.NewPooledHTTPClient(clickhousehttp.DataPlaneTransportConfig(8 * time.Second))
	}
	trimmedUser := strings.TrimSpace(config.Username)
	creds := clickhousehttp.Credentials{Username: trimmedUser, Password: config.Password}
	return &Client{
		endpoint: endpoint, database: config.Database, table: config.Table,
		username: trimmedUser, password: config.Password, httpClient: client,
		ch: clickhousehttp.NewClient(endpoint, client, creds),
	}, nil
}

func (client *Client) transportClient() *clickhousehttp.Client {
	if client.ch != nil {
		return client.ch
	}
	return clickhousehttp.NewClient(client.endpoint, client.httpClient, clickhousehttp.Credentials{Username: client.username, Password: client.password})
}

func (client *Client) Ping(ctx context.Context) error {
	if client == nil || client.endpoint == nil || client.httpClient == nil {
		return errors.New("ClickHouse history client is closed")
	}
	if err := client.transportClient().Ping(ctx, true); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return &clickHouseQueryError{statusCode: statusErr.StatusCode, body: statusErr.Body}
		}
		return fmt.Errorf("ping ClickHouse history: %w", err)
	}
	return nil
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
	sql, params := client.pointsQuery(canonical, snapshotAt, cursor)
	payload, err := client.execute(ctx, sql, params)
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
	sql, params := client.snapshotQuery(query)
	payload, err := client.execute(ctx, sql, params)
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

func (client *Client) execute(ctx context.Context, sql string, params url.Values) ([]byte, error) {
	payload, err := client.transportClient().Query(ctx, clickhousehttp.QueryRequest{
		Method:           http.MethodPost,
		SQL:              sql,
		Params:           params,
		Accept:           "application/x-ndjson",
		CompressResponse: clickhousehttp.IsSelectQuery(sql),
		MaxResponseBytes: maximumResponseBodySize,
		PrepareHeader:    observability.InjectHTTP,
	})
	if err != nil {
		var tooLargeErr *clickhousehttp.ResponseTooLargeError
		if errors.As(err, &tooLargeErr) {
			return nil, errors.New("ClickHouse history response exceeds 8 MiB")
		}
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return nil, &clickHouseQueryError{statusCode: statusErr.StatusCode, body: statusErr.Body}
		}
		return nil, fmt.Errorf("query ClickHouse history: %w", err)
	}
	return payload, nil
}

type clickHouseQueryError struct {
	statusCode int
	body       string
}

func (err *clickHouseQueryError) Error() string {
	return fmt.Sprintf("ClickHouse history query failed with status %d", err.statusCode)
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

func baseHistoryParams(tenantID, siteID, deviceID string, keys []string, from, to time.Time) url.Values {
	return url.Values{
		"param_tenant_id": {tenantID},
		"param_site_id":   {siteID},
		"param_device_id": {deviceID},
		"param_keys":      {clickHouseStringArray(keys)},
		"param_from_ts":   {formatClickHouseTime(from)},
		"param_to_ts":     {formatClickHouseTime(to)},
	}
}

func (client *Client) snapshotQuery(query telemetryhistorymodel.DeviceHistoryQuery) (string, url.Values) {
	sql := fmt.Sprintf(`WITH now64(3) AS snapshot_cutoff
SELECT
  formatDateTime(snapshot_cutoff, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS snapshot_at,
  if(count() = 0, CAST(NULL, 'Nullable(String)'), formatDateTime(max(projected_at), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC')) AS projection_watermark
FROM %s.%s
WHERE tenant_id = toUUID({tenant_id:UUID})
  AND site_id = toUUID({site_id:UUID})
  AND device_id = toUUID({device_id:UUID})
  AND telemetry_key IN {keys:Array(String)}
  AND sampled_at >= parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')
  AND sampled_at < parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')
  AND projected_at < snapshot_cutoff
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_id IS NOT NULL
  AND point_revision IS NOT NULL
  AND value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')
  AND value_json IS NOT NULL
FORMAT JSONEachRow`, client.database, client.table)
	return sql, baseHistoryParams(query.TenantID, query.SiteID, query.DeviceID, query.Keys, query.From, query.To)
}

func (client *Client) pointsQuery(query telemetryhistorymodel.DeviceHistoryQuery, snapshotAt time.Time, cursor *historyCursor) (string, url.Values) {
	params := baseHistoryParams(query.TenantID, query.SiteID, query.DeviceID, query.Keys, query.From, query.To)
	params.Set("param_snapshot_ts", formatClickHouseTime(snapshotAt))
	params.Set("param_limit", strconv.Itoa(query.PageSize+1))
	cursorPredicate := ""
	if cursor != nil {
		params.Set("param_cursor_key", cursor.LastTelemetryKey)
		params.Set("param_cursor_sampled_at", cursor.LastSampledAt)
		params.Set("param_cursor_observation_id", cursor.LastObservationID)
		cursorPredicate = `
  AND (telemetry_key > {cursor_key:String}
    OR (telemetry_key = {cursor_key:String} AND history_source.sampled_at > parseDateTime64BestEffort({cursor_sampled_at:String}, 3, 'UTC'))
    OR (telemetry_key = {cursor_key:String} AND history_source.sampled_at = parseDateTime64BestEffort({cursor_sampled_at:String}, 3, 'UTC') AND toString(observation_id) > {cursor_observation_id:String}))`
	}
	sql := fmt.Sprintf(`SELECT
  toString(observation_id) AS observation_id,
  toString(assumeNotNull(point_id)) AS point_id,
  if(sensor_id IS NULL, CAST(NULL, 'Nullable(String)'), toString(sensor_id)) AS sensor_id,
  telemetry_key,
  assumeNotNull(point_type) AS point_type,
  assumeNotNull(point_revision) AS point_revision,
  formatDateTime(history_source.sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS sampled_at,
  formatDateTime(history_source.received_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS received_at,
  acceptance_status,
  assumeNotNull(value_type) AS value_type,
  assumeNotNull(value_json) AS value_json,
  unit,
  quality,
  quality_reasons,
  toString(source_event_id) AS source_event_id,
  source_partition,
  source_offset
FROM %s.%s AS history_source
WHERE tenant_id = toUUID({tenant_id:UUID})
  AND site_id = toUUID({site_id:UUID})
  AND device_id = toUUID({device_id:UUID})
  AND telemetry_key IN {keys:Array(String)}
  AND history_source.sampled_at >= parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')
  AND history_source.sampled_at < parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')
  AND projected_at < parseDateTime64BestEffort({snapshot_ts:String}, 3, 'UTC')
  AND acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND point_id IS NOT NULL
  AND point_type IS NOT NULL
  AND point_revision IS NOT NULL
  AND value_type IN ('NUMBER', 'STRING', 'BOOLEAN', 'JSON')
  AND value_json IS NOT NULL%s
ORDER BY telemetry_key, history_source.sampled_at, toString(observation_id)
LIMIT {limit:UInt64}
FORMAT JSONEachRow`, client.database, client.table, cursorPredicate)
	return sql, params
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
	if !uuidv7.IsValidAnyCase(cursor.LastObservationID) {
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

func clickHouseStringArray(values []string) string {
	ordered := append([]string(nil), values...)
	sort.Strings(ordered)
	replacer := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	quoted := make([]string, len(ordered))
	for index, value := range ordered {
		quoted[index] = "'" + replacer.Replace(value) + "'"
	}
	return "[" + strings.Join(quoted, ",") + "]"
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
