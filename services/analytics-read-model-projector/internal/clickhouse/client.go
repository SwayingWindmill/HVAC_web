package clickhouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/energy"
)

const (
	maximumBatchSize        = 4096
	maximumResponseBodySize = int64(8 << 20)
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type ReaderConfig struct {
	BaseURL           string
	SourceDatabase    string
	SourceTable       string
	AnalyticsDatabase string
	AnalyticsTable    string
	Username          string
	Password          string
	HTTPClient        *http.Client
}

type Reader struct {
	endpoint          *url.URL
	sourceDatabase    string
	sourceTable       string
	analyticsDatabase string
	analyticsTable    string
	username          string
	password          string
	httpClient        *http.Client
}

type WriterConfig struct {
	BaseURL    string
	Database   string
	Table      string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type Writer struct {
	endpoint   *url.URL
	database   string
	table      string
	username   string
	password   string
	httpClient *http.Client
}

type counterDeltaRow struct {
	PreviousObservationID  string   `json:"previous_observation_id"`
	CurrentObservationID   string   `json:"current_observation_id"`
	TenantID               string   `json:"tenant_id"`
	SiteID                 string   `json:"site_id"`
	DeviceID               string   `json:"device_id"`
	PointID                string   `json:"point_id"`
	SensorID               *string  `json:"sensor_id"`
	TelemetryKey           string   `json:"telemetry_key"`
	PointRevision          uint64   `json:"point_revision"`
	Unit                   string   `json:"unit"`
	CounterDecreaseMode    string   `json:"counter_decrease_mode"`
	CounterRolloverModulus *float64 `json:"counter_rollover_modulus"`
	PreviousValue          float64  `json:"previous_value"`
	PreviousQuality        string   `json:"previous_quality"`
	PreviousQualityReasons []string `json:"previous_quality_reasons"`
	PreviousSampledAt      string   `json:"previous_sampled_at"`
	CurrentSampledAt       string   `json:"current_sampled_at"`
	CurrentReceivedAt      string   `json:"current_received_at"`
	CurrentQuality         string   `json:"current_quality"`
	CurrentQualityReasons  []string `json:"current_quality_reasons"`
	CurrentSourceEventID   string   `json:"source_event_id"`
	CurrentSourcePartition string   `json:"source_partition"`
	CurrentSourceOffset    uint64   `json:"source_offset"`
	TransitionType         string   `json:"transition_type"`
	DeltaValue             *float64 `json:"delta_value"`
}

func NewReader(config ReaderConfig) (*Reader, error) {
	endpoint, client, err := validateClientConfig(config.BaseURL, config.HTTPClient)
	if err != nil {
		return nil, err
	}
	for _, identifier := range []string{config.SourceDatabase, config.SourceTable, config.AnalyticsDatabase, config.AnalyticsTable} {
		if !identifierPattern.MatchString(identifier) {
			return nil, errors.New("ClickHouse analytics reader identifiers are invalid")
		}
	}
	return &Reader{
		endpoint: endpoint, sourceDatabase: config.SourceDatabase, sourceTable: config.SourceTable,
		analyticsDatabase: config.AnalyticsDatabase, analyticsTable: config.AnalyticsTable,
		username: strings.TrimSpace(config.Username), password: config.Password, httpClient: client,
	}, nil
}

func NewWriter(config WriterConfig) (*Writer, error) {
	endpoint, client, err := validateClientConfig(config.BaseURL, config.HTTPClient)
	if err != nil {
		return nil, err
	}
	if !identifierPattern.MatchString(config.Database) || !identifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse analytics writer identifiers are invalid")
	}
	return &Writer{
		endpoint: endpoint, database: config.Database, table: config.Table,
		username: strings.TrimSpace(config.Username), password: config.Password, httpClient: client,
	}, nil
}

func validateClientConfig(rawURL string, client *http.Client) (*url.URL, *http.Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, nil, errors.New("ClickHouse analytics base URL must be an HTTP(S) origin without user info, path, query or fragment")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return parsed, client, nil
}

func (reader *Reader) ListDeltas(ctx context.Context, limit int) ([]energy.CounterDelta, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	if limit < 1 || limit > maximumBatchSize {
		return nil, errors.New("ClickHouse counter delta limit must be between 1 and 4096")
	}
	query := reader.counterDeltaQuery(limit)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reader.endpoint.String(), strings.NewReader(query))
	if err != nil {
		return nil, fmt.Errorf("create ClickHouse analytics read request: %w", err)
	}
	request.Header.Set("Accept", "application/x-ndjson")
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if reader.username != "" {
		request.SetBasicAuth(reader.username, reader.password)
	}
	observability.InjectHTTP(ctx, request.Header)
	response, err := reader.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse analytics candidates: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read ClickHouse analytics candidate response: %w", err)
	}
	if int64(len(payload)) > maximumResponseBodySize {
		return nil, errors.New("ClickHouse counter delta response exceeds 8 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, clickHouseStatusError("counter delta query", response.StatusCode, payload)
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	deltas := make([]energy.CounterDelta, 0)
	for {
		var row counterDeltaRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse counter delta: %w", err)
		}
		previousSampledAt, err := parseClickHouseTime(row.PreviousSampledAt)
		if err != nil {
			return nil, fmt.Errorf("decode previous counter sample time: %w", err)
		}
		currentSampledAt, err := parseClickHouseTime(row.CurrentSampledAt)
		if err != nil {
			return nil, fmt.Errorf("decode current counter sample time: %w", err)
		}
		currentReceivedAt, err := parseClickHouseTime(row.CurrentReceivedAt)
		if err != nil {
			return nil, fmt.Errorf("decode current counter receive time: %w", err)
		}
		sensorID := ""
		if row.SensorID != nil {
			sensorID = *row.SensorID
		}
		deltas = append(deltas, energy.CounterDelta{
			PreviousObservationID: row.PreviousObservationID, CurrentObservationID: row.CurrentObservationID,
			TenantID: row.TenantID, SiteID: row.SiteID, DeviceID: row.DeviceID,
			PointID: row.PointID, SensorID: sensorID, TelemetryKey: row.TelemetryKey,
			PointRevision: row.PointRevision, Unit: row.Unit, CounterDecreaseMode: row.CounterDecreaseMode,
			CounterRolloverModulus: row.CounterRolloverModulus,
			PreviousValue:          row.PreviousValue, PreviousQuality: row.PreviousQuality,
			PreviousQualityReasons: append([]string(nil), row.PreviousQualityReasons...), PreviousSampledAt: previousSampledAt,
			CurrentSampledAt: currentSampledAt, CurrentReceivedAt: currentReceivedAt,
			CurrentQuality: row.CurrentQuality, CurrentQualityReasons: append([]string(nil), row.CurrentQualityReasons...),
			CurrentSourceEventID: row.CurrentSourceEventID, CurrentSourcePartition: row.CurrentSourcePartition,
			CurrentSourceOffset: row.CurrentSourceOffset, TransitionType: energy.TransitionType(row.TransitionType), DeltaValue: row.DeltaValue,
		})
	}
	return deltas, nil
}

