package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeExe(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustReadBackup(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func commandEnv(bin string, extra ...string) []string {
	kept := make([]string, 0, len(os.Environ())+len(extra)+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") {
			continue
		}
		kept = append(kept, entry)
	}
	kept = append(kept, "PATH="+bin+":"+os.Getenv("PATH"))
	return append(kept, extra...)
}

func createFakePostgresBackup(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(dir, "base.tar.gz")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	payload := []byte("PG_VERSION_16")
	if err := tw.WriteHeader(&tar.Header{Name: "PG_VERSION", Mode: 0o600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	raw, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	manifest := fmt.Sprintf("%x  ./base.tar.gz\n", sum)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return tarPath
}

func TestSyncOffsiteResticCOSLookupInitBackupForgetCheck(t *testing.T) {
	setupFixtures := func(t *testing.T, mode os.FileMode) (runtimeDir, envFile, bin, logFile string) {
		t.Helper()
		runtimeDir = t.TempDir()
		pgDir := filepath.Join(runtimeDir, "backups", "postgres", "base", "20261125T033000Z")
		walDir := filepath.Join(runtimeDir, "backups", "postgres", "wal")
		chDir := filepath.Join(runtimeDir, "backups", "clickhouse", "20261125T033000Z")
		createFakePostgresBackup(t, pgDir)
		if err := os.MkdirAll(walDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(walDir, "000000010000000000000001"), []byte("wal"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(chDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chDir, "metadata.sql"), []byte("CREATE DATABASE telemetry_history"), 0o644); err != nil {
			t.Fatal(err)
		}

		envFile = filepath.Join(t.TempDir(), "backup-offsite.env")
		envBody := strings.Join([]string{
			"RESTIC_REPOSITORY=s3:cos.ap-guangzhou.myqcloud.com/hvac-backup-gz-1257763875/hvac-phase1",
			"RESTIC_PASSWORD=super-secret-restic",
			"AWS_ACCESS_KEY_ID=cos-access-key",
			"AWS_SECRET_ACCESS_KEY=cos-secret-key",
		}, "\n") + "\n"
		if err := os.WriteFile(envFile, []byte(envBody), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(envFile, mode); err != nil {
			t.Fatal(err)
		}

		bin = t.TempDir()
		logFile = filepath.Join(t.TempDir(), "restic.log")
		writeExe(t, bin, "restic", `#!/usr/bin/env bash
set -euo pipefail
printf 'restic %s | region=%s\n' "$*" "${AWS_DEFAULT_REGION:-}" >> "$RESTIC_LOG"
for arg in "$@"; do
  if [ "$arg" = "cat" ]; then
    if [ "${MOCK_CAT_MODE:-missing}" = "missing" ]; then
      echo "Fatal: unable to open config file: Is there a repository at the following location?" >&2
      exit 10
    elif [ "${MOCK_CAT_MODE:-}" = "auth" ]; then
      echo "Fatal: wrong password or no key found" >&2
      exit 1
    fi
  fi
done
exit 0
`)
		return runtimeDir, envFile, bin, logFile
	}

	t.Run("initializes COS repo with dns lookup, backs up, prunes, and redacts secrets", func(t *testing.T) {
		runtimeDir, envFile, bin, logFile := setupFixtures(t, 0o600)
		cmd := exec.Command("bash", "sync-offsite-restic.sh")
		cmd.Env = commandEnv(bin,
			"PHASE1_RUNTIME_DIR="+runtimeDir,
			"OFFSITE_BACKUP_ENV_FILE="+envFile,
			"RESTIC_LOG="+logFile,
			"MOCK_CAT_MODE=missing",
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sync-offsite-restic.sh failed: %v\n%s", err, output)
		}
		for _, secret := range []string{"super-secret-restic", "cos-secret-key"} {
			if strings.Contains(string(output), secret) {
				t.Fatalf("sync-offsite-restic.sh leaked secret %q in output:\n%s", secret, output)
			}
		}
		logText := string(mustReadBackup(t, logFile))
		for _, want := range []string{
			"-o s3.bucket-lookup=dns cat config | region=ap-guangzhou",
			"-o s3.bucket-lookup=dns init",
			"--one-file-system --tag phase1-backup --tag hvac-platform",
			"backups/postgres/base",
			"backups/clickhouse",
			"backups/postgres/wal",
			"-o s3.bucket-lookup=dns forget --tag phase1-backup --keep-daily 7 --keep-weekly 4 --prune",
			"-o s3.bucket-lookup=dns check --read-data-subset=1%",
		} {
			if !strings.Contains(logText, want) {
				t.Fatalf("restic log missing %q:\n%s", want, logText)
			}
		}
	})

	t.Run("fails closed on repository auth error without running init", func(t *testing.T) {
		runtimeDir, envFile, bin, logFile := setupFixtures(t, 0o600)
		cmd := exec.Command("bash", "sync-offsite-restic.sh")
		cmd.Env = commandEnv(bin,
			"PHASE1_RUNTIME_DIR="+runtimeDir,
			"OFFSITE_BACKUP_ENV_FILE="+envFile,
			"RESTIC_LOG="+logFile,
			"MOCK_CAT_MODE=auth",
		)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "failed to access restic repository") {
			t.Fatalf("expected fail-closed repository error, err=%v\n%s", err, output)
		}
		logText := string(mustReadBackup(t, logFile))
		if strings.Contains(logText, " init") || strings.Contains(logText, " backup") {
			t.Fatalf("must not init or backup after auth failure:\n%s", logText)
		}
	})

	t.Run("rejects group/world-readable offsite env file", func(t *testing.T) {
		runtimeDir, envFile, bin, logFile := setupFixtures(t, 0o644)
		cmd := exec.Command("bash", "sync-offsite-restic.sh")
		cmd.Env = commandEnv(bin,
			"PHASE1_RUNTIME_DIR="+runtimeDir,
			"OFFSITE_BACKUP_ENV_FILE="+envFile,
			"RESTIC_LOG="+logFile,
		)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "must not be group/world-accessible") {
			t.Fatalf("expected 0644 permission failure, err=%v\n%s", err, output)
		}
	})
}

func TestRunOffsiteRestoreDrillSkipDockerAndSha256Check(t *testing.T) {
	setupDrill := func(t *testing.T, tamperTar bool) (bin, envFile, fixtureSrc string) {
		t.Helper()
		fixtureSrc = t.TempDir()
		pgDir := filepath.Join(fixtureSrc, "backups", "postgres", "base", "20261125T033000Z")
		tarPath := createFakePostgresBackup(t, pgDir)
		if tamperTar {
			if err := os.WriteFile(tarPath, []byte("tampered"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		chDir := filepath.Join(fixtureSrc, "backups", "clickhouse", "20261125T033000Z")
		if err := os.MkdirAll(chDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chDir, "data.bin"), []byte("ch"), 0o644); err != nil {
			t.Fatal(err)
		}

		envFile = filepath.Join(t.TempDir(), "offsite.env")
		if err := os.WriteFile(envFile, []byte("RESTIC_REPOSITORY=s3:cos.ap-guangzhou.myqcloud.com/hvac-backup-gz-1257763875/hvac-phase1\nRESTIC_PASSWORD=pw\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(envFile, 0o600); err != nil {
			t.Fatal(err)
		}

		bin = t.TempDir()
		writeExe(t, bin, "restic", `#!/usr/bin/env bash
set -euo pipefail
args=("$@")
for ((i=0; i<${#args[@]}; i++)); do
  case "${args[$i]}" in
    snapshots)
      printf '[{"id":"abcdef1234567890","short_id":"abcdef12","time":"2026-11-25T03:30:00Z"}]\n'
      exit 0
      ;;
    restore)
      target=""
      for ((j=0; j<${#args[@]}; j++)); do
        if [ "${args[$j]}" = "--target" ]; then target="${args[$((j+1))]}"; fi
      done
      mkdir -p "$target"
      cp -a "$RESTIC_FIXTURE_SRC/." "$target/"
      exit 0
      ;;
  esac
done
exit 0
`)
		return bin, envFile, fixtureSrc
	}

	t.Run("verifies SHA256 and writes structured report and drill record draft", func(t *testing.T) {
		bin, envFile, fixtureSrc := setupDrill(t, false)
		reportOut := filepath.Join(t.TempDir(), "out", "report.json")
		recordOut := filepath.Join(t.TempDir(), "out", "drill-record.json")

		cmd := exec.Command("bash", "run-offsite-restore-drill.sh",
			"--skip-docker",
			"--environment=staging",
			"--operator=ci-test",
			"--report-out="+reportOut,
			"--drill-record-out="+recordOut,
		)
		cmd.Env = commandEnv(bin,
			"OFFSITE_BACKUP_ENV_FILE="+envFile,
			"RESTIC_FIXTURE_SRC="+fixtureSrc,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run-offsite-restore-drill.sh failed: %v\n%s", err, output)
		}

		var report map[string]any
		if err := json.Unmarshal(mustReadBackup(t, reportOut), &report); err != nil {
			t.Fatal(err)
		}
		if report["status"] != "passed" || report["sha256Verified"] != true || report["dockerSkipped"] != true {
			t.Fatalf("unexpected drill report: %v", report)
		}
		if report["postgresBackupStamp"] != "20261125T033000Z" || report["clickhouseBackupStamp"] != "20261125T033000Z" {
			t.Fatalf("unexpected backup stamps: %v", report)
		}

		var draft map[string]any
		if err := json.Unmarshal(mustReadBackup(t, recordOut), &draft); err != nil {
			t.Fatal(err)
		}
		prereq, _ := draft["prerequisites"].(map[string]any)
		evidence, _ := draft["evidence"].(map[string]any)
		if prereq["externalBackupVerified"] != true || prereq["backupSurvivesProductionServerLoss"] != true {
			t.Fatalf("expected external backup prerequisites true in draft: %v", draft)
		}
		if evidence["operator"] != "ci-test" {
			t.Fatalf("expected operator ci-test, got %v", evidence)
		}
	})

	t.Run("fails closed when PostgreSQL SHA256SUMS fails", func(t *testing.T) {
		bin, envFile, fixtureSrc := setupDrill(t, true)
		cmd := exec.Command("bash", "run-offsite-restore-drill.sh", "--skip-docker")
		cmd.Env = commandEnv(bin,
			"OFFSITE_BACKUP_ENV_FILE="+envFile,
			"RESTIC_FIXTURE_SRC="+fixtureSrc,
		)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "FAILED") {
			t.Fatalf("expected sha256sum failure on tampered tar, err=%v\n%s", err, output)
		}
	})
}

func TestHostTextfileEmitsOffsiteMetricConditionally(t *testing.T) {
	runHostMetrics := func(t *testing.T, runtimeDir string, extraEnv ...string) string {
		t.Helper()
		bin := t.TempDir()
		writeExe(t, bin, "docker", "#!/bin/sh\nif [ \"$1\" = info ]; then exit 0; fi\nexit 0\n")
		cmd := exec.Command("sh", "../observability/host-textfile.sh")
		cmd.Env = commandEnv(bin, append([]string{"PHASE1_RUNTIME_DIR=" + runtimeDir}, extraEnv...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("host-textfile.sh failed: %v\n%s", err, out)
		}
		return string(mustReadBackup(t, filepath.Join(runtimeDir, "observability", "textfile", "host.prom")))
	}

	t.Run("omits offsite metric by default when unconfigured", func(t *testing.T) {
		prom := runHostMetrics(t, t.TempDir())
		if strings.Contains(prom, `store="offsite"`) {
			t.Fatalf("unexpected offsite metric without stamp or flag:\n%s", prom)
		}
	})

	t.Run("emits offsite timestamp from offsite-backup.success", func(t *testing.T) {
		rt := t.TempDir()
		if err := os.MkdirAll(filepath.Join(rt, "observability"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rt, "observability", "offsite-backup.success"), []byte("1764041400\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		prom := runHostMetrics(t, rt)
		if !strings.Contains(prom, `hvac_backup_last_success_timestamp_seconds{store="offsite"} 1764041400`) {
			t.Fatalf("missing offsite timestamp metric:\n%s", prom)
		}
	})

	t.Run("emits 0 when BACKUP_OFFSITE_REQUIRED=1 and stamp is missing", func(t *testing.T) {
		prom := runHostMetrics(t, t.TempDir(), "BACKUP_OFFSITE_REQUIRED=1")
		if !strings.Contains(prom, `hvac_backup_last_success_timestamp_seconds{store="offsite"} 0`) {
			t.Fatalf("expected offsite metric 0 when required without stamp:\n%s", prom)
		}
	})
}
