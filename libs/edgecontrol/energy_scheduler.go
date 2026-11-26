package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultEnergySchedulerID is the canonical controller ID for EnergyScheduleController.
	DefaultEnergySchedulerID = "energy-scheduler"
	maxScheduleSlots         = 288
)

// ScheduleOperatingMode identifies HVAC optimization time-slot modes executed locally.
type ScheduleOperatingMode string

const (
	ScheduleModeNormal    ScheduleOperatingMode = "NORMAL"
	ScheduleModeEco       ScheduleOperatingMode = "ECO"
	ScheduleModePreCool   ScheduleOperatingMode = "PRE_COOL"
	ScheduleModePeakShave ScheduleOperatingMode = "PEAK_SHAVE"
	ScheduleModeStandby   ScheduleOperatingMode = "STANDBY"
)

// Valid reports whether mode is a recognized HVAC schedule operating mode.
func (m ScheduleOperatingMode) Valid() bool {
	switch m {
	case ScheduleModeNormal, ScheduleModeEco, ScheduleModePreCool, ScheduleModePeakShave, ScheduleModeStandby:
		return true
	default:
		return false
	}
}

// EnergyScheduleSlot specifies one non-overlapping daily time window and its setpoints/bounds.
type EnergyScheduleSlot struct {
	SlotID              string                `json:"slotId"`
	StartMinute         int                   `json:"startMinute"`
	EndMinute           int                   `json:"endMinute"`
	Mode                ScheduleOperatingMode `json:"mode"`
	SupplyTempSetpointC *float64              `json:"supplyTempSetpointC,omitempty"`
	PumpFrequencyMinHz  *float64              `json:"pumpFrequencyMinHz,omitempty"`
	PumpFrequencyMaxHz  *float64              `json:"pumpFrequencyMaxHz,omitempty"`
	TowerFanSpeedMinPct *float64              `json:"towerFanSpeedMinPct,omitempty"`
	TowerFanSpeedMaxPct *float64              `json:"towerFanSpeedMaxPct,omitempty"`
	MaxRunningChillers  *int                  `json:"maxRunningChillers,omitempty"`
	TargetCoolingLoadKW *float64              `json:"targetCoolingLoadKw,omitempty"`
}

// EdgeEnergySchedule represents a versioned cloud-dispatched HVAC energy schedule
// executed on the edge with explicit lease validity and offline fallback semantics.
type EdgeEnergySchedule struct {
	ScheduleID                string               `json:"scheduleId"`
	Version                   int64                `json:"version"`
	IssuedAt                  time.Time            `json:"issuedAt"`
	ValidFrom                 time.Time            `json:"validFrom"`
	ValidUntil                time.Time            `json:"validUntil"`
	LeaseExpiresAt            time.Time            `json:"leaseExpiresAt,omitempty"`
	ContinueOfflineWhileValid bool                 `json:"continueOfflineWhileValid"`
	Slots                     []EnergyScheduleSlot `json:"slots"`
}

