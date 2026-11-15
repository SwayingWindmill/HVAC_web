package simulator

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func testMQTTEvidenceSpool(t testing.TB) *mqttEvidenceSpool {
	t.Helper()
	spool, err := newMQTTEvidenceSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return spool
}

func evaluateMQTTCommandWithEdgeCycle(t *testing.T, handler *edgeCommandHandler, runtime *EdgeControlRuntime, request mqttCommandEnvelope, cycleAt time.Time) mqttCommandReply {
	t.Helper()
	replies := make(chan mqttCommandReply, 1)
	go func() {
		replies <- handler.evaluate(request)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		pending := len(runtime.pendingByAddress)
		runtime.mu.Unlock()
		if pending > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MQTT command did not enter Edge intent queue")
		}
		time.Sleep(time.Millisecond)
	}
	runtime.RunCycle(context.Background(), cycleAt)
	select {
	case reply := <-replies:
		return reply
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MQTT command reply")
		return mqttCommandReply{}
	}
}

func TestEdgeMQTTCommandIsIdempotentAndFenced(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, now)
	edgeRuntime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	wireID := config.Plant.Chiller.ID
	gatewayConfig := MQTTGatewayConfig{
		QueueDirectory: t.TempDir(),
	}
	handler, err := newEdgeCommandHandler(edgeRuntime, gatewayConfig, config.Plant.DeviceIDs(), testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	request := mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion,
		MessageID:     "018f3e00-9000-7000-8000-000000000002",
		CommandID:     "018f3e00-9000-7000-8000-000000000001",
		IssuedAt:      now.Add(-time.Second).UnixMilli(), ExpireAt: now.Add(time.Minute).UnixMilli(),
		DeviceID: wireID, CommandCode: "SET_LOAD_LIMIT",
		Params: map[string]float64{"loadLimitPct": 80},
		Policy: mqttCommandPolicy{RequiresReadback: true, VerificationWindowMS: 5000}, ExecutionFence: 2,
		PayloadHash: "payload-a",
	}
	first := evaluateMQTTCommandWithEdgeCycle(t, handler, edgeRuntime, request, now)
	if first.EdgeStatus != "EXECUTED" || first.ReasonCode != nil || first.ExecutionEvidence == nil ||
		first.ExecutionEvidence.Applied == nil || first.ExecutionEvidence.Applied.Number == nil || *first.ExecutionEvidence.Applied.Number != 80 ||
		first.ExecutionEvidence.WinnerControllerID != "cloud-command-intent" || first.ExecutionEvidence.Cycle == 0 {
		t.Fatalf("expected governed Edge execution evidence, got %+v", first)
	}
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["loadLimitPct"]; got != 80.0 {
		t.Fatalf("Edge runtime did not apply chiller load limit: %v", got)
	}

	second := handler.evaluate(request)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("duplicate command must return cached reply without re-execution: first=%+v second=%+v", first, second)
	}
	restarted, err := newEdgeCommandHandler(edgeRuntime, gatewayConfig, config.Plant.DeviceIDs(), testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = handler.now
	if replay := restarted.evaluate(request); !reflect.DeepEqual(replay, first) {
		t.Fatalf("terminal Edge reply must survive restart: first=%+v replay=%+v", first, replay)
	}
	request.CommandID = "018f3e00-9000-7000-8000-000000000003"
	request.MessageID = "018f3e00-9000-7000-8000-000000000004"
	request.ExecutionFence = 1
	request.PayloadHash = "payload-b"
	stale := handler.evaluate(request)
	if stale.EdgeStatus != "REJECTED" || stale.ReasonCode == nil || *stale.ReasonCode != "STALE_FENCE" {
		t.Fatalf("expected stale fence rejection, got %+v", stale)
	}
}

func TestEdgeMQTTCommandRejectsExpiredOrMismatchedMapping(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, now)
	edgeRuntime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	wireID := config.Plant.Chiller.ID
	gatewayConfig := MQTTGatewayConfig{
		QueueDirectory: t.TempDir(),
	}
	handler, err := newEdgeCommandHandler(edgeRuntime, gatewayConfig, config.Plant.DeviceIDs(), testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	base := mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion,
		MessageID:     "018f3e00-9000-7000-8000-000000000012",
		CommandID:     "018f3e00-9000-7000-8000-000000000011",
		IssuedAt:      now.Add(-2 * time.Second).UnixMilli(),
		DeviceID:      wireID,
		CommandCode:   "START", ExecutionFence: 1, PayloadHash: "payload-c",
	}
	expired := base
	expired.ExecutionFence = 10
	expired.ExpireAt = now.Add(-time.Second).UnixMilli()
	if reply := handler.evaluate(expired); reply.EdgeStatus != "EXPIRED" || reply.ReasonCode == nil || *reply.ReasonCode != "EXPIRED" {
		t.Fatalf("expected expired rejection, got %+v", reply)
	}
	mismatched := base
	mismatched.CommandID = "018f3e00-9000-7000-8000-000000000013"
	mismatched.MessageID = "018f3e00-9000-7000-8000-000000000014"
	mismatched.CommandCode = "NOT_A_CAPABILITY"
	mismatched.PayloadHash = "payload-d"
	mismatched.ExpireAt = now.Add(time.Minute).UnixMilli()
	if reply := handler.evaluate(mismatched); reply.EdgeStatus != "REJECTED" || reply.ReasonCode == nil || *reply.ReasonCode != "COMMAND_MAPPING_INVALID" {
		t.Fatalf("expected command mapping rejection, got %+v", reply)
	}
}

