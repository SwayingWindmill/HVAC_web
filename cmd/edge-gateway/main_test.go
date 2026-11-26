package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/cmd/edge-gateway/internal/edgedaemon"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestCLIVersionValidateAndHealthCheck(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--version"}, &out, &errOut); err != nil {
		t.Fatalf("--version failed: %v", err)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("--version = %q, want %q", out.String(), version)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"gatewayId":"0193f000-2000-7000-8000-000000000300","stateDir":"` + filepath.ToSlash(filepath.Join(dir, "state")) + `"}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if err := run([]string{"--validate-config", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("--validate-config failed: %v", err)
	}
	if !strings.Contains(out.String(), "config valid") {
		t.Fatalf("unexpected --validate-config output: %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if err := run([]string{"--health-check", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("--health-check failed: %v", err)
	}
	if !strings.Contains(out.String(), "health check passed") {
		t.Fatalf("unexpected --health-check output: %q", out.String())
	}
}

func TestHealthCheckDoesNotCollideWithLiveHealthPortOrStateDir(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	dir := t.TempDir()
	liveStateDir := filepath.Join(dir, "live-state")
	liveOTADir := filepath.Join(dir, "live-ota")
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := fmt.Sprintf(`{
		"gatewayId": "0193f000-2000-7000-8000-000000000300",
		"stateDir": %q,
		"platformBaseUrl": "https://127.0.0.1:1",
		"mqttBrokerUrl": "tls://127.0.0.1:1",
		"healthListenAddr": %q,
		"tunnel": {"enabled": true, "allowedTargetPorts": [22]},
		"ota": {"enabled": true, "dir": %q},
		"fieldPlant": {"enabled": true, "serialPort": "/dev/ttyNonExistentOTAProbe"}
	}`, filepath.ToSlash(liveStateDir), ln.Addr().String(), filepath.ToSlash(liveOTADir))
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := run([]string{"--health-check", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("--health-check failed while live health port occupied: %v (stderr=%s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "health check passed") {
		t.Fatalf("unexpected --health-check output: %q", out.String())
	}
	for _, untouched := range []string{liveStateDir, liveOTADir} {
		if _, statErr := os.Stat(untouched); !os.IsNotExist(statErr) {
			t.Fatalf("expected --health-check to leave %s untouched, stat err=%v", untouched, statErr)
		}
	}
}

func TestBrokenConfigRollsBackPendingOTAFromSlotBToSlotA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink-based A/B OTA rollback test runs on POSIX filesystem")
	}

	dir := t.TempDir()
	otaDir := filepath.Join(dir, "ota")
	slotsDir := filepath.Join(otaDir, "slots")
	if err := os.MkdirAll(slotsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotsDir, "slot-a"), []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotsDir, "slot-b"), []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("slots", "slot-b"), filepath.Join(otaDir, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("slots", "slot-a"), filepath.Join(otaDir, "previous")); err != nil {
		t.Fatal(err)
	}

	pendingState := edgedaemon.OTAState{
		CurrentVersion:  "1.0.0",
		PreviousVersion: "1.0.0",
		ActiveSlot:      "slot-b",
		PreviousSlot:    "slot-a",
		Status:          edgecontrol.OTAStageHealthChecking,
		PendingStage: &edgecontrol.OTAStagePayload{
			CampaignID:            "0193f000-2000-7000-8000-000000000102",
			TaskID:                "0193f000-2000-7000-8000-000000000103",
			PackageID:             "0193f000-2000-7000-8000-000000000101",
			Version:               "1.2.0",
			Component:             edgecontrol.OTAComponentEdgeGateway,
			TargetOS:              "linux",
			TargetArch:            "armv7l",
			DownloadURL:           "https://platform.example.com/pkg",
			FileSizeBytes:         2,
			SHA256:                strings.Repeat("a", 64),
			SignatureAlg:          edgecontrol.OTASignatureAlgEd25519,
			SignatureB64:          "c2ln",
			SigningKeyID:          "release-2026",
			HealthCheckTimeoutSec: 60,
			Action:                edgecontrol.OTAActionApply,
		},
		HealthDeadlineUnixMs: 1_900_000_000_000,
	}
	stateRaw, err := json.MarshalIndent(pendingState, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(otaDir, "ota-state.json")
	if err := os.WriteFile(statePath, stateRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	// Write a broken config.json containing an unknown field rejected by LoadConfig.
	cfgPath := filepath.Join(dir, "config.json")
	brokenJSON := fmt.Sprintf(`{"gatewayId":"0193f000-2000-7000-8000-000000000301","stateDir":%q,"ota":{"enabled":true,"dir":%q},"unexpectedBadField":true}`,
		filepath.ToSlash(filepath.Join(dir, "state")),
		filepath.ToSlash(otaDir),
	)
	if err := os.WriteFile(cfgPath, []byte(brokenJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	// --validate-config must NOT trigger OTA rollback.
	if err := run([]string{"--validate-config", "--config", cfgPath}, &out, &errOut); err == nil {
		t.Fatal("expected --validate-config to reject broken config")
	}
	if target, _ := os.Readlink(filepath.Join(otaDir, "current")); target != filepath.Join("slots", "slot-b") {
		t.Fatalf("--validate-config must not roll back slot-b, got %q", target)
	}

	// Normal startup failure MUST trigger early rollback from slot-b back to slot-a.
	out.Reset()
	errOut.Reset()
	if err := run([]string{"--config", cfgPath}, &out, &errOut); err == nil {
		t.Fatal("expected run with broken config to return error")
	}
	if target, err := os.Readlink(filepath.Join(otaDir, "current")); err != nil || target != filepath.Join("slots", "slot-a") {
		t.Fatalf("expected current symlink rolled back to slots/slot-a, got %q (err=%v)", target, err)
	}

	updatedRaw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var updated edgedaemon.OTAState
	if err := json.Unmarshal(updatedRaw, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status != edgecontrol.OTAStageRolledBack || updated.ActiveSlot != "slot-a" {
		t.Fatalf("unexpected rolled back OTA state: %+v", updated)
	}
	if updated.PendingRollbackReport == nil || updated.PendingRollbackReport.Stage != edgecontrol.OTAStageRolledBack || updated.PendingRollbackReport.TargetVersion != "1.2.0" {
		t.Fatalf("expected pendingRollbackReport populated for next boot reporting, got %+v", updated.PendingRollbackReport)
	}
}
