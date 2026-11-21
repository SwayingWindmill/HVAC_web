package commandservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	runtimeTestOrganization = "018f3e00-0000-7000-8000-000000000001"
	runtimeTestSite         = "018f3e00-1000-7000-8000-000000000001"
	runtimeTestDevice       = "018f3e00-3000-7000-8000-000000000001"
)

type runtimeStoreStub struct {
	dispatchEnvelope     commandmodel.DispatchEnvelope
	verificationEnvelope commandmodel.VerificationEnvelope
	claimDispatchErr     error
	claimVerificationErr error
	claimedTenant        string
	resolvedDispatch     bool
	resolvedVerification bool
	preparedEvidence     bool
	completedEvidence    bool
	cancelReq            commandmodel.CancelPendingRequest
	cancelledIDs         []string
	cancelErr            error
	syncTenantID         string
	syncedProfiles       []CapabilityProfileSyncItem
	syncErr              error
}

func (stub *runtimeStoreStub) ClaimDispatch(_ context.Context, tenantID string, _ string, _ time.Duration) (commandmodel.DispatchEnvelope, error) {
	stub.claimedTenant = tenantID
	return stub.dispatchEnvelope, stub.claimDispatchErr
}

func (stub *runtimeStoreStub) ClaimAnyDispatch(_ context.Context, _ string, _, _ time.Duration) (commandmodel.DispatchEnvelope, error) {
	return stub.dispatchEnvelope, stub.claimDispatchErr
}

func (stub *runtimeStoreStub) ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error {
	stub.resolvedDispatch = true
	return nil
}

func (stub *runtimeStoreStub) ClaimVerification(_ context.Context, tenantID string, _ string, _ time.Duration) (commandmodel.VerificationEnvelope, error) {
	stub.claimedTenant = tenantID
	return stub.verificationEnvelope, stub.claimVerificationErr
}

func (stub *runtimeStoreStub) ClaimAnyVerification(_ context.Context, _ string, _, _ time.Duration) (commandmodel.VerificationEnvelope, error) {
	return stub.verificationEnvelope, stub.claimVerificationErr
}

func (stub *runtimeStoreStub) ResolveVerification(context.Context, commandmodel.VerificationEnvelope, commandmodel.VerificationResult) error {
	stub.resolvedVerification = true
	return nil
}

func (stub *runtimeStoreStub) PrepareConnectorEvidence(context.Context, commandmodel.PreparedConnectorEvidence) error {
	stub.preparedEvidence = true
	return nil
}

func (stub *runtimeStoreStub) CompleteConnectorEvidence(context.Context, commandmodel.CompletedConnectorEvidence) error {
	stub.completedEvidence = true
	return nil
}

func (stub *runtimeStoreStub) CancelPending(_ context.Context, request commandmodel.CancelPendingRequest) ([]string, error) {
	stub.cancelReq = request
	return stub.cancelledIDs, stub.cancelErr
}

func (stub *runtimeStoreStub) SyncCapabilityProfiles(_ context.Context, tenantID string, _ time.Time, profiles []CapabilityProfileSyncItem) (int, error) {
	if stub.syncErr != nil {
		return 0, stub.syncErr
	}
	stub.syncTenantID = tenantID
	stub.syncedProfiles = append([]CapabilityProfileSyncItem(nil), profiles...)
	return len(profiles), nil
}

