package settlement

import (
	"context"
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
