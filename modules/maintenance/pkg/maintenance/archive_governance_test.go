package maintenance

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeVerifiedTestSlice(t *testing.T, dir, sliceBase string, now time.Time) (string, string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(`{"observation_id":"018f2e00-9100-7000-8000-000000000001"}` + "\n"))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	gzBytes := buf.Bytes()
	sum := sha256.Sum256(gzBytes)

	archivePath := filepath.Join(dir, sliceBase+".jsonl.gz")
	if err := os.WriteFile(archivePath, gzBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := coldArchiveManifestRecord{
		SchemaVersion: 1,
		Database:      "telemetry_history",
		Table:         "observations",
		CutoffUTC:     now.Add(-180 * 24 * time.Hour),
		RowCount:      1,
		GzipByteSize:  int64(len(gzBytes)),
		GzipSHA256:    hex.EncodeToString(sum[:]),
		ExportedAtUTC: now,
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, sliceBase+".manifest.json")
	if err := os.WriteFile(manifestPath, rawManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	return archivePath, manifestPath, gzBytes
}

func TestVerifyColdArchiveDirectoryHealthyAndFreshTemp(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()

	writeVerifiedTestSlice(t, dir, "telemetry_history__observations__20260522T120000.000Z", now)

	// Fresh temp file exactly 1 hour old (not > 1h) must not be flagged as stale.
	freshTmp := filepath.Join(dir, "in-progress.jsonl.gz.123.tmp")
	if err := os.WriteFile(freshTmp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	atBoundary := now.Add(-time.Hour)
	if err := os.Chtimes(freshTmp, atBoundary, atBoundary); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyColdArchiveDirectory(t.Context(), dir, now)
	if err != nil {
		t.Fatalf("VerifyColdArchiveDirectory failed: %v", err)
	}
	if !report.Healthy() || report.ManifestsChecked != 1 || report.VerifiedCount != 1 ||
		report.TempFilesScanned != 1 || report.StaleTempCount != 0 ||
		report.CorruptCount != 0 || report.MissingCount != 0 {
		t.Fatalf("unexpected healthy report: %+v", report)
	}
}

func TestVerifyColdArchiveDirectoryFlagsCorruptionMissingAndStaleTemp(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()

	writeVerifiedTestSlice(t, dir, "slice_ok", now)

	badHashArchive, _, gzBytes := writeVerifiedTestSlice(t, dir, "slice_bad_hash", now)
	altered := append([]byte(nil), gzBytes...)
	altered[len(altered)-1] ^= 0xff
	if err := os.WriteFile(badHashArchive, altered, 0o600); err != nil {
		t.Fatal(err)
	}

	badSizeArchive, _, _ := writeVerifiedTestSlice(t, dir, "slice_bad_size", now)
	if err := os.WriteFile(badSizeArchive, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}

	missingArchive, _, _ := writeVerifiedTestSlice(t, dir, "slice_missing", now)
	if err := os.Remove(missingArchive); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "slice_bad_json.manifest.json"), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	badSchema, _ := json.Marshal(coldArchiveManifestRecord{
		SchemaVersion: 2,
		Database:      "telemetry_history",
		Table:         "observations",
		CutoffUTC:     now,
		RowCount:      0,
		GzipByteSize:  20,
		GzipSHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExportedAtUTC: now,
	})
	if err := os.WriteFile(filepath.Join(dir, "slice_bad_schema.manifest.json"), badSchema, 0o600); err != nil {
		t.Fatal(err)
	}

	staleTmp := filepath.Join(dir, "abandoned.jsonl.gz.999.tmp")
	if err := os.WriteFile(staleTmp, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleTime := now.Add(-time.Hour - time.Second)
	if err := os.Chtimes(staleTmp, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyColdArchiveDirectory(t.Context(), dir, now)
	if err != nil {
		t.Fatalf("VerifyColdArchiveDirectory failed: %v", err)
	}
	if report.Healthy() {
		t.Fatal("expected unhealthy report")
	}
	if report.ManifestsChecked != 6 || report.VerifiedCount != 1 || report.MissingCount != 1 ||
		report.CorruptCount != 4 || report.TempFilesScanned != 1 || report.StaleTempCount != 1 {
		t.Fatalf("unexpected report counts: %+v", report)
	}

	byPath := make(map[string]ColdArchiveFindingCode, len(report.Findings))
	for _, f := range report.Findings {
		byPath[f.RelativePath] = f.Code
	}
	want := map[string]ColdArchiveFindingCode{
		"slice_bad_hash.manifest.json":   ColdArchiveFindingChecksumMismatch,
		"slice_bad_size.manifest.json":   ColdArchiveFindingSizeMismatch,
		"slice_missing.manifest.json":    ColdArchiveFindingArchiveMissing,
		"slice_bad_json.manifest.json":   ColdArchiveFindingManifestCorrupt,
		"slice_bad_schema.manifest.json": ColdArchiveFindingManifestCorrupt,
		"abandoned.jsonl.gz.999.tmp":     ColdArchiveFindingStaleTempFile,
	}
	for path, wantCode := range want {
		if gotCode, ok := byPath[path]; !ok || gotCode != wantCode {
			t.Fatalf("finding[%q]=%q (present=%v), want %q", path, gotCode, ok, wantCode)
		}
	}
}

func TestVerifyColdArchiveDirectoryRejectsInvalidInputsAndCancelledContext(t *testing.T) {
	if _, err := VerifyColdArchiveDirectory(t.Context(), "", time.Now()); err == nil {
		t.Fatal("expected error for empty dir")
	}
	if _, err := VerifyColdArchiveDirectory(t.Context(), "../escape", time.Now()); err == nil {
		t.Fatal("expected error for path traversal dir")
	}
	if _, err := VerifyColdArchiveDirectory(t.Context(), filepath.Join(t.TempDir(), "does-not-exist"), time.Now()); err == nil {
		t.Fatal("expected error for missing dir")
	}
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyColdArchiveDirectory(t.Context(), filePath, time.Now()); err == nil {
		t.Fatal("expected error for regular file path")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := VerifyColdArchiveDirectory(ctx, t.TempDir(), time.Now()); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestVerifyColdArchiveDirectoryFollowsSymlinkRoot(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	baseDir := t.TempDir()
	realDir := filepath.Join(baseDir, "real-archive")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeVerifiedTestSlice(t, realDir, "slice_ok", now)
	badHashArchive, _, gzBytes := writeVerifiedTestSlice(t, realDir, "slice_corrupt", now)
	corrupted := append([]byte(nil), gzBytes...)
	corrupted[0] ^= 0xff
	if err := os.WriteFile(badHashArchive, corrupted, 0o600); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(baseDir, "symlink-mount")
	if err := os.Symlink(realDir, linkDir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation requires elevated privilege on Windows: %v", err)
		}
		t.Fatal(err)
	}

	report, err := VerifyColdArchiveDirectory(t.Context(), linkDir, now)
	if err != nil {
		t.Fatalf("VerifyColdArchiveDirectory via symlink failed: %v", err)
	}
	if report.ManifestsChecked != 2 || report.VerifiedCount != 1 || report.CorruptCount != 1 || report.Healthy() {
		t.Fatalf("symlinked archive root was not audited: %+v", report)
	}
}
