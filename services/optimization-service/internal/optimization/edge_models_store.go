package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func (store *PostgresStore) LoadEdgeModelSiteSnapshot(ctx context.Context, tenantID, siteID string) (EdgeModelSiteSnapshot, error) {
	if store == nil || store.pool == nil {
		return EdgeModelSiteSnapshot{}, errors.New("optimization postgres store is unavailable")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return EdgeModelSiteSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return EdgeModelSiteSnapshot{}, err
	}
	snap, err := loadEdgeModelSiteSnapshotInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EdgeModelSiteSnapshot{}, err
	}
	return snap, nil
}

func loadEdgeModelSiteSnapshotInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) (EdgeModelSiteSnapshot, error) {
	out := EdgeModelSiteSnapshot{}

	artRows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, owner_domain, owner_version_id::text,
       object_uri, checksum_sha256, media_type, COALESCE(size_bytes, 0), metadata, created_at
FROM core_registry.mlops_artifacts
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND owner_domain IN ('EDGE_CONTROL', 'EDGE_FDD')
  AND artifact_type = 'MODEL'
ORDER BY created_at DESC`, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, fmt.Errorf("query mlops_artifacts: %w", err)
	}
	for artRows.Next() {
		var rec EdgeModelArtifactRecord
		var dom string
		var rawMeta []byte
		if scanErr := artRows.Scan(
			&rec.ID, &rec.TenantID, &rec.SiteID, &dom, &rec.VersionID,
			&rec.ObjectURI, &rec.ChecksumSHA256, &rec.MediaType, &rec.SizeBytes, &rawMeta, &rec.CreatedAt,
		); scanErr != nil {
			artRows.Close()
			return EdgeModelSiteSnapshot{}, scanErr
		}
		rec.Domain = edgecontrol.EdgeModelDomain(dom)
		rec.CreatedAt = rec.CreatedAt.UTC()
		if len(rawMeta) > 0 {
			if unmarshalErr := json.Unmarshal(rawMeta, &rec.Metadata); unmarshalErr != nil {
				artRows.Close()
				return EdgeModelSiteSnapshot{}, fmt.Errorf("decode mlops_artifacts metadata: %w", unmarshalErr)
			}
		}
		out.Artifacts = append(out.Artifacts, rec)
	}
	if err := artRows.Err(); err != nil {
		artRows.Close()
		return EdgeModelSiteSnapshot{}, err
	}
	artRows.Close()

	apRows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, owner_domain, owner_version_id::text,
       evaluation_id::text, decision, actor_type, actor_id, reason, policy_revision, decided_at, created_at
FROM core_registry.mlops_approvals
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND owner_domain IN ('EDGE_CONTROL', 'EDGE_FDD')
ORDER BY decided_at DESC, created_at DESC`, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, fmt.Errorf("query mlops_approvals: %w", err)
	}
	for apRows.Next() {
		var rec EdgeModelApprovalRecord
		var dom string
		if scanErr := apRows.Scan(
			&rec.ID, &rec.TenantID, &rec.SiteID, &dom, &rec.VersionID,
			&rec.EvaluationID, &rec.Decision, &rec.ActorType, &rec.ActorID,
			&rec.Reason, &rec.PolicyRevision, &rec.DecidedAt, &rec.CreatedAt,
		); scanErr != nil {
			apRows.Close()
			return EdgeModelSiteSnapshot{}, scanErr
		}
		rec.Domain = edgecontrol.EdgeModelDomain(dom)
		rec.DecidedAt = rec.DecidedAt.UTC()
		rec.CreatedAt = rec.CreatedAt.UTC()
		out.Approvals = append(out.Approvals, rec)
	}
	if err := apRows.Err(); err != nil {
		apRows.Close()
		return EdgeModelSiteSnapshot{}, err
	}
	apRows.Close()

	depRows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, owner_domain, owner_version_id::text, approval_id::text,
       environment, status, serving_ref, effective_from, effective_to, revision, created_at, updated_at
