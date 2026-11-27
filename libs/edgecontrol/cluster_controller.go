package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultClusterControllerID is the canonical component ID for ChillerPlantClusterController.
	DefaultClusterControllerID = "chiller-plant-cluster"
	// ClusterPumpLoopCHW marks a variable-speed pump serving the chilled-water loop.
	ClusterPumpLoopCHW = "CHW"
	// ClusterPumpLoopCW marks a variable-speed pump serving the condenser-water loop.
	ClusterPumpLoopCW = "CW"

	defaultChillerRatedKW          = 100.0
	minChillerLoadLimitPct         = 30.0
	defaultTargetCHWDeltaTC        = 4.5
	defaultTargetCWDeltaTC         = 4.2
	defaultTargetTowerApproachC    = 3.2
	defaultMinCondenserWaterTempC  = 18.0
	defaultMaxCondenserWaterTempC  = 33.0
	defaultTrimRespondIntervalSecs = 180
	defaultMaxPumpSlewHzPerMin     = 3.0
	defaultMaxTowerSlewPctPerMin   = 8.0
	defaultMinSupplySetpointC      = 6.5
	defaultMaxSupplySetpointC      = 10.0
	defaultTargetZoneTempMaxC      = 24.7
)

// ClusterChillerBinding maps one chiller unit into plant-level staging and load allocation.
type ClusterChillerBinding struct {
	ComponentID         string
	RatedCoolingKW      float64
	StartAddr           string
	StopAddr            string
	RunStateAddr        string
	FaultAddr           string
	SetpointAddr        string
	LoadLimitAddr       string
	CoolingCapacityAddr string
	PowerAddr           string
	LeavingCHWTempAddr  string
	EnteringCHWTempAddr string
	LeavingCWTempAddr   string
	EnteringCWTempAddr  string
	LocalRemoteModeAddr string
}

// ClusterPumpBinding maps one chilled/condenser variable-speed pump into cluster coordination.
type ClusterPumpBinding struct {
	ComponentID         string
	Loop                string
	FrequencyAddr       string
	ActualFrequencyAddr string
	RunStateAddr        string
	FaultAddr           string
	FlowAddr            string
	LocalRemoteModeAddr string
}

// ClusterTowerBinding maps one cooling tower fan into cluster coordination.
type ClusterTowerBinding struct {
	ComponentID           string
	FanSpeedAddr          string
	ActualFanSpeedAddr    string
	RunStateAddr          string
	FaultAddr             string
	LeavingWaterTempAddr  string
	EnteringWaterTempAddr string
	LocalRemoteModeAddr   string
}

// ClusterSensorBindings names plant-level closed-loop temperature, differential pressure,
// outdoor weather, and zone comfort channels consumed by ASHRAE Guideline 36 Trim & Respond.
type ClusterSensorBindings struct {
	CHWSupplyTempAddr   string
	CHWReturnTempAddr   string
	CHWDeltaTAddr       string
	CHWDeltaPAddr       string
	CWLeavingTempAddr   string
	CWEnteringTempAddr  string
	WetBulbTempAddr     string
	OutdoorTempAddr     string
	OutdoorHumidityAddr string
	ZoneTempAddr        string
}

// ClusterControlPolicy parameterizes multi-chiller staging hysteresis, plant bounds,
// and optional ASHRAE Guideline 36 closed-loop Trim & Respond tuning (zero = safe default).
type ClusterControlPolicy struct {
	Enabled                    bool    `json:"enabled"`
	MinRunningChillers         int     `json:"minRunningChillers"`
	MaxRunningChillers         int     `json:"maxRunningChillers"`
	StageUpLoadPct             float64 `json:"stageUpLoadPct"`
	StageDownLoadPct           float64 `json:"stageDownLoadPct"`
	StageDwellSeconds          int     `json:"stageDwellSeconds"`
	DefaultSupplySetpointC     float64 `json:"defaultSupplySetpointC"`
	MinPumpFrequencyHz         float64 `json:"minPumpFrequencyHz"`
	MaxPumpFrequencyHz         float64 `json:"maxPumpFrequencyHz"`
	MinTowerFanSpeedPct        float64 `json:"minTowerFanSpeedPct"`
	MaxTowerFanSpeedPct        float64 `json:"maxTowerFanSpeedPct"`
	TargetChwDeltaTC           float64 `json:"targetChwDeltaTC,omitempty"`
	TargetChwDeltaPKPa         float64 `json:"targetChwDeltaPKPa,omitempty"`
	TargetCwDeltaTC            float64 `json:"targetCwDeltaTC,omitempty"`
	TargetTowerApproachC       float64 `json:"targetTowerApproachC,omitempty"`
	MinCondenserWaterTempC     float64 `json:"minCondenserWaterTempC,omitempty"`
	MaxCondenserWaterTempC     float64 `json:"maxCondenserWaterTempC,omitempty"`
	TrimRespondIntervalSeconds int     `json:"trimRespondIntervalSeconds,omitempty"`
	MaxPumpSlewHzPerMin        float64 `json:"maxPumpSlewHzPerMin,omitempty"`
	MaxTowerSlewPctPerMin      float64 `json:"maxTowerSlewPctPerMin,omitempty"`
	EnableSupplyTempReset      bool    `json:"enableSupplyTempReset,omitempty"`
	MinSupplySetpointC         float64 `json:"minSupplySetpointC,omitempty"`
	MaxSupplySetpointC         float64 `json:"maxSupplySetpointC,omitempty"`
	TargetZoneTempMaxC         float64 `json:"targetZoneTempMaxC,omitempty"`
}

// DefaultClusterControlPolicy returns safe multi-chiller cluster controller defaults.
func DefaultClusterControlPolicy() ClusterControlPolicy {
	return ClusterControlPolicy{
		Enabled:                false,
		MinRunningChillers:     1,
		MaxRunningChillers:     4,
		StageUpLoadPct:         85,
		StageDownLoadPct:       40,
		StageDwellSeconds:      300,
		DefaultSupplySetpointC: 7.0,
		MinPumpFrequencyHz:     28.0,
		MaxPumpFrequencyHz:     50.0,
		MinTowerFanSpeedPct:    30.0,
		MaxTowerFanSpeedPct:    100.0,
	}
}

