package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func testServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tunnel-test"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startTestTunnelHarness(
	t *testing.T,
	targetHandler http.Handler,
	httpReadTimeout time.Duration,
	httpWriteTimeout time.Duration,
	pingInterval time.Duration,
	hubOpts ...Option,
) (loopbackAddr string, hub *Hub, edgeErrCh chan error, cancel context.CancelFunc) {
	t.Helper()
	const gatewayID = "0193f000-2000-7000-8000-000000000001"
	targetSrv := httptest.NewServer(targetHandler)
	t.Cleanup(targetSrv.Close)
	targetHostPort := strings.TrimPrefix(targetSrv.URL, "http://")

	hub = NewHub(hubOpts...)
	t.Cleanup(hub.Close)

	mux := http.NewServeMux()
	mux.Handle("GET /gateway/v1/tunnel", NewUpgradeHandler(
		hub,
		func(r *http.Request) (*x509.Certificate, error) {
			if r.Header.Get("X-Test-Gateway") != gatewayID {
				return nil, errors.New("missing gateway header")
			}
			return &x509.Certificate{Subject: pkix.Name{CommonName: gatewayID}}, nil
		},
		func(_ context.Context, peer *x509.Certificate) (string, error) {
			if peer == nil || peer.Subject.CommonName != gatewayID {
				return "", errors.New("rejected gateway")
			}
			return peer.Subject.CommonName, nil
		},
	))

	cert := testServerCert(t)
	srvLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srvLn.Close() })
	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
	}
	go func() { _ = httpSrv.Serve(srvLn) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	edgeConn, err := tls.Dial("tcp", srvLn.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, // test-only self-signed localhost cert
		NextProtos:         []string{"http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqText := "GET /gateway/v1/tunnel HTTP/1.1\r\n" +
		"Host: " + srvLn.Addr().String() + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: " + ProtocolName + "\r\n" +
		"X-Test-Gateway: " + gatewayID + "\r\n\r\n"
	if _, err := io.WriteString(edgeConn, reqText); err != nil {
		_ = edgeConn.Close()
		t.Fatal(err)
	}
	br := bufio.NewReader(edgeConn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = edgeConn.Close()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = edgeConn.Close()
		t.Fatalf("expected 101 Switching Protocols, got %d", resp.StatusCode)
	}
	var muxConn net.Conn = edgeConn
	if br.Buffered() > 0 {
		muxConn = &bufferedConn{Conn: edgeConn, r: br}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	edgeErrCh = make(chan error, 1)
	go func() {
		edgeErrCh <- RunEdgeConn(ctx, muxConn, func(dialCtx context.Context, port uint16) (net.Conn, error) {
			if port != AllowedTargetPort {
				return nil, ErrPortForbidden
			}
			var d net.Dialer
			return d.DialContext(dialCtx, "tcp", targetHostPort)
		}, pingInterval)
	}()

	loopbackLn, err := startLoopbackWithRetry(hub)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(hub.ActiveGateways()) == 1 {
			return loopbackLn.Addr().String(), hub, edgeErrCh, cancel
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway tunnel did not register in time")
	return "", nil, nil, nil
}

func startLoopbackWithRetry(hub *Hub) (net.Listener, error) {
	var lastErr error
	for range 5 {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			lastErr = err
			continue
		}
		addr := probe.Addr().String()
		_ = probe.Close()
		ln, err := hub.StartLoopbackListener(addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func TestTunnelHTTPAndLargeBodyOverLoopback(t *testing.T) {
	largeBody := bytes.Repeat([]byte("A1B2C3D4"), 16384) // 128 KiB > 64 KiB MaxFramePayload
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flows" && r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"path":"`+r.URL.Path+`"}`)
	})

	addr, _, _, _ := startTestTunnelHarness(t, handler, 10*time.Second, 10*time.Second, 50*time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get("http://" + addr + "/red/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET failed: status=%d err=%v body=%q", resp.StatusCode, err, string(body))
	}
	if string(body) != `{"ok":true,"path":"/red/settings"}` {
		t.Fatalf("unexpected GET body: %q", string(body))
	}

	postResp, err := client.Post("http://"+addr+"/flows", "application/json", bytes.NewReader(largeBody))
	if err != nil {
		t.Fatal(err)
	}
	gotLarge, err := io.ReadAll(postResp.Body)
	_ = postResp.Body.Close()
	if err != nil || postResp.StatusCode != http.StatusOK {
		t.Fatalf("POST large failed: status=%d err=%v", postResp.StatusCode, err)
	}
	if !bytes.Equal(gotLarge, largeBody) {
		t.Fatalf("large body mismatch: got %d bytes want %d", len(gotLarge), len(largeBody))
	}
}

func TestTunnelUpgradeStreamCommsEcho(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comms" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected ws upgrade", http.StatusBadRequest)
			return
		}
		rc := http.NewResponseController(w)
		c, brw, err := rc.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
		line, err := brw.ReadString('\n')
		if err == nil {
			_, _ = brw.WriteString("echo:" + line)
			_ = brw.Flush()
		}
	})

	addr, _, _, _ := startTestTunnelHarness(t, handler, 10*time.Second, 10*time.Second, 50*time.Millisecond)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET /comms HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 from /comms upgrade, got %d", resp.StatusCode)
	}
	if _, err := io.WriteString(conn, "nodered-deploy-ping\n"); err != nil {
		t.Fatal(err)
	}
	got, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if got != "echo:nodered-deploy-ping\n" {
		t.Fatalf("unexpected ws upgrade stream response: %q", got)
	}
}

