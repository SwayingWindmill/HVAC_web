package commandservice

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// IDGenerator returns a PostgreSQL-compatible UUID string.
type IDGenerator func(time.Time) (string, error)

type PostgresStore struct {
	pool  *pgxpool.Pool
	now   func() time.Time
	newID IDGenerator
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse command database URL: %w", err)
	}
	if config.ConnConfig.User != "s3_command_service" {
		return nil, errors.New("command database identity must be s3_command_service")
	}
	config.MaxConns = 16
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s3_command_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open command database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping command database: %w", err)
	}
	return NewPostgresStore(pool, time.Now, newUUID), nil
}

func NewPostgresStore(pool *pgxpool.Pool, now func() time.Time, generator IDGenerator) *PostgresStore {
	if now == nil {
		now = time.Now
	}
	if generator == nil {
		generator = newUUID
	}
	return &PostgresStore{pool: pool, now: now, newID: generator}
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) Submit(ctx context.Context, request commandmodel.SubmitRequest) (SubmitResult, error) {
	payloadHash, err := validateAndHash(request)
	if err != nil {
		return SubmitResult{}, err
	}
	for attempt := 0; attempt < 4; attempt++ {
		result, submitErr := store.submitOnce(ctx, request)
		if submitErr == nil {
			return result, nil
		}
		if errors.Is(submitErr, ErrIdempotencyConflict) {
			replayed, found, replayErr := store.replayIdempotentCommand(ctx, request, payloadHash)
			if replayErr != nil {
				return SubmitResult{}, replayErr
			}
			if found {
				return replayed, nil
			}
		}
		if !isRetryablePostgresTransaction(submitErr) {
			return SubmitResult{}, submitErr
		}
	}
	return SubmitResult{}, errors.New("command submission transaction retry limit exceeded")
}

