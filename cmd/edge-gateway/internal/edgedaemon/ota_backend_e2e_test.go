package edgedaemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

type e2eOTAStore struct {
	mu           sync.Mutex
	tenantID     string
	siteID       string
	gatewayID    string
	actorID      string
	packages     map[string]connectivity.OTAPackage
	campaigns    map[string]connectivity.OTACampaignDetail
	nextCampaign int
	nextTask     int
}

func (s *e2eOTAStore) ListOTAPackages(_ context.Context, claims registryauth.GrantClaims, _ connectivity.ListOTAPackagesFilter) ([]connectivity.OTAPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []connectivity.OTAPackage
	for _, pkg := range s.packages {
		if pkg.TenantID == claims.TenantID {
			out = append(out, pkg)
		}
	}
	return out, nil
}

func (s *e2eOTAStore) CreateOTAPackage(_ context.Context, claims registryauth.GrantClaims, input connectivity.CreateOTAPackageInput) (connectivity.OTAPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	norm, err := connectivity.NormalizeOTAPackageInput(input)
	if err != nil {
		return connectivity.OTAPackage{}, err
	}
	now := time.Now().UTC()
	pkg := connectivity.OTAPackage{
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
		Status:               connectivity.OTAPackageStatusActive,
		UploadedBy:           claims.PrincipalID,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	s.packages[pkg.ID] = pkg
	return pkg, nil
}

func (s *e2eOTAStore) GetOTAPackageForGateway(_ context.Context, _ string, packageID string) (connectivity.OTAPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pkg, ok := s.packages[packageID]
	if !ok {
		return connectivity.OTAPackage{}, connectivity.ErrNotFound
	}
	return pkg, nil
}

func (s *e2eOTAStore) ListOTACampaigns(_ context.Context, claims registryauth.GrantClaims, _ connectivity.ListOTACampaignsFilter) ([]connectivity.OTACampaignSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []connectivity.OTACampaignSummary
	for _, c := range s.campaigns {
		if c.TenantID == claims.TenantID {
			out = append(out, c.OTACampaignSummary)
		}
	}
	return out, nil
}

func (s *e2eOTAStore) GetOTACampaign(_ context.Context, claims registryauth.GrantClaims, campaignID string) (connectivity.OTACampaignDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[campaignID]
	if !ok || c.TenantID != claims.TenantID {
		return connectivity.OTACampaignDetail{}, connectivity.ErrNotFound
	}
	return c, nil
}

func (s *e2eOTAStore) CreateOTACampaign(_ context.Context, claims registryauth.GrantClaims, input connectivity.CreateOTACampaignInput) (connectivity.OTACampaignDetail, []connectivity.OTAStageDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pkg, ok := s.packages[input.PackageID]
	if !ok {
		return connectivity.OTACampaignDetail{}, nil, connectivity.ErrNotFound
	}
	s.nextCampaign++
	s.nextTask++
	campID := fmt.Sprintf("0194f030-3000-7000-8000-%012d", s.nextCampaign)
	taskID := fmt.Sprintf("0194f030-4000-7000-8000-%012d", s.nextTask)
	now := time.Now().UTC()
	timeoutSec := connectivity.DefaultOTAHealthCheckTimeoutSec
	if input.HealthCheckTimeoutSec != nil {
		timeoutSec = *input.HealthCheckTimeoutSec
	}
	task := connectivity.OTADeviceTask{
		ID:            taskID,
		TenantID:      claims.TenantID,
		CampaignID:    campID,
		PackageID:     pkg.ID,
		GatewayID:     s.gatewayID,
		SiteID:        s.siteID,
		GatewayCode:   "gw-e2e-1",
		GatewayName:   "E2E Gateway 1",
		Wave:          connectivity.OTAWaveCanary,
		DesiredAction: edgecontrol.OTAActionApply,
		Stage:         connectivity.OTATaskStageDispatched,
		TargetVersion: pkg.Version,
		DispatchedAt:  &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	detail := connectivity.OTACampaignDetail{
		OTACampaignSummary: connectivity.OTACampaignSummary{
			ID:                      campID,
			TenantID:                claims.TenantID,
			Name:                    input.Name,
			PackageID:               pkg.ID,
			PackageVersion:          pkg.Version,
			Component:               pkg.Component,
			TargetOS:                pkg.TargetOS,
			TargetArch:              pkg.TargetArch,
			TargetSiteIDs:           []string{s.siteID},
			TargetTags:              map[string]string{},
			CanaryPercent:           input.CanaryPercent,
			FailureRateThresholdPct: input.FailureRateThresholdPct,
			HealthCheckTimeoutSec:   timeoutSec,
			AutoPromoteRollout:      true,
			Status:                  connectivity.OTACampaignStatusCanaryRunning,
			Reason:                  input.Reason,
			TotalCount:              1,
			InProgressCount:         1,
			CreatedBy:               claims.PrincipalID,
			UpdatedBy:               claims.PrincipalID,
			CreatedAt:               now,
			UpdatedAt:               now,
		},
		Package: pkg,
		Tasks:   []connectivity.OTADeviceTask{task},
	}
	s.campaigns[campID] = detail
	return detail, connectivity.BuildOTAStageDispatches(pkg, []connectivity.OTADeviceTask{task}, timeoutSec, ""), nil
}

func (s *e2eOTAStore) PauseOTACampaign(_ context.Context, _ registryauth.GrantClaims, campaignID, _ string) (connectivity.OTACampaignDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.campaigns[campaignID], nil
}

func (s *e2eOTAStore) ResumeOTACampaign(_ context.Context, _ registryauth.GrantClaims, campaignID, _ string) (connectivity.OTACampaignDetail, []connectivity.OTAStageDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.campaigns[campaignID], nil, nil
}

func (s *e2eOTAStore) RollbackOTACampaign(_ context.Context, _ registryauth.GrantClaims, campaignID, _ string) (connectivity.OTACampaignDetail, []connectivity.OTAStageDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.campaigns[campaignID], nil, nil
}

func (s *e2eOTAStore) GetGatewayDesiredOTAStage(_ context.Context, _ string) (*edgecontrol.OTAStagePayload, error) {
	return nil, nil
}

func (s *e2eOTAStore) ApplyGatewayOTAResult(_ context.Context, gatewayID string, result edgecontrol.OTAResultPayload) (connectivity.OTADeviceTask, []connectivity.OTAStageDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	detail, ok := s.campaigns[result.CampaignID]
	if !ok {
		return connectivity.OTADeviceTask{}, nil, connectivity.ErrNotFound
	}
	now := time.Now().UTC()
	next, updated, err := connectivity.EvaluateOTATaskProgress(detail.Tasks[0], result, now)
	if err != nil {
		return connectivity.OTADeviceTask{}, nil, err
	}
	if !updated {
		return detail.Tasks[0], nil, nil
	}
	detail.Tasks[0] = next
	decision := connectivity.EvaluateOTACampaignTransition(detail.Status, detail.FailureRateThresholdPct, detail.AutoPromoteRollout, detail.Tasks, now)
	if decision.Changed {
		detail.Status = decision.NewStatus
		detail.PauseReason = decision.NewPauseReason
		if decision.CompletedAt != nil {
			detail.CompletedAt = decision.CompletedAt
		}
	}
	detail.PendingCount = 0
	detail.InProgressCount = 0
	detail.SucceededCount = 0
	detail.FailedCount = 0
	detail.RolledBackCount = 0
	switch next.Stage {
	case edgecontrol.OTAStageSucceeded:
		detail.SucceededCount = 1
	case edgecontrol.OTAStageFailed:
		detail.FailedCount = 1
	case edgecontrol.OTAStageRolledBack:
		detail.RolledBackCount = 1
	default:
		detail.InProgressCount = 1
	}
	s.campaigns[result.CampaignID] = detail
	return next, nil, nil
}

type e2ePeerVerifier struct {
	gatewayID string
}

func (v e2ePeerVerifier) VerifyActiveCredential(_ context.Context, peer *x509.Certificate) (string, error) {
	if peer == nil || peer.Subject.CommonName != v.gatewayID {
		return "", connectivity.ErrCredentialForbidden
	}
	return v.gatewayID, nil
}

func newOTABackendE2EHarness(t *testing.T) (
	srv *httptest.Server,
	storage *connectivity.OTAArtifactStorage,
	store *e2eOTAStore,
	agent *OTAAgent,
	agentDir string,
	seenRange *atomic.Bool,
	v1Bin []byte,
) {
	t.Helper()
	now := time.Now().UTC()
	pkiDir := t.TempDir()
	pki := newTestPKIFixture(t, pkiDir, now)

	const (
		tenantID = "0194f030-1000-7000-8000-000000000001"
		siteID   = "0194f030-2000-7000-8000-000000000001"
		gwID     = "0194f030-5000-7000-8000-000000000001"
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

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	storage, err = connectivity.NewOTAArtifactStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signer, err := connectivity.NewOTASignerVerifier(keyID, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	store = &e2eOTAStore{
		tenantID:  tenantID,
		siteID:    siteID,
		gatewayID: gwID,
		actorID:   actorID,
		packages:  map[string]connectivity.OTAPackage{},
		campaigns: map[string]connectivity.OTACampaignDetail{},
	}

	seenRange = &atomic.Bool{}

	otaHandler := connectivity.NewOTAHandler(
		store,
		storage,
		signer,
		e2ePeerVerifier{gatewayID: gwID},
		func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
			return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: actorID, AllowedSiteIDs: []string{siteID}}, nil
		},
		connectivity.OTAHandlerOptions{
			MaxPackageBytes: 1 << 20,
		},
	)

	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror nginx mTLS termination forwarding peer leaf certificate to Connectivity.
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.TLS.PeerCertificates[0].Raw})
			r.Header.Set("X-Gateway-Client-Cert", url.QueryEscape(string(pemBytes)))
		}
		if r.Header.Get("Range") != "" {
			seenRange.Store(true)
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

	agentDir = t.TempDir()
	agent, err = NewOTAAgent(OTAAgentOptions{
		Dir:             agentDir,
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
	})
	if err != nil {
		t.Fatalf("NewOTAAgent: %v", err)
	}
	v1Bin = []byte("#!/bin/sh\necho v1.0.0\n")
	if err := agent.SeedSlotA(v1Bin); err != nil {
		t.Fatalf("SeedSlotA: %v", err)
	}
	return srv, storage, store, agent, agentDir, seenRange, v1Bin
}

