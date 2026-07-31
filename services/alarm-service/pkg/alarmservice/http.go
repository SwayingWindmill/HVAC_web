package alarmservice

import (
	"crypto"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalSiteAlarmsPrefix = "/internal/v1/sites/"
	AlarmReadContextHeader   = "X-Alarm-Read-Context"
	AlarmListAction          = "alarm:list"
	AlarmReadAction          = "alarm:read"
	DefaultGatewaySPIFFEID   = "spiffe://hvac.local/platform-gateway"
	DefaultAudience          = "alarm-service"
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
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Alarm reads only support GET.", false)
		return
	}
	for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Admin", "X-Delegation-Grant"} {
		if request.Header.Get(header) != "" {
			handler.writeProblem(writer, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Caller-supplied identity headers are not accepted by Alarm Service.", false)
			return
		}
	}
	siteID, alarmID, ok := matchAlarmPath(request.URL.Path)
	if !ok {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Alarm route does not exist.", false)
		return
	}
	if !alarmmodel.IsUUIDv7(siteID) || (alarmID != "" && !alarmmodel.IsUUIDv7(alarmID)) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm resource is not visible.", false)
		return
	}
	action := AlarmListAction
	scopes := []string{"site:" + siteID}
	if alarmID != "" {
		action = AlarmReadAction
		scopes = append(scopes, "alarm:"+alarmID)
	}
	claims, ok := handler.authorize(request, action, scopes)
	if !ok {
		handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "The requested Alarm resource is outside the authorized read scope.", false)
		return
	}
	if alarmID == "" {
		handler.list(writer, request, claims.ActingOrganizationID, siteID)
		return
	}
	handler.get(writer, request, claims.ActingOrganizationID, siteID, alarmID)
}

func (handler *httpHandler) authorize(request *http.Request, action string, resourceScopes []string) (identitycontext.DelegationClaims, bool) {
	token := strings.TrimSpace(request.Header.Get(AlarmReadContextHeader))
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
	if err := alarm.Validate(); err != nil || alarm.OrganizationID != organizationID || alarm.SiteID != siteID || alarm.AlarmID != alarmID {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Alarm Store returned a projection outside the requested scope.", true)
		return
	}
	writeJSON(writer, http.StatusOK, alarm)
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

func matchAlarmPath(path string) (string, string, bool) {
	if !strings.HasPrefix(path, InternalSiteAlarmsPrefix) {
		return "", "", false
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 5 && segments[0] == "internal" && segments[1] == "v1" && segments[2] == "sites" && segments[4] == "alarms" {
		return segments[3], "", true
	}
	if len(segments) == 6 && segments[0] == "internal" && segments[1] == "v1" && segments[2] == "sites" && segments[4] == "alarms" {
		return segments[3], segments[5], true
	}
	return "", "", false
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
