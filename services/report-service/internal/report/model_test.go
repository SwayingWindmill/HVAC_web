package report

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
)

var shanghai = mustLocation("Asia/Shanghai")

func mustLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}

// The query service's price for a day's hours: 00:00–08:00 valley at 0.32, 08:00–24:00 peak at 1.2.
// Hours before pricedFrom are unpriced, as when no Tariff period covers them.
func day(date time.Time, electricity, cooling float64, pricedFrom time.Time) []Hour {
	hours := make([]Hour, 0, 24)
	for hour := 0; hour < 24; hour++ {
		start := date.Add(time.Duration(hour) * time.Hour)
		cost := &analyticsmodel.EnergyCost{UnpricedKWh: electricity, Periods: []analyticsmodel.PeriodCost{}}
		if !start.Before(pricedFrom) {
			code, rate := "PEAK", 1.2
			if start.In(shanghai).Hour() < 8 {
				code, rate = "VALLEY", 0.32
			}
			cost = &analyticsmodel.EnergyCost{AmountCNY: electricity * rate, Periods: []analyticsmodel.PeriodCost{{PeriodCode: code, EnergyKWh: electricity, AmountCNY: electricity * rate}}}
		}
		hours = append(hours, Hour{Start: start, ElectricityKWh: electricity, CoolingKWh: cooling, HasElectricity: true, Cost: cost})
	}
	return hours
}

var always = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
var never = time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)

func near(left, right float64) bool { return math.Abs(left-right) < 1e-9 }

func TestAMonthIsTotalledByDayAndTariffPeriod(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	hours := append(day(first, 10, 40, always), day(first.AddDate(0, 0, 1), 5, 20, always)...)
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: hours}, nil, nil)
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

