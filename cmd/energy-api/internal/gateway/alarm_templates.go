package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type alarmTemplateRouteKind int

const (
	alarmTmplListOrCreate alarmTemplateRouteKind = iota + 1
	alarmTmplItem
	alarmTmplReleases
	alarmTmplSites
	alarmTmplPreviewRollout
	alarmTmplRollout
	alarmTmplSiteDrift
	alarmTmplSiteReset
)

type alarmTemplateRoute struct {
	kind           alarmTemplateRouteKind
	template       string
	templateID     string
	ruleID         string
	internalPath   string
	upstreamMethod string
	isWrite        bool
	forwardBody    bool
}

func matchPublicAlarmTemplateRoute(path string) (*alarmTemplateRoute, bool) {
	if path == "/api/v1/alarm-rule-templates" {
		return &alarmTemplateRoute{
			kind:         alarmTmplListOrCreate,
			template:     "/api/v1/alarm-rule-templates",
			internalPath: "/internal/v1/alarm-rule-templates",
		}, true
	}
	if rest, ok := strings.CutPrefix(path, "/api/v1/alarm-rule-templates/"); ok && rest != "" && !strings.HasSuffix(rest, "/") {
		segments := strings.Split(rest, "/")
		tid, err := url.PathUnescape(segments[0])
		if err != nil || tid == "" {
			return nil, false
		}
		switch len(segments) {
		case 1:
			return &alarmTemplateRoute{
				kind:         alarmTmplItem,
				template:     "/api/v1/alarm-rule-templates/{templateId}",
				templateID:   tid,
				internalPath: "/internal/v1/alarm-rule-templates/" + url.PathEscape(tid),
			}, true
		case 2:
			switch segments[1] {
			case "releases":
				return &alarmTemplateRoute{
					kind:         alarmTmplReleases,
					template:     "/api/v1/alarm-rule-templates/{templateId}/releases",
					templateID:   tid,
					internalPath: "/internal/v1/alarm-rule-templates/" + url.PathEscape(tid) + "/releases",
				}, true
			case "sites":
				return &alarmTemplateRoute{
					kind:         alarmTmplSites,
					template:     "/api/v1/alarm-rule-templates/{templateId}/sites",
					templateID:   tid,
					internalPath: "/internal/v1/alarm-rule-templates/" + url.PathEscape(tid) + "/sites",
					isWrite:      true,
					forwardBody:  true,
				}, true
			case "preview-rollout":
				return &alarmTemplateRoute{
					kind:         alarmTmplPreviewRollout,
					template:     "/api/v1/alarm-rule-templates/{templateId}/preview-rollout",
					templateID:   tid,
					internalPath: "/internal/v1/alarm-rule-templates/" + url.PathEscape(tid) + ":preview-rollout",
					forwardBody:  true,
				}, true
			case "rollout":
				return &alarmTemplateRoute{
					kind:         alarmTmplRollout,
					template:     "/api/v1/alarm-rule-templates/{templateId}/rollout",
					templateID:   tid,
					internalPath: "/internal/v1/alarm-rule-templates/" + url.PathEscape(tid) + ":rollout",
					isWrite:      true,
					forwardBody:  true,
				}, true
			}
		}
		return nil, false
	}
	if path == "/api/v1/alarm-rules/template-drift" {
		return &alarmTemplateRoute{
			kind:     alarmTmplSiteDrift,
			template: "/api/v1/alarm-rules/template-drift",
		}, true
	}
	if rest, ok := strings.CutPrefix(path, "/api/v1/alarm-rules/"); ok {
		segments := strings.Split(rest, "/")
		if len(segments) == 2 && segments[0] != "" && segments[1] == "reset-template" {
			rid, err := url.PathUnescape(segments[0])
			if err == nil && rid != "" {
				return &alarmTemplateRoute{
					kind:        alarmTmplSiteReset,
					template:    "/api/v1/alarm-rules/{ruleId}/reset-template",
					ruleID:      rid,
					isWrite:     true,
					forwardBody: true,
				}, true
			}
		}
	}
	return nil, false
}

