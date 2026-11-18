package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/redis/go-redis/v9"
)

type trackingReadCloser struct {
	data   []byte
	offset int
	readN  int
	closed bool
}

func (r *trackingReadCloser) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	r.readN += n
	return n, nil
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestCloneDefaultTransportWithTLSAndRedisAndBodyBounds(t *testing.T) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("expected http.DefaultTransport to be *http.Transport")
	}

	cloned := cloneDefaultTransportWithTLS(&tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: "upstream.internal",
	})
	if cloned.TLSHandshakeTimeout != 5*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %s, want 5s", cloned.TLSHandshakeTimeout)
	}
	if cloned.IdleConnTimeout != 90*time.Second {
		t.Fatalf("IdleConnTimeout = %s, want 90s", cloned.IdleConnTimeout)
	}
	if cloned.MaxIdleConnsPerHost < 16 || cloned.MaxIdleConns <= 0 {
		t.Fatalf("unexpected idle pool bounds: MaxIdleConns=%d MaxIdleConnsPerHost=%d", cloned.MaxIdleConns, cloned.MaxIdleConnsPerHost)
	}
	if cloned.DialContext == nil {
		t.Fatal("expected DialContext to be configured on cloned transport")
	}
	if cloned.TLSClientConfig == nil || cloned.TLSClientConfig.ServerName != "upstream.internal" {
		t.Fatalf("unexpected TLSClientConfig: %#v", cloned.TLSClientConfig)
	}
	if defaultTransport.TLSClientConfig != nil && defaultTransport.TLSClientConfig.ServerName == "upstream.internal" {
		t.Fatal("cloning mutated global http.DefaultTransport")
	}

	redirectClient := nonRedirectingClient(cloned)
	if redirectClient.Timeout != 10*time.Second {
		t.Fatalf("nonRedirectingClient.Timeout = %s, want 10s", redirectClient.Timeout)
	}
	if err := redirectClient.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect returned %v, want http.ErrUseLastResponse", err)
	}

	defaultRedis := &redis.Options{}
	applyRedisClientBounds(defaultRedis)
	if defaultRedis.DialTimeout != 3*time.Second || defaultRedis.ReadTimeout != 2*time.Second ||
		defaultRedis.WriteTimeout != 2*time.Second || defaultRedis.PoolTimeout != 3*time.Second ||
		defaultRedis.PoolSize != 16 || defaultRedis.MinIdleConns != 2 {
		t.Fatalf("unexpected bounded default redis options: %+v", defaultRedis)
	}

	oversizedRedis := &redis.Options{
		DialTimeout:  30 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		PoolTimeout:  30 * time.Second,
		PoolSize:     256,
		MinIdleConns: 0,
	}
	applyRedisClientBounds(oversizedRedis)
	if oversizedRedis.DialTimeout > 5*time.Second || oversizedRedis.ReadTimeout > 5*time.Second ||
		oversizedRedis.WriteTimeout > 5*time.Second || oversizedRedis.PoolTimeout > 5*time.Second ||
		oversizedRedis.PoolSize != 32 || oversizedRedis.MinIdleConns != 2 {
		t.Fatalf("oversized redis options were not clamped: %+v", oversizedRedis)
	}

	smallBody := &trackingReadCloser{data: bytes.Repeat([]byte("a"), 1024)}
	drainAndCloseBody(smallBody)
	if smallBody.readN != 1024 || !smallBody.closed {
		t.Fatalf("small body readN=%d closed=%v, want readN=1024 closed=true", smallBody.readN, smallBody.closed)
	}

	largeBody := &trackingReadCloser{data: bytes.Repeat([]byte("b"), 128<<10)}
	drainAndCloseBody(largeBody)
	if largeBody.readN != maxDrainBodyBytes || !largeBody.closed {
		t.Fatalf("large body readN=%d closed=%v, want readN=%d closed=true", largeBody.readN, largeBody.closed, maxDrainBodyBytes)
	}
}

