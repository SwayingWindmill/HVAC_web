package simulator

import (
	"math"
	"testing"
	"time"
)

func testPlantConfig() PlantConfig {
	return PlantConfig{
		AmbientDryBulbC:  34,
		AmbientWetBulbC:  27,
		LoadFraction:     0.72,
		Chiller:          ChillerConfig{ID: "CHILLER-01", RatedCoolingCapacityKW: 1200, BaseCOP: 5.6, InitialSetpointC: 7, InitialLoadLimitPct: 100, InitiallyRunning: true},
		ChilledWaterPump: PumpConfig{ID: "CHWP-01", RatedPowerKW: 45, RatedFlowM3H: 220, InitialFrequencyHz: 50, InitiallyRunning: true},
		CoolingWaterPump: PumpConfig{ID: "CWP-01", RatedPowerKW: 37, RatedFlowM3H: 260, InitialFrequencyHz: 50, InitiallyRunning: true},
		CoolingTower:     CoolingTowerConfig{ID: "CT-01", RatedFanPowerKW: 18.5, InitialFanSpeedPct: 80, InitiallyRunning: true},
		PowerMeterID:     "METER-HVAC-TOTAL",
		BTUMeterID:       "BTU-METER-01",
		WeatherStationID: "WEATHER-STATION-01",
	}
}

func TestPlantTickProducesCentralPlantEnergyBalance(t *testing.T) {
	plant := NewPlant(testPlantConfig(), time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC))
	snapshot := plant.Tick(time.Minute)
	chiller := snapshot.Devices["CHILLER-01"]
	powerMeter := snapshot.Devices["METER-HVAC-TOTAL"]
	btuMeter := snapshot.Devices["BTU-METER-01"]

	if chiller["runState"] != "RUNNING" {
		t.Fatalf("expected running chiller, got %v", chiller["runState"])
	}
	if chiller["coolingCapacityKw"].(float64) <= 0 || chiller["powerKw"].(float64) <= 0 || chiller["cop"].(float64) < 2.5 {
		t.Fatalf("unexpected chiller telemetry: %#v", chiller)
	}
	if powerMeter["activePowerKw"].(float64) <= chiller["powerKw"].(float64) {
		t.Fatalf("plant power must include auxiliaries: %#v", powerMeter)
	}
	if btuMeter["instantCoolingCapacityKw"] != chiller["coolingCapacityKw"] {
		t.Fatalf("BTU meter and chiller capacity diverged: %#v %#v", btuMeter, chiller)
	}
}

func TestPumpAffinityLawReducesPowerAtEightyPercentSpeed(t *testing.T) {
	plant := NewPlant(testPlantConfig(), time.Now())
	plant.Tick(time.Second)
	fullPower := plant.Snapshot().Devices["CHWP-01"]["powerKw"].(float64)
	result := plant.ApplyCommand(Command{DeviceID: "CHWP-01", Method: "setFrequency", Params: map[string]float64{"frequencyHz": 40}})
	if !result.Success {
		t.Fatalf("frequency command failed: %#v", result)
	}
	plant.Tick(time.Second)
	reducedPower := plant.Snapshot().Devices["CHWP-01"]["powerKw"].(float64)
	wantRatio := math.Pow(0.8, 3)
	if math.Abs(reducedPower/fullPower-wantRatio) > 0.002 {
		t.Fatalf("pump affinity law mismatch: got %.4f want %.4f", reducedPower/fullPower, wantRatio)
	}
}

func TestChillerSetpointCommandIsValidatedAndRevisioned(t *testing.T) {
	plant := NewPlant(testPlantConfig(), time.Now())
	accepted := plant.ApplyCommand(Command{DeviceID: "CHILLER-01", Method: "setChilledWaterTemperatureSetpoint", Params: map[string]float64{"setpointC": 8.5}})
	if !accepted.Success || accepted.AppliedValue != 8.5 || accepted.BusinessRevision != 2 {
		t.Fatalf("unexpected accepted result: %#v", accepted)
	}
	rejected := plant.ApplyCommand(Command{DeviceID: "CHILLER-01", Method: "setChilledWaterTemperatureSetpoint", Params: map[string]float64{"setpointC": 3}})
	if rejected.Success || rejected.Code != "SETPOINT_OUT_OF_RANGE" || rejected.BusinessRevision != 2 {
		t.Fatalf("unexpected rejected result: %#v", rejected)
	}
	if got := plant.Snapshot().Devices["CHILLER-01"]["chilledWaterTemperatureSetpointC"]; got != 8.5 {
		t.Fatalf("rejected command mutated setpoint: %v", got)
	}
}

func TestFaultedCoolingWaterPumpStopsCoolingProduction(t *testing.T) {
	plant := NewPlant(testPlantConfig(), time.Now())
	if !plant.SetFault("CWP-01", "DRIVE_TRIP") {
		t.Fatal("expected fault injection to target CWP-01")
	}
	snapshot := plant.Tick(time.Minute)
	if got := snapshot.Devices["CWP-01"]["runState"]; got != "FAULT" {
		t.Fatalf("expected fault state, got %v", got)
	}
	if got := snapshot.Devices["CHILLER-01"]["coolingCapacityKw"].(float64); got != 0 {
		t.Fatalf("chiller produced cooling without condenser water flow: %v", got)
	}
}

func TestCommandRejectsUnexpectedParametersWithoutMutation(t *testing.T) {
	plant := NewPlant(testPlantConfig(), time.Now())
	result := plant.ApplyCommand(Command{
		DeviceID: "CHWP-01",
		Method:   "setFrequency",
		Params:   map[string]float64{"frequencyHz": 40, "unexpected": 1},
	})
	if result.Success || result.Code != "INVALID_PARAMETERS" || result.BusinessRevision != 1 {
		t.Fatalf("unexpected command result: %#v", result)
	}
	if got := plant.Snapshot().Devices["CHWP-01"]["frequencyHz"]; got != 50.0 {
		t.Fatalf("invalid command mutated frequency: %v", got)
	}
}
