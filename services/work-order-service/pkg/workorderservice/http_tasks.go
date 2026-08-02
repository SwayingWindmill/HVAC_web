package workorderservice

import (
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

type appendTaskRequest struct {
	ExpectedWorkOrderVersion uint64 `json:"expectedWorkOrderVersion"`
	Title                    string `json:"title"`
	Reason                   string `json:"reason"`
}

type taskStatusRequest struct {
	ExpectedWorkOrderVersion uint64                    `json:"expectedWorkOrderVersion"`
	ExpectedTaskVersion      uint64                    `json:"expectedTaskVersion"`
	Status                   workordermodel.TaskStatus `json:"status"`
	Reason                   string                    `json:"reason"`
}

type reorderTasksRequest struct {
	ExpectedWorkOrderVersion uint64   `json:"expectedWorkOrderVersion"`
	TaskIDs                  []string `json:"taskIds"`
	Reason                   string   `json:"reason"`
}

func (handler *httpHandler) handleTaskList(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	if request.URL.RawQuery != "" {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_TASK_INVALID", "Work Order task invalid", "Work Order task list does not accept query parameters.", false)
		return
	}
	claims, ok := handler.authorize(request, WorkOrderTaskListAction, []string{"site:" + route.siteID, "work-order:" + route.workOrderID}, false)
	if !ok {
		handler.writeAccessDenied(writer)
		return
	}
	if handler.taskStore == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	checklist, err := handler.taskStore.ListTasks(request.Context(), claims.ActingOrganizationID, route.siteID, route.workOrderID)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if checklist.Validate(claims.ActingOrganizationID, route.siteID, route.workOrderID) != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a task checklist outside the requested scope.", true)
		return
	}
	writeJSON(writer, http.StatusOK, checklist)
}

