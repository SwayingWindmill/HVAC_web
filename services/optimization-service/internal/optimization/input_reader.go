package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

const maximumOptimizationInputResponseBytes = int64(1 << 20)

// EnergySeries reads the query service's Energy Series as the optimization service's own Workload
// Principal (libs/energyseries).
type EnergySeries interface {
	Series(context.Context, analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error)
}

type InputReaderConfig struct {
	BaseURL          string
	Username         string
	Password         string
	HTTPClient       *http.Client
	Energy           EnergySeries
	LiveObservations TelemetryLiveObservationReader
	Now              func() time.Time
}

// ClickHouseInputReader reads the plant's latest temperatures from Telemetry History and the
// priced day from the query service.
type ClickHouseInputReader struct {
	baseURL          *url.URL
	username         string
	password         string
	client           *http.Client
	energy           EnergySeries
	liveObservations TelemetryLiveObservationReader
	now              func() time.Time
}

func NewClickHouseInputReader(config InputReaderConfig) (*ClickHouseInputReader, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("optimization input ClickHouse URL must be an HTTP(S) origin")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if config.Energy == nil {
		return nil, errors.New("optimization input reader requires the Energy Series reader")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &ClickHouseInputReader{
		baseURL:          parsed,
		username:         strings.TrimSpace(config.Username),
		password:         config.Password,
		client:           client,
		energy:           config.Energy,
		liveObservations: config.LiveObservations,
		now:              now,
	}, nil
}

func (reader *ClickHouseInputReader) ReadOptimizationState(ctx context.Context, query OptimizationStateQuery) (AuthoritativeState, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return AuthoritativeState{}, errors.New("optimization authoritative input reader is unavailable")
	}
	for field, value := range map[string]string{"tenantId": query.TenantID, "siteId": query.SiteID, "subjectId": query.SubjectID} {
		if !uuidPattern.MatchString(value) {
			return AuthoritativeState{}, fmt.Errorf("%s must be a UUID", field)
		}
	}
	zoneKeys := query.effectiveZoneKeys()
	if query.SubjectType != "SITE" || query.SubjectID != query.SiteID || query.At.IsZero() || query.SupplyTemperatureKey == "" || len(zoneKeys) == 0 {
		return AuthoritativeState{}, errors.New("optimization state query is invalid")
	}
	pricedDay, err := reader.readPricedDay(ctx, query)
	if err != nil {
		return AuthoritativeState{}, err
	}
	useLive, liveResult, liveAmbiguous := reader.readLiveObservationsBatch(ctx, query, zoneKeys)
	supplyTemperature, _, err := reader.readStateTelemetry(ctx, query, query.SupplyTemperatureKey, ReasonSupplyTempMissing, useLive, liveResult, liveAmbiguous)
	if err != nil {
		return AuthoritativeState{}, err
	}
	if reason, unhealthy := telemetryHealthBlockReason(supplyTemperature.HealthStatus, supplyTemperature.QualityReasons); unhealthy {
		return AuthoritativeState{}, unavailable(ReasonInputsInvalid, "authoritative telemetry %s is unhealthy (%s)", query.SupplyTemperatureKey, reason)
	}
	zoneTemperatures := make([]TelemetryEvidence, 0, len(zoneKeys))
	var worstZone TelemetryEvidence
	for idx, key := range zoneKeys {
		zoneObs, fromLive, zoneErr := reader.readStateTelemetry(ctx, query, key, ReasonZoneTempMissing, useLive, liveResult, liveAmbiguous)
		if zoneErr != nil {
			return AuthoritativeState{}, zoneErr
		}
		if !fromLive {
			if reason, unhealthy := telemetryHealthBlockReason(zoneObs.HealthStatus, zoneObs.QualityReasons); unhealthy {
				return AuthoritativeState{}, unavailable(ReasonInputsInvalid, "authoritative telemetry %s is unhealthy (%s)", key, reason)
			}
			if validErr := validateTelemetryEvidence(zoneObs, key, query.At); validErr != nil {
				return AuthoritativeState{}, unavailable(ReasonZoneTempMissing, "%v", validErr)
			}
		}
		zoneTemperatures = append(zoneTemperatures, zoneObs)
		if idx == 0 || zoneObs.Value > worstZone.Value {
			worstZone = zoneObs
		}
	}
	return AuthoritativeState{
		PricedDay:         pricedDay,
		SupplyTemperature: supplyTemperature,
		ZoneTemperature:   worstZone,
		ZoneTemperatures:  zoneTemperatures,
	}, nil
}

