package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// CoupledChillerPlantConfig defines a coupled multi-chiller central plant for
// closed-loop mismatch benchmarking.
type CoupledChillerPlantConfig struct {
	Chillers          []ChillerConfig     `json:"chillers"`
	ChilledWaterPump  PumpConfig          `json:"chilledWaterPump"`
	CoolingWaterPump  PumpConfig          `json:"coolingWaterPump"`
	CoolingTower      CoolingTowerConfig  `json:"coolingTower"`
	PowerMeterID      string              `json:"powerMeterId"`
	BTUMeterID        string              `json:"btuMeterId"`
	WeatherStationID  string              `json:"weatherStationId"`
	ZoneSensorID      string              `json:"zoneSensorId"`
	ThermalLagSeconds int                 `json:"thermalLagSeconds,omitempty"`
	ZoneTempMinC      float64             `json:"zoneTempMinC,omitempty"`
	ZoneTempMaxC      float64             `json:"zoneTempMaxC,omitempty"`
	Mismatch          PlantMismatchConfig `json:"mismatch,omitempty"`
}

func (c CoupledChillerPlantConfig) Validate() error {
	if len(c.Chillers) == 0 {
		return errors.New("at least one chiller is required")
	}
	if err := c.Mismatch.Validate(); err != nil {
		return err
	}
	if c.ThermalLagSeconds != 0 && (c.ThermalLagSeconds < 60 || c.ThermalLagSeconds > 1800) {
		return errors.New("thermalLagSeconds must be 0 or between 60 and 1800")
	}
	seen := map[string]struct{}{}
	for i, ch := range c.Chillers {
		if strings.TrimSpace(ch.ID) == "" || ch.RatedCoolingCapacityKW <= 0 || ch.BaseCOP < 2 || ch.BaseCOP > 9 ||
			ch.InitialSetpointC < 5 || ch.InitialSetpointC > 12 || ch.InitialLoadLimitPct < 20 || ch.InitialLoadLimitPct > 100 {
			return fmt.Errorf("chiller[%d] config is invalid", i)
		}
		if _, dup := seen[ch.ID]; dup {
			return fmt.Errorf("duplicate chiller id %s", ch.ID)
		}
		seen[ch.ID] = struct{}{}
	}
	for name, pump := range map[string]PumpConfig{
		"chilledWaterPump": c.ChilledWaterPump,
		"coolingWaterPump": c.CoolingWaterPump,
	} {
		if strings.TrimSpace(pump.ID) == "" || pump.RatedPowerKW <= 0 || pump.RatedFlowM3H <= 0 ||
			pump.InitialFrequencyHz < 20 || pump.InitialFrequencyHz > 50 {
			return fmt.Errorf("%s config is invalid", name)
		}
		if _, dup := seen[pump.ID]; dup {
			return fmt.Errorf("duplicate device id %s", pump.ID)
		}
		seen[pump.ID] = struct{}{}
	}
	if strings.TrimSpace(c.CoolingTower.ID) == "" || c.CoolingTower.RatedFanPowerKW <= 0 ||
		c.CoolingTower.InitialFanSpeedPct < 20 || c.CoolingTower.InitialFanSpeedPct > 100 {
		return errors.New("cooling tower config is invalid")
	}
	for _, id := range []string{c.CoolingTower.ID, c.PowerMeterID, c.BTUMeterID, c.WeatherStationID, c.ZoneSensorID} {
		if strings.TrimSpace(id) == "" {
			return errors.New("plant device IDs must be non-empty")
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate device id %s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (c CoupledChillerPlantConfig) totalRatedCoolingKW() float64 {
	total := 0.0
	for _, ch := range c.Chillers {
		total += ch.RatedCoolingCapacityKW
	}
	return total
}

func (c CoupledChillerPlantConfig) designHeatRejectionKW() float64 {
	total := 0.0
	for _, ch := range c.Chillers {
		total += ch.RatedCoolingCapacityKW * (1 + 1/math.Max(ch.BaseCOP, 1))
	}
	return total
}

// CoupledPlantInputProvider returns ambient weather and building cooling demand at instant at.
type CoupledPlantInputProvider func(elapsed time.Duration, at time.Time) ScenarioInputs

// ChillerTruthState captures uncorrupted per-chiller truth states.
type ChillerTruthState struct {
	ID                string
	RunRequested      bool
	Running           bool
	SetpointC         float64
	LoadLimitPct      float64
	CoolingCapacityKW float64
	PowerKW           float64
	EnergyKWh         float64
	COP               float64
	LoadPct           float64
	LocalRemoteMode   string
}

// CoupledPlantTruthState captures uncorrupted truth meters and thermal states.
type CoupledPlantTruthState struct {
	ObservedAt            time.Time
	AmbientDryBulbC       float64
	AmbientWetBulbC       float64
	TrueZoneTempC         float64
	ObservedZoneTempC     float64
	TrueCHWSupplyC        float64
	ObservedCHWSupplyC    float64
	TrueCHWReturnC        float64
	TrueCoolingLoadKW     float64
	TotalCoolingKW        float64
	TotalPowerKW          float64
	TotalEnergyKWh        float64
	ChillerEnergyKWh      float64
	CHWPEnergyKWh         float64
	CWPEnergyKWh          float64
	TowerEnergyKWh        float64
	TotalCoolingEnergyKWh float64
	CHWPFreqSetpointHz    float64
	CHWPFreqHz            float64
	CHWPFlowM3H           float64
	CHWPLocalMode         string
	CWPFreqSetpointHz     float64
	CWPFreqHz             float64
	CWPFlowM3H            float64
	CWPLocalMode          string
	TowerFanSetpointPct   float64
	TowerFanPct           float64
	TowerApproachC        float64
	TowerLeavingWaterC    float64
	TowerEnteringWaterC   float64
	TowerLocalMode        string
	Chillers              []ChillerTruthState
	FalseTrips            int
	InterlockViolations   int
}

// CoupledChillerPlant simulates N parallel chillers coupled to headered CHWP, CWP, and cooling tower
// with configurable physical plant-model mismatch and sensor bias.
type CoupledChillerPlant struct {
	mu              sync.RWMutex
	config          CoupledChillerPlantConfig
	inputFn         CoupledPlantInputProvider
	inputs          ScenarioInputs
	now             time.Time
	scenarioElapsed time.Duration
	chilledWaterLag time.Duration

	chillers         []chillerState
	chillerIndexByID map[string]int
	chilledWaterPump pumpState
	coolingWaterPump pumpState
	coolingTower     coolingTowerState

	chwValveOpen         bool
	cwValveOpen          bool
	chwFlowSwitchBlocked bool
	cwFlowSwitchBlocked  bool

	headerLeavingCHWC  float64
	headerEnteringCHWC float64
	zone               zoneState

	totalEnergyKWh        float64
	totalCoolingEnergyKWh float64
	falseTrips            int
	interlockViolations   int
}

// NewCoupledChillerPlant creates a coupled multi-chiller truth plant.
func NewCoupledChillerPlant(config CoupledChillerPlantConfig, inputFn CoupledPlantInputProvider, start time.Time) (*CoupledChillerPlant, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if inputFn == nil {
		return nil, errors.New("input provider is required")
	}
	startUTC := start.UTC()
	inputs := inputFn(0, startUTC)
	initialTowerLeavingC := inputs.AmbientWetBulbC + 3.5
	initialTowerEnteringC := initialTowerLeavingC + 4.0
	chwLag := chilledWaterTimeConstant
	if config.ThermalLagSeconds > 0 {
		chwLag = time.Duration(config.ThermalLagSeconds) * time.Second
	}
	chillers := make([]chillerState, len(config.Chillers))
	idxByID := make(map[string]int, len(config.Chillers))
	initialSP := config.Chillers[0].InitialSetpointC
	for i, chCfg := range config.Chillers {
		idxByID[chCfg.ID] = i
		chillers[i] = chillerState{
			runRequested:          chCfg.InitiallyRunning,
			running:               chCfg.InitiallyRunning,
			setpointC:             chCfg.InitialSetpointC,
			loadLimitPct:          chCfg.InitialLoadLimitPct,
			leavingChilledWaterC:  chCfg.InitialSetpointC,
			enteringChilledWaterC: chCfg.InitialSetpointC + 4.5,
			leavingCoolingWaterC:  initialTowerEnteringC,
			enteringCoolingWaterC: initialTowerLeavingC,
			localRemoteMode:       ControlCabinetModeRemote,
		}
	}
	return &CoupledChillerPlant{
		config:             config,
		inputFn:            inputFn,
		inputs:             inputs,
		now:                startUTC,
		chilledWaterLag:    chwLag,
		chillers:           chillers,
		chillerIndexByID:   idxByID,
		chwValveOpen:       true,
		cwValveOpen:        true,
		headerLeavingCHWC:  initialSP,
		headerEnteringCHWC: initialSP + 4.5,
		zone:               zoneState{temperatureC: zoneSetpointC},
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
	}, nil
}

// Tick advances the coupled truth plant in <= 1m physical integration steps.
func (p *CoupledChillerPlant) Tick(elapsed time.Duration) Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if elapsed <= 0 {
		return p.snapshotLocked()
	}
	remaining := elapsed
	for remaining > 0 {
		p.inputs = p.inputFn(p.scenarioElapsed, p.now)
		segment := min(remaining, maximumIntegrationStep)
		p.advanceLocked(segment)
		p.scenarioElapsed += segment
		remaining -= segment
	}
	p.inputs = p.inputFn(p.scenarioElapsed, p.now)
	return p.snapshotLocked()
}

func (p *CoupledChillerPlant) advanceLocked(elapsed time.Duration) {
	p.now = p.now.Add(elapsed)
	p.updatePumpLocked(&p.chilledWaterPump, p.config.ChilledWaterPump, p.config.Mismatch.CHWPStaticHeadFraction, elapsed)
	p.updatePumpLocked(&p.coolingWaterPump, p.config.CoolingWaterPump, p.config.Mismatch.CWPStaticHeadFraction, elapsed)
	p.updateCoolingTowerLocked(elapsed)
	p.updateZoneDemandLocked()
	p.updateChillersLocked(elapsed)
	p.updateZoneTemperatureLocked(elapsed)

	hours := elapsed.Hours()
	totalPowerKW := p.chilledWaterPump.powerKW + p.coolingWaterPump.powerKW + p.coolingTower.powerKW
	totalCapKW := 0.0
	for i := range p.chillers {
		p.chillers[i].energyKWh += p.chillers[i].powerKW * hours
		totalPowerKW += p.chillers[i].powerKW
		totalCapKW += p.chillers[i].coolingCapacityKW
	}
	p.chilledWaterPump.energyKWh += p.chilledWaterPump.powerKW * hours
	p.coolingWaterPump.energyKWh += p.coolingWaterPump.powerKW * hours
	p.coolingTower.energyKWh += p.coolingTower.powerKW * hours
	p.totalEnergyKWh += totalPowerKW * hours
	p.totalCoolingEnergyKWh += totalCapKW * hours
}

func (p *CoupledChillerPlant) updatePumpLocked(state *pumpState, cfg PumpConfig, staticHead float64, elapsed time.Duration) {
	if state.faultCode != "" {
		state.running = false
		state.frequencyHz = 0
		state.flowM3H = 0
		state.powerKW = 0
		return
	}
	targetHz := 0.0
	if state.runRequested {
		targetHz = state.frequencySetpointHz
		if state.registerReadOnlyLocked {
			targetHz = state.lockedFeedbackValue
		}
		if state.stuckHigh {
			targetHz = pumpNominalFrequencyHz
		}
	}
	state.frequencyHz = ramp(state.frequencyHz, targetHz, pumpRampHzPerSecond*elapsed.Seconds())
	state.running = state.frequencyHz > 0.5
	speedFrac := clamp(state.frequencyHz/pumpNominalFrequencyHz, 0, 1)
	state.flowM3H = cfg.RatedFlowM3H * speedFrac
	state.powerKW = pumpPowerKW(cfg.RatedPowerKW, speedFrac, staticHead, state.running)
}

func (p *CoupledChillerPlant) updateCoolingTowerLocked(elapsed time.Duration) {
	state := &p.coolingTower
	if state.faultCode != "" {
		state.running = false
		state.fanSpeedPct = 0
		state.powerKW = 0
		state.enteringWaterC = approach(state.enteringWaterC, state.leavingWaterC, elapsed, coolingTowerWaterTimeConstant)
		return
	}
	targetFanPct := 0.0
	if state.runRequested {
		targetFanPct = state.fanSpeedSetpointPct
		if state.registerReadOnlyLocked {
			targetFanPct = state.lockedFeedbackValue
		}
	}
	state.fanSpeedPct = approach(state.fanSpeedPct, targetFanPct, elapsed, coolingTowerFanTimeConstant)
	state.running = state.fanSpeedPct > 0.5
	speedFrac := clamp(state.fanSpeedPct/100, 0, 1)
	state.powerKW = p.config.CoolingTower.RatedFanPowerKW * speedFrac * speedFrac * speedFrac

	heatRejectionKW := 0.0
	for i := range p.chillers {
		heatRejectionKW += p.chillers[i].coolingCapacityKW + p.chillers[i].powerKW
	}
	designHeatRejKW := p.config.designHeatRejectionKW()
	heatRejectionRatio := 0.0
	if designHeatRejKW > 0 {
		heatRejectionRatio = heatRejectionKW / designHeatRejKW
	}
	approachC := coolingTowerApproachC(p.config.Mismatch, speedFrac, heatRejectionRatio)
	leavingTargetC := p.inputs.AmbientWetBulbC + approachC
	enteringTargetC := leavingTargetC
	if p.coolingWaterPump.flowM3H > 0 {
		enteringTargetC += heatRejectionKW / (waterHeatCapacityKWPerM3HDeltaC * p.coolingWaterPump.flowM3H)
	}
	state.leavingWaterC = approach(state.leavingWaterC, leavingTargetC, elapsed, coolingTowerWaterTimeConstant)
	state.enteringWaterC = approach(state.enteringWaterC, enteringTargetC, elapsed, coolingTowerWaterTimeConstant)
}

func (p *CoupledChillerPlant) effectiveCoolingLoadKW() float64 {
	return mismatchCoolingLoadKW(p.inputs.CoolingLoadKW, p.config.Mismatch)
}

func (p *CoupledChillerPlant) zoneThermalMassKWhPerC() float64 {
	return p.config.totalRatedCoolingKW() * zoneThermalMassHours
}

func (p *CoupledChillerPlant) updateZoneDemandLocked() {
	totalRated := p.config.totalRatedCoolingKW()
	coilKWPerC := totalRated / (zoneSetpointC - zoneDesignSupplyWaterC)
	coilLimitKW := coilCapacityLimitKW(
		p.config.Mismatch,
		coilKWPerC,
		p.zone.temperatureC,
		p.headerLeavingCHWC,
		p.chilledWaterPump.flowM3H,
		p.config.ChilledWaterPump.RatedFlowM3H,
	)
	correctionKW := math.Max(0, p.zone.temperatureC-zoneSetpointC-zoneDeadbandC) * p.zoneThermalMassKWhPerC() / zoneControlTimeConstant.Hours()
	p.zone.coolingDemandKW = clamp(p.effectiveCoolingLoadKW()+correctionKW, 0, coilLimitKW)
}

func (p *CoupledChillerPlant) flowReadyLocked() bool {
	chwFrac := clamp(p.chilledWaterPump.flowM3H/p.config.ChilledWaterPump.RatedFlowM3H, 0, 1)
	cwFrac := clamp(p.coolingWaterPump.flowM3H/p.config.CoolingWaterPump.RatedFlowM3H, 0, 1)
	return math.Min(chwFrac, cwFrac) >= 0.35 &&
		p.chwValveOpen && p.cwValveOpen && !p.chwFlowSwitchBlocked && !p.cwFlowSwitchBlocked
}

func (p *CoupledChillerPlant) updateChillersLocked(elapsed time.Duration) {
	flowReady := p.flowReadyLocked()
	auxFault := p.chilledWaterPump.faultCode != "" || p.coolingWaterPump.faultCode != "" || p.coolingTower.faultCode != ""

	available := make([]bool, len(p.chillers))
	availCap := make([]float64, len(p.chillers))
	totalAvailCapKW := 0.0
	weightedSetpointSum := 0.0

	for i := range p.chillers {
		ch := &p.chillers[i]
		chCfg := p.config.Chillers[i]
		isAvail := ch.runRequested && ch.faultCode == "" && !auxFault && flowReady && p.coolingTower.running
		if ch.runRequested && ch.faultCode == "" && ch.running && (!flowReady || !p.coolingTower.running || auxFault) {
			p.falseTrips++
		}
		available[i] = isAvail
		if isAvail {
			capKW := chCfg.RatedCoolingCapacityKW * ch.loadLimitPct / 100
			availCap[i] = capKW
			totalAvailCapKW += capKW
			weightedSetpointSum += capKW * effectiveSetpointForState(ch)
		}
	}

	totalDeliveredKW := 0.0
	for i := range p.chillers {
		ch := &p.chillers[i]
		chCfg := p.config.Chillers[i]
		targetCapKW := 0.0
		if available[i] && totalAvailCapKW > 0 {
			share := availCap[i] / totalAvailCapKW
			targetCapKW = math.Min(p.zone.coolingDemandKW*share, availCap[i])
		}
		if ch.faultCode != "" || auxFault {
			ch.coolingCapacityKW = 0
		} else {
			ch.coolingCapacityKW = approach(ch.coolingCapacityKW, targetCapKW, elapsed, chillerCapacityTimeConstant)
		}
		ch.running = available[i] || ch.coolingCapacityKW > 1
		ch.loadPct = 100 * ch.coolingCapacityKW / chCfg.RatedCoolingCapacityKW
		ch.enteringCoolingWaterC = p.coolingTower.leavingWaterC
		ch.leavingCoolingWaterC = p.coolingTower.enteringWaterC
		totalDeliveredKW += ch.coolingCapacityKW
	}

	var targetHeaderSP float64
	if totalAvailCapKW > 0 {
		targetHeaderSP = weightedSetpointSum / totalAvailCapKW
	} else {
		sumSP := 0.0
		for i := range p.chillers {
			sumSP += effectiveSetpointForState(&p.chillers[i])
		}
		targetHeaderSP = sumSP / float64(len(p.chillers))
	}

	flowM3H := p.chilledWaterPump.flowM3H
	if flowM3H <= 0 {
		p.headerLeavingCHWC = approach(p.headerLeavingCHWC, p.headerEnteringCHWC, elapsed, 12*time.Minute)
	} else {
		waterCapKWPerC := waterHeatCapacityKWPerM3HDeltaC * flowM3H
		unmetLoadKW := math.Max(p.zone.coolingDemandKW-totalDeliveredKW, 0)
		supplyTargetC := targetHeaderSP + unmetLoadKW/waterCapKWPerC
		p.headerLeavingCHWC = approach(p.headerLeavingCHWC, supplyTargetC, elapsed, p.chilledWaterLag)
		p.headerEnteringCHWC = p.headerLeavingCHWC + totalDeliveredKW/waterCapKWPerC
	}

	for i := range p.chillers {
		ch := &p.chillers[i]
		chCfg := p.config.Chillers[i]
		ch.leavingChilledWaterC = p.headerLeavingCHWC
		ch.enteringChilledWaterC = p.headerEnteringCHWC
		if ch.coolingCapacityKW <= 0 {
			ch.loadPct = 0
			ch.powerKW = 0
			ch.cop = 0
			continue
		}
		ch.cop = chillerEffectiveCOP(
			chCfg.BaseCOP,
			effectiveSetpointForState(ch),
			ch.enteringCoolingWaterC,
			ch.leavingCoolingWaterC,
			ch.loadPct,
			p.config.Mismatch,
		)
		ch.powerKW = ch.coolingCapacityKW / ch.cop
	}
}

func effectiveSetpointForState(ch *chillerState) float64 {
	if ch.registerReadOnlyLocked {
		return ch.lockedFeedbackValue
	}
	return ch.setpointC
}

func (p *CoupledChillerPlant) updateZoneTemperatureLocked(elapsed time.Duration) {
	totalCoolingKW := 0.0
	for i := range p.chillers {
		totalCoolingKW += p.chillers[i].coolingCapacityKW
	}
	imbalanceKW := p.effectiveCoolingLoadKW() - totalCoolingKW
	p.zone.temperatureC += imbalanceKW * elapsed.Hours() / p.zoneThermalMassKWhPerC()
}

// SetControlCabinetMode switches a controllable device between LOCAL manual and REMOTE auto mode.
func (p *CoupledChillerPlant) SetControlCabinetMode(deviceID, mode string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(mode))
	if normalized != ControlCabinetModeRemote && normalized != ControlCabinetModeLocal {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.chillerIndexByID[deviceID]; ok {
		p.chillers[idx].localRemoteMode = normalized
		return true
	}
	switch deviceID {
	case p.config.ChilledWaterPump.ID:
		p.chilledWaterPump.localRemoteMode = normalized
	case p.config.CoolingWaterPump.ID:
		p.coolingWaterPump.localRemoteMode = normalized
	case p.config.CoolingTower.ID:
		p.coolingTower.localRemoteMode = normalized
	default:
		return false
	}
	return true
}

// SetLocalManualTarget applies a local cabinet manual dial adjustment directly in plant memory.
func (p *CoupledChillerPlant) SetLocalManualTarget(deviceID string, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.chillerIndexByID[deviceID]; ok {
		if value < 5 || value > 12 {
			return false
		}
		p.chillers[idx].setpointC = value
		return true
	}
	switch deviceID {
	case p.config.ChilledWaterPump.ID:
		if value < 20 || value > 50 {
			return false
		}
		p.chilledWaterPump.frequencySetpointHz = value
	case p.config.CoolingWaterPump.ID:
		if value < 20 || value > 50 {
			return false
		}
		p.coolingWaterPump.frequencySetpointHz = value
	case p.config.CoolingTower.ID:
		if value < 20 || value > 100 {
			return false
		}
		p.coolingTower.fanSpeedSetpointPct = value
	default:
		return false
	}
	return true
}

// ApplyCommand validates and applies one remote control command to the coupled plant.
func (p *CoupledChillerPlant) ApplyCommand(command Command) CommandResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	method := strings.TrimSpace(command.Method)

	if idx, ok := p.chillerIndexByID[command.DeviceID]; ok {
		if p.chillers[idx].localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return p.applyChillerCommandLocked(idx, method, command.Params)
	}
	switch command.DeviceID {
	case p.config.ChilledWaterPump.ID:
		if p.chilledWaterPump.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return applyPumpCommand(&p.chilledWaterPump, method, command.Params)
	case p.config.CoolingWaterPump.ID:
		if p.coolingWaterPump.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return applyPumpCommand(&p.coolingWaterPump, method, command.Params)
	case p.config.CoolingTower.ID:
		if p.coolingTower.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return p.applyTowerCommandLocked(method, command.Params)
	default:
		return CommandResult{Success: false, Code: "DEVICE_NOT_CONTROLLABLE"}
	}
}

func (p *CoupledChillerPlant) applyChillerCommandLocked(idx int, method string, params map[string]float64) CommandResult {
	ch := &p.chillers[idx]
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters()
		}
		if ch.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE"}
		}
		if !p.chilledWaterPump.runRequested || !p.chilledWaterPump.running || p.chilledWaterPump.faultCode != "" ||
			!p.coolingWaterPump.runRequested || !p.coolingWaterPump.running || p.coolingWaterPump.faultCode != "" ||
			!p.coolingTower.runRequested || !p.coolingTower.running || p.coolingTower.faultCode != "" {
			p.interlockViolations++
			return CommandResult{Success: false, Code: "INTERLOCK_OPEN"}
		}
		if !p.flowReadyLocked() {
			p.interlockViolations++
			return CommandResult{Success: false, Code: "CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN"}
		}
		ch.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stop":
		if len(params) != 0 {
			return invalidParameters()
		}
		ch.runRequested = false
		return CommandResult{Success: true, Code: "APPLIED"}
	case "setChilledWaterTemperatureSetpoint":
		value, ok := singleCommandParam(params, "setpointC")
		if !ok {
			return invalidParameters()
		}
		if value < 5 || value > 12 {
			return CommandResult{Success: false, Code: "SETPOINT_OUT_OF_RANGE"}
		}
		ch.setpointC = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "setLoadLimit":
		value, ok := singleCommandParam(params, "loadLimitPct")
		if !ok {
			return invalidParameters()
		}
		if value < 20 || value > 100 {
			return CommandResult{Success: false, Code: "LOAD_LIMIT_OUT_OF_RANGE"}
		}
		ch.loadLimitPct = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters()
		}
		ch.faultCode = ""
		return CommandResult{Success: true, Code: "APPLIED"}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED"}
	}
}

