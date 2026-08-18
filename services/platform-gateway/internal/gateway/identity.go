package gateway

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const sessionCookieName = "__Host-hvac_session"
const userActivityHeaderName = "X-HVAC-User-Activity"
const authenticationACRBasic = "urn:hvac:loa:1"
const authenticationACRMFA = "urn:hvac:loa:2"

type IdentityConfig struct {
	OIDCIssuer              string
	OIDCBackchannelBaseURL  string
	OIDCClientID            string
	OIDCRedirectURI         string
	PublicOrigin            string
	DefaultTenantID         string
	IAMURL                  string
	IAMAudience             string
	AuditURL                string
	AuditAudience           string
	ExecutingWorkloadSPIFFE string
	PolicyRevision          string
	OIDCHTTPClient          *http.Client
	IAMHTTPClient           *http.Client
	AuditHTTPClient         *http.Client
	DelegationSigner        crypto.Signer
	TokenEncryptionKey      []byte
	SessionStore            sessionstore.Store
	LoginStateStore         LoginStateStore
	SessionTTL              time.Duration
	IdleTTL                 time.Duration
	StateTTL                time.Duration
	DelegationTTL           time.Duration
	RevocationObjective     time.Duration
	ReadinessCheck          func(context.Context) error
}

type identityController struct {
	config   IdentityConfig
	now      func() time.Time
	vault    cipher.AEAD
	protocol oidcProtocol
	states   LoginStateStore
	store    sessionstore.Store
}

type loginState struct {
	Verifier    string
	Nonce       string
	ReturnTo    string
	RequiredACR string
	CreatedAt   time.Time
}

