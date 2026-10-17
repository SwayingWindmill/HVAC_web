package mv

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

// An hour's price is the Site's electricity cost over the energy the query service priced in it; an
// hour with no priced energy, or none at all, is unpriced rather than free.
func TestQueryRatesPriceEachHourAtTheSiteElectricityAverage(t *testing.T) {
	site, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 17, 0, 0, 0, 0, site)
	hour := func(offset int, energy float64, cost analyticsmodel.EnergyCost) analyticsmodel.EnergySeriesPoint {
		start := from.Add(time.Duration(offset) * time.Hour)
		return analyticsmodel.EnergySeriesPoint{PeriodStart: start.UTC(), PeriodEnd: start.Add(time.Hour).UTC(), EnergyKWh: energy, Cost: &cost}
	}
	series := &energySeriesStub{response: analyticsmodel.EnergySeriesResponse{SchemaVersion: 1, Points: []analyticsmodel.EnergySeriesPoint{
		hour(0, 100, analyticsmodel.EnergyCost{AmountCNY: 24, Periods: []analyticsmodel.PeriodCost{}}),
		hour(1, 120, analyticsmodel.EnergyCost{AmountCNY: 62, UnpricedKWh: 20, Periods: []analyticsmodel.PeriodCost{}}),
		hour(2, 50, analyticsmodel.EnergyCost{UnpricedKWh: 50, Periods: []analyticsmodel.PeriodCost{}}),
	}}}

	rate, err := NewQueryRates(series).HourlyRates(t.Context(), testTenant, testSite, site, from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct {
		offset int
		want   float64
		priced bool
	}{{0, 0.24, true}, {1, 0.62, true}, {2, 0, false}, {3, 0, false}} {
		got, priced := rate(from.Add(time.Duration(check.offset) * time.Hour))
		if priced != check.priced || got != check.want {
			t.Fatalf("hour %d = %v %v, want %v %v", check.offset, got, priced, check.want, check.priced)
		}
	}
	asked := series.asked
	if asked.TenantID != testTenant || asked.SiteID != testSite || asked.EnergyType != analyticsmodel.EnergyTypeElectricity ||
		asked.Granularity != analyticsmodel.GranularityHour || asked.Timezone != "Asia/Shanghai" || asked.QualityPolicy != analyticsmodel.QualityPolicyValidOnly ||
		!asked.From.Equal(from) || !asked.To.Equal(from.AddDate(0, 0, 1)) {
		t.Fatalf("asked = %#v", asked)
	}
}

type energySeriesStub struct {
	asked    analyticsmodel.EnergySeriesQuery
	response analyticsmodel.EnergySeriesResponse
}

func (stub *energySeriesStub) Series(_ context.Context, query analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error) {
	stub.asked = query
	return stub.response, nil
}
