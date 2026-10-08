package iam_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

const (
	projectorWorkload    = "spiffe://hvac.local/analytics-read-model-projector"
	workloadTrustDomain  = "spiffe://hvac.local"
	projectorPrincipalID = "018f1e00-2000-7000-8000-0000000000aa"
)

func TestIAMIssuesWorkloadPrincipalItsOwnRegistryGrant(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = workloadPrincipalStore{}
	})
	recorder := harness.workloadDecision(t, projectorWorkload, `{"tenantId":"`+iam.S1FixtureTenantAID+`","action":"meter-binding.resolve"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response registryauth.DecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Decision.Allowed || response.Decision.ReasonCode != registryauth.ReasonAllowTenantRole {
		t.Fatalf("unexpected Workload Principal decision: %#v", response.Decision)
	}
	if response.DelegationGrantExpiresAt != "2026-07-21T12:00:30Z" {
		t.Fatalf("grant expiry = %q", response.DelegationGrantExpiresAt)
	}
	grant, err := registryauth.VerifyGrant(harness.iamSigner.Public(), response.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryauth.ValidateGrant(grant, registryauth.GrantValidation{
		Now:                   harness.now,
		Issuer:                harness.iamSPIFFEID,
		Presenter:             projectorWorkload,
		Audience:              fixtureCoreAudience,
		Action:                registryauth.ActionMeterBindingResolve,
		CurrentPolicyRevision: iam.S1FixturePolicyRevision,
		IsRevoked:             func(string) (bool, error) { return false, nil },
	}); err != nil {
		t.Fatalf("Workload Principal grant invalid at Core: %v", err)
	}
	if grant.PrincipalID != projectorPrincipalID || grant.Subject != projectorWorkload || grant.SessionID != "" {
		t.Fatalf("grant does not name the Workload Principal itself: %#v", grant)
	}
	if !registryauth.ScopeAllows(grant, iam.S1FixtureOwnerASite1ID) {
		t.Fatal("tenant role binding did not reach the Tenant's Site")
	}
}

func TestIAMDeniesUnregisteredWorkloadWithoutGrant(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = workloadPrincipalStore{}
	})
	recorder := harness.workloadDecision(t, "spiffe://hvac.local/notification-service", `{"tenantId":"`+iam.S1FixtureTenantAID+`","action":"meter-binding.resolve"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response registryauth.DecisionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Decision.Allowed || response.Decision.ReasonCode != registryauth.ReasonDenyPrincipalNotFound || response.DelegationGrant != "" {
		t.Fatalf("unregistered workload was authorized: %#v", response)
	}
}

func TestIAMWorkloadDecisionRejectsDelegatedOrRedirectedRequests(t *testing.T) {
	harness := newIAMHarnessWithConfig(t, func(config *iam.Config) {
		config.AuthorizationStore = workloadPrincipalStore{}
	})
	withDelegation := harness.workloadRequest(t, projectorWorkload, `{"tenantId":"`+iam.S1FixtureTenantAID+`","action":"meter-binding.resolve"}`)
	withDelegation.Header.Set("X-Delegation-Grant", "session-delegation")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, withDelegation)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER")

	recorder = harness.workloadDecision(t, projectorWorkload, `{"tenantId":"`+iam.S1FixtureTenantAID+`","action":"meter-binding.resolve","grantPresenter":"`+fixtureOperationsPresenter+`"}`)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID")
}

func (h iamHarness) workloadDecision(t *testing.T, workload, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, h.workloadRequest(t, workload, body))
	return recorder
}

func (h iamHarness) workloadRequest(t *testing.T, workload, body string) *http.Request {
	t.Helper()
	bundle, err := testpki.Generate("spiffe://hvac.local/iam-service", workload, h.now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, iam.RegistryWorkloadDecisionPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate},
		VerifiedChains:   [][]*x509.Certificate{{certificate}},
	}
	return request
}

// workloadPrincipalStore knows only the projector's Workload Principal, so a
// decision for any other identity proves IAM looked up the calling workload.
type workloadPrincipalStore struct{}

func (workloadPrincipalStore) LookupRegistryAuthorization(_ context.Context, lookup iam.AuthorizationLookup) (iam.AuthorizationFacts, error) {
	if lookup.SubjectIssuer != workloadTrustDomain || lookup.Subject != projectorWorkload {
		return iam.AuthorizationFacts{Found: false, PolicyRevision: iam.S1FixturePolicyRevision}, nil
	}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return iam.AuthorizationFacts{
		Found:          true,
		PolicyRevision: iam.S1FixturePolicyRevision,
		Principal:      iam.PrincipalRecord{ID: projectorPrincipalID, SubjectIssuer: workloadTrustDomain, Subject: projectorWorkload, Status: iam.FactStatusActive},
		TenantSiteIDs:  []string{iam.S1FixtureOwnerASite1ID},
		Memberships:    []iam.TenantMembership{{TenantID: iam.S1FixtureTenantAID, Status: iam.FactStatusActive, ValidFrom: since}},
		RoleBindings: []iam.RoleBinding{{
			TenantID: iam.S1FixtureTenantAID, RoleKey: "energy-projection", Actions: []registryauth.Action{registryauth.ActionMeterBindingResolve},
			Effect: iam.BindingEffectAllow, Status: iam.FactStatusActive, ValidFrom: since,
		}},
	}, nil
}
