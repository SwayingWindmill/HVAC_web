package simulator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestEdgeMQTTCommandWALAppendLegacyImportCrashAndCorruptFailClosed(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	deviceIDs := config.Plant.DeviceIDs()
	if len(deviceIDs) < 2 {
		t.Fatalf("expected at least 2 devices in test plant, got %d", len(deviceIDs))
	}
	chillerID := config.Plant.Chiller.ID
	secondID := deviceIDs[1]
	if secondID == chillerID {
		secondID = deviceIDs[0]
	}

	t.Run("wal_append_creates_wal_only_and_recovers_may_execute", func(t *testing.T) {
		plant := NewPlant(config.Plant, config.Scenario, now)
		edgeRuntime, err := NewEdgeControlRuntime(config, plant)
		if err != nil {
			t.Fatal(err)
		}
		queueDir := t.TempDir()
		handler, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: queueDir}, deviceIDs, testMQTTEvidenceSpool(t))
		if err != nil {
			t.Fatal(err)
		}
		handler.now = func() time.Time { return now }

		req := mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-wal-1",
			CommandID:      "cmd-wal-1",
			IssuedAt:       now.Add(-time.Second).UnixMilli(),
			ExpireAt:       now.Add(time.Minute).UnixMilli(),
			DeviceID:       chillerID,
			CommandCode:    "SET_LOAD_LIMIT",
			Params:         map[string]float64{"loadLimitPct": 70},
			Policy:         mqttCommandPolicy{RequiresReadback: true, VerificationWindowMS: 5000},
			ExecutionFence: 4,
			PayloadHash:    "hash-wal-1",
		}
		reply := evaluateMQTTCommandWithEdgeCycle(t, handler, edgeRuntime, req, now)
		if reply.EdgeStatus != "EXECUTED" {
			t.Fatalf("expected EXECUTED, got %+v", reply)
		}
		if _, err := os.Stat(filepath.Join(queueDir, "command-execution-records.json")); !os.IsNotExist(err) {
			t.Fatalf("legacy json file must not be created on WAL path, stat err=%v", err)
		}
		walRaw, err := os.ReadFile(filepath.Join(queueDir, "command-execution-records.wal"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(walRaw)), "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], edgeCommandMayExecute) || !strings.Contains(lines[1], edgeCommandTerminal) {
			t.Fatalf("expected MAY_EXECUTE and TERMINAL WAL lines, got:\n%s", string(walRaw))
		}

		// Simulate crash right after MAY_EXECUTE append for cmd-wal-crash.
		if err := appendEdgeCommandWALEntry(filepath.Join(queueDir, "command-execution-records.wal"), edgeCommandWALEntry{
			Type:      edgeCommandWALRecordTypeRecord,
			CommandID: "cmd-wal-crash",
			Record: &edgeCommandRecord{
				DeviceID:       secondID,
				PayloadHash:    "hash-crash",
				ExecutionFence: 9,
				State:          edgeCommandMayExecute,
			},
		}); err != nil {
			t.Fatal(err)
		}

		restarted, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: queueDir}, deviceIDs, testMQTTEvidenceSpool(t))
		if err != nil {
			t.Fatal(err)
		}
		restarted.now = handler.now

		if cached := restarted.evaluate(req); !reflect.DeepEqual(cached, reply) {
			t.Fatalf("replayed terminal reply=%+v want=%+v", cached, reply)
		}
		unknown := restarted.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-wal-crash-retry",
			CommandID:      "cmd-wal-crash",
			IssuedAt:       now.Add(-time.Second).UnixMilli(),
			ExpireAt:       now.Add(time.Minute).UnixMilli(),
			DeviceID:       secondID,
			CommandCode:    "START",
			ExecutionFence: 9,
			PayloadHash:    "hash-crash",
		})
		if unknown.EdgeStatus != "FAILED" || unknown.ReasonCode == nil || *unknown.ReasonCode != "EDGE_OUTCOME_UNKNOWN" {
			t.Fatalf("expected EDGE_OUTCOME_UNKNOWN for MAY_EXECUTE WAL crash, got %+v", unknown)
		}
		stale := restarted.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-wal-stale",
			CommandID:      "cmd-wal-other",
			IssuedAt:       now.Add(-time.Second).UnixMilli(),
			ExpireAt:       now.Add(time.Minute).UnixMilli(),
			DeviceID:       secondID,
			CommandCode:    "START",
			ExecutionFence: 9,
			PayloadHash:    "hash-other",
		})
		if stale.EdgeStatus != "REJECTED" || stale.ReasonCode == nil || *stale.ReasonCode != "STALE_FENCE" {
			t.Fatalf("expected STALE_FENCE on fence 9, got %+v", stale)
		}
	})

	t.Run("legacy_json_imports_and_compacts_into_wal", func(t *testing.T) {
		plant := NewPlant(config.Plant, config.Scenario, now)
		edgeRuntime, err := NewEdgeControlRuntime(config, plant)
		if err != nil {
			t.Fatal(err)
		}
		queueDir := t.TempDir()
		legacyPath := filepath.Join(queueDir, "command-execution-records.json")
		cachedReply := mqttCommandReply{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-legacy-reply",
			CommandID:      "cmd-legacy-term",
			EventTime:      now.UnixMilli(),
			EdgeStatus:     "EXECUTED",
			ExecutionFence: 12,
		}
		if err := persistEdgeCommandLedger(legacyPath, map[string]edgeCommandRecord{
			"cmd-legacy-term": {
				DeviceID:       chillerID,
				PayloadHash:    "hash-legacy-term",
				ExecutionFence: 12,
				State:          edgeCommandTerminal,
				Reply:          &cachedReply,
			},
		}); err != nil {
			t.Fatal(err)
		}
		beforeLegacy, err := os.ReadFile(legacyPath)
		if err != nil {
			t.Fatal(err)
		}

		handler, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: queueDir}, deviceIDs, testMQTTEvidenceSpool(t))
		if err != nil {
			t.Fatal(err)
		}
		handler.now = func() time.Time { return now }

		walRaw, err := os.ReadFile(filepath.Join(queueDir, "command-execution-records.wal"))
		if err != nil {
			t.Fatalf("expected migrated .wal file, err=%v", err)
		}
		if !strings.Contains(string(walRaw), `"type":"fence"`) || !strings.Contains(string(walRaw), `"cmd-legacy-term"`) {
			t.Fatalf("unexpected migrated WAL content:\n%s", string(walRaw))
		}
		afterLegacy, err := os.ReadFile(legacyPath)
		if err != nil || string(afterLegacy) != string(beforeLegacy) {
			t.Fatalf("legacy json should stay untouched")
		}

		replayed := handler.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-legacy-replay",
			CommandID:      "cmd-legacy-term",
			IssuedAt:       now.Add(-time.Second).UnixMilli(),
			ExpireAt:       now.Add(time.Minute).UnixMilli(),
			DeviceID:       chillerID,
			CommandCode:    "START",
			ExecutionFence: 12,
			PayloadHash:    "hash-legacy-term",
		})
		if !reflect.DeepEqual(replayed, cachedReply) {
			t.Fatalf("migrated terminal reply=%+v want=%+v", replayed, cachedReply)
		}
	})

	t.Run("corrupt_wal_fails_closed", func(t *testing.T) {
		plant := NewPlant(config.Plant, config.Scenario, now)
		edgeRuntime, err := NewEdgeControlRuntime(config, plant)
		if err != nil {
			t.Fatal(err)
		}
		badPayloads := []struct {
			name    string
			content string
		}{
			{name: "malformed_json", content: "{not-json\n"},
			{name: "unknown_type", content: `{"type":"other","deviceId":"d1","executionFence":1}` + "\n"},
			{name: "zero_fence_entry", content: `{"type":"fence","deviceId":"d1","executionFence":0}` + "\n"},
			{name: "invalid_state", content: `{"type":"record","commandId":"c1","record":{"deviceId":"d1","payloadHash":"h","executionFence":1,"state":"BOGUS"}}` + "\n"},
			{name: "terminal_without_reply", content: `{"type":"record","commandId":"c1","record":{"deviceId":"d1","payloadHash":"h","executionFence":1,"state":"TERMINAL"}}` + "\n"},
			{name: "trailing_tokens", content: `{"type":"fence","deviceId":"d1","executionFence":1}{"extra":1}` + "\n"},
		}
		for _, tc := range badPayloads {
			t.Run(tc.name, func(t *testing.T) {
				queueDir := t.TempDir()
				if err := os.WriteFile(filepath.Join(queueDir, "command-execution-records.wal"), []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: queueDir}, deviceIDs, testMQTTEvidenceSpool(t)); err == nil {
					t.Fatalf("expected error for corrupt WAL case %s", tc.name)
				}
			})
		}
	})
}

