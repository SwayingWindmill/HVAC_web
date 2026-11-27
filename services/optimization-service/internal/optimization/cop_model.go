package optimization

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/nameplate"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
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
	copMinimumRunningPowerKW    = DefaultChillerRunningPowerKW
	copMinimumCoolingLoadKW     = nameplate.DefaultHeatBalanceMinimumKW
	copMinimumAllocationDeltaTK = 0.2
	copMinimumPhysicalCOP       = 1.0
	copMaximumPhysicalCOP       = 12.0
)

// PlantBucket is one device's 15-minute means. A field is nil when the device reported no
// usable reading of it in the bucket.
type PlantBucket struct {
	Bucket                 time.Time `json:"bucket"`
	DeviceID               string    `json:"deviceId"`
	PowerKW                *float64  `json:"powerKW"`
	FeederAuxiliaryPowerKW float64   `json:"feederAuxiliaryPowerKW,omitempty"`
	ChwLeavingC            *float64  `json:"chwLeavingC"`
	ChwEnteringC           *float64  `json:"chwEnteringC"`
	CwLeavingC             *float64  `json:"cwLeavingC"`
	CoolingKW              *float64  `json:"coolingKW"`
	RatedPowerKW           *float64  `json:"ratedPowerKW,omitempty"`
	RatedCurrentA          *float64  `json:"ratedCurrentA,omitempty"`
	MeasuredCurrentA       *float64  `json:"measuredCurrentA,omitempty"`
}

type PlantHistoryQuery struct {
	TenantID string
	SiteID   string
	From     time.Time
	To       time.Time
	Twin     *planttwin.SiteTwin
}

// PlantHistory reads a Site's chilled-water Plant as 15-minute buckets.
type PlantHistory interface {
	PlantBuckets(context.Context, PlantHistoryQuery) ([]PlantBucket, error)
}

// COPSample is one steady 15-minute operating point of one Chiller.
type COPSample struct {
	Bucket              time.Time
	LoadKW              float64
	PowerKW             float64
	ChwSupplyC          float64
	CwReturnC           *float64
	Allocation          string
	LoadRatio           float64
	ElectricalLoadRatio *float64 `json:"electricalLoadRatio,omitempty"`
	LoadRatioBasis      string   `json:"loadRatioBasis,omitempty"`
	observedCOP         float64
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
		if bucket.PowerKW != nil && finite(*bucket.PowerKW) && bucket.ChwLeavingC != nil && finite(*bucket.ChwLeavingC) {
			chillerSet[bucket.DeviceID] = true
		}
	}
	for _, bucket := range buckets {
		if chillerSet[bucket.DeviceID] {
			if netPower, ok := netChillerPowerKW(bucket); ok && netPower > maxPower[bucket.DeviceID] {
				maxPower[bucket.DeviceID] = netPower
			}
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
		SetAside:       map[string]int{"noCoolingLoad": 0, "implausibleReading": 0, "startingUp": 0, "implausibleCOP": 0, "aboveNameplate": 0},
		LoadAllocation: map[string]int{"single": 0, "evaporatorDeltaT": 0, "power": 0},
	}
	running := func(group map[string]PlantBucket, id string) bool {
		bucket, ok := group[id]
		if !ok {
			return false
		}
		netPower, ok := netChillerPowerKW(bucket)
		if !ok ||
			maxPower[id] < copMinimumRunningPowerKW ||
			netPower < copMinimumRunningPowerKW ||
			netPower <= copRunningPowerFraction*maxPower[id] {
			return false
		}
		if bucket.RatedPowerKW != nil && finitePositive(*bucket.RatedPowerKW) && netPower <= copRunningPowerFraction**bucket.RatedPowerKW {
			return false
		}
		return true
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
				if !finite(*bucket.CoolingKW) {
					hasLoad = false
					break
				}
				load += *bucket.CoolingKW
				hasLoad = true
			}
		}
		if !hasLoad || !finite(load) || load < copMinimumCoolingLoadKW {
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
			netPower, ok := netChillerPowerKW(bucket)
			if !ok {
				evidence.SetAside["implausibleCOP"]++
				continue
			}
			sample := COPSample{Bucket: at, LoadKW: load * shares[index], PowerKW: netPower, ChwSupplyC: *bucket.ChwLeavingC, CwReturnC: bucket.CwLeavingC, Allocation: method}
			if !finitePositive(sample.LoadKW) || !finitePositive(sample.PowerKW) || sample.PowerKW < copMinimumRunningPowerKW {
				evidence.SetAside["implausibleCOP"]++
				continue
			}
			sample.observedCOP = sample.LoadKW / sample.PowerKW
			if !nameplate.COPPlausible(sample.observedCOP, sample.ChwSupplyC, sample.CwReturnC) {
				evidence.SetAside["implausibleCOP"]++
				continue
			}
			if ratio, basis, ok := nameplate.LoadRatio(&netPower, bucket.MeasuredCurrentA, nameplateFrom(bucket)); ok {
				sample.ElectricalLoadRatio = &ratio
				sample.LoadRatioBasis = basis
				measured, rated := sample.PowerKW, 0.0
				if basis == nameplate.BasisCurrent && bucket.MeasuredCurrentA != nil && bucket.RatedCurrentA != nil {
					measured, rated = *bucket.MeasuredCurrentA, *bucket.RatedCurrentA
				} else if bucket.RatedPowerKW != nil {
					rated = *bucket.RatedPowerKW
				}
				if nameplate.ExceedsRated(measured, rated, nameplate.DefaultPowerAboveRatedFraction) {
					evidence.SetAside["aboveNameplate"]++
					continue
				}
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
		if cv := cvRMSE(residuals, observed, parameters); finite(cv) {
			fit.CVRMSEPower = ptr(round(cv, 4))
		}
	}
	return fit
}

