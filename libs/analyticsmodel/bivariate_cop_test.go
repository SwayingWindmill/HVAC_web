package analyticsmodel

import (
	"encoding/json"
	"math"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }

func TestEvaluateBivariateCOPMatchedBinsAndEnergyWeighting(t *testing.T) {
	samples := []ControlTickSample{
		// Bin (24-26 C, 600-800 kW): placed first in input to verify deterministic sorting.
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(25.0), CoolingLoadKW: floatPtr(700), ObservedCOP: floatPtr(3.5), DurationHours: 0.5},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(700), ObservedCOP: floatPtr(4.2), DurationHours: 0.5},
		// Bin (22-24 C, 400-600 kW): mixed RULE_BASELINE + MANUAL_OVERRIDE, default DurationHours=0.25,
		// and derived COP from CoolingLoadKW / ChillerPowerKW.
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(22.2), CoolingLoadKW: floatPtr(450), ObservedCOP: floatPtr(4.5), DurationHours: 0},
		{Mode: ModeManualOverride, OutdoorWetBulbC: floatPtr(23.8), CoolingLoadKW: floatPtr(550), ChillerPowerKW: floatPtr(110), DurationHours: -1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(23.0), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(5.0), DurationHours: 0.5},
		// Lower load bin (22-24 C, 200-400 kW) to verify secondary sort by CoolingLoadMinKW.
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(22.5), CoolingLoadKW: floatPtr(300), ObservedCOP: floatPtr(4.0), DurationHours: 0.25},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(22.5), CoolingLoadKW: floatPtr(300), ObservedCOP: floatPtr(4.4), DurationHours: 0.25},
	}

	summary := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if summary.MatchedBinCount != 3 {
		t.Fatalf("MatchedBinCount = %d, want 3", summary.MatchedBinCount)
	}
	if summary.MatchedBaselineSamples != 4 || summary.MatchedAISamples != 3 {
		t.Fatalf("matched sample counts = (%d baseline, %d ai), want (4, 3)", summary.MatchedBaselineSamples, summary.MatchedAISamples)
	}

	// Deterministic ordering by (WetBulbMinC, CoolingLoadMinKW).
	if summary.Cells[0].WetBulbMinC != 22 || summary.Cells[0].CoolingLoadMinKW != 200 ||
		summary.Cells[1].WetBulbMinC != 22 || summary.Cells[1].CoolingLoadMinKW != 400 ||
		summary.Cells[2].WetBulbMinC != 24 || summary.Cells[2].CoolingLoadMinKW != 600 {
		t.Fatalf("cells not deterministically sorted: %+v", summary.Cells)
	}

	// Bin [22, 24) x [400, 600):
	// Baseline: 450*0.25=112.5 kWh @ COP 4.5 (25 kWh elec) + 550*0.25=137.5 kWh @ COP 5.0 (27.5 kWh elec)
	// => 250 / 52.5 = 4.76190476...
	// AI: 500*0.5=250 kWh @ COP 5.0 (50 kWh elec)
	// Saved: 52.5 - 50 = 2.5 kWh
	mid := summary.Cells[1]
	if mid.BaselineSamples != 2 || mid.AISamples != 1 {
		t.Fatalf("mid cell sample counts = (%d, %d), want (2, 1)", mid.BaselineSamples, mid.AISamples)
	}
	if math.Abs(mid.BaselineCOP-4.7619) > 1e-4 || mid.AICOP != 5.0 {
		t.Fatalf("mid cell COPs = (%v, %v), want (4.7619, 5.0)", mid.BaselineCOP, mid.AICOP)
	}
	if math.Abs(mid.COPUpliftPct-5.0) > 1e-4 {
		t.Fatalf("mid cell uplift = %v, want 5.0", mid.COPUpliftPct)
	}
	if math.Abs(mid.EstimatedSavedKWh-2.5) > 1e-4 {
		t.Fatalf("mid cell saved kWh = %v, want 2.5", mid.EstimatedSavedKWh)
	}

	// Bin [22, 24) x [200, 400): 75 * (1/4.0 - 1/4.4) = 1.704545...
	// Bin [24, 26) x [600, 800): 350 * (1/3.5 - 1/4.2) = 16.666666...
	// Total saved = 1.704545... + 2.5 + 16.666666... = 20.871212...
	if math.Abs(summary.TotalEstimatedSavedKWh-20.8712) > 1e-4 {
		t.Fatalf("TotalEstimatedSavedKWh = %v, want 20.8712", summary.TotalEstimatedSavedKWh)
	}
	if summary.WeightedBaselineCOP == nil || summary.WeightedAICOP == nil || summary.WeightedCOPUpliftPct == nil {
		t.Fatalf("weighted summary fields must be populated: %+v", summary)
	}
}

