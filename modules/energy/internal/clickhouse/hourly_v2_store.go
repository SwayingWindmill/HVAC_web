package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

const (
	maxHourlyBucketQuerySpan  = 24 * time.Hour
	maxHourlyBucketQueryCount = 128
	maxCrossGapWindowSpan     = 72 * time.Hour
)

var uuidTextPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type flexInt64 int64

func (fi *flexInt64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*fi = 0
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return err
		}
		*fi = flexInt64(parsed)
		return nil
	}
	var parsed int64
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return err
	}
	*fi = flexInt64(parsed)
	return nil
}

type flexUint64 uint64

func (fu *flexUint64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*fu = 0
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return err
		}
		*fu = flexUint64(parsed)
		return nil
	}
	var parsed uint64
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return err
	}
	*fu = flexUint64(parsed)
	return nil
}

type visibleIntervalFactRow struct {
	FactID                 string     `json:"fact_id"`
	TenantID               string     `json:"tenant_id"`
	SiteID                 string     `json:"site_id"`
	MeterID                string     `json:"meter_id"`
	MeterBindingID         string     `json:"meter_binding_id"`
	TopologyVersionID      string     `json:"topology_version_id"`
	BindingVersion         flexUint64 `json:"binding_version"`
	EnergyTypeID           string     `json:"energy_type_id"`
	EnergyType             string     `json:"energy_type"`
	MeterRole              string     `json:"meter_role"`
	Direction              string     `json:"direction"`
	AccountingScope        string     `json:"accounting_scope"`
	LoadDeviceID           *string    `json:"load_device_id"`
	DeviceID               string     `json:"device_id"`
	PointID                string     `json:"point_id"`
	SensorID               *string    `json:"sensor_id"`
	TelemetryKey           string     `json:"telemetry_key"`
	PointRevision          flexUint64 `json:"point_revision"`
	Unit                   string     `json:"unit"`
	CounterDecreaseMode    string     `json:"counter_decrease_mode"`
	CounterRolloverModulus *float64   `json:"counter_rollover_modulus"`
	PeriodStart            string     `json:"period_start"`
	PeriodEnd              string     `json:"period_end"`
	EnergyKWh              float64    `json:"energy_kwh"`
	TransitionType         string     `json:"transition_type"`
	Quality                string     `json:"quality"`
	QualityReasons         []string   `json:"quality_reasons"`
	PreviousObservationID  string     `json:"source_previous_observation_id"`
	CurrentObservationID   string     `json:"source_current_observation_id"`
	SourceEventID          string     `json:"source_event_id"`
	SourcePartition        string     `json:"source_partition"`
	SourceOffset           flexUint64 `json:"source_offset"`
	DatasetRevision        flexUint64 `json:"dataset_revision"`
	DataWatermark          string     `json:"data_watermark"`
	ProjectedAt            string     `json:"projected_at"`
	FactRevision           flexUint64 `json:"fact_revision"`
}

type existingHourlyV2Row struct {
	TenantID        string     `json:"tenant_id"`
	SiteID          string     `json:"site_id"`
	EnergyType      string     `json:"energy_type"`
	AccountingScope string     `json:"accounting_scope"`
	LoadDeviceID    string     `json:"load_device_id"`
	PointID         string     `json:"point_id"`
	Quality         string     `json:"quality"`
	TariffPeriod    string     `json:"tariff_period"`
	HourUTC         string     `json:"hour_utc"`
	IntervalCount   flexInt64  `json:"latest_interval_count"`
	CalcVersion     flexUint64 `json:"latest_calc_version"`
	DataWatermark   string     `json:"latest_data_watermark"`
	DatasetRevision flexUint64 `json:"latest_dataset_revision"`
}

type priorFactSpanRow struct {
	TenantID        string     `json:"tenant_id"`
	SiteID          string     `json:"site_id"`
	PointID         string     `json:"point_id"`
	EnergyType      string     `json:"energy_type"`
	AccountingScope string     `json:"accounting_scope"`
	LoadDeviceID    *string    `json:"load_device_id"`
	Quality         string     `json:"quality"`
	PeriodStart     string     `json:"period_start"`
	PeriodEnd       string     `json:"period_end"`
	EnergyKWh       float64    `json:"energy_kwh"`
	FactRevision    flexUint64 `json:"fact_revision"`
	DataWatermark   string     `json:"data_watermark"`
	DatasetRevision flexUint64 `json:"dataset_revision"`
	BindingID       string     `json:"meter_binding_id"`
	ObservationID   string     `json:"source_current_observation_id"`
}

type tariffWindowRow struct {
	WindowStart      string  `json:"window_start"`
	WindowEnd        string  `json:"window_end"`
	PeriodCode       string  `json:"period_code"`
	EnergyRatePerKWh float64 `json:"energy_rate_per_kwh"`
	TariffVersionID  string  `json:"tariff_version_id"`
}

type backfillBucketRow struct {
	TenantID string `json:"tenant_id"`
	SiteID   string `json:"site_id"`
	PointID  string `json:"point_id"`
	HourUTC  string `json:"hour_utc"`
}

