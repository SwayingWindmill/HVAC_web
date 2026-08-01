package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

func (store *PostgresAuthorizationStore) LookupWorkOrderAuthorization(ctx context.Context, lookup AuthorizationLookup) (WorkOrderAuthorizationFacts, error) {
	if store == nil || store.pool == nil {
		return WorkOrderAuthorizationFacts{}, errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(lookup.SubjectIssuer) == "" || strings.TrimSpace(lookup.Subject) == "" || strings.TrimSpace(lookup.ActingOrganizationID) == "" {
		return WorkOrderAuthorizationFacts{}, errors.New("IAM Work Order authorization lookup is incomplete")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return WorkOrderAuthorizationFacts{}, fmt.Errorf("begin IAM Work Order authorization lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	facts := WorkOrderAuthorizationFacts{}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `
SELECT principal_id::text, principal_status, principal_revision
FROM iam.resolve_principal_identity($1, $2)
`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WorkOrderAuthorizationFacts{}, fmt.Errorf("resolve IAM Work Order principal identity: %w", err)
	}
	principalID := facts.Principal.ID
	if errors.Is(err, pgx.ErrNoRows) {
		principalID = ""
	}
	if err := setIAMAuthorizationContext(ctx, transaction, principalID, lookup.ActingOrganizationID); err != nil {
		return WorkOrderAuthorizationFacts{}, err
	}
	facts.PolicyRevision, err = loadWorkOrderPolicyRevision(ctx, transaction)
	if err != nil {
		return WorkOrderAuthorizationFacts{}, err
	}
	if principalID == "" {
		if err := transaction.Commit(ctx); err != nil {
			return WorkOrderAuthorizationFacts{}, fmt.Errorf("commit unmapped IAM Work Order lookup: %w", err)
		}
		return facts, nil
	}

	facts.Found = true
	facts.Principal.SubjectIssuer = lookup.SubjectIssuer
	facts.Principal.Subject = lookup.Subject
	if facts.Memberships, err = loadOrganizationMemberships(ctx, transaction); err != nil {
		return WorkOrderAuthorizationFacts{}, err
	}
	if facts.Permissions, err = loadWorkOrderPermissions(ctx, transaction); err != nil {
		return WorkOrderAuthorizationFacts{}, err
	}
	if facts.Targets, err = loadWorkOrderOwnershipTargets(ctx, transaction); err != nil {
		return WorkOrderAuthorizationFacts{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return WorkOrderAuthorizationFacts{}, fmt.Errorf("commit IAM Work Order authorization lookup: %w", err)
	}
	return facts, nil
}

func loadWorkOrderPolicyRevision(ctx context.Context, transaction pgx.Tx) (string, error) {
	var policyKey string
	var policyRevision int64
	if err := transaction.QueryRow(ctx, `
SELECT policy_key, policy_revision
FROM iam.policies
WHERE status = 'ACTIVE'
  AND policy_key = 'work-order-access'
ORDER BY policy_revision DESC
LIMIT 1
`).Scan(&policyKey, &policyRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "work-order-access:unconfigured", nil
		}
		return "", fmt.Errorf("read active IAM Work Order policy: %w", err)
	}
	return fmt.Sprintf("%s:%d", policyKey, policyRevision), nil
}

func loadWorkOrderPermissions(ctx context.Context, transaction pgx.Tx) ([]WorkOrderPermission, error) {
	rows, err := transaction.Query(ctx, `
SELECT acting_organization_id::text, site_id::text, action, effect, status, valid_from, valid_to
FROM iam.work_order_permissions
ORDER BY site_id, action, effect
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM Work Order permissions: %w", err)
	}
	defer rows.Close()
	permissions := []WorkOrderPermission{}
	for rows.Next() {
		var permission WorkOrderPermission
		var action string
		if err := rows.Scan(
			&permission.OrganizationID,
			&permission.SiteID,
			&action,
			&permission.Effect,
			&permission.Status,
			&permission.ValidFrom,
			&permission.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan IAM Work Order permission: %w", err)
		}
		permission.Action = workorderauth.Action(action)
		if permission.Action != workorderauth.ActionList && permission.Action != workorderauth.ActionRead &&
			permission.Action != workorderauth.ActionCreate && permission.Action != workorderauth.ActionAssign {
			return nil, fmt.Errorf("validate IAM Work Order permission action: unsupported action %q", action)
		}
		permissions = append(permissions, permission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM Work Order permissions: %w", err)
	}
	return permissions, nil
}

func loadWorkOrderOwnershipTargets(ctx context.Context, transaction pgx.Tx) ([]WorkOrderOwnershipTarget, error) {
	rows, err := transaction.Query(ctx, `
SELECT acting_organization_id::text, site_id::text, target_type, target_id, effect, status, valid_from, valid_to
FROM iam.work_order_ownership_targets
ORDER BY site_id, target_type, target_id, effect
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM Work Order ownership targets: %w", err)
	}
	defer rows.Close()
	targets := []WorkOrderOwnershipTarget{}
	for rows.Next() {
		var target WorkOrderOwnershipTarget
		if err := rows.Scan(&target.OrganizationID, &target.SiteID, &target.TargetType, &target.TargetID, &target.Effect, &target.Status, &target.ValidFrom, &target.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM Work Order ownership target: %w", err)
		}
		if (target.TargetType != "PRINCIPAL" && target.TargetType != "TEAM") || strings.TrimSpace(target.TargetID) == "" {
			return nil, errors.New("validate IAM Work Order ownership target")
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM Work Order ownership targets: %w", err)
	}
	return targets, nil
}

func (store *PostgresAuthorizationStore) RecordWorkOrderDecision(ctx context.Context, event WorkOrderDecisionAudit) error {
	if store == nil || store.pool == nil {
		return errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(event.ActingOrganizationID) == "" || strings.TrimSpace(event.SiteID) == "" ||
		strings.TrimSpace(string(event.Action)) == "" || strings.TrimSpace(event.PolicyRevision) == "" ||
		strings.TrimSpace(string(event.ReasonCode)) == "" || strings.TrimSpace(event.RequestID) == "" ||
		strings.TrimSpace(event.TraceID) == "" || strings.TrimSpace(event.OccurredAt) == "" {
		return errors.New("IAM Work Order decision audit is incomplete")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return fmt.Errorf("begin IAM Work Order decision audit: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := setIAMAuthorizationContext(ctx, transaction, event.PrincipalID, event.ActingOrganizationID); err != nil {
		return err
	}
	var principalID any
	if event.PrincipalID != "" {
		principalID = event.PrincipalID
	}
	var workOrderID any
	if event.WorkOrderID != "" {
		workOrderID = event.WorkOrderID
	}
	var assigneeID, teamID any
	if event.AssigneeID != nil {
		assigneeID = *event.AssigneeID
	}
	if event.TeamID != nil {
		teamID = *event.TeamID
	}
	if _, err := transaction.Exec(ctx, `
INSERT INTO iam.work_order_authorization_decisions
  (principal_id, acting_organization_id, site_id, work_order_id, assignee_id, team_id, action, allowed, policy_revision, reason_code, request_id, trace_id, occurred_at)
VALUES
  ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13::timestamptz)
`, principalID, event.ActingOrganizationID, event.SiteID, workOrderID, assigneeID, teamID, string(event.Action), event.Allowed,
		event.PolicyRevision, string(event.ReasonCode), event.RequestID, event.TraceID, event.OccurredAt); err != nil {
		return fmt.Errorf("insert IAM Work Order decision audit: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit IAM Work Order decision audit: %w", err)
	}
	return nil
}

var _ WorkOrderAuthorizationStore = (*PostgresAuthorizationStore)(nil)
var _ WorkOrderDecisionAuditSink = (*PostgresAuthorizationStore)(nil)
