package history

import (
	"bytes"
	"context"
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

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

const maximumResponseBodySize = int64(8 << 20)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type Engine interface {
	QueryDeviceHistory(context.Context, telemetryhistorymodel.DeviceHistoryQuery) (telemetryhistorymodel.DeviceHistoryResponse, error)
}

type Config struct {
	BaseURL         string
	Database        string
	Table           string
	Username        string
	Password        string
	DatasetRevision string
	HTTPClient      *http.Client
}

type Client struct {
	endpoint        *url.URL
	database        string
	table           string
	username        string
	password        string
	datasetRevision string
	httpClient      *http.Client
}

type pointRow struct {
	ObservationID  string   `json:"observation_id"`
	TelemetryKey   string   `json:"telemetry_key"`
	SampledAt      string   `json:"sampled_at"`
	ReceivedAt     string   `json:"received_at"`
	Value          float64  `json:"value"`
	Unit           *string  `json:"unit"`
	Quality        string   `json:"quality"`
	QualityReasons []string `json:"quality_reasons"`
	Revision       uint64   `json:"revision"`
	TotalCount     uint64   `json:"total_count"`
}

type metadataRow struct {
	DataWatermark   *string `json:"data_watermark"`
	MaximumRevision uint64  `json:"maximum_revision"`
}

func NewClient(config Config) (*Client, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("ClickHouse history base URL must be an HTTP(S) origin")
	}
	if !identifierPattern.MatchString(config.Database) || !identifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse history identifiers are invalid")
	}
	if strings.TrimSpace(config.DatasetRevision) == "" || len(config.DatasetRevision) > 96 {
		return nil, errors.New("ClickHouse history dataset revision is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Client{
		endpoint: endpoint, database: config.Database, table: config.Table,
		username: strings.TrimSpace(config.Username), password: config.Password,
		datasetRevision: strings.TrimSpace(config.DatasetRevision), httpClient: client,
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
	pointPayload, err := client.execute(ctx, client.pointsQuery(canonical))
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, err
	}
	metadataPayload, err := client.execute(ctx, client.metadataQuery(canonical))
	if err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, err
	}
	return client.buildResponse(canonical, pointPayload, metadataPayload)
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

func (client *Client) buildResponse(query telemetryhistorymodel.DeviceHistoryQuery, pointPayload, metadataPayload []byte) (telemetryhistorymodel.DeviceHistoryResponse, error) {
	series := make(map[string][]telemetryhistorymodel.DeviceHistoryPoint, len(query.Keys))
	totalCounts := make(map[string]uint64, len(query.Keys))
	for _, key := range query.Keys {
		series[key] = []telemetryhistorymodel.DeviceHistoryPoint{}
	}
	decoder := json.NewDecoder(bytes.NewReader(pointPayload))
	returned := 0
	for {
		var row pointRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode ClickHouse history point: %w", err)
		}
		if _, expected := series[row.TelemetryKey]; !expected {
			return telemetryhistorymodel.DeviceHistoryResponse{}, errors.New("ClickHouse history returned an unrequested key")
		}
		sampledAt, err := parseClickHouseTime(row.SampledAt)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode ClickHouse history sampled time: %w", err)
		}
		receivedAt, err := parseClickHouseTime(row.ReceivedAt)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode ClickHouse history received time: %w", err)
		}
		series[row.TelemetryKey] = append(series[row.TelemetryKey], telemetryhistorymodel.DeviceHistoryPoint{
			ObservationID: row.ObservationID, SampledAt: sampledAt, ReceivedAt: receivedAt,
			Value: row.Value, Unit: row.Unit, Quality: telemetryhistorymodel.Quality(row.Quality),
			QualityReasons: append([]string{}, row.QualityReasons...), Revision: row.Revision,
		})
		totalCounts[row.TelemetryKey] = row.TotalCount
		returned++
	}
	var metadata metadataRow
	metadataDecoder := json.NewDecoder(bytes.NewReader(metadataPayload))
	if err := metadataDecoder.Decode(&metadata); err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode ClickHouse history metadata: %w", err)
	}
	var watermark *time.Time
	if metadata.DataWatermark != nil && strings.TrimSpace(*metadata.DataWatermark) != "" {
		parsed, err := parseClickHouseTime(*metadata.DataWatermark)
		if err != nil {
			return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("decode ClickHouse history watermark: %w", err)
		}
		watermark = &parsed
	}
	truncated := make([]string, 0)
	responseSeries := make([]telemetryhistorymodel.DeviceHistorySeries, 0, len(query.Keys))
	partial := watermark == nil || watermark.Before(query.To)
	for _, key := range query.Keys {
		points := series[key]
		if len(points) == 0 {
			partial = true
		}
		if totalCounts[key] > uint64(query.MaxPointsPerKey) {
			truncated = append(truncated, key)
			partial = true
		}
		responseSeries = append(responseSeries, telemetryhistorymodel.DeviceHistorySeries{Key: key, Points: points})
	}
	revisionSuffix := "empty"
	if metadata.MaximumRevision > 0 {
		revisionSuffix = strconv.FormatUint(metadata.MaximumRevision, 10)
	}
	response := telemetryhistorymodel.DeviceHistoryResponse{
		SchemaVersion: 1, OwningOrganizationID: query.OwningOrganizationID, SiteID: query.SiteID, DeviceID: query.DeviceID,
		Series: responseSeries,
		Metadata: telemetryhistorymodel.DeviceHistoryMetadata{
			RequestedFrom: query.From, RequestedTo: query.To, DataWatermark: watermark,
			DatasetRevision: client.datasetRevision + ":" + revisionSuffix, Partial: partial,
			MaxPointsPerKey: query.MaxPointsPerKey, ReturnedPoints: returned, TruncatedKeys: truncated,
		},
	}
	if err := response.ValidateFor(query); err != nil {
		return telemetryhistorymodel.DeviceHistoryResponse{}, fmt.Errorf("validate ClickHouse history response: %w", err)
	}
	return response, nil
}