func uploadAndStartCampaignViaHTTP(t *testing.T, mtlsClient *http.Client, baseURL, version string, payload []byte) (connectivity.OTAPackage, edgecontrol.OTAStagePayload) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{
		"component":      edgecontrol.OTAComponentEdgeGateway,
		"version":        version,
		"targetOs":       "linux",
		"targetArch":     "armv7l",
		"hardwareModels": "EG8200",
		"releaseNotes":   "E2E release " + version,
	} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", "edge-gateway-"+version+".bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	uploadReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/internal/v1/ota/packages", &body)
	if err != nil {
		t.Fatal(err)
	}
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
	uploadResp, err := mtlsClient.Do(uploadReq)
	if err != nil {
		t.Fatalf("upload POST failed: %v", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status=%d", uploadResp.StatusCode)
	}
	var pkg connectivity.OTAPackage
	if err := json.NewDecoder(uploadResp.Body).Decode(&pkg); err != nil {
		t.Fatal(err)
	}

	campBody := fmt.Sprintf(`{"name":"E2E %s","packageId":%q,"canaryPercent":100,"failureRateThresholdPct":50,"reason":"e2e"}`, version, pkg.ID)
	campReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/internal/v1/ota/campaigns", bytes.NewBufferString(campBody))
	if err != nil {
		t.Fatal(err)
	}
	campReq.Header.Set("Content-Type", "application/json")
	campResp, err := mtlsClient.Do(campReq)
	if err != nil {
		t.Fatalf("campaign POST failed: %v", err)
	}
	defer campResp.Body.Close()
	if campResp.StatusCode != http.StatusCreated {
		t.Fatalf("campaign status=%d", campResp.StatusCode)
	}
	var detail connectivity.OTACampaignDetail
	if err := json.NewDecoder(campResp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}

	dispatches := connectivity.BuildOTAStageDispatches(pkg, detail.Tasks, detail.HealthCheckTimeoutSec, baseURL)
	return pkg, dispatches[0].Stage
}

