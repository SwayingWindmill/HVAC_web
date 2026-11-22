package connectivity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestOTAArtifactStorageSaveOpenDeleteAndLimits(t *testing.T) {
	t.Parallel()

	storage, err := NewOTAArtifactStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewOTAArtifactStorage: %v", err)
	}

	const tenantID = "0194f001-1000-7000-8000-000000000001"
	const pkgID = "0194f001-2000-7000-8000-000000000001"
	payload := []byte("hvac-edge-ota-binary-payload-0123456789abcdef")
	wantSum := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(wantSum[:])

	relPath, size, actualSHA, err := storage.SaveStream(tenantID, pkgID, bytes.NewReader(payload), 1024)
	if err != nil {
		t.Fatalf("SaveStream: %v", err)
	}
	if relPath != tenantID+"/"+pkgID+".bin" || size != int64(len(payload)) || actualSHA != wantHex {
		t.Fatalf("SaveStream = (%q, %d, %s), want (%q, %d, %s)", relPath, size, actualSHA, tenantID+"/"+pkgID+".bin", len(payload), wantHex)
	}

	f, info, err := storage.Open(relPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	gotBytes, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || info.Size() != int64(len(payload)) || !bytes.Equal(gotBytes, payload) {
		t.Fatalf("Open read mismatch: err=%v size=%d bytes=%q", err, info.Size(), gotBytes)
	}

	if err := storage.Delete(relPath); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := storage.Open(relPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open after Delete got %v, want ErrNotFound", err)
	}

	if _, _, _, err := storage.SaveStream(tenantID, "0194f001-2000-7000-8000-000000000002", bytes.NewReader(nil), 1024); !errors.Is(err, ErrOTAInvalid) {
		t.Fatalf("empty stream got %v, want ErrOTAInvalid", err)
	}

	const oversizedID = "0194f001-2000-7000-8000-000000000003"
	if _, _, _, err := storage.SaveStream(tenantID, oversizedID, bytes.NewReader(payload), 8); !errors.Is(err, ErrOTAPackageTooLarge) {
		t.Fatalf("oversized stream got %v, want ErrOTAPackageTooLarge", err)
	}
	if _, _, err := storage.Open(tenantID + "/" + oversizedID + ".bin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oversized partial artifact leaked to final path: %v", err)
	}

	for _, badRel := range []string{"", ".", "../escape.bin", "/etc/passwd", tenantID + "/../../evil.bin"} {
		if _, _, err := storage.Open(badRel); !errors.Is(err, ErrOTAInvalid) {
			t.Fatalf("Open(%q) got %v, want ErrOTAInvalid", badRel, err)
		}
		if err := storage.Delete(badRel); !errors.Is(err, ErrOTAInvalid) && badRel != "" {
			t.Fatalf("Delete(%q) got %v, want ErrOTAInvalid", badRel, err)
		}
	}
}

