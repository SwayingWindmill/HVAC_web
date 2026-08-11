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

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	alarmDecisionPath          = "/internal/v1/alarm/decision"
	internalSiteAlarmsPrefix   = "/internal/v1/sites/"
	alarmReadContextHeader     = "X-Alarm-Read-Context"
	defaultAlarmTimeout        = 5 * time.Second
	defaultAlarmResponseLimit  = int64(2 << 20)
	maximumAlarmQueryLength    = 2048
	maximumAlarmDecisionLength = int64(256 << 10)
)

type AlarmConfig struct {
	BackendBaseURL    string
	BackendHTTPClient *http.Client
	BackendAudience   string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type alarmController struct {
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	timeout          time.Duration
	maxResponseBytes int64
}

type publicAlarmRoute struct {
	template string
	siteID   string
	alarmID  string
	action   alarmauth.Action
}

type alarmFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func newAlarmController(config *AlarmConfig) *alarmController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.BackendBaseURL = strings.TrimRight(strings.TrimSpace(resolved.BackendBaseURL), "/")
	if resolved.BackendHTTPClient == nil {
		resolved.BackendHTTPClient = &http.Client{Timeout: defaultAlarmTimeout}
	}
	if resolved.BackendAudience == "" {
		resolved.BackendAudience = "alarm-service"
	}
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultAlarmTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultAlarmResponseLimit
	}
	return &alarmController{
		baseURL: resolved.BackendBaseURL, httpClient: resolved.BackendHTTPClient,
		backendAudience: resolved.BackendAudience, timeout: resolved.Timeout, maxResponseBytes: resolved.MaxResponseBytes,
	}
}

func matchPublicAlarmRoute(path string) (publicAlarmRoute, bool) {
	prefix := "/api/v1/sites/"
	if !strings.HasPrefix(path, prefix) {
		return publicAlarmRoute{}, false
	}
	remainder := strings.TrimPrefix(path, prefix)
	segments := strings.Split(remainder, "/")
	if len(segments) < 2 || len(segments) > 3 || segments[1] != "alarms" {
		return publicAlarmRoute{}, false
	}
	siteID, err := url.PathUnescape(segments[0])
	if err != nil || !alarmmodel.IsUUIDv7(siteID) {
		return publicAlarmRoute{}, false
	}
	route := publicAlarmRoute{
		template: "/api/v1/sites/{siteId}/alarms",
		siteID:   siteID,
		action:   alarmauth.ActionList,
	}
	if len(segments) == 2 {
		return route, true
	}
	alarmID, err := url.PathUnescape(segments[2])
	if err != nil || !alarmmodel.IsUUIDv7(alarmID) {
		return publicAlarmRoute{}, false
	}
	route.template = "/api/v1/sites/{siteId}/alarms/{alarmId}"
	route.alarmID = alarmID
	route.action = alarmauth.ActionRead
	return route, true
}

func dispatchAlarmRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	if h.alarm == nil || h.alarm.baseURL == "" || h.alarm.httpClient == nil {
		h.writeAlarmFailure(writer, request, alarmUnavailable("The Alarm read service is not configured."))
		return
	}
	if len(request.URL.RawQuery) > maximumAlarmQueryLength {
		h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm read filter exceeds the supported boundary."))
		return
	}
	limit, ok := validatePublicAlarmQuery(route, request.URL.Query())
	if !ok {
		h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm read filter is invalid."))
		return
	}
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}
	decision, failure := h.authorizeAlarm(request, session, route)
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	site, err := h.resolveAuthoritativeSiteForDomain(request, session, route.siteID)
	if err != nil {
		h.writeAlarmFailure(writer, request, alarmUnavailable("The authoritative Tenant scope for this Site could not be resolved."))
		return
	}
	readContext, failure := h.signAlarmReadContext(session, route, decision, site.TenantID)
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	body, status, failure := h.executeAlarmRead(request, route, readContext)
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.forwardAlarmProblem(writer, request, status, body)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if route.alarmID == "" {
		var response alarmmodel.ListResponse
		if decodeStrictAlarmJSON(body, &response) != nil || response.Validate(session.ActingOrganizationID, route.siteID, limit) != nil {
			h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an invalid list projection."))
			return
		}
		writeJSON(writer, http.StatusOK, response)
		return
	}
	var alarm alarmmodel.Alarm
	if decodeStrictAlarmJSON(body, &alarm) != nil || alarm.Validate() != nil || alarm.OrganizationID != session.ActingOrganizationID || alarm.SiteID != route.siteID || alarm.AlarmID != route.alarmID {
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an invalid detail projection."))
		return
	}
	writeJSON(writer, http.StatusOK, alarm)
}

