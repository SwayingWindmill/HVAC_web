package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
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
	active, _ := tenantMembershipState(facts.Memberships, request.TenantID, now)
	if !active {
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyMembership
		return decision, nil
	}

	allowed := false
	reason := analyticsmodel.AuthorizationReasonDenyAction
	denied := false
	for _, binding := range facts.RoleBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != request.TenantID || !analyticsActionAllowed(binding.Actions, request.Action) {
			continue
		}
		if binding.Effect == BindingEffectDeny {
			denied = true
		}
	}
	for _, binding := range facts.SiteBindings {
		if binding.Status != FactStatusActive || !factEffective(binding.ValidFrom, binding.ValidTo, now) || binding.TenantID != request.TenantID || binding.SiteID != request.SiteID || !analyticsActionAllowed(binding.Actions, request.Action) {
			continue
		}
		if binding.Effect == BindingEffectDeny {
			denied = true
			continue
		}
		if bindingEffectAllows(binding.Effect) {
			allowed = true
			reason = analyticsmodel.AuthorizationReasonAllowSiteBinding
		}
	}
	for _, deny := range facts.ExplicitDenies {
		if deny.Status != FactStatusActive || !factEffective(deny.ValidFrom, deny.ValidTo, now) || !analyticsActionAllowed(deny.Actions, request.Action) {
			continue
		}
		if deny.TenantID != "" && deny.TenantID != request.TenantID {
			continue
		}
		if deny.SiteID == "" || deny.SiteID == request.SiteID {
			denied = true
		}
	}
	if denied {
		decision.ReasonCode = analyticsmodel.AuthorizationReasonDenyExplicit
		return decision, nil
	}
	decision.Allowed = allowed
	decision.ReasonCode = reason
	return decision, nil
}

func analyticsActionAllowed(actions []registryauth.Action, requested string) bool {
	for _, action := range actions {
		if string(action) == requested {
			return true
		}
	}
	return false
}
