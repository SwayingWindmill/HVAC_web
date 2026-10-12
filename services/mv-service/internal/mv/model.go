// Package mv verifies energy savings the IPMVP Option C way for a cold plant: a baseline line
// of hourly plant electricity against hourly cooling, fitted before a measure and frozen, then
// each reporting hour's adjusted baseline minus what the plant actually used (ADR 0021).
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
	ReasonTooFewHours = "基线期完整小时不足 72 小时"
	ReasonLowCoverage = "基线期数据覆盖率低于 90%"
	ReasonPoorFit     = "模型未通过 ASHRAE Guideline 14 检验"
)

// HourSample is one complete hour of the measurement boundary: plant electricity and cooling delivered.
type HourSample struct {
	Start      time.Time
	EnergyKWh  float64
	CoolingKWh float64
}

// Baseline is the fitted line energy = Intercept + Slope × cooling, with how well it fits.
type Baseline struct {
	From, To      time.Time
	Intercept     float64
	Slope         float64
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

// FitBaseline fits the baseline period [from, to) by ordinary least squares and checks it.
func FitBaseline(samples []HourSample, from, to time.Time) Baseline {
	baseline := Baseline{From: from, To: to, Samples: len(samples), ExpectedHours: int(to.Sub(from) / time.Hour)}
	if baseline.Samples < MinimumBaselineHours {
		baseline.Reason = ReasonTooFewHours
		return baseline
	}
	if baseline.ExpectedHours <= 0 || float64(baseline.Samples)/float64(baseline.ExpectedHours) < MinimumBaselineCoverage {
		baseline.Reason = ReasonLowCoverage
		return baseline
	}

	n := float64(len(samples))
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

	// Guideline 14 statistics with p = 2 fitted parameters.
	var squared, signed float64
	for _, sample := range samples {
		residual := sample.EnergyKWh - baseline.Predict(sample.CoolingKWh)
		squared += residual * residual
		signed += residual
	}
	degrees := n - 2
	if meanY > 0 {
		baseline.CVRMSE = math.Sqrt(squared/degrees) / meanY
		baseline.NMBE = signed / (degrees * meanY)
	}
	if meanY <= 0 || baseline.CVRMSE > MaximumCVRMSE || math.Abs(baseline.NMBE) > MaximumNMBE {
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
}

// VerifyDay compares each measured hour of [dayStart, dayEnd) from reportingFrom on with what the
// baseline predicts for its cooling. Missing hours are counted, never filled; an hour no tariff
// period prices still counts its energy but not its cost.
func VerifyDay(baseline Baseline, samples []HourSample, dayStart, dayEnd, reportingFrom time.Time, rate func(time.Time) (float64, bool)) DayResult {
	start := dayStart
	if reportingFrom.After(start) {
		start = reportingFrom
	}
	var result DayResult
	if dayEnd.After(start) {
		result.ExpectedHours = int(dayEnd.Sub(start) / time.Hour)
	}
	for _, sample := range samples {
		if sample.Start.Before(start) || !sample.Start.Before(dayEnd) {
			continue
		}
		adjusted := baseline.Predict(sample.CoolingKWh)
		avoided := adjusted - sample.EnergyKWh
		result.Hours++
		result.AdjustedBaselineKWh += adjusted
		result.ActualKWh += sample.EnergyKWh
		result.AvoidedKWh += avoided
		if price, ok := rate(sample.Start); ok {
			result.AvoidedCost += avoided * price
		} else {
			result.UnpricedHours++
		}
	}
	return result
}

// TariffPeriod is the part of a Tariff Version that prices energy by local time of day.
type TariffPeriod struct {
	DayType          string
	StartMinute      int
	EndMinute        int
	EnergyRatePerKWh float64
}

// TariffRates prices an hour by the period covering its start on the Site's clock. Holidays price
// as their weekday until Tariffs carry a calendar, as Settlement does.
func TariffRates(periods []TariffPeriod, location *time.Location) func(time.Time) (float64, bool) {
	return func(at time.Time) (float64, bool) {
		local := at.In(location)
		dayType := "WEEKDAY"
		if weekday := local.Weekday(); weekday == time.Saturday || weekday == time.Sunday {
			dayType = "WEEKEND"
		}
		minute := local.Hour()*60 + local.Minute()
		for _, period := range periods {
			if period.DayType == dayType && period.StartMinute <= minute && minute < period.EndMinute {
				return period.EnergyRatePerKWh, true
			}
		}
		return 0, false
	}
}
