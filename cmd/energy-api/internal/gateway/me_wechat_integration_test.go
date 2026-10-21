package gateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// The mini program binds the signed-in person's WeChat (#664, docs/specs/miniprogram-wechat.md):
// the Gateway hands identity-service the account of the Session, never one named in the request.

const meWeChatPath = "/api/v1/me/wechat"

func appRequest(t *testing.T, client *http.Client, method, url, token, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func weChatBound(t *testing.T, response *http.Response) bool {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var binding struct {
		Bound bool `json:"bound"`
	}
	if err := json.NewDecoder(response.Body).Decode(&binding); err != nil {
		t.Fatal(err)
	}
	return binding.Bound
}

func TestMiniProgramBindsTheSignedInPersonsWeChat(t *testing.T) {
	harness := newAuthHarness(t)
	client := &http.Client{Transport: harness.transport}
	token := decodeAppSession(t, postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions", map[string]string{"username": "fixture", "password": "correct-password"}), http.StatusCreated).SessionToken
	url := harness.gatewayURL + meWeChatPath

	if weChatBound(t, appRequest(t, client, http.MethodGet, url, token, "")) {
		t.Fatal("bound before binding")
	}
	// The request cannot name the account: only the code is accepted.
	assertProblemCode(t, appRequest(t, client, http.MethodPost, url, token, `{"code":"wechat-code-openid-duty","userId":"fixture-admin"}`), http.StatusBadRequest, "REQUEST_INVALID")
	if response := appRequest(t, client, http.MethodPost, url, token, `{"code":"wechat-code-openid-duty"}`); response.StatusCode != http.StatusNoContent {
		t.Fatalf("bind status = %d", response.StatusCode)
	}
	if harness.provider.WeChatOf("fixture-user") != "openid-duty" || harness.provider.WeChatOf("fixture-admin") != "" {
		t.Fatal("the WeChat was not bound to the signed-in account")
	}
	if !weChatBound(t, appRequest(t, client, http.MethodGet, url, token, "")) {
		t.Fatal("not bound after binding")
	}

	// identity-service's refusals reach the mini program as their own problem codes.
	assertProblemCode(t, appRequest(t, client, http.MethodPost, url, token, `{"code":"wechat-code-used"}`), http.StatusBadRequest, "WECHAT_CODE_INVALID")
	harness.provider.BindWeChatForTest("fixture-admin", "openid-admin")
	assertProblemCode(t, appRequest(t, client, http.MethodPost, url, token, `{"code":"wechat-code-openid-admin"}`), http.StatusConflict, "WECHAT_ALREADY_BOUND")

	if response := appRequest(t, client, http.MethodDelete, url, token, ""); response.StatusCode != http.StatusNoContent {
		t.Fatalf("remove status = %d", response.StatusCode)
	}
	if weChatBound(t, appRequest(t, client, http.MethodGet, url, token, "")) || harness.provider.WeChatOf("fixture-admin") != "openid-admin" {
		t.Fatal("removing touched the wrong binding")
	}
}

// There is no WeChat in a browser: the Web Session cannot bind or remove one.
func TestWebSessionCannotTouchWeChat(t *testing.T) {
	harness := newAuthHarness(t)
	browser := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, browser, harness.gatewayURL, "")
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		request, err := http.NewRequest(method, harness.gatewayURL+meWeChatPath, bytes.NewReader([]byte(`{"code":"wechat-code-openid-web"}`)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", harness.gatewayURL)
		request.Header.Set("X-CSRF-Token", principal.Session.CSRFToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := browser.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		assertProblemCode(t, response, http.StatusForbidden, "WECHAT_MINI_PROGRAM_ONLY")
		_ = response.Body.Close()
	}
	if harness.provider.WeChatOf("fixture-user") != "" {
		t.Fatal("a Web Session bound a WeChat")
	}
}
