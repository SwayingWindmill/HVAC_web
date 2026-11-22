package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type dueEvaluationRunner interface {
	RunDueEvaluations(ctx context.Context, workerID string, now time.Time) (DueEvaluationResult, error)
	WatchUnevaluableRules(ctx context.Context, now time.Time) (raised, cleared int, err error)
	PruneExpiredEvaluationEvidence(ctx context.Context) (int64, error)
}

// A pass runs every minute and claims at most this many due evaluations per Tenant, each for one
// lease; the next pass takes the rest.
const (
	dueEvaluationInterval = time.Minute
	dueEvaluationBatch    = 100
	dueEvaluationLease    = time.Minute
	evidencePruneBatch    = 5000
)

// RunScheduledEvaluations runs a pass every minute until ctx ends, wherever the Alarm owner runs:
// alone or inside energy-api. Each pass also raises or recovers the "rules-unevaluable" Alarm
// (#749) and deletes evaluation evidence past its 30 days.
func RunScheduledEvaluations(ctx context.Context, store *PostgresStore, logger *slog.Logger) {
	runScheduledEvaluations(ctx, store, logger, dueEvaluationInterval)
}

// StartScheduledEvaluations launches the scheduled evaluation loop and returns a channel that closes when it exits.
func StartScheduledEvaluations(ctx context.Context, store *PostgresStore, logger *slog.Logger) <-chan struct{} {
	return runScheduledEvaluations(ctx, store, logger, dueEvaluationInterval)
}

func runScheduledEvaluations(ctx context.Context, runner dueEvaluationRunner, logger *slog.Logger, interval time.Duration) <-chan struct{} {
	host, err := os.Hostname()
	if err != nil {
		host = "alarm-owner"
	}
	workerID := "alarm-due-evaluations:" + host
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer observability.Recover(logger, nil, "alarm.due_evaluations_loop")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			runDueEvaluationPass(ctx, runner, workerID, logger)
		}
	}()
	return done
}

func runDueEvaluationPass(ctx context.Context, runner dueEvaluationRunner, workerID string, logger *slog.Logger) {
	defer observability.Recover(logger, nil, "alarm.due_evaluations")
	result, err := runner.RunDueEvaluations(ctx, workerID, time.Now())
	if err != nil && logger != nil {
		logger.Error("alarm_due_evaluations_failed", "error_code", "ALARM_DUE_EVALUATIONS_FAILED", "error", err, "failed", result.Failed)
	}
	if result.Raised+result.Cleared+result.Escalated > 0 && logger != nil {
		logger.Info("alarm_due_evaluations", "evaluated", result.Evaluated, "raised", result.Raised, "cleared", result.Cleared, "escalated", result.Escalated)
	}
	raised, cleared, err := runner.WatchUnevaluableRules(ctx, time.Now())
	if err != nil && logger != nil {
		logger.Error("alarm_rules_unevaluable_watch_failed", "error_code", "ALARM_RULES_UNEVALUABLE_WATCH_FAILED", "error", err)
	}
	if raised+cleared > 0 && logger != nil {
		logger.Info("alarm_rules_unevaluable_watch", "raised", raised, "cleared", cleared)
	}
	pruned, err := runner.PruneExpiredEvaluationEvidence(ctx)
	if err != nil && logger != nil {
		logger.Error("alarm_evaluation_evidence_prune_failed", "error_code", "ALARM_EVALUATION_EVIDENCE_PRUNE_FAILED", "error", err)
	}
	if pruned > 0 && logger != nil {
		logger.Info("alarm_evaluation_evidence_pruned", "deleted", pruned)
	}
}

// DueEvaluationResult counts what one scheduled pass did.
type DueEvaluationResult struct {
	Evaluated int
	Raised    int
	Cleared   int
	Escalated int
	Failed    int
}

