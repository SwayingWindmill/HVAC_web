package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// RuleRetiredReason is why an Alarm recovered when its rule left the Site's rules file.
const RuleRetiredReason = "RULE_RETIRED"

// ErrRuleRetired means the rule left the Site after its evaluation was scheduled.
var ErrRuleRetired = errors.New("alarm rule is retired")

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
	return siteRulesInTx(ctx, tx, tenantID, siteID)
}

func siteRulesInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) ([]SiteRule, error) {
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

// SyncSiteRules reconciles the entire set of rules on a Site atomically inside one serializable
// transaction under a per-Site advisory lock so concurrent evaluations never observe half-synced
// rules and unchanged assignments preserve their in-flight evaluation state.
func (store *PostgresStore) SyncSiteRules(ctx context.Context, tenantID, siteID string, desired []SiteRule, now time.Time, by string) (SiteRuleSyncSummary, error) {
	if store == nil || store.pool == nil {
		return SiteRuleSyncSummary{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || !boundedText(by, 256) || now.IsZero() {
		return SiteRuleSyncSummary{}, errors.New("site rule sync metadata is invalid")
	}
	seen := make(map[string]bool, len(desired))
	for _, rule := range desired {
		if !alarmmodel.IsUUIDv7(rule.AssignmentID) {
			return SiteRuleSyncSummary{}, errors.New("site rule assignmentId must be a UUIDv7")
		}
		if seen[rule.AssignmentID] {
			return SiteRuleSyncSummary{}, fmt.Errorf("duplicate Site rule assignment %s", rule.AssignmentID)
		}
		seen[rule.AssignmentID] = true
	}

	tx, err := store.beginTenantTransactionWithOptions(ctx, tenantID, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('alarm_site_rules:' || $1 || ':' || $2))`, tenantID, siteID); err != nil {
		return SiteRuleSyncSummary{}, fmt.Errorf("lock Site Alarm rules sync: %w", err)
	}

	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	plan, err := PlanSiteRules(desired, active)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	sortByAssignment := func(rules []SiteRule) {
		sort.Slice(rules, func(left, right int) bool {
			return rules[left].AssignmentID < rules[right].AssignmentID
		})
	}
	sortByAssignment(plan.Added)
	sortByAssignment(plan.Changed)
	sortByAssignment(plan.Unchanged)
	sortByAssignment(plan.Retired)

	for _, rule := range plan.Added {
		if _, _, err := publishSitePolicyInTx(ctx, tx, store, tenantID, siteID, rule.Policy, rule.AssignmentID, now, by); err != nil {
			return SiteRuleSyncSummary{}, fmt.Errorf("publish added rule %s: %w", rule.AssignmentID, err)
		}
	}
	for _, rule := range plan.Changed {
		if _, _, err := publishSitePolicyInTx(ctx, tx, store, tenantID, siteID, rule.Policy, rule.AssignmentID, now, by); err != nil {
			return SiteRuleSyncSummary{}, fmt.Errorf("publish changed rule %s: %w", rule.AssignmentID, err)
		}
	}
	for _, rule := range plan.Retired {
		if err := retireSiteRuleInTx(ctx, tx, store, tenantID, siteID, rule.AssignmentID, now, by); err != nil {
			return SiteRuleSyncSummary{}, fmt.Errorf("retire removed rule %s: %w", rule.AssignmentID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteRuleSyncSummary{}, fmt.Errorf("commit Site Alarm rule sync: %w", err)
	}
	return SummarizeSiteRulePlan(plan), nil
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
	if err := retireSiteRuleInTx(ctx, tx, store, tenantID, siteID, assignmentID, at, by); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Site Alarm rule retirement: %w", err)
	}
	return nil
}

func retireSiteRuleInTx(ctx context.Context, tx pgx.Tx, store *PostgresStore, tenantID, siteID, assignmentID string, at time.Time, by string) error {
	// The same lock an evaluation of this rule takes, so a reading being judged and the retirement
	// never interleave.
	if err := lockEvaluationAssignment(ctx, tx, tenantID, assignmentID); err != nil {
		return err
	}
	var revision uint64
	var policyRevisionID, subjectType, subjectID string
	var retired bool
	err := tx.QueryRow(ctx, `
		SELECT assignment_revision, policy_revision_id::text, subject_type, subject_id::text, retired
		FROM alarm_runtime.alarm_policy_assignment
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3
		ORDER BY assignment_revision DESC LIMIT 1`, tenantID, siteID, assignmentID).Scan(&revision, &policyRevisionID, &subjectType, &subjectID, &retired)
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
		}); err != nil {
			return fmt.Errorf("recover retired rule's Alarm: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE alarm_runtime.alarm_evaluation_state
		SET next_evaluation_at = NULL, active_alarm_id = NULL, active_incident_correlation_id = NULL
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3`, tenantID, siteID, assignmentID); err != nil {
		return fmt.Errorf("stop retired rule's evaluations: %w", err)
	}
	return nil
}
