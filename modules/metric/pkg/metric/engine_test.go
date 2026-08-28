package metric

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRegistry struct {
	binding          Binding
	nextRevision     uint64
	stale            []Result
	completed        []Result
	runResultIDs     map[string]string
	beginFailures    int
	publicationCalls []Result
}

func (f *fakeRegistry) LoadBinding(context.Context, string, string, string, time.Time) (Binding, error) {
	return f.binding, nil
}

func (f *fakeRegistry) LoadCurrentMetricInput(context.Context, Binding, Dependency, time.Time, time.Time) (Input, error) {
	return Input{}, errors.New("unexpected Metric dependency")
}

func (f *fakeRegistry) CreateRun(_ context.Context, result Result, _ string, _ []byte) (Result, error) {
	if f.runResultIDs == nil {
		f.runResultIDs = map[string]string{}
	}
	if stored, ok := f.runResultIDs[result.RunID]; ok {
		result.ResultID = stored
		return result, nil
	}
	f.runResultIDs[result.RunID] = result.ResultID
	return result, nil
}
func (f *fakeRegistry) MarkRunRunning(context.Context, string, string, string, time.Time) error {
	return nil
}

func (f *fakeRegistry) BeginPublication(_ context.Context, result Result, _ time.Time) (Result, error) {
	f.publicationCalls = append(f.publicationCalls, result)
	if f.beginFailures > 0 {
		f.beginFailures--
		return Result{}, errors.New("begin publication failed")
	}
	f.nextRevision++
	result.Revision = f.nextRevision
	return result, nil
}

func (f *fakeRegistry) CompletePublication(_ context.Context, result Result, _ time.Time) error {
	f.completed = append(f.completed, result)
	return nil
}

func (f *fakeRegistry) FailRun(context.Context, Result, string, time.Time) error { return nil }

func (f *fakeRegistry) ListStalePublications(context.Context, time.Time, int) ([]Result, error) {
	return append([]Result(nil), f.stale...), nil
}

type fakeSeries struct {
	inserted []Result
	existing map[string]bool
}

func (f *fakeSeries) ReadPoint(context.Context, Binding, string, time.Time, time.Time) (Input, error) {
	return Input{Reference: "point:p", Value: 20, FirstValue: 20, LastValue: 20, Count: 1, Quality: "GOOD", Completeness: 1}, nil
}

func (f *fakeSeries) ReadMetricResult(_ context.Context, result Result) (Result, error) {
	for _, stored := range f.inserted {
		if stored.ResultID == result.ResultID {
			return stored, nil
		}
	}
	if f.existing[result.ResultID] {
		return result, nil
	}
	return Result{}, errors.New("Metric Result fact is unavailable")
}

func (f *fakeSeries) InsertMetric(_ context.Context, result Result) error {
	f.inserted = append(f.inserted, result)
	if f.existing == nil {
		f.existing = map[string]bool{}
	}
	f.existing[result.ResultID] = true
	return nil
}

func (f *fakeSeries) HasMetricResult(_ context.Context, resultID string) (bool, error) {
	return f.existing[resultID], nil
}

type fakeLatest struct{ results []Result }

func (f *fakeLatest) PutMetric(_ context.Context, result Result) error {
	f.results = append(f.results, result)
	return nil
}

func TestRepeatedRecalculationPreservesFactsAndAdvancesRevision(t *testing.T) {
	registry := &fakeRegistry{binding: Binding{
		TenantID: "tenant", SiteID: "site", BindingID: "binding", MetricVersionID: "version", MetricID: "metric",
		MetricCode: "load", MetricVersion: 1, BindingVersion: 1, SubjectType: "SITE", SubjectID: "site",
		Granularity: "HOUR", DataType: "NUMBER", CalculationMethod: "IDENTITY", QualityPolicy: "STRICT",
		SourceDefinition: map[string]any{"points": map[string]any{"load": "point"}},
		Dependencies:     []Dependency{{Type: "POINT", Code: "load", Required: true}},
	}}
	series := &fakeSeries{}
	latest := &fakeLatest{}
	engine, err := New(registry, series, latest)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC) }
	start := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	request := RunRequest{TenantID: "tenant", SiteID: "site", BindingID: "binding", PeriodStart: start, PeriodEnd: start.Add(time.Hour), Reason: "LATE_DATA"}

	first, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResultID == second.ResultID {
		t.Fatal("recalculation reused a historical Result ID")
	}
	if first.Revision != 1 || second.Revision != 2 {
		t.Fatalf("revisions = %d, %d; want 1, 2", first.Revision, second.Revision)
	}
	if len(series.inserted) != 2 || series.inserted[0].ResultID != first.ResultID || series.inserted[1].ResultID != second.ResultID {
		t.Fatalf("append-only facts = %#v", series.inserted)
	}
	if len(registry.completed) != 2 || registry.completed[1].Revision != 2 {
		t.Fatalf("completed publications = %#v", registry.completed)
	}
	if len(latest.results) != 2 || latest.results[1].Revision != 2 {
		t.Fatalf("latest projections = %#v", latest.results)
	}
}

