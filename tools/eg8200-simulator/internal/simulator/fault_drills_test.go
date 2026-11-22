package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func putJSONExpectStatus(t *testing.T, handler http.Handler, path string, payload any, wantStatus int) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(raw))
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("PUT %s status=%d want %d body=%s", path, rec.Code, wantStatus, rec.Body.String())
	}
}

func TestThermalInertiaLagFiveToFifteenMinutes(t *testing.T) {
	config := testPlantConfig()
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC))
	mux := http.NewServeMux()
	RegisterCHWPDisturbance(mux, plant)

	// Invalid lag (< 60s or > 1800s) is rejected cleanly.
	putJSONExpectStatus(t, mux, "/acceptance/plant/thermal-lag", map[string]any{"seconds": 30}, http.StatusBadRequest)
	putJSONExpectStatus(t, mux, "/acceptance/plant/thermal-lag", map[string]any{"seconds": 2400}, http.StatusBadRequest)

	// Stabilize at 7.0C baseline first, then configure 10-minute water thermal inertia lag.
	plant.Tick(30 * time.Minute)
	putJSONExpectStatus(t, mux, "/acceptance/plant/thermal-lag", map[string]any{"seconds": 600}, http.StatusNoContent)

	before := plant.Snapshot().Devices[config.BTUMeterID]["supplyWaterTemperatureC"].(float64)
	if res := plant.ApplyCommand(Command{
		DeviceID: config.Chiller.ID,
		Method:   "setChilledWaterTemperatureSetpoint",
		Params:   map[string]float64{"setpointC": 9.0},
	}); !res.Success {
		t.Fatalf("setpoint command failed: %+v", res)
	}

	at5m := plant.Tick(5 * time.Minute)
	temp5m := at5m.Devices[config.BTUMeterID]["supplyWaterTemperatureC"].(float64)
	assertWaterSideEnergyBalance(t, at5m, config)
	// With tau=10m, after 5m response stays noticeably shy of full convergence (~39% of 2C delta -> < 8.3C).
	if temp5m <= before+0.2 || temp5m >= 8.3 {
		t.Fatalf("5m response does not match 10m thermal inertia lag: before=%.3f at5m=%.3f", before, temp5m)
	}

	at10m := plant.Tick(5 * time.Minute)
	temp10m := at10m.Devices[config.BTUMeterID]["supplyWaterTemperatureC"].(float64)
	assertWaterSideEnergyBalance(t, at10m, config)
	if temp10m <= temp5m || temp10m >= 8.9 {
		t.Fatalf("10m response did not continue monotonic approach: at5m=%.3f at10m=%.3f", temp5m, temp10m)
	}

	at30m := plant.Tick(20 * time.Minute)
	temp30m := at30m.Devices[config.BTUMeterID]["supplyWaterTemperatureC"].(float64)
	assertWaterSideEnergyBalance(t, at30m, config)
	if temp30m <= temp10m || temp30m < 8.75 {
		t.Fatalf("30m response did not settle near 9.0C: at10m=%.3f at30m=%.3f", temp10m, temp30m)
	}
}

