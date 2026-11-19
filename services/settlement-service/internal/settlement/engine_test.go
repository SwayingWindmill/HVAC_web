package settlement

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

type fakeRepo struct {
	period       Period
	tariff       Tariff
	bindings     []MetricBinding
	snapshot     Snapshot
	candidate    Candidate
	transitions  []string
	candidateIDs map[string]string
}

func (f *fakeRepo) LoadPeriod(context.Context, string, string, string) (Period, []MetricBinding, Tariff, error) {
	return f.period, f.bindings, f.tariff, nil
}
func (f *fakeRepo) TransitionPeriod(_ context.Context, _ Period, status string, _ time.Time) error {
	f.transitions = append(f.transitions, status)
	f.period.Status = status
	return nil
}
func (f *fakeRepo) InsertSnapshot(_ context.Context, snapshot Snapshot) error {
	f.snapshot = snapshot
	return nil
}
func (f *fakeRepo) LatestSnapshot(context.Context, string, string, string) (Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeRepo) CreateCandidate(_ context.Context, candidate Candidate, _ time.Time) (string, error) {
	if f.candidateIDs == nil {
		f.candidateIDs = map[string]string{}
	}
	key := candidate.BaseSnapshotID + "|" + candidate.CalculationDigest
	if id := f.candidateIDs[key]; id != "" {
		return id, nil
	}
	f.candidateIDs[key] = candidate.ID
	f.candidate = candidate
	return candidate.ID, nil
}
func (f *fakeRepo) ApproveCandidate(context.Context, string, string, string, time.Time) error {
	return nil
}
func (f *fakeRepo) LoadApprovedCandidate(context.Context, string, string, string) (Candidate, Snapshot, error) {
	return f.candidate, f.snapshot, nil
}
func (f *fakeRepo) ApplyRevision(_ context.Context, _ Candidate, _ Snapshot, next Snapshot, _ time.Time) error {
	f.snapshot = next
	return nil
}

type fakeFacts struct{ facts []Fact }

func (f *fakeFacts) ReadMetricFacts(context.Context, Period, []MetricBinding) ([]Fact, error) {
	return append([]Fact(nil), f.facts...), nil
}

