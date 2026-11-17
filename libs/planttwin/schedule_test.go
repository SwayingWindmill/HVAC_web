package planttwin

import (
	"errors"
	"testing"
	"time"
)

func shanghaiAt(year int, month time.Month, day, hour, min int) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return time.Date(year, month, day, hour, min, 0, 0, loc)
}

func nyAt(year int, month time.Month, day, hour, min int) time.Time {
	loc, _ := time.LoadLocation("America/New_York")
	return time.Date(year, month, day, hour, min, 0, 0, loc)
}

func TestEvaluateOperatingScheduleRejectsInvalidTimezone(t *testing.T) {
	for _, tz := range []string{"", "   ", "Invalid/Zone"} {
		_, err := EvaluateOperatingSchedule(time.Now(), OperatingSchedule{Timezone: tz})
		if !errors.Is(err, ErrInvalidTimezone) {
			t.Fatalf("timezone %q: expected ErrInvalidTimezone, got %v", tz, err)
		}
	}
}

func TestEvaluateOperatingScheduleUnconfiguredAndBuffers(t *testing.T) {
	unconfigured, err := EvaluateOperatingSchedule(
		shanghaiAt(2026, 11, 16, 10, 0),
		OperatingSchedule{Timezone: "Asia/Shanghai"},
	)
	if err != nil {
		t.Fatalf("unconfigured evaluate error: %v", err)
	}
	if unconfigured.OperatingState != OperatingStateUnconfigured || unconfigured.WithinOperatingWindow {
		t.Fatalf("expected UNCONFIGURED outside window, got %+v", unconfigured)
	}

	temp := 7.5
	sched := OperatingSchedule{
		Timezone:            "Asia/Shanghai",
		PreCoolLeadMinutes:  45,
		CoastDownLagMinutes: 30,
		Slots: []ScheduleSlot{
			{DayOfWeek: 1, StartMinute: 8*60 + 30, EndMinute: 18 * 60, TargetSupplyTempC: &temp},
		},
	}

	cases := []struct {
		name      string
		at        time.Time
		wantState string
		wantOpen  bool
		wantBase  bool
		wantPre   bool
		wantCoast bool
	}{
		{"before pre-cool", shanghaiAt(2026, 11, 16, 7, 44), OperatingStateOutsideSchedule, false, false, false, false},
		{"pre-cool start", shanghaiAt(2026, 11, 16, 7, 45), OperatingStatePreCool, true, false, true, false},
		{"slot start", shanghaiAt(2026, 11, 16, 8, 30), OperatingStateScheduledOpen, true, true, false, false},
		{"slot end -> coast-down", shanghaiAt(2026, 11, 16, 18, 0), OperatingStateCoastDown, true, false, false, true},
		{"coast-down end", shanghaiAt(2026, 11, 16, 18, 30), OperatingStateOutsideSchedule, false, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := EvaluateOperatingSchedule(tc.at, sched)
			if err != nil {
				t.Fatalf("EvaluateOperatingSchedule error: %v", err)
			}
			if ev.OperatingState != tc.wantState || ev.WithinOperatingWindow != tc.wantOpen ||
				ev.InBaseSlot != tc.wantBase || ev.InPreCool != tc.wantPre || ev.InCoastDown != tc.wantCoast {
				t.Fatalf("got %+v, want state=%s open=%v base=%v pre=%v coast=%v",
					ev, tc.wantState, tc.wantOpen, tc.wantBase, tc.wantPre, tc.wantCoast)
			}
			if tc.wantOpen && (ev.TargetSupplyTempC == nil || *ev.TargetSupplyTempC != 7.5) {
				t.Fatalf("expected TargetSupplyTempC 7.5, got %v", ev.TargetSupplyTempC)
			}
		})
	}

	// Zero buffers must be preserved, not replaced with 45/30 defaults.
	zeroBuf := sched
	zeroBuf.PreCoolLeadMinutes = 0
	zeroBuf.CoastDownLagMinutes = 0
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 16, 8, 15), zeroBuf); err != nil || ev.WithinOperatingWindow {
		t.Fatalf("expected zero pre-cool lead preserved, got %+v err=%v", ev, err)
	}
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 16, 18, 5), zeroBuf); err != nil || ev.WithinOperatingWindow {
		t.Fatalf("expected zero coast-down lag preserved, got %+v err=%v", ev, err)
	}
}

