package main

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestRealtimeProxyRateLimitsHandshakeFloodsPerClientIP(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	t.Setenv("REALTIME_UPSTREAM_URL", upstream.URL)

	handler, err := withRealtimeProxy(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTeapot)
		}),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	for i := range realtimeRateBurst {
		req := httptest.NewRequest(http.MethodGet, "https://hvac.example.test/realtime/websocket", nil)
		req.RemoteAddr = "172.18.0.5:41000"
		req.Header.Set("X-Forwarded-For", "198.51.100.10")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d within burst failed with status %d", i, rec.Code)
		}
	}

	// Spoofing an earlier X-Forwarded-For entry must not bypass the rightmost nginx client IP bucket.
	floodReq := httptest.NewRequest(http.MethodGet, "https://hvac.example.test/realtime/websocket", nil)
	floodReq.RemoteAddr = "172.18.0.5:41001"
	floodReq.Header.Set("X-Forwarded-For", "203.0.113.250, 198.51.100.10")
	floodRec := httptest.NewRecorder()
	handler.ServeHTTP(floodRec, floodReq)
	if floodRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on flood past burst, got %d", floodRec.Code)
	}
	retryAfter, convErr := strconv.Atoi(floodRec.Header().Get("Retry-After"))
	if convErr != nil || retryAfter < 1 {
		t.Fatalf("expected positive integer Retry-After header, got %q (err=%v)", floodRec.Header().Get("Retry-After"), convErr)
	}
	if upstreamHits.Load() != realtimeRateBurst {
		t.Fatalf("expected rate-limited request not to reach upstream, hits=%d", upstreamHits.Load())
	}

	otherReq := httptest.NewRequest(http.MethodGet, "https://hvac.example.test/realtime/websocket", nil)
	otherReq.RemoteAddr = "172.18.0.5:41002"
	otherReq.Header.Set("X-Forwarded-For", "198.51.100.11")
	otherRec := httptest.NewRecorder()
	handler.ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusNoContent {
		t.Fatalf("expected distinct client IP to succeed, got %d", otherRec.Code)
	}
}

func TestRealtimeClientLimiterBoundsMapCardinalityAndRaceSafety(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	limiter := newRealtimeClientLimiter(3, time.Minute, 2, func() time.Time { return now })

	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if ok, _ := limiter.allow(ip); !ok {
			t.Fatalf("expected initial request for %s to succeed", ip)
		}
	}
	if ok, _ := limiter.allow("192.0.2.1"); !ok {
		t.Fatal("expected second request for 192.0.2.1 to succeed")
	}
	if ok, _ := limiter.allow("192.0.2.4"); !ok {
		t.Fatal("expected request for 192.0.2.4 to succeed")
	}
	if got := limiter.trackedClients(); got != 3 {
		t.Fatalf("expected map bounded at 3 clients, got %d", got)
	}
	if ok, _ := limiter.allow("192.0.2.1"); ok {
		t.Fatal("expected recently active 192.0.2.1 bucket to be retained and rate limited")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	t.Setenv("REALTIME_UPSTREAM_URL", upstream.URL)

	concurrentLimiter := newRealtimeClientLimiter(8, time.Minute, 5, nil)
	handler, err := newRealtimeProxyHandler(http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)), concurrentLimiter)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 20 {
				req := httptest.NewRequest(http.MethodGet, "https://hvac.example.test/realtime/websocket", nil)
				req.RemoteAddr = "198.51.100." + strconv.Itoa((worker*20+i)%40+1) + ":50000"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
			}
		}(g)
	}
	wg.Wait()

	if got := concurrentLimiter.trackedClients(); got > 8 {
		t.Fatalf("expected trackedClients <= 8 under concurrency, got %d", got)
	}
}

func TestRealtimeProxyWebSocketUpgradeOutlivesServerTimeouts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			http.Error(writer, "expected upgrade", http.StatusBadRequest)
			return
		}
		conn, brw, err := http.NewResponseController(writer).Hijack()
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
		line, err := brw.ReadString('\n')
		if err == nil {
			time.Sleep(220 * time.Millisecond)
			_, _ = brw.WriteString("echo:" + line)
			_ = brw.Flush()
		}
	}))
	defer upstream.Close()
	t.Setenv("REALTIME_UPSTREAM_URL", upstream.URL)

	handler, err := withRealtimeProxy(http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	gatewaySrv := httptest.NewUnstartedServer(handler)
	gatewaySrv.Config.ReadTimeout = 80 * time.Millisecond
	gatewaySrv.Config.WriteTimeout = 80 * time.Millisecond
	gatewaySrv.Start()
	defer gatewaySrv.Close()

	addr := strings.TrimPrefix(gatewaySrv.URL, "http://")
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, err = io.WriteString(conn, "GET /realtime/websocket HTTP/1.1\r\nHost: "+addr+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 Switching Protocols, got %d", resp.StatusCode)
	}

	time.Sleep(150 * time.Millisecond)
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatalf("write after server timeout failed: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read after server timeout failed: %v", err)
	}
	if got != "echo:ping\n" {
		t.Fatalf("expected echoed ws payload, got %q", got)
	}
}
