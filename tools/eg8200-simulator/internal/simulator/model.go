package simulator

import (
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/measurement"
)

const (
	ControlCabinetModeRemote = "REMOTE"
	ControlCabinetModeLocal  = "LOCAL"
)

const (
	waterHeatCapacityKWPerM3HDeltaC = measurement.WaterFactorKW
	pumpNominalFrequencyHz          = 50.0
	pumpRampHzPerSecond             = 5.0 // a VFD ACC/DEC ramp of 10 s from 0 to 50 Hz
	coolingTowerFanTimeConstant     = 30 * time.Second
	coolingTowerWaterTimeConstant   = 2 * time.Minute
	chillerCapacityTimeConstant     = 90 * time.Second
	// The building zone the plant cools. Its coil removes as much as the zone's warmth above the
	// supply water allows, sized to the chiller's rating at design (24 °C zone, 7 °C water); its
	// thermal mass warms the zone 4 °C an hour against a full-rated imbalance.
	zoneSetpointC            = 24.0
	zoneDesignSupplyWaterC   = 7.0
	zoneThermalMassHours     = 0.25
	zoneControlTimeConstant  = 3 * time.Minute
	zoneDeadbandC            = 0.3
	maximumIntegrationStep   = time.Minute
	chilledWaterTimeConstant = 3 * time.Minute
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
	chilledWaterLag time.Duration

	chiller          chillerState
	chilledWaterPump pumpState
	coolingWaterPump pumpState
	coolingTower     coolingTowerState

	chwValveOpen         bool
	cwValveOpen          bool
	chwFlowSwitchBlocked bool
	cwFlowSwitchBlocked  bool

	zone zoneState

	totalEnergyKWh        float64
	totalCoolingEnergyKWh float64
}

type zoneState struct {
	temperatureC float64
	// What the air-side coil draws from the chilled water to hold the zone at its setpoint.
	coolingDemandKW float64
}

type chillerState struct {
	runRequested           bool
	running                bool
	setpointC              float64
	loadLimitPct           float64
	leavingChilledWaterC   float64
	enteringChilledWaterC  float64
	leavingCoolingWaterC   float64
	enteringCoolingWaterC  float64
	loadPct                float64
	coolingCapacityKW      float64
	powerKW                float64
	energyKWh              float64
	cop                    float64
	faultCode              string
	localRemoteMode        string
	registerReadOnlyLocked bool
	lockedFeedbackValue    float64
}

type pumpState struct {
	runRequested           bool
	running                bool
	frequencySetpointHz    float64
	frequencyHz            float64
	flowM3H                float64
	powerKW                float64
	energyKWh              float64
	faultCode              string
	stuckHigh              bool
	localRemoteMode        string
	registerReadOnlyLocked bool
	lockedFeedbackValue    float64
}

type coolingTowerState struct {
	runRequested           bool
	running                bool
	fanSpeedSetpointPct    float64
	fanSpeedPct            float64
	enteringWaterC         float64
	leavingWaterC          float64
	powerKW                float64
	energyKWh              float64
	faultCode              string
	localRemoteMode        string
	registerReadOnlyLocked bool
	lockedFeedbackValue    float64
}

