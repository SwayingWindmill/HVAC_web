package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

type publicWorkOrderLifecycleRequest struct {
	ExpectedVersion    uint64          `json:"expectedVersion"`
	ScheduledStart     json.RawMessage `json:"scheduledStart"`
	DueAt              json.RawMessage `json:"dueAt"`
	CompletionEvidence json.RawMessage `json:"completionEvidence"`
	Reason             string          `json:"reason"`
}

type parsedPublicWorkOrderLifecycle struct {
	body               []byte
	idempotencyKey     string
	expectedVersion    uint64
	scheduledStart     *string
	dueAt              *string
	completionEvidence []workordermodel.EvidenceReference
	reason             string
}

func dispatchWorkOrderLifecycleRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	if h.workOrder == nil || h.workOrder.baseURL == "" || h.workOrder.httpClient == nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("The Work Order lifecycle service is not configured."))
		return
	}
	session, ok := h.workOrderMutationSession(writer, request)
	if !ok {
		return
	}
	mutation, failure := h.parsePublicWorkOrderLifecycle(request, route)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
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
	before, failure := h.executeWorkOrderLifecyclePrecondition(request, route, mutation.idempotencyKey, writeContext)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	if before.OrganizationID != session.ActingOrganizationID {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned a lifecycle precondition projection outside the authenticated Organization."))
		return
	}
	body, status, replayed, failure := h.executeWorkOrderLifecycle(request, route, mutation, writeContext)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid lifecycle status."))
		return
	}
	var workOrder workordermodel.WorkOrder
	if decodeStrictWorkOrderJSON(body, &workOrder) != nil || workOrder.Validate() != nil || workOrder.OrganizationID != session.ActingOrganizationID ||
		workOrder.SiteID != route.siteID || workOrder.WorkOrderID != route.workOrderID ||
		!validPublicLifecycleProjection(workOrder, route, mutation, decision) || (!replayed && !validPublicLifecycleChange(before, workOrder, route, mutation)) {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned a lifecycle projection outside the requested transition."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, workOrder)
}

func (h *handler) parsePublicWorkOrderLifecycle(request *http.Request, route publicWorkOrderRoute) (parsedPublicWorkOrderLifecycle, *workOrderFailure) {
	if request.URL.RawQuery != "" || strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])) != "application/json" {
		failure := workOrderInvalid("Work Order lifecycle actions require application/json without query parameters.")
		return parsedPublicWorkOrderLifecycle{}, &failure
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !publicWorkOrderIdempotencyKeyPattern.MatchString(idempotencyKey) {
		failure := workOrderInvalid("A bounded Idempotency-Key is required for every Work Order lifecycle action.")
		return parsedPublicWorkOrderLifecycle{}, &failure
	}
	body, err := readBoundedBody(request.Body, maximumWorkOrderMutationBodyLength)
	if err != nil || len(body) == 0 {
		failure := workOrderInvalid("The Work Order lifecycle body is empty, oversized or unreadable.")
		return parsedPublicWorkOrderLifecycle{}, &failure
	}
	var input publicWorkOrderLifecycleRequest
	if decodeStrictWorkOrderJSON(body, &input) != nil || input.ExpectedVersion == 0 || !boundedWorkOrderText(input.Reason, 256) {
		failure := workOrderInvalid("The Work Order lifecycle body is not a closed bounded JSON object.")
		return parsedPublicWorkOrderLifecycle{}, &failure
	}
	parsed := parsedPublicWorkOrderLifecycle{body: body, idempotencyKey: idempotencyKey, expectedVersion: input.ExpectedVersion, reason: strings.TrimSpace(input.Reason)}
	now := h.identity.now().UTC()
	if route.operation == workordermodel.OperationSchedule {
		if len(input.ScheduledStart) == 0 || len(input.DueAt) == 0 || len(input.CompletionEvidence) != 0 {
			failure := workOrderInvalid("plan must explicitly provide only scheduledStart, dueAt and reason.")
			return parsedPublicWorkOrderLifecycle{}, &failure
		}
		var valid bool
		parsed.scheduledStart, valid = decodePublicNullableInstant(input.ScheduledStart)
		if !valid {
			failure := workOrderInvalid("scheduledStart must be an RFC3339 instant or null.")
			return parsedPublicWorkOrderLifecycle{}, &failure
		}
		parsed.dueAt, valid = decodePublicNullableInstant(input.DueAt)
		if !valid || (parsed.scheduledStart == nil && parsed.dueAt == nil) || !validPublicSchedule(parsed.scheduledStart, parsed.dueAt, now) {
			failure := workOrderInvalid("The Work Order plan window is invalid.")
			return parsedPublicWorkOrderLifecycle{}, &failure
		}
	} else if len(input.ScheduledStart) != 0 || len(input.DueAt) != 0 {
		failure := workOrderInvalid("Only plan may carry scheduledStart or dueAt.")
		return parsedPublicWorkOrderLifecycle{}, &failure
	}
	if route.operation == workordermodel.OperationComplete {
		if len(input.CompletionEvidence) == 0 || json.Unmarshal(input.CompletionEvidence, &parsed.completionEvidence) != nil || !validPublicCompletionEvidence(parsed.completionEvidence, now) {
			failure := workOrderInvalid("complete requires non-empty bounded completionEvidence.")
			return parsedPublicWorkOrderLifecycle{}, &failure
		}
	} else if len(input.CompletionEvidence) != 0 {
		failure := workOrderInvalid("Only complete may carry completionEvidence.")
		return parsedPublicWorkOrderLifecycle{}, &failure
	}
	return parsed, nil
}

