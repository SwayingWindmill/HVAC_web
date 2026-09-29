package simulator

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func testPlantConfig() PlantConfig {
	return PlantConfig{
		Chiller:          ChillerConfig{ID: "CHILLER-01", RatedCoolingCapacityKW: 1200, BaseCOP: 5.6, InitialSetpointC: 7, InitialLoadLimitPct: 100, InitiallyRunning: true},
		ChilledWaterPump: PumpConfig{ID: "CHWP-01", RatedPowerKW: 45, RatedFlowM3H: 220, InitialFrequencyHz: 50, InitiallyRunning: true},
		CoolingWaterPump: PumpConfig{ID: "CWP-01", RatedPowerKW: 37, RatedFlowM3H: 260, InitialFrequencyHz: 50, InitiallyRunning: true},
		CoolingTower:     CoolingTowerConfig{ID: "CT-01", RatedFanPowerKW: 18.5, InitialFanSpeedPct: 80, InitiallyRunning: true},
		PowerMeterID:     "METER-HVAC-TOTAL",
		BTUMeterID:       "BTU-METER-01",
		WeatherStationID: "WEATHER-STATION-01",
	}
}

func testStaticScenario() Scenario {
	inputs := ScenarioInputs{AmbientDryBulbC: 34, AmbientWetBulbC: 27, CoolingLoadKW: 864}
	return Scenario{SchemaVersion: ScenarioSchemaVersion, Mode: ScenarioModeStatic, Inputs: &inputs}
}

func assertWaterSideEnergyBalance(t *testing.T, snapshot Snapshot, config PlantConfig) {
	t.Helper()
	btu := snapshot.Devices[config.BTUMeterID]
	flowM3H := btu["flowRateM3h"].(float64)
	deltaTC := btu["temperatureDifferenceC"].(float64)
	capacityKW := btu["instantCoolingCapacityKw"].(float64)
	capacityFromWaterSideKW := 1.163 * flowM3H * deltaTC
	if math.Abs(capacityFromWaterSideKW-capacityKW) > math.Max(capacityKW*0.01, 0.5) {
		t.Fatalf("water-side energy balance diverged: flow %.3f m3/h deltaT %.3f C implies %.3f kW, reported %.3f kW", flowM3H, deltaTC, capacityFromWaterSideKW, capacityKW)
	}
}

func TestPlantTickProducesCentralPlantEnergyBalance(t *testing.T) {
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC))
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

func TestPlantCoolingLoadIsAbsoluteDemandAcrossChillerRatings(t *testing.T) {
	scenario := testStaticScenario()
	scenario.Inputs.CoolingLoadKW = 600

	baseConfig := testPlantConfig()
	largerConfig := testPlantConfig()
	largerConfig.Chiller.RatedCoolingCapacityKW = 1800

	base := NewPlant(baseConfig, scenario, time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))
	larger := NewPlant(largerConfig, scenario, time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))
	baseCapacity := base.Tick(30 * time.Minute).Devices[baseConfig.Chiller.ID]["coolingCapacityKw"].(float64)
	largerCapacity := larger.Tick(30 * time.Minute).Devices[largerConfig.Chiller.ID]["coolingCapacityKw"].(float64)

	if math.Abs(baseCapacity-600) > 5 || math.Abs(largerCapacity-600) > 5 {
		t.Fatalf("coolingLoadKw must remain absolute demand: base %.3f kW larger %.3f kW", baseCapacity, largerCapacity)
	}
}

func TestPlantChilledWaterTelemetryObeysEnergyBalance(t *testing.T) {
	config := testPlantConfig()
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))
	assertWaterSideEnergyBalance(t, plant.Tick(30*time.Minute), config)
}

