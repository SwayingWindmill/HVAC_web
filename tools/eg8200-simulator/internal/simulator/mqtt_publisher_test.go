package simulator

import (
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
