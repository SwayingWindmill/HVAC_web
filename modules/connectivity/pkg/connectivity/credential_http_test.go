package connectivity

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
