package measurement

import (
	"math"
	"time"
)

const (
	// DefaultStableBandK is one probe accuracy: both temperatures must stay inside it.
	DefaultStableBandK = 0.10
	// DefaultOffsetMinimum is longer than one start, shorter than the idle hours at a plant
	// that circulates with every chiller off.
	DefaultOffsetMinimum = 10 * time.Minute
	// DefaultOffsetMinimumSamples keeps a single held value from becoming an estimate.
	DefaultOffsetMinimumSamples = 5
	// DefaultOffsetMaxGap breaks the window when samples stop arriving.
	DefaultOffsetMaxGap = 15 * time.Minute
	// DefaultOffsetAlarmK is wider than the ±0.141 K accuracy of two ±0.1 K probes,
	// so the offset is larger than those probes can explain.
	DefaultOffsetAlarmK = 0.20
	// DefaultDriftAlarmK is one probe accuracy of movement between two estimates.
	DefaultDriftAlarmK = 0.10

	AlarmOffset         = "OFFSET"
	AlarmDrift          = "DRIFT"
	AlarmOffsetAndDrift = "OFFSET_AND_DRIFT"
)

// OffsetConfig is how long a pumps-on, chillers-off, stable window must last,
// and when the resulting offset is worth an alarm. The estimate is only a suggestion.
type OffsetConfig struct {
	StableBandK     float64
	MinimumDuration time.Duration
	MinimumSamples  int
	MaxGap          time.Duration
	OffsetAlarmK    float64
	DriftAlarmK     float64
}

// OffsetWindow is the open calibration window. It is not an estimate yet.
type OffsetWindow struct {
	Open      bool
	StartedAt time.Time
	LastAt    time.Time
	Count     int
	SumOffset float64
	SumSq     float64
	SupplySum float64
	ReturnSum float64
	SupplyMin float64
	SupplyMax float64
	ReturnMin float64
	ReturnMax float64
}

// OffsetSample is one moment. Eligible means the pumps are running, every chiller is off,
// and both temperatures are good. The caller decides eligibility; this function only folds it.
type OffsetSample struct {
	At       time.Time
	SupplyC  float64
	ReturnC  float64
	Eligible bool
}

// OffsetEstimate is one finished window. OffsetK is mean(return − supply).
// CorrectedDeltaT subtracts it from a measured difference. Applied is always false:
// nothing in this package writes the correction back onto a reading.
type OffsetEstimate struct {
	StartedAt   time.Time
	EndedAt     time.Time
	Count       int
	OffsetK     float64
	StddevK     float64
	SupplyMeanC float64
	ReturnMeanC float64
	Alarm       string
	Applied     bool
}

// CorrectedDeltaT is the suggested difference. It is not applied to stored readings.
func CorrectedDeltaT(measuredK, offsetK float64) float64 {
	return measuredK - offsetK
}

func (cfg OffsetConfig) normalized() OffsetConfig {
	if cfg.StableBandK <= 0 {
		cfg.StableBandK = DefaultStableBandK
	}
	if cfg.MinimumDuration <= 0 {
		cfg.MinimumDuration = DefaultOffsetMinimum
	}
	if cfg.MinimumSamples < 2 {
		cfg.MinimumSamples = DefaultOffsetMinimumSamples
	}
	if cfg.MaxGap <= 0 {
		cfg.MaxGap = DefaultOffsetMaxGap
	}
	if cfg.OffsetAlarmK <= 0 {
		cfg.OffsetAlarmK = DefaultOffsetAlarmK
	}
	if cfg.DriftAlarmK <= 0 {
		cfg.DriftAlarmK = DefaultDriftAlarmK
	}
	return cfg
}

