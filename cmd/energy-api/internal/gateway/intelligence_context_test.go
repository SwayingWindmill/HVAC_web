package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
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

// #612: Optimization's latest recommendation, and how the schedule's latest firing went, are reads, a new run a write, each on the Site's context.
func TestOptimizationCallsCarryAGatewaySignedContextForTheirAction(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	for _, resource := range []string{"recommendations", "preparations"} {
		fixture.lastOwner.Store(nil)
		response := httptest.NewRecorder()
		controller.serveLatestOptimizationRead(response, httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/optimization/"+resource+"/latest", nil),
			fixture.session(t), publicIntelligenceRoute{siteID: fixture.siteID}, resource)
		if response.Code != http.StatusOK {
			t.Fatalf("%s read got %d: %s", resource, response.Code, response.Body.String())
		}
		requireOwnerContext(t, fixture, "X-Optimization-Context", "optimization-service", "optimization:read")
		if path := fixture.lastOwner.Load().URL.Path; path != "/v1/sites/"+fixture.siteID+"/optimization/"+resource+"/latest" {
			t.Fatalf("%s read went to %s", resource, path)
		}
	}

	response := httptest.NewRecorder()
	controller.createOptimizationRun(response, httptest.NewRequest(http.MethodPost, "/api/v1/optimization/runs", strings.NewReader(`{"siteId":"`+fixture.siteID+`"}`)), fixture.session(t))
	if response.Code != http.StatusOK {
		t.Fatalf("run request got %d: %s", response.Code, response.Body.String())
	}
	requireOwnerContext(t, fixture, "X-Optimization-Context", "optimization-service", "optimization:write")
}

// #612: M&V no longer trusts X-Tenant-ID or X-Actor-ID; reading the Site's measures is a read and
// registering one a write, each on the Site's context, which names the person.
func TestSavingsCallsCarryAGatewaySignedContextForTheirAction(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	session := fixture.session(t)
	session.CSRFToken = "operations-csrf-fixture"
	routed := func(method, path, body string) *http.Request {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Origin", "https://web.example.test")
		request.Header.Set("X-CSRF-Token", "operations-csrf-fixture")
		ctx := context.WithValue(request.Context(), routeDecisionContextKey, ownershipregistry.Decision{SelectedOwner: ownershipregistry.OwnerMV})
		return request.WithContext(context.WithValue(ctx, routeSessionContextKey, session))
	}
	base := "/api/v1/sites/" + fixture.siteID + "/savings/measures"

	response := httptest.NewRecorder()
	dispatchSavingsRoute(controller, response, routed(http.MethodGet, base, ""), publicSavingsRoute{siteID: fixture.siteID, internalPath: "/measures"})
	if response.Code != http.StatusOK {
		t.Fatalf("measures read got %d: %s", response.Code, response.Body.String())
	}
	requireOwnerContext(t, fixture, "X-MV-Context", "mv-service", "mv:read")

	response = httptest.NewRecorder()
	dispatchSavingsRoute(controller, response, routed(http.MethodPost, base, `{"name":"提高冷冻水供水温度","kind":"SETPOINT_RESET","plannedAt":"2026-10-15T09:00:00+08:00"}`),
		publicSavingsRoute{siteID: fixture.siteID, internalPath: "/measures", write: true})
	if response.Code != http.StatusOK {
		t.Fatalf("measure registration got %d: %s", response.Code, response.Body.String())
	}
	if claims := requireOwnerContext(t, fixture, "X-MV-Context", "mv-service", "mv:write"); claims.Subject != "operations-user" {
		t.Fatalf("the registration context does not name the person: %+v", claims)
	}
}

// #613: report-service no longer trusts a Tenant header; the month and year reads carry a context
// the Gateway signs to read the Site.
func TestReportReadsCarryAGatewaySignedContext(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	session := fixture.session(t)
	for _, route := range []publicReportRoute{
		{siteID: fixture.siteID, internalPath: "/monthly/2026-09"},
		{siteID: fixture.siteID, internalPath: "/monthly"},
	} {
		fixture.lastOwner.Store(nil)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/reports"+route.internalPath, nil)
		ctx := context.WithValue(request.Context(), routeDecisionContextKey, ownershipregistry.Decision{SelectedOwner: ownershipregistry.OwnerReport})
		response := httptest.NewRecorder()
		dispatchReportRoute(controller, response, request.WithContext(context.WithValue(ctx, routeSessionContextKey, session)), route)
		if response.Code != http.StatusOK {
			t.Fatalf("report read %s got %d: %s", route.internalPath, response.Code, response.Body.String())
		}
		requireOwnerContext(t, fixture, "X-Report-Context", "report-service", "report:read")
		if path := fixture.lastOwner.Load().URL.Path; path != "/v1/sites/"+fixture.siteID+"/reports"+route.internalPath {
			t.Fatalf("report read went to %s", path)
		}
	}
}
