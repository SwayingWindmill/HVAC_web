package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSiteRulesStreamInTx(ctx, tx, tenantID, siteID); err != nil {
		return SiteRuleSyncSummary{}, err
	}

	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	plan, err := PlanSiteRules(desired, active)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	summary, err := applySiteRulePlanInTx(ctx, tx, store, tenantID, siteID, plan, now, by)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	nextRules, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	if latestMeta.BundleRevision == 0 || hasSiteRulePlanChanges(summary) {
		if _, _, err := insertSiteRuleBundleRevisionInTx(ctx, tx, tenantID, siteID, latestMeta.BundleRevision+1, RuleBundleSourceCLISync, nil, "", summary, nextRules, now, by, ""); err != nil {
			return SiteRuleSyncSummary{}, err
		}
	}
	bundleRevision, assignmentCount, err := store.siteRuleCacheStampInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleSyncSummary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteRuleSyncSummary{}, fmt.Errorf("commit Site Alarm rule sync: %w", err)
	}
	store.ruleCache.Put(tenantID, siteID, bundleRevision, assignmentCount, nextRules)
	return summary, nil
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
	if alarmmodel.IsUUIDv7(siteID) {
		if err := lockSiteRulesStreamInTx(ctx, tx, tenantID, siteID); err != nil {
			return err
		}
	}
	if err := retireSiteRuleInTx(ctx, tx, store, tenantID, siteID, assignmentID, at, by); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Site Alarm rule retirement: %w", err)
	}
	store.ruleCache.Invalidate(tenantID, siteID)
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
		SET candidate_since = NULL, repeat_count = 0, sample_window = '[]'::jsonb,
		    next_evaluation_at = NULL, active_alarm_id = NULL, active_incident_correlation_id = NULL
		WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3`, tenantID, siteID, assignmentID); err != nil {
		return fmt.Errorf("stop retired rule's evaluations: %w", err)
	}
	return nil
}

func lockSiteRulesStreamInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('alarm_site_rules:' || $1 || ':' || $2, 0))`, tenantID, siteID); err != nil {
		return fmt.Errorf("lock Site Alarm rules sync: %w", err)
	}
	return nil
}

func hasSiteRulePlanChanges(summary SiteRuleSyncSummary) bool {
	return summary.Added > 0 || summary.Changed > 0 || summary.Retired > 0
}

func applySiteRulePlanInTx(ctx context.Context, tx pgx.Tx, store *PostgresStore, tenantID, siteID string, plan SiteRulePlan, now time.Time, by string) (SiteRuleSyncSummary, error) {
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
	return SummarizeSiteRulePlan(plan), nil
}

func loadSiteRuleHistoryInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) (map[string]SiteRule, error) {
	rows, err := tx.Query(ctx, `
SELECT current.assignment_id::text, policy.policy
FROM (SELECT DISTINCT ON (assignment_id) tenant_id, site_id, assignment_id, policy_revision_id
      FROM alarm_runtime.alarm_policy_assignment
      WHERE tenant_id = $1 AND site_id = $2 AND subject_type = 'SITE' AND subject_id = $2
      ORDER BY assignment_id, assignment_revision DESC) AS current
JOIN alarm_runtime.alarm_policy_revision policy
  ON policy.tenant_id = current.tenant_id AND policy.site_id = current.site_id AND policy.policy_revision_id = current.policy_revision_id`,
		tenantID, siteID)
	if err != nil {
		return nil, fmt.Errorf("read Site Alarm rule history: %w", err)
	}
	defer rows.Close()
	history := make(map[string]SiteRule)
	for rows.Next() {
		var rule SiteRule
		var policyJSON []byte
		if err := rows.Scan(&rule.AssignmentID, &policyJSON); err != nil {
			return nil, fmt.Errorf("scan Site Alarm rule history: %w", err)
		}
		if rule.Policy, err = decodeAlarmPolicy(policyJSON); err != nil {
			return nil, err
		}
		history[rule.AssignmentID] = rule
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Site Alarm rule history: %w", err)
	}
	return history, nil
}

func loadActiveAlarmCountsInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) (map[string]int, error) {
	rows, err := tx.Query(ctx, `
SELECT assignment_id::text, COUNT(*)::int
FROM alarm_runtime.alarm_evaluation_state
WHERE tenant_id = $1 AND site_id = $2 AND active_alarm_id IS NOT NULL
GROUP BY assignment_id`, tenantID, siteID)
	if err != nil {
		return nil, fmt.Errorf("count active Alarms by rule: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var assignmentID string
		var count int
		if err := rows.Scan(&assignmentID, &count); err != nil {
			return nil, fmt.Errorf("scan active Alarm count by rule: %w", err)
		}
		counts[assignmentID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active Alarm counts by rule: %w", err)
	}
	return counts, nil
}

func latestSiteRuleBundleMetaInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) (SiteRuleBundleMeta, error) {
	var meta SiteRuleBundleMeta
	err := tx.QueryRow(ctx, `
SELECT bundle_revision, bundle_digest
FROM alarm_runtime.site_rule_bundle_revision
WHERE tenant_id = $1 AND site_id = $2
ORDER BY bundle_revision DESC
LIMIT 1`, tenantID, siteID).Scan(&meta.BundleRevision, &meta.BundleDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteRuleBundleMeta{}, nil
	}
	if err != nil {
		return SiteRuleBundleMeta{}, fmt.Errorf("read latest Site Alarm rule bundle revision: %w", err)
	}
	return meta, nil
}

func insertSiteRuleBundleRevisionInTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, siteID string,
	revision uint64,
	source string,
	rolledBackFrom *uint64,
	changeNote string,
	summary SiteRuleSyncSummary,
	rules []SiteRule,
	publishedAt time.Time,
	publishedBy string,
	publishedByName string,
) (string, string, error) {
	sortedRules := cloneSiteRules(rules)
	sort.Slice(sortedRules, func(left, right int) bool {
		return sortedRules[left].AssignmentID < sortedRules[right].AssignmentID
	})
	digest, err := computeSiteRuleBundleDigest(sortedRules)
	if err != nil {
		return "", "", err
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return "", "", fmt.Errorf("encode Site rule bundle summary: %w", err)
	}
	snapshotJSON, err := json.Marshal(sortedRules)
	if err != nil {
		return "", "", fmt.Errorf("encode Site rule bundle snapshot: %w", err)
	}
	by, byName := normalizeRuleActor(publishedBy, publishedByName)
	note := strings.TrimSpace(changeNote)
	_, err = tx.Exec(ctx, `
INSERT INTO alarm_runtime.site_rule_bundle_revision (
	tenant_id, site_id, bundle_revision, bundle_digest, change_note, source, rolled_back_from_revision,
	summary, rules_snapshot, published_at, published_by, published_by_name
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		tenantID, siteID, revision, digest, note, source, rolledBackFrom,
		summaryJSON, snapshotJSON, publishedAt.UTC(), by, byName)
	if err != nil {
		return "", "", fmt.Errorf("record Site Alarm rule bundle revision: %w", err)
	}
	return digest, note, nil
}

func normalizeRuleActor(actorID, actorName string) (string, string) {
	by := strings.TrimSpace(actorID)
	if by == "" {
		by = "principal:operator"
	}
	return truncateUTF8Bytes(by, 256), truncateUTF8Bytes(strings.TrimSpace(actorName), 256)
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func validationErrorCodeForIssues(issues []SiteRuleValidationIssue) string {
	for _, issue := range issues {
		switch issue.Code {
		case "RULE_CYCLE_DETECTED", "UNKNOWN_DEPENDENCY", "SELF_DEPENDENCY", "MAX_LINKAGE_HOPS_EXCEEDED", "DUPLICATE_RULE_ID":
			return "ALARM_RULE_GRAPH_INVALID"
		}
	}
	return "ALARM_RULE_DRAFT_INVALID"
}

// LatestSiteRuleBundleMeta returns the latest published rule bundle revision and digest for a site.
func (store *PostgresStore) LatestSiteRuleBundleMeta(ctx context.Context, tenantID, siteID string) (SiteRuleBundleMeta, error) {
	if store == nil || store.pool == nil {
		return SiteRuleBundleMeta{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) {
		return SiteRuleBundleMeta{}, ErrNotFound
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return SiteRuleBundleMeta{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
}

// DiffSiteRules validates candidate drafts and returns structural deltas, active-alarm impacts, and DAG layout without mutating state.
func (store *PostgresStore) DiffSiteRules(ctx context.Context, tenantID, siteID string, drafts []SiteRuleDraft) (SiteRuleDiffResult, error) {
	if store == nil || store.pool == nil {
		return SiteRuleDiffResult{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) {
		return SiteRuleDiffResult{}, ErrNotFound
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	history, err := loadSiteRuleHistoryInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	activeAlarmCounts, err := loadActiveAlarmCountsInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	currentDigest := latestMeta.BundleDigest
	if currentDigest == "" {
		currentDigest, err = computeSiteRuleBundleDigest(active)
		if err != nil {
			return SiteRuleDiffResult{}, err
		}
	}
	currentReport, err := ValidateSiteRuleGraph(active)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}

	compiled, nextReport, issues := compileDraftsAgainstHistory(drafts, history, nil)
	if len(issues) > 0 {
		return SiteRuleDiffResult{
			Valid:           false,
			CurrentRevision: latestMeta.BundleRevision,
			CurrentDigest:   currentDigest,
			Summary:         SummarizeSiteRulePlan(SiteRulePlan{}),
			Changes:         []SiteRuleChangeEntry{},
			Graph:           buildSiteRuleGraphView(active, currentReport, nil),
			Issues:          issues,
		}, nil
	}
	plan, err := PlanSiteRules(compiled, active)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	activeByID := make(map[string]SiteRule, len(active))
	for _, rule := range active {
		activeByID[rule.AssignmentID] = rule
	}
	changes, changeByID, err := buildDiffChangeEntries(plan, activeByID, currentReport, nextReport, activeAlarmCounts)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	nextDigest, err := computeSiteRuleBundleDigest(compiled)
	if err != nil {
		return SiteRuleDiffResult{}, err
	}
	return SiteRuleDiffResult{
		Valid:           true,
		CurrentRevision: latestMeta.BundleRevision,
		CurrentDigest:   currentDigest,
		NextDigest:      nextDigest,
		Summary:         SummarizeSiteRulePlan(plan),
		Changes:         changes,
		Graph:           buildSiteRuleGraphView(compiled, nextReport, changeByID),
		Issues:          []SiteRuleValidationIssue{},
	}, nil
}

// PublishSiteRules atomically hot-publishes edited site rules under optimistic locking and records a bundle snapshot.
func (store *PostgresStore) PublishSiteRules(ctx context.Context, tenantID, siteID string, req SiteRulePublishRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error) {
	if store == nil || store.pool == nil {
		return SiteRulePublishResult{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || now.IsZero() {
		return SiteRulePublishResult{}, errors.New("site rule publish metadata is invalid")
	}
	changeNote, err := validateChangeNote(req.ChangeNote)
	if err != nil {
		return SiteRulePublishResult{}, &RuleDraftValidationError{
			Code:    "ALARM_RULE_DRAFT_INVALID",
			Message: err.Error(),
			Issues:  []SiteRuleValidationIssue{{Field: "changeNote", Code: "INVALID_CHANGE_NOTE", Message: err.Error()}},
		}
	}

	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSiteRulesStreamInTx(ctx, tx, tenantID, siteID); err != nil {
		return SiteRulePublishResult{}, err
	}

	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	if req.ExpectedRevision != latestMeta.BundleRevision {
		return SiteRulePublishResult{}, &RuleBundleConflictError{
			ExpectedRevision: req.ExpectedRevision,
			CurrentRevision:  latestMeta.BundleRevision,
		}
	}
	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	history, err := loadSiteRuleHistoryInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}

	compiled, nextReport, issues := compileDraftsAgainstHistory(req.Rules, history, func() (string, error) {
		return store.newID(now)
	})
	if len(issues) > 0 {
		return SiteRulePublishResult{}, &RuleDraftValidationError{
			Code:    validationErrorCodeForIssues(issues),
			Message: issues[0].Message,
			Issues:  issues,
		}
	}

	plan, err := PlanSiteRules(compiled, active)
	if err != nil {
		return SiteRulePublishResult{}, &RuleDraftValidationError{
			Code:    "ALARM_RULE_GRAPH_INVALID",
			Message: err.Error(),
			Issues:  []SiteRuleValidationIssue{graphErrorValidationIssue(err)},
		}
	}
	summary := SummarizeSiteRulePlan(plan)
	if latestMeta.BundleRevision > 0 && !hasSiteRulePlanChanges(summary) {
		views, err := buildSiteRuleViews(active, nextReport)
		if err != nil {
			return SiteRulePublishResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SiteRulePublishResult{}, fmt.Errorf("commit idempotent Site rule publish: %w", err)
		}
		by, byName := normalizeRuleActor(actorID, actorName)
		return SiteRulePublishResult{
			BundleRevision:  latestMeta.BundleRevision,
			BundleDigest:    latestMeta.BundleDigest,
			Source:          RuleBundleSourceWebWorkbench,
			Changed:         false,
			ChangeNote:      changeNote,
			PublishedAt:     now.UTC().Format(time.RFC3339Nano),
			PublishedBy:     by,
			PublishedByName: byName,
			Summary:         summary,
			Graph:           buildSiteRuleGraphView(active, nextReport, nil),
			Items:           views,
		}, nil
	}

	by, byName := normalizeRuleActor(actorID, actorName)
	if _, err := applySiteRulePlanInTx(ctx, tx, store, tenantID, siteID, plan, now, by); err != nil {
		return SiteRulePublishResult{}, err
	}
	nextRules, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	finalReport, err := ValidateSiteRuleGraph(nextRules)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	newRevision := latestMeta.BundleRevision + 1
	digest, storedNote, err := insertSiteRuleBundleRevisionInTx(
		ctx, tx, tenantID, siteID, newRevision, RuleBundleSourceWebWorkbench, nil, changeNote, summary, nextRules, now, by, byName,
	)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	bundleStamp, assignmentCount, err := store.siteRuleCacheStampInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	views, err := buildSiteRuleViews(nextRules, finalReport)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteRulePublishResult{}, fmt.Errorf("commit Site Alarm rule publish: %w", err)
	}
	store.ruleCache.Put(tenantID, siteID, bundleStamp, assignmentCount, nextRules)
	return SiteRulePublishResult{
		BundleRevision:  newRevision,
		BundleDigest:    digest,
		Source:          RuleBundleSourceWebWorkbench,
		Changed:         true,
		ChangeNote:      storedNote,
		PublishedAt:     now.UTC().Format(time.RFC3339Nano),
		PublishedBy:     by,
		PublishedByName: byName,
		Summary:         summary,
		Graph:           buildSiteRuleGraphView(nextRules, finalReport, nil),
		Items:           views,
	}, nil
}

// ListSiteRuleBundleRevisions returns immutable revision snapshots for a site newest-first.
func (store *PostgresStore) ListSiteRuleBundleRevisions(ctx context.Context, tenantID, siteID string, limit int) (SiteRuleBundleRevisionList, error) {
	if store == nil || store.pool == nil {
		return SiteRuleBundleRevisionList{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) {
		return SiteRuleBundleRevisionList{}, ErrNotFound
	}
	limit = normalizeBundleRevisionLimit(limit)
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return SiteRuleBundleRevisionList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRuleBundleRevisionList{}, err
	}
	rows, err := tx.Query(ctx, `
SELECT bundle_revision, bundle_digest, change_note, source, rolled_back_from_revision,
       summary, jsonb_array_length(rules_snapshot), published_at, published_by, published_by_name
FROM alarm_runtime.site_rule_bundle_revision
WHERE tenant_id = $1 AND site_id = $2
ORDER BY bundle_revision DESC
LIMIT $3`, tenantID, siteID, limit)
	if err != nil {
		return SiteRuleBundleRevisionList{}, fmt.Errorf("list Site Alarm rule revisions: %w", err)
	}
	defer rows.Close()
	items := make([]SiteRuleBundleRevisionView, 0)
	for rows.Next() {
		var view SiteRuleBundleRevisionView
		var summaryJSON []byte
		var rolledBackFrom *int64
		var publishedAt time.Time
		if err := rows.Scan(
			&view.BundleRevision, &view.BundleDigest, &view.ChangeNote, &view.Source, &rolledBackFrom,
			&summaryJSON, &view.RuleCount, &publishedAt, &view.PublishedBy, &view.PublishedByName,
		); err != nil {
			return SiteRuleBundleRevisionList{}, fmt.Errorf("scan Site Alarm rule revision: %w", err)
		}
		if rolledBackFrom != nil && *rolledBackFrom > 0 {
			converted := uint64(*rolledBackFrom)
			view.RolledBackFromRevision = &converted
		}
		if err := json.Unmarshal(summaryJSON, &view.Summary); err != nil {
			return SiteRuleBundleRevisionList{}, fmt.Errorf("decode Site Alarm rule revision summary: %w", err)
		}
		view.PublishedAt = publishedAt.UTC().Format(time.RFC3339Nano)
		items = append(items, view)
	}
	if err := rows.Err(); err != nil {
		return SiteRuleBundleRevisionList{}, fmt.Errorf("iterate Site Alarm rule revisions: %w", err)
	}
	return SiteRuleBundleRevisionList{
		CurrentRevision: latestMeta.BundleRevision,
		Items:           items,
	}, nil
}

// RollbackSiteRules restores a historical immutable rule snapshot under optimistic revision locking.
func (store *PostgresStore) RollbackSiteRules(ctx context.Context, tenantID, siteID string, targetRevision uint64, req SiteRuleRollbackRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error) {
	if store == nil || store.pool == nil {
		return SiteRulePublishResult{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || now.IsZero() {
		return SiteRulePublishResult{}, errors.New("site rule rollback metadata is invalid")
	}
	if targetRevision == 0 || targetRevision > math.MaxInt64 {
		return SiteRulePublishResult{}, ErrNotFound
	}
	changeNote, err := validateChangeNote(req.ChangeNote)
	if err != nil {
		return SiteRulePublishResult{}, &RuleDraftValidationError{
			Code:    "ALARM_RULE_DRAFT_INVALID",
			Message: err.Error(),
			Issues:  []SiteRuleValidationIssue{{Field: "changeNote", Code: "INVALID_CHANGE_NOTE", Message: err.Error()}},
		}
	}
	if changeNote == "" {
		changeNote = fmt.Sprintf("回滚至版本 v%d", targetRevision)
	}

	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSiteRulesStreamInTx(ctx, tx, tenantID, siteID); err != nil {
		return SiteRulePublishResult{}, err
	}

	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	if req.ExpectedRevision != latestMeta.BundleRevision {
		return SiteRulePublishResult{}, &RuleBundleConflictError{
			ExpectedRevision: req.ExpectedRevision,
			CurrentRevision:  latestMeta.BundleRevision,
		}
	}
	var snapshotJSON []byte
	err = tx.QueryRow(ctx, `
SELECT rules_snapshot
FROM alarm_runtime.site_rule_bundle_revision
WHERE tenant_id = $1 AND site_id = $2 AND bundle_revision = $3`,
		tenantID, siteID, targetRevision).Scan(&snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteRulePublishResult{}, ErrNotFound
	}
	if err != nil {
		return SiteRulePublishResult{}, fmt.Errorf("read target Site Alarm rule bundle snapshot: %w", err)
	}
	var targetRules []SiteRule
	if err := json.Unmarshal(snapshotJSON, &targetRules); err != nil {
		return SiteRulePublishResult{}, fmt.Errorf("decode target Site Alarm rule bundle snapshot: %w", err)
	}
	for index := range targetRules {
		targetRules[index].Policy.PolicyRevisionID = ""
		targetRules[index].Policy.Revision = 0
		targetRules[index].Policy.Digest = ""
	}
	active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	plan, err := PlanSiteRules(targetRules, active)
	if err != nil {
		return SiteRulePublishResult{}, &RuleDraftValidationError{
			Code:    "ALARM_RULE_GRAPH_INVALID",
			Message: err.Error(),
			Issues:  []SiteRuleValidationIssue{graphErrorValidationIssue(err)},
		}
	}
	summary := SummarizeSiteRulePlan(plan)
	by, byName := normalizeRuleActor(actorID, actorName)
	fromRev := targetRevision
	if !hasSiteRulePlanChanges(summary) {
		report, err := ValidateSiteRuleGraph(active)
		if err != nil {
			return SiteRulePublishResult{}, err
		}
		views, err := buildSiteRuleViews(active, report)
		if err != nil {
			return SiteRulePublishResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SiteRulePublishResult{}, fmt.Errorf("commit idempotent Site rule rollback: %w", err)
		}
		return SiteRulePublishResult{
			BundleRevision:         latestMeta.BundleRevision,
			BundleDigest:           latestMeta.BundleDigest,
			Source:                 RuleBundleSourceRollback,
			Changed:                false,
			RolledBackFromRevision: &fromRev,
			ChangeNote:             changeNote,
			PublishedAt:            now.UTC().Format(time.RFC3339Nano),
			PublishedBy:            by,
			PublishedByName:        byName,
			Summary:                summary,
			Graph:                  buildSiteRuleGraphView(active, report, nil),
			Items:                  views,
		}, nil
	}

	if _, err := applySiteRulePlanInTx(ctx, tx, store, tenantID, siteID, plan, now, by); err != nil {
		return SiteRulePublishResult{}, err
	}
	nextRules, err := siteRulesInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	finalReport, err := ValidateSiteRuleGraph(nextRules)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	newRevision := latestMeta.BundleRevision + 1
	digest, storedNote, err := insertSiteRuleBundleRevisionInTx(
		ctx, tx, tenantID, siteID, newRevision, RuleBundleSourceRollback, &fromRev, changeNote, summary, nextRules, now, by, byName,
	)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	bundleStamp, assignmentCount, err := store.siteRuleCacheStampInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	views, err := buildSiteRuleViews(nextRules, finalReport)
	if err != nil {
		return SiteRulePublishResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteRulePublishResult{}, fmt.Errorf("commit Site Alarm rule rollback: %w", err)
	}
	store.ruleCache.Put(tenantID, siteID, bundleStamp, assignmentCount, nextRules)
	return SiteRulePublishResult{
		BundleRevision:         newRevision,
		BundleDigest:           digest,
		Source:                 RuleBundleSourceRollback,
		Changed:                true,
		RolledBackFromRevision: &fromRev,
		ChangeNote:             storedNote,
		PublishedAt:            now.UTC().Format(time.RFC3339Nano),
		PublishedBy:            by,
		PublishedByName:        byName,
		Summary:                summary,
		Graph:                  buildSiteRuleGraphView(nextRules, finalReport, nil),
		Items:                  views,
	}, nil
}

// CompileSiteRulesEdge compiles either the currently active site rules (drafts == nil)
// or a candidate set of drafts into a verified edge-rule bundle IR without persisting changes.
func (store *PostgresStore) CompileSiteRulesEdge(
	ctx context.Context,
	tenantID, siteID string,
	drafts *[]SiteRuleDraft,
	now time.Time,
) (EdgeRuleCompileResult, error) {
	if store == nil || store.pool == nil {
		return EdgeRuleCompileResult{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) {
		return EdgeRuleCompileResult{}, ErrNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return EdgeRuleCompileResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	latestMeta, err := latestSiteRuleBundleMetaInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeRuleCompileResult{}, err
	}
	if drafts == nil {
		active, err := siteRulesInTx(ctx, tx, tenantID, siteID)
		if err != nil {
			return EdgeRuleCompileResult{}, err
		}
		bundleDigest := latestMeta.BundleDigest
		if bundleDigest == "" {
			computed, err := computeSiteRuleBundleDigest(active)
			if err != nil {
				return EdgeRuleCompileResult{}, err
			}
			bundleDigest = computed
		}
		return CompileSiteRulesForEdge(siteID, latestMeta.BundleRevision, bundleDigest, active, now)
	}

	history, err := loadSiteRuleHistoryInTx(ctx, tx, tenantID, siteID)
	if err != nil {
		return EdgeRuleCompileResult{}, err
	}
	compiled, _, issues := compileDraftsAgainstHistory(*drafts, history, nil)
	if len(issues) > 0 {
		return EdgeRuleCompileResult{}, &RuleDraftValidationError{
			Code:    validationErrorCodeForIssues(issues),
			Message: issues[0].Message,
			Issues:  issues,
		}
	}
	result, err := CompileSiteRulesForEdge(siteID, latestMeta.BundleRevision, "", compiled, now)
	if err != nil {
		return EdgeRuleCompileResult{}, &RuleDraftValidationError{
			Code:    "ALARM_RULE_DRAFT_INVALID",
			Message: err.Error(),
		}
	}
	return result, nil
}
