package alarmservice

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func (handler *httpHandler) handleRuleTemplateHTTP(writer http.ResponseWriter, request *http.Request) bool {
	path := request.URL.Path
	if siteID, ruleID, ok := matchSiteRuleResetTemplatePath(path); ok {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Template reset only supports POST.", false)
			return true
		}
		if !alarmmodel.IsUUIDv7(siteID) || !alarmmodel.IsUUIDv7(ruleID) {
			handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule was not found.", false)
			return true
		}
		claims, authorized := handler.authorize(request, AlarmWriteContextHeader, AlarmRulePublishAction, []string{"site:" + siteID})
		if !authorized {
			handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "The template reset request is outside the authorized write scope.", false)
			return true
		}
		handler.handleResetSiteRuleToTemplate(writer, request, claims.TenantID, siteID, ruleID, claims.Subject, claims.DisplayName)
		return true
	}

	if siteID, ok := matchSiteRuleTemplateDriftPath(path); ok {
		if claims, ok := handler.authorizeSiteRuleRoute(writer, request, siteID, http.MethodGet, "Template drift inspection only supports GET.", "The Site template drift inspection is outside the authorized read scope."); ok {
			handler.handleGetSiteRuleTemplateDrift(writer, request, claims.TenantID, siteID)
		}
		return true
	}

	if path == "/internal/v1/alarm-rule-templates" {
		switch request.Method {
		case http.MethodGet:
			claims, authorized := handler.authorize(request, AlarmReadContextHeader, AlarmListAction, nil)
			if !authorized {
				handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Alarm rule template list is not authorized.", false)
				return true
			}
			handler.handleListRuleTemplates(writer, request, claims.TenantID)
		case http.MethodPost:
			claims, authorized := handler.authorize(request, AlarmWriteContextHeader, AlarmRulePublishAction, nil)
			if !authorized {
				handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Alarm rule template creation is not authorized.", false)
				return true
			}
			handler.handleCreateRuleTemplate(writer, request, claims.TenantID, claims.Subject, claims.DisplayName)
		default:
			writer.Header().Set("Allow", "GET, POST")
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Alarm rule templates support GET and POST.", false)
		}
		return true
	}

	templateID, tail, matched := matchInternalTemplateRoute(path)
	if !matched {
		return false
	}
	if !alarmmodel.IsUUIDv7(templateID) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Alarm rule template was not found.", false)
		return true
	}

	switch tail {
	case "":
		switch request.Method {
		case http.MethodGet:
			claims, ok := handler.authorize(request, AlarmReadContextHeader, AlarmListAction, nil)
			if !ok {
				handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Alarm rule template read is not authorized.", false)
				return true
			}
			handler.handleGetRuleTemplate(writer, request, claims.TenantID, templateID)
		case http.MethodPut:
			claims, ok := handler.authorize(request, AlarmWriteContextHeader, AlarmRulePublishAction, nil)
			if !ok {
				handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Alarm rule template update is not authorized.", false)
				return true
			}
			handler.handleUpdateRuleTemplate(writer, request, claims.TenantID, templateID, claims.Subject, claims.DisplayName)
		default:
			writer.Header().Set("Allow", "GET, PUT")
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Alarm rule template item supports GET and PUT.", false)
		}
	case "releases":
		switch request.Method {
		case http.MethodGet:
			claims, ok := handler.authorize(request, AlarmReadContextHeader, AlarmListAction, nil)
			if !ok {
				handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Alarm rule template releases read is not authorized.", false)
				return true
			}
			handler.handleListRuleTemplateReleases(writer, request, claims.TenantID, templateID)
		case http.MethodPost:
			claims, ok := handler.authorize(request, AlarmWriteContextHeader, AlarmRulePublishAction, nil)
			if !ok {
				handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Alarm rule template release publish is not authorized.", false)
				return true
			}
			handler.handlePublishRuleTemplateRelease(writer, request, claims.TenantID, templateID, claims.Subject, claims.DisplayName)
		default:
			writer.Header().Set("Allow", "GET, POST")
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Alarm rule template releases support GET and POST.", false)
		}
	case "sites":
		if request.Method != http.MethodPut {
			writer.Header().Set("Allow", http.MethodPut)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Template bound sites only support PUT.", false)
			return true
		}
		claims, ok := handler.authorize(request, AlarmWriteContextHeader, AlarmRulePublishAction, nil)
		if !ok {
			handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Template site binding update is not authorized.", false)
			return true
		}
		handler.handleUpdateRuleTemplateSites(writer, request, claims.TenantID, templateID, claims.Subject)
	case ":preview-rollout":
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Template rollout preview only supports POST.", false)
			return true
		}
		claims, ok := handler.authorize(request, AlarmReadContextHeader, AlarmListAction, nil)
		if !ok {
			handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Template rollout preview is not authorized.", false)
			return true
		}
		handler.handlePreviewRuleTemplateRollout(writer, request, claims.TenantID, templateID)
	case ":rollout":
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Template rollout only supports POST.", false)
			return true
		}
		claims, ok := handler.authorize(request, AlarmWriteContextHeader, AlarmRulePublishAction, nil)
		if !ok {
			handler.writeProblem(writer, http.StatusForbidden, "ALARM_ACCESS_DENIED", "Alarm access denied", "Template rollout is not authorized.", false)
			return true
		}
		handler.handleExecuteRuleTemplateRollout(writer, request, claims.TenantID, templateID, claims.Subject, claims.DisplayName)
	default:
		return false
	}
	return true
}

