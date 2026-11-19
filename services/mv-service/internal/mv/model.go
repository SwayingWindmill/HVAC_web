// Package mv verifies energy savings for a cold plant the IPMVP way: a baseline line of hourly
// electricity on the measurement boundary (the plant's total meter, Option C, or one device's
// sub-meter, Option B) against hourly plant cooling, fitted before a measure and frozen, then each
// reporting hour's adjusted baseline minus what the boundary actually used (ADR 0021).
package mv

import (
	"math"
	"time"
)

const (
	// MinimumBaselineHours is the fewest complete hours a baseline may be fitted on.
	MinimumBaselineHours = 72
	// MinimumBaselineCoverage is the share of the baseline period's hours that must be measured.
	MinimumBaselineCoverage = 0.9
	// ASHRAE Guideline 14 limits for hourly models.
	MaximumCVRMSE = 0.30
	MaximumNMBE   = 0.10
	// StabilizationPeriod after implementation is not verified: the plant is still settling.
	StabilizationPeriod = 24 * time.Hour
)

// Why a baseline is not trusted, in the operator's words.
const (
	ReasonTooFewHours = "TOO_FEW_HOURS"
	ReasonLowCoverage = "LOW_COVERAGE"
	ReasonPoorFit     = "POOR_FIT"
)

// Baseline regression model kinds persisted for each implemented measure.
const (
	ModelKindUnivariateCooling = "UNIVARIATE_COOLING"
	ModelKindBivariateWetBulb  = "BIVARIATE_COOLING_WET_BULB"
	minimumWetBulbVariance     = 0.25
)

// HourSample is one complete hour of the measurement boundary: its electricity, the plant's
// cooling delivered, the plant's electricity (which equals the boundary's on the total meter),
// and optional outdoor weather / plant operating condition anchors.
type HourSample struct {
	Start             time.Time
	EnergyKWh         float64
	CoolingKWh        float64
	PlantKWh          float64
	OutdoorWetBulbC   *float64
	OutdoorDryBulbC   *float64
	PlantLoadRatio    *float64
	AIClosedLoopShare *float64
}

// Baseline is the fitted regression model (univariate against cooling, or bivariate against
// cooling and outdoor wet-bulb temperature), with how well it fits.
type Baseline struct {
	From, To      time.Time
	ModelKind     string
	Intercept     float64
	Slope         float64
	WetBulbSlope  *float64
	RSquared      *float64
	MeanWetBulbC  *float64
	Samples       int
	ExpectedHours int
	CVRMSE        float64
	NMBE          float64
	Trusted       bool
	Reason        string
}

// Fitted says whether the period held enough data for a line to be fitted at all; when it did not,
// the coefficients and statistics are not results and say nothing about the fit.
func (baseline Baseline) Fitted() bool {
	return baseline.Reason != ReasonTooFewHours && baseline.Reason != ReasonLowCoverage
}

func (baseline Baseline) Predict(coolingKWh float64) float64 {
	return baseline.Intercept + baseline.Slope*coolingKWh
}

// PredictWithCondition predicts boundary electricity using cooling load and, for bivariate
// models, outdoor wet-bulb temperature. Missing runtime wet-bulb readings fall back to the
// baseline period's mean wet-bulb temperature so energy balance is preserved.
func (baseline Baseline) PredictWithCondition(coolingKWh float64, wetBulbC *float64) float64 {
	if baseline.ModelKind != ModelKindBivariateWetBulb || baseline.WetBulbSlope == nil || !isFiniteFloat(*baseline.WetBulbSlope) {
		return baseline.Predict(coolingKWh)
	}
	if wetBulbC != nil && isFiniteFloat(*wetBulbC) {
		return baseline.Intercept + baseline.Slope*coolingKWh + (*baseline.WetBulbSlope)*(*wetBulbC)
	}
	if baseline.MeanWetBulbC != nil && isFiniteFloat(*baseline.MeanWetBulbC) {
		return baseline.Intercept + baseline.Slope*coolingKWh + (*baseline.WetBulbSlope)*(*baseline.MeanWetBulbC)
	}
	return baseline.Predict(coolingKWh)
}

func isFiniteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func floatPtr(value float64) *float64 {
	return &value
}

