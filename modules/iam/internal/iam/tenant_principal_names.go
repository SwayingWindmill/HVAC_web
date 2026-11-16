package iam

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// PrincipalNamesPath resolves display names for Principals who hold an active membership in the
// caller's Tenant, so operational views can label actors without granting iam.admin.
const PrincipalNamesPath = "/internal/v1/tenant/principal-names"

const (
	principalNamesAction        = "iam:principal:read"
	maximumTenantPrincipalNames = 200
)

type TenantPrincipalNamesRequest struct {
	TenantID     string   `json:"tenantId,omitempty"`
	PrincipalIDs []string `json:"principalIds"`
}

type TenantPrincipalNamesResponse struct {
	Names map[string]string `json:"names"`
}

func (h *handler) handleTenantPrincipalNames(writer http.ResponseWriter, request *http.Request, claims identitycontext.DelegationClaims) int {
	if h.tenantPrincipalNameStore == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_PRINCIPAL_NAMES_UNAVAILABLE", "Tenant principal name lookup is unavailable.")
		return http.StatusServiceUnavailable
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var input TenantPrincipalNamesRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || len(input.PrincipalIDs) > maximumTenantPrincipalNames {
		writeProblem(writer, http.StatusBadRequest, "IAM_PRINCIPAL_NAMES_REQUEST_INVALID", "The tenant principal names request is invalid.")
		return http.StatusBadRequest
	}
	for _, id := range input.PrincipalIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || len(trimmed) > 256 {
			writeProblem(writer, http.StatusBadRequest, "IAM_PRINCIPAL_NAMES_REQUEST_INVALID", "Principal identifiers must be non-empty strings within 256 characters.")
			return http.StatusBadRequest
		}
	}
	tenantID := strings.TrimSpace(input.TenantID)
	if tenantID == "" {
		tenantID = claims.TenantID
	}
	if strings.TrimSpace(claims.TenantID) == "" || tenantID != claims.TenantID {
		writeProblem(writer, http.StatusForbidden, "IAM_TENANT_CONTEXT_MISMATCH", "The requested Tenant does not match the delegated Tenant.")
		return http.StatusForbidden
	}
	names, err := h.tenantPrincipalNameStore.LookupTenantPrincipalNames(request.Context(), tenantID, input.PrincipalIDs, h.now())
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_PRINCIPAL_NAMES_UNAVAILABLE", "Tenant principal names could not be loaded.")
		return http.StatusServiceUnavailable
	}
	if names == nil {
		names = map[string]string{}
	}
	writeJSON(writer, http.StatusOK, TenantPrincipalNamesResponse{Names: names})
	return http.StatusOK
}

func uniqueCanonicalUUIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if !isCanonicalUUID(normalized) {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func isCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := 0; index < len(value); index++ {
		ch := value[index]
		switch index {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			isHex := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
