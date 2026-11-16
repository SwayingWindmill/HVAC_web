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
	// Fit Songyaxuan meters: unit4=chiller-a (UID 4), unit3=chiller-b (UID 3), unit1=chiller-c (degraded 0.85), unit2=chiller-d.
	buckets := syntheticStagingChillerHistory(at, map[string]float64{
		"01a14000-4000-7000-8000-000000000006": 1.00, // ammeter-unit4 -> chiller-a
		"01a14000-4000-7000-8000-000000000005": 1.00, // ammeter-unit3 -> chiller-b
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
		DeviceID:          "ammeter-unit4", // chiller-a meter (UID 4)
		Status:            ChillerFitted,
		CapacityReference: &CapacityReference{KW: 572, Source: "OBSERVED_P99_LOAD"},
		Coefficients:      map[string]float64{"intercept": 2.9, "loadRatio": 3.8, "loadRatioSquared": -2.2, "chilledWaterSupplyTempC": 0.08},
	}
	fitB := ChillerFit{
		DeviceID:          "ammeter-unit3", // chiller-b meter (UID 3)
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
				MeterSourceKey:  "ammeter-unit4",
				RunState:        RunStateRunning,
				RunStateQuality: "GOOD",
				RunStateAt:      at.Add(-time.Minute),
				Power:           &TelemetryValue{Value: 58, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
				ChwLeavingC:     &TelemetryValue{Value: 7.0, Unit: "Cel", Quality: "GOOD", SampledAt: at.Add(-time.Minute)},
			},
			{
				MeterSourceKey:  "ammeter-unit3",
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
		t.Fatalf("expected ammeter-unit3 to resolve to chiller-b (01a14000-3000-7000-8000-000000000002), got %+v", rec)
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

func testScheduleStagingSnapshot(at time.Time, schedule *OperatingScheduleContext) StagingSnapshot {
	history := syntheticStagingChillerHistory(at, map[string]float64{
		"ammeter-unit4": 1.0,
		"ammeter-unit3": 0.95,
	})
	fit := FitPlant(history, at.UTC().AddDate(0, 0, -copHistoryDays), at.UTC())

	return StagingSnapshot{
		TenantID:          "01a14000-1000-7000-8000-000000000001",
		SiteID:            "01a14000-2000-7000-8000-000000000001",
		EvaluatedAt:       at,
		SiteControlMode:   SiteControlModeRemoteAuto,
		OperatingSchedule: schedule,
		CommandPointIDs: map[string]string{
			canonicalCommandPointKey("01a14000-3000-7000-8000-000000000001", SuggestedCapabilityStopUnitSequence): "pt-chiller-a-stop-seq",
			canonicalCommandPointKey("01a14000-3000-7000-8000-000000000002", SuggestedCapabilityStop):             "pt-chiller-b-stop",
			canonicalCommandPointKey("asset-chwp-2", SuggestedCapabilityStop):                                     "pt-chwp-2-stop",
		},
		PlantCoolingLoad: &TelemetryValue{Value: 290, Quality: "GOOD", SampledAt: at},
		HeaderSupplyC:    &TelemetryValue{Value: 7.0, Quality: "GOOD", SampledAt: at},
		Chillers: []ChillerUnitInput{
			{
				AssetID:         "01a14000-3000-7000-8000-000000000001",
				AssetCode:       "chiller-a",
				AssetName:       "A 机组",
				MeterDeviceID:   "01a14000-4000-7000-8000-000000000006",
				MeterSourceKey:  "ammeter-unit4",
				RatedCapacityKW: 572,
				RatedPowerKW:    127.5,
				RunState:        RunStateRunning,
				RunStateQuality: "GOOD",
				RunStateAt:      at,
				Power:           &TelemetryValue{Value: 62.4, Quality: "GOOD", SampledAt: at},
				ChwLeavingC:     &TelemetryValue{Value: 7.0, Quality: "GOOD", SampledAt: at},
				CwLeavingC:      &TelemetryValue{Value: 30.0, Quality: "GOOD", SampledAt: at},
			},
			{
				AssetID:         "01a14000-3000-7000-8000-000000000002",
				AssetCode:       "chiller-b",
				AssetName:       "B 机组",
				MeterDeviceID:   "01a14000-4000-7000-8000-000000000005",
				MeterSourceKey:  "ammeter-unit3",
				RatedCapacityKW: 572,
				RatedPowerKW:    127.5,
				RunState:        RunStateRunning,
				RunStateQuality: "GOOD",
				RunStateAt:      at,
				Power:           &TelemetryValue{Value: 64.0, Quality: "GOOD", SampledAt: at},
				ChwLeavingC:     &TelemetryValue{Value: 7.1, Quality: "GOOD", SampledAt: at},
				CwLeavingC:      &TelemetryValue{Value: 30.2, Quality: "GOOD", SampledAt: at},
			},
			{
				AssetID:         "01a14000-3000-7000-8000-000000000004",
				AssetCode:       "chiller-d",
				AssetName:       "D 机组",
				MeterDeviceID:   "01a14000-4000-7000-8000-000000000004",
				MeterSourceKey:  "ammeter-unit2",
				RatedCapacityKW: 572,
				RatedPowerKW:    127.5,
				RunState:        RunStateStopped,
				RunStateQuality: "GOOD",
				RunStateAt:      at,
				RunStateHistory: []RunStateSample{
					{State: RunStateStopped, Quality: "GOOD", SampledAt: at.Add(-6 * time.Minute)},
					{State: RunStateStopped, Quality: "GOOD", SampledAt: at},
				},
			},
		},
		Auxiliaries: []AuxiliaryUnitInput{
			{
				AssetID:          "asset-chwp-2",
				AssetCode:        "chwp-2",
				AssetName:        "2 号冷冻泵",
				AssetType:        AssetTypeCHWP,
				RatedPowerKW:     18.5,
				RunState:         RunStateRunning,
				RunStateQuality:  "GOOD",
				RunStateAt:       at,
				Power:            &TelemetryValue{Value: 16.2, Quality: "GOOD", SampledAt: at},
				ServesChillerIDs: []string{"01a14000-3000-7000-8000-000000000004"},
			},
		},
		ChillerFits: fit.Chillers,
	}
}

func shanghaiTime(year int, month time.Month, day, hour, minute int) time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

func hasDiagnostic(diags []StagingDiagnostic, scope, status, reason string) bool {
	for _, d := range diags {
		if (scope == "" || d.Scope == scope) &&
			(status == "" || d.Status == status) &&
			(reason == "" || d.Reason == reason) {
			return true
		}
	}
	return false
}

func TestStagingOperatingScheduleInWindowPreCoolAndCoastDown(t *testing.T) {
	// 2026-11-16 is a Monday.
	baseSchedule := &OperatingScheduleContext{
		SiteTimezone:        "Asia/Shanghai",
		Configured:          true,
		PreCoolLeadMinutes:  30,
		CoastDownLagMinutes: 20,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 8*60 + 30, EndMinute: 18 * 60}, // Mon 08:30-18:00
		},
	}

	cases := map[string]time.Time{
		"inside regular cooling slot":  shanghaiTime(2026, 11, 16, 10, 0),
		"within pre-cool lead buffer":  shanghaiTime(2026, 11, 16, 8, 10),  // 08:00-08:30
		"within coast-down lag buffer": shanghaiTime(2026, 11, 16, 18, 10), // 18:00-18:20
	}

	for name, at := range cases {
		t.Run(name, func(t *testing.T) {
			eval, err := EvaluateStaging(testScheduleStagingSnapshot(at, baseSchedule), DefaultStagingPolicy())
			if err != nil {
				t.Fatalf("EvaluateStaging error: %v", err)
			}
			var stageDownCount, idleAuxCount int
			for _, rec := range eval.Recommendations {
				switch rec.Kind {
				case RecommendationKindOffHoursStop:
					t.Fatalf("unexpected OFF_HOURS_STOP inside cooling window: %+v", rec)
				case RecommendationKindChillerStageDown:
					stageDownCount++
				case RecommendationKindIdleAuxiliaryStop:
					idleAuxCount++
				}
			}
			if stageDownCount != 1 || idleAuxCount != 1 {
				t.Fatalf("expected 1 CHILLER_STAGE_DOWN + 1 IDLE_AUXILIARY_STOP inside window, got stageDown=%d aux=%d diags=%+v", stageDownCount, idleAuxCount, eval.Diagnostics)
			}
			if !hasDiagnostic(eval.Diagnostics, RecommendationKindOffHoursStop, "NO_OP", "IN_COOLING_WINDOW") {
				t.Fatalf("expected IN_COOLING_WINDOW diagnostic, got %+v", eval.Diagnostics)
			}
		})
	}
}

func TestStagingOperatingScheduleOffHoursEmitsStopDraftsAndChecksAux(t *testing.T) {
	// Monday 20:00 Shanghai is past 18:00 + 20m coast-down lag.
	at := shanghaiTime(2026, 11, 16, 20, 0)
	schedule := &OperatingScheduleContext{
		SiteTimezone:        "Asia/Shanghai",
		Configured:          true,
		PreCoolLeadMinutes:  30,
		CoastDownLagMinutes: 20,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 8*60 + 30, EndMinute: 18 * 60},
		},
	}

	snap := testScheduleStagingSnapshot(at, schedule)
	// Test power saving basis fallback on running chiller-b (no fresh meter -> NAMEPLATE) and add chiller-c (DEFAULT).
	snap.Chillers[1].Power = nil
	snap.Chillers = append(snap.Chillers, ChillerUnitInput{
		AssetID:         "asset-chiller-custom",
		AssetCode:       "chiller-custom",
		AssetName:       "临时备用机组",
		RatedCapacityKW: 500,
		RatedPowerKW:    0,
		RunState:        RunStateStarting,
		RunStateQuality: "GOOD",
		RunStateAt:      at,
	})

	eval, err := EvaluateStaging(snap, DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging error: %v", err)
	}

	offByCode := map[string]StagingRecommendationDraft{}
	var auxCount int
	for _, rec := range eval.Recommendations {
		switch rec.Kind {
		case RecommendationKindChillerStageDown:
			t.Fatalf("should not emit duplicate CHILLER_STAGE_DOWN during off-hours: %+v", rec)
		case RecommendationKindOffHoursStop:
			offByCode[rec.TargetAssetCode] = rec
		case RecommendationKindIdleAuxiliaryStop:
			auxCount++
		}
	}

	if len(offByCode) != 3 {
		t.Fatalf("expected 3 OFF_HOURS_STOP drafts (chiller-a, chiller-b, chiller-custom), got %+v", offByCode)
	}
	if auxCount != 1 {
		t.Fatalf("expected IDLE_AUXILIARY_STOP still evaluated during off-hours, got auxCount=%d", auxCount)
	}
	if !hasDiagnostic(eval.Diagnostics, RecommendationKindChillerStageDown, "NO_OP", "OFF_HOURS_WINDOW_ACTIVE") {
		t.Fatalf("expected OFF_HOURS_WINDOW_ACTIVE diagnostic for CHILLER_STAGE_DOWN, got %+v", eval.Diagnostics)
	}

	a := offByCode["chiller-a"]
	if a.ConfirmationMode != ConfirmationModeManual || !a.RequiresSupervisorReview || !a.RequiresTOTP {
		t.Fatalf("off-hours draft must require manual confirmation + TOTP: %+v", a)
	}
	if a.SuggestedCapability != SuggestedCapabilityStopUnitSequence || a.SuggestedCommandPointID != "pt-chiller-a-stop-seq" {
		t.Fatalf("unexpected chiller-a capability/point: %+v", a)
	}
	if a.PowerSavingBasis != "METER" || a.PredictedPowerSavingKW != 62.4 || a.EstimatedHourlySavingKWh != 62.4 || a.EstimatedShiftSavingKWh != 499.2 {
		t.Fatalf("unexpected chiller-a meter saving metrics: %+v", a)
	}

	b := offByCode["chiller-b"]
	if b.SuggestedCapability != SuggestedCapabilityStop || b.SuggestedCommandPointID != "pt-chiller-b-stop" {
		t.Fatalf("expected fallback to STOP capability when STOP_UNIT_SEQUENCE absent: %+v", b)
	}
	if b.PowerSavingBasis != "NAMEPLATE" || b.PredictedPowerSavingKW != 127.5 {
		t.Fatalf("unexpected chiller-b nameplate saving metrics: %+v", b)
	}

	custom := offByCode["chiller-custom"]
	if custom.PowerSavingBasis != "DEFAULT" || custom.PredictedPowerSavingKW != DefaultChillerRunningPowerKW {
		t.Fatalf("unexpected default saving metrics: %+v", custom)
	}
}