func TestEvaluateBivariateCOPRejectsUnmatchedWeatherAndLoadBias(t *testing.T) {
	// Cool weather has only baseline ticks (naturally high COP 6.0);
	// Hot weather has only AI ticks (naturally lower COP 3.5).
	// Without matched binning, AI would look worse despite never competing under the same conditions.
	samples := []ControlTickSample{
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(18.5), CoolingLoadKW: floatPtr(300), ObservedCOP: floatPtr(6.0), DurationHours: 1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(28.5), CoolingLoadKW: floatPtr(900), ObservedCOP: floatPtr(3.5), DurationHours: 1},
	}

	empty := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if empty.MatchedBinCount != 0 || empty.MatchedAISamples != 0 || empty.MatchedBaselineSamples != 0 {
		t.Fatalf("expected zero matched bins, got %+v", empty)
	}
	if empty.WeightedBaselineCOP != nil || empty.WeightedAICOP != nil || empty.WeightedCOPUpliftPct != nil {
		t.Fatalf("expected nil weighted metrics when unmatched, got %+v", empty)
	}
	if empty.Cells == nil || len(empty.Cells) != 0 {
		t.Fatalf("expected non-nil empty Cells slice, got %#v", empty.Cells)
	}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	cellsVal, ok := decoded["cells"].([]any)
	if !ok || len(cellsVal) != 0 {
		t.Fatalf("expected JSON cells to encode as [], got %s", string(encoded))
	}

	// Add a matched bin where AI actually beats baseline, plus require MinSamplesPerModeBin=2 on another bin.
	samples = append(samples,
		// Matched bin (24-26 C, 400-600 kW) with 2 baseline + 2 AI ticks.
		ControlTickSample{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(24.2), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(4.0), DurationHours: 0.25},
		ControlTickSample{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(25.1), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(4.0), DurationHours: 0.25},
		ControlTickSample{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24.8), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(4.4), DurationHours: 0.25},
		ControlTickSample{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(25.5), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(4.4), DurationHours: 0.25},
		// Under-threshold bin (26-28 C, 400-600 kW) with 2 baseline but only 1 AI tick.
		ControlTickSample{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(26.5), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(3.8), DurationHours: 0.25},
		ControlTickSample{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(26.7), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(3.8), DurationHours: 0.25},
		ControlTickSample{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(26.6), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(4.5), DurationHours: 0.25},
	)

	summary := EvaluateBivariateCOP(samples, BivariateBinConfig{
		WetBulbStepC:         2.0,
		CoolingLoadStepKW:    200.0,
		MinSamplesPerModeBin: 2,
	})
	if summary.MatchedBinCount != 1 {
		t.Fatalf("MatchedBinCount = %d, want 1", summary.MatchedBinCount)
	}
	if summary.Cells[0].WetBulbMinC != 24 || *summary.WeightedCOPUpliftPct != 10.0 {
		t.Fatalf("expected only matched [24,26) bin with +10%% uplift, got %+v", summary)
	}
}