func validatePublicAlarmQuery(route publicAlarmRoute, query url.Values) (int, bool) {
	if route.alarmID != "" {
		return 0, len(query) == 0
	}
	for key := range query {
		switch key {
		case "status", "severity", "cursor", "limit":
		default:
			return 0, false
		}
		if len(query[key]) != 1 {
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
	if cursor := query.Get("cursor"); cursor != "" && !alarmmodel.IsUUIDv7(cursor) {
		return 0, false
	}
	if status := alarmmodel.Status(query.Get("status")); status != "" {
		switch status {
		case alarmmodel.StatusOpen, alarmmodel.StatusAcknowledged, alarmmodel.StatusSuppressed, alarmmodel.StatusClosed:
		default:
			return 0, false
		}
	}
	if severity := alarmmodel.Severity(query.Get("severity")); severity != "" {
		switch severity {
		case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
		default:
			return 0, false
		}
	}
	return limit, true
}

func (h *handler) alarmSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	session, ok := routeSessionFromContext(request.Context())
	if ok {
		return session, true
	}
	if h.identity == nil {
		h.writeAlarmFailure(writer, request, alarmUnavailable("Session validation is unavailable."))
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

func (h *handler) authorizeAlarm(request *http.Request, session bffSession, route publicAlarmRoute) (alarmauth.Decision, *alarmFailure) {
	if h.identity == nil || h.identity.config.IAMURL == "" || h.identity.config.IAMHTTPClient == nil || h.identity.config.DelegationSigner == nil {
		failure := alarmUnavailable("Alarm authorization is not configured.")
		return alarmauth.Decision{}, &failure
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
		ActingOrganizationID: session.ActingOrganizationID, Actions: []string{"alarm:authorize"}, Scopes: []string{"session:" + session.ID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := alarmUnavailable("The Alarm authorization request could not be signed.")
		return alarmauth.Decision{}, &failure
	}
	input := alarmauth.DecisionRequest{
		ActingOrganizationID: session.ActingOrganizationID,
		SiteID:               route.siteID,
		AlarmID:              route.alarmID,
		Action:               route.action,
	}
	body, err := json.Marshal(input)
	if err != nil {
		failure := alarmUnavailable("The Alarm authorization request could not be encoded.")
		return alarmauth.Decision{}, &failure
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.alarm.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+alarmDecisionPath, bytes.NewReader(body))
	if err != nil {
		failure := alarmUnavailable("The Alarm authorization request could not be constructed.")
		return alarmauth.Decision{}, &failure
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set("X-Delegation-Grant", delegation)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstream.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(upstream)
	if err != nil {
		failure := alarmUnavailable("IAM Alarm authorization is temporarily unavailable.")
		return alarmauth.Decision{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := alarmUnavailable("IAM did not return a valid Alarm authorization decision.")
		return alarmauth.Decision{}, &failure
	}
	raw, err := readBoundedBody(response.Body, maximumAlarmDecisionLength)
	if err != nil {
		failure := alarmUnavailable("IAM returned an unreadable Alarm authorization decision.")
		return alarmauth.Decision{}, &failure
	}
	var output alarmauth.DecisionResponse
	if decodeStrictAlarmJSON(raw, &output) != nil || output.Decision.Validate() != nil {
		failure := alarmUnavailable("IAM returned an invalid Alarm authorization decision.")
		return alarmauth.Decision{}, &failure
	}
	decision := output.Decision
	if decision.Subject != session.Principal.Subject || decision.SubjectIssuer != session.Principal.Issuer ||
		decision.ActingOrganizationID != session.ActingOrganizationID || decision.SiteID != route.siteID || decision.AlarmID != route.alarmID || decision.Action != route.action {
		failure := alarmUnavailable("IAM returned an Alarm decision outside the authenticated boundary.")
		return alarmauth.Decision{}, &failure
	}
	if !decision.Allowed {
		failure := alarmDenied()
		return alarmauth.Decision{}, &failure
	}
	return decision, nil
}

func (h *handler) signAlarmReadContext(session bffSession, route publicAlarmRoute, decision alarmauth.Decision, tenantID string) (string, *alarmFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.alarm == nil || !isLowerUUIDv7(tenantID) {
		failure := alarmUnavailable("Alarm read context signing is unavailable.")
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	scopes := []string{"organization:" + session.ActingOrganizationID, "site:" + route.siteID}
	if route.alarmID != "" {
		scopes = append(scopes, "alarm:"+route.alarmID)
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		PrincipalID: decision.PrincipalID, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email,
		Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience: h.alarm.backendAudience, ActingOrganizationID: session.ActingOrganizationID, TenantID: tenantID,
		Actions: []string{string(route.action)}, Scopes: scopes, PolicyRevision: decision.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	token, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := alarmUnavailable("The Alarm read context could not be signed.")
		return "", &failure
	}
	return token, nil
}

func (h *handler) executeAlarmRead(publicRequest *http.Request, route publicAlarmRoute, readContext string) ([]byte, int, *alarmFailure) {
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.alarm.timeout)
	defer cancel()
	path := internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarms"
	if route.alarmID != "" {
		path += "/" + url.PathEscape(route.alarmID)
	}
	upstreamURL := h.alarm.baseURL + path
	if route.alarmID == "" && publicRequest.URL.RawQuery != "" {
		upstreamURL += "?" + publicRequest.URL.RawQuery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		failure := alarmUnavailable("The Alarm read request could not be constructed.")
		return nil, 0, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	request.Header.Set(alarmReadContextHeader, readContext)
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := h.alarm.httpClient.Do(request)
	if err != nil {
		failure := alarmUnavailable("Alarm Service is temporarily unavailable.")
		return nil, 0, &failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 599 {
		failure := alarmUnavailable("Alarm Service returned an invalid status.")
		return nil, 0, &failure
	}
	body, err := readBoundedBody(response.Body, h.alarm.maxResponseBytes)
	if err != nil {
		failure := alarmUnavailable("Alarm Service returned an oversized or unreadable response.")
		return nil, 0, &failure
	}
	return body, response.StatusCode, nil
}

func (h *handler) forwardAlarmProblem(writer http.ResponseWriter, request *http.Request, status int, body []byte) {
	var value struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if decodeStrictAlarmJSON(body, &value) != nil || value.Code == "" {
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an invalid error response."))
		return
	}
	switch status {
	case http.StatusBadRequest:
		h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm read request is invalid."))
	case http.StatusForbidden, http.StatusNotFound:
		h.writeAlarmFailure(writer, request, alarmDenied())
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service could not complete the read."))
	default:
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an unsupported response."))
	}
}

func decodeStrictAlarmJSON(body []byte, value any) error {
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

func (h *handler) writeAlarmFailure(writer http.ResponseWriter, request *http.Request, failure alarmFailure) {
	writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
}

func alarmInvalid(detail string) alarmFailure {
	return alarmFailure{status: http.StatusBadRequest, code: "ALARM_REQUEST_INVALID", title: "Alarm request invalid", detail: detail}
}

func alarmDenied() alarmFailure {
	return alarmFailure{status: http.StatusForbidden, code: "ALARM_ACCESS_DENIED", title: "Alarm access denied", detail: "The requested Alarm resource is not available to this Session."}
}

func alarmUnavailable(detail string) alarmFailure {
	return alarmFailure{status: http.StatusServiceUnavailable, code: "ALARM_UNAVAILABLE", title: "Alarm unavailable", detail: detail, retryable: true}
}
