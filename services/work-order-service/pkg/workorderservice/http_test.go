package workorderservice

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
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	httpTestOrganizationID = "01920000-0000-7000-8000-000000000001"
	httpTestSiteID         = "01920000-0001-7000-8000-000000000001"
	httpTestOtherSiteID    = "01920000-0001-7000-8000-000000000002"
	httpTestWorkOrderID    = "01920000-1000-7000-8000-000000000001"
	testAlarmID            = "01920000-2000-7000-8000-000000000001"
)

type fakeStore struct {
	item       workordermodel.WorkOrder
	listErr    error
	calls      atomic.Int32
	lastFilter Filter
}

func (store *fakeStore) List(context.Context, string, string, Filter) (workordermodel.ListResponse, error) {
	store.calls.Add(1)
	if store.listErr != nil {
		return workordermodel.ListResponse{}, store.listErr
	}
	return workordermodel.ListResponse{SchemaVersion: workordermodel.SchemaVersion, Items: []workordermodel.WorkOrder{store.item}}, nil
}

func (store *fakeStore) Get(context.Context, string, string, string) (workordermodel.WorkOrder, error) {
	store.calls.Add(1)
	return store.item, nil
}

func TestWorkOrderHTTPListAndDetail(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store := &fakeStore{item: validHTTPWorkOrder()}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders?status=OPEN&priority=HIGH&assigneeId=principal%3Aoperator&limit=25", nil)
	list.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, ""))
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var response workordermodel.ListResponse
	if json.NewDecoder(listRecorder.Body).Decode(&response) != nil || len(response.Items) != 1 || response.Items[0].WorkOrderID != httpTestWorkOrderID {
		t.Fatalf("unexpected list %#v", response)
	}

	detail := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpTestWorkOrderID, nil)
	detail.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderReadAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID))
	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
}

func TestWorkOrderHTTPRejectsUntrustedScopeBeforeStore(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store := &fakeStore{item: validHTTPWorkOrder()}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	missing := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	forged := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	forged.Header.Set("X-Organization-ID", httpTestOrganizationID)
	forged.Header.Set("X-Work-Order-ID", httpTestWorkOrderID)
	forged.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, ""))
	crossSite := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	crossSite.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestOtherSiteID, ""))
	multiAction := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	multiAction.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction, WorkOrderReadAction}, httpTestOrganizationID, httpTestSiteID, ""))
	expandedScope := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	expandedScope.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, "", "site:"+httpTestOtherSiteID))
	wrongAction := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	wrongAction.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderReadAction}, httpTestOrganizationID, httpTestSiteID, ""))
	expired := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	expired.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now.Add(-time.Minute), []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, ""))
	tampered := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", nil)
	tampered.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, "")+"x")

	for index, request := range []*http.Request{missing, forged, crossSite, multiAction, expandedScope, wrongAction, expired, tampered} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if index == 1 {
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("forged status=%d", recorder.Code)
			}
		} else if recorder.Code != http.StatusForbidden {
			t.Fatalf("index=%d status=%d body=%s", index, recorder.Code, recorder.Body.String())
		}
	}
	if store.calls.Load() != 0 {
		t.Fatalf("unauthorized request reached Store: %d", store.calls.Load())
	}
}

func TestWorkOrderHTTPRejectsInvalidBoundary(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store := &fakeStore{item: validHTTPWorkOrder()}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	contextValue := signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, "")
	for _, target := range []string{
		InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders?status=ASSIGNED",
		InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders?status=OPEN&status=BLOCKED",
		InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders?unknown=1",
		InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders?cursor=%20%20%20",
		InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders?limit=101",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set(WorkOrderReadContextHeader, contextValue)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d", target, recorder.Code)
		}
	}

	post := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", strings.NewReader(`{}`))
	post.Header.Set(WorkOrderReadContextHeader, contextValue)
	postRecorder := httptest.NewRecorder()
	handler.ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusMethodNotAllowed || postRecorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d allow=%s", postRecorder.Code, postRecorder.Header().Get("Allow"))
	}

	store.listErr = ErrInvalidCursor
	cursorRequest := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders?cursor=opaque", nil)
	cursorRequest.Header.Set(WorkOrderReadContextHeader, contextValue)
	cursorRecorder := httptest.NewRecorder()
	handler.ServeHTTP(cursorRecorder, cursorRequest)
	if cursorRecorder.Code != http.StatusBadRequest || !strings.Contains(cursorRecorder.Body.String(), "WORK_ORDER_CURSOR_INVALID") {
		t.Fatalf("status=%d body=%s", cursorRecorder.Code, cursorRecorder.Body.String())
	}
}

func TestWorkOrderHTTPRejectsProjectionOutsideRequestedFilter(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	item := validHTTPWorkOrder()
	item.Priority = workordermodel.PriorityLow
	store := &fakeStore{item: item}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders?priority=HIGH", nil)
	request.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderListAction}, httpTestOrganizationID, httpTestSiteID, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "WORK_ORDER_RESPONSE_INVALID") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func signContext(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, actions []string, organizationID, siteID, workOrderID string, extraScopes ...string) string {
	t.Helper()
	scopes := []string{"organization:" + organizationID, "site:" + siteID}
	if workOrderID != "" {
		scopes = append(scopes, "work-order:"+workOrderID)
	}
	scopes = append(scopes, extraScopes...)
	value, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: DefaultGatewaySPIFFEID, Subject: "operator", SubjectIssuer: "https://identity.example.test",
		DisplayName: "Operator", ExecutingService: DefaultGatewaySPIFFEID, Audience: DefaultAudience,
		ActingOrganizationID: organizationID, Actions: actions, Scopes: scopes,
		PolicyRevision: "policy-1", SessionID: "session-1", IssuedAt: now.Add(-time.Second).Unix(),
		ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "id-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newSigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func validHTTPWorkOrder() workordermodel.WorkOrder {
	assigneeID := "principal:operator"
	return workordermodel.WorkOrder{
		SchemaVersion: workordermodel.SchemaVersion, WorkOrderID: httpTestWorkOrderID, OrganizationID: httpTestOrganizationID, SiteID: httpTestSiteID,
		Title: "Inspect AHU fan vibration", Description: "Verify the vibration and record the maintenance outcome.",
		Priority: workordermodel.PriorityHigh, Status: workordermodel.StatusOpen, AssigneeID: &assigneeID,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: testAlarmID, Relationship: workordermodel.RelationshipOrigin}},
		Tasks:            workordermodel.TaskSummary{}, CompletionEvidence: []workordermodel.EvidenceReference{},
		Timeline: []workordermodel.TimelineEvent{{Operation: workordermodel.OperationCreate, ToStatus: workordermodel.StatusOpen, Reason: "created from Alarm", ActorType: "PRINCIPAL", ActorID: "principal:operator", OccurredAt: "2026-08-01T10:00:00Z", Version: 1}},
		Version:  1, CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z",
	}
}
