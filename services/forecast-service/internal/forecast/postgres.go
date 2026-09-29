package forecast

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

type Publication struct {
	Request        Request
	ResultCount    int
	ResultChecksum string
	Quality        string
	WindowStart    time.Time
	WindowEnd      time.Time
}

type ForecastSnapshotReference struct {
	SnapshotID      string    `json:"snapshotId"`
	ForecastJobID   string    `json:"forecastJobId"`
	DeploymentID    string    `json:"deploymentId"`
	ModelVersionID  string    `json:"modelVersionId"`
	InputSnapshotID string    `json:"inputSnapshotId"`
	SubjectType     string    `json:"subjectType"`
	SubjectID       string    `json:"subjectId"`
	Target          string    `json:"target"`
	ForecastOrigin  time.Time `json:"forecastOrigin"`
	WindowStart     time.Time `json:"windowStart"`
	WindowEnd       time.Time `json:"windowEnd"`
	ResultCount     int       `json:"resultCount"`
	Quality         string    `json:"quality"`
}

var ErrForecastNotFound = errors.New("forecast result not found")

type PublicationStore interface {
	StartJob(context.Context, Request, time.Time) error
	BeginPublication(context.Context, Publication, time.Time) error
	CompletePublication(context.Context, Publication, time.Time) error
	FailJob(context.Context, Request, string, time.Time) error
	ListStalePublications(context.Context, time.Time, int) ([]Publication, error)
	LatestForecast(context.Context, string, string, string) (ForecastSnapshotReference, error)
}

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("forecast postgres pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func forecastScope(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+siteID+"}")
	return err
}

