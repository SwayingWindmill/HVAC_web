package workloadtls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCertificateFilesReloadsRotatedKeypair(t *testing.T) {
	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "tls.crt")
	privateKeyPath := filepath.Join(directory, "tls.key")
	writeTestKeypair(t, certificatePath, privateKeyPath, 1)

	files := CertificateFiles{CertificatePath: certificatePath, PrivateKeyPath: privateKeyPath}
	first, err := files.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	firstLeaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	writeTestKeypair(t, certificatePath, privateKeyPath, 2)
	second, err := files.GetClientCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	secondLeaf, err := x509.ParseCertificate(second.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if firstLeaf.SerialNumber.Cmp(secondLeaf.SerialNumber) == 0 || secondLeaf.SerialNumber.Int64() != 2 {
		t.Fatalf("certificate rotation was not observed: first=%s second=%s", firstLeaf.SerialNumber, secondLeaf.SerialNumber)
	}
}

func TestNewServerTLSConfigRequiresInitialIdentityAndClientCA(t *testing.T) {
	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "tls.crt")
	privateKeyPath := filepath.Join(directory, "tls.key")
	writeTestKeypair(t, certificatePath, privateKeyPath, 3)

	config, err := NewServerTLSConfig(ServerConfig{
		CertificateFiles: CertificateFiles{CertificatePath: certificatePath, PrivateKeyPath: privateKeyPath},
		ClientCAPath:     certificatePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.GetCertificate == nil || config.ClientCAs == nil {
		t.Fatal("server TLS configuration is missing dynamic identity or client trust")
	}
}

type transportStub struct {
	roundTrips int
	closed     bool
}

func (stub *transportStub) RoundTrip(request *http.Request) (*http.Response, error) {
	stub.roundTrips++
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    request,
	}, nil
}

func (stub *transportStub) CloseIdleConnections() {
	stub.closed = true
}

func TestRotatingTransportReplacesLongLivedConnections(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	first := &transportStub{}
	second := &transportStub{}
	factoryCalls := 0
	transport := &rotatingTransport{
		current: first,
		factory: func() (idleClosingRoundTripper, error) {
			factoryCalls++
			return second, nil
		},
		now:       func() time.Time { return now },
		expiresAt: now.Add(time.Minute),
		lifetime:  time.Minute,
	}
	request, err := http.NewRequest(http.MethodGet, "https://runtime.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if first.roundTrips != 1 || factoryCalls != 0 || first.closed {
		t.Fatalf("unexpected initial transport state: first=%#v factoryCalls=%d", first, factoryCalls)
	}

	now = now.Add(2 * time.Minute)
	response, err = transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if factoryCalls != 1 || second.roundTrips != 1 || !first.closed {
		t.Fatalf("transport was not rotated: first=%#v second=%#v factoryCalls=%d", first, second, factoryCalls)
	}
}

func writeTestKeypair(t *testing.T, certificatePath, privateKeyPath string, serial int64) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "workload-test"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "workload-test"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	if err := os.WriteFile(certificatePath, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}
