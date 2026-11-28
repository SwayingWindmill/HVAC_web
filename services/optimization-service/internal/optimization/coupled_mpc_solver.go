package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/measurement"
)

const (
	// CoupledMPCBuiltinSolverRef is the canonical servingRef for the built-in coupled grey-box MPC solver.
	CoupledMPCBuiltinSolverRef = "builtin://coupled-plant-mpc-v1"
	// CoupledMPCSolverVersion is stored on recommendations produced by CoupledPlantMPCSolver.
	CoupledMPCSolverVersion = "coupled-plant-mpc-v1"

	coupledHardMinSupplyTempC = 5.0
	coupledWaterCapKWPerM3HC  = measurement.WaterFactorKW
	coupledFallbackRHPct      = 65.0
	coupledZoneSetpointC      = 24.0
	coupledDesignSupplyTempC  = 7.0
)

// EstimateWetBulbC computes ambient wet-bulb temperature from dry-bulb temperature (°C)
// and relative humidity (%) using Stull's (2011) inverse psychrometric approximation.
// When relative humidity is absent (non-positive or non-finite), it falls back gracefully to 65% RH.
func EstimateWetBulbC(dryBulbC, relativeHumidityPct float64) float64 {
	if !finite(dryBulbC) {
		return 24.0
	}
	rh := relativeHumidityPct
	if !finite(rh) || rh <= 0 {
		rh = coupledFallbackRHPct
	}
	rh = clampFloat(rh, 5.0, 100.0)
	tw := dryBulbC*math.Atan(0.151977*math.Sqrt(rh+8.313659)) +
		math.Atan(dryBulbC+rh) -
		math.Atan(rh-1.676331) +
		0.00391838*math.Pow(rh, 1.5)*math.Atan(0.023101*rh) -
		4.686035
	if tw > dryBulbC {
		tw = dryBulbC
	}
	return clampFloat(tw, -25.0, 45.0)
}

// MonotonicChillerModel represents a physical monotonic chiller COP/EIR curve.
// Coefficient projection guarantees dPower/dLoad > 0, dPower/dTcw >= 0, and dPower/dTchw <= 0.
type MonotonicChillerModel struct {
	BaseCOP                float64 `json:"baseCop"`
	RefTchwC               float64 `json:"refTchwC"`
	RefTcwC                float64 `json:"refTcwC"`
	TchwSlopeCOPPerC       float64 `json:"tchwSlopeCopPerC"`
	TcwSlopeCOPPerC        float64 `json:"tcwSlopeCopPerC"`
	CondenserQuadLossPerC2 float64 `json:"condenserQuadLossPerC2"`
	CondenserMeanWeight    float64 `json:"condenserMeanWeight"`
	PLRConstE0             float64 `json:"plrConstE0"`
	PLRLinearE1            float64 `json:"plrLinearE1"`
	PLRQuadE2              float64 `json:"plrQuadE2"`
	COPScale               float64 `json:"copScale"`
}

// DefaultMonotonicChillerModel returns physically grounded nominal chiller curve coefficients.
func DefaultMonotonicChillerModel() MonotonicChillerModel {
	return MonotonicChillerModel{
		BaseCOP:                5.7,
		RefTchwC:               7.0,
		RefTcwC:                30.0,
		TchwSlopeCOPPerC:       0.17,
		TcwSlopeCOPPerC:        0.145,
		CondenserQuadLossPerC2: 0.0025,
		CondenserMeanWeight:    0.45,
		PLRConstE0:             0.09,
		PLRLinearE1:            0.57,
		PLRQuadE2:              0.34,
		COPScale:               1.0,
	}.ProjectMonotonic()
}

// ProjectMonotonic enforces physical sign constraints so partial derivatives remain monotonic everywhere.
func (m MonotonicChillerModel) ProjectMonotonic() MonotonicChillerModel {
	if !finitePositive(m.BaseCOP) {
		m.BaseCOP = 5.7
	}
	m.BaseCOP = clampFloat(m.BaseCOP, 2.2, 9.5)
	if !finite(m.RefTchwC) {
		m.RefTchwC = 7.0
	}
	if !finite(m.RefTcwC) {
		m.RefTcwC = 30.0
	}
	m.TchwSlopeCOPPerC = math.Max(0.02, finiteOr(m.TchwSlopeCOPPerC, 0.17))
	m.TcwSlopeCOPPerC = math.Max(0.02, finiteOr(m.TcwSlopeCOPPerC, 0.145))
	m.CondenserQuadLossPerC2 = math.Max(0.0, finiteOr(m.CondenserQuadLossPerC2, 0.0025))
	m.CondenserMeanWeight = clampFloat(finiteOr(m.CondenserMeanWeight, 0.45), 0.0, 1.0)
	m.PLRConstE0 = math.Max(0.02, finiteOr(m.PLRConstE0, 0.09))
	m.PLRLinearE1 = math.Max(0.25, finiteOr(m.PLRLinearE1, 0.57))
	m.PLRQuadE2 = math.Max(0.02, finiteOr(m.PLRQuadE2, 0.34))
	sumE := m.PLRConstE0 + m.PLRLinearE1 + m.PLRQuadE2
	m.PLRConstE0 /= sumE
	m.PLRLinearE1 /= sumE
	m.PLRQuadE2 /= sumE
	m.COPScale = clampFloat(finiteOr(m.COPScale, 1.0), 0.55, 1.35)
	return m
}

// LiftCOP computes temperature-dependent chiller COP at a given chilled-water supply and condenser temperature pair.
func (m MonotonicChillerModel) LiftCOP(tChwC, cwEnteringC, cwLeavingC float64) float64 {
	proj := m.ProjectMonotonic()
	cwEff := (1.0-proj.CondenserMeanWeight)*cwEnteringC + proj.CondenserMeanWeight*cwLeavingC
	cwOver := math.Max(0.0, cwEff-proj.RefTcwC)
	rawCOP := proj.BaseCOP +
		proj.TchwSlopeCOPPerC*(tChwC-proj.RefTchwC) -
		proj.TcwSlopeCOPPerC*(cwEff-proj.RefTcwC) -
		proj.CondenserQuadLossPerC2*cwOver*cwOver
	return clampFloat(proj.COPScale*math.Max(1.8, rawCOP), 1.5, 11.0)
}

// PowerKW returns electrical input power (kW) and effective COP for a chiller delivering loadKW.
// By construction dPower/dLoad > 0, dPower/dCw >= 0, and dPower/dTchw <= 0.
func (m MonotonicChillerModel) PowerKW(loadKW, ratedCapKW, tChwC, cwEnteringC, cwLeavingC float64) (powerKW, cop float64) {
	if loadKW <= 0 || ratedCapKW <= 0 {
		return 0, 0
	}
	proj := m.ProjectMonotonic()
	liftCOP := proj.LiftCOP(tChwC, cwEnteringC, cwLeavingC)
	plr := loadKW / ratedCapKW
	eirPLR := proj.PLRConstE0 + proj.PLRLinearE1*plr + proj.PLRQuadE2*plr*plr
	powerKW = ratedCapKW * eirPLR / liftCOP
	if powerKW <= 0 {
		return 0, 0
	}
	return powerKW, loadKW / powerKW
}

// CoupledChillerSpec describes one physical chiller unit used for staging and load-sharing.
type CoupledChillerSpec struct {
	ID             string  `json:"id"`
	RatedCoolingKW float64 `json:"ratedCoolingKw"`
	BaseCOP        float64 `json:"baseCop"`
}

// PlantPhysicsSpec aggregates chilled/cooling water pump, cooling tower, coil, and zone RC parameters.
type PlantPhysicsSpec struct {
	Chillers               []CoupledChillerSpec `json:"chillers"`
	CHWPRatedKW            float64              `json:"chwpRatedKw"`
	CHWPRatedFlowM3H       float64              `json:"chwpRatedFlowM3h"`
	CHWPStaticHeadFrac     float64              `json:"chwpStaticHeadFrac"`
	CHWPMinHz              float64              `json:"chwpMinHz"`
	CHWPMaxHz              float64              `json:"chwpMaxHz"`
	CWPRatedKW             float64              `json:"cwpRatedKw"`
	CWPRatedFlowM3H        float64              `json:"cwpRatedFlowM3h"`
	CWPStaticHeadFrac      float64              `json:"cwpStaticHeadFrac"`
	CWPMinHz               float64              `json:"cwpMinHz"`
	CWPMaxHz               float64              `json:"cwpMaxHz"`
	TowerRatedKW           float64              `json:"towerRatedKw"`
	TowerDesignHeatRejKW   float64              `json:"towerDesignHeatRejKw"`
	TowerMinApproachC      float64              `json:"towerMinApproachC"`
	TowerLoadApproachCoeff float64              `json:"towerLoadApproachCoeff"`
	TowerNonlinearExp      float64              `json:"towerNonlinearExp"`
	TowerLowFanPenaltyC    float64              `json:"towerLowFanPenaltyC"`
	TowerMinFanPct         float64              `json:"towerMinFanPct"`
	TowerMaxFanPct         float64              `json:"towerMaxFanPct"`
	ZoneThermalMassKWhPerC float64              `json:"zoneThermalMassKwhPerC"`
	ZoneSetpointC          float64              `json:"zoneSetpointC"`
	DesignSupplyTempC      float64              `json:"designSupplyTempC"`
	CoilApproachMinC       float64              `json:"coilApproachMinC"`
	CoilFlowExp            float64              `json:"coilFlowExp"`
}

// DefaultPlantPhysicsSpec returns realistic default physical parameters matching standard dual-chiller central plants.
func DefaultPlantPhysicsSpec() PlantPhysicsSpec {
	return PlantPhysicsSpec{
		Chillers: []CoupledChillerSpec{
			{ID: "CHILLER-01", RatedCoolingKW: 600, BaseCOP: 5.8},
			{ID: "CHILLER-02", RatedCoolingKW: 600, BaseCOP: 5.6},
		},
		CHWPRatedKW:            45,
		CHWPRatedFlowM3H:       220,
		CHWPStaticHeadFrac:     0.10,
		CHWPMinHz:              commandmodel.DefaultPumpFrequencyMinHz,
		CHWPMaxHz:              commandmodel.DefaultPumpFrequencyMaxHz,
		CWPRatedKW:             37,
		CWPRatedFlowM3H:        260,
		CWPStaticHeadFrac:      0.12,
		CWPMinHz:               commandmodel.DefaultPumpFrequencyMinHz,
		CWPMaxHz:               commandmodel.DefaultPumpFrequencyMaxHz,
		TowerRatedKW:           22,
		TowerDesignHeatRejKW:   1420,
		TowerMinApproachC:      1.6,
		TowerLoadApproachCoeff: 1.8,
		TowerNonlinearExp:      1.25,
		TowerLowFanPenaltyC:    1.4,
		TowerMinFanPct:         commandmodel.DefaultFanSpeedMinPct,
		TowerMaxFanPct:         commandmodel.DefaultFanSpeedMaxPct,
		ZoneThermalMassKWhPerC: 300,
		ZoneSetpointC:          coupledZoneSetpointC,
		DesignSupplyTempC:      coupledDesignSupplyTempC,
		CoilApproachMinC:       4.5,
		CoilFlowExp:            0.72,
	}
}

