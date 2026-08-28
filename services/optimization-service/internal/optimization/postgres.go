package optimization

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type PublishedRecommendation struct {
	RunID          string         `json:"runId"`
	RunStatus      string         `json:"runStatus"`
	Quality        string         `json:"quality"`
	Recommendation Recommendation `json:"recommendation"`
}

var ErrOptimizationNotFound = errors.New("optimization recommendation not found")

type EvaluationPublicationStore interface {
	StartRun(context.Context, Request, time.Time) error
	BeginPublication(context.Context, Request, Recommendation, Evaluation, time.Time) error
	CompletePublication(context.Context, Evaluation, time.Time) error
	FailRun(context.Context, Request, string, time.Time) error
	ListStalePublications(context.Context, time.Time, int) ([]Evaluation, error)
	GetRecommendation(context.Context, string, string, string) (PublishedRecommendation, error)
	GetRecommendationForSites(context.Context, string, []string, string) (PublishedRecommendation, error)
	LatestRecommendation(context.Context, string, string) (PublishedRecommendation, error)
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

func (store *PostgresStore) BeginPublication(ctx context.Context, request Request, recommendation Recommendation, evaluation Evaluation, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	constraintStatus := encodeOptimizationJSON(map[string]any{"solverOutcome": evaluation.SolverOutcome, "constraintCount": evaluation.ConstraintCount})
	tag, err := tx.Exec(ctx, `UPDATE core_registry.optimization_runs
SET status='FEASIBLE',quality=$4,constraint_status=$5::jsonb,updated_at=$6,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='SOLVING'`, request.TenantID, request.SiteID, request.OptimizationRunID, evaluation.Quality, constraintStatus, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("optimization run FEASIBLE transition rejected")
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.optimization_recommendations(
id,tenant_id,site_id,optimization_run_id,input_snapshot_id,deployment_revision_id,baseline,objective,constraints,candidate,expected_impact,uncertainty,risk,rollback_plan,verification_plan,approval_state,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::jsonb,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13::jsonb,$14::jsonb,$15::jsonb,$16,1,$17,$17)
ON CONFLICT (tenant_id,site_id,optimization_run_id) DO NOTHING`, recommendation.ID, request.TenantID, request.SiteID, request.OptimizationRunID,
		recommendation.InputSnapshotID, recommendation.DeploymentRevision, encodeOptimizationJSON(recommendation.Baseline), encodeOptimizationJSON(recommendation.Objective),
		encodeOptimizationJSON(recommendation.Constraints), encodeOptimizationJSON(recommendation.Candidate), encodeOptimizationJSON(recommendation.ExpectedImpact),
		encodeOptimizationJSON(recommendation.Uncertainty), encodeOptimizationJSON(recommendation.Risk), encodeOptimizationJSON(recommendation.RollbackPlan),
		encodeOptimizationJSON(recommendation.VerificationPlan), recommendation.Approval, at)
	if err != nil {
		return fmt.Errorf("insert optimization recommendation: %w", err)
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
		"evaluationId":      evaluation.EvaluationID,
		"optimizationRunId": evaluation.OptimizationRunID,
		"recommendationId":  evaluation.RecommendationID,
		"solverOutcome":     evaluation.SolverOutcome,
		"quality":           evaluation.Quality,
	})
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_outbox_events(
id,tenant_id,site_id,event_type,schema_version,subject_type,subject_id,aggregate_type,aggregate_id,aggregate_version,occurred_at,payload,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'OPTIMIZATION_RECOMMENDATION_UPDATED',1,$4,$5::uuid,'OPTIMIZATION_RECOMMENDATION',$6::uuid,1,$7,$8::jsonb,$7)
ON CONFLICT (tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type) DO NOTHING`, eventID, evaluation.TenantID, evaluation.SiteID,
		evaluation.SubjectType, evaluation.SubjectID, evaluation.RecommendationID, at, payload)
	if err != nil {
		return err
	}
	var actualEventID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM core_registry.domain_outbox_events
WHERE tenant_id=$1::uuid AND aggregate_type='OPTIMIZATION_RECOMMENDATION' AND aggregate_id=$2::uuid AND aggregate_version=1 AND event_type='OPTIMIZATION_RECOMMENDATION_UPDATED'`, evaluation.TenantID, evaluation.RecommendationID).Scan(&actualEventID); err != nil {
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

func (store *PostgresStore) LatestRecommendation(ctx context.Context, tenantID, siteID string) (PublishedRecommendation, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return PublishedRecommendation{}, errors.New("optimization recommendation scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PublishedRecommendation{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return PublishedRecommendation{}, err
	}
	var runID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM core_registry.optimization_runs
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status='PUBLISHED'
ORDER BY finished_at DESC NULLS LAST,updated_at DESC LIMIT 1`, tenantID, siteID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	if err != nil {
		return PublishedRecommendation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PublishedRecommendation{}, err
	}
	return store.GetRecommendation(ctx, tenantID, siteID, runID)
}

func (store *PostgresStore) GetRecommendationForSites(ctx context.Context, tenantID string, allowedSiteIDs []string, runID string) (PublishedRecommendation, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(runID) || len(allowedSiteIDs) == 0 || len(allowedSiteIDs) > 256 {
		return PublishedRecommendation{}, errors.New("optimization recommendation authorization scope is invalid")
	}
	for _, siteID := range allowedSiteIDs {
		if !uuidPattern.MatchString(siteID) {
			return PublishedRecommendation{}, errors.New("optimization recommendation authorized site is invalid")
		}
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PublishedRecommendation{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return PublishedRecommendation{}, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+strings.Join(allowedSiteIDs, ",")+"}"); err != nil {
		return PublishedRecommendation{}, err
	}
	var siteID string
	err = tx.QueryRow(ctx, `SELECT site_id::text FROM core_registry.optimization_runs
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='PUBLISHED'`, tenantID, runID).Scan(&siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	if err != nil {
		return PublishedRecommendation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PublishedRecommendation{}, err
	}
	return store.GetRecommendation(ctx, tenantID, siteID, runID)
}

func (store *PostgresStore) GetRecommendation(ctx context.Context, tenantID, siteID, runID string) (PublishedRecommendation, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || !uuidPattern.MatchString(runID) {
		return PublishedRecommendation{}, errors.New("optimization recommendation scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PublishedRecommendation{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return PublishedRecommendation{}, err
	}
	var result PublishedRecommendation
	var baseline, objective, constraints, candidate, expectedImpact, uncertainty, risk, rollbackPlan, verificationPlan []byte
	var currentStateRevalidation []byte
	var commandIntentID *string
	err = tx.QueryRow(ctx, `SELECT r.id::text,r.optimization_run_id::text,r.input_snapshot_id::text,r.deployment_revision_id::text,
r.baseline,r.objective,r.constraints,r.candidate,r.expected_impact,r.uncertainty,r.risk,r.rollback_plan,r.verification_plan,
r.approval_state,r.current_state_revalidation,CASE WHEN r.command_intent_id IS NULL THEN NULL ELSE r.command_intent_id::text END,r.created_at,
run.status,run.quality
FROM core_registry.optimization_recommendations r
JOIN core_registry.optimization_runs run ON run.tenant_id=r.tenant_id AND run.site_id=r.site_id AND run.id=r.optimization_run_id
WHERE r.tenant_id=$1::uuid AND r.site_id=$2::uuid AND r.optimization_run_id=$3::uuid AND run.status='PUBLISHED'`, tenantID, siteID, runID).Scan(
		&result.Recommendation.ID, &result.RunID, &result.Recommendation.InputSnapshotID, &result.Recommendation.DeploymentRevision,
		&baseline, &objective, &constraints, &candidate, &expectedImpact, &uncertainty, &risk, &rollbackPlan, &verificationPlan,
		&result.Recommendation.Approval, &currentStateRevalidation, &commandIntentID, &result.Recommendation.CreatedAt,
		&result.RunStatus, &result.Quality,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	if err != nil {
		return PublishedRecommendation{}, err
	}
	result.Recommendation.TenantID = tenantID
	result.Recommendation.SiteID = siteID
	for _, field := range []struct {
		payload []byte
		target  any
	}{
		{baseline, &result.Recommendation.Baseline},
		{objective, &result.Recommendation.Objective},
		{constraints, &result.Recommendation.Constraints},
		{candidate, &result.Recommendation.Candidate},
		{expectedImpact, &result.Recommendation.ExpectedImpact},
		{uncertainty, &result.Recommendation.Uncertainty},
		{risk, &result.Recommendation.Risk},
		{rollbackPlan, &result.Recommendation.RollbackPlan},
		{verificationPlan, &result.Recommendation.VerificationPlan},
	} {
		if err = json.Unmarshal(field.payload, field.target); err != nil {
			return PublishedRecommendation{}, fmt.Errorf("decode published optimization recommendation: %w", err)
		}
	}
	if len(currentStateRevalidation) > 0 && string(currentStateRevalidation) != "null" {
		var revalidation intelligencemodel.CurrentStateRevalidation
		if err = json.Unmarshal(currentStateRevalidation, &revalidation); err != nil {
			return PublishedRecommendation{}, fmt.Errorf("decode current-state revalidation: %w", err)
		}
		result.Recommendation.Revalidation = &revalidation
	}
	if commandIntentID != nil {
		result.Recommendation.CommandIntentID = *commandIntentID
	}
	if err = tx.Commit(ctx); err != nil {
		return PublishedRecommendation{}, err
	}
	return result, nil
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
