package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testSendToken = "test-send-token"

type sendFixture struct {
	dir     string
	handler http.Handler
	send    *sender
}

func newSendFixture(t *testing.T) *sendFixture {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "alarm-send-token", testSendToken+"\n")
	send := newSender(filepath.Join(dir, "alarm-send-token"), dir, filepath.Join(dir, "wecom-webhook-url"), &http.Client{Timeout: 2 * time.Second})
	send.dialSMTP = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("a mail server was dialled")
		return nil, nil
	}
	return &sendFixture{dir: dir, send: send, handler: newHandler(filepath.Join(dir, "wecom-webhook-url"), send.client, send)}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *sendFixture) post(t *testing.T, token string, body any) int {
	t.Helper()
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(string(payload)))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func message(channel, to string, dryRun bool) sendRequest {
	return sendRequest{Channel: channel, To: to, Subject: "【告警·重要】松雅轩：网关上报中断", Text: "【告警·重要】松雅轩：网关上报中断\n开始 2026-11-01 14:26（北京时间）", DryRun: dryRun, Key: "a1:FIRE:0:" + channel + ":duty"}
}

func TestSendRefusesWithoutTheToken(t *testing.T) {
	f := newSendFixture(t)
	if code := f.post(t, "", message("wecom_bot", "group", true)); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code := f.post(t, "wrong", message("wecom_bot", "group", true)); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", code)
	}
	os.Remove(filepath.Join(f.dir, "alarm-send-token"))
	if code := f.post(t, testSendToken, message("wecom_bot", "group", true)); code != http.StatusServiceUnavailable {
		t.Fatalf("no token file: %d", code)
	}
}

func TestSendRejectsBadInputAndUnconfiguredChannels(t *testing.T) {
	f := newSendFixture(t)
	if code := f.post(t, testSendToken, map[string]any{"channel": "smtp", "to": "a@example.com"}); code != http.StatusBadRequest {
		t.Fatalf("no subject: %d", code)
	}
	if code := f.post(t, testSendToken, message("sms", "x", true)); code != http.StatusBadRequest {
		t.Fatalf("unknown channel: %d", code)
	}
	bad := message("smtp", "a@example.com", true)
	bad.Subject = "a\r\nBcc: x@example.com"
	if code := f.post(t, testSendToken, bad); code != http.StatusBadRequest {
		t.Fatalf("header injection: %d", code)
	}
	for _, channel := range []string{"smtp", "wecom_app", "wecom_bot"} {
		if code := f.post(t, testSendToken, message(channel, "someone@example.com", true)); code != http.StatusNotImplemented {
			t.Fatalf("%s unconfigured: %d", channel, code)
		}
	}
}

func TestSendDryRunValidatesConfigWithoutTheNetwork(t *testing.T) {
	f := newSendFixture(t)
	write(t, f.dir, "smtp.env", "SMTP_SMARTHOST=smtp.qq.com:465\nSMTP_FROM=alarm@example.com\nSMTP_AUTH_USERNAME=alarm@example.com\n")
	write(t, f.dir, "smtp-password", "app-password\n")
	write(t, f.dir, "wecom-app.env", "WECOM_CORP_ID=ww0000\nWECOM_AGENT_ID=1000002\nWECOM_API_BASE=http://127.0.0.1:1\n")
	write(t, f.dir, "wecom-app-secret", "secret\n")
	write(t, f.dir, "wecom-webhook-url", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test\n")
	for _, m := range []sendRequest{message("smtp", "duty@example.com", true), message("wecom_app", "ZhangSan", true), message("wecom_bot", "group", true)} {
		if code := f.post(t, testSendToken, m); code != http.StatusNoContent {
			t.Fatalf("%s dry run: %d", m.Channel, code)
		}
	}
	if code := f.post(t, testSendToken, message("smtp", "not-an-address", true)); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad address: %d", code)
	}
}

// fakeSMTP answers one session over a pipe and records what the client said.
func fakeSMTP(t *testing.T, rcptReply string) (func(context.Context, string, string) (net.Conn, error), *strings.Builder, *sync.WaitGroup) {
	transcript := &strings.Builder{}
	var done sync.WaitGroup
	dial := func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		done.Add(1)
		go func() {
			defer done.Done()
			defer server.Close()
			reader := bufio.NewReader(server)
			reply := func(line string) { _, _ = io.WriteString(server, line+"\r\n") }
			reply("220 localhost ESMTP")
			inData := false
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				transcript.WriteString(line)
				if inData {
					if line == ".\r\n" {
						inData = false
						reply("250 queued")
					}
					continue
				}
				command := strings.ToUpper(strings.TrimSpace(line))
				switch {
				case strings.HasPrefix(command, "EHLO"):
					reply("250-localhost\r\n250 AUTH PLAIN")
				case strings.HasPrefix(command, "AUTH PLAIN"):
					reply("235 ok")
				case strings.HasPrefix(command, "MAIL"):
					reply("250 ok")
				case strings.HasPrefix(command, "RCPT"):
					reply(rcptReply)
				case command == "DATA":
					inData = true
					reply("354 go")
				case command == "QUIT":
					reply("221 bye")
					return
				default:
					reply("250 ok")
				}
			}
		}()
		return client, nil
	}
	return dial, transcript, &done
}

