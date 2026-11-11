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

func TestMinuteAndFiveMinutePeriodsAlignToSiteClockAndSettingGaugeSQL(t *testing.T) {
	minuteQuery := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"chiller.leaving_chilled_water_temperature_setpoint"},
		Granularity: telemetryhistorymodel.AggregateGranularityMinute,
		Timezone: "Asia/Kolkata", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
		From: time.Date(2026, 10, 11, 0, 7, 20, 0, time.UTC),
		To:   time.Date(2026, 10, 11, 0, 10, 0, 0, time.UTC),
	}
	periods, err := buildCalendarPeriods(minuteQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 3 || !periods[0].Start.Equal(minuteQuery.From) || !periods[0].End.Equal(time.Date(2026, 10, 11, 0, 8, 0, 0, time.UTC)) || !periods[2].End.Equal(minuteQuery.To) {
		t.Fatalf("minute periods=%#v", periods)
	}
	minuteBucket, minuteNext := aggregateBucketExpressions(minuteQuery)
	if minuteBucket != "toStartOfInterval(sampled_at, INTERVAL 1 MINUTE, 'Asia/Kolkata')" || minuteNext != "addMinutes(bucket, 1)" {
		t.Fatalf("minute bucket=%s next=%s", minuteBucket, minuteNext)
	}

	fiveMinuteQuery := minuteQuery
	fiveMinuteQuery.Granularity = telemetryhistorymodel.AggregateGranularityFiveMinute
	fiveMinuteQuery.From = time.Date(2026, 10, 11, 0, 7, 0, 0, time.UTC)
	fiveMinuteQuery.To = time.Date(2026, 10, 11, 0, 20, 0, 0, time.UTC)
	fivePeriods, err := buildCalendarPeriods(fiveMinuteQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(fivePeriods) != 3 || !fivePeriods[0].Start.Equal(fiveMinuteQuery.From) || !fivePeriods[0].End.Equal(time.Date(2026, 10, 11, 0, 10, 0, 0, time.UTC)) || !fivePeriods[1].End.Equal(time.Date(2026, 10, 11, 0, 15, 0, 0, time.UTC)) {
		t.Fatalf("five-minute periods=%#v", fivePeriods)
	}
	fiveBucket, fiveNext := aggregateBucketExpressions(fiveMinuteQuery)
	if fiveBucket != "toStartOfInterval(sampled_at, INTERVAL 5 MINUTE, 'Asia/Kolkata')" || fiveNext != "addMinutes(bucket, 5)" {
		t.Fatalf("five-minute bucket=%s next=%s", fiveBucket, fiveNext)
	}

	client := &Client{database: "telemetry_history", table: "observations"}
	gaugeSQL := client.gaugeAggregateQuery(minuteQuery, minuteQuery.To)
	if !strings.Contains(gaugeSQL, "point_type IN ('TELEMETRY', 'SETTING')") {
		t.Fatalf("gauge SQL should include numeric SETTING points:\n%s", gaugeSQL)
	}
	snapshotSQL := client.aggregateSnapshotQuery(minuteQuery)
	if !strings.Contains(snapshotSQL, "'SETTING'") {
		t.Fatalf("snapshot SQL should include SETTING points:\n%s", snapshotSQL)
	}

	val := 7.0
	unit := "Cel"
	decoded, err := decodeAggregateRow(aggregateRow{
		TelemetryKey: "chiller.leaving_chilled_water_temperature_setpoint",
		PointID:      "018f2e00-4000-7000-8000-000000000001",
		PointRevision: 1,
		PointType:    "SETTING",
		Unit:         &unit,
		PeriodStart:  "2026-10-11T00:08:00.000Z",
		PeriodEnd:    "2026-10-11T00:09:00.000Z",
		Good:         1,
		TotalCount:   1,
		IncludedCount: 1,
		Average:      &val,
		Minimum:      &val,
		Maximum:      &val,
		First:        &val,
		Last:         &val,
	})
	if err != nil || decoded.PointType != telemetryhistorymodel.PointTypeSetting || decoded.Gauge == nil || decoded.Gauge.Last != 7.0 {
		t.Fatalf("decoded setting bucket=%#v err=%v", decoded, err)
	}
}

func TestStateAggregateQueryIncludesTelemetryAndSettingDiscreteValuesAndDecodesStateBucket(t *testing.T) {
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"chiller.run_state"},
		Granularity: telemetryhistorymodel.AggregateGranularityMinute,
		Timezone: "Asia/Shanghai", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
		From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 11, 0, 5, 0, 0, time.UTC),
	}
	client := &Client{database: "telemetry_history", table: "observations"}
	sql := client.stateAggregateQuery(query, query.To)
	if !strings.Contains(sql, "point_type IN ('TELEMETRY', 'SETTING') AND value_type IN ('STRING', 'BOOLEAN', 'JSON')") {
		t.Fatalf("state SQL must include non-numeric TELEMETRY/SETTING points without duplicating numeric gauges:\n%s", sql)
	}
	if !strings.Contains(sql, "assumeNotNull(point_type) AS point_type") {
		t.Fatalf("state SQL must preserve original point_type:\n%s", sql)
	}

	valueType := "STRING"
	lastValue := `"RUNNING"`
	decoded, err := decodeAggregateRow(aggregateRow{
		TelemetryKey:   "chiller.run_state",
		PointID:        "018f2e00-4000-7000-8000-000000000002",
		PointRevision:  1,
		PointType:      "TELEMETRY",
		PeriodStart:    "2026-10-11T00:00:00.000Z",
		PeriodEnd:      "2026-10-11T00:01:00.000Z",
		Good:           2,
		TotalCount:     2,
		IncludedCount:  2,
		StateValueType: &valueType,
		StateLastValue: &lastValue,
		StateSamples:   2,
		StateChanges:   1,
	})
	if err != nil || decoded.PointType != telemetryhistorymodel.PointTypeTelemetry || decoded.State == nil || string(decoded.State.LastValue) != `"RUNNING"` {
		t.Fatalf("decoded TELEMETRY state bucket=%#v err=%v", decoded, err)
	}
}
