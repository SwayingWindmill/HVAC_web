package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	publicIAMAdminMutationPath      = "/api/v1/iam/admin/mutations"
	publicAPICredentialCreatePath   = "/api/v1/iam/api-credentials/create"
	publicAPICredentialRotatePath   = "/api/v1/iam/api-credentials/rotate"
	publicAPICredentialRevokePath   = "/api/v1/iam/api-credentials/revoke"
	internalIAMAdminMutationPath    = "/internal/v1/admin/mutations"
	internalAPICredentialCreatePath = "/internal/v1/admin/api-credentials/create"
	internalAPICredentialRotatePath = "/internal/v1/admin/api-credentials/rotate"
	internalAPICredentialRevokePath = "/internal/v1/admin/api-credentials/revoke"
	maximumIAMAdminRequestBody      = 64 << 10
)

type iamAdminRoute struct {
	internalPath string
	action       string
	capability   identitycontext.Capability
}

func (h *handler) IAMAdmin(writer http.ResponseWriter, request *http.Request, route iamAdminRoute) {
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if failure := h.identity.validateStateChange(request, session, request.Header.Get("X-CSRF-Token")); failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !freshMFAAssurance(session, h.now()) {
		writeIdentityFailure(writer, request, identityFailure{428, "STEP_UP_REQUIRED", "Step-up authentication required", "IAM administration requires recent multi-factor authentication.", false})
		return
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), session)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !principal.Authorization.Has(route.capability) {
		writeIdentityFailure(writer, request, identityFailure{403, "IAM_ADMIN_FORBIDDEN", "IAM administration forbidden", "The authenticated Principal is not allowed to perform this IAM administration action.", false})
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumIAMAdminRequestBody)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{400, "IAM_ADMIN_REQUEST_INVALID", "IAM administration request invalid", "The IAM administration request body is invalid.", false})
		return
	}
	status, responseBody, contentType, failure := h.identity.forwardIAMAdmin(request.Context(), session, route, payload)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(responseBody)
}

func (controller *identityController) forwardIAMAdmin(ctx context.Context, session bffSession, route iamAdminRoute, payload []byte) (int, []byte, string, *identityFailure) {
	ctx, span := observability.Start(ctx, "http.iam.admin", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodPost,
		"server.service":      "iam-service",
		"rpc.operation":       route.action,
	})
	defer span.End()
	now := controller.now()
	expiry := now.Add(controller.config.DelegationTTL)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           controller.config.ExecutingWorkloadSPIFFE,
		Subject:          session.Principal.Subject,
		SubjectIssuer:    session.Principal.Issuer,
		DisplayName:      session.Principal.DisplayName,
		Email:            session.Principal.Email,
		Roles:            append([]string(nil), session.Principal.Roles...),
		ExecutingService: controller.config.ExecutingWorkloadSPIFFE,
		Audience:         controller.config.IAMAudience,
		TenantID:         session.TenantID,
		Actions:          []string{route.action},
		Scopes:           []string{"session:" + session.ID},
		PolicyRevision:   controller.config.PolicyRevision,
		SessionID:        session.ID,
		IssuedAt:         now.Unix(),
		ExpiresAt:        expiry.Unix(),
		TokenID:          randomURLToken(16),
	}
	grant, err := identitycontext.SignDelegation(controller.config.DelegationSigner, claims)
	if err != nil {
		failure := identityFailure{503, "DELEGATION_SIGNING_FAILED", "IAM administration unavailable", "The Gateway could not create a constrained IAM administration delegation.", true}
		return 0, nil, "", &failure
	}
	internalRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controller.config.IAMURL, "/")+route.internalPath, bytes.NewReader(payload))
	internalRequest.Header.Set("X-Delegation-Grant", grant)
	internalRequest.Header.Set("Content-Type", "application/json")
	internalRequest.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(ctx, internalRequest.Header)
	response, err := controller.config.IAMHTTPClient.Do(internalRequest)
	if err != nil {
		failure := identityFailure{503, "IAM_UNAVAILABLE", "IAM unavailable", "The private IAM service could not be reached.", true}
		return 0, nil, "", &failure
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		failure := identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an unreadable administration response.", true}
		return 0, nil, "", &failure
	}
	return response.StatusCode, responseBody, response.Header.Get("Content-Type"), nil
}
