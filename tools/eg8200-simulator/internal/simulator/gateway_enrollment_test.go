package simulator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayEnrollmentNeverSendsCodeOverPlainHTTP(t *testing.T) {
	identity := filepath.Join(t.TempDir(), "identity.pem")
	config := MQTTGatewayConfig{EnrollmentURL: "http://example.com", CertFile: identity, KeyFile: identity}
	err := EnsureGatewayCredential(context.Background(), &config, "one-time-code")
	if err == nil || !strings.Contains(err.Error(), "https:// origin") {
		t.Fatalf("insecure enrollment URL not rejected at the public boundary: %v", err)
	}
}

func TestGatewayEnrollmentRejectsStoredIdentityForAnotherGateway(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "0193f000-2000-7000-8000-000000000001"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity.pem")
	data := append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	config := MQTTGatewayConfig{GatewayID: "0193f000-2000-7000-8000-000000000002", EnrollmentURL: "https://localhost", CertFile: path, KeyFile: path}
	err = EnsureGatewayCredential(context.Background(), &config, "")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("another Gateway's stored identity accepted: %v", err)
	}
}
