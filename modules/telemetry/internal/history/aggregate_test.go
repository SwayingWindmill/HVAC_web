package history

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	sql := client.stateAggregateQuery(query, query.To)
	if !strings.Contains(sql, "point_type IN ('TELEMETRY', 'SETTING') AND value_type IN ('STRING', 'BOOLEAN', 'JSON')") {
		t.Fatalf("state SQL must include non-numeric TELEMETRY/SETTING points without duplicating numeric gauges:\n%s", sql)
	}
	if !strings.Contains(sql, "assumeNotNull(point_type) AS point_type") {
		t.Fatalf("state SQL must preserve original point_type:\n%s", sql)
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
		sql, usedRollup := c.gaugeAggregateQueryForSnapshot(q, snap, periods, concurrent)
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
	}

	base := telemetryhistorymodel.DeviceHistoryAggregateQuery{
		TenantID: historyTenantID, SiteID: historySiteID, DeviceID: historyDeviceID,
		Keys: []string{"zone.temperature"}, Timezone: "Asia/Shanghai", QualityPolicy: telemetryhistorymodel.AggregateQualityValidOnly,
	}

	// MINUTE and FIVE_MINUTE aligned on 1m UTC boundaries route to numeric_gauge_1min_states.
	for _, gran := range []telemetryhistorymodel.AggregateGranularity{
		telemetryhistorymodel.AggregateGranularityMinute,
		telemetryhistorymodel.AggregateGranularityFiveMinute,
	} {
		q := base
		q.Granularity = gran
		q.From = time.Date(2026, 10, 11, 0, 1, 0, 0, time.UTC)
		q.To = time.Date(2026, 10, 11, 0, 10, 0, 0, time.UTC)
		assertRoutes(t, client, q, false, "telemetry_history.numeric_gauge_1min_states",
			"countIfMerge(rollup.valid_count)", "avgIfMerge(rollup.valid_avg)", "argMinIfMerge(rollup.valid_first)", "argMaxIfMerge(rollup.valid_last)")
	}

	// QUARTER_HOUR, HOUR, DAY, WEEK, MONTH aligned on 15m UTC boundaries route to numeric_gauge_15min_states.
	for _, tc := range []struct {
		gran   telemetryhistorymodel.AggregateGranularity
		tz     string
		from   time.Time
		to     time.Time
		policy telemetryhistorymodel.AggregateQualityPolicy
		marker string
	}{
		{telemetryhistorymodel.AggregateGranularityQuarterHour, "Asia/Kolkata", time.Date(2026, 10, 11, 0, 15, 0, 0, time.UTC), time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityValidOnly, "avgIfMerge(rollup.valid_avg)"},
		{telemetryhistorymodel.AggregateGranularityHour, "Asia/Kathmandu", time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityUsable, "avgIfMerge(rollup.usable_avg)"},
		{telemetryhistorymodel.AggregateGranularityDay, "America/New_York", time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityValidOnly, "countIfMerge(rollup.valid_count)"},
		{telemetryhistorymodel.AggregateGranularityWeek, "UTC", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityUsable, "countIfMerge(rollup.usable_count)"},
		{telemetryhistorymodel.AggregateGranularityMonth, "Asia/Shanghai", time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), telemetryhistorymodel.AggregateQualityValidOnly, "maxMerge(rollup.max_projected_at)"},
	} {
		q := base
		q.Granularity = tc.gran
		q.Timezone = tc.tz
		q.From = tc.from
		q.To = tc.to
		q.QualityPolicy = tc.policy
		assertRoutes(t, client, q, false, "telemetry_history.numeric_gauge_15min_states", tc.marker)
	}

	// Unaligned boundaries, concurrent writes, sub-minute offsets, and custom tables fall back to raw observations.
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
		responses := []string{
			`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0}` + "\n",
			`{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":1,"invalid_count":1,"total_count":6,"included_count":4,"average":22.5,"minimum":21.0,"maximum":24.0,"first":21.0,"last":24.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:04:00.000Z"}` + "\n",
			"",
			"",
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
		if len(queries) != 4 || !strings.Contains(queries[1], "FROM telemetry_history.numeric_gauge_15min_states") {
			t.Fatalf("expected 4 queries with rollup gauge query, got %d:\n%s", len(queries), strings.Join(queries, "\n---\n"))
		}
		if !strings.Contains(queries[0], "has_concurrent_writes") {
			t.Fatalf("snapshot query missing has_concurrent_writes:\n%s", queries[0])
		}
	})

	t.Run("falls back to raw observations when snapshot reports concurrent writes", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		responses := []string{
			`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":1}` + "\n",
			`{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":23.0,"minimum":23.0,"maximum":23.0,"first":23.0,"last":23.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n",
			"",
			"",
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
		if len(queries) != 4 || !strings.Contains(queries[1], "FROM telemetry_history.observations") {
			t.Fatalf("expected raw observations fallback, got:\n%s", strings.Join(queries, "\n---\n"))
		}
	})

	t.Run("falls back to raw observations when rollup row reveals post-snapshot write", func(t *testing.T) {
		var mu sync.Mutex
		var queries []string
		responses := []string{
			`{"snapshot_at":"2026-10-11T02:05:00.000Z","projection_watermark":"2026-10-11T02:04:00.000Z","has_concurrent_writes":0}` + "\n",
			`{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":5,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":5,"included_count":5,"average":99.0,"minimum":23.0,"maximum":99.0,"first":23.0,"last":99.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[],"max_projected_at":"2026-10-11T02:05:00.010Z"}` + "\n",
			`{"telemetry_key":"zone.temperature","point_id":"` + historyPointID + `","point_revision":1,"point_type":"TELEMETRY","unit":"Cel","period_start":"2026-10-11T00:00:00.000Z","period_end":"2026-10-11T01:00:00.000Z","good_count":4,"partial_count":0,"estimated_count":0,"manual_count":0,"stale_count":0,"invalid_count":0,"total_count":4,"included_count":4,"average":23.0,"minimum":23.0,"maximum":23.0,"first":23.0,"last":23.0,"delta_sum":null,"delta_count":0,"reset_count":0,"rollover_count":0,"excluded_transition_count":0,"state_value_type":null,"state_last_value":null,"state_sample_count":0,"state_change_count":0,"state_values":[]}` + "\n",
			"",
			"",
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
		if len(queries) != 5 || !strings.Contains(queries[1], "FROM telemetry_history.numeric_gauge_15min_states") || !strings.Contains(queries[2], "FROM telemetry_history.observations") {
			t.Fatalf("expected rollup then raw fallback sequence, got:\n%s", strings.Join(queries, "\n---\n"))
		}
	})
}
