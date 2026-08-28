package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumOptimizationInputResponseBytes = int64(1 << 20)

type InputReaderConfig struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type ClickHouseInputReader struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func NewClickHouseInputReader(config InputReaderConfig) (*ClickHouseInputReader, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("optimization input ClickHouse URL must be an HTTP(S) origin")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ClickHouseInputReader{baseURL: parsed, username: strings.TrimSpace(config.Username), password: config.Password, client: client}, nil
}

func (reader *ClickHouseInputReader) ReadOptimizationState(ctx context.Context, query OptimizationStateQuery) (AuthoritativeState, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return AuthoritativeState{}, errors.New("optimization authoritative input reader is unavailable")
	}
	for field, value := range map[string]string{"tenantId": query.TenantID, "siteId": query.SiteID, "subjectId": query.SubjectID} {
		if !uuidPattern.MatchString(value) {
			return AuthoritativeState{}, fmt.Errorf("%s must be a UUID", field)
		}
	}
	if query.SubjectType != "SITE" || query.SubjectID != query.SiteID || query.At.IsZero() || query.SupplyTemperatureKey == "" || query.ZoneTemperatureKey == "" {
		return AuthoritativeState{}, errors.New("optimization state query is invalid")
	}
	dailyEnergy, err := reader.readMetric(ctx, query, "daily_energy")
	if err != nil {
		return AuthoritativeState{}, err
	}
	dailyCost, err := reader.readMetric(ctx, query, "energy_cost")
	if err != nil {
		return AuthoritativeState{}, err
	}
	supplyTemperature, err := reader.readTelemetry(ctx, query, query.SupplyTemperatureKey)
	if err != nil {
		return AuthoritativeState{}, err
	}
	zoneTemperature, err := reader.readTelemetry(ctx, query, query.ZoneTemperatureKey)
	if err != nil {
		return AuthoritativeState{}, err
	}
	return AuthoritativeState{
		DailyEnergy: dailyEnergy, DailyCost: dailyCost,
		SupplyTemperature: supplyTemperature, ZoneTemperature: zoneTemperature,
	}, nil
}

func (reader *ClickHouseInputReader) readMetric(ctx context.Context, query OptimizationStateQuery, metricCode string) (MetricEvidence, error) {
	sql := fmt.Sprintf(`SELECT
 toString(fact.result_id) AS result_id,
 toString(fact.metric_version_id) AS metric_version_id,
 fact.metric_code,
 formatDateTime(fact.period_start,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS period_start,
 formatDateTime(fact.period_end,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS period_end,
 formatDateTime(fact.calculated_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS calculated_at,
 assumeNotNull(fact.value_number) AS value,
 fact.unit,fact.quality,fact.revision
FROM analytics.metric_result_facts AS fact
WHERE fact.tenant_id=toUUID('%s')
  AND fact.site_id=toUUID('%s')
  AND fact.subject_type='SITE'
  AND fact.subject_id=toUUID('%s')
  AND fact.metric_code='%s'
  AND fact.period_end <= parseDateTime64BestEffort('%s',3,'UTC')
  AND fact.calculated_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND fact.value_type='NUMBER'
  AND fact.value_number IS NOT NULL
ORDER BY fact.period_end DESC,fact.revision DESC,fact.calculated_at DESC,toString(fact.result_id) ASC
LIMIT 1
FORMAT JSONEachRow`, query.TenantID, query.SiteID, query.SubjectID, clickHouseString(metricCode), formatOptimizationTime(query.At), formatOptimizationTime(query.At))
	payload, err := reader.query(ctx, sql)
	if err != nil {
		return MetricEvidence{}, fmt.Errorf("query authoritative Optimization metric %s: %w", metricCode, err)
	}
	var row struct {
		ResultID        string  `json:"result_id"`
		MetricVersionID string  `json:"metric_version_id"`
		MetricCode      string  `json:"metric_code"`
		PeriodStart     string  `json:"period_start"`
		PeriodEnd       string  `json:"period_end"`
		CalculatedAt    string  `json:"calculated_at"`
		Value           float64 `json:"value"`
		Unit            string  `json:"unit"`
		Quality         string  `json:"quality"`
		Revision        uint64  `json:"revision"`
	}
	if err = decodeSingleJSONEachRow(payload, &row); err != nil {
		return MetricEvidence{}, fmt.Errorf("decode authoritative Optimization metric %s: %w", metricCode, err)
	}
	periodStart, err := time.Parse(time.RFC3339Nano, row.PeriodStart)
	if err != nil {
		return MetricEvidence{}, err
	}
	periodEnd, err := time.Parse(time.RFC3339Nano, row.PeriodEnd)
	if err != nil {
		return MetricEvidence{}, err
	}
	calculatedAt, err := time.Parse(time.RFC3339Nano, row.CalculatedAt)
	if err != nil {
		return MetricEvidence{}, err
	}
	return MetricEvidence{
		ResultID: row.ResultID, MetricVersionID: row.MetricVersionID, MetricCode: row.MetricCode,
		PeriodStart: periodStart.UTC(), PeriodEnd: periodEnd.UTC(), CalculatedAt: calculatedAt.UTC(),
		Value: row.Value, Unit: row.Unit, Quality: row.Quality, Revision: row.Revision,
	}, nil
}

