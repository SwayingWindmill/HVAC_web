package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
	"github.com/quanlaihe/hvac-web/modules/workorder/pkg/workorderservice"
)

const (
	workOrderDecisionPath          = "/internal/v1/work-order/decision"
	internalSiteWorkOrdersPrefix   = "/internal/v1/sites/"
	workOrderReadContextHeader     = "X-Work-Order-Read-Context"
	workOrderWriteContextHeader    = "X-Work-Order-Write-Context"
	defaultWorkOrderTimeout        = 5 * time.Second
	defaultWorkOrderResponseLimit  = int64(2 << 20)
	maximumWorkOrderQueryLength    = 2048
	maximumWorkOrderDecisionLength = int64(256 << 10)
)

type publicWorkOrderRouteKind uint8

const (
	publicWorkOrderCollection publicWorkOrderRouteKind = iota + 1
	publicWorkOrderDetail
	publicWorkOrderAssignment
	publicWorkOrderLifecycle
)

type publicWorkOrderRoute struct {
	kind        publicWorkOrderRouteKind
	template    string
	siteID      string
	workOrderID string
	action      workorderauth.Action
	operation   workordermodel.Operation
}

type workOrderFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

type WorkOrderOperations interface {
	ExecuteRead(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, readContext string) ([]byte, int, *workOrderFailure)
	ExecuteMutation(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderMutation, writeContext string) ([]byte, int, bool, *workOrderFailure)
	ExecuteLifecyclePrecondition(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, idempotencyKey, writeContext string) (workordermodel.WorkOrder, *workOrderFailure)
	ExecuteLifecycle(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderLifecycle, writeContext string) ([]byte, int, bool, *workOrderFailure)
}

