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

const internalIAMAdminMutationPath = "/internal/v1/admin/mutations"

// forwardIAMAdmin posts one IAM administration request to internalPath as the session's
// Principal, under a short delegation that only allows "iam:admin".
func (controller *identityController) forwardIAMAdmin(ctx context.Context, session bffSession, internalPath string, payload []byte) (int, []byte, *identityFailure) {
	ctx, span := observability.Start(ctx, "http.iam.admin", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodPost,
		"server.service":      "iam-service",
		"rpc.operation":       "iam:admin",
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
		Actions:          []string{"iam:admin"},
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
		return 0, nil, &failure
	}
	internalRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controller.config.IAMURL, "/")+internalPath, bytes.NewReader(payload))
	internalRequest.Header.Set("X-Delegation-Grant", grant)
	internalRequest.Header.Set("Content-Type", "application/json")
	internalRequest.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(ctx, internalRequest.Header)
	response, err := controller.config.IAMHTTPClient.Do(internalRequest)
	if err != nil {
		failure := identityFailure{503, "IAM_UNAVAILABLE", "IAM unavailable", "The private IAM service could not be reached.", true}
		return 0, nil, &failure
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		failure := identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an unreadable administration response.", true}
		return 0, nil, &failure
	}
	return response.StatusCode, responseBody, nil
}