func (p *CoupledChillerPlant) applyTowerCommandLocked(method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters()
		}
		if p.coolingTower.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE"}
		}
		p.coolingTower.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stop":
		if len(params) != 0 {
			return invalidParameters()
		}
		p.coolingTower.runRequested = false
		return CommandResult{Success: true, Code: "APPLIED"}
	case "setFanSpeed":
		value, ok := singleCommandParam(params, "fanSpeedPct")
		if !ok {
			return invalidParameters()
		}
		if value < 20 || value > 100 {
			return CommandResult{Success: false, Code: "FAN_SPEED_OUT_OF_RANGE"}
		}
		p.coolingTower.fanSpeedSetpointPct = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters()
		}
		p.coolingTower.faultCode = ""
		return CommandResult{Success: true, Code: "APPLIED"}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED"}
	}
}

// DeviceSnapshot returns telemetry for a single device ID without allocating the full plant map.
func (p *CoupledChillerPlant) DeviceSnapshot(deviceID string) (time.Time, DeviceTelemetry, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if idx, ok := p.chillerIndexByID[deviceID]; ok {
		ch := p.chillers[idx]
		return p.now, DeviceTelemetry{
			"runState":                         runState(ch.running, ch.faultCode),
			"leavingChilledWaterTemperatureC":  round(ch.leavingChilledWaterC+p.config.Mismatch.CHWSupplySensorBiasC, 3),
			"enteringChilledWaterTemperatureC": round(ch.enteringChilledWaterC, 3),
			"chilledWaterTemperatureSetpointC": round(effectiveSetpointForState(&ch), 3),
			"leavingCoolingWaterTemperatureC":  round(ch.leavingCoolingWaterC, 3),
			"enteringCoolingWaterTemperatureC": round(ch.enteringCoolingWaterC, 3),
			"compressorLoadPct":                round(ch.loadPct, 3),
			"coolingCapacityKw":                round(ch.coolingCapacityKW, 3),
			"powerKw":                          round(ch.powerKW, 3),
			"energyKwh":                        round(ch.energyKWh, 6),
			"cop":                              round(ch.cop, 3),
			"loadLimitPct":                     round(ch.loadLimitPct, 3),
			"faultCode":                        ch.faultCode,
			"localRemoteMode":                  normalizedCabinetMode(ch.localRemoteMode),
		}, true
	}
	switch deviceID {
	case p.config.ChilledWaterPump.ID:
		return p.now, pumpTelemetry(p.chilledWaterPump), true
	case p.config.CoolingWaterPump.ID:
		return p.now, pumpTelemetry(p.coolingWaterPump), true
	case p.config.CoolingTower.ID:
		return p.now, DeviceTelemetry{
			"runState":                   runState(p.coolingTower.running, p.coolingTower.faultCode),
			"fanSpeedPct":                round(p.coolingTower.fanSpeedPct, 3),
			"enteringWaterTemperatureC":  round(p.coolingTower.enteringWaterC, 3),
			"leavingWaterTemperatureC":   round(p.coolingTower.leavingWaterC, 3),
			"ambientWetBulbTemperatureC": round(p.inputs.AmbientWetBulbC, 3),
			"approachTemperatureC":       round(p.coolingTower.leavingWaterC-p.inputs.AmbientWetBulbC, 3),
			"powerKw":                    round(p.coolingTower.powerKW, 3),
			"energyKwh":                  round(p.coolingTower.energyKWh, 6),
			"faultCode":                  p.coolingTower.faultCode,
			"localRemoteMode":            normalizedCabinetMode(p.coolingTower.localRemoteMode),
		}, true
	case p.config.PowerMeterID:
		totalPowerKW := p.chilledWaterPump.powerKW + p.coolingWaterPump.powerKW + p.coolingTower.powerKW
		for i := range p.chillers {
			totalPowerKW += p.chillers[i].powerKW
		}
		return p.now, DeviceTelemetry{
			"activePowerKw": round(totalPowerKW, 3),
			"energyKwh":     round(p.totalEnergyKWh, 6),
		}, true
	case p.config.BTUMeterID:
		observedCHWSupplyC := p.headerLeavingCHWC + p.config.Mismatch.CHWSupplySensorBiasC
		totalCoolingKW := 0.0
		for i := range p.chillers {
			totalCoolingKW += p.chillers[i].coolingCapacityKW
		}
		return p.now, DeviceTelemetry{
			"supplyWaterTemperatureC":     round(observedCHWSupplyC, 3),
			"returnWaterTemperatureC":     round(p.headerEnteringCHWC, 3),
			"temperatureDifferenceC":      round(p.headerEnteringCHWC-observedCHWSupplyC, 3),
			"flowRateM3h":                 round(p.chilledWaterPump.flowM3H, 3),
			"instantCoolingCapacityKw":    round(totalCoolingKW, 3),
			"accumulatedCoolingEnergyKwh": round(p.totalCoolingEnergyKWh, 6),
		}, true
	case p.config.WeatherStationID:
		return p.now, DeviceTelemetry{
			"ambientDryBulbTemperatureC": round(p.inputs.AmbientDryBulbC, 3),
			"ambientWetBulbTemperatureC": round(p.inputs.AmbientWetBulbC, 3),
			"relativeHumidityPct":        round(clamp(100-5*(p.inputs.AmbientDryBulbC-p.inputs.AmbientWetBulbC), 5, 100), 3),
		}, true
	case p.config.ZoneSensorID:
		return p.now, DeviceTelemetry{
			"zoneTemperatureC": round(p.zone.temperatureC+p.config.Mismatch.ZoneTempSensorBiasC, 3),
		}, true
	default:
		return time.Time{}, nil, false
	}
}

