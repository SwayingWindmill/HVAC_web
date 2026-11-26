package edgedaemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
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

func TestOTAAgentEarlyFailureRollbackAndPendingReportFlush(t *testing.T) {
	now := time.Unix(1_760_400_000, 0).UTC()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "release-2026"
	v1Bin := []byte("#!/bin/sh\necho v1.0.0\n")
	v2Bin := []byte("#!/bin/sh\necho v1.4.0\n")
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

	stage := buildSignedStage(t, priv, keyID, pkgSrv.URL+"/pkg", v2Bin, "1.4.0", edgecontrol.OTAActionApply)
	if err := agent.ApplyStage(t.Context(), stage, nil); err != nil {
		t.Fatalf("ApplyStage: %v", err)
	}

	rolledBack, err := RollbackPendingOTAOnEarlyFailure(dir, "config decode error")
	if err != nil || !rolledBack {
		t.Fatalf("RollbackPendingOTAOnEarlyFailure = (%v, %v), want (true, nil)", rolledBack, err)
	}
	active, _ := os.ReadFile(filepath.Join(dir, "current"))
	if !bytes.Equal(active, v1Bin) {
		t.Fatal("current symlink did not revert to slot-a")
	}

	// Second call is a no-op once already rolled back.
	rolledBackAgain, err := RollbackPendingOTAOnEarlyFailure(dir, "ignored")
	if err != nil || rolledBackAgain {
		t.Fatalf("second RollbackPendingOTAOnEarlyFailure = (%v, %v), want (false, nil)", rolledBackAgain, err)
	}

	// Next boot on slot-a flushes pending ROLLED_BACK report once and keeps slot-a running (rolledBack=false).
	var flushed []edgecontrol.OTAResultPayload
	needRestart, err := agent.VerifyPendingUpdateOnBoot(
		t.Context(),
		func(context.Context) error { return nil },
		func(r edgecontrol.OTAResultPayload) { flushed = append(flushed, r) },
	)
	if err != nil || needRestart {
		t.Fatalf("VerifyPendingUpdateOnBoot after early rollback = (%v, %v), want (false, nil)", needRestart, err)
	}
	if len(flushed) != 1 || flushed[0].Stage != edgecontrol.OTAStageRolledBack || flushed[0].TargetVersion != "1.4.0" {
		t.Fatalf("expected 1 flushed ROLLED_BACK report, got %+v", flushed)
	}
	// Verify pendingRollbackReport is cleared after first flush.
	flushed = nil
	if needRestart, err := agent.VerifyPendingUpdateOnBoot(t.Context(), nil, func(r edgecontrol.OTAResultPayload) { flushed = append(flushed, r) }); err != nil || needRestart || len(flushed) != 0 {
		t.Fatalf("expected no duplicate report on subsequent boot, got needRestart=%v err=%v flushed=%+v", needRestart, err, flushed)
	}
}

type pollDesiredTestStore struct {
	e2eOTAStore
	desired *edgecontrol.OTAStagePayload
}

func (s *pollDesiredTestStore) GetGatewayDesiredOTAStage(_ context.Context, _ string) (*edgecontrol.OTAStagePayload, error) {
	return s.desired, nil
}

