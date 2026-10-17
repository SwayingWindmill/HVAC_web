package report

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// The Site's hours as the Telemetry Query Service answers them for the integration month.
type integrationHours struct{}

func (integrationHours) Hours(context.Context, string, string, *time.Location, time.Time, time.Time) ([]Hour, error) {
	return []Hour{
		{Start: time.Date(2026, 7, 10, 2, 0, 0, 0, time.UTC), ElectricityKWh: 12, CoolingKWh: 40, HasElectricity: true},
		{Start: time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC), ElectricityKWh: 30, HasElectricity: true},
	}, nil
}

// report-service works a REPORT_GENERATE job as report_runtime and keeps the snapshot as the
// month's first version.
func TestReportRuntimeSnapshotsTheMonthItsJobNames(t *testing.T) {
	dsn := os.Getenv("REPORT_POSTGRES_INTEGRATION_DSN")
	if dsn == "" {
		t.Skip("REPORT_POSTGRES_INTEGRATION_DSN is not configured")
	}
	tenantID, siteID := os.Getenv("REPORT_INTEGRATION_TENANT_ID"), os.Getenv("REPORT_INTEGRATION_SITE_ID")
	ctx := context.Background()
	store, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, err := NewService(store, integrationHours{}, time.Now, schedulerjob.UUIDv7)
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
	// 12 and 30 kWh of electricity and 40 of cooling on 2026-07-10.
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
