package telemetry

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestColdArchiveExporterStreamsVerifiedArchiveAndManifest(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-180 * 24 * time.Hour)
	holdTenant := "018f2d00-0000-7000-8000-000000000001"
	holdSiteTenant := "018f2d00-0000-7000-8000-000000000002"
	holdSite := "018f2e00-1000-7000-8000-000000000009"

	var mu sync.Mutex
	var calls []recordedClickHouseRetentionCall

	obsLines := strings.Join([]string{
		`{"observation_id":"018f2e00-9100-7000-8000-000000000001","telemetry_key":"chw_temp","value_number":7.2}`,
		`{"observation_id":"018f2e00-9100-7000-8000-000000000002","telemetry_key":"cdw_temp","value_number":31.4}`,
	}, "\n") + "\n"
	factLines := `{"fact_id":"018f2e00-9200-7000-8000-000000000001","energy_kwh":12.5}` + "\n"

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "telemetry_retention_writer" || password != "[REDACTED_SECRET]" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		sqlBytes, _ := io.ReadAll(request.Body)
		sql := string(sqlBytes)
		mu.Lock()
		calls = append(calls, recordedClickHouseRetentionCall{SQL: sql, Params: request.URL.Query()})
		mu.Unlock()

		switch {
		case strings.Contains(sql, "FROM telemetry_history.observations"):
			_, _ = io.WriteString(writer, obsLines)
		case strings.Contains(sql, "FROM analytics.energy_interval_facts"):
			_, _ = io.WriteString(writer, factLines)
		default:
			http.Error(writer, "unexpected table", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	archiveDir := filepath.Join(t.TempDir(), "cold-archive")
	exporter, err := NewColdArchiveExporter(ColdArchiveConfig{
		BaseURL:    server.URL,
		Username:   "telemetry_retention_writer",
		Password:   "[REDACTED_SECRET]",
		ArchiveDir: archiveDir,
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	holds := []ClickHouseLegalHoldScope{
		{TenantID: holdTenant},
		{TenantID: holdSiteTenant, SiteID: holdSite},
	}

	obsRes, err := exporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database:   "telemetry_history",
		Table:      "observations",
		Cutoff:     cutoff,
		LegalHolds: holds,
	})
	if err != nil {
		t.Fatalf("export observations: %v", err)
	}
	if obsRes.Skipped {
		t.Fatal("expected initial observations export not skipped")
	}
	if obsRes.Manifest.SchemaVersion != 1 || obsRes.Manifest.RowCount != 2 ||
		obsRes.Manifest.Database != "telemetry_history" || obsRes.Manifest.Table != "observations" ||
		!obsRes.Manifest.CutoffUTC.Equal(cutoff) || !obsRes.Manifest.ExportedAtUTC.Equal(now) {
		t.Fatalf("unexpected observations manifest: %+v", obsRes.Manifest)
	}
	verifyArchiveFileMatchesManifest(t, obsRes.ArchivePath, obsRes.ManifestPath, obsLines)

	factRes, err := exporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database:   "analytics",
		Table:      "energy_interval_facts",
		Cutoff:     cutoff,
		LegalHolds: holds,
	})
	if err != nil {
		t.Fatalf("export energy_interval_facts: %v", err)
	}
	if factRes.Manifest.RowCount != 1 {
		t.Fatalf("expected 1 fact row, got %d", factRes.Manifest.RowCount)
	}
	verifyArchiveFileMatchesManifest(t, factRes.ArchivePath, factRes.ManifestPath, factLines)

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("unexpected leftover temp file %q", entry.Name())
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 ClickHouse queries, got %d", len(calls))
	}
	if !strings.Contains(calls[0].SQL, "sampled_at < {cutoff:DateTime64(3, 'UTC')}") ||
		!strings.Contains(calls[0].SQL, "tenant_id IS NULL") ||
		!strings.HasSuffix(calls[0].SQL, "FORMAT JSONEachRow") ||
		strings.Contains(calls[0].SQL, holdTenant) {
		t.Fatalf("unexpected observations export SQL: %q", calls[0].SQL)
	}
	if !strings.Contains(calls[1].SQL, "period_end < {cutoff:DateTime64(3, 'UTC')}") ||
		strings.Contains(calls[1].SQL, "tenant_id IS NULL") ||
		!strings.HasSuffix(calls[1].SQL, "FORMAT JSONEachRow") ||
		strings.Contains(calls[1].SQL, holdSite) {
		t.Fatalf("unexpected energy_interval_facts export SQL: %q", calls[1].SQL)
	}
	for idx, call := range calls {
		if call.Params.Get("param_cutoff") != cutoff.Format("2006-01-02 15:04:05.000") ||
			call.Params.Get("param_hold_tenant_0") != holdTenant ||
			call.Params.Get("param_hold_tenant_1") != holdSiteTenant ||
			call.Params.Get("param_hold_site_1") != holdSite {
			t.Fatalf("call %d params=%v", idx, call.Params)
		}
	}
}

