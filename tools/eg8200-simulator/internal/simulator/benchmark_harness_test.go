package simulator

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestMismatchPhysicsStaticHeadAndNonlinearTowerApproach(t *testing.T) {
	t.Parallel()

	// 1. Static head baseline power: P = P_rated * (c0 + (1-c0)*(f/50)^3).
	cfg := testPlantConfig()
	cfg.Mismatch.CHWPStaticHeadFraction = 0.12
	cfg.Mismatch.CWPStaticHeadFraction = 0.14
	plant := NewPlant(cfg, testStaticScenario(), time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))
	plant.Tick(time.Minute)

	res := plant.ApplyCommand(Command{
		DeviceID: cfg.ChilledWaterPump.ID,
		Method:   "setFrequency",
		Params:   map[string]float64{"frequencyHz": 25}, // 50% speed
	})
	if !res.Success {
		t.Fatalf("setFrequency failed: %+v", res)
	}
	snap := plant.Tick(2 * time.Minute)
	chwpPower := snap.Devices[cfg.ChilledWaterPump.ID]["powerKw"].(float64)
	wantPower := cfg.ChilledWaterPump.RatedPowerKW * (0.12 + 0.88*0.5*0.5*0.5)
	if math.Abs(chwpPower-wantPower) > 0.05 {
		t.Fatalf("static-head pump power = %.3f kW, want %.3f kW", chwpPower, wantPower)
	}

	// Stopping pump yields zero static-head power draw.
	if stopRes := plant.ApplyCommand(Command{DeviceID: cfg.ChilledWaterPump.ID, Method: "stop"}); !stopRes.Success {
		t.Fatalf("stop pump failed: %+v", stopRes)
	}
	stoppedSnap := plant.Tick(30 * time.Second)
	if got := stoppedSnap.Devices[cfg.ChilledWaterPump.ID]["powerKw"].(float64); got != 0 {
		t.Fatalf("stopped pump must draw 0 kW even with static head, got %.3f", got)
	}

	// 2. Nonlinear tower approach widens sharply at peak heat rejection with low fan speed.
	lowLoadApp := coolingTowerApproachC(PlantMismatchConfig{NonlinearTowerApproach: true}, 0.35, 0.30)
	highLoadLowFanApp := coolingTowerApproachC(PlantMismatchConfig{NonlinearTowerApproach: true}, 0.35, 0.95)
	highLoadFullFanApp := coolingTowerApproachC(PlantMismatchConfig{NonlinearTowerApproach: true}, 1.00, 0.95)
	if !(lowLoadApp < highLoadFullFanApp || highLoadLowFanApp > highLoadFullFanApp+3.0) {
		t.Fatalf("expected nonlinear approach penalty at peak load + low fan: lowLoad=%.2fC highLoadLowFan=%.2fC highLoadFullFan=%.2fC",
			lowLoadApp, highLoadLowFanApp, highLoadFullFanApp)
	}
	if highLoadLowFanApp <= 5.0 {
		t.Fatalf("low-fan tower at peak heat rejection cannot hold tight approach, got %.2fC", highLoadLowFanApp)
	}
}

func TestMismatchCHWCoilFlowCouplingCOPAndSensorBias(t *testing.T) {
	t.Parallel()

	// Raise CHW supply to 10.5C under 900 kW load: slow CHWP causes zone drift, raising CHWP restores zone stability.
	cfg := testPlantConfig()
	cfg.Mismatch = PlantMismatchConfig{
		CHWCoilFlowCoupling:  true,
		CHWCoilApproachMinC:  4.5,
		ChillerCOPMultiplier: 0.85,
		CHWSupplySensorBiasC: 0.3,
		ZoneTempSensorBiasC:  0.3,
	}
	inputs := ScenarioInputs{AmbientDryBulbC: 34, AmbientWetBulbC: 27, CoolingLoadKW: 900}
	scenario := Scenario{SchemaVersion: ScenarioSchemaVersion, Mode: ScenarioModeStatic, Inputs: &inputs}
	plant := NewPlant(cfg, scenario, time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))

	_ = plant.ApplyCommand(Command{DeviceID: cfg.Chiller.ID, Method: "setChilledWaterTemperatureSetpoint", Params: map[string]float64{"setpointC": 11.0}})
	_ = plant.ApplyCommand(Command{DeviceID: cfg.ChilledWaterPump.ID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 25}})
	lowFlowSnap := plant.Tick(90 * time.Minute)
	lowFlowTruth := plant.TruthState()

	// Sensor bias appears in Snapshot telemetry, NOT in TruthState.
	obsZone := lowFlowSnap.Devices[cfg.ZoneSensorID]["zoneTemperatureC"].(float64)
	if math.Abs((obsZone-lowFlowTruth.TrueZoneTempC)-0.3) > 0.01 {
		t.Fatalf("zone sensor bias mismatch: obs=%.3f true=%.3f", obsZone, lowFlowTruth.TrueZoneTempC)
	}
	obsCHW := lowFlowSnap.Devices[cfg.BTUMeterID]["supplyWaterTemperatureC"].(float64)
	if math.Abs((obsCHW-lowFlowTruth.TrueCHWSupplyC)-0.3) > 0.01 {
		t.Fatalf("CHW supply sensor bias mismatch: obs=%.3f true=%.3f", obsCHW, lowFlowTruth.TrueCHWSupplyC)
	}
	if lowFlowTruth.TrueZoneTempC <= 25.0 {
		t.Fatalf("expected true zone temp > 25.0C when CHW=11.0C and CHWP=25Hz under 900kW load, got %.3fC", lowFlowTruth.TrueZoneTempC)
	}

	// Raising CHWP back to 50 Hz restores coil capacity and pulls true zone back below 25.0C.
	_ = plant.ApplyCommand(Command{DeviceID: cfg.ChilledWaterPump.ID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 50}})
	plant.Tick(90 * time.Minute)
	recoveredTruth := plant.TruthState()
	if recoveredTruth.TrueZoneTempC >= lowFlowTruth.TrueZoneTempC || recoveredTruth.TrueZoneTempC >= 25.0 {
		t.Fatalf("expected higher CHWP flow to recover zone comfort below 25.0C, got %.3fC (from %.3fC)", recoveredTruth.TrueZoneTempC, lowFlowTruth.TrueZoneTempC)
	}

	// Verify -15% COP mismatch against baseline (without mismatch).
	nomCfg := testPlantConfig()
	nomPlant := NewPlant(nomCfg, scenario, time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))
	nomPlant.Tick(30 * time.Minute)
	misCfg := testPlantConfig()
	misCfg.Mismatch.ChillerCOPMultiplier = 0.85
	misPlant := NewPlant(misCfg, scenario, time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC))
	misPlant.Tick(30 * time.Minute)
	ratio := misPlant.TruthState().ChillerCOP / nomPlant.TruthState().ChillerCOP
	if math.Abs(ratio-0.85) > 0.02 {
		t.Fatalf("expected ~0.85 COP multiplier ratio, got %.4f", ratio)
	}
}

