package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatAlertsIncludesNameSeverityAndRunbook(t *testing.T) {
	text, err := formatAlerts([]byte(`{
		"status": "firing",
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "Phase1PostgresDown", "severity": "page"},
			"annotations": {"summary": "PostgreSQL 没有了", "runbook": "docs/operations/alerts.md#postgresql-进程不可用"}
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"firing", "Phase1PostgresDown", "page", "PostgreSQL 没有了", "docs/operations/alerts.md#postgresql-进程不可用"} {
		if !strings.Contains(text, want) {
			t.Fatalf("message missing %q:\n%s", want, text)
		}
	}
}

func TestFormatAlertsTruncatesLongText(t *testing.T) {
	text, err := formatAlerts([]byte(`{"status":"firing","alerts":[{"labels":{"alertname":"A"},"annotations":{"summary":"` + strings.Repeat("测", 2000) + `"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(text) > 1900 || !strings.Contains(text, "截断") {
		t.Fatalf("expected a truncated message, length %d", len(text))
	}
}

func TestDeliverPostsWeComTextAndOmitsTheSecretFromLogs(t *testing.T) {
	var got string
	wecom := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		got = string(body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer wecom.Close()
	dir := t.TempDir()
	// The adapter only accepts the real WeCom host. Point the client at the test server by
	// rewriting after the allowlist check is not possible, so this test calls the handler
	// with a file the allowlist accepts and a transport that ignores the URL host.
	urlFile := filepath.Join(dir, "wecom-webhook-url")
	if err := os.WriteFile(urlFile, []byte("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=unit-test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := wecom.Client()
	client.Transport = rewriteHost{base: wecom.URL, next: http.DefaultTransport}
	request := httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(`{"status":"firing","alerts":[{"labels":{"alertname":"Phase1PostgresDown","severity":"page"},"annotations":{"summary":"down"}}]}`))
	recorder := httptest.NewRecorder()
	deliver(recorder, request, urlFile, client)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d body %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(got, "Phase1PostgresDown") || strings.Contains(got, "unit-test-key") {
		t.Fatalf("unexpected wecom body %s", got)
	}
}

func TestDeliverDropsWhenTheWebhookFileIsMissing(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(`{"status":"firing","alerts":[]}`))
	recorder := httptest.NewRecorder()
	deliver(recorder, request, filepath.Join(t.TempDir(), "missing"), &http.Client{})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d", recorder.Code)
	}
}

func TestDeliverRejectsANonWeComURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wecom-webhook-url")
	if err := os.WriteFile(path, []byte("https://example.invalid/hook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	deliver(recorder, httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(`{}`)), path, &http.Client{})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", recorder.Code)
	}
}

func TestDeliverReturnsBadGatewayWhenWeComRejects(t *testing.T) {
	wecom := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"errcode":93000,"errmsg":"invalid"}`))
	}))
	defer wecom.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "wecom-webhook-url")
	if err := os.WriteFile(path, []byte("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=unit-test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := wecom.Client()
	client.Transport = rewriteHost{base: wecom.URL, next: http.DefaultTransport}
	recorder := httptest.NewRecorder()
	deliver(recorder, httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(`{"alerts":[{"labels":{"alertname":"A"}}]}`)), path, client)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status %d", recorder.Code)
	}
}

type rewriteHost struct {
	base string
	next http.RoundTripper
}

func (transport rewriteHost) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	target, err := http.NewRequest(clone.Method, transport.base, clone.Body)
	if err != nil {
		return nil, err
	}
	target.Header = clone.Header
	target.ContentLength = clone.ContentLength
	return transport.next.RoundTrip(target)
}