func matchSiteRuleTemplateDriftPath(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 6 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules" || segments[5] != "template-drift" {
		return "", false
	}
	sid, err := url.PathUnescape(segments[3])
	if err != nil {
		return "", false
	}
	return sid, true
}

func matchSiteRuleResetTemplatePath(path string) (string, string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 6 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules" {
		return "", "", false
	}
	ruleID, ok := strings.CutSuffix(segments[5], ":reset-template")
	if !ok || ruleID == "" {
		return "", "", false
	}
	sid, err1 := url.PathUnescape(segments[3])
	rid, err2 := url.PathUnescape(ruleID)
	if err1 != nil || err2 != nil {
		return "", "", false
	}
	return sid, rid, true
}

func matchInternalTemplateRoute(path string) (string, string, bool) {
	rest, ok := strings.CutPrefix(path, "/internal/v1/alarm-rule-templates/")
	if !ok || rest == "" {
		return "", "", false
	}
	if rawID, has := strings.CutSuffix(rest, ":preview-rollout"); has && !strings.Contains(rawID, "/") {
		id, err := url.PathUnescape(rawID)
		if err != nil {
			return "", "", false
		}
		return id, ":preview-rollout", true
	}
	if rawID, has := strings.CutSuffix(rest, ":rollout"); has && !strings.Contains(rawID, "/") {
		id, err := url.PathUnescape(rawID)
		if err != nil {
			return "", "", false
		}
		return id, ":rollout", true
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] != "" {
		id, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", "", false
		}
		return id, "", true
	}
	if len(parts) == 2 && parts[0] != "" && (parts[1] == "releases" || parts[1] == "sites") {
		id, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", "", false
		}
		return id, parts[1], true
	}
	return "", "", false
}

func decodeStrictTemplateRequest(writer http.ResponseWriter, request *http.Request, dst any) bool {
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRuleBundleBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst) == nil && ensureJSONEOF(decoder) == nil
}

func (handler *httpHandler) writeTemplateError(writer http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule template or rule was not found.", false)
		return
	}
	if errors.Is(err, ErrRuleTemplateConflict) || errors.Is(err, ErrRuleBundleConflict) {
		handler.writeProblem(writer, http.StatusConflict, "ALARM_TEMPLATE_CONFLICT", "Alarm template conflict", "A rule baseline template conflict occurred.", false)
		return
	}
	var tmplErr *RuleTemplateValidationError
	if errors.As(err, &tmplErr) {
		handler.writeProblem(writer, http.StatusUnprocessableEntity, tmplErr.Code, "Alarm template invalid", tmplErr.Message, false)
		return
	}
	var draftErr *RuleDraftValidationError
	if errors.As(err, &draftErr) {
		handler.writeProblem(writer, http.StatusUnprocessableEntity, draftErr.Code, "Alarm rule draft invalid", draftErr.Error(), false)
		return
	}
	handler.writeStoreFailure(writer, err)
}

func (handler *httpHandler) handleListRuleTemplates(writer http.ResponseWriter, request *http.Request, tenantID string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	items, err := handler.templates.ListRuleTemplates(ctx, tenantID, request.URL.Query().Get("status"))
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	if items == nil {
		items = []RuleBaselineTemplate{}
	}
	writeJSON(writer, http.StatusOK, RuleBaselineTemplateList{Items: items})
}

