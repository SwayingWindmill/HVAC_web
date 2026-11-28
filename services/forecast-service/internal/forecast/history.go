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
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
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
	ch       *clickhousehttp.Client
}

func NewClickHouseHistoryReader(config HistoryConfig) (*ClickHouseHistoryReader, error) {
	parsed, err := clickhousehttp.ParseOriginURL(config.BaseURL)
	if err != nil {
		return nil, errors.New("forecast history ClickHouse URL must be an HTTP(S) origin")
	}
	client := config.HTTPClient
	if client == nil {
		client = clickhousehttp.NewPooledHTTPClient(clickhousehttp.ServiceTransportConfig(10 * time.Second))
	}
	trimmedUser := strings.TrimSpace(config.Username)
	creds := clickhousehttp.Credentials{Username: trimmedUser, Password: config.Password}
	return &ClickHouseHistoryReader{
		baseURL:  parsed,
		username: trimmedUser,
		password: config.Password,
		client:   client,
		ch:       clickhousehttp.NewClient(parsed, client, creds),
	}, nil
}

func (reader *ClickHouseHistoryReader) CloseIdleConnections() {
	if reader != nil && reader.ch != nil {
		reader.ch.CloseIdleConnections()
	}
}

func (reader *ClickHouseHistoryReader) Ping(ctx context.Context) error {
	if reader == nil || reader.ch == nil {
		return errors.New("forecast history reader is unavailable")
	}
	if err := reader.ch.Ping(ctx, false); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return fmt.Errorf("ping forecast ClickHouse history returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("ping forecast ClickHouse history: %w", err)
	}
	return nil
}

