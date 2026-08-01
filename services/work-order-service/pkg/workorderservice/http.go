package workorderservice

import (
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	InternalSiteWorkOrdersPrefix = "/internal/v1/sites/"
	WorkOrderReadContextHeader   = "X-Work-Order-Read-Context"
	WorkOrderWriteContextHeader  = "X-Work-Order-Write-Context"
	WorkOrderListAction          = "work-order:list"
	WorkOrderReadAction          = "work-order:read"
	WorkOrderCreateAction        = "work-order:create"
	WorkOrderAssignAction        = "work-order:assign"
	DefaultGatewaySPIFFEID       = "spiffe://hvac.local/platform-gateway"
	DefaultAudience              = "work-order-service"
	maximumMutationBodyBytes     = 32 * 1024
)

type HTTPConfig struct {
	Store            Store
	GatewayPublicKey crypto.PublicKey
	Now              func() time.Time
	NewWorkOrderID   func(time.Time) (string, error)
	GatewaySPIFFEID  string
	Audience         string
	MaxListLimit     int
}

type httpHandler struct {
	store            Store
	gatewayPublicKey crypto.PublicKey
	now              func() time.Time
	newWorkOrderID   func(time.Time) (string, error)
	gatewaySPIFFEID  string
	audience         string
	maxListLimit     int
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

type workOrderRouteKind uint8

const (
	workOrderCollectionRoute workOrderRouteKind = iota + 1
	workOrderDetailRoute
	workOrderAssignmentRoute
)

type workOrderRoute struct {
	kind        workOrderRouteKind
	siteID      string
	workOrderID string
}

type createWorkOrderRequest struct {
	Title            string                           `json:"title"`
	Description      string                           `json:"description"`
	Priority         workordermodel.Priority          `json:"priority"`
	SourceReferences []workordermodel.SourceReference `json:"sourceReferences"`
	AssigneeID       *string                          `json:"assigneeId"`
	TeamID           *string                          `json:"teamId"`
	ScheduledStart   *string                          `json:"scheduledStart"`
	DueAt            *string                          `json:"dueAt"`
}

type assignWorkOrderRequest struct {
	ExpectedVersion uint64          `json:"expectedVersion"`
	AssigneeID      json.RawMessage `json:"assigneeId"`
	TeamID          json.RawMessage `json:"teamId"`
	Reason          string          `json:"reason"`
}

func NewHTTPHandler(config HTTPConfig) (http.Handler, error) {
	if config.Store == nil || config.GatewayPublicKey == nil {
		return nil, errors.New("Work Order Store and Gateway public key are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewWorkOrderID == nil {
		config.NewWorkOrderID = newUUIDv7
	}
	if strings.TrimSpace(config.GatewaySPIFFEID) == "" {
		config.GatewaySPIFFEID = DefaultGatewaySPIFFEID
	}
	if strings.TrimSpace(config.Audience) == "" {
		config.Audience = DefaultAudience
	}
	if config.MaxListLimit <= 0 || config.MaxListLimit > 100 {
		config.MaxListLimit = 100
	}
	return &httpHandler{
		store: config.Store, gatewayPublicKey: config.GatewayPublicKey, now: config.Now, newWorkOrderID: config.NewWorkOrderID,
		gatewaySPIFFEID: config.GatewaySPIFFEID, audience: config.Audience, maxListLimit: config.MaxListLimit,
	}, nil
}

func (handler *httpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "private, no-store")
	for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Work-Order-ID", "X-Admin", "X-Delegation-Grant"} {
		if request.Header.Get(header) != "" {
			handler.writeProblem(writer, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Caller-supplied identity headers are not accepted by Work Order Service.", false)
			return
		}
	}
	route, ok := matchWorkOrderPath(request.URL.Path)
	if !ok {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Work Order route does not exist.", false)
		return
	}
	if !workordermodel.IsUUIDv7(route.siteID) || (route.workOrderID != "" && !workordermodel.IsUUIDv7(route.workOrderID)) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Work Order resource is not visible.", false)
		return
	}
	switch route.kind {
	case workOrderCollectionRoute:
		switch request.Method {
		case http.MethodGet:
			handler.handleList(writer, request, route)
		case http.MethodPost:
			handler.handleCreate(writer, request, route)
		default:
			writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Work Order collection only supports GET and the reviewed POST create mutation.", false)
		}
	case workOrderDetailRoute:
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Work Order detail only supports GET.", false)
			return
		}
		handler.handleGet(writer, request, route)
	case workOrderAssignmentRoute:
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Work Order assignment only supports POST.", false)
			return
		}
		handler.handleAssign(writer, request, route)
	}
}