func TestSevenDayPlantMismatchBenchmarkBaseline(t *testing.T) {
	spec := DefaultSevenDayMismatchSpec()
	ctx := context.Background()

	arms := []BenchmarkArm{
		&FixedBaselineArm{TowerFanPct: 75},
		&LegacyLinearHeuristicArm{},
	}

	results := make([]BenchmarkMetrics, 0, len(arms))
	for _, arm := range arms {
		metrics, err := RunPlantBenchmark(ctx, spec, arm)
		if err != nil {
			t.Fatalf("benchmark arm %s failed: %v", arm.Name(), err)
		}
		if metrics.ElapsedMs >= 5000 {
			t.Fatalf("arm %s exceeded 5s wall-clock budget: %d ms", arm.Name(), metrics.ElapsedMs)
		}
		if metrics.FalseTrips != 0 {
			t.Fatalf("arm %s experienced %d false trips", arm.Name(), metrics.FalseTrips)
		}
		if metrics.InterlockViolations != 0 {
			t.Fatalf("arm %s experienced %d interlock violations", arm.Name(), metrics.InterlockViolations)
		}
		if !metrics.OutageHandledCleanly {
			t.Fatalf("arm %s failed 2h cloud outage stability check: %+v", arm.Name(), metrics)
		}
		if !metrics.LocalOverrideHandledCleanly {
			t.Fatalf("arm %s failed LOCAL manual override & recovery check: %+v", arm.Name(), metrics)
		}
		results = append(results, metrics)
	}

	if results[0].ZoneExceedMinutes != 0 {
		t.Fatalf("FixedBaseline expected 0 comfort exceedance minutes, got %.1f", results[0].ZoneExceedMinutes)
	}
	if results[1].TotalEnergyKWh >= results[0].TotalEnergyKWh {
		t.Fatalf("LegacyLinearHeuristic (%.2f kWh) should save energy vs FixedBaseline (%.2f kWh)",
			results[1].TotalEnergyKWh, results[0].TotalEnergyKWh)
	}

	mdTable := FormatBenchmarkMarkdownTable(results)
	jsonReport, err := FormatBenchmarkJSON(results)
	if err != nil {
		t.Fatalf("FormatBenchmarkJSON failed: %v", err)
	}
	t.Logf("\n=== 7-Day Plant-Model Mismatch Benchmark (Markdown) ===\n%s\n=== 7-Day Plant-Model Mismatch Benchmark (JSON) ===\n%s", mdTable, jsonReport)
}

func TestBenchmarkHarnessDetectsLocalBypassAndInterlockFaults(t *testing.T) {
	t.Parallel()
	spec := DefaultSevenDayMismatchSpec()
	spec.Duration = 6 * time.Hour
	spec.OutageStart = time.Hour
	spec.OutageDuration = 2 * time.Hour
	spec.LocalOverrideStart = 4 * time.Hour
	spec.LocalOverrideDur = time.Hour

	plant, err := NewCoupledChillerPlant(spec.PlantConfig, spec.InputsFor, spec.StartLocal.UTC())
	if err != nil {
		t.Fatal(err)
	}
	plant.SetControlCabinetMode("CHWP-01", ControlCabinetModeLocal)
	if !plant.SetLocalManualTarget("CHWP-01", 46.0) {
		t.Fatal("SetLocalManualTarget failed")
	}
	if res := plant.ApplyCommand(Command{DeviceID: "CHWP-01", Method: "setFrequency", Params: map[string]float64{"frequencyHz": 32}}); res.Success || res.Code != "CABINET_LOCAL_MANUAL" {
		t.Fatalf("expected CABINET_LOCAL_MANUAL rejection while in LOCAL mode, got %+v", res)
	}
	if got := plant.TruthState().CHWPFreqSetpointHz; got != 46.0 {
		t.Fatalf("LOCAL manual dial target mutated to %.2f, want 46.0", got)
	}
}
