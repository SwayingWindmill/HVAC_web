package commandservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func (store *PostgresStore) GetCommandBatchByIdempotencyKey(ctx context.Context, tenantID, siteID, key, requestHash string) (CommandBatchView, bool, error) {
	if store == nil || store.pool == nil {
		return CommandBatchView{}, false, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || !commandmodel.IsUUIDv7(siteID) || strings.TrimSpace(key) == "" {
		return CommandBatchView{}, false, ErrInvalidRequest
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return CommandBatchView{}, false, fmt.Errorf("begin batch idempotency lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return CommandBatchView{}, false, err
	}
	var batchID, storedHash string
	err = tx.QueryRow(ctx, `
SELECT batch_id::text, request_hash
FROM command_runtime.batches
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND idempotency_key = $3
`, tenantID, siteID, key).Scan(&batchID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommandBatchView{}, false, nil
	}
	if err != nil {
		return CommandBatchView{}, false, fmt.Errorf("lookup batch by idempotency key: %w", err)
	}
	if storedHash != requestHash {
		return CommandBatchView{}, false, ErrCommandBatchConflict
	}
	view, err := loadPostgresBatchView(ctx, tx, tenantID, siteID, batchID)
	if err != nil {
		return CommandBatchView{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommandBatchView{}, false, fmt.Errorf("commit batch idempotency lookup: %w", err)
	}
	return view, true, nil
}

func (store *PostgresStore) CreateCommandBatch(ctx context.Context, record CommandBatchRecord) (CommandBatchView, bool, error) {
	if store == nil || store.pool == nil {
		return CommandBatchView{}, false, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(record.TenantID) || !commandmodel.IsUUIDv7(record.SiteID) || strings.TrimSpace(record.IdempotencyKey) == "" {
		return CommandBatchView{}, false, ErrInvalidRequest
	}
	now := store.now().UTC()
	if !record.CreatedAt.IsZero() {
		now = record.CreatedAt.UTC()
	}
	batchID := strings.TrimSpace(record.ID)
	if batchID == "" {
		allocated, err := store.newID(now)
		if err != nil {
			return CommandBatchView{}, false, fmt.Errorf("allocate batch id: %w", err)
		}
		batchID = allocated
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CommandBatchView{}, false, fmt.Errorf("begin create command batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, record.TenantID); err != nil {
		return CommandBatchView{}, false, err
	}

	var insertedID string
	err = tx.QueryRow(ctx, `
INSERT INTO command_runtime.batches (
  batch_id, tenant_id, site_id, idempotency_key, request_hash,
  capability_name, command_source, delivery_mode, ttl_seconds, expires_at,
  target_count, accepted_count, rejected_count, status,
  initiated_by_principal_id, trace_id, reason, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5,
  $6, $7, $8, $9, $10,
  $11, $12, $13, $14,
  $15, $16, $17, $18, $19
)
ON CONFLICT (tenant_id, site_id, idempotency_key) DO NOTHING
RETURNING batch_id::text
`, batchID, record.TenantID, record.SiteID, record.IdempotencyKey, record.RequestHash,
		string(record.Capability), string(record.CommandSource), string(commandmodel.NormalizeDeliveryMode(record.DeliveryMode)),
		record.TTLSeconds, record.ExpiresAt,
		record.TargetCount, record.AcceptedCount, record.RejectedCount, record.Status,
		record.InitiatedByPrincipalID, record.TraceID, record.Reason, now, now).Scan(&insertedID)

	if errors.Is(err, pgx.ErrNoRows) {
		var existingID, storedHash string
		if checkErr := tx.QueryRow(ctx, `
SELECT batch_id::text, request_hash
FROM command_runtime.batches
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND idempotency_key = $3
`, record.TenantID, record.SiteID, record.IdempotencyKey).Scan(&existingID, &storedHash); checkErr != nil {
			return CommandBatchView{}, false, fmt.Errorf("reload conflicted command batch: %w", checkErr)
		}
		if storedHash != record.RequestHash {
			return CommandBatchView{}, false, ErrCommandBatchConflict
		}
		view, loadErr := loadPostgresBatchView(ctx, tx, record.TenantID, record.SiteID, existingID)
		if loadErr != nil {
			return CommandBatchView{}, false, loadErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return CommandBatchView{}, false, fmt.Errorf("commit replayed batch read: %w", commitErr)
		}
		return view, true, nil
	}
	if err != nil {
		return CommandBatchView{}, false, classifyCommandWriteError("insert command batch", err)
	}

	view, err := loadPostgresBatchView(ctx, tx, record.TenantID, record.SiteID, insertedID)
	if err != nil {
		return CommandBatchView{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommandBatchView{}, false, fmt.Errorf("commit create command batch: %w", err)
	}
	return view, false, nil
}

func (store *PostgresStore) FinalizeCommandBatch(
	ctx context.Context,
	tenantID string,
	batchID string,
	items []CommandBatchItemRecord,
	acceptedCount int,
	rejectedCount int,
	status string,
) (CommandBatchView, error) {
	if store == nil || store.pool == nil {
		return CommandBatchView{}, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || !lowerUUIDPattern.MatchString(batchID) {
		return CommandBatchView{}, ErrInvalidRequest
	}
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return CommandBatchView{}, fmt.Errorf("begin finalize command batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return CommandBatchView{}, err
	}

	var siteID string
	if err := tx.QueryRow(ctx, `
SELECT site_id::text
FROM command_runtime.batches
WHERE tenant_id = $1::uuid AND batch_id = $2::uuid
FOR UPDATE
`, tenantID, batchID).Scan(&siteID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CommandBatchView{}, ErrCommandBatchNotFound
		}
		return CommandBatchView{}, fmt.Errorf("lock batch for finalization: %w", err)
	}

	for _, item := range items {
		if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.batch_items (
  batch_id, item_index, tenant_id, site_id, device_id, point_id,
  command_id, admitted, rejection_code, rejection_reason, status, created_at, updated_at
) VALUES (
  $1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7::uuid, $8, $9, $10, $11, $12, $12
)
ON CONFLICT (batch_id, item_index) DO UPDATE
SET command_id = EXCLUDED.command_id,
    admitted = EXCLUDED.admitted,
    rejection_code = EXCLUDED.rejection_code,
    rejection_reason = EXCLUDED.rejection_reason,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at
`, batchID, item.ItemIndex, tenantID, siteID, item.DeviceID, item.PointID,
			item.CommandID, item.Admitted, item.RejectionCode, item.RejectionReason, item.Status, now); err != nil {
			return CommandBatchView{}, classifyCommandWriteError("upsert command batch item", err)
		}
	}

	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.batches
SET accepted_count = $3, rejected_count = $4, status = $5, updated_at = $6
WHERE tenant_id = $1::uuid AND batch_id = $2::uuid
`, tenantID, batchID, acceptedCount, rejectedCount, status, now); err != nil {
		return CommandBatchView{}, classifyCommandWriteError("update command batch summary", err)
	}

	view, err := loadPostgresBatchView(ctx, tx, tenantID, siteID, batchID)
	if err != nil {
		return CommandBatchView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommandBatchView{}, fmt.Errorf("commit finalize command batch: %w", err)
	}
	return view, nil
}

func (store *PostgresStore) GetCommandBatch(ctx context.Context, tenantID, siteID, batchID string) (CommandBatchView, error) {
	if store == nil || store.pool == nil {
		return CommandBatchView{}, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || !commandmodel.IsUUIDv7(siteID) || !lowerUUIDPattern.MatchString(batchID) {
		return CommandBatchView{}, ErrCommandBatchNotFound
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return CommandBatchView{}, fmt.Errorf("begin get command batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return CommandBatchView{}, err
	}
	view, err := loadPostgresBatchView(ctx, tx, tenantID, siteID, batchID)
	if err != nil {
		return CommandBatchView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommandBatchView{}, fmt.Errorf("commit get command batch: %w", err)
	}
	return view, nil
}

func (store *PostgresStore) ListCommandBatches(ctx context.Context, tenantID, siteID string, limit int) ([]CommandBatchView, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || !commandmodel.IsUUIDv7(siteID) {
		return nil, ErrInvalidRequest
	}
	if limit <= 0 || limit > MaximumListedCommands {
		limit = 50
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin list command batches: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
SELECT batch_id::text
FROM command_runtime.batches
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY created_at DESC, batch_id DESC
LIMIT $3
`, tenantID, siteID, limit)
	if err != nil {
		return nil, fmt.Errorf("list command batches: %w", err)
	}
	batchIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("collect command batch ids: %w", err)
	}
	views := make([]CommandBatchView, 0, len(batchIDs))
	for _, batchID := range batchIDs {
		view, err := loadPostgresBatchView(ctx, tx, tenantID, siteID, batchID)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit list command batches: %w", err)
	}
	return views, nil
}

func (store *PostgresStore) UpdateCommandBatchItemsStatus(ctx context.Context, tenantID, batchID string, commandIDs []string, status string) error {
	if store == nil || store.pool == nil {
		return errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || !lowerUUIDPattern.MatchString(batchID) || len(commandIDs) == 0 {
		return nil
	}
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin update batch items status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.batch_items
SET status = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND batch_id = $2::uuid AND command_id = ANY($3::uuid[])
`, tenantID, batchID, commandIDs, status, now); err != nil {
		return fmt.Errorf("update command batch items status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit update batch items status: %w", err)
	}
	return nil
}

func loadPostgresBatchView(ctx context.Context, tx pgx.Tx, tenantID, siteID, batchID string) (CommandBatchView, error) {
	var rec CommandBatchRecord
	var capability, source, deliveryMode string
	err := tx.QueryRow(ctx, `
SELECT batch_id::text, tenant_id::text, site_id::text, idempotency_key, request_hash,
       capability_name, command_source, delivery_mode, ttl_seconds, expires_at,
       target_count, accepted_count, rejected_count, status,
       initiated_by_principal_id, trace_id, reason, created_at, updated_at
FROM command_runtime.batches
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND batch_id = $3::uuid
`, tenantID, siteID, batchID).Scan(
		&rec.ID, &rec.TenantID, &rec.SiteID, &rec.IdempotencyKey, &rec.RequestHash,
		&capability, &source, &deliveryMode, &rec.TTLSeconds, &rec.ExpiresAt,
		&rec.TargetCount, &rec.AcceptedCount, &rec.RejectedCount, &rec.Status,
		&rec.InitiatedByPrincipalID, &rec.TraceID, &rec.Reason, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommandBatchView{}, ErrCommandBatchNotFound
	}
	if err != nil {
		return CommandBatchView{}, fmt.Errorf("read command batch: %w", err)
	}
	rec.Capability = commandmodel.Capability(capability)
	rec.CommandSource = commandmodel.CommandSource(source)
	rec.DeliveryMode = commandmodel.NormalizeDeliveryMode(commandmodel.DeliveryMode(deliveryMode))

	rows, err := tx.Query(ctx, `
SELECT bi.item_index, bi.device_id::text, bi.point_id::text, bi.command_id::text,
       bi.admitted, bi.rejection_code, bi.rejection_reason,
       COALESCE(i.status, bi.status) AS live_status,
       i.expires_at, i.next_dispatch_after,
       GREATEST(bi.updated_at, COALESCE(i.updated_at, bi.updated_at)) AS live_updated_at
FROM command_runtime.batch_items bi
LEFT JOIN command_runtime.command_intents i
  ON i.tenant_id = bi.tenant_id AND i.command_id = bi.command_id
WHERE bi.tenant_id = $1::uuid AND bi.batch_id = $2::uuid
ORDER BY bi.item_index
`, tenantID, batchID)
	if err != nil {
		return CommandBatchView{}, fmt.Errorf("read command batch items: %w", err)
	}
	defer rows.Close()

	items := make([]CommandBatchItemView, 0, rec.TargetCount)
	latestUpdated := rec.UpdatedAt
	for rows.Next() {
		var itemRec CommandBatchItemRecord
		var expAt, nextAt *time.Time
		var liveUpdated time.Time
		if scanErr := rows.Scan(
			&itemRec.ItemIndex, &itemRec.DeviceID, &itemRec.PointID, &itemRec.CommandID,
			&itemRec.Admitted, &itemRec.RejectionCode, &itemRec.RejectionReason,
			&itemRec.Status, &expAt, &nextAt, &liveUpdated,
		); scanErr != nil {
			return CommandBatchView{}, fmt.Errorf("scan command batch item: %w", scanErr)
		}
		if liveUpdated.After(latestUpdated) {
			latestUpdated = liveUpdated
		}
		items = append(items, commandBatchItemViewFromRecord(itemRec, expAt, nextAt))
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return CommandBatchView{}, fmt.Errorf("iterate command batch items: %w", rowsErr)
	}

	accepted, rejected, liveStatus := aggregateBatchStatus(items, rec.AcceptedCount, rec.RejectedCount)
	if len(items) == 0 && rec.Status != "" {
		liveStatus = rec.Status
	}

	return CommandBatchView{
		ID:                     rec.ID,
		TenantID:               rec.TenantID,
		SiteID:                 rec.SiteID,
		IdempotencyKey:         rec.IdempotencyKey,
		Capability:             rec.Capability,
		CommandSource:          rec.CommandSource,
		DeliveryMode:           rec.DeliveryMode,
		TTLSeconds:             cloneIntPtr(rec.TTLSeconds),
		ExpiresAt:              formatTimePtr(rec.ExpiresAt),
		TargetCount:            rec.TargetCount,
		AcceptedCount:          accepted,
		RejectedCount:          rejected,
		Status:                 liveStatus,
		InitiatedByPrincipalID: rec.InitiatedByPrincipalID,
		TraceID:                rec.TraceID,
		Reason:                 rec.Reason,
		CreatedAt:              rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              latestUpdated.UTC().Format(time.RFC3339Nano),
		Items:                  items,
	}, nil
}
