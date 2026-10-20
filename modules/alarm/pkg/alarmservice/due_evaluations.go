package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// A pass runs every minute and claims at most this many due evaluations per Tenant, each for one
// lease; the next pass takes the rest.
const (
	dueEvaluationInterval = time.Minute
	dueEvaluationBatch    = 100
	dueEvaluationLease    = time.Minute
	evidencePruneBatch    = 5000
)

// RunScheduledEvaluations runs a pass every minute until ctx ends, wherever the Alarm owner runs:
// alone or inside energy-api. Each pass also deletes evaluation evidence past its 30 days.
func RunScheduledEvaluations(ctx context.Context, store *PostgresStore, logger *slog.Logger) {
	host, err := os.Hostname()
	if err != nil {
		host = "alarm-owner"
	}
	workerID := "alarm-due-evaluations:" + host
	go func() {
		ticker := time.NewTicker(dueEvaluationInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			result, err := store.RunDueEvaluations(ctx, workerID, time.Now())
			if err != nil {
				logger.Error("alarm_due_evaluations_failed", "error_code", "ALARM_DUE_EVALUATIONS_FAILED", "error", err, "failed", result.Failed)
			}
			if result.Raised+result.Cleared > 0 {
				logger.Info("alarm_due_evaluations", "evaluated", result.Evaluated, "raised", result.Raised, "cleared", result.Cleared)
			}
			pruned, err := store.PruneExpiredEvaluationEvidence(ctx)
			if err != nil {
				logger.Error("alarm_evaluation_evidence_prune_failed", "error_code", "ALARM_EVALUATION_EVIDENCE_PRUNE_FAILED", "error", err)
			}
			if pruned > 0 {
				logger.Info("alarm_evaluation_evidence_pruned", "deleted", pruned)
			}
		}
	}()
}

// DueEvaluationResult counts what one scheduled pass did.
type DueEvaluationResult struct {
	Evaluated int
	Raised    int
	Cleared   int
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
		// A Site rule published while the Site was already silent has no evaluation, and a new
		// version of a rule whose device is silent has none with that version; no reading will
		// come to make one (#659): evaluate it with the Site's last readings.
		firsts, err := store.evaluateUnevaluatedSiteRules(tenantContext, tenantID, now)
		if err != nil {
			result.Failed++
			failures = append(failures, err)
		}
		for _, decision := range firsts {
			result.count(decision)
		}
		claims, err := store.ClaimDueEvaluations(tenantContext, tenantID, workerID, now, dueEvaluationLease, dueEvaluationBatch)
		if err != nil {
			result.Failed++
			failures = append(failures, err)
			continue
		}
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

// evaluateUnevaluatedSiteRules evaluates each Site rule not yet evaluated with the version
// assigned to it, with the Site's last readings, as a reading arriving would.
func (store *PostgresStore) evaluateUnevaluatedSiteRules(ctx context.Context, tenantID string, now time.Time) ([]EvaluationDecision, error) {
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
SELECT current.site_id::text, current.assignment_id::text
FROM (SELECT DISTINCT ON (assignment_id) tenant_id, site_id, assignment_id, policy_revision_id
      FROM alarm_runtime.alarm_policy_assignment
      WHERE tenant_id = $1 AND subject_type = 'SITE'
      ORDER BY assignment_id, assignment_revision DESC) AS current
WHERE EXISTS (SELECT 1 FROM alarm_runtime.telemetry_evaluation_input AS input
              WHERE input.tenant_id = current.tenant_id AND input.site_id = current.site_id)
  AND NOT EXISTS (SELECT 1 FROM alarm_runtime.alarm_evaluation_state AS state
                  WHERE state.tenant_id = current.tenant_id AND state.site_id = current.site_id
                    AND state.assignment_id = current.assignment_id
                    AND state.policy_revision_id = current.policy_revision_id)
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

	var decisions []EvaluationDecision
	for _, siteID := range sites {
		records, _, err := store.loadSiteTelemetryEvaluationState(ctx, tenantID, siteID)
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
		for _, assignmentID := range unevaluated[siteID] {
			decision, err := store.EvaluateAssignedSnapshot(ctx, assignmentID, snapshot, now.UTC())
			if err != nil {
				return decisions, err
			}
			decisions = append(decisions, decision)
		}
	}
	return decisions, nil
}
