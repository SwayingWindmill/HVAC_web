package telemetry

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/nameplate"
)

// Point health is the status a person sees for one point: the latest check,
// then overwritten to STALE when the displayed sample itself is no longer fresh.
const (
	HealthHealthy = "HEALTHY"
	HealthSuspect = "SUSPECT"
	HealthBad     = "BAD"
	HealthStale   = "STALE"
)

// A sentinel such as 38.4 is a module communication-error code, not a temperature.
// Half a displayed tenth of a degree keeps 38.40 and rejects 38.5.
const sentinelTolerance = 0.05

// Two samples are the same reading when they differ by less than a hundredth of
// a displayed tenth. A 0.1 ℃ step is a change; 18.7000001 is not.
const valueEquality = 0.0001

// QualityChecks is the optional per-point configuration stored on the freshness policy.
type QualityChecks struct {
	MaxStep         *float64
	MaxStepWindow   time.Duration
	Sentinels       []float64
	Flatline        time.Duration
	FlatlineRunning time.Duration
	Running         []OperatingExpect
	Inputs          []PointRef
	Physics         []PhysicsRule
}

// PhysicsRule is one cross-point check compiled from the site file onto this point.
// Rated is the confirmed nameplate kilowatts or amperes. Peers are other points read
// at ingest; history replay does not judge them, because it would compare a past
// sample with today's peers.
type PhysicsRule struct {
	Kind     string
	MarginK  *float64
	Fraction *float64
	Rated    float64
	EpsilonK *float64
	Peers    []PhysicsPeer
	Running  []OperatingExpect
}

// PhysicsPeer is another point this check reads, by the role the check gives it.
type PhysicsPeer struct {
	Role     string
	DeviceID string
	Key      string
}

// PeerSample is the last accepted sample of a peer. Good is false when that
// sample's quality is not GOOD, so a sentinel or a spike is not used as evidence.
type PeerSample struct {
	Number *float64
	Text   string
	Good   bool
}

// OperatingExpect is another point whose current value says the equipment is running.
type OperatingExpect struct {
	DeviceID string
	Key      string
	Equals   string
}

// PointRef names a point whose current quality this reading depends on.
type PointRef struct {
	DeviceID string
	Key      string
}

// PriorNumeric is the previous accepted number for this point.
type PriorNumeric struct {
	Value          float64
	SampledAt      time.Time
	UnchangedSince time.Time
}

// PointQualityInput is everything one numeric check needs besides the new sample.
type PointQualityInput struct {
	PointType       string
	Live            bool
	Previous        *PriorNumeric
	Running         bool
	InputsUntrusted bool
	Peers           map[string]PeerSample
}

// PointHealth is the status and score exposed on the current snapshot.
type PointHealth struct {
	Status  string
	Score   int
	Reasons []QualityReason
}

// AssessPointQuality reports suspect reasons for one numeric sample.
// Range rejection stays in validateObservation. Counters only inherit an
// untrusted input (a derived cooling total); they are not judged for spikes.
func AssessPointQuality(value float64, at time.Time, checks *QualityChecks, input PointQualityInput) []QualityReason {
	if checks == nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	reasons := make([]QualityReason, 0, 4)
	if input.PointType != "COUNTER" && matchesSentinel(value, checks.Sentinels) {
		reasons = append(reasons, QualityReasonSentinel)
	}
	if input.Live && input.PointType != "COUNTER" && input.Previous != nil && spike(value, at, *input.Previous, checks) {
		reasons = append(reasons, QualityReasonSpike)
	}
	if input.Live && input.PointType != "COUNTER" && input.Previous != nil && flatline(value, at, *input.Previous, checks, input.Running) {
		reasons = append(reasons, QualityReasonFlatline)
	}
	if input.Live && input.InputsUntrusted && len(checks.Inputs) > 0 {
		reasons = append(reasons, QualityReasonDerivedInput)
	}
	if input.Live {
		reasons = append(reasons, physicsReasons(value, checks, input)...)
	}
	return reasons
}

func physicsReasons(value float64, checks *QualityChecks, input PointQualityInput) []QualityReason {
	reasons := make([]QualityReason, 0, 1)
	for _, rule := range checks.Physics {
		code, ok := nameplate.QualityReason(rule.Kind)
		if !ok {
			continue
		}
		if !rule.peersReady(input) {
			continue
		}
		violates, skipped := nameplate.Violates(rule.observation(value, input))
		if skipped || !violates {
			continue
		}
		reasons = append(reasons, QualityReason(code))
	}
	return reasons
}

func (rule PhysicsRule) peersReady(input PointQualityInput) bool {
	for _, peer := range rule.Peers {
		sample, ok := input.Peers[peer.DeviceID+"/"+peer.Key]
		if !ok || !sample.Good || sample.Number == nil {
			return false
		}
	}
	return true
}