// Validate enforces structural, temporal, and safety bounds invariants on EdgeEnergySchedule.
func (s EdgeEnergySchedule) Validate() error {
	if strings.TrimSpace(s.ScheduleID) == "" {
		return errors.New("energy schedule requires scheduleId")
	}
	if s.Version < 1 {
		return fmt.Errorf("energy schedule version %d must be >= 1", s.Version)
	}
	if s.ValidFrom.IsZero() || s.ValidUntil.IsZero() || !s.ValidUntil.After(s.ValidFrom) {
		return errors.New("energy schedule requires validFrom < validUntil")
	}
	if len(s.Slots) == 0 || len(s.Slots) > maxScheduleSlots {
		return fmt.Errorf("energy schedule slots count %d must be in [1,%d]", len(s.Slots), maxScheduleSlots)
	}

	sorted := make([]EnergyScheduleSlot, len(s.Slots))
	copy(sorted, s.Slots)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartMinute != sorted[j].StartMinute {
			return sorted[i].StartMinute < sorted[j].StartMinute
		}
		return sorted[i].EndMinute < sorted[j].EndMinute
	})

	seenIDs := make(map[string]struct{}, len(sorted))
	for i, slot := range sorted {
		sid := strings.TrimSpace(slot.SlotID)
		if sid == "" {
			return fmt.Errorf("energy schedule slot[%d] requires slotId", i)
		}
		if _, dup := seenIDs[sid]; dup {
			return fmt.Errorf("duplicate energy schedule slotId %q", sid)
		}
		seenIDs[sid] = struct{}{}
		if !slot.Mode.Valid() {
			return fmt.Errorf("energy schedule slot %s unsupported mode %q", sid, slot.Mode)
		}
		if slot.StartMinute < 0 || slot.EndMinute > 1440 || slot.StartMinute >= slot.EndMinute {
			return fmt.Errorf("energy schedule slot %s window [%d,%d) must lie within [0,1440]", sid, slot.StartMinute, slot.EndMinute)
		}
		if i > 0 && slot.StartMinute < sorted[i-1].EndMinute {
			return fmt.Errorf("energy schedule slot %s overlaps preceding slot %s", sid, sorted[i-1].SlotID)
		}
		if slot.SupplyTempSetpointC != nil && (!isFiniteFloat(*slot.SupplyTempSetpointC) || *slot.SupplyTempSetpointC < 4 || *slot.SupplyTempSetpointC > 18) {
			return fmt.Errorf("energy schedule slot %s supplyTempSetpointC must be in [4,18]", sid)
		}
		if err := validateOptionalRange(slot.PumpFrequencyMinHz, slot.PumpFrequencyMaxHz, 15, 50, "pumpFrequencyHz", sid); err != nil {
			return err
		}
		if err := validateOptionalRange(slot.TowerFanSpeedMinPct, slot.TowerFanSpeedMaxPct, 15, 100, "towerFanSpeedPct", sid); err != nil {
			return err
		}
		if slot.MaxRunningChillers != nil && (*slot.MaxRunningChillers < 0 || *slot.MaxRunningChillers > 16) {
			return fmt.Errorf("energy schedule slot %s maxRunningChillers must be in [0,16]", sid)
		}
		if slot.TargetCoolingLoadKW != nil && (!isFiniteFloat(*slot.TargetCoolingLoadKW) || *slot.TargetCoolingLoadKW < 0) {
			return fmt.Errorf("energy schedule slot %s targetCoolingLoadKw must be finite and >= 0", sid)
		}
	}
	return nil
}

func validateOptionalRange(minPtr, maxPtr *float64, lo, hi float64, label, slotID string) error {
	if minPtr != nil && (!isFiniteFloat(*minPtr) || *minPtr < lo || *minPtr > hi) {
		return fmt.Errorf("energy schedule slot %s %s min must be in [%v,%v]", slotID, label, lo, hi)
	}
	if maxPtr != nil && (!isFiniteFloat(*maxPtr) || *maxPtr < lo || *maxPtr > hi) {
		return fmt.Errorf("energy schedule slot %s %s max must be in [%v,%v]", slotID, label, lo, hi)
	}
	if minPtr != nil && maxPtr != nil && *minPtr > *maxPtr {
		return fmt.Errorf("energy schedule slot %s %s min (%v) > max (%v)", slotID, label, *minPtr, *maxPtr)
	}
	return nil
}

// Clone returns a deep copy of EdgeEnergySchedule (nil-safe).
func (s *EdgeEnergySchedule) Clone() *EdgeEnergySchedule {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Slots = make([]EnergyScheduleSlot, len(s.Slots))
	for i, slot := range s.Slots {
		copied := slot
		copied.SupplyTempSetpointC = cloneFloatPtr(slot.SupplyTempSetpointC)
		copied.PumpFrequencyMinHz = cloneFloatPtr(slot.PumpFrequencyMinHz)
		copied.PumpFrequencyMaxHz = cloneFloatPtr(slot.PumpFrequencyMaxHz)
		copied.TowerFanSpeedMinPct = cloneFloatPtr(slot.TowerFanSpeedMinPct)
		copied.TowerFanSpeedMaxPct = cloneFloatPtr(slot.TowerFanSpeedMaxPct)
		copied.TargetCoolingLoadKW = cloneFloatPtr(slot.TargetCoolingLoadKW)
		if slot.MaxRunningChillers != nil {
			v := *slot.MaxRunningChillers
			copied.MaxRunningChillers = &v
		}
		cp.Slots[i] = copied
	}
	return &cp
}

func cloneFloatPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// EnergyScheduleStatus reports live schedule evaluation and fallback state.
type EnergyScheduleStatus struct {
	ScheduleID     string
	Version        int64
	Active         bool
	ActiveSlotID   string
	Mode           ScheduleOperatingMode
	FallbackReason string
}

// EnergyScheduleController executes versioned cloud energy schedules locally on
// the edge and relinquishes cleanly to local autonomy fallback when leases expire.
type EnergyScheduleController struct {
	mu                    sync.Mutex
	id                    string
	schedule              *EdgeEnergySchedule
	cloudConnected        bool
	chillers              []ChillerFallbackTarget
	pumps                 []PumpFallbackTarget
	towers                []ClusterTowerBinding
	modelSetpointProvider func(ProcessImage) (float64, bool)
}

// NewEnergyScheduleController creates a versioned schedule execution controller.
func NewEnergyScheduleController(id string) *EnergyScheduleController {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		trimmed = DefaultEnergySchedulerID
	}
	return &EnergyScheduleController{
		id:             trimmed,
		cloudConnected: true,
	}
}

// ID returns the controller identifier.
func (c *EnergyScheduleController) ID() string {
	return c.id
}

// UpdateSchedule installs or updates the active schedule, rejecting version downgrades for the same ScheduleID.
func (c *EnergyScheduleController) UpdateSchedule(schedule *EdgeEnergySchedule) error {
	if schedule == nil {
		c.mu.Lock()
		c.schedule = nil
		c.mu.Unlock()
		return nil
	}
	if err := schedule.Validate(); err != nil {
		return err
	}
	cloned := schedule.Clone()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.schedule != nil && c.schedule.ScheduleID == cloned.ScheduleID && cloned.Version < c.schedule.Version {
		return fmt.Errorf("energy schedule %s version downgrade %d < %d", cloned.ScheduleID, cloned.Version, c.schedule.Version)
	}
	c.schedule = cloned
	return nil
}

// SetCloudConnected notifies the schedule controller of MQTT cloud connectivity state.
func (c *EnergyScheduleController) SetCloudConnected(connected bool, _ time.Time) {
	c.mu.Lock()
	c.cloudConnected = connected
	c.mu.Unlock()
}

// UpdateTargets refreshes bound chillers, pumps, and cooling towers.
func (c *EnergyScheduleController) UpdateTargets(chillers []ChillerFallbackTarget, pumps []PumpFallbackTarget, towers []ClusterTowerBinding) {
	c.mu.Lock()
	c.chillers = slices.Clone(chillers)
	c.pumps = slices.Clone(pumps)
	c.towers = slices.Clone(towers)
	c.mu.Unlock()
}

// SetModelSetpointProvider configures an optional Edge AI setpoint provider
// consulted when an active schedule slot does not fix SupplyTempSetpointC.
func (c *EnergyScheduleController) SetModelSetpointProvider(fn func(ProcessImage) (float64, bool)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.modelSetpointProvider = fn
	c.mu.Unlock()
}

// IsActiveAt reports whether a valid schedule slot governs execution at at.
func (c *EnergyScheduleController) IsActiveAt(at time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, st := c.evaluateSlotLocked(at)
	return st.Active
}

// ActiveOverrideAt returns the cluster coordination override for at, or nil when inactive.
func (c *EnergyScheduleController) ActiveOverrideAt(at time.Time) *ClusterScheduleOverride {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, st := c.evaluateSlotLocked(at)
	if !st.Active || slot == nil {
		return nil
	}
	return &ClusterScheduleOverride{
		Mode:                slot.Mode,
		SupplyTempSetpointC: cloneFloatPtr(slot.SupplyTempSetpointC),
		MaxRunningChillers:  cloneIntPtr(slot.MaxRunningChillers),
		TargetCoolingLoadKW: cloneFloatPtr(slot.TargetCoolingLoadKW),
	}
}

// StatusAt returns the full evaluation status for timestamp at.
func (c *EnergyScheduleController) StatusAt(at time.Time) EnergyScheduleStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, st := c.evaluateSlotLocked(at)
	return st
}

