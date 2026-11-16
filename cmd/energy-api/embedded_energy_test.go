package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestAttachEmbeddedTelemetryExposesOwnerMetricsOnDiagnostics(t *testing.T) {
	parent := observability.NewRuntime(observability.RuntimeConfig{Service: "platform-gateway", QueueSize: 16})
	cmdRuntime := observability.NewRuntime(observability.RuntimeConfig{Service: "energy-api-command", QueueSize: 16})
	alarmRuntime := observability.NewRuntime(observability.RuntimeConfig{Service: "energy-api-alarm", QueueSize: 16})

	_ = cmdRuntime.Metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": "verified"}, 5)
	_ = cmdRuntime.Metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": "timed_out"}, 1)
	_ = alarmRuntime.Metrics.AddCounter("hvac_alarm_http_requests_total", "Alarm HTTP requests.", map[string]string{"service": "alarm-service", "method": "GET", "route": "alarms.list", "status_class": "2xx"}, 2)

	attachEmbeddedTelemetry(parent, &embeddedEnergyServices{
		telemetry: []*observability.Runtime{cmdRuntime, alarmRuntime},
	})

	rec := httptest.NewRecorder()
	parent.DiagnosticsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnostics /metrics status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`hvac_command_verifications_total{outcome="verified"} 5`,
		`hvac_command_verifications_total{outcome="timed_out"} 1`,
		`hvac_alarm_http_requests_total{method="GET",route="alarms.list",service="alarm-service",status_class="2xx"} 2`,
		`s0_telemetry_dropped_spans{service="platform-gateway"} 0`,
		`s0_telemetry_dropped_spans{service="energy-api-command"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("diagnostics /metrics missing %q:\n%s", want, body)
		}
	}
	if count := strings.Count(body, "# TYPE hvac_command_verifications_total counter"); count != 1 {
		t.Fatalf("expected deduplicated # TYPE line, got %d:\n%s", count, body)
	}
}

func TestEmbeddedCommandControlFailureNotifierPublishesIntoAlarmStore(t *testing.T) {
	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := &embeddedAlarmStoreRef{}
	ref.setStore(store)

	notifier := buildEmbeddedCommandControlFailureNotifier(ref, nil, nil)
	tenantID := "0195b5c0-0000-7000-8000-000000000010"
	siteID := "0195b5c0-0000-7000-8000-000000000011"
	deviceID := "0195b5c0-0000-7000-8000-000000000012"
	pointID := "0195b5c0-0000-7000-8000-000000000013"
	commandID := "0195b5c0-0000-7000-8000-000000000014"

	notifier(context.Background(), commandservice.ControlFailureEvent{
		Kind:       commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
		TenantID:   tenantID,
		SiteID:     siteID,
		DeviceID:   deviceID,
		PointID:    pointID,
		CommandID:  commandID,
		Capability: commandmodel.CapabilityStart,
		ReasonCode: commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
		Detail:     "chiller power remained below threshold after pulse",
		OccurredAt: time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC),
	})

	list, err := store.List(context.Background(), tenantID, siteID, alarmservice.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 operational alarm published, got %d", len(list.Items))
	}
	alarm := list.Items[0]
	if alarm.SourceType != alarmmodel.SourceExternal ||
		alarm.AlarmType != "COMMAND_POWER_UNCONFIRMED" ||
		alarm.CurrentSeverity != alarmmodel.SeverityMajor ||
		!strings.Contains(alarm.Summary, commandID) {
		t.Fatalf("unexpected operational alarm: %#v", alarm)
	}
}
