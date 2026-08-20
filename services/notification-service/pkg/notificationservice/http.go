package notificationservice

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalInboxPath          = "/internal/v1/notifications/inbox"
	NotificationContextHeader  = "X-Notification-Context"
	NotificationReadAction     = "notification:read"
	NotificationMarkReadAction = "notification:mark-read"
	DefaultGatewaySPIFFEID     = "spiffe://hvac.local/platform-gateway"
	DefaultAudience            = "notification-service"
)

type InboxStore interface {
	ListInbox(context.Context, string, string, int) ([]InboxItem, error)
	MarkRead(context.Context, string, string, string, time.Time) (InboxItem, error)
}

type HTTPConfig struct {
	Store            InboxStore
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFEID  string
	Audience         string
	Now              func() time.Time
}

type httpHandler struct {
	store            InboxStore
	gatewayPublicKey crypto.PublicKey
	gatewaySPIFFEID  string
	audience         string
	now              func() time.Time
}

type httpProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func NewHTTPHandler(config HTTPConfig) (http.Handler, error) {
	if config.Store == nil || config.GatewayPublicKey == nil {
		return nil, errors.New("Notification Store and Gateway public key are required")
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
	return &httpHandler{store: config.Store, gatewayPublicKey: config.GatewayPublicKey, gatewaySPIFFEID: config.GatewaySPIFFEID, audience: config.Audience, now: config.Now}, nil
}

func (handler *httpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "private, no-store")
	for _, header := range []string{"X-Principal", "X-Roles", "X-Tenant-ID", "X-Organization-ID", "X-Site-ID", "X-Admin", "X-Delegation-Grant"} {
		if request.Header.Get(header) != "" {
			handler.writeProblem(writer, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Caller-supplied identity headers are not accepted by Notification Service.", false)
			return
		}
	}
	if request.URL.Path == InternalInboxPath {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Notification Inbox listing only supports GET.", false)
			return
		}
		claims, ok := handler.authorize(request, NotificationReadAction, "")
		if !ok {
			handler.writeProblem(writer, http.StatusForbidden, "NOTIFICATION_ACCESS_DENIED", "Notification access denied", "The Notification Inbox is outside the authorized principal scope.", false)
			return
		}
		limit := 50
		query := request.URL.Query()
		for key := range query {
			if key != "limit" || len(query[key]) != 1 {
				handler.writeProblem(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "The Notification Inbox query is invalid.", false)
				return
			}
		}
		if raw := query.Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 200 {
				handler.writeProblem(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "limit must be between 1 and 200.", false)
				return
			}
			limit = value
		}
		items, err := handler.store.ListInbox(request.Context(), claims.TenantID, claims.Subject, limit)
		if err != nil {
			handler.writeStoreFailure(writer, err)
			return
		}
		writeNotificationJSON(writer, http.StatusOK, items)
		return
	}
	prefix := InternalInboxPath + "/"
	if !strings.HasPrefix(request.URL.Path, prefix) {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Notification route does not exist.", false)
		return
	}
	segments := strings.Split(strings.TrimPrefix(request.URL.Path, prefix), "/")
	if len(segments) != 2 || segments[1] != "read" {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Notification route does not exist.", false)
		return
	}
	inboxItemID, err := url.PathUnescape(segments[0])
	if err != nil || !alarmmodel.IsUUIDv7(inboxItemID) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Notification item is not visible.", false)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "Notification read-state mutation only supports POST.", false)
		return
	}
	if request.ContentLength > 0 {
		handler.writeProblem(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Notification read-state mutation does not accept a request body.", false)
		return
	}
	claims, ok := handler.authorize(request, NotificationMarkReadAction, inboxItemID)
	if !ok {
		handler.writeProblem(writer, http.StatusForbidden, "NOTIFICATION_ACCESS_DENIED", "Notification access denied", "The Notification item is outside the authorized principal scope.", false)
		return
	}
	item, err := handler.store.MarkRead(request.Context(), claims.TenantID, claims.Subject, inboxItemID, handler.now().UTC())
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	writeNotificationJSON(writer, http.StatusOK, item)
}

func (handler *httpHandler) authorize(request *http.Request, action, inboxItemID string) (identitycontext.DelegationClaims, bool) {
	claims, err := identitycontext.VerifyDelegation(handler.gatewayPublicKey, strings.TrimSpace(request.Header.Get(NotificationContextHeader)))
	if err != nil || !alarmmodel.IsUUIDv7(claims.TenantID) || strings.TrimSpace(claims.Subject) == "" || len(claims.Actions) != 1 || claims.Actions[0] != action {
		return identitycontext.DelegationClaims{}, false
	}
	expectedScopes := []string{"tenant:" + claims.TenantID, "principal:" + claims.Subject}
	if inboxItemID != "" {
		expectedScopes = append(expectedScopes, "notification:"+inboxItemID)
	}
	if err := identitycontext.ValidateDelegationAnyScope(claims, handler.now().UTC(), handler.gatewaySPIFFEID, handler.audience, action, expectedScopes); err != nil || !sameStringSet(claims.Scopes, expectedScopes) {
		return identitycontext.DelegationClaims{}, false
	}
	return claims, true
}

func (handler *httpHandler) writeStoreFailure(writer http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Notification item is not visible.", false)
		return
	}
	handler.writeProblem(writer, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "Notification state is temporarily unavailable.", true)
}

func (handler *httpHandler) writeProblem(writer http.ResponseWriter, status int, code, title, detail string, retryable bool) {
	writeNotificationJSON(writer, status, httpProblem{Type: "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")), Title: title, Status: status, Detail: detail, Code: code, Retryable: retryable})
}

func writeNotificationJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, item := range left {
		counts[item]++
	}
	for _, item := range right {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
