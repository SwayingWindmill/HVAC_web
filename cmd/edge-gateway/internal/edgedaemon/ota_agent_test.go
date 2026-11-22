package edgedaemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func buildSignedStage(
	t *testing.T,
	priv ed25519.PrivateKey,
	keyID string,
	downloadURL string,
	artifact []byte,
	version string,
	action string,
) edgecontrol.OTAStagePayload {
	t.Helper()
	sum := sha256.Sum256(artifact)
	shaHex := hex.EncodeToString(sum[:])
	pkgID := "0193f000-2000-7000-8000-000000000101"
	sigB64, err := edgecontrol.SignOTAPackageEd25519(priv, pkgID, version, edgecontrol.OTAComponentEdgeGateway, "linux", "armv7l", shaHex, int64(len(artifact)))
	if err != nil {
		t.Fatal(err)
	}
	return edgecontrol.OTAStagePayload{
		CampaignID:            "0193f000-2000-7000-8000-000000000102",
		TaskID:                "0193f000-2000-7000-8000-000000000103",
		PackageID:             pkgID,
		Version:               version,
		Component:             edgecontrol.OTAComponentEdgeGateway,
		TargetOS:              "linux",
		TargetArch:            "armv7l",
		HardwareModels:        []string{"EG8200"},
		MinCompatibleVersion:  "1.0.0",
		DownloadURL:           downloadURL,
		FileSizeBytes:         int64(len(artifact)),
		SHA256:                shaHex,
		SignatureAlg:          edgecontrol.OTASignatureAlgEd25519,
		SignatureB64:          sigB64,
		SigningKeyID:          keyID,
		HealthCheckTimeoutSec: 60,
		Action:                action,
	}
}

