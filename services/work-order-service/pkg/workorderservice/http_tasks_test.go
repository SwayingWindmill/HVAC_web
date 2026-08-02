package workorderservice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	httpTaskOneID = "01930000-5000-7000-8000-000000000061"
	httpTaskTwoID = "01930000-5000-7000-8000-000000000062"
)

func TestWorkOrderHTTPTaskChecklistIsAuthorizedIdempotentVersionedAndOrdered(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store, err := NewMemoryStoreWithTasks([]workordermodel.WorkOrder{validHTTPWorkOrder()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	allocated := 0
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
		NewTaskID: func(time.Time) (string, error) {
			allocated++
			if allocated == 1 {
				return httpTaskOneID, nil
			}
			return httpTaskTwoID, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	basePath := InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders/" + httpTestWorkOrderID

	appendTask := func(key string, expectedVersion uint64, title string) *httptest.ResponseRecorder {
		body := `{"expectedWorkOrderVersion":` + strconv.FormatUint(expectedVersion, 10) + `,"title":"` + title + `","reason":"append governed task"}`
		request := httptest.NewRequest(http.MethodPost, basePath+"/tasks", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderTaskAppendAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID, mutationKeyScope(key)))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	first := appendTask("task-http-key-0001", 1, "Inspect fan bearings")
	if first.Code != http.StatusCreated {
		t.Fatalf("first append status=%d body=%s", first.Code, first.Body.String())
	}
	var firstChecklist workordermodel.TaskChecklist
	if json.NewDecoder(first.Body).Decode(&firstChecklist) != nil || firstChecklist.WorkOrderVersion != 2 || len(firstChecklist.Tasks) != 1 || firstChecklist.Tasks[0].TaskID != httpTaskOneID {
		t.Fatalf("first checklist=%#v", firstChecklist)
	}
	firstReplay := appendTask("task-http-key-0001", 1, "Inspect fan bearings")
	if firstReplay.Code != http.StatusOK || firstReplay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("append replay status=%d header=%s body=%s", firstReplay.Code, firstReplay.Header().Get("Idempotency-Replayed"), firstReplay.Body.String())
	}

	now = now.Add(time.Minute)
	second := appendTask("task-http-key-0002", 2, "Record vibration")
	if second.Code != http.StatusCreated {
		t.Fatalf("second append status=%d body=%s", second.Code, second.Body.String())
	}

	now = now.Add(time.Minute)
	statusKey := "task-http-key-0003"
	statusRequest := httptest.NewRequest(http.MethodPost, basePath+"/tasks/"+httpTaskOneID+":status", strings.NewReader(`{"expectedWorkOrderVersion":3,"expectedTaskVersion":1,"status":"COMPLETED","reason":"inspection complete"}`))
	statusRequest.Header.Set("Content-Type", "application/json")
	statusRequest.Header.Set("Idempotency-Key", statusKey)
	statusRequest.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderTaskStatusAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID, "task:"+httpTaskOneID, mutationKeyScope(statusKey)))
	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("task status=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var statusChecklist workordermodel.TaskChecklist
	if json.NewDecoder(statusRecorder.Body).Decode(&statusChecklist) != nil || statusChecklist.WorkOrderVersion != 4 || statusChecklist.Summary.Completed != 1 || statusChecklist.Tasks[0].Version != 2 {
		t.Fatalf("status checklist=%#v", statusChecklist)
	}

	now = now.Add(time.Minute)
	reorderKey := "task-http-key-0004"
	reorderRequest := httptest.NewRequest(http.MethodPost, basePath+"/tasks:reorder", strings.NewReader(`{"expectedWorkOrderVersion":4,"taskIds":["`+httpTaskTwoID+`","`+httpTaskOneID+`"],"reason":"measure first"}`))
	reorderRequest.Header.Set("Content-Type", "application/json")
	reorderRequest.Header.Set("Idempotency-Key", reorderKey)
	reorderRequest.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderTaskReorderAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID, mutationKeyScope(reorderKey)))
	reorderRecorder := httptest.NewRecorder()
	handler.ServeHTTP(reorderRecorder, reorderRequest)
	if reorderRecorder.Code != http.StatusOK {
		t.Fatalf("task reorder=%d body=%s", reorderRecorder.Code, reorderRecorder.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, basePath+"/tasks", nil)
	listRequest.Header.Set(WorkOrderReadContextHeader, signContext(t, signer, now, []string{WorkOrderTaskListAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID))
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	var listed workordermodel.TaskChecklist
	if listRecorder.Code != http.StatusOK || json.NewDecoder(listRecorder.Body).Decode(&listed) != nil || listed.WorkOrderVersion != 5 || listed.Tasks[0].TaskID != httpTaskTwoID || listed.Tasks[1].TaskID != httpTaskOneID {
		t.Fatalf("task list status=%d checklist=%#v body=%s", listRecorder.Code, listed, listRecorder.Body.String())
	}

	invalidKey := "task-http-key-0005"
	invalidOrder := httptest.NewRequest(http.MethodPost, basePath+"/tasks:reorder", strings.NewReader(`{"expectedWorkOrderVersion":5,"taskIds":["`+httpTaskOneID+`","`+httpTaskOneID+`"],"reason":"duplicate"}`))
	invalidOrder.Header.Set("Content-Type", "application/json")
	invalidOrder.Header.Set("Idempotency-Key", invalidKey)
	invalidOrder.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderTaskReorderAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID, mutationKeyScope(invalidKey)))
	invalidRecorder := httptest.NewRecorder()
	handler.ServeHTTP(invalidRecorder, invalidOrder)
	if invalidRecorder.Code != http.StatusUnprocessableEntity || !strings.Contains(invalidRecorder.Body.String(), "WORK_ORDER_TASK_INVALID") {
		t.Fatalf("invalid order status=%d body=%s", invalidRecorder.Code, invalidRecorder.Body.String())
	}
}

func TestWorkOrderHTTPTaskBoundaryRejectsWrongScopeAndUnreviewedRoutes(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	signer := newSigner(t)
	store, err := NewMemoryStoreWithTasks([]workordermodel.WorkOrder{validHTTPWorkOrder()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{Store: store, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now }, NewTaskID: func(time.Time) (string, error) { return httpTaskOneID, nil }})
	if err != nil {
		t.Fatal(err)
	}
	basePath := InternalSiteWorkOrdersPrefix + httpTestSiteID + "/work-orders/" + httpTestWorkOrderID
	key := "task-http-wrong-01"
	wrongAction := httptest.NewRequest(http.MethodPost, basePath+"/tasks", strings.NewReader(`{"expectedWorkOrderVersion":1,"title":"Inspect","reason":"wrong scope"}`))
	wrongAction.Header.Set("Content-Type", "application/json")
	wrongAction.Header.Set("Idempotency-Key", key)
	wrongAction.Header.Set(WorkOrderWriteContextHeader, signContext(t, signer, now, []string{WorkOrderTaskStatusAction}, httpTestOrganizationID, httpTestSiteID, httpTestWorkOrderID, mutationKeyScope(key)))
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrongAction)
	if wrongRecorder.Code != http.StatusForbidden {
		t.Fatalf("wrong action status=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodPost, basePath+"/tasks/"+httpTaskOneID+":delete", strings.NewReader(`{}`))
	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNotFound {
		t.Fatalf("unreviewed delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
}
