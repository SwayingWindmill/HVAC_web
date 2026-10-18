package fdd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	testGatewaySPIFFE = "spiffe://hvac.local/platform-gateway"
	testAgentSPIFFE   = "spiffe://hvac.local/operations-agent-service"
)

// authFixture signs contexts the way the Gateway does and presents them from a workload peer.
type authFixture struct {
	signer *ecdsa.PrivateKey
	now    time.Time
}

func newAuthFixture(t *testing.T, now time.Time) authFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return authFixture{signer: key, now: now}
}

func (fixture authFixture) config() AuthConfig {
	return AuthConfig{
		GatewayPublicKey: &fixture.signer.PublicKey, GatewaySPIFFE: testGatewaySPIFFE,
		ReaderSPIFFE: testAgentSPIFFE, Now: func() time.Time { return fixture.now },
	}
}

func (fixture authFixture) context(t *testing.T, executing, action, tenantID, siteID string) string {
	t.Helper()
	token, err := identitycontext.SignDelegation(fixture.signer, identitycontext.DelegationClaims{
		Issuer: testGatewaySPIFFE, Subject: "operator", SubjectIssuer: "https://identity.example.test",
		DisplayName: "Operator", ExecutingService: executing, Audience: DefaultAudience,
		TenantID: tenantID, Actions: []string{action}, Scopes: []string{"tenant:" + tenantID, "site:" + siteID},
		PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: fixture.now.Add(-time.Second).Unix(),
		ExpiresAt: fixture.now.Add(30 * time.Second).Unix(), TokenID: "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func presentedBy(request *http.Request, peer string) *http.Request {
	uri, _ := url.Parse(peer)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	return request
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// #595: FDD trusted any caller's X-Tenant-ID. It now serves only a Gateway-signed context naming
// the workload that presents it, and takes the Tenant from that context.
func TestFDDServesOnlyAGatewaySignedContextNamingItsPresenter(t *testing.T) {
	input := validEvaluation()
	fixture := newAuthFixture(t, input.At)
	store := newStore()
	handler, err := NewHTTPHandler(newTestService(t, store, &loopEvidence{}, func() time.Time { return input.At }), fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	findings := "/v1/sites/" + input.SiteID + "/fdd/findings"
	list := func(peer, executing, tenantID, siteID string) int {
		request := presentedBy(httptest.NewRequest(http.MethodGet, findings, nil), peer)
		request.Header.Set(ContextHeader, fixture.context(t, executing, ReadAction, tenantID, siteID))
		return serve(handler, request).Code
	}
	if status := list(testGatewaySPIFFE, testGatewaySPIFFE, input.TenantID, input.SiteID); status != http.StatusOK {
		t.Fatalf("the Gateway could not read findings with its context: %d", status)
	}
	if status := list(testAgentSPIFFE, testAgentSPIFFE, input.TenantID, input.SiteID); status != http.StatusOK {
		t.Fatalf("the Agent could not list findings with a context naming it: %d", status)
	}
	if status := list(testGatewaySPIFFE, testAgentSPIFFE, input.TenantID, input.SiteID); status != http.StatusForbidden {
		t.Fatalf("the Gateway presented a context naming the Agent: %d", status)
	}
	if status := list(testAgentSPIFFE, testGatewaySPIFFE, input.TenantID, input.SiteID); status != http.StatusForbidden {
		t.Fatalf("the Agent presented the Gateway's own context: %d", status)
	}
	if status := list(testGatewaySPIFFE, testGatewaySPIFFE, input.TenantID, "01990000-5000-7000-8000-0000000000ff"); status != http.StatusForbidden {
		t.Fatalf("a context for another Site read this Site: %d", status)
	}

	// A caller-supplied Tenant header is no longer an identity.
	bare := presentedBy(httptest.NewRequest(http.MethodGet, findings, nil), testGatewaySPIFFE)
	bare.Header.Set("X-Tenant-ID", input.TenantID)
	if status := serve(handler, bare).Code; status != http.StatusForbidden {
		t.Fatalf("a request with only X-Tenant-ID was served: %d", status)
	}
}

// The Agent may only list a Site's findings; reading the rule, evaluating or linking stays the Gateway's.
func TestFDDLetsTheReaderWorkloadOnlyListFindings(t *testing.T) {
	input := validEvaluation()
	fixture := newAuthFixture(t, input.At)
	store := newStore()
	handler, err := NewHTTPHandler(newTestService(t, store, &loopEvidence{readings: validReadings(input)}, func() time.Time { return input.At }), fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"tenantId": input.TenantID, "siteId": input.SiteID, "assetId": input.AssetID, "deviceId": input.DeviceID})
	for _, refused := range []struct {
		method, path, action string
		body                 io.Reader
	}{
		{http.MethodGet, "/v1/sites/" + input.SiteID + "/fdd/rules/low-delta-t", ReadAction, nil},
		{http.MethodPost, "/v1/fdd/evaluate/low-delta-t", WriteAction, bytes.NewReader(body)},
		{http.MethodPatch, "/v1/sites/" + input.SiteID + "/fdd/findings/01990000-7000-7000-8000-000000000001/links", WriteAction, bytes.NewReader([]byte(`{}`))},
	} {
		request := presentedBy(httptest.NewRequest(refused.method, refused.path, refused.body), testAgentSPIFFE)
		request.Header.Set(ContextHeader, fixture.context(t, testAgentSPIFFE, refused.action, input.TenantID, input.SiteID))
		if status := serve(handler, request).Code; status != http.StatusForbidden {
			t.Fatalf("the Agent reached %s %s: %d", refused.method, refused.path, status)
		}
	}
	if len(store.findings) != 0 {
		t.Fatalf("a refused request evaluated: %#v", store.findings)
	}

	// An evaluation must name the Tenant and Site its context covers.
	evaluate := presentedBy(httptest.NewRequest(http.MethodPost, "/v1/fdd/evaluate/low-delta-t", bytes.NewReader(body)), testGatewaySPIFFE)
	evaluate.Header.Set(ContextHeader, fixture.context(t, testGatewaySPIFFE, WriteAction, input.TenantID, "01990000-5000-7000-8000-0000000000ff"))
	if status := serve(handler, evaluate).Code; status != http.StatusForbidden || len(store.findings) != 0 {
		t.Fatalf("an evaluation outside its context ran: %d", status)
	}
}
