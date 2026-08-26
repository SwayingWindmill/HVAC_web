package metric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const LifecycleMetricResultDataset = "metric_result_facts"

var ErrArchiveEvidenceRequired = errors.New("verified Archive Manifest is required before source deletion")

type LifecyclePayload struct {
	DatasetCode string `json:"datasetCode"`
	DataClass   string `json:"dataClass"`
	ResourceKey string `json:"resourceKey"`
}

type LifecyclePolicy struct {
	ID              string
	DeleteAfterDays *int
	ArchiveRequired bool
}

type LifecycleTarget struct {
	ResultID     string
	Revision     uint64
	CalculatedAt time.Time
	IsCurrent    bool
}

type LifecycleDeletion struct {
	ID     string
	Status string
}

type LifecycleOutcome struct {
	Status            string `json:"status"`
	ResourceKey       string `json:"resourceKey"`
	DeletionRequestID string `json:"deletionRequestId,omitempty"`
	TombstoneID       string `json:"tombstoneId,omitempty"`
}

type LifecycleGovernance interface {
	LoadLifecycleTarget(context.Context, SchedulerJob, LifecyclePayload) (LifecycleTarget, error)
	LoadLifecyclePolicy(context.Context, SchedulerJob, LifecyclePayload, time.Time) (LifecyclePolicy, error)
	LegalHoldBlocks(context.Context, SchedulerJob, LifecyclePayload, time.Time) (bool, error)
	VerifiedArchiveManifest(context.Context, SchedulerJob, LifecyclePayload) (string, error)
	EnsureRetentionDeletion(context.Context, SchedulerJob, LifecyclePayload, LifecycleTarget, LifecyclePolicy, string, time.Time) (LifecycleDeletion, error)
	ApplyRetentionDeletion(context.Context, SchedulerJob, LifecyclePayload, LifecycleTarget, LifecycleDeletion, time.Time) (string, error)
	RecordLifecycleEvent(context.Context, SchedulerJob, LifecyclePayload, string, map[string]any, time.Time) error
}

type LifecycleDataStore interface {
	DeleteMetricResult(context.Context, string, string, string) error
}

type LifecycleExecutor struct {
	governance LifecycleGovernance
	data       LifecycleDataStore
}

func NewLifecycleExecutor(governance LifecycleGovernance, data LifecycleDataStore) (*LifecycleExecutor, error) {
	if governance == nil || data == nil {
		return nil, errors.New("lifecycle governance and data stores are required")
	}
	return &LifecycleExecutor{governance: governance, data: data}, nil
}

