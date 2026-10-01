package forecast

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

const maximumForecastHistoryResponseBytes = int64(8 << 20)

type HistoryConfig struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type ClickHouseHistoryReader struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func NewClickHouseHistoryReader(config HistoryConfig) (*ClickHouseHistoryReader, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("forecast history ClickHouse URL must be an HTTP(S) origin")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ClickHouseHistoryReader{baseURL: parsed, username: strings.TrimSpace(config.Username), password: config.Password, client: client}, nil
}

func (reader *ClickHouseHistoryReader) ReadMetricSeries(ctx context.Context, query MetricHistoryQuery) ([]MetricFact, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return nil, errors.New("forecast history reader is unavailable")
	}
	for field, value := range map[string]string{
		"tenantId": query.TenantID, "siteId": query.SiteID, "subjectId": query.SubjectID, "metricVersionId": query.MetricVersionID,
	} {
		if !uuidPattern.MatchString(value) {
			return nil, fmt.Errorf("%s must be a UUID", field)
		}
	}
	if query.SubjectType != "SITE" && query.SubjectType != "ENERGY_NODE" {
		return nil, errors.New("metric history subjectType must be SITE or ENERGY_NODE")
	}
	if query.From.IsZero() || !query.To.After(query.From) {
		return nil, errors.New("metric history requires a valid half-open time window")
	}

	sql := fmt.Sprintf(`SELECT
 toString(fact.result_id) AS result_id,
 formatDateTime(fact.period_start,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS period_start,
 formatDateTime(fact.period_end,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS period_end,
 formatDateTime(fact.calculated_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS calculated_at,
 assumeNotNull(fact.value_number) AS value,
 fact.unit,fact.quality,fact.completeness,fact.revision
FROM analytics.metric_result_facts AS fact
WHERE fact.tenant_id=toUUID('%s')
  AND fact.site_id=toUUID('%s')
  AND fact.subject_type='%s'
  AND fact.subject_id=toUUID('%s')
  AND fact.metric_version_id=toUUID('%s')
  AND fact.period_end >= parseDateTime64BestEffort('%s',3,'UTC')
  AND fact.period_end < parseDateTime64BestEffort('%s',3,'UTC')
  AND fact.value_type='NUMBER'
  AND fact.value_number IS NOT NULL
ORDER BY fact.period_end ASC,fact.revision DESC,fact.calculated_at DESC,toString(fact.result_id) ASC
FORMAT JSONEachRow`, query.TenantID, query.SiteID, query.SubjectType, query.SubjectID, query.MetricVersionID, formatForecastHistoryTime(query.From), formatForecastHistoryTime(query.To))

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reader.baseURL.String(), strings.NewReader(sql))
	if err != nil {
		return nil, fmt.Errorf("build forecast history request: %w", err)
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Accept", "application/x-ndjson")
	if reader.username != "" {
		request.SetBasicAuth(reader.username, reader.password)
	}
	response, err := reader.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query authoritative Forecast metric history: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumForecastHistoryResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read authoritative Forecast metric history: %w", err)
	}
	if int64(len(payload)) > maximumForecastHistoryResponseBytes {
		return nil, errors.New("authoritative Forecast metric history response exceeds 8 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("authoritative Forecast metric history returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}

	type row struct {
		ResultID     string  `json:"result_id"`
		PeriodStart  string  `json:"period_start"`
		PeriodEnd    string  `json:"period_end"`
		CalculatedAt string  `json:"calculated_at"`
		Value        float64 `json:"value"`
		Unit         string  `json:"unit"`
		Quality      string  `json:"quality"`
		Completeness float64 `json:"completeness"`
		Revision     uint64  `json:"revision"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	facts := make([]MetricFact, 0)
	for {
		var value row
		if err = decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode authoritative Forecast metric history: %w", err)
		}
		periodStart, parseErr := time.Parse(time.RFC3339Nano, value.PeriodStart)
		if parseErr != nil {
			return nil, fmt.Errorf("decode metric period_start: %w", parseErr)
		}
		periodEnd, parseErr := time.Parse(time.RFC3339Nano, value.PeriodEnd)
		if parseErr != nil {
			return nil, fmt.Errorf("decode metric period_end: %w", parseErr)
		}
		calculatedAt, parseErr := time.Parse(time.RFC3339Nano, value.CalculatedAt)
		if parseErr != nil {
			return nil, fmt.Errorf("decode metric calculated_at: %w", parseErr)
		}
		facts = append(facts, MetricFact{
			ResultID: value.ResultID, PeriodStart: periodStart.UTC(), PeriodEnd: periodEnd.UTC(), CalculatedAt: calculatedAt.UTC(),
			Value: value.Value, Unit: value.Unit, Quality: value.Quality, Completeness: value.Completeness, Revision: value.Revision,
		})
	}
	return facts, nil
}

func formatForecastHistoryTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