func TestControlCabinetLocalManualModeBlocksRemoteCommandsAndRecovers(t *testing.T) {
	simConfig := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	plant := NewPlant(simConfig.Plant, simConfig.Scenario, now)
	edgeRT, err := NewEdgeControlRuntime(simConfig, plant)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newEdgeCommandHandler(edgeRT, MQTTGatewayConfig{QueueDirectory: t.TempDir()}, simConfig.Plant.DeviceIDs(), testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	mux := http.NewServeMux()
	RegisterCHWPDisturbance(mux, plant)

	putJSONExpectStatus(t, mux, "/acceptance/plant/cabinet-mode", map[string]any{
		"deviceId": simConfig.Plant.Chiller.ID,
		"mode":     "INVALID",
	}, http.StatusBadRequest)
	putJSONExpectStatus(t, mux, "/acceptance/plant/cabinet-mode", map[string]any{
		"deviceId": simConfig.Plant.Chiller.ID,
		"mode":     "LOCAL",
	}, http.StatusNoContent)

	snap := plant.Snapshot()
	if got := snap.Devices[simConfig.Plant.Chiller.ID]["localRemoteMode"]; got != ControlCabinetModeLocal {
		t.Fatalf("expected chiller localRemoteMode=LOCAL, got %v", got)
	}

	// Direct command and full MQTT Edge handler both reject while cabinet stays in LOCAL manual mode.
	if direct := plant.ApplyCommand(Command{
		DeviceID: simConfig.Plant.Chiller.ID,
		Method:   "setChilledWaterTemperatureSetpoint",
		Params:   map[string]float64{"setpointC": 8.5},
	}); direct.Success || direct.Code != "CABINET_LOCAL_MANUAL" {
		t.Fatalf("expected CABINET_LOCAL_MANUAL direct rejection, got %+v", direct)
	}

	replies := make(chan mqttCommandReply, 1)
	go func() {
		replies <- handler.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-local-1",
			CommandID:      "cmd-local-1",
			IssuedAt:       now.Add(-time.Second).UnixMilli(),
			ExpireAt:       now.Add(time.Minute).UnixMilli(),
			DeviceID:       simConfig.Plant.Chiller.ID,
			CommandCode:    "SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
			Params:         map[string]float64{"setpointC": 8.5},
			Policy:         mqttCommandPolicy{RequiresReadback: true, VerificationWindowMS: 5000},
			ExecutionFence: 1,
			PayloadHash:    "hash-local-1",
		})
	}()
	waitSimulatorPendingAndRunCycle(t, edgeRT, "cmd-local-1", now)
	localReply := <-replies
	if localReply.EdgeStatus != "REJECTED" || localReply.ReasonCode == nil || *localReply.ReasonCode != "CABINET_LOCAL_MANUAL" {
		t.Fatalf("expected REJECTED CABINET_LOCAL_MANUAL over MQTT, got %+v", localReply)
	}

	// Local cabinet manual dial adjusts setpoint cleanly while remote commands stay blocked.
	if !plant.SetLocalManualTarget(simConfig.Plant.Chiller.ID, 6.5) {
		t.Fatal("SetLocalManualTarget failed")
	}
	if got := plant.Snapshot().Devices[simConfig.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"]; got != 6.5 {
		t.Fatalf("expected local dial setpoint 6.5, got %v", got)
	}

	// Switch back to REMOTE -> remote MQTT control resumes normally.
	putJSONExpectStatus(t, mux, "/acceptance/plant/cabinet-mode", map[string]any{
		"deviceId": simConfig.Plant.Chiller.ID,
		"mode":     "REMOTE",
	}, http.StatusNoContent)
	go func() {
		replies <- handler.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-remote-2",
			CommandID:      "cmd-remote-2",
			IssuedAt:       now.Add(time.Second).UnixMilli(),
			ExpireAt:       now.Add(2 * time.Minute).UnixMilli(),
			DeviceID:       simConfig.Plant.Chiller.ID,
			CommandCode:    "SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
			Params:         map[string]float64{"setpointC": 8.0},
			Policy:         mqttCommandPolicy{RequiresReadback: true, VerificationWindowMS: 5000},
			ExecutionFence: 2,
			PayloadHash:    "hash-remote-2",
		})
	}()
	waitSimulatorPendingAndRunCycle(t, edgeRT, "cmd-remote-2", now.Add(time.Second))
	remoteReply := <-replies
	if remoteReply.EdgeStatus != "EXECUTED" || plant.Snapshot().Devices[simConfig.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"] != 8.0 {
		t.Fatalf("expected EXECUTED after restoring REMOTE, got %+v", remoteReply)
	}
}