func TestShutdownGatewayReverseOrderWaitsForHTTPDrainBeforeCancel(t *testing.T) {
	logger := observability.NewJSONLogger(io.Discard, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:       "platform-gateway-test",
		QueueSize:     16,
		ExportTimeout: 100 * time.Millisecond,
		Logger:        logger,
	})
	telemetry.MarkReady()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = telemetry.Shutdown(ctx)
	})

	pubListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen public: %v", err)
	}
	diagListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = pubListener.Close()
		t.Fatalf("listen diagnostics: %v", err)
	}

	inFlightEntered := make(chan struct{})
	releaseInFlight := make(chan struct{})
	publicServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(inFlightEntered)
			<-releaseInFlight
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("drained"))
		}),
	}
	diagServer := &http.Server{
		Handler: telemetry.DiagnosticsHandler(),
	}

	var httpWG sync.WaitGroup
	httpWG.Add(2)
	go func() {
		defer httpWG.Done()
		_ = publicServer.Serve(pubListener)
	}()
	go func() {
		defer httpWG.Done()
		_ = diagServer.Serve(diagListener)
	}()

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	var bgWG sync.WaitGroup
	var bgExited atomic.Bool
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		<-runCtx.Done()
		bgExited.Store(true)
	}()

	// Verify diagnostics is initially ready before triggering shutdown.
	client := &http.Client{Timeout: 3 * time.Second}
	readyResp, err := client.Get("http://" + diagListener.Addr().String() + "/health/ready")
	if err != nil {
		close(releaseInFlight)
		t.Fatalf("initial /health/ready failed: %v", err)
	}
	drainAndCloseBody(readyResp.Body)
	if readyResp.StatusCode != http.StatusOK {
		close(releaseInFlight)
		t.Fatalf("initial /health/ready status = %d, want 200", readyResp.StatusCode)
	}

	reqStatus := make(chan int, 1)
	reqErr := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + pubListener.Addr().String() + "/api/v1/test")
		if err != nil {
			reqErr <- err
			return
		}
		drainAndCloseBody(resp.Body)
		reqStatus <- resp.StatusCode
	}()

	<-inFlightEntered

	shutdownFinished := make(chan struct{})
	go func() {
		defer close(shutdownFinished)
		shutdownGateway(logger, telemetry, nil, publicServer, nil, diagServer, &httpWG, cancelRun, &bgWG, 5*time.Second)
	}()

	// Wait briefly to confirm readiness drops immediately while background context stays live during HTTP drain.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		resp, err := client.Get("http://" + diagListener.Addr().String() + "/health/ready")
		if err == nil {
			status := resp.StatusCode
			drainAndCloseBody(resp.Body)
			if status == http.StatusServiceUnavailable {
				break
			}
		}
		if time.Now().After(deadline) {
			close(releaseInFlight)
			t.Fatalf("expected /health/ready to switch to 503 immediately while HTTP drains")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if runCtx.Err() != nil {
		close(releaseInFlight)
		t.Fatalf("expected background context to stay active while ingress HTTP request is draining, got %v", runCtx.Err())
	}
	if bgExited.Load() {
		close(releaseInFlight)
		t.Fatal("background worker exited before HTTP drain completed")
	}
	select {
	case <-shutdownFinished:
		t.Fatal("shutdownGateway returned before in-flight HTTP request completed")
	default:
	}

	close(releaseInFlight)

	select {
	case err := <-reqErr:
		t.Fatalf("in-flight HTTP request failed: %v", err)
	case status := <-reqStatus:
		if status != http.StatusOK {
			t.Fatalf("in-flight HTTP status = %d, want 200", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for in-flight HTTP request")
	}

	select {
	case <-shutdownFinished:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for shutdownGateway to finish")
	}

	if !errors.Is(runCtx.Err(), context.Canceled) {
		t.Fatalf("runCtx.Err() = %v, want context.Canceled after shutdownGateway", runCtx.Err())
	}
	if !bgExited.Load() {
		t.Fatal("expected background worker to exit before shutdownGateway returned")
	}
}
