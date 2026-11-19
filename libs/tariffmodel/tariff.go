// Package tariffmodel lays a Tariff Version's time-of-use periods out on its own clock.
package tariffmodel

import (
	"strings"
	"time"
	_ "time/tzdata" // Tariff clocks load IANA zones in distroless images too.

	"github.com/quanlaihe/hvac-web/libs/fixedpoint"
)

const (
	PeriodSuperPeak  = "SUPER_PEAK"
	PeriodPeak       = "PEAK"
	PeriodFlat       = "FLAT"
	PeriodValley     = "VALLEY"
	PeriodDeepValley = "DEEP_VALLEY"

	DayTypeWeekday    = "WEEKDAY"
	DayTypeWeekend    = "WEEKEND"
	DayTypeHoliday    = "HOLIDAY"
	DayTypeSpecialDay = "SPECIAL_DAY"
)

// Period is the part of a Tariff Version that prices energy by local time of day.
type Period struct {
	// Code names the period on the Tariff, such as SUPER_PEAK, PEAK, FLAT, VALLEY or DEEP_VALLEY.
	Code             string
	DayType          string
	StartMinute      int
	EndMinute        int
	EnergyRatePerKWh float64
}

// FixedEnergyRate converts EnergyRatePerKWh to fixedpoint.Decimal6.
func (p Period) FixedEnergyRate() (fixedpoint.Decimal6, error) {
	return fixedpoint.FromFloat64(p.EnergyRatePerKWh)
}

// PriceInterval multiplies ratePerKWh by energyKWh using fixedpoint.Decimal6 to avoid float drift.
func PriceInterval(ratePerKWh, energyKWh float64) (fixedpoint.Decimal6, error) {
	rate, err := fixedpoint.FromFloat64(ratePerKWh)
	if err != nil {
		return 0, err
	}
	energy, err := fixedpoint.FromFloat64(energyKWh)
	if err != nil {
		return 0, err
	}
	return rate.Mul(energy)
}

// ResolveDayType determines the effective Tariff DayType for local using holidayDates (keyed by
// local date "2006-01-02" -> "HOLIDAY", "SPECIAL_DAY", "WEEKDAY", or "WEEKEND"). If HOLIDAY or
// SPECIAL_DAY is selected for a day without periods defined for that DayType, it falls back to
// "WEEKEND" when defined and otherwise "WEEKDAY".
func ResolveDayType(local time.Time, holidayDates map[string]string, hasDayType func(string) bool) string {
	kind := dayType(local)
	if len(holidayDates) > 0 {
		if override := strings.ToUpper(strings.TrimSpace(holidayDates[local.Format("2006-01-02")])); override != "" {
			kind = override
		}
	}
	if kind == DayTypeHoliday || kind == DayTypeSpecialDay {
		if hasDayType != nil && hasDayType(kind) {
			return kind
		}
		if hasDayType != nil && hasDayType(DayTypeWeekend) {
			return DayTypeWeekend
		}
		return DayTypeWeekday
	}
	return kind
}

// dayType is the baseline weekday/weekend day type a local day prices as.
func dayType(local time.Time) string {
	if weekday := local.Weekday(); weekday == time.Saturday || weekday == time.Sunday {
		return DayTypeWeekend
	}
	return DayTypeWeekday
}
