package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

func matchPublicAlarmRoutingRoute(path string) (siteID string, isReload bool, matched bool) {
	remainder, ok := strings.CutPrefix(path, "/api/v1/sites/")
	if !ok || strings.HasSuffix(remainder, "/") {
		return "", false, false
	}
	segments := strings.Split(remainder, "/")
	if len(segments) == 2 && segments[1] == "alarm-routing" && segments[0] != "" {
		decoded, err := url.PathUnescape(segments[0])
		if err == nil && decoded != "" {
			return decoded, false, true
		}
	}
	if len(segments) == 3 && segments[1] == "alarm-routing" && segments[2] == "reload" && segments[0] != "" {
		decoded, err := url.PathUnescape(segments[0])
		if err == nil && decoded != "" {
			return decoded, true, true
		}
	}
	return "", false, false
}

func dispatchAlarmRoutingRoute(h *handler, writer http.ResponseWriter, request *http.Request, siteID string, isReload bool) {
	if isReload {
		h.postSiteAlarmRoutingReload(writer, request, siteID)
		return
	}
	h.getSiteAlarmRouting(writer, request, siteID)
}

func (h *handler) getSiteAlarmRouting(writer http.ResponseWriter, request *http.Request, siteID string) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowedFor(writer, request, http.MethodGet)
		return
	}
	if len(request.URL.Query()) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The Site alarm routing query does not accept query parameters.", false, nil)
		return
	}
	if !alarmmodel.IsUUIDv7(siteID) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if h.notification == nil || h.notification.baseURL == "" || h.notification.httpClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Alarm routing inspection is not configured.", true, nil)
		return
	}
	session, ok := h.alarmSession(writer, request)
	if !ok {
		return
	}
	if failure := h.checkAlarmSiteVisibility(request, session, siteID, true); failure != nil {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	if _, failure := h.decideAlarm(request.Context(), h.alarmCaller(session), string(alarmmodel.ActionRead), ownerScope{tenantID: session.TenantID, siteID: siteID}); failure != nil {
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	internalPath := notificationservice.InternalSiteAlarmRoutingPrefix + url.PathEscape(siteID) + "/alarm-routing"
	status, rawBody, err := h.callSiteNotificationBackend(request.Context(), session, http.MethodGet, internalPath, notificationservice.AlarmRoutingReadAction, siteID)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Alarm routing summary is temporarily unavailable.", true, nil)
		return
	}
	if status != http.StatusOK {
		writeUpstreamNotificationProblem(writer, request, status, rawBody, "Alarm routing summary could not be loaded.")
		return
	}
	var summary notificationservice.SiteRoutingSummaryView
	if decodeNotificationJSON(rawBody, &summary) != nil || summary.SiteID != siteID {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service returned an invalid alarm routing summary.", true, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": summary,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func (h *handler) postSiteAlarmRoutingReload(writer http.ResponseWriter, request *http.Request, siteID string) {
	targetID := "site:" + siteID
	emitAudit := func(session bffSession, outcome string, summary map[string]any) {
		h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
			Category:      auditserver.AggregateTypeRegistryMutation,
			TargetID:      targetID,
			TargetLabel:   "站点告警路由热重载",
			Action:        "SITE_ALARM_ROUTING_RELOADED",
			Outcome:       outcome,
			ChangeSummary: summary,
		})
	}
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return
	}
	if h.identity == nil || h.identity.config.DelegationSigner == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Session validation is unavailable.", true, nil)
		return
	}
	if h.notification == nil || h.notification.baseURL == "" || h.notification.httpClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Alarm routing reload is not configured.", true, nil)
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
	if !alarmmodel.IsUUIDv7(siteID) {
		emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "RESOURCE_NOT_FOUND"})
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Site was not found.", false, nil)
		return
	}
	if len(request.URL.Query()) != 0 {
		emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
		writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The Site alarm routing reload route does not accept query parameters.", false, nil)
		return
	}
	if request.Body != nil && request.ContentLength != 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "" && mediaType != "application/json" {
			emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "Invalid content type", "The reload request body must use application/json.", false, nil)
			return
		}
		rawBody, err := readBoundedBody(request.Body, 16<<10)
		if err != nil {
			emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The reload request body is too large or unreadable.", false, nil)
			return
		}
		if len(bytes.TrimSpace(rawBody)) > 0 {
			var empty struct{}
			if decodeNotificationJSON(rawBody, &empty) != nil {
				emitAudit(session, auditOutcomeRejected, map[string]any{"reasonCode": "INVALID_ARGUMENT"})
				writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The reload request body must be empty or {}.", false, nil)
				return
			}
		}
	}
	if failure := h.checkAlarmSiteVisibility(request, session, siteID, true); failure != nil {
		emitAudit(session, auditOutcomeForStatus(failure.status), map[string]any{"reasonCode": failure.code})
		writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
		return
	}
	if _, failStatus, allowed := h.authorizeSiteScheduleManageProblem(writer, request, session, siteID, "The caller lacks permission to reload Site alarm routing."); !allowed {
		emitAudit(session, auditOutcomeForStatus(failStatus), map[string]any{"reasonCode": "FORBIDDEN"})
		return
	}
	internalPath := notificationservice.InternalSiteAlarmRoutingPrefix + url.PathEscape(siteID) + "/alarm-routing:reload"
	status, rawBody, err := h.callSiteNotificationBackend(request.Context(), session, http.MethodPost, internalPath, notificationservice.AlarmRoutingManageAction, siteID)
	if err != nil {
		emitAudit(session, auditOutcomeFailed, map[string]any{"reasonCode": "NOTIFICATION_UNAVAILABLE"})
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Alarm routing reload is temporarily unavailable.", true, nil)
		return
	}
	if status != http.StatusOK {
		emitAudit(session, auditOutcomeForStatus(status), map[string]any{"reasonCode": problemCodeFromRaw(rawBody)})
		writeUpstreamNotificationProblem(writer, request, status, rawBody, "Alarm routing reload failed.")
		return
	}
	var resp notificationservice.AlarmRoutingReloadResponse
	if decodeNotificationJSON(rawBody, &resp) != nil || resp.SiteID != siteID {
		emitAudit(session, auditOutcomeFailed, map[string]any{"reasonCode": "NOTIFICATION_UNAVAILABLE"})
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service returned an invalid alarm routing reload response.", true, nil)
		return
	}
	emitAudit(session, auditOutcomeSucceeded, map[string]any{
		"changed":      resp.Changed,
		"configDigest": resp.ConfigDigest,
		"reloadCount":  resp.ReloadCount,
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, map[string]any{
		"data": resp,
		"meta": map[string]any{"requestId": requestIDFromContext(request.Context())},
	})
}

