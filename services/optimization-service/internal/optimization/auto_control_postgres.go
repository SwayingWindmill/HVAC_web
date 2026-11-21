package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

func (store *PostgresStore) ActiveAutoControlLease(ctx context.Context, tenantID, siteID string, at time.Time) (*AutoControlLeaseRecord, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return nil, errors.New("auto control lease scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return nil, err
	}
	var lease AutoControlLeaseRecord
	err = tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, status, armed_by::text, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c, supply_temp_max_c,
       zone_temp_max_c, min_running_chillers, max_commands_per_hour,
       deadband_c, manual_backoff_until, manual_backoff_reason, revision,
       COALESCE(execution_mode, 'CLOSED_LOOP'),
       pump_freq_min_hz, pump_freq_max_hz, max_freq_step_hz, freq_deadband_hz,
       fan_speed_min_pct, fan_speed_max_pct, max_fan_step_pct, fan_deadband_pct
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE' AND expires_at > $3
ORDER BY armed_at DESC, id DESC
LIMIT 1
`, tenantID, siteID, at.UTC()).Scan(
		&lease.ID, &lease.TenantID, &lease.SiteID, &lease.Status, &lease.ArmedBy, &lease.ArmedAt, &lease.ExpiresAt,
		&lease.AllowedCapabilities, &lease.MaxSetpointStepC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC,
		&lease.ZoneTempMaxC, &lease.MinRunningChillers, &lease.MaxCommandsPerHour,
		&lease.DeadbandC, &lease.ManualBackoffUntil, &lease.ManualBackoffReason, &lease.Revision,
		&lease.ExecutionMode,
		&lease.PumpFreqMinHz, &lease.PumpFreqMaxHz, &lease.MaxFreqStepHz, &lease.FreqDeadbandHz,
		&lease.FanSpeedMinPct, &lease.FanSpeedMaxPct, &lease.MaxFanStepPct, &lease.FanDeadbandPct,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load active site auto control lease: %w", err)
	}
	lease.ExecutionMode = normalizeExecutionMode(lease.ExecutionMode)
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (store *PostgresStore) LoadAutoControlSiteContext(ctx context.Context, tenantID, siteID, inputSnapshotID string, at time.Time) (AutoControlSiteContext, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return AutoControlSiteContext{}, errors.New("auto control site context scope is invalid")
	}
	topology, err := store.StagingTopology(ctx, tenantID, siteID, at)
	if err != nil {
		return AutoControlSiteContext{}, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return AutoControlSiteContext{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return AutoControlSiteContext{}, err
	}

	var deviceIDs []string
	if topology.Twin != nil {
		deviceIDs = twinDeviceUUIDs(*topology.Twin)
	}
	result := AutoControlSiteContext{
		SiteControlMode: topology.SiteControlMode,
		Topology:        topology,
		DeviceIDs:       deviceIDs,
	}

	var policyConstraints []byte
	if uuidPattern.MatchString(inputSnapshotID) {
		err = tx.QueryRow(ctx, `
SELECT pv.constraints
FROM core_registry.optimization_input_snapshots s
JOIN core_registry.optimization_policy_versions pv
  ON pv.tenant_id = s.tenant_id AND pv.id = s.policy_version_id
WHERE s.tenant_id = $1::uuid AND s.site_id = $2::uuid AND s.id = $3::uuid
`, tenantID, siteID, inputSnapshotID).Scan(&policyConstraints)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return AutoControlSiteContext{}, err
		}
	}
	if len(policyConstraints) == 0 {
		err = tx.QueryRow(ctx, `
SELECT pv.constraints
FROM core_registry.optimization_policy_versions pv
JOIN core_registry.optimization_policies p
  ON p.tenant_id = pv.tenant_id AND p.id = pv.policy_id
WHERE pv.tenant_id = $1::uuid
  AND p.status = 'ACTIVE' AND p.resource_type = 'HVAC'
  AND pv.status = 'RELEASED'
  AND pv.effective_from <= $2
  AND (pv.effective_to IS NULL OR pv.effective_to > $2)
ORDER BY pv.effective_from DESC, pv.version DESC
LIMIT 1
`, tenantID, at.UTC()).Scan(&policyConstraints)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return AutoControlSiteContext{}, err
		}
	}
	if len(policyConstraints) > 0 {
		var parsed struct {
			InputMapping InputMapping `json:"inputMapping"`
		}
		if json.Unmarshal(policyConstraints, &parsed) == nil {
			result.InputMapping = resolveInputMappingWithTwin(parsed.InputMapping, topology.Twin)
		}
	}

	var leaseExecutionMode string
	var leaseDeadbandC float64
	var leaseFreqDeadbandHz, leaseFanDeadbandPct *float64
	err = tx.QueryRow(ctx, `
SELECT COALESCE(execution_mode, 'CLOSED_LOOP'), COALESCE(deadband_c, 0),
       freq_deadband_hz, fan_deadband_pct
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE' AND expires_at > $3
ORDER BY armed_at DESC, id DESC
LIMIT 1
`, tenantID, siteID, at.UTC()).Scan(&leaseExecutionMode, &leaseDeadbandC, &leaseFreqDeadbandHz, &leaseFanDeadbandPct)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AutoControlSiteContext{}, fmt.Errorf("load active lease execution mode: %w", err)
	}
	result.ExecutionMode = normalizeExecutionMode(leaseExecutionMode)
	result.LeaseDeadbandC = leaseDeadbandC
	result.LeaseFreqDeadbandHz = leaseFreqDeadbandHz
	result.LeaseFanDeadbandPct = leaseFanDeadbandPct

	if err = tx.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND started_at >= $3
  AND status IN ('PENDING_SUBMIT', 'OBSERVING', 'VERIFIED', 'ROLLED_BACK', 'TRIPPED', 'TRIPPED_ROLLED_BACK', 'ROLLBACK_FAILED', 'MANUAL_OVERRIDE')
`, tenantID, siteID, at.UTC().Add(-time.Hour)).Scan(&result.RecentAutoCountHour); err != nil {
		return AutoControlSiteContext{}, fmt.Errorf("count recent optimization monitors: %w", err)
	}

	if err = tx.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND started_at >= $3
  AND status = 'SHADOW_RECORDED'
`, tenantID, siteID, at.UTC().Add(-time.Hour)).Scan(&result.RecentShadowCountHour); err != nil {
		return AutoControlSiteContext{}, fmt.Errorf("count recent shadow optimization monitors: %w", err)
	}

	stalePendingCutoff := at.UTC().Add(-DefaultPendingSubmitStaleAge)
	activeLoopRows, err := tx.Query(ctx, `
