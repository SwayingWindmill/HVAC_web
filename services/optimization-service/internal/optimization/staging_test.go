package optimization

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func syntheticStagingChillerHistory(at time.Time, devices map[string]float64) []PlantBucket {
	start := at.UTC().Truncate(copBucketMinutes*time.Minute).AddDate(0, 0, -copHistoryDays)
	buckets := make([]PlantBucket, 0)
	// Generate 14 days * 96 buckets/day of diverse operating points per device so FitPlant builds a clean quadratic COP curve
	// that peaks near PLR = 0.72 and drops sharply at low part-load (PLR = 0.25).
	for step := 0; step < 14*96; step++ {
		when := start.Add(time.Duration(step) * copBucketMinutes * time.Minute)
		// Cycle PLR between 0.22 and 0.90 across buckets; vary supply temp between 6.0 and 8.5 C.
		plr := 0.22 + 0.68*float64(step%17)/16.0
		supplyC := 6.0 + 2.5*float64((step/17)%5)/4.0
		cwLeavingC := 28.0 + 4.0*float64((step/5)%4)/3.0
		totalCooling := 0.0
		for deviceID, efficiencyScale := range devices {
			ratedCap := 572.0
			loadKW := ratedCap * plr
			// Realistic centrifugal/screw chiller COP peaking near PLR ~ 0.72.
			trueCOP := efficiencyScale * (3.1 + 4.2*plr - 2.9*plr*plr + 0.08*(supplyC-7.0) - 0.03*(cwLeavingC-30.0))
			powerKW := loadKW / trueCOP
			enteringC := supplyC + math.Max(1.0, 5.0*plr)
			buckets = append(buckets, PlantBucket{
				Bucket:       when,
				DeviceID:     deviceID,
				PowerKW:      ptr(powerKW),
				ChwLeavingC:  ptr(supplyC),
				ChwEnteringC: ptr(enteringC),
				CwLeavingC:   ptr(cwLeavingC),
				RatedPowerKW: ptr(127.5),
			})
			totalCooling += loadKW
		}
		buckets = append(buckets, PlantBucket{
			Bucket:    when,
			DeviceID:  "01a14000-4000-7000-8000-000000000009", // btu-calc
			CoolingKW: ptr(totalCooling),
		})
	}
	return buckets
}

func testSongyaxuanUnits(at time.Time, powerA, powerB, powerC, powerD *float64) []ChillerUnitInput {
	bindings := SongyaxuanChillerMeterBindings()
	powers := map[string]*float64{
		"chiller-a": powerA,
		"chiller-b": powerB,
		"chiller-c": powerC,
		"chiller-d": powerD,
	}
	units := make([]ChillerUnitInput, 0, len(bindings))
	for _, b := range bindings {
		unit := ChillerUnitInput{
			AssetID:         b.AssetID,
			AssetCode:       b.AssetCode,
			AssetName:       b.AssetName,
			MeterDeviceID:   b.MeterDeviceID,
			MeterSourceKey:  b.MeterSourceKey,
			RatedCapacityKW: b.RatedCapacityKW,
			RatedPowerKW:    b.RatedPowerKW,
			RunStateQuality: "GOOD",
			RunStateAt:      at.Add(-30 * time.Second),
			ChwLeavingC:     &TelemetryValue{DeviceID: b.MeterDeviceID, Value: 7.0, Unit: "Cel", Quality: "GOOD", SampledAt: at.Add(-30 * time.Second)},
			CwLeavingC:      &TelemetryValue{DeviceID: b.MeterDeviceID, Value: 29.5, Unit: "Cel", Quality: "GOOD", SampledAt: at.Add(-30 * time.Second)},
		}
		p := powers[b.AssetCode]
		if p != nil && *p > DefaultChillerRunningPowerKW {
			unit.RunState = RunStateRunning
			unit.Power = &TelemetryValue{DeviceID: b.MeterDeviceID, SourceKey: b.MeterSourceKey, PointKey: "power_total", Value: *p, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-30 * time.Second)}
		} else {
			unit.RunState = RunStateStopped
			stoppedAt := at.Add(-15 * time.Minute)
			unit.StoppedSince = &stoppedAt
			if p != nil {
				unit.Power = &TelemetryValue{DeviceID: b.MeterDeviceID, SourceKey: b.MeterSourceKey, PointKey: "power_total", Value: *p, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-30 * time.Second)}
			}
		}
		units = append(units, unit)
	}
	return units
}

func TestChillerStageDownSongyaxuanFourChillerPartialLoad(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	// Fit Songyaxuan meters: unit3=chiller-a, unit4=chiller-b, unit1=chiller-c (degraded 0.85), unit2=chiller-d.
	buckets := syntheticStagingChillerHistory(at, map[string]float64{
		"01a14000-4000-7000-8000-000000000005": 1.00, // ammeter-unit3 -> chiller-a
		"01a14000-4000-7000-8000-000000000006": 1.00, // ammeter-unit4 -> chiller-b
		"01a14000-4000-7000-8000-000000000003": 0.85, // ammeter-unit1 -> chiller-c (least efficient)
		"01a14000-4000-7000-8000-000000000004": 0.98, // ammeter-unit2 -> chiller-d
	})
	to := at.Truncate(copBucketMinutes * time.Minute)
	from := to.AddDate(0, 0, -copHistoryDays)
	fit := FitPlant(buckets, from, to)
	if len(fit.usable()) != 4 {
		t.Fatalf("expected 4 fitted Songyaxuan chillers, got %+v", fit.Chillers)
	}

	// Partial-load scenario: A, C, D running (~540 kW cooling load across 3 chillers => ~31% PLR each); B stopped.
	// Shutting down C leaves A+D = 1144 kW capacity (>= 540*1.15 = 621 kW), raises PLR to ~47% and replaces fouled C.
	snapshot := StagingSnapshot{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01a14000-5000-7000-8000-000000000001",
		EvaluatedAt: at,
		PlantCoolingLoad: &TelemetryValue{
			DeviceID: "01a14000-4000-7000-8000-000000000009", SourceKey: "btu-calc", PointKey: "instant_cooling_capacity",
			Value: 540, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-20 * time.Second),
		},
		Chillers:    testSongyaxuanUnits(at, ptr(56.0), ptr(0.2), ptr(74.0), ptr(58.0)),
		ChillerFits: fit.Chillers,
	}

	eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging error: %v", err)
	}
	if eval.ConfirmationMode != ConfirmationModeManual {
		t.Fatalf("ConfirmationMode = %q, want MANUAL_CONFIRM", eval.ConfirmationMode)
	}
	if len(eval.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d (%+v); diagnostics=%+v", len(eval.Recommendations), eval.Recommendations, eval.Diagnostics)
	}
	rec := eval.Recommendations[0]
	if rec.Kind != RecommendationKindChillerStageDown {
		t.Fatalf("Kind = %q, want %s", rec.Kind, RecommendationKindChillerStageDown)
	}
	if rec.ConfirmationMode != ConfirmationModeManual || !rec.RequiresSupervisorReview || !rec.RequiresTOTP {
		t.Fatalf("confirmation safety metadata missing: %+v", rec)
	}
	if rec.SuggestedCapability != SuggestedCapabilityStopUnitSequence {
		t.Fatalf("SuggestedCapability = %q, want %s", rec.SuggestedCapability, SuggestedCapabilityStopUnitSequence)
	}
	if rec.TargetAssetID != "01a14000-3000-7000-8000-000000000003" || rec.TargetAssetCode != "chiller-c" {
		t.Fatalf("target = (%s, %s), want chiller-c (least efficient unit)", rec.TargetAssetID, rec.TargetAssetCode)
	}
	if rec.PredictedPowerSavingKW < DefaultMinimumPowerSavingKW {
		t.Fatalf("PredictedPowerSavingKW = %.2f, want >= %.2f", rec.PredictedPowerSavingKW, DefaultMinimumPowerSavingKW)
	}
	if rec.EstimatedHourlySavingKWh != rec.PredictedPowerSavingKW {
		t.Fatalf("hourly kWh = %.2f != predicted kW %.2f", rec.EstimatedHourlySavingKWh, rec.PredictedPowerSavingKW)
	}
	if math.Abs(rec.EstimatedShiftSavingKWh-round(rec.PredictedPowerSavingKW*DefaultStagingShiftHours, 2)) > 0.05 {
		t.Fatalf("shift kWh = %.2f, want ~8x %.2f", rec.EstimatedShiftSavingKWh, rec.PredictedPowerSavingKW)
	}
	if rec.COPBefore == nil || rec.ProjectedCOPAfter == nil || *rec.ProjectedCOPAfter <= *rec.COPBefore {
		t.Fatalf("expected COP improvement, got before=%v after=%v", rec.COPBefore, rec.ProjectedCOPAfter)
	}
	if rec.RemainingCapacityKW == nil || *rec.RemainingCapacityKW < 540*(1+DefaultHeadroomReserveFraction) {
		t.Fatalf("headroom check violated: remaining=%v", rec.RemainingCapacityKW)
	}
	if !strings.Contains(rec.Rationale, "C 机组") || len(rec.Preconditions) < 3 {
		t.Fatalf("rationale/preconditions incomplete: rationale=%q preconditions=%v", rec.Rationale, rec.Preconditions)
	}
}

