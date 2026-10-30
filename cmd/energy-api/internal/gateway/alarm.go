package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
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

type publicAlarmRoute struct {
	template string
	siteID   string
	alarmID  string
	action   alarmauth.Action
	// siteRules is the Site's rules in effect (#693), read with the same grant as its alarm list.
	siteRules bool
}

type alarmScope struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
}

type AlarmOperations interface {
	ResolveScope(ctx context.Context, tenantID, alarmID string, serviceContext string) (alarmScope, *ownerFailure)
	Execute(ctx context.Context, publicRequest *http.Request, route publicAlarmRoute, serviceContext string) ([]byte, int, *ownerFailure)
}

type AlarmConfig struct {
	Operations        AlarmOperations
	Store             alarmservice.Store
	BackendBaseURL    string
	BackendHTTPClient *http.Client
	BackendAudience   string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type alarmController struct {
	operations       AlarmOperations
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	timeout          time.Duration
	maxResponseBytes int64
}

type directAlarmAdapter struct {
	store alarmservice.Store
}

func newDirectAlarmAdapter(store alarmservice.Store) AlarmOperations {
	return &directAlarmAdapter{store: store}
}

func (a *directAlarmAdapter) ResolveScope(ctx context.Context, tenantID, alarmID string, _ string) (alarmScope, *ownerFailure) {
	scope, err := a.store.ResolveScope(ctx, tenantID, alarmID)
	if err != nil {
		if errors.Is(err, alarmservice.ErrNotFound) {
			failure := alarmNotFound()
			return alarmScope{}, &failure
		}
		failure := alarmUnavailable("Alarm ownership resolution failed.")
		return alarmScope{}, &failure
	}
	if scope.TenantID != tenantID || !alarmmodel.IsUUIDv7(scope.SiteID) {
		failure := alarmUnavailable("Alarm ownership resolution returned an invalid scope.")
		return alarmScope{}, &failure
	}
	return alarmScope{TenantID: scope.TenantID, SiteID: scope.SiteID}, nil
}

func (a *directAlarmAdapter) Execute(ctx context.Context, publicRequest *http.Request, route publicAlarmRoute, _ string) ([]byte, int, *ownerFailure) {
	if route.siteRules {
		failure := alarmUnavailable("Site rules are only served by the Alarm Service.")
		return nil, http.StatusServiceUnavailable, &failure
	}
	query := publicRequest.URL.Query()
	tenantID := ""
	if session, ok := routeSessionFromContext(publicRequest.Context()); ok {
		tenantID = session.TenantID
	}
	if route.alarmID == "" {
		limit := 50
		if raw := query.Get("limit"); raw != "" {
			if val, err := strconv.Atoi(raw); err == nil && val >= 1 && val <= 200 {
				limit = val
			}
		}
		filter := alarmservice.Filter{
			Limit: limit,
		}
		if cursor := query.Get("cursor"); cursor != "" {
			filter.Cursor = cursor
		}
		if condition := alarmmodel.Condition(query.Get("condition")); condition != "" {
			filter.Condition = condition
		}
		if severity := alarmmodel.Severity(query.Get("severity")); severity != "" {
			filter.Severity = severity
		}
		if raw := query.Get("acknowledged"); raw != "" {
			if val, err := strconv.ParseBool(raw); err == nil {
				filter.Acknowledged = &val
			}
		}
		if raw := query.Get("suppressed"); raw != "" {
			if val, err := strconv.ParseBool(raw); err == nil {
				filter.Suppressed = &val
			}
		}
		resp, err := a.store.List(ctx, tenantID, route.siteID, filter)
		if err != nil {
			if errors.Is(err, alarmservice.ErrInvalidCursor) {
				failure := alarmInvalidCursor()
				return nil, http.StatusBadRequest, &failure
			}
			failure := alarmUnavailable("Alarm Service could not complete the request.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		bytes, err := json.Marshal(resp)
		if err != nil {
			failure := alarmUnavailable("Alarm Service returned an invalid list projection.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		return bytes, http.StatusOK, nil
	}

	if route.action == alarmauth.ActionRead {
		alarm, err := a.store.Get(ctx, tenantID, route.siteID, route.alarmID)
		if err != nil {
			if errors.Is(err, alarmservice.ErrNotFound) {
				failure := alarmNotFound()
				return nil, http.StatusNotFound, &failure
			}
			failure := alarmUnavailable("Alarm Service could not complete the request.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		bytes, err := json.Marshal(alarm)
		if err != nil {
			failure := alarmUnavailable("Alarm Service returned an invalid Alarm projection.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		return bytes, http.StatusOK, nil
	}

	if route.action == alarmauth.ActionAck {
		var payload struct {
			Reason          string  `json:"reason"`
			AssigneeID      *string `json:"assigneeId"`
			SuppressedUntil *string `json:"suppressedUntil"`
			ExpectedVersion uint64  `json:"expectedVersion"`
		}
		if publicRequest.Body != nil && publicRequest.ContentLength != 0 {
			body, err := readBoundedBody(publicRequest.Body, 16<<10)
			if err != nil {
				failure := alarmInvalid("The Alarm acknowledgement body is too large or unreadable.")
				return nil, 0, &failure
			}
			if len(body) > 0 {
				if err := decodeStrictAlarmJSON(body, &payload); err != nil {
					failure := alarmInvalid("The Alarm acknowledgement request is invalid.")
					return nil, 0, &failure
				}
			}
		}
		idempotencyKey := strings.TrimSpace(publicRequest.Header.Get("Idempotency-Key"))
		actorID := "principal:operator"
		if session, ok := routeSessionFromContext(publicRequest.Context()); ok && session.Principal.Subject != "" {
			actorID = session.Principal.Subject
		}
		corrID := idempotencyKey
		if corrID == "" {
			corrID = "ack-" + route.alarmID
		}
		mutation := alarmservice.Mutation{
			Operation:       alarmmodel.OperationAcknowledge,
			ExpectedVersion: payload.ExpectedVersion,
			Reason:          payload.Reason,
			AssigneeID:      payload.AssigneeID,
			SuppressedUntil: payload.SuppressedUntil,
			IdempotencyKey:  idempotencyKey,
			ActorType:       "PRINCIPAL",
			ActorID:         actorID,
			PolicyRevision:  "1",
			CorrelationID:   corrID,
			OccurredAt:      time.Now().UTC().Format(time.RFC3339Nano),
		}
		res, err := a.store.Apply(ctx, tenantID, route.siteID, route.alarmID, mutation)
		if err != nil {
			if errors.Is(err, alarmservice.ErrNotFound) {
				failure := alarmNotFound()
				return nil, http.StatusNotFound, &failure
			}
			if errors.Is(err, alarmservice.ErrIdempotencyConflict) {
				failure := alarmIdempotencyConflict()
				return nil, http.StatusConflict, &failure
			}
			failure := alarmUnavailable("Alarm Service could not complete the request.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		bytes, err := json.Marshal(res.Alarm)
		if err != nil {
			failure := alarmUnavailable("Alarm Service returned an invalid Alarm projection.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		return bytes, http.StatusOK, nil
	}

	if route.action == alarmauth.ActionAssign {
		var payload struct {
			ExpectedVersion uint64 `json:"expectedVersion"`
			Reason          string `json:"reason"`
			AssigneeID      string `json:"assigneeId"`
		}
		body, err := readBoundedBody(publicRequest.Body, 16<<10)
		if err != nil || len(body) == 0 || decodeStrictAlarmJSON(body, &payload) != nil {
			failure := alarmInvalid("The Alarm assignment request is invalid.")
			return nil, 0, &failure
		}
		payload.Reason = strings.TrimSpace(payload.Reason)
		payload.AssigneeID = strings.TrimSpace(payload.AssigneeID)
		idempotencyKey := strings.TrimSpace(publicRequest.Header.Get("Idempotency-Key"))
		if payload.ExpectedVersion == 0 || payload.Reason == "" || len(payload.Reason) > 256 || payload.AssigneeID == "" || len(payload.AssigneeID) > 256 || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 {
			failure := alarmInvalid("The Alarm assignment request is invalid.")
			return nil, 0, &failure
		}
		actorID := "principal:operator"
		if session, ok := routeSessionFromContext(publicRequest.Context()); ok && session.Principal.Subject != "" {
			actorID = session.Principal.Subject
		}
		assigneeID := payload.AssigneeID
		res, err := a.store.Apply(ctx, tenantID, route.siteID, route.alarmID, alarmservice.Mutation{
			Operation: alarmmodel.OperationAssign, ExpectedVersion: payload.ExpectedVersion, Reason: payload.Reason,
			AssigneeID: &assigneeID, IdempotencyKey: idempotencyKey,
			ActorType: "PRINCIPAL", ActorID: actorID, PolicyRevision: "1", CorrelationID: idempotencyKey,
			OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			switch {
			case errors.Is(err, alarmservice.ErrNotFound):
				failure := alarmNotFound()
				return nil, http.StatusNotFound, &failure
			case errors.Is(err, alarmmodel.ErrVersionConflict):
				failure := ownerFailure{status: http.StatusConflict, code: "ALARM_VERSION_CONFLICT", title: "Alarm version conflict", detail: "The Alarm changed before assignment was committed."}
				return nil, http.StatusConflict, &failure
			case errors.Is(err, alarmservice.ErrIdempotencyConflict):
				failure := alarmIdempotencyConflict()
				return nil, http.StatusConflict, &failure
			case errors.Is(err, alarmmodel.ErrInvalidOperation), errors.Is(err, alarmmodel.ErrInvalidTransition):
				failure := ownerFailure{status: http.StatusUnprocessableEntity, code: "ALARM_TRANSITION_INVALID", title: "Alarm transition invalid", detail: "The Alarm cannot be assigned from its current authoritative state."}
				return nil, http.StatusUnprocessableEntity, &failure
			default:
				failure := alarmUnavailable("Alarm Service could not complete the assignment.")
				return nil, http.StatusServiceUnavailable, &failure
			}
		}
		response, err := json.Marshal(res.Alarm)
		if err != nil {
			failure := alarmUnavailable("Alarm Service returned an invalid Alarm projection.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		return response, http.StatusOK, nil
	}

	failure := alarmUnavailable("Alarm Service returned an unsupported response.")
	return nil, http.StatusServiceUnavailable, &failure
}

type httpAlarmAdapter struct {
	baseURL          string
	httpClient       *http.Client
	timeout          time.Duration
	maxResponseBytes int64
}

func newHTTPAlarmAdapter(config AlarmConfig) AlarmOperations {
	return &httpAlarmAdapter{
		baseURL:          config.BackendBaseURL,
		httpClient:       config.BackendHTTPClient,
		timeout:          config.Timeout,
		maxResponseBytes: config.MaxResponseBytes,
	}
}

func (a *httpAlarmAdapter) ResolveScope(ctx context.Context, tenantID, alarmID string, serviceContext string) (alarmScope, *ownerFailure) {
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(callCtx, http.MethodGet, a.baseURL+internalAlarmScopePrefix+url.PathEscape(alarmID)+"/scope", nil)
	if err != nil {
		failure := alarmUnavailable("Alarm ownership resolution request could not be constructed.")
		return alarmScope{}, &failure
	}
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set(alarmReadContextHeader, serviceContext)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, upstream.Header)
	response, err := a.httpClient.Do(upstream)
	if err != nil {
		failure := alarmUnavailable("Alarm ownership resolution is temporarily unavailable.")
		return alarmScope{}, &failure
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, a.maxResponseBytes)
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
	if decodeStrictAlarmJSON(body, &scope) != nil || scope.TenantID != tenantID || !alarmmodel.IsUUIDv7(scope.SiteID) {
		failure := alarmUnavailable("Alarm ownership resolution returned an invalid scope.")
		return alarmScope{}, &failure
	}
	return scope, nil
}

func (a *httpAlarmAdapter) Execute(ctx context.Context, publicRequest *http.Request, route publicAlarmRoute, serviceContext string) ([]byte, int, *ownerFailure) {
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	path := internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarms"
	if route.siteRules {
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules"
	}
	method := http.MethodGet
	var requestBody io.Reader
	if route.alarmID != "" {
		path += "/" + url.PathEscape(route.alarmID)
	}
	upstreamURL := a.baseURL + path
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
		upstreamURL = a.baseURL + path
		if publicRequest.Body != nil && publicRequest.ContentLength != 0 {
			body, err := readBoundedBody(publicRequest.Body, 16<<10)
			if err != nil {
				failure := alarmInvalid("The Alarm acknowledgement body is too large or unreadable.")
				return nil, 0, &failure
			}
			requestBody = bytes.NewReader(body)
		}
	}
	if route.action == alarmauth.ActionAssign {
		method = http.MethodPost
		path += ":assign"
		upstreamURL = a.baseURL + path
		if publicRequest.Body != nil && publicRequest.ContentLength != 0 {
			body, err := readBoundedBody(publicRequest.Body, 16<<10)
			if err != nil {
				failure := alarmInvalid("The Alarm assignment body is too large or unreadable.")
				return nil, 0, &failure
			}
			requestBody = bytes.NewReader(body)
		}
	}
	request, err := http.NewRequestWithContext(callCtx, method, upstreamURL, requestBody)
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
	if route.action == alarmauth.ActionAssign {
		request.Header.Del(alarmReadContextHeader)
		request.Header.Set(alarmWriteContextHeader, serviceContext)
		request.Header.Set("Content-Type", publicRequest.Header.Get("Content-Type"))
		if key := strings.TrimSpace(publicRequest.Header.Get("Idempotency-Key")); key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
	}
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := a.httpClient.Do(request)
	if err != nil {
		failure := alarmUnavailable("Alarm Service is temporarily unavailable.")
		return nil, 0, &failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 599 {
		failure := alarmUnavailable("Alarm Service returned an invalid status.")
		return nil, 0, &failure
	}
	body, err := readBoundedBody(response.Body, a.maxResponseBytes)
	if err != nil {
		failure := alarmUnavailable("Alarm Service returned an oversized or unreadable response.")
		return nil, 0, &failure
	}
	return body, response.StatusCode, nil
}

func newAlarmController(config *AlarmConfig) *alarmController {
	if config == nil {
		return nil
	}
	resolved := *config
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultAlarmTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultAlarmResponseLimit
	}
	if resolved.BackendAudience == "" {
		resolved.BackendAudience = "alarm-service"
	}
	var ops AlarmOperations
	if resolved.Operations != nil {
		ops = resolved.Operations
	} else if resolved.Store != nil {
		ops = newDirectAlarmAdapter(resolved.Store)
	} else if strings.TrimSpace(resolved.BackendBaseURL) != "" {
		resolved.BackendBaseURL = strings.TrimRight(strings.TrimSpace(resolved.BackendBaseURL), "/")
		if resolved.BackendHTTPClient == nil {
			resolved.BackendHTTPClient = &http.Client{Timeout: resolved.Timeout}
		}
		ops = newHTTPAlarmAdapter(resolved)
	} else {
		return nil
	}
	return &alarmController{
		operations:       ops,
		baseURL:          resolved.BackendBaseURL,
		httpClient:       resolved.BackendHTTPClient,
		backendAudience:  resolved.BackendAudience,
		timeout:          resolved.Timeout,
		maxResponseBytes: resolved.MaxResponseBytes,
	}
}

func matchPublicAlarmRoute(path string) (publicAlarmRoute, bool) {
	if path == "/api/v1/alarms" {
		return publicAlarmRoute{template: "/api/v1/alarms", action: alarmauth.ActionRead}, true
	}
	if path == "/api/v1/alarm-rules" {
		return publicAlarmRoute{template: "/api/v1/alarm-rules", action: alarmauth.ActionRead, siteRules: true}, true
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
	if err != nil || !alarmmodel.IsUUIDv7(alarmID) {
		return publicAlarmRoute{}, false
	}
	if len(segments) == 1 {
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}", alarmID: alarmID, action: alarmauth.ActionRead,
		}, true
	}
	if segments[1] == "assign" {
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/assign", alarmID: alarmID, action: alarmauth.ActionAssign,
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
	if route.siteRules {
		dispatchSiteRulesRoute(h, writer, request, route)
		return
	}
	expectedMethod := http.MethodGet
	if route.action == alarmauth.ActionAssign {
		expectedMethod = http.MethodPost
	}
	if route.action == alarmauth.ActionAck {
		expectedMethod = http.MethodPost
	}
	if request.Method != expectedMethod {
		writeMethodNotAllowedFor(writer, request, expectedMethod)
		return
	}
	if h.alarm == nil || h.alarm.operations == nil {
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
	response, failure := CallOwner(request.Context(), h, h.alarmCaller(session), h.alarmOwner(), string(route.action), route.scope(session.TenantID), alarmCall{public: request, route: route, limit: limit})
	if failure != nil {
		h.writeAlarmFailure(writer, request, *failure)
		return
	}
	if response.status != http.StatusOK {
		h.forwardAlarmProblem(writer, request, response.status, response.body, route)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	switch value := response.value.(type) {
	case alarmmodel.ListResponse:
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": value.Items,
			"meta": map[string]any{
				"requestId": requestIDFromContext(request.Context()), "limit": limit,
				"nextCursor": value.NextCursor, "hasMore": value.HasMore,
			},
		})
	case alarmmodel.Alarm:
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": value,
			"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
		})
	default:
		h.writeAlarmFailure(writer, request, alarmUnavailable("Alarm Service returned an unsupported response."))
	}
}

// dispatchSiteRulesRoute lists the Site's rules in effect. It answers with Problem Details, as the
// generated client expects, where the older Alarm routes keep their own error envelope.
func dispatchSiteRulesRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	fail := func(failure ownerFailure) {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
	}
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	if h.alarm == nil || h.alarm.operations == nil {
		fail(alarmUnavailable("The Alarm service is not configured."))
		return
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["siteId"]) != 1 || !alarmmodel.IsUUIDv7(query.Get("siteId")) {
		fail(alarmInvalid("The Site rules query takes exactly one siteId."))
		return
	}
	route.siteID = query.Get("siteId")
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}
	if failure := h.checkAlarmSiteVisibility(request, session, route.siteID, true); failure != nil {
		fail(*failure)
		return
	}
	response, failure := CallOwner(request.Context(), h, h.alarmCaller(session), h.alarmOwner(), string(route.action), route.scope(session.TenantID), alarmCall{public: request, route: route})
	if failure != nil {
		fail(*failure)
		return
	}
	switch response.status {
	case http.StatusOK:
	case http.StatusForbidden:
		fail(alarmDenied())
		return
	default:
		fail(alarmUnavailable("Alarm Service could not list the Site rules."))
		return
	}
	list, ok := response.value.(alarmservice.SiteRuleList)
	if !ok {
		fail(alarmUnavailable("Alarm Service returned an invalid Site rule list."))
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": list.Items,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func validatePublicAlarmQuery(query url.Values) (string, int, bool) {
	for key := range query {
		switch key {
		case "siteId", "condition", "severity", "acknowledged", "suppressed", "cursor", "limit":
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
	if condition := alarmmodel.Condition(query.Get("condition")); condition != "" {
		switch condition {
		case alarmmodel.ConditionActive, alarmmodel.ConditionCleared:
		default:
			return "", 0, false
		}
	}
	for _, key := range []string{"acknowledged", "suppressed"} {
		if raw := query.Get(key); raw != "" {
			if _, err := strconv.ParseBool(raw); err != nil {
				return "", 0, false
			}
		}
	}
	if severity := alarmmodel.Severity(query.Get("severity")); severity != "" {
		switch severity {
		case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMinor, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
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

func (h *handler) resolveAlarmScope(request *http.Request, session bffSession, alarmID string) (alarmScope, *ownerFailure) {
	if h.identity == nil || h.identity.config.DelegationSigner == nil || h.alarm == nil || h.alarm.operations == nil || !alarmmodel.IsUUIDv7(alarmID) {
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
	return h.alarm.operations.ResolveScope(request.Context(), session.TenantID, alarmID, token)
}

func (h *handler) checkAlarmSiteVisibility(request *http.Request, session bffSession, siteID string, list bool) *ownerFailure {
	if !alarmmodel.IsUUIDv7(siteID) {
		failure := alarmUnavailable("Authoritative Site visibility is unavailable.")
		return &failure
	}
	visibility := h.siteVisibility(request, session, siteID)
	switch {
	case visibility.unavailable != "":
		value := alarmUnavailable(visibility.unavailable)
		return &value
	case visibility.hidden:
		value := alarmNotFound()
		if list {
			value = alarmSiteNotFound()
		}
		return &value
	}
	return nil
}

// alarmCall is one public Alarm request as the Alarm owner serves it.
type alarmCall struct {
	public *http.Request
	route  publicAlarmRoute
	// limit is the page size the list was asked for, which the list projection must honour.
	limit int
}

// scope is what an Alarm action is decided and signed for: the Site, and the Alarm when there is one.
func (route publicAlarmRoute) scope(tenantID string) ownerScope {
	scope := ownerScope{tenantID: tenantID, siteID: route.siteID}
	if route.alarmID != "" {
		scope.resourceKind, scope.resourceID = "alarm", route.alarmID
	}
	return scope
}

func (h *handler) alarmCaller(session bffSession) ownerCaller {
	presenter := ""
	if h.identity != nil {
		presenter = h.identity.config.ExecutingWorkloadSPIFFE
	}
	return ownerCaller{session: session, presenter: presenter}
}

// alarmOwner is the Alarm Service as CallOwner calls it: IAM decides, the configured adapter
// serves, and the public Alarm contract checks the answer.
func (h *handler) alarmOwner() domainOwner[alarmCall] {
	audience := ""
	if h.alarm != nil {
		audience = h.alarm.backendAudience
	}
	return domainOwner[alarmCall]{
		name:     "Alarm Service",
		audience: audience,
		decide:   h.decideAlarm,
		serve: func(ctx context.Context, call alarmCall, serviceContext string) (ownerResponse, *ownerFailure) {
			if h.alarm == nil || h.alarm.operations == nil {
				failure := alarmUnavailable("The Alarm service is not configured.")
				return ownerResponse{}, &failure
			}
			body, status, failure := h.alarm.operations.Execute(ctx, call.public, call.route, serviceContext)
			if failure != nil {
				return ownerResponse{}, failure
			}
			return ownerResponse{status: status, body: body}, nil
		},
		validate: validateAlarmResponse,
	}
}

// validateAlarmResponse decodes the owner's answer as the public contract has it, bound to the
// Tenant, Site and Alarm that were decided.
func validateAlarmResponse(scope ownerScope, call alarmCall, response ownerResponse) (any, error) {
	switch {
	case call.route.siteRules:
		var list alarmservice.SiteRuleList
		if err := decodeStrictAlarmJSON(response.body, &list); err != nil {
			return nil, err
		}
		return list, list.Validate()
	case scope.resourceID == "":
		var list alarmmodel.ListResponse
		if err := decodeStrictAlarmJSON(response.body, &list); err != nil {
			return nil, err
		}
		return list, list.Validate(scope.tenantID, scope.siteID, call.limit)
	default:
		var alarm alarmmodel.Alarm
		if err := decodeStrictAlarmJSON(response.body, &alarm); err != nil {
			return nil, err
		}
		if err := alarm.Validate(); err != nil {
			return nil, err
		}
		if alarm.TenantID != scope.tenantID || alarm.SiteID != scope.siteID || alarm.AlarmID != scope.resourceID {
			return nil, errors.New("alarm outside the decided scope")
		}
		return alarm, nil
	}
}

// decideAlarm asks IAM whether the caller may take the Alarm action on the scope.
func (h *handler) decideAlarm(ctx context.Context, caller ownerCaller, action string, scope ownerScope) (ownerDecision, *ownerFailure) {
	if h.identity == nil || h.alarm == nil || h.identity.config.IAMURL == "" || h.identity.config.IAMHTTPClient == nil || h.identity.config.DelegationSigner == nil {
		failure := alarmUnavailable("Alarm authorization is not configured.")
		return ownerDecision{}, &failure
	}
	session := caller.session
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
		return ownerDecision{}, &failure
	}
	input := alarmauth.DecisionRequest{
		TenantID: scope.tenantID,
		SiteID:   scope.siteID,
		AlarmID:  scope.resourceID,
		Action:   alarmauth.Action(action),
	}
	body, err := json.Marshal(input)
	if err != nil {
		failure := alarmUnavailable("The Alarm authorization request could not be encoded.")
		return ownerDecision{}, &failure
	}
	callCtx, cancel := context.WithTimeout(ctx, h.alarm.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(h.identity.config.IAMURL, "/")+alarmDecisionPath, bytes.NewReader(body))
	if err != nil {
		failure := alarmUnavailable("The Alarm authorization request could not be constructed.")
		return ownerDecision{}, &failure
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set("X-Delegation-Grant", delegation)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, upstream.Header)
	response, err := h.identity.config.IAMHTTPClient.Do(upstream)
	if err != nil {
		failure := alarmUnavailable("IAM Alarm authorization is temporarily unavailable.")
		return ownerDecision{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := alarmUnavailable("IAM did not return a valid Alarm authorization decision.")
		return ownerDecision{}, &failure
	}
	raw, err := readBoundedBody(response.Body, maximumAlarmDecisionLength)
	if err != nil {
		failure := alarmUnavailable("IAM returned an unreadable Alarm authorization decision.")
		return ownerDecision{}, &failure
	}
	var output alarmauth.DecisionResponse
	if decodeStrictAlarmJSON(raw, &output) != nil || output.Decision.Validate() != nil {
		failure := alarmUnavailable("IAM returned an invalid Alarm authorization decision.")
		return ownerDecision{}, &failure
	}
	decision := output.Decision
	if decision.Subject != session.Principal.Subject || decision.SubjectIssuer != session.Principal.Issuer ||
		decision.TenantID != scope.tenantID || decision.SiteID != scope.siteID || decision.AlarmID != scope.resourceID || decision.Action != input.Action {
		failure := alarmUnavailable("IAM returned an Alarm decision outside the authenticated boundary.")
		return ownerDecision{}, &failure
	}
	if !decision.Allowed {
		failure := alarmDenied()
		return ownerDecision{}, &failure
	}
	return ownerDecision{principalID: decision.PrincipalID, policyRevision: decision.PolicyRevision}, nil
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
	case status == http.StatusConflict && value.Code == "ALARM_VERSION_CONFLICT":
		h.writeAlarmFailure(writer, request, alarmVersionConflict())
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

func (h *handler) writeAlarmFailure(writer http.ResponseWriter, request *http.Request, failure ownerFailure) {
	writeV212Error(writer, request, failure.status, failure.code, failure.detail, map[string]any{"retryable": failure.retryable})
}

func alarmInvalid(detail string) ownerFailure {
	return ownerFailure{status: http.StatusBadRequest, code: "INVALID_ARGUMENT", title: "Invalid argument", detail: detail}
}

func alarmInvalidCursor() ownerFailure {
	return ownerFailure{status: http.StatusBadRequest, code: "INVALID_CURSOR", title: "Invalid cursor", detail: "The Alarm cursor is invalid for the current filters."}
}

func alarmNotFound() ownerFailure {
	return ownerFailure{status: http.StatusNotFound, code: "ALARM_NOT_FOUND", title: "Alarm not found", detail: "Alarm not found."}
}

func alarmSiteNotFound() ownerFailure {
	return ownerFailure{status: http.StatusNotFound, code: "SITE_NOT_FOUND", title: "Site not found", detail: "Site not found."}
}

func alarmDenied() ownerFailure {
	return ownerFailure{status: http.StatusForbidden, code: "FORBIDDEN", title: "Forbidden", detail: "The caller lacks the required Alarm permission for this visible Site."}
}

func alarmIdempotencyConflict() ownerFailure {
	return ownerFailure{status: http.StatusConflict, code: "IDEMPOTENCY_CONFLICT", title: "Idempotency conflict", detail: "The supplied Idempotency-Key is already bound to a different Alarm mutation request."}
}

func alarmVersionConflict() ownerFailure {
	return ownerFailure{status: http.StatusConflict, code: "ALARM_VERSION_CONFLICT", title: "Alarm version conflict", detail: "The Alarm changed before this mutation was committed."}
}

func alarmUnavailable(detail string) ownerFailure {
	return ownerFailure{status: http.StatusServiceUnavailable, code: "INTERNAL_ERROR", title: "Internal error", detail: detail, retryable: true}
}

func writeV212Error(writer http.ResponseWriter, request *http.Request, status int, code, message string, details any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
		"meta": map[string]any{
			"requestId": requestIDFromContext(request.Context()),
		},
	})
}
