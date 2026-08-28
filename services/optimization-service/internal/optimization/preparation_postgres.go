package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (store *PostgresStore) ResolveOptimizationPreparation(ctx context.Context, request PreparationRequest, at time.Time) (PreparationDefinition, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PreparationDefinition{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return PreparationDefinition{}, err
	}

	var definition PreparationDefinition
	var policyCount int
	err = tx.QueryRow(ctx, `SELECT
 pv.id::text,pv.objective,pv.horizon,pv.horizon_minutes,pv.granularity,pv.constraints,count(*) OVER ()
FROM core_registry.optimization_policy_versions pv
JOIN core_registry.optimization_policies p
  ON p.tenant_id=pv.tenant_id AND p.id=pv.policy_id
WHERE pv.tenant_id=$1::uuid
  AND p.status='ACTIVE' AND p.resource_type='HVAC'
  AND pv.status='RELEASED'
  AND pv.effective_from <= $2
  AND (pv.effective_to IS NULL OR pv.effective_to > $2)
ORDER BY pv.effective_from DESC,pv.version DESC
LIMIT 1`, request.TenantID, at.UTC()).Scan(
		&definition.PolicyVersionID, &definition.Objective, &definition.Horizon, &definition.HorizonMinutes,
		&definition.Granularity, &definition.PolicyConstraints, &policyCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, fmt.Errorf("%w: no effective RELEASED Optimization policy", ErrPreparationUnavailable)
	}
	if err != nil {
		return PreparationDefinition{}, err
	}
	if policyCount != 1 {
		return PreparationDefinition{}, fmt.Errorf("%w: effective Optimization policy mapping is ambiguous", ErrPreparationUnavailable)
	}

	var topologyCount int
	err = tx.QueryRow(ctx, `SELECT id::text,count(*) OVER ()
FROM core_registry.energy_topology_versions
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status='ACTIVE'
  AND effective_from <= $3 AND (effective_to IS NULL OR effective_to > $3)
ORDER BY version DESC
LIMIT 1`, request.TenantID, request.SiteID, at.UTC()).Scan(&definition.TopologyVersionID, &topologyCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, fmt.Errorf("%w: no ACTIVE Topology Version for Site", ErrPreparationUnavailable)
	}
	if err != nil {
		return PreparationDefinition{}, err
	}
	if topologyCount != 1 {
		return PreparationDefinition{}, fmt.Errorf("%w: ACTIVE Topology Version mapping is ambiguous", ErrPreparationUnavailable)
	}

	forecastWindowStart := at.UTC().Add(15 * time.Minute)
	forecastWindowEnd := at.UTC().Add(time.Duration(definition.HorizonMinutes) * time.Minute)
	err = tx.QueryRow(ctx, `SELECT snapshot.id::text
FROM core_registry.forecast_snapshots snapshot
JOIN core_registry.forecast_jobs job
  ON job.tenant_id=snapshot.tenant_id AND job.site_id=snapshot.site_id AND job.id=snapshot.forecast_job_id
JOIN core_registry.forecast_input_snapshots input
  ON input.tenant_id=snapshot.tenant_id AND input.site_id=snapshot.site_id AND input.id=snapshot.input_snapshot_id
WHERE snapshot.tenant_id=$1::uuid AND snapshot.site_id=$2::uuid
  AND job.status='PERSISTED' AND job.target='SITE_LOAD'
  AND job.subject_type=$3 AND job.subject_id=$4::uuid
  AND input.topology_version_id=$8::uuid
  AND snapshot.forecast_origin <= $5
  AND snapshot.window_start <= $6
  AND snapshot.window_end >= $7
ORDER BY snapshot.forecast_origin DESC,snapshot.created_at DESC,snapshot.id DESC
LIMIT 1`, request.TenantID, request.SiteID, request.SubjectType, request.SubjectID, at.UTC(), forecastWindowStart, forecastWindowEnd, definition.TopologyVersionID).Scan(&definition.LoadForecastSnapshotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, fmt.Errorf("%w: no PERSISTED SITE_LOAD Forecast Snapshot covers the Optimization horizon", ErrPreparationUnavailable)
	}
	if err != nil {
		return PreparationDefinition{}, err
	}

	var pvSnapshotID string
	err = tx.QueryRow(ctx, `SELECT snapshot.id::text
FROM core_registry.forecast_snapshots snapshot
JOIN core_registry.forecast_jobs job
  ON job.tenant_id=snapshot.tenant_id AND job.site_id=snapshot.site_id AND job.id=snapshot.forecast_job_id
JOIN core_registry.forecast_input_snapshots input
  ON input.tenant_id=snapshot.tenant_id AND input.site_id=snapshot.site_id AND input.id=snapshot.input_snapshot_id
WHERE snapshot.tenant_id=$1::uuid AND snapshot.site_id=$2::uuid
  AND job.status='PERSISTED' AND job.target='PV_GENERATION'
  AND job.subject_type=$3 AND job.subject_id=$4::uuid
  AND input.topology_version_id=$8::uuid
  AND snapshot.forecast_origin <= $5
  AND snapshot.window_start <= $6
  AND snapshot.window_end >= $7
ORDER BY snapshot.forecast_origin DESC,snapshot.created_at DESC,snapshot.id DESC
LIMIT 1`, request.TenantID, request.SiteID, request.SubjectType, request.SubjectID, at.UTC(), forecastWindowStart, forecastWindowEnd, definition.TopologyVersionID).Scan(&pvSnapshotID)
	if err == nil {
		definition.PVForecastSnapshotID = &pvSnapshotID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, err
	}

	var tariffCount int
	err = tx.QueryRow(ctx, `SELECT tv.id::text,count(*) OVER ()
FROM core_registry.tariff_assignments ta
JOIN core_registry.settlement_boundaries boundary
  ON boundary.tenant_id=ta.tenant_id AND boundary.site_id=ta.site_id AND boundary.id=ta.boundary_id
JOIN core_registry.tariff_versions tv
  ON tv.tenant_id=ta.tenant_id AND tv.site_id=ta.site_id AND tv.tariff_id=ta.tariff_id
WHERE ta.tenant_id=$1::uuid AND ta.site_id=$2::uuid
  AND ta.status='RELEASED' AND ta.effective_from <= $3 AND (ta.effective_to IS NULL OR ta.effective_to >= $4)
  AND boundary.topology_version_id=$5::uuid
  AND boundary.status IN ('RELEASED','ACTIVE') AND boundary.effective_from <= $3 AND (boundary.effective_to IS NULL OR boundary.effective_to >= $4)
  AND boundary.direction='IMPORT' AND boundary.boundary_type IN ('GRID_CONNECTION','SITE')
  AND tv.status='RELEASED' AND tv.effective_from <= $3 AND (tv.effective_to IS NULL OR tv.effective_to >= $4)
ORDER BY CASE boundary.boundary_type WHEN 'GRID_CONNECTION' THEN 0 ELSE 1 END,ta.effective_from DESC,tv.version DESC
LIMIT 1`, request.TenantID, request.SiteID, at.UTC(), forecastWindowEnd, definition.TopologyVersionID).Scan(&definition.TariffVersionID, &tariffCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, fmt.Errorf("%w: no effective RELEASED Site import Tariff Version", ErrPreparationUnavailable)
	}
	if err != nil {
		return PreparationDefinition{}, err
	}
	if tariffCount != 1 {
		return PreparationDefinition{}, fmt.Errorf("%w: effective Site import Tariff mapping is ambiguous", ErrPreparationUnavailable)
	}

	err = tx.QueryRow(ctx, `SELECT binding.deployment_revision_id::text
FROM core_registry.ai_deployment_bindings binding
JOIN core_registry.ai_deployment_revisions revision
  ON revision.tenant_id=binding.tenant_id AND revision.id=binding.deployment_revision_id
WHERE binding.tenant_id=$1::uuid AND binding.site_id=$2::uuid
  AND binding.use_case='OPTIMIZATION' AND binding.status='ACTIVE'
  AND revision.use_case='OPTIMIZATION' AND revision.enabled=true`, request.TenantID, request.SiteID).Scan(&definition.DeploymentRevisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, fmt.Errorf("%w: no ACTIVE Optimization model deployment", ErrPreparationUnavailable)
	}
	if err != nil {
		return PreparationDefinition{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return PreparationDefinition{}, err
	}
	return definition, nil
}

func (store *PostgresStore) CreatePreparedOptimization(ctx context.Context, input PreparedInput, at time.Time) (PreparedOptimization, error) {
	inputSnapshotID, err := optimizationUUIDv7(at)
	if err != nil {
		return PreparedOptimization{}, err
	}
	optimizationRunID, err := optimizationUUIDv7(at)
	if err != nil {
		return PreparedOptimization{}, err
	}
	currentState, err := json.Marshal(input.CurrentState)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("encode Optimization current state: %w", err)
	}
	safetyConstraints, err := json.Marshal(input.SafetyConstraints)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("encode Optimization safety constraints: %w", err)
	}
	maintenanceConstraints, err := json.Marshal(input.MaintenanceConstraints)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("encode Optimization maintenance constraints: %w", err)
	}
	manualLocks, err := json.Marshal(input.ManualLocks)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("encode Optimization manual locks: %w", err)
	}
	schedulerPayload, err := json.Marshal(SchedulerOptimizationReference{OptimizationRunID: optimizationRunID, InputSnapshotID: inputSnapshotID})
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("encode Optimization scheduler reference: %w", err)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return PreparedOptimization{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, input.TenantID, input.SiteID); err != nil {
		return PreparedOptimization{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.optimization_input_snapshots(
 id,tenant_id,site_id,subject_type,subject_id,policy_version_id,topology_version_id,
 load_forecast_snapshot_id,pv_forecast_snapshot_id,tariff_version_id,current_state,safety_constraints,
 maintenance_constraints,manual_locks,captured_at,input_checksum,status,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7::uuid,$8::uuid,$9::uuid,$10::uuid,
 $11::jsonb,$12::jsonb,$13::jsonb,$14::jsonb,$15,NULL,'BUILDING',1,$15,$15)`,
		inputSnapshotID, input.TenantID, input.SiteID, input.SubjectType, input.SubjectID, input.PolicyVersionID, input.TopologyVersionID,
		input.LoadForecastSnapshotID, input.PVForecastSnapshotID, input.TariffVersionID, currentState, safetyConstraints, maintenanceConstraints, manualLocks, input.CapturedAt.UTC()); err != nil {
		return PreparedOptimization{}, fmt.Errorf("insert BUILDING Optimization input snapshot: %w", err)
	}
	sealTag, err := tx.Exec(ctx, `UPDATE core_registry.optimization_input_snapshots
SET input_checksum=$4,status='SEALED',revision=revision+1,updated_at=$5
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='BUILDING'`,
		input.TenantID, input.SiteID, inputSnapshotID, input.InputChecksum, at.UTC())
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("seal Optimization input snapshot: %w", err)
	}
	if sealTag.RowsAffected() != 1 {
		return PreparedOptimization{}, errors.New("Optimization input snapshot sealing was rejected")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.optimization_runs(
 id,tenant_id,site_id,subject_type,subject_id,policy_version_id,input_snapshot_id,objective,horizon,horizon_minutes,granularity,
 solver,solver_version,status,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7::uuid,$8,$9,$10,$11,
 'hvac-recommendation','v1','CREATED',1,$12,$12)`,
		optimizationRunID, input.TenantID, input.SiteID, input.SubjectType, input.SubjectID, input.PolicyVersionID, inputSnapshotID,
		input.Objective, input.Horizon, input.HorizonMinutes, input.Granularity, at.UTC()); err != nil {
		return PreparedOptimization{}, fmt.Errorf("insert Optimization run: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.job_instances(
 job_id,trigger_type,job_type,tenant_id,site_id,subject_type,subject_id,scheduled_for,priority,dedup_key,payload,state,max_attempts,timeout_seconds,created_at,updated_at)
VALUES($1::uuid,'MANUAL','OPTIMIZATION_RUN',$2::uuid,$3::uuid,$4,$5,$6,50,$7,$8::jsonb,'READY',3,120,$6,$6)`,
		optimizationRunID, input.TenantID, input.SiteID, input.SubjectType, input.SubjectID, at.UTC(), "optimization:"+optimizationRunID, schedulerPayload); err != nil {
		return PreparedOptimization{}, fmt.Errorf("insert Optimization scheduler job: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return PreparedOptimization{}, err
	}
	return PreparedOptimization{OptimizationRunID: optimizationRunID, InputSnapshotID: inputSnapshotID, Status: "PENDING"}, nil
}

func (store *PostgresStore) LoadOptimizationRequest(ctx context.Context, tenantID, siteID string, reference SchedulerOptimizationReference) (Request, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return Request{}, err
	}
	var input PreparedInput
	var currentState, safetyConstraints, maintenanceConstraints, manualLocks []byte
	err = tx.QueryRow(ctx, `SELECT
 run.subject_type,run.subject_id::text,run.policy_version_id::text,snapshot.topology_version_id::text,
 snapshot.load_forecast_snapshot_id::text,CASE WHEN snapshot.pv_forecast_snapshot_id IS NULL THEN NULL ELSE snapshot.pv_forecast_snapshot_id::text END,
 snapshot.tariff_version_id::text,run.objective,run.horizon,run.horizon_minutes,run.granularity,
 snapshot.captured_at,snapshot.current_state,snapshot.safety_constraints,snapshot.maintenance_constraints,snapshot.manual_locks,snapshot.input_checksum
FROM core_registry.optimization_runs run
JOIN core_registry.optimization_input_snapshots snapshot
  ON snapshot.tenant_id=run.tenant_id AND snapshot.site_id=run.site_id AND snapshot.id=run.input_snapshot_id AND snapshot.policy_version_id=run.policy_version_id
WHERE run.tenant_id=$1::uuid AND run.site_id=$2::uuid AND run.id=$3::uuid
  AND run.input_snapshot_id=$4::uuid AND snapshot.status='SEALED'`, tenantID, siteID, reference.OptimizationRunID, reference.InputSnapshotID).Scan(
		&input.SubjectType, &input.SubjectID, &input.PolicyVersionID, &input.TopologyVersionID,
		&input.LoadForecastSnapshotID, &input.PVForecastSnapshotID, &input.TariffVersionID,
		&input.Objective, &input.Horizon, &input.HorizonMinutes, &input.Granularity, &input.CapturedAt,
		&currentState, &safetyConstraints, &maintenanceConstraints, &manualLocks, &input.InputChecksum,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, errors.New("server-created SEALED Optimization input was not found")
	}
	if err != nil {
		return Request{}, err
	}
	if err = json.Unmarshal(currentState, &input.CurrentState); err != nil {
		return Request{}, fmt.Errorf("decode frozen Optimization current state: %w", err)
	}
	if err = json.Unmarshal(safetyConstraints, &input.SafetyConstraints); err != nil {
		return Request{}, fmt.Errorf("decode frozen Optimization safety constraints: %w", err)
	}
	if err = json.Unmarshal(maintenanceConstraints, &input.MaintenanceConstraints); err != nil {
		return Request{}, fmt.Errorf("decode frozen Optimization maintenance constraints: %w", err)
	}
	if err = json.Unmarshal(manualLocks, &input.ManualLocks); err != nil {
		return Request{}, fmt.Errorf("decode frozen Optimization manual locks: %w", err)
	}
	input.TenantID, input.SiteID = tenantID, siteID
	input.DeploymentRevisionID = input.SafetyConstraints.ModelDeploymentRevisionID
	if !uuidPattern.MatchString(input.DeploymentRevisionID) {
		return Request{}, errors.New("frozen Optimization model deployment provenance is invalid")
	}
	recomputed, checksumErr := checksumPreparedOptimization(input)
	if checksumErr != nil {
		return Request{}, checksumErr
	}
	if recomputed != input.InputChecksum {
		return Request{}, errors.New("frozen Optimization input checksum does not match persisted provenance")
	}
	request, err := executionRequestFromPrepared(input, reference.OptimizationRunID, reference.InputSnapshotID)
	if err != nil {
		return Request{}, fmt.Errorf("load frozen Optimization execution input: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	return request, nil
}
