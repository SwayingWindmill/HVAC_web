package simulator

import (
	"math"
	"sync"
	"time"
)

type DeviceTelemetry map[string]any

type Snapshot struct {
	ObservedAt time.Time
	Devices    map[string]DeviceTelemetry
}

type Command struct {
	DeviceID string
	Method   string
	Params   map[string]float64
}

type CommandResult struct {
	Success      bool    `json:"success"`
	Code         string  `json:"code"`
	AppliedValue float64 `json:"appliedValue,omitempty"`
}

type Plant struct {
	mu              sync.RWMutex
	config          PlantConfig
	scenario        Scenario
	scenarioElapsed time.Duration
	inputs          ScenarioInputs
	now             time.Time

	chiller          chillerState
	chilledWaterPump pumpState
	coolingWaterPump pumpState
	coolingTower     coolingTowerState

	totalEnergyKWh        float64
	totalCoolingEnergyKWh float64
}

type chillerState struct {
	running               bool
	setpointC             float64
	loadLimitPct          float64
	leavingChilledWaterC  float64
	enteringChilledWaterC float64
	leavingCoolingWaterC  float64
	enteringCoolingWaterC float64
	loadPct               float64
	coolingCapacityKW     float64
	powerKW               float64
	cop                   float64
	faultCode             string
}

type pumpState struct {
	running     bool
	frequencyHz float64
	flowM3H     float64
	powerKW     float64
	faultCode   string
}

type coolingTowerState struct {
	running        bool
	fanSpeedPct    float64
	enteringWaterC float64
	leavingWaterC  float64
	powerKW        float64
	faultCode      string
}

func NewPlant(config PlantConfig, scenario Scenario, now time.Time) *Plant {
	return &Plant{
		config:         config,
		scenario:       scenario,
		inputs:         scenario.InputsAt(0),
		now:            now.UTC(),
		totalEnergyKWh: config.InitialEnergyKWh,
		chiller: chillerState{
			running:               config.Chiller.InitiallyRunning,
			setpointC:             config.Chiller.InitialSetpointC,
			loadLimitPct:          config.Chiller.InitialLoadLimitPct,
			leavingChilledWaterC:  config.Chiller.InitialSetpointC + 1,
			enteringChilledWaterC: config.Chiller.InitialSetpointC + 6,
		},
		chilledWaterPump: pumpState{
			running:     config.ChilledWaterPump.InitiallyRunning,
			frequencyHz: config.ChilledWaterPump.InitialFrequencyHz,
		},
		coolingWaterPump: pumpState{
			running:     config.CoolingWaterPump.InitiallyRunning,
			frequencyHz: config.CoolingWaterPump.InitialFrequencyHz,
		},
		coolingTower: coolingTowerState{
			running:     config.CoolingTower.InitiallyRunning,
			fanSpeedPct: config.CoolingTower.InitialFanSpeedPct,
		},
	}
}

func (plant *Plant) Tick(elapsed time.Duration) Snapshot {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	if elapsed <= 0 {
		return plant.snapshotLocked()
	}

	remaining := elapsed
	for remaining > 0 {
		plant.inputs = plant.scenario.InputsAt(plant.scenarioElapsed)
		segment := remaining
		if transition, ok := plant.scenario.nextTransitionAfter(plant.scenarioElapsed); ok {
			untilTransition := transition - plant.scenarioElapsed
			if untilTransition < segment {
				segment = untilTransition
			}
		}
		plant.advanceLocked(segment)
		plant.scenarioElapsed += segment
		remaining -= segment
	}
	plant.inputs = plant.scenario.InputsAt(plant.scenarioElapsed)
	return plant.snapshotLocked()
}

func (plant *Plant) advanceLocked(elapsed time.Duration) {
	plant.now = plant.now.Add(elapsed)
	plant.updatePump(&plant.chilledWaterPump, plant.config.ChilledWaterPump)
	plant.updatePump(&plant.coolingWaterPump, plant.config.CoolingWaterPump)
	plant.updateCoolingTower()
	plant.updateChiller(elapsed)

	totalPowerKW := plant.chiller.powerKW + plant.chilledWaterPump.powerKW + plant.coolingWaterPump.powerKW + plant.coolingTower.powerKW
	plant.totalEnergyKWh += totalPowerKW * elapsed.Hours()
	plant.totalCoolingEnergyKWh += plant.chiller.coolingCapacityKW * elapsed.Hours()
}