func NewPlant(config PlantConfig, scenario Scenario, now time.Time) *Plant {
	inputs := scenario.InputsFor(0, now)
	initialTowerLeavingC := inputs.AmbientWetBulbC + 4
	initialTowerEnteringC := initialTowerLeavingC + 4
	chwLag := chilledWaterTimeConstant
	if config.ThermalLagSeconds > 0 {
		chwLag = time.Duration(config.ThermalLagSeconds) * time.Second
	}
	return &Plant{
		config:          config,
		scenario:        scenario,
		inputs:          inputs,
		now:             now.UTC(),
		chilledWaterLag: chwLag,
		chwValveOpen:    true,
		cwValveOpen:     true,
		totalEnergyKWh:  config.InitialEnergyKWh,
		zone:            zoneState{temperatureC: zoneSetpointC},
		chiller: chillerState{
			runRequested:          config.Chiller.InitiallyRunning,
			running:               config.Chiller.InitiallyRunning,
			setpointC:             config.Chiller.InitialSetpointC,
			loadLimitPct:          config.Chiller.InitialLoadLimitPct,
			leavingChilledWaterC:  config.Chiller.InitialSetpointC + 1,
			enteringChilledWaterC: config.Chiller.InitialSetpointC + 6,
			leavingCoolingWaterC:  initialTowerEnteringC,
			enteringCoolingWaterC: initialTowerLeavingC,
			localRemoteMode:       ControlCabinetModeRemote,
		},
		chilledWaterPump: pumpState{
			runRequested:        config.ChilledWaterPump.InitiallyRunning,
			running:             config.ChilledWaterPump.InitiallyRunning,
			frequencySetpointHz: config.ChilledWaterPump.InitialFrequencyHz,
			frequencyHz:         config.ChilledWaterPump.InitialFrequencyHz,
			flowM3H:             initialPumpFlowM3H(config.ChilledWaterPump),
			localRemoteMode:     ControlCabinetModeRemote,
		},
		coolingWaterPump: pumpState{
			runRequested:        config.CoolingWaterPump.InitiallyRunning,
			running:             config.CoolingWaterPump.InitiallyRunning,
			frequencySetpointHz: config.CoolingWaterPump.InitialFrequencyHz,
			frequencyHz:         config.CoolingWaterPump.InitialFrequencyHz,
			flowM3H:             initialPumpFlowM3H(config.CoolingWaterPump),
			localRemoteMode:     ControlCabinetModeRemote,
		},
		coolingTower: coolingTowerState{
			runRequested:        config.CoolingTower.InitiallyRunning,
			running:             config.CoolingTower.InitiallyRunning,
			fanSpeedSetpointPct: config.CoolingTower.InitialFanSpeedPct,
			fanSpeedPct:         config.CoolingTower.InitialFanSpeedPct,
			enteringWaterC:      initialTowerEnteringC,
			leavingWaterC:       initialTowerLeavingC,
			localRemoteMode:     ControlCabinetModeRemote,
		},
	}
}

func (plant *Plant) Tick(elapsed time.Duration) Snapshot {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	plant.tickLocked(elapsed)
	return plant.snapshotLocked()
}

func initialPumpFlowM3H(config PumpConfig) float64 {
	if !config.InitiallyRunning || config.InitialFrequencyHz <= 0.5 {
		return 0
	}
	return config.RatedFlowM3H * clamp(config.InitialFrequencyHz/pumpNominalFrequencyHz, 0, 1)
}

// TickTo advances the plant to the wall-clock instant now. A clock corrected backwards
// advances nothing and takes the plant's time back with it, so readings are never stamped
// ahead of the clock.
func (plant *Plant) TickTo(now time.Time) Snapshot {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	now = now.UTC()
	plant.tickLocked(now.Sub(plant.now))
	plant.now = now
	return plant.snapshotLocked()
}

