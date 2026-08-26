package workorderservice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const httpMutationWorkOrderID = "01930000-1000-7000-8000-000000000020"

func TestWorkOrderHTTPCreateAndAssignAreAuthorizedIdempotentAndVersioned(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
		NewWorkOrderID: func(time.Time) (string, error) { return httpMutationWorkOrderID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	createBody := `{"title":"Inspect AHU fan","description":"Validate vibration.","priority":"HIGH","sourceReferences":[{"domain":"ALARM","resourceId":"` + testAlarmID + `","relationship":"ORIGIN"}],"assigneeId":"principal:operator-a","teamId":"team:mechanical","dueAt":"2026-08-01T16:00:00Z"}`
	create := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", strings.NewReader(createBody))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "create-http-0001")
		request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderCreateAction}, httpTestOrganizationID, httpTestSiteID, "", mutationKeyScope("create-http-0001")))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	first := create()
	if first.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", first.Code, first.Body.String())
	}
	var created workordermodel.WorkOrder
	if json.NewDecoder(first.Body).Decode(&created) != nil || created.WorkOrderID != httpMutationWorkOrderID || created.Version != 1 || created.Status != workordermodel.StatusOpen {
		t.Fatalf("unexpected create projection: %#v", created)
	}
	second := create()
	if second.Code != http.StatusOK || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status=%d header=%s body=%s", second.Code, second.Header().Get("Idempotency-Replayed"), second.Body.String())
	}
	var replayed workordermodel.WorkOrder
	if json.NewDecoder(second.Body).Decode(&replayed) != nil || replayed.WorkOrderID != created.WorkOrderID || replayed.Version != 1 {
		t.Fatalf("unexpected replay: %#v", replayed)
	}

	assignBody := `{"expectedVersion":1,"assigneeId":"principal:operator-b","teamId":"team:controls","reason":"route to controls"}`
	assign := func(key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":assign", strings.NewReader(assignBody))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderAssignAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(key)))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	assigned := assign("assign-http-0001")
	if assigned.Code != http.StatusOK {
		t.Fatalf("assign status=%d body=%s", assigned.Code, assigned.Body.String())
	}
	var assignment workordermodel.WorkOrder
	if json.NewDecoder(assigned.Body).Decode(&assignment) != nil || assignment.Version != 2 || assignment.AssigneeID == nil || *assignment.AssigneeID != "principal:operator-b" || assignment.TeamID == nil || *assignment.TeamID != "team:controls" {
		t.Fatalf("unexpected assignment projection: %#v", assignment)
	}
	assignmentReplay := assign("assign-http-0001")
	if assignmentReplay.Code != http.StatusOK || assignmentReplay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("assignment replay status=%d header=%s body=%s", assignmentReplay.Code, assignmentReplay.Header().Get("Idempotency-Replayed"), assignmentReplay.Body.String())
	}
	stale := assign("assign-http-0002")
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "VERSION_CONFLICT") {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body.String())
	}
	current, err := store.Get(t.Context(), httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID)
	if err != nil || current.Version != 2 || len(current.Timeline) != 2 {
		t.Fatalf("stale request changed state: %#v err=%v", current, err)
	}
}

func TestWorkOrderHTTPMutationBoundaryFailsClosedBeforeStore(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
		NewWorkOrderID: func(time.Time) (string, error) { return httpMutationWorkOrderID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	validBody := `{"title":"Inspect AHU fan","description":"Validate vibration.","priority":"HIGH","sourceReferences":[{"domain":"ALARM","resourceId":"` + testAlarmID + `","relationship":"ORIGIN"}]}`
	readAuthority := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", strings.NewReader(validBody))
	readAuthority.Header.Set("Content-Type", "application/json")
	readAuthority.Header.Set("Idempotency-Key", "create-http-0002")
	readAuthority.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderCreateAction}, httpTestOrganizationID, httpTestSiteID, "", mutationKeyScope("create-http-0001")))
	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, readAuthority)
	if readRecorder.Code != http.StatusForbidden {
		t.Fatalf("read authority status=%d body=%s", readRecorder.Code, readRecorder.Body.String())
	}

	unknownField := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", strings.NewReader(strings.TrimSuffix(validBody, "}")+`,"status":"COMPLETED"}`))
	unknownField.Header.Set("Content-Type", "application/json")
	unknownField.Header.Set("Idempotency-Key", "create-http-0003")
	unknownField.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderCreateAction}, httpTestOrganizationID, httpTestSiteID, "", mutationKeyScope("create-http-0003")))
	unknownRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unknownRecorder, unknownField)
	if unknownRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknownRecorder.Code, unknownRecorder.Body.String())
	}

	unreviewed := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":link-alarm", strings.NewReader(`{}`))
	unreviewed.Header.Set("Content-Type", "application/json")
	unreviewed.Header.Set("Idempotency-Key", "complete-http-01")
	unreviewed.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{"work-order:complete"}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope("complete-http-01")))
	unreviewedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unreviewedRecorder, unreviewed)
	if unreviewedRecorder.Code != http.StatusNotFound {
		t.Fatalf("unreviewed route status=%d body=%s", unreviewedRecorder.Code, unreviewedRecorder.Body.String())
	}

	page, err := store.List(t.Context(), httpTestOrganizationID, httpTestSiteID, Filter{Limit: 10})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("rejected mutation reached store: %#v err=%v", page, err)
	}
}

func TestWorkOrderHTTPMutationContextIsBoundToIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
		NewWorkOrderID: func(time.Time) (string, error) { return httpMutationWorkOrderID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"title":"Inspect AHU fan","description":"Validate vibration.","priority":"HIGH","sourceReferences":[{"domain":"ALARM","resourceId":"` + testAlarmID + `","relationship":"ORIGIN"}]}`
	request := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-http-bound-1")
	request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderCreateAction}, httpTestOrganizationID, httpTestSiteID, "", mutationKeyScope("different-key-value")))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong key-bound context status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	page, err := store.List(t.Context(), httpTestOrganizationID, httpTestSiteID, Filter{Limit: 10})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("wrong key-bound context reached Store: %#v err=%v", page, err)
	}
}

func TestWorkOrderHTTPAssignmentRequiresExplicitOwnershipTuple(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store, err := NewMemoryStore([]workordermodel.WorkOrder{validHTTPWorkOrder()})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"expectedVersion":1,"assigneeId":"principal:operator-b","reason":"missing team"}`,
		`{"expectedVersion":1,"teamId":"team:controls","reason":"missing assignee"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpTestWorkOrderID+":assign", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "assign-http-missing")
		request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderAssignAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID, mutationKeyScope("assign-http-missing")))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
	current, err := store.Get(t.Context(), httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID)
	if err != nil || current.Version != 1 {
		t.Fatalf("invalid tuple changed state: %#v err=%v", current, err)
	}
}
