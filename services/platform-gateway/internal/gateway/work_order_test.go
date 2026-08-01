package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	gatewayWorkOrderOrganizationID = "01910000-0000-7000-8000-000000000001"
	gatewayWorkOrderSiteID         = "01910000-0001-7000-8000-000000000001"
	gatewayWorkOrderOtherSiteID    = "01910000-0002-7000-8000-000000000001"
	gatewayWorkOrderID             = "01910000-1000-7000-8000-000000000001"
	gatewayWorkOrderAlarmID        = "01910000-2000-7000-8000-000000000001"
)

func TestGatewayWorkOrderListUsesIAMAndExactSignedReadContext(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders?status=OPEN&limit=25", nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{
		template: "/api/v1/sites/{siteId}/work-orders", siteID: gatewayWorkOrderSiteID, action: workorderauth.ActionList,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 1 || fixture.workOrderCalls.Load() != 1 {
		t.Fatalf("calls iam=%d workOrder=%d", fixture.iamCalls.Load(), fixture.workOrderCalls.Load())
	}
	if fixture.lastUpstreamPath.Load() != "/internal/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders" {
		t.Fatalf("upstream path=%q", fixture.lastUpstreamPath.Load())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache control=%q", recorder.Header().Get("Cache-Control"))
	}
	var response workordermodel.ListResponse
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || len(response.Items) != 1 || response.Items[0].WorkOrderID != gatewayWorkOrderID {
		t.Fatalf("unexpected Work Order list: %#v", response)
	}
}

func TestGatewayWorkOrderDetailRejectsCrossSiteProjection(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	fixture.crossSite.Store(true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders/"+gatewayWorkOrderID, nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{
		template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}", siteID: gatewayWorkOrderSiteID, workOrderID: gatewayWorkOrderID, action: workorderauth.ActionRead,
	})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), gatewayWorkOrderOtherSiteID) {
		t.Fatal("cross-Site Work Order projection leaked its scope")
	}
}

func TestGatewayWorkOrderDenialAndInvalidFilterStopBeforeService(t *testing.T) {
	fixture := newWorkOrderGatewayFixture(t)
	fixture.deny.Store(true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder := httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{siteID: gatewayWorkOrderSiteID, action: workorderauth.ActionList})
	if recorder.Code != http.StatusForbidden || fixture.workOrderCalls.Load() != 0 {
		t.Fatalf("denial status=%d workOrderCalls=%d body=%s", recorder.Code, fixture.workOrderCalls.Load(), recorder.Body.String())
	}

	fixture.deny.Store(false)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders?deviceId=caller-supplied", nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder = httptest.NewRecorder()
	dispatchWorkOrderRoute(fixture.handler, recorder, request, publicWorkOrderRoute{siteID: gatewayWorkOrderSiteID, action: workorderauth.ActionList})
	if recorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 1 || fixture.workOrderCalls.Load() != 0 {
		t.Fatalf("invalid filter reached upstream: status=%d iam=%d workOrder=%d", recorder.Code, fixture.iamCalls.Load(), fixture.workOrderCalls.Load())
	}
	if !strings.Contains(recorder.Body.String(), "WORK_ORDER_REQUEST_INVALID") {
		t.Fatalf("invalid filter problem code=%s", recorder.Body.String())
	}
}

func TestGatewayRejectsBrowserWorkOrderAuthorityHeaders(t *testing.T) {
	handler := NewHandler(Config{Now: time.Now})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayWorkOrderSiteID+"/work-orders", nil)
	request.Header.Set("X-Work-Order-Read-Context", "caller-supplied")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type workOrderGatewayFixture struct {
	handler          *handler
	session          bffSession
	iamCalls         atomic.Int64
	workOrderCalls   atomic.Int64
	deny             atomic.Bool
	crossSite        atomic.Bool
	lastUpstreamPath atomic.Value
}

