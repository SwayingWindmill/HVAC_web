package edgecontrol

import (
	"math"
	"testing"
	"time"
)

func TestDeadbandFilterDisabledWhenNonPositiveOrNonFinite(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, deadband := range []float64{0, -0.5, math.NaN(), math.Inf(1)} {
		filter := NewDeadbandFilter(deadband, 10*time.Second)
		if filter.Enabled() {
			t.Fatalf("expected Deadband=%v to disable filter", deadband)
		}
		for i := 0; i < 3; i++ {
			if !filter.Allow(DeadbandSample{
				Key:        "CHILLER-01/chw_temp",
				PointType:  "TELEMETRY",
				ValueType:  "NUMBER",
				Value:      7.0,
				Quality:    QualityGood,
				ObservedAt: t0.Add(time.Duration(i) * time.Second),
			}) {
				t.Fatalf("disabled filter (Deadband=%v) suppressed sample %d", deadband, i)
			}
		}
	}
}

func TestDeadbandFilterAnalogSuppressionDriftAndMaxSilenceHeartbeat(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	filter := &DeadbandFilter{
		Deadband:   0.5,
		MaxSilence: 60 * time.Second,
	}

	if !filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.0, Quality: QualityGood, ObservedAt: t0}) {
		t.Fatal("first sample must pass")
	}

	if filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.2, Quality: QualityGood, ObservedAt: t0.Add(10 * time.Second)}) {
		t.Fatal("7.2 within 0.5 of 7.0 should be suppressed")
	}
	if filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.49, Quality: QualityGood, ObservedAt: t0.Add(20 * time.Second)}) {
		t.Fatal("7.49 within 0.5 of 7.0 should be suppressed")
	}

	if !filter.Allow(DeadbandSample{Key: "CHILLER-01/cdw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 30.0, Quality: QualityGood, ObservedAt: t0.Add(25 * time.Second)}) {
		t.Fatal("first sample for cdw_temp must pass")
	}

	if !filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.5, Quality: QualityGood, ObservedAt: t0.Add(30 * time.Second)}) {
		t.Fatal("7.5 reaches deadband >= 0.5 from 7.0 and must pass")
	}

	if filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.6, Quality: QualityGood, ObservedAt: t0.Add(89 * time.Second)}) {
		t.Fatal("sample before MaxSilence should be suppressed")
	}

	if !filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.6, Quality: QualityGood, ObservedAt: t0.Add(90 * time.Second)}) {
		t.Fatal("sample at elapsed >= MaxSilence must emit heartbeat")
	}

	if filter.Allow(DeadbandSample{Key: "CHILLER-01/chw_temp", PointType: "TELEMETRY", ValueType: "NUMBER", Value: 7.6, Quality: QualityGood, ObservedAt: t0.Add(91 * time.Second)}) {
		t.Fatal("sample 1s after heartbeat should be suppressed")
	}
}