FROM core_registry.mlops_deployment_metadata
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND owner_domain IN ('EDGE_CONTROL', 'EDGE_FDD')
ORDER BY revision DESC, effective_from DESC`, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, fmt.Errorf("query mlops_deployment_metadata: %w", err)
	}
	for depRows.Next() {
		var rec EdgeModelDeploymentRecord
		var dom string
		if scanErr := depRows.Scan(
			&rec.ID, &rec.TenantID, &rec.SiteID, &dom, &rec.VersionID, &rec.ApprovalID,
			&rec.Environment, &rec.Status, &rec.ServingRef, &rec.EffectiveFrom, &rec.EffectiveTo,
			&rec.Revision, &rec.CreatedAt, &rec.UpdatedAt,
		); scanErr != nil {
			depRows.Close()
			return EdgeModelSiteSnapshot{}, scanErr
		}
		rec.Domain = edgecontrol.EdgeModelDomain(dom)
		rec.EffectiveFrom = rec.EffectiveFrom.UTC()
		rec.CreatedAt = rec.CreatedAt.UTC()
		rec.UpdatedAt = rec.UpdatedAt.UTC()
		out.Deployments = append(out.Deployments, rec)
	}
	if err := depRows.Err(); err != nil {
		depRows.Close()
		return EdgeModelSiteSnapshot{}, err
	}
	depRows.Close()

	evRows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, owner_domain, owner_version_id::text,
       evaluation_type, dataset_artifact_id::text, metrics, outcome, evaluator, evaluated_at, created_at
FROM core_registry.mlops_evaluations
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND owner_domain IN ('EDGE_CONTROL', 'EDGE_FDD')
ORDER BY evaluated_at DESC, created_at DESC`, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, fmt.Errorf("query mlops_evaluations: %w", err)
	}
	for evRows.Next() {
		var rec EdgeModelEvaluationRecord
		var dom string
		var rawMetrics []byte
		if scanErr := evRows.Scan(
			&rec.ID, &rec.TenantID, &rec.SiteID, &dom, &rec.VersionID,
			&rec.EvaluationType, &rec.DatasetArtifactID, &rawMetrics, &rec.Outcome,
			&rec.Evaluator, &rec.EvaluatedAt, &rec.CreatedAt,
		); scanErr != nil {
			evRows.Close()
			return EdgeModelSiteSnapshot{}, scanErr
		}
		rec.Domain = edgecontrol.EdgeModelDomain(dom)
		rec.EvaluatedAt = rec.EvaluatedAt.UTC()
		rec.CreatedAt = rec.CreatedAt.UTC()
		if len(rawMetrics) > 0 {
			if unmarshalErr := json.Unmarshal(rawMetrics, &rec.Metrics); unmarshalErr != nil {
				evRows.Close()
				return EdgeModelSiteSnapshot{}, fmt.Errorf("decode mlops_evaluations metrics: %w", unmarshalErr)
			}
		}
		out.Evaluations = append(out.Evaluations, rec)
	}
	if err := evRows.Err(); err != nil {
		evRows.Close()
		return EdgeModelSiteSnapshot{}, err
	}
	evRows.Close()

	drRows, err := tx.Query(ctx, `
SELECT o.id::text, o.tenant_id::text, o.site_id::text, o.deployment_id::text, o.metric_code,
       o.observed_value, o.threshold_value, o.drift_status, o.evidence, o.observed_at, o.created_at
FROM core_registry.mlops_drift_observations o
JOIN core_registry.mlops_deployment_metadata d ON d.tenant_id = o.tenant_id AND d.id = o.deployment_id
WHERE o.tenant_id = $1::uuid AND o.site_id = $2::uuid
  AND d.owner_domain IN ('EDGE_CONTROL', 'EDGE_FDD')
ORDER BY o.observed_at DESC`, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, fmt.Errorf("query mlops_drift_observations: %w", err)
	}
	for drRows.Next() {
		var rec EdgeModelDriftRecord
		var rawEv []byte
		if scanErr := drRows.Scan(
			&rec.ID, &rec.TenantID, &rec.SiteID, &rec.DeploymentID, &rec.MetricCode,
			&rec.ObservedValue, &rec.Threshold, &rec.DriftStatus, &rawEv, &rec.ObservedAt, &rec.CreatedAt,
		); scanErr != nil {
			drRows.Close()
			return EdgeModelSiteSnapshot{}, scanErr
		}
		rec.ObservedAt = rec.ObservedAt.UTC()
		rec.CreatedAt = rec.CreatedAt.UTC()
		if len(rawEv) > 0 {
			_ = json.Unmarshal(rawEv, &rec.Evidence)
		}
		out.DriftObservations = append(out.DriftObservations, rec)
	}
	if err := drRows.Err(); err != nil {
		drRows.Close()
		return EdgeModelSiteSnapshot{}, err
	}
	drRows.Close()

	rbRows, err := tx.Query(ctx, `
SELECT r.id::text, r.tenant_id::text, r.site_id::text, r.deployment_id::text, r.from_owner_version_id::text,
       r.to_owner_version_id::text, r.reason, r.actor_type, r.actor_id, r.status, r.requested_at, r.applied_at, r.created_at
FROM core_registry.mlops_rollback_records r
JOIN core_registry.mlops_deployment_metadata d ON d.tenant_id = r.tenant_id AND d.id = r.deployment_id
WHERE r.tenant_id = $1::uuid AND r.site_id = $2::uuid
  AND d.owner_domain IN ('EDGE_CONTROL', 'EDGE_FDD')
ORDER BY r.requested_at DESC`, tenantID, siteID)
	if err != nil {
		return EdgeModelSiteSnapshot{}, fmt.Errorf("query mlops_rollback_records: %w", err)
	}
	for rbRows.Next() {
		var rec EdgeModelRollbackRecord
		if scanErr := rbRows.Scan(
			&rec.ID, &rec.TenantID, &rec.SiteID, &rec.DeploymentID, &rec.FromVersionID,
			&rec.ToVersionID, &rec.Reason, &rec.ActorType, &rec.ActorID,
			&rec.Status, &rec.RequestedAt, &rec.AppliedAt, &rec.CreatedAt,
		); scanErr != nil {
			rbRows.Close()
			return EdgeModelSiteSnapshot{}, scanErr
		}
		rec.RequestedAt = rec.RequestedAt.UTC()
		rec.CreatedAt = rec.CreatedAt.UTC()
		out.Rollbacks = append(out.Rollbacks, rec)
	}
	if err := rbRows.Err(); err != nil {
		rbRows.Close()
		return EdgeModelSiteSnapshot{}, err
	}
	rbRows.Close()

	return out, nil
}