func (plant *Plant) Snapshot() Snapshot {
	plant.mu.RLock()
	defer plant.mu.RUnlock()
	return plant.snapshotLocked()
}

func (plant *Plant) updatePump(state *pumpState, config PumpConfig) {
	if !state.running || state.faultCode != "" {
		state.flowM3H = 0
		state.powerKW = 0
		return
	}
	speedFraction := clamp(state.frequencyHz/50, 0, 1)
	state.flowM3H = config.RatedFlowM3H * speedFraction
	state.powerKW = config.RatedPowerKW * math.Pow(speedFraction, 3)
}

func (plant *Plant) updateCoolingTower() {
	state := &plant.coolingTower
	if !state.running || state.faultCode != "" {
		state.powerKW = 0
		state.enteringWaterC = plant.inputs.AmbientWetBulbC + 10
		state.leavingWaterC = state.enteringWaterC
		return
	}
	speedFraction := clamp(state.fanSpeedPct/100, 0, 1)
	loadRatio := plant.inputs.CoolingLoadKW / plant.config.Chiller.RatedCoolingCapacityKW
	state.powerKW = plant.config.CoolingTower.RatedFanPowerKW * math.Pow(speedFraction, 3)
	state.enteringWaterC = plant.inputs.AmbientWetBulbC + 10 + 2*loadRatio
	approachC := 2.5 + (1-speedFraction)*6
	state.leavingWaterC = plant.inputs.AmbientWetBulbC + approachC
}

func (plant *Plant) updateChiller(elapsed time.Duration) {
	state := &plant.chiller
	chilledWaterFlowFraction := clamp(plant.chilledWaterPump.flowM3H/plant.config.ChilledWaterPump.RatedFlowM3H, 0, 1)
	coolingWaterFlowFraction := clamp(plant.coolingWaterPump.flowM3H/plant.config.CoolingWaterPump.RatedFlowM3H, 0, 1)
	flowFraction := math.Min(chilledWaterFlowFraction, coolingWaterFlowFraction)
	available := state.running && state.faultCode == "" && flowFraction >= 0.35 && plant.coolingTower.running && plant.coolingTower.faultCode == ""
	if !available {
		state.loadPct = 0
		state.coolingCapacityKW = 0
		state.powerKW = 0
		state.cop = 0
		state.leavingCoolingWaterC = plant.coolingTower.enteringWaterC
		state.enteringCoolingWaterC = plant.coolingTower.leavingWaterC
		state.leavingChilledWaterC = approach(state.leavingChilledWaterC, state.enteringChilledWaterC, elapsed, 12*time.Minute)
		return
	}
	requestedCapacityKW := plant.inputs.CoolingLoadKW
	limitCapacityKW := plant.config.Chiller.RatedCoolingCapacityKW * state.loadLimitPct / 100
	state.coolingCapacityKW = math.Min(requestedCapacityKW, limitCapacityKW) * flowFraction
	state.loadPct = 100 * state.coolingCapacityKW / plant.config.Chiller.RatedCoolingCapacityKW
	loadRatio := requestedCapacityKW / plant.config.Chiller.RatedCoolingCapacityKW
	state.enteringChilledWaterC = state.setpointC + 4 + 2*loadRatio
	state.leavingChilledWaterC = approach(state.leavingChilledWaterC, state.setpointC, elapsed, 4*time.Minute)
	state.enteringCoolingWaterC = plant.coolingTower.leavingWaterC
	state.leavingCoolingWaterC = plant.coolingTower.enteringWaterC

	lowLoadPenalty := 0.0
	if state.loadPct < 35 {
		lowLoadPenalty = (35 - state.loadPct) * 0.025
	}
	state.cop = clamp(
		plant.config.Chiller.BaseCOP+
			0.14*(state.setpointC-6.5)-
			0.045*(state.enteringCoolingWaterC-28)-
			lowLoadPenalty,
		2.5,
		7.5,
	)
	state.powerKW = state.coolingCapacityKW / state.cop
}

