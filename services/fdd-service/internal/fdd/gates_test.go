package fdd

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// Songyaxuan's registry as of 2026-10: chillers A–D bound to their meters, no rated power or
// capacity yet, and the chilled-water pump meter bound to no pump.
const (
	songyaxuanTenant = "018f3d00-0000-7000-8000-000000000001"
	songyaxuanSite   = "01a14000-1000-7000-8000-000000000001"
	songyaxuanLoop   = "01a14000-4000-7000-8000-000000000009"
)

func songyaxuanTopology() Topology {
	var topology Topology
	for _, meter := range []string{"03", "04", "05", "06"} {
		topology.Chillers = append(topology.Chillers, GateAsset{AssetID: "01a14000-3000-7000-8000-0000000000" + meter, DeviceIDs: []string{"01a14000-4000-7000-8000-0000000000" + meter}})
	}
	return topology
}

// songyaxuanRule is the Tenant's revision 1: 3.5 K over 15 minutes, now under the default gates.
func songyaxuanRule() RuleRevision {
	return RuleRevision{ID: "songyaxuan-rev-1", Revision: 1, MinimumDeltaTC: 3.5, WindowMinutes: 15, OperatingGates: testGates()}
}

func replayFixture(t *testing.T, name string) []Sample {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	samples, err := decodeSamples(raw)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

// Replays of Songyaxuan's history (read-only export, 2026-10-16): what the gated rule concludes
// from the same rows FDD reads from Telemetry history.
func TestReplayOfSongyaxuanHistory(t *testing.T) {
	for _, replay := range []struct {
		name, fixture string
		at            time.Time
		threshold     float64
		status        string
		operating     int
		low           int
	}{
		// Chillers C and D run all window at ~0.8 K: below 3.5 K every minute.
		{"a running plant at 0.8 K is low", "songyaxuan-low-delta-t-2026-10-16T0030Z.jsonl", time.Date(2026, 10, 16, 0, 30, 0, 0, time.UTC), 3.5, statusLow, 15, 15},
		// The plant stops six minutes before the window ends: 9 of 15 minutes is under 70%.
		{"a plant shutting down is not judged", "songyaxuan-shutting-down-2026-10-16T0600Z.jsonl", time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC), 3.5, StatusNotOperating, 9, 9},
		// At high load the loop holds ~2.1 K: clear against a 1.5 K threshold, low against 3.5 K.
		{"high load against 1.5 K is clear", "songyaxuan-high-load-2026-10-16T1030Z.jsonl", time.Date(2026, 10, 16, 10, 30, 0, 0, time.UTC), 1.5, StatusClear, 15, 0},
		{"high load against 3.5 K is low", "songyaxuan-high-load-2026-10-16T1030Z.jsonl", time.Date(2026, 10, 16, 10, 30, 0, 0, time.UTC), 3.5, statusLow, 15, 15},
	} {
		t.Run(replay.name, func(t *testing.T) {
			rule := songyaxuanRule()
			rule.MinimumDeltaTC = replay.threshold
			window, err := AssessWindow(rule, songyaxuanTopology(), songyaxuanLoop, replayFixture(t, replay.fixture), replay.at)
			if err != nil {
				t.Fatal(err)
			}
			if window.Status != replay.status || window.OperatingMinutes != replay.operating || window.LowMinutes != replay.low || window.TemperatureMinutes != 15 {
				t.Fatalf("window=%+v", window)
			}
			// Without rated power the chillers are judged by their run state; without rated capacity
			// the load ratio cannot apply; with no pump bound the pump gate cannot either.
			want := []GateOutcome{
				{Gate: ChillerRunningGate, Applied: true, Bases: []string{runningBasisRunState}, PassedMinutes: replay.operating},
				{Gate: LoadRatioGate, SkippedReason: SkippedNoRatedCapacity},
				{Gate: ChilledWaterPumpRunningGate, SkippedReason: SkippedNoPumpMapped},
			}
			if !reflect.DeepEqual(window.Gates, want) {
				t.Fatalf("gates=%+v", window.Gates)
			}
		})
	}
}