func TestRegisterReadOnlyLockAcksCommandButFeedbackDoesNotConverge(t *testing.T) {
	simConfig := loadGeneratedCentralPlantConfig(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	plant := NewPlant(simConfig.Plant, simConfig.Scenario, now)
	plant.Tick(15 * time.Minute)

	edgeRT, err := NewEdgeControlRuntime(simConfig, plant)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newEdgeCommandHandler(edgeRT, MQTTGatewayConfig{QueueDirectory: t.TempDir()}, simConfig.Plant.DeviceIDs(), testMQTTEvidenceSpool(t))
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	mux := http.NewServeMux()
	RegisterCHWPDisturbance(mux, plant)

	initialPumpHz := plant.Snapshot().Devices[simConfig.Plant.ChilledWaterPump.ID]["frequencyHz"].(float64)
	initialChillerSP := plant.Snapshot().Devices[simConfig.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"].(float64)

	putJSONExpectStatus(t, mux, "/acceptance/plant/register-readonly-lock", map[string]any{
		"deviceId": simConfig.Plant.ChilledWaterPump.ID,
		"active":   true,
	}, http.StatusNoContent)
	putJSONExpectStatus(t, mux, "/acceptance/plant/register-readonly-lock", map[string]any{
		"deviceId": simConfig.Plant.Chiller.ID,
		"active":   true,
	}, http.StatusNoContent)

	replies := make(chan mqttCommandReply, 2)
	go func() {
		replies <- handler.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-lock-pump",
			CommandID:      "cmd-lock-pump",
			IssuedAt:       now.UnixMilli(),
			ExpireAt:       now.Add(2 * time.Minute).UnixMilli(),
			DeviceID:       simConfig.Plant.ChilledWaterPump.ID,
			CommandCode:    "SET_FREQUENCY",
			Params:         map[string]float64{"frequencyHz": 32},
			Policy:         mqttCommandPolicy{RequiresReadback: true, VerificationWindowMS: 5000},
			ExecutionFence: 1,
			PayloadHash:    "hash-lock-pump",
		})
	}()
	waitSimulatorPendingAndRunCycle(t, edgeRT, "cmd-lock-pump", now)
	pumpReply := <-replies
	if pumpReply.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected ACK/EXECUTED even on read-only locked VFD, got %+v", pumpReply)
	}

	go func() {
		replies <- handler.evaluate(mqttCommandEnvelope{
			SchemaVersion:  mqttCommandSchemaVersion,
			MessageID:      "msg-lock-chiller",
			CommandID:      "cmd-lock-chiller",
			IssuedAt:       now.Add(time.Second).UnixMilli(),
			ExpireAt:       now.Add(2 * time.Minute).UnixMilli(),
			DeviceID:       simConfig.Plant.Chiller.ID,
			CommandCode:    "SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
			Params:         map[string]float64{"setpointC": 10.5},
			Policy:         mqttCommandPolicy{RequiresReadback: true, VerificationWindowMS: 5000},
			ExecutionFence: 1,
			PayloadHash:    "hash-lock-chiller",
		})
	}()
	waitSimulatorPendingAndRunCycle(t, edgeRT, "cmd-lock-chiller", now.Add(time.Second))
	chillerReply := <-replies
	if chillerReply.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected ACK/EXECUTED even on read-only locked chiller, got %+v", chillerReply)
	}

	// Physical plant ticks 15 minutes; feedback values fail to converge while register lock holds.
	after15m := plant.Tick(15 * time.Minute)
	if gotHz := after15m.Devices[simConfig.Plant.ChilledWaterPump.ID]["frequencyHz"].(float64); gotHz != initialPumpHz {
		t.Fatalf("locked pump frequency converged to %.3f, want stuck at %.3f", gotHz, initialPumpHz)
	}
	if gotSP := after15m.Devices[simConfig.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"].(float64); gotSP != initialChillerSP {
		t.Fatalf("locked chiller setpoint feedback changed to %.3f, want stuck at %.3f", gotSP, initialChillerSP)
	}

	// Releasing lock allows physical equipment to converge toward latched command targets.
	putJSONExpectStatus(t, mux, "/acceptance/plant/register-readonly-lock", map[string]any{
		"deviceId": simConfig.Plant.ChilledWaterPump.ID,
		"active":   false,
	}, http.StatusNoContent)
	putJSONExpectStatus(t, mux, "/acceptance/plant/register-readonly-lock", map[string]any{
		"deviceId": simConfig.Plant.Chiller.ID,
		"active":   false,
	}, http.StatusNoContent)
	recovered := plant.Tick(10 * time.Minute)
	if gotHz := recovered.Devices[simConfig.Plant.ChilledWaterPump.ID]["frequencyHz"].(float64); gotHz != 32.0 {
		t.Fatalf("unlocked pump did not converge to 32 Hz, got %.3f", gotHz)
	}
	if gotSP := recovered.Devices[simConfig.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"].(float64); gotSP != 10.5 {
		t.Fatalf("unlocked chiller setpoint did not converge to 10.5 C, got %.3f", gotSP)
	}
}

