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

func TestWorkOrderHTTPLifecycleRunsReviewedGraphWithExactContext(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	assignee := "principal:operator-a"
	initial, err := workordermodel.Create(workordermodel.CreateInput{
		WorkOrderID: httpMutationWorkOrderID, TenantID: httpTestOrganizationID, SiteID: httpTestSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: testAlarmID, Relationship: workordermodel.RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "create-lifecycle-http", OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore([]workordermodel.WorkOrder{initial})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	call := func(suffix, action, key, body string, at time.Time) workordermodel.WorkOrder {
		now = at
		request := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+suffix, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{action}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(key)))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("suffix=%s status=%d body=%s", suffix, recorder.Code, recorder.Body.String())
		}
		var workOrder workordermodel.WorkOrder
		if json.NewDecoder(recorder.Body).Decode(&workOrder) != nil {
			t.Fatalf("suffix=%s invalid response", suffix)
		}
		return workOrder
	}

	planned := call(":plan", WorkOrderPlanAction, "plan-http-000001", "{\"expectedVersion\":1,\"scheduledStart\":\"2026-08-03T01:00:00Z\",\"dueAt\":\"2026-08-03T04:00:00Z\",\"reason\":\"plan maintenance\"}", now.Add(time.Hour))
	if planned.Status != workordermodel.StatusOpen || planned.Version != 2 || planned.ScheduledStart == nil {
		t.Fatalf("plan=%#v", planned)
	}
	started := call(":start", WorkOrderStartAction, "start-http-00001", "{\"expectedVersion\":2,\"reason\":\"start work\"}", time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC))
	if started.Status != workordermodel.StatusInProgress || started.Version != 3 {
		t.Fatalf("start=%#v", started)
	}
	blocked := call(":block", WorkOrderBlockAction, "block-http-00001", "{\"expectedVersion\":3,\"reason\":\"part unavailable\"}", time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC))
	if blocked.Status != workordermodel.StatusBlocked || blocked.Version != 4 {
		t.Fatalf("block=%#v", blocked)
	}
	resumed := call(":resume", WorkOrderResumeAction, "resume-http-0001", "{\"expectedVersion\":4,\"reason\":\"part arrived\"}", time.Date(2026, 8, 3, 2, 30, 0, 0, time.UTC))
	if resumed.Status != workordermodel.StatusInProgress || resumed.Version != 5 {
		t.Fatalf("resume=%#v", resumed)
	}
	completed := call(":complete", WorkOrderCompleteAction, "complete-http-001", "{\"expectedVersion\":5,\"completionEvidence\":[{\"kind\":\"report\",\"reference\":\"object://report/http-1\",\"capturedAt\":\"2026-08-03T02:45:00Z\"}],\"reason\":\"verified\"}", time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC))
	if completed.Status != workordermodel.StatusCompleted || completed.Version != 6 || len(completed.CompletionEvidence) != 1 {
		t.Fatalf("complete=%#v", completed)
	}
	reopened := call(":reopen", WorkOrderReopenAction, "reopen-http-0001", "{\"expectedVersion\":6,\"reason\":\"vibration recurred\"}", time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC))
	if reopened.Status != workordermodel.StatusOpen || reopened.Version != 7 || len(reopened.CompletionEvidence) != 1 {
		t.Fatalf("reopen=%#v", reopened)
	}
	cancelled := call(":cancel", WorkOrderCancelAction, "cancel-http-0001", "{\"expectedVersion\":7,\"reason\":\"asset retired\"}", time.Date(2026, 8, 3, 5, 0, 0, 0, time.UTC))
	if cancelled.Status != workordermodel.StatusCancelled || cancelled.Version != 8 || len(cancelled.Timeline) != 8 {
		t.Fatalf("cancel=%#v", cancelled)
	}
}

