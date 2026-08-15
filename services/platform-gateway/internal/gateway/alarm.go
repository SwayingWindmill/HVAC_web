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
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	alarmDecisionPath          = "/internal/v1/alarm/decision"
	internalSiteAlarmsPrefix   = "/internal/v1/sites/"
	internalAlarmScopePrefix   = "/internal/v1/alarms/"
	alarmReadContextHeader     = "X-Alarm-Read-Context"
	alarmWriteContextHeader    = "X-Alarm-Write-Context"
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

type alarmScope struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
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
	if path == "/api/v1/alarms" {
		return publicAlarmRoute{template: "/api/v1/alarms", action: alarmauth.ActionRead}, true
	}
	prefix := "/api/v1/alarms/"
	if !strings.HasPrefix(path, prefix) {
		return publicAlarmRoute{}, false
	}
	segments := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(segments) < 1 || len(segments) > 2 || segments[0] == "" {
		return publicAlarmRoute{}, false
	}
	alarmID, err := url.PathUnescape(segments[0])
	if err != nil || alarmID == "" {
		return publicAlarmRoute{}, false
	}
	if len(segments) == 1 {
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}", alarmID: alarmID, action: alarmauth.ActionRead,
		}, true
	}
	if segments[1] != "ack" {
		return publicAlarmRoute{}, false
	}
	return publicAlarmRoute{
		template: "/api/v1/alarms/{alarmId}/ack", alarmID: alarmID, action: alarmauth.ActionAck,
	}, true
}

func dispatchAlarmRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	expectedMethod := http.MethodGet
	if route.action == alarmauth.ActionAck {
		expectedMethod = http.MethodPost
	}
	if request.Method != expectedMethod {
		writeMethodNotAllowedFor(writer, request, expectedMethod)
		return
	}
	if h.alarm == nil || h.alarm.baseURL == "" || h.alarm.httpClient == nil {
		h.writeAlarmFailure(writer, request, alarmUnavailable("The Alarm service is not configured."))
		return
	}
	if len(request.URL.RawQuery) > maximumAlarmQueryLength {
		h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm request exceeds the supported query boundary."))
		return
	}
	if route.alarmID != "" && !alarmmodel.IsUUIDv7(route.alarmID) {
		h.writeAlarmFailure(writer, request, alarmInvalid("alarmId must be a UUIDv7 identifier."))
		return
	}
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}
	limit := 0
	if route.alarmID == "" {
		var valid bool
		route.siteID, limit, valid = validatePublicAlarmQuery(request.URL.Query())
		if !valid {
			h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm list query is invalid."))
			return
		}
	} else {
		if len(request.URL.Query()) != 0 {
			h.writeAlarmFailure(writer, request, alarmInvalid("Alarm detail and acknowledgement do not accept query parameters."))
			return
		}
		scope, failure := h.resolveAlarmScope(request, session, route.alarmID)
		if failure != nil {
			h.writeAlarmFailure(writer, request, *failure)
			return
		}
		route.siteID = scope.SiteID
	}
	if failure := h.checkAlarmSiteVisibility(request, session, route.siteID, route.alarmID == ""); failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	decision, failure := h.authorizeAlarm(request, session, route)
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	contextToken, failure := h.signAlarmServiceContext(session, route, decision)
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	body, status, failure := h.executeAlarmOperation(request, route, contextToken)
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.forwardAlarmProblem(writer, request, status, body, route)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if route.alarmID == "" {
		var response alarmmodel.ListResponse
		if decodeStrictAlarmJSON(body, &response) != nil || response.Validate(session.TenantID, route.siteID, limit) != nil {
			h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an invalid list projection."))
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": response.Items,
			"meta": map[string]any{
				"requestId": requestIDFromContext(request.Context()), "limit": limit,
				"nextCursor": response.NextCursor, "hasMore": response.HasMore,
			},
		})
		return
	}
	var alarm alarmmodel.Alarm
	if decodeStrictAlarmJSON(body, &alarm) != nil || alarm.Validate() != nil || alarm.TenantID != session.TenantID || alarm.SiteID != route.siteID || alarm.AlarmID != route.alarmID {
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an invalid Alarm projection."))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": alarm,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func validatePublicAlarmQuery(query url.Values) (string, int, bool) {
	for key := range query {
		switch key {
		case "siteId", "status", "severity", "cursor", "limit":
		default:
			return "", 0, false
		}
		if len(query[key]) != 1 {
			return "", 0, false
		}
	}
	siteID := strings.TrimSpace(query.Get("siteId"))
	if !alarmmodel.IsUUIDv7(siteID) {
		return "", 0, false
	}
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return "", 0, false
		}
		limit = value
	}
	if cursor := query.Get("cursor"); len(cursor) > 4096 {
		return "", 0, false
	}
	if status := alarmmodel.Status(query.Get("status")); status != "" {
		switch status {
		case alarmmodel.StatusOpen, alarmmodel.StatusAcknowledged, alarmmodel.StatusSuppressed, alarmmodel.StatusClosed:
		default:
			return "", 0, false
		}
	}
	if severity := alarmmodel.Severity(query.Get("severity")); severity != "" {
		switch severity {
		case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
		default:
			return "", 0, false
		}
	}
	return siteID, limit, true
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