func (handler *httpHandler) handleGetRuleTemplate(writer http.ResponseWriter, request *http.Request, tenantID, templateID string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	detail, err := handler.templates.GetRuleTemplate(ctx, tenantID, templateID)
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	if detail.Releases == nil {
		detail.Releases = []RuleBaselineTemplateRelease{}
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (handler *httpHandler) handleCreateRuleTemplate(writer http.ResponseWriter, request *http.Request, tenantID, actorID, actorName string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input CreateRuleTemplateRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The rule template create payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	detail, err := handler.templates.CreateRuleTemplate(ctx, tenantID, input, actorID, actorName, handler.now().UTC())
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, detail)
}

func (handler *httpHandler) handleUpdateRuleTemplate(writer http.ResponseWriter, request *http.Request, tenantID, templateID, actorID, actorName string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input UpdateRuleTemplateRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The rule template update payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	tmpl, err := handler.templates.UpdateRuleTemplate(ctx, tenantID, templateID, input, actorID, actorName, handler.now().UTC())
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, tmpl)
}

func (handler *httpHandler) handleListRuleTemplateReleases(writer http.ResponseWriter, request *http.Request, tenantID, templateID string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	limit, ok := parseQueryLimit(request, defaultRuleBundleRevisionLimit, maxTemplateReleaseListLim)
	if !ok {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_FILTER_INVALID", "Alarm filter invalid", "Invalid template release limit query.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	items, err := handler.templates.ListRuleTemplateReleases(ctx, tenantID, templateID, limit)
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	if items == nil {
		items = []RuleBaselineTemplateRelease{}
	}
	writeJSON(writer, http.StatusOK, RuleBaselineTemplateReleaseList{Items: items})
}

func (handler *httpHandler) handlePublishRuleTemplateRelease(writer http.ResponseWriter, request *http.Request, tenantID, templateID, actorID, actorName string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input PublishRuleTemplateReleaseRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The rule template release payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	rel, err := handler.templates.PublishRuleTemplateRelease(ctx, tenantID, templateID, input, actorID, actorName, handler.now().UTC())
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, rel)
}

func (handler *httpHandler) handleUpdateRuleTemplateSites(writer http.ResponseWriter, request *http.Request, tenantID, templateID, actorID string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input UpdateRuleTemplateSitesRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The bound site list payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	tmpl, err := handler.templates.UpdateRuleTemplateSites(ctx, tenantID, templateID, input.SiteIDs, actorID, handler.now().UTC())
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, tmpl)
}

func (handler *httpHandler) handlePreviewRuleTemplateRollout(writer http.ResponseWriter, request *http.Request, tenantID, templateID string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input PreviewRuleTemplateRolloutRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The rollout preview payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	preview, err := handler.templates.PreviewRuleTemplateRollout(ctx, tenantID, templateID, input)
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, preview)
}

func (handler *httpHandler) handleExecuteRuleTemplateRollout(writer http.ResponseWriter, request *http.Request, tenantID, templateID, actorID, actorName string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input ExecuteRuleTemplateRolloutRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The rollout request payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	res, err := handler.templates.ExecuteRuleTemplateRollout(ctx, tenantID, templateID, input, actorID, actorName, handler.now().UTC())
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, res)
}

func (handler *httpHandler) handleGetSiteRuleTemplateDrift(writer http.ResponseWriter, request *http.Request, tenantID, siteID string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	report, err := handler.templates.GetSiteRuleTemplateDrift(ctx, tenantID, siteID)
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, report)
}

func (handler *httpHandler) handleResetSiteRuleToTemplate(writer http.ResponseWriter, request *http.Request, tenantID, siteID, ruleID, actorID, actorName string) {
	if handler.templates == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	var input ResetSiteRuleToTemplateRequest
	if !decodeStrictTemplateRequest(writer, request, &input) {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The reset-to-template payload is invalid.", false)
		return
	}
	ctx := identitycontext.WithTenantID(request.Context(), tenantID)
	res, err := handler.templates.ResetSiteRuleToTemplate(ctx, tenantID, siteID, ruleID, input, actorID, actorName, handler.now().UTC())
	if err != nil {
		handler.writeTemplateError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, res)
}
