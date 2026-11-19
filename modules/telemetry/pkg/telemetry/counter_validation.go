package telemetry

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	minCounterRateWindow         = time.Minute
	defaultEnergyMaxRatePerHour  = 100000.0
	defaultEnergyJitterTolerance = 0.5
	defaultDiscreteJitterTol     = 0.05
)

type CounterTransitionType string

const (
	TransitionInitial          CounterTransitionType = "INITIAL"
	TransitionIncrease         CounterTransitionType = "INCREASE"
	TransitionUnchanged        CounterTransitionType = "UNCHANGED"
	TransitionReset            CounterTransitionType = "RESET"
	TransitionRollover         CounterTransitionType = "ROLLOVER"
	TransitionMeterReplacement CounterTransitionType = "METER_REPLACEMENT"
	TransitionRecovery         CounterTransitionType = "RECOVERY"
	TransitionRevisionBoundary CounterTransitionType = "REVISION_BOUNDARY"
	TransitionUnitBoundary     CounterTransitionType = "UNIT_BOUNDARY"
	TransitionInvalidDecrease  CounterTransitionType = "INVALID_DECREASE"
	TransitionInvalidZeroDrop  CounterTransitionType = "INVALID_ZERO_DROP"
	TransitionInvalidSpike     CounterTransitionType = "INVALID_SPIKE"
	TransitionInvalid          CounterTransitionType = "INVALID"
)

// CounterValidationConfig holds per-point limits and transformer ratios for cumulative counters.
type CounterValidationConfig struct {
	MaxPowerKW      float64
	MaxStep         float64
	MaxStepWindow   time.Duration
	JitterTolerance float64
	CTRatio         float64
	PTRatio         float64
}

type CounterSample struct {
	TenantID         string
	SiteID           string
	PointID          string
	ObservationID    string
	PointRevision    int64
	Unit             string
	DecreaseMode     string
	RolloverModulus  float64
	ReplacementCarry float64
	SampledAt        time.Time
	Value            float64
	Quality          ObservationQuality
	QualityReasons   []QualityReason
}

type CleanedCounterDelta struct {
	ObservationID         string
	TenantID              string
	SiteID                string
	PointID               string
	SampledAt             time.Time
	Value                 float64
	NormalizedValue       float64
	Unit                  string
	PreviousObservationID string
	PreviousSampledAt     time.Time
	PreviousValue         float64
	HasPrevious           bool
	Transition            CounterTransitionType
	Delta                 float64
	HasDelta              bool
	Quality               ObservationQuality
	Reasons               []QualityReason
	Quarantined           bool
}

// CounterEnergyUnitToKWhFactor converts an energy counter unit into kWh per 1 native unit.
func CounterEnergyUnitToKWhFactor(unit string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "wh", "w·h":
		return 0.001, true
	case "kwh", "kw·h":
		return 1.0, true
	case "mwh", "mw·h":
		return 1000.0, true
	case "gj":
		return 1000.0 / 3.6, true
	case "mj":
		return 1.0 / 3.6, true
	case "rth", "rt·h":
		return 3.51685284, true
	default:
		return 1.0, false
	}
}

// ApplyCounterRatio multiplies a raw counter reading or step by finite non-negative CT/PT ratios.
func ApplyCounterRatio(raw, ctRatio, ptRatio float64) (float64, error) {
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return 0, errors.New("counter value must be finite")
	}
	ct, pt := ctRatio, ptRatio
	if ct == 0 {
		ct = 1
	}
	if pt == 0 {
		pt = 1
	}
	if math.IsNaN(ct) || math.IsInf(ct, 0) || ct < 0 || math.IsNaN(pt) || math.IsInf(pt, 0) || pt < 0 {
		return 0, errors.New("counter CT/PT ratio must be positive and finite")
	}
	scaled := raw * ct * pt
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
		return 0, errors.New("scaled counter value overflows float64")
	}
	return scaled, nil
}

// DefaultCounterMaxRatePerHour returns the physical rate ceiling in native units per hour.
func DefaultCounterMaxRatePerHour(unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "wh", "w·h":
		return 1e8
	case "mwh", "mw·h":
		return 100.0
	case "gj":
		return 360.0
	case "mj":
		return 360000.0
	case "rth", "rt·h":
		return 30000.0
	default:
		return defaultEnergyMaxRatePerHour
	}
}

