package identity

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const loginCookieName = "__Host-hvac_idp_login"

type Config struct {
	Issuer                string
	ClientID              string
	RedirectURI           string
	PostLogoutRedirectURI string
	DatabaseURL           string
	SigningKeyFile        string
	MFAEncryptionKeyFile  string
	SchemaVersion         string
	// The mini program's AppID and the file the administrator puts its AppSecret in.
	WeChatAppID      string
	WeChatSecretFile string
	// Which version of the mini program a WeChat message opens: developer, trial or formal.
	WeChatState string
	// The Gateway's delegation certificate and workload: the Gateway signs a grant for the account
	// on every WeChat call.
	GatewayDelegationCertFile string
	GatewaySPIFFE             string
	Logger                    *slog.Logger
	Now                       func() time.Time
}

type Server struct {
	issuer                string
	clientID              string
	redirectURI           string
	postLogoutRedirectURI string
	store                 *Store
	now                   func() time.Time
	signingKey            SigningKey
	mfaProtector          *MFAProtector
	signInURL             string
	// nil until the administrator puts the mini program's AppSecret on the server.
	wechat *WeChat
	// The Gateway's delegation key and workload, for the grants on WeChat calls.
	gatewayKey    crypto.PublicKey
	gatewaySPIFFE string
	logger        *slog.Logger
}

func (server *Server) log() *slog.Logger {
	if server.logger == nil {
		return slog.Default()
	}
	return server.logger
}

func NewServer(ctx context.Context, config Config) (*Server, error) {
	issuer := strings.TrimRight(strings.TrimSpace(config.Issuer), "/")
	clientID := strings.TrimSpace(config.ClientID)
	redirectURI := strings.TrimSpace(config.RedirectURI)
	postLogout := strings.TrimSpace(config.PostLogoutRedirectURI)
	if issuer == "" || clientID == "" || redirectURI == "" || postLogout == "" || strings.TrimSpace(config.DatabaseURL) == "" || strings.TrimSpace(config.SigningKeyFile) == "" || strings.TrimSpace(config.MFAEncryptionKeyFile) == "" {
		return nil, errors.New("identity issuer, client, redirect, logout redirect, database URL, signing key file and MFA encryption key file are required")
	}
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Scheme != "https" || issuerURL.Host == "" {
		return nil, errors.New("identity issuer must be an absolute HTTPS URL")
	}
	callbackURL, err := url.Parse(redirectURI)
	if err != nil || callbackURL.Scheme != "https" || callbackURL.Host == "" {
		return nil, errors.New("identity redirect URI must be absolute HTTPS")
	}
	logoutURL, err := url.Parse(postLogout)
	if err != nil || logoutURL.Scheme != "https" || logoutURL.Host == "" {
		return nil, errors.New("identity post logout redirect URI must be absolute HTTPS")
	}
	store, err := OpenStore(ctx, config.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := store.VerifyProductSchema(ctx, config.SchemaVersion); err != nil {
		store.Close()
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	signingKey, err := LoadSigningKeyFile(config.SigningKeyFile)
	if err != nil {
		store.Close()
		return nil, err
	}
	mfaProtector, err := LoadMFAProtectorFile(config.MFAEncryptionKeyFile)
	if err != nil {
		store.Close()
		return nil, err
	}
	wechat, err := LoadWeChat(config.WeChatAppID, config.WeChatSecretFile, config.WeChatState)
	if err != nil {
		store.Close()
		return nil, err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(config.GatewayDelegationCertFile)
	if err != nil || strings.TrimSpace(config.GatewaySPIFFE) == "" {
		store.Close()
		return nil, errors.New("identity gateway delegation certificate and workload are required")
	}
	// The Web application's sign-in page on the same public origin is the login interface.
	signInURL := issuerURL.Scheme + "://" + issuerURL.Host + "/sign-in"
	return &Server{
		issuer: issuer, clientID: clientID, redirectURI: redirectURI, postLogoutRedirectURI: postLogout,
		store: store, now: now, signingKey: signingKey, mfaProtector: mfaProtector,
		signInURL: signInURL, wechat: wechat, gatewayKey: gatewayKey, gatewaySPIFFE: strings.TrimSpace(config.GatewaySPIFFE), logger: config.Logger,
	}, nil
}

func (server *Server) Close() { server.store.Close() }

func (server *Server) Ping(ctx context.Context) error { return server.store.Ping(ctx) }

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", server.discovery)
	mux.HandleFunc("GET /jwks", server.jwks)
	mux.HandleFunc("GET /authorize", server.authorize)
	mux.HandleFunc("POST /login", server.login)
	mux.HandleFunc("POST /mfa/totp", server.completeTOTP)
	mux.HandleFunc("POST /token", server.token)
	mux.HandleFunc("POST /app/login", server.appLogin)
	mux.HandleFunc("POST /app/mfa/totp", server.appTOTP)
	mux.HandleFunc("POST /app/wechat/login", server.appWeChatLogin)
	mux.HandleFunc("POST /app/wechat/binding", server.bindWeChat)
	mux.HandleFunc("DELETE /app/wechat/binding", server.unbindWeChat)
	mux.HandleFunc("GET /app/wechat/binding", server.readWeChatBinding)
	mux.HandleFunc("POST /app/wechat/messages", server.sendWeChatMessage)
	mux.HandleFunc("GET /session/end", server.endSession)
	return securityHeaders(mux)
}

func (server *Server) discovery(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"issuer":                                server.issuer,
		"authorization_endpoint":                server.issuer + "/authorize",
		"token_endpoint":                        server.issuer + "/token",
		"jwks_uri":                              server.issuer + "/jwks",
		"end_session_endpoint":                  server.issuer + "/session/end",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"acr_values_supported":                  []string{AssuranceBasic, AssuranceMFA},
		"claims_supported":                      []string{"iss", "aud", "sub", "exp", "iat", "nbf", "nonce", "name", "email", "acr", "amr", "auth_time"},
	})
}

