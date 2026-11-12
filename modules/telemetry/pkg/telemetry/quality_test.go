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
