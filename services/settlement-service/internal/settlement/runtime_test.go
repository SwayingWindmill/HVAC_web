package settlement

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePostgresPoolConfigAndNilSafety(t *testing.T) {
	cfg, err := ParsePostgresPoolConfig("postgres://settlement:secret@127.0.0.1:5432/hvac?sslmode=disable")
	if err != nil {
		t.Fatalf("ParsePostgresPoolConfig failed: %v", err)
	}
	if cfg.MaxConns != 4 || cfg.MinConns != 1 || cfg.MaxConnLifetime != 30*time.Minute || cfg.MaxConnIdleTime != 5*time.Minute || cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("unexpected pgxpool config: MaxConns=%d MinConns=%d MaxConnLifetime=%s MaxConnIdleTime=%s HealthCheckPeriod=%s",
			cfg.MaxConns, cfg.MinConns, cfg.MaxConnLifetime, cfg.MaxConnIdleTime, cfg.HealthCheckPeriod)
	}
	var nilStore *PostgresStore
	nilStore.Close()
	if err := nilStore.Ping(context.Background()); err == nil {
		t.Fatal("nil PostgresStore.Ping should return error")
	}
}

func TestClickHouseStorePoolingDrainAndPing(t *testing.T) {
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

	store, err := NewClickHouseStore(server.URL, "settlement_reader", "secret", nil)
	if err != nil {
		t.Fatalf("NewClickHouseStore failed: %v", err)
	}
	defer store.CloseIdleConnections()

	transport, ok := store.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", store.client.Transport)
	}
	if store.client.Timeout != 15*time.Second {
		t.Fatalf("Timeout = %s, want 15s", store.client.Timeout)
	}
	if transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 32 || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("unexpected transport pool config: %+v", transport)
	}

	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("first Ping failed: %v", err)
	}
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("second Ping failed: %v", err)
	}
	now := time.Now().UTC()
	facts, err := store.ReadMetricFacts(context.Background(), Period{
		TenantID: "01990000-3000-7000-8000-000000000001",
		SiteID:   "01990000-5000-7000-8000-000000000001",
		Start:    now.Add(-time.Hour),
		End:      now,
	}, []MetricBinding{{MetricBindingID: "01990000-7000-7000-8000-000000000001", MetricCode: "energy"}})
	if err != nil || len(facts) != 0 {
		t.Fatalf("ReadMetricFacts returned facts=%d err=%v", len(facts), err)
	}
	if seenPing.Load() != 2 {
		t.Fatalf("seenPing = %d, want 2", seenPing.Load())
	}
	if got := newConnections.Load(); got != 1 {
		t.Fatalf("new TCP connections = %d, want 1", got)
	}

	// Ensure oversized ClickHouse responses fail explicitly rather than truncating.
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", int(maximumMetricFactsResponseBytes)+64)))
	}))
	defer oversized.Close()
	badStore, err := NewClickHouseStore(oversized.URL, "settlement_reader", "secret", nil)
	if err != nil {
		t.Fatalf("NewClickHouseStore failed: %v", err)
	}
	defer badStore.CloseIdleConnections()
	if _, err := badStore.ReadMetricFacts(context.Background(), Period{
		TenantID: "01990000-3000-7000-8000-000000000001",
		SiteID:   "01990000-5000-7000-8000-000000000001",
		Start:    now.Add(-time.Hour),
		End:      now,
	}, []MetricBinding{{MetricBindingID: "01990000-7000-7000-8000-000000000001", MetricCode: "energy"}}); err == nil {
		t.Fatal("expected oversized response error")
	}
}
