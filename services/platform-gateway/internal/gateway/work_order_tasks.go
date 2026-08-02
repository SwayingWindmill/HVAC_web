package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

type publicAppendTaskRequest struct {
	ExpectedWorkOrderVersion uint64 `json:"expectedWorkOrderVersion"`
	Title                    string `json:"title"`
	Reason                   string `json:"reason"`
}

type publicTaskStatusRequest struct {
	ExpectedWorkOrderVersion uint64                    `json:"expectedWorkOrderVersion"`
	ExpectedTaskVersion      uint64                    `json:"expectedTaskVersion"`
	Status                   workordermodel.TaskStatus `json:"status"`
	Reason                   string                    `json:"reason"`
}

type publicReorderTasksRequest struct {
	ExpectedWorkOrderVersion uint64   `json:"expectedWorkOrderVersion"`
	TaskIDs                  []string `json:"taskIds"`
	Reason                   string   `json:"reason"`
}

type parsedPublicTaskMutation struct {
	body                     []byte
	idempotencyKey           string
	expectedWorkOrderVersion uint64
	expectedTaskVersion      uint64
	status                   workordermodel.TaskStatus
	taskIDs                  []string
}

func matchPublicWorkOrderTaskRoute(path string) (publicWorkOrderRoute, bool) {
	const prefix = "/api/v1/sites/"
	if !strings.HasPrefix(path, prefix) || strings.HasSuffix(path, "/") {
		return publicWorkOrderRoute{}, false
	}
	segments := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(segments) != 4 && len(segments) != 5 {
		return publicWorkOrderRoute{}, false
	}
	if segments[1] != "work-orders" {
		return publicWorkOrderRoute{}, false
	}
	siteID, siteErr := url.PathUnescape(segments[0])
	workOrderID, workOrderErr := url.PathUnescape(segments[2])
	if siteErr != nil || workOrderErr != nil || !workordermodel.IsUUIDv7(siteID) || !workordermodel.IsUUIDv7(workOrderID) {
		return publicWorkOrderRoute{}, false
	}
	if len(segments) == 4 {
		switch segments[3] {
		case "tasks":
			return publicWorkOrderRoute{
				kind: publicWorkOrderTaskCollection, template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks",
				siteID: siteID, workOrderID: workOrderID, action: workorderauth.ActionTaskList,
			}, true
		case "tasks:reorder":
			return publicWorkOrderRoute{
				kind: publicWorkOrderTaskReorder, template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks:reorder",
				siteID: siteID, workOrderID: workOrderID, action: workorderauth.ActionTaskReorder,
			}, true
		default:
			return publicWorkOrderRoute{}, false
		}
	}
	if segments[3] != "tasks" || !strings.HasSuffix(segments[4], ":status") {
		return publicWorkOrderRoute{}, false
	}
	taskID, err := url.PathUnescape(strings.TrimSuffix(segments[4], ":status"))
	if err != nil || !workordermodel.IsUUIDv7(taskID) || strings.Contains(taskID, ":") {
		return publicWorkOrderRoute{}, false
	}
	return publicWorkOrderRoute{
		kind: publicWorkOrderTaskStatus, template: "/api/v1/sites/{siteId}/work-orders/{workOrderId}/tasks/{taskId}:status",
		siteID: siteID, workOrderID: workOrderID, taskID: taskID, action: workorderauth.ActionTaskStatus,
	}, true
}

func dispatchWorkOrderTaskRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	if h.workOrder == nil || h.workOrder.baseURL == "" || h.workOrder.httpClient == nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("The Work Order task service is not configured."))
		return
	}
	if route.kind == publicWorkOrderTaskCollection && request.Method == http.MethodGet {
		dispatchWorkOrderTaskRead(h, writer, request, route)
		return
	}
	if request.Method != http.MethodPost {
		allow := http.MethodPost
		if route.kind == publicWorkOrderTaskCollection {
			allow = http.MethodGet + ", " + http.MethodPost
		}
		writeMethodNotAllowedFor(writer, request, allow)
		return
	}
	dispatchWorkOrderTaskMutation(h, writer, request, route)
}