func forwardAgentReportHTTP(t *testing.T, mtlsClient *http.Client, tlsID tlsIdentity, baseURL string, res edgecontrol.OTAResultPayload) {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/gateway/v1/ota/tasks/"+res.TaskID+"/report", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := signHTTPPoPIfPresent(req, tlsID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	resp, err := mtlsClient.Do(req)
	if err != nil {
		t.Fatalf("report POST failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report status=%d for stage=%s", resp.StatusCode, res.Stage)
	}
}

func TestOTAAgentToConnectivityBackendE2E(t *testing.T) {
	t.Run("ResumeDownloadVerifySwitchAndBootCommit", func(t *testing.T) {
		srv, _, store, agent, agentDir, seenRange, _ := newOTABackendE2EHarness(t)
		client, tlsID, err := agent.httpClientAndIdentity()
		if err != nil {
			t.Fatal(err)
		}

		v2Bin := []byte("#!/bin/sh\necho edge-gateway-v2.0.0-real-mtls-range-download-payload\n")
		pkg, stage := uploadAndStartCampaignViaHTTP(t, client, srv.URL, "2.0.0", v2Bin)

		// Pre-seed 16 bytes so OTAAgent exercises real HTTP Range: bytes=16- -> 206 Partial Content.
		partPath := filepath.Join(agentDir, "downloads", pkg.ID+".part")
		if err := os.WriteFile(partPath, v2Bin[:16], 0o644); err != nil {
			t.Fatal(err)
		}

		var applyReports []edgecontrol.OTAResultPayload
		if err := agent.ApplyStage(t.Context(), stage, func(res edgecontrol.OTAResultPayload) {
			applyReports = append(applyReports, res)
			forwardAgentReportHTTP(t, client, tlsID, srv.URL, res)
		}); err != nil {
			t.Fatalf("ApplyStage: %v", err)
		}
		if !seenRange.Load() {
			t.Fatal("expected HTTP Range header during resumable download")
		}
		if len(applyReports) == 0 || applyReports[len(applyReports)-1].Stage != edgecontrol.OTAStageHealthChecking {
			t.Fatalf("expected final ApplyStage report HEALTH_CHECKING, got %+v", applyReports)
		}

		// Simulate reboot confirmation with passing health check -> SUCCEEDED -> campaign COMPLETED.
		rolledBack, err := agent.VerifyPendingUpdateOnBoot(
			t.Context(),
			func(context.Context) error { return nil },
			func(res edgecontrol.OTAResultPayload) {
				forwardAgentReportHTTP(t, client, tlsID, srv.URL, res)
			},
		)
		if err != nil || rolledBack {
			t.Fatalf("VerifyPendingUpdateOnBoot = (%v, %v)", rolledBack, err)
		}

		linkTarget, err := os.Readlink(filepath.Join(agentDir, "current"))
		if err != nil || linkTarget != filepath.Join("slots", otaSlotB) {
			t.Fatalf("current symlink = %q, err=%v", linkTarget, err)
		}
		camp, err := store.GetOTACampaign(t.Context(), registryauth.GrantClaims{TenantID: store.tenantID}, stage.CampaignID)
		if err != nil || camp.Status != connectivity.OTACampaignStatusCompleted || camp.SucceededCount != 1 {
			t.Fatalf("expected COMPLETED campaign, got %+v err=%v", camp, err)
		}
	})

	t.Run("TamperedArtifactRejectedAndTripsCircuitBreaker", func(t *testing.T) {
		srv, storage, store, agent, agentDir, _, v1Bin := newOTABackendE2EHarness(t)
		client, tlsID, err := agent.httpClientAndIdentity()
		if err != nil {
			t.Fatal(err)
		}

		v2Bin := []byte("#!/bin/sh\necho edge-gateway-v2.1.0-tamper-test\n")
		pkg, stage := uploadAndStartCampaignViaHTTP(t, client, srv.URL, "2.1.0", v2Bin)

		// Tamper stored artifact on disk directly (StorageRelPath is json:"-" on HTTP response).
		storedPkg, err := store.GetOTAPackageForGateway(t.Context(), store.gatewayID, pkg.ID)
		if err != nil {
			t.Fatal(err)
		}
		f, _, err := storage.Open(storedPkg.StorageRelPath)
		if err != nil {
			t.Fatal(err)
		}
		storedPath := f.Name()
		_ = f.Close()
		tampered := bytes.Replace(bytes.Clone(v2Bin), []byte("tamper"), []byte("hacked"), 1)
		if err := os.WriteFile(storedPath, tampered, 0o640); err != nil {
			t.Fatal(err)
		}

		applyErr := agent.ApplyStage(t.Context(), stage, func(res edgecontrol.OTAResultPayload) {
			forwardAgentReportHTTP(t, client, tlsID, srv.URL, res)
		})
		if applyErr == nil {
			t.Fatal("expected ApplyStage to reject tampered binary")
		}
		active, _ := os.ReadFile(filepath.Join(agentDir, "current"))
		if !bytes.Equal(active, v1Bin) {
			t.Fatal("active slot changed despite SHA-256 tamper")
		}

		camp, err := store.GetOTACampaign(t.Context(), registryauth.GrantClaims{TenantID: store.tenantID}, stage.CampaignID)
		if err != nil || camp.Status != connectivity.OTACampaignStatusPaused ||
			camp.PauseReason == nil || *camp.PauseReason != connectivity.OTAPauseReasonFailureRateExceeded {
			t.Fatalf("expected PAUSED FAILURE_RATE_THRESHOLD_EXCEEDED, got %+v err=%v", camp, err)
		}
	})

	t.Run("BootHealthFailureAutoRollsBackAndTripsCircuitBreaker", func(t *testing.T) {
		srv, _, store, agent, agentDir, _, v1Bin := newOTABackendE2EHarness(t)
		client, tlsID, err := agent.httpClientAndIdentity()
		if err != nil {
			t.Fatal(err)
		}

		v2Bin := []byte("#!/bin/sh\necho edge-gateway-v2.2.0-rollback-test\n")
		_, stage := uploadAndStartCampaignViaHTTP(t, client, srv.URL, "2.2.0", v2Bin)

		if err := agent.ApplyStage(t.Context(), stage, func(res edgecontrol.OTAResultPayload) {
			forwardAgentReportHTTP(t, client, tlsID, srv.URL, res)
		}); err != nil {
			t.Fatalf("ApplyStage: %v", err)
		}

		rolledBack, err := agent.VerifyPendingUpdateOnBoot(
			t.Context(),
			func(context.Context) error { return errors.New("systemd health check failed") },
			func(res edgecontrol.OTAResultPayload) {
				forwardAgentReportHTTP(t, client, tlsID, srv.URL, res)
			},
		)
		if err != nil || !rolledBack {
			t.Fatalf("expected auto-rollback on failed boot health check, got (%v, %v)", rolledBack, err)
		}
		active, _ := os.ReadFile(filepath.Join(agentDir, "current"))
		if !bytes.Equal(active, v1Bin) {
			t.Fatal("current binary did not revert to slot-a")
		}

		camp, err := store.GetOTACampaign(t.Context(), registryauth.GrantClaims{TenantID: store.tenantID}, stage.CampaignID)
		if err != nil || camp.Status != connectivity.OTACampaignStatusPaused ||
			camp.PauseReason == nil || *camp.PauseReason != connectivity.OTAPauseReasonFailureRateExceeded ||
			camp.RolledBackCount != 1 {
			t.Fatalf("expected PAUSED circuit breaker after ROLLED_BACK, got %+v err=%v", camp, err)
		}
		sumV2 := sha256.Sum256(v2Bin)
		_ = hex.EncodeToString(sumV2[:])
	})
}