type WorkOrderConfig struct {
	Operations        WorkOrderOperations
	Store             workorderservice.Store
	BackendBaseURL    string
	BackendHTTPClient *http.Client
	BackendAudience   string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type workOrderController struct {
	operations       WorkOrderOperations
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	timeout          time.Duration
	maxResponseBytes int64
}

type directWorkOrderAdapter struct {
	store workorderservice.Store
}

func newDirectWorkOrderAdapter(store workorderservice.Store) WorkOrderOperations {
	return &directWorkOrderAdapter{store: store}
}

func (a *directWorkOrderAdapter) ExecuteRead(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, _ string) ([]byte, int, *workOrderFailure) {
	tenantID := ""
	if session, ok := routeSessionFromContext(publicRequest.Context()); ok {
		tenantID = session.TenantID
	}
	if route.workOrderID == "" {
		limit := 50
		if raw := publicRequest.URL.Query().Get("limit"); raw != "" {
			if val, err := strconv.Atoi(raw); err == nil && val >= 1 && val <= 100 {
				limit = val
			}
		}
		filter := workorderservice.Filter{
			Limit: limit,
		}
		query := publicRequest.URL.Query()
		if cursor := query.Get("cursor"); cursor != "" {
			filter.Cursor = cursor
		}
		if status := workordermodel.Status(query.Get("status")); status != "" {
			filter.Status = status
		}
		if priority := workordermodel.Priority(query.Get("priority")); priority != "" {
			filter.Priority = priority
		}
		if assigneeID := query.Get("assigneeId"); assigneeID != "" {
			filter.AssigneeID = assigneeID
		}
		resp, err := a.store.List(ctx, tenantID, route.siteID, filter)
		if err != nil {
			if errors.Is(err, workorderservice.ErrInvalidFilter) {
				failure := workOrderInvalid("The Work Order read filter is invalid.")
				return nil, http.StatusBadRequest, &failure
			}
			failure := workOrderUnavailable("Work Order Service could not complete the read.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		bytes, err := json.Marshal(resp)
		if err != nil {
			failure := workOrderUnavailable("Work Order Service returned an invalid list projection.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		return bytes, http.StatusOK, nil
	}

	workOrder, err := a.store.Get(ctx, tenantID, route.siteID, route.workOrderID)
	if err != nil {
		if errors.Is(err, workorderservice.ErrNotFound) {
			failure := workOrderDenied()
			return nil, http.StatusNotFound, &failure
		}
		failure := workOrderUnavailable("Work Order Service could not complete the read.")
		return nil, http.StatusServiceUnavailable, &failure
	}
	bytes, err := json.Marshal(workOrder)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an invalid detail projection.")
		return nil, http.StatusServiceUnavailable, &failure
	}
	return bytes, http.StatusOK, nil
}

func (a *directWorkOrderAdapter) ExecuteMutation(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderMutation, _ string) ([]byte, int, bool, *workOrderFailure) {
	tenantID := ""
	actorID := "principal:operator"
	if session, ok := routeSessionFromContext(publicRequest.Context()); ok {
		tenantID = session.TenantID
		if session.Principal.Subject != "" {
			actorID = session.Principal.Subject
		}
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	if route.kind == publicWorkOrderCollection {
		if mutation.create == nil {
			failure := workOrderInvalid("The Work Order create body is missing.")
			return nil, 0, false, &failure
		}
		workOrderID, err := newWorkOrderUUIDv7(now)
		if err != nil {
			failure := workOrderUnavailable("Work Order Service cannot allocate an authoritative identity.")
			return nil, http.StatusServiceUnavailable, false, &failure
		}
		createMut := workorderservice.CreateMutation{
			WorkOrderID:      workOrderID,
			Title:            mutation.create.Title,
			Description:      mutation.create.Description,
			Priority:         mutation.create.Priority,
			SourceReferences: mutation.create.SourceReferences,
			AssigneeID:       mutation.create.AssigneeID,
			TeamID:           mutation.create.TeamID,
			ScheduledStart:   mutation.create.ScheduledStart,
			DueAt:            mutation.create.DueAt,
			ActorType:        "PRINCIPAL",
			ActorID:          actorID,
			PolicyRevision:   "1",
			CorrelationID:    mutation.idempotencyKey,
			IdempotencyKey:   mutation.idempotencyKey,
			OccurredAt:       nowStr,
		}
		res, err := a.store.Create(ctx, tenantID, route.siteID, createMut)
		if err != nil {
			if errors.Is(err, workorderservice.ErrIdempotencyConflict) {
				failure := workOrderFailure{status: http.StatusConflict, code: "IDEMPOTENCY_CONFLICT", title: "Idempotency conflict", detail: "The Idempotency-Key was already committed with a different Work Order request."}
				return nil, http.StatusConflict, false, &failure
			}
			failure := workOrderUnavailable("Work Order Service could not complete the mutation.")
			return nil, http.StatusServiceUnavailable, false, &failure
		}
		bytes, err := json.Marshal(res.WorkOrder)
		if err != nil {
			failure := workOrderUnavailable("Work Order Service returned an invalid mutation projection.")
			return nil, http.StatusServiceUnavailable, false, &failure
		}
		status := http.StatusCreated
		if res.Replayed {
			status = http.StatusOK
		}
		return bytes, status, res.Replayed, nil
	}

	if route.kind == publicWorkOrderAssignment {
		if mutation.assignment == nil {
			failure := workOrderInvalid("The Work Order assignment body is missing.")
			return nil, 0, false, &failure
		}
		assignMut := workorderservice.AssignmentMutation{
			ExpectedVersion: mutation.assignment.ExpectedVersion,
			AssigneeID:      mutation.assignmentTarget,
			TeamID:          mutation.assignmentTeam,
			Reason:          mutation.assignment.Reason,
			ActorType:       "PRINCIPAL",
			ActorID:         actorID,
			PolicyRevision:  "1",
			CorrelationID:   mutation.idempotencyKey,
			IdempotencyKey:  mutation.idempotencyKey,
			OccurredAt:      nowStr,
		}
		res, err := a.store.Assign(ctx, tenantID, route.siteID, route.workOrderID, assignMut)
		if err != nil {
			if errors.Is(err, workorderservice.ErrNotFound) {
				failure := workOrderDenied()
				return nil, http.StatusNotFound, false, &failure
			}
			if errors.Is(err, workordermodel.ErrVersionConflict) {
				failure := workOrderFailure{status: http.StatusConflict, code: "VERSION_CONFLICT", title: "Version conflict", detail: "The Work Order changed before this mutation could commit."}
				return nil, http.StatusConflict, false, &failure
			}
			if errors.Is(err, workorderservice.ErrIdempotencyConflict) {
				failure := workOrderFailure{status: http.StatusConflict, code: "IDEMPOTENCY_CONFLICT", title: "Idempotency conflict", detail: "The Idempotency-Key was already committed with a different Work Order request."}
				return nil, http.StatusConflict, false, &failure
			}
			if errors.Is(err, workordermodel.ErrInvalidAssignment) {
				failure := workOrderFailure{status: http.StatusUnprocessableEntity, code: "WORK_ORDER_ASSIGNMENT_INVALID", title: "Work Order mutation invalid", detail: "The Work Order mutation violates the authoritative contract."}
				return nil, http.StatusUnprocessableEntity, false, &failure
			}
			failure := workOrderUnavailable("Work Order Service could not complete the mutation.")
			return nil, http.StatusServiceUnavailable, false, &failure
		}
		bytes, err := json.Marshal(res.WorkOrder)
		if err != nil {
			failure := workOrderUnavailable("Work Order Service returned an invalid mutation projection.")
			return nil, http.StatusServiceUnavailable, false, &failure
		}
		return bytes, http.StatusOK, res.Replayed, nil
	}

	failure := workOrderUnavailable("The requested Work Order mutation is not supported.")
	return nil, http.StatusBadRequest, false, &failure
}

func (a *directWorkOrderAdapter) ExecuteLifecyclePrecondition(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, _ string, _ string) (workordermodel.WorkOrder, *workOrderFailure) {
	tenantID := ""
	if session, ok := routeSessionFromContext(publicRequest.Context()); ok {
		tenantID = session.TenantID
	}
	workOrder, err := a.store.Get(ctx, tenantID, route.siteID, route.workOrderID)
	if err != nil {
		if errors.Is(err, workorderservice.ErrNotFound) {
			failure := workOrderDenied()
			return workordermodel.WorkOrder{}, &failure
		}
		failure := workOrderUnavailable("Work Order Service could not complete the read.")
		return workordermodel.WorkOrder{}, &failure
	}
	return workOrder, nil
}

func (a *directWorkOrderAdapter) ExecuteLifecycle(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderLifecycle, _ string) ([]byte, int, bool, *workOrderFailure) {
	tenantID := ""
	actorID := "principal:operator"
	if session, ok := routeSessionFromContext(publicRequest.Context()); ok {
		tenantID = session.TenantID
		if session.Principal.Subject != "" {
			actorID = session.Principal.Subject
		}
	}
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	transMut := workorderservice.LifecycleMutation{
		Operation:          route.operation,
		ExpectedVersion:    mutation.expectedVersion,
		ScheduledStart:     mutation.scheduledStart,
		DueAt:              mutation.dueAt,
		CompletionEvidence: mutation.completionEvidence,
		Reason:             mutation.reason,
		ActorType:          "PRINCIPAL",
		ActorID:            actorID,
		PolicyRevision:     "1",
		CorrelationID:      mutation.idempotencyKey,
		IdempotencyKey:     mutation.idempotencyKey,
		OccurredAt:         nowStr,
	}
	res, err := a.store.Transition(ctx, tenantID, route.siteID, route.workOrderID, transMut)
	if err != nil {
		if errors.Is(err, workorderservice.ErrNotFound) {
			failure := workOrderDenied()
			return nil, http.StatusNotFound, false, &failure
		}
		if errors.Is(err, workordermodel.ErrVersionConflict) {
			failure := workOrderFailure{status: http.StatusConflict, code: "VERSION_CONFLICT", title: "Version conflict", detail: "The Work Order changed before this mutation could commit."}
			return nil, http.StatusConflict, false, &failure
		}
		if errors.Is(err, workorderservice.ErrIdempotencyConflict) {
			failure := workOrderFailure{status: http.StatusConflict, code: "IDEMPOTENCY_CONFLICT", title: "Idempotency conflict", detail: "The Idempotency-Key was already committed with a different Work Order request."}
			return nil, http.StatusConflict, false, &failure
		}
		if errors.Is(err, workordermodel.ErrInvalidLifecycle) {
			failure := workOrderFailure{status: http.StatusUnprocessableEntity, code: "WORK_ORDER_LIFECYCLE_INVALID", title: "Work Order mutation invalid", detail: "The Work Order mutation violates the authoritative contract."}
			return nil, http.StatusUnprocessableEntity, false, &failure
		}
		failure := workOrderUnavailable("Work Order Service could not complete the mutation.")
		return nil, http.StatusServiceUnavailable, false, &failure
	}
	bytes, err := json.Marshal(res.WorkOrder)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an invalid mutation projection.")
		return nil, http.StatusServiceUnavailable, false, &failure
	}
	return bytes, http.StatusOK, res.Replayed, nil
}

type httpWorkOrderAdapter struct {
	baseURL          string
	httpClient       *http.Client
	timeout          time.Duration
	maxResponseBytes int64
}

func newHTTPWorkOrderAdapter(config WorkOrderConfig) WorkOrderOperations {
	return &httpWorkOrderAdapter{
		baseURL:          config.BackendBaseURL,
		httpClient:       config.BackendHTTPClient,
		timeout:          config.Timeout,
		maxResponseBytes: config.MaxResponseBytes,
	}
}

func (a *httpWorkOrderAdapter) ExecuteRead(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, readContext string) ([]byte, int, *workOrderFailure) {
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders"
	if route.workOrderID != "" {
		path += "/" + url.PathEscape(route.workOrderID)
	}
	upstreamURL := a.baseURL + path
	if route.workOrderID == "" && publicRequest.URL.RawQuery != "" {
		upstreamURL += "?" + publicRequest.URL.RawQuery
	}
	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		failure := workOrderUnavailable("The Work Order read request could not be constructed.")
		return nil, 0, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set(workOrderReadContextHeader, readContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := a.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return nil, 0, &failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 599 {
		failure := workOrderUnavailable("Work Order Service returned an invalid status.")
		return nil, 0, &failure
	}
	body, err := readBoundedBody(response.Body, a.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an oversized or unreadable response.")
		return nil, 0, &failure
	}
	return body, response.StatusCode, nil
}

func (a *httpWorkOrderAdapter) ExecuteMutation(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderMutation, writeContext string) ([]byte, int, bool, *workOrderFailure) {
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders"
	if route.kind == publicWorkOrderAssignment {
		path += "/" + url.PathEscape(route.workOrderID) + ":assign"
	}
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, a.baseURL+path, bytes.NewReader(mutation.body))
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
	response, err := a.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return nil, 0, false, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, a.maxResponseBytes)
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

func (a *httpWorkOrderAdapter) ExecuteLifecyclePrecondition(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, idempotencyKey, writeContext string) (workordermodel.WorkOrder, *workOrderFailure) {
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, a.baseURL+internalSiteWorkOrdersPrefix+url.PathEscape(route.siteID)+"/work-orders/"+url.PathEscape(route.workOrderID)+":lifecycle-precondition", nil)
	if err != nil {
		failure := workOrderUnavailable("The Work Order lifecycle precondition request could not be constructed.")
		return workordermodel.WorkOrder{}, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set(workOrderWriteContextHeader, writeContext)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := a.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return workordermodel.WorkOrder{}, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, a.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an unreadable precondition response.")
		return workordermodel.WorkOrder{}, &failure
	}
	if response.StatusCode != http.StatusOK {
		failure := mapWorkOrderMutationProblem(response.StatusCode, body)
		return workordermodel.WorkOrder{}, &failure
	}
	var workOrder workordermodel.WorkOrder
	if decodeStrictWorkOrderJSON(body, &workOrder) != nil || workOrder.Validate() != nil || workOrder.SiteID != route.siteID || workOrder.WorkOrderID != route.workOrderID {
		failure := workOrderUnavailable("Work Order Service returned an invalid precondition projection.")
		return workordermodel.WorkOrder{}, &failure
	}
	return workOrder, nil
}

func (a *httpWorkOrderAdapter) ExecuteLifecycle(ctx context.Context, publicRequest *http.Request, route publicWorkOrderRoute, mutation parsedPublicWorkOrderLifecycle, writeContext string) ([]byte, int, bool, *workOrderFailure) {
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	actionSuffix := strings.TrimPrefix(route.template, "/api/v1/sites/{siteId}/work-orders/{workOrderId}")
	upstreamURL := a.baseURL + internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders/" + url.PathEscape(route.workOrderID) + actionSuffix
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, upstreamURL, bytes.NewReader(mutation.body))
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
	response, err := a.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return nil, 0, false, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, a.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an oversized or unreadable response.")
		return nil, 0, false, &failure
	}
	if response.StatusCode != http.StatusOK {
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

func newWorkOrderController(config *WorkOrderConfig) *workOrderController {
	if config == nil {
		return nil
	}
	resolved := *config
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultWorkOrderTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultWorkOrderResponseLimit
	}
	if resolved.BackendAudience == "" {
		resolved.BackendAudience = "work-order-service"
	}
	var ops WorkOrderOperations
	if resolved.Operations != nil {
		ops = resolved.Operations
	} else if resolved.Store != nil {
		ops = newDirectWorkOrderAdapter(resolved.Store)
	} else if strings.TrimSpace(resolved.BackendBaseURL) != "" {
		resolved.BackendBaseURL = strings.TrimRight(strings.TrimSpace(resolved.BackendBaseURL), "/")
		if resolved.BackendHTTPClient == nil {
			resolved.BackendHTTPClient = &http.Client{Timeout: resolved.Timeout}
		}
		ops = newHTTPWorkOrderAdapter(resolved)
	} else {
		return nil
	}
	return &workOrderController{
		operations:       ops,
		baseURL:          resolved.BackendBaseURL,
		httpClient:       resolved.BackendHTTPClient,
		backendAudience:  resolved.BackendAudience,
		timeout:          resolved.Timeout,
		maxResponseBytes: resolved.MaxResponseBytes,
	}
}

func matchPublicWorkOrderRoute(path string) (publicWorkOrderRoute, bool) {
	prefix := "/api/v1/sites/"
	if !strings.HasPrefix(path, prefix) || strings.HasSuffix(path, "/") {
		return publicWorkOrderRoute{}, false
	}
	remainder := strings.TrimPrefix(path, prefix)
	segments := strings.Split(remainder, "/")
	if len(segments) < 2 || len(segments) > 3 || segments[1] != "work-orders" {
		return publicWorkOrderRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || !workordermodel.IsUUIDv7(siteID) {
		return publicWorkOrderRoute{}, false
	}
	route := publicWorkOrderRoute{
		kind: publicWorkOrderCollection, template: "/api/v1/sites/{siteId}/work-orders",
		siteID: siteID, action: workorderauth.ActionList,
	}
	if len(segments) == 2 {
		return route, true
	}
	resourceSegment, err := url.PathUnescape(segments[2])
	if err != nil || resourceSegment == "" {
		return publicWorkOrderRoute{}, false
	}
	if strings.HasSuffix(resourceSegment, ":assign") {
		workOrderID := strings.TrimSuffix(resourceSegment, ":assign")
		if !workordermodel.IsUUIDv7(workOrderID) || strings.Contains(workOrderID, ":") {
			return publicWorkOrderRoute{}, false
		}
		route.kind = publicWorkOrderAssignment
		route.template = "/api/v1/sites/{siteId}/work-orders/{workOrderId}:assign"
		route.workOrderID = workOrderID
		route.action = workorderauth.ActionAssign
		return route, true
	}
	for suffix, metadata := range map[string]struct {
		action    workorderauth.Action
		operation workordermodel.Operation
	}{
		":plan":     {workorderauth.ActionPlan, workordermodel.OperationSchedule},
		":start":    {workorderauth.ActionStart, workordermodel.OperationStart},
		":block":    {workorderauth.ActionBlock, workordermodel.OperationBlock},
		":resume":   {workorderauth.ActionResume, workordermodel.OperationResume},
		":complete": {workorderauth.ActionComplete, workordermodel.OperationComplete},
		":cancel":   {workorderauth.ActionCancel, workordermodel.OperationCancel},
		":reopen":   {workorderauth.ActionReopen, workordermodel.OperationReopen},
	} {
		if strings.HasSuffix(resourceSegment, suffix) {
			workOrderID := strings.TrimSuffix(resourceSegment, suffix)
			if !workordermodel.IsUUIDv7(workOrderID) || strings.Contains(workOrderID, ":") {
				return publicWorkOrderRoute{}, false
			}
			route.kind = publicWorkOrderLifecycle
			route.template = "/api/v1/sites/{siteId}/work-orders/{workOrderId}" + suffix
			route.workOrderID = workOrderID
			route.action = metadata.action
			route.operation = metadata.operation
			return route, true
		}
	}
	if strings.Contains(resourceSegment, ":") || !workordermodel.IsUUIDv7(resourceSegment) {
		return publicWorkOrderRoute{}, false
	}
	route.kind = publicWorkOrderDetail
	route.template = "/api/v1/sites/{siteId}/work-orders/{workOrderId}"
	route.workOrderID = resourceSegment
	route.action = workorderauth.ActionRead
	return route, true
}

func dispatchWorkOrderReadRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicWorkOrderRoute) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	if h.workOrder == nil || h.workOrder.operations == nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("The Work Order read service is not configured."))
		return
	}
	if len(request.URL.RawQuery) > maximumWorkOrderQueryLength {
		h.writeWorkOrderFailure(writer, request, workOrderInvalid("The Work Order read filter exceeds the supported boundary."))
		return
	}
	limit, ok := validatePublicWorkOrderQuery(route, request.URL.Query())
	if !ok {
		h.writeWorkOrderFailure(writer, request, workOrderInvalid("The Work Order read filter is invalid."))
		return
	}
	session, ok := h.workOrderSession(writer, request)
	if !ok {
		return
	}
	decision, failure := h.authorizeWorkOrder(request, session, route, nil, nil)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	site, err := h.resolveAuthoritativeSiteForDomain(request, session, route.siteID)
	if err != nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("The authoritative Tenant scope for this Site could not be resolved."))
		return
	}
	readContext, failure := h.signWorkOrderReadContext(session, route, decision, site.TenantID)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	body, status, failure := h.executeWorkOrderRead(request, route, readContext)
	if failure != nil {
		h.writeWorkOrderFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.forwardWorkOrderProblem(writer, request, status, body)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if route.workOrderID == "" {
		var response workordermodel.ListResponse
		if decodeStrictWorkOrderJSON(body, &response) != nil || response.Validate(session.TenantID, route.siteID, limit) != nil {
			h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid list projection."))
			return
		}
		for _, workOrder := range response.Items {
			if !workOrderMatchesPublicQuery(workOrder, request.URL.Query()) {
				h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned a projection outside the requested filter."))
				return
			}
		}
		writeJSON(writer, http.StatusOK, response)
		return
	}
	var workOrder workordermodel.WorkOrder
	if decodeStrictWorkOrderJSON(body, &workOrder) != nil || workOrder.Validate() != nil || workOrder.TenantID != session.TenantID || workOrder.SiteID != route.siteID || workOrder.WorkOrderID != route.workOrderID {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid detail projection."))
		return
	}
	writeJSON(writer, http.StatusOK, workOrder)
}

