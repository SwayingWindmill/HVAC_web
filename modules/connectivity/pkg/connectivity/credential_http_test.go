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
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
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
