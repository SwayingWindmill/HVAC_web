package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestGatewayWorkOrderTaskRoutesAreExactAndClosed(t *testing.T) {
	base := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID
	cases := map[string]struct {
		path   string
		kind   publicWorkOrderRouteKind
		action workorderauth.Action
		taskID string
	}{
		"collection": {base + "/tasks", publicWorkOrderTaskCollection, workorderauth.ActionTaskList, ""},
		"status":     {base + "/tasks/" + gatewayWorkOrderTaskOneID + ":status", publicWorkOrderTaskStatus, workorderauth.ActionTaskStatus, gatewayWorkOrderTaskOneID},
		"reorder":    {base + "/tasks:reorder", publicWorkOrderTaskReorder, workorderauth.ActionTaskReorder, ""},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			route, ok := matchPublicWorkOrderRoute(testCase.path)
			if !ok || route.kind != testCase.kind || route.action != testCase.action || route.taskID != testCase.taskID || route.workOrderID != gatewayWorkOrderID {
				t.Fatalf("route=%#v ok=%v", route, ok)
			}
		})
	}
	for _, path := range []string{
		base + "/tasks/" + gatewayWorkOrderTaskOneID + ":delete",
		base + "/tasks/" + gatewayWorkOrderTaskOneID + ":title",
		base + "/tasks/" + gatewayWorkOrderTaskOneID,
		base + "/tasks/" + gatewayWorkOrderTaskOneID + ":status/extra",
		base + "/tasks/",
	} {
		if _, ok := matchPublicWorkOrderRoute(path); ok {
			t.Fatalf("unreviewed task route is public: %s", path)
		}
	}
}

func TestGatewayWorkOrderTaskAppendUsesExactIAMAndSignedWriteContext(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks"
	route, ok := matchPublicWorkOrderRoute(path)
	if !ok {
		t.Fatal("task append route did not match")
	}
	body := `{"expectedWorkOrderVersion":1,"title":"Inspect fan bearings","reason":"append governed task"}`
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, body, "task-gateway-0001")
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.workOrderCalls.Load() != 1 || fixture.lastUpstreamPath.Load() != "/internal/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders/"+gatewayWorkOrderID+"/tasks" || fixture.lastUpstreamMethod.Load() != http.MethodPost || fixture.lastUpstreamIdempotency.Load() != "task-gateway-0001" {
		t.Fatalf("calls iam=%d backend=%d path=%q method=%q key=%q", fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), fixture.lastUpstreamPath.Load(), fixture.lastUpstreamMethod.Load(), fixture.lastUpstreamIdempotency.Load())
	}
	var checklist workordermodel.TaskChecklist
	if json.NewDecoder(recorder.Body).Decode(&checklist) != nil || checklist.WorkOrderVersion != 2 || len(checklist.Tasks) != 1 || checklist.Tasks[0].TaskID != gatewayWorkOrderTaskOneID {
		t.Fatalf("checklist=%#v", checklist)
	}
}

func TestGatewayWorkOrderTaskListStatusAndReorderPreserveAuthoritativeChecklist(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks"
		route, _ := matchPublicWorkOrderRoute(path)
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
		var checklist workordermodel.TaskChecklist
		if recorder.Code != http.StatusOK || json.NewDecoder(recorder.Body).Decode(&checklist) != nil || checklist.Tasks[0].TaskID != gatewayWorkOrderTaskOneID || fixture.lastUpstreamMethod.Load() != http.MethodGet {
			t.Fatalf("status=%d checklist=%#v method=%q body=%s", recorder.Code, checklist, fixture.lastUpstreamMethod.Load(), recorder.Body.String())
		}
	})

	t.Run("status", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks/" + gatewayWorkOrderTaskOneID + ":status"
		route, _ := matchPublicWorkOrderRoute(path)
		request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, `{"expectedWorkOrderVersion":1,"expectedTaskVersion":1,"status":"COMPLETED","reason":"inspection complete"}`, "task-gateway-0002")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
		var checklist workordermodel.TaskChecklist
		if recorder.Code != http.StatusOK || json.NewDecoder(recorder.Body).Decode(&checklist) != nil || checklist.Summary.Completed != 1 || checklist.Tasks[0].Status != workordermodel.TaskStatusCompleted || checklist.Tasks[0].Version != 2 {
			t.Fatalf("status=%d checklist=%#v body=%s", recorder.Code, checklist, recorder.Body.String())
		}
		if fixture.lastUpstreamPath.Load() != "/internal/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders/"+gatewayWorkOrderID+"/tasks/"+gatewayWorkOrderTaskOneID+":status" {
			t.Fatalf("upstream path=%q", fixture.lastUpstreamPath.Load())
		}
	})

	t.Run("reorder", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks:reorder"
		route, _ := matchPublicWorkOrderRoute(path)
		body := `{"expectedWorkOrderVersion":1,"taskIds":["` + gatewayWorkOrderTaskTwoID + `","` + gatewayWorkOrderTaskOneID + `"],"reason":"measure first"}`
		request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, body, "task-gateway-0003")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
		var checklist workordermodel.TaskChecklist
		if recorder.Code != http.StatusOK || json.NewDecoder(recorder.Body).Decode(&checklist) != nil || len(checklist.Tasks) != 2 || checklist.Tasks[0].TaskID != gatewayWorkOrderTaskTwoID || checklist.Tasks[1].TaskID != gatewayWorkOrderTaskOneID || checklist.Tasks[0].Position != 0 || checklist.Tasks[1].Position != 1 {
			t.Fatalf("status=%d checklist=%#v body=%s", recorder.Code, checklist, recorder.Body.String())
		}
	})
}

func TestGatewayWorkOrderTaskFailuresStopBeforeBackend(t *testing.T) {
	t.Run("missing csrf", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks"
		route, _ := matchPublicWorkOrderRoute(path)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expectedWorkOrderVersion":1,"title":"Inspect","reason":"append"}`))
		request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "task-gateway-0004")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
		if recorder.Code != http.StatusForbidden || fixture.iamCalls.Load() != 0 || fixture.workOrderCalls.Load() != 0 {
			t.Fatalf("status=%d iam=%d backend=%d body=%s", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), recorder.Body.String())
		}
	})

	t.Run("duplicate reorder", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks:reorder"
		route, _ := matchPublicWorkOrderRoute(path)
		body := `{"expectedWorkOrderVersion":1,"taskIds":["` + gatewayWorkOrderTaskOneID + `","` + gatewayWorkOrderTaskOneID + `"],"reason":"invalid"}`
		request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, path, body, "task-gateway-0005")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
		if recorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 0 || fixture.workOrderCalls.Load() != 0 {
			t.Fatalf("status=%d iam=%d backend=%d body=%s", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), recorder.Body.String())
		}
	})

	t.Run("cross-site response", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		fixture.crossSite.Store(true)
		path := "/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + "/tasks"
		route, _ := matchPublicWorkOrderRoute(path)
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, route)
		if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), gatewayWorkOrderOtherSiteID) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}