func TestHydraulicHardInterlockValveAndFlowSwitchBlockChillerStart(t *testing.T) {
	config := testPlantConfig()
	config.Chiller.InitiallyRunning = false
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC))
	plant.Tick(time.Minute)

	mux := http.NewServeMux()
	RegisterCHWPDisturbance(mux, plant)

	// Closing chilled-water isolation butterfly valve prevents flow proof and blocks chiller start.
	putJSONExpectStatus(t, mux, "/acceptance/plant/hydraulic-interlock", map[string]any{
		"chwValveOpen":         false,
		"cwValveOpen":          true,
		"chwFlowSwitchBlocked": false,
		"cwFlowSwitchBlocked":  false,
	}, http.StatusNoContent)
	snap := plant.Snapshot()
	if snap.Devices[config.Chiller.ID]["chwValveOpen"] != false || snap.Devices[config.Chiller.ID]["chwFlowSwitch"] != false {
		t.Fatalf("expected closed butterfly valve and unproven flow switch, got %+v", snap.Devices[config.Chiller.ID])
	}
	if res := plant.ApplyCommand(Command{DeviceID: config.Chiller.ID, Method: "start"}); res.Success || res.Code != "CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN" {
		t.Fatalf("expected CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN when valve closed, got %+v", res)
	}

	// Open valve but stick flow switch open (no flow proof) -> still blocked.
	putJSONExpectStatus(t, mux, "/acceptance/plant/hydraulic-interlock", map[string]any{
		"chwValveOpen":         true,
		"cwValveOpen":          true,
		"chwFlowSwitchBlocked": true,
		"cwFlowSwitchBlocked":  false,
	}, http.StatusNoContent)
	if res := plant.ApplyCommand(Command{DeviceID: config.Chiller.ID, Method: "start"}); res.Success || res.Code != "CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN" {
		t.Fatalf("expected CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN when flow switch blocked, got %+v", res)
	}

	// Restore butterfly valves open & flow switches proven -> chiller starts cleanly.
	putJSONExpectStatus(t, mux, "/acceptance/plant/hydraulic-interlock", map[string]any{
		"chwValveOpen":         true,
		"cwValveOpen":          true,
		"chwFlowSwitchBlocked": false,
		"cwFlowSwitchBlocked":  false,
	}, http.StatusNoContent)
	if res := plant.ApplyCommand(Command{DeviceID: config.Chiller.ID, Method: "start"}); !res.Success {
		t.Fatalf("expected chiller start after hydraulic interlock proven, got %+v", res)
	}
	after := plant.Tick(5 * time.Minute)
	if after.Devices[config.Chiller.ID]["runState"] != "RUNNING" {
		t.Fatalf("expected RUNNING chiller after proved start, got %v", after.Devices[config.Chiller.ID]["runState"])
	}
	_ = edgecontrol.ReasonHardRealtimeSafetyBoundary
}

func waitSimulatorPendingAndRunCycle(t *testing.T, edgeRT *EdgeControlRuntime, cmdID string, cycleAt time.Time) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		edgeRT.mu.Lock()
		found := false
		for _, pending := range edgeRT.pendingByAddress {
			if pending.intentID == cmdID {
				found = true
				break
			}
		}
		edgeRT.mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("command %s did not enter Edge pending queue", cmdID)
		}
		goruntime.Gosched()
	}
	edgeRT.RunCycle(context.Background(), cycleAt)
}
