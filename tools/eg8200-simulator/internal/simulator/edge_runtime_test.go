package simulator

import (
	"context"
	"testing"
	"time"
)

func runEdgeCommand(t *testing.T, runtime *EdgeControlRuntime, at time.Time, commandID, deviceID, capability string, params map[string]float64) EdgeCommandOutcome {
	t.Helper()
	outcomeCh, err := runtime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID: commandID, DeviceID: deviceID, CommandCode: capability, Params: params,
		IssuedAt: at, ExpiresAt: at.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.RunCycle(context.Background(), at.Add(time.Second))
	select {
	case outcome := <-outcomeCh:
		return outcome
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Edge command outcome")
		return EdgeCommandOutcome{}
	}
}

func TestEdgeControlRuntimeArbitratesAndExecutesSimulatorDriver(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(3000, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}

	frequency := runEdgeCommand(t, runtime, at, "command-frequency", config.Plant.ChilledWaterPump.ID, "SET_FREQUENCY", map[string]float64{"frequencyHz": 55})
	if !frequency.Accepted || frequency.Effective == nil || frequency.Effective.Double != 50 || frequency.Code != "APPLIED" ||
		frequency.WinnerControllerID != "cloud-command-intent" || frequency.Cycle == 0 || len(frequency.ConstraintReasons) == 0 {
		t.Fatalf("capability limit did not record requested/effective/constraint/winner/cycle evidence: %#v", frequency)
	}
	if got := plant.Snapshot().Devices[config.Plant.ChilledWaterPump.ID]["frequencyHz"]; got != 50.0 {
		t.Fatalf("simulator driver did not apply effective frequency: %v", got)
	}

	stopPump := runEdgeCommand(t, runtime, at.Add(2*time.Second), "command-stop-pump", config.Plant.ChilledWaterPump.ID, "STOP", nil)
	if !stopPump.Accepted || stopPump.Code != "APPLIED" {
		t.Fatalf("pump STOP failed: %#v", stopPump)
	}
	stopChiller := runEdgeCommand(t, runtime, at.Add(4*time.Second), "command-stop-chiller", config.Plant.Chiller.ID, "STOP", nil)
	if !stopChiller.Accepted || stopChiller.Code != "APPLIED" {
		t.Fatalf("chiller STOP failed: %#v", stopChiller)
	}
	startChiller := runEdgeCommand(t, runtime, at.Add(6*time.Second), "command-start-chiller", config.Plant.Chiller.ID, "START", nil)
	if startChiller.Accepted || startChiller.Code != "INTERLOCK_OPEN" {
		t.Fatalf("chiller START bypassed local plant interlock: %#v", startChiller)
	}
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["runState"]; got != "RUNNING" {
		t.Fatalf("chiller physical state changed before time advanced: %v", got)
	}
	settled := plant.Tick(5 * time.Minute)
	if got := settled.Devices[config.Plant.Chiller.ID]["runState"]; got != "STOPPED" {
		t.Fatalf("stopped chiller did not physically coast down: %v", got)
	}
}

