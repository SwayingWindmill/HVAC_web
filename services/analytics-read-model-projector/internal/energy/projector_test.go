package energy

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

const (
	testOrganizationID      = "018f4e00-0000-7000-8000-000000000001"
	testSiteID              = "018f4e00-1000-7000-8000-000000000001"
	testDeviceID            = "018f4e00-2000-7000-8000-000000000001"
	testPreviousObservation = "018f4e00-3000-7000-8000-000000000001"
	testCurrentObservation  = "018f4e00-3000-7000-8000-000000000002"
)

func TestBuildFactConvertsCumulativeReadingToAdditiveInterval(t *testing.T) {
	projectedAt := time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC)
	fact, err := BuildFact(validCandidate(), projectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if fact.FactID != testCurrentObservation || fact.EnergyType != EnergyTypeElectricity || fact.EnergyKWh != 2.75 {
		t.Fatalf("fact=%#v", fact)
	}
	if fact.Quality != QualityValid || fact.ObservationCount != 2 || fact.DatasetRevision != 1722258003000 {
		t.Fatalf("fact=%#v", fact)
	}
	if !fact.PeriodStart.Equal(time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC)) ||
		!fact.PeriodEnd.Equal(time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)) ||
		!fact.DataWatermark.Equal(fact.PeriodEnd) || !fact.ProjectedAt.Equal(projectedAt) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactPropagatesSuspectSourceQuality(t *testing.T) {
	candidate := validCandidate()
	candidate.PreviousQuality = SourceQualitySuspect
	candidate.PreviousQualityReasons = []string{"SOURCE_LAG_EXCEEDED"}
	fact, err := BuildFact(candidate, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.Quality != QualitySuspect || !reflect.DeepEqual(fact.QualityReasons, []string{"SOURCE_LAG_EXCEEDED"}) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactMarksUnknownSourceQualityInvalid(t *testing.T) {
	candidate := validCandidate()
	candidate.CurrentQuality = "UNKNOWN"
	fact, err := BuildFact(candidate, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.Quality != QualityInvalid || !reflect.DeepEqual(fact.QualityReasons, []string{ReasonSourceQualityInvalid}) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactTreatsMeterRollbackAsSuspectZeroEnergy(t *testing.T) {
	candidate := validCandidate()
	candidate.PreviousValue = 120.5
	candidate.CurrentValue = 4.25
	fact, err := BuildFact(candidate, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.EnergyKWh != 0 || fact.Quality != QualitySuspect || !reflect.DeepEqual(fact.QualityReasons, []string{ReasonMeterResetOrRollback}) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestBuildFactRejectsInvalidStructuralCandidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Candidate)
	}{
		{"missing organization", func(candidate *Candidate) { candidate.OrganizationID = "" }},
		{"wrong telemetry key", func(candidate *Candidate) { candidate.TelemetryKey = "chiller.power" }},
		{"non increasing time", func(candidate *Candidate) { candidate.CurrentSampledAt = candidate.PreviousSampledAt }},
		{"missing source offset", func(candidate *Candidate) { candidate.SourceOffset = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validCandidate()
			test.mutate(&candidate)
			if _, err := BuildFact(candidate, time.Now()); err == nil {
				t.Fatal("BuildFact() error = nil")
			}
		})
	}
}

func TestBuildFactPreservesNegativeCumulativeEvidenceAsInvalid(t *testing.T) {
	candidate := validCandidate()
	candidate.CurrentValue = -1
	fact, err := BuildFact(candidate, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fact.EnergyKWh != 0 || fact.Quality != QualityInvalid || !reflect.DeepEqual(fact.QualityReasons, []string{ReasonNegativeCumulativeValue}) {
		t.Fatalf("fact=%#v", fact)
	}
}

func TestProjectorProcessesOneBatch(t *testing.T) {
	source := &fakeSource{candidates: []Candidate{validCandidate()}}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{Source: source, Sink: sink, BatchSize: 100, Now: func() time.Time {
		return time.Date(2026, 7, 29, 13, 0, 3, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	count, err := projector.ProjectOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || source.limit != 100 || len(sink.facts) != 1 {
		t.Fatalf("count=%d source=%#v sink=%#v", count, source, sink)
	}
}

func TestProjectorDoesNotWriteEmptyBatch(t *testing.T) {
	source := &fakeSource{}
	sink := &fakeSink{}
	projector, err := NewProjector(ProjectorConfig{Source: source, Sink: sink, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	count, err := projector.ProjectOnce(context.Background())
	if err != nil || count != 0 || sink.called {
		t.Fatalf("count=%d err=%v sink=%#v", count, err, sink)
	}
}

func TestProjectorReturnsSinkFailure(t *testing.T) {
	source := &fakeSource{candidates: []Candidate{validCandidate()}}
	sink := &fakeSink{err: errors.New("write failed")}
	projector, err := NewProjector(ProjectorConfig{Source: source, Sink: sink, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProjectOnce(context.Background()); err == nil {
		t.Fatal("ProjectOnce() error = nil")
	}
}

func validCandidate() Candidate {
	return Candidate{
		PreviousObservationID: testPreviousObservation,
		CurrentObservationID:  testCurrentObservation,
		OrganizationID:        testOrganizationID,
		SiteID:                testSiteID,
		DeviceID:              testDeviceID,
		TelemetryKey:          CumulativeElectricityTelemetryKey,
		PreviousValue:         100.25,
		CurrentValue:          103,
		PreviousQuality:       SourceQualityGood,
		CurrentQuality:        SourceQualityGood,
		PreviousSampledAt:     time.Date(2026, 7, 29, 12, 55, 0, 0, time.UTC),
		CurrentSampledAt:      time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
		SourceOffset:          1722258003000,
	}
}

type fakeSource struct {
	candidates []Candidate
	limit      int
	err        error
}

func (source *fakeSource) ListCandidates(_ context.Context, limit int) ([]Candidate, error) {
	source.limit = limit
	return append([]Candidate(nil), source.candidates...), source.err
}

type fakeSink struct {
	facts  []Fact
	called bool
	err    error
}

func (sink *fakeSink) InsertFacts(_ context.Context, facts []Fact) error {
	sink.called = true
	sink.facts = append([]Fact(nil), facts...)
	return sink.err
}