func (reader *ClickHouseInputReader) readTelemetry(ctx context.Context, query OptimizationStateQuery, telemetryKey string) (TelemetryEvidence, error) {
	sql := fmt.Sprintf(`SELECT
 toString(observation_id) AS observation_id,
 toString(device_id) AS device_id,
 toString(point_id) AS point_id,
 telemetry_key,
 assumeNotNull(point_revision) AS point_revision,
 formatDateTime(sampled_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS sampled_at,
 formatDateTime(received_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS received_at,
 assumeNotNull(value_number) AS value,
 assumeNotNull(unit) AS unit,
 quality,
 toString(source_event_id) AS source_event_id,
 source_partition,
 source_offset
FROM (
 SELECT *,row_number() OVER (PARTITION BY point_id ORDER BY sampled_at DESC,received_at DESC,source_offset DESC,toString(observation_id) ASC) AS point_rank
 FROM telemetry_history.observations AS observation
 WHERE observation.tenant_id=toUUID('%s')
   AND observation.site_id=toUUID('%s')
   AND observation.telemetry_key='%s'
   AND observation.sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND observation.received_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND observation.acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
   AND observation.value_number IS NOT NULL
   AND observation.device_id IS NOT NULL
   AND observation.point_id IS NOT NULL
   AND observation.point_revision IS NOT NULL
   AND observation.unit IS NOT NULL
)
WHERE point_rank=1
ORDER BY sampled_at DESC,received_at DESC,source_offset DESC,toString(observation_id) ASC
LIMIT 2
FORMAT JSONEachRow`, query.TenantID, query.SiteID, clickHouseString(telemetryKey), formatOptimizationTime(query.At), formatOptimizationTime(query.At))
	payload, err := reader.query(ctx, sql)
	if err != nil {
		return TelemetryEvidence{}, fmt.Errorf("query authoritative Optimization telemetry %s: %w", telemetryKey, err)
	}
	type row struct {
		ObservationID   string  `json:"observation_id"`
		DeviceID        string  `json:"device_id"`
		PointID         string  `json:"point_id"`
		TelemetryKey    string  `json:"telemetry_key"`
		PointRevision   uint64  `json:"point_revision"`
		SampledAt       string  `json:"sampled_at"`
		ReceivedAt      string  `json:"received_at"`
		Value           float64 `json:"value"`
		Unit            string  `json:"unit"`
		Quality         string  `json:"quality"`
		SourceEventID   string  `json:"source_event_id"`
		SourcePartition string  `json:"source_partition"`
		SourceOffset    uint64  `json:"source_offset"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	rows := make([]row, 0, 2)
	for {
		var value row
		if err = decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return TelemetryEvidence{}, err
		}
		rows = append(rows, value)
	}
	if len(rows) == 0 {
		return TelemetryEvidence{}, errors.New("authoritative telemetry mapping has no usable observation")
	}
	if len(rows) > 1 {
		return TelemetryEvidence{}, errors.New("authoritative telemetry mapping resolves to multiple Points")
	}
	sampledAt, err := time.Parse(time.RFC3339Nano, rows[0].SampledAt)
	if err != nil {
		return TelemetryEvidence{}, err
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, rows[0].ReceivedAt)
	if err != nil {
		return TelemetryEvidence{}, err
	}
	return TelemetryEvidence{
		ObservationID: rows[0].ObservationID, DeviceID: rows[0].DeviceID, PointID: rows[0].PointID,
		TelemetryKey: rows[0].TelemetryKey, PointRevision: rows[0].PointRevision,
		SampledAt: sampledAt.UTC(), ReceivedAt: receivedAt.UTC(), Value: rows[0].Value, Unit: rows[0].Unit, Quality: rows[0].Quality,
		SourceEventID: rows[0].SourceEventID, SourcePartition: rows[0].SourcePartition, SourceOffset: rows[0].SourceOffset,
	}, nil
}

func (reader *ClickHouseInputReader) query(ctx context.Context, sql string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reader.baseURL.String(), strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Accept", "application/x-ndjson")
	if reader.username != "" {
		request.SetBasicAuth(reader.username, reader.password)
	}
	response, err := reader.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumOptimizationInputResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximumOptimizationInputResponseBytes {
		return nil, errors.New("authoritative Optimization input response exceeds 1 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("ClickHouse returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func decodeSingleJSONEachRow(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("authoritative input is missing")
		}
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("authoritative input returned multiple rows")
	}
	return nil
}

func clickHouseString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

func formatOptimizationTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
