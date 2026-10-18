package workorderservice

import (
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
	routerGatewaySPIFFE = "spiffe://hvac.local/platform-gateway"
	routerAgentSPIFFE   = "spiffe://hvac.local/operations-agent-service"
)

func presentedBy(request *http.Request, peer string) *http.Request {
	uri, _ := url.Parse(peer)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	return request
}

// #595: the Operations Agent may list a Site's work orders and nothing else; every other caller
// but the Gateway is refused before the handler.
func TestOwnerRouterLetsAReaderWorkloadOnlyListASitesWorkOrders(t *testing.T) {
	calls := 0
	router, err := NewOwnerRouter(OwnerRouterConfig{
		GatewaySPIFFE: routerGatewaySPIFFE,
		ReaderSPIFFE:  routerAgentSPIFFE,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			calls++
			writer.WriteHeader(http.StatusNoContent)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	site := InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders"
	serve := func(method, path, peer string) int {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, presentedBy(httptest.NewRequest(method, path, nil), peer))
		return recorder.Code
	}
	if status := serve(http.MethodGet, site+"?status=OPEN", routerAgentSPIFFE); status != http.StatusNoContent || calls != 1 {
		t.Fatalf("reader could not list a Site's work orders: status=%d calls=%d", status, calls)
	}
	for _, refused := range []struct{ method, path string }{
		{http.MethodGet, site + "/" + httpTestWorkOrderID},
		{http.MethodPost, site},
		{http.MethodPost, site + "/" + httpTestWorkOrderID + ":assign"},
		{http.MethodPost, site + "/" + httpTestWorkOrderID + ":start"},
	} {
		if status := serve(refused.method, refused.path, routerAgentSPIFFE); status != http.StatusForbidden || calls != 1 {
			t.Fatalf("reader reached %s %s: status=%d calls=%d", refused.method, refused.path, status, calls)
		}
	}
	if status := serve(http.MethodGet, site, "spiffe://hvac.local/report-service"); status != http.StatusForbidden || calls != 1 {
		t.Fatalf("an unknown workload reached the handler: %d", status)
	}
	if status := serve(http.MethodPost, site, routerGatewaySPIFFE); status != http.StatusNoContent || calls != 2 {
		t.Fatalf("the Gateway was refused: %d", status)
	}
}

// A list context the Gateway signs for the Agent names it; only the Agent may present it, and the
// Agent may not present the Gateway's own contexts.
func TestWorkOrderHTTPAcceptsAListContextOnlyFromTheWorkloadItNames(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	handler, err := NewHTTPHandler(HTTPConfig{Store: &fakeStore{item: validHTTPWorkOrder()}, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	list := func(peer, executing string) int {
		token, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
			Issuer: DefaultGatewaySPIFFEID, Subject: "operator", SubjectIssuer: "https://identity.example.test",
			DisplayName: "Operator", ExecutingService: executing, Audience: DefaultAudience,
			TenantID: httpTestTenantID, Actions: []string{WorkOrderListAction}, Scopes: []string{"tenant:" + httpTestTenantID, "site:" + httpTestSiteID},
			PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
			ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "token-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := presentedBy(httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil), peer)
		request.Header.Set(WorkOrderReadContextHeader, token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	if status := list(routerAgentSPIFFE, routerAgentSPIFFE); status != http.StatusOK {
		t.Fatalf("the Agent could not present its own list context: %d", status)
	}
	if status := list(routerGatewaySPIFFE, routerAgentSPIFFE); status != http.StatusForbidden {
		t.Fatalf("the Gateway presented a context naming the Agent: %d", status)
	}
	if status := list(routerAgentSPIFFE, routerGatewaySPIFFE); status != http.StatusForbidden {
		t.Fatalf("the Agent presented the Gateway's own context: %d", status)
	}
}
