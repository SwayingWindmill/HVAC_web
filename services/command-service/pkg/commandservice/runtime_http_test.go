package commandservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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
	claimedOrganization  string
	claimedSite          string
	claimedDevice        string
	resolvedDispatch     bool
	resolvedVerification bool
	preparedEvidence     bool
	completedEvidence    bool
}

func (stub *runtimeStoreStub) ClaimDispatchForCohort(_ context.Context, organizationID, siteID, deviceID string, _ commandmodel.Capability, _ string, _ time.Duration) (commandmodel.DispatchEnvelope, error) {
	stub.claimedOrganization = organizationID
	stub.claimedSite = siteID
	stub.claimedDevice = deviceID
	return stub.dispatchEnvelope, stub.claimDispatchErr
}

func (stub *runtimeStoreStub) ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error {
	stub.resolvedDispatch = true
	return nil
}

func (stub *runtimeStoreStub) ClaimVerificationForCohort(_ context.Context, organizationID, siteID, deviceID string, _ commandmodel.Capability, _ string, _ time.Duration) (commandmodel.VerificationEnvelope, error) {
	stub.claimedOrganization = organizationID
	stub.claimedSite = siteID
	stub.claimedDevice = deviceID
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

func TestRuntimeHTTPDispatcherCanClaimAndResolveExactCohort(t *testing.T) {
	stub := &runtimeStoreStub{dispatchEnvelope: commandmodel.DispatchEnvelope{
		CommandID: "command-1", AttemptID: "attempt-1", OrganizationID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: runtimeTestDevice,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: setpointCapabilityRevision,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 22}, PayloadHash: "hash", ExecutionFence: 1, DeviceCommandSequence: 1,
		LeaseOwner: "dispatcher-a", LeaseUntil: time.Now().UTC().Add(30 * time.Second),
	}}
	handler := runtimeTestHandler(t, stub)

	claim := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"leaseOwner":"dispatcher-a","leaseSeconds":30}`, "spiffe://hvac.local/command-dispatcher")
	claimRecorder := httptest.NewRecorder()
	handler.ServeHTTP(claimRecorder, claim)
	if claimRecorder.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimRecorder.Code, claimRecorder.Body.String())
	}
	if stub.claimedOrganization != runtimeTestOrganization || stub.claimedSite != runtimeTestSite || stub.claimedDevice != runtimeTestDevice {
		t.Fatalf("claimed cohort=%s/%s/%s", stub.claimedOrganization, stub.claimedSite, stub.claimedDevice)
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
		request := runtimeRequest(t, http.MethodPost, test.path, `{"leaseOwner":"worker-a","leaseSeconds":15}`, test.spiffe)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("path=%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRuntimeHTTPRejectsClientSelectedCohortAndCrossCohortResolution(t *testing.T) {
	stub := &runtimeStoreStub{}
	handler := runtimeTestHandler(t, stub)

	claim := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"organizationId":"other-org","leaseOwner":"dispatcher-a","leaseSeconds":15}`, "spiffe://hvac.local/command-dispatcher")
	claimRecorder := httptest.NewRecorder()
	handler.ServeHTTP(claimRecorder, claim)
	if claimRecorder.Code != http.StatusBadRequest || stub.claimedOrganization != "" {
		t.Fatalf("claim status=%d claimed=%q body=%s", claimRecorder.Code, stub.claimedOrganization, claimRecorder.Body.String())
	}

	body, err := json.Marshal(runtimeDispatchResolveRequest{
		Envelope: commandmodel.DispatchEnvelope{OrganizationID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: "other-device"},
		Result:   commandmodel.ConnectorResult{Phase: commandmodel.ConnectorPreSendRejected},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolve := runtimeRequest(t, http.MethodPost, InternalDispatchResolvePath, string(body), "spiffe://hvac.local/command-dispatcher")
	resolveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(resolveRecorder, resolve)
	if resolveRecorder.Code != http.StatusBadRequest || stub.resolvedDispatch {
		t.Fatalf("resolve status=%d resolved=%v body=%s", resolveRecorder.Code, stub.resolvedDispatch, resolveRecorder.Body.String())
	}
}

func TestRuntimeHTTPRejectsWrongWorkloadIdentity(t *testing.T) {
	stub := &runtimeStoreStub{claimDispatchErr: errors.New("must not be reached")}
	handler := runtimeTestHandler(t, stub)
	request := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"leaseOwner":"verifier-a","leaseSeconds":15}`, "spiffe://hvac.local/command-verifier")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRuntimeHTTPSelectsExactMultiCohortByWorkloadIdentity(t *testing.T) {
	secondDevice := "018f3e00-3000-7000-8000-000000000002"
	stub := &runtimeStoreStub{claimDispatchErr: ErrNoDispatchAvailable, claimVerificationErr: ErrVerificationNotAvailable}
	handler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store: stub,
		Cohorts: []RuntimeCohort{
			{
				DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher/ahu-01",
				VerifierSPIFFE:   "spiffe://hvac.local/command-verifier/ahu-01",
				OrganizationID:   runtimeTestOrganization,
				SiteID:           runtimeTestSite,
				DeviceID:         runtimeTestDevice,
				Capability:       commandmodel.CapabilitySetTemperatureSetpoint,
			},
			{
				DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher/fcu-02",
				VerifierSPIFFE:   "spiffe://hvac.local/command-verifier/fcu-02",
				OrganizationID:   runtimeTestOrganization,
				SiteID:           runtimeTestSite,
				DeviceID:         secondDevice,
				Capability:       commandmodel.CapabilitySetTemperatureSetpoint,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	dispatch := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"leaseOwner":"dispatcher-fcu","leaseSeconds":15}`, "spiffe://hvac.local/command-dispatcher/fcu-02")
	dispatchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(dispatchRecorder, dispatch)
	if dispatchRecorder.Code != http.StatusNoContent || stub.claimedDevice != secondDevice {
		t.Fatalf("dispatch status=%d claimedDevice=%q body=%s", dispatchRecorder.Code, stub.claimedDevice, dispatchRecorder.Body.String())
	}

	verification := runtimeRequest(t, http.MethodPost, InternalVerificationClaimPath, `{"leaseOwner":"verifier-ahu","leaseSeconds":15}`, "spiffe://hvac.local/command-verifier/ahu-01")
	verificationRecorder := httptest.NewRecorder()
	handler.ServeHTTP(verificationRecorder, verification)
	if verificationRecorder.Code != http.StatusNoContent || stub.claimedDevice != runtimeTestDevice {
		t.Fatalf("verification status=%d claimedDevice=%q body=%s", verificationRecorder.Code, stub.claimedDevice, verificationRecorder.Body.String())
	}

	wrongRole := runtimeRequest(t, http.MethodPost, InternalDispatchClaimPath, `{"leaseOwner":"wrong-role","leaseSeconds":15}`, "spiffe://hvac.local/command-verifier/fcu-02")
	wrongRoleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRoleRecorder, wrongRole)
	if wrongRoleRecorder.Code != http.StatusForbidden {
		t.Fatalf("wrong role status=%d body=%s", wrongRoleRecorder.Code, wrongRoleRecorder.Body.String())
	}
}

func TestRuntimeHTTPRecordsBoundedVerificationMetrics(t *testing.T) {
	registry := observability.NewRegistry()
	stub := &runtimeStoreStub{}
	handler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store: stub, Metrics: registry,
		DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher", VerifierSPIFFE: "spiffe://hvac.local/command-verifier",
		OrganizationID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: runtimeTestDevice,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := commandmodel.VerificationEnvelope{
		OrganizationID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: runtimeTestDevice,
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

func runtimeTestHandler(t *testing.T, stub RuntimeStore) http.Handler {
	t.Helper()
	handler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store: stub, DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher", VerifierSPIFFE: "spiffe://hvac.local/command-verifier",
		OrganizationID: runtimeTestOrganization, SiteID: runtimeTestSite, DeviceID: runtimeTestDevice,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint,
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
