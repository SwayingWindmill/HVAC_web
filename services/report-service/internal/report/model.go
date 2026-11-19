// Package report keeps a Site's monthly energy report as a snapshot taken once the month is over,
// so a closed month reads the same however its records change later.
package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

// SchemaVersion is the shape of a stored snapshot's payload.
const SchemaVersion = 1

const (
	minReportElectricityKWh = 1.0
	minReportCoolingKWh     = 1.0
	maxReportCOP            = 12.0
)

// Hour is one Site-local hour of the Site's valid energy, starting at Start, with its electricity
// as the query service priced it (#575). HasElectricity is false for an hour with cooling readings
// only, which has no Cost.
type Hour struct {
	Start          time.Time
	ElectricityKWh float64
	CoolingKWh     float64
	HasElectricity bool
	Incomplete     bool
	MissingMeters  []string
	Cost           *analyticsmodel.EnergyCost
}

// MonthEnergy holds a month's hourly energy alongside any > 1h cross-gap energy recovered in it (#798)
// and optional P2 efficiency, meter-balance, and operating-condition summaries.
type MonthEnergy struct {
	Hours               []Hour
	ElectricityCrossGap *analyticsmodel.EnergyCrossGap
	CoolingCrossGapKWh  float64
	Partial             bool
	LayeredEfficiency   *LayeredEfficiencySnapshot
	MeterBalance        *MeterBalanceSnapshot
	ConditionAnchors    *ConditionAnchorsSnapshot
}

// LayeredEfficiencySnapshot captures plant-to-subsystem monthly energy efficiency indicators.
type LayeredEfficiencySnapshot struct {
	PlantCOP        *float64 `json:"plantCOP,omitempty"`
	ChillerCOP      *float64 `json:"chillerCOP,omitempty"`
	ChilledPumpWTF  *float64 `json:"chilledPumpWTF,omitempty"`
	CoolingPumpWTF  *float64 `json:"coolingPumpWTF,omitempty"`
	TowerPowerShare *float64 `json:"towerPowerShare,omitempty"`
}

// MeterBalanceSnapshot captures the monthly total-meter vs sub-meter reconciliation check.
type MeterBalanceSnapshot struct {
	SiteKWh        float64  `json:"siteKWh"`
	SubmeterSumKWh float64  `json:"submeterSumKWh"`
	DiffKWh        float64  `json:"diffKWh"`
	ImbalanceRatio *float64 `json:"imbalanceRatio,omitempty"`
	Status         string   `json:"status"`
}