func (plant *Plant) tickLocked(elapsed time.Duration) {
	if elapsed <= 0 {
		return
	}

	remaining := elapsed
	for remaining > 0 {
		plant.inputs = plant.scenario.InputsFor(plant.scenarioElapsed, plant.now)
		// Coupled states (zone, coil, chiller) advance together, so a long tick is taken in short steps.
		segment := min(remaining, maximumIntegrationStep)
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
	plant.inputs = plant.scenario.InputsFor(plant.scenarioElapsed, plant.now)
}

func (plant *Plant) advanceLocked(elapsed time.Duration) {
	plant.now = plant.now.Add(elapsed)
	plant.updatePump(&plant.chilledWaterPump, plant.config.ChilledWaterPump, elapsed)
	plant.updatePump(&plant.coolingWaterPump, plant.config.CoolingWaterPump, elapsed)
	plant.updateCoolingTower(elapsed)
	plant.updateZoneDemand()
	plant.updateChiller(elapsed)
	plant.updateZoneTemperature(elapsed)

	// Each piece of equipment has its own sub-meter; the total meter sees all of them.
	plant.chiller.energyKWh += plant.chiller.powerKW * elapsed.Hours()
	plant.chilledWaterPump.energyKWh += plant.chilledWaterPump.powerKW * elapsed.Hours()
	plant.coolingWaterPump.energyKWh += plant.coolingWaterPump.powerKW * elapsed.Hours()
	plant.coolingTower.energyKWh += plant.coolingTower.powerKW * elapsed.Hours()
	totalPowerKW := plant.chiller.powerKW + plant.chilledWaterPump.powerKW + plant.coolingWaterPump.powerKW + plant.coolingTower.powerKW
	plant.totalEnergyKWh += totalPowerKW * elapsed.Hours()
	plant.totalCoolingEnergyKWh += plant.chiller.coolingCapacityKW * elapsed.Hours()
}

func (plant *Plant) Snapshot() Snapshot {
	plant.mu.RLock()
	defer plant.mu.RUnlock()
	return plant.snapshotLocked()
}

func (plant *Plant) SetCHWPStuckHighDisturbance(active bool) {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	plant.chilledWaterPump.stuckHigh = active
}

// SetThermalLag configures the chilled-water loop first-order response time constant.
// Passing 0 resets to the default 3-minute time constant; non-zero values must be in [1m, 30m].
func (plant *Plant) SetThermalLag(lag time.Duration) error {
	if lag == 0 {
		plant.mu.Lock()
		plant.chilledWaterLag = chilledWaterTimeConstant
		plant.mu.Unlock()
		return nil
	}
	if lag < time.Minute || lag > 30*time.Minute {
		return errors.New("thermal lag must be 0 or between 60s and 1800s")
	}
	plant.mu.Lock()
	plant.chilledWaterLag = lag
	plant.mu.Unlock()
	return nil
}

// SetControlCabinetMode switches a controllable device between LOCAL manual and REMOTE auto mode.
func (plant *Plant) SetControlCabinetMode(deviceID, mode string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(mode))
	if normalized != ControlCabinetModeRemote && normalized != ControlCabinetModeLocal {
		return false
	}
	plant.mu.Lock()
	defer plant.mu.Unlock()
	switch deviceID {
	case plant.config.Chiller.ID:
		plant.chiller.localRemoteMode = normalized
	case plant.config.ChilledWaterPump.ID:
		plant.chilledWaterPump.localRemoteMode = normalized
	case plant.config.CoolingWaterPump.ID:
		plant.coolingWaterPump.localRemoteMode = normalized
	case plant.config.CoolingTower.ID:
		plant.coolingTower.localRemoteMode = normalized
	default:
		return false
	}
	return true
}

// SetLocalManualTarget applies a local cabinet setpoint dial change directly in plant memory.
func (plant *Plant) SetLocalManualTarget(deviceID string, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	plant.mu.Lock()
	defer plant.mu.Unlock()
	switch deviceID {
	case plant.config.Chiller.ID:
		if value < 5 || value > 12 {
			return false
		}
		plant.chiller.setpointC = value
		if plant.chiller.registerReadOnlyLocked {
			plant.chiller.lockedFeedbackValue = value
		}
	case plant.config.ChilledWaterPump.ID:
		if value < 20 || value > 50 {
			return false
		}
		plant.chilledWaterPump.frequencySetpointHz = value
		if plant.chilledWaterPump.registerReadOnlyLocked {
			plant.chilledWaterPump.lockedFeedbackValue = value
		}
	case plant.config.CoolingWaterPump.ID:
		if value < 20 || value > 50 {
			return false
		}
		plant.coolingWaterPump.frequencySetpointHz = value
		if plant.coolingWaterPump.registerReadOnlyLocked {
			plant.coolingWaterPump.lockedFeedbackValue = value
		}
	case plant.config.CoolingTower.ID:
		if value < 20 || value > 100 {
			return false
		}
		plant.coolingTower.fanSpeedSetpointPct = value
		if plant.coolingTower.registerReadOnlyLocked {
			plant.coolingTower.lockedFeedbackValue = value
		}
	default:
		return false
	}
	return true
}

// SetRegisterReadOnlyLock freezes physical actuator feedback so remote writes ACK cleanly
// while telemetry feedback refuses to converge until unlocked.
func (plant *Plant) SetRegisterReadOnlyLock(deviceID string, active bool) bool {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	switch deviceID {
	case plant.config.Chiller.ID:
		if active && !plant.chiller.registerReadOnlyLocked {
			plant.chiller.lockedFeedbackValue = plant.chiller.setpointC
		}
		plant.chiller.registerReadOnlyLocked = active
	case plant.config.ChilledWaterPump.ID:
		if active && !plant.chilledWaterPump.registerReadOnlyLocked {
			plant.chilledWaterPump.lockedFeedbackValue = plant.chilledWaterPump.frequencyHz
		}
		plant.chilledWaterPump.registerReadOnlyLocked = active
	case plant.config.CoolingWaterPump.ID:
		if active && !plant.coolingWaterPump.registerReadOnlyLocked {
			plant.coolingWaterPump.lockedFeedbackValue = plant.coolingWaterPump.frequencyHz
		}
		plant.coolingWaterPump.registerReadOnlyLocked = active
	case plant.config.CoolingTower.ID:
		if active && !plant.coolingTower.registerReadOnlyLocked {
			plant.coolingTower.lockedFeedbackValue = plant.coolingTower.fanSpeedPct
		}
		plant.coolingTower.registerReadOnlyLocked = active
	default:
		return false
	}
	return true
}

