package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/maintenance/pkg/maintenance"
)

type stubLifecycleAuditor struct {
	calls   int
	summary maintenance.LifecycleAuditSummary
	err     error
}

func (s *stubLifecycleAuditor) AuditLifecycleGovernance(context.Context, time.Time) (maintenance.LifecycleAuditSummary, error) {
	s.calls++
	return s.summary, s.err
}

func writeTestArchiveSlice(t *testing.T, dir, base string, corruptArchive bool) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(`{"observation_id":"018f2e00-9100-7000-8000-000000000001"}` + "\n"))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	gzBytes := buf.Bytes()
	sum := sha256.Sum256(gzBytes)

	manifestBytes, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"database":      "telemetry_history",
		"table":         "observations",
		"cutoffUTC":     time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
		"rowCount":      1,
		"gzipByteSize":  len(gzBytes),
		"gzipSha256":    hex.EncodeToString(sum[:]),
		"exportedAtUTC": time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, base+".manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if corruptArchive {
		gzBytes[len(gzBytes)-1] ^= 0xff
	}
	if err := os.WriteFile(filepath.Join(dir, base+".jsonl.gz"), gzBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunLifecycleAuditWithColdArchiveVerification(t *testing.T) {
	t.Run("healthy cold archive logs counts without secrets", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "secret-archive-root")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		writeTestArchiveSlice(t, dir, "slice_healthy", false)

		var logs bytes.Buffer
		logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
		runtime := observability.NewRuntime(observability.RuntimeConfig{Service: "maintenance-test"})
		auditor := &stubLifecycleAuditor{
			summary: maintenance.LifecycleAuditSummary{PoliciesChecked: 2, FindingsCount: 1},
		}

		runLifecycleAudit(t.Context(), auditor, dir, runtime, logger)
		if auditor.calls != 1 {
			t.Fatalf("auditor calls=%d want 1", auditor.calls)
		}
		out := logs.String()
		if !strings.Contains(out, `"maintenance_lifecycle_audit_completed"`) ||
			!strings.Contains(out, `"maintenance_cold_archive_audit_completed"`) ||
			!strings.Contains(out, `"verified_count":1`) {
			t.Fatalf("unexpected logs: %s", out)
		}
		if strings.Contains(out, "secret-archive-root") {
			t.Fatalf("log leaked archive path: %s", out)
		}
	})

	t.Run("corrupt archive emits error code and redacted counts even if db audit errors", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "secret-corrupt-root")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		writeTestArchiveSlice(t, dir, "slice_corrupt", true)

		var logs bytes.Buffer
		logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
		runtime := observability.NewRuntime(observability.RuntimeConfig{Service: "maintenance-test"})
		auditor := &stubLifecycleAuditor{err: errors.New("postgres dsn secret leak")}

		runLifecycleAudit(t.Context(), auditor, dir, runtime, logger)
		out := logs.String()
		if !strings.Contains(out, `"MAINTENANCE_LIFECYCLE_AUDIT_FAILED"`) ||
			!strings.Contains(out, `"MAINTENANCE_COLD_ARCHIVE_INTEGRITY_FINDINGS"`) ||
			!strings.Contains(out, `"corrupt_count":1`) {
			t.Fatalf("expected both lifecycle and archive error codes in logs: %s", out)
		}
		if strings.Contains(out, "postgres dsn secret leak") || strings.Contains(out, "secret-corrupt-root") || strings.Contains(out, "slice_corrupt") {
			t.Fatalf("log leaked raw error or path: %s", out)
		}
	})

	t.Run("missing archive dir redacts filesystem error", func(t *testing.T) {
		missingDir := filepath.Join(t.TempDir(), "missing-secret-dir")
		var logs bytes.Buffer
		logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
		runtime := observability.NewRuntime(observability.RuntimeConfig{Service: "maintenance-test"})

		runLifecycleAudit(t.Context(), &stubLifecycleAuditor{}, missingDir, runtime, logger)
		out := logs.String()
		if !strings.Contains(out, `"MAINTENANCE_COLD_ARCHIVE_AUDIT_FAILED"`) {
			t.Fatalf("expected audit failure error code, got %s", out)
		}
		if strings.Contains(out, "missing-secret-dir") || strings.Contains(out, "no such file") {
			t.Fatalf("log leaked filesystem details: %s", out)
		}
	})
}