func (store *PostgresStore) submitOnce(ctx context.Context, request commandmodel.SubmitRequest) (SubmitResult, error) {
	if store == nil || store.pool == nil {
		return SubmitResult{}, errors.New("command store is closed")
	}
	payloadHash, err := validateAndHash(request)
	if err != nil {
		return SubmitResult{}, err
	}

	now := store.now().UTC()
	ids, err := store.commandIDs(now)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("allocate command identifiers: %w", err)
	}
	parameters, _ := json.Marshal(map[string]any{"setpointC": request.SetpointC})

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("begin command submission transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, request.OrganizationID); err != nil {
		return SubmitResult{}, err
	}

	existing, found, err := loadIdempotentCommand(ctx, tx, request.OrganizationID, request.DeviceID, request.IdempotencyKey)
	if err != nil {
		return SubmitResult{}, err
	}
	if found {
		if existing.PayloadHash != payloadHash {
			return SubmitResult{}, ErrIdempotencyConflict
		}
		intent, err := loadIntent(ctx, tx, request.OrganizationID, existing.CommandID)
		if err != nil {
			return SubmitResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SubmitResult{}, fmt.Errorf("commit idempotent command replay: %w", err)
		}
		return SubmitResult{Intent: intent, Replayed: true}, nil
	}

	risk, approvalPolicy, err := evaluateGovernance(request, now)
	if err != nil {
		return SubmitResult{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.device_control_state (
  organization_id, site_id, device_id, next_command_sequence, active_execution_fence, frozen_control_groups, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 0, '[]'::jsonb, $4)
ON CONFLICT (organization_id, device_id) DO NOTHING
`, request.OrganizationID, request.SiteID, request.DeviceID, now); err != nil {
		return SubmitResult{}, fmt.Errorf("ensure device control state: %w", err)
	}

	var sequence uint64
	if err := tx.QueryRow(ctx, `
SELECT next_command_sequence
FROM command_runtime.device_control_state
WHERE organization_id = $1::uuid AND device_id = $2::uuid
FOR UPDATE
`, request.OrganizationID, request.DeviceID).Scan(&sequence); err != nil {
		return SubmitResult{}, fmt.Errorf("lock device command sequence: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.device_control_state
SET next_command_sequence = next_command_sequence + 1, site_id = $3::uuid, updated_at = $4
WHERE organization_id = $1::uuid AND device_id = $2::uuid
`, request.OrganizationID, request.DeviceID, request.SiteID, now); err != nil {
		return SubmitResult{}, fmt.Errorf("advance device command sequence: %w", err)
	}

	finalStatus := commandmodel.IntentQueued
	if approvalPolicy != commandmodel.ApprovalNone {
		finalStatus = commandmodel.IntentAwaitingApproval
	}
	intent := commandmodel.CommandIntent{
		ID:                    ids.commandID,
		OrganizationID:        request.OrganizationID,
		SiteID:                request.SiteID,
		DeviceID:              request.DeviceID,
		PrincipalID:           request.PrincipalID,
		IdempotencyKey:        request.IdempotencyKey,
		Capability:            request.Capability,
		CapabilityRevision:    setpointCapabilityRevision,
		Risk:                  risk.Level,
		RiskSnapshot:          risk,
		ApprovalPolicy:        approvalPolicy,
		Authorization:         request.Authorization,
		RetryPolicy:           commandmodel.RetryPreSendOnly,
		SetpointC:             request.SetpointC,
		PayloadHash:           payloadHash,
		SnapshotRevision:      request.CurrentState.BusinessRevision,
		DeviceCommandSequence: sequence,
		Status:                finalStatus,
		Version:               3,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_intents (
  command_id, organization_id, site_id, device_id, principal_id, idempotency_key,
  capability_name, capability_revision, risk_level, risk_rule_revision, approval_policy,
  retry_policy, canonical_parameters, payload_hash, snapshot_revision, device_command_sequence,
  status, version, active_execution_fence, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6,
  $7, $8, $9, $10, $11,
  $12, $13::jsonb, $14, $15, $16,
  $17, $18, 0, $19, $19
)
`, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID, intent.PrincipalID,
		intent.IdempotencyKey, intent.Capability, intent.CapabilityRevision, intent.Risk,
		intent.RiskSnapshot.RuleRevision, intent.ApprovalPolicy, intent.RetryPolicy, parameters,
		intent.PayloadHash, intent.SnapshotRevision, intent.DeviceCommandSequence,
		intent.Status, intent.Version, now); err != nil {
		return SubmitResult{}, classifyCommandWriteError("insert command intent", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_idempotency (
  organization_id, device_id, idempotency_key, payload_hash, command_id, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6)
`, intent.OrganizationID, intent.DeviceID, intent.IdempotencyKey, intent.PayloadHash, intent.ID, now); err != nil {
		return SubmitResult{}, classifyCommandWriteError("insert command idempotency", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_authorization_snapshots (
  command_id, organization_id, site_id, device_id, principal_id, grant_id,
  policy_revision, authorization_purpose, capability_name, capability_revision, maximum_risk,
  emergency_revocation_revision, issued_at, expires_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6,
  $7, $8, $9, $10, $11, $12, $13, $14, $15
)
`, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID, intent.PrincipalID,
		intent.Authorization.GrantID, intent.Authorization.PolicyRevision, intent.Authorization.Purpose,
		intent.Authorization.Capability, intent.Authorization.CapabilityRevision, intent.Authorization.MaximumRisk,
		intent.Authorization.EmergencyRevocationRevision, intent.Authorization.IssuedAt,
		intent.Authorization.ExpiresAt, now); err != nil {
		return SubmitResult{}, classifyCommandWriteError("insert command authorization snapshot", err)
	}
	riskReasons, _ := json.Marshal(intent.RiskSnapshot.Reasons)
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_risk_snapshots (
  command_id, organization_id, site_id, device_id, risk_level, rule_revision, reasons, evaluated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::jsonb, $8)
`, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID, intent.Risk,
		intent.RiskSnapshot.RuleRevision, riskReasons, intent.RiskSnapshot.EvaluatedAt); err != nil {
		return SubmitResult{}, classifyCommandWriteError("insert command risk snapshot", err)
	}

	finalReason := "READY_FOR_DISPATCH"
	if intent.Status == commandmodel.IntentAwaitingApproval {
		finalReason = "RISK_REQUIRES_APPROVAL"
	}
	transitionRows := []struct {
		id      string
		version uint64
		from    *commandmodel.IntentStatus
		to      commandmodel.IntentStatus
		reason  string
		actor   string
	}{
		{id: ids.transitionIDs[0], version: 1, to: commandmodel.IntentSubmitted, reason: "COMMAND_ACCEPTED", actor: request.PrincipalID},
		{id: ids.transitionIDs[1], version: 2, from: statusPointer(commandmodel.IntentSubmitted), to: commandmodel.IntentValidating, reason: "CAPABILITY_CURRENT_STATE_AND_AUTHORIZATION_VALIDATED", actor: "command-service"},
		{id: ids.transitionIDs[2], version: 3, from: statusPointer(commandmodel.IntentValidating), to: intent.Status, reason: finalReason, actor: "command-service"},
	}
	for _, row := range transitionRows {
		var from any
		if row.from != nil {
			from = string(*row.from)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_transitions (
  transition_id, command_id, organization_id, site_id, device_id, command_version,
  from_status, to_status, reason, actor_type, actor_id, causation_id, evidence_id, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, $10, $11, $12, NULL, $13)
`, row.id, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID, row.version,
			from, row.to, row.reason, actorType(row.actor, request.PrincipalID), row.actor, "submit", now); err != nil {
			return SubmitResult{}, classifyCommandWriteError("insert command transition", err)
		}
		intent.Transitions = append(intent.Transitions, commandmodel.Transition{
			From: rowFrom(row.from), To: row.to, Reason: row.reason, Actor: row.actor,
			At: now, Version: row.version, Causation: "submit",
		})
	}

	redactedPayload, _ := json.Marshal(map[string]any{
		"capability": intent.Capability,
		"setpointC":  intent.SetpointC,
		"risk":       intent.Risk,
	})
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_audit_intents (
  audit_intent_id, command_id, organization_id, site_id, device_id,
  event_kind, payload_hash, redacted_payload, created_at, relayed_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'COMMAND_ACCEPTED', $6, $7::jsonb, $8, NULL)
`, ids.auditID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		intent.PayloadHash, redactedPayload, now); err != nil {
		return SubmitResult{}, classifyCommandWriteError("insert command audit intent", err)
	}

	if intent.Status == commandmodel.IntentQueued {
		outboxPayload, _ := json.Marshal(map[string]any{
			"schemaVersion":         1,
			"commandId":             intent.ID,
			"organizationId":        intent.OrganizationID,
			"siteId":                intent.SiteID,
			"deviceId":              intent.DeviceID,
			"capability":            intent.Capability,
			"capabilityRevision":    intent.CapabilityRevision,
			"deviceCommandSequence": intent.DeviceCommandSequence,
			"payloadHash":           intent.PayloadHash,
		})
		if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_dispatch_outbox (
  outbox_id, command_id, organization_id, site_id, device_id, command_version,
  available_at, lease_owner, lease_until, delivered_at, attempt_count, payload, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, NULL, NULL, NULL, 0, $8::jsonb, $7)
`, ids.outboxID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
			intent.Version, now, outboxPayload); err != nil {
			return SubmitResult{}, classifyCommandWriteError("insert command dispatch outbox", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return SubmitResult{}, fmt.Errorf("commit command submission: %w", err)
	}
	return SubmitResult{Intent: intent}, nil
}

func (store *PostgresStore) Get(ctx context.Context, organizationID, commandID string) (commandmodel.CommandIntent, error) {
	if store == nil || store.pool == nil {
		return commandmodel.CommandIntent{}, errors.New("command store is closed")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("begin command read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, organizationID); err != nil {
		return commandmodel.CommandIntent{}, err
	}
	intent, err := loadIntent(ctx, tx, organizationID, commandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("commit command read: %w", err)
	}
	return intent, nil
}

type SubmissionEvidence struct {
	IntentCount        int
	IdempotencyCount   int
	AuthorizationCount int
	RiskCount          int
	ApprovalCount      int
	TransitionCount    int
	AuditIntentCount   int
	OutboxCount        int
}

func (store *PostgresStore) SubmissionEvidence(ctx context.Context, organizationID, commandID string) (SubmissionEvidence, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SubmissionEvidence{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, organizationID); err != nil {
		return SubmissionEvidence{}, err
	}
	var evidence SubmissionEvidence
	err = tx.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM command_runtime.command_intents WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_idempotency WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_authorization_snapshots WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_risk_snapshots WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_approval_snapshots WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_transitions WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_audit_intents WHERE command_id = $1::uuid),
  (SELECT count(*) FROM command_runtime.command_dispatch_outbox WHERE command_id = $1::uuid)
`, commandID).Scan(&evidence.IntentCount, &evidence.IdempotencyCount,
		&evidence.AuthorizationCount, &evidence.RiskCount, &evidence.ApprovalCount,
		&evidence.TransitionCount, &evidence.AuditIntentCount, &evidence.OutboxCount)
	if err != nil {
		return SubmissionEvidence{}, fmt.Errorf("read command submission evidence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmissionEvidence{}, err
	}
	return evidence, nil
}

type commandIdentifiers struct {
	commandID     string
	transitionIDs [3]string
	auditID       string
	outboxID      string
}

func (store *PostgresStore) commandIDs(now time.Time) (commandIdentifiers, error) {
	var result commandIdentifiers
	values := []*string{&result.commandID, &result.transitionIDs[0], &result.transitionIDs[1], &result.transitionIDs[2], &result.auditID, &result.outboxID}
	for _, target := range values {
		value, err := store.newID(now)
		if err != nil {
			return commandIdentifiers{}, err
		}
		*target = value
	}
	return result, nil
}

type idempotentCommand struct {
	CommandID   string
	PayloadHash string
}

func (store *PostgresStore) replayIdempotentCommand(ctx context.Context, request commandmodel.SubmitRequest, payloadHash string) (SubmitResult, bool, error) {
	if store == nil || store.pool == nil {
		return SubmitResult{}, false, errors.New("command store is closed")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SubmitResult{}, false, fmt.Errorf("begin idempotency replay transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, request.OrganizationID); err != nil {
		return SubmitResult{}, false, err
	}
	existing, found, err := loadIdempotentCommand(ctx, tx, request.OrganizationID, request.DeviceID, request.IdempotencyKey)
	if err != nil {
		return SubmitResult{}, false, err
	}
	if !found {
		return SubmitResult{}, false, nil
	}
	if existing.PayloadHash != payloadHash {
		return SubmitResult{}, false, ErrIdempotencyConflict
	}
	intent, err := loadIntent(ctx, tx, request.OrganizationID, existing.CommandID)
	if err != nil {
		return SubmitResult{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitResult{}, false, fmt.Errorf("commit idempotency replay transaction: %w", err)
	}
	return SubmitResult{Intent: intent, Replayed: true}, true, nil
}

func loadIdempotentCommand(ctx context.Context, tx pgx.Tx, organizationID, deviceID, key string) (idempotentCommand, bool, error) {
	var result idempotentCommand
	err := tx.QueryRow(ctx, `
SELECT command_id::text, payload_hash
FROM command_runtime.command_idempotency
WHERE organization_id = $1::uuid AND device_id = $2::uuid AND idempotency_key = $3
`, organizationID, deviceID, key).Scan(&result.CommandID, &result.PayloadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotentCommand{}, false, nil
	}
	if err != nil {
		return idempotentCommand{}, false, fmt.Errorf("read command idempotency: %w", err)
	}
	return result, true, nil
}

func loadIntent(ctx context.Context, tx pgx.Tx, organizationID, commandID string) (commandmodel.CommandIntent, error) {
	var intent commandmodel.CommandIntent
	var capability, risk, retry, status, approvalPolicy string
	err := tx.QueryRow(ctx, `
SELECT command_id::text, organization_id::text, site_id::text, device_id::text, principal_id::text,
       idempotency_key, capability_name, capability_revision, risk_level, risk_rule_revision,
       approval_policy, retry_policy, (canonical_parameters ->> 'setpointC')::double precision,
       payload_hash, snapshot_revision, device_command_sequence, status, version,
       active_execution_fence, created_at, updated_at
FROM command_runtime.command_intents
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, organizationID, commandID).Scan(
		&intent.ID, &intent.OrganizationID, &intent.SiteID, &intent.DeviceID, &intent.PrincipalID,
		&intent.IdempotencyKey, &capability, &intent.CapabilityRevision, &risk,
		&intent.RiskSnapshot.RuleRevision, &approvalPolicy, &retry, &intent.SetpointC,
		&intent.PayloadHash, &intent.SnapshotRevision, &intent.DeviceCommandSequence,
		&status, &intent.Version, &intent.ActiveFence, &intent.CreatedAt, &intent.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("read command intent: %w", err)
	}
	intent.Capability = commandmodel.Capability(capability)
	intent.Risk = commandmodel.RiskLevel(risk)
	intent.RiskSnapshot.Level = intent.Risk
	intent.ApprovalPolicy = commandmodel.ApprovalPolicy(approvalPolicy)
	intent.RetryPolicy = commandmodel.RetryPolicy(retry)
	intent.Status = commandmodel.IntentStatus(status)

	var authorizationCapability, maximumRisk string
	if err := tx.QueryRow(ctx, `
SELECT grant_id, policy_revision, authorization_purpose, principal_id::text, organization_id::text, site_id::text,
       device_id::text, capability_name, maximum_risk, capability_revision,
       emergency_revocation_revision, issued_at, expires_at
FROM command_runtime.command_authorization_snapshots
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, organizationID, commandID).Scan(
		&intent.Authorization.GrantID, &intent.Authorization.PolicyRevision, &intent.Authorization.Purpose,
		&intent.Authorization.PrincipalID, &intent.Authorization.OrganizationID,
		&intent.Authorization.SiteID, &intent.Authorization.DeviceID,
		&authorizationCapability, &maximumRisk, &intent.Authorization.CapabilityRevision,
		&intent.Authorization.EmergencyRevocationRevision, &intent.Authorization.IssuedAt,
		&intent.Authorization.ExpiresAt,
	); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("read command authorization snapshot: %w", err)
	}
	intent.Authorization.Capability = commandmodel.Capability(authorizationCapability)
	intent.Authorization.MaximumRisk = commandmodel.RiskLevel(maximumRisk)
	intent.Authorizations = append(intent.Authorizations, intent.Authorization)

	var riskReasons []byte
	if err := tx.QueryRow(ctx, `
SELECT risk_level, rule_revision, reasons, evaluated_at
FROM command_runtime.command_risk_snapshots
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, organizationID, commandID).Scan(
		&risk, &intent.RiskSnapshot.RuleRevision, &riskReasons, &intent.RiskSnapshot.EvaluatedAt,
	); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("read command risk snapshot: %w", err)
	}
	intent.RiskSnapshot.Level = commandmodel.RiskLevel(risk)
	if err := json.Unmarshal(riskReasons, &intent.RiskSnapshot.Reasons); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("decode command risk reasons: %w", err)
	}

	approvalRows, err := tx.Query(ctx, `
SELECT approval_id::text, approver_id::text, approver_role, approval_policy,
       payload_hash, capability_revision, risk_level, risk_rule_revision,
       authorization_grant_id, authorization_policy_revision, authorization_purpose, authorization_maximum_risk,
       authorization_emergency_revocation_revision, authorization_issued_at, authorization_expires_at,
       issued_at, expires_at
FROM command_runtime.command_approval_snapshots
WHERE organization_id = $1::uuid AND command_id = $2::uuid
ORDER BY created_at, approval_id
`, organizationID, commandID)
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("read command approval snapshots: %w", err)
	}
	for approvalRows.Next() {
		var approval commandmodel.ApprovalEvidence
		var policy, approvalRisk, approvalMaximumRisk string
		if err := approvalRows.Scan(&approval.ApprovalID, &approval.ApproverID, &approval.ApproverRole,
			&policy, &approval.PayloadHash, &approval.CapabilityRevision, &approvalRisk,
			&approval.RiskRuleRevision, &approval.Authorization.GrantID,
			&approval.Authorization.PolicyRevision, &approval.Authorization.Purpose, &approvalMaximumRisk,
			&approval.Authorization.EmergencyRevocationRevision,
			&approval.Authorization.IssuedAt, &approval.Authorization.ExpiresAt,
			&approval.IssuedAt, &approval.ExpiresAt); err != nil {
			approvalRows.Close()
			return commandmodel.CommandIntent{}, fmt.Errorf("scan command approval snapshot: %w", err)
		}
		approval.Policy = commandmodel.ApprovalPolicy(policy)
		approval.Risk = commandmodel.RiskLevel(approvalRisk)
		approval.Authorization.PrincipalID = approval.ApproverID
		approval.Authorization.OrganizationID = intent.OrganizationID
		approval.Authorization.SiteID = intent.SiteID
		approval.Authorization.DeviceID = intent.DeviceID
		approval.Authorization.Capability = intent.Capability
		approval.Authorization.CapabilityRevision = intent.CapabilityRevision
		approval.Authorization.MaximumRisk = commandmodel.RiskLevel(approvalMaximumRisk)
		intent.Approvals = append(intent.Approvals, approval)
		intent.Authorizations = append(intent.Authorizations, approval.Authorization)
		intent.Authorization = approval.Authorization
	}
	if err := approvalRows.Err(); err != nil {
		approvalRows.Close()
		return commandmodel.CommandIntent{}, fmt.Errorf("iterate command approval snapshots: %w", err)
	}
	approvalRows.Close()

	rows, err := tx.Query(ctx, `
SELECT from_status, to_status, reason, actor_id, occurred_at, command_version, causation_id, COALESCE(evidence_id, '')
FROM command_runtime.command_transitions
WHERE organization_id = $1::uuid AND command_id = $2::uuid
ORDER BY command_version
`, organizationID, commandID)
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("read command transitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var transition commandmodel.Transition
		var from *string
		var to string
		if err := rows.Scan(&from, &to, &transition.Reason, &transition.Actor, &transition.At,
			&transition.Version, &transition.Causation, &transition.EvidenceID); err != nil {
			return commandmodel.CommandIntent{}, fmt.Errorf("scan command transition: %w", err)
		}
		if from != nil {
			transition.From = commandmodel.IntentStatus(*from)
		}
		transition.To = commandmodel.IntentStatus(to)
		intent.Transitions = append(intent.Transitions, transition)
	}
	if err := rows.Err(); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("iterate command transitions: %w", err)
	}
	return intent, nil
}

func activateOrganization(ctx context.Context, tx pgx.Tx, organizationID string) error {
	if organizationID == "" {
		return ErrInvalidRequest
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s3_command_runtime`); err != nil {
		return fmt.Errorf("activate command runtime database identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID); err != nil {
		return fmt.Errorf("set command Organization context: %w", err)
	}
	return nil
}

func classifyCommandWriteError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, ErrIdempotencyConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func isRetryablePostgresTransaction(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && (postgresError.Code == "40001" || postgresError.Code == "40P01")
}

func statusPointer(value commandmodel.IntentStatus) *commandmodel.IntentStatus {
	return &value
}

func rowFrom(value *commandmodel.IntentStatus) commandmodel.IntentStatus {
	if value == nil {
		return ""
	}
	return *value
}

func actorType(actor, principalID string) string {
	if actor == principalID {
		return "PRINCIPAL"
	}
	return "WORKLOAD"
}

func newUUID(_ time.Time) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
