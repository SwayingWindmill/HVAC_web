package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// ownerUpstream records what the Gateway sends an intelligence owner and answers an empty list.
func ownerUpstream(seen *atomic.Pointer[http.Request]) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen.Store(request)
		return telemetryJSONResponse(http.StatusOK, map[string]any{"items": []any{}}), nil
	})}
}

// #595: FDD no longer trusts a Tenant header; the pages' reads carry a context the Gateway signs
// for the Site, naming the Gateway as the workload presenting it.
func TestFDDReadsCarryAGatewaySignedContextInsteadOfATenantHeader(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)

	controller := fixture.handler.(*handler)
	stored, err := controller.identity.store.GetSession(context.Background(), fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/fdd/findings?limit=100", nil)
	response := httptest.NewRecorder()
	controller.serveFDDRead(response, request, bffSession{Session: stored}, publicIntelligenceRoute{siteID: fixture.siteID, target: "fdd"})
	if response.Code != http.StatusOK {
		t.Fatalf("findings read got %d: %s", response.Code, response.Body.String())
	}
	upstream := fixture.lastOwner.Load()
	if upstream == nil || upstream.Header.Get("X-Tenant-ID") != "" {
		t.Fatalf("FDD was called with a Tenant header or not at all: %v", upstream)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, upstream.Header.Get("X-FDD-Context"))
	if err != nil {
		t.Fatalf("FDD context is not the Gateway's: %v", err)
	}
	if claims.ExecutingService != "spiffe://hvac.local/platform-gateway" || claims.Audience != "fdd-service" ||
		len(claims.Actions) != 1 || claims.Actions[0] != "fdd:read" ||
		len(claims.Scopes) != 2 || claims.Scopes[0] != "tenant:"+fixture.tenantID || claims.Scopes[1] != "site:"+fixture.siteID {
		t.Fatalf("FDD context is not exact: %+v", claims)
	}
}

// #595: the Agent lists the operator's Site's findings from FDD with a context naming it.
func TestOperationsToolAuthorizationGrantsFDDFindingsOnlyForTheOperatorsSite(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	serviceGrant := fixture.serviceDelegation(t)
	body := func(requestID, siteID string) string {
		return `{"sessionId":"session-001","runId":"run-001","request":{"requestId":"` + requestID + `","tool":"fdd.listFindings","input":{"siteId":"` + siteID + `","limit":50}}}`
	}
	response := fixture.authorizeTool(t, body("fdd-001", fixture.siteID), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if response.Code != http.StatusOK {
		t.Fatalf("expected an FDD findings grant, got %d: %s", response.Code, response.Body.String())
	}
	var issued operationsToolAuthorizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, issued.DelegationGrant)
	if err != nil {
		t.Fatalf("invalid FDD delegation: %v", err)
	}
	if claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" || claims.Audience != "fdd-service" ||
		len(claims.Actions) != 1 || claims.Actions[0] != "fdd:read" ||
		len(claims.Scopes) != 2 || claims.Scopes[1] != "site:"+fixture.siteID {
		t.Fatalf("FDD findings grant is not exact: %+v", claims)
	}

	decisions := fixture.iamCalls.Load()
	elsewhere := fixture.authorizeTool(t, body("fdd-002", "018f1e00-1000-7000-8000-0000000000ff"), serviceGrant, "spiffe://hvac.local/operations-agent-service")
	if elsewhere.Code != http.StatusForbidden || strings.Contains(elsewhere.Body.String(), "delegationGrant") || fixture.iamCalls.Load() != decisions {
		t.Fatalf("another Site's findings got %d (IAM calls %d→%d)", elsewhere.Code, decisions, fixture.iamCalls.Load())
	}

	fixture.denySite.Store(true)
	if denied := fixture.authorizeTool(t, body("fdd-003", fixture.siteID), serviceGrant, "spiffe://hvac.local/operations-agent-service"); denied.Code != http.StatusNotFound {
		t.Fatalf("a Site the operator cannot see must be not found, got %d", denied.Code)
	}
}
