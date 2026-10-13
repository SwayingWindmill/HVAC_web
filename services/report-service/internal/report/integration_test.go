package report

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// report-service works a REPORT_GENERATE job as report_runtime: it reads the month's valid Site
// energy as report_service_reader and keeps the snapshot as the month's first version.
func TestReportRuntimeSnapshotsTheMonthItsJobNames(t *testing.T) {
	dsn := os.Getenv("REPORT_POSTGRES_INTEGRATION_DSN")
	clickHouseURL := os.Getenv("REPORT_CLICKHOUSE_INTEGRATION_URL")
	if dsn == "" || clickHouseURL == "" {
		t.Skip("REPORT_POSTGRES_INTEGRATION_DSN and REPORT_CLICKHOUSE_INTEGRATION_URL are not configured")
	}
	tenantID, siteID := os.Getenv("REPORT_INTEGRATION_TENANT_ID"), os.Getenv("REPORT_INTEGRATION_SITE_ID")
	ctx := context.Background()
	store, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hours, err := NewClickHouseHours(clickHouseURL, "report_service_reader", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, hours, time.Now, schedulerjob.UUIDv7)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := store.GenerationQueue()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunScheduledGenerations(ctx, queue, service, "report-integration", 4); err != nil {
		t.Fatal(err)
	}

	stored, err := store.LatestSnapshot(ctx, tenantID, siteID, "2026-07")
	if err != nil {
		t.Fatal(err)
	}
	// The runner wrote 12 and 30 kWh of valid electricity and 40 of cooling on 2026-07-10, and an
	// invalid interval and a sub-meter's interval that the report leaves out.
	report := stored.Report
	if stored.Version != 1 || stored.SchedulerJobID == nil || *stored.SchedulerJobID != os.Getenv("REPORT_INTEGRATION_JOB_ID") {
		t.Fatalf("stored = %#v", stored)
	}
	if report.DaysWithData != 1 || report.ElectricityKWh != 42 || report.CoolingKWh != 40 || len(report.Days) != 1 || report.Days[0].Date != "2026-07-10" {
		t.Fatalf("report = %#v", report)
	}
	if _, err := store.LatestSnapshot(ctx, tenantID, siteID, "2026-06"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("a month without a snapshot = %v", err)
	}
	year, err := store.YearSnapshots(ctx, tenantID, siteID, 2026)
	if err != nil || len(year) != 1 || year[0].Month != "2026-07" {
		t.Fatalf("year = %#v err=%v", year, err)
	}
}