func TestChillerStageDownSongyaxuanMeterAliasResolution(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	fitA := ChillerFit{
		DeviceID:          "ammeter-unit3", // chiller-a meter
		Status:            ChillerFitted,
		CapacityReference: &CapacityReference{KW: 572, Source: "OBSERVED_P99_LOAD"},
		Coefficients:      map[string]float64{"intercept": 2.9, "loadRatio": 3.8, "loadRatioSquared": -2.2, "chilledWaterSupplyTempC": 0.08},
	}
	fitB := ChillerFit{
		DeviceID:          "ammeter-unit4", // chiller-b meter
		Status:            ChillerFitted,
		CapacityReference: &CapacityReference{KW: 572, Source: "OBSERVED_P99_LOAD"},
		Coefficients:      map[string]float64{"intercept": 2.5, "loadRatio": 3.0, "loadRatioSquared": -1.8, "chilledWaterSupplyTempC": 0.08},
	}
	snapshot := StagingSnapshot{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01a14000-5000-7000-8000-000000000001",
		EvaluatedAt: at,
		PlantCoolingLoad: &TelemetryValue{
			SourceKey: "btu-calc", Value: 360, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute),
		},
		Chillers: []ChillerUnitInput{
			{
				MeterSourceKey:  "ammeter-unit3",
				RunState:        RunStateRunning,
				RunStateQuality: "GOOD",
				RunStateAt:      at.Add(-time.Minute),
				Power:           &TelemetryValue{Value: 58, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
				ChwLeavingC:     &TelemetryValue{Value: 7.0, Unit: "Cel", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
			},
			{
				MeterSourceKey:  "ammeter-unit4",
				RunState:        RunStateRunning,
				RunStateQuality: "GOOD",
				RunStateAt:      at.Add(-time.Minute),
				Power:           &TelemetryValue{Value: 68, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
				ChwLeavingC:     &TelemetryValue{Value: 7.0, Unit: "Cel", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
			},
		},
		ChillerFits: []ChillerFit{fitA, fitB},
	}

	eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(eval.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation from Songyaxuan meter alias lookup, got %+v (diags=%+v)", eval.Recommendations, eval.Diagnostics)
	}
	rec := eval.Recommendations[0]
	if rec.TargetAssetCode != "chiller-b" || rec.TargetAssetID != "01a14000-3000-7000-8000-000000000002" {
		t.Fatalf("expected ammeter-unit4 to resolve to chiller-b (01a14000-3000-7000-8000-000000000002), got %+v", rec)
	}
}

func TestChillerStageDownHeadroomFailure(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	fit := func(id string) ChillerFit {
		return ChillerFit{
			DeviceID:          id,
			Status:            ChillerFitted,
			CapacityReference: &CapacityReference{KW: 572, Source: "OBSERVED_P99_LOAD"},
			Coefficients:      map[string]float64{"intercept": 3.0, "loadRatio": 4.0, "loadRatioSquared": -2.4, "chilledWaterSupplyTempC": 0.08},
		}
	}
	// Remaining capacity of 1 chiller = 572 kW. At 510 kW cooling load, reserve is (572-510)/510 = 12.16% < 15%.
	snapshot := StagingSnapshot{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01a14000-5000-7000-8000-000000000001",
		EvaluatedAt: at,
		PlantCoolingLoad: &TelemetryValue{
			SourceKey: "btu-calc", Value: 510, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute),
		},
		Chillers:    testSongyaxuanUnits(at, ptr(80.0), ptr(82.0), nil, nil),
		ChillerFits: []ChillerFit{fit("ammeter-unit3"), fit("ammeter-unit4")},
	}

	eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(eval.Recommendations) != 0 {
		t.Fatalf("headroom check failed: expected 0 recommendations, got %+v", eval.Recommendations)
	}
	if len(eval.Diagnostics) == 0 || eval.Diagnostics[0].Reason != "HEADROOM_INSUFFICIENT" {
		t.Fatalf("expected HEADROOM_INSUFFICIENT diagnostic, got %+v", eval.Diagnostics)
	}

	// Verify configurable reserve margin: 470 kW passes 15% (572 >= 540.5) but fails 25% (572 < 587.5).
	snapshot.PlantCoolingLoad.Value = 470
	strictPolicy := DefaultStagingPolicy()
	strictPolicy.HeadroomReserveFraction = 0.25
	evalStrict, err := EvaluateStaging(snapshot, strictPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if len(evalStrict.Recommendations) != 0 || evalStrict.Diagnostics[0].Reason != "HEADROOM_INSUFFICIENT" {
		t.Fatalf("expected strict 25%% reserve margin to block 470 kW load, got recs=%+v diags=%+v", evalStrict.Recommendations, evalStrict.Diagnostics)
	}
}

func TestChillerStageDownGracefulNoOpOnMissingOrStaleTelemetry(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	baseFit := ChillerFit{
		DeviceID:          "ammeter-unit3",
		Status:            ChillerFitted,
		CapacityReference: &CapacityReference{KW: 572},
		Coefficients:      map[string]float64{"intercept": 3.0, "loadRatio": 4.0, "loadRatioSquared": -2.4, "chilledWaterSupplyTempC": 0.08},
	}

	cases := []struct {
		name       string
		mutate     func(*StagingSnapshot)
		wantReason string
	}{
		{
			name:       "missing_btu_calc",
			mutate:     func(s *StagingSnapshot) { s.PlantCoolingLoad = nil },
			wantReason: "COOLING_LOAD_MISSING",
		},
		{
			name:       "stale_btu_calc",
			mutate:     func(s *StagingSnapshot) { s.PlantCoolingLoad.SampledAt = at.Add(-20 * time.Minute) },
			wantReason: "COOLING_LOAD_STALE",
		},
		{
			name:       "non_good_btu_calc",
			mutate:     func(s *StagingSnapshot) { s.PlantCoolingLoad.Quality = "STALE" },
			wantReason: "COOLING_LOAD_QUALITY_STALE",
		},
		{
			name:       "zero_cooling_load",
			mutate:     func(s *StagingSnapshot) { s.PlantCoolingLoad.Value = 0 },
			wantReason: "COOLING_LOAD_NON_POSITIVE",
		},
		{
			name: "missing_running_meter_power",
			mutate: func(s *StagingSnapshot) {
				s.Chillers[0].Power = nil
				s.Chillers[0].RunState = RunStateRunning
			},
			wantReason: "CHILLER_METER_MISSING",
		},
		{
			name: "stale_running_meter_power",
			mutate: func(s *StagingSnapshot) {
				s.Chillers[0].Power.SampledAt = at.Add(-15 * time.Minute)
			},
			wantReason: "CHILLER_METER_STALE",
		},
		{
			name: "only_one_chiller_running",
			mutate: func(s *StagingSnapshot) {
				s.Chillers = testSongyaxuanUnits(at, ptr(75.0), nil, nil, nil)
			},
			wantReason: "SINGLE_CHILLER_RUNNING",
		},
		{
			name: "chiller_starting_suppresses_stage_down",
			mutate: func(s *StagingSnapshot) {
				s.Chillers[2].RunState = RunStateStarting
				s.Chillers[2].RunStateQuality = "GOOD"
				s.Chillers[2].RunStateAt = at.Add(-10 * time.Second)
			},
			wantReason: "PLANT_CHILLER_STARTING",
		},
		{
			name: "condenser_model_missing_cw_temp",
			mutate: func(s *StagingSnapshot) {
				s.Chillers[0].CwLeavingC = nil
				s.Chillers[1].CwLeavingC = nil
				s.ChillerFits = []ChillerFit{
					{
						DeviceID:          "ammeter-unit3",
						Status:            ChillerFitted,
						CapacityReference: &CapacityReference{KW: 572},
						Coefficients:      map[string]float64{"intercept": 4.0, "loadRatio": 3.0, "loadRatioSquared": -2.0, "chilledWaterSupplyTempC": 0.08, "condenserReturnTempC": -0.05},
					},
					{
						DeviceID:          "ammeter-unit4",
						Status:            ChillerFitted,
						CapacityReference: &CapacityReference{KW: 572},
						Coefficients:      map[string]float64{"intercept": 4.0, "loadRatio": 3.0, "loadRatioSquared": -2.0, "chilledWaterSupplyTempC": 0.08, "condenserReturnTempC": -0.05},
					},
				}
			},
			wantReason: "CONDENSER_TEMPERATURE_MISSING",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fitB := baseFit
			fitB.DeviceID = "ammeter-unit4"
			snapshot := StagingSnapshot{
				TenantID:    "01990000-3000-7000-8000-000000000001",
				SiteID:      "01a14000-5000-7000-8000-000000000001",
				EvaluatedAt: at,
				PlantCoolingLoad: &TelemetryValue{
					SourceKey: "btu-calc", Value: 360, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute),
				},
				Chillers:    testSongyaxuanUnits(at, ptr(60.0), ptr(62.0), nil, nil),
				ChillerFits: []ChillerFit{baseFit, fitB},
			}
			tc.mutate(&snapshot)
			eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(eval.Recommendations) != 0 {
				t.Fatalf("expected 0 recommendations for %s, got %+v", tc.name, eval.Recommendations)
			}
			if len(eval.Diagnostics) == 0 || eval.Diagnostics[0].Reason != tc.wantReason {
				t.Fatalf("diagnostic reason = %+v, want %s", eval.Diagnostics, tc.wantReason)
			}
		})
	}
}

func TestChillerStageDownCOPEdgeCases(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	baseSnapshot := func(fitA, fitB ChillerFit, loadKW, pA, pB float64) StagingSnapshot {
		return StagingSnapshot{
			TenantID:    "01990000-3000-7000-8000-000000000001",
			SiteID:      "01a14000-5000-7000-8000-000000000001",
			EvaluatedAt: at,
			PlantCoolingLoad: &TelemetryValue{
				SourceKey: "btu-calc", Value: loadKW, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute),
			},
			Chillers:    testSongyaxuanUnits(at, ptr(pA), ptr(pB), nil, nil),
			ChillerFits: []ChillerFit{fitA, fitB},
		}
	}

	t.Run("cop_not_improved_suppresses_stage_down", func(t *testing.T) {
		// Current: 450 kW load / (50 + 50 kW) = COP 4.50. At higher PLR on 1 chiller (~0.787), curve yields COP 4.10 -> higher power (109.8 kW).
		fitA := ChillerFit{
			DeviceID: "ammeter-unit3", Status: ChillerFitted, CapacityReference: &CapacityReference{KW: 572},
			Coefficients: map[string]float64{"intercept": 4.9, "loadRatio": -1.5, "loadRatioSquared": 0.1, "chilledWaterSupplyTempC": 0.05},
		}
		fitB := fitA
		fitB.DeviceID = "ammeter-unit4"
		eval, err := EvaluateStaging(baseSnapshot(fitA, fitB, 450, 50, 50), DefaultStagingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if len(eval.Recommendations) != 0 || eval.Diagnostics[0].Reason != "COP_NOT_IMPROVED" {
			t.Fatalf("expected COP_NOT_IMPROVED, got recs=%+v diags=%+v", eval.Recommendations, eval.Diagnostics)
		}
	})

	t.Run("hysteresis_rejects_marginal_kw_saving", func(t *testing.T) {
		// Current power = 44 + 44 = 88 kW at 360 kW cooling (COP=4.09). Projected single chiller power ~85.5 kW -> saving ~2.5 kW < 5.0 kW.
		fitA := ChillerFit{
			DeviceID: "ammeter-unit3", Status: ChillerFitted, CapacityReference: &CapacityReference{KW: 572},
			Coefficients: map[string]float64{"intercept": 3.5, "loadRatio": 0.8, "loadRatioSquared": -0.3, "chilledWaterSupplyTempC": 0.05},
		}
		fitB := fitA
		fitB.DeviceID = "ammeter-unit4"
		eval, err := EvaluateStaging(baseSnapshot(fitA, fitB, 360, 44, 44), DefaultStagingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if len(eval.Recommendations) != 0 || eval.Diagnostics[0].Reason != "SAVING_BELOW_THRESHOLD" {
			t.Fatalf("expected SAVING_BELOW_THRESHOLD, got recs=%+v diags=%+v", eval.Recommendations, eval.Diagnostics)
		}
	})

	t.Run("implausible_predicted_cop_blocked", func(t *testing.T) {
		fitA := ChillerFit{
			DeviceID: "ammeter-unit3", Status: ChillerFitted, CapacityReference: &CapacityReference{KW: 572},
			Coefficients: map[string]float64{"intercept": -10.0, "loadRatio": 1.0, "loadRatioSquared": 0.0, "chilledWaterSupplyTempC": 0.05},
		}
		fitB := fitA
		fitB.DeviceID = "ammeter-unit4"
		eval, err := EvaluateStaging(baseSnapshot(fitA, fitB, 360, 60, 60), DefaultStagingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if len(eval.Recommendations) != 0 || eval.Diagnostics[0].Reason != "MODEL_PREDICTION_IMPLAUSIBLE" {
			t.Fatalf("expected MODEL_PREDICTION_IMPLAUSIBLE, got recs=%+v diags=%+v", eval.Recommendations, eval.Diagnostics)
		}
	})
}

func TestIdleAuxiliaryStopDebounceAndSafetyInterlocks(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)

	t.Run("triggers_only_after_5m_continuous_chiller_stop_across_aux_types", func(t *testing.T) {
		chillerStoppedSince := at.Add(-8 * time.Minute)
		chillerStoppedHist := []RunStateSample{
			{State: RunStateRunning, Quality: "GOOD", SampledAt: at.Add(-12 * time.Minute)},
			{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-8 * time.Minute)},
			{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-5 * time.Minute)},
			{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-1 * time.Minute)},
		}
		snapshot := StagingSnapshot{
			TenantID:    "01990000-3000-7000-8000-000000000001",
			SiteID:      "01a14000-5000-7000-8000-000000000001",
			EvaluatedAt: at,
			Chillers: []ChillerUnitInput{
				{
					AssetID: "01a14000-3000-7000-8000-000000000001", AssetCode: "chiller-a", AssetName: "A 机组",
					RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-1 * time.Minute),
					RunStateHistory: chillerStoppedHist,
					Power:           &TelemetryValue{Value: 0.4, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-1 * time.Minute)},
				},
				{
					AssetID: "01a14000-3000-7000-8000-000000000002", AssetCode: "chiller-b", AssetName: "B 机组",
					RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
					StoppedSince: &chillerStoppedSince,
				},
			},
			Auxiliaries: []AuxiliaryUnitInput{
				{
					AssetID: "01a14000-3000-7000-8000-000000000009", AssetCode: "chwp-1", AssetName: "1 号冷冻泵",
					AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
					Power: &TelemetryValue{Value: 18.5, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-30 * time.Second)},
				},
				{
					AssetID: "01a14000-3000-7000-8000-00000000000d", AssetCode: "cwp-1", AssetName: "1 号冷却泵",
					AssetType: "CWP", RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
					RatedPowerKW:     22.0,
					ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000002"},
				},
				{
					AssetID: "01a14000-3000-7000-8000-000000000005", AssetCode: "cooling-tower-1", AssetName: "1 号冷却塔",
					AssetType: AssetTypeCoolingTower, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
					Power: &TelemetryValue{Value: 11.0, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-30 * time.Second)},
				},
			},
			Edges: []HydraulicEdge{
				{FromAssetID: "01a14000-3000-7000-8000-000000000009", ToAssetID: "01a14000-3000-7000-8000-000000000001", Role: HydraulicEdgeServes},
				{FromAssetID: "01a14000-3000-7000-8000-000000000009", ToAssetID: "01a14000-3000-7000-8000-000000000002", Role: HydraulicEdgeServes},
				{FromAssetID: "01a14000-3000-7000-8000-000000000005", ToAssetID: "01a14000-3000-7000-8000-000000000001", Role: HydraulicEdgeServes},
			},
		}

		eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if len(eval.Recommendations) != 3 {
			t.Fatalf("expected 3 IDLE_AUXILIARY_STOP recommendations (CHWP, CWP, COOLING_TOWER), got %d: %+v (diags=%+v)", len(eval.Recommendations), eval.Recommendations, eval.Diagnostics)
		}
		for _, rec := range eval.Recommendations {
			if rec.Kind != RecommendationKindIdleAuxiliaryStop || rec.ConfirmationMode != ConfirmationModeManual || rec.SuggestedCapability != SuggestedCapabilityStop {
				t.Fatalf("unexpected aux draft metadata: %+v", rec)
			}
			if rec.ContinuousIdleSeconds == nil || *rec.ContinuousIdleSeconds < 300 {
				t.Fatalf("idle duration below 5m: %+v", rec)
			}
			if !strings.Contains(rec.InterlockSafetyStatement, "所服务冷机均已停机超过 5 分钟") {
				t.Fatalf("missing required Chinese interlock safety statement: %q", rec.InterlockSafetyStatement)
			}
			if rec.PredictedPowerSavingKW <= 0 {
				t.Fatalf("expected positive power saving, got %+v", rec)
			}
		}
	})

	t.Run("suppresses_below_5m_debounce_and_safety_blocks", func(t *testing.T) {
		blockedCases := []struct {
			name       string
			chiller    ChillerUnitInput
			aux        AuxiliaryUnitInput
			wantReason string
		}{
			{
				name: "debounce_under_5m_via_history",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute),
					RunStateHistory: []RunStateSample{
						{State: RunStateRunning, Quality: "GOOD", SampledAt: at.Add(-4 * time.Minute)},
						{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-3 * time.Minute)},
						{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-1 * time.Minute)},
					},
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-1", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute), ServesChillerIDs: []string{"c1"}},
				wantReason: "IDLE_DEBOUNCE_NOT_MET",
			},
			{
				name: "paired_chiller_running",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute),
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-1", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute), ServesChillerIDs: []string{"c1"}},
				wantReason: "PAIRED_CHILLER_RUNNING",
			},
			{
				name: "paired_chiller_starting",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateStarting, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute),
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-1", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute), ServesChillerIDs: []string{"c1"}},
				wantReason: "PAIRED_CHILLER_STARTING",
			},
			{
				name: "paired_chiller_stale_state",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-25 * time.Minute),
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-1", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute), ServesChillerIDs: []string{"c1"}},
				wantReason: "CHILLER_STATE_STALE",
			},
			{
				name: "paired_chiller_contradictory_active_power",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute),
					Power: &TelemetryValue{Value: 18.0, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-1", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute), ServesChillerIDs: []string{"c1"}},
				wantReason: "PAIRED_CHILLER_DRAWING_POWER",
			},
			{
				name: "history_telemetry_gap_inside_debounce",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute),
					RunStateHistory: []RunStateSample{
						{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-18 * time.Minute)},
						{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-1 * time.Minute)},
					},
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-1", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute), ServesChillerIDs: []string{"c1"}},
				wantReason: "IDLE_HISTORY_TELEMETRY_GAP",
			},
			{
				name: "unmapped_served_chillers_suppressed",
				chiller: ChillerUnitInput{
					AssetID: "c1", AssetCode: "chiller-a", RunState: RunStateStopped, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute),
				},
				aux:        AuxiliaryUnitInput{AssetID: "p1", AssetCode: "chwp-main", AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-time.Minute)},
				wantReason: "NO_SERVED_CHILLERS_MAPPED",
			},
		}

		for _, tc := range blockedCases {
			t.Run(tc.name, func(t *testing.T) {
				snapshot := StagingSnapshot{
					TenantID:    "01990000-3000-7000-8000-000000000001",
					SiteID:      "01a14000-5000-7000-8000-000000000001",
					EvaluatedAt: at,
					Chillers:    []ChillerUnitInput{tc.chiller},
					Auxiliaries: []AuxiliaryUnitInput{tc.aux},
				}
				eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range eval.Recommendations {
					if r.Kind == RecommendationKindIdleAuxiliaryStop {
						t.Fatalf("expected suppression for %s, got recommendation %+v", tc.name, r)
					}
				}
				matched := false
				for _, d := range eval.Diagnostics {
					if d.Scope == RecommendationKindIdleAuxiliaryStop && d.Reason == tc.wantReason {
						matched = true
					}
				}
				if !matched {
					t.Fatalf("expected diagnostic reason %q for %s, got %+v", tc.wantReason, tc.name, eval.Diagnostics)
				}
			})
		}
	})
}

