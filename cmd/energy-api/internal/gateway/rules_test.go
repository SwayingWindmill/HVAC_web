package gateway

import (
	"net/http"
	"net/url"
	"testing"
)

func TestRuleRouteMatcherAllowsOnlyGovernedPublicSeams(t *testing.T) {
	cases := []struct {
		method    string
		path      string
		operation string
	}{
		{http.MethodGet, "/api/v1/rules/catalog", "catalog"},
		{http.MethodGet, "/api/v1/rules/revisions", "revisions"},
		{http.MethodPost, "/api/v1/rules/validate", "validate"},
		{http.MethodPost, "/api/v1/rules/simulate", "simulate"},
		{http.MethodPost, "/api/v1/rules/releases", "release"},
		{http.MethodGet, "/api/v1/rules/bindings", "bindings"},
		{http.MethodPost, "/api/v1/rules/assignments", "assign"},
		{http.MethodGet, "/api/v1/rules/executions", "evidence"},
	}
	for _, testCase := range cases {
		t.Run(testCase.operation, func(t *testing.T) {
			route, ok := matchPublicRuleRoute(testCase.method, testCase.path)
			if !ok || route.operation != testCase.operation {
				t.Fatalf("route %s %s = %#v, %t", testCase.method, testCase.path, route, ok)
			}
		})
	}
	if _, ok := matchPublicRuleRoute(http.MethodPost, "/api/v1/rules/catalog"); ok {
		t.Fatal("catalog accepted a mutation method")
	}
	if _, ok := matchPublicRuleRoute(http.MethodPost, "/api/v1/rules/raw-script"); ok {
		t.Fatal("unapproved Rule endpoint was routed")
	}
}

func TestRuleRetireRouteRequiresUUIDv7Binding(t *testing.T) {
	bindingID := "01900000-0102-7000-8000-000000000001"
	route, ok := matchPublicRuleRoute(http.MethodPost, "/api/v1/rules/bindings/"+bindingID+"/retire")
	if !ok || route.operation != "retire" || route.bindingID != bindingID {
		t.Fatalf("retire route = %#v, %t", route, ok)
	}
	for _, path := range []string{
		"/api/v1/rules/bindings/not-a-uuid/retire",
		"/api/v1/rules/bindings/01900000-0102-4000-8000-000000000001/retire",
	} {
		if _, ok := matchPublicRuleRoute(http.MethodPost, path); ok {
			t.Fatalf("invalid binding identity routed: %s", path)
		}
	}
}

func TestRuleQueryKeysRejectUnknownOrDuplicateAuthorityInputs(t *testing.T) {
	if !ruleQueryKeys(url.Values{"siteId": {"site"}, "limit": {"50"}}, "siteId", "limit") {
		t.Fatal("valid bounded evidence query was rejected")
	}
	if ruleQueryKeys(url.Values{"siteId": {"one", "two"}}, "siteId") {
		t.Fatal("duplicate Site authority input was accepted")
	}
	if ruleQueryKeys(url.Values{"siteId": {"site"}, "owner": {"browser"}}, "siteId") {
		t.Fatal("unknown Rule authority query input was accepted")
	}
}
