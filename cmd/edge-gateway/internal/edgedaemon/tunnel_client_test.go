package edgedaemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

func TestTunnelClientAllowsConfiguredPortRejectsForbiddenAndReloads(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()
	pki := newTestPKIFixture(t, dir, now)
	gwID := "0193f000-2000-7000-8000-000000000030"

	srvCert, srvKey, _, _ := pki.issueCertFiles(t, dir, "tunnel-srv", "127.0.0.1", 10, now.Add(-time.Hour), now.Add(48*time.Hour), true)
	srvPair, err := tls.LoadX509KeyPair(srvCert, srvKey)
	if err != nil {
		t.Fatal(err)
	}
	gwCert, gwKey, _, _ := pki.issueCertFiles(t, dir, "tunnel-gw", gwID, 11, now.Add(-time.Hour), now.Add(48*time.Hour), false)

	// Mock local SSH echo target
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echoLn.Close() })
	go func() {
		for {
			c, acceptErr := echoLn.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()

	var forbiddenDialed atomic.Bool
	hub := tunnel.NewHub(
		tunnel.WithAllowedTargetPorts(tunnel.AllowedTargetPort, tunnel.SSHTargetPort),
		tunnel.WithPingInterval(time.Minute),
		tunnel.WithOpenTimeout(3*time.Second),
	)
	t.Cleanup(hub.Close)

	mux := http.NewServeMux()
	mux.Handle("GET /gateway/v1/tunnel", tunnel.NewUpgradeHandler(
		hub,
		func(r *http.Request) (*x509.Certificate, error) {
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				return nil, errors.New("missing client cert")
			}
			return r.TLS.PeerCertificates[0], nil
		},
		func(_ context.Context, peer *x509.Certificate) (string, error) {
			if peer == nil || peer.Subject.CommonName != gwID {
				return "", errors.New("unauthorized gateway")
			}
			return gwID, nil
		},
	))

	pool := x509.NewCertPool()
	pool.AddCert(pki.caCert)
	srv := httptest.NewUnstartedServer(mux)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{srvPair},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"http/1.1"},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	var connCount atomic.Int32
	connectedCh := make(chan int32, 4)
	client, err := NewTunnelClient(TunnelClientOptions{
		PlatformBaseURL:    srv.URL,
		ServerName:         "127.0.0.1",
		CAFile:             pki.caFile,
		CertFile:           gwCert,
		KeyFile:            gwKey,
		AllowedTargetPorts: []uint16{tunnel.SSHTargetPort},
		PingInterval:       time.Minute,
		LocalDialTarget: func(ctx context.Context, port uint16) (net.Conn, error) {
			if port != tunnel.SSHTargetPort {
				forbiddenDialed.Store(true)
				return nil, tunnel.ErrPortForbidden
			}
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", echoLn.Addr().String())
		},
		OnConnected: func() {
			n := connCount.Add(1)
			connectedCh <- n
		},
	})
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = client.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	select {
	case <-connectedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tunnel connection")
	}

	// Open allowed SSH stream via Hub
	stream, err := hub.OpenStream(t.Context(), gwID, tunnel.SSHTargetPort)
	if err != nil {
		t.Fatalf("OpenStream(22) failed: %v", err)
	}
	if _, err := stream.Write([]byte("SSH-2.0-test\n")); err != nil {
		t.Fatal(err)
	}
	_ = stream.CloseWrite()
	echoed, err := io.ReadAll(stream)
	_ = stream.Close()
	if err != nil || string(echoed) != "SSH-2.0-test\n" {
		t.Fatalf("echoed = %q, err=%v", string(echoed), err)
	}

	// Forbidden port 1880 (omitted from client allowed ports) and 80 (hub disallowed)
	if _, err := hub.OpenStream(t.Context(), gwID, tunnel.AllowedTargetPort); !errors.Is(err, tunnel.ErrPortForbidden) && err == nil {
		t.Fatal("expected stream open for unconfigured port 1880 to fail on edge side")
	} else if err != nil && !strings.Contains(err.Error(), tunnel.ErrPortForbidden.Error()) {
		t.Fatalf("expected ErrPortForbidden message, got %v", err)
	}
	if forbiddenDialed.Load() {
		t.Fatal("forbidden target port reached local dialer")
	}

	// Reload credentials triggers clean immediate reconnect
	client.ReloadCredentials()
	select {
	case n := <-connectedCh:
		if n < 2 {
			t.Fatalf("expected reconnect count >= 2, got %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tunnel reconnect after ReloadCredentials")
	}
}
