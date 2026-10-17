package mv

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

const (
	testTenant = "01990000-3000-7000-8000-000000000001"
	testSite   = "01990000-5000-7000-8000-000000000001"
	testActor  = "01990000-2000-7000-8000-000000000001"
	testDevice = "01990000-6000-7000-8000-000000000001"
	// otherDevice has a sub-meter too, as does every device but unmeteredDevice.
	otherDevice     = "01990000-6000-7000-8000-000000000002"
	unmeteredDevice = "01990000-6000-7000-8000-000000000003"
)

type memoryStore struct {
	measures map[string]Measure
	results  map[string][]DailyResult
}

func newMemoryStore() *memoryStore {
	return &memoryStore{measures: map[string]Measure{}, results: map[string][]DailyResult{}}
}

func (store *memoryStore) InsertMeasure(_ context.Context, measure Measure) error {
	store.measures[measure.ID] = measure
	return nil
}

func (store *memoryStore) UpdateMeasure(_ context.Context, measure Measure) error {
	if _, ok := store.measures[measure.ID]; !ok {
		return ErrMeasureNotFound
	}
	store.measures[measure.ID] = measure
	return nil
}

func (store *memoryStore) Measure(_ context.Context, _, _, id string) (Measure, error) {
	measure, ok := store.measures[id]
	if !ok {
		return Measure{}, ErrMeasureNotFound
	}
	return measure, nil
}

func (store *memoryStore) Measures(_ context.Context, _, siteID string) ([]Measure, error) {
	var measures []Measure
	for _, measure := range store.measures {
		if measure.SiteID == siteID {
			measures = append(measures, measure)
		}
	}
	return measures, nil
}

func (store *memoryStore) InsertDailyResult(_ context.Context, result DailyResult) error {
	for _, existing := range store.results[result.MeasureID] {
		if existing.Day == result.Day {
			return nil
		}
	}
	store.results[result.MeasureID] = append(store.results[result.MeasureID], result)
	return nil
}

func (store *memoryStore) DailyResults(_ context.Context, _, _, measureID string) ([]DailyResult, error) {
	return store.results[measureID], nil
}

// plantHistory answers hourly samples from a plant that follows `energy` until `changedAt`,
// then uses `factor` of it on the measure's boundary. A device's sub-meter reads a fifth of the
// plant; the plant's own electricity is what it would be without the change on a device.
type plantHistory struct {
	changedAt time.Time
	factor    float64
	gapFrom   time.Time
	gapTo     time.Time
}

func (history plantHistory) HasDeviceEnergy(_ context.Context, _, _, deviceID string) (bool, error) {
	return deviceID != unmeteredDevice, nil
}

func (history plantHistory) HourlySamples(_ context.Context, _, _ string, boundaryDeviceID *string, from, to time.Time) ([]HourSample, error) {
	var samples []HourSample
	for at := from; at.Before(to); at = at.Add(time.Hour) {
		if !at.Before(history.gapFrom) && at.Before(history.gapTo) {
			continue
		}
		cooling := 300.0
		if hour := at.In(shanghai).Hour(); hour >= 8 && hour < 20 {
			cooling = 700 + 10*float64(hour-8) + 15*float64(at.In(shanghai).Day()%3)
		}
		plant := 20 + 0.25*cooling
		energy := plant
		if boundaryDeviceID != nil {
			energy = plant / 5
		}
		if !at.Before(history.changedAt) {
			energy *= history.factor
		}
		if boundaryDeviceID == nil {
			plant = energy
		}
		samples = append(samples, HourSample{Start: at, CoolingKWh: cooling, EnergyKWh: energy, PlantKWh: plant})
	}
	return samples, nil
}

type shanghaiClock struct{}

func (shanghaiClock) SiteClock(context.Context, string, string) (SiteClock, error) {
	return SiteClock{Location: shanghai}, nil
}

// hourlyRates stands in for the query service's hourly price of the Site's electricity.
type hourlyRates func(time.Time) (float64, bool)

func (rates hourlyRates) HourlyRates(context.Context, string, string, *time.Location, time.Time, time.Time) (func(time.Time) (float64, bool), error) {
	return rates, nil
}

func flatRate(rate float64) hourlyRates {
	return func(time.Time) (float64, bool) { return rate, true }
}

// unpriced is a rate source that prices no hour, as for a Site without an import Tariff.
var unpriced = hourlyRates(func(time.Time) (float64, bool) { return 0, false })