func TestRuntimeHTTPDispatcherClaimsWithinTheNamedTenantAndResolves(t *testing.T) {
	stub := &runtimeStoreStub{dispatchEnvelope: commandmodel.DispatchEnvelope{
		CommandID: "command-1", AttemptID: "attempt-1", TenantID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: runtimeTestDevice,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: setpointCapabilityRevision,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 22}, PayloadHash: "hash", ExecutionFence: 1, DeviceCommandSequence: 1,
		LeaseOwner: "dispatcher-a", LeaseUntil: time.Now().UTC().Add(30 * time.Second),
	}}
	handler := runtimeTestHandler(t, stub)

	claim := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"tenantId":"`+runtimeTestOrganization+`","leaseOwner":"dispatcher-a","leaseSeconds":30}`, "spiffe://hvac.local/command-dispatcher")
	claimRecorder := httptest.NewRecorder()
	handler.ServeHTTP(claimRecorder, claim)
	if claimRecorder.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimRecorder.Code, claimRecorder.Body.String())
	}
	if stub.claimedTenant != runtimeTestOrganization {
		t.Fatalf("claimed Tenant=%s", stub.claimedTenant)
	}
	var envelope commandmodel.DispatchEnvelope
	if err := json.NewDecoder(claimRecorder.Body).Decode(&envelope); err != nil || envelope.CommandID != "command-1" {
		t.Fatalf("claim envelope=%#v err=%v", envelope, err)
	}

	resolveBody, err := json.Marshal(runtimeDispatchResolveRequest{
		Envelope: stub.dispatchEnvelope,
		Result:   commandmodel.ConnectorResult{Phase: commandmodel.ConnectorAcknowledged, EvidenceID: "evidence-1", Acknowledged: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolve := runtimeRequest(t, http.MethodPost, InternalDispatchResolvePath, string(resolveBody), "spiffe://hvac.local/command-dispatcher")
	resolveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(resolveRecorder, resolve)
	if resolveRecorder.Code != http.StatusNoContent || !stub.resolvedDispatch {
		t.Fatalf("resolve status=%d resolved=%v body=%s", resolveRecorder.Code, stub.resolvedDispatch, resolveRecorder.Body.String())
	}
}

func TestRuntimeHTTPNoWorkIsNoContent(t *testing.T) {
	stub := &runtimeStoreStub{claimDispatchErr: ErrNoDispatchAvailable, claimVerificationErr: ErrVerificationNotAvailable}
	handler := runtimeTestHandler(t, stub)
	for _, test := range []struct {
		path, spiffe string
	}{
		{InternalDispatchClaimPath, "spiffe://hvac.local/command-dispatcher"},
		{InternalVerificationClaimPath, "spiffe://hvac.local/command-verifier"},
	} {
		request := runtimeRequest(t, http.MethodPost, test.path, `{"tenantId":"`+runtimeTestOrganization+`","leaseOwner":"worker-a","leaseSeconds":15}`, test.spiffe)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("path=%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRuntimeHTTPRejectsAClaimWithoutATenant(t *testing.T) {
	stub := &runtimeStoreStub{}
	handler := runtimeTestHandler(t, stub)
	claim := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"leaseOwner":"dispatcher-a","leaseSeconds":15}`, "spiffe://hvac.local/command-dispatcher")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, claim)
	if recorder.Code != http.StatusBadRequest || stub.claimedTenant != "" {
		t.Fatalf("claim status=%d claimed=%q body=%s", recorder.Code, stub.claimedTenant, recorder.Body.String())
	}
}

