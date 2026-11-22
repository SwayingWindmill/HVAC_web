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
	defaultChillerRatedKW      = 100.0
	minChillerLoadLimitPct     = 30.0
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
}

// ClusterPumpBinding maps one chilled/condenser variable-speed pump into cluster coordination.
type ClusterPumpBinding struct {
	ComponentID   string
	FrequencyAddr string
	RunStateAddr  string
	FaultAddr     string
	FlowAddr      string
}

// ClusterTowerBinding maps one cooling tower fan into cluster coordination.
type ClusterTowerBinding struct {
	ComponentID  string
	FanSpeedAddr string
	RunStateAddr string
	FaultAddr    string
}

// ClusterControlPolicy parameterizes multi-chiller staging hysteresis and plant bounds.
type ClusterControlPolicy struct {
	Enabled                bool    `json:"enabled"`
	MinRunningChillers     int     `json:"minRunningChillers"`
	MaxRunningChillers     int     `json:"maxRunningChillers"`
	StageUpLoadPct         float64 `json:"stageUpLoadPct"`
	StageDownLoadPct       float64 `json:"stageDownLoadPct"`
	StageDwellSeconds      int     `json:"stageDwellSeconds"`
	DefaultSupplySetpointC float64 `json:"defaultSupplySetpointC"`
	MinPumpFrequencyHz     float64 `json:"minPumpFrequencyHz"`
	MaxPumpFrequencyHz     float64 `json:"maxPumpFrequencyHz"`
	MinTowerFanSpeedPct    float64 `json:"minTowerFanSpeedPct"`
	MaxTowerFanSpeedPct    float64 `json:"maxTowerFanSpeedPct"`
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
	return nil
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
}

// ClusterControllerConfig configures ChillerPlantClusterController.
type ClusterControllerConfig struct {
	ID            string
	Policy        ClusterControlPolicy
	Chillers      []ClusterChillerBinding
	Pumps         []ClusterPumpBinding
	Towers        []ClusterTowerBinding
	PlantLoadAddr string
}

// ClusterControllerStatus exposes observable staging and load balance state.
type ClusterControllerStatus struct {
	Active                  bool
	Mode                    ScheduleOperatingMode
	DesiredChillers         int
	RunningChillers         int
	TotalCoolingLoadKW      float64
	LoadRatioPct            float64
	RuntimeSecondsByChiller map[string]float64
}

// ChillerPlantClusterController coordinates staging order, wear-leveling, cooling
// load sharing, pump VFD speed, and cooling tower fan speed across a multi-chiller plant.
type ChillerPlantClusterController struct {
	mu                 sync.Mutex
	id                 string
	policy             ClusterControlPolicy
	chillers           []ClusterChillerBinding
	pumps              []ClusterPumpBinding
	towers             []ClusterTowerBinding
	plantLoadAddr      string
	scheduleOverrideFn func(time.Time) *ClusterScheduleOverride
	lastRunAt          time.Time
	lastStageAt        time.Time
	runtimeSec         map[string]float64
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
		runtimeSec:    make(map[string]float64),
		status:        ClusterControllerStatus{RuntimeSecondsByChiller: map[string]float64{}},
	}, nil
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
// and coordinates chilled water setpoints, load limits, pump VFDs, and tower fans.
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

	targetLimitPct := clampFloat(math.Max(loadRatioPct, minChillerLoadLimitPct), minChillerLoadLimitPct, 100.0)
	for _, ch := range runningHealthy {
		if ch.ComponentID == stoppingID {
			continue
		}
		_ = reqDoubleWritable(img, plan, ch.SetpointAddr, c.id, clampFloat(targetSetpoint, 4, 18))
		_ = reqDoubleWritable(img, plan, ch.LoadLimitAddr, c.id, targetLimitPct)
	}

	loadFrac := clampFloat(loadRatioPct/100.0, 0, 1)
	if len(runningHealthy) == 0 {
		loadFrac = 0
	}
	pumpFreq := c.policy.MinPumpFrequencyHz + (c.policy.MaxPumpFrequencyHz-c.policy.MinPumpFrequencyHz)*loadFrac
	for _, pump := range c.pumps {
		if faultChannelActive(img, pump.FaultAddr) {
			continue
		}
		if pump.RunStateAddr != "" {
			running, _ := runChannelActive(img, pump.RunStateAddr)
			if !running {
				continue
			}
		}
		_ = reqDoubleWritable(img, plan, pump.FrequencyAddr, c.id, pumpFreq)
	}

	towerSpeed := c.policy.MinTowerFanSpeedPct + (c.policy.MaxTowerFanSpeedPct-c.policy.MinTowerFanSpeedPct)*loadFrac
	for _, tower := range c.towers {
		if faultChannelActive(img, tower.FaultAddr) {
			continue
		}
		if tower.RunStateAddr != "" {
			running, _ := runChannelActive(img, tower.RunStateAddr)
			if !running {
				continue
			}
		}
		_ = reqDoubleWritable(img, plan, tower.FanSpeedAddr, c.id, towerSpeed)
	}

	c.status = ClusterControllerStatus{
		Active:             true,
		Mode:               mode,
		DesiredChillers:    desiredChillers,
		RunningChillers:    totalRunning,
		TotalCoolingLoadKW: totalLoadKW,
		LoadRatioPct:       loadRatioPct,
	}
	return nil
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
	if addr == "" {
		return false
	}
	snap, ok := img.Get(addr)
	if !ok || snap.Descriptor.Access == AccessReadOnly || snap.Descriptor.DataType != DataTypeDouble || IsHardRealtimeChannel(snap.Descriptor) {
		return false
	}
	_, changed, err := plan.Request(addr, controllerID, DoubleValue(val))
	return err == nil && changed
}
