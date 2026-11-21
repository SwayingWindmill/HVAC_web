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
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

type conditionAnchorInsertRow struct {
	TenantID          string   `json:"tenant_id"`
	SiteID            string   `json:"site_id"`
	Hour              string   `json:"hour"`
	OutdoorWetBulbC   *float64 `json:"outdoor_wet_bulb_c"`
	OutdoorDryBulbC   *float64 `json:"outdoor_dry_bulb_c"`
	PlantLoadRatio    *float64 `json:"plant_load_ratio"`
	AIClosedLoopShare *float64 `json:"ai_closed_loop_share"`
	SampleCount       uint32   `json:"sample_count"`
	CalcVersion       uint64   `json:"calc_version"`
	UpdatedAt         string   `json:"updated_at"`
}

// InsertConditionAnchors writes hourly operating condition anchors into
// analytics.site_condition_anchors_hourly using append-only JSONEachRow inserts.
func (writer *Writer) InsertConditionAnchors(ctx context.Context, anchors []energy.SiteConditionAnchor) error {
	if writer == nil || writer.endpoint == nil || writer.httpClient == nil {
		return errors.New("ClickHouse analytics writer is closed")
	}
	if len(anchors) == 0 {
		return nil
	}
	if len(anchors) > maximumBatchSize {
		return errors.New("ClickHouse analytics condition anchors batch exceeds 4096")
	}

	ordered := append([]energy.SiteConditionAnchor(nil), anchors...)
	sort.Slice(ordered, func(left, right int) bool {
		a, b := ordered[left], ordered[right]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.SiteID != b.SiteID {
			return a.SiteID < b.SiteID
		}
		return a.Hour.Before(b.Hour)
	})

	var body bytes.Buffer
	digest := sha256.New()
	fallbackNow := time.Now().UTC()

	for _, anchor := range ordered {
		if err := validateConditionAnchor(anchor); err != nil {
			return err
		}
		hourUTC := anchor.Hour.UTC().Truncate(time.Hour)
		updatedAt := anchor.UpdatedAt.UTC()
		if updatedAt.IsZero() {
			updatedAt = fallbackNow
		}
		encoded, err := json.Marshal(conditionAnchorInsertRow{
			TenantID:          anchor.TenantID,
			SiteID:            anchor.SiteID,
			Hour:              hourUTC.Format("2006-01-02 15:04:05"),
			OutdoorWetBulbC:   anchor.OutdoorWetBulbC,
			OutdoorDryBulbC:   anchor.OutdoorDryBulbC,
			PlantLoadRatio:    anchor.PlantLoadRatio,
			AIClosedLoopShare: anchor.AIClosedLoopShare,
			SampleCount:       anchor.SampleCount,
			CalcVersion:       anchor.CalcVersion,
			UpdatedAt:         clickHouseTime(updatedAt),
		})
		if err != nil {
			return fmt.Errorf("encode site condition anchor: %w", err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
		fmt.Fprintf(digest, "%s|%s|%d|%s|%s|%s|%s|%d|%d\n",
			anchor.TenantID,
			anchor.SiteID,
			hourUTC.Unix(),
			formatOptionalFloatDigest(anchor.OutdoorWetBulbC),
			formatOptionalFloatDigest(anchor.OutdoorDryBulbC),
			formatOptionalFloatDigest(anchor.PlantLoadRatio),
			formatOptionalFloatDigest(anchor.AIClosedLoopShare),
			anchor.SampleCount,
			anchor.CalcVersion,
		)
	}

	db := strings.TrimSpace(writer.database)
	if db == "" {
		db = "analytics"
	}

	return writer.insert(ctx, "condition anchors insert", url.Values{
		"query":                      {"INSERT INTO " + db + ".site_condition_anchors_hourly FORMAT JSONEachRow"},
		"date_time_input_format":     {"best_effort"},
		"insert_deduplication_token": {hex.EncodeToString(digest.Sum(nil))},
		"async_insert":               {"1"},
		"wait_for_async_insert":      {"1"},
		"async_insert_deduplicate":   {"1"},
		"wait_end_of_query":          {"1"},
	}, body.Bytes())
}

func validateConditionAnchor(anchor energy.SiteConditionAnchor) error {
	if strings.TrimSpace(anchor.TenantID) == "" || strings.TrimSpace(anchor.SiteID) == "" {
		return errors.New("ClickHouse condition anchor tenant_id and site_id are required")
	}
	if anchor.Hour.IsZero() {
		return errors.New("ClickHouse condition anchor hour is required")
	}
	for _, value := range []*float64{
		anchor.OutdoorWetBulbC,
		anchor.OutdoorDryBulbC,
		anchor.PlantLoadRatio,
		anchor.AIClosedLoopShare,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return errors.New("ClickHouse condition anchor metrics must be finite")
		}
	}
	return nil
}

func formatOptionalFloatDigest(val *float64) string {
	if val == nil {
		return "null"
	}
	return strconv.FormatFloat(*val, 'g', -1, 64)
}

type flexInt int

func (fi *flexInt) UnmarshalJSON(data []byte) error {
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
		parsed, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return err
		}
		*fi = flexInt(parsed)
		return nil
	}
	var parsed int
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return err
	}
	*fi = flexInt(parsed)
	return nil
}

