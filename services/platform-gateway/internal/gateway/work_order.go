package gateway

import (
	"bytes"
	"context"
	"encoding/json"
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

type WorkOrderConfig struct {
	BackendBaseURL    string
	BackendHTTPClient *http.Client
	BackendAudience   string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type workOrderController struct {
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	timeout          time.Duration
	maxResponseBytes int64
}

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

func newWorkOrderController(config *WorkOrderConfig) *workOrderController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.BackendBaseURL = strings.TrimRight(strings.TrimSpace(resolved.BackendBaseURL), "/")
	if resolved.BackendHTTPClient == nil {
		resolved.BackendHTTPClient = &http.Client{Timeout: defaultWorkOrderTimeout}
	}
	if resolved.BackendAudience == "" {
		resolved.BackendAudience = "work-order-service"
	}
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultWorkOrderTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultWorkOrderResponseLimit
	}
	return &workOrderController{
		baseURL: resolved.BackendBaseURL, httpClient: resolved.BackendHTTPClient,
		backendAudience: resolved.BackendAudience, timeout: resolved.Timeout, maxResponseBytes: resolved.MaxResponseBytes,
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
	if h.workOrder == nil || h.workOrder.baseURL == "" || h.workOrder.httpClient == nil {
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
		TenantID: session.TenantID,
		SiteID:               route.siteID,
		WorkOrderID:          route.workOrderID,
		AssigneeID:           assigneeID,
		TeamID:               teamID,
		Action:               route.action,
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
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.workOrder.timeout)
	defer cancel()
	path := internalSiteWorkOrdersPrefix + url.PathEscape(route.siteID) + "/work-orders"
	if route.workOrderID != "" {
		path += "/" + url.PathEscape(route.workOrderID)
	}
	upstreamURL := h.workOrder.baseURL + path
	if route.workOrderID == "" && publicRequest.URL.RawQuery != "" {
		upstreamURL += "?" + publicRequest.URL.RawQuery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		failure := workOrderUnavailable("The Work Order read request could not be constructed.")
		return nil, 0, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set(workOrderReadContextHeader, readContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := h.workOrder.httpClient.Do(request)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service is temporarily unavailable.")
		return nil, 0, &failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 599 {
		failure := workOrderUnavailable("Work Order Service returned an invalid status.")
		return nil, 0, &failure
	}
	body, err := readBoundedBody(response.Body, h.workOrder.maxResponseBytes)
	if err != nil {
		failure := workOrderUnavailable("Work Order Service returned an oversized or unreadable response.")
		return nil, 0, &failure
	}
	return body, response.StatusCode, nil
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