// Validate checks all staging, dwell, and equipment limit invariants.
func (p ClusterControlPolicy) Validate() error {
	if p.MinRunningChillers < 0 || p.MinRunningChillers > 16 {
		return fmt.Errorf("minRunningChillers %d must be in [0,16]", p.MinRunningChillers)
	}
	if p.MaxRunningChillers < 1 || p.MaxRunningChillers > 16 {
		return fmt.Errorf("maxRunningChillers %d must be in [1,16]", p.MaxRunningChillers)
	}
	if p.MinRunningChillers > p.MaxRunningChillers {
		return fmt.Errorf("minRunningChillers (%d) must be <= maxRunningChillers (%d)", p.MinRunningChillers, p.MaxRunningChillers)
	}
	if !isFiniteFloat(p.StageUpLoadPct) || p.StageUpLoadPct <= 50 || p.StageUpLoadPct > 100 {
		return fmt.Errorf("stageUpLoadPct %v must be finite and in (50,100]", p.StageUpLoadPct)
	}
	if !isFiniteFloat(p.StageDownLoadPct) || p.StageDownLoadPct < 5 || p.StageDownLoadPct >= 80 {
		return fmt.Errorf("stageDownLoadPct %v must be finite and in [5,80)", p.StageDownLoadPct)
	}
	if p.StageDownLoadPct >= p.StageUpLoadPct-10 {
		return fmt.Errorf("stageDownLoadPct (%v) must leave >= 10%% hysteresis below stageUpLoadPct (%v)", p.StageDownLoadPct, p.StageUpLoadPct)
	}
	if p.StageDwellSeconds < 0 || p.StageDwellSeconds > 3600 {
		return fmt.Errorf("stageDwellSeconds %d must be in [0,3600]", p.StageDwellSeconds)
	}
	if !isFiniteFloat(p.DefaultSupplySetpointC) || p.DefaultSupplySetpointC < 4 || p.DefaultSupplySetpointC > 18 {
		return fmt.Errorf("defaultSupplySetpointC %v must be finite and in [4,18]", p.DefaultSupplySetpointC)
	}
	if !isFiniteFloat(p.MinPumpFrequencyHz) || p.MinPumpFrequencyHz < 15 || p.MinPumpFrequencyHz > 50 {
		return fmt.Errorf("minPumpFrequencyHz %v must be in [15,50]", p.MinPumpFrequencyHz)
	}
	if !isFiniteFloat(p.MaxPumpFrequencyHz) || p.MaxPumpFrequencyHz < 15 || p.MaxPumpFrequencyHz > 50 || p.MinPumpFrequencyHz > p.MaxPumpFrequencyHz {
		return errors.New("pump frequency bounds must be finite in [15,50] with min <= max")
	}
	if !isFiniteFloat(p.MinTowerFanSpeedPct) || p.MinTowerFanSpeedPct < 15 || p.MinTowerFanSpeedPct > 100 {
		return fmt.Errorf("minTowerFanSpeedPct %v must be in [15,100]", p.MinTowerFanSpeedPct)
	}
	if !isFiniteFloat(p.MaxTowerFanSpeedPct) || p.MaxTowerFanSpeedPct < 15 || p.MaxTowerFanSpeedPct > 100 || p.MinTowerFanSpeedPct > p.MaxTowerFanSpeedPct {
		return errors.New("tower fan speed bounds must be finite in [15,100] with min <= max")
	}
	if err := validateOptionalFiniteRange(p.TargetChwDeltaTC, 1.5, 12.0, "targetChwDeltaTC"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.TargetChwDeltaPKPa, 10.0, 1000.0, "targetChwDeltaPKPa"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.TargetCwDeltaTC, 1.5, 12.0, "targetCwDeltaTC"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.TargetTowerApproachC, 1.0, 12.0, "targetTowerApproachC"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.MinCondenserWaterTempC, 10.0, 30.0, "minCondenserWaterTempC"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.MaxCondenserWaterTempC, 20.0, 45.0, "maxCondenserWaterTempC"); err != nil {
		return err
	}
	if p.MinCondenserWaterTempC != 0 && p.MaxCondenserWaterTempC != 0 && p.MinCondenserWaterTempC > p.MaxCondenserWaterTempC {
		return errors.New("minCondenserWaterTempC must be <= maxCondenserWaterTempC")
	}
	if p.TrimRespondIntervalSeconds != 0 && (p.TrimRespondIntervalSeconds < 10 || p.TrimRespondIntervalSeconds > 900) {
		return fmt.Errorf("trimRespondIntervalSeconds %d must be 0 or in [10,900]", p.TrimRespondIntervalSeconds)
	}
	if err := validateOptionalFiniteRange(p.MaxPumpSlewHzPerMin, 0.2, 50.0, "maxPumpSlewHzPerMin"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.MaxTowerSlewPctPerMin, 0.5, 100.0, "maxTowerSlewPctPerMin"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.MinSupplySetpointC, 4.0, 18.0, "minSupplySetpointC"); err != nil {
		return err
	}
	if err := validateOptionalFiniteRange(p.MaxSupplySetpointC, 4.0, 18.0, "maxSupplySetpointC"); err != nil {
		return err
	}
	if p.MinSupplySetpointC != 0 && p.MaxSupplySetpointC != 0 && p.MinSupplySetpointC > p.MaxSupplySetpointC {
		return errors.New("minSupplySetpointC must be <= maxSupplySetpointC")
	}
	if err := validateOptionalFiniteRange(p.TargetZoneTempMaxC, 18.0, 30.0, "targetZoneTempMaxC"); err != nil {
		return err
	}
	return nil
}

func validateOptionalFiniteRange(v, minVal, maxVal float64, name string) error {
	if v == 0 {
		return nil
	}
	if !isFiniteFloat(v) || v < minVal || v > maxVal {
		return fmt.Errorf("%s %v must be 0 or finite in [%v,%v]", name, v, minVal, maxVal)
	}
	return nil
}

func (p ClusterControlPolicy) effectiveTargetChwDeltaTC() float64 {
	if p.TargetChwDeltaTC > 0 {
		return p.TargetChwDeltaTC
	}
	return defaultTargetCHWDeltaTC
}

func (p ClusterControlPolicy) effectiveTargetCwDeltaTC() float64 {
	if p.TargetCwDeltaTC > 0 {
		return p.TargetCwDeltaTC
	}
	return defaultTargetCWDeltaTC
}

func (p ClusterControlPolicy) effectiveTargetTowerApproachC() float64 {
	if p.TargetTowerApproachC > 0 {
		return p.TargetTowerApproachC
	}
	return defaultTargetTowerApproachC
}

