package fdd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

// Gate codes name the operating conditions a window minute must meet before its delta-T counts.
const (
	OperatingScheduleGate       = "OPERATING_SCHEDULE"
	ChillerRunningGate          = "CHILLER_RUNNING"
	LoadRatioGate               = "LOAD_RATIO"
	ChilledWaterPumpRunningGate = "CHILLED_WATER_PUMP_RUNNING"
)

// Why a gate the rule names did not apply to this Site's window.
const (
	SkippedNotInRule          = "NOT_IN_RULE"
	SkippedNoChillerMapped    = "NO_CHILLER_MAPPED"
	SkippedNoRatedCapacity    = "NO_RATED_CAPACITY"
	SkippedNoCoolingLoad      = "NO_COOLING_LOAD_READING"
	SkippedNoPumpMapped       = "NO_PUMP_MAPPED"
	runningBasisRatedPower    = "RATED_POWER"
	runningBasisRunState      = "RUN_STATE"
	coolingLoadKey            = "instant_cooling_capacity"
	powerTotalKey             = "power_total"
	runStateKey               = "run_state"
	defaultPumpRunningPowerKW = 0.5
	maximumHoldMinutes        = 60
	maximumOperatingGateBytes = 4096
)

// OperatingGates is the part of a rule revision that says when the loop counts as operating
// (spec docs/specs/fdd-rule-definition.md). A gate the revision leaves out is not applied; a
// gate it names applies only where the Site's registry maps what the gate reads.
type OperatingGates struct {
	// MinimumOperatingShare of the window's minutes must be operating, or the loop is NOT_OPERATING.
	MinimumOperatingShare float64 `json:"minimumOperatingShare"`
	// MinimumLowShare of the operating minutes must be below the threshold for a finding.
	MinimumLowShare float64 `json:"minimumLowShare"`
	// TemperatureHoldMinutes is how long a supply or return reading stands for later minutes.
	TemperatureHoldMinutes int `json:"temperatureHoldMinutes"`
	// GateHoldMinutes is how long a power, run-state or cooling-load reading stands for later minutes.
	GateHoldMinutes         int                      `json:"gateHoldMinutes"`
	ChillerRunning          *ChillerRunningCondition `json:"chillerRunning,omitempty"`
	LoadRatio               *LoadRatioCondition      `json:"loadRatio,omitempty"`
	ChilledWaterPumpRunning *PumpRunningCondition    `json:"chilledWaterPumpRunning,omitempty"`
}

// ChillerRunningCondition: at least one chiller runs. A chiller with a rated power runs when its
// power is above this fraction of it; one without falls back to its run state.
type ChillerRunningCondition struct {
	MinimumRatedPowerFraction float64 `json:"minimumRatedPowerFraction"`
}

// LoadRatioCondition: the loop's cooling load over the rated capacity of the running chillers.
type LoadRatioCondition struct {
	Minimum float64 `json:"minimum"`
}

// PumpRunningCondition: at least one chilled-water pump reports RUNNING.
type PumpRunningCondition struct{}

