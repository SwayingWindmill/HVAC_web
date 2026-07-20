package gateway

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const sessionCookieName = "__Host-hvac_session"

type IdentityConfig struct {
	OIDCIssuer              string
	OIDCClientID            string
	OIDCRedirectURI         string
	PublicOrigin            string
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
	SessionTTL              time.Duration
	StateTTL                time.Duration
	DelegationTTL           time.Duration
	RevocationObjective     time.Duration
}

type identityController struct {
	config IdentityConfig
	now    func() time.Time
	vault  cipher.AEAD
	mu     sync.RWMutex
	states map[string]loginState
	store  sessionstore.Store
}

type loginState struct {
	Verifier  string
	Nonce     string
	ReturnTo  string
	CreatedAt time.Time
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
	Issuer               string   `json:"iss"`
	Audience             string   `json:"aud"`
	Subject              string   `json:"sub"`
	ExpiresAt            int64    `json:"exp"`
	IssuedAt             int64    `json:"iat"`
	NotBefore            int64    `json:"nbf"`
	Nonce                string   `json:"nonce"`
	Name                 string   `json:"name"`
	Email                string   `json:"email"`
	Roles                []string `json:"roles"`
	ActingOrganizationID string   `json:"actingOrganizationId"`
	TokenUse             string   `json:"token_use"`
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
		resolved.SessionTTL = 30 * time.Minute
	}
	if resolved.StateTTL <= 0 {
		resolved.StateTTL = 2 * time.Minute
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
	return &identityController{config: resolved, now: now, vault: vault, states: map[string]loginState{}, store: resolved.SessionStore}
}

func (h *handler) BeginLogin(writer http.ResponseWriter, request *http.Request, params platformapi.BeginLoginParams) {
	if h.identity == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true})
		return
	}
	if !safeReturnTo(params.ReturnTo) {
		writeIdentityFailure(writer, request, identityFailure{400, "INVALID_RETURN_TO", "Invalid return target", "The returnTo value must be a local absolute path.", false})
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
	challengeDigest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])
	h.identity.mu.Lock()
	h.identity.states[state] = loginState{Verifier: verifier, Nonce: nonce, ReturnTo: params.ReturnTo, CreatedAt: h.identity.now()}
	h.identity.cleanupLocked()
	h.identity.mu.Unlock()
	endpoint, _ := url.Parse(discovery.AuthorizationEndpoint)
	query := endpoint.Query()
	query.Set("client_id", h.identity.config.OIDCClientID)
	query.Set("redirect_uri", h.identity.config.OIDCRedirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid profile email")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	if params.LoginHint != "" {
		query.Set("login_hint", params.LoginHint)
	}
	endpoint.RawQuery = query.Encode()
	http.Redirect(writer, request, endpoint.String(), http.StatusFound)
}

