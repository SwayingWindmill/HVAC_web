package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Web application's /sign-in page is the login interface; the identity service only answers with redirects.
func TestLoginOutcomesRedirectToTheSignInPage(t *testing.T) {
	server := &Server{issuer: "https://platform.example.test/identity", signInURL: "https://platform.example.test/sign-in"}
	cases := []struct {
		name    string
		path    string
		body    string
		cookie  string
		wantURL string
	}{
		{name: "login without the login cookie", path: "/login", body: "csrf_token=abc&username=a&password=b", wantURL: "https://platform.example.test/sign-in"},
		{name: "login whose csrf token does not match the cookie", path: "/login", body: "csrf_token=abc&username=a&password=b", cookie: "other", wantURL: "https://platform.example.test/sign-in"},
		{name: "one-time code without the login cookie", path: "/mfa/totp", body: "csrf_token=abc&code=123456", wantURL: "https://platform.example.test/sign-in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.cookie != "" {
				request.AddCookie(&http.Cookie{Name: loginCookieName, Value: tc.cookie})
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != tc.wantURL {
				t.Fatalf("got %d %q, want 303 %q", recorder.Code, recorder.Header().Get("Location"), tc.wantURL)
			}
			if strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
				t.Fatal("the identity service rendered an HTML page")
			}
		})
	}
}

// The mini program sign-in (#664) issues a code only for the platform's own client and redirect
// with a PKCE challenge, so only the Gateway can redeem it.
func TestAppLoginRefusesAnotherClientBeforeCheckingThePassword(t *testing.T) {
	server := &Server{issuer: "https://platform.example.test/identity", clientID: "hvac-web", redirectURI: "https://platform.example.test/api/v1/auth/callback"}
	for name, body := range map[string]string{
		"another client":   `{"clientId":"other","redirectUri":"https://platform.example.test/api/v1/auth/callback","state":"s","nonce":"n","codeChallenge":"c","username":"a","password":"b"}`,
		"another redirect": `{"clientId":"hvac-web","redirectUri":"https://evil.example.test/cb","state":"s","nonce":"n","codeChallenge":"c","username":"a","password":"b"}`,
		"no PKCE":          `{"clientId":"hvac-web","redirectUri":"https://platform.example.test/api/v1/auth/callback","state":"s","nonce":"n","username":"a","password":"b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/app/login", strings.NewReader(body)))
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_request") {
				t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLoginContextStatusesAndOriginGuards(t *testing.T) {
	server := &Server{issuer: "https://platform.example.test/identity", signInURL: "https://platform.example.test/sign-in"}

	if deriveLoginCSRF("chal-1") == "" || deriveLoginCSRF("chal-1") == deriveLoginCSRF("chal-2") {
		t.Fatalf("unexpected deriveLoginCSRF output")
	}

	req := httptest.NewRequest(http.MethodGet, "/login-context", nil)
	req.Header.Set("Origin", "https://platform.example.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login-context status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Pragma") != "no-cache" || rec.Header().Get("Vary") != "Cookie" {
		t.Fatalf("unexpected cache headers: %#v", rec.Header())
	}
	var payload loginContextPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "none" {
		t.Fatalf("payload = %+v, want none", payload)
	}

	expiredReq := httptest.NewRequest(http.MethodGet, "/login-context", nil)
	expiredReq.AddCookie(&http.Cookie{Name: loginErrorCookieName, Value: "expired"})
	expiredRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(expiredRec, expiredReq)
	if expiredRec.Code != http.StatusOK {
		t.Fatalf("expired login-context status = %d, want 200", expiredRec.Code)
	}
	payload = loginContextPayload{}
	if err := json.NewDecoder(expiredRec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "expired" {
		t.Fatalf("payload = %+v, want expired", payload)
	}
	clearedErrorCookie := false
	for _, cookie := range expiredRec.Result().Cookies() {
		if cookie.Name == loginErrorCookieName && cookie.MaxAge < 0 {
			clearedErrorCookie = true
		}
	}
	if !clearedErrorCookie {
		t.Fatalf("expected %s to be cleared, got %+v", loginErrorCookieName, expiredRec.Result().Cookies())
	}

	bothExpiredReq := httptest.NewRequest(http.MethodGet, "/login-context", nil)
	bothExpiredReq.AddCookie(&http.Cookie{Name: loginCookieName, Value: "stale-challenge"})
	bothExpiredReq.AddCookie(&http.Cookie{Name: loginErrorCookieName, Value: "expired"})
	bothExpiredRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(bothExpiredRec, bothExpiredReq)
	if bothExpiredRec.Code != http.StatusOK {
		t.Fatalf("both-expired login-context status = %d, want 200", bothExpiredRec.Code)
	}
	payload = loginContextPayload{}
	if err := json.NewDecoder(bothExpiredRec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "expired" {
		t.Fatalf("both-expired payload = %+v, want expired", payload)
	}
	var clearedBothLogin, clearedBothError bool
	for _, cookie := range bothExpiredRec.Result().Cookies() {
		if cookie.Name == loginCookieName && cookie.MaxAge < 0 {
			clearedBothLogin = true
		}
		if cookie.Name == loginErrorCookieName && cookie.MaxAge < 0 {
			clearedBothError = true
		}
	}
	if !clearedBothLogin || !clearedBothError {
		t.Fatalf("expected both login and error cookies cleared, got %+v", bothExpiredRec.Result().Cookies())
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		origin string
		site   string
	}{
		{name: "cross-origin get", method: http.MethodGet, path: "/login-context", origin: "https://evil.example", site: "cross-site"},
		{name: "same-site get rejected", method: http.MethodGet, path: "/login-context", origin: "https://other.platform.example.test", site: "same-site"},
		{name: "cross-origin login post", method: http.MethodPost, path: "/login", origin: "https://evil.example", site: "cross-site"},
		{name: "cross-origin totp post", method: http.MethodPost, path: "/mfa/totp", origin: "https://evil.example", site: "cross-site"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crossReq := httptest.NewRequest(tc.method, tc.path, strings.NewReader("csrf_token=abc"))
			crossReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			crossReq.Header.Set("Origin", tc.origin)
			crossReq.Header.Set("Sec-Fetch-Site", tc.site)
			crossRec := httptest.NewRecorder()
			server.Handler().ServeHTTP(crossRec, crossReq)
			if crossRec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", crossRec.Code)
			}
			if len(crossRec.Result().Cookies()) != 0 {
				t.Fatalf("unexpected cookies on 403: %+v", crossRec.Result().Cookies())
			}
		})
	}
}

func TestLoginSetsExpiredFlashCookieAndBareSignInLocation(t *testing.T) {
	server := &Server{issuer: "https://platform.example.test/identity", signInURL: "https://platform.example.test/sign-in"}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("csrf_token=wrong&username=a&password=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: loginCookieName, Value: "real-challenge"})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "https://platform.example.test/sign-in" {
		t.Fatalf("Location = %q, want bare /sign-in", location)
	}
	foundExpired := false
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == loginErrorCookieName && cookie.Value == "expired" && cookie.HttpOnly && cookie.Secure && cookie.Path == "/" {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("expected %s=expired cookie, got %+v", loginErrorCookieName, rec.Result().Cookies())
	}
}

func TestStorePingUnavailableDoesNotPanic(t *testing.T) {
	var nilStore *Store
	if err := nilStore.Ping(context.Background()); err == nil {
		t.Fatal("expected error for nil store")
	}
	if err := (&Store{}).Ping(context.Background()); err == nil {
		t.Fatal("expected error for store with nil pool")
	}
}