func TestDeadbandFilterAlwaysPassesNonGoodQualityCountersStatesBooleansAndNonFinite(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	filter := NewDeadbandFilter(1.0, time.Hour)

	if !filter.Allow(DeadbandSample{Key: "analog", Value: 20.0, Quality: QualityGood, ObservedAt: t0}) {
		t.Fatal("initial GOOD sample must pass")
	}
	if !filter.Allow(DeadbandSample{Key: "analog", Value: 20.0, Quality: QualityBad, ObservedAt: t0.Add(time.Second)}) {
		t.Fatal("non-GOOD sample must always pass")
	}
	if !filter.Allow(DeadbandSample{Key: "analog", Value: 20.0, Quality: QualitySuspect, ObservedAt: t0.Add(2 * time.Second)}) {
		t.Fatal("repeated non-GOOD sample must always pass")
	}
	if !filter.Allow(DeadbandSample{Key: "analog", Value: 20.0, Quality: QualityGood, ObservedAt: t0.Add(3 * time.Second)}) {
		t.Fatal("quality recovery back to GOOD must pass even with unchanged value")
	}
	if filter.Allow(DeadbandSample{Key: "analog", Value: 20.0, Quality: QualityGood, ObservedAt: t0.Add(4 * time.Second)}) {
		t.Fatal("subsequent identical GOOD sample should be suppressed")
	}

	if !filter.Allow(DeadbandSample{Key: "analog", Value: math.NaN(), Quality: QualityGood, ObservedAt: t0.Add(5 * time.Second)}) {
		t.Fatal("NaN must pass")
	}
	if !filter.Allow(DeadbandSample{Key: "analog", Value: math.Inf(1), Quality: QualityGood, ObservedAt: t0.Add(6 * time.Second)}) {
		t.Fatal("+Inf must pass")
	}
	if !filter.Allow(DeadbandSample{Key: "analog", Value: 20.0, Quality: QualityGood, ObservedAt: t0.Add(7 * time.Second)}) {
		t.Fatal("finite sample following NaN/Inf must pass")
	}

	for i := 0; i < 3; i++ {
		if !filter.Allow(DeadbandSample{
			Key:        "meter/kwh",
			PointType:  "COUNTER",
			ValueType:  "NUMBER",
			Value:      1234.5,
			Quality:    QualityGood,
			ObservedAt: t0.Add(time.Duration(i) * time.Second),
		}) {
			t.Fatalf("COUNTER sample %d must pass", i)
		}
	}

	for i := 0; i < 3; i++ {
		if !filter.Allow(DeadbandSample{
			Key:        "chiller/run_state",
			PointType:  "STATE",
			ValueType:  "NUMBER",
			Value:      1,
			Quality:    QualityGood,
			ObservedAt: t0.Add(time.Duration(i) * time.Second),
		}) {
			t.Fatalf("STATE sample %d must pass", i)
		}
	}

	for i := 0; i < 2; i++ {
		if !filter.Allow(DeadbandSample{
			Key:        "pump/running",
			PointType:  "TELEMETRY",
			ValueType:  "BOOLEAN",
			Value:      BooleanValue(true),
			Quality:    QualityGood,
			ObservedAt: t0.Add(time.Duration(i) * time.Second),
		}) {
			t.Fatalf("BOOLEAN sample %d must pass", i)
		}
		if !filter.Allow(DeadbandSample{
			Key:        "chiller/mode",
			Value:      StringValue("AUTO"),
			Quality:    QualityGood,
			ObservedAt: t0.Add(time.Duration(i) * time.Second),
		}) {
			t.Fatalf("string sample %d must pass", i)
		}
	}
}

func TestDeadbandFilterCheckMarkEmittedForgetAndReset(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	filter := NewDeadbandFilter(0.5, 0)

	s1 := DeadbandSample{Key: "k1", Value: DoubleValue(10.0), Quality: QualityGood, ObservedAt: t0}
	if !filter.Check(s1) {
		t.Fatal("first Check must return true")
	}
	if !filter.Check(s1) {
		t.Fatal("Check must not mutate state")
	}
	filter.MarkEmitted(s1)
	if filter.Check(DeadbandSample{Key: "k1", Value: DoubleValue(10.2), Quality: QualityGood, ObservedAt: t0.Add(time.Second)}) {
		t.Fatal("sample within deadband after MarkEmitted should be suppressed")
	}
	filter.Forget("k1")
	if !filter.Check(DeadbandSample{Key: "k1", Value: DoubleValue(10.2), Quality: QualityGood, ObservedAt: t0.Add(2 * time.Second)}) {
		t.Fatal("sample after Forget should pass as first sample")
	}
	filter.MarkEmitted(s1)
	filter.Reset()
	if !filter.Allow(DeadbandSample{Key: "k1", Value: DoubleValue(10.1), Quality: QualityGood, ObservedAt: t0.Add(3 * time.Second)}) {
		t.Fatal("sample after Reset should pass as first sample")
	}
}