func TestSendMailOverImplicitTLS(t *testing.T) {
	f := newSendFixture(t)
	write(t, f.dir, "smtp.env", "SMTP_SMARTHOST=localhost:465\nSMTP_FROM=alarm@example.com\nSMTP_AUTH_USERNAME=alarm@example.com\n")
	write(t, f.dir, "smtp-password", "app-password\n")
	dial, transcript, done := fakeSMTP(t, "250 ok")
	f.send.dialSMTP = dial
	if code := f.post(t, testSendToken, message("smtp", "duty@example.com", false)); code != http.StatusNoContent {
		t.Fatalf("send: %d", code)
	}
	done.Wait()
	got := transcript.String()
	for _, want := range []string{"MAIL FROM:<alarm@example.com>", "RCPT TO:<duty@example.com>", "Subject: =?UTF-8?b?", "Content-Type: text/plain; charset=UTF-8"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, base64.StdEncoding.EncodeToString([]byte("【告警·重要】松雅轩"))[:20]) {
		t.Fatalf("body is not the base64 text:\n%s", got)
	}
}

func TestSendMailRefusedRecipientIsRejected(t *testing.T) {
	f := newSendFixture(t)
	write(t, f.dir, "smtp.env", "SMTP_SMARTHOST=localhost:465\nSMTP_FROM=alarm@example.com\nSMTP_AUTH_USERNAME=alarm@example.com\n")
	write(t, f.dir, "smtp-password", "app-password\n")
	dial, _, done := fakeSMTP(t, "550 no such user")
	f.send.dialSMTP = dial
	if code := f.post(t, testSendToken, message("smtp", "nobody@example.com", false)); code != http.StatusUnprocessableEntity {
		t.Fatalf("refused recipient: %d", code)
	}
	done.Wait()
	dial, _, done = fakeSMTP(t, "451 try later")
	f.send.dialSMTP = dial
	if code := f.post(t, testSendToken, message("smtp", "duty@example.com", false)); code != http.StatusBadGateway {
		t.Fatalf("temporary refusal: %d", code)
	}
	done.Wait()
}

func TestSendWeComAppCachesAndRefreshesTheToken(t *testing.T) {
	var tokens, sends atomic.Int32
	var lastBody atomic.Value
	wecom := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpid") != "ww0000" || request.URL.Query().Get("corpsecret") != "secret" {
				_, _ = io.WriteString(writer, `{"errcode":40013}`)
				return
			}
			n := tokens.Add(1)
			_, _ = io.WriteString(writer, `{"errcode":0,"access_token":"tok`+string(rune('0'+n))+`","expires_in":7200}`)
		case "/cgi-bin/message/send":
			n := sends.Add(1)
			body, _ := io.ReadAll(request.Body)
			lastBody.Store(string(body))
			switch {
			case n == 2 && request.URL.Query().Get("access_token") == "tok1":
				_, _ = io.WriteString(writer, `{"errcode":42001}`)
			case strings.Contains(string(body), `"touser":"Ghost"`):
				_, _ = io.WriteString(writer, `{"errcode":0,"invaliduser":"Ghost"}`)
			case strings.Contains(string(body), `"touser":"Blocked"`):
				_, _ = io.WriteString(writer, `{"errcode":60020}`)
			default:
				_, _ = io.WriteString(writer, `{"errcode":0}`)
			}
		}
	}))
	defer wecom.Close()
	f := newSendFixture(t)
	write(t, f.dir, "wecom-app.env", "WECOM_CORP_ID=ww0000\nWECOM_AGENT_ID=1000002\nWECOM_API_BASE="+wecom.URL+"\n")
	write(t, f.dir, "wecom-app-secret", "secret\n")
	if code := f.post(t, testSendToken, message("wecom_app", "ZhangSan", false)); code != http.StatusNoContent {
		t.Fatalf("first: %d", code)
	}
	var sent struct {
		ToUser  string `json:"touser"`
		AgentID int    `json:"agentid"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	_ = json.Unmarshal([]byte(lastBody.Load().(string)), &sent)
	if sent.ToUser != "ZhangSan" || sent.AgentID != 1000002 || !strings.Contains(sent.Text.Content, "网关上报中断") || strings.Count(sent.Text.Content, "网关上报中断") != 1 {
		t.Fatalf("app message: %+v", sent)
	}
	// The second send meets an expired token, refreshes once and goes through.
	if code := f.post(t, testSendToken, message("wecom_app", "ZhangSan", false)); code != http.StatusNoContent {
		t.Fatalf("refresh: %d", code)
	}
	if tokens.Load() != 2 || sends.Load() != 3 {
		t.Fatalf("tokens %d sends %d", tokens.Load(), sends.Load())
	}
	if code := f.post(t, testSendToken, message("wecom_app", "Ghost", false)); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown user: %d", code)
	}
	if code := f.post(t, testSendToken, message("wecom_app", "Blocked", false)); code != http.StatusNotImplemented {
		t.Fatalf("untrusted IP: %d", code)
	}
	if tokens.Load() != 2 {
		t.Fatalf("the token was not cached: %d", tokens.Load())
	}
	write(t, f.dir, "wecom-app-secret", "wrong\n")
	f.send.wecomToken = ""
	if code := f.post(t, testSendToken, message("wecom_app", "ZhangSan", false)); code != http.StatusNotImplemented {
		t.Fatalf("bad secret: %d", code)
	}
}

func TestSendWeComBotUpstreamFailureIsRetryable(t *testing.T) {
	f := newSendFixture(t)
	write(t, f.dir, "wecom-webhook-url", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test\n")
	f.send.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "qyapi.weixin.qq.com" {
			t.Fatalf("posted to %s", request.URL.Host)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errcode":45009}`)), Header: http.Header{}}, nil
	})}
	if code := f.post(t, testSendToken, message("wecom_bot", "group", false)); code != http.StatusBadGateway {
		t.Fatalf("robot rate limited: %d", code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
