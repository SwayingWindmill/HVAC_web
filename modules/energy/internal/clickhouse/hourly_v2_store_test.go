package clickhouse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

func TestReaderHourlyV2SourcesQueriesAndDecoding(t *testing.T) {
	const (
		tenantID = "018f4d00-0000-7000-8000-000000000001"
		siteID   = "018f4e00-1000-7000-8000-000000000001"
		pointID  = "018f4e00-2100-7000-8000-000000000001"
		binding  = "018f4e00-1200-7000-8000-000000000001"
	)
	hour10 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	var seenSQL []string
	var seenParams []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := string(body)
		seenSQL = append(seenSQL, sql)
		seenParams = append(seenParams, r.URL.Query())
		switch {
		case strings.Contains(sql, "AS fact_version") && strings.Contains(sql, "has({point_hours:Array(String)}"):
			_, _ = io.WriteString(w, `{"fact_id":"f1","tenant_id":"`+tenantID+`","site_id":"`+siteID+`","meter_id":"m1","meter_binding_id":"`+binding+`","topology_version_id":"tv1","binding_version":"2","energy_type_id":"et1","energy_type":"electricity","meter_role":"PRIMARY","direction":"IMPORT","accounting_scope":"SITE","load_device_id":null,"device_id":"d1","point_id":"`+pointID+`","sensor_id":null,"telemetry_key":"site.energy","point_revision":"3","unit":"kWh","counter_decrease_mode":"RESET_TO_ZERO","counter_rollover_modulus":null,"period_start":"2026-11-26T10:00:00.000Z","period_end":"2026-11-26T10:15:00.000Z","energy_kwh":10,"transition_type":"INCREASE","quality":"VALID","quality_reasons":[],"source_previous_observation_id":"o0","source_current_observation_id":"o1","source_event_id":"e1","source_partition":"p0","source_offset":"101","dataset_revision":"101","data_watermark":"2026-11-26T10:15:00.000Z","projected_at":"2026-11-26T10:15:01.000Z","fact_revision":"0"}`+"\n")
		case strings.Contains(sql, "FROM analytics.energy_hourly_facts_v2 AS f") && strings.Contains(sql, "argMax(f.interval_count"):
			_, _ = io.WriteString(w, `{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","energy_type":"electricity","accounting_scope":"SITE","load_device_id":"00000000-0000-0000-0000-000000000000","point_id":"`+pointID+`","quality":"VALID","tariff_period":"","hour_utc":"2026-11-26T10:00:00Z","interval_count":"1","calc_version":"1795687200000","data_watermark":"2026-11-26T10:15:00.000Z","dataset_revision":"101"}`+"\n")
		case strings.Contains(sql, "FROM analytics.tariff_price_windows AS w FINAL"):
			_, _ = io.WriteString(w, `{"window_start":"2026-11-26T10:00:00.000Z","window_end":"2026-11-26T11:00:00.000Z","period_code":"PEAK","energy_rate_per_kwh":1.25,"tariff_version_id":"tv-1"}`+"\n")
		case strings.Contains(sql, "has({bindings:Array(UUID)}"):
			_, _ = io.WriteString(w, `{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","point_id":"`+pointID+`","energy_type":"electricity","accounting_scope":"SITE","load_device_id":null,"quality":"VALID","period_start":"2026-11-26T09:00:00.000Z","period_end":"2026-11-26T10:30:00.000Z","energy_kwh":20,"fact_revision":"0","data_watermark":"2026-11-26T10:30:00.000Z","dataset_revision":"90","meter_binding_id":"`+binding+`","source_current_observation_id":"o1"}`+"\n"+
				`{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","point_id":"`+pointID+`","energy_type":"electricity","accounting_scope":"SITE","load_device_id":null,"quality":"INVALID","period_start":"2026-11-26T10:15:00.000Z","period_end":"2026-11-26T10:30:00.000Z","energy_kwh":0,"fact_revision":"1","data_watermark":"2026-11-26T10:30:00.000Z","dataset_revision":"102","meter_binding_id":"`+binding+`","source_current_observation_id":"o1"}`+"\n")
		case strings.Contains(sql, "toStartOfHour(addMilliseconds(v.period_end, -1))"):
			_, _ = io.WriteString(w, `{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","point_id":"`+pointID+`","hour_utc":"2026-11-26T10:00:00Z"}`+"\n")
		case strings.Contains(sql, "dateDiff('millisecond', v.period_start, v.period_end) > 3600000"):
			// Cross-gap interval 09:00..12:00 must be clamped to [10:00, 11:00) -> only 10:00 added.
			_, _ = io.WriteString(w, `{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","point_id":"`+pointID+`","period_start":"2026-11-26T09:00:00.000Z","period_end":"2026-11-26T12:00:00.000Z"}`+"\n")
		case strings.Contains(sql, "HAVING latest_interval_count != 0"):
			_, _ = io.WriteString(w, `{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","point_id":"`+pointID+`","hour_utc":"2026-11-26T10:00:00Z"}`+"\n")
		default:
			t.Errorf("unexpected SQL: %s", sql)
		}
	}))
	defer server.Close()

	reader, err := NewReader(ReaderConfig{
		BaseURL:           server.URL,
		SourceDatabase:    "telemetry_history",
		AnalyticsDatabase: "analytics",
		AnalyticsTable:    "energy_interval_facts",
		HTTPClient:        server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	buckets := []energy.HourlyBucketKey{{TenantID: tenantID, SiteID: siteID, PointID: pointID, Hour: hour10}}
	facts, err := reader.VisibleIntervalsForHours(context.Background(), buckets)
	if err != nil || len(facts) != 1 || facts[0].EnergyKWh != 10 || facts[0].LoadDeviceID != nil || facts[0].SourceOffset != 101 {
		t.Fatalf("VisibleIntervalsForHours = (%+v, %v)", facts, err)
	}
	if strings.Contains(seenSQL[0], "energy_interval_facts_visible") {
		t.Fatalf("expected query against analytics.energy_interval_facts base table (reader-accessible), got:\n%s", seenSQL[0])
	}

	existing, err := reader.ExistingHourlyFactsV2(context.Background(), buckets)
	if err != nil || len(existing) != 1 || existing[0].LoadDeviceID != "" || existing[0].CalcVersion != 1795687200000 || existing[0].IntervalCount != 1 {
		t.Fatalf("ExistingHourlyFactsV2 = (%+v, %v)", existing, err)
	}

	windows, err := reader.TariffPriceWindows(context.Background(), tenantID, siteID, hour10, hour10.Add(time.Hour))
	if err != nil || len(windows) != 1 || windows[0].PeriodCode != "PEAK" || windows[0].EnergyRatePerKWh != 1.25 {
		t.Fatalf("TariffPriceWindows = (%+v, %v)", windows, err)
	}

	priors, err := reader.PriorIntervalsForCorrections(context.Background(), []energy.EnergyIntervalFact{{
		TenantID: tenantID, SiteID: siteID, MeterBindingID: binding, CurrentObservationID: "o1",
		PeriodEnd: hour10.Add(30 * time.Minute), FactRevision: 1,
	}})
	if err != nil || len(priors) != 1 || priors[0].FactRevision != 0 {
		t.Fatalf("PriorIntervalsForCorrections = (%+v, %v)", priors, err)
	}

	listed, err := reader.ListHourlyBackfillBuckets(context.Background(), energy.HourlyBackfillQuery{
		Since:    hour10,
		Until:    hour10.Add(time.Hour),
		TenantID: tenantID,
		SiteID:   siteID,
	})
	if err != nil || len(listed) != 1 || !listed[0].Hour.Equal(hour10) {
		t.Fatalf("ListHourlyBackfillBuckets = (%+v, %v)", listed, err)
	}
	if _, err := reader.ListHourlyBackfillBuckets(context.Background(), energy.HourlyBackfillQuery{
		Since:    hour10,
		Until:    hour10.Add(time.Hour),
		TenantID: "not-a-uuid",
	}); err == nil {
		t.Fatal("expected invalid UUID error")
	}
	_ = json.NewDecoder
	_ = seenParams
}