func (rule PhysicsRule) observation(value float64, input PointQualityInput) nameplate.Observation {
	obs := nameplate.Observation{
		Kind: rule.Kind, Value: value, Rated: rule.Rated,
		MarginK: rule.MarginK, Fraction: rule.Fraction, EpsilonK: rule.EpsilonK,
		Running: rule.running(input),
	}
	for _, peer := range rule.Peers {
		sample, ok := input.Peers[peer.DeviceID+"/"+peer.Key]
		if !ok || !sample.Good || sample.Number == nil {
			continue
		}
		number := *sample.Number
		switch peer.Role {
		case "dryBulb":
			obs.DryBulbC = &number
		case "condenserEntering":
			obs.CondenserEnteringC = append(obs.CondenserEnteringC, number)
		case "power":
			obs.PowersKW = append(obs.PowersKW, number)
		case "evaporator":
			obs.EvaporatorC = &number
		case "condenser":
			obs.CondenserC = append(obs.CondenserC, number)
		case "return":
			obs.ReturnC = &number
		case "entering":
			obs.EnteringC = &number
		case "condenserHeat":
			obs.CondenserHeatKW = &number
		case "flow":
			obs.FlowM3H = &number
		case "heatEntering":
			obs.HeatEnteringC = &number
		case "heatLeaving":
			obs.HeatLeavingC = &number
		}
	}
	return obs
}

func (rule PhysicsRule) running(input PointQualityInput) *bool {
	if len(rule.Running) == 0 {
		return nil
	}
	matched := false
	for _, expect := range rule.Running {
		sample, ok := input.Peers[expect.DeviceID+"/"+expect.Key]
		if ok && sample.Good && sample.Text == expect.Equals {
			matched = true
			break
		}
	}
	return &matched
}

func isPhysicsReason(reason QualityReason) bool {
	return strings.HasPrefix(string(reason), "PHYSICS_")
}

func matchesSentinel(value float64, sentinels []float64) bool {
	for _, sentinel := range sentinels {
		if math.Abs(value-sentinel) <= sentinelTolerance {
			return true
		}
	}
	return false
}

func spike(value float64, at time.Time, previous PriorNumeric, checks *QualityChecks) bool {
	if checks.MaxStep == nil || previous.SampledAt.IsZero() || !at.After(previous.SampledAt) {
		return false
	}
	window := checks.MaxStepWindow
	if window <= 0 {
		window = 15 * time.Minute
	}
	if at.Sub(previous.SampledAt) > window {
		return false
	}
	return math.Abs(value-previous.Value) > *checks.MaxStep
}

func flatline(value float64, at time.Time, previous PriorNumeric, checks *QualityChecks, running bool) bool {
	if !nearlyEqual(value, previous.Value) {
		return false
	}
	since := previous.UnchangedSince
	if since.IsZero() {
		since = previous.SampledAt
	}
	if since.IsZero() || !at.After(since) {
		return false
	}
	held := at.Sub(since)
	if running && checks.FlatlineRunning > 0 && held >= checks.FlatlineRunning {
		return true
	}
	// No running gate, or the plant is off: only the longer threshold applies.
	if !running && checks.Flatline > 0 && held >= checks.Flatline {
		return true
	}
	return false
}

func nearlyEqual(left, right float64) bool {
	return math.Abs(left-right) <= valueEquality
}

// HealthFor is the status of a sample that was accepted or rejected for range.
func HealthFor(quality ObservationQuality, reasons []QualityReason) PointHealth {
	switch {
	case quality == QualityInvalid || containsReason(reasons, QualityReasonOutOfRange):
		return PointHealth{Status: HealthBad, Score: 0, Reasons: copyReasons(reasons)}
	case quality == QualityPartial || containsReason(reasons, QualityReasonSpike) || containsReason(reasons, QualityReasonSentinel) || containsReason(reasons, QualityReasonFlatline) || containsReason(reasons, QualityReasonDerivedInput) || containsPhysics(reasons):
		return PointHealth{Status: HealthSuspect, Score: 40, Reasons: copyReasons(reasons)}
	case quality == QualityStale:
		return PointHealth{Status: HealthStale, Score: 20, Reasons: copyReasons(reasons)}
	default:
		return PointHealth{Status: HealthHealthy, Score: 100, Reasons: []QualityReason{}}
	}
}

// ApplyFreshness downgrades a healthy point whose displayed sample is stale.
// A bad or suspect sample stays that way: staleness does not hide it.
func ApplyFreshness(health PointHealth, fresh bool) PointHealth {
	if fresh || health.Status == HealthBad || health.Status == HealthSuspect {
		return health
	}
	health.Status = HealthStale
	health.Score = 20
	return health
}

func containsPhysics(reasons []QualityReason) bool {
	for _, reason := range reasons {
		if isPhysicsReason(reason) {
			return true
		}
	}
	return false
}

