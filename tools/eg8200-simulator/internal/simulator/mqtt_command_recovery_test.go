package simulator

import (
	"testing"
	"time"
)

func TestEdgeMQTTCommandRestartDoesNotReplayMayExecuteRecord(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, now)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	wireID := "018f3e00-4000-7000-8000-000000000001"
	queueDirectory := t.TempDir()
	gatewayConfig := MQTTGatewayConfig{
		TenantID: "018f3d00-0000-7000-8000-000000000001", SiteID: "018f3e00-1000-7000-8000-000000000001",
		QueueDirectory: queueDirectory, DeviceExternalIDByDeviceID: map[string]string{config.Plant.Chiller.ID: wireID},
	}
	ledgerPath := queueDirectory + "/command-execution-records.json"
	commandID := "018f3e00-9000-7000-8000-000000000021"
	if err := persistEdgeCommandLedger(ledgerPath, map[string]edgeCommandRecord{
		commandID: {DeviceID: config.Plant.Chiller.ID, PayloadHash: "payload-recovery", ExecutionFence: 7, State: edgeCommandMayExecute},
	}); err != nil {
		t.Fatal(err)
	}

	handler, err := newEdgeCommandHandler(runtime, gatewayConfig, "EG8200-COMMERCIAL-001", testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	request := mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion, MessageID: "018f3e00-9000-7000-8000-000000000022", CommandID: commandID,
		IssuedAt: now.Add(-time.Second).UnixMilli(), ExpireAt: now.Add(time.Minute).UnixMilli(), DeviceID: wireID,
		CommandCode: "SET_LOAD_LIMIT", Params: map[string]float64{"loadLimitPct": 75}, ExecutionFence: 7, PayloadHash: "payload-recovery",
	}
	unknown := handler.evaluate(request)
	if unknown.EdgeStatus != "FAILED" || unknown.ReasonCode == nil || *unknown.ReasonCode != "EDGE_OUTCOME_UNKNOWN" {
		t.Fatalf("restart must not replay MAY_EXECUTE command, got %+v", unknown)
	}
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["loadLimitPct"]; got == 75.0 {
		t.Fatal("restart recovery duplicated physical effect")
	}

	request.CommandID = "018f3e00-9000-7000-8000-000000000023"
	request.MessageID = "018f3e00-9000-7000-8000-000000000024"
	request.PayloadHash = "payload-new"
	stale := handler.evaluate(request)
	if stale.EdgeStatus != "REJECTED" || stale.ReasonCode == nil || *stale.ReasonCode != "STALE_FENCE" {
		t.Fatalf("recovered fence must reject same-fence different command, got %+v", stale)
	}
}
