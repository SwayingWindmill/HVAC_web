package logtopoc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
)

type oidcFixture struct {
	server             *httptest.Server
	mu                 sync.RWMutex
	signingKey         *rsa.PrivateKey
	attackerKey        *rsa.PrivateKey
	expectedChallenge  string
	discoveryAvailable atomic.Bool
	revokeStatus       atomic.Int64
	refreshRequests    atomic.Int64
	revokeRequests     atomic.Int64
}

func newOIDCFixture() (*oidcFixture, error) {
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	fixture := &oidcFixture{signingKey: signingKey, attackerKey: attackerKey}
	fixture.discoveryAvailable.Store(true)
	fixture.revokeStatus.Store(http.StatusInternalServerError)
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture, nil
}

func (fixture *oidcFixture) Close()           { fixture.server.Close() }
func (fixture *oidcFixture) endpoint() string { return fixture.server.URL }
func (fixture *oidcFixture) issuer() string   { return fixture.server.URL + "/oidc" }

func (fixture *oidcFixture) rotateKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	fixture.mu.Lock()
	fixture.signingKey = key
	fixture.mu.Unlock()
	return nil
}

func (fixture *oidcFixture) setExpectedChallenge(value string) {
	fixture.mu.Lock()
	fixture.expectedChallenge = value
	fixture.mu.Unlock()
}

func (fixture *oidcFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/oidc/.well-known/openid-configuration":
		if !fixture.discoveryAvailable.Load() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(writer, map[string]any{
			"authorization_endpoint": fixture.server.URL + "/oidc/authorize",
			"token_endpoint":         fixture.server.URL + "/oidc/token",
			"userinfo_endpoint":      fixture.server.URL + "/oidc/me",
			"end_session_endpoint":   fixture.server.URL + "/oidc/session/end",
			"revocation_endpoint":    fixture.server.URL + "/oidc/revoke",
			"jwks_uri":               fixture.server.URL + "/oidc/jwks",
			"issuer":                 fixture.issuer(),
		})
	case "/oidc/jwks":
		fixture.mu.RLock()
		publicKey := fixture.signingKey.PublicKey
		fixture.mu.RUnlock()
		writeJSON(writer, map[string]any{"keys": []any{jwk("fixture-key", &publicKey)}})
	case "/oidc/token":
		fixture.handleToken(writer, request)
	case "/oidc/revoke":
		fixture.revokeRequests.Add(1)
		writer.WriteHeader(int(fixture.revokeStatus.Load()))
	case "/oidc/session/end":
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func jwk(keyID string, publicKey *rsa.PublicKey) map[string]any {
	exponent := big.NewInt(int64(publicKey.E)).Bytes()
	return map[string]any{
		"kid": keyID, "kty": "RSA", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func signJWT(key *rsa.PrivateKey, keyID string, claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": keyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
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

func writeJSON(writer http.ResponseWriter, value any) { writeJSONStatus(writer, http.StatusOK, value) }

func writeJSONStatus(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
