package commandservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

var ErrReconciliationInvalid = errors.New("command outcome reconciliation is invalid")

// Reconcile settles an OUTCOME_UNKNOWN command on a person's statement of whether it took
// effect (#444). The Attempt keeps OUTCOME_UNKNOWN as evidence; the Intent becomes SUCCEEDED
// or FAILED, and the Device's control group is released once no unknown outcome remains.
func (store *PostgresStore) Reconcile(ctx context.Context, request commandmodel.ReconcileRequest) (commandmodel.CommandIntent, error) {
	if store == nil || store.pool == nil {
		return commandmodel.CommandIntent{}, errors.New("command store is closed")
	}
	for attempt := 0; attempt < 4; attempt++ {
		intent, err := store.reconcileOnce(ctx, request)
		if err == nil || errors.Is(err, ErrReconciliationInvalid) || errors.Is(err, ErrAuthorizationDenied) || errors.Is(err, ErrCommandNotFound) {
			return intent, err
		}
		if !isRetryablePostgresTransaction(err) {
			return commandmodel.CommandIntent{}, err
		}
	}
	return commandmodel.CommandIntent{}, errors.New("command reconciliation transaction retry limit exceeded")
}

func (store *PostgresStore) reconcileOnce(ctx context.Context, request commandmodel.ReconcileRequest) (commandmodel.CommandIntent, error) {
	final, reason := commandmodel.IntentSucceeded, "OPERATOR_RECONCILED_APPLIED"
	switch request.Outcome {
	case commandmodel.ReconciledApplied:
	case commandmodel.ReconciledNotApplied:
		final, reason = commandmodel.IntentFailed, "OPERATOR_RECONCILED_NOT_APPLIED"
	default:
		return commandmodel.CommandIntent{}, ErrReconciliationInvalid
	}
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("begin command reconciliation transaction: %w", err)
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
		return commandmodel.CommandIntent{}, fmt.Errorf("lock command for reconciliation: %w", err)
	}
	intent, err := loadIntent(ctx, tx, request.TenantID, request.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if intent.Status != commandmodel.IntentOutcomeUnknown {
		return commandmodel.CommandIntent{}, ErrReconciliationInvalid
	}
	if err := validateAuthorizationScope(request.Authorization, commandmodel.AuthorizationCommandSubmit,
		request.Authorization.PrincipalID, intent.TenantID, intent.SiteID, intent.DeviceID,
		intent.Capability, intent.CapabilityRevision, intent.Risk, now); err != nil {
		return commandmodel.CommandIntent{}, err
	}

	transitionID, err := store.newID(now)
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("allocate reconciliation transition identifier: %w", err)
	}
	auditID, err := store.newID(now)
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("allocate reconciliation audit identifier: %w", err)
	}
	version := intent.Version + 1
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_transitions (
  transition_id, command_id, tenant_id, site_id, device_id, command_version,
  from_status, to_status, reason, actor_type, actor_id, causation_id, evidence_id, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, 'PRINCIPAL', $10, $11, $11, $12)
`, transitionID, intent.ID, intent.TenantID, intent.SiteID, intent.DeviceID, version,
		commandmodel.IntentOutcomeUnknown, final, reason, request.Authorization.PrincipalID,
		request.Authorization.GrantID, now); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("insert reconciliation transition: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = $3, version = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND command_id = $2::uuid
`, intent.TenantID, intent.ID, final, version, now); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("update reconciled command intent: %w", err)
	}
	redactedPayload, _ := json.Marshal(map[string]any{"outcome": request.Outcome, "principalId": request.Authorization.PrincipalID})
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_audit_intents (
  audit_intent_id, command_id, tenant_id, site_id, device_id,
  event_kind, payload_hash, redacted_payload, created_at, relayed_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'COMMAND_OUTCOME_RECONCILED', $6, $7::jsonb, $8, NULL)
`, auditID, intent.ID, intent.TenantID, intent.SiteID, intent.DeviceID,
		intent.PayloadHash, redactedPayload, now); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("insert reconciliation audit intent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET frozen_control_groups = frozen_control_groups - $3, updated_at = $4
WHERE tenant_id = $1::uuid AND device_id = $2::uuid
  AND NOT EXISTS (
    SELECT 1 FROM command_runtime.command_intents
    WHERE tenant_id = $1::uuid AND device_id = $2::uuid AND status = 'OUTCOME_UNKNOWN'
  )
`, intent.TenantID, intent.DeviceID, setpointControlGroup, now); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("release reconciled control group: %w", err)
	}
	updated, err := loadIntent(ctx, tx, request.TenantID, request.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("commit command reconciliation: %w", err)
	}
	return updated, nil
}
