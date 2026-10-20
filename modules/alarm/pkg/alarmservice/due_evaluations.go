package alarmservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// A pass claims at most this many due evaluations per Tenant, each for one lease; the next pass
// takes the rest.
const (
	dueEvaluationBatch = 100
	dueEvaluationLease = time.Minute
)

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
		claims, err := store.ClaimDueEvaluations(tenantContext, tenantID, workerID, now, dueEvaluationLease, dueEvaluationBatch)
		if err != nil {
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
			result.Evaluated++
			switch decision.Effect {
			case EvaluationEffectPublish:
				result.Raised++
			case EvaluationEffectClear:
				result.Cleared++
			}
		}
	}
	return result, errors.Join(failures...)
}
