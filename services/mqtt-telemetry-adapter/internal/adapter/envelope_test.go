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
	payload := `{"schemaVersion":1,"messageId":"` + testMessageID + `","tenantId":"` + testTenantID + `","siteId":"` + testSiteID + `","gatewayId":"EG8200-COMMERCIAL-001","publishedAt":"2026-08-10T09:00:00Z","replay":false,"devices":[{"externalDeviceId":"METER-01","points":[{"telemetryKey":"hvac_meter.energy","value":1234.5,"valueType":"NUMBER","unit":"kWh","quality":"GOOD","sampledAt":"2026-08-10T08:59:59Z","sequence":42}]}]}`
	envelope, err := DecodeTelemetryEnvelope([]byte(payload), scope)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.GatewayID != "EG8200-COMMERCIAL-001" || len(envelope.Devices) != 1 || len(envelope.Devices[0].Points) != 1 {
		t.Fatalf("envelope=%#v", envelope)
	}
}

func TestDecodeTelemetryEnvelopeRejectsScopeDriftAndUnsupportedQuality(t *testing.T) {
	topic := "energy/v1/" + testTenantID + "/" + testSiteID + "/EG8200-COMMERCIAL-001/telemetry"
	scope, err := ParseTelemetryTopic(topic)
	if err != nil {
		t.Fatal(err)
	}
	base := `{"schemaVersion":1,"messageId":"` + testMessageID + `","tenantId":"` + testTenantID + `","siteId":"` + testSiteID + `","gatewayId":"EG8200-COMMERCIAL-001","publishedAt":"2026-08-10T09:00:00Z","replay":false,"devices":[{"externalDeviceId":"METER-01","points":[{"telemetryKey":"active_power","value":126.4,"valueType":"NUMBER","unit":"kW","quality":"GOOD","sampledAt":"2026-08-10T08:59:59Z","sequence":42}]}]}`
	if _, err := DecodeTelemetryEnvelope([]byte(strings.Replace(base, testSiteID, "018f2e00-1000-7000-8000-000000000099", 1)), scope); err == nil {
		t.Fatal("scope drift was accepted")
	}
	if _, err := DecodeTelemetryEnvelope([]byte(strings.Replace(base, `"quality":"GOOD"`, `"quality":"TIMEOUT"`, 1)), scope); err == nil {
		t.Fatal("unsupported edge quality was accepted")
	}
}
