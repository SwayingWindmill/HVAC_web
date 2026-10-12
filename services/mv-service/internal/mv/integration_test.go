package mv

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// The analytics history runner points these at a disposable registry seeded with an import Tariff
// for the Site, and at ClickHouse holding hourly Metric results for it.
func integrationEnv(t *testing.T, names ...string) []string {
	t.Helper()
	values := make([]string, len(names))
	for index, name := range names {
		if values[index] = strings.TrimSpace(os.Getenv(name)); values[index] == "" {
			t.Skipf("%s is not configured", name)
		}
	}
	return values
}

func TestPostgresMVRuntimeKeepsOneReportingMeasureAndVerifiesDaysOnce(t *testing.T) {
	env := integrationEnv(t, "MV_POSTGRES_INTEGRATION_DSN", "MV_POSTGRES_INTEGRATION_TENANT_ID", "MV_POSTGRES_INTEGRATION_SITE_ID", "MV_POSTGRES_INTEGRATION_ACTOR_ID")
	dsn, tenantID, siteID, actor := env[0], env[1], env[2], env[3]
	store, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	implemented := time.Date(2026, 8, 20, 9, 0, 0, 0, shanghai)
	until := time.Date(2026, 8, 24, 2, 0, 0, 0, shanghai)
	service, err := NewService(store, plantHistory{changedAt: implemented, factor: 0.9}, store, func() time.Time { return until }, schedulerjob.UUIDv7)
	if err != nil {
		t.Fatal(err)
	}
	input := MeasureInput{TenantID: tenantID, SiteID: siteID, Kind: KindSetpointReset, PlannedAt: implemented, Actor: actor}
	input.Name = "提高冷冻水供水温度"
	first, err := service.Register(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "冷却塔风机变频"
	second, err := service.Register(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first, err = service.Implement(t.Context(), tenantID, siteID, first.ID, implemented, actor); err != nil || !first.Baseline.Trusted {
		t.Fatalf("implement = %+v err = %v", first, err)
	}

	// The table refuses a second reporting measure even when a writer skips the service's check.
	later := implemented.Add(48 * time.Hour)
	second.Status, second.ImplementedAt, second.Baseline = StatusImplemented, &later, first.Baseline
	if err := store.UpdateMeasure(t.Context(), second); !errors.Is(err, ErrReportingOverlap) {
		t.Fatalf("overlapping update = %v", err)
	}

	for range 2 {
		if err := service.VerifyDueDays(t.Context(), tenantID, siteID, until); err != nil {
			t.Fatal(err)
		}
	}
	results, err := store.DailyResults(t.Context(), tenantID, siteID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Day != "2026-08-21" || results[0].ExpectedHours != 15 || results[2].Day != "2026-08-23" || results[2].Hours != 24 {
		t.Fatalf("results = %+v", results)
	}
	if _, err := service.Refit(t.Context(), tenantID, siteID, first.ID, implemented.Add(-7*24*time.Hour), implemented, actor); !errors.Is(err, ErrBaselineLocked) {
		t.Fatalf("refit after results = %v", err)
	}
}

// The runner seeds one READY MV_EVALUATION job as the Scheduler would; the M&V runtime role can
// claim, start and complete it.
func TestPostgresMVRuntimeWorksItsScheduledEvaluations(t *testing.T) {
	env := integrationEnv(t, "MV_POSTGRES_INTEGRATION_DSN", "MV_POSTGRES_INTEGRATION_JOB_ID")
	store, err := OpenPostgres(t.Context(), env[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	queue, err := store.EvaluationQueue()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	jobs, err := queue.Claim(t.Context(), "mv-integration", 10, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != env[1] || jobs[0].JobType != EvaluationJobType {
		t.Fatalf("claimed = %#v", jobs)
	}
	started, err := queue.Start(t.Context(), jobs[0], time.Minute, now)
	if err != nil || !started {
		t.Fatalf("started = %v err = %v", started, err)
	}
	if err = queue.Complete(t.Context(), jobs[0], map[string]any{"settledBy": now}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

// The runner writes two hours with both Metrics (the second hour revised once) and a third hour with
// electricity alone; M&V reads the latest revision of the two complete hours.
func TestClickHouseHoursReadTheLatestRevisionOfCompleteHours(t *testing.T) {
	env := integrationEnv(t, "MV_CLICKHOUSE_INTEGRATION_URL", "MV_POSTGRES_INTEGRATION_TENANT_ID", "MV_POSTGRES_INTEGRATION_SITE_ID")
	reader, err := NewClickHouseHours(env[0], "mv_service_reader", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	samples, err := reader.HourlySamples(t.Context(), env[1], env[2], from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || !samples[0].Start.Equal(from) || samples[0].EnergyKWh != 210 || samples[0].CoolingKWh != 740 || samples[1].EnergyKWh != 190 {
		t.Fatalf("samples = %+v", samples)
	}
}