func settlementFixture() (*fakeRepo, time.Time) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	return &fakeRepo{
		period: Period{TenantID: "t", SiteID: "s", ID: "p", BoundaryID: "b", Timezone: "UTC", Status: "OPEN", Start: start, End: start.Add(time.Hour)},
		bindings: []MetricBinding{
			{MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY"},
			{MetricBindingID: "demand-binding", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND"},
		},
		tariff: Tariff{VersionID: "tariff-v1", Currency: "CNY", Periods: []TariffPeriod{{Code: "FLAT", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 1440, EnergyRate: .5, DemandRate: 2}}},
	}, start
}

func TestCalculatePeriodPersistsSourceRevisionQualityAndWatermark(t *testing.T) {
	repo, start := settlementFixture()
	watermark := start.Add(70 * time.Minute)
	facts := &fakeFacts{facts: []Fact{
		{ID: "energy-result", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY", Start: start, End: start.Add(time.Hour), CalculatedAt: start.Add(65 * time.Minute), Value: 10, Revision: 3, Quality: "GOOD", Completeness: 1},
		{ID: "demand-result", MetricBindingID: "demand-binding", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND", Start: start, End: start.Add(time.Hour), CalculatedAt: watermark, Value: 10, Revision: 7, Quality: "GOOD", Completeness: 1},
	}}
	engine, _ := New(repo, facts)
	engine.now = func() time.Time { return start.Add(2 * time.Hour) }

	got, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Calculation.TotalCost != 25 || got.Calculation.Quality != "GOOD" || got.Calculation.Completeness != 1 {
		t.Fatalf("calculation=%#v", got.Calculation)
	}
	if got.DatasetRevision != 1 || got.Calculation.SourceMetricRevisions["energy-binding"] != 3 || got.Calculation.SourceMetricRevisions["demand-binding"] != 7 {
		t.Fatalf("revision evidence=%#v", got)
	}
	if !got.Calculation.SourceWatermark.Equal(watermark) || len(got.Calculation.MissingMetricBindingRefs) != 0 {
		t.Fatalf("watermark/missing=%#v", got.Calculation)
	}
}

func TestCalculatePeriodReviewReusesCurrentSnapshotWhenInputsAreUnchanged(t *testing.T) {
	repo, start := settlementFixture()
	facts := &fakeFacts{facts: []Fact{
		{ID: "energy-result", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY", Start: start, End: start.Add(time.Hour), CalculatedAt: start.Add(time.Hour), Value: 10, Revision: 1, Quality: "GOOD", Completeness: 1},
		{ID: "demand-result", MetricBindingID: "demand-binding", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND", Start: start, End: start.Add(time.Hour), CalculatedAt: start.Add(time.Hour), Value: 10, Revision: 1, Quality: "GOOD", Completeness: 1},
	}}
	engine, _ := New(repo, facts)
	engine.now = func() time.Time { return start.Add(2 * time.Hour) }

	first, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.DatasetRevision != 1 || second.RevisionNo != 0 {
		t.Fatalf("repeated calculate changed snapshot identity/revision: first=%#v second=%#v", first, second)
	}
}

func TestCalculatePeriodMakesMissingAndPartialSourceQualityVisible(t *testing.T) {
	repo, start := settlementFixture()
	facts := &fakeFacts{facts: []Fact{{
		ID: "energy-result", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
		Start: start, End: start.Add(time.Hour), CalculatedAt: start.Add(time.Hour), Value: 10, Revision: 2, Quality: "PARTIAL", Completeness: .8,
	}}}
	engine, _ := New(repo, facts)
	engine.now = func() time.Time { return start.Add(2 * time.Hour) }

	got, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Calculation.Quality != "PARTIAL" || got.Calculation.Completeness != .4 {
		t.Fatalf("quality=%s completeness=%v", got.Calculation.Quality, got.Calculation.Completeness)
	}
	if len(got.Calculation.MissingMetricBindingRefs) != 1 || got.Calculation.MissingMetricBindingRefs[0] != "demand-binding" {
		t.Fatalf("missing=%v", got.Calculation.MissingMetricBindingRefs)
	}
}

func TestReconcileIsIdempotentAndAppliedSnapshotAdvancesDatasetRevision(t *testing.T) {
	repo, start := settlementFixture()
	repo.period.Status = "LOCKED"
	repo.snapshot = Snapshot{
		ID: "base", RevisionNo: 0, DatasetRevision: 1, Period: repo.period, CreatedAt: start,
		Calculation: Calculation{TotalCost: 5, Currency: "CNY", Quality: "GOOD", Completeness: 1, SourceMetricRevisions: map[string]uint64{"energy-binding": 1}},
	}
	facts := &fakeFacts{facts: []Fact{
		{ID: "energy-result", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY", Start: start, End: start.Add(time.Hour), CalculatedAt: start.Add(2 * time.Hour), Value: 12, Revision: 2, Quality: "GOOD", Completeness: 1},
		{ID: "demand-result", MetricBindingID: "demand-binding", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND", Start: start, End: start.Add(time.Hour), CalculatedAt: start.Add(2 * time.Hour), Value: 10, Revision: 2, Quality: "GOOD", Completeness: 1},
	}}
	engine, _ := New(repo, facts)
	engine.now = func() time.Time { return start.Add(3 * time.Hour) }

	first, err := engine.ReconcilePeriod(context.Background(), "t", "s", "p", "LATE_DATA")
	if err != nil || first == "" {
		t.Fatalf("first candidate=%q err=%v", first, err)
	}
	second, err := engine.ReconcilePeriod(context.Background(), "t", "s", "p", "LATE_DATA")
	if err != nil || second != first {
		t.Fatalf("second candidate=%q first=%q err=%v", second, first, err)
	}
	applied, err := engine.ApplyApprovedRevision(context.Background(), "t", "s", first)
	if err != nil {
		t.Fatal(err)
	}
	if applied.RevisionNo != 1 || applied.DatasetRevision != 2 || applied.PreviousSnapshotID != "base" {
		t.Fatalf("applied=%#v", applied)
	}
}

func shanghaiLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	return loc
}

func TestCalculatePeriodSupportsMidPeriodTariffVersionSwitchAndAsiaShanghaiBoundaries(t *testing.T) {
	shanghai := shanghaiLocation(t)
	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, shanghai)
	switchAt := time.Date(2026, 8, 16, 0, 0, 0, 0, shanghai)
	periodEnd := time.Date(2026, 9, 1, 0, 0, 0, 0, shanghai)

	repo := &fakeRepo{
		period: Period{
			TenantID: "t", SiteID: "s", ID: "p-aug", BoundaryID: "b",
			Timezone: "Asia/Shanghai", Status: "OPEN", Start: periodStart.UTC(), End: periodEnd.UTC(),
		},
		bindings: []MetricBinding{
			{MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY"},
			{MetricBindingID: "energy-peak", MetricVersionID: "energy-v1", MetricCode: "energy_peak", Role: "ENERGY", TariffPeriodCode: "PEAK"},
			{MetricBindingID: "demand-max", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND"},
		},
		tariff: Tariff{
			ID: "tariff-1", VersionID: "tariff-v1", Version: 1, Currency: "CNY", Timezone: "Asia/Shanghai",
			Versions: []TariffVersion{
				{
					ID: "tariff-1", VersionID: "tariff-v1", Version: 1, Currency: "CNY", Timezone: "Asia/Shanghai",
					EffectiveFrom: periodStart.UTC(), EffectiveTo: ptrTime(switchAt.UTC()),
					Periods: []TariffPeriod{
						{Code: "VALLEY", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 480, EnergyRate: 0.4, DemandRate: 0},
						{Code: "PEAK", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 1440, EnergyRate: 1.0, DemandRate: 10},
						{Code: "WEEKEND_VALLEY", DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRate: 0.3, DemandRate: 0},
					},
				},
				{
					ID: "tariff-1", VersionID: "tariff-v2", Version: 2, Currency: "CNY", Timezone: "Asia/Shanghai",
					EffectiveFrom: switchAt.UTC(), EffectiveTo: nil,
					Periods: []TariffPeriod{
						{Code: "VALLEY", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 480, EnergyRate: 0.6, DemandRate: 0},
						{Code: "PEAK", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 1440, EnergyRate: 1.5, DemandRate: 14},
						{Code: "WEEKEND_VALLEY", DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRate: 0.5, DemandRate: 0},
					},
				},
			},
		},
	}

	facts := &fakeFacts{facts: []Fact{
		// Monday 2026-08-03 08:30 Asia/Shanghai == 2026-08-03T00:30Z (UTC midnight, Shanghai PEAK) -> V1 PEAK (1.0)
		{
			ID: "f-sh-weekday-peak", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: time.Date(2026, 8, 3, 0, 30, 0, 0, time.UTC), End: time.Date(2026, 8, 3, 1, 30, 0, 0, time.UTC),
			CalculatedAt: switchAt, Value: 10, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
		// Saturday 2026-08-08 00:00 Asia/Shanghai == 2026-08-07T16:00Z (Friday UTC, Saturday Shanghai WEEKEND) -> V1 WEEKEND_VALLEY (0.3)
		{
			ID: "f-sh-weekend-midnight", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: time.Date(2026, 8, 7, 16, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 7, 17, 0, 0, 0, time.UTC),
			CalculatedAt: switchAt, Value: 20, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
		// Straddles switchAt (2026-08-15 23:00+08 to 2026-08-16 01:00+08): 1h pre-bound PEAK in V1 (1.0) + 1h in V2 (1.5)
		{
			ID: "f-straddle-switch", MetricBindingID: "energy-peak", MetricVersionID: "energy-v1", MetricCode: "energy_peak", Role: "ENERGY",
			TariffPeriodCode: "PEAK",
			Start:            switchAt.Add(-time.Hour).UTC(), End: switchAt.Add(time.Hour).UTC(),
			CalculatedAt: periodEnd, Value: 10, Revision: 2, Quality: "GOOD", Completeness: 1,
		},
		// Sunday 2026-08-16 00:00 Asia/Shanghai == exactly switchAt -> V2 WEEKEND_VALLEY (0.5)
		{
			ID: "f-v2-start", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: switchAt.UTC(), End: switchAt.Add(time.Hour).UTC(),
			CalculatedAt: periodEnd, Value: 12, Revision: 3, Quality: "GOOD", Completeness: 1,
		},
		// Demand during V2 WEEKDAY PEAK (Monday 2026-08-17 09:00+08): 100 kW * 14 = 1400
		{
			ID: "f-demand-v2", MetricBindingID: "demand-max", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND",
			Start: time.Date(2026, 8, 17, 9, 0, 0, 0, shanghai).UTC(), End: time.Date(2026, 8, 17, 10, 0, 0, 0, shanghai).UTC(),
			CalculatedAt: periodEnd, Value: 100, Revision: 4, Quality: "GOOD", Completeness: 1,
		},
	}}

	engine, err := New(repo, facts)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return periodEnd.Add(time.Hour) }

	got, err := engine.CalculatePeriod(context.Background(), "t", "s", "p-aug")
	if err != nil {
		t.Fatalf("CalculatePeriod: %v", err)
	}

	calc := got.Calculation
	if diff := math.Abs(calc.EnergyBreakdown["PEAK"] - 20); diff > 1e-9 {
		t.Fatalf("PEAK energy=%v want 20", calc.EnergyBreakdown["PEAK"])
	}
	if diff := math.Abs(calc.EnergyBreakdown["WEEKEND_VALLEY"] - 32); diff > 1e-9 {
		t.Fatalf("WEEKEND_VALLEY energy=%v want 32", calc.EnergyBreakdown["WEEKEND_VALLEY"])
	}
	// PEAK cost: 10*1.0 + 5*1.0 + 5*1.5 = 22.5
	if diff := math.Abs(calc.EnergyCost["PEAK"] - 22.5); diff > 1e-9 {
		t.Fatalf("PEAK energy cost=%v want 22.5", calc.EnergyCost["PEAK"])
	}
	// WEEKEND_VALLEY cost: 20*0.3 + 12*0.5 = 12.0
	if diff := math.Abs(calc.EnergyCost["WEEKEND_VALLEY"] - 12.0); diff > 1e-9 {
		t.Fatalf("WEEKEND_VALLEY energy cost=%v want 12.0", calc.EnergyCost["WEEKEND_VALLEY"])
	}
	if diff := math.Abs(calc.DemandCost["PEAK"] - 1400); diff > 1e-9 {
		t.Fatalf("PEAK demand cost=%v want 1400", calc.DemandCost["PEAK"])
	}
	if diff := math.Abs(calc.TotalCost - 1434.5); diff > 1e-9 {
		t.Fatalf("TotalCost=%v want 1434.5", calc.TotalCost)
	}
	if len(calc.TariffVersionRefs) != 2 || calc.TariffVersionRefs[0] != "tariff-v1" || calc.TariffVersionRefs[1] != "tariff-v2" {
		t.Fatalf("TariffVersionRefs=%v want [tariff-v1 tariff-v2]", calc.TariffVersionRefs)
	}
}

func TestCalculatePeriodRejectsImplicitTimezoneTariffGapOverlapAndNonFiniteFact(t *testing.T) {
	shanghai := shanghaiLocation(t)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, shanghai)

	t.Run("rejects empty or Local timezone", func(t *testing.T) {
		for _, tz := range []string{"", "Local", "Mars/Olympus"} {
			repo, factStart := settlementFixture()
			repo.period.Timezone = tz
			facts := &fakeFacts{facts: []Fact{{
				ID: "e1", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
				Start: factStart, End: factStart.Add(time.Hour), CalculatedAt: factStart, Value: 10, Revision: 1, Quality: "GOOD", Completeness: 1,
			}}}
			engine, _ := New(repo, facts)
			if _, err := engine.CalculatePeriod(context.Background(), "t", "s", "p"); err == nil {
				t.Fatalf("expected timezone %q error, got nil", tz)
			}
		}
	})

	t.Run("rejects gap between tariff versions", func(t *testing.T) {
		repo, _ := settlementFixture()
		repo.period.Timezone = "Asia/Shanghai"
		repo.period.Start = start
		repo.period.End = start.Add(48 * time.Hour)
		v1End := start.Add(12 * time.Hour)
		v2Start := start.Add(24 * time.Hour)
		repo.tariff.Versions = []TariffVersion{
			{ID: "t1", VersionID: "v1", Version: 1, Currency: "CNY", EffectiveFrom: start, EffectiveTo: &v1End, Periods: repo.tariff.Periods},
			{ID: "t1", VersionID: "v2", Version: 2, Currency: "CNY", EffectiveFrom: v2Start, Periods: repo.tariff.Periods},
		}
		facts := &fakeFacts{facts: []Fact{{
			ID: "e-gap", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: start.Add(15 * time.Hour), End: start.Add(16 * time.Hour), CalculatedAt: start, Value: 10, Revision: 1, Quality: "GOOD", Completeness: 1,
		}}}
		engine, _ := New(repo, facts)
		_, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
		if err == nil || !strings.Contains(err.Error(), "no released tariff version") {
			t.Fatalf("expected tariff gap error, got %v", err)
		}
	})

	t.Run("rejects overlapping tariff versions", func(t *testing.T) {
		repo, _ := settlementFixture()
		repo.period.Timezone = "Asia/Shanghai"
		repo.period.Start = start
		repo.period.End = start.Add(48 * time.Hour)
		v1End := start.Add(24 * time.Hour)
		v2Start := start.Add(12 * time.Hour)
		repo.tariff.Versions = []TariffVersion{
			{ID: "t1", VersionID: "v1", Version: 1, Currency: "CNY", EffectiveFrom: start, EffectiveTo: &v1End, Periods: repo.tariff.Periods},
			{ID: "t1", VersionID: "v2", Version: 2, Currency: "CNY", EffectiveFrom: v2Start, Periods: repo.tariff.Periods},
		}
		facts := &fakeFacts{}
		engine, _ := New(repo, facts)
		_, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
		if err == nil || !strings.Contains(err.Error(), "overlapping released tariff versions") {
			t.Fatalf("expected overlapping tariff error, got %v", err)
		}
	})

	t.Run("rejects NaN fact values", func(t *testing.T) {
		repo, factStart := settlementFixture()
		facts := &fakeFacts{facts: []Fact{{
			ID: "e-nan", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: factStart, End: factStart.Add(time.Hour), CalculatedAt: factStart, Value: math.NaN(), Revision: 1, Quality: "GOOD", Completeness: 1,
		}}}
		engine, _ := New(repo, facts)
		_, err := engine.CalculatePeriod(context.Background(), "t", "s", "p")
		if err == nil || !strings.Contains(err.Error(), "non-finite") {
			t.Fatalf("expected non-finite error, got %v", err)
		}
	})
}

func TestCalculatePeriodSupportsFiveTierTOUDeepValleyAndHolidayCalendar(t *testing.T) {
	shanghai := shanghaiLocation(t)
	// 2026-10-01 Thursday (HOLIDAY), 2026-10-02 Friday (SPECIAL_DAY), 2026-10-03 Saturday (WEEKDAY override), 2026-10-04 Sunday (normal WEEKEND).
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)
	end := start.AddDate(0, 0, 4)

	repo := &fakeRepo{
		period: Period{
			TenantID: "t", SiteID: "s", ID: "p-holiday", BoundaryID: "b",
			Timezone: "Asia/Shanghai", Status: "OPEN", Start: start.UTC(), End: end.UTC(),
		},
		bindings: []MetricBinding{
			{MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY"},
			{MetricBindingID: "demand-max", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND"},
		},
		tariff: Tariff{
			ID: "tariff-5tier", VersionID: "v1", Version: 1, Currency: "CNY", Timezone: "Asia/Shanghai",
			EffectiveFrom: start.UTC(),
			Holidays: map[string]string{
				"2026-10-01": "HOLIDAY",
				"2026-10-02": "SPECIAL_DAY",
				"2026-10-03": "WEEKDAY",
			},
			Periods: []TariffPeriod{
				// WEEKDAY 5-tier TOU
				{Code: "DEEP_VALLEY", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 240, EnergyRate: 0.15},
				{Code: "VALLEY", DayType: "WEEKDAY", StartMinute: 240, EndMinute: 480, EnergyRate: 0.30},
				{Code: "FLAT", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 660, EnergyRate: 0.70},
				{Code: "PEAK", DayType: "WEEKDAY", StartMinute: 660, EndMinute: 840, EnergyRate: 1.10, DemandRate: 30},
				{Code: "SUPER_PEAK", DayType: "WEEKDAY", StartMinute: 840, EndMinute: 1440, EnergyRate: 1.45, DemandRate: 40},
				// WEEKEND fallback for SPECIAL_DAY (no SPECIAL_DAY periods defined)
				{Code: "VALLEY", DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRate: 0.35},
				// Explicit HOLIDAY periods
				{Code: "DEEP_VALLEY", DayType: "HOLIDAY", StartMinute: 0, EndMinute: 1440, EnergyRate: 0.12},
			},
		},
	}

	facts := &fakeFacts{facts: []Fact{
		// Oct 1 10:00+08 (HOLIDAY -> DEEP_VALLEY @ 0.12): 100 kWh -> 12.00 CNY
		{
			ID: "f-holiday", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: time.Date(2026, 10, 1, 10, 0, 0, 0, shanghai).UTC(), End: time.Date(2026, 10, 1, 11, 0, 0, 0, shanghai).UTC(),
			CalculatedAt: end.UTC(), Value: 100, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
		// Oct 2 10:00+08 (SPECIAL_DAY with no SPECIAL_DAY periods -> falls back to WEEKEND VALLEY @ 0.35): 40 kWh -> 14.00 CNY
		{
			ID: "f-special-fallback", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: time.Date(2026, 10, 2, 10, 0, 0, 0, shanghai).UTC(), End: time.Date(2026, 10, 2, 11, 0, 0, 0, shanghai).UTC(),
			CalculatedAt: end.UTC(), Value: 40, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
		// Oct 3 Saturday 01:00+08 (WEEKDAY override -> DEEP_VALLEY @ 0.15): 50 kWh -> 7.50 CNY
		{
			ID: "f-sat-deep-valley", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: time.Date(2026, 10, 3, 1, 0, 0, 0, shanghai).UTC(), End: time.Date(2026, 10, 3, 2, 0, 0, 0, shanghai).UTC(),
			CalculatedAt: end.UTC(), Value: 50, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
		// Oct 3 Saturday 15:00+08 (WEEKDAY override -> SUPER_PEAK @ 1.45): 20 kWh -> 29.00 CNY
		{
			ID: "f-sat-super-peak", MetricBindingID: "energy-auto", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: time.Date(2026, 10, 3, 15, 0, 0, 0, shanghai).UTC(), End: time.Date(2026, 10, 3, 16, 0, 0, 0, shanghai).UTC(),
			CalculatedAt: end.UTC(), Value: 20, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
		// Demand on Oct 3 15:00+08 (SUPER_PEAK demand @ 40): 10 kW -> 400.00 CNY
		{
			ID: "f-sat-demand", MetricBindingID: "demand-max", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND",
			Start: time.Date(2026, 10, 3, 15, 0, 0, 0, shanghai).UTC(), End: time.Date(2026, 10, 3, 16, 0, 0, 0, shanghai).UTC(),
			CalculatedAt: end.UTC(), Value: 10, Revision: 1, Quality: "GOOD", Completeness: 1,
		},
	}}

	engine, err := New(repo, facts)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return end.Add(time.Hour).UTC() }

	got, err := engine.CalculatePeriod(context.Background(), "t", "s", "p-holiday")
	if err != nil {
		t.Fatalf("CalculatePeriod: %v", err)
	}
	calc := got.Calculation
	if calc.EnergyBreakdown["DEEP_VALLEY"] != 150 || calc.EnergyCost["DEEP_VALLEY"] != 19.5 {
		t.Fatalf("DEEP_VALLEY = (%v kWh, %v CNY), want (150, 19.5)", calc.EnergyBreakdown["DEEP_VALLEY"], calc.EnergyCost["DEEP_VALLEY"])
	}
	if calc.EnergyBreakdown["VALLEY"] != 40 || calc.EnergyCost["VALLEY"] != 14 {
		t.Fatalf("VALLEY = (%v kWh, %v CNY), want (40, 14)", calc.EnergyBreakdown["VALLEY"], calc.EnergyCost["VALLEY"])
	}
	if calc.EnergyBreakdown["SUPER_PEAK"] != 20 || calc.EnergyCost["SUPER_PEAK"] != 29 {
		t.Fatalf("SUPER_PEAK = (%v kWh, %v CNY), want (20, 29)", calc.EnergyBreakdown["SUPER_PEAK"], calc.EnergyCost["SUPER_PEAK"])
	}
	if calc.DemandCost["SUPER_PEAK"] != 400 {
		t.Fatalf("SUPER_PEAK demand cost = %v, want 400", calc.DemandCost["SUPER_PEAK"])
	}
	// Total: 19.50 + 14.00 + 29.00 + 400.00 = 462.50
	if calc.TotalCost != 462.5 {
		t.Fatalf("TotalCost = %v, want 462.5", calc.TotalCost)
	}
}

func TestCalculatePeriodFixedPointEliminatesFloatDriftAndRoundsHalfUp(t *testing.T) {
	shanghai := shanghaiLocation(t)
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, shanghai)
	end := start.Add(24 * time.Hour)

	repo := &fakeRepo{
		period: Period{
			TenantID: "t", SiteID: "s", ID: "p-fp", BoundaryID: "b",
			Timezone: "Asia/Shanghai", Status: "OPEN", Start: start.UTC(), End: end.UTC(),
		},
		bindings: []MetricBinding{
			{MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY"},
			{MetricBindingID: "demand-binding", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND"},
		},
		tariff: Tariff{
			VersionID: "tariff-fp", Currency: "CNY", Timezone: "Asia/Shanghai",
			Periods: []TariffPeriod{
				// 0.015 * 0.1 kWh/interval = 0.0015 CNY per interval; 100 intervals = 0.15 CNY exact.
				// Also 1.005 CNY subtotal rounds half-up to 1.01 CNY.
				{Code: "DEEP_VALLEY", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 240, EnergyRate: 0.1},
				{Code: "FLAT", DayType: "WEEKDAY", StartMinute: 240, EndMinute: 1440, EnergyRate: 0.5, DemandRate: 1},
			},
		},
	}

	facts := make([]Fact, 0, 102)
	// 100 tiny intervals of 0.1 kWh + 0.2 kWh alternating at rate 0.1 CNY/kWh = 15.0 kWh * 0.1 = 1.50 CNY exact.
	for i := 0; i < 100; i++ {
		val := 0.1
		if i%2 == 1 {
			val = 0.2
		}
		t0 := start.Add(time.Duration(i) * time.Minute).UTC()
		facts = append(facts, Fact{
			ID: "e-tiny", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
			Start: t0, End: t0.Add(time.Minute), CalculatedAt: end.UTC(), Value: val, Revision: 1, Quality: "GOOD", Completeness: 1,
		})
	}
	// FLAT: 2.01 kWh * 0.5 = 1.005 CNY -> half-up rounds subtotal to 1.01 CNY.
	facts = append(facts, Fact{
		ID: "e-halfup", MetricBindingID: "energy-binding", MetricVersionID: "energy-v1", MetricCode: "energy", Role: "ENERGY",
		Start: start.Add(5 * time.Hour).UTC(), End: start.Add(6 * time.Hour).UTC(), CalculatedAt: end.UTC(), Value: 2.01, Revision: 1, Quality: "GOOD", Completeness: 1,
	})
	// DEMAND: 10.005 kW * 1 = 10.005 CNY -> half-up rounds subtotal to 10.01 CNY.
	facts = append(facts, Fact{
		ID: "d-halfup", MetricBindingID: "demand-binding", MetricVersionID: "demand-v1", MetricCode: "demand", Role: "DEMAND",
		Start: start.Add(5 * time.Hour).UTC(), End: start.Add(6 * time.Hour).UTC(), CalculatedAt: end.UTC(), Value: 10.005, Revision: 1, Quality: "GOOD", Completeness: 1,
	})

	engine, err := New(repo, &fakeFacts{facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return end.Add(time.Hour).UTC() }

	got, err := engine.CalculatePeriod(context.Background(), "t", "s", "p-fp")
	if err != nil {
		t.Fatalf("CalculatePeriod: %v", err)
	}
	calc := got.Calculation
	if calc.EnergyBreakdown["DEEP_VALLEY"] != 15 {
		t.Fatalf("DEEP_VALLEY energy = %v, want exact 15", calc.EnergyBreakdown["DEEP_VALLEY"])
	}
	if calc.EnergyCost["DEEP_VALLEY"] != 1.5 {
		t.Fatalf("DEEP_VALLEY cost = %v, want exact 1.5", calc.EnergyCost["DEEP_VALLEY"])
	}
	if calc.EnergyCost["FLAT"] != 1.01 {
		t.Fatalf("FLAT energy cost = %v, want 1.01 (half-up from 1.005)", calc.EnergyCost["FLAT"])
	}
	if calc.DemandCost["FLAT"] != 10.01 {
		t.Fatalf("FLAT demand cost = %v, want 10.01 (half-up from 10.005)", calc.DemandCost["FLAT"])
	}
	// Total: 1.50 + 1.01 + 10.01 = 12.52
	if calc.TotalCost != 12.52 {
		t.Fatalf("TotalCost = %v, want 12.52", calc.TotalCost)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