// SetHydraulicInterlockFault injects butterfly-valve closure or flow-switch faults.
func (plant *Plant) SetHydraulicInterlockFault(chwValveOpen, cwValveOpen, chwFlowSwitchBlocked, cwFlowSwitchBlocked bool) {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	plant.chwValveOpen = chwValveOpen
	plant.cwValveOpen = cwValveOpen
	plant.chwFlowSwitchBlocked = chwFlowSwitchBlocked
	plant.cwFlowSwitchBlocked = cwFlowSwitchBlocked
}

func (plant *Plant) chwFlowSwitchProvenLocked() bool {
	flowFrac := clamp(plant.chilledWaterPump.flowM3H/plant.config.ChilledWaterPump.RatedFlowM3H, 0, 1)
	return plant.chwValveOpen && !plant.chwFlowSwitchBlocked && flowFrac >= 0.35
}

func (plant *Plant) cwFlowSwitchProvenLocked() bool {
	flowFrac := clamp(plant.coolingWaterPump.flowM3H/plant.config.CoolingWaterPump.RatedFlowM3H, 0, 1)
	return plant.cwValveOpen && !plant.cwFlowSwitchBlocked && flowFrac >= 0.35
}

func (plant *Plant) effectiveChillerSetpointLocked() float64 {
	if plant.chiller.registerReadOnlyLocked {
		return plant.chiller.lockedFeedbackValue
	}
	return plant.chiller.setpointC
}

func normalizedCabinetMode(mode string) string {
	if mode == ControlCabinetModeLocal {
		return ControlCabinetModeLocal
	}
	return ControlCabinetModeRemote
}

func (plant *Plant) updatePump(state *pumpState, config PumpConfig, elapsed time.Duration) {
	if state.faultCode != "" {
		state.running = false
		state.frequencyHz = 0
		state.flowM3H = 0
		state.powerKW = 0
		return
	}

	targetFrequencyHz := 0.0
	if state.runRequested {
		targetFrequencyHz = state.frequencySetpointHz
		if state.registerReadOnlyLocked {
			targetFrequencyHz = state.lockedFeedbackValue
		}
		if state.stuckHigh {
			targetFrequencyHz = pumpNominalFrequencyHz
		}
	}
	state.frequencyHz = ramp(state.frequencyHz, targetFrequencyHz, pumpRampHzPerSecond*elapsed.Seconds())
	state.running = state.frequencyHz > 0.5

	speedFraction := clamp(state.frequencyHz/pumpNominalFrequencyHz, 0, 1)
	state.flowM3H = config.RatedFlowM3H * speedFraction
	staticHead := 0.0
	if state == &plant.chilledWaterPump {
		staticHead = plant.config.Mismatch.CHWPStaticHeadFraction
	} else if state == &plant.coolingWaterPump {
		staticHead = plant.config.Mismatch.CWPStaticHeadFraction
	}
	state.powerKW = pumpPowerKW(config.RatedPowerKW, speedFraction, staticHead, state.running)
}

