package energy

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

const (
	testTenantID            = "018f4d00-0000-7000-8000-000000000001"
	testSiteID              = "018f4e00-1000-7000-8000-000000000001"
	testMeterID             = "018f4e00-1100-7000-8000-000000000001"
	testBindingID           = "018f4e00-1200-7000-8000-000000000001"
	testTopologyVersionID   = "018f4e00-1300-7000-8000-000000000001"
	testEnergyTypeID        = "018f4e00-1400-7000-8000-000000000001"
	testDeviceID            = "018f4e00-2000-7000-8000-000000000001"
	testPointID             = "018f4e00-2100-7000-8000-000000000001"
	testSensorID            = "018f4e00-2200-7000-8000-000000000001"
	testPreviousObservation = "018f4e00-3000-7000-8000-000000000001"
	testCurrentObservation  = "018f4e00-3000-7000-8000-000000000002"
)

func TestBuildFactUsesCanonicalDeltaAndBindingSnapshot(t *testing.T) {
	projectedAt := time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC)
	delta := validDelta()
	delta.PreviousValue = 100.25
	delta.DeltaValue = float64Pointer(2.75)
	fact, err := BuildFact(delta, validBinding(), projectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if fact.FactID != testCurrentObservation || fact.EnergyType != EnergyTypeElectricity || fact.EnergyKWh != 2.75 {
		t.Fatalf("fact=%#v", fact)
	}
	if fact.MeterBindingID != testBindingID || fact.TelemetryKey != "site.energy.total" || fact.FactRevision != 0 || fact.AccountingScope != AccountingScopeSite || fact.LoadDeviceID != nil {
		t.Fatalf("fact=%#v", fact)
	}
	if fact.Quality != FactQualityValid || fact.DatasetRevision != 1722258003000 || fact.SourceOffset != 1722258003000 {
		t.Fatalf("fact=%#v", fact)
	}
	if !fact.PeriodStart.Equal(time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC)) ||
		!fact.PeriodEnd.Equal(time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)) ||
		!fact.DataWatermark.Equal(fact.PeriodEnd) || !fact.ProjectedAt.Equal(projectedAt) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactRejectsABindingWithoutAnAccountingScope(t *testing.T) {
	binding := validBinding()
	binding.AccountingScope = ""
	if _, err := BuildFact(validDelta(), binding, time.Now()); err == nil {
		t.Fatal("BuildFact() error = nil")
	}
}

