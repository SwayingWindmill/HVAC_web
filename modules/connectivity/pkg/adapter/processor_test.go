package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/observability"
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

func TestProcessorQuarantineTruncatesUTF8OnRuneBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  string
		maxLen int
		want   int
	}{
		{name: "3-byte Chinese crosses 1024 at byte 1022", input: strings.Repeat("a", 1022) + "中文测试信息", maxLen: 1024, want: 1022},
		{name: "3-byte Chinese crosses 1024 at byte 1023", input: strings.Repeat("a", 1023) + "中文测试信息", maxLen: 1024, want: 1023},
		{name: "4-byte emoji crosses 1024 at byte 1021", input: strings.Repeat("a", 1021) + "\U0001F600测试", maxLen: 1024, want: 1021},
		{name: "exact rune fit at 1024", input: strings.Repeat("a", 1021) + "中" + "后面的内容", maxLen: 1024, want: 1024},
		{name: "invalid UTF-8 bytes sanitized before truncation", input: strings.Repeat("a", 1020) + "\xff\xfe中文", maxLen: 1024, want: 1023},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateUTF8(tc.input, tc.maxLen)
			if len(got) > tc.maxLen || len(got) != tc.want {
				t.Fatalf("len(truncateUTF8)=%d, want %d (<= %d)", len(got), tc.want, tc.maxLen)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncateUTF8 produced invalid UTF-8: %x", got)
			}
		})
	}

	identities := newFakeIdentities()
	processor := newTestProcessor(t, identities, &fakeTelemetry{})
	longField := strings.Repeat("k", 1003) + strings.Repeat("测", 20)
	payload := []byte(`{"` + longField + `":1}`)
	_, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), payload)
	if !isPermanentMessageError(err) {
		t.Fatalf("err=%v, want permanent quarantine error", err)
	}
	quarantines := identities.snapshotQuarantines()
	if len(quarantines) != 1 {
		t.Fatalf("quarantines=%d, want 1", len(quarantines))
	}
	detail := quarantines[0].Detail
	if len(detail) > 1024 || len(detail) < 1000 {
		t.Fatalf("quarantine Detail length=%d, want within [1000, 1024]", len(detail))
	}
	if !utf8.ValidString(detail) {
		t.Fatalf("quarantine Detail is invalid UTF-8: %x", detail)
	}
}

