package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestPostgresLoginContextFlowKeepsSignInURLClean(t *testing.T) {
	adminURL := os.Getenv("IDENTITY_TEST_ADMIN_DATABASE_URL")
	runtimeURL := os.Getenv("IDENTITY_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("IDENTITY_TEST_ADMIN_DATABASE_URL and IDENTITY_TEST_RUNTIME_DATABASE_URL are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	adminStore, err := OpenStore(ctx, adminURL)
	if err != nil {
		t.Fatalf("open identity admin store: %v", err)
	}
	defer adminStore.Close()
	runtimeStore, err := OpenStore(ctx, runtimeURL)
	if err != nil {
		t.Fatalf("open identity runtime store: %v", err)
	}
	defer runtimeStore.Close()

	encodedKey, err := GenerateMFAEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "mfa-encryption.key")
	if err := os.WriteFile(keyPath, []byte(encodedKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	protector, err := LoadMFAProtectorFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	enrollmentTime := now.Add(-60 * time.Second)
	username := "login-context-" + strings.ReplaceAll(now.Format("20060102T150405.000000000"), ".", "")
	password := randomToken(24)
	if _, err := adminStore.CreateUser(ctx, CreateUserInput{
		Username: username, DisplayName: "Login Context User", Email: username + "@example.invalid", Password: password, Now: enrollmentTime,
	}); err != nil {
		t.Fatal(err)
	}
	enrollment, err := adminStore.StartTOTPEnrollment(ctx, username, "HVAC", protector, enrollmentTime)
	if err != nil {
		t.Fatal(err)
	}
	actCode, err := totp.GenerateCode(enrollment.ProvisioningKey, enrollmentTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminStore.ActivateTOTP(ctx, username, actCode, protector, enrollmentTime); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		store:        runtimeStore,
		issuer:       "https://platform.example.test/identity",
		clientID:     "hvac-web",
		redirectURI:  "https://platform.example.test/api/v1/auth/callback",
		signInURL:    "https://platform.example.test/sign-in",
		mfaProtector: protector,
	}
	handler := server.Handler()

	verifier := randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.clientID)
	authParams.Set("redirect_uri", server.redirectURI)
	authParams.Set("scope", "openid profile email")
	authParams.Set("state", "state-1")
	authParams.Set("nonce", "nonce-1")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+authParams.Encode(), nil)
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusFound && authRec.Code != http.StatusSeeOther {
		t.Fatalf("/authorize status = %d, body = %s", authRec.Code, authRec.Body.String())
	}
	if location := authRec.Header().Get("Location"); location != server.signInURL {
		t.Fatalf("/authorize Location = %q, want bare %q", location, server.signInURL)
	}
	var loginCookie *http.Cookie
	for _, cookie := range authRec.Result().Cookies() {
		if cookie.Name == loginCookieName && cookie.Value != "" {
			loginCookie = cookie
		}
	}
	if loginCookie == nil {
		t.Fatalf("missing %s cookie after /authorize", loginCookieName)
	}

	readContext := func(extraCookies ...*http.Cookie) (loginContextPayload, []*http.Cookie) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/login-context", nil)
		req.Header.Set("Origin", "https://platform.example.test")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(loginCookie)
		for _, c := range extraCookies {
			if c != nil {
				req.AddCookie(c)
			}
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/login-context status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var payload loginContextPayload
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload, rec.Result().Cookies()
	}

	firstCtx, _ := readContext()
	if firstCtx.Status != "ready" || firstCtx.Step != "password" || firstCtx.CSRFToken == "" || firstCtx.Error != "" {
		t.Fatalf("initial /login-context = %+v", firstCtx)
	}

	stepFromStore, err := runtimeStore.ReadLoginContext(ctx, hashOpaque(loginCookie.Value), time.Now().UTC())
	if err != nil || stepFromStore != "password" {
		t.Fatalf("ReadLoginContext = (%q, %v), want password", stepFromStore, err)
	}

	badLoginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{
		"csrf_token": {firstCtx.CSRFToken},
		"username":   {username},
		"password":   {"wrong-password"},
	}.Encode()))
	badLoginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badLoginReq.Header.Set("Origin", "https://platform.example.test")
	badLoginReq.Header.Set("Sec-Fetch-Site", "same-origin")
	badLoginReq.AddCookie(loginCookie)
	badLoginRec := httptest.NewRecorder()
	handler.ServeHTTP(badLoginRec, badLoginReq)
	if badLoginRec.Code != http.StatusSeeOther || badLoginRec.Header().Get("Location") != server.signInURL {
		t.Fatalf("failed login response = (%d, %q)", badLoginRec.Code, badLoginRec.Header().Get("Location"))
	}
	var errorCookie *http.Cookie
	for _, c := range badLoginRec.Result().Cookies() {
		if c.Name == loginErrorCookieName {
			errorCookie = c
		}
	}
	if errorCookie == nil || errorCookie.Value != "invalid_credentials" {
		t.Fatalf("expected invalid_credentials cookie, got %+v", badLoginRec.Result().Cookies())
	}

	errCtx, errCtxCookies := readContext(errorCookie)
	if errCtx.Error != "invalid_credentials" || errCtx.Step != "password" {
		t.Fatalf("error /login-context = %+v", errCtx)
	}
	clearedFlash := false
	for _, c := range errCtxCookies {
		if c.Name == loginErrorCookieName && c.MaxAge < 0 {
			clearedFlash = true
		}
	}
	if !clearedFlash {
		t.Fatalf("expected %s to be cleared after consumption", loginErrorCookieName)
	}

	goodLoginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{
		"csrf_token": {errCtx.CSRFToken},
		"username":   {username},
		"password":   {password},
	}.Encode()))
	goodLoginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	goodLoginReq.Header.Set("Origin", "https://platform.example.test")
	goodLoginReq.Header.Set("Sec-Fetch-Site", "same-origin")
	goodLoginReq.AddCookie(loginCookie)
	goodLoginRec := httptest.NewRecorder()
	handler.ServeHTTP(goodLoginRec, goodLoginReq)
	if goodLoginRec.Code != http.StatusSeeOther || goodLoginRec.Header().Get("Location") != server.signInURL {
		t.Fatalf("TOTP-required login response = (%d, %q)", goodLoginRec.Code, goodLoginRec.Header().Get("Location"))
	}

	totpCtx, _ := readContext()
	if totpCtx.Status != "ready" || totpCtx.Step != "totp" || totpCtx.CSRFToken == "" || totpCtx.Error != "" {
		t.Fatalf("totp /login-context = %+v", totpCtx)
	}

	stepAtTOTP, err := runtimeStore.ReadLoginContext(ctx, hashOpaque(loginCookie.Value), time.Now().UTC())
	if err != nil || stepAtTOTP != "totp" {
		t.Fatalf("ReadLoginContext(totp) = (%q, %v), want totp", stepAtTOTP, err)
	}

	totpCode, err := totp.GenerateCode(enrollment.ProvisioningKey, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	totpReq := httptest.NewRequest(http.MethodPost, "/mfa/totp", strings.NewReader(url.Values{
		"csrf_token": {totpCtx.CSRFToken},
		"code":       {totpCode},
	}.Encode()))
	totpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	totpReq.Header.Set("Origin", "https://platform.example.test")
	totpReq.Header.Set("Sec-Fetch-Site", "same-origin")
	totpReq.AddCookie(loginCookie)
	totpRec := httptest.NewRecorder()
	handler.ServeHTTP(totpRec, totpReq)
	if totpRec.Code != http.StatusSeeOther && totpRec.Code != http.StatusFound {
		t.Fatalf("/mfa/totp status = %d", totpRec.Code)
	}
	grantLocation, err := url.Parse(totpRec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(grantLocation.String(), server.redirectURI) || grantLocation.Query().Get("code") == "" || grantLocation.Query().Get("state") != "state-1" {
		t.Fatalf("grant redirect = %q", grantLocation)
	}

	if _, err := runtimeStore.ReadLoginContext(ctx, hashOpaque("missing-challenge"), time.Now().UTC()); !errors.Is(err, ErrAuthorizationRequestExpired) {
		t.Fatalf("ReadLoginContext(missing) = %v, want ErrAuthorizationRequestExpired", err)
	}
}
