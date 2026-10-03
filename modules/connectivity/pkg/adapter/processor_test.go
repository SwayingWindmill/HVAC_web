package adapter

import (
	"context"
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

func newTestProcessor(t *testing.T, identities IdentityResolver, telemetry RuntimeClient) *Processor {
	t.Helper()
	processor, err := NewProcessor(identities, telemetry)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}