func TestTunnelRejectsForbiddenTargetPorts(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	for _, forbidden := range []uint16{22, 80, 443, 1883, 5432} {
		if _, err := hub.OpenStream(context.Background(), "", forbidden); !errors.Is(err, ErrPortForbidden) {
			t.Fatalf("port %d expected ErrPortForbidden, got %v", forbidden, err)
		}
	}

	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	var dialCount atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunEdgeConn(ctx, right, func(_ context.Context, _ uint16) (net.Conn, error) {
			dialCount.Add(1)
			return nil, errors.New("should never dial")
		}, 0)
	}()

	var p22 [2]byte
	binary.BigEndian.PutUint16(p22[:], 22)
	if err := WriteFrame(left, Frame{Type: FrameOpen, StreamID: 1, Payload: p22[:]}); err != nil {
		t.Fatal(err)
	}
	reply, err := ReadFrame(left)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != FrameOpenErr || !strings.Contains(string(reply.Payload), ErrPortForbidden.Error()) {
		t.Fatalf("expected FrameOpenErr for port 22, got type=%d payload=%q", reply.Type, string(reply.Payload))
	}
	if err := WriteFrame(left, Frame{Type: FrameOpen, StreamID: 3, Payload: []byte{0x01}}); err != nil {
		t.Fatal(err)
	}
	badLenReply, err := ReadFrame(left)
	if err != nil {
		t.Fatal(err)
	}
	if badLenReply.Type != FrameOpenErr {
		t.Fatalf("expected FrameOpenErr for invalid open payload, got %d", badLenReply.Type)
	}
	if dialCount.Load() != 0 {
		t.Fatalf("edge dialed target %d times on forbidden port requests", dialCount.Load())
	}
}

func TestTunnelFailClosedOnNonLoopbackAddresses(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	for _, addr := range []string{"0.0.0.0:18800", ":18800", "127.0.0.1:0", "192.168.1.50:18800", "10.0.0.2:18800", "localhost:18800", "not-an-addr"} {
		if err := ValidateLoopbackAddr(addr); !errors.Is(err, ErrNonLoopbackAddress) {
			t.Fatalf("ValidateLoopbackAddr(%q) = %v, want ErrNonLoopbackAddress", addr, err)
		}
		if _, err := hub.StartLoopbackListener(addr); !errors.Is(err, ErrNonLoopbackAddress) {
			t.Fatalf("StartLoopbackListener(%q) = %v, want ErrNonLoopbackAddress", addr, err)
		}
		if err := RunStdioBridge(context.Background(), addr, strings.NewReader(""), io.Discard); !errors.Is(err, ErrNonLoopbackAddress) {
			t.Fatalf("RunStdioBridge(%q) = %v, want ErrNonLoopbackAddress", addr, err)
		}
	}
	for _, okAddr := range []string{"127.0.0.1:18800", "[::1]:18800"} {
		if err := ValidateLoopbackAddr(okAddr); err != nil {
			t.Fatalf("ValidateLoopbackAddr(%q) unexpected err: %v", okAddr, err)
		}
	}
}

