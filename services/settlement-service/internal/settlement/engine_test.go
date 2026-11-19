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

func ptrTime(value time.Time) *time.Time {
	return &value
}
