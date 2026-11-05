package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// A rule that has been unjudgeable this long raises one Site Alarm (#749). Rules that watch the
// data itself are left out: a stale, flat or missing reading already has its own Alarm, and
// "reports interrupted" says the whole Site is silent.
const (
	RulesUnevaluableAlarmType       = "rules-unevaluable"
	rulesUnevaluableSourceReference = "alarm-watchdog:rules-unevaluable"
	rulesUnevaluableRuleRevision    = "alarm-watchdog/rules-unevaluable@1"
	rulesUnevaluableAfter           = 30 * time.Minute
	rulesUnevaluableListed          = 10
)

var rulesUnevaluableExcludedPrefixes = []string{"data-quality/", "readings-not-updated", "readings-flat", "reports-interrupted", RulesUnevaluableAlarmType}

// UnevaluableRule is one rule the watchdog lists.
type UnevaluableRule struct {
	Title              string
	AlarmType          string
	Blocker            string
	IndeterminateSince time.Time
}

func watchedForUnevaluable(alarmType string) bool {
	for _, prefix := range rulesUnevaluableExcludedPrefixes {
		if strings.HasPrefix(alarmType, prefix) {
			return false
		}
	}
	return true
}

// blockerLabel says in a few words why a rule cannot be judged.
func blockerLabel(blocker string) string {
	switch {
	case strings.HasPrefix(blocker, "MISSING_INPUT"):
		return "缺输入"
	case strings.HasPrefix(blocker, "STALE_INPUT"):
		return "输入过期"
	case strings.HasPrefix(blocker, "INVALID_INPUT"):
		return "输入无效"
	case strings.HasPrefix(blocker, "UNTRUSTED_QUALITY"):
		return "输入不可信"
	default:
		return "无法判断"
	}
}

// RulesUnevaluableSummary lists the rules, the longest-unjudgeable first, at most ten by name.
func RulesUnevaluableSummary(rules []UnevaluableRule, now time.Time) string {
	sorted := append([]UnevaluableRule{}, rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].IndeterminateSince.Equal(sorted[j].IndeterminateSince) {
			return sorted[i].IndeterminateSince.Before(sorted[j].IndeterminateSince)
		}
		return sorted[i].Title < sorted[j].Title
	})
	parts := make([]string, 0, rulesUnevaluableListed)
	for index, rule := range sorted {
		if index == rulesUnevaluableListed {
			break
		}
		minutes := int(now.Sub(rule.IndeterminateSince) / time.Minute)
		parts = append(parts, fmt.Sprintf("%s（%s，%d 分钟）", rule.Title, blockerLabel(rule.Blocker), minutes))
	}
	summary := fmt.Sprintf("%d 条告警规则已有 %d 分钟以上无法判断，这期间既不会报警也不会恢复：%s", len(sorted), int(rulesUnevaluableAfter/time.Minute), strings.Join(parts, "；"))
	if len(sorted) > rulesUnevaluableListed {
		summary += fmt.Sprintf("；另有 %d 条", len(sorted)-rulesUnevaluableListed)
	}
	return summary + "。"
}

// WatchUnevaluableRules raises a "rules-unevaluable" Alarm on each Site with rules unjudgeable
// for 30 minutes or more, and recovers it once none is left. An Alarm already active is left as
// it is, so the pass does not add an occurrence every minute.
func (store *PostgresStore) WatchUnevaluableRules(ctx context.Context, now time.Time) (raised, cleared int, err error) {
	if store == nil || store.pool == nil {
		return 0, 0, ErrUnavailable
	}
	since := now.UTC().Add(-rulesUnevaluableAfter)
	rows, err := store.pool.Query(ctx, `SELECT tenant_id::text FROM alarm_runtime.tenants_with_unevaluable_rules($1) AS due(tenant_id)`, since)
	if err != nil {
		return 0, 0, fmt.Errorf("find Tenants with unevaluable Alarm rules: %w", err)
	}
	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan Tenant with unevaluable Alarm rules: %w", err)
		}
		tenants = append(tenants, tenantID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate Tenants with unevaluable Alarm rules: %w", err)
	}
	var failures []error
	for _, tenantID := range tenants {
		tenantContext := identitycontext.WithTenantID(ctx, tenantID)
		r, c, err := store.watchTenantUnevaluableRules(tenantContext, tenantID, now, since)
		raised += r
		cleared += c
		if err != nil {
			failures = append(failures, err)
		}
	}
	return raised, cleared, errors.Join(failures...)
}

