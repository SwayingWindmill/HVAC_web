// Package report keeps a Site's monthly energy report as a snapshot taken once the month is over,
// so a closed month reads the same however its records change later.
package report

import (
	"fmt"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

// SchemaVersion is the shape of a stored snapshot's payload.
const SchemaVersion = 1

// Hour is one Site-local hour of the Site's valid energy, starting at Start. HasElectricity is
// false for an hour with cooling readings only.
type Hour struct {
	Start          time.Time
	ElectricityKWh float64
	CoolingKWh     float64
	HasElectricity bool
}

// Why a snapshot has or lacks an energy cost.
const (
	CostPriced        = "PRICED"
	CostNoTariff      = "NO_TARIFF"
	CostUnpricedHours = "UNPRICED_HOURS"
)

// Tariff is a Site's import Tariff Version over the time it is in effect. EffectiveTo is nil while
// it has no successor.
type Tariff struct {
	VersionID     string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Location      *time.Location
	Periods       []tariffmodel.Period
}

// SavingsDay is one verified day of a measure in the month.
type SavingsDay struct {
	AdjustedBaselineKWh float64
	AvoidedKWh          float64
	AvoidedCost         float64
	UnpricedHours       int
}

type FindingCount struct {
	FindingType string `json:"findingType"`
	Count       int    `json:"count"`
}

type PeriodTotal struct {
	Code           string  `json:"code"`
	ElectricityKWh float64 `json:"electricityKWh"`
	CostCNY        float64 `json:"costCNY"`
}

type Day struct {
	Date           string   `json:"date"`
	ElectricityKWh float64  `json:"electricityKWh"`
	CoolingKWh     float64  `json:"coolingKWh"`
	COP            *float64 `json:"cop"`
	EnergyCostCNY  *float64 `json:"energyCostCNY"`
}

type Findings struct {
	Count  int            `json:"count"`
	ByType []FindingCount `json:"byType"`
}

type Savings struct {
	Days                int     `json:"days"`
	AdjustedBaselineKWh float64 `json:"adjustedBaselineKWh"`
	AvoidedKWh          float64 `json:"avoidedKWh"`
	AvoidedCost         float64 `json:"avoidedCost"`
	UnpricedHours       int     `json:"unpricedHours"`
}

// Snapshot is a Site's month as its report shows it. Demand, alarms and work orders are not in it:
// demand needs quarter-hours, and alarms and work orders are other owners' records.
type Snapshot struct {
	SchemaVersion  int     `json:"schemaVersion"`
	Month          string  `json:"month"`
	Timezone       string  `json:"timezone"`
	DaysInMonth    int     `json:"daysInMonth"`
	DaysWithData   int     `json:"daysWithData"`
	ElectricityKWh float64 `json:"electricityKWh"`
	CoolingKWh     float64 `json:"coolingKWh"`
	// COP is cooling over electricity; nil without electricity.
	COP *float64 `json:"cop"`
	// EnergyCostCNY is each hour's electricity at its time-of-use energy rate; nil when the Site has no
	// import Tariff or an hour of the month is not priced. Demand charges are not in it.
	EnergyCostCNY    *float64      `json:"energyCostCNY"`
	EnergyCostStatus string        `json:"energyCostStatus"`
	TariffPeriods    []PeriodTotal `json:"tariffPeriods"`
	Days             []Day         `json:"days"`
	Findings         Findings      `json:"findings"`
	Savings          Savings       `json:"savings"`
}

// MonthBounds is a YYYY-MM month on the Site's calendar: its first instant, the next month's first
// instant, and its number of days.
func MonthBounds(month string, location *time.Location) (time.Time, time.Time, int, error) {
	parsed, err := time.ParseInLocation("2006-01", month, location)
	if err != nil {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("%w: %q is not YYYY-MM", ErrInvalidMonth, month)
	}
	end := parsed.AddDate(0, 1, 0)
	return parsed, end, end.AddDate(0, 0, -1).Day(), nil
}

// Build totals a month's hours by day and by Tariff period, with its verified savings and findings.
func Build(month string, location *time.Location, hours []Hour, tariffs []Tariff, savings []SavingsDay, findings []FindingCount) (Snapshot, error) {
	from, to, daysInMonth, err := MonthBounds(month, location)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion, Month: month, Timezone: location.String(), DaysInMonth: daysInMonth,
		TariffPeriods: []PeriodTotal{}, Days: []Day{}, Findings: Findings{ByType: []FindingCount{}},
	}
	priced := len(tariffs) > 0
	snapshot.EnergyCostStatus = CostNoTariff
	if priced {
		snapshot.EnergyCostStatus = CostPriced
	}
	byDay := map[string]*Day{}
	dayCost := map[string]float64{}
	periods := map[string]*PeriodTotal{}
	periodRates := map[string]float64{}
	var cost float64
	for _, hour := range hours {
		if hour.Start.Before(from) || !hour.Start.Before(to) {
			return Snapshot{}, fmt.Errorf("hour %s is outside %s", hour.Start.Format(time.RFC3339), month)
		}
		snapshot.ElectricityKWh += hour.ElectricityKWh
		snapshot.CoolingKWh += hour.CoolingKWh
		// A day has metering when it has an electricity reading; cooling alone still counts in the
		// month's total.
		date := hour.Start.In(location).Format("2006-01-02")
		entry := byDay[date]
		if entry == nil && hour.HasElectricity {
			entry = &Day{Date: date}
			byDay[date] = entry
		}
		if entry != nil {
			entry.ElectricityKWh += hour.ElectricityKWh
			entry.CoolingKWh += hour.CoolingKWh
		}
		if !priced {
			continue
		}
		if !hour.HasElectricity {
			continue
		}
		period, ok := coveringPeriod(tariffs, hour.Start)
		if !ok {
			priced = false
			snapshot.EnergyCostStatus = CostUnpricedHours
			continue
		}
		hourCost := hour.ElectricityKWh * period.EnergyRatePerKWh
		cost += hourCost
		dayCost[date] += hourCost
		total := periods[period.Code]
		if total == nil {
			total = &PeriodTotal{Code: period.Code}
			periods[period.Code] = total
			periodRates[period.Code] = period.EnergyRatePerKWh
		}
		total.ElectricityKWh += hour.ElectricityKWh
		total.CostCNY += hourCost
	}

	dates := make([]string, 0, len(byDay))
	for date := range byDay {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		entry := *byDay[date]
		entry.COP = ratio(entry.CoolingKWh, entry.ElectricityKWh)
		if priced {
			value := dayCost[date]
			entry.EnergyCostCNY = &value
		}
		snapshot.Days = append(snapshot.Days, entry)
	}
	snapshot.DaysWithData = len(dates)
	snapshot.COP = ratio(snapshot.CoolingKWh, snapshot.ElectricityKWh)
	if priced && len(hours) > 0 {
		snapshot.EnergyCostCNY = &cost
		for _, total := range periods {
			snapshot.TariffPeriods = append(snapshot.TariffPeriods, *total)
		}
		// Dearest period first: peak, flat, valley on a time-of-use Tariff.
		sort.Slice(snapshot.TariffPeriods, func(left, right int) bool {
			leftRate, rightRate := periodRates[snapshot.TariffPeriods[left].Code], periodRates[snapshot.TariffPeriods[right].Code]
			if leftRate != rightRate {
				return leftRate > rightRate
			}
			return snapshot.TariffPeriods[left].Code < snapshot.TariffPeriods[right].Code
		})
	}

	for _, verified := range savings {
		snapshot.Savings.Days++
		snapshot.Savings.AdjustedBaselineKWh += verified.AdjustedBaselineKWh
		snapshot.Savings.AvoidedKWh += verified.AvoidedKWh
		snapshot.Savings.AvoidedCost += verified.AvoidedCost
		snapshot.Savings.UnpricedHours += verified.UnpricedHours
	}
	for _, count := range findings {
		snapshot.Findings.Count += count.Count
		snapshot.Findings.ByType = append(snapshot.Findings.ByType, count)
	}
	sort.SliceStable(snapshot.Findings.ByType, func(left, right int) bool {
		return snapshot.Findings.ByType[left].Count > snapshot.Findings.ByType[right].Count
	})
	return snapshot, nil
}

func coveringPeriod(tariffs []Tariff, at time.Time) (tariffmodel.Period, bool) {
	for _, tariff := range tariffs {
		if at.Before(tariff.EffectiveFrom) || (tariff.EffectiveTo != nil && !at.Before(*tariff.EffectiveTo)) {
			continue
		}
		return tariffmodel.Covering(tariff.Periods, tariff.Location, at)
	}
	return tariffmodel.Period{}, false
}

func ratio(numerator, denominator float64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := numerator / denominator
	return &value
}