SELECT COALESCE(loop_key, 'PLANT')
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND (status = 'OBSERVING' OR (status = 'PENDING_SUBMIT' AND started_at >= $3))
GROUP BY COALESCE(loop_key, 'PLANT')
`, tenantID, siteID, stalePendingCutoff)
	if err != nil {
		return AutoControlSiteContext{}, fmt.Errorf("check active optimization monitors: %w", err)
	}
	activeLoops := make(map[string]bool)
	for activeLoopRows.Next() {
		var loopKey string
		if scanErr := activeLoopRows.Scan(&loopKey); scanErr != nil {
			activeLoopRows.Close()
			return AutoControlSiteContext{}, scanErr
		}
		cleanLoop := strings.ToUpper(strings.TrimSpace(loopKey))
		if cleanLoop == "" {
			cleanLoop = LoopKeyPlant
		}
		activeLoops[cleanLoop] = true
	}
	if err = activeLoopRows.Err(); err != nil {
		activeLoopRows.Close()
		return AutoControlSiteContext{}, err
	}
	activeLoopRows.Close()
	result.ActiveLoopMonitors = activeLoops
	result.HasActiveMonitor = activeLoops[LoopKeyPlant]

	rows, err := tx.Query(ctx, `
SELECT tp.id::text, tp.reporting_device_id::text, tp.point_code,
       COALESCE(tp.source_metadata->>'feedbackPointKey', ''),
       COALESCE(psb.asset_id::text, ''),
       EXISTS (
         SELECT 1
         FROM core_registry.device_bindings db
         JOIN core_registry.asset_control_locks acl
           ON acl.tenant_id = db.tenant_id AND acl.site_id = db.site_id AND acl.asset_id = db.asset_id
         WHERE db.tenant_id = $1::uuid AND db.site_id = $2::uuid AND db.device_id = tp.reporting_device_id
           AND db.status = 'ACTIVE' AND db.valid_to IS NULL AND acl.lock_mode <> 'UNLOCKED'
       )
FROM core_registry.telemetry_points tp
JOIN core_registry.devices d
  ON d.tenant_id = tp.tenant_id AND d.site_id = tp.site_id AND d.id = tp.reporting_device_id AND d.status = 'ACTIVE'
LEFT JOIN core_registry.point_subject_bindings psb
  ON psb.tenant_id = tp.tenant_id AND psb.site_id = tp.site_id
 AND psb.point_id = tp.id AND psb.binding_role = 'CONTROLS'
 AND psb.subject_type = 'ASSET' AND psb.status = 'ACTIVE'
 AND psb.valid_from <= $3 AND (psb.valid_to IS NULL OR psb.valid_to > $3)
WHERE tp.tenant_id = $1::uuid AND tp.site_id = $2::uuid
  AND tp.point_type = 'COMMAND' AND tp.writable = true AND tp.status = 'ACTIVE'
  AND tp.source_metadata->>'capability' = $4
ORDER BY tp.point_code, tp.id
LIMIT 2
`, tenantID, siteID, at.UTC(), string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint))
	if err != nil {
		return AutoControlSiteContext{}, fmt.Errorf("lookup chilled water setpoint command point: %w", err)
	}
	defer rows.Close()
	targets := make([]SetpointCommandTarget, 0, 2)
	for rows.Next() {
		var t SetpointCommandTarget
		if scanErr := rows.Scan(&t.PointID, &t.DeviceID, &t.PointCode, &t.FeedbackPointKey, &t.AssetID, &t.AssetLocked); scanErr != nil {
			return AutoControlSiteContext{}, scanErr
		}
		t.SiteControlMode = topology.SiteControlMode
		targets = append(targets, t)
	}
	if err = rows.Err(); err != nil {
		return AutoControlSiteContext{}, err
	}
	if len(targets) == 1 {
		target := targets[0]
		result.SetpointTarget = &target
	}
	rows.Close()

	auxTargets, err := loadAuxiliarySiteTargets(ctx, tx, tenantID, siteID, at.UTC(), topology.SiteControlMode)
	if err != nil {
		return AutoControlSiteContext{}, err
	}
	result.AuxiliaryTargets = auxTargets

	if err = tx.Commit(ctx); err != nil {
		return AutoControlSiteContext{}, err
	}
	return result, nil
}

func loadAuxiliarySiteTargets(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, siteID string,
	at time.Time,
	siteControlMode string,
) ([]AuxiliaryLoopTarget, error) {
	rows, err := tx.Query(ctx, `
SELECT tp.id::text, tp.reporting_device_id::text, tp.point_code,
       COALESCE(tp.source_metadata->>'capability', ''),
       COALESCE(tp.source_metadata->>'feedbackPointKey', ''),
       COALESCE(psb.asset_id::text, ''),
       COALESCE(a.asset_type, ''),
       EXISTS (
         SELECT 1
         FROM core_registry.device_bindings db
         JOIN core_registry.asset_control_locks acl
           ON acl.tenant_id = db.tenant_id AND acl.site_id = db.site_id AND acl.asset_id = db.asset_id
         WHERE db.tenant_id = $1::uuid AND db.site_id = $2::uuid AND db.device_id = tp.reporting_device_id
           AND db.status = 'ACTIVE' AND db.valid_to IS NULL AND acl.lock_mode <> 'UNLOCKED'
       )
FROM core_registry.telemetry_points tp
JOIN core_registry.devices d
  ON d.tenant_id = tp.tenant_id AND d.site_id = tp.site_id AND d.id = tp.reporting_device_id AND d.status = 'ACTIVE'
LEFT JOIN core_registry.point_subject_bindings psb
  ON psb.tenant_id = tp.tenant_id AND psb.site_id = tp.site_id
 AND psb.point_id = tp.id AND psb.binding_role = 'CONTROLS'
 AND psb.subject_type = 'ASSET' AND psb.status = 'ACTIVE'
 AND psb.valid_from <= $3 AND (psb.valid_to IS NULL OR psb.valid_to > $3)
LEFT JOIN core_registry.assets a
  ON a.tenant_id = tp.tenant_id AND a.site_id = tp.site_id AND a.id = psb.asset_id
WHERE tp.tenant_id = $1::uuid AND tp.site_id = $2::uuid
  AND tp.point_type = 'COMMAND' AND tp.writable = true AND tp.status = 'ACTIVE'
  AND tp.source_metadata->>'capability' IN ($4, $5)
