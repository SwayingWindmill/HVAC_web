package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestGatewayWorkOrderStartUsesExactLifecycleActionAndKeyBoundWriteContext(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	body := "{\"expectedVersion\":1,\"reason\":\"begin repair\"}"
	path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":start"
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, body, "start-gateway-0001")
	recorder := httptest.NewRecorder()
	route, ok := matchPublicWorkOrderRoute(path)
	if !ok || route.kind != publicWorkOrderLifecycle || route.action != workorderauth.ActionStart || route.operation != workordermodel.OperationStart {
		t.Fatalf("route=%#v ok=%v", route, ok)
	}
	dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.workOrderCalls.Load() != 2 || fixture.lastUpstreamPath.Load() != "/internal/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders/"+gatewayWorkOrderID+":start" || fixture.lastUpstreamIdempotency.Load() != "start-gateway-0001" {
		t.Fatalf("iam=%d backend=%d path=%q key=%q", fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), fixture.lastUpstreamPath.Load(), fixture.lastUpstreamIdempotency.Load())
	}
	var started workordermodel.WorkOrder
	if json.NewDecoder(recorder.Body).Decode(&started) != nil || started.Status != workordermodel.StatusInProgress || started.Version != 2 || started.Timeline[1].Operation != workordermodel.OperationStart {
		t.Fatalf("projection=%#v", started)
	}
}

func TestGatewayWorkOrderLifecycleRejectsImmutableDownstreamDrift(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	fixture.lifecycleDrift.Store(true)
	body := "{\"expectedVersion\":1,\"reason\":\"begin repair\"}"
	path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":start"
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, body, "start-gateway-drift1")
	recorder := httptest.NewRecorder()
	route, _ := matchPublicWorkOrderRoute(path)
	dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "WORK_ORDER_UNAVAILABLE") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.workOrderCalls.Load() != 2 {
		t.Fatalf("iam=%d backend=%d", fixture.iamCalls.Load(), fixture.workOrderCalls.Load())
	}
}

func TestGatewayWorkOrderLifecycleRejectsDownstreamAuditEvidenceDrift(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	fixture.lifecycleAuditDrift.Store(true)
	body := "{\"expectedVersion\":1,\"reason\":\"begin repair\"}"
	path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":start"
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, body, "start-gateway-audit1")
	recorder := httptest.NewRecorder()
	route, _ := matchPublicWorkOrderRoute(path)
	dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "WORK_ORDER_UNAVAILABLE") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

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

func TestGatewayWorkOrderLifecycleInvalidBodiesStopBeforeIAM(t *testing.T) {
	testCases := map[string]struct{ suffix, body string }{
		"plan missing explicit due": {":plan", "{\"expectedVersion\":1,\"scheduledStart\":\"2026-08-02T00:00:00Z\",\"reason\":\"plan\"}"},
		"complete missing evidence": {":complete", "{\"expectedVersion\":1,\"reason\":\"complete\"}"},
		"start carries schedule":    {":start", "{\"expectedVersion\":1,\"scheduledStart\":null,\"reason\":\"start\"}"},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			fixture := newWorkOrderGatewayFixture(t)
			path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + testCase.suffix
			request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, testCase.body, "invalid-life-0001")
			recorder := httptest.NewRecorder()
			route, _ := matchPublicWorkOrderRoute(path)
			dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
			if recorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 0 || fixture.workOrderCalls.Load() != 0 || !strings.Contains(recorder.Body.String(), "WORK_ORDER_REQUEST_INVALID") {
				t.Fatalf("status=%d iam=%d backend=%d body=%s", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), recorder.Body.String())
			}
		})
	}
}
