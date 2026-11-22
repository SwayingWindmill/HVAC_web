package main

import (
	"context"
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
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodPost, alarmservice.InternalEdgeAlarmEventsPath, nil)); got != "alarms.ingest_edge_event" {
		t.Fatalf("route=%q want alarms.ingest_edge_event", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodGet, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules", nil)); got != "alarms.rules" {
		t.Fatalf("route=%q want alarms.rules", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodPost, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules:simulate", nil)); got != "alarms.rules" {
		t.Fatalf("route=%q want alarms.rules", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodPost, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules:diff", nil)); got != "alarms.rules" {
		t.Fatalf("route=%q want alarms.rules", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodGet, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules/revisions", nil)); got != "alarms.rules" {
		t.Fatalf("route=%q want alarms.rules", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodPost, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules/revisions/2:rollback", nil)); got != "alarms.rules" {
		t.Fatalf("route=%q want alarms.rules", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodGet, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules/dead-letters", nil)); got != "alarms.dead_letters" {
		t.Fatalf("route=%q want alarms.dead_letters", got)
	}
	if got := alarmObservabilityRoute(httptest.NewRequest(http.MethodPost, alarmservice.InternalSiteAlarmsPrefix+"018f3e00-1000-7000-8000-000000000001/alarm-rules/dead-letters/018f3e00-1000-7000-8000-000000000002/replay", nil)); got != "alarms.dead_letters" {
		t.Fatalf("route=%q want alarms.dead_letters", got)
	}
}

func TestAlarmOwnerShutdownOrderAndRequiredEnv(t *testing.T) {
	t.Setenv("ALARM_TLS_CERT", "")
	if _, err := requiredEnv("ALARM_TLS_CERT"); err == nil {
		t.Fatal("expected error for missing ALARM_TLS_CERT")
	}
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when ALARM_TLS_CERT is missing", code)
	}

	t.Setenv("ALARM_TLS_CERT", "  /tmp/alarm.crt  ")
	val, err := requiredEnv("ALARM_TLS_CERT")
	if err != nil || val != "/tmp/alarm.crt" {
		t.Fatalf("requiredEnv() = %q, %v", val, err)
	}

	var order []string
	diagDone := make(chan struct{})
	scheduledDone := make(chan struct{})

	shutdownAlarmOwnerHTTP(alarmOwnerHTTPShutdownDeps{
		markNotReady:   func() { order = append(order, "mark_not_ready") },
		shutdownServer: func(context.Context) error { order = append(order, "server_shutdown"); return nil },
		shutdownDiag: func(context.Context) error {
			order = append(order, "diagnostics_shutdown")
			close(diagDone)
			return nil
		},
		diagnosticsDone: diagDone,
	})
	drainAlarmOwnerWorkers(alarmOwnerWorkerDrainDeps{
		cancelScheduled: func() {
			order = append(order, "cancel_scheduled")
			close(scheduledDone)
		},
		scheduledDone: scheduledDone,
		shutdownTelemetry: func(context.Context) error {
			order = append(order, "telemetry_shutdown")
			return nil
		},
	})

	want := "mark_not_ready,server_shutdown,diagnostics_shutdown,cancel_scheduled,telemetry_shutdown"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("shutdown order = %s, want %s", got, want)
	}
}
