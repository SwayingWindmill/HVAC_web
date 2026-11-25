package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeWeChatMPFiles(t *testing.T, dir, envBody, secret string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "wechat-mp.env"), []byte(envBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if secret != "" {
		if err := os.WriteFile(filepath.Join(dir, "wechat-mp-secret"), []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWeChatMPCachesAndRefreshesTokenAndSendsMultipleOpenIDs(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32
	var sentBodies []map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("GET /cgi-bin/token", func(w http.ResponseWriter, r *http.Request) {
		n := tokenCalls.Add(1)
		if r.URL.Query().Get("appid") != "wx1234567890" || r.URL.Query().Get("secret") != "super-mp-secret" {
			t.Fatalf("unexpected token query params: %s", r.URL.RawQuery)
		}
		tok := "tok-first"
		if n > 1 {
			tok = "tok-refreshed"
		}
		_, _ = w.Write([]byte(`{"access_token":"` + tok + `","expires_in":7200}`))
	})
	mux.HandleFunc("POST /cgi-bin/message/template/send", func(w http.ResponseWriter, r *http.Request) {
		n := sendCalls.Add(1)
		tok := r.URL.Query().Get("access_token")
		if n == 1 && tok == "tok-first" {
			// Force expired token handling on first attempt
			_, _ = w.Write([]byte(`{"errcode":42001,"errmsg":"access_token expired"}`))
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatal(err)
		}
		sentBodies = append(sentBodies, parsed)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	envBody := strings.Join([]string{
		"WECHAT_MP_APP_ID=wx1234567890",
		"WECHAT_MP_TEMPLATE_ID=tmpl_alert_001",
		"WECHAT_MP_OPENIDS=openid-user-1, openid-user-2",
		"WECHAT_MP_URL=https://hvac.qlhtech.com/overview",
		"WECHAT_MP_API_BASE=" + srv.URL,
	}, "\n") + "\n"
	writeWeChatMPFiles(t, dir, envBody, "super-mp-secret\n")

	var logBuf bytes.Buffer
	logger := newJSONLogger(&logBuf, slog.LevelInfo)
	deliverer := newWeChatMPDeliverer(dir, srv.Client())
	deliverer.now = func() time.Time { return time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC) }

	handler := newHandlerWithAll(filepath.Join(dir, "missing-wecom"), srv.Client(), nil, deliverer, logger)
	alertPayload := `{"status":"firing","alerts":[{"labels":{"alertname":"Phase1HostDiskUsageHigh","severity":"page"},"annotations":{"summary":"Disk > 85% on /"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(alertPayload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if got := tokenCalls.Load(); got != 2 {
		t.Fatalf("tokenCalls = %d, want 2 (initial + 42001 refresh)", got)
	}
	if got := len(sentBodies); got != 2 {
		t.Fatalf("sentBodies len = %d, want 2", got)
	}
	if sentBodies[0]["touser"] != "openid-user-1" || sentBodies[1]["touser"] != "openid-user-2" {
		t.Fatalf("unexpected openids sent: %+v", sentBodies)
	}
	if sentBodies[0]["url"] != "https://hvac.qlhtech.com/overview" {
		t.Fatalf("missing click url: %+v", sentBodies[0])
	}
	logText := logBuf.String()
	for _, sensitive := range []string{"super-mp-secret", "tok-first", "tok-refreshed", "openid-user-1", "openid-user-2"} {
		if strings.Contains(logText, sensitive) {
			t.Fatalf("structured logs leaked sensitive string %q:\n%s", sensitive, logText)
		}
	}
}

func TestWeChatMPKeyOverridesAndThingTruncation(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/cgi-bin/token") {
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":7200}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	envBody := strings.Join([]string{
		"WECHAT_MP_APP_ID=wx_app_1",
		"WECHAT_MP_TEMPLATE_ID=tmpl_thing_1",
		"WECHAT_MP_OPENIDS=openid-one",
		"WECHAT_MP_API_BASE=" + srv.URL,
		"WECHAT_MP_KEY_TITLE=thing1",
		"WECHAT_MP_KEY_SEVERITY=-",
		"WECHAT_MP_KEY_SUMMARY=thing3",
		"WECHAT_MP_KEY_TIME=time4",
	}, "\n") + "\n"
	writeWeChatMPFiles(t, dir, envBody, "sec-1")

	deliverer := newWeChatMPDeliverer(dir, srv.Client())
	cfg, err := deliverer.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	longSummary := strings.Repeat("高", 30)
	body := `{"status":"firing","alerts":[{"labels":{"alertname":"Alert","severity":"critical"},"annotations":{"summary":"` + longSummary + `"}}]}`
	if err := deliverer.deliverAlerts(t.Context(), cfg, []byte(body), newJSONLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatalf("deliverAlerts failed: %v", err)
	}
	data, _ := sent["data"].(map[string]any)
	if _, hasSeverity := data["severity"]; hasSeverity {
		t.Fatalf("expected omitted severity key when set to '-', got %+v", data)
	}
	thing3 := data["thing3"].(map[string]any)["value"].(string)
	if len([]rune(thing3)) != 20 || !strings.HasSuffix(thing3, "…") {
		t.Fatalf("expected thing3 truncated to 20 runes with ellipsis, got %d (%q)", len([]rune(thing3)), thing3)
	}
	if _, hasTime := data["time4"]; !hasTime {
		t.Fatalf("expected custom time4 key, got %+v", data)
	}
}

func TestWeChatMPRejectsSSRFAndBadSecrets(t *testing.T) {
	t.Run("rejects non-official WECHAT_MP_API_BASE", func(t *testing.T) {
		dir := t.TempDir()
		writeWeChatMPFiles(t, dir, "WECHAT_MP_APP_ID=wx1\nWECHAT_MP_TEMPLATE_ID=t1\nWECHAT_MP_OPENIDS=o1\nWECHAT_MP_API_BASE=https://evil.example\n", "sec")
		_, err := newWeChatMPDeliverer(dir, &http.Client{}).loadConfig()
		if err == nil || !strings.Contains(err.Error(), "api.weixin.qq.com") {
			t.Fatalf("expected SSRF base rejection, got %v", err)
		}
	})

	t.Run("rejects whitespace in wechat-mp-secret", func(t *testing.T) {
		dir := t.TempDir()
		writeWeChatMPFiles(t, dir, "WECHAT_MP_APP_ID=wx1\nWECHAT_MP_TEMPLATE_ID=t1\nWECHAT_MP_OPENIDS=o1\n", "bad secret\nline2")
		_, err := newWeChatMPDeliverer(dir, &http.Client{}).loadConfig()
		if err == nil || !strings.Contains(err.Error(), "whitespace") {
			t.Fatalf("expected whitespace secret rejection, got %v", err)
		}
	})

	t.Run("drains body on upstream error", func(t *testing.T) {
		errBody := &trackingReadCloser{reader: strings.NewReader(`{"errcode":40013}`)}
		dir := t.TempDir()
		writeWeChatMPFiles(t, dir, "WECHAT_MP_APP_ID=wx1\nWECHAT_MP_TEMPLATE_ID=t1\nWECHAT_MP_OPENIDS=o1\nWECHAT_MP_API_BASE=http://127.0.0.1:9999\n", "sec")
		d := newWeChatMPDeliverer(dir, &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Body: errBody, Header: make(http.Header)}, nil
			}),
		})
		cfg, err := d.loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if err := d.deliverAlerts(t.Context(), cfg, []byte(`{"status":"firing","alerts":[]}`), newJSONLogger(io.Discard, slog.LevelInfo)); err == nil {
			t.Fatal("expected error on 502")
		}
		if !errBody.drained || !errBody.closed {
			t.Fatalf("expected response body drained and closed: drained=%v closed=%v", errBody.drained, errBody.closed)
		}
	})
}
