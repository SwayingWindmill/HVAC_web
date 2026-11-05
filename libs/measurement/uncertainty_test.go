package measurement

import (
	"math"
	"testing"
)

func ptr(value float64) *float64 { return &value }

// Songyaxuan header, pumps fixed near 489 m³/h. While the plant runs, the header
// difference (return t16 minus supply t18) has a median near 0.9 K. Two probes at
// ±0.1 K each make that difference uncertain by sqrt(0.1²+0.1²)/0.9 ≈ 15.7%.
func TestHeaderCoolingAtPointNineKelvin(t *testing.T) {
	const flow = 489.0
	const delta = 0.9
	alone := DeltaTAccuracyK(0.1, 0.1) / delta
	if math.Abs(alone-0.157134) > 1e-4 {
		t.Fatalf("ΔT alone = %.4f, want about 15.7%%", alone)
	}
	reported := CoolingKW(flow, delta)
	if math.Abs(reported-511.8357) > 0.01 {
		t.Fatalf("cooling = %.3f kW", reported)
	}
	got, ok := PropagateCooling(CoolingInput{
		FlowM3H: flow, SupplyC: 7.0, ReturnC: 7.9, PowerKW: ptr(120), ReportedKW: reported,
		SupplyAccuracyK: 0.1, ReturnAccuracyK: 0.1,
		FlowAccuracyFraction: 0.02, PowerAccuracyFraction: 0.01, PropertyAccuracyFraction: 0.005,
		LowConfidenceRelative: 0.10,
	})
	if !ok || !got.LowConfidence {
		t.Fatalf("0.9 K header must be low confidence: %+v ok=%v", got, ok)
	}
	if math.Abs(got.DeltaTRelative-alone) > 1e-9 {
		t.Fatalf("delta relative = %.4f", got.DeltaTRelative)
	}
	// Flow ±2% and the water-property ±0.5% sit on top of the 15.7%, still about ±16%.
	if got.Relative < 0.155 || got.Relative > 0.165 {
		t.Fatalf("combined cooling relative = %.4f, want about 16%%", got.Relative)
	}
	if math.Abs((got.High-got.Low)/2-reported*got.Relative) > 0.1 {
		t.Fatalf("half width = %.2f", (got.High-got.Low)/2)
	}
	if !got.HasCOP || got.COPRelative < got.Relative || got.COPRelative > 0.17 {
		t.Fatalf("COP relative = %.4f (has %v)", got.COPRelative, got.HasCOP)
	}
	// The offset is not an input. 0.9 K is propagated as measured.
	if math.Abs(got.DeltaTK-delta) > 1e-9 {
		t.Fatalf("delta was rewritten: %.3f", got.DeltaTK)
	}
}

func TestChillerDifferenceOfTwoPointEightIsNotLowConfidence(t *testing.T) {
	// A running chiller at Songyaxuan shows 1.3–2.8 K. 2.8 K is about 5% of the same probes.
	got, ok := PropagateCooling(CoolingInput{
		FlowM3H: 489, SupplyC: 7, ReturnC: 9.8, ReportedKW: CoolingKW(489, 2.8),
		SupplyAccuracyK: 0.1, ReturnAccuracyK: 0.1,
		FlowAccuracyFraction: 0.02, PropertyAccuracyFraction: 0.005,
		LowConfidenceRelative: 0.10,
	})
	if !ok || got.LowConfidence {
		t.Fatalf("2.8 K should be usable: %+v", got)
	}
	if got.DeltaTRelative > 0.06 {
		t.Fatalf("delta relative = %.4f", got.DeltaTRelative)
	}
}

func TestOnePointThreeKelvinIsJustLowConfidence(t *testing.T) {
	got, ok := PropagateCooling(CoolingInput{
		FlowM3H: 489, SupplyC: 7, ReturnC: 8.3, ReportedKW: CoolingKW(489, 1.3),
		SupplyAccuracyK: 0.1, ReturnAccuracyK: 0.1, LowConfidenceRelative: 0.10,
	})
	if !ok || !got.LowConfidence {
		t.Fatalf("1.3 K is 0.141/1.3 ≈ 10.9%%, above 10%%: %+v", got)
	}
}

func TestStoppedPlantIsNotAnUncertainLoad(t *testing.T) {
	if _, ok := PropagateCooling(CoolingInput{FlowM3H: 489, SupplyC: 12, ReturnC: 12.4, ReportedKW: 0}); ok {
		t.Fatal("a zero cooling load is the chiller-off clamp, not a low-confidence load")
	}
	// 0.2 K is low confidence for a counter increment. A reversed difference is not.
	if !CounterUncertain(CoolingInput{FlowM3H: 489, SupplyC: 12, ReturnC: 12.2}) {
		t.Fatal("0.2 K counter increment is low confidence")
	}
	if CounterUncertain(CoolingInput{FlowM3H: 489, SupplyC: 12.5, ReturnC: 12.0}) {
		t.Fatal("reversed difference is not a low-confidence load")
	}
	if CounterUncertain(CoolingInput{FlowM3H: 489, SupplyC: 7, ReturnC: 9.8}) {
		t.Fatal("2.8 K is not a low-confidence increment")
	}
}

func TestDeltaIntervalUsesTheSameProbeAccuracy(t *testing.T) {
	got, ok := PropagateDelta(0.9, 0.1, 0.1, 0.10)
	if !ok || !got.LowConfidence || math.Abs(got.Relative-0.157134) > 1e-3 {
		t.Fatalf("%+v", got)
	}
}