func (h *handler) CompleteLogin(writer http.ResponseWriter, request *http.Request, params platformapi.CompleteLoginParams) {
	if h.identity == nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_NOT_CONFIGURED", "Identity unavailable", "Identity is not configured for this Gateway.", true})
		return
	}
	h.identity.mu.Lock()
	state, exists := h.identity.states[params.State]
	if exists {
		delete(h.identity.states, params.State)
	}
	h.identity.mu.Unlock()
	if !exists || h.identity.now().Sub(state.CreatedAt) > h.identity.config.StateTTL {
		writeIdentityFailure(writer, request, identityFailure{400, "OIDC_STATE_INVALID", "OIDC state invalid", "The login state is missing, expired, or already used.", false})
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
	if tokens.TokenType != "Bearer" || claims.TokenUse != "id" {
		writeIdentityFailure(writer, request, identityFailure{401, "OIDC_TOKEN_TYPE_INVALID", "OIDC token type invalid", "The identity provider returned an unsupported token type.", false})
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
	stored, err := h.identity.store.CreateSession(request.Context(), sessionstore.Session{
		ID:                       randomURLToken(32),
		Principal:                identitycontext.UserPrincipal{Subject: claims.Subject, Issuer: claims.Issuer, DisplayName: claims.Name, Email: claims.Email, Roles: append([]string(nil), claims.Roles...)},
		ActingOrganizationID:     claims.ActingOrganizationID,
		CSRFTokenCiphertext:      encryptedCSRF,
		ProviderTokensCiphertext: encryptedTokens,
		ExpiresAt:                now.Add(h.identity.config.SessionTTL),
	}, h.identity.mutationContext(request, "SESSION_CREATED"))
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
	writeJSON(writer, http.StatusOK, platformapi.CurrentPrincipalResponse{Principal: toPublicUser(principal.Principal), Context: toPublicContext(principal.Context), Session: platformapi.SessionView{ID: session.ID, ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339), CSRFToken: session.CSRFToken, RevocationObjectiveMS: int(h.identity.config.RevocationObjective.Milliseconds()), LastAuditMessageID: session.LastAuditMessageID}})
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
	revoked, err := h.identity.store.RevokeSession(request.Context(), session.ID, h.identity.mutationContext(request, "SESSION_LOGGED_OUT"))
	if err != nil {
		h.identity.writeSessionMutationError(writer, request, err)
		return
	}
	writer.Header().Set("X-Audit-Message-ID", revoked.LastAuditMessageID)
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
	if stored.RevokedAt != nil || !h.identity.now().Before(stored.ExpiresAt) {
		failure := identityFailure{401, "SESSION_INVALID", "Session invalid", "The BFF Session is expired, revoked, or unknown.", false}
		return bffSession{}, &failure
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
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(controller.config.OIDCIssuer, "/")+"/.well-known/openid-configuration", nil)
	response, err := controller.config.OIDCHTTPClient.Do(request)
	if err != nil {
		failure := identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "OIDC provider unavailable", "The identity provider could not be reached.", true}
		return oidcDiscovery{}, &failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := identityFailure{503, "OIDC_PROVIDER_UNAVAILABLE", "OIDC provider unavailable", "The identity provider discovery document was unavailable.", true}
		return oidcDiscovery{}, &failure
	}
	var discovery oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&discovery); err != nil || discovery.Issuer != strings.TrimRight(controller.config.OIDCIssuer, "/") || discovery.AuthorizationEndpoint == "" || discovery.TokenEndpoint == "" || discovery.JWKSURI == "" {
		failure := identityFailure{503, "OIDC_DISCOVERY_INVALID", "OIDC discovery invalid", "The identity provider discovery document is invalid.", true}
		return oidcDiscovery{}, &failure
	}
	return discovery, nil
}

func (controller *identityController) exchangeCode(ctx context.Context, code, verifier string) (oidcTokenResponse, *identityFailure) {
	discovery, failure := controller.discover(ctx)
	if failure != nil {
		return oidcTokenResponse{}, failure
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {controller.config.OIDCClientID}, "redirect_uri": {controller.config.OIDCRedirectURI}, "code": {code}, "code_verifier": {verifier}}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := controller.config.OIDCHTTPClient.Do(request)
	if err != nil {
		failure := identityFailure{503, "OIDC_CODE_EXCHANGE_UNAVAILABLE", "OIDC exchange unavailable", "The authorization code could not be exchanged.", true}
		return oidcTokenResponse{}, &failure
	}
	defer response.Body.Close()
	var tokens oidcTokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens); err != nil {
		failure := identityFailure{503, "OIDC_CODE_EXCHANGE_INVALID", "OIDC exchange invalid", "The identity provider returned an invalid token response.", true}
		return oidcTokenResponse{}, &failure
	}
	if response.StatusCode != http.StatusOK || tokens.Error != "" {
		codeValue := "OIDC_CODE_EXCHANGE_FAILED"
		if strings.Contains(strings.ToLower(tokens.Description), "pkce") {
			codeValue = "OIDC_PKCE_VALIDATION_FAILED"
		}
		failure := identityFailure{401, codeValue, "OIDC login rejected", "The identity provider rejected the authorization code exchange.", false}
		return oidcTokenResponse{}, &failure
	}
	if tokens.IDToken == "" || tokens.AccessToken == "" || tokens.RefreshToken == "" {
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
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" || header.Type != "JWT" || header.KeyID == "" {
		failure := identityFailure{401, "OIDC_TOKEN_TYPE_INVALID", "OIDC token type invalid", "The ID token header is unsupported.", false}
		return oidcClaims{}, &failure
	}
	discovery, failure := controller.discover(ctx)
	if failure != nil {
		return oidcClaims{}, failure
	}
	key, failure := controller.fetchRSAKey(ctx, discovery.JWKSURI, header.KeyID)
	if failure != nil {
		return oidcClaims{}, failure
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		failure := identityFailure{401, "OIDC_SIGNATURE_INVALID", "OIDC signature invalid", "The ID token signature is invalid.", false}
		return oidcClaims{}, &failure
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		failure := identityFailure{401, "OIDC_SIGNATURE_INVALID", "OIDC signature invalid", "The ID token signature could not be verified.", false}
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
	if claims.Subject == "" || claims.ActingOrganizationID == "" {
		failure := identityFailure{401, "OIDC_CLAIMS_INCOMPLETE", "OIDC claims incomplete", "The ID token identity claims are incomplete.", false}
		return oidcClaims{}, &failure
	}
	return claims, nil
}