func (client *Client) pointsQuery(query telemetryhistorymodel.DeviceHistoryQuery) string {
	return fmt.Sprintf(`WITH scoped AS (
  SELECT
    observation_id,
    telemetry_key,
    sampled_at,
    received_at,
    value_number,
    unit,
    quality,
    quality_reasons,
    source_offset,
    row_number() OVER (PARTITION BY telemetry_key ORDER BY sampled_at DESC, source_offset DESC, observation_id DESC) AS row_number,
    count() OVER (PARTITION BY telemetry_key) AS total_count
  FROM %s.%s
  WHERE owning_organization_id = toUUID('%s')
    AND site_id = toUUID('%s')
    AND device_id = toUUID('%s')
    AND telemetry_key IN (%s)
    AND sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
    AND sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
    AND acceptance_status = 'ACCEPTED'
    AND value_number IS NOT NULL
    AND isFinite(value_number)
)
SELECT
  toString(observation_id) AS observation_id,
  telemetry_key,
  formatDateTime(sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS sampled_at,
  formatDateTime(received_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS received_at,
  assumeNotNull(value_number) AS value,
  unit,
  quality,
  quality_reasons,
  source_offset AS revision,
  total_count
FROM scoped
WHERE row_number <= %d
ORDER BY telemetry_key, sampled_at, source_offset, observation_id
FORMAT JSONEachRow`, client.database, client.table, query.OwningOrganizationID, query.SiteID, query.DeviceID, quotedKeys(query.Keys), formatClickHouseTime(query.From), formatClickHouseTime(query.To), query.MaxPointsPerKey)
}

func (client *Client) metadataQuery(query telemetryhistorymodel.DeviceHistoryQuery) string {
	return fmt.Sprintf(`SELECT
  if(count() = 0, CAST(NULL, 'Nullable(String)'), formatDateTime(max(sampled_at), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC')) AS data_watermark,
  maxOrDefault(source_offset) AS maximum_revision
FROM %s.%s
WHERE owning_organization_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND device_id = toUUID('%s')
  AND telemetry_key IN (%s)
  AND acceptance_status = 'ACCEPTED'
  AND value_number IS NOT NULL
  AND isFinite(value_number)
FORMAT JSONEachRow`, client.database, client.table, query.OwningOrganizationID, query.SiteID, query.DeviceID, quotedKeys(query.Keys))
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