func TestProcessorInvokesGatewayConnectHookOnlyOnConnectEvents(t *testing.T) {
	telemetry := &fakeTelemetry{}
	var hooked []string
	processor, err := NewProcessor(
		newFakeIdentities(),
		telemetry,
		WithGatewayConnectHook(func(_ context.Context, gatewayID string) {
			hooked = append(hooked, gatewayID)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	disconnectEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayB + `","timestamp":1786352400000,"sequence":1,"payload":{"eventType":"GATEWAY_DISCONNECTED","sourceType":"GATEWAY","sourceId":"` + testGatewayB + `","eventTime":1786352399000,"severity":"INFO","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayB+"/up/event", disconnectEvent); err != nil {
		t.Fatal(err)
	}
	deviceEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(2) + `","gatewayId":"` + testGatewayB + `","timestamp":1786352401000,"sequence":2,"payload":{"eventType":"CONNECTED","sourceType":"DEVICE","sourceId":"METER-01","eventTime":1786352400000,"severity":"INFO","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayB+"/up/event", deviceEvent); err != nil {
		t.Fatal(err)
	}
	if len(hooked) != 0 {
		t.Fatalf("unexpected connect hook calls before GATEWAY_CONNECTED: %v", hooked)
	}

	connectEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(3) + `","gatewayId":"` + testGatewayB + `","timestamp":1786352402000,"sequence":3,"payload":{"eventType":"GATEWAY_CONNECTED","sourceType":"GATEWAY","sourceId":"` + testGatewayB + `","eventTime":1786352401000,"severity":"INFO","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayB+"/up/event", connectEvent); err != nil {
		t.Fatal(err)
	}
	if len(hooked) != 1 || hooked[0] != testGatewayB {
		t.Fatalf("hooked=%v, want [%s]", hooked, testGatewayB)
	}
}

func TestProcessorInvokesGatewayPresenceHook(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	var states []string
	processor, err := NewProcessor(
		identities,
		telemetry,
		WithGatewayPresenceHook(func(gatewayID string, online bool) {
			state := gatewayID + ":offline"
			if online {
				state = gatewayID + ":online"
			}
			states = append(states, state)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), telemetryPayload(testGatewayA, messageID(1), 1, "METER-01", "active_power")); err != nil {
		t.Fatal(err)
	}
	disconnectEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(2) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352401000,"sequence":2,"payload":{"eventType":"LWT","sourceType":"GATEWAY","sourceId":"` + testGatewayA + `","eventTime":1786352400000,"severity":"WARNING","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/event", disconnectEvent); err != nil {
		t.Fatal(err)
	}
	connectEvent := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(3) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352402000,"sequence":3,"payload":{"eventType":"GATEWAY_CONNECTED","sourceType":"GATEWAY","sourceId":"` + testGatewayA + `","eventTime":1786352401000,"severity":"INFO","data":{}}}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/event", connectEvent); err != nil {
		t.Fatal(err)
	}

	want := []string{testGatewayA + ":online", testGatewayA + ":offline", testGatewayA + ":online"}
	if len(states) != len(want) {
		t.Fatalf("states=%v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("states=%v, want %v", states, want)
		}
	}
}

func TestProcessorGeneratesRootTraceIDAndPropagatesToTelemetryClient(t *testing.T) {
	exporter := &observability.MemoryExporter{}
	tracer := observability.NewTracer("mqtt-telemetry-adapter", exporter)

	var obsTraceparent string
	var obsTraceIDs []string
	var eventTraceparent string
	var eventTraceID string

	server, defaultClient := newTestTelemetryServer(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case sourceObservationBatchPath:
			obsTraceparent = request.Header.Get("traceparent")
			var payload observationBatchRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			receipts := make([]ObservationReceipt, len(payload.Observations))
			for i, obs := range payload.Observations {
				obsTraceIDs = append(obsTraceIDs, obs.TraceID)
				receipts[i] = ObservationReceipt{Status: "ACCEPTED", DeviceID: testDeviceID}
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(observationBatchResponse{Receipts: receipts})
		case mqttRuntimeEventPath:
			eventTraceparent = request.Header.Get("traceparent")
			var evidence RuntimeEventEvidence
			if err := json.NewDecoder(request.Body).Decode(&evidence); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			eventTraceID = evidence.TraceID
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"accepted":true}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
	defer server.Close()
	defaultClient.tracer = tracer

	processor, err := NewProcessor(newFakeIdentities(), defaultClient, WithProcessorTracer(tracer))
	if err != nil {
		t.Fatal(err)
	}

	outcomes, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{
		{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(1), 1, "METER-01", "active_power")},
		{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(2), 2, "METER-01", "reactive_power")},
	})
	if err != nil {
		t.Fatalf("ProcessPrefix telemetry failed: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes=%d, want 2", len(outcomes))
	}
	rootTraceID := outcomes[0].Result.TraceID
	if !traceIDPattern.MatchString(rootTraceID) || rootTraceID == zeroTraceID {
		t.Fatalf("expected valid 32-hex generated traceId, got %q", rootTraceID)
	}
	if outcomes[1].Result.TraceID != rootTraceID {
		t.Fatalf("coalesced message[1] TraceID=%q, want inherited root %q", outcomes[1].Result.TraceID, rootTraceID)
	}
	if len(obsTraceIDs) != 2 || obsTraceIDs[0] != rootTraceID || obsTraceIDs[1] != rootTraceID {
		t.Fatalf("observation traceIds=%v, want [%s %s]", obsTraceIDs, rootTraceID, rootTraceID)
	}
	if !strings.HasPrefix(obsTraceparent, "00-"+rootTraceID+"-") {
		t.Fatalf("HTTP traceparent=%q does not carry root traceId %q", obsTraceparent, rootTraceID)
	}

	evtRes, err := processor.Process(context.Background(), eventTopic(testGatewayA), eventPayload(testGatewayA, messageID(3), 3))
	if err != nil {
		t.Fatalf("Process event failed: %v", err)
	}
	if !traceIDPattern.MatchString(evtRes.TraceID) || evtRes.TraceID == zeroTraceID {
		t.Fatalf("expected valid 32-hex event traceId, got %q", evtRes.TraceID)
	}
	if eventTraceID != evtRes.TraceID {
		t.Fatalf("RuntimeEventEvidence.TraceID=%q, want %q", eventTraceID, evtRes.TraceID)
	}
	if !strings.HasPrefix(eventTraceparent, "00-"+evtRes.TraceID+"-") {
		t.Fatalf("event HTTP traceparent=%q does not carry event traceId %q", eventTraceparent, evtRes.TraceID)
	}

	spans := exporter.Spans()
	if len(spans) != 2 {
		t.Fatalf("recorded spans=%d, want 2", len(spans))
	}
}

func TestProcessorQuarantineAttributesTenantOnRegisteredGatewayInvalidSubtopic(t *testing.T) {
	unknownGateway := "018f3e00-4000-7000-8000-000000000999"
	for _, tc := range []struct {
		name       string
		gatewayID  string
		inactive   bool
		wantTenant string
	}{
		{name: "active registered gateway", gatewayID: testGatewayA, wantTenant: testTenantID},
		{name: "inactive registered gateway", gatewayID: testGatewayA, inactive: true, wantTenant: testTenantID},
		{name: "unknown gateway", gatewayID: unknownGateway, wantTenant: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identities := newFakeIdentities()
			identities.inactiveCredential[testGatewayA] = tc.inactive
			processor := newTestProcessor(t, identities, &fakeTelemetry{})
			topic := "hvac/v1/" + tc.gatewayID + "/up/state"
			_, err := processor.Process(context.Background(), topic, []byte(`{"raw":"state"}`))
			if !isPermanentMessageError(err) {
				t.Fatalf("err=%v, want permanent quarantine error", err)
			}
			quarantines := identities.snapshotQuarantines()
			if len(quarantines) != 1 {
				t.Fatalf("quarantines=%d, want 1", len(quarantines))
			}
			q := quarantines[0]
			if q.ReasonCode != QuarantineMessageInvalid || q.GatewayID != tc.gatewayID || q.TenantID != tc.wantTenant {
				t.Fatalf("quarantine=%#v, want reason=%s gateway=%s tenant=%q", q, QuarantineMessageInvalid, tc.gatewayID, tc.wantTenant)
			}
		})
	}
}

func TestProcessorPacketHookCapturesTelemetryEventAndQuarantine(t *testing.T) {
	type capturedPacket struct {
		direction string
		channel   string
		gatewayID string
		topic     string
		traceID   string
		payload   string
	}
	var captured []capturedPacket

	tracer := observability.NewTracer("mqtt-telemetry-adapter", nil)
	processor, err := NewProcessor(
		newFakeIdentities(),
		&fakeTelemetry{},
		WithProcessorTracer(tracer),
		WithProcessorPacketHook(func(direction, channel, gatewayID, topic, traceID string, payload []byte) {
			captured = append(captured, capturedPacket{
				direction: direction,
				channel:   channel,
				gatewayID: gatewayID,
				topic:     topic,
				traceID:   traceID,
				payload:   string(payload),
			})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	const explicitTrace = "abcdef0123456789abcdef0123456789"
	m2Payload := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(2) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352400001,"sequence":2,"traceId":"` + explicitTrace + `","replay":false,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":1786352399001,"points":[{"code":"reactive_power","value":10.5,"quality":0,"unit":"kvar"}]}]}}`)

	outcomes, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{
		{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(1), 1, "METER-01", "active_power")},
		{Topic: telemetryTopic(testGatewayA), Payload: m2Payload},
	})
	if err != nil || len(outcomes) != 2 {
		t.Fatalf("ProcessPrefix telemetry outcomes=%#v err=%v", outcomes, err)
	}

	evtOutcome, err := processor.Process(context.Background(), eventTopic(testGatewayA), eventPayload(testGatewayA, messageID(3), 3))
	if err != nil {
		t.Fatalf("Process event failed: %v", err)
	}

	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/state", []byte(`{"bogus":1}`)); !isPermanentMessageError(err) {
		t.Fatalf("expected quarantine error for invalid subtopic, got %v", err)
	}

	if len(captured) != 4 {
		t.Fatalf("captured packets=%d, want 4: %#v", len(captured), captured)
	}
	if captured[0].direction != "UPLINK" || captured[0].channel != "TELEMETRY" || captured[0].gatewayID != testGatewayA || captured[0].traceID != outcomes[0].Result.TraceID || captured[0].traceID == "" {
		t.Fatalf("captured[0]=%#v, want TELEMETRY with generated traceId %q", captured[0], outcomes[0].Result.TraceID)
	}
	if captured[1].direction != "UPLINK" || captured[1].channel != "TELEMETRY" || captured[1].gatewayID != testGatewayA || captured[1].traceID != explicitTrace {
		t.Fatalf("captured[1]=%#v, want TELEMETRY with explicit traceId %q", captured[1], explicitTrace)
	}
	if captured[2].direction != "UPLINK" || captured[2].channel != "EVENT" || captured[2].gatewayID != testGatewayA || captured[2].topic != eventTopic(testGatewayA) || captured[2].traceID != evtOutcome.TraceID || captured[2].traceID == "" {
		t.Fatalf("captured[2]=%#v, want EVENT with traceId %q", captured[2], evtOutcome.TraceID)
	}
	if captured[3].direction != "UPLINK" || captured[3].channel != "QUARANTINE" || captured[3].gatewayID != testGatewayA || captured[3].topic != "hvac/v1/"+testGatewayA+"/up/state" || captured[3].traceID != "" || captured[3].payload != `{"bogus":1}` {
		t.Fatalf("captured[3]=%#v, want QUARANTINE", captured[3])
	}
}

