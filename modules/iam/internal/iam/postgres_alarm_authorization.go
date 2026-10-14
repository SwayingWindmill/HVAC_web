package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (store *PostgresAuthorizationStore) LookupAlarmAuthorization(ctx context.Context, lookup AuthorizationLookup) (AuthorizationFacts, error) {
	return store.lookupAccess(ctx, lookup, &alarmAccessPolicy, nil)
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