func validatePublicWorkOrderQuery(route publicWorkOrderRoute, query url.Values) (int, bool) {
	if route.workOrderID != "" {
		return 0, len(query) == 0
	}
	for key, values := range query {
		if len(values) != 1 {
			return 0, false
		}
		switch key {
		case "status", "priority", "assigneeId", "cursor", "limit":
		default:
			return 0, false
		}
	}
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return 0, false
		}
		limit = value
	}
	if raw := query.Get("cursor"); raw != "" && (strings.TrimSpace(raw) != raw || len(raw) > 512) {
		return 0, false
	}
	if status := workordermodel.Status(query.Get("status")); status != "" {
		switch status {
		case workordermodel.StatusDraft, workordermodel.StatusOpen, workordermodel.StatusInProgress, workordermodel.StatusBlocked, workordermodel.StatusCompleted, workordermodel.StatusCancelled:
		default:
			return 0, false
		}
	}
	if priority := workordermodel.Priority(query.Get("priority")); priority != "" {
		switch priority {
		case workordermodel.PriorityLow, workordermodel.PriorityMedium, workordermodel.PriorityHigh, workordermodel.PriorityUrgent:
		default:
			return 0, false
		}
	}
	if raw := query.Get("assigneeId"); raw != "" && (strings.TrimSpace(raw) != raw || len(raw) > 256) {
		return 0, false
	}
	return limit, true
}

