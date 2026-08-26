package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	TelemetryGrantConsumePath   = "/internal/v1/telemetry/grants:consume"
	TelemetryRevocationPollPath = "/internal/v1/telemetry/revocations:poll"
)

type TelemetryGrantStore interface {
	ConsumeGrant(ctx context.Context, claims telemetryauth.GrantClaims, now time.Time) (telemetryauth.GrantUseStatus, error)
	PollRevocations(ctx context.Context, tenantID string, afterSequence int64, limit int) ([]TelemetryRevocationFact, error)
}

type telemetryGrantConsumeRequest struct {
	DelegationGrant      string                 `json:"delegationGrant"`
	PrincipalID          string                 `json:"principalId"`
	SessionID            string                 `json:"sessionId"`
	TenantID string                 `json:"tenantId"`
	Action               telemetryauth.Action   `json:"action"`
	Targets              []telemetryauth.Target `json:"targets"`
}

type telemetryGrantAcceptance struct {
	TokenID              string               `json:"tokenId"`
	PrincipalID          string               `json:"principalId"`
	SessionID            string               `json:"sessionId"`
	TenantID string               `json:"tenantId"`
	Action               telemetryauth.Action `json:"action"`
	ScopeDigest          string               `json:"scopeDigest"`
	PolicyRevision       string               `json:"policyRevision"`
	ExpiresAt            int64                `json:"expiresAt"`
}

type telemetryRevocationPollRequest struct {
	TenantID string `json:"tenantId"`
	AfterSequence        int64  `json:"afterSequence"`
	Limit                int    `json:"limit"`
}

type telemetryRevocationPollResponse struct {
	Facts        []TelemetryRevocationFact `json:"facts"`
	NextSequence int64                     `json:"nextSequence"`
}

func (h *handler) handleTelemetryRuntimeRoute(writer http.ResponseWriter, request *http.Request) int {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, http.StatusMethodNotAllowed, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return http.StatusMethodNotAllowed
	}
	if hasForgedIdentityHeader(request.Header) || strings.TrimSpace(request.Header.Get("X-Delegation-Grant")) != "" {
		writeProblem(writer, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.")
		return http.StatusBadRequest
	}
	_, spiffeID, ok := peerIdentity(request)
	if !ok || h.telemetryRuntimeSPIFFE == "" || spiffeID != h.telemetryRuntimeSPIFFE {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	if h.telemetryGrantStore == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_GRANT_STORE_UNAVAILABLE", "Telemetry grant state is unavailable.")
		return http.StatusServiceUnavailable
	}
	if request.URL.Path == TelemetryGrantConsumePath {
		return h.handleTelemetryGrantConsume(writer, request)
	}
	return h.handleTelemetryRevocationPoll(writer, request)
}

func (h *handler) handleTelemetryGrantConsume(writer http.ResponseWriter, request *http.Request) int {
	request.Body = http.MaxBytesReader(writer, request.Body, telemetryauth.MaximumEncodedGrantSize+maximumDecisionRequestSize)
	var input telemetryGrantConsumeRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || strings.TrimSpace(input.DelegationGrant) == "" || strings.TrimSpace(input.PrincipalID) == "" || strings.TrimSpace(input.SessionID) == "" {
		writeProblem(writer, http.StatusBadRequest, "IAM_TELEMETRY_GRANT_REQUEST_INVALID", "The Telemetry grant request is invalid.")
		return http.StatusBadRequest
	}
	requestScope := telemetryauth.DecisionRequest{TenantID: input.TenantID, Action: input.Action, Targets: input.Targets}
	if requestScope.Validate() != nil {
		writeProblem(writer, http.StatusBadRequest, "IAM_TELEMETRY_GRANT_REQUEST_INVALID", "The Telemetry grant request is invalid.")
		return http.StatusBadRequest
	}
	if h.telemetryGrantSigner == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_GRANT_VERIFIER_UNAVAILABLE", "The Telemetry grant verifier is unavailable.")
		return http.StatusServiceUnavailable
	}
	now := h.now()
	var useErr error
	claims, err := telemetryauth.VerifyGrant(h.telemetryGrantSigner.Public(), input.DelegationGrant)
	if err == nil {
		err = telemetryauth.ValidateGrant(claims, telemetryauth.GrantValidation{
			Now: now, Issuer: h.telemetryGrantIssuer, Presenter: h.allowedWorkloadSPIFFE, Audience: h.telemetryGrantAudience,
			PrincipalID: input.PrincipalID, SessionID: input.SessionID, Action: input.Action,
			TenantID: input.TenantID, Targets: input.Targets,
			UseChecker: func(claims telemetryauth.GrantClaims) (telemetryauth.GrantUseStatus, error) {
				status, err := h.telemetryGrantStore.ConsumeGrant(request.Context(), claims, now)
				useErr = err
				return status, err
			},
		})
	}
	if useErr != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_GRANT_STATE_UNAVAILABLE", "Telemetry grant state is unavailable.")
		return http.StatusServiceUnavailable
	}
	if err != nil {
		writeProblem(writer, http.StatusForbidden, "IAM_TELEMETRY_GRANT_REJECTED", "The Telemetry grant is not valid for this operation.")
		return http.StatusForbidden
	}
	writeJSON(writer, http.StatusOK, telemetryGrantAcceptance{
		TokenID: claims.TokenID, PrincipalID: claims.PrincipalID, SessionID: claims.SessionID,
		TenantID: claims.TenantID, Action: claims.Action, ScopeDigest: claims.ScopeDigest,
		PolicyRevision: claims.PolicyRevision, ExpiresAt: claims.ExpiresAt,
	})
	return http.StatusOK
}

func (h *handler) handleTelemetryRevocationPoll(writer http.ResponseWriter, request *http.Request) int {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumGrantStatusSize)
	var input telemetryRevocationPollRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || strings.TrimSpace(input.TenantID) == "" || input.AfterSequence < 0 || input.Limit <= 0 || input.Limit > 500 {
		writeProblem(writer, http.StatusBadRequest, "IAM_TELEMETRY_REVOCATION_REQUEST_INVALID", "The Telemetry revocation request is invalid.")
		return http.StatusBadRequest
	}
	facts, err := h.telemetryGrantStore.PollRevocations(request.Context(), input.TenantID, input.AfterSequence, input.Limit)
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_TELEMETRY_REVOCATION_UNAVAILABLE", "Telemetry revocation facts are unavailable.")
		return http.StatusServiceUnavailable
	}
	next := input.AfterSequence
	if len(facts) > 0 {
		next = facts[len(facts)-1].Sequence
	}
	writeJSON(writer, http.StatusOK, telemetryRevocationPollResponse{Facts: facts, NextSequence: next})
	return http.StatusOK
}