func (p ClusterControlPolicy) effectiveCondenserTempBounds() (float64, float64) {
	lo := defaultMinCondenserWaterTempC
	if p.MinCondenserWaterTempC > 0 {
		lo = p.MinCondenserWaterTempC
	}
	hi := defaultMaxCondenserWaterTempC
	if p.MaxCondenserWaterTempC > 0 {
		hi = p.MaxCondenserWaterTempC
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

func (p ClusterControlPolicy) effectiveTrimRespondInterval() time.Duration {
	if p.TrimRespondIntervalSeconds > 0 {
		return time.Duration(p.TrimRespondIntervalSeconds) * time.Second
	}
	return defaultTrimRespondIntervalSecs * time.Second
}

func (p ClusterControlPolicy) effectivePumpSlewHzPerMin() float64 {
	if p.MaxPumpSlewHzPerMin > 0 {
		return p.MaxPumpSlewHzPerMin
	}
	return defaultMaxPumpSlewHzPerMin
}

func (p ClusterControlPolicy) effectiveTowerSlewPctPerMin() float64 {
	if p.MaxTowerSlewPctPerMin > 0 {
		return p.MaxTowerSlewPctPerMin
	}
	return defaultMaxTowerSlewPctPerMin
}

func (p ClusterControlPolicy) effectiveSupplySetpointBounds() (float64, float64) {
	lo := defaultMinSupplySetpointC
	if p.MinSupplySetpointC > 0 {
		lo = p.MinSupplySetpointC
	}
	hi := defaultMaxSupplySetpointC
	if p.MaxSupplySetpointC > 0 {
		hi = p.MaxSupplySetpointC
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

func (p ClusterControlPolicy) effectiveTargetZoneMaxC() float64 {
	if p.TargetZoneTempMaxC > 0 {
		return p.TargetZoneTempMaxC
	}
	return defaultTargetZoneTempMaxC
}

// Clone returns a copy of ClusterControlPolicy (nil-safe).
func (p *ClusterControlPolicy) Clone() *ClusterControlPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// ClusterScheduleOverride injects active time-slot schedule mode and bounds into cluster coordination.
type ClusterScheduleOverride struct {
	Mode                ScheduleOperatingMode
	SupplyTempSetpointC *float64
	MaxRunningChillers  *int
	TargetCoolingLoadKW *float64
	LoadLimitPct        *float64
}

// ClusterControllerConfig configures ChillerPlantClusterController.
type ClusterControllerConfig struct {
	ID            string
	Policy        ClusterControlPolicy
	Chillers      []ClusterChillerBinding
	Pumps         []ClusterPumpBinding
	Towers        []ClusterTowerBinding
	PlantLoadAddr string
	Sensors       ClusterSensorBindings
}

// ClusterControllerStatus exposes observable staging, load balance, and closed-loop state.
type ClusterControllerStatus struct {
	Active                  bool
	Mode                    ScheduleOperatingMode
	DesiredChillers         int
	RunningChillers         int
	TotalCoolingLoadKW      float64
	LoadRatioPct            float64
	CHWPTargetHz            float64
	CWPTargetHz             float64
	TowerTargetPct          float64
	SupplySetpointC         float64
	ObservedCHWDeltaTC      float64
	ObservedCWDeltaTC       float64
	ObservedTowerApproachC  float64
	RuntimeSecondsByChiller map[string]float64
}

type trimRespondLoopState struct {
	initialized          bool
	hadClosedLoopSensors bool
	lastEvalAt           time.Time
	value                float64
}

// ChillerPlantClusterController coordinates staging order, wear-leveling, cooling
// load sharing, ASHRAE Guideline 36 Trim & Respond pump/tower modulation, and chiller setpoints.
type ChillerPlantClusterController struct {
	mu                 sync.Mutex
	id                 string
	policy             ClusterControlPolicy
	chillers           []ClusterChillerBinding
	pumps              []ClusterPumpBinding
	towers             []ClusterTowerBinding
	plantLoadAddr      string
	sensors            ClusterSensorBindings
	scheduleOverrideFn func(time.Time) *ClusterScheduleOverride
	lastRunAt          time.Time
	lastStageAt        time.Time
	runtimeSec         map[string]float64
	chwpLoop           trimRespondLoopState
	cwpLoop            trimRespondLoopState
	towerLoop          trimRespondLoopState
	chwResetLoop       trimRespondLoopState
	status             ClusterControllerStatus
}

// NewChillerPlantClusterController constructs a validated multi-chiller cluster controller.
func NewChillerPlantClusterController(cfg ClusterControllerConfig) (*ChillerPlantClusterController, error) {
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		id = DefaultClusterControllerID
	}
	return &ChillerPlantClusterController{
		id:            id,
		policy:        cfg.Policy,
		chillers:      slices.Clone(cfg.Chillers),
		pumps:         slices.Clone(cfg.Pumps),
		towers:        slices.Clone(cfg.Towers),
		plantLoadAddr: strings.TrimSpace(cfg.PlantLoadAddr),
		sensors:       trimSensorBindings(cfg.Sensors),
		runtimeSec:    make(map[string]float64),
		status:        ClusterControllerStatus{RuntimeSecondsByChiller: map[string]float64{}},
	}, nil
}

func trimSensorBindings(in ClusterSensorBindings) ClusterSensorBindings {
	return ClusterSensorBindings{
		CHWSupplyTempAddr:   strings.TrimSpace(in.CHWSupplyTempAddr),
		CHWReturnTempAddr:   strings.TrimSpace(in.CHWReturnTempAddr),
		CHWDeltaTAddr:       strings.TrimSpace(in.CHWDeltaTAddr),
		CHWDeltaPAddr:       strings.TrimSpace(in.CHWDeltaPAddr),
		CWLeavingTempAddr:   strings.TrimSpace(in.CWLeavingTempAddr),
		CWEnteringTempAddr:  strings.TrimSpace(in.CWEnteringTempAddr),
		WetBulbTempAddr:     strings.TrimSpace(in.WetBulbTempAddr),
		OutdoorTempAddr:     strings.TrimSpace(in.OutdoorTempAddr),
		OutdoorHumidityAddr: strings.TrimSpace(in.OutdoorHumidityAddr),
		ZoneTempAddr:        strings.TrimSpace(in.ZoneTempAddr),
	}
}

// ID returns the controller identifier.
func (c *ChillerPlantClusterController) ID() string {
	return c.id
}

// UpdatePolicy hot-reloads cluster control parameters.
func (c *ChillerPlantClusterController) UpdatePolicy(policy ClusterControlPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	c.policy = policy
	if !policy.Enabled {
		c.status.Active = false
	}
	c.mu.Unlock()
	return nil
}

// UpdateBindings hot-reloads plant equipment channel bindings from the runtime manifest.
func (c *ChillerPlantClusterController) UpdateBindings(
	chillers []ClusterChillerBinding,
	pumps []ClusterPumpBinding,
	towers []ClusterTowerBinding,
	plantLoadAddr string,
) {
	c.mu.Lock()
	c.chillers = slices.Clone(chillers)
	c.pumps = slices.Clone(pumps)
	c.towers = slices.Clone(towers)
	c.plantLoadAddr = strings.TrimSpace(plantLoadAddr)
	c.mu.Unlock()
}

// UpdateSensorBindings hot-reloads plant-level closed-loop sensor addresses.
func (c *ChillerPlantClusterController) UpdateSensorBindings(sensors ClusterSensorBindings) {
	c.mu.Lock()
	c.sensors = trimSensorBindings(sensors)
	c.mu.Unlock()
}

// SetScheduleOverrideFn wires optional schedule overrides evaluated on each control cycle.
func (c *ChillerPlantClusterController) SetScheduleOverrideFn(fn func(time.Time) *ClusterScheduleOverride) {
	c.mu.Lock()
	c.scheduleOverrideFn = fn
	c.mu.Unlock()
}

// Status returns a snapshot of the cluster controller's most recent evaluation.
func (c *ChillerPlantClusterController) Status() ClusterControllerStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.status
	cp.RuntimeSecondsByChiller = make(map[string]float64, len(c.runtimeSec))
	for k, v := range c.runtimeSec {
		cp.RuntimeSecondsByChiller[k] = v
	}
	return cp
}

// Run evaluates plant load, stages chillers with wear-leveling and dwell anti-hunting,
// enforces non-starving load limits, and executes ASHRAE Guideline 36 Trim & Respond
// closed-loop modulation across chilled-water pumps, condenser pumps, and cooling towers.
func (c *ChillerPlantClusterController) Run(_ context.Context, img ProcessImage, plan *ControlPlan) error {
	if plan == nil {
		return errors.New("cluster controller requires ControlPlan")
	}
	now := img.At().UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dt := time.Duration(0)
	if !c.lastRunAt.IsZero() && now.After(c.lastRunAt) {
		dt = now.Sub(c.lastRunAt)
	}
	c.lastRunAt = now

	if !c.policy.Enabled {
		c.status.Active = false
		return nil
	}

	var runningHealthy, standbyHealthy []ClusterChillerBinding
	totalRunning := 0
	runningRatedKW := 0.0
	measuredChillerLoadKW := 0.0

	for _, ch := range c.chillers {
		running, _ := runChannelActive(img, ch.RunStateAddr)
		faulted := faultChannelActive(img, ch.FaultAddr)
		if running && dt > 0 {
			c.runtimeSec[ch.ComponentID] += dt.Seconds()
		}
		if running {
			totalRunning++
		}
		if capKW, ok := readGoodNumeric(img, ch.CoolingCapacityAddr); ok && capKW > 0 {
			measuredChillerLoadKW += capKW
		}
		if faulted {
			continue
		}
		if running {
			runningHealthy = append(runningHealthy, ch)
			runningRatedKW += effectiveRatedKW(ch.RatedCoolingKW)
		} else {
			standbyHealthy = append(standbyHealthy, ch)
		}
	}

	mode := ScheduleModeNormal
	targetSetpoint := c.policy.DefaultSupplySetpointC
	schedulePinsSetpoint := false
	effectiveMin := c.policy.MinRunningChillers
	effectiveMax := c.policy.MaxRunningChillers

	var override *ClusterScheduleOverride
	if c.scheduleOverrideFn != nil {
		override = c.scheduleOverrideFn(now)
	}
	if override != nil {
		if override.Mode != "" {
			mode = override.Mode
		}
		if override.SupplyTempSetpointC != nil {
			targetSetpoint = *override.SupplyTempSetpointC
			schedulePinsSetpoint = true
		}
		if override.MaxRunningChillers != nil {
			effectiveMax = *override.MaxRunningChillers
		}
	}
	if mode == ScheduleModeStandby {
		effectiveMin = 0
		effectiveMax = 0
	}
	if available := totalRunning + len(standbyHealthy); effectiveMax > available {
		effectiveMax = available
	}
	if effectiveMin > effectiveMax {
		effectiveMin = effectiveMax
	}

	totalLoadKW, hasLoad := readGoodNumeric(img, c.plantLoadAddr)
	if !hasLoad {
		totalLoadKW = measuredChillerLoadKW
	}
	if totalLoadKW <= 0 && override != nil && override.TargetCoolingLoadKW != nil {
		totalLoadKW = *override.TargetCoolingLoadKW
	}
	if totalLoadKW < 0 {
		totalLoadKW = 0
	}

	loadRatioPct := 0.0
	if runningRatedKW > 0 {
		loadRatioPct = (totalLoadKW / runningRatedKW) * 100.0
	}

	dwell := time.Duration(c.policy.StageDwellSeconds) * time.Second
	dwellReady := c.lastStageAt.IsZero() || now.Sub(c.lastStageAt) >= dwell

	desiredChillers := totalRunning
	var stoppingID string
	switch {
	case totalRunning < effectiveMin && len(standbyHealthy) > 0:
		chosen := pickLowestRuntimeChiller(standbyHealthy, c.runtimeSec)
		if reqBoolWritable(img, plan, chosen.StartAddr, c.id, true) {
			c.lastStageAt = now
			desiredChillers = totalRunning + 1
		}
	case totalRunning > effectiveMax && len(runningHealthy) > 0:
		chosen := pickHighestRuntimeChiller(runningHealthy, c.runtimeSec)
		if reqBoolWritable(img, plan, chosen.StopAddr, c.id, true) {
			stoppingID = chosen.ComponentID
			c.lastStageAt = now
			desiredChillers = totalRunning - 1
		}
	case dwellReady && runningRatedKW > 0 && loadRatioPct >= c.policy.StageUpLoadPct && totalRunning < effectiveMax && len(standbyHealthy) > 0:
		chosen := pickLowestRuntimeChiller(standbyHealthy, c.runtimeSec)
		if reqBoolWritable(img, plan, chosen.StartAddr, c.id, true) {
			c.lastStageAt = now
			desiredChillers = totalRunning + 1
		}
	case dwellReady && runningRatedKW > 0 && loadRatioPct <= c.policy.StageDownLoadPct && len(runningHealthy) > effectiveMin && len(runningHealthy) > 0:
		chosen := pickHighestRuntimeChiller(runningHealthy, c.runtimeSec)
		if reqBoolWritable(img, plan, chosen.StopAddr, c.id, true) {
			stoppingID = chosen.ComponentID
			c.lastStageAt = now
			desiredChillers = totalRunning - 1
		}
	}

	chwDeltaT, hasCHWDeltaT := c.observeCHWDeltaT(img, runningHealthy)
	chwDeltaP, hasCHWDeltaP := readGoodNumeric(img, c.sensors.CHWDeltaPAddr)
	chwSupplyTemp, hasCHWSupplyTemp := c.observeCHWSupplyTemp(img, runningHealthy)
	cwDeltaT, hasCWDeltaT := c.observeCWDeltaT(img, runningHealthy)
	cwLeaving, hasCWLeaving := c.observeCWLeavingTemp(img, runningHealthy)
	wetBulb, hasWetBulb := c.observeWetBulbTemp(img)
	dryBulb, hasDryBulb := readGoodNumeric(img, c.sensors.OutdoorTempAddr)
	zoneTemp, hasZoneTemp := readGoodNumeric(img, c.sensors.ZoneTempAddr)

	evalInterval := c.policy.effectiveTrimRespondInterval()
	if !schedulePinsSetpoint && c.policy.EnableSupplyTempReset && mode != ScheduleModeStandby && len(runningHealthy) > 0 {
		targetSetpoint = c.stepCHWSupplyReset(now, evalInterval, loadRatioPct, chwDeltaT, hasCHWDeltaT, zoneTemp, hasZoneTemp)
	} else {
		c.chwResetLoop.initialized = true
		c.chwResetLoop.value = clampFloat(targetSetpoint, 4, 18)
		c.chwResetLoop.lastEvalAt = now
	}

	// Never feed current loadRatioPct back into LoadLimitAddr: doing so traps chillers at
	// minChillerLoadLimitPct (30%) once part-load drops. Only apply explicit operator/schedule
	// peak-demand caps, otherwise release LoadLimit to 100% full capacity.
	activeRatedKW := 0.0
	for _, ch := range runningHealthy {
		if ch.ComponentID == stoppingID {
			continue
		}
		activeRatedKW += effectiveRatedKW(ch.RatedCoolingKW)
	}
	targetLimitPct := 100.0
	if override != nil {
		if override.LoadLimitPct != nil && isFiniteFloat(*override.LoadLimitPct) {
			targetLimitPct = clampFloat(*override.LoadLimitPct, minChillerLoadLimitPct, 100.0)
		} else if override.Mode == ScheduleModePeakShave && override.TargetCoolingLoadKW != nil && *override.TargetCoolingLoadKW > 0 && activeRatedKW > 0 {
			targetLimitPct = clampFloat((*override.TargetCoolingLoadKW/activeRatedKW)*100.0, minChillerLoadLimitPct, 100.0)
		}
	}

	clampedSP := clampFloat(targetSetpoint, 4, 18)
	winningSP, hasWinningSP := 0.0, false
	for _, ch := range runningHealthy {
		if ch.ComponentID == stoppingID {
			continue
		}
		if d, ok := reqDoubleDecisionWritable(img, plan, ch.SetpointAddr, c.id, clampedSP); ok && d.Accepted && d.Effective != nil {
			winningSP = d.Effective.Double
			hasWinningSP = true
		}
		_ = reqDoubleWritable(img, plan, ch.LoadLimitAddr, c.id, targetLimitPct)
	}
	if hasWinningSP {
		c.chwResetLoop.initialized = true
		c.chwResetLoop.value = winningSP
		targetSetpoint = winningSP
	}

	loadFrac := clampFloat(loadRatioPct/100.0, 0, 1)
	if len(runningHealthy) == 0 {
		loadFrac = 0
	}
	fallbackPumpFreq := c.policy.MinPumpFrequencyHz + (c.policy.MaxPumpFrequencyHz-c.policy.MinPumpFrequencyHz)*loadFrac

	supForCoil := targetSetpoint
	if hasCHWSupplyTemp {
		supForCoil = chwSupplyTemp
	}

	chwPumps, cwPumps := splitClusterPumpsByLoop(c.pumps)
	chwpFreq := 0.0
	if len(chwPumps) > 0 {
		chwpFreq = c.computeCHWPFrequency(img, now, evalInterval, runningHealthy, chwPumps, fallbackPumpFreq, supForCoil, chwDeltaT, hasCHWDeltaT, chwDeltaP, hasCHWDeltaP, zoneTemp, hasZoneTemp)
		c.applyPumpLoop(img, plan, chwPumps, chwpFreq, &c.chwpLoop, now)
	}

	cwpFreq := 0.0
	if len(cwPumps) > 0 {
		cwpFreq = c.computeCWPFrequency(img, now, evalInterval, runningHealthy, cwPumps, fallbackPumpFreq, cwDeltaT, hasCWDeltaT, cwLeaving, hasCWLeaving)
		c.applyPumpLoop(img, plan, cwPumps, cwpFreq, &c.cwpLoop, now)
	}

	fallbackTowerSpeed := c.policy.MinTowerFanSpeedPct + (c.policy.MaxTowerFanSpeedPct-c.policy.MinTowerFanSpeedPct)*loadFrac
	towerSpeed := 0.0
	if len(c.towers) > 0 {
		towerSpeed = c.computeTowerFanSpeed(img, now, evalInterval, runningHealthy, c.towers, fallbackTowerSpeed, cwLeaving, hasCWLeaving, wetBulb, hasWetBulb, dryBulb, hasDryBulb)
		c.applyTowerLoop(img, plan, c.towers, towerSpeed, &c.towerLoop, now)
	}

	observedApproach := 0.0
	if hasCWLeaving && hasWetBulb {
		observedApproach = cwLeaving - wetBulb
	}

	c.status = ClusterControllerStatus{
		Active:                 true,
		Mode:                   mode,
		DesiredChillers:        desiredChillers,
		RunningChillers:        totalRunning,
		TotalCoolingLoadKW:     totalLoadKW,
		LoadRatioPct:           loadRatioPct,
		CHWPTargetHz:           chwpFreq,
		CWPTargetHz:            cwpFreq,
		TowerTargetPct:         towerSpeed,
		SupplySetpointC:        targetSetpoint,
		ObservedCHWDeltaTC:     chwDeltaT,
		ObservedCWDeltaTC:      cwDeltaT,
		ObservedTowerApproachC: observedApproach,
	}
	return nil
}

func (c *ChillerPlantClusterController) stepCHWSupplyReset(
	now time.Time,
	evalInterval time.Duration,
	loadRatioPct float64,
	chwDeltaT float64,
	hasCHWDeltaT bool,
	zoneTemp float64,
	hasZoneTemp bool,
) float64 {
	minSP, maxSP := c.policy.effectiveSupplySetpointBounds()
	seed := clampFloat(c.policy.DefaultSupplySetpointC, minSP, maxSP)
	if !c.chwResetLoop.initialized {
		c.chwResetLoop.initialized = true
		c.chwResetLoop.value = seed
		c.chwResetLoop.lastEvalAt = now
		return seed
	}
	cur := clampFloat(c.chwResetLoop.value, minSP, maxSP)
	resetInterval := evalInterval
	if resetInterval < 15*time.Minute {
		resetInterval = 15 * time.Minute
	}
	if now.Sub(c.chwResetLoop.lastEvalAt) < resetInterval {
		c.chwResetLoop.value = cur
		return cur
	}
	c.chwResetLoop.lastEvalAt = now

	// G36 CHW supply reset: respond DOWN (colder water) on zone demand / high pump utilization;
	// trim UP (warmer water for higher chiller COP) only when zones are cool and pump has headroom.
	zoneMax := c.policy.effectiveTargetZoneMaxC()
	pumpMaxed := c.chwpLoop.initialized && c.chwpLoop.value >= c.policy.MaxPumpFrequencyHz-2.0
	pumpHasHeadroom := !c.chwpLoop.initialized || c.chwpLoop.value <= c.policy.MaxPumpFrequencyHz-3.0
	targetDT := c.policy.effectiveTargetChwDeltaTC()

	stepC := 0.0
	if hasZoneTemp {
		switch {
		case zoneTemp > zoneMax+0.05:
			excess := zoneTemp - (zoneMax + 0.05)
			stepC = -math.Min(1.0, 0.5+1.2*excess)
		case pumpMaxed && hasCHWDeltaT && chwDeltaT > targetDT:
			stepC = -0.5
		case zoneTemp < zoneMax-0.15 && pumpHasHeadroom && loadRatioPct < 88.0:
			margin := (zoneMax - 0.15) - zoneTemp
			stepC = math.Min(0.6, 0.4+0.4*margin)
		}
	} else {
		// Without zone temperature feedback, only reset gently across [minSP, min(maxSP, minSP+2.0)]
		// from chilled-water load/DeltaT headroom to protect comfort.
		cappedMax := math.Min(maxSP, minSP+2.0)
		if pumpMaxed || (hasCHWDeltaT && chwDeltaT > targetDT+0.5) || loadRatioPct >= 80.0 {
			stepC = -0.5
		} else if pumpHasHeadroom && loadRatioPct < 60.0 && cur < cappedMax {
			stepC = 0.25
		}
		maxSP = cappedMax
	}

	next := clampFloat(cur+stepC, minSP, maxSP)
	c.chwResetLoop.value = next
	return next
}

func (c *ChillerPlantClusterController) computeCHWPFrequency(
	img ProcessImage,
	now time.Time,
	evalInterval time.Duration,
	runningHealthy []ClusterChillerBinding,
	pumps []ClusterPumpBinding,
	fallbackHz float64,
	supplyTempC float64,
	chwDeltaT float64,
	hasCHWDeltaT bool,
	chwDeltaP float64,
	hasCHWDeltaP bool,
	zoneTemp float64,
	hasZoneTemp bool,
) float64 {
	minHz, maxHz := c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz
	if len(runningHealthy) == 0 {
		c.chwpLoop.initialized = true
		c.chwpLoop.value = minHz
		c.chwpLoop.lastEvalAt = now
		return minHz
	}

	targetDP := c.policy.TargetChwDeltaPKPa
	useDP := hasCHWDeltaP && targetDP > 0
	hasClosedLoop := hasCHWDeltaT || useDP
	if !hasClosedLoop {
		return fallbackLoopSpeed(&c.chwpLoop, minHz, maxHz, fallbackHz, c.policy.effectivePumpSlewHzPerMin(), now)
	}
	c.chwpLoop.hadClosedLoopSensors = true

	if !c.chwpLoop.initialized {
		seed := firstGoodPumpFreq(img, pumps, fallbackHz, minHz, maxHz)
		c.chwpLoop.initialized = true
		c.chwpLoop.value = seed
		c.chwpLoop.lastEvalAt = now
		return seed
	}

	// Combine CHW DeltaT, optional DeltaP, and optional zone-high guard into a normalized error
	// relative to deadband = 0.5 (positive -> Respond UP, negative -> Trim DOWN).
	const deadband = 0.5
	var signals []float64
	if hasCHWDeltaT {
		supplyElev := math.Max(0, supplyTempC-7.0)
		effTargetDT := math.Max(3.0, c.policy.effectiveTargetChwDeltaTC()-0.30*supplyElev)
		signals = append(signals, chwDeltaT-effTargetDT)
	}
	if useDP {
		dpDeadband := math.Max(5.0, 0.08*targetDP)
		signals = append(signals, ((targetDP-chwDeltaP)/dpDeadband)*deadband)
	}
	zoneMax := c.policy.effectiveTargetZoneMaxC()
	if hasZoneTemp && zoneTemp > zoneMax+0.05 {
		signals = append(signals, deadband+(zoneTemp-(zoneMax+0.05))*2.0)
	}

	combinedErr := 0.0
	maxSig := math.Inf(-1)
	allTrim := len(signals) > 0
	minTrimMag := math.Inf(-1)
	for _, sig := range signals {
		if sig > maxSig {
			maxSig = sig
		}
		if sig >= -deadband {
			allTrim = false
		} else if sig > minTrimMag {
			minTrimMag = sig
		}
	}
	switch {
	case maxSig > deadband:
		combinedErr = maxSig
	case allTrim && !(hasZoneTemp && zoneTemp > zoneMax):
		combinedErr = minTrimMag
	}

	return stepTrimRespondLoop(
		&c.chwpLoop, now, evalInterval,
		minHz, maxHz,
		combinedErr, deadband,
		0.8, 1.4, 3.5,
		0.5, 0.7, 1.5,
		c.policy.effectivePumpSlewHzPerMin(),
	)
}

func (c *ChillerPlantClusterController) computeCWPFrequency(
	img ProcessImage,
	now time.Time,
	evalInterval time.Duration,
	runningHealthy []ClusterChillerBinding,
	pumps []ClusterPumpBinding,
	fallbackHz float64,
	cwDeltaT float64,
	hasCWDeltaT bool,
	cwLeaving float64,
	hasCWLeaving bool,
) float64 {
	minHz, maxHz := c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz
	if len(runningHealthy) == 0 {
		c.cwpLoop.initialized = true
		c.cwpLoop.value = minHz
		c.cwpLoop.lastEvalAt = now
		return minHz
	}
	if !hasCWDeltaT {
		return fallbackLoopSpeed(&c.cwpLoop, minHz, maxHz, fallbackHz, c.policy.effectivePumpSlewHzPerMin(), now)
	}
	c.cwpLoop.hadClosedLoopSensors = true

	if !c.cwpLoop.initialized {
		seed := firstGoodPumpFreq(img, pumps, fallbackHz, minHz, maxHz)
		c.cwpLoop.initialized = true
		c.cwpLoop.value = seed
		c.cwpLoop.lastEvalAt = now
		return seed
	}

	const deadband = 0.45
	errVal := cwDeltaT - c.policy.effectiveTargetCwDeltaTC()
	_, maxCW := c.policy.effectiveCondenserTempBounds()
	if hasCWLeaving && cwLeaving > maxCW-1.5 {
		highGuard := deadband + (cwLeaving-(maxCW-1.5))*1.2
		if highGuard > errVal {
			errVal = highGuard
		}
	}

	return stepTrimRespondLoop(
		&c.cwpLoop, now, evalInterval,
		minHz, maxHz,
		errVal, deadband,
		0.8, 1.5, 3.5,
		0.5, 0.7, 1.5,
		c.policy.effectivePumpSlewHzPerMin(),
	)
}

func (c *ChillerPlantClusterController) computeTowerFanSpeed(
	img ProcessImage,
	now time.Time,
	evalInterval time.Duration,
	runningHealthy []ClusterChillerBinding,
	towers []ClusterTowerBinding,
	fallbackPct float64,
	cwLeaving float64,
	hasCWLeaving bool,
	wetBulb float64,
	hasWetBulb bool,
	dryBulb float64,
	hasDryBulb bool,
) float64 {
	minPct, maxPct := c.policy.MinTowerFanSpeedPct, c.policy.MaxTowerFanSpeedPct
	if len(runningHealthy) == 0 {
		c.towerLoop.initialized = true
		c.towerLoop.value = minPct
		c.towerLoop.lastEvalAt = now
		return minPct
	}
	if !hasCWLeaving {
		return fallbackLoopSpeed(&c.towerLoop, minPct, maxPct, fallbackPct, c.policy.effectiveTowerSlewPctPerMin(), now)
	}
	c.towerLoop.hadClosedLoopSensors = true

	if !c.towerLoop.initialized {
		seed := firstGoodTowerSpeed(img, towers, fallbackPct, minPct, maxPct)
		c.towerLoop.initialized = true
		c.towerLoop.value = seed
		c.towerLoop.lastEvalAt = now
		return seed
	}

	minCW, maxCW := c.policy.effectiveCondenserTempBounds()
	targetCW := clampFloat(26.0, minCW, maxCW)
	deadband := 0.8
	if hasWetBulb {
		targetCW = clampFloat(wetBulb+c.policy.effectiveTargetTowerApproachC(), minCW, maxCW)
		deadband = 0.45
	} else if hasDryBulb {
		targetCW = clampFloat(dryBulb-2.0, minCW, math.Min(maxCW, 30.0))
	}

	return stepTrimRespondLoop(
		&c.towerLoop, now, evalInterval,
		minPct, maxPct,
		cwLeaving-targetCW, deadband,
		2.0, 3.5, 8.0,
		1.5, 2.0, 4.0,
		c.policy.effectiveTowerSlewPctPerMin(),
	)
}

func fallbackLoopSpeed(loop *trimRespondLoopState, minVal, maxVal, fallbackVal, slewPerMin float64, now time.Time) float64 {
	clamped := clampFloat(fallbackVal, minVal, maxVal)
	if !loop.hadClosedLoopSensors {
		return clamped
	}
	// If closed-loop sensors were previously live and temporarily dropped out, ramp smoothly
	// rather than snapping immediately.
	if !loop.initialized {
		loop.initialized = true
		loop.value = clamped
		loop.lastEvalAt = now
		return clamped
	}
	elapsed := now.Sub(loop.lastEvalAt)
	if elapsed <= 0 {
		return clampFloat(loop.value, minVal, maxVal)
	}
	loop.lastEvalAt = now
	maxStep := slewPerMin * elapsed.Minutes()
	delta := clampFloat(clamped-loop.value, -maxStep, maxStep)
	loop.value = clampFloat(loop.value+delta, minVal, maxVal)
	return loop.value
}

func stepTrimRespondLoop(
	loop *trimRespondLoopState,
	now time.Time,
	evalInterval time.Duration,
	minVal, maxVal float64,
	errVal, deadband float64,
	respondBase, respondGain, maxRespondStep float64,
	trimBase, trimGain, maxTrimStep float64,
	slewPerMin float64,
) float64 {
	cur := clampFloat(loop.value, minVal, maxVal)
	elapsed := now.Sub(loop.lastEvalAt)
	if elapsed < evalInterval {
		loop.value = cur
		return cur
	}
	loop.lastEvalAt = now

	rawStep := 0.0
	if errVal > deadband {
		excess := errVal - deadband
		rawStep = math.Min(maxRespondStep, respondBase+respondGain*excess)
	} else if errVal < -deadband {
		excess := -errVal - deadband
		rawStep = -math.Min(maxTrimStep, trimBase+trimGain*excess)
	}
	maxSlew := slewPerMin * elapsed.Minutes()
	if maxSlew > 0 {
		rawStep = clampFloat(rawStep, -maxSlew, maxSlew)
	}
	next := clampFloat(cur+rawStep, minVal, maxVal)
	loop.value = next
	return next
}

func (c *ChillerPlantClusterController) applyPumpLoop(
	img ProcessImage,
	plan *ControlPlan,
	pumps []ClusterPumpBinding,
	targetHz float64,
	loop *trimRespondLoopState,
	now time.Time,
) {
	hasWinningRemote := false
	winningHz := 0.0
	manualFeedbackHz := 0.0
	hasManualFeedback := false

	for _, pump := range pumps {
		if faultChannelActive(img, pump.FaultAddr) {
			continue
		}
		if pump.RunStateAddr != "" {
			running, _ := runChannelActive(img, pump.RunStateAddr)
			if !running {
				continue
			}
		}
		isManual := localRemoteChannelIsManual(img, pump.LocalRemoteModeAddr)
		if isManual {
			if actual, ok := readGoodNumeric(img, pump.ActualFrequencyAddr); ok {
				manualFeedbackHz = clampFloat(actual, c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz)
				hasManualFeedback = true
			}
		}
		d, ok := reqDoubleDecisionWritable(img, plan, pump.FrequencyAddr, c.id, targetHz)
		if !ok {
			continue
		}
		if d.Accepted && d.Effective != nil {
			winningHz = d.Effective.Double
			hasWinningRemote = true
		} else if !d.Accepted && !hasManualFeedback {
			if actual, ok := readGoodNumeric(img, pump.ActualFrequencyAddr); ok {
				manualFeedbackHz = clampFloat(actual, c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz)
				hasManualFeedback = true
			}
		}
	}

	if hasWinningRemote {
		loop.value = clampFloat(winningHz, c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz)
	} else if hasManualFeedback {
		loop.initialized = true
		loop.value = manualFeedbackHz
		loop.lastEvalAt = now
	}
}

func (c *ChillerPlantClusterController) applyTowerLoop(
	img ProcessImage,
	plan *ControlPlan,
	towers []ClusterTowerBinding,
	targetPct float64,
	loop *trimRespondLoopState,
	now time.Time,
) {
	hasWinningRemote := false
	winningPct := 0.0
	manualFeedbackPct := 0.0
	hasManualFeedback := false

	for _, tower := range towers {
		if faultChannelActive(img, tower.FaultAddr) {
			continue
		}
		if tower.RunStateAddr != "" {
			running, _ := runChannelActive(img, tower.RunStateAddr)
			if !running {
				continue
			}
		}
		isManual := localRemoteChannelIsManual(img, tower.LocalRemoteModeAddr)
		if isManual {
			if actual, ok := readGoodNumeric(img, tower.ActualFanSpeedAddr); ok {
				manualFeedbackPct = clampFloat(actual, c.policy.MinTowerFanSpeedPct, c.policy.MaxTowerFanSpeedPct)
				hasManualFeedback = true
			}
		}
		d, ok := reqDoubleDecisionWritable(img, plan, tower.FanSpeedAddr, c.id, targetPct)
		if !ok {
			continue
		}
		if d.Accepted && d.Effective != nil {
			winningPct = d.Effective.Double
			hasWinningRemote = true
		} else if !d.Accepted && !hasManualFeedback {
			if actual, ok := readGoodNumeric(img, tower.ActualFanSpeedAddr); ok {
				manualFeedbackPct = clampFloat(actual, c.policy.MinTowerFanSpeedPct, c.policy.MaxTowerFanSpeedPct)
				hasManualFeedback = true
			}
		}
	}

	if hasWinningRemote {
		loop.value = clampFloat(winningPct, c.policy.MinTowerFanSpeedPct, c.policy.MaxTowerFanSpeedPct)
	} else if hasManualFeedback {
		loop.initialized = true
		loop.value = manualFeedbackPct
		loop.lastEvalAt = now
	}
}

func splitClusterPumpsByLoop(pumps []ClusterPumpBinding) (chwPumps, cwPumps []ClusterPumpBinding) {
	for _, p := range pumps {
		switch strings.ToUpper(strings.TrimSpace(p.Loop)) {
		case ClusterPumpLoopCW, "CONDENSER":
			cwPumps = append(cwPumps, p)
		default:
			chwPumps = append(chwPumps, p)
		}
	}
	return chwPumps, cwPumps
}

func firstGoodPumpFreq(img ProcessImage, pumps []ClusterPumpBinding, fallback, minHz, maxHz float64) float64 {
	for _, p := range pumps {
		if v, ok := readGoodNumeric(img, p.ActualFrequencyAddr); ok && v > 0 {
			return clampFloat(v, minHz, maxHz)
		}
		if v, ok := readGoodNumeric(img, p.FrequencyAddr); ok && v > 0 {
			return clampFloat(v, minHz, maxHz)
		}
	}
	return clampFloat(fallback, minHz, maxHz)
}

func firstGoodTowerSpeed(img ProcessImage, towers []ClusterTowerBinding, fallback, minPct, maxPct float64) float64 {
	for _, t := range towers {
		if v, ok := readGoodNumeric(img, t.ActualFanSpeedAddr); ok && v > 0 {
			return clampFloat(v, minPct, maxPct)
		}
		if v, ok := readGoodNumeric(img, t.FanSpeedAddr); ok && v > 0 {
			return clampFloat(v, minPct, maxPct)
		}
	}
	return clampFloat(fallback, minPct, maxPct)
}

func (c *ChillerPlantClusterController) observeCHWDeltaT(img ProcessImage, running []ClusterChillerBinding) (float64, bool) {
	if dtVal, ok := readGoodNumeric(img, c.sensors.CHWDeltaTAddr); ok && dtVal >= 0 && dtVal <= 25 {
		return dtVal, true
	}
	sup, hasSup := readGoodNumeric(img, c.sensors.CHWSupplyTempAddr)
	ret, hasRet := readGoodNumeric(img, c.sensors.CHWReturnTempAddr)
	if hasSup && hasRet && ret >= sup-1.0 {
		return math.Max(0, ret-sup), true
	}
	sum, count := 0.0, 0
	for _, ch := range running {
		l, okL := readGoodNumeric(img, ch.LeavingCHWTempAddr)
		e, okE := readGoodNumeric(img, ch.EnteringCHWTempAddr)
		if okL && okE && e >= l-1.0 {
			sum += math.Max(0, e-l)
			count++
		}
	}
	if count > 0 {
		return sum / float64(count), true
	}
	return 0, false
}

func (c *ChillerPlantClusterController) observeCHWSupplyTemp(img ProcessImage, running []ClusterChillerBinding) (float64, bool) {
	if sup, ok := readGoodNumeric(img, c.sensors.CHWSupplyTempAddr); ok {
		return sup, true
	}
	sum, count := 0.0, 0
	for _, ch := range running {
		if sup, ok := readGoodNumeric(img, ch.LeavingCHWTempAddr); ok {
			sum += sup
			count++
		}
	}
	if count > 0 {
		return sum / float64(count), true
	}
	return 0, false
}

func (c *ChillerPlantClusterController) observeCWDeltaT(img ProcessImage, running []ClusterChillerBinding) (float64, bool) {
	cwIn, hasIn := c.observeCWLeavingTemp(img, running)
	cwOut, hasOut := c.observeCWEnteringTemp(img, running)
	if hasIn && hasOut && cwOut >= cwIn-1.0 {
		return math.Max(0, cwOut-cwIn), true
	}
	sum, count := 0.0, 0
	for _, ch := range running {
		e, okE := readGoodNumeric(img, ch.EnteringCWTempAddr)
		l, okL := readGoodNumeric(img, ch.LeavingCWTempAddr)
		if okE && okL && l >= e-1.0 {
			sum += math.Max(0, l-e)
			count++
		}
	}
	if count > 0 {
		return sum / float64(count), true
	}
	return 0, false
}

func (c *ChillerPlantClusterController) observeCWLeavingTemp(img ProcessImage, running []ClusterChillerBinding) (float64, bool) {
	if v, ok := readGoodNumeric(img, c.sensors.CWLeavingTempAddr); ok {
		return v, true
	}
	for _, t := range c.towers {
		if v, ok := readGoodNumeric(img, t.LeavingWaterTempAddr); ok {
			return v, true
		}
	}
	sum, count := 0.0, 0
	for _, ch := range running {
		if v, ok := readGoodNumeric(img, ch.EnteringCWTempAddr); ok {
			sum += v
			count++
		}
	}
	if count > 0 {
		return sum / float64(count), true
	}
	return 0, false
}

func (c *ChillerPlantClusterController) observeCWEnteringTemp(img ProcessImage, running []ClusterChillerBinding) (float64, bool) {
	if v, ok := readGoodNumeric(img, c.sensors.CWEnteringTempAddr); ok {
		return v, true
	}
	for _, t := range c.towers {
		if v, ok := readGoodNumeric(img, t.EnteringWaterTempAddr); ok {
			return v, true
		}
	}
	sum, count := 0.0, 0
	for _, ch := range running {
		if v, ok := readGoodNumeric(img, ch.LeavingCWTempAddr); ok {
			sum += v
			count++
		}
	}
	if count > 0 {
		return sum / float64(count), true
	}
	return 0, false
}

func (c *ChillerPlantClusterController) observeWetBulbTemp(img ProcessImage) (float64, bool) {
	if wb, ok := readGoodNumeric(img, c.sensors.WetBulbTempAddr); ok {
		return wb, true
	}
	db, hasDB := readGoodNumeric(img, c.sensors.OutdoorTempAddr)
	rh, hasRH := readGoodNumeric(img, c.sensors.OutdoorHumidityAddr)
	if !hasDB || !hasRH {
		return 0, false
	}
	rh = clampFloat(rh, 5.0, 100.0)
	// Stull (2011) empirical wet-bulb approximation valid for standard atmospheric pressure.
	wb := db*math.Atan(0.151977*math.Sqrt(rh+8.313659)) +
		math.Atan(db+rh) -
		math.Atan(rh-1.676331) +
		0.00391838*math.Pow(rh, 1.5)*math.Atan(0.023101*rh) -
		4.686035
	return math.Min(wb, db), true
}

func pickLowestRuntimeChiller(candidates []ClusterChillerBinding, runtimeSec map[string]float64) ClusterChillerBinding {
	best := candidates[0]
	bestRuntime := runtimeSec[best.ComponentID]
	for _, ch := range candidates[1:] {
		rt := runtimeSec[ch.ComponentID]
		if rt < bestRuntime || (rt == bestRuntime && ch.ComponentID < best.ComponentID) {
			best = ch
			bestRuntime = rt
		}
	}
	return best
}

func pickHighestRuntimeChiller(candidates []ClusterChillerBinding, runtimeSec map[string]float64) ClusterChillerBinding {
	best := candidates[0]
	bestRuntime := runtimeSec[best.ComponentID]
	for _, ch := range candidates[1:] {
		rt := runtimeSec[ch.ComponentID]
		if rt > bestRuntime || (rt == bestRuntime && ch.ComponentID > best.ComponentID) {
			best = ch
			bestRuntime = rt
		}
	}
	return best
}

func effectiveRatedKW(rated float64) float64 {
	if isFiniteFloat(rated) && rated > 0 {
		return rated
	}
	return defaultChillerRatedKW
}

func readGoodNumeric(img ProcessImage, addr string) (float64, bool) {
	if addr == "" {
		return 0, false
	}
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != QualityGood {
		return 0, false
	}
	val, numOK := snap.Sample.Value.NumericFloat64()
	if !numOK || !isFiniteFloat(val) {
		return 0, false
	}
	return val, true
}

func reqBoolWritable(img ProcessImage, plan *ControlPlan, addr, controllerID string, val bool) bool {
	if addr == "" {
		return false
	}
	snap, ok := img.Get(addr)
	if !ok || snap.Descriptor.Access == AccessReadOnly || IsHardRealtimeChannel(snap.Descriptor) {
		return false
	}
	_, changed, err := plan.Request(addr, controllerID, BooleanValue(val))
	return err == nil && changed
}

func reqDoubleWritable(img ProcessImage, plan *ControlPlan, addr, controllerID string, val float64) bool {
	_, changed := reqDoubleDecisionWritable(img, plan, addr, controllerID, val)
	return changed
}

func reqDoubleDecisionWritable(img ProcessImage, plan *ControlPlan, addr, controllerID string, val float64) (Decision, bool) {
	if addr == "" {
		return Decision{}, false
	}
	snap, ok := img.Get(addr)
	if !ok || snap.Descriptor.Access == AccessReadOnly || snap.Descriptor.DataType != DataTypeDouble || IsHardRealtimeChannel(snap.Descriptor) {
		return Decision{}, false
	}
	d, _, err := plan.Request(addr, controllerID, DoubleValue(val))
	if err != nil {
		return Decision{}, false
	}
	return d, true
}
