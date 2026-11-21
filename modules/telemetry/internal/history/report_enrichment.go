package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

type flexUint64 uint64

func (u *flexUint64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*u = 0
		return nil
	}
	if trimmed[0] == '"' {
		var raw string
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return err
		}
		if raw == "" {
			*u = 0
			return nil
		}
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		*u = flexUint64(parsed)
		return nil
	}
	var parsed uint64
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return err
	}
	*u = flexUint64(parsed)
	return nil
}

type meterBalanceAggregateRow struct {
	HourCount             flexUint64 `json:"hour_count"`
	TotalSiteKWh          float64    `json:"total_site_kwh"`
	TotalSubmeterKWh      float64    `json:"total_submeter_kwh"`
	SynthesizedPointCount flexUint64 `json:"synthesized_point_count"`
}

type layeredPointRow struct {
	EnergyType         string  `json:"energy_type"`
	AccountingScope    string  `json:"accounting_scope"`
	TelemetryKey       string  `json:"telemetry_key"`
	InSynthesizedTotal uint8   `json:"in_synthesized_total"`
	TotalKWh           float64 `json:"total_kwh"`
}

type conditionAnchorsAggregateRow struct {
	AnchorCount       flexUint64 `json:"anchor_count"`
	MeanWetBulbC      *float64   `json:"mean_wet_bulb_c"`
	MeanDryBulbC      *float64   `json:"mean_dry_bulb_c"`
	MeanLoadRatio     *float64   `json:"mean_load_ratio"`
	AIClosedLoopShare *float64   `json:"mean_ai_closed_loop_share"`
}

// ReportEnrichment aggregates ClickHouse P2 meter-balance, layered-efficiency, and condition-anchor
// diagnostics over [query.From, query.To).
func (client *Client) ReportEnrichment(ctx context.Context, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.ReportEnrichmentResponse, error) {
	if client == nil || client.endpoint == nil || client.httpClient == nil {
		return analyticsmodel.ReportEnrichmentResponse{}, errors.New("ClickHouse history client is closed")
	}
	if err := query.Validate(); err != nil {
		return analyticsmodel.ReportEnrichmentResponse{}, err
	}
	params := reportEnrichmentParams(query)

	var response analyticsmodel.ReportEnrichmentResponse
	meterBalance, synthesized, err := client.queryMeterBalance(ctx, params)
	if err != nil {
		return analyticsmodel.ReportEnrichmentResponse{}, err
	}
	response.MeterBalance = meterBalance

	layeredEfficiency, err := client.queryLayeredEfficiency(ctx, params, synthesized)
	if err != nil {
		return analyticsmodel.ReportEnrichmentResponse{}, err
	}
	response.LayeredEfficiency = layeredEfficiency

	conditionAnchors, err := client.queryConditionAnchors(ctx, params)
	if err != nil {
		return analyticsmodel.ReportEnrichmentResponse{}, err
	}
	response.ConditionAnchors = conditionAnchors

	return response, nil
}

func reportEnrichmentParams(query analyticsmodel.EnergySeriesQuery) url.Values {
	return url.Values{
		"param_tenant_id": {query.TenantID},
		"param_site_id":   {query.SiteID},
		"param_from_ts":   {formatClickHouseTime(query.From)},
		"param_to_ts":     {formatClickHouseTime(query.To)},
	}
}

