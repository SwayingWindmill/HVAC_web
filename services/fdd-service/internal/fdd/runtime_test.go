package fdd

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
	cfg, err := ParsePostgresPoolConfig("postgres://fdd:secret@127.0.0.1:5432/hvac?sslmode=disable")
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

func TestClickHouseEvidencePoolingDrainAndPing(t *testing.T) {
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
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	evidence, err := NewClickHouseEvidence(server.URL, "fdd_reader", "secret", nil)
	if err != nil {
		t.Fatalf("NewClickHouseEvidence failed: %v", err)
	}
	defer evidence.CloseIdleConnections()

	transport, ok := evidence.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", evidence.client.Transport)
	}
	if transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 32 || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("unexpected transport pool config: %+v", transport)
	}

	if err := evidence.Ping(context.Background()); err != nil {
		t.Fatalf("first Ping failed: %v", err)
	}
	if err := evidence.Ping(context.Background()); err != nil {
		t.Fatalf("second Ping failed: %v", err)
	}
	now := time.Now().UTC()
	if _, err := evidence.WindowSamples(context.Background(), WindowQuery{
		TenantID:     "01990000-3000-7000-8000-000000000001",
		SiteID:       "01990000-5000-7000-8000-000000000001",
		LoopDeviceID: "01990000-6100-7000-8000-000000000001",
		From:         now.Add(-time.Hour),
		To:           now,
	}); err != nil {
		t.Fatalf("WindowSamples failed: %v", err)
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
	if !strings.Contains(metricsRec.Body.String(), `hvac_service_up{service="fdd-service"} 1`) {
		t.Fatalf("GET /metrics missing hvac_service_up gauge:\n%s", metricsRec.Body.String())
	}

	var chErr atomic.Value
	rt := observability.NewRuntime(observability.RuntimeConfig{Service: "fdd-service"})
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

	chErr.Store(errors.New("clickhouse down"))
	failRec := httptest.NewRecorder()
	diag.ServeHTTP(failRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health/ready degraded = %d, want 503", failRec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(failRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid readiness JSON: %v", err)
	}

	diagMetrics := httptest.NewRecorder()
	diag.ServeHTTP(diagMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(diagMetrics.Body.String(), `hvac_service_up{service="fdd-service"} 1`) {
		t.Fatalf("runtime /metrics missing hvac_service_up gauge:\n%s", diagMetrics.Body.String())
	}
}

type fakeFDDQueue struct {
	jobs          []schedulerjob.Job
	failCalls     int
	lastFailRetry bool
	lastFailCtx   error
}

func (q *fakeFDDQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]schedulerjob.Job, error) {
	return q.jobs, nil
}
func (q *fakeFDDQueue) Start(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
	return true, nil
}
func (q *fakeFDDQueue) Complete(context.Context, schedulerjob.Job, any, time.Time) error {
	return nil
}
func (q *fakeFDDQueue) Fail(ctx context.Context, _ schedulerjob.Job, _ string, _ error, retryable bool, _ time.Time) error {
	q.failCalls++
	q.lastFailRetry = retryable
	q.lastFailCtx = ctx.Err()
	return nil
}

type fakeFDDEvaluator struct {
	fn func(context.Context, EvaluationRequest) (EvaluationResult, error)
}

func (e fakeFDDEvaluator) EvaluateLowDeltaT(ctx context.Context, input EvaluationRequest) (EvaluationResult, error) {
	return e.fn(ctx, input)
}

func TestRunScheduledEvaluationsWithLoggerSIGTERMMarksRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	payload, _ := json.Marshal(ScheduledEvaluation{
		AssetID:  "01990000-6000-7000-8000-000000000001",
		DeviceID: "01990000-6100-7000-8000-000000000001",
	})
	queue := &fakeFDDQueue{
		jobs: []schedulerjob.Job{{
			JobID:          "01990000-1111-7000-8000-000000000001",
			JobType:        EvaluationJobType,
			TriggerType:    "SCHEDULE",
			TenantID:       "01990000-3000-7000-8000-000000000001",
			SiteID:         "01990000-5000-7000-8000-000000000001",
			ScheduledFor:   time.Now().UTC(),
			Payload:        payload,
			TimeoutSeconds: 30,
		}},
	}
	eval := fakeFDDEvaluator{fn: func(jobCtx context.Context, _ EvaluationRequest) (EvaluationResult, error) {
		cancel()
		<-jobCtx.Done()
		return EvaluationResult{}, jobCtx.Err()
	}}
	var logBuf bytes.Buffer
	logger := observability.NewJSONLogger(&logBuf, slog.LevelInfo)

	if err := RunScheduledEvaluationsWithLogger(ctx, queue, eval, "w1", 8, logger); err != nil {
		t.Fatalf("RunScheduledEvaluationsWithLogger returned error: %v", err)
	}
	if queue.failCalls != 1 || !queue.lastFailRetry || queue.lastFailCtx != nil {
		t.Fatalf("unexpected fail state: calls=%d retry=%t ctxErr=%v", queue.failCalls, queue.lastFailRetry, queue.lastFailCtx)
	}
}
