package mv

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

const (
	// HourlyEnergyMetric and HourlyCoolingMetric are the released Site Metrics M&V verifies on.
	HourlyEnergyMetric  = "hourly_energy"
	HourlyCoolingMetric = "hourly_cooling"
	maximumHourlyBytes  = int64(4 << 20)
)

// ClickHouseHours reads hourly plant electricity and cooling from released Metric results with
// M&V's own read-only account (ADR 0021).
type ClickHouseHours struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func NewClickHouseHours(baseURL, username, password string, client *http.Client) (*ClickHouseHours, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("M&V ClickHouse URL must be an HTTP(S) origin")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &ClickHouseHours{baseURL: parsed, username: strings.TrimSpace(username), password: password, client: client}, nil
}

// hourlySQL takes each hour's latest revision of both Metrics and keeps hours where both are good.
func hourlySQL(tenantID, siteID string, from, to time.Time) string {
	return fmt.Sprintf(`SELECT
 formatDateTime(period_start,'%%Y-%%m-%%dT%%H:%%i:%%SZ','UTC') AS period_start,
 sumIf(value, metric_code='%[3]s') AS energy_kwh,
 sumIf(value, metric_code='%[4]s') AS cooling_kwh
FROM (
 SELECT metric_code, period_start,
  argMax(assumeNotNull(value_number), (revision, calculated_at)) AS value,
  argMax(quality, (revision, calculated_at)) AS quality
 FROM analytics.metric_result_facts
 WHERE tenant_id=toUUID('%[1]s') AND site_id=toUUID('%[2]s') AND subject_type='SITE' AND subject_id=toUUID('%[2]s')
   AND metric_code IN ('%[3]s','%[4]s')
   AND period_start >= parseDateTime64BestEffort('%[5]s',3,'UTC') AND period_start < parseDateTime64BestEffort('%[6]s',3,'UTC')
   AND period_end - period_start = 3600
   AND value_type='NUMBER' AND value_number IS NOT NULL
 GROUP BY metric_code, period_start
)
WHERE quality='GOOD'
GROUP BY period_start
HAVING count()=2
ORDER BY period_start
FORMAT JSONEachRow`, tenantID, siteID, HourlyEnergyMetric, HourlyCoolingMetric, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
}

func (reader *ClickHouseHours) HourlySamples(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]HourSample, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !from.Before(to) {
		return nil, errors.New("hourly sample query is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reader.baseURL.String(), strings.NewReader(hourlySQL(tenantID, siteID, from, to)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if reader.username != "" {
		request.SetBasicAuth(reader.username, reader.password)
	}
	response, err := reader.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumHourlyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximumHourlyBytes {
		return nil, errors.New("hourly Metric results exceed 4 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ClickHouse returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return decodeHours(payload)
}

func decodeHours(payload []byte) ([]HourSample, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	samples := []HourSample{}
	for {
		var row struct {
			PeriodStart string  `json:"period_start"`
			EnergyKWh   float64 `json:"energy_kwh"`
			CoolingKWh  float64 `json:"cooling_kwh"`
		}
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return samples, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode hourly Metric results: %w", err)
		}
		start, err := time.Parse(time.RFC3339, row.PeriodStart)
		if err != nil {
			return nil, err
		}
		samples = append(samples, HourSample{Start: start, EnergyKWh: row.EnergyKWh, CoolingKWh: row.CoolingKWh})
	}
}