func (reader *Reader) counterDeltaQuery(limit int) string {
	return fmt.Sprintf(`SELECT
  toString(delta.previous_observation_id) AS previous_observation_id,
  toString(delta.observation_id) AS current_observation_id,
  toString(delta.tenant_id) AS tenant_id,
  toString(delta.site_id) AS site_id,
  toString(delta.device_id) AS device_id,
  toString(delta.point_id) AS point_id,
  ifNull(toString(delta.sensor_id), '') AS sensor_id,
  delta.telemetry_key,
  delta.point_revision,
  delta.unit,
  delta.counter_decrease_mode,
  delta.counter_rollover_modulus,
  delta.previous_value,
  delta.previous_quality,
  delta.previous_quality_reasons,
  formatDateTime(delta.previous_sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS previous_sampled_at,
  formatDateTime(delta.sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS current_sampled_at,
  formatDateTime(delta.received_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS current_received_at,
  delta.quality AS current_quality,
  delta.quality_reasons AS current_quality_reasons,
  toString(delta.source_event_id) AS source_event_id,
  delta.source_partition,
  delta.source_offset,
  delta.transition_type,
  delta.delta_value
FROM %[1]s.%[2]s AS delta
LEFT ANTI JOIN %[3]s.%[4]s AS fact
  ON fact.tenant_id = delta.tenant_id
 AND fact.site_id = delta.site_id
 AND fact.source_current_observation_id = delta.observation_id
 AND fact.source_previous_observation_id = delta.previous_observation_id
WHERE delta.transition_type IN ('INCREASE', 'UNCHANGED', 'RECOVERY', 'RESET', 'ROLLOVER')
  AND delta.delta_value IS NOT NULL
ORDER BY delta.sampled_at, delta.source_offset, delta.observation_id
LIMIT %[5]d
FORMAT JSONEachRow`, reader.sourceDatabase, reader.sourceTable, reader.analyticsDatabase, reader.analyticsTable, limit)
}

