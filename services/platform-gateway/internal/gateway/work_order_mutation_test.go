package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestGatewayWorkOrderCreateUsesCSRFExactIAMAndWriteContext(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	body := validGatewayCreateBody()
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", body, "create-gateway-0001")
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 2 || fixture.workOrderCalls.Load() != 1 || fixture.lastUpstreamMethod.Load() != http.MethodPost || fixture.lastUpstreamIdempotency.Load() != "create-gateway-0001" {
		t.Fatalf("calls iam=%d backend=%d method=%q idempotency=%q", fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), fixture.lastUpstreamMethod.Load(), fixture.lastUpstreamIdempotency.Load())
	}
	if fixture.lastUpstreamPath.Load() != "/internal/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders" || fixture.lastUpstreamBody.Load() != body {
		t.Fatalf("upstream path=%q body=%q", fixture.lastUpstreamPath.Load(), fixture.lastUpstreamBody.Load())
	}
	var created workordermodel.WorkOrder
	if json.NewDecoder(recorder.Body).Decode(&created) != nil || created.WorkOrderID != gatewayWorkOrderID || created.Version != 1 || created.Status != workordermodel.StatusOpen {
		t.Fatalf("unexpected create projection: %#v", created)
	}
}

func TestGatewayWorkOrderAssignBindsVersionAndOwnershipTuple(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	body := "{\"expectedVersion\":1,\"assigneeId\":\"principal:operator-b\",\"teamId\":\"team:controls\",\"reason\":\"route to controls\"}"
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders/"+gatewayWorkOrderID+":assign", body, "assign-gateway-0001")
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{kind: publicWorkOrderAssignment, siteID: gatewayWorkOrderSiteID, workOrderID: gatewayWorkOrderID, action: workorderauth.ActionAssign})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var assigned workordermodel.WorkOrder
	if json.NewDecoder(recorder.Body).Decode(&assigned) != nil || assigned.Version != 2 || assigned.AssigneeID == nil || *assigned.AssigneeID != "principal:operator-b" || assigned.TeamID == nil || *assigned.TeamID != "team:controls" || assigned.Timeline[1].Operation != workordermodel.OperationAssign {
		t.Fatalf("unexpected assignment projection: %#v", assigned)
	}
}

func TestGatewayWorkOrderMutationFailuresStopBeforeBackend(t *testing.T) {
	t.Run("missing csrf", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", strings.NewReader(validGatewayCreateBody()))
		request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "create-gateway-0002")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID})
		if recorder.Code != http.StatusForbidden || fixture.iamCalls.Load() != 0 || fixture.workOrderCalls.Load() != 0 {
			t.Fatalf("status=%d iam=%d backend=%d body=%s", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), recorder.Body.String())
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", "{\"status\":\"COMPLETED\"}", "create-gateway-0003")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID})
		if recorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 0 || fixture.workOrderCalls.Load() != 0 {
			t.Fatalf("status=%d iam=%d backend=%d body=%s", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), recorder.Body.String())
		}
	})
	t.Run("iam deny", func(t *testing.T) {
		fixture := newWorkOrderGatewayFixture(t)
		fixture.deny.Store(true)
		request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", validGatewayCreateBody(), "create-gateway-0004")
		recorder := httptest.NewRecorder()
		dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID})
		if recorder.Code != http.StatusForbidden || fixture.iamCalls.Load() != 1 || fixture.workOrderCalls.Load() != 0 {
			t.Fatalf("status=%d iam=%d backend=%d body=%s", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load(), recorder.Body.String())
		}
	})
}

func TestGatewayWorkOrderMutationRejectsProjectionDriftAndUnreviewedRoutes(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	fixture.crossSite.Store(true)
	request := authenticatedWorkOrderMutationRequest(fixture, http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", validGatewayCreateBody(), "create-gateway-0005")
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID})
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), gatewayWorkOrderOtherSiteID) {
		t.Fatalf("status=%d response=%s", recorder.Code, recorder.Body.String())
	}
	if _, ok := matchPublicWorkOrderRoute("/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":link-alarm"); ok {
		t.Fatal("unreviewed collaboration route is publicly matchable")
	}
	route, ok := matchPublicWorkOrderRoute("/api/v1/sites/" + gatewayWorkOrderSiteID + "/work-orders/" + gatewayWorkOrderID + ":assign")
	if !ok || route.kind != publicWorkOrderAssignment || route.action != workorderauth.ActionAssign {
		t.Fatalf("assignment route=%#v ok=%v", route, ok)
	}
}

func TestGatewayRejectsBrowserWorkOrderWriteContext(t *testing.T) {
	handler := NewHandler(Config{Now: time.Now})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", strings.NewReader("{}"))
	request.Header.Set("X-Work-Order-Write-Context", "caller-supplied")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func authenticatedWorkOrderMutationRequest(fixture *workOrderGatewayFixture, method, path, body, idempotencyKey string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	request.Header.Set("Origin", gatewayWorkOrderOrigin)
	request.Header.Set("X-CSRF-Token", gatewayWorkOrderCSRF)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	return request
}

func validGatewayCreateBody() string {
	return "{\"title\":\"Inspect AHU fan vibration\",\"description\":\"Verify the vibration and record the maintenance outcome.\",\"priority\":\"HIGH\",\"sourceReferences\":[{\"domain\":\"ALARM\",\"resourceId\":\"" + gatewayWorkOrderAlarmID + "\",\"relationship\":\"ORIGIN\"}],\"assigneeId\":\"principal:operator\"}"
}
