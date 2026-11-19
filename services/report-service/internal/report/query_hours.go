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

func (reader *QueryHours) Hours(ctx context.Context, tenantID, siteID string, location *time.Location, from, to time.Time) (MonthEnergy, error) {
	byStart := map[int64]*Hour{}
	at := func(start time.Time) *Hour {
		hour, ok := byStart[start.Unix()]
		if !ok {
			hour = &Hour{Start: start.UTC()}
			byStart[start.Unix()] = hour
		}
		return hour
	}
	timezone := "UTC"
	if location != nil {
		timezone = location.String()
	}
	var electricityCrossGap *analyticsmodel.EnergyCrossGap
	var coolingCrossGapKWh float64
	for _, energyType := range []analyticsmodel.EnergyType{analyticsmodel.EnergyTypeElectricity, analyticsmodel.EnergyTypeCooling} {
		query := analyticsmodel.EnergySeriesQuery{
			TenantID: tenantID, SiteID: siteID, EnergyType: energyType, Granularity: analyticsmodel.GranularityHour,
			Timezone: timezone, From: from, To: to, QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
		}
		series, err := reader.series.Series(ctx, query)
		if err != nil {
			return MonthEnergy{}, err
		}
		if energyType == analyticsmodel.EnergyTypeElectricity {
			electricityCrossGap = series.CrossGap
		} else if series.CrossGap != nil {
			coolingCrossGapKWh = series.CrossGap.EnergyKWh
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

	result := MonthEnergy{
		Hours:               hours,
		ElectricityCrossGap: electricityCrossGap,
		CoolingCrossGapKWh:  coolingCrossGapKWh,
	}
	// Enrichment diagnostics are optional; snapshot generation succeeds even if the route
	// fails or returns empty sections for sites without P2 instrumentation.
	if reader.series != nil {
		enrichment, err := reader.series.ReportEnrichment(ctx, analyticsmodel.EnergySeriesQuery{
			TenantID:      tenantID,
			SiteID:        siteID,
			EnergyType:    analyticsmodel.EnergyTypeElectricity,
			Granularity:   analyticsmodel.GranularityHour,
			Timezone:      timezone,
			From:          from,
			To:            to,
			QualityPolicy: analyticsmodel.QualityPolicyValidOnly,
		})
		if err == nil {
			mapEnrichment(&result, enrichment)
		}
	}
	return result, nil
}

func mapEnrichment(target *MonthEnergy, enrichment analyticsmodel.ReportEnrichmentResponse) {
	if enrichment.LayeredEfficiency != nil && enrichment.LayeredEfficiency.HasMetrics() {
		target.LayeredEfficiency = &LayeredEfficiencySnapshot{
			PlantCOP:        enrichment.LayeredEfficiency.PlantCOP,
			ChillerCOP:      enrichment.LayeredEfficiency.ChillerCOP,
			ChilledPumpWTF:  enrichment.LayeredEfficiency.ChilledPumpWTF,
			CoolingPumpWTF:  enrichment.LayeredEfficiency.CoolingPumpWTF,
			TowerPowerShare: enrichment.LayeredEfficiency.TowerPowerShare,
		}
	}
	if enrichment.MeterBalance != nil {
		target.MeterBalance = &MeterBalanceSnapshot{
			SiteKWh:        enrichment.MeterBalance.SiteKWh,
			SubmeterSumKWh: enrichment.MeterBalance.SubmeterSumKWh,
			DiffKWh:        enrichment.MeterBalance.DiffKWh,
			ImbalanceRatio: enrichment.MeterBalance.ImbalanceRatio,
			Status:         string(enrichment.MeterBalance.Status),
		}
	}
	if enrichment.ConditionAnchors != nil && enrichment.ConditionAnchors.HasValues() {
		target.ConditionAnchors = &ConditionAnchorsSnapshot{
			MeanWetBulbC:      enrichment.ConditionAnchors.MeanWetBulbC,
			MeanDryBulbC:      enrichment.ConditionAnchors.MeanDryBulbC,
			MeanLoadRatio:     enrichment.ConditionAnchors.MeanLoadRatio,
			AIClosedLoopShare: enrichment.ConditionAnchors.AIClosedLoopShare,
		}
	}
}
