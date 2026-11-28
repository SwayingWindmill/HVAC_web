package forecast

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

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestParsePostgresPoolConfigAndNilSafety(t *testing.T) {
	cfg, err := ParsePostgresPoolConfig("postgres://forecast:secret@127.0.0.1:5432/hvac?sslmode=disable")
	if err != nil {
		t.Fatalf("ParsePostgresPoolConfig returned unexpected error: %v", err)
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
		t.Fatal("nil PostgresStore.Ping should return an error")
	}
}

func TestClickHouseHTTPPoolDrainAndPing(t *testing.T) {
	var newConnections atomic.Int32
	var seenQueries []string
	var writerUser, readerUser string

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		if r.Method == http.MethodPost {
			writerUser = user
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("ok ", 1024)))
			return
		}
		readerUser = user
		seenQueries = append(seenQueries, r.URL.Query().Get("query"))
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

	sink, err := NewClickHouseSink(ClickHouseConfig{
		BaseURL:        server.URL,
		Database:       "analytics",
		Table:          "forecast_series",
		Username:       "forecast_service_writer",
		Password:       "pw",
		ReaderUsername: "forecast_service_reader",
		ReaderPassword: "rpw",
	})
	if err != nil {
		t.Fatalf("NewClickHouseSink returned error: %v", err)
	}
	defer sink.CloseIdleConnections()

	transport, ok := sink.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", sink.client.Transport)
	}
	if transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 32 || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("unexpected transport pool limits: MaxIdleConns=%d MaxIdleConnsPerHost=%d IdleConnTimeout=%s", transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.IdleConnTimeout)
	}
	if sink.client.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %s, want 10s", sink.client.Timeout)
	}

	now := time.Now().UTC()
	point := Point{
		ForecastID:         "f-1",
		TenantID:           "t-1",
		SiteID:             "s-1",
		ForecastJobID:      "job-1",
		ForecastSnapshotID: "snap-1",
		ForecastOrigin:     now,
		ForecastFor:        now.Add(15 * time.Minute),
		HorizonMinutes:     15,
		Value:              100.5,
		Quality:            "VALID",
		GeneratedAt:        now,
	}
	if err := sink.InsertForecastPoints(context.Background(), []Point{point}); err != nil {
		t.Fatalf("first InsertForecastPoints failed: %v", err)
	}
	if err := sink.InsertForecastPoints(context.Background(), []Point{point}); err != nil {
		t.Fatalf("second InsertForecastPoints failed: %v", err)
	}
	if err := sink.Ping(context.Background()); err != nil {
		t.Fatalf("sink.Ping failed: %v", err)
	}
	if writerUser != "forecast_service_writer" {
		t.Fatalf("insert basic auth user = %q, want forecast_service_writer", writerUser)
	}
	if readerUser != "forecast_service_reader" {
		t.Fatalf("ping basic auth user = %q, want forecast_service_reader", readerUser)
	}
	if got := newConnections.Load(); got != 1 {
		t.Fatalf("new TCP connections = %d, want 1 (connection reuse requires draining 2xx response bodies)", got)
	}
	if len(seenQueries) == 0 || seenQueries[len(seenQueries)-1] != "SELECT 1" {
		t.Fatalf("expected SELECT 1 ping query, got %v", seenQueries)
	}

	history, err := NewClickHouseHistoryReader(HistoryConfig{
		BaseURL:  server.URL,
		Username: "forecast_service_reader",
		Password: "rpw",
	})
	if err != nil {
		t.Fatalf("NewClickHouseHistoryReader returned error: %v", err)
	}
	defer history.CloseIdleConnections()
	if err := history.Ping(context.Background()); err != nil {
		t.Fatalf("history.Ping failed: %v", err)
	}
}

func TestDiagnosticsHandlerHealthzMetricsAndReadiness(t *testing.T) {
	var pgErr atomic.Value
	var chErr atomic.Value
	rt := observability.NewRuntime(observability.RuntimeConfig{Service: "forecast-service"})
	rt.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: func(context.Context) error {
			if v := pgErr.Load(); v != nil {
				return v.(error)
			}
			return nil
		}},
		observability.Dependency{Name: "clickhouse", Required: true, Check: func(context.Context) error {
			if v := chErr.Load(); v != nil {
				return v.(error)
			}
			return nil
		}},
	)
	rt.MarkReady()
	diag := rt.DiagnosticsHandler()

	rec := httptest.NewRecorder()
	diag.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", rec.Code)
	}

	readyRec := httptest.NewRecorder()
	diag.ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyRec.Code != http.StatusOK {
		t.Fatalf("GET /health/ready healthy = %d, want 200", readyRec.Code)
	}

	chErr.Store(errors.New("clickhouse down"))
	failRec := httptest.NewRecorder()
	diag.ServeHTTP(failRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health/ready with failed dependency = %d, want 503", failRec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(failRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid readiness JSON: %v", err)
	}

	diagMetrics := httptest.NewRecorder()
	diag.ServeHTTP(diagMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(diagMetrics.Body.String(), `hvac_service_up{service="forecast-service"} 1`) {
		t.Fatalf("runtime /metrics missing hvac_service_up gauge:\n%s", diagMetrics.Body.String())
	}
}