func TestTunnelRevokeDisconnectsActiveSessionAndRejectsUnverified(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	h := NewUpgradeHandler(
		hub,
		func(*http.Request) (*x509.Certificate, error) {
			return &x509.Certificate{Subject: pkix.Name{CommonName: "gw-1"}}, nil
		},
		func(_ context.Context, _ *x509.Certificate) (string, error) {
			return "", errors.New("revoked")
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/gateway/v1/tunnel", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", ProtocolName)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when peer rejected, got %d", rec.Code)
	}

	_, activeHub, edgeErrCh, _ := startTestTunnelHarness(t, http.NewServeMux(), 5*time.Second, 5*time.Second, 50*time.Millisecond)
	activeHub.CloseGateway("0193f000-2000-7000-8000-000000000001")
	select {
	case err := <-edgeErrCh:
		if err == nil {
			t.Fatal("expected edge tunnel error after CloseGateway")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("active gateway tunnel did not close within 1s of revoke")
	}
	if _, err := activeHub.OpenStream(context.Background(), "", AllowedTargetPort); !errors.Is(err, ErrNoActiveGateway) {
		t.Fatalf("expected ErrNoActiveGateway after CloseGateway, got %v", err)
	}
}

func TestTunnelSurvivesShortHTTPServerTimeoutsViaKeepalive(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "alive-after-idle")
	})
	addr, _, _, _ := startTestTunnelHarness(
		t,
		handler,
		80*time.Millisecond,
		80*time.Millisecond,
		30*time.Millisecond,
		WithPingInterval(30*time.Millisecond),
		WithReadTimeout(220*time.Millisecond),
	)
	time.Sleep(320 * time.Millisecond)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/red/probe")
	if err != nil {
		t.Fatalf("request failed after idle past http.Server timeouts: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "alive-after-idle" {
		t.Fatalf("unexpected response body after idle: %q", string(body))
	}
}

func TestRunStdioBridgeRoundTrip(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "stdio:"+r.URL.Path)
	})
	addr, _, _, _ := startTestTunnelHarness(t, handler, 5*time.Second, 5*time.Second, 50*time.Millisecond)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- RunStdioBridge(ctx, addr, inR, outW)
		_ = outW.Close()
	}()

	go func() {
		_, _ = io.WriteString(inW, "GET /red/stdio-check HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		_ = inW.Close()
	}()

	rawResp, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawResp), "stdio:/red/stdio-check") {
		t.Fatalf("expected HTTP body from stdio bridge, got %q", string(rawResp))
	}
	if err := <-bridgeDone; err != nil {
		t.Fatalf("RunStdioBridge returned error: %v", err)
	}
}

type recordingAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (s *recordingAuditSink) RecordTunnelAudit(_ context.Context, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordingAuditSink) snapshot() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, len(s.events))
	copy(out, s.events)
	return out
}