func TestActuatorCommandsChangePhysicalReadbackOverTime(t *testing.T) {
	config := testPlantConfig()
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))
	plant.Tick(10 * time.Minute)
	before := plant.Snapshot()

	pumpResult := plant.ApplyCommand(Command{DeviceID: config.ChilledWaterPump.ID, Method: "setFrequency", Params: map[string]float64{"frequencyHz": 30}})
	towerResult := plant.ApplyCommand(Command{DeviceID: config.CoolingTower.ID, Method: "setFanSpeed", Params: map[string]float64{"fanSpeedPct": 40}})
	if !pumpResult.Success || !towerResult.Success {
		t.Fatalf("actuator command failed: pump %#v tower %#v", pumpResult, towerResult)
	}

	immediate := plant.Snapshot()
	if got, want := immediate.Devices[config.ChilledWaterPump.ID]["frequencyHz"], before.Devices[config.ChilledWaterPump.ID]["frequencyHz"]; got != want {
		t.Fatalf("pump actual frequency changed before physical time advanced: got %v want %v", got, want)
	}
	if got, want := immediate.Devices[config.CoolingTower.ID]["fanSpeedPct"], before.Devices[config.CoolingTower.ID]["fanSpeedPct"]; got != want {
		t.Fatalf("tower actual fan speed changed before physical time advanced: got %v want %v", got, want)
	}

	mid := plant.Tick(5 * time.Second)
	midPump := mid.Devices[config.ChilledWaterPump.ID]["frequencyHz"].(float64)
	midFan := mid.Devices[config.CoolingTower.ID]["fanSpeedPct"].(float64)
	if !(midPump > 30 && midPump < 50) || !(midFan > 40 && midFan < 80) {
		t.Fatalf("actuators did not ramp toward targets: pump %.3f Hz fan %.3f%%", midPump, midFan)
	}

	settled := plant.Tick(3 * time.Minute)
	if got := settled.Devices[config.ChilledWaterPump.ID]["frequencyHz"].(float64); math.Abs(got-30) > 0.5 {
		t.Fatalf("pump did not settle near commanded frequency: %.3f", got)
	}
	if got := settled.Devices[config.CoolingTower.ID]["fanSpeedPct"].(float64); math.Abs(got-40) > 0.5 {
		t.Fatalf("tower did not settle near commanded fan speed: %.3f", got)
	}
}

func TestPlantLoadStepProducesDynamicResponseAndRecovery(t *testing.T) {
	config := testPlantConfig()
	scenario := Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		Mode:          ScenarioModeScenario,
		Steps: []ScenarioStep{
			{Offset: 0, Inputs: ScenarioInputs{AmbientDryBulbC: 32, AmbientWetBulbC: 25, CoolingLoadKW: 420}},
			{Offset: 10 * time.Minute, Inputs: ScenarioInputs{AmbientDryBulbC: 35, AmbientWetBulbC: 27, CoolingLoadKW: 900}},
			{Offset: 20 * time.Minute, Inputs: ScenarioInputs{AmbientDryBulbC: 32, AmbientWetBulbC: 25, CoolingLoadKW: 420}},
		},
	}
	plant := NewPlant(config, scenario, time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))

	low := plant.Tick(10 * time.Minute)
	assertWaterSideEnergyBalance(t, low, config)
	lowCapacity := low.Devices[config.Chiller.ID]["coolingCapacityKw"].(float64)
	lowPower := low.Devices[config.Chiller.ID]["powerKw"].(float64)
	lowReturn := low.Devices[config.BTUMeterID]["returnWaterTemperatureC"].(float64)

	rising := plant.Tick(10 * time.Second)
	assertWaterSideEnergyBalance(t, rising, config)
	risingCapacity := rising.Devices[config.Chiller.ID]["coolingCapacityKw"].(float64)
	risingPower := rising.Devices[config.Chiller.ID]["powerKw"].(float64)
	risingReturn := rising.Devices[config.BTUMeterID]["returnWaterTemperatureC"].(float64)
	if risingCapacity <= lowCapacity || risingCapacity >= 890 || risingPower <= lowPower || risingReturn <= lowReturn {
		t.Fatalf("load step must produce a time response instead of an instantaneous jump: capacity %.3f -> %.3f, power %.3f -> %.3f, return %.3f -> %.3f", lowCapacity, risingCapacity, lowPower, risingPower, lowReturn, risingReturn)
	}

	high := plant.Tick(9*time.Minute + 50*time.Second)
	assertWaterSideEnergyBalance(t, high, config)
	highCapacity := high.Devices[config.Chiller.ID]["coolingCapacityKw"].(float64)
	highPower := high.Devices[config.Chiller.ID]["powerKw"].(float64)
	highReturn := high.Devices[config.BTUMeterID]["returnWaterTemperatureC"].(float64)
	if highCapacity < 850 || highPower <= risingPower || highReturn <= risingReturn {
		t.Fatalf("plant did not reach higher-load state: capacity %.3f -> %.3f, power %.3f -> %.3f, return %.3f -> %.3f", risingCapacity, highCapacity, risingPower, highPower, risingReturn, highReturn)
	}

	falling := plant.Tick(10 * time.Second)
	assertWaterSideEnergyBalance(t, falling, config)
	fallingCapacity := falling.Devices[config.Chiller.ID]["coolingCapacityKw"].(float64)
	if fallingCapacity >= highCapacity || fallingCapacity <= 430 {
		t.Fatalf("load recovery must also be dynamic: high %.3f falling %.3f", highCapacity, fallingCapacity)
	}

	recovered := plant.Tick(10 * time.Minute)
	assertWaterSideEnergyBalance(t, recovered, config)
	recoveredCapacity := recovered.Devices[config.Chiller.ID]["coolingCapacityKw"].(float64)
	recoveredReturn := recovered.Devices[config.BTUMeterID]["returnWaterTemperatureC"].(float64)
	if math.Abs(recoveredCapacity-420) > 10 || recoveredReturn >= highReturn {
		t.Fatalf("plant did not recover toward normal load: capacity %.3f return %.3f (high return %.3f)", recoveredCapacity, recoveredReturn, highReturn)
	}
}

