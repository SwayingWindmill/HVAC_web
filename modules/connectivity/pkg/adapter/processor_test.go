package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseMessageTopicAcceptsOnlyGatewayUplinks(t *testing.T) {
	topic, err := ParseMessageTopic(telemetryTopic(testGatewayA))
	if err != nil || topic.GatewayID != testGatewayA || topic.MessageType != MessageTypeTelemetry {
		t.Fatalf("topic=%#v err=%v", topic, err)
	}
	for _, rejected := range []string{
		"energy/v1/" + testTenantID + "/" + testSiteID + "/" + testGatewayA + "/telemetry",
		"hvac/v1/" + strings.ToUpper(testGatewayA) + "/up/telemetry",
		"hvac/v1/EG8200-COMMERCIAL-001/up/telemetry",
		"hvac/v1/" + testGatewayA + "/down/command",
		"hvac/v1/" + testGatewayA + "/up/state",
	} {
		if _, err := ParseMessageTopic(rejected); err == nil {
			t.Fatalf("topic %s was accepted", rejected)
		}
	}
}

func TestProcessorSendsRegistryResolvedIdentityToTelemetry(t *testing.T) {
	telemetry := &fakeTelemetry{}
	processor := newTestProcessor(t, newFakeIdentities(), telemetry)
	result, err := processor.Process(context.Background(), telemetryTopic(testGatewayB), telemetryPayload(testGatewayB, testMessageID, 42, "METER-01", "active_power"))
	if err != nil || result.Accepted != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	observation := telemetry.accepted()[0]
	if observation.SourceID != testGatewayB || *observation.Device != (ResolvedDevice{TenantID: testTenantID, SiteID: testSiteB, DeviceID: testDeviceID}) || observation.Point.PointID != testPointID {
		t.Fatalf("observation=%#v device=%#v", observation, observation.Device)
	}
	if observation.SourcePosition.Partition != "mqtt:"+testGatewayB+":METER-01:active_power" || observation.SourcePosition.Offset != 42 {
		t.Fatalf("source position=%#v", observation.SourcePosition)
	}
}

func TestProcessorSendsUnregisteredIdentityToTelemetryQuarantine(t *testing.T) {
	telemetry := &fakeTelemetry{}
	processor := newTestProcessor(t, newFakeIdentities(), telemetry)
	for index, message := range []struct{ sourceKey, pointCode string }{{"UNKNOWN-CHILD", "active_power"}, {"METER-01", "unregistered_point"}} {
		result, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), telemetryPayload(testGatewayA, messageID(index+1), index+1, message.sourceKey, message.pointCode))
		if err != nil || result.Quarantined != 1 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	}
	observations := telemetry.accepted()
	if observations[0].Device != nil || observations[0].Point != nil || observations[0].ExternalID != "UNKNOWN-CHILD" {
		t.Fatalf("unregistered Device observation=%#v", observations[0])
	}
	if observations[1].Device == nil || observations[1].Point != nil {
		t.Fatalf("unregistered Point observation=%#v", observations[1])
	}
}

func TestProcessorQuarantinesWhatItWillNeverAccept(t *testing.T) {
	unknownGateway := "018f3e00-4000-7000-8000-000000000999"
	for _, test := range []struct {
		name     string
		topic    string
		payload  []byte
		inactive bool
		reason   string
		tenant   string
	}{
		{name: "unknown Gateway", topic: telemetryTopic(unknownGateway), payload: telemetryPayload(unknownGateway, testMessageID, 1, "METER-01", "active_power"), reason: QuarantineGatewayUnknown},
		{name: "inactive credential", topic: telemetryTopic(testGatewayA), payload: telemetryPayload(testGatewayA, testMessageID, 1, "METER-01", "active_power"), inactive: true, reason: QuarantineGatewayCredentialInactive, tenant: testTenantID},
		{name: "envelope names another Gateway", topic: telemetryTopic(testGatewayA), payload: telemetryPayload(testGatewayB, testMessageID, 1, "METER-01", "active_power"), reason: QuarantineMessageInvalid, tenant: testTenantID},
		{name: "foreign topic", topic: "energy/v1/x/y/z/telemetry", payload: []byte(`{}`), reason: QuarantineMessageInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			identities := newFakeIdentities()
			identities.inactiveCredential[testGatewayA] = test.inactive
			telemetry := &fakeTelemetry{}
			_, err := newTestProcessor(t, identities, telemetry).Process(context.Background(), test.topic, test.payload)
			if !isPermanentMessageError(err) {
				t.Fatalf("err=%v, want permanent", err)
			}
			if len(telemetry.accepted()) != 0 || len(identities.quarantines) != 1 {
				t.Fatalf("observations=%d quarantines=%d", len(telemetry.accepted()), len(identities.quarantines))
			}
			quarantine := identities.quarantines[0]
			if quarantine.ReasonCode != test.reason || quarantine.TenantID != test.tenant || quarantine.Topic != test.topic || quarantine.PayloadSHA256() == "" {
				t.Fatalf("quarantine=%#v", quarantine)
			}
		})
	}
}

