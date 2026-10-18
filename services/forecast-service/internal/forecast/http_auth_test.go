package forecast

import (
	"context"
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

const (
	testGatewaySPIFFE = "spiffe://hvac.local/platform-gateway"
	testTenant        = "01990000-3000-7000-8000-000000000001"
	testOtherTenant   = "01990000-3000-7000-8000-000000000002"
	testSite          = "01990000-5000-7000-8000-000000000001"
)

// oneTenantStore holds a published forecast for testTenant's testSite only.
type oneTenantStore struct{ memoryPublicationStore }

func (store *oneTenantStore) LatestForecast(_ context.Context, tenantID, siteID, _ string) (ForecastSnapshotReference, error) {
	if tenantID != testTenant || siteID != testSite {
		return ForecastSnapshotReference{}, ErrForecastNotFound
	}
	return ForecastSnapshotReference{SnapshotID: "snapshot-1", ForecastJobID: "job-1"}, nil
}

// #612: Forecast trusted any caller's X-Tenant-ID. It now serves only the Gateway, with a context
// the Gateway signed for the Site, and takes the Tenant from that context.
func TestForecastServesOnlyTheGatewayWithAContextForTheSite(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(&captureSink{}, &oneTenantStore{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(service, AuthConfig{GatewayPublicKey: &signer.PublicKey, GatewaySPIFFE: testGatewaySPIFFE, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	read := func(peer, presenter, audience, action, tenantID, siteID string) int {
		request := httptest.NewRequest(http.MethodGet, "/v1/sites/"+testSite+"/forecast/load", nil)
		uri, _ := url.Parse(peer)
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
		// The header a caller could always write; it must decide nothing.
		request.Header.Set("X-Tenant-ID", testTenant)
		token, signErr := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
			Issuer: testGatewaySPIFFE, Subject: "operator", SubjectIssuer: "https://identity.example.test",
			ExecutingService: presenter, Audience: audience, TenantID: tenantID,
			Actions: []string{action}, Scopes: []string{"tenant:" + tenantID, "site:" + siteID},
			PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
			ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-1",
		})
		if signErr != nil {
			t.Fatal(signErr)
		}
		request.Header.Set(ContextHeader, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}

	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, Audience, ReadAction, testTenant, testSite); status != http.StatusOK {
		t.Fatalf("the Gateway could not read the Site's forecast with its context: %d", status)
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, Audience, ReadAction, testOtherTenant, testSite); status != http.StatusNotFound {
		t.Fatalf("another Tenant's context read this Tenant's forecast: %d", status)
	}
	other := "spiffe://hvac.local/telemetry-worker"
	if status := read(other, other, Audience, ReadAction, testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("another workload read the forecast: %d", status)
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, Audience, ReadAction, testTenant, "01990000-5000-7000-8000-0000000000ff"); status != http.StatusForbidden {
		t.Fatalf("a context for another Site read this Site: %d", status)
	}
	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, Audience, "forecast:write", testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("a context for another action was accepted: %d", status)
	}

	if status := read(testGatewaySPIFFE, testGatewaySPIFFE, "fdd-service", ReadAction, testTenant, testSite); status != http.StatusForbidden {
		t.Fatalf("a context for another owner was accepted: %d", status)
	}
	uncontexted := httptest.NewRequest(http.MethodGet, "/v1/sites/"+testSite+"/forecast/load", nil)
	gateway, _ := url.Parse(testGatewaySPIFFE)
	uncontexted.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{gateway}}}}
	uncontexted.Header.Set("X-Tenant-ID", testTenant)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, uncontexted)
	if response.Code != http.StatusForbidden {
		t.Fatalf("the Gateway's workload was served without a context: %d", response.Code)
	}

	bare := httptest.NewRequest(http.MethodGet, "/v1/sites/"+testSite+"/forecast/pv", nil)
	bare.Header.Set("X-Tenant-ID", testTenant)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, bare)
	if response.Code != http.StatusForbidden {
		t.Fatalf("a request with only X-Tenant-ID was served: %d", response.Code)
	}
}