func (reader *ClickHouseInputReader) readLiveObservationsBatch(
	ctx context.Context,
	query OptimizationStateQuery,
	zoneKeys []string,
) (bool, OptimizationLiveObservationsResult, map[string]struct{}) {
	keys := make([]string, 0, 1+len(zoneKeys))
	keys = append(keys, query.SupplyTemperatureKey)
	keys = append(keys, zoneKeys...)
	return reader.readLiveObservationsKeys(ctx, query.TenantID, query.SiteID, query.At, keys)
}

func (reader *ClickHouseInputReader) readLiveObservationsKeys(
	ctx context.Context,
	tenantID, siteID string,
	at time.Time,
	keys []string,
) (bool, OptimizationLiveObservationsResult, map[string]struct{}) {
	if reader.liveObservations == nil {
		return false, OptimizationLiveObservationsResult{}, nil
	}
	if reader.now().UTC().Sub(at.UTC()) > DefaultPreparationMaxTelemetryAge {
		return false, OptimizationLiveObservationsResult{}, nil
	}
	liveResult, err := reader.liveObservations.ReadLiveObservations(ctx, tenantID, siteID, keys)
	if err != nil {
		return false, OptimizationLiveObservationsResult{}, nil
	}
	var ambiguous map[string]struct{}
	if len(liveResult.AmbiguousKeys) > 0 {
		ambiguous = make(map[string]struct{}, len(liveResult.AmbiguousKeys))
		for _, raw := range liveResult.AmbiguousKeys {
			if clean := strings.TrimSpace(raw); clean != "" {
				ambiguous[clean] = struct{}{}
			}
		}
	}
	return true, liveResult, ambiguous
}

func (reader *ClickHouseInputReader) readStateTelemetry(
	ctx context.Context,
	query OptimizationStateQuery,
	telemetryKey string,
	missing PreparationReason,
	useLive bool,
	liveResult OptimizationLiveObservationsResult,
	liveAmbiguous map[string]struct{},
) (TelemetryEvidence, bool, error) {
	if useLive {
		if _, ambiguous := liveAmbiguous[telemetryKey]; ambiguous {
			return TelemetryEvidence{}, false, unavailable(missing, "authoritative telemetry %s resolves to multiple Points", telemetryKey)
		}
		if liveObs, found := liveResult.Observations[telemetryKey]; found {
			evidence, usable, err := convertLiveObservation(liveObs, telemetryKey, query.At)
			if err != nil {
				return TelemetryEvidence{}, false, err
			}
			if usable {
				if validErr := validateTelemetryEvidence(evidence, telemetryKey, query.At); validErr != nil {
					return TelemetryEvidence{}, false, unavailable(missing, "%v", validErr)
				}
				return evidence, true, nil
			}
		}
	}
	evidence, err := reader.readTelemetry(ctx, query, telemetryKey, missing)
	return evidence, false, err
}

func convertLiveObservation(obs OptimizationLiveObservation, expectedKey string, at time.Time) (TelemetryEvidence, bool, error) {
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(obs.SampledAt))
	if err != nil {
		return TelemetryEvidence{}, false, nil
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(obs.ReceivedAt))
	if err != nil {
		return TelemetryEvidence{}, false, nil
	}
	if !finite(obs.Value) {
		return TelemetryEvidence{}, false, nil
	}
	sampledUTC := sampledAt.UTC()
	receivedUTC := receivedAt.UTC()
	atUTC := at.UTC()
	if sampledUTC.After(atUTC) || receivedUTC.After(atUTC) {
		return TelemetryEvidence{}, false, nil
	}
	evidence := TelemetryEvidence{
		ObservationID:   obs.ObservationID,
		DeviceID:        obs.DeviceID,
		PointID:         obs.PointID,
		TelemetryKey:    obs.TelemetryKey,
		PointRevision:   obs.PointRevision,
		SampledAt:       sampledUTC,
		ReceivedAt:      receivedUTC,
		Value:           obs.Value,
		Unit:            obs.Unit,
		Quality:         obs.Quality,
		QualityReasons:  slices.Clone(obs.QualityReasons),
		HealthStatus:    obs.HealthStatus,
		SourceEventID:   obs.SourceEventID,
		SourcePartition: obs.SourcePartition,
		SourceOffset:    obs.SourceOffset,
	}
	for _, reasonSet := range [][]string{evidence.QualityReasons, obs.HealthReasons} {
		if reason, unhealthy := telemetryHealthBlockReason(evidence.HealthStatus, reasonSet); unhealthy {
			return TelemetryEvidence{}, true, unavailable(ReasonInputsInvalid, "authoritative telemetry %s is unhealthy (%s)", expectedKey, reason)
		}
	}
	return evidence, true, nil
}

// pricedDays is how far back a fully priced day is looked for.
const pricedDays = 7

