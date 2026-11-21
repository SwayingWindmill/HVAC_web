package edgecontrol

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

func float64Ptr(v float64) *float64 { return &v }

func mustEdgeBundleDigest(t *testing.T, bundle EdgeRuleBundle) EdgeRuleBundle {
	t.Helper()
	d, err := bundle.ComputeDigest()
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	bundle.EdgeBundleDigest = d
	return bundle
}

func TestComputeWindowAggregateAllFunctions(t *testing.T) {
	vals := []float64{10, 20, 30, 40, 50}
	cases := []struct {
		agg  EdgeWindowAggFunc
		want float64
	}{
		{EdgeWindowAggAvg, 30},
		{EdgeWindowAggMin, 10},
		{EdgeWindowAggMax, 50},
		{EdgeWindowAggSum, 150},
		{EdgeWindowAggCount, 5},
		{EdgeWindowAggStdDev, math.Sqrt(200)},
		{EdgeWindowAggP95, 48},
		{EdgeWindowAggP99, 49.6},
	}
	for _, tc := range cases {
		got, ok := ComputeWindowAggregate(tc.agg, vals)
		if !ok {
			t.Fatalf("ComputeWindowAggregate(%s) ok = false", tc.agg)
		}
		if math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("ComputeWindowAggregate(%s) = %v, want %v", tc.agg, got, tc.want)
		}
	}

	if _, ok := ComputeWindowAggregate(EdgeWindowAggStdDev, []float64{12.5}); ok {
		t.Fatal("STDDEV with 1 sample must return ok=false")
	}
	if _, ok := ComputeWindowAggregate(EdgeWindowAggAvg, nil); ok {
		t.Fatal("empty slice must return ok=false")
	}
}

func TestEdgeRuleBundleValidationAndDigestDeterminism(t *testing.T) {
	const (
		siteID  = "01900000-0000-7000-8000-000000000010"
		devID   = "01900000-1000-7000-8000-000000000001"
		ruleA   = "01900000-2000-7000-8000-000000000001"
		ruleB   = "01900000-2000-7000-8000-000000000002"
		inputCh = devID + "/supply_water_temperature"
	)

	makeRule := func(id string, deps []string) CompiledEdgeRule {
		var gate EdgeDependencyGate
		if len(deps) > 0 {
			gate = EdgeDependencyGateAllActive
		}
		return CompiledEdgeRule{
			RuleID:           id,
			AlarmType:        "site-rule/" + id,
			Title:            "Rule " + id,
			Severity:         "MAJOR",
			ExecutionTarget:  EdgeTargetCloudAndEdge,
			DependsOnRuleIDs: deps,
			DependencyGate:   gate,
			QualityPolicy:    EdgeQualityValidOnly,
			FreshnessSeconds: 900,
			TriggerMode:      EdgeTriggerSimple,
			Raise: EdgeCondition{
				Kind:          EdgeConditionWindowAgg,
				Input:         inputCh,
				Agg:           EdgeWindowAggP95,
				WindowType:    EdgeWindowSliding,
				WindowSeconds: 300,
				MinSamples:    3,
				Operator:      EdgeCompareGT,
				Threshold:     float64Ptr(12),
			},
			Clear: EdgeCondition{
				Kind:     EdgeConditionCompare,
				Input:    inputCh,
				Operator: EdgeCompareLTE,
				Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 10},
			},
		}
	}

	b1 := mustEdgeBundleDigest(t, EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         siteID,
		BundleRevision: 4,
		CompiledAt:     "2026-11-22T00:00:00Z",
		Rules:          []CompiledEdgeRule{makeRule(ruleA, nil), makeRule(ruleB, []string{ruleA})},
	})
	b2 := mustEdgeBundleDigest(t, EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         siteID,
		BundleRevision: 4,
		CompiledAt:     "2026-11-22T00:05:00Z",
		Rules:          []CompiledEdgeRule{makeRule(ruleB, []string{ruleA}), makeRule(ruleA, nil)},
	})
	if b1.EdgeBundleDigest != b2.EdgeBundleDigest {
		t.Fatalf("ComputeDigest() not deterministic across rule order/CompiledAt: %s != %s", b1.EdgeBundleDigest, b2.EdgeBundleDigest)
	}
	if err := b1.Validate(); err != nil {
		t.Fatalf("valid bundle Validate() error = %v", err)
	}

	tampered := b1
	tampered.Rules[0].Title = "Tampered"
	if err := tampered.Validate(); err == nil {
		t.Fatal("expected tamper digest mismatch error")
	}

	cyclic := mustEdgeBundleDigest(t, EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         siteID,
		BundleRevision: 5,
		Rules:          []CompiledEdgeRule{makeRule(ruleA, []string{ruleB}), makeRule(ruleB, []string{ruleA})},
	})
	if err := cyclic.Validate(); err == nil {
		t.Fatal("expected cyclic dependency validation error")
	}
}