func TestClickHouseInputReaderReadsStagingSnapshot(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	var seenQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql := string(body)
		seenQueries = append(seenQueries, sql)
		writer.Header().Set("Content-Type", "application/x-ndjson")
		if strings.Contains(sql, "row_number() OVER") {
			rows := []string{
				`{"deviceId":"01a14000-4000-7000-8000-000000000009","sourceId":"btu-calc","telemetryKey":"instant_cooling_capacity","valueNumber":480,"valueString":null,"unit":"kW","quality":"GOOD","sampledAt":"2026-11-14T14:59:30.000Z"}`,
				`{"deviceId":"01a14000-4000-7000-8000-000000000003","sourceId":"ammeter-unit1","telemetryKey":"power_total","valueNumber":82,"valueString":null,"unit":"kW","quality":"GOOD","sampledAt":"2026-11-14T14:59:00.000Z"}`,
				`{"deviceId":"01a14000-4000-7000-8000-000000000003","sourceId":"ammeter-unit1","telemetryKey":"chw_leaving_temperature","valueNumber":7.1,"valueString":null,"unit":"Cel","quality":"GOOD","sampledAt":"2026-11-14T14:59:00.000Z"}`,
				`{"deviceId":"01a14000-4000-7000-8000-000000000003","sourceId":"ammeter-unit1","telemetryKey":"chiller.run_state","valueNumber":null,"valueString":"RUNNING","unit":"","quality":"GOOD","sampledAt":"2026-11-14T14:59:00.000Z"}`,
				`{"deviceId":"01a14000-4000-7000-8000-000000000004","sourceId":"ammeter-unit2","telemetryKey":"chiller.run_state","valueNumber":null,"valueString":"STOPPED","unit":"","quality":"GOOD","sampledAt":"2026-11-14T14:59:00.000Z"}`,
			}
			_, _ = writer.Write([]byte(strings.Join(rows, "\n") + "\n"))
			return
		}
		_, _ = writer.Write([]byte(
			`{"deviceId":"01a14000-4000-7000-8000-000000000004","sourceId":"ammeter-unit2","telemetryKey":"chiller.run_state","valueString":"STOPPED","quality":"GOOD","sampledAt":"2026-11-14T14:50:00.000Z"}` + "\n" +
				`{"deviceId":"01a14000-4000-7000-8000-000000000004","sourceId":"ammeter-unit2","telemetryKey":"chiller.run_state","valueString":"STOPPED","quality":"GOOD","sampledAt":"2026-11-14T14:59:00.000Z"}` + "\n",
		))
	}))
	defer server.Close()

	reader, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: server.URL, Energy: &energySeriesStub{}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.ReadStagingSnapshot(t.Context(), StagingSnapshotQuery{
		TenantID: "01990000-3000-7000-8000-000000000001",
		SiteID:   "01a14000-5000-7000-8000-000000000001",
		At:       at,
		Policy:   DefaultStagingPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenQueries) != 2 {
		t.Fatalf("expected 2 ClickHouse queries, got %d", len(seenQueries))
	}
	if strings.Contains(seenQueries[0], "quality = 'GOOD'") || strings.Contains(seenQueries[0], "quality='GOOD'") {
		t.Fatalf("latest query must not skip newer non-GOOD observation: %s", seenQueries[0])
	}
	if snapshot.PlantCoolingLoad == nil || snapshot.PlantCoolingLoad.Value != 480 {
		t.Fatalf("PlantCoolingLoad = %+v", snapshot.PlantCoolingLoad)
	}
	var chillerC, chillerD *ChillerUnitInput
	for i := range snapshot.Chillers {
		switch snapshot.Chillers[i].AssetCode {
		case "chiller-c":
			chillerC = &snapshot.Chillers[i]
		case "chiller-d":
			chillerD = &snapshot.Chillers[i]
		}
	}
	if chillerC == nil || chillerC.Power == nil || chillerC.Power.Value != 82 || chillerC.RunState != RunStateRunning {
		t.Fatalf("chiller-c not resolved cleanly: %+v", chillerC)
	}
	if chillerD == nil || chillerD.RunState != RunStateStopped || len(chillerD.RunStateHistory) != 2 {
		t.Fatalf("chiller-d history not populated: %+v", chillerD)
	}
}