// readPricedDay is the latest Site-local day before the capture that has ended, that all its energy
// has reached and every kWh of which the query service priced. It prices that day's energy at its
// own mix of Tariff periods, which the baseline's cost per kWh comes from.
func (reader *ClickHouseInputReader) readPricedDay(ctx context.Context, query OptimizationStateQuery) (EnergyDayEvidence, error) {
	location, err := time.LoadLocation(query.Timezone)
	if err != nil {
		return EnergyDayEvidence{}, fmt.Errorf("site timezone: %w", err)
	}
	local := query.At.In(location)
	to := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	series, err := reader.energy.Series(ctx, analyticsmodel.EnergySeriesQuery{
		TenantID: query.TenantID, SiteID: query.SiteID, EnergyType: analyticsmodel.EnergyTypeElectricity,
		Granularity: analyticsmodel.GranularityDay, Timezone: query.Timezone, From: to.AddDate(0, 0, -pricedDays), To: to,
		QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	})
	if err != nil {
		return EnergyDayEvidence{}, fmt.Errorf("read the Site's priced days: %w", err)
	}
	watermark := series.Metadata.DataWatermark
	for index := len(series.Points) - 1; index >= 0; index-- {
		point := series.Points[index]
		if point.Cost == nil || point.Cost.UnpricedKWh > 0 || point.EnergyKWh <= 0 || watermark == nil || point.PeriodEnd.After(*watermark) {
			continue
		}
		return EnergyDayEvidence{
			PeriodStart: point.PeriodStart.UTC(), PeriodEnd: point.PeriodEnd.UTC(), EnergyKWh: point.EnergyKWh, CostCNY: point.Cost.AmountCNY,
			DatasetRevision: series.Metadata.DatasetRevision, DataWatermark: watermark.UTC(),
		}, nil
	}
	return EnergyDayEvidence{}, unavailable(ReasonPricedDayMissing, "the Site has no fully priced complete day in the last %d days", pricedDays)
}

