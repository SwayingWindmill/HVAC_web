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
	DefaultHydraulicInterlockID  = "hydraulic-interlock"
	DefaultAutonomousFallbackID  = "autonomous-fallback"
	defaultOfflineGraceSeconds   = 120
	defaultSupplySetpointC       = 7.0
	defaultPumpFrequencyHz       = 35.0
	defaultMinPumpFrequencyHz    = 20.0
	defaultMaxPumpFrequencyHz    = 50.0
	defaultSnapshotIntervalSecs  = 300
	autonomyReasonNoCHWFlow      = "HYDRAULIC_INTERLOCK_NO_CHW_FLOW"
	autonomyReasonChillerFault   = "HYDRAULIC_INTERLOCK_CHILLER_FAULT"
	autonomyReasonMinRunTime     = "COMPRESSOR_MIN_RUN_TIME"
	autonomyReasonMinStopTime    = "COMPRESSOR_MIN_STOP_TIME"
	autonomyReasonPumpFreqLimits = "PUMP_FREQUENCY_LIMIT"
)

// AutonomyPolicyConfig mirrors the edge autonomy & snapshot policy distributed
// via config shadow.
type AutonomyPolicyConfig struct {
	Enabled                            bool    `json:"enabled"`
	OfflineGraceSeconds                int     `json:"offlineGraceSeconds"`
	DefaultChilledWaterSupplySetpointC float64 `json:"defaultChilledWaterSupplySetpointC"`
	DefaultPumpFrequencyHz             float64 `json:"defaultPumpFrequencyHz"`
	MinPumpFrequencyHz                 float64 `json:"minPumpFrequencyHz"`
	MaxPumpFrequencyHz                 float64 `json:"maxPumpFrequencyHz"`
	MinRunSeconds                      int     `json:"minRunSeconds"`
	MinStopSeconds                     int     `json:"minStopSeconds"`
	SnapshotIntervalSeconds            int     `json:"snapshotIntervalSeconds"`
}

// DefaultAutonomyPolicyConfig returns safe default autonomy policy settings with
// autonomous fallback initially disabled and short-cycle lockouts set to 0s.
func DefaultAutonomyPolicyConfig() AutonomyPolicyConfig {
	return AutonomyPolicyConfig{
		Enabled:                            false,
		OfflineGraceSeconds:                defaultOfflineGraceSeconds,
		DefaultChilledWaterSupplySetpointC: defaultSupplySetpointC,
		DefaultPumpFrequencyHz:             defaultPumpFrequencyHz,
		MinPumpFrequencyHz:                 defaultMinPumpFrequencyHz,
		MaxPumpFrequencyHz:                 defaultMaxPumpFrequencyHz,
		MinRunSeconds:                      0,
		MinStopSeconds:                     0,
		SnapshotIntervalSeconds:            defaultSnapshotIntervalSecs,
	}
}

