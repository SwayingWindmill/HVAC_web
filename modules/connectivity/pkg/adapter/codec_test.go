package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodecRegistryDecodesV2TelemetryAndStatusAndRejectsCompactV1(t *testing.T) {
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

	v2StatusPayload := []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","sessionId":"sess-9","sequence":7,"eventTime":"2026-11-17T12:01:00Z","online":true}`)
	st, err := registry.DecodeStatus(v2StatusPayload, testGatewayA)
	if err != nil {
		t.Fatalf("DecodeStatus(v2.0) failed: %v", err)
	}
	if !st.Online || st.SchemaVersion != "2.0" || st.SessionID != "sess-9" || !st.HasSequence || st.Sequence != 7 || !st.HasEventTime {
		t.Fatalf("unexpected decoded status: %+v", st)
	}

	v1TelemetryPayload := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"sess-01","seq":42,"ts":"2026-11-17T12:00:00Z","replay":true,"devs":{"CH-01":{"pts":{"active_power":128.5}}}}`)
	if _, err := registry.DecodeTelemetry(v1TelemetryPayload, testGatewayA); err == nil {
		t.Fatal("expected compact v1.0 telemetry payload to be rejected")
	}

	v1StatusPayload := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"sess-9","seq":7,"ts":"2026-11-17T12:01:00Z","online":true}`)
	if _, err := registry.DecodeStatus(v1StatusPayload, testGatewayA); err == nil {
		t.Fatal("expected compact v1.0 status payload to be rejected")
	}

	legacyStatusPayload := []byte(`{"online":false,"ts":1786352400000}`)
	if _, err := registry.DecodeStatus(legacyStatusPayload, testGatewayA); err == nil {
		t.Fatal("expected legacy unversioned status payload to be rejected")
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
		{"truncated json", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","gatewayId":`)},
		{"literal NaN", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400000,"sequence":1,"payload":{"devices":[{"deviceId":"d1","deviceTimestamp":1786352400000,"points":[{"code":"p1","value":NaN,"quality":0}]}]}}`)},
		{"float overflow 1e309", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400000,"sequence":1,"payload":{"devices":[{"deviceId":"d1","deviceTimestamp":1786352400000,"points":[{"code":"p1","value":1e309,"quality":0}]}]}}`)},
		{"null point value", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400000,"sequence":1,"payload":{"devices":[{"deviceId":"d1","deviceTimestamp":1786352400000,"points":[{"code":"p1","value":null,"quality":0}]}]}}`)},
		{"out-of-bounds ts", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":281474976710657,"sequence":1,"payload":{"devices":[{"deviceId":"d1","deviceTimestamp":1786352400000,"points":[{"code":"p1","value":1,"quality":0}]}]}}`)},
		{"gateway mismatch", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayB + `","timestamp":1786352400000,"sequence":1,"payload":{"devices":[{"deviceId":"d1","deviceTimestamp":1786352400000,"points":[{"code":"p1","value":1,"quality":0}]}]}}`)},
		{"empty devices", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400000,"sequence":1,"payload":{"devices":[]}}`)},
		{"unsupported version", MessageTypeTelemetry, []byte(`{"schemaVersion":"1.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400000,"sequence":1,"payload":{"devices":[{"deviceId":"d1","deviceTimestamp":1786352400000,"points":[{"code":"p1","value":1,"quality":0}]}]}}`)},
		{"oversized payload", MessageTypeTelemetry, []byte(`{"schemaVersion":"2.0","pad":"` + strings.Repeat("a", maximumEnvelopeBytes) + `"}`)},
		{"status missing online", MessageTypeStatus, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","sessionId":"s1","sequence":1,"eventTime":1786352400000}`)},
		{"status string online", MessageTypeStatus, []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","sessionId":"s1","sequence":1,"eventTime":1786352400000,"online":"false"}`)},
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
