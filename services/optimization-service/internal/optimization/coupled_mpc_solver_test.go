package optimization

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/measurement"
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
	if rec.CommandIntentID != "" || string(rec.Approval) != "DRAFT" {
		t.Fatalf("draft recommendation must leave CommandIntentID empty and Approval=DRAFT, got approval=%q commandIntentId=%q", rec.Approval, rec.CommandIntentID)
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

func assertNearParity(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s parity mismatch: got %.9f, want %.9f (diff %.9f > 1e-6)", label, got, want, math.Abs(got-want))
	}
}

func TestCoupledMPCGoldenParity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "http", "testdata", "coupled-mpc-parity.v1.json"))
	if err != nil {
		t.Fatalf("read golden parity fixture: %v", err)
	}
	var fixture struct {
		WaterHeatCapacityKWPerM3HC float64 `json:"waterHeatCapacityKwPerM3hC"`
		ChillerPoints              []struct {
			Name       string  `json:"name"`
			LoadKW     float64 `json:"loadKw"`
			RatedCapKW float64 `json:"ratedCapKw"`
			TChwC      float64 `json:"tChwC"`
			CWEntering float64 `json:"cwEnteringC"`
			CWLeaving  float64 `json:"cwLeavingC"`
			Model      struct {
				BaseCOP                float64 `json:"baseCop"`
				TchwSlopeCOPPerC       float64 `json:"tchwSlopeCopPerC"`
				TcwSlopeCOPPerC        float64 `json:"tcwSlopeCopPerC"`
				CondenserQuadLossPerC2 float64 `json:"condenserQuadLossPerC2"`
				PLRConstE0             float64 `json:"plrConstE0"`
				COPScale               float64 `json:"copScale"`
			} `json:"model"`
			Expected struct {
				LiftCOP float64 `json:"liftCop"`
				PowerKW float64 `json:"powerKw"`
				COP     float64 `json:"cop"`
			} `json:"expected"`
		} `json:"chillerPoints"`
		OperatingPoints []struct {
			Name      string  `json:"name"`
			LoadKW    float64 `json:"loadKw"`
			ZoneTempC float64 `json:"zoneTempC"`
			TChwC     float64 `json:"tChwC"`
			DryBulbC  float64 `json:"dryBulbC"`
			WetBulbC  float64 `json:"wetBulbC"`
			RHPct     float64 `json:"rhPct"`
			COPScale  float64 `json:"copScale"`
			Expected  struct {
				Feasible        bool    `json:"feasible"`
				TargetChillers  int     `json:"targetChillers"`
				SupplyTempC     float64 `json:"supplyTempC"`
				CHWPFreqHz      float64 `json:"chwpFreqHz"`
				CWPFreqHz       float64 `json:"cwpFreqHz"`
				TowerFanPct     float64 `json:"towerFanPct"`
				TotalPowerKW    float64 `json:"totalPowerKw"`
				ChillerPowerKW  float64 `json:"chillerPowerKw"`
				CHWPPowerKW     float64 `json:"chwpPowerKw"`
				CWPPowerKW      float64 `json:"cwpPowerKw"`
				TowerPowerKW    float64 `json:"towerPowerKw"`
				PredictedCOP    float64 `json:"predictedCop"`
				CoilCapacityKW  float64 `json:"coilCapacityKw"`
				DeliveredLoadKW float64 `json:"deliveredLoadKw"`
			} `json:"expected"`
		} `json:"operatingPoints"`
		MPCCases []struct {
			Name     string          `json:"name"`
			Request  json.RawMessage `json:"request"`
			Expected struct {
				Candidate struct {
					SupplyTempC        float64 `json:"supplyTempC"`
					ProjectedZoneTempC float64 `json:"projectedZoneTempC"`
					DailyEnergyKWh     float64 `json:"dailyEnergyKWh"`
					DailyCost          float64 `json:"dailyCost"`
				} `json:"candidate"`
				ExpectedImpact struct {
					EnergySavingKWhPerDay float64 `json:"energySavingKWhPerDay"`
					CostSavingPerDay      float64 `json:"costSavingPerDay"`
				} `json:"expectedImpact"`
				Risk struct {
					Level          string  `json:"level"`
					ComfortMarginC float64 `json:"comfortMarginC"`
				} `json:"risk"`
				TargetRunningChillers int                  `json:"targetRunningChillers"`
				PredictedCOP          float64              `json:"predictedCop"`
				MPCSlots              []CoupledPlannedSlot `json:"mpcSlots"`
			} `json:"expected"`
		} `json:"mpcCases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode golden parity fixture: %v", err)
	}

	assertNearParity(t, "waterHeatCapacityKwPerM3hC", measurement.WaterFactorKW, fixture.WaterHeatCapacityKWPerM3HC)
	assertNearParity(t, "coupledWaterCapKWPerM3HC", coupledWaterCapKWPerM3HC, measurement.WaterFactorKW)

	for _, tc := range fixture.ChillerPoints {
		m := DefaultMonotonicChillerModel()
		m.BaseCOP = tc.Model.BaseCOP
		m.TchwSlopeCOPPerC = tc.Model.TchwSlopeCOPPerC
		m.TcwSlopeCOPPerC = tc.Model.TcwSlopeCOPPerC
		m.CondenserQuadLossPerC2 = tc.Model.CondenserQuadLossPerC2
		m.PLRConstE0 = tc.Model.PLRConstE0
		m.COPScale = tc.Model.COPScale
		assertNearParity(t, tc.Name+".liftCop", m.LiftCOP(tc.TChwC, tc.CWEntering, tc.CWLeaving), tc.Expected.LiftCOP)
		pKW, cop := m.PowerKW(tc.LoadKW, tc.RatedCapKW, tc.TChwC, tc.CWEntering, tc.CWLeaving)
		assertNearParity(t, tc.Name+".powerKw", pKW, tc.Expected.PowerKW)
		assertNearParity(t, tc.Name+".cop", cop, tc.Expected.COP)
	}

	spec := DefaultPlantPhysicsSpec()
	for _, tc := range fixture.OperatingPoints {
		m := DefaultMonotonicChillerModel()
		m.COPScale = tc.COPScale
		got := EvaluateOptimalOperatingPoint(tc.LoadKW, tc.ZoneTempC, tc.TChwC, tc.DryBulbC, tc.WetBulbC, tc.RHPct, m, spec)
		if got.Feasible != tc.Expected.Feasible || got.TargetChillers != tc.Expected.TargetChillers {
			t.Fatalf("%s discrete mismatch: got (%v, %d), want (%v, %d)", tc.Name, got.Feasible, got.TargetChillers, tc.Expected.Feasible, tc.Expected.TargetChillers)
		}
		assertNearParity(t, tc.Name+".supplyTempC", got.SupplyTempC, tc.Expected.SupplyTempC)
		assertNearParity(t, tc.Name+".chwpFreqHz", got.CHWPFreqHz, tc.Expected.CHWPFreqHz)
		assertNearParity(t, tc.Name+".cwpFreqHz", got.CWPFreqHz, tc.Expected.CWPFreqHz)
		assertNearParity(t, tc.Name+".towerFanPct", got.TowerFanPct, tc.Expected.TowerFanPct)
		assertNearParity(t, tc.Name+".totalPowerKw", got.TotalPowerKW, tc.Expected.TotalPowerKW)
		assertNearParity(t, tc.Name+".chillerPowerKw", got.ChillerPowerKW, tc.Expected.ChillerPowerKW)
		assertNearParity(t, tc.Name+".chwpPowerKw", got.CHWPPowerKW, tc.Expected.CHWPPowerKW)
		assertNearParity(t, tc.Name+".cwpPowerKw", got.CWPPowerKW, tc.Expected.CWPPowerKW)
		assertNearParity(t, tc.Name+".towerPowerKw", got.TowerPowerKW, tc.Expected.TowerPowerKW)
		assertNearParity(t, tc.Name+".predictedCop", got.PredictedCOP, tc.Expected.PredictedCOP)
		assertNearParity(t, tc.Name+".coilCapacityKw", got.CoilCapacityKW, tc.Expected.CoilCapacityKW)
		assertNearParity(t, tc.Name+".deliveredLoadKw", got.DeliveredLoadKW, tc.Expected.DeliveredLoadKW)
	}

	for _, tc := range fixture.MPCCases {
		handler := NewCoupledSolverHTTPHandler(NewCoupledPlantMPCSolver())
		req := httptest.NewRequest(http.MethodPost, "/v1/solve", bytes.NewReader(tc.Request))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /v1/solve status = %d body=%s", tc.Name, rec.Code, rec.Body.String())
		}
		var resp coupledSolverHTTPResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s decode response: %v", tc.Name, err)
		}
		assertNearParity(t, tc.Name+".supplyTempC", resp.Candidate.SupplyTempC, tc.Expected.Candidate.SupplyTempC)
		assertNearParity(t, tc.Name+".projectedZoneTempC", resp.Candidate.ProjectedZoneTempC, tc.Expected.Candidate.ProjectedZoneTempC)
		assertNearParity(t, tc.Name+".dailyEnergyKWh", resp.Candidate.DailyEnergyKWh, tc.Expected.Candidate.DailyEnergyKWh)
		assertNearParity(t, tc.Name+".dailyCost", resp.Candidate.DailyCost, tc.Expected.Candidate.DailyCost)
		assertNearParity(t, tc.Name+".energySavingKWhPerDay", resp.ExpectedImpact.EnergySavingKWhPerDay, tc.Expected.ExpectedImpact.EnergySavingKWhPerDay)
		assertNearParity(t, tc.Name+".costSavingPerDay", resp.ExpectedImpact.CostSavingPerDay, tc.Expected.ExpectedImpact.CostSavingPerDay)
		if resp.Risk.Level != tc.Expected.Risk.Level {
			t.Fatalf("%s risk.level = %q, want %q", tc.Name, resp.Risk.Level, tc.Expected.Risk.Level)
		}
		assertNearParity(t, tc.Name+".comfortMarginC", resp.Risk.ComfortMarginC, tc.Expected.Risk.ComfortMarginC)
		if resp.VerificationPlan.TargetRunningChillers != tc.Expected.TargetRunningChillers {
			t.Fatalf("%s targetRunningChillers = %d, want %d", tc.Name, resp.VerificationPlan.TargetRunningChillers, tc.Expected.TargetRunningChillers)
		}
		assertNearParity(t, tc.Name+".predictedCop", resp.VerificationPlan.PredictedCOP, tc.Expected.PredictedCOP)
		if len(resp.VerificationPlan.MPCSlots) != len(tc.Expected.MPCSlots) {
			t.Fatalf("%s mpcSlots len = %d, want %d", tc.Name, len(resp.VerificationPlan.MPCSlots), len(tc.Expected.MPCSlots))
		}
		for i := range tc.Expected.MPCSlots {
			gotSl, wantSl := resp.VerificationPlan.MPCSlots[i], tc.Expected.MPCSlots[i]
			if gotSl.TargetChillers != wantSl.TargetChillers || gotSl.OperatingMode != wantSl.OperatingMode {
				t.Fatalf("%s slot %d discrete mismatch: got (%d, %q), want (%d, %q)", tc.Name, i, gotSl.TargetChillers, gotSl.OperatingMode, wantSl.TargetChillers, wantSl.OperatingMode)
			}
			assertNearParity(t, tc.Name+".slot.supplyTempC", gotSl.SupplyTempC, wantSl.SupplyTempC)
			assertNearParity(t, tc.Name+".slot.chwpFreqHz", gotSl.CHWPFreqHz, wantSl.CHWPFreqHz)
			assertNearParity(t, tc.Name+".slot.cwpFreqHz", gotSl.CWPFreqHz, wantSl.CWPFreqHz)
			assertNearParity(t, tc.Name+".slot.towerFanPct", gotSl.TowerFanPct, wantSl.TowerFanPct)
			assertNearParity(t, tc.Name+".slot.projectedZoneTempC", gotSl.ProjectedZoneTempC, wantSl.ProjectedZoneTempC)
			assertNearParity(t, tc.Name+".slot.predictedPowerKw", gotSl.PredictedPowerKW, wantSl.PredictedPowerKW)
			assertNearParity(t, tc.Name+".slot.predictedCop", gotSl.PredictedCOP, wantSl.PredictedCOP)
			assertNearParity(t, tc.Name+".slot.slotCostCny", gotSl.SlotCostCNY, wantSl.SlotCostCNY)
		}
	}
}
