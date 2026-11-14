package identity

import (
	"errors"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	MFATOTPVerifyAudience    = "identity-service"
	MFATOTPVerifyAction      = "mfa:totp:verify"
	InternalMFATOTPVerifyURL = "/internal/v1/mfa/totp/verify"
)

type internalTOTPVerifyRequest struct {
	Code string `json:"code"`
}

func (server *Server) verifyInternalTOTP(writer http.ResponseWriter, request *http.Request) {
	claims, err := identitycontext.VerifyDelegation(server.gatewayKey, request.Header.Get("X-Delegation-Grant"))
	if err == nil {
		err = identitycontext.ValidateDelegationFromIssuer(
			claims,
			server.now(),
			server.gatewaySPIFFE,
			server.gatewaySPIFFE,
			MFATOTPVerifyAudience,
			MFATOTPVerifyAction,
			"session:"+claims.SessionID,
		)
	}
	if err == nil && (claims.SubjectIssuer != server.issuer || !validUserID(claims.Subject)) {
		err = errors.New("the grant names an account this identity service does not keep")
	}
	if err != nil {
		writeOAuthError(writer, http.StatusUnauthorized, "access_denied", "a Gateway grant for the signed-in account is required")
		return
	}

	var input internalTOTPVerifyRequest
	if !decodeAppRequest(writer, request, 4<<10, &input) {
		return
	}
	passcode := strings.TrimSpace(input.Code)
	if !isSixDigitTOTP(passcode) {
		writeOAuthError(writer, http.StatusBadRequest, "mfa_code_invalid", "a 6-digit verification code is required")
		return
	}

	verifier := server.totpVerifier
	if verifier == nil {
		if server.store == nil || server.mfaProtector == nil {
			writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity MFA verifier is unavailable")
			return
		}
		verifier = server.store
	}

	err = verifier.VerifyTOTPByUserID(request.Context(), claims.Subject, passcode, server.mfaProtector, server.now())
	switch {
	case errors.Is(err, ErrMFANotConfigured):
		writeOAuthError(writer, http.StatusPreconditionFailed, "mfa_not_configured", "authenticator TOTP MFA is not active on this account")
	case errors.Is(err, ErrMFALocked):
		writeOAuthError(writer, http.StatusLocked, "mfa_locked", "authenticator verification is temporarily locked after repeated invalid codes")
	case errors.Is(err, ErrMFAReplayed):
		writeOAuthError(writer, http.StatusConflict, "mfa_code_replayed", "this 6-digit verification code has already been used")
	case errors.Is(err, ErrMFAInvalid):
		writeOAuthError(writer, http.StatusUnauthorized, "mfa_code_invalid", "the 6-digit verification code is invalid")
	case err != nil:
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
	default:
		writeJSON(writer, http.StatusOK, map[string]any{"verified": true, "method": "TOTP"})
	}
}