// ParseOperatingGates reads a revision's gates strictly; an unknown field is an error, not ignored.
func ParseOperatingGates(raw []byte) (OperatingGates, error) {
	if len(raw) == 0 || len(raw) > maximumOperatingGateBytes {
		return OperatingGates{}, errors.New("operating gates are missing or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var gates OperatingGates
	if err := decoder.Decode(&gates); err != nil {
		return OperatingGates{}, fmt.Errorf("decode operating gates: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return OperatingGates{}, errors.New("operating gates contain trailing JSON")
	}
	return gates, gates.Validate()
}

func (gates OperatingGates) Validate() error {
	share := func(value float64) bool { return value > 0 && value <= 1 }
	fraction := func(value float64) bool { return value > 0 && value < 1 }
	switch {
	case !share(gates.MinimumOperatingShare):
		return errors.New("minimumOperatingShare must be in (0, 1]")
	case !share(gates.MinimumLowShare):
		return errors.New("minimumLowShare must be in (0, 1]")
	case gates.TemperatureHoldMinutes < 0 || gates.TemperatureHoldMinutes > maximumHoldMinutes:
		return errors.New("temperatureHoldMinutes must be between 0 and 60")
	case gates.GateHoldMinutes < 0 || gates.GateHoldMinutes > maximumHoldMinutes:
		return errors.New("gateHoldMinutes must be between 0 and 60")
	case gates.ChillerRunning != nil && !fraction(gates.ChillerRunning.MinimumRatedPowerFraction):
		return errors.New("chillerRunning.minimumRatedPowerFraction must be in (0, 1)")
	case gates.LoadRatio != nil && !fraction(gates.LoadRatio.Minimum):
		return errors.New("loadRatio.minimum must be in (0, 1)")
	case gates.LoadRatio != nil && gates.ChillerRunning == nil:
		return errors.New("loadRatio needs chillerRunning: it divides by the running chillers' capacity")
	}
	return nil
}

// PointRef names a single telemetry series by reporting device and point_code (telemetry_key).
type PointRef struct {
	DeviceID string
	Key      string
}

// LoopSignals holds the resolved sensor points for a chilled-water loop's supply, return, and cooling load.
type LoopSignals struct {
	SupplyTemp  []PointRef
	ReturnTemp  []PointRef
	CoolingLoad []PointRef
}

// Topology is what the Site's registry maps for the gates: its loop points, chillers and
// chilled-water pumps and the devices/points that report for them.
type Topology struct {
	Loop           LoopSignals
	Chillers       []GateAsset
	Pumps          []GateAsset
	ResolvedByTwin bool
}

// GateAsset is one active asset with the devices and semantic points bound to it; rated values are kW, 0 when unknown.
type GateAsset struct {
	AssetID                string
	DeviceIDs              []string
	PowerPoints            []PointRef
	RunStatePoints         []PointRef
	RatedPowerKW           float64
	RatedCapacityKW        float64
	FeederAuxiliaryPowerKW float64
}

// DeviceIDs are the devices whose power and run state the gates read.
func (topology Topology) DeviceIDs() []string {
	seen := map[string]bool{}
	var devices []string
	addDevice := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			devices = append(devices, id)
		}
	}
	for _, asset := range append(append([]GateAsset(nil), topology.Chillers...), topology.Pumps...) {
		for _, device := range asset.DeviceIDs {
			addDevice(device)
		}
		for _, ref := range asset.PowerPoints {
			addDevice(ref.DeviceID)
		}
		for _, ref := range asset.RunStatePoints {
			addDevice(ref.DeviceID)
		}
	}
	sort.Strings(devices)
	return devices
}

// PointRefs returns every resolved (device_id, telemetry_key) pair required for loop and gate assessment.
func (topology Topology) PointRefs() []PointRef {
	seen := map[PointRef]bool{}
	var refs []PointRef
	add := func(list []PointRef) {
		for _, ref := range list {
			if ref.DeviceID == "" || ref.Key == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	add(topology.Loop.SupplyTemp)
	add(topology.Loop.ReturnTemp)
	add(topology.Loop.CoolingLoad)
	for _, asset := range topology.Chillers {
		add(effectivePowerPoints(asset, topology.ResolvedByTwin))
		add(effectiveRunStatePoints(asset, topology.ResolvedByTwin))
	}
	for _, asset := range topology.Pumps {
		add(effectiveRunStatePoints(asset, topology.ResolvedByTwin))
		add(effectivePowerPoints(asset, topology.ResolvedByTwin))
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].DeviceID != refs[j].DeviceID {
			return refs[i].DeviceID < refs[j].DeviceID
		}
		return refs[i].Key < refs[j].Key
	})
	return refs
}

// Sample is a device's last authoritative observation of a key within one minute.
type Sample struct {
	DeviceID      string
	Key           string
	Minute        time.Time
	ObservationID string
	PointType     string
	ValueType     string
	Unit          string
	Quality       string
	Number        float64
	HasNumber     bool
	Text          string
}

// GateOutcome reports one gate over the window: whether it applied, why not, on what basis and
// in how many minutes it passed.
type GateOutcome struct {
	Gate          string   `json:"gate"`
	Applied       bool     `json:"applied"`
	SkippedReason string   `json:"skippedReason,omitempty"`
	Bases         []string `json:"bases,omitempty"`
	PassedMinutes int      `json:"passedMinutes"`
}

// WindowAssessment is the window's statistics under the rule's gates.
type WindowAssessment struct {
	Status             string        `json:"-"`
	Minutes            int           `json:"minutes"`
	TemperatureMinutes int           `json:"temperatureMinutes"`
	OperatingMinutes   int           `json:"operatingMinutes"`
	LowMinutes         int           `json:"lowMinutes"`
	OperatingShare     float64       `json:"operatingShare"`
	LowShare           float64       `json:"lowShare"`
	MeanDeltaTC        float64       `json:"meanDeltaTC"`
	Suppressed         bool          `json:"suppressed,omitempty"`
	ScheduleState      string        `json:"scheduleState,omitempty"`
	SuppressionReason  string        `json:"suppressionReason,omitempty"`
	SuppressionDetail  string        `json:"suppressionDetail,omitempty"`
	Gates              []GateOutcome `json:"gates"`
	LowEvidenceIDs     []string      `json:"-"`
}

// Window statuses before the finding store decides FINDING or ONGOING.
const (
	StatusClear        = "CLEAR"
	StatusNotOperating = "NOT_OPERATING"
	statusLow          = "LOW"
)

// WindowMinutes are the whole minutes of a rule's window that end at or before the evaluation.
func WindowMinutes(rule RuleRevision, at time.Time) (from, to time.Time) {
	to = at.UTC().Truncate(time.Minute)
	return to.Add(-time.Duration(rule.WindowMinutes) * time.Minute), to
}

type series map[string][]Sample

func (index series) at(device, key string, minute time.Time, holdMinutes int, valid func(Sample) bool) (Sample, bool) {
	samples := index[device+"\x00"+key]
	position := sort.Search(len(samples), func(i int) bool { return samples[i].Minute.After(minute) }) - 1
	if position < 0 {
		return Sample{}, false
	}
	sample := samples[position]
	if minute.Sub(sample.Minute) > time.Duration(holdMinutes)*time.Minute || !valid(sample) {
		return Sample{}, false
	}
	return sample, true
}

func (index series) atAny(refs []PointRef, minute time.Time, holdMinutes int, valid func(Sample) bool) (Sample, bool) {
	for _, ref := range refs {
		if sample, ok := index.at(ref.DeviceID, ref.Key, minute, holdMinutes, valid); ok {
			return sample, true
		}
	}
	return Sample{}, false
}

func (index series) hasAnySeries(refs []PointRef) bool {
	for _, ref := range refs {
		if len(index[ref.DeviceID+"\x00"+ref.Key]) > 0 {
			return true
		}
	}
	return false
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validTemperature(sample Sample) bool {
	return sample.PointType == "TELEMETRY" && sample.Quality == "GOOD" && sample.Unit == "Cel" && sample.ValueType == "NUMBER" && sample.HasNumber && finite(sample.Number)
}

func validKilowatts(sample Sample) bool {
	return sample.Quality == "GOOD" && sample.Unit == "kW" && sample.ValueType == "NUMBER" && sample.HasNumber && finite(sample.Number) && sample.Number >= 0
}

func validRunState(sample Sample) bool {
	return sample.Quality == "GOOD" && sample.ValueType == "STRING" && (sample.Text == "RUNNING" || sample.Text == "STOPPED")
}

// AssessWindow judges a loop's window under its rule from the window's samples alone, so a live
// evaluation and a replay of history reach the same answer. It errs only when no minute of the
// window has both temperatures.
func AssessWindow(rule RuleRevision, topology Topology, loopDeviceID string, samples []Sample, at time.Time) (WindowAssessment, error) {
	gates := rule.OperatingGates
	index := series{}
	for _, sample := range samples {
		key := sample.DeviceID + "\x00" + sample.Key
		index[key] = append(index[key], sample)
	}
	for key := range index {
		sort.Slice(index[key], func(i, j int) bool { return index[key][i].Minute.Before(index[key][j].Minute) })
	}
	from, _ := WindowMinutes(rule, at)

	supplyRefs := topology.Loop.SupplyTemp
	returnRefs := topology.Loop.ReturnTemp
	coolingLoadRefs := topology.Loop.CoolingLoad
	if !topology.ResolvedByTwin {
		if len(supplyRefs) == 0 && loopDeviceID != "" {
			supplyRefs = []PointRef{{DeviceID: loopDeviceID, Key: supplyTemperatureKey}}
		}
		if len(returnRefs) == 0 && loopDeviceID != "" {
			returnRefs = []PointRef{{DeviceID: loopDeviceID, Key: returnTemperatureKey}}
		}
		if len(coolingLoadRefs) == 0 && loopDeviceID != "" {
			coolingLoadRefs = []PointRef{{DeviceID: loopDeviceID, Key: coolingLoadKey}}
		}
	}

	chillerOutcome := GateOutcome{Gate: ChillerRunningGate}
	loadOutcome := GateOutcome{Gate: LoadRatioGate}
	pumpOutcome := GateOutcome{Gate: ChilledWaterPumpRunningGate}
	chillers := mappedAssets(topology.Chillers, topology.ResolvedByTwin)
	pumps := mappedAssets(topology.Pumps, topology.ResolvedByTwin)
	switch {
	case gates.ChillerRunning == nil:
		chillerOutcome.SkippedReason = SkippedNotInRule
	case len(chillers) == 0:
		chillerOutcome.SkippedReason = SkippedNoChillerMapped
	default:
		chillerOutcome.Applied = true
	}
	switch {
	case gates.LoadRatio == nil:
		loadOutcome.SkippedReason = SkippedNotInRule
	case !chillerOutcome.Applied:
		loadOutcome.SkippedReason = SkippedNoChillerMapped
	case !allRated(chillers):
		loadOutcome.SkippedReason = SkippedNoRatedCapacity
	case !index.hasAnySeries(coolingLoadRefs):
		loadOutcome.SkippedReason = SkippedNoCoolingLoad
	default:
		loadOutcome.Applied = true
	}
	switch {
	case gates.ChilledWaterPumpRunning == nil:
		pumpOutcome.SkippedReason = SkippedNotInRule
	case len(pumps) == 0:
		pumpOutcome.SkippedReason = SkippedNoPumpMapped
	default:
		pumpOutcome.Applied = true
	}

	assessment := WindowAssessment{Minutes: rule.WindowMinutes}
	bases := map[string]bool{}
	seenEvidence := map[string]bool{}
	var operatingSum, temperatureSum float64
	for offset := 0; offset < rule.WindowMinutes; offset++ {
		minute := from.Add(time.Duration(offset) * time.Minute)
		supply, hasSupply := index.atAny(supplyRefs, minute, gates.TemperatureHoldMinutes, validTemperature)
		ret, hasReturn := index.atAny(returnRefs, minute, gates.TemperatureHoldMinutes, validTemperature)
		if !hasSupply || !hasReturn {
			continue
		}
		deltaT := ret.Number - supply.Number
		assessment.TemperatureMinutes++
		temperatureSum += deltaT
		operating := true
		var runningCapacity float64
		if chillerOutcome.Applied {
			running := false
			for _, chiller := range chillers {
				on, basis := chillerRunning(index, chiller, minute, gates, topology.ResolvedByTwin)
				if basis != "" {
					bases[basis] = true
				}
				if on {
					running = true
					runningCapacity += chiller.RatedCapacityKW
				}
			}
			if running {
				chillerOutcome.PassedMinutes++
			}
			operating = operating && running
		}
		if loadOutcome.Applied {
			load, ok := index.atAny(coolingLoadRefs, minute, gates.GateHoldMinutes, validKilowatts)
			passed := ok && runningCapacity > 0 && load.Number/runningCapacity >= gates.LoadRatio.Minimum
			if passed {
				loadOutcome.PassedMinutes++
			}
			operating = operating && passed
		}
		if pumpOutcome.Applied {
			running := false
			for _, pump := range pumps {
				if pumpRunning(index, pump, minute, gates, topology.ResolvedByTwin) {
					running = true
				}
			}
			if running {
				pumpOutcome.PassedMinutes++
			}
			operating = operating && running
		}
		if !operating {
			continue
		}
		assessment.OperatingMinutes++
		operatingSum += deltaT
		if deltaT < rule.MinimumDeltaTC {
			assessment.LowMinutes++
			for _, id := range []string{supply.ObservationID, ret.ObservationID} {
				if !seenEvidence[id] {
					seenEvidence[id] = true
					assessment.LowEvidenceIDs = append(assessment.LowEvidenceIDs, id)
				}
			}
		}
	}
	for basis := range bases {
		chillerOutcome.Bases = append(chillerOutcome.Bases, basis)
	}
	sort.Strings(chillerOutcome.Bases)
	assessment.Gates = []GateOutcome{chillerOutcome, loadOutcome, pumpOutcome}
	if assessment.TemperatureMinutes == 0 {
		return assessment, ErrNoLoopEvidence
	}
	assessment.OperatingShare = float64(assessment.OperatingMinutes) / float64(rule.WindowMinutes)
	if assessment.OperatingMinutes > 0 {
		assessment.MeanDeltaTC = operatingSum / float64(assessment.OperatingMinutes)
		assessment.LowShare = float64(assessment.LowMinutes) / float64(assessment.OperatingMinutes)
	} else {
		assessment.MeanDeltaTC = temperatureSum / float64(assessment.TemperatureMinutes)
	}
	switch {
	case assessment.OperatingShare < gates.MinimumOperatingShare:
		assessment.Status = StatusNotOperating
	case assessment.LowShare >= gates.MinimumLowShare:
		assessment.Status = statusLow
	default:
		assessment.Status = StatusClear
	}
	return assessment, nil
}

func mappedAssets(assets []GateAsset, resolvedByTwin bool) []GateAsset {
	var mapped []GateAsset
	for _, asset := range assets {
		if len(effectivePowerPoints(asset, resolvedByTwin)) > 0 || len(effectiveRunStatePoints(asset, resolvedByTwin)) > 0 {
			mapped = append(mapped, asset)
		}
	}
	return mapped
}

func effectivePowerPoints(asset GateAsset, resolvedByTwin bool) []PointRef {
	if len(asset.PowerPoints) > 0 || resolvedByTwin {
		return asset.PowerPoints
	}
	refs := make([]PointRef, 0, len(asset.DeviceIDs))
	for _, device := range asset.DeviceIDs {
		refs = append(refs, PointRef{DeviceID: device, Key: powerTotalKey})
	}
	return refs
}

func effectiveRunStatePoints(asset GateAsset, resolvedByTwin bool) []PointRef {
	if len(asset.RunStatePoints) > 0 || resolvedByTwin {
		return asset.RunStatePoints
	}
	refs := make([]PointRef, 0, len(asset.DeviceIDs))
	for _, device := range asset.DeviceIDs {
		refs = append(refs, PointRef{DeviceID: device, Key: runStateKey})
	}
	return refs
}

func allRated(chillers []GateAsset) bool {
	for _, chiller := range chillers {
		if chiller.RatedCapacityKW <= 0 {
			return false
		}
	}
	return len(chillers) > 0
}

// chillerRunning judges one chiller in one minute: by power against its rated power when both are
// known, otherwise by the run state its bound points report.
func chillerRunning(index series, chiller GateAsset, minute time.Time, gates OperatingGates, resolvedByTwin bool) (bool, string) {
	if chiller.RatedPowerKW > 0 {
		if power, ok := index.atAny(effectivePowerPoints(chiller, resolvedByTwin), minute, gates.GateHoldMinutes, validKilowatts); ok {
			return power.Number > math.Max(0, chiller.FeederAuxiliaryPowerKW)+gates.ChillerRunning.MinimumRatedPowerFraction*chiller.RatedPowerKW, runningBasisRatedPower
		}
	}
	if state, ok := index.atAny(effectiveRunStatePoints(chiller, resolvedByTwin), minute, gates.GateHoldMinutes, validRunState); ok {
		return state.Text == "RUNNING", runningBasisRunState
	}
	return false, ""
}

func pumpRunning(index series, pump GateAsset, minute time.Time, gates OperatingGates, resolvedByTwin bool) bool {
	if state, ok := index.atAny(effectiveRunStatePoints(pump, resolvedByTwin), minute, gates.GateHoldMinutes, validRunState); ok {
		return state.Text == "RUNNING"
	}
	if power, ok := index.atAny(effectivePowerPoints(pump, resolvedByTwin), minute, gates.GateHoldMinutes, validKilowatts); ok {
		if pump.RatedPowerKW > 0 {
			fraction := 0.1
			if gates.ChillerRunning != nil {
				fraction = gates.ChillerRunning.MinimumRatedPowerFraction
			}
			return power.Number > math.Max(0, pump.FeederAuxiliaryPowerKW)+fraction*pump.RatedPowerKW
		}
		return power.Number > defaultPumpRunningPowerKW
	}
	return false
}