type stagingSnapshotStub struct {
	snapshot StagingSnapshot
	err      error
}

func (stub stagingSnapshotStub) ReadStagingSnapshot(_ context.Context, query StagingSnapshotQuery) (StagingSnapshot, error) {
	if stub.err != nil {
		return StagingSnapshot{}, stub.err
	}
	out := stub.snapshot
	out.TenantID = query.TenantID
	out.SiteID = query.SiteID
	out.EvaluatedAt = query.At
	return out, nil
}

func TestOptimizationServiceAndHTTPStagingWiring(t *testing.T) {
	fixture := newOptimizationFixture(t)
	stagingEndpoint := "/v1/sites/" + testSite + "/optimization/staging-recommendations/latest"

	// Unconfigured staging evaluator returns clean 404.
	missing := fixture.call(t, http.MethodGet, stagingEndpoint, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if missing.Code != http.StatusNotFound || strings.TrimSpace(missing.Body.String()) != `{"error":"optimization_staging_not_found"}` {
		t.Fatalf("unconfigured status=%d body=%s", missing.Code, missing.Body.String())
	}

	// Configure staging evaluator independently of zone_temperature or published recommendations.
	buckets := syntheticStagingChillerHistory(fixture.now, map[string]float64{
		"01a14000-4000-7000-8000-000000000005": 1.00,
		"01a14000-4000-7000-8000-000000000006": 1.00,
		"01a14000-4000-7000-8000-000000000003": 0.85,
	})
	snapshot := StagingSnapshot{
		PlantCoolingLoad: &TelemetryValue{
			DeviceID: "01a14000-4000-7000-8000-000000000009", SourceKey: "btu-calc", Value: 520, Unit: "kW", Quality: "GOOD", SampledAt: fixture.now.Add(-time.Minute),
		},
		Chillers: testSongyaxuanUnits(fixture.now, ptr(55.0), ptr(55.0), ptr(74.0), nil),
		Auxiliaries: []AuxiliaryUnitInput{
			{
				AssetID: "01a14000-3000-7000-8000-00000000000a", AssetCode: "chwp-2", AssetName: "2 号冷冻泵",
				AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: fixture.now.Add(-time.Minute),
				RatedPowerKW: 18.5, ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000004"},
			},
		},
	}
	history := &fixedPlantHistory{buckets: buckets}
	fixture.service.WithStaging(&StagingEvaluator{
		History:  history,
		Snapshot: stagingSnapshotStub{snapshot: snapshot},
		Policy:   DefaultStagingPolicy(),
	})

	resp := fixture.call(t, http.MethodGet, stagingEndpoint, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testSite)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var decoded StagingEvaluation
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ConfirmationMode != ConfirmationModeManual || len(decoded.Recommendations) != 2 {
		t.Fatalf("expected CHILLER_STAGE_DOWN + IDLE_AUXILIARY_STOP, got %+v", decoded)
	}
	if status := fixture.call(t, http.MethodGet, stagingEndpoint, "", testGatewaySPIFFE, testGatewaySPIFFE, ReadAction, testTenant, testOtherSite).Code; status != http.StatusForbidden {
		t.Fatalf("cross-site staging read status=%d, want 403", status)
	}
	if status := fixture.call(t, http.MethodGet, stagingEndpoint, "", testGatewaySPIFFE, testGatewaySPIFFE, WriteAction, testTenant, testSite).Code; status != http.StatusForbidden {
		t.Fatalf("write action staging read status=%d, want 403", status)
	}

	// Verify COPModelSolver wires Staging cleanly into ShadowEvaluation without requiring changes to published runs.
	shadowEval := (COPModelSolver{History: history, Staging: stagingSnapshotStub{snapshot: snapshot}}).Evaluate(t.Context(), copRequest(), fixture.now)
	if shadowEval.Staging == nil || len(shadowEval.Staging.Recommendations) != 2 {
		t.Fatalf("expected ShadowEvaluation.Staging to include staging recommendations, got %+v", shadowEval.Staging)
	}
}

func TestStagingSuppressedDuringSiteControlExit(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	buckets := syntheticStagingChillerHistory(at, map[string]float64{
		"01a14000-4000-7000-8000-000000000005": 1.00,
		"01a14000-4000-7000-8000-000000000006": 1.00,
		"01a14000-4000-7000-8000-000000000003": 0.85,
		"01a14000-4000-7000-8000-000000000004": 0.98,
	})
	to := at.Truncate(copBucketMinutes * time.Minute)
	fit := FitPlant(buckets, to.AddDate(0, 0, -copHistoryDays), to)

	for _, tc := range []struct {
		mode   string
		reason string
	}{
		{mode: SiteControlModeManualOverride, reason: "现场手操接管"},
		{mode: SiteControlModeSafeShutdownExit, reason: "安全停机退出"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			snapshot := StagingSnapshot{
				TenantID:          "01990000-3000-7000-8000-000000000001",
				SiteID:            "01a14000-5000-7000-8000-000000000001",
				EvaluatedAt:       at,
				SiteControlMode:   tc.mode,
				SiteControlReason: tc.reason,
				PlantCoolingLoad: &TelemetryValue{
					DeviceID: "01a14000-4000-7000-8000-000000000009", SourceKey: "btu-calc", Value: 520, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-20 * time.Second),
				},
				Chillers: testSongyaxuanUnits(at, ptr(55.0), ptr(0.2), ptr(74.0), ptr(56.0)),
				Auxiliaries: []AuxiliaryUnitInput{
					{
						AssetID: "01a14000-3000-7000-8000-00000000000a", AssetCode: "chwp-2", AssetName: "2 号冷冻泵",
						AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
						RatedPowerKW: 18.5, ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000002"},
					},
				},
				ChillerFits: fit.Chillers,
			}

			eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
			if err != nil {
				t.Fatalf("EvaluateStaging error: %v", err)
			}
			if eval.Recommendations == nil || len(eval.Recommendations) != 0 {
				t.Fatalf("expected non-nil empty recommendations under %s, got %+v", tc.mode, eval.Recommendations)
			}
			if eval.SiteControlMode != tc.mode {
				t.Fatalf("SiteControlMode = %q, want %q", eval.SiteControlMode, tc.mode)
			}
			var chillerSuppressed, auxSuppressed bool
			for _, d := range eval.Diagnostics {
				if d.Status == "SUPPRESSED" && d.Reason == "SITE_CONTROL_EXIT_ACTIVE" && strings.Contains(d.Detail, tc.reason) {
					if d.Scope == RecommendationKindChillerStageDown {
						chillerSuppressed = true
					}
					if d.Scope == RecommendationKindIdleAuxiliaryStop {
						auxSuppressed = true
					}
				}
			}
			if !chillerSuppressed || !auxSuppressed {
				t.Fatalf("expected both scopes suppressed with SITE_CONTROL_EXIT_ACTIVE, diagnostics=%+v", eval.Diagnostics)
			}
		})
	}
}

