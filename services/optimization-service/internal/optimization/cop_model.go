package optimization

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

// ChillerCOPSolverName names the Chiller COP Model solver in its evaluation records.
const ChillerCOPSolverName = "CHILLER_COP_MODEL"

// The telemetry keys the Chiller COP Model reads. A Chiller is a device that reports both
// power_total and chw_leaving_temperature; the Plant's cooling load is instant_cooling_capacity.
const (
	chillerPowerKey             = "power_total"
	chilledWaterLeavingKey      = "chw_leaving_temperature"
	chilledWaterEnteringKey     = "chw_entering_temperature"
	condenserWaterLeavingKey    = "cw_leaving_temperature"
	plantCoolingLoadKey         = "instant_cooling_capacity"
	copHistoryDays              = 14
	copBucketMinutes            = 15
	copReferenceHours           = 24
	copMinimumSamples           = 96
	copMaximumCVRMSE            = 0.30
	copRunningPowerFraction     = 0.10
	copMinimumAllocationDeltaTK = 0.2
)

// PlantBucket is one device's 15-minute means. A field is nil when the device reported no
// usable reading of it in the bucket.
type PlantBucket struct {
	Bucket       time.Time `json:"bucket"`
	DeviceID     string    `json:"deviceId"`
	PowerKW      *float64  `json:"powerKW"`
	ChwLeavingC  *float64  `json:"chwLeavingC"`
	ChwEnteringC *float64  `json:"chwEnteringC"`
	CwLeavingC   *float64  `json:"cwLeavingC"`
	CoolingKW    *float64  `json:"coolingKW"`
}

type PlantHistoryQuery struct {
	TenantID string
	SiteID   string
	From     time.Time
	To       time.Time
}

// PlantHistory reads a Site's chilled-water Plant as 15-minute buckets.
type PlantHistory interface {
	PlantBuckets(context.Context, PlantHistoryQuery) ([]PlantBucket, error)
}

// COPSample is one steady 15-minute operating point of one Chiller.
type COPSample struct {
	Bucket      time.Time
	LoadKW      float64
	PowerKW     float64
	ChwSupplyC  float64
	CwReturnC   *float64
	Allocation  string
	LoadRatio   float64
	observedCOP float64
}

// ChillerFit is one Chiller's fitted COP model:
// COP = intercept + a·PLR + b·PLR² + c·T_chw,supply (+ d·T_cw,return when that is measured).
type ChillerFit struct {
	DeviceID          string             `json:"deviceId"`
	Status            string             `json:"status"`
	Reason            string             `json:"reason,omitempty"`
	Samples           int                `json:"samples"`
	CapacityReference *CapacityReference `json:"capacityReference,omitempty"`
	Coefficients      map[string]float64 `json:"coefficients,omitempty"`
	CoefficientSource string             `json:"coefficientSource,omitempty"`
	CVRMSEPower       *float64           `json:"cvRmsePower,omitempty"`
	CVRMSECOP         *float64           `json:"cvRmseCOP,omitempty"`
	MeanCOP           *float64           `json:"meanCOP,omitempty"`
	ChwSupplyRangeC   []float64          `json:"chilledWaterSupplyRangeC,omitempty"`
	coefficients      []float64
	withCondenser     bool
	samples           []COPSample
}

type CapacityReference struct {
	KW     float64 `json:"kW"`
	Source string  `json:"source"`
}

const (
	ChillerFitted           = "FITTED"
	ChillerInsufficientData = "INSUFFICIENT_DATA"
	ChillerRejected         = "REJECTED"
)

// HistoryEvidence says what the fit read and what it set aside.
type HistoryEvidence struct {
	From           time.Time      `json:"from"`
	To             time.Time      `json:"to"`
	BucketMinutes  int            `json:"bucketMinutes"`
	Chillers       int            `json:"chillers"`
	RunningBuckets int            `json:"runningBuckets"`
	SetAside       map[string]int `json:"setAside"`
	LoadAllocation map[string]int `json:"loadAllocation"`
}

