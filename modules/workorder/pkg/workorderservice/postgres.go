package workorderservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

type PostgresStore struct {
	readPool     *pgxpool.Pool
	mutationPool *pgxpool.Pool
	cursor       *cursorCodec
}
type currentRecord struct {
	workOrder workordermodel.WorkOrder
	updatedAt time.Time
}

func OpenPostgresStore(ctx context.Context, databaseURL string, cursorSecret []byte) (*PostgresStore, error) {
	codec, err := newCursorCodec(cursorSecret)
	if err != nil {
		return nil, err
	}
	readPool, err := openWorkOrderPool(ctx, databaseURL, "s5_work_order_service")
	if err != nil {
		return nil, err
	}
	return &PostgresStore{readPool: readPool, cursor: codec}, nil
}

func OpenPostgresStoreWithMutations(ctx context.Context, readDatabaseURL, mutationDatabaseURL string, cursorSecret []byte) (*PostgresStore, error) {
	store, err := OpenPostgresStore(ctx, readDatabaseURL, cursorSecret)
	if err != nil {
		return nil, err
	}
	mutationPool, err := openWorkOrderPool(ctx, mutationDatabaseURL, "s5_work_order_mutation_service")
	if err != nil {
		store.Close()
		return nil, err
	}
	store.mutationPool = mutationPool
	return store, nil
}

func openWorkOrderPool(ctx context.Context, databaseURL, expectedUser string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Work Order database URL: %w", err)
	}
	if config.ConnConfig.User != expectedUser {
		return nil, fmt.Errorf("Work Order database identity must be %s", expectedUser)
	}
	config.MaxConns = 16
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Work Order database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Work Order database: %w", err)
	}
	return pool, nil
}

func (store *PostgresStore) Close() {
	if store == nil {
		return
	}
	if store.readPool != nil {
		store.readPool.Close()
	}
	if store.mutationPool != nil {
		store.mutationPool.Close()
	}
}

