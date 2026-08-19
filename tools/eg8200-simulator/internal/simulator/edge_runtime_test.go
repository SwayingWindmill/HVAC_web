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
	plant := NewPlant(config.Plant, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}

	frequency := runEdgeCommand(t, runtime, at, "command-frequency", config.Plant.ChilledWaterPump.ID, "SET_FREQUENCY", map[string]float64{"frequencyHz": 55})
	if !frequency.Accepted || frequency.Effective == nil || frequency.Effective.Double != 50 || frequency.Code != "APPLIED" {
		t.Fatalf("capability limit did not arbitrate 55 Hz to 50 Hz: %#v", frequency)
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
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["runState"]; got != "STOPPED" {
		t.Fatalf("interlocked chiller unexpectedly started: %v", got)
	}
}

func TestEdgeTelemetryPublishesProcessImageBeforeCurrentCycleWrite(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(3500, 0).UTC()
	plant := NewPlant(config.Plant, at)
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
	if got := plant.Snapshot().Devices[deviceID]["frequencyHz"]; got != target {
		t.Fatalf("device write was not applied after Process Image: got=%v want=%v", got, target)
	}
	next := runtime.RunCycle(context.Background(), at.Add(2*time.Second))
	if got := next.TelemetrySnapshot.Devices[deviceID]["frequencyHz"]; got != target {
		t.Fatalf("next Cycle did not observe prior write: got=%v want=%v", got, target)
	}
}

func TestNumericRemoteIntentPersistsUntilLeaseExpiry(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(3750, 0).UTC()
	plant := NewPlant(config.Plant, at)
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

	// Simulate a lower-level/local drift while the remote lease is still active.
	if result := plant.ApplyCommand(Command{DeviceID: deviceID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 45}}); !result.Success {
		t.Fatalf("failed to perturb simulated equipment: %#v", result)
	}
	runtime.RunCycle(context.Background(), at.Add(2*time.Second))
	if got := plant.Snapshot().Devices[deviceID]["frequencyHz"]; got != target {
		t.Fatalf("active remote lease was not rewritten each Cycle: got=%v want=%v", got, target)
	}

	// Once expired the Edge Intent must stop participating; a local value remains.
	if result := plant.ApplyCommand(Command{DeviceID: deviceID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 45}}); !result.Success {
		t.Fatalf("failed to perturb simulated equipment after lease: %#v", result)
	}
	runtime.RunCycle(context.Background(), at.Add(4*time.Second))
	if got := plant.Snapshot().Devices[deviceID]["frequencyHz"]; got != 45.0 {
		t.Fatalf("expired remote lease remained sticky: got=%v", got)
	}
}

func TestEdgeControlRuntimeExpiresCommandBeforeExecution(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(4000, 0).UTC()
	plant := NewPlant(config.Plant, at)
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
