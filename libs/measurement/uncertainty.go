// Package measurement propagates the stated accuracy of the probes that make a
// cooling load and a COP, and estimates the zero offset of a supply/return pair.
//
// Accuracies are maximum permissible errors (±), combined by root-sum-square.
// That is the combination behind "two probes at ±0.1 K make a 0.9 K difference
// uncertain by about ±16%". It is not a GUM standard uncertainty (which would
// divide a rectangular tolerance by sqrt(3) and report a smaller number).
package measurement

import "math"

const (
	// WaterFactorKW is kW per (m³/h · K), the factor the gateway already uses:
	// 1000 kg/m³ × 4.1868 kJ/(kg·K) / 3600 s/h = 1.163.
	WaterFactorKW = 1.163

	// DefaultTemperatureAccuracyK is a PT1000 near IEC 60751 Class AA
	// (±(0.10 + 0.0017|t|) °C, ±0.10 °C at 0 °C, about ±0.12 °C at 10 °C).
	DefaultTemperatureAccuracyK = 0.1
	// DefaultFlowAccuracyFraction is the wide end of a typical HVAC flow meter
	// (±0.5% electromagnetic, ±1–2% ultrasonic or turbine). ±2% does not claim
	// a stuck reading is accurate; it is the instrument class until the site says otherwise.
	DefaultFlowAccuracyFraction = 0.02
	// DefaultPowerAccuracyFraction is IEC 62053-21 Class 1 (±1%). Class 0.5 is ±0.5%
	// and is selected per point. A summed total keeps ±1%: a shared calibration
	// error does not shrink by averaging the meters.
	DefaultPowerAccuracyFraction = 0.01
	// DefaultPropertyAccuracyFraction covers ρ·cp from about 5 °C to 20 °C,
	// which stays within well under 0.5% of the 1.163 factor.
	DefaultPropertyAccuracyFraction = 0.005
	// DefaultLowConfidenceRelative flags a cooling load when the temperature
	// difference is smaller than ten times its own accuracy (relative accuracy > 10%).
	// Two ±0.1 K probes give ±0.141 K, so a difference below 1.41 K is low confidence.
	DefaultLowConfidenceRelative = 0.10
)

// CoolingInput is one cooling-load sample and the accuracies of the probes behind it.
// ReportedKW is the value the gateway already computed. An offset estimate is not an input:
// the suggested correction is never applied here.
type CoolingInput struct {
	FlowM3H                  float64
	SupplyC                  float64
	ReturnC                  float64
	PowerKW                  *float64
	ReportedKW               float64
	SupplyAccuracyK          float64
	ReturnAccuracyK          float64
	FlowAccuracyFraction     float64
	PowerAccuracyFraction    float64
	PropertyAccuracyFraction float64
	LowConfidenceRelative    float64
}

// CoolingUncertainty is the interval around the reported cooling load, and around COP when power is known.
type CoolingUncertainty struct {
	DeltaTK         float64
	DeltaTAccuracyK float64
	DeltaTRelative  float64
	Relative        float64
	Low             float64
	High            float64
	LowConfidence   bool
	COPRelative     float64
	COPLow          float64
	COPHigh         float64
	HasCOP          bool
}

// DeltaTAccuracyK is the root-sum-square of two independent temperature accuracies.
func DeltaTAccuracyK(supplyK, returnK float64) float64 {
	return math.Hypot(positive(supplyK, DefaultTemperatureAccuracyK), positive(returnK, DefaultTemperatureAccuracyK))
}

// CoolingKW is flow × ΔT × cp × ρ, written as 1.163 × m³/h × K.
func CoolingKW(flowM3H, deltaTK float64) float64 {
	if flowM3H <= 0 || deltaTK <= 0 {
		return 0
	}
	return WaterFactorKW * flowM3H * deltaTK
}

