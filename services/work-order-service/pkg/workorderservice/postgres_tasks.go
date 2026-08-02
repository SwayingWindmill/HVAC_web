package workorderservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	postgresTaskOperation        = "TASK"
	postgresTaskAppendOperation  = "TASK_APPEND"
	postgresTaskStatusOperation  = "TASK_STATUS"
	postgresTaskReorderOperation = "TASK_REORDER"
)

func (store *PostgresStore) ListTasks(ctx context.Context, organizationID, siteID, workOrderID string) (workordermodel.TaskChecklist, error) {
	if store == nil || store.readPool == nil {
		return workordermodel.TaskChecklist{}, ErrUnavailable
	}
	if !workordermodel.IsUUIDv7(organizationID) || !workordermodel.IsUUIDv7(siteID) || !workordermodel.IsUUIDv7(workOrderID) {
		return workordermodel.TaskChecklist{}, ErrNotFound
	}
	tx, err := store.beginReadTransaction(ctx, organizationID)
	if err != nil {
		return workordermodel.TaskChecklist{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := getCurrentRecord(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return workordermodel.TaskChecklist{}, err
	}
	workOrder, err := hydrateProjection(ctx, tx, record)
	if err != nil {
		return workordermodel.TaskChecklist{}, err
	}
	tasks, err := loadPostgresTasks(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return workordermodel.TaskChecklist{}, err
	}
	checklist, err := workordermodel.NewTaskChecklist(workOrder, tasks)
	if err != nil {
		return workordermodel.TaskChecklist{}, fmt.Errorf("%w: validate stored Work Order task checklist: %v", ErrUnavailable, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return workordermodel.TaskChecklist{}, fmt.Errorf("commit Work Order task list read: %w", err)
	}
	return checklist, nil
}

func (store *PostgresStore) AppendTask(ctx context.Context, organizationID, siteID, workOrderID string, mutation AppendTaskMutation) (TaskMutationResult, error) {
	idempotencyKey := strings.TrimSpace(mutation.IdempotencyKey)
	if !store.validTaskMutationScope(organizationID, siteID, workOrderID, idempotencyKey) || !workordermodel.IsUUIDv7(mutation.TaskID) {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	digest, err := appendTaskMutationDigest(mutation)
	if err != nil {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	return store.executePostgresTaskMutation(ctx, organizationID, siteID, workOrderID, idempotencyKey, digest, postgresTaskAppendOperation, &mutation.TaskID,
		mutation.ActorType, mutation.ActorID, mutation.PolicyRevision, mutation.CorrelationID, mutation.OccurredAt,
		func(current workordermodel.WorkOrder, tasks []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error) {
			return workordermodel.ApplyTaskAppend(current, tasks, workordermodel.AppendTaskInput{
				TaskID: mutation.TaskID, ExpectedWorkOrderVersion: mutation.ExpectedWorkOrderVersion, Title: mutation.Title,
				Reason: mutation.Reason, ActorType: mutation.ActorType, ActorID: mutation.ActorID,
				PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
			})
		},
		func(ctx context.Context, tx pgx.Tx, before, after []workordermodel.Task) error {
			task := after[len(after)-1]
			if _, err := tx.Exec(ctx, `
				INSERT INTO work_order_runtime.work_order_task (
					organization_id, site_id, work_order_id, task_id, position, title, status, version, created_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			`, organizationID, siteID, workOrderID, task.TaskID, task.Position, task.Title, string(task.Status), task.Version, task.CreatedAt, task.UpdatedAt); err != nil {
				return fmt.Errorf("insert Work Order task: %w", err)
			}
			return nil
		})
}

func (store *PostgresStore) SetTaskStatus(ctx context.Context, organizationID, siteID, workOrderID string, mutation TaskStatusMutation) (TaskMutationResult, error) {
	idempotencyKey := strings.TrimSpace(mutation.IdempotencyKey)
	if !store.validTaskMutationScope(organizationID, siteID, workOrderID, idempotencyKey) || !workordermodel.IsUUIDv7(mutation.TaskID) {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	digest, err := taskStatusMutationDigest(mutation)
	if err != nil {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	return store.executePostgresTaskMutation(ctx, organizationID, siteID, workOrderID, idempotencyKey, digest, postgresTaskStatusOperation, &mutation.TaskID,
		mutation.ActorType, mutation.ActorID, mutation.PolicyRevision, mutation.CorrelationID, mutation.OccurredAt,
		func(current workordermodel.WorkOrder, tasks []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error) {
			return workordermodel.ApplyTaskStatus(current, tasks, workordermodel.TaskStatusInput{
				TaskID: mutation.TaskID, ExpectedWorkOrderVersion: mutation.ExpectedWorkOrderVersion,
				ExpectedTaskVersion: mutation.ExpectedTaskVersion, Status: mutation.Status, Reason: mutation.Reason,
				ActorType: mutation.ActorType, ActorID: mutation.ActorID, PolicyRevision: mutation.PolicyRevision,
				CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
			})
		},
		func(ctx context.Context, tx pgx.Tx, before, after []workordermodel.Task) error {
			var updated workordermodel.Task
			for _, task := range after {
				if task.TaskID == mutation.TaskID {
					updated = task
					break
				}
			}
			command, err := tx.Exec(ctx, `
				UPDATE work_order_runtime.work_order_task
				SET status = $6, version = $7, updated_at = $8
				WHERE organization_id = $1 AND site_id = $2 AND work_order_id = $3 AND task_id = $4 AND version = $5
			`, organizationID, siteID, workOrderID, mutation.TaskID, mutation.ExpectedTaskVersion, string(updated.Status), updated.Version, updated.UpdatedAt)
			if err != nil {
				return fmt.Errorf("update Work Order task status: %w", err)
			}
			if command.RowsAffected() != 1 {
				return workordermodel.ErrVersionConflict
			}
			return nil
		})
}

func (store *PostgresStore) ReorderTasks(ctx context.Context, organizationID, siteID, workOrderID string, mutation ReorderTasksMutation) (TaskMutationResult, error) {
	idempotencyKey := strings.TrimSpace(mutation.IdempotencyKey)
	if !store.validTaskMutationScope(organizationID, siteID, workOrderID, idempotencyKey) {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	digest, err := reorderTasksMutationDigest(mutation)
	if err != nil {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	return store.executePostgresTaskMutation(ctx, organizationID, siteID, workOrderID, idempotencyKey, digest, postgresTaskReorderOperation, nil,
		mutation.ActorType, mutation.ActorID, mutation.PolicyRevision, mutation.CorrelationID, mutation.OccurredAt,
		func(current workordermodel.WorkOrder, tasks []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error) {
			return workordermodel.ApplyTaskReorder(current, tasks, workordermodel.ReorderTasksInput{
				ExpectedWorkOrderVersion: mutation.ExpectedWorkOrderVersion, TaskIDs: append([]string(nil), mutation.TaskIDs...),
				Reason: mutation.Reason, ActorType: mutation.ActorType, ActorID: mutation.ActorID,
				PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
			})
		},
		func(ctx context.Context, tx pgx.Tx, before, after []workordermodel.Task) error {
			if _, err := tx.Exec(ctx, `
				UPDATE work_order_runtime.work_order_task
				SET position = position + 1000000
				WHERE organization_id = $1 AND site_id = $2 AND work_order_id = $3
			`, organizationID, siteID, workOrderID); err != nil {
				return fmt.Errorf("stage Work Order task reorder: %w", err)
			}
			for _, task := range after {
				command, err := tx.Exec(ctx, `
					UPDATE work_order_runtime.work_order_task
					SET position = $5, version = $6, updated_at = $7
					WHERE organization_id = $1 AND site_id = $2 AND work_order_id = $3 AND task_id = $4
				`, organizationID, siteID, workOrderID, task.TaskID, task.Position, task.Version, task.UpdatedAt)
				if err != nil {
					return fmt.Errorf("commit Work Order task reorder: %w", err)
				}
				if command.RowsAffected() != 1 {
					return ErrUnavailable
				}
			}
			return nil
		})
}

func (store *PostgresStore) validTaskMutationScope(organizationID, siteID, workOrderID, idempotencyKey string) bool {
	return store != nil && store.mutationPool != nil && workordermodel.IsUUIDv7(organizationID) && workordermodel.IsUUIDv7(siteID) &&
		workordermodel.IsUUIDv7(workOrderID) && idempotencyKeyPattern.MatchString(idempotencyKey)
}

func (store *PostgresStore) executePostgresTaskMutation(
	ctx context.Context,
	organizationID, siteID, workOrderID, idempotencyKey, digest, auditOperation string,
	taskID *string,
	actorType, actorID, policyRevision, correlationID, occurredAt string,
	apply func(workordermodel.WorkOrder, []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error),
	persistTasks func(context.Context, pgx.Tx, []workordermodel.Task, []workordermodel.Task) error,
) (TaskMutationResult, error) {
	tx, err := store.beginWriterTransaction(ctx, organizationID)
	if err != nil {
		return TaskMutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	resourceKey := "work-order:" + workOrderID
	if err := lockPostgresMutationKey(ctx, tx, organizationID, siteID, postgresTaskOperation, resourceKey, idempotencyKey); err != nil {
		return TaskMutationResult{}, err
	}
	if replay, found, err := readPostgresTaskReplay(ctx, tx, organizationID, siteID, resourceKey, idempotencyKey, digest, workOrderID); err != nil {
		return TaskMutationResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return TaskMutationResult{}, fmt.Errorf("commit Work Order task replay: %w", err)
		}
		return TaskMutationResult{Checklist: replay, Replayed: true}, nil
	}
	record, err := getCurrentRecordForMutation(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return TaskMutationResult{}, err
	}
	current, err := hydrateProjection(ctx, tx, record)
	if err != nil {
		return TaskMutationResult{}, err
	}
	before, err := loadPostgresTasks(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return TaskMutationResult{}, err
	}
	if _, err := workordermodel.NewTaskChecklist(current, before); err != nil {
		return TaskMutationResult{}, fmt.Errorf("%w: stored Work Order tasks do not converge: %v", ErrUnavailable, err)
	}
	updated, after, err := apply(current, before)
	if err != nil {
		return TaskMutationResult{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE work_order_runtime.work_order_current
		SET task_total = $5, task_completed = $6, task_blocked = $7, version = $8, updated_at = $9
		WHERE organization_id = $1 AND site_id = $2 AND work_order_id = $3 AND version = $4
	`, organizationID, siteID, workOrderID, current.Version, updated.Tasks.Total, updated.Tasks.Completed, updated.Tasks.Blocked, updated.Version, updated.UpdatedAt)
	if err != nil {
		return TaskMutationResult{}, fmt.Errorf("update Work Order task summary: %w", err)
	}
	if command.RowsAffected() != 1 {
		return TaskMutationResult{}, workordermodel.ErrVersionConflict
	}
	if err := persistTasks(ctx, tx, before, after); err != nil {
		return TaskMutationResult{}, err
	}
	if err := insertPostgresTimeline(ctx, tx, updated, updated.Timeline[len(updated.Timeline)-1]); err != nil {
		return TaskMutationResult{}, err
	}
	checklist, err := workordermodel.NewTaskChecklist(updated, after)
	if err != nil {
		return TaskMutationResult{}, fmt.Errorf("%w: committed Work Order tasks do not converge: %v", ErrUnavailable, err)
	}
	if err := insertPostgresTaskMutationEvidence(ctx, tx, organizationID, siteID, workOrderID, resourceKey, idempotencyKey, digest,
		auditOperation, taskID, actorType, actorID, policyRevision, correlationID, occurredAt, checklist); err != nil {
		return TaskMutationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TaskMutationResult{}, fmt.Errorf("commit Work Order task mutation: %w", err)
	}
	return TaskMutationResult{Checklist: checklist}, nil
}

func loadPostgresTasks(ctx context.Context, tx pgx.Tx, organizationID, siteID, workOrderID string) ([]workordermodel.Task, error) {
	rows, err := tx.Query(ctx, `
		SELECT task_id, position, title, status, version, created_at, updated_at
		FROM work_order_runtime.work_order_task
		WHERE organization_id = $1 AND site_id = $2 AND work_order_id = $3
		ORDER BY position ASC
	`, organizationID, siteID, workOrderID)
	if err != nil {
		return nil, fmt.Errorf("read Work Order tasks: %w", err)
	}
	defer rows.Close()
	tasks := make([]workordermodel.Task, 0)
	for rows.Next() {
		var task workordermodel.Task
		var position int64
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&task.TaskID, &position, &task.Title, &task.Status, &task.Version, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan Work Order task: %w", err)
		}
		if position < 0 {
			return nil, fmt.Errorf("%w: Work Order task position is negative", ErrUnavailable)
		}
		task.Position = uint64(position)
		task.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		task.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Work Order tasks: %w", err)
	}
	return tasks, nil
}

func readPostgresTaskReplay(ctx context.Context, tx pgx.Tx, organizationID, siteID, resourceKey, idempotencyKey, digest, workOrderID string) (workordermodel.TaskChecklist, bool, error) {
	var storedDigest string
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT request_digest, response_payload
		FROM work_order_runtime.work_order_idempotency
		WHERE organization_id = $1 AND site_id = $2 AND operation = $3 AND resource_key = $4 AND idempotency_key = $5
	`, organizationID, siteID, postgresTaskOperation, resourceKey, idempotencyKey).Scan(&storedDigest, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return workordermodel.TaskChecklist{}, false, nil
	}
	if err != nil {
		return workordermodel.TaskChecklist{}, false, fmt.Errorf("read Work Order task idempotency record: %w", err)
	}
	if storedDigest != digest {
		return workordermodel.TaskChecklist{}, false, ErrIdempotencyConflict
	}
	var replay workordermodel.TaskChecklist
	if json.Unmarshal(payload, &replay) != nil || replay.Validate(organizationID, siteID, workOrderID) != nil {
		return workordermodel.TaskChecklist{}, false, ErrUnavailable
	}
	return replay, true, nil
}

func insertPostgresTaskMutationEvidence(
	ctx context.Context,
	tx pgx.Tx,
	organizationID, siteID, workOrderID, resourceKey, idempotencyKey, digest, auditOperation string,
	taskID *string,
	actorType, actorID, policyRevision, correlationID, occurredAt string,
	checklist workordermodel.TaskChecklist,
) error {
	payload, err := json.Marshal(checklist)
	if err != nil || len(payload) > 64<<10 {
		return fmt.Errorf("%w: Work Order task idempotency response is invalid or oversized", ErrUnavailable)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_order_runtime.work_order_idempotency (
			organization_id, site_id, operation, resource_key, idempotency_key, request_digest, response_payload, committed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, organizationID, siteID, postgresTaskOperation, resourceKey, idempotencyKey, digest, string(payload), occurredAt); err != nil {
		return fmt.Errorf("insert Work Order task idempotency record: %w", err)
	}
	var nullableTaskID any
	if taskID != nil {
		nullableTaskID = strings.TrimSpace(*taskID)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_order_runtime.work_order_mutation_audit (
			organization_id, site_id, work_order_id, task_id, operation, idempotency_key, request_digest,
			actor_type, actor_id, policy_revision, correlation_id, committed_version, committed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, organizationID, siteID, workOrderID, nullableTaskID, auditOperation, idempotencyKey, digest,
		strings.TrimSpace(actorType), strings.TrimSpace(actorID), strings.TrimSpace(policyRevision), strings.TrimSpace(correlationID), checklist.WorkOrderVersion, occurredAt); err != nil {
		return fmt.Errorf("insert Work Order task mutation audit: %w", err)
	}
	return nil
}
