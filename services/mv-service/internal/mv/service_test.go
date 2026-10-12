package mv

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	testTenant = "01990000-3000-7000-8000-000000000001"
	testSite   = "01990000-5000-7000-8000-000000000001"
	testActor  = "01990000-2000-7000-8000-000000000001"
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
// then uses `factor` of it.
type plantHistory struct {
	changedAt time.Time
	factor    float64
	gapFrom   time.Time
	gapTo     time.Time
}

func (history plantHistory) HourlySamples(_ context.Context, _, _ string, from, to time.Time) ([]HourSample, error) {
	var samples []HourSample
	for at := from; at.Before(to); at = at.Add(time.Hour) {
		if !at.Before(history.gapFrom) && at.Before(history.gapTo) {
			continue
		}
		cooling := 300.0
		if hour := at.In(shanghai).Hour(); hour >= 8 && hour < 20 {
			cooling = 700 + 10*float64(hour-8) + 15*float64(at.In(shanghai).Day()%3)
		}
		energy := 20 + 0.25*cooling
		if !at.Before(history.changedAt) {
			energy *= history.factor
		}
		samples = append(samples, HourSample{Start: at, CoolingKWh: cooling, EnergyKWh: energy})
	}
	return samples, nil
}

type flatTariff struct{ rate float64 }

func (tariff flatTariff) SiteClock(_ context.Context, _, _ string) (SiteClock, error) {
	return SiteClock{Location: shanghai, Periods: []TariffPeriod{
		{DayType: "WEEKDAY", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: tariff.rate},
		{DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: tariff.rate},
	}}, nil
}

func newTestService(t *testing.T, store Store, history HourlySource) *Service {
	t.Helper()
	ids := 0
	service, err := NewService(store, history, flatTariff{rate: 0.8}, func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai) }, func(time.Time) (string, error) {
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

func TestOnlyOneMeasureReportsOnASiteAtATime(t *testing.T) {
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

type unpricedClock struct{}

func (unpricedClock) SiteClock(context.Context, string, string) (SiteClock, error) {
	return SiteClock{Location: shanghai}, nil
}

func TestASiteWithoutATariffIsVerifiedWithItsHoursUnpriced(t *testing.T) {
	implemented := time.Date(2026, 10, 15, 9, 0, 0, 0, shanghai)
	store := newMemoryStore()
	service, err := NewService(store, plantHistory{changedAt: implemented, factor: 0.9}, unpricedClock{}, func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, shanghai) }, func(time.Time) (string, error) {
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