func TestCrossGapEnergyCountsInMonthTotalsWithoutEnteringDailyCurve(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{
		Hours: day(first, 10, 40, always),
		ElectricityCrossGap: &analyticsmodel.EnergyCrossGap{
			EnergyKWh: 100,
			Cost: &analyticsmodel.EnergyCost{
				AmountCNY: 120,
				Periods:   []analyticsmodel.PeriodCost{{PeriodCode: "PEAK", EnergyKWh: 100, AmountCNY: 120}},
			},
		},
		CoolingCrossGapKWh: 400,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !near(snapshot.ElectricityKWh, 340) || !near(snapshot.CrossGapElectricityKWh, 100) || !near(snapshot.CoolingKWh, 1360) {
		t.Fatalf("snapshot totals = %#v", snapshot)
	}
	if len(snapshot.Days) != 1 || !near(snapshot.Days[0].ElectricityKWh, 240) {
		t.Fatalf("days = %#v", snapshot.Days)
	}
	if snapshot.EnergyCostCNY == nil || !near(*snapshot.EnergyCostCNY, 25.6+192+120) {
		t.Fatalf("cost = %v", snapshot.EnergyCostCNY)
	}
}

// No Tariff priced any of the month's electricity.
func TestAMonthWithoutATariffHasNoCost(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: day(first, 10, 40, never)}, nil, nil)
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
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: append(day(first, 10, 40, always), coolingOnly)}, nil, nil)
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
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: day(first, 10, 40, first.Add(12*time.Hour))}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EnergyCostCNY != nil || len(snapshot.TariffPeriods) != 0 || snapshot.EnergyCostStatus != CostUnpricedHours {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestAMonthCarriesItsVerifiedSavingsAndFindings(t *testing.T) {
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{},
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
	if _, err := Build("2026-09", shanghai, MonthEnergy{Hours: day(time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai), 10, 40, always)}, nil, nil); err == nil {
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

func TestReportCOPUsesEnergyWeightedSumAndHandlesStandbyMissingInvalidAndIncomplete(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)

	t.Run("energy-weighted COP across varying hourly loads", func(t *testing.T) {
		// Hour 0: 10 kWh elec, 60 kWh cooling (instant COP = 6.0)
		// Hour 1: 100 kWh elec, 350 kWh cooling (instant COP = 3.5)
		// Energy-weighted COP = 410 / 110 = 3.727272..., NOT arithmetic avg (6.0 + 3.5)/2 = 4.75.
		hours := []Hour{
			{Start: first, ElectricityKWh: 10, CoolingKWh: 60, HasElectricity: true},
			{Start: first.Add(time.Hour), ElectricityKWh: 100, CoolingKWh: 350, HasElectricity: true},
		}
		snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: hours}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := 410.0 / 110.0
		if snapshot.COP == nil || !near(*snapshot.COP, want) {
			t.Fatalf("snapshot COP = %v, want %v", snapshot.COP, want)
		}
		if len(snapshot.Days) != 1 || snapshot.Days[0].COP == nil || !near(*snapshot.Days[0].COP, want) {
			t.Fatalf("day COP = %+v, want %v", snapshot.Days, want)
		}
	})

	t.Run("returns nil COP for standby, missing cooling, zero power, or out-of-bounds ratio", func(t *testing.T) {
		cases := []struct {
			name  string
			hours []Hour
		}{
			{
				name:  "standby power below deadband",
				hours: []Hour{{Start: first, ElectricityKWh: 0.2, CoolingKWh: 20, HasElectricity: true}},
			},
			{
				name:  "missing or zero cooling",
				hours: []Hour{{Start: first, ElectricityKWh: 100, CoolingKWh: 0, HasElectricity: true}},
			},
			{
				name:  "zero electricity",
				hours: []Hour{{Start: first, ElectricityKWh: 0, CoolingKWh: 100, HasElectricity: true}},
			},
			{
				name:  "physically impossible COP > 12",
				hours: []Hour{{Start: first, ElectricityKWh: 2, CoolingKWh: 160, HasElectricity: true}},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: tc.hours}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.COP != nil {
					t.Fatalf("snapshot COP = %v, want nil", *snapshot.COP)
				}
				if len(snapshot.Days) == 1 && snapshot.Days[0].COP != nil {
					t.Fatalf("day COP = %v, want nil", *snapshot.Days[0].COP)
				}
			})
		}
	})

	t.Run("preserves submeter incompleteness and ignores NaN/Inf/negative readings cleanly", func(t *testing.T) {
		hours := []Hour{
			{Start: first, ElectricityKWh: 50, CoolingKWh: 200, HasElectricity: true, Incomplete: true, MissingMeters: []string{"2号电表（D机组）", "1号电表（B机组）"}},
			{Start: first.Add(time.Hour), ElectricityKWh: math.NaN(), CoolingKWh: math.Inf(1), HasElectricity: true},
			{Start: first.Add(2 * time.Hour), ElectricityKWh: -10, CoolingKWh: -5, HasElectricity: true},
		}
		snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: hours, Partial: true}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Incomplete || len(snapshot.MissingMeters) != 2 || snapshot.MissingMeters[0] != "1号电表（B机组）" || snapshot.MissingMeters[1] != "2号电表（D机组）" {
			t.Fatalf("snapshot completeness = %+v", snapshot)
		}
		if !near(snapshot.ElectricityKWh, 50) || !near(snapshot.CoolingKWh, 200) {
			t.Fatalf("invalid readings poisoned totals: elec=%v cooling=%v", snapshot.ElectricityKWh, snapshot.CoolingKWh)
		}
		if snapshot.COP == nil || !near(*snapshot.COP, 4.0) {
			t.Fatalf("snapshot COP = %v, want 4.0 from valid reading", snapshot.COP)
		}
		if len(snapshot.Days) != 1 || !snapshot.Days[0].Incomplete || len(snapshot.Days[0].MissingMeters) != 2 {
			t.Fatalf("day completeness = %+v", snapshot.Days)
		}
	})
}