func TestEvaluateOperatingScheduleOvernightWraparoundAndTimezoneRollover(t *testing.T) {
	// Split overnight slot (Sat 22:00-24:00, Sun 00:00-06:00) + early Monday 00:15 slot with 45m lead.
	sched := OperatingSchedule{
		Timezone:            "Asia/Shanghai",
		PreCoolLeadMinutes:  45,
		CoastDownLagMinutes: 30,
		Slots: []ScheduleSlot{
			{DayOfWeek: 6, StartMinute: 22 * 60, EndMinute: 24 * 60},
			{DayOfWeek: 7, StartMinute: 0, EndMinute: 6 * 60},
			{DayOfWeek: 1, StartMinute: 15, EndMinute: 8 * 60},
		},
	}

	// Sunday 00:10 must be SCHEDULED_OPEN (not COAST_DOWN from Saturday 22:00-24:00).
	evSunOpen, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 0, 10), sched)
	if err != nil || evSunOpen.OperatingState != OperatingStateScheduledOpen || !evSunOpen.InBaseSlot {
		t.Fatalf("expected Sunday 00:10 SCHEDULED_OPEN, got %+v err=%v", evSunOpen, err)
	}
	// Sunday 06:15 is in coast-down lag from Sunday 00:00-06:00.
	evSunCoast, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 6, 15), sched)
	if err != nil || evSunCoast.OperatingState != OperatingStateCoastDown || !evSunCoast.InCoastDown {
		t.Fatalf("expected Sunday 06:15 COAST_DOWN, got %+v err=%v", evSunCoast, err)
	}
	// Sunday 23:35 is within 45m pre-cool of Monday 00:15 across midnight boundary!
	evPreMidnight, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 23, 35), sched)
	if err != nil || evPreMidnight.OperatingState != OperatingStatePreCool || !evPreMidnight.InPreCool {
		t.Fatalf("expected Sunday 23:35 PRE_COOL for Monday 00:15 slot, got %+v err=%v", evPreMidnight, err)
	}

	// Unsplit overnight representation (EndMinute < StartMinute): Fri 22:00 -> Sat 05:00.
	unsplit := OperatingSchedule{
		Timezone:            "Asia/Shanghai",
		CoastDownLagMinutes: 20,
		Slots: []ScheduleSlot{
			{DayOfWeek: 5, StartMinute: 22 * 60, EndMinute: 5 * 60},
		},
	}
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 14, 2, 0), unsplit); err != nil || ev.OperatingState != OperatingStateScheduledOpen {
		t.Fatalf("expected Saturday 02:00 inside Fri overnight slot, got %+v err=%v", ev, err)
	}
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 14, 5, 10), unsplit); err != nil || ev.OperatingState != OperatingStateCoastDown {
		t.Fatalf("expected Saturday 05:10 coast-down from Fri overnight slot, got %+v err=%v", ev, err)
	}

	// UTC Sunday 2026-11-15 16:30 == Shanghai Monday 2026-11-16 00:30.
	utcSched := OperatingSchedule{
		Timezone: "Asia/Shanghai",
		Slots: []ScheduleSlot{
			{DayOfWeek: 0, StartMinute: 0, EndMinute: 24 * 60}, // 0=Sunday accepted alongside 7=Sunday
			{DayOfWeek: 1, StartMinute: 8 * 60, EndMinute: 18 * 60},
		},
	}
	evUTC, err := EvaluateOperatingSchedule(time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC), utcSched)
	if err != nil || evUTC.LocalDate != "2026-11-16" || evUTC.OperatingState != OperatingStateOutsideSchedule {
		t.Fatalf("expected Shanghai Monday 00:30 outside schedule, got %+v err=%v", evUTC, err)
	}
}

func TestEvaluateOperatingScheduleDSTTransitions(t *testing.T) {
	sched := OperatingSchedule{
		Timezone:            "America/New_York",
		PreCoolLeadMinutes:  45,
		CoastDownLagMinutes: 30,
		Slots: []ScheduleSlot{
			{DayOfWeek: 7, StartMinute: 8 * 60, EndMinute: 18 * 60},
		},
	}

	// 2026-03-08 is US Spring Forward (02:00 EST -> 03:00 EDT). 08:00 wall-clock must still be 08:00 EDT.
	if ev, err := EvaluateOperatingSchedule(nyAt(2026, 3, 8, 7, 14), sched); err != nil || ev.WithinOperatingWindow {
		t.Fatalf("expected 07:14 EDT outside pre-cool, got %+v err=%v", ev, err)
	}
	if ev, err := EvaluateOperatingSchedule(nyAt(2026, 3, 8, 7, 15), sched); err != nil || ev.OperatingState != OperatingStatePreCool {
		t.Fatalf("expected 07:15 EDT in pre-cool, got %+v err=%v", ev, err)
	}
	if ev, err := EvaluateOperatingSchedule(nyAt(2026, 3, 8, 8, 0), sched); err != nil || ev.OperatingState != OperatingStateScheduledOpen {
		t.Fatalf("expected 08:00 EDT in base slot, got %+v err=%v", ev, err)
	}

	// 2026-11-01 is US Fall Back (02:00 EDT -> 01:00 EST). 08:00 EST must still be slot start.
	if ev, err := EvaluateOperatingSchedule(nyAt(2026, 11, 1, 7, 50), sched); err != nil || ev.OperatingState != OperatingStatePreCool {
		t.Fatalf("expected 07:50 EST in pre-cool, got %+v err=%v", ev, err)
	}
	if ev, err := EvaluateOperatingSchedule(nyAt(2026, 11, 1, 8, 0), sched); err != nil || ev.OperatingState != OperatingStateScheduledOpen {
		t.Fatalf("expected 08:00 EST in base slot, got %+v err=%v", ev, err)
	}
}

