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
