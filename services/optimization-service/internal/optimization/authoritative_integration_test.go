package optimization

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuthoritativeOwnersPrepareSealedOptimizationAndPublishRecommendation(t *testing.T) {
	baseURL := os.Getenv("OPTIMIZATION_CLICKHOUSE_TEST_URL")
	postgresDSN := os.Getenv("OPTIMIZATION_POSTGRES_TEST_DSN")
	if baseURL == "" || postgresDSN == "" {
		t.Skip("OPTIMIZATION_CLICKHOUSE_TEST_URL and OPTIMIZATION_POSTGRES_TEST_DSN are required")
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
	inputs, err := NewClickHouseInputReader(InputReaderConfig{BaseURL: baseURL, Username: "optimization_service_reader"})
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := NewPreparer(store, inputs, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(t.Context(), PreparationRequest{
		TenantID: "018f1d00-0000-7000-8000-000000000001", SiteID: "018f1e00-1000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "018f1e00-1000-7000-8000-000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != "PENDING" || prepared.OptimizationRunID == "" || prepared.InputSnapshotID == "" {
		t.Fatalf("prepared=%#v", prepared)
	}

	jobs, err := store.ClaimOptimizationJobs(t.Context(), "optimization-authoritative-input-tracer", 1, time.Minute, origin.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != prepared.OptimizationRunID {
		t.Fatalf("claimed=%#v prepared=%#v", jobs, prepared)
	}
	started, err := store.StartOptimizationJob(t.Context(), jobs[0], time.Minute, origin.Add(2*time.Second))
	if err != nil || !started {
		t.Fatalf("started=%t err=%v", started, err)
	}
	reference, err := ValidateOptimizationSchedulerJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.LoadOptimizationRequest(t.Context(), jobs[0].TenantID, jobs[0].SiteID, reference)
	if err != nil {
		t.Fatal(err)
	}
	if request.InputSnapshotID != prepared.InputSnapshotID || request.Baseline.DailyEnergyKWh != 2400 || request.Baseline.DailyCost != 360 || request.Baseline.SupplyTempC != 7 || request.Baseline.ZoneTempC != 23 {
		t.Fatalf("frozen request=%#v", request)
	}
	if request.LoadForecastSnapshotID != "01990000-2660-7000-8000-000000000001" || request.TariffVersionID != "01990000-2740-7000-8000-000000000001" || request.TopologyVersionID != "01990000-2300-7000-8000-000000000001" {
		t.Fatalf("authoritative lineage=%#v", request)
	}

	evaluations, err := NewClickHouseSink(baseURL, "optimization_service_writer", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewDefaultService(store, evaluations, func() time.Time { return origin.Add(3 * time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	recommendation, err := service.Optimize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.InputSnapshotID != prepared.InputSnapshotID || recommendation.CommandIntentID != "" || recommendation.Approval != "DRAFT" {
		t.Fatalf("recommendation=%#v", recommendation)
	}
	if err = store.CompleteOptimizationJob(t.Context(), jobs[0], map[string]any{
		"optimizationRunId": prepared.OptimizationRunID, "recommendationId": recommendation.ID,
	}, origin.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	published, err := service.GetRecommendation(t.Context(), request.TenantID, request.SiteID, prepared.OptimizationRunID)
	if err != nil {
		t.Fatal(err)
	}
	if published.RunID != prepared.OptimizationRunID || published.Recommendation.InputSnapshotID != prepared.InputSnapshotID || published.Recommendation.CommandIntentID != "" {
		t.Fatalf("published=%#v", published)
	}
}
