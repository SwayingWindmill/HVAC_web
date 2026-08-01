package workorderservice

import (
	"crypto"
	"encoding/json"
	"errors"
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
	WorkOrderListAction          = "work-order:list"
	WorkOrderReadAction          = "work-order:read"
	DefaultGatewaySPIFFEID       = "spiffe://hvac.local/platform-gateway"
	DefaultAudience              = "work-order-service"
)

type HTTPConfig struct {
	Store            Store
	GatewayPublicKey crypto.PublicKey
	Now              func() time.Time
	GatewaySPIFFEID  string
	Audience         string
	MaxListLimit     int
}

type httpHandler struct {
	store            Store
	gatewayPublicKey crypto.PublicKey
	now              func() time.Time
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

type workOrderRoute struct {
	siteID      string
	workOrderID string
}

func NewHTTPHandler(config HTTPConfig) (http.Handler, error) {
	if config.Store == nil || config.GatewayPublicKey == nil {
		return nil, errors.New("Work Order Store and Gateway public key are required")
	}
	if config.Now == nil {
		config.Now = time.Now
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
		store: config.Store, gatewayPublicKey: config.GatewayPublicKey, now: config.Now,
		gatewaySPIFFEID: config.GatewaySPIFFEID, audience: config.Audience, maxListLimit: config.MaxListLimit,
	}, nil
}

func (handler *httpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "private, no-store")
	for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Admin", "X-Delegation-Grant"} {
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
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Work Order reads only support GET.", false)
		return
	}
	action := WorkOrderListAction
	scopes := []string{"site:" + route.siteID}
	if route.workOrderID != "" {
		action = WorkOrderReadAction
		scopes = append(scopes, "work-order:"+route.workOrderID)
	}
	claims, ok := handler.authorize(request, action, scopes)
	if !ok {
		handler.writeProblem(writer, http.StatusForbidden, "WORK_ORDER_ACCESS_DENIED", "Work Order access denied", "The requested Work Order resource is outside the authorized read scope.", false)
		return
	}
	if route.workOrderID == "" {
		handler.list(writer, request, claims.ActingOrganizationID, route.siteID)
		return
	}
	handler.get(writer, request, claims.ActingOrganizationID, route.siteID, route.workOrderID)
}

func (handler *httpHandler) authorize(request *http.Request, action string, resourceScopes []string) (identitycontext.DelegationClaims, bool) {
	token := strings.TrimSpace(request.Header.Get(WorkOrderReadContextHeader))
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

func (handler *httpHandler) writeStoreFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Work Order resource is not visible.", false)
	case errors.Is(err, ErrInvalidCursor):
		handler.writeProblem(writer, http.StatusBadRequest, "WORK_ORDER_CURSOR_INVALID", "Work Order cursor invalid", "The Work Order cursor is invalid for this scope or filter.", false)
	default:
		handler.writeProblem(writer, http.StatusServiceUnavailable, "WORK_ORDER_UNAVAILABLE", "Work Order unavailable", "Work Order Service cannot read its authoritative store.", true)
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
		return workOrderRoute{siteID: segments[0]}, true
	}
	if len(segments) == 3 && segments[0] != "" && segments[1] == "work-orders" && segments[2] != "" {
		return workOrderRoute{siteID: segments[0], workOrderID: segments[2]}, true
	}
	return workOrderRoute{}, false
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
