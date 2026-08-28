package alarmservice

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const (
	ownerRouterGatewaySPIFFE   = "spiffe://hvac.local/platform-gateway"
	ownerRouterTelemetrySPIFFE = "spiffe://hvac.local/telemetry-runtime-service"
)

func TestOwnerRouterSeparatesGatewayAndTelemetryWorkloads(t *testing.T) {
	gatewayCalls := 0
	telemetryCalls := 0
	router, err := NewOwnerRouter(OwnerRouterConfig{
		GatewaySPIFFE:   ownerRouterGatewaySPIFFE,
		TelemetrySPIFFE: ownerRouterTelemetrySPIFFE,
		GatewayHandler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			gatewayCalls++
			writer.WriteHeader(http.StatusNoContent)
		}),
		TelemetryHandler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			telemetryCalls++
			writer.WriteHeader(http.StatusNoContent)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	telemetryRequest := ownerRouterRequest(t, InternalTelemetryEvaluationPath, ownerRouterTelemetrySPIFFE)
	telemetryResponse := httptest.NewRecorder()
	router.ServeHTTP(telemetryResponse, telemetryRequest)
	if telemetryResponse.Code != http.StatusNoContent || telemetryCalls != 1 || gatewayCalls != 0 {
		t.Fatalf("authorized Telemetry workload was not routed exclusively to evaluation: status=%d telemetry=%d gateway=%d", telemetryResponse.Code, telemetryCalls, gatewayCalls)
	}

	gatewayOnTelemetry := ownerRouterRequest(t, InternalTelemetryEvaluationPath, ownerRouterGatewaySPIFFE)
	gatewayOnTelemetryResponse := httptest.NewRecorder()
	router.ServeHTTP(gatewayOnTelemetryResponse, gatewayOnTelemetry)
	if gatewayOnTelemetryResponse.Code != http.StatusForbidden || telemetryCalls != 1 || gatewayCalls != 0 {
		t.Fatalf("gateway workload reached Telemetry evaluation route: status=%d telemetry=%d gateway=%d", gatewayOnTelemetryResponse.Code, telemetryCalls, gatewayCalls)
	}

	telemetryOnGateway := ownerRouterRequest(t, InternalSiteAlarmsPrefix+"01910000-0001-7000-8000-000000000001/alarms", ownerRouterTelemetrySPIFFE)
	telemetryOnGatewayResponse := httptest.NewRecorder()
	router.ServeHTTP(telemetryOnGatewayResponse, telemetryOnGateway)
	if telemetryOnGatewayResponse.Code != http.StatusForbidden || telemetryCalls != 1 || gatewayCalls != 0 {
		t.Fatalf("Telemetry workload reached gateway route: status=%d telemetry=%d gateway=%d", telemetryOnGatewayResponse.Code, telemetryCalls, gatewayCalls)
	}

	gatewayRequest := ownerRouterRequest(t, InternalSiteAlarmsPrefix+"01910000-0001-7000-8000-000000000001/alarms", ownerRouterGatewaySPIFFE)
	gatewayResponse := httptest.NewRecorder()
	router.ServeHTTP(gatewayResponse, gatewayRequest)
	if gatewayResponse.Code != http.StatusNoContent || telemetryCalls != 1 || gatewayCalls != 1 {
		t.Fatalf("authorized gateway workload was not routed to Alarm API: status=%d telemetry=%d gateway=%d", gatewayResponse.Code, telemetryCalls, gatewayCalls)
	}
}

func TestOwnerRouterRejectsRequestsWithoutAuthorizedWorkloadIdentity(t *testing.T) {
	router, err := NewOwnerRouter(OwnerRouterConfig{
		GatewaySPIFFE:    ownerRouterGatewaySPIFFE,
		TelemetrySPIFFE:  ownerRouterTelemetrySPIFFE,
		GatewayHandler:   http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("gateway handler called") }),
		TelemetryHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("telemetry handler called") }),
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, InternalTelemetryEvaluationPath, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("request without mTLS workload identity was not rejected: %d", response.Code)
	}
}

func ownerRouterRequest(t *testing.T, path, identity string) *http.Request {
	t.Helper()
	uri, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	return request
}
