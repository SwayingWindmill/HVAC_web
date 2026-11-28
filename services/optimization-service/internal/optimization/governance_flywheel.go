package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	ControlModeAIClosedLoop   = "AI_CLOSED_LOOP"
	ControlModeAIShadow       = "AI_SHADOW"
	ControlModeManualOverride = "MANUAL_OVERRIDE"
	ControlModeRuleBaseline   = "RULE_BASELINE"

	NegativeSampleGuardrailClamped  = "GUARDRAIL_CLAMPED"
	NegativeSampleGuardrailVetoed   = "GUARDRAIL_VETOED"
	NegativeSampleManualOverride    = "MANUAL_OVERRIDE"
	NegativeSampleTrackingDeviation = "TRACKING_DEVIATION"
	NegativeSampleMonitorTripped    = "MONITOR_TRIPPED"

	DefaultGovernanceWindowDays = 14
	MaxGovernanceWindowDays     = 365
)

// OptimizationFeatureSnapshot captures the typed physical plant state and active guardrail bounds
// observed at a control tick or negative-sample event.
type OptimizationFeatureSnapshot struct {
	SupplyTempC        *float64 `json:"supplyTempC,omitempty"`
	ZoneMaxTempC       *float64 `json:"zoneMaxTempC,omitempty"`
	OutdoorWetBulbC    *float64 `json:"outdoorWetBulbC,omitempty"`
	CoolingLoadKW      *float64 `json:"coolingLoadKW,omitempty"`
	ChillerPowerKW     *float64 `json:"chillerPowerKW,omitempty"`
	ObservedCOP        *float64 `json:"observedCOP,omitempty"`
	RunningChillers    int      `json:"runningChillers"`
	CabinetLocalManual bool     `json:"cabinetLocalManual"`
	DeviceFaultActive  bool     `json:"deviceFaultActive"`
	LeaseDeadbandC     float64  `json:"leaseDeadbandC"`
	SupplyTempMinC     float64  `json:"supplyTempMinC"`
	SupplyTempMaxC     float64  `json:"supplyTempMaxC"`
}

// ControlTickRecord records a single AutoController evaluation cycle mode and plant observation
// aligned with core_registry.optimization_control_ticks.
type ControlTickRecord struct {
	ID                     string
	TenantID               string
	SiteID                 string
	OptimizationRunID      *string
	RecommendationID       *string
	DecisionID             string
	ControlMode            string
	LoopKey                string
	SolverServingRef       string
	DeploymentRevisionID   *string
	BaselineSupplyTempC    *float64
	RecommendedSupplyTempC *float64
	AppliedSupplyTempC     *float64
	FeatureSnapshot        OptimizationFeatureSnapshot
	TickAt                 time.Time
}

// NegativeSampleRecord captures a clamped, vetoed, manual-override, deviation, or tripped event
// aligned with core_registry.optimization_negative_samples.
type NegativeSampleRecord struct {
	ID                    string
	TenantID              string
	SiteID                string
	RecommendationID      *string
	MonitorID             *string
	DecisionID            string
	TraceID               string
	SampleKind            string
	ReasonCode            string
	SolverServingRef      string
	RawRecommendedValue   *float64
	AppliedOrClampedValue *float64
	GuardrailTrace        json.RawMessage
	FeatureSnapshot       OptimizationFeatureSnapshot
	OccurredAt            time.Time
}

// GovernanceFlywheelStore persists and queries control-mode ticks and flywheel negative samples.
type GovernanceFlywheelStore interface {
	RecordControlTick(ctx context.Context, tick ControlTickRecord) error
	RecordNegativeSample(ctx context.Context, sample NegativeSampleRecord) error
	ListControlTicks(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]ControlTickRecord, error)
	ListNegativeSamples(ctx context.Context, tenantID, siteID string, from, to time.Time, limit int) ([]NegativeSampleRecord, error)
}

// SiteVisibilityChecker verifies whether a Site exists under the Tenant's authorized scope.
type SiteVisibilityChecker interface {
	CheckSiteVisible(ctx context.Context, tenantID, siteID string) (bool, error)
}

// GovernanceTickCounts summarizes control mode ticks over the evaluation window.
type GovernanceTickCounts struct {
	AIClosedLoop   int `json:"aiClosedLoop"`
	AIShadow       int `json:"aiShadow"`
	RuleBaseline   int `json:"ruleBaseline"`
	ManualOverride int `json:"manualOverride"`
}

// GovernanceFlywheelSummary summarizes negative-sample counts across flywheel categories.
type GovernanceFlywheelSummary struct {
	TotalSamples      int `json:"totalSamples"`
	GuardrailClamped  int `json:"guardrailClamped"`
	GuardrailVetoed   int `json:"guardrailVetoed"`
	ManualOverride    int `json:"manualOverride"`
	MonitorTripped    int `json:"monitorTripped"`
	TrackingDeviation int `json:"trackingDeviation"`
}

// GovernanceSummary is the site-level AI governance, routing, bivariate M&V COP, and flywheel response.
type GovernanceSummary struct {
	TenantID      string                             `json:"tenantId"`
	SiteID        string                             `json:"siteId"`
	WindowDays    int                                `json:"windowDays"`
	GeneratedAt   time.Time                          `json:"generatedAt"`
	ActiveSolver  ActiveSolverSelection              `json:"activeSolver"`
	TickCounts    GovernanceTickCounts               `json:"tickCounts"`
	COPComparison analyticsmodel.BivariateCOPSummary `json:"copComparison"`
	Flywheel      GovernanceFlywheelSummary          `json:"flywheel"`
}

