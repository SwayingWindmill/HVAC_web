package telemetry

import (
	"math"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/nameplate"
)

// Patterns replayed from the Songyaxuan site, 25 Oct–1 Nov (Beijing): outdoor
// temperature stuck, room temperature stepping and spiking, a repeated 38.4
// communication value, and a supply temperature that should move only while a
// chiller runs.

func TestOutdoorFlatlineAfterDaysStuck(t *testing.T) {
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	checks := &QualityChecks{Flatline: 3 * time.Hour, Sentinels: []float64{38.4}}
	held := PriorNumeric{Value: 18.7, SampledAt: start, UnchangedSince: start}

	short := AssessPointQuality(18.7, start.Add(2*time.Hour), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held})
	if len(short) != 0 {
		t.Fatalf("2 hours at 18.7 ℃ is not yet stuck: %v", short)
	}
	week := AssessPointQuality(18.7, start.Add(7*24*time.Hour), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held})
	if !sameReasons(week, QualityReasonFlatline) {
		t.Fatalf("7 days at 18.7 ℃ = %v", week)
	}
	// An earlier stuck value (22.1) and a 0 reading are the same check, not special cases.
	if reasons := AssessPointQuality(22.1, start.Add(8*time.Hour), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &PriorNumeric{Value: 22.1, SampledAt: start, UnchangedSince: start}}); !sameReasons(reasons, QualityReasonFlatline) {
		t.Fatalf("stuck 22.1 = %v", reasons)
	}
}

func TestSupplyFlatlineDependsOnChillerRunning(t *testing.T) {
	start := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	checks := &QualityChecks{Flatline: 24 * time.Hour, FlatlineRunning: 30 * time.Minute}
	held := PriorNumeric{Value: 6.4, SampledAt: start, UnchangedSince: start}
	at := start.Add(40 * time.Minute)

	running := AssessPointQuality(6.4, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held, Running: true})
	if !sameReasons(running, QualityReasonFlatline) {
		t.Fatalf("supply stuck 40 min while a chiller runs = %v", running)
	}
	idle := AssessPointQuality(6.4, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held, Running: false})
	if len(idle) != 0 {
		t.Fatalf("the same hold while the plant is off is not suspicious yet: %v", idle)
	}
	// Pumps can circulate for hours with chillers A and B idle. That alone is not a flat supply.
	pumpsOnly := AssessPointQuality(6.4, start.Add(604*time.Minute), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held, Running: false})
	if len(pumpsOnly) != 0 {
		t.Fatalf("604 min idle = %v", pumpsOnly)
	}
	day := AssessPointQuality(6.4, start.Add(25*time.Hour), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held, Running: false})
	if !sameReasons(day, QualityReasonFlatline) {
		t.Fatalf("a full day stuck while off = %v", day)
	}
}

func TestRoomStepSpikeAndHold(t *testing.T) {
	start := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	step := 1.5
	checks := &QualityChecks{MaxStep: &step, MaxStepWindow: 10 * time.Minute, Sentinels: []float64{38.4}, Flatline: 6 * time.Hour}
	previous := PriorNumeric{Value: 24.2, SampledAt: start, UnchangedSince: start}

	if reasons := AssessPointQuality(24.6, start.Add(time.Minute), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &previous}); len(reasons) != 0 {
		t.Fatalf("a tenth-of-a-degree step is the sensor's normal stair: %v", reasons)
	}
	if reasons := AssessPointQuality(176.8, start.Add(time.Minute), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &previous}); !sameReasons(reasons, QualityReasonSpike) {
		t.Fatalf("176.8 spike = %v", reasons)
	}
	for _, value := range []float64{74.9, 75.7, 7.2} {
		if reasons := AssessPointQuality(value, start.Add(2*time.Minute), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &previous}); !sameReasons(reasons, QualityReasonSpike) {
			t.Fatalf("%v = %v", value, reasons)
		}
	}
	held := PriorNumeric{Value: 36.5, SampledAt: start, UnchangedSince: start}
	if reasons := AssessPointQuality(36.5, start.Add(849*time.Minute), checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &held}); !sameReasons(reasons, QualityReasonFlatline) {
		t.Fatalf("36.5 held 849 min = %v", reasons)
	}
}