func (handler *httpHandler) handleList(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	claims, ok := handler.authorize(request, WorkOrderListAction, []string{"site:" + route.siteID}, false)
	if !ok {
		handler.writeAccessDenied(writer)
		return
	}
	handler.list(writer, request, claims.ActingOrganizationID, route.siteID)
}

func (handler *httpHandler) handleGet(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	if request.URL.RawQuery != "" {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_FILTER_INVALID", "Work Order filter invalid", "Work Order detail does not accept query parameters.", false)
		return
	}
	claims, ok := handler.authorize(request, WorkOrderReadAction, []string{"site:" + route.siteID, "work-order:" + route.workOrderID}, false)
	if !ok {
		handler.writeAccessDenied(writer)
		return
	}
	handler.get(writer, request, claims.ActingOrganizationID, route.siteID, route.workOrderID)
}

func (handler *httpHandler) handleCreate(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	if request.URL.RawQuery != "" {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_CREATE_INVALID", "Work Order create invalid", "Work Order create does not accept query parameters.", false)
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		handler.writeProblem(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", "Idempotency key invalid", "A bounded Idempotency-Key is required for Work Order creation.", false)
		return
	}
	claims, ok := handler.authorize(request, WorkOrderCreateAction, []string{"site:" + route.siteID, mutationKeyScope(idempotencyKey)}, true)
	if !ok {
		handler.writeAccessDenied(writer)
		return
	}
	var body createWorkOrderRequest
	if !decodeStrictJSON(request, &body) {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_CREATE_INVALID", "Work Order create invalid", "The Work Order create request is not a closed bounded JSON object.", false)
		return
	}
	now := handler.now().UTC()
	workOrderID, err := handler.newWorkOrderID(now)
	if err != nil || !workordermodel.IsUUIDv7(workOrderID) {
		handler.writeProblem(writer, http.StatusServiceUnavailable, "WORK_ORDER_UNAVAILABLE", "Work Order unavailable", "Work Order Service cannot allocate an authoritative identity.", true)
		return
	}
	result, err := handler.store.Create(request.Context(), claims.ActingOrganizationID, route.siteID, CreateMutation{
		WorkOrderID: workOrderID, Title: body.Title, Description: body.Description, Priority: body.Priority,
		SourceReferences: body.SourceReferences, AssigneeID: body.AssigneeID, TeamID: body.TeamID,
		ScheduledStart: body.ScheduledStart, DueAt: body.DueAt,
		ActorType: "PRINCIPAL", ActorID: initiatingActorID(claims), PolicyRevision: claims.PolicyRevision,
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if result.WorkOrder.Validate() != nil || result.WorkOrder.OrganizationID != claims.ActingOrganizationID || result.WorkOrder.SiteID != route.siteID {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a create projection outside the requested scope.", true)
		return
	}
	if result.Replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
		writeJSON(writer, http.StatusOK, result.WorkOrder)
		return
	}
	writeJSON(writer, http.StatusCreated, result.WorkOrder)
}

func (handler *httpHandler) handleAssign(writer http.ResponseWriter, request *http.Request, route workOrderRoute) {
	if request.URL.RawQuery != "" {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_ASSIGNMENT_INVALID", "Work Order assignment invalid", "Work Order assignment does not accept query parameters.", false)
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		handler.writeProblem(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", "Idempotency key invalid", "A bounded Idempotency-Key is required for Work Order assignment.", false)
		return
	}
	claims, ok := handler.authorize(request, WorkOrderAssignAction, []string{"site:" + route.siteID, "work-order:" + route.workOrderID, mutationKeyScope(idempotencyKey)}, true)
	if !ok {
		handler.writeAccessDenied(writer)
		return
	}
	var body assignWorkOrderRequest
	if !decodeStrictJSON(request, &body) || len(body.AssigneeID) == 0 || len(body.TeamID) == 0 {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_ASSIGNMENT_INVALID", "Work Order assignment invalid", "The assignment request must explicitly provide assigneeId and teamId, including null when clearing ownership.", false)
		return
	}
	assigneeID, ok := decodeNullableString(body.AssigneeID)
	if !ok {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_ASSIGNMENT_INVALID", "Work Order assignment invalid", "assigneeId must be a bounded string or null.", false)
		return
	}
	teamID, ok := decodeNullableString(body.TeamID)
	if !ok {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_ASSIGNMENT_INVALID", "Work Order assignment invalid", "teamId must be a bounded string or null.", false)
		return
	}
	now := handler.now().UTC()
	result, err := handler.store.Assign(request.Context(), claims.ActingOrganizationID, route.siteID, route.workOrderID, AssignmentMutation{
		ExpectedVersion: body.ExpectedVersion, AssigneeID: assigneeID, TeamID: teamID, Reason: body.Reason,
		ActorType: "PRINCIPAL", ActorID: initiatingActorID(claims), PolicyRevision: claims.PolicyRevision,
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if result.WorkOrder.Validate() != nil || result.WorkOrder.OrganizationID != claims.ActingOrganizationID || result.WorkOrder.SiteID != route.siteID || result.WorkOrder.WorkOrderID != route.workOrderID {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned an assignment projection outside the requested scope.", true)
		return
	}
	if result.Replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, result.WorkOrder)
}

func mutationKeyScope(idempotencyKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(idempotencyKey)))
	return "key:" + hex.EncodeToString(digest[:])
}

func (handler *httpHandler) authorize(request *http.Request, action string, resourceScopes []string, write bool) (identitycontext.DelegationClaims, bool) {
	header := WorkOrderReadContextHeader
	otherHeader := WorkOrderWriteContextHeader
	if write {
		header, otherHeader = WorkOrderWriteContextHeader, WorkOrderReadContextHeader
	}
	if request.Header.Get(otherHeader) != "" {
		return identitycontext.DelegationClaims{}, false
	}
	token := strings.TrimSpace(request.Header.Get(header))
	claims, err := identitycontext.VerifyDelegation(handler.gatewayPublicKey, token)
	if err != nil || !workordermodel.IsUUIDv7(claims.ActingOrganizationID) || len(claims.Actions) != 1 || claims.Actions[0] != action {
		return identitycontext.DelegationClaims{}, false
	}
	expectedScopes := append([]string{"organization:" + claims.ActingOrganizationID}, resourceScopes...)
	if err := identitycontext.ValidateDelegationAnyScope(claims, handler.now().UTC(), handler.gatewaySPIFFEID, handler.audience, action, expectedScopes); err != nil {
		return identitycontext.DelegationClaims{}, false
	}
	if !sameStringSet(claims.Scopes, expectedScopes) {
		return identitycontext.DelegationClaims{}, false
	}
	return claims, true
}

func (handler *httpHandler) list(writer http.ResponseWriter, request *http.Request, organizationID, siteID string) {
	filter, ok := handler.parseFilter(request)
	if !ok {
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_FILTER_INVALID", "Work Order filter invalid", "The Work Order list filter exceeds the supported read boundary.", false)
		return
	}
	response, err := handler.store.List(request.Context(), organizationID, siteID, filter)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if err := response.Validate(organizationID, siteID, filter.Limit); err != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a projection outside the requested scope.", true)
		return
	}
	for _, workOrder := range response.Items {
		if !matchesFilter(workOrder, filter) {
			handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a projection outside the requested filter.", true)
			return
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *httpHandler) get(writer http.ResponseWriter, request *http.Request, organizationID, siteID, workOrderID string) {
	workOrder, err := handler.store.Get(request.Context(), organizationID, siteID, workOrderID)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if workOrder.Validate() != nil || workOrder.OrganizationID != organizationID || workOrder.SiteID != siteID || workOrder.WorkOrderID != workOrderID {
		handler.writeProblem(writer, http.StatusBadGateway, "WORK_ORDER_RESPONSE_INVALID", "Work Order response invalid", "Work Order Store returned a projection outside the requested scope.", true)
		return
	}
	writeJSON(writer, http.StatusOK, workOrder)
}

func (handler *httpHandler) parseFilter(request *http.Request) (Filter, bool) {
	query := request.URL.Query()
	for key, values := range query {
		if len(values) != 1 {
			return Filter{}, false
		}
		switch key {
		case "status", "priority", "assigneeId", "cursor", "limit":
		default:
			return Filter{}, false
		}
	}
	filter := Filter{
		Status: workordermodel.Status(query.Get("status")), Priority: workordermodel.Priority(query.Get("priority")),
		AssigneeID: strings.TrimSpace(query.Get("assigneeId")), Cursor: strings.TrimSpace(query.Get("cursor")), Limit: 50,
	}
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > handler.maxListLimit {
			return Filter{}, false
		}
		filter.Limit = value
	}
	if !validStatusFilter(filter.Status) || !validPriorityFilter(filter.Priority) || len(filter.AssigneeID) > 256 {
		return Filter{}, false
	}
	if raw := query.Get("cursor"); raw != "" && (filter.Cursor == "" || filter.Cursor != raw || len(filter.Cursor) > 512) {
		return Filter{}, false
	}
	return filter, true
}

func decodeStrictJSON(request *http.Request, destination any) bool {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maximumMutationBodyBytes+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	return true
}

func decodeNullableString(raw json.RawMessage) (*string, bool) {
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

func initiatingActorID(claims identitycontext.DelegationClaims) string {
	if principalID := strings.TrimSpace(claims.PrincipalID); principalID != "" {
		return principalID
	}
	return strings.TrimSpace(claims.SubjectIssuer) + "#" + strings.TrimSpace(claims.Subject)
}

func (handler *httpHandler) writeAccessDenied(writer http.ResponseWriter) {
	handler.writeProblem(writer, http.StatusForbidden, "WORK_ORDER_ACCESS_DENIED", "Work Order access denied", "The requested Work Order resource is outside the authorized scope.", false)
}

func (handler *httpHandler) writeStoreFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Work Order resource is not visible.", false)
	case errors.Is(err, ErrInvalidCursor):
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_CURSOR_INVALID", "Work Order cursor invalid", "The Work Order cursor is invalid for this scope or filter.", false)
	case errors.Is(err, ErrInvalidFilter):
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_FILTER_INVALID", "Work Order filter invalid", "The Work Order list filter exceeds the supported read boundary.", false)
	case errors.Is(err, ErrIdempotencyConflict):
		handler.writeProblem(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency conflict", "The Idempotency-Key was already committed with a different Work Order request.", false)
	case errors.Is(err, workordermodel.ErrVersionConflict):
		handler.writeProblem(writer, http.StatusConflict, "VERSION_CONFLICT", "Version conflict", "The Work Order version changed before this assignment could commit.", false)
	case errors.Is(err, workordermodel.ErrInvalidCreate):
		handler.writeProblem(writer, http.StatusUnprocessableEntity, "WORK_ORDER_CREATE_INVALID", "Work Order create invalid", "The create request violates the authoritative Work Order contract.", false)
	case errors.Is(err, workordermodel.ErrInvalidAssignment):
		handler.writeProblem(writer, http.StatusUnprocessableEntity, "WORK_ORDER_ASSIGNMENT_INVALID", "Work Order assignment invalid", "The assignment request violates the authoritative Work Order contract.", false)
	default:
		handler.writeProblem(writer, http.StatusServiceUnavailable, "WORK_ORDER_UNAVAILABLE", "Work Order unavailable", "Work Order Service cannot access its authoritative store.", true)
	}
}

func (handler *httpHandler) writeProblem(writer http.ResponseWriter, status int, code, title, detail string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem{
		Type:  "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title: title, Status: status, Detail: detail, Code: code, Retryable: retryable,
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func matchWorkOrderPath(path string) (workOrderRoute, bool) {
	if !strings.HasPrefix(path, InternalSiteWorkOrdersPrefix) || strings.HasSuffix(path, "/") {
		return workOrderRoute{}, false
	}
	segments := strings.Split(strings.TrimPrefix(path, InternalSiteWorkOrdersPrefix), "/")
	if len(segments) == 2 && segments[0] != "" && segments[1] == "work-orders" {
		return workOrderRoute{kind: workOrderCollectionRoute, siteID: segments[0]}, true
	}
	if len(segments) != 3 || segments[0] == "" || segments[1] != "work-orders" || segments[2] == "" {
		return workOrderRoute{}, false
	}
	if strings.HasSuffix(segments[2], ":assign") {
		workOrderID := strings.TrimSuffix(segments[2], ":assign")
		if workOrderID == "" || strings.Contains(workOrderID, ":") {
			return workOrderRoute{}, false
		}
		return workOrderRoute{kind: workOrderAssignmentRoute, siteID: segments[0], workOrderID: workOrderID}, true
	}
	if strings.Contains(segments[2], ":") {
		return workOrderRoute{}, false
	}
	return workOrderRoute{kind: workOrderDetailRoute, siteID: segments[0], workOrderID: segments[2]}, true
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
