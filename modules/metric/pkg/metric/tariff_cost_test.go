package metric

import (
	"context"
	"strings"
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

// A Monday in Asia/Shanghai: 00:00–08:00 valley at 0.32, 08:00–24:00 peak at 1.2.
var monday = time.Date(2026, 10, 12, 0, 0, 0, 0, shanghai)

func valleyPeak(versionID string, from time.Time, to *time.Time, peak float64) ImportTariff {
	return ImportTariff{VersionID: versionID, EffectiveFrom: from, EffectiveTo: to, Location: shanghai, Periods: []tariffmodel.Period{
		{DayType: "WEEKDAY", StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.32},
		{DayType: "WEEKDAY", StartMinute: 480, EndMinute: 1440, EnergyRatePerKWh: peak},
	}}
}

func hoursOf(start time.Time, energy float64, count int) []HourlyResult {
	hours := make([]HourlyResult, 0, count)
	for hour := 0; hour < count; hour++ {
		hours = append(hours, HourlyResult{PeriodStart: start.Add(time.Duration(hour) * time.Hour), Value: energy, Quality: "GOOD"})
	}
	return hours
}

func dailyCostEngine(t *testing.T, hours []HourlyResult, tariffs []ImportTariff) (*Engine, *fakeRegistry, RunRequest) {
	t.Helper()
	registry := &fakeRegistry{binding: Binding{
		TenantID: "tenant", SiteID: "site", BindingID: "cost", MetricVersionID: "cost-v2", MetricID: "energy-cost",
		MetricCode: "energy_cost", MetricVersion: 2, BindingVersion: 1, SubjectType: "SITE", SubjectID: "site",
		Granularity: "DAY", DataType: "NUMBER", Unit: "CNY", CalculationMethod: "TARIFF_ENERGY_COST", QualityPolicy: "STRICT",
		Dependencies: []Dependency{{Type: "METRIC", Code: "hourly_energy", MetricID: "hourly-energy", Required: true}},
	}, hourly: hours, tariffs: tariffs}
	engine, err := New(registry, &fakeSeries{}, &fakeLatest{})
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return monday.Add(25 * time.Hour) }
	return engine, registry, RunRequest{TenantID: "tenant", SiteID: "site", BindingID: "cost", PeriodStart: monday, PeriodEnd: monday.Add(24 * time.Hour)}
}

func TestDailyEnergyCostPricesEachHourAtItsTimeOfUseRate(t *testing.T) {
	engine, _, request := dailyCostEngine(t, hoursOf(monday, 10, 24), []ImportTariff{valleyPeak("tou-v1", monday.AddDate(0, -1, 0), nil, 1.2)})
	result, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	// 8 valley hours × 10 kWh × 0.32 + 16 peak hours × 10 kWh × 1.2
	if want := 25.6 + 192.0; result.Value < want-1e-9 || result.Value > want+1e-9 {
		t.Fatalf("daily cost = %v, want %v", result.Value, want)
	}
	if len(result.Inputs) != 1 || !strings.Contains(result.Inputs[0].Reference, "tou-v1") || result.Inputs[0].Count != 24 {
		t.Fatalf("inputs = %#v", result.Inputs)
	}
}

func TestDailyEnergyCostIsNotPublishedWithAnHourMissing(t *testing.T) {
	engine, registry, request := dailyCostEngine(t, hoursOf(monday, 10, 23), []ImportTariff{valleyPeak("tou-v1", monday.AddDate(0, -1, 0), nil, 1.2)})
	if _, err := engine.Execute(context.Background(), request); err == nil {
		t.Fatal("a day with an hour missing was priced")
	}
	if len(registry.completed) != 0 {
		t.Fatalf("published = %#v", registry.completed)
	}
}

func TestDailyEnergyCostIsNotPublishedForAnHourNoTariffPrices(t *testing.T) {
	noon := monday.Add(12 * time.Hour)
	engine, _, request := dailyCostEngine(t, hoursOf(monday, 10, 24), []ImportTariff{valleyPeak("tou-v1", noon, nil, 1.2)})
	if _, err := engine.Execute(context.Background(), request); err == nil {
		t.Fatal("hours before the Tariff took effect were priced")
	}
}

func TestDailyEnergyCostPricesEachHourAtTheTariffVersionThenInEffect(t *testing.T) {
	noon := monday.Add(12 * time.Hour)
	engine, _, request := dailyCostEngine(t, hoursOf(monday, 10, 24), []ImportTariff{
		valleyPeak("tou-v1", monday.AddDate(0, -1, 0), &noon, 1.2),
		valleyPeak("tou-v2", noon, nil, 1.5),
	})
	result, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	// 8 valley hours at 0.32, 4 peak hours at 1.2 before noon, 12 peak hours at 1.5 after.
	if want := 25.6 + 48.0 + 180.0; result.Value < want-1e-9 || result.Value > want+1e-9 {
		t.Fatalf("daily cost = %v, want %v", result.Value, want)
	}
	if reference := result.Inputs[0].Reference; !strings.Contains(reference, "tou-v1") || !strings.Contains(reference, "tou-v2") {
		t.Fatalf("reference = %q", reference)
	}
}