type bffSession struct {
	sessionstore.Session
	CSRFToken string
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type oidcTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

type oidcClaims struct {
	Issuer    string   `json:"iss"`
	Audience  string   `json:"aud"`
	Subject   string   `json:"sub"`
	ExpiresAt int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf"`
	Nonce     string   `json:"nonce"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	Roles     []string `json:"roles"`
	TenantID  string   `json:"tenantId"`
	TokenUse  string   `json:"token_use"`
	ACR       string   `json:"acr"`
	AMR       []string `json:"amr"`
	AuthTime  int64    `json:"auth_time"`
}

type identityFailure struct {
	status    int
	code      string
	title     string
	detail    string
	retryable bool
}

func (failure identityFailure) Error() string { return failure.code }

func newIdentityController(config *IdentityConfig, now func() time.Time) *identityController {
	if config == nil {
		return nil
	}
	resolved := *config
	if resolved.OIDCHTTPClient == nil {
		resolved.OIDCHTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if resolved.IAMHTTPClient == nil {
		resolved.IAMHTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if resolved.SessionTTL <= 0 {
		resolved.SessionTTL = 8 * time.Hour
	}
	if resolved.IdleTTL <= 0 {
		resolved.IdleTTL = time.Hour
	}
	if resolved.StateTTL <= 0 {
		resolved.StateTTL = 10 * time.Minute
	}
	if resolved.DelegationTTL <= 0 || resolved.DelegationTTL > time.Minute {
		resolved.DelegationTTL = 30 * time.Second
	}
	if resolved.RevocationObjective <= 0 {
		resolved.RevocationObjective = time.Second
	}
	if resolved.IAMAudience == "" {
		resolved.IAMAudience = "iam-service"
	}
	if resolved.AuditAudience == "" {
		resolved.AuditAudience = "audit-ledger-service"
	}
	if resolved.AuditURL != "" && resolved.AuditHTTPClient == nil {
		resolved.AuditHTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if resolved.PolicyRevision == "" {
		resolved.PolicyRevision = "policy-v1"
	}
	if resolved.SessionStore == nil {
		resolved.SessionStore = sessionstore.NewMemoryStore()
	}
	if len(resolved.TokenEncryptionKey) != 32 {
		panic("identity token encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(resolved.TokenEncryptionKey)
	if err != nil {
		panic(err)
	}
	vault, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	if resolved.OIDCIssuer == "" || resolved.OIDCClientID == "" || resolved.OIDCRedirectURI == "" || resolved.PublicOrigin == "" || resolved.IAMURL == "" || resolved.ExecutingWorkloadSPIFFE == "" || resolved.DelegationSigner == nil {
		panic("identity configuration is incomplete")
	}
	return &identityController{config: resolved, now: now, vault: vault, protocol: newStandardOIDCProtocol(), states: resolved.LoginStateStore, store: resolved.SessionStore}
}

func (h *handler) BeginLogin(writer http.ResponseWriter, request *http.Request, params platformapi.BeginLoginParams) {
	if h.identity == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true})
		return
	}
	if h.identity.states == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "OIDC login unavailable", "The shared OIDC login state store is not configured.", true})
		return
	}
	if !safeReturnTo(params.ReturnTo) {
		writeIdentityFailure(writer, request, identityFailure{400, "INVALID_RETURN_TO", "Invalid return target", "The returnTo value must be a local absolute path.", false})
		return
	}
	assurance := params.Assurance
	if assurance == "" {
		assurance = "normal"
	}
	requiredACR := authenticationACRBasic
	if assurance == "high" {
		requiredACR = authenticationACRMFA
	} else if assurance != "normal" {
		writeIdentityFailure(writer, request, identityFailure{400, "AUTHENTICATION_ASSURANCE_INVALID", "Invalid authentication assurance", "The assurance value must be normal or high.", false})
		return
	}
	discovery, failure := h.identity.discover(request.Context())
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	state := randomURLToken(24)
	nonce := randomURLToken(24)
	verifier := randomURLToken(48)
	authorizationURL, err := h.identity.protocol.AuthorizationURL(discovery, oidcAuthorizationRequest{
		ClientID:     h.identity.config.OIDCClientID,
		RedirectURI:  h.identity.config.OIDCRedirectURI,
		CodeVerifier: verifier,
		State:        state,
		Nonce:        nonce,
		LoginHint:    params.LoginHint,
		ACRValues:    requiredACR,
	})
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_AUTHORIZATION_REQUEST_INVALID", "OIDC login unavailable", "The Gateway could not construct the provider authorization request.", true})
		return
	}
	if err := h.identity.states.Put(request.Context(), state, loginState{Verifier: verifier, Nonce: nonce, ReturnTo: params.ReturnTo, RequiredACR: requiredACR, CreatedAt: h.identity.now()}, h.identity.config.StateTTL); err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "OIDC login unavailable", "The Gateway could not persist the shared login state.", true})
		return
	}
	http.Redirect(writer, request, authorizationURL, http.StatusFound)
}

func (h *handler) CompleteLogin(writer http.ResponseWriter, request *http.Request, params platformapi.CompleteLoginParams) {
	if h.identity == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true})
		return
	}
	if h.identity.states == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "OIDC login unavailable", "The shared OIDC login state store is not configured.", true})
		return
	}
	if params.Issuer != "" && params.Issuer != strings.TrimRight(h.identity.config.OIDCIssuer, "/") {
		writeIdentityFailure(writer, request, identityFailure{401, "OIDC_ISSUER_INVALID", "OIDC issuer invalid", "The authorization response issuer is not trusted.", false})
		return
	}
	state, err := h.identity.states.Consume(request.Context(), params.State)
	if errors.Is(err, ErrLoginStateNotFound) || (err == nil && h.identity.now().Sub(state.CreatedAt) > h.identity.config.StateTTL) {
		writeIdentityFailure(writer, request, identityFailure{400, "OIDC_STATE_INVALID", "OIDC state invalid", "The login state is missing, expired, or already used.", false})
		return
	}
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "OIDC_STATE_STORE_UNAVAILABLE", "OIDC login unavailable", "The Gateway could not consume the shared login state.", true})
		return
	}
	tokens, failure := h.identity.exchangeCode(request.Context(), params.Code, state.Verifier)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	claims, failure := h.identity.validateIDToken(request.Context(), tokens.IDToken, state.Nonce)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if tokens.TokenType != "Bearer" || (claims.TokenUse != "" && claims.TokenUse != "id") {
		writeIdentityFailure(writer, request, identityFailure{401, "OIDC_TOKEN_TYPE_INVALID", "OIDC token type invalid", "The identity provider returned an unsupported token type.", false})
		return
	}
	if !validAuthenticationAssurance(claims, state.RequiredACR, h.identity.now()) {
		writeIdentityFailure(writer, request, identityFailure{401, "OIDC_ASSURANCE_INVALID", "Authentication assurance invalid", "The identity provider did not satisfy the requested authentication assurance.", false})
		return
	}
	encryptedTokens, err := h.identity.encryptTokens(tokens)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "SESSION_TOKEN_STORE_FAILED", "Session unavailable", "The server could not protect the identity tokens.", true})
		return
	}
	csrfToken := randomURLToken(24)
	encryptedCSRF, err := h.identity.encryptBytes([]byte(csrfToken))
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "SESSION_TOKEN_STORE_FAILED", "Session unavailable", "The server could not protect the session secret.", true})
		return
	}
	now := h.identity.now()
	pending := bffSession{Session: sessionstore.Session{
		ID:                       randomURLToken(32),
		Principal:                identitycontext.UserPrincipal{Subject: claims.Subject, Issuer: claims.Issuer, DisplayName: claims.Name, Email: claims.Email, Roles: []string{}},
		TenantID:                 claims.TenantID,
		CSRFTokenCiphertext:      encryptedCSRF,
		ProviderTokensCiphertext: encryptedTokens,
		AuthenticationACR:        claims.ACR,
		AuthenticationAMR:        append([]string(nil), claims.AMR...),
		AuthenticationTime:       time.Unix(claims.AuthTime, 0).UTC(),
		ExpiresAt:                now.Add(h.identity.config.SessionTTL),
	}, CSRFToken: csrfToken}
	validated, validationFailure := h.identity.fetchPrincipal(request.Context(), pending)
	if validationFailure != nil {
		writeIdentityFailure(writer, request, *validationFailure)
		return
	}
	if validated.Principal.Subject != pending.Principal.Subject || validated.Principal.Issuer != pending.Principal.Issuer || validated.Context.TenantID != pending.TenantID {
		writeIdentityFailure(writer, request, identityFailure{503, "IAM_IDENTITY_MISMATCH", "Identity validation failed", "IAM returned a principal outside the authenticated Session boundary.", false})
		return
	}
	stored, err := h.identity.store.CreateSession(request.Context(), pending.Session, h.identity.mutationContext(request, "SESSION_CREATED"))
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "SESSION_PERSISTENCE_FAILED", "Session unavailable", "The authenticated session could not be committed with its audit intent.", true})
		return
	}
	writer.Header().Set("X-Audit-Message-ID", stored.LastAuditMessageID)
	http.SetCookie(writer, &http.Cookie{Name: sessionCookieName, Value: stored.ID, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(h.identity.config.SessionTTL.Seconds()), Expires: stored.ExpiresAt})
	http.Redirect(writer, request, state.ReturnTo, http.StatusFound)
}

func (h *handler) GetCurrentPrincipal(writer http.ResponseWriter, request *http.Request) {
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
	writeJSON(writer, http.StatusOK, platformapi.CurrentPrincipalResponse{Principal: toPublicUser(principal.Principal), Context: toPublicContext(principal.Context), Authorization: toPublicAuthorization(principal.Authorization), Session: platformapi.SessionView{ID: session.ID, ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339), IdleTimeoutMS: int(h.identity.config.IdleTTL.Milliseconds()), CSRFToken: session.CSRFToken, RevocationObjectiveMS: int(h.identity.config.RevocationObjective.Milliseconds()), LastAuditMessageID: session.LastAuditMessageID}})
}

func (h *handler) Logout(writer http.ResponseWriter, request *http.Request, params platformapi.LogoutParams) {
	session, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if failure := h.identity.validateStateChange(request, session, params.CSRFToken); failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	providerLogoutURL, failure := h.identity.providerLogoutURL(request.Context())
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	revoked, err := h.identity.store.RevokeSession(request.Context(), session.ID, h.identity.mutationContext(request, "SESSION_LOGGED_OUT"))
	if err != nil {
		h.identity.writeSessionMutationError(writer, request, err)
		return
	}
	writer.Header().Set("X-Audit-Message-ID", revoked.LastAuditMessageID)
	writer.Header().Set("Location", providerLogoutURL)
	http.SetCookie(writer, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	writer.WriteHeader(http.StatusNoContent)
}

func (h *handler) RevokeSession(writer http.ResponseWriter, request *http.Request, params platformapi.RevokeSessionParams) {
	adminSession, failure := h.identitySession(request)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if failure := h.identity.validateStateChange(request, adminSession, params.CSRFToken); failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	principal, failure := h.identity.fetchPrincipal(request.Context(), adminSession)
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	if !containsRole(principal.Principal.Roles, "platform-admin") {
		writeIdentityFailure(writer, request, identityFailure{403, "SESSION_REVOCATION_FORBIDDEN", "Session revocation forbidden", "The authenticated principal is not allowed to revoke sessions.", false})
		return
	}
	targetSession, err := h.identity.store.GetSession(request.Context(), params.SessionID)
	if errors.Is(err, sessionstore.ErrSessionNotFound) || (err == nil && targetSession.TenantID != adminSession.TenantID) {
		writeIdentityFailure(writer, request, identityFailure{404, "SESSION_NOT_FOUND", "Session not found", "The requested session does not exist.", false})
		return
	}
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "SESSION_STORE_UNAVAILABLE", "Session unavailable", "The durable Session store could not be read.", true})
		return
	}
	target, err := h.identity.store.RevokeSession(request.Context(), params.SessionID, h.identity.mutationContext(request, "SESSION_REVOKED"))
	if errors.Is(err, sessionstore.ErrSessionNotFound) {
		writeIdentityFailure(writer, request, identityFailure{404, "SESSION_NOT_FOUND", "Session not found", "The requested session does not exist.", false})
		return
	}
	if err != nil {
		h.identity.writeSessionMutationError(writer, request, err)
		return
	}
	writer.Header().Set("X-Audit-Message-ID", target.LastAuditMessageID)
	writeJSON(writer, http.StatusOK, platformapi.SessionRevocationResponse{SessionID: target.ID, RevokedAt: target.RevokedAt.UTC().Format(time.RFC3339), ObjectiveMS: int(h.identity.config.RevocationObjective.Milliseconds()), AuditMessageID: target.LastAuditMessageID})
}

func (h *handler) identitySession(request *http.Request) (bffSession, *identityFailure) {
	if h.identity == nil {
		failure := identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true}
		return bffSession{}, &failure
	}
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		failure := identityFailure{401, "AUTHENTICATION_REQUIRED", "Authentication required", "A valid BFF Session is required.", false}
		return bffSession{}, &failure
	}
	stored, err := h.identity.store.GetSession(request.Context(), cookie.Value)
	if errors.Is(err, sessionstore.ErrSessionNotFound) || errors.Is(err, sessionstore.ErrSessionRevoked) {
		failure := identityFailure{401, "SESSION_INVALID", "Session invalid", "The BFF Session is expired, revoked, or unknown.", false}
		return bffSession{}, &failure
	}
	if err != nil {
		failure := identityFailure{503, "SESSION_STORE_UNAVAILABLE", "Session unavailable", "The durable Session store could not be read.", true}
		return bffSession{}, &failure
	}
	now := h.identity.now()
	if stored.RevokedAt != nil || !now.Before(stored.ExpiresAt) || !now.Before(stored.LastActivityAt.Add(h.identity.config.IdleTTL)) {
		failure := identityFailure{401, "SESSION_INVALID", "Session invalid", "The BFF Session is expired, revoked, idle, or unknown.", false}
		return bffSession{}, &failure
	}
	if request.Header.Get(userActivityHeaderName) == "1" {
		touched, err := h.identity.store.TouchSession(request.Context(), stored.ID, now)
		if errors.Is(err, sessionstore.ErrSessionNotFound) || errors.Is(err, sessionstore.ErrSessionRevoked) {
			failure := identityFailure{401, "SESSION_INVALID", "Session invalid", "The BFF Session is expired, revoked, idle, or unknown.", false}
			return bffSession{}, &failure
		}
		if err != nil {
			failure := identityFailure{503, "SESSION_STORE_UNAVAILABLE", "Session unavailable", "The durable Session store could not record user activity.", true}
			return bffSession{}, &failure
		}
		stored = touched
	}
	csrfToken, err := h.identity.decryptBytes(stored.CSRFTokenCiphertext)
	if err != nil {
		failure := identityFailure{503, "SESSION_SECRET_INVALID", "Session unavailable", "The durable Session secret could not be opened.", true}
		return bffSession{}, &failure
	}
	return bffSession{Session: stored, CSRFToken: string(csrfToken)}, nil
}

func (controller *identityController) validateStateChange(request *http.Request, session bffSession, csrfToken string) *identityFailure {
	if request.Header.Get("Origin") != controller.config.PublicOrigin {
		failure := identityFailure{403, "ORIGIN_NOT_ALLOWED", "Origin not allowed", "The request Origin is not allowed for this session.", false}
		return &failure
	}
	if subtle.ConstantTimeCompare([]byte(csrfToken), []byte(session.CSRFToken)) != 1 {
		failure := identityFailure{403, "CSRF_TOKEN_INVALID", "CSRF token invalid", "The CSRF token is missing or invalid.", false}
		return &failure
	}
	return nil
}

func (controller *identityController) discover(ctx context.Context) (oidcDiscovery, *identityFailure) {
	discoveryBaseURL := controller.config.OIDCIssuer
	if strings.TrimSpace(controller.config.OIDCBackchannelBaseURL) != "" {
		discoveryBaseURL = controller.config.OIDCBackchannelBaseURL
	}
	discovery, err := controller.protocol.Discover(ctx, controller.config.OIDCHTTPClient, discoveryBaseURL)
	if err != nil {
		status, _ := oidcProtocolErrorDetails(err)
		if status == 0 || status != http.StatusOK {
			failure := identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "OIDC provider unavailable", "The identity provider discovery document was unavailable.", true}
			return oidcDiscovery{}, &failure
		}
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC discovery invalid", "The identity provider discovery document is invalid.", true}
		return oidcDiscovery{}, &failure
	}
	if discovery.Issuer != strings.TrimRight(controller.config.OIDCIssuer, "/") || discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" || discovery.JWKSURI == "" {
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC discovery invalid", "The identity provider discovery document is invalid.", true}
		return oidcDiscovery{}, &failure
	}
	return discovery, nil
}

func (controller *identityController) backchannelDiscovery(discovery oidcDiscovery) (oidcDiscovery, error) {
	base := strings.TrimRight(strings.TrimSpace(controller.config.OIDCBackchannelBaseURL), "/")
	if base == "" {
		return discovery, nil
	}
	issuer := strings.TrimRight(controller.config.OIDCIssuer, "/")
	rewrite := func(endpoint string) (string, error) {
		if endpoint == "" {
			return "", nil
		}
		if endpoint == issuer {
			return base, nil
		}
		prefix := issuer + "/"
		if !strings.HasPrefix(endpoint, prefix) {
			return "", errors.New("OIDC endpoint is outside the configured issuer")
		}
		return base + "/" + strings.TrimPrefix(endpoint, prefix), nil
	}
	var err error
	discovery.TokenEndpoint, err = rewrite(discovery.TokenEndpoint)
	if err != nil {
		return oidcDiscovery{}, err
	}
	discovery.JWKSURI, err = rewrite(discovery.JWKSURI)
	if err != nil {
		return oidcDiscovery{}, err
	}
	return discovery, nil
}

func (controller *identityController) providerLogoutURL(ctx context.Context) (string, *identityFailure) {
	discovery, failure := controller.discover(ctx)
	if failure != nil {
		return "", failure
	}
	if discovery.EndSessionEndpoint == "" {
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC logout unavailable", "The identity provider discovery document does not publish an end-session endpoint.", true}
		return "", &failure
	}
	postLogoutRedirectURI := strings.TrimRight(controller.config.PublicOrigin, "/") + "/?logged_out=1"
	logoutURL, err := controller.protocol.SignOutURL(discovery, oidcSignOutRequest{
		ClientID:              controller.config.OIDCClientID,
		PostLogoutRedirectURI: postLogoutRedirectURI,
	})
	if err != nil {
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC logout unavailable", "The Gateway could not construct the identity provider logout request.", true}
		return "", &failure
	}
	return logoutURL, nil
}

func (controller *identityController) exchangeCode(ctx context.Context, code, verifier string) (oidcTokenResponse, *identityFailure) {
	discovery, failure := controller.discover(ctx)
	if failure != nil {
		return oidcTokenResponse{}, failure
	}
	discovery, err := controller.backchannelDiscovery(discovery)
	if err != nil {
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC discovery invalid", "The identity provider backchannel endpoints are invalid.", true}
		return oidcTokenResponse{}, &failure
	}
	tokens, err := controller.protocol.ExchangeCode(ctx, controller.config.OIDCHTTPClient, discovery, oidcCodeExchangeRequest{
		ClientID:     controller.config.OIDCClientID,
		RedirectURI:  controller.config.OIDCRedirectURI,
		Code:         code,
		CodeVerifier: verifier,
	})
	if err != nil {
		status, description := oidcProtocolErrorDetails(err)
		if status == 0 {
			failure := identityFailure{503, "OIDC_CODE_EXCHANGE_UNAVAILABLE", "OIDC exchange unavailable", "The authorization code could not be exchanged.", true}
			return oidcTokenResponse{}, &failure
		}
		if status == http.StatusOK {
			failure := identityFailure{503, "OIDC_CODE_EXCHANGE_INVALID", "OIDC exchange invalid", "The identity provider returned an invalid token response.", true}
			return oidcTokenResponse{}, &failure
		}
		codeValue := "OIDC_CODE_EXCHANGE_FAILED"
		if strings.Contains(strings.ToLower(description), "pkce") {
			codeValue = "OIDC_PKCE_VALIDATION_FAILED"
		}
		failure := identityFailure{401, codeValue, "OIDC login rejected", "The identity provider rejected the authorization code exchange.", false}
		return oidcTokenResponse{}, &failure
	}
	if tokens.IDToken == "" || tokens.AccessToken == "" {
		failure := identityFailure{401, "OIDC_TOKEN_RESPONSE_INCOMPLETE", "OIDC token response invalid", "The identity provider token response is incomplete.", false}
		return oidcTokenResponse{}, &failure
	}
	return tokens, nil
}

func (controller *identityController) validateIDToken(ctx context.Context, token, expectedNonce string) (oidcClaims, *identityFailure) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		failure := identityFailure{401, "OIDC_TOKEN_FORMAT_INVALID", "OIDC token invalid", "The ID token format is invalid.", false}
		return oidcClaims{}, &failure
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		failure := identityFailure{401, "OIDC_TOKEN_FORMAT_INVALID", "OIDC token invalid", "The ID token header is invalid.", false}
		return oidcClaims{}, &failure
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil ||
		(header.Algorithm != "RS256" && header.Algorithm != "ES384") ||
		(header.Type != "" && header.Type != "JWT") || header.KeyID == "" {
		failure := identityFailure{401, "OIDC_TOKEN_TYPE_INVALID", "OIDC token type invalid", "The ID token header is unsupported.", false}
		return oidcClaims{}, &failure
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		failure := identityFailure{401, "OIDC_TOKEN_FORMAT_INVALID", "OIDC token invalid", "The ID token payload is invalid.", false}
		return oidcClaims{}, &failure
	}
	var claims oidcClaims
	if json.Unmarshal(payload, &claims) != nil {
		failure := identityFailure{401, "OIDC_TOKEN_FORMAT_INVALID", "OIDC token invalid", "The ID token claims are invalid.", false}
		return oidcClaims{}, &failure
	}
	discovery, failure := controller.discover(ctx)
	if failure != nil {
		return oidcClaims{}, failure
	}
	discovery, err = controller.backchannelDiscovery(discovery)
	if err != nil {
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC discovery invalid", "The identity provider backchannel endpoints are invalid.", true}
		return oidcClaims{}, &failure
	}
	if err := controller.protocol.VerifyIDToken(ctx, controller.config.OIDCHTTPClient, discovery, controller.config.OIDCClientID, header.KeyID, token); err != nil {
		switch {
		case errors.Is(err, errOIDCTokenIssuerInvalid):
			failure := identityFailure{401, "OIDC_ISSUER_INVALID", "OIDC issuer invalid", "The ID token issuer is not trusted.", false}
			return oidcClaims{}, &failure
		case errors.Is(err, errOIDCTokenAudienceInvalid):
			failure := identityFailure{401, "OIDC_AUDIENCE_INVALID", "OIDC audience invalid", "The ID token audience is invalid.", false}
			return oidcClaims{}, &failure
		case errors.Is(err, errOIDCTokenExpired), errors.Is(err, errOIDCTokenIssuedPast):
			failure := identityFailure{401, "OIDC_TOKEN_EXPIRED", "OIDC token expired", "The ID token has expired or is no longer fresh.", false}
			return oidcClaims{}, &failure
		case errors.Is(err, errOIDCTokenIssuedFuture):
			failure := identityFailure{401, "OIDC_TOKEN_NOT_ACTIVE", "OIDC token not active", "The ID token is not active yet.", false}
			return oidcClaims{}, &failure
		case errors.Is(err, errOIDCSignatureKeyUnknown):
			failure := identityFailure{401, "OIDC_SIGNATURE_KEY_UNKNOWN", "OIDC signing key unknown", "The ID token signing key is not trusted.", false}
			return oidcClaims{}, &failure
		case errors.Is(err, errOIDCJWKSInvalid):
			failure := identityFailure{503, "OIDC_JWKS_INVALID", "OIDC keys invalid", "The identity provider signing keys are invalid.", true}
			return oidcClaims{}, &failure
		}
		var protocolFailure *oidcProtocolError
		if errors.As(err, &protocolFailure) {
			if protocolFailure.StatusCode == http.StatusOK {
				failure := identityFailure{503, "OIDC_JWKS_INVALID", "OIDC keys invalid", "The identity provider signing keys are invalid.", true}
				return oidcClaims{}, &failure
			}
			failure := identityFailure{503, "OIDC_JWKS_UNAVAILABLE", "OIDC keys unavailable", "The identity provider signing keys could not be reached.", true}
			return oidcClaims{}, &failure
		}
		failure := identityFailure{401, "OIDC_SIGNATURE_INVALID", "OIDC signature invalid", "The ID token signature could not be verified.", false}
		return oidcClaims{}, &failure
	}
	now := controller.now().Unix()
	expectedIssuer := strings.TrimRight(controller.config.OIDCIssuer, "/")
	if claims.Issuer != expectedIssuer {
		failure := identityFailure{401, "OIDC_ISSUER_INVALID", "OIDC issuer invalid", "The ID token issuer is not trusted.", false}
		return oidcClaims{}, &failure
	}
	if claims.Audience != controller.config.OIDCClientID {
		failure := identityFailure{401, "OIDC_AUDIENCE_INVALID", "OIDC audience invalid", "The ID token audience is invalid.", false}
		return oidcClaims{}, &failure
	}
	if claims.ExpiresAt <= now {
		failure := identityFailure{401, "OIDC_TOKEN_EXPIRED", "OIDC token expired", "The ID token has expired.", false}
		return oidcClaims{}, &failure
	}
	if claims.NotBefore > now+5 {
		failure := identityFailure{401, "OIDC_TOKEN_NOT_ACTIVE", "OIDC token not active", "The ID token is not active yet.", false}
		return oidcClaims{}, &failure
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
		failure := identityFailure{401, "OIDC_NONCE_INVALID", "OIDC nonce invalid", "The ID token nonce does not match the login request.", false}
		return oidcClaims{}, &failure
	}
	if claims.TenantID == "" {
		claims.TenantID = strings.TrimSpace(controller.config.DefaultTenantID)
	}
	if claims.Subject == "" || claims.TenantID == "" {
		failure := identityFailure{401, "OIDC_CLAIMS_INCOMPLETE", "OIDC claims incomplete", "The ID token identity claims are incomplete.", false}
		return oidcClaims{}, &failure
	}
	return claims, nil
}

func (controller *identityController) encryptTokens(tokens oidcTokenResponse) ([]byte, error) {
	plaintext, err := json.Marshal(tokens)
	if err != nil {
		return nil, err
	}
	return controller.encryptBytes(plaintext)
}

func (controller *identityController) encryptBytes(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, controller.vault.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return controller.vault.Seal(nonce, nonce, plaintext, nil), nil
}

func (controller *identityController) decryptBytes(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < controller.vault.NonceSize() {
		return nil, errors.New("ciphertext is too short")
	}
	nonce := ciphertext[:controller.vault.NonceSize()]
	return controller.vault.Open(nil, nonce, ciphertext[controller.vault.NonceSize():], nil)
}

func (controller *identityController) fetchPrincipal(ctx context.Context, session bffSession) (identitycontext.InternalPrincipalResponse, *identityFailure) {
	ctx, span := observability.Start(ctx, "http.iam.current_principal", observability.SpanKindClient, map[string]any{
		"http.request.method": http.MethodPost, "server.service": "iam-service", "rpc.operation": "principal.current",
	})
	defer span.End()
	now := controller.now()
	expiry := now.Add(controller.config.DelegationTTL)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{Issuer: controller.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: controller.config.ExecutingWorkloadSPIFFE, Audience: controller.config.IAMAudience, TenantID: session.TenantID, Actions: []string{"principal:read"}, Scopes: []string{"session:" + session.ID}, PolicyRevision: controller.config.PolicyRevision, SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiry.Unix(), TokenID: randomURLToken(16)}
	grant, err := identitycontext.SignDelegation(controller.config.DelegationSigner, claims)
	if err != nil {
		failure := identityFailure{503, "DELEGATION_SIGNING_FAILED", "Identity delegation unavailable", "The Gateway could not create a delegated identity context.", true}
		return identitycontext.InternalPrincipalResponse{}, &failure
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controller.config.IAMURL, "/")+"/internal/v1/principal/current", nil)
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(ctx, request.Header)
	response, err := controller.config.IAMHTTPClient.Do(request)
	if err != nil {
		failure := identityFailure{503, "IAM_UNAVAILABLE", "IAM unavailable", "The private IAM service could not be reached.", true}
		return identitycontext.InternalPrincipalResponse{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := identityFailure{403, "IAM_IDENTITY_REJECTED", "Identity rejected", "IAM rejected the authenticated identity context.", false}
		return identitycontext.InternalPrincipalResponse{}, &failure
	}
	var principal identitycontext.InternalPrincipalResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&principal) != nil || principal.Validate() != nil {
		failure := identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an invalid principal response.", true}
		return identitycontext.InternalPrincipalResponse{}, &failure
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		failure := identityFailure{503, "IAM_RESPONSE_INVALID", "IAM response invalid", "IAM returned an invalid principal response.", true}
		return identitycontext.InternalPrincipalResponse{}, &failure
	}
	return principal, nil
}

func (controller *identityController) mutationContext(request *http.Request, action string) sessionstore.MutationContext {
	return sessionstore.MutationContext{
		Action:            action,
		Result:            "SUCCEEDED",
		PolicyRevision:    controller.config.PolicyRevision,
		CorrelationID:     requestIDFromContext(request.Context()),
		TraceID:           traceIDFromContext(request.Context()),
		Traceparent:       observability.Traceparent(request.Context()),
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: controller.config.ExecutingWorkloadSPIFFE,
		OccurredAt:        controller.now().UTC(),
	}
}

func (controller *identityController) writeSessionMutationError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, sessionstore.ErrSessionNotFound) || errors.Is(err, sessionstore.ErrSessionRevoked) {
		writeIdentityFailure(writer, request, identityFailure{401, "SESSION_INVALID", "Session invalid", "The durable BFF Session is revoked or unknown.", false})
		return
	}
	writeIdentityFailure(writer, request, identityFailure{503, "SESSION_PERSISTENCE_FAILED", "Session unavailable", "The Session state and audit intent could not be committed atomically.", true})
}

func writeIdentityFailure(writer http.ResponseWriter, request *http.Request, failure identityFailure) {
	writeProblem(writer, request, failure.status, failure.code, failure.title, failure.detail, failure.retryable, nil)
}
func randomURLToken(byteCount int) string {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
func safeReturnTo(value string) bool {
	if value == "" {
		return true
	}
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\")
}
func validAuthenticationAssurance(claims oidcClaims, requiredACR string, now time.Time) bool {
	if requiredACR != authenticationACRBasic && requiredACR != authenticationACRMFA {
		return false
	}
	if claims.AuthTime <= 0 {
		return false
	}
	authenticatedAt := time.Unix(claims.AuthTime, 0).UTC()
	now = now.UTC()
	if authenticatedAt.After(now.Add(time.Minute)) {
		return false
	}
	if claims.ACR == authenticationACRBasic {
		return requiredACR != authenticationACRMFA && containsString(claims.AMR, "pwd")
	}
	if claims.ACR != authenticationACRMFA || !containsString(claims.AMR, "pwd") || !containsString(claims.AMR, "otp") {
		return false
	}
	if requiredACR == authenticationACRMFA && now.Sub(authenticatedAt) > 10*time.Minute {
		return false
	}
	return true
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsRole(roles []string, expected string) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}
func toPublicUser(value identitycontext.UserPrincipal) platformapi.UserPrincipal {
	return platformapi.UserPrincipal{Subject: value.Subject, Issuer: value.Issuer, DisplayName: value.DisplayName, Email: value.Email, Roles: append([]string{}, value.Roles...)}
}
func toPublicContext(value identitycontext.PrincipalContext) platformapi.PrincipalContext {
	return platformapi.PrincipalContext{InitiatingPrincipal: toPublicUser(value.InitiatingPrincipal), ExecutingServicePrincipal: platformapi.ServicePrincipal{Service: value.ExecutingServicePrincipal.Service, SPIFFEID: value.ExecutingServicePrincipal.SPIFFEID}, TenantID: value.TenantID, Audience: value.Audience, PolicyRevision: value.PolicyRevision, DelegationExpiresAt: value.DelegationExpiresAt}
}
func toPublicAuthorization(value identitycontext.EffectiveAuthorization) platformapi.EffectiveAuthorization {
	capabilities := make([]platformapi.Capability, len(value.Capabilities))
	for index, capability := range value.Capabilities {
		capabilities[index] = platformapi.Capability(capability)
	}
	return platformapi.EffectiveAuthorization{CapabilitySetVersion: value.CapabilitySetVersion, PolicyRevision: value.PolicyRevision, Capabilities: capabilities}
}
