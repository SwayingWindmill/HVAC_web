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

type candidateRow struct {
	PreviousObservationID  string   `json:"previous_observation_id"`
	CurrentObservationID   string   `json:"current_observation_id"`
	OrganizationID         string   `json:"organization_id"`
	SiteID                 string   `json:"site_id"`
	DeviceID               string   `json:"device_id"`
	TelemetryKey           string   `json:"telemetry_key"`
	PreviousValue          float64  `json:"previous_value"`
	CurrentValue           float64  `json:"current_value"`
	PreviousQuality        string   `json:"previous_quality"`
	CurrentQuality         string   `json:"current_quality"`
	PreviousQualityReasons []string `json:"previous_quality_reasons"`
	CurrentQualityReasons  []string `json:"current_quality_reasons"`
	PreviousSampledAt      string   `json:"previous_sampled_at"`
	CurrentSampledAt       string   `json:"current_sampled_at"`
	SourceOffset           uint64   `json:"source_offset"`
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

func (reader *Reader) ListCandidates(ctx context.Context, limit int) ([]energy.Candidate, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	if limit < 1 || limit > maximumBatchSize {
		return nil, errors.New("ClickHouse analytics candidate limit must be between 1 and 4096")
	}
	query := reader.candidateQuery(limit)
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
		return nil, errors.New("ClickHouse analytics candidate response exceeds 8 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, clickHouseStatusError("candidate query", response.StatusCode, payload)
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	candidates := make([]energy.Candidate, 0)
	for {
		var row candidateRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse analytics candidate: %w", err)
		}
		previousSampledAt, err := parseClickHouseTime(row.PreviousSampledAt)
		if err != nil {
			return nil, fmt.Errorf("decode previous energy sample time: %w", err)
		}
		currentSampledAt, err := parseClickHouseTime(row.CurrentSampledAt)
		if err != nil {
			return nil, fmt.Errorf("decode current energy sample time: %w", err)
		}
		candidates = append(candidates, energy.Candidate{
			PreviousObservationID: row.PreviousObservationID, CurrentObservationID: row.CurrentObservationID,
			OrganizationID: row.OrganizationID, SiteID: row.SiteID, DeviceID: row.DeviceID, TelemetryKey: row.TelemetryKey,
			PreviousValue: row.PreviousValue, CurrentValue: row.CurrentValue,
			PreviousQuality: row.PreviousQuality, CurrentQuality: row.CurrentQuality,
			PreviousQualityReasons: append([]string(nil), row.PreviousQualityReasons...),
			CurrentQualityReasons:  append([]string(nil), row.CurrentQualityReasons...),
			PreviousSampledAt:      previousSampledAt, CurrentSampledAt: currentSampledAt, SourceOffset: row.SourceOffset,
		})
	}
	return candidates, nil
}

func (reader *Reader) candidateQuery(limit int) string {
	window := "PARTITION BY owning_organization_id, site_id, device_id, telemetry_key ORDER BY sampled_at, source_offset, observation_id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW"
	return fmt.Sprintf(`WITH ordered AS (
  SELECT
    observation_id AS current_observation_id,
    lagInFrame(toNullable(observation_id), 1) OVER (%[1]s) AS previous_observation_id,
    assumeNotNull(owning_organization_id) AS organization_id,
    assumeNotNull(site_id) AS site_id,
    assumeNotNull(device_id) AS device_id,
    telemetry_key,
    lagInFrame(value_number, 1) OVER (%[1]s) AS previous_value,
    assumeNotNull(value_number) AS current_value,
    lagInFrame(quality, 1, '') OVER (%[1]s) AS previous_quality,
    quality AS current_quality,
    lagInFrame(quality_reasons, 1, []) OVER (%[1]s) AS previous_quality_reasons,
    quality_reasons AS current_quality_reasons,
    lagInFrame(sampled_at, 1) OVER (%[1]s) AS previous_sampled_at,
    sampled_at AS current_sampled_at,
    projected_at AS current_projected_at,
    source_offset
  FROM %[2]s.%[3]s
  WHERE acceptance_status = 'ACCEPTED'
    AND telemetry_key = 'hvac_meter.energy'
    AND unit = 'kWh'
    AND value_number IS NOT NULL
    AND isFinite(value_number)
    AND owning_organization_id IS NOT NULL
    AND site_id IS NOT NULL
    AND device_id IS NOT NULL
)
SELECT
  toString(candidate.previous_observation_id) AS previous_observation_id,
  toString(candidate.current_observation_id) AS current_observation_id,
  toString(candidate.organization_id) AS organization_id,
  toString(candidate.site_id) AS site_id,
  toString(candidate.device_id) AS device_id,
  candidate.telemetry_key,
  assumeNotNull(candidate.previous_value) AS previous_value,
  candidate.current_value,
  candidate.previous_quality,
  candidate.current_quality,
  candidate.previous_quality_reasons,
  candidate.current_quality_reasons,
  formatDateTime(candidate.previous_sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS previous_sampled_at,
  formatDateTime(candidate.current_sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS current_sampled_at,
  candidate.source_offset
FROM ordered AS candidate
LEFT ANTI JOIN %[4]s.%[5]s AS fact
  ON fact.source_current_observation_id = candidate.current_observation_id
WHERE candidate.previous_observation_id IS NOT NULL
  AND candidate.previous_value IS NOT NULL
ORDER BY candidate.current_projected_at, candidate.current_observation_id
LIMIT %[6]d
FORMAT JSONEachRow`, window, reader.sourceDatabase, reader.sourceTable, reader.analyticsDatabase, reader.analyticsTable, limit)
}

func (writer *Writer) InsertFacts(ctx context.Context, facts []energy.Fact) error {
	if writer == nil || writer.endpoint == nil || writer.httpClient == nil {
		return errors.New("ClickHouse analytics writer is closed")
	}
	if len(facts) == 0 {
		return nil
	}
	if len(facts) > maximumBatchSize {
		return errors.New("ClickHouse analytics fact batch exceeds 4096")
	}
	ordered := append([]energy.Fact(nil), facts...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].SourceCurrentObservationID < ordered[right].SourceCurrentObservationID
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
		digest.Write([]byte(fact.SourceCurrentObservationID))
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

func validateFact(fact energy.Fact) error {
	if strings.TrimSpace(fact.FactID) == "" || strings.TrimSpace(fact.OrganizationID) == "" || strings.TrimSpace(fact.SiteID) == "" ||
		strings.TrimSpace(fact.DeviceID) == "" || strings.TrimSpace(fact.SourcePreviousObservationID) == "" || strings.TrimSpace(fact.SourceCurrentObservationID) == "" {
		return errors.New("ClickHouse energy interval fact identifiers are required")
	}
	if fact.TelemetryKey != energy.CumulativeElectricityTelemetryKey || fact.EnergyType != energy.EnergyTypeElectricity || fact.EnergyKWh < 0 ||
		fact.PeriodStart.IsZero() || fact.PeriodEnd.IsZero() || !fact.PeriodStart.Before(fact.PeriodEnd) || fact.ProjectedAt.IsZero() ||
		fact.ObservationCount != 2 || fact.DatasetRevision == 0 || fact.SourceOffset == 0 || fact.DatasetRevision != fact.SourceOffset ||
		fact.DataWatermark.IsZero() || !fact.DataWatermark.Equal(fact.PeriodEnd) {
		return errors.New("ClickHouse energy interval fact is invalid")
	}
	switch fact.Quality {
	case energy.QualityValid, energy.QualitySuspect, energy.QualityInvalid:
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
	_ energy.Source = (*Reader)(nil)
	_ energy.Sink   = (*Writer)(nil)
)
