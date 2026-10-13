package report

import (
	"math"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

var shanghai = mustLocation("Asia/Shanghai")

func mustLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}

// 2026-09-14 is a Monday: 00:00–08:00 valley at 0.32, 08:00–24:00 peak at 1.2, every day.
func valleyPeak(from time.Time) Tariff {
	periods := []tariffmodel.Period{}
	for _, dayType := range []string{"WEEKDAY", "WEEKEND"} {
		periods = append(periods,
			tariffmodel.Period{Code: "VALLEY", DayType: dayType, StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.32},
			tariffmodel.Period{Code: "PEAK", DayType: dayType, StartMinute: 480, EndMinute: 1440, EnergyRatePerKWh: 1.2},
		)
	}
	return Tariff{VersionID: "tou-v1", EffectiveFrom: from, Location: shanghai, Periods: periods}
}

func day(date time.Time, electricity, cooling float64) []Hour {
	hours := make([]Hour, 0, 24)
	for hour := 0; hour < 24; hour++ {
		hours = append(hours, Hour{Start: date.Add(time.Duration(hour) * time.Hour), ElectricityKWh: electricity, CoolingKWh: cooling, HasElectricity: true})
	}
	return hours
}

func near(left, right float64) bool { return math.Abs(left-right) < 1e-9 }

func TestAMonthIsTotalledByDayAndTariffPeriod(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	hours := append(day(first, 10, 40), day(first.AddDate(0, 0, 1), 5, 20)...)
	snapshot, err := Build("2026-09", shanghai, hours, []Tariff{valleyPeak(first.AddDate(0, -1, 0))}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DaysInMonth != 30 || snapshot.DaysWithData != 2 || !near(snapshot.ElectricityKWh, 360) || !near(snapshot.CoolingKWh, 1440) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.COP == nil || !near(*snapshot.COP, 4) {
		t.Fatalf("cop = %v", snapshot.COP)
	}
	// Valley: 8 h × (10 + 5) kWh × 0.32; peak: 16 h × 15 kWh × 1.2.
	if snapshot.EnergyCostCNY == nil || !near(*snapshot.EnergyCostCNY, 38.4+288) {
		t.Fatalf("cost = %v", snapshot.EnergyCostCNY)
	}
	if len(snapshot.TariffPeriods) != 2 || snapshot.TariffPeriods[0].Code != "PEAK" || !near(snapshot.TariffPeriods[0].ElectricityKWh, 240) ||
		!near(snapshot.TariffPeriods[1].CostCNY, 38.4) {
		t.Fatalf("periods = %#v", snapshot.TariffPeriods)
	}
	if len(snapshot.Days) != 2 || snapshot.Days[0].Date != "2026-09-14" || !near(snapshot.Days[1].ElectricityKWh, 120) ||
		snapshot.Days[0].EnergyCostCNY == nil || !near(*snapshot.Days[0].EnergyCostCNY, 25.6+192) {
		t.Fatalf("days = %#v", snapshot.Days)
	}
}

func TestAMonthWithoutATariffHasNoCost(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	snapshot, err := Build("2026-09", shanghai, day(first, 10, 40), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EnergyCostCNY != nil || len(snapshot.TariffPeriods) != 0 || snapshot.Days[0].EnergyCostCNY != nil || snapshot.EnergyCostStatus != CostNoTariff {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestADayWithCoolingReadingsOnlyHasNoMetering(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	coolingOnly := Hour{Start: first.AddDate(0, 0, 1), CoolingKWh: 40}
	snapshot, err := Build("2026-09", shanghai, append(day(first, 10, 40), coolingOnly), []Tariff{valleyPeak(first.AddDate(0, -1, 0))}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DaysWithData != 1 || len(snapshot.Days) != 1 || !near(snapshot.CoolingKWh, 1000) || snapshot.EnergyCostStatus != CostPriced {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestAMonthWithAnUnpricedHourHasNoCost(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	// The Tariff takes effect at noon on the first day, so the morning is not priced.
	snapshot, err := Build("2026-09", shanghai, day(first, 10, 40), []Tariff{valleyPeak(first.Add(12 * time.Hour))}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EnergyCostCNY != nil || len(snapshot.TariffPeriods) != 0 || snapshot.EnergyCostStatus != CostUnpricedHours {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestAMonthCarriesItsVerifiedSavingsAndFindings(t *testing.T) {
	snapshot, err := Build("2026-09", shanghai, nil, nil,
		// Two measures on different devices verified the 14th; the plant's electricity counts once.
		[]SavingsDay{
			{Day: "2026-09-14", AdjustedBaselineKWh: 500, AvoidedKWh: 60, AvoidedCost: 50, PlantKWh: 3000},
			{Day: "2026-09-14", AdjustedBaselineKWh: 100, AvoidedKWh: 10, AvoidedCost: 8, PlantKWh: 3000},
			{Day: "2026-09-15", AdjustedBaselineKWh: 520, AvoidedKWh: 62, AvoidedCost: 52, UnpricedHours: 2, PlantKWh: 3100},
		},
		[]FindingCount{{FindingType: "CHILLED_WATER_LOW_DELTA_T", Count: 3}, {FindingType: "OTHER", Count: 1}})
	if err != nil {
		t.Fatal(err)
	}
	savings := snapshot.Savings
	if savings.Days != 2 || !near(savings.AdjustedBaselineKWh, 1120) || !near(savings.AvoidedKWh, 132) || !near(savings.AvoidedCost, 110) || savings.UnpricedHours != 2 || !near(savings.PlantKWh, 6100) {
		t.Fatalf("savings = %#v", savings)
	}
	if snapshot.Findings.Count != 4 || snapshot.Findings.ByType[0].FindingType != "CHILLED_WATER_LOW_DELTA_T" {
		t.Fatalf("findings = %#v", snapshot.Findings)
	}
	if snapshot.DaysWithData != 0 || snapshot.COP != nil || snapshot.Days == nil {
		t.Fatalf("a month without readings = %#v", snapshot)
	}
}

func TestAnHourOutsideTheMonthIsRefused(t *testing.T) {
	if _, err := Build("2026-09", shanghai, day(time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai), 10, 40), nil, nil, nil); err == nil {
		t.Fatal("an October hour was put in the September report")
	}
}

func TestMonthBoundsFollowTheSiteCalendar(t *testing.T) {
	from, to, days, err := MonthBounds("2026-02", shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if !from.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, shanghai)) || !to.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, shanghai)) || days != 28 {
		t.Fatalf("bounds = %s %s %d", from, to, days)
	}
	if _, _, _, err := MonthBounds("2026-13", shanghai); err == nil {
		t.Fatal("month 13 accepted")
	}
}