// Snapshot returns the telemetry snapshot including configured sensor biases.
func (p *CoupledChillerPlant) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshotLocked()
}

func (p *CoupledChillerPlant) snapshotLocked() Snapshot {
	devices := make(map[string]DeviceTelemetry, len(p.chillers)+7)
	totalPowerKW := p.chilledWaterPump.powerKW + p.coolingWaterPump.powerKW + p.coolingTower.powerKW
	totalCoolingKW := 0.0
	observedCHWSupplyC := p.headerLeavingCHWC + p.config.Mismatch.CHWSupplySensorBiasC
	observedZoneTempC := p.zone.temperatureC + p.config.Mismatch.ZoneTempSensorBiasC

	for i, chCfg := range p.config.Chillers {
		ch := p.chillers[i]
		totalPowerKW += ch.powerKW
		totalCoolingKW += ch.coolingCapacityKW
		devices[chCfg.ID] = DeviceTelemetry{
			"runState":                         runState(ch.running, ch.faultCode),
			"leavingChilledWaterTemperatureC":  round(ch.leavingChilledWaterC+p.config.Mismatch.CHWSupplySensorBiasC, 3),
			"enteringChilledWaterTemperatureC": round(ch.enteringChilledWaterC, 3),
			"chilledWaterTemperatureSetpointC": round(effectiveSetpointForState(&ch), 3),
			"leavingCoolingWaterTemperatureC":  round(ch.leavingCoolingWaterC, 3),
			"enteringCoolingWaterTemperatureC": round(ch.enteringCoolingWaterC, 3),
			"compressorLoadPct":                round(ch.loadPct, 3),
			"coolingCapacityKw":                round(ch.coolingCapacityKW, 3),
			"powerKw":                          round(ch.powerKW, 3),
			"energyKwh":                        round(ch.energyKWh, 6),
			"cop":                              round(ch.cop, 3),
			"loadLimitPct":                     round(ch.loadLimitPct, 3),
			"faultCode":                        ch.faultCode,
			"faultActive":                      ch.faultCode != "",
			"localRemoteMode":                  normalizedCabinetMode(ch.localRemoteMode),
		}
	}
	devices[p.config.ChilledWaterPump.ID] = pumpTelemetryWithFaultBool(p.chilledWaterPump)
	devices[p.config.CoolingWaterPump.ID] = pumpTelemetryWithFaultBool(p.coolingWaterPump)
	devices[p.config.CoolingTower.ID] = DeviceTelemetry{
		"runState":                   runState(p.coolingTower.running, p.coolingTower.faultCode),
		"fanSpeedPct":                round(p.coolingTower.fanSpeedPct, 3),
		"enteringWaterTemperatureC":  round(p.coolingTower.enteringWaterC, 3),
		"leavingWaterTemperatureC":   round(p.coolingTower.leavingWaterC, 3),
		"ambientWetBulbTemperatureC": round(p.inputs.AmbientWetBulbC, 3),
		"approachTemperatureC":       round(p.coolingTower.leavingWaterC-p.inputs.AmbientWetBulbC, 3),
		"powerKw":                    round(p.coolingTower.powerKW, 3),
		"energyKwh":                  round(p.coolingTower.energyKWh, 6),
		"faultCode":                  p.coolingTower.faultCode,
		"faultActive":                p.coolingTower.faultCode != "",
		"localRemoteMode":            normalizedCabinetMode(p.coolingTower.localRemoteMode),
	}
	devices[p.config.PowerMeterID] = DeviceTelemetry{
		"activePowerKw": round(totalPowerKW, 3),
		"energyKwh":     round(p.totalEnergyKWh, 6),
	}
	devices[p.config.BTUMeterID] = DeviceTelemetry{
		"supplyWaterTemperatureC":     round(observedCHWSupplyC, 3),
		"returnWaterTemperatureC":     round(p.headerEnteringCHWC, 3),
		"temperatureDifferenceC":      round(p.headerEnteringCHWC-observedCHWSupplyC, 3),
		"flowRateM3h":                 round(p.chilledWaterPump.flowM3H, 3),
		"instantCoolingCapacityKw":    round(totalCoolingKW, 3),
		"accumulatedCoolingEnergyKwh": round(p.totalCoolingEnergyKWh, 6),
	}
	devices[p.config.WeatherStationID] = DeviceTelemetry{
		"ambientDryBulbTemperatureC": round(p.inputs.AmbientDryBulbC, 3),
		"ambientWetBulbTemperatureC": round(p.inputs.AmbientWetBulbC, 3),
		"relativeHumidityPct":        round(clamp(100-5*(p.inputs.AmbientDryBulbC-p.inputs.AmbientWetBulbC), 5, 100), 3),
	}
	devices[p.config.ZoneSensorID] = DeviceTelemetry{
		"zoneTemperatureC": round(observedZoneTempC, 3),
	}
	return Snapshot{ObservedAt: p.now, Devices: devices}
}

func pumpTelemetryWithFaultBool(state pumpState) DeviceTelemetry {
	t := pumpTelemetry(state)
	t["faultActive"] = state.faultCode != ""
	return t
}