func sanitizeFloatPtr(value *float64) *float64 {
	if value == nil || !isFiniteFloat(*value) {
		return nil
	}
	v := *value
	return &v
}

func fitBivariateWetBulb(samples []HourSample) (intercept, coolingSlope, wetBulbSlope, meanWetBulb float64, ok bool) {
	wbSamples := make([]HourSample, 0, len(samples))
	var sumQ, sumT, sumY float64
	for _, sample := range samples {
		if sample.OutdoorWetBulbC == nil || !isFiniteFloat(*sample.OutdoorWetBulbC) ||
			!isFiniteFloat(sample.CoolingKWh) || !isFiniteFloat(sample.EnergyKWh) {
			continue
		}
		wbSamples = append(wbSamples, sample)
		sumQ += sample.CoolingKWh
		sumT += *sample.OutdoorWetBulbC
		sumY += sample.EnergyKWh
	}
	if len(wbSamples) < MinimumBaselineHours || len(samples) == 0 {
		return 0, 0, 0, 0, false
	}
	if float64(len(wbSamples))/float64(len(samples)) < MinimumBaselineCoverage {
		return 0, 0, 0, 0, false
	}
	m := float64(len(wbSamples))
	meanQ, meanT, meanY := sumQ/m, sumT/m, sumY/m
	var sxx, stt, sxt, sxy, sty float64
	for _, sample := range wbSamples {
		dq := sample.CoolingKWh - meanQ
		dt := *sample.OutdoorWetBulbC - meanT
		dy := sample.EnergyKWh - meanY
		sxx += dq * dq
		stt += dt * dt
		sxt += dq * dt
		sxy += dq * dy
		sty += dt * dy
	}
	if stt/m <= minimumWetBulbVariance || sxx <= 0 {
		return 0, 0, 0, 0, false
	}
	det := sxx*stt - sxt*sxt
	// Require both absolute determinant and non-collinearity relative to variance scale.
	if math.Abs(det) <= 1e-9 || det <= 1e-9*sxx*stt {
		return 0, 0, 0, 0, false
	}
	b1 := (sxy*stt - sty*sxt) / det
	b2 := (sxx*sty - sxt*sxy) / det
	a := meanY - b1*meanQ - b2*meanT
	if !isFiniteFloat(a) || !isFiniteFloat(b1) || !isFiniteFloat(b2) {
		return 0, 0, 0, 0, false
	}
	return a, b1, b2, meanT, true
}

// FitBaseline fits the baseline period [from, to) by ordinary least squares and checks it.
// When sufficient outdoor wet-bulb coverage and variation exist, it fits a bivariate model
// E = a + b1·Q + b2·Twb; otherwise it falls back to univariate cooling OLS.
func FitBaseline(samples []HourSample, from, to time.Time) Baseline {
	baseline := Baseline{
		From:          from,
		To:            to,
		ModelKind:     ModelKindUnivariateCooling,
		Samples:       len(samples),
		ExpectedHours: int(to.Sub(from) / time.Hour),
	}
	if baseline.Samples < MinimumBaselineHours {
		baseline.Reason = ReasonTooFewHours
		return baseline
	}
	if baseline.ExpectedHours <= 0 || float64(baseline.Samples)/float64(baseline.ExpectedHours) < MinimumBaselineCoverage {
		baseline.Reason = ReasonLowCoverage
		return baseline
	}

	n := float64(len(samples))
	degrees := n - 2
	if a, b1, b2, meanT, ok := fitBivariateWetBulb(samples); ok {
		baseline.ModelKind = ModelKindBivariateWetBulb
		baseline.Intercept = a
		baseline.Slope = b1
		baseline.WetBulbSlope = floatPtr(b2)
		baseline.MeanWetBulbC = floatPtr(meanT)
		degrees = n - 3
	} else {
		var sumX, sumY float64
		for _, sample := range samples {
			sumX += sample.CoolingKWh
			sumY += sample.EnergyKWh
		}
		meanX, meanY := sumX/n, sumY/n
		var sxx, sxy float64
		for _, sample := range samples {
			sxx += (sample.CoolingKWh - meanX) * (sample.CoolingKWh - meanX)
			sxy += (sample.CoolingKWh - meanX) * (sample.EnergyKWh - meanY)
		}
		if sxx > 0 {
			baseline.Slope = sxy / sxx
		}
		baseline.Intercept = meanY - baseline.Slope*meanX
	}

	var sumY float64
	for _, sample := range samples {
		sumY += sample.EnergyKWh
	}
	meanY := sumY / n

	// Guideline 14 statistics with p = 2 (univariate) or p = 3 (bivariate) fitted parameters.
	var squared, signed, sst float64
	for _, sample := range samples {
		residual := sample.EnergyKWh - baseline.PredictWithCondition(sample.CoolingKWh, sample.OutdoorWetBulbC)
		squared += residual * residual
		signed += residual
		diff := sample.EnergyKWh - meanY
		sst += diff * diff
	}
	if sst > 0 {
		r2 := 1 - squared/sst
		if isFiniteFloat(r2) {
			baseline.RSquared = &r2
		}
	}
	if meanY > 0 {
		baseline.CVRMSE = math.Sqrt(squared/degrees) / meanY
		baseline.NMBE = signed / (degrees * meanY)
	}
	if meanY <= 0 || !isFiniteFloat(baseline.CVRMSE) || !isFiniteFloat(baseline.NMBE) || baseline.CVRMSE > MaximumCVRMSE || math.Abs(baseline.NMBE) > MaximumNMBE {
		baseline.Reason = ReasonPoorFit
		return baseline
	}
	baseline.Trusted = true
	return baseline
}

