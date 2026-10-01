package workorderservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	postgresCreateOperation = "CREATE"
	postgresAssignOperation = "ASSIGN"
)

func (store *PostgresStore) Create(ctx context.Context, organizationID, siteID string, mutation CreateMutation) (MutationResult, error) {
	if store == nil || store.mutationPool == nil {
		return MutationResult{}, ErrUnavailable
	}
	idempotencyKey := strings.TrimSpace(mutation.IdempotencyKey)
	if !workordermodel.IsUUIDv7(organizationID) || !workordermodel.IsUUIDv7(siteID) || !idempotencyKeyPattern.MatchString(idempotencyKey) {
		return MutationResult{}, workordermodel.ErrInvalidCreate
	}
	digest, err := createMutationDigest(mutation)
	if err != nil {
		return MutationResult{}, workordermodel.ErrInvalidCreate
	}
	tx, err := store.beginWriterTransaction(ctx, organizationID)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	resourceKey := "site:" + siteID
	if err := lockPostgresMutationKey(ctx, tx, organizationID, siteID, postgresCreateOperation, resourceKey, idempotencyKey); err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := readPostgresReplay(ctx, tx, organizationID, siteID, postgresCreateOperation, resourceKey, idempotencyKey, digest, ""); err != nil {
		return MutationResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return MutationResult{}, fmt.Errorf("commit Work Order create replay: %w", err)
		}
		return MutationResult{WorkOrder: replay, Replayed: true}, nil
	}
	created, err := workordermodel.Create(mutation.createInput(organizationID, siteID))
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_order_runtime.work_order_current (
			work_order_id, tenant_id, site_id, title, description, priority, status,
			assignee_id, team_id, scheduled_start, due_at,
			task_total, task_completed, task_blocked, note_count, attachment_count,
			version, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,0,0,0,0,0,$12,$13,$13)
	`, created.WorkOrderID, organizationID, siteID, created.Title, created.Description, string(created.Priority), string(created.Status),
		created.AssigneeID, created.TeamID, created.ScheduledStart, created.DueAt, created.Version, created.CreatedAt); err != nil {
		return MutationResult{}, fmt.Errorf("insert Work Order current projection: %w", err)
	}
	for _, reference := range created.SourceReferences {
		if _, err := tx.Exec(ctx, `
			INSERT INTO work_order_runtime.work_order_source_reference (
				tenant_id, site_id, work_order_id, source_domain, source_resource_id, relationship, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, organizationID, siteID, created.WorkOrderID, string(reference.Domain), reference.ResourceID, string(reference.Relationship), created.CreatedAt); err != nil {
			return MutationResult{}, fmt.Errorf("insert Work Order source reference: %w", err)
		}
	}
	if err := insertPostgresTimeline(ctx, tx, created, created.Timeline[0]); err != nil {
		return MutationResult{}, err
	}
	if err := insertPostgresMutationEvidence(ctx, tx, organizationID, siteID, postgresCreateOperation, postgresCreateOperation, resourceKey, idempotencyKey, digest,
		mutation.ActorType, mutation.ActorID, mutation.PolicyRevision, mutation.CorrelationID, created.UpdatedAt, created); err != nil {
		return MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, fmt.Errorf("commit Work Order create: %w", err)
	}
	return MutationResult{WorkOrder: created}, nil
}

