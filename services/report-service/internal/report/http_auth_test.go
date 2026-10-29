package report

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
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const testGatewaySPIFFE = "spiffe://hvac.local/platform-gateway"

// testGatewaySigner signs contexts the way the Gateway does.
var testGatewaySigner = func() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}()

func newTestHandler(t *testing.T, service *Service) http.Handler {
	t.Helper()
	handler, err := NewHTTPHandler(service, AuthConfig{GatewayPublicKey: &testGatewaySigner.PublicKey, GatewaySPIFFE: testGatewaySPIFFE})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// presented is a GET from peer carrying a context the Gateway signed for audience, action, Tenant and Site.
func presented(t *testing.T, path, peer, audience, action, tenantID, siteID string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	uri, _ := url.Parse(peer)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	now := time.Now()
	token, err := identitycontext.SignDelegation(testGatewaySigner, identitycontext.DelegationClaims{
		Issuer: testGatewaySPIFFE, Subject: "operator-1", SubjectIssuer: "https://identity.example.test",
		ExecutingService: peer, Audience: audience, TenantID: tenantID,
		Actions: []string{action}, Scopes: []string{"tenant:" + tenantID, "site:" + siteID},
		PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
		ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(ContextHeader, token)
	return request
}

// #613: report-service trusted any caller's X-Tenant-ID. It now serves only the Gateway, with a
// context the Gateway signed to read the Site, and takes the Tenant from that context.
func TestReportsServeOnlyTheGatewayWithAContextToReadTheSite(t *testing.T) {
	service, store, _ := testService(t, time.Date(2026, 10, 1, 3, 0, 0, 0, shanghai))
	if _, err := service.Generate(context.Background(), testTenant, testSite, "2026-09", nil); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, service)
	month := "/v1/sites/" + testSite + "/reports/monthly/2026-09"
	otherTenant := "01990000-5000-7000-8000-0000000000ee"
	otherSite := "01990000-5000-7000-8000-0000000000ff"
	uncontexted := presented(t, month, testGatewaySPIFFE, Audience, ReadAction, testTenant, testSite)
	uncontexted.Header.Del(ContextHeader)
	bare := httptest.NewRequest(http.MethodGet, month, nil)
	bare.Header.Set("X-Tenant-ID", testTenant)
	for name, request := range map[string]*http.Request{
		"another workload":              presented(t, month, "spiffe://hvac.local/telemetry-worker", Audience, ReadAction, testTenant, testSite),
		"the Gateway without a context": uncontexted,
		"a context for another owner":   presented(t, month, testGatewaySPIFFE, "mv-service", ReadAction, testTenant, testSite),
		"a context for another action":  presented(t, month, testGatewaySPIFFE, Audience, "report:write", testTenant, testSite),
		"a context for another Site":    presented(t, month, testGatewaySPIFFE, Audience, ReadAction, testTenant, otherSite),
		"a caller naming its Tenant":    bare,
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s read a report: %d", name, recorder.Code)
		}
	}

	for _, path := range []string{month, "/v1/sites/" + testSite + "/reports/monthly?year=2026"} {
		store.asked.readTenant = ""
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, presented(t, path, testGatewaySPIFFE, Audience, ReadAction, otherTenant, testSite))
		if recorder.Code == http.StatusForbidden || store.asked.readTenant != otherTenant {
			t.Fatalf("%s did not read the context's Tenant: %d, asked %q", path, recorder.Code, store.asked.readTenant)
		}
	}
}

// The container health check has no certificate; it asks the plain diagnostics port, which answers nothing else.
func TestTheHealthCheckIsOnTheDiagnosticsPortAlone(t *testing.T) {
	recorder := httptest.NewRecorder()
	HealthHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health = %d", recorder.Code)
	}
	metrics := httptest.NewRecorder()
	HealthHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "hvac_service_up") {
		t.Fatalf("metrics = %d %s", metrics.Code, metrics.Body.String())
	}
	service, _, _ := testService(t, time.Date(2026, 10, 1, 3, 0, 0, 0, shanghai))
	business := httptest.NewRecorder()
	newTestHandler(t, service).ServeHTTP(business, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if business.Code == http.StatusOK {
		t.Fatal("the business port still answers the health check")
	}
	report := httptest.NewRecorder()
	HealthHandler().ServeHTTP(report, httptest.NewRequest(http.MethodGet, "/v1/sites/"+testSite+"/reports/monthly?year=2026", nil))
	if report.Code == http.StatusOK {
		t.Fatal("the diagnostics port serves reports")
	}
}