func TestStagingRespectsAssetControlLocks(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	buckets := syntheticStagingChillerHistory(at, map[string]float64{
		"01a14000-4000-7000-8000-000000000005": 1.00,
		"01a14000-4000-7000-8000-000000000006": 1.00,
		"01a14000-4000-7000-8000-000000000003": 0.85,
		"01a14000-4000-7000-8000-000000000004": 0.98,
	})
	to := at.Truncate(copBucketMinutes * time.Minute)
	fit := FitPlant(buckets, to.AddDate(0, 0, -copHistoryDays), to)

	// Lock only chiller-c; evaluator must never suggest stopping chiller-c.
	partialLockSnapshot := StagingSnapshot{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01a14000-5000-7000-8000-000000000001",
		EvaluatedAt: at,
		AssetControlLocks: map[string]AssetControlLockInfo{
			"01a14000-3000-7000-8000-000000000003": {LockMode: "MAINTENANCE_LOCK", Reason: "检修保养"},
		},
		PlantCoolingLoad: &TelemetryValue{
			DeviceID: "01a14000-4000-7000-8000-000000000009", SourceKey: "btu-calc", Value: 420, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-20 * time.Second),
		},
		Chillers:    testSongyaxuanUnits(at, ptr(52.0), ptr(0.2), ptr(74.0), ptr(54.0)),
		ChillerFits: fit.Chillers,
	}
	evalPartial, err := EvaluateStaging(partialLockSnapshot, DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging partial lock error: %v", err)
	}
	if len(evalPartial.Recommendations) != 1 || evalPartial.Recommendations[0].TargetAssetCode == "chiller-c" {
		t.Fatalf("expected unlocked candidate chiller recommendation instead of locked chiller-c, got recs=%+v diags=%+v", evalPartial.Recommendations, evalPartial.Diagnostics)
	}

	// Lock all running chillers and running idle auxiliary pump, while explicit UNLOCKED does not suppress.
	fullLockSnapshot := partialLockSnapshot
	fullLockSnapshot.AssetControlLocks = map[string]AssetControlLockInfo{
		"01a14000-3000-7000-8000-000000000001": {LockMode: "MANUAL_LOCK", Reason: "值班锁定"},
		"01a14000-3000-7000-8000-000000000003": {LockMode: "MAINTENANCE_LOCK", Reason: "检修清理"},
		"01a14000-3000-7000-8000-000000000004": {LockMode: "MANUAL_LOCK"},
		"01a14000-3000-7000-8000-00000000000a": {LockMode: "MAINTENANCE_LOCK", Reason: "泵阀检修"},
		"01a14000-3000-7000-8000-00000000000b": {LockMode: AssetControlLockUnlocked},
	}
	fullLockSnapshot.Auxiliaries = []AuxiliaryUnitInput{
		{
			AssetID: "01a14000-3000-7000-8000-00000000000a", AssetCode: "chwp-2", AssetName: "2 号冷冻泵",
			AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
			RatedPowerKW: 18.5, ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000002"},
		},
		{
			AssetID: "01a14000-3000-7000-8000-00000000000b", AssetCode: "chwp-3", AssetName: "3 号冷冻泵",
			AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
			RatedPowerKW: 18.5, ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000002"},
		},
	}
	evalLocked, err := EvaluateStaging(fullLockSnapshot, DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging full lock error: %v", err)
	}
	if len(evalLocked.Recommendations) != 1 || evalLocked.Recommendations[0].TargetAssetCode != "chwp-3" {
		t.Fatalf("expected only unlocked chwp-3 recommendation, got recs=%+v diags=%+v", evalLocked.Recommendations, evalLocked.Diagnostics)
	}
	var sawChillerLock, sawAuxLock bool
	for _, d := range evalLocked.Diagnostics {
		if d.Status == "SUPPRESSED" && d.Reason == "ASSET_CONTROL_LOCKED" {
			if d.Scope == RecommendationKindChillerStageDown {
				sawChillerLock = true
			}
			if d.Scope == RecommendationKindIdleAuxiliaryStop && d.AssetCode == "chwp-2" {
				sawAuxLock = true
			}
		}
	}
	if !sawChillerLock || !sawAuxLock {
		t.Fatalf("expected ASSET_CONTROL_LOCKED diagnostics for chiller and chwp-2, got %+v", evalLocked.Diagnostics)
	}
}