// Validate enforces all protocol bounds and invariant ordering on AutonomyPolicyConfig.
func (p AutonomyPolicyConfig) Validate() error {
	if p.OfflineGraceSeconds < 10 || p.OfflineGraceSeconds > 3600 {
		return fmt.Errorf("offlineGraceSeconds %d must be in [10,3600]", p.OfflineGraceSeconds)
	}
	if !isFiniteFloat(p.DefaultChilledWaterSupplySetpointC) || p.DefaultChilledWaterSupplySetpointC < 4 || p.DefaultChilledWaterSupplySetpointC > 18 {
		return fmt.Errorf("defaultChilledWaterSupplySetpointC %v must be finite and in [4,18]", p.DefaultChilledWaterSupplySetpointC)
	}
	if !isFiniteFloat(p.MinPumpFrequencyHz) || p.MinPumpFrequencyHz < 15 || p.MinPumpFrequencyHz > 50 {
		return fmt.Errorf("minPumpFrequencyHz %v must be finite and in [15,50]", p.MinPumpFrequencyHz)
	}
	if !isFiniteFloat(p.MaxPumpFrequencyHz) || p.MaxPumpFrequencyHz < 15 || p.MaxPumpFrequencyHz > 50 {
		return fmt.Errorf("maxPumpFrequencyHz %v must be finite and in [15,50]", p.MaxPumpFrequencyHz)
	}
	if p.MinPumpFrequencyHz > p.MaxPumpFrequencyHz {
		return fmt.Errorf("minPumpFrequencyHz (%v) must be <= maxPumpFrequencyHz (%v)", p.MinPumpFrequencyHz, p.MaxPumpFrequencyHz)
	}
	if !isFiniteFloat(p.DefaultPumpFrequencyHz) || p.DefaultPumpFrequencyHz < 15 || p.DefaultPumpFrequencyHz > 50 {
		return fmt.Errorf("defaultPumpFrequencyHz %v must be finite and in [15,50]", p.DefaultPumpFrequencyHz)
	}
	if p.DefaultPumpFrequencyHz < p.MinPumpFrequencyHz || p.DefaultPumpFrequencyHz > p.MaxPumpFrequencyHz {
		return fmt.Errorf("defaultPumpFrequencyHz (%v) must stay within [minPumpFrequencyHz (%v), maxPumpFrequencyHz (%v)]", p.DefaultPumpFrequencyHz, p.MinPumpFrequencyHz, p.MaxPumpFrequencyHz)
	}
	if p.MinRunSeconds < 0 || p.MinRunSeconds > 3600 {
		return fmt.Errorf("minRunSeconds %d must be in [0,3600]", p.MinRunSeconds)
	}
	if p.MinStopSeconds < 0 || p.MinStopSeconds > 3600 {
		return fmt.Errorf("minStopSeconds %d must be in [0,3600]", p.MinStopSeconds)
	}
	if p.SnapshotIntervalSeconds < 30 || p.SnapshotIntervalSeconds > 3600 {
		return fmt.Errorf("snapshotIntervalSeconds %d must be in [30,3600]", p.SnapshotIntervalSeconds)
	}
	return nil
}

// OfflineGrace returns the grace period before offline autonomy takes over.
func (p AutonomyPolicyConfig) OfflineGrace() time.Duration {
	return time.Duration(p.OfflineGraceSeconds) * time.Second
}

// SnapshotInterval returns the periodic full-snapshot emission interval.
func (p AutonomyPolicyConfig) SnapshotInterval() time.Duration {
	return time.Duration(p.SnapshotIntervalSeconds) * time.Second
}

func isFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// ChillerInterlockBinding names the ProcessImage channels used to guard one chiller.
type ChillerInterlockBinding struct {
	ComponentID  string
	StartCommand string
	StopCommand  string
	RunState     string
	FaultChannel string
}

// PumpInterlockBinding names the ProcessImage channels used to verify pump run/flow proof.
type PumpInterlockBinding struct {
	ComponentID      string
	RunState         string
	FlowFeedback     string
	MinFlowRate      float64
	FaultChannel     string
	FrequencyCommand string
}

// HydraulicInterlockConfig configures HydraulicInterlockController.
type HydraulicInterlockConfig struct {
	ID                 string
	Chillers           []ChillerInterlockBinding
	ChilledWaterPumps  []PumpInterlockBinding
	PumpFrequencyAddrs []string
	Policy             AutonomyPolicyConfig
}

// HydraulicInterlockStatus reports the controller's latest evaluated interlock state.
type HydraulicInterlockStatus struct {
	ChilledWaterFlowProven bool
	MinPumpFrequencyHz     float64
	MaxPumpFrequencyHz     float64
	LastBlockedReasons     map[string]string
}

type compressorCycleTracker struct {
	initialized   bool
	running       bool
	lastStartedAt time.Time
	lastStoppedAt time.Time
}

// HydraulicInterlockController runs at highest local priority before equipment
// intents commit, blocking unsafe chiller starts, tripping chillers on flow loss,
// enforcing compressor min-run / min-stop timers, and clamping pump VFD bounds.
type HydraulicInterlockController struct {
	mu                 sync.Mutex
	id                 string
	chillers           []ChillerInterlockBinding
	chwPumps           []PumpInterlockBinding
	pumpFrequencyAddrs []string
	policy             AutonomyPolicyConfig
	trackers           map[string]*compressorCycleTracker
	flowProven         bool
	lastBlockedReasons map[string]string
}