func TestPlantContinuesFromConfiguredCumulativeEnergy(t *testing.T) {
	config := testPlantConfig()
	config.InitialEnergyKWh = 1250000
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC))
	before := plant.Snapshot().Devices[config.PowerMeterID]["energyKwh"].(float64)
	if before != config.InitialEnergyKWh {
		t.Fatalf("initial cumulative energy mismatch: got %.6f want %.6f", before, config.InitialEnergyKWh)
	}
	after := plant.Tick(time.Hour).Devices[config.PowerMeterID]["energyKwh"].(float64)
	if after <= before {
		t.Fatalf("cumulative energy did not continue from configured value: before %.6f after %.6f", before, after)
	}
}

func TestPumpAffinityLawReducesPowerAtEightyPercentSpeed(t *testing.T) {
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Now())
	plant.Tick(time.Second)
	fullPower := plant.Snapshot().Devices["CHWP-01"]["powerKw"].(float64)
	result := plant.ApplyCommand(Command{DeviceID: "CHWP-01", Method: "setFrequency", Params: map[string]float64{"frequencyHz": 40}})
	if !result.Success {
		t.Fatalf("frequency command failed: %#v", result)
	}
	plant.Tick(2 * time.Minute)
	reducedPower := plant.Snapshot().Devices["CHWP-01"]["powerKw"].(float64)
	wantRatio := math.Pow(0.8, 3)
	if math.Abs(reducedPower/fullPower-wantRatio) > 0.002 {
		t.Fatalf("pump affinity law mismatch: got %.4f want %.4f", reducedPower/fullPower, wantRatio)
	}
}

func TestChillerSetpointCommandIsValidated(t *testing.T) {
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Now())
	accepted := plant.ApplyCommand(Command{DeviceID: "CHILLER-01", Method: "setChilledWaterTemperatureSetpoint", Params: map[string]float64{"setpointC": 8.5}})
	if !accepted.Success || accepted.AppliedValue != 8.5 {
		t.Fatalf("unexpected accepted result: %#v", accepted)
	}
	rejected := plant.ApplyCommand(Command{DeviceID: "CHILLER-01", Method: "setChilledWaterTemperatureSetpoint", Params: map[string]float64{"setpointC": 3}})
	if rejected.Success || rejected.Code != "SETPOINT_OUT_OF_RANGE" {
		t.Fatalf("unexpected rejected result: %#v", rejected)
	}
	if got := plant.Snapshot().Devices["CHILLER-01"]["chilledWaterTemperatureSetpointC"]; got != 8.5 {
		t.Fatalf("rejected command mutated setpoint: %v", got)
	}
}

func TestFaultedCoolingWaterPumpStopsCoolingProduction(t *testing.T) {
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Now())
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
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Now())
	result := plant.ApplyCommand(Command{
		DeviceID: "CHWP-01",
		Method:   "setFrequency",
		Params:   map[string]float64{"frequencyHz": 40, "unexpected": 1},
	})
	if result.Success || result.Code != "INVALID_PARAMETERS" {
		t.Fatalf("unexpected command result: %#v", result)
	}
	if got := plant.Snapshot().Devices["CHWP-01"]["frequencyHz"]; got != 50.0 {
		t.Fatalf("invalid command mutated frequency: %v", got)
	}
}

func TestSimulatorDoesNotExposeBusinessRevision(t *testing.T) {
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Now())
	snapshot := plant.Tick(time.Second)
	for deviceID, telemetry := range snapshot.Devices {
		if _, exists := telemetry["businessRevision"]; exists {
			t.Fatalf("simulator device %s exposed businessRevision", deviceID)
		}
	}

	result := plant.ApplyCommand(Command{DeviceID: "CHWP-01", Method: "setFrequency", Params: map[string]float64{"frequencyHz": 40}})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal command result: %v", err)
	}
	if strings.Contains(string(encoded), "businessRevision") {
		t.Fatalf("simulator command result exposed businessRevision: %s", encoded)
	}
}
