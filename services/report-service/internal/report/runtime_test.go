package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

func TestParsePostgresPoolConfigAndNilSafety(t *testing.T) {
	cfg, err := ParsePostgresPoolConfig("postgres://report:secret@127.0.0.1:5432/hvac?sslmode=disable")
	if err != nil {
		t.Fatalf("ParsePostgresPoolConfig failed: %v", err)
	}
	if cfg.MaxConns != 16 || cfg.MinConns != 1 || cfg.MaxConnLifetime != 30*time.Minute || cfg.MaxConnIdleTime != 5*time.Minute || cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("unexpected pgxpool config: MaxConns=%d MinConns=%d MaxConnLifetime=%s MaxConnIdleTime=%s HealthCheckPeriod=%s",
			cfg.MaxConns, cfg.MinConns, cfg.MaxConnLifetime, cfg.MaxConnIdleTime, cfg.HealthCheckPeriod)
	}
	var nilStore *PostgresStore
	nilStore.Close()
	if err := nilStore.Ping(context.Background()); err == nil {
		t.Fatal("nil PostgresStore.Ping should return error")
	}
}

func TestDiagnosticsHandlerHealthzMetricsAndReadiness(t *testing.T) {
	healthRec := httptest.NewRecorder()
	HealthHandler().ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthRec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", healthRec.Code)
	}

	metricsRec := httptest.NewRecorder()
	HealthHandler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricsRec.Body.String(), `hvac_service_up{service="report-service"} 1`) {
		t.Fatalf("GET /metrics missing hvac_service_up gauge:\n%s", metricsRec.Body.String())
	}

	var pgErr atomic.Value
	rt := observability.NewRuntime(observability.RuntimeConfig{Service: "report-service"})
	rt.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: func(context.Context) error {
			if v := pgErr.Load(); v != nil {
				return v.(error)
			}
			return nil
		}},
	)
	rt.MarkReady()
	diag := NewDiagnosticsHandler(rt)

	okRec := httptest.NewRecorder()
	diag.ServeHTTP(okRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if okRec.Code != http.StatusOK {
		t.Fatalf("GET /health/ready = %d, want 200", okRec.Code)
	}

	apiRec := httptest.NewRecorder()
	diag.ServeHTTP(apiRec, httptest.NewRequest(http.MethodGet, "/v1/sites/s1/reports/monthly", nil))
	if apiRec.Code == http.StatusOK {
		t.Fatalf("diagnostics handler must not serve API routes, got 200")
	}

	pgErr.Store(errors.New("postgres down"))
	failRec := httptest.NewRecorder()
	diag.ServeHTTP(failRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health/ready failed dep = %d, want 503", failRec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(failRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid readiness JSON: %v", err)
	}

	diagMetrics := httptest.NewRecorder()
	diag.ServeHTTP(diagMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(diagMetrics.Body.String(), `hvac_service_up{service="report-service"} 1`) {
		t.Fatalf("runtime /metrics missing hvac_service_up gauge:\n%s", diagMetrics.Body.String())
	}
}

type fakeReportQueue struct {
	jobs          []schedulerjob.Job
	failCalls     int
	lastFailRetry bool
	lastFailCtx   error
}

func (q *fakeReportQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]schedulerjob.Job, error) {
	return q.jobs, nil
}
func (q *fakeReportQueue) Start(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
	return true, nil
}
func (q *fakeReportQueue) Complete(context.Context, schedulerjob.Job, any, time.Time) error {
	return nil
}
func (q *fakeReportQueue) Fail(ctx context.Context, _ schedulerjob.Job, _ string, _ error, retryable bool, _ time.Time) error {
	q.failCalls++
	q.lastFailRetry = retryable
	q.lastFailCtx = ctx.Err()
	return nil
}

type fakeReportGenerator struct {
	fn func(context.Context, string, string, string, *string) (StoredSnapshot, error)
}

func (g fakeReportGenerator) Generate(ctx context.Context, tenantID, siteID, month string, generatedByJob *string) (StoredSnapshot, error) {
	return g.fn(ctx, tenantID, siteID, month, generatedByJob)
}
func (g fakeReportGenerator) MonthBefore(context.Context, string, string, time.Time) (string, error) {
	return "2026-09", nil
}

func TestRunScheduledGenerationsWithLoggerSIGTERMMarksRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := &fakeReportQueue{
		jobs: []schedulerjob.Job{{
			JobID:          "01990000-1111-7000-8000-000000000001",
			JobType:        GenerationJobType,
			TriggerType:    "SCHEDULE",
			TenantID:       "01990000-3000-7000-8000-000000000001",
			SiteID:         "01990000-5000-7000-8000-000000000001",
			ScheduledFor:   time.Now().UTC(),
			Payload:        json.RawMessage(`{"month":"2026-09"}`),
			TimeoutSeconds: 30,
		}},
	}
	gen := fakeReportGenerator{fn: func(jobCtx context.Context, _, _, _ string, _ *string) (StoredSnapshot, error) {
		cancel()
		<-jobCtx.Done()
		return StoredSnapshot{}, jobCtx.Err()
	}}
	var logBuf bytes.Buffer
	logger := observability.NewJSONLogger(&logBuf, slog.LevelInfo)

	if err := RunScheduledGenerationsWithLogger(ctx, queue, gen, "w1", 4, logger); err != nil {
		t.Fatalf("RunScheduledGenerationsWithLogger returned error: %v", err)
	}
	if queue.failCalls != 1 || !queue.lastFailRetry || queue.lastFailCtx != nil {
		t.Fatalf("unexpected fail state: calls=%d retry=%t ctxErr=%v", queue.failCalls, queue.lastFailRetry, queue.lastFailCtx)
	}
}
