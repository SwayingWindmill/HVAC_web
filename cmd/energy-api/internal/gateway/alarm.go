package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
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
	siteRules                bool
	siteRulesDiff            bool
	siteRulesPublish         bool
	siteRulesSimulate        bool
	siteRuleRevisions        bool
	siteRuleRollback         bool
	siteRuleDeadLetters      bool
	siteRuleDeadLetterReplay bool
	bundleRevision           uint64
	outboxID                 string
	evaluations              bool
	now                      time.Time
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
	Now               func() time.Time
}

type alarmController struct {
	operations       AlarmOperations
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	timeout          time.Duration
	maxResponseBytes int64
}

type alarmLifecycleSpec struct {
	operation alarmmodel.Operation
	suffix    string
	noun      string
	label     string
}

var alarmLifecycleSpecs = map[alarmauth.Action]alarmLifecycleSpec{
	alarmauth.ActionAssign:     {operation: alarmmodel.OperationAssign, suffix: ":assign", noun: "assignment", label: "assigned"},
	alarmauth.ActionUnassign:   {operation: alarmmodel.OperationUnassign, suffix: ":unassign", noun: "unassignment", label: "unassigned"},
	alarmauth.ActionSuppress:   {operation: alarmmodel.OperationSuppress, suffix: ":suppress", noun: "suppression", label: "suppressed"},
	alarmauth.ActionUnsuppress: {operation: alarmmodel.OperationUnsuppress, suffix: ":unsuppress", noun: "unsuppression", label: "unsuppressed"},
}