func TestProcessorCompactV1BatchAndStatusLifecycleFencing(t *testing.T) {
	t.Parallel()

	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	var presenceEvents []bool
	var connectedCalls []string

	processor, err := NewProcessor(
		identities,
		telemetry,
		WithGatewayPresenceHook(func(_ string, online bool) {
			presenceEvents = append(presenceEvents, online)
		}),
		WithGatewayConnectHook(func(_ context.Context, gatewayID string) {
			connectedCalls = append(connectedCalls, gatewayID)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Process compact v1.0 batch telemetry across two messages in one prefix.
	v1A := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"s2","seq":1,"ts":"2026-11-17T12:00:00Z","replay":true,"devs":{"METER-01":{"pts":{"active_power":55.5},"q":{"active_power":"GOOD"}}}}`)
	v1B := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"s2","seq":2,"ts":1786352402000,"replay":false,"devs":{"METER-01":{"pts":{"reactive_power":12.1},"q":{"reactive_power":"BAD"}}}}`)
	outcomes, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{
		{Topic: telemetryTopic(testGatewayA), Payload: v1A},
		{Topic: telemetryTopic(testGatewayA), Payload: v1B},
	})
	if err != nil {
		t.Fatalf("ProcessPrefix compact v1 failed: %v", err)
	}
	if len(outcomes) != 2 || outcomes[0].Result.Accepted != 1 || outcomes[1].Result.Accepted != 1 {
		t.Fatalf("unexpected outcomes: %+v", outcomes)
	}
	accepted := telemetry.accepted()
	if len(accepted) != 2 || accepted[0].WireQuality != 0 || accepted[1].WireQuality != 1 {
		t.Fatalf("expected GOOD->0 and BAD->1 wire quality in accepted observations, got %+v", accepted)
	}

	// Valid s2 connect lifecycle event on /up/event sets session "s2" online.
	connectS2 := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(10) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352403000,"sequence":1,"payload":{"eventType":"GATEWAY_CONNECTED","sourceType":"GATEWAY","sourceId":"` + testGatewayA + `","eventTime":1786352403000,"severity":"INFO","data":{"sessionId":"s2"}}}`)
	if _, err := processor.Process(context.Background(), eventTopic(testGatewayA), connectS2); err != nil {
		t.Fatal(err)
	}

	// Stale LWT from old session "s1" even with later keepalive timestamp MUST suppress presence(false).
	staleLWTS1 := []byte(`{"schemaVersion":"2.0","messageId":"` + messageID(11) + `","gatewayId":"` + testGatewayA + `","timestamp":1786352410000,"sequence":2,"payload":{"eventType":"LWT","sourceType":"GATEWAY","sourceId":"` + testGatewayA + `","eventTime":1786352410000,"severity":"WARNING","data":{"sessionId":"s1"}}}`)
	if _, err := processor.Process(context.Background(), eventTopic(testGatewayA), staleLWTS1); err != nil {
		t.Fatal(err)
	}

	// Superseded connect replay on /up/status with older timestamp or old session MUST suppress hooks.
	staleStatusS1 := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"s1","seq":3,"ts":1786352401000,"online":true}`)
	if _, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/status", staleStatusS1); err != nil {
		t.Fatal(err)
	}

	// Valid s2 disconnect on /up/status MUST trigger presence(false).
	validOfflineS2 := []byte(`{"v":"1.0","gw":"` + testGatewayA + `","sid":"s2","seq":2,"ts":1786352412000,"online":false}`)
	res, err := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/status", validOfflineS2)
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageType != MessageTypeStatus {
		t.Fatalf("expected MessageType=%s, got %q", MessageTypeStatus, res.MessageType)
	}

	if len(connectedCalls) != 1 || connectedCalls[0] != testGatewayA {
		t.Fatalf("expected exactly 1 connect hook call (stale replay suppressed), got %v", connectedCalls)
	}
	wantPresence := []bool{true, true, false}
	if len(presenceEvents) != len(wantPresence) {
		t.Fatalf("presence=%v, want %v (stale LWT must be suppressed)", presenceEvents, wantPresence)
	}
	for i := range wantPresence {
		if presenceEvents[i] != wantPresence[i] {
			t.Fatalf("presence[%d]=%v, want %v", i, presenceEvents[i], wantPresence[i])
		}
	}
}