func TestEvaluateOperatingScheduleExceptions(t *testing.T) {
	tempOT := 6.8
	sched := OperatingSchedule{
		Timezone:            "Asia/Shanghai",
		PreCoolLeadMinutes:  30,
		CoastDownLagMinutes: 15,
		Slots: []ScheduleSlot{
			{DayOfWeek: 1, StartMinute: 22 * 60, EndMinute: 24 * 60},
			{DayOfWeek: 2, StartMinute: 8 * 60, EndMinute: 18 * 60},
			{DayOfWeek: 6, StartMinute: 22 * 60, EndMinute: 24 * 60},
		},
		Exceptions: []ScheduleException{
			{
				ID: "exc-holiday", Title: "公休停机", Kind: ExceptionHolidayShutdown,
				StartDate: "2026-11-17", EndDate: "2026-11-17", Reason: "法定假日",
			},
			{
				ID: "exc-maint", Title: "机组检修", Kind: ExceptionMaintenanceWindow,
				StartDate: "2026-11-18", EndDate: "2026-11-19", Reason: "清洗冷凝器",
			},
			{
				ID: "exc-ot", Title: "周日加班供冷", Kind: ExceptionOvertimeCooling,
				StartDate: "2026-11-15", EndDate: "2026-11-15",
				ReplacementSlots: []ScheduleSlot{{StartMinute: 10 * 60, EndMinute: 15 * 60, TargetSupplyTempC: &tempOT}},
			},
		},
	}

	// HOLIDAY_SHUTDOWN suppresses both Tuesday 00:10 spillover and 10:00 regular slot.
	for _, at := range []time.Time{shanghaiAt(2026, 11, 17, 0, 10), shanghaiAt(2026, 11, 17, 10, 0)} {
		ev, err := EvaluateOperatingSchedule(at, sched)
		if err != nil {
			t.Fatalf("holiday eval err: %v", err)
		}
		if !ev.Suppressed || ev.WithinOperatingWindow || ev.OperatingState != OperatingStateExceptionClosed || ev.ActiveException == nil || ev.ActiveException.ID != "exc-holiday" {
			t.Fatalf("expected holiday suppression at %s, got %+v", at, ev)
		}
	}

	// MAINTENANCE_WINDOW spans inclusive [StartDate, EndDate].
	evMaint, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 19, 14, 0), sched)
	if err != nil || !evMaint.Suppressed || evMaint.OperatingState != OperatingStateExceptionClosed || evMaint.ActiveException.ID != "exc-maint" {
		t.Fatalf("expected maintenance suppression on endDate inclusive, got %+v err=%v", evMaint, err)
	}

	// OVERTIME_COOLING blocks Saturday 22:00-24:00 spillover at Sunday 00:05 and applies replacement pre-cool/base/coast-down.
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 0, 5), sched); err != nil || ev.WithinOperatingWindow || ev.OperatingState != OperatingStateOutsideSchedule {
		t.Fatalf("expected Sat spillover blocked on Sunday overtime day, got %+v err=%v", ev, err)
	}
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 9, 40), sched); err != nil || ev.OperatingState != OperatingStatePreCool || !ev.WithinOperatingWindow {
		t.Fatalf("expected overtime pre-cool at 09:40, got %+v err=%v", ev, err)
	}
	evOT, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 11, 0), sched)
	if err != nil || evOT.OperatingState != OperatingStateScheduledOpen || evOT.TargetSupplyTempC == nil || *evOT.TargetSupplyTempC != 6.8 {
		t.Fatalf("expected overtime scheduled open with 6.8C target, got %+v err=%v", evOT, err)
	}
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 15, 15, 10), sched); err != nil || ev.OperatingState != OperatingStateCoastDown {
		t.Fatalf("expected overtime coast-down at 15:10, got %+v err=%v", ev, err)
	}

	// Unconfigured weekly schedule remains UNCONFIGURED on days before a future OVERTIME_COOLING exception.
	unconfWithFutureOT := OperatingSchedule{
		Timezone: "Asia/Shanghai",
		Exceptions: []ScheduleException{
			{
				ID: "exc-ot-future", Title: "明日加班", Kind: ExceptionOvertimeCooling,
				StartDate: "2026-11-17", EndDate: "2026-11-17",
				ReplacementSlots: []ScheduleSlot{{StartMinute: 10 * 60, EndMinute: 15 * 60}},
			},
		},
	}
	if ev, err := EvaluateOperatingSchedule(shanghaiAt(2026, 11, 16, 12, 0), unconfWithFutureOT); err != nil || ev.OperatingState != OperatingStateUnconfigured {
		t.Fatalf("expected UNCONFIGURED before future OVERTIME_COOLING when weekly slots empty, got %+v err=%v", ev, err)
	}
}