func (handler *httpHandler) handleTaskAppend(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	idempotencyKey, claims, ok := handler.authorizeTaskMutation(writer, request, route, WorkOrderTaskAppendAction, "append", "")
	if !ok {
		return
	}
	var body appendTaskRequest
	if !decodeStrictJSON(request, &body) || body.ExpectedWorkOrderVersion == 0 {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_TASK_INVALID", "Work Order task invalid", "The append request is not a closed bounded JSON object.", false)
		return
	}
	now := handler.now().UTC()
	taskID, err := handler.newTaskID(now)
	if err != nil || !workordermodel.IsUUIDv7(taskID) {
		handler.writeProblem(writer, http.StatusServiceUnavailable, "WORK_ORDER_UNAVAILABLE", "Work Order unavailable", "Work Order Service cannot allocate an authoritative task identity.", true)
		return
	}
	result, err := handler.taskStore.AppendTask(request.Context(), claims.ActingOrganizationID, route.siteID, route.workOrderID, AppendTaskMutation{
		TaskID: taskID, ExpectedWorkOrderVersion: body.ExpectedWorkOrderVersion, Title: body.Title, Reason: body.Reason,
		ActorType: "PRINCIPAL", ActorID: initiatingActorID(claims), PolicyRevision: claims.PolicyRevision,
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if !validTaskMutationResponse(result.Checklist, claims.ActingOrganizationID, route.siteID, route.workOrderID, body.ExpectedWorkOrderVersion) {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned an append checklist outside the requested mutation.", true)
		return
	}
	if !result.Replayed {
		found := false
		for _, task := range result.Checklist.Tasks {
			found = found || task.TaskID == taskID
		}
		if !found {
			handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store did not return the appended task.", true)
			return
		}
	}
	if result.Replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
		writeJSON(writer, http.StatusOK, result.Checklist)
		return
	}
	writeJSON(writer, http.StatusCreated, result.Checklist)
}

func (handler *httpHandler) handleTaskStatus(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	idempotencyKey, claims, ok := handler.authorizeTaskMutation(writer, request, route, WorkOrderTaskStatusAction, "status", route.taskID)
	if !ok {
		return
	}
	var body taskStatusRequest
	if !decodeStrictJSON(request, &body) || body.ExpectedWorkOrderVersion == 0 || body.ExpectedTaskVersion == 0 {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_TASK_INVALID", "Work Order task invalid", "The status request is not a closed bounded JSON object.", false)
		return
	}
	now := handler.now().UTC()
	result, err := handler.taskStore.SetTaskStatus(request.Context(), claims.ActingOrganizationID, route.siteID, route.workOrderID, TaskStatusMutation{
		TaskID: route.taskID, ExpectedWorkOrderVersion: body.ExpectedWorkOrderVersion, ExpectedTaskVersion: body.ExpectedTaskVersion,
		Status: body.Status, Reason: body.Reason, ActorType: "PRINCIPAL", ActorID: initiatingActorID(claims),
		PolicyRevision: claims.PolicyRevision, CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if !validTaskMutationResponse(result.Checklist, claims.ActingOrganizationID, route.siteID, route.workOrderID, body.ExpectedWorkOrderVersion) ||
		!containsTaskState(result.Checklist.Tasks, route.taskID, body.Status, body.ExpectedTaskVersion+1) {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a task status checklist outside the requested mutation.", true)
		return
	}
	if result.Replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, result.Checklist)
}

func (handler *httpHandler) handleTaskReorder(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	idempotencyKey, claims, ok := handler.authorizeTaskMutation(writer, request, route, WorkOrderTaskReorderAction, "reorder", "")
	if !ok {
		return
	}
	var body reorderTasksRequest
	if !decodeStrictJSON(request, &body) || body.ExpectedWorkOrderVersion == 0 || body.TaskIDs == nil {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_TASK_INVALID", "Work Order task invalid", "The reorder request is not a closed bounded JSON object.", false)
		return
	}
	now := handler.now().UTC()
	result, err := handler.taskStore.ReorderTasks(request.Context(), claims.ActingOrganizationID, route.siteID, route.workOrderID, ReorderTasksMutation{
		ExpectedWorkOrderVersion: body.ExpectedWorkOrderVersion, TaskIDs: append([]string(nil), body.TaskIDs...), Reason: body.Reason,
		ActorType: "PRINCIPAL", ActorID: initiatingActorID(claims), PolicyRevision: claims.PolicyRevision,
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if !validTaskMutationResponse(result.Checklist, claims.ActingOrganizationID, route.siteID, route.workOrderID, body.ExpectedWorkOrderVersion) || !sameTaskOrder(result.Checklist.Tasks, body.TaskIDs) {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a task order outside the requested exact permutation.", true)
		return
	}
	if result.Replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, result.Checklist)
}

func (handler *httpHandler) authorizeTaskMutation(writer http.ResponseWriter, request *http.Request, route workOrderRoute, action, operation, taskID string) (string, identitycontext.DelegationClaims, bool) {
	if request.URL.RawQuery != "" {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_TASK_INVALID", "Work Order task invalid", "Work Order task "+operation+" does not accept query parameters.", false)
		return "", identitycontext.DelegationClaims{}, false
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		handler.writeProblem(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", "Idempotency key invalid", "A bounded Idempotency-Key is required for Work Order task mutations.", false)
		return "", identitycontext.DelegationClaims{}, false
	}
	scopes := []string{"site:" + route.siteID, "work-order:" + route.workOrderID}
	if taskID != "" {
		scopes = append(scopes, "task:"+taskID)
	}
	scopes = append(scopes, mutationKeyScope(idempotencyKey))
	claims, ok := handler.authorize(request, action, scopes, true)
	if !ok {
		handler.writeAccessDenied(writer)
		return "", identitycontext.DelegationClaims{}, false
	}
	if handler.taskStore == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return "", identitycontext.DelegationClaims{}, false
	}
	return idempotencyKey, claims, true
}

func validTaskMutationResponse(checklist workordermodel.TaskChecklist, organizationID, siteID, workOrderID string, expectedVersion uint64) bool {
	return checklist.Validate(organizationID, siteID, workOrderID) == nil && checklist.WorkOrderVersion == expectedVersion+1
}

func containsTaskState(tasks []workordermodel.Task, taskID string, status workordermodel.TaskStatus, version uint64) bool {
	for _, task := range tasks {
		if task.TaskID == taskID {
			return task.Status == status && task.Version == version
		}
	}
	return false
}

func sameTaskOrder(tasks []workordermodel.Task, taskIDs []string) bool {
	if len(tasks) != len(taskIDs) {
		return false
	}
	for index, task := range tasks {
		if task.TaskID != strings.TrimSpace(taskIDs[index]) || task.Position != uint64(index) {
			return false
		}
	}
	return true
}
