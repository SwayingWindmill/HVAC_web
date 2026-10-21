package notificationservice

import (
	"encoding/json"
	"net/http"
)

// serveWeChatReminders reads (GET), adds one (POST {"kind":"ALARM"} or {"kind":"DAILY","siteId":…})
// or clears (DELETE) the WeChat reminders of the person the Gateway's context names.
func (handler *httpHandler) serveWeChatReminders(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writer.Header().Set("Allow", "GET, POST, DELETE")
		handler.writeProblem(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", "WeChat reminders support GET, POST and DELETE.", false)
		return
	}
	claims, ok := handler.authorize(request, WeChatRemindersAction, "")
	if !ok {
		handler.writeProblem(writer, http.StatusForbidden, "NOTIFICATION_ACCESS_DENIED", "Notification access denied", "WeChat reminders are outside the authorized principal scope.", false)
		return
	}
	person := WeChatPerson{TenantID: claims.TenantID, SubjectIssuer: claims.SubjectIssuer, Subject: claims.Subject}
	now := handler.now().UTC()
	switch request.Method {
	case http.MethodDelete:
		if err := handler.reminders.ClearWeChatReminders(request.Context(), person, now); err != nil {
			handler.writeStoreFailure(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		var input ReminderRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || decoder.More() || !input.Valid() {
			handler.writeProblem(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid argument", "Send the kind of reminder the person allowed, and the Site of a daily summary.", false)
			return
		}
		reminders, err := handler.reminders.AddWeChatReminder(request.Context(), person, input, now)
		if err != nil {
			handler.writeStoreFailure(writer, err)
			return
		}
		writeNotificationJSON(writer, http.StatusOK, reminders)
	default:
		reminders, err := handler.reminders.WeChatReminders(request.Context(), person)
		if err != nil {
			handler.writeStoreFailure(writer, err)
			return
		}
		writeNotificationJSON(writer, http.StatusOK, reminders)
	}
}
