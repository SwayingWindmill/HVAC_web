package tariffmodel

import (
	"sort"
	"time"
)

// Version is an import Tariff Version over the time it prices a Site: the part where both the
// version and its assignment are in effect. EffectiveTo is nil while it has no successor.
type Version struct {
	ID            string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Location      *time.Location
	Periods       []Period
}

// Window is one stretch of time, in UTC, priced at one energy rate.
type Window struct {
	Start            time.Time
	End              time.Time
	PeriodCode       string
	EnergyRatePerKWh float64
	VersionID        string
}

// Windows lays the versions' periods out over [from, to) on each version's own clock and day
// types, in time order. Adjacent stretches of the same period and rate are one window; time no
// period covers has no window.
func Windows(versions []Version, from, to time.Time) []Window {
	var windows []Window
	for _, version := range versions {
		start, end := latest(from, version.EffectiveFrom), to
		if version.EffectiveTo != nil && version.EffectiveTo.Before(end) {
			end = *version.EffectiveTo
		}
		if !start.Before(end) {
			continue
		}
		periods := append([]Period(nil), version.Periods...)
		sort.Slice(periods, func(left, right int) bool { return periods[left].StartMinute < periods[right].StartMinute })
		local := start.In(version.Location)
		for day := midnight(local.Year(), local.Month(), local.Day(), version.Location); day.Before(end); day = midnight(day.Year(), day.Month(), day.Day()+1, version.Location) {
			kind := dayType(day)
			for _, period := range periods {
				if period.DayType != kind {
					continue
				}
				window := Window{
					Start: latest(start, minuteOf(day, period.StartMinute)), End: earliest(end, minuteOf(day, period.EndMinute)),
					PeriodCode: period.Code, EnergyRatePerKWh: period.EnergyRatePerKWh, VersionID: version.ID,
				}
				if !window.Start.Before(window.End) {
					continue
				}
				window.Start, window.End = window.Start.UTC(), window.End.UTC()
				if last := len(windows) - 1; last >= 0 && windows[last].End.Equal(window.Start) && windows[last].PeriodCode == window.PeriodCode &&
					windows[last].EnergyRatePerKWh == window.EnergyRatePerKWh && windows[last].VersionID == window.VersionID {
					windows[last].End = window.End
					continue
				}
				windows = append(windows, window)
			}
		}
	}
	return windows
}

func midnight(year int, month time.Month, day int, location *time.Location) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, location)
}

func minuteOf(day time.Time, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, minute, 0, 0, day.Location())
}

func latest(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func earliest(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
