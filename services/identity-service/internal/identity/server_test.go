package identity

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{name: "login without the login cookie", path: "/login", body: "challenge=abc&username=a&password=b", wantURL: "https://platform.example.test/sign-in?error=expired"},
		{name: "login whose challenge does not match the cookie", path: "/login", body: "challenge=abc&username=a&password=b", cookie: "other", wantURL: "https://platform.example.test/sign-in?error=expired"},
		{name: "one-time code without the login cookie", path: "/mfa/totp", body: "challenge=abc&code=123456", wantURL: "https://platform.example.test/sign-in?error=expired"},
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

func TestSignInLocationCarriesOnlyTheChallengeStepAndCode(t *testing.T) {
	server := &Server{signInURL: "https://platform.example.test/sign-in"}
	location, err := url.Parse(server.signInLocation("chal", "totp", "totp_invalid"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if location.Path != "/sign-in" || query.Get("login_challenge") != "chal" || query.Get("step") != "totp" || query.Get("error") != "totp_invalid" || len(query) != 3 {
		t.Fatalf("sign-in location = %s", location)
	}
}
