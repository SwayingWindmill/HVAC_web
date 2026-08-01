package iam

import (
	"encoding/json"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func (h *handler) handleAlarmDecision(writer http.ResponseWriter, request *http.Request, inbound identitycontext.DelegationClaims) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input alarmauth.DecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_ALARM_DECISION_REQUEST_INVALID", "The Alarm authorization request is invalid.")
		return http.StatusBadRequest
	}
	if input.ActingOrganizationID != inbound.ActingOrganizationID {
		writeProblem(writer, http.StatusForbidden, "IAM_ALARM_CONTEXT_MISMATCH", "The Alarm authorization context does not match the delegated Session.")
		return http.StatusForbidden
	}
	decision, err := evaluateAlarmAuthorization(request.Context(), h.alarmAuthorizationStore, h.now(), inbound.SubjectIssuer, inbound.Subject, input)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	if err := decision.Validate(); err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_ALARM_DECISION_INVALID", "The IAM Alarm authorization decision is invalid.")
		return http.StatusServiceUnavailable
	}
	requestID := telemetryRequestID(request)
	traceID := observability.TraceID(request.Context())
	if traceID == "" {
		traceID = requestID
	}
	if h.alarmAuditSink.RecordAlarmDecision(request.Context(), AlarmDecisionAudit{
		PrincipalID: decision.PrincipalID, ActingOrganizationID: decision.ActingOrganizationID,
		SiteID: decision.SiteID, AlarmID: decision.AlarmID, Action: decision.Action, Allowed: decision.Allowed,
		PolicyRevision: decision.PolicyRevision, ReasonCode: decision.ReasonCode,
		RequestID: requestID, TraceID: traceID, OccurredAt: formatInstant(h.now()),
	}) != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Alarm authorization evidence could not be recorded.")
		return http.StatusServiceUnavailable
	}
	_ = h.observability.Metrics.AddCounter(
		"s4_iam_alarm_authorization_decisions_total",
		"IAM exact Alarm authorization decisions.",
		map[string]string{"result": decisionResult(decision.Allowed), "action": string(decision.Action), "reason": string(decision.ReasonCode)},
		1,
	)
	writeJSON(writer, http.StatusOK, alarmauth.DecisionResponse{Decision: decision})
	return http.StatusOK
}