func TestOTASignerVerifierSignVerifyAndKeyParsing(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	const (
		keyID      = "platform-ota-v1"
		pkgID      = "0194f001-2000-7000-8000-000000000010"
		version    = "1.4.0"
		component  = edgecontrol.OTAComponentEdgeGateway
		targetOS   = "linux"
		targetArch = "armv7l"
		fileSize   = int64(4096)
	)
	shaHex := strings.Repeat("a", 64)

	signer, err := NewOTASignerVerifier(keyID, priv, map[string]string{
		"external-ci-key": base64.StdEncoding.EncodeToString(otherPub),
	})
	if err != nil {
		t.Fatalf("NewOTASignerVerifier: %v", err)
	}
	if !signer.CanServerSign() {
		t.Fatal("expected CanServerSign() == true")
	}

	gotKeyID, gotSig, err := signer.ResolveSignature(pkgID, version, component, targetOS, targetArch, shaHex, fileSize, "", "")
	if err != nil || gotKeyID != keyID {
		t.Fatalf("server ResolveSignature = (%q, %v)", gotKeyID, err)
	}
	if err := edgecontrol.VerifyOTASignatureEd25519(pub, pkgID, version, component, targetOS, targetArch, shaHex, fileSize, gotSig); err != nil {
		t.Fatalf("VerifyOTASignatureEd25519 failed: %v", err)
	}

	extSig, err := edgecontrol.SignOTAPackageEd25519(otherPriv, pkgID, version, component, targetOS, targetArch, shaHex, fileSize)
	if err != nil {
		t.Fatal(err)
	}
	resolvedExtID, resolvedExtSig, err := signer.ResolveSignature(pkgID, version, component, targetOS, targetArch, shaHex, fileSize, "external-ci-key", extSig)
	if err != nil || resolvedExtID != "external-ci-key" || resolvedExtSig != extSig {
		t.Fatalf("external ResolveSignature failed: id=%q err=%v", resolvedExtID, err)
	}

	// Tampered SHA, version, unknown key, and unconfigured server signer must fail closed.
	if _, _, err := signer.ResolveSignature(pkgID, version, component, targetOS, targetArch, strings.Repeat("b", 64), fileSize, "external-ci-key", extSig); !errors.Is(err, ErrOTAInvalid) {
		t.Fatalf("tampered sha got %v, want ErrOTAInvalid", err)
	}
	if _, _, err := signer.ResolveSignature(pkgID, "1.4.1", component, targetOS, targetArch, shaHex, fileSize, "external-ci-key", extSig); !errors.Is(err, ErrOTAInvalid) {
		t.Fatalf("tampered version got %v, want ErrOTAInvalid", err)
	}
	if _, _, err := signer.ResolveSignature(pkgID, version, component, targetOS, targetArch, shaHex, fileSize, "unknown-key", extSig); !errors.Is(err, ErrOTAInvalid) {
		t.Fatalf("unknown key got %v, want ErrOTAInvalid", err)
	}

	verifierOnly, err := NewOTASignerVerifier("", nil, map[string]string{
		"external-ci-key": base64.StdEncoding.EncodeToString(otherPub),
	})
	if err != nil || verifierOnly.CanServerSign() {
		t.Fatalf("verifierOnly unexpected: canSign=%v err=%v", verifierOnly.CanServerSign(), err)
	}
	if _, _, err := verifierOnly.ResolveSignature(pkgID, version, component, targetOS, targetArch, shaHex, fileSize, "", ""); !errors.Is(err, ErrOTAInvalid) {
		t.Fatalf("expected error when server signing key absent, got %v", err)
	}

	// Key parsing: PKCS#8 PEM, base64 64-byte private key, and base64 32-byte seed.
	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8DER})
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"pem-pkcs8", pemBytes},
		{"b64-full", []byte(base64.StdEncoding.EncodeToString(priv))},
		{"b64-seed", []byte(base64.StdEncoding.EncodeToString(priv.Seed()))},
	} {
		parsed, parseErr := parseEd25519PrivateKeyBytes(tc.raw)
		if parseErr != nil || !bytes.Equal(parsed, priv) {
			t.Fatalf("%s parse failed: err=%v equal=%v", tc.name, parseErr, bytes.Equal(parsed, priv))
		}
	}
	if _, err := parseEd25519PrivateKeyBytes([]byte("not-a-valid-key")); err == nil {
		t.Fatal("expected invalid private key error")
	}
}