func TestOTAAgentPollDesiredWithMTLSAndPoP(t *testing.T) {
	now := time.Now().UTC()
	pkiDir := t.TempDir()
	pki := newTestPKIFixture(t, pkiDir, now)

	const (
		tenantID = "0194f030-1000-7000-8000-000000000001"
		siteID   = "0194f030-2000-7000-8000-000000000001"
		gwID     = "0194f030-5000-7000-8000-000000000099"
		actorID  = "0194f030-9000-7000-8000-000000000001"
		keyID    = "platform-ota-v1"
	)

	srvCertFile, srvKeyFile, _, _ := pki.issueCertFiles(t, pkiDir, "server", "127.0.0.1", 10, now.Add(-time.Hour), now.Add(24*time.Hour), true)
	srvPair, err := tls.LoadX509KeyPair(srvCertFile, srvKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	gwCertFile, gwKeyFile, _, _ := pki.issueCertFiles(t, pkiDir, "gateway", gwID, 11, now.Add(-time.Hour), now.Add(24*time.Hour), false)
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(pki.caPEM) {
		t.Fatal("append CA PEM")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := connectivity.NewOTAArtifactStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signer, err := connectivity.NewOTASignerVerifier(keyID, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	desiredStage := buildSignedStage(t, priv, keyID, "https://127.0.0.1/placeholder", []byte("pkg-bytes"), "2.0.0", edgecontrol.OTAActionApply)
	store := &pollDesiredTestStore{
		e2eOTAStore: e2eOTAStore{
			tenantID:  tenantID,
			siteID:    siteID,
			gatewayID: gwID,
			actorID:   actorID,
			packages:  map[string]connectivity.OTAPackage{},
			campaigns: map[string]connectivity.OTACampaignDetail{},
		},
		desired: &desiredStage,
	}

	otaHandler := connectivity.NewOTAHandler(
		store,
		storage,
		signer,
		e2ePeerVerifier{gatewayID: gwID},
		func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
			return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: actorID, AllowedSiteIDs: []string{siteID}}, nil
		},
		connectivity.OTAHandlerOptions{PublicBaseURL: "https://127.0.0.1"},
	)

	var sawPoP bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.TLS.PeerCertificates[0].Raw})
			r.Header.Set("X-Gateway-Client-Cert", url.QueryEscape(string(pemBytes)))
		}
		if r.Header.Get(edgecontrol.HeaderGatewayCertPopSignature) != "" {
			sawPoP = true
		}
		otaHandler.ServeHTTP(w, r)
	}))
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{srvPair},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	agent, err := NewOTAAgent(OTAAgentOptions{
		Dir:             t.TempDir(),
		CurrentVersion:  "1.0.0",
		HardwareModel:   "EG8200",
		TargetOS:        "linux",
		TargetArch:      "armv7l",
		PlatformBaseURL: srv.URL,
		ServerName:      "127.0.0.1",
		CAFile:          pki.caFile,
		CertFile:        gwCertFile,
		KeyFile:         gwKeyFile,
		TrustedKeys:     map[string]string{keyID: base64.StdEncoding.EncodeToString(pub)},
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := agent.PollDesired(t.Context())
	if err != nil {
		t.Fatalf("PollDesired failed: %v", err)
	}
	if !sawPoP {
		t.Fatal("expected PollDesired to sign HTTP request with PoP header")
	}
	if got == nil || got.TaskID != desiredStage.TaskID || got.PackageID != desiredStage.PackageID {
		t.Fatalf("unexpected desired stage: %+v", got)
	}
	if !strings.HasSuffix(got.DownloadURL, "/gateway/v1/ota/packages/"+desiredStage.PackageID+"/download") {
		t.Fatalf("unexpected resolved downloadUrl: %q", got.DownloadURL)
	}

	store.desired = nil
	gotEmpty, err := agent.PollDesired(t.Context())
	if err != nil || gotEmpty != nil {
		t.Fatalf("expected nil desired stage when none pending, got stage=%+v err=%v", gotEmpty, err)
	}
}

func TestSystemdNotifyUnixgramSocketAndNoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unixgram sockets require POSIX environment")
	}
	if err := notifySystemdSocket("", "READY=1"); err != nil {
		t.Fatalf("expected unset NOTIFY_SOCKET to be no-op, got %v", err)
	}

	sockPath := filepath.Join(t.TempDir(), "notify.sock")
	addr := &net.UnixAddr{Name: sockPath, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := notifySystemdSocket(sockPath, "READY=1"); err != nil {
		t.Fatalf("notifySystemdSocket READY=1: %v", err)
	}
	if err := notifySystemdSocket(sockPath, "WATCHDOG=1"); err != nil {
		t.Fatalf("notifySystemdSocket WATCHDOG=1: %v", err)
	}

	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n1, _, err := conn.ReadFrom(buf)
	if err != nil || string(buf[:n1]) != "READY=1" {
		t.Fatalf("first notify = (%q, %v), want READY=1", string(buf[:n1]), err)
	}
	n2, _, err := conn.ReadFrom(buf)
	if err != nil || string(buf[:n2]) != "WATCHDOG=1" {
		t.Fatalf("second notify = (%q, %v), want WATCHDOG=1", string(buf[:n2]), err)
	}
}