func TestWorkOrderHTTPLifecycleRejectsWrongActionMissingEvidenceAndStaleVersion(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	assignee := "principal:operator-a"
	initial, _ := workordermodel.Create(workordermodel.CreateInput{
		WorkOrderID: httpMutationWorkOrderID, TenantID: httpTestOrganizationID, SiteID: httpTestSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: testAlarmID, Relationship: workordermodel.RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "create-lifecycle-http", OccurredAt: now.Format(time.RFC3339Nano),
	})
	store, _ := NewMemoryStore([]workordermodel.WorkOrder{initial})
	handler, _ := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})

	wrongKey := "start-http-wrong1"
	wrong := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":start", strings.NewReader("{\"expectedVersion\":1,\"reason\":\"start\"}"))
	wrong.Header.Set("Content-Type", "application/json")
	wrong.Header.Set("Idempotency-Key", wrongKey)
	wrong.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderBlockAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(wrongKey)))
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrong)
	if wrongRecorder.Code != http.StatusForbidden {
		t.Fatalf("wrong action status=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}

	startKey := "start-http-valid1"
	now = now.Add(time.Hour)
	start := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":start", strings.NewReader("{\"expectedVersion\":1,\"reason\":\"start\"}"))
	start.Header.Set("Content-Type", "application/json")
	start.Header.Set("Idempotency-Key", startKey)
	start.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderStartAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(startKey)))
	startRecorder := httptest.NewRecorder()
	handler.ServeHTTP(startRecorder, start)
	if startRecorder.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", startRecorder.Code, startRecorder.Body.String())
	}

	completeKey := "complete-http-bad1"
	now = now.Add(time.Hour)
	complete := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":complete", strings.NewReader("{\"expectedVersion\":2,\"reason\":\"complete\"}"))
	complete.Header.Set("Content-Type", "application/json")
	complete.Header.Set("Idempotency-Key", completeKey)
	complete.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderCompleteAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(completeKey)))
	completeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(completeRecorder, complete)
	if completeRecorder.Code != http.StatusUnprocessableEntity || !strings.Contains(completeRecorder.Body.String(), "WORK_ORDER_LIFECYCLE_INVALID") {
		t.Fatalf("complete status=%d body=%s", completeRecorder.Code, completeRecorder.Body.String())
	}

	staleKey := "block-http-stale1"
	stale := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":block", strings.NewReader("{\"expectedVersion\":1,\"reason\":\"blocked\"}"))
	stale.Header.Set("Content-Type", "application/json")
	stale.Header.Set("Idempotency-Key", staleKey)
	stale.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderBlockAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(staleKey)))
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, stale)
	if staleRecorder.Code != http.StatusConflict || !strings.Contains(staleRecorder.Body.String(), "VERSION_CONFLICT") {
		t.Fatalf("stale status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
	current, _ := store.Get(t.Context(), httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID)
	if current.Version != 2 || current.Status != workordermodel.StatusInProgress || len(current.Timeline) != 2 {
		t.Fatalf("rejected mutation changed state: %#v", current)
	}
}

func TestWorkOrderHTTPLifecyclePreconditionRequiresExactWriteContextAndKeyScope(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	assignee := "principal:operator-a"
	initial, err := workordermodel.Create(workordermodel.CreateInput{
		WorkOrderID: httpMutationWorkOrderID, TenantID: httpTestOrganizationID, SiteID: httpTestSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: testAlarmID, Relationship: workordermodel.RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "create-precondition-http", OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore([]workordermodel.WorkOrder{initial})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	path := InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders/" + httpMutationWorkOrderID + ":lifecycle-precondition"
	key := "start-precondition-0001"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderStartAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(key)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var before workordermodel.WorkOrder
	if json.NewDecoder(recorder.Body).Decode(&before) != nil || before.Version != 1 || before.Status != workordermodel.StatusOpen {
		t.Fatalf("precondition=%#v", before)
	}

	wrongKeyRequest := httptest.NewRequest(http.MethodGet, path, nil)
	wrongKeyRequest.Header.Set("Idempotency-Key", "start-precondition-0002")
	wrongKeyRequest.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderStartAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(key)))
	wrongKeyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongKeyRecorder, wrongKeyRequest)
	if wrongKeyRecorder.Code != http.StatusForbidden {
		t.Fatalf("wrong key status=%d body=%s", wrongKeyRecorder.Code, wrongKeyRecorder.Body.String())
	}

	readContextRequest := httptest.NewRequest(http.MethodGet, path, nil)
	readContextRequest.Header.Set("Idempotency-Key", key)
	readContextRequest.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderReadAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID))
	readContextRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readContextRecorder, readContextRequest)
	if readContextRecorder.Code != http.StatusForbidden {
		t.Fatalf("read context status=%d body=%s", readContextRecorder.Code, readContextRecorder.Body.String())
	}
}

func TestWorkOrderHTTPLifecycleRejectsStoreAuditEvidenceDrift(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	assignee := "principal:operator"
	initial, err := workordermodel.Create(workordermodel.CreateInput{
		WorkOrderID: httpMutationWorkOrderID, TenantID: httpTestOrganizationID, SiteID: httpTestSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: testAlarmID, Relationship: workordermodel.RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:operator", PolicyRevision: "policy-1", CorrelationID: "create-audit-drift", OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "start-audit-drift-0001"
	started, err := workordermodel.ApplyLifecycle(initial, workordermodel.LifecycleInput{
		Operation: workordermodel.OperationStart, ExpectedVersion: 1, Reason: "begin repair",
		ActorType: "PRINCIPAL", ActorID: "principal:operator", PolicyRevision: "policy-1", CorrelationID: key,
		OccurredAt: now.Add(time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	started.Timeline[len(started.Timeline)-1].Reason = "tampered store reason"
	if err := started.Validate(); err != nil {
		t.Fatalf("drift fixture must remain structurally valid: %v", err)
	}
	store := &fakeStore{item: started}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now.Add(time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, InternalSiteWorkOrdersPrefix+httpTestSiteID+"/work-orders/"+httpMutationWorkOrderID+":start", strings.NewReader("{\"expectedVersion\":1,\"reason\":\"begin repair\"}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now.Add(time.Minute), []string{WorkOrderStartAction}, httpTestOrganizationID, httpTestSiteID, httpMutationWorkOrderID, mutationKeyScope(key)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "WORK_ORDER_RESPONSE_INVALID") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