// TruthState returns uncorrupted physical plant telemetry, energy sub-meters, and comfort states.
func (p *CoupledChillerPlant) TruthState() CoupledPlantTruthState {
	p.mu.RLock()
	defer p.mu.RUnlock()

	totalPowerKW := p.chilledWaterPump.powerKW + p.coolingWaterPump.powerKW + p.coolingTower.powerKW
	totalCoolingKW := 0.0
	chillerEnergy := 0.0
	chStates := make([]ChillerTruthState, len(p.chillers))
	for i, ch := range p.chillers {
		totalPowerKW += ch.powerKW
		totalCoolingKW += ch.coolingCapacityKW
		chillerEnergy += ch.energyKWh
		chStates[i] = ChillerTruthState{
			ID:                p.config.Chillers[i].ID,
			RunRequested:      ch.runRequested,
			Running:           ch.running,
			SetpointC:         effectiveSetpointForState(&ch),
			LoadLimitPct:      ch.loadLimitPct,
			CoolingCapacityKW: ch.coolingCapacityKW,
			PowerKW:           ch.powerKW,
			EnergyKWh:         ch.energyKWh,
			COP:               ch.cop,
			LoadPct:           ch.loadPct,
			LocalRemoteMode:   normalizedCabinetMode(ch.localRemoteMode),
		}
	}
	return CoupledPlantTruthState{
		ObservedAt:            p.now,
		AmbientDryBulbC:       p.inputs.AmbientDryBulbC,
		AmbientWetBulbC:       p.inputs.AmbientWetBulbC,
		TrueZoneTempC:         p.zone.temperatureC,
		ObservedZoneTempC:     p.zone.temperatureC + p.config.Mismatch.ZoneTempSensorBiasC,
		TrueCHWSupplyC:        p.headerLeavingCHWC,
		ObservedCHWSupplyC:    p.headerLeavingCHWC + p.config.Mismatch.CHWSupplySensorBiasC,
		TrueCHWReturnC:        p.headerEnteringCHWC,
		TrueCoolingLoadKW:     p.effectiveCoolingLoadKW(),
		TotalCoolingKW:        totalCoolingKW,
		TotalPowerKW:          totalPowerKW,
		TotalEnergyKWh:        p.totalEnergyKWh,
		ChillerEnergyKWh:      chillerEnergy,
		CHWPEnergyKWh:         p.chilledWaterPump.energyKWh,
		CWPEnergyKWh:          p.coolingWaterPump.energyKWh,
		TowerEnergyKWh:        p.coolingTower.energyKWh,
		TotalCoolingEnergyKWh: p.totalCoolingEnergyKWh,
		CHWPFreqSetpointHz:    p.chilledWaterPump.frequencySetpointHz,
		CHWPFreqHz:            p.chilledWaterPump.frequencyHz,
		CHWPFlowM3H:           p.chilledWaterPump.flowM3H,
		CHWPLocalMode:         normalizedCabinetMode(p.chilledWaterPump.localRemoteMode),
		CWPFreqSetpointHz:     p.coolingWaterPump.frequencySetpointHz,
		CWPFreqHz:             p.coolingWaterPump.frequencyHz,
		CWPFlowM3H:            p.coolingWaterPump.flowM3H,
		CWPLocalMode:          normalizedCabinetMode(p.coolingWaterPump.localRemoteMode),
		TowerFanSetpointPct:   p.coolingTower.fanSpeedSetpointPct,
		TowerFanPct:           p.coolingTower.fanSpeedPct,
		TowerApproachC:        p.coolingTower.leavingWaterC - p.inputs.AmbientWetBulbC,
		TowerLeavingWaterC:    p.coolingTower.leavingWaterC,
		TowerEnteringWaterC:   p.coolingTower.enteringWaterC,
		TowerLocalMode:        normalizedCabinetMode(p.coolingTower.localRemoteMode),
		Chillers:              chStates,
		FalseTrips:            p.falseTrips,
		InterlockViolations:   p.interlockViolations,
	}
}