func TestSentinel38Point4OnAnyChannel(t *testing.T) {
	checks := &QualityChecks{Sentinels: []float64{38.4}}
	at := time.Date(2026, 10, 27, 4, 0, 0, 0, time.UTC)
	// 38.4 sits inside a condenser range, so the sentinel is what catches it.
	for _, value := range []float64{38.4, 38.40, 38.36} {
		if reasons := AssessPointQuality(value, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true}); !sameReasons(reasons, QualityReasonSentinel) {
			t.Fatalf("%v = %v", value, reasons)
		}
	}
	if reasons := AssessPointQuality(38.5, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true}); len(reasons) != 0 {
		t.Fatalf("38.5 is not the communication value: %v", reasons)
	}
	// A gap longer than the step window is not a spike: the plant may have moved.
	step := 2.5
	windowed := &QualityChecks{MaxStep: &step, MaxStepWindow: 10 * time.Minute}
	previous := PriorNumeric{Value: 15.1, SampledAt: at.Add(-2 * time.Hour)}
	if reasons := AssessPointQuality(0.9, at, windowed, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &previous}); len(reasons) != 0 {
		t.Fatalf("a change across a two-hour gap is not a single-sample spike: %v", reasons)
	}
	previous.SampledAt = at.Add(-time.Minute)
	if reasons := AssessPointQuality(19.0, at, windowed, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &previous}); !sameReasons(reasons, QualityReasonSpike) {
		t.Fatalf("15.1 to 19.0 in one minute = %v", reasons)
	}
}

func TestDerivedCoolingUsesInputQuality(t *testing.T) {
	checks := &QualityChecks{Inputs: []PointRef{{DeviceID: "temperature", Key: "t18"}}}
	at := time.Date(2026, 10, 28, 5, 0, 0, 0, time.UTC)
	if reasons := AssessPointQuality(420, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, InputsUntrusted: true}); !sameReasons(reasons, QualityReasonDerivedInput) {
		t.Fatalf("cooling while supply is untrusted = %v", reasons)
	}
	if reasons := AssessPointQuality(1200, at, checks, PointQualityInput{PointType: "COUNTER", Live: true, InputsUntrusted: true}); !sameReasons(reasons, QualityReasonDerivedInput) {
		t.Fatalf("cooling counter = %v", reasons)
	}
	// A counter is not a temperature: 38.4 and a flat total are not sensor faults.
	checks.Sentinels = []float64{38.4}
	checks.Flatline = time.Hour
	previous := PriorNumeric{Value: 38.4, SampledAt: at.Add(-2 * time.Hour), UnchangedSince: at.Add(-2 * time.Hour)}
	if reasons := AssessPointQuality(38.4, at, checks, PointQualityInput{PointType: "COUNTER", Live: true, Previous: &previous}); len(reasons) != 0 {
		t.Fatalf("counter must ignore sentinel and flatline: %v", reasons)
	}
	if reasons := AssessPointQuality(410, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true}); len(reasons) != 0 {
		t.Fatalf("trusted inputs = %v", reasons)
	}
}

func TestHistoricalReplaySkipsChecksThatNeedNeighbors(t *testing.T) {
	step := 1.0
	checks := &QualityChecks{MaxStep: &step, Flatline: time.Hour, Inputs: []PointRef{{DeviceID: "d", Key: "t18"}}, Sentinels: []float64{38.4}}
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	previous := PriorNumeric{Value: 6, SampledAt: at.Add(-time.Minute), UnchangedSince: at.Add(-time.Hour)}
	reasons := AssessPointQuality(38.4, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: false, Previous: &previous, InputsUntrusted: true})
	if !sameReasons(reasons, QualityReasonSentinel) {
		t.Fatalf("replay still recognises a sentinel, and skips spike, flatline and derived input: %v", reasons)
	}
}

