package alarmservice

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const (
	controlTestTenantID  = "0195b5c0-0000-7000-8000-000000000001"
	controlTestSiteID    = "0195b5c0-0000-7000-8000-000000000002"
	controlTestDeviceID  = "0195b5c0-0000-7000-8000-000000000003"
	controlTestCommandID = "0195b5c0-0000-7000-8000-000000000004"
)

func TestControlFailureHandlerPublishesIdempotentOperationalAlarms(t *testing.T) {
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewControlFailureHandler(store)
	if err != nil {
		t.Fatalf("NewControlFailureHandler: %v", err)
	}

	payload := ControlEventRequest{
		SchemaVersion: 1,
		TenantID:      controlTestTenantID,
		SiteID:        controlTestSiteID,
		DeviceID:      strPtr(controlTestDeviceID),
		CommandID:     controlTestCommandID,
		EventKind:     ControlEventOutcomeUnknown,
		Capability:    "START",
		ReasonCode:    "CONNECTOR_REQUEST_COMMITTED",
		Detail:        "edge ack lost after dispatch commit",
		OccurredAt:    "2026-11-16T03:00:00Z",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, InternalControlEventsPath, strings.NewReader(string(body))))
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first status=%d want 201 body=%s", rec1.Code, rec1.Body.String())
	}
	var created alarmmodel.Alarm
	if err := json.Unmarshal(rec1.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.SourceType != alarmmodel.SourceExternal ||
		created.SourceReference != "control-failure:OUTCOME_UNKNOWN:"+controlTestCommandID ||
		created.CurrentSeverity != alarmmodel.SeverityCritical ||
		created.OccurrenceCount != 1 {
		t.Fatalf("unexpected created alarm: %#v", created)
	}
	if !strings.Contains(created.Summary, controlTestCommandID) || !strings.Contains(created.Summary, "OUTCOME_UNKNOWN") {
		t.Fatalf("expected diagnostic summary, got %q", created.Summary)
	}

	payload.OccurredAt = "2026-11-16T03:01:00Z"
	body2, _ := json.Marshal(payload)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, InternalControlEventsPath, strings.NewReader(string(body2))))
	if rec2.Code != http.StatusOK {
		t.Fatalf("repeat status=%d want 200 body=%s", rec2.Code, rec2.Body.String())
	}
	var updated alarmmodel.Alarm
	if err := json.Unmarshal(rec2.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.AlarmID != created.AlarmID || updated.OccurrenceCount != 2 {
		t.Fatalf("expected idempotent active alarm recurrence, got %#v vs %#v", updated, created)
	}

	powerPayload := `{
		"schemaVersion": 1,
		"tenantId": "` + controlTestTenantID + `",
		"siteId": "` + controlTestSiteID + `",
		"deviceId": "` + controlTestDeviceID + `",
		"commandId": "0195b5c0-0000-7000-8000-000000000005",
		"eventKind": "PULSE_DELIVERED_POWER_UNCONFIRMED",
		"capability": "START",
		"severity": "HIGH",
		"occurredAt": "2026-11-16T03:02:00Z"
	}`
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, httptest.NewRequest(http.MethodPost, InternalControlEventsPath, strings.NewReader(powerPayload)))
	if rec3.Code != http.StatusCreated {
		t.Fatalf("power unconfirmed status=%d body=%s", rec3.Code, rec3.Body.String())
	}
	var powerAlarm alarmmodel.Alarm
	if err := json.Unmarshal(rec3.Body.Bytes(), &powerAlarm); err != nil {
		t.Fatal(err)
	}
	if powerAlarm.CurrentSeverity != alarmmodel.SeverityMajor || powerAlarm.AlarmType != "COMMAND_POWER_UNCONFIRMED" {
		t.Fatalf("unexpected power alarm: %#v", powerAlarm)
	}

	recBad := httptest.NewRecorder()
	handler.ServeHTTP(recBad, httptest.NewRequest(http.MethodPost, InternalControlEventsPath, strings.NewReader(`{"schemaVersion":2,"tenantId":"bad"}`)))
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("invalid schema status=%d want 400", recBad.Code)
	}
}