func (server *Server) jwks(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(writer, http.StatusOK, map[string]any{"keys": []any{server.signingKey.PublicJWK}})
}

func (server *Server) authorize(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if query.Get("client_id") != server.clientID || query.Get("redirect_uri") != server.redirectURI || query.Get("response_type") != "code" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "client, redirect, or response type is invalid")
		return
	}
	if query.Get("state") == "" || query.Get("nonce") == "" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "state, nonce, and PKCE S256 are required")
		return
	}
	if !scopeContains(query.Get("scope"), "openid") {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_scope", "openid scope is required")
		return
	}
	requiredACR := query.Get("acr_values")
	if requiredACR == "" {
		requiredACR = AssuranceBasic
	}
	if requiredACR != AssuranceBasic && requiredACR != AssuranceMFA {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "requested authentication assurance is unsupported")
		return
	}
	challenge := randomToken(32)
	if err := server.store.CreateAuthorizationRequest(request.Context(), AuthorizationRequest{
		ChallengeHash: hashOpaque(challenge), ClientID: server.clientID, RedirectURI: server.redirectURI,
		State: query.Get("state"), Nonce: query.Get("nonce"), CodeChallenge: query.Get("code_challenge"),
		Scope: query.Get("scope"), RequiredACR: requiredACR, ExpiresAt: server.now().UTC().Add(10 * time.Minute),
	}); err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: loginCookieName, Value: challenge, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	server.redirectToSignIn(writer, request, challenge, "", "")
}

func (server *Server) login(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	if err := request.ParseForm(); err != nil {
		server.redirectToSignIn(writer, request, "", "", "expired")
		return
	}
	challenge := request.Form.Get("challenge")
	cookie, err := request.Cookie(loginCookieName)
	if err != nil || challenge == "" || !subtleStringCompare(challenge, cookie.Value) {
		server.redirectToSignIn(writer, request, "", "", "expired")
		return
	}
	result, err := server.store.CompleteLogin(request.Context(), hashOpaque(challenge), request.Form.Get("username"), request.Form.Get("password"), server.now().UTC())
	if errors.Is(err, ErrInvalidCredentials) {
		server.redirectToSignIn(writer, request, challenge, "", "invalid_credentials")
		return
	}
	if errors.Is(err, ErrMFAEnrollmentRequired) {
		server.redirectToSignIn(writer, request, challenge, "", "mfa_enrollment_required")
		return
	}
	if errors.Is(err, ErrMFARequired) && result.MFARequired {
		server.redirectToSignIn(writer, request, challenge, "totp", "")
		return
	}
	if errors.Is(err, ErrAuthorizationRequestExpired) {
		http.SetCookie(writer, &http.Cookie{Name: loginCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, MaxAge: -1})
		server.redirectToSignIn(writer, request, "", "", "expired")
		return
	}
	if err != nil {
		server.redirectToSignIn(writer, request, challenge, "", "unavailable")
		return
	}
	server.redirectAuthorizationGrant(writer, request, result.Grant)
}