func (store *PostgresStore) List(ctx context.Context, organizationID, siteID string, filter Filter) (workordermodel.ListResponse, error) {
	if store == nil || store.readPool == nil || store.cursor == nil {
		return workordermodel.ListResponse{}, ErrUnavailable
	}
	filter = normalizeFilter(filter)
	if !validStatusFilter(filter.Status) || !validPriorityFilter(filter.Priority) || len(filter.AssigneeID) > 256 || !validSourceFilter(filter) {
		return workordermodel.ListResponse{}, ErrInvalidFilter
	}
	tx, err := store.beginReadTransaction(ctx, organizationID)
	if err != nil {
		return workordermodel.ListResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cursorTime any
	var cursorID any
	if filter.Cursor != "" {
		position, err := store.cursor.Decode(filter.Cursor, organizationID, siteID, filter)
		if err != nil {
			return workordermodel.ListResponse{}, ErrInvalidCursor
		}
		cursorTime = position.UpdatedAt
		cursorID = position.WorkOrderID
	}
	rows, err := tx.Query(ctx, `
		SELECT work_order_id, tenant_id, site_id, title, description, priority, status,
		       assignee_id, team_id, scheduled_start, due_at,
		       task_total, task_completed, task_blocked, note_count, attachment_count,
		       version, created_at, updated_at
		FROM work_order_runtime.work_order_current
		WHERE tenant_id = $1
		  AND site_id = $2
		  AND ($3 = '' OR status = $3)
		  AND ($4 = '' OR priority = $4)
		  AND ($5 = '' OR assignee_id = $5)
		  AND ($6 = '' OR EXISTS (
		    SELECT 1 FROM work_order_runtime.work_order_source_reference source
		    WHERE source.tenant_id = $1 AND source.site_id = $2
		      AND source.work_order_id = work_order_current.work_order_id
		      AND source.source_domain = $6 AND source.source_resource_id = $7
		  ))
		  AND ($8::timestamptz IS NULL OR updated_at < $8 OR (updated_at = $8 AND work_order_id > $9::uuid))
		ORDER BY updated_at DESC, work_order_id ASC
		LIMIT $10
	`, organizationID, siteID, string(filter.Status), string(filter.Priority), filter.AssigneeID, string(filter.SourceDomain), filter.SourceRef, cursorTime, cursorID, filter.Limit+1)
	if err != nil {
		return workordermodel.ListResponse{}, fmt.Errorf("list Work Orders: %w", err)
	}
	records := make([]currentRecord, 0, filter.Limit+1)
	for rows.Next() {
		record, err := scanCurrentRecord(rows)
		if err != nil {
			rows.Close()
			return workordermodel.ListResponse{}, fmt.Errorf("scan Work Order list: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return workordermodel.ListResponse{}, fmt.Errorf("iterate Work Orders: %w", err)
	}
	rows.Close()

	hasMore := len(records) > filter.Limit
	if hasMore {
		records = records[:filter.Limit]
	}
	items := make([]workordermodel.WorkOrder, 0, len(records))
	for _, record := range records {
		workOrder, err := hydrateProjection(ctx, tx, record)
		if err != nil {
			return workordermodel.ListResponse{}, err
		}
		items = append(items, workOrder)
	}
	response := workordermodel.ListResponse{SchemaVersion: workordermodel.SchemaVersion, Items: items, HasMore: hasMore}
	if hasMore {
		last := records[len(records)-1]
		cursor, err := store.cursor.Encode(organizationID, siteID, filter, last.updatedAt, last.workOrder.WorkOrderID)
		if err != nil {
			return workordermodel.ListResponse{}, fmt.Errorf("encode Work Order cursor: %w", err)
		}
		response.NextCursor = &cursor
	}
	if err := response.Validate(organizationID, siteID, filter.Limit); err != nil {
		return workordermodel.ListResponse{}, fmt.Errorf("%w: validate Work Order list: %v", ErrUnavailable, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return workordermodel.ListResponse{}, fmt.Errorf("commit Work Order list read: %w", err)
	}
	return response, nil
}

func (store *PostgresStore) Get(ctx context.Context, organizationID, siteID, workOrderID string) (workordermodel.WorkOrder, error) {
	if store == nil || store.readPool == nil {
		return workordermodel.WorkOrder{}, ErrUnavailable
	}
	tx, err := store.beginReadTransaction(ctx, organizationID)
	if err != nil {
		return workordermodel.WorkOrder{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := getCurrentRecord(ctx, tx, organizationID, siteID, workOrderID)
	if err != nil {
		return workordermodel.WorkOrder{}, err
	}
	workOrder, err := hydrateProjection(ctx, tx, record)
	if err != nil {
		return workordermodel.WorkOrder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workordermodel.WorkOrder{}, fmt.Errorf("commit Work Order detail read: %w", err)
	}
	return workOrder, nil
}

func (store *PostgresStore) beginReadTransaction(ctx context.Context, tenantID string) (pgx.Tx, error) {
	contextTenantID, ok := identitycontext.TenantIDFromContext(ctx)
	if !ok || !workordermodel.IsUUIDv7(tenantID) || contextTenantID != tenantID {
		return nil, ErrUnavailable
	}
	tx, err := store.readPool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin Work Order transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s5_work_order_runtime`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("activate Work Order read role: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("activate Work Order Tenant/Organization scope: %w", err)
	}
	return tx, nil
}

func getCurrentRecord(ctx context.Context, tx pgx.Tx, organizationID, siteID, workOrderID string) (currentRecord, error) {
	record, err := scanCurrentRecord(tx.QueryRow(ctx, `
		SELECT work_order_id, tenant_id, site_id, title, description, priority, status,
		       assignee_id, team_id, scheduled_start, due_at,
		       task_total, task_completed, task_blocked, note_count, attachment_count,
		       version, created_at, updated_at
		FROM work_order_runtime.work_order_current
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3
	`, organizationID, siteID, workOrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return currentRecord{}, ErrNotFound
	}
	if err != nil {
		return currentRecord{}, fmt.Errorf("read Work Order current projection: %w", err)
	}
	return record, nil
}

type rowScanner interface{ Scan(...any) error }

func scanCurrentRecord(scanner rowScanner) (currentRecord, error) {
	var record currentRecord
	var assigneeID, teamID sql.NullString
	var scheduledStart, dueAt sql.NullTime
	var taskTotal, taskCompleted, taskBlocked, noteCount, attachmentCount int64
	var createdAt, updatedAt time.Time
	if err := scanner.Scan(
		&record.workOrder.WorkOrderID, &record.workOrder.TenantID, &record.workOrder.SiteID,
		&record.workOrder.Title, &record.workOrder.Description, &record.workOrder.Priority, &record.workOrder.Status,
		&assigneeID, &teamID, &scheduledStart, &dueAt,
		&taskTotal, &taskCompleted, &taskBlocked, &noteCount, &attachmentCount,
		&record.workOrder.Version, &createdAt, &updatedAt,
	); err != nil {
		return currentRecord{}, err
	}
	if taskTotal < 0 || taskCompleted < 0 || taskBlocked < 0 || noteCount < 0 || attachmentCount < 0 {
		return currentRecord{}, errors.New("Work Order summary counts are negative")
	}
	record.workOrder.SchemaVersion = workordermodel.SchemaVersion
	record.workOrder.AssigneeID = nullableStringPointer(assigneeID)
	record.workOrder.TeamID = nullableStringPointer(teamID)
	record.workOrder.ScheduledStart = nullableTimePointer(scheduledStart)
	record.workOrder.DueAt = nullableTimePointer(dueAt)
	record.workOrder.Tasks = workordermodel.TaskSummary{Total: uint64(taskTotal), Completed: uint64(taskCompleted), Blocked: uint64(taskBlocked)}
	record.workOrder.NoteCount = uint64(noteCount)
	record.workOrder.AttachmentCount = uint64(attachmentCount)
	record.workOrder.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	record.workOrder.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	record.updatedAt = updatedAt.UTC()
	return record, nil
}

func hydrateProjection(ctx context.Context, tx pgx.Tx, record currentRecord) (workordermodel.WorkOrder, error) {
	workOrder := record.workOrder

	sourceRows, err := tx.Query(ctx, `
		SELECT source_domain, source_resource_id, relationship
		FROM work_order_runtime.work_order_source_reference
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3
		ORDER BY relationship ASC, source_domain ASC, source_resource_id ASC
	`, workOrder.TenantID, workOrder.SiteID, workOrder.WorkOrderID)
	if err != nil {
		return workordermodel.WorkOrder{}, fmt.Errorf("read Work Order sources: %w", err)
	}
	for sourceRows.Next() {
		var reference workordermodel.SourceReference
		if err := sourceRows.Scan(&reference.Domain, &reference.ResourceID, &reference.Relationship); err != nil {
			sourceRows.Close()
			return workordermodel.WorkOrder{}, fmt.Errorf("scan Work Order source: %w", err)
		}
		workOrder.SourceReferences = append(workOrder.SourceReferences, reference)
	}
	if err := sourceRows.Err(); err != nil {
		sourceRows.Close()
		return workordermodel.WorkOrder{}, fmt.Errorf("iterate Work Order sources: %w", err)
	}
	sourceRows.Close()

	timelineRows, err := tx.Query(ctx, `
		SELECT operation, from_status, to_status, reason, actor_type, actor_id,
		       assignee_id, team_id, policy_revision, correlation_id, occurred_at, version
		FROM work_order_runtime.work_order_timeline
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3
		ORDER BY version ASC
	`, workOrder.TenantID, workOrder.SiteID, workOrder.WorkOrderID)
	if err != nil {
		return workordermodel.WorkOrder{}, fmt.Errorf("read Work Order timeline: %w", err)
	}
	for timelineRows.Next() {
		var event workordermodel.TimelineEvent
		var fromStatus, assigneeID, teamID, policyRevision, correlationID sql.NullString
		var occurredAt time.Time
		if err := timelineRows.Scan(
			&event.Operation, &fromStatus, &event.ToStatus, &event.Reason, &event.ActorType, &event.ActorID,
			&assigneeID, &teamID, &policyRevision, &correlationID, &occurredAt, &event.Version,
		); err != nil {
			timelineRows.Close()
			return workordermodel.WorkOrder{}, fmt.Errorf("scan Work Order timeline: %w", err)
		}
		if fromStatus.Valid {
			value := workordermodel.Status(fromStatus.String)
			event.FromStatus = &value
		}
		event.AssigneeID = nullableStringPointer(assigneeID)
		event.TeamID = nullableStringPointer(teamID)
		event.PolicyRevision = nullableStringPointer(policyRevision)
		event.CorrelationID = nullableStringPointer(correlationID)
		event.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
		workOrder.Timeline = append(workOrder.Timeline, event)
	}
	if err := timelineRows.Err(); err != nil {
		timelineRows.Close()
		return workordermodel.WorkOrder{}, fmt.Errorf("iterate Work Order timeline: %w", err)
	}
	timelineRows.Close()

	evidenceRows, err := tx.Query(ctx, `
		SELECT kind, reference, captured_at
		FROM work_order_runtime.work_order_completion_evidence
		WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3
		ORDER BY captured_at ASC, kind ASC, reference ASC
	`, workOrder.TenantID, workOrder.SiteID, workOrder.WorkOrderID)
	if err != nil {
		return workordermodel.WorkOrder{}, fmt.Errorf("read Work Order completion evidence: %w", err)
	}
	for evidenceRows.Next() {
		var reference workordermodel.EvidenceReference
		var capturedAt time.Time
		if err := evidenceRows.Scan(&reference.Kind, &reference.Reference, &capturedAt); err != nil {
			evidenceRows.Close()
			return workordermodel.WorkOrder{}, fmt.Errorf("scan Work Order completion evidence: %w", err)
		}
		reference.CapturedAt = capturedAt.UTC().Format(time.RFC3339Nano)
		workOrder.CompletionEvidence = append(workOrder.CompletionEvidence, reference)
	}
	if err := evidenceRows.Err(); err != nil {
		evidenceRows.Close()
		return workordermodel.WorkOrder{}, fmt.Errorf("iterate Work Order completion evidence: %w", err)
	}
	evidenceRows.Close()

	var taskTotal, taskCompleted, taskBlocked, noteCount, attachmentCount int64
	if err := tx.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM work_order_runtime.work_order_task WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3),
		  (SELECT count(*) FROM work_order_runtime.work_order_task WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3 AND status = 'COMPLETED'),
		  (SELECT count(*) FROM work_order_runtime.work_order_task WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3 AND status = 'BLOCKED'),
		  (SELECT count(*) FROM work_order_runtime.work_order_note WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3),
		  (SELECT count(*) FROM work_order_runtime.work_order_attachment_metadata WHERE tenant_id = $1 AND site_id = $2 AND work_order_id = $3)
	`, workOrder.TenantID, workOrder.SiteID, workOrder.WorkOrderID).Scan(&taskTotal, &taskCompleted, &taskBlocked, &noteCount, &attachmentCount); err != nil {
		return workordermodel.WorkOrder{}, fmt.Errorf("read Work Order projection counts: %w", err)
	}
	if uint64(taskTotal) != workOrder.Tasks.Total || uint64(taskCompleted) != workOrder.Tasks.Completed || uint64(taskBlocked) != workOrder.Tasks.Blocked ||
		uint64(noteCount) != workOrder.NoteCount || uint64(attachmentCount) != workOrder.AttachmentCount {
		return workordermodel.WorkOrder{}, fmt.Errorf("%w: Work Order projection summaries do not converge", ErrUnavailable)
	}
	if err := workOrder.Validate(); err != nil {
		return workordermodel.WorkOrder{}, fmt.Errorf("%w: validate stored Work Order: %v", ErrUnavailable, err)
	}
	return workOrder, nil
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func nullableTimePointer(value sql.NullTime) *string {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC().Format(time.RFC3339Nano)
	return &result
}