func (plant *Plant) updateCoolingTower(elapsed time.Duration) {
	state := &plant.coolingTower
	if state.faultCode != "" {
		state.running = false
		state.fanSpeedPct = 0
		state.powerKW = 0
		state.enteringWaterC = approach(state.enteringWaterC, state.leavingWaterC, elapsed, coolingTowerWaterTimeConstant)
		return
	}

	targetFanSpeedPct := 0.0
	if state.runRequested {
		targetFanSpeedPct = state.fanSpeedSetpointPct
		if state.registerReadOnlyLocked {
			targetFanSpeedPct = state.lockedFeedbackValue
		}
	}
	state.fanSpeedPct = approach(state.fanSpeedPct, targetFanSpeedPct, elapsed, coolingTowerFanTimeConstant)
	state.running = state.fanSpeedPct > 0.5

	speedFraction := clamp(state.fanSpeedPct/100, 0, 1)
	state.powerKW = plant.config.CoolingTower.RatedFanPowerKW * speedFraction * speedFraction * speedFraction
	heatRejectionKW := plant.chiller.coolingCapacityKW + plant.chiller.powerKW
	designHeatRejectionKW := plant.config.Chiller.RatedCoolingCapacityKW * (1 + 1/math.Max(plant.config.Chiller.BaseCOP, 1))
	heatRejectionRatio := 0.0
	if designHeatRejectionKW > 0 {
		heatRejectionRatio = heatRejectionKW / designHeatRejectionKW
	}
	approachC := coolingTowerApproachC(plant.config.Mismatch, speedFraction, heatRejectionRatio)
	leavingTargetC := plant.inputs.AmbientWetBulbC + approachC
	enteringTargetC := leavingTargetC
	if plant.coolingWaterPump.flowM3H > 0 {
		enteringTargetC += heatRejectionKW / (waterHeatCapacityKWPerM3HDeltaC * plant.coolingWaterPump.flowM3H)
	}
	state.leavingWaterC = approach(state.leavingWaterC, leavingTargetC, elapsed, coolingTowerWaterTimeConstant)
	state.enteringWaterC = approach(state.enteringWaterC, enteringTargetC, elapsed, coolingTowerWaterTimeConstant)
}

func (plant *Plant) updateChiller(elapsed time.Duration) {
	state := &plant.chiller
	chilledWaterFlowFraction := clamp(plant.chilledWaterPump.flowM3H/plant.config.ChilledWaterPump.RatedFlowM3H, 0, 1)
	coolingWaterFlowFraction := clamp(plant.coolingWaterPump.flowM3H/plant.config.CoolingWaterPump.RatedFlowM3H, 0, 1)
	flowReady := math.Min(chilledWaterFlowFraction, coolingWaterFlowFraction) >= 0.35 &&
		plant.chwValveOpen && plant.cwValveOpen && !plant.chwFlowSwitchBlocked && !plant.cwFlowSwitchBlocked
	equipmentFault := state.faultCode != "" || plant.chilledWaterPump.faultCode != "" || plant.coolingWaterPump.faultCode != "" || plant.coolingTower.faultCode != ""
	available := state.runRequested && !equipmentFault && flowReady && plant.coolingTower.running

	targetCapacityKW := 0.0
	if available {
		limitCapacityKW := plant.config.Chiller.RatedCoolingCapacityKW * state.loadLimitPct / 100
		targetCapacityKW = math.Min(plant.zone.coolingDemandKW, limitCapacityKW)
	}
	if equipmentFault {
		state.coolingCapacityKW = 0
	} else {
		state.coolingCapacityKW = approach(state.coolingCapacityKW, targetCapacityKW, elapsed, chillerCapacityTimeConstant)
	}
	state.running = available || state.coolingCapacityKW > 1
	state.loadPct = 100 * state.coolingCapacityKW / plant.config.Chiller.RatedCoolingCapacityKW

	state.enteringCoolingWaterC = plant.coolingTower.leavingWaterC
	state.leavingCoolingWaterC = plant.coolingTower.enteringWaterC
	plant.updateChilledWaterTemperatures(elapsed)

	if state.coolingCapacityKW <= 0 {
		state.loadPct = 0
		state.powerKW = 0
		state.cop = 0
		return
	}

	state.cop = chillerEffectiveCOP(
		plant.config.Chiller.BaseCOP,
		plant.effectiveChillerSetpointLocked(),
		state.enteringCoolingWaterC,
		state.leavingCoolingWaterC,
		state.loadPct,
		plant.config.Mismatch,
	)
	state.powerKW = state.coolingCapacityKW / state.cop
}

// zoneThermalMassKWhPerC is how much heat moves the zone by one degree.
func (plant *Plant) zoneThermalMassKWhPerC() float64 {
	return plant.config.Chiller.RatedCoolingCapacityKW * zoneThermalMassHours
}

func (plant *Plant) effectiveCoolingLoadKW() float64 {
	return mismatchCoolingLoadKW(plant.inputs.CoolingLoadKW, plant.config.Mismatch)
}

