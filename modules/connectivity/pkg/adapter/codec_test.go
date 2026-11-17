package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodecRegistryDecodesV2AndCompactV1TelemetryAndStatus(t *testing.T) {
	t.Parallel()

	registry := DefaultCodecRegistry()

	// Standard v2.0 telemetry
	v2Payload := telemetryPayload(testGatewayA, testMessageID, 1, "METER-01", "active_power")
	decV2, err := registry.DecodeTelemetry(v2Payload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeTelemetry(v2.0) failed: %v", err)
	}
	if decV2.Envelope.SchemaVersion != "2.0" || decV2.Envelope.MessageID != testMessageID || decV2.SessionID != "" {
		t.Fatalf("unexpected v2 decoded telemetry: %+v", decV2)
	}

	// Compact v1.0 batch telemetry with RFC3339 timestamps, replay, GOOD/BAD quality
	v1Payload := []byte(`{
		"v": "1.0",
		"gw": "` + testGatewayA + `",
		"sid": "sess-01",
		"seq": 42,
		"ts": "2026-11-17T12:00:00Z",
		"replay": true,
		"devs": {
			"CH-01": {
				"ts": "2026-11-17T11:59:59.500Z",
				"pts": {
					"active_power": 128.5,
					"running": true
				},
				"q": {
					"active_power": "GOOD",
					"running": "BAD"
				}
			},
			"CH-02": {
				"pts": {
					"supply_temp": 7.2
				}
			}
		}
	}`)
	decV1, err := registry.DecodeTelemetry(v1Payload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeTelemetry(v1.0) failed: %v", err)
	}
	if decV1.SessionID != "sess-01" {
		t.Fatalf("expected sid=sess-01, got %q", decV1.SessionID)
	}
	if !decV1.Envelope.Replay || decV1.Envelope.Sequence != 42 || decV1.Envelope.GatewayID != testGatewayA {
		t.Fatalf("unexpected normalized v1 envelope metadata: %+v", decV1.Envelope)
	}
	if !uuidV7Pattern.MatchString(decV1.Envelope.MessageID) {
		t.Fatalf("expected valid UUIDv7 messageId fallback, got %q", decV1.Envelope.MessageID)
	}
	// Deterministic messageId for identical frame
	decV1Again, err := registry.DecodeTelemetry(v1Payload, testGatewayA)
	if err != nil || decV1Again.Envelope.MessageID != decV1.Envelope.MessageID {
		t.Fatalf("expected deterministic UUIDv7 generation, got %q vs %q (err=%v)", decV1Again.Envelope.MessageID, decV1.Envelope.MessageID, err)
	}
	if len(decV1.Envelope.Payload.Devices) != 2 {
		t.Fatalf("expected 2 devices sorted CH-01, CH-02, got %d", len(decV1.Envelope.Payload.Devices))
	}
	ch1 := decV1.Envelope.Payload.Devices[0]
	ch2 := decV1.Envelope.Payload.Devices[1]
	if ch1.DeviceID != "CH-01" || ch2.DeviceID != "CH-02" {
		t.Fatalf("expected sorted device order CH-01, CH-02, got %s, %s", ch1.DeviceID, ch2.DeviceID)
	}
	if ch2.DeviceTimestamp != decV1.Envelope.Timestamp {
		t.Fatalf("expected CH-02 to inherit envelope ts=%d, got %d", decV1.Envelope.Timestamp, ch2.DeviceTimestamp)
	}
	if ch1.Points[0].Code != "active_power" || ch1.Points[0].Quality != 0 || ch1.Points[1].Code != "running" || ch1.Points[1].Quality != 1 {
		t.Fatalf("unexpected compact point quality mapping: %+v", ch1.Points)
	}

	// Compact v1.0 status frame
	statusPayload := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"sess-9","seq":7,"ts":"2026-11-17T12:01:00Z","online":true}`)
	st, err := registry.DecodeStatus(statusPayload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeStatus(v1.0) failed: %v", err)
	}
	if !st.Online || st.SessionID != "sess-9" || !st.HasSequence || st.Sequence != 7 || !st.HasEventTime {
		t.Fatalf("unexpected decoded status: %+v", st)
	}

	// Legacy status frame omitting sid/seq/v
	legacyPayload := []byte(`{"online":false,"ts":1786352400000}`)
	legacyStatus, err := registry.DecodeStatus(legacyPayload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeStatus(legacy) failed: %v", err)
	}
	if legacyStatus.Online || legacyStatus.HasSequence || legacyStatus.SessionID != "" || legacyStatus.GatewayID != testGatewayA || legacyStatus.EventTime != 1786352400000 {
		t.Fatalf("unexpected decoded legacy status: %+v", legacyStatus)
	}
}

func TestCodecRejectsNaNInfBoundsAndMalformedPayloadsWithoutPanic(t *testing.T) {
	t.Parallel()

	registry := DefaultCodecRegistry()

	cases := []struct {
		name        string
		messageType string
		payload     []byte
	}{
		{"empty", MessageTypeTelemetry, nil},
		{"null literal", MessageTypeTelemetry, []byte("null")},
		{"array literal", MessageTypeTelemetry, []byte("[]")},
		{"number literal", MessageTypeStatus, []byte("123")},
		{"truncated json", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":`)},
		{"literal NaN", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":NaN}}}}`)},
		{"float overflow 1e309", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1e309}}}}`)},
		{"null point value", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":null}}}}`)},
		{"object point value", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":{"nested":1}}}}}`)},
		{"wrong pts type", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":[1,2]}}}`)},
		{"null device", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":null}}`)},
		{"null quality value", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1},"q":{"p1":null}}}}`)},
		{"bad quality string", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1},"q":{"p1":"BOGUS"}}}}`)},
		{"orphan quality key", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1},"q":{"other":"GOOD"}}}}`)},
		{"negative seq", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":-1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1}}}}`)},
		{"out-of-bounds ts", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":281474976710657,"devs":{"d1":{"pts":{"p1":1}}}}`)},
		{"bad ts string", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":"not-a-time","devs":{"d1":{"pts":{"p1":1}}}}`)},
		{"gateway mismatch", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayB + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1}}}}`)},
		{"empty devs", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{}}`)},
		{"unsupported version", MessageTypeTelemetry, []byte(`{"v":"9.9","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"devs":{"d1":{"pts":{"p1":1}}}}`)},
		{"unknown field", MessageTypeTelemetry, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","seq":1,"ts":1786352400000,"extra":true,"devs":{"d1":{"pts":{"p1":1}}}}`)},
		{"oversized payload", MessageTypeTelemetry, []byte(`{"v":"1.0","pad":"` + strings.Repeat("a", maximumEnvelopeBytes) + `"}`)},
		{"status missing online", MessageTypeStatus, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"s1","seq":1,"ts":1786352400000}`)},
		{"status string online", MessageTypeStatus, []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"s1","seq":1,"ts":1786352400000,"online":"false"}`)},
		{"status null payload", MessageTypeStatus, []byte("null")},
		{"v2 strict extra field", MessageTypeTelemetry, func() []byte {
			raw := map[string]any{}
			_ = json.Unmarshal(telemetryPayload(testGatewayA, testMessageID, 1, "METER-01", "active_power"), &raw)
			raw["sessionId"] = "unexpected"
			b, _ := json.Marshal(raw)
			return b
		}()},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			switch tc.messageType {
			case MessageTypeTelemetry:
				if _, err := registry.DecodeTelemetry(tc.payload, testGatewayA); err == nil {
					t.Fatalf("expected error for invalid telemetry case %s", tc.name)
				}
			case MessageTypeStatus:
				if _, err := registry.DecodeStatus(tc.payload, testGatewayA); err == nil {
					t.Fatalf("expected error for invalid status case %s", tc.name)
				}
			}
		})
	}
}
