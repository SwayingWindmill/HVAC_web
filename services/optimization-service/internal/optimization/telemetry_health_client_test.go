package optimization

import (
	"context"
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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type capturedHealthHTTPRequest struct {
	Path        string
	Traceparent string
	PeerSPIFFE  string
	Body        optimizationPointHealthRequest
}

type stubHealthHTTPHandler struct {
	mu       sync.Mutex
	status   int
	response optimizationPointHealthResponse
	rawBody  string
	requests []capturedHealthHTTPRequest
}

func (stub *stubHealthHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	var body optimizationPointHealthRequest
	_ = json.NewDecoder(request.Body).Decode(&body)
	var peerSPIFFE string
	if request.TLS != nil && len(request.TLS.PeerCertificates) > 0 && len(request.TLS.PeerCertificates[0].URIs) > 0 {
		peerSPIFFE = request.TLS.PeerCertificates[0].URIs[0].String()
	}

	stub.mu.Lock()
	stub.requests = append(stub.requests, capturedHealthHTTPRequest{
		Path:        request.URL.Path,
		Traceparent: request.Header.Get("traceparent"),
		PeerSPIFFE:  peerSPIFFE,
		Body:        body,
	})
	status := stub.status
	if status == 0 {
		status = http.StatusOK
	}
	response := stub.response
	rawBody := stub.rawBody
	stub.mu.Unlock()

	if rawBody != "" {
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(rawBody))
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}

func (stub *stubHealthHTTPHandler) setResponse(status int, response optimizationPointHealthResponse, rawBody string) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.status = status
	stub.response = response
	stub.rawBody = rawBody
}

func (stub *stubHealthHTTPHandler) snapshot() []capturedHealthHTTPRequest {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	out := make([]capturedHealthHTTPRequest, len(stub.requests))
	copy(out, stub.requests)
	return out
}

