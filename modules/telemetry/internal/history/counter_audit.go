package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetry"
)

const (
	counterAuditLookback = 24 * time.Hour
	counterAuditRowLimit = 50000
)

type counterAuditRow struct {
	ObservationID          string   `json:"observation_id"`
	DeviceID               string   `json:"device_id"`
	PointID                string   `json:"point_id"`
	TelemetryKey           string   `json:"telemetry_key"`
	PointRevision          uint64   `json:"point_revision"`
	Unit                   *string  `json:"unit"`
	CounterDecreaseMode    *string  `json:"counter_decrease_mode"`
	CounterRolloverModulus *float64 `json:"counter_rollover_modulus"`
	ValueNumber            float64  `json:"value_number"`
	SampledAt              string   `json:"sampled_at"`
	Quality                string   `json:"quality"`
	QualityReasons         []string `json:"quality_reasons"`
}

type counterAuditObsMeta struct {
	deviceID     string
	telemetryKey string
}

func (client *Client) QueryCounterAudit(ctx context.Context, query telemetryhistorymodel.CounterAuditQuery) (telemetryhistorymodel.CounterAuditResponse, error) {
	if client == nil || client.endpoint == nil || client.httpClient == nil {
		return telemetryhistorymodel.CounterAuditResponse{}, errors.New("ClickHouse history client is closed")
	}
	canonical, err := query.Canonical()
	if err != nil {
		return telemetryhistorymodel.CounterAuditResponse{}, err
	}
	sqlText, params := client.counterAuditQuery(canonical)
	payload, err := client.execute(ctx, sqlText, params)
	if err != nil {
		return telemetryhistorymodel.CounterAuditResponse{}, err
	}
	return buildCounterAuditResponse(canonical, payload)
}

func (client *Client) counterAuditQuery(canonical telemetryhistorymodel.CounterAuditQuery) (string, url.Values) {
	lookbackFrom := canonical.From.Add(-counterAuditLookback)
	params := url.Values{
		"param_tenant_id":        {canonical.TenantID},
		"param_site_id":          {canonical.SiteID},
		"param_lookback_from_ts": {formatClickHouseTime(lookbackFrom)},
		"param_to_ts":            {formatClickHouseTime(canonical.To)},
	}
	extraFilters := ""
	if canonical.DeviceID != nil {
		params.Set("param_device_id", *canonical.DeviceID)
		extraFilters += "\n  AND history_source.device_id = toUUID({device_id:UUID})"
	}
	if canonical.PointID != nil {
		params.Set("param_point_id", *canonical.PointID)
		extraFilters += "\n  AND history_source.point_id = toUUID({point_id:UUID})"
	}

	sqlText := fmt.Sprintf(`SELECT
  toString(history_source.observation_id) AS observation_id,
  toString(assumeNotNull(history_source.device_id)) AS device_id,
  toString(assumeNotNull(history_source.point_id)) AS point_id,
  history_source.telemetry_key AS telemetry_key,
  assumeNotNull(history_source.point_revision) AS point_revision,
  history_source.unit AS unit,
  history_source.counter_decrease_mode AS counter_decrease_mode,
  history_source.counter_rollover_modulus AS counter_rollover_modulus,
  assumeNotNull(history_source.value_number) AS value_number,
  formatDateTime(history_source.sampled_at, '%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ', 'UTC') AS sampled_at,
  history_source.quality AS quality,
  history_source.quality_reasons AS quality_reasons
FROM %s.%s AS history_source
WHERE history_source.tenant_id = toUUID({tenant_id:UUID})
  AND history_source.site_id = toUUID({site_id:UUID})
  AND history_source.sampled_at >= parseDateTime64BestEffort({lookback_from_ts:String}, 3, 'UTC')
  AND history_source.sampled_at < parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')%s
  AND history_source.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
  AND history_source.point_type = 'COUNTER'
  AND history_source.point_id IS NOT NULL
  AND history_source.point_revision IS NOT NULL
  AND history_source.device_id IS NOT NULL
  AND history_source.value_type = 'NUMBER'
  AND history_source.value_number IS NOT NULL
  AND isFinite(history_source.value_number)
ORDER BY (history_source.point_id, history_source.sampled_at, history_source.observation_id)
LIMIT %d
FORMAT JSONEachRow`, client.database, client.table, extraFilters, counterAuditRowLimit)

	return sqlText, params
}

