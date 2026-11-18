package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
