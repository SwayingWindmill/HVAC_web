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

type fakeDirtyWindowEnqueuer struct {
	requests []DirtyWindowEnqueueRequest
}

func (f *fakeDirtyWindowEnqueuer) EnqueueDirtyWindow(_ context.Context, req DirtyWindowEnqueueRequest) error {
	f.requests = append(f.requests, req)
	return nil
}

func TestProjectorAppliesHistoricalRatioVersionsAndEnqueuesDirtyWindowForPastHours(t *testing.T) {
	janStart := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	boundary := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)

	binding := validBinding()
	binding.RatioVersions = []RatioVersion{
		{
			ID:                   "ratio-old",
			CTRatio:              20,
			PTRatio:              1,
			MeterMultiplier:      1,
			RatioApplicationMode: RatioAppModePlatform,
			ReadingUnit:          UnitKWh,
			EffectiveFrom:        janStart,
			EffectiveTo:          &boundary,
			Status:               RatioStatusReleased,
		},
		{
			ID:                   "ratio-new",
			CTRatio:              40,
			PTRatio:              1,
			MeterMultiplier:      1,
			RatioApplicationMode: RatioAppModePlatform,
			ReadingUnit:          UnitMWh,
			EffectiveFrom:        boundary,
			Status:               RatioStatusReleased,
		},
	}

	rawPre := 2.0
	deltaPre := validDelta()
	deltaPre.CurrentObservationID = "018f4e00-3000-7000-8000-000000000091"
	deltaPre.PreviousSampledAt = time.Date(2026, 7, 29, 12, 50, 0, 0, time.UTC)
	deltaPre.CurrentSampledAt = boundary
	deltaPre.DeltaValue = &rawPre

	rawPost := 0.5
	deltaPost := validDelta()
	deltaPost.CurrentObservationID = "018f4e00-3000-7000-8000-000000000092"
	deltaPost.PreviousObservationID = deltaPre.CurrentObservationID
	deltaPost.PreviousSampledAt = boundary
	deltaPost.CurrentSampledAt = time.Date(2026, 7, 29, 15, 5, 0, 0, time.UTC)
	deltaPost.DeltaValue = &rawPost

	source := &fakeSource{
		deltas: []CounterDelta{deltaPre, deltaPost},
	}
	sink := &fakeSink{}
	enqueuer := &fakeDirtyWindowEnqueuer{}
	now := time.Date(2026, 7, 29, 15, 10, 0, 0, time.UTC)

	projector, err := NewProjector(ProjectorConfig{
		CounterSource:       source,
		BindingResolver:     &fakeResolver{resolution: binding},
		FactSink:            sink,
		RebuildEvents:       sink,
		DirtyWindowEnqueuer: enqueuer,
		DirtyWindowDelay:    45 * time.Second,
		BatchSize:           16,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatalf("ProjectOnce() error = %v", err)
	}
	if n != 2 || len(sink.facts) != 2 {
		t.Fatalf("expected 2 projected facts, got %d / %d", n, len(sink.facts))
	}
	// Pre-boundary interval uses old CT=20 * kWh -> 2.0 * 20 = 40 kWh.
	if sink.facts[0].EnergyKWh != 40.0 {
		t.Fatalf("pre-boundary EnergyKWh = %v, want 40.0", sink.facts[0].EnergyKWh)
	}
	// Post-boundary interval uses new CT=40 * MWh (1000) -> 0.5 * 40 * 1000 = 20000 kWh.
	if sink.facts[1].EnergyKWh != 20000.0 {
		t.Fatalf("post-boundary EnergyKWh = %v, want 20000.0", sink.facts[1].EnergyKWh)
	}
	// Only the pre-boundary fact landed in a past closed hour (12:00-13:00 < current 15:00); post-boundary is current hour.
	if len(enqueuer.requests) != 1 {
		t.Fatalf("expected 1 coalesced dirty window request, got %d (%+v)", len(enqueuer.requests), enqueuer.requests)
	}
	req := enqueuer.requests[0]
	wantStart := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	if !req.WindowStart.Equal(wantStart) || !req.WindowEnd.Equal(wantEnd) || !req.AvailableAt.Equal(now.Add(45*time.Second)) {
		t.Fatalf("unexpected dirty window request: %+v", req)
	}
}

type fakeHourlyV2Sink struct {
	rows  []HourlyFactV2
	calls int
	err   error
}

func (f *fakeHourlyV2Sink) InsertHourlyFactsV2(_ context.Context, rows []HourlyFactV2) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, rows...)
	return nil
}

type fakeLoadProfileSource struct {
	profiles   map[string][]HourlyLoadWeight
	calls      int
	queriedIDs []string
	gotFrom    time.Time
	gotTo      time.Time
	err        error
}

