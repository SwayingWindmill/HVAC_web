package iam

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

// AnalyticsWorkloadDecisionPath is where an unattended workload asks for an Energy Series grant
// as its own Workload Principal (ADR 0017): mutual TLS, no delegation, the exact query in the body.
const AnalyticsWorkloadDecisionPath = "/internal/v1/analytics/workload-decision"

type AnalyticsWorkloadGrantResponse struct {
	DelegationGrant string `json:"delegationGrant"`
	ExpiresAt       string `json:"expiresAt"`
}

func (h *handler) handleAnalyticsWorkloadDecision(writer http.ResponseWriter, request *http.Request) int {
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
	if !ok || !slices.Contains(h.allowedAnalyticsWorkloads, workload) || h.analyticsGrantAudience == "" {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var query analyticsmodel.EnergySeriesQuery
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&query) != nil || ensureJSONEOF(decoder) != nil || query.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_ANALYTICS_DECISION_REQUEST_INVALID", "The Analytics authorization request is invalid.")
		return http.StatusBadRequest
	}
	scope, err := query.ScopeDigest()
	if err != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_ANALYTICS_DECISION_REQUEST_INVALID", "The Analytics authorization request is invalid.")
		return http.StatusBadRequest
	}
	subjectIssuer := workloadSubjectIssuer(workload)
	now := h.now().UTC()
	decision, err := evaluateAnalyticsAuthorization(request.Context(), h.authorizationStore, now, subjectIssuer, workload, analyticsmodel.AuthorizationDecisionRequest{
		TenantID: query.TenantID, SiteID: query.SiteID, Action: analyticsmodel.EnergySeriesAction,
	})
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
	h.logger.Info("iam_analytics_workload_authorization_decided",
		"principal_id", decision.PrincipalID,
		"workload", workload,
		"tenant_id", decision.TenantID,
		"site_id", decision.SiteID,
		"allowed", decision.Allowed,
		"reason_code", decision.ReasonCode,
		"policy_revision", decision.PolicyRevision,
		"trace_id", observability.TraceID(request.Context()),
	)
	// A Site the workload is not bound to is not disclosed as existing.
	if !decision.Allowed {
		writeProblem(writer, http.StatusNotFound, "IAM_RESOURCE_NOT_FOUND", "The requested Site was not found.")
		return http.StatusNotFound
	}
	expiresAt := now.Add(h.registryGrantLifetime)
	grant, err := identitycontext.SignDelegation(h.registryGrantSigner, identitycontext.DelegationClaims{
		Issuer: h.registryGrantIssuer, Subject: workload, SubjectIssuer: subjectIssuer, PrincipalID: decision.PrincipalID,
		ExecutingService: workload, Audience: h.analyticsGrantAudience, TenantID: query.TenantID,
		Actions: []string{analyticsmodel.EnergySeriesAction}, Scopes: []string{scope}, PolicyRevision: decision.PolicyRevision,
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: h.newRegistryGrantID(),
	})
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_GRANT_UNAVAILABLE", "IAM could not sign the Analytics grant.")
		return http.StatusServiceUnavailable
	}
	writeJSON(writer, http.StatusOK, AnalyticsWorkloadGrantResponse{DelegationGrant: grant, ExpiresAt: expiresAt.Format(time.RFC3339)})
	return http.StatusOK
}
