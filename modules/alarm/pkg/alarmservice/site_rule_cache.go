package alarmservice

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// cachedSiteRuleSnapshot holds an immutable, decoded rule set for a single site
// stamped with the monotonic bundle and assignment versions observed in PostgreSQL.
type cachedSiteRuleSnapshot struct {
	BundleRevision  uint64
	AssignmentCount uint64
	Rules           []SiteRule
}

// siteRuleCache provides lock-free reads for compiled site alarm rules via
// atomic copy-on-write map replacement. Zero value is ready for concurrent use.
type siteRuleCache struct {
	entries atomic.Pointer[map[string]*cachedSiteRuleSnapshot]
}

func siteRuleCacheKey(tenantID, siteID string) string {
	return tenantID + ":" + siteID
}

// Get returns a defensive copy of the cached site rules when the cached bundle
// revision and assignment count match the caller's PostgreSQL transaction view.
func (cache *siteRuleCache) Get(tenantID, siteID string, bundleRevision, assignmentCount uint64) ([]SiteRule, bool) {
	if cache == nil {
		return nil, false
	}
	current := cache.entries.Load()
	if current == nil {
		return nil, false
	}
	entry, ok := (*current)[siteRuleCacheKey(tenantID, siteID)]
	if !ok || entry == nil {
		return nil, false
	}
	if entry.BundleRevision != bundleRevision || entry.AssignmentCount != assignmentCount {
		return nil, false
	}
	return cloneSiteRules(entry.Rules), true
}

// Put stores a defensive copy of rules under (bundleRevision, assignmentCount),
// ignoring out-of-order stale puts from older transactions.
func (cache *siteRuleCache) Put(tenantID, siteID string, bundleRevision, assignmentCount uint64, rules []SiteRule) {
	if cache == nil {
		return
	}
	key := siteRuleCacheKey(tenantID, siteID)
	snapshot := &cachedSiteRuleSnapshot{
		BundleRevision:  bundleRevision,
		AssignmentCount: assignmentCount,
		Rules:           cloneSiteRules(rules),
	}
	for {
		old := cache.entries.Load()
		if old != nil {
			if existing, ok := (*old)[key]; ok && existing != nil && !snapshotIsNewer(snapshot, existing) {
				return
			}
		}
		next := cloneCacheMap(old)
		next[key] = snapshot
		if cache.entries.CompareAndSwap(old, &next) {
			return
		}
	}
}

// Invalidate drops the cached rules for a site so the next evaluation reloads from PostgreSQL.
func (cache *siteRuleCache) Invalidate(tenantID, siteID string) {
	if cache == nil {
		return
	}
	key := siteRuleCacheKey(tenantID, siteID)
	for {
		old := cache.entries.Load()
		if old == nil {
			return
		}
		if _, ok := (*old)[key]; !ok {
			return
		}
		next := cloneCacheMap(old)
		delete(next, key)
		if cache.entries.CompareAndSwap(old, &next) {
			return
		}
	}
}

func snapshotIsNewer(candidate, existing *cachedSiteRuleSnapshot) bool {
	if candidate.BundleRevision != existing.BundleRevision {
		return candidate.BundleRevision > existing.BundleRevision
	}
	return candidate.AssignmentCount > existing.AssignmentCount
}

func cloneCacheMap(source *map[string]*cachedSiteRuleSnapshot) map[string]*cachedSiteRuleSnapshot {
	if source == nil || len(*source) == 0 {
		return make(map[string]*cachedSiteRuleSnapshot, 1)
	}
	cloned := make(map[string]*cachedSiteRuleSnapshot, len(*source)+1)
	for key, value := range *source {
		cloned[key] = value
	}
	return cloned
}

func cloneSiteRules(rules []SiteRule) []SiteRule {
	if rules == nil {
		return nil
	}
	out := make([]SiteRule, len(rules))
	for index, rule := range rules {
		out[index] = cloneSiteRule(rule)
	}
	return out
}

func cloneSiteRule(rule SiteRule) SiteRule {
	cloned := rule
	cloned.Policy = cloneAlarmPolicyRevision(rule.Policy)
	return cloned
}

func cloneAlarmPolicyRevision(policy AlarmPolicyRevision) AlarmPolicyRevision {
	cloned := policy
	cloned.DependsOnRuleIDs = slices.Clone(policy.DependsOnRuleIDs)
	cloned.OverriddenFields = slices.Clone(policy.OverriddenFields)
	if policy.Actions != nil {
		actions := make([]RuleAction, len(policy.Actions))
		for i, action := range policy.Actions {
			actions[i] = action
			actions[i].Parameters = maps.Clone(action.Parameters)
		}
		cloned.Actions = actions
	}
	cloned.Raise = cloneCondition(policy.Raise)
	cloned.Clear = cloneCondition(policy.Clear)
	if policy.Schedule != nil {
		sched := *policy.Schedule
		if policy.Schedule.Windows != nil {
			windows := make([]ScheduleWindow, len(policy.Schedule.Windows))
			for i, w := range policy.Schedule.Windows {
				windows[i] = w
				windows[i].DaysOfWeek = slices.Clone(w.DaysOfWeek)
			}
			sched.Windows = windows
		}
		cloned.Schedule = &sched
	}
	return cloned
}

func (store *PostgresStore) siteRuleCacheStampInTx(ctx context.Context, tx pgx.Tx, tenantID, siteID string) (uint64, uint64, error) {
	var bundleRevision, assignmentCount uint64
	err := tx.QueryRow(ctx, `
SELECT
	COALESCE((SELECT MAX(bundle_revision) FROM alarm_runtime.site_rule_bundle_revision WHERE tenant_id = $1 AND site_id = $2), 0),
	(SELECT COUNT(*) FROM alarm_runtime.alarm_policy_assignment WHERE tenant_id = $1 AND site_id = $2 AND subject_type = 'SITE' AND subject_id = $2)`,
		tenantID, siteID,
	).Scan(&bundleRevision, &assignmentCount)
	if err != nil {
		return 0, 0, fmt.Errorf("read site rule cache stamp: %w", err)
	}
	return bundleRevision, assignmentCount, nil
}

func cloneFloatPtr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneCondition(condition Condition) Condition {
	cloned := condition
	cloned.Minimum = cloneFloatPtr(condition.Minimum)
	cloned.Maximum = cloneFloatPtr(condition.Maximum)
	cloned.Delta = cloneFloatPtr(condition.Delta)
	cloned.Threshold = cloneFloatPtr(condition.Threshold)
	cloned.Reasons = slices.Clone(condition.Reasons)
	cloned.ExprInputs = maps.Clone(condition.ExprInputs)
	if condition.Children != nil {
		children := make([]Condition, len(condition.Children))
		for i, child := range condition.Children {
			children[i] = cloneCondition(child)
		}
		cloned.Children = children
	}
	return cloned
}