func TestRuntimeHTTPRejectsWrongWorkloadIdentity(t *testing.T) {
	stub := &runtimeStoreStub{claimDispatchErr: errors.New("must not be reached")}
	handler := runtimeTestHandler(t, stub)
	request := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"tenantId":"`+runtimeTestOrganization+`","leaseOwner":"verifier-a","leaseSeconds":15}`, "spiffe://hvac.local/command-verifier")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRuntimeHTTPRecordsBoundedVerificationMetrics(t *testing.T) {
	registry := observability.NewRegistry()
	stub := &runtimeStoreStub{}
	handler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store: stub, Metrics: registry,
		DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher", VerifierSPIFFE: "spiffe://hvac.local/command-verifier",
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := commandmodel.VerificationEnvelope{
		TenantID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: runtimeTestDevice,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, AcknowledgedAt: time.Now().UTC().Add(-2 * time.Second),
	}
	body, err := json.Marshal(runtimeVerificationResolveRequest{Envelope: envelope, Result: commandmodel.VerificationResult{Outcome: commandmodel.VerificationSucceeded}})
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeRequest(t, http.MethodPost, InternalVerificationResolvePath, string(body), "spiffe://hvac.local/command-verifier")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || !stub.resolvedVerification {
		t.Fatalf("status=%d resolved=%v body=%s", recorder.Code, stub.resolvedVerification, recorder.Body.String())
	}
	metricsRecorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(metricsRecorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := metricsRecorder.Body.String()
	for _, marker := range []string{
		`hvac_command_verifications_total{outcome="verified"} 1`,
		`hvac_command_verification_duration_seconds_count{outcome="verified"} 1`,
	} {
		if !strings.Contains(metricsBody, marker) {
			t.Fatalf("missing %q in metrics: %s", marker, metricsBody)
		}
	}
	if strings.Contains(metricsBody, runtimeTestDevice) {
		t.Fatalf("command metrics leaked device identity: %s", metricsBody)
	}
}

func TestRuntimeHTTPCancelRetiredTargetAuthorizationAndDelegation(t *testing.T) {
	stub := &runtimeStoreStub{
		cancelledIDs: []string{"0193f100-9000-7000-8000-000000000001"},
	}
	handler := runtimeTestHandler(t, stub)
	payload := fmt.Sprintf(`{"tenantId":%q,"siteId":%q,"deviceIds":[%q],"causationId":"0193f100-7000-7000-8000-000000000009"}`,
		runtimeTestOrganization, runtimeTestSite, runtimeTestDevice)

	// Dispatcher and verifier must not be able to invoke cancelRetiredTarget.
	for _, disallowed := range []string{"spiffe://hvac.local/command-dispatcher", "spiffe://hvac.local/command-verifier", ""} {
		req := runtimeRequest(t, http.MethodPost, InternalCommandCancelRetiredPath, payload, disallowed)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("spiffe=%q unexpectedly allowed status=%d", disallowed, rec.Code)
		}
	}

	req := runtimeRequest(t, http.MethodPost, InternalCommandCancelRetiredPath, payload, "spiffe://hvac.local/platform-core-service")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from cancelRetiredTarget, got %d (%s)", rec.Code, rec.Body.String())
	}
	if stub.cancelReq.TenantID != runtimeTestOrganization || stub.cancelReq.SiteID != runtimeTestSite ||
		len(stub.cancelReq.DeviceIDs) != 1 || stub.cancelReq.DeviceIDs[0] != runtimeTestDevice ||
		stub.cancelReq.Reason != "TARGET_DEVICE_RETIRED" {
		t.Fatalf("unexpected CancelPending request: %#v", stub.cancelReq)
	}
	var resp struct {
		CancelledCommandIDs []string `json:"cancelledCommandIds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.CancelledCommandIDs) != 1 || resp.CancelledCommandIDs[0] != stub.cancelledIDs[0] {
		t.Fatalf("unexpected response payload: %#v", resp)
	}

	for _, invalidPayload := range []string{
		fmt.Sprintf(`{"tenantId":"not-a-uuid","siteId":%q,"deviceIds":[%q]}`, runtimeTestSite, runtimeTestDevice),
		fmt.Sprintf(`{"tenantId":%q,"siteId":"bad-site","deviceIds":[%q]}`, runtimeTestOrganization, runtimeTestDevice),
		fmt.Sprintf(`{"tenantId":%q,"siteId":%q,"deviceIds":["bad-device"]}`, runtimeTestOrganization, runtimeTestSite),
		fmt.Sprintf(`{"tenantId":%q,"siteId":%q,"deviceIds":[%q,""]}`, runtimeTestOrganization, runtimeTestSite, runtimeTestDevice),
		fmt.Sprintf(`{"tenantId":%q,"siteId":%q,"deviceIds":[]}`, runtimeTestOrganization, runtimeTestSite),
	} {
		badReq := runtimeRequest(t, http.MethodPost, InternalCommandCancelRetiredPath, invalidPayload, "spiffe://hvac.local/platform-core-service")
		badRec := httptest.NewRecorder()
		handler.ServeHTTP(badRec, badReq)
		if badRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for payload %s, got %d (%s)", invalidPayload, badRec.Code, badRec.Body.String())
		}
	}
}

