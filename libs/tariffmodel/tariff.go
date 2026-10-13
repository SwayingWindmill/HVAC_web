// Package tariffmodel prices energy by a Tariff Version's time-of-use periods.
package tariffmodel

import "time"

// Period is the part of a Tariff Version that prices energy by local time of day.
type Period struct {
	// Code names the period on the Tariff, such as PEAK, FLAT or VALLEY.
	Code             string
	DayType          string
	StartMinute      int
	EndMinute        int
	EnergyRatePerKWh float64
}

// Rates prices an instant by the period covering it on the Tariff Version's clock.
func Rates(periods []Period, location *time.Location) func(time.Time) (float64, bool) {
	return func(at time.Time) (float64, bool) {
		period, ok := Covering(periods, location, at)
		return period.EnergyRatePerKWh, ok
	}
}

// Covering is the period covering an instant on the Tariff Version's clock. Holidays price as
// their weekday until Tariffs carry a calendar, as Settlement does.
func Covering(periods []Period, location *time.Location, at time.Time) (Period, bool) {
	local := at.In(location)
	dayType := "WEEKDAY"
	if weekday := local.Weekday(); weekday == time.Saturday || weekday == time.Sunday {
		dayType = "WEEKEND"
	}
	minute := local.Hour()*60 + local.Minute()
	for _, period := range periods {
		if period.DayType == dayType && period.StartMinute <= minute && minute < period.EndMinute {
			return period, true
		}
	}
	return Period{}, false
}