func (store *PostgresStore) Assign(ctx context.Context, organizationID, siteID, workOrderID string, mutation AssignmentMutation) (MutationResult, error) {
	if store == nil || store.mutationPool == nil {
		return MutationResult{}, ErrUnavailable
	}
	idempotencyKey := strings.TrimSpace(mutation.IdempotencyKey)
	if !workordermodel.IsUUIDv7(organizationID) || !workordermodel.IsUUIDv7(siteID) || !workordermodel.IsUUIDv7(workOrderID) || !idempotencyKeyPattern.MatchString(idempotencyKey) {
		return MutationResult{}, workordermodel.ErrInvalidAssignment
	}
	digest, err := assignmentMutationDigest(mutation)
	if err != nil {
		return MutationResult{}, workordermodel.ErrInvalidAssignment
	}
	tx, err := store.beginWriterTransaction(ctx, organizationID)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	resourceKey := "work-order:" + workOrderID
	if err := lockPostgresMutationKey(ctx, tx, organizationID, siteID, postgresAssignOperation, resourceKey, idempotencyKey); err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := readPostgresReplay(ctx, tx, organizationID, siteID, postgresAssignOperation, resourceKey, idempotencyKey, digest, workOrderID); err != nil {
		return MutationResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return MutationResult{}, fmt.Errorf("commit Work Order assignment replay: %w", err)
		}
		return MutationResult{WorkOrder: replay, Replayed: true}, nil
	}
	record, err := getCurrentRecordForMutation(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return MutationResult{}, err
	}
	current, err := hydrateProjection(ctx, tx, record)
	if err != nil {
		return MutationResult{}, err
	}
	updated, err := workordermodel.ApplyAssignment(current, mutation.assignmentInput())
	if err != nil {
		return MutationResult{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE work_order_runtime.work_order_current
		SET assignee_id = $5, team_id = $6, version = $7, updated_at = $8
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3 AND version = $4
	`, organizationID, siteID, workOrderID, mutation.ExpectedVersion, updated.AssigneeID, updated.TeamID, updated.Version, updated.UpdatedAt)
	if err != nil {
		return MutationResult{}, fmt.Errorf("update Work Order assignment: %w", err)
	}
	if command.RowsAffected() != 1 {
		return MutationResult{}, workordermodel.ErrVersionConflict
	}
	if err := insertPostgresTimeline(ctx, tx, updated, updated.Timeline[len(updated.Timeline)-1]); err != nil {
		return MutationResult{}, err
	}
	if err := insertPostgresMutationEvidence(ctx, tx, organizationID, siteID, postgresAssignOperation, postgresAssignOperation, resourceKey, idempotencyKey, digest,
		mutation.ActorType, mutation.ActorID, mutation.PolicyRevision, mutation.CorrelationID, updated.UpdatedAt, updated); err != nil {
		return MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, fmt.Errorf("commit Work Order assignment: %w", err)
	}
	return MutationResult{WorkOrder: updated}, nil
}

func (store *PostgresStore) Transition(ctx context.Context, organizationID, siteID, workOrderID string, mutation LifecycleMutation) (MutationResult, error) {
	if store == nil || store.mutationPool == nil {
		return MutationResult{}, ErrUnavailable
	}
	idempotencyKey := strings.TrimSpace(mutation.IdempotencyKey)
	if !workordermodel.IsUUIDv7(organizationID) || !workordermodel.IsUUIDv7(siteID) || !workordermodel.IsUUIDv7(workOrderID) || !idempotencyKeyPattern.MatchString(idempotencyKey) {
		return MutationResult{}, workordermodel.ErrInvalidLifecycle
	}
	digest, err := lifecycleMutationDigest(mutation)
	if err != nil {
		return MutationResult{}, workordermodel.ErrInvalidLifecycle
	}
	auditOperation := string(mutation.Operation)
	idempotencyOperation := lifecycleIdempotencyOperation
	tx, err := store.beginWriterTransaction(ctx, organizationID)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	resourceKey := "work-order:" + workOrderID
	if err := lockPostgresMutationKey(ctx, tx, organizationID, siteID, idempotencyOperation, resourceKey, idempotencyKey); err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := readPostgresReplay(ctx, tx, organizationID, siteID, idempotencyOperation, resourceKey, idempotencyKey, digest, workOrderID); err != nil {
		return MutationResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return MutationResult{}, fmt.Errorf("commit Work Order lifecycle replay: %w", err)
		}
		return MutationResult{WorkOrder: replay, Replayed: true}, nil
	}
	record, err := getCurrentRecordForMutation(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return MutationResult{}, err
	}
	current, err := hydrateProjection(ctx, tx, record)
	if err != nil {
		return MutationResult{}, err
	}
	updated, err := workordermodel.ApplyLifecycle(current, mutation.lifecycleInput())
	if err != nil {
		return MutationResult{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE work_order_runtime.work_order_current
		SET status = $5, scheduled_start = $6, due_at = $7, version = $8, updated_at = $9
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3 AND version = $4
	`, organizationID, siteID, workOrderID, mutation.ExpectedVersion, string(updated.Status), updated.ScheduledStart, updated.DueAt, updated.Version, updated.UpdatedAt)
	if err != nil {
		return MutationResult{}, fmt.Errorf("update Work Order lifecycle: %w", err)
	}
	if command.RowsAffected() != 1 {
		return MutationResult{}, workordermodel.ErrVersionConflict
	}
	if mutation.Operation == workordermodel.OperationComplete {
		added := updated.CompletionEvidence[len(current.CompletionEvidence):]
		for _, reference := range added {
			if _, err := tx.Exec(ctx, `
				INSERT INTO work_order_runtime.work_order_completion_evidence (
					tenant_id, site_id, work_order_id, kind, reference, captured_at, completion_version
				) VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, organizationID, siteID, workOrderID, reference.Kind, reference.Reference, reference.CapturedAt, updated.Version); err != nil {
				return MutationResult{}, fmt.Errorf("insert Work Order completion evidence: %w", err)
			}
		}
	}
	if err := insertPostgresTimeline(ctx, tx, updated, updated.Timeline[len(updated.Timeline)-1]); err != nil {
		return MutationResult{}, err
	}
	if err := insertPostgresMutationEvidence(ctx, tx, organizationID, siteID, idempotencyOperation, auditOperation, resourceKey, idempotencyKey, digest,
		mutation.ActorType, mutation.ActorID, mutation.PolicyRevision, mutation.CorrelationID, updated.UpdatedAt, updated); err != nil {
		return MutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, fmt.Errorf("commit Work Order lifecycle: %w", err)
	}
	return MutationResult{WorkOrder: updated}, nil
}

func (store *PostgresStore) beginWriterTransaction(ctx context.Context, tenantID string) (pgx.Tx, error) {
	contextTenantID, ok := identitycontext.TenantIDFromContext(ctx)
	if !ok || !workordermodel.IsUUIDv7(tenantID) || contextTenantID != tenantID {
		return nil, ErrUnavailable
	}
	tx, err := store.mutationPool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return nil, fmt.Errorf("begin Work Order writer transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s5_work_order_writer`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("activate Work Order writer role: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("activate Work Order writer Tenant/Organization scope: %w", err)
	}
	return tx, nil
}

func lockPostgresMutationKey(ctx context.Context, tx pgx.Tx, organizationID, siteID, operation, resourceKey, idempotencyKey string) error {
	key := strings.Join([]string{organizationID, siteID, operation, resourceKey, idempotencyKey}, "|")
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("lock Work Order idempotency key: %w", err)
	}
	return nil
}

func readPostgresReplay(ctx context.Context, tx pgx.Tx, organizationID, siteID, operation, resourceKey, idempotencyKey, digest, expectedWorkOrderID string) (workordermodel.WorkOrder, bool, error) {
	var storedDigest string
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT request_digest, response_payload
		FROM work_order_runtime.work_order_idempotency
		WHERE tenant_id = $1 AND site_id = $2 AND operation = $3 AND resource_key = $4 AND idempotency_key = $5
	`, organizationID, siteID, operation, resourceKey, idempotencyKey).Scan(&storedDigest, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return workordermodel.WorkOrder{}, false, nil
	}
	if err != nil {
		return workordermodel.WorkOrder{}, false, fmt.Errorf("read Work Order idempotency record: %w", err)
	}
	if storedDigest != digest {
		return workordermodel.WorkOrder{}, false, ErrIdempotencyConflict
	}
	var replay workordermodel.WorkOrder
	if json.Unmarshal(payload, &replay) != nil || replay.Validate() != nil || replay.TenantID != organizationID || replay.SiteID != siteID ||
		(expectedWorkOrderID != "" && replay.WorkOrderID != expectedWorkOrderID) {
		return workordermodel.WorkOrder{}, false, ErrUnavailable
	}
	return replay, true, nil
}

func insertPostgresMutationEvidence(ctx context.Context, tx pgx.Tx, organizationID, siteID, idempotencyOperation, auditOperation, resourceKey, idempotencyKey, digest,
	actorType, actorID, policyRevision, correlationID, occurredAt string, workOrder workordermodel.WorkOrder) error {
	payload, err := json.Marshal(workOrder)
	if err != nil || len(payload) > 64<<10 {
		return fmt.Errorf("%w: Work Order idempotency response is invalid or oversized", ErrUnavailable)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_order_runtime.work_order_idempotency (
			tenant_id, site_id, operation, resource_key, idempotency_key, request_digest, response_payload, committed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, organizationID, siteID, idempotencyOperation, resourceKey, idempotencyKey, digest, string(payload), occurredAt); err != nil {
		return fmt.Errorf("insert Work Order idempotency record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_order_runtime.work_order_mutation_audit (
			tenant_id, site_id, work_order_id, operation, idempotency_key, request_digest,
			actor_type, actor_id, policy_revision, correlation_id, committed_version, committed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, organizationID, siteID, workOrder.WorkOrderID, auditOperation, idempotencyKey, digest,
		strings.TrimSpace(actorType), strings.TrimSpace(actorID), strings.TrimSpace(policyRevision), strings.TrimSpace(correlationID), workOrder.Version, occurredAt); err != nil {
		return fmt.Errorf("insert Work Order mutation audit: %w", err)
	}
	return nil
}

func insertPostgresTimeline(ctx context.Context, tx pgx.Tx, workOrder workordermodel.WorkOrder, event workordermodel.TimelineEvent) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_order_runtime.work_order_timeline (
			tenant_id, site_id, work_order_id, version, operation, from_status, to_status,
			reason, actor_type, actor_id, assignee_id, team_id, policy_revision, correlation_id, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, workOrder.TenantID, workOrder.SiteID, workOrder.WorkOrderID, event.Version, string(event.Operation), nullableStatus(event.FromStatus), string(event.ToStatus),
		event.Reason, event.ActorType, event.ActorID, event.AssigneeID, event.TeamID, event.PolicyRevision, event.CorrelationID, event.OccurredAt); err != nil {
		return fmt.Errorf("insert Work Order timeline event: %w", err)
	}
	return nil
}

func getCurrentRecordForMutation(ctx context.Context, tx pgx.Tx, organizationID, siteID, workOrderID string) (currentRecord, error) {
	record, err := scanCurrentRecord(tx.QueryRow(ctx, `
		SELECT work_order_id, tenant_id, site_id, title, description, priority, status,
		       assignee_id, team_id, scheduled_start, due_at,
		       task_total, task_completed, task_blocked, note_count, attachment_count,
		       version, created_at, updated_at
		FROM work_order_runtime.work_order_current
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3
		FOR UPDATE
	`, organizationID, siteID, workOrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return currentRecord{}, ErrNotFound
	}
	if err != nil {
		return currentRecord{}, fmt.Errorf("lock Work Order current projection: %w", err)
	}
	return record, nil
}

func nullableStatus(value *workordermodel.Status) any {
	if value == nil {
		return sql.NullString{}
	}
	return string(*value)
}