// updateZoneDemand sets what the coil asks of the chilled water: the building's heat gain plus a
// recovery toward the setpoint, no more than the coil can remove at the current supply water.
func (plant *Plant) updateZoneDemand() {
	coilKWPerC := plant.config.Chiller.RatedCoolingCapacityKW / (zoneSetpointC - zoneDesignSupplyWaterC)
	coilLimitKW := coilCapacityLimitKW(
		plant.config.Mismatch,
		coilKWPerC,
		plant.zone.temperatureC,
		plant.chiller.leavingChilledWaterC,
		plant.chilledWaterPump.flowM3H,
		plant.config.ChilledWaterPump.RatedFlowM3H,
	)
	// Like a real zone control, it ignores drift inside its deadband and recovers only beyond it.
	correctionKW := math.Max(0, plant.zone.temperatureC-zoneSetpointC-zoneDeadbandC) * plant.zoneThermalMassKWhPerC() / zoneControlTimeConstant.Hours()
	plant.zone.coolingDemandKW = clamp(plant.effectiveCoolingLoadKW()+correctionKW, 0, coilLimitKW)
}

// updateZoneTemperature warms the zone by the heat the chilled water did not remove.
func (plant *Plant) updateZoneTemperature(elapsed time.Duration) {
	imbalanceKW := plant.effectiveCoolingLoadKW() - plant.chiller.coolingCapacityKW
	plant.zone.temperatureC += imbalanceKW * elapsed.Hours() / plant.zoneThermalMassKWhPerC()
}

func (plant *Plant) updateChilledWaterTemperatures(elapsed time.Duration) {
	state := &plant.chiller
	flowM3H := plant.chilledWaterPump.flowM3H
	if flowM3H <= 0 {
		state.leavingChilledWaterC = approach(state.leavingChilledWaterC, state.enteringChilledWaterC, elapsed, 12*time.Minute)
		return
	}

	waterCapacityKWPerC := waterHeatCapacityKWPerM3HDeltaC * flowM3H
	unmetLoadKW := math.Max(plant.zone.coolingDemandKW-state.coolingCapacityKW, 0)
	supplyTargetC := plant.effectiveChillerSetpointLocked() + unmetLoadKW/waterCapacityKWPerC
	lag := plant.chilledWaterLag
	if lag <= 0 {
		lag = chilledWaterTimeConstant
	}
	state.leavingChilledWaterC = approach(state.leavingChilledWaterC, supplyTargetC, elapsed, lag)
	state.enteringChilledWaterC = state.leavingChilledWaterC + state.coolingCapacityKW/waterCapacityKWPerC
}

// PlantTruthState exposes uncorrupted physical plant states without telemetry sensor bias.
type PlantTruthState struct {
	ObservedAt            time.Time
	TrueZoneTempC         float64
	TrueCHWSupplyC        float64
	TrueCHWReturnC        float64
	TrueCoolingLoadKW     float64
	TrueCoolingCapacityKW float64
	TotalEnergyKWh        float64
	ChillerEnergyKWh      float64
	CHWPEnergyKWh         float64
	CWPEnergyKWh          float64
	TowerEnergyKWh        float64
	ChillerCOP            float64
	ChillerRunRequested   bool
	ChillerRunning        bool
	CHWPFlowM3H           float64
	CWPFlowM3H            float64
	TowerApproachC        float64
}

func (plant *Plant) TruthState() PlantTruthState {
	plant.mu.RLock()
	defer plant.mu.RUnlock()
	return PlantTruthState{
		ObservedAt:            plant.now,
		TrueZoneTempC:         plant.zone.temperatureC,
		TrueCHWSupplyC:        plant.chiller.leavingChilledWaterC,
		TrueCHWReturnC:        plant.chiller.enteringChilledWaterC,
		TrueCoolingLoadKW:     plant.effectiveCoolingLoadKW(),
		TrueCoolingCapacityKW: plant.chiller.coolingCapacityKW,
		TotalEnergyKWh:        plant.totalEnergyKWh,
		ChillerEnergyKWh:      plant.chiller.energyKWh,
		CHWPEnergyKWh:         plant.chilledWaterPump.energyKWh,
		CWPEnergyKWh:          plant.coolingWaterPump.energyKWh,
		TowerEnergyKWh:        plant.coolingTower.energyKWh,
		ChillerCOP:            plant.chiller.cop,
		ChillerRunRequested:   plant.chiller.runRequested,
		ChillerRunning:        plant.chiller.running,
		CHWPFlowM3H:           plant.chilledWaterPump.flowM3H,
		CWPFlowM3H:            plant.coolingWaterPump.flowM3H,
		TowerApproachC:        plant.coolingTower.leavingWaterC - plant.inputs.AmbientWetBulbC,
	}
}

