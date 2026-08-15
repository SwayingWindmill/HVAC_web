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

func (store *PostgresStore) ClaimVerification(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	return store.claimVerification(ctx, tenantID, unrestrictedCommandCohort(), leaseOwner, leaseFor)
}

func (store *PostgresStore) ClaimVerificationForCohort(ctx context.Context, tenantID, siteID, deviceID string, capability commandmodel.Capability, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	scope, err := exactCommandCohort(siteID, deviceID, capability)
	if err != nil {
		return commandmodel.VerificationEnvelope{}, err
	}
	return store.claimVerification(ctx, tenantID, scope, leaseOwner, leaseFor)
}

func (store *PostgresStore) claimVerification(ctx context.Context, tenantID string, scope commandCohortScope, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	if store == nil || store.pool == nil {
		return commandmodel.VerificationEnvelope{}, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || strings.TrimSpace(leaseOwner) == "" || leaseFor < time.Second || leaseFor > time.Minute {
		return commandmodel.VerificationEnvelope{}, ErrInvalidRequest
	}
	for attempt := 0; attempt < 4; attempt++ {
		envelope, err := store.claimVerificationOnce(ctx, tenantID, scope, leaseOwner, leaseFor)
		if err == nil || errors.Is(err, ErrVerificationNotAvailable) {
			return envelope, err
		}
		if !isRetryablePostgresTransaction(err) {
			return commandmodel.VerificationEnvelope{}, err
		}
	}
	return commandmodel.VerificationEnvelope{}, errors.New("verification claim transaction retry limit exceeded")
}

func (store *PostgresStore) claimVerificationOnce(ctx context.Context, tenantID string, scope commandCohortScope, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandmodel.VerificationEnvelope{}, fmt.Errorf("begin verification claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return commandmodel.VerificationEnvelope{}, err
	}
	if err := store.reconcileExpiredAcknowledgedAttempts(ctx, tx, tenantID, scope, now); err != nil {
		return commandmodel.VerificationEnvelope{}, err
	}

	var envelope commandmodel.VerificationEnvelope
	var capability string
	var parameters []byte
	var attemptVersion uint64
	err = tx.QueryRow(ctx, `
SELECT i.command_id::text, a.attempt_id::text, i.tenant_id::text, i.site_id::text, i.device_id::text, i.point_id::text,
       i.capability_name, i.capability_revision, i.canonical_parameters, i.verification_point_key,
       i.payload_hash, a.execution_fence, i.snapshot_revision,
       a.acknowledged_at, a.verification_deadline, a.connector_evidence_id, a.version
FROM command_runtime.command_intents i
JOIN command_runtime.command_attempts a ON a.command_id = i.command_id
JOIN command_runtime.device_control_state d
  ON d.tenant_id = i.tenant_id AND d.device_id = i.device_id
WHERE i.tenant_id = $1::uuid
  AND (NOT $4 OR (i.site_id = $5::uuid AND i.device_id = $6::uuid AND i.capability_name = $7))
  AND i.status = 'DISPATCHING'
  AND a.status = 'ACKNOWLEDGED'
  AND a.execution_fence = i.active_execution_fence
  AND a.verification_deadline > $2
  AND (a.verification_lease_until IS NULL OR a.verification_lease_until <= $2)
  AND NOT (d.frozen_control_groups ? $3)
ORDER BY a.acknowledged_at, a.attempt_id
FOR UPDATE OF i, a SKIP LOCKED
LIMIT 1
`, tenantID, now, setpointControlGroup, scope.enforced, scope.querySiteID(), scope.queryDeviceID(), scope.queryCapability()).Scan(
		&envelope.CommandID, &envelope.AttemptID, &envelope.TenantID, &envelope.SiteID, &envelope.DeviceID, &envelope.PointID,
		&capability, &envelope.CapabilityRevision, &parameters, &envelope.VerificationPointKey,
		&envelope.PayloadHash, &envelope.ExecutionFence, &envelope.BaselineBusinessRevision,
		&envelope.AcknowledgedAt, &envelope.VerificationDeadline, &envelope.ConnectorEvidenceID, &attemptVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return commandmodel.VerificationEnvelope{}, fmt.Errorf("commit empty verification claim: %w", err)
		}
		return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
	}
	if err != nil {
		return commandmodel.VerificationEnvelope{}, fmt.Errorf("select acknowledged verification work: %w", err)
	}
	if err := json.Unmarshal(parameters, &envelope.Parameters); err != nil {
		return commandmodel.VerificationEnvelope{}, fmt.Errorf("decode verification command parameters: %w", err)
	}
	envelope.Capability = commandmodel.Capability(capability)
	leaseUntil := now.Add(leaseFor).UTC().Truncate(time.Microsecond)
	if envelope.VerificationDeadline.Before(leaseUntil) {
		leaseUntil = envelope.VerificationDeadline
	}
	if !leaseUntil.After(now) {
		return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_attempts
SET verification_lease_owner = $4, verification_lease_until = $5,
    version = $6, updated_at = $7
WHERE tenant_id = $1::uuid AND command_id = $2::uuid AND attempt_id = $3::uuid
  AND status = 'ACKNOWLEDGED'
`, tenantID, envelope.CommandID, envelope.AttemptID, leaseOwner, leaseUntil, attemptVersion+1, now); err != nil {
		return commandmodel.VerificationEnvelope{}, fmt.Errorf("lease reported-state verification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.VerificationEnvelope{}, fmt.Errorf("commit verification claim: %w", err)
	}
	envelope.LeaseOwner = leaseOwner
	envelope.LeaseUntil = leaseUntil
	return envelope, nil
}

func (store *PostgresStore) ResolveVerification(ctx context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	if store == nil || store.pool == nil {
		return errors.New("command store is closed")
	}
	if envelope.TenantID == "" || envelope.CommandID == "" || envelope.AttemptID == "" ||
		strings.TrimSpace(envelope.LeaseOwner) == "" || envelope.ExecutionFence == 0 || strings.TrimSpace(result.EvidenceID) == "" {
		return ErrInvalidRequest
	}
	switch result.Outcome {
	case commandmodel.VerificationSucceeded, commandmodel.VerificationInconclusive, commandmodel.VerificationMismatch, commandmodel.VerificationTimedOut:
	default:
		return ErrInvalidRequest
	}
	for attempt := 0; attempt < 4; attempt++ {
		err := store.resolveVerificationOnce(ctx, envelope, result)
		if err == nil || errors.Is(err, ErrStaleFence) || errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrCommandNotFound) {
			return err
		}
		if !isRetryablePostgresTransaction(err) {
			return err
		}
	}
	return errors.New("verification resolution transaction retry limit exceeded")
}

func (store *PostgresStore) resolveVerificationOnce(ctx context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin verification resolution transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, envelope.TenantID); err != nil {
		return err
	}

	var intent commandmodel.CommandIntent
	var capability, attemptStatus, attemptPayloadHash, connectorEvidenceID string
	var parameters []byte
	var activeFence, attemptFence, attemptVersion uint64
	var acknowledgedAt, verificationDeadline, verificationLeaseUntil time.Time
	var verificationLeaseOwner string
	err = tx.QueryRow(ctx, `
SELECT i.command_id::text, i.tenant_id::text, i.site_id::text, i.device_id::text, i.point_id::text,
       i.capability_name, i.capability_revision, i.canonical_parameters, i.verification_point_key,
       i.payload_hash, i.snapshot_revision, i.version, i.status, i.active_execution_fence,
       a.status, a.version, a.execution_fence, a.payload_hash, a.connector_evidence_id,
       a.acknowledged_at, a.verification_deadline, a.verification_lease_owner, a.verification_lease_until
FROM command_runtime.command_intents i
JOIN command_runtime.command_attempts a ON a.command_id = i.command_id
WHERE i.tenant_id = $1::uuid AND i.command_id = $2::uuid AND a.attempt_id = $3::uuid
FOR UPDATE OF i, a
`, envelope.TenantID, envelope.CommandID, envelope.AttemptID).Scan(
		&intent.ID, &intent.TenantID, &intent.SiteID, &intent.DeviceID, &intent.PointID,
		&capability, &intent.CapabilityRevision, &parameters, &intent.VerificationPointKey,
		&intent.PayloadHash, &intent.SnapshotRevision, &intent.Version, &intent.Status, &activeFence,
		&attemptStatus, &attemptVersion, &attemptFence, &attemptPayloadHash, &connectorEvidenceID,
		&acknowledgedAt, &verificationDeadline, &verificationLeaseOwner, &verificationLeaseUntil,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCommandNotFound
	}
	if err != nil {
		return fmt.Errorf("lock verification resolution state: %w", err)
	}
	if err := json.Unmarshal(parameters, &intent.Parameters); err != nil {
		return fmt.Errorf("decode verification resolution parameters: %w", err)
	}
	intent.Capability = commandmodel.Capability(capability)
	if intent.Status != commandmodel.IntentDispatching || attemptStatus != string(commandmodel.AttemptAcknowledged) ||
		activeFence != envelope.ExecutionFence || attemptFence != envelope.ExecutionFence ||
		intent.PointID != envelope.PointID || intent.PayloadHash != envelope.PayloadHash || attemptPayloadHash != envelope.PayloadHash ||
		verificationLeaseOwner != envelope.LeaseOwner || !verificationLeaseUntil.Equal(envelope.LeaseUntil) ||
		connectorEvidenceID != envelope.ConnectorEvidenceID || !acknowledgedAt.Equal(envelope.AcknowledgedAt) ||
		!verificationDeadline.Equal(envelope.VerificationDeadline) || intent.SnapshotRevision != envelope.BaselineBusinessRevision ||
		intent.VerificationPointKey != envelope.VerificationPointKey {
		return ErrStaleFence
	}

	verified := result.Outcome == commandmodel.VerificationSucceeded && !now.After(verificationDeadline) && validReportedState(intent, envelope, result.Reported)
	attemptFinal := commandmodel.AttemptOutcomeUnknown
	intentFinal := commandmodel.IntentOutcomeUnknown
	reason := "REPORTED_STATE_VERIFICATION_NOT_PROVEN"
	freeze := true
	if verified {
		attemptFinal = commandmodel.AttemptVerified
		intentFinal = commandmodel.IntentSucceeded
		reason = "ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED"
		freeze = false
	} else if now.After(verificationDeadline) || result.Outcome == commandmodel.VerificationTimedOut {
		reason = "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED"
	}

	transitionID, err := store.newID(now)
	if err != nil {
		return fmt.Errorf("allocate verification transition: %w", err)
	}
	auditID, err := store.newID(now)
	if err != nil {
		return fmt.Errorf("allocate verification audit: %w", err)
	}
	newVersion := intent.Version + 1
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_attempts
SET status = $4, version = $5, verification_evidence_id = $6,
    verification_lease_owner = NULL, verification_lease_until = NULL, updated_at = $7
WHERE tenant_id = $1::uuid AND command_id = $2::uuid AND attempt_id = $3::uuid
`, envelope.TenantID, envelope.CommandID, envelope.AttemptID, attemptFinal, attemptVersion+1, result.EvidenceID, now); err != nil {
		return fmt.Errorf("resolve reported-state verification attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = $3, version = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND command_id = $2::uuid
`, envelope.TenantID, envelope.CommandID, intentFinal, newVersion, now); err != nil {
		return fmt.Errorf("resolve reported-state verification intent: %w", err)
	}
	if freeze {
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET frozen_control_groups = CASE
      WHEN frozen_control_groups ? $3 THEN frozen_control_groups
      ELSE frozen_control_groups || to_jsonb($3::text)
    END,
    updated_at = $4
WHERE tenant_id = $1::uuid AND device_id = $2::uuid
`, envelope.TenantID, intent.DeviceID, setpointControlGroup, now); err != nil {
			return fmt.Errorf("freeze unverified command control group: %w", err)
		}
	}
	if err := insertDispatchTransition(ctx, tx, transitionID, intent, newVersion,
		commandmodel.IntentDispatching, intentFinal, reason, envelope.LeaseOwner,
		envelope.AttemptID, result.EvidenceID, now); err != nil {
		return err
	}
	if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_REPORTED_STATE_VERIFICATION_RESOLVED", map[string]any{
		"attemptId": envelope.AttemptID, "executionFence": envelope.ExecutionFence,
		"outcome": result.Outcome, "verified": verified, "reason": reason,
		"connectorEvidenceId": envelope.ConnectorEvidenceID, "verificationEvidenceId": result.EvidenceID,
		"baselineBusinessRevision": envelope.BaselineBusinessRevision, "reportedBusinessRevision": result.Reported.BusinessRevision,
	}, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verification resolution: %w", err)
	}
	return nil
}

