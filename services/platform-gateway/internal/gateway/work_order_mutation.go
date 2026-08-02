package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const maximumWorkOrderMutationBodyLength = int64(32 << 10)

var publicWorkOrderIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

type publicWorkOrderCreateRequest struct {
	Title            string                           `json:"title"`
	Description      string                           `json:"description"`
	Priority         workordermodel.Priority          `json:"priority"`
	SourceReferences []workordermodel.SourceReference `json:"sourceReferences"`
	AssigneeID       *string                          `json:"assigneeId"`
	TeamID           *string                          `json:"teamId"`
	ScheduledStart   *string                          `json:"scheduledStart"`
	DueAt            *string                          `json:"dueAt"`
}

type publicWorkOrderAssignmentRequest struct {
	ExpectedVersion uint64          `json:"expectedVersion"`
	AssigneeID      json.RawMessage `json:"assigneeId"`
	TeamID          json.RawMessage `json:"teamId"`
	Reason          string          `json:"reason"`
}

type parsedPublicWorkOrderMutation struct {
	body             []byte
	idempotencyKey   string
	create           *publicWorkOrderCreateRequest
	expectedCreate   *workordermodel.WorkOrder
	assignment       *publicWorkOrderAssignmentRequest
	assignmentTarget *string
	assignmentTeam   *string
}

type upstreamWorkOrderProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func dispatchWorkOrderRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	if route.kind == 0 {
		if route.workOrderID == "" {
			route.kind = publicWorkOrderCollection
		} else {
			route.kind = publicWorkOrderDetail
		}
	}
	switch route.kind {
	case publicWorkOrderCollection:
		switch request.Method {
		case http.MethodGet:
			route.action = workorderauth.ActionList
			dispatchWorkOrderReadRoute(h, writer, request, route)
		case http.MethodPost:
			route.action = workorderauth.ActionCreate
			dispatchWorkOrderMutationRoute(h, writer, request, route)
		default:
			writeMethodNotAllowedFor(writer, request, http.MethodGet+", "+http.MethodPost)
		}
	case publicWorkOrderDetail:
		if request.Method != http.MethodGet {
			writeMethodNotAllowedFor(writer, request, http.MethodGet)
			return
		}
		route.action = workorderauth.ActionRead
		dispatchWorkOrderReadRoute(h, writer, request, route)
	case publicWorkOrderAssignment:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		route.action = workorderauth.ActionAssign
		dispatchWorkOrderMutationRoute(h, writer, request, route)
	case publicWorkOrderLifecycle:
		if request.Method != http.MethodPost {
			writeMethodNotAllowedFor(writer, request, http.MethodPost)
			return
		}
		dispatchWorkOrderLifecycleRoute(h, writer, request, route)
	case publicWorkOrderTaskCollection, publicWorkOrderTaskStatus, publicWorkOrderTaskReorder:
		dispatchWorkOrderTaskRoute(h, writer, request, route)
	default:
		writeProblem(writer, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested public API route does not exist.", false, nil)
	}
}

func dispatchWorkOrderMutationRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	if h.workOrder == nil || h.workOrder.baseURL == "" || h.workOrder.httpClient == nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("The Work Order mutation service is not configured."))
		return
	}
	session, ok := h.workOrderMutationSession(writer, request)
	if !ok {
		return
	}
	mutation, failure := h.parseWorkOrderMutation(request, session, route)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	var targetAssignee, targetTeam *string
	if mutation.create != nil {
		targetAssignee, targetTeam = mutation.create.AssigneeID, mutation.create.TeamID
	} else {
		targetAssignee, targetTeam = mutation.assignmentTarget, mutation.assignmentTeam
	}
	decision, failure := h.authorizeWorkOrder(request, session, route, targetAssignee, targetTeam)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	writeContext, failure := h.signWorkOrderWriteContext(session, route, decision, mutation.idempotencyKey)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	body, status, replayed, failure := h.executeWorkOrderMutation(request, route, mutation, writeContext)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	if !validWorkOrderMutationStatus(route, status, replayed) {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid mutation status."))
		return
	}
	var workOrder workordermodel.WorkOrder
	if decodeStrictWorkOrderJSON(body, &workOrder) != nil || workOrder.Validate() != nil ||
		workOrder.OrganizationID != session.ActingOrganizationID || workOrder.SiteID != route.siteID {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid mutation projection."))
		return
	}
	if route.kind == publicWorkOrderCollection {
		if mutation.expectedCreate == nil || !matchesExpectedWorkOrderCreate(workOrder, *mutation.expectedCreate) || workOrder.Status != workordermodel.StatusOpen || workOrder.Version != 1 || len(workOrder.Timeline) != 1 || workOrder.Timeline[0].Operation != workordermodel.OperationCreate {
			h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid create projection."))
			return
		}
	} else {
		if mutation.assignment == nil || workOrder.WorkOrderID != route.workOrderID || workOrder.Version != mutation.assignment.ExpectedVersion+1 || len(workOrder.Timeline) != int(workOrder.Version) || !sameOptionalWorkOrderIdentity(workOrder.AssigneeID, mutation.assignmentTarget) || !sameOptionalWorkOrderIdentity(workOrder.TeamID, mutation.assignmentTeam) {
			h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid assignment projection."))
			return
		}
		last := workOrder.Timeline[len(workOrder.Timeline)-1]
		if last.Operation != workordermodel.OperationAssign && last.Operation != workordermodel.OperationUnassign {
			h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an unreviewed lifecycle projection."))
			return
		}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(writer, status, workOrder)
}

