package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestPublicIntelligenceRoutesHaveSingleDomainOwners(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	for _, test := range []struct {
		method string
		path   string
		owner  string
		siteID string
	}{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/forecast/load", ownershipregistry.OwnerForecast, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/forecast/pv", ownershipregistry.OwnerForecast, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/fdd/findings", ownershipregistry.OwnerFDD, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/fdd/rules/low-delta-t", ownershipregistry.OwnerFDD, siteID},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/fdd/evaluate/low-delta-t", ownershipregistry.OwnerFDD, siteID},
		{http.MethodPost, "/api/v1/optimization/runs", ownershipregistry.OwnerOptimization, ""},
	} {
		route, ok := matchPublicIntelligenceRoute(test.method, test.path)
		if !ok || route.owner != test.owner || route.siteID != test.siteID {
			t.Fatalf("route %s %s resolved as %#v ok=%t", test.method, test.path, route, ok)
		}
	}
}

func TestPublicFDDLinkRouteIsPatchOnly(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	findingID := "01990000-7000-7000-8000-000000000001"
	path := "/api/v1/sites/" + siteID + "/fdd/findings/" + findingID + "/links"
	route, ok := matchPublicIntelligenceRoute(http.MethodPatch, path)
	if !ok || route.owner != ownershipregistry.OwnerFDD || route.siteID != siteID || route.findingID != findingID || route.target != "fdd-link" {
		t.Fatalf("FDD link route resolved as %#v ok=%t", route, ok)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		if _, ok := matchPublicIntelligenceRoute(method, path); ok {
			t.Fatalf("%s unexpectedly matched FDD link route", method)
		}
	}
}

func TestWorkOrderOriginatesFromExactAlarm(t *testing.T) {
	alarmID := "01990000-7100-7000-8000-000000000001"
	workOrder := workordermodel.WorkOrder{SourceReferences: []workordermodel.SourceReference{{
		Domain: workordermodel.SourceAlarm, ResourceID: alarmID, Relationship: workordermodel.RelationshipOrigin,
	}}}
	if !workOrderOriginatesFromAlarm(workOrder, alarmID) {
		t.Fatal("matching ALARM origin was not recognized")
	}
	for _, reference := range []workordermodel.SourceReference{
		{Domain: workordermodel.SourceAlarm, ResourceID: "01990000-7100-7000-8000-000000000002", Relationship: workordermodel.RelationshipOrigin},
		{Domain: workordermodel.SourceAlarm, ResourceID: alarmID, Relationship: workordermodel.RelationshipRelated},
		{Domain: workordermodel.SourceAsset, ResourceID: alarmID, Relationship: workordermodel.RelationshipOrigin},
	} {
		workOrder.SourceReferences = []workordermodel.SourceReference{reference}
		if workOrderOriginatesFromAlarm(workOrder, alarmID) {
			t.Fatalf("non-matching Work Order provenance was accepted: %#v", reference)
		}
	}
}

func TestIntelligenceRoutesRejectWrongMethods(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	for _, path := range []string{
		"/api/v1/sites/" + siteID + "/forecast/load",
		"/api/v1/sites/" + siteID + "/forecast/pv",
		"/api/v1/sites/" + siteID + "/fdd/findings",
	} {
		if _, ok := matchPublicIntelligenceRoute(http.MethodPost, path); ok {
			t.Fatalf("POST unexpectedly matched read-only Intelligence route %s", path)
		}
	}
	if _, ok := matchPublicIntelligenceRoute(http.MethodGet, "/api/v1/sites/"+siteID+"/fdd/evaluate/low-delta-t"); ok {
		t.Fatal("GET unexpectedly matched FDD evaluation route")
	}
	if _, ok := matchPublicIntelligenceRoute(http.MethodGet, publicOptimizationRunsPath); ok {
		t.Fatal("GET unexpectedly matched Optimization create route")
	}
}

// A client tells "this Site has no result yet" from "this deployment does not serve the route" by
// the code alone, so an owner router's plain-text 404 must not read as a missing result (#A5).
func TestIntelligenceNotFoundSeparatesMissingRouteFromMissingResult(t *testing.T) {
	h := &handler{intelligence: &intelligenceController{}}
	for _, testCase := range []struct {
		name string
		raw  []byte
		code string
	}{
		{"owner router 404", []byte("404 page not found\n"), "ROUTE_NOT_FOUND"},
		{"empty 404", nil, "ROUTE_NOT_FOUND"},
		{"owner JSON not-found", []byte(`{"error":"optimization_not_found"}`), "RESOURCE_NOT_FOUND"},
		{"owner problem not-found", []byte(`{"status":404,"code":"REPORT_SNAPSHOT_NOT_FOUND"}`), "RESOURCE_NOT_FOUND"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			h.writeIntelligenceResult(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/sites/x/forecast/load", nil), testCase.raw, http.StatusNotFound, nil)
			assertProblemCode(t, recorder, http.StatusNotFound, testCase.code)
		})
	}
}

func TestIntelligenceReadWithoutConfiguredOwnerIsAMissingRoute(t *testing.T) {
	h := &handler{intelligence: &intelligenceController{}}
	siteID := "01990000-5000-7000-8000-000000000001"
	for _, testCase := range []struct {
		name  string
		serve func(http.ResponseWriter, *http.Request, bffSession, publicIntelligenceRoute)
		route publicIntelligenceRoute
	}{
		{"forecast", h.serveForecast, publicIntelligenceRoute{siteID: siteID, target: "load"}},
		{"fdd findings", h.serveFDDRead, publicIntelligenceRoute{siteID: siteID, target: "fdd-findings"}},
		{"fdd link", h.serveFDDLink, publicIntelligenceRoute{siteID: siteID, findingID: siteID}},
		{"optimization latest", h.serveLatestOptimizationRecommendation, publicIntelligenceRoute{siteID: siteID}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			testCase.serve(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+siteID, nil), bffSession{}, testCase.route)
			assertProblemCode(t, recorder, http.StatusNotFound, "ROUTE_NOT_FOUND")
		})
	}
}

func assertProblemCode(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil || problem.Code != code {
		t.Fatalf("problem code = %q (%v), want %q: %s", problem.Code, err, code, recorder.Body.String())
	}
}
