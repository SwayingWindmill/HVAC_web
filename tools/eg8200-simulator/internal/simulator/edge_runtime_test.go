package simulator

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
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
	plant.Tick(50 * time.Millisecond)
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
	reasserted := plant.Tick(500 * time.Millisecond).Devices[deviceID]["frequencyHz"].(float64)
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

// A WSL clock resync once stepped the host clock back mid-run; the cycle that followed halted
// on the regression with no process image, and publishing its empty snapshot killed the
// publisher, restarting the plant from its initial state 14 times in an evening.
func TestACycleHaltedByAClockRegressionIsNotPublished(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(3500, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	if cycle := runtime.RunCycle(context.Background(), at.Add(2*time.Second)); !cycle.Publishable() {
		t.Fatalf("a normal cycle must be publishable: %#v", cycle.Cycle.ClockError)
	}
	regressed := runtime.RunCycle(context.Background(), at.Add(time.Second))
	if regressed.Publishable() || regressed.Cycle.ClockError == nil {
		t.Fatalf("a cycle behind the previous one must not be published: %#v", regressed.Cycle.ClockError)
	}
	resumed := runtime.RunCycle(context.Background(), at.Add(3*time.Second))
	if !resumed.Publishable() || resumed.TelemetrySnapshot.Devices[config.Plant.Chiller.ID] == nil {
		t.Fatal("cycles resume publishing once the clock passes the last cycle")
	}
}

func TestEdgeControlRuntimeAutonomyPolicyAndOfflineYield(t *testing.T) {
	t.Parallel()

	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(4500, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}

	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	policy.Enabled = true
	policy.OfflineGraceSeconds = 20
	policy.DefaultChilledWaterSupplySetpointC = 6.5
	policy.DefaultPumpFrequencyHz = 33.0
	policy.MinPumpFrequencyHz = 25.0
	policy.MaxPumpFrequencyHz = 45.0
	if err := runtime.ApplyAutonomyPolicy(policy); err != nil {
		t.Fatalf("ApplyAutonomyPolicy() error = %v", err)
	}

	// Connected online: pump freq setpoint clamped by updated policy [25, 45].
	runtime.NotifyCloudContact(true, at)
	res := runEdgeCommand(t, runtime, at.Add(time.Second), "cmd-clamped-max", config.Plant.ChilledWaterPump.ID, "SET_FREQUENCY", map[string]float64{"frequencyHz": 49})
	if !res.Accepted || res.Effective == nil || res.Effective.Double != 45.0 {
		t.Fatalf("expected pump frequency clamped to 45.0 by hot-updated autonomy policy, got %+v", res)
	}

	// Disconnect cloud and advance past OfflineGraceSeconds (20s): fallback takes over.
	disconnectAt := at.Add(2 * time.Second)
	runtime.NotifyCloudContact(false, disconnectAt)
	runtime.RunCycle(context.Background(), disconnectAt.Add(10*time.Second))
	if st, ok := runtime.AutonomousFallbackStatus(disconnectAt.Add(10 * time.Second)); !ok || st.FallbackActive {
		t.Fatalf("fallback should stay inactive within OfflineGraceSeconds: %+v", st)
	}

	fallbackAt := disconnectAt.Add(25 * time.Second)
	runtime.RunCycle(context.Background(), fallbackAt)
	if st, ok := runtime.AutonomousFallbackStatus(fallbackAt); !ok || !st.FallbackActive {
		t.Fatalf("expected FallbackActive=true after OfflineGraceSeconds: %+v", st)
	}
	plant.Tick(15 * time.Second)
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"]; got != 6.5 {
		t.Fatalf("expected chiller setpoint driven to fallback 6.5C, got %v", got)
	}

	// Cloud reconnects: yields cleanly so new cloud command at 9.5C takes effect.
	reconnectAt := fallbackAt.Add(2 * time.Second)
	runtime.NotifyCloudContact(true, reconnectAt)
	cloudCmd := runEdgeCommand(t, runtime, reconnectAt, "cmd-cloud-restore", config.Plant.Chiller.ID, "SET_CHILLED_WATER_TEMPERATURE_SETPOINT", map[string]float64{"setpointC": 9.5})
	if !cloudCmd.Accepted || cloudCmd.Code != "APPLIED" {
		t.Fatalf("expected cloud command applied immediately on reconnect yield, got %+v", cloudCmd)
	}
	if st, ok := runtime.AutonomousFallbackStatus(reconnectAt); !ok || st.FallbackActive {
		t.Fatalf("expected FallbackActive=false after reconnect: %+v", st)
	}
	if got := plant.Snapshot().Devices[config.Plant.Chiller.ID]["chilledWaterTemperatureSetpointC"]; got != 9.5 {
		t.Fatalf("expected cloud setpoint 9.5C after reconnect yield, got %v", got)
	}
}