func (store *PostgresStore) StartJob(ctx context.Context, request Request, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.forecast_jobs
SET status='RUNNING',started_at=COALESCE(started_at,$4),updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='PENDING'`, request.TenantID, request.SiteID, request.ForecastJobID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("forecast job RUNNING transition rejected")
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) BeginPublication(ctx context.Context, publication Publication, at time.Time) error {
	request := publication.Request
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.forecast_jobs
SET status='PERSISTING',updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='RUNNING'`, request.TenantID, request.SiteID, request.ForecastJobID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("forecast job PERSISTING transition rejected")
	}
	publicationID, err := uuidv7(at)
	if err != nil {
		return err
	}
	evidence, _ := json.Marshal(map[string]any{
		"resultCount":    publication.ResultCount,
		"resultChecksum": publication.ResultChecksum,
		"quality":        publication.Quality,
		"windowStart":    publication.WindowStart.UTC(),
		"windowEnd":      publication.WindowEnd.UTC(),
	})
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.cross_store_publications(
id,tenant_id,site_id,producer,run_id,result_id,target_store,target_dataset,publication_evidence,status,started_at,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'FORECAST',$4::uuid,$5::uuid,'CLICKHOUSE','analytics.forecast_series',$6::jsonb,'PERSISTING',$7,1,$7,$7)`,
		publicationID, request.TenantID, request.SiteID, request.ForecastJobID, request.ForecastSnapshotID, evidence, at)
	if err != nil {
		return fmt.Errorf("create forecast cross-store publication: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) CompletePublication(ctx context.Context, publication Publication, at time.Time) error {
	request := publication.Request
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.cross_store_publications
SET status='PERSISTED',persisted_at=COALESCE(persisted_at,$4),reconciled_at=CASE WHEN status='PERSISTING' THEN reconciled_at ELSE reconciled_at END,updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND producer='FORECAST' AND run_id=$2::uuid AND result_id=$3::uuid AND status IN ('PERSISTING','PERSISTED')`, request.TenantID, request.ForecastJobID, request.ForecastSnapshotID, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.forecast_jobs
SET status='PERSISTED',completed_at=COALESCE(completed_at,$4),updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('PERSISTING','PERSISTED')`, request.TenantID, request.SiteID, request.ForecastJobID, at); err != nil {
		return err
	}
	quality := map[string]any{"resultQuality": publication.Quality}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.forecast_snapshots(
id,tenant_id,site_id,forecast_job_id,deployment_id,model_version_id,input_snapshot_id,forecast_origin,window_start,window_end,result_count,result_checksum,quality_summary,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8,$9,$10,$11,$12,$13::jsonb,$14)
ON CONFLICT (tenant_id,site_id,forecast_job_id) DO NOTHING`,
		request.ForecastSnapshotID, request.TenantID, request.SiteID, request.ForecastJobID, request.DeploymentID, request.ModelVersionID,
		request.InputSnapshotID, request.ForecastOrigin.UTC(), publication.WindowStart.UTC(), publication.WindowEnd.UTC(), publication.ResultCount,
		publication.ResultChecksum, encodeForecastJSON(quality), at)
	if err != nil {
		return fmt.Errorf("insert forecast snapshot: %w", err)
	}
	eventID, err := uuidv7(at)
	if err != nil {
		return err
	}
	payload := encodeForecastJSON(map[string]any{
		"forecastSnapshotId": request.ForecastSnapshotID,
		"forecastJobId":      request.ForecastJobID,
		"target":             request.Target,
		"subjectType":        request.SubjectType,
		"subjectId":          request.SubjectID,
		"forecastOrigin":     request.ForecastOrigin.UTC(),
		"windowStart":        publication.WindowStart.UTC(),
		"windowEnd":          publication.WindowEnd.UTC(),
	})
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_outbox_events(
id,tenant_id,site_id,event_type,schema_version,subject_type,subject_id,aggregate_type,aggregate_id,aggregate_version,occurred_at,payload,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'FORECAST_RESULT_UPDATED',1,$4,$5::uuid,'FORECAST_SNAPSHOT',$6::uuid,1,$7,$8::jsonb,$7)
ON CONFLICT (tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type) DO NOTHING`, eventID, request.TenantID, request.SiteID,
		request.SubjectType, request.SubjectID, request.ForecastSnapshotID, at, payload)
	if err != nil {
		return fmt.Errorf("create FORECAST_RESULT_UPDATED event: %w", err)
	}
	var actualEventID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM core_registry.domain_outbox_events
WHERE tenant_id=$1::uuid AND aggregate_type='FORECAST_SNAPSHOT' AND aggregate_id=$2::uuid AND aggregate_version=1 AND event_type='FORECAST_RESULT_UPDATED'`, request.TenantID, request.ForecastSnapshotID).Scan(&actualEventID); err != nil {
		return err
	}
	for _, consumer := range []string{"optimization-trigger", "audit-notification"} {
		if _, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_event_deliveries(event_id,consumer_name,status,attempt,created_at,updated_at)
VALUES($1::uuid,$2,'PENDING',0,$3,$3) ON CONFLICT (event_id,consumer_name) DO NOTHING`, actualEventID, consumer, at); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) FailJob(ctx context.Context, request Request, failureCode string, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `UPDATE core_registry.cross_store_publications
SET status='FAILED',last_error=$4,updated_at=$5,revision=revision+1
WHERE tenant_id=$1::uuid AND producer='FORECAST' AND run_id=$2::uuid AND result_id=$3::uuid AND status='PERSISTING'`, request.TenantID, request.ForecastJobID, request.ForecastSnapshotID, failureCode, at)
	_, err = tx.Exec(ctx, `UPDATE core_registry.forecast_jobs
SET status='FAILED',completed_at=$4,updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('RUNNING','PERSISTING')`, request.TenantID, request.SiteID, request.ForecastJobID, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) ListStalePublications(ctx context.Context, staleBefore time.Time, limit int) ([]Publication, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.pool.Query(ctx, `SELECT p.tenant_id::text,p.site_id::text,p.run_id::text,p.result_id::text,p.publication_evidence,
 j.deployment_id::text,j.model_version_id::text,j.input_snapshot_id::text,j.target,j.subject_type,j.subject_id::text,j.forecast_origin,j.horizon_minutes,j.granularity,
 mv.model_id::text,mv.model_version,mv.feature_set_version_id::text,fsv.version,mv.topology_version_id::text
FROM core_registry.cross_store_publications p
JOIN core_registry.forecast_jobs j ON j.tenant_id=p.tenant_id AND j.site_id=p.site_id AND j.id=p.run_id
JOIN core_registry.forecast_model_versions mv ON mv.tenant_id=j.tenant_id AND mv.site_id=j.site_id AND mv.id=j.model_version_id
JOIN core_registry.forecast_feature_set_versions fsv ON fsv.tenant_id=mv.tenant_id AND fsv.id=mv.feature_set_version_id
WHERE p.producer='FORECAST' AND p.status='PERSISTING' AND p.updated_at < $1
ORDER BY p.updated_at,p.id LIMIT $2`, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	publications := make([]Publication, 0)
	for rows.Next() {
		var publication Publication
		var evidence []byte
		var modelVersion, featureSetVersion uint64
		if err = rows.Scan(&publication.Request.TenantID, &publication.Request.SiteID, &publication.Request.ForecastJobID, &publication.Request.ForecastSnapshotID, &evidence,
			&publication.Request.DeploymentID, &publication.Request.ModelVersionID, &publication.Request.InputSnapshotID, &publication.Request.Target,
			&publication.Request.SubjectType, &publication.Request.SubjectID, &publication.Request.ForecastOrigin, &publication.Request.HorizonMinutes, &publication.Request.Granularity,
			&publication.Request.ModelID, &modelVersion, &publication.Request.FeatureSetVersionID, &featureSetVersion, &publication.Request.TopologyVersionID); err != nil {
			return nil, err
		}
		publication.Request.ModelVersion = modelVersion
		publication.Request.FeatureSetVersion = featureSetVersion
		var value struct {
			ResultCount    int       `json:"resultCount"`
			ResultChecksum string    `json:"resultChecksum"`
			Quality        string    `json:"quality"`
			WindowStart    time.Time `json:"windowStart"`
			WindowEnd      time.Time `json:"windowEnd"`
		}
		if err = json.Unmarshal(evidence, &value); err != nil {
			return nil, err
		}
		publication.ResultCount, publication.ResultChecksum, publication.Quality = value.ResultCount, value.ResultChecksum, value.Quality
		publication.WindowStart, publication.WindowEnd = value.WindowStart, value.WindowEnd
		publications = append(publications, publication)
	}
	return publications, rows.Err()
}

func (store *PostgresStore) LatestForecast(ctx context.Context, tenantID, siteID, target string) (ForecastSnapshotReference, error) {
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) || (target != "SITE_LOAD" && target != "PV_GENERATION") {
		return ForecastSnapshotReference{}, errors.New("latest forecast scope or target is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ForecastSnapshotReference{}, err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, tenantID, siteID); err != nil {
		return ForecastSnapshotReference{}, err
	}
	var reference ForecastSnapshotReference
	var qualitySummary []byte
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.forecast_job_id::text,s.deployment_id::text,s.model_version_id::text,s.input_snapshot_id::text,
j.subject_type,j.subject_id::text,j.target,s.forecast_origin,s.window_start,s.window_end,s.result_count,s.quality_summary
FROM core_registry.forecast_snapshots s
JOIN core_registry.forecast_jobs j ON j.tenant_id=s.tenant_id AND j.site_id=s.site_id AND j.id=s.forecast_job_id
WHERE s.tenant_id=$1::uuid AND s.site_id=$2::uuid AND j.target=$3 AND j.status='PERSISTED'
ORDER BY s.forecast_origin DESC,s.created_at DESC
LIMIT 1`, tenantID, siteID, target).Scan(
		&reference.SnapshotID, &reference.ForecastJobID, &reference.DeploymentID, &reference.ModelVersionID, &reference.InputSnapshotID,
		&reference.SubjectType, &reference.SubjectID, &reference.Target, &reference.ForecastOrigin, &reference.WindowStart, &reference.WindowEnd, &reference.ResultCount, &qualitySummary,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ForecastSnapshotReference{}, ErrForecastNotFound
	}
	if err != nil {
		return ForecastSnapshotReference{}, err
	}
	var quality struct {
		ResultQuality string `json:"resultQuality"`
	}
	if err = json.Unmarshal(qualitySummary, &quality); err != nil || (quality.ResultQuality != "VALID" && quality.ResultQuality != "DEGRADED" && quality.ResultQuality != "FALLBACK") {
		return ForecastSnapshotReference{}, errors.New("persisted forecast quality summary is invalid")
	}
	reference.Quality = quality.ResultQuality
	if err = tx.Commit(ctx); err != nil {
		return ForecastSnapshotReference{}, err
	}
	return reference, nil
}

func encodeForecastJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func uuidv7(now time.Time) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	milliseconds := uint64(now.UnixMilli())
	bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5] = byte(milliseconds>>40), byte(milliseconds>>32), byte(milliseconds>>24), byte(milliseconds>>16), byte(milliseconds>>8), byte(milliseconds)
	bytes[6] = (bytes[6] & 0x0f) | 0x70
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	h := hex.EncodeToString(bytes)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func (store *PostgresStore) ResolvePreparation(ctx context.Context, request PreparationRequest, at time.Time) (PreparationDefinition, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PreparationDefinition{}, err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, request.TenantID, request.SiteID); err != nil {
		return PreparationDefinition{}, err
	}
	var definition PreparationDefinition
	var metricRefs, featureSchema []byte
	err = tx.QueryRow(ctx, `SELECT
 d.id::text,d.model_id::text,d.model_version_id::text,mv.model_version,
 d.feature_set_version_id::text,fsv.version,d.topology_version_id::text,
 m.horizon_minutes,m.granularity,ds.metric_version_refs,fsv.feature_schema
FROM core_registry.forecast_deployments d
JOIN core_registry.forecast_model_versions mv
  ON mv.tenant_id=d.tenant_id AND mv.site_id=d.site_id AND mv.id=d.model_version_id
JOIN core_registry.forecast_models m
  ON m.tenant_id=mv.tenant_id AND m.id=mv.model_id
JOIN core_registry.forecast_feature_set_versions fsv
  ON fsv.tenant_id=d.tenant_id AND fsv.id=d.feature_set_version_id
JOIN core_registry.forecast_dataset_snapshots ds
  ON ds.tenant_id=mv.tenant_id AND ds.site_id=mv.site_id AND ds.id=mv.dataset_snapshot_id
WHERE d.tenant_id=$1::uuid AND d.site_id=$2::uuid
  AND d.target=$3 AND d.subject_type=$4 AND d.subject_id=$5::uuid
  AND d.status='ACTIVE' AND d.effective_from <= $6
  AND (d.effective_to IS NULL OR d.effective_to > $6)
LIMIT 1`, request.TenantID, request.SiteID, request.Target, request.SubjectType, request.SubjectID, at.UTC()).Scan(
		&definition.DeploymentID, &definition.ModelID, &definition.ModelVersionID, &definition.ModelVersion,
		&definition.FeatureSetVersionID, &definition.FeatureSetVersion, &definition.TopologyVersionID,
		&definition.HorizonMinutes, &definition.Granularity, &metricRefs, &featureSchema,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationDefinition{}, fmt.Errorf("%w: no active Forecast deployment for requested scope", ErrPreparationUnavailable)
	}
	if err != nil {
		return PreparationDefinition{}, err
	}
	if err = json.Unmarshal(metricRefs, &definition.MetricVersionRefs); err != nil {
		return PreparationDefinition{}, fmt.Errorf("decode Forecast dataset metric_version_refs: %w", err)
	}
	definition.FeatureSchema = append(json.RawMessage(nil), featureSchema...)
	if err = tx.Commit(ctx); err != nil {
		return PreparationDefinition{}, err
	}
	return definition, nil
}

