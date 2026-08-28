package forecast

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuthoritativeHistoryPreparesAndPublishesServerOwnedForecast(t *testing.T) {
	baseURL := os.Getenv("FORECAST_CLICKHOUSE_TEST_URL")
	postgresDSN := os.Getenv("FORECAST_POSTGRES_TEST_DSN")
	if baseURL == "" || postgresDSN == "" {
		t.Skip("FORECAST_CLICKHOUSE_TEST_URL and FORECAST_POSTGRES_TEST_DSN are required")
	}
	origin := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	pool, err := pgxpool.New(t.Context(), postgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	history, err := NewClickHouseHistoryReader(HistoryConfig{BaseURL: baseURL, Username: "forecast_service_reader"})
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := NewPreparer(store, history, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(t.Context(), PreparationRequest{
		TenantID: "018f1d00-0000-7000-8000-000000000001", SiteID: "018f1e00-1000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "018f1e00-1000-7000-8000-000000000001", Target: "SITE_LOAD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != "PENDING" || prepared.ForecastJobID == "" || prepared.InputSnapshotID == "" {
		t.Fatalf("prepared=%#v", prepared)
	}

	jobs, err := store.ClaimForecastJobs(t.Context(), "forecast-authoritative-history-tracer", 1, time.Minute, origin.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != prepared.ForecastJobID {
		t.Fatalf("claimed jobs=%#v prepared=%#v", jobs, prepared)
	}
	started, err := store.StartForecastJob(t.Context(), jobs[0], time.Minute, origin.Add(2*time.Second))
	if err != nil || !started {
		t.Fatalf("started=%t err=%v", started, err)
	}
	reference, err := ValidateForecastSchedulerJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.LoadForecastRequest(t.Context(), jobs[0].TenantID, jobs[0].SiteID, reference)
	if err != nil {
		t.Fatal(err)
	}
	if request.InputSnapshotID != prepared.InputSnapshotID || len(request.Observations) != 4 || request.Observations[3].Value != 820 {
		t.Fatalf("frozen request=%#v", request)
	}

	writer, err := NewClickHouseSink(ClickHouseConfig{BaseURL: baseURL, Database: "analytics", Table: "forecast_series", Username: "forecast_service_writer"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(writer, store, func() time.Time { return origin.Add(3 * time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 4 || points[0].Quality != "VALID" {
		t.Fatalf("points=%#v", points)
	}
	if err = store.CompleteForecastJob(t.Context(), jobs[0], map[string]any{
		"forecastSnapshotId": prepared.ForecastSnapshotID, "forecastJobId": prepared.ForecastJobID,
		"pointCount": len(points), "quality": points[0].Quality,
	}, origin.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}

	reader, err := NewClickHouseSink(ClickHouseConfig{BaseURL: baseURL, Database: "analytics", Table: "forecast_series", Username: "forecast_service_reader"})
	if err != nil {
		t.Fatal(err)
	}
	readService, err := NewService(reader, store, func() time.Time { return origin.Add(5 * time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	published, err := readService.LatestForecast(t.Context(), request.TenantID, request.SiteID, request.Target)
	if err != nil {
		t.Fatal(err)
	}
	if published.Snapshot.InputSnapshotID != prepared.InputSnapshotID || published.Snapshot.ForecastJobID != prepared.ForecastJobID || len(published.Points) != 4 {
		t.Fatalf("published=%#v prepared=%#v", published, prepared)
	}
}

func TestClickHouseForecastSinkPersistsTraceableBaseline(t *testing.T) {
	baseURL := os.Getenv("FORECAST_CLICKHOUSE_TEST_URL")
	if baseURL == "" {
		t.Skip("FORECAST_CLICKHOUSE_TEST_URL is not set")
	}
	sink, err := NewClickHouseSink(ClickHouseConfig{
		BaseURL:  baseURL,
		Database: "analytics",
		Table:    "forecast_series",
		Username: "forecast_service_writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 8, 13, 0, 1, 0, 0, time.UTC)
	service, err := NewService(sink, &memoryPublicationStore{}, func() time.Time { return generatedAt })
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest()
	request.HorizonMinutes = 60
	points, err := service.Forecast(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 4 {
		t.Fatalf("points=%d", len(points))
	}
	if points[0].Quality != "VALID" || points[0].LowerBound == nil || points[0].UpperBound == nil || points[3].HorizonMinutes != 60 {
		t.Fatalf("points=%#v", points)
	}
}