func (store *PostgresStore) reconcileExpiredAcknowledgedAttempts(ctx context.Context, tx pgx.Tx, tenantID string, scope commandCohortScope, now time.Time) error {
	type expiredVerification struct {
		AttemptID, CommandID, SiteID, DeviceID, PayloadHash, ConnectorEvidenceID string
		Fence, IntentVersion, AttemptVersion                                     uint64
	}
	rows, err := tx.Query(ctx, `
SELECT a.attempt_id::text, a.command_id::text, a.site_id::text, a.device_id::text,
       a.payload_hash, a.connector_evidence_id, a.execution_fence, i.version, a.version
FROM command_runtime.command_attempts a
JOIN command_runtime.command_intents i ON i.command_id = a.command_id
WHERE a.tenant_id = $1::uuid
  AND (NOT $3 OR (a.site_id = $4::uuid AND a.device_id = $5::uuid))
  AND a.status = 'ACKNOWLEDGED'
  AND a.verification_deadline <= $2
  AND i.status = 'DISPATCHING'
FOR UPDATE OF a, i SKIP LOCKED
LIMIT 50
`, tenantID, now, scope.enforced, scope.querySiteID(), scope.queryDeviceID())
	if err != nil {
		return fmt.Errorf("select expired reported-state verifications: %w", err)
	}
	items := make([]expiredVerification, 0)
	for rows.Next() {
		var item expiredVerification
		if err := rows.Scan(&item.AttemptID, &item.CommandID, &item.SiteID, &item.DeviceID,
			&item.PayloadHash, &item.ConnectorEvidenceID, &item.Fence, &item.IntentVersion, &item.AttemptVersion); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired reported-state verification: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired reported-state verifications: %w", err)
	}
	rows.Close()
	for _, item := range items {
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
    verification_evidence_id = 'REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED',
    verification_lease_owner = NULL, verification_lease_until = NULL, updated_at = $5
WHERE tenant_id = $1::uuid AND command_id = $2::uuid AND attempt_id = $3::uuid
`, tenantID, item.CommandID, item.AttemptID, item.AttemptVersion+1, now); err != nil {
			return fmt.Errorf("expire reported-state verification attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = 'OUTCOME_UNKNOWN', version = $3, updated_at = $4
WHERE tenant_id = $1::uuid AND command_id = $2::uuid
`, tenantID, item.CommandID, item.IntentVersion+1, now); err != nil {
			return fmt.Errorf("expire reported-state verification intent: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET frozen_control_groups = CASE
      WHEN frozen_control_groups ? $3 THEN frozen_control_groups
      ELSE frozen_control_groups || to_jsonb($3::text)
    END,
    updated_at = $4
WHERE tenant_id = $1::uuid AND device_id = $2::uuid
`, tenantID, item.DeviceID, setpointControlGroup, now); err != nil {
			return fmt.Errorf("freeze expired verification control group: %w", err)
		}
		intent := commandmodel.CommandIntent{ID: item.CommandID, TenantID: tenantID, SiteID: item.SiteID, DeviceID: item.DeviceID, PayloadHash: item.PayloadHash, Version: item.IntentVersion}
		if err := insertDispatchTransition(ctx, tx, transitionID, intent, item.IntentVersion+1,
			commandmodel.IntentDispatching, commandmodel.IntentOutcomeUnknown,
			"REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED", "command-verifier", item.AttemptID,
			"REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED", now); err != nil {
			return err
		}
		if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_REPORTED_STATE_VERIFICATION_EXPIRED", map[string]any{
			"attemptId": item.AttemptID, "executionFence": item.Fence,
			"connectorEvidenceId": item.ConnectorEvidenceID,
		}, now); err != nil {
			return err
		}
	}
	return nil
}
