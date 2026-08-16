package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const loginCookieName = "__Host-hvac_idp_login"

type Config struct {
	Issuer                string
	ClientID              string
	RedirectURI           string
	PostLogoutRedirectURI string
	DatabaseURL           string
	SigningKeyFile        string
	Now                   func() time.Time
}

type Server struct {
	issuer                string
	clientID              string
	redirectURI           string
	postLogoutRedirectURI string
	store                 *Store
	now                   func() time.Time
	signingKey            SigningKey
	loginTemplate         *template.Template
	loginAction           string
}

func NewServer(ctx context.Context, config Config) (*Server, error) {
	issuer := strings.TrimRight(strings.TrimSpace(config.Issuer), "/")
	clientID := strings.TrimSpace(config.ClientID)
	redirectURI := strings.TrimSpace(config.RedirectURI)
	postLogout := strings.TrimSpace(config.PostLogoutRedirectURI)
	if issuer == "" || clientID == "" || redirectURI == "" || postLogout == "" || strings.TrimSpace(config.DatabaseURL) == "" || strings.TrimSpace(config.SigningKeyFile) == "" {
		return nil, errors.New("identity issuer, client, redirect, logout redirect, database URL and signing key file are required")
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
	now := config.Now
	if now == nil {
		now = time.Now
	}
	signingKey, err := LoadSigningKeyFile(config.SigningKeyFile)
	if err != nil {
		store.Close()
		return nil, err
	}
	page, err := template.New("login").Parse(loginPageTemplate)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("parse identity login page: %w", err)
	}
	loginAction := strings.TrimRight(issuerURL.EscapedPath(), "/") + "/login"
	if loginAction == "" {
		loginAction = "/login"
	}
	return &Server{
		issuer: issuer, clientID: clientID, redirectURI: redirectURI, postLogoutRedirectURI: postLogout,
		store: store, now: now, signingKey: signingKey, loginTemplate: page, loginAction: loginAction,
	}, nil
}

func (server *Server) Close() { server.store.Close() }

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", server.discovery)
	mux.HandleFunc("GET /jwks", server.jwks)
	mux.HandleFunc("GET /authorize", server.authorize)
	mux.HandleFunc("POST /login", server.login)
	mux.HandleFunc("POST /token", server.token)
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
		"claims_supported":                      []string{"iss", "aud", "sub", "exp", "iat", "nbf", "nonce", "name", "email"},
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
	challenge := randomToken(32)
	if err := server.store.CreateAuthorizationRequest(request.Context(), AuthorizationRequest{
		ChallengeHash: hashOpaque(challenge), ClientID: server.clientID, RedirectURI: server.redirectURI,
		State: query.Get("state"), Nonce: query.Get("nonce"), CodeChallenge: query.Get("code_challenge"),
		Scope: query.Get("scope"), ExpiresAt: server.now().UTC().Add(10 * time.Minute),
	}); err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: loginCookieName, Value: challenge, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	server.renderLogin(writer, http.StatusOK, challenge, "", "")
}

func (server *Server) login(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	if err := request.ParseForm(); err != nil {
		server.renderLogin(writer, http.StatusBadRequest, "", "", "登录请求无效，请重新开始登录。")
		return
	}
	challenge := request.Form.Get("challenge")
	cookie, err := request.Cookie(loginCookieName)
	if err != nil || challenge == "" || !subtleStringCompare(challenge, cookie.Value) {
		server.renderLogin(writer, http.StatusBadRequest, "", "", "登录请求已过期，请重新开始登录。")
		return
	}
	username := request.Form.Get("username")
	grant, err := server.store.CompleteLogin(request.Context(), hashOpaque(challenge), username, request.Form.Get("password"), server.now().UTC())
	if errors.Is(err, ErrInvalidCredentials) {
		server.renderLogin(writer, http.StatusUnauthorized, challenge, username, "用户名或密码错误。")
		return
	}
	if errors.Is(err, ErrAuthorizationRequestExpired) {
		http.SetCookie(writer, &http.Cookie{Name: loginCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, MaxAge: -1})
		server.renderLogin(writer, http.StatusBadRequest, "", username, "登录请求已过期，请重新开始登录。")
		return
	}
	if err != nil {
		server.renderLogin(writer, http.StatusServiceUnavailable, challenge, username, "身份服务暂时不可用。")
		return
	}
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

func (server *Server) renderLogin(writer http.ResponseWriter, status int, challenge, username, message string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(status)
	_ = server.loginTemplate.Execute(writer, map[string]string{"Challenge": challenge, "Username": username, "Message": message, "LoginAction": server.loginAction})
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
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

const loginPageTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>智慧能源系统登录</title>
  <style>
    :root { color-scheme: light; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #f3f6fb; color: #14213d; }
    .card { width: min(420px, calc(100vw - 32px)); background: #fff; border: 1px solid #dfe6f1; border-radius: 18px; padding: 32px; box-shadow: 0 18px 50px rgba(20,33,61,.08); }
    .eyebrow { font-size: 12px; letter-spacing: .12em; color: #64748b; text-transform: uppercase; margin-bottom: 10px; }
    h1 { margin: 0 0 8px; font-size: 26px; }
    .subtitle { margin: 0 0 24px; color: #64748b; font-size: 14px; line-height: 1.6; }
    label { display: block; margin: 14px 0 7px; font-size: 13px; font-weight: 600; }
    input { width: 100%; height: 44px; padding: 0 12px; border: 1px solid #cbd5e1; border-radius: 10px; font: inherit; outline: none; }
    input:focus { border-color: #2563eb; box-shadow: 0 0 0 3px rgba(37,99,235,.12); }
    button { width: 100%; height: 44px; margin-top: 22px; border: 0; border-radius: 10px; background: #1d4ed8; color: white; font: inherit; font-weight: 700; cursor: pointer; }
    .error { margin: 0 0 14px; padding: 10px 12px; border-radius: 10px; background: #fff1f2; color: #be123c; font-size: 13px; }
    .expired { text-align: center; color: #64748b; line-height: 1.7; }
  </style>
</head>
<body>
  <main class="card">
    <div class="eyebrow">HVAC Energy Platform</div>
    <h1>登录智慧能源系统</h1>
    <p class="subtitle">使用平台账号完成身份认证。业务 Tenant、Site 与角色权限由平台 IAM 独立管理。</p>
    {{if .Message}}<div class="error">{{.Message}}</div>{{end}}
    {{if .Challenge}}
    <form method="post" action="{{.LoginAction}}" autocomplete="on">
      <input type="hidden" name="challenge" value="{{.Challenge}}" />
      <label for="username">用户名</label>
      <input id="username" name="username" value="{{.Username}}" autocomplete="username" required autofocus />
      <label for="password">密码</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required />
      <button type="submit">登录</button>
    </form>
    {{else}}
    <p class="expired">请返回智慧能源系统重新发起登录。</p>
    {{end}}
  </main>
</body>
</html>`