func TestProcessorRetriesWhenQuarantineEvidenceCannotBeKept(t *testing.T) {
	identities := newFakeIdentities()
	identities.quarantineErr = errors.New("database unavailable")
	unknownGateway := "018f3e00-4000-7000-8000-000000000999"
	_, err := newTestProcessor(t, identities, &fakeTelemetry{}).Process(context.Background(), telemetryTopic(unknownGateway), telemetryPayload(unknownGateway, testMessageID, 1, "METER-01", "active_power"))
	if err == nil || isPermanentMessageError(err) {
		t.Fatalf("err=%v, want transient", err)
	}
}

func TestProcessorResolvesEventDeviceStrictlyWithoutGatewayCascade(t *testing.T) {
	telemetry := &fakeTelemetry{}
	processor := newTestProcessor(t, newFakeIdentities(), telemetry)

	gatewayEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayB + `","timestamp":1786352400000,"sequence":1,"payload":{"eventType":"GATEWAY_DISCONNECTED","sourceType":"GATEWAY","sourceId":"` + testGatewayB + `","eventTime":1786352399000,"severity":"WARNING","data":{"reason":"lwt"}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayB+"/up/event", gatewayEvent); err != nil {
		t.Fatal(err)
	}

	deviceEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(2) + `","gatewayId":"` + testGatewayB + `","timestamp":1786352401000,"sequence":2,"payload":{"eventType":"DEVICE_OFFLINE","sourceType":"DEVICE","sourceId":"METER-01","eventTime":1786352400000,"severity":"WARNING","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayB+"/up/event", deviceEvent); err != nil {
		t.Fatal(err)
	}

	unknownEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(3) + `","gatewayId":"` + testGatewayB + `","timestamp":1786352402000,"sequence":3,"payload":{"eventType":"DEVICE_OFFLINE","sourceType":"DEVICE","sourceId":"UNKNOWN-METER","eventTime":1786352401000,"severity":"INFO","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayB+"/up/event", unknownEvent); err != nil {
		t.Fatal(err)
	}

	if len(telemetry.events) != 3 {
		t.Fatalf("events=%#v", telemetry.events)
	}
	if telemetry.events[0].Device == nil || telemetry.events[0].Device.DeviceID != testGatewayB || telemetry.events[0].Device.SiteID != testSiteB {
		t.Fatalf("gateway event device=%#v", telemetry.events[0].Device)
	}
	if telemetry.events[1].Device == nil || telemetry.events[1].Device.DeviceID != testDeviceID || telemetry.events[1].Device.SiteID != testSiteB {
		t.Fatalf("device event device=%#v", telemetry.events[1].Device)
	}
	if telemetry.events[2].Device != nil {
		t.Fatalf("unknown device event unexpectedly resolved: %#v", telemetry.events[2].Device)
	}
}

func newTestProcessor(t *testing.T, identities IdentityResolver, telemetry RuntimeClient) *Processor {
	t.Helper()
	processor, err := NewProcessor(identities, telemetry)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func TestProcessPrefixCoalescesContiguousValidTelemetryAndResolvesBindingsOnce(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	processor := newTestProcessor(t, identities, telemetry)

	messages := []UplinkMessage{
		{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(1), 1, "METER-01", 3)},
		{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(2), 2, "METER-01", 2)},
		{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(3), 3, "UNKNOWN-CHILD", "unregistered_point")},
	}
	outcomes, err := processor.ProcessPrefix(context.Background(), messages)
	if err != nil {
		t.Fatalf("ProcessPrefix error: %v", err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("outcomes=%d, want 3", len(outcomes))
	}
	if outcomes[0].Result.Accepted != 3 || outcomes[0].Result.PointCount != 3 || outcomes[0].Result.MessageID != messageID(1) {
		t.Fatalf("outcome[0]=%#v", outcomes[0])
	}
	if outcomes[1].Result.Accepted != 2 || outcomes[1].Result.PointCount != 2 || outcomes[1].Result.MessageID != messageID(2) {
		t.Fatalf("outcome[1]=%#v", outcomes[1])
	}
	if outcomes[2].Result.Quarantined != 1 || outcomes[2].Result.PointCount != 1 || outcomes[2].Result.MessageID != messageID(3) {
		t.Fatalf("outcome[2]=%#v", outcomes[2])
	}
	if calls := identities.bindingsCalls(); len(calls) != 1 {
		t.Fatalf("ResolveBindings calls=%d, want 1", len(calls))
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 1 || len(batches[0]) != 6 {
		t.Fatalf("batches=%d len=%d, want 1 batch of 6 observations", len(batches), len(batches[0]))
	}
}

func TestProcessPrefixStopsAtBoundaryAnd512PointLimit(t *testing.T) {
	for _, test := range []struct {
		name         string
		messages     []UplinkMessage
		wantConsumed int
		wantPoints   int
	}{
		{
			name: "stops before invalid telemetry",
			messages: []UplinkMessage{
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(1), 1, "METER-01", 2)},
				{Topic: telemetryTopic(testGatewayA), Payload: []byte(`{bad json`)},
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(3), 3, "METER-01", 2)},
			},
			wantConsumed: 1,
			wantPoints:   2,
		},
		{
			name: "stops before runtime event",
			messages: []UplinkMessage{
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(1), 1, "METER-01", 2)},
				{Topic: eventTopic(testGatewayA), Payload: eventPayload(testGatewayA, messageID(2), 2)},
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(3), 3, "METER-01", 2)},
			},
			wantConsumed: 1,
			wantPoints:   2,
		},
		{
			name: "stops before exceeding 512 points",
			messages: []UplinkMessage{
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(1), 1, "METER-01", 300)},
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(2), 2, "METER-01", 212)},
				{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(3), 3, "METER-01", 1)},
			},
			wantConsumed: 2,
			wantPoints:   512,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			telemetry := &fakeTelemetry{}
			outcomes, err := newTestProcessor(t, newFakeIdentities(), telemetry).ProcessPrefix(context.Background(), test.messages)
			if err != nil {
				t.Fatalf("ProcessPrefix error: %v", err)
			}
			if len(outcomes) != test.wantConsumed {
				t.Fatalf("consumed=%d, want %d", len(outcomes), test.wantConsumed)
			}
			batches := telemetry.recordedBatches()
			if len(batches) != 1 || len(batches[0]) != test.wantPoints {
				t.Fatalf("batches=%d points=%d, want 1 batch of %d points", len(batches), len(batches[0]), test.wantPoints)
			}
		})
	}
}

func TestProcessPrefixChunksSingleEnvelopeOver512Points(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	processor := newTestProcessor(t, identities, telemetry)

	messages := []UplinkMessage{
		{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(1), 1, "METER-01", 1100)},
		{Topic: telemetryTopic(testGatewayA), Payload: multiPointTelemetryPayload(testGatewayA, messageID(2), 2, "METER-01", 2)},
	}
	outcomes, err := processor.ProcessPrefix(context.Background(), messages)
	if err != nil {
		t.Fatalf("ProcessPrefix error: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Result.PointCount != 1100 || outcomes[0].Result.Accepted != 1100 {
		t.Fatalf("outcomes=%#v", outcomes)
	}
	if calls := identities.bindingsCalls(); len(calls) != 1 || len(calls[0]) != 1100 {
		t.Fatalf("ResolveBindings calls=%d points=%d", len(calls), len(calls[0]))
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 3 || len(batches[0]) != 512 || len(batches[1]) != 512 || len(batches[2]) != 76 {
		t.Fatalf("chunk sizes=%v, want [512 512 76]", []int{len(batches[0]), len(batches[1]), len(batches[2])})
	}
}

func TestProcessorSplitsLargeBytePayloadsBelowBatchByteLimit(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	processor := newTestProcessor(t, identities, telemetry)

	largeValue := strings.Repeat("a", 40<<10)
	messages := make([]UplinkMessage, 90)
	for index := range messages {
		payload, err := json.Marshal(map[string]any{
			"schemaVersion": "2.0",
			"messageId":     messageID(index + 1),
			"gatewayId":     testGatewayA,
			"timestamp":     1786352400000 + int64(index),
			"sequence":      index + 1,
			"replay":        false,
			"payload": map[string]any{
				"devices": []map[string]any{{
					"deviceId":        "METER-01",
					"deviceTimestamp": 1786352399000 + int64(index),
					"points": []map[string]any{{
						"code":    "status_text",
						"value":   largeValue,
						"quality": 0,
					}},
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		messages[index] = UplinkMessage{Topic: telemetryTopic(testGatewayA), Payload: payload}
	}

	var allOutcomes []ProcessingOutcome
	remaining := messages
	for len(remaining) > 0 {
		outcomes, err := processor.ProcessPrefix(context.Background(), remaining)
		if err != nil {
			t.Fatalf("ProcessPrefix error with %d remaining: %v", len(remaining), err)
		}
		allOutcomes = append(allOutcomes, outcomes...)
		remaining = remaining[len(outcomes):]
	}
	if len(allOutcomes) != len(messages) {
		t.Fatalf("processed=%d, want %d", len(allOutcomes), len(messages))
	}
	batches := telemetry.recordedBatches()
	if len(batches) < 2 {
		t.Fatalf("expected >1 byte-bounded batch for ~3.6MiB payload, got %d", len(batches))
	}
	for batchIndex, batch := range batches {
		encoded, err := json.Marshal(observationBatchRequest{Observations: batch})
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > MaxObservationBatchBytes {
			t.Fatalf("batch[%d] serialized length=%d exceeds MaxObservationBatchBytes=%d", batchIndex, len(encoded), MaxObservationBatchBytes)
		}
	}
}
