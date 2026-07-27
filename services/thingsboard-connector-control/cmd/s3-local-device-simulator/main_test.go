package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProviderAndReportedStateCloseTheLocalLoop(t *testing.T) {
	clock := time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC)
	sim := &simulator{
		state:              &deviceState{setpointC: 23, revision: 21, updatedAt: clock.Add(-time.Minute)},
		organizationID:     "018f3e00-0000-7000-8000-000000000001",
		siteID:             "018f3e00-1000-7000-8000-000000000001",
		deviceID:           "018f3e00-3000-7000-8000-000000000001",
		externalDeviceID:   "local-device-1",
		providerMethod:     "setTemperatureSetpoint",
		reportedStateKey:   "temperatureSetpointC",
		providerCredential: "local-provider-token",
		verifierSPIFFE:     "spiffe://hvac.local/command-verifier",
		now:                func() time.Time { return clock },
	}

	providerRequest := httptest.NewRequest(http.MethodPost, "/api/rpc/twoway/local-device-1", strings.NewReader(`{"method":"setTemperatureSetpoint","params":{"setpointC":24},"timeout":5000}`))
	providerRequest.Header.Set("X-Authorization", "Bearer local-provider-token")
	providerResponse := httptest.NewRecorder()
	sim.providerHandler().ServeHTTP(providerResponse, providerRequest)
	if providerResponse.Code != http.StatusOK {
		t.Fatalf("provider status=%d body=%s", providerResponse.Code, providerResponse.Body.String())
	}

	spiffe, err := url.Parse(sim.verifierSPIFFE)
	if err != nil {
		t.Fatal(err)
	}
	reportedRequest := httptest.NewRequest(http.MethodGet, "/internal/v1/commands/reported-state", nil)
	reportedRequest.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{spiffe}}}}
	reportedResponse := httptest.NewRecorder()
	sim.reportedStateHandler().ServeHTTP(reportedResponse, reportedRequest)
	if reportedResponse.Code != http.StatusOK {
		t.Fatalf("reported-state status=%d body=%s", reportedResponse.Code, reportedResponse.Body.String())
	}
	var result reportedStateResponse
	if err := json.Unmarshal(reportedResponse.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReportedSetpointC != 24 || result.BusinessRevision != 22 || result.EvidenceID[:10] != "s2:sha256:" {
		t.Fatalf("reported state=%#v", result)
	}
}

func TestReportedStateRejectsWrongWorkload(t *testing.T) {
	wrong, _ := url.Parse("spiffe://hvac.local/not-verifier")
	sim := &simulator{state: &deviceState{}, verifierSPIFFE: "spiffe://hvac.local/command-verifier", now: time.Now}
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/commands/reported-state", nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{wrong}}}}
	response := httptest.NewRecorder()
	sim.reportedStateHandler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}
