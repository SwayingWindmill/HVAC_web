package identity

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// Sending a WeChat subscription message to an account (#664, docs/specs/miniprogram-wechat.md):
// Notification Service names the account in a grant the Gateway's workload signs, and
// identity-service, the only holder of the OpenID and the AppSecret, sends it.

// fakeWeChatMessages answers stable_token and subscribeMessage.send the way WeChat does: a person
// with no reminder left refuses with errcode 43101.
type fakeWeChatMessages struct {
	mu       sync.Mutex
	tokens   int
	sent     []map[string]any
	refusing string
}

func (fake *fakeWeChatMessages) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	switch request.URL.Path {
	case "/cgi-bin/stable_token":
		var input map[string]any
		_ = json.NewDecoder(request.Body).Decode(&input)
		if input["appid"] != wechatTestAppID || input["secret"] != wechatTestSecret || input["grant_type"] != "client_credential" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 40125, "errmsg": "invalid appsecret"})
			return
		}
		fake.tokens++
		_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "token-" + string(rune('0'+fake.tokens)), "expires_in": 7200})
	case "/cgi-bin/message/subscribe/send":
		if !strings.HasPrefix(request.URL.Query().Get("access_token"), "token-") {
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 40001, "errmsg": "invalid credential"})
			return
		}
		var message map[string]any
		_ = json.NewDecoder(request.Body).Decode(&message)
		if message["touser"] == fake.refusing {
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 43101, "errmsg": "user refuse to accept the msg"})
			return
		}
		fake.sent = append(fake.sent, message)
		_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// workloadGrant is what the Gateway's workload signs for itself to send a message to one account.
func workloadGrant(t *testing.T, signer crypto.Signer, action, account string, now time.Time) string {
	t.Helper()
	grant, err := identitycontext.SignDelegation(signer, identitycontext.DelegationClaims{
		Issuer: wechatTestGateway, ExecutingService: wechatTestGateway, Audience: WeChatAudience,
		Subject: wechatTestGateway, SubjectIssuer: "spiffe://hvac.local", TenantID: "tenant-1", PolicyRevision: "policy-v1",
		Actions: []string{action}, Scopes: []string{"account:" + account},
		IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomToken(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

const alarmMessage = `{"templateId":"alarm-template","page":"pages/alarm/index?id=alarm-1","data":{"thing10":"网关上报中断","short_thing15":"重要","thing9":"未关联设备","thing4":"松雅轩","time2":"2026-10-21 13:56"}}`

func TestWeChatSendsAMessageToTheBoundAccount(t *testing.T) {
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
	stamp := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	createUser := func(name string) string {
		user, err := adminStore.CreateUser(ctx, CreateUserInput{Username: name + stamp, DisplayName: name, Email: name + stamp + "@example.invalid", Password: randomToken(24), Now: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		return user.ID
	}
	duty, refusing, unbound := createUser("send-duty"), createUser("send-refusing"), createUser("send-unbound")
	for user, openID := range map[string]string{duty: "openid-send-duty-" + stamp, refusing: "openid-send-refusing-" + stamp} {
		if err := runtimeStore.BindWeChat(ctx, user, wechatTestAppID, openID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeWeChatMessages{refusing: "openid-send-refusing-" + stamp}
	weChatServer := httptest.NewServer(fake)
	t.Cleanup(weChatServer.Close)
	gatewayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	var logs bytes.Buffer
	server := &Server{
		issuer: wechatTestIssuer, store: runtimeStore, now: func() time.Time { return clock }, gatewayKey: gatewayKey.Public(), gatewaySPIFFE: wechatTestGateway,
		wechat: &WeChat{AppID: wechatTestAppID, Secret: wechatTestSecret, BaseURL: weChatServer.URL, Client: weChatServer.Client(), State: "developer"},
		logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	send := func(account string) *httptest.ResponseRecorder {
		return wechatRequest(t, server, http.MethodPost, "/app/wechat/messages", workloadGrant(t, gatewayKey, WeChatSendAction, account, clock), alarmMessage)
	}

	expectWeChatAnswer(t, send(duty), http.StatusNoContent, "")
	expectWeChatAnswer(t, send(duty), http.StatusNoContent, "")
	if fake.tokens != 1 || len(fake.sent) != 2 {
		t.Fatalf("WeChat's interface credential should be fetched once and reused: tokens=%d sent=%d", fake.tokens, len(fake.sent))
	}
	message := fake.sent[0]
	data, _ := message["data"].(map[string]any)
	title, _ := data["thing10"].(map[string]any)
	if message["touser"] != "openid-send-duty-"+stamp || message["template_id"] != "alarm-template" || message["page"] != "pages/alarm/index?id=alarm-1" ||
		message["miniprogram_state"] != "developer" || title["value"] != "网关上报中断" {
		t.Fatalf("message sent = %v", message)
	}
	// The credential is fetched again once it has expired.
	clock = clock.Add(2 * time.Hour)
	expectWeChatAnswer(t, send(duty), http.StatusNoContent, "")
	if fake.tokens != 2 {
		t.Fatalf("an expired credential was reused: tokens=%d", fake.tokens)
	}

	// WeChat refusing a person with no reminder left, and an account without WeChat, say so.
	expectWeChatAnswer(t, send(refusing), http.StatusConflict, "wechat_reminder_refused")
	expectWeChatAnswer(t, send(unbound), http.StatusNotFound, "wechat_not_bound")

	// Only the Gateway workload's own grant to send reaches WeChat: not a person's grant, nor one for another action.
	expectWeChatAnswer(t, wechatRequest(t, server, http.MethodPost, "/app/wechat/messages", gatewayGrant(t, gatewayKey, duty, WeChatAudience), alarmMessage), http.StatusUnauthorized, "access_denied")
	expectWeChatAnswer(t, wechatRequest(t, server, http.MethodPost, "/app/wechat/messages", workloadGrant(t, gatewayKey, WeChatAction, duty, clock), alarmMessage), http.StatusUnauthorized, "access_denied")

	// When WeChat cannot be reached, the address carrying the AppSecret and the credential stays out of the log.
	weChatServer.Close()
	server.wechat.token = ""
	expectWeChatAnswer(t, send(duty), http.StatusServiceUnavailable, "temporarily_unavailable")
	if strings.Contains(logs.String(), wechatTestSecret) || strings.Contains(logs.String(), "access_token=") {
		t.Fatalf("the log carries WeChat secrets: %s", logs.String())
	}
}
