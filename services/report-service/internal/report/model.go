// Package report keeps a Site's monthly energy report as a snapshot taken once the month is over,
// so a closed month reads the same however its records change later.
package report

import (
	"fmt"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

// SchemaVersion is the shape of a stored snapshot's payload.
const SchemaVersion = 1

// Hour is one Site-local hour of the Site's valid energy, starting at Start, with its electricity
// as the query service priced it (#575). HasElectricity is false for an hour with cooling readings
// only, which has no Cost.
type Hour struct {
	Start          time.Time
	ElectricityKWh float64
	CoolingKWh     float64
	HasElectricity bool
	Cost           *analyticsmodel.EnergyCost
}

// Why a snapshot has or lacks an energy cost: every kWh priced, none of it priced (no Tariff in
// effect), or some of it outside every Tariff period.
const (
	CostPriced        = "PRICED"
	CostNoTariff      = "NO_TARIFF"
	CostUnpricedHours = "UNPRICED_HOURS"
)

// SavingsDay is one verified day of a measure in the month, with the plant's electricity over
// the hours it verified.
type SavingsDay struct {
	Day                 string
	AdjustedBaselineKWh float64
	AvoidedKWh          float64
	AvoidedCost         float64
	UnpricedHours       int
	PlantKWh            float64
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

// Savings sums the month's verified days over every measure. Days and the plant's electricity
// count each day once, however many measures verified it.
type Savings struct {
	Days                int     `json:"days"`
	AdjustedBaselineKWh float64 `json:"adjustedBaselineKWh"`
	AvoidedKWh          float64 `json:"avoidedKWh"`
	AvoidedCost         float64 `json:"avoidedCost"`
	UnpricedHours       int     `json:"unpricedHours"`
	PlantKWh            float64 `json:"plantKWh"`
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
	// EnergyCostCNY is the month's electricity as the query service priced it at the time-of-use
	// energy rates; nil unless every kWh was priced. Demand charges are not in it.
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

// Build totals a month's hours and their costs by day and by Tariff period, with its verified
// savings and findings.
func Build(month string, location *time.Location, hours []Hour, savings []SavingsDay, findings []FindingCount) (Snapshot, error) {
	from, to, daysInMonth, err := MonthBounds(month, location)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion, Month: month, Timezone: location.String(), DaysInMonth: daysInMonth,
		TariffPeriods: []PeriodTotal{}, Days: []Day{}, Findings: Findings{ByType: []FindingCount{}},
	}
	byDay := map[string]*Day{}
	dayCost := map[string]float64{}
	periods := map[string]*PeriodTotal{}
	var cost, pricedKWh, unpricedKWh float64
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
		if !hour.HasElectricity {
			continue
		}
		if hour.Cost == nil {
			unpricedKWh += hour.ElectricityKWh
			continue
		}
		cost += hour.Cost.AmountCNY
		dayCost[date] += hour.Cost.AmountCNY
		unpricedKWh += hour.Cost.UnpricedKWh
		for _, period := range hour.Cost.Periods {
			total := periods[period.PeriodCode]
			if total == nil {
				total = &PeriodTotal{Code: period.PeriodCode}
				periods[period.PeriodCode] = total
			}
			total.ElectricityKWh += period.EnergyKWh
			total.CostCNY += period.AmountCNY
			pricedKWh += period.EnergyKWh
		}
	}
	priced := unpricedKWh == 0
	switch {
	case priced:
		snapshot.EnergyCostStatus = CostPriced
	case pricedKWh == 0:
		snapshot.EnergyCostStatus = CostNoTariff
	default:
		snapshot.EnergyCostStatus = CostUnpricedHours
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
		// Dearest period first: super peak, peak, flat, valley.
		sort.Slice(snapshot.TariffPeriods, func(left, right int) bool {
			return periodOrder[snapshot.TariffPeriods[left].Code] < periodOrder[snapshot.TariffPeriods[right].Code]
		})
	}

	plant := map[string]float64{}
	for _, verified := range savings {
		plant[verified.Day] = max(plant[verified.Day], verified.PlantKWh)
		snapshot.Savings.AdjustedBaselineKWh += verified.AdjustedBaselineKWh
		snapshot.Savings.AvoidedKWh += verified.AvoidedKWh
		snapshot.Savings.AvoidedCost += verified.AvoidedCost
		snapshot.Savings.UnpricedHours += verified.UnpricedHours
	}
	snapshot.Savings.Days = len(plant)
	for _, kwh := range plant {
		snapshot.Savings.PlantKWh += kwh
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

var periodOrder = map[string]int{"SUPER_PEAK": 0, "PEAK": 1, "FLAT": 2, "VALLEY": 3}

func ratio(numerator, denominator float64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := numerator / denominator
	return &value
}
