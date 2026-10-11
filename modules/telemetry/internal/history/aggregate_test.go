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

// Demand is billed on quarter-hour average power, so quarter-hours follow the Site's clock.
func TestQuarterHourPeriodsFollowTheSiteClock(t *testing.T) {
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"hvac_meter.active_power"}, Granularity: telemetryhistorymodel.AggregateGranularityQuarterHour,
		Timezone: "Asia/Kolkata", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
		// 05:37–06:30 Site time (UTC+05:30).
		From: time.Date(2026, 10, 11, 0, 7, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
	}
	periods, err := buildCalendarPeriods(query)
	if err != nil {
		t.Fatal(err)
	}
	starts := []time.Time{query.From, time.Date(2026, 10, 11, 0, 15, 0, 0, time.UTC), time.Date(2026, 10, 11, 0, 30, 0, 0, time.UTC), time.Date(2026, 10, 11, 0, 45, 0, 0, time.UTC)}
	if len(periods) != len(starts) {
		t.Fatalf("periods=%#v", periods)
	}
	for index, period := range periods {
		if !period.Start.Equal(starts[index]) || !period.End.Equal(starts[index].Add(15*time.Minute).Truncate(15*time.Minute)) {
			t.Fatalf("period %d=%#v", index, period)
		}
	}
	bucket, next := aggregateBucketExpressions(query)
	if bucket != "toStartOfInterval(sampled_at, INTERVAL 15 MINUTE, 'Asia/Kolkata')" || next != "addMinutes(bucket, 15)" {
		t.Fatalf("bucket=%s next=%s", bucket, next)
	}
}
