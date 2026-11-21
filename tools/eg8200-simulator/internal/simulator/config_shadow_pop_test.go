package simulator

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assertValidGatewayShadowPoPHeaders(t *testing.T, r *http.Request, certPath string) {
	t.Helper()

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read gateway cert PEM %q: %v", certPath, err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("expected CERTIFICATE PEM block in %q", certPath)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse gateway cert %q: %v", certPath, err)
	}
	assertValidGatewayRequestPoPForCert(t, r, leaf)
}

func assertValidGatewayRequestPoPForCert(t *testing.T, r *http.Request, leaf *x509.Certificate) {
	t.Helper()

	tsRaw := strings.TrimSpace(r.Header.Get("X-Gateway-Cert-Pop-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Gateway-Cert-Pop-Nonce"))
	sigRaw := strings.TrimSpace(r.Header.Get("X-Gateway-Cert-Pop-Signature"))
	if tsRaw == "" || nonce == "" || sigRaw == "" {
		t.Fatalf("missing gateway PoP headers on %s %s: ts=%q nonce=%q sig=%q", r.Method, r.URL.RequestURI(), tsRaw, nonce, sigRaw)
	}
	tsSec, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		t.Fatalf("invalid PoP timestamp %q: %v", tsRaw, err)
	}
	delta := time.Since(time.Unix(tsSec, 0).UTC())
	if delta < -300*time.Second || delta > 300*time.Second {
		t.Fatalf("PoP timestamp skew out of range: %v", delta)
	}
	if len(nonce) < 16 {
		t.Fatalf("PoP nonce too short: %q", nonce)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigRaw)
	if err != nil {
		t.Fatalf("decode PoP signature: %v", err)
	}
	fp := sha256.Sum256(leaf.Raw)
	uri := r.URL.RequestURI()
	if uri == "" {
		uri = "/"
	}
	canonical := "HVAC-GW-POP-V1\n" +
		strings.ToUpper(strings.TrimSpace(r.Method)) + "\n" +
		uri + "\n" +
		tsRaw + "\n" +
		nonce + "\n" +
		hex.EncodeToString(fp[:])
	digest := sha256.Sum256([]byte(canonical))

	switch pub := leaf.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(pub, digest[:], sigBytes) {
			t.Fatalf("ECDSA PoP verification failed for %s %s", r.Method, uri)
		}
	case *rsa.PublicKey:
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sigBytes) != nil &&
			rsa.VerifyPSS(pub, crypto.SHA256, digest[:], sigBytes, nil) != nil {
			t.Fatalf("RSA PoP verification failed for %s %s", r.Method, uri)
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(pub, []byte(canonical), sigBytes) {
			t.Fatalf("Ed25519 PoP verification failed for %s %s", r.Method, uri)
		}
	default:
		t.Fatalf("unsupported leaf public key type %T", leaf.PublicKey)
	}
}

func TestConfigShadowPoPSignsAllSupportedAlgorithms(t *testing.T) {
	dir := t.TempDir()
	_, certPath, keyPath, _ := writeTestMTLSBundle(t, dir)

	certDER, signer, ok := loadShadowSigningIdentity(MQTTGatewayConfig{
		CertFile: certPath,
		KeyFile:  keyPath,
	})
	if !ok {
		t.Fatal("loadShadowSigningIdentity returned ok=false")
	}
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}

	now := time.Now().UTC()
	reqGet := httptest.NewRequest(http.MethodGet, "https://localhost/gateway/v1/config-shadow?sinceRevision=7", nil)
	if err := signShadowRequestPoP(reqGet, certDER, signer, now); err != nil {
		t.Fatalf("signShadowRequestPoP GET failed: %v", err)
	}
	assertValidGatewayRequestPoPForCert(t, reqGet, leaf)

	reqPost := httptest.NewRequest(http.MethodPost, "https://localhost/gateway/v1/config-shadow/report", bytes.NewReader([]byte(`{}`)))
	if err := signShadowRequestPoP(reqPost, certDER, signer, now); err != nil {
		t.Fatalf("signShadowRequestPoP POST failed: %v", err)
	}
	assertValidGatewayRequestPoPForCert(t, reqPost, leaf)
}

func TestEnsureGatewayCredentialSignsRenewPoP(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-enroll-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(180 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	const gwID = "0193f000-2000-7000-8000-000000000777"
	oldKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: gwID},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	oldDER, err := x509.CreateCertificate(rand.Reader, oldTmpl, caCert, &oldKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	oldLeaf, err := x509.ParseCertificate(oldDER)
	if err != nil {
		t.Fatal(err)
	}
	oldKeyDER, err := x509.MarshalECPrivateKey(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "gateway.pem")
	bundleBytes := append(
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: oldKeyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: oldDER})...,
	)
	if err := os.WriteFile(bundlePath, bundleBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srvKeyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	srvCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: srvKeyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}

	var verifiedRenewPoP bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gateway/v1/renew" {
			t.Fatalf("unexpected renewal request: %s %s", r.Method, r.URL.Path)
		}
		assertValidGatewayRequestPoPForCert(t, r, oldLeaf)
		verifiedRenewPoP = true
		var reqBody struct {
			CSRPEM string `json:"csrPem"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode([]byte(reqBody.CSRPEM))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		newTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(4),
			Subject:      csr.Subject,
			NotBefore:    now.Add(-time.Minute),
			NotAfter:     now.Add(90 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		newDER, err := x509.CreateCertificate(rand.Reader, newTmpl, caCert, csr.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"certificatePem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: newDER})),
			"caPem":          string(caPEM),
			"brokerUrl":      "tls://127.0.0.1:8883",
			"expiresAt":      newTmpl.NotAfter,
		})
	}))
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{srvCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	srv.StartTLS()
	defer srv.Close()

	cfg := MQTTGatewayConfig{
		SchemaVersion:        MQTTGatewayConfigSchemaVersion,
		GatewayID:            gwID,
		BrokerURL:            "tls://127.0.0.1:8883",
		EnrollmentURL:        srv.URL,
		EnrollmentCAFile:     caPath,
		EnrollmentServerName: "127.0.0.1",
		CAFile:               caPath,
		CertFile:             bundlePath,
		KeyFile:              bundlePath,
		ServerName:           "127.0.0.1",
		QueueDirectory:       filepath.Join(dir, "queue"),
		MaximumQueueBytes:    4 << 20,
	}
	if err := EnsureGatewayCredential(context.Background(), &cfg, ""); err != nil {
		t.Fatalf("EnsureGatewayCredential renewal failed: %v", err)
	}
	if !verifiedRenewPoP {
		t.Fatal("expected /gateway/v1/renew request to include verifiable PoP headers")
	}
}
