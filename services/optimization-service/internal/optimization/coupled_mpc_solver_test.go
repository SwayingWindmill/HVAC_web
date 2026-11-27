package optimization

import (
	"math"
	"testing"
	"time"
)

func TestEstimateWetBulbCAndAbsentRHFallback(t *testing.T) {
	// Stull reference: 30C dry-bulb, 60% RH -> ~23.7C wet-bulb.
	wb := EstimateWetBulbC(30.0, 60.0)
	if wb < 23.0 || wb > 24.5 {
		t.Fatalf("EstimateWetBulbC(30, 60) = %.2f, want ~23.7C", wb)
	}
	// Missing/non-positive/NaN RH must gracefully fall back and never return NaN or exceed dryBulb.
	for _, badRH := range []float64{0, -10, math.NaN(), math.Inf(1)} {
		got := EstimateWetBulbC(32.0, badRH)
		if math.IsNaN(got) || math.IsInf(got, 0) || got > 32.0 || got < 15.0 {
			t.Fatalf("EstimateWetBulbC(32, %v) = %.2f out of valid fallback range", badRH, got)
		}
	}
}

func TestMonotonicChillerModelPhysicalPartials(t *testing.T) {
	// Even with invalid/negative input slopes, ProjectMonotonic must enforce monotonicity.
	raw := MonotonicChillerModel{
		BaseCOP:                5.8,
		TchwSlopeCOPPerC:       -0.4, // adversarial negative sign
		TcwSlopeCOPPerC:        -0.2, // adversarial negative sign
		CondenserQuadLossPerC2: -0.01,
		PLRConstE0:             -0.2,
		PLRLinearE1:            0.05,
		PLRQuadE2:              -0.3,
		COPScale:               0.85,
	}.ProjectMonotonic()

	// 1. dPower/dTchw <= 0 and COP increases with Tchw.
	for _, cw := range []float64{24.0, 30.0, 35.0} {
		for _, load := range []float64{180.0, 380.0, 560.0} {
			pPrev, copPrev := raw.PowerKW(load, 600.0, 5.0, cw, cw+4.5)
			for tChw := 5.5; tChw <= 12.0; tChw += 0.5 {
				pCur, copCur := raw.PowerKW(load, 600.0, tChw, cw, cw+4.5)
				if pCur > pPrev+1e-9 || copCur < copPrev-1e-9 {
					t.Fatalf("Tchw monotonicity violated at load=%.0f cw=%.0f tChw=%.1f: P %.4f->%.4f COP %.4f->%.4f",
						load, cw, tChw, pPrev, pCur, copPrev, copCur)
				}
				pPrev, copPrev = pCur, copCur
			}
		}
	}

	// 2. dPower/dTcw >= 0 and COP decreases with condenser water temp.
	for _, tChw := range []float64{5.5, 7.0, 9.5} {
		pPrev, copPrev := raw.PowerKW(400.0, 600.0, tChw, 20.0, 24.0)
		for cw := 21.0; cw <= 38.0; cw += 1.0 {
			pCur, copCur := raw.PowerKW(400.0, 600.0, tChw, cw, cw+4.0)
			if pCur < pPrev-1e-9 || copCur > copPrev+1e-9 {
				t.Fatalf("Tcw monotonicity violated at tChw=%.1f cw=%.0f: P %.4f->%.4f COP %.4f->%.4f",
					tChw, cw, pPrev, pCur, copPrev, copCur)
			}
			pPrev, copPrev = pCur, copCur
		}
	}

	// 3. dPower/dLoad > 0 across full and overload range.
	pPrev, _ := raw.PowerKW(50.0, 600.0, 7.0, 28.0, 32.5)
	for load := 100.0; load <= 720.0; load += 50.0 {
		pCur, _ := raw.PowerKW(load, 600.0, 7.0, 28.0, 32.5)
		if pCur <= pPrev {
			t.Fatalf("Load monotonicity violated at load=%.0f: %.4f <= %.4f", load, pCur, pPrev)
		}
		pPrev = pCur
	}
}