func telemetryPayloadWithTimestamps(gatewayID, messageID string, sequence int, replay bool, deviceTS int64, sourceKey, pointCode string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2.0","messageId":%q,"gatewayId":%q,"timestamp":%d,"sequence":%d,"replay":%t,"payload":{"devices":[{"deviceId":%q,"deviceTimestamp":%d,"points":[{"code":%q,"value":126.4,"quality":0,"unit":"kW"}]}]}}`,
		messageID, gatewayID, deviceTS, sequence, replay, sourceKey, deviceTS, pointCode))
}

func TestProcessorEdgeReplayAutoBackfillAndRebootEpochDecoupling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	telemetry := &fakeTelemetry{}
	processor, err := NewProcessor(
		newFakeIdentities(),
		telemetry,
		WithAutoBackfillLagThreshold(10*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	processor.now = func() time.Time { return now }

	// Fresh live message (5m lag <= 10m): EdgeReplay=false, epoch 0 partition.
	res1, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(1), 10, false, now.Add(-5*time.Minute).UnixMilli(), "METER-01", "active_power"))
	if err != nil || res1.Accepted != 1 {
		t.Fatalf("msg 1 failed: res=%+v err=%v", res1, err)
	}
	// Exact boundary lag (10m == 10m): strictly > threshold is required, stays live.
	res2, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(2), 11, false, now.Add(-10*time.Minute).UnixMilli(), "METER-01", "active_power"))
	if err != nil || res2.Accepted != 1 {
		t.Fatalf("msg 2 failed: res=%+v err=%v", res2, err)
	}
	// Stale non-replay reading (10m+1ms lag > 10m) with regressed seq=3 promotes to
	// EdgeReplay without bumping live epoch.
	staleTS := now.Add(-10*time.Minute - time.Millisecond)
	res3, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(3), 3, false, staleTS.UnixMilli(), "METER-01", "active_power"))
	if err != nil || res3.Accepted != 1 {
		t.Fatalf("msg 3 failed: res=%+v err=%v", res3, err)
	}
	// Explicit replay=true with fresh timestamp and low seq=1 keeps live epoch untouched.
	freshReplayTS := now.Add(-1 * time.Second)
	res4, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(4), 1, true, freshReplayTS.UnixMilli(), "METER-01", "active_power"))
	if err != nil || res4.Accepted != 1 {
		t.Fatalf("msg 4 failed: res=%+v err=%v", res4, err)
	}
	// Next live reading seq=12 stays on epoch 0 because prior replays did not reset live sequence.
	res5, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(5), 12, false, now.UnixMilli(), "METER-01", "active_power"))
	if err != nil || res5.Accepted != 1 {
		t.Fatalf("msg 5 failed: res=%+v err=%v", res5, err)
	}
	// Gateway reboots and sends live seq=1 (< 12): allocates new monotonic epoch partition.
	res6, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(6), 1, false, now.UnixMilli(), "METER-01", "active_power"))
	if err != nil || res6.Accepted != 1 {
		t.Fatalf("msg 6 failed: res=%+v err=%v", res6, err)
	}
	// Subsequent live reading seq=2 stays on the new epoch partition.
	res7, err := processor.Process(context.Background(), telemetryTopic(testGatewayA),
		telemetryPayloadWithTimestamps(testGatewayA, messageID(7), 2, false, now.UnixMilli(), "METER-01", "active_power"))
	if err != nil || res7.Accepted != 1 {
		t.Fatalf("msg 7 failed: res=%+v err=%v", res7, err)
	}

	obs := telemetry.accepted()
	if len(obs) != 7 {
		t.Fatalf("accepted=%d, want 7", len(obs))
	}
	liveBasePartition := "mqtt:" + testGatewayA + ":METER-01:active_power"
	replayPartition := "mqtt-replay:" + testGatewayA + ":METER-01:active_power"
	wantEpochPartition := fmt.Sprintf("mqtt:%s:e%d:METER-01:active_power", testGatewayA, now.UnixMilli())

	if obs[0].EdgeReplay || obs[0].SourcePosition.Partition != liveBasePartition || obs[0].SourcePosition.Offset != 10 {
		t.Fatalf("obs[0]=%+v", obs[0])
	}
	if obs[1].EdgeReplay || obs[1].SourcePosition.Partition != liveBasePartition || obs[1].SourcePosition.Offset != 11 {
		t.Fatalf("obs[1]=%+v", obs[1])
	}
	if !obs[2].EdgeReplay || obs[2].SourcePosition.Partition != replayPartition || obs[2].SourcePosition.Offset != staleTS.UnixMicro() {
		t.Fatalf("obs[2]=%+v", obs[2])
	}
	if !obs[3].EdgeReplay || obs[3].SourcePosition.Partition != replayPartition || obs[3].SourcePosition.Offset != freshReplayTS.UnixMicro() {
		t.Fatalf("obs[3]=%+v", obs[3])
	}
	if obs[4].EdgeReplay || obs[4].SourcePosition.Partition != liveBasePartition || obs[4].SourcePosition.Offset != 12 {
		t.Fatalf("obs[4]=%+v", obs[4])
	}
	if obs[5].EdgeReplay || obs[5].SourcePosition.Partition != wantEpochPartition || obs[5].SourcePosition.Offset != 1 {
		t.Fatalf("obs[5]=%+v, want partition=%s offset=1", obs[5], wantEpochPartition)
	}
	if obs[6].EdgeReplay || obs[6].SourcePosition.Partition != wantEpochPartition || obs[6].SourcePosition.Offset != 2 {
		t.Fatalf("obs[6]=%+v, want partition=%s offset=2", obs[6], wantEpochPartition)
	}
}

