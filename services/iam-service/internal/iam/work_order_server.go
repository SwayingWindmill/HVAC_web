package iam

import (
	"encoding/json"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
)

func (h *handler) handleWorkOrderDecision(writer http.ResponseWriter, request *http.Request, inbound identitycontext.DelegationClaims) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input workorderauth.DecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_WORK_ORDER_DECISION_REQUEST_INVALID", "The Work Order authorization request is invalid.")
		return http.StatusBadRequest
	}
	if input.TenantID != inbound.TenantID {
		writeProblem(writer, http.StatusForbidden, "IAM_WORK_ORDER_CONTEXT_MISMATCH", "The Work Order authorization context does not match the delegated Session.")
		return http.StatusForbidden
	}
	decision, err := evaluateWorkOrderAuthorization(request.Context(), h.workOrderAuthorizationStore, h.now(), inbound.SubjectIssuer, inbound.Subject, input)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	if err := decision.Validate(); err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_WORK_ORDER_DECISION_INVALID", "The IAM Work Order authorization decision is invalid.")
		return http.StatusServiceUnavailable
	}
	requestID := telemetryRequestID(request)
	traceID := observability.TraceID(request.Context())
	if traceID == "" {
		traceID = requestID
	}
	if h.workOrderAuditSink.RecordWorkOrderDecision(request.Context(), WorkOrderDecisionAudit{
		PrincipalID: decision.PrincipalID, TenantID: decision.TenantID,
		SiteID: decision.SiteID, WorkOrderID: decision.WorkOrderID, AssigneeID: decision.AssigneeID, TeamID: decision.TeamID,
		Action: decision.Action, Allowed: decision.Allowed,
		PolicyRevision: decision.PolicyRevision, ReasonCode: decision.ReasonCode,
		RequestID: requestID, TraceID: traceID, OccurredAt: formatInstant(h.now()),
	}) != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Work Order authorization evidence could not be recorded.")
		return http.StatusServiceUnavailable
	}
	_ = h.observability.Metrics.AddCounter(
		"s5_iam_work_order_authorization_decisions_total",
		"IAM exact Work Order authorization decisions.",
		map[string]string{"result": decisionResult(decision.Allowed), "action": string(decision.Action), "reason": string(decision.ReasonCode)},
		1,
	)
	writeJSON(writer, http.StatusOK, workorderauth.DecisionResponse{Decision: decision})
	return http.StatusOK
}
