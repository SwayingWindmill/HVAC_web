package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func TestLegacyRegistryRoutesUseConcreteActions(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	tests := map[string]string{
		"/api/v1/organizations":                                     "legacy:registry:organization.list",
		"/api/v1/organizations/" + fixtureOrganizationID:            "legacy:registry:organization.read",
		"/api/v1/organizations/" + fixtureOrganizationID + "/sites": "legacy:registry:site.list",
		"/api/v1/sites/" + fixtureSiteID:                            "legacy:registry:site.read",
		"/api/v1/sites/" + fixtureSiteID + "/equipment":             "legacy:registry:equipment.list",
		"/api/v1/equipment/" + fixtureEquipmentID:                   "legacy:registry:equipment.read",
		"/api/v1/sites/" + fixtureSiteID + "/devices":               "legacy:registry:device.list",
		"/api/v1/devices/" + fixtureDeviceID:                        "legacy:registry:device.read",
	}
	for path, expectedAction := range tests {
		route, ok := resolveLegacyRoute(path, now)
		if !ok || route.action != expectedAction || route.payload == nil {
			t.Fatalf("%s route=%#v ok=%v", path, route, ok)
		}
	}
	if _, ok := resolveLegacyRoute("/api/v1/telemetry", now); ok {
		t.Fatal("post-S1 Legacy route was exposed")
	}
}

func TestLegacyHandlerAcceptsSiteOnlyRegistryProjection(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	const spiffeID = "spiffe://hvac.local/platform-gateway"
	const audience = "legacy-hvac-backend"
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: spiffeID, Subject: "fixture-user", SubjectIssuer: "https://issuer.example.test",
		ExecutingService: spiffeID, Audience: audience, ActingOrganizationID: "018f1e00-9000-7000-8000-000000000001",
		Actions: []string{"legacy:registry:site.read"}, Scopes: []string{"site:" + fixtureSiteID}, PolicyRevision: "policy-v1",
		SessionID: "session-site-only", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "grant-site-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	spiffeURL, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatal(err)
	}
	peer := &x509.Certificate{PublicKey: &signer.PublicKey, URIs: []*url.URL{spiffeURL}}
	request := httptest.NewRequest(http.MethodGet, "https://legacy.test/api/v1/sites/"+fixtureSiteID, nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}, VerifiedChains: [][]*x509.Certificate{{peer}}}
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Route-Policy-Revision", "3")
	request.Header.Set("X-Request-ID", "request-site-only")
	response := httptest.NewRecorder()
	legacyHandler(spiffeID, audience, func() time.Time { return now }).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("site-only delegation status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLegacyHandlerRequiresWorkloadIdentityAndRestrictedDelegation(t *testing.T) {
	now := time.Date(2026, 7, 20, 7, 0, 0, 0, time.UTC)
	const spiffeID = "spiffe://hvac.local/platform-gateway"
	const audience = "legacy-hvac-backend"

	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer:               spiffeID,
		Subject:              "fixture-user",
		SubjectIssuer:        "https://issuer.example.test",
		ExecutingService:     spiffeID,
		Audience:             audience,
		ActingOrganizationID: "org-fixture-01",
		Actions:              []string{"legacy:platform-status:read"},
		Scopes:               []string{"organization:org-fixture-01"},
		PolicyRevision:       "policy-v1",
		SessionID:            "session-hash",
		IssuedAt:             now.Unix(),
		ExpiresAt:            now.Add(30 * time.Second).Unix(),
		TokenID:              "grant-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	spiffeURL, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatal(err)
	}
	peer := &x509.Certificate{PublicKey: &signer.PublicKey, URIs: []*url.URL{spiffeURL}}

	request := httptest.NewRequest(http.MethodGet, "https://legacy.test/api/v1/health", nil)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{peer},
		VerifiedChains:   [][]*x509.Certificate{{peer}},
	}
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("X-Route-Policy-Revision", "1")
	request.Header.Set("X-Request-ID", "request-01")

	response := httptest.NewRecorder()
	legacyHandler(spiffeID, audience, func() time.Time { return now }).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	withoutTLS := httptest.NewRequest(http.MethodGet, "https://legacy.test/api/v1/health", nil)
	withoutTLS.Header = request.Header.Clone()
	unauthorized := httptest.NewRecorder()
	legacyHandler(spiffeID, audience, func() time.Time { return now }).ServeHTTP(unauthorized, withoutTLS)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected direct caller rejection, got %d", unauthorized.Code)
	}

	forged := request.Clone(request.Context())
	forged.Header = request.Header.Clone()
	forged.Header.Set("X-Principal", "forged-user")
	badRequest := httptest.NewRecorder()
	legacyHandler(spiffeID, audience, func() time.Time { return now }).ServeHTTP(badRequest, forged)
	if badRequest.Code != http.StatusBadRequest {
		t.Fatalf("expected forged identity header rejection, got %d", badRequest.Code)
	}
}
