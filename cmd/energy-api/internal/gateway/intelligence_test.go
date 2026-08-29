package gateway

import (
	"net/http"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestPublicIntelligenceRoutesHaveSingleDomainOwners(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	runID := "01990000-6000-7000-8000-000000000001"
	for _, test := range []struct {
		method string
		path   string
		owner  string
		siteID string
		runID  string
	}{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/forecast/load", ownershipregistry.OwnerForecast, siteID, ""},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/forecast/pv", ownershipregistry.OwnerForecast, siteID, ""},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/fdd/findings", ownershipregistry.OwnerFDD, siteID, ""},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/fdd/evaluate/low-delta-t", ownershipregistry.OwnerFDD, siteID, ""},
		{http.MethodPost, "/api/v1/optimization/runs", ownershipregistry.OwnerOptimization, "", ""},
		{http.MethodGet, "/api/v1/optimization/runs/" + runID, ownershipregistry.OwnerOptimization, "", runID},
	} {
		route, ok := matchPublicIntelligenceRoute(test.method, test.path)
		if !ok || route.owner != test.owner || route.siteID != test.siteID || route.runID != test.runID {
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
