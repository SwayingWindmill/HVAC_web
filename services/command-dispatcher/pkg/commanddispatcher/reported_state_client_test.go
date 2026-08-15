package commanddispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestReportedStateClientReadsExactCohort(t *testing.T) {
	observedAt := time.Date(2026, 7, 26, 2, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != internalCommandReportedStatePath || request.URL.Query().Get("key") != "zone.temperature_setpoint" {
			t.Fatalf("request=%s %s", request.Method, request.URL.String())
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"schemaVersion":  1,
			"evidenceId":     "s2:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"tenantId": "org-1", "siteId": "site-1", "deviceId": "device-1",
			"evaluationAvailability": "AVAILABLE", "presence": "ONLINE", "readiness": "CURRENT",
			"freshness": "FRESH", "quality": "GOOD", "businessRevision": 19,
			"reportedValue": map[string]any{"number": 22.5}, "observedAt": observedAt, "reportedStateKey": "zone.temperature_setpoint",
		})
	}))
	defer server.Close()
	client, err := NewReportedStateClient(ReportedStateClientConfig{
		BaseURL: server.URL, HTTPClient: server.Client(), TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1",
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

func TestReportedStateClientRejectsEnvelopeOutsideCohort(t *testing.T) {
	client, err := NewReportedStateClient(ReportedStateClientConfig{
		BaseURL: "https://s2.example.test", HTTPClient: http.DefaultClient, TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ReadReportedState(context.Background(), commandmodel.VerificationEnvelope{
		TenantID: "org-1", SiteID: "site-1", DeviceID: "other-device", CommandID: "command-1", AttemptID: "attempt-1", ExecutionFence: 1,
	}); err == nil {
		t.Fatal("expected out-of-cohort envelope to fail closed")
	}
}
