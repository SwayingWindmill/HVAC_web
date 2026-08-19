package gateway

import (
	"net/url"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

const (
	gatewayWorkOrderSiteID = "01910000-0001-7000-8000-000000000001"
	gatewayWorkOrderID     = "01910000-1000-7000-8000-000000000001"
)

func TestGatewayWorkOrderCanonicalRoutes(t *testing.T) {
	tests := []struct {
		path   string
		action workorderauth.Action
	}{
		{"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders", workorderauth.ActionList},
		{"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID, workorderauth.ActionRead},
		{"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":assign", workorderauth.ActionAssign},
		{"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":start", workorderauth.ActionStart},
	}
	for _, test := range tests {
		route, ok := matchPublicWorkOrderRoute(test.path)
		if !ok || route.siteID != gatewayWorkOrderSiteID || route.action != test.action {
			t.Fatalf("route %s did not resolve to Site=%s action=%s", test.path, gatewayWorkOrderSiteID, test.action)
		}
	}
}

func TestGatewayWorkOrderRejectsMalformedRoutes(t *testing.T) {
	for _, path := range []string{
		"/api/v1/sites/not-a-uuid/work-orders",
		"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/",
		"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/not-a-uuid",
		"/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":unknown",
	} {
		if _, ok := matchPublicWorkOrderRoute(path); ok {
			t.Fatalf("malformed Work Order route was accepted: %s", path)
		}
	}
}

func TestGatewayWorkOrderQueryBoundary(t *testing.T) {
	collection, ok := matchPublicWorkOrderRoute("/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders")
	if !ok {
		t.Fatal("collection route did not match")
	}
	if limit, ok := validatePublicWorkOrderQuery(collection, url.Values{"status": {"OPEN"}, "limit": {"25"}}); !ok || limit != 25 {
		t.Fatalf("valid Work Order query rejected: limit=%d ok=%t", limit, ok)
	}
	for _, query := range []url.Values{
		{"unknown": {"value"}},
		{"limit": {"0"}},
		{"limit": {"101"}},
		{"status": {"OPEN", "CLOSED"}},
	} {
		if _, ok := validatePublicWorkOrderQuery(collection, query); ok {
			t.Fatalf("invalid Work Order query was accepted: %v", query)
		}
	}

	detail, ok := matchPublicWorkOrderRoute("/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID)
	if !ok {
		t.Fatal("detail route did not match")
	}
	if _, ok := validatePublicWorkOrderQuery(detail, url.Values{"status": {"OPEN"}}); ok {
		t.Fatal("detail Work Order route accepted collection filters")
	}
}
