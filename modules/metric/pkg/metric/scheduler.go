package metric

import (
	"errors"
	"strings"
	"time"
	_ "time/tzdata"
)

type Schedule struct {
	Granularity string
	Timezone    string
}

type Period struct {
	Start time.Time
	End   time.Time
}

func CompletedPeriod(schedule Schedule, now time.Time, finalizationDelay time.Duration) (Period, error) {
	if now.IsZero() {
		return Period{}, errors.New("metric schedule time is required")
	}
	if finalizationDelay < 0 || finalizationDelay > 24*time.Hour {
		return Period{}, errors.New("metric finalization delay is invalid")
	}
	location, err := time.LoadLocation(strings.TrimSpace(schedule.Timezone))
	if err != nil {
		return Period{}, errors.New("metric Site timezone is invalid")
	}
	local := now.UTC().Add(-finalizationDelay).In(location)
	granularity := strings.ToUpper(strings.TrimSpace(schedule.Granularity))
	var end, start time.Time
	switch granularity {
	case "1MIN":
		end = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, location)
		start = end.Add(-time.Minute)
	case "5MIN":
		minute := local.Minute() - local.Minute()%5
		end = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), minute, 0, 0, location)
		start = end.Add(-5 * time.Minute)
	case "15MIN":
		minute := local.Minute() - local.Minute()%15
		end = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), minute, 0, 0, location)
		start = end.Add(-15 * time.Minute)
	case "HOUR":
		end = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, location)
		start = end.Add(-time.Hour)
	case "DAY":
		end = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		start = end.AddDate(0, 0, -1)
	case "MONTH":
		end = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
		start = end.AddDate(0, -1, 0)
	case "QUARTER":
		quarterMonth := time.Month(((int(local.Month())-1)/3)*3 + 1)
		end = time.Date(local.Year(), quarterMonth, 1, 0, 0, 0, 0, location)
		start = end.AddDate(0, -3, 0)
	case "YEAR":
		end = time.Date(local.Year(), time.January, 1, 0, 0, 0, 0, location)
		start = end.AddDate(-1, 0, 0)
	case "REALTIME":
		return Period{}, errors.New("REALTIME Metric is not scheduled by the Phase1 metric-worker")
	default:
		return Period{}, errors.New("metric time granularity is unsupported for scheduled execution")
	}
	if !end.After(start) {
		return Period{}, errors.New("metric completed period is invalid")
	}
	return Period{Start: start.UTC(), End: end.UTC()}, nil
}