func TestEvaluateBivariateCOPSkipsAIShadowAndUnknownModes(t *testing.T) {
	samples := []ControlTickSample{
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(24.0), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(4.0), DurationHours: 0.25},
		{Mode: ModeAIShadow, OutdoorWetBulbC: floatPtr(24.0), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(6.0), DurationHours: 0.25},
		{Mode: ControlTickMode("UNKNOWN_MODE"), OutdoorWetBulbC: floatPtr(24.0), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(7.0), DurationHours: 0.25},
	}

	withoutClosedLoop := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if withoutClosedLoop.MatchedBinCount != 0 {
		t.Fatalf("AI_SHADOW must not count toward matched bins, got %+v", withoutClosedLoop)
	}

	samples = append(samples, ControlTickSample{
		Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24.0), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(4.4), DurationHours: 0.25,
	})
	withClosedLoop := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if withClosedLoop.MatchedBinCount != 1 || withClosedLoop.MatchedAISamples != 1 || withClosedLoop.Cells[0].AICOP != 4.4 {
		t.Fatalf("AI_SHADOW polluted closed-loop COP comparison: %+v", withClosedLoop)
	}
}

func TestEvaluateBivariateCOPFiltersInvalidTelemetryAndCOP(t *testing.T) {
	samples := []ControlTickSample{
		// Valid pair at boundaries: CoolingLoadKW == 10.0 (with ChillerPowerKW == 5.0 -> COP 2.0) and ObservedCOP == 12.0.
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(-2.5), CoolingLoadKW: floatPtr(10.0), ChillerPowerKW: floatPtr(5.0), ChwSupplyTempC: floatPtr(7.0), ChwReturnTempC: floatPtr(10.5), ChwFlowM3H: floatPtr(120.0), DurationHours: 0.25},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(-2.1), CoolingLoadKW: floatPtr(60.0), ChillerPowerKW: floatPtr(5.0), ObservedCOP: floatPtr(12.0), ChwSupplyTempC: floatPtr(7.0), ChwReturnTempC: floatPtr(12.0), ChwFlowM3H: floatPtr(120.0), DurationHours: 0.25},

		// Invalid samples that must all be filtered out:
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: nil, CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(math.NaN()), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(math.Inf(1)), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: nil, ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(0), ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(9.99), ChillerPowerKW: floatPtr(5.0)}, // below cooling deadband
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(-100), ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(math.NaN()), ObservedCOP: floatPtr(5.0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(0)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(0.99)}, // below min COP
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(-3.2)},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(12.01)}, // above max COP
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(85.0)},  // standby spike COP 80+
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: floatPtr(math.Inf(-1))},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: nil, ChillerPowerKW: nil},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: nil, ChillerPowerKW: floatPtr(4.99)},                                            // standby power deadband
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(200), ObservedCOP: floatPtr(5.0), ChillerPowerKW: floatPtr(1.0)},                                   // standby power even with ObservedCOP
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ObservedCOP: nil, ChillerPowerKW: floatPtr(40.0)},                                            // derived COP 12.5 > 12
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), ChwFlowM3H: floatPtr(0.5)},                                    // below flow deadband
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), ChwSupplyTempC: floatPtr(8.0), ChwReturnTempC: floatPtr(8.0)}, // zero deltaT
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), ChwSupplyTempC: floatPtr(8.5), ChwReturnTempC: floatPtr(7.9)}, // negative deltaT
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), ChwSupplyTempC: floatPtr(math.NaN())},                         // NaN supply temp
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), DurationHours: math.NaN()},                                    // NaN duration
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), DurationHours: math.Inf(1)},                                   // Inf duration
	}

	summary := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if summary.MatchedBinCount != 1 || summary.MatchedAISamples != 1 || summary.MatchedBaselineSamples != 1 {
		t.Fatalf("invalid samples were not filtered cleanly: %+v", summary)
	}
	if summary.Incomplete || summary.ExcludedIncompleteSamples != 0 {
		t.Fatalf("expected no incomplete samples flagged, got %+v", summary)
	}
	cell := summary.Cells[0]
	if cell.WetBulbMinC != -4 || cell.WetBulbMaxC != -2 || cell.BaselineCOP != 2.0 || cell.AICOP != 12.0 {
		t.Fatalf("unexpected boundary cell: %+v", cell)
	}
}