func (s PlantPhysicsSpec) normalize() PlantPhysicsSpec {
	def := DefaultPlantPhysicsSpec()
	out := s
	if len(out.Chillers) == 0 {
		out.Chillers = append([]CoupledChillerSpec(nil), def.Chillers...)
	} else {
		clean := make([]CoupledChillerSpec, len(out.Chillers))
		for i, ch := range out.Chillers {
			if ch.ID == "" {
				ch.ID = fmt.Sprintf("CHILLER-%02d", i+1)
			}
			if !finitePositive(ch.RatedCoolingKW) {
				ch.RatedCoolingKW = 600
			}
			if !finitePositive(ch.BaseCOP) {
				ch.BaseCOP = 5.7
			}
			clean[i] = ch
		}
		out.Chillers = clean
	}
	totalCap := out.totalCoolingCapacityKW()
	out.CHWPRatedKW = finitePositiveOr(out.CHWPRatedKW, def.CHWPRatedKW)
	out.CHWPRatedFlowM3H = finitePositiveOr(out.CHWPRatedFlowM3H, def.CHWPRatedFlowM3H)
	out.CHWPStaticHeadFrac = clampFloat(finiteOr(out.CHWPStaticHeadFrac, def.CHWPStaticHeadFrac), 0, 0.5)
	out.CHWPMinHz = clampFloat(finitePositiveOr(out.CHWPMinHz, def.CHWPMinHz), 15, 50)
	out.CHWPMaxHz = clampFloat(finitePositiveOr(out.CHWPMaxHz, def.CHWPMaxHz), out.CHWPMinHz, 60)

	out.CWPRatedKW = finitePositiveOr(out.CWPRatedKW, def.CWPRatedKW)
	out.CWPRatedFlowM3H = finitePositiveOr(out.CWPRatedFlowM3H, def.CWPRatedFlowM3H)
	out.CWPStaticHeadFrac = clampFloat(finiteOr(out.CWPStaticHeadFrac, def.CWPStaticHeadFrac), 0, 0.5)
	out.CWPMinHz = clampFloat(finitePositiveOr(out.CWPMinHz, def.CWPMinHz), 15, 50)
	out.CWPMaxHz = clampFloat(finitePositiveOr(out.CWPMaxHz, def.CWPMaxHz), out.CWPMinHz, 60)

	out.TowerRatedKW = finitePositiveOr(out.TowerRatedKW, def.TowerRatedKW)
	out.TowerDesignHeatRejKW = finitePositiveOr(out.TowerDesignHeatRejKW, totalCap*(1.0+1.0/out.weightedBaseCOP()))
	out.TowerMinApproachC = clampFloat(finiteOr(out.TowerMinApproachC, def.TowerMinApproachC), 0.5, 6.0)
	out.TowerLoadApproachCoeff = clampFloat(finiteOr(out.TowerLoadApproachCoeff, def.TowerLoadApproachCoeff), 0.2, 6.0)
	out.TowerNonlinearExp = clampFloat(finiteOr(out.TowerNonlinearExp, def.TowerNonlinearExp), 0.8, 2.0)
	out.TowerLowFanPenaltyC = clampFloat(finiteOr(out.TowerLowFanPenaltyC, def.TowerLowFanPenaltyC), 0.0, 4.0)
	out.TowerMinFanPct = clampFloat(finitePositiveOr(out.TowerMinFanPct, def.TowerMinFanPct), 15, 100)
	out.TowerMaxFanPct = clampFloat(finitePositiveOr(out.TowerMaxFanPct, def.TowerMaxFanPct), out.TowerMinFanPct, 100)

	out.ZoneThermalMassKWhPerC = finitePositiveOr(out.ZoneThermalMassKWhPerC, totalCap*0.25)
	out.ZoneSetpointC = finiteOr(out.ZoneSetpointC, def.ZoneSetpointC)
	out.DesignSupplyTempC = finiteOr(out.DesignSupplyTempC, def.DesignSupplyTempC)
	out.CoilApproachMinC = clampFloat(finiteOr(out.CoilApproachMinC, def.CoilApproachMinC), 2.0, 10.0)
	out.CoilFlowExp = clampFloat(finitePositiveOr(out.CoilFlowExp, def.CoilFlowExp), 0.4, 1.0)
	return out
}

func (s PlantPhysicsSpec) totalCoolingCapacityKW() float64 {
	total := 0.0
	for _, ch := range s.Chillers {
		total += ch.RatedCoolingKW
	}
	return total
}

func (s PlantPhysicsSpec) weightedBaseCOP() float64 {
	capSum := 0.0
	weighted := 0.0
	for _, ch := range s.Chillers {
		capSum += ch.RatedCoolingKW
		weighted += ch.RatedCoolingKW * ch.BaseCOP
	}
	if capSum <= 0 {
		return 5.7
	}
	return weighted / capSum
}

// VariableSpeedPumpPowerKW computes VFD centrifugal pump power with static head + cubic affinity curve.
func VariableSpeedPumpPowerKW(ratedKW, staticHeadFrac, freqHz, maxHz float64) float64 {
	if ratedKW <= 0 || maxHz <= 0 || freqHz <= 0 {
		return 0
	}
	flowFrac := clampFloat(freqHz/maxHz, 0, 1)
	staticFrac := clampFloat(staticHeadFrac, 0, 0.5)
	return ratedKW * (staticFrac + (1.0-staticFrac)*flowFrac*flowFrac*flowFrac)
}

// TowerApproachC evaluates nonlinear cooling-tower approach temperature (°C) given heat rejection ratio and fan speed (%).
func TowerApproachC(spec PlantPhysicsSpec, heatRejKW, fanPct float64) float64 {
	designRej := spec.TowerDesignHeatRejKW
	if designRej <= 0 {
		designRej = 1420
	}
	heatRatio := clampFloat(heatRejKW/designRej, 0.05, 1.5)
	speedFrac := clampFloat(fanPct/100.0, 0.05, 1.0)
	lowFanPenalty := 0.0
	if speedFrac < 0.55 {
		lowFanPenalty = spec.TowerLowFanPenaltyC * (0.55 - speedFrac) / 0.55
	}
	return spec.TowerMinApproachC +
		spec.TowerLoadApproachCoeff*math.Pow(heatRatio, spec.TowerNonlinearExp)/math.Pow(speedFrac, 0.85) +
		lowFanPenalty
}

// CoilCapacityLimitKW evaluates hydronic AHU coil capacity given zone temp, CHW supply temp, and pump frequency.
func CoilCapacityLimitKW(spec PlantPhysicsSpec, zoneTempC, tChwC, chwpHz float64) float64 {
	totalRated := spec.totalCoolingCapacityKW()
	denom := math.Max(1.0, spec.ZoneSetpointC-spec.DesignSupplyTempC)
	coilKWPerC := totalRated / denom
	flowFrac := clampFloat(chwpHz/spec.CHWPMaxHz, 0.05, 1.0)
	flowEffectiveness := math.Pow(flowFrac, spec.CoilFlowExp)
	deltaT := math.Max(0.0, zoneTempC-tChwC)
	limit := coilKWPerC * deltaT * flowEffectiveness
	if zoneTempC-tChwC < spec.CoilApproachMinC {
		approachRatio := math.Max(0.0, (zoneTempC-tChwC)/math.Max(1.0, spec.CoilApproachMinC))
		limit *= approachRatio
	}
	return limit
}

// CoupledOperatingPoint summarizes one optimal steady hydraulic/thermodynamic plant operating point.
type CoupledOperatingPoint struct {
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
	PredictedCWInC  float64 `json:"predictedCwInC"`
	PredictedCWOutC float64 `json:"predictedCwOutC"`
	CoilCapacityKW  float64 `json:"coilCapacityKw"`
	DeliveredLoadKW float64 `json:"deliveredLoadKw"`
}

