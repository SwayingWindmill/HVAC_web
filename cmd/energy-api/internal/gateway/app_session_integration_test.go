package gateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
)

// The WeChat mini program signs in with a username and password and carries the session it gets
// back in the Authorization header (#664): no browser, no cookie, no Origin.

type appSession struct {
	SessionToken string `json:"sessionToken"`
	ExpiresAt    string `json:"expiresAt"`
	Step         string `json:"step"`
	PendingLogin string `json:"pendingLogin"`
}

func postAppJSON(t *testing.T, client *http.Client, url string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func decodeAppSession(t *testing.T, response *http.Response, status int) appSession {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d", response.StatusCode, status)
	}
	var session appSession
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	return session
}

func bearer(t *testing.T, client *http.Client, method, url, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestMiniProgramSignsInWithPasswordAndUsesTheSessionAsBearer(t *testing.T) {
	harness := newAuthHarness(t)
	client := &http.Client{Transport: harness.transport}

	wrong := postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions", map[string]string{"username": "fixture", "password": "wrong-password"})
	assertProblemCode(t, wrong, http.StatusUnauthorized, "INVALID_CREDENTIALS")

	session := decodeAppSession(t, postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions", map[string]string{"username": "fixture", "password": "correct-password"}), http.StatusCreated)
	if session.SessionToken == "" || session.ExpiresAt == "" {
		t.Fatalf("app session is incomplete: %#v", session)
	}

	principalResponse := bearer(t, client, http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, session.SessionToken)
	if principalResponse.StatusCode != http.StatusOK {
		t.Fatalf("principal status = %d", principalResponse.StatusCode)
	}
	var principal platformapi.CurrentPrincipalResponse
	if err := json.NewDecoder(principalResponse.Body).Decode(&principal); err != nil {
		t.Fatal(err)
	}
	if principal.Principal.Subject != "fixture-user" || principal.Context.TenantID != authFixtureTenantID {
		t.Fatalf("bearer session principal = %#v", principal)
	}

	forged := bearer(t, client, http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, "not-a-session")
	assertProblemCode(t, forged, http.StatusUnauthorized, "SESSION_INVALID")

	// A request carrying the session in a header cannot be forged by another site, so it needs
	// neither the page Origin nor the CSRF token.
	logout := bearer(t, client, http.MethodPost, harness.gatewayURL+platformapi.LogoutPath, session.SessionToken)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", logout.StatusCode)
	}
	afterLogout := bearer(t, client, http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, session.SessionToken)
	assertProblemCode(t, afterLogout, http.StatusUnauthorized, "SESSION_INVALID")
}

// A Session is accepted only the way it was issued: the browser's in its cookie, where changes
// still need the page Origin and CSRF token, and the mini program's in the header.
func TestSessionsAreAcceptedOnlyTheWayTheyWereIssued(t *testing.T) {
	harness := newAuthHarness(t)
	browserPrincipal, _ := loginAndReadPrincipal(t, harness.browserClient(t), harness.gatewayURL, "")
	client := &http.Client{Transport: harness.transport}

	browserAsHeader := bearer(t, client, http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, browserPrincipal.Session.ID)
	assertProblemCode(t, browserAsHeader, http.StatusUnauthorized, "SESSION_INVALID")

	session := decodeAppSession(t, postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions", map[string]string{"username": "fixture", "password": "correct-password"}), http.StatusCreated)
	request, err := http.NewRequest(http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "__Host-hvac_session", Value: session.SessionToken})
	headerAsCookie, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer headerAsCookie.Body.Close()
	assertProblemCode(t, headerAsCookie, http.StatusUnauthorized, "SESSION_INVALID")
}

func TestMiniProgramAsksForTheOneTimeCodeWhenTheAccountHasOne(t *testing.T) {
	harness := newAuthHarness(t)
	client := &http.Client{Transport: harness.transport}

	pending := decodeAppSession(t, postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions", map[string]string{"username": "mfa-user", "password": "correct-password"}), http.StatusAccepted)
	if pending.Step != "totp" || pending.PendingLogin == "" || pending.SessionToken != "" {
		t.Fatalf("pending sign-in = %#v", pending)
	}

	wrong := postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions/totp", map[string]string{"pendingLogin": pending.PendingLogin, "code": "000000"})
	assertProblemCode(t, wrong, http.StatusUnauthorized, "TOTP_INVALID")

	session := decodeAppSession(t, postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions/totp", map[string]string{"pendingLogin": pending.PendingLogin, "code": "123456"}), http.StatusCreated)
	principal := bearer(t, client, http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, session.SessionToken)
	if principal.StatusCode != http.StatusOK {
		t.Fatalf("principal status = %d", principal.StatusCode)
	}

	reused := postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions/totp", map[string]string{"pendingLogin": pending.PendingLogin, "code": "123456"})
	assertProblemCode(t, reused, http.StatusBadRequest, "LOGIN_EXPIRED")
}
