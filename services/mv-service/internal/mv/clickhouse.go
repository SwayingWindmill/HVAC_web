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

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
)

const (
	// HourlyEnergyMetric and HourlyCoolingMetric are the released Site Metrics M&V verifies on;
	// DeviceHourlyEnergyMetric is a device's sub-meter, the boundary of an Option B measure.
	HourlyEnergyMetric       = "hourly_energy"
	HourlyCoolingMetric      = "hourly_cooling"
	DeviceHourlyEnergyMetric = "device_hourly_energy"
	maximumHourlyBytes       = int64(4 << 20)
)

// ClickHouseHours reads hourly plant electricity and cooling from released Metric results with
// M&V's own read-only account (ADR 0021).
type ClickHouseHours struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
	ch       *clickhousehttp.Client
}

func NewClickHouseHours(baseURL, username, password string, client *http.Client) (*ClickHouseHours, error) {
	parsed, err := clickhousehttp.ParseBaseURL(baseURL, true)
	if err != nil {
		return nil, errors.New("M&V ClickHouse URL must be an HTTP(S) origin")
	}
	if client == nil {
		client = clickhousehttp.NewPooledHTTPClient(clickhousehttp.ServiceTransportConfig(15 * time.Second))
	}
	trimmedUser := strings.TrimSpace(username)
	creds := clickhousehttp.Credentials{Username: trimmedUser, Password: password}
	return &ClickHouseHours{
		baseURL:  parsed,
		username: trimmedUser,
		password: password,
		client:   client,
		ch:       clickhousehttp.NewClient(parsed, client, creds),
	}, nil
}

// Ping verifies ClickHouse reader connectivity via a lightweight SELECT 1 query.
func (reader *ClickHouseHours) Ping(ctx context.Context) error {
	if reader == nil || reader.ch == nil {
		return errors.New("M&V ClickHouse reader is not initialized")
	}
	if err := reader.ch.Ping(ctx, false); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return fmt.Errorf("ping M&V ClickHouse returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("ping M&V ClickHouse: %w", err)
	}
	return nil
}

// CloseIdleConnections releases idle HTTP keep-alive connections held by this client.
func (reader *ClickHouseHours) CloseIdleConnections() {
	if reader != nil && reader.ch != nil {
		reader.ch.CloseIdleConnections()
	}
}

// series is one hourly Metric a sample is made of: what it is to the sample, and whose it is.
type series struct{ key, subjectType, subjectID, metric string }

// samplesSeries are the Metrics a boundary's samples are made of: the plant's cooling and
// electricity, and the device's own electricity when a sub-meter bounds the measure.
func samplesSeries(siteID string, boundaryDeviceID *string) []series {
	all := []series{{"cooling", "SITE", siteID, HourlyCoolingMetric}, {"plant", "SITE", siteID, HourlyEnergyMetric}}
	if boundaryDeviceID != nil {
		all = append(all, series{"device", "DEVICE", *boundaryDeviceID, DeviceHourlyEnergyMetric})
	}
	return all
}

// hourlySQL takes each hour's latest revision of every series, keeps hours where all are good,
// and left-joins optional hourly condition anchors for bivariate M&V modeling.
func hourlySQL(tenantID, siteID string, boundaryDeviceID *string, from, to time.Time) string {
	all := samplesSeries(siteID, boundaryDeviceID)
	keys := make([]string, 0, len(all)*2+1)
	matches := make([]string, 0, len(all))
	for _, one := range all {
		match := fmt.Sprintf("(subject_type='%s' AND subject_id=toUUID('%s') AND metric_code='%s')", one.subjectType, one.subjectID, one.metric)
		keys = append(keys, match, "'"+one.key+"'")
		matches = append(matches, match)
	}
	keys = append(keys, "''")
	energy := "plant"
	if boundaryDeviceID != nil {
		energy = "device"
	}
	return fmt.Sprintf(`WITH metrics_hourly AS (
 SELECT
  period_start,
  sumIf(value, key='%[3]s') AS energy_kwh,
  sumIf(value, key='cooling') AS cooling_kwh,
  sumIf(value, key='plant') AS plant_kwh
 FROM (
  SELECT multiIf(%[4]s) AS key, period_start,
   argMax(assumeNotNull(value_number), (revision, calculated_at)) AS value,
   argMax(quality, (revision, calculated_at)) AS quality
  FROM analytics.metric_result_facts
  WHERE tenant_id=toUUID('%[1]s') AND site_id=toUUID('%[2]s') AND (%[5]s)
    AND period_start >= parseDateTime64BestEffort('%[6]s',3,'UTC') AND period_start < parseDateTime64BestEffort('%[7]s',3,'UTC')
    AND period_end - period_start = 3600
    AND value_type='NUMBER' AND value_number IS NOT NULL
  GROUP BY key, period_start
 )
 WHERE quality='GOOD'
 GROUP BY period_start
 HAVING count()=%[8]d
)
SELECT
 formatDateTime(metrics_hourly.period_start,'%%Y-%%m-%%dT%%H:%%i:%%SZ','UTC') AS period_start,
 metrics_hourly.energy_kwh AS energy_kwh,
 metrics_hourly.cooling_kwh AS cooling_kwh,
 metrics_hourly.plant_kwh AS plant_kwh,
 cond.outdoor_wet_bulb_c AS outdoor_wet_bulb_c,
 cond.outdoor_dry_bulb_c AS outdoor_dry_bulb_c,
 cond.plant_load_ratio AS plant_load_ratio,
 cond.ai_closed_loop_share AS ai_closed_loop_share
FROM metrics_hourly
LEFT JOIN (
 SELECT hour, outdoor_wet_bulb_c, outdoor_dry_bulb_c, plant_load_ratio, ai_closed_loop_share
 FROM analytics.site_condition_anchors_hourly_latest
 WHERE tenant_id=toUUID('%[1]s') AND site_id=toUUID('%[2]s')
   AND hour >= parseDateTime64BestEffort('%[6]s',3,'UTC') AND hour < parseDateTime64BestEffort('%[7]s',3,'UTC')
) AS cond ON metrics_hourly.period_start = cond.hour
ORDER BY metrics_hourly.period_start
SETTINGS join_use_nulls = 1
FORMAT JSONEachRow`, tenantID, siteID, energy, strings.Join(keys, ","), strings.Join(matches, " OR "),
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), len(all))
}