func TestStagingOperatingScheduleHolidayShutdownMasksSpilloverAndRegularSlots(t *testing.T) {
	// Tuesday 2026-11-17 has HOLIDAY_SHUTDOWN; Monday 2026-11-16 had 22:00-24:00 + 30m coast-down lag.
	schedule := &OperatingScheduleContext{
		SiteTimezone:        "Asia/Shanghai",
		Configured:          true,
		PreCoolLeadMinutes:  15,
		CoastDownLagMinutes: 30,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 22 * 60, EndMinute: 24 * 60}, // Mon 22:00-24:00
			{DayOfWeek: 2, StartMinute: 8 * 60, EndMinute: 18 * 60},  // Tue 08:00-18:00
		},
		ApprovedExceptions: []ApprovedOperatingExceptionWindow{
			{
				ID:        "exc-holiday-1",
				Title:     "全厂公休日停机",
				Kind:      "HOLIDAY_SHUTDOWN",
				StartDate: "2026-11-17",
				EndDate:   "2026-11-17",
			},
		},
	}

	for name, at := range map[string]time.Time{
		"masks prior day coast-down spillover at 00:15": shanghaiTime(2026, 11, 17, 0, 15),
		"masks regular weekday cooling slot at 10:00":   shanghaiTime(2026, 11, 17, 10, 0),
	} {
		t.Run(name, func(t *testing.T) {
			eval, err := EvaluateStaging(testScheduleStagingSnapshot(at, schedule), DefaultStagingPolicy())
			if err != nil {
				t.Fatalf("EvaluateStaging error: %v", err)
			}
			var offCount int
			for _, rec := range eval.Recommendations {
				if rec.Kind == RecommendationKindChillerStageDown {
					t.Fatalf("unexpected CHILLER_STAGE_DOWN on HOLIDAY_SHUTDOWN: %+v", rec)
				}
				if rec.Kind == RecommendationKindOffHoursStop {
					offCount++
				}
			}
			if offCount != 2 {
				t.Fatalf("expected 2 OFF_HOURS_STOP drafts on HOLIDAY_SHUTDOWN, got %d (%+v)", offCount, eval.Recommendations)
			}
		})
	}
}