// RunDueEvaluations looks again at every evaluation whose time has come, across Tenants, with the
// snapshot it last saw (#615). Telemetry evaluates a Site only when a reading arrives, so a Site
// that stopped reporting, or a reading that stopped updating, is noticed only here. One failed
// evaluation does not stop the others; it is counted and the error returned with the rest.
func (store *PostgresStore) RunDueEvaluations(ctx context.Context, workerID string, now time.Time) (DueEvaluationResult, error) {
	if store == nil || store.pool == nil {
		return DueEvaluationResult{}, ErrUnavailable
	}
	rows, err := store.pool.Query(ctx, `SELECT tenant_id::text FROM alarm_runtime.tenants_with_due_evaluations($1) AS due(tenant_id)`, now.UTC())
	if err != nil {
		return DueEvaluationResult{}, fmt.Errorf("find Tenants with due Alarm evaluations: %w", err)
	}
	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			rows.Close()
			return DueEvaluationResult{}, fmt.Errorf("scan Tenant with due Alarm evaluations: %w", err)
		}
		tenants = append(tenants, tenantID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DueEvaluationResult{}, fmt.Errorf("iterate Tenants with due Alarm evaluations: %w", err)
	}

	var result DueEvaluationResult
	var failures []error
	for _, tenantID := range tenants {
		tenantContext := identitycontext.WithTenantID(ctx, tenantID)
		changedBySite := map[string]map[string]bool{}
		recordChanged := func(siteID, assignmentID string, decision EvaluationDecision) {
			if decision.Effect != EvaluationEffectPublish && decision.Effect != EvaluationEffectClear {
				return
			}
			siteChanged := changedBySite[siteID]
			if siteChanged == nil {
				siteChanged = map[string]bool{}
				changedBySite[siteID] = siteChanged
			}
			siteChanged[assignmentID] = true
		}

		// A Site rule published while the Site was already silent has no evaluation, and a new
		// version of a rule whose device is silent has none with that version; no reading will
		// come to make one (#659): evaluate it with the Site's last readings.
		firsts, err := store.evaluateUnevaluatedSiteRules(tenantContext, tenantID, now)
		if err != nil {
			result.Failed++
			failures = append(failures, err)
		}
		for _, evaluated := range firsts {
			result.count(evaluated.decision)
			recordChanged(evaluated.siteID, evaluated.assignmentID, evaluated.decision)
		}
		claims, err := store.ClaimDueEvaluations(tenantContext, tenantID, workerID, now, dueEvaluationLease, dueEvaluationBatch)
		if err != nil {
			result.Failed++
			failures = append(failures, err)
		} else {
			for _, claim := range claims {
				decision, err := store.EvaluateClaim(tenantContext, claim, now)
				if errors.Is(err, ErrEvaluationClaimLost) {
					continue
				}
				if err != nil {
					result.Failed++
					failures = append(failures, err)
					continue
				}
				result.count(decision)
				recordChanged(claim.SiteID, claim.AssignmentID, decision)
			}
		}
		if len(changedBySite) > 0 {
			changedSites := make([]string, 0, len(changedBySite))
			for siteID := range changedBySite {
				changedSites = append(changedSites, siteID)
			}
			sort.Strings(changedSites)
			for _, siteID := range changedSites {
				cascaded, err := store.cascadeDownstreamSiteRules(tenantContext, tenantID, siteID, changedBySite[siteID], now)
				if err != nil {
					result.Failed++
					failures = append(failures, err)
				}
				for _, decision := range cascaded {
					result.count(decision)
				}
			}
		}
		escalated, err := store.escalateUnacknowledgedReadingGaps(tenantContext, tenantID, now)
		result.Escalated += escalated
		if err != nil {
			result.Failed++
			failures = append(failures, err)
		}
	}
	return result, errors.Join(failures...)
}