func (writer *Writer) InsertFacts(ctx context.Context, facts []energy.EnergyIntervalFact) error {
	if writer == nil || writer.endpoint == nil || writer.httpClient == nil {
		return errors.New("ClickHouse analytics writer is closed")
	}
	if len(facts) == 0 {
		return nil
	}
	if len(facts) > maximumBatchSize {
		return errors.New("ClickHouse analytics fact batch exceeds 4096")
	}
	ordered := append([]energy.EnergyIntervalFact(nil), facts...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].LogicalKey() < ordered[right].LogicalKey()
	})
	var body bytes.Buffer
	digest := sha256.New()
	for _, fact := range ordered {
		if err := validateFact(fact); err != nil {
			return err
		}
		encoded, err := json.Marshal(fact)
		if err != nil {
			return fmt.Errorf("encode energy interval fact %s: %w", fact.FactID, err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
		digest.Write([]byte(fact.LogicalKey()))
		digest.Write([]byte{'\n'})
	}

	endpoint := *writer.endpoint
	query := endpoint.Query()
	query.Set("query", "INSERT INTO "+writer.database+"."+writer.table+" FORMAT JSONEachRow")
	query.Set("date_time_input_format", "best_effort")
	query.Set("insert_deduplication_token", hex.EncodeToString(digest.Sum(nil)))
	query.Set("async_insert", "1")
	query.Set("wait_for_async_insert", "1")
	query.Set("async_insert_deduplicate", "1")
	query.Set("wait_end_of_query", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return fmt.Errorf("create ClickHouse analytics insert request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	if writer.username != "" {
		request.SetBasicAuth(writer.username, writer.password)
	}
	observability.InjectHTTP(ctx, request.Header)
	response, err := writer.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("insert ClickHouse energy interval facts: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if err != nil {
		return fmt.Errorf("read ClickHouse analytics insert response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return clickHouseStatusError("fact insert", response.StatusCode, payload)
	}
	return nil
}

func validateFact(fact energy.EnergyIntervalFact) error {
	if strings.TrimSpace(fact.FactID) == "" || strings.TrimSpace(fact.TenantID) == "" || strings.TrimSpace(fact.SiteID) == "" ||
		strings.TrimSpace(fact.MeterID) == "" || strings.TrimSpace(fact.MeterBindingID) == "" || strings.TrimSpace(fact.DeviceID) == "" ||
		strings.TrimSpace(fact.PointID) == "" || strings.TrimSpace(fact.PreviousObservationID) == "" || strings.TrimSpace(fact.CurrentObservationID) == "" {
		return errors.New("ClickHouse energy interval fact identifiers are required")
	}
	if fact.EnergyType != energy.EnergyTypeElectricity || fact.MeterRole != energy.MeterRolePrimary || fact.EnergyKWh < 0 ||
		fact.PeriodStart.IsZero() || fact.PeriodEnd.IsZero() || !fact.PeriodStart.Before(fact.PeriodEnd) || fact.ProjectedAt.IsZero() ||
		fact.DatasetRevision != fact.SourceOffset || fact.DataWatermark.IsZero() || !fact.DataWatermark.Equal(fact.PeriodEnd) ||
		fact.FactID != fact.CurrentObservationID || !fact.TransitionType.ProducesFact() {
		return errors.New("ClickHouse energy interval fact is invalid")
	}
	switch fact.Quality {
	case energy.FactQualityValid, energy.FactQualitySuspect, energy.FactQualityInvalid:
		return nil
	default:
		return errors.New("ClickHouse energy interval quality is invalid")
	}
}

func parseClickHouseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unsupported ClickHouse time format")
}

func clickHouseStatusError(operation string, status int, payload []byte) error {
	message := strings.TrimSpace(string(payload))
	if len(message) > 512 {
		message = message[:512]
	}
	return fmt.Errorf("ClickHouse analytics %s returned %d: %s", operation, status, message)
}

var (
	_ energy.CounterSource = (*Reader)(nil)
	_ energy.FactSink      = (*Writer)(nil)
)
