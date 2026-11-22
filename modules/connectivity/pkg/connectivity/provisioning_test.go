package connectivity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestParseJITPCSRValidation(t *testing.T) {
	devID := "0194f800-3000-7000-8000-000000000001"
	serial := "SN-HVAC-0001"

	serialCSR := credentialTestCSR(t, serial)
	if _, err := parseJITPCSR(serialCSR, serial, devID); err != nil {
		t.Fatalf("CSR with hardwareSerial CN rejected: %v", err)
	}
	idCSR := credentialTestCSR(t, strings.ToUpper(devID))
	if _, err := parseJITPCSR(idCSR, serial, devID); err != nil {
		t.Fatalf("CSR with deviceId CN rejected: %v", err)
	}
	wrongCSR := credentialTestCSR(t, "SN-OTHER-9999")
	if _, err := parseJITPCSR(wrongCSR, serial, devID); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("mismatched CSR CN err=%v, want ErrEnrollmentRejected", err)
	}

	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	weakDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serial}}, weakKey)
	if err != nil {
		t.Fatal(err)
	}
	weakPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: weakDER}))
	if _, err := parseJITPCSR(weakPEM, serial, devID); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("weak RSA-1024 CSR err=%v, want ErrEnrollmentRejected", err)
	}

	p224Key, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p224DER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serial}}, p224Key)
	if err != nil {
		t.Fatal(err)
	}
	p224PEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: p224DER}))
	if _, err := parseJITPCSR(p224PEM, serial, devID); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("unsupported P-224 CSR err=%v, want ErrEnrollmentRejected", err)
	}
}

func TestProvisioningHTTPAuthAndValidation(t *testing.T) {
	svc := NewCredentialService(&Store{}, &CertificateAuthority{})
	claims := registryauth.GrantClaims{
		TenantID:       "0194f800-0000-7000-8000-000000000001",
		PrincipalID:    "0194f800-1000-7000-8000-000000000001",
		AllowedSiteIDs: []string{"0194f800-2000-7000-8000-000000000001"},
	}
	handler := NewCredentialHandler(svc, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return claims, nil
	})
	spiffeGateway, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	tlsState := &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{spiffeGateway}}}}}

	noTLS := httptest.NewRequest(http.MethodPost, "/gateway/v1/jitp/enroll", strings.NewReader(`{}`))
	noTLSRec := httptest.NewRecorder()
	handler.ServeHTTP(noTLSRec, noTLS)
	if noTLSRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing SPIFFE TLS code=%d, want 401", noTLSRec.Code)
	}

	forged := httptest.NewRequest(http.MethodGet, "/internal/v1/provisioning/batches", nil)
	forged.TLS = tlsState
	forged.Header.Set("X-Gateway-Client-Cert", "spoof")
	forgedRec := httptest.NewRecorder()
	handler.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusForbidden {
		t.Fatalf("forged X-Gateway-Client-Cert code=%d, want 403", forgedRec.Code)
	}

	badJITP := httptest.NewRequest(http.MethodPost, "/gateway/v1/jitp/enroll", bytes.NewBufferString(`{"unknown":true}`))
	badJITP.TLS = tlsState
	badJITPRec := httptest.NewRecorder()
	handler.ServeHTTP(badJITPRec, badJITP)
	if badJITPRec.Code != http.StatusBadRequest || badJITPRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad JITP payload code=%d cache=%q", badJITPRec.Code, badJITPRec.Header().Get("Cache-Control"))
	}

	badBatch := httptest.NewRequest(http.MethodGet, "/internal/v1/provisioning/batches/not-a-uuid", nil)
	badBatch.TLS = tlsState
	badBatchRec := httptest.NewRecorder()
	handler.ServeHTTP(badBatchRec, badBatch)
	var errBody map[string]string
	_ = json.NewDecoder(badBatchRec.Body).Decode(&errBody)
	if badBatchRec.Code != http.StatusBadRequest || errBody["code"] != "PROVISIONING_INVALID" {
		t.Fatalf("bad batch id code=%d body=%#v", badBatchRec.Code, errBody)
	}
}