func (a *httpAlarmAdapter) executeTemplateRequest(
	callCtx context.Context,
	publicRequest *http.Request,
	tr *alarmTemplateRoute,
	serviceContext string,
) ([]byte, int, *ownerFailure) {
	upstreamURL := a.baseURL + tr.internalPath
	method := tr.upstreamMethod
	if method == "" {
		method = publicRequest.Method
	}
	if method == http.MethodGet && publicRequest.URL.RawQuery != "" {
		if tr.kind == alarmTmplSiteDrift {
			q := publicRequest.URL.Query()
			q.Del("siteId")
			if enc := q.Encode(); enc != "" {
				upstreamURL += "?" + enc
			}
		} else {
			upstreamURL += "?" + publicRequest.URL.RawQuery
		}
	}
	var bodyReader io.Reader
	if tr.forwardBody && publicRequest.Body != nil {
		raw, err := readBoundedBody(publicRequest.Body, 256<<10)
		if err != nil {
			failure := alarmInvalid("The alarm template request body is unreadable or oversized.")
			return nil, 0, &failure
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			bodyReader = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequestWithContext(callCtx, method, upstreamURL, bodyReader)
	if err != nil {
		failure := alarmUnavailable("The Alarm Service request could not be constructed.")
		return nil, 0, &failure
	}
	req.Header.Set("Accept", "application/json, application/problem+json")
	if tr.isWrite {
		req.Header.Set(alarmWriteContextHeader, serviceContext)
	} else {
		req.Header.Set(alarmReadContextHeader, serviceContext)
	}
	if tr.forwardBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Request-ID", requestIDFromContext(publicRequest.Context()))
	observability.InjectHTTP(publicRequest.Context(), req.Header)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		failure := alarmUnavailable("Alarm Service is temporarily unavailable.")
		return nil, 0, &failure
	}
	defer drainAndCloseBody(resp.Body)
	respBody, err := readBoundedBody(resp.Body, a.maxResponseBytes)
	if err != nil {
		failure := alarmUnavailable("Alarm Service returned an oversized or unreadable response.")
		return nil, 0, &failure
	}
	return respBody, resp.StatusCode, nil
}

func (h *handler) signAlarmTenantDelegation(session bffSession, authorization registryAuthorization, action string) (string, error) {
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
		Scopes:           []string{"tenant:" + session.TenantID},
		PolicyRevision:   policyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiresAt.Unix(),
		TokenID:          randomURLToken(16),
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
}

func resolveTemplateMethodAndWrite(kind alarmTemplateRouteKind, method string) (allowedMethods string, isWrite bool, forwardBody bool, ok bool) {
	switch kind {
	case alarmTmplListOrCreate:
		switch method {
		case http.MethodGet:
			return "GET, POST", false, false, true
		case http.MethodPost:
			return "GET, POST", true, true, true
		default:
			return "GET, POST", false, false, false
		}
	case alarmTmplItem:
		switch method {
		case http.MethodGet:
			return "GET, PUT", false, false, true
		case http.MethodPut:
			return "GET, PUT", true, true, true
		default:
			return "GET, PUT", false, false, false
		}
	case alarmTmplReleases:
		switch method {
		case http.MethodGet:
			return "GET, POST", false, false, true
		case http.MethodPost:
			return "GET, POST", true, true, true
		default:
			return "GET, POST", false, false, false
		}
	case alarmTmplSites:
		return http.MethodPut, true, true, method == http.MethodPut
	case alarmTmplPreviewRollout:
		return http.MethodPost, false, true, method == http.MethodPost
	case alarmTmplRollout:
		return http.MethodPost, true, true, method == http.MethodPost
	case alarmTmplSiteDrift:
		return http.MethodGet, false, false, method == http.MethodGet
	case alarmTmplSiteReset:
		return http.MethodPost, true, true, method == http.MethodPost
	}
	return "", false, false, false
}

func extractTargetSiteIDsFromPayload(kind alarmTemplateRouteKind, raw []byte) ([]string, error) {
	switch kind {
	case alarmTmplListOrCreate:
		var req alarmservice.CreateRuleTemplateRequest
		if decodeStrictAlarmJSON(raw, &req) != nil {
			return nil, fmt.Errorf("invalid create template body")
		}
		return req.BoundSiteIDs, nil
	case alarmTmplSites:
		var req alarmservice.UpdateRuleTemplateSitesRequest
		if decodeStrictAlarmJSON(raw, &req) != nil {
			return nil, fmt.Errorf("invalid template sites body")
		}
		return req.SiteIDs, nil
	case alarmTmplPreviewRollout:
		var req alarmservice.PreviewRuleTemplateRolloutRequest
		if decodeStrictAlarmJSON(raw, &req) != nil {
			return nil, fmt.Errorf("invalid preview rollout body")
		}
		out := append([]string(nil), req.SiteIDs...)
		for _, t := range req.Targets {
			out = append(out, t.SiteID)
		}
		return out, nil
	case alarmTmplRollout:
		var req alarmservice.ExecuteRuleTemplateRolloutRequest
		if decodeStrictAlarmJSON(raw, &req) != nil {
			return nil, fmt.Errorf("invalid rollout body")
		}
		out := append([]string(nil), req.SiteIDs...)
		for _, t := range req.Targets {
			out = append(out, t.SiteID)
		}
		return out, nil
	}
	return nil, nil
}

func dispatchAlarmTemplateRoute(h *handler, writer http.ResponseWriter, request *http.Request, route publicAlarmRoute) {
	tr := *route.templateRoute
	allow, isWrite, forwardBody, methodOK := resolveTemplateMethodAndWrite(tr.kind, request.Method)
	if !methodOK {
		writeMethodNotAllowedFor(writer, request, allow)
		return
	}
	tr.isWrite = isWrite
	tr.forwardBody = forwardBody
	tr.upstreamMethod = request.Method

	if h.alarm == nil || h.alarm.operations == nil || h.identity == nil || h.identity.config.DelegationSigner == nil {
		failure := alarmUnavailable("The Alarm service is not configured.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}

	if tr.kind == alarmTmplSiteDrift || tr.kind == alarmTmplSiteReset {
		dispatchAlarmTemplateSiteRoute(h, writer, request, session, tr)
		return
	}

	if tr.templateID != "" && !alarmmodel.IsUUIDv7(tr.templateID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule template was not found.", false, nil)
		return
	}

	targetID := "alarm-templates"
	if tr.templateID != "" {
		targetID = "alarm-template:" + tr.templateID
	}
	auditAction := "ALARM_TEMPLATE_MUTATION"
	switch tr.kind {
	case alarmTmplListOrCreate:
		auditAction = "ALARM_RULE_TEMPLATE_CREATED"
	case alarmTmplItem:
		auditAction = "ALARM_RULE_TEMPLATE_UPDATED"
	case alarmTmplReleases:
		auditAction = "ALARM_RULE_TEMPLATE_RELEASED"
	case alarmTmplSites:
		auditAction = "ALARM_RULE_TEMPLATE_SITES_BOUND"
	case alarmTmplRollout:
		auditAction = "ALARM_RULE_TEMPLATE_ROLLED_OUT"
	}
	emitAudit := func(outcome string, summary map[string]any) {
		if !isWrite {
			return
		}
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeRegistryMutation,
			TargetID:      targetID,
			TargetLabel:   "跨站点告警规则模板",
			Action:        auditAction,
			Outcome:       outcome,
			ChangeSummary: summary,
		})
	}

	var rawBody []byte
	if isWrite {
		if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
			emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": failure.code})
			writeIdentityFailure(writer, request, *failure)
			return
		}
		if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
			return
		}
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
			emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "Invalid content type", "Alarm template writes require application/json.", false, nil)
			return
		}
	}
	if forwardBody {
		if request.Body == nil {
			emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Request body is required.", false, nil)
			return
		}
		var err error
		rawBody, err = readBoundedBody(request.Body, 256<<10)
		if err != nil || len(bytes.TrimSpace(rawBody)) == 0 {
			emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Request body is empty or oversized.", false, nil)
			return
		}
	}

	var authorization registryAuthorization
	var authFailure *registryAuthorizationFailure
	if isWrite {
		authorization, authFailure = h.authorizeRegistry(request.Context(), session, registryauth.ActionScheduleManage)
	} else {
		authorization, authFailure = h.authorizeRegistryCandidates(request.Context(), session, registryauth.ActionSiteList, registryauth.ActionScheduleManage)
	}
	if authFailure != nil {
		emitAudit(auditOutcomeForStatus(authFailure.status), map[string]any{"reasonCode": authFailure.code})
		writeProblem(writer, request, authFailure.status, authFailure.code, authFailure.title, authFailure.detail, authFailure.retryable, nil)
		return
	}

	// Enforce per-site authorization/visibility on every target or bound site before rollout/preview/binding.
	targetSites, parseErr := extractTargetSiteIDsFromPayload(tr.kind, rawBody)
	if parseErr != nil {
		emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Alarm template request payload is malformed.", false, nil)
		return
	}
	if (tr.kind == alarmTmplRollout || tr.kind == alarmTmplPreviewRollout) && len(targetSites) == 0 {
		// Fetch stored boundSiteIds so authorization still checks every site this rollout touches.
		bound, fetchErr := h.fetchTemplateBoundSiteIDs(request.Context(), request, session, authorization, tr.templateID)
		if fetchErr != nil {
			emitAudit(auditOutcomeForStatus(fetchErr.status), map[string]any{"reasonCode": fetchErr.code})
			writeProblem(writer, request, fetchErr.status, fetchErr.code, fetchErr.title, fetchErr.detail, fetchErr.retryable, nil)
			return
		}
		targetSites = bound
	}
	for _, sid := range targetSites {
		trimmed := strings.TrimSpace(sid)
		if !alarmmodel.IsUUIDv7(trimmed) {
			emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT", "siteId": trimmed})
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Target siteId must be a valid UUIDv7.", false, nil)
			return
		}
		if visFail := h.checkAlarmSiteVisibility(request, session, trimmed, true); visFail != nil {
			if visFail.status == http.StatusNotFound {
				// Return 403 on cross-site authorization probe to keep multi-site rollout boundary strict.
				emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "FORBIDDEN", "siteId": trimmed})
				writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", "Target site is outside the caller's authorized scope.", false, nil)
				return
			}
			emitAudit(auditOutcomeForStatus(visFail.status), map[string]any{"reasonCode": visFail.code, "siteId": trimmed})
			writeProblem(writer, request, visFail.status, visFail.code, visFail.title, visFail.detail, visFail.retryable, nil)
			return
		}
		if isWrite {
			if _, ok := authorization.allowedSiteIDs[trimmed]; !ok {
				emitAudit(auditOutcomeRejected, map[string]any{"reasonCode": "FORBIDDEN", "siteId": trimmed})
				writeProblem(writer, request, http.StatusForbidden, "FORBIDDEN", "Forbidden", "The caller lacks permission to modify alarm rules on target site.", false, nil)
				return
			}
		}
	}

	action := alarmservice.AlarmListAction
	if isWrite {
		action = alarmservice.AlarmRulePublishAction
	}
	serviceContext, signErr := h.signAlarmTenantDelegation(session, authorization, action)
	if signErr != nil {
		emitAudit(auditOutcomeFailed, map[string]any{"reasonCode": "INTERNAL_ERROR"})
		failure := alarmUnavailable("The Alarm template delegation context could not be signed.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}

	cloned := request.Clone(request.Context())
	if forwardBody {
		cloned.Body = io.NopCloser(bytes.NewReader(rawBody))
		cloned.ContentLength = int64(len(rawBody))
	}
	upstreamRoute := route
	upstreamRoute.templateRoute = &tr
	respBody, status, execFail := h.alarm.operations.Execute(request.Context(), cloned, upstreamRoute, serviceContext)
	if execFail != nil {
		emitAudit(auditOutcomeForStatus(execFail.status), map[string]any{"reasonCode": execFail.code})
		writeProblem(writer, request, execFail.status, execFail.code, execFail.title, execFail.detail, execFail.retryable, nil)
		return
	}
	if status != http.StatusOK && status != http.StatusCreated {
		upFail := alarmProblemFromUpstream(status, respBody, "Alarm Service could not complete the template operation.")
		emitAudit(auditOutcomeForStatus(upFail.status), map[string]any{"reasonCode": upFail.code})
		writeProblem(writer, request, upFail.status, upFail.code, upFail.title, upFail.detail, upFail.retryable, nil)
		return
	}

	decoded := json.RawMessage(bytes.TrimSpace(respBody))
	emitAudit(auditOutcomeSucceeded, map[string]any{
		"templateId":  tr.templateID,
		"targetSites": len(targetSites),
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, status, map[string]any{
		"data": decoded,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func (h *handler) fetchTemplateBoundSiteIDs(
	ctx context.Context,
	publicReq *http.Request,
	session bffSession,
	authorization registryAuthorization,
	templateID string,
) ([]string, *ownerFailure) {
	ctxTok, err := h.signAlarmTenantDelegation(session, authorization, alarmservice.AlarmListAction)
	if err != nil {
		f := alarmUnavailable("The Alarm template read context could not be signed.")
		return nil, &f
	}
	internalReq := publicReq.Clone(ctx)
	internalReq.Method = http.MethodGet
	internalReq.Body = nil
	internalReq.ContentLength = 0
	tr := alarmTemplateRoute{
		kind:           alarmTmplItem,
		internalPath:   "/internal/v1/alarm-rule-templates/" + url.PathEscape(templateID),
		upstreamMethod: http.MethodGet,
	}
	body, status, execFail := h.alarm.operations.Execute(ctx, internalReq, publicAlarmRoute{templateRoute: &tr}, ctxTok)
	if execFail != nil {
		return nil, execFail
	}
	if status != http.StatusOK {
		f := alarmProblemFromUpstream(status, body, "Alarm template could not be resolved.")
		return nil, &f
	}
	var detail alarmservice.RuleBaselineTemplateDetail
	if json.Unmarshal(body, &detail) != nil {
		f := alarmUnavailable("Alarm Service returned an invalid template detail response.")
		return nil, &f
	}
	return detail.Template.BoundSiteIDs, nil
}

func dispatchAlarmTemplateSiteRoute(
	h *handler,
	writer http.ResponseWriter,
	request *http.Request,
	session bffSession,
	tr alarmTemplateRoute,
) {
	siteID := strings.TrimSpace(request.URL.Query().Get("siteId"))
	if !alarmmodel.IsUUIDv7(siteID) || len(request.URL.Query()["siteId"]) != 1 || len(request.URL.Query()) != 1 {
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "This Alarm rule template query requires exactly one UUIDv7 siteId.", false, nil)
		return
	}
	if tr.kind == alarmTmplSiteReset && !alarmmodel.IsUUIDv7(tr.ruleID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule was not found.", false, nil)
		return
	}
	isWrite := tr.kind == alarmTmplSiteReset
	emitAudit := func(outcome string, summary map[string]any) {
		if !isWrite {
			return
		}
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeRegistryMutation,
			TargetID:      fmt.Sprintf("site:%s:rule:%s", siteID, tr.ruleID),
			TargetLabel:   "单站告警规则重置为模板基线",
			Action:        "ALARM_RULE_TEMPLATE_RESET",
			Outcome:       outcome,
			ChangeSummary: summary,
		})
	}

	var rawBody []byte
	if isWrite {
		if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
			emitAudit(auditOutcomeRejected, map[string]any{"siteId": siteID, "reasonCode": failure.code})
			writeIdentityFailure(writer, request, *failure)
			return
		}
		if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionCommandWrite, session.TenantID) {
			return
		}
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
			emitAudit(auditOutcomeRejected, map[string]any{"siteId": siteID, "reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "Invalid content type", "Template reset requests require application/json.", false, nil)
			return
		}
		if request.Body != nil {
			var err error
			rawBody, err = readBoundedBody(request.Body, 16<<10)
			if err != nil {
				emitAudit(auditOutcomeRejected, map[string]any{"siteId": siteID, "reasonCode": "INVALID_ARGUMENT"})
				writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Template reset request body is oversized or unreadable.", false, nil)
				return
			}
		}
		if len(bytes.TrimSpace(rawBody)) == 0 {
			rawBody = []byte("{}")
		}
	}

	if failure := h.checkAlarmSiteVisibility(request, session, siteID, true); failure != nil {
		emitAudit(auditOutcomeForStatus(failure.status), map[string]any{"siteId": siteID, "reasonCode": failure.code})
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}

	var serviceContext string
	var signErr error
	if isWrite {
		authorization, failStatus, allowed := h.authorizeSiteScheduleManageProblem(writer, request, session, siteID, "The caller lacks permission to reset Site alarm rules.")
		if !allowed {
			emitAudit(auditOutcomeForStatus(failStatus), map[string]any{"siteId": siteID, "reasonCode": "FORBIDDEN"})
			return
		}
		serviceContext, signErr = h.signAlarmSiteDelegation(session, authorization, siteID, alarmservice.AlarmRulePublishAction)
		tr.internalPath = "/internal/v1/sites/" + url.PathEscape(siteID) + "/alarm-rules/" + url.PathEscape(tr.ruleID) + ":reset-template"
	} else {
		authorization, authFailure := h.authorizeRegistryCandidates(request.Context(), session, registryauth.ActionSiteList, registryauth.ActionScheduleManage)
		if authFailure != nil {
			writeProblem(writer, request, authFailure.status, authFailure.code, authFailure.title, authFailure.detail, authFailure.retryable, nil)
			return
		}
		serviceContext, signErr = h.signAlarmSiteDelegation(session, authorization, siteID, alarmservice.AlarmListAction)
		tr.internalPath = "/internal/v1/sites/" + url.PathEscape(siteID) + "/alarm-rules/template-drift"
	}
	if signErr != nil {
		emitAudit(auditOutcomeFailed, map[string]any{"siteId": siteID, "reasonCode": "INTERNAL_ERROR"})
		failure := alarmUnavailable("The Alarm site template delegation could not be signed.")
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}

	cloned := request.Clone(request.Context())
	if isWrite {
		cloned.Body = io.NopCloser(bytes.NewReader(rawBody))
		cloned.ContentLength = int64(len(rawBody))
	}
	respBody, status, execFail := h.alarm.operations.Execute(request.Context(), cloned, publicAlarmRoute{siteID: siteID, templateRoute: &tr}, serviceContext)
	if execFail != nil {
		emitAudit(auditOutcomeForStatus(execFail.status), map[string]any{"siteId": siteID, "reasonCode": execFail.code})
		writeProblem(writer, request, execFail.status, execFail.code, execFail.title, execFail.detail, execFail.retryable, nil)
		return
	}
	if status != http.StatusOK {
		upFail := alarmProblemFromUpstream(status, respBody, "Alarm Service could not complete the site template operation.")
		emitAudit(auditOutcomeForStatus(upFail.status), map[string]any{"siteId": siteID, "reasonCode": upFail.code})
		writeProblem(writer, request, upFail.status, upFail.code, upFail.title, upFail.detail, upFail.retryable, nil)
		return
	}

	emitAudit(auditOutcomeSucceeded, map[string]any{"siteId": siteID, "ruleId": tr.ruleID})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": json.RawMessage(bytes.TrimSpace(respBody)),
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}
