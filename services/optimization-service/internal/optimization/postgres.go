package optimization

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EvaluationPublicationStore interface {
	StartRun(context.Context, Request, time.Time) error
	BeginPublication(context.Context, Request, Plan, Evaluation, time.Time) error
	CompletePublication(context.Context, Evaluation, time.Time) error
	FailRun(context.Context, Request, string, time.Time) error
	ListStalePublications(context.Context, time.Time, int) ([]Evaluation, error)
}

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("optimization postgres pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func optimizationScope(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+siteID+"}")
	return err
}

func (store *PostgresStore) StartRun(ctx context.Context, request Request, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	for _, transition := range []struct{ from, to string }{{"CREATED", "VALIDATING"}, {"VALIDATING", "SOLVING"}} {
		tag, execErr := tx.Exec(ctx, `UPDATE core_registry.optimization_runs
SET status=$4,started_at=COALESCE(started_at,$5),updated_at=$5,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status=$6`, request.TenantID, request.SiteID, request.OptimizationRunID, transition.to, at, transition.from)
		if execErr != nil {
			return execErr
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("optimization run %s transition rejected", transition.to)
		}
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) BeginPublication(ctx context.Context, request Request, plan Plan, evaluation Evaluation, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	constraintStatus := encodeOptimizationJSON(map[string]any{"solverOutcome": evaluation.SolverOutcome, "intervalCount": evaluation.IntervalCount})
	tag, err := tx.Exec(ctx, `UPDATE core_registry.optimization_runs
SET status='FEASIBLE',quality=$4,constraint_status=$5::jsonb,updated_at=$6,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='SOLVING'`, request.TenantID, request.SiteID, request.OptimizationRunID, plan.Quality, constraintStatus, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("optimization run FEASIBLE transition rejected")
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.dispatch_plans(
id,tenant_id,site_id,optimization_run_id,input_snapshot_id,policy_version_id,subject_type,subject_id,plan_version,quality,status,valid_from,valid_to,explanation,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8::uuid,$9,$10,'DRAFT',$11,$12,$13::jsonb,1,$14,$14)
ON CONFLICT (tenant_id,site_id,optimization_run_id,plan_version) DO NOTHING`, plan.PlanID, request.TenantID, request.SiteID, request.OptimizationRunID,
		plan.InputSnapshotID, plan.PolicyVersionID, plan.SubjectType, plan.SubjectID, plan.PlanVersion, plan.Quality, plan.ValidFrom, plan.ValidTo, encodeOptimizationJSON(plan.Explanation), at)
	if err != nil {
		return fmt.Errorf("insert dispatch plan: %w", err)
	}
	for _, interval := range plan.Intervals {
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.dispatch_intervals(
id,tenant_id,site_id,dispatch_plan_id,resource_id,start_time,end_time,target_type,target_value,unit,expected_soc,constraint_margin,ordinal,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14)
ON CONFLICT (tenant_id,site_id,dispatch_plan_id,resource_id,ordinal) DO NOTHING`, interval.IntervalID, request.TenantID, request.SiteID, plan.PlanID,
		interval.ResourceID, interval.StartTime, interval.EndTime, interval.TargetType, interval.TargetValue, interval.Unit, interval.ExpectedSOC,
		encodeOptimizationJSON(interval.ConstraintMargin), interval.Ordinal, at)
		if err != nil {
			return fmt.Errorf("insert dispatch interval: %w", err)
		}
	}
	tag, err = tx.Exec(ctx, `UPDATE core_registry.optimization_runs
SET status='PERSISTING',updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='FEASIBLE'`, request.TenantID, request.SiteID, request.OptimizationRunID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("optimization run PERSISTING transition rejected")
	}
	publicationID, err := optimizationUUIDv7(at)
	if err != nil {
		return err
	}
	evidence := encodeOptimizationJSON(evaluation)
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.cross_store_publications(
id,tenant_id,site_id,producer,run_id,result_id,target_store,target_dataset,publication_evidence,status,started_at,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'OPTIMIZATION',$4::uuid,$5::uuid,'CLICKHOUSE','analytics.optimization_evaluations',$6::jsonb,'PERSISTING',$7,1,$7,$7)`,
		publicationID, request.TenantID, request.SiteID, request.OptimizationRunID, evaluation.EvaluationID, evidence, at)
	if err != nil {
		return fmt.Errorf("create optimization cross-store publication: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) CompletePublication(ctx context.Context, evaluation Evaluation, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, evaluation.TenantID, evaluation.SiteID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.cross_store_publications
SET status='PERSISTED',persisted_at=COALESCE(persisted_at,$4),updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND producer='OPTIMIZATION' AND run_id=$2::uuid AND result_id=$3::uuid AND status IN ('PERSISTING','PERSISTED')`,
		evaluation.TenantID, evaluation.OptimizationRunID, evaluation.EvaluationID, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.optimization_runs
SET status='PUBLISHED',finished_at=COALESCE(finished_at,$4),updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('PERSISTING','PUBLISHED')`, evaluation.TenantID, evaluation.SiteID, evaluation.OptimizationRunID, at); err != nil {
		return err
	}
	eventID, err := optimizationUUIDv7(at)
	if err != nil {
		return err
	}
	payload := encodeOptimizationJSON(map[string]any{
		"evaluationId": evaluation.EvaluationID,
		"optimizationRunId": evaluation.OptimizationRunID,
		"dispatchPlanId": evaluation.DispatchPlanID,
		"solverOutcome": evaluation.SolverOutcome,
		"quality": evaluation.Quality,
	})
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_outbox_events(
id,tenant_id,site_id,event_type,schema_version,subject_type,subject_id,aggregate_type,aggregate_id,aggregate_version,occurred_at,payload,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'OPTIMIZATION_EVALUATION_UPDATED',1,$4,$5::uuid,'OPTIMIZATION_EVALUATION',$6::uuid,1,$7,$8::jsonb,$7)
ON CONFLICT (tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type) DO NOTHING`, eventID, evaluation.TenantID, evaluation.SiteID,
		evaluation.SubjectType, evaluation.SubjectID, evaluation.EvaluationID, at, payload)
	if err != nil {
		return err
	}
	var actualEventID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM core_registry.domain_outbox_events