func (h *handler) resolveAlarmScope(request *http.Request, session bffSession, alarmID string) (alarmScope, *alarmFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.alarm == nil || !alarmmodel.IsUUIDv7(alarmID) {
		failure := alarmUnavailable("Alarm ownership resolution is unavailable.")
		return alarmScope{}, &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.alarm.backendAudience,
		TenantID: session.TenantID, Actions: []string{"alarm:resolve"}, Scopes: []string{"tenant:" + session.TenantID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	token, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := alarmUnavailable("Alarm ownership resolution context could not be signed.")
		return alarmScope{}, &failure
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.alarm.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, h.alarm.baseURL+internalAlarmScopePrefix+url.PathEscape(alarmID)+"/scope", nil)
	if err != nil {
		failure := alarmUnavailable("Alarm ownership resolution request could not be constructed.")
		return alarmScope{}, &failure
	}
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set(alarmReadContextHeader, token)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstream.Header)
	response, err := h.alarm.httpClient.Do(upstream)
	if err != nil {
		failure := alarmUnavailable("Alarm ownership resolution is temporarily unavailable.")
		return alarmScope{}, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, h.alarm.maxResponseBytes)
	if err != nil {
		failure := alarmUnavailable("Alarm ownership resolution returned an unreadable response.")
		return alarmScope{}, &failure
	}
	if response.StatusCode == http.StatusNotFound {
		failure := alarmNotFound()
		return alarmScope{}, &failure
	}
	if response.StatusCode != http.StatusOK {
		failure := alarmUnavailable("Alarm ownership resolution failed.")
		return alarmScope{}, &failure
	}
	var scope alarmScope
	if decodeStrictAlarmJSON(body, &scope) != nil || scope.TenantID != session.TenantID || !alarmmodel.IsUUIDv7(scope.SiteID) {
		failure := alarmUnavailable("Alarm ownership resolution returned an invalid scope.")
		return alarmScope{}, &failure
	}
	return scope, nil
}