// NewHydraulicInterlockController creates a validated hydraulic safety controller.
func NewHydraulicInterlockController(cfg HydraulicInterlockConfig) (*HydraulicInterlockController, error) {
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		id = DefaultHydraulicInterlockID
	}

	return &HydraulicInterlockController{
		id:                 id,
		chillers:           slices.Clone(cfg.Chillers),
		chwPumps:           slices.Clone(cfg.ChilledWaterPumps),
		pumpFrequencyAddrs: collectPumpFrequencyAddrs(cfg.PumpFrequencyAddrs, cfg.ChilledWaterPumps),
		policy:             cfg.Policy,
		trackers:           make(map[string]*compressorCycleTracker, len(cfg.Chillers)),
		lastBlockedReasons: make(map[string]string),
	}, nil
}

// ID returns the controller identifier.
func (c *HydraulicInterlockController) ID() string {
	return c.id
}

// UpdatePolicy hot-updates interlock bounds and short-cycle timers without dropping
// active compressor run/stop transition history.
func (c *HydraulicInterlockController) UpdatePolicy(policy AutonomyPolicyConfig) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policy = policy
	return nil
}

// UpdateBindings hot-updates equipment bindings when driver config shadow changes.
func (c *HydraulicInterlockController) UpdateBindings(chillers []ChillerInterlockBinding, chwPumps []PumpInterlockBinding, pumpFrequencyAddrs []string) {
	dedup := collectPumpFrequencyAddrs(pumpFrequencyAddrs, chwPumps)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.chillers = slices.Clone(chillers)
	c.chwPumps = slices.Clone(chwPumps)
	c.pumpFrequencyAddrs = dedup
}

func collectPumpFrequencyAddrs(pumpFrequencyAddrs []string, chwPumps []PumpInterlockBinding) []string {
	freqSet := make(map[string]struct{}, len(pumpFrequencyAddrs)+len(chwPumps))
	out := make([]string, 0, len(pumpFrequencyAddrs)+len(chwPumps))
	addFreq := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}
		if _, dup := freqSet[addr]; dup {
			return
		}
		freqSet[addr] = struct{}{}
		out = append(out, addr)
	}
	for _, addr := range pumpFrequencyAddrs {
		addFreq(addr)
	}
	for _, pump := range chwPumps {
		addFreq(pump.FrequencyCommand)
	}
	return out
}

// Policy returns a copy of the active policy config.
func (c *HydraulicInterlockController) Policy() AutonomyPolicyConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.policy
}

// Status returns a snapshot of the latest evaluated hydraulic interlock state.
func (c *HydraulicInterlockController) Status() HydraulicInterlockStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	reasons := make(map[string]string, len(c.lastBlockedReasons))
	for k, v := range c.lastBlockedReasons {
		reasons[k] = v
	}
	return HydraulicInterlockStatus{
		ChilledWaterFlowProven: c.flowProven,
		MinPumpFrequencyHz:     c.policy.MinPumpFrequencyHz,
		MaxPumpFrequencyHz:     c.policy.MaxPumpFrequencyHz,
		LastBlockedReasons:     reasons,
	}
}