func buildCounterAuditResponse(canonical telemetryhistorymodel.CounterAuditQuery, payload []byte) (telemetryhistorymodel.CounterAuditResponse, error) {
	pointSamples := map[string][]telemetry.CounterSample{}
	pointIDs := make([]string, 0)
	obsMeta := map[string]counterAuditObsMeta{}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var row counterAuditRow
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return telemetryhistorymodel.CounterAuditResponse{}, fmt.Errorf("decode ClickHouse counter audit row: %w", err)
		}
		sampledAt, err := parseClickHouseTime(row.SampledAt)
		if err != nil {
			return telemetryhistorymodel.CounterAuditResponse{}, fmt.Errorf("decode ClickHouse counter audit sampled_at: %w", err)
		}
		if _, seen := pointSamples[row.PointID]; !seen {
			pointIDs = append(pointIDs, row.PointID)
		}
		pointSamples[row.PointID] = append(pointSamples[row.PointID], counterSampleFromRow(canonical, row, sampledAt))
		obsMeta[row.ObservationID] = counterAuditObsMeta{
			deviceID:     row.DeviceID,
			telemetryKey: row.TelemetryKey,
		}
	}

	slices.Sort(pointIDs)

	anomaliesOnly := canonical.AnomaliesOnly == nil || *canonical.AnomaliesOnly
	var summary telemetryhistorymodel.CounterAuditSummary
	items := make([]telemetryhistorymodel.CounterAuditItem, 0)

	for _, pointID := range pointIDs {
		deltas, err := telemetry.CleanCounterSeries(telemetry.CounterValidationConfig{}, pointSamples[pointID])
		if err != nil {
			return telemetryhistorymodel.CounterAuditResponse{}, fmt.Errorf("clean counter series for point %s: %w", pointID, err)
		}
		for _, d := range deltas {
			if d.SampledAt.Before(canonical.From) || !d.SampledAt.Before(canonical.To) {
				continue
			}
			summary.TotalSamples++
			isAnomaly := isCounterAuditAnomaly(d)
			if isAnomaly {
				summary.AnomalyCount++
			}
			incrementCounterAuditSummaryTransition(&summary, d.Transition)

			if anomaliesOnly && !isAnomaly {
				continue
			}
			meta := obsMeta[d.ObservationID]
			items = append(items, counterAuditItemFromDelta(d, meta))
		}
	}

	slices.SortFunc(items, compareCounterAuditItems)

	truncated := len(items) > canonical.Limit
	if truncated {
		items = items[:canonical.Limit]
	}

	response := telemetryhistorymodel.CounterAuditResponse{
		SiteID:    canonical.SiteID,
		From:      canonical.From,
		To:        canonical.To,
		Summary:   summary,
		Items:     items,
		Truncated: truncated,
	}
	if err := response.ValidateFor(canonical); err != nil {
		return telemetryhistorymodel.CounterAuditResponse{}, fmt.Errorf("validate counter audit response: %w", err)
	}
	return response, nil
}