func TestTunnelAuditSinkRecordsLifecycleStreamsAndByteCounts(t *testing.T) {
	sink := &recordingAuditSink{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tunnel-audit-ok")
	})
	addr, hub, edgeErrCh, _ := startTestTunnelHarness(
		t,
		handler,
		5*time.Second,
		5*time.Second,
		50*time.Millisecond,
		WithAuditSink(sink),
	)

	resp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://" + addr + "/red/audit-check")
	if err != nil {
		t.Fatalf("GET through tunnel failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "tunnel-audit-ok" {
		t.Fatalf("unexpected tunnel response %q", string(body))
	}

	if _, err := hub.OpenStream(context.Background(), "", 22); !errors.Is(err, ErrPortForbidden) {
		t.Fatalf("expected ErrPortForbidden for port 22, got %v", err)
	}

	hub.CloseGateway("0193f000-2000-7000-8000-000000000001")
	select {
	case <-edgeErrCh:
	case <-time.After(1 * time.Second):
		t.Fatal("expected edge loop to exit after CloseGateway")
	}

	events := sink.snapshot()
	var seenOpen, seenStreamOpen, seenStreamReject, seenClosed bool
	var closedEvent AuditEvent
	var sessionID string
	for _, ev := range events {
		switch ev.EventType {
		case AuditSessionOpened:
			seenOpen = true
			sessionID = ev.SessionID
		case AuditStreamOpened:
			seenStreamOpen = true
			if ev.StreamID == nil || *ev.StreamID <= 0 || ev.TargetPort == nil || *ev.TargetPort != AllowedTargetPort {
				t.Fatalf("unexpected STREAM_OPENED audit payload: %#v", ev)
			}
		case AuditStreamRejected:
			seenStreamReject = true
			if ev.TargetPort == nil || *ev.TargetPort != 22 {
				t.Fatalf("unexpected STREAM_REJECTED audit payload: %#v", ev)
			}
		case AuditSessionClosed:
			seenClosed = true
			closedEvent = ev
		}
	}
	if !seenOpen || !seenStreamOpen || !seenStreamReject || !seenClosed {
		t.Fatalf("missing tunnel audit events: open=%v streamOpen=%v streamReject=%v closed=%v all=%#v",
			seenOpen, seenStreamOpen, seenStreamReject, seenClosed, events)
	}
	if sessionID == "" || closedEvent.SessionID != sessionID {
		t.Fatalf("session ID mismatch between open (%q) and close (%q)", sessionID, closedEvent.SessionID)
	}
	if closedEvent.BytesTX <= 0 || closedEvent.BytesRX <= 0 {
		t.Fatalf("expected non-zero byte counts on SESSION_CLOSED, got tx=%d rx=%d", closedEvent.BytesTX, closedEvent.BytesRX)
	}
}

type blockingCloseAuditSink struct {
	release chan struct{}
}

func (s *blockingCloseAuditSink) RecordTunnelAudit(_ context.Context, event AuditEvent) error {
	if event.EventType == AuditSessionClosed {
		<-s.release
	}
	return nil
}

