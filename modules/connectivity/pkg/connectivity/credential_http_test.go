package connectivity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestCredentialHTTPRejectsUnverifiedForwardedGatewayIdentity(t *testing.T) {
	handler := NewCredentialHandler(nil, nil)
	uri, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	peer := &x509.Certificate{URIs: []*url.URL{uri}}
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/renew", nil)
	request.Header.Set("X-Gateway-Client-Cert", "forged")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unverified workload accepted: %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/revoke", nil)
	request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{peer}}}
	request.Header.Set("X-Gateway-Client-Cert", "forged")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("Gateway-supplied internal identity accepted: %d", recorder.Code)
	}
}

func TestCredentialHandlerWithTunnelRoutingAndPeerCertParsing(t *testing.T) {
	called := false
	handler := NewCredentialHandlerWithTunnel(nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/gateway/v1/tunnel" {
			t.Fatalf("unexpected tunnel path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	uri, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	workloadPeer := &x509.Certificate{URIs: []*url.URL{uri}}

	unverifiedReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/tunnel", nil)
	unverifiedReq.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{workloadPeer}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unverifiedReq)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("expected 401 before tunnel handler for unverified workload, got status=%d called=%v", rec.Code, called)
	}

	verifiedReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/tunnel", nil)
	verifiedReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{workloadPeer}}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, verifiedReq)
	if rec.Code != http.StatusSwitchingProtocols || !called {
		t.Fatalf("expected verified workload to reach tunnel handler (101), got status=%d called=%v", rec.Code, called)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "0193f000-2000-7000-8000-000000000001"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}, &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "0193f000-2000-7000-8000-000000000001"},
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	reqWithCert := httptest.NewRequest(http.MethodGet, "/gateway/v1/tunnel", nil)
	reqWithCert.Header.Set("X-Gateway-Client-Cert", url.QueryEscape(string(pemBytes)))
	parsed, err := ParseGatewayPeerCert(reqWithCert)
	if err != nil || parsed.Subject.CommonName != "0193f000-2000-7000-8000-000000000001" {
		t.Fatalf("ParseGatewayPeerCert failed: parsed=%v err=%v", parsed, err)
	}
	if _, err := ParseGatewayPeerCert(httptest.NewRequest(http.MethodGet, "/gateway/v1/tunnel", nil)); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected ErrEnrollmentRejected on missing peer header, got %v", err)
	}

	var svc CredentialService
	svc.SetRevokeHook(func(string) {})
	if _, err := svc.VerifyActiveCredential(context.Background(), nil); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected ErrEnrollmentRejected for nil cert, got %v", err)
	}
}

func TestCredentialHTTPRetireRouteAuthorizationAndValidation(t *testing.T) {
	concHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := NewCredentialHandlerWithExtensions(&CredentialService{}, nil, nil, concHandler)

	coreURI, _ := url.Parse("spiffe://hvac.local/platform-core-service")
	corePeer := &x509.Certificate{URIs: []*url.URL{coreURI}}
	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}

	// platform-core-service must NOT access non-retire routes such as /gateway/v1/renew or operator /revoke.
	for _, target := range []string{"/gateway/v1/renew", "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/revoke"} {
		req := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(`{}`))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("core-service unexpectedly authorized for %s: status=%d", target, rec.Code)
		}
	}

	retirePath := "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001:retire"

	// Forged X-Gateway-Client-Cert on :retire must be rejected.
	forgedReq := httptest.NewRequest(http.MethodPost, retirePath, bytes.NewBufferString(`{"tenantId":"0193f000-1000-7000-8000-000000000001"}`))
	forgedReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	forgedReq.Header.Set("X-Gateway-Client-Cert", "forged")
	forgedRec := httptest.NewRecorder()
	handler.ServeHTTP(forgedRec, forgedReq)
	if forgedRec.Code != http.StatusForbidden {
		t.Fatalf("forged X-Gateway-Client-Cert accepted on :retire: %d", forgedRec.Code)
	}

	// platform-core-service passes SPIFFE check on :retire and reaches service validation (400 on invalid tenantId).
	badReq := httptest.NewRequest(http.MethodPost, retirePath, bytes.NewBufferString(`{"tenantId":"not-a-uuid"}`))
	badReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 ENROLLMENT_REJECTED for malformed tenantId, got %d (%s)", badRec.Code, badRec.Body.String())
	}

	// platform-gateway remains authorized for concentrator routes alongside :retire.
	concReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/concentrators/units", nil)
	concReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	concRec := httptest.NewRecorder()
	handler.ServeHTTP(concRec, concReq)
	if concRec.Code != http.StatusOK {
		t.Fatalf("expected concentrator route 200, got %d", concRec.Code)
	}
}

