package edgecontrol

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"
)

// DeadbandSample carries one point observation evaluated by DeadbandFilter.
type DeadbandSample struct {
	Key        string
	PointType  string
	ValueType  string
	Value      any
	Quality    Quality
	ObservedAt time.Time
}

type deadbandPointState struct {
	quality    string
	value      float64
	hasNumeric bool
	emittedAt  time.Time
}

// DeadbandFilter suppresses redundant analog GOOD observations within Deadband
// until MaxSilence elapses, while passing quality transitions, non-GOOD samples,
// counters, state/boolean values, and non-finite values unchanged.
type DeadbandFilter struct {
	Deadband   float64
	MaxSilence time.Duration

	mu     sync.Mutex
	states map[string]deadbandPointState
}

// NewDeadbandFilter creates an analog deadband and heartbeat filter.
// A non-positive Deadband disables filtering.
func NewDeadbandFilter(deadband float64, maxSilence time.Duration) *DeadbandFilter {
	return &DeadbandFilter{
		Deadband:   deadband,
		MaxSilence: maxSilence,
		states:     map[string]deadbandPointState{},
	}
}

// Enabled reports whether analog deadband suppression is active.
func (f *DeadbandFilter) Enabled() bool {
	return f != nil && !math.IsNaN(f.Deadband) && !math.IsInf(f.Deadband, 0) && f.Deadband > 0
}

// Allow evaluates sample against the last emitted state for sample.Key and records
// it as emitted when allowed.
func (f *DeadbandFilter) Allow(sample DeadbandSample) bool {
	if !f.Enabled() {
		return true
	}
	key := strings.TrimSpace(sample.Key)
	if key == "" {
		return true
	}
	observedAt := normalizeDeadbandTime(sample.ObservedAt)

	f.mu.Lock()
	defer f.mu.Unlock()
	emit, nextState := f.evaluateLocked(key, sample, observedAt, f.Deadband)
	if emit {
		if f.states == nil {
			f.states = map[string]deadbandPointState{}
		}
		f.states[key] = nextState
	}
	return emit
}

// IsAnalog reports whether sample is a finite analog value eligible for deadband suppression.
func (sample DeadbandSample) IsAnalog() bool {
	_, ok := classifyDeadbandAnalogValue(sample)
	return ok
}

// Check evaluates whether sample should be emitted without updating filter state.
func (f *DeadbandFilter) Check(sample DeadbandSample) bool {
	return f.CheckWithMinDeadband(sample, 0)
}

// CheckWithMinDeadband evaluates whether sample should be emitted using max(f.Deadband, minDeadband).
func (f *DeadbandFilter) CheckWithMinDeadband(sample DeadbandSample, minDeadband float64) bool {
	if !f.Enabled() {
		return true
	}
	key := strings.TrimSpace(sample.Key)
	if key == "" {
		return true
	}
	observedAt := normalizeDeadbandTime(sample.ObservedAt)
	threshold := f.effectiveDeadband(minDeadband)

	f.mu.Lock()
	defer f.mu.Unlock()
	emit, _ := f.evaluateLocked(key, sample, observedAt, threshold)
	return emit
}

// MarkEmitted records sample as the latest emitted baseline for sample.Key.
func (f *DeadbandFilter) MarkEmitted(sample DeadbandSample) {
	if !f.Enabled() {
		return
	}
	key := strings.TrimSpace(sample.Key)
	if key == "" {
		return
	}
	observedAt := normalizeDeadbandTime(sample.ObservedAt)
	nextState := buildDeadbandState(sample, observedAt)

	f.mu.Lock()
	if f.states == nil {
		f.states = map[string]deadbandPointState{}
	}
	f.states[key] = nextState
	f.mu.Unlock()
}

// Forget removes any stored state for key.
func (f *DeadbandFilter) Forget(key string) {
	if f == nil {
		return
	}
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return
	}
	f.mu.Lock()
	delete(f.states, trimmed)
	f.mu.Unlock()
}

// Reset clears all tracked point states.
func (f *DeadbandFilter) Reset() {
	if f == nil {
		return
	}
	f.mu.Lock()
	clear(f.states)
	f.mu.Unlock()
}

func (f *DeadbandFilter) effectiveDeadband(minDeadband float64) float64 {
	threshold := f.Deadband
	if !math.IsNaN(minDeadband) && !math.IsInf(minDeadband, 0) && minDeadband > threshold {
		threshold = minDeadband
	}
	return threshold
}

func (f *DeadbandFilter) evaluateLocked(key string, sample DeadbandSample, observedAt time.Time, threshold float64) (bool, deadbandPointState) {
	nextState := buildDeadbandState(sample, observedAt)
	prev, hasPrev := f.states[key]
	if !hasPrev {
		return true, nextState
	}
	if nextState.quality != string(QualityGood) || prev.quality != nextState.quality {
		return true, nextState
	}
	if !nextState.hasNumeric || !prev.hasNumeric {
		return true, nextState
	}
	if f.MaxSilence > 0 && observedAt.Sub(prev.emittedAt) >= f.MaxSilence {
		return true, nextState
	}
	if math.Abs(nextState.value-prev.value) >= threshold {
		return true, nextState
	}
	return false, deadbandPointState{}
}

func buildDeadbandState(sample DeadbandSample, observedAt time.Time) deadbandPointState {
	quality := strings.ToUpper(strings.TrimSpace(string(sample.Quality)))
	val, isAnalogFinite := classifyDeadbandAnalogValue(sample)
	return deadbandPointState{
		quality:    quality,
		value:      val,
		hasNumeric: isAnalogFinite,
		emittedAt:  observedAt,
	}
}

func classifyDeadbandAnalogValue(sample DeadbandSample) (float64, bool) {
	switch strings.ToUpper(strings.TrimSpace(sample.PointType)) {
	case "COUNTER", "STATE":
		return 0, false
	}
	switch strings.ToUpper(strings.TrimSpace(sample.ValueType)) {
	case "BOOLEAN", "BOOL", "STRING", "JSON":
		return 0, false
	}
	val, ok := extractDeadbandFloat64(sample.Value)
	if !ok || math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, false
	}
	return val, true
}

func extractDeadbandFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case Value:
		return x.NumericFloat64()
	case *Value:
		if x == nil {
			return 0, false
		}
		return x.NumericFloat64()
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func normalizeDeadbandTime(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now().UTC()
	}
	return at.UTC()
}
