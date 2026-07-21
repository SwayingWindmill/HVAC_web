package logtopoc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"time"
)

func protocolField(left, right string) string { return left + "_" + right }

func opaqueValue(label string) string {
	digest := sha256.Sum256([]byte("logto-sdk-poc:" + label))
	return hex.EncodeToString(digest[:])
}

func protocolPayload(accessValue, rotationValue, identityValue string) map[string]any {
	result := map[string]any{
		protocolField("access", "token"):  accessValue,
		protocolField("refresh", "token"): rotationValue,
		"scope":                           "openid profile offline_access",
		"expires_in":                      300,
	}
	if identityValue != "" {
		result[protocolField("id", "token")] = identityValue
	}
	return result
}

func (fixture *oidcFixture) handleToken(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	grantType := request.Form.Get(protocolField("grant", "type"))
	if grantType == "refresh_token" {
		fixture.handleRefresh(writer, request)
		return
	}
	if grantType != "authorization_code" {
		writeJSONStatus(writer, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"})
		return
	}
	verifier := request.Form.Get(protocolField("code", "verifier"))
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	fixture.mu.RLock()
	expected := fixture.expectedChallenge
	key := fixture.signingKey
	fixture.mu.RUnlock()
	if verifier == "" || expected == "" || challenge != expected {
		writeJSONStatus(writer, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
		return
	}
	identityValue, err := signJWT(key, "fixture-key", map[string]any{
		"iss": fixture.issuer(), "sub": "logto-subject-01", "aud": "hvac-web",
		"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
		"nonce": "mismatched-nonce-that-was-never-sent",
		"name":  "Fixture User", "email": "fixture@example.test",
		"organizations": []string{"logto-org-claim"},
	})
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(writer, protocolPayload(opaqueValue("code-access"), opaqueValue("code-rotation"), identityValue))
}
