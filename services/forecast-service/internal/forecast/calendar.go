package forecast

import (
	"strings"
	"time"
)

const defaultSiteTimezone = "Asia/Shanghai"

type dayKind int

const (
	dayWorkday dayKind = iota
	dayWeekend
	dayHoliday
)

// China State Council statutory holidays and makeup workdays (2025-2026), keyed by local YYYY-MM-DD.
var cnStatutoryHolidays = map[string]struct{}{
	// 2025
	"2025-01-01": {},
	"2025-01-28": {}, "2025-01-29": {}, "2025-01-30": {}, "2025-01-31": {},
	"2025-02-01": {}, "2025-02-02": {}, "2025-02-03": {}, "2025-02-04": {},
	"2025-04-04": {}, "2025-04-05": {}, "2025-04-06": {},
	"2025-05-01": {}, "2025-05-02": {}, "2025-05-03": {}, "2025-05-04": {}, "2025-05-05": {},
	"2025-05-31": {}, "2025-06-01": {}, "2025-06-02": {},
	"2025-10-01": {}, "2025-10-02": {}, "2025-10-03": {}, "2025-10-04": {},
	"2025-10-05": {}, "2025-10-06": {}, "2025-10-07": {}, "2025-10-08": {},
	// 2026
	"2026-01-01": {}, "2026-01-02": {}, "2026-01-03": {},
	"2026-02-15": {}, "2026-02-16": {}, "2026-02-17": {}, "2026-02-18": {},
	"2026-02-19": {}, "2026-02-20": {}, "2026-02-21": {}, "2026-02-22": {}, "2026-02-23": {},
	"2026-04-04": {}, "2026-04-05": {}, "2026-04-06": {},
	"2026-05-01": {}, "2026-05-02": {}, "2026-05-03": {}, "2026-05-04": {}, "2026-05-05": {},
	"2026-06-19": {}, "2026-06-20": {}, "2026-06-21": {},
	"2026-09-25": {}, "2026-09-26": {}, "2026-09-27": {},
	"2026-10-01": {}, "2026-10-02": {}, "2026-10-03": {}, "2026-10-04": {},
	"2026-10-05": {}, "2026-10-06": {}, "2026-10-07": {},
}

var cnMakeupWorkdays = map[string]struct{}{
	// 2025
	"2025-01-26": {}, "2025-02-08": {}, "2025-04-27": {}, "2025-09-28": {}, "2025-10-11": {},
	// 2026
	"2026-01-04": {}, "2026-02-14": {}, "2026-02-28": {}, "2026-05-09": {}, "2026-09-20": {}, "2026-10-10": {},
}

func resolveSiteLocation(timezone string) *time.Location {
	tz := strings.TrimSpace(timezone)
	if tz == "" {
		tz = defaultSiteTimezone
	}
	if loc, err := time.LoadLocation(tz); err == nil && loc != nil {
		return loc
	}
	if tz == "Asia/Shanghai" {
		return time.FixedZone("CST", 8*3600)
	}
	return time.UTC
}

func classifyDay(at time.Time, loc *time.Location) dayKind {
	local := at.In(loc)
	dateKey := local.Format("2006-01-02")
	if _, ok := cnStatutoryHolidays[dateKey]; ok {
		return dayHoliday
	}
	if _, ok := cnMakeupWorkdays[dateKey]; ok {
		return dayWorkday
	}
	// Fixed solar holiday anchors for years outside explicit State Council lookup.
	if year := local.Year(); year < 2025 || year > 2026 {
		monthDay := local.Format("01-02")
		if monthDay == "01-01" || monthDay == "05-01" || (local.Month() == time.October && local.Day() <= 7) {
			return dayHoliday
		}
	}
	if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return dayWeekend
	}
	return dayWorkday
}

func localSlotIndex(at time.Time, loc *time.Location, granularityMinutes int) int {
	local := at.In(loc)
	minuteOfDay := local.Hour()*60 + local.Minute()
	slotsPerDay := 1440 / granularityMinutes
	slot := minuteOfDay / granularityMinutes
	if slot < 0 {
		return 0
	}
	if slot >= slotsPerDay {
		return slotsPerDay - 1
	}
	return slot
}