// Run applies active schedule bounds (pump/tower limits) and baseline setpoints onto ControlPlan.
func (c *EnergyScheduleController) Run(_ context.Context, img ProcessImage, plan *ControlPlan) error {
	if plan == nil {
		return errors.New("energy schedule controller requires ControlPlan")
	}
	now := img.At().UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	c.mu.Lock()
	slot, st := c.evaluateSlotLocked(now)
	chillers := slices.Clone(c.chillers)
	pumps := slices.Clone(c.pumps)
	towers := slices.Clone(c.towers)
	modelProvider := c.modelSetpointProvider
	c.mu.Unlock()

	if !st.Active || slot == nil {
		return nil
	}

	reason := "schedule:" + st.ActiveSlotID
	if slot.PumpFrequencyMinHz != nil || slot.PumpFrequencyMaxHz != nil {
		for _, pump := range pumps {
			constrainDoubleWritable(img, plan, pump.FrequencyAddr, c.id, reason, slot.PumpFrequencyMinHz, slot.PumpFrequencyMaxHz)
		}
	}
	if slot.TowerFanSpeedMinPct != nil || slot.TowerFanSpeedMaxPct != nil {
		for _, tower := range towers {
			constrainDoubleWritable(img, plan, tower.FanSpeedAddr, c.id, reason, slot.TowerFanSpeedMinPct, slot.TowerFanSpeedMaxPct)
		}
	}
	var targetSetpoint *float64
	if slot.SupplyTempSetpointC != nil {
		sp := *slot.SupplyTempSetpointC
		targetSetpoint = &sp
	} else if slot.Mode != ScheduleModeStandby && modelProvider != nil {
		if sp, ok := modelProvider(img); ok && isFiniteFloat(sp) {
			clamped := clampFloat(sp, 4, 18)
			targetSetpoint = &clamped
		}
	}
	if targetSetpoint != nil {
		for _, ch := range chillers {
			if faultChannelActive(img, ch.FaultAddr) {
				continue
			}
			_ = reqDoubleWritable(img, plan, ch.SupplySetpointAddr, c.id, *targetSetpoint)
		}
	}
	return nil
}

func (c *EnergyScheduleController) evaluateSlotLocked(at time.Time) (*EnergyScheduleSlot, EnergyScheduleStatus) {
	if c.schedule == nil {
		return nil, EnergyScheduleStatus{FallbackReason: "NO_SCHEDULE"}
	}
	now := at.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	st := EnergyScheduleStatus{
		ScheduleID: c.schedule.ScheduleID,
		Version:    c.schedule.Version,
	}
	if now.Before(c.schedule.ValidFrom.UTC()) || !now.Before(c.schedule.ValidUntil.UTC()) {
		st.FallbackReason = "SCHEDULE_EXPIRED"
		return nil, st
	}
	if !c.schedule.LeaseExpiresAt.IsZero() && now.After(c.schedule.LeaseExpiresAt.UTC()) {
		st.FallbackReason = "LEASE_EXPIRED"
		return nil, st
	}
	if !c.cloudConnected && !c.schedule.ContinueOfflineWhileValid {
		st.FallbackReason = "CLOUD_DISCONNECTED_NO_OFFLINE_CONTINUATION"
		return nil, st
	}

	minuteOfDay := now.Hour()*60 + now.Minute()
	for i := range c.schedule.Slots {
		slot := &c.schedule.Slots[i]
		if minuteOfDay >= slot.StartMinute && minuteOfDay < slot.EndMinute {
			st.Active = true
			st.ActiveSlotID = slot.SlotID
			st.Mode = slot.Mode
			return slot, st
		}
	}
	st.FallbackReason = "NO_MATCHING_SLOT"
	return nil, st
}

func constrainDoubleWritable(img ProcessImage, plan *ControlPlan, addr, controllerID, reason string, minPtr, maxPtr *float64) {
	if addr == "" || (minPtr == nil && maxPtr == nil) {
		return
	}
	snap, ok := img.Get(addr)
	if !ok || snap.Descriptor.Access == AccessReadOnly || snap.Descriptor.DataType != DataTypeDouble || IsHardRealtimeChannel(snap.Descriptor) {
		return
	}
	_, _ = plan.ConstrainNumber(addr, controllerID, reason, minPtr, maxPtr)
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