func (reader *ClickHouseHistoryReader) queryHistory(ctx context.Context, sql, opLabel string) ([]byte, error) {
	payload, err := reader.ch.Query(ctx, clickhousehttp.QueryRequest{
		Method:           http.MethodPost,
		SQL:              sql,
		Accept:           "application/x-ndjson",
		MaxResponseBytes: maximumForecastHistoryResponseBytes,
	})
	if err != nil {
		var tooLargeErr *clickhousehttp.ResponseTooLargeError
		if errors.As(err, &tooLargeErr) {
			return nil, fmt.Errorf("%s response exceeds 8 MiB", opLabel)
		}
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return nil, fmt.Errorf("%s returned %d: %s", opLabel, statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return nil, fmt.Errorf("query %s: %w", opLabel, err)
	}
	return payload, nil
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

	payload, err := reader.queryHistory(ctx, sql, "authoritative Forecast metric history")
	if err != nil {
		return nil, err
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

// ReadWeatherSeries reads slot-aggregated outdoor dry-bulb, wet-bulb, and relative humidity
// observations from telemetry_history.observations, preferring direct site sensors and applying
// physical sanity bounds before and after aggregation.
func (reader *ClickHouseHistoryReader) ReadWeatherSeries(ctx context.Context, query WeatherHistoryQuery) ([]WeatherObservation, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return nil, errors.New("forecast history reader is unavailable")
	}
	for field, value := range map[string]string{"tenantId": query.TenantID, "siteId": query.SiteID} {
		if !uuidPattern.MatchString(value) {
			return nil, fmt.Errorf("%s must be a UUID", field)
		}
	}
	if query.From.IsZero() || !query.To.After(query.From) {
		return nil, errors.New("weather history requires a valid half-open time window")
	}
	minutes := query.GranularityMinutes
	if minutes <= 0 {
		minutes = 15
	}
	intervalExpr := strconv.Itoa(minutes)
	dryKeys := "'weather.ambient_dry_bulb_temperature','ambient_dry_bulb_temperature','outdoor_dry_bulb_temp','outdoor_dry_bulb','outdoor_dry_bulb_c','ambient_temp','ambient_temperature','outdoor_temp','weather.dry_bulb_temp','ambient.dry_bulb_temp','dry_bulb_temp'"
	wetKeys := "'weather.ambient_wet_bulb_temperature','ambient_wet_bulb_temperature','outdoor_wet_bulb_temp','outdoor_wet_bulb','outdoor_wet_bulb_c','weather.wet_bulb_temp','ambient.wet_bulb_temp','wet_bulb_temp'"
	rhKeys := "'weather.relative_humidity','relative_humidity','outdoor_relative_humidity','ambient_relative_humidity','outdoor_rh','ambient_rh','rh'"
	allKeys := dryKeys + "," + wetKeys + "," + rhKeys

	sql := fmt.Sprintf(`SELECT
 formatDateTime(toDateTime(toStartOfInterval(obs.sampled_at, INTERVAL %s MINUTE), 'UTC'), '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC') AS bucket_utc,
 if(countIf(obs.telemetry_key IN (%s) AND obs.value_number >= -25 AND obs.value_number <= 50) > 0,
    toNullable(avgIf(obs.value_number, obs.telemetry_key IN (%s) AND obs.value_number >= -25 AND obs.value_number <= 50)),
    CAST(NULL, 'Nullable(Float64)')) AS dry_bulb_c,
 if(countIf(obs.telemetry_key IN (%s) AND obs.value_number >= -30 AND obs.value_number <= 45) > 0,
    toNullable(avgIf(obs.value_number, obs.telemetry_key IN (%s) AND obs.value_number >= -30 AND obs.value_number <= 45)),
    CAST(NULL, 'Nullable(Float64)')) AS wet_bulb_c,
 if(countIf(obs.telemetry_key IN (%s) AND obs.value_number >= 1 AND obs.value_number <= 100) > 0,
    toNullable(avgIf(obs.value_number, obs.telemetry_key IN (%s) AND obs.value_number >= 1 AND obs.value_number <= 100)),
    CAST(NULL, 'Nullable(Float64)')) AS relative_humidity_pct
FROM telemetry_history.observations AS obs
WHERE obs.tenant_id = toUUID('%s')
  AND obs.site_id = toUUID('%s')
  AND obs.telemetry_key IN (%s)
  AND obs.sampled_at >= parseDateTime64BestEffort('%s', 3, 'UTC')
  AND obs.sampled_at < parseDateTime64BestEffort('%s', 3, 'UTC')
  AND obs.received_at <= parseDateTime64BestEffort('%s', 3, 'UTC')
  AND obs.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND obs.quality IN ('GOOD', 'INTERPOLATED', 'AGGREGATED')
  AND obs.value_number IS NOT NULL
  AND isFinite(obs.value_number)
GROUP BY bucket_utc
ORDER BY bucket_utc ASC
FORMAT JSONEachRow`,
		intervalExpr,
		dryKeys, dryKeys,
		wetKeys, wetKeys,
		rhKeys, rhKeys,
		query.TenantID, query.SiteID, allKeys,
		formatForecastHistoryTime(query.From), formatForecastHistoryTime(query.To), formatForecastHistoryTime(query.To),
	)

	payload, err := reader.queryHistory(ctx, sql, "forecast weather history")
	if err != nil {
		return nil, err
	}

	type row struct {
		BucketUTC           string   `json:"bucket_utc"`
		DryBulbC            *float64 `json:"dry_bulb_c"`
		WetBulbC            *float64 `json:"wet_bulb_c"`
		RelativeHumidityPct *float64 `json:"relative_humidity_pct"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	observations := make([]WeatherObservation, 0)
	for {
		var value row
		if err = decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode forecast weather history: %w", err)
		}
		observedAt, parseErr := time.Parse(time.RFC3339, value.BucketUTC)
		if parseErr != nil {
			return nil, fmt.Errorf("decode weather bucket_utc: %w", parseErr)
		}
		clean, ok := sanitizeWeatherObservation(WeatherObservation{
			ObservedAt:          observedAt.UTC(),
			DryBulbC:            value.DryBulbC,
			WetBulbC:            value.WetBulbC,
			RelativeHumidityPct: value.RelativeHumidityPct,
			Source:              WeatherSourceSiteSensors,
		})
		if ok {
			observations = append(observations, clean)
		}
	}
	return observations, nil
}