func TestColdArchiveExporterIdempotentSkipAndCorruptRecovery(t *testing.T) {
	now := time.Date(2026, 11, 18, 13, 0, 0, 0, time.UTC)
	cutoff := now.Add(-180 * 24 * time.Hour)
	var queryCount int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		queryCount++
		mu.Unlock()
		_, _ = io.WriteString(writer, `{"observation_id":"018f2e00-9100-7000-8000-000000000003"}`+"\n")
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

	req := ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
		Cutoff:   cutoff,
	}
	first, err := exporter.ExportBeforePrune(t.Context(), req)
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	if first.Skipped {
		t.Fatal("first export should not be skipped")
	}

	second, err := exporter.ExportBeforePrune(t.Context(), req)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if !second.Skipped {
		t.Fatal("expected second export with valid manifest and archive to skip")
	}
	if second.Manifest != first.Manifest {
		t.Fatalf("skipped manifest mismatch: got %+v want %+v", second.Manifest, first.Manifest)
	}
	mu.Lock()
	if queryCount != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 HTTP call before corruption, got %d", queryCount)
	}
	mu.Unlock()

	if err := os.WriteFile(first.ArchivePath, []byte("corrupted-gzip-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	repaired, err := exporter.ExportBeforePrune(t.Context(), req)
	if err != nil {
		t.Fatalf("re-export after corruption: %v", err)
	}
	if repaired.Skipped {
		t.Fatal("expected corrupted archive to force re-export")
	}
	verifyArchiveFileMatchesManifest(t, repaired.ArchivePath, repaired.ManifestPath, `{"observation_id":"018f2e00-9100-7000-8000-000000000003"}`+"\n")

	if err := os.Remove(repaired.ArchivePath); err != nil {
		t.Fatal(err)
	}
	restored, err := exporter.ExportBeforePrune(t.Context(), req)
	if err != nil {
		t.Fatalf("re-export after missing archive: %v", err)
	}
	if restored.Skipped {
		t.Fatal("expected missing archive to force re-export")
	}
	mu.Lock()
	if queryCount != 3 {
		mu.Unlock()
		t.Fatalf("expected 3 HTTP calls total, got %d", queryCount)
	}
	mu.Unlock()
}

func TestColdArchiveExporterZeroRowsAndErrors(t *testing.T) {
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	cutoff := now.Add(-180 * 24 * time.Hour)

	mode := "empty"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		switch mode {
		case "empty":
			writer.WriteHeader(http.StatusOK)
		case "bad_json":
			_, _ = io.WriteString(writer, "{not-valid-json\n")
		case "http_error":
			http.Error(writer, "query denied", http.StatusForbidden)
		}
	}))
	defer server.Close()

	archiveDir := filepath.Join(t.TempDir(), "slices")
	exporter, err := NewColdArchiveExporter(ColdArchiveConfig{
		BaseURL:    server.URL,
		ArchiveDir: archiveDir,
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	emptyRes, err := exporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
		Cutoff:   cutoff,
	})
	if err != nil {
		t.Fatalf("zero-row export failed: %v", err)
	}
	if emptyRes.Manifest.RowCount != 0 || emptyRes.Manifest.GzipByteSize <= 0 {
		t.Fatalf("unexpected zero-row manifest: %+v", emptyRes.Manifest)
	}
	verifyArchiveFileMatchesManifest(t, emptyRes.ArchivePath, emptyRes.ManifestPath, "")

	skippedRes, err := exporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
		Cutoff:   cutoff,
	})
	if err != nil || !skippedRes.Skipped {
		t.Fatalf("expected zero-row archive to skip idempotently, got skipped=%v err=%v", skippedRes.Skipped, err)
	}

	badDir := filepath.Join(t.TempDir(), "bad")
	badExporter, err := NewColdArchiveExporter(ColdArchiveConfig{
		BaseURL:    server.URL,
		ArchiveDir: badDir,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	mode = "bad_json"
	if _, err := badExporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
		Cutoff:   cutoff,
	}); err == nil || !strings.Contains(err.Error(), "invalid JSONEachRow") {
		t.Fatalf("expected invalid JSONEachRow error, got %v", err)
	}

	mode = "http_error"
	if _, err := badExporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
		Cutoff:   cutoff,
	}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected HTTP 403 error, got %v", err)
	}
	entries, err := os.ReadDir(badDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no committed or temp files after failed exports, got %v", entries)
	}

	if _, err := NewColdArchiveExporter(ColdArchiveConfig{BaseURL: "http://user@localhost:8123", ArchiveDir: t.TempDir()}); err == nil {
		t.Fatal("accepted URL with userinfo")
	}
	if _, err := NewColdArchiveExporter(ColdArchiveConfig{BaseURL: server.URL, ArchiveDir: "../escape"}); err == nil {
		t.Fatal("accepted path traversal in ArchiveDir")
	}
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewColdArchiveExporter(ColdArchiveConfig{BaseURL: server.URL, ArchiveDir: filePath}); err == nil {
		t.Fatal("accepted regular file as ArchiveDir")
	}
	if _, err := badExporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "numeric_gauge_1min_states",
		Cutoff:   cutoff,
	}); err == nil {
		t.Fatal("accepted unsupported archive table")
	}
	if _, err := badExporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry-history",
		Table:    "observations",
		Cutoff:   cutoff,
	}); err == nil {
		t.Fatal("accepted invalid database identifier")
	}
	if _, err := badExporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
	}); err == nil {
		t.Fatal("accepted zero cutoff")
	}
	if _, err := badExporter.ExportBeforePrune(t.Context(), ColdArchiveExportRequest{
		Database:   "telemetry_history",
		Table:      "observations",
		Cutoff:     cutoff,
		LegalHolds: []ClickHouseLegalHoldScope{{TenantID: "invalid-uuid"}},
	}); err == nil {
		t.Fatal("accepted invalid legal hold UUID")
	}
}