ORDER BY tp.point_code, tp.id
`, tenantID, siteID, at, string(commandmodel.CapabilitySetFrequency), string(commandmodel.CapabilitySetFanSpeed))
	if err != nil {
		return nil, fmt.Errorf("lookup auxiliary loop command points: %w", err)
	}
	defer rows.Close()

	grouped := make(map[string][]AuxiliaryLoopTarget)
	for rows.Next() {
		var target SetpointCommandTarget
		var capRaw, assetType string
		if scanErr := rows.Scan(
			&target.PointID, &target.DeviceID, &target.PointCode,
			&capRaw, &target.FeedbackPointKey, &target.AssetID, &assetType, &target.AssetLocked,
		); scanErr != nil {
			return nil, scanErr
		}
		target.SiteControlMode = siteControlMode
		capName := commandmodel.Capability(strings.TrimSpace(capRaw))
		loopKey, ok := resolveAuxiliaryLoopKey(capName, assetType, target.PointCode)
		if !ok {
			continue
		}
		groupKey := loopKey + "|" + string(capName)
		grouped[groupKey] = append(grouped[groupKey], AuxiliaryLoopTarget{
			LoopKey:    loopKey,
			Capability: capName,
			Target:     target,
			AssetType:  strings.TrimSpace(assetType),
		})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	out := make([]AuxiliaryLoopTarget, 0, len(grouped))
	for _, list := range grouped {
		if len(list) == 1 {
			out = append(out, list[0])
		}
	}
	slices.SortFunc(out, func(a, b AuxiliaryLoopTarget) int {
		if cmp := strings.Compare(a.LoopKey, b.LoopKey); cmp != 0 {
			return cmp
		}
		return strings.Compare(string(a.Capability), string(b.Capability))
	})
	return out, nil
}

func resolveAuxiliaryLoopKey(capability commandmodel.Capability, assetType, pointCode string) (string, bool) {
	cleanAssetType := strings.ToUpper(strings.TrimSpace(assetType))
	switch capability {
	case commandmodel.CapabilitySetFanSpeed:
		return LoopKeyCoolingTowerFan, true
	case commandmodel.CapabilitySetFrequency:
		switch cleanAssetType {
		case AssetTypeCHWP:
			return LoopKeyChilledWaterPump, true
		case AssetTypeCWP:
			return LoopKeyCoolingWaterPump, true
		case AssetTypeCoolingTower:
			return LoopKeyCoolingTowerFan, true
		}
		lowerCode := strings.ToLower(strings.TrimSpace(pointCode))
		switch {
		case strings.Contains(lowerCode, "chwp") || strings.Contains(lowerCode, "chw") || strings.Contains(lowerCode, "chilled"):
			return LoopKeyChilledWaterPump, true
		case strings.Contains(lowerCode, "cwp") || strings.Contains(lowerCode, "cw") || strings.Contains(lowerCode, "cooling_water"):
			return LoopKeyCoolingWaterPump, true
		case strings.Contains(lowerCode, "tower") || strings.Contains(lowerCode, "fan"):
			return LoopKeyCoolingTowerFan, true
		default:
			return "", false
		}
	default:
		return "", false
	}
}

// PreRegisterAutomatedDispatch atomically reserves a PENDING_SUBMIT monitor row under a site advisory lock
// before any outbound HTTP command is sent, closing crash split-brain and concurrent rate-limit bypasses.
func (store *PostgresStore) PreRegisterAutomatedDispatch(ctx context.Context, reg AutomatedDispatchRegistration) (PreRegisterDispatchResult, error) {
	return store.recordExecutionMonitor(ctx, reg, false)
}

// RecordShadowMonitor persists a SHADOW_RECORDED guardrail evaluation trace under a site advisory lock
// without reserving or dispatching a live closed-loop command.
func (store *PostgresStore) RecordShadowMonitor(ctx context.Context, reg AutomatedDispatchRegistration) (PreRegisterDispatchResult, error) {
	return store.recordExecutionMonitor(ctx, reg, true)
}

func (store *PostgresStore) recordExecutionMonitor(ctx context.Context, reg AutomatedDispatchRegistration, shadow bool) (PreRegisterDispatchResult, error) {
	if store == nil || store.pool == nil {
		return PreRegisterDispatchResult{}, errors.New("optimization postgres store is unavailable")
	}
	if reg.Recommendation.Revalidation == nil {
		return PreRegisterDispatchResult{}, errors.New("current-state revalidation is required before pre-registering automated dispatch")
	}
	if !uuidPattern.MatchString(reg.Recommendation.TenantID) ||
		!uuidPattern.MatchString(reg.Recommendation.SiteID) ||
		!uuidPattern.MatchString(reg.Recommendation.ID) ||
		!uuidPattern.MatchString(reg.LeaseID) ||
		!uuidPattern.MatchString(reg.Target.DeviceID) {
		return PreRegisterDispatchResult{}, errors.New("automated dispatch registration scope is invalid")
	}
	cleanDecisionID := strings.TrimSpace(reg.DecisionID)
	var decisionIDPtr *string
	if cleanDecisionID != "" {
		if !uuidPattern.MatchString(cleanDecisionID) {
			return PreRegisterDispatchResult{}, errors.New("automated dispatch decisionId is invalid")
		}
		decisionIDPtr = &cleanDecisionID
	}
	cleanTraceID := intelligencemodel.NormalizeTraceID32(reg.TraceID)
	nowUTC := reg.At.UTC()
	traceBytes := bytes.TrimSpace(reg.GuardrailTrace)
	if len(traceBytes) == 0 || string(traceBytes) == "null" {
		traceBytes = []byte("[]")
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return PreRegisterDispatchResult{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, reg.Recommendation.TenantID, reg.Recommendation.SiteID); err != nil {
		return PreRegisterDispatchResult{}, err
	}

	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('auto-control-dispatch:' || $1 || ':' || $2))`,
		reg.Recommendation.TenantID, reg.Recommendation.SiteID); err != nil {
		return PreRegisterDispatchResult{}, fmt.Errorf("acquire site auto-control dispatch lock: %w", err)
	}

	var maxCommandsPerHour int
	var manualBackoffUntil *time.Time
	var dbExecutionMode string
	err = tx.QueryRow(ctx, `
SELECT max_commands_per_hour, manual_backoff_until, COALESCE(execution_mode, 'CLOSED_LOOP')
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'ACTIVE'
  AND expires_at > $4
FOR UPDATE
`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, reg.LeaseID, nowUTC).Scan(&maxCommandsPerHour, &manualBackoffUntil, &dbExecutionMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreRegisterDispatchResult{SkipReason: "MANUAL_CONFIRM"}, nil
	}
	if err != nil {
		return PreRegisterDispatchResult{}, fmt.Errorf("recheck active auto-control lease: %w", err)
	}
	if manualBackoffUntil != nil && manualBackoffUntil.UTC().After(nowUTC) {
		return PreRegisterDispatchResult{SkipReason: "MANUAL_BACKOFF_ACTIVE"}, nil
	}
	if !shadow && normalizeExecutionMode(dbExecutionMode) == ExecutionModeShadow {
		return PreRegisterDispatchResult{SkipReason: "SHADOW_MODE_ACTIVE"}, nil
	}

	targetCap := reg.TargetCapability
	if strings.TrimSpace(string(targetCap)) == "" {
		targetCap = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	}
	loopKey := normalizeMonitorLoopKey(reg.LoopKey, targetCap)
	targetUnit := firstNonEmptyString(strings.TrimSpace(reg.TargetUnit), targetUnitForCapability(targetCap))

	var approvalState string
	var alreadyDispatched bool
	err = tx.QueryRow(ctx, `
SELECT approval_state, command_intent_id IS NOT NULL
FROM core_registry.optimization_recommendations
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
FOR UPDATE
`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, reg.Recommendation.ID).Scan(&approvalState, &alreadyDispatched)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreRegisterDispatchResult{}, errors.New("optimization recommendation missing")
	}
	if err != nil {
		return PreRegisterDispatchResult{}, fmt.Errorf("lock optimization recommendation for dispatch: %w", err)
	}
	if loopKey == LoopKeyPlant && alreadyDispatched {
		return PreRegisterDispatchResult{SkipReason: "ALREADY_DISPATCHED"}, nil
	}
	if loopKey != LoopKeyPlant && !shadow {
		var loopDup bool
		if err = tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM core_registry.optimization_execution_monitors
  WHERE tenant_id = $1::uuid
    AND site_id = $2::uuid
    AND recommendation_id = $3::uuid
    AND COALESCE(loop_key, 'PLANT') = $4
    AND status <> 'SUBMIT_FAILED'
)`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, reg.Recommendation.ID, loopKey).Scan(&loopDup); err != nil {
			return PreRegisterDispatchResult{}, fmt.Errorf("check duplicate auxiliary monitor: %w", err)
		}
		if loopDup {
			return PreRegisterDispatchResult{SkipReason: "ALREADY_DISPATCHED"}, nil
		}
	}
	if approvalState == "REJECTED" {
		return PreRegisterDispatchResult{SkipReason: "RECOMMENDATION_REJECTED"}, nil
	}

	if shadow {
		var recentShadowCount int
		if err = tx.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND started_at >= $3
  AND status = 'SHADOW_RECORDED'
`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, nowUTC.Add(-time.Hour)).Scan(&recentShadowCount); err != nil {
			return PreRegisterDispatchResult{}, fmt.Errorf("enforce hourly shadow rate limit: %w", err)
		}
		if recentShadowCount >= DefaultShadowMaxPerHour {
			return PreRegisterDispatchResult{SkipReason: "SHADOW_RATE_LIMIT_REACHED"}, nil
		}
	} else {
		stalePendingCutoff := nowUTC.Add(-DefaultPendingSubmitStaleAge)
		if _, err = tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = 'SUBMIT_FAILED',
    trip_reason = 'pending_submit_stale_timeout',
    updated_at = $3
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND COALESCE(loop_key, 'PLANT') = $5
  AND status = 'PENDING_SUBMIT'
  AND started_at < $4
`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, nowUTC, stalePendingCutoff, loopKey); err != nil {
			return PreRegisterDispatchResult{}, fmt.Errorf("sweep stale pending execution monitors: %w", err)
		}

		var activeMonitorCount int
		if err = tx.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND COALESCE(loop_key, 'PLANT') = $3
  AND status IN ('PENDING_SUBMIT', 'OBSERVING')
`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, loopKey).Scan(&activeMonitorCount); err != nil {
			return PreRegisterDispatchResult{}, fmt.Errorf("check active execution monitors: %w", err)
		}
		if activeMonitorCount > 0 {
			return PreRegisterDispatchResult{SkipReason: "ACTIVE_MONITOR_EXISTS"}, nil
		}

		var recentCount int
		if err = tx.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND started_at >= $3
  AND status IN ('PENDING_SUBMIT', 'OBSERVING', 'VERIFIED', 'ROLLED_BACK', 'TRIPPED', 'TRIPPED_ROLLED_BACK', 'ROLLBACK_FAILED', 'MANUAL_OVERRIDE')
