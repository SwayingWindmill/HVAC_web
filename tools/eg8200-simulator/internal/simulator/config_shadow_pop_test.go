package simulator

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
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
