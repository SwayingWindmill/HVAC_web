package planttwin

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidTimezone indicates that an operating schedule has an empty or unknown IANA timezone.
var ErrInvalidTimezone = errors.New("operating schedule timezone is invalid or empty")

// ScheduleEvaluation captures the deterministic operating state at a specific site-local instant.
type ScheduleEvaluation struct {
	LocalTime             time.Time          `json:"localTime"`
	LocalDate             string             `json:"localDate"`
	InBaseSlot            bool               `json:"inBaseSlot"`
	InPreCool             bool               `json:"inPreCool"`
	InCoastDown           bool               `json:"inCoastDown"`
	WithinOperatingWindow bool               `json:"withinOperatingWindow"`
	Suppressed            bool               `json:"suppressed"`
	ActiveSlot            *ScheduleSlot      `json:"activeSlot,omitempty"`
	ActiveException       *ScheduleException `json:"activeException,omitempty"`
	TargetSupplyTempC     *float64           `json:"targetSupplyTempC,omitempty"`
	OperatingState        string             `json:"operatingState"`
	Reason                string             `json:"reason"`
}

type candidateWindow struct {
	slot      ScheduleSlot
	exc       *ScheduleException
	baseStart time.Time
	baseEnd   time.Time
}

// EvaluateOperatingSchedule evaluates sched at instant at in sched.Timezone.
// It fails fast with ErrInvalidTimezone if Timezone is empty or unknown.
func EvaluateOperatingSchedule(at time.Time, sched OperatingSchedule) (ScheduleEvaluation, error) {
	tz := strings.TrimSpace(sched.Timezone)
	if tz == "" {
		return ScheduleEvaluation{}, fmt.Errorf("%w: timezone is empty", ErrInvalidTimezone)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return ScheduleEvaluation{}, fmt.Errorf("%w: %q: %v", ErrInvalidTimezone, tz, err)
	}

	atLocal := at.In(loc)
	day0 := time.Date(atLocal.Year(), atLocal.Month(), atLocal.Day(), 0, 0, 0, 0, loc)
	localDate := day0.Format("2006-01-02")
	lead := time.Duration(max(0, sched.PreCoolLeadMinutes)) * time.Minute
	lag := time.Duration(max(0, sched.CoastDownLagMinutes)) * time.Minute

	todayExc := findExceptionForDate(sched.Exceptions, localDate)
	if todayExc != nil {
		kind := strings.ToUpper(strings.TrimSpace(todayExc.Kind))
		if kind == ExceptionHolidayShutdown || kind == ExceptionMaintenanceWindow {
			return ScheduleEvaluation{
				LocalTime:       atLocal,
				LocalDate:       localDate,
				Suppressed:      true,
				ActiveException: todayExc,
				OperatingState:  OperatingStateExceptionClosed,
				Reason:          formatExceptionReason(todayExc),
			}, nil
		}
	}

	var candidates []candidateWindow
	appendWindow := func(day time.Time, slot ScheduleSlot, exc *ScheduleException) {
		start, end, ok := resolveSlotBounds(day, slot, loc)
		if !ok {
			return
		}
		candidates = append(candidates, candidateWindow{
			slot:      cloneScheduleSlot(slot),
			exc:       exc,
			baseStart: start,
			baseEnd:   end,
		})
	}

	hasOvertimeToday := todayExc != nil && strings.EqualFold(strings.TrimSpace(todayExc.Kind), ExceptionOvertimeCooling)
	configured := len(sched.Slots) > 0 || hasOvertimeToday

	if hasOvertimeToday {
		for _, slot := range todayExc.ReplacementSlots {
			appendWindow(day0, slot, todayExc)
		}
		// Allow next day's early pre-cool window without letting yesterday's slots bleed into today.
		tomorrow := day0.AddDate(0, 0, 1)
		forEachDaySlot(tomorrow, sched, func(slot ScheduleSlot, exc *ScheduleException) {
			start, end, ok := resolveSlotBounds(tomorrow, slot, loc)
			if !ok || lead == 0 {
				return
			}
			// Only include tomorrow's window for its pre-cool lead onto day0.
			if !atLocal.Before(start.Add(-lead)) && atLocal.Before(start) {
				candidates = append(candidates, candidateWindow{
					slot:      cloneScheduleSlot(slot),
					exc:       exc,
					baseStart: start,
					baseEnd:   end,
				})
			}
		})
	} else {
		for _, day := range []time.Time{day0.AddDate(0, 0, -1), day0, day0.AddDate(0, 0, 1)} {
			forEachDaySlot(day, sched, func(slot ScheduleSlot, exc *ScheduleException) {
				appendWindow(day, slot, exc)
			})
		}
	}

	for _, c := range candidates {
		if !atLocal.Before(c.baseStart) && atLocal.Before(c.baseEnd) {
			slotCopy := cloneScheduleSlot(c.slot)
			reason := "within scheduled cooling slot"
			if c.exc != nil {
				reason = formatExceptionReason(c.exc)
			}
			return ScheduleEvaluation{
				LocalTime:             atLocal,
				LocalDate:             localDate,
				InBaseSlot:            true,
				WithinOperatingWindow: true,
				ActiveSlot:            &slotCopy,
				ActiveException:       c.exc,
				TargetSupplyTempC:     cloneFloatPtr(c.slot.TargetSupplyTempC),
				OperatingState:        OperatingStateScheduledOpen,
				Reason:                reason,
			}, nil
		}
	}

	var preCoolMatch *candidateWindow
	var coastDownMatch *candidateWindow
	for i := range candidates {
		c := &candidates[i]
		if lead > 0 && !atLocal.Before(c.baseStart.Add(-lead)) && atLocal.Before(c.baseStart) {
			if preCoolMatch == nil {
				preCoolMatch = c
			}
		}
		if lag > 0 && !atLocal.Before(c.baseEnd) && atLocal.Before(c.baseEnd.Add(lag)) {
			if coastDownMatch == nil {
				coastDownMatch = c
			}
		}
	}

	inPreCool := preCoolMatch != nil
	inCoastDown := coastDownMatch != nil
	if inPreCool {
		slotCopy := cloneScheduleSlot(preCoolMatch.slot)
		reason := "within pre-cool lead window"
		if preCoolMatch.exc != nil {
			reason = fmt.Sprintf("%s (pre-cool lead)", formatExceptionReason(preCoolMatch.exc))
		}
		return ScheduleEvaluation{
			LocalTime:             atLocal,
			LocalDate:             localDate,
			InPreCool:             true,
			InCoastDown:           inCoastDown,
			WithinOperatingWindow: true,
			ActiveSlot:            &slotCopy,
			ActiveException:       preCoolMatch.exc,
			TargetSupplyTempC:     cloneFloatPtr(preCoolMatch.slot.TargetSupplyTempC),
			OperatingState:        OperatingStatePreCool,
			Reason:                reason,
		}, nil
	}

	if inCoastDown {
		slotCopy := cloneScheduleSlot(coastDownMatch.slot)
		reason := "within coast-down lag window"
		if coastDownMatch.exc != nil {
			reason = fmt.Sprintf("%s (coast-down lag)", formatExceptionReason(coastDownMatch.exc))
		}
		return ScheduleEvaluation{
			LocalTime:             atLocal,
			LocalDate:             localDate,
			InCoastDown:           true,
			WithinOperatingWindow: true,
			ActiveSlot:            &slotCopy,
			ActiveException:       coastDownMatch.exc,
			TargetSupplyTempC:     cloneFloatPtr(coastDownMatch.slot.TargetSupplyTempC),
			OperatingState:        OperatingStateCoastDown,
			Reason:                reason,
		}, nil
	}

	if !configured {
		return ScheduleEvaluation{
			LocalTime:      atLocal,
			LocalDate:      localDate,
			OperatingState: OperatingStateUnconfigured,
			Reason:         "no operating schedule slots configured",
		}, nil
	}

	reason := "outside scheduled cooling window"
	if todayExc != nil {
		reason = fmt.Sprintf("%s: outside replacement cooling slots", formatExceptionReason(todayExc))
	}
	return ScheduleEvaluation{
		LocalTime:       atLocal,
		LocalDate:       localDate,
		ActiveException: todayExc,
		OperatingState:  OperatingStateOutsideSchedule,
		Reason:          reason,
	}, nil
}

