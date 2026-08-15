package metric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("metric postgres pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func scope(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+siteID+"}")
	return err
}

func (s *PostgresStore) LoadSchedule(ctx context.Context, tenantID, siteID, bindingID string, at time.Time) (Schedule, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, tenantID, siteID); err != nil {
		return Schedule{}, err
	}
	var schedule Schedule
	err = tx.QueryRow(ctx, `SELECT b.time_granularity, s.timezone
FROM core_registry.metric_bindings b
JOIN core_registry.metric_versions v ON v.tenant_id=b.tenant_id AND v.id=b.metric_version_id
JOIN core_registry.sites s ON s.tenant_id=b.tenant_id AND s.id=b.site_id
WHERE b.tenant_id=$1::uuid AND b.site_id=$2::uuid AND b.id=$3::uuid
  AND b.status='RELEASED' AND v.status='RELEASED'
  AND b.effective_from <= $4 AND (b.effective_to IS NULL OR b.effective_to > $4)
  AND v.effective_from <= $4 AND (v.effective_to IS NULL OR v.effective_to > $4)`, tenantID, siteID, bindingID, at.UTC()).Scan(&schedule.Granularity, &schedule.Timezone)
	if err != nil {
		return Schedule{}, fmt.Errorf("load metric schedule: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Schedule{}, err
	}
	return schedule, nil
}

func (s *PostgresStore) HasActiveScheduledRun(ctx context.Context, tenantID, siteID, bindingID string, period Period) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, tenantID, siteID); err != nil {
		return false, err
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM core_registry.metric_calculation_runs
  WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND metric_binding_id=$3::uuid
    AND period_start=$4 AND period_end=$5 AND run_reason='SCHEDULED' AND status <> 'FAILED'
)`, tenantID, siteID, bindingID, period.Start.UTC(), period.End.UTC()).Scan(&exists)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *PostgresStore) LoadBinding(ctx context.Context, tenantID, siteID, bindingID string, at time.Time) (Binding, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, tenantID, siteID); err != nil {
		return Binding{}, err
	}
	var b Binding
	var source []byte
	var unit *string
	err = tx.QueryRow(ctx, `SELECT b.tenant_id::text,b.site_id::text,b.id::text,b.metric_version_id::text,b.metric_id::text,m.metric_code,
 b.metric_version,b.binding_version,b.subject_type,b.subject_id::text,b.time_granularity,v.data_type,v.unit_code,v.aggregation,v.calculation_method,v.quality_policy,b.source_definition
 FROM core_registry.metric_bindings b JOIN core_registry.metric_versions v ON v.tenant_id=b.tenant_id AND v.id=b.metric_version_id
 JOIN core_registry.metrics m ON m.tenant_id=b.tenant_id AND m.id=b.metric_id
 WHERE b.tenant_id=$1::uuid AND b.site_id=$2::uuid AND b.id=$3::uuid AND b.status='RELEASED'
 AND b.effective_from <= $4 AND (b.effective_to IS NULL OR b.effective_to > $4)
 AND v.status='RELEASED' AND v.effective_from <= $4 AND (v.effective_to IS NULL OR v.effective_to > $4)`, tenantID, siteID, bindingID, at.UTC()).Scan(
		&b.TenantID, &b.SiteID, &b.BindingID, &b.MetricVersionID, &b.MetricID, &b.MetricCode,
		&b.MetricVersion, &b.BindingVersion, &b.SubjectType, &b.SubjectID, &b.Granularity,
		&b.DataType, &unit, &b.Aggregation, &b.CalculationMethod, &b.QualityPolicy, &source,
	)
	if err != nil {
		return Binding{}, fmt.Errorf("load metric binding: %w", err)
	}
	if unit != nil {
		b.Unit = *unit
	}
	if err = json.Unmarshal(source, &b.SourceDefinition); err != nil {
		return Binding{}, fmt.Errorf("decode metric source definition: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT dependency_type,dependency_code,coalesce(dependency_metric_id::text,''),required FROM core_registry.metric_dependencies WHERE tenant_id=$1::uuid AND metric_version_id=$2::uuid ORDER BY sort_order`, tenantID, b.MetricVersionID)
	if err != nil {
		return Binding{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Dependency
		if err = rows.Scan(&d.Type, &d.Code, &d.MetricID, &d.Required); err != nil {
			return Binding{}, err
		}
		b.Dependencies = append(b.Dependencies, d)
	}
	if err = rows.Err(); err != nil {
		return Binding{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Binding{}, err
	}
	return b, nil
}

func (s *PostgresStore) CreateRun(ctx context.Context, r Result, reason string, inputRefs []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, r.Binding.TenantID, r.Binding.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.metric_calculation_runs(id,tenant_id,site_id,metric_binding_id,metric_version_id,subject_type,subject_id,binding_version,period_start,period_end,granularity,run_reason,input_refs,status,revision,created_at,updated_at)
 VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8,$9,$10,$11,$12,$13::jsonb,'PENDING',1,$14,$14)`, r.RunID, r.Binding.TenantID, r.Binding.SiteID, r.Binding.BindingID, r.Binding.MetricVersionID, r.Binding.SubjectType, r.Binding.SubjectID, r.Binding.BindingVersion, r.PeriodStart, r.PeriodEnd, r.Binding.Granularity, reason, inputRefs, r.CalculatedAt)
	if err != nil {
		return fmt.Errorf("create metric calculation run: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) MarkRunRunning(ctx context.Context, tenantID, siteID, runID string, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, tenantID, siteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.metric_calculation_runs
SET status='RUNNING',started_at=$4,updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='PENDING'`, tenantID, siteID, runID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("metric calculation run RUNNING transition rejected")
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) BeginPublication(ctx context.Context, r Result, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, r.Binding.TenantID, r.Binding.SiteID); err != nil {
		return err
	}
	publicationID, err := uuidv7(at)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.metric_calculation_runs
