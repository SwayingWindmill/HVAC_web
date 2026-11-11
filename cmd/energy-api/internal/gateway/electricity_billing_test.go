package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestElectricityBillingRoutesMatchPublicContract(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	billID := "01990000-9000-7000-8000-000000000001"

	valid := []struct {
		method, path, kind, internalPath string
		write                            bool
	}{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/electricity-budgets", "budgets-list", "/internal/v1/registry/sites/" + siteID + "/electricity-budgets", false},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/electricity-budgets", "budgets-put", "/internal/v1/registry/sites/" + siteID + "/electricity-budgets", true},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/utility-bills", "bills-list", "/internal/v1/registry/sites/" + siteID + "/utility-bills", false},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/utility-bills", "bills-create", "/internal/v1/registry/sites/" + siteID + "/utility-bills", true},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/utility-bills/" + billID, "bills-update", "/internal/v1/registry/sites/" + siteID + "/utility-bills/" + billID, true},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/utility-bills/" + billID + "/confirm", "bills-confirm", "/internal/v1/registry/sites/" + siteID + "/utility-bills/" + billID + "/confirm", true},
	}
	for _, tc := range valid {
		route, ok := matchPublicElectricityBillingRoute(tc.method, tc.path)
		if !ok || route.siteID != siteID || route.kind != tc.kind || route.internalPath != tc.internalPath || route.write != tc.write {
			t.Fatalf("%s %s resolved as %#v ok=%t", tc.method, tc.path, route, ok)
		}
	}

	invalid := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/sites/" + siteID + "/electricity-budgets"},
		{http.MethodDelete, "/api/v1/sites/" + siteID + "/electricity-budgets"},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/utility-bills"},
		{http.MethodDelete, "/api/v1/sites/" + siteID + "/utility-bills/" + billID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/utility-bills/" + billID + "/confirm"},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/utility-bills/"},
	}
	for _, tc := range invalid {
		if _, ok := matchPublicElectricityBillingRoute(tc.method, tc.path); ok {
			t.Fatalf("%s %s unexpectedly matched an electricity billing route", tc.method, tc.path)
		}
	}
}

func TestElectricityBillingGatewayBlocksForgedHeadersAndPassthroughsConflicts(t *testing.T) {
	h := NewHandler(Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/01990000-5000-7000-8000-000000000001/utility-bills", strings.NewReader(`{}`))
	req.Header.Set("X-Actor-Display-Name", "伪造财务")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged X-Actor-Display-Name status=%d body=%s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, "REGISTRY_MUTATION_INVALID"},
		{http.StatusBadRequest, "REGISTRY_QUERY_INVALID"},
		{http.StatusForbidden, "BILLING_MANAGE_FORBIDDEN"},
		{http.StatusConflict, "REGISTRY_REVISION_CONFLICT"},
		{http.StatusConflict, "REGISTRY_BINDING_CONFLICT"},
		{http.StatusConflict, "UTILITY_BILL_STATE_CONFLICT"},
	} {
		resp := &http.Response{
			StatusCode: tc.status,
			Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":"` + tc.code + `","detail":"refused"}`)),
		}
		out := httptest.NewRecorder()
		writeElectricityBillingBackendResponse(out, httptest.NewRequest(http.MethodPost, "/api/v1/sites/x/utility-bills", nil), resp, 2<<20)
		var problem struct {
			Code   string `json:"code"`
			Title  string `json:"title"`
			Status int    `json:"status"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &problem); err != nil || out.Code != tc.status || problem.Code != tc.code {
			t.Fatalf("passthrough %d %s -> got %d %s", tc.status, tc.code, out.Code, out.Body.String())
		}
		if tc.status == http.StatusForbidden && problem.Title != "Electricity billing action forbidden" {
			t.Fatalf("expected Electricity billing action forbidden title, got %q", problem.Title)
		}
	}
}