func (h *handler) workOrderMutationSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	session, ok := h.workOrderSession(writer, request)
	if !ok {
		return bffSession{}, false
	}
	if h.identity == nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Session validation is unavailable."))
		return bffSession{}, false
	}
	csrf := request.Header.Get("X-CSRF-Token")
	if csrf == "" {
		writeProblem(writer, request, http.StatusForbidden, "CSRF_REQUIRED", "CSRF token required", "A CSRF token is required for this Work Order mutation.", false, nil)
		return bffSession{}, false
	}
	if request.Header.Get("Origin") != h.identity.config.PublicOrigin || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRFToken)) != 1 {
		writeProblem(writer, request, http.StatusForbidden, "CSRF_INVALID", "CSRF token invalid", "The request Origin or CSRF token is invalid.", false, nil)
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) parseWorkOrderMutation(request *http.Request, session bffSession, route publicWorkOrderRoute) (parsedPublicWorkOrderMutation, *workOrderFailure) {
	if request.URL.RawQuery != "" {
		failure := workOrderInvalid("Work Order mutations do not accept query parameters.")
		return parsedPublicWorkOrderMutation{}, &failure
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		failure := workOrderInvalid("Work Order mutations require application/json.")
		return parsedPublicWorkOrderMutation{}, &failure
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !publicWorkOrderIdempotencyKeyPattern.MatchString(idempotencyKey) {
		failure := workOrderInvalid("A bounded Idempotency-Key is required for every Work Order mutation.")
		return parsedPublicWorkOrderMutation{}, &failure
	}
	body, err := readBoundedBody(request.Body, maximumWorkOrderMutationBodyLength)
	if err != nil || len(body) == 0 {
		failure := workOrderInvalid("The Work Order mutation body is empty, oversized or unreadable.")
		return parsedPublicWorkOrderMutation{}, &failure
	}
	parsed := parsedPublicWorkOrderMutation{body: body, idempotencyKey: idempotencyKey}
	switch route.kind {
	case publicWorkOrderCollection:
		var create publicWorkOrderCreateRequest
		if decodeStrictWorkOrderJSON(body, &create) != nil {
			failure := workOrderInvalid("The Work Order create body is not a closed JSON object.")
			return parsedPublicWorkOrderMutation{}, &failure
		}
		expectedCreate, modelErr := workordermodel.Create(workordermodel.CreateInput{
			WorkOrderID: "01930000-ffff-7000-8000-000000000001", OrganizationID: session.ActingOrganizationID, SiteID: route.siteID,
			Title: create.Title, Description: create.Description, Priority: create.Priority, SourceReferences: create.SourceReferences,
			AssigneeID: create.AssigneeID, TeamID: create.TeamID, ScheduledStart: create.ScheduledStart, DueAt: create.DueAt,
			ActorType: "PRINCIPAL", ActorID: "gateway-validation", PolicyRevision: "gateway-validation", CorrelationID: idempotencyKey,
			OccurredAt: h.identity.now().UTC().Format(time.RFC3339Nano),
		})
		if modelErr != nil {
			failure := workOrderInvalid("The Work Order create body violates the authoritative contract.")
			return parsedPublicWorkOrderMutation{}, &failure
		}
		parsed.create = &create
		parsed.expectedCreate = &expectedCreate
	case publicWorkOrderAssignment:
		var assignment publicWorkOrderAssignmentRequest
		if decodeStrictWorkOrderJSON(body, &assignment) != nil || assignment.ExpectedVersion == 0 || len(assignment.AssigneeID) == 0 || len(assignment.TeamID) == 0 || !boundedWorkOrderText(assignment.Reason, 256) {
			failure := workOrderInvalid("The Work Order assignment body is invalid or omits the explicit ownership tuple.")
			return parsedPublicWorkOrderMutation{}, &failure
		}
		assigneeID, valid := decodePublicNullableWorkOrderIdentity(assignment.AssigneeID)
		if !valid {
			failure := workOrderInvalid("assigneeId must be a bounded string or null.")
			return parsedPublicWorkOrderMutation{}, &failure
		}
		teamID, valid := decodePublicNullableWorkOrderIdentity(assignment.TeamID)
		if !valid {
			failure := workOrderInvalid("teamId must be a bounded string or null.")
			return parsedPublicWorkOrderMutation{}, &failure
		}
		parsed.assignment = &assignment
		parsed.assignmentTarget = assigneeID
		parsed.assignmentTeam = teamID
	default:
		failure := workOrderInvalid("The requested Work Order mutation is not reviewed.")
		return parsedPublicWorkOrderMutation{}, &failure
	}
	return parsed, nil
}

func matchesExpectedWorkOrderCreate(actual, expected workordermodel.WorkOrder) bool {
	if actual.Title != expected.Title || actual.Description != expected.Description || actual.Priority != expected.Priority ||
		!sameOptionalWorkOrderIdentity(actual.AssigneeID, expected.AssigneeID) || !sameOptionalWorkOrderIdentity(actual.TeamID, expected.TeamID) ||
		!sameOptionalWorkOrderIdentity(actual.ScheduledStart, expected.ScheduledStart) || !sameOptionalWorkOrderIdentity(actual.DueAt, expected.DueAt) ||
		len(actual.SourceReferences) != len(expected.SourceReferences) {
		return false
	}
	for index := range expected.SourceReferences {
		if actual.SourceReferences[index] != expected.SourceReferences[index] {
			return false
		}
	}
	return true
}

func sameOptionalWorkOrderIdentity(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func decodePublicNullableWorkOrderIdentity(raw json.RawMessage) (*string, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > 256 {
		return nil, false
	}
	return &trimmed, true
}

func boundedWorkOrderText(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= maximum
}

func (h *handler) signWorkOrderWriteContext(session bffSession, route publicWorkOrderRoute, decision workorderauth.Decision, idempotencyKey string) (string, *workOrderFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.workOrder == nil {
		failure := workOrderUnavailable("Work Order write context signing is unavailable.")
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	scopes := []string{"organization:" + session.ActingOrganizationID, "site:" + route.siteID}
	if route.workOrderID != "" {
		scopes = append(scopes, "work-order:"+route.workOrderID)
	}
	if route.taskID != "" {
		scopes = append(scopes, "task:"+route.taskID)
	}
	if idempotencyKey != "" {
		scopes = append(scopes, workOrderMutationKeyScope(idempotencyKey))
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		PrincipalID: decision.PrincipalID, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email,
		Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience: h.workOrder.backendAudience, ActingOrganizationID: session.ActingOrganizationID,
		Actions: []string{string(route.action)}, Scopes: scopes, PolicyRevision: decision.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	token, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := workOrderUnavailable("The Work Order write context could not be signed.")
		return "", &failure
	}
	return token, nil
}

func (h *handler) executeWorkOrderMutation(publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderMutation, writeContext string) ([]byte, int, bool, *workOrderFailure) {
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.workOrder.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders"
	if route.kind == publicWorkOrderAssignment {
		path += "/" + url.PathEscape(route.workOrderID) + ":assign"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.workOrder.baseURL+path, bytes.NewReader(mutation.body))
	if err != nil {
		failure := workOrderUnavailable("The Work Order mutation request could not be constructed.")
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
	replayedHeader := response.Header.Get("Idempotency-Replayed")
	if replayedHeader != "" && replayedHeader != "true" {
		failure := workOrderUnavailable("Work Order Service returned an invalid idempotency replay marker.")
		return nil, 0, false, &failure
	}
	return body, response.StatusCode, replayedHeader == "true", nil
}

func validWorkOrderMutationStatus(route publicWorkOrderRoute, status int, replayed bool) bool {
	switch route.kind {
	case publicWorkOrderCollection:
		return (status == http.StatusCreated && !replayed) || (status == http.StatusOK && replayed)
	case publicWorkOrderAssignment:
		return status == http.StatusOK
	default:
		return false
	}
}

func mapWorkOrderMutationProblem(status int, body []byte) workOrderFailure {
	var upstream upstreamWorkOrderProblem
	if decodeStrictWorkOrderJSON(body, &upstream) != nil || upstream.Status != status || upstream.Code == "" {
		return workOrderUnavailable("Work Order Service returned an invalid mutation error response.")
	}
	switch status {
	case http.StatusBadRequest:
		return workOrderInvalid("The Work Order mutation request is invalid.")
	case http.StatusForbidden, http.StatusNotFound:
		return workOrderDenied()
	case http.StatusConflict:
		switch upstream.Code {
		case "IDEMPOTENCY_CONFLICT":
			return workOrderFailure{status: status, code: upstream.Code, title: "Idempotency conflict", detail: "The Idempotency-Key was already committed with a different Work Order request."}
		case "VERSION_CONFLICT":
			return workOrderFailure{status: status, code: upstream.Code, title: "Version conflict", detail: "The Work Order changed before this mutation could commit."}
		default:
			return workOrderUnavailable("Work Order Service returned an unsupported conflict response.")
		}
	case http.StatusUnprocessableEntity:
		if upstream.Code != "WORK_ORDER_CREATE_INVALID" && upstream.Code != "WORK_ORDER_ASSIGNMENT_INVALID" && upstream.Code != "WORK_ORDER_LIFECYCLE_INVALID" && upstream.Code != "WORK_ORDER_TASK_INVALID" {
			return workOrderUnavailable("Work Order Service returned an unsupported validation response.")
		}
		return workOrderFailure{status: status, code: upstream.Code, title: "Work Order mutation invalid", detail: "The Work Order mutation violates the authoritative contract."}
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return workOrderUnavailable("Work Order Service could not complete the mutation.")
	default:
		return workOrderUnavailable("Work Order Service returned an unsupported mutation response.")
	}
}