func TestStagingPopulatesSuggestedCommandPointID(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	buckets := syntheticStagingChillerHistory(at, map[string]float64{
		"01a14000-4000-7000-8000-000000000005": 1.00,
		"01a14000-4000-7000-8000-000000000006": 1.00,
		"01a14000-4000-7000-8000-000000000003": 0.85,
	})
	to := at.Truncate(copBucketMinutes * time.Minute)
	fit := FitPlant(buckets, to.AddDate(0, 0, -copHistoryDays), to)

	snapshot := StagingSnapshot{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01a14000-5000-7000-8000-000000000001",
		EvaluatedAt: at,
		CommandPointIDs: map[string]string{
			canonicalCommandPointKey("01a14000-3000-7000-8000-000000000003", SuggestedCapabilityStopUnitSequence): "pt-chiller-c-stop-seq",
			canonicalCommandPointKey("01a14000-3000-7000-8000-00000000000a", SuggestedCapabilityStop):             "pt-chwp-2-stop",
		},
		PlantCoolingLoad: &TelemetryValue{
			DeviceID: "01a14000-4000-7000-8000-000000000009", SourceKey: "btu-calc", Value: 520, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-20 * time.Second),
		},
		Chillers: testSongyaxuanUnits(at, ptr(55.0), ptr(55.0), ptr(74.0), nil),
		Auxiliaries: []AuxiliaryUnitInput{
			{
				AssetID: "01a14000-3000-7000-8000-00000000000a", AssetCode: "chwp-2", AssetName: "2 号冷冻泵",
				AssetType: AssetTypeCHWP, RunState: RunStateRunning, RunStateQuality: "GOOD", RunStateAt: at.Add(-30 * time.Second),
				RatedPowerKW: 18.5, ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000004"},
			},
		},
		ChillerFits: fit.Chillers,
	}

	eval, err := EvaluateStaging(snapshot, DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging error: %v", err)
	}
	if len(eval.Recommendations) != 2 {
		t.Fatalf("expected 2 recommendations, got %+v", eval.Recommendations)
	}
	seenChillerPoint := false
	seenAuxPoint := false
	for _, rec := range eval.Recommendations {
		if rec.Kind == RecommendationKindChillerStageDown && rec.SuggestedCommandPointID == "pt-chiller-c-stop-seq" {
			seenChillerPoint = true
		}
		if rec.Kind == RecommendationKindIdleAuxiliaryStop && rec.SuggestedCommandPointID == "pt-chwp-2-stop" {
			seenAuxPoint = true
		}
	}
	if !seenChillerPoint || !seenAuxPoint {
		t.Fatalf("missing SuggestedCommandPointID in recommendations: %+v", eval.Recommendations)
	}
}