func TestStagingOperatingScheduleMaintenanceWindowSuppressesAll(t *testing.T) {
	at := shanghaiTime(2026, 11, 16, 21, 0) // even if off-hours, MAINTENANCE_WINDOW suppresses all
	schedule := &OperatingScheduleContext{
		SiteTimezone: "Asia/Shanghai",
		Configured:   true,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 8 * 60, EndMinute: 18 * 60},
		},
		ApprovedExceptions: []ApprovedOperatingExceptionWindow{
			{
				ID:        "exc-maint-1",
				Title:     "年度冷机大修",
				Kind:      "MAINTENANCE_WINDOW",
				StartDate: "2026-11-16",
				EndDate:   "2026-11-16",
				Reason:    "拆机清洗冷凝器",
			},
		},
	}

	eval, err := EvaluateStaging(testScheduleStagingSnapshot(at, schedule), DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging error: %v", err)
	}
	if len(eval.Recommendations) != 0 {
		t.Fatalf("expected 0 recommendations during MAINTENANCE_WINDOW, got %+v", eval.Recommendations)
	}
	if !hasDiagnostic(eval.Diagnostics, RecommendationKindChillerStageDown, "SUPPRESSED", "MAINTENANCE_WINDOW_ACTIVE") ||
		!hasDiagnostic(eval.Diagnostics, RecommendationKindOffHoursStop, "SUPPRESSED", "MAINTENANCE_WINDOW_ACTIVE") ||
		!hasDiagnostic(eval.Diagnostics, RecommendationKindIdleAuxiliaryStop, "SUPPRESSED", "MAINTENANCE_WINDOW_ACTIVE") {
		t.Fatalf("expected MAINTENANCE_WINDOW_ACTIVE suppression diagnostics across scopes, got %+v", eval.Diagnostics)
	}
}

