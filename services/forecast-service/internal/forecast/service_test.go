package forecast

import (
	"context"
	"errors"
	"testing"
	"time"
)

type captureSink struct {
	points []Point
	err    error
}

func (sink *captureSink) InsertForecastPoints(_ context.Context, points []Point) error {
	sink.points = append([]Point(nil), points...)
	return sink.err
}

func (sink *captureSink) HasForecastJob(_ context.Context, _ string, expectedCount int) (bool, error) {
	return len(sink.points) == expectedCount, nil
}
func (sink *captureSink) ReadForecastJob(_ context.Context, _ string) ([]Point, error) {
	return append([]Point(nil), sink.points...), sink.err
}

type memoryPublicationStore struct {
	started   bool
	begun     []Publication
	completed []Publication
	failed    []string
	latest    ForecastSnapshotReference
}

func (store *memoryPublicationStore) StartJob(context.Context, Request, time.Time) error {
	store.started = true
	return nil
}
func (store *memoryPublicationStore) BeginPublication(_ context.Context, publication Publication, _ time.Time) error {
	store.begun = append(store.begun, publication)
	return nil
}
func (store *memoryPublicationStore) CompletePublication(_ context.Context, publication Publication, _ time.Time) error {
	store.completed = append(store.completed, publication)
	return nil
}
func (store *memoryPublicationStore) FailJob(_ context.Context, _ Request, code string, _ time.Time) error {
	store.failed = append(store.failed, code)
	return nil
}
func (store *memoryPublicationStore) ListStalePublications(context.Context, time.Time, int) ([]Publication, error) {
	return nil, nil
}
func (store *memoryPublicationStore) LatestForecast(context.Context, string, string, string) (ForecastSnapshotReference, error) {
	if store.latest.SnapshotID == "" {
		return ForecastSnapshotReference{}, ErrForecastNotFound
	}
	return store.latest, nil
}

func validRequest() Request {
	origin := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	return Request{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Target: "SITE_LOAD",
		ForecastJobID:      "01990000-1700-7000-8000-000000000001",
		ForecastSnapshotID: "01990000-1710-7000-8000-000000000001",
		DeploymentID:       "01990000-1720-7000-8000-000000000001",
		ModelID:            "01990000-1730-7000-8000-000000000001",
		ModelVersionID:     "01990000-1740-7000-8000-000000000001", ModelVersion: 3,
		FeatureSetVersionID: "01990000-1750-7000-8000-000000000001", FeatureSetVersion: 7,
		InputSnapshotID:   "01990000-1760-7000-8000-000000000001",
		TopologyVersionID: "01990000-1770-7000-8000-000000000001",
		ForecastOrigin:    origin, HorizonMinutes: 1440, Granularity: "15MIN", Unit: "kW",
		Observations: []Observation{
			{ObservedAt: origin.Add(-60 * time.Minute), Value: 760},
			{ObservedAt: origin.Add(-45 * time.Minute), Value: 775},
			{ObservedAt: origin.Add(-30 * time.Minute), Value: 790},
			{ObservedAt: origin.Add(-15 * time.Minute), Value: 805},
		},
	}
}

func TestTrendForecastPublishesModelResultWithUncertainty(t *testing.T) {
	sink := &captureSink{}
	publication := &memoryPublicationStore{}
	generatedAt := time.Date(2026, 8, 12, 23, 59, 0, 0, time.UTC)
	service, err := NewService(sink, publication, func() time.Time { return generatedAt })
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 96 || len(sink.points) != 96 || len(publication.completed) != 1 {
		t.Fatalf("points=%d persisted=%d completed=%d", len(points), len(sink.points), len(publication.completed))
	}
	first, last := points[0], points[len(points)-1]
	if first.Quality != "VALID" || first.LowerBound == nil || first.UpperBound == nil {
		t.Fatalf("model result must expose VALID quality and uncertainty: %#v", first)
	}
	if first.Value <= request.Observations[len(request.Observations)-1].Value || last.Value <= first.Value {
		t.Fatalf("trend model did not project the observed slope: first=%f last=%f", first.Value, last.Value)
	}
	if publication.completed[0].Quality != "VALID" {
		t.Fatalf("publication quality=%s", publication.completed[0].Quality)
	}
	if first.ForecastJobID != request.ForecastJobID || first.ForecastSnapshotID != request.ForecastSnapshotID ||
		first.DeploymentID != request.DeploymentID || first.ModelID != request.ModelID || first.ModelVersionID != request.ModelVersionID ||
		first.FeatureSetVersionID != request.FeatureSetVersionID || first.InputSnapshotID != request.InputSnapshotID || first.TopologyVersionID != request.TopologyVersionID {
		t.Fatalf("traceability drift=%#v", first)
	}
}