func configWithUnitSequencePoints(t *testing.T) Config {
	t.Helper()
	config := loadGeneratedCentralPlantConfig(t)
	config.Points = append(config.Points,
		PointConfig{
			PointID: "01910000-0000-7000-8000-500000000091", DeviceID: config.Plant.Chiller.ID,
			SubjectType: "ASSET", SubjectID: "equipment-chiller-01",
			SourceKey: "startUnitSequence", TelemetryKey: "chiller.command.start_unit_sequence", PointCode: "start_unit_sequence",
			Name: "一键启机顺序", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true,
			SampleInterval: "1s", PublishInterval: "2s", StaleAfter: "10s",
			SourceMetadata: map[string]any{
				"controlKind": "ACTION", "capability": "START_UNIT_SEQUENCE",
				"capabilityRevision": "capability:start-unit-sequence:v1",
				"feedbackPointKey":   "run_state", "feedbackSourceKey": "runState",
			},
		},
		PointConfig{
			PointID: "01910000-0000-7000-8000-500000000092", DeviceID: config.Plant.Chiller.ID,
			SubjectType: "ASSET", SubjectID: "equipment-chiller-01",
			SourceKey: "stopUnitSequence", TelemetryKey: "chiller.command.stop_unit_sequence", PointCode: "stop_unit_sequence",
			Name: "一键停机顺序", PointType: "COMMAND", ValueType: "BOOLEAN", Writable: true,
			SampleInterval: "1s", PublishInterval: "2s", StaleAfter: "10s",
			SourceMetadata: map[string]any{
				"controlKind": "ACTION", "capability": "STOP_UNIT_SEQUENCE",
				"capabilityRevision": "capability:stop-unit-sequence:v1",
				"feedbackPointKey":   "run_state", "feedbackSourceKey": "runState",
			},
		},
	)
	return config
}

func TestEdgeMQTTCommandAcceptsRouteMetadataAndUnitSequence(t *testing.T) {
	config := configWithUnitSequencePoints(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, now)
	edgeRuntime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: t.TempDir()}, config.Plant.DeviceIDs(), testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	pulseJSON := `{
		"schemaVersion":"2.0",
		"messageId":"018f3e00-9000-7000-8000-000000000101",
		"commandId":"018f3e00-9000-7000-8000-000000000102",
		"issuedAt":` + jsonNumber(now.Add(-time.Second).UnixMilli()) + `,
		"expireAt":` + jsonNumber(now.Add(25*time.Second).UnixMilli()) + `,
		"deviceId":"` + config.Plant.Chiller.ID + `",
		"commandCode":"STOP_UNIT_SEQUENCE",
		"params":{},
		"policy":{"requiresReadback":true,"verificationWindowMs":600000},
		"executionFence":1,
		"payloadHash":"payload-stop-seq",
		"pointCode":"stop_unit_sequence",
		"actuationMode":"UNIT_SEQUENCE",
		"channel":"unit-a",
		"coilAddress":16,
		"pulseDurationMs":500
	}`
	stopEnv, err := decodeMQTTCommandEnvelope([]byte(pulseJSON))
	if err != nil {
		t.Fatalf("038 route metadata must decode cleanly: %v", err)
	}
	stopReply := evaluateMQTTCommandWithEdgeCycle(t, handler, edgeRuntime, stopEnv, now)
	if stopReply.EdgeStatus != "EXECUTED" || stopReply.ReasonCode != nil {
		t.Fatalf("STOP_UNIT_SEQUENCE reply=%+v want EXECUTED", stopReply)
	}
	plant.Tick(5 * time.Minute)
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["runState"]; got != "STOPPED" {
		t.Fatalf("STOP_UNIT_SEQUENCE did not stop chiller: %v", got)
	}

	startJSON := `{
		"schemaVersion":"2.0",
		"messageId":"018f3e00-9000-7000-8000-000000000103",
		"commandId":"018f3e00-9000-7000-8000-000000000104",
		"issuedAt":` + jsonNumber(now.Add(-time.Second).UnixMilli()) + `,
		"expireAt":` + jsonNumber(now.Add(25*time.Second).UnixMilli()) + `,
		"deviceId":"` + config.Plant.Chiller.ID + `",
		"commandCode":"START_UNIT_SEQUENCE",
		"params":{},
		"policy":{"requiresReadback":true,"verificationWindowMs":600000},
		"executionFence":2,
		"payloadHash":"payload-start-seq",
		"pointCode":"start_unit_sequence",
		"actuationMode":"UNIT_SEQUENCE",
		"channel":"unit-a",
		"pulseDurationMs":500
	}`
	startEnv, err := decodeMQTTCommandEnvelope([]byte(startJSON))
	if err != nil {
		t.Fatalf("START_UNIT_SEQUENCE envelope must decode cleanly: %v", err)
	}
	startReply := evaluateMQTTCommandWithEdgeCycle(t, handler, edgeRuntime, startEnv, now.Add(time.Second))
	if startReply.EdgeStatus != "EXECUTED" || startReply.ReasonCode != nil {
		t.Fatalf("START_UNIT_SEQUENCE reply=%+v want EXECUTED", startReply)
	}
	plant.Tick(20 * time.Second)
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["runState"]; got != "RUNNING" {
		t.Fatalf("START_UNIT_SEQUENCE did not bring plant back online: %v", got)
	}
}

func jsonNumber(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