func TestFiveTierTariffPeriodsIncludingDeepValleyAreTotalledAndSorted(t *testing.T) {
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)
	hours := []Hour{
		{
			Start: start, ElectricityKWh: 100, CoolingKWh: 400, HasElectricity: true,
			Cost: &analyticsmodel.EnergyCost{
				AmountCNY: 115,
				Periods: []analyticsmodel.PeriodCost{
					{PeriodCode: "DEEP_VALLEY", EnergyKWh: 20, AmountCNY: 4},
					{PeriodCode: "VALLEY", EnergyKWh: 20, AmountCNY: 8},
					{PeriodCode: "FLAT", EnergyKWh: 20, AmountCNY: 15},
					{PeriodCode: "PEAK", EnergyKWh: 20, AmountCNY: 28},
					{PeriodCode: "SUPER_PEAK", EnergyKWh: 20, AmountCNY: 60},
				},
			},
		},
	}
	snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: hours}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SUPER_PEAK", "PEAK", "FLAT", "VALLEY", "DEEP_VALLEY"}
	if len(snapshot.TariffPeriods) != len(want) {
		t.Fatalf("tariff periods len = %d, want %d (%#v)", len(snapshot.TariffPeriods), len(want), snapshot.TariffPeriods)
	}
	for idx, code := range want {
		if snapshot.TariffPeriods[idx].Code != code {
			t.Fatalf("tariff periods[%d] = %q, want %q", idx, snapshot.TariffPeriods[idx].Code, code)
		}
	}
	if !near(snapshot.TariffPeriods[4].CostCNY, 4) || !near(snapshot.TariffPeriods[4].ElectricityKWh, 20) {
		t.Fatalf("deep valley totals = %#v", snapshot.TariffPeriods[4])
	}
}

func reportFloatPtr(v float64) *float64 { return &v }

