package adapter

import (
	"context"
	"testing"
)

type fakeRuntimeClient struct {
	observations []Observation
	statuses     []string
}

func (client *fakeRuntimeClient) AcceptObservation(_ context.Context, observation Observation) (ObservationReceipt, error) {
	client.observations = append(client.observations, observation)
	status := "ACCEPTED"
	if len(client.statuses) >= len(client.observations) {
		status = client.statuses[len(client.observations)-1]
	}
	return ObservationReceipt{Status: status}, nil
}

func TestProcessorMapsMQTTPointToStableS2SourcePosition(t *testing.T) {
	client := &fakeRuntimeClient{statuses: []string{"ACCEPTED", "DUPLICATE"}}
	processor, err := NewProcessor("018f3e00-0000-7000-8000-000000000101", []GatewayScopeConfig{{GatewayID: "EG8200-COMMERCIAL-001", TenantID: testTenantID, SiteID: testSiteID}}, client)
	if err != nil {
		t.Fatal(err)
	}
	topic := "energy/v1/" + testTenantID + "/" + testSiteID + "/EG8200-COMMERCIAL-001/telemetry"
	payload := []byte(`{"schemaVersion":1,"messageId":"` + testMessageID + `","tenantId":"` + testTenantID + `","siteId":"` + testSiteID + `","gatewayId":"EG8200-COMMERCIAL-001","publishedAt":"2026-08-10T09:00:00Z","replay":true,"devices":[{"externalDeviceId":"METER-01","points":[{"telemetryKey":"active_power","value":126.4,"valueType":"NUMBER","unit":"kW","quality":"GOOD","sampledAt":"2026-08-10T08:59:59Z","sequence":42},{"telemetryKey":"hvac_meter.energy","value":1234.5,"valueType":"NUMBER","unit":"kWh","quality":"GOOD","sampledAt":"2026-08-10T08:59:59Z","sequence":7}]}]}`)
	result, err := processor.Process(context.Background(), topic, payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.PointCount != 2 || result.Accepted != 1 || result.Duplicate != 1 || !result.Replay {
		t.Fatalf("result=%#v", result)
	}
	if len(client.observations) != 2 {
		t.Fatalf("observations=%#v", client.observations)
	}
	first := client.observations[0]
	if first.SourcePath != "PUSH" || first.ExternalID != "METER-01" || first.TelemetryKey != "active_power" || first.SourcePosition.Partition != "mqtt:EG8200-COMMERCIAL-001:METER-01:active_power" || first.SourcePosition.Offset != 42 || !uuidV7Pattern.MatchString(first.SourcePosition.EventID) {
		t.Fatalf("first observation=%#v", first)
	}
	again := &fakeRuntimeClient{}
	processorAgain, _ := NewProcessor("018f3e00-0000-7000-8000-000000000101", []GatewayScopeConfig{{GatewayID: "EG8200-COMMERCIAL-001", TenantID: testTenantID, SiteID: testSiteID}}, again)
	if _, err := processorAgain.Process(context.Background(), topic, payload); err != nil {
		t.Fatal(err)
	}
	if again.observations[0].SourcePosition.EventID != first.SourcePosition.EventID {
		t.Fatalf("event id changed across retry: %s != %s", again.observations[0].SourcePosition.EventID, first.SourcePosition.EventID)
	}
}