type backfillCrossGapRow struct {
	TenantID    string `json:"tenant_id"`
	SiteID      string `json:"site_id"`
	PointID     string `json:"point_id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

type bucketQueryBatch struct {
	tenantID   string
	siteID     string
	points     []string
	pointHours []string
	minHour    time.Time
	maxHour    time.Time
}

func groupBucketsForQueries(buckets []energy.HourlyBucketKey) ([]bucketQueryBatch, error) {
	if len(buckets) == 0 {
		return nil, nil
	}
	seen := make(map[energy.HourlyBucketKey]struct{}, len(buckets))
	normalized := make([]energy.HourlyBucketKey, 0, len(buckets))
	for _, b := range buckets {
		nb := energy.HourlyBucketKey{
			TenantID: strings.TrimSpace(b.TenantID),
			SiteID:   strings.TrimSpace(b.SiteID),
			PointID:  strings.TrimSpace(b.PointID),
			Hour:     b.Hour.UTC().Truncate(time.Hour),
		}
		if nb.TenantID == "" || nb.SiteID == "" || nb.PointID == "" || nb.Hour.IsZero() {
			return nil, errors.New("hourly bucket key tenant_id, site_id, point_id and non-zero hour are required")
		}
		if _, exists := seen[nb]; exists {
			continue
		}
		seen[nb] = struct{}{}
		normalized = append(normalized, nb)
	}
	sort.Slice(normalized, func(i, j int) bool {
		a, b := normalized[i], normalized[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.SiteID != b.SiteID {
			return a.SiteID < b.SiteID
		}
		if !a.Hour.Equal(b.Hour) {
			return a.Hour.Before(b.Hour)
		}
		return a.PointID < b.PointID
	})

	var batches []bucketQueryBatch
	for _, b := range normalized {
		pointHour := b.PointID + "|" + b.Hour.Format(time.RFC3339)
		if len(batches) == 0 {
			batches = append(batches, bucketQueryBatch{
				tenantID:   b.TenantID,
				siteID:     b.SiteID,
				points:     []string{b.PointID},
				pointHours: []string{pointHour},
				minHour:    b.Hour,
				maxHour:    b.Hour,
			})
			continue
		}
		last := &batches[len(batches)-1]
		if last.tenantID != b.TenantID || last.siteID != b.SiteID ||
			len(last.pointHours) >= maxHourlyBucketQueryCount ||
			b.Hour.Sub(last.minHour) >= maxHourlyBucketQuerySpan {
			batches = append(batches, bucketQueryBatch{
				tenantID:   b.TenantID,
				siteID:     b.SiteID,
				points:     []string{b.PointID},
				pointHours: []string{pointHour},
				minHour:    b.Hour,
				maxHour:    b.Hour,
			})
			continue
		}
		last.points = appendUnique(last.points, b.PointID)
		last.pointHours = append(last.pointHours, pointHour)
		if b.Hour.After(last.maxHour) {
			last.maxHour = b.Hour
		}
	}
	return batches, nil
}

// VisibleIntervalsForHours reads all visible latest-revision energy interval facts
// contributing to the requested (tenant_id, site_id, point_id, hour_utc) buckets.
func (reader *Reader) VisibleIntervalsForHours(ctx context.Context, buckets []energy.HourlyBucketKey) ([]energy.EnergyIntervalFact, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	batches, err := groupBucketsForQueries(buckets)
	if err != nil {
		return nil, err
	}
	if len(batches) == 0 {
		return nil, nil
	}

	sql := fmt.Sprintf(`SELECT
  toString(v.fact_id) AS fact_id,
  toString(v.tenant_id) AS tenant_id,
  toString(v.site_id) AS site_id,
  toString(v.meter_id) AS meter_id,
  toString(v.meter_binding_id) AS meter_binding_id,
  toString(v.topology_version_id) AS topology_version_id,
  v.binding_version AS binding_version,
  toString(v.energy_type_id) AS energy_type_id,
  v.energy_type AS energy_type,
  v.meter_role AS meter_role,
  v.direction AS direction,
  v.accounting_scope AS accounting_scope,
  toString(v.load_device_id) AS load_device_id,
  toString(v.device_id) AS device_id,
  toString(v.point_id) AS point_id,
  toString(v.sensor_id) AS sensor_id,
  v.telemetry_key AS telemetry_key,
  v.point_revision AS point_revision,
  v.unit AS unit,
  v.counter_decrease_mode AS counter_decrease_mode,
  v.counter_rollover_modulus AS counter_rollover_modulus,
  formatDateTime(v.period_start, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(v.period_end, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end,
  v.energy_kwh AS energy_kwh,
  v.transition_type AS transition_type,
  v.quality AS quality,
  v.quality_reasons AS quality_reasons,
  toString(v.source_previous_observation_id) AS source_previous_observation_id,
  toString(v.source_current_observation_id) AS source_current_observation_id,
  toString(v.source_event_id) AS source_event_id,
  v.source_partition AS source_partition,
  v.source_offset AS source_offset,
  v.dataset_revision AS dataset_revision,
  formatDateTime(v.data_watermark, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS data_watermark,
  formatDateTime(v.projected_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS projected_at,
  v.fact_revision AS fact_revision
FROM (
  SELECT
    argMax(raw.fact_id, raw.fact_version) AS fact_id,
    raw.tenant_id AS tenant_id,
    raw.site_id AS site_id,
    argMax(raw.meter_id, raw.fact_version) AS meter_id,
    raw.meter_binding_id AS meter_binding_id,
    argMax(raw.topology_version_id, raw.fact_version) AS topology_version_id,
    argMax(raw.binding_version, raw.fact_version) AS binding_version,
    argMax(raw.energy_type_id, raw.fact_version) AS energy_type_id,
    argMax(raw.energy_type, raw.fact_version) AS energy_type,
    argMax(raw.meter_role, raw.fact_version) AS meter_role,
    argMax(raw.direction, raw.fact_version) AS direction,
    argMax(raw.accounting_scope, raw.fact_version) AS accounting_scope,
    argMax(tuple(raw.load_device_id), raw.fact_version).1 AS load_device_id,
    argMax(raw.device_id, raw.fact_version) AS device_id,
    argMax(raw.point_id, raw.fact_version) AS point_id,
    argMax(tuple(raw.sensor_id), raw.fact_version).1 AS sensor_id,
    argMax(raw.telemetry_key, raw.fact_version) AS telemetry_key,
    argMax(raw.point_revision, raw.fact_version) AS point_revision,
    argMax(raw.unit, raw.fact_version) AS unit,
    argMax(raw.counter_decrease_mode, raw.fact_version) AS counter_decrease_mode,
    argMax(tuple(raw.counter_rollover_modulus), raw.fact_version).1 AS counter_rollover_modulus,
    argMax(raw.period_start, raw.fact_version) AS period_start,
    raw.period_end AS period_end,
    argMax(raw.energy_kwh, raw.fact_version) AS energy_kwh,
    argMax(raw.transition_type, raw.fact_version) AS transition_type,
    argMax(raw.quality, raw.fact_version) AS quality,
    argMax(raw.quality_reasons, raw.fact_version) AS quality_reasons,
    argMax(raw.source_previous_observation_id, raw.fact_version) AS source_previous_observation_id,
    raw.source_current_observation_id AS source_current_observation_id,
    argMax(raw.source_event_id, raw.fact_version) AS source_event_id,
    argMax(raw.source_partition, raw.fact_version) AS source_partition,
    argMax(raw.source_offset, raw.fact_version) AS source_offset,
    argMax(raw.dataset_revision, raw.fact_version) AS dataset_revision,
    argMax(raw.data_watermark, raw.fact_version) AS data_watermark,
    argMax(raw.projected_at, raw.fact_version) AS projected_at,
    argMax(raw.fact_revision, raw.fact_version) AS fact_revision
  FROM (
    SELECT
      fact_id,
      tenant_id,
      site_id,
      meter_id,
      meter_binding_id,
      topology_version_id,
      binding_version,
      energy_type_id,
      energy_type,
      meter_role,
      direction,
      accounting_scope,
      load_device_id,
      device_id,
      point_id,
      sensor_id,
      telemetry_key,
      point_revision,
      unit,
      counter_decrease_mode,
      counter_rollover_modulus,
      period_start,
      period_end,
      energy_kwh,
      transition_type,
      quality,
      quality_reasons,
      source_previous_observation_id,
      source_current_observation_id,
      source_event_id,
      source_partition,
      source_offset,
      dataset_revision,
      data_watermark,
      projected_at,
      fact_revision,
      bitShiftLeft(toUInt256(fact_revision), 192)
        + bitShiftLeft(toUInt256(toUnixTimestamp64Milli(projected_at)), 128)
        + toUInt256(toUInt128(fact_id)) AS fact_version
    FROM %s.%s
    WHERE tenant_id = {tenant:UUID}
      AND site_id = {site:UUID}
      AND has({points:Array(UUID)}, point_id)
      AND period_end > {min_hour:DateTime64(3, 'UTC')}
      AND period_end <= {max_cross_gap_end:DateTime64(3, 'UTC')}
  ) AS raw
  GROUP BY
    raw.tenant_id,
    raw.site_id,
    raw.meter_binding_id,
    raw.period_end,
    raw.source_current_observation_id
) AS v
WHERE (
  (
    dateDiff('millisecond', v.period_start, v.period_end) <= 3600000
    AND v.period_end <= {max_hour_end:DateTime64(3, 'UTC')}
    AND has({point_hours:Array(String)}, concat(toString(v.point_id), '|', formatDateTime(toStartOfHour(addMilliseconds(v.period_end, -1)), '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC')))
  )
  OR (
    dateDiff('millisecond', v.period_start, v.period_end) > 3600000
    AND dateDiff('millisecond', v.period_start, v.period_end) <= 259200000
    AND v.quality = 'VALID'
    AND v.energy_kwh >= 0
    AND v.period_start < {max_hour_end:DateTime64(3, 'UTC')}
    AND v.period_end > {min_hour:DateTime64(3, 'UTC')}
  )
)
ORDER BY v.period_end ASC, v.source_current_observation_id ASC
FORMAT JSONEachRow`, reader.analyticsDB(), reader.analyticsTableName())

	var results []energy.EnergyIntervalFact
	for _, batch := range batches {
		maxHourEnd := batch.maxHour.Add(time.Hour)
		maxCrossGapEnd := maxHourEnd.Add(maxCrossGapWindowSpan)
		payload, err := reader.query(ctx, "visible hourly intervals query", sql, url.Values{
			"param_tenant":            {batch.tenantID},
			"param_site":              {batch.siteID},
			"param_points":            {clickHouseArray(batch.points)},
			"param_point_hours":       {clickHouseArray(batch.pointHours)},
			"param_min_hour":          {clickHouseTime(batch.minHour)},
			"param_max_hour_end":      {clickHouseTime(maxHourEnd)},
			"param_max_cross_gap_end": {clickHouseTime(maxCrossGapEnd)},
		})
		if err != nil {
			return nil, err
		}
		decoded, err := decodeVisibleIntervalFacts(payload)
		if err != nil {
			return nil, err
		}
		results = append(results, decoded...)
	}
	return results, nil
}

func decodeVisibleIntervalFacts(payload []byte) ([]energy.EnergyIntervalFact, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	facts := make([]energy.EnergyIntervalFact, 0)
	for {
		var row visibleIntervalFactRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return facts, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse visible energy interval fact: %w", err)
		}
		periodStart, err := parseClickHouseTime(row.PeriodStart)
		if err != nil {
			return nil, fmt.Errorf("decode visible fact period_start: %w", err)
		}
		periodEnd, err := parseClickHouseTime(row.PeriodEnd)
		if err != nil {
			return nil, fmt.Errorf("decode visible fact period_end: %w", err)
		}
		dataWatermark, err := parseClickHouseTime(row.DataWatermark)
		if err != nil {
			return nil, fmt.Errorf("decode visible fact data_watermark: %w", err)
		}
		projectedAt, err := parseClickHouseTime(row.ProjectedAt)
		if err != nil {
			return nil, fmt.Errorf("decode visible fact projected_at: %w", err)
		}
		var loadDeviceID *string
		if row.LoadDeviceID != nil {
			trimmed := strings.TrimSpace(*row.LoadDeviceID)
			if trimmed != "" && trimmed != zeroClickHouseUUID && row.AccountingScope == energy.AccountingScopeBranch {
				loadDeviceID = &trimmed
			}
		}
		var sensorID *string
		if row.SensorID != nil {
			trimmed := strings.TrimSpace(*row.SensorID)
			if trimmed != "" && trimmed != zeroClickHouseUUID {
				sensorID = &trimmed
			}
		}
		facts = append(facts, energy.EnergyIntervalFact{
			FactID:                 row.FactID,
			TenantID:               row.TenantID,
			SiteID:                 row.SiteID,
			MeterID:                row.MeterID,
			MeterBindingID:         row.MeterBindingID,
			TopologyVersionID:      row.TopologyVersionID,
			BindingVersion:         uint64(row.BindingVersion),
			EnergyTypeID:           row.EnergyTypeID,
			EnergyType:             row.EnergyType,
			MeterRole:              row.MeterRole,
			Direction:              row.Direction,
			AccountingScope:        row.AccountingScope,
			LoadDeviceID:           loadDeviceID,
			DeviceID:               row.DeviceID,
			PointID:                row.PointID,
			SensorID:               sensorID,
			TelemetryKey:           row.TelemetryKey,
			PointRevision:          uint64(row.PointRevision),
			Unit:                   row.Unit,
			CounterDecreaseMode:    row.CounterDecreaseMode,
			CounterRolloverModulus: row.CounterRolloverModulus,
			PeriodStart:            periodStart,
			PeriodEnd:              periodEnd,
			EnergyKWh:              row.EnergyKWh,
			TransitionType:         energy.TransitionType(row.TransitionType),
			Quality:                row.Quality,
			QualityReasons:         append([]string(nil), row.QualityReasons...),
			PreviousObservationID:  row.PreviousObservationID,
			CurrentObservationID:   row.CurrentObservationID,
			SourceEventID:          row.SourceEventID,
			SourcePartition:        row.SourcePartition,
			SourceOffset:           uint64(row.SourceOffset),
			DatasetRevision:        uint64(row.DatasetRevision),
			DataWatermark:          dataWatermark,
			ProjectedAt:            projectedAt,
			FactRevision:           uint64(row.FactRevision),
		})
	}
}

// ExistingHourlyFactsV2 reads the latest visible state per key in analytics.energy_hourly_facts_v2
// for touched buckets so calc_version stays monotonic and superseded keys can be tombstoned.
func (reader *Reader) ExistingHourlyFactsV2(ctx context.Context, buckets []energy.HourlyBucketKey) ([]energy.ExistingHourlyV2Key, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	batches, err := groupBucketsForQueries(buckets)
	if err != nil {
		return nil, err
	}
	if len(batches) == 0 {
		return nil, nil
	}

	sql := fmt.Sprintf(`SELECT
  toString(f.tenant_id) AS tenant_id,
  toString(f.site_id) AS site_id,
  f.energy_type AS energy_type,
  f.accounting_scope AS accounting_scope,
  toString(f.load_device_id) AS load_device_id,
  toString(f.point_id) AS point_id,
  f.quality AS quality,
  f.tariff_period AS tariff_period,
  formatDateTime(f.hour, '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC') AS hour_utc,
  argMax(f.interval_count, f.calc_version) AS latest_interval_count,
  max(f.calc_version) AS latest_calc_version,
  formatDateTime(argMax(f.data_watermark, f.calc_version), '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS latest_data_watermark,
  argMax(f.dataset_revision, f.calc_version) AS latest_dataset_revision
FROM %s.energy_hourly_facts_v2 AS f
WHERE f.tenant_id = {tenant:UUID}
  AND f.site_id = {site:UUID}
  AND has({points:Array(UUID)}, f.point_id)
  AND f.hour >= {min_hour:DateTime('UTC')}
  AND f.hour <= {max_hour:DateTime('UTC')}
  AND has({point_hours:Array(String)}, concat(toString(f.point_id), '|', formatDateTime(f.hour, '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC')))
GROUP BY
  f.tenant_id,
  f.site_id,
  f.energy_type,
  f.accounting_scope,
  f.load_device_id,
  f.point_id,
  f.quality,
  f.tariff_period,
  f.hour
FORMAT JSONEachRow`, reader.analyticsDB())

	var existing []energy.ExistingHourlyV2Key
	for _, batch := range batches {
		payload, err := reader.query(ctx, "existing hourly v2 state query", sql, url.Values{
			"param_tenant":      {batch.tenantID},
			"param_site":        {batch.siteID},
			"param_points":      {clickHouseArray(batch.points)},
			"param_point_hours": {clickHouseArray(batch.pointHours)},
			"param_min_hour":    {batch.minHour.UTC().Format("2006-01-02 15:04:05")},
			"param_max_hour":    {batch.maxHour.UTC().Format("2006-01-02 15:04:05")},
		})
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		for {
			var row existingHourlyV2Row
			if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("decode ClickHouse existing hourly v2 state: %w", err)
			}
			hourUTC, err := parseClickHouseTime(row.HourUTC)
			if err != nil {
				return nil, fmt.Errorf("decode existing hourly v2 hour_utc: %w", err)
			}
			watermark, err := parseClickHouseTime(row.DataWatermark)
			if err != nil {
				return nil, fmt.Errorf("decode existing hourly v2 data_watermark: %w", err)
			}
			loadDeviceID := strings.TrimSpace(row.LoadDeviceID)
			if loadDeviceID == zeroClickHouseUUID || row.AccountingScope != energy.AccountingScopeBranch {
				loadDeviceID = ""
			}
			existing = append(existing, energy.ExistingHourlyV2Key{
				TenantID:        row.TenantID,
				SiteID:          row.SiteID,
				EnergyType:      row.EnergyType,
				AccountingScope: row.AccountingScope,
				LoadDeviceID:    loadDeviceID,
				PointID:         row.PointID,
				Quality:         row.Quality,
				TariffPeriod:    row.TariffPeriod,
				Hour:            hourUTC.UTC().Truncate(time.Hour),
				IntervalCount:   int64(row.IntervalCount),
				CalcVersion:     uint64(row.CalcVersion),
				DataWatermark:   watermark,
				DatasetRevision: uint64(row.DatasetRevision),
			})
		}
	}
	return existing, nil
}

// PriorIntervalsForCorrections returns superseded revisions of corrected facts in facts
// so any hour buckets vacated when an interval changes span or quality are also recomputed.
func (reader *Reader) PriorIntervalsForCorrections(ctx context.Context, facts []energy.EnergyIntervalFact) ([]energy.EnergyIntervalFact, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	type siteKey struct{ tenantID, siteID string }
	type siteBatch struct {
		bindings     []string
		observations []string
		keys         []string
		minEnd       time.Time
		maxEnd       time.Time
		maxRevByKey  map[string]uint64
	}
	bySite := make(map[siteKey]*siteBatch)
	for _, fact := range facts {
		if fact.FactRevision == 0 {
			continue
		}
		sk := siteKey{tenantID: strings.TrimSpace(fact.TenantID), siteID: strings.TrimSpace(fact.SiteID)}
		b := bySite[sk]
		if b == nil {
			b = &siteBatch{
				minEnd:      fact.PeriodEnd.UTC(),
				maxEnd:      fact.PeriodEnd.UTC(),
				maxRevByKey: make(map[string]uint64),
			}
			bySite[sk] = b
		}
		b.bindings = appendUnique(b.bindings, fact.MeterBindingID)
		b.observations = appendUnique(b.observations, fact.CurrentObservationID)
		k := fact.MeterBindingID + "|" + fact.CurrentObservationID
		b.keys = appendUnique(b.keys, k)
		b.maxRevByKey[k] = fact.FactRevision
		if endUTC := fact.PeriodEnd.UTC(); endUTC.Before(b.minEnd) {
			b.minEnd = endUTC
		} else if endUTC.After(b.maxEnd) {
			b.maxEnd = endUTC
		}
	}
	if len(bySite) == 0 {
		return nil, nil
	}

	sql := fmt.Sprintf(`SELECT
  toString(f.tenant_id) AS tenant_id,
  toString(f.site_id) AS site_id,
  toString(f.point_id) AS point_id,
  f.energy_type AS energy_type,
  f.accounting_scope AS accounting_scope,
  toString(f.load_device_id) AS load_device_id,
  f.quality AS quality,
  formatDateTime(f.period_start, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(f.period_end, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end,
  f.energy_kwh AS energy_kwh,
  f.fact_revision AS fact_revision,
  formatDateTime(f.data_watermark, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS data_watermark,
  f.dataset_revision AS dataset_revision,
  toString(f.meter_binding_id) AS meter_binding_id,
  toString(f.source_current_observation_id) AS source_current_observation_id
FROM %s.%s AS f
WHERE f.tenant_id = {tenant:UUID}
  AND f.site_id = {site:UUID}
  AND has({bindings:Array(UUID)}, f.meter_binding_id)
  AND has({observations:Array(UUID)}, f.source_current_observation_id)
  AND f.period_end >= {min_end:DateTime64(3, 'UTC')}
  AND f.period_end <= {max_end:DateTime64(3, 'UTC')}
  AND has({keys:Array(String)}, concat(toString(f.meter_binding_id), '|', toString(f.source_current_observation_id)))
FORMAT JSONEachRow`, reader.analyticsDB(), reader.analyticsTableName())

	sites := make([]siteKey, 0, len(bySite))
	for sk := range bySite {
		sites = append(sites, sk)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].tenantID != sites[j].tenantID {
			return sites[i].tenantID < sites[j].tenantID
		}
		return sites[i].siteID < sites[j].siteID
	})

	var priors []energy.EnergyIntervalFact
	for _, sk := range sites {
		b := bySite[sk]
		payload, err := reader.query(ctx, "prior fact revisions query", sql, url.Values{
			"param_tenant":       {sk.tenantID},
			"param_site":         {sk.siteID},
			"param_bindings":     {clickHouseArray(b.bindings)},
			"param_observations": {clickHouseArray(b.observations)},
			"param_keys":         {clickHouseArray(b.keys)},
			"param_min_end":      {clickHouseTime(b.minEnd)},
			"param_max_end":      {clickHouseTime(b.maxEnd)},
		})
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		for {
			var row priorFactSpanRow
			if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("decode ClickHouse prior fact revision: %w", err)
			}
			k := row.BindingID + "|" + row.ObservationID
			maxRev, ok := b.maxRevByKey[k]
			if !ok || uint64(row.FactRevision) >= maxRev {
				continue
			}
			periodStart, err := parseClickHouseTime(row.PeriodStart)
			if err != nil {
				return nil, fmt.Errorf("decode prior fact period_start: %w", err)
			}
			periodEnd, err := parseClickHouseTime(row.PeriodEnd)
			if err != nil {
				return nil, fmt.Errorf("decode prior fact period_end: %w", err)
			}
			watermark, err := parseClickHouseTime(row.DataWatermark)
			if err != nil {
				return nil, fmt.Errorf("decode prior fact data_watermark: %w", err)
			}
			var loadDeviceID *string
			if row.LoadDeviceID != nil {
				trimmed := strings.TrimSpace(*row.LoadDeviceID)
				if trimmed != "" && trimmed != zeroClickHouseUUID && row.AccountingScope == energy.AccountingScopeBranch {
					loadDeviceID = &trimmed
				}
			}
			priors = append(priors, energy.EnergyIntervalFact{
				TenantID:             row.TenantID,
				SiteID:               row.SiteID,
				MeterBindingID:       row.BindingID,
				PointID:              row.PointID,
				EnergyType:           row.EnergyType,
				AccountingScope:      row.AccountingScope,
				LoadDeviceID:         loadDeviceID,
				Quality:              row.Quality,
				PeriodStart:          periodStart,
				PeriodEnd:            periodEnd,
				EnergyKWh:            row.EnergyKWh,
				FactRevision:         uint64(row.FactRevision),
				DataWatermark:        watermark,
				DatasetRevision:      uint64(row.DatasetRevision),
				CurrentObservationID: row.ObservationID,
			})
		}
	}
	return priors, nil
}

// TariffPriceWindows reads the latest synced generation of UTC TOU price windows overlapping
// [from, to) from analytics.tariff_price_windows for one site.
func (reader *Reader) TariffPriceWindows(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]tariffmodel.Window, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if tenant == "" || site == "" {
		return nil, errors.New("tariff price windows tenant_id and site_id are required")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, errors.New("tariff price windows range must be ordered non-zero timestamps")
	}

	db := reader.analyticsDB()
	sql := fmt.Sprintf(`SELECT
  formatDateTime(w.window_start, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS window_start,
  formatDateTime(w.window_end, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS window_end,
  w.period_code AS period_code,
  w.energy_rate_per_kwh AS energy_rate_per_kwh,
  toString(w.tariff_version_id) AS tariff_version_id
FROM %s.tariff_price_windows AS w FINAL
INNER JOIN (
  SELECT
    s.tenant_id AS sync_tenant_id,
    s.site_id AS sync_site_id,
    max(s.generation) AS latest_generation
  FROM %s.tariff_price_window_syncs AS s FINAL
  WHERE s.tenant_id = {tenant:UUID}
    AND s.site_id = {site:UUID}
  GROUP BY s.tenant_id, s.site_id
) AS latest
  ON w.tenant_id = latest.sync_tenant_id
 AND w.site_id = latest.sync_site_id
 AND w.generation = latest.latest_generation
WHERE w.tenant_id = {tenant:UUID}
  AND w.site_id = {site:UUID}
  AND w.window_end > {from:DateTime64(3, 'UTC')}
  AND w.window_start < {to:DateTime64(3, 'UTC')}
ORDER BY w.window_start ASC
FORMAT JSONEachRow`, db, db)

	payload, err := reader.query(ctx, "tariff price windows query", sql, url.Values{
		"param_tenant": {tenant},
		"param_site":   {site},
		"param_from":   {clickHouseTime(from.UTC())},
		"param_to":     {clickHouseTime(to.UTC())},
	})
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	windows := make([]tariffmodel.Window, 0)
	for {
		var row tariffWindowRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return windows, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse tariff price window: %w", err)
		}
		if math.IsNaN(row.EnergyRatePerKWh) || math.IsInf(row.EnergyRatePerKWh, 0) || row.EnergyRatePerKWh < 0 {
			return nil, fmt.Errorf("ClickHouse tariff price window rate is invalid: %v", row.EnergyRatePerKWh)
		}
		start, err := parseClickHouseTime(row.WindowStart)
		if err != nil {
			return nil, fmt.Errorf("decode tariff window_start: %w", err)
		}
		end, err := parseClickHouseTime(row.WindowEnd)
		if err != nil {
			return nil, fmt.Errorf("decode tariff window_end: %w", err)
		}
		if !start.Before(end) {
			continue
		}
		windows = append(windows, tariffmodel.Window{
			Start:            start,
			End:              end,
			PeriodCode:       strings.TrimSpace(row.PeriodCode),
			EnergyRatePerKWh: row.EnergyRatePerKWh,
			VersionID:        row.TariffVersionID,
		})
	}
}

// ListHourlyBackfillBuckets discovers distinct (tenant_id, site_id, point_id, hour_utc)
// scopes with visible intervals or existing non-zero v2 rows overlapping [query.Since, query.Until).
func (reader *Reader) ListHourlyBackfillBuckets(ctx context.Context, query energy.HourlyBackfillQuery) ([]energy.HourlyBucketKey, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	if query.Since.IsZero() || query.Until.IsZero() || !query.Since.Before(query.Until) {
		return nil, errors.New("hourly backfill query requires ordered non-zero since and until")
	}
	sinceUTC := query.Since.UTC().Truncate(time.Hour)
	untilUTC := query.Until.UTC()

	tenant := strings.TrimSpace(query.TenantID)
	site := strings.TrimSpace(query.SiteID)
	if tenant != "" && !uuidTextPattern.MatchString(tenant) {
		return nil, fmt.Errorf("invalid backfill tenant_id %q", tenant)
	}
	if site != "" && !uuidTextPattern.MatchString(site) {
		return nil, fmt.Errorf("invalid backfill site_id %q", site)
	}

	bucketSet := make(map[energy.HourlyBucketKey]struct{})
	addBucket := func(tenantID, siteID, pointID string, hour time.Time) {
		h := hour.UTC().Truncate(time.Hour)
		if h.Before(sinceUTC) || !h.Before(untilUTC) {
			return
		}
		bucketSet[energy.HourlyBucketKey{
			TenantID: strings.TrimSpace(tenantID),
			SiteID:   strings.TrimSpace(siteID),
			PointID:  strings.TrimSpace(pointID),
			Hour:     h,
		}] = struct{}{}
	}

	buildParams := func() url.Values {
		params := url.Values{
			"param_since": {clickHouseTime(sinceUTC)},
			"param_until": {clickHouseTime(untilUTC)},
		}
		if tenant != "" {
			params.Set("param_tenant", tenant)
		}
		if site != "" {
			params.Set("param_site", site)
		}
		return params
	}
	scopeClause := func(prefix string) string {
		var b strings.Builder
		if tenant != "" {
			b.WriteString("\n      AND ")
			b.WriteString(prefix)
			b.WriteString("tenant_id = {tenant:UUID}")
		}
		if site != "" {
			b.WriteString("\n      AND ")
			b.WriteString(prefix)
			b.WriteString("site_id = {site:UUID}")
		}
		return b.String()
	}

	db := reader.analyticsDB()
	table := reader.analyticsTableName()
	rawFilter := scopeClause("")

	// 1) Regular <= 1h visible intervals in [sinceUTC, untilUTC).
	regularSQL := fmt.Sprintf(`SELECT DISTINCT
  toString(v.tenant_id) AS tenant_id,
  toString(v.site_id) AS site_id,
  toString(v.point_id) AS point_id,
  formatDateTime(toStartOfHour(addMilliseconds(v.period_end, -1)), '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC') AS hour_utc
FROM (
  SELECT
    raw.tenant_id AS tenant_id,
    raw.site_id AS site_id,
    argMax(raw.point_id, raw.fact_version) AS point_id,
    argMax(raw.period_start, raw.fact_version) AS period_start,
    raw.period_end AS period_end
  FROM (
    SELECT
      tenant_id,
      site_id,
      meter_binding_id,
      point_id,
      period_start,
      period_end,
      source_current_observation_id,
      fact_id,
      bitShiftLeft(toUInt256(fact_revision), 192)
        + bitShiftLeft(toUInt256(toUnixTimestamp64Milli(projected_at)), 128)
        + toUInt256(toUInt128(fact_id)) AS fact_version
    FROM %s.%s
    WHERE period_end > {since:DateTime64(3, 'UTC')}
      AND period_end <= {until:DateTime64(3, 'UTC')}%s
  ) AS raw
  GROUP BY
    raw.tenant_id,
    raw.site_id,
    raw.meter_binding_id,
    raw.period_end,
    raw.source_current_observation_id
) AS v
WHERE dateDiff('millisecond', v.period_start, v.period_end) <= 3600000
ORDER BY hour_utc ASC, tenant_id ASC, site_id ASC, point_id ASC
FORMAT JSONEachRow`, db, table, rawFilter)

	payload, err := reader.query(ctx, "backfill regular hourly buckets query", regularSQL, buildParams())
	if err != nil {
		return nil, err
	}
	if err := decodeBackfillBucketRows(payload, addBucket); err != nil {
		return nil, err
	}

	// 2) Valid cross-gap (> 1h, <= 72h) visible intervals overlapping [sinceUTC, untilUTC).
	crossGapParams := buildParams()
	crossGapParams.Set("param_max_cross_gap_end", clickHouseTime(untilUTC.Add(maxCrossGapWindowSpan)))
	crossGapSQL := fmt.Sprintf(`SELECT
  toString(v.tenant_id) AS tenant_id,
  toString(v.site_id) AS site_id,
  toString(v.point_id) AS point_id,
  formatDateTime(v.period_start, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_start,
  formatDateTime(v.period_end, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS period_end
FROM (
  SELECT
    raw.tenant_id AS tenant_id,
    raw.site_id AS site_id,
    argMax(raw.point_id, raw.fact_version) AS point_id,
    argMax(raw.period_start, raw.fact_version) AS period_start,
    raw.period_end AS period_end,
    argMax(raw.quality, raw.fact_version) AS quality,
    argMax(raw.energy_kwh, raw.fact_version) AS energy_kwh
  FROM (
    SELECT
      tenant_id,
      site_id,
      meter_binding_id,
      point_id,
      period_start,
      period_end,
      quality,
      energy_kwh,
      source_current_observation_id,
      fact_id,
      bitShiftLeft(toUInt256(fact_revision), 192)
        + bitShiftLeft(toUInt256(toUnixTimestamp64Milli(projected_at)), 128)
        + toUInt256(toUInt128(fact_id)) AS fact_version
    FROM %s.%s
    WHERE period_end > {since:DateTime64(3, 'UTC')}
      AND period_end <= {max_cross_gap_end:DateTime64(3, 'UTC')}%s
  ) AS raw
  GROUP BY
    raw.tenant_id,
    raw.site_id,
    raw.meter_binding_id,
    raw.period_end,
    raw.source_current_observation_id
) AS v
WHERE dateDiff('millisecond', v.period_start, v.period_end) > 3600000
  AND dateDiff('millisecond', v.period_start, v.period_end) <= 259200000
  AND v.quality = 'VALID'
  AND v.energy_kwh >= 0
  AND v.period_start < {until:DateTime64(3, 'UTC')}
  AND v.period_end > {since:DateTime64(3, 'UTC')}
ORDER BY v.period_end ASC
FORMAT JSONEachRow`, db, table, rawFilter)

	crossGapPayload, err := reader.query(ctx, "backfill cross-gap hourly buckets query", crossGapSQL, crossGapParams)
	if err != nil {
		return nil, err
	}
	crossGapDecoder := json.NewDecoder(bytes.NewReader(crossGapPayload))
	for {
		var row backfillCrossGapRow
		if err := crossGapDecoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse backfill cross-gap interval: %w", err)
		}
		pStart, err := parseClickHouseTime(row.PeriodStart)
		if err != nil {
			return nil, fmt.Errorf("decode backfill cross-gap period_start: %w", err)
		}
		pEnd, err := parseClickHouseTime(row.PeriodEnd)
		if err != nil {
			return nil, fmt.Errorf("decode backfill cross-gap period_end: %w", err)
		}
		for _, h := range expandCrossGapHours(pStart, pEnd) {
			addBucket(row.TenantID, row.SiteID, row.PointID, h)
		}
	}

	// 3) Active rows already in energy_hourly_facts_v2 inside [sinceUTC, untilUTC) so orphaned hours get tombstoned.
	v2Params := url.Values{
		"param_since": {sinceUTC.Format("2006-01-02 15:04:05")},
		"param_until": {untilUTC.Format("2006-01-02 15:04:05")},
	}
	if tenant != "" {
		v2Params.Set("param_tenant", tenant)
	}
	if site != "" {
		v2Params.Set("param_site", site)
	}
	existingSQL := fmt.Sprintf(`SELECT DISTINCT
  toString(h.tenant_id) AS tenant_id,
  toString(h.site_id) AS site_id,
  toString(h.point_id) AS point_id,
  formatDateTime(h.hour, '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC') AS hour_utc
FROM (
  SELECT
    f.tenant_id AS tenant_id,
    f.site_id AS site_id,
    f.point_id AS point_id,
    f.hour AS hour,
    argMax(f.interval_count, f.calc_version) AS latest_interval_count
  FROM %s.energy_hourly_facts_v2 AS f
  WHERE f.hour >= {since:DateTime('UTC')}
    AND f.hour < {until:DateTime('UTC')}%s
  GROUP BY
    f.tenant_id,
    f.site_id,
    f.energy_type,
    f.accounting_scope,
    f.load_device_id,
    f.point_id,
    f.quality,
    f.tariff_period,
    f.hour
  HAVING latest_interval_count != 0
) AS h
ORDER BY hour_utc ASC, tenant_id ASC, site_id ASC, point_id ASC
FORMAT JSONEachRow`, db, scopeClause("f."))

	existingPayload, err := reader.query(ctx, "backfill existing hourly v2 buckets query", existingSQL, v2Params)
	if err != nil {
		return nil, err
	}
	if err := decodeBackfillBucketRows(existingPayload, addBucket); err != nil {
		return nil, err
	}

	buckets := make([]energy.HourlyBucketKey, 0, len(bucketSet))
	for b := range bucketSet {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool {
		a, b := buckets[i], buckets[j]
		if !a.Hour.Equal(b.Hour) {
			return a.Hour.Before(b.Hour)
		}
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.SiteID != b.SiteID {
			return a.SiteID < b.SiteID
		}
		return a.PointID < b.PointID
	})
	return buckets, nil
}

func decodeBackfillBucketRows(payload []byte, add func(tenantID, siteID, pointID string, hour time.Time)) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var row backfillBucketRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("decode ClickHouse backfill hourly bucket: %w", err)
		}
		hour, err := parseClickHouseTime(row.HourUTC)
		if err != nil {
			return fmt.Errorf("decode backfill bucket hour_utc: %w", err)
		}
		add(row.TenantID, row.SiteID, row.PointID, hour)
	}
}

func expandCrossGapHours(startUTC, endUTC time.Time) []time.Time {
	start := startUTC.UTC()
	end := endUTC.UTC()
	var hours []time.Time
	for h := start.Truncate(time.Hour); h.Before(end); h = h.Add(time.Hour) {
		slotEnd := h.Add(time.Hour)
		auditStart := h
		if start.After(auditStart) {
			auditStart = start
		}
		auditEnd := slotEnd
		if end.Before(auditEnd) {
			auditEnd = end
		}
		if auditEnd.Sub(auditStart) <= 0 {
			continue
		}
		if len(hours) >= energy.MaxCrossGapAllocationHours {
			return nil
		}
		hours = append(hours, h)
	}
	return hours
}

func (reader *Reader) analyticsTableName() string {
	table := strings.TrimSpace(reader.analyticsTable)
	if table == "" {
		return "energy_interval_facts"
	}
	return table
}

var (
	_ energy.HourlyIntervalSource    = (*Reader)(nil)
	_ energy.HourlyV2StateSource     = (*Reader)(nil)
	_ energy.PriorFactIntervalSource = (*Reader)(nil)
	_ energy.TariffWindowSource      = (*Reader)(nil)
	_ energy.HourlyBackfillSource    = (*Reader)(nil)
)