type conditionInputRow struct {
	TenantID          string   `json:"tenant_id"`
	SiteID            string   `json:"site_id"`
	HourUTC           string   `json:"hour_utc"`
	OutdoorWetBulbC   *float64 `json:"outdoor_wet_bulb_c"`
	OutdoorDryBulbC   *float64 `json:"outdoor_dry_bulb_c"`
	CoolingKWh        *float64 `json:"cooling_kwh"`
	ClosedLoopTicks   flexInt  `json:"closed_loop_ticks"`
	TotalControlTicks flexInt  `json:"total_control_ticks"`
}

// ReadConditionInputs reads hourly outdoor wet-bulb / dry-bulb temperatures and AI closed-loop
// control ticks from <analyticsDB>.operating_condition_observations combined with hourly cooling kWh
// from <analyticsDB>.energy_hourly over [from, to) in UTC.
func (reader *Reader) ReadConditionInputs(ctx context.Context, from, to time.Time) ([]energy.ConditionSampleInput, error) {
	if reader == nil || reader.endpoint == nil || reader.httpClient == nil {
		return nil, errors.New("ClickHouse analytics reader is closed")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, errors.New("condition inputs window must be ordered non-zero timestamps")
	}

	analyticsDB := reader.analyticsDB()
	fromUTC := from.UTC()
	toUTC := to.UTC()
	coolingFromUTC := fromUTC.Truncate(time.Hour)

	sql := fmt.Sprintf(`WITH
  obs_raw AS (
    SELECT
      assumeNotNull(obs.tenant_id) AS obs_tenant_key,
      assumeNotNull(obs.site_id) AS obs_site_key,
      obs.hour_utc AS obs_hour_key,
      obs.telemetry_key AS obs_metric_key,
      obs.value_number AS obs_num_val,
      obs.value_text AS obs_text_val
    FROM %[1]s.operating_condition_observations AS obs
    WHERE obs.tenant_id IS NOT NULL
      AND obs.site_id IS NOT NULL
      AND obs.sampled_at >= {from:DateTime64(3, 'UTC')}
      AND obs.sampled_at < {to:DateTime64(3, 'UTC')}
      AND (
        (
          obs.telemetry_key IN (
            'weather.ambient_wet_bulb_temperature',
            'ambient_wet_bulb_temperature',
            'outdoor_wet_bulb',
            'cooling_tower.ambient_wet_bulb_temperature',
            'outdoor_wet_bulb_c',
            'weather.ambient_dry_bulb_temperature',
            'ambient_dry_bulb_temperature',
            'outdoor_dry_bulb',
            'outdoor_dry_bulb_c',
            'ambient_temperature'
          )
          AND obs.value_number IS NOT NULL
          AND isFinite(obs.value_number)
        )
        OR (
          obs.telemetry_key IN (
            'ai_closed_loop',
            'plant.ai_closed_loop',
            'optimization.ai_closed_loop',
            'control_mode',
            'plant.control_mode',
            'site.control_mode',
            'optimization.control_mode',
            'ai_control_mode',
            'auto_control.mode',
            'auto_control_mode'
          )
          AND (
            (obs.value_number IS NOT NULL AND isFinite(obs.value_number))
            OR trimBoth(ifNull(obs.value_text, '')) != ''
          )
        )
      )
  ),
  obs_hourly AS (
    SELECT
      r.obs_tenant_key AS o_tenant_key,
      r.obs_site_key AS o_site_key,
      r.obs_hour_key AS o_hour_key,
      if(
        countIf(
          r.obs_num_val IS NOT NULL
            AND isFinite(r.obs_num_val)
            AND r.obs_metric_key IN (
              'weather.ambient_wet_bulb_temperature',
              'ambient_wet_bulb_temperature',
              'outdoor_wet_bulb',
              'cooling_tower.ambient_wet_bulb_temperature',
              'outdoor_wet_bulb_c'
            )
        ) > 0,
        toNullable(avgIf(
          r.obs_num_val,
          r.obs_num_val IS NOT NULL
            AND isFinite(r.obs_num_val)
            AND r.obs_metric_key IN (
              'weather.ambient_wet_bulb_temperature',
              'ambient_wet_bulb_temperature',
              'outdoor_wet_bulb',
              'cooling_tower.ambient_wet_bulb_temperature',
              'outdoor_wet_bulb_c'
            )
        )),
        CAST(NULL, 'Nullable(Float64)')
      ) AS wet_bulb_c,
      if(
        countIf(
          r.obs_num_val IS NOT NULL
            AND isFinite(r.obs_num_val)
            AND r.obs_metric_key IN (
              'weather.ambient_dry_bulb_temperature',
              'ambient_dry_bulb_temperature',
              'outdoor_dry_bulb',
              'outdoor_dry_bulb_c',
              'ambient_temperature'
            )
        ) > 0,
        toNullable(avgIf(
          r.obs_num_val,
          r.obs_num_val IS NOT NULL
            AND isFinite(r.obs_num_val)
            AND r.obs_metric_key IN (
              'weather.ambient_dry_bulb_temperature',
              'ambient_dry_bulb_temperature',
              'outdoor_dry_bulb',
              'outdoor_dry_bulb_c',
              'ambient_temperature'
            )
        )),
        CAST(NULL, 'Nullable(Float64)')
      ) AS dry_bulb_c,
      toUInt32(countIf(
        (
          r.obs_metric_key IN ('ai_closed_loop', 'plant.ai_closed_loop', 'optimization.ai_closed_loop')
          AND (
            (r.obs_num_val IS NOT NULL AND isFinite(r.obs_num_val) AND r.obs_num_val > 0)
            OR upper(trimBoth(ifNull(r.obs_text_val, ''))) IN ('1', 'TRUE', 'CLOSED_LOOP', 'AI_CLOSED_LOOP', 'ACTIVE', 'ON')
          )
        )
        OR (
          r.obs_metric_key IN (
            'control_mode',
            'plant.control_mode',
            'site.control_mode',
            'optimization.control_mode',
            'ai_control_mode',
            'auto_control.mode',
            'auto_control_mode'
          )
          AND (
            upper(trimBoth(ifNull(r.obs_text_val, ''))) IN ('CLOSED_LOOP', 'AI_CLOSED_LOOP')
            OR (trimBoth(ifNull(r.obs_text_val, '')) = '' AND r.obs_num_val IS NOT NULL AND isFinite(r.obs_num_val) AND r.obs_num_val = 1)
          )
        )
      )) AS closed_loop_count,
      toUInt32(countIf(
        r.obs_metric_key IN (
          'ai_closed_loop',
          'plant.ai_closed_loop',
          'optimization.ai_closed_loop',
          'control_mode',
          'plant.control_mode',
          'site.control_mode',
          'optimization.control_mode',
          'ai_control_mode',
          'auto_control.mode',
          'auto_control_mode'
        )
      )) AS total_control_count
    FROM obs_raw AS r
    GROUP BY r.obs_tenant_key, r.obs_site_key, r.obs_hour_key
  ),
  cooling_hourly AS (
    SELECT
      e.tenant_id AS cooling_tenant_key,
      e.site_id AS cooling_site_key,
      toStartOfHour(e.hour, 'UTC') AS cooling_hour_key,
      toNullable(sum(e.energy_kwh)) AS cooling_kwh
    FROM %[1]s.energy_hourly AS e
    WHERE e.energy_type = 'cooling'
      AND e.accounting_scope = 'SITE'
      AND e.quality = 'VALID'
      AND e.hour >= {cooling_from:DateTime64(3, 'UTC')}
      AND e.hour < {to:DateTime64(3, 'UTC')}
    GROUP BY e.tenant_id, e.site_id, toStartOfHour(e.hour, 'UTC')
    HAVING sum(e.interval_count) > 0
  )
SELECT
  toString(if(o.o_tenant_key != toUUID('00000000-0000-0000-0000-000000000000'), o.o_tenant_key, c.cooling_tenant_key)) AS tenant_id,
  toString(if(o.o_site_key != toUUID('00000000-0000-0000-0000-000000000000'), o.o_site_key, c.cooling_site_key)) AS site_id,
  formatDateTime(
    if(o.o_hour_key > toDateTime(0, 'UTC'), o.o_hour_key, c.cooling_hour_key),
    '%%Y-%%m-%%dT%%H:%%i:%%SZ',
    'UTC'
  ) AS hour_utc,
  o.wet_bulb_c AS outdoor_wet_bulb_c,
  o.dry_bulb_c AS outdoor_dry_bulb_c,
  c.cooling_kwh AS cooling_kwh,
  toUInt32(ifNull(o.closed_loop_count, 0)) AS closed_loop_ticks,
  toUInt32(ifNull(o.total_control_count, 0)) AS total_control_ticks
FROM obs_hourly AS o
FULL OUTER JOIN cooling_hourly AS c
  ON o.o_tenant_key = c.cooling_tenant_key
 AND o.o_site_key = c.cooling_site_key
 AND o.o_hour_key = c.cooling_hour_key
ORDER BY tenant_id ASC, site_id ASC, hour_utc ASC
FORMAT JSONEachRow`, analyticsDB)

	payload, err := reader.query(ctx, "condition inputs query", sql, url.Values{
		"param_from":         {clickHouseTime(fromUTC)},
		"param_cooling_from": {clickHouseTime(coolingFromUTC)},
		"param_to":           {clickHouseTime(toUTC)},
	})
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	inputs := make([]energy.ConditionSampleInput, 0)
	for {
		var row conditionInputRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse condition input: %w", err)
		}
		hour, err := parseClickHouseTime(row.HourUTC)
		if err != nil {
			return nil, fmt.Errorf("decode condition input hour_utc: %w", err)
		}
		inputs = append(inputs, energy.ConditionSampleInput{
			TenantID:          strings.TrimSpace(row.TenantID),
			SiteID:            strings.TrimSpace(row.SiteID),
			Hour:              hour.UTC().Truncate(time.Hour),
			OutdoorWetBulbC:   finiteFloatPtr(row.OutdoorWetBulbC),
			OutdoorDryBulbC:   finiteFloatPtr(row.OutdoorDryBulbC),
			CoolingKWh:        finiteFloatPtr(row.CoolingKWh),
			ClosedLoopTicks:   int(row.ClosedLoopTicks),
			TotalControlTicks: int(row.TotalControlTicks),
		})
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].TenantID != inputs[j].TenantID {
			return inputs[i].TenantID < inputs[j].TenantID
		}
		if inputs[i].SiteID != inputs[j].SiteID {
			return inputs[i].SiteID < inputs[j].SiteID
		}
		return inputs[i].Hour.Before(inputs[j].Hour)
	})
	return inputs, nil
}

func finiteFloatPtr(val *float64) *float64 {
	if val == nil || math.IsNaN(*val) || math.IsInf(*val, 0) {
		return nil
	}
	v := *val
	return &v
}

var (
	_ energy.ConditionInputReader  = (*Reader)(nil)
	_ energy.ConditionAnchorWriter = (*Writer)(nil)
)
