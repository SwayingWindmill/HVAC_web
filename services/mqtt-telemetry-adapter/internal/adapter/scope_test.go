package adapter

import (
	"context"
	"strings"
	"testing"
)

func TestProcessorRejectsGatewayOutsideConfiguredTenantSite(t *testing.T) {
	client := &fakeRuntimeClient{}
	processor, err := NewProcessor(
		"018f3e00-0000-7000-8000-000000000101",
		[]GatewayScopeConfig{{GatewayID: "EG8200-COMMERCIAL-001", TenantID: testTenantID, SiteID: testSiteID}},
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongSite := "018f2e00-1000-7000-8000-000000000099"
	topic := "energy/v1/" + testTenantID + "/" + wrongSite + "/EG8200-COMMERCIAL-001/telemetry"
	payload := `{"schemaVersion":1,"messageId":"` + testMessageID + `","tenantId":"` + testTenantID + `","siteId":"` + wrongSite + `","gatewayId":"EG8200-COMMERCIAL-001","publishedAt":"2026-08-10T09:00:00Z","replay":false,"devices":[{"externalDeviceId":"METER-01","points":[{"telemetryKey":"active_power","value":126.4,"valueType":"NUMBER","unit":"kW","quality":"GOOD","sampledAt":"2026-08-10T08:59:59Z","sequence":42}]}]}`
	_, err = processor.Process(context.Background(), topic, []byte(payload))
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("scope error=%v", err)
	}
	if len(client.observations) != 0 {
		t.Fatalf("unauthorized scope reached S2: %#v", client.observations)
	}
}
