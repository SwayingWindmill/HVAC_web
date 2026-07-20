package iam_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func TestIAMAcceptsOnlyVerifiedGatewayDelegation(t *testing.T) {
	harness := newIAMHarness(t)
	request := harness.request(t, validIAMClaims(harness.now), harness.signer)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var principal identitycontext.InternalPrincipalResponse
	if err := json.NewDecoder(recorder.Body).Decode(&principal); err != nil {
		t.Fatal(err)
	}
	if principal.Principal.Subject != "fixture-user" || principal.Context.ExecutingServicePrincipal.SPIFFEID != harness.spiffeID {
		t.Fatalf("unexpected actor chain: %#v", principal)
	}
}

func TestIAMRejectsUnverifiedWorkloadAndForgedHeaders(t *testing.T) {
	harness := newIAMHarness(t)

	withoutTLS := httptest.NewRequest(http.MethodPost, iam.CurrentPrincipalPath, nil)
	withoutTLS.Header.Set("X-Delegation-Grant", "not-relevant")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, withoutTLS)
	assertIAMProblem(t, recorder, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID")

	forged := harness.request(t, validIAMClaims(harness.now), harness.signer)
	forged.Header.Set("X-Admin", "true")
	recorder = httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, forged)
	assertIAMProblem(t, recorder, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER")
}

func TestIAMRejectsExpandedForwardedAndInvalidDelegation(t *testing.T) {
	harness := newIAMHarness(t)
	cases := []struct {
		name   string
		mutate func(*identitycontext.DelegationClaims)
	}{
		{name: "wrong audience", mutate: func(claims *identitycontext.DelegationClaims) { claims.Audience = "audit-service" }},
		{name: "expanded actions", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.Actions = []string{"principal:read", "session:revoke"}
		}},
		{name: "expanded scopes", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.Scopes = []string{"session:session-01", "organization:*"}
		}},
		{name: "forwarded by IAM", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.ExecutingService = "spiffe://hvac.local/iam-service"
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			claims := validIAMClaims(harness.now)
			testCase.mutate(&claims)
			recorder := httptest.NewRecorder()
			harness.handler.ServeHTTP(recorder, harness.request(t, claims, harness.signer))
			assertIAMProblem(t, recorder, http.StatusForbidden, "IAM_DELEGATION_REJECTED")
		})
	}

	otherSigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, validIAMClaims(harness.now), otherSigner))
	assertIAMProblem(t, recorder, http.StatusUnauthorized, "IAM_DELEGATION_INVALID")
}

type iamHarness struct {
	handler  http.Handler
	now      time.Time
	spiffeID string
	cert     *x509.Certificate
	signer   crypto.Signer
}

func newIAMHarness(t *testing.T) iamHarness {
	t.Helper()
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	bundle, err := testpki.Generate("spiffe://hvac.local/iam-service", "spiffe://hvac.local/platform-gateway", now)
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
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("test client key is not a signer")
	}
	return iamHarness{
		handler: iam.NewHandler(iam.Config{AllowedWorkloadSPIFFE: bundle.ClientSPIFFEID, Audience: "iam-service", Now: func() time.Time { return now }}),
		now:     now, spiffeID: bundle.ClientSPIFFEID, cert: certificate, signer: signer,
	}
}

func (h iamHarness) request(t *testing.T, claims identitycontext.DelegationClaims, signer crypto.Signer) *http.Request {
	t.Helper()
	grant, err := identitycontext.SignDelegation(signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, iam.CurrentPrincipalPath, nil)
	request.Header.Set("X-Delegation-Grant", grant)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{h.cert},
		VerifiedChains:   [][]*x509.Certificate{{h.cert}},
	}
	return request
}

func validIAMClaims(now time.Time) identitycontext.DelegationClaims {
	return identitycontext.DelegationClaims{
		Issuer: "spiffe://hvac.local/platform-gateway", Subject: "fixture-user", SubjectIssuer: "https://issuer.example.test",
		DisplayName: "Fixture User", Email: "fixture@example.test", Roles: []string{"operator"},
		ExecutingService: "spiffe://hvac.local/platform-gateway", Audience: "iam-service", ActingOrganizationID: "org-01",
		Actions: []string{"principal:read"}, Scopes: []string{"session:session-01"}, PolicyRevision: "policy-v1",
		SessionID: "session-01", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "grant-01",
	}
}

func assertIAMProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != code {
		t.Fatalf("code = %q, want %q", problem.Code, code)
	}
}