func (store *PostgresStore) CreatePreparedForecast(ctx context.Context, input PreparedInput, at time.Time) (PreparedForecast, error) {
	inputSnapshotID, err := uuidv7(at)
	if err != nil {
		return PreparedForecast{}, err
	}
	forecastJobID, err := uuidv7(at)
	if err != nil {
		return PreparedForecast{}, err
	}
	forecastSnapshotID, err := uuidv7(at)
	if err != nil {
		return PreparedForecast{}, err
	}
	metricRefs, err := json.Marshal(input.MetricVersionRefs)
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("encode Forecast input metric provenance: %w", err)
	}
	featureValues, err := json.Marshal(input.FeatureValues)
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("encode Forecast frozen feature values: %w", err)
	}
	schedulerPayload, err := json.Marshal(SchedulerForecastReference{ForecastJobID: forecastJobID, ForecastSnapshotID: forecastSnapshotID})
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("encode Forecast scheduler reference: %w", err)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return PreparedForecast{}, err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, input.TenantID, input.SiteID); err != nil {
		return PreparedForecast{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.forecast_input_snapshots(
 id,tenant_id,site_id,deployment_id,model_version_id,feature_set_version_id,topology_version_id,
 latest_data_time,weather_issue_time,metric_version_refs,feature_values,input_checksum,captured_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8,$9,$10::jsonb,$11::jsonb,$12,$13)`,
		inputSnapshotID, input.TenantID, input.SiteID, input.DeploymentID, input.ModelVersionID, input.FeatureSetVersionID, input.TopologyVersionID,
		input.LatestDataTime.UTC(), input.WeatherIssueTime, metricRefs, featureValues, input.InputChecksum, at.UTC()); err != nil {
		return PreparedForecast{}, fmt.Errorf("insert Forecast input snapshot: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.forecast_jobs(
 id,tenant_id,site_id,deployment_id,model_version_id,input_snapshot_id,target,subject_type,subject_id,
 forecast_origin,horizon_minutes,granularity,trigger_type,status,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9::uuid,$10,$11,$12,'ON_DEMAND','PENDING',1,$13,$13)`,
		forecastJobID, input.TenantID, input.SiteID, input.DeploymentID, input.ModelVersionID, inputSnapshotID,
		input.Target, input.SubjectType, input.SubjectID, input.ForecastOrigin.UTC(), input.HorizonMinutes, input.Granularity, at.UTC()); err != nil {
		return PreparedForecast{}, fmt.Errorf("insert Forecast job: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.job_instances(
 job_id,trigger_type,job_type,tenant_id,site_id,subject_type,subject_id,scheduled_for,priority,dedup_key,payload,state,max_attempts,timeout_seconds,created_at,updated_at)
VALUES($1::uuid,'MANUAL','FORECAST_RUN',$2::uuid,$3::uuid,$4,$5,$6,50,$7,$8::jsonb,'READY',3,120,$6,$6)`,
		forecastJobID, input.TenantID, input.SiteID, input.SubjectType, input.SubjectID, at.UTC(), "forecast:"+forecastJobID, schedulerPayload); err != nil {
		return PreparedForecast{}, fmt.Errorf("insert Forecast scheduler job: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return PreparedForecast{}, err
	}
	return PreparedForecast{ForecastJobID: forecastJobID, InputSnapshotID: inputSnapshotID, ForecastSnapshotID: forecastSnapshotID, Status: "PENDING"}, nil
}

func (store *PostgresStore) LoadForecastRequest(ctx context.Context, tenantID, siteID string, reference SchedulerForecastReference) (Request, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	if err = forecastScope(ctx, tx, tenantID, siteID); err != nil {
		return Request{}, err
	}
	var request Request
	var snapshot PreparedInput
	var metricRefs, featureValues []byte
	var checksum string
	err = tx.QueryRow(ctx, `SELECT
 j.deployment_id::text,d.model_id::text,j.model_version_id::text,mv.model_version,
 mv.feature_set_version_id::text,fsv.version,mv.topology_version_id::text,j.input_snapshot_id::text,
 j.subject_type,j.subject_id::text,j.target,j.forecast_origin,j.horizon_minutes,j.granularity,
 i.latest_data_time,i.weather_issue_time,i.metric_version_refs,i.feature_values,i.input_checksum
FROM core_registry.forecast_jobs j
JOIN core_registry.forecast_deployments d
  ON d.tenant_id=j.tenant_id AND d.site_id=j.site_id AND d.id=j.deployment_id
JOIN core_registry.forecast_model_versions mv
  ON mv.tenant_id=j.tenant_id AND mv.site_id=j.site_id AND mv.id=j.model_version_id
JOIN core_registry.forecast_feature_set_versions fsv
  ON fsv.tenant_id=mv.tenant_id AND fsv.id=mv.feature_set_version_id
JOIN core_registry.forecast_input_snapshots i
  ON i.tenant_id=j.tenant_id AND i.site_id=j.site_id AND i.id=j.input_snapshot_id
WHERE j.tenant_id=$1::uuid AND j.site_id=$2::uuid AND j.id=$3::uuid`, tenantID, siteID, reference.ForecastJobID).Scan(
		&request.DeploymentID, &request.ModelID, &request.ModelVersionID, &request.ModelVersion,
		&request.FeatureSetVersionID, &request.FeatureSetVersion, &request.TopologyVersionID, &request.InputSnapshotID,
		&request.SubjectType, &request.SubjectID, &request.Target, &request.ForecastOrigin, &request.HorizonMinutes, &request.Granularity,
		&snapshot.LatestDataTime, &snapshot.WeatherIssueTime, &metricRefs, &featureValues, &checksum,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, errors.New("server-created Forecast job was not found")
	}
	if err != nil {
		return Request{}, err
	}
	if err = json.Unmarshal(metricRefs, &snapshot.MetricVersionRefs); err != nil {
		return Request{}, fmt.Errorf("decode frozen Forecast metric provenance: %w", err)
	}
	if err = json.Unmarshal(featureValues, &snapshot.FeatureValues); err != nil {
		return Request{}, fmt.Errorf("decode frozen Forecast feature values: %w", err)
	}
	request.TenantID, request.SiteID = tenantID, siteID
	request.ForecastJobID, request.ForecastSnapshotID = reference.ForecastJobID, reference.ForecastSnapshotID
	snapshot.TenantID, snapshot.SiteID, snapshot.SubjectType, snapshot.SubjectID, snapshot.Target = tenantID, siteID, request.SubjectType, request.SubjectID, request.Target
	snapshot.DeploymentID, snapshot.ModelID, snapshot.ModelVersionID, snapshot.ModelVersion = request.DeploymentID, request.ModelID, request.ModelVersionID, request.ModelVersion
	snapshot.FeatureSetVersionID, snapshot.FeatureSetVersion, snapshot.TopologyVersionID = request.FeatureSetVersionID, request.FeatureSetVersion, request.TopologyVersionID
	recomputed, checksumErr := checksumPreparedInput(snapshot)
	if checksumErr != nil {
		return Request{}, checksumErr
	}
	if recomputed != checksum {
		return Request{}, errors.New("frozen Forecast input checksum does not match persisted provenance")
	}
	request.Observations, request.Unit, err = executionInput(snapshot.FeatureValues)
	if err != nil {
		return Request{}, fmt.Errorf("load frozen Forecast execution input: %w", err)
	}
	if err = request.Validate(); err != nil {
		return Request{}, fmt.Errorf("validate frozen Forecast execution request: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	return request, nil
}
