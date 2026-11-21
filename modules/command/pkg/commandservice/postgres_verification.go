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
	if store == nil || store.pool == nil {
		return commandmodel.VerificationEnvelope{}, errors.New("command store is closed")
	}
	if !commandmodel.IsUUIDv7(tenantID) || strings.TrimSpace(leaseOwner) == "" || leaseFor < time.Second || leaseFor > time.Minute {
		return commandmodel.VerificationEnvelope{}, ErrInvalidRequest
	}
	for attempt := 0; attempt < 4; attempt++ {
		envelope, err := store.claimVerificationOnce(ctx, tenantID, leaseOwner, leaseFor)
		if err == nil || errors.Is(err, ErrVerificationNotAvailable) {
			return envelope, err
		}
		if !isRetryablePostgresTransaction(err) {
			return commandmodel.VerificationEnvelope{}, err
		}
	}
	return commandmodel.VerificationEnvelope{}, errors.New("verification claim transaction retry limit exceeded")
}

func (store *PostgresStore) claimVerificationOnce(ctx context.Context, tenantID string, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandmodel.VerificationEnvelope{}, fmt.Errorf("begin verification claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateTenant(ctx, tx, tenantID); err != nil {
		return commandmodel.VerificationEnvelope{}, err
	}
	failureEvents, err := store.reconcileExpiredAcknowledgedAttempts(ctx, tx, tenantID, now)
	if err != nil {
		return commandmodel.VerificationEnvelope{}, err
	}

	var envelope commandmodel.VerificationEnvelope
	var capability string
	var parameters, edgeExecutionJSON []byte
	var attemptVersion uint64
	err = tx.QueryRow(ctx, `
SELECT i.command_id::text, a.attempt_id::text, i.tenant_id::text, i.site_id::text, i.device_id::text, i.point_id::text, i.trace_id,
       i.capability_name, i.capability_revision, i.canonical_parameters, i.verification_point_key,
       i.payload_hash, a.execution_fence, i.snapshot_revision,
       a.acknowledged_at, a.verification_deadline, a.connector_evidence_id, a.version,
       ce.edge_execution_evidence
FROM command_runtime.command_intents i
JOIN command_runtime.command_attempts a ON a.command_id = i.command_id
JOIN command_runtime.connector_evidence ce
  ON ce.tenant_id = i.tenant_id AND ce.command_id = i.command_id
  AND ce.attempt_id = a.attempt_id AND ce.execution_fence = a.execution_fence
  AND ce.connector_phase = 'ACKNOWLEDGED'
JOIN command_runtime.device_control_state d
  ON d.tenant_id = i.tenant_id AND d.device_id = i.device_id
WHERE i.tenant_id = $1::uuid
  AND i.status = 'DISPATCHING'
  AND a.status = 'ACKNOWLEDGED'
  AND a.execution_fence = i.active_execution_fence
  AND a.verification_deadline > $2
  AND (a.verification_lease_until IS NULL OR a.verification_lease_until <= $2)
  AND NOT (d.frozen_control_groups ? $3)
ORDER BY a.acknowledged_at, a.attempt_id
FOR UPDATE OF i, a SKIP LOCKED
LIMIT 1
`, tenantID, now, setpointControlGroup).Scan(
		&envelope.CommandID, &envelope.AttemptID, &envelope.TenantID, &envelope.SiteID, &envelope.DeviceID, &envelope.PointID, &envelope.TraceID,
		&capability, &envelope.CapabilityRevision, &parameters, &envelope.VerificationPointKey,
		&envelope.PayloadHash, &envelope.ExecutionFence, &envelope.BaselineBusinessRevision,
		&envelope.AcknowledgedAt, &envelope.VerificationDeadline, &envelope.ConnectorEvidenceID, &attemptVersion, &edgeExecutionJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := store.commitAndNotifyFailures(ctx, tx, failureEvents); err != nil {
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
	if len(edgeExecutionJSON) == 0 {
		return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
	}
	var edgeExecution commandmodel.EdgeExecutionEvidence
	if err := json.Unmarshal(edgeExecutionJSON, &edgeExecution); err != nil || !edgeExecution.ValidExecuted() {
		return commandmodel.VerificationEnvelope{}, ErrInvalidRequest
	}
	envelope.EdgeExecution = &edgeExecution
	envelope.Capability = commandmodel.Capability(capability)
	envelope.TraceID = commandmodel.NormalizeTraceID(envelope.TraceID)
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
	if err := store.commitAndNotifyFailures(ctx, tx, failureEvents); err != nil {
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
	var parameters, edgeExecutionJSON []byte
	var activeFence, attemptFence, attemptVersion uint64
	var acknowledgedAt, verificationDeadline, verificationLeaseUntil time.Time
	var verificationLeaseOwner string
	err = tx.QueryRow(ctx, `
SELECT i.command_id::text, i.tenant_id::text, i.site_id::text, i.device_id::text, i.point_id::text,
       i.capability_name, i.capability_revision, i.canonical_parameters, i.verification_point_key,
       i.payload_hash, i.snapshot_revision, i.version, i.status, i.active_execution_fence,
       a.status, a.version, a.execution_fence, a.payload_hash, COALESCE(a.connector_evidence_id, ''),
       a.acknowledged_at, a.verification_deadline, a.verification_lease_owner, a.verification_lease_until,
       ce.edge_execution_evidence
FROM command_runtime.command_intents i
JOIN command_runtime.command_attempts a ON a.command_id = i.command_id
LEFT JOIN command_runtime.connector_evidence ce
  ON ce.tenant_id = i.tenant_id AND ce.command_id = i.command_id
 AND ce.attempt_id = a.attempt_id AND ce.execution_fence = a.execution_fence
 AND ce.connector_phase = 'ACKNOWLEDGED'
WHERE i.tenant_id = $1::uuid AND i.command_id = $2::uuid AND a.attempt_id = $3::uuid
FOR UPDATE OF i, a
`, envelope.TenantID, envelope.CommandID, envelope.AttemptID).Scan(
		&intent.ID, &intent.TenantID, &intent.SiteID, &intent.DeviceID, &intent.PointID,
		&capability, &intent.CapabilityRevision, &parameters, &intent.VerificationPointKey,
		&intent.PayloadHash, &intent.SnapshotRevision, &intent.Version, &intent.Status, &activeFence,
		&attemptStatus, &attemptVersion, &attemptFence, &attemptPayloadHash, &connectorEvidenceID,
		&acknowledgedAt, &verificationDeadline, &verificationLeaseOwner, &verificationLeaseUntil,
		&edgeExecutionJSON,
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
	edgeExecution := envelope.EdgeExecution
	if len(edgeExecutionJSON) > 0 {
		var dbEdge commandmodel.EdgeExecutionEvidence
		if err := json.Unmarshal(edgeExecutionJSON, &dbEdge); err != nil {
			return fmt.Errorf("decode verification connector edge execution: %w", err)
		}
		edgeExecution = &dbEdge
		envelope.EdgeExecution = &dbEdge
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

	decision, err := DecideVerificationResolutionWithEdge(intent, envelope, edgeExecution, result, verificationDeadline, now)
	if err != nil {
		return ErrInvalidRequest
	}
	verified := decision.Verified
	attemptFinal := decision.AttemptFinal
	intentFinal := decision.IntentFinal
	reason := decision.Reason
	freeze := decision.FreezeControlGroup

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
		"intentStatus": intentFinal, "freezeControlGroup": freeze,
		"connectorEvidenceId": envelope.ConnectorEvidenceID, "verificationEvidenceId": result.EvidenceID,
		"baselineBusinessRevision": envelope.BaselineBusinessRevision, "reportedBusinessRevision": result.Reported.BusinessRevision,
	}, now); err != nil {
		return err
	}
	failureEvent, notifyFailure := controlFailureEventFromDecision(intent, intentFinal, reason, result.FailureCode, now)
	if notifyFailure {
		if err := store.insertControlAlarmOutbox(ctx, tx, failureEvent, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verification resolution: %w", err)
	}
	if notifyFailure {
		store.notifyControlFailure(failureEvent)
	}
	return nil
}

func (store *PostgresStore) reconcileExpiredAcknowledgedAttempts(ctx context.Context, tx postgresQueryExecutor, tenantID string, now time.Time) ([]ControlFailureEvent, error) {
	type expiredVerification struct {
		AttemptID, CommandID, SiteID, DeviceID, PointID, TraceID string
		Capability, CapabilityRevision, VerificationPointKey     string
		PayloadHash, ConnectorEvidenceID                         string
		Parameters, EdgeExecutionJSON                            []byte
		SnapshotRevision, Fence, IntentVersion, AttemptVersion   uint64
		AcknowledgedAt, VerificationDeadline                     time.Time
	}
	rows, err := tx.Query(ctx, `
SELECT a.attempt_id::text, a.command_id::text, a.site_id::text, a.device_id::text,
       i.point_id::text, i.trace_id, i.capability_name, i.capability_revision, i.canonical_parameters,
       i.verification_point_key, i.snapshot_revision,
       a.payload_hash, COALESCE(a.connector_evidence_id, ''), a.execution_fence,
       i.version, a.version, a.acknowledged_at, a.verification_deadline,
       ce.edge_execution_evidence
FROM command_runtime.command_attempts a
JOIN command_runtime.command_intents i ON i.command_id = a.command_id
LEFT JOIN command_runtime.connector_evidence ce
  ON ce.tenant_id = i.tenant_id AND ce.command_id = i.command_id
 AND ce.attempt_id = a.attempt_id AND ce.execution_fence = a.execution_fence
 AND ce.connector_phase = 'ACKNOWLEDGED'
WHERE a.tenant_id = $1::uuid
  AND a.status = 'ACKNOWLEDGED'
  AND a.verification_deadline <= $2
  AND i.status = 'DISPATCHING'
FOR UPDATE OF a, i SKIP LOCKED
LIMIT 50
`, tenantID, now)
	if err != nil {
		return nil, fmt.Errorf("select expired reported-state verifications: %w", err)
	}
	items := make([]expiredVerification, 0)
	for rows.Next() {
		var item expiredVerification
		if err := rows.Scan(
			&item.AttemptID, &item.CommandID, &item.SiteID, &item.DeviceID,
			&item.PointID, &item.TraceID, &item.Capability, &item.CapabilityRevision, &item.Parameters,
			&item.VerificationPointKey, &item.SnapshotRevision,
			&item.PayloadHash, &item.ConnectorEvidenceID, &item.Fence,
			&item.IntentVersion, &item.AttemptVersion, &item.AcknowledgedAt, &item.VerificationDeadline,
			&item.EdgeExecutionJSON,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan expired reported-state verification: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate expired reported-state verifications: %w", err)
	}
	rows.Close()
	events := make([]ControlFailureEvent, 0, len(items))
	for _, item := range items {
		var params commandmodel.CommandParameters
		if err := json.Unmarshal(item.Parameters, &params); err != nil {
			return nil, fmt.Errorf("decode expired verification command parameters: %w", err)
		}
		var edgeExecution *commandmodel.EdgeExecutionEvidence
		if len(item.EdgeExecutionJSON) > 0 {
			var parsed commandmodel.EdgeExecutionEvidence
			if err := json.Unmarshal(item.EdgeExecutionJSON, &parsed); err == nil {
				edgeExecution = &parsed
			}
		}
		intent := commandmodel.CommandIntent{
			ID:                   item.CommandID,
			TenantID:             tenantID,
			SiteID:               item.SiteID,
			DeviceID:             item.DeviceID,
			PointID:              item.PointID,
			TraceID:              commandmodel.NormalizeTraceID(item.TraceID),
			Capability:           commandmodel.Capability(item.Capability),
			CapabilityRevision:   item.CapabilityRevision,
			Parameters:           params,
			VerificationPointKey: item.VerificationPointKey,
			PayloadHash:          item.PayloadHash,
			SnapshotRevision:     item.SnapshotRevision,
			Version:              item.IntentVersion,
			Status:               commandmodel.IntentDispatching,
		}
		envelope := commandmodel.VerificationEnvelope{
			CommandID:                item.CommandID,
			AttemptID:                item.AttemptID,
			TenantID:                 tenantID,
			SiteID:                   item.SiteID,
			DeviceID:                 item.DeviceID,
			PointID:                  item.PointID,
			TraceID:                  intent.TraceID,
			Capability:               intent.Capability,
			CapabilityRevision:       item.CapabilityRevision,
			Parameters:               params,
			VerificationPointKey:     item.VerificationPointKey,
			PayloadHash:              item.PayloadHash,
			ExecutionFence:           item.Fence,
			BaselineBusinessRevision: item.SnapshotRevision,
			AcknowledgedAt:           item.AcknowledgedAt,
			VerificationDeadline:     item.VerificationDeadline,
			ConnectorEvidenceID:      item.ConnectorEvidenceID,
			EdgeExecution:            edgeExecution,
		}
		decision, err := DecideVerificationResolutionWithEdge(
			intent,
			envelope,
			edgeExecution,
			commandmodel.VerificationResult{Outcome: commandmodel.VerificationTimedOut},
			item.VerificationDeadline,
			now,
		)
		if err != nil {
			return nil, err
		}
		transitionID, err := store.newID(now)
		if err != nil {
			return nil, err
		}
		auditID, err := store.newID(now)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_attempts
SET status = $4, version = $5,
    verification_evidence_id = $6,
    verification_lease_owner = NULL, verification_lease_until = NULL, updated_at = $7
WHERE tenant_id = $1::uuid AND command_id = $2::uuid AND attempt_id = $3::uuid
`, tenantID, item.CommandID, item.AttemptID, decision.AttemptFinal, item.AttemptVersion+1, decision.Reason, now); err != nil {
			return nil, fmt.Errorf("expire reported-state verification attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = $3, version = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND command_id = $2::uuid
`, tenantID, item.CommandID, decision.IntentFinal, item.IntentVersion+1, now); err != nil {
			return nil, fmt.Errorf("expire reported-state verification intent: %w", err)
		}
		if decision.FreezeControlGroup {
			if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET frozen_control_groups = CASE
      WHEN frozen_control_groups ? $3 THEN frozen_control_groups
      ELSE frozen_control_groups || to_jsonb($3::text)
    END,
    updated_at = $4
WHERE tenant_id = $1::uuid AND device_id = $2::uuid
`, tenantID, item.DeviceID, setpointControlGroup, now); err != nil {
				return nil, fmt.Errorf("freeze expired verification control group: %w", err)
			}
		}
		if err := insertDispatchTransition(ctx, tx, transitionID, intent, item.IntentVersion+1,
			commandmodel.IntentDispatching, decision.IntentFinal,
			decision.Reason, "command-verifier", item.AttemptID,
			decision.Reason, now); err != nil {
			return nil, err
		}
		if err := insertDispatchAudit(ctx, tx, auditID, intent, "COMMAND_REPORTED_STATE_VERIFICATION_EXPIRED", map[string]any{
			"attemptId": item.AttemptID, "executionFence": item.Fence,
			"connectorEvidenceId": item.ConnectorEvidenceID,
			"intentStatus":        decision.IntentFinal,
			"reason":              decision.Reason,
			"freezeControlGroup":  decision.FreezeControlGroup,
		}, now); err != nil {
			return nil, err
		}
		if event, notify := controlFailureEventFromDecision(intent, decision.IntentFinal, decision.Reason, "", now); notify {
			if err := store.insertControlAlarmOutbox(ctx, tx, event, now); err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

// DueVerificationTenants lists distinct tenants currently holding acknowledged commands
// due for reported-state verification.
func (store *PostgresStore) DueVerificationTenants(ctx context.Context, now time.Time) ([]string, error) {
	return store.queryDueTenants(ctx, `SELECT tenant_id::text FROM command_runtime.tenants_with_due_verification($1) ORDER BY 1`, now)
}

// ClaimAnyVerification scans due tenants fairly and claims the next verification work item,
// waiting up to waitDuration (capped at 2s) on the verification wakeup signal when idle.
func (store *PostgresStore) ClaimAnyVerification(ctx context.Context, leaseOwner string, leaseFor, waitDuration time.Duration) (commandmodel.VerificationEnvelope, error) {
	if store == nil || store.pool == nil {
		return commandmodel.VerificationEnvelope{}, errors.New("command store is closed")
	}
	if strings.TrimSpace(leaseOwner) == "" || leaseFor < time.Second || leaseFor > time.Minute {
		return commandmodel.VerificationEnvelope{}, ErrInvalidRequest
	}
	return claimAnyWork(
		ctx,
		waitDuration,
		store.SubscribeVerificationWork,
		func(scanCtx context.Context) ([]string, error) {
			return store.DueVerificationTenants(scanCtx, store.now().UTC())
		},
		&store.verificationCursor,
		func(claimCtx context.Context, tenantID string) (commandmodel.VerificationEnvelope, error) {
			return store.ClaimVerification(claimCtx, tenantID, leaseOwner, leaseFor)
		},
		ErrVerificationNotAvailable,
	)
}
