package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #597: the assistant's usage is for whoever may read the Tenant's records (audit.read). Without
// it, or for a Site the operator cannot read, the Gateway refuses before asking the Agent; the
// Agent's figures pass through only in their public shape.
func TestAgentUsageNeedsAuditReadAndTheSite(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 10)
	get := func(query string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/operations/agent-usage"+query, nil)
		fixture.authenticate(request, false)
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		return recorder
	}
	const shanghai = "?timezone=Asia%2FShanghai"

	if response := get(shanghai); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "OPERATIONS_USAGE_FORBIDDEN") {
		t.Fatalf("usage without audit.read: %d %s", response.Code, response.Body.String())
	}
	fixture.auditRead.Store(true)
	fixture.denySite.Store(true)
	if response := get(shanghai); response.Code != http.StatusNotFound {
		t.Fatalf("usage of a Site the operator cannot read: %d %s", response.Code, response.Body.String())
	}
	if calls := fixture.operationsCalls.Load(); calls != 0 {
		t.Fatalf("the Agent was asked %d times before the refusals", calls)
	}
	fixture.denySite.Store(false)
	for _, query := range []string{"", "?timezone=Asia%2FShanghai&days=30"} {
		if response := get(query); response.Code != http.StatusBadRequest {
			t.Fatalf("usage with query %q: %d", query, response.Code)
		}
	}

	response := get(shanghai)
	if response.Code != http.StatusOK {
		t.Fatalf("usage: %d %s", response.Code, response.Body.String())
	}
	upstream := fixture.lastUpstream.Load()
	if upstream.URL.Path != "/internal/v1/sites/"+fixture.siteID+"/operations/agent-usage" || upstream.URL.Query().Get("timezone") != "Asia/Shanghai" {
		t.Fatalf("the Agent was asked %s", upstream.URL.String())
	}
	var usage map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &usage); err != nil || len(usage["days"].([]any)) != 14 {
		t.Fatalf("usage body: %s", response.Body.String())
	}
	// #624: why the failed reads failed reaches the page, as codes the page puts into words.
	if !strings.Contains(response.Body.String(), `"failureReasons":[{"code":"TOOL_OWNER_UNAVAILABLE","count":1}]`) {
		t.Fatalf("usage reasons: %s", response.Body.String())
	}
	fixture.usageTooManyFailures.Store(true)
	if response := get(shanghai); response.Code != http.StatusBadGateway {
		t.Fatalf("usage whose reasons count more failures than the step had: %d %s", response.Code, response.Body.String())
	}
	fixture.usageTooManyFailures.Store(false)

	// A field the public shape does not have, such as a question, never reaches the browser.
	fixture.usageExtraField.Store(true)
	if response := get(shanghai); response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "10月17日") {
		t.Fatalf("usage with an extra field: %d %s", response.Code, response.Body.String())
	}
}