func TestOTAAgentResumeVerifyABSwitchAndRollback(t *testing.T) {
	now := time.Unix(1_760_200_000, 0).UTC()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "release-2026"
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	v1Bin := []byte("#!/bin/sh\necho v1.0.0\n")
	v2Bin := []byte("#!/bin/sh\necho v1.2.0-upgraded-payload-0123456789\n")

	var rangeHeaderSeen string
	var serveCorrupt bool
	pkgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := v2Bin
		if serveCorrupt {
			payload = bytes.Replace(bytes.Clone(v2Bin), []byte("upgraded"), []byte("tampered"), 1)
		}
		rng := r.Header.Get("Range")
		if rng != "" {
			rangeHeaderSeen = rng
			offsetStr := strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-")
			offset, parseErr := strconv.Atoi(offsetStr)
			if parseErr == nil && offset >= 0 && offset < len(payload) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[offset:])
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(pkgSrv.Close)

	dir := t.TempDir()
	agent, err := NewOTAAgent(OTAAgentOptions{
		Dir:            dir,
		CurrentVersion: "1.0.0",
		HardwareModel:  "EG8200",
		TargetOS:       "linux",
		TargetArch:     "armv7l",
		TrustedKeys:    map[string]string{keyID: pubB64},
		HTTPClient:     pkgSrv.Client(),
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOTAAgent: %v", err)
	}
	if err := agent.SeedSlotA(v1Bin); err != nil {
		t.Fatalf("SeedSlotA: %v", err)
	}

	// 1. Incompatible arch rejected before download
	badArchStage := buildSignedStage(t, priv, keyID, pkgSrv.URL+"/pkg", v2Bin, "1.2.0", edgecontrol.OTAActionApply)
	badArchStage.TargetArch = "amd64"
	var results []edgecontrol.OTAResultPayload
	if err := agent.ApplyStage(t.Context(), badArchStage, func(r edgecontrol.OTAResultPayload) {
		results = append(results, r)
	}); err == nil || len(results) == 0 || results[len(results)-1].ErrorCode != "INCOMPATIBLE_TARGET" {
		t.Fatalf("expected INCOMPATIBLE_TARGET before download, got err=%v results=%+v", err, results)
	}

	// 2. Corrupted payload rejected by SHA-256 without overwriting active slot-a
	serveCorrupt = true
	results = nil
	stageV2 := buildSignedStage(t, priv, keyID, pkgSrv.URL+"/pkg", v2Bin, "1.2.0", edgecontrol.OTAActionApply)
	if err := agent.ApplyStage(t.Context(), stageV2, func(r edgecontrol.OTAResultPayload) {
		results = append(results, r)
	}); err == nil || results[len(results)-1].ErrorCode != "CHECKSUM_MISMATCH" {
		t.Fatalf("expected CHECKSUM_MISMATCH, got err=%v results=%+v", err, results)
	}
	activeBytes, _ := os.ReadFile(filepath.Join(dir, "current"))
	if !bytes.Equal(activeBytes, v1Bin) {
		t.Fatal("active slot changed after checksum failure")
	}

	// 3. Invalid Ed25519 signature rejected cleanly
	serveCorrupt = false
	badSigStage := stageV2
	badSigStage.SignatureB64 = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	results = nil
	if err := agent.ApplyStage(t.Context(), badSigStage, func(r edgecontrol.OTAResultPayload) {
		results = append(results, r)
	}); err == nil || results[len(results)-1].ErrorCode != "SIGNATURE_INVALID" {
		t.Fatalf("expected SIGNATURE_INVALID, got err=%v results=%+v", err, results)
	}

	// 4. Resumable HTTP Range download + A/B switch to slot-b -> boot failure triggers auto-rollback
	partFile := filepath.Join(dir, "downloads", stageV2.PackageID+".part")
	if err := os.WriteFile(partFile, v2Bin[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	results = nil
	if err := agent.ApplyStage(t.Context(), stageV2, func(r edgecontrol.OTAResultPayload) {
		results = append(results, r)
	}); err != nil {
		t.Fatalf("ApplyStage v2 failed: %v", err)
	}
	if rangeHeaderSeen != "bytes=10-" {
		t.Fatalf("expected Range bytes=10-, got %q", rangeHeaderSeen)
	}
	if results[len(results)-1].Stage != edgecontrol.OTAStageHealthChecking {
		t.Fatalf("expected HEALTH_CHECKING final stage after ApplyStage, got %+v", results[len(results)-1])
	}
	activeAfterSwap, _ := os.ReadFile(filepath.Join(dir, "current"))
	if !bytes.Equal(activeAfterSwap, v2Bin) {
		t.Fatal("current symlink did not switch to slot-b v2 binary")
	}

	// Simulate failed boot health check -> automatic rollback to slot-a
	var bootReports []edgecontrol.OTAResultPayload
	rolledBack, err := agent.VerifyPendingUpdateOnBoot(
		t.Context(),
		func(context.Context) error { return errors.New("daemon self-check failed") },
		func(r edgecontrol.OTAResultPayload) { bootReports = append(bootReports, r) },
	)
	if err != nil || !rolledBack {
		t.Fatalf("expected automatic rollback on health failure, rolledBack=%v err=%v", rolledBack, err)
	}
	if bootReports[len(bootReports)-1].Stage != edgecontrol.OTAStageRolledBack {
		t.Fatalf("expected ROLLED_BACK report, got %+v", bootReports[len(bootReports)-1])
	}
	revertedBytes, _ := os.ReadFile(filepath.Join(dir, "current"))
	if !bytes.Equal(revertedBytes, v1Bin) {
		t.Fatal("current symlink did not revert back to slot-a v1 binary")
	}

	// 5. Re-apply valid v2 and pass boot health check -> SUCCEEDED; then manual ROLLBACK
	if err := agent.ApplyStage(t.Context(), stageV2, nil); err != nil {
		t.Fatalf("re-apply v2: %v", err)
	}
	bootReports = nil
	rolledBack, err = agent.VerifyPendingUpdateOnBoot(
		t.Context(),
		func(context.Context) error { return nil },
		func(r edgecontrol.OTAResultPayload) { bootReports = append(bootReports, r) },
	)
	if err != nil || rolledBack || bootReports[0].Stage != edgecontrol.OTAStageSucceeded {
		t.Fatalf("expected SUCCEEDED commit, rolledBack=%v err=%v reports=%+v", rolledBack, err, bootReports)
	}
	st, err := agent.State()
	if err != nil || st.CurrentVersion != "1.2.0" {
		t.Fatalf("unexpected committed OTA state: %+v err=%v", st, err)
	}

	// Manual ROLLBACK reverts from 1.2.0 back to slot-a (1.0.0)
	rbStage := buildSignedStage(t, priv, keyID, pkgSrv.URL+"/pkg", v1Bin, "1.0.0", edgecontrol.OTAActionRollback)
	results = nil
	if err := agent.ApplyStage(t.Context(), rbStage, func(r edgecontrol.OTAResultPayload) {
		results = append(results, r)
	}); err != nil || results[0].Stage != edgecontrol.OTAStageRolledBack {
		t.Fatalf("manual ROLLBACK failed: err=%v results=%+v", err, results)
	}
	revertedManual, _ := os.ReadFile(filepath.Join(dir, "current"))
	if !bytes.Equal(revertedManual, v1Bin) {
		t.Fatal("manual rollback did not restore slot-a v1 binary")
	}
}

func TestOTAAgentUnknownKeyAndDeadlineTimeoutRollback(t *testing.T) {
	now := time.Unix(1_760_300_000, 0).UTC()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "release-2026"
	v1Bin := []byte("v1-bin")
	v2Bin := []byte("v2-bin-payload")
	pkgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(v2Bin)
	}))
	t.Cleanup(pkgSrv.Close)

	dir := t.TempDir()
	agent, err := NewOTAAgent(OTAAgentOptions{
		Dir:            dir,
		CurrentVersion: "1.0.0",
		HardwareModel:  "EG8200",
		TargetOS:       "linux",
		TargetArch:     "armv7l",
		TrustedKeys:    map[string]string{keyID: base64.StdEncoding.EncodeToString(pub)},
		HTTPClient:     pkgSrv.Client(),
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SeedSlotA(v1Bin); err != nil {
		t.Fatal(err)
	}

	// Unknown signingKeyId must reject without overwriting active slot
	unknownKeyStage := buildSignedStage(t, priv, "unknown-key-id", pkgSrv.URL+"/pkg", v2Bin, "1.3.0", edgecontrol.OTAActionApply)
	var results []edgecontrol.OTAResultPayload
	if err := agent.ApplyStage(t.Context(), unknownKeyStage, func(r edgecontrol.OTAResultPayload) {
		results = append(results, r)
	}); err == nil || results[len(results)-1].ErrorCode != "UNKNOWN_SIGNING_KEY" {
		t.Fatalf("expected UNKNOWN_SIGNING_KEY, got err=%v results=%+v", err, results)
	}

	// Apply valid v2, advance fake clock past healthCheckTimeoutSec -> VerifyPendingUpdateOnBoot rolls back automatically
	stage := buildSignedStage(t, priv, keyID, pkgSrv.URL+"/pkg", v2Bin, "1.3.0", edgecontrol.OTAActionApply)
	stage.HealthCheckTimeoutSec = 30
	if err := agent.ApplyStage(t.Context(), stage, nil); err != nil {
		t.Fatalf("ApplyStage: %v", err)
	}
	now = now.Add(31 * time.Second)

	var bootReports []edgecontrol.OTAResultPayload
	rolledBack, err := agent.VerifyPendingUpdateOnBoot(
		t.Context(),
		func(context.Context) error { t.Fatal("healthCheck should not run after deadline expired"); return nil },
		func(r edgecontrol.OTAResultPayload) { bootReports = append(bootReports, r) },
	)
	if err != nil || !rolledBack || bootReports[len(bootReports)-1].Stage != edgecontrol.OTAStageRolledBack {
		t.Fatalf("expected timeout auto-rollback, rolledBack=%v err=%v reports=%+v", rolledBack, err, bootReports)
	}
	active, _ := os.ReadFile(filepath.Join(dir, "current"))
	if !bytes.Equal(active, v1Bin) {
		t.Fatal("current binary did not revert on deadline expiration")
	}
}
