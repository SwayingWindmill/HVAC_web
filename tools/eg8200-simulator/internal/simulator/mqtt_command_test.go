package simulator

import (
	"testing"
	"time"
)

func TestEdgeMQTTCommandIsIdempotentAndFenced(t *testing.T) {
	config := testConfig()
	plant := NewPlant(config.Plant, time.Now().UTC())
	gatewayConfig := MQTTGatewayConfig{
		TenantID: "018f3d00-0000-7000-8000-000000000001",
		SiteID: "018f3e00-1000-7000-8000-000000000001",
		DeviceExternalIDByDeviceID: map[string]string{config.Plant.Chiller.ID: "018f3e00-4000-7000-8000-000000000001"},
	}
	handler, err := newEdgeCommandHandler(plant, gatewayConfig, "EG8200-COMMERCIAL-001")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return now }
	request := mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion,
		CommandID: "018f3e00-9000-7000-8000-000000000001",
		AttemptID: "018f3e00-9000-7000-8000-000000000002",
		ExternalDeviceID: "018f3e00-4000-7000-8000-000000000001",
		CommandCode: "SET_LOAD_LIMIT",
		Method: "setLoadLimit",
		Params: map[string]float64{"loadLimitPct": 80},
		ExecutionFence: 2,
		PayloadHash: "payload-a",
		ExpireAt: now.Add(time.Minute).Format(time.RFC3339Nano),
	}
	first := handler.evaluate(request)
	if !first.Success || first.Code != "APPLIED" {
		t.Fatalf("expected applied command, got %+v", first)
	}
	second := handler.evaluate(request)
	if second != first {
		t.Fatalf("duplicate command must return cached reply: first=%+v second=%+v", first, second)
	}
	request.CommandID = "018f3e00-9000-7000-8000-000000000003"
	request.AttemptID = "018f3e00-9000-7000-8000-000000000004"
	request.ExecutionFence = 1
	request.PayloadHash = "payload-b"
	stale := handler.evaluate(request)
	if stale.Success || stale.Code != "STALE_FENCE" {
		t.Fatalf("expected stale fence rejection, got %+v", stale)
	}
}

func TestEdgeMQTTCommandRejectsExpiredOrMismatchedMapping(t *testing.T) {
	config := testConfig()
	plant := NewPlant(config.Plant, time.Now().UTC())
	gatewayConfig := MQTTGatewayConfig{
		TenantID: "018f3d00-0000-7000-8000-000000000001",
		SiteID: "018f3e00-1000-7000-8000-000000000001",
		DeviceExternalIDByDeviceID: map[string]string{config.Plant.Chiller.ID: "018f3e00-4000-7000-8000-000000000001"},
	}
	handler, err := newEdgeCommandHandler(plant, gatewayConfig, "EG8200-COMMERCIAL-001")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return now }
	base := mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion,
		CommandID: "018f3e00-9000-7000-8000-000000000011",
		AttemptID: "018f3e00-9000-7000-8000-000000000012",
		ExternalDeviceID: "018f3e00-4000-7000-8000-000000000001",
		CommandCode: "START", Method: "start", ExecutionFence: 1, PayloadHash: "payload-c",
	}
	expired := base
	expired.ExpireAt = now.Add(-time.Second).Format(time.RFC3339Nano)
	if reply := handler.evaluate(expired); reply.Success || reply.Code != "EXPIRED" {
		t.Fatalf("expected expired rejection, got %+v", reply)
	}
	mismatched := base
	mismatched.CommandID = "018f3e00-9000-7000-8000-000000000013"
	mismatched.AttemptID = "018f3e00-9000-7000-8000-000000000014"
	mismatched.Method = "stop"
	mismatched.PayloadHash = "payload-d"
	mismatched.ExpireAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	if reply := handler.evaluate(mismatched); reply.Success || reply.Code != "COMMAND_MAPPING_INVALID" {
		t.Fatalf("expected command mapping rejection, got %+v", reply)
	}
}