// EvaluateOptimalOperatingPoint jointly picks chiller count, CHWP Hz, CWP Hz, and CT fan %
// minimizing total coupled plant electric power while honoring coil capacity and flow bounds.
func EvaluateOptimalOperatingPoint(
	loadKW, zoneTempC, tChwC, dryBulbC, wetBulbC, rhPct float64,
	model MonotonicChillerModel,
	spec PlantPhysicsSpec,
) CoupledOperatingPoint {
	spec = spec.normalize()
	model = model.ProjectMonotonic()
	if !finite(wetBulbC) || wetBulbC <= -25 || wetBulbC > dryBulbC {
		wetBulbC = EstimateWetBulbC(dryBulbC, rhPct)
	}
	loadKW = math.Max(10.0, loadKW)

	// Minimum CHWP frequency that satisfies hydraulic interlock flow (>=38% safety over 35%),
	// delta-T limit (<= 7.8C), and coil capacity limit with 4% safety headroom.
	const minSafetyFlowFrac = 0.38
	minInterlockHz := minSafetyFlowFrac * spec.CHWPMaxHz
	minDeltaTHz := (loadKW / (coupledWaterCapKWPerM3HC * 7.8 * math.Max(1.0, spec.CHWPRatedFlowM3H))) * spec.CHWPMaxHz
	chwpStart := math.Max(spec.CHWPMinHz, math.Max(minInterlockHz, minDeltaTHz))
	chwpStart = clampFloat(math.Ceil(chwpStart*2.0)/2.0, spec.CHWPMinHz, spec.CHWPMaxHz)

	chwpHz := chwpStart
	coilCap := CoilCapacityLimitKW(spec, zoneTempC, tChwC, chwpHz)
	for hz := chwpStart; hz <= spec.CHWPMaxHz+1e-9; hz += 1.0 {
		clampedHz := math.Min(hz, spec.CHWPMaxHz)
		cap := CoilCapacityLimitKW(spec, zoneTempC, tChwC, clampedHz)
		if cap >= loadKW*1.03 {
			chwpHz = clampedHz
			coilCap = cap
			break
		}
		chwpHz = clampedHz
		coilCap = cap
	}
	if coilCap < loadKW*0.97 {
		// Unmet coil headroom: still evaluate max CHWP flow so caller sees penalty/unmet load.
		chwpHz = spec.CHWPMaxHz
		coilCap = CoilCapacityLimitKW(spec, zoneTempC, tChwC, chwpHz)
	}
	deliveredKW := math.Min(loadKW, math.Max(0, coilCap))
	chwpKW := VariableSpeedPumpPowerKW(spec.CHWPRatedKW, spec.CHWPStaticHeadFrac, chwpHz, spec.CHWPMaxHz)

	best := CoupledOperatingPoint{
		Feasible:        coilCap >= loadKW*0.97,
		SupplyTempC:     tChwC,
		CHWPFreqHz:      chwpHz,
		CHWPPowerKW:     chwpKW,
		CoilCapacityKW:  coilCap,
		DeliveredLoadKW: deliveredKW,
		TotalPowerKW:    math.Inf(1),
	}

	cwpFloorHz := math.Max(spec.CWPMinHz, minSafetyFlowFrac*spec.CWPMaxHz)
	// Evaluate feasible chiller counts (preferring 1..N that cover deliveredKW within safe overload margin).
	for nChillers := 1; nChillers <= len(spec.Chillers); nChillers++ {
		availCap := 0.0
		for i := 0; i < nChillers; i++ {
			availCap += spec.Chillers[i].RatedCoolingKW
		}
		if nChillers < len(spec.Chillers) && deliveredKW > availCap*0.99 {
			continue
		}
		// Avoid multi-chiller idle overhead when fewer chillers sit comfortably below high-load penalty region.
		if nChillers > 1 && deliveredKW < 0.68*(availCap-spec.Chillers[nChillers-1].RatedCoolingKW) {
			continue
		}

		// Coarse then refined search over (CWP Hz, Tower Fan %).
		bestCWP, bestFan := cwpFloorHz, clampFloat(spec.TowerMinFanPct, 25, 100)
		bestPointKW := math.Inf(1)
		evalAux := func(cwpHz, fanPct float64, iters int) (totalKW, chillerKW, cwpKW, towerKW, cwInC, cwOutC, cop float64) {
			cwpFlow := spec.CWPRatedFlowM3H * clampFloat(cwpHz/spec.CWPMaxHz, 0.2, 1.0)
			if cwpFlow <= 0 {
				return math.Inf(1), 0, 0, 0, 0, 0, 0
			}
			chillerKW = deliveredKW / model.BaseCOP
			for iter := 0; iter < iters; iter++ {
				heatRejKW := deliveredKW + chillerKW
				approach := TowerApproachC(spec, heatRejKW, fanPct)
				cwInC = wetBulbC + approach
				cwOutC = cwInC + heatRejKW/(coupledWaterCapKWPerM3HC*cwpFlow)
				sumP := 0.0
				for i := 0; i < nChillers; i++ {
					share := spec.Chillers[i].RatedCoolingKW / availCap
					unitModel := model
					unitModel.BaseCOP = spec.Chillers[i].BaseCOP
					pUnit, _ := unitModel.PowerKW(deliveredKW*share, spec.Chillers[i].RatedCoolingKW, tChwC, cwInC, cwOutC)
					sumP += pUnit
				}
				chillerKW = sumP
			}
			cwpKW = VariableSpeedPumpPowerKW(spec.CWPRatedKW, spec.CWPStaticHeadFrac, cwpHz, spec.CWPMaxHz)
			fanFrac := clampFloat(fanPct/100.0, 0, 1)
			towerKW = spec.TowerRatedKW * fanFrac * fanFrac * fanFrac
			totalKW = chillerKW + chwpKW + cwpKW + towerKW
			if chillerKW > 0 {
				cop = deliveredKW / chillerKW
			}
			return
		}

		for cwp := cwpFloorHz; cwp <= spec.CWPMaxHz+1e-9; cwp += 5.0 {
			cwpClamped := math.Min(cwp, spec.CWPMaxHz)
			for fan := spec.TowerMinFanPct; fan <= spec.TowerMaxFanPct+1e-9; fan += 12.0 {
				fanClamped := math.Min(fan, spec.TowerMaxFanPct)
				tot, _, _, _, _, _, _ := evalAux(cwpClamped, fanClamped, 1)
				if tot < bestPointKW {
					bestPointKW = tot
					bestCWP = cwpClamped
					bestFan = fanClamped
				}
			}
		}
		// Local polish around coarse optimum with full fixed-point convergence.
		cwpLow := math.Max(cwpFloorHz, bestCWP-4.0)
		cwpHigh := math.Min(spec.CWPMaxHz, bestCWP+4.0)
		fanLow := math.Max(spec.TowerMinFanPct, bestFan-10.0)
		fanHigh := math.Min(spec.TowerMaxFanPct, bestFan+10.0)
		for cwp := cwpLow; cwp <= cwpHigh+1e-9; cwp += 1.5 {
			cwpClamped := math.Min(cwp, spec.CWPMaxHz)
			for fan := fanLow; fan <= fanHigh+1e-9; fan += 4.0 {
				fanClamped := math.Min(fan, spec.TowerMaxFanPct)
				tot, chKW, cwpKW, twrKW, cwIn, cwOut, cop := evalAux(cwpClamped, fanClamped, 2)
				if tot < best.TotalPowerKW {
					best = CoupledOperatingPoint{
						Feasible:        coilCap >= loadKW*0.97 && deliveredKW <= availCap*1.02,
						TargetChillers:  nChillers,
						SupplyTempC:     tChwC,
						CHWPFreqHz:      chwpHz,
						CWPFreqHz:       round4(cwpClamped),
						TowerFanPct:     round4(fanClamped),
						TotalPowerKW:    tot,
						ChillerPowerKW:  chKW,
						CHWPPowerKW:     chwpKW,
						CWPPowerKW:      cwpKW,
						TowerPowerKW:    twrKW,
						PredictedCOP:    cop,
						PredictedCWInC:  cwIn,
						PredictedCWOutC: cwOut,
						CoilCapacityKW:  coilCap,
						DeliveredLoadKW: deliveredKW,
					}
				}
			}
		}
	}

	if math.IsInf(best.TotalPowerKW, 1) {
		best.TargetChillers = len(spec.Chillers)
		best.CWPFreqHz = spec.CWPMaxHz
		best.TowerFanPct = spec.TowerMaxFanPct
		best.TotalPowerKW = 0
	}
	return best
}

// ObservedPlantTelemetry feeds online closed-loop grey-box residual adaptation without peeking at truth mismatch.
type ObservedPlantTelemetry struct {
	CoolingCapacityKW     float64 `json:"coolingCapacityKw"`
	ChillerPowerKW        float64 `json:"chillerPowerKw"`
	ChilledWaterSupplyC   float64 `json:"chilledWaterSupplyC"`
	CoolingWaterEnteringC float64 `json:"coolingWaterEnteringC"`
	CoolingWaterLeavingC  float64 `json:"coolingWaterLeavingC"`
	RunningChillers       int     `json:"runningChillers"`
	RatedRunningCapKW     float64 `json:"ratedRunningCapKw"`
	Steady                bool    `json:"steady"`
}

// GreyBoxResidualState stores online EMA-adapted chiller COP scale factor.
type GreyBoxResidualState struct {
	COPScale     float64 `json:"copScale"`
	SampleCount  int     `json:"sampleCount"`
	LastObserved float64 `json:"lastObservedCop"`
}

// CoupledHorizonSlot defines forecast inputs for a single MPC planning step (typically 1 hour).
type CoupledHorizonSlot struct {
	StartAt         time.Time `json:"startAt"`
	DurationHours   float64   `json:"durationHours"`
	CoolingLoadKW   float64   `json:"coolingLoadKw"`
	AmbientDryBulbC float64   `json:"ambientDryBulbC"`
	AmbientWetBulbC float64   `json:"ambientWetBulbC"`
	RelHumidityPct  float64   `json:"relHumidityPct"`
	TariffRateCNY   float64   `json:"tariffRateCny"`
}

// CoupledPlantMPCInput parameterizes a rolling-horizon coupled grey-box MPC solve.
type CoupledPlantMPCInput struct {
	CurrentSupplyTempC        float64              `json:"currentSupplyTempC"`
	ObservedZoneTempC         float64              `json:"observedZoneTempC"`
	ZoneTempUncertaintyP90C   float64              `json:"zoneTempUncertaintyP90C"`
	EnergyUncertaintyP90KWh   float64              `json:"energyUncertaintyP90Kwh"`
	SupplyTempMinC            float64              `json:"supplyTempMinC"`
	SupplyTempMaxC            float64              `json:"supplyTempMaxC"`
	ZoneTempMinC              float64              `json:"zoneTempMinC"`
	ZoneTempMaxC              float64              `json:"zoneTempMaxC"`
	MaxSupplyTempStepC        float64              `json:"maxSupplyTempStepC"`
	DailyBaselineEnergyKWh    float64              `json:"dailyBaselineEnergyKwh"`
	DailyBaselineCostCNY      float64              `json:"dailyBaselineCostCny"`
	ZoneTempDeltaPerSupplyC   float64              `json:"zoneTempDeltaPerSupplyC"`
	DailyEnergyDeltaPerSupply float64              `json:"dailyEnergyDeltaPerSupply"`
	PrecoolingEnabled         bool                 `json:"precoolingEnabled"`
	TariffRateCNY             float64              `json:"tariffRateCny"`
	CurrentLoadKW             float64              `json:"currentLoadKw"`
	AmbientDryBulbC           float64              `json:"ambientDryBulbC"`
	AmbientWetBulbC           float64              `json:"ambientWetBulbC"`
	RelHumidityPct            float64              `json:"relHumidityPct"`
	Horizon                   []CoupledHorizonSlot `json:"horizon,omitempty"`
	Plant                     PlantPhysicsSpec     `json:"plant"`
}