func TestWorkloadTelemetryHealthClientMTLSAndTracing(t *testing.T) {
	pkiDir := t.TempDir()
	caPath, caCert, caKey := writeTestCA(t, pkiDir)
	serverCert := writeTestLeafCert(t, pkiDir, "server", caCert, caKey, []string{"telemetry-runtime-service"}, "spiffe://hvac.local/telemetry-runtime-service", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	clientCertPath, clientKeyPath := writeTestClientCertFiles(t, pkiDir, "opt", caCert, caKey, "spiffe://hvac.local/optimization-service")

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	stub := &stubHealthHTTPHandler{
		status:   http.StatusOK,
		response: optimizationPointHealthResponse{Healthy: true},
	}
	server := httptest.NewUnstartedServer(stub)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	server.StartTLS()
	defer server.Close()

	client, err := NewWorkloadTelemetryHealthClient(TelemetryHealthClientConfig{
		CertificatePath: clientCertPath,
		PrivateKeyPath:  clientKeyPath,
		CAPath:          caPath,
		TelemetryURL:    server.URL,
		ServerName:      "telemetry-runtime-service",
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"
	deviceID := "01990000-6000-7000-8000-000000000001"

	wantTraceparent := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	ctx := observability.ContextWithRemoteParent(context.Background(), observability.NewTracer("optimization-service", nil), wantTraceparent, "")
	if err := client.CheckTelemetryPointHealth(ctx, tenantID, siteID, []string{deviceID, "not-a-uuid", deviceID}, []string{" hdr.sup_t ", "zone.temp", "hdr.sup_t"}); err != nil {
		t.Fatalf("expected healthy response, got %v", err)
	}

	captured := stub.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 request, got %d", len(captured))
	}
	if captured[0].Path != InternalOptimizationPointHealthPath {
		t.Fatalf("path=%q want %q", captured[0].Path, InternalOptimizationPointHealthPath)
	}
	if captured[0].PeerSPIFFE != "spiffe://hvac.local/optimization-service" {
		t.Fatalf("peerSPIFFE=%q", captured[0].PeerSPIFFE)
	}
	if captured[0].Traceparent != wantTraceparent {
		t.Fatalf("traceparent=%q", captured[0].Traceparent)
	}
	if captured[0].Body.TenantID != tenantID || captured[0].Body.SiteID != siteID ||
		!slices.Equal(captured[0].Body.DeviceIDs, []string{deviceID}) ||
		!slices.Equal(captured[0].Body.TelemetryKeys, []string{"hdr.sup_t", "zone.temp"}) {
		t.Fatalf("unexpected request body: %+v", captured[0].Body)
	}

	stub.setResponse(http.StatusOK, optimizationPointHealthResponse{
		Healthy:      false,
		ReasonCode:   "PROBE_OFFSET_ALARM",
		TelemetryKey: "hdr.sup_t",
		BlockReason:  "DRIFT",
		Detail:       "active probe offset alarm (DRIFT) on point hdr.sup_t suppresses optimization",
	}, "")
	err = client.CheckTelemetryPointHealth(ctx, tenantID, siteID, []string{deviceID}, []string{"hdr.sup_t"})
	if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonInputsInvalid || !strings.Contains(err.Error(), "active probe offset alarm (DRIFT) on point hdr.sup_t suppresses optimization") {
		t.Fatalf("unexpected unhealthy error: ok=%v reason=%s err=%v", ok, reason, err)
	}

	stub.setResponse(http.StatusServiceUnavailable, optimizationPointHealthResponse{}, `{"code":"TELEMETRY_OPTIMIZATION_HEALTH_UNAVAILABLE"}`)
	err = client.CheckTelemetryPointHealth(ctx, tenantID, siteID, []string{deviceID}, []string{"hdr.sup_t"})
	if err == nil {
		t.Fatal("expected HTTP 503 to fail")
	}
}

func TestWorkloadTelemetryHealthClientValidationAndPreparerIntegration(t *testing.T) {
	if _, err := NewWorkloadTelemetryHealthClient(TelemetryHealthClientConfig{}); err == nil {
		t.Fatal("expected empty TelemetryURL error")
	}
	if _, err := NewWorkloadTelemetryHealthClient(TelemetryHealthClientConfig{TelemetryURL: "://bad-url"}); err == nil {
		t.Fatal("expected invalid TelemetryURL error")
	}

	stub := &stubHealthHTTPHandler{
		status:   http.StatusOK,
		response: optimizationPointHealthResponse{Healthy: true},
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client, err := NewWorkloadTelemetryHealthClient(TelemetryHealthClientConfig{
		TelemetryURL: srv.URL,
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"
	if err := client.CheckTelemetryPointHealth(t.Context(), tenantID, siteID, nil, nil); err != nil {
		t.Fatalf("empty telemetry keys should skip remote call cleanly, got %v", err)
	}
	if len(stub.snapshot()) != 0 {
		t.Fatalf("empty telemetry keys must not send HTTP request: %+v", stub.snapshot())
	}
	if err := client.CheckTelemetryPointHealth(t.Context(), "invalid", siteID, nil, []string{"k"}); err == nil {
		t.Fatal("expected invalid tenantId error")
	}

	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	def := testPreparationDefinition()
	def.DeviceIDs = []string{"01990000-6000-7000-8000-000000000001"}
	store := &preparationStoreStub{
		definition: def,
		result: PreparedOptimization{
			OptimizationRunID: "01990000-1950-7000-8000-000000000201",
			InputSnapshotID:   "01990000-1930-7000-8000-000000000201",
			Status:            "PENDING",
		},
	}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	preparer.WithPointHealth(client)

	req := PreparationRequest{
		TenantID:    tenantID,
		SiteID:      siteID,
		SubjectType: "SITE",
		SubjectID:   siteID,
		Trigger:     TriggerManual,
	}
	if _, err := preparer.Prepare(t.Context(), req); err != nil {
		t.Fatalf("expected healthy preparation to pass, got %v", err)
	}
	reqs := stub.snapshot()
	if len(reqs) != 1 || !slices.Equal(reqs[0].Body.DeviceIDs, def.DeviceIDs) {
		t.Fatalf("expected Preparer to forward definition DeviceIDs, got %+v", reqs)
	}

	stub.setResponse(http.StatusOK, optimizationPointHealthResponse{
		Healthy: false,
		Detail:  "point hdr.sup_t telemetry health (BAD) suppresses optimization",
	}, "")
	_, err = preparer.Prepare(t.Context(), req)
	if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonInputsInvalid {
		t.Fatalf("expected ReasonInputsInvalid on unhealthy point, got ok=%v reason=%s err=%v", ok, reason, err)
	}

	stub.setResponse(http.StatusServiceUnavailable, optimizationPointHealthResponse{}, `{"code":"UNAVAILABLE"}`)
	_, err = preparer.Prepare(t.Context(), req)
	if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonInputsUnreadable {
		t.Fatalf("expected ReasonInputsUnreadable on 503, got ok=%v reason=%s err=%v", ok, reason, err)
	}
}

func writeTestCA(t *testing.T, dir string) (string, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return caPath, cert, key
}

func writeTestLeafCert(t *testing.T, dir, prefix string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, dnsNames []string, spiffe string, eku []x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	certPath, keyPath := writeTestLeafCertFiles(t, dir, prefix, caCert, caKey, dnsNames, spiffe, eku)
	loaded, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func writeTestClientCertFiles(t *testing.T, dir, prefix string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, spiffe string) (string, string) {
	t.Helper()
	return writeTestLeafCertFiles(t, dir, prefix, caCert, caKey, nil, spiffe, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
}

func writeTestLeafCertFiles(t *testing.T, dir, prefix string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, dnsNames []string, spiffe string, eku []x509.ExtKeyUsage) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var uris []*url.URL
	if spiffe != "" {
		parsed, parseErr := url.Parse(spiffe)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		uris = []*url.URL{parsed}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: prefix},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  eku,
		DNSNames:     dnsNames,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		URIs:         uris,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, prefix+".crt")
	keyPath := filepath.Join(dir, prefix+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