func (h *handler) executeWorkOrderLifecyclePrecondition(publicRequest *http.Request, route publicWorkOrderRoute, idempotencyKey, writeContext string) (workordermodel.WorkOrder, *workOrderFailure) {
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.workOrder.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders/" + url.PathEscape(route.workOrderID) + ":lifecycle-precondition"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.workOrder.baseURL+path, nil)
	if err != nil {
		failure := workOrderUnavailable("The Work Order lifecycle precondition request could not be constructed.")
		return workordermodel.WorkOrder{}, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set(workOrderWriteContextHeader, writeContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := h.workOrder.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service lifecycle precondition is temporarily unavailable.")
		return workordermodel.WorkOrder{}, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, h.workOrder.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an oversized or unreadable precondition response.")
		return workordermodel.WorkOrder{}, &failure
	}
	if response.StatusCode != http.StatusOK {
		failure := mapWorkOrderMutationProblem(response.StatusCode, body)
		return workordermodel.WorkOrder{}, &failure
	}
	var workOrder workordermodel.WorkOrder
	if decodeStrictWorkOrderJSON(body, &workOrder) != nil || workOrder.Validate() != nil || workOrder.OrganizationID == "" || workOrder.SiteID != route.siteID || workOrder.WorkOrderID != route.workOrderID {
		failure := workOrderUnavailable("Work Order Service returned an invalid lifecycle precondition projection.")
		return workordermodel.WorkOrder{}, &failure
	}
	return workOrder, nil
}

func (h *handler) executeWorkOrderLifecycle(publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderLifecycle, writeContext string) ([]byte, int, bool, *workOrderFailure) {
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.workOrder.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders/" + url.PathEscape(route.workOrderID) + lifecycleRouteSuffix(route.operation)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.workOrder.baseURL+path, bytes.NewReader(mutation.body))
	if err != nil {
		failure := workOrderUnavailable("The Work Order lifecycle request could not be constructed.")
		return nil, 0, false, &failure
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set("Idempotency-Key", mutation.idempotencyKey)
	request.Header.Set(workOrderWriteContextHeader, writeContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := h.workOrder.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return nil, 0, false, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, h.workOrder.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an oversized or unreadable response.")
		return nil, 0, false, &failure
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := mapWorkOrderMutationProblem(response.StatusCode, body)
		return nil, 0, false, &failure
	}
	replayed := response.Header.Get("Idempotency-Replayed")
	if replayed != "" && replayed != "true" {
		failure := workOrderUnavailable("Work Order Service returned an invalid idempotency replay marker.")
		return nil, 0, false, &failure
	}
	return body, response.StatusCode, replayed == "true", nil
}

func workOrderMutationKeyScope(idempotencyKey string) string {
	return workorderauth.MutationKeyScope(idempotencyKey)
}

func lifecycleRouteSuffix(operation workordermodel.Operation) string {
	switch operation {
	case workordermodel.OperationSchedule:
		return ":plan"
	case workordermodel.OperationStart:
		return ":start"
	case workordermodel.OperationBlock:
		return ":block"
	case workordermodel.OperationResume:
		return ":resume"
	case workordermodel.OperationComplete:
		return ":complete"
	case workordermodel.OperationCancel:
		return ":cancel"
	case workordermodel.OperationReopen:
		return ":reopen"
	default:
		return ""
	}
}

func decodePublicNullableInstant(raw json.RawMessage) (*string, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) != value {
		return nil, false
	}
	instant, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, false
	}
	normalized := instant.UTC().Format(time.RFC3339Nano)
	return &normalized, true
}