func TestOTACanaryProgressAndCircuitBreakerStateMachine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		total, pct, want int
	}{
		{0, 10, 0},
		{1, 10, 1},
		{10, 1, 1},
		{10, 10, 1},
		{10, 15, 2},
		{10, 100, 10},
	} {
		if got := ComputeCanaryTargetCount(tc.total, tc.pct); got != tc.want {
			t.Fatalf("ComputeCanaryTargetCount(%d, %d) = %d, want %d", tc.total, tc.pct, got, tc.want)
		}
	}

	now := time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC)
	task := OTADeviceTask{
		ID:            "0194f001-4000-7000-8000-000000000001",
		CampaignID:    "0194f001-3000-7000-8000-000000000001",
		PackageID:     "0194f001-2000-7000-8000-000000000001",
		GatewayID:     "0194f001-5000-7000-8000-000000000001",
		Wave:          OTAWaveCanary,
		DesiredAction: edgecontrol.OTAActionApply,
		Stage:         OTATaskStageDispatched,
		TargetVersion: "1.4.0",
	}

	// Monotonic APPLY progression.
	steps := []struct {
		stage string
		pct   int
	}{
		{edgecontrol.OTAStageDownloading, 10},
		{edgecontrol.OTAStageDownloading, 55},
		{edgecontrol.OTAStageVerifying, 60},
		{edgecontrol.OTAStageApplying, 80},
		{edgecontrol.OTAStageHealthChecking, 95},
		{edgecontrol.OTAStageSucceeded, 100},
	}
	cur := task
	for i, step := range steps {
		next, updated, err := EvaluateOTATaskProgress(cur, edgecontrol.OTAResultPayload{
			CampaignID:     cur.CampaignID,
			TaskID:         cur.ID,
			PackageID:      cur.PackageID,
			Stage:          step.stage,
			ProgressPct:    step.pct,
			CurrentVersion: "1.3.0",
			TargetVersion:  "1.4.0",
			OccurredAt:     now.Add(time.Duration(i+1) * time.Second).UnixMilli(),
		}, now.Add(time.Duration(i+1)*time.Second))
		if err != nil || !updated || next.Stage != step.stage || next.ProgressPct != step.pct {
			t.Fatalf("step %s: updated=%v err=%v next=%+v", step.stage, updated, err, next)
		}
		if step.stage == edgecontrol.OTAStageDownloading {
			// Stale lower-pct update ignored.
			stale, staleUpdated, staleErr := EvaluateOTATaskProgress(next, edgecontrol.OTAResultPayload{
				CampaignID:     cur.CampaignID,
				TaskID:         cur.ID,
				PackageID:      cur.PackageID,
				Stage:          edgecontrol.OTAStageDownloading,
				ProgressPct:    step.pct,
				CurrentVersion: "1.3.0",
				TargetVersion:  "1.4.0",
				OccurredAt:     now.UnixMilli(),
			}, now)
			if staleErr != nil || staleUpdated || stale.ProgressPct != step.pct {
				t.Fatalf("stale update should be ignored: updated=%v err=%v", staleUpdated, staleErr)
			}
		}
		cur = next
	}
	if cur.FinishedAt == nil {
		t.Fatal("expected FinishedAt on terminal SUCCEEDED task")
	}

	// Terminal APPLY task ignores further APPLY reports.
	_, updatedAfterTerm, err := EvaluateOTATaskProgress(cur, edgecontrol.OTAResultPayload{
		CampaignID:     cur.CampaignID,
		TaskID:         cur.ID,
		PackageID:      cur.PackageID,
		Stage:          edgecontrol.OTAStageFailed,
		ProgressPct:    100,
		CurrentVersion: "1.3.0",
		TargetVersion:  "1.4.0",
		OccurredAt:     now.UnixMilli(),
	}, now)
	if err != nil || updatedAfterTerm {
		t.Fatalf("terminal task should ignore late report: updated=%v err=%v", updatedAfterTerm, err)
	}

	// Mismatched taskId must fail validation.
	if _, _, err := EvaluateOTATaskProgress(task, edgecontrol.OTAResultPayload{
		CampaignID:     task.CampaignID,
		TaskID:         "0194f001-4000-7000-8000-000000000999",
		PackageID:      task.PackageID,
		Stage:          edgecontrol.OTAStageDownloading,
		ProgressPct:    10,
		CurrentVersion: "1.3.0",
		TargetVersion:  "1.4.0",
		OccurredAt:     now.UnixMilli(),
	}, now); !errors.Is(err, ErrOTAInvalid) {
		t.Fatalf("mismatched taskId got %v, want ErrOTAInvalid", err)
	}

	// ROLLBACK action allows SUCCEEDED -> ROLLED_BACK.
	cur.DesiredAction = edgecontrol.OTAActionRollback
	rolledBack, updatedRB, err := EvaluateOTATaskProgress(cur, edgecontrol.OTAResultPayload{
		CampaignID:     cur.CampaignID,
		TaskID:         cur.ID,
		PackageID:      cur.PackageID,
		Stage:          edgecontrol.OTAStageRolledBack,
		ProgressPct:    100,
		CurrentVersion: "1.3.0",
		TargetVersion:  "1.4.0",
		OccurredAt:     now.Add(time.Minute).UnixMilli(),
	}, now.Add(time.Minute))
	if err != nil || !updatedRB || rolledBack.Stage != edgecontrol.OTAStageRolledBack {
		t.Fatalf("rollback transition failed: updated=%v err=%v task=%+v", updatedRB, err, rolledBack)
	}

	// EvaluateOTACampaignTransition cases.
	mkTask := func(id, wave, stage string) OTADeviceTask {
		return OTADeviceTask{ID: id, Wave: wave, DesiredAction: edgecontrol.OTAActionApply, Stage: stage}
	}

	autoDec := EvaluateOTACampaignTransition(OTACampaignStatusCanaryRunning, 30, true, []OTADeviceTask{
		mkTask("t1", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
		mkTask("t2", OTAWaveRollout, OTATaskStagePending),
		mkTask("t3", OTAWaveRollout, OTATaskStagePending),
	}, now)
	if !autoDec.Changed || autoDec.NewStatus != OTACampaignStatusRolloutRunning || len(autoDec.PromoteTaskIDs) != 2 {
		t.Fatalf("auto promote decision = %+v", autoDec)
	}

	manualDec := EvaluateOTACampaignTransition(OTACampaignStatusCanaryRunning, 30, false, []OTADeviceTask{
		mkTask("t1", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
		mkTask("t2", OTAWaveRollout, OTATaskStagePending),
	}, now)
	if !manualDec.Changed || manualDec.NewStatus != OTACampaignStatusPaused || manualDec.NewPauseReason == nil || *manualDec.NewPauseReason != OTAPauseReasonCanaryAwaitingPromote {
		t.Fatalf("manual promote pause decision = %+v", manualDec)
	}

	doneDec := EvaluateOTACampaignTransition(OTACampaignStatusCanaryRunning, 30, true, []OTADeviceTask{
		mkTask("t1", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
	}, now)
	if !doneDec.Changed || doneDec.NewStatus != OTACampaignStatusCompleted || doneDec.CompletedAt == nil {
		t.Fatalf("100%% canary complete decision = %+v", doneDec)
	}

	// Circuit breaker trips at threshold in canary wave (both FAILED and ROLLED_BACK count).
	tripCanary := EvaluateOTACampaignTransition(OTACampaignStatusCanaryRunning, 25, true, []OTADeviceTask{
		mkTask("t1", OTAWaveCanary, edgecontrol.OTAStageRolledBack),
		mkTask("t2", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
		mkTask("t3", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
		mkTask("t4", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
		mkTask("t5", OTAWaveRollout, OTATaskStagePending),
	}, now)
	if !tripCanary.Changed || tripCanary.NewStatus != OTACampaignStatusPaused || tripCanary.NewPauseReason == nil || *tripCanary.NewPauseReason != OTAPauseReasonFailureRateExceeded {
		t.Fatalf("canary circuit breaker decision = %+v", tripCanary)
	}

	// Strictly below threshold does NOT trip and still completes when all rollout tasks terminal.
	belowThreshold := EvaluateOTACampaignTransition(OTACampaignStatusRolloutRunning, 30, true, []OTADeviceTask{
		mkTask("t1", OTAWaveCanary, edgecontrol.OTAStageSucceeded),
		mkTask("t2", OTAWaveRollout, edgecontrol.OTAStageFailed),
		mkTask("t3", OTAWaveRollout, edgecontrol.OTAStageSucceeded),
		mkTask("t4", OTAWaveRollout, edgecontrol.OTAStageSucceeded),
		mkTask("t5", OTAWaveRollout, edgecontrol.OTAStageSucceeded),
	}, now)
	if !belowThreshold.Changed || belowThreshold.NewStatus != OTACampaignStatusCompleted {
		t.Fatalf("below threshold rollout completion = %+v", belowThreshold)
	}
}

type fakeMemoryOTAStore struct {
	mu           sync.Mutex
	packages     map[string]OTAPackage
	campaigns    map[string]OTACampaignDetail
	createPkgErr error
	gatewayID    string
	promoted     []OTAStageDispatch
	now          time.Time
}

func (f *fakeMemoryOTAStore) ListOTAPackages(_ context.Context, claims registryauth.GrantClaims, filter ListOTAPackagesFilter) ([]OTAPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []OTAPackage
	for _, pkg := range f.packages {
		if pkg.TenantID != claims.TenantID {
			continue
		}
		if filter.Component != "" && pkg.Component != strings.ToUpper(filter.Component) {
			continue
		}
		out = append(out, pkg)
	}
	return out, nil
}

func (f *fakeMemoryOTAStore) CreateOTAPackage(_ context.Context, claims registryauth.GrantClaims, input CreateOTAPackageInput) (OTAPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createPkgErr != nil {
		return OTAPackage{}, f.createPkgErr
	}
	norm, err := NormalizeOTAPackageInput(input)
	if err != nil {
		return OTAPackage{}, err
	}
	pkg := OTAPackage{
		ID:                   norm.PackageID,
		TenantID:             claims.TenantID,
		Component:            norm.Component,
		Version:              norm.Version,
		TargetOS:             norm.TargetOS,
		TargetArch:           norm.TargetArch,
		HardwareModels:       norm.HardwareModels,
		MinCompatibleVersion: norm.MinCompatibleVersion,
		FileName:             norm.FileName,
		FileSizeBytes:        norm.FileSizeBytes,
		SHA256:               norm.SHA256,
		SignatureAlg:         edgecontrol.OTASignatureAlgEd25519,
		SigningKeyID:         norm.SigningKeyID,
		SignatureB64:         norm.SignatureB64,
		StorageRelPath:       norm.StorageRelPath,
		ReleaseNotes:         norm.ReleaseNotes,
		Status:               OTAPackageStatusActive,
		UploadedBy:           claims.PrincipalID,
		CreatedAt:            f.now,
		UpdatedAt:            f.now,
	}
	f.packages[pkg.ID] = pkg
	return pkg, nil
}

func (f *fakeMemoryOTAStore) GetOTAPackageForGateway(_ context.Context, _ string, packageID string) (OTAPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pkg, ok := f.packages[strings.ToLower(packageID)]
	if !ok {
		return OTAPackage{}, ErrNotFound
	}
	return pkg, nil
}

func (f *fakeMemoryOTAStore) ListOTACampaigns(_ context.Context, claims registryauth.GrantClaims, _ ListOTACampaignsFilter) ([]OTACampaignSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []OTACampaignSummary
	for _, c := range f.campaigns {
		if c.TenantID == claims.TenantID {
			out = append(out, c.OTACampaignSummary)
		}
	}
	return out, nil
}

func (f *fakeMemoryOTAStore) GetOTACampaign(_ context.Context, claims registryauth.GrantClaims, campaignID string) (OTACampaignDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.campaigns[campaignID]
	if !ok || c.TenantID != claims.TenantID {
		return OTACampaignDetail{}, ErrNotFound
	}
	return c, nil
}

func (f *fakeMemoryOTAStore) CreateOTACampaign(_ context.Context, claims registryauth.GrantClaims, input CreateOTACampaignInput) (OTACampaignDetail, []OTAStageDispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pkg, ok := f.packages[input.PackageID]
	if !ok {
		return OTACampaignDetail{}, nil, ErrNotFound
	}
	const campID = "0194f001-3000-7000-8000-000000000001"
	const taskID = "0194f001-4000-7000-8000-000000000001"
	task := OTADeviceTask{
		ID:            taskID,
		TenantID:      claims.TenantID,
		CampaignID:    campID,
		PackageID:     pkg.ID,
		GatewayID:     f.gatewayID,
		SiteID:        "0194f001-0100-7000-8000-000000000001",
		GatewayCode:   "gw-1",
		GatewayName:   "Gateway 1",
		Wave:          OTAWaveCanary,
		DesiredAction: edgecontrol.OTAActionApply,
		Stage:         OTATaskStageDispatched,
		TargetVersion: pkg.Version,
		CreatedAt:     f.now,
		UpdatedAt:     f.now,
	}
	detail := OTACampaignDetail{
		OTACampaignSummary: OTACampaignSummary{
			ID:                      campID,
			TenantID:                claims.TenantID,
			Name:                    input.Name,
			PackageID:               pkg.ID,
			PackageVersion:          pkg.Version,
			Component:               pkg.Component,
			TargetOS:                pkg.TargetOS,
			TargetArch:              pkg.TargetArch,
			TargetSiteIDs:           []string{},
			TargetTags:              map[string]string{},
			CanaryPercent:           input.CanaryPercent,
			FailureRateThresholdPct: input.FailureRateThresholdPct,
			HealthCheckTimeoutSec:   DefaultOTAHealthCheckTimeoutSec,
			AutoPromoteRollout:      true,
			Status:                  OTACampaignStatusCanaryRunning,
			Reason:                  input.Reason,
			TotalCount:              1,
			InProgressCount:         1,
			CreatedBy:               claims.PrincipalID,
			UpdatedBy:               claims.PrincipalID,
			CreatedAt:               f.now,
			UpdatedAt:               f.now,
		},
		Package: pkg,
		Tasks:   []OTADeviceTask{task},
	}
	f.campaigns[campID] = detail
	return detail, BuildOTAStageDispatches(pkg, []OTADeviceTask{task}, DefaultOTAHealthCheckTimeoutSec, ""), nil
}

func (f *fakeMemoryOTAStore) PauseOTACampaign(_ context.Context, claims registryauth.GrantClaims, campaignID, _ string) (OTACampaignDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.campaigns[campaignID]
	reason := OTAPauseReasonManual
	c.Status = OTACampaignStatusPaused
	c.PauseReason = &reason
	c.TenantID = claims.TenantID
	f.campaigns[campaignID] = c
	return c, nil
}

func (f *fakeMemoryOTAStore) ResumeOTACampaign(_ context.Context, _ registryauth.GrantClaims, campaignID, _ string) (OTACampaignDetail, []OTAStageDispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.campaigns[campaignID]
	c.Status = OTACampaignStatusCanaryRunning
	c.PauseReason = nil
	f.campaigns[campaignID] = c
	return c, BuildOTAStageDispatches(c.Package, c.Tasks, DefaultOTAHealthCheckTimeoutSec, ""), nil
}

func (f *fakeMemoryOTAStore) RollbackOTACampaign(_ context.Context, _ registryauth.GrantClaims, campaignID, _ string) (OTACampaignDetail, []OTAStageDispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.campaigns[campaignID]
	c.Status = OTACampaignStatusRolledBack
	c.PauseReason = nil
	c.Tasks[0].DesiredAction = edgecontrol.OTAActionRollback
	f.campaigns[campaignID] = c
	return c, BuildOTAStageDispatches(c.Package, c.Tasks, DefaultOTAHealthCheckTimeoutSec, ""), nil
}

func (f *fakeMemoryOTAStore) GetGatewayDesiredOTAStage(_ context.Context, _ string) (*edgecontrol.OTAStagePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.campaigns {
		dispatches := BuildOTAStageDispatches(c.Package, c.Tasks, c.HealthCheckTimeoutSec, "")
		if len(dispatches) > 0 {
			s := dispatches[0].Stage
			return &s, nil
		}
	}
	return nil, nil
}

func (f *fakeMemoryOTAStore) ApplyGatewayOTAResult(_ context.Context, _ string, result edgecontrol.OTAResultPayload) (OTADeviceTask, []OTAStageDispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.campaigns[result.CampaignID]
	next, _, err := EvaluateOTATaskProgress(c.Tasks[0], result, f.now)
	if err != nil {
		return OTADeviceTask{}, nil, err
	}
	c.Tasks[0] = next
	f.campaigns[result.CampaignID] = c
	return next, f.promoted, nil
}

func buildMultipartOTAUpload(t *testing.T, fileBytes []byte, fields map[string]string) (string, *bytes.Buffer) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if fileBytes != nil {
		fw, err := mw.CreateFormFile("file", "edge-gateway-armv7l")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(fileBytes); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return mw.FormDataContentType(), &body
}

func makeGatewayEscapedCert(t *testing.T, gwID string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: gwID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return url.QueryEscape(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
}

func TestOTAHTTPHandlerOperatorAndGatewayEndpoints(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 5, 0, 0, 0, time.UTC)
	const (
		tenantID = "0194f001-1000-7000-8000-000000000001"
		actorID  = "0194f001-9000-7000-8000-000000000001"
		gwID     = "0194f001-5000-7000-8000-000000000001"
		pkgID    = "0194f001-2000-7000-8000-000000000099"
	)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	storageDir := t.TempDir()
	storage, err := NewOTAArtifactStorage(storageDir)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewOTASignerVerifier("platform-ota-v1", priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeMemoryOTAStore{
		packages:  map[string]OTAPackage{},
		campaigns: map[string]OTACampaignDetail{},
		gatewayID: gwID,
		now:       now,
	}

	var authErr error
	var dispatched [][]OTAStageDispatch
	var seenGateways []string
	otaHandler := NewOTAHandler(
		store,
		storage,
		signer,
		fakePeerVerifier{gatewayID: gwID},
		func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
			if authErr != nil {
				return registryauth.GrantClaims{}, authErr
			}
			return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: actorID}, nil
		},
		OTAHandlerOptions{
			PublicBaseURL:   "https://edge.hvac.example.test",
			MaxPackageBytes: 1024,
			Now:             func() time.Time { return now },
			OnDispatch:      func(items []OTAStageDispatch) { dispatched = append(dispatched, items) },
			OnGatewaySeen:   func(id string) { seenGateways = append(seenGateways, id) },
			IsGatewayOnline: func(id string) bool { return id == gwID },
		},
	)

	binPayload := []byte("0123456789ABCDEFedge-ota-v1.5.0-binary-content")
	sum := sha256.Sum256(binPayload)
	shaHex := hex.EncodeToString(sum[:])

	// 1. Checksum mismatch cleans up disk file and returns 422 OTA_CHECKSUM_MISMATCH.
	ctMismatch, bodyMismatch := buildMultipartOTAUpload(t, binPayload, map[string]string{
		"version":        "1.5.0",
		"targetArch":     "armv7l",
		"expectedSha256": strings.Repeat("0", 64),
	})
	recMismatch := httptest.NewRecorder()
	reqMismatch := httptest.NewRequest(http.MethodPost, "/internal/v1/ota/packages", bodyMismatch)
	reqMismatch.Header.Set("Content-Type", ctMismatch)
	otaHandler.ServeHTTP(recMismatch, reqMismatch)
	if recMismatch.Code != http.StatusUnprocessableEntity || !strings.Contains(recMismatch.Body.String(), "OTA_CHECKSUM_MISMATCH") {
		t.Fatalf("mismatch upload = %d %s", recMismatch.Code, recMismatch.Body.String())
	}

	// 2. Oversized upload returns 413 OTA_PACKAGE_TOO_LARGE.
	ctOver, bodyOver := buildMultipartOTAUpload(t, bytes.Repeat([]byte("X"), 1200), map[string]string{
		"version":    "1.5.0",
		"targetArch": "armv7l",
	})
	recOver := httptest.NewRecorder()
	reqOver := httptest.NewRequest(http.MethodPost, "/internal/v1/ota/packages", bodyOver)
	reqOver.Header.Set("Content-Type", ctOver)
	otaHandler.ServeHTTP(recOver, reqOver)
	if recOver.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recOver.Body.String(), "OTA_PACKAGE_TOO_LARGE") {
		t.Fatalf("oversized upload = %d %s", recOver.Code, recOver.Body.String())
	}

	// 3. Conflict on store persistence returns 409 and removes stored file.
	store.createPkgErr = ErrOTAConflict
	ctConflict, bodyConflict := buildMultipartOTAUpload(t, binPayload, map[string]string{
		"packageId":  pkgID,
		"version":    "1.5.0",
		"targetArch": "arm",
	})
	recConflict := httptest.NewRecorder()
	reqConflict := httptest.NewRequest(http.MethodPost, "/internal/v1/ota/packages", bodyConflict)
	reqConflict.Header.Set("Content-Type", ctConflict)
	otaHandler.ServeHTTP(recConflict, reqConflict)
	if recConflict.Code != http.StatusConflict {
		t.Fatalf("conflict upload = %d %s", recConflict.Code, recConflict.Body.String())
	}
	store.createPkgErr = nil

	// 4. Successful multipart upload (201 Created) + list (200 OK).
	ctOK, bodyOK := buildMultipartOTAUpload(t, binPayload, map[string]string{
		"packageId":      pkgID,
		"component":      "EDGE_GATEWAY",
		"version":        "1.5.0",
		"targetOs":       "linux",
		"targetArch":     "arm",
		"hardwareModels": "EG8200, EG8200-Pro",
		"expectedSha256": shaHex,
		"releaseNotes":   "First OTA release",
	})
	recOK := httptest.NewRecorder()
	reqOK := httptest.NewRequest(http.MethodPost, "/internal/v1/ota/packages", bodyOK)
	reqOK.Header.Set("Content-Type", ctOK)
	otaHandler.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusCreated {
		t.Fatalf("upload 201 = %d %s", recOK.Code, recOK.Body.String())
	}
	var createdPkg OTAPackage
	if err := json.Unmarshal(recOK.Body.Bytes(), &createdPkg); err != nil {
		t.Fatal(err)
	}
	if createdPkg.TargetArch != "armv7l" || createdPkg.SHA256 != shaHex || len(createdPkg.HardwareModels) != 2 {
		t.Fatalf("unexpected createdPkg: %+v", createdPkg)
	}

	recList := httptest.NewRecorder()
	otaHandler.ServeHTTP(recList, httptest.NewRequest(http.MethodGet, "/internal/v1/ota/packages?limit=10", nil))
	if recList.Code != http.StatusOK || !strings.Contains(recList.Body.String(), pkgID) {
		t.Fatalf("list packages = %d %s", recList.Code, recList.Body.String())
	}

	// 5. Campaign lifecycle: create (dispatches), get (online enriched), pause, resume, rollback.
	campBody := fmt.Sprintf(`{"name":"Canary 1.5.0","packageId":%q,"canaryPercent":20,"failureRateThresholdPct":50,"reason":"rollout 1.5.0"}`, pkgID)
	recCamp := httptest.NewRecorder()
	otaHandler.ServeHTTP(recCamp, httptest.NewRequest(http.MethodPost, "/internal/v1/ota/campaigns", strings.NewReader(campBody)))
	if recCamp.Code != http.StatusCreated || len(dispatched) != 1 || dispatched[0][0].Stage.DownloadURL != "https://edge.hvac.example.test/gateway/v1/ota/packages/"+pkgID+"/download" {
		t.Fatalf("create campaign = %d dispatched=%+v body=%s", recCamp.Code, dispatched, recCamp.Body.String())
	}
	var campDetail OTACampaignDetail
	if err := json.Unmarshal(recCamp.Body.Bytes(), &campDetail); err != nil || !campDetail.Tasks[0].GatewayOnline {
		t.Fatalf("campaign detail online enrich failed: %+v err=%v", campDetail, err)
	}

	for _, action := range []string{"pause", "resume", "rollback"} {
		recAct := httptest.NewRecorder()
		otaHandler.ServeHTTP(recAct, httptest.NewRequest(http.MethodPost, "/internal/v1/ota/campaigns/"+campDetail.ID+"/"+action, strings.NewReader(`{"reason":"operator test"}`)))
		if recAct.Code != http.StatusOK {
			t.Fatalf("%s campaign = %d %s", action, recAct.Code, recAct.Body.String())
		}
	}
	if len(dispatched) != 3 {
		t.Fatalf("expected dispatches on create, resume, and rollback, got %d", len(dispatched))
	}

	// 6. Unauthorized caller returns 403.
	authErr = ErrCredentialForbidden
	recDenied := httptest.NewRecorder()
	otaHandler.ServeHTTP(recDenied, httptest.NewRequest(http.MethodGet, "/internal/v1/ota/campaigns", nil))
	if recDenied.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when authorizer rejects, got %d", recDenied.Code)
	}
	authErr = nil

	// 7. Gateway mTLS download: 200 OK, Range 206, Range 416, desired, report.
	escapedCert := makeGatewayEscapedCert(t, gwID)

	dlFullReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/ota/packages/"+pkgID+"/download", nil)
	dlFullReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	dlFullRec := httptest.NewRecorder()
	otaHandler.ServeHTTP(dlFullRec, dlFullReq)
	if dlFullRec.Code != http.StatusOK || !bytes.Equal(dlFullRec.Body.Bytes(), binPayload) ||
		dlFullRec.Header().Get("X-OTA-SHA256") != shaHex || dlFullRec.Header().Get("X-OTA-Signature") != createdPkg.SignatureB64 {
		t.Fatalf("full download = %d headers=%v body=%q", dlFullRec.Code, dlFullRec.Header(), dlFullRec.Body.String())
	}

	dlRangeReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/ota/packages/"+pkgID+"/download", nil)
	dlRangeReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	dlRangeReq.Header.Set("Range", "bytes=8-15")
	dlRangeRec := httptest.NewRecorder()
	otaHandler.ServeHTTP(dlRangeRec, dlRangeReq)
	if dlRangeRec.Code != http.StatusPartialContent || !bytes.Equal(dlRangeRec.Body.Bytes(), binPayload[8:16]) ||
		dlRangeRec.Header().Get("Content-Range") != fmt.Sprintf("bytes 8-15/%d", len(binPayload)) {
		t.Fatalf("range download = %d content-range=%q body=%q", dlRangeRec.Code, dlRangeRec.Header().Get("Content-Range"), dlRangeRec.Body.String())
	}

	dlBadRangeReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/ota/packages/"+pkgID+"/download", nil)
	dlBadRangeReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	dlBadRangeReq.Header.Set("Range", "bytes=99999-")
	dlBadRangeRec := httptest.NewRecorder()
	otaHandler.ServeHTTP(dlBadRangeRec, dlBadRangeReq)
	if dlBadRangeRec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range = %d", dlBadRangeRec.Code)
	}

	dlMissingReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/ota/packages/0194f001-2000-7000-8000-000000000404/download", nil)
	dlMissingReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	dlMissingRec := httptest.NewRecorder()
	otaHandler.ServeHTTP(dlMissingRec, dlMissingReq)
	if dlMissingRec.Code != http.StatusNotFound {
		t.Fatalf("missing package download = %d", dlMissingRec.Code)
	}

	desiredReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/ota/desired", nil)
	desiredReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	desiredRec := httptest.NewRecorder()
	otaHandler.ServeHTTP(desiredRec, desiredReq)
	if desiredRec.Code != http.StatusOK || !strings.Contains(desiredRec.Body.String(), "https://edge.hvac.example.test/gateway/v1/ota/packages/"+pkgID+"/download") {
		t.Fatalf("desired stage = %d %s", desiredRec.Code, desiredRec.Body.String())
	}

	// Mismatched taskId in path vs body -> 400.
	badRepReq := httptest.NewRequest(http.MethodPost, "/gateway/v1/ota/tasks/"+campDetail.Tasks[0].ID+"/report", strings.NewReader(`{"campaignId":"`+campDetail.ID+`","taskId":"0194f001-4000-7000-8000-000000000999","packageId":"`+pkgID+`","stage":"DOWNLOADING","progressPct":10,"targetVersion":"1.5.0","occurredAt":1760000000000}`))
	badRepReq.Header.Set("X-Gateway-Client-Cert", escapedCert)
	badRepRec := httptest.NewRecorder()
	otaHandler.ServeHTTP(badRepRec, badRepReq)
	if badRepRec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched report taskId = %d %s", badRepRec.Code, badRepRec.Body.String())
	}

	if len(seenGateways) == 0 {
		t.Fatal("expected OnGatewaySeen callback to be invoked")
	}
}
