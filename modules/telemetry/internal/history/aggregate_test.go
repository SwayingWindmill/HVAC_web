package history

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	sql, params := client.counterAggregateQuery(query, query.To)
	for _, marker := range []string{
		"RESET_TO_ZERO", "ROLLOVER", "REVISION_BOUNDARY", "UNIT_BOUNDARY", "previous_quality", "OUT_OF_ORDER", "point_revision",
		"toUUID({tenant_id:UUID})", "telemetry_key IN {keys:Array(String)}", "toStartOfDay(sampled_at, {timezone:String})",
		"parseDateTime64BestEffort({from_ts:String}, 3, 'UTC')", "parseDateTime64BestEffort({to_ts:String}, 3, 'UTC')", "parseDateTime64BestEffort({snapshot_ts:String}, 3, 'UTC')",
	} {
		if !strings.Contains(sql, marker) {
			t.Fatalf("counter SQL missing %s:\n%s", marker, sql)
		}
	}
	for _, forbidden := range []string{historyTenantID, historySiteID, historyDeviceID, "energy_total", "Asia/Singapore", "2026-08-19T00:00:00.000Z", "2026-08-20T00:00:00.000Z"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("counter SQL leaked interpolated value %q:\n%s", forbidden, sql)
		}
	}
	if params.Get("param_tenant_id") != historyTenantID || params.Get("param_keys") != "['energy_total']" || params.Get("param_timezone") != "Asia/Singapore" {
		t.Fatalf("counter params = %#v", params)
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
	if bucket != "toStartOfInterval(sampled_at, INTERVAL 15 MINUTE, {timezone:String})" || next != "addMinutes(bucket, 15)" {
		t.Fatalf("bucket=%s next=%s", bucket, next)
	}
}