WHERE tenant_id=$1::uuid AND aggregate_type='OPTIMIZATION_EVALUATION' AND aggregate_id=$2::uuid AND aggregate_version=1 AND event_type='OPTIMIZATION_EVALUATION_UPDATED'`, evaluation.TenantID, evaluation.EvaluationID).Scan(&actualEventID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_event_deliveries(event_id,consumer_name,status,attempt,created_at,updated_at)
VALUES($1::uuid,'audit-notification','PENDING',0,$2,$2) ON CONFLICT (event_id,consumer_name) DO NOTHING`, actualEventID, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) FailRun(ctx context.Context, request Request, failureCode string, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `UPDATE core_registry.cross_store_publications
SET status='FAILED',last_error=$3,updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND producer='OPTIMIZATION' AND run_id=$2::uuid AND status='PERSISTING'`, request.TenantID, request.OptimizationRunID, failureCode, at)
	_, err = tx.Exec(ctx, `UPDATE core_registry.optimization_runs
SET status='FAILED',finished_at=$4,updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('VALIDATING','SOLVING','PERSISTING')`, request.TenantID, request.SiteID, request.OptimizationRunID, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) ListStalePublications(ctx context.Context, staleBefore time.Time, limit int) ([]Evaluation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.pool.Query(ctx, `SELECT publication_evidence
FROM core_registry.cross_store_publications
WHERE producer='OPTIMIZATION' AND status='PERSISTING' AND updated_at < $1
ORDER BY updated_at,id LIMIT $2`, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Evaluation, 0)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var evaluation Evaluation
		if err = json.Unmarshal(raw, &evaluation); err != nil {
			return nil, err
		}
		values = append(values, evaluation)
	}
	return values, rows.Err()
}

func encodeOptimizationJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func optimizationUUIDv7(at time.Time) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	milliseconds := uint64(at.UTC().UnixMilli())
	bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5] = byte(milliseconds>>40), byte(milliseconds>>32), byte(milliseconds>>24), byte(milliseconds>>16), byte(milliseconds>>8), byte(milliseconds)
	bytes[6] = (bytes[6] & 0x0f) | 0x70
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	h := hex.EncodeToString(bytes)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
