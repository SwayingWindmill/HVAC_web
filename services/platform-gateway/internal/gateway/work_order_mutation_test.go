package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

const (
	gatewayWorkOrderTestTenantID = "0190f000-0000-7000-8000-000000000001"
	gatewayWorkOrderTestAlarmID  = "01910000-2000-7000-8000-000000000001"
)

func TestGatewayWorkOrderCreateParserUsesTenantScope(t *testing.T) {
	h := workOrderParserHandler()
	session := bffSession{Session: sessionstore.Session{TenantID: gatewayWorkOrderTestTenantID}}
	body := validGatewayCreateBody()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-gateway-0001")

	parsed, failure := h.parseWorkOrderMutation(request, session, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID, action: workorderauth.ActionCreate})
	if failure != nil || parsed.create == nil || parsed.expectedCreate == nil {
		t.Fatalf("valid Work Order create rejected: parsed=%#v failure=%#v", parsed, failure)
	}
	if parsed.expectedCreate.TenantID != gatewayWorkOrderTestTenantID || parsed.expectedCreate.SiteID != gatewayWorkOrderSiteID {
		t.Fatalf("create scope drifted: Tenant=%s Site=%s", parsed.expectedCreate.TenantID, parsed.expectedCreate.SiteID)
	}
}

func TestGatewayWorkOrderAssignmentParserBindsOwnershipTuple(t *testing.T) {
	h := workOrderParserHandler()
	session := bffSession{Session: sessionstore.Session{TenantID: gatewayWorkOrderTestTenantID}}
	body := `{"expectedVersion":1,"assigneeId":"principal:operator-b","teamId":"team:controls","reason":"route to controls"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders/"+gatewayWorkOrderID+":assign", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "assign-gateway-0001")

	parsed, failure := h.parseWorkOrderMutation(request, session, publicWorkOrderRoute{kind: publicWorkOrderAssignment, siteID: gatewayWorkOrderSiteID, workOrderID: gatewayWorkOrderID, action: workorderauth.ActionAssign})
	if failure != nil || parsed.assignment == nil || parsed.assignmentTarget == nil || parsed.assignmentTeam == nil {
		t.Fatalf("valid Work Order assignment rejected: parsed=%#v failure=%#v", parsed, failure)
	}
	if *parsed.assignmentTarget != "principal:operator-b" || *parsed.assignmentTeam != "team:controls" || parsed.assignment.ExpectedVersion != 1 {
		t.Fatalf("assignment tuple drifted: %#v", parsed)
	}
}

func TestGatewayWorkOrderMutationParserRejectsInvalidRequests(t *testing.T) {
	h := workOrderParserHandler()
	session := bffSession{Session: sessionstore.Session{TenantID: gatewayWorkOrderTestTenantID}}
	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "missing idempotency", body: validGatewayCreateBody()},
		{name: "closed object", body: `{"status":"COMPLETED"}`, key: "create-gateway-0003"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}
			if _, failure := h.parseWorkOrderMutation(request, session, publicWorkOrderRoute{kind: publicWorkOrderCollection, siteID: gatewayWorkOrderSiteID}); failure == nil {
				t.Fatalf("invalid Work Order mutation was accepted: %s", test.name)
			}
		})
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

func workOrderParserHandler() *handler {
	return &handler{identity: &identityController{now: func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }}}
}

func validGatewayCreateBody() string {
	return `{"title":"Inspect AHU fan vibration","description":"Verify the vibration and record the maintenance outcome.","priority":"HIGH","sourceReferences":[{"domain":"ALARM","resourceId":"` + gatewayWorkOrderTestAlarmID + `","relationship":"ORIGIN"}],"assigneeId":"principal:operator"}`
}