// DefaultCounterJitterTolerance returns tiny communication rollback tolerance in native units.
func DefaultCounterJitterTolerance(unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "wh", "w·h":
		return 500.0
	case "mwh", "mw·h":
		return 0.0005
	case "gj":
		return 0.15
	case "mj":
		return 150.0
	case "m3", "m³", "h", "count":
		return defaultDiscreteJitterTol
	default:
		return defaultEnergyJitterTolerance
	}
}

func effectiveJitterTolerance(cfg CounterValidationConfig, unit string) float64 {
	if cfg.JitterTolerance > 0 && !math.IsNaN(cfg.JitterTolerance) && !math.IsInf(cfg.JitterTolerance, 0) {
		return cfg.JitterTolerance
	}
	return DefaultCounterJitterTolerance(unit)
}

func maxAllowedCounterStep(cfg CounterValidationConfig, unit string, dt time.Duration) (float64, error) {
	if dt <= 0 {
		return 0, errors.New("counter elapsed duration must be positive")
	}
	if cfg.MaxPowerKW > 0 {
		rate := cfg.MaxPowerKW
		if factor, isEnergy := CounterEnergyUnitToKWhFactor(unit); isEnergy && factor > 0 {
			rate = cfg.MaxPowerKW / factor
		}
		effectiveHours := math.Max(dt.Hours(), minCounterRateWindow.Hours())
		return rate * effectiveHours, nil
	}
	if cfg.MaxStep > 0 {
		window := cfg.MaxStepWindow
		if window <= 0 {
			window = 15 * time.Minute
		}
		if dt <= window {
			return cfg.MaxStep, nil
		}
		return cfg.MaxStep * (dt.Hours() / window.Hours()), nil
	}
	effectiveHours := math.Max(dt.Hours(), minCounterRateWindow.Hours())
	return DefaultCounterMaxRatePerHour(unit) * effectiveHours, nil
}

// AssessCounterStepQuality checks physical step limits and explicit policy violations for live counters.
func AssessCounterStepQuality(value float64, at time.Time, checks *QualityChecks, input PointQualityInput) []QualityReason {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return []QualityReason{QualityReasonOutOfRange}
	}
	if !input.Live || input.Previous == nil || input.Previous.SampledAt.IsZero() || !at.After(input.Previous.SampledAt) {
		return nil
	}
	cfg := counterConfigFromChecks(checks)
	prevVal := input.Previous.Value
	if math.IsNaN(prevVal) || math.IsInf(prevVal, 0) {
		return nil
	}
	dt := at.Sub(input.Previous.SampledAt)
	maxStep, err := maxAllowedCounterStep(cfg, input.Unit, dt)
	if err != nil {
		return nil
	}
	if value > prevVal && (value-prevVal) > maxStep {
		return []QualityReason{QualityReasonSpike}
	}
	jitterTol := effectiveJitterTolerance(cfg, input.Unit)
	if value < prevVal-jitterTol {
		mode := strings.ToUpper(strings.TrimSpace(input.DecreaseMode))
		switch mode {
		case "RESET", "RESET_TO_ZERO", "METER_REPLACEMENT":
			if value == 0 && prevVal > 0 {
				return []QualityReason{QualityReasonSpike}
			}
			if value > maxStep {
				return []QualityReason{QualityReasonSpike}
			}
		case "ROLLOVER":
			if input.RolloverModulus <= 0 || prevVal > input.RolloverModulus || value >= input.RolloverModulus {
				return []QualityReason{QualityReasonSpike}
			}
			if (input.RolloverModulus-prevVal)+value > maxStep {
				return []QualityReason{QualityReasonSpike}
			}
		case "INVALID":
			return []QualityReason{QualityReasonSpike}
		}
	}
	return nil
}

func counterConfigFromChecks(checks *QualityChecks) CounterValidationConfig {
	if checks == nil {
		return CounterValidationConfig{}
	}
	cfg := CounterValidationConfig{
		MaxPowerKW:      checks.MaxPowerKW,
		MaxStepWindow:   checks.MaxStepWindow,
		JitterTolerance: checks.CounterJitterTolerance,
		CTRatio:         checks.CTRatio,
		PTRatio:         checks.PTRatio,
	}
	if checks.MaxStep != nil && *checks.MaxStep > 0 {
		cfg.MaxStep = *checks.MaxStep
	}
	return cfg
}

