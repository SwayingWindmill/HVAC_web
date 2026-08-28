package iam

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

var safeCorrelationPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func (h *handler) handleTelemetryDecision(writer http.ResponseWriter, request *http.Request, inbound identitycontext.DelegationClaims, presenter string) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input telemetryauth.DecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_TELEMETRY_DECISION_REQUEST_INVALID", "The Telemetry authorization request is invalid.")
		return http.StatusBadRequest
	}
	if input.TenantID != inbound.TenantID {
		writeProblem(writer, http.StatusForbidden, "IAM_TELEMETRY_CONTEXT_MISMATCH", "The Telemetry authorization context does not match the delegated Session.")
		return http.StatusForbidden
	}

	now := h.now()
	decision, err := evaluateTelemetryAuthorization(request.Context(), h.telemetryAuthorizationStore, now, inbound.SubjectIssuer, inbound.Subject, input)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	response := telemetryauth.DecisionResponse{Decision: decision}
	deliveryCode := "DECISION_DENIED"
	requestID := telemetryRequestID(request)
	if decision.Allowed {
		if h.telemetryGrantSigner == nil {
			deliveryCode = "GRANT_SIGNER_UNAVAILABLE"
			if !h.recordTelemetryDecision(request, decision, false, deliveryCode, requestID) {
				writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Telemetry authorization evidence could not be recorded.")
				return http.StatusServiceUnavailable
			}
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_GRANT_SIGNER_UNAVAILABLE", "The Telemetry delegation signer is unavailable.")
			return http.StatusServiceUnavailable
		}
		grantID := h.newTelemetryGrantID()
		if grantID == "" {
			deliveryCode = "GRANT_ID_UNAVAILABLE"
			if !h.recordTelemetryDecision(request, decision, false, deliveryCode, requestID) {
				writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Telemetry authorization evidence could not be recorded.")
				return http.StatusServiceUnavailable
			}
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_GRANT_ID_UNAVAILABLE", "The Telemetry delegation identifier is unavailable.")
			return http.StatusServiceUnavailable
		}
		targetCount, keyCount := telemetryDecisionCounts(decision)
		grant, err := telemetryauth.SignGrant(h.telemetryGrantSigner, telemetryauth.GrantClaims{
			Issuer: h.telemetryGrantIssuer, Presenter: presenter, Audience: h.telemetryGrantAudience,
			PrincipalID: decision.PrincipalID, SubjectIssuer: decision.SubjectIssuer, Subject: decision.Subject,
			TenantID: decision.TenantID,
			ActorChain:           []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: presenter}},
			Action:               decision.Action, ScopeDigest: decision.ScopeDigest, TargetCount: targetCount, KeyCount: keyCount,
			PolicyRevision: decision.PolicyRevision, SessionID: inbound.SessionID, ParentTokenID: inbound.TokenID,
			RequestID: requestID, TraceID: observability.TraceID(request.Context()), Route: telemetryPublicRoute(decision.Action),
			IssuedAt: now.Unix(), ExpiresAt: now.Add(h.telemetryGrantLifetime).Unix(), TokenID: grantID, Transitive: false,
		})
		if err != nil {
			deliveryCode = "GRANT_SIGNING_FAILED"
			if !h.recordTelemetryDecision(request, decision, false, deliveryCode, requestID) {
				writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Telemetry authorization evidence could not be recorded.")
				return http.StatusServiceUnavailable
			}
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_GRANT_SIGNING_FAILED", "The Telemetry delegation could not be signed.")
			return http.StatusServiceUnavailable
		}
		response.DelegationGrant = grant
		deliveryCode = "GRANT_SIGNED"
	}
	if !h.recordTelemetryDecision(request, decision, response.DelegationGrant != "", deliveryCode, requestID) {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_AUDIT_UNAVAILABLE", "The Telemetry authorization evidence could not be recorded.")
		return http.StatusServiceUnavailable
	}
	_ = h.observability.Metrics.AddCounter(
		"s2_iam_telemetry_authorization_decisions_total",
		"IAM exact Telemetry authorization decisions.",
		map[string]string{"result": decisionResult(decision.Allowed), "action": string(decision.Action), "reason": string(decision.ReasonCode), "delivery": deliveryCode},
		1,
	)
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}

func (h *handler) recordTelemetryDecision(request *http.Request, decision telemetryauth.Decision, grantSigned bool, deliveryCode, requestID string) bool {
	targetCount, keyCount := telemetryDecisionCounts(decision)
	return h.telemetryAuditSink.RecordTelemetryDecision(request.Context(), TelemetryDecisionAudit{
		PrincipalID: decision.PrincipalID, TenantID: decision.TenantID, Action: decision.Action,
		Allowed: decision.Allowed, TargetCount: targetCount, KeyCount: keyCount, ScopeDigest: decision.ScopeDigest,
		PolicyRevision: decision.PolicyRevision, ReasonCode: decision.ReasonCode, GrantSigned: grantSigned,
		DeliveryCode: deliveryCode, RequestID: requestID, TraceID: observability.TraceID(request.Context()), OccurredAt: formatInstant(h.now()),
	}) == nil
}

func telemetryDecisionCounts(decision telemetryauth.Decision) (int, int) {
	keyCount := 0
	for _, target := range decision.Targets {
		keyCount += len(target.Keys)
	}
	return len(decision.Targets), keyCount
}

func telemetryRequestID(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if safeCorrelationPattern.MatchString(value) {
		return value
	}
	return observability.TraceID(request.Context())
}

func telemetryPublicRoute(action telemetryauth.Action) string {
	switch action {
	case telemetryauth.ActionSnapshotRead:
		return "/api/v1/devices/{deviceId}/observation-snapshot"
	case telemetryauth.ActionBatchRead:
		return "/api/v1/telemetry/observation-snapshots:batchGet"
	case telemetryauth.ActionSubscribe, telemetryauth.ActionResubscribe, telemetryauth.ActionRecoveryUse:
		return "/api/v1/telemetry/subscriptions:bootstrap"
	case telemetryauth.ActionRecoveryCheckpoint:
		return "/api/v1/telemetry/recovery-cursors:checkpoint"
	default:
		return ""
	}
}
