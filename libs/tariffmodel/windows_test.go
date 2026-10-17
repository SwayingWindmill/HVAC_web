package tariffmodel

import (
	"reflect"
	"testing"
	"time"
)

func TestWindowsLayAVersionsDayOutInUTC(t *testing.T) {
	shanghai := mustLocation(t, "Asia/Shanghai")
	version := Version{
		ID: "v1", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai), Location: shanghai,
		Periods: []Period{
			{Code: "VALLEY", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.3},
			{Code: "FLAT", DayType: "WEEKDAY", StartMinute: 480, EndMinute: 690, EnergyRatePerKWh: 0.7},
			{Code: "PEAK", DayType: "WEEKDAY", StartMinute: 690, EndMinute: 1440, EnergyRatePerKWh: 1.2},
		},
	}
	monday := time.Date(2026, 10, 12, 0, 0, 0, 0, shanghai)

	got := Windows([]Version{version}, monday, monday.AddDate(0, 0, 1))

	want := []Window{
		{Start: utc("2026-10-11T16:00:00Z"), End: utc("2026-10-12T00:00:00Z"), PeriodCode: "VALLEY", EnergyRatePerKWh: 0.3, VersionID: "v1"},
		{Start: utc("2026-10-12T00:00:00Z"), End: utc("2026-10-12T03:30:00Z"), PeriodCode: "FLAT", EnergyRatePerKWh: 0.7, VersionID: "v1"},
		{Start: utc("2026-10-12T03:30:00Z"), End: utc("2026-10-12T16:00:00Z"), PeriodCode: "PEAK", EnergyRatePerKWh: 1.2, VersionID: "v1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windows = %#v\nwant %#v", got, want)
	}
}

func TestWindowsMergeAcrossMidnightAndSwitchVersionsWhereTheyTakeEffect(t *testing.T) {
	shanghai := mustLocation(t, "Asia/Shanghai")
	switchAt := time.Date(2026, 10, 12, 12, 0, 0, 0, shanghai)
	weekend := Period{Code: "FLAT", DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.6}
	first := Version{ID: "v1", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai), EffectiveTo: &switchAt, Location: shanghai, Periods: []Period{
		weekend, {Code: "FLAT", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.6},
	}}
	second := Version{ID: "v2", EffectiveFrom: switchAt, Location: shanghai, Periods: []Period{
		{Code: "FLAT", DayType: "WEEKDAY", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.8},
	}}
	saturday := time.Date(2026, 10, 10, 0, 0, 0, 0, shanghai)

	got := Windows([]Version{first, second}, saturday, saturday.AddDate(0, 0, 3))

	want := []Window{
		{Start: utc("2026-10-09T16:00:00Z"), End: utc("2026-10-12T04:00:00Z"), PeriodCode: "FLAT", EnergyRatePerKWh: 0.6, VersionID: "v1"},
		{Start: utc("2026-10-12T04:00:00Z"), End: utc("2026-10-12T16:00:00Z"), PeriodCode: "FLAT", EnergyRatePerKWh: 0.8, VersionID: "v2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windows = %#v\nwant %#v", got, want)
	}
}

func TestWindowsFollowTheTariffClockAcrossADaylightSavingChange(t *testing.T) {
	berlin := mustLocation(t, "Europe/Berlin")
	version := Version{ID: "v1", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, berlin), Location: berlin, Periods: []Period{
		{Code: "VALLEY", DayType: "WEEKEND", StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.2},
		{Code: "PEAK", DayType: "WEEKEND", StartMinute: 480, EndMinute: 1440, EnergyRatePerKWh: 0.9},
	}}
	// Clocks went back an hour at 03:00 on Sunday 2026-10-25: that day is 25 hours long.
	sunday := time.Date(2026, 10, 25, 0, 0, 0, 0, berlin)

	got := Windows([]Version{version}, sunday, sunday.AddDate(0, 0, 1))

	want := []Window{
		{Start: utc("2026-10-24T22:00:00Z"), End: utc("2026-10-25T07:00:00Z"), PeriodCode: "VALLEY", EnergyRatePerKWh: 0.2, VersionID: "v1"},
		{Start: utc("2026-10-25T07:00:00Z"), End: utc("2026-10-25T23:00:00Z"), PeriodCode: "PEAK", EnergyRatePerKWh: 0.9, VersionID: "v1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windows = %#v\nwant %#v", got, want)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func utc(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
