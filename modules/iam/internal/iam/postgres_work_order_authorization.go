package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (store *PostgresAuthorizationStore) LookupWorkOrderAuthorization(ctx context.Context, lookup WorkOrderAuthorizationLookup) (WorkOrderAuthorizationFacts, error) {
	result := WorkOrderAuthorizationFacts{}
	facts, err := store.lookupAccess(ctx, lookup.AuthorizationLookup, &workOrderAccessPolicy, func(transaction pgx.Tx, facts *AuthorizationFacts) error {
		if !facts.Found {
			return nil
		}
		var err error
		if result.Targets, err = loadWorkOrderOwnershipTargets(ctx, transaction); err != nil {
			return err
		}
		if lookup.AssigneePrincipalID != "" {
			result.Assignee, err = loadPrincipalAccessByID(ctx, transaction, lookup.AssigneePrincipalID, lookup.TenantID, lookupInstant(lookup.At))
		}
		return err
	})
	if err != nil {
		return WorkOrderAuthorizationFacts{}, err
	}
	result.AuthorizationFacts = facts
	return result, nil
}

func loadWorkOrderOwnershipTargets(ctx context.Context, transaction pgx.Tx) ([]WorkOrderOwnershipTarget, error) {
	rows, err := transaction.Query(ctx, `
SELECT tenant_id::text, site_id::text, target_type, target_id, effect, status, valid_from, valid_to
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
		if err := rows.Scan(&target.TenantID, &target.SiteID, &target.TargetType, &target.TargetID, &target.Effect, &target.Status, &target.ValidFrom, &target.ValidTo); err != nil {
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
	if strings.TrimSpace(event.TenantID) == "" || strings.TrimSpace(event.SiteID) == "" ||
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
	if err := setIAMAuthorizationContext(ctx, transaction, event.PrincipalID, event.TenantID); err != nil {
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
  (principal_id, tenant_id, site_id, work_order_id, assignee_id, team_id, action, allowed, policy_revision, reason_code, request_id, trace_id, occurred_at)
VALUES
  ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13::timestamptz)
`, principalID, event.TenantID, event.SiteID, workOrderID, assigneeID, teamID, string(event.Action), event.Allowed,
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