func TestShortHistoryIsExplicitFallbackAndVisiblyDifferentFromModelOutput(t *testing.T) {
	sink := &captureSink{}
	publication := &memoryPublicationStore{}
	service, _ := NewService(sink, publication, time.Now)
	request := validRequest()
	request.Observations = request.Observations[:1]
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if points[0].Quality != "FALLBACK" || points[0].LowerBound != nil || points[0].UpperBound != nil {
		t.Fatalf("fallback must be distinguishable from modeled output: %#v", points[0])
	}
	if points[0].Value != request.Observations[0].Value || publication.completed[0].Quality != "FALLBACK" {
		t.Fatalf("fallback result=%#v publication=%#v", points[0], publication.completed[0])
	}
}

func TestLatestForecastRequiresPersistedSnapshotAndMatchingHistory(t *testing.T) {
	sink := &captureSink{}
	publication := &memoryPublicationStore{}
	service, _ := NewService(sink, publication, time.Now)
	request := validRequest()
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	publication.latest = ForecastSnapshotReference{
		SnapshotID: request.ForecastSnapshotID, ForecastJobID: request.ForecastJobID, DeploymentID: request.DeploymentID,
		ModelVersionID: request.ModelVersionID, InputSnapshotID: request.InputSnapshotID, SubjectType: request.SubjectType,
		SubjectID: request.SubjectID, Target: request.Target, ForecastOrigin: request.ForecastOrigin,
		WindowStart: points[0].ForecastFor, WindowEnd: points[len(points)-1].ForecastFor, ResultCount: len(points), Quality: points[0].Quality,
	}
	published, err := service.LatestForecast(t.Context(), request.TenantID, request.SiteID, request.Target)
	if err != nil {
		t.Fatal(err)
	}
	if published.Snapshot.SnapshotID != request.ForecastSnapshotID || len(published.Points) != len(points) {
		t.Fatalf("published=%#v", published)
	}
	publication.latest.Quality = "FALLBACK"
	if _, err = service.LatestForecast(t.Context(), request.TenantID, request.SiteID, request.Target); err == nil {
		t.Fatal("expected mismatched history quality to fail closed")
	}
}

func TestForecastFailsClosedWithoutInput(t *testing.T) {
	sink := &captureSink{}
	publication := &memoryPublicationStore{}
	service, _ := NewService(sink, publication, time.Now)
	request := validRequest()
	request.Observations = nil
	if _, err := service.Forecast(t.Context(), request); err == nil {
		t.Fatal("expected missing observations to fail")
	}
	if publication.started || len(sink.points) != 0 {
		t.Fatal("invalid no-input request must not start or persist a forecast")
	}
}

func TestForecastRejectsScopeAndGranularityDrift(t *testing.T) {
	service, _ := NewService(&captureSink{}, &memoryPublicationStore{}, time.Now)
	for name, mutate := range map[string]func(*Request){
		"site subject drift":   func(request *Request) { request.SubjectID = "01990000-5000-7000-8000-000000000002" },
		"unknown target":       func(request *Request) { request.Target = "GRID_IMPORT" },
		"bad granularity":      func(request *Request) { request.Granularity = "5MIN" },
		"nondivisible horizon": func(request *Request) { request.HorizonMinutes = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			request := validRequest()
			mutate(&request)
			if _, err := service.Forecast(t.Context(), request); err == nil {
				t.Fatal("expected request to fail")
			}
		})
	}
}

func TestForecastDoesNotReportSuccessWhenHistoryWriteFails(t *testing.T) {
	sink := &captureSink{err: errors.New("clickhouse unavailable")}
	publication := &memoryPublicationStore{}
	service, _ := NewService(sink, publication, time.Now)
	if _, err := service.Forecast(t.Context(), validRequest()); err == nil {
		t.Fatal("expected persistence failure")
	}
	if len(publication.failed) != 1 || publication.failed[0] != "CLICKHOUSE_WRITE_FAILED" {
		t.Fatalf("failure evidence=%v", publication.failed)
	}
}