func TestEdgeMQTTCommandWALCompactionPreservesNonTerminalAndMaxFences(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	plant := NewPlant(config.Plant, config.Scenario, now)
	edgeRuntime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	deviceIDs := config.Plant.DeviceIDs()
	if len(deviceIDs) < 3 {
		t.Fatalf("expected at least 3 devices, got %d", len(deviceIDs))
	}
	oldDevice := deviceIDs[0]
	inflightDevice := deviceIDs[1]
	churnDevice := deviceIDs[2]

	queueDir := t.TempDir()
	handler, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: queueDir}, deviceIDs, testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	// 1. Record an early terminal command on oldDevice with high fence 99 (will be evicted from terminal window).
	oldReply := mqttCommandReply{
		SchemaVersion: mqttCommandSchemaVersion, MessageID: "msg-old", CommandID: "cmd-old-evicted",
		EventTime: now.UnixMilli(), EdgeStatus: "EXECUTED", ExecutionFence: 99,
	}
	handler.mu.Lock()
	if err := handler.appendRecordLocked("cmd-old-evicted", edgeCommandRecord{
		DeviceID: oldDevice, PayloadHash: "hash-old", ExecutionFence: 99, State: edgeCommandMayExecute,
	}); err != nil {
		handler.mu.Unlock()
		t.Fatal(err)
	}
	handler.mu.Unlock()
	handler.finish(oldDevice, "hash-old", oldReply)

	// 2. Record a non-terminal MAY_EXECUTE command on inflightDevice (must survive compaction!).
	handler.mu.Lock()
	if err := handler.appendRecordLocked("cmd-inflight-keep", edgeCommandRecord{
		DeviceID: inflightDevice, PayloadHash: "hash-inflight", ExecutionFence: 50, State: edgeCommandMayExecute,
	}); err != nil {
		handler.mu.Unlock()
		t.Fatal(err)
	}
	handler.mu.Unlock()

	// 3. Append 1100 commands (2200 WAL lines, crossing compactThreshold=2048) on churnDevice.
	const totalChurn = 1100
	for i := 1; i <= totalChurn; i++ {
		cmdID := "cmd-churn-" + jsonNumber(int64(i))
		hash := "hash-churn-" + jsonNumber(int64(i))
		fence := uint64(i)
		handler.mu.Lock()
		if err := handler.appendRecordLocked(cmdID, edgeCommandRecord{
			DeviceID: churnDevice, PayloadHash: hash, ExecutionFence: fence, State: edgeCommandMayExecute,
		}); err != nil {
			handler.mu.Unlock()
			t.Fatal(err)
		}
		handler.mu.Unlock()
		handler.finish(churnDevice, hash, mqttCommandReply{
			SchemaVersion: mqttCommandSchemaVersion, MessageID: "msg-" + cmdID, CommandID: cmdID,
			EventTime: now.UnixMilli(), EdgeStatus: "EXECUTED", ExecutionFence: fence,
		})
	}

	// Inspect compacted WAL and in-memory state.
	walRaw, err := os.ReadFile(filepath.Join(queueDir, "command-execution-records.wal"))
	if err != nil {
		t.Fatal(err)
	}
	walLines := strings.Split(strings.TrimSpace(string(walRaw)), "\n")
	if len(walLines) >= edgeCommandWALCompactThreshold {
		t.Fatalf("expected WAL line count < %d after compaction, got %d", edgeCommandWALCompactThreshold, len(walLines))
	}

	handler.mu.Lock()
	inMemTerminal := 0
	for _, rec := range handler.results {
		if rec.State == edgeCommandTerminal {
			inMemTerminal++
		}
	}
	_, hasInflight := handler.results["cmd-inflight-keep"]
	_, hasEvicted := handler.results["cmd-old-evicted"]
	_, hasNewest := handler.results["cmd-churn-1100"]
	oldMaxFence := handler.maxFenceByDevice[oldDevice]
	handler.mu.Unlock()

	if inMemTerminal > maxRetainedTerminalCommandRecords {
		t.Fatalf("retained terminal count=%d > %d", inMemTerminal, maxRetainedTerminalCommandRecords)
	}
	if !hasInflight || hasEvicted || !hasNewest {
		t.Fatalf("compaction retention mismatch: inflight=%v evicted=%v newest=%v", hasInflight, hasEvicted, hasNewest)
	}
	if oldMaxFence != 99 {
		t.Fatalf("maxFenceByDevice[%s]=%d, want 99", oldDevice, oldMaxFence)
	}

	// Restart from compacted WAL and confirm invariants hold across restart.
	restarted, err := newEdgeCommandHandler(edgeRuntime, MQTTGatewayConfig{QueueDirectory: queueDir}, deviceIDs, testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = handler.now

	restarted.mu.Lock()
	restartedTerminal := 0
	for _, rec := range restarted.results {
		if rec.State == edgeCommandTerminal {
			restartedTerminal++
		}
	}
	restarted.mu.Unlock()
	if restartedTerminal > maxRetainedTerminalCommandRecords {
		t.Fatalf("restarted terminal count=%d > %d", restartedTerminal, maxRetainedTerminalCommandRecords)
	}

	// Non-terminal record still returns EDGE_OUTCOME_UNKNOWN after restart.
	inflightResp := restarted.evaluate(mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion, MessageID: "msg-inflight-retry", CommandID: "cmd-inflight-keep",
		IssuedAt: now.Add(-time.Second).UnixMilli(), ExpireAt: now.Add(time.Minute).UnixMilli(),
		DeviceID: inflightDevice, CommandCode: "START", ExecutionFence: 50, PayloadHash: "hash-inflight",
	})
	if inflightResp.EdgeStatus != "FAILED" || inflightResp.ReasonCode == nil || *inflightResp.ReasonCode != "EDGE_OUTCOME_UNKNOWN" {
		t.Fatalf("expected EDGE_OUTCOME_UNKNOWN for retained MAY_EXECUTE record, got %+v", inflightResp)
	}

	// Old evicted device still rejects fence <= 99 with STALE_FENCE after restart.
	staleResp := restarted.evaluate(mqttCommandEnvelope{
		SchemaVersion: mqttCommandSchemaVersion, MessageID: "msg-stale-after-compact", CommandID: "cmd-old-new-attempt",
		IssuedAt: now.Add(-time.Second).UnixMilli(), ExpireAt: now.Add(time.Minute).UnixMilli(),
		DeviceID: oldDevice, CommandCode: "START", ExecutionFence: 99, PayloadHash: "hash-old-new",
	})
	if staleResp.EdgeStatus != "REJECTED" || staleResp.ReasonCode == nil || *staleResp.ReasonCode != "STALE_FENCE" {
		t.Fatalf("expected STALE_FENCE for evicted device fence 99, got %+v", staleResp)
	}
}
