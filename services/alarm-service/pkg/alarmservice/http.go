package alarmservice

import (
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalSiteAlarmsPrefix = "/internal/v1/sites/"
	AlarmReadContextHeader   = "X-Alarm-Read-Context"
	AlarmWriteContextHeader  = "X-Alarm-Write-Context"
	AlarmListAction          = "alarm:list"
	AlarmReadAction          = "alarm:read"
	AlarmAcknowledgeAction   = "alarm:acknowledge"
	AlarmAssignAction        = "alarm:assign"
	AlarmUnassignAction      = "alarm:unassign"
	AlarmSuppressAction      = "alarm:suppress"
	AlarmUnsuppressAction    = "alarm:unsuppress"
	AlarmCloseAction         = "alarm:close"
	AlarmReopenAction        = "alarm:reopen"
	DefaultGatewaySPIFFEID   = "spiffe://hvac.local/platform-gateway"
	DefaultAudience          = "alarm-service"
	maximumMutationBody      = 8 * 1024
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

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

type mutationRequest struct {
	ExpectedVersion uint64  `json:"expectedVersion"`
	Reason          string  `json:"reason"`
	AssigneeID      *string `json:"assigneeId,omitempty"`
	SuppressedUntil *string `json:"suppressedUntil,omitempty"`
}

type alarmRoute struct {
	siteID    string
	alarmID   string
	operation alarmmodel.Operation
}

func NewHTTPHandler(config HTTPConfig) (http.Handler, error) {
	if config.Store == nil || config.GatewayPublicKey == nil {
		return nil, errors.New("Alarm Store and Gateway public key are required")
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
			handler.writeProblem(writer, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Caller-supplied identity headers are not accepted by Alarm Service.", false)
			return
		}
	}
	route, ok := matchAlarmPath(request.URL.Path)
	if !ok {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Alarm route does not exist.", false)
		return
	}
	if !alarmmodel.IsUUIDv7(route.siteID) || (route.alarmID != "" && !alarmmodel.IsUUIDv7(route.alarmID)) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm resource is not visible.", false)
		return
	}
	if route.operation != "" {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Alarm lifecycle transitions only support POST.", false)
			return
		}
		handler.mutate(writer, request, route)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Alarm reads only support GET.", false)
		return
	}
	action := AlarmListAction
	scopes := []string{"site:" + route.siteID}
	if route.alarmID != "" {
		action = AlarmReadAction
		scopes = append(scopes, "alarm:"+route.alarmID)
	}
	claims, ok := handler.authorize(request, AlarmReadContextHeader, action, scopes)
	if !ok {
		handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "The requested Alarm resource is outside the authorized read scope.", false)
		return
	}
	if route.alarmID == "" {
		handler.list(writer, request, claims.ActingOrganizationID, route.siteID)
		return
	}
	handler.get(writer, request, claims.ActingOrganizationID, route.siteID, route.alarmID)
}

func (handler *httpHandler) authorize(request *http.Request, header, action string, resourceScopes []string) (identitycontext.DelegationClaims, bool) {
	token := strings.TrimSpace(request.Header.Get(header))
	claims, err := identitycontext.VerifyDelegation(handler.gatewayPublicKey, token)
	if err != nil || !alarmmodel.IsUUIDv7(claims.ActingOrganizationID) || len(claims.Actions) != 1 || claims.Actions[0] != action {
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
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_FILTER_INVALID", "Alarm filter invalid", "The Alarm list filter exceeds the supported read boundary.", false)
		return
	}
	response, err := handler.store.List(request.Context(), organizationID, siteID, filter)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if err := response.Validate(organizationID, siteID, filter.Limit); err != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Alarm Store returned a projection outside the requested scope.", true)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *httpHandler) get(writer http.ResponseWriter, request *http.Request, organizationID, siteID, alarmID string) {
	alarm, err := handler.store.Get(request.Context(), organizationID, siteID, alarmID)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if !validAlarmResponse(alarm, organizationID, siteID, alarmID) {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Alarm Store returned a projection outside the requested scope.", true)
		return
	}
	writeJSON(writer, http.StatusOK, alarm)
}