type activeWatchdogAlarm struct{ fingerprint, incidentCorrelationID string }

func (store *PostgresStore) watchTenantUnevaluableRules(ctx context.Context, tenantID string, now, since time.Time) (raised, cleared int, err error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT state.site_id::text, policy.policy->>'title', policy.policy->>'alarmType', COALESCE(state.quality_blocker, ''), state.indeterminate_since
FROM alarm_runtime.alarm_evaluation_state AS state
JOIN (SELECT DISTINCT ON (site_id, assignment_id) site_id, assignment_id, retired
      FROM alarm_runtime.alarm_policy_assignment
      WHERE tenant_id = $1
      ORDER BY site_id, assignment_id, assignment_revision DESC) AS current
  ON current.site_id = state.site_id AND current.assignment_id = state.assignment_id
JOIN alarm_runtime.alarm_policy_revision AS policy
  ON policy.tenant_id = state.tenant_id AND policy.site_id = state.site_id AND policy.policy_revision_id = state.policy_revision_id
WHERE state.tenant_id = $1 AND state.indeterminate_since IS NOT NULL AND state.indeterminate_since <= $2
  AND NOT current.retired`, tenantID, since)
	if err != nil {
		return 0, 0, fmt.Errorf("read unevaluable Alarm rules: %w", err)
	}
	bySite := map[string][]UnevaluableRule{}
	for rows.Next() {
		var siteID string
		var rule UnevaluableRule
		if err := rows.Scan(&siteID, &rule.Title, &rule.AlarmType, &rule.Blocker, &rule.IndeterminateSince); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan unevaluable Alarm rule: %w", err)
		}
		if watchedForUnevaluable(rule.AlarmType) {
			bySite[siteID] = append(bySite[siteID], rule)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate unevaluable Alarm rules: %w", err)
	}
	active := map[string]activeWatchdogAlarm{}
	rows, err = tx.Query(ctx, `
SELECT site_id::text, fingerprint, incident_correlation_id::text
FROM alarm_runtime.alarm_current
WHERE tenant_id = $1 AND alarm_type = $2 AND condition = 'ACTIVE'`, tenantID, RulesUnevaluableAlarmType)
	if err != nil {
		return 0, 0, fmt.Errorf("read active rules-unevaluable Alarms: %w", err)
	}
	for rows.Next() {
		var siteID string
		var alarm activeWatchdogAlarm
		if err := rows.Scan(&siteID, &alarm.fingerprint, &alarm.incidentCorrelationID); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan active rules-unevaluable Alarm: %w", err)
		}
		active[siteID] = alarm
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate active rules-unevaluable Alarms: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}

	var failures []error
	for siteID, rules := range bySite {
		if _, ok := active[siteID]; ok {
			continue
		}
		correlationID, err := store.newID(now)
		if err != nil {
			return raised, cleared, err
		}
		_, err = store.Publish(ctx, tenantID, siteID, Publication{
			AlarmType: RulesUnevaluableAlarmType, SourceType: alarmmodel.SourceSiteRule, SourceReference: rulesUnevaluableSourceReference,
			RuleRevision: rulesUnevaluableRuleRevision, Title: "告警规则长时间无法判断", Summary: RulesUnevaluableSummary(rules, now),
			Severity: alarmmodel.SeverityWarning, OccurredAt: now.UTC().Format(time.RFC3339Nano),
			ActorType: "WORKLOAD", ActorID: "alarm-watchdog", CorrelationID: correlationID,
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("raise rules-unevaluable Alarm: %w", err))
			continue
		}
		raised++
	}
	for siteID, alarm := range active {
		if _, ok := bySite[siteID]; ok {
			continue
		}
		correlationID, err := store.newID(now)
		if err != nil {
			return raised, cleared, err
		}
		_, err = store.ClearActive(ctx, tenantID, siteID, Recovery{
			Fingerprint: alarm.fingerprint, IncidentCorrelationID: alarm.incidentCorrelationID,
			OccurredAt: now.UTC().Format(time.RFC3339Nano), Reason: "RULES_EVALUABLE_AGAIN", RuleRevision: rulesUnevaluableRuleRevision,
			ActorType: "WORKLOAD", ActorID: "alarm-watchdog", CorrelationID: correlationID,
		})
		if err != nil && !errors.Is(err, ErrNotFound) {
			failures = append(failures, fmt.Errorf("recover rules-unevaluable Alarm: %w", err))
			continue
		}
		cleared++
	}
	return raised, cleared, errors.Join(failures...)
}
