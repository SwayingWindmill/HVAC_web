package main

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
)

func TestValidatePrivateServiceURLRequiresHTTPSOrigin(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "https origin", value: "https://core.example.test"},
		{name: "https origin trailing slash", value: "https://core.example.test/"},
		{name: "http rejected", value: "http://core.example.test", wantErr: true},
		{name: "credentials rejected", value: "https://user:password@core.example.test", wantErr: true},
		{name: "path rejected", value: "https://core.example.test/private", wantErr: true},
		{name: "query rejected", value: "https://core.example.test?target=legacy", wantErr: true},
		{name: "relative rejected", value: "core.example.test", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePrivateServiceURL(test.value, "CORE_URL")
			if (err != nil) != test.wantErr {
				t.Fatalf("validatePrivateServiceURL(%q) error=%v", test.value, err)
			}
		})
	}
}

func TestGatewayServerTLSConfigVerifiesOptionalClientCertificates(t *testing.T) {
	clientCAs := x509.NewCertPool()
	config := gatewayServerTLSConfig(tls.Certificate{}, clientCAs)
	if config.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion=%d", config.MinVersion)
	}
	if config.ClientAuth != tls.VerifyClientCertIfGiven || config.ClientCAs != clientCAs || len(config.Certificates) != 1 {
		t.Fatalf("unexpected Gateway TLS config: %#v", config)
	}
}

func TestLoopbackTelemetryFixtureURL(t *testing.T) {
	for _, value := range []string{
		"https://127.0.0.1:18456",
		"https://localhost:18456",
		"https://[::1]:18456",
	} {
		if !isLoopbackTelemetryFixtureURL(value) {
			t.Fatalf("loopback fixture URL rejected: %s", value)
		}
	}
	for _, value := range []string{
		"",
		"http://127.0.0.1:18456",
		"https://telemetry.example.test",
		"https://127.0.0.1:18456/path",
		"https://user@127.0.0.1:18456",
	} {
		if isLoopbackTelemetryFixtureURL(value) {
			t.Fatalf("non-fixture URL accepted: %s", value)
		}
	}
}
