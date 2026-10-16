package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func (h *handler) handleAnalyticsDecision(writer http.ResponseWriter, request *http.Request, inbound identitycontext.DelegationClaims) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input analyticsmodel.AuthorizationDecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_ANALYTICS_DECISION_REQUEST_INVALID", "The Analytics authorization request is invalid.")
		return http.StatusBadRequest
	}
	if input.TenantID != inbound.TenantID {
		writeProblem(writer, http.StatusForbidden, "IAM_ANALYTICS_CONTEXT_MISMATCH", "The Analytics authorization context does not match the delegated Session.")
		return http.StatusForbidden
	}
	decision, err := evaluateAnalyticsAuthorization(request.Context(), h.authorizationStore, h.now(), inbound.SubjectIssuer, inbound.Subject, input)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The IAM authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	_ = h.observability.Metrics.AddCounter(
		"hvac_iam_analytics_authorization_decisions_total",
		"IAM exact Energy Analytics authorization decisions.",
		map[string]string{"result": decisionResult(decision.Allowed), "reason": string(decision.ReasonCode)},
		1,
	)
	h.logger.Info("iam_analytics_authorization_decided",
		"principal_id", decision.PrincipalID,
		"tenant_id", decision.TenantID,
		"site_id", decision.SiteID,
		"allowed", decision.Allowed,
		"reason_code", decision.ReasonCode,
		"policy_revision", decision.PolicyRevision,
		"request_id", telemetryRequestID(request),
		"trace_id", observability.TraceID(request.Context()),
	)
	writeJSON(writer, http.StatusOK, analyticsmodel.AuthorizationDecisionResponse{Decision: decision})
	return http.StatusOK
}

func evaluateAnalyticsAuthorization(ctx context.Context, store AuthorizationStore, now time.Time, subjectIssuer, subject string, request analyticsmodel.AuthorizationDecisionRequest) (analyticsmodel.AuthorizationDecision, error) {
	facts, err := store.LookupRegistryAuthorization(ctx, AuthorizationLookup{
		SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID,
	})
	if err != nil {
		return analyticsmodel.AuthorizationDecision{}, err
	}
	decision := analyticsmodel.AuthorizationDecision{
		SubjectIssuer: subjectIssuer, Subject: subject, TenantID: request.TenantID,
		SiteID: request.SiteID, Action: request.Action, PolicyRevision: facts.PolicyRevision, DecidedAt: formatInstant(now),
	}
	if !facts.Found || facts.Principal.Status != FactStatusActive {
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyPrincipal
		return decision, nil
	}
	decision.PrincipalID = facts.Principal.ID
	if active, _ := facts.member(request.TenantID, now); !active {
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyMembership
		return decision, nil
	}
	allowed, denied := facts.grantsAt(request.TenantID, request.SiteID, request.Action, now)
	switch {
	case !slices.Contains(facts.TenantSiteIDs, request.SiteID):
		// A Site the Tenant does not hold is granted by no role, Tenant-wide ones included.
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyAction
	case denied:
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyExplicit
	case !allowed:
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyAction
	default:
		decision.Allowed = true
		decision.ReasonCode = analyticsmodel.AuthorizationReasonAllowRole
	}
	return decision, nil
}
