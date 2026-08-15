package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
)

func (store *PostgresAuthorizationStore) LookupAlarmAuthorization(ctx context.Context, lookup AuthorizationLookup) (AlarmAuthorizationFacts, error) {
	if store == nil || store.pool == nil {
		return AlarmAuthorizationFacts{}, errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(lookup.SubjectIssuer) == "" || strings.TrimSpace(lookup.Subject) == "" || strings.TrimSpace(lookup.TenantID) == "" {
		return AlarmAuthorizationFacts{}, errors.New("IAM Alarm authorization lookup is incomplete")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return AlarmAuthorizationFacts{}, fmt.Errorf("begin IAM Alarm authorization lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	facts := AlarmAuthorizationFacts{}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `
SELECT principal_id::text, principal_status, principal_revision
FROM iam.resolve_principal_identity($1, $2)
`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AlarmAuthorizationFacts{}, fmt.Errorf("resolve IAM Alarm principal identity: %w", err)
	}
	principalID := facts.Principal.ID
	if errors.Is(err, pgx.ErrNoRows) {
		principalID = ""
	}
	if err := setIAMAuthorizationContext(ctx, transaction, principalID, lookup.TenantID); err != nil {
		return AlarmAuthorizationFacts{}, err
	}
	facts.PolicyRevision, err = loadAlarmPolicyRevision(ctx, transaction)
	if err != nil {
		return AlarmAuthorizationFacts{}, err
	}
	if principalID == "" {
		if err := transaction.Commit(ctx); err != nil {
			return AlarmAuthorizationFacts{}, fmt.Errorf("commit unmapped IAM Alarm lookup: %w", err)
		}
		return facts, nil
	}

	facts.Found = true
	facts.Principal.SubjectIssuer = lookup.SubjectIssuer
	facts.Principal.Subject = lookup.Subject
	if facts.Memberships, err = loadTenantMemberships(ctx, transaction); err != nil {
		return AlarmAuthorizationFacts{}, err
	}
	if facts.Permissions, err = loadAlarmPermissions(ctx, transaction); err != nil {
		return AlarmAuthorizationFacts{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return AlarmAuthorizationFacts{}, fmt.Errorf("commit IAM Alarm authorization lookup: %w", err)
	}
	return facts, nil
}

func loadAlarmPolicyRevision(ctx context.Context, transaction pgx.Tx) (string, error) {
	var policyKey string
	var policyRevision int64
	if err := transaction.QueryRow(ctx, `
SELECT policy_key, policy_revision
FROM iam.policies
WHERE status = 'ACTIVE'
  AND policy_key = 'alarm-access'
ORDER BY policy_revision DESC
LIMIT 1
`).Scan(&policyKey, &policyRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "alarm-access:unconfigured", nil
		}
		return "", fmt.Errorf("read active IAM Alarm policy: %w", err)
	}
	return fmt.Sprintf("%s:%d", policyKey, policyRevision), nil
}

func loadAlarmPermissions(ctx context.Context, transaction pgx.Tx) ([]AlarmPermission, error) {
	rows, err := transaction.Query(ctx, `
SELECT tenant_id::text, site_id::text, action, effect, status, valid_from, valid_to
FROM iam.alarm_permissions
ORDER BY site_id, action, effect
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM Alarm permissions: %w", err)
	}
	defer rows.Close()
	permissions := []AlarmPermission{}
	for rows.Next() {
		var permission AlarmPermission
		var action string
		if err := rows.Scan(
			&permission.TenantID,
			&permission.SiteID,
			&action,
			&permission.Effect,
			&permission.Status,
			&permission.ValidFrom,
			&permission.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan IAM Alarm permission: %w", err)
		}
		permission.Action = alarmauth.Action(action)
		if permission.Action != alarmauth.ActionList && permission.Action != alarmauth.ActionRead {
			return nil, fmt.Errorf("validate IAM Alarm permission action: unsupported action %q", action)
		}
		permissions = append(permissions, permission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM Alarm permissions: %w", err)
	}
	return permissions, nil
}

func (store *PostgresAuthorizationStore) RecordAlarmDecision(ctx context.Context, event AlarmDecisionAudit) error {
	if store == nil || store.pool == nil {
		return errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(event.TenantID) == "" || strings.TrimSpace(event.SiteID) == "" ||
		strings.TrimSpace(string(event.Action)) == "" || strings.TrimSpace(event.PolicyRevision) == "" ||
		strings.TrimSpace(string(event.ReasonCode)) == "" || strings.TrimSpace(event.RequestID) == "" ||
		strings.TrimSpace(event.TraceID) == "" || strings.TrimSpace(event.OccurredAt) == "" {
		return errors.New("IAM Alarm decision audit is incomplete")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return fmt.Errorf("begin IAM Alarm decision audit: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := setIAMAuthorizationContext(ctx, transaction, event.PrincipalID, event.TenantID); err != nil {
		return err
	}
	var principalID any
	if event.PrincipalID != "" {
		principalID = event.PrincipalID
	}
	var alarmID any
	if event.AlarmID != "" {
		alarmID = event.AlarmID
	}
	if _, err := transaction.Exec(ctx, `
INSERT INTO iam.alarm_authorization_decisions
  (principal_id, tenant_id, site_id, alarm_id, action, allowed, policy_revision, reason_code, request_id, trace_id, occurred_at)
VALUES
  ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11::timestamptz)
`, principalID, event.TenantID, event.SiteID, alarmID, string(event.Action), event.Allowed,
		event.PolicyRevision, string(event.ReasonCode), event.RequestID, event.TraceID, event.OccurredAt); err != nil {
		return fmt.Errorf("insert IAM Alarm decision audit: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit IAM Alarm decision audit: %w", err)
	}
	return nil
}

var _ AlarmAuthorizationStore = (*PostgresAuthorizationStore)(nil)
var _ AlarmDecisionAuditSink = (*PostgresAuthorizationStore)(nil)
