package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/testpki"
)

func TestTelemetryRuntimeClientUsesMTLSSPIFFEIdentityAndS2Contract(t *testing.T) {
	files, serverTLS := createTestPKI(t)
	var received Observation
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != observationPath {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.PeerCertificates[0].URIs) != 1 || request.TLS.PeerCertificates[0].URIs[0].String() != "spiffe://hvac.local/thingsboard-telemetry-adapter" {
			t.Errorf("client SPIFFE identity was not verified")
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode observation: %v", err)
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"observationId":"018f3e00-0000-7000-8000-000000000201","evidenceId":"018f3e00-0000-7000-8000-000000000202","status":"ACCEPTED","quality":"GOOD","qualityReasons":[],"stateChanged":true,"positionAdvanced":true}`))
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	config := TelemetryRuntimeConfig{
		BaseURL:    server.URL,
		CAFile:     files.ca,
		CertFile:   files.clientCert,
		KeyFile:    files.clientKey,
		ServerName: "localhost",
	}
	httpClient, err := NewMTLSHTTPClient(config)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewTelemetryRuntimeClient(server.URL, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	observation := Observation{
		IntegrationInstanceID: "018f3e00-0000-7000-8000-000000000101",
		SourcePath:            "POLL",
		ExternalEntityType:    "DEVICE",
		ExternalID:            "tb-device-1",
		TelemetryKey:          "hvac.power",
		Value:                 12.5,
		ValueType:             "NUMBER",
		SampledAt:             "2026-07-28T08:00:00Z",
		SourcePosition: SourcePosition{
			Partition: "thingsboard:tb-device-1:powerKw",
			Offset:    1000,
			EventID:   "018f3e00-0000-7000-8000-000000000301",
		},
	}
	receipt, err := client.AcceptObservation(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "ACCEPTED" || receipt.EvidenceID == "" || !receipt.PositionAdvanced || received.TelemetryKey != "hvac.power" || received.SourcePath != "POLL" {
		t.Fatalf("unexpected contract result receipt=%#v received=%#v", receipt, received)
	}
}

type testPKIFiles struct {
	ca         string
	clientCert string
	clientKey  string
}

func createTestPKI(t *testing.T) (testPKIFiles, *tls.Config) {
	t.Helper()
	bundle, err := testpki.Generate(
		"spiffe://hvac.local/telemetry-runtime-service",
		"spiffe://hvac.local/thingsboard-telemetry-adapter",
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := bundle.WriteFiles(directory); err != nil {
		t.Fatal(err)
	}
	serverTLS, err := bundle.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	return testPKIFiles{
		ca:         filepath.Join(directory, "ca.pem"),
		clientCert: filepath.Join(directory, "gateway-cert.pem"),
		clientKey:  filepath.Join(directory, "gateway-key.pem"),
	}, serverTLS
}
