package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
	"github.com/quanlaihe/hvac-web/modules/workorder/pkg/workorderservice"
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

func TestDirectWorkOrderAdapterOperations(t *testing.T) {
	tenantID := "01910000-0000-7000-8000-000000000001"
	siteID := gatewayWorkOrderSiteID
	ctx := context.WithValue(context.Background(), routeSessionContextKey, bffSession{Session: sessionstore.Session{TenantID: tenantID}})

	store, err := workorderservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	adapter := newDirectWorkOrderAdapter(store)

	// 1. Test Create Mutation
	createReq := &publicWorkOrderCreateRequest{
		Title:            "Repair AHU Motor",
		Description:      "AHU motor bearing has high vibration",
		Priority:         workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: "01910000-0003-7000-8000-000000000001", Relationship: workordermodel.RelationshipOrigin}},
	}
	createJSON, _ := json.Marshal(createReq)
	reqCreate, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/sites/"+siteID+"/work-orders", bytes.NewReader(createJSON))
	reqCreate.Header.Set("Content-Type", "application/json")
	createParsed := parsedPublicWorkOrderMutation{
		body:           createJSON,
		idempotencyKey: "idemp-wo-create-001",
		create:         createReq,
	}
	createRoute := publicWorkOrderRoute{kind: publicWorkOrderCollection, template: "/api/v1/sites/{siteId}/work-orders", siteID: siteID, action: workorderauth.ActionCreate}
	body, status, replayed, failure := adapter.ExecuteMutation(ctx, reqCreate, createRoute, createParsed, "")
	if failure != nil || status != http.StatusCreated || replayed {
		t.Fatalf("Create failed: status=%d replayed=%v failure=%#v", status, replayed, failure)
	}
	var createdWO workordermodel.WorkOrder
	if err := json.Unmarshal(body, &createdWO); err != nil || createdWO.Title != createReq.Title {
		t.Fatalf("unexpected created WO: %#v", createdWO)
	}

	// 2. Test Read Detail
	reqGet, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/sites/"+siteID+"/work-orders/"+createdWO.WorkOrderID, nil)
	getRoute := publicWorkOrderRoute{kind: publicWorkOrderDetail, template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}", siteID: siteID, workOrderID: createdWO.WorkOrderID, action: workorderauth.ActionRead}
	getBody, getStatus, getFailure := adapter.ExecuteRead(ctx, reqGet, getRoute, "")
	if getFailure != nil || getStatus != http.StatusOK {
		t.Fatalf("Get failed: status=%d failure=%#v", getStatus, getFailure)
	}
	var fetchedWO workordermodel.WorkOrder
	if err := json.Unmarshal(getBody, &fetchedWO); err != nil || fetchedWO.WorkOrderID != createdWO.WorkOrderID {
		t.Fatalf("unexpected fetched WO: %#v", fetchedWO)
	}

	// 3. Test Read List
	reqList, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/sites/"+siteID+"/work-orders", nil)
	listRoute := publicWorkOrderRoute{kind: publicWorkOrderCollection, template: "/api/v1/sites/{siteId}/work-orders", siteID: siteID, action: workorderauth.ActionList}
	listBody, listStatus, listFailure := adapter.ExecuteRead(ctx, reqList, listRoute, "")
	if listFailure != nil || listStatus != http.StatusOK {
		t.Fatalf("List failed: status=%d failure=%#v", listStatus, listFailure)
	}
	var listResp workordermodel.ListResponse
	if err := json.Unmarshal(listBody, &listResp); err != nil || len(listResp.Items) != 1 {
		t.Fatalf("unexpected list response: %#v", listResp)
	}

	// 4. Test Assign Mutation
	assignee := "tech-001"
	assignReq := &publicWorkOrderAssignmentRequest{
		ExpectedVersion: 1,
		AssigneeID:      json.RawMessage(`"tech-001"`),
		TeamID:          json.RawMessage(`null`),
		Reason:          "Assigned to lead technician",
	}
	assignJSON, _ := json.Marshal(assignReq)
	reqAssign, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/sites/"+siteID+"/work-orders/"+createdWO.WorkOrderID+":assign", bytes.NewReader(assignJSON))
	reqAssign.Header.Set("Content-Type", "application/json")
	assignParsed := parsedPublicWorkOrderMutation{
		body:             assignJSON,
		idempotencyKey:   "idemp-wo-assign-001",
		assignment:       assignReq,
		assignmentTarget: &assignee,
	}
	assignRoute := publicWorkOrderRoute{kind: publicWorkOrderAssignment, template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}:assign", siteID: siteID, workOrderID: createdWO.WorkOrderID, action: workorderauth.ActionAssign}
	assignBody, assignStatus, assignReplayed, assignFailure := adapter.ExecuteMutation(ctx, reqAssign, assignRoute, assignParsed, "")
	if assignFailure != nil || assignStatus != http.StatusOK || assignReplayed {
		t.Fatalf("Assign failed: status=%d replayed=%v failure=%#v", assignStatus, assignReplayed, assignFailure)
	}
	var assignedWO workordermodel.WorkOrder
	if err := json.Unmarshal(assignBody, &assignedWO); err != nil || assignedWO.AssigneeID == nil || *assignedWO.AssigneeID != assignee {
		t.Fatalf("unexpected assigned WO: %#v", assignedWO)
	}

	// 5. Test Lifecycle Precondition
	precondWO, precondFailure := adapter.ExecuteLifecyclePrecondition(ctx, reqAssign, assignRoute, "idemp-wo-plan-001", "")
	if precondFailure != nil || precondWO.WorkOrderID != createdWO.WorkOrderID {
		t.Fatalf("Precondition failed: %#v", precondFailure)
	}

	// 6. Test Lifecycle Transition (:start)
	startRoute := publicWorkOrderRoute{
		kind:        publicWorkOrderLifecycle,
		template:    "/api/v1/sites/{siteId}/work-orders/{workOrderId}:start",
		siteID:      siteID,
		workOrderID: createdWO.WorkOrderID,
		action:      workorderauth.ActionStart,
		operation:   workordermodel.OperationStart,
	}
	startMutation := parsedPublicWorkOrderLifecycle{
		idempotencyKey:  "idemp-wo-start-001",
		expectedVersion: 2,
		reason:          "Starting maintenance work",
	}
	reqStart, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/sites/"+siteID+"/work-orders/"+createdWO.WorkOrderID+":start", nil)
	startBody, startStatus, startReplayed, startFailure := adapter.ExecuteLifecycle(ctx, reqStart, startRoute, startMutation, "")
	if startFailure != nil || startStatus != http.StatusOK || startReplayed {
		t.Fatalf("Start failed: status=%d replayed=%v failure=%#v", startStatus, startReplayed, startFailure)
	}
	var startedWO workordermodel.WorkOrder
	if err := json.Unmarshal(startBody, &startedWO); err != nil || startedWO.Status != workordermodel.StatusInProgress {
		t.Fatalf("unexpected started WO: %#v", startedWO)
	}
}
