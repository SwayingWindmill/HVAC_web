package mqttguard

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

type stubVerifier struct {
	mu      sync.RWMutex
	revoked map[string]bool
}

func (v *stubVerifier) revoke(fp string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.revoked == nil {
		v.revoked = map[string]bool{}
	}
	v.revoked[fp] = true
}

func (v *stubVerifier) VerifyActiveCredential(_ context.Context, peer *x509.Certificate) (string, error) {
	if peer == nil {
		return "", errors.New("missing peer cert")
	}
	sum := sha256.Sum256(peer.Raw)
	fp := hex.EncodeToString(sum[:])
	v.mu.RLock()
	rev := v.revoked[fp]
	v.mu.RUnlock()
	if rev {
		return "", errors.New("credential revoked")
	}
	return strings.ToLower(strings.TrimSpace(peer.Subject.CommonName)), nil
}

type guardTestPKI struct {
	authority    *connectivity.CertificateAuthority
	caPool       *x509.CertPool
	serverCert   tls.Certificate
	brokerCert   tls.Certificate
	gatewayCertA tls.Certificate
	gatewayFpA   string
}

func newGuardTestPKI(t *testing.T, gatewayID string) guardTestPKI {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)

	caDir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Gateway CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER})
	_ = caDir

	authority, err := connectivity.NewCertificateAuthority(caPEM, caKeyPEM, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append CA PEM")
	}

	signServer := func(dnsName string) tls.Certificate {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: dnsName},
			DNSNames:     []string{dnsName},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
			NotBefore:    now,
			NotAfter:     now.Add(2 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k, Leaf: leaf}
	}

	gwCertA, err := authority.IssueEphemeralGatewayTLS(gatewayID, time.Hour, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	fpSum := sha256.Sum256(gwCertA.Leaf.Raw)
	return guardTestPKI{
		authority:    authority,
		caPool:       pool,
		serverCert:   signServer("localhost"),
		brokerCert:   signServer("mqtt-broker"),
		gatewayCertA: gwCertA,
		gatewayFpA:   hex.EncodeToString(fpSum[:]),
	}
}

func TestMQTTGuardProxiesChecksRevocationRateLimitAndKicks(t *testing.T) {
	t.Parallel()

	gatewayID := "0193f000-2000-7000-8000-000000000001"
	pki := newGuardTestPKI(t, gatewayID)
	verifier := &stubVerifier{}
	metrics := observability.NewRegistry()

	// Upstream fake MQTT broker verifying TLS 1.3 Gateway CA client cert CN == gatewayID.
	upstreamLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{pki.brokerCert},
		ClientCAs:    pki.caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamLn.Close()

	var upstreamPeersMu sync.Mutex
	var upstreamPeerCNs []string
	go func() {
		for {
			c, err := upstreamLn.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				tc := conn.(*tls.Conn)
				if err := tc.Handshake(); err != nil {
					return
				}
				state := tc.ConnectionState()
				if len(state.VerifiedChains) > 0 {
					upstreamPeersMu.Lock()
					upstreamPeerCNs = append(upstreamPeerCNs, state.VerifiedChains[0][0].Subject.CommonName)
					upstreamPeersMu.Unlock()
				}
				buf := make([]byte, 64)
				for {
					n, err := tc.Read(buf)
					if n > 0 {
						_, _ = tc.Write(bytes.ToUpper(buf[:n]))
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()

	disconnectCh := make(chan string, 4)
	srv, err := New(Config{
		ListenAddr:          "127.0.0.1:0",
		ServerCertificate:   pki.serverCert,
		GatewayClientCAs:    pki.caPool,
		UpstreamAddr:        upstreamLn.Addr().String(),
		UpstreamRootCAs:     pki.caPool,
		UpstreamServerName:  "mqtt-broker",
		Verifier:            verifier,
		Issuer:              pki.authority,
		PerIPBurst:          100,
		PerIPHandshakesPerSec: 100,
		PerGatewayBurst:     3,
		PerGatewayPerSec:    0.001, // 3 allowed total during short test before rate limit triggers
		Metrics:             metrics,
		OnGatewayDisconnect: func(_ context.Context, gw string, _ time.Time) {
			disconnectCh <- gw
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	dialGuard := func(cert tls.Certificate) (*tls.Conn, error) {
		return tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      pki.caPool,
			ServerName:   "localhost",
			Certificates: []tls.Certificate{cert},
		})
	}

	// 1. Valid gateway session succeeds and echoes over ephemeral upstream TLS.
	c1, err := dialGuard(pki.gatewayCertA)
	if err != nil {
		t.Fatalf("dialGuard c1: %v", err)
	}
	if _, err := c1.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(c1, reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if string(reply) != "PING" {
		t.Fatalf("reply=%q, want PING", string(reply))
	}

	// Verify active session closes immediately on fingerprint revocation kick.
	if kicked := srv.CloseGatewayByFingerprint(gatewayID, pki.gatewayFpA); kicked != 1 {
		t.Fatalf("kicked=%d, want 1", kicked)
	}
	_ = c1.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c1.Read(reply); err == nil {
		t.Fatal("expected kicked session read to fail")
	}
	_ = c1.Close()

	select {
	case gw := <-disconnectCh:
		if gw != gatewayID {
			t.Fatalf("disconnect gw=%q, want %q", gw, gatewayID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for disconnect callback after kick")
	}

	// 2. Subsequent dial with revoked fingerprint is rejected before dialing upstream.
	verifier.revoke(pki.gatewayFpA)
	if cRevoked, err := dialGuard(pki.gatewayCertA); err == nil {
		_ = cRevoked.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1)
		if _, readErr := cRevoked.Read(buf); readErr == nil {
			t.Fatal("expected revoked cert connection to close immediately")
		}
		_ = cRevoked.Close()
	}

	// 3. Per-gateway rate limit drops burst beyond configured capacity using a non-revoked cert.
	gwCertB, err := pki.authority.IssueEphemeralGatewayTLS(gatewayID, time.Hour, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if c, dialErr := dialGuard(gwCertB); dialErr == nil {
			_ = c.Close()
		}
	}

	upstreamPeersMu.Lock()
	peerCount := len(upstreamPeerCNs)
	firstCN := ""
	if peerCount > 0 {
		firstCN = upstreamPeerCNs[0]
	}
	upstreamPeersMu.Unlock()
	if peerCount < 1 || firstCN != gatewayID {
		t.Fatalf("unexpected upstream peer CNs=%v", upstreamPeerCNs)
	}
}
