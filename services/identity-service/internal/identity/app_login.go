package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Sign-in for the platform's own WeChat mini program (#664). The mini program cannot follow the
// browser's redirects and cookies, so the Gateway signs in on its behalf over the internal
// network: it sends the same client, redirect, state, nonce and PKCE challenge as the browser
// flow and redeems the code at /token with its verifier, exactly as after /authorize. Passwords,
// lockout, disabled accounts and one-time codes are the ones /login and /mfa/totp apply.
// nginx does not route /identity/app/, so only the Gateway reaches these.

type appLoginRequest struct {
	ClientID      string `json:"clientId"`
	RedirectURI   string `json:"redirectUri"`
	State         string `json:"state"`
	Nonce         string `json:"nonce"`
	CodeChallenge string `json:"codeChallenge"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

type appTOTPRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

// appLoginResult is a code for /token, or the second step: a one-time code for this challenge.
type appLoginResult struct {
	Code      string `json:"code,omitempty"`
	State     string `json:"state,omitempty"`
	Step      string `json:"step,omitempty"`
	Challenge string `json:"challenge,omitempty"`
}

func (server *Server) appLogin(writer http.ResponseWriter, request *http.Request) {
	var input appLoginRequest
	if !decodeAppRequest(writer, request, 16<<10, &input) {
		return
	}
	if input.ClientID != server.clientID || input.RedirectURI != server.redirectURI || input.State == "" || input.Nonce == "" || input.CodeChallenge == "" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "client, redirect, state, nonce, and PKCE challenge are required")
		return
	}
	now := server.now().UTC()
	challenge := randomToken(32)
	if err := server.store.CreateAuthorizationRequest(request.Context(), AuthorizationRequest{
		ChallengeHash: hashOpaque(challenge), ClientID: server.clientID, RedirectURI: server.redirectURI,
		State: input.State, Nonce: input.Nonce, CodeChallenge: input.CodeChallenge,
		Scope: "openid profile email", RequiredACR: AssuranceBasic, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return
	}
	result, err := server.store.CompleteLogin(request.Context(), hashOpaque(challenge), input.Username, input.Password, now)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeOAuthError(writer, http.StatusUnauthorized, "invalid_credentials", "the username or password is wrong, or the account cannot sign in")
	case errors.Is(err, ErrMFARequired) && result.MFARequired:
		writeJSON(writer, http.StatusOK, appLoginResult{Step: "totp", Challenge: challenge})
	case err != nil:
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
	default:
		writeJSON(writer, http.StatusOK, appLoginResult{Code: result.Grant.Code, State: result.Grant.State})
	}
}

func (server *Server) appTOTP(writer http.ResponseWriter, request *http.Request) {
	var input appTOTPRequest
	if !decodeAppRequest(writer, request, 4<<10, &input) {
		return
	}
	if input.Challenge == "" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "challenge is required")
		return
	}
	grant, err := server.store.CompleteTOTP(request.Context(), hashOpaque(input.Challenge), input.Code, server.mfaProtector, server.now().UTC())
	switch {
	case errors.Is(err, ErrMFAInvalid):
		writeOAuthError(writer, http.StatusUnauthorized, "totp_invalid", "the one-time code is wrong")
	case errors.Is(err, ErrAuthorizationRequestExpired):
		writeOAuthError(writer, http.StatusBadRequest, "expired", "the sign-in has expired; start again")
	case err != nil:
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
	default:
		writeJSON(writer, http.StatusOK, appLoginResult{Code: grant.Code, State: grant.State})
	}
}

func decodeAppRequest(writer http.ResponseWriter, request *http.Request, limit int64, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil || decoder.More() {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return false
	}
	return true
}
