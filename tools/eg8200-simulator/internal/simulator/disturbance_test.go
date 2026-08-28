package simulator

import (
	"math"
	"testing"
	"time"
)

func TestCHWPStuckHighDisturbanceProducesLowDeltaTAndRecovers(t *testing.T) {
	config := testPlantConfig()
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))

	lowerTarget := plant.ApplyCommand(Command{
		DeviceID: config.ChilledWaterPump.ID,
		Method:   "setFrequency",
		Params:   map[string]float64{"frequencyHz": 30},
	})
	if !lowerTarget.Success || lowerTarget.AppliedValue != 30 {
		t.Fatalf("lower governed CHWP target was not accepted: %#v", lowerTarget)
	}

	healthy := plant.Tick(10 * time.Minute)
	healthyCHWP := healthy.Devices[config.ChilledWaterPump.ID]
	healthyBTU := healthy.Devices[config.BTUMeterID]
	healthyFrequency := healthyCHWP["frequencyHz"].(float64)
	healthyFlow := healthyCHWP["flowRateM3h"].(float64)
	healthyDeltaT := healthyBTU["temperatureDifferenceC"].(float64)
	if math.Abs(healthyFrequency-30) > 0.5 {
		t.Fatalf("healthy CHWP did not settle near governed target: %.3f Hz", healthyFrequency)
	}
	if healthyDeltaT <= 5 {
		t.Fatalf("healthy lower-flow baseline must stay above low-delta-T threshold: %.3f C", healthyDeltaT)
	}
	assertWaterSideEnergyBalance(t, healthy, config)

	plant.SetCHWPStuckHighDisturbance(true)
	disturbed := plant.Tick(3 * time.Minute)
	disturbedCHWP := disturbed.Devices[config.ChilledWaterPump.ID]
	disturbedBTU := disturbed.Devices[config.BTUMeterID]
	disturbedFrequency := disturbedCHWP["frequencyHz"].(float64)
	disturbedFlow := disturbedCHWP["flowRateM3h"].(float64)
	disturbedDeltaT := disturbedBTU["temperatureDifferenceC"].(float64)

	if disturbedFrequency < 49.5 {
		t.Fatalf("stuck-high CHWP actual frequency did not remain high: %.3f Hz", disturbedFrequency)
	}
	if disturbedFlow <= healthyFlow {
		t.Fatalf("stuck-high CHWP did not increase chilled-water flow: healthy %.3f disturbed %.3f m3/h", healthyFlow, disturbedFlow)
	}
	if disturbedDeltaT >= healthyDeltaT || disturbedDeltaT >= 5 {
		t.Fatalf("stuck-high CHWP did not physically reduce delta-T: healthy %.3f disturbed %.3f C", healthyDeltaT, disturbedDeltaT)
	}
	assertWaterSideEnergyBalance(t, disturbed, config)

	plant.SetCHWPStuckHighDisturbance(false)
	recovered := plant.Tick(3 * time.Minute)
	recoveredCHWP := recovered.Devices[config.ChilledWaterPump.ID]
	recoveredBTU := recovered.Devices[config.BTUMeterID]
	recoveredFrequency := recoveredCHWP["frequencyHz"].(float64)
	recoveredFlow := recoveredCHWP["flowRateM3h"].(float64)
	recoveredDeltaT := recoveredBTU["temperatureDifferenceC"].(float64)

	if math.Abs(recoveredFrequency-30) > 0.5 {
		t.Fatalf("CHWP actual frequency did not recover to governed target: %.3f Hz", recoveredFrequency)
	}
	if recoveredFlow >= disturbedFlow || recoveredDeltaT <= disturbedDeltaT || recoveredDeltaT <= 5 {
		t.Fatalf("physical recovery did not restore lower flow and higher delta-T: disturbed flow/deltaT %.3f/%.3f recovered %.3f/%.3f", disturbedFlow, disturbedDeltaT, recoveredFlow, recoveredDeltaT)
	}
	assertWaterSideEnergyBalance(t, recovered, config)
}

func TestCHWPStuckHighDisturbanceUsesOnlyProductionObservations(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	plant := NewPlant(config.Plant, config.Scenario, time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC))
	plant.SetCHWPStuckHighDisturbance(true)
	snapshot := plant.Tick(3 * time.Minute)

	required := map[string][]string{
		config.Plant.BTUMeterID: {
			"supplyWaterTemperatureC",
			"returnWaterTemperatureC",
			"flowRateM3h",
		},
		config.Plant.ChilledWaterPump.ID: {
			"frequencyHz",
			"flowRateM3h",
		},
		config.Plant.Chiller.ID: {
			"coolingCapacityKw",
			"runState",
		},
	}
	for deviceID, sourceKeys := range required {
		telemetry := snapshot.Devices[deviceID]
		for _, sourceKey := range sourceKeys {
			if _, ok := telemetry[sourceKey]; !ok {
				t.Fatalf("required production observation %s/%s is missing from Plant snapshot", deviceID, sourceKey)
			}
			if !configHasObservedSourceKey(config, deviceID, sourceKey) {
				t.Fatalf("required production observation %s/%s is not backed by a canonical observed Point", deviceID, sourceKey)
			}
		}
	}

	for deviceID, telemetry := range snapshot.Devices {
		for _, forbidden := range []string{"simulatorFault", "faultInjected", "expectedDiagnosis", "syntheticDeltaT", "alarm", "fdd", "workOrder", "workOrderId"} {
			if _, exists := telemetry[forbidden]; exists {
				t.Fatalf("simulator-private business field %s leaked from device %s", forbidden, deviceID)
			}
		}
	}
}

func configHasObservedSourceKey(config Config, deviceID, sourceKey string) bool {
	for _, point := range config.Points {
		if point.DeviceID == deviceID && point.SourceKey == sourceKey && point.PointType != "COMMAND" {
			return true
		}
	}
	return false
}
