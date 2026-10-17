// Package tariffmodel lays a Tariff Version's time-of-use periods out on its own clock.
package tariffmodel

import (
	"time"
	_ "time/tzdata" // Tariff clocks load IANA zones in distroless images too.
)

// Period is the part of a Tariff Version that prices energy by local time of day.
type Period struct {
	// Code names the period on the Tariff, such as PEAK, FLAT or VALLEY.
	Code             string
	DayType          string
	StartMinute      int
	EndMinute        int
	EnergyRatePerKWh float64
}

// dayType is the day type a local day prices as. Holidays price as their weekday until Tariffs
// carry a calendar, as Settlement does.
func dayType(local time.Time) string {
	if weekday := local.Weekday(); weekday == time.Saturday || weekday == time.Sunday {
		return "WEEKEND"
	}
	return "WEEKDAY"
}