func (f *fakeLoadProfileSource) HourlyPowerProfile(_ context.Context, _, _, deviceID string, from, to time.Time) ([]HourlyLoadWeight, error) {
	f.calls++
	f.queriedIDs = append(f.queriedIDs, deviceID)
	f.gotFrom = from
	f.gotTo = to
	if f.err != nil {
		return nil, f.err
	}
	return append([]HourlyLoadWeight(nil), f.profiles[deviceID]...), nil
}

func TestProjectorWritesHourlyV2DecimalAndCrossGapAllocations(t *testing.T) {
	now := time.Date(2026, 10, 12, 16, 5, 0, 0, time.UTC)
	binding := validBinding()

	// Two regular <=1h intervals in the same Option A hour (12:00 UTC), including exact boundary at 13:00:00Z.
	d1Val := 0.100001
	d1 := validDelta()
	d1.CurrentObservationID = "018f4e00-3000-7000-8000-000000000101"
	d1.PreviousSampledAt = time.Date(2026, 10, 12, 12, 10, 0, 0, time.UTC)
	d1.CurrentSampledAt = time.Date(2026, 10, 12, 12, 25, 0, 0, time.UTC)
	d1.DeltaValue = &d1Val

	d2Val := 0.200002
	d2 := validDelta()
	d2.CurrentObservationID = "018f4e00-3000-7000-8000-000000000102"
	d2.PreviousObservationID = d1.CurrentObservationID
	d2.PreviousSampledAt = d1.CurrentSampledAt
	d2.CurrentSampledAt = time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC) // Option A: 13:00-1ms => 12:00Z
	d2.DeltaValue = &d2Val

	// Cross-gap (>1h) valid interval across 13:00Z..16:00Z (3 hours), load-weighted 1:2:3.
	gapVal := 60.0
	dGap := validDelta()
	dGap.CurrentObservationID = "018f4e00-3000-7000-8000-000000000103"
	dGap.PreviousObservationID = d2.CurrentObservationID
	dGap.PreviousSampledAt = time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	dGap.CurrentSampledAt = time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	dGap.DeltaValue = &gapVal

	h13 := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	h14 := h13.Add(time.Hour)
	h15 := h13.Add(2 * time.Hour)

	v2Sink := &fakeHourlyV2Sink{}
	loadSource := &fakeLoadProfileSource{
		profiles: map[string][]HourlyLoadWeight{
			testDeviceID: {
				{HourStart: h13, Weight: 100, Source: "active_power"},
				{HourStart: h14, Weight: 200, Source: "active_power"},
				{HourStart: h15, Weight: 300, Source: "active_power"},
			},
		},
	}
	factSink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:     &fakeSource{deltas: []CounterDelta{d1, d2, dGap}},
		BindingResolver:   &fakeResolver{resolution: binding},
		FactSink:          factSink,
		HourlyV2Sink:      v2Sink,
		LoadProfileSource: loadSource,
		RebuildEvents:     factSink,
		BatchSize:         16,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	projected, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatalf("ProjectOnce failed: %v", err)
	}
	if projected != 3 || len(factSink.advanced) != 1 || v2Sink.calls != 1 {
		t.Fatalf("projected=%d advanced=%d v2Calls=%d", projected, len(factSink.advanced), v2Sink.calls)
	}
	if !loadSource.gotFrom.Equal(h13) || !loadSource.gotTo.Equal(h15.Add(time.Hour)) {
		t.Fatalf("load profile window = [%v, %v), want [%v, %v)", loadSource.gotFrom, loadSource.gotTo, h13, h15.Add(time.Hour))
	}

	// Expect 4 v2 rows: 12:00 VALID (0.300003, count=2), plus 13:00/14:00/15:00 SUSPECT (10, 20, 30).
	if len(v2Sink.rows) != 4 {
		t.Fatalf("expected 4 v2 rows, got %d: %+v", len(v2Sink.rows), v2Sink.rows)
	}
	wantCalcVersion := uint64(now.UnixMilli())
	byHour := make(map[time.Time]HourlyFactV2, len(v2Sink.rows))
	for _, row := range v2Sink.rows {
		if row.CalcVersion != wantCalcVersion {
			t.Fatalf("row calc_version = %d, want %d", row.CalcVersion, wantCalcVersion)
		}
		byHour[row.Hour] = row
	}
	r12 := byHour[time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)]
	if r12.Quality != FactQualityValid || r12.EnergyKWh != 0.300003 || r12.IntervalCount != 2 {
		t.Fatalf("unexpected 12:00 regular v2 row: %+v", r12)
	}
	if r13 := byHour[h13]; r13.Quality != FactQualitySuspect || r13.EnergyKWh != 10.0 || r13.IntervalCount != 1 {
		t.Fatalf("unexpected 13:00 split v2 row: %+v", r13)
	}
	if r14 := byHour[h14]; r14.Quality != FactQualitySuspect || r14.EnergyKWh != 20.0 || r14.IntervalCount != 1 {
		t.Fatalf("unexpected 14:00 split v2 row: %+v", r14)
	}
	if r15 := byHour[h15]; r15.Quality != FactQualitySuspect || r15.EnergyKWh != 30.0 || r15.IntervalCount != 1 {
		t.Fatalf("unexpected 15:00 split v2 row: %+v", r15)
	}
}