func TestHealthScoresAndStaleness(t *testing.T) {
	bad := HealthFor(QualityInvalid, []QualityReason{QualityReasonOutOfRange})
	if bad.Status != HealthBad || bad.Score != 0 {
		t.Fatalf("out of range = %+v", bad)
	}
	suspect := HealthFor(QualityPartial, []QualityReason{QualityReasonSpike})
	if suspect.Status != HealthSuspect || suspect.Score != 40 {
		t.Fatalf("spike = %+v", suspect)
	}
	healthy := HealthFor(QualityGood, nil)
	if healthy.Status != HealthHealthy || healthy.Score != 100 {
		t.Fatalf("good = %+v", healthy)
	}
	stale := ApplyFreshness(healthy, false)
	if stale.Status != HealthStale || stale.Score != 20 {
		t.Fatalf("freshness = %+v", stale)
	}
	// A suspect sample that is also old stays suspect.
	if got := ApplyFreshness(suspect, false); got.Status != HealthSuspect {
		t.Fatalf("suspect must not be hidden by staleness: %+v", got)
	}
}

func TestUnchangedSinceStaysWhileTheValueHolds(t *testing.T) {
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	previous := &PriorNumeric{Value: 18.7, SampledAt: start.Add(time.Minute), UnchangedSince: start}
	if got := UnchangedSinceOf(18.7, start.Add(time.Hour), previous); !got.Equal(start) {
		t.Fatalf("held since %s", got)
	}
	if got := UnchangedSinceOf(18.8, start.Add(time.Hour), previous); !got.Equal(start.Add(time.Hour)) {
		t.Fatalf("moved at %s", got)
	}
}

func TestParseQualityChecks(t *testing.T) {
	checks, err := parseQualityChecks([]byte(`{"maxStep":2.5,"maxStepWindowSeconds":600,"sentinels":[38.4],"flatlineSeconds":86400,"flatlineRunningSeconds":1800,"running":[{"deviceId":"01a14000-4000-7000-8000-000000000003","key":"run_state","equals":"RUNNING"}],"inputs":[{"deviceId":"01a14000-4000-7000-8000-000000000001","key":"t18"}]}`))
	if err != nil || checks == nil || checks.MaxStep == nil || *checks.MaxStep != 2.5 || checks.MaxStepWindow != 10*time.Minute || checks.Flatline != 24*time.Hour || checks.FlatlineRunning != 30*time.Minute || len(checks.Running) != 1 || checks.Running[0].Equals != "RUNNING" || len(checks.Inputs) != 1 {
		t.Fatalf("checks=%+v err=%v", checks, err)
	}
	empty, err := parseQualityChecks([]byte(`{}`))
	if err != nil || empty != nil {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	copiedOnly, err := parseQualityChecks([]byte(`{"copied":true}`))
	if err != nil || copiedOnly == nil || !copiedOnly.Copied {
		t.Fatalf("copiedOnly=%+v err=%v", copiedOnly, err)
	}
	notCopied, err := parseQualityChecks([]byte(`{"copied":false}`))
	if err != nil || notCopied != nil {
		t.Fatalf("notCopied=%+v err=%v", notCopied, err)
	}
	if _, err := parseQualityChecks([]byte(`[`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestChillerPowerAboveTheNameplateIsSuspectAndAReplayIsNot(t *testing.T) {
	fraction := nameplate.DefaultPowerAboveRatedFraction
	checks := &QualityChecks{Physics: []PhysicsRule{{Kind: nameplate.KindPowerNameplate, Fraction: &fraction, Rated: 127.5}}}
	at := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	reasons := AssessPointQuality(191.8, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true})
	if !sameReasons(reasons, QualityReasonPhysicsPower) {
		t.Fatalf("live=%v", reasons)
	}
	health := HealthFor(QualityPartial, reasons)
	if health.Status != HealthSuspect || health.Score != 40 || !sameReasons(health.Reasons, QualityReasonPhysicsPower) {
		t.Fatalf("health=%+v", health)
	}
	if replay := AssessPointQuality(191.8, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: false}); len(replay) != 0 {
		t.Fatalf("replay=%v", replay)
	}
	if inside := AssessPointQuality(140, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true}); len(inside) != 0 {
		t.Fatalf("inside the margin=%v", inside)
	}
}

func TestWetBulbAboveCondenserWaterNeedsThePeersAndTheTower(t *testing.T) {
	margin := nameplate.DefaultWetBulbCondenserMarginK
	checks := &QualityChecks{Physics: []PhysicsRule{{
		Kind: nameplate.KindWetBulbCondenser, MarginK: &margin,
		Peers: []PhysicsPeer{
			{Role: "condenserEntering", DeviceID: "temp", Key: "t5"},
			{Role: "condenserEntering", DeviceID: "temp", Key: "t9"},
		},
		Running: []OperatingExpect{{DeviceID: "meter", Key: "run_state", Equals: "RUNNING"}},
	}}}
	number := func(value float64) *float64 { return &value }
	at := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	peers := map[string]PeerSample{
		"temp/t5":         {Number: number(26), Good: true},
		"temp/t9":         {Number: number(27), Good: true},
		"meter/run_state": {Text: "RUNNING", Good: true},
	}
	reasons := AssessPointQuality(32, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers})
	if !sameReasons(reasons, QualityReasonPhysicsWetBulbCondenser) {
		t.Fatalf("t15 above both condenser entering readings: %v", reasons)
	}
	peers["meter/run_state"] = PeerSample{Text: "STOPPED", Good: true}
	if stopped := AssessPointQuality(32, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers}); len(stopped) != 0 {
		t.Fatalf("plant off: %v", stopped)
	}
	peers["meter/run_state"] = PeerSample{Text: "RUNNING", Good: true}
	peers["temp/t9"] = PeerSample{Number: number(27), Good: false}
	if partial := AssessPointQuality(32, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers}); len(partial) != 0 {
		t.Fatalf("a suspect condenser reading must not be used: %v", partial)
	}
}

