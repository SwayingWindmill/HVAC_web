package cube

import (
	"context"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/analytics"
)

// Each electricity fact is priced at the window its period ends in; energy outside every window
// is reported as unpriced, never as free. run-analytics-cube-tests.mjs projects the Site's facts
// (12:00 bucket: one fact of 2 kWh ending 12:58; 13:00 bucket: 1 kWh ending 13:00 and 1 kWh
// ending 13:05) before it runs this test.
func TestCubePricesTheSiteElectricityAtItsPriceWindows(t *testing.T) {
	cubeURL := strings.TrimSpace(os.Getenv("ANALYTICS_CUBE_TEST_URL"))
	clickHouseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if cubeURL == "" || clickHouseURL == "" {
		t.Skip("ANALYTICS_CUBE_TEST_URL and ANALYTICS_CLICKHOUSE_TEST_URL are not configured")
	}
	const tenantID, siteID = "018f4f00-0100-7000-8000-000000000001", "018f4f00-1000-7000-8000-000000000001"
	for _, statement := range []string{
		`INSERT INTO analytics.tariff_price_windows (tenant_id, site_id, generation, window_start, window_end, period_code, energy_rate_per_kwh, tariff_version_id) VALUES
 ('` + tenantID + `', '` + siteID + `', 1, '2026-07-29 12:00:00', '2026-07-29 13:00:00', 'FLAT', 0.5, '018f4f00-6000-7000-8000-000000000001'),
 ('` + tenantID + `', '` + siteID + `', 1, '2026-07-29 13:00:00', '2026-07-29 13:03:00', 'PEAK', 1.0, '018f4f00-6000-7000-8000-000000000001')`,
		`INSERT INTO analytics.tariff_price_window_syncs (tenant_id, site_id, generation, timeline_digest, covered_to) VALUES
 ('` + tenantID + `', '` + siteID + `', 1, 'integration', '2026-07-29 13:03:00')`,
	} {
		response, err := http.Post(clickHouseURL+"/?user=telemetry_history", "text/plain", strings.NewReader(statement))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("ClickHouse returned %d for %s", response.StatusCode, statement)
		}
	}
	factory, err := NewHMACTokenFactory([]byte(os.Getenv("ANALYTICS_CUBE_TEST_KEY")), nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{BaseURL: cubeURL, DatasetRevision: "integration", TokenFactory: factory})
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.QueryEnergySeries(context.Background(), analytics.CallerContext{PrincipalID: "analytics-integration-user", PolicyRevision: "analytics-integration:1"}, analyticsmodel.EnergySeriesQuery{
		TenantID: tenantID, SiteID: siteID, EnergyType: analyticsmodel.EnergyTypeElectricity, Granularity: analyticsmodel.GranularityHour, Timezone: "UTC",
		From: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC), QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := make([]*analyticsmodel.EnergyCost, len(response.Points))
	for index, point := range response.Points {
		got[index] = point.Cost
	}
	want := []*analyticsmodel.EnergyCost{
		{AmountCNY: 1, Periods: []analyticsmodel.PeriodCost{{PeriodCode: "FLAT", EnergyKWh: 2, AmountCNY: 1}}},
		{AmountCNY: 1, UnpricedKWh: 1, Periods: []analyticsmodel.PeriodCost{{PeriodCode: "PEAK", EnergyKWh: 1, AmountCNY: 1}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hourly costs = %+v, want %+v", got, want)
	}
}

func TestCubeSynthesizesSiteTotalMarksMissingSubmetersAndSeparatesCrossGap(t *testing.T) {
	cubeURL := strings.TrimSpace(os.Getenv("ANALYTICS_CUBE_TEST_URL"))
	clickHouseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if cubeURL == "" || clickHouseURL == "" {
		t.Skip("ANALYTICS_CUBE_TEST_URL and ANALYTICS_CLICKHOUSE_TEST_URL are not configured")
	}
	const (
		tenantID    = "018f4f00-0100-7000-8000-000000000001"
		synthSiteID = "018f4f00-1000-7000-8000-000000000002"
		pointA      = "018f4f00-7000-7000-8000-000000000001"
		pointB      = "018f4f00-7000-7000-8000-000000000002"
		deviceA     = "018f4f00-4000-7000-8000-000000000021"
		deviceB     = "018f4f00-4000-7000-8000-000000000022"
	)
	for _, statement := range []string{
		`INSERT INTO analytics.site_total_submeters (tenant_id, site_id, generation, point_id, device_id, meter_name, ordinal) VALUES
 ('` + tenantID + `', '` + synthSiteID + `', 1, '` + pointA + `', '` + deviceA + `', '3号电表（A机组）', 0),
 ('` + tenantID + `', '` + synthSiteID + `', 1, '` + pointB + `', '` + deviceB + `', '2号电表（D机组）', 1)`,
		`INSERT INTO analytics.site_total_submeter_syncs (tenant_id, site_id, generation, synthesized, sources_digest) VALUES
 ('` + tenantID + `', '` + synthSiteID + `', 1, 1, 'synth-integration')`,
		`INSERT INTO analytics.tariff_price_windows (tenant_id, site_id, generation, window_start, window_end, period_code, energy_rate_per_kwh, tariff_version_id) VALUES
 ('` + tenantID + `', '` + synthSiteID + `', 1, '2026-11-12 00:00:00', '2026-11-12 06:00:00', 'PEAK', 1.2, '018f4f00-6000-7000-8000-000000000002')`,
		`INSERT INTO analytics.tariff_price_window_syncs (tenant_id, site_id, generation, timeline_digest, covered_to) VALUES
 ('` + tenantID + `', '` + synthSiteID + `', 1, 'synth-tariff', '2026-11-12 06:00:00')`,
		// Hour 01:00 UTC: both sub-meters report (10 + 15 = 25 kWh), plus a legacy SITE row (999 kWh) that must NOT be double-counted.
		// Hour 02:00 UTC: only sub-meter A reports (12 kWh), sub-meter B is missing -> Incomplete=true, MissingMeters=['2号电表（D机组）'], plus a 3-day cross-gap fact on sub-meter B (180 kWh) ending 02:15 UTC.
		`INSERT INTO analytics.energy_interval_facts (fact_id, tenant_id, site_id, meter_id, meter_binding_id, point_id, load_device_id, energy_type, accounting_scope, quality, period_start, period_end, energy_kwh, source_current_observation_id, data_watermark, dataset_revision, fact_revision) VALUES
 (generateUUIDv4(), '` + tenantID + `', '` + synthSiteID + `', '018f4f00-5000-7000-8000-000000000020', '018f4f00-5100-7000-8000-000000000020', '018f4f00-7000-7000-8000-000000000020', NULL, 'electricity', 'SITE', 'VALID', '2026-11-12 01:00:00', '2026-11-12 01:15:00', 999, generateUUIDv4(), '2026-11-12 02:30:00', 1, 0),
 (generateUUIDv4(), '` + tenantID + `', '` + synthSiteID + `', '018f4f00-5000-7000-8000-000000000021', '018f4f00-5100-7000-8000-000000000021', '` + pointA + `', '` + deviceA + `', 'electricity', 'BRANCH', 'VALID', '2026-11-12 01:00:00', '2026-11-12 01:15:00', 10, generateUUIDv4(), '2026-11-12 02:30:00', 1, 0),
 (generateUUIDv4(), '` + tenantID + `', '` + synthSiteID + `', '018f4f00-5000-7000-8000-000000000022', '018f4f00-5100-7000-8000-000000000022', '` + pointB + `', '` + deviceB + `', 'electricity', 'BRANCH', 'VALID', '2026-11-12 01:00:00', '2026-11-12 01:15:00', 15, generateUUIDv4(), '2026-11-12 02:30:00', 1, 0),
 (generateUUIDv4(), '` + tenantID + `', '` + synthSiteID + `', '018f4f00-5000-7000-8000-000000000021', '018f4f00-5100-7000-8000-000000000021', '` + pointA + `', '` + deviceA + `', 'electricity', 'BRANCH', 'VALID', '2026-11-12 02:00:00', '2026-11-12 02:15:00', 12, generateUUIDv4(), '2026-11-12 02:30:00', 1, 0),
 (generateUUIDv4(), '` + tenantID + `', '` + synthSiteID + `', '018f4f00-5000-7000-8000-000000000022', '018f4f00-5100-7000-8000-000000000022', '` + pointB + `', '` + deviceB + `', 'electricity', 'BRANCH', 'VALID', '2026-11-09 02:15:00', '2026-11-12 02:15:00', 180, generateUUIDv4(), '2026-11-12 02:30:00', 1, 0)`,
	} {
		response, err := http.Post(clickHouseURL+"/?user=telemetry_history", "text/plain", strings.NewReader(statement))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("ClickHouse returned %d for %s", response.StatusCode, statement)
		}
	}

	factory, err := NewHMACTokenFactory([]byte(os.Getenv("ANALYTICS_CUBE_TEST_KEY")), nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{BaseURL: cubeURL, DatasetRevision: "integration", TokenFactory: factory})
	if err != nil {
		t.Fatal(err)
	}

	hourly, err := client.QueryEnergySeries(context.Background(), analytics.CallerContext{PrincipalID: "analytics-integration-user", PolicyRevision: "analytics-integration:1"}, analyticsmodel.EnergySeriesQuery{
		TenantID: tenantID, SiteID: synthSiteID, EnergyType: analyticsmodel.EnergyTypeElectricity, Granularity: analyticsmodel.GranularityHour, Timezone: "UTC",
		From: time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC), QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly.Points) != 2 || hourly.Points[0].EnergyKWh != 25 || hourly.Points[0].Incomplete ||
		hourly.Points[1].EnergyKWh != 12 || !hourly.Points[1].Incomplete || !reflect.DeepEqual(hourly.Points[1].MissingMeters, []string{"2号电表（D机组）"}) {
		t.Fatalf("synthesized hourly points = %+v", hourly.Points)
	}
	if hourly.CrossGap == nil || hourly.CrossGap.EnergyKWh != 180 || hourly.CrossGap.Cost == nil || hourly.CrossGap.Cost.AmountCNY != 216 {
		t.Fatalf("synthesized crossGap = %+v", hourly.CrossGap)
	}
}
