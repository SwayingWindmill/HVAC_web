package commandservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func (store *PostgresStore) Approve(ctx context.Context, request commandmodel.ApproveRequest) (commandmodel.CommandIntent, error) {
	if store == nil || store.pool == nil {
		return commandmodel.CommandIntent{}, errors.New("command store is closed")
	}
	for attempt := 0; attempt < 4; attempt++ {
		intent, err := store.approveOnce(ctx, request)
		if err == nil || errors.Is(err, ErrApprovalRequired) || errors.Is(err, ErrApprovalInvalid) || errors.Is(err, ErrCommandNotFound) {
			return intent, err
		}
		if !isRetryablePostgresTransaction(err) {
			return commandmodel.CommandIntent{}, err
		}
	}
	return commandmodel.CommandIntent{}, errors.New("command approval transaction retry limit exceeded")
}

func (store *PostgresStore) approveOnce(ctx context.Context, request commandmodel.ApproveRequest) (commandmodel.CommandIntent, error) {
	if request.OrganizationID == "" || request.CommandID == "" {
		return commandmodel.CommandIntent{}, ErrApprovalInvalid
	}
	now := store.now().UTC()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("begin command approval transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activateOrganization(ctx, tx, request.OrganizationID); err != nil {
		return commandmodel.CommandIntent{}, err
	}
	var lockedCommandID string
	if err := tx.QueryRow(ctx, `
SELECT command_id::text
FROM command_runtime.command_intents
WHERE organization_id = $1::uuid AND command_id = $2::uuid
FOR UPDATE
`, request.OrganizationID, request.CommandID).Scan(&lockedCommandID); errors.Is(err, pgx.ErrNoRows) {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	} else if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("lock command for approval: %w", err)
	}

	intent, err := loadIntent(ctx, tx, request.OrganizationID, request.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if intent.Status != commandmodel.IntentAwaitingApproval {
		return commandmodel.CommandIntent{}, ErrApprovalInvalid
	}
	if err := validateApproval(intent, request.Approval, now); err != nil {
		return commandmodel.CommandIntent{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_approval_snapshots (
  approval_id, command_id, organization_id, site_id, device_id, approver_id,
  approver_role, approval_policy, payload_hash, capability_revision,
  risk_level, risk_rule_revision, authorization_grant_id,
  authorization_policy_revision, authorization_purpose, authorization_maximum_risk,
  authorization_emergency_revocation_revision, authorization_issued_at,
  authorization_expires_at, issued_at, expires_at, created_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
  $7, $8, $9, $10,
  $11, $12, $13,
  $14, $15, $16,
  $17, $18,
  $19, $20, $21, $22
)
`, request.Approval.ApprovalID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		request.Approval.ApproverID, request.Approval.ApproverRole, request.Approval.Policy,
		request.Approval.PayloadHash, request.Approval.CapabilityRevision, request.Approval.Risk,
		request.Approval.RiskRuleRevision, request.Approval.Authorization.GrantID,
		request.Approval.Authorization.PolicyRevision, request.Approval.Authorization.Purpose,
		request.Approval.Authorization.MaximumRisk, request.Approval.Authorization.EmergencyRevocationRevision,
		request.Approval.Authorization.IssuedAt, request.Approval.Authorization.ExpiresAt,
		request.Approval.IssuedAt, request.Approval.ExpiresAt, now); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && (postgresError.Code == "23505" || postgresError.Code == "22P02") {
			return commandmodel.CommandIntent{}, ErrApprovalInvalid
		}
		return commandmodel.CommandIntent{}, fmt.Errorf("insert command approval snapshot: %w", err)
	}

	approvalCount := len(intent.Approvals) + 1
	required := requiredApprovalCount(intent.ApprovalPolicy)
	transitionCount := 1
	finalStatus := commandmodel.IntentAwaitingApproval
	if approvalCount >= required {
		transitionCount = 2
		finalStatus = commandmodel.IntentQueued
	}
	transitionIDs := make([]string, transitionCount)
	for index := range transitionIDs {
		transitionIDs[index], err = store.newID(now)
		if err != nil {
			return commandmodel.CommandIntent{}, fmt.Errorf("allocate approval transition identifier: %w", err)
		}
	}
	auditID, err := store.newID(now)
	if err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("allocate approval audit identifier: %w", err)
	}

	version := intent.Version
	if finalStatus == commandmodel.IntentAwaitingApproval {
		version++
		if err := insertApprovalTransition(ctx, tx, transitionIDs[0], intent, version,
			commandmodel.IntentAwaitingApproval, commandmodel.IntentAwaitingApproval,
			"APPROVAL_CAPTURED_AWAITING_MORE", request.Approval, now); err != nil {
			return commandmodel.CommandIntent{}, err
		}
	} else {
		version++
		if err := insertApprovalTransition(ctx, tx, transitionIDs[0], intent, version,
			commandmodel.IntentAwaitingApproval, commandmodel.IntentApproved,
			"REQUIRED_APPROVALS_CAPTURED", request.Approval, now); err != nil {
			return commandmodel.CommandIntent{}, err
		}
		version++
		if err := insertApprovalTransition(ctx, tx, transitionIDs[1], intent, version,
			commandmodel.IntentApproved, commandmodel.IntentQueued,
			"APPROVED_AND_READY_FOR_DISPATCH", request.Approval, now); err != nil {
			return commandmodel.CommandIntent{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE command_runtime.command_intents
SET status = $3, version = $4, updated_at = $5
WHERE organization_id = $1::uuid AND command_id = $2::uuid
`, intent.OrganizationID, intent.ID, finalStatus, version, now); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("update approved command intent: %w", err)
	}

	redactedPayload, _ := json.Marshal(map[string]any{
		"approvalPolicy": intent.ApprovalPolicy,
		"approvalCount":  approvalCount,
		"requiredCount":  required,
		"approverRole":   request.Approval.ApproverRole,
		"risk":           intent.Risk,
	})
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_audit_intents (
  audit_intent_id, command_id, organization_id, site_id, device_id,
  event_kind, payload_hash, redacted_payload, created_at, relayed_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'COMMAND_APPROVAL_CAPTURED', $6, $7::jsonb, $8, NULL)
`, auditID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		intent.PayloadHash, redactedPayload, now); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("insert approval audit intent: %w", err)
	}

	if finalStatus == commandmodel.IntentQueued {
		outboxID, err := store.newID(now)
		if err != nil {
			return commandmodel.CommandIntent{}, fmt.Errorf("allocate approved dispatch outbox identifier: %w", err)
		}
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
			"authorizationGrantId":  request.Approval.Authorization.GrantID,
		})
		if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_dispatch_outbox (
  outbox_id, command_id, organization_id, site_id, device_id, command_version,
  available_at, lease_owner, lease_until, delivered_at, attempt_count, payload, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, NULL, NULL, NULL, 0, $8::jsonb, $7)
`, outboxID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
			version, now, outboxPayload); err != nil {
			return commandmodel.CommandIntent{}, fmt.Errorf("insert approved dispatch outbox: %w", err)
		}
	}

	updated, err := loadIntent(ctx, tx, request.OrganizationID, request.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return commandmodel.CommandIntent{}, fmt.Errorf("commit command approval: %w", err)
	}
	if finalStatus == commandmodel.IntentAwaitingApproval {
		return updated, ErrApprovalRequired
	}
	return updated, nil
}

func insertApprovalTransition(ctx context.Context, tx pgx.Tx, transitionID string, intent commandmodel.CommandIntent, version uint64, from, to commandmodel.IntentStatus, reason string, approval commandmodel.ApprovalEvidence, now time.Time) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.command_transitions (
  transition_id, command_id, organization_id, site_id, device_id, command_version,
  from_status, to_status, reason, actor_type, actor_id, causation_id, evidence_id, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, 'PRINCIPAL', $10, $11, $11, $12)
`, transitionID, intent.ID, intent.OrganizationID, intent.SiteID, intent.DeviceID,
		version, from, to, reason, approval.ApproverID, approval.ApprovalID, now); err != nil {
		return fmt.Errorf("insert approval transition: %w", err)
	}
	return nil
}