func (executor *LifecycleExecutor) Execute(ctx context.Context, job SchedulerJob, payload LifecyclePayload, now time.Time) (LifecycleOutcome, error) {
	payload.DatasetCode = strings.TrimSpace(payload.DatasetCode)
	payload.DataClass = strings.TrimSpace(payload.DataClass)
	payload.ResourceKey = strings.TrimSpace(payload.ResourceKey)
	if payload.DatasetCode != LifecycleMetricResultDataset || payload.DataClass == "" || payload.ResourceKey == "" {
		return LifecycleOutcome{}, errors.New("lifecycle payload is invalid")
	}
	if job.JobType != "DATA_RETENTION_SCAN" && job.JobType != "DATA_ARCHIVE" {
		return LifecycleOutcome{}, errors.New("lifecycle job type is invalid")
	}
	now = now.UTC()
	target, err := executor.governance.LoadLifecycleTarget(ctx, job, payload)
	if err != nil {
		return LifecycleOutcome{}, err
	}
	policy, err := executor.governance.LoadLifecyclePolicy(ctx, job, payload, now)
	if err != nil {
		return LifecycleOutcome{}, err
	}
	if policy.DeleteAfterDays == nil || target.CalculatedAt.AddDate(0, 0, *policy.DeleteAfterDays).After(now) {
		return LifecycleOutcome{Status: "NOT_DUE", ResourceKey: payload.ResourceKey}, nil
	}
	if err = executor.governance.RecordLifecycleEvent(ctx, job, payload, "CLAIMED", map[string]any{"policyId": policy.ID, "sourceRevision": target.Revision}, now); err != nil {
		return LifecycleOutcome{}, err
	}
	blocked, err := executor.governance.LegalHoldBlocks(ctx, job, payload, now)
	if err != nil {
		return LifecycleOutcome{}, err
	}
	if blocked {
		if err = executor.governance.RecordLifecycleEvent(ctx, job, payload, "HOLD_BLOCKED", map[string]any{"policyId": policy.ID}, now); err != nil {
			return LifecycleOutcome{}, err
		}
		return LifecycleOutcome{Status: "HOLD_BLOCKED", ResourceKey: payload.ResourceKey}, nil
	}
	if target.IsCurrent {
		if err = executor.governance.RecordLifecycleEvent(ctx, job, payload, "CURRENT_BLOCKED", map[string]any{"policyId": policy.ID, "sourceRevision": target.Revision}, now); err != nil {
			return LifecycleOutcome{}, err
		}
		return LifecycleOutcome{Status: "CURRENT_BLOCKED", ResourceKey: payload.ResourceKey}, nil
	}
	archiveManifestID := ""
	if policy.ArchiveRequired {
		archiveManifestID, err = executor.governance.VerifiedArchiveManifest(ctx, job, payload)
		if err != nil {
			_ = executor.governance.RecordLifecycleEvent(ctx, job, payload, "ARCHIVE_FAILED", map[string]any{"policyId": policy.ID, "reason": "VERIFIED_ARCHIVE_UNAVAILABLE"}, now)
			if errors.Is(err, pgx.ErrNoRows) {
				return LifecycleOutcome{}, ErrArchiveEvidenceRequired
			}
			return LifecycleOutcome{}, err
		}
		if err = executor.governance.RecordLifecycleEvent(ctx, job, payload, "ARCHIVE_VERIFIED", map[string]any{"archiveManifestId": archiveManifestID}, now); err != nil {
			return LifecycleOutcome{}, err
		}
	}
	deletion, err := executor.governance.EnsureRetentionDeletion(ctx, job, payload, target, policy, archiveManifestID, now)
	if err != nil {
		return LifecycleOutcome{}, err
	}
	if deletion.Status == "APPLIED" {
		return LifecycleOutcome{Status: "ALREADY_APPLIED", ResourceKey: payload.ResourceKey, DeletionRequestID: deletion.ID}, nil
	}
	if err = executor.governance.RecordLifecycleEvent(ctx, job, payload, "DELETE_STARTED", map[string]any{"deletionRequestId": deletion.ID}, now); err != nil {
		return LifecycleOutcome{}, err
	}
	if err = executor.data.DeleteMetricResult(ctx, job.TenantID, job.SiteID, target.ResultID); err != nil {
		_ = executor.governance.RecordLifecycleEvent(ctx, job, payload, "FAILED", map[string]any{"deletionRequestId": deletion.ID, "reason": "SOURCE_DELETE_FAILED"}, now)
		return LifecycleOutcome{}, fmt.Errorf("delete Metric Result source: %w", err)
	}
	tombstoneID, err := executor.governance.ApplyRetentionDeletion(ctx, job, payload, target, deletion, now)
	if err != nil {
		return LifecycleOutcome{}, err
	}
	if err = executor.governance.RecordLifecycleEvent(ctx, job, payload, "DELETE_APPLIED", map[string]any{"deletionRequestId": deletion.ID, "tombstoneId": tombstoneID}, now); err != nil {
		return LifecycleOutcome{}, err
	}
	return LifecycleOutcome{Status: "DELETED", ResourceKey: payload.ResourceKey, DeletionRequestID: deletion.ID, TombstoneID: tombstoneID}, nil
}

func (s *PostgresStore) LoadLifecycleTarget(ctx context.Context, job SchedulerJob, payload LifecyclePayload) (LifecycleTarget, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return LifecycleTarget{}, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return LifecycleTarget{}, err
	}
	var target LifecycleTarget
	if err = tx.QueryRow(ctx, `SELECT rr.result_id::text,rr.result_revision,rr.calculated_at,
EXISTS (
  SELECT 1 FROM core_registry.metric_result_heads h
  WHERE h.tenant_id=rr.tenant_id AND h.site_id=rr.site_id AND h.current_result_id=rr.result_id
)
FROM core_registry.metric_result_revisions rr
WHERE rr.tenant_id=$1::uuid AND rr.site_id=$2::uuid AND rr.result_id=$3::uuid AND rr.status='PERSISTED'`,
		job.TenantID, job.SiteID, payload.ResourceKey).Scan(&target.ResultID, &target.Revision, &target.CalculatedAt, &target.IsCurrent); err != nil {
		return LifecycleTarget{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LifecycleTarget{}, err
	}
	return target, nil
}

func (s *PostgresStore) LoadLifecyclePolicy(ctx context.Context, job SchedulerJob, payload LifecyclePayload, at time.Time) (LifecyclePolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return LifecyclePolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return LifecyclePolicy{}, err
	}
	var policy LifecyclePolicy
	var deleteDays pgtype.Int4
	if err = tx.QueryRow(ctx, `SELECT id::text,delete_after_days,archive_required
FROM core_registry.data_lifecycle_policies
WHERE tenant_id=$1::uuid AND dataset_code=$2 AND data_class=$3 AND status='RELEASED'
  AND effective_from <= $4 AND (effective_to IS NULL OR effective_to > $4)
ORDER BY effective_from DESC LIMIT 1`, job.TenantID, payload.DatasetCode, payload.DataClass, at).Scan(&policy.ID, &deleteDays, &policy.ArchiveRequired); err != nil {
		return LifecyclePolicy{}, err
	}
	if deleteDays.Valid {
		value := int(deleteDays.Int32)
		policy.DeleteAfterDays = &value
	}
	if err = tx.Commit(ctx); err != nil {
		return LifecyclePolicy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) LegalHoldBlocks(ctx context.Context, job SchedulerJob, payload LifecyclePayload, at time.Time) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return false, err
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT core_registry.legal_hold_blocks_deletion($1::uuid,$2::uuid,$3,$4,$5)`, job.TenantID, job.SiteID, payload.DatasetCode, payload.ResourceKey, at).Scan(&blocked); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return blocked, nil
}

func (s *PostgresStore) VerifiedArchiveManifest(ctx context.Context, job SchedulerJob, payload LifecyclePayload) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return "", err
	}
	var id string
	if err = tx.QueryRow(ctx, `SELECT id::text