func forEachDaySlot(
	day time.Time,
	sched OperatingSchedule,
	emit func(slot ScheduleSlot, exc *ScheduleException),
) {
	exc := findExceptionForDate(sched.Exceptions, day.Format("2006-01-02"))
	if exc != nil {
		kind := strings.ToUpper(strings.TrimSpace(exc.Kind))
		if kind == ExceptionHolidayShutdown || kind == ExceptionMaintenanceWindow {
			return
		}
		if kind == ExceptionOvertimeCooling {
			for _, slot := range exc.ReplacementSlots {
				emit(slot, exc)
			}
			return
		}
	}

	dow := isoWeekday(day.Weekday())
	for _, slot := range sched.Slots {
		slotDow := slot.DayOfWeek
		if slotDow == 0 {
			slotDow = 7
		}
		if slotDow == dow {
			emit(slot, nil)
		}
	}
}

func resolveSlotBounds(day time.Time, slot ScheduleSlot, loc *time.Location) (time.Time, time.Time, bool) {
	if slot.StartMinute < 0 || slot.StartMinute >= 1440 || slot.EndMinute <= 0 || slot.EndMinute > 1440 {
		return time.Time{}, time.Time{}, false
	}
	if slot.EndMinute == slot.StartMinute {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), slot.StartMinute/60, slot.StartMinute%60, 0, 0, loc)
	if slot.EndMinute > slot.StartMinute {
		end := time.Date(day.Year(), day.Month(), day.Day(), slot.EndMinute/60, slot.EndMinute%60, 0, 0, loc)
		return start, end, true
	}
	// EndMinute < StartMinute represents an unsplit cross-midnight slot ending next day.
	nextDay := day.AddDate(0, 0, 1)
	end := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), slot.EndMinute/60, slot.EndMinute%60, 0, 0, loc)
	return start, end, true
}

