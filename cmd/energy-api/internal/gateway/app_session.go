package gateway

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
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

// identityAppLogin is identity-service's /app/login request, field for field.
type identityAppLogin struct {
	ClientID      string `json:"clientId"`
	RedirectURI   string `json:"redirectUri"`
	State         string `json:"state"`
	Nonce         string `json:"nonce"`
	CodeChallenge string `json:"codeChallenge"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

// identityAppTOTP is identity-service's /app/mfa/totp request.
type identityAppTOTP struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

const appStepTOTP = "totp"

// identityAppResult is identity-service's answer: a code for /token with the state it was
// issued for, or the one-time-code step.
type identityAppResult struct {
	Code      string `json:"code"`
	State     string `json:"state"`
	Step      string `json:"step"`
	Challenge string `json:"challenge"`
	Error     string `json:"error"`
}

var (
	appStateStoreFailure = identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "Sign-in unavailable", "The Gateway could not keep or read the pending sign-in.", true}
	appLoginExpired      = identityFailure{400, "LOGIN_EXPIRED", "Sign-in expired", "The pending sign-in is missing, expired, or already used; start again.", false}
	appStateMismatch     = identityFailure{401, "OIDC_STATE_INVALID", "Sign-in failed", "The identity service answered for another sign-in.", false}
)

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
	result, failure := h.identity.callIdentityApp(request, "/app/login", identityAppLogin{
		ClientID: h.identity.config.OIDCClientID, RedirectURI: h.identity.config.OIDCRedirectURI,
		State: stateKey, Nonce: state.Nonce, CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]),
		Username: input.Username, Password: input.Password,
	})
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if result.Step == appStepTOTP {
		state.Challenge = result.Challenge
		if err := h.identity.states.Put(request.Context(), stateKey, state, h.identity.config.StateTTL); err != nil {
			writeIdentityFailure(writer, request, appStateStoreFailure)
			return
		}
		writeJSON(writer, http.StatusAccepted, appPendingResponse{Step: appStepTOTP, PendingLogin: stateKey})
		return
	}
	h.finishAppSession(writer, request, stateKey, result, state)
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
	if errors.Is(err, ErrLoginStateNotFound) || (err == nil && (state.Challenge == "" || h.identity.now().Sub(state.CreatedAt) >= h.identity.config.StateTTL)) {
		writeIdentityFailure(writer, request, appLoginExpired)
		return
	}
	if err != nil {
		writeIdentityFailure(writer, request, appStateStoreFailure)
		return
	}
	result, failure := h.identity.callIdentityApp(request, "/app/mfa/totp", identityAppTOTP{Challenge: state.Challenge, Code: input.Code})
	if failure != nil {
		if failure.code == "TOTP_INVALID" {
			// A mistyped code may be tried again within the sign-in's time; identity-service limits the attempts.
			remaining := h.identity.config.StateTTL - h.identity.now().Sub(state.CreatedAt)
			if remaining <= 0 {
				failure = &appLoginExpired
			} else if err := h.identity.states.Put(request.Context(), input.PendingLogin, state, remaining); err != nil {
				failure = &appStateStoreFailure
			}
		}
		writeIdentityFailure(writer, request, *failure)
		return
	}
	h.finishAppSession(writer, request, input.PendingLogin, result, state)
}

// finishAppSession redeems the code identity-service issued for this sign-in's state.
func (h *handler) finishAppSession(writer http.ResponseWriter, request *http.Request, stateKey string, result identityAppResult, state loginState) {
	if subtle.ConstantTimeCompare([]byte(result.State), []byte(stateKey)) != 1 {
		writeIdentityFailure(writer, request, appStateMismatch)
		return
	}
	stored, failure := h.identity.signIn(request, sessionstore.ClientMiniProgram, result.Code, state)
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

// backchannel sends one request to identity-service on the internal network and reads its answer.
func (controller *identityController) backchannel(request *http.Request, method, path string, body any, header map[string]string) (int, []byte, error) {
	base := strings.TrimSpace(controller.config.OIDCBackchannelBaseURL)
	if base == "" {
		base = controller.config.OIDCIssuer
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	outbound, err := http.NewRequestWithContext(request.Context(), method, strings.TrimRight(base, "/")+path, payload)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		outbound.Header.Set("Content-Type", "application/json")
	}
	for name, value := range header {
		outbound.Header.Set(name, value)
	}
	response, err := controller.config.OIDCHTTPClient.Do(outbound)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, reply, nil
}

// callIdentityApp posts one sign-in step to identity-service over the backchannel.
func (controller *identityController) callIdentityApp(request *http.Request, path string, body any) (identityAppResult, *identityFailure) {
	status, reply, err := controller.backchannel(request, http.MethodPost, path, body, nil)
	if err != nil {
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service could not be reached.", true}
	}
	var result identityAppResult
	if err := json.Unmarshal(reply, &result); err != nil {
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service answered with an invalid body.", true}
	}
	switch {
	case status == http.StatusOK && (result.Code != "" || (result.Step == appStepTOTP && result.Challenge != "")):
		return result, nil
	case result.Error == "invalid_credentials":
		return identityAppResult{}, &identityFailure{401, "INVALID_CREDENTIALS", "Sign-in failed", "The username or password is wrong, or the account cannot sign in.", false}
	case result.Error == "totp_invalid":
		return identityAppResult{}, &identityFailure{401, "TOTP_INVALID", "Sign-in failed", "The one-time code is wrong.", false}
	case result.Error == "expired":
		return identityAppResult{}, &appLoginExpired
	default:
		return identityAppResult{}, &identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "Sign-in unavailable", "The identity service could not complete the sign-in.", true}
	}
}
