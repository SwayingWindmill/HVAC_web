package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestGatewayWorkOrderLifecycleRoutesAreClosedAndActionSpecific(t *testing.T) {
	expected := map[string]struct {
		action    workorderauth.Action
		operation workordermodel.Operation
	}{
		":plan":     {workorderauth.ActionPlan, workordermodel.OperationSchedule},
		":start":    {workorderauth.ActionStart, workordermodel.OperationStart},
		":block":    {workorderauth.ActionBlock, workordermodel.OperationBlock},
		":resume":   {workorderauth.ActionResume, workordermodel.OperationResume},
		":complete": {workorderauth.ActionComplete, workordermodel.OperationComplete},
		":cancel":   {workorderauth.ActionCancel, workordermodel.OperationCancel},
		":reopen":   {workorderauth.ActionReopen, workordermodel.OperationReopen},
	}
	for suffix, want := range expected {
		route, ok := matchPublicWorkOrderRoute("/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + suffix)
		if !ok || route.kind != publicWorkOrderLifecycle || route.action != want.action || route.operation != want.operation {
			t.Fatalf("suffix=%s route=%#v ok=%v", suffix, route, ok)
		}
	}
	for _, suffix := range []string{":open", ":draft", ":link-alarm", ":add-note", ":attach"} {
		if _, ok := matchPublicWorkOrderRoute("/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + suffix); ok {
			t.Fatalf("unreviewed suffix %s is matchable", suffix)
		}
	}
}

func TestGatewayWorkOrderLifecycleParserAcceptsBoundedStart(t *testing.T) {
	h := workOrderParserHandler()
	path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":start"
	route, ok := matchPublicWorkOrderRoute(path)
	if !ok {
		t.Fatal("start route did not match")
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expectedVersion":1,"reason":"begin repair"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "start-gateway-0001")
	parsed, failure := h.parsePublicWorkOrderLifecycle(request, route)
	if failure != nil || parsed.expectedVersion != 1 || parsed.reason != "begin repair" || parsed.idempotencyKey != "start-gateway-0001" {
		t.Fatalf("valid lifecycle request rejected: parsed=%#v failure=%#v", parsed, failure)
	}
}

func TestGatewayWorkOrderLifecycleParserRejectsInvalidBodies(t *testing.T) {
	h := workOrderParserHandler()
	testCases := map[string]struct{ suffix, body string }{
		"plan missing explicit due": {":plan", `{"expectedVersion":1,"scheduledStart":"2026-08-02T00:00:00Z","reason":"plan"}`},
		"complete missing evidence": {":complete", `{"expectedVersion":1,"reason":"complete"}`},
		"start carries schedule":    {":start", `{"expectedVersion":1,"scheduledStart":null,"reason":"start"}`},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + testCase.suffix
			route, ok := matchPublicWorkOrderRoute(path)
			if !ok {
				t.Fatalf("lifecycle route did not match: %s", path)
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "invalid-life-0001")
			if _, failure := h.parsePublicWorkOrderLifecycle(request, route); failure == nil {
				t.Fatalf("invalid lifecycle body was accepted: %s", testCase.body)
			}
		})
	}
}
