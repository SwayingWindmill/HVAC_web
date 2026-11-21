package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type dispatchContextFakeStore struct {
	*fakeRegistryStore
	ctxView       CommandDispatchContext
	ctxErr        error
	calls         int
	resolveCalls  int
	resolvedID    string
	resolveErr    error
	lastPointCode string
	lastCap       string
}

func (s *dispatchContextFakeStore) GetCommandDispatchContext(
	_ context.Context,
	tenantID, siteID, deviceID, pointID string,
	_ time.Time,
) (CommandDispatchContext, error) {
	s.calls++
	if s.ctxErr != nil {
		return CommandDispatchContext{}, s.ctxErr
	}
	view := s.ctxView
	view.TenantID = tenantID
	view.SiteID = siteID
	view.DeviceID = deviceID
	view.PointID = pointID
	return view, nil
}

func (s *dispatchContextFakeStore) ResolveCommandDispatchContext(
	_ context.Context,
	tenantID, siteID, deviceID, pointCode, capability string,
	_ time.Time,
) (CommandDispatchContext, error) {
	s.resolveCalls++
	s.lastPointCode = pointCode
	s.lastCap = capability
	if s.resolveErr != nil {
		return CommandDispatchContext{}, s.resolveErr
	}
	view := s.ctxView
	view.TenantID = tenantID
	view.SiteID = siteID
	view.DeviceID = deviceID
	view.PointID = s.resolvedID
	return view, nil
}

func peerCertForSPIFFE(t *testing.T, spiffeID string) *x509.Certificate {
	t.Helper()
	uri, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatal(err)
	}
	return &x509.Certificate{URIs: []*url.URL{uri}}
}

