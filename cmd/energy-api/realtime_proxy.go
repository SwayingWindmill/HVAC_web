package main

import (
	"bufio"
	"container/list"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	realtimePublicPrefix      = "/realtime/"
	realtimeMaxTrackedClients = 4096
	realtimeRateWindow        = time.Minute
	realtimeRateBurst         = 30
)

type realtimeClientBucket struct {
	key    string
	tokens float64
	last   time.Time
}

type realtimeClientLimiter struct {
	mu         sync.Mutex
	now        func() time.Time
	maxClients int
	burst      float64
	ratePerSec float64
	lru        *list.List
	entries    map[string]*list.Element
}

func newRealtimeClientLimiter(maxClients int, window time.Duration, burst int, now func() time.Time) *realtimeClientLimiter {
	if maxClients < 1 {
		maxClients = 1
	}
	if burst < 1 {
		burst = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &realtimeClientLimiter{
		now:        now,
		maxClients: maxClients,
		burst:      float64(burst),
		ratePerSec: float64(burst) / window.Seconds(),
		lru:        list.New(),
		entries:    make(map[string]*list.Element),
	}
}

func (l *realtimeClientLimiter) allow(clientKey string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if elem, ok := l.entries[clientKey]; ok {
		l.lru.MoveToFront(elem)
		bucket := elem.Value.(*realtimeClientBucket)
		return l.consumeLocked(bucket, now)
	}
	if len(l.entries) >= l.maxClients {
		if oldest := l.lru.Back(); oldest != nil {
			l.lru.Remove(oldest)
			delete(l.entries, oldest.Value.(*realtimeClientBucket).key)
		}
	}
	bucket := &realtimeClientBucket{
		key:    clientKey,
		tokens: l.burst,
		last:   now,
	}
	elem := l.lru.PushFront(bucket)
	l.entries[clientKey] = elem
	return l.consumeLocked(bucket, now)
}

func (l *realtimeClientLimiter) consumeLocked(bucket *realtimeClientBucket, now time.Time) (bool, time.Duration) {
	if elapsed := now.Sub(bucket.last).Seconds(); elapsed > 0 {
		bucket.tokens = math.Min(l.burst, bucket.tokens+elapsed*l.ratePerSec)
		bucket.last = now
	}
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	needed := (1 - bucket.tokens) / l.ratePerSec
	retryAfter := time.Duration(math.Ceil(needed)) * time.Second
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter
}

func (l *realtimeClientLimiter) trackedClients() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func realtimeClientIP(request *http.Request) string {
	forwardedValues := request.Header.Values("X-Forwarded-For")
	for i := len(forwardedValues) - 1; i >= 0; i-- {
		parts := strings.Split(forwardedValues[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			if addr, err := netip.ParseAddr(strings.TrimSpace(parts[j])); err == nil {
				return addr.Unmap().String()
			}
		}
	}
	host := strings.TrimSpace(request.RemoteAddr)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().String()
	}
	if host != "" {
		return host
	}
	return "unknown"
}

func isRealtimeWebSocketUpgrade(request *http.Request) bool {
	if strings.TrimSpace(request.Header.Get("Upgrade")) == "" {
		return false
	}
	for _, value := range request.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

type realtimeUpgradeResponseWriter struct {
	http.ResponseWriter
}

func (w *realtimeUpgradeResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Hijack clears http.Server's read/write deadlines only after Centrifugo accepts 101 Switching Protocols,
// leaving ordinary HTTP handshakes and non-upgraded paths bounded by the server timeouts.
func (w *realtimeUpgradeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && conn != nil {
		if deadlineErr := conn.SetDeadline(time.Time{}); deadlineErr != nil {
			_ = conn.Close()
			return nil, nil, deadlineErr
		}
	}
	return conn, brw, err
}

// withRealtimeProxy keeps the browser-facing realtime boundary inside energy-api.
// The current Centrifugo transport remains an internal implementation detail and
// is not exposed directly by Nginx.
func withRealtimeProxy(next http.Handler, logger *slog.Logger) (http.Handler, error) {
	limiter := newRealtimeClientLimiter(realtimeMaxTrackedClients, realtimeRateWindow, realtimeRateBurst, nil)
	return newRealtimeProxyHandler(next, logger, limiter)
}

func newRealtimeProxyHandler(next http.Handler, logger *slog.Logger, limiter *realtimeClientLimiter) (http.Handler, error) {
	rawUpstream := strings.TrimSpace(os.Getenv("REALTIME_UPSTREAM_URL"))
	if rawUpstream == "" {
		rawUpstream = "http://centrifugo:8000"
	}
	upstream, err := url.Parse(rawUpstream)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return nil, fmt.Errorf("invalid REALTIME_UPSTREAM_URL %q", rawUpstream)
	}

	proxy := &httputil.ReverseProxy{
		Transport: cloneDefaultTransportWithTLS(nil),
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(upstream)
			proxyRequest.Out.URL.Path = "/connection/" + strings.TrimPrefix(proxyRequest.In.URL.Path, realtimePublicPrefix)
			proxyRequest.Out.URL.RawPath = ""
			// Keep the browser's Host, as the proxy did before Rewrite; SetURL would replace it with the upstream's.
			proxyRequest.Out.Host = proxyRequest.In.Host
			// Rewrite drops the inbound X-Forwarded-For; copy it so SetXForwarded appends to nginx's chain like Director did.
			proxyRequest.Out.Header["X-Forwarded-For"] = proxyRequest.In.Header["X-Forwarded-For"]
			proxyRequest.SetXForwarded()
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
			logger.Warn("energy_api_realtime_upstream_unavailable", "error_code", "REALTIME_UPSTREAM_UNAVAILABLE")
			http.Error(writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, realtimePublicPrefix) {
			if limiter != nil {
				allowed, retryAfter := limiter.allow(realtimeClientIP(request))
				if !allowed {
					seconds := max(int(math.Ceil(retryAfter.Seconds())), 1)
					writer.Header().Set("Retry-After", strconv.Itoa(seconds))
					http.Error(writer, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
					return
				}
			}
			targetWriter := writer
			if isRealtimeWebSocketUpgrade(request) {
				targetWriter = &realtimeUpgradeResponseWriter{ResponseWriter: writer}
			}
			proxy.ServeHTTP(targetWriter, request)
			return
		}
		next.ServeHTTP(writer, request)
	}), nil
}