func verifyArchiveFileMatchesManifest(t *testing.T, archivePath, manifestPath, wantContent string) {
	t.Helper()

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest ColdArchiveManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	gzBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if int64(len(gzBytes)) != manifest.GzipByteSize {
		t.Fatalf("gzipByteSize=%d want %d", len(gzBytes), manifest.GzipByteSize)
	}
	sum := sha256.Sum256(gzBytes)
	gotHash := hex.EncodeToString(sum[:])
	if gotHash != manifest.GzipSHA256 {
		t.Fatalf("gzipSha256=%q want %q", gotHash, manifest.GzipSHA256)
	}

	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip reader: %v", err)
	}
	defer gzReader.Close()
	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("read decompressed archive: %v", err)
	}
	if string(decompressed) != wantContent {
		t.Fatalf("decompressed content=%q want %q", string(decompressed), wantContent)
	}
}

func TestColdArchiveExporterPingAndGzipSelectStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "archive_user" || pass != "archive_pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("enable_http_compression") != "1" || r.Header.Get("Accept-Encoding") != "gzip" {
			t.Fatalf("expected gzip negotiation, query=%v accept=%q", r.URL.Query(), r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		if r.URL.Query().Get("query") == "SELECT 1" {
			_, _ = io.WriteString(gz, "1\n")
		} else {
			_, _ = io.WriteString(gz, `{"observation_id":"018f2e00-9100-7000-8000-000000000001"}`+"\n")
		}
		_ = gz.Close()
	}))
	defer server.Close()

	exporter, err := NewColdArchiveExporter(ColdArchiveConfig{
		BaseURL:    server.URL,
		ArchiveDir: t.TempDir(),
		Username:   "archive_user",
		Password:   "archive_pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	res, err := exporter.ExportBeforePrune(context.Background(), ColdArchiveExportRequest{
		Database: "telemetry_history",
		Table:    "observations",
		Cutoff:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ExportBeforePrune with gzip stream failed: %v", err)
	}
	if res.Manifest.RowCount != 1 {
		t.Fatalf("RowCount=%d, want 1", res.Manifest.RowCount)
	}
}