func TestCertificateAuthorityCreateCRLAndSerialHex(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	// New Gateway CA created by InitializeGatewayCA includes KeyUsageCRLSign.
	caDir := t.TempDir()
	if err := InitializeGatewayCA(caDir, []byte(""), now); err != nil {
		t.Fatalf("InitializeGatewayCA: %v", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(caDir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewCertificateAuthority(caPEM, keyPEM, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatalf("NewCertificateAuthority: %v", err)
	}

	gatewayID := "0193f000-2000-7000-8000-000000000001"
	csrKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: gatewayID}}, csrKey)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := parseGatewayCSR(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})))
	if err != nil {
		t.Fatal(err)
	}

	issued, cert, err := authority.issue(gatewayID, csr, now)
	if err != nil {
		t.Fatalf("authority.issue: %v", err)
	}
	wantSerial := strings.ToLower(cert.SerialNumber.Text(16))
	if issued.SerialNumber != wantSerial || issued.SerialNumber == "" {
		t.Fatalf("issued.SerialNumber=%q, want %q", issued.SerialNumber, wantSerial)
	}

	crlPEM, err := authority.CreateCRL([]pkix.RevokedCertificate{{
		SerialNumber:   cert.SerialNumber,
		RevocationTime: now,
	}}, now)
	if err != nil {
		t.Fatalf("CreateCRL: %v", err)
	}
	block, rest := pem.Decode(crlPEM)
	if block == nil || block.Type != "X509 CRL" || len(rest) != 0 {
		t.Fatalf("unexpected CRL PEM block=%v restLen=%d", block, len(rest))
	}
	crl, err := x509.ParseRevocationList(block.Bytes)
	if err != nil {
		t.Fatalf("x509.ParseRevocationList: %v", err)
	}
	if err := crl.CheckSignatureFrom(authority.certificate); err != nil {
		t.Fatalf("CRL signature check failed: %v", err)
	}
	if len(crl.RevokedCertificateEntries) != 1 || crl.RevokedCertificateEntries[0].SerialNumber.Cmp(cert.SerialNumber) != 0 {
		t.Fatalf("unexpected CRL entries: %+v", crl.RevokedCertificateEntries)
	}

	// Legacy CA without KeyUsageCRLSign fails explicitly with ErrCRLSignUnsupported.
	legacyKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	legacyTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Legacy CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	legacyDER, err := x509.CreateCertificate(rand.Reader, legacyTpl, legacyTpl, &legacyKey.PublicKey, legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	legacyPriv, err := x509.MarshalPKCS8PrivateKey(legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	legacyAuth, err := NewCertificateAuthority(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: legacyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: legacyPriv}),
		"tls://mqtt-broker:8883",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyAuth.CreateCRL(nil, now); !errors.Is(err, ErrCRLSignUnsupported) {
		t.Fatalf("expected ErrCRLSignUnsupported for legacy CA, got %v", err)
	}
}