func TestTunnelUpgradeRecheckRejectsBeforeExposureAndAudit(t *testing.T) {
	sink := &recordingAuditSink{}
	hub := NewHub(WithAuditSink(sink))
	defer hub.Close()

	certTLS := testServerCert(t)
	leaf, err := x509.ParseCertificate(certTLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	gwID := "0193f000-2000-7000-8000-000000000011"
	existingServer, existingClient := net.Pipe()
	defer func() {
		_ = existingServer.Close()
		_ = existingClient.Close()
	}()
	existingSess := hub.registerSession(gwID, existingServer)
	if existingSess == nil {
		t.Fatal("failed to register initial session")
	}

	var calls atomic.Int32
	handler := NewUpgradeHandler(
		hub,
		func(*http.Request) (*x509.Certificate, error) { return leaf, nil },
		func(context.Context, *x509.Certificate) (string, error) {
			if calls.Add(1) == 1 {
				return gwID, nil
			}
			return "", errors.New("credential revoked during handshake")
		},
	)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	conn, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial upgrade server: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET /tunnel HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: "+ProtocolName+"\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, _ := io.ReadAll(conn)
	if !strings.Contains(string(raw), "101 Switching Protocols") {
		t.Fatalf("expected 101 response before recheck rejection, got %q", string(raw))
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 verifyPeer calls, got %d", calls.Load())
	}
	resolved, err := hub.resolveSession(gwID)
	if err != nil || resolved != existingSess {
		t.Fatalf("expected existing session to stay intact after recheck failure, resolved=%v err=%v", resolved, err)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("expected zero audit events on rejected recheck upgrade, got %#v", events)
	}
}

func TestTunnelHubCloseBoundedWhenAuditSinkBlocks(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	hub := NewHub(WithAuditSink(&blockingCloseAuditSink{release: release}))

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	sess := hub.registerSession("0193f000-2000-7000-8000-000000000012", serverConn)
	if sess == nil {
		t.Fatal("registerSession returned nil")
	}
	sess.markOpened(context.Background())

	start := time.Now()
	hub.Close()
	elapsed := time.Since(start)
	if elapsed > 1800*time.Millisecond {
		t.Fatalf("expected Hub.Close to remain bounded despite blocking audit sink, took %v", elapsed)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var buf [1]byte
	if _, err := clientConn.Read(buf[:]); err == nil {
		t.Fatal("expected Hub.Close to close session transport immediately")
	}
}

func TestTunnelTruncateAuditUTF8Safe(t *testing.T) {
	input := strings.Repeat("中", 200)
	got := truncateAuditReason(input)
	if len(got) > 512 {
		t.Fatalf("expected <= 512 bytes, got %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 after truncation")
	}
	if len(got) != 510 {
		t.Fatalf("expected rune boundary at 510 bytes (170 runes), got %d", len(got))
	}
}

func TestTunnelHubCloseGatewayByFingerprint(t *testing.T) {
	hub := NewHub()
	t.Cleanup(hub.Close)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const gwID = "0193f000-2000-7000-8000-000000000099"
	makeCert := func(serial int64) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: gwID},
			NotBefore:    time.Now().Add(-time.Minute),
			NotAfter:     time.Now().Add(time.Hour),
		}, &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: gwID},
		}, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}

	certOld := makeCert(101)
	certNew := makeCert(102)
	oldFP := peerCertificateFingerprint(certOld)
	newFP := peerCertificateFingerprint(certNew)

	s1Conn, c1Conn := net.Pipe()
	defer func() { _ = c1Conn.Close() }()
	sessOld := hub.registerSessionWithFingerprint(gwID, s1Conn, "127.0.0.1:41001", oldFP)
	if sessOld == nil {
		t.Fatal("registerSessionWithFingerprint returned nil")
	}

	// Mismatched fingerprint does not close active session.
	if hub.CloseGatewayByFingerprint(gwID, newFP) {
		t.Fatal("expected CloseGatewayByFingerprint to return false for mismatched fingerprint")
	}
	if got, err := hub.resolveSession(gwID); err != nil || got != sessOld {
		t.Fatalf("expected sessOld to remain active, got=(%v, %v)", got, err)
	}

	// Matching fingerprint closes sessOld and removes it from hub.
	if !hub.CloseGatewayByFingerprint(gwID, oldFP) {
		t.Fatal("expected CloseGatewayByFingerprint to return true for matching fingerprint")
	}
	_ = c1Conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var buf [1]byte
	if _, err := c1Conn.Read(buf[:]); err == nil {
		t.Fatal("expected c1Conn to be closed after fingerprint kick")
	}
	if _, err := hub.resolveSession(gwID); err == nil {
		t.Fatal("expected gateway session removed after fingerprint kick")
	}

	// Rotating to a new cert session protects it from a stale kick of the old fingerprint.
	s2Conn, c2Conn := net.Pipe()
	defer func() { _ = c2Conn.Close() }()
	sessNew := hub.registerSessionWithFingerprint(gwID, s2Conn, "127.0.0.1:41002", newFP)
	if hub.CloseGatewayByFingerprint(gwID, oldFP) {
		t.Fatal("expected stale oldFP kick to leave rotated sessNew connected")
	}
	if got, err := hub.resolveSession(gwID); err != nil || got != sessNew {
		t.Fatalf("expected sessNew to stay active after stale kick, got=(%v, %v)", got, err)
	}
}