func buildEdgeRuleTestHost(t *testing.T, devID, pumpID string, controllers []ControllerBinding) (*Host, *stubAutonomyDeviceAdapter, *stubAutonomyDeviceAdapter) {
	t.Helper()
	host, err := NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	sensorAdapter := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID: devID, Kind: ComponentDeviceDriver, Type: "chiller", FactoryID: DeclarativeModbusFactoryID, Version: "1.0",
			ChannelBindings: map[SemanticChannel]string{
				SemanticSupplyTemperature: devID + "/supply_water_temperature",
				SemanticReturnTemperature: devID + "/return_water_temperature",
			},
		},
		[]ChannelDescriptor{
			testChannelDesc(devID, "supply_water_temperature", "01910000-0000-7000-8000-000000000001", DataTypeDouble, AccessReadOnly),
			testChannelDesc(devID, "return_water_temperature", "01910000-0000-7000-8000-000000000002", DataTypeDouble, AccessReadOnly),
			testChannelDesc(devID, "fault_code", "01910000-0000-7000-8000-000000000003", DataTypeBoolean, AccessReadOnly),
		},
	)
	pumpAdapter := newStubAutonomyDeviceAdapter(
		ComponentDescriptor{
			ID: pumpID, Kind: ComponentDeviceDriver, Type: "pump", FactoryID: DeclarativeModbusFactoryID, Version: "1.0",
			ChannelBindings: map[SemanticChannel]string{
				SemanticRunState:          pumpID + "/run_state",
				SemanticFlow:              pumpID + "/flow_rate",
				SemanticFrequencySetpoint: pumpID + "/set_frequency",
			},
		},
		[]ChannelDescriptor{
			testChannelDesc(pumpID, "run_state", "01910000-0000-7000-8000-000000000011", DataTypeBoolean, AccessReadOnly),
			testChannelDesc(pumpID, "flow_rate", "01910000-0000-7000-8000-000000000012", DataTypeDouble, AccessReadOnly),
			testChannelDesc(pumpID, "set_frequency", "01910000-0000-7000-8000-000000000013", DataTypeDouble, AccessReadWrite),
		},
	)
	if err := host.RegisterAdapter(sensorAdapter); err != nil {
		t.Fatalf("RegisterAdapter(sensor) error = %v", err)
	}
	if err := host.RegisterAdapter(pumpAdapter); err != nil {
		t.Fatalf("RegisterAdapter(pump) error = %v", err)
	}
	if err := host.Start(controllers); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return host, sensorAdapter, pumpAdapter
}

