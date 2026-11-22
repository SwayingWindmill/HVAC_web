package edgedaemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testPKIFixture struct {
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caPEM  []byte
	caFile string
}

func newTestPKIFixture(t *testing.T, dir string, now time.Time) *testPKIFixture {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "HVAC Test CA"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caFile := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caFile, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return &testPKIFixture{caCert: caCert, caKey: caKey, caPEM: caPEM, caFile: caFile}
}

func (f *testPKIFixture) issueCertFiles(t *testing.T, dir, prefix, cn string, serial int64, notBefore, notAfter time.Time, isServer bool) (certFile, keyFile string, certPEM []byte, priv *ecdsa.PrivateKey) {
	t.Helper()
	var err error
	priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ext := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if isServer {
		ext = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  ext,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, f.caCert, &priv.PublicKey, f.caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certFile = filepath.Join(dir, prefix+".crt")
	keyFile = filepath.Join(dir, prefix+".key")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, certPEM, priv
}

func TestCheckAndRenewCertificate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	dir := t.TempDir()
	pki := newTestPKIFixture(t, dir, now)
	gwID := "0193f000-2000-7000-8000-000000000020"

	srvCertFile, srvKeyFile, _, _ := pki.issueCertFiles(t, dir, "server", "127.0.0.1", 2, now.Add(-time.Hour), now.Add(48*time.Hour), true)
	srvPair, err := tls.LoadX509KeyPair(srvCertFile, srvKeyFile)
	if err != nil {
		t.Fatal(err)
	}

	gwCertFile, gwKeyFile, _, _ := pki.issueCertFiles(t, dir, "gateway", gwID, 3, now.Add(-24*time.Hour), now.Add(60*24*time.Hour), false)

	// Case 1: valid beyond 14 days -> skipped without hitting network
	res, err := CheckAndRenewCertificate(t.Context(), CertRenewOptions{
		PlatformBaseURL: "https://127.0.0.1:1",
		GatewayID:       gwID,
		CertFile:        gwCertFile,
		KeyFile:         gwKeyFile,
		CAFile:          pki.caFile,
		ThresholdDays:   14,
		Now:             func() time.Time { return now },
	})
	if err != nil || res.Renewed || res.Reason != "valid_beyond_threshold" {
		t.Fatalf("expected valid cert skip, got res=%+v err=%v", res, err)
	}

	// Re-issue gateway cert expiring in 10 days
	gwCertFile2, gwKeyFile2, origPEM, gwKey2 := pki.issueCertFiles(t, dir, "gw2", gwID, 4, now.Add(-24*time.Hour), now.Add(10*24*time.Hour), false)

	// Prepare next renewed cert signed by same CA for gwKey2
	nextTMPL := &x509.Certificate{
		SerialNumber: big.NewInt(5),
		Subject:      pkix.Name{CommonName: gwID},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	nextDER, err := x509.CreateCertificate(rand.Reader, nextTMPL, pki.caCert, &gwKey2.PublicKey, pki.caKey)
	if err != nil {
		t.Fatal(err)
	}
	nextPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: nextDER}))

	returnBadCN := false
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/v1/renew" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if returnBadCN {
			badTMPL := *nextTMPL
			badTMPL.Subject = pkix.Name{CommonName: "wrong-gateway"}
			badDER, _ := x509.CreateCertificate(rand.Reader, &badTMPL, pki.caCert, &gwKey2.PublicKey, pki.caKey)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"certificatePem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: badDER})),
				"caPem":          string(pki.caPEM),
				"expiresAt":      badTMPL.NotAfter.Format(time.RFC3339),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"certificatePem": nextPEM,
			"caPem":          string(pki.caPEM),
			"expiresAt":      nextTMPL.NotAfter.Format(time.RFC3339),
		})
	}))
	pool := x509.NewCertPool()
	pool.AddCert(pki.caCert)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{srvPair},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	// Case 2: mismatched CN rejected and disk cert unchanged
	returnBadCN = true
	if _, err := CheckAndRenewCertificate(t.Context(), CertRenewOptions{
		PlatformBaseURL: srv.URL,
		GatewayID:       gwID,
		CertFile:        gwCertFile2,
		KeyFile:         gwKeyFile2,
		CAFile:          pki.caFile,
		ThresholdDays:   14,
		Now:             func() time.Time { return now },
	}); err == nil {
		t.Fatal("expected mismatched CN in renewed cert to fail")
	}
	diskNow, _ := os.ReadFile(gwCertFile2)
	if string(diskNow) != string(origPEM) {
		t.Fatal("disk cert should remain unchanged after rejected renew")
	}

	// Case 3: valid renew succeeds and triggers callback
	returnBadCN = false
	callbackFired := false
	res, err = CheckAndRenewCertificate(t.Context(), CertRenewOptions{
		PlatformBaseURL: srv.URL,
		GatewayID:       gwID,
		CertFile:        gwCertFile2,
		KeyFile:         gwKeyFile2,
		CAFile:          pki.caFile,
		ThresholdDays:   14,
		Now:             func() time.Time { return now },
		OnRenewed:       func(CertRenewResult) { callbackFired = true },
	})
	if err != nil || !res.Renewed || !callbackFired {
		t.Fatalf("expected successful renew with callback, got res=%+v err=%v callback=%v", res, err, callbackFired)
	}
	diskUpdated, _ := os.ReadFile(gwCertFile2)
	if string(diskUpdated) != nextPEM {
		t.Fatal("updated cert PEM was not atomically written")
	}
}