func newWorkOrderGatewayFixture(t *testing.T) *workOrderGatewayFixture {
	t.Helper()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &workOrderGatewayFixture{}
	fixture.lastUpstreamPath.Store("")
	fixture.session = bffSession{Session: sessionstore.Session{
		ID:                   "session-work-order-1",
		Principal:            identitycontext.UserPrincipal{Subject: "subject-work-order", Issuer: "https://issuer.example", DisplayName: "Work Order Operator", Email: "work-order@example.test", Roles: []string{"operator"}},
		ActingOrganizationID: gatewayWorkOrderOrganizationID,
		ExpiresAt:            now.Add(15 * time.Minute),
	}}
	iamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.iamCalls.Add(1)
		claims, verifyErr := identitycontext.VerifyDelegation(signer.Public(), request.Header.Get("X-Delegation-Grant"))
		if verifyErr != nil || identitycontext.ValidateDelegation(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "work-order:authorize", "session:"+fixture.session.ID) != nil {
			http.Error(writer, "invalid IAM delegation", http.StatusForbidden)
			return
		}
		var input workorderauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil || input.Validate() != nil {
			http.Error(writer, "invalid decision request", http.StatusBadRequest)
			return
		}
		decision := workorderauth.Decision{
			Allowed: !fixture.deny.Load(), PrincipalID: "principal-work-order-1",
			SubjectIssuer: fixture.session.Principal.Issuer, Subject: fixture.session.Principal.Subject,
			ActingOrganizationID: gatewayWorkOrderOrganizationID, SiteID: input.SiteID, WorkOrderID: input.WorkOrderID, Action: input.Action,
			PolicyRevision: "work-order-policy-1", ReasonCode: workorderauth.ReasonAllowExactScope, DecidedAt: now.Format(time.RFC3339Nano),
		}
		if fixture.deny.Load() {
			decision.ReasonCode = workorderauth.ReasonDenyScope
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(workorderauth.DecisionResponse{Decision: decision})
	}))
	t.Cleanup(iamServer.Close)

	workOrderServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.workOrderCalls.Add(1)
		fixture.lastUpstreamPath.Store(request.URL.Path)
		claims, verifyErr := identitycontext.VerifyDelegation(signer.Public(), request.Header.Get(workOrderReadContextHeader))
		action := string(workorderauth.ActionList)
		scopes := []string{"organization:" + gatewayWorkOrderOrganizationID, "site:" + gatewayWorkOrderSiteID}
		if strings.HasSuffix(request.URL.Path, "/"+gatewayWorkOrderID) {
			action = string(workorderauth.ActionRead)
			scopes = append(scopes, "work-order:"+gatewayWorkOrderID)
		}
		if verifyErr != nil || identitycontext.ValidateDelegationAnyScope(claims, now, "spiffe://hvac.local/platform-gateway", "work-order-service", action, scopes) != nil || claims.PrincipalID != "principal-work-order-1" || claims.PolicyRevision != "work-order-policy-1" {
			writer.Header().Set("Content-Type", "application/problem+json")
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"code":"WORK_ORDER_FORBIDDEN","retryable":false}`))
			return
		}
		workOrder := validGatewayWorkOrder(gatewayWorkOrderSiteID)
		if fixture.crossSite.Load() {
			workOrder.SiteID = gatewayWorkOrderOtherSiteID
		}
		writer.Header().Set("Content-Type", "application/json")
		if action == string(workorderauth.ActionList) {
			_ = json.NewEncoder(writer).Encode(workordermodel.ListResponse{SchemaVersion: workordermodel.SchemaVersion, Items: []workordermodel.WorkOrder{workOrder}, HasMore: false})
			return
		}
		_ = json.NewEncoder(writer).Encode(workOrder)
	}))
	t.Cleanup(workOrderServer.Close)

	fixture.handler = &handler{
		identity: &identityController{config: IdentityConfig{
			IAMURL: iamServer.URL, IAMAudience: "iam-service", IAMHTTPClient: iamServer.Client(),
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "gateway-policy-1",
			DelegationSigner: signer, DelegationTTL: 30 * time.Second,
		}, now: func() time.Time { return now }},
		workOrder: newWorkOrderController(&WorkOrderConfig{BackendBaseURL: workOrderServer.URL, BackendHTTPClient: workOrderServer.Client(), BackendAudience: "work-order-service"}),
	}
	return fixture
}

func validGatewayWorkOrder(siteID string) workordermodel.WorkOrder {
	assigneeID := "principal:operator"
	return workordermodel.WorkOrder{
		SchemaVersion: workordermodel.SchemaVersion, WorkOrderID: gatewayWorkOrderID, OrganizationID: gatewayWorkOrderOrganizationID, SiteID: siteID,
		Title: "Inspect AHU fan vibration", Description: "Verify the vibration and record the maintenance outcome.",
		Priority: workordermodel.PriorityHigh, Status: workordermodel.StatusOpen, AssigneeID: &assigneeID,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: gatewayWorkOrderAlarmID, Relationship: workordermodel.RelationshipOrigin}},
		Tasks:            workordermodel.TaskSummary{}, CompletionEvidence: []workordermodel.EvidenceReference{},
		Timeline: []workordermodel.TimelineEvent{{Operation: workordermodel.OperationCreate, ToStatus: workordermodel.StatusOpen, Reason: "created from Alarm", ActorType: "PRINCIPAL", ActorID: "principal:operator", OccurredAt: "2026-08-01T10:00:00Z", Version: 1}},
		Version:  1, CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z",
	}
}
