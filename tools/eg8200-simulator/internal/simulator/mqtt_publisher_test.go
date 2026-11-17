package simulator

import (
	"context"
	"encoding/json"
	"regexp"
	"sync/atomic"
	"testing"
	"time"
)

var simulatorUUIDv7Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestLifecycleEventEnvelopesMatchGatewayUplinkContract(t *testing.T) {
	const gatewayID = "018f3e00-4000-7000-8000-000000000100"
	now := time.Date(2026, 11, 16, 12, 34, 56, 789000000, time.UTC)

	if got := mqttEventTopic(gatewayID); got != "hvac/v1/"+gatewayID+"/up/event" {
		t.Fatalf("mqttEventTopic = %q", got)
	}

	cases := []struct {
		eventType string
		severity  string
		data      json.RawMessage
	}{
		{"LWT", "WARNING", json.RawMessage(`{"reason":"UNEXPECTED_DISCONNECT"}`)},
		{"GATEWAY_CONNECTED", "INFO", json.RawMessage(`{}`)},
		{"GATEWAY_DISCONNECTED", "INFO", json.RawMessage(`{"clean":true}`)},
	}
	for idx, tc := range cases {
		raw, err := buildLifecycleEventPayload(gatewayID, tc.eventType, uint64(idx+1), now, tc.data)
		if err != nil {
			t.Fatalf("%s buildLifecycleEventPayload: %v", tc.eventType, err)
		}
		var env mqttEventEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s decode envelope: %v", tc.eventType, err)
		}
		if env.SchemaVersion != "2.0" || env.GatewayID != gatewayID || env.Sequence != uint64(idx+1) {
			t.Fatalf("%s invalid header: %+v", tc.eventType, env)
		}
		if !simulatorUUIDv7Regex.MatchString(env.MessageID) {
			t.Fatalf("%s messageId %q is not UUIDv7", tc.eventType, env.MessageID)
		}
		if env.Payload.EventType != tc.eventType || env.Payload.SourceType != "GATEWAY" || env.Payload.SourceID != gatewayID {
			t.Fatalf("%s invalid payload identity: %+v", tc.eventType, env.Payload)
		}
		if env.Payload.Severity != tc.severity {
			t.Fatalf("%s severity = %q, want %q", tc.eventType, env.Payload.Severity, tc.severity)
		}
		if env.Payload.EventTime != now.UnixMilli() {
			t.Fatalf("%s eventTime = %d, want %d", tc.eventType, env.Payload.EventTime, now.UnixMilli())
		}
	}
}

func TestLifecycleSessionPairsWillAndConnectedAndRegeneratesOnReconnect(t *testing.T) {
	const gatewayID = "018f3e00-4000-7000-8000-000000000100"
	session := &mqttSessionState{}
	seq := &atomic.Uint64{}
	t1 := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	t2 := t1.Add(30 * time.Second)

	s1, ts1, err := session.next(t1)
	if err != nil {
		t.Fatalf("session 1 next: %v", err)
	}
	will1Raw, err := buildSessionWillPayload(gatewayID, s1, seq.Add(1), ts1)
	if err != nil {
		t.Fatalf("will1: %v", err)
	}
	curID, curTS := session.current()
	conn1Raw, err := buildSessionConnectedPayload(gatewayID, curID, seq.Add(1), curTS)
	if err != nil {
		t.Fatalf("conn1: %v", err)
	}

	var will1, conn1 mqttEventEnvelope
	_ = json.Unmarshal(will1Raw, &will1)
	_ = json.Unmarshal(conn1Raw, &conn1)
	var will1Data, conn1Data struct {
		SessionID string `json:"sessionId"`
		Reason    string `json:"reason"`
	}
	_ = json.Unmarshal(will1.Payload.Data, &will1Data)
	_ = json.Unmarshal(conn1.Payload.Data, &conn1Data)
	if !simulatorUUIDv7Regex.MatchString(will1Data.SessionID) || will1Data.SessionID != conn1Data.SessionID {
		t.Fatalf("session 1 mismatch between LWT (%+v) and GATEWAY_CONNECTED (%+v)", will1Data, conn1Data)
	}
	if will1Data.Reason != "broker_connection_lost" {
		t.Fatalf("LWT reason = %q, want broker_connection_lost", will1Data.Reason)
	}

	s2, ts2, err := session.next(t2)
	if err != nil {
		t.Fatalf("session 2 next: %v", err)
	}
	if s2 == s1 || ts2.Equal(ts1) {
		t.Fatalf("reconnect must generate fresh sessionId/sessionTime: s1=%s s2=%s", s1, s2)
	}
}

