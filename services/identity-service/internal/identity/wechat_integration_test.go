package identity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// Binding WeChat in the mini program (#664, docs/specs/miniprogram-wechat.md): identity-service
// trades the one-time code from wx.login for the person's OpenID at WeChat and keeps one WeChat
// per account and one account per WeChat. The session key WeChat sends along is never kept.

const (
	wechatTestAppID      = "wx-test-app"
	wechatTestSecret     = "wechat-test-secret"
	wechatTestSessionKey = "session-key-that-must-not-be-kept"
)

// fakeWeChat answers code2Session the way WeChat does: an OpenID for a known code, error 40164
// when the server is not on the mini program's IP allowlist, error 40163 for a code already used.
func fakeWeChat(t *testing.T, openIDs map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path != "/sns/jscode2session" || query.Get("appid") != wechatTestAppID || query.Get("secret") != wechatTestSecret || query.Get("grant_type") != "authorization_code" {
			t.Errorf("unexpected WeChat request %s", request.URL.Redacted())
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if query.Get("js_code") == "code-from-unlisted-server" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 40164, "errmsg": "invalid ip, not in whitelist"})
			return
		}
		openID, known := openIDs[query.Get("js_code")]
		if !known {
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 40163, "errmsg": "code been used"})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"openid": openID, "session_key": wechatTestSessionKey})
	}))
	t.Cleanup(server.Close)
	return server
}

const (
	wechatTestIssuer  = "https://platform.example.test/identity"
	wechatTestGateway = "spiffe://hvac.local/platform-gateway"
)