func isoWeekday(wd time.Weekday) int {
	if wd == time.Sunday {
		return 7
	}
	return int(wd)
}

func findExceptionForDate(exceptions []ScheduleException, dateStr string) *ScheduleException {
	var best *ScheduleException
	bestRank := 0
	rankOf := func(kind string) int {
		switch strings.ToUpper(strings.TrimSpace(kind)) {
		case ExceptionMaintenanceWindow:
			return 3
		case ExceptionHolidayShutdown:
			return 2
		case ExceptionOvertimeCooling:
			return 1
		default:
			return 0
		}
	}
	for _, exc := range exceptions {
		start := strings.TrimSpace(exc.StartDate)
		end := strings.TrimSpace(exc.EndDate)
		if start == "" || end == "" || start > end {
			continue
		}
		if dateStr < start || dateStr > end {
			continue
		}
		rank := rankOf(exc.Kind)
		if best == nil || rank > bestRank {
			copied := cloneScheduleException(exc)
			best = &copied
			bestRank = rank
		}
	}
	return best
}

func formatExceptionReason(exc *ScheduleException) string {
	if exc == nil {
		return ""
	}
	label := strings.TrimSpace(exc.Title)
	if label == "" {
		label = strings.TrimSpace(exc.Kind)
	}
	kind := strings.ToUpper(strings.TrimSpace(exc.Kind))
	if reason := strings.TrimSpace(exc.Reason); reason != "" {
		return fmt.Sprintf("%s (%s): %s", kind, label, reason)
	}
	return fmt.Sprintf("%s (%s)", kind, label)
}
