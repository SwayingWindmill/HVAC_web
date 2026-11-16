package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

func TestBuildAlarmOwnerRouterWiresControlEventsEndpoint(t *testing.T) {
	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	router, err := buildAlarmOwnerRouter(
		store,
		"spiffe://hvac.local/platform-gateway",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)
	if err != nil {
		t.Fatalf("buildAlarmOwnerRouter: %v", err)
	}

	reqFor := func(spiffe, body string) *httptest.ResponseRecorder {
		uri, parseErr := url.Parse(spiffe)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		req := httptest.NewRequest(http.MethodPost, alarmservice.InternalControlEventsPath, strings.NewReader(body))
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	payload := `{"schemaVersion":1,"tenantId":"018f3d00-0000-7000-8000-000000000001","siteId":"018f3e00-1000-7000-8000-000000000001","commandId":"cmd-owner-1","eventKind":"OUTCOME_UNKNOWN","occurredAt":"2026-11-16T03:05:00Z"}`
	if rec := reqFor("spiffe://hvac.local/command-service", payload); rec.Code != http.StatusCreated {
		t.Fatalf("command-service control event status=%d want 201 body=%s", rec.Code, rec.Body.String())
	}
	if rec := reqFor("spiffe://hvac.local/connectivity-service", payload); rec.Code != http.StatusOK {
		t.Fatalf("connectivity-service recurrence status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if rec := reqFor("spiffe://hvac.local/telemetry-runtime-service", payload); rec.Code != http.StatusForbidden {
		t.Fatalf("telemetry workload should be forbidden on control events, got %d", rec.Code)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodPost, alarmservice.InternalControlEventsPath, nil)); got != "alarms.publish_control_event" {
		t.Fatalf("route=%q want alarms.publish_control_event", got)
	}
}