func TestBuildFactCreditsASubMeterToTheEquipmentItFeeds(t *testing.T) {
	const chiller = "018f4e00-2000-7000-8000-000000000099"
	binding := validBinding()
	binding.AccountingScope = AccountingScopeBranch
	binding.LoadDeviceID = chiller
	fact, err := BuildFact(validDelta(), binding, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.AccountingScope != AccountingScopeBranch || fact.LoadDeviceID == nil || *fact.LoadDeviceID != chiller || fact.DeviceID != testDeviceID {
		t.Fatalf("fact=%#v", fact)
	}
	binding.LoadDeviceID = ""
	if _, err := BuildFact(validDelta(), binding, time.Now()); err == nil {
		t.Fatal("BuildFact() accepted a sub-meter without the equipment it feeds")
	}
}

func TestBuildFactMapsRawQualityWithoutRecomputingDelta(t *testing.T) {
	delta := validDelta()
	delta.CurrentQuality = SourceQualityPartial
	delta.PreviousQuality = SourceQualityGood
	delta.CurrentQualityReasons = []string{"SOURCE_LAG_EXCEEDED"}
	delta.DeltaValue = float64Pointer(0)
	fact, err := BuildFact(delta, validBinding(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.EnergyKWh != 0 || fact.Quality != FactQualitySuspect || !reflect.DeepEqual(fact.QualityReasons, []string{"SOURCE_LAG_EXCEEDED"}) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactMapsUnknownRawQualityToInvalid(t *testing.T) {
	delta := validDelta()
	delta.CurrentQuality = "UNKNOWN"
	fact, err := BuildFact(delta, validBinding(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.Quality != FactQualityInvalid || !reflect.DeepEqual(fact.QualityReasons, []string{ReasonSourceQualityInvalid}) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactRejectsInvalidDecreaseInsteadOfWritingZero(t *testing.T) {
	delta := validDelta()
	delta.TransitionType = TransitionInvalidDecrease
	delta.DeltaValue = nil
	if _, err := BuildFact(delta, validBinding(), time.Now()); err == nil {
		t.Fatal("BuildFact() error = nil")
	}
}

func TestProjectorProcessesOneCanonicalBatch(t *testing.T) {
	source := &fakeSource{deltas: []CounterDelta{validDelta()}}
	resolver := &fakeResolver{resolution: validBinding()}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource: source, BindingResolver: resolver, FactSink: sink, RebuildEvents: sink, BatchSize: 100,
		Now: func() time.Time { return time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || source.limit != 100 || resolver.calls != 1 || len(sink.facts) != 1 || len(sink.advanced) != 1 || sink.advanced[0] != fakeThrough {
		t.Fatalf("count=%d source=%#v resolver=%#v sink=%#v", count, source, resolver, sink)
	}
}

func TestProjectorWritesFactsForPrimaryCoolingMeter(t *testing.T) {
	binding := validBinding()
	binding.EnergyType = "cooling"
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource: &fakeSource{deltas: []CounterDelta{validDelta()}}, BindingResolver: &fakeResolver{resolution: binding}, FactSink: sink, RebuildEvents: sink, BatchSize: 10,
		Now: func() time.Time { return time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.facts) != 1 || sink.facts[0].EnergyType != "cooling" || sink.facts[0].EnergyKWh != 2.75 {
		t.Fatalf("facts=%#v", sink.facts)
	}
}

func TestProjectorDoesNotWriteWhenBindingIsNotUnique(t *testing.T) {
	source := &fakeSource{deltas: []CounterDelta{validDelta()}}
	resolver := &fakeResolver{resolution: BindingResolution{Status: BindingAmbiguous}}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{CounterSource: source, BindingResolver: resolver, FactSink: sink, RebuildEvents: sink, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil || sink.called {
		t.Fatalf("ProjectOnce() err=%v sink=%#v", err, sink)
	}
}

// A counter no meter binding names (a calculated BTU meter's cooling, say) is not electricity in the
// energy topology: the projector passes over it rather than stopping every Site's facts behind it.
func TestProjectorPassesOverCountersNoMeterBindingNames(t *testing.T) {
	source := &fakeSource{deltas: []CounterDelta{validDelta()}}
	resolver := &fakeResolver{resolution: BindingResolution{Status: BindingNoMatch}}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{CounterSource: source, BindingResolver: resolver, FactSink: sink, RebuildEvents: sink, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projector.ProjectOnce(context.Background())
	if err != nil || projected != 0 || sink.called || len(sink.advanced) != 1 || sink.advanced[0] != fakeThrough {
		t.Fatalf("ProjectOnce() projected=%d err=%v sink=%#v", projected, err, sink)
	}
}

func TestBuildFactRejectsBindingOutsideEventTime(t *testing.T) {
	binding := validBinding()
	end := validDelta().CurrentSampledAt.Add(-time.Minute)
	binding.EffectiveTo = &end
	if _, err := BuildFact(validDelta(), binding, time.Now()); err == nil {
		t.Fatal("BuildFact() error = nil")
	}
}

func TestProjectorRejectsDuplicateLogicalFactKey(t *testing.T) {
	first := validDelta()
	second := validDelta()
	second.CurrentObservationID = first.CurrentObservationID
	source := &fakeSource{deltas: []CounterDelta{first, second}}
	projector, err := NewProjector(ProjectorConfig{CounterSource: source, BindingResolver: &fakeResolver{resolution: validBinding()}, FactSink: &fakeSink{}, RebuildEvents: &fakeSink{}, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil {
		t.Fatal("ProjectOnce() error = nil")
	}
}

func TestProjectorReturnsSinkFailureWithoutAdvancingTheCheckpoint(t *testing.T) {
	sink := &fakeSink{err: errors.New("write failed")}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:   &fakeSource{deltas: []CounterDelta{validDelta()}},
		BindingResolver: &fakeResolver{resolution: validBinding()},
		FactSink:        sink, RebuildEvents: sink, BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil || len(sink.advanced) != 0 {
		t.Fatalf("ProjectOnce() err=%v advanced=%v", err, sink.advanced)
	}
}

func validDelta() CounterDelta {
	delta := 2.75
	return CounterDelta{
		PreviousObservationID: testPreviousObservation,
		CurrentObservationID:  testCurrentObservation,
		TenantID:              testTenantID, SiteID: testSiteID, DeviceID: testDeviceID, PointID: testPointID, SensorID: testSensorID,
		TelemetryKey: "site.energy.total", PointRevision: 3, Unit: "kWh", CounterDecreaseMode: "RESET_TO_ZERO",
		PreviousValue: 100.25, PreviousSampledAt: time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC),
		PreviousQuality: SourceQualityGood, CurrentSampledAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		CurrentQuality: SourceQualityGood, CurrentSourceEventID: "018f4e00-3100-7000-8000-000000000001",
		CurrentSourcePartition: "telemetry-0", CurrentSourceOffset: 1722258003000,
		TransitionType: TransitionIncrease, DeltaValue: &delta,
	}
}

func validBinding() BindingResolution {
	return BindingResolution{
		Status: BindingMatch, TenantID: testTenantID, SiteID: testSiteID, MeterID: testMeterID,
		MeterBindingID: testBindingID, TopologyVersionID: testTopologyVersionID, BindingVersion: 4,
		EnergyTypeID: testEnergyTypeID, EnergyType: EnergyTypeElectricity, MeterRole: MeterRolePrimary,
		Direction: "IMPORT", AccountingScope: AccountingScopeSite, DeviceID: testDeviceID, PointID: testPointID, PointType: PointTypeCounter,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func float64Pointer(value float64) *float64 { return &value }

type fakeSource struct {
	deltas       []CounterDelta
	windowDeltas []CounterDelta
	lastWindow   DirtyWindow
	limit        int
	err          error
}

func (source *fakeSource) WindowDeltas(_ context.Context, window DirtyWindow) ([]CounterDelta, error) {
	source.lastWindow = window
	return append([]CounterDelta(nil), source.windowDeltas...), source.err
}

var fakeThrough = ProjectionCursor{HistorySequence: 7, ObservationID: "018f2e00-0000-7000-8000-000000000099"}

func (source *fakeSource) NextBatch(_ context.Context, limit int) (CounterBatch, error) {
	source.limit = limit
	through := fakeThrough
	return CounterBatch{Deltas: append([]CounterDelta(nil), source.deltas...), Through: &through}, source.err
}

type fakeResolver struct {
	resolution BindingResolution
	calls      int
	err        error
}

func (resolver *fakeResolver) Resolve(_ context.Context, _ BindingResolveInput) (BindingResolution, error) {
	resolver.calls++
	return resolver.resolution, resolver.err
}

type fakeSink struct {
	facts    []EnergyIntervalFact
	called   bool
	err      error
	advanced []ProjectionCursor
}

func (sink *fakeSink) AdvanceCheckpoint(_ context.Context, cursor ProjectionCursor) error {
	sink.advanced = append(sink.advanced, cursor)
	return nil
}

func (sink *fakeSink) AppendRebuildEvent(context.Context, RebuildEvent) error {
	return nil
}

func (sink *fakeSink) InsertFacts(_ context.Context, facts []EnergyIntervalFact) error {
	sink.called = true
	sink.facts = append(sink.facts, facts...)
	return sink.err
}

// The telemetry worker once ran without a rebuild event sink; the first counter correction
// then failed every batch and energy projection stopped for good.
func TestNewProjectorRequiresARebuildEventSink(t *testing.T) {
	_, err := NewProjector(ProjectorConfig{
		CounterSource: &fakeSource{}, BindingResolver: &fakeResolver{}, FactSink: &fakeSink{}, BatchSize: 10,
	})
	if err == nil {
		t.Fatal("projector built without a rebuild event sink")
	}
}

func TestStaticBindingResolverAndMatchMeterBindingsEnforceEffectiveWindows(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	mid := from.Add(24 * time.Hour)
	end := mid.Add(24 * time.Hour)

	v1 := validBinding()
	v1.EffectiveFrom = from
	v1.EffectiveTo = &mid

	v2 := validBinding()
	v2.MeterBindingID = "018f4e00-1200-7000-8000-000000000002"
	v2.BindingVersion = 5
	v2.EffectiveFrom = mid
	v2.EffectiveTo = &end

	overlap := validBinding()
	overlap.MeterBindingID = "018f4e00-1200-7000-8000-000000000003"
	overlap.EffectiveFrom = end.Add(-time.Hour)

	resolver := NewStaticBindingResolver(v1, v2, overlap)
	query := func(at time.Time) BindingResolution {
		res, err := resolver.Resolve(context.Background(), BindingResolveInput{
			TenantID: testTenantID, SiteID: testSiteID, DeviceID: testDeviceID, PointID: testPointID, SampledAt: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	if before := query(from.Add(-time.Second)); before.Status != BindingNoMatch {
		t.Fatalf("before effectiveFrom=%#v, want NO_MATCH", before)
	}
	if hit1 := query(from); hit1.Status != BindingMatch || hit1.MeterBindingID != v1.MeterBindingID {
		t.Fatalf("at effectiveFrom=%#v, want v1", hit1)
	}
	if hit2 := query(mid); hit2.Status != BindingMatch || hit2.MeterBindingID != v2.MeterBindingID {
		t.Fatalf("at half-open boundary=%#v, want v2", hit2)
	}
	if ambiguous := query(end.Add(-30 * time.Minute)); ambiguous.Status != BindingAmbiguous {
		t.Fatalf("overlap=%#v, want AMBIGUOUS", ambiguous)
	}
}

func TestProjectorReemitsDeltaValueChangeAndInvalidationRevisions(t *testing.T) {
	binding := validBinding()

	deltaChanged := validDelta()
	deltaChanged.CurrentObservationID = "018f4e00-3000-7000-8000-000000000010"
	updatedKWh := 14.25
	deltaChanged.DeltaValue = &updatedKWh
	deltaChanged.ExistingFactFound = true
	deltaChanged.ExistingFactMeterBindingID = binding.MeterBindingID
	deltaChanged.ExistingFactPreviousObservationID = deltaChanged.PreviousObservationID
	deltaChanged.ExistingFactEnergyKWh = 10.0
	deltaChanged.ExistingFactTransitionType = TransitionIncrease
	deltaChanged.ExistingFactQuality = FactQualityValid
	deltaChanged.ExistingFactRevision = 2

	deltaInvalidated := validDelta()
	deltaInvalidated.CurrentObservationID = "018f4e00-3000-7000-8000-000000000011"
	deltaInvalidated.TransitionType = TransitionInvalidDecrease
	deltaInvalidated.DeltaValue = nil
	deltaInvalidated.ExistingFactFound = true
	deltaInvalidated.ExistingFactMeterBindingID = binding.MeterBindingID
	deltaInvalidated.ExistingFactPreviousObservationID = deltaInvalidated.PreviousObservationID
	deltaInvalidated.ExistingFactEnergyKWh = 6.5
	deltaInvalidated.ExistingFactTransitionType = TransitionIncrease
	deltaInvalidated.ExistingFactQuality = FactQualityValid
	deltaInvalidated.ExistingFactRevision = 4

	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:   &fakeSource{deltas: []CounterDelta{deltaChanged, deltaInvalidated}},
		BindingResolver: &fakeResolver{resolution: binding},
		FactSink:        sink,
		RebuildEvents:   sink,
		BatchSize:       16,
		Now:             func() time.Time { return time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if projected != 2 || len(sink.facts) != 2 {
		t.Fatalf("projected=%d facts=%d, want 2", projected, len(sink.facts))
	}
	if sink.facts[0].FactRevision != 3 || sink.facts[0].EnergyKWh != 14.25 || sink.facts[0].Quality != FactQualityValid {
		t.Fatalf("unexpected delta-updated fact: %+v", sink.facts[0])
	}
	if sink.facts[1].FactRevision != 5 || sink.facts[1].EnergyKWh != 0 || sink.facts[1].Quality != FactQualityInvalid {
		t.Fatalf("unexpected invalidated fact: %+v", sink.facts[1])
	}
}

func TestProjectorRebuildDirtyWindowStitchesBoundariesIdempotently(t *testing.T) {
	binding := validBinding()

	unchanged := validDelta()
	unchanged.CurrentObservationID = "018f4e00-3000-7000-8000-000000000020"
	unchanged.ExistingFactFound = true
	unchanged.ExistingFactMeterBindingID = binding.MeterBindingID
	unchanged.ExistingFactPreviousObservationID = unchanged.PreviousObservationID
	unchanged.ExistingFactEnergyKWh = *unchanged.DeltaValue
	unchanged.ExistingFactTransitionType = TransitionIncrease
	unchanged.ExistingFactQuality = FactQualityValid
	unchanged.ExistingFactRevision = 1

	anchoredBeforeStart := validDelta()
	anchoredBeforeStart.CurrentObservationID = "018f4e00-3000-7000-8000-000000000021"
	anchoredBeforeStart.PreviousSampledAt = time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	anchoredBeforeStart.CurrentSampledAt = time.Date(2026, 7, 29, 13, 15, 0, 0, time.UTC)
	edgeKWh := 18.0
	anchoredBeforeStart.DeltaValue = &edgeKWh

	successorAfterEnd := validDelta()
	successorAfterEnd.CurrentObservationID = "018f4e00-3000-7000-8000-000000000022"
	successorAfterEnd.PreviousObservationID = anchoredBeforeStart.CurrentObservationID
	successorAfterEnd.PreviousSampledAt = anchoredBeforeStart.CurrentSampledAt
	successorAfterEnd.CurrentSampledAt = time.Date(2026, 7, 29, 14, 20, 0, 0, time.UTC)
	succKWh := 4.5
	successorAfterEnd.DeltaValue = &succKWh
	successorAfterEnd.ExistingFactFound = true
	successorAfterEnd.ExistingFactMeterBindingID = binding.MeterBindingID
	successorAfterEnd.ExistingFactPreviousObservationID = "018f4e00-3000-7000-8000-000000000001"
	successorAfterEnd.ExistingFactEnergyKWh = 22.5
	successorAfterEnd.ExistingFactTransitionType = TransitionIncrease
	successorAfterEnd.ExistingFactQuality = FactQualityValid
	successorAfterEnd.ExistingFactRevision = 0

	source := &fakeSource{windowDeltas: []CounterDelta{unchanged, anchoredBeforeStart, successorAfterEnd}}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:   source,
		BindingResolver: &fakeResolver{resolution: binding},
		FactSink:        sink,
		RebuildEvents:   sink,
		BatchSize:       16,
		Now:             func() time.Time { return time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	window := DirtyWindow{
		TenantID: testTenantID,
		SiteID:   testSiteID,
		PointIDs: []string{testPointID},
		Start:    time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
	}
	rebuilt, err := projector.RebuildDirtyWindow(context.Background(), window)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt != 2 || len(sink.facts) != 2 {
		t.Fatalf("rebuilt=%d facts=%d, want 2 (unchanged skipped, start-anchor + T_end+1 kept)", rebuilt, len(sink.facts))
	}
	if len(sink.advanced) != 0 {
		t.Fatalf("dirty window rebuild should not advance checkpoint, got %+v", sink.advanced)
	}
	if sink.facts[0].CurrentObservationID != anchoredBeforeStart.CurrentObservationID || sink.facts[0].EnergyKWh != 18.0 || sink.facts[0].FactRevision != 0 {
		t.Fatalf("unexpected pre-T_start stitched fact: %+v", sink.facts[0])
	}
	if sink.facts[1].CurrentObservationID != successorAfterEnd.CurrentObservationID || sink.facts[1].EnergyKWh != 4.5 || sink.facts[1].FactRevision != 1 {
		t.Fatalf("unexpected T_end+1 stitched successor fact: %+v", sink.facts[1])
	}
}