func (server *Server) completeTOTP(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 4<<10)
	if err := request.ParseForm(); err != nil {
		server.redirectToSignIn(writer, request, "", "", "expired")
		return
	}
	challenge := request.Form.Get("challenge")
	cookie, err := request.Cookie(loginCookieName)
	if err != nil || challenge == "" || !subtleStringCompare(challenge, cookie.Value) {
		server.redirectToSignIn(writer, request, "", "", "expired")
		return
	}
	grant, err := server.store.CompleteTOTP(request.Context(), hashOpaque(challenge), request.Form.Get("code"), server.mfaProtector, server.now().UTC())
	if errors.Is(err, ErrMFAInvalid) {
		server.redirectToSignIn(writer, request, challenge, "totp", "totp_invalid")
		return
	}
	if errors.Is(err, ErrAuthorizationRequestExpired) {
		http.SetCookie(writer, &http.Cookie{Name: loginCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, MaxAge: -1})
		server.redirectToSignIn(writer, request, "", "", "expired")
		return
	}
	if err != nil {
		server.redirectToSignIn(writer, request, challenge, "totp", "unavailable")
		return
	}
	server.redirectAuthorizationGrant(writer, request, grant)
}

func (server *Server) redirectAuthorizationGrant(writer http.ResponseWriter, request *http.Request, grant LoginGrant) {
	http.SetCookie(writer, &http.Cookie{Name: loginCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, MaxAge: -1})
	callback, _ := url.Parse(grant.RedirectURI)
	values := callback.Query()
	values.Set("code", grant.Code)
	values.Set("state", grant.State)
	values.Set("iss", server.issuer)
	callback.RawQuery = values.Encode()
	http.Redirect(writer, request, callback.String(), http.StatusFound)
}

func (server *Server) token(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	if err := request.ParseForm(); err != nil {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "token form is invalid")
		return
	}
	if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("client_id") != server.clientID || request.Form.Get("redirect_uri") != server.redirectURI {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "grant, client, or redirect is invalid")
		return
	}
	verifier := request.Form.Get("code_verifier")
	if len(verifier) < 43 || len(verifier) > 128 {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "PKCE verifier is invalid")
		return
	}
	subject, err := server.store.ExchangeAuthorizationCode(request.Context(), request.Form.Get("code"), verifier, server.clientID, server.redirectURI, server.now().UTC())
	if err != nil {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "authorization code is invalid")
		return
	}
	now := server.now().UTC()
	idToken, err := signIDToken(server.signingKey, idTokenClaims{
		Issuer: server.issuer, Audience: server.clientID, Subject: subject.UserID,
		ExpiresAt: now.Add(5 * time.Minute).Unix(), IssuedAt: now.Unix(), NotBefore: now.Add(-time.Second).Unix(),
		Nonce: subject.Nonce, Name: subject.DisplayName, Email: subject.Email,
		ACR: subject.ACR, AMR: subject.AMR, AuthTime: subject.AuthTime.Unix(),
	})
	if err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "token signing failed")
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writeJSON(writer, http.StatusOK, map[string]any{
		"access_token": randomToken(32),
		"id_token":     idToken,
		"token_type":   "Bearer",
		"expires_in":   300,
	})
}

func (server *Server) endSession(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if query.Get("client_id") != server.clientID || query.Get("post_logout_redirect_uri") != server.postLogoutRedirectURI {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "logout client or redirect is invalid")
		return
	}
	http.Redirect(writer, request, server.postLogoutRedirectURI, http.StatusFound)
}

// redirectToSignIn sends the browser to the sign-in page with the login request, the step and the outcome.
// The username never goes into the address; the page keeps what was typed itself.
func (server *Server) redirectToSignIn(writer http.ResponseWriter, request *http.Request, challenge, step, code string) {
	http.Redirect(writer, request, server.signInLocation(challenge, step, code), http.StatusSeeOther)
}

func (server *Server) signInLocation(challenge, step, code string) string {
	query := url.Values{}
	if challenge != "" {
		query.Set("login_challenge", challenge)
	}
	if step != "" {
		query.Set("step", step)
	}
	if code != "" {
		query.Set("error", code)
	}
	return server.signInURL + "?" + query.Encode()
}

func scopeContains(scope, expected string) bool {
	for _, item := range strings.Fields(scope) {
		if item == expected {
			return true
		}
	}
	return false
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeOAuthError(writer http.ResponseWriter, status int, code, description string) {
	writeJSON(writer, status, map[string]string{"error": code, "error_description": description})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}
