package metric

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	replayIntelligenceTenantID        = "018f1d00-0000-7000-8000-000000000001"
	replayIntelligenceSiteID          = "018f1e00-1000-7000-8000-000000000001"
	replayIntelligenceLoadPointID     = "01990000-3481-7000-8000-000000000001"
	replayIntelligenceMetricID        = "01990000-3483-7000-8000-000000000001"
	replayIntelligenceMetricVersionID = "01990000-3483-7000-8000-000000000002"
	replayIntelligenceBindingID       = "01990000-3483-7000-8000-000000000003"
)

func TestHistoricalReplayHistoryProjectsForecastMetricFacts(t *testing.T) {
	postgresDSN := os.Getenv("HISTORICAL_REPLAY_REGISTRY_DSN")
	clickHouseURL := os.Getenv("HISTORICAL_REPLAY_CLICKHOUSE_URL")
	if postgresDSN == "" || clickHouseURL == "" {
		t.Skip("Historical Replay Metric acceptance environment is not configured")
	}
	pool, err := pgxpool.New(t.Context(), postgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	registry, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	series, err := NewClickHouseStore(clickHouseURL, "metric_engine_runtime", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(registry, series, replayAcceptanceLatest{})
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return time.Date(2026, 8, 28, 11, 59, 10, 0, time.UTC) }

	start := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	want := []float64{760, 790, 800, 820}
	for index, expected := range want {
		periodStart := start.Add(time.Duration(index) * 15 * time.Minute)
		result, executeErr := engine.Execute(t.Context(), RunRequest{
			TenantID: replayIntelligenceTenantID, SiteID: replayIntelligenceSiteID,
			BindingID:      replayIntelligenceBindingID,
			SchedulerJobID: replayMetricSchedulerJobID(index),
			PeriodStart:    periodStart, PeriodEnd: periodStart.Add(15 * time.Minute), Reason: "BACKFILL",
		})
		if executeErr != nil {
			t.Fatal(executeErr)
		}
		if result.Value != expected || result.Quality != "GOOD" || result.Completeness != 1 {
			t.Fatalf("replay Metric result[%d]=%#v", index, result)
		}
		if len(result.Inputs) != 1 || result.Inputs[0].Reference != "point:"+replayIntelligenceLoadPointID {
			t.Fatalf("replay Metric provenance[%d]=%#v", index, result.Inputs)
		}
	}

	var count int
	if err := pool.QueryRow(t.Context(), `
SELECT count(*) FROM core_registry.metric_result_heads
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND metric_id=$3::uuid
  AND subject_type='SITE' AND subject_id=$2::uuid AND granularity='15MIN' AND current_revision > 0
`, replayIntelligenceTenantID, replayIntelligenceSiteID, replayIntelligenceMetricID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Fatalf("replay Metric current heads=%d want=%d", count, len(want))
	}

	dayStart := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	dayEnd := time.Date(2026, 8, 28, 11, 59, 0, 0, time.UTC)
	dailyEnergy, err := engine.Execute(t.Context(), RunRequest{
		TenantID: replayIntelligenceTenantID, SiteID: replayIntelligenceSiteID,
		BindingID:      "01990000-3483-7000-8000-000000000006",
		SchedulerJobID: "01990000-3484-7000-8000-000000000005",
		PeriodStart:    dayStart, PeriodEnd: dayEnd, Reason: "BACKFILL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dailyEnergy.Value != 60 || dailyEnergy.Binding.MetricCode != "daily_energy" || dailyEnergy.Quality != "GOOD" {
		t.Fatalf("replay daily energy=%#v", dailyEnergy)
	}
	if len(dailyEnergy.Inputs) != 1 || dailyEnergy.Inputs[0].Reference != "point:01990000-3481-7000-8000-000000000002" {
		t.Fatalf("replay daily energy provenance=%#v", dailyEnergy.Inputs)
	}

	energyCost, err := engine.Execute(t.Context(), RunRequest{
		TenantID: replayIntelligenceTenantID, SiteID: replayIntelligenceSiteID,
		BindingID:      "01990000-3483-7000-8000-000000000009",
		SchedulerJobID: "01990000-3484-7000-8000-000000000006",
		PeriodStart:    dayStart, PeriodEnd: dayEnd, Reason: "BACKFILL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if energyCost.Value != 90 || energyCost.Binding.MetricCode != "energy_cost" || energyCost.Binding.Unit != "CNY" || energyCost.Quality != "GOOD" {
		t.Fatalf("replay energy cost=%#v", energyCost)
	}
	if len(energyCost.Inputs) != 1 || energyCost.Inputs[0].Reference == "" {
		t.Fatalf("replay energy cost provenance=%#v", energyCost.Inputs)
	}
}

type replayAcceptanceLatest struct{}

func (replayAcceptanceLatest) PutMetric(context.Context, Result) error { return nil }

func replayMetricSchedulerJobID(index int) string {
	return []string{
		"01990000-3484-7000-8000-000000000001",
		"01990000-3484-7000-8000-000000000002",
		"01990000-3484-7000-8000-000000000003",
		"01990000-3484-7000-8000-000000000004",
	}[index]
}
