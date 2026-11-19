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
	completeCalls int
	lastOutput    any
	failCalls     int
	lastFailCode  string
	lastFailRetry bool
	lastFailCtx   error
}

func (q *fakeFDDQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]schedulerjob.Job, error) {
	return q.jobs, nil
}
func (q *fakeFDDQueue) Start(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
	return true, nil
}
func (q *fakeFDDQueue) Complete(_ context.Context, _ schedulerjob.Job, output any, _ time.Time) error {
	q.completeCalls++
	q.lastOutput = output
	return nil
}
func (q *fakeFDDQueue) Fail(ctx context.Context, _ schedulerjob.Job, code string, _ error, retryable bool, _ time.Time) error {
	q.failCalls++
	q.lastFailCode = code
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

type fakeFullFDDEvaluator struct {
	fakeFDDEvaluator
	imbalanceFn func(context.Context, MeterImbalanceRequest) (MeterImbalanceResult, error)
}

func (e fakeFullFDDEvaluator) EvaluateMeterImbalance(ctx context.Context, input MeterImbalanceRequest) (MeterImbalanceResult, error) {
	return e.imbalanceFn(ctx, input)
}

func TestRunScheduledEvaluationsWithLoggerRuleDispatch(t *testing.T) {
	now := time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)
	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"
	jobBase := schedulerjob.Job{
		JobID:          "01990000-1111-7000-8000-000000000002",
		JobType:        EvaluationJobType,
		TriggerType:    "SCHEDULE",
		TenantID:       tenantID,
		SiteID:         siteID,
		ScheduledFor:   now,
		TimeoutSeconds: 30,
	}

	imbalanceJob := jobBase
	imbalanceJob.Payload = []byte(`{"ruleCode":"METER_IMBALANCE"}`)

	// Existing lowDeltaTEvaluator mocks continue compiling and fail unsupported METER_IMBALANCE non-retryably.
	lowOnlyQueue := &fakeFDDQueue{jobs: []schedulerjob.Job{imbalanceJob}}
	if err := RunScheduledEvaluationsWithLogger(context.Background(), lowOnlyQueue, fakeFDDEvaluator{}, "w1", 8, slog.Default()); err != nil {
		t.Fatalf("lowOnly failed: %v", err)
	}
	if lowOnlyQueue.failCalls != 1 || lowOnlyQueue.lastFailCode != "FDD_EVALUATION_UNSUPPORTED" || lowOnlyQueue.lastFailRetry {
		t.Fatalf("unexpected lowOnly fail state: %+v", lowOnlyQueue)
	}

	var gotImbalanceReq MeterImbalanceRequest
	var gotLowReq EvaluationRequest
	lowJob := jobBase
	lowJob.Payload = []byte(`{"ruleCode":"LOW_DELTA_T","assetId":"01990000-6000-7000-8000-000000000001","deviceId":"01990000-6100-7000-8000-000000000001"}`)

	fullEval := fakeFullFDDEvaluator{
		fakeFDDEvaluator: fakeFDDEvaluator{fn: func(_ context.Context, req EvaluationRequest) (EvaluationResult, error) {
			gotLowReq = req
			return EvaluationResult{Status: StatusClear}, nil
		}},
		imbalanceFn: func(_ context.Context, req MeterImbalanceRequest) (MeterImbalanceResult, error) {
			gotImbalanceReq = req
			return MeterImbalanceResult{Status: StatusClear, ImbalanceStatus: "BALANCED", ThresholdRatio: 0.05}, nil
		},
	}

	queue := &fakeFDDQueue{jobs: []schedulerjob.Job{imbalanceJob, lowJob}}
	if err := RunScheduledEvaluationsWithLogger(context.Background(), queue, fullEval, "w1", 8, slog.Default()); err != nil {
		t.Fatalf("RunScheduledEvaluationsWithLogger failed: %v", err)
	}
	if queue.completeCalls != 2 || queue.failCalls != 0 {
		t.Fatalf("complete=%d fail=%d", queue.completeCalls, queue.failCalls)
	}
	if !gotImbalanceReq.From.Equal(now.Add(-24*time.Hour)) || !gotImbalanceReq.To.Equal(now) {
		t.Fatalf("imbalance window=%#v", gotImbalanceReq)
	}
	if gotLowReq.AssetID != "01990000-6000-7000-8000-000000000001" || !gotLowReq.At.Equal(now) {
		t.Fatalf("lowDeltaT request=%#v", gotLowReq)
	}
}

type fakeMeterBalanceSiteLister struct {
	sites []SiteRuleScope
	err   error
	from  time.Time
	to    time.Time
}

func (f *fakeMeterBalanceSiteLister) ListRecentMeterBalanceSites(_ context.Context, from, to time.Time) ([]SiteRuleScope, error) {
	f.from = from
	f.to = to
	if f.err != nil {
		return nil, f.err
	}
	return f.sites, nil
}

func TestRunMeterImbalanceSweepContinuesPastPerSiteFailure(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	site1 := SiteRuleScope{TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001"}
	site2 := SiteRuleScope{TenantID: "01990000-3000-7000-8000-000000000002", SiteID: "01990000-5000-7000-8000-000000000002"}
	lister := &fakeMeterBalanceSiteLister{sites: []SiteRuleScope{site1, site2}}

	var evaluated []SiteRuleScope
	eval := fakeFullFDDEvaluator{
		imbalanceFn: func(_ context.Context, req MeterImbalanceRequest) (MeterImbalanceResult, error) {
			evaluated = append(evaluated, SiteRuleScope{TenantID: req.TenantID, SiteID: req.SiteID})
			if !req.From.Equal(now.Add(-24*time.Hour)) || !req.To.Equal(now) {
				t.Fatalf("unexpected sweep window [%s, %s)", req.From, req.To)
			}
			if req.SiteID == site1.SiteID {
				return MeterImbalanceResult{}, errors.New("temporary clickhouse error")
			}
			return MeterImbalanceResult{Status: StatusClear}, nil
		},
	}

	var logBuf bytes.Buffer
	logger := observability.NewJSONLogger(&logBuf, slog.LevelInfo)
	if err := RunMeterImbalanceSweep(context.Background(), lister, eval, now, logger); err != nil {
		t.Fatalf("RunMeterImbalanceSweep failed: %v", err)
	}
	if len(evaluated) != 2 || evaluated[0] != site1 || evaluated[1] != site2 {
		t.Fatalf("evaluated=%#v want [%#v %#v]", evaluated, site1, site2)
	}
	if !strings.Contains(logBuf.String(), "FDD_METER_IMBALANCE_SITE_FAILED") {
		t.Fatalf("expected warning log for failing site, got:\n%s", logBuf.String())
	}

	listerErr := &fakeMeterBalanceSiteLister{err: errors.New("clickhouse unreachable")}
	if err := RunMeterImbalanceSweep(context.Background(), listerErr, eval, now, logger); err == nil {
		t.Fatal("expected list error to be returned")
	}
}