type directAlarmAdapter struct {
	store alarmservice.Store
	now   func() time.Time
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
	if route.siteRules || route.siteRulesDiff || route.siteRulesPublish || route.siteRulesSimulate || route.siteRuleRevisions || route.siteRuleRollback {
		failure := alarmUnavailable("Site rules are only served by the Alarm Service.")
		return nil, http.StatusServiceUnavailable, &failure
	}
	if route.siteRuleDeadLetters || route.siteRuleDeadLetterReplay {
		failure := alarmUnavailable("Site rule dead letters are only served by the Alarm Service.")
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
		if activeFrom, ok := parseOptionalRFC3339UTC(query, "activeFrom"); ok {
			filter.ActiveFrom = activeFrom
		}
		if activeTo, ok := parseOptionalRFC3339UTC(query, "activeTo"); ok {
			filter.ActiveTo = activeTo
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

	if route.action == alarmauth.ActionRead && route.evaluations {
		limit, valid := validatePublicAlarmEvaluationsQuery(query)
		if !valid {
			failure := alarmInvalid("The Alarm evaluation query is invalid.")
			return nil, 0, &failure
		}
		list, err := a.store.ListAlarmEvaluations(ctx, tenantID, route.siteID, route.alarmID, limit)
		if err != nil {
			if errors.Is(err, alarmservice.ErrNotFound) {
				failure := alarmNotFound()
				return nil, http.StatusNotFound, &failure
			}
			failure := alarmUnavailable("Alarm Service could not complete the request.")
			return nil, http.StatusServiceUnavailable, &failure
		}
		bytes, err := json.Marshal(list)
		if err != nil {
			failure := alarmUnavailable("Alarm Service returned an invalid Alarm evaluation list.")
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
			OccurredAt:      adapterReferenceTime(route, a.now).Format(time.RFC3339Nano),
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

	if spec, isMutation := alarmLifecycleSpecs[route.action]; isMutation {
		mutation, _, failure := parseAlarmLifecycleMutation(publicRequest, route.action, adapterReferenceTime(route, a.now))
		if failure != nil {
			return nil, 0, failure
		}
		res, err := a.store.Apply(ctx, tenantID, route.siteID, route.alarmID, mutation)
		if err != nil {
			switch {
			case errors.Is(err, alarmservice.ErrNotFound):
				failure := alarmNotFound()
				return nil, http.StatusNotFound, &failure
			case errors.Is(err, alarmmodel.ErrVersionConflict):
				failure := ownerFailure{status: http.StatusConflict, code: "ALARM_VERSION_CONFLICT", title: "Alarm version conflict", detail: "The Alarm changed before " + spec.noun + " was committed."}
				return nil, http.StatusConflict, &failure
			case errors.Is(err, alarmservice.ErrIdempotencyConflict):
				failure := alarmIdempotencyConflict()
				return nil, http.StatusConflict, &failure
			case errors.Is(err, alarmmodel.ErrInvalidOperation), errors.Is(err, alarmmodel.ErrInvalidTransition):
				failure := ownerFailure{status: http.StatusUnprocessableEntity, code: "ALARM_TRANSITION_INVALID", title: "Alarm transition invalid", detail: "The Alarm cannot be " + spec.label + " from its current authoritative state."}
				return nil, http.StatusUnprocessableEntity, &failure
			default:
				failure := alarmUnavailable("Alarm Service could not complete the " + spec.noun + ".")
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
	now              func() time.Time
}

func newHTTPAlarmAdapter(config AlarmConfig) AlarmOperations {
	return &httpAlarmAdapter{
		baseURL:          config.BackendBaseURL,
		httpClient:       config.BackendHTTPClient,
		timeout:          config.Timeout,
		maxResponseBytes: config.MaxResponseBytes,
		now:              config.Now,
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
	defer drainAndCloseBody(response.Body)
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
	switch {
	case route.siteRules:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules"
	case route.siteRulesDiff:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules:diff"
	case route.siteRulesSimulate:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules:simulate"
	case route.siteRuleRevisions:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules/revisions"
	case route.siteRuleRollback:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules/revisions/" + strconv.FormatUint(route.bundleRevision, 10) + ":rollback"
	case route.siteRuleDeadLetters:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules/dead-letters"
	case route.siteRuleDeadLetterReplay:
		path = internalSiteAlarmsPrefix + url.PathEscape(route.siteID) + "/alarm-rules/dead-letters/" + url.PathEscape(route.outboxID) + "/replay"
	}
	method := http.MethodGet
	var requestBody io.Reader
	if route.alarmID != "" {
		path += "/" + url.PathEscape(route.alarmID)
		if route.evaluations {
			path += "/evaluations"
		}
	}
	upstreamURL := a.baseURL + path
	switch {
	case route.siteRulesPublish:
		method = http.MethodPut
		if publicRequest.Body == nil {
			failure := alarmInvalid("The Alarm rule publish request body is required.")
			return nil, 0, &failure
		}
		rawBody, err := readBoundedBody(publicRequest.Body, 256<<10)
		if err != nil || len(bytes.TrimSpace(rawBody)) == 0 {
			failure := alarmInvalid("The Alarm rule publish request body is invalid.")
			return nil, 0, &failure
		}
		requestBody = bytes.NewReader(rawBody)
	case route.siteRulesDiff:
		method = http.MethodPost
		if publicRequest.Body == nil {
			failure := alarmInvalid("The Alarm rule diff request body is required.")
			return nil, 0, &failure
		}
		rawBody, err := readBoundedBody(publicRequest.Body, 256<<10)
		if err != nil || len(bytes.TrimSpace(rawBody)) == 0 {
			failure := alarmInvalid("The Alarm rule diff request body is invalid.")
			return nil, 0, &failure
		}
		requestBody = bytes.NewReader(rawBody)
	case route.siteRuleRollback:
		method = http.MethodPost
		if publicRequest.Body == nil {
			failure := alarmInvalid("The Alarm rule rollback request body is required.")
			return nil, 0, &failure
		}
		rawBody, err := readBoundedBody(publicRequest.Body, 16<<10)
		if err != nil || len(bytes.TrimSpace(rawBody)) == 0 {
			failure := alarmInvalid("The Alarm rule rollback request body is invalid.")
			return nil, 0, &failure
		}
		requestBody = bytes.NewReader(rawBody)
	case route.siteRulesSimulate:
		method = http.MethodPost
		if publicRequest.Body == nil {
			failure := alarmInvalid("The Alarm rule simulation request body is required.")
			return nil, 0, &failure
		}
		rawBody, err := readBoundedBody(publicRequest.Body, 64<<10)
		if err != nil || len(bytes.TrimSpace(rawBody)) == 0 {
			failure := alarmInvalid("The Alarm rule simulation request body is invalid.")
			return nil, 0, &failure
		}
		requestBody = bytes.NewReader(rawBody)
	case route.siteRuleDeadLetterReplay:
		method = http.MethodPost
	case route.alarmID == "":
		query := publicRequest.URL.Query()
		query.Del("siteId")
		if encoded := query.Encode(); encoded != "" {
			upstreamURL += "?" + encoded
		}
	case route.evaluations:
		if encoded := publicRequest.URL.Query().Encode(); encoded != "" {
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
	spec, isMutation := alarmLifecycleSpecs[route.action]
	if isMutation {
		_, rawBody, failure := parseAlarmLifecycleMutation(publicRequest, route.action, adapterReferenceTime(route, a.now))
		if failure != nil {
			return nil, 0, failure
		}
		method = http.MethodPost
		path += spec.suffix
		upstreamURL = a.baseURL + path
		requestBody = bytes.NewReader(rawBody)
	}
	request, err := http.NewRequestWithContext(callCtx, method, upstreamURL, requestBody)
	if err != nil {
		failure := alarmUnavailable("The Alarm service request could not be constructed.")
		return nil, 0, &failure
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	if route.siteRulesSimulate || route.siteRulesDiff {
		request.Header.Set(alarmReadContextHeader, serviceContext)
		if ct := strings.TrimSpace(publicRequest.Header.Get("Content-Type")); ct != "" {
			request.Header.Set("Content-Type", ct)
		} else {
			request.Header.Set("Content-Type", "application/json")
		}
	} else if route.siteRulesPublish || route.siteRuleRollback || route.siteRuleDeadLetterReplay || route.action == alarmauth.ActionAck || isMutation {
		request.Header.Set(alarmWriteContextHeader, serviceContext)
		if ct := publicRequest.Header.Get("Content-Type"); strings.TrimSpace(ct) != "" {
			request.Header.Set("Content-Type", ct)
		} else if isMutation || route.siteRulesPublish || route.siteRuleRollback {
			request.Header.Set("Content-Type", "application/json")
		}
		if key := strings.TrimSpace(publicRequest.Header.Get("Idempotency-Key")); key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
	} else {
		request.Header.Set(alarmReadContextHeader, serviceContext)
	}
	request.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), request.Header)
	response, err := a.httpClient.Do(request)
	if err != nil {
		failure := alarmUnavailable("Alarm Service is temporarily unavailable.")
		return nil, 0, &failure
	}
	defer drainAndCloseBody(response.Body)
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
		ops = &directAlarmAdapter{store: resolved.Store, now: resolved.Now}
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
	if path == "/api/v1/alarm-rules/diff" {
		return publicAlarmRoute{template: "/api/v1/alarm-rules/diff", action: alarmauth.ActionRead, siteRulesDiff: true}, true
	}
	if path == "/api/v1/alarm-rules/simulate" {
		return publicAlarmRoute{template: "/api/v1/alarm-rules/simulate", action: alarmauth.ActionRead, siteRulesSimulate: true}, true
	}
	if path == "/api/v1/alarm-rules/revisions" {
		return publicAlarmRoute{template: "/api/v1/alarm-rules/revisions", action: alarmauth.ActionRead, siteRuleRevisions: true}, true
	}
	if rest, ok := strings.CutPrefix(path, "/api/v1/alarm-rules/revisions/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "rollback" {
			revText, err := url.PathUnescape(parts[0])
			if err == nil && revText != "" {
				if rev, parseErr := strconv.ParseInt(revText, 10, 64); parseErr == nil && rev > 0 && strconv.FormatInt(rev, 10) == revText {
					return publicAlarmRoute{
						template:         "/api/v1/alarm-rules/revisions/{revision}/rollback",
						bundleRevision:   uint64(rev),
						siteRuleRollback: true,
					}, true
				}
			}
		}
		return publicAlarmRoute{}, false
	}
	if path == "/api/v1/alarm-rules/dead-letters" {
		return publicAlarmRoute{template: "/api/v1/alarm-rules/dead-letters", action: alarmauth.ActionRead, siteRuleDeadLetters: true}, true
	}
	if rest, ok := strings.CutPrefix(path, "/api/v1/alarm-rules/dead-letters/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "replay" {
			outboxID, err := url.PathUnescape(parts[0])
			if err == nil && outboxID != "" {
				return publicAlarmRoute{
					template:                 "/api/v1/alarm-rules/dead-letters/{outboxId}/replay",
					outboxID:                 outboxID,
					siteRuleDeadLetterReplay: true,
				}, true
			}
		}
		return publicAlarmRoute{}, false
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
	switch segments[1] {
	case "ack":
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/ack", alarmID: alarmID, action: alarmauth.ActionAck,
		}, true
	case "assign":
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/assign", alarmID: alarmID, action: alarmauth.ActionAssign,
		}, true
	case "unassign":
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/unassign", alarmID: alarmID, action: alarmauth.ActionUnassign,
		}, true
	case "suppress":
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/suppress", alarmID: alarmID, action: alarmauth.ActionSuppress,
		}, true
	case "unsuppress":
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/unsuppress", alarmID: alarmID, action: alarmauth.ActionUnsuppress,
		}, true
	case "evaluations":
		return publicAlarmRoute{
			template: "/api/v1/alarms/{alarmId}/evaluations", alarmID: alarmID, action: alarmauth.ActionRead, evaluations: true,
		}, true
	default:
		return publicAlarmRoute{}, false
	}
}

func dispatchAlarmRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	if route.siteRules {
		dispatchSiteRulesRoute(h, writer, request, route)
		return
	}
	if route.siteRulesDiff {
		dispatchSiteRulesDiffRoute(h, writer, request, route)
		return
	}
	if route.siteRulesSimulate {
		dispatchSiteRulesSimulateRoute(h, writer, request, route)
		return
	}
	if route.siteRuleRevisions {
		dispatchSiteRuleRevisionsRoute(h, writer, request, route)
		return
	}
	if route.siteRuleRollback {
		dispatchSiteRuleRollbackRoute(h, writer, request, route)
		return
	}
	if route.siteRuleDeadLetters {
		dispatchSiteRuleDeadLettersRoute(h, writer, request, route)
		return
	}
	if route.siteRuleDeadLetterReplay {
		dispatchSiteRuleDeadLetterReplayRoute(h, writer, request, route)
		return
	}
	expectedMethod := http.MethodGet
	if route.action == alarmauth.ActionAck {
		expectedMethod = http.MethodPost
	}
	if _, isMutation := alarmLifecycleSpecs[route.action]; isMutation {
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
	if expectedMethod == http.MethodPost {
		if h.identity == nil {
			h.writeAlarmFailure(writer, request, alarmUnavailable("Session validation is unavailable."))
			return
		}
		if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
	}
	route.now = h.alarmReferenceNow()
	limit := 0
	if route.alarmID == "" {
		var valid bool
		route.siteID, limit, valid = validatePublicAlarmQuery(request.URL.Query())
		if !valid {
			h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm list query is invalid."))
			return
		}
	} else {
		if route.evaluations {
			var valid bool
			limit, valid = validatePublicAlarmEvaluationsQuery(request.URL.Query())
			if !valid {
				h.writeAlarmFailure(writer, request, alarmInvalid("The Alarm evaluation query is invalid."))
				return
			}
		} else if len(request.URL.Query()) != 0 {
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
	case alarmservice.AlarmEvaluationList:
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": value,
			"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
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

func executeSiteRulesGatewayCall(
	h *handler,
	writer http.ResponseWriter,
	request *http.Request,
	route publicAlarmRoute,
	expectedMethod string,
	queryErrorDetail string,
	checkCSRF bool,
) (ownerResponse, bool) {
	fail := func(failure ownerFailure) {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
	}
	if request.Method != expectedMethod {
		writeMethodNotAllowedFor(writer, request, expectedMethod)
		return ownerResponse{}, false
	}
	if h.alarm == nil || h.alarm.operations == nil {
		fail(alarmUnavailable("The Alarm service is not configured."))
		return ownerResponse{}, false
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["siteId"]) != 1 || !alarmmodel.IsUUIDv7(query.Get("siteId")) {
		fail(alarmInvalid(queryErrorDetail))
		return ownerResponse{}, false
	}
	route.siteID = query.Get("siteId")
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return ownerResponse{}, false
	}
	if checkCSRF {
		if h.identity == nil {
			fail(alarmUnavailable("Session validation is unavailable."))
			return ownerResponse{}, false
		}
		if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return ownerResponse{}, false
		}
	}
	if failure := h.checkAlarmSiteVisibility(request, session, route.siteID, true); failure != nil {
		fail(*failure)
		return ownerResponse{}, false
	}
	response, failure := CallOwner(request.Context(), h, h.alarmCaller(session), h.alarmOwner(), string(route.action), route.scope(session.TenantID), alarmCall{public: request, route: route})
	if failure != nil {
		fail(*failure)
		return ownerResponse{}, false
	}
	return response, true
}

// dispatchSiteRulesRoute handles GET (list rules) and PUT (hot-publish bundle) on /api/v1/alarm-rules.
func dispatchSiteRulesRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	switch request.Method {
	case http.MethodGet:
	case http.MethodPut:
		route.siteRulesPublish = true
		dispatchSiteRulesPublishRoute(h, writer, request, route)
		return
	default:
		writeMethodNotAllowedFor(writer, request, "GET, PUT")
		return
	}
	response, ok := executeSiteRulesGatewayCall(h, writer, request, route, http.MethodGet, "The Site rules query takes exactly one siteId.", false)
	if !ok {
		return
	}
	switch response.status {
	case http.StatusOK:
	case http.StatusForbidden:
		writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", "The caller lacks the required Alarm permission for this visible Site.", false, nil)
		return
	default:
		writeProblem(writer, request, http.StatusServiceUnavailable, "INTERNAL_ERROR", "Internal error", "Alarm Service could not list the Site rules.", true, nil)
		return
	}
	list, ok := response.value.(alarmservice.SiteRuleList)
	if !ok {
		writeProblem(writer, request, http.StatusServiceUnavailable, "INTERNAL_ERROR", "Internal error", "Alarm Service returned an invalid Site rule list.", true, nil)
		return
	}
	meta := map[string]any{"requestId": requestIDFromContext(request.Context())}
	if list.BundleRevision > 0 {
		meta["bundleRevision"] = list.BundleRevision
	}
	if list.BundleDigest != "" {
		meta["bundleDigest"] = list.BundleDigest
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": list.Items,
		"meta": meta,
	})
}

func dispatchSiteRulesDiffRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	response, ok := executeSiteRulesGatewayCall(h, writer, request, route, http.MethodPost, "The Site rule diff query takes exactly one siteId.", true)
	if !ok {
		return
	}
	if response.status != http.StatusOK {
		failure := alarmProblemFromUpstream(response.status, response.body, "Alarm Service could not compute the Site rule diff.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	result, ok := response.value.(alarmservice.SiteRuleDiffResult)
	if !ok {
		failure := alarmUnavailable("Alarm Service returned an invalid Site rule diff.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func validateSiteRuleRevisionsQuery(query url.Values) (string, bool) {
	for key, values := range query {
		if len(values) != 1 || (key != "siteId" && key != "limit") {
			return "", false
		}
	}
	siteID := strings.TrimSpace(query.Get("siteId"))
	if !alarmmodel.IsUUIDv7(siteID) {
		return "", false
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > 100 {
			return "", false
		}
	}
	return siteID, true
}

func dispatchSiteRuleRevisionsRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
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
	siteID, valid := validateSiteRuleRevisionsQuery(request.URL.Query())
	if !valid {
		fail(alarmInvalid("The Site rule revisions query requires siteId and optional limit between 1 and 100."))
		return
	}
	route.siteID = siteID
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
	if response.status != http.StatusOK {
		fail(alarmProblemFromUpstream(response.status, response.body, "Alarm Service could not list Site rule revisions."))
		return
	}
	list, ok := response.value.(alarmservice.SiteRuleBundleRevisionList)
	if !ok {
		fail(alarmUnavailable("Alarm Service returned an invalid Site rule revision list."))
		return
	}
	if list.Items == nil {
		list.Items = []alarmservice.SiteRuleBundleRevisionView{}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": list,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

type siteRuleWriteSpec struct {
	expectedMethod        string
	targetID              string
	targetLabel           string
	auditAction           string
	forbiddenDetail       string
	queryErrorDetail      string
	contentTypeDetail     string
	invalidBodyDetail     string
	maxBodyBytes          int64
	upstreamErrorDetail   string
	invalidResponseDetail string
}

func dispatchSiteRulesPublishRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["siteId"]) != 1 || !alarmmodel.IsUUIDv7(query.Get("siteId")) {
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The Site rule publish query takes exactly one siteId.", false, nil)
		return
	}
	route.siteID = query.Get("siteId")
	executeSiteRuleWriteRoute(h, writer, request, route, siteRuleWriteSpec{
		expectedMethod:        http.MethodPut,
		targetID:              "site:" + route.siteID,
		targetLabel:           "站点告警规则热发布",
		auditAction:           "ALARM_RULES_PUBLISHED",
		forbiddenDetail:       "The caller lacks permission to publish Site alarm rules.",
		queryErrorDetail:      "The Site rule publish query takes exactly one siteId.",
		contentTypeDetail:     "The Site rule publish request must use application/json.",
		invalidBodyDetail:     "The Site rule publish request body is missing, empty, or oversized.",
		maxBodyBytes:          256 << 10,
		upstreamErrorDetail:   "Alarm Service could not publish the Site alarm rules.",
		invalidResponseDetail: "Alarm Service returned an invalid Site rule publish response.",
	})
}

func dispatchSiteRuleRollbackRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["siteId"]) != 1 || !alarmmodel.IsUUIDv7(query.Get("siteId")) {
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The Site rule rollback query takes exactly one siteId.", false, nil)
		return
	}
	route.siteID = query.Get("siteId")
	executeSiteRuleWriteRoute(h, writer, request, route, siteRuleWriteSpec{
		expectedMethod:        http.MethodPost,
		targetID:              fmt.Sprintf("site:%s:rev:%d", route.siteID, route.bundleRevision),
		targetLabel:           "站点告警规则版本回滚",
		auditAction:           "ALARM_RULES_ROLLED_BACK",
		forbiddenDetail:       "The caller lacks permission to roll back Site alarm rules.",
		queryErrorDetail:      "The Site rule rollback query takes exactly one siteId.",
		contentTypeDetail:     "The Site rule rollback request must use application/json.",
		invalidBodyDetail:     "The Site rule rollback request body is missing, empty, or oversized.",
		maxBodyBytes:          16 << 10,
		upstreamErrorDetail:   "Alarm Service could not roll back the Site alarm rules.",
		invalidResponseDetail: "Alarm Service returned an invalid Site rule rollback response.",
	})
}

func executeSiteRuleWriteRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute, spec siteRuleWriteSpec) {
	emitAudit := func(session bffSession, outcome string, summary map[string]any) {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeRegistryMutation,
			TargetID:      spec.targetID,
			TargetLabel:   spec.targetLabel,
			Action:        spec.auditAction,
			Outcome:       outcome,
			ChangeSummary: summary,
		})
	}
	fail := func(failure ownerFailure) {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
	}
	if request.Method != spec.expectedMethod {
		writeMethodNotAllowedFor(writer, request, spec.expectedMethod)
		return
	}
	if h.alarm == nil || h.alarm.operations == nil || h.identity == nil || h.identity.config.DelegationSigner == nil {
		fail(alarmUnavailable("The Alarm service is not configured."))
		return
	}
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}
	if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
		emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": failure.code})
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": "INVALID_ARGUMENT"})
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "Invalid content type", spec.contentTypeDetail, false, nil)
		return
	}
	if request.Body == nil {
		emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": "INVALID_ARGUMENT"})
		fail(alarmInvalid(spec.invalidBodyDetail))
		return
	}
	rawBody, err := readBoundedBody(request.Body, spec.maxBodyBytes)
	if err != nil || len(bytes.TrimSpace(rawBody)) == 0 {
		emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": "INVALID_ARGUMENT"})
		fail(alarmInvalid(spec.invalidBodyDetail))
		return
	}
	clonedRequest := request.Clone(request.Context())
	clonedRequest.Body = io.NopCloser(bytes.NewReader(rawBody))
	clonedRequest.ContentLength = int64(len(rawBody))
	if failure := h.checkAlarmSiteVisibility(request, session, route.siteID, true); failure != nil {
		emitAudit(session, auditOutcomeForStatus(failure.status), map[string]any{"siteId": route.siteID, "reasonCode": failure.code})
		fail(*failure)
		return
	}
	authorization, failStatus, allowed := h.authorizeSiteScheduleManageProblem(writer, request, session, route.siteID, spec.forbiddenDetail)
	if !allowed {
		emitAudit(session, auditOutcomeForStatus(failStatus), map[string]any{"siteId": route.siteID, "reasonCode": "FORBIDDEN"})
		return
	}
	serviceContext, signErr := h.signAlarmSiteDelegation(session, authorization, route.siteID, alarmservice.AlarmRulePublishAction)
	if signErr != nil {
		emitAudit(session, auditOutcomeFailed, map[string]any{"siteId": route.siteID, "reasonCode": "INTERNAL_ERROR"})
		fail(alarmUnavailable("The Alarm rule write context could not be signed."))
		return
	}
	respBody, status, execFailure := h.alarm.operations.Execute(request.Context(), clonedRequest, route, serviceContext)
	if execFailure != nil {
		emitAudit(session, auditOutcomeForStatus(execFailure.status), map[string]any{"siteId": route.siteID, "reasonCode": execFailure.code})
		fail(*execFailure)
		return
	}
	if status != http.StatusOK {
		upstreamFailure := alarmProblemFromUpstream(status, respBody, spec.upstreamErrorDetail)
		emitAudit(session, auditOutcomeForStatus(upstreamFailure.status), map[string]any{"siteId": route.siteID, "reasonCode": upstreamFailure.code})
		fail(upstreamFailure)
		return
	}
	var result alarmservice.SiteRulePublishResult
	if decodeStrictAlarmJSON(respBody, &result) != nil || result.BundleRevision == 0 || (alarmservice.SiteRuleList{Items: result.Items}).Validate() != nil {
		emitAudit(session, auditOutcomeFailed, map[string]any{"siteId": route.siteID, "reasonCode": "INTERNAL_ERROR"})
		fail(alarmUnavailable(spec.invalidResponseDetail))
		return
	}
	emitAudit(session, auditOutcomeSucceeded, map[string]any{
		"siteId":         route.siteID,
		"bundleRevision": result.BundleRevision,
		"source":         result.Source,
		"changed":        result.Changed,
		"added":          result.Summary.Added,
		"changedRules":   result.Summary.Changed,
		"retired":        result.Summary.Retired,
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func (h *handler) signAlarmSiteDelegation(
	session bffSession,
	authorization registryAuthorization,
	siteID string,
	action string,
) (string, error) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if !session.ExpiresAt.IsZero() && expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	policyRevision := strings.TrimSpace(authorization.policyRevision)
	if policyRevision == "" {
		policyRevision = h.identity.config.PolicyRevision
	}
	principalID := strings.TrimSpace(authorization.principalID)
	if principalID == "" {
		principalID = session.Principal.Subject
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:          principalID,
		SubjectIssuer:    session.Principal.Issuer,
		DisplayName:      session.Principal.DisplayName,
		Email:            session.Principal.Email,
		Roles:            append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:         h.alarm.backendAudience,
		TenantID:         session.TenantID,
		Actions:          []string{action},
		Scopes:           []string{"tenant:" + session.TenantID, "site:" + siteID},
		PolicyRevision:   policyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiresAt.Unix(),
		TokenID:          randomURLToken(16),
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
}

func dispatchSiteRulesSimulateRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	response, ok := executeSiteRulesGatewayCall(h, writer, request, route, http.MethodPost, "The Site rule simulation query takes exactly one siteId.", true)
	if !ok {
		return
	}
	if response.status != http.StatusOK {
		failure := alarmProblemFromUpstream(response.status, response.body, "Alarm Service could not simulate the Site rule.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	result, ok := response.value.(alarmservice.SiteRuleSimulationResult)
	if !ok {
		failure := alarmUnavailable("Alarm Service returned an invalid Site rule simulation.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func validateDeadLetterListQuery(query url.Values) (string, bool) {
	for key, values := range query {
		if len(values) != 1 || (key != "siteId" && key != "limit") {
			return "", false
		}
	}
	siteID := strings.TrimSpace(query.Get("siteId"))
	if !alarmmodel.IsUUIDv7(siteID) {
		return "", false
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > 50 {
			return "", false
		}
	}
	return siteID, true
}

func dispatchSiteRuleDeadLettersRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
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
	siteID, valid := validateDeadLetterListQuery(request.URL.Query())
	if !valid {
		fail(alarmInvalid("The Site rule dead-letter query requires siteId and optional limit between 1 and 50."))
		return
	}
	route.siteID = siteID
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
	if response.status != http.StatusOK {
		upstreamFailure := alarmProblemFromUpstream(response.status, response.body, "Alarm Service could not list Site rule dead letters.")
		fail(upstreamFailure)
		return
	}
	list, ok := response.value.(alarmservice.RuleActionDeadLetterList)
	if !ok {
		fail(alarmUnavailable("Alarm Service returned an invalid Site rule dead-letter list."))
		return
	}
	items := list.Items
	if items == nil {
		items = []alarmservice.RuleActionDeadLetterView{}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": items,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func (h *handler) authorizeSiteScheduleManageProblem(
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	siteID string,
	forbiddenDetail string,
) (registryAuthorization, int, bool) {
	authorization, failure := h.authorizeRegistry(request.Context(), session, registryauth.ActionScheduleManage)
	if failure != nil {
		if failure.status == http.StatusForbidden {
			writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", forbiddenDetail, false, nil)
			return registryAuthorization{}, http.StatusForbidden, false
		}
		if failure.status == http.StatusNotFound {
			writeProblem(writer, request, http.StatusNotFound, "SITE_NOT_FOUND", "Site not found", "The requested Site was not found.", false, nil)
			return registryAuthorization{}, http.StatusNotFound, false
		}
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return registryAuthorization{}, failure.status, false
	}
	if _, allowed := authorization.allowedSiteIDs[siteID]; !allowed {
		writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", forbiddenDetail, false, nil)
		return registryAuthorization{}, http.StatusForbidden, false
	}
	return authorization, 0, true
}

func dispatchSiteRuleDeadLetterReplayRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	targetID := "outbox:" + route.outboxID
	emitAudit := func(session bffSession, outcome string, summary map[string]any) {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeRegistryMutation,
			TargetID:      targetID,
			TargetLabel:   "告警规则动作死信重投",
			Action:        "ALARM_RULE_ACTION_DEAD_LETTER_REPLAYED",
			Outcome:       outcome,
			ChangeSummary: summary,
		})
	}
	fail := func(failure ownerFailure) {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
	}
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	if h.alarm == nil || h.alarm.operations == nil || h.identity == nil || h.identity.config.DelegationSigner == nil {
		fail(alarmUnavailable("The Alarm service is not configured."))
		return
	}
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}
	if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
		emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": failure.code})
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
		return
	}
	if !alarmmodel.IsUUIDv7(route.outboxID) {
		emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "RESOURCE_NOT_FOUND"})
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested rule action dead-letter entry was not found.", false, nil)
		return
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["siteId"]) != 1 || !alarmmodel.IsUUIDv7(query.Get("siteId")) {
		emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
		fail(alarmInvalid("The Site rule dead-letter replay query takes exactly one siteId."))
		return
	}
	route.siteID = query.Get("siteId")
	if request.Body != nil && request.ContentLength != 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "" && mediaType != "application/json" {
			emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "Invalid content type", "The replay request body must use application/json.", false, nil)
			return
		}
		rawBody, err := readBoundedBody(request.Body, 16<<10)
		if err != nil {
			emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": "INVALID_ARGUMENT"})
			fail(alarmInvalid("The replay request body is too large or unreadable."))
			return
		}
		if len(bytes.TrimSpace(rawBody)) > 0 {
			var empty struct{}
			if decodeStrictAlarmJSON(rawBody, &empty) != nil {
				emitAudit(session, auditOutcomeRejected, map[string]any{"siteId": route.siteID, "reasonCode": "INVALID_ARGUMENT"})
				fail(alarmInvalid("The replay request body must be empty or {}."))
				return
			}
		}
	}
	if failure := h.checkAlarmSiteVisibility(request, session, route.siteID, true); failure != nil {
		emitAudit(session, auditOutcomeForStatus(failure.status), map[string]any{"siteId": route.siteID, "reasonCode": failure.code})
		fail(*failure)
		return
	}
	authorization, failStatus, allowed := h.authorizeSiteScheduleManageProblem(writer, request, session, route.siteID, "The caller lacks permission to replay Site rule dead letters.")
	if !allowed {
		emitAudit(session, auditOutcomeForStatus(failStatus), map[string]any{"siteId": route.siteID, "reasonCode": "FORBIDDEN"})
		return
	}
	serviceContext, err := h.signAlarmSiteDelegation(session, authorization, route.siteID, alarmservice.AlarmRuleDeadLetterReplayAction)
	if err != nil {
		emitAudit(session, auditOutcomeFailed, map[string]any{"siteId": route.siteID, "reasonCode": "INTERNAL_ERROR"})
		fail(alarmUnavailable("The Alarm replay context could not be signed."))
		return
	}
	rawBody, status, execFailure := h.alarm.operations.Execute(request.Context(), request, route, serviceContext)
	if execFailure != nil {
		emitAudit(session, auditOutcomeForStatus(execFailure.status), map[string]any{"siteId": route.siteID, "reasonCode": execFailure.code})
		fail(*execFailure)
		return
	}
	if status != http.StatusOK {
		upstreamFailure := alarmProblemFromUpstream(status, rawBody, "Alarm Service could not replay the Site rule dead letter.")
		emitAudit(session, auditOutcomeForStatus(upstreamFailure.status), map[string]any{"siteId": route.siteID, "reasonCode": upstreamFailure.code})
		fail(upstreamFailure)
		return
	}
	var result alarmservice.RuleActionReplayResult
	if decodeStrictAlarmJSON(rawBody, &result) != nil || result.OutboxID != route.outboxID || result.SiteID != route.siteID {
		emitAudit(session, auditOutcomeFailed, map[string]any{"siteId": route.siteID, "reasonCode": "INTERNAL_ERROR"})
		fail(alarmUnavailable("Alarm Service returned an invalid Site rule replay response."))
		return
	}
	emitAudit(session, auditOutcomeSucceeded, map[string]any{
		"siteId": route.siteID,
		"status": string(result.Status),
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": result,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func validatePublicAlarmQuery(query url.Values) (string, int, bool) {
	for key := range query {
		switch key {
		case "siteId", "condition", "severity", "acknowledged", "suppressed", "activeFrom", "activeTo", "cursor", "limit":
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
	activeFrom, validFrom := parseOptionalRFC3339UTC(query, "activeFrom")
	activeTo, validTo := parseOptionalRFC3339UTC(query, "activeTo")
	if !validFrom || !validTo || (activeFrom != nil && activeTo != nil && activeFrom.After(*activeTo)) {
		return "", 0, false
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

func parseOptionalRFC3339UTC(query url.Values, key string) (*time.Time, bool) {
	raw := query.Get(key)
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, false
	}
	utc := parsed.UTC()
	return &utc, true
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
	case call.route.siteRulesDiff:
		var diff alarmservice.SiteRuleDiffResult
		if err := decodeStrictAlarmJSON(response.body, &diff); err != nil {
			return nil, err
		}
		return diff, nil
	case call.route.siteRuleRevisions:
		var revisions alarmservice.SiteRuleBundleRevisionList
		if err := decodeStrictAlarmJSON(response.body, &revisions); err != nil {
			return nil, err
		}
		return revisions, nil
	case call.route.siteRulesSimulate:
		var sim alarmservice.SiteRuleSimulationResult
		if err := decodeStrictAlarmJSON(response.body, &sim); err != nil {
			return nil, err
		}
		return sim, sim.Validate("")
	case call.route.siteRuleDeadLetters:
		var list alarmservice.RuleActionDeadLetterList
		if err := decodeStrictAlarmJSON(response.body, &list); err != nil {
			return nil, err
		}
		return list, nil
	case call.route.evaluations:
		var list alarmservice.AlarmEvaluationList
		if err := decodeStrictAlarmJSON(response.body, &list); err != nil {
			return nil, err
		}
		limit := call.limit
		if limit == 0 {
			limit = alarmservice.DefaultAlarmEvaluationsLimit
		}
		return list, list.Validate(scope.resourceID, limit)
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
	defer drainAndCloseBody(response.Body)
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

type upstreamAlarmProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func decodeUpstreamAlarmProblem(body []byte) (upstreamAlarmProblem, bool) {
	var value upstreamAlarmProblem
	if decodeStrictAlarmJSON(body, &value) != nil || value.Code == "" {
		return upstreamAlarmProblem{}, false
	}
	return value, true
}

func alarmProblemFromUpstream(status int, body []byte, fallbackDetail string) ownerFailure {
	value, ok := decodeUpstreamAlarmProblem(body)
	if !ok {
		return alarmUnavailable("Alarm Service returned an invalid error response.")
	}
	switch status {
	case http.StatusForbidden:
		return alarmDenied()
	case http.StatusNotFound:
		title := value.Title
		if strings.TrimSpace(title) == "" {
			title = "Resource not found"
		}
		detail := value.Detail
		if strings.TrimSpace(detail) == "" {
			detail = "The requested Alarm resource was not found."
		}
		return ownerFailure{status: http.StatusNotFound, code: value.Code, title: title, detail: detail}
	case http.StatusConflict:
		title := value.Title
		if strings.TrimSpace(title) == "" {
			title = "Conflict"
		}
		detail := value.Detail
		if strings.TrimSpace(detail) == "" {
			detail = "The requested Alarm resource changed concurrently."
		}
		return ownerFailure{status: http.StatusConflict, code: value.Code, title: title, detail: detail}
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusUnsupportedMediaType:
		title := value.Title
		if strings.TrimSpace(title) == "" {
			title = "Invalid argument"
		}
		detail := value.Detail
		if strings.TrimSpace(detail) == "" {
			detail = "The Alarm request is invalid."
		}
		return ownerFailure{status: status, code: value.Code, title: title, detail: detail}
	default:
		return alarmUnavailable(fallbackDetail)
	}
}

func (h *handler) forwardAlarmProblem(writer http.ResponseWriter, request *http.Request, status int, body []byte, route publicAlarmRoute) {
	value, ok := decodeUpstreamAlarmProblem(body)
	if !ok {
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

func (h *handler) alarmReferenceNow() time.Time {
	if h != nil && h.identity != nil && h.identity.now != nil {
		return h.identity.now().UTC()
	}
	return time.Now().UTC()
}

func adapterReferenceTime(route publicAlarmRoute, adapterNow func() time.Time) time.Time {
	if !route.now.IsZero() {
		return route.now.UTC()
	}
	if adapterNow != nil {
		return adapterNow().UTC()
	}
	return time.Now().UTC()
}

func validatePublicAlarmEvaluationsQuery(query url.Values) (int, bool) {
	for key, values := range query {
		if key != "limit" || len(values) != 1 {
			return 0, false
		}
	}
	limit := alarmservice.DefaultAlarmEvaluationsLimit
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > alarmservice.MaxAlarmEvaluationsLimit {
			return 0, false
		}
		limit = value
	}
	return limit, true
}

func parseAlarmLifecycleMutation(publicRequest *http.Request, action alarmauth.Action, referenceNow time.Time) (alarmservice.Mutation, []byte, *ownerFailure) {
	spec, ok := alarmLifecycleSpecs[action]
	if !ok {
		failure := alarmInvalid("Unsupported Alarm lifecycle action.")
		return alarmservice.Mutation{}, nil, &failure
	}
	invalid := func() *ownerFailure {
		failure := alarmInvalid("The Alarm " + spec.noun + " request is invalid.")
		return &failure
	}
	if publicRequest.Body == nil {
		return alarmservice.Mutation{}, nil, invalid()
	}
	body, err := readBoundedBody(publicRequest.Body, 16<<10)
	if err != nil || len(body) == 0 {
		return alarmservice.Mutation{}, nil, invalid()
	}
	var payload struct {
		ExpectedVersion uint64  `json:"expectedVersion"`
		Reason          string  `json:"reason"`
		AssigneeID      *string `json:"assigneeId"`
		SuppressedUntil *string `json:"suppressedUntil"`
	}
	if decodeStrictAlarmJSON(body, &payload) != nil {
		return alarmservice.Mutation{}, nil, invalid()
	}
	payload.Reason = strings.TrimSpace(payload.Reason)
	idempotencyKey := strings.TrimSpace(publicRequest.Header.Get("Idempotency-Key"))
	if payload.ExpectedVersion == 0 || payload.Reason == "" || len(payload.Reason) > 256 || len(idempotencyKey) < 8 || len(idempotencyKey) > 128 {
		return alarmservice.Mutation{}, nil, invalid()
	}
	mutation := alarmservice.Mutation{
		Operation:       spec.operation,
		ExpectedVersion: payload.ExpectedVersion,
		Reason:          payload.Reason,
		IdempotencyKey:  idempotencyKey,
		ActorType:       "PRINCIPAL",
		ActorID:         "principal:operator",
		PolicyRevision:  "1",
		CorrelationID:   idempotencyKey,
		OccurredAt:      referenceNow.UTC().Format(time.RFC3339Nano),
	}
	if session, ok := routeSessionFromContext(publicRequest.Context()); ok && session.Principal.Subject != "" {
		mutation.ActorID = session.Principal.Subject
	}
	switch action {
	case alarmauth.ActionAssign:
		if payload.AssigneeID == nil || payload.SuppressedUntil != nil {
			return alarmservice.Mutation{}, nil, invalid()
		}
		assigneeID := strings.TrimSpace(*payload.AssigneeID)
		if assigneeID == "" || len(assigneeID) > 256 {
			return alarmservice.Mutation{}, nil, invalid()
		}
		mutation.AssigneeID = &assigneeID
	case alarmauth.ActionSuppress:
		if payload.AssigneeID != nil || payload.SuppressedUntil == nil {
			return alarmservice.Mutation{}, nil, invalid()
		}
		suppressedUntil := strings.TrimSpace(*payload.SuppressedUntil)
		expiresAt, err := time.Parse(time.RFC3339Nano, suppressedUntil)
		if err != nil || !expiresAt.After(referenceNow.UTC()) {
			return alarmservice.Mutation{}, nil, invalid()
		}
		mutation.SuppressedUntil = &suppressedUntil
	case alarmauth.ActionUnassign, alarmauth.ActionUnsuppress:
		if payload.AssigneeID != nil || payload.SuppressedUntil != nil {
			return alarmservice.Mutation{}, nil, invalid()
		}
	default:
		return alarmservice.Mutation{}, nil, invalid()
	}
	return mutation, body, nil
}