func TestCredentialHTTPNewRoutesAndValidation(t *testing.T) {
	handler := NewCredentialHandler(&CredentialService{}, nil)
	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}
	coreURI, _ := url.Parse("spiffe://hvac.local/platform-core-service")
	corePeer := &x509.Certificate{URIs: []*url.URL{coreURI}}

	// Unverified workload rejected on ca.crl.
	unverifiedCRL := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/ca.crl", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unverifiedCRL)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unverified ca.crl status=%d, want 401", rec.Code)
	}

	// platform-core-service is not authorized for /internal/v1/gateways/{id}/rotate or ca.crl.
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/internal/v1/gateways/ca.crl"},
		{http.MethodGet, "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/credentials"},
		{http.MethodPost, "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/rotate"},
		{http.MethodPost, "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/credentials/0193f000-3000-7000-8000-000000000001/revoke"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("core-service authorized on %s %s: %d", tc.method, tc.path, r.Code)
		}
	}

	// Forged X-Gateway-Client-Cert on ca.crl is rejected.
	forgedCRL := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/ca.crl", nil)
	forgedCRL.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	forgedCRL.Header.Set("X-Gateway-Client-Cert", "forged")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, forgedCRL)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged X-Gateway-Client-Cert on ca.crl status=%d, want 403", rec.Code)
	}

	// /gateway/v1/renew parses revokePeerCertificate field strictly and reaches peer cert check cleanly.
	renewReq := httptest.NewRequest(http.MethodPost, "/gateway/v1/renew", bytes.NewBufferString(`{"csrPem":"invalid","revokePeerCertificate":true}`))
	renewReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, renewReq)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("renew with revokePeerCertificate missing peer cert returned %d, want 400", rec.Code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp["code"] != "ENROLLMENT_REJECTED" {
		t.Fatalf("unexpected renew error body: %s (%v)", rec.Body.String(), err)
	}

	// Blank reason on certificate revoke violates minLength=1 OpenAPI constraint and is rejected before DB.
	authHandler := NewCredentialHandler(&CredentialService{}, func(*http.Request, registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: "0193f000-0000-7000-8000-000000000001", PrincipalID: "0193f000-1000-7000-8000-000000000001"}, nil
	})
	blankReasonReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/0193f000-2000-7000-8000-000000000001/credentials/0193f000-3000-7000-8000-000000000001/revoke", bytes.NewBufferString(`{"reason":"   "}`))
	blankReasonReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	blankRec := httptest.NewRecorder()
	authHandler.ServeHTTP(blankRec, blankReasonReq)
	if blankRec.Code != http.StatusBadRequest {
		t.Fatalf("blank revoke reason status=%d, want 400", blankRec.Code)
	}
}

func TestCertificateAuthorityIssueEphemeralGatewayTLS(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	caDir := t.TempDir()
	if err := InitializeGatewayCA(caDir, []byte(""), now); err != nil {
		t.Fatalf("InitializeGatewayCA: %v", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(caDir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewCertificateAuthority(caPEM, keyPEM, "tls://mqtt-broker:8883")
	if err != nil {
		t.Fatalf("NewCertificateAuthority: %v", err)
	}

	if _, err := authority.IssueEphemeralGatewayTLS("not-a-uuid", 5*time.Minute, now); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("expected ErrEnrollmentRejected for invalid gateway ID, got %v", err)
	}

	gatewayID := "0193f000-2000-7000-8000-000000000001"
	tlsCert, err := authority.IssueEphemeralGatewayTLS(gatewayID, 10*time.Minute, now)
	if err != nil {
		t.Fatalf("IssueEphemeralGatewayTLS: %v", err)
	}
	if tlsCert.Leaf == nil || tlsCert.Leaf.Subject.CommonName != gatewayID {
		t.Fatalf("unexpected leaf cert: %+v", tlsCert.Leaf)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to parse gateway CA")
	}
	if _, err := tlsCert.Leaf.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: now.Add(5 * time.Minute),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("ephemeral cert failed verification against Gateway CA: %v", err)
	}

	// Clamps requested TTL to CA NotAfter when CA expires sooner.
	farTTL := 100 * 365 * 24 * time.Hour
	clamped, err := authority.IssueEphemeralGatewayTLS(gatewayID, farTTL, now)
	if err != nil {
		t.Fatalf("IssueEphemeralGatewayTLS clamped: %v", err)
	}
	if clamped.Leaf.NotAfter.After(authority.certificate.NotAfter) {
		t.Fatalf("clamped NotAfter=%v exceeds CA NotAfter=%v", clamped.Leaf.NotAfter, authority.certificate.NotAfter)
	}
}