func TestEdgeTelemetryPublishesProcessImageBeforeCurrentCycleWrite(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(3500, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := config.Plant.ChilledWaterPump.ID
	before, _ := plant.Snapshot().Devices[deviceID]["frequencyHz"].(float64)
	target := before - 0.5
	outcomeCh, err := runtime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID: "command-process-image-boundary", DeviceID: deviceID, CommandCode: "SET_FREQUENCY",
		Params: map[string]float64{"frequencyHz": target}, IssuedAt: at, ExpiresAt: at.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle := runtime.RunCycle(context.Background(), at.Add(time.Second))
	outcome := <-outcomeCh
	if !outcome.Accepted {
		t.Fatalf("command was not applied: %#v", outcome)
	}
	if got := cycle.TelemetrySnapshot.Devices[deviceID]["frequencyHz"]; got != before {
		t.Fatalf("current Cycle telemetry leaked post-write device state: got=%v want=%v", got, before)
	}
	if got := plant.Snapshot().Devices[deviceID]["frequencyHz"]; got != before {
		t.Fatalf("device actual readback changed before physical time advanced: got=%v want=%v", got, before)
	}
	plant.Tick(time.Second)
	next := runtime.RunCycle(context.Background(), at.Add(2*time.Second))
	nextFrequency := next.TelemetrySnapshot.Devices[deviceID]["frequencyHz"].(float64)
	if !(nextFrequency < before && nextFrequency > target) {
		t.Fatalf("next Cycle did not observe physical response toward prior write: got=%v target=%v", nextFrequency, target)
	}
}

func TestNumericRemoteIntentPersistsUntilLeaseExpiry(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(3750, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := config.Plant.ChilledWaterPump.ID
	target := 49.5
	outcomeCh, err := runtime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID: "command-leased-frequency", DeviceID: deviceID, CommandCode: "SET_FREQUENCY",
		Params: map[string]float64{"frequencyHz": target}, IssuedAt: at, ExpiresAt: at.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.RunCycle(context.Background(), at.Add(time.Second))
	if outcome := <-outcomeCh; !outcome.Accepted {
		t.Fatalf("leased command initial write failed: %#v", outcome)
	}

	// Simulate a lower-level/local target drift while the remote lease is still active.
	if result := plant.ApplyCommand(Command{DeviceID: deviceID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 45}}); !result.Success {
		t.Fatalf("failed to perturb simulated equipment: %#v", result)
	}
	drifted := plant.Tick(20 * time.Second).Devices[deviceID]["frequencyHz"].(float64)
	runtime.RunCycle(context.Background(), at.Add(2*time.Second))
	reasserted := plant.Tick(20 * time.Second).Devices[deviceID]["frequencyHz"].(float64)
	if reasserted <= drifted || reasserted >= target {
		t.Fatalf("active remote lease did not restore the physical target: drifted=%v reasserted=%v target=%v", drifted, reasserted, target)
	}

	// Once expired the Edge Intent must stop participating; a local target remains.
	if result := plant.ApplyCommand(Command{DeviceID: deviceID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 45}}); !result.Success {
		t.Fatalf("failed to perturb simulated equipment after lease: %#v", result)
	}
	runtime.RunCycle(context.Background(), at.Add(4*time.Second))
	settled := plant.Tick(2 * time.Minute).Devices[deviceID]["frequencyHz"].(float64)
	if settled < 44.9 || settled > 45.1 {
		t.Fatalf("expired remote lease remained sticky: got=%v", settled)
	}
}

func TestEdgeControlRuntimeExpiresCommandBeforeExecution(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(4000, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	outcomeCh, err := runtime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID: "expired-command", DeviceID: config.Plant.ChilledWaterPump.ID, CommandCode: "SET_FREQUENCY",
		Params: map[string]float64{"frequencyHz": 35}, IssuedAt: at, ExpiresAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.RunCycle(context.Background(), at.Add(2*time.Second))
	outcome := <-outcomeCh
	if outcome.Accepted || outcome.Code != "EXPIRED" {
		t.Fatalf("expired command was executed: %#v", outcome)
	}
	if got := plant.Snapshot().Devices[config.Plant.ChilledWaterPump.ID]["frequencyHz"]; got != 50.0 {
		t.Fatalf("expired command changed equipment: %v", got)
	}
}

func TestEdgeSafetyRejectsStartWhenInterlockEvidenceIsStale(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(4500, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	if cycle := runtime.RunCycle(context.Background(), at.Add(time.Second)); cycle.Cycle.Halted {
		t.Fatalf("initial Edge cycle failed: %#v", cycle.Cycle)
	}
	outcomeCh, err := runtime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID: "stale-interlock-start", DeviceID: config.Plant.Chiller.ID, CommandCode: "START",
		IssuedAt: at.Add(20 * time.Second), ExpiresAt: at.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.RunCycle(context.Background(), at.Add(21*time.Second))
	outcome := <-outcomeCh
	if outcome.Accepted || outcome.Code != "SAFETY_STATE_STALE" {
		t.Fatalf("stale safety evidence did not block start: %#v", outcome)
	}
}