func TestMinuteAndFiveMinutePeriodsAlignToSiteClockAndSettingGaugeSQL(t *testing.T) {
	minuteQuery := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys:        []string{"chiller.leaving_chilled_water_temperature_setpoint"},
		Granularity: telemetryhistorymodel.AggregateGranularityMinute,
		Timezone:    "Asia/Kolkata", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
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
	if minuteBucket != "toStartOfInterval(sampled_at, INTERVAL 1 MINUTE, {timezone:String})" || minuteNext != "addMinutes(bucket, 1)" {
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
	if fiveBucket != "toStartOfInterval(sampled_at, INTERVAL 5 MINUTE, {timezone:String})" || fiveNext != "addMinutes(bucket, 5)" {
		t.Fatalf("five-minute bucket=%s next=%s", fiveBucket, fiveNext)
	}

	client := &Client{database: "telemetry_history", table: "observations"}
	gaugeSQL, gaugeParams, usedRollup := client.gaugeAggregateQueryForSnapshot(minuteQuery, minuteQuery.To, periods, false)
	if usedRollup || !strings.Contains(gaugeSQL, "point_type IN ('TELEMETRY', 'SETTING')") || !strings.Contains(gaugeSQL, "telemetry_key IN {keys:Array(String)}") {
		t.Fatalf("gauge SQL should include numeric SETTING points and key placeholder (usedRollup=%v):\n%s", usedRollup, gaugeSQL)
	}
	for _, forbidden := range []string{"Asia/Kolkata", "chiller.leaving_chilled_water_temperature_setpoint", historyTenantID} {
		if strings.Contains(gaugeSQL, forbidden) {
			t.Fatalf("gauge SQL leaked interpolated value %q", forbidden)
		}
	}
	if gaugeParams.Get("param_timezone") != "Asia/Kolkata" || gaugeParams.Get("param_keys") != "['chiller.leaving_chilled_water_temperature_setpoint']" {
		t.Fatalf("gauge params = %#v", gaugeParams)
	}
	snapshotSQL, snapshotParams := client.aggregateSnapshotQuery(minuteQuery)
	if !strings.Contains(snapshotSQL, "'SETTING'") || strings.Contains(snapshotSQL, historyTenantID) || snapshotParams.Get("param_tenant_id") != historyTenantID {
		t.Fatalf("snapshot SQL should parameterize identifiers and include SETTING points:\n%s", snapshotSQL)
	}

	val := 7.0
	unit := "Cel"
	decoded, err := decodeAggregateRow(aggregateRow{
		TelemetryKey:  "chiller.leaving_chilled_water_temperature_setpoint",
		PointID:       "018f2e00-4000-7000-8000-000000000001",
		PointRevision: 1,
		PointType:     "SETTING",
		Unit:          &unit,
		PeriodStart:   "2026-10-11T00:08:00.000Z",
		PeriodEnd:     "2026-10-11T00:09:00.000Z",
		Good:          1,
		TotalCount:    1,
		IncludedCount: 1,
		Average:       &val,
		Minimum:       &val,
		Maximum:       &val,
		First:         &val,
		Last:          &val,
	})
	if err != nil || decoded.PointType != telemetryhistorymodel.PointTypeSetting || decoded.Gauge == nil || decoded.Gauge.Last != 7.0 {
		t.Fatalf("decoded setting bucket=%#v err=%v", decoded, err)
	}
}

func TestStateAggregateQueryIncludesTelemetryAndSettingDiscreteValuesAndDecodesStateBucket(t *testing.T) {
	query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys:        []string{"chiller.run_state"},
		Granularity: telemetryhistorymodel.AggregateGranularityMinute,
		Timezone:    "Asia/Shanghai", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
		From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 11, 0, 5, 0, 0, time.UTC),
	}
	client := &Client{database: "telemetry_history", table: "observations"}
	sql, params := client.stateAggregateQuery(query, query.To)
	if !strings.Contains(sql, "point_type IN ('TELEMETRY', 'SETTING') AND value_type IN ('STRING', 'BOOLEAN', 'JSON')") {
		t.Fatalf("state SQL must include non-numeric TELEMETRY/SETTING points without duplicating numeric gauges:\n%s", sql)
	}
	if !strings.Contains(sql, "assumeNotNull(point_type) AS point_type") {
		t.Fatalf("state SQL must preserve original point_type:\n%s", sql)
	}
	for _, forbidden := range []string{"Asia/Shanghai", "chiller.run_state", historyTenantID, "2026-10-11T00:00:00.000Z"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("state SQL leaked interpolated value %q", forbidden)
		}
	}
	if params.Get("param_timezone") != "Asia/Shanghai" || params.Get("param_keys") != "['chiller.run_state']" {
		t.Fatalf("state params = %#v", params)
	}

	valueType := "STRING"
	lastValue := `"STOPPED"`
	decoded, err := decodeAggregateRow(aggregateRow{
		TelemetryKey:   "chiller.run_state",
		PointID:        "018f2e00-4000-7000-8000-000000000002",
		PointRevision:  1,
		PointType:      "TELEMETRY",
		PeriodStart:    "2026-10-11T00:00:00.000Z",
		PeriodEnd:      "2026-10-11T00:05:00.000Z",
		Good:           5,
		TotalCount:     5,
		IncludedCount:  5,
		StateValueType: &valueType,
		StateLastValue: &lastValue,
		StateSamples:   5,
		StateChanges:   1,
		StateValues:    []string{`"RUNNING"`, `"RUNNING"`, `"RUNNING"`, `"RUNNING"`, `"STOPPED"`},
	})
	if err != nil || decoded.PointType != telemetryhistorymodel.PointTypeTelemetry || decoded.State == nil || string(decoded.State.LastValue) != `"STOPPED"` {
		t.Fatalf("decoded TELEMETRY state bucket=%#v err=%v", decoded, err)
	}
	if len(decoded.State.StateCounts) != 2 || string(decoded.State.StateCounts[0].Value) != `"RUNNING"` || decoded.State.StateCounts[0].Count != 4 || string(decoded.State.StateCounts[1].Value) != `"STOPPED"` || decoded.State.StateCounts[1].Count != 1 {
		t.Fatalf("expected dominant RUNNING (4) and STOPPED (1) in StateCounts, got %#v", decoded.State.StateCounts)
	}
}