func TestEdgeRuleEngineWindowAggArbitrationHotSwapAndEvents(t *testing.T) {
	const (
		siteID = "01900000-0000-7000-8000-000000000020"
		devID  = "01900000-1000-7000-8000-000000000001"
		pumpID = "01900000-1000-7000-8000-000000000002"
		rule1  = "01900000-3000-7000-8000-000000000001"
		rule2  = "01900000-3000-7000-8000-000000000002"
	)

	policy := DefaultAutonomyPolicyConfig()
	policy.MinPumpFrequencyHz = 20
	policy.MaxPumpFrequencyHz = 50
	hyd, err := NewHydraulicInterlockController(HydraulicInterlockConfig{
		ChilledWaterPumps: []PumpInterlockBinding{{
			ComponentID:      pumpID,
			RunState:         pumpID + "/run_state",
			FlowFeedback:     pumpID + "/flow_rate",
			MinFlowRate:      5,
			FrequencyCommand: pumpID + "/set_frequency",
		}},
		Policy: policy,
	})
	if err != nil {
		t.Fatalf("NewHydraulicInterlockController() error = %v", err)
	}

	edgeCtrl := NewEdgeRuleController(EdgeRuleControllerConfig{
		CommandChannelResolver: func(deviceID, pointCode, capability string) (string, DataType, string, bool) {
			if deviceID == pumpID && capability == "SET_FREQUENCY" {
				return pumpID + "/set_frequency", DataTypeDouble, "frequencyHz", true
			}
			return "", "", "", false
		},
	})
	intentCtrl := &intentRequestController{
		id:   "cloud-command-intent",
		reqs: map[string]Value{pumpID + "/set_frequency": DoubleValue(30)},
	}

	host, sensor, pump := buildEdgeRuleTestHost(t, devID, pumpID, []ControllerBinding{
		{Priority: 5, Controller: hyd},
		{Priority: 15, Controller: edgeCtrl},
		{Priority: 100, Controller: intentCtrl},
	})

	// Rule 1 uses STDDEV (cold-starts on 1 sample, triggers on variance >= 2) + Hysteresis clear.
	// Rule 2 depends on Rule 1 via ALL_ACTIVE AND checks TUMBLING AVG > 11 -> commands pump to 48 Hz.
	bundle := mustEdgeBundleDigest(t, EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         siteID,
		BundleRevision: 1,
		Rules: []CompiledEdgeRule{
			{
				RuleID:           rule1,
				Title:            "Supply temp jitter",
				Severity:         "WARNING",
				ExecutionTarget:  EdgeTargetEdge,
				QualityPolicy:    EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerSimple,
				Raise: EdgeCondition{
					Kind:          EdgeConditionWindowAgg,
					Input:         devID + "/supply_water_temperature",
					Agg:           EdgeWindowAggStdDev,
					WindowType:    EdgeWindowSliding,
					WindowSeconds: 120,
					Operator:      EdgeCompareGT,
					Threshold:     float64Ptr(1.5),
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/supply_water_temperature",
					Operator: EdgeCompareLTE,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 8.5},
				},
			},
			{
				RuleID:           rule2,
				Title:            "High return/supply boost pump",
				Severity:         "CRITICAL",
				ExecutionTarget:  EdgeTargetCloudAndEdge,
				DependsOnRuleIDs: []string{rule1},
				DependencyGate:   EdgeDependencyGateAllActive,
				QualityPolicy:    EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerSimple,
				Raise: EdgeCondition{
					Kind: EdgeConditionAnd,
					Children: []EdgeCondition{
						{
							Kind:          EdgeConditionWindowAgg,
							Input:         devID + "/supply_water_temperature",
							Agg:           EdgeWindowAggAvg,
							WindowType:    EdgeWindowTumbling,
							WindowSeconds: 60,
							MinSamples:    2,
							Operator:      EdgeCompareGTE,
							Threshold:     float64Ptr(11),
						},
						{
							Kind:       EdgeConditionExpression,
							Expression: "r - s >= 2",
							Inputs: map[string]string{
								"r": devID + "/return_water_temperature",
								"s": devID + "/supply_water_temperature",
							},
						},
					},
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/supply_water_temperature",
					Operator: EdgeCompareLTE,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 9},
				},
				Actions: []EdgeRuleAction{{
					Kind:           EdgeRuleActionProtectionCommand,
					TriggerOn:      EdgeActionTriggerRaise,
					TargetDeviceID: pumpID,
					Capability:     "SET_FREQUENCY",
					Parameters:     map[string]float64{"frequencyHz": 48},
				}},
			},
		},
	})
	if err := edgeCtrl.UpdateBundle(&bundle); err != nil {
		t.Fatalf("UpdateBundle() error = %v", err)
	}
	edgeCtrl.SetOffline(true)

	ctx := context.Background()
	t0 := time.Date(2026, 11, 22, 0, 5, 10, 0, time.UTC)
	pump.SetValue(pumpID+"/run_state", BooleanValue(true), QualityGood, t0)
	pump.SetValue(pumpID+"/flow_rate", DoubleValue(12), QualityGood, t0)
	pump.SetValue(pumpID+"/set_frequency", DoubleValue(30), QualityGood, t0)

	// Cycle 1: 1 sample -> STDDEV must NOT trigger (needs >= 2 samples), Intent wins at 30 Hz.
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(10), QualityGood, t0)
	sensor.SetValue(devID+"/return_water_temperature", DoubleValue(15), QualityGood, t0)
	res1, err := host.RunCycle(ctx, t0)
	if err != nil {
		t.Fatalf("RunCycle(t0) error = %v", err)
	}
	if len(res1.Cycle.Decisions) != 1 || res1.Cycle.Decisions[0].ControllerID != intentCtrl.ID() || res1.Cycle.Decisions[0].Effective.Double != 30 {
		t.Fatalf("cycle 1 unexpected decision = %+v", res1.Cycle.Decisions)
	}
	if evs := edgeCtrl.DrainEvents(); len(evs) != 0 {
		t.Fatalf("expected 0 events on cold start, got %+v", evs)
	}

	// Cycle 2 (+15s, same 60s tumbling bucket): supply rises to 14 -> AVG=(10+14)/2=12 >= 11,
	// STDDEV(10,14)=2 > 1.5! Both Rule 1 and dependent Rule 2 fire; Rule 2 beats Intent (48 vs 30 Hz).
	t1 := t0.Add(15 * time.Second)
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(14), QualityGood, t1)
	sensor.SetValue(devID+"/return_water_temperature", DoubleValue(18), QualityGood, t1)
	res2, err := host.RunCycle(ctx, t1)
	if err != nil {
		t.Fatalf("RunCycle(t1) error = %v", err)
	}
	if len(res2.Cycle.Decisions) != 1 || res2.Cycle.Decisions[0].ControllerID != DefaultEdgeRuleControllerID || res2.Cycle.Decisions[0].Effective.Double != 48 {
		t.Fatalf("cycle 2 protection arbitration failed: decisions=%+v", res2.Cycle.Decisions)
	}
	evs := edgeCtrl.DrainEvents()
	if len(evs) != 2 || evs[0].EventType != EdgeRuleEventRaised || !evs[0].Offline || evs[1].RuleID != rule2 || len(evs[1].TriggeredActions) != 1 {
		t.Fatalf("cycle 2 events unexpected: %+v", evs)
	}

	// Hot bundle swap with unchanged rules preserves active alarm state cleanly.
	if err := edgeCtrl.UpdateBundle(&bundle); err != nil {
		t.Fatalf("hot swap UpdateBundle() error = %v", err)
	}
	if st := edgeCtrl.Status(); st.ActiveRuleCount != 2 || !st.Offline || st.RuleCount != 2 {
		t.Fatalf("expected preserved active rule state after hot swap, got %+v", st)
	}

	// Cycle 3 (+30s): drop supply to 8 -> both rules clear, intent regains control at 30 Hz.
	t2 := t1.Add(30 * time.Second)
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(8), QualityGood, t2)
	res3, err := host.RunCycle(ctx, t2)
	if err != nil {
		t.Fatalf("RunCycle(t2) error = %v", err)
	}
	if len(res3.Cycle.Decisions) != 1 || res3.Cycle.Decisions[0].ControllerID != intentCtrl.ID() || res3.Cycle.Decisions[0].Effective.Double != 30 {
		t.Fatalf("cycle 3 expected intent regain control, got %+v", res3.Cycle.Decisions)
	}
	clearEvs := edgeCtrl.DrainEvents()
	if len(clearEvs) != 2 || clearEvs[0].EventType != EdgeRuleEventCleared || clearEvs[1].EventType != EdgeRuleEventCleared {
		t.Fatalf("expected 2 clear events, got %+v", clearEvs)
	}
}