// PruneExpiredEvaluationEvidence deletes evaluation evidence older than 30 days across Tenants, a
// batch at a time, and returns how many rows it deleted (#651).
func (store *PostgresStore) PruneExpiredEvaluationEvidence(ctx context.Context) (int64, error) {
	if store == nil || store.pool == nil {
		return 0, ErrUnavailable
	}
	var total int64
	for {
		var deleted int64
		if err := store.pool.QueryRow(ctx, `SELECT alarm_runtime.prune_expired_alarm_evaluation_events($1)`, evidencePruneBatch).Scan(&deleted); err != nil {
			return total, fmt.Errorf("prune expired Alarm evaluation evidence: %w", err)
		}
		total += deleted
		if deleted < evidencePruneBatch {
			return total, nil
		}
	}
}

func (result *DueEvaluationResult) count(decision EvaluationDecision) {
	result.Evaluated++
	switch decision.Effect {
	case EvaluationEffectPublish:
		result.Raised++
	case EvaluationEffectClear:
		result.Cleared++
	}
}

type evaluatedRuleDecision struct {
	siteID       string
	assignmentID string
	decision     EvaluationDecision
}

// selectDownstreamSiteRules returns rules in topological order that depend (directly or transitively)
// on any rule in changedAssignmentIDs, excluding the seed rules themselves.
func selectDownstreamSiteRules(ordered []SiteRule, changedAssignmentIDs map[string]bool) []SiteRule {
	if len(ordered) == 0 || len(changedAssignmentIDs) == 0 {
		return nil
	}
	activeFrontier := make(map[string]bool, len(changedAssignmentIDs))
	for id, changed := range changedAssignmentIDs {
		if changed {
			activeFrontier[id] = true
		}
	}
	if len(activeFrontier) == 0 {
		return nil
	}
	downstreamIDs := make(map[string]bool)
	for {
		added := false
		for _, rule := range ordered {
			if changedAssignmentIDs[rule.AssignmentID] || downstreamIDs[rule.AssignmentID] {
				continue
			}
			for _, depID := range rule.Policy.DependsOnRuleIDs {
				if activeFrontier[depID] {
					downstreamIDs[rule.AssignmentID] = true
					activeFrontier[rule.AssignmentID] = true
					added = true
					break
				}
			}
		}
		if !added {
			break
		}
	}
	if len(downstreamIDs) == 0 {
		return nil
	}
	out := make([]SiteRule, 0, len(downstreamIDs))
	for _, rule := range ordered {
		if downstreamIDs[rule.AssignmentID] {
			out = append(out, rule)
		}
	}
	return out
}

