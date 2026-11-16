package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
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
       zone_temp_max_c, min_running_chillers, max_commands_per_hour, revision
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE' AND expires_at > $3
ORDER BY armed_at DESC, id DESC
LIMIT 1
`, tenantID, siteID, at.UTC()).Scan(
		&lease.ID, &lease.TenantID, &lease.SiteID, &lease.Status, &lease.ArmedBy, &lease.ArmedAt, &lease.ExpiresAt,
		&lease.AllowedCapabilities, &lease.MaxSetpointStepC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC,
		&lease.ZoneTempMaxC, &lease.MinRunningChillers, &lease.MaxCommandsPerHour, &lease.Revision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load active site auto control lease: %w", err)
	}
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

	result := AutoControlSiteContext{
		SiteControlMode: topology.SiteControlMode,
		Topology:        topology,
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

	if err = tx.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND started_at >= $3
`, tenantID, siteID, at.UTC().Add(-time.Hour)).Scan(&result.RecentAutoCountHour); err != nil {
		return AutoControlSiteContext{}, fmt.Errorf("count recent optimization monitors: %w", err)
	}

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

	if err = tx.Commit(ctx); err != nil {
		return AutoControlSiteContext{}, err
	}
	return result, nil
}

func (store *PostgresStore) CheckSiteTelemetryHealth(ctx context.Context, tenantID, siteID string, keys []string) error {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return errors.New("telemetry health check scope is invalid")
	}
	twin, err := store.LoadSiteTwin(ctx, planttwin.SiteScope{TenantID: tenantID, SiteID: siteID}, time.Now().UTC())
	if err != nil {
		return err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	if err = checkTelemetryPointHealth(ctx, tx, siteID, twinDeviceUUIDs(twin), keys); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) RecordAutomatedDispatch(
	ctx context.Context,
	recommendation Recommendation,
	leaseID string,
	target SetpointCommandTarget,
	commandIntentID string,
	baselineSupplyC, appliedSupplyC, restoreSupplyC, zoneMaxC, supplyMaxC float64,
	at time.Time,
) (OptimizationExecutionMonitorRecord, error) {
	if recommendation.Revalidation == nil {
		return OptimizationExecutionMonitorRecord{}, errors.New("current-state revalidation is required before recording automated dispatch")
	}
	nowUTC := at.UTC()
	revalBytes := encodeOptimizationJSON(recommendation.Revalidation)

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return OptimizationExecutionMonitorRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, recommendation.TenantID, recommendation.SiteID); err != nil {
		return OptimizationExecutionMonitorRecord{}, err
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
		return OptimizationExecutionMonitorRecord{}, fmt.Errorf("record approved revalidated optimization recommendation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return OptimizationExecutionMonitorRecord{}, errors.New("optimization recommendation already dispatched or missing")
	}

	monitorID, err := optimizationUUIDv7(nowUTC)
	if err != nil {
		return OptimizationExecutionMonitorRecord{}, err
	}
	windowEndsAt := nowUTC.Add(DefaultExecutionMonitorWindow)
	if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.optimization_execution_monitors (
  id, tenant_id, site_id, lease_id, recommendation_id, command_intent_id,
  device_id, target_capability, baseline_supply_temp_c, applied_supply_temp_c,
  restore_supply_temp_c, zone_temp_max_c, supply_temp_max_c,
  started_at, window_ends_at, last_checked_at, status, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7::uuid, $8, $9, $10,
  $11, $12, $13,
  $14, $15, $14, 'OBSERVING', $14, $14
)`, monitorID, recommendation.TenantID, recommendation.SiteID, leaseID, recommendation.ID, commandIntentID,
		target.DeviceID, string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
		baselineSupplyC, appliedSupplyC, restoreSupplyC, zoneMaxC, supplyMaxC, nowUTC, windowEndsAt); err != nil {
		return OptimizationExecutionMonitorRecord{}, fmt.Errorf("insert optimization execution monitor: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return OptimizationExecutionMonitorRecord{}, err
	}
	return OptimizationExecutionMonitorRecord{
		ID:                  monitorID,
		TenantID:            recommendation.TenantID,
		SiteID:              recommendation.SiteID,
		LeaseID:             leaseID,
		RecommendationID:    recommendation.ID,
		CommandIntentID:     commandIntentID,
		DeviceID:            target.DeviceID,
		TargetCapability:    string(commandmodel.CapabilitySetChilledWaterTemperatureSetpoint),
		BaselineSupplyTempC: &baselineSupplyC,
		AppliedSupplyTempC:  &appliedSupplyC,
		RestoreSupplyTempC:  &restoreSupplyC,
		ZoneTempMaxC:        zoneMaxC,
		SupplyTempMaxC:      supplyMaxC,
		StartedAt:           nowUTC,
		WindowEndsAt:        windowEndsAt,
		LastCheckedAt:       &nowUTC,
		Status:              ExecutionMonitorObserving,
	}, nil
}

func (store *PostgresStore) ListObservingExecutionMonitors(ctx context.Context, limit int) ([]OptimizationExecutionMonitorRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.pool.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, lease_id::text, recommendation_id::text,
       command_intent_id::text, device_id::text, target_capability,
       baseline_supply_temp_c, applied_supply_temp_c, restore_supply_temp_c,
       zone_temp_max_c, supply_temp_max_c, started_at, window_ends_at,
       last_checked_at, telemetry_unhealthy_since, status, trip_reason,
       CASE WHEN rollback_command_intent_id IS NULL THEN NULL ELSE rollback_command_intent_id::text END
FROM core_registry.optimization_execution_monitors
WHERE status = 'OBSERVING'
ORDER BY window_ends_at ASC, started_at ASC, id ASC
LIMIT $1
`, limit)
	if err != nil {
		return nil, fmt.Errorf("list observing execution monitors: %w", err)
	}
	defer rows.Close()
	items := []OptimizationExecutionMonitorRecord{}
	for rows.Next() {
		var item OptimizationExecutionMonitorRecord
		if scanErr := rows.Scan(
			&item.ID, &item.TenantID, &item.SiteID, &item.LeaseID, &item.RecommendationID,
			&item.CommandIntentID, &item.DeviceID, &item.TargetCapability,
			&item.BaselineSupplyTempC, &item.AppliedSupplyTempC, &item.RestoreSupplyTempC,
			&item.ZoneTempMaxC, &item.SupplyTempMaxC, &item.StartedAt, &item.WindowEndsAt,
			&item.LastCheckedAt, &item.TelemetryUnhealthySince, &item.Status, &item.TripReason,
			&item.RollbackCommandIntentID,
		); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) RecordMonitorHealthyHeartbeat(ctx context.Context, monitorID, tenantID, siteID string, checkedAt time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET telemetry_unhealthy_since = NULL, last_checked_at = $4, updated_at = $4
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND status = 'OBSERVING'
`, tenantID, siteID, monitorID, checkedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) RecordMonitorTelemetryUnhealthy(ctx context.Context, monitorID, tenantID, siteID string, unhealthySince, checkedAt time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET telemetry_unhealthy_since = COALESCE(telemetry_unhealthy_since, $4),
    last_checked_at = $5,
    updated_at = $5
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND status = 'OBSERVING'
`, tenantID, siteID, monitorID, unhealthySince.UTC(), checkedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) CompleteMonitorVerified(ctx context.Context, monitorID, tenantID, siteID string, checkedAt time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = 'VERIFIED', telemetry_unhealthy_since = NULL, last_checked_at = $4, updated_at = $4
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND status = 'OBSERVING'
`, tenantID, siteID, monitorID, checkedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) TripMonitorAndLease(ctx context.Context, monitor OptimizationExecutionMonitorRecord, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error {
	nowUTC := trippedAt.UTC()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, monitor.TenantID, monitor.SiteID); err != nil {
		return err
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

	if _, err = tx.Exec(ctx, `
UPDATE core_registry.optimization_execution_monitors
SET status = $4, trip_reason = $5, rollback_command_intent_id = $6::uuid, last_checked_at = $7, updated_at = $7
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND status = 'OBSERVING'
`, monitor.TenantID, monitor.SiteID, monitor.ID, newStatus, tripReason, rollbackCommandIntentID, nowUTC); err != nil {
		return fmt.Errorf("update tripped execution monitor: %w", err)
	}

	eventID, err := optimizationUUIDv7(nowUTC)
	if err != nil {
		return err
	}
	payload := encodeOptimizationJSON(map[string]any{
		"siteId":                  monitor.SiteID,
		"leaseId":                 monitor.LeaseID,
		"monitorId":               monitor.ID,
		"recommendationId":        monitor.RecommendationID,
		"commandIntentId":         monitor.CommandIntentID,
		"status":                  newStatus,
		"tripReason":              tripReason,
		"rollbackCommandIntentId": rollbackCommandIntentID,
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

	if query.SupplyTemperatureKey == "" {
		result.SupplyErr = unavailable(ReasonSupplyTempMissing, "supplyTemperatureKey is missing")
	} else {
		supplyObs, supplyErr := reader.readTelemetry(ctx, query, query.SupplyTemperatureKey, ReasonSupplyTempMissing)
		if supplyErr != nil {
			result.SupplyErr = supplyErr
		} else if validErr := validateTelemetryEvidence(supplyObs, query.SupplyTemperatureKey, atUTC); validErr != nil {
			result.SupplyErr = validErr
		} else {
			result.SupplyTemp = &supplyObs
		}
	}

	zoneKeys := query.effectiveZoneKeys()
	if len(zoneKeys) == 0 {
		result.ZoneErr = unavailable(ReasonZoneTempMissing, "no zoneTemperatureKeys configured")
	} else {
		for _, zoneKey := range zoneKeys {
			obs, zoneErr := reader.readTelemetry(ctx, query, zoneKey, ReasonZoneTempMissing)
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
	stagingPolicy.MaxTelemetryAge = DefaultPreparationMaxTelemetryAge
	stagingSnap, snapErr := reader.ReadStagingSnapshot(ctx, StagingSnapshotQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		At:       atUTC,
		Policy:   stagingPolicy,
		Topology: &topology,
	})
	if snapErr != nil {
		result.ChillersHealthy = false
		return result, nil
	}
	for _, chiller := range stagingSnap.Chillers {
		state, _ := resolveChillerRunState(chiller, atUTC, stagingPolicy)
		running := state == "RUNNING"
		if !running && chiller.Power != nil && chiller.Power.Quality == "GOOD" &&
			atUTC.Sub(chiller.Power.SampledAt.UTC()) <= DefaultPreparationMaxTelemetryAge &&
			chiller.Power.Value > stagingPolicy.ChillerRunningPowerKW {
			running = true
		}
		if !running {
			continue
		}
		result.RunningChillers++
		if chiller.RatedPowerKW > 0 {
			if chiller.Power == nil || chiller.Power.Quality != "GOOD" || atUTC.Sub(chiller.Power.SampledAt.UTC()) > DefaultPreparationMaxTelemetryAge {
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
	return result, nil
}