func TestGaugeAggregateQueryRoutingAndFallbackRules(t *testing.T) {
	client := &Client{database: "telemetry_history", table: "observations"}
	snap := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	assertRoutes := func(t *testing.T, c *Client, q telemetryhistorymodel.DeviceHistoryAggregateQuery, concurrent bool, wantTable string, wantMarkers ...string) {
		t.Helper()
		periods, err := buildCalendarPeriods(q)
		if err != nil {
			t.Fatal(err)
		}
		sql, params, usedRollup := c.gaugeAggregateQueryForSnapshot(q, snap, periods, concurrent)
		if !strings.Contains(sql, "FROM "+wantTable) {
			t.Fatalf("granularity=%s tz=%s from=%s to=%s concurrent=%v: expected table %s, got SQL:\n%s", q.Granularity, q.Timezone, q.From, q.To, concurrent, wantTable, sql)
		}
		if (wantTable != c.database+"."+c.table) != usedRollup {
			t.Fatalf("usedRollup=%v mismatch with table %s", usedRollup, wantTable)
		}
		for _, marker := range wantMarkers {
			if !strings.Contains(sql, marker) {
				t.Fatalf("SQL missing %q:\n%s", marker, sql)
			}
		}
		forbidden := []string{
			q.TenantID,
			q.SiteID,
			q.DeviceID,
			q.Keys[0],
			formatClickHouseTime(q.From),
			formatClickHouseTime(q.To),
			q.From.UTC().Format("2006-01-02 15:04:05"),
			q.To.UTC().Format("2006-01-02 15:04:05"),
		}
		if q.Timezone != "UTC" {
			forbidden = append(forbidden, q.Timezone)
		}
		for _, raw := range forbidden {
			if strings.Contains(sql, raw) {
				t.Fatalf("gauge SQL leaked interpolated value %q:\n%s", raw, sql)
			}
		}
		if params.Get("param_tenant_id") != q.TenantID ||
			params.Get("param_site_id") != q.SiteID ||
			params.Get("param_device_id") != q.DeviceID ||
			params.Get("param_keys") != clickHouseStringArray(q.Keys) ||
			params.Get("param_timezone") != q.Timezone {
			t.Fatalf("unexpected gauge params: %#v", params)
		}
		if usedRollup {
			if strings.Contains(sql, "WITH ") {
				t.Fatalf("rollup SQL must not use top-level WITH alias over rollup columns:\n%s", sql)
			}
			if !strings.Contains(sql, "GROUP BY gauge_rollup.telemetry_key, toString(gauge_rollup.point_id), gauge_rollup.point_revision, gauge_rollup.point_type,") {
				t.Fatalf("rollup SQL must group by projected toString(gauge_rollup.point_id):\n%s", sql)
			}
			if !strings.Contains(sql, "gauge_rollup.bucket >= toDateTime({from_utc:String}, 'UTC')") ||
				params.Get("param_from_utc") != q.From.UTC().Format("2006-01-02 15:04:05") ||
				params.Get("param_to_utc") != q.To.UTC().Format("2006-01-02 15:04:05") {
				t.Fatalf("rollup SQL/params missing UTC bounds: params=%#v\n%s", params, sql)
			}
		}
	}

	snapSQL, snapParams := client.aggregateSnapshotQuery(telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"zone.temperature"}, QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
		From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
	})
	if !strings.Contains(snapSQL, "toInt64(countIf(") || !strings.Contains(snapSQL, "ifNull(isFinite(value_number), 0) = 1") {
		t.Fatalf("snapshot SQL must normalize Nullable countIf predicates:\n%s", snapSQL)
	}
	if strings.Contains(snapSQL, historyTenantID) || strings.Contains(snapSQL, "zone.temperature") || snapParams.Get("param_tenant_id") != historyTenantID {
		t.Fatalf("snapshot SQL must parameterize scope values: params=%#v\n%s", snapParams, snapSQL)
	}

	base := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"zone.temperature"}, Timezone: "Asia/Shanghai", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
	}

	for _, gran := range []telemetryhistorymodel.AggregateGranularity{
		telemetryhistorymodel.AggregateGranularityMinute,
		telemetryhistorymodel.AggregateGranularityFiveMinute,
	} {
		q := base
		q.Granularity = gran
		q.From = time.Date(2026, 10, 11, 0, 1, 0, 0, time.UTC)
		q.To = time.Date(2026, 10, 11, 0, 10, 0, 0, time.UTC)
		assertRoutes(t, client, q, false, "telemetry_history.numeric_gauge_1min_states",
			"countIfMerge(gauge_rollup.valid_count)", "avgIfMerge(gauge_rollup.valid_avg)", "argMinIfMerge(gauge_rollup.valid_first)", "argMaxIfMerge(gauge_rollup.valid_last)")
	}

	for _, tc := range []struct {
		gran   telemetryhistorymodel.AggregateGranularity
		tz     string
		from   time.Time
		to     time.Time
		policy telemetryhistorymodel.AggregateQualityPolicy
		marker string
	}{
		{telemetryhistorymodel.AggregateGranularityQuarterHour, "Asia/Kolkata", time.Date(2026, 10, 11, 0, 15, 0, 0, time.UTC), time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityValidOnly, "avgIfMerge(gauge_rollup.valid_avg)"},
		{telemetryhistorymodel.AggregateGranularityHour, "Asia/Kathmandu", time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityUsable, "avgIfMerge(gauge_rollup.usable_avg)"},
		{telemetryhistorymodel.AggregateGranularityDay, "America/New_York", time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityValidOnly, "countIfMerge(gauge_rollup.valid_count)"},
		{telemetryhistorymodel.AggregateGranularityWeek, "UTC", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityUsable, "countIfMerge(gauge_rollup.usable_count)"},
		{telemetryhistorymodel.AggregateGranularityMonth, "Asia/Shanghai", time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityValidOnly, "maxMerge(gauge_rollup.max_projected_at)"},
	} {
		q := base
		q.Granularity = tc.gran
		q.Timezone = tc.tz
		q.From = tc.from
		q.To = tc.to
		q.QualityPolicy = tc.policy
		assertRoutes(t, client, q, false, "telemetry_history.numeric_gauge_15min_states", tc.marker)
	}

	unalignedMin := base
	unalignedMin.Granularity = telemetryhistorymodel.AggregateGranularityMinute
	unalignedMin.From = time.Date(2026, 10, 11, 0, 1, 20, 0, time.UTC)
	unalignedMin.To = time.Date(2026, 10, 11, 0, 5, 0, 0, time.UTC)
	assertRoutes(t, client, unalignedMin, false, "telemetry_history.observations", "avgIf(assumeNotNull(value_number)")

	unalignedQtr := base
	unalignedQtr.Granularity = telemetryhistorymodel.AggregateGranularityHour
	unalignedQtr.From = time.Date(2026, 10, 11, 0, 5, 0, 0, time.UTC)
	unalignedQtr.To = time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC)
	assertRoutes(t, client, unalignedQtr, false, "telemetry_history.observations")

	alignedHour := base
	alignedHour.Granularity = telemetryhistorymodel.AggregateGranularityHour
	alignedHour.From = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	alignedHour.To = time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC)
	assertRoutes(t, client, alignedHour, true, "telemetry_history.observations")

	lmtHour := alignedHour
	lmtHour.From = time.Date(1900, 6, 1, 0, 0, 0, 0, time.UTC)
	lmtHour.To = time.Date(1900, 6, 1, 2, 0, 0, 0, time.UTC)
	assertRoutes(t, client, lmtHour, false, "telemetry_history.observations")

	customClient := &Client{database: "test_history", table: "custom_observations"}
	assertRoutes(t, customClient, alignedHour, false, "test_history.custom_observations")
}