// CheckSiteVisible reports whether siteID exists within tenantID under RLS.
func (store *PostgresStore) CheckSiteVisible(ctx context.Context, tenantID, siteID string) (bool, error) {
	if store == nil || store.pool == nil {
		return false, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return false, errors.New("optimization site scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return false, err
	}
	visible, err := txPreparationReader{tx: tx}.checkSiteVisible(ctx, tenant, site)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return visible, nil
}

// RecordControlTick persists a single control-mode tick into core_registry.optimization_control_ticks.
func (store *PostgresStore) RecordControlTick(ctx context.Context, tick ControlTickRecord) error {
	if store == nil || store.pool == nil {
		return errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tick.TenantID)
	site := strings.TrimSpace(tick.SiteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return errors.New("control tick scope is invalid")
	}
	mode := normalizeControlTickMode(tick.ControlMode)
	if mode == "" {
		return fmt.Errorf("control tick mode %q is invalid", tick.ControlMode)
	}
	tickAt := tick.TickAt.UTC()
	if tickAt.IsZero() {
		tickAt = time.Now().UTC()
	}
	id := strings.TrimSpace(tick.ID)
	if id == "" {
		generated, err := optimizationUUIDv7(tickAt)
		if err != nil {
			return err
		}
		id = generated
	} else if !uuidv7.IsValidLowerTrimmed(id) {
		return errors.New("control tick id must be UUIDv7")
	}
	runID, err := optionalUUIDPtr(tick.OptimizationRunID)
	if err != nil {
		return fmt.Errorf("control tick optimizationRunId: %w", err)
	}
	recID, err := optionalUUIDPtr(tick.RecommendationID)
	if err != nil {
		return fmt.Errorf("control tick recommendationId: %w", err)
	}
	decisionIDPtr, err := optionalUUIDStringPtr(tick.DecisionID)
	if err != nil {
		return fmt.Errorf("control tick decisionId: %w", err)
	}
	deploymentRevID, err := optionalUUIDPtr(tick.DeploymentRevisionID)
	if err != nil {
		return fmt.Errorf("control tick deploymentRevisionId: %w", err)
	}
	loopKey := normalizeSolverLoopKey(tick.LoopKey)
	servingRef := sanitizeServingRef(tick.SolverServingRef)
	if servingRef == "" {
		servingRef = DefaultBuiltinSolverRef
	}
	supplyTempC := finiteFloatPtr(tick.FeatureSnapshot.SupplyTempC)
	if supplyTempC == nil {
		supplyTempC = finiteFloatPtr(tick.BaselineSupplyTempC)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return err
	}

	if _, err = tx.Exec(ctx, `
INSERT INTO core_registry.optimization_control_ticks (
  id, tenant_id, site_id, optimization_run_id, recommendation_id, decision_id,
  control_mode, loop_key, solver_serving_ref, deployment_revision_id,
  outdoor_wet_bulb_c, cooling_load_kw, chiller_power_kw, observed_cop,
  supply_temp_c, recommended_supply_temp_c, applied_supply_temp_c,
  recorded_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7, $8, $9, $10::uuid,
  $11, $12, $13, $14,
  $15, $16, $17,
  $18, $18
)`,
		id, tenant, site, runID, recID, decisionIDPtr,
		mode, loopKey, servingRef, deploymentRevID,
		finiteFloatPtr(tick.FeatureSnapshot.OutdoorWetBulbC),
		finiteFloatPtr(tick.FeatureSnapshot.CoolingLoadKW),
		finiteFloatPtr(tick.FeatureSnapshot.ChillerPowerKW),
		finiteFloatPtr(tick.FeatureSnapshot.ObservedCOP),
		supplyTempC,
		finiteFloatPtr(tick.RecommendedSupplyTempC),
		finiteFloatPtr(tick.AppliedSupplyTempC),
		tickAt,
	); err != nil {
		return fmt.Errorf("insert optimization control tick: %w", err)
	}
	return tx.Commit(ctx)
}

// RecordNegativeSample persists a typed negative flywheel sample into core_registry.optimization_negative_samples.
func (store *PostgresStore) RecordNegativeSample(ctx context.Context, sample NegativeSampleRecord) error {
	if store == nil || store.pool == nil {
		return errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(sample.TenantID)
	site := strings.TrimSpace(sample.SiteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return errors.New("negative sample scope is invalid")
	}
	sampleKind := resolveNegativeSampleKind(sample)
	if sampleKind == "" {
		return fmt.Errorf("negative sample kind/reason (%q/%q) is invalid", sample.SampleKind, sample.ReasonCode)
	}
	reasonCode := strings.TrimSpace(sample.ReasonCode)
	if reasonCode == "" {
		reasonCode = sampleKind
	}
	occurredAt := sample.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	id := strings.TrimSpace(sample.ID)
	if id == "" {
		generated, err := optimizationUUIDv7(occurredAt)
		if err != nil {
			return err
		}
		id = generated
	} else if !uuidv7.IsValidLowerTrimmed(id) {
		return errors.New("negative sample id must be UUIDv7")
	}
	recID, err := optionalUUIDPtr(sample.RecommendationID)
	if err != nil {
		return fmt.Errorf("negative sample recommendationId: %w", err)
	}
	monID, err := optionalUUIDPtr(sample.MonitorID)
	if err != nil {
		return fmt.Errorf("negative sample monitorId: %w", err)
	}
	decisionIDPtr, err := optionalUUIDStringPtr(sample.DecisionID)
	if err != nil {
		return fmt.Errorf("negative sample decisionId: %w", err)
	}
	traceID := intelligencemodel.NormalizeTraceID32(sample.TraceID)
	servingRef := sanitizeServingRef(sample.SolverServingRef)
	if servingRef == "" {
		servingRef = DefaultBuiltinSolverRef
	}
	traceBytes := bytes.TrimSpace(sample.GuardrailTrace)
	if len(traceBytes) == 0 || string(traceBytes) == "null" {
		traceBytes = []byte("[]")
	} else if traceBytes[0] != '[' || !json.Valid(traceBytes) {
		return errors.New("negative sample guardrailTrace must be a valid JSON array")
	}
	snapshotBytes := encodeOptimizationJSON(sample.FeatureSnapshot)
	if len(snapshotBytes) == 0 || string(snapshotBytes) == "null" {
		snapshotBytes = []byte("{}")
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return err
	}

	if _, err = tx.Exec(ctx, `
INSERT INTO core_registry.optimization_negative_samples (
  id, tenant_id, site_id, recommendation_id, monitor_id, decision_id, trace_id,
  sample_kind, reason_code, solver_serving_ref,
  raw_recommended_value, applied_or_clamped_value,
  feature_snapshot, guardrail_trace, occurred_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7,
  $8, $9, $10,
  $11, $12,
  $13::jsonb, $14::jsonb, $15, $15
)`,
		id, tenant, site, recID, monID, decisionIDPtr, traceID,
		sampleKind, reasonCode, servingRef,
		finiteFloatPtr(sample.RawRecommendedValue),
		finiteFloatPtr(sample.AppliedOrClampedValue),
		snapshotBytes, traceBytes, occurredAt,
	); err != nil {
		return fmt.Errorf("insert optimization negative sample: %w", err)
	}
	return tx.Commit(ctx)
}

// ListControlTicks reads control ticks for a site within [from, to].
func (store *PostgresStore) ListControlTicks(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]ControlTickRecord, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return nil, errors.New("control tick query scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text,
       CASE WHEN optimization_run_id IS NULL THEN NULL ELSE optimization_run_id::text END,
       CASE WHEN recommendation_id IS NULL THEN NULL ELSE recommendation_id::text END,
       COALESCE(decision_id::text, ''),
       control_mode, COALESCE(loop_key, 'PLANT'), COALESCE(solver_serving_ref, ''),
       CASE WHEN deployment_revision_id IS NULL THEN NULL ELSE deployment_revision_id::text END,
       outdoor_wet_bulb_c, cooling_load_kw, chiller_power_kw, observed_cop,
       supply_temp_c, recommended_supply_temp_c, applied_supply_temp_c,
       recorded_at
FROM core_registry.optimization_control_ticks
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND recorded_at >= $3
  AND recorded_at <= $4
ORDER BY recorded_at DESC, id DESC
`, tenant, site, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("query optimization control ticks: %w", err)
	}
	defer rows.Close()

	ticks := []ControlTickRecord{}
	for rows.Next() {
		var item ControlTickRecord
		var wetBulb, coolingLoad, chillerPower, observedCOP, supplyTempC *float64
		if scanErr := rows.Scan(
			&item.ID, &item.TenantID, &item.SiteID,
			&item.OptimizationRunID, &item.RecommendationID, &item.DecisionID,
			&item.ControlMode, &item.LoopKey, &item.SolverServingRef, &item.DeploymentRevisionID,
			&wetBulb, &coolingLoad, &chillerPower, &observedCOP,
			&supplyTempC, &item.RecommendedSupplyTempC, &item.AppliedSupplyTempC,
			&item.TickAt,
		); scanErr != nil {
			return nil, scanErr
		}
		item.FeatureSnapshot.OutdoorWetBulbC = wetBulb
		item.FeatureSnapshot.CoolingLoadKW = coolingLoad
		item.FeatureSnapshot.ChillerPowerKW = chillerPower
		item.FeatureSnapshot.ObservedCOP = observedCOP
		item.FeatureSnapshot.SupplyTempC = supplyTempC
		item.BaselineSupplyTempC = supplyTempC
		item.TickAt = item.TickAt.UTC()
		ticks = append(ticks, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ticks, nil
}

// ListNegativeSamples reads flywheel negative samples for a site within [from, to].
func (store *PostgresStore) ListNegativeSamples(ctx context.Context, tenantID, siteID string, from, to time.Time, limit int) ([]NegativeSampleRecord, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return nil, errors.New("negative sample query scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenant, site); err != nil {
		return nil, err
	}

	query := `
SELECT id::text, tenant_id::text, site_id::text,
       CASE WHEN recommendation_id IS NULL THEN NULL ELSE recommendation_id::text END,
       CASE WHEN monitor_id IS NULL THEN NULL ELSE monitor_id::text END,
       COALESCE(decision_id::text, ''), COALESCE(trace_id, ''),
       sample_kind, COALESCE(reason_code, ''), COALESCE(solver_serving_ref, ''),
       raw_recommended_value, applied_or_clamped_value,
       feature_snapshot, guardrail_trace, occurred_at
FROM core_registry.optimization_negative_samples
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND occurred_at >= $3
  AND occurred_at <= $4
ORDER BY occurred_at DESC, id DESC`
	var rows pgx.Rows
	if limit > 0 {
		query += "\nLIMIT $5"
		rows, err = tx.Query(ctx, query, tenant, site, from.UTC(), to.UTC(), limit)
	} else {
		rows, err = tx.Query(ctx, query, tenant, site, from.UTC(), to.UTC())
	}
	if err != nil {
		return nil, fmt.Errorf("query optimization negative samples: %w", err)
	}
	defer rows.Close()

	samples := []NegativeSampleRecord{}
	for rows.Next() {
		var item NegativeSampleRecord
		var snapshotBytes []byte
		if scanErr := rows.Scan(
			&item.ID, &item.TenantID, &item.SiteID,
			&item.RecommendationID, &item.MonitorID,
			&item.DecisionID, &item.TraceID,
			&item.SampleKind, &item.ReasonCode, &item.SolverServingRef,
			&item.RawRecommendedValue, &item.AppliedOrClampedValue,
			&snapshotBytes, &item.GuardrailTrace, &item.OccurredAt,
		); scanErr != nil {
			return nil, scanErr
		}
		if len(snapshotBytes) > 0 && string(snapshotBytes) != "null" {
			if unmarshalErr := json.Unmarshal(snapshotBytes, &item.FeatureSnapshot); unmarshalErr != nil {
				return nil, fmt.Errorf("decode negative sample feature snapshot: %w", unmarshalErr)
			}
		}
		item.OccurredAt = item.OccurredAt.UTC()
		samples = append(samples, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return samples, nil
}

// GovernanceSummary aggregates active solver routing, control-mode tick distribution, bivariate
// wet-bulb × load COP comparison, and negative-sample flywheel counts for the trailing window.
func (service *Service) GovernanceSummary(ctx context.Context, tenantID, siteID string, windowDays int) (GovernanceSummary, error) {
	if service == nil {
		return GovernanceSummary{}, errors.New("optimization service is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return GovernanceSummary{}, errors.New("governance summary scope is invalid")
	}
	if windowDays <= 0 {
		windowDays = DefaultGovernanceWindowDays
	} else if windowDays > MaxGovernanceWindowDays {
		windowDays = MaxGovernanceWindowDays
	}

	if checker := service.effectiveSiteVisibilityChecker(); checker != nil {
		visible, err := checker.CheckSiteVisible(ctx, tenant, site)
		if err != nil {
			return GovernanceSummary{}, err
		}
		if !visible {
			return GovernanceSummary{}, ErrOptimizationNotFound
		}
	}

	now := service.clock().UTC()
	from := now.AddDate(0, 0, -windowDays)

	activeSolver := ActiveSolverSelection{
		ServingRef: DefaultBuiltinSolverRef,
		Source:     SolverSourceDefault,
		LoopKey:    DefaultSolverLoopKey,
	}
	if resolver := service.effectiveSolverResolver(); resolver != nil {
		resolved, err := resolver.ResolveActiveSolver(ctx, tenant, site, DefaultSolverLoopKey, now)
		if err != nil {
			return GovernanceSummary{}, err
		}
		if clean := sanitizeServingRef(resolved.ServingRef); clean != "" {
			resolved.ServingRef = clean
		} else {
			resolved.ServingRef = DefaultBuiltinSolverRef
		}
		resolved.LoopKey = normalizeSolverLoopKey(resolved.LoopKey)
		activeSolver = resolved
	}

	var ticks []ControlTickRecord
	var samples []NegativeSampleRecord
	if flywheel := service.effectiveFlywheelStore(); flywheel != nil {
		var err error
		ticks, err = flywheel.ListControlTicks(ctx, tenant, site, from, now)
		if err != nil {
			return GovernanceSummary{}, err
		}
		samples, err = flywheel.ListNegativeSamples(ctx, tenant, site, from, now, 0)
		if err != nil {
			return GovernanceSummary{}, err
		}
	}

	return buildGovernanceSummary(tenant, site, windowDays, now, activeSolver, ticks, samples), nil
}

func buildGovernanceSummary(
	tenantID, siteID string,
	windowDays int,
	generatedAt time.Time,
	activeSolver ActiveSolverSelection,
	ticks []ControlTickRecord,
	samples []NegativeSampleRecord,
) GovernanceSummary {
	var tickCounts GovernanceTickCounts
	for _, tick := range ticks {
		switch normalizeControlTickMode(tick.ControlMode) {
		case ControlModeAIClosedLoop:
			tickCounts.AIClosedLoop++
		case ControlModeAIShadow:
			tickCounts.AIShadow++
		case ControlModeRuleBaseline:
			tickCounts.RuleBaseline++
		case ControlModeManualOverride:
			tickCounts.ManualOverride++
		}
	}

	copComparison := evaluateBivariateCOPComparison(ticks)
	flywheel := summarizeNegativeSamples(samples)

	return GovernanceSummary{
		TenantID:      tenantID,
		SiteID:        siteID,
		WindowDays:    windowDays,
		GeneratedAt:   generatedAt.UTC(),
		ActiveSolver:  activeSolver,
		TickCounts:    tickCounts,
		COPComparison: copComparison,
		Flywheel:      flywheel,
	}
}

func evaluateBivariateCOPComparison(ticks []ControlTickRecord) analyticsmodel.BivariateCOPSummary {
	samples := make([]analyticsmodel.ControlTickSample, 0, len(ticks))
	for _, tick := range ticks {
		mode := normalizeControlTickMode(tick.ControlMode)
		if mode == "" {
			continue
		}
		cop, hasCOP := tickObservedCOP(tick.FeatureSnapshot)
		var copPtr *float64
		if hasCOP {
			c := cop
			copPtr = &c
		}
		samples = append(samples, analyticsmodel.ControlTickSample{
			Mode:            analyticsmodel.ControlTickMode(mode),
			OutdoorWetBulbC: tick.FeatureSnapshot.OutdoorWetBulbC,
			CoolingLoadKW:   tick.FeatureSnapshot.CoolingLoadKW,
			ChillerPowerKW:  tick.FeatureSnapshot.ChillerPowerKW,
			ObservedCOP:     copPtr,
		})
	}
	return analyticsmodel.EvaluateBivariateCOP(samples, analyticsmodel.BivariateBinConfig{})
}

func tickObservedCOP(snap OptimizationFeatureSnapshot) (float64, bool) {
	if snap.ObservedCOP != nil && finitePositive(*snap.ObservedCOP) {
		return *snap.ObservedCOP, true
	}
	if snap.CoolingLoadKW != nil && snap.ChillerPowerKW != nil &&
		finitePositive(*snap.CoolingLoadKW) && finitePositive(*snap.ChillerPowerKW) {
		cop := *snap.CoolingLoadKW / *snap.ChillerPowerKW
		if finitePositive(cop) {
			return cop, true
		}
	}
	return 0, false
}

func summarizeNegativeSamples(samples []NegativeSampleRecord) GovernanceFlywheelSummary {
	var summary GovernanceFlywheelSummary
	for _, sample := range samples {
		switch resolveNegativeSampleKind(sample) {
		case NegativeSampleGuardrailClamped:
			summary.TotalSamples++
			summary.GuardrailClamped++
		case NegativeSampleGuardrailVetoed:
			summary.TotalSamples++
			summary.GuardrailVetoed++
		case NegativeSampleManualOverride:
			summary.TotalSamples++
			summary.ManualOverride++
		case NegativeSampleTrackingDeviation:
			summary.TotalSamples++
			summary.TrackingDeviation++
		case NegativeSampleMonitorTripped:
			summary.TotalSamples++
			summary.MonitorTripped++
		}
	}
	return summary
}

func normalizeControlTickMode(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case ControlModeAIClosedLoop:
		return ControlModeAIClosedLoop
	case ControlModeAIShadow:
		return ControlModeAIShadow
	case ControlModeManualOverride:
		return ControlModeManualOverride
	case ControlModeRuleBaseline:
		return ControlModeRuleBaseline
	default:
		return ""
	}
}

func resolveNegativeSampleKind(sample NegativeSampleRecord) string {
	if kind := normalizeNegativeSampleKind(sample.SampleKind); kind != "" {
		return kind
	}
	return normalizeNegativeSampleKind(sample.ReasonCode)
}

func normalizeNegativeSampleKind(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case NegativeSampleGuardrailClamped:
		return NegativeSampleGuardrailClamped
	case NegativeSampleGuardrailVetoed:
		return NegativeSampleGuardrailVetoed
	case NegativeSampleManualOverride:
		return NegativeSampleManualOverride
	case NegativeSampleTrackingDeviation:
		return NegativeSampleTrackingDeviation
	case NegativeSampleMonitorTripped:
		return NegativeSampleMonitorTripped
	default:
		return ""
	}
}

func optionalUUIDPtr(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	return optionalUUIDStringPtr(*raw)
}

func optionalUUIDStringPtr(raw string) (*string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidPattern.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid UUID %q", trimmed)
	}
	return &trimmed, nil
}

func finiteFloatPtr(v *float64) *float64 {
	if v == nil || !finite(*v) {
		return nil
	}
	copyVal := *v
	return &copyVal
}

type dispatchFlywheelState struct {
	recommendation   Recommendation
	lease            *AutoControlLeaseRecord
	siteCtx          *AutoControlSiteContext
	liveState        *autoControlLiveState
	liveObs          *AutoControlLiveObservation
	guardrail        *commandmodel.GuardrailResult
	guardrailTrace   json.RawMessage
	loopKey          string
	monitorID        string
	baselineSupplyC  *float64
	recommendedValue *float64
	appliedSupplyC   *float64
	decisionID       string
	traceID          string
	now              time.Time
}

func flywheelWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), 3*time.Second)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
}

func (controller *AutoController) resolveActiveSolver(ctx context.Context, tenantID, siteID string, at time.Time) ActiveSolverSelection {
	fallback := ActiveSolverSelection{
		ServingRef: DefaultBuiltinSolverRef,
		Source:     SolverSourceDefault,
		LoopKey:    DefaultSolverLoopKey,
	}
	if controller == nil {
		return fallback
	}
	resolver := controller.effectiveSolverResolver()
	if resolver == nil {
		return fallback
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	selection, err := resolver.ResolveActiveSolver(resolveCtx, tenantID, siteID, DefaultSolverLoopKey, at)
	if err != nil {
		if ctx.Err() == nil {
			controller.logf("resolve active solver serving ref failed (site=%s): %v", siteID, err)
		}
		return fallback
	}
	selection.LoopKey = normalizeSolverLoopKey(selection.LoopKey)
	if clean := sanitizeServingRef(selection.ServingRef); clean != "" {
		selection.ServingRef = clean
	} else {
		selection.ServingRef = DefaultBuiltinSolverRef
	}
	return selection
}

func (state *dispatchFlywheelState) enrichTelemetryBestEffort(ctx context.Context, controller *AutoController) {
	if state == nil || controller == nil || ctx.Err() != nil {
		return
	}
	tenantID := strings.TrimSpace(state.recommendation.TenantID)
	siteID := strings.TrimSpace(state.recommendation.SiteID)
	if tenantID == "" || siteID == "" {
		return
	}
	enrichCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if state.siteCtx == nil && controller.store != nil {
		if siteCtx, err := controller.store.LoadAutoControlSiteContext(enrichCtx, tenantID, siteID, state.recommendation.InputSnapshotID, state.now); err == nil {
			siteCopy := siteCtx
			state.siteCtx = &siteCopy
		}
	}

	if state.liveState == nil && state.siteCtx != nil && controller.pointHealth != nil &&
		strings.TrimSpace(state.siteCtx.InputMapping.SupplyTemperatureKey) != "" &&
		len(state.siteCtx.InputMapping.effectiveZoneKeys()) > 0 {
		targetDevID := ""
		if state.siteCtx.SetpointTarget != nil {
			targetDevID = state.siteCtx.SetpointTarget.DeviceID
		}
		deviceIDs, allKeys := autoControlHealthScope(*state.siteCtx, targetDevID)
		if healthSnapshot, err := controller.pointHealth.InspectTelemetryPointHealth(enrichCtx, tenantID, siteID, deviceIDs, allKeys); err == nil && len(healthSnapshot.LivePoints) > 0 {
			live := extractAutoControlLiveState(healthSnapshot.LivePoints, *state.siteCtx, targetDevID, state.now)
			state.liveState = &live
		}
	}

	if state.liveObs == nil && controller.telemetry != nil {
		var mapping InputMapping
		var topology StagingTopology
		if state.siteCtx != nil {
			mapping = state.siteCtx.InputMapping
			topology = state.siteCtx.Topology
		}
		if obs, err := controller.telemetry.ReadLiveAutoControlObservation(enrichCtx, tenantID, siteID, mapping, topology, state.now); err == nil {
			obsCopy := obs
			state.liveObs = &obsCopy
		}
	}
}

func classifyDispatchControlMode(state *dispatchFlywheelState, skipReason string, dispatched, shadowRecorded bool) string {
	if dispatched {
		return ControlModeAIClosedLoop
	}
	if shadowRecorded {
		return ControlModeAIShadow
	}
	if isManualOverrideDispatchState(state, skipReason) {
		return ControlModeManualOverride
	}
	return ControlModeRuleBaseline
}

func isManualOverrideDispatchState(state *dispatchFlywheelState, skipReason string) bool {
	if strings.EqualFold(strings.TrimSpace(skipReason), "MANUAL_BACKOFF_ACTIVE") {
		return true
	}
	if state == nil {
		return false
	}
	if state.lease != nil && state.lease.ManualBackoffUntil != nil && !state.lease.ManualBackoffUntil.IsZero() && !state.lease.ManualBackoffUntil.Before(state.now) {
		return true
	}
	if state.siteCtx != nil {
		mode := strings.ToUpper(strings.TrimSpace(state.siteCtx.SiteControlMode))
		if mode == SiteControlModeManualOverride || mode == "MANUAL" {
			return true
		}
	}
	if state.liveState != nil && state.liveState.CabinetLocalManual {
		return true
	}
	if state.guardrail != nil && strings.EqualFold(strings.TrimSpace(state.guardrail.ReasonCode), "manual_override_backoff") {
		return true
	}
	return false
}

func buildOptimizationFeatureSnapshot(
	liveState *autoControlLiveState,
	liveObs *AutoControlLiveObservation,
	lease *AutoControlLeaseRecord,
	siteCtx *AutoControlSiteContext,
	monitor *OptimizationExecutionMonitorRecord,
) OptimizationFeatureSnapshot {
	var snap OptimizationFeatureSnapshot

	if liveState != nil && liveState.SupplyTemp != nil && liveState.SupplyTemp.NumericValue != nil && finite(*liveState.SupplyTemp.NumericValue) {
		v := *liveState.SupplyTemp.NumericValue
		snap.SupplyTempC = &v
	} else if liveObs != nil && liveObs.SupplyTemp != nil && finite(liveObs.SupplyTemp.Value) {
		v := liveObs.SupplyTemp.Value
		snap.SupplyTempC = &v
	}

	snap.ZoneMaxTempC = maxZoneTemperature(liveState, liveObs)

	if liveState != nil && liveState.OutdoorWetBulbC != nil && finite(*liveState.OutdoorWetBulbC) {
		v := *liveState.OutdoorWetBulbC
		snap.OutdoorWetBulbC = &v
	} else if liveObs != nil && liveObs.OutdoorWetBulbC != nil && finite(*liveObs.OutdoorWetBulbC) {
		v := *liveObs.OutdoorWetBulbC
		snap.OutdoorWetBulbC = &v
	}

	if liveState != nil && liveState.CoolingLoadKW != nil && finiteNonNegative(*liveState.CoolingLoadKW) {
		v := *liveState.CoolingLoadKW
		snap.CoolingLoadKW = &v
	} else if liveObs != nil && liveObs.CoolingLoadKW != nil && finiteNonNegative(*liveObs.CoolingLoadKW) {
		v := *liveObs.CoolingLoadKW
		snap.CoolingLoadKW = &v
	}

	if liveState != nil && liveState.ChillerPowerKW != nil && finiteNonNegative(*liveState.ChillerPowerKW) {
		v := *liveState.ChillerPowerKW
		snap.ChillerPowerKW = &v
	} else if liveObs != nil && liveObs.ChillerPowerKW != nil && finiteNonNegative(*liveObs.ChillerPowerKW) {
		v := *liveObs.ChillerPowerKW
		snap.ChillerPowerKW = &v
	}

	snap.ObservedCOP = computeObservedCOPPtr(snap.CoolingLoadKW, snap.ChillerPowerKW)

	if liveState != nil {
		snap.RunningChillers = liveState.RunningChillers
		snap.CabinetLocalManual = liveState.CabinetLocalManual
		snap.DeviceFaultActive = liveState.DeviceFaultActive
	}
	if snap.RunningChillers == 0 && liveObs != nil && liveObs.RunningChillers > 0 {
		snap.RunningChillers = liveObs.RunningChillers
	}

	if lease != nil {
		snap.LeaseDeadbandC = lease.DeadbandC
		snap.SupplyTempMinC = math.Max(DefaultSupplyTempHardFloorC, lease.SupplyTempMinC)
		snap.SupplyTempMaxC = lease.SupplyTempMaxC
	} else if monitor != nil {
		snap.SupplyTempMinC = math.Max(DefaultSupplyTempHardFloorC, monitor.SupplyTempMinC)
		snap.SupplyTempMaxC = monitor.SupplyTempMaxC
		if siteCtx != nil {
			snap.LeaseDeadbandC = siteCtx.LeaseDeadbandC
		}
	} else if siteCtx != nil {
		snap.LeaseDeadbandC = siteCtx.LeaseDeadbandC
	}

	return snap
}

func maxZoneTemperature(liveState *autoControlLiveState, liveObs *AutoControlLiveObservation) *float64 {
	var maxVal float64
	found := false
	if liveState != nil {
		for _, pt := range liveState.ZoneTemps {
			if pt.NumericValue == nil || !finite(*pt.NumericValue) {
				continue
			}
			if !found || *pt.NumericValue > maxVal {
				maxVal = *pt.NumericValue
				found = true
			}
		}
	}
	if !found && liveObs != nil {
		for _, pt := range liveObs.ZoneTemps {
			if !finite(pt.Value) {
				continue
			}
			if !found || pt.Value > maxVal {
				maxVal = pt.Value
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return &maxVal
}

func computeObservedCOPPtr(loadKW, powerKW *float64) *float64 {
	if loadKW == nil || powerKW == nil || !finitePositive(*loadKW) || !finitePositive(*powerKW) {
		return nil
	}
	cop := round(*loadKW / *powerKW, 4)
	if !finitePositive(cop) {
		return nil
	}
	return &cop
}

func (controller *AutoController) recordDispatchFlywheel(
	ctx context.Context,
	state *dispatchFlywheelState,
	_ string,
	skipReason string,
	dispatched, shadowRecorded bool,
) {
	if controller == nil || state == nil {
		return
	}
	flywheel := controller.effectiveFlywheelStore()
	if flywheel == nil {
		return
	}
	if strings.TrimSpace(state.recommendation.TenantID) == "" || strings.TrimSpace(state.recommendation.SiteID) == "" {
		return
	}

	state.enrichTelemetryBestEffort(ctx, controller)
	controlMode := classifyDispatchControlMode(state, skipReason, dispatched, shadowRecorded)
	solverSel := controller.resolveActiveSolver(ctx, state.recommendation.TenantID, state.recommendation.SiteID, state.now)
	snapshot := buildOptimizationFeatureSnapshot(state.liveState, state.liveObs, state.lease, state.siteCtx, nil)

	baselineSupplyC := finiteFloatPtr(state.baselineSupplyC)
	if baselineSupplyC == nil && finite(state.recommendation.Baseline.SupplyTempC) && state.recommendation.Baseline.SupplyTempC != 0 {
		v := state.recommendation.Baseline.SupplyTempC
		baselineSupplyC = &v
	}
	if baselineSupplyC != nil && snapshot.SupplyTempC == nil {
		snapshot.SupplyTempC = baselineSupplyC
	}

	var recommendedSupplyC *float64
	if state.recommendedValue != nil && finite(*state.recommendedValue) {
		v := *state.recommendedValue
		recommendedSupplyC = &v
	} else if finite(state.recommendation.Candidate.SupplyTempC) && state.recommendation.Candidate.SupplyTempC != 0 {
		v := state.recommendation.Candidate.SupplyTempC
		recommendedSupplyC = &v
	}

	var appliedSupplyC *float64
	if dispatched || shadowRecorded {
		appliedSupplyC = finiteFloatPtr(state.appliedSupplyC)
	}

	depRevID := solverSel.DeploymentRevisionID
	if depRevID == nil {
		if parsed, err := optionalUUIDStringPtr(state.recommendation.DeploymentRevision); err == nil {
			depRevID = parsed
		}
	}

	recID := cleanStringPtr(state.recommendation.ID)
	monID := cleanStringPtr(state.monitorID)
	loopKey := strings.TrimSpace(state.loopKey)
	if loopKey == "" {
		loopKey = solverSel.LoopKey
	}

	writeCtx, cancel := flywheelWriteContext(ctx)
	defer cancel()

	if err := flywheel.RecordControlTick(writeCtx, ControlTickRecord{
		TenantID:               state.recommendation.TenantID,
		SiteID:                 state.recommendation.SiteID,
		RecommendationID:       recID,
		DecisionID:             state.decisionID,
		ControlMode:            controlMode,
		LoopKey:                loopKey,
		SolverServingRef:       solverSel.ServingRef,
		DeploymentRevisionID:   depRevID,
		BaselineSupplyTempC:    baselineSupplyC,
		RecommendedSupplyTempC: recommendedSupplyC,
		AppliedSupplyTempC:     appliedSupplyC,
		FeatureSnapshot:        snapshot,
		TickAt:                 state.now,
	}); err != nil {
		controller.logf("record optimization control tick failed (site=%s mode=%s): %v", state.recommendation.SiteID, controlMode, err)
	}

	if state.guardrail == nil {
		return
	}
	var sampleKind string
	var appliedOrClampedVal *float64
	switch {
	case state.guardrail.Verdict == commandmodel.GuardrailVerdictClamped:
		sampleKind = NegativeSampleGuardrailClamped
		if finite(state.guardrail.AdjustedValue) && state.guardrail.AdjustedValue != 0 {
			v := state.guardrail.AdjustedValue
			appliedOrClampedVal = &v
		}
	case state.guardrail.Verdict == commandmodel.GuardrailVerdictVetoed ||
		(!state.guardrail.Allowed && state.guardrail.Verdict != commandmodel.GuardrailVerdictDeadbandSkip):
		sampleKind = NegativeSampleGuardrailVetoed
	default:
		return
	}

	rawRecommendedVal := recommendedSupplyC
	if finite(state.guardrail.RawValue) && state.guardrail.RawValue != 0 {
		v := state.guardrail.RawValue
		rawRecommendedVal = &v
	}

	traceBytes := bytes.TrimSpace(state.guardrailTrace)
	if len(traceBytes) == 0 {
		traceBytes = encodeOptimizationJSON(state.guardrail.Trace)
	}
	if len(traceBytes) == 0 || string(traceBytes) == "null" {
		traceBytes = []byte("[]")
	}

	reasonCode := firstNonEmptyString(state.guardrail.ReasonCode, skipReason, sampleKind)
	if err := flywheel.RecordNegativeSample(writeCtx, NegativeSampleRecord{
		TenantID:              state.recommendation.TenantID,
		SiteID:                state.recommendation.SiteID,
		RecommendationID:      recID,
		MonitorID:             monID,
		DecisionID:            state.decisionID,
		TraceID:               state.traceID,
		SampleKind:            sampleKind,
		ReasonCode:            reasonCode,
		SolverServingRef:      solverSel.ServingRef,
		RawRecommendedValue:   rawRecommendedVal,
		AppliedOrClampedValue: appliedOrClampedVal,
		GuardrailTrace:        traceBytes,
		FeatureSnapshot:       snapshot,
		OccurredAt:            state.now,
	}); err != nil {
		controller.logf("record guardrail negative sample failed (site=%s kind=%s): %v", state.recommendation.SiteID, sampleKind, err)
	}
}

func (controller *AutoController) recordMonitorNegativeSample(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	siteCtx AutoControlSiteContext,
	liveState *autoControlLiveState,
	liveObs *AutoControlLiveObservation,
	telemetryHealthy bool,
	sampleKind string,
	reasonCode string,
	now time.Time,
) {
	if controller == nil {
		return
	}
	flywheel := controller.effectiveFlywheelStore()
	if flywheel == nil {
		return
	}
	if liveObs == nil && telemetryHealthy && controller.telemetry != nil && ctx.Err() == nil {
		enrichCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if obs, err := controller.telemetry.ReadLiveAutoControlObservation(enrichCtx, monitor.TenantID, monitor.SiteID, siteCtx.InputMapping, siteCtx.Topology, now); err == nil {
			obsCopy := obs
			liveObs = &obsCopy
		}
		cancel()
	}

	solverSel := controller.resolveActiveSolver(ctx, monitor.TenantID, monitor.SiteID, now)
	snapshot := buildOptimizationFeatureSnapshot(liveState, liveObs, nil, &siteCtx, &monitor)

	traceBytes := bytes.TrimSpace(monitor.GuardrailTrace)
	if len(traceBytes) == 0 || string(traceBytes) == "null" {
		traceBytes = []byte("[]")
	}

	normalizedKind := normalizeNegativeSampleKind(sampleKind)
	if normalizedKind == "" {
		normalizedKind = NegativeSampleMonitorTripped
	}
	effectiveReason := firstNonEmptyString(reasonCode, normalizedKind)

	writeCtx, cancel := flywheelWriteContext(ctx)
	defer cancel()

	if err := flywheel.RecordNegativeSample(writeCtx, NegativeSampleRecord{
		TenantID:              monitor.TenantID,
		SiteID:                monitor.SiteID,
		RecommendationID:      cleanStringPtr(monitor.RecommendationID),
		MonitorID:             cleanStringPtr(monitor.ID),
		DecisionID:            monitor.DecisionID,
		TraceID:               monitor.TraceID,
		SampleKind:            normalizedKind,
		ReasonCode:            effectiveReason,
		SolverServingRef:      solverSel.ServingRef,
		RawRecommendedValue:   finiteFloatPtr(monitor.RawRecommendedValue),
		AppliedOrClampedValue: finiteFloatPtr(monitor.AppliedSupplyTempC),
		GuardrailTrace:        traceBytes,
		FeatureSnapshot:       snapshot,
		OccurredAt:            now.UTC(),
	}); err != nil {
		controller.logf("record monitor negative sample failed (monitor=%s kind=%s): %v", monitor.ID, normalizedKind, err)
	}
}
