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
	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

type PublishedRecommendation struct {
	RunID string `json:"runId"`
	// Trigger is MANUAL when an operator asked for the run, SCHEDULE when the Site's schedule did.
	Trigger        string         `json:"trigger"`
	RunStatus      string         `json:"runStatus"`
	Quality        string         `json:"quality"`
	Recommendation Recommendation `json:"recommendation"`
	// ResponseModel is the policy's response coefficients the run weighed the candidate with, as
	// frozen in its input snapshot. They are set by hand in the policy, not fitted from the Site.
	ResponseModel HVACResponseModel `json:"responseModel"`
}

var ErrOptimizationNotFound = errors.New("optimization recommendation not found")

const (
	PreparationStatusPrepared     = "PREPARED"
	PreparationStatusFailed       = "FAILED"
	PreparationStatusPending      = "PENDING"
	PreparationStatusNotScheduled = "NOT_SCHEDULED"

	ResponseCoefficientCalibrated    = "CALIBRATED"
	ResponseCoefficientNotCalibrated = "NOT_CALIBRATED"

	TenantStrategyConfirmed = "CONFIRMED"
	TenantStrategyPending   = "PENDING"
)

type PreparationResponseCoefficient struct {
	Status       string  `json:"status"`
	CalibratedAt *string `json:"calibratedAt"`
}

type PreparationTenantStrategy struct {
	Status string `json:"status"`
}

// PreparationOutcome is how the Site's latest schedule firing went, plus readiness reasons
// explaining why supply-water temperature optimization is not yet enabled on the Site.
type PreparationOutcome struct {
	Trigger *string `json:"trigger"`
	// Status is PREPARED, FAILED, PENDING, or NOT_SCHEDULED when the Site has no scheduled firing yet.
	Status string `json:"status"`
	// Reason is one of the PreparationReason codes, or PREPARATION_FAILED; null unless FAILED.
	Reason              *string                        `json:"reason"`
	FiredAt             *time.Time                     `json:"firedAt"`
	CompletedAt         *time.Time                     `json:"completedAt"`
	ResponseCoefficient PreparationResponseCoefficient `json:"responseCoefficient"`
	TenantStrategy      PreparationTenantStrategy      `json:"tenantStrategy"`
}

// ErrPreparationNotFound is returned when the requested Site is not visible to the Tenant.
var ErrPreparationNotFound = errors.New("optimization preparation not found")

type EvaluationPublicationStore interface {
	StartRun(context.Context, Request, time.Time) error
	BeginPublication(context.Context, Request, Recommendation, Evaluation, time.Time) error
	CompletePublication(context.Context, Evaluation, time.Time) error
	FailRun(context.Context, Request, string, time.Time) error
	ListStalePublications(context.Context, time.Time, int) ([]Evaluation, error)
	LatestRecommendation(context.Context, string, string) (PublishedRecommendation, error)
	LatestPreparation(context.Context, string, string) (PreparationOutcome, error)
}

type PostgresStore struct {
	pool       *pgxpool.Pool
	twinLoader planttwin.Loader
}

// ParsePostgresPoolConfig builds a pgxpool configuration with bounded pool sizing and keep-alive health checks.
func ParsePostgresPoolConfig(dsn string) (*pgxpool.Config, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("optimization postgres dsn is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse optimization postgres dsn: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	return cfg, nil
}

// OpenPostgres connects to PostgreSQL using bounded pool defaults and verifies connectivity immediately.
func OpenPostgres(ctx context.Context, dsn string) (*PostgresStore, error) {
	cfg, err := ParsePostgresPoolConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open optimization postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping optimization postgres pool: %w", err)
	}
	store, err := NewPostgresStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("optimization postgres pool is required")
	}
	twinLoader, err := planttwin.NewPostgresLoader(pool)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{pool: pool, twinLoader: twinLoader}, nil
}

// Close closes the underlying PostgreSQL pool if present.
func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

// Ping verifies PostgreSQL connectivity for readiness probes.
func (store *PostgresStore) Ping(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("optimization postgres store is not initialized")
	}
	return store.pool.Ping(ctx)
}

// Pool exposes the underlying pgx pool for companion stores sharing the service pool.
func (store *PostgresStore) Pool() *pgxpool.Pool {
	if store == nil {
		return nil
	}
	return store.pool
}