`, reg.Recommendation.TenantID, reg.Recommendation.SiteID, nowUTC.Add(-time.Hour)).Scan(&recentCount); err != nil {
			return PreRegisterDispatchResult{}, fmt.Errorf("enforce hourly auto-control rate limit: %w", err)
		}
		if recentCount >= maxCommandsPerHour {
			return PreRegisterDispatchResult{SkipReason: "RATE_LIMIT_REACHED"}, nil
		}
	}

	monitorID, err := optimizationUUIDv7(nowUTC)
	if err != nil {
		return PreRegisterDispatchResult{}, err
	}
	status := ExecutionMonitorPendingSubmit
	executionMode := ExecutionModeClosedLoop
	if shadow {
		status = ExecutionMonitorShadowRecorded
		executionMode = ExecutionModeShadow
	}
	windowEndsAt := nowUTC.Add(DefaultExecutionMonitorWindow)
	supplyMinC := reg.SupplyMinC
	if targetCap == commandmodel.CapabilitySetChilledWaterTemperatureSetpoint || targetCap == commandmodel.CapabilitySetTemperatureSetpoint {
		supplyMinC = math.Max(DefaultSupplyTempHardFloorC, reg.SupplyMinC)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.optimization_execution_monitors (
  id, tenant_id, site_id, lease_id, recommendation_id, command_intent_id,
  device_id, target_capability, loop_key, target_unit,
  baseline_supply_temp_c, applied_supply_temp_c,
  restore_supply_temp_c, zone_temp_max_c, supply_temp_min_c, supply_temp_max_c,
  raw_recommended_value, clamped_value, guardrail_verdict, guardrail_trace,
  started_at, window_ends_at, last_checked_at, status,
  execution_mode, decision_id, trace_id, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, NULL,
  $6::uuid, $7, $24, $25,
  $8, $9,
  $10, $11, $12, $13,
  $14, $15, $16, $17::jsonb,
  $18, $19, $18, $20,
  $21, $22::uuid, $23, $18, $18
)`,
		monitorID, reg.Recommendation.TenantID, reg.Recommendation.SiteID, reg.LeaseID, reg.Recommendation.ID,
		reg.Target.DeviceID, string(targetCap),
		reg.BaselineSupplyC, reg.AppliedSupplyC,
		reg.RestoreSupplyC, reg.ZoneMaxC, supplyMinC, reg.SupplyMaxC,
		reg.RawRecommendedC, reg.ClampedC, reg.GuardrailVerdict, traceBytes,
		nowUTC, windowEndsAt, status,
		executionMode, decisionIDPtr, cleanTraceID, loopKey, targetUnit,
	); err != nil {
		return PreRegisterDispatchResult{}, fmt.Errorf("insert optimization execution monitor (%s): %w", status, err)
	}

	if err = tx.Commit(ctx); err != nil {
		return PreRegisterDispatchResult{}, err
	}
	rawCopy := reg.RawRecommendedC
	return PreRegisterDispatchResult{
		Monitor: OptimizationExecutionMonitorRecord{
			ID:                  monitorID,
			TenantID:            reg.Recommendation.TenantID,
			SiteID:              reg.Recommendation.SiteID,
			LeaseID:             reg.LeaseID,
			RecommendationID:    reg.Recommendation.ID,
			DeviceID:            reg.Target.DeviceID,
			TargetCapability:    string(targetCap),
			LoopKey:             loopKey,
			TargetUnit:          targetUnit,
			BaselineSupplyTempC: &reg.BaselineSupplyC,
			AppliedSupplyTempC:  &reg.AppliedSupplyC,
			RestoreSupplyTempC:  &reg.RestoreSupplyC,
			ZoneTempMaxC:        reg.ZoneMaxC,
			SupplyTempMinC:      supplyMinC,
			SupplyTempMaxC:      reg.SupplyMaxC,
			RawRecommendedValue: &rawCopy,
			ClampedValue:        reg.ClampedC,
			GuardrailVerdict:    reg.GuardrailVerdict,
			GuardrailTrace:      append(json.RawMessage(nil), traceBytes...),
			StartedAt:           nowUTC,
			WindowEndsAt:        windowEndsAt,
			LastCheckedAt:       &nowUTC,
			Status:              status,
			ExecutionMode:       executionMode,
			DecisionID:          cleanDecisionID,
			TraceID:             cleanTraceID,
		},
	}, nil
}