func TestEdgeRuleEngineHysteresisSlopeDurationAndRepeating(t *testing.T) {
	const (
		siteID = "01900000-0000-7000-8000-000000000030"
		devID  = "01900000-1000-7000-8000-000000000001"
		pumpID = "01900000-1000-7000-8000-000000000002"
		rDur   = "01900000-4000-7000-8000-000000000001"
		rRep   = "01900000-4000-7000-8000-000000000002"
		rSlope = "01900000-4000-7000-8000-000000000003"
	)
	edgeCtrl := NewEdgeRuleController(EdgeRuleControllerConfig{})
	host, sensor, _ := buildEdgeRuleTestHost(t, devID, pumpID, []ControllerBinding{
		{Priority: 15, Controller: edgeCtrl},
	})
	sensor.preserveObservedAt = true
	bundle := mustEdgeBundleDigest(t, EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         siteID,
		BundleRevision: 2,
		Rules: []CompiledEdgeRule{
			{
				RuleID:           rDur,
				Title:            "Duration hysteresis",
				Severity:         "MAJOR",
				ExecutionTarget:  EdgeTargetEdge,
				QualityPolicy:    EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerDuration,
				DurationSeconds:  30,
				Raise: EdgeCondition{
					Kind:      EdgeConditionHysteresis,
					Input:     devID + "/supply_water_temperature",
					Direction: EdgeDirectionAbove,
					Trigger:   float64Ptr(15),
					Reset:     float64Ptr(12),
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/supply_water_temperature",
					Operator: EdgeCompareLTE,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 12},
				},
			},
			{
				RuleID:           rRep,
				Title:            "Repeating p95",
				Severity:         "WARNING",
				ExecutionTarget:  EdgeTargetCloudAndEdge,
				QualityPolicy:    EdgeQualityUsable,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerRepeating,
				RepeatCount:      2,
				Raise: EdgeCondition{
					Kind:          EdgeConditionWindowAgg,
					Input:         devID + "/return_water_temperature",
					Agg:           EdgeWindowAggP95,
					WindowType:    EdgeWindowSliding,
					WindowSeconds: 120,
					MinSamples:    2,
					Operator:      EdgeCompareGTE,
					Threshold:     float64Ptr(18),
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/return_water_temperature",
					Operator: EdgeCompareLT,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 14},
				},
			},
			{
				RuleID:           rSlope,
				Title:            "Rapid slope rise",
				Severity:         "MINOR",
				ExecutionTarget:  EdgeTargetEdge,
				QualityPolicy:    EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerSimple,
				Raise: EdgeCondition{
					Kind:          EdgeConditionSlope,
					Input:         devID + "/supply_water_temperature",
					Operator:      EdgeCompareGTE,
					Delta:         float64Ptr(4),
					WindowSeconds: 60,
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/supply_water_temperature",
					Operator: EdgeCompareLTE,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 12},
				},
			},
		},
	})
	if err := edgeCtrl.UpdateBundle(&bundle); err != nil {
		t.Fatalf("UpdateBundle() error = %v", err)
	}

	ctx := context.Background()
	t0 := time.Date(2026, 11, 22, 0, 10, 0, 0, time.UTC)
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(16), QualityGood, t0)
	sensor.SetValue(devID+"/return_water_temperature", DoubleValue(19), QualitySuspect, t0)
	if _, err := host.RunCycle(ctx, t0); err != nil {
		t.Fatalf("RunCycle(t0) error = %v", err)
	}
	if evs := edgeCtrl.DrainEvents(); len(evs) != 0 {
		t.Fatalf("t0 expected 0 events during warm-up/duration, got %+v", evs)
	}

	// At t0+20s: rRep reaches 2nd sample >= 18 (repeatCount 1 of 2); supply jumps to 20 -> delta=4 -> rSlope fires immediately.
	t1 := t0.Add(20 * time.Second)
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(20), QualityGood, t1)
	sensor.SetValue(devID+"/return_water_temperature", DoubleValue(20), QualitySuspect, t1)
	if _, err := host.RunCycle(ctx, t1); err != nil {
		t.Fatalf("RunCycle(t1) error = %v", err)
	}
	evs := edgeCtrl.DrainEvents()
	if len(evs) != 1 || evs[0].RuleID != rSlope {
		t.Fatalf("t1 expected rSlope event only, got %+v", evs)
	}

	// Repeat poll on t1 without advancing ObservedAt must NOT advance REPEATING count even if Sequence bumps.
	if _, err := host.RunCycle(ctx, t1.Add(time.Second)); err != nil {
		t.Fatalf("RunCycle(t1 repeat) error = %v", err)
	}
	if st := edgeCtrl.Status(); st.ActiveRuleCount != 1 {
		t.Fatalf("expected only rSlope active after duplicate sample poll, got %+v", st)
	}

	// At t0+35s: fresh return reading arrives at t2 -> rDur meets 30s duration & rRep hits repeatCount 2!
	t2 := t0.Add(35 * time.Second)
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(16), QualityGood, t2)
	sensor.SetValue(devID+"/return_water_temperature", DoubleValue(20), QualitySuspect, t2)
	if _, err := host.RunCycle(ctx, t2); err != nil {
		t.Fatalf("RunCycle(t2) error = %v", err)
	}
	evs = edgeCtrl.DrainEvents()
	if len(evs) != 2 {
		t.Fatalf("t2 expected rDur and rRep raise events, got %+v", evs)
	}
	if st := edgeCtrl.Status(); st.ActiveRuleCount != 3 {
		t.Fatalf("expected 3 active rules, got %+v", st)
	}
}

