package telemetry

import (
	"encoding/json"
	"math"
	"time"
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
	return reasons
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
	case quality == QualityPartial || containsReason(reasons, QualityReasonSpike) || containsReason(reasons, QualityReasonSentinel) || containsReason(reasons, QualityReasonFlatline) || containsReason(reasons, QualityReasonDerivedInput):
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
	if checks.MaxStep == nil && len(checks.Sentinels) == 0 && checks.Flatline == 0 && checks.FlatlineRunning == 0 && len(checks.Running) == 0 && len(checks.Inputs) == 0 {
		return nil, nil
	}
	return checks, nil
}