// Run evaluates pump limits, flow proof, fault trips, and short-cycle timers against img/plan.
func (c *HydraulicInterlockController) Run(_ context.Context, img ProcessImage, plan *ControlPlan) error {
	if plan == nil {
		return errors.New("hydraulic interlock control plan is nil")
	}

	now := img.At()
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	policy := c.policy
	minHz := policy.MinPumpFrequencyHz
	maxHz := policy.MaxPumpFrequencyHz
	for _, addr := range c.pumpFrequencyAddrs {
		snap, ok := img.Get(addr)
		if !ok || snap.Descriptor.DataType != DataTypeDouble || snap.Descriptor.Access == AccessReadOnly {
			continue
		}
		if _, err := plan.ConstrainNumber(addr, c.id, autonomyReasonPumpFreqLimits, &minHz, &maxHz); err != nil {
			return err
		}
	}

	flowProven := chilledWaterFlowProven(img, c.chwPumps)
	c.flowProven = flowProven
	blocked := make(map[string]string)

	for _, ch := range c.chillers {
		key := ch.ComponentID
		if key == "" {
			key = ch.StartCommand + "|" + ch.RunState
		}
		tracker := c.trackers[key]
		if tracker == nil {
			tracker = &compressorCycleTracker{}
			c.trackers[key] = tracker
		}

		isRunning, hasRunState := runChannelActive(img, ch.RunState)
		if hasRunState {
			if !tracker.initialized {
				tracker.initialized = true
				tracker.running = isRunning
				if isRunning {
					tracker.lastStartedAt = now
				}
			} else if tracker.running != isRunning {
				tracker.running = isRunning
				if isRunning {
					tracker.lastStartedAt = now
				} else {
					tracker.lastStoppedAt = now
				}
			}
		}

		chFaulted := faultChannelActive(img, ch.FaultChannel)

		if !flowProven {
			if isRunning && ch.StopCommand == "" && ch.StartCommand != "" {
				if snap, ok := img.Get(ch.StartCommand); ok && snap.Descriptor.Access != AccessReadOnly {
					if _, _, err := plan.Request(ch.StartCommand, c.id, BooleanValue(false)); err != nil {
						return err
					}
					blocked[ch.StartCommand] = autonomyReasonNoCHWFlow
				}
				continue
			}
			if denyWritableChannel(img, plan, ch.StartCommand, c.id, autonomyReasonNoCHWFlow) {
				blocked[ch.StartCommand] = autonomyReasonNoCHWFlow
			}
			// Emergency hydraulic interlock trip overrides MinRunSeconds whenever the chiller is running.
			if isRunning && ch.StopCommand != "" {
				if snap, ok := img.Get(ch.StopCommand); ok && snap.Descriptor.Access != AccessReadOnly {
					if _, _, err := plan.Request(ch.StopCommand, c.id, BooleanValue(true)); err != nil {
						return err
					}
					blocked[key+":emergency_stop"] = autonomyReasonNoCHWFlow
				}
			}
			continue
		}

		if chFaulted {
			if denyWritableChannel(img, plan, ch.StartCommand, c.id, autonomyReasonChillerFault) {
				blocked[ch.StartCommand] = autonomyReasonChillerFault
			}
			continue
		}

		if isRunning && policy.MinRunSeconds > 0 && !tracker.lastStartedAt.IsZero() && now.Sub(tracker.lastStartedAt) < time.Duration(policy.MinRunSeconds)*time.Second {
			if denyWritableChannel(img, plan, ch.StopCommand, c.id, autonomyReasonMinRunTime) {
				blocked[ch.StopCommand] = autonomyReasonMinRunTime
			}
		}
		if !isRunning && policy.MinStopSeconds > 0 && !tracker.lastStoppedAt.IsZero() && now.Sub(tracker.lastStoppedAt) < time.Duration(policy.MinStopSeconds)*time.Second {
			if denyWritableChannel(img, plan, ch.StartCommand, c.id, autonomyReasonMinStopTime) {
				blocked[ch.StartCommand] = autonomyReasonMinStopTime
			}
		}
	}

	c.lastBlockedReasons = blocked
	return nil
}

func denyWritableChannel(img ProcessImage, plan *ControlPlan, addr, controllerID, reason string) bool {
	if addr == "" {
		return false
	}
	snap, ok := img.Get(addr)
	if !ok || snap.Descriptor.Access == AccessReadOnly {
		return false
	}
	accepted, err := plan.Deny(addr, controllerID, reason)
	return err == nil && accepted
}