func TestBuildStagingSnapshotUsesBoundDeviceIDs(t *testing.T) {
	at := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	runState := RunStateRunning
	topology := &StagingTopology{
		CoolingLoadDeviceID: "dev-btu",
		SiteControlMode:     SiteControlModeRemoteAuto,
		Chillers: []ChillerMeterBinding{
			{
				AssetID:         "asset-chiller-c",
				AssetCode:       "chiller-c",
				AssetName:       "C 机组",
				MeterDeviceID:   "dev-meter-1",
				MeterSourceKey:  "ammeter-unit1",
				BoundDeviceIDs:  []string{"dev-ctrl-chiller-c", "et1010-chiller-c"},
				RatedCapacityKW: 572,
				RatedPowerKW:    127.5,
			},
		},
		Auxiliaries: []AuxiliaryUnitBinding{
			{
				AssetID:          "asset-chwp-2",
				AssetCode:        "chwp-2",
				AssetName:        "2 号冷冻泵",
				AssetType:        AssetTypeCHWP,
				BoundDeviceIDs:   []string{"dev-ctrl-chwp-2", "et1010-chwp-2"},
				RatedPowerKW:     18.5,
				ServesChillerIDs: []string{"asset-chiller-c"},
			},
		},
	}

	latest := []stagingTelemetryRow{
		{DeviceID: "dev-btu", SourceID: "btu-calc", TelemetryKey: "instant_cooling_capacity", ValueNumber: ptr(510.0), Unit: "kW", Quality: "GOOD", SampledAt: at},
		{DeviceID: "dev-meter-1", SourceID: "ammeter-unit1", TelemetryKey: "power_total", ValueNumber: ptr(81.5), Unit: "kW", Quality: "GOOD", SampledAt: at},
		{DeviceID: "dev-ctrl-chiller-c", SourceID: "et1010-chiller-c", TelemetryKey: "chiller.run_state", ValueString: &runState, Quality: "GOOD", SampledAt: at},
		{DeviceID: "dev-ctrl-chwp-2", SourceID: "et1010-chwp-2", TelemetryKey: "chwp.run_state", ValueString: &runState, Quality: "GOOD", SampledAt: at},
	}

	snapshot := buildStagingSnapshotFromObservations("tenant-1", "site-1", at, topology, latest, nil)
	if snapshot.SiteControlMode != SiteControlModeRemoteAuto {
		t.Fatalf("SiteControlMode = %q, want REMOTE_AUTO", snapshot.SiteControlMode)
	}
	if len(snapshot.Chillers) != 1 || snapshot.Chillers[0].Power == nil || snapshot.Chillers[0].Power.Value != 81.5 || snapshot.Chillers[0].RunState != RunStateRunning {
		t.Fatalf("chiller telemetry across bound devices failed: %+v", snapshot.Chillers)
	}
	if len(snapshot.Auxiliaries) != 1 || snapshot.Auxiliaries[0].RunState != RunStateRunning {
		t.Fatalf("aux telemetry across bound devices failed: %+v", snapshot.Auxiliaries)
	}
}

