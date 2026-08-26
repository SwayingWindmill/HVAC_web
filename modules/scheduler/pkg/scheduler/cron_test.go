package scheduler

import (
	"testing"
	"time"
)

func TestCron5NextUsesScheduleTimezone(t *testing.T) {
	spec, err := parseCron5("5 0 * * *")
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 8, 14, 16, 4, 0, 0, time.UTC)
	next, err := spec.next(after, location)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 14, 16, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next fire = %s, want %s", next, want)
	}
}

func TestSelectMisfiresCatchUpLimitedKeepsNewestWindows(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	due := []time.Time{
		now.Add(-4 * time.Hour),
		now.Add(-3 * time.Hour),
		now.Add(-2 * time.Hour),
		now.Add(-time.Hour),
	}
	schedule := Schedule{MisfirePolicy: "CATCH_UP_LIMITED", CatchUpLimit: 2}
	selected := selectMisfires(schedule, due, now, time.Minute)
	if len(selected) != 2 || !selected[0].Equal(due[2]) || !selected[1].Equal(due[3]) {
		t.Fatalf("selected misfires = %v, want newest two windows", selected)
	}
}