func (store *PostgresStore) ActivateAutomatedDispatch(ctx context.Context, recommendation Recommendation, monitorID, commandIntentID string, at time.Time) error {
	if store == nil || store.pool == nil {
		return errors.New("optimization postgres store is unavailable")
	}
	if recommendation.Revalidation == nil {
		return errors.New("current-state revalidation is required before activating automated dispatch")
	}
	if !uuidPattern.MatchString(recommendation.TenantID) ||
		!uuidPattern.MatchString(recommendation.SiteID) ||
		!uuidPattern.MatchString(recommendation.ID) ||
		!uuidPattern.MatchString(monitorID) ||
		!uuidPattern.MatchString(commandIntentID) {
		return errors.New("automated dispatch activation scope is invalid")
	}
	nowUTC := at.UTC()
	revalBytes := encodeOptimizationJSON(recommendation.Revalidation)

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, recommendation.TenantID, recommendation.SiteID); err != nil {
		return err
	}

	var monitorLoopKey string
	if err = tx.QueryRow(ctx, `
SELECT COALESCE(loop_key, 'PLANT')
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
FOR UPDATE
`, recommendation.TenantID, recommendation.SiteID, monitorID).Scan(&monitorLoopKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("pending optimization execution monitor missing or already transitioned")
		}
		return fmt.Errorf("lock pending optimization execution monitor: %w", err)
	}

	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_recommendations
SET approval_state = 'APPROVED',
    current_state_revalidation = $4::jsonb,
    command_intent_id = $5::uuid,
    updated_at = $6,
    revision = revision + 1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND command_intent_id IS NULL
`, recommendation.TenantID, recommendation.SiteID, recommendation.ID, revalBytes, commandIntentID, nowUTC)
	if err != nil {
		return fmt.Errorf("record approved revalidated optimization recommendation: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(monitorLoopKey), LoopKeyPlant) && tag.RowsAffected() != 1 {
		return errors.New("optimization recommendation already dispatched or missing")
	}

	monTag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = 'OBSERVING',
    command_intent_id = $4::uuid,
    last_checked_at = $5,
    updated_at = GREATEST(updated_at, $5)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'PENDING_SUBMIT'
`, recommendation.TenantID, recommendation.SiteID, monitorID, commandIntentID, nowUTC)
	if err != nil {
		return fmt.Errorf("activate optimization execution monitor: %w", err)
	}
	if monTag.RowsAffected() != 1 {
		return errors.New("pending optimization execution monitor missing or already transitioned")
	}

	return tx.Commit(ctx)
}