func (plant *Plant) snapshotLocked() Snapshot {
	totalPowerKW := plant.chiller.powerKW + plant.chilledWaterPump.powerKW + plant.coolingWaterPump.powerKW + plant.coolingTower.powerKW
	observedCHWSupplyC := plant.chiller.leavingChilledWaterC + plant.config.Mismatch.CHWSupplySensorBiasC
	observedZoneTempC := plant.zone.temperatureC + plant.config.Mismatch.ZoneTempSensorBiasC
	return Snapshot{
		ObservedAt: plant.now,
		Devices: map[string]DeviceTelemetry{
			plant.config.Chiller.ID: {
				"runState":                         runState(plant.chiller.running, plant.chiller.faultCode),
				"leavingChilledWaterTemperatureC":  round(observedCHWSupplyC, 3),
				"enteringChilledWaterTemperatureC": round(plant.chiller.enteringChilledWaterC, 3),
				"chilledWaterTemperatureSetpointC": round(plant.effectiveChillerSetpointLocked(), 3),
				"leavingCoolingWaterTemperatureC":  round(plant.chiller.leavingCoolingWaterC, 3),
				"enteringCoolingWaterTemperatureC": round(plant.chiller.enteringCoolingWaterC, 3),
				"compressorLoadPct":                round(plant.chiller.loadPct, 3),
				"coolingCapacityKw":                round(plant.chiller.coolingCapacityKW, 3),
				"powerKw":                          round(plant.chiller.powerKW, 3),
				"energyKwh":                        round(plant.chiller.energyKWh, 6),
				"cop":                              round(plant.chiller.cop, 3),
				"loadLimitPct":                     round(plant.chiller.loadLimitPct, 3),
				"faultCode":                        plant.chiller.faultCode,
				"localRemoteMode":                  normalizedCabinetMode(plant.chiller.localRemoteMode),
				"chwValveOpen":                     plant.chwValveOpen,
				"cwValveOpen":                      plant.cwValveOpen,
				"chwFlowSwitch":                    plant.chwFlowSwitchProvenLocked(),
				"cwFlowSwitch":                     plant.cwFlowSwitchProvenLocked(),
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
				"energyKwh":                  round(plant.coolingTower.energyKWh, 6),
				"faultCode":                  plant.coolingTower.faultCode,
				"localRemoteMode":            normalizedCabinetMode(plant.coolingTower.localRemoteMode),
			},
			plant.config.PowerMeterID: {
				"activePowerKw": round(totalPowerKW, 3),
				"energyKwh":     round(plant.totalEnergyKWh, 6),
				"powerFactor":   0.93,
				"frequencyHz":   50.0,
			},
			plant.config.BTUMeterID: {
				"supplyWaterTemperatureC":     round(observedCHWSupplyC, 3),
				"returnWaterTemperatureC":     round(plant.chiller.enteringChilledWaterC, 3),
				"temperatureDifferenceC":      round(plant.chiller.enteringChilledWaterC-observedCHWSupplyC, 3),
				"flowRateM3h":                 round(plant.chilledWaterPump.flowM3H, 3),
				"instantCoolingCapacityKw":    round(plant.chiller.coolingCapacityKW, 3),
				"accumulatedCoolingEnergyKwh": round(plant.totalCoolingEnergyKWh, 6),
			},
			plant.config.ZoneSensorID: {
				"zoneTemperatureC": round(observedZoneTempC, 3),
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
		"runState":        runState(state.running, state.faultCode),
		"frequencyHz":     round(state.frequencyHz, 3),
		"speedPct":        round(100*state.frequencyHz/pumpNominalFrequencyHz, 3),
		"flowRateM3h":     round(state.flowM3H, 3),
		"powerKw":         round(state.powerKW, 3),
		"energyKwh":       round(state.energyKWh, 6),
		"faultCode":       state.faultCode,
		"localRemoteMode": normalizedCabinetMode(state.localRemoteMode),
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

func ramp(current, target, maximumStep float64) float64 {
	return current + clamp(target-current, -maximumStep, maximumStep)
}

func pumpPowerKW(ratedPowerKW, speedFraction, staticHeadFraction float64, running bool) float64 {
	if !running || speedFraction <= 0 {
		return 0
	}
	c0 := clamp(staticHeadFraction, 0, 0.5)
	return ratedPowerKW * (c0 + (1-c0)*speedFraction*speedFraction*speedFraction)
}

func coolingTowerApproachC(mismatch PlantMismatchConfig, speedFraction, heatRejectionRatio float64) float64 {
	if !mismatch.NonlinearTowerApproach {
		return 2.5 + (1-speedFraction)*6
	}
	minApproach := mismatch.TowerMinApproachC
	if minApproach <= 0 {
		minApproach = 1.5
	}
	loadCoeff := mismatch.TowerLoadApproachCoeff
	if loadCoeff <= 0 {
		loadCoeff = 1.9
	}
	exp := mismatch.TowerNonlinearExp
	if exp <= 0 {
		exp = 1.25
	}
	lowFanPenalty := mismatch.TowerLowFanPenaltyC
	if lowFanPenalty <= 0 {
		lowFanPenalty = 1.2
	}
	rQ := clamp(heatRejectionRatio, 0, 2.5)
	rAir := math.Max(speedFraction, 0.12)
	return minApproach + loadCoeff*math.Pow(rQ/rAir, exp) + lowFanPenalty*(1-speedFraction)*(1-speedFraction)
}

func chillerEffectiveCOP(baseCOP, setpointC, enteringCW, leavingCW, loadPct float64, mismatch PlantMismatchConfig) float64 {
	lowLoadPenalty := 0.0
	if loadPct < 35 {
		lowLoadPenalty = (35 - loadPct) * 0.025
	}
	highLoadPenalty := mismatch.HighLoadCOPPenaltyCoeff * math.Max(0, loadPct-80)
	cwSlope := mismatch.CondenserCOPSlopePerC
	if cwSlope <= 0 {
		cwSlope = 0.045
	}
	cwRangeExcess := math.Max(0, (leavingCW-enteringCW)-4.7)
	effectiveCW := enteringCW + mismatch.CondenserMeanTempWeight*cwRangeExcess
	rawCOP := baseCOP + 0.14*(setpointC-6.5) - cwSlope*(effectiveCW-28) - lowLoadPenalty - highLoadPenalty

	mult := mismatch.ChillerCOPMultiplier
	if mult <= 0 {
		mult = 1.0
	}
	adjusted := rawCOP*mult + mismatch.ChillerCOPOffset
	if mult == 1.0 && mismatch.ChillerCOPOffset == 0 && mismatch.CondenserCOPSlopePerC == 0 && mismatch.CondenserMeanTempWeight == 0 && mismatch.HighLoadCOPPenaltyCoeff == 0 {
		return clamp(adjusted, 2.5, 7.5)
	}
	return clamp(adjusted, 1.8, 8.5)
}

func mismatchCoolingLoadKW(baseLoadKW float64, mismatch PlantMismatchConfig) float64 {
	mult := mismatch.ThermalLoadMultiplier
	if mult <= 0 {
		mult = 1.0
	}
	return math.Max(0, baseLoadKW*mult+mismatch.ThermalLoadBiasKW)
}

func coilCapacityLimitKW(mismatch PlantMismatchConfig, coilKWPerC, zoneTempC, leavingCHWC, chwFlowM3H, ratedCHWFlowM3H float64) float64 {
	airDeltaC := math.Max(0, zoneTempC-leavingCHWC)
	if !mismatch.CHWCoilFlowCoupling || ratedCHWFlowM3H <= 0 {
		return coilKWPerC * airDeltaC
	}
	flowFrac := clamp(chwFlowM3H/ratedCHWFlowM3H, 0, 1.2)
	if flowFrac <= 0 {
		return 0
	}
	effectiveness := flowFrac / (0.45 + 0.55*flowFrac)
	airLimit := effectiveness * coilKWPerC * airDeltaC
	approachMin := mismatch.CHWCoilApproachMinC
	if approachMin <= 0 {
		approachMin = 4.5
	}
	waterLimit := waterHeatCapacityKWPerM3HDeltaC * chwFlowM3H * math.Max(0, zoneTempC-approachMin-leavingCHWC)
	return math.Min(airLimit, waterLimit)
}
