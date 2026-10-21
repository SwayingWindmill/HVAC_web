package iam

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

// ReminderRecipientDecisionPath is where Notification Service, with nobody signed in, asks which
// of the people holding WeChat reminders may get one now (#664, docs/specs/miniprogram-wechat.md).
// An ALARM reminder is judged by the rule of a signed-in Alarm read on the Site and recorded the
// same way; a DAILY summary by the rule of a signed-in energy read, which is only counted, like
// every energy decision. Mutual TLS from a listed workload, no delegation.
const ReminderRecipientDecisionPath = "/internal/v1/reminders/recipient-decision"

// maximumReminderRecipients bounds one question; Notification Service asks in pages of this size.
const maximumReminderRecipients = 200

type ReminderRecipient struct {
	SubjectIssuer string `json:"subjectIssuer"`
	Subject       string `json:"subject"`
}

type ReminderRecipientRequest struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	// Kind is ALARM (with the Alarm) or DAILY.
	Kind    string              `json:"kind"`
	AlarmID string              `json:"alarmId,omitempty"`
	People  []ReminderRecipient `json:"people"`
}

type ReminderRecipientDecision struct {
	Allowed []ReminderRecipient `json:"allowed"`
}

func (h *handler) handleReminderRecipientDecision(writer http.ResponseWriter, request *http.Request) int {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, http.StatusMethodNotAllowed, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return http.StatusMethodNotAllowed
	}
	if hasForgedIdentityHeader(request.Header) || strings.TrimSpace(request.Header.Get("X-Delegation-Grant")) != "" {
		writeProblem(writer, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.")
		return http.StatusBadRequest
	}
	_, workload, ok := peerIdentity(request)
	if !ok || !slices.Contains(h.allowedReminderRecipientWorkloads, workload) {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input ReminderRecipientRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&input)
	valid := decodeErr == nil && ensureJSONEOF(decoder) == nil && len(input.People) > 0 && len(input.People) <= maximumReminderRecipients
	switch input.Kind {
	case "ALARM":
		valid = valid && alarmmodel.IsUUIDv7(input.AlarmID) &&
			(alarmauth.DecisionRequest{TenantID: input.TenantID, SiteID: input.SiteID, AlarmID: input.AlarmID, Action: alarmauth.ActionRead}).Validate() == nil
	case "DAILY":
		valid = valid && input.AlarmID == "" && alarmmodel.IsUUIDv7(input.TenantID) && alarmmodel.IsUUIDv7(input.SiteID)
	default:
		valid = false
	}
	for _, person := range input.People {
		valid = valid && strings.TrimSpace(person.SubjectIssuer) != "" && strings.TrimSpace(person.Subject) != ""
	}
	if !valid {
		writeProblem(writer, http.StatusBadRequest, "IAM_REMINDER_RECIPIENT_REQUEST_INVALID", "The reminder recipient request is invalid.")
		return http.StatusBadRequest
	}
	response := ReminderRecipientDecision{Allowed: []ReminderRecipient{}}
	for _, person := range input.People {
		allowed, status := h.mayGetReminder(writer, request, input, person)
		if status != http.StatusOK {
			return status
		}
		if allowed {
			response.Allowed = append(response.Allowed, person)
		}
	}
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}

// mayGetReminder judges one person; a status other than OK has been written.
func (h *handler) mayGetReminder(writer http.ResponseWriter, request *http.Request, input ReminderRecipientRequest, person ReminderRecipient) (bool, int) {
	if input.Kind == "DAILY" {
		decision, err := evaluateAnalyticsAuthorization(request.Context(), h.authorizationStore, h.now(), person.SubjectIssuer, person.Subject, analyticsmodel.AuthorizationDecisionRequest{
			TenantID: input.TenantID, SiteID: input.SiteID, Action: analyticsmodel.EnergySeriesAction,
		})
		if err != nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
			return false, http.StatusServiceUnavailable
		}
		_ = h.observability.Metrics.AddCounter("iam_reminder_recipient_decisions_total", "IAM decisions on who may get a WeChat reminder.",
			map[string]string{"kind": "DAILY", "result": decisionResult(decision.Allowed)}, 1)
		return decision.Allowed, http.StatusOK
	}
	read := alarmauth.DecisionRequest{TenantID: input.TenantID, SiteID: input.SiteID, AlarmID: input.AlarmID, Action: alarmauth.ActionRead}
	decision, err := evaluateAlarmAuthorization(request.Context(), h.alarmAuthorizationStore, h.now(), person.SubjectIssuer, person.Subject, read)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
		return false, http.StatusServiceUnavailable
	}
	requestID := telemetryRequestID(request)
	traceID := observability.TraceID(request.Context())
	if traceID == "" {
		traceID = requestID
	}
	if h.alarmAuditSink.RecordAlarmDecision(request.Context(), AlarmDecisionAudit{
		PrincipalID: decision.PrincipalID, TenantID: decision.TenantID,
		SiteID: decision.SiteID, AlarmID: decision.AlarmID, Action: decision.Action, Allowed: decision.Allowed,
		PolicyRevision: decision.PolicyRevision, ReasonCode: decision.ReasonCode,
		RequestID: requestID, TraceID: traceID, OccurredAt: formatInstant(h.now()),
	}) != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Alarm authorization evidence could not be recorded.")
		return false, http.StatusServiceUnavailable
	}
	return decision.Allowed, http.StatusOK
}