func (client *Client) queryMeterBalance(ctx context.Context, params url.Values) (*analyticsmodel.MeterBalanceSummary, bool, error) {
	sql := `SELECT
  mb.hour_count AS hour_count,
  mb.total_site_kwh AS total_site_kwh,
  mb.total_submeter_kwh AS total_submeter_kwh,
  synth.submeter_count AS synthesized_point_count
FROM (
  SELECT
    toUInt64(count()) AS hour_count,
    toFloat64(ifNull(sum(b.site_kwh), 0.0)) AS total_site_kwh,
    toFloat64(ifNull(sum(b.submeter_sum_kwh), 0.0)) AS total_submeter_kwh
  FROM analytics.site_meter_balance_hourly AS b
  WHERE b.tenant_id = toUUID({tenant_id:UUID})
    AND b.site_id = toUUID({site_id:UUID})
    AND b.hour >= parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')
    AND b.hour < parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')
) AS mb
CROSS JOIN (
  SELECT toUInt64(count()) AS submeter_count
  FROM analytics.site_total_submeters_visible AS s
  WHERE s.tenant_id = toUUID({tenant_id:UUID})
    AND s.site_id = toUUID({site_id:UUID})
) AS synth
FORMAT JSONEachRow`
	payload, err := client.execute(ctx, sql, params)
	if err != nil {
		return nil, false, err
	}
	var row meterBalanceAggregateRow
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("decode ClickHouse meter balance summary: %w", err)
	}
	synthesized := row.SynthesizedPointCount > 0
	if row.HourCount == 0 {
		return nil, synthesized, nil
	}
	summary := analyticsmodel.ComputeMeterBalance(row.TotalSiteKWh, row.TotalSubmeterKWh, 0, synthesized)
	return &summary, synthesized, nil
}

func (client *Client) queryLayeredEfficiency(ctx context.Context, params url.Values, synthesized bool) (*analyticsmodel.LayeredEfficiency, error) {
	sql := `WITH
  hourly_points AS (
    SELECT
      e.point_id AS agg_point_id,
      e.accounting_scope AS agg_scope,
      e.energy_type AS agg_energy_type,
      toFloat64(sum(e.energy_kwh)) AS agg_kwh
    FROM analytics.energy_hourly_effective AS e
    WHERE e.tenant_id = toUUID({tenant_id:UUID})
      AND e.site_id = toUUID({site_id:UUID})
      AND e.quality = 'VALID'
      AND e.hour >= parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')
      AND e.hour < parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')
    GROUP BY e.point_id, e.accounting_scope, e.energy_type
  ),
  point_keys AS (
    SELECT
      f.point_id AS key_point_id,
      argMax(f.telemetry_key, tuple(f.fact_revision, f.projected_at, f.fact_id)) AS key_telemetry_key
    FROM analytics.energy_interval_facts_visible AS f
    WHERE f.tenant_id = toUUID({tenant_id:UUID})
      AND f.site_id = toUUID({site_id:UUID})
      AND f.energy_type = 'electricity'
      AND f.accounting_scope = 'BRANCH'
      AND f.period_end >= parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')
      AND f.period_end <= parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')
    GROUP BY f.point_id
  ),
  synth_points AS (
    SELECT
      s.point_id AS synth_point_id,
      toUInt8(1) AS is_synth_point
    FROM analytics.site_total_submeters_visible AS s
    WHERE s.tenant_id = toUUID({tenant_id:UUID})
      AND s.site_id = toUUID({site_id:UUID})
  )
SELECT
  hp.agg_energy_type AS energy_type,
  hp.agg_scope AS accounting_scope,
  ifNull(pk.key_telemetry_key, '') AS telemetry_key,
  ifNull(sp.is_synth_point, toUInt8(0)) AS in_synthesized_total,
  hp.agg_kwh AS total_kwh
FROM hourly_points AS hp
LEFT JOIN point_keys AS pk ON pk.key_point_id = hp.agg_point_id
LEFT JOIN synth_points AS sp ON sp.synth_point_id = hp.agg_point_id
FORMAT JSONEachRow`
	payload, err := client.execute(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	input := analyticsmodel.LayeredEfficiencyInput{SynthesizedTotal: synthesized}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var row layeredPointRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse layered efficiency: %w", err)
		}
		if math.IsNaN(row.TotalKWh) || math.IsInf(row.TotalKWh, 0) || row.TotalKWh < 0 {
			continue
		}
		switch {
		case row.EnergyType == "cooling" && row.AccountingScope == "SITE":
			input.CoolingKWh += row.TotalKWh
		case row.EnergyType == "electricity":
			if synthesized {
				if row.AccountingScope == "BRANCH" && row.InSynthesizedTotal > 0 {
					input.PlantElectricityKWh += row.TotalKWh
				}
			} else if row.AccountingScope == "SITE" {
				input.PlantElectricityKWh += row.TotalKWh
			}
			if row.AccountingScope == "BRANCH" {
				switch classifyEquipmentKey(row.TelemetryKey) {
				case "chiller":
					input.ChillerElectricityKWh += row.TotalKWh
				case "chwp":
					input.ChilledPumpElectricityKWh += row.TotalKWh
				case "cwp":
					input.CoolingPumpElectricityKWh += row.TotalKWh
				case "cooling_tower":
					input.CoolingTowerElectricityKWh += row.TotalKWh
				}
			}
		}
	}
	efficiency := analyticsmodel.ComputeLayeredEfficiency(input)
	if !efficiency.HasMetrics() {
		return nil, nil
	}
	return &efficiency, nil
}