func (controller *identityController) fetchRSAKey(ctx context.Context, jwksURI, keyID string) (*rsa.PublicKey, *identityFailure) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	response, err := controller.config.OIDCHTTPClient.Do(request)
	if err != nil {
		failure := identityFailure{503, "OIDC_JWKS_UNAVAILABLE", "OIDC keys unavailable", "The identity provider signing keys could not be reached.", true}
		return nil, &failure
	}
	defer response.Body.Close()
	var document struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			KeyType   string `json:"kty"`
			Algorithm string `json:"alg"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document) != nil {
		failure := identityFailure{503, "OIDC_JWKS_INVALID", "OIDC keys invalid", "The identity provider signing keys are invalid.", true}
		return nil, &failure
	}
	for _, item := range document.Keys {
		if item.KeyID == keyID && item.KeyType == "RSA" && item.Algorithm == "RS256" {
			n, errN := base64.RawURLEncoding.DecodeString(item.Modulus)
			e, errE := base64.RawURLEncoding.DecodeString(item.Exponent)
			if errN != nil || errE != nil {
				break
			}
			exponent := new(big.Int).SetBytes(e)
			if !exponent.IsInt64() {
				break
			}
			return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}, nil
		}
	}
	failure := identityFailure{401, "OIDC_SIGNATURE_KEY_UNKNOWN", "OIDC signing key unknown", "The ID token signing key is not trusted.", false}
	return nil, &failure
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
	now := controller.now()
	expiry := now.Add(controller.config.DelegationTTL)
	if expiry.After(session.ExpiresAt) {
		expiry = session.ExpiresAt
	}
	claims := identitycontext.DelegationClaims{Issuer: controller.config.ExecutingWorkloadSPIFFE, Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer, DisplayName: session.Principal.DisplayName, Email: session.Principal.Email, Roles: append([]string(nil), session.Principal.Roles...), ExecutingService: controller.config.ExecutingWorkloadSPIFFE, Audience: controller.config.IAMAudience, ActingOrganizationID: session.ActingOrganizationID, Actions: []string{"principal:read"}, Scopes: []string{"session:" + session.ID}, PolicyRevision: controller.config.PolicyRevision, SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiry.Unix(), TokenID: randomURLToken(16)}
	grant, err := identitycontext.SignDelegation(controller.config.DelegationSigner, claims)
	if err != nil {
		failure := identityFailure{503, "DELEGATION_SIGNING_FAILED", "Identity delegation unavailable", "The Gateway could not create a delegated identity context.", true}
		return identitycontext.InternalPrincipalResponse{}, &failure
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controller.config.IAMURL, "/")+"/internal/v1/principal/current", nil)
	request.Header.Set("X-Delegation-Grant", grant)
	request.Header.Set("Accept", "application/json, application/problem+json")
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
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&principal) != nil {
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

func (controller *identityController) cleanupLocked() {
	now := controller.now()
	for state, value := range controller.states {
		if now.Sub(value.CreatedAt) > controller.config.StateTTL {
			delete(controller.states, state)
		}
	}
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
func containsRole(roles []string, expected string) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}
func toPublicUser(value identitycontext.UserPrincipal) platformapi.UserPrincipal {
	return platformapi.UserPrincipal{Subject: value.Subject, Issuer: value.Issuer, DisplayName: value.DisplayName, Email: value.Email, Roles: append([]string(nil), value.Roles...)}
}
func toPublicContext(value identitycontext.PrincipalContext) platformapi.PrincipalContext {
	return platformapi.PrincipalContext{InitiatingPrincipal: toPublicUser(value.InitiatingPrincipal), ExecutingServicePrincipal: platformapi.ServicePrincipal{Service: value.ExecutingServicePrincipal.Service, SPIFFEID: value.ExecutingServicePrincipal.SPIFFEID}, ActingOrganizationID: value.ActingOrganizationID, Audience: value.Audience, PolicyRevision: value.PolicyRevision, DelegationExpiresAt: value.DelegationExpiresAt}
}