func chilledWaterFlowProven(img ProcessImage, pumps []PumpInterlockBinding) bool {
	for _, pump := range pumps {
		if faultChannelActive(img, pump.FaultChannel) {
			continue
		}
		if pump.RunState == "" && pump.FlowFeedback == "" {
			continue
		}
		if pump.RunState != "" {
			running, ok := runChannelActive(img, pump.RunState)
			if !ok || !running {
				continue
			}
		}
		if pump.FlowFeedback != "" {
			snap, ok := img.Get(pump.FlowFeedback)
			if !ok || !snap.HasValue || snap.Sample.Quality != QualityGood {
				continue
			}
			if snap.Sample.Value.Type == DataTypeBoolean {
				if !snap.Sample.Value.Boolean {
					continue
				}
			} else {
				num, numOK := snap.Sample.Value.NumericFloat64()
				if !numOK || !isFiniteFloat(num) {
					continue
				}
				if pump.MinFlowRate > 0 {
					if num < pump.MinFlowRate {
						continue
					}
				} else if num <= 0 {
					continue
				}
			}
		}
		return true
	}
	return false
}

func runChannelActive(img ProcessImage, addr string) (value bool, valid bool) {
	if addr == "" {
		return false, false
	}
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != QualityGood {
		return false, false
	}
	switch snap.Sample.Value.Type {
	case DataTypeBoolean:
		return snap.Sample.Value.Boolean, true
	case DataTypeString:
		switch strings.ToUpper(strings.TrimSpace(snap.Sample.Value.String)) {
		case "RUNNING", "RUN", "ON", "TRUE", "1":
			return true, true
		case "STOPPED", "STOP", "OFF", "IDLE", "STANDBY", "FALSE", "0", "":
			return false, true
		default:
			return false, false
		}
	default:
		if num, numOK := snap.Sample.Value.NumericFloat64(); numOK && isFiniteFloat(num) {
			return num > 0, true
		}
		return false, false
	}
}

func faultChannelActive(img ProcessImage, addr string) bool {
	if addr == "" {
		return false
	}
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != QualityGood {
		return false
	}
	if snap.Sample.Value.Type == DataTypeBoolean {
		return snap.Sample.Value.Boolean
	}
	if snap.Sample.Value.Type == DataTypeString {
		trimmed := strings.ToUpper(strings.TrimSpace(snap.Sample.Value.String))
		return trimmed != "" && trimmed != "NONE" && trimmed != "OK" && trimmed != "NORMAL" && trimmed != "NO_FAULT" && trimmed != "0" && trimmed != "FALSE"
	}
	if num, ok := snap.Sample.Value.NumericFloat64(); ok && isFiniteFloat(num) {
		return num > 0
	}
	return false
}

// ChillerFallbackTarget identifies chiller channels controlled by AutonomousFallbackController.
type ChillerFallbackTarget struct {
	ComponentID        string
	SupplySetpointAddr string
	RunStateAddr       string
	FaultAddr          string
}

// PumpFallbackTarget identifies pump channels controlled by AutonomousFallbackController.
type PumpFallbackTarget struct {
	ComponentID   string
	FrequencyAddr string
	RunStateAddr  string
	FaultAddr     string
}

// AutonomousFallbackConfig configures AutonomousFallbackController.
type AutonomousFallbackConfig struct {
	ID                         string
	Chillers                   []ChillerFallbackTarget
	Pumps                      []PumpFallbackTarget
	Policy                     AutonomyPolicyConfig
	CloudConnected             bool
	LastCloudContactAt         time.Time
	SetpointRampRateCPerSecond float64
	FrequencyRampRateHzPerSec  float64
}

// AutonomousFallbackStatus summarizes the fallback controller's live state.
type AutonomousFallbackStatus struct {
	Enabled            bool
	CloudConnected     bool
	FallbackActive     bool
	LastCloudContactAt time.Time
	OfflineDuration    time.Duration
}

// AutonomousFallbackController drives local chilled-water supply temperature and
// pump frequency fallback setpoints after the cloud offline grace period expires,
// skipping faulted units and yielding cleanly once cloud supervision reconnects.
type AutonomousFallbackController struct {
	mu               sync.Mutex
	id               string
	chillers         []ChillerFallbackTarget
	pumps            []PumpFallbackTarget
	policy           AutonomyPolicyConfig
	cloudConnected   bool
	lastContactAt    time.Time
	fallbackActive   bool
	lastRunAt        time.Time
	setpointRampRate float64
	freqRampRate     float64
	currentSetpoints map[string]float64
	currentFreqs     map[string]float64
}