func (plant *Plant) snapshotLocked() Snapshot {
	totalPowerKW := plant.chiller.powerKW + plant.chilledWaterPump.powerKW + plant.coolingWaterPump.powerKW + plant.coolingTower.powerKW
	return Snapshot{
		ObservedAt: plant.now,
		Devices: map[string]DeviceTelemetry{
			plant.config.Chiller.ID: {
				"runState":                         runState(plant.chiller.running, plant.chiller.faultCode),
				"leavingChilledWaterTemperatureC":  round(plant.chiller.leavingChilledWaterC, 3),
				"enteringChilledWaterTemperatureC": round(plant.chiller.enteringChilledWaterC, 3),
				"chilledWaterTemperatureSetpointC": round(plant.chiller.setpointC, 3),
				"leavingCoolingWaterTemperatureC":  round(plant.chiller.leavingCoolingWaterC, 3),
				"enteringCoolingWaterTemperatureC": round(plant.chiller.enteringCoolingWaterC, 3),
				"compressorLoadPct":                round(plant.chiller.loadPct, 3),
				"coolingCapacityKw":                round(plant.chiller.coolingCapacityKW, 3),
				"powerKw":                          round(plant.chiller.powerKW, 3),
				"cop":                              round(plant.chiller.cop, 3),
				"loadLimitPct":                     round(plant.chiller.loadLimitPct, 3),
				"faultCode":                        plant.chiller.faultCode,
			},
			plant.config.ChilledWaterPump.ID: pumpTelemetry(plant.chilledWaterPump),
			plant.config.CoolingWaterPump.ID: pumpTelemetry(plant.coolingWaterPump),
			plant.config.CoolingTower.ID: {
				"runState":                   runState(plant.coolingTower.running, plant.coolingTower.faultCode),
				"fanSpeedPct":                round(plant.coolingTower.fanSpeedPct, 3),
				"enteringWaterTemperatureC":  round(plant.coolingTower.enteringWaterC, 3),
				"leavingWaterTemperatureC":   round(plant.coolingTower.leavingWaterC, 3),
				"ambientWetBulbTemperatureC": round(plant.inputs.AmbientWetBulbC, 3),
				"approachTemperatureC":       round(plant.coolingTower.leavingWaterC-plant.inputs.AmbientWetBulbC, 3),
				"powerKw":                    round(plant.coolingTower.powerKW, 3),
				"faultCode":                  plant.coolingTower.faultCode,
			},
			plant.config.PowerMeterID: {
				"activePowerKw": round(totalPowerKW, 3),
				"energyKwh":     round(plant.totalEnergyKWh, 6),
				"powerFactor":   0.93,
				"frequencyHz":   50.0,
			},
			plant.config.BTUMeterID: {
				"supplyWaterTemperatureC":     round(plant.chiller.leavingChilledWaterC, 3),
				"returnWaterTemperatureC":     round(plant.chiller.enteringChilledWaterC, 3),
				"temperatureDifferenceC":      round(plant.chiller.enteringChilledWaterC-plant.chiller.leavingChilledWaterC, 3),
				"flowRateM3h":                 round(plant.chilledWaterPump.flowM3H, 3),
				"instantCoolingCapacityKw":    round(plant.chiller.coolingCapacityKW, 3),
				"accumulatedCoolingEnergyKwh": round(plant.totalCoolingEnergyKWh, 6),
			},
			plant.config.WeatherStationID: {
				"ambientDryBulbTemperatureC": round(plant.inputs.AmbientDryBulbC, 3),
				"ambientWetBulbTemperatureC": round(plant.inputs.AmbientWetBulbC, 3),
				"relativeHumidityPct":        round(clamp(100-5*(plant.inputs.AmbientDryBulbC-plant.inputs.AmbientWetBulbC), 5, 100), 3),
			},
		},
	}
}

func pumpTelemetry(state pumpState) DeviceTelemetry {
	return DeviceTelemetry{
		"runState":    runState(state.running, state.faultCode),
		"frequencyHz": round(state.frequencyHz, 3),
		"speedPct":    round(100*state.frequencyHz/50, 3),
		"flowRateM3h": round(state.flowM3H, 3),
		"powerKw":     round(state.powerKW, 3),
		"faultCode":   state.faultCode,
	}
}

func runState(running bool, faultCode string) string {
	if faultCode != "" {
		return "FAULT"
	}
	if running {
		return "RUNNING"
	}
	return "STOPPED"
}

func approach(current, target float64, elapsed, timeConstant time.Duration) float64 {
	if timeConstant <= 0 {
		return target
	}
	alpha := 1 - math.Exp(-elapsed.Seconds()/timeConstant.Seconds())
	return current + alpha*(target-current)
}

func clamp(value, lower, upper float64) float64 {
	return math.Max(lower, math.Min(upper, value))
}

func round(value float64, places int) float64 {
	power := math.Pow10(places)
	return math.Round(value*power) / power
}
