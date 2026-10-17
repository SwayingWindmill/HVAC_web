package report

import (
	"context"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/energyseries"
)

// QueryHours reads a Site's valid electricity, its cost and cooling by Site-local hour from the
// Telemetry Query Service, as report-service's own Workload Principal.
type QueryHours struct {
	series *energyseries.Client
}

func NewQueryHours(series *energyseries.Client) *QueryHours {
	return &QueryHours{series: series}
}

func (reader *QueryHours) Hours(ctx context.Context, tenantID, siteID string, location *time.Location, from, to time.Time) ([]Hour, error) {
	byStart := map[int64]*Hour{}
	at := func(start time.Time) *Hour {
		hour, ok := byStart[start.Unix()]
		if !ok {
			hour = &Hour{Start: start.UTC()}
			byStart[start.Unix()] = hour
		}
		return hour
	}
	for _, energyType := range []analyticsmodel.EnergyType{analyticsmodel.EnergyTypeElectricity, analyticsmodel.EnergyTypeCooling} {
		query := analyticsmodel.EnergySeriesQuery{
			TenantID: tenantID, SiteID: siteID, EnergyType: energyType, Granularity: analyticsmodel.GranularityHour,
			Timezone: location.String(), From: from, To: to, QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
		}
		series, err := reader.series.Series(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, point := range series.Points {
			hour := at(point.PeriodStart)
			if energyType == analyticsmodel.EnergyTypeElectricity {
				hour.ElectricityKWh, hour.HasElectricity, hour.Cost = point.EnergyKWh, true, point.Cost
			} else {
				hour.CoolingKWh = point.EnergyKWh
			}
		}
	}
	hours := make([]Hour, 0, len(byStart))
	for _, hour := range byStart {
		hours = append(hours, *hour)
	}
	sort.Slice(hours, func(left, right int) bool { return hours[left].Start.Before(hours[right].Start) })
	return hours, nil
}
