package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RuleRetiredReason is why an Alarm recovered when its rule left the Site's rules file.
const RuleRetiredReason = "RULE_RETIRED"

// SiteRules are the rules the Site evaluates now: each Site assignment whose latest revision is not
// retired, with the policy revision it points at.
func (store *PostgresStore) SiteRules(ctx context.Context, tenantID, siteID string) ([]SiteRule, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT current.assignment_id::text, policy.policy
FROM (SELECT DISTINCT ON (assignment_id) tenant_id, site_id, assignment_id, policy_revision_id, retired
      FROM alarm_runtime.alarm_policy_assignment
      WHERE tenant_id = $1 AND site_id = $2 AND subject_type = 'SITE' AND subject_id = $2
      ORDER BY assignment_id, assignment_revision DESC) AS current
JOIN alarm_runtime.alarm_policy_revision policy
  ON policy.tenant_id = current.tenant_id AND policy.site_id = current.site_id AND policy.policy_revision_id = current.policy_revision_id
WHERE NOT current.retired
ORDER BY current.assignment_id`, tenantID, siteID)
	if err != nil {
		return nil, fmt.Errorf("read Site Alarm rules: %w", err)
	}
	defer rows.Close()
	rules := []SiteRule{}
	for rows.Next() {
		var rule SiteRule
		var policyJSON []byte
		if err := rows.Scan(&rule.AssignmentID, &policyJSON); err != nil {
			return nil, fmt.Errorf("scan Site Alarm rule: %w", err)
		}
		if rule.Policy, err = decodeAlarmPolicy(policyJSON); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Site Alarm rules: %w", err)
	}
	return rules, nil
}

// RetireSiteRule takes a rule off the Site: it appends a retired revision of the assignment, stops
// the rule's pending evaluations and recovers its active Alarm because the rule was retired.
// Retiring a rule that is already retired changes nothing.
func (store *PostgresStore) RetireSiteRule(ctx context.Context, tenantID, siteID, assignmentID string, at time.Time, by string) error {
	if store == nil || store.pool == nil {
		return ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revision uint64
	var policyRevisionID, subjectType, subjectID string
	var retired bool
	err = tx.QueryRow(ctx, `
		SELECT assignment_revision, policy_revision_id::text, subject_type, subject_id::text, retired
		FROM alarm_runtime.alarm_policy_assignment
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3
		ORDER BY assignment_revision DESC LIMIT 1
		FOR UPDATE`, tenantID, siteID, assignmentID).Scan(&revision, &policyRevisionID, &subjectType, &subjectID, &retired)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read Site Alarm rule to retire: %w", err)
	}
	if retired {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_policy_assignment
		  (tenant_id, site_id, assignment_id, assignment_revision, policy_revision_id, subject_type, subject_id, assigned_at, assigned_by, retired)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true)`,
		tenantID, siteID, assignmentID, revision+1, policyRevisionID, subjectType, subjectID, at.UTC(), by); err != nil {
		return fmt.Errorf("retire Site Alarm rule: %w", err)
	}

	type activeAlarm struct{ fingerprint, incidentCorrelationID string }
	rows, err := tx.Query(ctx, `
		SELECT fingerprint, active_incident_correlation_id::text
		FROM alarm_runtime.alarm_evaluation_state
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3 AND active_alarm_id IS NOT NULL
		FOR UPDATE`, tenantID, siteID, assignmentID)
	if err != nil {
		return fmt.Errorf("read retired rule's active Alarms: %w", err)
	}
	var active []activeAlarm
	for rows.Next() {
		var alarm activeAlarm
		if err := rows.Scan(&alarm.fingerprint, &alarm.incidentCorrelationID); err != nil {
			rows.Close()
			return fmt.Errorf("scan retired rule's active Alarm: %w", err)
		}
		active = append(active, alarm)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate retired rule's active Alarms: %w", err)
	}
	for _, alarm := range active {
		correlationID, err := store.newID(at)
		if err != nil {
			return err
		}
		if _, err := store.clearInTransaction(ctx, tx, tenantID, siteID, Recovery{
			Fingerprint: alarm.fingerprint, IncidentCorrelationID: alarm.incidentCorrelationID,
			OccurredAt: at.UTC().Format(time.RFC3339Nano), Reason: RuleRetiredReason, RuleRevision: policyRevisionID,
			ActorType: "WORKLOAD", ActorID: by, CorrelationID: correlationID,
		}); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("recover retired rule's Alarm: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE alarm_runtime.alarm_evaluation_state
		SET next_evaluation_at = NULL, active_alarm_id = NULL, active_incident_correlation_id = NULL
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3`, tenantID, siteID, assignmentID); err != nil {
		return fmt.Errorf("stop retired rule's evaluations: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Site Alarm rule retirement: %w", err)
	}
	return nil
}