// FoldOffset extends or closes a window. An estimate is returned only when a window that
// already lasted long enough is closed by a gap, a jump, or the plant leaving the idle condition.
func FoldOffset(cfg OffsetConfig, window OffsetWindow, previousOffset *float64, sample OffsetSample) (OffsetWindow, *OffsetEstimate) {
	cfg = cfg.normalized()
	var estimate *OffsetEstimate
	// The same timestamp is the temperatures we already counted, re-read because a pump
	// or a chiller reported. It must not close the window or add the sample again.
	close := false
	if window.Open {
		switch {
		case !sample.Eligible, sample.At.Before(window.LastAt), sample.At.Sub(window.LastAt) > cfg.MaxGap:
			close = true
		case sample.At.After(window.LastAt) && !stableWith(window, sample, cfg.StableBandK):
			close = true
		}
	}
	if close {
		if finished(window, cfg) {
			estimate = summarize(window, previousOffset, cfg)
		}
		window = OffsetWindow{}
	}
	if !sample.Eligible || !finite(sample.SupplyC) || !finite(sample.ReturnC) {
		return window, estimate
	}
	if window.Open && !sample.At.After(window.LastAt) {
		return window, estimate
	}
	if !window.Open {
		return startWindow(sample), estimate
	}
	return addSample(window, sample), estimate
}

func stableWith(window OffsetWindow, sample OffsetSample, band float64) bool {
	supplyMin, supplyMax := span(window.SupplyMin, window.SupplyMax, sample.SupplyC)
	returnMin, returnMax := span(window.ReturnMin, window.ReturnMax, sample.ReturnC)
	return supplyMax-supplyMin <= band && returnMax-returnMin <= band
}

func span(min, max, value float64) (float64, float64) {
	if value < min {
		min = value
	}
	if value > max {
		max = value
	}
	return min, max
}

func finished(window OffsetWindow, cfg OffsetConfig) bool {
	return window.Open && window.Count >= cfg.MinimumSamples && window.LastAt.Sub(window.StartedAt) >= cfg.MinimumDuration
}

func startWindow(sample OffsetSample) OffsetWindow {
	offset := sample.ReturnC - sample.SupplyC
	return OffsetWindow{
		Open: true, StartedAt: sample.At, LastAt: sample.At, Count: 1,
		SumOffset: offset, SumSq: offset * offset,
		SupplySum: sample.SupplyC, ReturnSum: sample.ReturnC,
		SupplyMin: sample.SupplyC, SupplyMax: sample.SupplyC,
		ReturnMin: sample.ReturnC, ReturnMax: sample.ReturnC,
	}
}

func addSample(window OffsetWindow, sample OffsetSample) OffsetWindow {
	offset := sample.ReturnC - sample.SupplyC
	window.LastAt = sample.At
	window.Count++
	window.SumOffset += offset
	window.SumSq += offset * offset
	window.SupplySum += sample.SupplyC
	window.ReturnSum += sample.ReturnC
	window.SupplyMin, window.SupplyMax = span(window.SupplyMin, window.SupplyMax, sample.SupplyC)
	window.ReturnMin, window.ReturnMax = span(window.ReturnMin, window.ReturnMax, sample.ReturnC)
	return window
}

func summarize(window OffsetWindow, previous *float64, cfg OffsetConfig) *OffsetEstimate {
	count := float64(window.Count)
	offset := window.SumOffset / count
	variance := 0.0
	if window.Count > 1 {
		variance = (window.SumSq - window.SumOffset*window.SumOffset/count) / float64(window.Count-1)
		// A constant offset is not exact in floating point; a residual under 1e-12 K² is zero.
		if variance < 1e-12 {
			variance = 0
		}
	}
	estimate := &OffsetEstimate{
		StartedAt: window.StartedAt, EndedAt: window.LastAt, Count: window.Count,
		OffsetK: offset, StddevK: math.Sqrt(variance),
		SupplyMeanC: window.SupplySum / count, ReturnMeanC: window.ReturnSum / count,
		Applied: false,
	}
	offsetHit := math.Abs(offset) >= cfg.OffsetAlarmK
	driftHit := previous != nil && finite(*previous) && math.Abs(offset-*previous) >= cfg.DriftAlarmK
	switch {
	case offsetHit && driftHit:
		estimate.Alarm = AlarmOffsetAndDrift
	case offsetHit:
		estimate.Alarm = AlarmOffset
	case driftHit:
		estimate.Alarm = AlarmDrift
	}
	return estimate
}