func TestStagingOperatingScheduleOvertimeCoolingOverridesDayAndBuffers(t *testing.T) {
	// 2026-11-15 is Sunday (normally off-hours). Approved OVERTIME_COOLING runs 10:00-15:00 with lead=30, lag=15.
	// Saturday 2026-11-14 had 22:00-24:00, which must NOT bleed past Sunday 00:00 because Sunday is overridden.
	schedule := &OperatingScheduleContext{
		SiteTimezone:        "Asia/Shanghai",
		Configured:          true,
		PreCoolLeadMinutes:  30,
		CoastDownLagMinutes: 15,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 6, StartMinute: 22 * 60, EndMinute: 24 * 60}, // Sat 22:00-24:00
		},
		ApprovedExceptions: []ApprovedOperatingExceptionWindow{
			{
				ID:        "exc-ot-1",
				Title:     "周末活动保障加班供冷",
				Kind:      "OVERTIME_COOLING",
				StartDate: "2026-11-15",
				EndDate:   "2026-11-15",
				ReplacementSlots: []OperatingExceptionReplacementSlot{
					{StartMinute: 10 * 60, EndMinute: 15 * 60},
				},
			},
		},
	}

	inCases := map[string]time.Time{
		"within overtime slot":      shanghaiTime(2026, 11, 15, 11, 0),
		"within overtime pre-cool":  shanghaiTime(2026, 11, 15, 9, 40),
		"within overtime coast-lag": shanghaiTime(2026, 11, 15, 15, 10),
	}
	for name, at := range inCases {
		t.Run(name, func(t *testing.T) {
			eval, err := EvaluateStaging(testScheduleStagingSnapshot(at, schedule), DefaultStagingPolicy())
			if err != nil {
				t.Fatalf("EvaluateStaging error: %v", err)
			}
			for _, rec := range eval.Recommendations {
				if rec.Kind == RecommendationKindOffHoursStop {
					t.Fatalf("unexpected OFF_HOURS_STOP during OVERTIME_COOLING (%s): %+v", name, rec)
				}
			}
		})
	}

	offCases := map[string]time.Time{
		"prior-day spillover blocked at 00:05": shanghaiTime(2026, 11, 15, 0, 5),
		"before overtime pre-cool at 09:00":    shanghaiTime(2026, 11, 15, 9, 0),
		"after overtime coast-down at 16:00":   shanghaiTime(2026, 11, 15, 16, 0),
	}
	for name, at := range offCases {
		t.Run(name, func(t *testing.T) {
			eval, err := EvaluateStaging(testScheduleStagingSnapshot(at, schedule), DefaultStagingPolicy())
			if err != nil {
				t.Fatalf("EvaluateStaging error: %v", err)
			}
			var offCount int
			for _, rec := range eval.Recommendations {
				if rec.Kind == RecommendationKindOffHoursStop {
					offCount++
				}
			}
			if offCount != 2 {
				t.Fatalf("expected 2 OFF_HOURS_STOP outside OVERTIME_COOLING (%s), got %d", name, offCount)
			}
		})
	}
}

