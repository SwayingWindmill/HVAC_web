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

func validRequest() Request {
	value := 812.5
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
		ForecastOrigin:    time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		HorizonMinutes:    1440, Granularity: "15MIN", LastValue: &value, Unit: "kW",
	}
}

func TestBaselineForecastPreservesTraceabilityAndFutureGrid(t *testing.T) {
	sink := &captureSink{}
	generatedAt := time.Date(2026, 8, 12, 23, 59, 0, 0, time.UTC)
	service, err := NewService(sink, func() time.Time { return generatedAt })
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 96 || len(sink.points) != 96 {
		t.Fatalf("points=%d persisted=%d", len(points), len(sink.points))
	}
	first, last := points[0], points[len(points)-1]
	if !first.ForecastFor.Equal(request.ForecastOrigin.Add(15*time.Minute)) || !last.ForecastFor.Equal(request.ForecastOrigin.Add(24*time.Hour)) {
		t.Fatalf("forecast grid first=%s last=%s", first.ForecastFor, last.ForecastFor)
	}
	if first.HorizonMinutes != 15 || last.HorizonMinutes != 1440 {
		t.Fatalf("horizons first=%d last=%d", first.HorizonMinutes, last.HorizonMinutes)
	}
	if first.Quality != "FALLBACK" || first.Value != *request.LastValue || first.Unit != "kW" || !first.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("first point=%#v", first)
	}
	if first.ForecastJobID != request.ForecastJobID || first.ForecastSnapshotID != request.ForecastSnapshotID ||
		first.DeploymentID != request.DeploymentID || first.ModelID != request.ModelID ||
		first.ModelVersionID != request.ModelVersionID || first.ModelVersion != request.ModelVersion ||
		first.FeatureSetVersionID != request.FeatureSetVersionID || first.FeatureSetVersion != request.FeatureSetVersion ||
		first.InputSnapshotID != request.InputSnapshotID || first.TopologyVersionID != request.TopologyVersionID {
		t.Fatalf("traceability drift=%#v", first)
	}
	if first.ForecastID != deterministicForecastID(request.ForecastJobID, first.ForecastFor) {
		t.Fatalf("forecast id=%s", first.ForecastID)
	}
}

func TestForecastFailsClosedWithoutLastValue(t *testing.T) {
	sink := &captureSink{}
	service, err := NewService(sink, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	request.LastValue = nil
	if _, err := service.Forecast(t.Context(), request); err == nil {
		t.Fatal("expected missing last value to fail")
	}
	if len(sink.points) != 0 {
		t.Fatalf("persisted points=%d", len(sink.points))
	}
}

func TestForecastRejectsScopeAndGranularityDrift(t *testing.T) {
	service, _ := NewService(&captureSink{}, time.Now)
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
	service, _ := NewService(sink, time.Now)
	if _, err := service.Forecast(t.Context(), validRequest()); err == nil {
		t.Fatal("expected persistence failure")
	}
}
