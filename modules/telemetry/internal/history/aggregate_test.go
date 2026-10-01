package history

import (
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

func TestCalendarPeriodsHonorDSTBoundaries(t *testing.T) {
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"zone.temperature"}, Granularity: telemetryhistorymodel.AggregateGranularityDay,
		Timezone: "America/New_York", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
		From: time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC),
	}
	periods, err := buildCalendarPeriods(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 || periods[0].End.Sub(periods[0].Start) != 23*time.Hour {
		t.Fatalf("spring-forward periods=%#v", periods)
	}

	query.From = time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	query.To = time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)
	periods, err = buildCalendarPeriods(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 || periods[0].End.Sub(periods[0].Start) != 25*time.Hour {
		t.Fatalf("fall-back periods=%#v", periods)
	}
}

func TestCounterAggregateQueryKeepsResetRolloverRevisionAndQualitySemantics(t *testing.T) {
	client := &Client{database: "telemetry_history", table: "observations"}
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"energy_total"}, From: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		Granularity: telemetryhistorymodel.AggregateGranularityDay, Timezone: "Asia/Singapore", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
	}
	sql := client.counterAggregateQuery(query, query.To)
	for _, marker := range []string{"RESET_TO_ZERO", "ROLLOVER", "REVISION_BOUNDARY", "UNIT_BOUNDARY", "previous_quality", "OUT_OF_ORDER", "point_revision"} {
		if !strings.Contains(sql, marker) {
			t.Fatalf("counter SQL missing %s:\n%s", marker, sql)
		}
	}
	if !strings.Contains(sql, "quality IN ('GOOD', 'PARTIAL', 'ESTIMATED', 'MANUAL')") {
		t.Fatalf("usable quality policy missing:\n%s", sql)
	}
}