func TestEdgeControlRuntimeRuleEnginePriorityAndOfflineAlarmEvents(t *testing.T) {
	t.Parallel()

	config := loadGeneratedCentralPlantConfig(t)
	at := time.Unix(5500, 0).UTC()
	plant := NewPlant(config.Plant, config.Scenario, at)
	runtime, err := NewEdgeControlRuntime(config, plant)
	if err != nil {
		t.Fatal(err)
	}

	initialTemp, ok := plant.Snapshot().Devices[config.Plant.Chiller.ID]["leavingChilledWaterTemperatureC"].(float64)
	if !ok {
		t.Fatalf("missing initial chiller leaving temperature in plant snapshot: %#v", plant.Snapshot().Devices[config.Plant.Chiller.ID])
	}
	thresh := initialTemp + 5.0
	clearThresh := initialTemp + 6.0
	bundle := &edgecontrol.EdgeRuleBundle{
		SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
		SiteID:         "0194f000-2000-7000-8000-000000000001",
		BundleRevision: 7,
		CompiledAt:     "2026-11-22T00:00:00Z",
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           "0194f000-4000-7000-8000-000000000101",
			AlarmType:        "EDGE_WINDOW_PROTECTION",
			Title:            "Chiller Leaving Temp Window Protection",
			Severity:         "CRITICAL",
			ExecutionTarget:  edgecontrol.EdgeTargetCloudAndEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 30,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:          edgecontrol.EdgeConditionWindowAgg,
				Input:         config.Plant.Chiller.ID + "/leaving_chilled_water_temperature",
				Agg:           edgecontrol.EdgeWindowAggAvg,
				WindowType:    edgecontrol.EdgeWindowSliding,
				WindowSeconds: 30,
				MinSamples:    2,
				Operator:      edgecontrol.EdgeCompareLT,
				Threshold:     &thresh,
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    config.Plant.Chiller.ID + "/leaving_chilled_water_temperature",
				Operator: edgecontrol.EdgeCompareGT,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: clearThresh},
			},
			Actions: []edgecontrol.EdgeRuleAction{{
				Kind:           edgecontrol.EdgeRuleActionProtectionCommand,
				TriggerOn:      edgecontrol.EdgeActionTriggerRaise,
				TargetDeviceID: config.Plant.ChilledWaterPump.ID,
				Capability:     "SET_FREQUENCY",
				Parameters:     map[string]float64{"frequencyHz": 42.5},
			}},
		}},
	}
	digest, err := bundle.ComputeDigest()
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	bundle.EdgeBundleDigest = digest

	if err := runtime.ApplyEdgeRuleBundle(bundle); err != nil {
		t.Fatalf("ApplyEdgeRuleBundle() error = %v", err)
	}
	runtime.NotifyCloudContact(false, at)

	// Cycle 1 collects 1st sample (< minSamples=2) -> should not raise yet.
	runtime.RunCycle(context.Background(), at.Add(time.Second))
	if st, ok := runtime.EdgeRuleStatus(); !ok || st.ActiveRuleCount != 0 {
		t.Fatalf("expected 0 active rules after 1 sample (< minSamples=2), got %+v", st)
	}

	// Advance plant clock so sample 2 has a distinct ObservedAt, then satisfy minSamples=2
	// AND race with lower-priority cloud command (priority 100 vs 15).
	plant.Tick(time.Second)
	cloudRes := runEdgeCommand(t, runtime, at.Add(2*time.Second), "cmd-overridden-by-edge-rule", config.Plant.ChilledWaterPump.ID, "SET_FREQUENCY", map[string]float64{"frequencyHz": 31.0})
	if cloudRes.WinnerControllerID != edgecontrol.DefaultEdgeRuleControllerID {
		t.Fatalf("expected edge rule engine (priority 15) to override cloud intent (priority 100), got %+v", cloudRes)
	}
	plant.Tick(30 * time.Second)
	if got := plant.Snapshot().Devices[config.Plant.ChilledWaterPump.ID]["frequencyHz"]; got != 42.5 {
		t.Fatalf("expected pump driven to edge protection frequency 42.5Hz, got %v", got)
	}
	st, ok := runtime.EdgeRuleStatus()
	if !ok || !st.Offline || st.RuleCount != 1 || st.ActiveRuleCount != 1 || st.EdgeBundleDigest != digest {
		t.Fatalf("unexpected EdgeRuleStatus after firing: %+v (ok=%v)", st, ok)
	}
	events := runtime.DrainEdgeRuleEvents()
	if len(events) != 1 || events[0].EventType != edgecontrol.EdgeRuleEventRaised || !events[0].Offline || len(events[0].TriggeredActions) != 1 {
		t.Fatalf("expected 1 offline EDGE_RULE_ALARM_RAISED event with action, got %+v", events)
	}

	// Clearing edge rule bundle yields priority back to cloud commands.
	if err := runtime.ApplyEdgeRuleBundle(nil); err != nil {
		t.Fatalf("ApplyEdgeRuleBundle(nil) error = %v", err)
	}
	runtime.NotifyCloudContact(true, at.Add(4*time.Second))
	cloudAllowed := runEdgeCommand(t, runtime, at.Add(4*time.Second), "cmd-after-rule-clear", config.Plant.ChilledWaterPump.ID, "SET_FREQUENCY", map[string]float64{"frequencyHz": 31.0})
	if !cloudAllowed.Accepted || cloudAllowed.WinnerControllerID != "cloud-command-intent" {
		t.Fatalf("expected cloud intent allowed after bundle clear, got %+v", cloudAllowed)
	}
}
