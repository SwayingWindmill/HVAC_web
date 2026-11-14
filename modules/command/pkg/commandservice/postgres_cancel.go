package commandservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// Cancel transitions an AWAITING_APPROVAL or QUEUED command to CANCELLED and retires any pending outbox row.
func (store *PostgresStore) Cancel(ctx context.Context, request commandmodel.CancelRequest) (commandmodel.CommandIntent, error) {
	if store == nil || store.pool == nil {
		return commandmodel.CommandIntent{}, errors.New("command store is closed")
	}
	for attempt := 0; attempt < 4; attempt++ {
		intent, err := store.cancelOnce(ctx, request)
		if err == nil || errors.Is(err, ErrCancelInvalid) || errors.Is(err, ErrAuthorizationDenied) || errors.Is(err, ErrCommandNotFound) {
			return intent, err
		}
		if !isRetryablePostgresTransaction(err) {
			return commandmodel.CommandIntent{}, err
		}
	}
	return commandmodel.CommandIntent{}, errors.New("command cancel transaction retry limit exceeded")
}

// CancelPending cancels all AWAITING_APPROVAL or QUEUED commands for a site (and optional device IDs).
func (store *PostgresStore) CancelPending(ctx context.Context, request commandmodel.CancelPendingRequest) ([]string, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("command store is closed")
	}
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.SiteID) == "" || strings.TrimSpace(request.PrincipalID) == "" {
		return nil, ErrInvalidRequest
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "CONTROL_EXIT_CANCELLED"
	}
	causationID := strings.TrimSpace(request.CausationID)
	if causationID == "" {
		causationID = reason
	}
	for attempt := 0; attempt < 4; attempt++ {
		ids, err := store.cancelPendingOnce(ctx, request, reason, causationID)
		if err == nil {
			return ids, nil
		}
		if !isRetryablePostgresTransaction(err) {
			return nil, err
		}
	}
	return nil, errors.New("command bulk cancel transaction retry limit exceeded")
}

func (store *PostgresStore) cancelOnce(ctx context.Context, request commandmodel.CancelRequest) (commandmodel.CommandIntent, error) {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("begin command cancel transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, request.TenantID); err != nil {
		return commandmodel.CommandIntent{}, err
	}
	var lockedCommandID string
	if err := tx.QueryRow(ctx, `
SELECT command_id::text
FROM command_runtime.command_intents
WHERE tenant_id = $1::uuid AND command_id = $2::uuid
FOR UPDATE
`, request.TenantID, request.CommandID).Scan(&lockedCommandID); errors.Is(err, pgx.ErrNoRows) {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	} else if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("lock command for cancel: %w", err)
	}
	intent, err := loadIntent(ctx, tx, request.TenantID, request.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	decision, err := DecideCommandCancel(intent, request, now)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if err := store.applyCancelledIntent(ctx, tx, intent, decision.Reason, request.Authorization.PrincipalID, request.Authorization.GrantID, decision.RetireOutboxRow, now); err != nil {
		return commandmodel.CommandIntent{}, err
	}
	updated, err := loadIntent(ctx, tx, request.TenantID, request.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("commit command cancel: %w", err)
	}
	return updated, nil
}

func (store *PostgresStore) cancelPendingOnce(ctx context.Context, request commandmodel.CancelPendingRequest, reason, causationID string) ([]string, error) {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin bulk command cancel transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, request.TenantID); err != nil {
		return nil, err
	}
	var rows pgx.Rows
	if len(request.DeviceIDs) > 0 {
		rows, err = tx.Query(ctx, `
SELECT command_id::text
FROM command_runtime.command_intents
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND device_id = ANY($3::uuid[])
  AND status IN ('AWAITING_APPROVAL', 'QUEUED')
ORDER BY device_command_sequence ASC, command_id ASC
FOR UPDATE
`, request.TenantID, request.SiteID, request.DeviceIDs)
	} else {
		rows, err = tx.Query(ctx, `
SELECT command_id::text
FROM command_runtime.command_intents
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND status IN ('AWAITING_APPROVAL', 'QUEUED')
ORDER BY device_command_sequence ASC, command_id ASC
FOR UPDATE
`, request.TenantID, request.SiteID)
	}
	if err != nil {
		return nil, fmt.Errorf("query pending commands for cancel: %w", err)
	}
	var commandIDs []string
	for rows.Next() {
		var commandID string
		if err := rows.Scan(&commandID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending command id: %w", err)
		}
		commandIDs = append(commandIDs, commandID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate pending commands: %w", err)
	}
	rows.Close()

	for _, commandID := range commandIDs {
		intent, err := loadIntent(ctx, tx, request.TenantID, commandID)
		if err != nil {
			return nil, err
		}
		if intent.Status != commandmodel.IntentAwaitingApproval && intent.Status != commandmodel.IntentQueued {
			continue
		}
		if err := store.applyCancelledIntent(ctx, tx, intent, reason, request.PrincipalID, causationID, true, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit bulk command cancel: %w", err)
	}
	return commandIDs, nil
}

func (store *PostgresStore) applyCancelledIntent(ctx context.Context, tx pgx.Tx, intent commandmodel.CommandIntent, reason, principalID, causationID string, retireOutbox bool, now time.Time) error {
	transitionID, err := store.newID(now)
	if err != nil {
		return fmt.Errorf("allocate cancel transition identifier: %w", err)
	}
	auditID, err := store.newID(now)
	if err != nil {
		return fmt.Errorf("allocate cancel audit identifier: %w", err)
	}
	version := intent.Version + 1
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_transitions (
  transition_id, command_id, tenant_id, site_id, device_id, command_version,
  from_status, to_status, reason, actor_type, actor_id, causation_id, evidence_id, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, 'PRINCIPAL', $10, $11, $11, $12)
`, transitionID, intent.ID, intent.TenantID, intent.SiteID, intent.DeviceID, version,
		intent.Status, commandmodel.IntentCancelled, reason, principalID, causationID, now); err != nil {
		return fmt.Errorf("insert cancel transition: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = $3, version = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND command_id = $2::uuid
`, intent.TenantID, intent.ID, commandmodel.IntentCancelled, version, now); err != nil {
		return fmt.Errorf("update cancelled command intent: %w", err)
	}
	if retireOutbox {
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_dispatch_outbox
SET delivered_at = $3, lease_until = NULL
WHERE tenant_id = $1::uuid AND command_id = $2::uuid AND delivered_at IS NULL
`, intent.TenantID, intent.ID, now); err != nil {
			return fmt.Errorf("retire cancelled dispatch outbox: %w", err)
		}
	}
	redactedPayload, _ := json.Marshal(map[string]any{"reason": reason, "principalId": principalID})
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_audit_intents (
  audit_intent_id, command_id, tenant_id, site_id, device_id,
  event_kind, payload_hash, redacted_payload, created_at, relayed_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'COMMAND_CANCELLED', $6, $7::jsonb, $8, NULL)
`, auditID, intent.ID, intent.TenantID, intent.SiteID, intent.DeviceID,
		intent.PayloadHash, redactedPayload, now); err != nil {
		return fmt.Errorf("insert cancel audit intent: %w", err)
	}
	return nil
}