SET status='PERSISTING',updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status='RUNNING'`, r.Binding.TenantID, r.Binding.SiteID, r.RunID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("metric calculation run PERSISTING transition rejected")
	}
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.cross_store_publications(
id,tenant_id,site_id,producer,run_id,result_id,target_store,target_dataset,status,started_at,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'METRIC',$4::uuid,$5::uuid,'CLICKHOUSE','analytics.metric_series','PERSISTING',$6,1,$6,$6)`, publicationID, r.Binding.TenantID, r.Binding.SiteID, r.RunID, r.ResultID, at)
	if err != nil {
		return fmt.Errorf("create metric cross-store publication: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) CompletePublication(ctx context.Context, r Result, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, r.Binding.TenantID, r.Binding.SiteID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.cross_store_publications
SET status='PERSISTED',persisted_at=COALESCE(persisted_at,$4),updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND producer='METRIC' AND run_id=$2::uuid AND result_id=$3::uuid AND status IN ('PERSISTING','PERSISTED')`, r.Binding.TenantID, r.RunID, r.ResultID, at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.metric_calculation_runs
SET status='PERSISTED',completed_at=COALESCE(completed_at,$4),updated_at=$4,revision=revision+1
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('PERSISTING','PERSISTED')`, r.Binding.TenantID, r.Binding.SiteID, r.RunID, at)
	if err != nil {
		return err
	}
	eventID, err := uuidv7(at)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"resultId": r.ResultID, "calculationRunId": r.RunID, "metricId": r.Binding.MetricID,
		"metricVersionId": r.Binding.MetricVersionID, "bindingId": r.Binding.BindingID,
		"subjectType": r.Binding.SubjectType, "subjectId": r.Binding.SubjectID,
		"periodStart": r.PeriodStart, "periodEnd": r.PeriodEnd,
	})
	_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_outbox_events(
id,tenant_id,site_id,event_type,schema_version,subject_type,subject_id,aggregate_type,aggregate_id,aggregate_version,occurred_at,payload,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'METRIC_RESULT_UPDATED',1,$4,$5::uuid,'METRIC_RESULT',$6::uuid,1,$7,$8::jsonb,$7)
ON CONFLICT (tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type) DO NOTHING`, eventID, r.Binding.TenantID, r.Binding.SiteID, r.Binding.SubjectType, r.Binding.SubjectID, r.ResultID, at, payload)
	if err != nil {
		return fmt.Errorf("create METRIC_RESULT_UPDATED outbox event: %w", err)
	}
	var actualEventID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM core_registry.domain_outbox_events
WHERE tenant_id=$1::uuid AND aggregate_type='METRIC_RESULT' AND aggregate_id=$2::uuid AND aggregate_version=1 AND event_type='METRIC_RESULT_UPDATED'`, r.Binding.TenantID, r.ResultID).Scan(&actualEventID); err != nil {
		return err
	}
	for _, consumer := range []string{"settlement", "forecast-trigger", "alarm-rule", "audit-notification"} {
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_event_deliveries(event_id,consumer_name,status,attempt,created_at,updated_at)
VALUES($1::uuid,$2,'PENDING',0,$3,$3) ON CONFLICT (event_id,consumer_name) DO NOTHING`, actualEventID, consumer, at)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) FailRun(ctx context.Context, r Result, failureCode string, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, r.Binding.TenantID, r.Binding.SiteID); err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `UPDATE core_registry.cross_store_publications SET status='FAILED',last_error=$4,updated_at=$5,revision=revision+1 WHERE tenant_id=$1::uuid AND producer='METRIC' AND run_id=$2::uuid AND result_id=$3::uuid AND status='PERSISTING'`, r.Binding.TenantID, r.RunID, r.ResultID, failureCode, at)
	_, err = tx.Exec(ctx, `UPDATE core_registry.metric_calculation_runs SET status='FAILED',completed_at=$4,updated_at=$4,revision=revision+1 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status IN ('RUNNING','PERSISTING')`, r.Binding.TenantID, r.Binding.SiteID, r.RunID, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ListStalePublicationsForScope(ctx context.Context, tenantID, siteID string, staleBefore time.Time, limit int) ([]Result, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, tenantID, siteID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT p.run_id::text,p.result_id::text,p.tenant_id::text,p.site_id::text,
b.id::text,b.metric_version_id::text,b.metric_id::text,m.metric_code,b.metric_version,b.binding_version,
b.subject_type,b.subject_id::text,b.time_granularity,v.data_type,coalesce(v.unit_code,''),v.aggregation,v.calculation_method,v.quality_policy,
r.period_start,r.period_end
FROM core_registry.cross_store_publications p
JOIN core_registry.metric_calculation_runs r ON r.tenant_id=p.tenant_id AND r.id=p.run_id
JOIN core_registry.metric_bindings b ON b.tenant_id=r.tenant_id AND b.id=r.metric_binding_id
JOIN core_registry.metric_versions v ON v.tenant_id=b.tenant_id AND v.id=b.metric_version_id
JOIN core_registry.metrics m ON m.tenant_id=b.tenant_id AND m.id=b.metric_id
WHERE p.tenant_id=$1::uuid AND p.site_id=$2::uuid AND p.producer='METRIC' AND p.status='PERSISTING' AND p.updated_at < $3
ORDER BY p.updated_at LIMIT $4`, tenantID, siteID, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]Result, 0)
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.RunID, &r.ResultID, &r.Binding.TenantID, &r.Binding.SiteID,
			&r.Binding.BindingID, &r.Binding.MetricVersionID, &r.Binding.MetricID, &r.Binding.MetricCode,
			&r.Binding.MetricVersion, &r.Binding.BindingVersion, &r.Binding.SubjectType, &r.Binding.SubjectID,
			&r.Binding.Granularity, &r.Binding.DataType, &r.Binding.Unit, &r.Binding.Aggregation,
			&r.Binding.CalculationMethod, &r.Binding.QualityPolicy, &r.PeriodStart, &r.PeriodEnd); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *PostgresStore) ListStalePublications(ctx context.Context, staleBefore time.Time, limit int) ([]Result, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.run_id::text,p.result_id::text,p.tenant_id::text,p.site_id::text,
b.id::text,b.metric_version_id::text,b.metric_id::text,m.metric_code,b.metric_version,b.binding_version,
b.subject_type,b.subject_id::text,b.time_granularity,v.data_type,coalesce(v.unit_code,''),v.aggregation,v.calculation_method,v.quality_policy,
r.period_start,r.period_end
FROM core_registry.cross_store_publications p
JOIN core_registry.metric_calculation_runs r ON r.tenant_id=p.tenant_id AND r.id=p.run_id
JOIN core_registry.metric_bindings b ON b.tenant_id=r.tenant_id AND b.id=r.metric_binding_id
JOIN core_registry.metric_versions v ON v.tenant_id=b.tenant_id AND v.id=b.metric_version_id
JOIN core_registry.metrics m ON m.tenant_id=b.tenant_id AND m.id=b.metric_id
WHERE p.producer='METRIC' AND p.status='PERSISTING' AND p.updated_at < $1
ORDER BY p.updated_at LIMIT $2`, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]Result, 0)
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.RunID, &r.ResultID, &r.Binding.TenantID, &r.Binding.SiteID,
			&r.Binding.BindingID, &r.Binding.MetricVersionID, &r.Binding.MetricID, &r.Binding.MetricCode,
			&r.Binding.MetricVersion, &r.Binding.BindingVersion, &r.Binding.SubjectType, &r.Binding.SubjectID,
			&r.Binding.Granularity, &r.Binding.DataType, &r.Binding.Unit, &r.Binding.Aggregation,
			&r.Binding.CalculationMethod, &r.Binding.QualityPolicy, &r.PeriodStart, &r.PeriodEnd); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

var _ pgconn.CommandTag