func workOrderMatchesPublicQuery(workOrder workordermodel.WorkOrder, query url.Values) bool {
	if status := workordermodel.Status(query.Get("status")); status != "" && workOrder.Status != status {
		return false
	}
	if priority := workordermodel.Priority(query.Get("priority")); priority != "" && workOrder.Priority != priority {
		return false
	}
	if assigneeID := query.Get("assigneeId"); assigneeID != "" && (workOrder.AssigneeID == nil || *workOrder.AssigneeID != assigneeID) {
		return false
	}
	return true
}
func (h *handler) workOrderSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	session, ok := routeSessionFromContext(request.Context())
	if ok {
		return session, true
	}
	if h.identity == nil {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Session validation is unavailable."))
		return bffSession{}, false
	}
	var failure *identityFailure
	session, failure = h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) authorizeWorkOrder(request *http.Request, session bffSession, route publicWorkOrderRoute, assigneeID, teamID *string) (workorderauth.Decision, *workOrderFailure) {
	if h.identity == nil || h.identity.config.IAMURL == "" || h.identity.config.IAMHTTPClient == nil || h.identity.config.DelegationSigner == nil {
		failure := workOrderUnavailable("Work Order authorization is not configured.")
		return workorderauth.Decision{}, &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.identity.config.IAMAudience,
		TenantID: session.TenantID, Actions: []string{"work-order:authorize"}, Scopes: []string{"session:" + session.ID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := workOrderUnavailable("The Work Order authorization request could not be signed.")
		return workorderauth.Decision{}, &failure
	}
	input := workorderauth.DecisionRequest{
		TenantID:    session.TenantID,
		SiteID:      route.siteID,
		WorkOrderID: route.workOrderID,
		AssigneeID:  assigneeID,
		TeamID:      teamID,
		Action:      route.action,
	}
	body, err := json.Marshal(input)
	if err != nil {
		failure := workOrderUnavailable("The Work Order authorization request could not be encoded.")
		return workorderauth.Decision{}, &failure
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.workOrder.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+workOrderDecisionPath, bytes.NewReader(body))
	if err != nil {
		failure := workOrderUnavailable("The Work Order authorization request could not be constructed.")
		return workorderauth.Decision{}, &failure
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set("X-Delegation-Grant", delegation)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstream.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(upstream)
	if err != nil {
		failure := workOrderUnavailable("IAM Work Order authorization is temporarily unavailable.")
		return workorderauth.Decision{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := workOrderUnavailable("IAM did not return a valid Work Order authorization decision.")
		return workorderauth.Decision{}, &failure
	}
	raw, err := readBoundedBody(response.Body, maximumWorkOrderDecisionLength)
	if err != nil {
		failure := workOrderUnavailable("IAM returned an unreadable Work Order authorization decision.")
		return workorderauth.Decision{}, &failure
	}
	var output workorderauth.DecisionResponse
	if decodeStrictWorkOrderJSON(raw, &output) != nil || output.Decision.Validate() != nil {
		failure := workOrderUnavailable("IAM returned an invalid Work Order authorization decision.")
		return workorderauth.Decision{}, &failure
	}
	decision := output.Decision
	if decision.Subject != session.Principal.Subject || decision.SubjectIssuer != session.Principal.Issuer ||
		decision.TenantID != session.TenantID || decision.SiteID != route.siteID || decision.WorkOrderID != route.workOrderID ||
		!sameOptionalWorkOrderTarget(decision.AssigneeID, assigneeID) || !sameOptionalWorkOrderTarget(decision.TeamID, teamID) || decision.Action != route.action {
		failure := workOrderUnavailable("IAM returned a Work Order decision outside the authenticated boundary.")
		return workorderauth.Decision{}, &failure
	}
	if !decision.Allowed {
		failure := workOrderDenied()
		return workorderauth.Decision{}, &failure
	}
	return decision, nil
}

func (h *handler) signWorkOrderReadContext(session bffSession, route publicWorkOrderRoute, decision workorderauth.Decision, tenantID string) (string, *workOrderFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.workOrder == nil || !isLowerUUIDv7(tenantID) || session.TenantID != tenantID {
		failure := workOrderUnavailable("Work Order read context signing is unavailable.")
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	scopes := []string{"tenant:" + session.TenantID, "site:" + route.siteID}
	if route.workOrderID != "" {
		scopes = append(scopes, "work-order:"+route.workOrderID)
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		PrincipalID: decision.PrincipalID, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email,
		Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience: h.workOrder.backendAudience, TenantID: tenantID,
		Actions: []string{string(route.action)}, Scopes: scopes, PolicyRevision: decision.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	token, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := workOrderUnavailable("The Work Order read context could not be signed.")
		return "", &failure
	}
	return token, nil
}

func (h *handler) executeWorkOrderRead(publicRequest *http.Request, route publicWorkOrderRoute, readContext string) ([]byte, int, *workOrderFailure) {
	if h.workOrder == nil || h.workOrder.operations == nil {
		failure := workOrderUnavailable("The Work Order read service is not configured.")
		return nil, 0, &failure
	}
	return h.workOrder.operations.ExecuteRead(publicRequest.Context(), publicRequest, route, readContext)
}

func (h *handler) forwardWorkOrderProblem(writer http.ResponseWriter, request *http.Request, status int, body []byte) {
	var value struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if decodeStrictWorkOrderJSON(body, &value) != nil || value.Code == "" {
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an invalid error response."))
		return
	}
	switch status {
	case http.StatusBadRequest:
		h.writeWorkOrderFailure(writer, request, workOrderInvalid("The Work Order read request is invalid."))
	case http.StatusForbidden, http.StatusNotFound:
		h.writeWorkOrderFailure(writer, request, workOrderDenied())
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service could not complete the read."))
	default:
		h.writeWorkOrderFailure(writer, request, workOrderUnavailable("Work Order Service returned an unsupported response."))
	}
}

func decodeStrictWorkOrderJSON(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (h *handler) writeWorkOrderFailure(writer http.ResponseWriter, request *http.Request, failure workOrderFailure) {
	writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
}

func workOrderInvalid(detail string) workOrderFailure {
	return workOrderFailure{status: http.StatusBadRequest, code: "WORK_ORDER_REQUEST_INVALID", title: "Work Order request invalid", detail: detail}
}

func workOrderDenied() workOrderFailure {
	return workOrderFailure{status: http.StatusForbidden, code: "WORK_ORDER_ACCESS_DENIED", title: "Work Order access denied", detail: "The requested Work Order resource is not available to this Session."}
}

func workOrderUnavailable(detail string) workOrderFailure {
	return workOrderFailure{status: http.StatusServiceUnavailable, code: "WORK_ORDER_UNAVAILABLE", title: "Work Order unavailable", detail: detail, retryable: true}
}

func sameOptionalWorkOrderTarget(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func newWorkOrderUUIDv7(now time.Time) (string, error) {
	millis := now.UTC().UnixMilli()
	if millis < 0 || millis > (1<<48)-1 {
		return "", errors.New("UUIDv7 timestamp is out of range")
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80

	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}