func (h *handler) callSiteNotificationBackend(ctx context.Context, session bffSession, method, internalPath, action, siteID string) (int, []byte, error) {
	token, err := h.signSiteNotificationContext(session, action, siteID)
	if err != nil {
		return 0, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, h.notification.timeout)
	defer cancel()
	outbound, err := http.NewRequestWithContext(callCtx, method, strings.TrimRight(h.notification.baseURL, "/")+internalPath, nil)
	if err != nil {
		return 0, nil, err
	}
	outbound.Header.Set("Accept", "application/json, application/problem+json")
	outbound.Header.Set(notificationservice.NotificationContextHeader, token)
	outbound.Header.Set("X-Request-ID", requestIDFromContext(ctx))
	observability.InjectHTTP(ctx, outbound.Header)
	response, err := h.notification.httpClient.Do(outbound)
	if err != nil {
		return 0, nil, err
	}
	defer drainAndCloseBody(response.Body)
	reply, err := io.ReadAll(io.LimitReader(response.Body, h.notification.maxResponseBytes))
	return response.StatusCode, reply, err
}

func writeUpstreamNotificationProblem(writer http.ResponseWriter, request *http.Request, status int, rawBody []byte, fallbackDetail string) {
	var upstream struct {
		Code      string `json:"code"`
		Title     string `json:"title"`
		Detail    string `json:"detail"`
		Retryable bool   `json:"retryable"`
	}
	if json.Unmarshal(rawBody, &upstream) == nil && strings.TrimSpace(upstream.Code) != "" && status >= 400 && status < 600 {
		title := strings.TrimSpace(upstream.Title)
		if title == "" {
			title = http.StatusText(status)
		}
		detail := strings.TrimSpace(upstream.Detail)
		if detail == "" {
			detail = fallbackDetail
		}
		writeProblem(writer, request, status, upstream.Code, title, detail, upstream.Retryable, nil)
		return
	}
	writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", fallbackDetail, true, nil)
}