func (h *handler) checkAlarmSiteVisibility(request *http.Request, session bffSession, siteID string, list bool) *alarmFailure {
	if !alarmmodel.IsUUIDv7(siteID) || h.registry == nil || h.registry.coreHTTPClient == nil || strings.TrimSpace(h.registry.coreBaseURL) == "" {
		failure := alarmUnavailable("Authoritative Site visibility is unavailable.")
		return &failure
	}
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionSiteRead)
	if failure != nil {
		if failure.status == http.StatusForbidden || failure.status == http.StatusNotFound {
			value := alarmNotFound()
			if list {
				value = alarmSiteNotFound()
			}
			return &value
		}
		value := alarmUnavailable("Authoritative Site authorization is temporarily unavailable.")
		return &value
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; !allowed {
		value := alarmNotFound()
		if list {
			value = alarmSiteNotFound()
		}
		return &value
	}
	publicPath := strings.Replace(platformapi.GetSitePathTemplate, "{siteId}", url.PathEscape(siteID), 1)
	registryRoute, _, matches := matchPublicRegistryRoute(publicPath)
	if !matches {
		value := alarmUnavailable("Authoritative Site route is unavailable.")
		return &value
	}
	outer := routeDecisionFromContext(request.Context())
	decision := ownershipregistry.Decision{
		RouteKey: http.MethodGet + " " + registryRoute.template, PathTemplate: registryRoute.template,
		DeclaredOwner: ownershipregistry.OwnerCore, SelectedOwner: ownershipregistry.OwnerCore,
		RegistryRevision: outer.RegistryRevision, RouteRevision: 1, CompatibilityMode: "native",
	}
	if h.routeManager != nil {
		resolved, err := h.routeManager.Current().Resolve(http.MethodGet, publicPath, session.TenantID)
		if err != nil || resolved.DeclaredOwner != ownershipregistry.OwnerCore || resolved.SelectedOwner != ownershipregistry.OwnerCore || resolved.ReadFallbackOwner != "" || resolved.ShadowOwner != "" {
			value := alarmUnavailable("Authoritative Site route ownership is unavailable.")
			return &value
		}
		decision = resolved
	}
	result := h.executeCoreRegistry(request.Context(), registryRoute, "", authorization.coreGrant, decision)
	if result.status == http.StatusNotFound || result.status == http.StatusForbidden {
		value := alarmNotFound()
		if list {
			value = alarmSiteNotFound()
		}
		return &value
	}
	if result.status != http.StatusOK {
		value := alarmUnavailable("Authoritative Site lookup is temporarily unavailable.")
		return &value
	}
	var site platformapi.Site
	if decodeStrictAlarmJSON(result.body, &site) != nil || validateSite(site) != nil || site.ID != siteID || site.TenantID != session.TenantID {
		value := alarmUnavailable("Authoritative Site lookup returned an invalid projection.")
		return &value
	}
	return nil
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
		TenantID: session.TenantID, Actions: []string{"alarm:authorize"}, Scopes: []string{"session:" + session.ID},
		PolicyRevision: h.identity.config.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	delegation, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := alarmUnavailable("The Alarm authorization request could not be signed.")
		return alarmauth.Decision{}, &failure
	}
	input := alarmauth.DecisionRequest{
		TenantID: session.TenantID,
		SiteID:   route.siteID,
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
		decision.TenantID != session.TenantID || decision.SiteID != route.siteID || decision.AlarmID != route.alarmID || decision.Action != route.action {
		failure := alarmUnavailable("IAM returned an Alarm decision outside the authenticated boundary.")
		return alarmauth.Decision{}, &failure
	}
	if !decision.Allowed {
		failure := alarmDenied()
		return alarmauth.Decision{}, &failure
	}
	return decision, nil
}

func (h *handler) signAlarmServiceContext(session bffSession, route publicAlarmRoute, decision alarmauth.Decision) (string, *alarmFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.alarm == nil || !isLowerUUIDv7(session.TenantID) {
		failure := alarmUnavailable("Alarm service context signing is unavailable.")
		return "", &failure
	}
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	scopes := []string{"tenant:" + session.TenantID, "site:" + route.siteID}
	if route.alarmID != "" {
		scopes = append(scopes, "alarm:"+route.alarmID)
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		PrincipalID: decision.PrincipalID, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email,
		Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience: h.alarm.backendAudience, TenantID: session.TenantID,
		Actions: []string{string(route.action)}, Scopes: scopes, PolicyRevision: decision.PolicyRevision, SessionID: session.ID,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	token, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		failure := alarmUnavailable("The Alarm service context could not be signed.")
		return "", &failure
	}
	return token, nil
}