func newTestService(t *testing.T, store Store, history HourlySource) *Service {
	t.Helper()
	ids := 0
	service, err := NewService(store, history, shanghaiClock{}, flatRate(0.8), func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai) }, func(time.Time) (string, error) {
		ids++
		return []string{"", "01990000-7000-7000-8000-000000000001", "01990000-7000-7000-8000-000000000002", "01990000-7000-7000-8000-000000000003"}[ids], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func register(t *testing.T, service *Service, name string) Measure {
	t.Helper()
	measure, err := service.Register(t.Context(), MeasureInput{
		TenantID: testTenant, SiteID: testSite, Name: name, Kind: KindSetpointReset,
		PlannedAt: time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai), Actor: testActor,
	})
	if err != nil {
		t.Fatal(err)
	}
	return measure
}

func TestARegisteredMeasureIsPlannedWithoutABaseline(t *testing.T) {
	store := newMemoryStore()
	measure := register(t, newTestService(t, store, plantHistory{}), "提高冷冻水供水温度")
	if measure.Status != StatusPlanned || measure.Baseline != nil || measure.ImplementedAt != nil {
		t.Fatalf("measure = %+v", measure)
	}
	if _, err := newTestService(t, store, plantHistory{}).Register(t.Context(), MeasureInput{TenantID: testTenant, SiteID: testSite, Kind: KindSetpointReset, Actor: testActor}); err == nil {
		t.Fatal("a measure without a name was registered")
	}
}

func TestImplementingFreezesABaselineFromTheFourteenDaysBefore(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	measure := register(t, service, "提高冷冻水供水温度")
	implementedMeasure, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor)
	if err != nil {
		t.Fatal(err)
	}
	baseline := implementedMeasure.Baseline
	if implementedMeasure.Status != StatusImplemented || baseline == nil || !baseline.Trusted {
		t.Fatalf("measure = %+v", implementedMeasure)
	}
	if !baseline.From.Equal(implemented.Add(-DefaultBaselineDays*24*time.Hour)) || !baseline.To.Equal(implemented) || baseline.Samples != 14*24 {
		t.Fatalf("baseline period %v–%v, %d samples", baseline.From, baseline.To, baseline.Samples)
	}
}

func TestAnUntrustedBaselineCanBeRefittedUntilResultsArePublished(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	// The week before implementation has no data, so the default fourteen days fall short.
	history := plantHistory{changedAt: implemented, factor: 0.9, gapFrom: implemented.Add(-7 * 24 * time.Hour), gapTo: implemented}
	store := newMemoryStore()
	service := newTestService(t, store, history)
	measure := register(t, service, "提高冷冻水供水温度")
	untrusted, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor)
	if err != nil || untrusted.Baseline.Trusted || untrusted.Baseline.Reason != ReasonLowCoverage {
		t.Fatalf("measure = %+v err = %v", untrusted, err)
	}
	refitted, err := service.Refit(t.Context(), testTenant, testSite, measure.ID, implemented.Add(-14*24*time.Hour), implemented.Add(-7*24*time.Hour), testActor)
	if err != nil || !refitted.Baseline.Trusted {
		t.Fatalf("refit = %+v err = %v", refitted, err)
	}

	if err := service.VerifyDay(t.Context(), refitted, time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refit(t.Context(), testTenant, testSite, measure.ID, implemented.Add(-10*24*time.Hour), implemented.Add(-7*24*time.Hour), testActor); !errors.Is(err, ErrBaselineLocked) {
		t.Fatalf("refit after publishing = %v", err)
	}
}

func registerOn(t *testing.T, service *Service, name string, device *string) Measure {
	t.Helper()
	measure, err := service.Register(t.Context(), MeasureInput{
		TenantID: testTenant, SiteID: testSite, Name: name, Kind: KindControlSequence,
		PlannedAt: time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai), BoundaryDeviceID: device, Actor: testActor,
	})
	if err != nil {
		t.Fatal(err)
	}
	return measure
}

func TestADeviceMeasureIsVerifiedOnItsSubMeterAgainstPlantCooling(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.8})
	device := testDevice
	measure := registerOn(t, service, "冷冻泵降频", &device)
	measure, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor)
	if err != nil {
		t.Fatal(err)
	}
	if !measure.Baseline.Trusted || math.Abs(measure.Baseline.Slope-0.05) > 1e-9 {
		t.Fatalf("baseline = %+v, want the sub-meter's fifth of the plant's slope", measure.Baseline)
	}
	if err := service.VerifyDay(t.Context(), measure, time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	result := store.results[measure.ID][0]
	if share := result.AvoidedKWh / result.AdjustedBaselineKWh; math.Abs(share-0.2) > 1e-9 {
		t.Fatalf("avoided share = %v", share)
	}
	// The plant's electricity over the same hours is five times the device's adjusted baseline.
	if math.Abs(result.PlantKWh-5*result.AdjustedBaselineKWh) > 1e-6 {
		t.Fatalf("plant %v for adjusted baseline %v", result.PlantKWh, result.AdjustedBaselineKWh)
	}
}