func (store *PostgresStore) MarkAutomatedDispatchFailed(ctx context.Context, tenantID, siteID, monitorID, errorSummary string, at time.Time) error {
	if store == nil || store.pool == nil {
		return errors.New("optimization postgres store is unavailable")
	}
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !uuidPattern.MatchString(monitorID) {
		return errors.New("automated dispatch failure scope is invalid")
	}
	nowUTC := at.UTC()
	summary := truncateSummary(strings.TrimSpace(errorSummary), 500)
	if summary == "" {
		summary = "automated command submission failed"
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = 'SUBMIT_FAILED',
    trip_reason = $4,
    last_checked_at = $5,
    updated_at = GREATEST(updated_at, $5)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'PENDING_SUBMIT'
`, tenantID, siteID, monitorID, summary, nowUTC)
	if err != nil {
		return fmt.Errorf("mark optimization execution monitor submit failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("pending optimization execution monitor missing or already transitioned")
	}
	return tx.Commit(ctx)
}

func truncateSummary(raw string, maxRunes int) string {
	if maxRunes <= 0 || raw == "" {
		return raw
	}
	count := 0
	for idx := range raw {
		if count == maxRunes {
			return raw[:idx]
		}
		count++
	}
	return raw
}

func scanExecutionMonitorRow(row pgx.Row) (OptimizationExecutionMonitorRecord, error) {
	var item OptimizationExecutionMonitorRecord
	var decisionID *string
	err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID, &item.LeaseID, &item.RecommendationID,
		&item.CommandIntentID, &item.DeviceID, &item.TargetCapability, &item.LoopKey, &item.TargetUnit,
		&item.BaselineSupplyTempC, &item.AppliedSupplyTempC, &item.RestoreSupplyTempC,
		&item.ZoneTempMaxC, &item.SupplyTempMinC, &item.SupplyTempMaxC,
		&item.RawRecommendedValue, &item.ClampedValue, &item.GuardrailVerdict, &item.GuardrailTrace,
		&item.StartedAt, &item.WindowEndsAt,
		&item.LastCheckedAt, &item.TelemetryUnhealthySince, &item.Status, &item.TripReason,
		&item.RollbackCommandIntentID, &item.ClaimOwner, &item.ClaimUntil,
		&item.ExecutionMode, &decisionID, &item.TraceID,
	)
	if err != nil {
		return OptimizationExecutionMonitorRecord{}, err
	}
	if decisionID != nil {
		item.DecisionID = *decisionID
	}
	item.ExecutionMode = normalizeExecutionMode(item.ExecutionMode)
	return item, nil
}

// ClaimObservingExecutionMonitors atomically leases up to limit OBSERVING monitors using SKIP LOCKED
// so multiple replicas never process or roll back the same monitor concurrently.
func (store *PostgresStore) ClaimObservingExecutionMonitors(ctx context.Context, claimOwner string, leaseDuration time.Duration, now time.Time, limit int) ([]OptimizationExecutionMonitorRecord, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("optimization postgres store is unavailable")
	}
	owner := strings.TrimSpace(claimOwner)
	if owner == "" || leaseDuration <= 0 {
		return nil, errors.New("execution monitor claim owner and positive lease duration are required")
	}
	if limit <= 0 || limit > 500 {
		limit = DefaultExecutionMonitorClaimLimit
	}
	nowUTC := now.UTC()
	claimUntil := nowUTC.Add(leaseDuration)

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT m.id::text, m.tenant_id::text, m.site_id::text, m.lease_id::text, m.recommendation_id::text,
       COALESCE(m.command_intent_id::text, ''), m.device_id::text, m.target_capability,
       COALESCE(m.loop_key, 'PLANT'), COALESCE(m.target_unit, 'C'),
       m.baseline_supply_temp_c, m.applied_supply_temp_c, m.restore_supply_temp_c,
       m.zone_temp_max_c, m.supply_temp_min_c, m.supply_temp_max_c,
       m.raw_recommended_value, m.clamped_value, m.guardrail_verdict, m.guardrail_trace,
       m.started_at, m.window_ends_at,
       m.last_checked_at, m.telemetry_unhealthy_since, m.status, m.trip_reason,
       CASE WHEN m.rollback_command_intent_id IS NULL THEN NULL ELSE m.rollback_command_intent_id::text END,
       m.claim_owner, m.claim_until,
       COALESCE(m.execution_mode, 'CLOSED_LOOP'),
       CASE WHEN m.decision_id IS NULL THEN NULL ELSE m.decision_id::text END,
       COALESCE(m.trace_id, '')
FROM core_registry.claim_observing_execution_monitors($1, $2, $3, $4) AS m
`, owner, claimUntil, nowUTC, limit)
	if err != nil {
		return nil, fmt.Errorf("claim observing execution monitors: %w", err)
	}
	items := []OptimizationExecutionMonitorRecord{}
	for rows.Next() {
		item, scanErr := scanExecutionMonitorRow(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		items = append(items, item)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, rowsErr
	}
	rows.Close()

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b OptimizationExecutionMonitorRecord) int {
		if cmp := a.WindowEndsAt.Compare(b.WindowEndsAt); cmp != 0 {
			return cmp
		}
		if cmp := a.StartedAt.Compare(b.StartedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	return items, nil
}

func validateMonitorClaimScope(store *PostgresStore, monitorID, tenantID, siteID, claimOwner string) (string, error) {
	if store == nil || store.pool == nil {
		return "", errors.New("optimization postgres store is unavailable")
	}
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !uuidPattern.MatchString(monitorID) {
		return "", errors.New("execution monitor scope is invalid")
	}
	owner := strings.TrimSpace(claimOwner)
	if owner == "" {
		return "", errors.New("execution monitor claim owner is required")
	}
	return owner, nil
}

func (store *PostgresStore) ReleaseMonitorClaim(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, releasedAt time.Time) error {
	owner, err := validateMonitorClaimScope(store, monitorID, tenantID, siteID, claimOwner)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET claim_owner = NULL,
    claim_until = NULL,
    updated_at = GREATEST(updated_at, $4)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'OBSERVING'
  AND claim_owner = $5
`, tenantID, siteID, monitorID, releasedAt.UTC(), owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionMonitorClaimLost
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) RecordMonitorHealthyHeartbeat(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, checkedAt time.Time) error {
	owner, err := validateMonitorClaimScope(store, monitorID, tenantID, siteID, claimOwner)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET telemetry_unhealthy_since = NULL,
    last_checked_at = $4,
    claim_owner = NULL,
    claim_until = NULL,
    updated_at = GREATEST(updated_at, $4)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'OBSERVING'
  AND claim_owner = $5
`, tenantID, siteID, monitorID, checkedAt.UTC(), owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionMonitorClaimLost
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) RecordMonitorTelemetryUnhealthy(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, unhealthySince, checkedAt time.Time) error {
	owner, err := validateMonitorClaimScope(store, monitorID, tenantID, siteID, claimOwner)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET telemetry_unhealthy_since = COALESCE(telemetry_unhealthy_since, $4),
    last_checked_at = $5,
    claim_owner = NULL,
    claim_until = NULL,
    updated_at = GREATEST(updated_at, $5)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'OBSERVING'
  AND claim_owner = $6
`, tenantID, siteID, monitorID, unhealthySince.UTC(), checkedAt.UTC(), owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionMonitorClaimLost
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) CompleteMonitorVerified(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, checkedAt time.Time) error {
	owner, err := validateMonitorClaimScope(store, monitorID, tenantID, siteID, claimOwner)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = 'VERIFIED',
    telemetry_unhealthy_since = NULL,
    last_checked_at = $4,
    claim_owner = NULL,
    claim_until = NULL,
    updated_at = GREATEST(updated_at, $4)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'OBSERVING'
  AND claim_owner = $5
`, tenantID, siteID, monitorID, checkedAt.UTC(), owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionMonitorClaimLost
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) MarkMonitorManualOverride(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	claimOwner, reason string,
	backoffUntil, checkedAt time.Time,
) error {
	owner, err := validateMonitorClaimScope(store, monitor.ID, monitor.TenantID, monitor.SiteID, claimOwner)
	if err != nil {
		return err
	}
	nowUTC := checkedAt.UTC()
	backoffUTC := backoffUntil.UTC()
	cleanReason := strings.TrimSpace(reason)
	if cleanReason == "" {
		cleanReason = "cabinet_setpoint_manual_override"
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, monitor.TenantID, monitor.SiteID); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = 'MANUAL_OVERRIDE',
    trip_reason = $4,
    last_checked_at = $5,
    claim_owner = NULL,
    claim_until = NULL,
    updated_at = GREATEST(updated_at, $5)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'OBSERVING'
  AND claim_owner = $6
`, monitor.TenantID, monitor.SiteID, monitor.ID, cleanReason, nowUTC, owner)
	if err != nil {
		return fmt.Errorf("mark execution monitor manual override: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionMonitorClaimLost
	}

	if _, err = tx.Exec(ctx, `
UPDATE core_registry.site_auto_control_leases
SET manual_backoff_until = $4,
    manual_backoff_reason = $5,
    revision = revision + 1,
    updated_at = $6
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND status = 'ACTIVE'
`, monitor.TenantID, monitor.SiteID, monitor.LeaseID, backoffUTC, cleanReason, nowUTC); err != nil {
		return fmt.Errorf("set manual backoff on active auto control lease: %w", err)
	}

	return tx.Commit(ctx)
}

func (store *PostgresStore) TripMonitorAndLease(ctx context.Context, monitor OptimizationExecutionMonitorRecord, claimOwner, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error {
	owner, err := validateMonitorClaimScope(store, monitor.ID, monitor.TenantID, monitor.SiteID, claimOwner)
	if err != nil {
		return err
	}
	nowUTC := trippedAt.UTC()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, monitor.TenantID, monitor.SiteID); err != nil {
		return err
	}

	// Fence the monitor row first so a slow replica whose lease expired cannot trip the lease or emit a duplicate outbox event.
	tag, err := tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = $4,
    trip_reason = $5,
    rollback_command_intent_id = $6::uuid,
    last_checked_at = $7,
    claim_owner = NULL,
    claim_until = NULL,
    updated_at = GREATEST(updated_at, $7)
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND id = $3::uuid
  AND status = 'OBSERVING'
  AND claim_owner = $8
`, monitor.TenantID, monitor.SiteID, monitor.ID, newStatus, tripReason, rollbackCommandIntentID, nowUTC, owner)
	if err != nil {
		return fmt.Errorf("update tripped execution monitor: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionMonitorClaimLost
	}

	var nextLeaseRevision int64 = 2
	if err = tx.QueryRow(ctx, `
UPDATE core_registry.site_auto_control_leases
SET status = 'TRIPPED', trip_reason = $4, revision = revision + 1, updated_at = $5
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND status = 'ACTIVE'
RETURNING revision
`, monitor.TenantID, monitor.SiteID, monitor.LeaseID, tripReason, nowUTC).Scan(&nextLeaseRevision); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("trip site auto control lease: %w", err)
	}

	eventID, err := optimizationUUIDv7(nowUTC)
	if err != nil {
		return err
	}
	type autoControlTripEventPayload struct {
		SiteID                  string  `json:"siteId"`
		LeaseID                 string  `json:"leaseId"`
		MonitorID               string  `json:"monitorId"`
		RecommendationID        string  `json:"recommendationId"`
		CommandIntentID         string  `json:"commandIntentId"`
		Status                  string  `json:"status"`
		TripReason              string  `json:"tripReason"`
		RollbackCommandIntentID *string `json:"rollbackCommandIntentId"`
	}
	payload := encodeOptimizationJSON(autoControlTripEventPayload{
		SiteID:                  monitor.SiteID,
		LeaseID:                 monitor.LeaseID,
		MonitorID:               monitor.ID,
		RecommendationID:        monitor.RecommendationID,
		CommandIntentID:         monitor.CommandIntentID,
		Status:                  newStatus,
		TripReason:              tripReason,
		RollbackCommandIntentID: rollbackCommandIntentID,
	})
	if _, err = tx.Exec(ctx, `
INSERT INTO core_registry.domain_outbox_events(
  id, tenant_id, site_id, event_type, schema_version, subject_type, subject_id,
  aggregate_type, aggregate_id, aggregate_version, occurred_at, payload, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, 'AUTO_CONTROL_LEASE_TRIPPED', 1, 'SITE', $3::uuid,
  'AUTO_CONTROL_LEASE', $4::uuid, $5, $6, $7::jsonb, $6
)
ON CONFLICT (tenant_id, aggregate_type, aggregate_id, aggregate_version, event_type) DO NOTHING
`, eventID, monitor.TenantID, monitor.SiteID, monitor.LeaseID, nextLeaseRevision, nowUTC, payload); err != nil {
		return fmt.Errorf("emit auto control trip event: %w", err)
	}
	var actualEventID string
	if err = tx.QueryRow(ctx, `
SELECT id::text FROM core_registry.domain_outbox_events
WHERE tenant_id = $1::uuid AND aggregate_type = 'AUTO_CONTROL_LEASE'
  AND aggregate_id = $2::uuid AND aggregate_version = $3 AND event_type = 'AUTO_CONTROL_LEASE_TRIPPED'
`, monitor.TenantID, monitor.LeaseID, nextLeaseRevision).Scan(&actualEventID); err == nil && actualEventID != "" {
		if _, err = tx.Exec(ctx, `
INSERT INTO core_registry.domain_event_deliveries(event_id, consumer_name, status, attempt, created_at, updated_at)
VALUES($1::uuid, 'audit-notification', 'PENDING', 0, $2, $2)
ON CONFLICT (event_id, consumer_name) DO NOTHING
`, actualEventID, nowUTC); err != nil {
			return fmt.Errorf("enqueue trip notification delivery: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (reader *ClickHouseInputReader) ReadLiveAutoControlObservation(ctx context.Context, tenantID, siteID string, mapping InputMapping, topology StagingTopology, at time.Time) (AutoControlLiveObservation, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return AutoControlLiveObservation{}, errors.New("optimization live telemetry reader is unavailable")
	}
	atUTC := at.UTC()
	query := OptimizationStateQuery{
		TenantID:             tenantID,
		SiteID:               siteID,
		SubjectType:          "SITE",
		SubjectID:            siteID,
		At:                   atUTC,
		SupplyTemperatureKey: strings.TrimSpace(mapping.SupplyTemperatureKey),
		ZoneTemperatureKeys:  mapping.effectiveZoneKeys(),
	}
	result := AutoControlLiveObservation{
		ZoneTemps:       map[string]TelemetryEvidence{},
		ChillersHealthy: true,
	}

	wetBulbCandidateKeys := []string{
		"outdoor_wet_bulb",
		"weather.ambient_wet_bulb_temperature",
		"ambient_wet_bulb_temperature",
		"cooling_tower.ambient_wet_bulb_temperature",
	}
	zoneKeys := query.effectiveZoneKeys()
	allKeys := make([]string, 0, 1+len(zoneKeys)+len(wetBulbCandidateKeys))
	if query.SupplyTemperatureKey != "" {
		allKeys = append(allKeys, query.SupplyTemperatureKey)
	}
	allKeys = append(allKeys, zoneKeys...)
	allKeys = append(allKeys, wetBulbCandidateKeys...)
	useLive, liveResult, liveAmbiguous := reader.readLiveObservationsKeys(ctx, tenantID, siteID, atUTC, allKeys)

	if query.SupplyTemperatureKey == "" {
		result.SupplyErr = unavailable(ReasonSupplyTempMissing, "supplyTemperatureKey is missing")
	} else {
		supplyObs, _, supplyErr := reader.readStateTelemetry(ctx, query, query.SupplyTemperatureKey, ReasonSupplyTempMissing, useLive, liveResult, liveAmbiguous)
		if supplyErr != nil {
			result.SupplyErr = supplyErr
		} else if validErr := validateTelemetryEvidence(supplyObs, query.SupplyTemperatureKey, atUTC); validErr != nil {
			result.SupplyErr = validErr
		} else {
			result.SupplyTemp = &supplyObs
		}
	}

	if len(zoneKeys) == 0 {
		result.ZoneErr = unavailable(ReasonZoneTempMissing, "no zoneTemperatureKeys configured")
	} else {
		for _, zoneKey := range zoneKeys {
			obs, _, zoneErr := reader.readStateTelemetry(ctx, query, zoneKey, ReasonZoneTempMissing, useLive, liveResult, liveAmbiguous)
			if zoneErr != nil {
				result.ZoneErr = zoneErr
				break
			}
			if validErr := validateTelemetryEvidence(obs, zoneKey, atUTC); validErr != nil {
				result.ZoneErr = validErr
				break
			}
			result.ZoneTemps[zoneKey] = obs
		}
	}

	stagingPolicy := DefaultStagingPolicy()
	stagingPolicy.MaxTelemetryAge = DefaultAutoControlMaxPointAge
	stagingSnap, snapErr := reader.ReadStagingSnapshot(ctx, StagingSnapshotQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		At:       atUTC,
		Policy:   stagingPolicy,
		Topology: &topology,
	})
	if snapErr != nil {
		result.ChillersHealthy = false
	} else {
		if stagingSnap.PlantCoolingLoad != nil &&
			strings.EqualFold(strings.TrimSpace(stagingSnap.PlantCoolingLoad.Quality), "GOOD") &&
			atUTC.Sub(stagingSnap.PlantCoolingLoad.SampledAt.UTC()) <= DefaultAutoControlMaxPointAge &&
			finite(stagingSnap.PlantCoolingLoad.Value) &&
			stagingSnap.PlantCoolingLoad.Value >= 0 {
			loadVal := stagingSnap.PlantCoolingLoad.Value
			result.CoolingLoadKW = &loadVal
		}
		chillerPowerSum := 0.0
		hasChillerPower := false
		for _, chiller := range stagingSnap.Chillers {
			state, _ := resolveChillerRunState(chiller, atUTC, stagingPolicy)
			running := state == "RUNNING"
			if !running && chiller.Power != nil && chiller.Power.Quality == "GOOD" &&
				atUTC.Sub(chiller.Power.SampledAt.UTC()) <= DefaultAutoControlMaxPointAge &&
				chiller.Power.Value > stagingPolicy.ChillerRunningPowerKW {
				running = true
			}
			if !running {
				continue
			}
			result.RunningChillers++
			if chiller.Power != nil &&
				strings.EqualFold(strings.TrimSpace(chiller.Power.Quality), "GOOD") &&
				atUTC.Sub(chiller.Power.SampledAt.UTC()) <= DefaultAutoControlMaxPointAge &&
				finite(chiller.Power.Value) &&
				chiller.Power.Value >= 0 {
				chillerPowerSum += chiller.Power.Value
				hasChillerPower = true
			}
			if chiller.RatedPowerKW > 0 {
				if chiller.Power == nil || chiller.Power.Quality != "GOOD" || atUTC.Sub(chiller.Power.SampledAt.UTC()) > DefaultAutoControlMaxPointAge {
					result.ChillersHealthy = false
					continue
				}
				if chiller.Power.Value > DefaultChillerPowerRatioLimit*chiller.RatedPowerKW {
					result.ChillerPowerTrip = &ChillerPowerViolation{
						AssetID:      chiller.AssetID,
						AssetCode:    chiller.AssetCode,
						PowerKW:      chiller.Power.Value,
						RatedPowerKW: chiller.RatedPowerKW,
					}
				}
			}
		}
		if hasChillerPower {
			result.ChillerPowerKW = &chillerPowerSum
		}
	}
	if wetBulb, hit := selectLiveWetBulb(useLive, liveResult, liveAmbiguous, wetBulbCandidateKeys, atUTC); hit {
		result.OutdoorWetBulbC = wetBulb
	} else {
		result.OutdoorWetBulbC = reader.readOptionalWetBulb(ctx, tenantID, siteID, atUTC)
	}
	return result, nil
}

func selectLiveWetBulb(
	useLive bool,
	liveResult OptimizationLiveObservationsResult,
	liveAmbiguous map[string]struct{},
	candidateKeys []string,
	atUTC time.Time,
) (*float64, bool) {
	if !useLive {
		return nil, false
	}
	for _, key := range candidateKeys {
		if _, ambiguous := liveAmbiguous[key]; ambiguous {
			continue
		}
		obs, found := liveResult.Observations[key]
		if !found {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(obs.Quality), "GOOD") {
			continue
		}
		val := obs.Value
		if obs.ValueNumber != nil {
			val = *obs.ValueNumber
		}
		if !finite(val) {
			continue
		}
		sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(obs.SampledAt))
		if err != nil {
			continue
		}
		sampledUTC := sampledAt.UTC()
		if sampledUTC.After(atUTC) || atUTC.Sub(sampledUTC) > DefaultPreparationMaxTelemetryAge {
			continue
		}
		if rawReceived := strings.TrimSpace(obs.ReceivedAt); rawReceived != "" {
			receivedAt, err := time.Parse(time.RFC3339Nano, rawReceived)
			if err != nil || receivedAt.UTC().After(atUTC) {
				continue
			}
		}
		valCopy := val
		return &valCopy, true
	}
	return nil, false
}

func (reader *ClickHouseInputReader) readOptionalWetBulb(ctx context.Context, tenantID, siteID string, atUTC time.Time) *float64 {
	if !uuidPattern.MatchString(strings.TrimSpace(tenantID)) || !uuidPattern.MatchString(strings.TrimSpace(siteID)) {
		return nil
	}
	earliest := atUTC.Add(-DefaultPreparationMaxTelemetryAge)
	sql := fmt.Sprintf(`SELECT assumeNotNull(value_number) AS value
FROM telemetry_history.observations
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND telemetry_key IN ('outdoor_wet_bulb','weather.ambient_wet_bulb_temperature','ambient_wet_bulb_temperature','cooling_tower.ambient_wet_bulb_temperature')
  AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
  AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
  AND quality = 'GOOD'
  AND value_number IS NOT NULL
  AND isFinite(value_number)
ORDER BY CASE telemetry_key
  WHEN 'outdoor_wet_bulb' THEN 0
  WHEN 'weather.ambient_wet_bulb_temperature' THEN 1
  WHEN 'ambient_wet_bulb_temperature' THEN 2
  ELSE 3
END, sampled_at DESC, received_at DESC
LIMIT 1
FORMAT JSONEachRow`,
		clickHouseString(tenantID), clickHouseString(siteID),
		formatOptimizationTime(earliest), formatOptimizationTime(atUTC), formatOptimizationTime(atUTC))
	payload, err := reader.query(ctx, sql)
	if err != nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	var row struct {
		Value float64 `json:"value"`
	}
	if err := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(payload))).Decode(&row); err != nil || !finite(row.Value) {
		return nil
	}
	return &row.Value
}