func containsReason(reasons []QualityReason, want QualityReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func copyReasons(reasons []QualityReason) []QualityReason {
	if len(reasons) == 0 {
		return []QualityReason{}
	}
	return append([]QualityReason(nil), reasons...)
}

// UnchangedSinceOf is when the new sample's value began. A repeat keeps the previous start.
func UnchangedSinceOf(value float64, at time.Time, previous *PriorNumeric) time.Time {
	if previous != nil && nearlyEqual(value, previous.Value) {
		if !previous.UnchangedSince.IsZero() {
			return previous.UnchangedSince
		}
		if !previous.SampledAt.IsZero() {
			return previous.SampledAt
		}
	}
	return at
}

type qualityChecksDocument struct {
	MaxStep                *float64        `json:"maxStep"`
	MaxStepWindowSeconds   *int            `json:"maxStepWindowSeconds"`
	Sentinels              []float64       `json:"sentinels"`
	FlatlineSeconds        *int            `json:"flatlineSeconds"`
	FlatlineRunningSeconds *int            `json:"flatlineRunningSeconds"`
	Running                []operatingJSON `json:"running"`
	Inputs                 []pointRefJSON  `json:"inputs"`
	Physics                []physicsJSON   `json:"physics"`
}

type physicsJSON struct {
	Kind                string            `json:"kind"`
	MarginK             *float64          `json:"marginK"`
	AboveRatedFraction  *float64          `json:"aboveRatedFraction"`
	CarnotFraction      *float64          `json:"carnotFraction"`
	HeatBalanceFraction *float64          `json:"heatBalanceFraction"`
	RatedPowerKW        *float64          `json:"ratedPowerKw"`
	RatedCurrentA       *float64          `json:"ratedCurrentA"`
	Basis               string            `json:"basis"`
	EpsilonK            *float64          `json:"epsilonK"`
	Peers               []physicsPeerJSON `json:"peers"`
	Running             []operatingJSON   `json:"running"`
}

type physicsPeerJSON struct {
	Role     string `json:"role"`
	DeviceID string `json:"deviceId"`
	Key      string `json:"key"`
}

type operatingJSON struct {
	DeviceID string `json:"deviceId"`
	Key      string `json:"key"`
	Equals   string `json:"equals"`
}

type pointRefJSON struct {
	DeviceID string `json:"deviceId"`
	Key      string `json:"key"`
}

func parseQualityChecks(raw []byte) (*QualityChecks, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var document qualityChecksDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	checks := &QualityChecks{MaxStep: document.MaxStep, Sentinels: document.Sentinels}
	if document.MaxStepWindowSeconds != nil && *document.MaxStepWindowSeconds > 0 {
		checks.MaxStepWindow = time.Duration(*document.MaxStepWindowSeconds) * time.Second
	}
	if document.FlatlineSeconds != nil && *document.FlatlineSeconds > 0 {
		checks.Flatline = time.Duration(*document.FlatlineSeconds) * time.Second
	}
	if document.FlatlineRunningSeconds != nil && *document.FlatlineRunningSeconds > 0 {
		checks.FlatlineRunning = time.Duration(*document.FlatlineRunningSeconds) * time.Second
	}
	for _, item := range document.Running {
		if item.DeviceID == "" || item.Key == "" || item.Equals == "" {
			continue
		}
		checks.Running = append(checks.Running, OperatingExpect(item))
	}
	for _, item := range document.Inputs {
		if item.DeviceID == "" || item.Key == "" {
			continue
		}
		checks.Inputs = append(checks.Inputs, PointRef(item))
	}
	for _, item := range document.Physics {
		rule, err := physicsRule(item)
		if err != nil {
			return nil, err
		}
		checks.Physics = append(checks.Physics, rule)
	}
	if checks.MaxStep == nil && len(checks.Sentinels) == 0 && checks.Flatline == 0 && checks.FlatlineRunning == 0 && len(checks.Running) == 0 && len(checks.Inputs) == 0 && len(checks.Physics) == 0 {
		return nil, nil
	}
	return checks, nil
}

func physicsRule(item physicsJSON) (PhysicsRule, error) {
	if _, ok := nameplate.QualityReason(item.Kind); !ok {
		return PhysicsRule{}, errUnknownPhysicsKind(item.Kind)
	}
	rule := PhysicsRule{Kind: item.Kind, MarginK: item.MarginK, EpsilonK: item.EpsilonK}
	switch item.Kind {
	case nameplate.KindPowerNameplate:
		rule.Fraction = item.AboveRatedFraction
		if item.Basis == nameplate.BasisCurrent {
			if item.RatedCurrentA != nil {
				rule.Rated = *item.RatedCurrentA
			}
		} else if item.RatedPowerKW != nil {
			rule.Rated = *item.RatedPowerKW
		}
	case nameplate.KindCOPCarnot:
		rule.Fraction = item.CarnotFraction
	case nameplate.KindHeatBalance:
		rule.Fraction = item.HeatBalanceFraction
	}
	for _, peer := range item.Peers {
		if peer.DeviceID == "" || peer.Key == "" || peer.Role == "" {
			continue
		}
		rule.Peers = append(rule.Peers, PhysicsPeer(peer))
	}
	for _, expect := range item.Running {
		if expect.DeviceID == "" || expect.Key == "" || expect.Equals == "" {
			continue
		}
		rule.Running = append(rule.Running, OperatingExpect(expect))
	}
	return rule, nil
}

type physicsKindError string

func (err physicsKindError) Error() string { return string(err) }

func errUnknownPhysicsKind(kind string) error {
	return physicsKindError("unknown physics check " + kind)
}