// PropagateCooling reports the interval of a positive cooling load.
// A zero or negative reported load is left alone: the gateway writes 0 when the chillers are off,
// and that zero is not an uncertain small difference.
func PropagateCooling(in CoolingInput) (CoolingUncertainty, bool) {
	if !finite(in.FlowM3H) || !finite(in.SupplyC) || !finite(in.ReturnC) || !finite(in.ReportedKW) || in.ReportedKW <= 0 || in.FlowM3H <= 0 {
		return CoolingUncertainty{}, false
	}
	delta := in.ReturnC - in.SupplyC
	accuracy := DeltaTAccuracyK(in.SupplyAccuracyK, in.ReturnAccuracyK)
	threshold := positive(in.LowConfidenceRelative, DefaultLowConfidenceRelative)
	flow := positive(in.FlowAccuracyFraction, DefaultFlowAccuracyFraction)
	property := positive(in.PropertyAccuracyFraction, DefaultPropertyAccuracyFraction)
	result := CoolingUncertainty{DeltaTK: delta, DeltaTAccuracyK: accuracy}
	if delta <= 0 {
		result.DeltaTRelative = math.Inf(1)
		result.Relative = 1
		result.Low = 0
		result.High = in.ReportedKW
		result.LowConfidence = true
		return withCOP(result, in), true
	}
	result.DeltaTRelative = accuracy / delta
	result.Relative = math.Hypot(flow, math.Hypot(result.DeltaTRelative, property))
	result.Low = in.ReportedKW * (1 - result.Relative)
	result.High = in.ReportedKW * (1 + result.Relative)
	result.LowConfidence = result.DeltaTRelative > threshold
	return withCOP(result, in), true
}

// PropagateDelta reports the interval of a temperature difference itself.
func PropagateDelta(deltaK, supplyAccuracyK, returnAccuracyK, lowConfidenceRelative float64) (CoolingUncertainty, bool) {
	if !finite(deltaK) || deltaK <= 0 {
		return CoolingUncertainty{}, false
	}
	accuracy := DeltaTAccuracyK(supplyAccuracyK, returnAccuracyK)
	relative := accuracy / deltaK
	threshold := positive(lowConfidenceRelative, DefaultLowConfidenceRelative)
	return CoolingUncertainty{
		DeltaTK: deltaK, DeltaTAccuracyK: accuracy, DeltaTRelative: relative, Relative: relative,
		Low: deltaK - accuracy, High: deltaK + accuracy, LowConfidence: relative > threshold,
	}, true
}

// CounterUncertain is true when the current supply, return and flow would make the
// instantaneous cooling load low confidence. The counter total is not given a ± band:
// only the latest increment is in doubt.
func CounterUncertain(in CoolingInput) bool {
	if !finite(in.FlowM3H) || !finite(in.SupplyC) || !finite(in.ReturnC) || in.FlowM3H <= 0 {
		return false
	}
	delta := in.ReturnC - in.SupplyC
	if delta <= 0 || CoolingKW(in.FlowM3H, delta) <= 0 {
		return false
	}
	accuracy := DeltaTAccuracyK(in.SupplyAccuracyK, in.ReturnAccuracyK)
	threshold := positive(in.LowConfidenceRelative, DefaultLowConfidenceRelative)
	return accuracy/delta > threshold
}

func withCOP(result CoolingUncertainty, in CoolingInput) CoolingUncertainty {
	if in.PowerKW == nil || !finite(*in.PowerKW) || *in.PowerKW <= 0 || !finite(result.Relative) {
		return result
	}
	cop := in.ReportedKW / *in.PowerKW
	power := positive(in.PowerAccuracyFraction, DefaultPowerAccuracyFraction)
	result.COPRelative = math.Hypot(result.Relative, power)
	result.COPLow = cop * (1 - result.COPRelative)
	result.COPHigh = cop * (1 + result.COPRelative)
	result.HasCOP = true
	return result
}

func positive(value, fallback float64) float64 {
	if value > 0 && !math.IsInf(value, 0) {
		return value
	}
	return fallback
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