func TestOTABootGuardScriptRollsBackOnSecondFailedAttemptWithoutJQOrPython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash boot guard test runs on Linux/WSL")
	}

	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "deploy", "edge-gateway", "ota-boot-guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	otaDir := filepath.Join(baseDir, "ota")
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

	state := OTAState{
		CurrentVersion:  "1.0.0",
		PreviousVersion: "1.0.0",
		ActiveSlot:      "slot-b",
		PreviousSlot:    "slot-a",
		Status:          edgecontrol.OTAStageHealthChecking,
		PendingStage: &edgecontrol.OTAStagePayload{
			CampaignID:            "0193f000-2000-7000-8000-000000000102",
			TaskID:                "0193f000-2000-7000-8000-000000000103",
			PackageID:             "0193f000-2000-7000-8000-000000000101",
			Version:               "1.5.0",
			Component:             edgecontrol.OTAComponentEdgeGateway,
			TargetOS:              "linux",
			TargetArch:            "armv7l",
			DownloadURL:           "https://platform.example.com/pkg",
			FileSizeBytes:         2,
			SHA256:                strings.Repeat("b", 64),
			SignatureAlg:          edgecontrol.OTASignatureAlgEd25519,
			SignatureB64:          "c2ln",
			SigningKeyID:          "release-2026",
			HealthCheckTimeoutSec: 60,
			Action:                edgecontrol.OTAActionApply,
		},
		HealthDeadlineUnixMs: 1_900_000_000_000,
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(otaDir, "ota-state.json")
	if err := os.WriteFile(statePath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	// Build a minimal PATH directory containing only standard coreutils/awk (explicitly excluding jq & python3).
	binDir := filepath.Join(baseDir, "min-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"bash", "awk", "date", "readlink", "ln", "mv", "rm", "chmod"} {
		resolved, lookErr := exec.LookPath(tool)
		if lookErr != nil {
			t.Fatalf("LookPath(%s): %v", tool, lookErr)
		}
		if err := os.Symlink(resolved, filepath.Join(binDir, tool)); err != nil {
			t.Fatal(err)
		}
	}

	runGuard := func() {
		t.Helper()
		cmd := exec.Command(filepath.Join(binDir, "bash"), scriptPath, "pre-start")
		cmd.Env = []string{
			"PATH=" + binDir,
			"BASE_DIR=" + baseDir,
			"OTA_DIR=" + otaDir,
		}
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("ota-boot-guard.sh failed: %v (output=%s)", runErr, string(out))
		}
	}

	readState := func() OTAState {
		t.Helper()
		b, readErr := os.ReadFile(statePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var st OTAState
		if uErr := json.Unmarshal(b, &st); uErr != nil {
			t.Fatalf("decode state JSON %s: %v", string(b), uErr)
		}
		return st
	}

	// Attempt 1: increments bootAttempts 0 -> 1, keeps slot-b active.
	runGuard()
	st1 := readState()
	if st1.BootAttempts != 1 || st1.Status != edgecontrol.OTAStageHealthChecking {
		t.Fatalf("after 1st pre-start got %+v", st1)
	}
	if tgt, _ := os.Readlink(filepath.Join(otaDir, "current")); tgt != filepath.Join("slots", "slot-b") {
		t.Fatalf("after 1st pre-start expected slots/slot-b, got %q", tgt)
	}

	// Attempt 2: increments bootAttempts 1 -> 2, keeps slot-b active.
	runGuard()
	st2 := readState()
	if st2.BootAttempts != 2 || st2.Status != edgecontrol.OTAStageHealthChecking {
		t.Fatalf("after 2nd pre-start got %+v", st2)
	}
	if tgt, _ := os.Readlink(filepath.Join(otaDir, "current")); tgt != filepath.Join("slots", "slot-b") {
		t.Fatalf("after 2nd pre-start expected slots/slot-b, got %q", tgt)
	}

	// After 2 failed boots, next pre-start sees bootAttempts >= 2 and rolls back slot-b -> slot-a!
	runGuard()
	st3 := readState()
	if st3.Status != edgecontrol.OTAStageRolledBack || st3.ActiveSlot != "slot-a" || st3.BootAttempts != 0 {
		t.Fatalf("after 3rd pre-start expected ROLLED_BACK on slot-a, got %+v", st3)
	}
	if st3.PendingRollbackReport == nil || st3.PendingRollbackReport.Stage != edgecontrol.OTAStageRolledBack || st3.PendingRollbackReport.TargetVersion != "1.5.0" {
		t.Fatalf("expected pendingRollbackReport populated by boot guard, got %+v", st3.PendingRollbackReport)
	}
	if tgt, _ := os.Readlink(filepath.Join(otaDir, "current")); tgt != filepath.Join("slots", "slot-a") {
		t.Fatalf("expected current symlink reverted to slots/slot-a, got %q", tgt)
	}

	// Verify OTAAgent on booted slot-a flushes the boot-guard rollback report cleanly.
	agent, err := NewOTAAgent(OTAAgentOptions{
		Dir:            otaDir,
		CurrentVersion: "1.0.0",
		HardwareModel:  "EG8200",
		TargetOS:       "linux",
		TargetArch:     "armv7l",
	})
	if err != nil {
		t.Fatal(err)
	}
	var reports []edgecontrol.OTAResultPayload
	needRestart, err := agent.VerifyPendingUpdateOnBoot(
		t.Context(),
		func(context.Context) error { return nil },
		func(r edgecontrol.OTAResultPayload) { reports = append(reports, r) },
	)
	if err != nil || needRestart || len(reports) != 1 || reports[0].Stage != edgecontrol.OTAStageRolledBack {
		t.Fatalf("VerifyPendingUpdateOnBoot after boot guard rollback = (%v, %v) reports=%+v", needRestart, err, reports)
	}
}

func TestDaemonRunSystemdNotifyAndOTAPollLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unixgram socket and symlink OTA test runs on Linux/WSL")
	}

	now := time.Now().UTC()
	pkiDir := t.TempDir()
	pki := newTestPKIFixture(t, pkiDir, now)

	const (
		gwID  = "0194f030-5000-7000-8000-000000000199"
		keyID = "platform-ota-v1"
	)

	srvCertFile, srvKeyFile, _, _ := pki.issueCertFiles(t, pkiDir, "server", "127.0.0.1", 10, now.Add(-time.Hour), now.Add(24*time.Hour), true)
	srvPair, err := tls.LoadX509KeyPair(srvCertFile, srvKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	gwCertFile, gwKeyFile, _, _ := pki.issueCertFiles(t, pkiDir, "gateway", gwID, 11, now.Add(-time.Hour), now.Add(24*time.Hour), false)
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(pki.caPEM) {
		t.Fatal("append CA PEM")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	v1Bin := []byte("#!/bin/sh\necho v1.0.0\n")
	v2Bin := []byte("#!/bin/sh\necho v2.5.0-polled-ota\n")

	var srvURL string
	var stage edgecontrol.OTAStagePayload
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gateway/v1/ota/desired":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"gatewayId": gwID,
				"stage":     stage,
			})
		case "/pkg":
			_, _ = w.Write(v2Bin)
		case "/gateway/v1/config-shadow":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"gatewayId":"` + gwID + `","desiredRevision":0,"hasDelta":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{srvPair},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	stage = buildSignedStage(t, priv, keyID, srvURL+"/pkg", v2Bin, "2.5.0", edgecontrol.OTAActionApply)

	sockPath := filepath.Join(t.TempDir(), "notify.sock")
	notifyConn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sockPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = notifyConn.Close() }()
	t.Setenv("NOTIFY_SOCKET", sockPath)

	var (
		msgsMu              sync.Mutex
		msgs                []string
		sawReadyFlag        bool
		sawWatchdogFlag     bool
		readyAndWatchdogCh  = make(chan struct{})
		notifyConditionOnce sync.Once
	)
	stopNotifyReader := make(chan struct{})
	go func() {
		buf := make([]byte, 128)
		for {
			_ = notifyConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, readErr := notifyConn.ReadFrom(buf)
			if readErr == nil && n > 0 {
				msg := string(buf[:n])
				msgsMu.Lock()
				msgs = append(msgs, msg)
				if msg == "READY=1" {
					sawReadyFlag = true
				}
				if msg == "WATCHDOG=1" {
					sawWatchdogFlag = true
				}
				if sawReadyFlag && sawWatchdogFlag {
					notifyConditionOnce.Do(func() { close(readyAndWatchdogCh) })
				}
				msgsMu.Unlock()
			}
			select {
			case <-stopNotifyReader:
				return
			default:
			}
		}
	}()
	defer close(stopNotifyReader)

	stateDir := t.TempDir()
	otaDir := filepath.Join(stateDir, "ota")
	cfg := Config{
		GatewayID:                 gwID,
		TargetOS:                  "linux",
		TargetArch:                "armv7l",
		StateDir:                  stateDir,
		PlatformBaseURL:           srvURL,
		ServerName:                "127.0.0.1",
		CAFile:                    pki.caFile,
		CertFile:                  gwCertFile,
		KeyFile:                   gwKeyFile,
		CycleIntervalMS:           200,
		ShadowPollIntervalSec:     60,
		CertRenewCheckIntervalSec: 43200,
		OTA: OTAConfig{
			Enabled:           true,
			Dir:               otaDir,
			PollIntervalSec:   1,
			TrustedPublicKeys: map[string]string{keyID: base64.StdEncoding.EncodeToString(pub)},
		},
	}
	cfg.ApplyDefaults("1.0.0")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	switched := make(chan struct{}, 1)
	daemon, err := NewDaemon(cfg, DaemonOptions{
		ProbeBinary: func(context.Context, string) error { return nil },
		OnOTASwitchBoot: func() {
			select {
			case switched <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	if err := daemon.OTAAgent().SeedSlotA(v1Bin); err != nil {
		t.Fatalf("SeedSlotA: %v", err)
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- daemon.Run(ctx)
	}()

	select {
	case <-switched:
	case <-time.After(4 * time.Second):
		cancel()
		t.Fatal("timed out waiting for runOTAPollLoop to apply OTA stage and trigger OnOTASwitchBoot")
	}

	select {
	case <-readyAndWatchdogCh:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for READY=1 and WATCHDOG=1 on NOTIFY_SOCKET")
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Daemon.Run: %v", err)
	}

	msgsMu.Lock()
	gotMsgs := append([]string(nil), msgs...)
	msgsMu.Unlock()
	var sawReady, sawWatchdog bool
	for _, m := range gotMsgs {
		if m == "READY=1" {
			sawReady = true
		}
		if m == "WATCHDOG=1" {
			sawWatchdog = true
		}
	}
	if !sawReady || !sawWatchdog {
		t.Fatalf("expected READY=1 and WATCHDOG=1 on NOTIFY_SOCKET, got %v", gotMsgs)
	}
	if active, readErr := os.ReadFile(filepath.Join(otaDir, "current")); readErr != nil || !bytes.Equal(active, v2Bin) {
		t.Fatalf("expected polled OTA to switch current symlink to v2 binary, err=%v", readErr)
	}
}
