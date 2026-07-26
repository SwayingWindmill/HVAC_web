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

const setpointControlGroup = "SETPOINT"

var ErrNoDispatchAvailable = errors.New("no governed command is available for dispatch")

func (store *PostgresStore) ClaimDispatch(ctx context.Context, organizationID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	return store.claimDispatch(ctx, organizationID, unrestrictedCommandCohort(), leaseOwner, leaseFor)
}

func (store *PostgresStore) ClaimDispatchForCohort(ctx context.Context, organizationID, siteID, deviceID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	scope, err := exactCommandCohort(siteID, deviceID)
	if err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	return store.claimDispatch(ctx, organizationID, scope, leaseOwner, leaseFor)
}

func (store *PostgresStore) claimDispatch(ctx context.Context, organizationID string, scope commandCohortScope, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	if store == nil || store.pool == nil {
		return commandmodel.DispatchEnvelope{}, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(organizationID) || strings.TrimSpace(leaseOwner) == "" || leaseFor < time.Second || leaseFor > 2*time.Minute {
		return commandmodel.DispatchEnvelope{}, ErrInvalidRequest
	}
	for attempt := 0; attempt < 4; attempt++ {
		envelope, err := store.claimDispatchOnce(ctx, organizationID, scope, leaseOwner, leaseFor)
		if err == nil || errors.Is(err, ErrNoDispatchAvailable) {
			return envelope, err
		}
		if !isRetryablePostgresTransaction(err) {
			return commandmodel.DispatchEnvelope{}, err
		}
	}
	return commandmodel.DispatchEnvelope{}, errors.New("dispatch claim transaction retry limit exceeded")
}

func (store *PostgresStore) claimDispatchOnce(ctx context.Context, organizationID string, scope commandCohortScope, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("begin dispatch claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, organizationID); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	if err := store.reconcileExpiredPreparedAttempts(ctx, tx, organizationID, scope, now); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	if err := store.reconcileExpiredAcknowledgedAttempts(ctx, tx, organizationID, scope, now); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	if err := store.expireGovernanceInvalidQueued(ctx, tx, organizationID, scope, now); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}

	var outboxID string
	var intent commandmodel.CommandIntent
	var capability string
	err = tx.QueryRow(ctx, `
SELECT o.outbox_id::text,
       i.command_id::text, i.organization_id::text, i.site_id::text, i.device_id::text,
       i.capability_name, i.capability_revision,
       (i.canonical_parameters ->> 'setpointC')::double precision,
       i.payload_hash, i.device_command_sequence, i.version
FROM command_runtime.command_dispatch_outbox o
JOIN command_runtime.command_intents i ON i.command_id = o.command_id
JOIN command_runtime.device_control_state d
  ON d.organization_id = i.organization_id AND d.device_id = i.device_id
WHERE o.organization_id = $1::uuid
  AND (NOT $4 OR (i.site_id = $5::uuid AND i.device_id = $6::uuid))
  AND o.delivered_at IS NULL
  AND o.available_at <= $2
  AND (o.lease_until IS NULL OR o.lease_until <= $2)
  AND i.status = 'QUEUED'
  AND NOT (d.frozen_control_groups ? $3)
  AND (
        (
          EXISTS (SELECT 1 FROM command_runtime.command_approval_snapshots a WHERE a.command_id = i.command_id)
          AND NOT EXISTS (
            SELECT 1 FROM command_runtime.command_approval_snapshots a
            WHERE a.command_id = i.command_id
              AND (a.expires_at <= $2 OR a.authorization_expires_at <= $2 OR a.authorization_purpose <> 'COMMAND_APPROVE')
          )
        )
        OR
        (
          NOT EXISTS (SELECT 1 FROM command_runtime.command_approval_snapshots a WHERE a.command_id = i.command_id)
          AND EXISTS (
            SELECT 1 FROM command_runtime.command_authorization_snapshots s
            WHERE s.command_id = i.command_id
              AND s.expires_at > $2
              AND s.authorization_purpose = 'COMMAND_SUBMIT'
          )
        )
      )
  AND NOT EXISTS (
        SELECT 1 FROM command_runtime.command_intents earlier
        WHERE earlier.organization_id = i.organization_id
          AND earlier.device_id = i.device_id
          AND earlier.device_command_sequence < i.device_command_sequence
          AND earlier.status IN ('SUBMITTED','VALIDATING','AWAITING_APPROVAL','APPROVED','QUEUED','DISPATCHING','OUTCOME_UNKNOWN')
      )
ORDER BY o.created_at, o.outbox_id
FOR UPDATE OF o SKIP LOCKED
LIMIT 1
`, organizationID, now, setpointControlGroup, scope.enforced, scope.querySiteID(), scope.queryDeviceID()).Scan(
		&outboxID, &intent.ID, &intent.OrganizationID, &intent.SiteID, &intent.DeviceID,
		&capability, &intent.CapabilityRevision, &intent.SetpointC,
		&intent.PayloadHash, &intent.DeviceCommandSequence, &intent.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return commandmodel.DispatchEnvelope{}, fmt.Errorf("commit empty dispatch claim: %w", err)
		}
		return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
	}
	if err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("select governed dispatch outbox: %w", err)
	}
	intent.Capability = commandmodel.Capability(capability)

	if _, err := tx.Exec(ctx, `
SELECT command_id FROM command_runtime.command_intents
WHERE organization_id = $1::uuid AND command_id = $2::uuid
FOR UPDATE
`, organizationID, intent.ID); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("lock command intent for dispatch: %w", err)
	}
	var executionFence uint64
	if err := tx.QueryRow(ctx, `
UPDATE command_runtime.device_control_state
SET active_execution_fence = active_execution_fence + 1, updated_at = $3
WHERE organization_id = $1::uuid AND device_id = $2::uuid
RETURNING active_execution_fence
`, organizationID, intent.DeviceID, now).Scan(&executionFence); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("advance device execution fence: %w", err)
	}
	var attemptNumber int
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(attempt_number), 0) + 1
FROM command_runtime.command_attempts
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, organizationID, intent.ID).Scan(&attemptNumber); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("allocate command attempt number: %w", err)
	}
	attemptID, err := store.newID(now)
	if err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("allocate command attempt identifier: %w", err)
	}
	transitionID, err := store.newID(now)
	if err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("allocate dispatch transition identifier: %w", err)
	}
	auditID, err := store.newID(now)
	if err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("allocate dispatch audit identifier: %w", err)
	}
	leaseUntil := now.Add(leaseFor)
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_attempts (
  attempt_id, command_id, organization_id, site_id, device_id, attempt_number,
  status, version, execution_fence, payload_hash, lease_owner, lease_until,
  connector_evidence_id, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6,
  'PREPARED', 1, $7, $8, $9, $10,
  NULL, $11, $11
)
`, attemptID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		attemptNumber, executionFence, intent.PayloadHash, leaseOwner, leaseUntil, now); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("insert prepared command attempt: %w", err)
	}
	newVersion := intent.Version + 1
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = 'DISPATCHING', version = $3, active_execution_fence = $4, updated_at = $5
WHERE organization_id = $1::uuid AND command_id = $2::uuid AND status = 'QUEUED'
`, organizationID, intent.ID, newVersion, executionFence, now); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("mark command dispatching: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_dispatch_outbox
SET lease_owner = $3, lease_until = $4, attempt_count = attempt_count + 1
WHERE organization_id = $1::uuid AND outbox_id = $2::uuid AND delivered_at IS NULL
`, organizationID, outboxID, leaseOwner, leaseUntil); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("lease command dispatch outbox: %w", err)
	}
	if err := insertDispatchTransition(ctx, tx, transitionID, intent, newVersion,
		commandmodel.IntentQueued, commandmodel.IntentDispatching,
		"DISPATCH_ATTEMPT_PREPARED", leaseOwner, attemptID, "", now); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_DISPATCH_PREPARED", map[string]any{
		"attemptId": attemptID, "leaseOwner": leaseOwner, "leaseUntil": leaseUntil,
		"executionFence": executionFence, "attemptNumber": attemptNumber,
	}, now); err != nil {
		return commandmodel.DispatchEnvelope{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.DispatchEnvelope{}, fmt.Errorf("commit dispatch claim: %w", err)
	}
	return commandmodel.DispatchEnvelope{
		CommandID: intent.ID, AttemptID: attemptID, OrganizationID: intent.OrganizationID,
		SiteID: intent.SiteID, DeviceID: intent.DeviceID, Capability: intent.Capability,
		CapabilityRevision: intent.CapabilityRevision, SetpointC: intent.SetpointC,
		PayloadHash: intent.PayloadHash, ExecutionFence: executionFence,
		DeviceCommandSequence: intent.DeviceCommandSequence, LeaseOwner: leaseOwner, LeaseUntil: leaseUntil,
	}, nil
}

func (store *PostgresStore) ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	if store == nil || store.pool == nil {
		return errors.New("command store is closed")
	}
	if envelope.OrganizationID == "" || envelope.CommandID == "" || envelope.AttemptID == "" ||
		strings.TrimSpace(envelope.LeaseOwner) == "" || envelope.ExecutionFence == 0 {
		return ErrInvalidRequest
	}
	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected, commandmodel.ConnectorRequestCommitted:
	case commandmodel.ConnectorAcknowledged:
		if !result.Acknowledged || result.Verified || strings.TrimSpace(result.EvidenceID) == "" {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	for attempt := 0; attempt < 4; attempt++ {
		err := store.resolveDispatchOnce(ctx, envelope, result)
		if err == nil || errors.Is(err, ErrStaleFence) || errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrCommandNotFound) {
			return err
		}
		if !isRetryablePostgresTransaction(err) {
			return err
		}
	}
	return errors.New("dispatch resolution transaction retry limit exceeded")
}

func (store *PostgresStore) resolveDispatchOnce(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin dispatch resolution transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, envelope.OrganizationID); err != nil {
		return err
	}
	var intent commandmodel.CommandIntent
	var attemptStatus string
	var attemptVersion uint64
	var activeFence uint64
	var attemptFence uint64
	var attemptPayloadHash, leaseOwner string
	var leaseUntil time.Time
	var outboxID string
	err = tx.QueryRow(ctx, `
SELECT i.command_id::text, i.organization_id::text, i.site_id::text, i.device_id::text,
       i.payload_hash, i.version, i.active_execution_fence, i.status,
       a.status, a.version, a.execution_fence, a.payload_hash, a.lease_owner, a.lease_until,
       o.outbox_id::text
FROM command_runtime.command_intents i
JOIN command_runtime.command_attempts a ON a.command_id = i.command_id
JOIN command_runtime.command_dispatch_outbox o
  ON o.command_id = i.command_id AND o.lease_owner = a.lease_owner
WHERE i.organization_id = $1::uuid AND i.command_id = $2::uuid AND a.attempt_id = $3::uuid
FOR UPDATE OF i, a, o
`, envelope.OrganizationID, envelope.CommandID, envelope.AttemptID).Scan(
		&intent.ID, &intent.OrganizationID, &intent.SiteID, &intent.DeviceID,
		&intent.PayloadHash, &intent.Version, &activeFence, &intent.Status,
		&attemptStatus, &attemptVersion, &attemptFence, &attemptPayloadHash, &leaseOwner, &leaseUntil,
		&outboxID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCommandNotFound
	}
	if err != nil {
		return fmt.Errorf("lock dispatch resolution state: %w", err)
	}
	var deviceFence uint64
	if err := tx.QueryRow(ctx, `
SELECT active_execution_fence
FROM command_runtime.device_control_state
WHERE organization_id = $1::uuid AND device_id = $2::uuid
FOR UPDATE
`, envelope.OrganizationID, intent.DeviceID).Scan(&deviceFence); err != nil {
		return fmt.Errorf("lock device fence for resolution: %w", err)
	}
	if attemptStatus != string(commandmodel.AttemptPrepared) || intent.Status != commandmodel.IntentDispatching ||
		attemptFence != envelope.ExecutionFence || activeFence != envelope.ExecutionFence || deviceFence != envelope.ExecutionFence ||
		attemptPayloadHash != envelope.PayloadHash || intent.PayloadHash != envelope.PayloadHash || leaseOwner != envelope.LeaseOwner {
		return ErrStaleFence
	}
	if result.Phase != commandmodel.ConnectorPreSendRejected && strings.TrimSpace(result.EvidenceID) == "" {
		return ErrInvalidRequest
	}

	attemptFinal := commandmodel.AttemptOutcomeUnknown
	intentFinal := commandmodel.IntentOutcomeUnknown
	reason := "REQUEST_COMMITTED_OUTCOME_UNKNOWN"
	freeze := true
	retry := false
	var acknowledgedAt *time.Time
	var verificationDeadline *time.Time
	switch result.Phase {
	case commandmodel.ConnectorPreSendRejected:
		attemptFinal = commandmodel.AttemptNotSent
		intentFinal = commandmodel.IntentQueued
		reason = "PROVABLY_NOT_SENT_REQUEUE"
		freeze = false
		retry = true
	case commandmodel.ConnectorRequestCommitted:
		attemptFinal = commandmodel.AttemptOutcomeUnknown
		intentFinal = commandmodel.IntentOutcomeUnknown
		reason = "REQUEST_COMMITTED_OUTCOME_UNKNOWN"
	case commandmodel.ConnectorAcknowledged:
		attemptFinal = commandmodel.AttemptAcknowledged
		intentFinal = commandmodel.IntentDispatching
		reason = "PROVIDER_ACKNOWLEDGED_AWAITING_REPORTED_STATE"
		freeze = false
		ackAt := now
		deadline := now.Add(verificationWindow)
		acknowledgedAt = &ackAt
		verificationDeadline = &deadline
	default:
		return ErrInvalidRequest
	}

	transitionID, err := store.newID(now)
	if err != nil {
		return fmt.Errorf("allocate dispatch resolution transition: %w", err)
	}
	auditID, err := store.newID(now)
	if err != nil {
		return fmt.Errorf("allocate dispatch resolution audit: %w", err)
	}
	newVersion := intent.Version + 1
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_attempts
SET status = $4, version = $5, connector_evidence_id = NULLIF($6, ''),
    acknowledged_at = $7, verification_deadline = $8,
    verification_lease_owner = NULL, verification_lease_until = NULL,
    verification_evidence_id = NULL, updated_at = $9
WHERE organization_id = $1::uuid AND command_id = $2::uuid AND attempt_id = $3::uuid
`, envelope.OrganizationID, envelope.CommandID, envelope.AttemptID,
		attemptFinal, attemptVersion+1, result.EvidenceID, acknowledgedAt, verificationDeadline, now); err != nil {
		return fmt.Errorf("resolve command attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = $3, version = $4, updated_at = $5
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, envelope.OrganizationID, envelope.CommandID, intentFinal, newVersion, now); err != nil {
		return fmt.Errorf("resolve command intent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_dispatch_outbox
SET delivered_at = $3, lease_until = NULL
WHERE organization_id = $1::uuid AND outbox_id = $2::uuid
`, envelope.OrganizationID, outboxID, now); err != nil {
		return fmt.Errorf("complete command dispatch outbox: %w", err)
	}
	if freeze {
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET frozen_control_groups = CASE
      WHEN frozen_control_groups ? $3 THEN frozen_control_groups
      ELSE frozen_control_groups || to_jsonb($3::text)
    END,
    updated_at = $4
WHERE organization_id = $1::uuid AND device_id = $2::uuid
`, envelope.OrganizationID, intent.DeviceID, setpointControlGroup, now); err != nil {
			return fmt.Errorf("freeze unknown command control group: %w", err)
		}
	}
	if err := insertDispatchTransition(ctx, tx, transitionID, intent, newVersion,
		commandmodel.IntentDispatching, intentFinal, reason, envelope.LeaseOwner,
		envelope.AttemptID, result.EvidenceID, now); err != nil {
		return err
	}
	if retry {
		newOutboxID, err := store.newID(now)
		if err != nil {
			return fmt.Errorf("allocate pre-send retry outbox identifier: %w", err)
		}
		payload, _ := json.Marshal(map[string]any{
			"schemaVersion": 1, "commandId": intent.ID, "organizationId": intent.OrganizationID,
			"siteId": intent.SiteID, "deviceId": intent.DeviceID, "payloadHash": intent.PayloadHash,
			"retryReason": "PROVABLY_NOT_SENT", "previousAttemptId": envelope.AttemptID,
		})
		if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_dispatch_outbox (
  outbox_id, command_id, organization_id, site_id, device_id, command_version,
  available_at, lease_owner, lease_until, delivered_at, attempt_count, payload, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, NULL, NULL, NULL, 0, $8::jsonb, $7)
`, newOutboxID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
			newVersion, now, payload); err != nil {
			return fmt.Errorf("insert safe pre-send retry outbox: %w", err)
		}
	}
	if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_DISPATCH_RESOLVED", map[string]any{
		"attemptId": envelope.AttemptID, "executionFence": envelope.ExecutionFence,
		"attemptStatus": attemptFinal, "intentStatus": intentFinal, "reason": reason,
		"evidenceId": result.EvidenceID,
	}, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit dispatch resolution: %w", err)
	}
	return nil
}

func (store *PostgresStore) reconcileExpiredPreparedAttempts(ctx context.Context, tx pgx.Tx, organizationID string, scope commandCohortScope, now time.Time) error {
	type expiredAttempt struct {
		AttemptID, CommandID, SiteID, DeviceID, PayloadHash, LeaseOwner, OutboxID string
		Fence, IntentVersion, AttemptVersion                                      uint64
	}
	rows, err := tx.Query(ctx, `
SELECT a.attempt_id::text, a.command_id::text, a.site_id::text, a.device_id::text,
       a.payload_hash, a.lease_owner, o.outbox_id::text,
       a.execution_fence, i.version, a.version
FROM command_runtime.command_attempts a
JOIN command_runtime.command_intents i ON i.command_id = a.command_id
JOIN command_runtime.command_dispatch_outbox o
  ON o.command_id = a.command_id AND o.delivered_at IS NULL AND o.lease_owner = a.lease_owner
WHERE a.organization_id = $1::uuid
  AND (NOT $3 OR (a.site_id = $4::uuid AND a.device_id = $5::uuid))
  AND a.status = 'PREPARED'
  AND a.lease_until <= $2
  AND i.status = 'DISPATCHING'
FOR UPDATE OF a, i, o SKIP LOCKED
LIMIT 50
`, organizationID, now, scope.enforced, scope.querySiteID(), scope.queryDeviceID())
	if err != nil {
		return fmt.Errorf("select expired prepared attempts: %w", err)
	}
	expired := make([]expiredAttempt, 0)
	for rows.Next() {
		var item expiredAttempt
		if err := rows.Scan(&item.AttemptID, &item.CommandID, &item.SiteID, &item.DeviceID,
			&item.PayloadHash, &item.LeaseOwner, &item.OutboxID,
			&item.Fence, &item.IntentVersion, &item.AttemptVersion); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired prepared attempt: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired prepared attempts: %w", err)
	}
	rows.Close()
	for _, item := range expired {
		transitionID, err := store.newID(now)
		if err != nil {
			return err
		}
		auditID, err := store.newID(now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_attempts
SET status = 'OUTCOME_UNKNOWN', version = $4,
    connector_evidence_id = 'LEASE_EXPIRED_WITHOUT_SEND_PROOF', updated_at = $5
WHERE organization_id = $1::uuid AND command_id = $2::uuid AND attempt_id = $3::uuid
`, organizationID, item.CommandID, item.AttemptID, item.AttemptVersion+1, now); err != nil {
			return fmt.Errorf("freeze expired command attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = 'OUTCOME_UNKNOWN', version = $3, updated_at = $4
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, organizationID, item.CommandID, item.IntentVersion+1, now); err != nil {
			return fmt.Errorf("freeze expired command intent: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_dispatch_outbox
SET delivered_at = $3, lease_until = NULL
WHERE organization_id = $1::uuid AND outbox_id = $2::uuid
`, organizationID, item.OutboxID, now); err != nil {
			return fmt.Errorf("complete expired dispatch outbox: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET frozen_control_groups = CASE
      WHEN frozen_control_groups ? $3 THEN frozen_control_groups
      ELSE frozen_control_groups || to_jsonb($3::text)
    END,
    updated_at = $4
WHERE organization_id = $1::uuid AND device_id = $2::uuid
`, organizationID, item.DeviceID, setpointControlGroup, now); err != nil {
			return fmt.Errorf("freeze expired command control group: %w", err)
		}
		intent := commandmodel.CommandIntent{ID: item.CommandID, OrganizationID: organizationID, SiteID: item.SiteID, DeviceID: item.DeviceID, PayloadHash: item.PayloadHash, Version: item.IntentVersion}
		if err := insertDispatchTransition(ctx, tx, transitionID, intent, item.IntentVersion+1,
			commandmodel.IntentDispatching, commandmodel.IntentOutcomeUnknown,
			"LEASE_EXPIRED_WITHOUT_SEND_PROOF", item.LeaseOwner, item.AttemptID,
			"LEASE_EXPIRED_WITHOUT_SEND_PROOF", now); err != nil {
			return err
		}
		if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_DISPATCH_LEASE_EXPIRED", map[string]any{
			"attemptId": item.AttemptID, "executionFence": item.Fence,
			"reason": "LEASE_EXPIRED_WITHOUT_SEND_PROOF",
		}, now); err != nil {
			return err
		}
	}
	return nil
}