func TestBuildCarriesP2SnapshotEnrichmentAndSanitizesNonFinite(t *testing.T) {
	first := time.Date(2026, 9, 14, 0, 0, 0, 0, shanghai)

	t.Run("carries valid P2 sections with SchemaVersion 1", func(t *testing.T) {
		snapshot, err := Build("2026-09", shanghai, MonthEnergy{
			Hours: day(first, 10, 40, always),
			LayeredEfficiency: &LayeredEfficiencySnapshot{
				PlantCOP:        reportFloatPtr(4.2),
				ChillerCOP:      reportFloatPtr(5.8),
				ChilledPumpWTF:  reportFloatPtr(31.5),
				CoolingPumpWTF:  reportFloatPtr(27.0),
				TowerPowerShare: reportFloatPtr(0.085),
			},
			MeterBalance: &MeterBalanceSnapshot{
				SiteKWh:        240.0,
				SubmeterSumKWh: 235.2,
				DiffKWh:        4.8,
				ImbalanceRatio: reportFloatPtr(0.02),
				Status:         "BALANCED",
			},
			ConditionAnchors: &ConditionAnchorsSnapshot{
				MeanWetBulbC:      reportFloatPtr(23.4),
				MeanDryBulbC:      reportFloatPtr(28.1),
				MeanLoadRatio:     reportFloatPtr(0.62),
				AIClosedLoopShare: reportFloatPtr(0.78),
			},
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.SchemaVersion != 1 {
			t.Fatalf("SchemaVersion = %d, want 1", snapshot.SchemaVersion)
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var roundTrip Snapshot
		if err := json.Unmarshal(raw, &roundTrip); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if roundTrip.LayeredEfficiency == nil || !near(*roundTrip.LayeredEfficiency.PlantCOP, 4.2) || !near(*roundTrip.LayeredEfficiency.ChilledPumpWTF, 31.5) {
			t.Fatalf("roundTrip.LayeredEfficiency = %+v", roundTrip.LayeredEfficiency)
		}
		if roundTrip.MeterBalance == nil || !near(roundTrip.MeterBalance.SiteKWh, 240.0) || roundTrip.MeterBalance.Status != "BALANCED" || !near(*roundTrip.MeterBalance.ImbalanceRatio, 0.02) {
			t.Fatalf("roundTrip.MeterBalance = %+v", roundTrip.MeterBalance)
		}
		if roundTrip.ConditionAnchors == nil || !near(*roundTrip.ConditionAnchors.MeanWetBulbC, 23.4) || !near(*roundTrip.ConditionAnchors.AIClosedLoopShare, 0.78) {
			t.Fatalf("roundTrip.ConditionAnchors = %+v", roundTrip.ConditionAnchors)
		}
	})

	t.Run("omits optional P2 blocks when nil for backward compatibility", func(t *testing.T) {
		snapshot, err := Build("2026-09", shanghai, MonthEnergy{Hours: day(first, 10, 40, always)}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		text := string(raw)
		for _, field := range []string{"layeredEfficiency", "meterBalance", "conditionAnchors"} {
			if strings.Contains(text, field) {
				t.Fatalf("omitted field %q present in JSON: %s", field, text)
			}
		}
	})

	t.Run("sanitizes NaN and Inf values across P2 blocks while preserving negative temperatures and diffs", func(t *testing.T) {
		snapshot, err := Build("2026-09", shanghai, MonthEnergy{
			Hours: day(first, 10, 40, always),
			LayeredEfficiency: &LayeredEfficiencySnapshot{
				PlantCOP:        reportFloatPtr(math.NaN()),
				ChillerCOP:      reportFloatPtr(5.4),
				ChilledPumpWTF:  reportFloatPtr(math.Inf(1)),
				CoolingPumpWTF:  reportFloatPtr(math.Inf(-1)),
				TowerPowerShare: nil,
			},
			MeterBalance: &MeterBalanceSnapshot{
				SiteKWh:        240.0,
				SubmeterSumKWh: 245.0,
				DiffKWh:        math.NaN(),
				ImbalanceRatio: reportFloatPtr(math.Inf(1)),
				Status:         " EXCESS_SUBMETER ",
			},
			ConditionAnchors: &ConditionAnchorsSnapshot{
				MeanWetBulbC:      reportFloatPtr(-2.5),
				MeanDryBulbC:      reportFloatPtr(-1.0),
				MeanLoadRatio:     reportFloatPtr(math.NaN()),
				AIClosedLoopShare: reportFloatPtr(math.Inf(-1)),
			},
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.LayeredEfficiency == nil || snapshot.LayeredEfficiency.PlantCOP != nil ||
			snapshot.LayeredEfficiency.ChillerCOP == nil || !near(*snapshot.LayeredEfficiency.ChillerCOP, 5.4) ||
			snapshot.LayeredEfficiency.ChilledPumpWTF != nil || snapshot.LayeredEfficiency.CoolingPumpWTF != nil {
			t.Fatalf("sanitized LayeredEfficiency = %+v", snapshot.LayeredEfficiency)
		}
		if snapshot.MeterBalance == nil || !near(snapshot.MeterBalance.DiffKWh, -5.0) ||
			snapshot.MeterBalance.ImbalanceRatio != nil || snapshot.MeterBalance.Status != "EXCESS_SUBMETER" {
			t.Fatalf("sanitized MeterBalance = %+v", snapshot.MeterBalance)
		}
		if snapshot.ConditionAnchors == nil || !near(*snapshot.ConditionAnchors.MeanWetBulbC, -2.5) ||
			!near(*snapshot.ConditionAnchors.MeanDryBulbC, -1.0) ||
			snapshot.ConditionAnchors.MeanLoadRatio != nil || snapshot.ConditionAnchors.AIClosedLoopShare != nil {
			t.Fatalf("sanitized ConditionAnchors = %+v", snapshot.ConditionAnchors)
		}
		if _, err := json.Marshal(snapshot); err != nil {
			t.Fatalf("json.Marshal() failed after sanitization: %v", err)
		}

		// Non-finite SiteKWh/SubmeterSumKWh drops MeterBalance, and all-NaN ConditionAnchors drops to nil.
		dropped, err := Build("2026-09", shanghai, MonthEnergy{
			Hours: day(first, 10, 40, always),
			MeterBalance: &MeterBalanceSnapshot{
				SiteKWh:        math.NaN(),
				SubmeterSumKWh: 100,
				DiffKWh:        math.Inf(1),
				Status:         "UNKNOWN",
			},
			ConditionAnchors: &ConditionAnchorsSnapshot{
				MeanWetBulbC: reportFloatPtr(math.NaN()),
			},
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if dropped.MeterBalance != nil || dropped.ConditionAnchors != nil {
			t.Fatalf("expected invalid MeterBalance and all-NaN ConditionAnchors to drop to nil, got mb=%+v ca=%+v", dropped.MeterBalance, dropped.ConditionAnchors)
		}
		if _, err := json.Marshal(dropped); err != nil {
			t.Fatalf("json.Marshal() failed on dropped blocks: %v", err)
		}
	})
}
