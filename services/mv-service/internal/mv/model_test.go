package mv

import (
	"math"
	"testing"
	"time"
)

var shanghai = time.FixedZone("CST", 8*3600)

// hours builds consecutive hourly samples from `start` whose energy follows energy(cooling).
func hours(start time.Time, coolings []float64, energy func(index int, cooling float64) float64) []HourSample {
	samples := make([]HourSample, len(coolings))
	for index, cooling := range coolings {
		samples[index] = HourSample{Start: start.Add(time.Duration(index) * time.Hour), CoolingKWh: cooling, EnergyKWh: energy(index, cooling)}
	}
	return samples
}

// dayOfCooling is an office day's hourly cooling: low at night, high while occupied.
func dayOfCooling(days int) []float64 {
	coolings := make([]float64, 0, days*24)
	for day := 0; day < days; day++ {
		for hour := 0; hour < 24; hour++ {
			cooling := 300.0
			if hour >= 8 && hour < 20 {
				cooling = 700 + 10*float64(hour-8) + 15*float64(day%3)
			}
			coolings = append(coolings, cooling)
		}
	}
	return coolings
}

func TestABaselineIsTheLinePlantEnergyFollowsWithCooling(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)
	samples := hours(from, dayOfCooling(4), func(index int, cooling float64) float64 {
		// 20 kWh of standing load plus 0.25 kWh per kWh of cooling, with a small wobble.
		return 20 + 0.25*cooling + 1.5*math.Sin(float64(index))
	})
	baseline := FitBaseline(samples, from, from.Add(96*time.Hour))
	if !baseline.Trusted {
		t.Fatalf("baseline not trusted: %+v", baseline)
	}
	if math.Abs(baseline.Slope-0.25) > 0.01 || math.Abs(baseline.Intercept-20) > 3 {
		t.Fatalf("coefficients = %v + %v·cooling", baseline.Intercept, baseline.Slope)
	}
	if baseline.Samples != 96 || baseline.CVRMSE > 0.05 || math.Abs(baseline.NMBE) > 0.001 {
		t.Fatalf("fit quality %+v", baseline)
	}
}

func TestABaselineWithoutEnoughHoursIsNotTrusted(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)
	short := hours(from, dayOfCooling(2), func(_ int, cooling float64) float64 { return 20 + 0.25*cooling })
	if baseline := FitBaseline(short, from, from.Add(48*time.Hour)); baseline.Trusted || baseline.Reason != ReasonTooFewHours {
		t.Fatalf("48 hours accepted: %+v", baseline)
	}
	// Five days requested, a day of them missing: coverage below 90%.
	gappy := hours(from, dayOfCooling(5), func(_ int, cooling float64) float64 { return 20 + 0.25*cooling })[:96]
	if baseline := FitBaseline(gappy, from, from.Add(120*time.Hour)); baseline.Trusted || baseline.Reason != ReasonLowCoverage {
		t.Fatalf("80%% coverage accepted: %+v", baseline)
	}
}

func TestABaselineThatDoesNotExplainEnergyIsNotTrusted(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)
	noisy := hours(from, dayOfCooling(4), func(index int, _ float64) float64 {
		// Energy unrelated to cooling, swinging wildly.
		return 200 + 180*math.Sin(float64(index)*1.7)
	})
	baseline := FitBaseline(noisy, from, from.Add(96*time.Hour))
	if baseline.Trusted || baseline.Reason != ReasonPoorFit || baseline.CVRMSE <= 0.30 {
		t.Fatalf("a model that explains nothing passed: %+v", baseline)
	}
}

func TestADayIsVerifiedAgainstItsAdjustedBaselineAndPricedByTheHour(t *testing.T) {
	baseline := Baseline{Intercept: 20, Slope: 0.25, Trusted: true}
	implemented := time.Date(2026, 10, 10, 9, 0, 0, 0, shanghai)
	day := time.Date(2026, 10, 11, 0, 0, 0, 0, shanghai)
	// The plant now uses 10% less than the baseline predicts for the same cooling.
	samples := hours(day, dayOfCooling(1), func(_ int, cooling float64) float64 { return 0.9 * (20 + 0.25*cooling) })
	rate := func(at time.Time) (float64, bool) {
		if hour := at.In(shanghai).Hour(); hour >= 8 && hour < 20 {
			return 1.2, true
		}
		return 0.32, true
	}
	result := VerifyDay(baseline, samples, day, day.Add(24*time.Hour), implemented.Add(StabilizationPeriod), rate)
	// The first nine hours fall in the 24-hour stabilization period after implementation.
	if result.ExpectedHours != 15 || result.Hours != 15 {
		t.Fatalf("hours = %d of %d", result.Hours, result.ExpectedHours)
	}
	var adjusted, cost float64
	for _, sample := range samples[9:] {
		predicted := 20 + 0.25*sample.CoolingKWh
		adjusted += predicted
		rate, _ := rate(sample.Start)
		cost += 0.1 * predicted * rate
	}
	if math.Abs(result.AdjustedBaselineKWh-adjusted) > 1e-6 || math.Abs(result.AvoidedKWh-0.1*adjusted) > 1e-6 || math.Abs(result.AvoidedCost-cost) > 1e-6 {
		t.Fatalf("result %+v want adjusted %v avoided %v cost %v", result, adjusted, 0.1*adjusted, cost)
	}
}

func TestMissingAndUnpricedHoursAreCountedNotFilled(t *testing.T) {
	baseline := Baseline{Intercept: 20, Slope: 0.25, Trusted: true}
	day := time.Date(2026, 10, 11, 0, 0, 0, 0, shanghai)
	samples := hours(day, dayOfCooling(1), func(_ int, cooling float64) float64 { return 20 + 0.25*cooling })
	samples = append(samples[:5], samples[8:]...) // three hours never measured
	rate := func(at time.Time) (float64, bool) { return 0.72, at.In(shanghai).Hour() != 12 }
	result := VerifyDay(baseline, samples, day, day.Add(24*time.Hour), day.Add(-48*time.Hour), rate)
	if result.ExpectedHours != 24 || result.Hours != 21 || result.UnpricedHours != 1 {
		t.Fatalf("result %+v", result)
	}
	if result.AvoidedKWh > 1e-9 || result.AvoidedKWh < -1e-9 {
		t.Fatalf("a plant on its baseline avoided %v kWh", result.AvoidedKWh)
	}
}
