package edgecontrol

import (
	"math"
	"testing"
	"time"
)

func TestWindowAggregatorTumblingWindowsAndRawPreservation(t *testing.T) {
	t.Parallel()

	policy := EdgeAggregationPolicy{
		Enabled: true,
		Points: []AggregationPointPolicy{
			{DeviceKey: "weather-1", PointCode: "outdoor_temp", WindowSeconds: AggregationWindow1Min, EmitMinMax: true},
			{DeviceKey: "*", PointCode: "branch_temp", WindowSeconds: AggregationWindow5Min},
		},
		RawAllowlist: []string{"weather-1/raw_override"},
	}
	agg, err := NewWindowAggregator(policy)
	if err != nil {
		t.Fatalf("NewWindowAggregator: %v", err)
	}
	agg.SetProtectedAddresses([]string{"branch-1/rule_watch_temp"})

	roDesc := func(dev, pt string, priority DataPriority, dt DataType) ChannelDescriptor {
		return ChannelDescriptor{
			ComponentID: dev, ChannelID: pt, PointID: dev + ":" + pt,
			DataType: dt, Access: AccessReadOnly, Category: ChannelCategoryOpenemsType,
			PollPriority: priority, LocalPersistencePriority: priority, RemotePersistencePriority: priority,
			AggregationPriority: priority, ResendPriority: priority,
		}
	}

	t0 := time.Date(2026, 11, 22, 10, 0, 10, 0, time.UTC)

	// 1. 60s tumbling window calculates exact mean/min/max and flushes cleanly across boundary.
	for idx, val := range []float64{20.0, 26.0, 23.0} {
		passRaw, flushed := agg.Observe(AggregationSample{
			DeviceKey:  "weather-1",
			PointCode:  "outdoor_temp",
			Descriptor: roDesc("weather-1", "outdoor_temp", PriorityLow, DataTypeDouble),
			Value:      DoubleValue(val),
			Quality:    QualityGood,
			ObservedAt: t0.Add(time.Duration(idx*15) * time.Second),
		})
		if passRaw || len(flushed) != 0 {
			t.Fatalf("sample %d unexpectedly passed raw or flushed early: passRaw=%v flushed=%+v", idx, passRaw, flushed)
		}
	}

	// Out-of-order sample before current window is ignored rather than corrupting closed/future windows.
	if passRaw, flushed := agg.Observe(AggregationSample{
		DeviceKey:  "weather-1",
		PointCode:  "outdoor_temp",
		Descriptor: roDesc("weather-1", "outdoor_temp", PriorityLow, DataTypeDouble),
		Value:      DoubleValue(99.0),
		Quality:    QualityGood,
		ObservedAt: t0.Add(-time.Minute),
	}); passRaw || len(flushed) != 0 {
		t.Fatalf("stale out-of-order sample unexpected result: passRaw=%v flushed=%+v", passRaw, flushed)
	}

	// Sample in next 60s window flushes 10:00:00..10:01:00 bucket immediately.
	passRaw, flushed := agg.Observe(AggregationSample{
		DeviceKey:  "weather-1",
		PointCode:  "outdoor_temp",
		Descriptor: roDesc("weather-1", "outdoor_temp", PriorityLow, DataTypeDouble),
		Value:      DoubleValue(28.0),
		Quality:    QualityGood,
		ObservedAt: t0.Add(time.Minute),
	})
	if passRaw || len(flushed) != 1 {
		t.Fatalf("expected 1 flushed 60s window, got passRaw=%v len=%d", passRaw, len(flushed))
	}
	out := flushed[0]
	if out.Count != 3 || math.Abs(out.Mean-23.0) > 1e-9 || out.Min != 20.0 || out.Max != 26.0 || !out.EmitMinMax || out.WindowSeconds != 60 {
		t.Fatalf("unexpected 60s aggregated output: %+v", out)
	}

	// 2. 300s tumbling window flushes on FlushDue when WindowEnd <= now.
	passRaw5, _ := agg.Observe(AggregationSample{
		DeviceKey:  "aux-2",
		PointCode:  "branch_temp",
		Descriptor: roDesc("aux-2", "branch_temp", PriorityMedium, DataTypeDouble),
		Value:      DoubleValue(14.5),
		Quality:    QualityGood,
		ObservedAt: t0,
	})
	if passRaw5 {
		t.Fatal("expected aux-2/branch_temp to buffer into 300s window")
	}
	if dueEarly := agg.FlushDue(t0.Add(90 * time.Second)); len(dueEarly) != 0 {
		t.Fatalf("expected no due windows before boundary, got %+v", dueEarly)
	}
	dueAt5Min := agg.FlushDue(time.Date(2026, 11, 22, 10, 5, 0, 0, time.UTC))
	if len(dueAt5Min) != 2 {
		t.Fatalf("expected both 60s tail and 300s window flushed at 10:05:00, got %+v", dueAt5Min)
	}

	// 3. Fail-closed raw preservation checks.
	writableDesc := roDesc("chiller-1", "chw_sp", PriorityMedium, DataTypeDouble)
	writableDesc.Access = AccessReadWrite
	rawCases := []struct {
		name   string
		sample AggregationSample
	}{
		{"writable channel", AggregationSample{DeviceKey: "chiller-1", PointCode: "chw_sp", Descriptor: writableDesc, Value: DoubleValue(7.0), Quality: QualityGood, ObservedAt: t0}},
		{"VERY_HIGH priority", AggregationSample{DeviceKey: "chiller-1", PointCode: "crit_temp", Descriptor: roDesc("chiller-1", "crit_temp", PriorityVeryHigh, DataTypeDouble), Value: DoubleValue(7.0), Quality: QualityGood, ObservedAt: t0}},
		{"energy metering semantic", AggregationSample{DeviceKey: "meter-1", PointCode: "active_energy", Semantic: SemanticEnergy, Descriptor: roDesc("meter-1", "active_energy", PriorityLow, DataTypeDouble), Value: DoubleValue(1200.5), Quality: QualityGood, ObservedAt: t0}},
		{"rule protected address", AggregationSample{DeviceKey: "branch-1", PointCode: "rule_watch_temp", Descriptor: roDesc("branch-1", "rule_watch_temp", PriorityLow, DataTypeDouble), Value: DoubleValue(12.0), Quality: QualityGood, ObservedAt: t0}},
		{"non-GOOD quality passes raw", AggregationSample{DeviceKey: "weather-1", PointCode: "outdoor_temp", Descriptor: roDesc("weather-1", "outdoor_temp", PriorityLow, DataTypeDouble), Value: DoubleValue(25.0), Quality: QualityBad, ObservedAt: t0.Add(2 * time.Minute)}},
		{"unmatched point defaults to raw", AggregationSample{DeviceKey: "weather-1", PointCode: "wind_speed", Descriptor: roDesc("weather-1", "wind_speed", PriorityLow, DataTypeDouble), Value: DoubleValue(3.0), Quality: QualityGood, ObservedAt: t0}},
	}
	for _, tc := range rawCases {
		if !agg.MustPreserveRaw(tc.sample) {
			t.Fatalf("%s: expected MustPreserveRaw=true", tc.name)
		}
		if pass, _ := agg.Observe(tc.sample); !pass {
			t.Fatalf("%s: expected Observe passRaw=true", tc.name)
		}
	}

	if err := agg.UpdatePolicy(EdgeAggregationPolicy{
		Points: []AggregationPointPolicy{{DeviceKey: "*", PointCode: "*", WindowSeconds: 120}},
	}); err == nil {
		t.Fatal("expected unsupported 120s window validation error")
	}

	// SetChannelSemantics preserves registered energy channels raw even without inline sample.Semantic.
	agg.SetChannelSemantics(map[string]SemanticChannel{
		"aux-2/branch_temp": SemanticEnergy,
	})
	semanticSample := AggregationSample{
		DeviceKey:  "aux-2",
		PointCode:  "branch_temp",
		Descriptor: roDesc("aux-2", "branch_temp", PriorityLow, DataTypeDouble),
		Value:      DoubleValue(15.0),
		Quality:    QualityGood,
		ObservedAt: t0.Add(6 * time.Minute),
	}
	if !agg.MustPreserveRaw(semanticSample) {
		t.Fatal("expected SetChannelSemantics SemanticEnergy channel to bypass aggregation")
	}

	// CollectEdgeRuleAddresses walks nested Raise/Clear inputs and deduplicates sorted results.
	bundle := &EdgeRuleBundle{
		Rules: []CompiledEdgeRule{
			{
				RuleID: "r1",
				Raise: EdgeCondition{
					Kind: "ALL",
					Children: []EdgeCondition{
						{Kind: "THRESHOLD", Input: "chwp-1/diff_pressure"},
						{Kind: "EXPRESSION", Inputs: map[string]string{"a": "ch-1/sup_temp", "b": "ch-1/ret_temp"}},
					},
				},
				Clear: EdgeCondition{Kind: "THRESHOLD", Input: "chwp-1/diff_pressure"},
			},
		},
	}
	addrs := CollectEdgeRuleAddresses(bundle)
	wantAddrs := []string{"ch-1/ret_temp", "ch-1/sup_temp", "chwp-1/diff_pressure"}
	if len(addrs) != len(wantAddrs) || addrs[0] != wantAddrs[0] || addrs[1] != wantAddrs[1] || addrs[2] != wantAddrs[2] {
		t.Fatalf("CollectEdgeRuleAddresses() = %v, want %v", addrs, wantAddrs)
	}
}
