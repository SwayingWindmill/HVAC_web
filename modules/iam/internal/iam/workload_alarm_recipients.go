package iam

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

// AlarmRecipientDecisionPath is where Notification Service, with nobody signed in, asks which of
// the people holding WeChat reminders may read a Site's Alarms now (#664,
// docs/specs/miniprogram-wechat.md). Each person is judged by the rule of a signed-in Alarm read
// and recorded the same way. Mutual TLS from a listed workload, no delegation.
const AlarmRecipientDecisionPath = "/internal/v1/alarm/recipient-decision"

// maximumAlarmRecipients bounds one question; a Tenant's reminder holders are far fewer.
const maximumAlarmRecipients = 200

type AlarmRecipient struct {
	SubjectIssuer string `json:"subjectIssuer"`
	Subject       string `json:"subject"`
}

type AlarmRecipientRequest struct {
	TenantID string           `json:"tenantId"`
	SiteID   string           `json:"siteId"`
	AlarmID  string           `json:"alarmId"`
	People   []AlarmRecipient `json:"people"`
}

type AlarmRecipientDecision struct {
	Allowed []AlarmRecipient `json:"allowed"`
}

func (h *handler) handleAlarmRecipientDecision(writer http.ResponseWriter, request *http.Request) int {
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
	if !ok || !slices.Contains(h.allowedAlarmRecipientWorkloads, workload) {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input AlarmRecipientRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&input)
	read := alarmauth.DecisionRequest{TenantID: input.TenantID, SiteID: input.SiteID, AlarmID: input.AlarmID, Action: alarmauth.ActionRead}
	if decodeErr != nil || ensureJSONEOF(decoder) != nil || read.Validate() != nil || !alarmmodel.IsUUIDv7(input.AlarmID) || len(input.People) == 0 || len(input.People) > maximumAlarmRecipients {
		writeProblem(writer, http.StatusBadRequest, "IAM_ALARM_RECIPIENT_REQUEST_INVALID", "The Alarm recipient request is invalid.")
		return http.StatusBadRequest
	}
	requestID := telemetryRequestID(request)
	traceID := observability.TraceID(request.Context())
	if traceID == "" {
		traceID = requestID
	}
	response := AlarmRecipientDecision{Allowed: []AlarmRecipient{}}
	for _, person := range input.People {
		if strings.TrimSpace(person.SubjectIssuer) == "" || strings.TrimSpace(person.Subject) == "" {
			writeProblem(writer, http.StatusBadRequest, "IAM_ALARM_RECIPIENT_REQUEST_INVALID", "The Alarm recipient request is invalid.")
			return http.StatusBadRequest
		}
		decision, err := evaluateAlarmAuthorization(request.Context(), h.alarmAuthorizationStore, h.now(), person.SubjectIssuer, person.Subject, read)
		if err != nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
			return http.StatusServiceUnavailable
		}
		if h.alarmAuditSink.RecordAlarmDecision(request.Context(), AlarmDecisionAudit{
			PrincipalID: decision.PrincipalID, TenantID: decision.TenantID,
			SiteID: decision.SiteID, AlarmID: decision.AlarmID, Action: decision.Action, Allowed: decision.Allowed,
			PolicyRevision: decision.PolicyRevision, ReasonCode: decision.ReasonCode,
			RequestID: requestID, TraceID: traceID, OccurredAt: formatInstant(h.now()),
		}) != nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Alarm authorization evidence could not be recorded.")
			return http.StatusServiceUnavailable
		}
		if decision.Allowed {
			response.Allowed = append(response.Allowed, person)
		}
	}
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}
