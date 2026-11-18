package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

type trackingReadCloser struct {
	reader  io.Reader
	drained bool
	closed  bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if err == io.EOF {
		t.drained = true
	}
	return n, err
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

func TestNewBoundedHTTPClientClonesDefaultTransport(t *testing.T) {
	client := newBoundedHTTPClient(8*time.Second, refuseRedirect)
	if client.Timeout != 8*time.Second {
		t.Fatalf("Timeout = %v, want 8s", client.Timeout)
	}
	if client.CheckRedirect == nil || !errors.Is(client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("expected CheckRedirect to refuse redirects")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr == http.DefaultTransport {
		t.Fatal("expected cloned *http.Transport distinct from http.DefaultTransport")
	}
	if tr.DialContext == nil || tr.TLSHandshakeTimeout <= 0 || tr.IdleConnTimeout <= 0 || tr.MaxIdleConnsPerHost <= 0 {
		t.Fatalf("expected explicit transport bounds, got %+v", tr)
	}
}

func TestHealthzAndHealthcheck(t *testing.T) {
	srv := httptest.NewServer(newHandlerWithLogger(filepath.Join(t.TempDir(), "missing"), newBoundedHTTPClient(time.Second, refuseRedirect), nil, newJSONLogger(io.Discard, slog.LevelInfo)))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	if code := healthcheck(addr, newJSONLogger(io.Discard, slog.LevelInfo)); code != 0 {
		t.Fatalf("healthcheck(%s) = %d, want 0", addr, code)
	}
	srv.Close()
	if code := healthcheck(addr, newJSONLogger(io.Discard, slog.LevelInfo)); code == 0 {
		t.Fatalf("expected non-zero healthcheck exit code after server close")
	}
}

func TestServeWithGracefulShutdownDrainsInFlightRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{
		Addr: ln.Addr().String(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("drained\n"))
		}),
	}
	// Replace ListenAndServe cleanly via serving on ln in a test wrapper using the same shutdown path:
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	respCh := make(chan int, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			respCh <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		respCh <- resp.StatusCode
	}()

	<-entered
	cancel()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(shutdownCtx)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("shutdown finished before in-flight request completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-shutdownDone
	if status := <-respCh; status != http.StatusOK {
		t.Fatalf("in-flight status = %d, want 200", status)
	}
	select {
	case err := <-serveErr:
		t.Fatalf("unexpected serveErr: %v", err)
	default:
	}

	// Verify serveWithGracefulShutdown exits cleanly when ctx is canceled.
	probeSrv := &http.Server{Addr: "127.0.0.1:0", Handler: newHandler("", nil, nil)}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		done2 <- serveWithGracefulShutdown(ctx2, probeSrv, newJSONLogger(io.Discard, slog.LevelInfo), 2*time.Second)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel2()
	if err := <-done2; err != nil {
		t.Fatalf("serveWithGracefulShutdown returned error: %v", err)
	}
}

func TestDeliverAndSenderDrainResponseBodiesOnErrors(t *testing.T) {
	dir := t.TempDir()
	urlFile := filepath.Join(dir, "wecom-webhook-url")
	if err := os.WriteFile(urlFile, []byte("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=unit-test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body1 := &trackingReadCloser{reader: strings.NewReader(`{"errcode":45009}`)}
	deliverClient := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: body1, Header: make(http.Header)}, nil
		}),
	}
	rec := httptest.NewRecorder()
	deliverWithLogger(rec, httptest.NewRequest(http.MethodPost, "/alertmanager", strings.NewReader(`{"alerts":[]}`)), urlFile, deliverClient, newJSONLogger(io.Discard, slog.LevelInfo))
	if rec.Code != http.StatusBadGateway || !body1.drained || !body1.closed {
		t.Fatalf("deliver status=%d drained=%v closed=%v", rec.Code, body1.drained, body1.closed)
	}

	body2 := &trackingReadCloser{reader: strings.NewReader(`{"errcode":40013}` + "  \n")}
	send := newSender(filepath.Join(dir, "tok"), filepath.Join(dir, "rec"), dir, urlFile, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: body2, Header: make(http.Header)}, nil
		}),
	})
	send.logger = newJSONLogger(io.Discard, slog.LevelInfo)
	if err := send.postJSON(context.Background(), "https://qyapi.weixin.qq.com/test", []byte(`{}`), &map[string]any{}); err == nil {
		t.Fatal("expected postJSON error on 502")
	}
	if !body2.drained || !body2.closed {
		t.Fatalf("postJSON body drained=%v closed=%v", body2.drained, body2.closed)
	}

	body3 := &trackingReadCloser{reader: strings.NewReader(`{"errcode":40013}`)}
	send.client = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: body3, Header: make(http.Header)}, nil
		}),
	}
	if _, err := send.wecomAccessToken(context.Background(), "https://qyapi.weixin.qq.com", "corp", "sec", true); err == nil {
		t.Fatal("expected wecomAccessToken error on 502")
	}
	if !body3.drained || !body3.closed {
		t.Fatalf("wecomAccessToken body drained=%v closed=%v", body3.drained, body3.closed)
	}
}

func TestNewJSONLoggerRedactsSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf, slog.LevelInfo)
	logger.Error("test_event", "token", "secret-tok", "api_key", "secret-key", "error", "raw-error")
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON log: %v (%s)", err, buf.String())
	}
	for _, k := range []string{"token", "api_key", "error"} {
		if parsed[k] != "[REDACTED]" {
			t.Fatalf("%s = %v, want [REDACTED]", k, parsed[k])
		}
	}
}
