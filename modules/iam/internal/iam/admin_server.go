package iam

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type apiCredentialRotateRequest struct {
	CredentialID     string    `json:"credentialId"`
	ExpectedRevision int64     `json:"expectedRevision"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type apiCredentialRevokeRequest struct {
	CredentialID     string `json:"credentialId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func (h *handler) handleAdminRoute(writer http.ResponseWriter, request *http.Request, claims identitycontext.DelegationClaims) int {
	if h.adminStore == nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_ADMIN_UNAVAILABLE", "IAM administration is unavailable.")
		return http.StatusServiceUnavailable
	}
	required := identitycontext.CapabilityIAMAdmin
	if request.URL.Path != AdminMutationPath {
		required = identitycontext.CapabilityAPICredentialManage
	}
	authorization, err := h.principalCapabilityResolver.ResolvePrincipalCapabilities(request.Context(), PrincipalCapabilityLookup{
		SubjectIssuer: claims.SubjectIssuer,
		Subject:       claims.Subject,
		TenantID:      claims.TenantID,
	})
	if err != nil {
		writeProblem(writer, http.StatusServiceUnavailable, "IAM_ADMIN_AUTHORIZATION_UNAVAILABLE", "IAM administration authorization is unavailable.")
		return http.StatusServiceUnavailable
	}
	if !authorization.Has(required) {
		writeProblem(writer, http.StatusForbidden, "IAM_ADMIN_FORBIDDEN", "The initiating Principal cannot perform this IAM administration action.")
		return http.StatusForbidden
	}
	actor := AdminActor{
		SubjectIssuer: claims.SubjectIssuer,
		Subject:       claims.Subject,
		TenantID:      claims.TenantID,
		CorrelationID: claims.TokenID,
		TraceID:       observability.TraceID(request.Context()),
		OccurredAt:    h.now().UTC(),
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	switch request.URL.Path {
	case AdminMutationPath:
		var input AdminMutationRequest
		if err := decodeAdminJSON(request, &input); err != nil {
			writeProblem(writer, http.StatusBadRequest, "IAM_ADMIN_REQUEST_INVALID", "IAM administration request is invalid.")
			return http.StatusBadRequest
		}
		result, err := h.adminStore.ApplyMutation(request.Context(), actor, input)
		return writeAdminMutationResult(writer, result, err)
	case APICredentialCreatePath:
		var input APICredentialCreateRequest
		if err := decodeAdminJSON(request, &input); err != nil {
			writeProblem(writer, http.StatusBadRequest, "API_CREDENTIAL_REQUEST_INVALID", "API Credential request is invalid.")
			return http.StatusBadRequest
		}
		result, err := h.adminStore.CreateAPICredential(request.Context(), actor, input)
		if err != nil {
			return writeAdminError(writer, err)
		}
		writeJSON(writer, http.StatusCreated, result)
		return http.StatusCreated
	case APICredentialRotatePath:
		var input apiCredentialRotateRequest
		if err := decodeAdminJSON(request, &input); err != nil || strings.TrimSpace(input.CredentialID) == "" {
			writeProblem(writer, http.StatusBadRequest, "API_CREDENTIAL_REQUEST_INVALID", "API Credential rotation request is invalid.")
			return http.StatusBadRequest
		}
		result, err := h.adminStore.RotateAPICredential(request.Context(), actor, input.CredentialID, input.ExpectedRevision, input.ExpiresAt)
		if err != nil {
			return writeAdminError(writer, err)
		}
		writeJSON(writer, http.StatusOK, result)
		return http.StatusOK
	case APICredentialRevokePath:
		var input apiCredentialRevokeRequest
		if err := decodeAdminJSON(request, &input); err != nil || strings.TrimSpace(input.CredentialID) == "" {
			writeProblem(writer, http.StatusBadRequest, "API_CREDENTIAL_REQUEST_INVALID", "API Credential revocation request is invalid.")
			return http.StatusBadRequest
		}
		result, err := h.adminStore.RevokeAPICredential(request.Context(), actor, input.CredentialID, input.ExpectedRevision)
		return writeAdminMutationResult(writer, result, err)
	default:
		writeProblem(writer, http.StatusNotFound, "IAM_ROUTE_NOT_FOUND", "The requested IAM route does not exist.")
		return http.StatusNotFound
	}
}

func decodeAdminJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func writeAdminMutationResult(writer http.ResponseWriter, result AdminMutationResult, err error) int {
	if err != nil {
		return writeAdminError(writer, err)
	}
	writeJSON(writer, http.StatusOK, result)
	return http.StatusOK
}

func writeAdminError(writer http.ResponseWriter, err error) int {
	switch {
	case errors.Is(err, ErrAdminRevisionConflict):
		writeProblem(writer, http.StatusConflict, "IAM_ADMIN_REVISION_CONFLICT", "The IAM resource revision is stale.")
		return http.StatusConflict
	case errors.Is(err, ErrAdminResourceNotFound):
		writeProblem(writer, http.StatusNotFound, "IAM_ADMIN_RESOURCE_NOT_FOUND", "The IAM resource was not found.")
		return http.StatusNotFound
	default:
		writeProblem(writer, http.StatusBadRequest, "IAM_ADMIN_REQUEST_REJECTED", "The IAM administration request was rejected.")
		return http.StatusBadRequest
	}
}
