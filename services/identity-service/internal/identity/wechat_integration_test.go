package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Binding WeChat in the mini program (#664, docs/specs/miniprogram-wechat.md): identity-service
// trades the one-time code from wx.login for the person's OpenID at WeChat and keeps one WeChat
// per account and one account per WeChat. The session key WeChat sends along is never kept.

const (
	wechatTestAppID      = "wx-test-app"
	wechatTestSecret     = "wechat-test-secret"
	wechatTestSessionKey = "session-key-that-must-not-be-kept"
)

// fakeWeChat answers code2Session the way WeChat does: an OpenID for a known code, error 40163
// for a code already used.
func fakeWeChat(t *testing.T, openIDs map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path != "/sns/jscode2session" || query.Get("appid") != wechatTestAppID || query.Get("secret") != wechatTestSecret || query.Get("grant_type") != "authorization_code" {
			t.Errorf("unexpected WeChat request %s", request.URL.Redacted())
			writer.WriteHeader(http.StatusBadRequest)
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

func wechatRequest(t *testing.T, server *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
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
	server := &Server{store: runtimeStore, now: func() time.Time { return now }, wechat: &WeChat{AppID: wechatTestAppID, Secret: wechatTestSecret, BaseURL: weChat.URL, Client: weChat.Client()}}
	bind := func(userID, code string) *httptest.ResponseRecorder {
		return wechatRequest(t, server, http.MethodPost, "/app/wechat/binding", `{"userId":"`+userID+`","code":"`+code+`"}`)
	}
	binding := func(userID string) map[string]any {
		recorder := wechatRequest(t, server, http.MethodGet, "/app/wechat/binding?userId="+url.QueryEscape(userID), "")
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
	if answer := binding(duty); answer["bound"] != true || answer["boundAt"] != "2026-10-21T08:00:00Z" {
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

	expectWeChatAnswer(t, wechatRequest(t, server, http.MethodDelete, "/app/wechat/binding?userId="+url.QueryEscape(duty), ""), http.StatusNoContent, "")
	if answer := binding(duty); answer["bound"] != false {
		t.Fatalf("after removing: %v", answer)
	}

	// Without the AppSecret on the server, WeChat is plainly not set up.
	unconfigured := &Server{store: runtimeStore, now: server.now}
	expectWeChatAnswer(t, wechatRequest(t, unconfigured, http.MethodPost, "/app/wechat/binding", `{"userId":"`+duty+`","code":"code-duty"}`), http.StatusServiceUnavailable, "wechat_not_configured")
}
