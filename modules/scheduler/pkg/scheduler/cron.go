package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type cronSpec struct {
	minute, hour, dayOfMonth, month, dayOfWeek fieldSet
	domWildcard, dowWildcard                  bool
}

type fieldSet map[int]struct{}

func parseCron5(expression string) (cronSpec, error) {
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return cronSpec{}, errors.New("5-field cron expression must contain minute hour day-of-month month day-of-week")
	}
	minute, _, err := parseField(parts[0], 0, 59, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("cron minute: %w", err)
	}
	hour, _, err := parseField(parts[1], 0, 23, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("cron hour: %w", err)
	}
	dayOfMonth, domWildcard, err := parseField(parts[2], 1, 31, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("cron day-of-month: %w", err)
	}
	month, _, err := parseField(parts[3], 1, 12, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("cron month: %w", err)
	}
	dayOfWeek, dowWildcard, err := parseField(parts[4], 0, 7, true)
	if err != nil {
		return cronSpec{}, fmt.Errorf("cron day-of-week: %w", err)
	}
	return cronSpec{
		minute: minute, hour: hour, dayOfMonth: dayOfMonth, month: month, dayOfWeek: dayOfWeek,
		domWildcard: domWildcard, dowWildcard: dowWildcard,
	}, nil
}

func parseField(raw string, minValue, maxValue int, sundaySeven bool) (fieldSet, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, errors.New("field is empty")
	}
	result := fieldSet{}
	wildcard := raw == "*" || strings.HasPrefix(raw, "*/")
	for _, segment := range strings.Split(raw, ",") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, false, errors.New("empty list segment")
		}
		base, step, err := splitStep(segment)
		if err != nil {
			return nil, false, err
		}
		start, end, err := parseRange(base, minValue, maxValue)
		if err != nil {
			return nil, false, err
		}
		for value := start; value <= end; value += step {
			if sundaySeven && value == 7 {
				result[0] = struct{}{}
				continue
			}
			result[value] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil, false, errors.New("field selects no values")
	}
	return result, wildcard, nil
}

func splitStep(segment string) (string, int, error) {
	parts := strings.Split(segment, "/")
	if len(parts) > 2 {
		return "", 0, errors.New("invalid step expression")
	}
	step := 1
	if len(parts) == 2 {
		value, err := strconv.Atoi(parts[1])
		if err != nil || value <= 0 {
			return "", 0, errors.New("step must be a positive integer")
		}
		step = value
	}
	return parts[0], step, nil
}

func parseRange(raw string, minValue, maxValue int) (int, int, error) {
	if raw == "*" {
		return minValue, maxValue, nil
	}
	parts := strings.Split(raw, "-")
	if len(parts) > 2 {
		return 0, 0, errors.New("invalid range expression")
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, errors.New("field value must be an integer")
	}
	end := start
	if len(parts) == 2 {
		end, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, errors.New("range end must be an integer")
		}
	}
	if start < minValue || end > maxValue || end < start {
		return 0, 0, fmt.Errorf("field range must be within %d..%d", minValue, maxValue)
	}
	return start, end, nil
}

func (spec cronSpec) next(after time.Time, location *time.Location) (time.Time, error) {
	if location == nil {
		return time.Time{}, errors.New("cron timezone is required")
	}
	candidate := after.In(location).Truncate(time.Minute).Add(time.Minute)
	deadline := candidate.AddDate(5, 0, 0)
	for candidate.Before(deadline) {
		if spec.matches(candidate) {
			return candidate.UTC(), nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, errors.New("cron next fire was not found within five years")
}

func (spec cronSpec) matches(value time.Time) bool {
	if _, ok := spec.minute[value.Minute()]; !ok {
		return false
	}
	if _, ok := spec.hour[value.Hour()]; !ok {
		return false
	}
	if _, ok := spec.month[int(value.Month())]; !ok {
		return false
	}
	_, domMatch := spec.dayOfMonth[value.Day()]
	_, dowMatch := spec.dayOfWeek[int(value.Weekday())]
	dayMatch := false
	switch {
	case spec.domWildcard && spec.dowWildcard:
		dayMatch = true
	case spec.domWildcard:
		dayMatch = dowMatch
	case spec.dowWildcard:
		dayMatch = domMatch
	default:
		dayMatch = domMatch || dowMatch
	}
	return dayMatch
}
