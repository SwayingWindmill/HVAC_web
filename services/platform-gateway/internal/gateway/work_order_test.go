package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
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
	gatewayWorkOrderCSRF           = "work-order-csrf-fixture"
	gatewayWorkOrderOrigin         = "https://web.example.test"
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
	handler                 *handler
	session                 bffSession
	iamCalls                atomic.Int64
	workOrderCalls          atomic.Int64
	deny                    atomic.Bool
	crossSite               atomic.Bool
	lifecycleDrift          atomic.Bool
	lifecycleAuditDrift     atomic.Bool
	lastUpstreamPath        atomic.Value
	lastUpstreamMethod      atomic.Value
	lastUpstreamBody        atomic.Value
	lastUpstreamIdempotency atomic.Value
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
	fixture.lastUpstreamMethod.Store("")
	fixture.lastUpstreamBody.Store("")
	fixture.lastUpstreamIdempotency.Store("")
	fixture.session = bffSession{Session: sessionstore.Session{
		ID:                   "session-work-order-1",
		Principal:            identitycontext.UserPrincipal{Subject: "subject-work-order", Issuer: "https://issuer.example", DisplayName: "Work Order Operator", Email: "work-order@example.test", Roles: []string{"operator"}},
		ActingOrganizationID: gatewayWorkOrderOrganizationID,
		ExpiresAt:            now.Add(15 * time.Minute),
	}, CSRFToken: gatewayWorkOrderCSRF}
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
			ActingOrganizationID: gatewayWorkOrderOrganizationID, SiteID: input.SiteID, WorkOrderID: input.WorkOrderID,
			AssigneeID: input.AssigneeID, TeamID: input.TeamID, Action: input.Action,
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
		fixture.lastUpstreamMethod.Store(request.Method)
		fixture.lastUpstreamIdempotency.Store(request.Header.Get("Idempotency-Key"))
		body, _ := io.ReadAll(request.Body)
		fixture.lastUpstreamBody.Store(string(body))
		header := workOrderReadContextHeader
		action := string(workorderauth.ActionList)
		scopes := []string{"organization:" + gatewayWorkOrderOrganizationID, "site:" + gatewayWorkOrderSiteID}
		precondition := strings.HasSuffix(request.URL.Path, "/"+gatewayWorkOrderID+":lifecycle-precondition")
		if precondition {
			header = workOrderWriteContextHeader
			scopes = append(scopes, "work-order:"+gatewayWorkOrderID, workOrderMutationKeyScope(request.Header.Get("Idempotency-Key")))
		}
		if request.Method == http.MethodPost {
			header = workOrderWriteContextHeader
			action = string(workorderauth.ActionCreate)
			scopes = append(scopes, workOrderMutationKeyScope(request.Header.Get("Idempotency-Key")))
		}
		if strings.HasSuffix(request.URL.Path, "/"+gatewayWorkOrderID) {
			action = string(workorderauth.ActionRead)
			scopes = append(scopes, "work-order:"+gatewayWorkOrderID)
		}
		if strings.HasSuffix(request.URL.Path, "/"+gatewayWorkOrderID+":assign") {
			header = workOrderWriteContextHeader
			action = string(workorderauth.ActionAssign)
			scopes = append(scopes, "work-order:"+gatewayWorkOrderID)
		}
		for suffix, lifecycleAction := range map[string]workorderauth.Action{
			":plan": workorderauth.ActionPlan, ":start": workorderauth.ActionStart, ":block": workorderauth.ActionBlock,
			":resume": workorderauth.ActionResume, ":complete": workorderauth.ActionComplete,
			":cancel": workorderauth.ActionCancel, ":reopen": workorderauth.ActionReopen,
		} {
			if strings.HasSuffix(request.URL.Path, "/"+gatewayWorkOrderID+suffix) {
				header = workOrderWriteContextHeader
				action = string(lifecycleAction)
				scopes = append(scopes, "work-order:"+gatewayWorkOrderID)
			}
		}
		claims, verifyErr := identitycontext.VerifyDelegation(signer.Public(), request.Header.Get(header))
		preconditionActionOK := !precondition
		if precondition && verifyErr == nil && len(claims.Actions) == 1 {
			switch workorderauth.Action(claims.Actions[0]) {
			case workorderauth.ActionPlan, workorderauth.ActionStart, workorderauth.ActionBlock, workorderauth.ActionResume, workorderauth.ActionComplete, workorderauth.ActionCancel, workorderauth.ActionReopen:
				action = claims.Actions[0]
				preconditionActionOK = true
			}
		}
		if verifyErr != nil || !preconditionActionOK || identitycontext.ValidateDelegationAnyScope(claims, now, "spiffe://hvac.local/platform-gateway", "work-order-service", action, scopes) != nil || claims.PrincipalID != "principal-work-order-1" || claims.PolicyRevision != "work-order-policy-1" {
			writer.Header().Set("Content-Type", "application/problem+json")
			writer.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(writer).Encode(upstreamWorkOrderProblem{Type: "https://example.test/problems/work-order-access-denied", Title: "denied", Status: http.StatusForbidden, Detail: "denied", Code: "WORK_ORDER_ACCESS_DENIED"})
			return
		}
		workOrder := validGatewayWorkOrder(gatewayWorkOrderSiteID)
		if fixture.crossSite.Load() {
			workOrder.SiteID = gatewayWorkOrderOtherSiteID
		}
		if precondition {
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(workOrder)
			return
		}
		if action == string(workorderauth.ActionAssign) {
			assigneeID := "principal:operator-b"
			teamID := "team:controls"
			fromStatus := workordermodel.StatusOpen
			workOrder.AssigneeID = &assigneeID
			workOrder.TeamID = &teamID
			workOrder.Version = 2
			workOrder.UpdatedAt = "2026-08-01T10:01:00Z"
			workOrder.Timeline = append(workOrder.Timeline, workordermodel.TimelineEvent{
				Operation: workordermodel.OperationAssign, FromStatus: &fromStatus, ToStatus: workordermodel.StatusOpen,
				Reason: "route to controls", ActorType: "PRINCIPAL", ActorID: "principal-work-order-1",
				AssigneeID: &assigneeID, TeamID: &teamID, OccurredAt: workOrder.UpdatedAt, Version: 2,
			})
		}
		if action == string(workorderauth.ActionStart) {
			fromStatus := workordermodel.StatusOpen
			policyRevision := "work-order-policy-1"
			correlationID := request.Header.Get("Idempotency-Key")
			workOrder.Status = workordermodel.StatusInProgress
			workOrder.Version = 2
			workOrder.UpdatedAt = "2026-08-01T10:01:00Z"
			workOrder.Timeline = append(workOrder.Timeline, workordermodel.TimelineEvent{
				Operation: workordermodel.OperationStart, FromStatus: &fromStatus, ToStatus: workordermodel.StatusInProgress,
				Reason: "begin repair", ActorType: "PRINCIPAL", ActorID: "principal-work-order-1", PolicyRevision: &policyRevision, CorrelationID: &correlationID,
				OccurredAt: workOrder.UpdatedAt, Version: 2,
			})
		}
		if fixture.lifecycleDrift.Load() && action == string(workorderauth.ActionStart) {
			workOrder.Title = "tampered downstream title"
		}
		if fixture.lifecycleAuditDrift.Load() && action == string(workorderauth.ActionStart) {
			workOrder.Timeline[len(workOrder.Timeline)-1].Reason = "tampered downstream reason"
		}
		writer.Header().Set("Content-Type", "application/json")
		switch action {
		case string(workorderauth.ActionList):
			_ = json.NewEncoder(writer).Encode(workordermodel.ListResponse{SchemaVersion: workordermodel.SchemaVersion, Items: []workordermodel.WorkOrder{workOrder}, HasMore: false})
		case string(workorderauth.ActionCreate):
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(workOrder)
		default:
			_ = json.NewEncoder(writer).Encode(workOrder)
		}
	}))
	t.Cleanup(workOrderServer.Close)

	fixture.handler = &handler{
		identity: &identityController{config: IdentityConfig{
			PublicOrigin: gatewayWorkOrderOrigin,
			IAMURL:       iamServer.URL, IAMAudience: "iam-service", IAMHTTPClient: iamServer.Client(),
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
		Timeline: []workordermodel.TimelineEvent{{Operation: workordermodel.OperationCreate, ToStatus: workordermodel.StatusOpen, Reason: "created from Alarm", ActorType: "PRINCIPAL", ActorID: "principal:operator", AssigneeID: &assigneeID, OccurredAt: "2026-08-01T10:00:00Z", Version: 1}},
		Version:  1, CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z",
	}
}