func TestHeaderCoolingAtPointNineKelvinIsLowConfidence(t *testing.T) {
	// Replay of the Songyaxuan header: 489 m³/h, return 7.9 °C, supply 7.0 °C, ΔT 0.9 K.
	supply, ret, flow, power := 7.0, 7.9, 489.0, 120.0
	checks := &QualityChecks{Uncertainty: headerUncertainty()}
	peers := map[string]PeerSample{
		"temp/t18":    {Number: &supply, Good: true},
		"temp/t16":    {Number: &ret, Good: true},
		"flow/flow":   {Number: &flow, Good: true},
		"meter/power": {Number: &power, Good: true},
	}
	value := 1.163 * flow * 0.9
	at := time.Date(2026, 10, 27, 6, 0, 0, 0, time.UTC)
	reasons := AssessPointQuality(value, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers})
	if !sameReasons(reasons, QualityReasonLowConfidence) {
		t.Fatalf("0.9 K header = %v", reasons)
	}
	measured, ok := AssessMeasurement(value, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers})
	if !ok || !measured.LowConfidence || !measured.HasNumbers || measured.Relative < 0.155 || measured.Relative > 0.165 || measured.COPRelative == nil {
		t.Fatalf("%+v ok=%v", measured, ok)
	}
	health := HealthFor(QualityPartial, reasons)
	if health.Status != HealthSuspect || health.Score != 40 {
		t.Fatalf("health %+v", health)
	}
	// A chiller at 2.8 K, and a replay that is not live, stay usable.
	supplyWide, returnWide := 7.0, 9.8
	wide := AssessPointQuality(1.163*flow*2.8, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: map[string]PeerSample{
		"temp/t18": {Number: &supplyWide, Good: true}, "temp/t16": {Number: &returnWide, Good: true},
		"flow/flow": {Number: &flow, Good: true}, "meter/power": {Number: &power, Good: true},
	}})
	if len(wide) != 0 {
		t.Fatalf("2.8 K = %v", wide)
	}
	if replay := AssessPointQuality(value, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: false, Peers: peers}); len(replay) != 0 {
		t.Fatalf("history replay = %v", replay)
	}
	if stopped := AssessPointQuality(0, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers}); len(stopped) != 0 {
		t.Fatalf("chillers off = %v", stopped)
	}
}