func counterSampleFromRow(canonical telemetryhistorymodel.CounterAuditQuery, row counterAuditRow, sampledAt time.Time) telemetry.CounterSample {
	var unit string
	if row.Unit != nil {
		unit = *row.Unit
	}
	var decreaseMode string
	if row.CounterDecreaseMode != nil {
		decreaseMode = *row.CounterDecreaseMode
	}
	var rolloverModulus float64
	if row.CounterRolloverModulus != nil {
		rolloverModulus = *row.CounterRolloverModulus
	}
	reasons := make([]telemetry.QualityReason, 0, len(row.QualityReasons))
	for _, reason := range row.QualityReasons {
		reasons = append(reasons, telemetry.QualityReason(reason))
	}
	return telemetry.CounterSample{
		TenantID:        canonical.TenantID,
		SiteID:          canonical.SiteID,
		PointID:         row.PointID,
		ObservationID:   row.ObservationID,
		PointRevision:   int64(row.PointRevision),
		Unit:            unit,
		DecreaseMode:    decreaseMode,
		RolloverModulus: rolloverModulus,
		SampledAt:       sampledAt.UTC(),
		Value:           row.ValueNumber,
		Quality:         telemetry.ObservationQuality(row.Quality),
		QualityReasons:  reasons,
	}
}

func isCounterAuditAnomaly(d telemetry.CleanedCounterDelta) bool {
	if d.Quarantined || d.Quality != telemetry.QualityGood {
		return true
	}
	switch d.Transition {
	case telemetry.TransitionInitial, telemetry.TransitionIncrease, telemetry.TransitionUnchanged:
		return false
	default:
		return true
	}
}

func incrementCounterAuditSummaryTransition(summary *telemetryhistorymodel.CounterAuditSummary, transition telemetry.CounterTransitionType) {
	switch transition {
	case telemetry.TransitionInvalidZeroDrop:
		summary.ZeroDropCount++
	case telemetry.TransitionInvalidSpike:
		summary.SpikeCount++
	case telemetry.TransitionReset:
		summary.ResetCount++
	case telemetry.TransitionRollover:
		summary.RolloverCount++
	case telemetry.TransitionInvalidDecrease:
		summary.InvalidDecreaseCount++
	}
}

func counterAuditItemFromDelta(d telemetry.CleanedCounterDelta, meta counterAuditObsMeta) telemetryhistorymodel.CounterAuditItem {
	var cleanedDeltaKWh *float64
	if !d.Quarantined && d.HasDelta {
		value := d.Delta
		if factor, ok := telemetry.CounterEnergyUnitToKWhFactor(d.Unit); ok {
			value = d.Delta * factor
		}
		cleanedDeltaKWh = &value
	}

	periodStart := d.SampledAt.UTC()
	var rawPrevious *float64
	if d.HasPrevious {
		periodStart = d.PreviousSampledAt.UTC()
		prev := d.PreviousValue
		rawPrevious = &prev
	}

	reasons := make([]string, 0, len(d.Reasons))
	for _, reason := range d.Reasons {
		reasons = append(reasons, string(reason))
	}

	return telemetryhistorymodel.CounterAuditItem{
		PointID:         d.PointID,
		DeviceID:        meta.deviceID,
		TelemetryKey:    meta.telemetryKey,
		PeriodStart:     periodStart,
		PeriodEnd:       d.SampledAt.UTC(),
		RawPrevious:     rawPrevious,
		RawCurrent:      d.Value,
		CleanedDeltaKWh: cleanedDeltaKWh,
		TransitionType:  string(d.Transition),
		Quality:         string(d.Quality),
		QualityReasons:  reasons,
		Unit:            d.Unit,
	}
}

func compareCounterAuditItems(a, b telemetryhistorymodel.CounterAuditItem) int {
	if !a.PeriodEnd.Equal(b.PeriodEnd) {
		if a.PeriodEnd.After(b.PeriodEnd) {
			return -1
		}
		return 1
	}
	if a.PointID < b.PointID {
		return -1
	}
	if a.PointID > b.PointID {
		return 1
	}
	if a.PeriodStart.Before(b.PeriodStart) {
		return -1
	}
	if a.PeriodStart.After(b.PeriodStart) {
		return 1
	}
	return 0
}