func classifyEquipmentKey(telemetryKey string) string {
	lower := strings.ToLower(strings.TrimSpace(telemetryKey))
	switch {
	case lower == "":
		return ""
	case strings.Contains(lower, "chiller"):
		return "chiller"
	case strings.Contains(lower, "chwp") || strings.Contains(lower, "chilled_pump"):
		return "chwp"
	case strings.Contains(lower, "cwp") || strings.Contains(lower, "cooling_pump"):
		return "cwp"
	case strings.Contains(lower, "cooling_tower") || strings.Contains(lower, "tower"):
		return "cooling_tower"
	default:
		return ""
	}
}

func (client *Client) queryConditionAnchors(ctx context.Context, params url.Values) (*analyticsmodel.ReportConditionAnchorsSummary, error) {
	sql := `SELECT
  toUInt64(count()) AS anchor_count,
  avgIf(a.outdoor_wet_bulb_c, a.outdoor_wet_bulb_c IS NOT NULL AND isFinite(assumeNotNull(a.outdoor_wet_bulb_c))) AS mean_wet_bulb_c,
  avgIf(a.outdoor_dry_bulb_c, a.outdoor_dry_bulb_c IS NOT NULL AND isFinite(assumeNotNull(a.outdoor_dry_bulb_c))) AS mean_dry_bulb_c,
  avgIf(a.plant_load_ratio, a.plant_load_ratio IS NOT NULL AND isFinite(assumeNotNull(a.plant_load_ratio))) AS mean_load_ratio,
  avgIf(a.ai_closed_loop_share, a.ai_closed_loop_share IS NOT NULL AND isFinite(assumeNotNull(a.ai_closed_loop_share))) AS mean_ai_closed_loop_share
FROM analytics.site_condition_anchors_hourly_latest AS a
WHERE a.tenant_id = toUUID({tenant_id:UUID})
  AND a.site_id = toUUID({site_id:UUID})
  AND a.hour >= parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')
  AND a.hour < parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')
FORMAT JSONEachRow`
	payload, err := client.execute(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	var row conditionAnchorsAggregateRow
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("decode ClickHouse condition anchors: %w", err)
	}
	if row.AnchorCount == 0 {
		return nil, nil
	}
	summary := analyticsmodel.ReportConditionAnchorsSummary{
		MeanWetBulbC:      finiteFloatPtr(row.MeanWetBulbC),
		MeanDryBulbC:      finiteFloatPtr(row.MeanDryBulbC),
		MeanLoadRatio:     finiteFloatPtr(row.MeanLoadRatio),
		AIClosedLoopShare: finiteFloatPtr(row.AIClosedLoopShare),
	}
	if !summary.HasValues() {
		return nil, nil
	}
	return &summary, nil
}

func finiteFloatPtr(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	copyVal := *value
	return &copyVal
}