// CoupledPlannedSlot records one hour of the solved 24h trajectory (consumable as EdgeEnergySchedule slots).
type CoupledPlannedSlot struct {
	StartAt            time.Time `json:"startAt"`
	DurationHours      float64   `json:"durationHours"`
	SupplyTempC        float64   `json:"supplyTempC"`
	CHWPFreqHz         float64   `json:"chwpFreqHz"`
	CWPFreqHz          float64   `json:"cwpFreqHz"`
	TowerFanPct        float64   `json:"towerFanPct"`
	TargetChillers     int       `json:"targetChillers"`
	ProjectedZoneTempC float64   `json:"projectedZoneTempC"`
	PredictedPowerKW   float64   `json:"predictedPowerKw"`
	PredictedCOP       float64   `json:"predictedCop"`
	SlotCostCNY        float64   `json:"slotCostCny"`
	OperatingMode      string    `json:"operatingMode"`
}

// CoupledPlantMPCSolution contains the optimal immediate recommendation and rolling-horizon trajectory.
type CoupledPlantMPCSolution struct {
	SupplyTempC           float64              `json:"supplyTempC"`
	ProjectedZoneTempC    float64              `json:"projectedZoneTempC"`
	CHWPFreqHz            float64              `json:"chwpFreqHz"`
	CWPFreqHz             float64              `json:"cwpFreqHz"`
	TowerFanPct           float64              `json:"towerFanPct"`
	TargetChillers        int                  `json:"targetChillers"`
	HorizonEnergyKWh      float64              `json:"horizonEnergyKwh"`
	HorizonCostCNY        float64              `json:"horizonCostCny"`
	DailyEnergyKWh        float64              `json:"dailyEnergyKwh"`
	DailyCostCNY          float64              `json:"dailyCostCny"`
	EnergySavingKWhPerDay float64              `json:"energySavingKwhPerDay"`
	CostSavingPerDay      float64              `json:"costSavingPerDay"`
	ComfortMarginC        float64              `json:"comfortMarginC"`
	PredictedCOP          float64              `json:"predictedCop"`
	CalibratedCOPScale    float64              `json:"calibratedCopScale"`
	Slots                 []CoupledPlannedSlot `json:"slots"`
	SolveDuration         time.Duration        `json:"solveDuration"`
}

// CoupledPlantMPCSolver implements SolverAdapter and 24h rolling-horizon coupled grey-box MPC.
type CoupledPlantMPCSolver struct {
	mu       sync.RWMutex
	model    MonotonicChillerModel
	plant    PlantPhysicsSpec
	residual GreyBoxResidualState
}

// NewCoupledPlantMPCSolver initializes the production coupled grey-box MPC solver.
func NewCoupledPlantMPCSolver() *CoupledPlantMPCSolver {
	return &CoupledPlantMPCSolver{
		model: DefaultMonotonicChillerModel(),
		plant: DefaultPlantPhysicsSpec(),
		residual: GreyBoxResidualState{
			COPScale: 1.0,
		},
	}
}

// ResidualState returns the latest calibrated grey-box COP residual state.
func (s *CoupledPlantMPCSolver) ResidualState() GreyBoxResidualState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.residual
}