// With rated values in the registry the same window is judged by power and load.
func TestRatedValuesTurnOnThePowerAndLoadGates(t *testing.T) {
	at := time.Date(2026, 10, 16, 10, 30, 0, 0, time.UTC)
	samples := replayFixture(t, "songyaxuan-high-load-2026-10-16T1030Z.jsonl")
	rated := func(power, capacity float64) Topology {
		topology := songyaxuanTopology()
		for index := range topology.Chillers {
			topology.Chillers[index].RatedPowerKW, topology.Chillers[index].RatedCapacityKW = power, capacity
		}
		return topology
	}
	window, err := AssessWindow(songyaxuanRule(), rated(200, 1000), songyaxuanLoop, samples, at)
	if err != nil || window.Status != statusLow || window.Gates[0].Bases[0] != runningBasisRatedPower || !window.Gates[1].Applied || window.Gates[1].PassedMinutes != 15 {
		t.Fatalf("rated plant window=%+v err=%v", window, err)
	}
	// Rated far above what the chillers draw, the chillers count as idle: 10% of 5 MW is above them.
	window, err = AssessWindow(songyaxuanRule(), rated(5000, 20000), songyaxuanLoop, samples, at)
	if err != nil || window.Status != StatusNotOperating || window.Gates[0].PassedMinutes != 0 {
		t.Fatalf("oversized rating window=%+v err=%v", window, err)
	}
	// Rated capacity so large that the load is under 15% of it fails the load ratio alone.
	window, err = AssessWindow(songyaxuanRule(), rated(200, 100000), songyaxuanLoop, samples, at)
	if err != nil || window.Status != StatusNotOperating || window.Gates[0].PassedMinutes != 15 || window.Gates[1].PassedMinutes != 0 {
		t.Fatalf("light load window=%+v err=%v", window, err)
	}
}

// A pump gate applies only where a pump is bound, and then a stopped pump stops the window.
func TestThePumpGateAppliesOnlyWhereAPumpIsBound(t *testing.T) {
	request := validEvaluation()
	pumpMeter := "01990000-6100-7000-8000-000000000003"
	topology := testTopology()
	topology.Pumps = []GateAsset{{AssetID: "01990000-6000-7000-8000-000000000003", DeviceIDs: []string{pumpMeter}}}
	samples := validSamples(request)
	from, _ := WindowMinutes(testRule, request.At)
	for index := 0; index < 15; index++ {
		samples = append(samples, Sample{DeviceID: pumpMeter, Key: runStateKey, Minute: from.Add(time.Duration(index) * time.Minute), PointType: "TELEMETRY", ValueType: "STRING", Quality: "GOOD", Text: "STOPPED"})
	}
	window, err := AssessWindow(testRule, topology, request.DeviceID, samples, request.At)
	if err != nil || window.Status != StatusNotOperating || !window.Gates[2].Applied {
		t.Fatalf("stopped pump window=%+v err=%v", window, err)
	}
	window, err = AssessWindow(testRule, testTopology(), request.DeviceID, samples, request.At)
	if err != nil || window.Status != statusLow || window.Gates[2].SkippedReason != SkippedNoPumpMapped {
		t.Fatalf("unbound pump window=%+v err=%v", window, err)
	}
}

// Low minutes under 70% of the operating minutes clear; a five-minute power sample stands for the
// minutes after it up to the gate hold.
func TestShareAndHoldDecideTheWindow(t *testing.T) {
	request := validEvaluation()
	minutes := steadyLoop()
	for index := 0; index < 5; index++ {
		minutes[index].ret = 13
	}
	window, err := AssessWindow(testRule, testTopology(), request.DeviceID, loopSamples(request, minutes), request.At)
	if err != nil || window.Status != StatusClear || window.LowMinutes != 10 {
		t.Fatalf("10 of 15 low window=%+v err=%v", window, err)
	}
	samples := validSamples(request)
	kept := samples[:0]
	for _, sample := range samples {
		if sample.Key != runStateKey || sample.Minute.Minute()%5 == 0 {
			kept = append(kept, sample)
		}
	}
	window, err = AssessWindow(testRule, testTopology(), request.DeviceID, kept, request.At)
	if err != nil || window.Status != statusLow || window.OperatingMinutes != 15 {
		t.Fatalf("five-minute run state window=%+v err=%v", window, err)
	}
	rule := testRule
	rule.OperatingGates.GateHoldMinutes = 0
	window, err = AssessWindow(rule, testTopology(), request.DeviceID, kept, request.At)
	if err != nil || window.Status != StatusNotOperating || window.OperatingMinutes != 3 {
		t.Fatalf("without a hold window=%+v err=%v", window, err)
	}
}

func TestOperatingGatesAreDecodedStrictly(t *testing.T) {
	gates, err := ParseOperatingGates([]byte(`{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10,"chillerRunning":{"minimumRatedPowerFraction":0.1},"loadRatio":{"minimum":0.15},"chilledWaterPumpRunning":{}}`))
	if err != nil || !reflect.DeepEqual(gates, testGates()) {
		t.Fatalf("default gates=%+v err=%v", gates, err)
	}
	for name, raw := range map[string]string{
		"unknown field":              `{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10,"outdoorAir":{}}`,
		"share above one":            `{"minimumOperatingShare":1.5,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10}`,
		"load ratio without chiller": `{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10,"loadRatio":{"minimum":0.15}}`,
		"hold beyond an hour":        `{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":90}`,
		"trailing JSON":              `{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10}{}`,
	} {
		if _, err := ParseOperatingGates([]byte(raw)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