func TestBuildStagingTopologyFromRecordsExtractsLocksCommandsAndEdges(t *testing.T) {
	locks := map[string]AssetControlLockInfo{
		"ASSET-CHILLER-C": {LockMode: "MAINTENANCE_LOCK", Reason: "维保锁定"},
		"asset-chiller-a": {LockMode: AssetControlLockUnlocked},
	}
	assets := []stagingAssetRow{
		{ID: "asset-chiller-a", Code: "chiller-a", DisplayName: "A 机组", AssetType: AssetTypeChiller, RatedPowerKW: 127.5, RatedCapacityKW: 572},
		{ID: "asset-chiller-c", Code: "chiller-c", DisplayName: "C 机组", AssetType: AssetTypeChiller, RatedPowerKW: 127.5, RatedCapacityKW: 572},
		{ID: "asset-chwp-2", Code: "chwp-2", DisplayName: "2 号冷冻泵", AssetType: AssetTypeCHWP, RatedPowerKW: 18.5},
	}
	devices := []stagingDeviceRow{
		{ID: "dev-btu", Code: "btu-calc", DeviceType: "BTU_METER", SourceKey: "btu-calc"},
		{ID: "dev-meter-1", Code: "ammeter-unit1", DeviceType: "POWER_METER", SourceKey: "ammeter-unit1"},
		{ID: "dev-ctrl-c", Code: "ctrl-chiller-c", DeviceType: "GATEWAY_IO", SourceKey: "et1010-c"},
		{ID: "dev-ctrl-p2", Code: "ctrl-chwp-2", DeviceType: "GATEWAY_IO", SourceKey: "et1010-p2"},
	}
	bindings := []stagingDeviceBindingRow{
		{AssetID: "asset-chiller-c", DeviceID: "dev-meter-1", BindingRole: "METER"},
		{AssetID: "asset-chiller-c", DeviceID: "dev-ctrl-c", BindingRole: "CONTROLLER"},
		{AssetID: "asset-chwp-2", DeviceID: "dev-ctrl-p2", BindingRole: "CONTROLLER"},
	}
	commandPoints := []stagingCommandPointRow{
		{
			PointID: "pt-chiller-c-stop-seq", ReportingDeviceID: "dev-ctrl-c", PointCode: "stop_seq", BoundAssetID: "asset-chiller-c",
			SourceMetadata: []byte(`{"capability":"STOP_UNIT_SEQUENCE","interlockChecks":[{"deviceId":"dev-ctrl-p2","key":"chwp.run_state"}]}`),
		},
		{
			PointID: "pt-chwp-2-stop", ReportingDeviceID: "dev-ctrl-p2", PointCode: "stop", BoundAssetID: "asset-chwp-2",
			SourceMetadata: []byte(`{"capability":"STOP","interlockChecks":[{"deviceId":"dev-ctrl-c","key":"chiller.run_state"}]}`),
		},
	}

	topology := buildStagingTopologyFromRecords(SiteControlModeManualOverride, "值班手操", locks, assets, devices, bindings, commandPoints)
	if topology.SiteControlMode != SiteControlModeManualOverride || topology.SiteControlReason != "值班手操" {
		t.Fatalf("unexpected site control mode fields: %+v", topology)
	}
	if topology.CoolingLoadDeviceID != "dev-btu" {
		t.Fatalf("CoolingLoadDeviceID = %q, want dev-btu", topology.CoolingLoadDeviceID)
	}
	if got := topology.CommandPointIDs[canonicalCommandPointKey("asset-chiller-c", SuggestedCapabilityStopUnitSequence)]; got != "pt-chiller-c-stop-seq" {
		t.Fatalf("chiller stop seq point = %q", got)
	}
	if got := topology.CommandPointIDs[canonicalCommandPointKey("asset-chwp-2", SuggestedCapabilityStop)]; got != "pt-chwp-2-stop" {
		t.Fatalf("aux stop point = %q", got)
	}
	if len(topology.Edges) != 1 || topology.Edges[0] != (HydraulicEdge{FromAssetID: "asset-chwp-2", ToAssetID: "asset-chiller-c", Role: HydraulicEdgeServes}) {
		t.Fatalf("expected deduplicated SERVES edge, got %+v", topology.Edges)
	}
	if len(topology.Auxiliaries) != 1 || len(topology.Auxiliaries[0].ServesChillerIDs) != 1 || topology.Auxiliaries[0].ServesChillerIDs[0] != "asset-chiller-c" {
		t.Fatalf("expected aux ServesChillerIDs=[asset-chiller-c], got %+v", topology.Auxiliaries)
	}
	if lock, ok := lookupAssetControlLock(topology.AssetControlLocks, "asset-chiller-c", "chiller-c"); !ok || lock.LockMode != "MAINTENANCE_LOCK" {
		t.Fatalf("expected MAINTENANCE_LOCK on chiller-c, got ok=%v lock=%+v", ok, lock)
	}
	if _, ok := lookupAssetControlLock(topology.AssetControlLocks, "asset-chiller-a", "chiller-a"); ok {
		t.Fatalf("UNLOCKED lock entry should not suppress chiller-a")
	}
}