func TestOwnerRouterRestrictsControlEventsBySPIFFE(t *testing.T) {
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	controlHandler, err := NewControlFailureHandler(store)
	if err != nil {
		t.Fatal(err)
	}
	gatewayHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	telemetryHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router, err := NewOwnerRouter(OwnerRouterConfig{
		GatewaySPIFFE:   "spiffe://hvac.local/platform-gateway",
		TelemetrySPIFFE: "spiffe://hvac.local/telemetry-runtime-service",
		ReaderSPIFFE:    "spiffe://hvac.local/operations-agent-service",
		ControlEventsSPIFFEs: []string{
			"spiffe://hvac.local/command-service",
			"spiffe://hvac.local/optimization-service",
		},
		GatewayHandler:       gatewayHandler,
		TelemetryHandler:     telemetryHandler,
		ControlEventsHandler: controlHandler,
	})
	if err != nil {
		t.Fatal(err)
	}

	makeTLSRequest := func(method, path, body, spiffe string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		uri, parseErr := url.Parse(spiffe)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		req.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}},
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	validEvent := `{"schemaVersion":1,"tenantId":"` + controlTestTenantID + `","siteId":"` + controlTestSiteID + `","commandId":"` + controlTestCommandID + `","eventKind":"EMERGENCY_DO_RELEASE","occurredAt":"2026-11-16T03:05:00Z"}`

	if rec := makeTLSRequest(http.MethodPost, InternalControlEventsPath, validEvent, "spiffe://hvac.local/platform-gateway"); rec.Code != http.StatusCreated {
		t.Fatalf("gateway control event status=%d want 201 body=%s", rec.Code, rec.Body.String())
	}
	if rec := makeTLSRequest(http.MethodPost, InternalControlEventsPath, validEvent, "spiffe://hvac.local/command-service"); rec.Code != http.StatusOK {
		t.Fatalf("command-service recurrence status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	optimizationEvent := `{"schemaVersion":1,"tenantId":"` + controlTestTenantID + `","siteId":"` + controlTestSiteID + `","deviceId":"` + controlTestDeviceID + `","commandId":"0195b5c0-0000-7000-8000-000000000006","eventKind":"SETPOINT_TRACKING_DEVIATION","capability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","reasonCode":"TRACKING_DEVIATION","detail":"supply temp stayed at 9.2C vs 8.0C","occurredAt":"2026-11-18T03:05:00Z"}`
	if rec := makeTLSRequest(http.MethodPost, InternalControlEventsPath, optimizationEvent, "spiffe://hvac.local/optimization-service"); rec.Code != http.StatusCreated {
		t.Fatalf("optimization-service control event status=%d want 201 body=%s", rec.Code, rec.Body.String())
	}
	if rec := makeTLSRequest(http.MethodPost, InternalControlEventsPath, validEvent, "spiffe://hvac.local/telemetry-runtime-service"); rec.Code != http.StatusForbidden {
		t.Fatalf("telemetry workload should be forbidden, got %d", rec.Code)
	}
	if rec := makeTLSRequest(http.MethodPost, InternalControlEventsPath, validEvent, "spiffe://hvac.local/operations-agent-service"); rec.Code != http.StatusForbidden {
		t.Fatalf("reader workload should be forbidden from posting control events, got %d", rec.Code)
	}
	if rec := makeTLSRequest(http.MethodGet, InternalSiteAlarmsPrefix+controlTestSiteID+"/alarms", "", "spiffe://hvac.local/command-service"); rec.Code != http.StatusForbidden {
		t.Fatalf("command-service should not access general alarm routes, got %d", rec.Code)
	}
	if rec := makeTLSRequest(http.MethodGet, InternalSiteAlarmsPrefix+controlTestSiteID+"/alarms", "", "spiffe://hvac.local/optimization-service"); rec.Code != http.StatusForbidden {
		t.Fatalf("optimization-service should not access general alarm routes, got %d", rec.Code)
	}
}

func TestControlFailureHandlerPublishesOptimizationControlEvents(t *testing.T) {
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewControlFailureHandler(store)
	if err != nil {
		t.Fatalf("NewControlFailureHandler: %v", err)
	}

	cases := []struct {
		name            string
		commandID       string
		eventKind       string
		wantAlarmType   string
		wantSeverity    alarmmodel.Severity
		wantTitle       string
		wantSummaryFrag string
	}{
		{
			name:            "setpoint tracking deviation",
			commandID:       "0195b5c0-0000-7000-8000-000000000011",
			eventKind:       ControlEventSetpointTrackingDeviation,
			wantAlarmType:   "SETPOINT_TRACKING_DEVIATION",
			wantSeverity:    alarmmodel.SeverityMajor,
			wantTitle:       "设定值下发后未收敛",
			wantSummaryFrag: "自动控制指令下发后在收敛窗口内未达到目标设定值",
		},
		{
			name:            "auto control rollback failed",
			commandID:       "0195b5c0-0000-7000-8000-000000000012",
			eventKind:       ControlEventAutoRollbackFailed,
			wantAlarmType:   "AUTO_CONTROL_ROLLBACK_FAILED",
			wantSeverity:    alarmmodel.SeverityCritical,
			wantTitle:       "自动控制异常回退失败",
			wantSummaryFrag: "自动控制进入异常触发条件但回退恢复指令未能确认成功",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := ControlEventRequest{
				SchemaVersion: 1,
				TenantID:      controlTestTenantID,
				SiteID:        controlTestSiteID,
				DeviceID:      strPtr(controlTestDeviceID),
				CommandID:     tc.commandID,
				EventKind:     tc.eventKind,
				Capability:    "SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
				ReasonCode:    tc.eventKind,
				Detail:        "target=8.0C live=9.4C",
				OccurredAt:    "2026-11-18T04:00:00Z",
				ActorType:     "WORKLOAD",
				ActorID:       "spiffe://hvac.local/optimization-service",
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			rec1 := httptest.NewRecorder()
			handler.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, InternalControlEventsPath, strings.NewReader(string(body))))
			if rec1.Code != http.StatusCreated {
				t.Fatalf("status=%d want 201 body=%s", rec1.Code, rec1.Body.String())
			}
			var created alarmmodel.Alarm
			if err := json.Unmarshal(rec1.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.AlarmType != tc.wantAlarmType ||
				created.CurrentSeverity != tc.wantSeverity ||
				created.Title != tc.wantTitle ||
				created.SourceReference != "control-failure:"+tc.eventKind+":"+tc.commandID ||
				created.OccurrenceCount != 1 {
				t.Fatalf("unexpected alarm: %#v", created)
			}
			if !strings.Contains(created.Summary, tc.wantSummaryFrag) || !strings.Contains(created.Summary, "target=8.0C live=9.4C") {
				t.Fatalf("unexpected summary %q", created.Summary)
			}

			payload.OccurredAt = "2026-11-18T04:01:00Z"
			body2, _ := json.Marshal(payload)
			rec2 := httptest.NewRecorder()
			handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, InternalControlEventsPath, strings.NewReader(string(body2))))
			if rec2.Code != http.StatusOK {
				t.Fatalf("recurrence status=%d want 200 body=%s", rec2.Code, rec2.Body.String())
			}
			var updated alarmmodel.Alarm
			if err := json.Unmarshal(rec2.Body.Bytes(), &updated); err != nil {
				t.Fatal(err)
			}
			if updated.AlarmID != created.AlarmID || updated.OccurrenceCount != 2 {
				t.Fatalf("expected idempotent recurrence, got %#v vs %#v", updated, created)
			}
		})
	}
}

func strPtr(v string) *string { return &v }
