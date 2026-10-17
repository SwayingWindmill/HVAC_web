package clickhouse

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

// A Site with electricity facts gets its Tariff laid out as price windows, and once the Tariff
// changes only the new windows are visible: Cube never prices a fact on two timelines at once.
func TestPriceWindowProjectionShowsOnlyTheLatestTariffTimeline(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_TEST_URL"))
	if baseURL == "" {
		t.Skip("ANALYTICS_CLICKHOUSE_TEST_URL is not configured")
	}
	adminUsername := envOr("ANALYTICS_CLICKHOUSE_TEST_ADMIN_USERNAME", "telemetry_history")
	adminPassword := os.Getenv("ANALYTICS_CLICKHOUSE_TEST_ADMIN_PASSWORD")
	client := &http.Client{Timeout: 15 * time.Second}
	tenantID := "018f4f00-0100-7000-8000-000000000003"
	siteID := fmt.Sprintf("018f4f00-1000-7000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	insertJSONEachRow(t, client, baseURL, adminUsername, adminPassword, "analytics.energy_interval_facts", []map[string]any{{
		"fact_id": "018f4f00-5000-7000-8000-000000000001", "tenant_id": tenantID, "site_id": siteID,
		"meter_binding_id": "018f4f00-5100-7000-8000-000000000001", "energy_type": "electricity", "accounting_scope": "SITE",
		"period_start": "2026-10-12 01:59:00.000", "period_end": "2026-10-12 02:00:00.000", "energy_kwh": 1.5, "quality": "VALID",
	}})

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	tariff := func(peakRate float64) []tariffmodel.Version {
		return []tariffmodel.Version{{
			ID: "018f4f00-6000-7000-8000-000000000001", EffectiveFrom: time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai), Location: shanghai,
			Periods: []tariffmodel.Period{
				{Code: "VALLEY", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.3},
				{Code: "PEAK", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 1440, EnergyRatePerKWh: peakRate},
				{Code: "FLAT", DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.6},
			},
		}}
	}
	tariffs := &integrationTariffs{versions: tariff(1.2)}
	reader, writer := integrationReaderWriter(t, client, baseURL)
	projector, err := energy.NewPriceWindowProjector(energy.PriceWindowProjectorConfig{
		Reader: reader, Writer: writer, Tariffs: tariffs, Now: func() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	monday := func() string {
		return string(executeClickHouse(t, client, baseURL, adminUsername, adminPassword, fmt.Sprintf(`SELECT formatDateTime(window_start, '%%FT%%TZ', 'UTC'), formatDateTime(window_end, '%%FT%%TZ', 'UTC'), period_code, energy_rate_per_kwh
FROM analytics.tariff_price_windows_visible
WHERE tenant_id = toUUID('%s') AND site_id = toUUID('%s')
  AND window_start >= toDateTime64('2026-10-11 16:00:00', 3, 'UTC') AND window_start < toDateTime64('2026-10-12 16:00:00', 3, 'UTC')
ORDER BY window_start FORMAT TSVRaw`, tenantID, siteID)))
	}

	if err := projector.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := monday(), "2026-10-11T16:00:00Z\t2026-10-12T00:00:00Z\tVALLEY\t0.3\n2026-10-12T00:00:00Z\t2026-10-12T16:00:00Z\tPEAK\t1.2\n"; got != want {
		t.Fatalf("Monday windows = %q, want %q", got, want)
	}

	tariffs.versions = tariff(1.5)
	if err := projector.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := monday(), "2026-10-11T16:00:00Z\t2026-10-12T00:00:00Z\tVALLEY\t0.3\n2026-10-12T00:00:00Z\t2026-10-12T16:00:00Z\tPEAK\t1.5\n"; got != want {
		t.Fatalf("Monday windows after the Tariff changed = %q, want %q", got, want)
	}
}

type integrationTariffs struct {
	versions []tariffmodel.Version
}

func (tariffs *integrationTariffs) ImportTariffs(context.Context, string, string, time.Time) ([]tariffmodel.Version, error) {
	return tariffs.versions, nil
}