func TestADeviceWithoutAnHourlyEnergyMetricCannotBoundAMeasure(t *testing.T) {
	service := newTestService(t, newMemoryStore(), plantHistory{})
	device := unmeteredDevice
	if _, err := service.Register(t.Context(), MeasureInput{
		TenantID: testTenant, SiteID: testSite, Name: "冷却塔风机变频", Kind: KindControlSequence,
		PlannedAt: time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai), BoundaryDeviceID: &device, Actor: testActor,
	}); !errors.Is(err, ErrBoundaryUnmetered) {
		t.Fatalf("register on an unmetered device = %v", err)
	}
}

// Measures overlap only when they would count the same energy: on one device, or beside the
// plant's total meter, which holds every sub-meter.
func TestReportingPeriodsOverlapOnlyAcrossDifferentDevices(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	device, other := testDevice, otherDevice
	for _, test := range []struct {
		name          string
		first, second *string
		overlaps      bool
	}{
		{"two devices", &device, &other, false},
		{"one device", &device, &device, true},
		{"plant then device", nil, &device, true},
		{"device then plant", &device, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newTestService(t, newMemoryStore(), plantHistory{changedAt: implemented, factor: 0.9})
			first := registerOn(t, service, "第一项", test.first)
			second := registerOn(t, service, "第二项", test.second)
			if _, err := service.Implement(t.Context(), testTenant, testSite, first.ID, implemented, testActor); err != nil {
				t.Fatal(err)
			}
			_, err := service.Implement(t.Context(), testTenant, testSite, second.ID, implemented.Add(48*time.Hour), testActor)
			if overlapped := errors.Is(err, ErrReportingOverlap); overlapped != test.overlaps || (err != nil && !overlapped) {
				t.Fatalf("second implementation = %v", err)
			}
		})
	}
}

