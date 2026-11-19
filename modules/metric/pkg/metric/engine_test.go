package metric

import (
	"context"
	"errors"
	"math"
	"strings"
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
	inserted         []Result
	insertBatchCalls [][]Result
	existing         map[string]bool
	byPoint          map[string]Input
	errPoint         map[string]error
}

func (f *fakeSeries) ReadPoint(_ context.Context, _ Binding, pointID string, _, _ time.Time) (Input, error) {
	if err, ok := f.errPoint[pointID]; ok {
		return Input{}, err
	}
	if in, ok := f.byPoint[pointID]; ok {
		return in, nil
	}
	return Input{Reference: "point:" + pointID, Value: 20, FirstValue: 20, LastValue: 20, Count: 1, Quality: "GOOD", Completeness: 1}, nil
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

func (f *fakeSeries) InsertMetrics(_ context.Context, results []Result) error {
	f.insertBatchCalls = append(f.insertBatchCalls, append([]Result(nil), results...))
	for _, result := range results {
		if err := f.InsertMetric(context.Background(), result); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSeries) HasMetricResult(_ context.Context, result Result) (bool, error) {
	return f.existing[result.ResultID], nil
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

func TestSynthesizedMultiPointBindingSumsSubMetersAndMarksMissingSubMeterPartial(t *testing.T) {
	baseBinding := func(policy string) Binding {
		return Binding{
			TenantID: "tenant", SiteID: "site", BindingID: "binding", MetricVersionID: "version", MetricID: "metric",
			MetricCode: "hourly_energy", MetricVersion: 1, BindingVersion: 2, SubjectType: "SITE", SubjectID: "site",
			Granularity: "HOUR", DataType: "NUMBER", Aggregation: "LAST", CalculationMethod: "DELTA", QualityPolicy: policy,
			SourceDefinition: map[string]any{
				"points":  map[string]any{"site_energy": []any{"p1", "p2", "p3", "p4"}},
				"counter": map[string]any{"decreaseMode": "RESET"},
			},
			Dependencies: []Dependency{{Type: "POINT", Code: "site_energy", Required: true}},
		}
	}

	start := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	req := RunRequest{TenantID: "tenant", SiteID: "site", BindingID: "binding", PeriodStart: start, PeriodEnd: start.Add(time.Hour)}

	// All four sub-meters present (including one counter reset): GOOD and exact sum.
	allFour := &fakeSeries{
		byPoint: map[string]Input{
			"p1": {Reference: "point:p1", Rise: 40, Count: 12, Quality: "GOOD", Completeness: 1},
			"p2": {Reference: "point:p2", Rise: 35, Count: 12, Quality: "GOOD", Completeness: 1},
			"p3": {Reference: "point:p3", Rise: 50, Drops: 1, ValueAfterDrops: 5, Count: 12, Quality: "GOOD", Completeness: 1},
			"p4": {Reference: "point:p4", Rise: 30, Count: 12, Quality: "GOOD", Completeness: 1},
		},
	}
	engine, err := New(&fakeRegistry{binding: baseBinding("STRICT")}, allFour, &fakeLatest{})
	if err != nil {
		t.Fatal(err)
	}
	full, err := engine.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("all 4 sub-meters failed under STRICT: %v", err)
	}
	if full.Value != 160 || full.Quality != "GOOD" || full.Completeness != 1 {
		t.Fatalf("full result = (%v, %s, %v); want (160, GOOD, 1)", full.Value, full.Quality, full.Completeness)
	}

	// One sub-meter silent: STRICT rejects (preventing fake M&V savings), ESTIMATION_ALLOWED records the 3-meter sum with 0.75 completeness.
	threeOfFour := &fakeSeries{
		byPoint: map[string]Input{
			"p1": {Reference: "point:p1", Rise: 40, Count: 12, Quality: "GOOD", Completeness: 1},
			"p3": {Reference: "point:p3", Rise: 55, Count: 12, Quality: "GOOD", Completeness: 1},
			"p4": {Reference: "point:p4", Rise: 30, Count: 12, Quality: "GOOD", Completeness: 1},
		},
		errPoint: map[string]error{
			"p2": errors.New("metric point dependency has no numeric observations"),
		},
	}
	strictEngine, _ := New(&fakeRegistry{binding: baseBinding("STRICT")}, threeOfFour, &fakeLatest{})
	if _, err := strictEngine.Execute(context.Background(), req); err == nil {
		t.Fatal("STRICT policy must reject a synthesized hour missing a sub-meter")
	}

	estimatedEngine, _ := New(&fakeRegistry{binding: baseBinding("ESTIMATION_ALLOWED")}, threeOfFour, &fakeLatest{})
	partial, err := estimatedEngine.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("ESTIMATION_ALLOWED failed with 3 of 4 sub-meters: %v", err)
	}
	if partial.Value != 125 || partial.Quality != "ESTIMATED" || partial.Completeness != 0.75 {
		t.Fatalf("partial result = (%v, %s, %v); want (125, ESTIMATED, 0.75)", partial.Value, partial.Quality, partial.Completeness)
	}
}

func TestReconcileBatchesMissingFacts(t *testing.T) {
	first := Result{RunID: "run-1", ResultID: "res-1", Revision: 1, Binding: Binding{TenantID: "tenant", SiteID: "site"}, Value: 10, Quality: "GOOD", Completeness: 1}
	second := Result{RunID: "run-2", ResultID: "res-2", Revision: 2, Binding: Binding{TenantID: "tenant", SiteID: "site"}, Value: 20, Quality: "GOOD", Completeness: 1}
	registry := &fakeRegistry{stale: []Result{first, second}}
	series := &fakeSeries{existing: map[string]bool{}}
	engine, err := New(registry, series, &fakeLatest{})
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := engine.Reconcile(context.Background(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if repaired != 2 || len(registry.completed) != 2 {
		t.Fatalf("repaired=%d completed=%d", repaired, len(registry.completed))
	}
	if len(series.insertBatchCalls) != 1 || len(series.insertBatchCalls[0]) != 2 {
		t.Fatalf("expected 1 batched InsertMetrics call with 2 items, got %#v", series.insertBatchCalls)
	}
}

func TestCounterDeltaHandlesAllDecreaseModesAndGuardsBogusJumps(t *testing.T) {
	resetToZero := Binding{SourceDefinition: map[string]any{
		"counter": map[string]any{"decreaseMode": "RESET_TO_ZERO", "maxPowerKw": 200.0},
	}}
	got, err := counterDelta(resetToZero, Input{Rise: 12, Drops: 1, DropSum: -100, ValueAfterDrops: 5, DurationSeconds: 3600})
	if err != nil || math.Abs(got-17) > 1e-9 {
		t.Fatalf("RESET_TO_ZERO delta = %v err=%v, want 17", got, err)
	}

	// Bogus jump exceeding maxPowerKw * elapsedHours under RESET_TO_ZERO must fail.
	if _, err := counterDelta(resetToZero, Input{Rise: 10, Drops: 1, ValueAfterDrops: 5000, DurationSeconds: 60}); err == nil {
		t.Fatal("expected oversized jump under RESET_TO_ZERO to fail")
	}

	rollover := Binding{SourceDefinition: map[string]any{
		"counter": map[string]any{"decreaseMode": "ROLLOVER", "rolloverModulus": 1000.0},
	}}
	if got, err := counterDelta(rollover, Input{Rise: 20, Drops: 1, DropSum: -985, ValueAfterDrops: 5}); err != nil || math.Abs(got-35) > 1e-9 {
		t.Fatalf("ROLLOVER delta = %v err=%v, want 35", got, err)
	}
	if _, err := counterDelta(rollover, Input{Rise: 20, Drops: 1, DropSum: -1500, ValueAfterDrops: 5}); err == nil {
		t.Fatal("expected rollover drop exceeding rolloverModulus to fail")
	}

	replacement := Binding{SourceDefinition: map[string]any{
		"counter": map[string]any{"decreaseMode": "METER_REPLACEMENT", "replacementCarry": 12.5},
	}}
	if got, err := counterDelta(replacement, Input{Rise: 8, Drops: 1, DropSum: -500, ValueAfterDrops: 4}); err != nil || math.Abs(got-24.5) > 1e-9 {
		t.Fatalf("METER_REPLACEMENT delta = %v err=%v, want 24.5", got, err)
	}

	invalidMode := Binding{SourceDefinition: map[string]any{
		"counter": map[string]any{"decreaseMode": "INVALID"},
	}}
	if got, err := counterDelta(invalidMode, Input{Rise: 12.5, Drops: 0}); err != nil || math.Abs(got-12.5) > 1e-9 {
		t.Fatalf("INVALID mode with 0 drops = %v err=%v, want 12.5", got, err)
	}
	if _, err := counterDelta(invalidMode, Input{Rise: 10, Drops: 1, DropSum: -40, ValueAfterDrops: 20}); err == nil {
		t.Fatal("expected INVALID mode with uncleaned drop to reject")
	}
	if _, err := counterDelta(resetToZero, Input{Rise: math.NaN()}); err == nil {
		t.Fatal("expected NaN counter rise to fail")
	}
}

func TestThermalAndCOPGuardrailsAndEnergyWeightedAggregation(t *testing.T) {
	copBinding := Binding{MetricCode: "plant_cop", Aggregation: "AVG", CalculationMethod: "COP"}

	// Energy-weighted COP uses time-integrated energy (Integral) rather than unweighted rate ratio.
	got, err := calculate(copBinding, []Input{
		{Code: "cooling", Value: 500, Integral: 250, DurationSeconds: 1800},
		{Code: "power", Value: 100, Integral: 100, DurationSeconds: 3600},
	})
	if err != nil || math.Abs(got-2.5) > 1e-9 {
		t.Fatalf("energy-weighted COP = %v err=%v, want 2.5", got, err)
	}

	// Standby low power (2 kW < 5 kW deadband) with residual cooling must be rejected.
	if _, err := calculate(copBinding, []Input{
		{Code: "cooling", Value: 15},
		{Code: "power", Value: 2},
	}); err == nil || !strings.Contains(err.Error(), "standby") {
		t.Fatalf("expected standby deadband rejection, got err=%v", err)
	}

	// Residual delta-T (0.3 K < 0.5 K) and negative delta-T (-1.5 K) must be rejected.
	if _, err := calculate(copBinding, []Input{
		{Code: "cooling", Value: 400},
		{Code: "power", Value: 100},
		{Code: "delta_t", Value: 0.3},
	}); err == nil || !strings.Contains(err.Error(), "deadband") {
		t.Fatalf("expected residual delta-T rejection, got err=%v", err)
	}
	if _, err := calculate(copBinding, []Input{
		{Code: "cooling", Value: 400},
		{Code: "power", Value: 100},
		{Code: "return_temp", Value: 10.0},
		{Code: "supply_temp", Value: 11.5},
	}); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("expected negative delta-T rejection, got err=%v", err)
	}

	// Div-by-zero, NaN/Inf, out-of-range COP, and unweighted arithmetic mean of COP must be rejected.
	if _, err := calculate(copBinding, []Input{{Code: "cooling", Value: 400}, {Code: "power", Value: 0}}); err == nil {
		t.Fatal("expected div-by-zero power rejection")
	}
	if _, err := calculate(copBinding, []Input{{Code: "cooling", Value: math.NaN()}, {Code: "power", Value: 100}}); err == nil {
		t.Fatal("expected NaN rejection")
	}
	if _, err := calculate(copBinding, []Input{{Code: "cooling", Value: 2000}, {Code: "power", Value: 100}}); err == nil {
		t.Fatal("expected COP=20 outside physical bounds [0.5, 12] rejection")
	}
	if _, err := calculate(Binding{MetricCode: "plant_cop", CalculationMethod: "AVG"}, []Input{{Code: "c1", Value: 3.0}, {Code: "c2", Value: 5.0}}); err == nil {
		t.Fatal("expected arithmetic mean of instantaneous COP to be rejected")
	}

	// Generic RATIO without COP semantics still works on arbitrary finite inputs.
	if ratio, err := calculate(Binding{MetricCode: "custom_ratio", CalculationMethod: "RATIO"}, []Input{{Code: "a", Value: 10}, {Code: "b", Value: 2}}); err != nil || math.Abs(ratio-5.0) > 1e-9 {
		t.Fatalf("generic RATIO = %v err=%v", ratio, err)
	}
	if _, err := calculate(Binding{MetricCode: "expr_nan", CalculationMethod: "EXPRESSION", SourceDefinition: map[string]any{"expression": "a * b"}}, []Input{{Code: "a", Value: math.Inf(1)}, {Code: "b", Value: 0}}); err == nil {
		t.Fatal("expected Inf*0 -> NaN expression rejection")
	}
}
