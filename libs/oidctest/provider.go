package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Issuer                      string
	ClientID                    string
	RedirectURI                 string
	DefaultActingOrganizationID string
	Now                         func() time.Time
}

type Provider struct {
	mu                          sync.Mutex
	issuer                      string
	clientID                    string
	redirectURI                 string
	defaultActingOrganizationID string
	now                         func() time.Time
	activeKey                   *rsa.PrivateKey
	activeKid                   string
	rogueKey                    *rsa.PrivateKey
	codes                       map[string]authorizationCode
}

type authorizationCode struct {
	RedirectURI   string
	Nonce         string
	CodeChallenge string
	LoginHint     string
	CreatedAt     time.Time
}

type tokenClaims struct {
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

func New(config Config) (*Provider, error) {
	if config.ClientID == "" || config.RedirectURI == "" {
		return nil, fmt.Errorf("OIDC fixture client and redirect URI are required")
	}
	active, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	rogue, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	defaultActingOrganizationID := strings.TrimSpace(config.DefaultActingOrganizationID)
	if defaultActingOrganizationID == "" {
		defaultActingOrganizationID = "org-fixture-01"
	}
	return &Provider{
		issuer:                      strings.TrimRight(config.Issuer, "/"),
		clientID:                    config.ClientID,
		redirectURI:                 config.RedirectURI,
		defaultActingOrganizationID: defaultActingOrganizationID,
		now:                         now,
		activeKey:                   active,
		activeKid:                   randomToken(8),
		rogueKey:                    rogue,
		codes:                       map[string]authorizationCode{},
	}, nil
}

func (provider *Provider) SetIssuer(issuer string) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.issuer = strings.TrimRight(issuer, "/")
}

func (provider *Provider) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/.well-known/openid-configuration":
		provider.discovery(writer)
	case "/jwks":
		provider.jwks(writer)
	case "/authorize":
		provider.authorize(writer, request)
	case "/token":
		provider.token(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (provider *Provider) discovery(writer http.ResponseWriter) {
	provider.mu.Lock()
	issuer := provider.issuer
	provider.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/authorize",
		"token_endpoint":                        issuer + "/token",
		"jwks_uri":                              issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (provider *Provider) jwks(writer http.ResponseWriter) {
	provider.mu.Lock()
	key := provider.activeKey.PublicKey
	kid := provider.activeKid
	provider.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}},
	})
}

func (provider *Provider) authorize(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if query.Get("client_id") != provider.clientID || query.Get("redirect_uri") != provider.redirectURI || query.Get("response_type") != "code" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "client, redirect, or response type is invalid")
		return
	}
	if query.Get("state") == "" || query.Get("nonce") == "" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "state, nonce, and PKCE S256 are required")
		return
	}
	loginHint := query.Get("login_hint")
	if loginHint == "rotated" {
		provider.rotateKey()
	}
	challenge := query.Get("code_challenge")
	if loginHint == "pkce-mismatch" {
		challenge = "fixture-intentional-pkce-mismatch"
	}
	code := randomToken(24)
	provider.mu.Lock()
	provider.codes[code] = authorizationCode{
		RedirectURI:   query.Get("redirect_uri"),
		Nonce:         query.Get("nonce"),
		CodeChallenge: challenge,
		LoginHint:     loginHint,
		CreatedAt:     provider.now(),
	}
	provider.mu.Unlock()
	redirect, _ := url.Parse(provider.redirectURI)
	values := redirect.Query()
	values.Set("code", code)
	values.Set("state", query.Get("state"))
	redirect.RawQuery = values.Encode()
	http.Redirect(writer, request, redirect.String(), http.StatusFound)
}

func (provider *Provider) token(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "form is invalid")
		return
	}
	if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("client_id") != provider.clientID {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "grant or client is invalid")
		return
	}
	codeValue := request.Form.Get("code")
	provider.mu.Lock()
	code, exists := provider.codes[codeValue]
	if exists {
		delete(provider.codes, codeValue)
	}
	provider.mu.Unlock()
	if !exists || code.RedirectURI != request.Form.Get("redirect_uri") || provider.now().Sub(code.CreatedAt) > 2*time.Minute {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "authorization code is invalid")
		return
	}
	verifierDigest := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
	challenge := base64.RawURLEncoding.EncodeToString(verifierDigest[:])
	if challenge != code.CodeChallenge {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	if code.LoginHint == "disabled-user" {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant", "user is disabled")
		return
	}

	now := provider.now()
	issuer := provider.issuer
	audience := provider.clientID
	nonce := code.Nonce
	expiresAt := now.Add(5 * time.Minute)
	notBefore := now.Add(-time.Second)
	tokenType := "Bearer"
	tokenUse := "id"
	roles := []string{"operator", "audit-reader"}
	subject := "fixture-user"
	name := "Fixture User"
	email := "fixture.user@example.test"
	organizationID := provider.defaultActingOrganizationID

	switch code.LoginHint {
	case "s2-telemetry":
		organizationID = "018f2e00-1000-7000-8000-000000000003"
	case "admin":
		subject = "fixture-admin"
		name = "Fixture Admin"
		email = "fixture.admin@example.test"
		roles = []string{"platform-admin"}
	case "admin-other-organization":
		subject = "fixture-other-admin"
		name = "Fixture Other Admin"
		email = "fixture.other.admin@example.test"
		roles = []string{"platform-admin"}
		organizationID = "org-fixture-02"
	case "other-organization":
		subject = "fixture-other-user"
		name = "Fixture Other User"
		email = "fixture.other@example.test"
		organizationID = "org-fixture-02"
	case "invalid-issuer":
		issuer = issuer + "/wrong"
	case "invalid-audience":
		audience = "wrong-client"
	case "invalid-token-type":
		tokenType = "DPoP"
		tokenUse = "access"
	case "nonce-mismatch":
		nonce = "fixture-wrong-nonce"
	case "expired":
		expiresAt = now.Add(-time.Minute)
	case "not-before":
		notBefore = now.Add(time.Hour)
	}
	claims := tokenClaims{
		Issuer:               issuer,
		Audience:             audience,
		Subject:              subject,
		ExpiresAt:            expiresAt.Unix(),
		IssuedAt:             now.Unix(),
		NotBefore:            notBefore.Unix(),
		Nonce:                nonce,
		Name:                 name,
		Email:                email,
		Roles:                roles,
		ActingOrganizationID: organizationID,
		TokenUse:             tokenUse,
	}
	idToken, err := provider.signJWT(claims, code.LoginHint == "invalid-signature", code.LoginHint == "unknown-signing-key")
	if err != nil {
		writeOAuthError(writer, http.StatusInternalServerError, "server_error", "fixture signing failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"access_token":  randomToken(32),
		"refresh_token": randomToken(32),
		"id_token":      idToken,
		"token_type":    tokenType,
		"expires_in":    300,
	})
}

func (provider *Provider) signJWT(claims tokenClaims, rogue, unknownKeyID bool) (string, error) {
	provider.mu.Lock()
	key := provider.activeKey
	kid := provider.activeKid
	if rogue {
		key = provider.rogueKey
	}
	if unknownKeyID {
		kid = "fixture-unknown-key"
	}
	provider.mu.Unlock()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (provider *Provider) rotateKey() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}
	provider.mu.Lock()
	provider.activeKey = key
	provider.activeKid = randomToken(8)
	provider.mu.Unlock()
}

func randomToken(byteCount int) string {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().String()))
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeOAuthError(writer http.ResponseWriter, status int, code, description string) {
	writeJSON(writer, status, map[string]string{"error": code, "error_description": description})
}
