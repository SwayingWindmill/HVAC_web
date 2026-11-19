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

func TestWindowsSupportFiveTierTOUIncludingDeepValley(t *testing.T) {
	shanghai := mustLocation(t, "Asia/Shanghai")
	version := Version{
		ID: "v-5tier", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai), Location: shanghai,
		Periods: []Period{
			{Code: PeriodDeepValley, DayType: DayTypeWeekday, StartMinute: 0, EndMinute: 240, EnergyRatePerKWh: 0.15},
			{Code: PeriodValley, DayType: DayTypeWeekday, StartMinute: 240, EndMinute: 480, EnergyRatePerKWh: 0.30},
			{Code: PeriodFlat, DayType: DayTypeWeekday, StartMinute: 480, EndMinute: 660, EnergyRatePerKWh: 0.70},
			{Code: PeriodPeak, DayType: DayTypeWeekday, StartMinute: 660, EndMinute: 840, EnergyRatePerKWh: 1.10},
			{Code: PeriodSuperPeak, DayType: DayTypeWeekday, StartMinute: 840, EndMinute: 960, EnergyRatePerKWh: 1.45},
			{Code: PeriodFlat, DayType: DayTypeWeekday, StartMinute: 960, EndMinute: 1320, EnergyRatePerKWh: 0.70},
			{Code: PeriodDeepValley, DayType: DayTypeWeekday, StartMinute: 1320, EndMinute: 1440, EnergyRatePerKWh: 0.15},
		},
	}
	// Monday 2026-10-12 and Tuesday 2026-10-13: 22:00..24:00 + 00:00..04:00 DEEP_VALLEY merges across midnight.
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, shanghai)
	got := Windows([]Version{version}, start, start.AddDate(0, 0, 2))

	if len(got) != 13 {
		t.Fatalf("expected 13 merged 5-tier windows across 2 days, got %d: %#v", len(got), got)
	}
	if got[0].PeriodCode != PeriodDeepValley || got[0].EnergyRatePerKWh != 0.15 {
		t.Fatalf("first window = %#v, want DEEP_VALLEY @ 0.15", got[0])
	}
	if got[4].PeriodCode != PeriodSuperPeak || got[4].EnergyRatePerKWh != 1.45 {
		t.Fatalf("fifth window = %#v, want SUPER_PEAK @ 1.45", got[4])
	}
	// Window 6 spans 2026-10-12 22:00+08 to 2026-10-13 04:00+08 across midnight.
	if !got[6].Start.Equal(utc("2026-10-12T14:00:00Z")) || !got[6].End.Equal(utc("2026-10-12T20:00:00Z")) || got[6].PeriodCode != PeriodDeepValley {
		t.Fatalf("midnight-merged DEEP_VALLEY window = %#v", got[6])
	}
}

func TestWindowsResolveHolidayDatesSpecialDaysAndFallbacks(t *testing.T) {
	shanghai := mustLocation(t, "Asia/Shanghai")
	// 2026-10-01 Thursday -> HOLIDAY (uses explicit HOLIDAY periods)
	// 2026-10-02 Friday   -> SPECIAL_DAY (uses explicit SPECIAL_DAY periods)
	// 2026-10-03 Saturday -> WEEKDAY (compensated workday override)
	withExplicitHoliday := Version{
		ID: "v-explicit", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai), Location: shanghai,
		HolidayDates: map[string]string{
			"2026-10-01": DayTypeHoliday,
			"2026-10-02": DayTypeSpecialDay,
			"2026-10-03": DayTypeWeekday,
		},
		Periods: []Period{
			{Code: PeriodFlat, DayType: DayTypeWeekday, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.70},
			{Code: PeriodValley, DayType: DayTypeWeekend, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.30},
			{Code: PeriodDeepValley, DayType: DayTypeHoliday, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.12},
			{Code: PeriodSuperPeak, DayType: DayTypeSpecialDay, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 1.50},
		},
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)
	got := Windows([]Version{withExplicitHoliday}, from, from.AddDate(0, 0, 3))
	want := []Window{
		{Start: utc("2026-09-30T16:00:00Z"), End: utc("2026-10-01T16:00:00Z"), PeriodCode: PeriodDeepValley, EnergyRatePerKWh: 0.12, VersionID: "v-explicit"},
		{Start: utc("2026-10-01T16:00:00Z"), End: utc("2026-10-02T16:00:00Z"), PeriodCode: PeriodSuperPeak, EnergyRatePerKWh: 1.50, VersionID: "v-explicit"},
		{Start: utc("2026-10-02T16:00:00Z"), End: utc("2026-10-03T16:00:00Z"), PeriodCode: PeriodFlat, EnergyRatePerKWh: 0.70, VersionID: "v-explicit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit holiday windows = %#v\nwant %#v", got, want)
	}

	// Fallback: version defines WEEKDAY + WEEKEND only -> HOLIDAY and SPECIAL_DAY both fall back to WEEKEND.
	weekendFallback := Version{
		ID: "v-wknd-fallback", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai), Location: shanghai,
		HolidayDates: map[string]string{
			"2026-10-01": DayTypeHoliday,
			"2026-10-02": DayTypeSpecialDay,
		},
		Periods: []Period{
			{Code: PeriodPeak, DayType: DayTypeWeekday, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 1.00},
			{Code: PeriodValley, DayType: DayTypeWeekend, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.35},
		},
	}
	gotFallback := Windows([]Version{weekendFallback}, from, from.AddDate(0, 0, 2))
	wantFallback := []Window{
		{Start: utc("2026-09-30T16:00:00Z"), End: utc("2026-10-02T16:00:00Z"), PeriodCode: PeriodValley, EnergyRatePerKWh: 0.35, VersionID: "v-wknd-fallback"},
	}
	if !reflect.DeepEqual(gotFallback, wantFallback) {
		t.Fatalf("weekend fallback windows = %#v\nwant %#v", gotFallback, wantFallback)
	}

	// Fallback: version defines WEEKDAY only -> HOLIDAY falls back to WEEKDAY when WEEKEND is also absent.
	weekdayOnlyFallback := Version{
		ID: "v-wkdy-fallback", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, shanghai), Location: shanghai,
		HolidayDates: map[string]string{
			"2026-10-01": DayTypeHoliday,
		},
		Periods: []Period{
			{Code: PeriodFlat, DayType: DayTypeWeekday, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.65},
		},
	}
	gotWeekdayFallback := Windows([]Version{weekdayOnlyFallback}, from, from.AddDate(0, 0, 1))
	wantWeekdayFallback := []Window{
		{Start: utc("2026-09-30T16:00:00Z"), End: utc("2026-10-01T16:00:00Z"), PeriodCode: PeriodFlat, EnergyRatePerKWh: 0.65, VersionID: "v-wkdy-fallback"},
	}
	if !reflect.DeepEqual(gotWeekdayFallback, wantWeekdayFallback) {
		t.Fatalf("weekday fallback windows = %#v\nwant %#v", gotWeekdayFallback, wantWeekdayFallback)
	}
}