// NewAutonomousFallbackController creates a validated offline fallback controller.
func NewAutonomousFallbackController(cfg AutonomousFallbackConfig) (*AutonomousFallbackController, error) {
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	if cfg.SetpointRampRateCPerSecond < 0 || !isFiniteFloat(cfg.SetpointRampRateCPerSecond) {
		return nil, errors.New("setpointRampRateCPerSecond must be finite and >= 0")
	}
	if cfg.FrequencyRampRateHzPerSec < 0 || !isFiniteFloat(cfg.FrequencyRampRateHzPerSec) {
		return nil, errors.New("frequencyRampRateHzPerSec must be finite and >= 0")
	}
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		id = DefaultAutonomousFallbackID
	}
	return &AutonomousFallbackController{
		id:               id,
		chillers:         slices.Clone(cfg.Chillers),
		pumps:            slices.Clone(cfg.Pumps),
		policy:           cfg.Policy,
		cloudConnected:   cfg.CloudConnected,
		lastContactAt:    cfg.LastCloudContactAt.UTC(),
		setpointRampRate: cfg.SetpointRampRateCPerSecond,
		freqRampRate:     cfg.FrequencyRampRateHzPerSec,
		currentSetpoints: make(map[string]float64),
		currentFreqs:     make(map[string]float64),
	}, nil
}

// ID returns the controller identifier.
func (c *AutonomousFallbackController) ID() string {
	return c.id
}

// UpdatePolicy hot-updates autonomy policy settings without resetting contact history.
func (c *AutonomousFallbackController) UpdatePolicy(policy AutonomyPolicyConfig) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policy = policy
	if !policy.Enabled {
		c.fallbackActive = false
		clear(c.currentSetpoints)
		clear(c.currentFreqs)
	}
	return nil
}

// UpdateTargets hot-updates target channel bindings when driver config shadow changes.
func (c *AutonomousFallbackController) UpdateTargets(chillers []ChillerFallbackTarget, pumps []PumpFallbackTarget) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chillers = slices.Clone(chillers)
	c.pumps = slices.Clone(pumps)
}

// RecordCloudContact records an explicit cloud heartbeat/command/telemetry exchange
// and marks cloud supervision online.
func (c *AutonomousFallbackController) RecordCloudContact(at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cloudConnected = true
	c.lastContactAt = at
	c.fallbackActive = false
	clear(c.currentSetpoints)
	clear(c.currentFreqs)
}

// SetCloudConnected updates the MQTT session connection status, resetting offline
// elapsed time on clean online -> offline transitions and yielding on reconnect.
func (c *AutonomousFallbackController) SetCloudConnected(connected bool, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if connected {
		c.cloudConnected = true
		c.lastContactAt = at
		c.fallbackActive = false
		clear(c.currentSetpoints)
		clear(c.currentFreqs)
		return
	}
	if c.cloudConnected || c.lastContactAt.IsZero() {
		c.lastContactAt = at
	}
	c.cloudConnected = false
}

// Status returns fallback state evaluated at time.Now().UTC().
func (c *AutonomousFallbackController) Status() AutonomousFallbackStatus {
	return c.StatusAt(time.Now().UTC())
}

// StatusAt returns fallback state evaluated at the given timestamp.
func (c *AutonomousFallbackController) StatusAt(at time.Time) AutonomousFallbackStatus {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var offline time.Duration
	if !c.cloudConnected && !c.lastContactAt.IsZero() && at.After(c.lastContactAt) {
		offline = at.Sub(c.lastContactAt)
	}
	return AutonomousFallbackStatus{
		Enabled:            c.policy.Enabled,
		CloudConnected:     c.cloudConnected,
		FallbackActive:     c.fallbackActive,
		LastCloudContactAt: c.lastContactAt,
		OfflineDuration:    offline,
	}
}