// DayResult is one Site-local day of a measure's reporting period.
type DayResult struct {
	ExpectedHours       int
	Hours               int
	UnpricedHours       int
	AdjustedBaselineKWh float64
	ActualKWh           float64
	AvoidedKWh          float64
	AvoidedCost         float64
	// PlantKWh is the plant's electricity over the verified hours.
	PlantKWh          float64
	MeanWetBulbC      *float64
	MeanDryBulbC      *float64
	MeanLoadRatio     *float64
	AIClosedLoopShare *float64
}

type runningMean struct {
	sum   float64
	count int
}

func (m *runningMean) add(value *float64) {
	if value == nil || !isFiniteFloat(*value) {
		return
	}
	m.sum += *value
	m.count++
}

func (m runningMean) mean() *float64 {
	if m.count == 0 {
		return nil
	}
	mean := m.sum / float64(m.count)
	return &mean
}

// VerifyDay compares each measured hour of [dayStart, dayEnd) from reportingFrom on with what the
// baseline predicts for its cooling and outdoor wet-bulb condition. Missing hours are counted,
// never filled; an hour no tariff period prices still counts its energy but not its cost.
func VerifyDay(baseline Baseline, samples []HourSample, dayStart, dayEnd, reportingFrom time.Time, rate func(time.Time) (float64, bool)) DayResult {
	start := dayStart
	if reportingFrom.After(start) {
		start = reportingFrom
	}
	var result DayResult
	if dayEnd.After(start) {
		result.ExpectedHours = int(dayEnd.Sub(start) / time.Hour)
	}
	var wetBulb, dryBulb, loadRatio, aiShare runningMean
	for _, sample := range samples {
		if sample.Start.Before(start) || !sample.Start.Before(dayEnd) {
			continue
		}
		adjusted := baseline.PredictWithCondition(sample.CoolingKWh, sample.OutdoorWetBulbC)
		avoided := adjusted - sample.EnergyKWh
		result.Hours++
		result.AdjustedBaselineKWh += adjusted
		result.ActualKWh += sample.EnergyKWh
		result.AvoidedKWh += avoided
		result.PlantKWh += sample.PlantKWh
		wetBulb.add(sample.OutdoorWetBulbC)
		dryBulb.add(sample.OutdoorDryBulbC)
		loadRatio.add(sample.PlantLoadRatio)
		aiShare.add(sample.AIClosedLoopShare)
		if price, ok := rate(sample.Start); ok {
			result.AvoidedCost += avoided * price
		} else {
			result.UnpricedHours++
		}
	}
	result.MeanWetBulbC = wetBulb.mean()
	result.MeanDryBulbC = dryBulb.mean()
	result.MeanLoadRatio = loadRatio.mean()
	result.AIClosedLoopShare = aiShare.mean()
	return result
}