func (store *PostgresStore) InsertEdgeModelArtifact(ctx context.Context, record EdgeModelArtifactRecord) (EdgeModelArtifactRecord, error) {
	if store == nil || store.pool == nil {
		return EdgeModelArtifactRecord{}, errors.New("optimization postgres store is unavailable")
	}
	rawMeta, err := json.Marshal(record.Metadata)
	if err != nil {
		return EdgeModelArtifactRecord{}, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeModelArtifactRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, record.TenantID, record.SiteID); err != nil {
		return EdgeModelArtifactRecord{}, err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_artifacts (
  id, tenant_id, site_id, owner_domain, artifact_type, owner_version_id,
  object_uri, checksum_sha256, media_type, size_bytes, metadata, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, 'MODEL', $5::uuid,
  $6, $7, $8, $9, $10::jsonb, $11
)`, record.ID, record.TenantID, record.SiteID, string(record.Domain), record.VersionID,
		record.ObjectURI, record.ChecksumSHA256, record.MediaType, record.SizeBytes, string(rawMeta), record.CreatedAt.UTC())
	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			return EdgeModelArtifactRecord{}, ErrEdgeModelConflict
		}
		return EdgeModelArtifactRecord{}, fmt.Errorf("insert mlops_artifacts: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return EdgeModelArtifactRecord{}, err
	}
	return record, nil
}

func (store *PostgresStore) InsertEdgeModelApproval(ctx context.Context, record EdgeModelApprovalRecord) (EdgeModelApprovalRecord, error) {
	if store == nil || store.pool == nil {
		return EdgeModelApprovalRecord{}, errors.New("optimization postgres store is unavailable")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeModelApprovalRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, record.TenantID, record.SiteID); err != nil {
		return EdgeModelApprovalRecord{}, err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_approvals (
  id, tenant_id, site_id, owner_domain, owner_version_id, evaluation_id,
  decision, actor_type, actor_id, reason, policy_revision, decided_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::uuid,
  $7, $8, $9, $10, $11, $12, $13
)`, record.ID, record.TenantID, record.SiteID, string(record.Domain), record.VersionID, record.EvaluationID,
		record.Decision, record.ActorType, record.ActorID, record.Reason, record.PolicyRevision, record.DecidedAt.UTC(), record.CreatedAt.UTC())
	if err != nil {
		return EdgeModelApprovalRecord{}, fmt.Errorf("insert mlops_approvals: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return EdgeModelApprovalRecord{}, err
	}
	return record, nil
}

func (store *PostgresStore) DeployEdgeModelVersion(ctx context.Context, tenantID, siteID string, artifact EdgeModelArtifactRecord, approvalID, environment, servingRef string, at time.Time) (EdgeModelDeploymentRecord, []EdgeModelDeploymentRecord, error) {
	if store == nil || store.pool == nil {
		return EdgeModelDeploymentRecord{}, nil, errors.New("optimization postgres store is unavailable")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeModelDeploymentRecord{}, nil, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return EdgeModelDeploymentRecord{}, nil, err
	}
	now := at.UTC()
	snap, err := loadEdgeModelSiteSnapshotInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeModelDeploymentRecord{}, nil, err
	}
	byVersion := make(map[string]EdgeModelArtifactRecord, len(snap.Artifacts))
	for _, a := range snap.Artifacts {
		byVersion[a.VersionID] = a
	}
	var maxRev int64
	for _, dep := range snap.Deployments {
		if dep.Revision > maxRev {
			maxRev = dep.Revision
		}
		if dep.Status == "ACTIVE" && dep.Environment == environment && dep.Domain == artifact.Domain && byVersion[dep.VersionID].Metadata.ModelKey == artifact.Metadata.ModelKey {
			effTo := now
			if !effTo.After(dep.EffectiveFrom) {
				effTo = dep.EffectiveFrom.Add(time.Millisecond)
			}
			_, err = tx.Exec(ctx, `
UPDATE core_registry.mlops_deployment_metadata
SET status = 'RETIRED', effective_to = $3, updated_at = GREATEST($3, created_at)
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, dep.ID, effTo)
			if err != nil {
				return EdgeModelDeploymentRecord{}, nil, fmt.Errorf("retire prior edge deployment: %w", err)
			}
		}
	}
	depID := deterministicV7("edge_model_deploy", tenantID+"|"+artifact.VersionID+"|"+environment, now)
	newRec := EdgeModelDeploymentRecord{
		ID:            depID,
		TenantID:      tenantID,
		SiteID:        siteID,
		Domain:        artifact.Domain,
		VersionID:     artifact.VersionID,
		ApprovalID:    approvalID,
		Environment:   environment,
		Status:        "ACTIVE",
		ServingRef:    servingRef,
		EffectiveFrom: now,
		Revision:      maxRev + 1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_deployment_metadata (
  id, tenant_id, site_id, owner_domain, owner_version_id, approval_id,
  environment, status, serving_ref, effective_from, effective_to, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::uuid,
  $7, 'ACTIVE', $8, $9, NULL, $10, $9, $9
)`, newRec.ID, tenantID, siteID, string(newRec.Domain), newRec.VersionID, newRec.ApprovalID,
		newRec.Environment, newRec.ServingRef, newRec.EffectiveFrom, newRec.Revision)
	if err != nil {
		return EdgeModelDeploymentRecord{}, nil, fmt.Errorf("insert mlops_deployment_metadata: %w", err)
	}
	updatedSnap, err := loadEdgeModelSiteSnapshotInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeModelDeploymentRecord{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EdgeModelDeploymentRecord{}, nil, err
	}
	return newRec, updatedSnap.Deployments, nil
}

func (store *PostgresStore) RollbackEdgeModelDeployment(ctx context.Context, tenantID, siteID, deploymentID string, targetArtifact *EdgeModelArtifactRecord, targetApprovalID, reason, actorType, actorID, servingRef string, at time.Time) (EdgeModelRollbackRecord, *EdgeModelDeploymentRecord, []EdgeModelDeploymentRecord, error) {
	if store == nil || store.pool == nil {
		return EdgeModelRollbackRecord{}, nil, nil, errors.New("optimization postgres store is unavailable")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, err
	}
	now := at.UTC()
	snap, err := loadEdgeModelSiteSnapshotInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, err
	}
	var current *EdgeModelDeploymentRecord
	var maxRev int64
	for i := range snap.Deployments {
		if snap.Deployments[i].Revision > maxRev {
			maxRev = snap.Deployments[i].Revision
		}
		if snap.Deployments[i].ID == deploymentID {
			current = &snap.Deployments[i]
		}
	}
	if current == nil {
		return EdgeModelRollbackRecord{}, nil, nil, ErrEdgeModelNotFound
	}
	if current.Status != "ACTIVE" {
		return EdgeModelRollbackRecord{}, nil, nil, ErrEdgeModelRolloutConflict
	}
	effTo := now
	if !effTo.After(current.EffectiveFrom) {
		effTo = current.EffectiveFrom.Add(time.Millisecond)
	}
	_, err = tx.Exec(ctx, `
UPDATE core_registry.mlops_deployment_metadata
SET status = 'ROLLED_BACK', effective_to = $3, updated_at = GREATEST($3, created_at)
WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, current.ID, effTo)
	if err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, fmt.Errorf("rollback active deployment: %w", err)
	}

	var restored *EdgeModelDeploymentRecord
	toVersionID := current.VersionID
	if targetArtifact != nil {
		toVersionID = targetArtifact.VersionID
		restoredID := deterministicV7("edge_model_restore", tenantID+"|"+targetArtifact.VersionID+"|"+current.Environment, now)
		rec := EdgeModelDeploymentRecord{
			ID:            restoredID,
			TenantID:      tenantID,
			SiteID:        siteID,
			Domain:        targetArtifact.Domain,
			VersionID:     targetArtifact.VersionID,
			ApprovalID:    targetApprovalID,
			Environment:   current.Environment,
			Status:        "ACTIVE",
			ServingRef:    servingRef,
			EffectiveFrom: now,
			Revision:      maxRev + 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_deployment_metadata (
  id, tenant_id, site_id, owner_domain, owner_version_id, approval_id,
  environment, status, serving_ref, effective_from, effective_to, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::uuid,
  $7, 'ACTIVE', $8, $9, NULL, $10, $9, $9
)`, rec.ID, tenantID, siteID, string(rec.Domain), rec.VersionID, rec.ApprovalID,
			rec.Environment, rec.ServingRef, rec.EffectiveFrom, rec.Revision)
		if err != nil {
			return EdgeModelRollbackRecord{}, nil, nil, fmt.Errorf("insert restored deployment: %w", err)
		}
		restored = &rec
	}

	appliedAt := now
	rbRec := EdgeModelRollbackRecord{
		ID:            deterministicV7("edge_model_rollback", tenantID+"|"+current.ID, now),
		TenantID:      tenantID,
		SiteID:        siteID,
		DeploymentID:  current.ID,
		FromVersionID: current.VersionID,
		ToVersionID:   toVersionID,
		Reason:        reason,
		ActorType:     actorType,
		ActorID:       actorID,
		Status:        "APPLIED",
		RequestedAt:   now,
		AppliedAt:     &appliedAt,
		CreatedAt:     now,
	}
	_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_rollback_records (
  id, tenant_id, site_id, deployment_id, from_owner_version_id, to_owner_version_id,
  reason, actor_type, actor_id, status, requested_at, applied_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7, $8, $9, 'APPLIED', $10, $11, $12
)`, rbRec.ID, tenantID, siteID, rbRec.DeploymentID, rbRec.FromVersionID, rbRec.ToVersionID,
		rbRec.Reason, rbRec.ActorType, rbRec.ActorID, rbRec.RequestedAt, appliedAt, rbRec.CreatedAt)
	if err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, fmt.Errorf("insert mlops_rollback_records: %w", err)
	}
	updatedSnap, err := loadEdgeModelSiteSnapshotInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EdgeModelRollbackRecord{}, nil, nil, err
	}
	return rbRec, restored, updatedSnap.Deployments, nil
}

func (store *PostgresStore) InsertEdgeModelEvaluation(ctx context.Context, eval EdgeModelEvaluationRecord, drift *EdgeModelDriftRecord) error {
	if store == nil || store.pool == nil {
		return errors.New("optimization postgres store is unavailable")
	}
	rawMetrics, err := json.Marshal(eval.Metrics)
	if err != nil {
		return err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, eval.TenantID, eval.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_evaluations (
  id, tenant_id, site_id, owner_domain, owner_version_id, evaluation_type,
  dataset_artifact_id, metrics, outcome, evaluator, evaluated_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6,
  $7::uuid, $8::jsonb, $9, $10, $11, $12
)`, eval.ID, eval.TenantID, eval.SiteID, string(eval.Domain), eval.VersionID, eval.EvaluationType,
		eval.DatasetArtifactID, string(rawMetrics), eval.Outcome, eval.Evaluator, eval.EvaluatedAt.UTC(), eval.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("insert mlops_evaluations: %w", err)
	}
	if drift != nil {
		rawEv, err := json.Marshal(drift.Evidence)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO core_registry.mlops_drift_observations (
  id, tenant_id, site_id, deployment_id, metric_code, observed_value,
  threshold_value, drift_status, evidence, observed_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6,
  $7, $8, $9::jsonb, $10, $11
)`, drift.ID, drift.TenantID, drift.SiteID, drift.DeploymentID, drift.MetricCode, drift.ObservedValue,
			drift.Threshold, drift.DriftStatus, string(rawEv), drift.ObservedAt.UTC(), drift.CreatedAt.UTC())
		if err != nil {
			return fmt.Errorf("insert mlops_drift_observations: %w", err)
		}
	}
	return tx.Commit(ctx)
}
