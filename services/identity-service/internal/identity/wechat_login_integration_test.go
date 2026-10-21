package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// WeChat one-tap sign-in (#664, docs/specs/miniprogram-wechat.md section 四): the Gateway sends the
// code from wx.login with the same client, redirect, state, nonce and PKCE challenge as a password
// sign-in; identity-service trades the code for the OpenID and signs in the account bound to it.
// The sign-in says it was WeChat, never a password.

const (
	wechatLoginClientID    = "hvac-web"
	wechatLoginRedirectURI = "https://platform.example.test/api/v1/auth/callback"
)

func TestWeChatSignsInTheBoundAccountOnly(t *testing.T) {
	adminURL := os.Getenv("IDENTITY_TEST_ADMIN_DATABASE_URL")
	runtimeURL := os.Getenv("IDENTITY_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("IDENTITY_TEST_ADMIN_DATABASE_URL and IDENTITY_TEST_RUNTIME_DATABASE_URL are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminStore, err := OpenStore(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminStore.Close()
	runtimeStore, err := OpenStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	keyPath := filepath.Join(t.TempDir(), "mfa-encryption.key")
	encodedKey, err := GenerateMFAEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(encodedKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	protector, err := LoadMFAProtectorFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	stamp := strings.ReplaceAll(now.Format("150405.000000000"), ".", "") + randomToken(3)
	names := map[string]string{}
	createUser := func(name string) string {
		username := name + stamp
		user, err := adminStore.CreateUser(ctx, CreateUserInput{Username: username, DisplayName: name, Email: username + "@example.invalid", Password: randomToken(24), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		names[user.ID] = username
		return user.ID
	}
	duty, disabled, locked, withCode := createUser("duty"), createUser("disabled"), createUser("locked"), createUser("withcode")
	codes := map[string]string{"code-stranger": "openid-stranger-" + stamp}
	for _, userID := range []string{duty, disabled, locked, withCode} {
		openID := "openid-" + userID
		codes["code-"+userID] = openID
		if err := runtimeStore.BindWeChat(ctx, userID, wechatTestAppID, openID, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := adminStore.SetStatus(ctx, "test-administrator", disabled, "DISABLED", now); err != nil {
		t.Fatal(err)
	}
	// Five wrong passwords lock the account for 15 minutes.
	if _, err := adminStore.pool.Exec(ctx, `UPDATE identity.users SET locked_until = $2 WHERE id = $1::uuid`, locked, now.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	enrollment, err := adminStore.StartTOTPEnrollment(ctx, names[withCode], "HVAC", protector, now)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := totp.GenerateCode(enrollment.ProvisioningKey, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminStore.ActivateTOTP(ctx, names[withCode], activation, protector, now); err != nil {
		t.Fatal(err)
	}

	// Signing in half a minute later, so the one-time code is a new one.
	signInAt := now.Add(30 * time.Second)
	weChat := fakeWeChat(t, codes)
	server := &Server{
		issuer: wechatTestIssuer, clientID: wechatLoginClientID, redirectURI: wechatLoginRedirectURI,
		store: runtimeStore, now: func() time.Time { return signInAt }, mfaProtector: protector,
		wechat: &WeChat{AppID: wechatTestAppID, Secret: wechatTestSecret, BaseURL: weChat.URL, Client: weChat.Client()},
	}
	verifier := randomToken(48)
	digest := sha256.Sum256([]byte(verifier))
	post := func(path string, body map[string]string) (*httptest.ResponseRecorder, appLoginResult) {
		encoded, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded))))
		var result appLoginResult
		_ = json.Unmarshal(recorder.Body.Bytes(), &result)
		return recorder, result
	}
	signIn := func(code string) (*httptest.ResponseRecorder, appLoginResult) {
		return post("/app/wechat/login", map[string]string{
			"clientId": wechatLoginClientID, "redirectUri": wechatLoginRedirectURI, "state": "state-" + code, "nonce": "nonce-1",
			"codeChallenge": base64.RawURLEncoding.EncodeToString(digest[:]), "code": code,
		})
	}
	redeem := func(code string) TokenSubject {
		t.Helper()
		subject, err := runtimeStore.ExchangeAuthorizationCode(ctx, code, verifier, wechatLoginClientID, wechatLoginRedirectURI, signInAt)
		if err != nil {
			t.Fatal(err)
		}
		return subject
	}
	lastSignIn := func(userID string) (string, string) {
		t.Helper()
		var eventType, reason string
		if err := runtimeStore.pool.QueryRow(ctx, `
SELECT event_type, reason_code FROM identity.security_audit_intents
WHERE user_id = $1::uuid AND event_type LIKE 'LOGIN_%' ORDER BY event_id DESC LIMIT 1`, userID).Scan(&eventType, &reason); err != nil {
			t.Fatal(err)
		}
		return eventType, reason
	}

	// The bound account signs in, and the sign-in says WeChat.
	recorder, result := signIn("code-" + duty)
	if recorder.Code != http.StatusOK || result.Code == "" || result.State != "state-code-"+duty {
		t.Fatalf("bound WeChat: %d %s", recorder.Code, recorder.Body.String())
	}
	if subject := redeem(result.Code); subject.UserID != duty || subject.ACR != AssuranceBasic || !slices.Equal(subject.AMR, []string{"wechat"}) {
		t.Fatalf("signed in as %+v", subject)
	}
	if eventType, reason := lastSignIn(duty); eventType != "LOGIN_SUCCEEDED" || reason != "WECHAT_AUTHENTICATED" {
		t.Fatalf("recorded %s %s", eventType, reason)
	}

	// A WeChat bound to no account signs nobody in; there is no sign-up.
	recorder, _ = signIn("code-stranger")
	expectWeChatAnswer(t, recorder, http.StatusUnauthorized, "wechat_not_bound")

	// Disabled and locked accounts are refused like a password sign-in, and the refusal is recorded.
	for _, userID := range []string{disabled, locked} {
		recorder, _ = signIn("code-" + userID)
		expectWeChatAnswer(t, recorder, http.StatusUnauthorized, "account_unavailable")
		if eventType, _ := lastSignIn(userID); eventType != "LOGIN_FAILED" {
			t.Fatalf("refusal recorded as %s", eventType)
		}
	}

	// An account with one-time codes takes the same second step, and the sign-in says WeChat and the code.
	recorder, result = signIn("code-" + withCode)
	if recorder.Code != http.StatusOK || result.Step != "totp" || result.Challenge == "" {
		t.Fatalf("account with one-time codes: %d %s", recorder.Code, recorder.Body.String())
	}
	passcode, err := totp.GenerateCode(enrollment.ProvisioningKey, signInAt)
	if err != nil {
		t.Fatal(err)
	}
	recorder, second := post("/app/mfa/totp", map[string]string{"challenge": result.Challenge, "code": passcode})
	if recorder.Code != http.StatusOK || second.Code == "" {
		t.Fatalf("one-time code: %d %s", recorder.Code, recorder.Body.String())
	}
	if subject := redeem(second.Code); subject.UserID != withCode || subject.ACR != AssuranceMFA || !slices.Equal(subject.AMR, []string{"wechat", "otp"}) {
		t.Fatalf("signed in as %+v", subject)
	}

	// WeChat refusing this server is WeChat not set up, as for binding.
	recorder, _ = signIn("code-from-unlisted-server")
	expectWeChatAnswer(t, recorder, http.StatusServiceUnavailable, "wechat_not_configured")
}