func TestChangeOnUpdateAndPeriodicFullSnapshot(t *testing.T) {
	t.Parallel()

	shadow := newEdgeConfigShadow("")
	if _, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 1,
		SamplingPolicies: []GatewaySamplingPolicy{
			{
				DeviceKey:         "CHILLER-01",
				PointCode:         "chw_temp_c",
				SampleIntervalMS:  1000,
				PublishIntervalMS: 1000,
				Deadband:          0.2,
			},
		},
	}); err != nil {
		t.Fatalf("applyDesired() error = %v", err)
	}

	publisher := &MQTTPublisher{
		gatewayID: "0193f000-2000-7000-8000-000000000002",
		pointByKey: map[string]PointConfig{
			pointReference("CHILLER-01", "chw_temp"): {
				DeviceID:     "CHILLER-01",
				TelemetryKey: "chw_temp",
				PointCode:    "chw_temp_c",
				Unit:         "degC",
			},
			pointReference("CHILLER-01", "run_state"): {
				DeviceID:     "CHILLER-01",
				TelemetryKey: "run_state",
				PointCode:    "run_state",
			},
		},
		configShadow:     shadow,
		snapshotInterval: 300 * time.Second,
		lastPointStates:  map[string]lastEmittedPointState{},
	}

	t0 := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)

	// Initial sample at t0: both points emit + strict v2 JSON fields check.
	env0, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.0, Quality: "GOOD", ObservedAt: t0, Sequence: 1},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "GOOD", ObservedAt: t0, Sequence: 2},
	})
	if err != nil {
		t.Fatalf("t0 buildEnvelope() error = %v", err)
	}
	if len(env0.Payload.Devices) != 1 || len(env0.Payload.Devices[0].Points) != 2 {
		t.Fatalf("expected 2 points on initial snapshot, got %+v", env0.Payload.Devices)
	}
	raw, err := json.Marshal(env0)
	if err != nil {
		t.Fatalf("marshal env0 error = %v", err)
	}
	var rawMap map[string]any
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		t.Fatalf("unmarshal rawMap error = %v", err)
	}
	for _, forbidden := range []string{"sessionId", "eventTime"} {
		if _, found := rawMap[forbidden]; found {
			t.Fatalf("v2.0 telemetry envelope must NOT include %q outside schema: %s", forbidden, string(raw))
		}
	}

	// Within deadband & unchanged quality/state at t0+5s: MUST be suppressed.
	envSuppressed, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.15, Quality: "GOOD", ObservedAt: t0.Add(5 * time.Second), Sequence: 3},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "GOOD", ObservedAt: t0.Add(5 * time.Second), Sequence: 4},
	})
	if err != nil {
		t.Fatalf("t0+5s buildEnvelope() error = %v", err)
	}
	if len(envSuppressed.Payload.Devices) != 0 {
		t.Fatalf("expected unchanged/within-deadband points suppressed, got %+v", envSuppressed.Payload.Devices)
	}

	// Quality change with unchanged value emits immediately ONLY the quality-changed point.
	envQuality, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.15, Quality: "GOOD", ObservedAt: t0.Add(10 * time.Second), Sequence: 5},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(10 * time.Second), Sequence: 6},
	})
	if err != nil {
		t.Fatalf("t0+10s buildEnvelope() error = %v", err)
	}
	if len(envQuality.Payload.Devices) != 1 || len(envQuality.Payload.Devices[0].Points) != 1 ||
		envQuality.Payload.Devices[0].Points[0].Code != "run_state" || envQuality.Payload.Devices[0].Points[0].Quality != 1 {
		t.Fatalf("expected immediate quality-change emission for run_state only, got %+v", envQuality.Payload.Devices)
	}

	// Value change beyond deadband emits immediately ONLY chw_temp.
	envDelta, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.35, Quality: "GOOD", ObservedAt: t0.Add(15 * time.Second), Sequence: 7},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(15 * time.Second), Sequence: 8},
	})
	if err != nil {
		t.Fatalf("t0+15s buildEnvelope() error = %v", err)
	}
	if len(envDelta.Payload.Devices) != 1 || len(envDelta.Payload.Devices[0].Points) != 1 || envDelta.Payload.Devices[0].Points[0].Code != "chw_temp_c" {
		t.Fatalf("expected immediate beyond-deadband emission for chw_temp_c only, got %+v", envDelta.Payload.Devices)
	}

	// At t0+300s (unchanged since t0+15s): forced full all-point snapshot emits BOTH points.
	envFull, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.35, Quality: "GOOD", ObservedAt: t0.Add(300 * time.Second), Sequence: 9},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(300 * time.Second), Sequence: 10},
	})
	if err != nil {
		t.Fatalf("t0+300s buildEnvelope() error = %v", err)
	}
	if len(envFull.Payload.Devices) != 1 || len(envFull.Payload.Devices[0].Points) != 2 {
		t.Fatalf("expected forced periodic full snapshot with all 2 points at 300s, got %+v", envFull.Payload.Devices)
	}

	// Hot-update snapshotInterval to 45s via SetSnapshotInterval: triggers again at t0+345s.
	publisher.SetSnapshotInterval(45 * time.Second)
	envCustomFull, err := publisher.buildEnvelope([]Measurement{
		{DeviceID: "CHILLER-01", TelemetryKey: "chw_temp", Value: 7.35, Quality: "GOOD", ObservedAt: t0.Add(345 * time.Second), Sequence: 11},
		{DeviceID: "CHILLER-01", TelemetryKey: "run_state", Value: "RUNNING", Quality: "BAD", ObservedAt: t0.Add(345 * time.Second), Sequence: 12},
	})
	if err != nil {
		t.Fatalf("t0+345s buildEnvelope() error = %v", err)
	}
	if len(envCustomFull.Payload.Devices) != 1 || len(envCustomFull.Payload.Devices[0].Points) != 2 {
		t.Fatalf("expected hot-configured 45s full snapshot at +345s, got %+v", envCustomFull.Payload.Devices)
	}
}