func TestStagingOperatingScheduleTimezoneMidnightRollover(t *testing.T) {
	// 2026-11-15 16:30:00Z == 2026-11-16 00:30:00+08:00 (Monday morning in Shanghai).
	atUTC := time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC)
	schedule := &OperatingScheduleContext{
		SiteTimezone: "Asia/Shanghai",
		Configured:   true,
		Slots: []OperatingScheduleSlotWindow{
			// Sunday 00:00-24:00 is cooling, Monday 00:00-08:00 is OFF.
			// In UTC it's still Sunday, in Shanghai it's Monday -> must evaluate Monday (off-hours).
			{DayOfWeek: 7, StartMinute: 0, EndMinute: 24 * 60},
			{DayOfWeek: 1, StartMinute: 8 * 60, EndMinute: 18 * 60},
		},
	}

	eval, err := EvaluateStaging(testScheduleStagingSnapshot(atUTC, schedule), DefaultStagingPolicy())
	if err != nil {
		t.Fatalf("EvaluateStaging error: %v", err)
	}
	var offCount int
	for _, rec := range eval.Recommendations {
		if rec.Kind == RecommendationKindOffHoursStop {
			offCount++
		}
	}
	if offCount != 2 {
		t.Fatalf("expected Monday 00:30 Asia/Shanghai to be off-hours despite Sunday UTC date, got %+v", eval.Recommendations)
	}
}

