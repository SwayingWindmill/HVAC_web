package measurement

import (
	"math"
	"testing"
	"time"
)

// A synthetic idle window: pumps on, every chiller off, both temperatures steady.
// Songyaxuan circulates like this for about 600 minutes a week, around 07:00,
// 13:00–14:00 and 20:00–21:00 Beijing. The window here is the short one we accept.
func TestStableIdleWindowEstimatesOffsetAndDoesNotApplyIt(t *testing.T) {
	start := time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC) // 13:00 Beijing
	cfg := OffsetConfig{}
	var window OffsetWindow
	var estimate *OffsetEstimate
	for minute := 0; minute <= 12; minute++ {
		window, estimate = FoldOffset(cfg, window, nil, OffsetSample{
			At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 12.00, ReturnC: 12.22, Eligible: true,
		})
		if estimate != nil {
			t.Fatalf("minute %d closed the window early: %+v", minute, estimate)
		}
	}
	if !window.Open || window.Count != 13 {
		t.Fatalf("window %+v", window)
	}
	window, estimate = FoldOffset(cfg, window, nil, OffsetSample{At: start.Add(13 * time.Minute), Eligible: false})
	if estimate == nil {
		t.Fatal("leaving the idle condition should close a long enough window")
	}
	if math.Abs(estimate.OffsetK-0.22) > 1e-9 || estimate.Count != 13 || estimate.StddevK > 1e-9 {
		t.Fatalf("%+v", estimate)
	}
	if estimate.Applied {
		t.Fatal("offset must stay a suggestion")
	}
	if estimate.Alarm != AlarmOffset {
		t.Fatalf("0.22 K is past the 0.20 K line: %s", estimate.Alarm)
	}
	if math.Abs(CorrectedDeltaT(0.90, estimate.OffsetK)-0.68) > 1e-9 {
		t.Fatalf("suggested correction = %.3f", CorrectedDeltaT(0.90, estimate.OffsetK))
	}
	if estimate.EndedAt.Sub(estimate.StartedAt) != 12*time.Minute {
		t.Fatalf("duration %s", estimate.EndedAt.Sub(estimate.StartedAt))
	}
}

func TestShortOrUnstableOrRunningWindowsDoNotEstimate(t *testing.T) {
	start := time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC)
	cfg := OffsetConfig{}
	short := feed(cfg, start, 8, func(minute int) OffsetSample {
		return OffsetSample{At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 12, ReturnC: 12.1, Eligible: true}
	}, false)
	if short != nil {
		t.Fatalf("8 minutes is under 10: %+v", short)
	}
	wandering := feed(cfg, start, 12, func(minute int) OffsetSample {
		return OffsetSample{At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 12 + 0.05*float64(minute), ReturnC: 12.2 + 0.05*float64(minute), Eligible: true}
	}, false)
	if wandering != nil {
		t.Fatalf("a 0.05 K/min drift leaves the 0.1 K band: %+v", wandering)
	}
	running := feed(cfg, start, 20, func(minute int) OffsetSample {
		return OffsetSample{At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 7, ReturnC: 7.2, Eligible: false}
	}, false)
	if running != nil {
		t.Fatalf("chillers on is not a zero: %+v", running)
	}
}

func TestOffsetDriftFromThePreviousEstimate(t *testing.T) {
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) // 20:00 Beijing
	previous := 0.05
	cfg := OffsetConfig{}
	var window OffsetWindow
	for minute := 0; minute <= 12; minute++ {
		var ignored *OffsetEstimate
		window, ignored = FoldOffset(cfg, window, &previous, OffsetSample{
			At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 11, ReturnC: 11.18, Eligible: true,
		})
		if ignored != nil {
			t.Fatal(ignored)
		}
	}
	_, estimate := FoldOffset(cfg, window, &previous, OffsetSample{At: start.Add(13 * time.Minute), Eligible: false})
	if estimate == nil || estimate.Alarm != AlarmDrift || estimate.Applied {
		t.Fatalf("%+v", estimate)
	}
	if math.Abs(estimate.OffsetK-0.18) > 1e-9 {
		t.Fatalf("offset %.3f", estimate.OffsetK)
	}
}

func TestOffsetAndDriftTogether(t *testing.T) {
	start := time.Date(2026, 10, 28, 23, 0, 0, 0, time.UTC) // 07:00 Beijing the next morning
	previous := 0.05
	var window OffsetWindow
	var estimate *OffsetEstimate
	for minute := 0; minute <= 12; minute++ {
		window, estimate = FoldOffset(OffsetConfig{}, window, &previous, OffsetSample{
			At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 13, ReturnC: 13.30, Eligible: true,
		})
		if estimate != nil {
			t.Fatal(estimate)
		}
	}
	_, estimate = FoldOffset(OffsetConfig{}, window, &previous, OffsetSample{At: start.Add(13 * time.Minute), Eligible: false})
	if estimate == nil || estimate.Alarm != AlarmOffsetAndDrift || math.Abs(estimate.OffsetK-0.30) > 1e-9 || estimate.Applied {
		t.Fatalf("%+v", estimate)
	}
}

func TestRereadingTheSameTemperaturesDoesNotResetTheWindow(t *testing.T) {
	start := time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC)
	var window OffsetWindow
	for minute := 0; minute <= 4; minute++ {
		window, _ = FoldOffset(OffsetConfig{}, window, nil, OffsetSample{
			At: start.Add(time.Duration(minute) * time.Minute), SupplyC: 12, ReturnC: 12.1, Eligible: true,
		})
	}
	again, estimate := FoldOffset(OffsetConfig{}, window, nil, OffsetSample{At: window.LastAt, SupplyC: 12, ReturnC: 12.1, Eligible: true})
	if estimate != nil || again.Count != window.Count {
		t.Fatalf("repeat count %d estimate %+v", again.Count, estimate)
	}
}

func TestSmallSteadyOffsetDoesNotAlarm(t *testing.T) {
	start := time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC)
	estimate := closeAfter(start, 12.00, 12.08)
	if estimate == nil || estimate.Alarm != "" || math.Abs(estimate.OffsetK-0.08) > 1e-9 || estimate.Applied {
		t.Fatalf("%+v", estimate)
	}
}

func closeAfter(start time.Time, supply, ret float64) *OffsetEstimate {
	var window OffsetWindow
	for minute := 0; minute <= 12; minute++ {
		window, _ = FoldOffset(OffsetConfig{}, window, nil, OffsetSample{
			At: start.Add(time.Duration(minute) * time.Minute), SupplyC: supply, ReturnC: ret, Eligible: true,
		})
	}
	_, estimate := FoldOffset(OffsetConfig{}, window, nil, OffsetSample{At: start.Add(13 * time.Minute), Eligible: false})
	return estimate
}

func feed(cfg OffsetConfig, start time.Time, minutes int, sample func(int) OffsetSample, close bool) *OffsetEstimate {
	var window OffsetWindow
	var estimate *OffsetEstimate
	for minute := 0; minute <= minutes; minute++ {
		window, estimate = FoldOffset(cfg, window, nil, sample(minute))
		if estimate != nil {
			return estimate
		}
	}
	if close {
		_, estimate = FoldOffset(cfg, window, nil, OffsetSample{At: start.Add(time.Duration(minutes+1) * time.Minute), Eligible: false})
	}
	return estimate
}
