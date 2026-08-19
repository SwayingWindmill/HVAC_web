package metric

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMetricDependencyRevisionAndCycleGuards(t *testing.T) {
	runtimeDSN := os.Getenv("S08_METRIC_POSTGRES_URL")
	adminDSN := os.Getenv("S08_METRIC_ADMIN_POSTGRES_URL")
	if runtimeDSN == "" || adminDSN == "" {
		t.Skip("S08_METRIC_POSTGRES_URL and S08_METRIC_ADMIN_POSTGRES_URL are required")
	}
	ctx := context.Background()
	runtimeConfig, err := pgxpool.ParseConfig(runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	adminConfig, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()

	const (
		tenantID  = "01910000-0000-7000-8000-000000000101"
		siteID    = "01910000-0000-7000-8000-000000000102"
		metricID  = "01910000-0000-7000-8000-000000000103"
		v1ID      = "01910000-0000-7000-8000-000000000104"
		v2ID      = "01910000-0000-7000-8000-000000000105"
		bindingID = "01910000-0000-7000-8000-000000000106"
		resultID  = "01910000-0000-7000-8000-000000000107"
		runID     = "01910000-0000-7000-8000-000000000108"
		metricA   = "01910000-0000-7000-8000-000000000109"
		metricB   = "01910000-0000-7000-8000-00000000010a"
		versionA  = "01910000-0000-7000-8000-00000000010b"
		versionB  = "01910000-0000-7000-8000-00000000010c"
		depA      = "01910000-0000-7000-8000-00000000010d"
		depB      = "01910000-0000-7000-8000-00000000010e"
	)
	seedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = adminPool.Exec(ctx, `
INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at)
VALUES($1::uuid,'s08-test','S08 Test','UTC','USD','US','ACTIVE',1,$2,$2);
INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at)
VALUES($3::uuid,$1::uuid,'s08-site','S08 Site','UTC','ACTIVE',1,$2,$2);
INSERT INTO core_registry.metrics(id,tenant_id,metric_code,metric_name,category,status,revision,created_at,updated_at)
VALUES
 ($4::uuid,$1::uuid,'dependency_metric','Dependency Metric','test','ACTIVE',1,$2,$2),
 ($5::uuid,$1::uuid,'cycle_a','Cycle A','test','ACTIVE',1,$2,$2),
 ($6::uuid,$1::uuid,'cycle_b','Cycle B','test','ACTIVE',1,$2,$2);
INSERT INTO core_registry.metric_versions(id,tenant_id,metric_id,version,data_type,subject_type,time_granularity,aggregation,calculation_method,quality_policy,effective_from,effective_to,status,revision,created_at,updated_at)
VALUES
 ($7::uuid,$1::uuid,$4::uuid,1,'NUMBER','SITE','HOUR','AVG','IDENTITY','STRICT','2026-01-01T00:00:00Z','2026-06-01T00:00:00Z','RELEASED',1,$2,$2),
 ($8::uuid,$1::uuid,$4::uuid,2,'NUMBER','SITE','HOUR','AVG','IDENTITY','STRICT','2026-06-01T00:00:00Z',NULL,'RELEASED',1,$2,$2),
 ($9::uuid,$1::uuid,$5::uuid,1,'NUMBER','SITE','HOUR','AVG','IDENTITY','STRICT','2026-01-01T00:00:00Z',NULL,'DRAFT',1,$2,$2),
 ($10::uuid,$1::uuid,$6::uuid,1,'NUMBER','SITE','HOUR','AVG','IDENTITY','STRICT','2026-01-01T00:00:00Z',NULL,'DRAFT',1,$2,$2);
INSERT INTO core_registry.metric_bindings(id,tenant_id,site_id,metric_version_id,metric_id,metric_version,binding_version,subject_type,subject_id,time_granularity,source_definition,effective_from,status,revision,created_at,updated_at)
VALUES($11::uuid,$1::uuid,$3::uuid,$8::uuid,$4::uuid,2,1,'SITE',$3::uuid,'HOUR','{"externals":{"x":1}}'::jsonb,'2026-06-01T00:00:00Z','RELEASED',1,$2,$2);
INSERT INTO core_registry.metric_result_heads(tenant_id,site_id,metric_id,subject_type,subject_id,granularity,period_start,period_end,last_allocated_revision,current_revision,current_result_id,current_run_id,metric_version_id,metric_binding_id,binding_version,value_type,value_number,quality,completeness,calculated_at,updated_at)
VALUES($1::uuid,$3::uuid,$4::uuid,'SITE',$3::uuid,'HOUR','2026-02-01T00:00:00Z','2026-02-01T01:00:00Z',1,1,$12::uuid,$13::uuid,$8::uuid,$11::uuid,1,'NUMBER',42,'GOOD',1,'2026-02-01T01:05:00Z','2026-02-01T01:05:00Z');
INSERT INTO core_registry.metric_dependencies(id,tenant_id,metric_version_id,dependency_type,dependency_code,dependency_metric_id,sort_order,required,revision,created_at,updated_at)
VALUES($14::uuid,$1::uuid,$9::uuid,'METRIC','b',$6::uuid,0,true,1,$2,$2);`,
		tenantID, seedAt, siteID, metricID, metricA, metricB, v1ID, v2ID, versionA, versionB, bindingID, resultID, runID, depA)
	if err != nil {
		t.Fatal(err)
	}

	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.LoadCurrentMetricInput(ctx, Binding{TenantID: tenantID, SiteID: siteID, SubjectType: "SITE", SubjectID: siteID, Granularity: "HOUR"}, Dependency{Type: "METRIC", MetricID: metricID},
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 1, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "revision does not match") {
		t.Fatalf("revision mismatch error = %v", err)
	}

	_, err = adminPool.Exec(ctx, `INSERT INTO core_registry.metric_dependencies(id,tenant_id,metric_version_id,dependency_type,dependency_code,dependency_metric_id,sort_order,required,revision,created_at,updated_at)
VALUES($1::uuid,$2::uuid,$3::uuid,'METRIC','a',$4::uuid,0,true,1,$5,$5)`, depB, tenantID, versionB, metricA, seedAt)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || !strings.Contains(pgErr.Message, "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}
