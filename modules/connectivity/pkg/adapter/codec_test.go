package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodecRegistryDecodesV2AndCompactV1TelemetryAndStatus(t *testing.T) {
	t.Parallel()

	registry := DefaultCodecRegistry()

	v2Payload := telemetryPayload(testGatewayA, testMessageID, 1, "METER-01", "active_power")
	decV2, err := registry.DecodeTelemetry(v2Payload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeTelemetry(v2.0) failed: %v", err)
	}
	if decV2.Envelope.SchemaVersion != "2.0" || decV2.Envelope.MessageID != testMessageID || decV2.SessionID != "" {
		t.Fatalf("unexpected v2 decoded telemetry: %+v", decV2)
	}

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

	statusPayload := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"sess-9","seq":7,"ts":"2026-11-17T12:01:00Z","online":true}`)
	st, err := registry.DecodeStatus(statusPayload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeStatus(v1.0) failed: %v", err)
	}
	if !st.Online || st.SessionID != "sess-9" || !st.HasSequence || st.Sequence != 7 || !st.HasEventTime {
		t.Fatalf("unexpected decoded status: %+v", st)
	}

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

func TestV2MinorForwardCompatibilityAndStrict20Boundaries(t *testing.T) {
	t.Parallel()

	registry := DefaultCodecRegistry()

	// 2.1 telemetry with unknown fields at top level, device level, and point level succeeds
	// while still enforcing UseNumber, trailing-token EOF, header checks, and Validate().
	v21Telemetry := []byte(`{
		"schemaVersion": "2.1",
		"messageId": "` + testMessageID + `",
		"gatewayId": "` + testGatewayA + `",
		"timestamp": 1786352400000,
		"sequence": 7,
		"traceId": "trace-v21",
		"futureTopField": {"nested": true},
		"payload": {
			"futurePayloadFlag": "enabled",
			"devices": [
				{
					"deviceId": "CH-01",
					"deviceTimestamp": 1786352400000,
					"firmwareBuild": "2026.11",
					"points": [
						{
							"code": "supply_temp",
							"value": 7.25,
							"quality": 0,
							"rawRegister": 725
						}
					]
				}
			]
		}
	}`)
	dec, err := registry.DecodeTelemetry(v21Telemetry, testGatewayA)
	if err != nil {
		t.Fatalf("expected 2.1 telemetry with unknown fields to decode, got %v", err)
	}
	if dec.Envelope.SchemaVersion != "2.1" || len(dec.Envelope.Payload.Devices) != 1 {
		t.Fatalf("unexpected 2.1 decoded telemetry envelope: %+v", dec.Envelope)
	}
	if got := dec.Envelope.Payload.Devices[0].Points[0].Value; got != json.Number("7.25") {
		if f, ok := got.(float64); !ok || f != 7.25 {
			t.Fatalf("expected numeric value 7.25 preserved, got %#v", got)
		}
	}

	// 2.1 with trailing JSON tokens MUST still be rejected.
	trailingV21 := append(append([]byte(nil), v21Telemetry...), []byte(`{"trailing":1}`)...)
	if _, err := registry.DecodeTelemetry(trailingV21, testGatewayA); err == nil {
		t.Fatal("expected trailing tokens on 2.1 telemetry to fail")
	}

	// 2.1 event with unknown fields succeeds; identical 2.0 event with unknown field fails.
	v21Event := []byte(`{
		"schemaVersion": "2.1",
		"messageId": "` + testMessageID + `",
		"gatewayId": "` + testGatewayA + `",
		"timestamp": 1786352400000,
		"sequence": 8,
		"traceId": "trace-evt-21",
		"futureEventHeader": "ok",
		"payload": {
			"eventType": "DEVICE_FAULT",
			"sourceType": "DEVICE",
			"sourceId": "CH-01",
			"eventTime": 1786352400000,
			"severity": "WARNING",
			"data": {"code": 42},
			"extraDetail": {"code": 42}
		}
	}`)
	ev, err := registry.DecodeEvent(v21Event, testGatewayA)
	if err != nil {
		t.Fatalf("expected 2.1 event with unknown fields to decode, got %v", err)
	}
	if ev.SchemaVersion != "2.1" {
		t.Fatalf("expected event schemaVersion 2.1, got %q", ev.SchemaVersion)
	}
	v20EventStrict := strings.Replace(string(v21Event), `"schemaVersion": "2.1"`, `"schemaVersion": "2.0"`, 1)
	if _, err := registry.DecodeEvent([]byte(v20EventStrict), testGatewayA); err == nil {
		t.Fatal("expected 2.0 event with unknown fields to be rejected")
	}

	// 2.1 status with unknown fields succeeds; 2.0 status with unknown fields fails.
	v21Status := []byte(`{
		"schemaVersion": "2.1",
		"messageId": "` + testMessageID + `",
		"gatewayId": "` + testGatewayA + `",
		"sessionId": "sess-21",
		"sequence": 9,
		"eventTime": "2026-11-17T12:00:00Z",
		"online": true,
		"firmware": "1.2.3"
	}`)
	st, err := registry.DecodeStatus(v21Status, testGatewayA)
	if err != nil {
		t.Fatalf("expected 2.1 status with unknown fields to decode, got %v", err)
	}
	if st.SchemaVersion != "2.1" || !st.Online || st.SessionID != "sess-21" {
		t.Fatalf("unexpected 2.1 decoded status: %+v", st)
	}
	v20StatusStrict := strings.Replace(string(v21Status), `"schemaVersion": "2.1"`, `"schemaVersion": "2.0"`, 1)
	if _, err := registry.DecodeStatus([]byte(v20StatusStrict), testGatewayA); err == nil {
		t.Fatal("expected 2.0 status with unknown fields to be rejected")
	}

	// Invalid 2.x minor formats must still be rejected across all message types.
	for _, badVer := range []string{"2.", "2.00", "2.01", "2.-1", "2.1.0", "2.abc", "3.0"} {
		badTelemetry := strings.Replace(string(v21Telemetry), `"schemaVersion": "2.1"`, `"schemaVersion": "`+badVer+`"`, 1)
		if _, err := registry.DecodeTelemetry([]byte(badTelemetry), testGatewayA); err == nil {
			t.Fatalf("expected telemetry with version %q to fail", badVer)
		}
		badStatus := strings.Replace(string(v21Status), `"schemaVersion": "2.1"`, `"schemaVersion": "`+badVer+`"`, 1)
		if _, err := registry.DecodeStatus([]byte(badStatus), testGatewayA); err == nil {
			t.Fatalf("expected status with version %q to fail", badVer)
		}
	}
}