func TestEvaluateBivariateCOPExcludesAndFlagsIncompleteSubmeters(t *testing.T) {
	samples := []ControlTickSample{
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(125), DurationHours: 0.25},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(100), DurationHours: 0.25},
		// Incomplete submeter tick (e.g. missing 1 chiller power meter -> inflated apparent COP 10.0) must be excluded and flagged.
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(50), Incomplete: true, DurationHours: 0.25},
		// Incomplete AI_SHADOW tick is outside closed-loop comparison so it is ignored silently.
		{Mode: ModeAIShadow, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(500), ChillerPowerKW: floatPtr(50), Incomplete: true, DurationHours: 0.25},
	}

	summary := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if !summary.Incomplete || summary.ExcludedIncompleteSamples != 1 {
		t.Fatalf("expected Incomplete=true and ExcludedIncompleteSamples=1, got %+v", summary)
	}
	if summary.MatchedBinCount != 1 || summary.MatchedAISamples != 1 || summary.Cells[0].AICOP != 5.0 {
		t.Fatalf("incomplete submeter sample leaked into COP cell: %+v", summary)
	}
}

func TestEvaluateBivariateCOPHandlesNegativeUpliftAndBalancedWeights(t *testing.T) {
	// Negative uplift: AI COP 3.6 vs Baseline COP 4.0 (-10% uplift) on 720 kW * 1h = 720 kWh cooling.
	// Counterfactual baseline elec = 720 / 4.0 = 180 kWh; AI elec = 720 / 3.6 = 200 kWh; saved = -20 kWh.
	samples := []ControlTickSample{
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(720), ObservedCOP: floatPtr(4.0), DurationHours: 1.0},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(24.5), CoolingLoadKW: floatPtr(720), ObservedCOP: floatPtr(3.6), DurationHours: 1.0},
	}

	summary := EvaluateBivariateCOP(samples, BivariateBinConfig{})
	if summary.MatchedBinCount != 1 {
		t.Fatalf("MatchedBinCount = %d, want 1", summary.MatchedBinCount)
	}
	cell := summary.Cells[0]
	if cell.COPUpliftPct != -10.0 || cell.EstimatedSavedKWh != -20.0 {
		t.Fatalf("expected negative uplift -10%% and -20 kWh saved, got %+v", cell)
	}
	if *summary.WeightedCOPUpliftPct != -10.0 || summary.TotalEstimatedSavedKWh != -20.0 {
		t.Fatalf("expected negative summary uplift -10%% and -20 kWh saved, got %+v", summary)
	}

	// Verify AI-cooling-weighted normalization eliminates Simpson's paradox across imbalanced matched bins:
	// Bin A (cool): 4 baseline ticks @ COP 5.0 vs 1 AI tick @ COP 5.5 (+10%).
	// Bin B (hot):  1 baseline tick @ COP 3.0 vs 4 AI ticks @ COP 3.3 (+10%).
	paradox := []ControlTickSample{
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(20.5), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(5.0), DurationHours: 1},
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(20.5), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(5.0), DurationHours: 1},
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(20.5), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(5.0), DurationHours: 1},
		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(20.5), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(5.0), DurationHours: 1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(20.5), CoolingLoadKW: floatPtr(400), ObservedCOP: floatPtr(5.5), DurationHours: 1},

		{Mode: ModeRuleBaseline, OutdoorWetBulbC: floatPtr(28.5), CoolingLoadKW: floatPtr(800), ObservedCOP: floatPtr(3.0), DurationHours: 1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(28.5), CoolingLoadKW: floatPtr(800), ObservedCOP: floatPtr(3.3), DurationHours: 1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(28.5), CoolingLoadKW: floatPtr(800), ObservedCOP: floatPtr(3.3), DurationHours: 1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(28.5), CoolingLoadKW: floatPtr(800), ObservedCOP: floatPtr(3.3), DurationHours: 1},
		{Mode: ModeAIClosedLoop, OutdoorWetBulbC: floatPtr(28.5), CoolingLoadKW: floatPtr(800), ObservedCOP: floatPtr(3.3), DurationHours: 1},
	}
	balanced := EvaluateBivariateCOP(paradox, BivariateBinConfig{})
	if balanced.WeightedCOPUpliftPct == nil || math.Abs(*balanced.WeightedCOPUpliftPct-10.0) > 1e-4 {
		t.Fatalf("expected +10.0%% weighted uplift without Simpson's paradox, got %+v", balanced)
	}
}
