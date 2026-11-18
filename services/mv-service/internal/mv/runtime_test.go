package mv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
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
	cfg, err := ParsePostgresPoolConfig("postgres://mv:secret@127.0.0.1:5432/hvac?sslmode=disable")
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

func TestClickHouseHoursPoolingDrainAndPing(t *testing.T) {
	var newConnections atomic.Int32
	var seenPing atomic.Int32

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "SELECT 1" {
			seenPing.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("1\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(""))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	hours, err := NewClickHouseHours(server.URL, "mv_reader", "secret", nil)
	if err != nil {
		t.Fatalf("NewClickHouseHours failed: %v", err)
	}
	defer hours.CloseIdleConnections()

	transport, ok := hours.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", hours.client.Transport)
	}
	if transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 32 || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("unexpected transport pool config: %+v", transport)
	}

	if err := hours.Ping(context.Background()); err != nil {
		t.Fatalf("first Ping failed: %v", err)
	}
	if err := hours.Ping(context.Background()); err != nil {
		t.Fatalf("second Ping failed: %v", err)
	}
	if _, err := hours.query(context.Background(), "SELECT 1 FORMAT JSONEachRow"); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if seenPing.Load() != 2 {
		t.Fatalf("seenPing = %d, want 2", seenPing.Load())
	}
	if got := newConnections.Load(); got != 1 {
		t.Fatalf("new TCP connections = %d, want 1", got)
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
	if !strings.Contains(metricsRec.Body.String(), `hvac_service_up{service="mv-service"} 1`) {
		t.Fatalf("GET /metrics missing hvac_service_up gauge:\n%s", metricsRec.Body.String())
	}

	var chErr atomic.Value
	rt := observability.NewRuntime(observability.RuntimeConfig{Service: "mv-service"})
	rt.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: func(context.Context) error { return nil }},
		observability.Dependency{Name: "clickhouse", Required: true, Check: func(context.Context) error {
			if v := chErr.Load(); v != nil {
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

	chErr.Store(errors.New("clickhouse unreachable"))
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
	if !strings.Contains(diagMetrics.Body.String(), `hvac_service_up{service="mv-service"} 1`) {
		t.Fatalf("runtime /metrics missing hvac_service_up gauge:\n%s", diagMetrics.Body.String())
	}
}

type fakeMVQueue struct {
	jobs          []schedulerjob.Job
	failCalls     int
	lastFailRetry bool
	lastFailCtx   error
}

func (q *fakeMVQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]schedulerjob.Job, error) {
	return q.jobs, nil
}
func (q *fakeMVQueue) Start(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
	return true, nil
}
func (q *fakeMVQueue) Complete(context.Context, schedulerjob.Job, any, time.Time) error {
	return nil
}
func (q *fakeMVQueue) Fail(ctx context.Context, _ schedulerjob.Job, _ string, _ error, retryable bool, _ time.Time) error {
	q.failCalls++
	q.lastFailRetry = retryable
	q.lastFailCtx = ctx.Err()
	return nil
}

type fakeMVVerifier struct {
	fn func(context.Context, string, string, time.Time) error
}

func (v fakeMVVerifier) VerifyDueDays(ctx context.Context, tenantID, siteID string, asOf time.Time) error {
	return v.fn(ctx, tenantID, siteID, asOf)
}

func TestRunScheduledEvaluationsWithLoggerSIGTERMMarksRetryableAndFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := &fakeMVQueue{
		jobs: []schedulerjob.Job{{
			JobID:          "01990000-1111-7000-8000-000000000001",
			JobType:        EvaluationJobType,
			TriggerType:    "SCHEDULE",
			TenantID:       "01990000-3000-7000-8000-000000000001",
			SiteID:         "01990000-5000-7000-8000-000000000001",
			ScheduledFor:   time.Now().UTC(),
			TimeoutSeconds: 30,
		}},
	}
	verifier := fakeMVVerifier{fn: func(jobCtx context.Context, _, _ string, _ time.Time) error {
		cancel()
		<-jobCtx.Done()
		return jobCtx.Err()
	}}
	var logBuf bytes.Buffer
	logger := observability.NewJSONLogger(&logBuf, slog.LevelInfo)

	if err := RunScheduledEvaluationsWithLogger(ctx, queue, verifier, "w1", 8, logger); err != nil {
		t.Fatalf("RunScheduledEvaluationsWithLogger returned error: %v", err)
	}
	if queue.failCalls != 1 || !queue.lastFailRetry || queue.lastFailCtx != nil {
		t.Fatalf("unexpected fail state: calls=%d retry=%t ctxErr=%v", queue.failCalls, queue.lastFailRetry, queue.lastFailCtx)
	}
}
