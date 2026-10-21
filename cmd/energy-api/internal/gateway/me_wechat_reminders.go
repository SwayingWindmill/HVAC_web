package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

// The WeChat reminders the signed-in person allowed in the mini program (#664,
// docs/specs/miniprogram-wechat.md): Notification Service keeps the count for the person the
// Gateway's context names.

const publicMeWeChatRemindersPath = "/api/v1/me/wechat/reminders"

var errNotificationNotConfigured = errors.New("Notification Service is not configured")

func (h *handler) MeWeChatReminders(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, "GET, POST")
		return
	}
	session, ok := h.weChatSession(writer, request)
	if !ok {
		return
	}
	var body any
	if request.Method == http.MethodPost {
		var input notificationservice.ReminderRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<10))
		decoder.DisallowUnknownFields()
		valid := decoder.Decode(&input) == nil && ensureJSONEOF(decoder) == nil
		switch input.Kind {
		case notificationservice.ReminderAlarm:
			valid = valid && input.SiteID == ""
		case notificationservice.ReminderDaily:
			valid = valid && alarmmodel.IsUUIDv7(input.SiteID)
		default:
			valid = false
		}
		if !valid {
			writeIdentityFailure(writer, request, identityFailure{400, "REQUEST_INVALID", "Request invalid", "Send the kind of reminder the person allowed (and the Site of a daily summary), and nothing else.", false})
			return
		}
		body = input
	}
	status, reply, err := h.callWeChatReminders(request, session, request.Method, body)
	var reminders notificationservice.WeChatReminders
	if err != nil || status != http.StatusOK || decodeNotificationJSON(reply, &reminders) != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "WeChat reminders are temporarily unavailable.", true, nil)
		return
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, reminders)
}

// callWeChatReminders sends one reminder request to Notification Service for the Session's person.
func (h *handler) callWeChatReminders(request *http.Request, session bffSession, method string, body any) (int, []byte, error) {
	if h.notification == nil || h.notification.baseURL == "" || h.notification.httpClient == nil {
		return 0, nil, errNotificationNotConfigured
	}
	token, err := h.signNotificationContext(session, notificationservice.WeChatRemindersAction, "")
	if err != nil {
		return 0, nil, err
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	outbound, err := http.NewRequestWithContext(request.Context(), method, strings.TrimRight(h.notification.baseURL, "/")+notificationservice.InternalWeChatRemindersPath, payload)
	if err != nil {
		return 0, nil, err
	}
	outbound.Header.Set(notificationservice.NotificationContextHeader, token)
	if body != nil {
		outbound.Header.Set("Content-Type", "application/json")
	}
	response, err := h.notification.httpClient.Do(outbound)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(response.Body, h.notification.maxResponseBytes))
	return response.StatusCode, reply, err
}