func validPublicSchedule(start, due *string, now time.Time) bool {
	maximum := now.AddDate(1, 0, 0)
	parse := func(value *string) (*time.Time, bool) {
		if value == nil {
			return nil, true
		}
		instant, err := time.Parse(time.RFC3339Nano, *value)
		return &instant, err == nil
	}
	startAt, startOK := parse(start)
	dueAt, dueOK := parse(due)
	if !startOK || !dueOK || (startAt != nil && (startAt.Before(now) || startAt.After(maximum))) || (dueAt != nil && (dueAt.Before(now) || dueAt.After(maximum))) {
		return false
	}
	return startAt == nil || dueAt == nil || !dueAt.Before(*startAt)
}

func validPublicCompletionEvidence(values []workordermodel.EvidenceReference, now time.Time) bool {
	if len(values) == 0 || len(values) > 256 {
		return false
	}
	seen := map[string]struct{}{}
	for index := range values {
		values[index].Kind = strings.TrimSpace(values[index].Kind)
		values[index].Reference = strings.TrimSpace(values[index].Reference)
		instant, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(values[index].CapturedAt))
		if err != nil || instant.After(now) || !boundedWorkOrderText(values[index].Kind, 128) || !boundedWorkOrderText(values[index].Reference, 1024) {
			return false
		}
		values[index].CapturedAt = instant.UTC().Format(time.RFC3339Nano)
		key := values[index].Kind + "\x00" + values[index].Reference
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validPublicLifecycleProjection(workOrder workordermodel.WorkOrder, route publicWorkOrderRoute, mutation parsedPublicWorkOrderLifecycle, decision workorderauth.Decision) bool {
	if workOrder.Version != mutation.expectedVersion+1 || len(workOrder.Timeline) != int(workOrder.Version) {
		return false
	}
	last := workOrder.Timeline[len(workOrder.Timeline)-1]
	if last.Operation != route.operation || last.FromStatus == nil || last.ToStatus != workOrder.Status || last.OccurredAt != workOrder.UpdatedAt ||
		last.Reason != mutation.reason || last.ActorType != "PRINCIPAL" || last.ActorID != decision.PrincipalID ||
		last.PolicyRevision == nil || *last.PolicyRevision != decision.PolicyRevision || last.CorrelationID == nil || *last.CorrelationID != mutation.idempotencyKey ||
		last.AssigneeID != nil || last.TeamID != nil {
		return false
	}
	valid := false
	switch route.operation {
	case workordermodel.OperationSchedule:
		valid = *last.FromStatus == workordermodel.StatusOpen && workOrder.Status == workordermodel.StatusOpen && sameOptionalWorkOrderIdentity(workOrder.ScheduledStart, mutation.scheduledStart) && sameOptionalWorkOrderIdentity(workOrder.DueAt, mutation.dueAt)
	case workordermodel.OperationStart:
		valid = *last.FromStatus == workordermodel.StatusOpen && workOrder.Status == workordermodel.StatusInProgress && (workOrder.AssigneeID != nil || workOrder.TeamID != nil)
	case workordermodel.OperationBlock:
		valid = *last.FromStatus == workordermodel.StatusInProgress && workOrder.Status == workordermodel.StatusBlocked
	case workordermodel.OperationResume:
		valid = *last.FromStatus == workordermodel.StatusBlocked && workOrder.Status == workordermodel.StatusInProgress
	case workordermodel.OperationComplete:
		valid = *last.FromStatus == workordermodel.StatusInProgress && workOrder.Status == workordermodel.StatusCompleted && evidenceSuffixMatches(workOrder.CompletionEvidence, mutation.completionEvidence)
	case workordermodel.OperationCancel:
		valid = (*last.FromStatus == workordermodel.StatusOpen || *last.FromStatus == workordermodel.StatusInProgress || *last.FromStatus == workordermodel.StatusBlocked) && workOrder.Status == workordermodel.StatusCancelled
	case workordermodel.OperationReopen:
		valid = (*last.FromStatus == workordermodel.StatusCompleted || *last.FromStatus == workordermodel.StatusCancelled) && workOrder.Status == workordermodel.StatusOpen
	}
	return valid
}

func validPublicLifecycleChange(before, after workordermodel.WorkOrder, route publicWorkOrderRoute, mutation parsedPublicWorkOrderLifecycle) bool {
	if before.OrganizationID != after.OrganizationID || before.SiteID != after.SiteID || before.WorkOrderID != after.WorkOrderID ||
		before.SchemaVersion != after.SchemaVersion || before.Title != after.Title || before.Description != after.Description || before.Priority != after.Priority ||
		!reflect.DeepEqual(before.SourceReferences, after.SourceReferences) || !reflect.DeepEqual(before.AssigneeID, after.AssigneeID) || !reflect.DeepEqual(before.TeamID, after.TeamID) ||
		before.Tasks != after.Tasks || before.NoteCount != after.NoteCount || before.AttachmentCount != after.AttachmentCount || before.CreatedAt != after.CreatedAt ||
		before.Version != mutation.expectedVersion || after.Version != before.Version+1 || len(after.Timeline) != len(before.Timeline)+1 ||
		!reflect.DeepEqual(before.Timeline, after.Timeline[:len(before.Timeline)]) {
		return false
	}
	if route.operation == workordermodel.OperationSchedule {
		if !sameOptionalWorkOrderIdentity(after.ScheduledStart, mutation.scheduledStart) || !sameOptionalWorkOrderIdentity(after.DueAt, mutation.dueAt) {
			return false
		}
	} else if !reflect.DeepEqual(before.ScheduledStart, after.ScheduledStart) || !reflect.DeepEqual(before.DueAt, after.DueAt) {
		return false
	}
	if route.operation == workordermodel.OperationComplete {
		return len(after.CompletionEvidence) == len(before.CompletionEvidence)+len(mutation.completionEvidence) &&
			reflect.DeepEqual(before.CompletionEvidence, after.CompletionEvidence[:len(before.CompletionEvidence)]) && evidenceSuffixMatches(after.CompletionEvidence, mutation.completionEvidence)
	}
	return reflect.DeepEqual(before.CompletionEvidence, after.CompletionEvidence)
}

func evidenceSuffixMatches(actual, expected []workordermodel.EvidenceReference) bool {
	if len(expected) == 0 || len(actual) < len(expected) {
		return false
	}
	offset := len(actual) - len(expected)
	for index := range expected {
		if actual[offset+index] != expected[index] {
			return false
		}
	}
	return true
}