func TestServerServesCommandDispatchContextOnlyToCommandService(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	pointID := "018f1e00-6000-7000-8000-000000000001"
	store := &dispatchContextFakeStore{
		fakeRegistryStore: &fakeRegistryStore{},
		ctxView: CommandDispatchContext{
			AssetID:          testAssetA1,
			ControlMode:      SiteControlModeManualOverride,
			FeedbackPointKey: "run_state",
			ActuationMode:    "PULSE",
			AssetLocks: []AssetControlLockState{{
				SiteID: testSiteA1, AssetID: testAssetA1, LockMode: AssetControlLockManual,
				BoundDeviceIDs: []string{testDeviceA1},
			}},
			ActiveMaintenanceWindow: &ActiveMaintenanceWindow{
				ID: "mw-1", Title: "Annual", StartDate: "2026-11-16", EndDate: "2026-11-16", Reason: "check",
			},
			Interlocks: []CommandDispatchInterlock{{
				DeviceID: testControlDevicePLC1, Key: "run_state", Expected: "RUNNING", Reason: "pump must run",
			}},
		},
	}
	harness := newCoreHarness(t, now, store, StaticGrantStatusProvider{PolicyRevision: testPolicy})

	queryURL := CommandDispatchContextPath +
		"?tenantId=" + testTenantA +
		"&siteId=" + testSiteA1 +
		"&deviceId=" + testDeviceA1 +
		"&pointId=" + pointID

	// Non-command-service SPIFFE (platform-gateway) must be rejected with 401.
	gatewayReq := httptest.NewRequest(http.MethodGet, queryURL, nil)
	gatewayCert := peerCertForSPIFFE(t, testPresenter)
	gatewayReq.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{gatewayCert},
		VerifiedChains:   [][]*x509.Certificate{{gatewayCert}},
	}
	rec := httptest.NewRecorder()
	harness.handler.ServeHTTP(rec, gatewayReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("gateway status=%d body=%s, want 401", rec.Code, rec.Body.String())
	}

	// Forged identity header must be rejected with 400.
	cmdCert := peerCertForSPIFFE(t, "spiffe://hvac.local/command-service")
	forgedReq := httptest.NewRequest(http.MethodGet, queryURL, nil)
	forgedReq.Header.Set("X-Principal", "forged")
	forgedReq.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cmdCert},
		VerifiedChains:   [][]*x509.Certificate{{cmdCert}},
	}
	forgedRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(forgedRec, forgedReq)
	if forgedRec.Code != http.StatusBadRequest {
		t.Fatalf("forged status=%d body=%s, want 400", forgedRec.Code, forgedRec.Body.String())
	}

	// Non-GET must return 405.
	postReq := httptest.NewRequest(http.MethodPost, queryURL, nil)
	postReq.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cmdCert},
		VerifiedChains:   [][]*x509.Certificate{{cmdCert}},
	}
	postRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post status=%d, want 405", postRec.Code)
	}

	// Malformed UUID query must return 400.
	badQueryReq := httptest.NewRequest(http.MethodGet, CommandDispatchContextPath+"?tenantId=bad", nil)
	badQueryReq.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cmdCert},
		VerifiedChains:   [][]*x509.Certificate{{cmdCert}},
	}
	badQueryRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(badQueryRec, badQueryReq)
	if badQueryRec.Code != http.StatusBadRequest {
		t.Fatalf("bad query status=%d, want 400", badQueryRec.Code)
	}

	// Valid mTLS command-service request succeeds without X-Delegation-Grant.
	okReq := httptest.NewRequest(http.MethodGet, queryURL, nil)
	okReq.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cmdCert},
		VerifiedChains:   [][]*x509.Certificate{{cmdCert}},
	}
	okRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK || store.calls != 1 {
		t.Fatalf("ok status=%d calls=%d body=%s", okRec.Code, store.calls, okRec.Body.String())
	}
	var got CommandDispatchContext
	if err := json.NewDecoder(okRec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ControlMode != SiteControlModeManualOverride || got.AssetID != testAssetA1 ||
		got.ActuationMode != "PULSE" || got.FeedbackPointKey != "run_state" ||
		len(got.AssetLocks) != 1 || got.ActiveMaintenanceWindow == nil || len(got.Interlocks) != 1 {
		t.Fatalf("unexpected dispatch context: %+v", got)
	}

	// Mixing pointId with capability/pointCode must fail with 400.
	mixReq := httptest.NewRequest(http.MethodGet, queryURL+"&capability=SET_FREQUENCY", nil)
	mixReq.TLS = okReq.TLS
	mixRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(mixRec, mixReq)
	if mixRec.Code != http.StatusBadRequest {
		t.Fatalf("mixed query status=%d body=%s, want 400", mixRec.Code, mixRec.Body.String())
	}

	// Missing both pointId and capability must fail with 400.
	noPointReq := httptest.NewRequest(http.MethodGet, CommandDispatchContextPath+"?tenantId="+testTenantA+"&siteId="+testSiteA1+"&deviceId="+testDeviceA1, nil)
	noPointReq.TLS = okReq.TLS
	noPointRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(noPointRec, noPointReq)
	if noPointRec.Code != http.StatusBadRequest {
		t.Fatalf("missing point selector status=%d body=%s, want 400", noPointRec.Code, noPointRec.Body.String())
	}

	// Mode B: Resolve by capability and optional pointCode succeeds.
	store.resolvedID = pointID
	resolveURL := CommandDispatchContextPath +
		"?tenantId=" + testTenantA +
		"&siteId=" + testSiteA1 +
		"&deviceId=" + testDeviceA1 +
		"&pointCode=freq_cmd&capability=SET_FREQUENCY"
	resReq := httptest.NewRequest(http.MethodGet, resolveURL, nil)
	resReq.TLS = okReq.TLS
	resRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(resRec, resReq)
	if resRec.Code != http.StatusOK || store.resolveCalls != 1 || store.lastPointCode != "freq_cmd" || store.lastCap != "SET_FREQUENCY" {
		t.Fatalf("resolve status=%d calls=%d code=%q cap=%q body=%s", resRec.Code, store.resolveCalls, store.lastPointCode, store.lastCap, resRec.Body.String())
	}
	var resolved CommandDispatchContext
	if err := json.NewDecoder(resRec.Body).Decode(&resolved); err != nil || resolved.PointID != pointID {
		t.Fatalf("resolved payload mismatch: err=%v got=%+v", err, resolved)
	}

	// ErrNotFound -> 404, ErrBindingConflict -> 409.
	store.resolveErr = ErrNotFound
	nfReq := httptest.NewRequest(http.MethodGet, resolveURL, nil)
	nfReq.TLS = okReq.TLS
	nfRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(nfRec, nfReq)
	if nfRec.Code != http.StatusNotFound {
		t.Fatalf("not found resolve status=%d, want 404", nfRec.Code)
	}

	store.resolveErr = ErrBindingConflict
	confReq := httptest.NewRequest(http.MethodGet, resolveURL, nil)
	confReq.TLS = okReq.TLS
	confRec := httptest.NewRecorder()
	harness.handler.ServeHTTP(confRec, confReq)
	if confRec.Code != http.StatusConflict {
		t.Fatalf("ambiguous resolve status=%d, want 409", confRec.Code)
	}
}