func netChillerPowerKW(bucket PlantBucket) (float64, bool) {
	if bucket.PowerKW == nil || !finite(*bucket.PowerKW) {
		return 0, false
	}
	aux := 0.0
	if finitePositive(bucket.FeederAuxiliaryPowerKW) {
		aux = bucket.FeederAuxiliaryPowerKW
	}
	return math.Max(0, *bucket.PowerKW-aux), true
}

func nameplateFrom(bucket PlantBucket) nameplate.Plate {
	plate := nameplate.Plate{}
	if bucket.RatedPowerKW != nil {
		plate.RatedPowerKW = *bucket.RatedPowerKW
	}
	if bucket.RatedCurrentA != nil {
		plate.RatedCurrentA = *bucket.RatedCurrentA
	}
	return plate
}

// plausible rejects buckets whose temperatures cannot be a running Chiller's: supply outside
// 2–12 ℃ or warmer than the return, condenser outside 10–45 ℃ or colder than the supply.
func plausible(group map[string]PlantBucket, runningIDs []string) bool {
	for _, id := range runningIDs {
		bucket := group[id]
		if bucket.ChwLeavingC == nil || !finite(*bucket.ChwLeavingC) {
			return false
		}
		supply := *bucket.ChwLeavingC
		if supply < 2 || supply > 12 {
			return false
		}
		if bucket.ChwEnteringC != nil && (!finite(*bucket.ChwEnteringC) || supply > *bucket.ChwEnteringC+0.5) {
			return false
		}
		if bucket.CwLeavingC != nil && (!finite(*bucket.CwLeavingC) || *bucket.CwLeavingC < 10 || *bucket.CwLeavingC > 45 || *bucket.CwLeavingC <= supply) {
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
		if bucket.ChwEnteringC == nil || !finite(*bucket.ChwEnteringC) || !finite(*bucket.ChwLeavingC) || *bucket.ChwEnteringC-*bucket.ChwLeavingC < copMinimumAllocationDeltaTK {
			byDrop = false
			break
		}
		drops[index] = *bucket.ChwEnteringC - *bucket.ChwLeavingC
		total += drops[index]
	}
	if byDrop && finitePositive(total) {
		for index := range drops {
			drops[index] /= total
		}
		return drops, "evaporatorDeltaT"
	}
	total = 0
	for index, id := range runningIDs {
		netPower, _ := netChillerPowerKW(group[id])
		drops[index] = netPower
		total += drops[index]
	}
	if !finitePositive(total) {
		even := 1.0 / float64(len(runningIDs))
		for index := range drops {
			drops[index] = even
		}
		return drops, "power"
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
	const bucketHours = float64(copBucketMinutes) / 60.0
	powerResiduals, powers, copResiduals, cops := make([]float64, len(samples)), make([]float64, len(samples)), make([]float64, len(samples)), make([]float64, len(samples))
	predictable := true
	var totalCoolingKWh, totalElectricityKWh float64
	for index, sample := range samples {
		predicted := chiller.predictCOP(sample.LoadRatio, sample.ChwSupplyC, sample.CwReturnC)
		if !finite(predicted) || predicted <= 0.5 {
			predictable = false
		}
		safePredicted := 0.5
		if finite(predicted) && predicted > 0.5 {
			safePredicted = predicted
		}
		copResiduals[index], cops[index] = sample.observedCOP-predicted, sample.observedCOP
		powerResiduals[index], powers[index] = sample.PowerKW-sample.LoadKW/safePredicted, sample.PowerKW
		totalCoolingKWh += sample.LoadKW * bucketHours
		totalElectricityKWh += sample.PowerKW * bucketHours
	}
	powerCV, copCV := cvRMSE(powerResiduals, powers, len(coefficients)), cvRMSE(copResiduals, cops, len(coefficients))
	if finite(powerCV) {
		chiller.CVRMSEPower = ptr(round(powerCV, 4))
	}
	if finite(copCV) {
		chiller.CVRMSECOP = ptr(round(copCV, 4))
	}
	var meanCOP float64
	validMeanCOP := finitePositive(totalCoolingKWh) && finitePositive(totalElectricityKWh)
	if validMeanCOP {
		// Period/fit MeanCOP must be energy-weighted SUM(cooling_kwh)/SUM(electricity_kwh),
		// never an arithmetic mean of per-bucket instantaneous COPs.
		meanCOP = totalCoolingKWh / totalElectricityKWh
		validMeanCOP = finite(meanCOP) && meanCOP >= copMinimumPhysicalCOP && meanCOP <= copMaximumPhysicalCOP
	}
	if validMeanCOP {
		chiller.MeanCOP = ptr(round(meanCOP, 3))
	}
	switch {
	case !predictable:
		chiller.Status, chiller.Reason = ChillerRejected, "the fit predicts a COP at or below 0.5 for an observed operating point"
	case !validMeanCOP:
		chiller.Status, chiller.Reason = ChillerRejected, "energy-weighted mean COP is outside physical bounds"
	case !finite(powerCV) || !finite(copCV):
		chiller.Status, chiller.Reason = ChillerRejected, "the fit error metric is not finite"
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
		predicted := chiller.predictCOP(sample.LoadRatio, supply, sample.CwReturnC)
		denom := 0.5
		if finite(predicted) && predicted > 0.5 {
			denom = predicted
		}
		energy += sample.LoadKW / denom * copBucketMinutes / 60
		count++
	}
	return energy, count, extrapolated
}

// COPModelSolver is the Chiller COP Model solver: it fits each Chiller's COP from the Site's last
// 14 days and moves the chilled-water supply temperature where the fit says the Plant uses the
// least electricity, inside the frozen comfort and safety constraints. It only recommends; the
// condenser-water reset needs a condenser setpoint Point, which no Site maps yet.
type COPModelSolver struct {
	History        PlantHistory
	Staging        StagingSnapshotReader
	StagingCatalog StagingSiteCatalog
	TwinLoader     planttwin.Loader
	StagingPolicy  *StagingPolicy
}

// ShadowEvaluation is what a shadow solver made of one Optimization run.
type ShadowEvaluation struct {
	Solver         string                `json:"solver"`
	Mode           string                `json:"mode"`
	Outcome        string                `json:"outcome"`
	Reason         string                `json:"reason,omitempty"`
	Fit            *PlantFit             `json:"fit,omitempty"`
	CVRMSE         *float64              `json:"cvRmse"`
	CondenserReset *ShadowCondenserReset `json:"condenserReset"`
	EnergyResponse *ShadowEnergyResponse `json:"energyResponse,omitempty"`
	ZoneResponse   *ShadowZoneResponse   `json:"zoneResponse,omitempty"`
	Candidates     []ShadowCandidate     `json:"candidates,omitempty"`
	Staging        *StagingEvaluation    `json:"staging,omitempty"`
	Recommendation *Recommendation       `json:"recommendation"`
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
		Solver:         ChillerCOPSolverName,
		Mode:           "SHADOW",
		CondenserReset: &ShadowCondenserReset{Status: "NOT_AVAILABLE", Reason: "NO_CONDENSER_SETPOINT"},
	}
	if err := request.Validate(); err != nil {
		evaluation.Outcome, evaluation.Reason = ShadowInvalidRequest, err.Error()
		return evaluation
	}
	if solver.History == nil {
		evaluation.Outcome, evaluation.Reason = ShadowHistoryUnavailable, "no Plant history reader"
		return evaluation
	}
	twin, topology, _ := loadTwinAndTopology(ctx, request.TenantID, request.SiteID, at.UTC(), solver.TwinLoader, solver.StagingCatalog)
	to := at.UTC().Truncate(copBucketMinutes * time.Minute)
	from := to.AddDate(0, 0, -copHistoryDays)
	buckets, err := solver.History.PlantBuckets(ctx, PlantHistoryQuery{TenantID: request.TenantID, SiteID: request.SiteID, From: from, To: to, Twin: twin})
	if err != nil {
		evaluation.Outcome, evaluation.Reason = ShadowHistoryUnavailable, err.Error()
		return evaluation
	}
	fit := FitPlant(buckets, from, to)
	evaluation.Fit, evaluation.CVRMSE = &fit, fit.CVRMSEPower
	evaluation.Staging = evaluateStagingWithFit(ctx, request.TenantID, request.SiteID, at, fit, solver.Staging, solver.StagingCatalog, twin, topology, solver.StagingPolicy)
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
	evaluation.EnergyResponse = &ShadowEnergyResponse{
		Source:                            "FITTED_FROM_HISTORY",
		DailyEnergyDeltaKWhPerSupplyTempC: round((up-down)/(2*step), 2),
		ReferenceChillerEnergyKWh:         round(reference, 1),
		ReferenceSamples:                  referenceSamples,
		ReferenceHours:                    copReferenceHours,
		ReplacesPolicyValue:               policy.DailyEnergyDeltaPerSupplyTempC,
	}
	evaluation.ZoneResponse = &ShadowZoneResponse{
		Source:                       "POLICY",
		ZoneTempDeltaCPerSupplyTempC: policy.ZoneTempDeltaPerSupplyTempC,
		ZoneTempUncertaintyP90C:      policy.ZoneTempUncertaintyP90C,
	}
	costPerKWh := baseline.DailyCost / baseline.DailyEnergyKWh
	energyP90 := 1.2816 * *fit.CVRMSEPower * reference
	var best *ShadowCandidate
	var bestCost, bestMargin float64
	for _, delta := range []float64{-step, 0, step} {
		supply := baseline.SupplyTempC + delta
		energy, _, extrapolated := plantEnergy(delta)
		zone := baseline.ZoneTempC + policy.ZoneTempDeltaPerSupplyTempC*delta
		dailyEnergy := math.Max(0, baseline.DailyEnergyKWh+energy-reference)
		candidate := ShadowCandidate{
			SupplyTempC:           supply,
			ProjectedZoneTempC:    zone,
			ChillerEnergyDeltaKWh: round(energy-reference, 2),
			DailyEnergyKWh:        dailyEnergy,
			DailyCost:             dailyEnergy * costPerKWh,
			Extrapolated:          extrapolated,
		}
		zoneLower, zoneUpper := zone-policy.ZoneTempUncertaintyP90C, zone+policy.ZoneTempUncertaintyP90C
		switch {
		case supply < constraints.SupplyTempMinC || supply > constraints.SupplyTempMaxC:
			candidate.Feasible, candidate.Reason = false, "SUPPLY_TEMP"
		case zoneLower < constraints.ZoneTempMinC || zoneUpper > constraints.ZoneTempMaxC:
			candidate.Feasible, candidate.Reason = false, "ZONE_COMFORT"
		default:
			candidate.Feasible = true
			if best == nil || candidate.DailyCost < bestCost-1e-9 {
				copyCandidate := candidate
				best = &copyCandidate
				bestCost = candidate.DailyCost
				bestMargin = math.Min(zoneLower-constraints.ZoneTempMinC, constraints.ZoneTempMaxC-zoneUpper)
			}
		}
		evaluation.Candidates = append(evaluation.Candidates, candidate)
	}
	if best == nil {
		evaluation.Outcome, evaluation.Reason = ShadowInfeasible, "no chilled-water supply step satisfies the frozen comfort and safety constraints"
		return evaluation
	}
	riskLevel := "LOW"
	if bestMargin <= math.Max(0.5, 2*policy.ZoneTempUncertaintyP90C) || best.Extrapolated {
		riskLevel = "MEDIUM"
	}
	cvRmsePower := *fit.CVRMSEPower
	extrapolated := best.Extrapolated
	savingWithinEnergyUncertainty := math.Abs(baseline.DailyEnergyKWh-best.DailyEnergyKWh) <= energyP90
	recommendation := Recommendation{
		ID: deterministicV7(request.OptimizationRunID, "recommendation:"+ChillerCOPSolverName, request.ValidFrom.UTC()), TenantID: request.TenantID, SiteID: request.SiteID,
		InputSnapshotID: request.InputSnapshotID, DeploymentRevision: request.DeploymentRevisionID,
		Baseline: intelligencemodel.RecommendationBaseline{
			DailyEnergyKWh: baseline.DailyEnergyKWh, DailyCost: baseline.DailyCost, SupplyTempC: baseline.SupplyTempC, ZoneTempC: baseline.ZoneTempC,
		},
		Objective:   intelligencemodel.RecommendationObjective{Kind: request.Objective, Horizon: request.Horizon, HorizonMinutes: request.HorizonMinutes},
		Constraints: frozenRecommendationConstraints(constraints),
		Candidate: intelligencemodel.RecommendationCandidate{
			SupplyTempC: best.SupplyTempC, ProjectedZoneTempC: best.ProjectedZoneTempC, DailyEnergyKWh: best.DailyEnergyKWh, DailyCost: best.DailyCost,
		},
		ExpectedImpact: intelligencemodel.RecommendationExpectedImpact{
			EnergySavingKWhPerDay: baseline.DailyEnergyKWh - best.DailyEnergyKWh, CostSavingPerDay: baseline.DailyCost - bestCost,
		},
		Uncertainty: intelligencemodel.RecommendationUncertainty{
			EnergyP90KWh: round(energyP90, 1), EnergyP90Source: "FITTED_CV_RMSE", ZoneTempP90C: policy.ZoneTempUncertaintyP90C, CVRMSEPower: &cvRmsePower,
		},
		Risk: intelligencemodel.RecommendationRisk{
			Level: riskLevel, ComfortMarginC: bestMargin, Extrapolated: &extrapolated, SavingWithinEnergyUncertainty: &savingWithinEnergyUncertainty,
		},
		RollbackPlan:     standardRollbackPlan(baseline.SupplyTempC),
		VerificationPlan: standardVerificationPlan(),
		Approval:         intelligencemodel.RecommendationDraft,
		ValidUntil:       timePointer(at.UTC().Add(intelligencemodel.DefaultRecommendationTTL)),
		CreatedAt:        at.UTC(),
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
	if len(residuals) <= parameters || len(observed) == 0 {
		return math.NaN()
	}
	observedMean := mean(observed)
	if !finitePositive(observedMean) {
		return math.NaN()
	}
	sum := 0.0
	for _, residual := range residuals {
		sum += residual * residual
	}
	return math.Sqrt(sum/float64(len(residuals)-parameters)) / observedMean
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