func TestEdgeRuleEngineUpdateBundleCloneMonotonicSeqPeekAckAndFailClosedCommand(t *testing.T) {
	const (
		siteID = "01900000-0000-7000-8000-000000000040"
		devID  = "01900000-1000-7000-8000-000000000001"
		pumpID = "01900000-1000-7000-8000-000000000002"
		r1     = "01900000-4000-7000-8000-000000000011"
		r2     = "01900000-4000-7000-8000-000000000012"
	)
	resolverCalls := 0
	edgeCtrl := NewEdgeRuleController(EdgeRuleControllerConfig{
		CommandChannelResolver: func(deviceID, pointCode, capability string) (string, DataType, string, bool) {
			resolverCalls++
			return deviceID + "/set_frequency", DataTypeDouble, "frequencyHz", true
		},
	})
	host, sensor, pump := buildEdgeRuleTestHost(t, devID, pumpID, []ControllerBinding{
		{Priority: 15, Controller: edgeCtrl},
	})

	// Rule with mismatched ParameterKey ("wrongKey" instead of "frequencyHz") must fail closed
	// rather than falling back to unvalidated TargetPointCode.
	bundle := mustEdgeBundleDigest(t, EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         siteID,
		BundleRevision: 1,
		Rules: []CompiledEdgeRule{
			{
				RuleID:           r1,
				Title:            "Fail-closed wrong param",
				Severity:         "CRITICAL",
				ExecutionTarget:  EdgeTargetEdge,
				QualityPolicy:    EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerSimple,
				Raise: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/supply_water_temperature",
					Operator: EdgeCompareGT,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 10},
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/supply_water_temperature",
					Operator: EdgeCompareLTE,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 8},
				},
				Actions: []EdgeRuleAction{{
					Kind:            EdgeRuleActionProtectionCommand,
					TriggerOn:       EdgeActionTriggerRaise,
					TargetDeviceID:  pumpID,
					TargetPointCode: "start",
					Capability:      "SET_FREQUENCY",
					Parameters:      map[string]float64{"wrongKey": 40},
				}},
			},
			{
				RuleID:           r2,
				Title:            "Active rule removed on update",
				Severity:         "MAJOR",
				ExecutionTarget:  EdgeTargetCloudAndEdge,
				QualityPolicy:    EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      EdgeTriggerSimple,
				Raise: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/return_water_temperature",
					Operator: EdgeCompareGT,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 10},
				},
				Clear: EdgeCondition{
					Kind:     EdgeConditionCompare,
					Input:    devID + "/return_water_temperature",
					Operator: EdgeCompareLTE,
					Value:    &EdgeTypedValue{Type: EdgeValueNumber, Number: 8},
				},
			},
		},
	})
	originalBundle := bundle.Clone()
	if err := edgeCtrl.UpdateBundle(&bundle); err != nil {
		t.Fatalf("UpdateBundle() error = %v", err)
	}
	// Mutate caller's bundle after UpdateBundle to verify no slice/map aliasing.
	bundle.Rules[0].Title = "Mutated caller title"

	t0 := time.Date(2026, 11, 22, 0, 20, 0, 0, time.UTC)
	sensor.SetValue(devID+"/supply_water_temperature", DoubleValue(15), QualityGood, t0)
	sensor.SetValue(devID+"/return_water_temperature", DoubleValue(15), QualityGood, t0)
	pump.SetValue(pumpID+"/run_state", BooleanValue(false), QualityGood, t0)
	pump.SetValue(pumpID+"/set_frequency", DoubleValue(35), QualityGood, t0)

	res, err := host.RunCycle(context.Background(), t0)
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}
	if resolverCalls == 0 || len(res.Cycle.Decisions) != 0 {
		t.Fatalf("expected fail-closed command resolver to emit 0 actuator requests, decisions=%+v", res.Cycle.Decisions)
	}

	// PeekEvents + partial AckEvents must keep un-acked tail.
	peeked := edgeCtrl.PeekEvents()
	if len(peeked) != 2 {
		t.Fatalf("expected 2 peeked raise events, got %+v", peeked)
	}
	if peeked[0].RuleID == r1 && peeked[0].Title != "Fail-closed wrong param" {
		t.Fatalf("caller mutation leaked into controller rule title: %q", peeked[0].Title)
	}
	edgeCtrl.AckEvents(1)
	if rem := edgeCtrl.PeekEvents(); len(rem) != 1 {
		t.Fatalf("expected 1 remaining event after AckEvents(1), got %+v", rem)
	}
	edgeCtrl.AckEvents(1)

	// Clearing bundle must emit RULE_REMOVED clear events and retain monotonic EventID sequences.
	if err := edgeCtrl.UpdateBundle(nil); err != nil {
		t.Fatalf("UpdateBundle(nil) error = %v", err)
	}
	clearEvs := edgeCtrl.DrainEvents()
	if len(clearEvs) != 2 || clearEvs[0].EventType != EdgeRuleEventCleared || clearEvs[0].Reason != "RULE_REMOVED" || !strings.HasSuffix(clearEvs[0].EventID, ":2") {
		t.Fatalf("expected 2 RULE_REMOVED clear events with seq 2, got %+v", clearEvs)
	}

	// Re-apply bundle and re-trigger -> EventID must continue at :3, never colliding with :1.
	if err := edgeCtrl.UpdateBundle(originalBundle); err != nil {
		t.Fatalf("UpdateBundle(re-apply) error = %v", err)
	}
	if _, err := host.RunCycle(context.Background(), t0.Add(time.Second)); err != nil {
		t.Fatalf("RunCycle() re-trigger error = %v", err)
	}
	reEvs := edgeCtrl.DrainEvents()
	if len(reEvs) != 2 || !strings.HasSuffix(reEvs[0].EventID, ":3") {
		t.Fatalf("expected monotonic EventID ending in :3, got %+v", reEvs)
	}
}