func (store *PostgresStore) cascadeDownstreamSiteRules(
	ctx context.Context,
	tenantID, siteID string,
	changedAssignmentIDs map[string]bool,
	now time.Time,
) ([]EvaluationDecision, error) {
	records, rules, upstreamActive, err := store.loadSiteTelemetryEvaluationState(ctx, tenantID, siteID)
	if err != nil || len(records) == 0 || len(rules) == 0 {
		return nil, err
	}
	downstream := selectDownstreamSiteRules(orderSiteRulesForEvaluation(rules), changedAssignmentIDs)
	if len(downstream) == 0 {
		return nil, nil
	}
	newest := records[0]
	for _, record := range records {
		if record.EvaluatedAt.After(newest.EvaluatedAt) {
			newest = record
		}
	}
	snapshot, err := buildSiteEvaluationSnapshot(newest.EventID, tenantID, siteID, records)
	if err != nil {
		return nil, err
	}
	snapshot.UpstreamRuleActive = upstreamActive
	snapshot.ForceReevaluate = true
	decisions := make([]EvaluationDecision, 0, len(downstream))
	for _, rule := range downstream {
		decision, err := store.EvaluateAssignedSnapshot(ctx, rule.AssignmentID, snapshot, now.UTC())
		if errors.Is(err, ErrRuleRetired) {
			delete(upstreamActive, rule.AssignmentID)
			continue
		}
		if err != nil {
			return decisions, err
		}
		upstreamActive[rule.AssignmentID] = decision.State.ActiveAlarmID != ""
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

// evaluateUnevaluatedSiteRules evaluates each Site rule not yet evaluated with the version
// assigned to it, with the Site's last readings, as a reading arriving would.
func (store *PostgresStore) evaluateUnevaluatedSiteRules(ctx context.Context, tenantID string, now time.Time) ([]evaluatedRuleDecision, error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
SELECT current.site_id::text, current.assignment_id::text
FROM (SELECT DISTINCT ON (assignment_id) tenant_id, site_id, assignment_id, assignment_revision, policy_revision_id, retired
      FROM alarm_runtime.alarm_policy_assignment
      WHERE tenant_id = $1 AND subject_type = 'SITE'
      ORDER BY assignment_id, assignment_revision DESC) AS current
WHERE NOT current.retired
  AND EXISTS (SELECT 1 FROM alarm_runtime.telemetry_evaluation_input AS input
              WHERE input.tenant_id = current.tenant_id AND input.site_id = current.site_id)
  AND NOT EXISTS (SELECT 1 FROM alarm_runtime.alarm_evaluation_state AS state
                  WHERE state.tenant_id = current.tenant_id AND state.site_id = current.site_id
                    AND state.assignment_id = current.assignment_id
                    AND state.policy_revision_id = current.policy_revision_id
                    AND state.assignment_revision = current.assignment_revision)
ORDER BY current.site_id, current.assignment_id`, tenantID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("find unevaluated Site Alarm rules: %w", err)
	}
	unevaluated := map[string][]string{}
	var sites []string
	for rows.Next() {
		var siteID, assignmentID string
		if err := rows.Scan(&siteID, &assignmentID); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("scan unevaluated Site Alarm rule: %w", err)
		}
		if _, seen := unevaluated[siteID]; !seen {
			sites = append(sites, siteID)
		}
		unevaluated[siteID] = append(unevaluated[siteID], assignmentID)
	}
	rows.Close()
	_ = tx.Rollback(ctx)
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unevaluated Site Alarm rules: %w", err)
	}

	var decisions []evaluatedRuleDecision
	for _, siteID := range sites {
		records, rules, upstreamActive, err := store.loadSiteTelemetryEvaluationState(ctx, tenantID, siteID)
		if err != nil {
			return decisions, err
		}
		newest := records[0]
		for _, record := range records {
			if record.EvaluatedAt.After(newest.EvaluatedAt) {
				newest = record
			}
		}
		snapshot, err := buildSiteEvaluationSnapshot(newest.EventID, tenantID, siteID, records)
		if err != nil {
			return decisions, err
		}
		snapshot.UpstreamRuleActive = upstreamActive
		pendingSet := make(map[string]bool, len(unevaluated[siteID]))
		for _, assignmentID := range unevaluated[siteID] {
			pendingSet[assignmentID] = true
		}
		orderedAssignments := make([]string, 0, len(unevaluated[siteID]))
		for _, rule := range orderSiteRulesForEvaluation(rules) {
			if pendingSet[rule.AssignmentID] {
				orderedAssignments = append(orderedAssignments, rule.AssignmentID)
				delete(pendingSet, rule.AssignmentID)
			}
		}
		if len(pendingSet) > 0 {
			for _, assignmentID := range unevaluated[siteID] {
				if pendingSet[assignmentID] {
					orderedAssignments = append(orderedAssignments, assignmentID)
				}
			}
		}
		delegated := store.resolveEdgeDelegatedRules(ctx, tenantID, siteID, rules)
		for _, assignmentID := range orderedAssignments {
			if delegated[assignmentID] {
				continue
			}
			decision, err := store.EvaluateAssignedSnapshot(ctx, assignmentID, snapshot, now.UTC())
			if errors.Is(err, ErrRuleRetired) {
				delete(upstreamActive, assignmentID)
				continue
			}
			if err != nil {
				return decisions, err
			}
			upstreamActive[assignmentID] = decision.State.ActiveAlarmID != ""
			decisions = append(decisions, evaluatedRuleDecision{
				siteID:       siteID,
				assignmentID: assignmentID,
				decision:     decision,
			})
		}
	}
	return decisions, nil
}