func TestSchedulerRetryReusesRunAndResultIdentityBeforePublication(t *testing.T) {
	registry := &fakeRegistry{binding: Binding{
		TenantID: "tenant", SiteID: "site", BindingID: "binding", MetricVersionID: "version", MetricID: "metric",
		MetricCode: "load", MetricVersion: 1, BindingVersion: 1, SubjectType: "SITE", SubjectID: "site",
		Granularity: "HOUR", DataType: "NUMBER", CalculationMethod: "IDENTITY", QualityPolicy: "STRICT",
		SourceDefinition: map[string]any{"points": map[string]any{"load": "point"}},
		Dependencies:     []Dependency{{Type: "POINT", Code: "load", Required: true}},
	}, beginFailures: 1}
	engine, err := New(registry, &fakeSeries{}, &fakeLatest{})
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC) }
	start := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	jobID := "01910000-0000-7000-8000-000000000099"
	request := RunRequest{TenantID: "tenant", SiteID: "site", BindingID: "binding", SchedulerJobID: jobID, PeriodStart: start, PeriodEnd: start.Add(time.Hour), Reason: "BACKFILL"}
	if _, err = engine.Execute(context.Background(), request); err == nil {
		t.Fatal("first attempt unexpectedly succeeded")
	}
	result, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != jobID || len(registry.publicationCalls) != 2 {
		t.Fatalf("run=%s publicationCalls=%d", result.RunID, len(registry.publicationCalls))
	}
	if registry.publicationCalls[0].RunID != registry.publicationCalls[1].RunID || registry.publicationCalls[0].ResultID != registry.publicationCalls[1].ResultID {
		t.Fatalf("retry changed identity: first=%#v second=%#v", registry.publicationCalls[0], registry.publicationCalls[1])
	}
}

func TestReconcileCompletesExistingResultIdentity(t *testing.T) {
	result := Result{RunID: "run", ResultID: "result", Revision: 7, Binding: Binding{TenantID: "tenant", SiteID: "site"}}
	registry := &fakeRegistry{stale: []Result{result}}
	series := &fakeSeries{existing: map[string]bool{"result": true}}
	engine, err := New(registry, series, &fakeLatest{})
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := engine.Reconcile(context.Background(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if repaired != 1 || len(registry.completed) != 1 {
		t.Fatalf("repaired=%d completed=%d", repaired, len(registry.completed))
	}
	if registry.completed[0].ResultID != result.ResultID || registry.completed[0].Revision != result.Revision {
		t.Fatalf("reconcile changed Result identity: %#v", registry.completed[0])
	}
	if len(series.inserted) != 0 {
		t.Fatal("reconcile inserted a duplicate Metric fact")
	}
}

func TestReconcileReplaysMissingFactWithSameResultIdentity(t *testing.T) {
	result := Result{RunID: "run", ResultID: "result", Revision: 7, Binding: Binding{TenantID: "tenant", SiteID: "site"}, Value: 12.5, Quality: "GOOD", Completeness: 1}
	registry := &fakeRegistry{stale: []Result{result}}
	series := &fakeSeries{existing: map[string]bool{}}
	engine, err := New(registry, series, &fakeLatest{})
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := engine.Reconcile(context.Background(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if repaired != 1 || len(series.inserted) != 1 || len(registry.completed) != 1 {
		t.Fatalf("repaired=%d inserted=%d completed=%d", repaired, len(series.inserted), len(registry.completed))
	}
	if series.inserted[0].ResultID != result.ResultID || series.inserted[0].Revision != result.Revision || registry.completed[0].ResultID != result.ResultID {
		t.Fatalf("reconcile replay changed Result identity: inserted=%#v completed=%#v", series.inserted[0], registry.completed[0])
	}
}
