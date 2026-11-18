package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
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
		sql := string(body)
		switch {
		case strings.HasPrefix(sql, "SELECT count() FROM system.mutations"):
			_, _ = io.WriteString(writer, "0\n")
			return
		case strings.HasPrefix(sql, "SELECT 1 FROM "):
			_, _ = io.WriteString(writer, "1\n")
			return
		}
		mutex.Lock()
		calls = append(calls, recordedClickHouseRetentionCall{
			SQL:    sql,
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
	if len(result.SubmittedTables) != 5 {
		t.Fatalf("submitted tables=%v", result.SubmittedTables)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(calls) != 5 {
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
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_gauge_1min_states DELETE WHERE bucket < {cutoff:DateTime('UTC')}",
			expectedCut: now.Add(-365 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
		},
		{
			sqlPrefix:   "ALTER TABLE telemetry_history.numeric_gauge_15min_states DELETE WHERE bucket < {cutoff:DateTime('UTC')}",
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

type stubColdArchiveRunner struct {
	mu       sync.Mutex
	requests []ColdArchiveExportRequest
	failOn   string
	err      error
}

func (s *stubColdArchiveRunner) ExportBeforePrune(_ context.Context, req ColdArchiveExportRequest) (ColdArchiveExportResult, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if s.failOn != "" && req.Database+"."+req.Table == s.failOn {
		return ColdArchiveExportResult{}, s.err
	}
	return ColdArchiveExportResult{
		Manifest: ColdArchiveManifest{
			SchemaVersion: 1,
			Database:      req.Database,
			Table:         req.Table,
			CutoffUTC:     req.Cutoff,
		},
	}, nil
}

func TestClickHouseRetentionPrunerArchiveRequiredFailClosed(t *testing.T) {
	var deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		deleteCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:         server.URL,
		HTTPClient:      server.Client(),
		ArchiveRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pruner.Prune(t.Context(), time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "archive exporter is required") {
		t.Fatalf("expected fail-closed archive required error, got %v", err)
	}
	if deleteCalls.Load() != 0 {
		t.Fatalf("expected 0 delete calls when archive exporter missing, got %d", deleteCalls.Load())
	}
}

func TestClickHouseRetentionPrunerArchiveErrorAbortsBeforeAnyDelete(t *testing.T) {
	var deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		deleteCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner := &stubColdArchiveRunner{
		failOn: "analytics.energy_interval_facts",
		err:    errors.New("disk full"),
	}
	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:         server.URL,
		HTTPClient:      server.Client(),
		ArchiveExporter: runner,
		ArchiveRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pruner.Prune(t.Context(), time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected archive error, got %v", err)
	}
	if deleteCalls.Load() != 0 {
		t.Fatalf("expected 0 ALTER TABLE DELETE calls when second archive fails, got %d", deleteCalls.Load())
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.requests) != 2 {
		t.Fatalf("expected export attempted for both raw tables before abort, got %d", len(runner.requests))
	}
}

func TestClickHouseRetentionPrunerExportsArchiveBeforeDeletesAndPropagatesLegalHolds(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	holdTenant := "018f2d00-0000-7000-8000-000000000001"
	holdSiteTenant := "018f2d00-0000-7000-8000-000000000002"
	holdSite := "018f2e00-1000-7000-8000-000000000009"

	var mu sync.Mutex
	var ops []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(sql, "SELECT count() FROM system.mutations"):
			_, _ = io.WriteString(writer, "0\n")
			return
		case strings.HasPrefix(sql, "SELECT 1 FROM "):
			_, _ = io.WriteString(writer, "1\n")
			return
		case strings.HasPrefix(sql, "SELECT "):
			if strings.Contains(sql, "FROM telemetry_history.observations") {
				ops = append(ops, "EXPORT:telemetry_history.observations")
				_, _ = io.WriteString(writer, `{"observation_id":"018f2e00-9100-7000-8000-000000000010"}`+"\n")
				return
			}
			if strings.Contains(sql, "FROM analytics.energy_interval_facts") {
				ops = append(ops, "EXPORT:analytics.energy_interval_facts")
				_, _ = io.WriteString(writer, `{"fact_id":"018f2e00-9200-7000-8000-000000000010"}`+"\n")
				return
			}
		case strings.HasPrefix(sql, "ALTER TABLE "):
			table := strings.TrimPrefix(sql, "ALTER TABLE ")
			if idx := strings.Index(table, " "); idx > 0 {
				table = table[:idx]
			}
			ops = append(ops, "DELETE:"+table)
			writer.WriteHeader(http.StatusOK)
			return
		}
		http.Error(writer, "unexpected sql", http.StatusBadRequest)
	}))
	defer server.Close()

	archiveDir := t.TempDir()
	exporter, err := NewColdArchiveExporter(ColdArchiveConfig{
		BaseURL:    server.URL,
		ArchiveDir: archiveDir,
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:         server.URL,
		HTTPClient:      server.Client(),
		ArchiveExporter: exporter,
		ArchiveRequired: true,
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
		t.Fatalf("Prune failed: %v", err)
	}
	wantArchived := []string{"telemetry_history.observations", "analytics.energy_interval_facts"}
	if !slices.Equal(result.ArchivedTables, wantArchived) {
		t.Fatalf("ArchivedTables=%v want %v", result.ArchivedTables, wantArchived)
	}
	if len(result.SubmittedTables) != 5 {
		t.Fatalf("SubmittedTables=%v want 5 tables", result.SubmittedTables)
	}

	mu.Lock()
	gotOps := slices.Clone(ops)
	mu.Unlock()
	if len(gotOps) != 7 {
		t.Fatalf("expected 2 exports + 5 deletes = 7 ops, got %v", gotOps)
	}
	if gotOps[0] != "EXPORT:telemetry_history.observations" || gotOps[1] != "EXPORT:analytics.energy_interval_facts" {
		t.Fatalf("exports must execute before all deletes, got %v", gotOps)
	}
	for _, op := range gotOps[2:] {
		if !strings.HasPrefix(op, "DELETE:") {
			t.Fatalf("unexpected operation order: %v", gotOps)
		}
	}

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 2 .jsonl.gz + 2 .manifest.json files, got %d", len(entries))
	}
}

func TestClickHouseRetentionPrunerRecordsColdArchiveRunMetrics(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	registry := observability.NewRegistry()
	metrics := NewPipelineMetrics(registry)

	failPruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:         "http://127.0.0.1:8123",
		HTTPClient:      &http.Client{},
		ArchiveRequired: true,
		Metrics:         metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failPruner.Prune(t.Context(), now); err == nil {
		t.Fatal("expected missing exporter failure")
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		if strings.HasPrefix(sql, "SELECT count() FROM system.mutations") {
			_, _ = io.WriteString(writer, "0\n")
			return
		}
		if strings.HasPrefix(sql, "SELECT ") {
			_, _ = io.WriteString(writer, "")
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter, err := NewColdArchiveExporter(ColdArchiveConfig{
		BaseURL:    server.URL,
		ArchiveDir: t.TempDir(),
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	okPruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:         server.URL,
		HTTPClient:      server.Client(),
		ArchiveExporter: exporter,
		ArchiveRequired: true,
		Metrics:         metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := okPruner.Prune(t.Context(), now); err != nil {
		t.Fatalf("okPruner.Prune failed: %v", err)
	}

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, needle := range []string{
		`hvac_s2_cold_archive_runs_total{outcome="failed"} 1`,
		`hvac_s2_cold_archive_runs_total{outcome="succeeded"} 2`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("missing %q in /metrics output:\n%s", needle, body)
		}
	}
}

func TestClickHouseRetentionPrunerDropsWholeExpiredPartitionsWithoutLegalHolds(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var dropped []string
	var deletes []string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		params := request.URL.Query()
		switch {
		case strings.Contains(sql, "FROM system.parts"):
			if params.Get("param_db") == "telemetry_history" && params.Get("param_table") == "observations" {
				_, _ = io.WriteString(writer, "202604\n202605\n202606\n")
				return
			}
			writer.WriteHeader(http.StatusOK)
			return
		case strings.Contains(sql, "FROM system.mutations"):
			_, _ = io.WriteString(writer, "0\n")
			return
		case strings.HasPrefix(sql, "SELECT 1 FROM "):
			if strings.Contains(sql, "telemetry_history.observations") {
				_, _ = io.WriteString(writer, "1\n")
				return
			}
			writer.WriteHeader(http.StatusOK)
			return
		case strings.Contains(sql, "DROP PARTITION ID"):
			mu.Lock()
			dropped = append(dropped, sql)
			mu.Unlock()
			writer.WriteHeader(http.StatusOK)
			return
		case strings.Contains(sql, "DELETE WHERE"):
			mu.Lock()
			deletes = append(deletes, sql)
			mu.Unlock()
			writer.WriteHeader(http.StatusOK)
			return
		default:
			http.Error(writer, "unexpected sql", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := pruner.Prune(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	wantDropped := []string{"telemetry_history.observations:202604"}
	if !slices.Equal(result.DroppedPartitions, wantDropped) {
		t.Fatalf("DroppedPartitions=%v want %v", result.DroppedPartitions, wantDropped)
	}
	if len(dropped) != 1 || dropped[0] != "ALTER TABLE telemetry_history.observations DROP PARTITION ID '202604'" {
		t.Fatalf("dropped=%v", dropped)
	}
	if len(deletes) != 1 || !strings.HasPrefix(deletes[0], "ALTER TABLE telemetry_history.observations DELETE WHERE") {
		t.Fatalf("expected tail delete only on observations, got %v", deletes)
	}
}

func TestClickHouseRetentionPrunerGovernsMutationsExistenceAndCooldown(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var activeMutations atomic.Int64
	activeMutations.Store(1)
	var obsHasRows atomic.Bool
	obsHasRows.Store(false)

	var mu sync.Mutex
	var existsChecks int
	var deleteCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		params := request.URL.Query()
		switch {
		case strings.Contains(sql, "FROM system.parts"):
			writer.WriteHeader(http.StatusOK)
		case strings.Contains(sql, "FROM system.mutations"):
			if params.Get("param_table") == "observations" {
				_, _ = io.WriteString(writer, strconv.FormatInt(activeMutations.Load(), 10)+"\n")
				return
			}
			_, _ = io.WriteString(writer, "0\n")
		case strings.HasPrefix(sql, "SELECT 1 FROM "):
			mu.Lock()
			existsChecks++
			mu.Unlock()
			if strings.Contains(sql, "telemetry_history.observations") && obsHasRows.Load() {
				_, _ = io.WriteString(writer, "1\n")
			}
		case strings.Contains(sql, "DELETE WHERE"):
			mu.Lock()
			deleteCalls++
			mu.Unlock()
			writer.WriteHeader(http.StatusOK)
		default:
			http.Error(writer, "unexpected sql", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	pruner, err := NewClickHouseRetentionPruner(ClickHouseRetentionConfig{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pass 1: observations has active mutation (no existence check); other 4 tables have no rows (recorded cooldown).
	if _, err := pruner.Prune(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if existsChecks != 4 || deleteCalls != 0 {
		t.Fatalf("pass 1 existsChecks=%d deleteCalls=%d want 4, 0", existsChecks, deleteCalls)
	}

	// Pass 2 (+1h): active mutation clears and observations now has tail rows; cooldown still blocks other 4 tables.
	activeMutations.Store(0)
	obsHasRows.Store(true)
	res2, err := pruner.Prune(t.Context(), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res2.SubmittedTables, []string{"telemetry_history.observations"}) {
		t.Fatalf("pass 2 SubmittedTables=%v", res2.SubmittedTables)
	}
	if existsChecks != 5 || deleteCalls != 1 {
		t.Fatalf("pass 2 existsChecks=%d deleteCalls=%d want 5, 1", existsChecks, deleteCalls)
	}

	// Pass 3 (+12h): all 5 tables remain inside the 24h cooldown window.
	if _, err := pruner.Prune(t.Context(), now.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if existsChecks != 5 || deleteCalls != 1 {
		t.Fatalf("pass 3 existsChecks=%d deleteCalls=%d want 5, 1", existsChecks, deleteCalls)
	}

	// Pass 4 (+25h): cooldown elapsed for all tables.
	res4, err := pruner.Prune(t.Context(), now.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res4.SubmittedTables, []string{"telemetry_history.observations"}) || deleteCalls != 2 {
		t.Fatalf("pass 4 SubmittedTables=%v deleteCalls=%d", res4.SubmittedTables, deleteCalls)
	}
}