// BuildEdgeAdapters returns edgecontrol.DeviceAdapters wired to this coupled truth plant.
func (p *CoupledChillerPlant) BuildEdgeAdapters() []edgecontrol.DeviceAdapter {
	adapters := make([]edgecontrol.DeviceAdapter, 0, len(p.config.Chillers)+7)
	for _, ch := range p.config.Chillers {
		adapters = append(adapters, newCoupledAdapter(p, ch.ID, "CHILLER", edgecontrol.ProfileChiller, []coupledChannelMap{
			{channelID: "RunState", sourceKey: "runState", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticRunState},
			{channelID: "Fault", sourceKey: "faultCode", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFaultCode},
			{channelID: "LocalRemoteMode", sourceKey: "localRemoteMode", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticLocalRemoteMode},
			{channelID: "CapacityKW", sourceKey: "coolingCapacityKw", dt: edgecontrol.DataTypeDouble, unit: "kW", access: edgecontrol.AccessReadOnly},
			{channelID: "PowerKW", sourceKey: "powerKw", dt: edgecontrol.DataTypeDouble, unit: "kW", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticPower},
			{channelID: "LeavingCHWTempC", sourceKey: "leavingChilledWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
			{channelID: "EnteringCHWTempC", sourceKey: "enteringChilledWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
			{channelID: "EnteringCWTempC", sourceKey: "enteringCoolingWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
			{channelID: "LeavingCWTempC", sourceKey: "leavingCoolingWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
			{channelID: "Start", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticStartCommand, cmdMethod: "start"},
			{channelID: "Stop", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticStopCommand, cmdMethod: "stop"},
			{channelID: "ResetFault", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticResetFaultCommand, cmdMethod: "resetFault"},
			{channelID: "SupplySp", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticTemperatureSetpoint, cmdMethod: "setChilledWaterTemperatureSetpoint", cmdParam: "setpointC"},
			{channelID: "LoadLimit", dt: edgecontrol.DataTypeDouble, unit: "%", access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticLoadLimit, cmdMethod: "setLoadLimit", cmdParam: "loadLimitPct"},
		}))
	}
	for _, pump := range []PumpConfig{p.config.ChilledWaterPump, p.config.CoolingWaterPump} {
		adapters = append(adapters, newCoupledAdapter(p, pump.ID, "PUMP", edgecontrol.ProfileVariableSpeedPump, []coupledChannelMap{
			{channelID: "RunState", sourceKey: "runState", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticRunState},
			{channelID: "Fault", sourceKey: "faultCode", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFaultCode},
			{channelID: "LocalRemoteMode", sourceKey: "localRemoteMode", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticLocalRemoteMode},
			{channelID: "Flow", sourceKey: "flowRateM3h", dt: edgecontrol.DataTypeDouble, unit: "m3/h", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFlow},
			{channelID: "Frequency", sourceKey: "frequencyHz", dt: edgecontrol.DataTypeDouble, unit: "Hz", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFrequency},
			{channelID: "PowerKW", sourceKey: "powerKw", dt: edgecontrol.DataTypeDouble, unit: "kW", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticPower},
			{channelID: "Start", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticStartCommand, cmdMethod: "start"},
			{channelID: "Stop", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticStopCommand, cmdMethod: "stop"},
			{channelID: "ResetFault", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticResetFaultCommand, cmdMethod: "resetFault"},
			{channelID: "FreqSp", dt: edgecontrol.DataTypeDouble, unit: "Hz", access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticFrequencySetpoint, cmdMethod: "setFrequency", cmdParam: "frequencyHz"},
		}))
	}
	adapters = append(adapters, newCoupledAdapter(p, p.config.CoolingTower.ID, "TOWER", edgecontrol.ProfileCoolingTower, []coupledChannelMap{
		{channelID: "RunState", sourceKey: "runState", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticRunState},
		{channelID: "Fault", sourceKey: "faultCode", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFaultCode},
		{channelID: "LocalRemoteMode", sourceKey: "localRemoteMode", dt: edgecontrol.DataTypeString, access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticLocalRemoteMode},
		{channelID: "FanSpeed", sourceKey: "fanSpeedPct", dt: edgecontrol.DataTypeDouble, unit: "%", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFanSpeed},
		{channelID: "LeavingWaterTempC", sourceKey: "leavingWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
		{channelID: "EnteringWaterTempC", sourceKey: "enteringWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
		{channelID: "PowerKW", sourceKey: "powerKw", dt: edgecontrol.DataTypeDouble, unit: "kW", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticPower},
		{channelID: "Start", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticStartCommand, cmdMethod: "start"},
		{channelID: "Stop", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticStopCommand, cmdMethod: "stop"},
		{channelID: "ResetFault", dt: edgecontrol.DataTypeBoolean, access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticResetFaultCommand, cmdMethod: "resetFault"},
		{channelID: "FanSp", dt: edgecontrol.DataTypeDouble, unit: "%", access: edgecontrol.AccessWriteOnly, semantic: edgecontrol.SemanticFanSpeedSetpoint, cmdMethod: "setFanSpeed", cmdParam: "fanSpeedPct"},
	}))
	adapters = append(adapters, newCoupledAdapter(p, p.config.BTUMeterID, "BTU_METER", edgecontrol.ProfileThermalEnergyMeter, []coupledChannelMap{
		{channelID: "SupplyTempC", sourceKey: "supplyWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticSupplyTemperature},
		{channelID: "ReturnTempC", sourceKey: "returnWaterTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticReturnTemperature},
		{channelID: "DeltaTC", sourceKey: "temperatureDifferenceC", dt: edgecontrol.DataTypeDouble, unit: "K", access: edgecontrol.AccessReadOnly},
		{channelID: "FlowM3H", sourceKey: "flowRateM3h", dt: edgecontrol.DataTypeDouble, unit: "m3/h", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticFlow},
		{channelID: "CoolingCapacityKW", sourceKey: "instantCoolingCapacityKw", dt: edgecontrol.DataTypeDouble, unit: "kW", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticCoolingCapacity},
		{channelID: "CoolingEnergyKWh", sourceKey: "accumulatedCoolingEnergyKwh", dt: edgecontrol.DataTypeDouble, unit: "kWh", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticCoolingEnergy},
	}))
	adapters = append(adapters, newCoupledAdapter(p, p.config.PowerMeterID, "METER", edgecontrol.ProfileElectricityMeter, []coupledChannelMap{
		{channelID: "ActivePowerKW", sourceKey: "activePowerKw", dt: edgecontrol.DataTypeDouble, unit: "kW", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticPower},
		{channelID: "EnergyKWh", sourceKey: "energyKwh", dt: edgecontrol.DataTypeDouble, unit: "kWh", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticEnergy},
	}))
	adapters = append(adapters, newCoupledAdapter(p, p.config.WeatherStationID, "WEATHER", edgecontrol.ProfileWeatherStation, []coupledChannelMap{
		{channelID: "DryBulbC", sourceKey: "ambientDryBulbTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticOutdoorTemperature},
		{channelID: "WetBulbC", sourceKey: "ambientWetBulbTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly},
		{channelID: "RelativeHumidityPct", sourceKey: "relativeHumidityPct", dt: edgecontrol.DataTypeDouble, unit: "%RH", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticOutdoorHumidity},
	}))
	adapters = append(adapters, newCoupledAdapter(p, p.config.ZoneSensorID, "ZONE_SENSOR", edgecontrol.ProfileZoneTemperatureSensor, []coupledChannelMap{
		{channelID: "ZoneTempC", sourceKey: "zoneTemperatureC", dt: edgecontrol.DataTypeDouble, unit: "Cel", access: edgecontrol.AccessReadOnly, semantic: edgecontrol.SemanticZoneTemperature},
	}))
	return adapters
}

type coupledChannelMap struct {
	channelID string
	sourceKey string
	dt        edgecontrol.DataType
	unit      string
	access    edgecontrol.AccessMode
	semantic  edgecontrol.SemanticChannel
	cmdMethod string
	cmdParam  string
}

type coupledPlantAdapter struct {
	plant     *CoupledChillerPlant
	component edgecontrol.ComponentDescriptor
	channels  []edgecontrol.ChannelDescriptor
	byAddr    map[string]coupledChannelMap
}

func newCoupledAdapter(plant *CoupledChillerPlant, compID, compType string, profile edgecontrol.CapabilityProfileID, specs []coupledChannelMap) *coupledPlantAdapter {
	chans := make([]edgecontrol.ChannelDescriptor, len(specs))
	byAddr := make(map[string]coupledChannelMap, len(specs))
	bindings := make(map[edgecontrol.SemanticChannel]string, len(specs))
	for i, spec := range specs {
		ch := edgecontrol.ChannelDescriptor{
			ComponentID:               compID,
			ChannelID:                 spec.channelID,
			PointID:                   compID + ":" + spec.channelID,
			DataType:                  spec.dt,
			Unit:                      spec.unit,
			Access:                    spec.access,
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              edgecontrol.PriorityHigh,
			LocalPersistencePriority:  edgecontrol.PriorityHigh,
			RemotePersistencePriority: edgecontrol.PriorityHigh,
			AggregationPriority:       edgecontrol.PriorityHigh,
			ResendPriority:            edgecontrol.PriorityHigh,
		}
		chans[i] = ch
		byAddr[ch.Address()] = spec
		if spec.semantic != "" {
			bindings[spec.semantic] = ch.Address()
		}
	}
	return &coupledPlantAdapter{
		plant: plant,
		component: edgecontrol.ComponentDescriptor{
			ID:              compID,
			Enabled:         true,
			Kind:            edgecontrol.ComponentSimulator,
			Type:            compType,
			FactoryID:       "COUPLED_BENCHMARK_SIM",
			Version:         "1.0.0",
			Profiles:        []edgecontrol.CapabilityProfileID{profile},
			ChannelBindings: bindings,
		},
		channels: chans,
		byAddr:   byAddr,
	}
}

func (a *coupledPlantAdapter) Component() edgecontrol.ComponentDescriptor { return a.component }
func (a *coupledPlantAdapter) Channels() []edgecontrol.ChannelDescriptor {
	return slices.Clone(a.channels)
}

func (a *coupledPlantAdapter) Poll(_ context.Context, at time.Time) ([]edgecontrol.ChannelUpdate, error) {
	observedAt, dev, ok := a.plant.DeviceSnapshot(a.component.ID)
	if !ok {
		return nil, fmt.Errorf("missing device %s", a.component.ID)
	}
	if observedAt.IsZero() {
		observedAt = at
	}
	out := make([]edgecontrol.ChannelUpdate, 0, len(a.channels))
	for _, ch := range a.channels {
		spec := a.byAddr[ch.Address()]
		if spec.access == edgecontrol.AccessWriteOnly || spec.sourceKey == "" {
			continue
		}
		raw, ok := dev[spec.sourceKey]
		if !ok {
			continue
		}
		val, err := edgeValue(ch.DataType, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, edgecontrol.ChannelUpdate{
			Address: ch.Address(),
			Sample:  edgecontrol.Sample{Value: val, Quality: edgecontrol.QualityGood, ObservedAt: observedAt},
		})
	}
	return out, nil
}

func (a *coupledPlantAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, decisions []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	results := make([]edgecontrol.DeviceWriteResult, 0, len(decisions))
	for _, d := range decisions {
		if !d.Accepted || d.Effective == nil {
			continue
		}
		spec, ok := a.byAddr[d.Address]
		if !ok || spec.cmdMethod == "" {
			results = append(results, edgecontrol.DeviceWriteResult{Address: d.Address, Success: false, Code: "COMMAND_CHANNEL_INVALID"})
			continue
		}
		params := map[string]float64{}
		if spec.cmdParam == "" {
			if d.Effective.Type != edgecontrol.DataTypeBoolean || !d.Effective.Boolean {
				results = append(results, edgecontrol.DeviceWriteResult{Address: d.Address, Success: false, Code: "ACTION_TRIGGER_REQUIRED"})
				continue
			}
		} else {
			num, numOK := d.Effective.NumericFloat64()
			if !numOK {
				results = append(results, edgecontrol.DeviceWriteResult{Address: d.Address, Success: false, Code: "COMMAND_PARAMETER_INVALID"})
				continue
			}
			params[spec.cmdParam] = num
		}
		res := a.plant.ApplyCommand(Command{DeviceID: a.component.ID, Method: spec.cmdMethod, Params: params})
		applied := *d.Effective
		results = append(results, edgecontrol.DeviceWriteResult{
			Address:          d.Address,
			Success:          res.Success,
			Code:             res.Code,
			AppliedValue:     &applied,
			ReadbackVerified: res.Success,
		})
	}
	return results, nil
}

// TOUTariff prices electricity in CNY/kWh by local wall clock and day regime.
type TOUTariff struct {
	Location             *time.Location
	ValleyCNYPerKWh      float64
	FlatCNYPerKWh        float64
	PeakCNYPerKWh        float64
	CriticalPeakCNYPerKW float64
	CriticalPeakDays     map[int]bool
}

// CommercialTOUTariff returns a realistic Chinese commercial 10kV TOU tariff schedule.
func CommercialTOUTariff(loc *time.Location) TOUTariff {
	if loc == nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return TOUTariff{
		Location:             loc,
		ValleyCNYPerKWh:      0.38,
		FlatCNYPerKWh:        0.78,
		PeakCNYPerKWh:        1.28,
		CriticalPeakCNYPerKW: 1.58,
		CriticalPeakDays:     map[int]bool{0: true, 1: true, 2: true, 6: true},
	}
}

// RateAt returns the electricity price in CNY/kWh for timestamp at (and dayIndex >= 0).
func (t TOUTariff) RateAt(at time.Time, dayIndex int) float64 {
	local := at.In(t.Location)
	hour := local.Hour()
	switch {
	case hour < 8:
		return t.ValleyCNYPerKWh
	case (hour >= 14 && hour < 16) && t.CriticalPeakDays[dayIndex]:
		return t.CriticalPeakCNYPerKW
	case (hour >= 10 && hour < 12) || (hour >= 14 && hour < 18):
		return t.PeakCNYPerKWh
	default:
		return t.FlatCNYPerKWh
	}
}

// SevenDayBenchmarkSpec parameterizes the 7-day closed-loop mismatch suite.
type SevenDayBenchmarkSpec struct {
	PlantConfig         CoupledChillerPlantConfig
	StartLocal          time.Time
	Location            *time.Location
	Step                time.Duration
	Duration            time.Duration
	Tariff              TOUTariff
	OutageStart         time.Duration
	OutageDuration      time.Duration
	LocalOverrideStart  time.Duration
	LocalOverrideDur    time.Duration
	LocalOverrideDevice string
	LocalManualTarget   float64
}

// DefaultSevenDayMismatchSpec returns the standard 7-day commercial chiller plant
// benchmark with hot peak + mild shoulder days, TOU tariff, +0.3C sensor bias,
// pump static head, nonlinear cooling tower approach, -15% chiller COP mismatch,
// a 2-hour cloud disconnect, and a mid-run LOCAL cabinet manual override.
func DefaultSevenDayMismatchSpec() SevenDayBenchmarkSpec {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	startLocal := time.Date(2026, 7, 20, 0, 0, 0, 0, loc)
	return SevenDayBenchmarkSpec{
		PlantConfig: CoupledChillerPlantConfig{
			Chillers: []ChillerConfig{
				{
					ID:                     "CHILLER-01",
					RatedCoolingCapacityKW: 600,
					BaseCOP:                5.8,
					InitialSetpointC:       7.0,
					InitialLoadLimitPct:    100,
					InitiallyRunning:       true,
				},
				{
					ID:                     "CHILLER-02",
					RatedCoolingCapacityKW: 600,
					BaseCOP:                5.6,
					InitialSetpointC:       7.0,
					InitialLoadLimitPct:    100,
					InitiallyRunning:       true,
				},
			},
			ChilledWaterPump: PumpConfig{
				ID:                 "CHWP-01",
				RatedPowerKW:       45,
				RatedFlowM3H:       220,
				InitialFrequencyHz: 50,
				InitiallyRunning:   true,
			},
			CoolingWaterPump: PumpConfig{
				ID:                 "CWP-01",
				RatedPowerKW:       37,
				RatedFlowM3H:       260,
				InitialFrequencyHz: 50,
				InitiallyRunning:   true,
			},
			CoolingTower: CoolingTowerConfig{
				ID:                 "CT-01",
				RatedFanPowerKW:    22,
				InitialFanSpeedPct: 75,
				InitiallyRunning:   true,
			},
			PowerMeterID:     "METER-HVAC-TOTAL",
			BTUMeterID:       "BTU-METER-01",
			WeatherStationID: "WEATHER-STATION-01",
			ZoneSensorID:     "ZONE-SENSOR-01",
			ZoneTempMinC:     21.0,
			ZoneTempMaxC:     25.0,
			Mismatch: PlantMismatchConfig{
				CHWPStaticHeadFraction:  0.12,
				CWPStaticHeadFraction:   0.14,
				NonlinearTowerApproach:  true,
				TowerMinApproachC:       1.5,
				TowerLoadApproachCoeff:  1.9,
				TowerNonlinearExp:       1.25,
				TowerLowFanPenaltyC:     1.2,
				ChillerCOPMultiplier:    0.85, // -15% chiller efficiency mismatch vs nominal expectation
				CondenserMeanTempWeight: 0.45,
				CondenserCOPSlopePerC:   0.12,
				HighLoadCOPPenaltyCoeff: 0.015,
				CHWCoilFlowCoupling:     true,
				CHWCoilApproachMinC:     4.5,
				ThermalLoadMultiplier:   1.05,
				ThermalLoadBiasKW:       15.0,
				CHWSupplySensorBiasC:    0.3,
				ZoneTempSensorBiasC:     0.3,
			},
		},
		StartLocal:          startLocal,
		Location:            loc,
		Step:                5 * time.Minute,
		Duration:            7 * 24 * time.Hour,
		Tariff:              CommercialTOUTariff(loc),
		OutageStart:         37 * time.Hour, // Day 2 13:00-15:00 local
		OutageDuration:      2 * time.Hour,
		LocalOverrideStart:  82 * time.Hour, // Day 4 10:00-11:00 local
		LocalOverrideDur:    time.Hour,
		LocalOverrideDevice: "CHWP-01",
		LocalManualTarget:   46.0,
	}
}

type dayWeatherLoadProfile struct {
	dryMeanC   float64
	dryAmpC    float64
	wetMeanC   float64
	wetAmpC    float64
	unoccKW    float64
	occPeakKW  float64
	occFromHr  float64
	occToHr    float64
	rippleAmpK float64
}

var defaultSevenDayProfiles = [7]dayWeatherLoadProfile{
	// Day 1 (Mon): Hot sunny peak
	{dryMeanC: 32.0, dryAmpC: 4.5, wetMeanC: 27.0, wetAmpC: 1.2, unoccKW: 220, occPeakKW: 960, occFromHr: 8, occToHr: 20, rippleAmpK: 28},
	// Day 2 (Tue): Extreme hot peak + 2h cloud outage
	{dryMeanC: 32.8, dryAmpC: 4.2, wetMeanC: 27.3, wetAmpC: 1.1, unoccKW: 230, occPeakKW: 990, occFromHr: 8, occToHr: 20, rippleAmpK: 30},
	// Day 3 (Wed): Hot sunny peak
	{dryMeanC: 31.5, dryAmpC: 4.0, wetMeanC: 26.6, wetAmpC: 1.2, unoccKW: 215, occPeakKW: 910, occFromHr: 8, occToHr: 20, rippleAmpK: 25},
	// Day 4 (Thu): Mild overcast shoulder + LOCAL override
	{dryMeanC: 23.5, dryAmpC: 2.5, wetMeanC: 18.2, wetAmpC: 1.1, unoccKW: 175, occPeakKW: 460, occFromHr: 8, occToHr: 19, rippleAmpK: 18},
	// Day 5 (Fri): Cool overcast shoulder
	{dryMeanC: 22.8, dryAmpC: 2.2, wetMeanC: 17.6, wetAmpC: 1.0, unoccKW: 170, occPeakKW: 420, occFromHr: 8, occToHr: 19, rippleAmpK: 15},
	// Day 6 (Sat): Weekend light part-load
	{dryMeanC: 24.5, dryAmpC: 2.6, wetMeanC: 19.2, wetAmpC: 1.1, unoccKW: 155, occPeakKW: 330, occFromHr: 9, occToHr: 17, rippleAmpK: 12},
	// Day 7 (Sun/Recovery): Hot peak recovery
	{dryMeanC: 32.2, dryAmpC: 4.3, wetMeanC: 27.1, wetAmpC: 1.2, unoccKW: 210, occPeakKW: 930, occFromHr: 8, occToHr: 20, rippleAmpK: 26},
}

// InputsFor evaluates the deterministic 7-day diurnal weather and commercial load profile.
func (s SevenDayBenchmarkSpec) InputsFor(elapsed time.Duration, at time.Time) ScenarioInputs {
	local := at.In(s.Location)
	dayIdx := int(elapsed / (24 * time.Hour))
	if dayIdx < 0 {
		dayIdx = 0
	}
	if dayIdx >= len(defaultSevenDayProfiles) {
		dayIdx = len(defaultSevenDayProfiles) - 1
	}
	p := defaultSevenDayProfiles[dayIdx]
	hour := float64(local.Hour()) + float64(local.Minute())/60.0 + float64(local.Second())/3600.0
	dry := p.dryMeanC + p.dryAmpC*math.Cos(2*math.Pi*(hour-15.0)/24.0)
	wet := math.Min(p.wetMeanC+p.wetAmpC*math.Cos(2*math.Pi*(hour-15.0)/24.0), dry-0.5)
	occ := occupancy(hour, p.occFromHr, p.occToHr)
	bellZ := (hour - 14.5) / 3.2
	afternoonBell := math.Exp(-0.5 * bellZ * bellZ)
	ripple := p.rippleAmpK * (0.6*math.Sin(2*math.Pi*elapsed.Minutes()/75.0) + 0.4*math.Cos(2*math.Pi*elapsed.Minutes()/23.0))
	baseLoad := p.unoccKW + (p.occPeakKW-p.unoccKW)*(0.70*occ+0.30*occ*afternoonBell) + ripple
	return ScenarioInputs{
		AmbientDryBulbC: dry,
		AmbientWetBulbC: wet,
		CoolingLoadKW:   math.Max(120.0, baseLoad),
	}
}

// BenchmarkMetrics reports closed-loop performance strictly from the truth plant.
type BenchmarkMetrics struct {
	Strategy                    string  `json:"strategy"`
	TotalEnergyKWh              float64 `json:"totalEnergyKWh"`
	ChillerKWh                  float64 `json:"chillerKWh"`
	CHWPumpKWh                  float64 `json:"chwPumpKWh"`
	CWPumpKWh                   float64 `json:"cwPumpKWh"`
	TowerKWh                    float64 `json:"towerKWh"`
	TotalCostCNY                float64 `json:"totalCostCNY"`
	ZoneExceedMinutes           float64 `json:"zoneExceedMinutes"`
	ActuatorChanges             int     `json:"actuatorChanges"`
	ActuatorReversals           int     `json:"actuatorReversals"`
	FalseTrips                  int     `json:"falseTrips"`
	InterlockViolations         int     `json:"interlockViolations"`
	OutageHandledCleanly        bool    `json:"outageHandledCleanly"`
	LocalOverrideHandledCleanly bool    `json:"localOverrideHandledCleanly"`
	ElapsedMs                   int64   `json:"elapsedMs"`
	MaxTrueZoneTempC            float64 `json:"maxTrueZoneTempC"`
	MinTrueZoneTempC            float64 `json:"minTrueZoneTempC"`
	MeanTrueCOP                 float64 `json:"meanTrueCOP"`
}

// BenchmarkStepContext carries step state passed to a BenchmarkArm.
type BenchmarkStepContext struct {
	Now            time.Time
	Elapsed        time.Duration
	Step           time.Duration
	CloudConnected bool
	InLocalManual  bool
	Spec           SevenDayBenchmarkSpec
	Truth          CoupledPlantTruthState
	Observed       Snapshot
	TariffRateCNY  float64
}

// BenchmarkArm defines one closed-loop control strategy evaluated on the benchmark harness.
// Wave 3 P1 coupled optimizer and G36 Trim & Respond arms plug directly into this interface.
type BenchmarkArm interface {
	Name() string
	ConfigureHost(host *edgecontrol.Host, plant *CoupledChillerPlant, cfg CoupledChillerPlantConfig, start time.Time) error
	OnCloudConnectivityChanged(connected bool, at time.Time)
	OnStep(ctx context.Context, step BenchmarkStepContext) error
	OnCycleResult(res edgecontrol.HostCycleResult, step BenchmarkStepContext)
}

type actuatorTracker struct {
	initialized bool
	lastVal     float64
	lastDir     int
	lastMoveAt  time.Time
	deadband    float64
	window      time.Duration
}

func (tr *actuatorTracker) observe(val float64, at time.Time) (changed, reversed bool) {
	if !tr.initialized {
		tr.initialized = true
		tr.lastVal = val
		return false, false
	}
	diff := val - tr.lastVal
	if math.Abs(diff) < tr.deadband {
		return false, false
	}
	dir := 1
	if diff < 0 {
		dir = -1
	}
	if tr.lastDir != 0 && dir == -tr.lastDir && !tr.lastMoveAt.IsZero() && at.Sub(tr.lastMoveAt) <= tr.window {
		reversed = true
	}
	tr.lastVal = val
	tr.lastDir = dir
	tr.lastMoveAt = at
	return true, reversed
}

type discreteRunTracker struct {
	initialized  bool
	lastState    bool
	lastChangeAt time.Time
	window       time.Duration
}

func (tr *discreteRunTracker) observe(running bool, at time.Time) (changed, reversed bool) {
	if !tr.initialized {
		tr.initialized = true
		tr.lastState = running
		return false, false
	}
	if running == tr.lastState {
		return false, false
	}
	if !tr.lastChangeAt.IsZero() && at.Sub(tr.lastChangeAt) <= tr.window {
		reversed = true
	}
	tr.lastState = running
	tr.lastChangeAt = at
	return true, reversed
}

// RunPlantBenchmark runs a full closed-loop accelerated simulation of arm on spec.
func RunPlantBenchmark(ctx context.Context, spec SevenDayBenchmarkSpec, arm BenchmarkArm) (BenchmarkMetrics, error) {
	if arm == nil {
		return BenchmarkMetrics{}, errors.New("benchmark arm is required")
	}
	if spec.Step <= 0 {
		spec.Step = time.Minute
	}
	if spec.Duration <= 0 {
		spec.Duration = 7 * 24 * time.Hour
	}

	wallStart := time.Now()
	startUTC := spec.StartLocal.UTC()

	plant, err := NewCoupledChillerPlant(spec.PlantConfig, spec.InputsFor, startUTC)
	if err != nil {
		return BenchmarkMetrics{}, err
	}
	host, err := edgecontrol.NewHost()
	if err != nil {
		return BenchmarkMetrics{}, err
	}
	for _, ad := range plant.BuildEdgeAdapters() {
		if err := host.RegisterAdapter(ad); err != nil {
			return BenchmarkMetrics{}, err
		}
	}
	if err := arm.ConfigureHost(host, plant, spec.PlantConfig, startUTC); err != nil {
		return BenchmarkMetrics{}, err
	}

	totalSteps := int(spec.Duration / spec.Step)
	outageEnd := spec.OutageStart + spec.OutageDuration
	localEnd := spec.LocalOverrideStart + spec.LocalOverrideDur

	chillerRunTrackers := make([]discreteRunTracker, len(spec.PlantConfig.Chillers))
	chillerSPTrackers := make([]actuatorTracker, len(spec.PlantConfig.Chillers))
	for i := range spec.PlantConfig.Chillers {
		chillerRunTrackers[i] = discreteRunTracker{window: 30 * time.Minute}
		chillerSPTrackers[i] = actuatorTracker{deadband: 0.2, window: 15 * time.Minute}
	}
	chwpTracker := actuatorTracker{deadband: 0.5, window: 15 * time.Minute}
	cwpTracker := actuatorTracker{deadband: 0.5, window: 15 * time.Minute}
	towerTracker := actuatorTracker{deadband: 2.0, window: 15 * time.Minute}

	prevCloudConnected := true
	prevLocalManual := false

	totalCostCNY := 0.0
	prevTotalEnergyKWh := 0.0
	zoneExceedMinutes := 0.0
	actuatorChanges := 0
	actuatorReversals := 0
	extraInterlockViolations := 0

	outageFalseTrips := 0
	outageReversals := 0
	outageInterlocks := 0
	outageRunningSteps := 0
	outageTotalSteps := 0

	localRemoteWriteAttempts := 0
	localDialPreserved := true
	postLocalRecoveryObserved := false
	postLocalRecoveryStart := localEnd
	postLocalRecoveryEnd := localEnd + 15*time.Minute

	maxTrueZoneC := -999.0
	minTrueZoneC := 999.0

	for stepIdx := 0; stepIdx < totalSteps; stepIdx++ {
		elapsed := time.Duration(stepIdx) * spec.Step
		now := startUTC.Add(elapsed)

		cloudConnected := spec.OutageDuration <= 0 || elapsed < spec.OutageStart || elapsed >= outageEnd
		if cloudConnected != prevCloudConnected {
			arm.OnCloudConnectivityChanged(cloudConnected, now)
			prevCloudConnected = cloudConnected
		}

		inLocalManual := spec.LocalOverrideDur > 0 && elapsed >= spec.LocalOverrideStart && elapsed < localEnd
		if inLocalManual && !prevLocalManual {
			if !plant.SetControlCabinetMode(spec.LocalOverrideDevice, ControlCabinetModeLocal) ||
				!plant.SetLocalManualTarget(spec.LocalOverrideDevice, spec.LocalManualTarget) {
				return BenchmarkMetrics{}, fmt.Errorf("failed to inject LOCAL override on %s", spec.LocalOverrideDevice)
			}
			prevLocalManual = true
		} else if !inLocalManual && prevLocalManual {
			if !plant.SetControlCabinetMode(spec.LocalOverrideDevice, ControlCabinetModeRemote) {
				return BenchmarkMetrics{}, fmt.Errorf("failed to restore REMOTE on %s", spec.LocalOverrideDevice)
			}
			prevLocalManual = false
		}

		truthBefore := plant.TruthState()
		obsSnapshot := plant.Snapshot()
		dayIdx := int(elapsed / (24 * time.Hour))
		rateCNY := spec.Tariff.RateAt(now, dayIdx)

		stepCtx := BenchmarkStepContext{
			Now:            now,
			Elapsed:        elapsed,
			Step:           spec.Step,
			CloudConnected: cloudConnected,
			InLocalManual:  inLocalManual,
			Spec:           spec,
			Truth:          truthBefore,
			Observed:       obsSnapshot,
			TariffRateCNY:  rateCNY,
		}
		if err := arm.OnStep(ctx, stepCtx); err != nil {
			return BenchmarkMetrics{}, err
		}

		cycleRes, err := host.RunCycle(ctx, now)
		if err != nil {
			return BenchmarkMetrics{}, err
		}
		arm.OnCycleResult(cycleRes, stepCtx)

		if inLocalManual {
			prefix := spec.LocalOverrideDevice + "/"
			for _, d := range cycleRes.Cycle.Decisions {
				if strings.HasPrefix(d.Address, prefix) && d.Accepted {
					localRemoteWriteAttempts++
					extraInterlockViolations++
				}
			}
		} else if !prevLocalManual && spec.LocalOverrideDur > 0 && elapsed >= postLocalRecoveryStart && elapsed < postLocalRecoveryEnd {
			prefix := spec.LocalOverrideDevice + "/"
			for _, wr := range cycleRes.WriteResults {
				if strings.HasPrefix(wr.Address, prefix) && wr.Success {
					postLocalRecoveryObserved = true
				}
			}
		}

		// Advance physical truth plant one step.
		plant.Tick(spec.Step)
		truthAfter := plant.TruthState()

		stepEnergyKWh := truthAfter.TotalEnergyKWh - prevTotalEnergyKWh
		prevTotalEnergyKWh = truthAfter.TotalEnergyKWh
		totalCostCNY += stepEnergyKWh * rateCNY

		if truthAfter.TrueZoneTempC > maxTrueZoneC {
			maxTrueZoneC = truthAfter.TrueZoneTempC
		}
		if truthAfter.TrueZoneTempC < minTrueZoneC {
			minTrueZoneC = truthAfter.TrueZoneTempC
		}
		if truthAfter.TrueZoneTempC > spec.PlantConfig.ZoneTempMaxC || truthAfter.TrueZoneTempC < spec.PlantConfig.ZoneTempMinC {
			zoneExceedMinutes += spec.Step.Minutes()
		}

		// Observe truth actuator moves & hunting reversals.
		stepReversals := 0
		for i, ch := range truthAfter.Chillers {
			if c, r := chillerRunTrackers[i].observe(ch.RunRequested, now); c {
				actuatorChanges++
				if r {
					actuatorReversals++
					stepReversals++
				}
			}
			if c, r := chillerSPTrackers[i].observe(ch.SetpointC, now); c {
				actuatorChanges++
				if r {
					actuatorReversals++
					stepReversals++
				}
			}
		}
		if c, r := chwpTracker.observe(truthAfter.CHWPFreqSetpointHz, now); c {
			actuatorChanges++
			if r {
				actuatorReversals++
				stepReversals++
			}
		}
		if c, r := cwpTracker.observe(truthAfter.CWPFreqSetpointHz, now); c {
			actuatorChanges++
			if r {
				actuatorReversals++
				stepReversals++
			}
		}
		if c, r := towerTracker.observe(truthAfter.TowerFanSetpointPct, now); c {
			actuatorChanges++
			if r {
				actuatorReversals++
				stepReversals++
			}
		}

		if inLocalManual {
			if math.Abs(truthAfter.CHWPFreqSetpointHz-spec.LocalManualTarget) > 1e-6 {
				localDialPreserved = false
			}
		}
		if !cloudConnected {
			outageTotalSteps++
			anyRunning := false
			for _, ch := range truthAfter.Chillers {
				if ch.Running {
					anyRunning = true
					break
				}
			}
			if anyRunning {
				outageRunningSteps++
			}
			outageReversals += stepReversals
			if truthAfter.FalseTrips > truthBefore.FalseTrips {
				outageFalseTrips += truthAfter.FalseTrips - truthBefore.FalseTrips
			}
			if truthAfter.InterlockViolations > truthBefore.InterlockViolations {
				outageInterlocks += truthAfter.InterlockViolations - truthBefore.InterlockViolations
			}
		}
	}

	finalTruth := plant.TruthState()
	meanCOP := 0.0
	if finalTruth.ChillerEnergyKWh > 0 {
		meanCOP = finalTruth.TotalCoolingEnergyKWh / finalTruth.ChillerEnergyKWh
	}

	outageHandledCleanly := outageFalseTrips == 0 && outageInterlocks == 0 && outageReversals <= 2 && (outageTotalSteps == 0 || outageRunningSteps == outageTotalSteps)
	localOverrideHandledCleanly := localRemoteWriteAttempts == 0 && localDialPreserved && (spec.LocalOverrideDur <= 0 || postLocalRecoveryObserved)

	return BenchmarkMetrics{
		Strategy:                    arm.Name(),
		TotalEnergyKWh:              round(finalTruth.TotalEnergyKWh, 2),
		ChillerKWh:                  round(finalTruth.ChillerEnergyKWh, 2),
		CHWPumpKWh:                  round(finalTruth.CHWPEnergyKWh, 2),
		CWPumpKWh:                   round(finalTruth.CWPEnergyKWh, 2),
		TowerKWh:                    round(finalTruth.TowerEnergyKWh, 2),
		TotalCostCNY:                round(totalCostCNY, 2),
		ZoneExceedMinutes:           round(zoneExceedMinutes, 1),
		ActuatorChanges:             actuatorChanges,
		ActuatorReversals:           actuatorReversals,
		FalseTrips:                  finalTruth.FalseTrips,
		InterlockViolations:         finalTruth.InterlockViolations + extraInterlockViolations,
		OutageHandledCleanly:        outageHandledCleanly,
		LocalOverrideHandledCleanly: localOverrideHandledCleanly,
		ElapsedMs:                   time.Since(wallStart).Milliseconds(),
		MaxTrueZoneTempC:            round(maxTrueZoneC, 2),
		MinTrueZoneTempC:            round(minTrueZoneC, 2),
		MeanTrueCOP:                 round(meanCOP, 3),
	}, nil
}

// FormatBenchmarkMarkdownTable renders benchmark results as a GitHub-flavored Markdown table.
func FormatBenchmarkMarkdownTable(results []BenchmarkMetrics) string {
	var b strings.Builder
	b.WriteString("| Strategy | Total kWh | Chiller kWh | CHWP kWh | CWP kWh | Tower kWh | Cost (CNY) | Zone Exceed (min) | Max Zone (°C) | Mean COP | Act Changes | Reversals | False Trips | Interlock Viol | Outage OK | Local OK | Wall (ms) |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: | :---: | ---: |\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.1f | %.2f | %.3f | %d | %d | %d | %d | %t | %t | %d |\n",
			r.Strategy, r.TotalEnergyKWh, r.ChillerKWh, r.CHWPumpKWh, r.CWPumpKWh, r.TowerKWh,
			r.TotalCostCNY, r.ZoneExceedMinutes, r.MaxTrueZoneTempC, r.MeanTrueCOP,
			r.ActuatorChanges, r.ActuatorReversals, r.FalseTrips, r.InterlockViolations,
			r.OutageHandledCleanly, r.LocalOverrideHandledCleanly, r.ElapsedMs)
	}
	return b.String()
}

// FormatBenchmarkJSON encodes benchmark results as formatted JSON.
func FormatBenchmarkJSON(results []BenchmarkMetrics) (string, error) {
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// --- Arm 1: FixedBaseline ---

// FixedBaselineArm maintains fixed 7.0C CHW setpoint, 50 Hz pumps, and 75% tower fan.
type FixedBaselineArm struct {
	TowerFanPct float64
}

func (a *FixedBaselineArm) Name() string { return "FixedBaseline" }

func (a *FixedBaselineArm) ConfigureHost(host *edgecontrol.Host, _ *CoupledChillerPlant, cfg CoupledChillerPlantConfig, _ time.Time) error {
	fanPct := a.TowerFanPct
	if fanPct <= 0 {
		fanPct = 75.0
	}
	interlock, err := buildCoupledHydraulicInterlock(cfg)
	if err != nil {
		return err
	}
	fixedCtrl := &fixedBaselineEdgeController{
		id:          "fixed-baseline",
		cfg:         cfg,
		setpointC:   7.0,
		pumpFreqHz:  50.0,
		towerFanPct: fanPct,
	}
	return host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 60, Controller: fixedCtrl},
	})
}

func (a *FixedBaselineArm) OnCloudConnectivityChanged(bool, time.Time) {}
func (a *FixedBaselineArm) OnStep(context.Context, BenchmarkStepContext) error {
	return nil
}
func (a *FixedBaselineArm) OnCycleResult(edgecontrol.HostCycleResult, BenchmarkStepContext) {}

type fixedBaselineEdgeController struct {
	id          string
	cfg         CoupledChillerPlantConfig
	setpointC   float64
	pumpFreqHz  float64
	towerFanPct float64
}

func (c *fixedBaselineEdgeController) ID() string { return c.id }

func (c *fixedBaselineEdgeController) Run(_ context.Context, img edgecontrol.ProcessImage, plan *edgecontrol.ControlPlan) error {
	for _, ch := range c.cfg.Chillers {
		running, _ := coupledRunActive(img, ch.ID+"/RunState")
		if !running {
			_, _, _ = plan.Request(ch.ID+"/Start", c.id, edgecontrol.BooleanValue(true))
		}
		_, _, _ = plan.Request(ch.ID+"/SupplySp", c.id, edgecontrol.DoubleValue(c.setpointC))
	}
	_, _, _ = plan.Request(c.cfg.ChilledWaterPump.ID+"/FreqSp", c.id, edgecontrol.DoubleValue(c.pumpFreqHz))
	_, _, _ = plan.Request(c.cfg.CoolingWaterPump.ID+"/FreqSp", c.id, edgecontrol.DoubleValue(c.pumpFreqHz))
	_, _, _ = plan.Request(c.cfg.CoolingTower.ID+"/FanSp", c.id, edgecontrol.DoubleValue(c.towerFanPct))
	return nil
}

// --- Arm 2: LegacyLinearHeuristic ---
// Combines the legacy optimization.HVACRecommendationSolver greedy linear heuristic
// (hourly cloud cycle) with edgecontrol.ChillerPlantClusterController open-loop linear VFD mapping.

type LegacyLinearHeuristicArm struct {
	schedCtrl    *edgecontrol.EnergyScheduleController
	fallbackCtrl *edgecontrol.AutonomousFallbackController
	clusterCtrl  *edgecontrol.ChillerPlantClusterController

	lastCloudEvalAt   time.Time
	lastRollbackAt    time.Time
	currentSetpointC  float64
	lastDayEnergyKWh  float64
	lastDayCostCNY    float64
	trailingCostCNY   float64
	prevEnergyKWh     float64
	windowEnergyStart float64
	windowStartAt     time.Time
}

func (a *LegacyLinearHeuristicArm) Name() string { return "LegacyLinearHeuristic" }

func (a *LegacyLinearHeuristicArm) ConfigureHost(host *edgecontrol.Host, _ *CoupledChillerPlant, cfg CoupledChillerPlantConfig, start time.Time) error {
	a.currentSetpointC = 7.0
	a.lastDayEnergyKWh = 3500.0
	a.lastDayCostCNY = 2800.0
	a.windowStartAt = start

	autonomyPolicy := edgecontrol.DefaultAutonomyPolicyConfig()
	autonomyPolicy.Enabled = true
	autonomyPolicy.OfflineGraceSeconds = 60
	autonomyPolicy.DefaultChilledWaterSupplySetpointC = 7.0
	autonomyPolicy.DefaultPumpFrequencyHz = 42.0
	autonomyPolicy.MinPumpFrequencyHz = 25.0
	autonomyPolicy.MaxPumpFrequencyHz = 50.0
	autonomyPolicy.MinRunSeconds = 600
	autonomyPolicy.MinStopSeconds = 600

	interlock, err := buildCoupledHydraulicInterlockWithPolicy(cfg, autonomyPolicy)
	if err != nil {
		return err
	}

	chillerFallbacks := make([]edgecontrol.ChillerFallbackTarget, len(cfg.Chillers))
	clusterChillers := make([]edgecontrol.ClusterChillerBinding, len(cfg.Chillers))
	for i, ch := range cfg.Chillers {
		chillerFallbacks[i] = edgecontrol.ChillerFallbackTarget{
			ComponentID:        ch.ID,
			SupplySetpointAddr: ch.ID + "/SupplySp",
			RunStateAddr:       ch.ID + "/RunState",
			FaultAddr:          ch.ID + "/Fault",
		}
		// Note: LoadLimitAddr intentionally omitted so open-loop CapacityKW feedback does not latch
		// chiller capacity at night-time low load (documented simulator/cluster caveat).
		clusterChillers[i] = edgecontrol.ClusterChillerBinding{
			ComponentID:         ch.ID,
			RatedCoolingKW:      ch.RatedCoolingCapacityKW,
			StartAddr:           ch.ID + "/Start",
			StopAddr:            ch.ID + "/Stop",
			RunStateAddr:        ch.ID + "/RunState",
			FaultAddr:           ch.ID + "/Fault",
			SetpointAddr:        ch.ID + "/SupplySp",
			CoolingCapacityAddr: ch.ID + "/CapacityKW",
			PowerAddr:           ch.ID + "/PowerKW",
		}
	}
	pumpFallbacks := []edgecontrol.PumpFallbackTarget{
		{ComponentID: cfg.ChilledWaterPump.ID, FrequencyAddr: cfg.ChilledWaterPump.ID + "/FreqSp", RunStateAddr: cfg.ChilledWaterPump.ID + "/RunState", FaultAddr: cfg.ChilledWaterPump.ID + "/Fault"},
		{ComponentID: cfg.CoolingWaterPump.ID, FrequencyAddr: cfg.CoolingWaterPump.ID + "/FreqSp", RunStateAddr: cfg.CoolingWaterPump.ID + "/RunState", FaultAddr: cfg.CoolingWaterPump.ID + "/Fault"},
	}
	clusterPumps := []edgecontrol.ClusterPumpBinding{
		{ComponentID: cfg.ChilledWaterPump.ID, FrequencyAddr: cfg.ChilledWaterPump.ID + "/FreqSp", RunStateAddr: cfg.ChilledWaterPump.ID + "/RunState", FaultAddr: cfg.ChilledWaterPump.ID + "/Fault", FlowAddr: cfg.ChilledWaterPump.ID + "/Flow"},
		{ComponentID: cfg.CoolingWaterPump.ID, FrequencyAddr: cfg.CoolingWaterPump.ID + "/FreqSp", RunStateAddr: cfg.CoolingWaterPump.ID + "/RunState", FaultAddr: cfg.CoolingWaterPump.ID + "/Fault", FlowAddr: cfg.CoolingWaterPump.ID + "/Flow"},
	}
	clusterTowers := []edgecontrol.ClusterTowerBinding{
		{ComponentID: cfg.CoolingTower.ID, FanSpeedAddr: cfg.CoolingTower.ID + "/FanSp", RunStateAddr: cfg.CoolingTower.ID + "/RunState", FaultAddr: cfg.CoolingTower.ID + "/Fault"},
	}

	fallbackCtrl, err := edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{
		Policy:                     autonomyPolicy,
		CloudConnected:             true,
		LastCloudContactAt:         start,
		SetpointRampRateCPerSecond: 0.05,
		FrequencyRampRateHzPerSec:  0.5,
		Chillers:                   chillerFallbacks,
		Pumps:                      pumpFallbacks,
	})
	if err != nil {
		return err
	}

	schedCtrl := edgecontrol.NewEnergyScheduleController("")
	schedCtrl.UpdateTargets(chillerFallbacks, pumpFallbacks, clusterTowers)
	fallbackCtrl.SetScheduleActiveFn(schedCtrl.IsActiveAt)

	clusterPolicy := edgecontrol.DefaultClusterControlPolicy()
	clusterPolicy.Enabled = true
	clusterPolicy.MinRunningChillers = 1
	clusterPolicy.MaxRunningChillers = len(cfg.Chillers)
	clusterPolicy.StageUpLoadPct = 85
	clusterPolicy.StageDownLoadPct = 40
	clusterPolicy.StageDwellSeconds = 300
	clusterPolicy.DefaultSupplySetpointC = 7.0
	clusterPolicy.MinPumpFrequencyHz = 25.0
	clusterPolicy.MaxPumpFrequencyHz = 50.0
	clusterPolicy.MinTowerFanSpeedPct = 30.0
	clusterPolicy.MaxTowerFanSpeedPct = 100.0

	clusterCtrl, err := edgecontrol.NewChillerPlantClusterController(edgecontrol.ClusterControllerConfig{
		Policy:        clusterPolicy,
		Chillers:      clusterChillers,
		Pumps:         clusterPumps,
		Towers:        clusterTowers,
		PlantLoadAddr: cfg.BTUMeterID + "/CoolingCapacityKW",
	})
	if err != nil {
		return err
	}
	clusterCtrl.SetScheduleOverrideFn(schedCtrl.ActiveOverrideAt)

	a.schedCtrl = schedCtrl
	a.fallbackCtrl = fallbackCtrl
	a.clusterCtrl = clusterCtrl

	if err := a.installCloudSchedule(start, 7.0); err != nil {
		return err
	}

	return host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallbackCtrl},
		{Priority: 50, Controller: schedCtrl},
		{Priority: 60, Controller: clusterCtrl},
	})
}

func (a *LegacyLinearHeuristicArm) OnCloudConnectivityChanged(connected bool, at time.Time) {
	if a.schedCtrl != nil {
		a.schedCtrl.SetCloudConnected(connected, at)
	}
	if a.fallbackCtrl != nil {
		a.fallbackCtrl.SetCloudConnected(connected, at)
	}
}

func (a *LegacyLinearHeuristicArm) OnStep(_ context.Context, step BenchmarkStepContext) error {
	// Accumulate observed meter energy/cost strictly from meter telemetry (never truth state).
	obsEnergy := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.PowerMeterID, "energyKwh", a.prevEnergyKWh)
	deltaE := obsEnergy - a.prevEnergyKWh
	if deltaE > 0 {
		a.trailingCostCNY += deltaE * step.TariffRateCNY
	}
	a.prevEnergyKWh = obsEnergy
	if !a.windowStartAt.IsZero() && step.Now.Sub(a.windowStartAt) >= 24*time.Hour {
		a.lastDayEnergyKWh = math.Max(100, obsEnergy-a.windowEnergyStart)
		a.lastDayCostCNY = math.Max(100, a.trailingCostCNY)
		a.windowEnergyStart = obsEnergy
		a.trailingCostCNY = 0
		a.windowStartAt = step.Now
	}

	if !step.CloudConnected {
		return nil
	}

	obsZoneC := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.ZoneSensorID, "zoneTemperatureC", 24.0)
	obsSupplyC := readObservedTelemetry(step.Observed, step.Spec.PlantConfig.BTUMeterID, "supplyWaterTemperatureC", a.currentSetpointC)
	zoneMaxC := step.Spec.PlantConfig.ZoneTempMaxC

	// AutoController safety monitor trip/rollback if observed zone breaches zoneMaxC.
	if obsZoneC > zoneMaxC && a.currentSetpointC > 7.0 {
		a.currentSetpointC = 7.0
		a.lastRollbackAt = step.Now
		a.lastCloudEvalAt = step.Now
		return a.installCloudSchedule(step.Now, a.currentSetpointC)
	}

	// Hourly optimization cadence matching DefaultRecommendationTTL (65m) and 30m post-rollback hold.
	if !a.lastCloudEvalAt.IsZero() && step.Now.Sub(a.lastCloudEvalAt) < time.Hour {
		return nil
	}
	if !a.lastRollbackAt.IsZero() && step.Now.Sub(a.lastRollbackAt) < 30*time.Minute {
		return nil
	}
	a.lastCloudEvalAt = step.Now

	nextSP := legacyHVACRecommendationStep(
		clamp(obsSupplyC, 6.0, 11.0),
		obsZoneC,
		a.lastDayEnergyKWh,
		a.lastDayCostCNY,
		6.0, 11.0,
		step.Spec.PlantConfig.ZoneTempMinC, zoneMaxC,
		1.0,    // MaxSupplyTempStep
		-180.0, // legacy policy DailyEnergyDeltaPerSupplyTempC
		0.4,    // legacy policy ZoneTempDeltaPerSupplyTempC
		0.2,    // legacy policy ZoneTempUncertaintyP90C
	)
	a.currentSetpointC = nextSP
	return a.installCloudSchedule(step.Now, nextSP)
}

func (a *LegacyLinearHeuristicArm) OnCycleResult(edgecontrol.HostCycleResult, BenchmarkStepContext) {}

func (a *LegacyLinearHeuristicArm) installCloudSchedule(at time.Time, supplySP float64) error {
	sp := supplySP
	return a.schedCtrl.UpdateSchedule(&edgecontrol.EdgeEnergySchedule{
		ScheduleID:                "legacy-cloud-opt",
		Version:                   at.Unix(),
		IssuedAt:                  at,
		ValidFrom:                 at.Add(-time.Minute),
		ValidUntil:                at.Add(24 * time.Hour),
		LeaseExpiresAt:            at.Add(65 * time.Minute),
		ContinueOfflineWhileValid: true,
		Slots: []edgecontrol.EnergyScheduleSlot{
			{
				SlotID:              "all-day",
				StartMinute:         0,
				EndMinute:           1440,
				Mode:                edgecontrol.ScheduleModeNormal,
				SupplyTempSetpointC: &sp,
			},
		},
	})
}

// legacyHVACRecommendationStep reproduces services/optimization-service/internal/optimization/service.go
// HVACRecommendationSolver.Recommend step selection verbatim.
func legacyHVACRecommendationStep(
	supplyC, zoneC, dailyEnergyKWh, dailyCostCNY float64,
	supplyMinC, supplyMaxC, zoneMinC, zoneMaxC, maxStepC float64,
	dailyEnergyDeltaPerC, zoneTempDeltaPerC, zoneTempP90C float64,
) float64 {
	costPerKWh := dailyCostCNY / math.Max(dailyEnergyKWh, 1)
	steps := []float64{-maxStepC, 0, maxStepC}
	bestSP := supplyC
	bestCost := math.Inf(1)
	found := false
	for _, delta := range steps {
		sp := supplyC + delta
		if sp < supplyMinC || sp > supplyMaxC {
			continue
		}
		projZone := zoneC + zoneTempDeltaPerC*delta
		if projZone-zoneTempP90C < zoneMinC || projZone+zoneTempP90C > zoneMaxC {
			continue
		}
		energy := math.Max(0, dailyEnergyKWh+dailyEnergyDeltaPerC*delta)
		cost := energy * costPerKWh
		if !found || cost < bestCost {
			bestCost = cost
			bestSP = sp
			found = true
		}
	}
	if !found {
		return clamp(supplyC-maxStepC, supplyMinC, supplyMaxC)
	}
	return bestSP
}

func buildCoupledHydraulicInterlock(cfg CoupledChillerPlantConfig) (*edgecontrol.HydraulicInterlockController, error) {
	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	policy.Enabled = true
	policy.MinPumpFrequencyHz = 20
	policy.MaxPumpFrequencyHz = 50
	return buildCoupledHydraulicInterlockWithPolicy(cfg, policy)
}

func buildCoupledHydraulicInterlockWithPolicy(cfg CoupledChillerPlantConfig, policy edgecontrol.AutonomyPolicyConfig) (*edgecontrol.HydraulicInterlockController, error) {
	chillers := make([]edgecontrol.ChillerInterlockBinding, len(cfg.Chillers))
	for i, ch := range cfg.Chillers {
		chillers[i] = edgecontrol.ChillerInterlockBinding{
			ComponentID:      ch.ID,
			StartCommand:     ch.ID + "/Start",
			StopCommand:      ch.ID + "/Stop",
			SetpointCommand:  ch.ID + "/SupplySp",
			LoadLimitCommand: ch.ID + "/LoadLimit",
			RunState:         ch.ID + "/RunState",
			FaultChannel:     ch.ID + "/Fault",
			LocalRemoteMode:  ch.ID + "/LocalRemoteMode",
		}
	}
	pumps := []edgecontrol.PumpInterlockBinding{
		{
			ComponentID:      cfg.ChilledWaterPump.ID,
			StartCommand:     cfg.ChilledWaterPump.ID + "/Start",
			StopCommand:      cfg.ChilledWaterPump.ID + "/Stop",
			RunState:         cfg.ChilledWaterPump.ID + "/RunState",
			FlowFeedback:     cfg.ChilledWaterPump.ID + "/Flow",
			MinFlowRate:      cfg.ChilledWaterPump.RatedFlowM3H * 0.35,
			FaultChannel:     cfg.ChilledWaterPump.ID + "/Fault",
			FrequencyCommand: cfg.ChilledWaterPump.ID + "/FreqSp",
			LocalRemoteMode:  cfg.ChilledWaterPump.ID + "/LocalRemoteMode",
		},
	}
	return edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
		Policy:             policy,
		Chillers:           chillers,
		ChilledWaterPumps:  pumps,
		PumpFrequencyAddrs: []string{cfg.CoolingWaterPump.ID + "/FreqSp"},
	})
}

func coupledRunActive(img edgecontrol.ProcessImage, addr string) (bool, bool) {
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
		return false, false
	}
	if snap.Sample.Value.Type == edgecontrol.DataTypeString {
		return strings.EqualFold(strings.TrimSpace(snap.Sample.Value.String), "RUNNING"), true
	}
	return false, false
}

func readObservedTelemetry(snap Snapshot, deviceID, key string, fallback float64) float64 {
	if dev, ok := snap.Devices[deviceID]; ok {
		if val, exists := dev[key]; exists {
			if f, isF := val.(float64); isF {
				return f
			}
		}
	}
	return fallback
}
