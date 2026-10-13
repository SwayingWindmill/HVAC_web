package report

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
	"strings"
	"time"
)

const maximumHourBytes = int64(4 << 20)

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	timezonePattern = regexp.MustCompile(`^[A-Za-z0-9_+\-/]+$`)
)

// ClickHouseHours reads a Site's valid energy facts by hour with the report's own read-only
// account, bucketed as the energy series buckets them: by each fact's end on the Site's clock.
type ClickHouseHours struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

func NewClickHouseHours(baseURL, username, password string, client *http.Client) (*ClickHouseHours, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("report ClickHouse URL must be an HTTP(S) origin")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &ClickHouseHours{baseURL: parsed, username: strings.TrimSpace(username), password: password, client: client}, nil
}

// hoursSQL sums the Site's total (SITE) electricity and cooling facts of VALID quality per hour.
func hoursSQL(tenantID, siteID, timezone string, from, to time.Time) string {
	return fmt.Sprintf(`SELECT
 formatDateTime(toStartOfHour(period_end, '%[3]s'),'%%Y-%%m-%%dT%%H:%%i:%%SZ','UTC') AS hour,
 sumIf(energy_kwh, energy_type='electricity') AS electricity_kwh,
 sumIf(energy_kwh, energy_type='cooling') AS cooling_kwh,
 countIf(energy_type='electricity') > 0 AS has_electricity
FROM analytics.energy_interval_facts_visible
WHERE tenant_id=toUUID('%[1]s') AND site_id=toUUID('%[2]s') AND accounting_scope='SITE' AND quality='VALID'
  AND period_end >= parseDateTime64BestEffort('%[4]s',3,'UTC') AND period_end < parseDateTime64BestEffort('%[5]s',3,'UTC')
GROUP BY hour
ORDER BY hour
FORMAT JSONEachRow`, tenantID, siteID, timezone, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
}

func (reader *ClickHouseHours) Hours(ctx context.Context, tenantID, siteID string, location *time.Location, from, to time.Time) ([]Hour, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !timezonePattern.MatchString(location.String()) || !from.Before(to) {
		return nil, errors.New("report hour query is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reader.baseURL.String(), strings.NewReader(hoursSQL(tenantID, siteID, location.String(), from, to)))
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
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumHourBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximumHourBytes {
		return nil, errors.New("report hours exceed 4 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ClickHouse returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return decodeHours(payload)
}

func decodeHours(payload []byte) ([]Hour, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	hours := []Hour{}
	for {
		var row struct {
			Hour           string  `json:"hour"`
			ElectricityKWh float64 `json:"electricity_kwh"`
			CoolingKWh     float64 `json:"cooling_kwh"`
			HasElectricity uint8   `json:"has_electricity"`
		}
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return hours, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode report hours: %w", err)
		}
		start, err := time.Parse(time.RFC3339, row.Hour)
		if err != nil {
			return nil, err
		}
		hours = append(hours, Hour{Start: start, ElectricityKWh: row.ElectricityKWh, CoolingKWh: row.CoolingKWh, HasElectricity: row.HasElectricity == 1})
	}
}