func TestWindowsHolidayCalendarAcrossIANADSTTransition(t *testing.T) {
	ny := mustLocation(t, "America/New_York")
	// Sunday 2026-11-01 is DST fall-back in America/New_York (02:00 EDT -> 01:00 EST, 25h day).
	// Mark Monday 2026-11-02 as HOLIDAY and verify EST offset (-05:00) applies cleanly.
	version := Version{
		ID: "v-ny", EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, ny), Location: ny,
		HolidayDates: map[string]string{
			"2026-11-02": DayTypeHoliday,
		},
		Periods: []Period{
			{Code: PeriodValley, DayType: DayTypeWeekend, StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.2},
			{Code: PeriodPeak, DayType: DayTypeWeekend, StartMinute: 480, EndMinute: 1440, EnergyRatePerKWh: 0.8},
			{Code: PeriodDeepValley, DayType: DayTypeHoliday, StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.1},
		},
	}
	sunday := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	got := Windows([]Version{version}, sunday, sunday.AddDate(0, 0, 2))
	want := []Window{
		{Start: utc("2026-11-01T04:00:00Z"), End: utc("2026-11-01T13:00:00Z"), PeriodCode: PeriodValley, EnergyRatePerKWh: 0.2, VersionID: "v-ny"},
		{Start: utc("2026-11-01T13:00:00Z"), End: utc("2026-11-02T05:00:00Z"), PeriodCode: PeriodPeak, EnergyRatePerKWh: 0.8, VersionID: "v-ny"},
		{Start: utc("2026-11-02T05:00:00Z"), End: utc("2026-11-03T05:00:00Z"), PeriodCode: PeriodDeepValley, EnergyRatePerKWh: 0.1, VersionID: "v-ny"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NY DST+holiday windows = %#v\nwant %#v", got, want)
	}
}

func TestFixedPointTariffPricingHelpers(t *testing.T) {
	p := Period{Code: PeriodDeepValley, DayType: DayTypeWeekday, StartMinute: 0, EndMinute: 240, EnergyRatePerKWh: 0.1}
	rate, err := p.FixedEnergyRate()
	if err != nil || rate.String() != "0.1" {
		t.Fatalf("FixedEnergyRate = (%s, %v), want 0.1", rate, err)
	}
	w := Window{PeriodCode: PeriodDeepValley, EnergyRatePerKWh: 0.2}
	wRate, err := w.FixedEnergyRate()
	if err != nil || wRate.String() != "0.2" {
		t.Fatalf("Window.FixedEnergyRate = (%s, %v), want 0.2", wRate, err)
	}
	c1, err := PriceInterval(0.1, 1.0)
	if err != nil {
		t.Fatalf("PriceInterval c1 err = %v", err)
	}
	c2, err := w.PriceEnergy(1.0)
	if err != nil {
		t.Fatalf("PriceEnergy c2 err = %v", err)
	}
	total, err := c1.Add(c2)
	if err != nil || total.String() != "0.3" || total.Cents() != 30 {
		t.Fatalf("0.1 + 0.2 pricing = (%s, cents=%d, err=%v), want 0.3 / 30", total, total.Cents(), err)
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