func TestStagingOperatingScheduleRespectsSiteExitAndAssetLocks(t *testing.T) {
	at := shanghaiTime(2026, 11, 16, 21, 0) // off-hours Monday
	schedule := &OperatingScheduleContext{
		SiteTimezone: "Asia/Shanghai",
		Configured:   true,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 8 * 60, EndMinute: 18 * 60},
		},
	}

	t.Run("site control exit suppresses off-hours recommendations", func(t *testing.T) {
		snap := testScheduleStagingSnapshot(at, schedule)
		snap.SiteControlMode = SiteControlModeManualOverride
		snap.SiteControlReason = "现场值守接管"

		eval, err := EvaluateStaging(snap, DefaultStagingPolicy())
		if err != nil {
			t.Fatalf("EvaluateStaging error: %v", err)
		}
		if len(eval.Recommendations) != 0 {
			t.Fatalf("expected 0 recommendations under MANUAL_OVERRIDE, got %+v", eval.Recommendations)
		}
		if !hasDiagnostic(eval.Diagnostics, RecommendationKindOffHoursStop, "SUPPRESSED", "SITE_CONTROL_EXIT_ACTIVE") {
			t.Fatalf("expected SITE_CONTROL_EXIT_ACTIVE diagnostic for OFF_HOURS_STOP, got %+v", eval.Diagnostics)
		}
	})

	t.Run("per-asset control lock suppresses only locked chiller during off-hours", func(t *testing.T) {
		snap := testScheduleStagingSnapshot(at, schedule)
		snap.AssetControlLocks = map[string]AssetControlLockInfo{
			"01a14000-3000-7000-8000-000000000001": {LockMode: "MAINTENANCE_LOCK", Reason: "A机单机锁定"},
		}

		eval, err := EvaluateStaging(snap, DefaultStagingPolicy())
		if err != nil {
			t.Fatalf("EvaluateStaging error: %v", err)
		}
		var offTargets []string
		for _, rec := range eval.Recommendations {
			if rec.Kind == RecommendationKindOffHoursStop {
				offTargets = append(offTargets, rec.TargetAssetCode)
			}
		}
		if len(offTargets) != 1 || offTargets[0] != "chiller-b" {
			t.Fatalf("expected only unlocked chiller-b in OFF_HOURS_STOP, got %+v", offTargets)
		}
		if !hasDiagnostic(eval.Diagnostics, RecommendationKindOffHoursStop, "SUPPRESSED", "ASSET_CONTROL_LOCKED") {
			t.Fatalf("expected ASSET_CONTROL_LOCKED diagnostic for locked chiller, got %+v", eval.Diagnostics)
		}
	})

	t.Run("applyStagingTopology propagates OperatingSchedule without overriding existing snapshot schedule", func(t *testing.T) {
		topology := &StagingTopology{OperatingSchedule: schedule}
		snap := StagingSnapshot{}
		applyStagingTopology(&snap, topology)
		if snap.OperatingSchedule == nil || !snap.OperatingSchedule.Configured || len(snap.OperatingSchedule.Slots) != 1 {
			t.Fatalf("expected OperatingSchedule copied by applyStagingTopology, got %+v", snap.OperatingSchedule)
		}
		snap.OperatingSchedule.Slots[0].StartMinute = 0
		if schedule.Slots[0].StartMinute == 0 {
			t.Fatal("expected OperatingSchedule clone to isolate slot slice mutations")
		}
	})
}

