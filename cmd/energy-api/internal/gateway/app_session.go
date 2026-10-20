package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

// The WeChat mini program cannot follow the browser's sign-in redirects or keep its cookie, so it
// signs in here (#664). The Gateway asks identity-service on the internal network to check the
// password with the same client, redirect, state, nonce and PKCE challenge as the browser flow,
// redeems the code it gets back exactly like the browser callback, and returns the Session for
// the mini program to send in the Authorization header.

const (
	publicAppSessionsPath = "/api/v1/auth/app/sessions"
	publicAppSessionTOTP  = "/api/v1/auth/app/sessions/totp"
)

type appPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type appTOTPRequest struct {
	PendingLogin string `json:"pendingLogin"`
	Code         string `json:"code"`
}

type appSessionResponse struct {
	SessionToken string `json:"sessionToken"`
	ExpiresAt    string `json:"expiresAt"`
}

// appPendingResponse asks for the account's one-time code; pendingLogin carries the sign-in on.
type appPendingResponse struct {
	Step         string `json:"step"`
	PendingLogin string `json:"pendingLogin"`
}

// identityAppResult is identity-service's answer: a code for /token, or the one-time-code step.
type identityAppResult struct {
	Code      string `json:"code"`
	Step      string `json:"step"`
	Challenge string `json:"challenge"`
	Error     string `json:"error"`
}

func (h *handler) CreateAppSession(writer http.ResponseWriter, request *http.Request) {
	if !h.appSignInAvailable(writer, request) {
		return
	}
	var input appPasswordRequest
	if !decodeAppSignIn(writer, request, &input) {
		return
	}
	state := loginState{Verifier: randomURLToken(48), Nonce: randomURLToken(24), RequiredACR: authenticationACRBasic, CreatedAt: h.identity.now()}
	stateKey := randomURLToken(24)
	digest := sha256.Sum256([]byte(state.Verifier))
	result, failure := h.identity.callIdentityApp(request, "/app/login", map[string]string{
		"clientId": h.identity.config.OIDCClientID, "redirectUri": h.identity.config.OIDCRedirectURI,
		"state": stateKey, "nonce": state.Nonce, "codeChallenge": base64.RawURLEncoding.EncodeToString(digest[:]),
		"username": input.Username, "password": input.Password,
	})
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if result.Step == "totp" {
		state.Challenge = result.Challenge
		if err := h.identity.states.Put(request.Context(), stateKey, state, h.identity.config.StateTTL); err != nil {
			writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "Sign-in unavailable", "The Gateway could not keep the pending sign-in.", true})
			return
		}
		writeJSON(writer, http.StatusAccepted, appPendingResponse{Step: "totp", PendingLogin: stateKey})
		return
	}
	h.finishAppSession(writer, request, result.Code, state)
}

func (h *handler) CompleteAppSessionTOTP(writer http.ResponseWriter, request *http.Request) {
	if !h.appSignInAvailable(writer, request) {
		return
	}
	var input appTOTPRequest
	if !decodeAppSignIn(writer, request, &input) {
		return
	}
	state, err := h.identity.states.Consume(request.Context(), input.PendingLogin)
	if errors.Is(err, ErrLoginStateNotFound) || (err == nil && (state.Challenge == "" || h.identity.now().Sub(state.CreatedAt) > h.identity.config.StateTTL)) {
		writeIdentityFailure(writer, request, identityFailure{400, "LOGIN_EXPIRED", "Sign-in expired", "The pending sign-in is missing, expired, or already used; start again.", false})
		return
	}
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "Sign-in unavailable", "The Gateway could not read the pending sign-in.", true})
		return
	}
	result, failure := h.identity.callIdentityApp(request, "/app/mfa/totp", map[string]string{"challenge": state.Challenge, "code": input.Code})
	if failure != nil {
		if failure.code == "TOTP_INVALID" {
			// A mistyped code may be tried again; identity-service limits the attempts.
			if err := h.identity.states.Put(request.Context(), input.PendingLogin, state, h.identity.config.StateTTL-h.identity.now().Sub(state.CreatedAt)); err != nil {
				failure = &identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "Sign-in unavailable", "The Gateway could not keep the pending sign-in.", true}
			}
		}
		writeIdentityFailure(writer, request, *failure)
		return
	}
	h.finishAppSession(writer, request, result.Code, state)
}

func (h *handler) finishAppSession(writer http.ResponseWriter, request *http.Request, code string, state loginState) {
	stored, failure := h.identity.signIn(request, sessionstore.ClientMiniProgram, code, state)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("X-Audit-Message-ID", stored.LastAuditMessageID)
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusCreated, appSessionResponse{SessionToken: stored.ID, ExpiresAt: stored.ExpiresAt.UTC().Format(time.RFC3339)})
}

func (h *handler) appSignInAvailable(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodPost {
		writeMethodNotAllowedFor(writer, request, http.MethodPost)
		return false
	}
	if h.identity == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true})
		return false
	}
	if h.identity.states == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "Sign-in unavailable", "The shared login state store is not configured.", true})
		return false
	}
	return true
}

func decodeAppSignIn(writer http.ResponseWriter, request *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil || ensureJSONEOF(decoder) != nil {
		writeIdentityFailure(writer, request, identityFailure{400, "REQUEST_INVALID", "Request invalid", "The sign-in request body is invalid.", false})
		return false
	}
	return true
}

// callIdentityApp posts one sign-in step to identity-service over the backchannel.
func (controller *identityController) callIdentityApp(request *http.Request, path string, body map[string]string) (identityAppResult, *identityFailure) {
	base := strings.TrimSpace(controller.config.OIDCBackchannelBaseURL)
	if base == "" {
		base = controller.config.OIDCIssuer
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The sign-in request could not be encoded.", true}
	}
	outbound, err := http.NewRequestWithContext(request.Context(), http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service address is invalid.", true}
	}
	outbound.Header.Set("Content-Type", "application/json")
	response, err := controller.config.OIDCHTTPClient.Do(outbound)
	if err != nil {
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service could not be reached.", true}
	}
	defer response.Body.Close()
	var result identityAppResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&result); err != nil {
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service answered with an invalid body.", true}
	}
	switch {
	case response.StatusCode == http.StatusOK && (result.Code != "" || (result.Step == "totp" && result.Challenge != "")):
		return result, nil
	case result.Error == "invalid_credentials":
		return identityAppResult{}, &identityFailure{401, "INVALID_CREDENTIALS", "Sign-in failed", "The username or password is wrong, or the account cannot sign in.", false}
	case result.Error == "totp_invalid":
		return identityAppResult{}, &identityFailure{401, "TOTP_INVALID", "Sign-in failed", "The one-time code is wrong.", false}
	case result.Error == "expired":
		return identityAppResult{}, &identityFailure{400, "LOGIN_EXPIRED", "Sign-in expired", "The sign-in has expired; start again.", false}
	default:
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service could not complete the sign-in.", true}
	}
}
