package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

func (h *handler) GetSessionAuditEvent(writer http.ResponseWriter, request *http.Request, params platformapi.GetSessionAuditEventParams) {
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), session)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !containsAuditReaderRole(principal.Principal.Roles) {
		writeIdentityFailure(writer, request, identityFailure{403, "AUDIT_QUERY_FORBIDDEN", "Audit query forbidden", "The authenticated principal cannot read Organization audit records.", false})
		return
	}
	if h.identity.config.AuditURL == "" || h.identity.config.AuditHTTPClient == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_LEDGER_UNAVAILABLE", "Audit Ledger unavailable", "The private Audit Ledger service is not configured.", true})
		return
	}
	now := h.identity.now()
	expiry := now.Add(h.identity.config.DelegationTTL)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:               h.identity.config.ExecutingWorkloadSPIFFE,
		Subject:              principal.Principal.Subject,
		SubjectIssuer:        principal.Principal.Issuer,
		DisplayName:          principal.Principal.DisplayName,
		Email:                principal.Principal.Email,
		Roles:                append([]string(nil), principal.Principal.Roles...),
		ExecutingService:     h.identity.config.ExecutingWorkloadSPIFFE,
		Audience:             h.identity.config.AuditAudience,
		ActingOrganizationID: principal.Context.ActingOrganizationID,
		Actions:              []string{"audit:read"},
		Scopes:               []string{"organization:" + principal.Context.ActingOrganizationID},
		PolicyRevision:       principal.Context.PolicyRevision,
		SessionID:            session.ID,
		IssuedAt:             now.Unix(),
		ExpiresAt:            expiry.Unix(),
		TokenID:              randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(h.identity.config.DelegationSigner, claims)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_DELEGATION_SIGNING_FAILED", "Audit query unavailable", "The Gateway could not create the constrained Audit delegation.", true})
		return
	}
	endpoint := strings.TrimRight(h.identity.config.AuditURL, "/") + "/internal/v1/audit/session-events/" + url.PathEscape(params.MessageID)
	internalRequest, _ := http.NewRequestWithContext(request.Context(), http.MethodGet, endpoint, nil)
	internalRequest.Header.Set("Accept", "application/json, application/problem+json")
	internalRequest.Header.Set("X-Delegation-Grant", grant)
	response, err := h.identity.config.AuditHTTPClient.Do(internalRequest)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_LEDGER_UNAVAILABLE", "Audit Ledger unavailable", "The private Audit Ledger service could not be reached.", true})
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		writeIdentityFailure(writer, request, identityFailure{404, "AUDIT_RECORD_NOT_FOUND", "Audit record not found", "No audit record is visible for this Organization and message ID.", false})
		return
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
		writeIdentityFailure(writer, request, identityFailure{403, "AUDIT_QUERY_FORBIDDEN", "Audit query forbidden", "The private Audit Ledger rejected the delegated query.", false})
		return
	}
	if response.StatusCode != http.StatusOK {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_QUERY_UNAVAILABLE", "Audit query unavailable", "The private Audit Ledger could not complete the query.", true})
		return
	}
	var record platformapi.AuditRecord
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&record); err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_RESPONSE_INVALID", "Audit response invalid", "The private Audit Ledger returned an invalid response.", true})
		return
	}
	if record.MessageID != params.MessageID || record.OrganizationID != session.ActingOrganizationID || record.ActingOrganizationID != session.ActingOrganizationID {
		writeIdentityFailure(writer, request, identityFailure{503, "AUDIT_RESPONSE_INVALID", "Audit response invalid", "The private Audit Ledger response violated the Organization boundary.", true})
		return
	}
	writeJSON(writer, http.StatusOK, record)
}

func containsAuditReaderRole(roles []string) bool {
	for _, role := range roles {
		if role == "audit-reader" || role == "platform-admin" {
			return true
		}
	}
	return false
}