func runtimeTestHandler(t *testing.T, stub RuntimeStore) http.Handler {
	t.Helper()
	handler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store: stub, DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher", VerifierSPIFFE: "spiffe://hvac.local/command-verifier",
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return handler
}

func runtimeRequest(t *testing.T, method, path, body, spiffe string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if request.TLS == nil {
		request.TLS = &tls.ConnectionState{}
	}
	identity, err := url.Parse(spiffe)
	if err != nil {
		t.Fatal(err)
	}
	request.TLS.PeerCertificates = []*x509.Certificate{{URIs: []*url.URL{identity}}}
	return request
}

func TestRuntimeHTTPClaimAnyDispatchAndVerificationRoutes(t *testing.T) {
	t.Parallel()

	stub := &runtimeStoreStub{
		dispatchEnvelope:     commandmodel.DispatchEnvelope{CommandID: "cmd-any-1", AttemptID: "att-any-1", TenantID: runtimeTestOrganization},
		verificationEnvelope: commandmodel.VerificationEnvelope{CommandID: "cmd-any-2", AttemptID: "att-any-2", TenantID: runtimeTestOrganization},
	}
	handler := runtimeTestHandler(t, stub)

	body := `{"leaseOwner":"worker-1","leaseSeconds":30}`
	dispReq := runtimeRequest(t, http.MethodPost, InternalDispatchClaimAnyPath+"?waitMs=25", body, "spiffe://hvac.local/command-dispatcher")
	dispRec := httptest.NewRecorder()
	handler.ServeHTTP(dispRec, dispReq)
	if dispRec.Code != http.StatusOK {
		t.Fatalf("dispatch claim-any status=%d, want 200 (%s)", dispRec.Code, dispRec.Body.String())
	}
	if route := ObservabilityRoute(dispReq); route != "dispatch.claim-any" {
		t.Fatalf("dispatch claim-any metric route=%q, want dispatch.claim-any", route)
	}

	verReq := runtimeRequest(t, http.MethodPost, InternalVerificationClaimAnyPath+"?waitMs=25", body, "spiffe://hvac.local/command-verifier")
	verRec := httptest.NewRecorder()
	handler.ServeHTTP(verRec, verReq)
	if verRec.Code != http.StatusOK {
		t.Fatalf("verification claim-any status=%d, want 200 (%s)", verRec.Code, verRec.Body.String())
	}
	if route := ObservabilityRoute(verReq); route != "verification.claim-any" {
		t.Fatalf("verification claim-any metric route=%q, want verification.claim-any", route)
	}

	forbiddenReq := runtimeRequest(t, http.MethodPost, InternalDispatchClaimAnyPath, body, "spiffe://hvac.local/command-verifier")
	forbiddenRec := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("wrong peer status=%d, want 403", forbiddenRec.Code)
	}

	badWaitReq := runtimeRequest(t, http.MethodPost, InternalDispatchClaimAnyPath+"?waitMs=5000", body, "spiffe://hvac.local/command-dispatcher")
	badWaitRec := httptest.NewRecorder()
	handler.ServeHTTP(badWaitRec, badWaitReq)
	if badWaitRec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-bounds waitMs status=%d, want 400", badWaitRec.Code)
	}

	stub.claimDispatchErr = ErrNoDispatchAvailable
	stub.claimVerificationErr = ErrVerificationNotAvailable
	idleRec := httptest.NewRecorder()
	handler.ServeHTTP(idleRec, runtimeRequest(t, http.MethodPost, InternalDispatchClaimAnyPath, body, "spiffe://hvac.local/command-dispatcher"))
	if idleRec.Code != http.StatusNoContent {
		t.Fatalf("idle dispatch claim-any status=%d, want 204", idleRec.Code)
	}

	stub.claimDispatchErr = errTransientSafetyEvaluation
	transientRec := httptest.NewRecorder()
	handler.ServeHTTP(transientRec, runtimeRequest(t, http.MethodPost, InternalDispatchClaimAnyPath, body, "spiffe://hvac.local/command-dispatcher"))
	if transientRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("transient safety eval claim-any status=%d, want 503", transientRec.Code)
	}
}