type counterAnchor struct {
	observationID string
	sampledAt     time.Time
	value         float64
	revision      int64
	unitKey       string
}

type indexedCounterSample struct {
	originalIdx int
	sample      CounterSample
	scaledValue float64
	validInput  bool
	inputReason QualityReason
}

// CleanCounterSeries partitions samples strictly by (TenantID, SiteID, PointID), cleans
// bus zero-drops, jitter rollbacks, and physical step spikes, and returns per-sample deltas.
func CleanCounterSeries(cfg CounterValidationConfig, samples []CounterSample) ([]CleanedCounterDelta, error) {
	if err := validateCounterConfig(cfg); err != nil {
		return nil, err
	}
	results := make([]CleanedCounterDelta, len(samples))
	if len(samples) == 0 {
		return results, nil
	}

	type partitionKey struct {
		tenantID string
		siteID   string
		pointID  string
	}
	partitions := make(map[partitionKey][]indexedCounterSample)
	var order []partitionKey
	for i, s := range samples {
		key := partitionKey{tenantID: s.TenantID, siteID: s.SiteID, pointID: s.PointID}
		if _, exists := partitions[key]; !exists {
			order = append(order, key)
		}
		scaled, err := ApplyCounterRatio(s.Value, cfg.CTRatio, cfg.PTRatio)
		valid := err == nil && scaled >= 0 && !s.SampledAt.IsZero() && s.Quality != QualityInvalid && !containsReason(s.QualityReasons, QualityReasonOutOfRange) && !containsReason(s.QualityReasons, QualityReasonSpike)
		var reason QualityReason
		if err != nil || scaled < 0 || s.SampledAt.IsZero() || s.Quality == QualityInvalid || containsReason(s.QualityReasons, QualityReasonOutOfRange) {
			reason = QualityReasonOutOfRange
		} else if containsReason(s.QualityReasons, QualityReasonSpike) {
			reason = QualityReasonSpike
		}
		partitions[key] = append(partitions[key], indexedCounterSample{
			originalIdx: i,
			sample:      s,
			scaledValue: scaled,
			validInput:  valid,
			inputReason: reason,
		})
	}

	for _, key := range order {
		items := partitions[key]
		sort.SliceStable(items, func(i, j int) bool {
			ti, tj := items[i].sample.SampledAt.UTC(), items[j].sample.SampledAt.UTC()
			if !ti.Equal(tj) {
				return ti.Before(tj)
			}
			return items[i].sample.ObservationID < items[j].sample.ObservationID
		})
		cleanPartition(cfg, items, results)
	}
	return results, nil
}

func validateCounterConfig(cfg CounterValidationConfig) error {
	for _, v := range []float64{cfg.MaxPowerKW, cfg.MaxStep, cfg.JitterTolerance, cfg.CTRatio, cfg.PTRatio} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return errors.New("counter validation config numeric parameters must be finite and non-negative")
		}
	}
	return nil
}

func normalizeUnitKey(unit string) string {
	return strings.TrimSpace(unit)
}