// PlantFit is every Chiller's fit from one history window.
type PlantFit struct {
	History  HistoryEvidence `json:"history"`
	Chillers []ChillerFit    `json:"chillers"`
	// CVRMSEPower is the power CV(RMSE) over every fitted Chiller's samples together.
	CVRMSEPower *float64 `json:"cvRmsePower"`
}

func (fit PlantFit) usable() []ChillerFit {
	usable := []ChillerFit{}
	for _, chiller := range fit.Chillers {
		if chiller.Status == ChillerFitted {
			usable = append(usable, chiller)
		}
	}
	return usable
}

// FitPlant fits each Chiller's COP from the Plant's buckets. The Plant's cooling load is split
// over the running Chillers by their evaporator temperature drop (equal evaporator flow), or by
// power when a drop is too small to read.
func FitPlant(buckets []PlantBucket, from, to time.Time) PlantFit {
	byBucket := map[time.Time]map[string]PlantBucket{}
	chillerSet := map[string]bool{}
	maxPower := map[string]float64{}
	for _, bucket := range buckets {
		at := bucket.Bucket.UTC()
		if byBucket[at] == nil {
			byBucket[at] = map[string]PlantBucket{}
		}
		byBucket[at][bucket.DeviceID] = bucket
	}
	for _, bucket := range buckets {
		if bucket.PowerKW != nil && bucket.ChwLeavingC != nil {
			chillerSet[bucket.DeviceID] = true
		}
	}
	for _, bucket := range buckets {
		if chillerSet[bucket.DeviceID] && bucket.PowerKW != nil && *bucket.PowerKW > maxPower[bucket.DeviceID] {
			maxPower[bucket.DeviceID] = *bucket.PowerKW
		}
	}
	chillers := make([]string, 0, len(chillerSet))
	for id := range chillerSet {
		chillers = append(chillers, id)
	}
	sort.Strings(chillers)
	times := make([]time.Time, 0, len(byBucket))
	for at := range byBucket {
		times = append(times, at)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	evidence := HistoryEvidence{
		From: from.UTC(), To: to.UTC(), BucketMinutes: copBucketMinutes, Chillers: len(chillers),
		SetAside:       map[string]int{"noCoolingLoad": 0, "implausibleReading": 0, "startingUp": 0, "implausibleCOP": 0},
		LoadAllocation: map[string]int{"single": 0, "evaporatorDeltaT": 0, "power": 0},
	}
	running := func(group map[string]PlantBucket, id string) bool {
		bucket, ok := group[id]
		return ok && bucket.PowerKW != nil && maxPower[id] > 0 && *bucket.PowerKW > copRunningPowerFraction*maxPower[id]
	}
	samples := map[string][]COPSample{}
	for _, at := range times {
		group := byBucket[at]
		runningIDs := []string{}
		for _, id := range chillers {
			if running(group, id) {
				runningIDs = append(runningIDs, id)
			}
		}
		if len(runningIDs) == 0 {
			continue
		}
		evidence.RunningBuckets++
		load, hasLoad := 0.0, false
		for _, bucket := range group {
			if bucket.CoolingKW != nil {
				load += *bucket.CoolingKW
				hasLoad = true
			}
		}
		if !hasLoad || load <= 0 {
			evidence.SetAside["noCoolingLoad"]++
			continue
		}
		if !plausible(group, runningIDs) {
			evidence.SetAside["implausibleReading"]++
			continue
		}
		previous := byBucket[at.Add(-copBucketMinutes*time.Minute)]
		shares, method := allocate(group, runningIDs)
		evidence.LoadAllocation[method]++
		for index, id := range runningIDs {
			if previous == nil || !running(previous, id) {
				evidence.SetAside["startingUp"]++
				continue
			}
			bucket := group[id]
			sample := COPSample{Bucket: at, LoadKW: load * shares[index], PowerKW: *bucket.PowerKW, ChwSupplyC: *bucket.ChwLeavingC, CwReturnC: bucket.CwLeavingC, Allocation: method}
			sample.observedCOP = sample.LoadKW / sample.PowerKW
			if sample.observedCOP < 1 || sample.observedCOP > 12 {
				evidence.SetAside["implausibleCOP"]++
				continue
			}
			samples[id] = append(samples[id], sample)
		}
	}
	fit := PlantFit{History: evidence, Chillers: make([]ChillerFit, 0, len(chillers))}
	var residuals, observed []float64
	parameters := 0
	for _, id := range chillers {
		chiller := fitChiller(id, samples[id])
		if chiller.Status == ChillerFitted {
			parameters += len(chiller.coefficients)
			for _, sample := range chiller.samples {
				residuals = append(residuals, sample.PowerKW-sample.LoadKW/chiller.predictCOP(sample.LoadRatio, sample.ChwSupplyC, sample.CwReturnC))
				observed = append(observed, sample.PowerKW)
			}
		}
		fit.Chillers = append(fit.Chillers, chiller)
	}
	if len(residuals) > 0 {
		fit.CVRMSEPower = ptr(round(cvRMSE(residuals, observed, parameters), 4))
	}
	return fit
}

// plausible rejects buckets whose temperatures cannot be a running Chiller's: supply outside
// 2–12 ℃ or warmer than the return, condenser outside 15–45 ℃ or colder than the supply.
func plausible(group map[string]PlantBucket, runningIDs []string) bool {
	for _, id := range runningIDs {
		bucket := group[id]
		if bucket.ChwLeavingC == nil {
			return false
		}
		supply := *bucket.ChwLeavingC
		if supply < 2 || supply > 12 || (bucket.ChwEnteringC != nil && supply > *bucket.ChwEnteringC+0.5) {
			return false
		}
		if bucket.CwLeavingC != nil && (*bucket.CwLeavingC < 15 || *bucket.CwLeavingC > 45 || *bucket.CwLeavingC <= supply) {
			return false
		}
	}
	return true
}

func allocate(group map[string]PlantBucket, runningIDs []string) ([]float64, string) {
	if len(runningIDs) == 1 {
		return []float64{1}, "single"
	}
	drops := make([]float64, len(runningIDs))
	total := 0.0
	byDrop := true
	for index, id := range runningIDs {
		bucket := group[id]
		if bucket.ChwEnteringC == nil || *bucket.ChwEnteringC-*bucket.ChwLeavingC < copMinimumAllocationDeltaTK {
			byDrop = false
			break
		}
		drops[index] = *bucket.ChwEnteringC - *bucket.ChwLeavingC
		total += drops[index]
	}
	if byDrop {
		for index := range drops {
			drops[index] /= total
		}
		return drops, "evaporatorDeltaT"
	}
	total = 0
	for index, id := range runningIDs {
		drops[index] = *group[id].PowerKW
		total += drops[index]
	}
	for index := range drops {
		drops[index] /= total
	}
	return drops, "power"
}

func fitChiller(deviceID string, samples []COPSample) ChillerFit {
	chiller := ChillerFit{DeviceID: deviceID, Samples: len(samples)}
	if len(samples) < copMinimumSamples {
		chiller.Status, chiller.Reason = ChillerInsufficientData, fmt.Sprintf("fewer than %d steady 15-minute samples", copMinimumSamples)
		return chiller
	}
	// The condenser term is used when every sample measured the condenser return.
	withCondenser := true
	for _, sample := range samples {
		if sample.CwReturnC == nil {
			withCondenser = false
			break
		}
	}
	loads := make([]float64, len(samples))
	for index, sample := range samples {
		loads[index] = sample.LoadKW
	}
	reference := percentile(loads, 0.99)
	features := func(loadRatio, supply float64, condenser *float64) []float64 {
		row := []float64{1, loadRatio, loadRatio * loadRatio, supply}
		if withCondenser {
			row = append(row, *condenser)
		}
		return row
	}
	rows := make([][]float64, len(samples))
	targets := make([]float64, len(samples))
	low, high := math.Inf(1), math.Inf(-1)
	for index := range samples {
		samples[index].LoadRatio = samples[index].LoadKW / reference
		rows[index] = features(samples[index].LoadRatio, samples[index].ChwSupplyC, samples[index].CwReturnC)
		targets[index] = samples[index].observedCOP
		low, high = math.Min(low, samples[index].ChwSupplyC), math.Max(high, samples[index].ChwSupplyC)
	}
	coefficients, err := leastSquares(rows, targets)
	if err != nil {
		chiller.Status, chiller.Reason = ChillerRejected, "readings do not vary enough to fit"
		return chiller
	}
	chiller.coefficients, chiller.withCondenser, chiller.samples = coefficients, withCondenser, samples
	chiller.CapacityReference = &CapacityReference{KW: round(reference, 1), Source: "OBSERVED_P99_LOAD"}
	chiller.CoefficientSource = "FITTED_FROM_HISTORY"
	chiller.Coefficients = map[string]float64{
		"intercept": coefficients[0], "loadRatio": coefficients[1], "loadRatioSquared": coefficients[2], "chilledWaterSupplyTempC": coefficients[3],
	}
	if withCondenser {
		chiller.Coefficients["condenserReturnTempC"] = coefficients[4]
	}
	chiller.ChwSupplyRangeC = []float64{round(low, 2), round(high, 2)}
	powerResiduals, powers, copResiduals, cops := make([]float64, len(samples)), make([]float64, len(samples)), make([]float64, len(samples)), make([]float64, len(samples))
	predictable := true
	for index, sample := range samples {
		predicted := chiller.predictCOP(sample.LoadRatio, sample.ChwSupplyC, sample.CwReturnC)
		if predicted <= 0.5 {
			predictable = false
		}
		copResiduals[index], cops[index] = sample.observedCOP-predicted, sample.observedCOP
		powerResiduals[index], powers[index] = sample.PowerKW-sample.LoadKW/math.Max(predicted, 0.5), sample.PowerKW
	}
	powerCV, copCV := cvRMSE(powerResiduals, powers, len(coefficients)), cvRMSE(copResiduals, cops, len(coefficients))
	meanCOP := mean(cops)
	chiller.CVRMSEPower, chiller.CVRMSECOP, chiller.MeanCOP = ptr(round(powerCV, 4)), ptr(round(copCV, 4)), ptr(round(meanCOP, 3))
	switch {
	case !predictable:
		chiller.Status, chiller.Reason = ChillerRejected, "the fit predicts a COP at or below 0.5 for an observed operating point"
	case coefficients[3] < 0:
		chiller.Status, chiller.Reason = ChillerRejected, "the fit says a warmer chilled-water supply lowers COP"
	case withCondenser && coefficients[4] > 0:
		chiller.Status, chiller.Reason = ChillerRejected, "the fit says a warmer condenser return raises COP"
	case powerCV > copMaximumCVRMSE:
		chiller.Status, chiller.Reason = ChillerRejected, fmt.Sprintf("power CV(RMSE) %.1f%% exceeds %.0f%%", powerCV*100, copMaximumCVRMSE*100)
	default:
		chiller.Status = ChillerFitted
	}
	return chiller
}

func (chiller ChillerFit) predictCOP(loadRatio, supply float64, condenser *float64) float64 {
	value := chiller.coefficients[0] + chiller.coefficients[1]*loadRatio + chiller.coefficients[2]*loadRatio*loadRatio + chiller.coefficients[3]*supply
	if chiller.withCondenser {
		value += chiller.coefficients[4] * *condenser
	}
	return value
}

// referenceEnergy is the Chiller's predicted electricity over its samples since `since`, with
// the chilled-water supply moved by delta.
func (chiller ChillerFit) referenceEnergy(since time.Time, delta float64) (float64, int, bool) {
	energy, count, extrapolated := 0.0, 0, false
	for _, sample := range chiller.samples {
		if sample.Bucket.Before(since) {
			continue
		}
		supply := sample.ChwSupplyC + delta
		if supply < chiller.ChwSupplyRangeC[0]-0.5 || supply > chiller.ChwSupplyRangeC[1]+0.5 {
			extrapolated = true
		}
		energy += sample.LoadKW / math.Max(chiller.predictCOP(sample.LoadRatio, supply, sample.CwReturnC), 0.5) * copBucketMinutes / 60
		count++
	}
	return energy, count, extrapolated
}

// COPModelSolver is the Chiller COP Model solver: it fits each Chiller's COP from the Site's last
// 14 days and moves the chilled-water supply temperature where the fit says the Plant uses the
// least electricity, inside the frozen comfort and safety constraints. It only recommends; the
// condenser-water reset needs a condenser setpoint Point, which no Site maps yet.
type COPModelSolver struct {
	History PlantHistory
}

// ShadowEvaluation is what a shadow solver made of one Optimization run.
type ShadowEvaluation struct {
	Solver         string           `json:"solver"`
	Mode           string           `json:"mode"`
	Outcome        string           `json:"outcome"`
	Reason         string           `json:"reason,omitempty"`
	Fit            *PlantFit        `json:"fit,omitempty"`
	CVRMSE         *float64         `json:"cvRmse"`
	CondenserReset map[string]any   `json:"condenserReset"`
	EnergyResponse map[string]any   `json:"energyResponse,omitempty"`
	ZoneResponse   map[string]any   `json:"zoneResponse,omitempty"`
	Candidates     []map[string]any `json:"candidates,omitempty"`
	Recommendation *Recommendation  `json:"recommendation"`
}

const (
	ShadowFeasible           = "FEASIBLE"
	ShadowInfeasible         = "INFEASIBLE"
	ShadowNoUsableModel      = "NO_USABLE_MODEL"
	ShadowHistoryUnavailable = "HISTORY_UNAVAILABLE"
	ShadowInvalidRequest     = "INVALID_REQUEST"
)

var errNoShadowRecommendation = errors.New("chiller COP model made no recommendation")

// Recommend makes COPModelSolver a SolverAdapter: the recommendation Evaluate makes, if any.
func (solver COPModelSolver) Recommend(ctx context.Context, request Request, at time.Time) (Recommendation, error) {
	evaluation := solver.Evaluate(ctx, request, at)
	if evaluation.Recommendation == nil {
		return Recommendation{}, fmt.Errorf("%w: %s %s", errNoShadowRecommendation, evaluation.Outcome, evaluation.Reason)
	}
	return *evaluation.Recommendation, nil
}

// Evaluate fits the Plant and searches the chilled-water supply steps the published solver
// searches. The zone response stays the policy's; the energy response is the fit's.
func (solver COPModelSolver) Evaluate(ctx context.Context, request Request, at time.Time) ShadowEvaluation {
	evaluation := ShadowEvaluation{
		Solver: ChillerCOPSolverName, Mode: "SHADOW",
		CondenserReset: map[string]any{"status": "NOT_AVAILABLE", "reason": "NO_CONDENSER_SETPOINT"},
	}
	if err := request.Validate(); err != nil {
		evaluation.Outcome, evaluation.Reason = ShadowInvalidRequest, err.Error()
		return evaluation
	}
	if solver.History == nil {
		evaluation.Outcome, evaluation.Reason = ShadowHistoryUnavailable, "no Plant history reader"
		return evaluation
	}
	to := at.UTC().Truncate(copBucketMinutes * time.Minute)
	from := to.AddDate(0, 0, -copHistoryDays)
	buckets, err := solver.History.PlantBuckets(ctx, PlantHistoryQuery{TenantID: request.TenantID, SiteID: request.SiteID, From: from, To: to})
	if err != nil {
		evaluation.Outcome, evaluation.Reason = ShadowHistoryUnavailable, err.Error()
		return evaluation
	}
	fit := FitPlant(buckets, from, to)
	evaluation.Fit, evaluation.CVRMSE = &fit, fit.CVRMSEPower
	usable := fit.usable()
	if len(usable) == 0 {
		evaluation.Outcome, evaluation.Reason = ShadowNoUsableModel, "no Chiller has a usable COP fit"
		return evaluation
	}
	since := to.Add(-copReferenceHours * time.Hour)
	plantEnergy := func(delta float64) (float64, int, bool) {
		total, samples, extrapolated := 0.0, 0, false
		for _, chiller := range usable {
			energy, count, outside := chiller.referenceEnergy(since, delta)
			total, samples, extrapolated = total+energy, samples+count, extrapolated || outside
		}
		return total, samples, extrapolated
	}
	reference, referenceSamples, _ := plantEnergy(0)
	baseline, constraints, policy := request.Baseline, request.Constraints, request.ResponseModel
	step := constraints.MaxSupplyTempStep
	up, _, _ := plantEnergy(step)
	down, _, _ := plantEnergy(-step)
	evaluation.EnergyResponse = map[string]any{
		"source": "FITTED_FROM_HISTORY", "dailyEnergyDeltaKWhPerSupplyTempC": round((up-down)/(2*step), 2),
		"referenceChillerEnergyKWh": round(reference, 1), "referenceSamples": referenceSamples, "referenceHours": copReferenceHours,
		"replacesPolicyValue": policy.DailyEnergyDeltaPerSupplyTempC,
	}
	evaluation.ZoneResponse = map[string]any{"source": "POLICY", "zoneTempDeltaCPerSupplyTempC": policy.ZoneTempDeltaPerSupplyTempC, "zoneTempUncertaintyP90C": policy.ZoneTempUncertaintyP90C}
	costPerKWh := baseline.DailyCost / baseline.DailyEnergyKWh
	energyP90 := 1.2816 * *fit.CVRMSEPower * reference
	var best map[string]any
	var bestCost, bestMargin float64
	for _, delta := range []float64{-step, 0, step} {
		supply := baseline.SupplyTempC + delta
		energy, _, extrapolated := plantEnergy(delta)
		zone := baseline.ZoneTempC + policy.ZoneTempDeltaPerSupplyTempC*delta
		candidate := map[string]any{
			"supplyTempC": supply, "projectedZoneTempC": zone, "chillerEnergyDeltaKWh": round(energy-reference, 2),
			"dailyEnergyKWh": math.Max(0, baseline.DailyEnergyKWh+energy-reference), "extrapolated": extrapolated,
		}
		candidate["dailyCost"] = candidate["dailyEnergyKWh"].(float64) * costPerKWh
		zoneLower, zoneUpper := zone-policy.ZoneTempUncertaintyP90C, zone+policy.ZoneTempUncertaintyP90C
		switch {
		case supply < constraints.SupplyTempMinC || supply > constraints.SupplyTempMaxC:
			candidate["feasible"], candidate["reason"] = false, "SUPPLY_TEMP"
		case zoneLower < constraints.ZoneTempMinC || zoneUpper > constraints.ZoneTempMaxC:
			candidate["feasible"], candidate["reason"] = false, "ZONE_COMFORT"
		default:
			candidate["feasible"] = true
			cost := candidate["dailyCost"].(float64)
			if best == nil || cost < bestCost-1e-9 {
				best, bestCost, bestMargin = candidate, cost, math.Min(zoneLower-constraints.ZoneTempMinC, constraints.ZoneTempMaxC-zoneUpper)
			}
		}
		evaluation.Candidates = append(evaluation.Candidates, candidate)
	}
	if best == nil {
		evaluation.Outcome, evaluation.Reason = ShadowInfeasible, "no chilled-water supply step satisfies the frozen comfort and safety constraints"
		return evaluation
	}
	riskLevel := "LOW"
	if bestMargin <= math.Max(0.5, 2*policy.ZoneTempUncertaintyP90C) || best["extrapolated"].(bool) {
		riskLevel = "MEDIUM"
	}
	recommendation := Recommendation{
		ID: deterministicV7(request.OptimizationRunID, "recommendation:"+ChillerCOPSolverName, request.ValidFrom.UTC()), TenantID: request.TenantID, SiteID: request.SiteID,
		InputSnapshotID: request.InputSnapshotID, DeploymentRevision: request.DeploymentRevisionID,
		Baseline:  map[string]any{"dailyEnergyKWh": baseline.DailyEnergyKWh, "dailyCost": baseline.DailyCost, "supplyTempC": baseline.SupplyTempC, "zoneTempC": baseline.ZoneTempC},
		Objective: map[string]any{"kind": request.Objective, "horizon": request.Horizon, "horizonMinutes": request.HorizonMinutes},
		Constraints: []map[string]any{
			{"kind": "SUPPLY_TEMP", "minC": constraints.SupplyTempMinC, "maxC": constraints.SupplyTempMaxC, "maxStepC": constraints.MaxSupplyTempStep},
			{"kind": "ZONE_COMFORT", "minC": constraints.ZoneTempMinC, "maxC": constraints.ZoneTempMaxC},
			{"kind": "CURRENT_STATE_REVALIDATION", "required": true},
		},
		Candidate: map[string]any{"supplyTempC": best["supplyTempC"], "projectedZoneTempC": best["projectedZoneTempC"], "dailyEnergyKWh": best["dailyEnergyKWh"], "dailyCost": best["dailyCost"]},
		ExpectedImpact: map[string]any{
			"energySavingKWhPerDay": baseline.DailyEnergyKWh - best["dailyEnergyKWh"].(float64), "costSavingPerDay": baseline.DailyCost - bestCost,
		},
		Uncertainty: map[string]any{"energyP90KWh": round(energyP90, 1), "energyP90Source": "FITTED_CV_RMSE", "zoneTempP90C": policy.ZoneTempUncertaintyP90C, "cvRmsePower": *fit.CVRMSEPower},
		Risk: map[string]any{
			"level": riskLevel, "comfortMarginC": bestMargin, "extrapolated": best["extrapolated"],
			"savingWithinEnergyUncertainty": math.Abs(baseline.DailyEnergyKWh-best["dailyEnergyKWh"].(float64)) <= energyP90,
		},
		RollbackPlan: map[string]any{
			"restoreSupplyTempC": baseline.SupplyTempC,
			"conditions":         []string{"zone_comfort_violation", "energy_saving_not_observed", "operator_cancelled"},
		},
		VerificationPlan: map[string]any{
			"windowMinutes": 30, "requiredSignals": []string{"supply_temperature", "zone_temperature", "site_load"},
			"successCriteria": []string{"zone_temperature_within_frozen_constraint", "site_load_not_worse_than_baseline"},
		},
		Approval: intelligencemodel.RecommendationDraft, CreatedAt: at.UTC(),
	}
	if err = recommendation.ValidateForApproval(); err != nil {
		evaluation.Outcome, evaluation.Reason = ShadowInfeasible, err.Error()
		return evaluation
	}
	evaluation.Outcome, evaluation.Recommendation = ShadowFeasible, &recommendation
	return evaluation
}

// leastSquares solves the normal equations by Gaussian elimination with partial pivoting.
func leastSquares(rows [][]float64, targets []float64) ([]float64, error) {
	width := len(rows[0])
	if len(rows) <= width {
		return nil, errors.New("too few rows")
	}
	matrix := make([][]float64, width)
	for i := range matrix {
		matrix[i] = make([]float64, width+1)
	}
	for index, row := range rows {
		for i := 0; i < width; i++ {
			for j := 0; j < width; j++ {
				matrix[i][j] += row[i] * row[j]
			}
			matrix[i][width] += row[i] * targets[index]
		}
	}
	for column := 0; column < width; column++ {
		pivot := column
		for row := column + 1; row < width; row++ {
			if math.Abs(matrix[row][column]) > math.Abs(matrix[pivot][column]) {
				pivot = row
			}
		}
		if math.Abs(matrix[pivot][column]) < 1e-9*math.Max(1, math.Abs(matrix[column][column])) {
			return nil, errors.New("singular")
		}
		matrix[column], matrix[pivot] = matrix[pivot], matrix[column]
		for row := 0; row < width; row++ {
			if row == column {
				continue
			}
			factor := matrix[row][column] / matrix[column][column]
			for k := column; k <= width; k++ {
				matrix[row][k] -= factor * matrix[column][k]
			}
		}
	}
	solution := make([]float64, width)
	for i := range solution {
		solution[i] = matrix[i][width] / matrix[i][i]
		if !finite(solution[i]) {
			return nil, errors.New("singular")
		}
	}
	return solution, nil
}

// cvRMSE is ASHRAE Guideline 14's CV(RMSE): sqrt(Σresidual²/(n−p)) / mean(observed).
func cvRMSE(residuals, observed []float64, parameters int) float64 {
	sum := 0.0
	for _, residual := range residuals {
		sum += residual * residual
	}
	return math.Sqrt(sum/float64(len(residuals)-parameters)) / mean(observed)
}

func mean(values []float64) float64 {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func percentile(values []float64, fraction float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	position := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	return sorted[lower] + (sorted[upper]-sorted[lower])*(position-float64(lower))
}

func round(value float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.Round(value*scale) / scale
}

func ptr(value float64) *float64 { return &value }
