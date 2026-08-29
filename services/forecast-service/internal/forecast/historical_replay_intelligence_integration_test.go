package forecast

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHistoricalReplayMetricFactsPrepareAndPublishDayAheadForecast(t *testing.T) {
	baseURL := os.Getenv("HISTORICAL_REPLAY_CLICKHOUSE_URL")
	postgresDSN := os.Getenv("HISTORICAL_REPLAY_FORECAST_DSN")
	if baseURL == "" || postgresDSN == "" {
		t.Skip("Historical Replay Forecast acceptance environment is not configured")
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
	if prepared.Status != "PENDING" || prepared.ForecastJobID == "" || prepared.InputSnapshotID == "" || prepared.ForecastSnapshotID == "" {
		t.Fatalf("prepared replay Forecast=%#v", prepared)
	}

	jobs, err := store.ClaimForecastJobs(t.Context(), "historical-replay-intelligence-acceptance", 1, time.Minute, origin.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != prepared.ForecastJobID {
		t.Fatalf("claimed replay Forecast jobs=%#v prepared=%#v", jobs, prepared)
	}
	started, err := store.StartForecastJob(t.Context(), jobs[0], time.Minute, origin.Add(2*time.Second))
	if err != nil || !started {
		t.Fatalf("start replay Forecast job=%t err=%v", started, err)
	}
	reference, err := ValidateForecastSchedulerJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.LoadForecastRequest(t.Context(), jobs[0].TenantID, jobs[0].SiteID, reference)
	if err != nil {
		t.Fatal(err)
	}
	if request.InputSnapshotID != prepared.InputSnapshotID || request.HorizonMinutes != 1440 || request.Granularity != "15MIN" {
		t.Fatalf("replay frozen Forecast request=%#v", request)
	}
	if len(request.Observations) != 4 {
		t.Fatalf("replay frozen Forecast observations=%#v", request.Observations)
	}
	want := []float64{760, 790, 800, 820}
	for index, value := range want {
		if request.Observations[index].Value != value {
			t.Fatalf("replay Forecast observation[%d]=%v want=%v", index, request.Observations[index].Value, value)
		}
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
	if len(points) != 96 || points[0].Quality != "VALID" || points[len(points)-1].HorizonMinutes != 1440 {
		t.Fatalf("replay day-ahead Forecast points=%#v", points)
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
	if published.Snapshot.SnapshotID != prepared.ForecastSnapshotID || published.Snapshot.InputSnapshotID != prepared.InputSnapshotID || len(published.Points) != 96 {
		t.Fatalf("published replay Forecast=%#v prepared=%#v", published, prepared)
	}
}
