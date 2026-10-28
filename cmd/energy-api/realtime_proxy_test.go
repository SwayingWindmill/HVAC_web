package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealtimeProxyForwardsBrowserPathToCentrifugoConnection(t *testing.T) {
	var gotPath, gotQuery, gotHost, gotForwardedFor string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath, gotQuery, gotHost = request.URL.Path, request.URL.RawQuery, request.Host
		gotForwardedFor = request.Header.Get("X-Forwarded-For")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	t.Setenv("REALTIME_UPSTREAM_URL", upstream.URL)
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := withRealtimeProxy(next, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://hvac.example.test/realtime/websocket?cf_ws_frame_ping_pong=true", nil)
	request.RemoteAddr = "172.18.0.5:41000"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}
	if gotPath != "/connection/websocket" || gotQuery != "cf_ws_frame_ping_pong=true" || gotHost != "hvac.example.test" {
		t.Fatalf("upstream path=%q query=%q host=%q", gotPath, gotQuery, gotHost)
	}
	// The browser's address from nginx stays first; the proxy appends the hop it received the request from.
	if gotForwardedFor != "203.0.113.7, 172.18.0.5" {
		t.Fatalf("upstream X-Forwarded-For=%q", gotForwardedFor)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "https://hvac.example.test/api/v1/me", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("non-realtime status=%d", recorder.Code)
	}
}