func (h *handler) executeAlarmOperation(publicRequest *http.Request, route publicAlarmRoute, serviceContext string) ([]byte, int, *alarmFailure) {
	ctx, cancel := context.WithTimeout(publicRequest.Context(), h.alarm.timeout)
	defer cancel()
	path := internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarms"
	method := http.MethodGet
	var requestBody io.Reader
	if route.alarmID != "" {
		path += "/" + url.PathEscape(route.alarmID)
	}
	upstreamURL := h.alarm.baseURL + path
	if route.alarmID == "" {
		query := publicRequest.URL.Query()
		query.Del("siteId")
		if encoded := query.Encode(); encoded != "" {
			upstreamURL += "?" + encoded
		}
	}
	if route.action == alarmauth.ActionAck {
		method = http.MethodPost
		path += ":acknowledge"
		upstreamURL = h.alarm.baseURL + path
		if publicRequest.Body != nil && publicRequest.ContentLength != 0 {
			body, err := readBoundedBody(publicRequest.Body, 16<<10)
			if err != nil {
				failure := alarmInvalid("The Alarm acknowledgement body is too large or unreadable.")
				return nil, 0, &failure
			}
			requestBody = bytes.NewReader(body)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, upstreamURL, requestBody)
	if err != nil {
		failure := alarmUnavailable("The Alarm service request could not be constructed.")
		return nil, 0, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	if route.action == alarmauth.ActionAck {
		request.Header.Set(alarmWriteContextHeader, serviceContext)
		if publicRequest.ContentLength != 0 {
			request.Header.Set("Content-Type", publicRequest.Header.Get("Content-Type"))
		}
		if key := strings.TrimSpace(publicRequest.Header.Get("Idempotency-Key")); key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
	} else {
		request.Header.Set(alarmReadContextHeader, serviceContext)
	}
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

func (h *handler) forwardAlarmProblem(writer http.ResponseWriter, request *http.Request, status int, body []byte, route publicAlarmRoute) {
	var value struct {
		Type      string `json:"type"`
		Title     string `json:"title"`
		Status    int    `json:"status"`
		Detail    string `json:"detail"`
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if decodeStrictAlarmJSON(body, &value) != nil || value.Code == "" {
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an invalid error response."))
		return
	}
	switch {
	case status == http.StatusNotFound:
		h.writeAlarmFailure(writer, request, alarmNotFound())
	case status == http.StatusForbidden:
		h.writeAlarmFailure(writer, request, alarmDenied())
	case status == http.StatusConflict && value.Code == "ALARM_IDEMPOTENCY_CONFLICT":
		h.writeAlarmFailure(writer, request, alarmIdempotencyConflict())
	case status == http.StatusBadRequest && value.Code == "INVALID_CURSOR":
		h.writeAlarmFailure(writer, request, alarmInvalidCursor())
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity || status == http.StatusUnsupportedMediaType:
		detail := "The Alarm request is invalid."
		if route.action == alarmauth.ActionAck {
			detail = "The Alarm acknowledgement request is invalid."
		}
		h.writeAlarmFailure(writer, request, alarmInvalid(detail))
	case status == http.StatusServiceUnavailable || status == http.StatusBadGateway || status == http.StatusGatewayTimeout:
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service could not complete the request."))
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
	writeV212Error(writer, request, failure.status, failure.code, failure.detail, map[string]any{"retryable": failure.retryable})
}

func alarmInvalid(detail string) alarmFailure {
	return alarmFailure{status: http.StatusBadRequest, code: "INVALID_ARGUMENT", title: "Invalid argument", detail: detail}
}

func alarmInvalidCursor() alarmFailure {
	return alarmFailure{status: http.StatusBadRequest, code: "INVALID_CURSOR", title: "Invalid cursor", detail: "The Alarm cursor is invalid for the current filters."}
}

func alarmNotFound() alarmFailure {
	return alarmFailure{status: http.StatusNotFound, code: "ALARM_NOT_FOUND", title: "Alarm not found", detail: "Alarm not found."}
}

func alarmSiteNotFound() alarmFailure {
	return alarmFailure{status: http.StatusNotFound, code: "SITE_NOT_FOUND", title: "Site not found", detail: "Site not found."}
}

func alarmDenied() alarmFailure {
	return alarmFailure{status: http.StatusForbidden, code: "FORBIDDEN", title: "Forbidden", detail: "The caller lacks the required Alarm permission for this visible Site."}
}

func alarmIdempotencyConflict() alarmFailure {
	return alarmFailure{status: http.StatusConflict, code: "IDEMPOTENCY_CONFLICT", title: "Idempotency conflict", detail: "The supplied Idempotency-Key is already bound to a different Alarm acknowledgement request."}
}

func alarmUnavailable(detail string) alarmFailure {
	return alarmFailure{status: http.StatusServiceUnavailable, code: "INTERNAL_ERROR", title: "Internal error", detail: detail, retryable: true}
}
