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

// appClient is what every mini program sign-in carries for the browser flow's /token.
type appClient struct {
	ClientID      string `json:"clientId"`
	RedirectURI   string `json:"redirectUri"`
	State         string `json:"state"`
	Nonce         string `json:"nonce"`
	CodeChallenge string `json:"codeChallenge"`
}

type appLoginRequest struct {
	appClient
	Username string `json:"username"`
	Password string `json:"password"`
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
	if !decodeAppRequest(writer, request, 16<<10, &input) || !server.validAppClient(writer, input.appClient) {
		return
	}
	challenge, now, ok := server.startAppLogin(writer, request, input.appClient)
	if !ok {
		return
	}
	result, err := server.store.CompleteLogin(request.Context(), hashOpaque(challenge), input.Username, input.Password, now)
	if errors.Is(err, ErrInvalidCredentials) {
		writeOAuthError(writer, http.StatusUnauthorized, "invalid_credentials", "the username or password is wrong, or the account cannot sign in")
		return
	}
	writeAppLoginResult(writer, challenge, result, err)
}

func (server *Server) validAppClient(writer http.ResponseWriter, client appClient) bool {
	if client.ClientID != server.clientID || client.RedirectURI != server.redirectURI || client.State == "" || client.Nonce == "" || client.CodeChallenge == "" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "client, redirect, state, nonce, and PKCE challenge are required")
		return false
	}
	return true
}

// startAppLogin keeps the sign-in's authorization request, as /authorize does for the browser.
func (server *Server) startAppLogin(writer http.ResponseWriter, request *http.Request, client appClient) (string, time.Time, bool) {
	now := server.now().UTC()
	challenge := randomToken(32)
	if err := server.store.CreateAuthorizationRequest(request.Context(), AuthorizationRequest{
		ChallengeHash: hashOpaque(challenge), ClientID: server.clientID, RedirectURI: server.redirectURI,
		State: client.State, Nonce: client.Nonce, CodeChallenge: client.CodeChallenge,
		Scope: "openid profile email", RequiredACR: AssuranceBasic, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return "", time.Time{}, false
	}
	return challenge, now, true
}

// writeAppLoginResult answers a first factor the account passed: its code, or the one-time-code step.
func writeAppLoginResult(writer http.ResponseWriter, challenge string, result LoginResult, err error) {
	switch {
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