// UpdateResidualFromTelemetry adapts internal chiller COP multiplier strictly from observed meters.
func (s *CoupledPlantMPCSolver) UpdateResidualFromTelemetry(obs ObservedPlantTelemetry) GreyBoxResidualState {
	if !obs.Steady || obs.CoolingCapacityKW < 80 || obs.ChillerPowerKW < 15 || obs.RatedRunningCapKW <= 0 {
		return s.ResidualState()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nominal := s.model
	nominal.COPScale = 1.0
	nomPowerKW, nomCOP := nominal.PowerKW(
		obs.CoolingCapacityKW,
		obs.RatedRunningCapKW,
		obs.ChilledWaterSupplyC,
		obs.CoolingWaterEnteringC,
		obs.CoolingWaterLeavingC,
	)
	if nomPowerKW <= 0 || nomCOP <= 0 {
		return s.residual
	}
	obsCOP := obs.CoolingCapacityKW / obs.ChillerPowerKW
	ratio := clampFloat(nomPowerKW/obs.ChillerPowerKW, 0.60, 1.25)
	const emaAlpha = 0.22
	if s.residual.SampleCount == 0 {
		s.residual.COPScale = clampFloat(0.5+0.5*ratio, 0.60, 1.25)
	} else {
		s.residual.COPScale = clampFloat((1.0-emaAlpha)*s.residual.COPScale+emaAlpha*ratio, 0.60, 1.25)
	}
	s.residual.SampleCount++
	s.residual.LastObserved = obsCOP
	s.model.COPScale = s.residual.COPScale
	return s.residual
}

// SolveCoupledPlant computes an optimal 24h coupled supply/pump/tower/staging MPC trajectory.
func (s *CoupledPlantMPCSolver) SolveCoupledPlant(input CoupledPlantMPCInput) (CoupledPlantMPCSolution, error) {
	startedAt := time.Now()
	s.mu.RLock()
	model := s.model.ProjectMonotonic()
	defaultPlant := s.plant
	copScale := s.residual.COPScale
	s.mu.RUnlock()
	if copScale <= 0 {
		copScale = model.COPScale
	}
	model.COPScale = copScale

	plant := input.Plant
	if len(plant.Chillers) == 0 && !finitePositive(plant.CHWPRatedKW) {
		plant = defaultPlant
	}
	plant = plant.normalize()

	minSupply := math.Max(input.SupplyTempMinC, coupledHardMinSupplyTempC)
	maxSupply := input.SupplyTempMaxC
	if !finite(maxSupply) || maxSupply < minSupply {
		return CoupledPlantMPCSolution{}, errors.New("coupled mpc: invalid supply temperature bounds after freeze floor")
	}
	maxStep := input.MaxSupplyTempStepC
	if !finitePositive(maxStep) {
		maxStep = 1.0
	}
	if !finite(input.ZoneTempMinC) || !finite(input.ZoneTempMaxC) || input.ZoneTempMinC >= input.ZoneTempMaxC {
		return CoupledPlantMPCSolution{}, errors.New("coupled mpc: invalid zone temperature comfort bounds")
	}
	p90Z := math.Max(0.0, finiteOr(input.ZoneTempUncertaintyP90C, 0.2))
	zoneFloor := input.ZoneTempMinC + p90Z
	zoneCeiling := input.ZoneTempMaxC - p90Z
	if zoneFloor > zoneCeiling {
		return CoupledPlantMPCSolution{}, errors.New("coupled mpc: comfort window collapsed under uncertainty margin")
	}

	curSupply := clampFloat(finiteOr(input.CurrentSupplyTempC, plant.DesignSupplyTempC), minSupply, maxSupply)
	curZone := finiteOr(input.ObservedZoneTempC, plant.ZoneSetpointC)

	horizon := buildNormalizedHorizon(input, plant)
	candidates := buildSupplyCandidateGrid(minSupply, maxSupply, curSupply, 0.5)
	if len(candidates) == 0 {
		return CoupledPlantMPCSolution{}, errors.New("coupled mpc: no candidate chilled-water temperatures")
	}
	curSupplyIdx := nearestCandidateIdx(candidates, curSupply)

	if !input.PrecoolingEnabled {
		return s.solveCoupledNoStorageDP(startedAt, input, plant, model, horizon, candidates, curSupplyIdx, curZone, zoneFloor, zoneCeiling, maxStep)
	}
	return s.solveCoupledStorageDP(startedAt, input, plant, model, horizon, candidates, curSupplyIdx, curZone, zoneFloor, zoneCeiling, maxStep)
}

func (s *CoupledPlantMPCSolver) solveCoupledNoStorageDP(
	startedAt time.Time,
	input CoupledPlantMPCInput,
	plant PlantPhysicsSpec,
	model MonotonicChillerModel,
	horizon []CoupledHorizonSlot,
	candidates []float64,
	curSupplyIdx int,
	curZone, zoneFloor, zoneCeiling, maxStep float64,
) (CoupledPlantMPCSolution, error) {
	H := len(horizon)
	S := len(candidates)

	table := make([][]CoupledOperatingPoint, H)
	projZone := make([][]float64, H)
	feasible := make([][]bool, H)
	zoneDeltaPerC := finitePositiveOr(input.ZoneTempDeltaPerSupplyC, 0.32)

	for t := 0; t < H; t++ {
		table[t] = make([]CoupledOperatingPoint, S)
		projZone[t] = make([]float64, S)
		feasible[t] = make([]bool, S)
		for si, sp := range candidates {
			// Predict zone drift with coil limit sensitivity.
			zEst := curZone + zoneDeltaPerC*(sp-candidates[curSupplyIdx])
			pt := EvaluateOptimalOperatingPoint(horizon[t].CoolingLoadKW, zEst, sp, horizon[t].AmbientDryBulbC, horizon[t].AmbientWetBulbC, horizon[t].RelHumidityPct, model, plant)
			if pt.DeliveredLoadKW < horizon[t].CoolingLoadKW*0.97 {
				// Unmet coil capacity adds thermal drift proportionally over step.
				unmetKW := horizon[t].CoolingLoadKW - pt.DeliveredLoadKW
				zEst += unmetKW * horizon[t].DurationHours / plant.ZoneThermalMassKWhPerC
			}
			table[t][si] = pt
			projZone[t][si] = zEst
			feasible[t][si] = pt.Feasible && zEst >= zoneFloor && zEst <= zoneCeiling
		}
		// Ensure at least one step-reachable option stays valid under emergencies by softening upward bound.
		anyReachable := false
		if t == 0 {
			for si, sp := range candidates {
				if math.Abs(sp-candidates[curSupplyIdx]) <= maxStep+1e-9 && feasible[t][si] {
					anyReachable = true
					break
				}
			}
			if !anyReachable {
				fallbackIdx := nearestCandidateIdx(candidates, math.Max(coupledHardMinSupplyTempC, candidates[curSupplyIdx]-maxStep))
				feasible[t][fallbackIdx] = true
			}
		}
	}

	if len(input.Horizon) == 0 && finite(input.DailyEnergyDeltaPerSupply) {
		bestStepIdx := curSupplyIdx
		bestDailyCost := math.Inf(1)
		costPerKWh := 0.15
		if input.DailyBaselineEnergyKWh > 0 && input.DailyBaselineCostCNY >= 0 {
			costPerKWh = input.DailyBaselineCostCNY / input.DailyBaselineEnergyKWh
		}
		for si, sp := range candidates {
			if math.Abs(sp-candidates[curSupplyIdx]) > maxStep+1e-9 || !feasible[0][si] {
				continue
			}
			dailyE := math.Max(0, input.DailyBaselineEnergyKWh+input.DailyEnergyDeltaPerSupply*(sp-input.CurrentSupplyTempC))
			dailyC := dailyE * costPerKWh
			if dailyC < bestDailyCost {
				bestDailyCost = dailyC
				bestStepIdx = si
			}
		}
		slots := make([]CoupledPlannedSlot, H)
		pt := table[0][bestStepIdx]
		for t := 0; t < H; t++ {
			slots[t] = CoupledPlannedSlot{
				StartAt:            horizon[t].StartAt,
				DurationHours:      horizon[t].DurationHours,
				SupplyTempC:        candidates[bestStepIdx],
				CHWPFreqHz:         pt.CHWPFreqHz,
				CWPFreqHz:          pt.CWPFreqHz,
				TowerFanPct:        pt.TowerFanPct,
				TargetChillers:     pt.TargetChillers,
				ProjectedZoneTempC: round4(projZone[0][bestStepIdx]),
				PredictedPowerKW:   round4(pt.TotalPowerKW),
				PredictedCOP:       round4(pt.PredictedCOP),
				SlotCostCNY:        round4(pt.TotalPowerKW * horizon[t].DurationHours * horizon[t].TariffRateCNY),
				OperatingMode:      "ECO",
			}
		}
		return buildMPCSolutionFromSlots(startedAt, input, slots, pt.TotalPowerKW*24.0, pt.TotalPowerKW*24.0*costPerKWh, model.COPScale, zoneFloor, zoneCeiling), nil
	}

	// Dynamic program over (t, sIdx): min sum_t (power_t * dt * tariff_t + switchPenalty * dS^2).
	const infCost = 1e18
	const switchWeightCNYPerC2 = 1.4
	dp := make([][]float64, H)
	parent := make([][]int, H)
	for t := 0; t < H; t++ {
		dp[t] = make([]float64, S)
		parent[t] = make([]int, S)
		for si := 0; si < S; si++ {
			dp[t][si] = infCost
			parent[t][si] = -1
		}
	}

	for si := 0; si < S; si++ {
		sp := candidates[si]
		dS := sp - candidates[curSupplyIdx]
		if math.Abs(dS) > maxStep+1e-9 || !feasible[0][si] {
			continue
		}
		stageCost := table[0][si].TotalPowerKW*horizon[0].DurationHours*horizon[0].TariffRateCNY +
			switchWeightCNYPerC2*dS*dS
		dp[0][si] = stageCost
		parent[0][si] = curSupplyIdx
	}

	for t := 1; t < H; t++ {
		for prev := 0; prev < S; prev++ {
			if dp[t-1][prev] >= infCost*0.5 {
				continue
			}
			for si := 0; si < S; si++ {
				if !feasible[t][si] {
					continue
				}
				dS := candidates[si] - candidates[prev]
				if math.Abs(dS) > maxStep+1e-9 {
					continue
				}
				candCost := dp[t-1][prev] +
					table[t][si].TotalPowerKW*horizon[t].DurationHours*horizon[t].TariffRateCNY +
					switchWeightCNYPerC2*dS*dS
				if candCost < dp[t][si] {
					dp[t][si] = candCost
					parent[t][si] = prev
				}
			}
		}
	}

	bestEnd := -1
	bestTotal := infCost
	for si := 0; si < S; si++ {
		if dp[H-1][si] < bestTotal {
			bestTotal = dp[H-1][si]
			bestEnd = si
		}
	}
	if bestEnd < 0 {
		// Recovery fallback if horizon constraints infeasible under extreme initial zone anomaly.
		bestEnd = nearestCandidateIdx(candidates, math.Max(coupledHardMinSupplyTempC, candidates[curSupplyIdx]-maxStep))
	}

	chosenIdx := make([]int, H)
	cur := bestEnd
	for t := H - 1; t >= 0; t-- {
		if cur < 0 || cur >= S {
			cur = curSupplyIdx
		}
		chosenIdx[t] = cur
		cur = parent[t][cur]
	}

	slots := make([]CoupledPlannedSlot, H)
	totalE := 0.0
	totalC := 0.0
	for t := 0; t < H; t++ {
		si := chosenIdx[t]
		pt := table[t][si]
		slotE := pt.TotalPowerKW * horizon[t].DurationHours
		slotC := slotE * horizon[t].TariffRateCNY
		totalE += slotE
		totalC += slotC
		mode := "ECO"
		if horizon[t].TariffRateCNY >= 1.0 {
			mode = "PEAK_SHAVE"
		} else if candidates[si] <= plant.DesignSupplyTempC-0.5 {
			mode = "NORMAL"
		}
		slots[t] = CoupledPlannedSlot{
			StartAt:            horizon[t].StartAt,
			DurationHours:      horizon[t].DurationHours,
			SupplyTempC:        candidates[si],
			CHWPFreqHz:         pt.CHWPFreqHz,
			CWPFreqHz:          pt.CWPFreqHz,
			TowerFanPct:        pt.TowerFanPct,
			TargetChillers:     pt.TargetChillers,
			ProjectedZoneTempC: round4(projZone[t][si]),
			PredictedPowerKW:   round4(pt.TotalPowerKW),
			PredictedCOP:       round4(pt.PredictedCOP),
			SlotCostCNY:        round4(slotC),
			OperatingMode:      mode,
		}
	}

	return buildMPCSolutionFromSlots(startedAt, input, slots, totalE, totalC, model.COPScale, zoneFloor, zoneCeiling), nil
}

func (s *CoupledPlantMPCSolver) solveCoupledStorageDP(
	startedAt time.Time,
	input CoupledPlantMPCInput,
	plant PlantPhysicsSpec,
	model MonotonicChillerModel,
	horizon []CoupledHorizonSlot,
	candidates []float64,
	curSupplyIdx int,
	curZone, zoneFloor, zoneCeiling, maxStep float64,
) (CoupledPlantMPCSolution, error) {
	H := len(horizon)
	S := len(candidates)

	// Discretize zone temperature with 0.15C spacing inside comfort window (with safety margin)
	// and use continuous linear value-function interpolation to avoid grid-snapping quantization bias.
	effZoneMin := clampFloat(zoneFloor+0.2, input.ZoneTempMinC+0.1, plant.ZoneSetpointC)
	effZoneMax := clampFloat(zoneCeiling-0.15, effZoneMin+0.4, input.ZoneTempMaxC-0.05)
	if !finite(effZoneMin) || !finite(effZoneMax) || effZoneMax <= effZoneMin {
		effZoneMin = clampFloat(plant.ZoneSetpointC-1.5, 21.0, 24.0)
		effZoneMax = clampFloat(plant.ZoneSetpointC+0.4, 22.0, 24.6)
	}
	const targetZStep = 0.15
	nZones := int(math.Round((effZoneMax-effZoneMin)/targetZStep)) + 1
	if nZones < 5 {
		nZones = 5
	}
	zoneGrid := make([]float64, nZones)
	zSpacing := (effZoneMax - effZoneMin) / float64(nZones-1)
	for zi := 0; zi < nZones; zi++ {
		zoneGrid[zi] = effZoneMin + float64(zi)*zSpacing
	}

	minFloorC := math.Max(input.ZoneTempMinC+0.8, 22.0)
	baseTable := make([][]CoupledOperatingPoint, H)
	chargeTable := make([][]CoupledOperatingPoint, H)
	dischargeTable := make([][]CoupledOperatingPoint, H)

	totalCap := plant.totalCoolingCapacityKW()
	for t := 0; t < H; t++ {
		baseTable[t] = make([]CoupledOperatingPoint, S)
		chargeTable[t] = make([]CoupledOperatingPoint, S)
		dischargeTable[t] = make([]CoupledOperatingPoint, S)
		baseLoad := horizon[t].CoolingLoadKW
		for si, sp := range candidates {
			baseTable[t][si] = EvaluateOptimalOperatingPoint(baseLoad, plant.ZoneSetpointC, sp, horizon[t].AmbientDryBulbC, horizon[t].AmbientWetBulbC, horizon[t].RelHumidityPct, model, plant)
			if sp < plant.DesignSupplyTempC-0.15 {
				chargeLoad := math.Min(totalCap*0.95, baseLoad+math.Min(totalCap*0.35, 175.0))
				chargeTable[t][si] = EvaluateOptimalOperatingPoint(chargeLoad, plant.ZoneSetpointC-0.7, sp, horizon[t].AmbientDryBulbC, horizon[t].AmbientWetBulbC, horizon[t].RelHumidityPct, model, plant)
			} else {
				chargeTable[t][si] = baseTable[t][si]
			}
			if sp >= plant.DesignSupplyTempC-0.05 {
				dischargeLoad := math.Max(120.0, baseLoad*0.72)
				dischargeTable[t][si] = EvaluateOptimalOperatingPoint(dischargeLoad, plant.ZoneSetpointC-0.8, sp, horizon[t].AmbientDryBulbC, horizon[t].AmbientWetBulbC, horizon[t].RelHumidityPct, model, plant)
			} else {
				dischargeTable[t][si] = baseTable[t][si]
			}
		}
	}

	minTariff := horizon[0].TariffRateCNY
	for t := 1; t < H; t++ {
		if horizon[t].TariffRateCNY < minTariff {
			minTariff = horizon[t].TariffRateCNY
		}
	}

	simulateTransition := func(t int, zFrom, sp float64, baseLoad float64, si int) (nextZ float64, op CoupledOperatingPoint, ok bool) {
		dtH := horizon[t].DurationHours
		if dtH <= 0 {
			dtH = 1.0
		}
		subcool := math.Max(0.0, plant.DesignSupplyTempC-sp)
		effTarget := clampFloat(plant.ZoneSetpointC-0.9*subcool, minFloorC, plant.ZoneSetpointC)
		correctionKW := 0.0
		switch {
		case zFrom > plant.ZoneSetpointC+0.3:
			correctionKW = (zFrom - plant.ZoneSetpointC - 0.3) * plant.ZoneThermalMassKWhPerC / 0.25
		case subcool > 0.15 && zFrom > effTarget+0.1:
			maxPull := math.Max(totalCap*0.35, 250.0)
			correctionKW = math.Min((zFrom-effTarget)*plant.ZoneThermalMassKWhPerC/1.8, maxPull)
		case sp >= plant.DesignSupplyTempC-0.05 && zFrom < plant.ZoneSetpointC-0.15:
			frac := clampFloat((plant.ZoneSetpointC-0.15-zFrom)/2.0, 0, 0.45)
			correctionKW = -baseLoad * frac
		}
		desiredDemandKW := math.Max(0.0, baseLoad+correctionKW)

		coilCap := CoilCapacityLimitKW(plant, zFrom, sp, plant.CHWPMaxHz)
		if desiredDemandKW > baseLoad*1.08 && subcool > 0.15 {
			op = chargeTable[t][si]
		} else if desiredDemandKW < baseLoad*0.88 {
			op = dischargeTable[t][si]
		} else {
			op = baseTable[t][si]
		}
		if desiredDemandKW > coilCap*1.01 && sp > plant.DesignSupplyTempC+0.5 {
			return zFrom, op, false
		}
		deliveredKW := math.Min(desiredDemandKW, math.Min(coilCap, totalCap))
		if op.DeliveredLoadKW > 1 {
			ratio := clampFloat(deliveredKW/op.DeliveredLoadKW, 0.35, 1.45)
			chillerScale := 0.12 + 0.88*math.Pow(ratio, 1.15)
			auxScale := clampFloat(0.65+0.35*ratio, 0.75, 1.30)
			op.ChillerPowerKW *= chillerScale
			op.CWPPowerKW *= auxScale
			op.TowerPowerKW *= auxScale
		}
		op.DeliveredLoadKW = deliveredKW
		op.TotalPowerKW = op.ChillerPowerKW + op.CHWPPowerKW + op.CWPPowerKW + op.TowerPowerKW
		standingLossKW := math.Max(0.0, plant.ZoneSetpointC-zFrom) * 14.0
		nextZ = zFrom + (baseLoad+standingLossKW-deliveredKW)*dtH/plant.ZoneThermalMassKWhPerC
		if nextZ > effZoneMax+0.05 || nextZ < effZoneMin-0.05 {
			return nextZ, op, false
		}
		return clampFloat(nextZ, effZoneMin, effZoneMax), op, true
	}

	const infCost = 1e18
	const switchPenaltyPerC2 = 1.8

	// valFunc[t][prevS][zi] = minimum future cost from step t..H-1 at zoneGrid[zi] after previous supply index prevS.
	valFunc := make([][][]float64, H+1)
	for t := 0; t <= H; t++ {
		valFunc[t] = make([][]float64, S)
		for si := 0; si < S; si++ {
			valFunc[t][si] = make([]float64, nZones)
		}
	}
	// Terminal value at t = H: mild credit for comfort cold stored below ZoneSetpointC at valley cost.
	shadowCNYPerC := (plant.ZoneThermalMassKWhPerC / math.Max(1.0, model.BaseCOP)) * minTariff * 0.25
	for si := 0; si < S; si++ {
		for zi := 0; zi < nZones; zi++ {
			storedCoolC := math.Max(0.0, plant.ZoneSetpointC-zoneGrid[zi])
			valFunc[H][si][zi] = -shadowCNYPerC * storedCoolC
		}
	}

	interpValue := func(slice []float64, z float64) float64 {
		zClamped := clampFloat(z, effZoneMin, effZoneMax)
		pos := (zClamped - effZoneMin) / zSpacing
		lo := int(math.Floor(pos))
		if lo < 0 {
			lo = 0
		}
		if lo >= nZones-1 {
			return slice[nZones-1]
		}
		w := pos - float64(lo)
		vLo := slice[lo]
		vHi := slice[lo+1]
		if vLo >= infCost*0.5 && vHi >= infCost*0.5 {
			return infCost
		}
		if vLo >= infCost*0.5 {
			return vHi + 1e4*(1.0-w)
		}
		if vHi >= infCost*0.5 {
			return vLo + 1e4*w
		}
		return (1.0-w)*vLo + w*vHi
	}

	// Backward Bellman pass over t = H-1 down to 0.
	baseQ := make([]float64, S)
	for t := H - 1; t >= 0; t-- {
		dtH := horizon[t].DurationHours
		if dtH <= 0 {
			dtH = 1.0
		}
		tariff := horizon[t].TariffRateCNY
		baseLoad := horizon[t].CoolingLoadKW
		for zi := 0; zi < nZones; zi++ {
			zFrom := zoneGrid[zi]
			// Compute action cost + future value once per (t, zi, si) independent of prevS.
			for si := 0; si < S; si++ {
				nextZ, op, ok := simulateTransition(t, zFrom, candidates[si], baseLoad, si)
				if !ok {
					baseQ[si] = infCost
					continue
				}
				future := interpValue(valFunc[t+1][si], nextZ)
				if future >= infCost*0.5 {
					baseQ[si] = infCost
					continue
				}
				baseQ[si] = op.TotalPowerKW*dtH*tariff + future
			}
			for prevS := 0; prevS < S; prevS++ {
				bestQ := infCost
				prevCand := candidates[prevS]
				for si := 0; si < S; si++ {
					if baseQ[si] >= infCost*0.5 {
						continue
					}
					dS := candidates[si] - prevCand
					if math.Abs(dS) > maxStep+1e-9 {
						continue
					}
					q := baseQ[si] + switchPenaltyPerC2*dS*dS
					if q < bestQ {
						bestQ = q
					}
				}
				valFunc[t][prevS][zi] = bestQ
			}
		}
	}

	// Verify continuous rollout starting from curZone, curSupplyIdx finds a feasible trajectory.
	simZ := clampFloat(curZone, effZoneMin, effZoneMax)
	curS := curSupplyIdx
	if interpValue(valFunc[0][curS], simZ) >= infCost*0.5 {
		return s.solveCoupledNoStorageDP(startedAt, input, plant, model, horizon, candidates, curSupplyIdx, curZone, zoneFloor, zoneCeiling, maxStep)
	}

	slots := make([]CoupledPlannedSlot, H)
	totalE := 0.0
	totalC := 0.0
	for t := 0; t < H; t++ {
		dtH := horizon[t].DurationHours
		if dtH <= 0 {
			dtH = 1.0
		}
		tariff := horizon[t].TariffRateCNY
		baseLoad := horizon[t].CoolingLoadKW

		bestS := -1
		bestQ := infCost
		bestNextZ := simZ
		var bestOp CoupledOperatingPoint
		for si := 0; si < S; si++ {
			dS := candidates[si] - candidates[curS]
			if math.Abs(dS) > maxStep+1e-9 {
				continue
			}
			nextZ, op, ok := simulateTransition(t, simZ, candidates[si], baseLoad, si)
			if !ok {
				continue
			}
			future := interpValue(valFunc[t+1][si], nextZ)
			q := op.TotalPowerKW*dtH*tariff + switchPenaltyPerC2*dS*dS + future
			if q < bestQ {
				bestQ = q
				bestS = si
				bestNextZ = nextZ
				bestOp = op
			}
		}
		if bestS < 0 {
			bestS = curS
			bestOp = baseTable[t][bestS]
			bestNextZ = simZ
		}

		sp := candidates[bestS]
		simZ = bestNextZ
		curS = bestS
		slotE := bestOp.TotalPowerKW * dtH
		slotC := slotE * tariff
		totalE += slotE
		totalC += slotC
		mode := "NORMAL"
		if sp <= plant.DesignSupplyTempC-0.4 && tariff <= minTariff+0.1 {
			mode = "PRE_COOL"
		} else if tariff >= 1.0 && sp >= plant.DesignSupplyTempC {
			mode = "PEAK_SHAVE"
		} else if sp > plant.DesignSupplyTempC+0.3 {
			mode = "ECO"
		}
		slots[t] = CoupledPlannedSlot{
			StartAt:            horizon[t].StartAt,
			DurationHours:      dtH,
			SupplyTempC:        sp,
			CHWPFreqHz:         bestOp.CHWPFreqHz,
			CWPFreqHz:          bestOp.CWPFreqHz,
			TowerFanPct:        bestOp.TowerFanPct,
			TargetChillers:     bestOp.TargetChillers,
			ProjectedZoneTempC: round4(simZ),
			PredictedPowerKW:   round4(bestOp.TotalPowerKW),
			PredictedCOP:       round4(bestOp.PredictedCOP),
			SlotCostCNY:        round4(slotC),
			OperatingMode:      mode,
		}
	}

	return buildMPCSolutionFromSlots(startedAt, input, slots, totalE, totalC, model.COPScale, zoneFloor, zoneCeiling), nil
}

func buildMPCSolutionFromSlots(
	startedAt time.Time,
	input CoupledPlantMPCInput,
	slots []CoupledPlannedSlot,
	totalE, totalC, copScale, zoneFloor, zoneCeiling float64,
) CoupledPlantMPCSolution {
	first := slots[0]
	totalHours := 0.0
	for _, sl := range slots {
		totalHours += sl.DurationHours
	}
	dailyE := totalE
	dailyC := totalC
	if totalHours > 0 && math.Abs(totalHours-24.0) > 1e-3 {
		scale := 24.0 / totalHours
		dailyE *= scale
		dailyC *= scale
	}

	// Ground daily expectation against Request.Baseline when present for single-step service parity.
	savingE := 0.0
	savingC := 0.0
	if input.DailyBaselineEnergyKWh > 0 {
		if finite(input.DailyEnergyDeltaPerSupply) && len(input.Horizon) == 0 {
			deltaS := first.SupplyTempC - input.CurrentSupplyTempC
			dailyE = math.Max(0, input.DailyBaselineEnergyKWh+input.DailyEnergyDeltaPerSupply*deltaS)
			costPerKWh := 0.15
			if input.DailyBaselineEnergyKWh > 0 && input.DailyBaselineCostCNY >= 0 {
				costPerKWh = input.DailyBaselineCostCNY / input.DailyBaselineEnergyKWh
			}
			dailyC = dailyE * costPerKWh
		}
		savingE = input.DailyBaselineEnergyKWh - dailyE
		savingC = input.DailyBaselineCostCNY - dailyC
	}

	marginUpper := zoneCeiling - first.ProjectedZoneTempC
	marginLower := first.ProjectedZoneTempC - zoneFloor
	comfortMargin := math.Min(marginUpper, marginLower)

	return CoupledPlantMPCSolution{
		SupplyTempC:           round4(first.SupplyTempC),
		ProjectedZoneTempC:    round4(first.ProjectedZoneTempC),
		CHWPFreqHz:            round4(first.CHWPFreqHz),
		CWPFreqHz:             round4(first.CWPFreqHz),
		TowerFanPct:           round4(first.TowerFanPct),
		TargetChillers:        first.TargetChillers,
		HorizonEnergyKWh:      round4(totalE),
		HorizonCostCNY:        round4(totalC),
		DailyEnergyKWh:        round4(dailyE),
		DailyCostCNY:          round4(dailyC),
		EnergySavingKWhPerDay: round4(savingE),
		CostSavingPerDay:      round4(savingC),
		ComfortMarginC:        round4(comfortMargin),
		PredictedCOP:          round4(first.PredictedCOP),
		CalibratedCOPScale:    round4(copScale),
		Slots:                 slots,
		SolveDuration:         time.Since(startedAt),
	}
}

func buildNormalizedHorizon(input CoupledPlantMPCInput, plant PlantPhysicsSpec) []CoupledHorizonSlot {
	if len(input.Horizon) > 0 {
		out := make([]CoupledHorizonSlot, len(input.Horizon))
		for i, sl := range input.Horizon {
			dt := sl.DurationHours
			if !finitePositive(dt) {
				dt = 1.0
			}
			db := finiteOr(sl.AmbientDryBulbC, 30.0)
			wb := sl.AmbientWetBulbC
			if !finite(wb) || wb > db {
				wb = EstimateWetBulbC(db, sl.RelHumidityPct)
			}
			rate := sl.TariffRateCNY
			if !finitePositive(rate) {
				rate = finitePositiveOr(input.TariffRateCNY, 0.75)
			}
			out[i] = CoupledHorizonSlot{
				StartAt:         sl.StartAt,
				DurationHours:   dt,
				CoolingLoadKW:   math.Max(60.0, finitePositiveOr(sl.CoolingLoadKW, 420.0)),
				AmbientDryBulbC: db,
				AmbientWetBulbC: wb,
				RelHumidityPct:  sl.RelHumidityPct,
				TariffRateCNY:   rate,
			}
		}
		return out
	}

	// Single-step Request mode without explicit time-series horizon: synthesize consistent 24h steady horizon.
	loadKW := input.CurrentLoadKW
	if !finitePositive(loadKW) {
		if finitePositive(input.DailyBaselineEnergyKWh) {
			loadKW = clampFloat(input.DailyBaselineEnergyKWh*4.5/24.0, 140.0, plant.totalCoolingCapacityKW()*0.90)
		} else {
			loadKW = 420.0
		}
	}
	db := finiteOr(input.AmbientDryBulbC, 30.0)
	wb := input.AmbientWetBulbC
	if !finite(wb) || wb > db {
		wb = EstimateWetBulbC(db, input.RelHumidityPct)
	}
	rate := finitePositiveOr(input.TariffRateCNY, 0.75)
	if rate == 0.75 && finitePositive(input.DailyBaselineEnergyKWh) && finitePositive(input.DailyBaselineCostCNY) {
		rate = input.DailyBaselineCostCNY / input.DailyBaselineEnergyKWh
	}
	slots := make([]CoupledHorizonSlot, 24)
	for h := 0; h < 24; h++ {
		slots[h] = CoupledHorizonSlot{
			DurationHours:   1.0,
			CoolingLoadKW:   loadKW,
			AmbientDryBulbC: db,
			AmbientWetBulbC: wb,
			RelHumidityPct:  input.RelHumidityPct,
			TariffRateCNY:   rate,
		}
	}
	return slots
}

func buildSupplyCandidateGrid(minC, maxC, anchorC, stepC float64) []float64 {
	grid := make([]float64, 0, 24)
	for v := minC; v <= maxC+1e-9; v += stepC {
		val := round4(math.Min(v, maxC))
		if len(grid) == 0 || math.Abs(val-grid[len(grid)-1]) > 1e-4 {
			grid = append(grid, val)
		}
	}
	// Ensure exact anchorC is present cleanly in sorted order.
	anchored := false
	for _, g := range grid {
		if math.Abs(g-anchorC) < 1e-4 {
			anchored = true
			break
		}
	}
	if !anchored && anchorC >= minC-1e-9 && anchorC <= maxC+1e-9 {
		inserted := false
		withAnchor := make([]float64, 0, len(grid)+1)
		for _, g := range grid {
			if !inserted && anchorC < g {
				withAnchor = append(withAnchor, round4(anchorC))
				inserted = true
			}
			withAnchor = append(withAnchor, g)
		}
		if !inserted {
			withAnchor = append(withAnchor, round4(anchorC))
		}
		grid = withAnchor
	}
	return grid
}

func nearestCandidateIdx(candidates []float64, target float64) int {
	bestIdx := 0
	bestDist := math.Abs(candidates[0] - target)
	for i := 1; i < len(candidates); i++ {
		if d := math.Abs(candidates[i] - target); d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}
	return bestIdx
}

// Recommend implements SolverAdapter for production optimization-service dispatch.
func (s *CoupledPlantMPCSolver) Recommend(_ context.Context, request Request, at time.Time) (Recommendation, error) {
	if err := request.Validate(); err != nil {
		return Recommendation{}, err
	}

	sol, err := s.SolveCoupledPlant(CoupledPlantMPCInput{
		CurrentSupplyTempC:        request.Baseline.SupplyTempC,
		ObservedZoneTempC:         request.Baseline.ZoneTempC,
		ZoneTempUncertaintyP90C:   request.ResponseModel.ZoneTempUncertaintyP90C,
		EnergyUncertaintyP90KWh:   request.ResponseModel.EnergyUncertaintyP90KWh,
		SupplyTempMinC:            request.Constraints.SupplyTempMinC,
		SupplyTempMaxC:            request.Constraints.SupplyTempMaxC,
		ZoneTempMinC:              request.Constraints.ZoneTempMinC,
		ZoneTempMaxC:              request.Constraints.ZoneTempMaxC,
		MaxSupplyTempStepC:        request.Constraints.MaxSupplyTempStep,
		DailyBaselineEnergyKWh:    request.Baseline.DailyEnergyKWh,
		DailyBaselineCostCNY:      request.Baseline.DailyCost,
		ZoneTempDeltaPerSupplyC:   request.ResponseModel.ZoneTempDeltaPerSupplyTempC,
		DailyEnergyDeltaPerSupply: request.ResponseModel.DailyEnergyDeltaPerSupplyTempC,
		PrecoolingEnabled:         false,
		Plant:                     DefaultPlantPhysicsSpec(),
	})
	if err != nil {
		return Recommendation{}, err
	}

	riskLevel := "LOW"
	if sol.ComfortMarginC <= math.Max(0.5, 2*request.ResponseModel.ZoneTempUncertaintyP90C) {
		riskLevel = "MEDIUM"
	}
	savingWithinUncertainty := math.Abs(sol.EnergySavingKWhPerDay) <= request.ResponseModel.EnergyUncertaintyP90KWh
	extrapolated := math.Abs(sol.SupplyTempC-request.Baseline.SupplyTempC) > request.Constraints.MaxSupplyTempStep*0.5

	auxTargets := []intelligencemodel.OptimizationAuxiliaryTarget{
		{
			LoopKey:     LoopKeyChilledWaterPump,
			Capability:  string(commandmodel.CapabilitySetFrequency),
			TargetValue: sol.CHWPFreqHz,
			Unit:        "Hz",
			Rationale:   fmt.Sprintf("coupled grey-box MPC: CHWP head/flow optimum for %d chillers", sol.TargetChillers),
		},
		{
			LoopKey:     LoopKeyCoolingWaterPump,
			Capability:  string(commandmodel.CapabilitySetFrequency),
			TargetValue: sol.CWPFreqHz,
			Unit:        "Hz",
			Rationale:   fmt.Sprintf("coupled grey-box MPC: condenser delta-T vs pump affinity optimum (COP %.2f)", sol.PredictedCOP),
		},
		{
			LoopKey:     LoopKeyCoolingTowerFan,
			Capability:  string(commandmodel.CapabilitySetFanSpeed),
			TargetValue: sol.TowerFanPct,
			Unit:        "%",
			Rationale:   "coupled grey-box MPC: nonlinear wet-bulb approach vs cubic fan power optimum",
		},
	}

	rec := Recommendation{
		ID:                 deterministicV7(request.OptimizationRunID, "recommendation:1", request.ValidFrom.UTC()),
		TenantID:           request.TenantID,
		SiteID:             request.SiteID,
		InputSnapshotID:    request.InputSnapshotID,
		DeploymentRevision: request.DeploymentRevisionID,
		Baseline: intelligencemodel.RecommendationBaseline{
			DailyEnergyKWh: request.Baseline.DailyEnergyKWh,
			DailyCost:      request.Baseline.DailyCost,
			SupplyTempC:    request.Baseline.SupplyTempC,
			ZoneTempC:      request.Baseline.ZoneTempC,
		},
		Objective: intelligencemodel.RecommendationObjective{
			Kind:           request.Objective,
			Horizon:        request.Horizon,
			HorizonMinutes: request.HorizonMinutes,
		},
		Constraints: frozenRecommendationConstraints(request.Constraints),
		Candidate: intelligencemodel.RecommendationCandidate{
			SupplyTempC:        sol.SupplyTempC,
			ProjectedZoneTempC: sol.ProjectedZoneTempC,
			DailyEnergyKWh:     sol.DailyEnergyKWh,
			DailyCost:          sol.DailyCostCNY,
		},
		AuxiliaryTargets: auxTargets,
		ExpectedImpact: intelligencemodel.RecommendationExpectedImpact{
			EnergySavingKWhPerDay: sol.EnergySavingKWhPerDay,
			CostSavingPerDay:      sol.CostSavingPerDay,
		},
		Uncertainty: intelligencemodel.RecommendationUncertainty{
			EnergyP90KWh: request.ResponseModel.EnergyUncertaintyP90KWh,
			ZoneTempP90C: request.ResponseModel.ZoneTempUncertaintyP90C,
		},
		Risk: intelligencemodel.RecommendationRisk{
			Level:                         riskLevel,
			ComfortMarginC:                sol.ComfortMarginC,
			Extrapolated:                  boolPtr(extrapolated),
			SavingWithinEnergyUncertainty: boolPtr(savingWithinUncertainty),
		},
		RollbackPlan:     standardRollbackPlan(request.Baseline.SupplyTempC),
		VerificationPlan: standardVerificationPlan(),
		Approval:         intelligencemodel.RecommendationDraft,
		ValidUntil:       timePointer(at.UTC().Add(intelligencemodel.DefaultRecommendationTTL)),
		CreatedAt:        at.UTC(),
	}
	if err := rec.ValidateForApproval(); err != nil {
		return Recommendation{}, err
	}
	return rec, nil
}

func clampFloat(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func finiteOr(v, fallback float64) float64 {
	if !finite(v) {
		return fallback
	}
	return v
}

func finitePositiveOr(v, fallback float64) float64 {
	if !finitePositive(v) {
		return fallback
	}
	return v
}

func round4(v float64) float64 {
	return math.Round(v*10000.0) / 10000.0
}

// CoupledContextPayload mirrors Python ai-solver-service's optional coupledContext /v1/solve JSON shape.
type CoupledContextPayload struct {
	PrecoolingEnabled bool                 `json:"precoolingEnabled"`
	COPScale          *float64             `json:"copScale,omitempty"`
	CurrentLoadKW     *float64             `json:"currentLoadKw,omitempty"`
	AmbientDryBulbC   *float64             `json:"ambientDryBulbC,omitempty"`
	AmbientWetBulbC   *float64             `json:"ambientWetBulbC,omitempty"`
	RelHumidityPct    *float64             `json:"relHumidityPct,omitempty"`
	TariffRateCNY     *float64             `json:"tariffRateCny,omitempty"`
	Plant             *PlantPhysicsSpec    `json:"plant,omitempty"`
	Horizon           []CoupledHorizonSlot `json:"horizon,omitempty"`
}

// CoupledSolverHTTPRequest decodes /v1/solve payloads with optional coupled plant context.
type CoupledSolverHTTPRequest struct {
	Request
	CoupledContext *CoupledContextPayload `json:"coupledContext,omitempty"`
}

type coupledHTTPVerificationPlan struct {
	WindowMinutes         int                  `json:"windowMinutes"`
	RequiredSignals       []string             `json:"requiredSignals"`
	SuccessCriteria       []string             `json:"successCriteria"`
	TargetRunningChillers int                  `json:"targetRunningChillers,omitempty"`
	PredictedCOP          float64              `json:"predictedCop,omitempty"`
	MPCSlots              []CoupledPlannedSlot `json:"mpcSlots,omitempty"`
}

type coupledSolverHTTPResponse struct {
	ID               string                                         `json:"id"`
	Candidate        intelligencemodel.RecommendationCandidate      `json:"candidate"`
	ExpectedImpact   intelligencemodel.RecommendationExpectedImpact `json:"expectedImpact"`
	Uncertainty      intelligencemodel.RecommendationUncertainty    `json:"uncertainty"`
	Risk             intelligencemodel.RecommendationRisk           `json:"risk"`
	RollbackPlan     intelligencemodel.RecommendationRollbackPlan   `json:"rollbackPlan"`
	VerificationPlan coupledHTTPVerificationPlan                    `json:"verificationPlan"`
	SolverVersion    string                                         `json:"solverVersion"`
	AuxiliaryTargets []RemoteAuxiliaryTarget                        `json:"auxiliaryTargets"`
}

// NewCoupledSolverHTTPHandler serves /healthz, /readyz, and /v1/solve over loopback HTTP
// using the exact Go CoupledPlantMPCSolver and SolveResponse JSON schema.
func NewCoupledSolverHTTPHandler(solver *CoupledPlantMPCSolver) http.Handler {
	if solver == nil {
		solver = NewCoupledPlantMPCSolver()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok","service":"coupled-mpc-go"}`)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ready","service":"coupled-mpc-go","solverVersion":%q}`, CoupledMPCSolverVersion)
	})
	mux.HandleFunc("/v1/solve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer clickhousehttp.DrainAndClose(r.Body)
		body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req CoupledSolverHTTPRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := req.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.CoupledContext == nil {
			rec, err := solver.Recommend(r.Context(), req.Request, time.Now().UTC())
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			aux := make([]RemoteAuxiliaryTarget, 0, len(rec.AuxiliaryTargets))
			for _, t := range rec.AuxiliaryTargets {
				aux = append(aux, RemoteAuxiliaryTarget{
					LoopKey: t.LoopKey, Capability: t.Capability, TargetValue: t.TargetValue, Unit: t.Unit, Rationale: t.Rationale,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Solver-Version", CoupledMPCSolverVersion)
			_ = json.NewEncoder(w).Encode(coupledSolverHTTPResponse{
				ID:             rec.ID,
				Candidate:      rec.Candidate,
				ExpectedImpact: rec.ExpectedImpact,
				Uncertainty:    rec.Uncertainty,
				Risk:           rec.Risk,
				RollbackPlan:   rec.RollbackPlan,
				VerificationPlan: coupledHTTPVerificationPlan{
					WindowMinutes:   30,
					RequiredSignals: []string{"supply_temperature", "zone_temperature", "site_load"},
					SuccessCriteria: []string{"zone_temperature_within_frozen_constraint", "site_load_not_worse_than_baseline"},
				},
				SolverVersion:    CoupledMPCSolverVersion,
				AuxiliaryTargets: aux,
			})
			return
		}

		cc := req.CoupledContext
		plant := DefaultPlantPhysicsSpec()
		if cc.Plant != nil {
			plant = *cc.Plant
		}
		solver.mu.Lock()
		if cc.COPScale != nil && finitePositive(*cc.COPScale) {
			clampedScale := clampFloat(*cc.COPScale, 0.55, 1.35)
			solver.residual.COPScale = clampedScale
			solver.model.COPScale = clampedScale
		}
		solver.mu.Unlock()

		mpcIn := CoupledPlantMPCInput{
			CurrentSupplyTempC:        req.Baseline.SupplyTempC,
			ObservedZoneTempC:         req.Baseline.ZoneTempC,
			ZoneTempUncertaintyP90C:   req.ResponseModel.ZoneTempUncertaintyP90C,
			EnergyUncertaintyP90KWh:   req.ResponseModel.EnergyUncertaintyP90KWh,
			SupplyTempMinC:            req.Constraints.SupplyTempMinC,
			SupplyTempMaxC:            req.Constraints.SupplyTempMaxC,
			ZoneTempMinC:              req.Constraints.ZoneTempMinC,
			ZoneTempMaxC:              req.Constraints.ZoneTempMaxC,
			MaxSupplyTempStepC:        req.Constraints.MaxSupplyTempStep,
			DailyBaselineEnergyKWh:    req.Baseline.DailyEnergyKWh,
			DailyBaselineCostCNY:      req.Baseline.DailyCost,
			ZoneTempDeltaPerSupplyC:   req.ResponseModel.ZoneTempDeltaPerSupplyTempC,
			DailyEnergyDeltaPerSupply: req.ResponseModel.DailyEnergyDeltaPerSupplyTempC,
			PrecoolingEnabled:         cc.PrecoolingEnabled,
			Horizon:                   cc.Horizon,
			Plant:                     plant,
		}
		if cc.CurrentLoadKW != nil {
			mpcIn.CurrentLoadKW = *cc.CurrentLoadKW
		}
		if cc.AmbientDryBulbC != nil {
			mpcIn.AmbientDryBulbC = *cc.AmbientDryBulbC
		}
		if cc.AmbientWetBulbC != nil {
			mpcIn.AmbientWetBulbC = *cc.AmbientWetBulbC
		}
		if cc.RelHumidityPct != nil {
			mpcIn.RelHumidityPct = *cc.RelHumidityPct
		}
		if cc.TariffRateCNY != nil {
			mpcIn.TariffRateCNY = *cc.TariffRateCNY
		}

		sol, err := solver.SolveCoupledPlant(mpcIn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		riskLevel := "LOW"
		if sol.ComfortMarginC <= math.Max(0.5, 2*req.ResponseModel.ZoneTempUncertaintyP90C) {
			riskLevel = "MEDIUM"
		}
		recID := deterministicV7(req.OptimizationRunID, "recommendation:1", req.ValidFrom.UTC())
		aux := []RemoteAuxiliaryTarget{
			{
				LoopKey:     "chilled_water_pump_freq",
				Capability:  string(commandmodel.CapabilitySetFrequency),
				TargetValue: sol.CHWPFreqHz,
				Unit:        "Hz",
				Rationale:   fmt.Sprintf("coupled grey-box MPC: CHWP flow/coil optimum for %d chillers", sol.TargetChillers),
			},
			{
				LoopKey:     "cooling_water_pump_freq",
				Capability:  string(commandmodel.CapabilitySetFrequency),
				TargetValue: sol.CWPFreqHz,
				Unit:        "Hz",
				Rationale:   fmt.Sprintf("coupled grey-box MPC: condenser delta-T vs pump affinity (COP %.2f)", sol.PredictedCOP),
			},
			{
				LoopKey:     "cooling_tower_fan_speed",
				Capability:  string(commandmodel.CapabilitySetFanSpeed),
				TargetValue: sol.TowerFanPct,
				Unit:        "%",
				Rationale:   "coupled grey-box MPC: nonlinear wet-bulb approach vs cubic fan power optimum",
			},
		}
		resp := coupledSolverHTTPResponse{
			ID: recID,
			Candidate: intelligencemodel.RecommendationCandidate{
				SupplyTempC:        sol.SupplyTempC,
				ProjectedZoneTempC: sol.ProjectedZoneTempC,
				DailyEnergyKWh:     sol.DailyEnergyKWh,
				DailyCost:          sol.DailyCostCNY,
			},
			ExpectedImpact: intelligencemodel.RecommendationExpectedImpact{
				EnergySavingKWhPerDay: sol.EnergySavingKWhPerDay,
				CostSavingPerDay:      sol.CostSavingPerDay,
			},
			Uncertainty: intelligencemodel.RecommendationUncertainty{
				EnergyP90KWh: req.ResponseModel.EnergyUncertaintyP90KWh,
				ZoneTempP90C: req.ResponseModel.ZoneTempUncertaintyP90C,
			},
			Risk: intelligencemodel.RecommendationRisk{
				Level:          riskLevel,
				ComfortMarginC: math.Max(0, sol.ComfortMarginC),
			},
			RollbackPlan: standardRollbackPlan(req.Baseline.SupplyTempC),
			VerificationPlan: coupledHTTPVerificationPlan{
				WindowMinutes:         30,
				RequiredSignals:       []string{"supply_temperature", "zone_temperature", "site_load"},
				SuccessCriteria:       []string{"zone_temperature_within_frozen_constraint", "site_load_not_worse_than_baseline"},
				TargetRunningChillers: sol.TargetChillers,
				PredictedCOP:          sol.PredictedCOP,
				MPCSlots:              sol.Slots,
			},
			SolverVersion:    CoupledMPCSolverVersion,
			AuxiliaryTargets: aux,
		}
		if strings.TrimSpace(r.Header.Get("X-Trace-Id")) != "" {
			w.Header().Set("X-Trace-Id", strings.TrimSpace(r.Header.Get("X-Trace-Id")))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Solver-Version", CoupledMPCSolverVersion)
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}