func TestQueryDeviceHistoryAggregateEndToEndRollupAndConcurrentFallback(t *testing.T) {
	t.Run("routes aligned query to numeric_gauge_15min_states", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		var rollupSQL string
		var rollupParams url.Values
		snapshotReply := `{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0}` + "\n"
		rollupReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":1,"invalid_count":1,"total_count":6,"included_count":4,"average":22.5,"minimum":21.0,"maximum":24.0,"first":21.0,"last":24.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:04:00.000Z"}` + "\n"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			mu.Lock()
			queries = append(queries, sql)
			if strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states") {
				rollupSQL = sql
				rollupParams = request.URL.Query()
			}
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(snapshotReply))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				_, _ = writer.Write([]byte(rollupReply))
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"zone.temperature"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.Average != 22.5 || resp.Buckets[0].Quality.Invalid != 1 {
			t.Fatalf("unexpected aggregate response=%#v", resp)
		}
		if len(queries) != 4 || rollupSQL == "" {
			t.Fatalf("expected 4 queries with rollup gauge query, got %d:\n%s", len(queries), strings.Join(queries, "\n---\n"))
		}
		if strings.Contains(rollupSQL, historyTenantID) ||
			rollupParams.Get("param_tenant_id") != historyTenantID ||
			rollupParams.Get("param_from_utc") != "2026-10-11 00:00:00" ||
			rollupParams.Get("param_to_utc") != "2026-10-11 01:00:00" ||
			rollupParams.Get("param_timezone") != "UTC" {
			t.Fatalf("rollup request did not send parameterized URL args cleanly: params=%#v\n%s", rollupParams, rollupSQL)
		}
		if !strings.Contains(queries[0], "has_concurrent_writes") {
			t.Fatalf("snapshot query missing has_concurrent_writes:\n%s", queries[0])
		}
	})

	t.Run("falls back to raw observations when snapshot reports concurrent writes", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		var rawGaugeSQL string
		snapshotReply := `{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":1}` + "\n"
		rawReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":23.0,"minimum":23.0,"maximum":23.0,"first":23.0,"last":23.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			mu.Lock()
			queries = append(queries, sql)
			if strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations") {
				rawGaugeSQL = sql
			}
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(snapshotReply))
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				_, _ = writer.Write([]byte(rawReply))
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"zone.temperature"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.Average != 23.0 {
			t.Fatalf("unexpected fallback response=%#v", resp)
		}
		if len(queries) != 4 || rawGaugeSQL == "" {
			t.Fatalf("expected raw observations fallback, got:\n%s", strings.Join(queries, "\n---\n"))
		}
	})

	t.Run("falls back to raw observations when rollup row reveals post-snapshot write", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		var sawRollup, sawRawFallback bool
		snapshotReply := `{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0}` + "\n"
		rollupReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":5,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":5,"included_count":5,"average":99.0,"minimum":23.0,"maximum":99.0,"first":23.0,"last":99.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:05:00.010Z"}` + "\n"
		rawReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":23.0,"minimum":23.0,"maximum":23.0,"first":23.0,"last":23.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			mu.Lock()
			queries = append(queries, sql)
			switch {
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				sawRollup = true
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				sawRawFallback = true
			}
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(snapshotReply))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				_, _ = writer.Write([]byte(rollupReply))
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				_, _ = writer.Write([]byte(rawReply))
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"zone.temperature"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.Average != 23.0 || resp.Buckets[0].Gauge.SampleCount != 4 {
			t.Fatalf("expected raw snapshot result after mid-query write fallback, got %#v", resp)
		}
		if len(queries) != 5 || !sawRollup || !sawRawFallback {
			t.Fatalf("expected rollup then raw fallback sequence, got:\n%s", strings.Join(queries, "\n---\n"))
		}
	})

	t.Run("falls back to raw observations when rollup table returns UNKNOWN_TABLE", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		var sawRollup, sawRawFallback bool
		snapshotReply := `{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0,"raw_gauge_included_count":4}` + "\n"
		rawReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":21.5,"minimum":21.0,"maximum":22.0,"first":21.0,"last":22.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			mu.Lock()
			queries = append(queries, sql)
			switch {
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				sawRollup = true
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				sawRawFallback = true
			}
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(snapshotReply))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(`Code: 60. DB::Exception: Table telemetry_history.numeric_gauge_15min_states does not exist. (UNKNOWN_TABLE)`))
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(rawReply))
			default:
				writer.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"zone.temperature"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.Average != 21.5 {
			t.Fatalf("unexpected fallback response=%#v", resp)
		}
		if len(queries) != 5 || !sawRollup || !sawRawFallback {
			t.Fatalf("expected rollup UNKNOWN_TABLE fallback to raw observations, got:\n%s", strings.Join(queries, "\n---\n"))
		}
	})

	t.Run("falls back to raw observations when raw_gauge_included_count exceeds rollup sum", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		var sawRawFallback bool
		snapshotReply := `{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0,"raw_gauge_included_count":4}` + "\n"
		rollupReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":1,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":1,"included_count":1,"average":20.0,"minimum":20.0,"maximum":20.0,"first":20.0,"last":20.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:04:00.000Z"}` + "\n"
		rawReply := `{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":22.5,"minimum":20.0,"maximum":25.0,"first":20.0,"last":25.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			mu.Lock()
			queries = append(queries, sql)
			if strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations") {
				sawRawFallback = true
			}
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(snapshotReply))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				_, _ = writer.Write([]byte(rollupReply))
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				_, _ = writer.Write([]byte(rawReply))
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		query := telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"zone.temperature"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.SampleCount != 4 || resp.Buckets[0].Gauge.Average != 22.5 {
			t.Fatalf("expected raw fallback with SampleCount=4, got %#v", resp)
		}
		if len(queries) != 5 || !sawRawFallback {
			t.Fatalf("expected rollup-count mismatch fallback to raw observations, got:\n%s", strings.Join(queries, "\n---\n"))
		}
	})
}

func TestQueryDeviceHistoryAggregateSkipsEmptyTypesSplitsUnalignedTailAndMergesMixed(t *testing.T) {
	t.Run("gauge-only snapshot skips counter and state queries", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		responses := []string{
			`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0,"raw_gauge_included_count":4,"raw_counter_included_count":0,"raw_state_included_count":0}` + "\n",
			`{"telemetry_key":"hvac_meter.active_power","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"kW","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":220.0,"minimum":200.0,"maximum":240.0,"first":205.0,"last":235.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:04:00.000Z"}` + "\n",
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			queries = append(queries, string(raw))
			idx := len(queries) - 1
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = writer.Write([]byte(responses[idx]))
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"hvac_meter.active_power"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.Average != 220.0 {
			t.Fatalf("unexpected response = %#v", resp)
		}
		if len(queries) != 2 {
			t.Fatalf("expected exactly 2 queries (snapshot + gauge rollup), got %d:\n%s", len(queries), strings.Join(queries, "\n---\n"))
		}
	})

	t.Run("empty snapshot skips all aggregate subqueries", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			queries = append(queries, string(raw))
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = writer.Write([]byte(`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":null,"has_concurrent_writes":0,"raw_gauge_included_count":0,"raw_counter_included_count":0,"raw_state_included_count":0}` + "\n"))
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"hvac_meter.active_power"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Buckets) != 0 || len(queries) != 1 {
			t.Fatalf("expected 1 query and 0 buckets, got queries=%d buckets=%d", len(queries), len(resp.Buckets))
		}
	})

	t.Run("unaligned to splits aligned prefix to rollup and tail to raw observations", func(t *testing.T) {
		var mu sync.Mutex
		var sawRollup, sawTail bool
		var sawCounterOrState bool
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			q := request.URL.Query()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T01:37:00.000Z","has_concurrent_writes":0,"raw_gauge_included_count":6,"raw_counter_included_count":0,"raw_state_included_count":0}` + "\n"))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				mu.Lock()
				sawRollup = true
				mu.Unlock()
				if q.Get("param_from_utc") != "2026-10-11 00:00:00" || q.Get("param_to_utc") != "2026-10-11 01:30:00" {
					t.Fatalf("unexpected prefix bounds: from=%q to=%q", q.Get("param_from_utc"), q.Get("param_to_utc"))
				}
				_, _ = writer.Write([]byte(`{"telemetry_key":"hvac_meter.active_power","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"kW","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T00:15:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":210.0,"minimum":200.0,"maximum":220.0,"first":200.0,"last":220.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T01:37:00.000Z"}` + "\n"))
			case strings.Contains(sql, "avgIf(") && strings.Contains(sql, "FROM telemetry_history.observations"):
				mu.Lock()
				sawTail = true
				mu.Unlock()
				if q.Get("param_from_ts") != "2026-10-11T01:30:00.000Z" || q.Get("param_to_ts") != "2026-10-11T01:37:14.000Z" {
					t.Fatalf("unexpected tail bounds: from=%q to=%q", q.Get("param_from_ts"), q.Get("param_to_ts"))
				}
				_, _ = writer.Write([]byte(`{"telemetry_key":"hvac_meter.active_power","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"kW","period_start":"2026-10-11T01:30:00.000Z","period_end":"2026-10-11T01:45:00.000Z","good_count":2,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":2,"included_count":2,"average":250.0,"minimum":240.0,"maximum":260.0,"first":240.0,"last":260.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"))
			default:
				mu.Lock()
				sawCounterOrState = true
				mu.Unlock()
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"hvac_meter.active_power"}, Granularity: telemetryhistorymodel.AggregateGranularityQuarterHour,
			Timezone: "Asia/Shanghai", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 37, 14, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !sawRollup || !sawTail || sawCounterOrState {
			t.Fatalf("unexpected query routing: rollup=%v tail=%v counterOrState=%v", sawRollup, sawTail, sawCounterOrState)
		}
		if len(resp.Buckets) != 2 || resp.Buckets[0].Gauge.Average != 210.0 || resp.Buckets[1].Gauge.Average != 250.0 {
			t.Fatalf("unexpected combined split buckets = %#v", resp.Buckets)
		}
	})

	t.Run("split rollup falls back to full raw observations when count mismatches", func(t *testing.T) {
		var mu sync.Mutex
		var fullRawSeen bool
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			q := request.URL.Query()
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T01:37:00.000Z","has_concurrent_writes":0,"raw_gauge_included_count":6,"raw_counter_included_count":0,"raw_state_included_count":0}` + "\n"))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				_, _ = writer.Write([]byte(`{"telemetry_key":"hvac_meter.active_power","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"kW","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T00:15:00.000Z","good_count":1,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":1,"included_count":1,"average":210.0,"minimum":210.0,"maximum":210.0,"first":210.0,"last":210.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T01:37:00.000Z"}` + "\n"))
			case strings.Contains(sql, "FROM telemetry_history.observations") && q.Get("param_from_ts") == "2026-10-11T00:00:00.000Z":
				mu.Lock()
				fullRawSeen = true
				mu.Unlock()
				_, _ = writer.Write([]byte(`{"telemetry_key":"hvac_meter.active_power","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"kW","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T00:15:00.000Z","good_count":6,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":6,"included_count":6,"average":215.0,"minimum":210.0,"maximum":220.0,"first":210.0,"last":220.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"))
			default:
				// tail raw returns empty
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"hvac_meter.active_power"}, Granularity: telemetryhistorymodel.AggregateGranularityQuarterHour,
			Timezone: "Asia/Shanghai", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 37, 14, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		gotFullRaw := fullRawSeen
		mu.Unlock()
		if !gotFullRaw || len(resp.Buckets) != 1 || resp.Buckets[0].Gauge == nil || resp.Buckets[0].Gauge.Average != 215.0 || resp.Buckets[0].Gauge.SampleCount != 6 {
			t.Fatalf("expected full raw fallback on split count mismatch (fullRaw=%v), got %#v", gotFullRaw, resp)
		}
	})

	t.Run("mixed gauge counter and state snapshot runs all three and sorts merged buckets", func(t *testing.T) {
		var mu sync.Mutex
		var sawGauge, sawCounter, sawState bool
		stateType := "STRING"
		stateVal := `"\"RUNNING\""`
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			sql := string(raw)
			writer.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(sql, "AS snapshot_cutoff"):
				_, _ = writer.Write([]byte(`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0,"raw_gauge_included_count":2,"raw_counter_included_count":2,"raw_state_included_count":2}` + "\n"))
			case strings.Contains(sql, "FROM telemetry_history.numeric_gauge_15min_states"):
				mu.Lock()
				sawGauge = true
				mu.Unlock()
				_, _ = writer.Write([]byte(`{"telemetry_key":"z.power","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"kW","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":2,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":2,"included_count":2,"average":100.0,"minimum":90.0,"maximum":110.0,"first":90.0,"last":110.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:04:00.000Z"}` + "\n"))
			case strings.Contains(sql, "counter_decrease_mode"):
				mu.Lock()
				sawCounter = true
				mu.Unlock()
				_, _ = writer.Write([]byte(`{"telemetry_key":"a.energy","point_id":"018f2e00-4000-7000-8000-000000000002","point_revision":1,"point_type":"COUNTER","unit":"kWh","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":2,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":2,"included_count":2,"average":null,"minimum":null,"maximum":null,"first":null,"last":null,"delta_sum":50.0,"delta_count":2,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n"))
			case strings.Contains(sql, "previous_value_json"):
				mu.Lock()
				sawState = true
				mu.Unlock()
				_, _ = writer.Write([]byte(`{"telemetry_key":"m.run_state","point_id":"018f2e00-4000-7000-8000-000000000003","point_revision":1,"point_type":"STATE","unit":null,"period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":2,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":2,"included_count":2,"average":null,"minimum":null,"maximum":null,"first":null,"last":null,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":"` + stateType + `","state_last_value":` + stateVal + `,"state_sample_count":2,"state_change_count":0,"state_values":[` + stateVal + `,` + stateVal + `]}` + "\n"))
			}
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, Database: "telemetry_history", Table: "observations", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.QueryDeviceHistoryAggregate(context.Background(), telemetryhistorymodel.DeviceHistoryAggregateQuery{
			TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
			Keys: []string{"z.power", "a.energy", "m.run_state"}, Granularity: telemetryhistorymodel.AggregateGranularityHour,
			Timezone: "UTC", QualityPolicy: telemetryhistorymodel.AggregateQualityUsable,
			From: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !sawGauge || !sawCounter || !sawState {
			t.Fatalf("expected all three subqueries to run: gauge=%v counter=%v state=%v", sawGauge, sawCounter, sawState)
		}
		if len(resp.Buckets) != 3 || resp.Buckets[0].TelemetryKey != "a.energy" || resp.Buckets[1].TelemetryKey != "m.run_state" || resp.Buckets[2].TelemetryKey != "z.power" {
			t.Fatalf("unexpected mixed buckets order/values = %#v", resp.Buckets)
		}
	})
}