func cleanPartition(cfg CounterValidationConfig, items []indexedCounterSample, out []CleanedCounterDelta) {
	var (
		hasAnchor        bool
		anchor           counterAnchor
		hasSegmentHigh   bool
		segmentHighWater float64
		lastTime         time.Time
	)

	for i, item := range items {
		s := item.sample
		unitKey := normalizeUnitKey(s.Unit)
		res := CleanedCounterDelta{
			ObservationID:   s.ObservationID,
			TenantID:        s.TenantID,
			SiteID:          s.SiteID,
			PointID:         s.PointID,
			SampledAt:       s.SampledAt.UTC(),
			Value:           s.Value,
			NormalizedValue: item.scaledValue,
			Unit:            s.Unit,
			Quality:         s.Quality,
			Reasons:         copyReasons(s.QualityReasons),
		}
		if res.Quality == "" {
			res.Quality = QualityGood
		}

		if !item.validInput {
			res.Quality = QualityInvalid
			res.Transition = TransitionInvalid
			res.Quarantined = true
			if item.inputReason != "" && !containsReason(res.Reasons, item.inputReason) {
				res.Reasons = append(res.Reasons, item.inputReason)
			}
			out[item.originalIdx] = res
			continue
		}

		if !lastTime.IsZero() && !s.SampledAt.UTC().After(lastTime) {
			res.Quality = QualityInvalid
			res.Transition = TransitionInvalid
			res.Quarantined = true
			if !containsReason(res.Reasons, QualityReasonOutOfOrder) {
				res.Reasons = append(res.Reasons, QualityReasonOutOfOrder)
			}
			out[item.originalIdx] = res
			continue
		}
		lastTime = s.SampledAt.UTC()

		if hasAnchor {
			res.PreviousObservationID = anchor.observationID
			res.PreviousSampledAt = anchor.sampledAt
			res.PreviousValue = anchor.value
			res.HasPrevious = true
		}

		if !hasAnchor {
			res.Transition = TransitionInitial
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: item.scaledValue, revision: s.PointRevision, unitKey: unitKey}
			hasAnchor = true
			segmentHighWater = item.scaledValue
			hasSegmentHigh = true
			out[item.originalIdx] = res
			continue
		}

		if s.PointRevision != anchor.revision {
			res.Transition = TransitionRevisionBoundary
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: item.scaledValue, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = item.scaledValue
			hasSegmentHigh = true
			out[item.originalIdx] = res
			continue
		}
		if unitKey != anchor.unitKey {
			res.Transition = TransitionUnitBoundary
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: item.scaledValue, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = item.scaledValue
			hasSegmentHigh = true
			out[item.originalIdx] = res
			continue
		}

		dt := s.SampledAt.UTC().Sub(anchor.sampledAt)
		maxStep, err := maxAllowedCounterStep(cfg, s.Unit, dt)
		if err != nil {
			res.Quality = QualityInvalid
			res.Transition = TransitionInvalid
			res.Quarantined = true
			out[item.originalIdx] = res
			continue
		}
		jitterTol := effectiveJitterTolerance(cfg, s.Unit)
		mode := strings.ToUpper(strings.TrimSpace(s.DecreaseMode))
		val := item.scaledValue

		if val == 0 && anchor.value > 0 {
			nextVal, hasNext := nextPositiveInSegment(items, i+1, s.PointRevision, unitKey)
			validRollover := mode == "ROLLOVER" && s.RolloverModulus > 0 && anchor.value <= s.RolloverModulus && (s.RolloverModulus-anchor.value) <= maxStep
			allowsReset := mode == "RESET" || mode == "RESET_TO_ZERO" || mode == "METER_REPLACEMENT" || validRollover
			if !allowsReset || !hasNext || nextVal >= anchor.value-jitterTol {
				res.Transition = TransitionInvalidZeroDrop
				res.Quality = QualityInvalid
				res.Quarantined = true
				if !containsReason(res.Reasons, QualityReasonSpike) {
					res.Reasons = append(res.Reasons, QualityReasonSpike)
				}
				out[item.originalIdx] = res
				continue
			}
			switch mode {
			case "ROLLOVER":
				res.Transition = TransitionRollover
				res.Delta = s.RolloverModulus - anchor.value
			case "METER_REPLACEMENT":
				res.Transition = TransitionMeterReplacement
				res.Delta = s.ReplacementCarry
			default:
				res.Transition = TransitionReset
				res.Delta = 0
			}
			res.HasDelta = true
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: 0, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = 0
			hasSegmentHigh = true
			out[item.originalIdx] = res
			continue
		}

		if val == anchor.value {
			res.Transition = TransitionUnchanged
			res.Delta = 0
			res.HasDelta = true
			anchor.sampledAt = s.SampledAt.UTC()
			out[item.originalIdx] = res
			continue
		}

		if val > anchor.value {
			if mode == "INVALID" && hasSegmentHigh && anchor.value < segmentHighWater {
				if val <= segmentHighWater {
					if (val - anchor.value) > maxStep {
						res.Transition = TransitionInvalidSpike
						res.Quality = QualityInvalid
						res.Quarantined = true
						if !containsReason(res.Reasons, QualityReasonSpike) {
							res.Reasons = append(res.Reasons, QualityReasonSpike)
						}
						out[item.originalIdx] = res
						continue
					}
					res.Transition = TransitionInvalidDecrease
					res.Quality = QualityInvalid
					res.Quarantined = true
					anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
					out[item.originalIdx] = res
					continue
				}
				step := val - segmentHighWater
				if step > maxStep {
					res.Transition = TransitionInvalidSpike
					res.Quality = QualityInvalid
					res.Quarantined = true
					if !containsReason(res.Reasons, QualityReasonSpike) {
						res.Reasons = append(res.Reasons, QualityReasonSpike)
					}
					out[item.originalIdx] = res
					continue
				}
				res.Transition = TransitionRecovery
				res.Delta = step
				res.HasDelta = true
				anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
				segmentHighWater = val
				out[item.originalIdx] = res
				continue
			}

			step := val - anchor.value
			if step > maxStep {
				res.Transition = TransitionInvalidSpike
				res.Quality = QualityInvalid
				res.Quarantined = true
				if !containsReason(res.Reasons, QualityReasonSpike) {
					res.Reasons = append(res.Reasons, QualityReasonSpike)
				}
				out[item.originalIdx] = res
				continue
			}
			res.Transition = TransitionIncrease
			res.Delta = step
			res.HasDelta = true
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = val
			hasSegmentHigh = true
			out[item.originalIdx] = res
			continue
		}

		// val < anchor.value: separate tiny communication jitter rollbacks from real resets/rollovers.
		if val > 0 && (anchor.value-val) <= jitterTol {
			res.Transition = TransitionUnchanged
			res.Delta = 0
			res.HasDelta = true
			anchor.sampledAt = s.SampledAt.UTC()
			out[item.originalIdx] = res
			continue
		}

		switch mode {
		case "RESET", "RESET_TO_ZERO":
			if val > maxStep {
				res.Transition = TransitionInvalidSpike
				res.Quality = QualityInvalid
				res.Quarantined = true
				if !containsReason(res.Reasons, QualityReasonSpike) {
					res.Reasons = append(res.Reasons, QualityReasonSpike)
				}
				out[item.originalIdx] = res
				continue
			}
			res.Transition = TransitionReset
			res.Delta = val
			res.HasDelta = true
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = val
			hasSegmentHigh = true
		case "METER_REPLACEMENT":
			if s.ReplacementCarry < 0 || math.IsNaN(s.ReplacementCarry) || math.IsInf(s.ReplacementCarry, 0) || val > maxStep {
				res.Transition = TransitionInvalidSpike
				res.Quality = QualityInvalid
				res.Quarantined = true
				if !containsReason(res.Reasons, QualityReasonSpike) {
					res.Reasons = append(res.Reasons, QualityReasonSpike)
				}
				out[item.originalIdx] = res
				continue
			}
			res.Transition = TransitionMeterReplacement
			res.Delta = val + s.ReplacementCarry
			res.HasDelta = true
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = val
			hasSegmentHigh = true
		case "ROLLOVER":
			modulus := s.RolloverModulus
			if modulus <= 0 || math.IsNaN(modulus) || math.IsInf(modulus, 0) || anchor.value > modulus || val >= modulus {
				res.Transition = TransitionInvalidDecrease
				res.Quality = QualityInvalid
				res.Quarantined = true
				out[item.originalIdx] = res
				continue
			}
			step := (modulus - anchor.value) + val
			if step > maxStep {
				res.Transition = TransitionInvalidSpike
				res.Quality = QualityInvalid
				res.Quarantined = true
				if !containsReason(res.Reasons, QualityReasonSpike) {
					res.Reasons = append(res.Reasons, QualityReasonSpike)
				}
				out[item.originalIdx] = res
				continue
			}
			res.Transition = TransitionRollover
			res.Delta = step
			res.HasDelta = true
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
			segmentHighWater = val
			hasSegmentHigh = true
		default:
			res.Transition = TransitionInvalidDecrease
			res.Quality = QualityInvalid
			res.Quarantined = true
			if !containsReason(res.Reasons, QualityReasonOutOfRange) {
				res.Reasons = append(res.Reasons, QualityReasonOutOfRange)
			}
			anchor = counterAnchor{observationID: s.ObservationID, sampledAt: s.SampledAt.UTC(), value: val, revision: s.PointRevision, unitKey: unitKey}
		}
		out[item.originalIdx] = res
	}
}

func nextPositiveInSegment(items []indexedCounterSample, startIdx int, rev int64, unitKey string) (float64, bool) {
	for i := startIdx; i < len(items); i++ {
		item := items[i]
		if !item.validInput {
			continue
		}
		if item.sample.PointRevision != rev || normalizeUnitKey(item.sample.Unit) != unitKey {
			return 0, false
		}
		if item.scaledValue > 0 {
			return item.scaledValue, true
		}
	}
	return 0, false
}