FROM core_registry.archive_manifests
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND dataset_code=$3 AND status='VERIFIED'
  AND scope_selector->>'resourceKey'=$4
ORDER BY verified_at DESC LIMIT 1`, job.TenantID, job.SiteID, payload.DatasetCode, payload.ResourceKey).Scan(&id); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (s *PostgresStore) EnsureRetentionDeletion(ctx context.Context, job SchedulerJob, payload LifecyclePayload, target LifecycleTarget, policy LifecyclePolicy, archiveManifestID string, at time.Time) (LifecycleDeletion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LifecycleDeletion{}, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return LifecycleDeletion{}, err
	}
	var existing LifecycleDeletion
	err = tx.QueryRow(ctx, `SELECT id::text,status FROM core_registry.deletion_requests
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND dataset_code=$3 AND resource_key=$4
  AND reason_code='RETENTION' AND status IN ('APPROVED','APPLIED')
ORDER BY requested_at DESC LIMIT 1`, job.TenantID, job.SiteID, payload.DatasetCode, payload.ResourceKey).Scan(&existing.ID, &existing.Status)
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return LifecycleDeletion{}, commitErr
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LifecycleDeletion{}, err
	}
	id, err := uuidv7(at)
	if err != nil {
		return LifecycleDeletion{}, err
	}
	evidence, err := json.Marshal(map[string]any{"jobId": job.JobID, "policyId": policy.ID, "sourceRevision": target.Revision})
	if err != nil {
		return LifecycleDeletion{}, err
	}
	var archive any
	if archiveManifestID != "" {
		archive = archiveManifestID
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.deletion_requests(
id,tenant_id,site_id,dataset_code,resource_key,reason_code,evidence,status,requested_at,approved_at,revision,created_at,updated_at,archive_manifest_id)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,'RETENTION',$6::jsonb,'APPROVED',$7,$7,1,$7,$7,$8::uuid)`,
		id, job.TenantID, job.SiteID, payload.DatasetCode, payload.ResourceKey, evidence, at, archive)
	if err != nil {
		return LifecycleDeletion{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LifecycleDeletion{}, err
	}
	return LifecycleDeletion{ID: id, Status: "APPROVED"}, nil
}

func (s *PostgresStore) ApplyRetentionDeletion(ctx context.Context, job SchedulerJob, payload LifecyclePayload, target LifecycleTarget, deletion LifecycleDeletion, at time.Time) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.deletion_requests
SET status='APPLIED',applied_at=COALESCE(applied_at,$5),updated_at=$5,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND resource_key=$4 AND status='APPROVED'`,
		job.TenantID, job.SiteID, deletion.ID, payload.ResourceKey, at)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		var status string
		if err = tx.QueryRow(ctx, `SELECT status FROM core_registry.deletion_requests WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid`, job.TenantID, job.SiteID, deletion.ID).Scan(&status); err != nil {
			return "", err
		}
		if status != "APPLIED" {
			return "", errors.New("retention deletion request could not be applied")
		}
	}
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM core_registry.deletion_tombstones
WHERE tenant_id=$1::uuid AND dataset_code=$2 AND resource_key=$3`, job.TenantID, payload.DatasetCode, payload.ResourceKey).Scan(&existingID)
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return "", commitErr
		}
		return existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	tombstoneID, err := uuidv7(at)
	if err != nil {
		return "", err
	}
	metadata, err := json.Marshal(map[string]any{"jobId": job.JobID})
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.deletion_tombstones(
id,tenant_id,site_id,deletion_request_id,dataset_code,resource_key,deleted_at,source_revision,metadata,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9::jsonb,$7)`, tombstoneID, job.TenantID, job.SiteID, deletion.ID, payload.DatasetCode, payload.ResourceKey, at, strconv.FormatUint(target.Revision, 10), metadata)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return tombstoneID, nil
}

func (s *PostgresStore) RecordLifecycleEvent(ctx context.Context, job SchedulerJob, payload LifecyclePayload, eventType string, evidence map[string]any, at time.Time) error {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	id, err := uuidv7(at)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, job.TenantID, job.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.lifecycle_execution_events(
id,tenant_id,site_id,job_id,dataset_code,resource_key,event_type,evidence,occurred_at,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8::jsonb,$9,$9)`, id, job.TenantID, job.SiteID, job.JobID, payload.DatasetCode, payload.ResourceKey, eventType, encoded, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