func TestProcessorPoisonPill400BatchSplitAndQuarantine(t *testing.T) {
	t.Parallel()

	t.Run("coalesced batch isolates 400 poison pill and commits valid neighbors", func(t *testing.T) {
		identities := newFakeIdentities()
		telemetry := &fakeTelemetry{
			failHook: func(_ int, batch []Observation) error {
				for _, obs := range batch {
					if obs.TelemetryKey == "poison_point" {
						return &RequestInvalidError{Label: "S2 observation batch", StatusCode: http.StatusBadRequest}
					}
				}
				return nil
			},
		}
		processor := newTestProcessor(t, identities, telemetry)

		messages := []UplinkMessage{
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(1), 1, "METER-01", "active_power")},
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(2), 2, "METER-01", "poison_point")},
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(3), 3, "METER-01", "reactive_power")},
		}
		outcomes, err := processor.ProcessPrefix(context.Background(), messages)
		if err != nil {
			t.Fatalf("ProcessPrefix error: %v", err)
		}
		if len(outcomes) != 3 {
			t.Fatalf("outcomes=%d, want 3", len(outcomes))
		}
		if outcomes[0].Err != nil || outcomes[0].Result.Accepted != 1 || outcomes[0].Result.MessageID != messageID(1) {
			t.Fatalf("outcome[0]=%+v", outcomes[0])
		}
		if !isPermanentMessageError(outcomes[1].Err) {
			t.Fatalf("outcome[1].Err=%v, want permanent quarantine error", outcomes[1].Err)
		}
		if outcomes[2].Err != nil || outcomes[2].Result.Accepted != 1 || outcomes[2].Result.MessageID != messageID(3) {
			t.Fatalf("outcome[2]=%+v", outcomes[2])
		}
		quarantines := identities.snapshotQuarantines()
		if len(quarantines) != 1 || quarantines[0].ReasonCode != QuarantineMessageInvalid || !strings.Contains(string(quarantines[0].Payload), "poison_point") {
			t.Fatalf("quarantines=%+v, want 1 MESSAGE_INVALID for poison_point", quarantines)
		}
		accepted := telemetry.accepted()
		if len(accepted) != 2 || accepted[0].TelemetryKey != "active_power" || accepted[1].TelemetryKey != "reactive_power" {
			t.Fatalf("accepted=%+v, want active_power and reactive_power", accepted)
		}
	})

	t.Run("single message 400 quarantines without duplicate HTTP call", func(t *testing.T) {
		identities := newFakeIdentities()
		var calls int
		telemetry := &fakeTelemetry{
			failHook: func(_ int, _ []Observation) error {
				calls++
				return &RequestInvalidError{Label: "S2 observation batch", StatusCode: http.StatusBadRequest}
			},
		}
		processor := newTestProcessor(t, identities, telemetry)
		outcomes, err := processor.ProcessPrefix(context.Background(), []UplinkMessage{
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(1), 1, "METER-01", "poison_point")},
		})
		if err != nil {
			t.Fatalf("ProcessPrefix error: %v", err)
		}
		if len(outcomes) != 1 || !isPermanentMessageError(outcomes[0].Err) {
			t.Fatalf("outcomes=%+v, want 1 permanent error", outcomes)
		}
		if calls != 1 {
			t.Fatalf("AcceptObservationBatch calls=%d, want 1", calls)
		}
		if len(identities.snapshotQuarantines()) != 1 {
			t.Fatalf("quarantines=%d, want 1", len(identities.snapshotQuarantines()))
		}
	})

	t.Run("transient 503 mid-fallback preserves already-handled prefix", func(t *testing.T) {
		identities := newFakeIdentities()
		telemetry := &fakeTelemetry{
			failHook: func(_ int, batch []Observation) error {
				for _, obs := range batch {
					if obs.TelemetryKey == "poison_point" {
						return &RequestInvalidError{Label: "S2 observation batch", StatusCode: http.StatusBadRequest}
					}
					if obs.TelemetryKey == "transient_point" {
						return errors.New("503 unavailable")
					}
				}
				return nil
			},
		}
		processor := newTestProcessor(t, identities, telemetry)
		messages := []UplinkMessage{
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(1), 1, "METER-01", "active_power")},
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(2), 2, "METER-01", "poison_point")},
			{Topic: telemetryTopic(testGatewayA), Payload: telemetryPayload(testGatewayA, messageID(3), 3, "METER-01", "transient_point")},
		}
		outcomes, err := processor.ProcessPrefix(context.Background(), messages)
		if err != nil {
			t.Fatalf("expected handled prefix outcomes without error, got err=%v", err)
		}
		if len(outcomes) != 2 || outcomes[0].Err != nil || !isPermanentMessageError(outcomes[1].Err) {
			t.Fatalf("outcomes=%+v, want 2 outcomes [ok, permanent]", outcomes)
		}
		if len(telemetry.accepted()) != 1 || len(identities.snapshotQuarantines()) != 1 {
			t.Fatalf("accepted=%d quarantines=%d, want 1 and 1", len(telemetry.accepted()), len(identities.snapshotQuarantines()))
		}
	})

	t.Run("event 400 quarantines as MESSAGE_INVALID", func(t *testing.T) {
		identities := newFakeIdentities()
		telemetry := &eventErrorTelemetry{fakeTelemetry: &fakeTelemetry{}, err: &RequestInvalidError{Label: "MQTT evidence", StatusCode: http.StatusBadRequest}}
		processor := newTestProcessor(t, identities, telemetry)
		_, err := processor.Process(context.Background(), eventTopic(testGatewayA), eventPayload(testGatewayA, messageID(1), 1))
		if !isPermanentMessageError(err) {
			t.Fatalf("err=%v, want permanent quarantine error", err)
		}
		quarantines := identities.snapshotQuarantines()
		if len(quarantines) != 1 || quarantines[0].ReasonCode != QuarantineMessageInvalid {
			t.Fatalf("quarantines=%+v, want 1 MESSAGE_INVALID", quarantines)
		}
	})
}

type eventErrorTelemetry struct {
	*fakeTelemetry
	err error
}

func (e *eventErrorTelemetry) AcceptRuntimeEvent(_ context.Context, _ RuntimeEventEvidence) error {
	return e.err
}