func (handler *httpHandler) mutate(writer http.ResponseWriter, request *http.Request, route alarmRoute) {
	action, ok := mutationAction(route.operation)
	if !ok {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Alarm route does not exist.", false)
		return
	}
	claims, authorized := handler.authorize(request, AlarmWriteContextHeader, action, []string{"site:" + route.siteID, "alarm:" + route.alarmID})
	if !authorized {
		handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "The requested Alarm resource is outside the authorized lifecycle scope.", false)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		handler.writeProblem(writer, http.StatusUnsupportedMediaType, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm lifecycle request must use application/json.", false)
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !idempotencyKeyPattern.MatchString(idempotencyKey) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "A stable Idempotency-Key is required.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumMutationBody)
	var input mutationRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || input.ExpectedVersion == 0 || strings.TrimSpace(input.Reason) == "" || len(strings.TrimSpace(input.Reason)) > 256 {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm lifecycle request is invalid.", false)
		return
	}
	actorID := strings.TrimSpace(claims.PrincipalID)
	if actorID == "" {
		actorID = strings.TrimSpace(claims.SubjectIssuer) + "#" + strings.TrimSpace(claims.Subject)
	}
	result, err := handler.store.Apply(request.Context(), claims.ActingOrganizationID, route.siteID, route.alarmID, Mutation{
		Operation: route.operation, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason,
		AssigneeID: input.AssigneeID, SuppressedUntil: input.SuppressedUntil,
		ActorType: "PRINCIPAL", ActorID: actorID, PolicyRevision: claims.PolicyRevision,
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey,
		OccurredAt: handler.now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		handler.writeMutationFailure(writer, err)
		return
	}
	if !validAlarmResponse(result.Alarm, claims.ActingOrganizationID, route.siteID, route.alarmID) {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Alarm Store returned a projection outside the requested scope.", true)
		return
	}
	if result.Replayed {
		writer.Header().Set("Idempotent-Replay", "true")
	}
	writeJSON(writer, http.StatusOK, result.Alarm)
}

func mutationAction(operation alarmmodel.Operation) (string, bool) {
	switch operation {
	case alarmmodel.OperationAcknowledge:
		return AlarmAcknowledgeAction, true
	case alarmmodel.OperationAssign:
		return AlarmAssignAction, true
	case alarmmodel.OperationUnassign:
		return AlarmUnassignAction, true
	case alarmmodel.OperationSuppress:
		return AlarmSuppressAction, true
	case alarmmodel.OperationUnsuppress:
		return AlarmUnsuppressAction, true
	case alarmmodel.OperationClose:
		return AlarmCloseAction, true
	case alarmmodel.OperationReopen:
		return AlarmReopenAction, true
	default:
		return "", false
	}
}

func (handler *httpHandler) parseFilter(request *http.Request) (Filter, bool) {
	query := request.URL.Query()
	for key := range query {
		switch key {
		case "status", "severity", "cursor", "limit":
		default:
			return Filter{}, false
		}
	}
	filter := Filter{Status: alarmmodel.Status(query.Get("status")), Severity: alarmmodel.Severity(query.Get("severity")), Cursor: query.Get("cursor"), Limit: 50}
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > handler.maxListLimit {
			return Filter{}, false
		}
		filter.Limit = value
	}
	if filter.Cursor != "" && !alarmmodel.IsUUIDv7(filter.Cursor) {
		return Filter{}, false
	}
	if filter.Status != "" && !contains([]alarmmodel.Status{alarmmodel.StatusOpen, alarmmodel.StatusAcknowledged, alarmmodel.StatusSuppressed, alarmmodel.StatusClosed}, filter.Status) {
		return Filter{}, false
	}
	if filter.Severity != "" && !contains([]alarmmodel.Severity{alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical}, filter.Severity) {
		return Filter{}, false
	}
	return filter, true
}

func (handler *httpHandler) writeStoreFailure(writer http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm resource is not visible.", false)
		return
	}
	handler.writeProblem(writer, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "Alarm Service cannot read its authoritative store.", true)
}

func (handler *httpHandler) writeMutationFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm resource is not visible.", false)
	case errors.Is(err, alarmmodel.ErrVersionConflict):
		handler.writeProblem(writer, http.StatusConflict, "ALARM_VERSION_CONFLICT", "Alarm version conflict", "The Alarm changed before this lifecycle transition was committed.", false)
	case errors.Is(err, ErrIdempotencyConflict):
		handler.writeProblem(writer, http.StatusConflict, "ALARM_IDEMPOTENCY_CONFLICT", "Alarm idempotency conflict", "The Idempotency-Key is already bound to another Alarm lifecycle payload.", false)
	case errors.Is(err, alarmmodel.ErrInvalidOperation), errors.Is(err, alarmmodel.ErrInvalidTransition):
		handler.writeProblem(writer, http.StatusUnprocessableEntity, "ALARM_TRANSITION_INVALID", "Alarm transition invalid", "The requested Alarm lifecycle transition is not valid from the current authoritative state.", false)
	default:
		handler.writeProblem(writer, http.StatusServiceUnavailable, "ALARM_UNAVAILABLE", "Alarm unavailable", "Alarm Service cannot update its authoritative store.", true)
	}
}

func (handler *httpHandler) writeProblem(writer http.ResponseWriter, status int, code, title, detail string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem{Type: "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")), Title: title, Status: status, Detail: detail, Code: code, Retryable: retryable})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func matchAlarmPath(path string) (alarmRoute, bool) {
	if !strings.HasPrefix(path, InternalSiteAlarmsPrefix) {
		return alarmRoute{}, false
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 5 && segments[0] == "internal" && segments[1] == "v1" && segments[2] == "sites" && segments[4] == "alarms" {
		return alarmRoute{siteID: segments[3]}, true
	}
	if len(segments) != 6 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarms" {
		return alarmRoute{}, false
	}
	alarmID, suffix, hasSuffix := strings.Cut(segments[5], ":")
	route := alarmRoute{siteID: segments[3], alarmID: alarmID}
	if !hasSuffix {
		return route, true
	}
	switch suffix {
	case "acknowledge":
		route.operation = alarmmodel.OperationAcknowledge
	case "assign":
		route.operation = alarmmodel.OperationAssign
	case "unassign":
		route.operation = alarmmodel.OperationUnassign
	case "suppress":
		route.operation = alarmmodel.OperationSuppress
	case "unsuppress":
		route.operation = alarmmodel.OperationUnsuppress
	case "close":
		route.operation = alarmmodel.OperationClose
	case "reopen":
		route.operation = alarmmodel.OperationReopen
	default:
		return alarmRoute{}, false
	}
	return route, true
}

func validAlarmResponse(alarm alarmmodel.Alarm, organizationID, siteID, alarmID string) bool {
	return alarm.Validate() == nil && alarm.OrganizationID == organizationID && alarm.SiteID == siteID && alarm.AlarmID == alarmID
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body contains trailing data")
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] != 1 {
			return false
		}
		seen[value]--
	}
	return true
}

func contains[T comparable](values []T, expected T) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