func TestProjectorCrossGapToleratesLoadProfileFailureAndPropagatesV2SinkError(t *testing.T) {
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	binding := validBinding()
	gapVal := 30.0
	dGap := validDelta()
	dGap.PreviousSampledAt = time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	dGap.CurrentSampledAt = time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	dGap.DeltaValue = &gapVal

	// LoadProfileSource error falls back uniformly without failing ProjectOnce.
	v2Sink := &fakeHourlyV2Sink{}
	factSink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:     &fakeSource{deltas: []CounterDelta{dGap}},
		BindingResolver:   &fakeResolver{resolution: binding},
		FactSink:          factSink,
		HourlyV2Sink:      v2Sink,
		LoadProfileSource: &fakeLoadProfileSource{err: errors.New("clickhouse timeout")},
		RebuildEvents:     factSink,
		BatchSize:         10,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projector.ProjectOnce(context.Background())
	if err != nil || projected != 1 || len(factSink.advanced) != 1 {
		t.Fatalf("expected uniform fallback to succeed, projected=%d err=%v advanced=%d", projected, err, len(factSink.advanced))
	}
	if len(v2Sink.rows) != 3 {
		t.Fatalf("expected 3 uniform split rows, got %d", len(v2Sink.rows))
	}
	for _, row := range v2Sink.rows {
		if row.EnergyKWh != 10.0 || row.Quality != FactQualitySuspect {
			t.Fatalf("unexpected uniform fallback row: %+v", row)
		}
	}

	// HourlyV2Sink failure must block checkpoint advance.
	failingV2 := &fakeHourlyV2Sink{err: errors.New("v2 insert failed")}
	factSink2 := &fakeSink{}
	projectorFail, err := NewProjector(ProjectorConfig{
		CounterSource:   &fakeSource{deltas: []CounterDelta{dGap}},
		BindingResolver: &fakeResolver{resolution: binding},
		FactSink:        factSink2,
		HourlyV2Sink:    failingV2,
		RebuildEvents:   factSink2,
		BatchSize:       10,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectorFail.ProjectOnce(context.Background()); err == nil || len(factSink2.advanced) != 0 {
		t.Fatalf("expected ProjectOnce to fail without advancing checkpoint, err=%v advanced=%v", err, factSink2.advanced)
	}
}

func TestProjectorRebuildDirtyWindowWritesHourlyV2(t *testing.T) {
	now := time.Date(2026, 10, 12, 16, 10, 0, 0, time.UTC)
	binding := validBinding()
	binding.AccountingScope = AccountingScopeBranch
	binding.LoadDeviceID = "018f4e00-2000-7000-8000-000000000099"

	deltaVal := 18.0
	delta := validDelta()
	delta.PreviousSampledAt = time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	delta.CurrentSampledAt = time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	delta.DeltaValue = &deltaVal

	h13 := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	h14 := h13.Add(time.Hour)

	v2Sink := &fakeHourlyV2Sink{}
	loadSource := &fakeLoadProfileSource{
		profiles: map[string][]HourlyLoadWeight{
			// Reporting device empty -> falls back to LoadDeviceID!
			binding.LoadDeviceID: {
				{HourStart: h13, Weight: 1, Source: "active_power"},
				{HourStart: h14, Weight: 2, Source: "active_power"},
			},
		},
	}
	factSink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:     &fakeSource{windowDeltas: []CounterDelta{delta}},
		BindingResolver:   &fakeResolver{resolution: binding},
		FactSink:          factSink,
		HourlyV2Sink:      v2Sink,
		LoadProfileSource: loadSource,
		RebuildEvents:     factSink,
		BatchSize:         10,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	rebuilt, err := projector.RebuildDirtyWindow(context.Background(), DirtyWindow{
		TenantID: testTenantID,
		SiteID:   testSiteID,
		PointIDs: []string{testPointID},
		Start:    h13,
		End:      h14.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("RebuildDirtyWindow failed: %v", err)
	}
	if rebuilt != 1 || len(factSink.advanced) != 0 || len(v2Sink.rows) != 2 {
		t.Fatalf("rebuilt=%d advanced=%d v2Rows=%d", rebuilt, len(factSink.advanced), len(v2Sink.rows))
	}
	if v2Sink.rows[0].LoadDeviceID != binding.LoadDeviceID || v2Sink.rows[0].EnergyKWh != 6.0 || v2Sink.rows[1].EnergyKWh != 12.0 {
		t.Fatalf("unexpected rebuild v2 rows: %+v", v2Sink.rows)
	}
}
