package commanddispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestReportedStateClientReadsTheCommandedDevice(t *testing.T) {
	observedAt := time.Date(2026, 7, 26, 2, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != internalCommandReportedStatePath || request.URL.Query().Get("key") != "zone.temperature_setpoint" {
			t.Fatalf("request=%s %s", request.Method, request.URL.String())
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"schemaVersion": 1,
			"evidenceId":    "s2:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"tenantId":      "org-1", "siteId": "site-1", "deviceId": "device-1",
			"evaluationAvailability": "AVAILABLE", "presence": "ONLINE", "readiness": "CURRENT",
			"freshness": "FRESH", "quality": "GOOD", "businessRevision": 19,
			"reportedValue": map[string]any{"number": 22.5}, "observedAt": observedAt, "reportedStateKey": "zone.temperature_setpoint",
		})
	}))
	defer server.Close()
	client, err := NewReportedStateClient(ReportedStateClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, reported, err := client.ReadReportedState(context.Background(), commandmodel.VerificationEnvelope{
		TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1", CommandID: "command-1", AttemptID: "attempt-1", ExecutionFence: 1,
		VerificationPointKey: "zone.temperature_setpoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidenceID == "" || reported.BusinessRevision != 19 || reported.ReportedValue.Number == nil || *reported.ReportedValue.Number != 22.5 || !reported.ObservedAt.Equal(observedAt) {
		t.Fatalf("evidence=%q reported=%#v", evidenceID, reported)
	}
}

func TestReportedStateClientRejectsStateOfAnotherDevice(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"schemaVersion": 1,
			"evidenceId":    "s2:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"tenantId":      "org-1", "siteId": "site-1", "deviceId": "other-device",
			"evaluationAvailability": "AVAILABLE", "presence": "ONLINE", "readiness": "CURRENT",
			"freshness": "FRESH", "quality": "GOOD", "businessRevision": 19,
			"reportedValue": map[string]any{"number": 22.5}, "observedAt": time.Now().UTC(), "reportedStateKey": "run_state",
		})
	}))
	defer server.Close()
	client, err := NewReportedStateClient(ReportedStateClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ReadReportedState(context.Background(), commandmodel.VerificationEnvelope{
		TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1", CommandID: "command-1", AttemptID: "attempt-1", ExecutionFence: 1,
		VerificationPointKey: "run_state",
	}); err == nil {
		t.Fatal("state of another Device was accepted for the commanded Device")
	}
}

func TestReportedStateClientPropagatesTraceparentHeader(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	var gotTraceparent string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotTraceparent = request.Header.Get("traceparent")
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"schemaVersion": 1,
			"evidenceId":    "s2:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"tenantId":      "org-1", "siteId": "site-1", "deviceId": "device-1",
			"evaluationAvailability": "AVAILABLE", "presence": "ONLINE", "readiness": "CURRENT",
			"freshness": "FRESH", "quality": "GOOD", "businessRevision": 19,
			"reportedValue": map[string]any{"number": 22.5}, "observedAt": time.Date(2026, 7, 26, 2, 0, 0, 0, time.UTC), "reportedStateKey": "zone.temperature_setpoint",
		})
	}))
	defer server.Close()

	client, err := NewReportedStateClient(ReportedStateClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tracer := observability.NewTracer("command-verifier", nil)
	ctx := observability.ContextWithRemoteParent(context.Background(), tracer, traceparent, "")
	if _, _, err := client.ReadReportedState(ctx, commandmodel.VerificationEnvelope{
		TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1", CommandID: "command-1", AttemptID: "attempt-1", ExecutionFence: 1,
		VerificationPointKey: "zone.temperature_setpoint",
	}); err != nil {
		t.Fatal(err)
	}
	if gotTraceparent != traceparent {
		t.Fatalf("reported-state client traceparent=%q, want %q", gotTraceparent, traceparent)
	}

	gotTraceparent = ""
	const envelopeTraceID = "abcdef0123456789abcdef0123456789"
	if _, _, err := client.ReadReportedState(context.Background(), commandmodel.VerificationEnvelope{
		TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1", CommandID: "command-1", AttemptID: "attempt-1", ExecutionFence: 1,
		VerificationPointKey: "zone.temperature_setpoint", TraceID: " " + "ABCDEF0123456789ABCDEF0123456789" + " ",
	}); err != nil {
		t.Fatal(err)
	}
	wantFallback := "00-" + envelopeTraceID + "-0000000000000001-01"
	if gotTraceparent != wantFallback {
		t.Fatalf("reported-state client envelope fallback traceparent=%q, want %q", gotTraceparent, wantFallback)
	}
}