func (store *PostgresStore) LoadSiteTwin(ctx context.Context, scope planttwin.SiteScope, at time.Time) (planttwin.SiteTwin, error) {
	if store == nil || store.twinLoader == nil {
		return planttwin.SiteTwin{}, errors.New("optimization plant twin loader is unavailable")
	}
	return store.twinLoader.LoadSiteTwin(ctx, scope, at)
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

type constraintStatusPayload struct {
	SolverOutcome   string `json:"solverOutcome"`
	ConstraintCount uint32 `json:"constraintCount"`
}

type recommendationUpdatedEventPayload struct {
	EvaluationID      string `json:"evaluationId"`
	OptimizationRunID string `json:"optimizationRunId"`
	RecommendationID  string `json:"recommendationId"`
	SolverOutcome     string `json:"solverOutcome"`
	Quality           string `json:"quality"`
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
	constraintStatus := encodeOptimizationJSON(constraintStatusPayload{
		SolverOutcome:   evaluation.SolverOutcome,
		ConstraintCount: evaluation.ConstraintCount,
	})
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
	payload := encodeOptimizationJSON(recommendationUpdatedEventPayload{
		EvaluationID:      evaluation.EvaluationID,
		OptimizationRunID: evaluation.OptimizationRunID,
		RecommendationID:  evaluation.RecommendationID,
		SolverOutcome:     evaluation.SolverOutcome,
		Quality:           evaluation.Quality,
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
	// The latest is the one prepared last: an older run still queued when a newer one published
	// must not replace it by finishing later.
	err = tx.QueryRow(ctx, `SELECT run.id::text
FROM core_registry.optimization_runs run
JOIN core_registry.optimization_recommendations rec
  ON rec.tenant_id=run.tenant_id AND rec.site_id=run.site_id AND rec.optimization_run_id=run.id
WHERE run.tenant_id=$1::uuid
  AND run.site_id=$2::uuid
  AND run.status='PUBLISHED'
  AND rec.approval_state <> 'REJECTED'
  AND (
    rec.approval_state <> 'DRAFT'
    OR (
      COALESCE((SELECT mode FROM core_registry.site_control_states WHERE tenant_id=$1::uuid AND site_id=$2::uuid), 'REMOTE_AUTO') = 'REMOTE_AUTO'
      AND NOT EXISTS (SELECT 1 FROM core_registry.asset_control_locks WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND lock_mode <> 'UNLOCKED')
    )
  )
ORDER BY run.created_at DESC, run.id DESC LIMIT 1`, tenantID, siteID).Scan(&runID)
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
	var currentStateRevalidation, responseModel []byte
	var commandIntentID *string
	err = tx.QueryRow(ctx, `SELECT r.id::text,r.optimization_run_id::text,r.input_snapshot_id::text,r.deployment_revision_id::text,
r.baseline,r.objective,r.constraints,r.candidate,r.expected_impact,r.uncertainty,r.risk,r.rollback_plan,r.verification_plan,
r.approval_state,r.current_state_revalidation,CASE WHEN r.command_intent_id IS NULL THEN NULL ELSE r.command_intent_id::text END,r.created_at,
run.status,run.quality,job.trigger_type,snapshot.safety_constraints->'responseModel'
FROM core_registry.optimization_recommendations r
JOIN core_registry.optimization_runs run ON run.tenant_id=r.tenant_id AND run.site_id=r.site_id AND run.id=r.optimization_run_id
JOIN core_registry.optimization_input_snapshots snapshot
  ON snapshot.tenant_id=r.tenant_id AND snapshot.site_id=r.site_id AND snapshot.id=r.input_snapshot_id
-- A prepared run is queued as the job with the run's own id.
JOIN core_registry.job_instances job ON job.job_id=run.id
WHERE r.tenant_id=$1::uuid AND r.site_id=$2::uuid AND r.optimization_run_id=$3::uuid AND run.status='PUBLISHED'`, tenantID, siteID, runID).Scan(
		&result.Recommendation.ID, &result.RunID, &result.Recommendation.InputSnapshotID, &result.Recommendation.DeploymentRevision,
		&baseline, &objective, &constraints, &candidate, &expectedImpact, &uncertainty, &risk, &rollbackPlan, &verificationPlan,
		&result.Recommendation.Approval, &currentStateRevalidation, &commandIntentID, &result.Recommendation.CreatedAt,
		&result.RunStatus, &result.Quality, &result.Trigger, &responseModel,
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
		{responseModel, &result.ResponseModel},
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
	if !result.Recommendation.CreatedAt.IsZero() {
		validUntil := result.Recommendation.CreatedAt.UTC().Add(intelligencemodel.DefaultRecommendationTTL)
		result.Recommendation.ValidUntil = &validUntil
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

// LatestPreparation reads the Site's latest Optimization schedule firing and readiness status.
// A visible Site without any scheduled OPTIMIZATION_RUN job returns NOT_SCHEDULED with Postgres-backed
// responseCoefficient and tenantStrategy status; non-visible Sites return ErrPreparationNotFound.
func (store *PostgresStore) LatestPreparation(ctx context.Context, tenantID, siteID string) (PreparationOutcome, error) {
	return store.latestPreparationAt(ctx, tenantID, siteID, time.Now().UTC())
}

func (store *PostgresStore) latestPreparationAt(ctx context.Context, tenantID, siteID string, at time.Time) (PreparationOutcome, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return PreparationOutcome{}, errors.New("optimization preparation scope is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PreparationOutcome{}, err
	}
	defer tx.Rollback(ctx)
	if err = optimizationScope(ctx, tx, tenantID, siteID); err != nil {
		return PreparationOutcome{}, err
	}
	outcome, err := resolveLatestPreparation(ctx, txPreparationReader{tx: tx}, tenantID, siteID, at)
	if err != nil {
		return PreparationOutcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PreparationOutcome{}, err
	}
	return outcome, nil
}

type scheduledPreparationJob struct {
	state       string
	errorCode   *string
	firedAt     time.Time
	completedAt *time.Time
}

type preparationReadDB interface {
	checkSiteVisible(ctx context.Context, tenantID, siteID string) (bool, error)
	readEffectiveHVACPolicy(ctx context.Context, tenantID string, at time.Time) (json.RawMessage, int, error)
	hasAllActiveTelemetryPoints(ctx context.Context, tenantID, siteID string, keys []string) (bool, error)
	hasActiveOptimizationDeployment(ctx context.Context, tenantID, siteID string) (bool, error)
	readLatestScheduledJob(ctx context.Context, tenantID, siteID string) (scheduledPreparationJob, bool, error)
}

type txPreparationReader struct{ tx pgx.Tx }

func (reader txPreparationReader) checkSiteVisible(ctx context.Context, tenantID, siteID string) (bool, error) {
	var visible int
	err := reader.tx.QueryRow(ctx, `SELECT 1 FROM core_registry.sites WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, siteID).Scan(&visible)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (reader txPreparationReader) readEffectiveHVACPolicy(ctx context.Context, tenantID string, at time.Time) (json.RawMessage, int, error) {
	var constraints json.RawMessage
	var policyCount int
	err := reader.tx.QueryRow(ctx, `SELECT pv.constraints, count(*) OVER ()
FROM core_registry.optimization_policy_versions pv
JOIN core_registry.optimization_policies p
  ON p.tenant_id=pv.tenant_id AND p.id=pv.policy_id
WHERE pv.tenant_id=$1::uuid
  AND p.status='ACTIVE' AND p.resource_type='HVAC'
  AND pv.status='RELEASED'
  AND pv.effective_from <= $2
  AND (pv.effective_to IS NULL OR pv.effective_to > $2)
ORDER BY pv.effective_from DESC,pv.version DESC
LIMIT 1`, tenantID, at.UTC()).Scan(&constraints, &policyCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return constraints, policyCount, nil
}

func (reader txPreparationReader) hasAllActiveTelemetryPoints(ctx context.Context, tenantID, siteID string, keys []string) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	rows, err := reader.tx.Query(ctx, `SELECT point_code, source_key
FROM core_registry.telemetry_points
WHERE tenant_id=$1::uuid
  AND site_id=$2::uuid
  AND status='ACTIVE'
  AND point_type='TELEMETRY'
  AND (source_key = ANY($3::text[]) OR point_code = ANY($3::text[]))`, tenantID, siteID, keys)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	present := make(map[string]struct{}, len(keys))
	for rows.Next() {
		var pointCode, sourceKey string
		if scanErr := rows.Scan(&pointCode, &sourceKey); scanErr != nil {
			return false, scanErr
		}
		if pointCode != "" {
			present[pointCode] = struct{}{}
		}
		if sourceKey != "" {
			present[sourceKey] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, key := range keys {
		if _, ok := present[key]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (reader txPreparationReader) hasActiveOptimizationDeployment(ctx context.Context, tenantID, siteID string) (bool, error) {
	var exists bool
	err := reader.tx.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1
  FROM core_registry.ai_deployment_bindings binding
  JOIN core_registry.ai_deployment_revisions revision
    ON revision.tenant_id=binding.tenant_id AND revision.id=binding.deployment_revision_id
  WHERE binding.tenant_id=$1::uuid AND binding.site_id=$2::uuid
    AND binding.use_case='OPTIMIZATION' AND binding.status='ACTIVE'
    AND revision.use_case='OPTIMIZATION' AND revision.enabled=true
)`, tenantID, siteID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (reader txPreparationReader) readLatestScheduledJob(ctx context.Context, tenantID, siteID string) (scheduledPreparationJob, bool, error) {
	var job scheduledPreparationJob
	err := reader.tx.QueryRow(ctx, `SELECT state,error_code,scheduled_for,completed_at
FROM core_registry.job_instances
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND job_type='OPTIMIZATION_RUN' AND schedule_id IS NOT NULL
ORDER BY scheduled_for DESC,created_at DESC,job_id DESC
LIMIT 1`, tenantID, siteID).Scan(&job.state, &job.errorCode, &job.firedAt, &job.completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return scheduledPreparationJob{}, false, nil
	}
	if err != nil {
		return scheduledPreparationJob{}, false, err
	}
	return job, true, nil
}

type preparationPolicyConstraints struct {
	InputMapping     InputMapping                       `json:"inputMapping"`
	ResponseModel    HVACResponseModel                  `json:"responseModel"`
	SiteCalibrations map[string]SiteResponseCalibration `json:"siteCalibrations"`
}

func resolveLatestPreparation(ctx context.Context, db preparationReadDB, tenantID, siteID string, at time.Time) (PreparationOutcome, error) {
	visible, err := db.checkSiteVisible(ctx, tenantID, siteID)
	if err != nil {
		return PreparationOutcome{}, err
	}
	if !visible {
		return PreparationOutcome{}, ErrPreparationNotFound
	}
	responseCoefficient, tenantStrategy, err := resolvePreparationReadiness(ctx, db, tenantID, siteID, at)
	if err != nil {
		return PreparationOutcome{}, err
	}
	job, found, err := db.readLatestScheduledJob(ctx, tenantID, siteID)
	if err != nil {
		return PreparationOutcome{}, err
	}
	if !found {
		return PreparationOutcome{
			Status:              PreparationStatusNotScheduled,
			ResponseCoefficient: responseCoefficient,
			TenantStrategy:      tenantStrategy,
		}, nil
	}
	firedAt := job.firedAt
	return preparationOutcome(job.state, job.errorCode, PreparationOutcome{
		FiredAt:             &firedAt,
		CompletedAt:         job.completedAt,
		ResponseCoefficient: responseCoefficient,
		TenantStrategy:      tenantStrategy,
	}), nil
}

func resolvePreparationReadiness(ctx context.Context, db preparationReadDB, tenantID, siteID string, at time.Time) (PreparationResponseCoefficient, PreparationTenantStrategy, error) {
	responseCoefficient := PreparationResponseCoefficient{Status: ResponseCoefficientNotCalibrated}
	tenantStrategy := PreparationTenantStrategy{Status: TenantStrategyPending}

	constraints, policyCount, err := db.readEffectiveHVACPolicy(ctx, tenantID, at)
	if err != nil {
		return responseCoefficient, tenantStrategy, err
	}
	if policyCount != 1 {
		return responseCoefficient, tenantStrategy, nil
	}

	var parsed preparationPolicyConstraints
	if err := json.Unmarshal(constraints, &parsed); err != nil {
		return responseCoefficient, tenantStrategy, nil
	}
	responseCoefficient = deriveResponseCoefficient(siteID, parsed)

	requiredKeys := requiredMappingKeys(parsed.InputMapping)
	if len(requiredKeys) == 0 {
		return responseCoefficient, tenantStrategy, nil
	}
	pointsOK, err := db.hasAllActiveTelemetryPoints(ctx, tenantID, siteID, requiredKeys)
	if err != nil {
		return responseCoefficient, tenantStrategy, err
	}
	if !pointsOK {
		return responseCoefficient, tenantStrategy, nil
	}
	deploymentOK, err := db.hasActiveOptimizationDeployment(ctx, tenantID, siteID)
	if err != nil {
		return responseCoefficient, tenantStrategy, err
	}
	if deploymentOK {
		tenantStrategy.Status = TenantStrategyConfirmed
	}
	return responseCoefficient, tenantStrategy, nil
}

func deriveResponseCoefficient(siteID string, parsed preparationPolicyConstraints) PreparationResponseCoefficient {
	if entry, ok := lookupSiteCalibration(parsed.SiteCalibrations, siteID); ok {
		return evaluateCalibration(entry.Status, entry.Source, entry.CalibratedAt)
	}
	return evaluateCalibration(parsed.ResponseModel.Status, parsed.ResponseModel.Source, parsed.ResponseModel.CalibratedAt)
}

func evaluateCalibration(rawStatus, rawSource, rawCalibratedAt string) PreparationResponseCoefficient {
	status := strings.ToUpper(strings.TrimSpace(rawStatus))
	if status == ResponseCoefficientNotCalibrated {
		return PreparationResponseCoefficient{Status: ResponseCoefficientNotCalibrated}
	}
	if status == ResponseCoefficientCalibrated || isCalibratedSource(rawSource) {
		return PreparationResponseCoefficient{
			Status:       ResponseCoefficientCalibrated,
			CalibratedAt: cleanStringPtr(rawCalibratedAt),
		}
	}
	return PreparationResponseCoefficient{Status: ResponseCoefficientNotCalibrated}
}

func isCalibratedSource(rawSource string) bool {
	switch strings.ToUpper(strings.TrimSpace(rawSource)) {
	case "SITE_DATA", "CALIBRATED":
		return true
	default:
		return false
	}
}

func lookupSiteCalibration(calibrations map[string]SiteResponseCalibration, siteID string) (SiteResponseCalibration, bool) {
	if len(calibrations) == 0 {
		return SiteResponseCalibration{}, false
	}
	if entry, ok := calibrations[siteID]; ok {
		return entry, true
	}
	for key, entry := range calibrations {
		if strings.EqualFold(strings.TrimSpace(key), siteID) {
			return entry, true
		}
	}
	return SiteResponseCalibration{}, false
}

func cleanStringPtr(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func requiredMappingKeys(mapping InputMapping) []string {
	supply := strings.TrimSpace(mapping.SupplyTemperatureKey)
	zoneKeys := mapping.effectiveZoneKeys()
	if supply == "" || len(zoneKeys) == 0 {
		return nil
	}
	seen := map[string]struct{}{supply: {}}
	keys := make([]string, 0, 1+len(zoneKeys))
	keys = append(keys, supply)
	for _, key := range zoneKeys {
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func preparationOutcome(state string, errorCode *string, outcome PreparationOutcome) PreparationOutcome {
	trigger := TriggerSchedule
	outcome.Trigger = &trigger
	if outcome.FiredAt != nil {
		fired := outcome.FiredAt.UTC()
		outcome.FiredAt = &fired
	}
	if outcome.CompletedAt != nil {
		completed := outcome.CompletedAt.UTC()
		outcome.CompletedAt = &completed
	}
	if outcome.ResponseCoefficient.Status == "" {
		outcome.ResponseCoefficient.Status = ResponseCoefficientNotCalibrated
	}
	if outcome.TenantStrategy.Status == "" {
		outcome.TenantStrategy.Status = TenantStrategyPending
	}
	switch state {
	case "SUCCEEDED":
		outcome.Status = PreparationStatusPrepared
	case "FAILED", "DEAD", "CANCELLED", "SKIPPED":
		outcome.Status = PreparationStatusFailed
		// Firings that failed before reasons were recorded say only that they failed.
		reason := PreparationFailedCode
		if errorCode != nil && KnownPreparationReason(*errorCode) {
			reason = *errorCode
		}
		outcome.Reason = &reason
	default:
		outcome.Status = PreparationStatusPending
	}
	return outcome
}