func (reader *ClickHouseInputReader) readTelemetry(ctx context.Context, query OptimizationStateQuery, telemetryKey string, missing PreparationReason) (TelemetryEvidence, error) {
	atUTC := query.At.UTC()
	earliest := atUTC.Add(-DefaultPreparationMaxTelemetryAge)
	sql := fmt.Sprintf(`SELECT
 toString(observation_id) AS observation_id,
 toString(device_id) AS device_id,
 toString(point_id) AS point_id,
 telemetry_key,
 assumeNotNull(point_revision) AS point_revision,
 formatDateTime(sampled_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS sampled_at,
 formatDateTime(received_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS received_at,
 assumeNotNull(value_number) AS value,
 assumeNotNull(unit) AS unit,
 quality,
 quality_reasons,
 toString(source_event_id) AS source_event_id,
 source_partition,
 source_offset
FROM (
 SELECT *,row_number() OVER (PARTITION BY point_id ORDER BY sampled_at DESC,received_at DESC,source_offset DESC,toString(observation_id) ASC) AS point_rank
 FROM telemetry_history.observations AS observation
 WHERE observation.tenant_id=toUUID('%s')
   AND observation.site_id=toUUID('%s')
   AND observation.telemetry_key='%s'
   AND observation.sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
   AND observation.sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND observation.received_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND observation.acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
   AND observation.value_number IS NOT NULL
   AND observation.device_id IS NOT NULL
   AND observation.point_id IS NOT NULL
   AND observation.point_revision IS NOT NULL
   AND observation.unit IS NOT NULL
)
WHERE point_rank=1
ORDER BY sampled_at DESC,received_at DESC,source_offset DESC,toString(observation_id) ASC
LIMIT 2
FORMAT JSONEachRow`, query.TenantID, query.SiteID, clickHouseString(telemetryKey), formatOptimizationTime(earliest), formatOptimizationTime(atUTC), formatOptimizationTime(atUTC))
	payload, err := reader.query(ctx, sql)
	if err != nil {
		return TelemetryEvidence{}, fmt.Errorf("query authoritative Optimization telemetry %s: %w", telemetryKey, err)
	}
	type row struct {
		ObservationID   string   `json:"observation_id"`
		DeviceID        string   `json:"device_id"`
		PointID         string   `json:"point_id"`
		TelemetryKey    string   `json:"telemetry_key"`
		PointRevision   uint64   `json:"point_revision"`
		SampledAt       string   `json:"sampled_at"`
		ReceivedAt      string   `json:"received_at"`
		Value           float64  `json:"value"`
		Unit            string   `json:"unit"`
		Quality         string   `json:"quality"`
		QualityReasons  []string `json:"quality_reasons"`
		SourceEventID   string   `json:"source_event_id"`
		SourcePartition string   `json:"source_partition"`
		SourceOffset    uint64   `json:"source_offset"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	rows := make([]row, 0, 2)
	for {
		var value row
		if err = decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return TelemetryEvidence{}, err
		}
		rows = append(rows, value)
	}
	if len(rows) == 0 {
		return TelemetryEvidence{}, unavailable(missing, "authoritative telemetry %s has no usable observation", telemetryKey)
	}
	if len(rows) > 1 {
		return TelemetryEvidence{}, unavailable(missing, "authoritative telemetry %s resolves to multiple Points", telemetryKey)
	}
	sampledAt, err := time.Parse(time.RFC3339Nano, rows[0].SampledAt)
	if err != nil {
		return TelemetryEvidence{}, err
	}
	if atUTC.Sub(sampledAt.UTC()) > DefaultPreparationMaxTelemetryAge {
		return TelemetryEvidence{}, unavailable(missing, "authoritative telemetry %s latest observation is stale (sampled_at %s)", telemetryKey, sampledAt.UTC().Format(time.RFC3339))
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, rows[0].ReceivedAt)
	if err != nil {
		return TelemetryEvidence{}, err
	}
	return TelemetryEvidence{
		ObservationID: rows[0].ObservationID, DeviceID: rows[0].DeviceID, PointID: rows[0].PointID,
		TelemetryKey: rows[0].TelemetryKey, PointRevision: rows[0].PointRevision,
		SampledAt: sampledAt.UTC(), ReceivedAt: receivedAt.UTC(), Value: rows[0].Value, Unit: rows[0].Unit, Quality: rows[0].Quality,
		QualityReasons: rows[0].QualityReasons,
		SourceEventID:  rows[0].SourceEventID, SourcePartition: rows[0].SourcePartition, SourceOffset: rows[0].SourceOffset,
	}, nil
}

// ReadShadowEvaluation reads the SHADOW evaluation recorded beside a specific published run.
func (reader *ClickHouseInputReader) ReadShadowEvaluation(ctx context.Context, query ShadowEvaluationQuery) (ShadowEvaluationRecord, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return ShadowEvaluationRecord{}, errors.New("optimization shadow evaluation reader is unavailable")
	}
	for field, value := range map[string]string{
		"tenantId": query.TenantID, "siteId": query.SiteID, "optimizationRunId": query.OptimizationRunID,
	} {
		if !uuidPattern.MatchString(value) {
			return ShadowEvaluationRecord{}, fmt.Errorf("%s must be a UUID", field)
		}
	}
	sql := fmt.Sprintf(`SELECT
 toString(evaluation_id) AS evaluationId,
 toString(optimization_run_id) AS optimizationRunId,
 toString(recommendation_id) AS recommendationId,
 formatDateTime(generated_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS generatedAt,
 evaluation_json AS evaluationJSON
FROM analytics.optimization_evaluations
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND optimization_run_id = toUUID('%s')
  AND quality = 'SHADOW'
ORDER BY generated_at DESC, evaluation_id DESC
LIMIT 1
FORMAT JSONEachRow`,
		clickHouseString(query.TenantID), clickHouseString(query.SiteID), clickHouseString(query.OptimizationRunID))
	payload, err := reader.query(ctx, sql)
	if err != nil {
		return ShadowEvaluationRecord{}, fmt.Errorf("query shadow evaluation: %w", err)
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return ShadowEvaluationRecord{}, ErrShadowEvaluationNotFound
	}
	var row struct {
		EvaluationID      string `json:"evaluationId"`
		OptimizationRunID string `json:"optimizationRunId"`
		RecommendationID  string `json:"recommendationId"`
		GeneratedAt       string `json:"generatedAt"`
		EvaluationJSON    string `json:"evaluationJSON"`
	}
	if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&row); err != nil {
		return ShadowEvaluationRecord{}, fmt.Errorf("decode shadow evaluation row: %w", err)
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, row.GeneratedAt)
	if err != nil {
		return ShadowEvaluationRecord{}, fmt.Errorf("parse shadow evaluation timestamp: %w", err)
	}
	return ShadowEvaluationRecord{
		EvaluationID:      row.EvaluationID,
		OptimizationRunID: row.OptimizationRunID,
		RecommendationID:  row.RecommendationID,
		GeneratedAt:       generatedAt.UTC(),
		EvaluationJSON:    row.EvaluationJSON,
	}, nil
}

func (reader *ClickHouseInputReader) query(ctx context.Context, sql string) ([]byte, error) {
	return reader.queryLimited(ctx, sql, maximumOptimizationInputResponseBytes)
}

func (reader *ClickHouseInputReader) queryLimited(ctx context.Context, sql string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, reader.baseURL.String(), strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Accept", "application/x-ndjson")
	if reader.username != "" {
		request.SetBasicAuth(reader.username, reader.password)
	}
	response, err := reader.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("authoritative Optimization input response exceeds %d MiB", limit>>20)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("ClickHouse returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func clickHouseString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

func formatOptimizationTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