func headerUncertainty() *UncertaintyCheck {
	return &UncertaintyCheck{
		Kind: "cooling", SupplyAccuracyK: 0.1, ReturnAccuracyK: 0.1,
		FlowAccuracyFraction: 0.02, PowerAccuracyFraction: 0.01, PropertyFraction: 0.005, LowConfidenceRelative: 0.10,
		Supply: PointRef{DeviceID: "temp", Key: "t18"}, Return: PointRef{DeviceID: "temp", Key: "t16"},
		Flow: &PointRef{DeviceID: "flow", Key: "flow"}, Power: &PointRef{DeviceID: "meter", Key: "power"},
	}
}

func sameReasons(got []QualityReason, want ...QualityReason) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestSentinelToleranceBoundary(t *testing.T) {
	if math.Abs(38.45-38.4) > sentinelTolerance {
		t.Fatal("tolerance setup")
	}
}

func TestParseStabilizeQualityChecks(t *testing.T) {
	checks, err := parseQualityChecks([]byte(`{
		"flatlineRunningSeconds": 1800,
		"flatlineStabilizeSeconds": 600,
		"physics": [{
			"kind": "CW_DIRECTION",
			"epsilonK": 0.05,
			"stabilizeSeconds": 600,
			"peers": [{"role": "entering", "deviceId": "temp", "key": "t2"}],
			"running": [{"deviceId": "meter", "key": "run_state", "equals": "RUNNING"}]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseQualityChecks: %v", err)
	}
	if checks == nil || checks.FlatlineStabilize != 10*time.Minute || len(checks.Physics) != 1 || checks.Physics[0].Stabilize != 10*time.Minute {
		t.Fatalf("unexpected parsed checks: %+v", checks)
	}
}

func TestMorningStartupStabilizeWindowSkipsPhysicsChecksUntilSettled(t *testing.T) {
	started := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	epsilon := nameplate.DefaultDirectionEpsilonK
	number := func(v float64) *float64 { return &v }
	checks := &QualityChecks{
		Physics: []PhysicsRule{
			{
				Kind:      nameplate.KindCondenserDirection,
				EpsilonK:  &epsilon,
				Stabilize: 10 * time.Minute,
				Peers:     []PhysicsPeer{{Role: "entering", DeviceID: "temp", Key: "cw_in"}},
				Running:   []OperatingExpect{{DeviceID: "chiller", Key: "run_state", Equals: "RUNNING"}},
			},
			{
				Kind:      nameplate.KindChilledDirection,
				EpsilonK:  &epsilon,
				Stabilize: 10 * time.Minute,
				Peers:     []PhysicsPeer{{Role: "return", DeviceID: "temp", Key: "chw_ret"}},
				Running:   []OperatingExpect{{DeviceID: "chiller", Key: "run_state", Equals: "RUNNING"}},
			},
		},
	}

	at2m := started.Add(2 * time.Minute)
	peers2m := map[string]PeerSample{
		"temp/cw_in":        {Number: number(28.0), Good: true, SampledAt: at2m},
		"temp/chw_ret":      {Number: number(11.0), Good: true, SampledAt: at2m},
		"chiller/run_state": {Text: "RUNNING", Good: true, SampledAt: at2m, UnchangedSince: &started},
	}
	// Reversed temperatures at t=2m (cw_out 27 < cw_in 28, chw_sup 12 > chw_ret 11) must skip during pull-down.
	if reasons := AssessPointQuality(27.0, at2m, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers2m}); len(reasons) != 0 {
		t.Fatalf("t=2m pull-down should skip physics checks, got %v", reasons)
	}

	at11m := started.Add(11 * time.Minute)
	peers11m := map[string]PeerSample{
		"temp/cw_in":        {Number: number(28.0), Good: true, SampledAt: at11m},
		"temp/chw_ret":      {Number: number(11.0), Good: true, SampledAt: at11m},
		"chiller/run_state": {Text: "RUNNING", Good: true, SampledAt: at11m, UnchangedSince: &started},
	}
	// After 10m stabilization, reversed cw_out (27 < 28) fires cw_direction and reversed chw_sup (12 > 11) fires chw_direction.
	cwReasons := AssessPointQuality(27.0, at11m, &QualityChecks{Physics: []PhysicsRule{checks.Physics[0]}}, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers11m})
	if !sameReasons(cwReasons, QualityReasonPhysicsCwDirection) {
		t.Fatalf("t=11m cw_direction: got %v", cwReasons)
	}
	chwReasons := AssessPointQuality(12.0, at11m, &QualityChecks{Physics: []PhysicsRule{checks.Physics[1]}}, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: peers11m})
	if !sameReasons(chwReasons, QualityReasonPhysicsChwDirection) {
		t.Fatalf("t=11m chw_direction: got %v", chwReasons)
	}

	// Rule without its own Running list falls back to input.Running / input.RunningSince when Stabilize > 0.
	fallbackChecks := &QualityChecks{
		Physics: []PhysicsRule{{
			Kind:      nameplate.KindCondenserDirection,
			EpsilonK:  &epsilon,
			Stabilize: 10 * time.Minute,
			Peers:     []PhysicsPeer{{Role: "entering", DeviceID: "temp", Key: "cw_in"}},
		}},
	}
	if reasons := AssessPointQuality(27.0, at2m, fallbackChecks, PointQualityInput{PointType: "TELEMETRY", Live: true, Running: true, RunningSince: &started, Peers: peers2m}); len(reasons) != 0 {
		t.Fatalf("fallback RunningSince at t=2m should skip, got %v", reasons)
	}
	if reasons := AssessPointQuality(27.0, at11m, fallbackChecks, PointQualityInput{PointType: "TELEMETRY", Live: true, Running: true, RunningSince: &started, Peers: peers11m}); !sameReasons(reasons, QualityReasonPhysicsCwDirection) {
		t.Fatalf("fallback RunningSince at t=11m should fire, got %v", reasons)
	}

	// Among multiple running peers, the latest start transition governs the stabilization window.
	multiRuleChecks := &QualityChecks{
		Physics: []PhysicsRule{{
			Kind:      nameplate.KindCondenserDirection,
			EpsilonK:  &epsilon,
			Stabilize: 10 * time.Minute,
			Peers:     []PhysicsPeer{{Role: "entering", DeviceID: "temp", Key: "cw_in"}},
			Running: []OperatingExpect{
				{DeviceID: "chiller-a", Key: "run_state", Equals: "RUNNING"},
				{DeviceID: "chiller-b", Key: "run_state", Equals: "RUNNING"},
			},
		}},
	}
	chillerAStart := at11m.Add(-30 * time.Minute)
	chillerBStart := at11m.Add(-2 * time.Minute)
	multiPeers := map[string]PeerSample{
		"temp/cw_in":          {Number: number(28.0), Good: true, SampledAt: at11m},
		"chiller-a/run_state": {Text: "RUNNING", Good: true, SampledAt: at11m, UnchangedSince: &chillerAStart},
		"chiller-b/run_state": {Text: "RUNNING", Good: true, SampledAt: at11m, UnchangedSince: &chillerBStart},
	}
	if reasons := AssessPointQuality(27.0, at11m, multiRuleChecks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: multiPeers}); len(reasons) != 0 {
		t.Fatalf("latest start transition (2m ago) must keep physics check in stabilization window, got %v", reasons)
	}
}

func TestStaleRunStateAndPeerTemperatureAreIgnored(t *testing.T) {
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	epsilon := nameplate.DefaultDirectionEpsilonK
	number := func(v float64) *float64 { return &v }
	checks := &QualityChecks{
		Physics: []PhysicsRule{{
			Kind:     nameplate.KindCondenserDirection,
			EpsilonK: &epsilon,
			Peers:    []PhysicsPeer{{Role: "entering", DeviceID: "temp", Key: "cw_in"}},
			Running:  []OperatingExpect{{DeviceID: "chiller", Key: "run_state", Equals: "RUNNING"}},
		}},
	}

	staleRunState := map[string]PeerSample{
		"temp/cw_in":        {Number: number(28.0), Good: true, SampledAt: at.Add(-30 * time.Second), StaleAfter: 3 * time.Minute},
		"chiller/run_state": {Text: "RUNNING", Good: true, SampledAt: at.Add(-5 * time.Minute), StaleAfter: 3 * time.Minute},
	}
	if reasons := AssessPointQuality(26.0, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: staleRunState}); len(reasons) != 0 {
		t.Fatalf("stale run_state must be treated as not running, got %v", reasons)
	}

	staleTemp := map[string]PeerSample{
		"temp/cw_in":        {Number: number(28.0), Good: true, SampledAt: at.Add(-5 * time.Minute), StaleAfter: 3 * time.Minute},
		"chiller/run_state": {Text: "RUNNING", Good: true, SampledAt: at.Add(-30 * time.Second), StaleAfter: 3 * time.Minute},
	}
	if reasons := AssessPointQuality(26.0, at, checks, PointQualityInput{PointType: "TELEMETRY", Live: true, Peers: staleTemp}); len(reasons) != 0 {
		t.Fatalf("stale peer temperature must skip physics check, got %v", reasons)
	}
}

func TestFlatlineSuppressedBySentinelSourceUntrustedAndStabilizeWindow(t *testing.T) {
	start := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	at := start.Add(40 * time.Minute)

	sentinelChecks := &QualityChecks{Flatline: 30 * time.Minute, Sentinels: []float64{38.4}}
	heldSentinel := PriorNumeric{Value: 38.4, SampledAt: start, UnchangedSince: start}
	if reasons := AssessPointQuality(38.4, at, sentinelChecks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &heldSentinel}); !sameReasons(reasons, QualityReasonSentinel) {
		t.Fatalf("sentinel must suppress flatline: got %v", reasons)
	}

	heldValue := PriorNumeric{Value: 18.7, SampledAt: start, UnchangedSince: start}
	if reasons := AssessPointQuality(18.7, at, sentinelChecks, PointQualityInput{PointType: "TELEMETRY", Live: true, Previous: &heldValue, SourceUntrusted: true}); len(reasons) != 0 {
		t.Fatalf("untrusted source must suppress flatline: got %v", reasons)
	}

	stabilizedChecks := &QualityChecks{
		Flatline:          24 * time.Hour,
		FlatlineRunning:   30 * time.Minute,
		FlatlineStabilize: 10 * time.Minute,
	}
	justStarted := at.Add(-2 * time.Minute)
	if reasons := AssessPointQuality(18.7, at, stabilizedChecks, PointQualityInput{
		PointType: "TELEMETRY", Live: true, Previous: &heldValue, Running: true, RunningSince: &justStarted,
	}); len(reasons) != 0 {
		t.Fatalf("running flatline inside FlatlineStabilize window must skip: got %v", reasons)
	}

	settled := at.Add(-11 * time.Minute)
	if reasons := AssessPointQuality(18.7, at, stabilizedChecks, PointQualityInput{
		PointType: "TELEMETRY", Live: true, Previous: &heldValue, Running: true, RunningSince: &settled,
	}); !sameReasons(reasons, QualityReasonFlatline) {
		t.Fatalf("running flatline after FlatlineStabilize window must fire: got %v", reasons)
	}

	longStuck := PriorNumeric{Value: 18.7, SampledAt: at.Add(-25 * time.Hour), UnchangedSince: at.Add(-25 * time.Hour)}
	if reasons := AssessPointQuality(18.7, at, stabilizedChecks, PointQualityInput{
		PointType: "TELEMETRY", Live: true, Previous: &longStuck, Running: true, RunningSince: &justStarted,
	}); !sameReasons(reasons, QualityReasonFlatline) {
		t.Fatalf("long flatline exceeding Flatline must still fire immediately on start: got %v", reasons)
	}

	futurePeer := PeerSample{Good: true, Text: "RUNNING", SampledAt: at.Add(10 * time.Minute), StaleAfter: 3 * time.Minute}
	if futurePeer.FreshAt(at) {
		t.Fatal("future peer beyond StaleAfter must not be treated as fresh for outbox replay")
	}
}

func TestValidatePhysicalBoundsEnvelopesAndRegisterSentinels(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := ValidatePhysicalBounds(bad, nil, "TELEMETRY"); !sameReasons(got, QualityReasonOutOfRange) {
			t.Fatalf("non-finite %v = %v", bad, got)
		}
	}
	if got := ValidatePhysicalBounds(100, nil, "TELEMETRY"); len(got) != 0 {
		t.Fatalf("nil unit finite = %v", got)
	}
	if got := ValidatePhysicalBounds(100, strPtr("   "), "TELEMETRY"); len(got) != 0 {
		t.Fatalf("blank unit finite = %v", got)
	}

	validCases := []struct {
		unit      string
		pointType string
		values    []float64
	}{
		{"Cel", "TELEMETRY", []float64{-60, 0, 23.5, 176.8, 200}},
		{" degC ", "TELEMETRY", []float64{-60, 200}},
		{"°C", "TELEMETRY", []float64{-60, 200}},
		{"℃", "TELEMETRY", []float64{-60, 200}},
		{"%", "TELEMETRY", []float64{0, 50, 100}},
		{"%RH", "TELEMETRY", []float64{0, 55.5, 100}},
		{"Hz", "TELEMETRY", []float64{0, 50, 120}},
		{"Pa", "TELEMETRY", []float64{-101325, 0, 32767, 65535, 10000000}},
		{"kPa", "TELEMETRY", []float64{-101.325, 0, 10000}},
		{"MPa", "TELEMETRY", []float64{-0.101325, 0, 10}},
		{"bar", "TELEMETRY", []float64{-1.01325, 0, 100}},
		{"m3/h", "TELEMETRY", []float64{0, 32767, 100000}},
		{"m³/h", "TELEMETRY", []float64{0, 100000}},
		{"L/s", "TELEMETRY", []float64{0, 30000}},
		{"kW", "TELEMETRY", []float64{-50000, 0, 32767, 200000}},
		{"kWh", "COUNTER", []float64{0, 65535, 1e12}},
		{"MWh", "COUNTER", []float64{0, 32767, 1e12}},
	}
	for _, tc := range validCases {
		for _, v := range tc.values {
			if got := ValidatePhysicalBounds(v, strPtr(tc.unit), tc.pointType); len(got) != 0 {
				t.Fatalf("expected valid (%v %q, %s), got %v", v, tc.unit, tc.pointType, got)
			}
		}
	}

	outOfRangeOnly := []struct {
		unit      string
		pointType string
		values    []float64
	}{
		{"Cel", "TELEMETRY", []float64{-60.01, 200.01, 500}},
		{"degC", "SETTING", []float64{-61, 201}},
		{"%RH", "TELEMETRY", []float64{-0.1, 100.1}},
		{"Hz", "TELEMETRY", []float64{-1, 120.1}},
		{"Pa", "TELEMETRY", []float64{-101326, 10000001}},
		{"kPa", "TELEMETRY", []float64{-102, 10001}},
		{"MPa", "TELEMETRY", []float64{-0.2, 10.1}},
		{"bar", "TELEMETRY", []float64{-1.1, 100.1}},
		{"m3/h", "TELEMETRY", []float64{-1, 100001}},
		{"L/s", "TELEMETRY", []float64{-1, 30001, 65535}},
		{"kW", "TELEMETRY", []float64{-50001, 200001}},
		{"kWh", "COUNTER", []float64{-0.1, 1.0001e12}},
		{"Cel", "COUNTER", []float64{65535}},
	}
	for _, tc := range outOfRangeOnly {
		for _, v := range tc.values {
			if got := ValidatePhysicalBounds(v, strPtr(tc.unit), tc.pointType); !sameReasons(got, QualityReasonOutOfRange) {
				t.Fatalf("expected OUT_OF_RANGE for (%v %q, %s), got %v", v, tc.unit, tc.pointType, got)
			}
		}
	}

	for _, unit := range []string{"Cel", " degC ", "°C", "℃", "%", "%RH", "Hz", "bar", "MPa"} {
		for _, sentinel := range []float64{65535, 32767, 32767.00005, -32768} {
			if got := ValidatePhysicalBounds(sentinel, strPtr(unit), "TELEMETRY"); !sameReasons(got, QualityReasonOutOfRange, QualityReasonSentinel) {
				t.Fatalf("expected OUT_OF_RANGE+SENTINEL for (%v %q), got %v", sentinel, unit, got)
			}
		}
	}
}