func (reader *ClickHouseHours) HourlySamples(ctx context.Context, tenantID, siteID string, boundaryDeviceID *string, from, to time.Time) ([]HourSample, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || (boundaryDeviceID != nil && !uuidPattern.MatchString(*boundaryDeviceID)) || !from.Before(to) {
		return nil, errors.New("hourly sample query is invalid")
	}
	payload, err := reader.query(ctx, hourlySQL(tenantID, siteID, boundaryDeviceID, from, to))
	if err != nil {
		return nil, err
	}
	return decodeHours(payload)
}

func (reader *ClickHouseHours) HasDeviceEnergy(ctx context.Context, tenantID, siteID, deviceID string) (bool, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !uuidPattern.MatchString(deviceID) {
		return false, errors.New("device energy query is invalid")
	}
	payload, err := reader.query(ctx, fmt.Sprintf(`SELECT count() > 0 FROM analytics.metric_result_facts
WHERE tenant_id=toUUID('%s') AND site_id=toUUID('%s') AND subject_type='DEVICE' AND subject_id=toUUID('%s') AND metric_code='%s'
FORMAT TabSeparated`, tenantID, siteID, deviceID, DeviceHourlyEnergyMetric))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(payload)) == "1", nil
}

func (reader *ClickHouseHours) query(ctx context.Context, sql string) ([]byte, error) {
	payload, err := reader.ch.Query(ctx, clickhousehttp.QueryRequest{
		Method:           http.MethodPost,
		SQL:              sql,
		MaxResponseBytes: maximumHourlyBytes,
		RequireStatusOK:  true,
	})
	if err != nil {
		var tooLargeErr *clickhousehttp.ResponseTooLargeError
		if errors.As(err, &tooLargeErr) {
			return nil, errors.New("hourly Metric results exceed 4 MiB")
		}
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return nil, fmt.Errorf("ClickHouse returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return nil, err
	}
	return payload, nil
}

func decodeHours(payload []byte) ([]HourSample, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	samples := []HourSample{}
	for {
		var row struct {
			PeriodStart       string   `json:"period_start"`
			EnergyKWh         float64  `json:"energy_kwh"`
			CoolingKWh        float64  `json:"cooling_kwh"`
			PlantKWh          float64  `json:"plant_kwh"`
			OutdoorWetBulbC   *float64 `json:"outdoor_wet_bulb_c"`
			OutdoorDryBulbC   *float64 `json:"outdoor_dry_bulb_c"`
			PlantLoadRatio    *float64 `json:"plant_load_ratio"`
			AIClosedLoopShare *float64 `json:"ai_closed_loop_share"`
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
		samples = append(samples, HourSample{
			Start:             start,
			EnergyKWh:         row.EnergyKWh,
			CoolingKWh:        row.CoolingKWh,
			PlantKWh:          row.PlantKWh,
			OutdoorWetBulbC:   sanitizeFloatPtr(row.OutdoorWetBulbC),
			OutdoorDryBulbC:   sanitizeFloatPtr(row.OutdoorDryBulbC),
			PlantLoadRatio:    sanitizeFloatPtr(row.PlantLoadRatio),
			AIClosedLoopShare: sanitizeFloatPtr(row.AIClosedLoopShare),
		})
	}
}