// ConditionAnchorsSnapshot summarizes monthly outdoor weather, load, and AI control anchors.
type ConditionAnchorsSnapshot struct {
	MeanWetBulbC      *float64 `json:"meanWetBulbC,omitempty"`
	MeanDryBulbC      *float64 `json:"meanDryBulbC,omitempty"`
	MeanLoadRatio     *float64 `json:"meanLoadRatio,omitempty"`
	AIClosedLoopShare *float64 `json:"aiClosedLoopShare,omitempty"`
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
	Incomplete     bool     `json:"incomplete,omitempty"`
	MissingMeters  []string `json:"missingMeters,omitempty"`
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
	SchemaVersion          int     `json:"schemaVersion"`
	Month                  string  `json:"month"`
	Timezone               string  `json:"timezone"`
	DaysInMonth            int     `json:"daysInMonth"`
	DaysWithData           int     `json:"daysWithData"`
	ElectricityKWh         float64 `json:"electricityKWh"`
	CrossGapElectricityKWh float64 `json:"crossGapElectricityKWh,omitempty"`
	CoolingKWh             float64 `json:"coolingKWh"`
	// COP is cooling over electricity; nil without electricity.
	COP *float64 `json:"cop"`
	// EnergyCostCNY is the month's electricity as the query service priced it at the time-of-use
	// energy rates; nil unless every kWh was priced. Demand charges are not in it.
	EnergyCostCNY     *float64                   `json:"energyCostCNY"`
	EnergyCostStatus  string                     `json:"energyCostStatus"`
	TariffPeriods     []PeriodTotal              `json:"tariffPeriods"`
	Days              []Day                      `json:"days"`
	Findings          Findings                   `json:"findings"`
	Savings           Savings                    `json:"savings"`
	Incomplete        bool                       `json:"incomplete,omitempty"`
	MissingMeters     []string                   `json:"missingMeters,omitempty"`
	LayeredEfficiency *LayeredEfficiencySnapshot `json:"layeredEfficiency,omitempty"`
	MeterBalance      *MeterBalanceSnapshot      `json:"meterBalance,omitempty"`
	ConditionAnchors  *ConditionAnchorsSnapshot  `json:"conditionAnchors,omitempty"`
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
// savings, findings, and any cross-gap energy (#798).
func Build(month string, location *time.Location, energy MonthEnergy, savings []SavingsDay, findings []FindingCount) (Snapshot, error) {
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
	dayIncomplete := map[string]bool{}
	dayMissing := map[string]map[string]struct{}{}
	snapshotMissing := map[string]struct{}{}
	snapshotIncomplete := energy.Partial
	periods := map[string]*PeriodTotal{}
	var cost, pricedKWh, unpricedKWh float64
	for _, hour := range energy.Hours {
		if hour.Start.Before(from) || !hour.Start.Before(to) {
			return Snapshot{}, fmt.Errorf("hour %s is outside %s", hour.Start.Format(time.RFC3339), month)
		}
		date := hour.Start.In(location).Format("2006-01-02")
		elecValid := isFiniteNonNegative(hour.ElectricityKWh)
		coolingValid := isFiniteNonNegative(hour.CoolingKWh)
		if !elecValid || !coolingValid {
			snapshotIncomplete = true
			dayIncomplete[date] = true
		}
		elecKWh := 0.0
		if elecValid {
			elecKWh = hour.ElectricityKWh
			snapshot.ElectricityKWh += elecKWh
		}
		coolingKWh := 0.0
		if coolingValid {
			coolingKWh = hour.CoolingKWh
			snapshot.CoolingKWh += coolingKWh
		}
		if hour.Incomplete || len(hour.MissingMeters) > 0 {
			snapshotIncomplete = true
			dayIncomplete[date] = true
			for _, rawMeter := range hour.MissingMeters {
				meter := strings.TrimSpace(rawMeter)
				if meter == "" {
					continue
				}
				snapshotMissing[meter] = struct{}{}
				if dayMissing[date] == nil {
					dayMissing[date] = map[string]struct{}{}
				}
				dayMissing[date][meter] = struct{}{}
			}
		}
		if !hour.HasElectricity && coolingValid && coolingKWh > 0 {
			snapshotIncomplete = true
		}
		// A day has metering when it has an electricity reading; cooling alone still counts in the
		// month's total.
		entry := byDay[date]
		if entry == nil && hour.HasElectricity {
			entry = &Day{Date: date}
			byDay[date] = entry
		}
		if entry != nil {
			entry.ElectricityKWh += elecKWh
			entry.CoolingKWh += coolingKWh
		}
		if !hour.HasElectricity {
			continue
		}
		if hour.Cost == nil || !validEnergyCost(hour.Cost) {
			if hour.Cost != nil {
				snapshotIncomplete = true
				dayIncomplete[date] = true
			}
			unpricedKWh += elecKWh
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
	if !isFiniteNonNegative(energy.CoolingCrossGapKWh) {
		snapshotIncomplete = true
	} else {
		snapshot.CoolingKWh += energy.CoolingCrossGapKWh
	}
	if energy.ElectricityCrossGap != nil {
		if !isFiniteNonNegative(energy.ElectricityCrossGap.EnergyKWh) {
			snapshotIncomplete = true
		} else if energy.ElectricityCrossGap.EnergyKWh > 0 {
			cgKWh := energy.ElectricityCrossGap.EnergyKWh
			snapshot.ElectricityKWh += cgKWh
			snapshot.CrossGapElectricityKWh = cgKWh
			if energy.ElectricityCrossGap.Cost == nil || !validEnergyCost(energy.ElectricityCrossGap.Cost) {
				if energy.ElectricityCrossGap.Cost != nil {
					snapshotIncomplete = true
				}
				unpricedKWh += cgKWh
			} else {
				cost += energy.ElectricityCrossGap.Cost.AmountCNY
				unpricedKWh += energy.ElectricityCrossGap.Cost.UnpricedKWh
				for _, period := range energy.ElectricityCrossGap.Cost.Periods {
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
		entry.COP = reportCOP(entry.CoolingKWh, entry.ElectricityKWh)
		entry.Incomplete = dayIncomplete[date]
		entry.MissingMeters = sortedKeys(dayMissing[date])
		if priced {
			value := dayCost[date]
			entry.EnergyCostCNY = &value
		}
		snapshot.Days = append(snapshot.Days, entry)
	}
	snapshot.DaysWithData = len(dates)
	snapshot.COP = reportCOP(snapshot.CoolingKWh, snapshot.ElectricityKWh)
	snapshot.Incomplete = snapshotIncomplete
	snapshot.MissingMeters = sortedKeys(snapshotMissing)
	if priced && (len(energy.Hours) > 0 || snapshot.CrossGapElectricityKWh > 0) {
		snapshot.EnergyCostCNY = &cost
		for _, total := range periods {
			snapshot.TariffPeriods = append(snapshot.TariffPeriods, *total)
		}
		// Dearest period first: super peak, peak, flat, valley, deep valley.
		sort.SliceStable(snapshot.TariffPeriods, func(left, right int) bool {
			return periodRank(snapshot.TariffPeriods[left].Code) < periodRank(snapshot.TariffPeriods[right].Code)
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
	snapshot.LayeredEfficiency = sanitizeLayeredEfficiency(energy.LayeredEfficiency)
	snapshot.MeterBalance = sanitizeMeterBalance(energy.MeterBalance)
	snapshot.ConditionAnchors = sanitizeConditionAnchors(energy.ConditionAnchors)
	return snapshot, nil
}

func periodRank(code string) int {
	if rank, ok := map[string]int{"SUPER_PEAK": 0, "PEAK": 1, "FLAT": 2, "VALLEY": 3, "DEEP_VALLEY": 4}[code]; ok {
		return rank
	}
	return 5
}

func reportCOP(coolingKWh, electricityKWh float64) *float64 {
	if !isFiniteNonNegative(coolingKWh) || !isFiniteNonNegative(electricityKWh) {
		return nil
	}
	if electricityKWh < minReportElectricityKWh || coolingKWh < minReportCoolingKWh {
		return nil
	}
	value := coolingKWh / electricityKWh
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > maxReportCOP {
		return nil
	}
	return &value
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isFiniteNonNegative(value float64) bool {
	return isFinite(value) && value >= 0
}

func sanitizeFloatPtr(value *float64) *float64 {
	if value == nil || !isFinite(*value) {
		return nil
	}
	v := *value
	return &v
}

func sanitizeLayeredEfficiency(src *LayeredEfficiencySnapshot) *LayeredEfficiencySnapshot {
	if src == nil {
		return nil
	}
	sanitized := &LayeredEfficiencySnapshot{
		PlantCOP:        sanitizeFloatPtr(src.PlantCOP),
		ChillerCOP:      sanitizeFloatPtr(src.ChillerCOP),
		ChilledPumpWTF:  sanitizeFloatPtr(src.ChilledPumpWTF),
		CoolingPumpWTF:  sanitizeFloatPtr(src.CoolingPumpWTF),
		TowerPowerShare: sanitizeFloatPtr(src.TowerPowerShare),
	}
	if sanitized.PlantCOP == nil && sanitized.ChillerCOP == nil && sanitized.ChilledPumpWTF == nil &&
		sanitized.CoolingPumpWTF == nil && sanitized.TowerPowerShare == nil {
		return nil
	}
	return sanitized
}

func sanitizeMeterBalance(src *MeterBalanceSnapshot) *MeterBalanceSnapshot {
	if src == nil || !isFinite(src.SiteKWh) || !isFinite(src.SubmeterSumKWh) {
		return nil
	}
	diff := src.DiffKWh
	if !isFinite(diff) {
		diff = src.SiteKWh - src.SubmeterSumKWh
	}
	return &MeterBalanceSnapshot{
		SiteKWh:        src.SiteKWh,
		SubmeterSumKWh: src.SubmeterSumKWh,
		DiffKWh:        diff,
		ImbalanceRatio: sanitizeFloatPtr(src.ImbalanceRatio),
		Status:         strings.TrimSpace(src.Status),
	}
}

func sanitizeConditionAnchors(src *ConditionAnchorsSnapshot) *ConditionAnchorsSnapshot {
	if src == nil {
		return nil
	}
	sanitized := &ConditionAnchorsSnapshot{
		MeanWetBulbC:      sanitizeFloatPtr(src.MeanWetBulbC),
		MeanDryBulbC:      sanitizeFloatPtr(src.MeanDryBulbC),
		MeanLoadRatio:     sanitizeFloatPtr(src.MeanLoadRatio),
		AIClosedLoopShare: sanitizeFloatPtr(src.AIClosedLoopShare),
	}
	if sanitized.MeanWetBulbC == nil && sanitized.MeanDryBulbC == nil &&
		sanitized.MeanLoadRatio == nil && sanitized.AIClosedLoopShare == nil {
		return nil
	}
	return sanitized
}

func validEnergyCost(cost *analyticsmodel.EnergyCost) bool {
	if cost == nil || !isFiniteNonNegative(cost.AmountCNY) || !isFiniteNonNegative(cost.UnpricedKWh) {
		return false
	}
	for _, period := range cost.Periods {
		if !isFiniteNonNegative(period.EnergyKWh) || !isFiniteNonNegative(period.AmountCNY) {
			return false
		}
	}
	return true
}

func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
