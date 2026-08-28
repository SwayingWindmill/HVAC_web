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

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

const (
	publicNotificationInboxPath      = "/api/v1/notifications/inbox"
	defaultNotificationTimeout       = 5 * time.Second
	defaultNotificationResponseLimit = int64(2 << 20)
)

type NotificationConfig struct {
	BackendBaseURL    string
	BackendHTTPClient *http.Client
	BackendAudience   string
	Timeout           time.Duration
	MaxResponseBytes  int64
}

type notificationController struct {
	baseURL          string
	httpClient       *http.Client
	backendAudience  string
	timeout          time.Duration
	maxResponseBytes int64
}

type notificationRoute struct {
	inboxItemID string
	markRead    bool
}

func newNotificationController(config *NotificationConfig) *notificationController {
	if config == nil {
		return nil
	}
	resolved := *config
	resolved.BackendBaseURL = strings.TrimRight(strings.TrimSpace(resolved.BackendBaseURL), "/")
	if resolved.BackendHTTPClient == nil {
		resolved.BackendHTTPClient = &http.Client{Timeout: defaultNotificationTimeout}
	}
	if resolved.BackendAudience == "" {
		resolved.BackendAudience = notificationservice.DefaultAudience
	}
	if resolved.Timeout <= 0 || resolved.Timeout > 30*time.Second {
		resolved.Timeout = defaultNotificationTimeout
	}
	if resolved.MaxResponseBytes <= 0 || resolved.MaxResponseBytes > 16<<20 {
		resolved.MaxResponseBytes = defaultNotificationResponseLimit
	}
	return &notificationController{baseURL: resolved.BackendBaseURL, httpClient: resolved.BackendHTTPClient, backendAudience: resolved.BackendAudience, timeout: resolved.Timeout, maxResponseBytes: resolved.MaxResponseBytes}
}

func matchPublicNotificationRoute(path string) (notificationRoute, bool) {
	if path == publicNotificationInboxPath {
		return notificationRoute{}, true
	}
	prefix := publicNotificationInboxPath + "/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/read") {
		return notificationRoute{}, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/read")
	if raw == "" || strings.Contains(raw, "/") {
		return notificationRoute{}, false
	}
	itemID, err := url.PathUnescape(raw)
	if err != nil || !alarmmodel.IsUUIDv7(itemID) {
		return notificationRoute{}, false
	}
	return notificationRoute{inboxItemID: itemID, markRead: true}, true
}

func dispatchNotificationRoute(h *handler, writer http.ResponseWriter, request *http.Request, route notificationRoute) {
	expectedMethod := http.MethodGet
	action := notificationservice.NotificationReadAction
	if route.markRead {
		expectedMethod = http.MethodPost
		action = notificationservice.NotificationMarkReadAction
	}
	if request.Method != expectedMethod {
		writeMethodNotAllowedFor(writer, request, expectedMethod)
		return
	}
	if h.notification == nil || h.notification.baseURL == "" || h.notification.httpClient == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service is not configured.", true, nil)
		return
	}
	if h.identity == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Session validation is unavailable.", true, nil)
		return
	}
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !h.allowRateLimitedTenant(writer, request, limitpolicy.DimensionNotification, session.TenantID) {
		return
	}
	if route.markRead {
		if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
			writeIdentityFailure(writer, request, *failure)
			return
		}
		if request.ContentLength > 0 {
			writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Notification read-state mutation does not accept a request body.", false, nil)
			return
		}
	} else {
		for key := range request.URL.Query() {
			if key != "limit" || len(request.URL.Query()[key]) != 1 {
				writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The Notification Inbox query is invalid.", false, nil)
				return
			}
		}
		if raw := request.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 200 {
				writeProblem(writer, request, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "limit must be between 1 and 200.", false, nil)
				return
			}
		}
	}
	token, err := h.signNotificationContext(session, action, route.inboxItemID)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification context could not be signed.", true, nil)
		return
	}
	body, status, err := h.executeNotification(request, route, token)
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service is temporarily unavailable.", true, nil)
		return
	}
	if status == http.StatusNotFound {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Notification item is not visible.", false, nil)
		return
	}
	if status != http.StatusOK {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service could not complete the request.", true, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	if route.markRead {
		var item notificationservice.InboxItem
		if decodeNotificationJSON(body, &item) != nil || item.TenantID != session.TenantID || item.PrincipalID != session.Principal.Subject || item.InboxItemID != route.inboxItemID {
			writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service returned an invalid principal projection.", true, nil)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": item, "meta": map[string]any{"requestId": requestIDFromContext(request.Context())}})
		return
	}
	var items []notificationservice.InboxItem
	if decodeNotificationJSON(body, &items) != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service returned an invalid Inbox projection.", true, nil)
		return
	}
	if items == nil {
		items = []notificationservice.InboxItem{}
	}
	for _, item := range items {
		if item.TenantID != session.TenantID || item.PrincipalID != session.Principal.Subject || !alarmmodel.IsUUIDv7(item.InboxItemID) {
			writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification Service returned an Inbox item outside the authenticated principal scope.", true, nil)
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": items, "meta": map[string]any{"requestId": requestIDFromContext(request.Context()), "count": len(items)}})
}

func (h *handler) signNotificationContext(session bffSession, action, inboxItemID string) (string, error) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	scopes := []string{"tenant:" + session.TenantID, "principal:" + session.Principal.Subject}
	if inboxItemID != "" {
		scopes = append(scopes, "notification:"+inboxItemID)
	}
	claims := identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...),
		ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE, Audience: h.notification.backendAudience,
		TenantID: session.TenantID, Actions: []string{action}, Scopes: scopes, PolicyRevision: h.identity.config.PolicyRevision,
		SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
}

func (h *handler) executeNotification(request *http.Request, route notificationRoute, token string) ([]byte, int, error) {
	path := notificationservice.InternalInboxPath
	method := http.MethodGet
	if route.markRead {
		method = http.MethodPost
		path += "/" + url.PathEscape(route.inboxItemID) + "/read"
	}
	requestContext, cancel := context.WithTimeout(request.Context(), h.notification.timeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(requestContext, method, h.notification.baseURL+path, bytes.NewReader(nil))
	if err != nil {
		return nil, 0, err
	}
	if !route.markRead {
		upstream.URL.RawQuery = request.URL.RawQuery
	}
	upstream.Header.Set("Accept", "application/json, application/problem+json")
	upstream.Header.Set(notificationservice.NotificationContextHeader, token)
	upstream.Header.Set("X-Request-ID", requestIDFromContext(request.Context()))
	observability.InjectHTTP(request.Context(), upstream.Header)
	response, err := h.notification.httpClient.Do(upstream)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, h.notification.maxResponseBytes+1))
	if err != nil || int64(len(body)) > h.notification.maxResponseBytes {
		return nil, 0, io.ErrUnexpectedEOF
	}
	return body, response.StatusCode, nil
}

func decodeNotificationJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
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
