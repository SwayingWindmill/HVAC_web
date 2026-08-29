package optimization

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHistoricalReplayForecastAndCurrentStatePrepareSealedOptimization(t *testing.T) {
	baseURL := os.Getenv("HISTORICAL_REPLAY_CLICKHOUSE_URL")
	postgresDSN := os.Getenv("HISTORICAL_REPLAY_OPTIMIZATION_DSN")
	if baseURL == "" || postgresDSN == "" {
		t.Skip("Historical Replay Optimization acceptance environment is not configured")
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
		t.Fatalf("prepared replay Optimization=%#v", prepared)
	}
	var snapshotStatus, loadForecastSnapshotID string
	if err := pool.QueryRow(t.Context(), `
SELECT snapshot.status, snapshot.load_forecast_snapshot_id::text
FROM core_registry.optimization_input_snapshots snapshot
WHERE snapshot.tenant_id=$1::uuid AND snapshot.site_id=$2::uuid AND snapshot.id=$3::uuid
`, "018f1d00-0000-7000-8000-000000000001", "018f1e00-1000-7000-8000-000000000001", prepared.InputSnapshotID).Scan(&snapshotStatus, &loadForecastSnapshotID); err != nil {
		t.Fatal(err)
	}
	if snapshotStatus != "SEALED" || loadForecastSnapshotID == "" {
		t.Fatalf("replay Optimization input status=%s forecast=%s", snapshotStatus, loadForecastSnapshotID)
	}
	var forecastCount int
	if err := pool.QueryRow(t.Context(), `
SELECT count(*)
FROM core_registry.forecast_snapshots snapshot
JOIN core_registry.forecast_jobs job ON job.tenant_id=snapshot.tenant_id AND job.site_id=snapshot.site_id AND job.id=snapshot.forecast_job_id
WHERE snapshot.id=$1::uuid AND job.status='PERSISTED' AND snapshot.result_count=96
`, loadForecastSnapshotID).Scan(&forecastCount); err != nil {
		t.Fatal(err)
	}
	if forecastCount != 1 {
		t.Fatalf("replay Optimization did not use published Forecast owner snapshot %s", loadForecastSnapshotID)
	}

	jobs, err := store.ClaimOptimizationJobs(t.Context(), "historical-replay-intelligence-acceptance", 1, time.Minute, origin.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != prepared.OptimizationRunID {
		t.Fatalf("claimed replay Optimization jobs=%#v prepared=%#v", jobs, prepared)
	}
	started, err := store.StartOptimizationJob(t.Context(), jobs[0], time.Minute, origin.Add(2*time.Second))
	if err != nil || !started {
		t.Fatalf("start replay Optimization job=%t err=%v", started, err)
	}
	reference, err := ValidateOptimizationSchedulerJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.LoadOptimizationRequest(t.Context(), jobs[0].TenantID, jobs[0].SiteID, reference)
	if err != nil {
		t.Fatal(err)
	}
	if request.InputSnapshotID != prepared.InputSnapshotID || request.LoadForecastSnapshotID != loadForecastSnapshotID {
		t.Fatalf("replay frozen Optimization lineage=%#v", request)
	}
	if request.Baseline.DailyEnergyKWh != 60 || request.Baseline.DailyCost != 90 || request.Baseline.SupplyTempC != 7 || request.Baseline.ZoneTempC != 23 {
		t.Fatalf("replay frozen Optimization authoritative inputs=%#v", request.Baseline)
	}
	if request.TariffVersionID == "" || request.TopologyVersionID == "" || request.PolicyVersionID == "" {
		t.Fatalf("replay frozen Optimization constraints=%#v", request)
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
		t.Fatalf("replay recommendation=%#v", recommendation)
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
		t.Fatalf("published replay recommendation=%#v", published)
	}
}
