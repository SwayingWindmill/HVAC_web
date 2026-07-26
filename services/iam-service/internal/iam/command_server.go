package iam

import (
	"encoding/json"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func (h *handler) handleCommandDecision(writer http.ResponseWriter, request *http.Request, inbound identitycontext.DelegationClaims, presenter string) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input commandauth.DecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || input.Validate() != nil || input.ActingOrganizationID != inbound.ActingOrganizationID {
		writeProblem(writer, http.StatusBadRequest, "IAM_COMMAND_DECISION_REQUEST_INVALID", "The Command authorization request is invalid.")
		return http.StatusBadRequest
	}

	now := h.now().UTC()
	decision, err := evaluateCommandAuthorization(request.Context(), h.commandAuthorizationStore, now, inbound.SubjectIssuer, inbound.Subject, input)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_AUTHORIZATION_UNAVAILABLE", "The Command authorization facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	response := commandauth.DecisionResponse{Decision: decision}
	deliveryCode := "DECISION_DENIED"
	if decision.Allowed {
		if h.commandGrantSigner == nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_COMMAND_GRANT_SIGNER_UNAVAILABLE", "The Command delegation signer is unavailable.")
			return http.StatusServiceUnavailable
		}
		grantID := h.newCommandGrantID()
		if grantID == "" {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_COMMAND_GRANT_ID_UNAVAILABLE", "The Command delegation identifier is unavailable.")
			return http.StatusServiceUnavailable
		}
		grant, signErr := commandauth.SignGrant(h.commandGrantSigner, commandauth.GrantClaims{
			Issuer: h.commandGrantIssuer, Presenter: presenter, Audience: h.commandGrantAudience,
			GrantID: grantID, Purpose: decision.Purpose, PrincipalID: decision.PrincipalID,
			OrganizationID: decision.ActingOrganizationID, SiteID: decision.SiteID, DeviceID: decision.DeviceID,
			Capability: decision.Capability, MaximumRisk: decision.MaximumRisk,
			CapabilityRevision: decision.CapabilityRevision, PolicyRevision: decision.PolicyRevision,
			EmergencyRevocationRevision: decision.EmergencyRevocationRevision,
			IssuedAt:                    now.Unix(), ExpiresAt: now.Add(h.commandGrantLifetime).Unix(),
			TokenID: grantID, Transitive: false,
		})
		if signErr != nil {
			writeProblem(writer, http.StatusServiceUnavailable, "IAM_COMMAND_GRANT_SIGNING_FAILED", "The Command delegation could not be signed.")
			return http.StatusServiceUnavailable
		}
		response.DelegationGrant = grant
		deliveryCode = "GRANT_SIGNED"
	}
	_ = h.observability.Metrics.AddCounter(
		"s3_iam_command_authorization_decisions_total",
		"IAM Command authorization decisions.",
		map[string]string{
			"result": decisionResult(decision.Allowed), "purpose": string(decision.Purpose),
			"capability": string(decision.Capability), "reason": string(decision.ReasonCode), "delivery": deliveryCode,
		},
		1,
	)
	h.logger.InfoContext(request.Context(), "command_authorization_decision",
		"principal_id", decision.PrincipalID,
		"organization_id", decision.ActingOrganizationID,
		"site_id", decision.SiteID,
		"device_id", decision.DeviceID,
		"capability", decision.Capability,
		"purpose", decision.Purpose,
		"allowed", decision.Allowed,
		"reason", decision.ReasonCode,
		"policy_revision", decision.PolicyRevision,
		"grant_signed", response.DelegationGrant != "",
	)
	writeJSON(writer, http.StatusOK, response)
	return http.StatusOK
}
