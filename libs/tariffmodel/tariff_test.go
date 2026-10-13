package tariffmodel

import (
	"testing"
	"time"
)

func TestRatesFollowTheTariffClockAndDayType(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	periods := []Period{
		{DayType: "WEEKDAY", StartMinute: 0, EndMinute: 480, EnergyRatePerKWh: 0.32},
		{DayType: "WEEKDAY", StartMinute: 480, EndMinute: 1440, EnergyRatePerKWh: 1.2},
		{DayType: "WEEKEND", StartMinute: 0, EndMinute: 1440, EnergyRatePerKWh: 0.5},
	}
	rate := Rates(periods, shanghai)
	monday := time.Date(2026, 10, 12, 7, 30, 0, 0, shanghai)
	if got, ok := rate(monday); !ok || got != 0.32 {
		t.Fatalf("07:30 Monday = %v %v", got, ok)
	}
	if got, ok := rate(monday.Add(time.Hour)); !ok || got != 1.2 {
		t.Fatalf("08:30 Monday = %v %v", got, ok)
	}
	if got, ok := rate(monday.AddDate(0, 0, -1)); !ok || got != 0.5 {
		t.Fatalf("Sunday = %v %v", got, ok)
	}
	if _, ok := Rates(periods[:1], shanghai)(monday.Add(2 * time.Hour)); ok {
		t.Fatal("an hour no period covers must not be priced")
	}
}
