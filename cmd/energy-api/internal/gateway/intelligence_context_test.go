package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// requireOwnerContext checks the Gateway's last call to an intelligence owner carried a context it
// signed for exactly this owner, action, Tenant and Site, and no header the owner used to trust.
func requireOwnerContext(t *testing.T, fixture *operationsGatewayFixture, header, audience, action string) identitycontext.DelegationClaims {
	t.Helper()
	upstream := fixture.lastOwner.Load()
	if upstream == nil {
		t.Fatal("the owner was not called")
	}
	for _, trusted := range []string{"X-Tenant-ID", "X-Actor-ID", "X-Authorized-Site-IDs"} {
		if upstream.Header.Get(trusted) != "" {
			t.Fatalf("the owner was sent %s", trusted)
		}
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, upstream.Header.Get(header))
	if err != nil {
		t.Fatalf("%s is not the Gateway's: %v", header, err)
	}
	if claims.ExecutingService != "spiffe://hvac.local/platform-gateway" || claims.Audience != audience ||
		len(claims.Actions) != 1 || claims.Actions[0] != action ||
		len(claims.Scopes) != 2 || claims.Scopes[0] != "tenant:"+fixture.tenantID || claims.Scopes[1] != "site:"+fixture.siteID {
		t.Fatalf("%s is not exact: %+v", header, claims)
	}
	return claims
}

func (fixture *operationsGatewayFixture) session(t *testing.T) bffSession {
	t.Helper()
	stored, err := fixture.handler.(*handler).identity.store.GetSession(context.Background(), fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return bffSession{Session: stored}
}

// #612: Forecast no longer trusts a Tenant header; the page's read carries a context the Gateway
// signs for the Site.
func TestForecastReadsCarryAGatewaySignedContext(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	response := httptest.NewRecorder()
	controller.serveForecast(response, httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/forecast/load", nil),
		fixture.session(t), publicIntelligenceRoute{siteID: fixture.siteID, target: "load"})
	if response.Code != http.StatusOK {
		t.Fatalf("forecast read got %d: %s", response.Code, response.Body.String())
	}
	requireOwnerContext(t, fixture, "X-Forecast-Context", "forecast-service", "forecast:read")
}

// #612: Optimization's latest recommendation is a read, a new run a write, each on the Site's context.
func TestOptimizationCallsCarryAGatewaySignedContextForTheirAction(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	response := httptest.NewRecorder()
	controller.serveLatestOptimizationRecommendation(response, httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/optimization/recommendations/latest", nil),
		fixture.session(t), publicIntelligenceRoute{siteID: fixture.siteID})
	if response.Code != http.StatusOK {
		t.Fatalf("recommendation read got %d: %s", response.Code, response.Body.String())
	}
	requireOwnerContext(t, fixture, "X-Optimization-Context", "optimization-service", "optimization:read")

	response = httptest.NewRecorder()
	controller.createOptimizationRun(response, httptest.NewRequest(http.MethodPost, "/api/v1/optimization/runs", strings.NewReader(`{"siteId":"`+fixture.siteID+`"}`)), fixture.session(t))
	if response.Code != http.StatusOK {
		t.Fatalf("run request got %d: %s", response.Code, response.Body.String())
	}
	requireOwnerContext(t, fixture, "X-Optimization-Context", "optimization-service", "optimization:write")
}