// gatewayGrant is what the Gateway signs for the signed-in account on each WeChat call.
func gatewayGrant(t *testing.T, signer crypto.Signer, userID, audience string) string {
	t.Helper()
	now := time.Now().UTC()
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: wechatTestGateway, ExecutingService: wechatTestGateway, Audience: audience,
		Subject: userID, SubjectIssuer: wechatTestIssuer, TenantID: "tenant-1", PolicyRevision: "policy-v1",
		Actions: []string{WeChatAction}, Scopes: []string{"session:session-1"}, SessionID: "session-1",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomToken(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func wechatRequest(t *testing.T, server *Server, method, path, grant, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Delegation-Grant", grant)
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func expectWeChatAnswer(t *testing.T, recorder *httptest.ResponseRecorder, status int, errorCode string) {
	t.Helper()
	if recorder.Code != status || (errorCode != "" && !strings.Contains(recorder.Body.String(), `"error":"`+errorCode+`"`)) {
		t.Fatalf("got %d %s, want %d %s", recorder.Code, recorder.Body.String(), status, errorCode)
	}
}

func TestWeChatBindsOneAccountToOneWeChat(t *testing.T) {
	adminURL := os.Getenv("IDENTITY_TEST_ADMIN_DATABASE_URL")
	runtimeURL := os.Getenv("IDENTITY_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("IDENTITY_TEST_ADMIN_DATABASE_URL and IDENTITY_TEST_RUNTIME_DATABASE_URL are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	stamp := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	createUser := func(name string) string {
		user, err := adminStore.CreateUser(ctx, CreateUserInput{Username: name + stamp, DisplayName: name, Email: name + stamp + "@example.invalid", Password: randomToken(24), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return user.ID
	}
	duty, other := createUser("duty"), createUser("other")
	weChat := fakeWeChat(t, map[string]string{"code-duty": "openid-duty-" + stamp, "code-new": "openid-new-" + stamp})
	gatewayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		issuer: wechatTestIssuer, store: runtimeStore, now: func() time.Time { return time.Now().UTC() }, gatewayKey: gatewayKey.Public(), gatewaySPIFFE: wechatTestGateway,
		wechat: &WeChat{AppID: wechatTestAppID, Secret: wechatTestSecret, BaseURL: weChat.URL, Client: weChat.Client()},
	}
	grant := func(userID string) string { return gatewayGrant(t, gatewayKey, userID, WeChatAudience) }
	bind := func(userID, code string) *httptest.ResponseRecorder {
		return wechatRequest(t, server, http.MethodPost, "/app/wechat/binding", grant(userID), `{"code":"`+code+`"}`)
	}
	binding := func(userID string) map[string]any {
		recorder := wechatRequest(t, server, http.MethodGet, "/app/wechat/binding", grant(userID), "")
		expectWeChatAnswer(t, recorder, http.StatusOK, "")
		var answer map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}

	if answer := binding(duty); answer["bound"] != false {
		t.Fatalf("before binding: %v", answer)
	}
	expectWeChatAnswer(t, bind(duty, "code-duty"), http.StatusNoContent, "")
	if answer := binding(duty); answer["bound"] != true || answer["boundAt"] == nil {
		t.Fatalf("after binding: %v", answer)
	}
	// The session key WeChat sent along is not kept anywhere in the binding.
	var row string
	if err := runtimeStore.pool.QueryRow(ctx, `SELECT row_to_json(b)::text FROM identity.wechat_bindings b WHERE user_id = $1::uuid`, duty).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row, wechatTestSessionKey) {
		t.Fatalf("the binding kept the session key: %s", row)
	}

	// One WeChat, one account: another account cannot take a WeChat that is already bound.
	expectWeChatAnswer(t, bind(other, "code-duty"), http.StatusConflict, "wechat_already_bound")
	// A used or made-up code binds nothing.
	expectWeChatAnswer(t, bind(other, "code-used"), http.StatusBadRequest, "wechat_code_invalid")
	if answer := binding(other); answer["bound"] != false {
		t.Fatalf("a refused binding was kept: %v", answer)
	}
	// One account, one WeChat: binding another WeChat replaces the first, which is then free.
	expectWeChatAnswer(t, bind(duty, "code-new"), http.StatusNoContent, "")
	expectWeChatAnswer(t, bind(other, "code-duty"), http.StatusNoContent, "")

	expectWeChatAnswer(t, wechatRequest(t, server, http.MethodDelete, "/app/wechat/binding", grant(duty), ""), http.StatusNoContent, "")
	if answer := binding(duty); answer["bound"] != false {
		t.Fatalf("after removing: %v", answer)
	}

	// Regression (#664, 2026-10-21): the site server was not on the mini program's IP allowlist and
	// binding said only "try again later". WeChat refusing the server is WeChat not set up.
	expectWeChatAnswer(t, bind(other, "code-from-unlisted-server"), http.StatusServiceUnavailable, "wechat_not_configured")

	// Without the AppSecret on the server, WeChat is plainly not set up.
	unconfigured := &Server{issuer: wechatTestIssuer, store: runtimeStore, now: server.now, gatewayKey: gatewayKey.Public(), gatewaySPIFFE: wechatTestGateway}
	expectWeChatAnswer(t, wechatRequest(t, unconfigured, http.MethodPost, "/app/wechat/binding", grant(duty), `{"code":"code-duty"}`), http.StatusServiceUnavailable, "wechat_not_configured")
}

// Only the Gateway's grant for the signed-in account reaches a binding: anything else on the
// internal network that asks to bind its own WeChat to someone's account is refused.
func TestWeChatBindingNeedsTheGatewaysGrantForTheAccount(t *testing.T) {
	gatewayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{issuer: wechatTestIssuer, now: func() time.Time { return time.Now().UTC() }, gatewayKey: gatewayKey.Public(), gatewaySPIFFE: wechatTestGateway}
	victim := "0190f000-0000-7000-8000-000000000001"
	for name, grant := range map[string]string{
		"no grant":               "",
		"signed by someone else": gatewayGrant(t, otherKey, victim, WeChatAudience),
		"for another service":    gatewayGrant(t, gatewayKey, victim, "iam-service"),
	} {
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
				expectWeChatAnswer(t, wechatRequest(t, server, method, "/app/wechat/binding", grant, `{"code":"code-attacker"}`), http.StatusUnauthorized, "access_denied")
			}
		})
	}
}