func (store *PostgresStore) expireGovernanceInvalidQueued(ctx context.Context, tx pgx.Tx, organizationID string, scope commandCohortScope, now time.Time) error {
	type expiredCommand struct {
		CommandID, SiteID, DeviceID, PayloadHash, OutboxID string
		Version                                            uint64
	}
	rows, err := tx.Query(ctx, `
SELECT i.command_id::text, i.site_id::text, i.device_id::text, i.payload_hash,
       o.outbox_id::text, i.version
FROM command_runtime.command_intents i
JOIN command_runtime.command_dispatch_outbox o ON o.command_id = i.command_id AND o.delivered_at IS NULL
WHERE i.organization_id = $1::uuid AND i.status = 'QUEUED'
  AND (NOT $3 OR (i.site_id = $4::uuid AND i.device_id = $5::uuid))
  AND (
    EXISTS (
      SELECT 1 FROM command_runtime.command_approval_snapshots a
      WHERE a.command_id = i.command_id
        AND (a.expires_at <= $2 OR a.authorization_expires_at <= $2 OR a.authorization_purpose <> 'COMMAND_APPROVE')
    )
    OR (
      NOT EXISTS (SELECT 1 FROM command_runtime.command_approval_snapshots a WHERE a.command_id = i.command_id)
      AND NOT EXISTS (
        SELECT 1 FROM command_runtime.command_authorization_snapshots s
        WHERE s.command_id = i.command_id
          AND s.expires_at > $2
          AND s.authorization_purpose = 'COMMAND_SUBMIT'
      )
    )
  )
FOR UPDATE OF i, o SKIP LOCKED
LIMIT 50
`, organizationID, now, scope.enforced, scope.querySiteID(), scope.queryDeviceID())
	if err != nil {
		return fmt.Errorf("select governance-expired queued commands: %w", err)
	}
	expired := make([]expiredCommand, 0)
	for rows.Next() {
		var item expiredCommand
		if err := rows.Scan(&item.CommandID, &item.SiteID, &item.DeviceID, &item.PayloadHash, &item.OutboxID, &item.Version); err != nil {
			rows.Close()
			return fmt.Errorf("scan governance-expired command: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate governance-expired commands: %w", err)
	}
	rows.Close()
	for _, item := range expired {
		transitionID, err := store.newID(now)
		if err != nil {
			return err
		}
		auditID, err := store.newID(now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = 'EXPIRED', version = $3, updated_at = $4
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, organizationID, item.CommandID, item.Version+1, now); err != nil {
			return fmt.Errorf("expire governance-invalid command: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_dispatch_outbox
SET delivered_at = $3, lease_until = NULL
WHERE organization_id = $1::uuid AND outbox_id = $2::uuid
`, organizationID, item.OutboxID, now); err != nil {
			return fmt.Errorf("complete governance-expired outbox: %w", err)
		}
		intent := commandmodel.CommandIntent{ID: item.CommandID, OrganizationID: organizationID, SiteID: item.SiteID, DeviceID: item.DeviceID, PayloadHash: item.PayloadHash, Version: item.Version}
		if err := insertDispatchTransition(ctx, tx, transitionID, intent, item.Version+1,
			commandmodel.IntentQueued, commandmodel.IntentExpired,
			"EXECUTION_AUTHORIZATION_OR_APPROVAL_EXPIRED", "command-service", item.CommandID, "", now); err != nil {
			return err
		}
		if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_EXECUTION_GOVERNANCE_EXPIRED", map[string]any{
			"reason": "EXECUTION_AUTHORIZATION_OR_APPROVAL_EXPIRED",
		}, now); err != nil {
			return err
		}
	}
	return nil
}

func insertDispatchTransition(ctx context.Context, tx pgx.Tx, transitionID string, intent commandmodel.CommandIntent, version uint64, from, to commandmodel.IntentStatus, reason, actor, causationID, evidenceID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_transitions (
  transition_id, command_id, organization_id, site_id, device_id, command_version,
  from_status, to_status, reason, actor_type, actor_id, causation_id, evidence_id, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, 'WORKLOAD', $10, $11, NULLIF($12, ''), $13)
`, transitionID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		version, from, to, reason, actor, causationID, evidenceID, now); err != nil {
		return fmt.Errorf("insert dispatch transition: %w", err)
	}
	return nil
}

func insertDispatchAudit(ctx context.Context, tx pgx.Tx, auditID string, intent commandmodel.CommandIntent, eventKind string, payload map[string]any, now time.Time) error {
	redacted, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_audit_intents (
  audit_intent_id, command_id, organization_id, site_id, device_id,
  event_kind, payload_hash, redacted_payload, created_at, relayed_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8::jsonb, $9, NULL)
`, auditID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		eventKind, intent.PayloadHash, redacted, now); err != nil {
		return fmt.Errorf("insert dispatch audit intent: %w", err)
	}
	return nil
}
