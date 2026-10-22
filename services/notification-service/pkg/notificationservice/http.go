package notificationservice

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	InternalWeChatRemindersPath = "/internal/v1/notifications/wechat-reminders"
	// WeChatRemindersAction lets the Gateway read, add and clear the signed-in person's reminders.
	WeChatRemindersAction     = "notification:wechat-reminders"
	NotificationContextHeader = "X-Notification-Context"
	DefaultGatewaySPIFFEID    = "spiffe://hvac.local/platform-gateway"
	DefaultAudience           = "notification-service"
)

// WeChatReminderStore keeps how many WeChat reminders each person allowed (wechat_reminders.go).
type WeChatReminderStore interface {
	WeChatReminders(context.Context, WeChatPerson) (WeChatReminders, error)
	AddWeChatReminder(context.Context, WeChatPerson, ReminderRequest, time.Time) (WeChatReminders, error)
	ClearWeChatReminders(context.Context, WeChatPerson, time.Time) error
}

type HTTPConfig struct {
	Reminders        WeChatReminderStore
	GatewayPublicKey crypto.PublicKey
	GatewaySPIFFEID  string
	Audience         string
	Now              func() time.Time
}

type httpHandler struct {
	reminders        WeChatReminderStore
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
	if config.Reminders == nil || config.GatewayPublicKey == nil {
		return nil, errors.New("Notification WeChat reminder store and Gateway public key are required")
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
	return &httpHandler{reminders: config.Reminders, gatewayPublicKey: config.GatewayPublicKey, gatewaySPIFFEID: config.GatewaySPIFFEID, audience: config.Audience, now: config.Now}, nil
}

func (handler *httpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "private, no-store")
	for _, header := range []string{"X-Principal", "X-Roles", "X-Tenant-ID", "X-Organization-ID", "X-Site-ID", "X-Admin", "X-Delegation-Grant"} {
		if request.Header.Get(header) != "" {
			handler.writeProblem(writer, http.StatusBadRequest, "FORGED_IDENTITY_HEADER", "Forged identity header", "Caller-supplied identity headers are not accepted by Notification Service.", false)
			return
		}
	}
	if request.URL.Path != InternalWeChatRemindersPath {
		handler.writeProblem(writer, http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", "The requested Notification route does not exist.", false)
		return
	}
	handler.serveWeChatReminders(writer, request)
}

func (handler *httpHandler) authorize(request *http.Request, action string) (identitycontext.DelegationClaims, bool) {
	claims, err := identitycontext.VerifyDelegation(handler.gatewayPublicKey, strings.TrimSpace(request.Header.Get(NotificationContextHeader)))
	if err != nil || !alarmmodel.IsUUIDv7(claims.TenantID) || strings.TrimSpace(claims.Subject) == "" || len(claims.Actions) != 1 || claims.Actions[0] != action {
		return identitycontext.DelegationClaims{}, false
	}
	expectedScopes := []string{"tenant:" + claims.TenantID, "principal:" + claims.Subject}
	if err := identitycontext.ValidateDelegationAnyScope(claims, handler.now().UTC(), handler.gatewaySPIFFEID, handler.audience, action, expectedScopes); err != nil || !sameStringSet(claims.Scopes, expectedScopes) {
		return identitycontext.DelegationClaims{}, false
	}
	return claims, true
}

func (handler *httpHandler) writeStoreFailure(writer http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The Notification object is not visible.", false)
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