func TestResolveChillerRunStateAccountsForFeederAuxiliaryPower(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	policy := DefaultStagingPolicy()
	combinedUnit := ChillerUnitInput{
		AssetID:                "01a14000-3000-7000-8000-000000000003",
		AssetCode:              "chiller-c",
		RatedPowerKW:           127.5,
		FeederAuxiliaryPowerKW: 30,
	}

	// Power-only fallback: 32 kW is below 5 + 30 = 35 kW, so unit is STOPPED; 40 kW is RUNNING.
	combinedUnit.Power = &TelemetryValue{Value: 32, Unit: "kW", Quality: "GOOD", SampledAt: at.Add(-time.Minute)}
	if state, reason := resolveChillerRunState(combinedUnit, at, policy); state != RunStateStopped || reason != "" {
		t.Fatalf("expected 32 kW combined feeder to resolve STOPPED, got %s (%s)", state, reason)
	}
	combinedUnit.Power.Value = 40
	if state, reason := resolveChillerRunState(combinedUnit, at, policy); state != RunStateRunning || reason != "" {
		t.Fatalf("expected 40 kW combined feeder to resolve RUNNING, got %s (%s)", state, reason)
	}

	// Explicit STOPPED with 32 kW auxiliary load must not raise PAIRED_CHILLER_DRAWING_POWER.
	combinedUnit.RunState = RunStateStopped
	combinedUnit.RunStateQuality = "GOOD"
	combinedUnit.RunStateAt = at.Add(-time.Minute)
	combinedUnit.Power.Value = 32
	if state, reason := resolveChillerRunState(combinedUnit, at, policy); state != RunStateStopped || reason != "" {
		t.Fatalf("expected explicit STOPPED at 32 kW to remain STOPPED, got %s (%s)", state, reason)
	}
	combinedUnit.Power.Value = 40
	if state, reason := resolveChillerRunState(combinedUnit, at, policy); state != RunStateUnknown || reason != "PAIRED_CHILLER_DRAWING_POWER" {
		t.Fatalf("expected explicit STOPPED at 40 kW to flag PAIRED_CHILLER_DRAWING_POWER, got %s (%s)", state, reason)
	}

	// buildStagingTopologyFromRecords carries FeederAuxiliaryPowerKW onto ChillerMeterBinding.
	topology := buildStagingTopologyFromRecords(
		SiteControlModeRemoteAuto,
		"",
		nil,
		[]stagingAssetRow{{ID: "asset-c", Code: "chiller-c", DisplayName: "C 机组", AssetType: AssetTypeChiller, RatedPowerKW: 127.5, RatedCapacityKW: 572, FeederAuxiliaryPowerKW: 30}},
		[]stagingDeviceRow{{ID: "dev-c", Code: "meter-uid5", DeviceType: "HVAC_POWER_METER", SourceKey: "ammeter-unit1"}},
		[]stagingDeviceBindingRow{{AssetID: "asset-c", DeviceID: "dev-c", BindingRole: "METER"}},
		nil,
	)
	if len(topology.Chillers) != 1 || topology.Chillers[0].FeederAuxiliaryPowerKW != 30 {
		t.Fatalf("expected FeederAuxiliaryPowerKW=30 in topology, got %+v", topology.Chillers)
	}
}