// Run evaluates offline grace and drives fallback setpoints while disconnected.
func (c *AutonomousFallbackController) Run(_ context.Context, img ProcessImage, plan *ControlPlan) error {
	if plan == nil {
		return errors.New("autonomous fallback control plan is nil")
	}

	now := img.At()
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dt := time.Duration(0)
	if !c.lastRunAt.IsZero() && now.After(c.lastRunAt) {
		dt = now.Sub(c.lastRunAt)
	}
	c.lastRunAt = now

	if c.lastContactAt.IsZero() {
		c.lastContactAt = now
	}
	if c.cloudConnected || !c.policy.Enabled {
		c.fallbackActive = false
		clear(c.currentSetpoints)
		clear(c.currentFreqs)
		return nil
	}

	offlineDuration := now.Sub(c.lastContactAt)
	if offlineDuration < c.policy.OfflineGrace() {
		c.fallbackActive = false
		clear(c.currentSetpoints)
		clear(c.currentFreqs)
		return nil
	}

	c.fallbackActive = true

	chillerTargets := make([]fallbackTargetSpec, len(c.chillers))
	for i, ch := range c.chillers {
		chillerTargets[i] = fallbackTargetSpec{targetAddr: ch.SupplySetpointAddr, faultAddr: ch.FaultAddr}
	}
	if err := applyFallbackTargets(img, plan, c.id, chillerTargets, c.currentSetpoints, c.policy.DefaultChilledWaterSupplySetpointC, c.setpointRampRate, dt, nil); err != nil {
		return err
	}

	targetFreq := clampFloat(c.policy.DefaultPumpFrequencyHz, c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz)
	pumpTargets := make([]fallbackTargetSpec, len(c.pumps))
	for i, pump := range c.pumps {
		pumpTargets[i] = fallbackTargetSpec{targetAddr: pump.FrequencyAddr, faultAddr: pump.FaultAddr}
	}
	clampFreq := func(v float64) float64 {
		return clampFloat(v, c.policy.MinPumpFrequencyHz, c.policy.MaxPumpFrequencyHz)
	}
	return applyFallbackTargets(img, plan, c.id, pumpTargets, c.currentFreqs, targetFreq, c.freqRampRate, dt, clampFreq)
}

type fallbackTargetSpec struct {
	targetAddr string
	faultAddr  string
}

func applyFallbackTargets(
	img ProcessImage,
	plan *ControlPlan,
	controllerID string,
	targets []fallbackTargetSpec,
	state map[string]float64,
	target float64,
	ratePerSec float64,
	dt time.Duration,
	clampFn func(float64) float64,
) error {
	for _, spec := range targets {
		if spec.targetAddr == "" || faultChannelActive(img, spec.faultAddr) {
			delete(state, spec.targetAddr)
			continue
		}
		snap, ok := img.Get(spec.targetAddr)
		if !ok || snap.Descriptor.DataType != DataTypeDouble || snap.Descriptor.Access == AccessReadOnly {
			continue
		}
		next, tracked := stepBumplessSetpoint(state, snap, spec.targetAddr, target, ratePerSec, dt)
		if clampFn != nil {
			next = clampFn(next)
			if tracked {
				state[spec.targetAddr] = next
			}
		}
		if _, _, err := plan.Request(spec.targetAddr, controllerID, DoubleValue(next)); err != nil {
			return err
		}
	}
	return nil
}

func stepBumplessSetpoint(state map[string]float64, snap ChannelSnapshot, addr string, target, ratePerSec float64, dt time.Duration) (float64, bool) {
	if ratePerSec <= 0 {
		state[addr] = target
		return target, true
	}
	cur, initialized := state[addr]
	if !initialized {
		if snap.HasValue && snap.Sample.Quality == QualityGood {
			if v, ok := snap.Sample.Value.NumericFloat64(); ok && isFiniteFloat(v) {
				state[addr] = v
				return v, true
			}
		}
		return target, false
	}
	if dt <= 0 {
		state[addr] = cur
		return cur, true
	}
	maxStep := ratePerSec * dt.Seconds()
	delta := target - cur
	if math.Abs(delta) <= maxStep {
		state[addr] = target
		return target, true
	}
	if delta > 0 {
		cur += maxStep
	} else {
		cur -= maxStep
	}
	state[addr] = cur
	return cur, true
}

func clampFloat(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}