func TestCoupledPlantMPCHigherPeakTOUShiftsPrecoolingEarlierAndRespectsComfort(t *testing.T) {
	solver := NewCoupledPlantMPCSolver()
	spec := DefaultPlantPhysicsSpec()
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))

	buildHorizon := func(peakPrice float64) []CoupledHorizonSlot {
		slots := make([]CoupledHorizonSlot, 24)
		for h := 0; h < 24; h++ {
			rate := 0.55
			switch {
			case h < 8:
				rate = 0.35
			case h >= 8 && h < 20:
				rate = peakPrice
			}
			load := 240.0
			if h >= 8 && h < 20 {
				load = 780.0
			}
			slots[h] = CoupledHorizonSlot{
				StartAt:         start.Add(time.Duration(h) * time.Hour),
				DurationHours:   1.0,
				CoolingLoadKW:   load,
				AmbientDryBulbC: 31.0,
				AmbientWetBulbC: 26.0,
				TariffRateCNY:   rate,
			}
		}
		return slots
	}

	flatSol, err := solver.SolveCoupledPlant(CoupledPlantMPCInput{
		CurrentSupplyTempC:      7.0,
		ObservedZoneTempC:       24.0,
		ZoneTempUncertaintyP90C: 0.2,
		SupplyTempMinC:          5.0,
		SupplyTempMaxC:          11.0,
		ZoneTempMinC:            21.0,
		ZoneTempMaxC:            25.0,
		MaxSupplyTempStepC:      1.5,
		PrecoolingEnabled:       true,
		Horizon:                 buildHorizon(0.35), // flat 0.35 day
		Plant:                   spec,
	})
	if err != nil {
		t.Fatalf("flat tariff SolveCoupledPlant failed: %v", err)
	}

	peakSol, err := solver.SolveCoupledPlant(CoupledPlantMPCInput{
		CurrentSupplyTempC:      7.0,
		ObservedZoneTempC:       24.0,
		ZoneTempUncertaintyP90C: 0.2,
		SupplyTempMinC:          5.0,
		SupplyTempMaxC:          11.0,
		ZoneTempMinC:            21.0,
		ZoneTempMaxC:            25.0,
		MaxSupplyTempStepC:      1.5,
		PrecoolingEnabled:       true,
		Horizon:                 buildHorizon(1.65), // high peak tariff arbitrage
		Plant:                   spec,
	})
	if err != nil {
		t.Fatalf("high peak tariff SolveCoupledPlant failed: %v", err)
	}

	valleyMean := func(sol CoupledPlantMPCSolution) float64 {
		sum := 0.0
		for h := 1; h < 8; h++ {
			sum += sol.Slots[h].SupplyTempC
		}
		return sum / 7.0
	}
	minValleyZone := func(sol CoupledPlantMPCSolution) float64 {
		minZ := 99.0
		for h := 0; h < 10; h++ {
			if sol.Slots[h].ProjectedZoneTempC < minZ {
				minZ = sol.Slots[h].ProjectedZoneTempC
			}
		}
		return minZ
	}

	if valleyMean(peakSol) >= valleyMean(flatSol)-0.2 {
		t.Fatalf("expected high peak TOU to shift valley CHW setpoint lower for pre-cooling: peakMean=%.2f flatMean=%.2f",
			valleyMean(peakSol), valleyMean(flatSol))
	}
	if minValleyZone(peakSol) >= minValleyZone(flatSol) {
		t.Fatalf("expected pre-cooling thermal charge under peak TOU: minZonePeak=%.2f minZoneFlat=%.2f",
			minValleyZone(peakSol), minValleyZone(flatSol))
	}

	// Verify strict comfort & freeze bounds at every hour of the trajectory.
	for i, sl := range peakSol.Slots {
		if sl.SupplyTempC < 5.0 || sl.SupplyTempC > 11.0 {
			t.Fatalf("slot %d supply temp %.2f outside [5.0, 11.0]", i, sl.SupplyTempC)
		}
		if sl.ProjectedZoneTempC+0.2 > 25.0 || sl.ProjectedZoneTempC-0.2 < 21.0 {
			t.Fatalf("slot %d projected zone temp %.2f violates comfort +/- P90 bounds", i, sl.ProjectedZoneTempC)
		}
	}
}

func TestCoupledPlantMPCRecommendContractAndResidualAdaptation(t *testing.T) {
	solver := NewCoupledPlantMPCSolver()
	req := validRequest()
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

	rec, err := solver.Recommend(t.Context(), req, now)
	if err != nil {
		t.Fatalf("Recommend failed: %v", err)
	}
	if err := rec.ValidateForApproval(); err != nil {
		t.Fatalf("ValidateForApproval failed: %v", err)
	}
	if rec.Candidate.SupplyTempC < 5.0 || rec.Candidate.SupplyTempC > req.Constraints.SupplyTempMaxC {
		t.Fatalf("candidate supply temp %.2f out of bounds", rec.Candidate.SupplyTempC)
	}
	if len(rec.AuxiliaryTargets) != 3 {
		t.Fatalf("expected 3 coupled auxiliary targets, got %d", len(rec.AuxiliaryTargets))
	}

	// Simulate observed -15% chiller COP mismatch; online grey-box residual estimator should adapt COPScale < 0.92.
	for i := 0; i < 12; i++ {
		st := solver.UpdateResidualFromTelemetry(ObservedPlantTelemetry{
			CoolingCapacityKW:     520,
			ChillerPowerKW:        116, // observed COP ~4.48 vs ~5.45 nominal
			ChilledWaterSupplyC:   7.0,
			CoolingWaterEnteringC: 29.0,
			CoolingWaterLeavingC:  33.0,
			RunningChillers:       1,
			RatedRunningCapKW:     600,
			Steady:                true,
		})
		if i == 11 && (st.COPScale >= 0.93 || st.COPScale < 0.70) {
			t.Fatalf("expected online grey-box COPScale adaptation around ~0.85, got %.3f", st.COPScale)
		}
	}
}
