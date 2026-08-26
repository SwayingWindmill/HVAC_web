package adapter

import (
	"strings"
	"testing"
)

const (
	testTenantID  = "018f2d00-0000-7000-8000-000000000001"
	testSiteID    = "018f2e00-1000-7000-8000-000000000001"
	testMessageID = "0198a100-0000-7000-8000-000000000001"
)

func TestDecodeTelemetryEnvelopeBindsTopicScope(t *testing.T) {
	topic := "energy/v1/" + testTenantID + "/" + testSiteID + "/EG8200-COMMERCIAL-001/telemetry"
	scope, err := ParseTelemetryTopic(topic)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"schemaVersion":"1.0","messageId":"` + testMessageID + `","gatewayId":"EG8200-COMMERCIAL-001","timestamp":1786352400000,"sequence":42,"replay":false,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":1786352399000,"points":[{"code":"active_power","value":1234.5,"quality":0,"unit":"kW"}]}]}}`
	envelope, err := DecodeTelemetryEnvelope([]byte(payload), scope)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.GatewayID != "EG8200-COMMERCIAL-001" || len(envelope.Payload.Devices) != 1 || len(envelope.Payload.Devices[0].Points) != 1 {
		t.Fatalf("envelope=%#v", envelope)
	}
}

func TestDecodeTelemetryEnvelopeRejectsGatewayScopeDrift(t *testing.T) {
	topic := "energy/v1/" + testTenantID + "/" + testSiteID + "/EG8200-COMMERCIAL-001/telemetry"
	scope, err := ParseTelemetryTopic(topic)
	if err != nil {
		t.Fatal(err)
	}
	base := `{"schemaVersion":"1.0","messageId":"` + testMessageID + `","gatewayId":"EG8200-COMMERCIAL-001","timestamp":1786352400000,"sequence":42,"replay":false,"payload":{"devices":[{"deviceId":"METER-01","deviceTimestamp":1786352399000,"points":[{"code":"active_power","value":126.4,"quality":0,"unit":"kW"}]}]}}`
	if _, err := DecodeTelemetryEnvelope([]byte(strings.Replace(base, "EG8200-COMMERCIAL-001", "EG8200-COMMERCIAL-002", 1)), scope); err == nil {
		t.Fatal("gateway scope drift was accepted")
	}
}
