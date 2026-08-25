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
	if fact.MeterBindingID != testBindingID || fact.TelemetryKey != "site.energy.total" || fact.FactRevision != 0 {
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
		CounterSource: source, BindingResolver: resolver, FactSink: sink, BatchSize: 100,
		Now: func() time.Time { return time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || source.limit != 100 || resolver.calls != 1 || len(sink.facts) != 1 {
		t.Fatalf("count=%d source=%#v resolver=%#v sink=%#v", count, source, resolver, sink)
	}
}

func TestProjectorDoesNotWriteWhenBindingIsNotUnique(t *testing.T) {
	source := &fakeSource{deltas: []CounterDelta{validDelta()}}
	resolver := &fakeResolver{resolution: BindingResolution{Status: BindingAmbiguous}}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{CounterSource: source, BindingResolver: resolver, FactSink: sink, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil || sink.called {
		t.Fatalf("ProjectOnce() err=%v sink=%#v", err, sink)
	}
}

func TestProjectorDoesNotWriteWhenBindingIsMissing(t *testing.T) {
	source := &fakeSource{deltas: []CounterDelta{validDelta()}}
	resolver := &fakeResolver{resolution: BindingResolution{Status: BindingNoMatch}}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{CounterSource: source, BindingResolver: resolver, FactSink: sink, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil || sink.called {
		t.Fatalf("ProjectOnce() err=%v sink=%#v", err, sink)
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
	projector, err := NewProjector(ProjectorConfig{CounterSource: source, BindingResolver: &fakeResolver{resolution: validBinding()}, FactSink: &fakeSink{}, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil {
		t.Fatal("ProjectOnce() error = nil")
	}
}

func TestProjectorReturnsSinkFailure(t *testing.T) {
	projector, err := NewProjector(ProjectorConfig{
		CounterSource:   &fakeSource{deltas: []CounterDelta{validDelta()}},
		BindingResolver: &fakeResolver{resolution: validBinding()},
		FactSink:        &fakeSink{err: errors.New("write failed")}, BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil {
		t.Fatal("ProjectOnce() error = nil")
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
		Direction: "IMPORT", DeviceID: testDeviceID, PointID: testPointID, PointType: PointTypeCounter,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func float64Pointer(value float64) *float64 { return &value }

type fakeSource struct {
	deltas []CounterDelta
	limit  int
	err    error
}

func (source *fakeSource) ListDeltas(_ context.Context, limit int) ([]CounterDelta, error) {
	source.limit = limit
	return append([]CounterDelta(nil), source.deltas...), source.err
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
	facts  []EnergyIntervalFact
	called bool
	err    error
}

func (sink *fakeSink) InsertFacts(_ context.Context, facts []EnergyIntervalFact) error {
	sink.called = true
	sink.facts = append([]EnergyIntervalFact(nil), facts...)
	return sink.err
}
