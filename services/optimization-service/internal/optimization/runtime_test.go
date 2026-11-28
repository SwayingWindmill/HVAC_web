package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type stubOptimizationEnergySeries struct{}

func (stubOptimizationEnergySeries) Series(context.Context, analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	return analyticsmodel.EnergySeriesResponse{}, nil
}

func TestParsePostgresPoolConfigAndNilSafety(t *testing.T) {
	cfg, err := ParsePostgresPoolConfig("postgres://opt:secret@127.0.0.1:5432/hvac?sslmode=disable")
	if err != nil {
		t.Fatalf("ParsePostgresPoolConfig failed: %v", err)
	}
	if cfg.MaxConns != 16 {
		t.Fatalf("MaxConns = %d, want 16", cfg.MaxConns)
	}
	if cfg.MinConns != 1 {
		t.Fatalf("MinConns = %d, want 1", cfg.MinConns)
	}
	if cfg.MaxConnLifetime != 30*time.Minute {
		t.Fatalf("MaxConnLifetime = %s, want 30m", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != 5*time.Minute {
		t.Fatalf("MaxConnIdleTime = %s, want 5m", cfg.MaxConnIdleTime)
	}
	if cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("HealthCheckPeriod = %s, want 30s", cfg.HealthCheckPeriod)
	}

	var nilStore *PostgresStore
	nilStore.Close()
	if err := nilStore.Ping(context.Background()); err == nil {
		t.Fatal("nil PostgresStore.Ping should fail")
	}
}

func TestClickHouseHTTPPoolingDrainAndPing(t *testing.T) {
	var newConnections atomic.Int32
	var pingCount atomic.Int32

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("inserted ", 512)))
			return
		}
		if r.URL.Query().Get("query") == "SELECT 1" {
			pingCount.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("1\n"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	sink, err := NewClickHouseSink(server.URL, "optimization_service_writer", "pw", nil)
	if err != nil {
		t.Fatalf("NewClickHouseSink failed: %v", err)
	}
	defer sink.CloseIdleConnections()

	transport, ok := sink.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", sink.client.Transport)
	}
	if transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 32 || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("unexpected transport pool config: %+v", transport)
	}

	eval := Evaluation{
		EvaluationID:      "01990000-1111-7000-8000-000000000001",
		TenantID:          "01990000-3000-7000-8000-000000000001",
		SiteID:            "01990000-5000-7000-8000-000000000001",
		OptimizationRunID: "01990000-1111-7000-8000-000000000002",
		GeneratedAt:       time.Now().UTC(),
	}
	if err := sink.InsertEvaluation(context.Background(), eval); err != nil {
		t.Fatalf("first InsertEvaluation failed: %v", err)
	}
	if err := sink.InsertEvaluation(context.Background(), eval); err != nil {
		t.Fatalf("second InsertEvaluation failed: %v", err)
	}
	if err := sink.Ping(context.Background()); err != nil {
		t.Fatalf("sink.Ping failed: %v", err)
	}
	if got := newConnections.Load(); got != 1 {
		t.Fatalf("new TCP connections = %d, want 1 (2xx body drain enables keep-alive reuse)", got)
	}

	reader, err := NewClickHouseInputReader(InputReaderConfig{
		BaseURL:  server.URL,
		Username: "optimization_service_reader",
		Password: "rpw",
		Energy:   stubOptimizationEnergySeries{},
	})
	if err != nil {
		t.Fatalf("NewClickHouseInputReader failed: %v", err)
	}
	defer reader.CloseIdleConnections()
	if err := reader.Ping(context.Background()); err != nil {
		t.Fatalf("reader.Ping failed: %v", err)
	}
	if pingCount.Load() != 2 {
		t.Fatalf("pingCount = %d, want 2", pingCount.Load())
	}
}

func TestDiagnosticsHandlerHealthzMetricsAndReadiness(t *testing.T) {
	var chErr atomic.Value
	rt := observability.NewRuntime(observability.RuntimeConfig{Service: "optimization-service"})
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
	diag := rt.DiagnosticsHandler()

	healthRec := httptest.NewRecorder()
	diag.ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthRec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", healthRec.Code)
	}

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
	if !strings.Contains(diagMetrics.Body.String(), `hvac_service_up{service="optimization-service"} 1`) {
		t.Fatalf("runtime /metrics missing hvac_service_up gauge:\n%s", diagMetrics.Body.String())
	}
}