func dispatchWorkOrderTaskRead(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	if request.URL.RawQuery != "" {
		h.writeWorkOrderFailure(writer, request, workOrderInvalid("Work Order task list does not accept query parameters."))
		return
	}
	route.action = workorderauth.ActionTaskList
	session, ok := h.workOrderSession(writer, request)
	if !ok {
		return
	}
	decision, failure := h.authorizeWorkOrder(request, session, route, nil, nil)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	readContext, failure := h.signWorkOrderReadContext(session, route, decision)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	body, status, _, failure := h.executeWorkOrderTask(request, route, nil, readContext, "")
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.forwardWorkOrderProblem(writer, request, status, body)
		return
	}
	var checklist workordermodel.TaskChecklist
	if decodeStrictWorkOrderJSON(body, &checklist) != nil || checklist.Validate(session.ActingOrganizationID, route.siteID, route.workOrderID) != nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid task checklist."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, checklist)
}

func dispatchWorkOrderTaskMutation(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	session, ok := h.workOrderMutationSession(writer, request)
	if !ok {
		return
	}
	mutation, failure := parsePublicTaskMutation(request, route)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	switch route.kind {
	case publicWorkOrderTaskCollection:
		route.action = workorderauth.ActionTaskAppend
	case publicWorkOrderTaskStatus:
		route.action = workorderauth.ActionTaskStatus
	case publicWorkOrderTaskReorder:
		route.action = workorderauth.ActionTaskReorder
	}
	decision, failure := h.authorizeWorkOrder(request, session, route, nil, nil)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	writeContext, failure := h.signWorkOrderWriteContext(session, route, decision, mutation.idempotencyKey)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	body, status, replayed, failure := h.executeWorkOrderTask(request, route, mutation.body, writeContext, mutation.idempotencyKey)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	if !validPublicTaskStatus(route, status, replayed) {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid task mutation status."))
		return
	}
	var checklist workordermodel.TaskChecklist
	if decodeStrictWorkOrderJSON(body, &checklist) != nil || checklist.Validate(session.ActingOrganizationID, route.siteID, route.workOrderID) != nil || checklist.WorkOrderVersion != mutation.expectedWorkOrderVersion+1 {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid task mutation checklist."))
		return
	}
	if route.kind == publicWorkOrderTaskStatus && !gatewayChecklistContainsTask(checklist.Tasks, route.taskID, mutation.status, mutation.expectedTaskVersion+1) {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned a task state outside the requested mutation."))
		return
	}
	if route.kind == publicWorkOrderTaskReorder && !gatewayTaskOrderMatches(checklist.Tasks, mutation.taskIDs) {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned a task order outside the requested exact permutation."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(writer, status, checklist)
}

func parsePublicTaskMutation(request *http.Request, route publicWorkOrderRoute) (parsedPublicTaskMutation, *workOrderFailure) {
	if request.URL.RawQuery != "" {
		failure := workOrderInvalid("Work Order task mutations do not accept query parameters.")
		return parsedPublicTaskMutation{}, &failure
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		failure := workOrderInvalid("Work Order task mutations require application/json.")
		return parsedPublicTaskMutation{}, &failure
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !publicWorkOrderIdempotencyKeyPattern.MatchString(idempotencyKey) {
		failure := workOrderInvalid("A bounded Idempotency-Key is required for every Work Order task mutation.")
		return parsedPublicTaskMutation{}, &failure
	}
	body, err := readBoundedBody(request.Body, maximumWorkOrderMutationBodyLength)
	if err != nil || len(body) == 0 {
		failure := workOrderInvalid("The Work Order task mutation body is empty, oversized or unreadable.")
		return parsedPublicTaskMutation{}, &failure
	}
	parsed := parsedPublicTaskMutation{body: body, idempotencyKey: idempotencyKey}
	switch route.kind {
	case publicWorkOrderTaskCollection:
		var input publicAppendTaskRequest
		if decodeStrictWorkOrderJSON(body, &input) != nil || input.ExpectedWorkOrderVersion == 0 || !boundedWorkOrderText(input.Title, 256) || !boundedWorkOrderText(input.Reason, 256) {
			failure := workOrderInvalid("The Work Order task append body is invalid.")
			return parsedPublicTaskMutation{}, &failure
		}
		parsed.expectedWorkOrderVersion = input.ExpectedWorkOrderVersion
	case publicWorkOrderTaskStatus:
		var input publicTaskStatusRequest
		if decodeStrictWorkOrderJSON(body, &input) != nil || input.ExpectedWorkOrderVersion == 0 || input.ExpectedTaskVersion == 0 || !validPublicTaskState(input.Status) || !boundedWorkOrderText(input.Reason, 256) {
			failure := workOrderInvalid("The Work Order task status body is invalid.")
			return parsedPublicTaskMutation{}, &failure
		}
		parsed.expectedWorkOrderVersion = input.ExpectedWorkOrderVersion
		parsed.expectedTaskVersion = input.ExpectedTaskVersion
		parsed.status = input.Status
	case publicWorkOrderTaskReorder:
		var input publicReorderTasksRequest
		if decodeStrictWorkOrderJSON(body, &input) != nil || input.ExpectedWorkOrderVersion == 0 || input.TaskIDs == nil || len(input.TaskIDs) > 512 || !boundedWorkOrderText(input.Reason, 256) {
			failure := workOrderInvalid("The Work Order task reorder body is invalid.")
			return parsedPublicTaskMutation{}, &failure
		}
		seen := make(map[string]struct{}, len(input.TaskIDs))
		for _, rawTaskID := range input.TaskIDs {
			taskID := strings.TrimSpace(rawTaskID)
			if taskID != rawTaskID || !workordermodel.IsUUIDv7(taskID) {
				failure := workOrderInvalid("The Work Order task reorder body contains an invalid task identity.")
				return parsedPublicTaskMutation{}, &failure
			}
			if _, duplicate := seen[taskID]; duplicate {
				failure := workOrderInvalid("The Work Order task reorder body contains a duplicate task identity.")
				return parsedPublicTaskMutation{}, &failure
			}
			seen[taskID] = struct{}{}
		}
		parsed.expectedWorkOrderVersion = input.ExpectedWorkOrderVersion
		parsed.taskIDs = append([]string(nil), input.TaskIDs...)
	default:
		failure := workOrderInvalid("The requested Work Order task mutation is not reviewed.")
		return parsedPublicTaskMutation{}, &failure
	}
	return parsed, nil
}

func (h *handler) executeWorkOrderTask(publicRequest *http.Request, route publicWorkOrderRoute, body []byte, contextToken, idempotencyKey string) ([]byte, int, bool, *workOrderFailure) {
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.workOrder.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders/" + url.PathEscape(route.workOrderID)
	switch route.kind {
	case publicWorkOrderTaskCollection:
		path += "/tasks"
	case publicWorkOrderTaskStatus:
		path += "/tasks/" + url.PathEscape(route.taskID) + ":status"
	case publicWorkOrderTaskReorder:
		path += "/tasks:reorder"
	}
	method := http.MethodGet
	var reader *bytes.Reader
	if body != nil {
		method = http.MethodPost
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	upstream, err := http.NewRequestWithContext(ctx, method, h.workOrder.baseURL+path, reader)
	if err != nil {
		failure := workOrderUnavailable("The Work Order task request could not be constructed.")
		return nil, 0, false, &failure
	}
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	if method == http.MethodGet {
		upstream.Header.Set(workOrderReadContextHeader, contextToken)
	} else {
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Idempotency-Key", idempotencyKey)
		upstream.Header.Set(workOrderWriteContextHeader, contextToken)
	}
	upstream.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), upstream.Header)
	response, err := h.workOrder.httpClient.Do(upstream)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return nil, 0, false, &failure
	}
	defer response.Body.Close()
	responseBody, err := readBoundedBody(response.Body, h.workOrder.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an oversized or unreadable task response.")
		return nil, 0, false, &failure
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := mapWorkOrderMutationProblem(response.StatusCode, responseBody)
		return nil, 0, false, &failure
	}
	replayed := response.Header.Get("Idempotency-Replayed")
	if replayed != "" && replayed != "true" {
		failure := workOrderUnavailable("Work Order Service returned an invalid task replay marker.")
		return nil, 0, false, &failure
	}
	return responseBody, response.StatusCode, replayed == "true", nil
}

func validPublicTaskStatus(route publicWorkOrderRoute, status int, replayed bool) bool {
	if route.kind == publicWorkOrderTaskCollection {
		return (status == http.StatusCreated && !replayed) || (status == http.StatusOK && replayed)
	}
	return status == http.StatusOK
}

func validPublicTaskState(status workordermodel.TaskStatus) bool {
	return status == workordermodel.TaskStatusOpen || status == workordermodel.TaskStatusBlocked || status == workordermodel.TaskStatusCompleted
}

func gatewayChecklistContainsTask(tasks []workordermodel.Task, taskID string, status workordermodel.TaskStatus, version uint64) bool {
	for _, task := range tasks {
		if task.TaskID == taskID {
			return task.Status == status && task.Version == version
		}
	}
	return false
}

func gatewayTaskOrderMatches(tasks []workordermodel.Task, taskIDs []string) bool {
	if len(tasks) != len(taskIDs) {
		return false
	}
	for index, task := range tasks {
		if task.TaskID != taskIDs[index] || task.Position != uint64(index) {
			return false
		}
	}
	return true
}
