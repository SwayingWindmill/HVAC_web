package mv

import (
	"context"
	"fmt"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

// EnergySeries reads the query service's Energy Series as M&V's own Workload Principal
// (libs/energyseries).
type EnergySeries interface {
	Series(context.Context, analyticsmodel.EnergySeriesQuery) (analyticsmodel.EnergySeriesResponse, error)
}

// QueryRates prices each verified hour at the Site's electricity cost over the energy the query
// service priced in that hour (#575), so avoided energy is priced on the same definition as every
// other cost the platform shows.
type QueryRates struct {
	series EnergySeries
}

func NewQueryRates(series EnergySeries) *QueryRates {
	return &QueryRates{series: series}
}

func (rates *QueryRates) HourlyRates(ctx context.Context, tenantID, siteID string, location *time.Location, from, to time.Time) (func(time.Time) (float64, bool), error) {
	series, err := rates.series.Series(ctx, analyticsmodel.EnergySeriesQuery{
		TenantID: tenantID, SiteID: siteID, EnergyType: analyticsmodel.EnergyTypeElectricity, Granularity: analyticsmodel.GranularityHour,
		Timezone: location.String(), From: from, To: to, QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("read the Site's priced hours: %w", err)
	}
	byStart := make(map[int64]float64, len(series.Points))
	for _, point := range series.Points {
		if point.Cost == nil {
			continue
		}
		if priced := point.EnergyKWh - point.Cost.UnpricedKWh; priced > 0 {
			byStart[point.PeriodStart.Unix()] = point.Cost.AmountCNY / priced
		}
	}
	return func(at time.Time) (float64, bool) {
		rate, ok := byStart[at.Unix()]
		return rate, ok
	}, nil
}