func TestLoadWorkerSettingsValidation(t *testing.T) {
	t.Run("defaults without error", func(t *testing.T) {
		settings, err := loadWorkerSettings()
		if err != nil {
			t.Fatalf("loadWorkerSettings() error = %v", err)
		}
		if settings.batch != 10 || settings.leaseDuration != 30*time.Second || settings.scanInterval != 2*time.Second || settings.auditInterval != time.Minute {
			t.Fatalf("unexpected defaults: %+v", settings)
		}
	})

	t.Run("invalid integer returns error without panic", func(t *testing.T) {
		t.Setenv("MAINTENANCE_CLAIM_BATCH", "not-a-number")
		if _, err := loadWorkerSettings(); err == nil {
			t.Fatal("expected error for invalid MAINTENANCE_CLAIM_BATCH")
		}
	})

	t.Run("out of bounds duration returns error without panic", func(t *testing.T) {
		t.Setenv("MAINTENANCE_SCAN_INTERVAL", "10ms")
		if _, err := loadWorkerSettings(); err == nil {
			t.Fatal("expected error for out-of-range MAINTENANCE_SCAN_INTERVAL")
		}
	})
}

type maintenanceRecordingExporter struct {
	record func(string)
}

func (e *maintenanceRecordingExporter) Export(context.Context, []observability.SpanData) error {
	return nil
}

func (e *maintenanceRecordingExporter) Shutdown(context.Context) error {
	if e.record != nil {
		e.record("telemetry_shutdown")
	}
	return nil
}

func TestShutdownMaintenanceWorkerMarksNotReadyImmediatelyAndOrdersTeardown(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(ev string) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:  "maintenance-service-test",
		Exporter: &maintenanceRecordingExporter{record: record},
	})
	telemetry.MarkReady()

	httpEntered := make(chan struct{})
	releaseHTTP := make(chan struct{})
	diagSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(httpEntered)
			<-releaseHTTP
			record("diag_handler_done")
			w.WriteHeader(http.StatusOK)
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	diagDone := make(chan struct{})
	go func() {
		defer close(diagDone)
		_ = diagSrv.Serve(ln)
		record("diag_goroutine_done")
	}()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	workerStarted := make(chan struct{})
	go func() {
		defer close(workerDone)
		close(workerStarted)
		<-workerCtx.Done()
		record("worker_done")
	}()
	<-workerStarted

	reqErrCh := make(chan error, 1)
	go func() {
		resp, reqErr := http.Get("http://" + ln.Addr().String() + "/metrics")
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		reqErrCh <- reqErr
	}()
	<-httpEntered

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		shutdownMaintenanceWorker(
			nil,
			telemetry,
			diagSrv,
			diagDone,
			cancelWorker,
			workerDone,
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = telemetry.Shutdown(ctx)
			},
			func() { record("store_closed") },
			2*time.Second,
		)
	}()

	deadline := time.Now().Add(time.Second)
	for telemetry.Ready() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if telemetry.Ready() {
		close(releaseHTTP)
		t.Fatal("expected telemetry.MarkNotReady() immediately on shutdown")
	}
	if workerCtx.Err() != nil {
		close(releaseHTTP)
		t.Fatalf("workerCtx canceled before diagnostics HTTP shutdown completed")
	}

	close(releaseHTTP)
	if err := <-reqErrCh; err != nil {
		t.Fatalf("diagnostics request failed: %v", err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownMaintenanceWorker timed out")
	}

	mu.Lock()
	got := slices.Clone(events)
	mu.Unlock()
	diagHandlerIdx := slices.Index(got, "diag_handler_done")
	diagGoroutineIdx := slices.Index(got, "diag_goroutine_done")
	workerIdx := slices.Index(got, "worker_done")
	telemetryIdx := slices.Index(got, "telemetry_shutdown")
	storeIdx := slices.Index(got, "store_closed")
	if diagHandlerIdx < 0 || diagGoroutineIdx < 0 || workerIdx < 0 || telemetryIdx < 0 || storeIdx < 0 {
		t.Fatalf("missing expected shutdown events: %v", got)
	}
	if diagHandlerIdx >= workerIdx || diagGoroutineIdx >= workerIdx {
		t.Fatalf("expected diagnostics drain and exit before worker_done: %v", got)
	}
	if workerIdx >= telemetryIdx || telemetryIdx >= storeIdx {
		t.Fatalf("expected worker_done < telemetry_shutdown < store_closed: %v", got)
	}
}