func TestASiteSummaryCountsThePlantOnceADay(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	device, other := testDevice, otherDevice
	for _, boundary := range []*string{&device, &other} {
		measure := registerOn(t, service, "设备措施", boundary)
		measure, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.VerifyDay(t.Context(), measure, time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := service.Savings(t.Context(), testTenant, testSite, "2026-10-17", "2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	day := store.results["01990000-7000-7000-8000-000000000001"][0]
	if totals.Days != 1 || math.Abs(totals.AvoidedKWh-2*day.AvoidedKWh) > 1e-6 || math.Abs(totals.PlantKWh-day.PlantKWh) > 1e-6 {
		t.Fatalf("totals = %+v, day = %+v", totals, day)
	}
}

func TestAMeasureOnThePlantMeterReportsAlone(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	first := register(t, service, "提高冷冻水供水温度")
	second := register(t, service, "冷却塔风机变频")
	if _, err := service.Implement(t.Context(), testTenant, testSite, first.ID, implemented, testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Implement(t.Context(), testTenant, testSite, second.ID, implemented.Add(48*time.Hour), testActor); !errors.Is(err, ErrReportingOverlap) {
		t.Fatalf("overlapping implementation = %v", err)
	}
	if _, err := service.End(t.Context(), testTenant, testSite, first.ID, implemented.Add(24*time.Hour), testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Implement(t.Context(), testTenant, testSite, second.ID, implemented.Add(48*time.Hour), testActor); err != nil {
		t.Fatalf("after the first ended: %v", err)
	}
}

func TestAVerifiedDayRecordsAvoidedEnergyAndCostOnce(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	measure := register(t, service, "提高冷冻水供水温度")
	measure, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai)
	for range 2 {
		if err := service.VerifyDay(t.Context(), measure, day); err != nil {
			t.Fatal(err)
		}
	}
	results := store.results[measure.ID]
	if len(results) != 1 || results[0].Day != "2026-10-17" || results[0].Hours != 24 {
		t.Fatalf("results = %+v", results)
	}
	if avoided := results[0].AvoidedKWh / results[0].AdjustedBaselineKWh; avoided < 0.0999 || avoided > 0.1001 {
		t.Fatalf("avoided share = %v", avoided)
	}
	if cost := results[0].AvoidedCost; cost < results[0].AvoidedKWh*0.8-1e-6 || cost > results[0].AvoidedKWh*0.8+1e-6 {
		t.Fatalf("avoided cost %v for %v kWh", cost, results[0].AvoidedKWh)
	}
}

func TestTheDailyRunVerifiesEachSettledDayOnce(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	measure := register(t, service, "提高冷冻水供水温度")
	if _, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor); err != nil {
		t.Fatal(err)
	}
	// Reporting starts 10-16 09:00 after the stabilization day; at 10-20 02:00 the days through 10-19 have settled.
	for range 2 {
		if err := service.VerifyDueDays(t.Context(), testTenant, testSite, time.Date(2026, 10, 20, 2, 0, 0, 0, shanghai)); err != nil {
			t.Fatal(err)
		}
	}
	var days []string
	for _, result := range store.results[measure.ID] {
		days = append(days, result.Day)
	}
	if len(days) != 4 || days[0] != "2026-10-16" || days[3] != "2026-10-19" || store.results[measure.ID][0].ExpectedHours != 15 {
		t.Fatalf("verified days = %v", store.results[measure.ID])
	}

	if _, err := service.End(t.Context(), testTenant, testSite, measure.ID, time.Date(2026, 10, 20, 12, 0, 0, 0, shanghai), testActor); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyDueDays(t.Context(), testTenant, testSite, time.Date(2026, 10, 23, 2, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	results := store.results[measure.ID]
	if last := results[len(results)-1]; len(results) != 5 || last.Day != "2026-10-20" || last.ExpectedHours != 12 {
		t.Fatalf("after ending = %+v", results)
	}
}

func TestAnEndInsideAnHourStopsVerificationAtTheHourBefore(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service := newTestService(t, store, plantHistory{changedAt: implemented, factor: 0.9})
	measure := register(t, service, "提高冷冻水供水温度")
	if _, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.End(t.Context(), testTenant, testSite, measure.ID, time.Date(2026, 10, 17, 12, 30, 0, 0, shanghai), testActor); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyDueDays(t.Context(), testTenant, testSite, time.Date(2026, 10, 20, 2, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	results := store.results[measure.ID]
	last := results[len(results)-1]
	if last.Day != "2026-10-17" || last.ExpectedHours != 12 || last.Hours != 12 {
		t.Fatalf("last verified day = %+v", last)
	}
}

func TestATrustedBaselineCannotBeRefitted(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	service := newTestService(t, newMemoryStore(), plantHistory{changedAt: implemented, factor: 0.9})
	measure := register(t, service, "提高冷冻水供水温度")
	if _, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refit(t.Context(), testTenant, testSite, measure.ID, implemented.Add(-7*24*time.Hour), implemented, testActor); !errors.Is(err, ErrWrongStatus) {
		t.Fatalf("refit of a trusted baseline = %v", err)
	}
}

func TestHoursWithoutAPriceAreVerifiedUnpriced(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service, err := NewService(store, plantHistory{changedAt: implemented, factor: 0.9}, shanghaiClock{}, unpriced, func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai) }, func(time.Time) (string, error) {
		return "01990000-7000-7000-8000-000000000001", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	measure := register(t, service, "提高冷冻水供水温度")
	if _, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyDueDays(t.Context(), testTenant, testSite, time.Date(2026, 10, 18, 2, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	totals, err := service.Savings(t.Context(), testTenant, testSite, "2026-10-17", "2026-10-17")
	if err != nil {
		t.Fatal(err)
	}
	if totals.Days != 1 || totals.AvoidedKWh <= 0 || totals.AvoidedCost != 0 || totals.UnpricedHours != 24 {
		t.Fatalf("totals = %+v", totals)
	}
}

// Each hour is priced at the rate the query service gives that instant, whatever the Site's clock:
// here UTC 00:00–08:00 costs 1/kWh and every other hour nothing, while days split on Shanghai's.
func TestHoursArePricedAtTheirOwnRateAndDaysSplitOnTheSiteClock(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	firstEightUTCHours := hourlyRates(func(at time.Time) (float64, bool) {
		if at.UTC().Hour() < 8 {
			return 1, true
		}
		return 0, true
	})
	service, err := NewService(store, plantHistory{changedAt: implemented, factor: 0.9}, shanghaiClock{}, firstEightUTCHours, func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai) }, func(time.Time) (string, error) {
		return "01990000-7000-7000-8000-000000000001", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	measure := register(t, service, "提高冷冻水供水温度")
	if _, err := service.Implement(t.Context(), testTenant, testSite, measure.ID, implemented, testActor); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyDueDays(t.Context(), testTenant, testSite, time.Date(2026, 10, 18, 2, 0, 0, 0, shanghai)); err != nil {
		t.Fatal(err)
	}
	day := store.results[measure.ID][len(store.results[measure.ID])-1]
	// UTC 00:00–08:00 is Shanghai 08:00–16:00 on 10-17: cooling 730…800 kWh, a tenth of 20 + 0.25 × cooling avoided each hour.
	if day.Day != "2026-10-17" || day.Hours != 24 || math.Abs(day.AvoidedCost-169) > 1e-6 {
		t.Fatalf("day = %+v", day)
	}
}
