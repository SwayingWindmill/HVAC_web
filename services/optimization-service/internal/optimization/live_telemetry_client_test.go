package optimization

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type capturedLiveHTTPRequest struct {
	Path        string
	Traceparent string
	PeerSPIFFE  string
	Body        optimizationLiveObservationsRequest
}

type stubLiveHTTPHandler struct {
	mu       sync.Mutex
	status   int
	response OptimizationLiveObservationsResult
	rawBody  string
	requests []capturedLiveHTTPRequest
}

func (stub *stubLiveHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	var body optimizationLiveObservationsRequest
	_ = json.NewDecoder(request.Body).Decode(&body)
	var peerSPIFFE string
	if request.TLS != nil && len(request.TLS.PeerCertificates) > 0 && len(request.TLS.PeerCertificates[0].URIs) > 0 {
		peerSPIFFE = request.TLS.PeerCertificates[0].URIs[0].String()
	}

	stub.mu.Lock()
	stub.requests = append(stub.requests, capturedLiveHTTPRequest{
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

func (stub *stubLiveHTTPHandler) setResponse(status int, response OptimizationLiveObservationsResult, rawBody string) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.status = status
	stub.response = response
	stub.rawBody = rawBody
}

func (stub *stubLiveHTTPHandler) snapshot() []capturedLiveHTTPRequest {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	out := make([]capturedLiveHTTPRequest, len(stub.requests))
	copy(out, stub.requests)
	return out
}

func TestWorkloadTelemetryLiveClientMTLSAndTracing(t *testing.T) {
	pkiDir := t.TempDir()
	caPath, caCert, caKey := writeTestCA(t, pkiDir)
	serverCert := writeTestLeafCert(t, pkiDir, "server", caCert, caKey, []string{"telemetry-runtime-service"}, "spiffe://hvac.local/telemetry-runtime-service", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	clientCertPath, clientKeyPath := writeTestClientCertFiles(t, pkiDir, "opt", caCert, caKey, "spiffe://hvac.local/optimization-service")

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	stub := &stubLiveHTTPHandler{
		status: http.StatusOK,
		response: OptimizationLiveObservationsResult{
			Observations: map[string]OptimizationLiveObservation{
				"hdr.sup_t": {
					ObservationID:   "01990000-2940-7000-8000-000000000001",
					DeviceID:        "01990000-2900-7000-8000-000000000001",
					PointID:         "01990000-2910-7000-8000-000000000001",
					TelemetryKey:    "hdr.sup_t",
					PointRevision:   2,
					SampledAt:       "2026-11-18T03:59:30Z",
					ReceivedAt:      "2026-11-18T03:59:31Z",
					Value:           6.8,
					Unit:            "Cel",
					Quality:         "GOOD",
					QualityReasons:  []string{},
					HealthStatus:    "HEALTHY",
					SourceEventID:   "01990000-2930-7000-8000-000000000001",
					SourcePartition: "p0",
					SourceOffset:    42,
				},
			},
			AmbiguousKeys: []string{"zone.temp"},
		},
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

	client, err := NewWorkloadTelemetryLiveClient(TelemetryLiveClientConfig{
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
	wantTraceparent := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	ctx := observability.ContextWithRemoteParent(context.Background(), observability.NewTracer("optimization-service", nil), wantTraceparent, "")

	res, err := client.ReadLiveObservations(ctx, tenantID, siteID, []string{" hdr.sup_t ", "zone.temp", "hdr.sup_t"})
	if err != nil {
		t.Fatalf("expected live observations response, got %v", err)
	}
	if res.Observations["hdr.sup_t"].Value != 6.8 || !slices.Equal(res.AmbiguousKeys, []string{"zone.temp"}) {
		t.Fatalf("unexpected response: %+v", res)
	}

	captured := stub.snapshot()
	if len(captured) != 1 {
		t.Fatalf("expected 1 request, got %d", len(captured))
	}
	if captured[0].Path != InternalOptimizationLiveObservationsPath {
		t.Fatalf("path=%q want %q", captured[0].Path, InternalOptimizationLiveObservationsPath)
	}
	if captured[0].PeerSPIFFE != "spiffe://hvac.local/optimization-service" {
		t.Fatalf("peerSPIFFE=%q", captured[0].PeerSPIFFE)
	}
	if captured[0].Traceparent != wantTraceparent {
		t.Fatalf("traceparent=%q", captured[0].Traceparent)
	}
	if captured[0].Body.TenantID != tenantID || captured[0].Body.SiteID != siteID || !slices.Equal(captured[0].Body.TelemetryKeys, []string{"hdr.sup_t", "zone.temp"}) {
		t.Fatalf("unexpected body: %+v", captured[0].Body)
	}

	stub.setResponse(http.StatusServiceUnavailable, OptimizationLiveObservationsResult{}, `{"code":"TELEMETRY_LIVE_OBSERVATIONS_UNAVAILABLE"}`)
	if _, err := client.ReadLiveObservations(ctx, tenantID, siteID, []string{"hdr.sup_t"}); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected 503 error, got %v", err)
	}
}

func TestWorkloadTelemetryLiveClientValidation(t *testing.T) {
	if _, err := NewWorkloadTelemetryLiveClient(TelemetryLiveClientConfig{}); err == nil {
		t.Fatal("expected empty TelemetryURL error")
	}
	if _, err := NewWorkloadTelemetryLiveClient(TelemetryLiveClientConfig{TelemetryURL: "://bad-url"}); err == nil {
		t.Fatal("expected invalid TelemetryURL error")
	}

	stub := &stubLiveHTTPHandler{
		status:   http.StatusOK,
		response: OptimizationLiveObservationsResult{Observations: map[string]OptimizationLiveObservation{}},
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client, err := NewWorkloadTelemetryLiveClient(TelemetryLiveClientConfig{
		TelemetryURL: srv.URL,
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	tenantID := "01990000-3000-7000-8000-000000000001"
	siteID := "01990000-5000-7000-8000-000000000001"
	res, err := client.ReadLiveObservations(t.Context(), tenantID, siteID, nil)
	if err != nil || len(res.Observations) != 0 {
		t.Fatalf("empty keys should skip remote call, got res=%+v err=%v", res, err)
	}
	if len(stub.snapshot()) != 0 {
		t.Fatalf("empty keys must not trigger HTTP request: %+v", stub.snapshot())
	}
	if _, err := client.ReadLiveObservations(t.Context(), "invalid", siteID, []string{"k"}); err == nil {
		t.Fatal("expected invalid tenantId error")
	}
}
