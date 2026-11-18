package telemetry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedClickHouseRetentionCall struct {
	SQL    string
	Params url.Values
}

func TestClickHouseRetentionPrunerTieredDefaultsAndLegalHolds(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var mutex sync.Mutex
	var calls []recordedClickHouseRetentionCall
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		username, password, ok := request.BasicAuth()
		if !ok || username != "telemetry_retention_writer" || password != "[REDACTED_SECRET]" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(request.Body)
		mutex.Lock()
		calls = append(calls, recordedClickHouseRetentionCall{
			SQL:    string(body),
			Params: request.URL.Query(),
		})
		mutex.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	holdTenant := "018f2d00-0000-7000-8000-000000000001"
	holdSiteTenant := "018f2d00-0000-7000-8000-000000000002"
	holdSite := "018f2e00-1000-7000-8000-000000000009"
	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:    server.URL,
		Username:   "telemetry_retention_writer",
		Password:   "[REDACTED_SECRET]",
		HTTPClient: server.Client(),
		LegalHolds: []ClickHouseLegalHoldScope{
			{TenantID: holdTenant},
			{TenantID: holdSiteTenant, SiteID: holdSite},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := pruner.Prune(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SubmittedTables) != 8 {
		t.Fatalf("submitted tables=%v", result.SubmittedTables)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(calls) != 8 {
		t.Fatalf("call count=%d", len(calls))
	}

	expected := []struct {
		sqlPrefix     string
		expectedCut   string
		nullableScope bool
	}{
		{
			sqlPrefix:     "ALTER TABLE telemetry_history.observations DELETE WHERE sampled_at < {cutoff:DateTime64(3, 'UTC')}",
			expectedCut:   now.Add(-180 * 24 * time.Hour).Format("2006-01-02 15:04:05.000"),
			nullableScope: true,
		},
		{
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_1min_states DELETE WHERE bucket < {cutoff:DateTime('UTC')}",
			expectedCut: now.Add(-365 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
		},
		{
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_15min_states DELETE WHERE bucket < {cutoff:DateTime('UTC')}",
			expectedCut: now.Add(-1825 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
		},
		{
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_gauge_1min_states DELETE WHERE bucket < {cutoff:DateTime('UTC')}",
			expectedCut: now.Add(-365 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
		},
		{
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_gauge_15min_states DELETE WHERE bucket < {cutoff:DateTime('UTC')}",
			expectedCut: now.Add(-1825 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
		},
		{
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_hourly_states DELETE WHERE hour < {cutoff:DateTime('UTC')}",
			expectedCut: now.Add(-1825 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
		},
		{
			sqlPrefix:   "ALTER TABLE analytics.energy_interval_facts DELETE WHERE period_end < {cutoff:DateTime64(3, 'UTC')}",
			expectedCut: now.Add(-1825 * 24 * time.Hour).Format("2006-01-02 15:04:05.000"),
		},
		{
			sqlPrefix:   "ALTER TABLE analytics.energy_hourly_states DELETE WHERE hour < {cutoff:DateTime64(3, 'UTC')}",
			expectedCut: now.Add(-1825 * 24 * time.Hour).Format("2006-01-02 15:04:05.000"),
		},
	}

	for index, exp := range expected {
		call := calls[index]
		if !strings.HasPrefix(call.SQL, exp.sqlPrefix) {
			t.Fatalf("call %d sql=%q missing prefix %q", index, call.SQL, exp.sqlPrefix)
		}
		if strings.Contains(call.SQL, holdTenant) || strings.Contains(call.SQL, holdSite) || strings.Contains(call.SQL, exp.expectedCut) {
			t.Fatalf("call %d interpolated literal values into SQL: %q", index, call.SQL)
		}
		if exp.nullableScope && !strings.Contains(call.SQL, "tenant_id IS NULL") {
			t.Fatalf("observations delete must preserve null tenant_id rows outside legal hold: %q", call.SQL)
		}
		if call.Params.Get("param_cutoff") != exp.expectedCut {
			t.Fatalf("call %d cutoff=%q want %q", index, call.Params.Get("param_cutoff"), exp.expectedCut)
		}
		if call.Params.Get("param_hold_tenant_0") != holdTenant || call.Params.Get("param_hold_tenant_1") != holdSiteTenant || call.Params.Get("param_hold_site_1") != holdSite {
			t.Fatalf("call %d params=%v", index, call.Params)
		}
		if call.Params.Get("wait_end_of_query") != "1" {
			t.Fatalf("call %d missing wait_end_of_query: %v", index, call.Params)
		}
	}
}

func TestClickHouseRetentionPrunerRejectsErrorsAndHTTPFailures(t *testing.T) {
	if _, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{BaseURL: "http://user@localhost:8123"}); err == nil {
		t.Fatal("accepted URL with userinfo")
	}
	if _, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{BaseURL: "http://localhost:8123", ObservationsRetention: -time.Hour}); err == nil {
		t.Fatal("accepted negative retention")
	}
	if _, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:    "http://localhost:8123",
		LegalHolds: []ClickHouseLegalHoldScope{{TenantID: "not-a-uuid"}},
	}); err == nil {
		t.Fatal("accepted invalid legal hold tenant UUID")
	}
	if _, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:    "http://localhost:8123",
		LegalHolds: []ClickHouseLegalHoldScope{{TenantID: "018f2d00-0000-7000-8000-000000000001", SiteID: "bad"}},
	}); err == nil {
		t.Fatal("accepted invalid legal hold site UUID")
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "Code: 497. Not enough privileges", http.StatusForbidden)
	}))
	defer server.Close()

	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pruner.Prune(t.Context(), time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected HTTP 403 error, got %v", err)
	}
}
