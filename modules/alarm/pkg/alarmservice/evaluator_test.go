package alarmservice

import (
	"strconv"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const (
	testPolicyID         = "01910000-5000-7000-8000-000000000001"
	testPolicyRevisionID = "01910000-5000-7000-8000-000000000002"
	testPolicyRevision2  = "01910000-5000-7000-8000-000000000003"
	testIncidentCorrID   = "01910000-6000-7000-8000-000000000001"
)

func TestAlarmPolicyRevisionRejectsPayloadThatDoesNotMatchDigest(t *testing.T) {
	policy := validAlarmPolicy()
	policy.Summary = "tampered after release"
	if err := policy.Validate(); err == nil {
		t.Fatal("released Alarm policy accepted content that no longer matches its digest")
	}
}

func TestEvaluatePolicyDurationAndHysteresisSurviveRestart(t *testing.T) {
	policy := validAlarmPolicy()
	policy.TriggerMode = TriggerDuration
	policy.DurationSeconds = 300
	policy.Raise = Condition{Kind: ConditionHysteresis, Input: "supplyTemp", Direction: DirectionAbove, Trigger: 30, Reset: 28}
	policy.Clear = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareLTE, Value: NumberValue(28)}
	sealAlarmPolicy(&policy)

	first := evaluationSnapshot("input-r1", "2026-08-19T10:00:00Z", NumberInput("supplyTemp", 31, "GOOD", "2026-08-19T10:00:00Z"))
	decision, err := EvaluatePolicy(policy, first, AlarmEvaluationState{}, mustTime(t, "2026-08-19T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectNone || decision.State.Status != EvaluationNotMatched || decision.State.CandidateSince == nil || decision.State.NextEvaluationAt == nil || *decision.State.NextEvaluationAt != "2026-08-19T10:05:00Z" {
		t.Fatalf("duration candidate was not persisted for restart: %#v", decision)
	}

	// A restarted evaluator receives only the persisted state and the same owner snapshot.
	restarted, err := EvaluatePolicy(policy, first, decision.State, mustTime(t, "2026-08-19T10:05:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Effect != EvaluationEffectPublish || restarted.State.Status != EvaluationMatched || restarted.State.NextEvaluationAt == nil || *restarted.State.NextEvaluationAt != "2026-08-19T10:15:00Z" {
		t.Fatalf("duration did not fire deterministically or preserve freshness reevaluation after restart: %#v", restarted)
	}
}

func TestEvaluatePolicyRepeatingCountsDistinctInputRevisionsOnly(t *testing.T) {
	policy := validAlarmPolicy()
	policy.TriggerMode = TriggerRepeating
	policy.RepeatCount = 3
	sealAlarmPolicy(&policy)

	state := AlarmEvaluationState{}
	for index, revision := range []string{"input-r1", "input-r1", "input-r2", "input-r3"} {
		snapshot := evaluationSnapshot(revision, time.Date(2026, 8, 19, 10, index, 0, 0, time.UTC).Format(time.RFC3339), NumberInput("supplyTemp", 31, "GOOD", time.Date(2026, 8, 19, 10, index, 0, 0, time.UTC).Format(time.RFC3339)))
		decision, err := EvaluatePolicy(policy, snapshot, state, mustTime(t, snapshot.AsOf))
		if err != nil {
			t.Fatal(err)
		}
		state = decision.State
		if index < 3 && decision.Effect != EvaluationEffectNone {
			t.Fatalf("repeating rule fired early at step %d: %#v", index, decision)
		}
		if index == 3 && decision.Effect != EvaluationEffectPublish {
			t.Fatalf("third distinct matching revision did not publish: %#v", decision)
		}
	}
	if state.RepeatCount != 3 {
		t.Fatalf("duplicate input revision advanced repeat state: %#v", state)
	}
}

func TestEvaluatePolicyNoDataUsesDurableScheduledReevaluation(t *testing.T) {
	policy := validAlarmPolicy()
	policy.Raise = Condition{Kind: ConditionNoData, Input: "supplyTemp", AgeSeconds: 60}
	policy.Clear = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareLTE, Value: NumberValue(28)}
	sealAlarmPolicy(&policy)

	observed := NumberInput("supplyTemp", 31, "GOOD", "2026-08-19T10:00:00Z")
	first := evaluationSnapshot("no-data-r1", "2026-08-19T10:00:00Z", observed)
	decision, err := EvaluatePolicy(policy, first, AlarmEvaluationState{}, mustTime(t, first.AsOf))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectNone || decision.State.NextEvaluationAt == nil || *decision.State.NextEvaluationAt != "2026-08-19T10:01:00Z" {
		t.Fatalf("no-data predicate did not schedule its durable threshold: %#v", decision)
	}
	decision, err = EvaluatePolicy(policy, first, decision.State, mustTime(t, "2026-08-19T10:01:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectPublish || decision.State.Status != EvaluationMatched {
		t.Fatalf("no-data threshold did not publish after scheduled reevaluation: %#v", decision)
	}
}

func TestEvaluatePolicyUntrustedInputCannotClearActiveIncident(t *testing.T) {
	policy := validAlarmPolicy()
	state := AlarmEvaluationState{
		PolicyRevisionID:            policy.PolicyRevisionID,
		Status:                      EvaluationMatched,
		ActiveIncidentCorrelationID: testIncidentCorrID,
		LastInputRevision:           "input-r1",
		Version:                     7,
	}

	for name, input := range map[string]InputFact{
		"stale":   NumberInput("supplyTemp", 27, "STALE", "2026-08-19T09:00:00Z"),
		"invalid": NumberInput("supplyTemp", 27, "INVALID", "2026-08-19T10:00:00Z"),
		"missing": {Key: "supplyTemp", Present: false, Quality: "GOOD"},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := evaluationSnapshot("input-r2-"+name, "2026-08-19T10:01:00Z", input)
			decision, err := EvaluatePolicy(policy, snapshot, state, mustTime(t, snapshot.AsOf))
			if err != nil {
				t.Fatal(err)
			}
			if decision.Effect == EvaluationEffectClear || decision.State.ActiveIncidentCorrelationID != testIncidentCorrID || decision.State.Status != EvaluationIndeterminate || decision.State.QualityBlocker == "" {
				t.Fatalf("untrusted input cleared or lost the active Incident: %#v", decision)
			}
		})
	}
}

func TestEvaluatePolicySiteScheduleUsesIanaTimezoneAcrossDST(t *testing.T) {
	policy := validAlarmPolicy()
	policy.Schedule = &SiteSchedule{
		Timezone: "America/Los_Angeles",
		Windows:  []ScheduleWindow{{DaysOfWeek: []int{7}, StartsOnMinute: 60, EndsOnMinute: 240}}, // Sunday 01:00-04:00 local.
	}
	sealAlarmPolicy(&policy)

	beforeJump := evaluationSnapshot("dst-r1", "2026-03-08T09:30:00Z", NumberInput("supplyTemp", 31, "GOOD", "2026-03-08T09:30:00Z")) // 01:30 PST
	decision, err := EvaluatePolicy(policy, beforeJump, AlarmEvaluationState{}, mustTime(t, beforeJump.AsOf))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectPublish || decision.State.Status != EvaluationMatched {
		t.Fatalf("schedule was not active before spring-forward jump: %#v", decision)
	}

	activeState := decision.State
	activeState.ActiveIncidentCorrelationID = testIncidentCorrID
	afterJump := evaluationSnapshot("dst-r2", "2026-03-08T10:30:00Z", NumberInput("supplyTemp", 31, "GOOD", "2026-03-08T10:30:00Z")) // 03:30 PDT
	decision, err = EvaluatePolicy(policy, afterJump, activeState, mustTime(t, afterJump.AsOf))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectNone || decision.State.Status != EvaluationMatched || decision.State.QualityBlocker != "" {
		t.Fatalf("schedule did not remain active across DST jump: %#v", decision)
	}

	outside := evaluationSnapshot("dst-r3", "2026-03-08T11:00:00Z", NumberInput("supplyTemp", 27, "GOOD", "2026-03-08T11:00:00Z")) // 04:00 PDT
	decision, err = EvaluatePolicy(policy, outside, activeState, mustTime(t, outside.AsOf))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect == EvaluationEffectClear || decision.State.Status != EvaluationIndeterminate || decision.State.QualityBlocker != QualityBlockerOutsideSchedule {
		t.Fatalf("schedule boundary incorrectly cleared active Incident: %#v", decision)
	}
}

func TestEvaluatePolicyRevisionSwitchResetsCandidateButPreservesActiveIncident(t *testing.T) {
	oldPolicy := validAlarmPolicy()
	oldPolicy.TriggerMode = TriggerDuration
	oldPolicy.DurationSeconds = 300
	oldPolicy.Raise = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareGT, Value: NumberValue(30)}
	oldPolicy.Clear = Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareLTE, Value: NumberValue(28)}
	sealAlarmPolicy(&oldPolicy)

	candidateSnapshot := evaluationSnapshot("old-r1", "2026-08-19T10:00:00Z", NumberInput("supplyTemp", 31, "GOOD", "2026-08-19T10:00:00Z"))
	candidate, err := EvaluatePolicy(oldPolicy, candidateSnapshot, AlarmEvaluationState{}, mustTime(t, candidateSnapshot.AsOf))
	if err != nil {
		t.Fatal(err)
	}

	newPolicy := oldPolicy
	newPolicy.PolicyRevisionID = testPolicyRevision2
	newPolicy.Revision = 2
	newPolicy.Digest, _ = AlarmPolicyDigest(newPolicy)
	switchedSnapshot := evaluationSnapshot("new-r1", "2026-08-19T10:04:59Z", NumberInput("supplyTemp", 31, "GOOD", "2026-08-19T10:04:59Z"))
	switched, err := EvaluatePolicy(newPolicy, switchedSnapshot, candidate.State, mustTime(t, switchedSnapshot.AsOf))
	if err != nil {
		t.Fatal(err)
	}
	if switched.Effect != EvaluationEffectNone || switched.State.PolicyRevisionID != testPolicyRevision2 || switched.State.CandidateSince == nil || *switched.State.CandidateSince != switchedSnapshot.AsOf {
		t.Fatalf("policy switch inherited an old duration candidate: %#v", switched)
	}

	activeState := candidate.State
	activeState.Status = EvaluationMatched
	activeState.ActiveIncidentCorrelationID = testIncidentCorrID
	clearSnapshot := evaluationSnapshot("new-r2", "2026-08-19T10:05:00Z", NumberInput("supplyTemp", 27, "GOOD", "2026-08-19T10:05:00Z"))
	cleared, err := EvaluatePolicy(newPolicy, clearSnapshot, activeState, mustTime(t, clearSnapshot.AsOf))
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Effect != EvaluationEffectClear || cleared.Recovery == nil || cleared.Recovery.IncidentCorrelationID != testIncidentCorrID {
		t.Fatalf("policy switch lost the active Incident clear target: %#v", cleared)
	}
}

func validAlarmPolicy() AlarmPolicyRevision {
	policy := AlarmPolicyRevision{
		SchemaVersion:    1,
		PolicyID:         testPolicyID,
		PolicyRevisionID: testPolicyRevisionID,
		Revision:         1,
		AlarmType:        "SUPPLY_TEMPERATURE_DRIFT",
		SourceType:       alarmmodel.SourceSiteRule,
		SourceReference:  "alarm-policy:supply-temperature-drift",
		Title:            "Supply temperature drift",
		Summary:          "Supply temperature is outside the governed operating band.",
		Severity:         alarmmodel.SeverityMajor,
		QualityPolicy:    EvaluationQualityValidOnly,
		FreshnessSeconds: 900,
		TriggerMode:      TriggerSimple,
		Raise:            Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareGT, Value: NumberValue(30)},
		Clear:            Condition{Kind: ConditionCompare, Input: "supplyTemp", Operator: CompareLTE, Value: NumberValue(28)},
	}
	sealAlarmPolicy(&policy)
	return policy
}

func sealAlarmPolicy(policy *AlarmPolicyRevision) {
	policy.Digest, _ = AlarmPolicyDigest(*policy)
}

func evaluationSnapshot(revision, asOf string, inputs ...InputFact) EvaluationSnapshot {
	values := make(map[string]InputFact, len(inputs))
	for _, input := range inputs {
		values[input.Key] = input
	}
	return EvaluationSnapshot{
		TenantID:      testTenantID,
		SiteID:        testSiteID,
		SubjectType:   "SITE",
		SubjectID:     testSiteID,
		InputRevision: revision,
		AsOf:          asOf,
		Inputs:        values,
		CorrelationID: revision,
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// #616: a reading that keeps the same value for the window raises, even with no new reading to
// evaluate; a change clears it. A reading that is no longer fresh is not judged stuck.
func TestEvaluatePolicyUnchangedReadingRaisesAfterItsWindowAndAChangeClears(t *testing.T) {
	policy := validAlarmPolicy()
	policy.QualityPolicy = EvaluationQualityUsable
	policy.Raise = Condition{Kind: ConditionUnchanged, Input: "outdoorTemp", AgeSeconds: 6 * 3600}
	policy.Clear = Condition{Kind: ConditionChanged, Input: "outdoorTemp", AgeSeconds: 6 * 3600}
	sealAlarmPolicy(&policy)
	since := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	reading := func(revision string, observed, unchangedSince time.Time, value float64) EvaluationSnapshot {
		fact := NumberInput("outdoorTemp", value, "GOOD", observed.Format(time.RFC3339Nano))
		fact.UnchangedSince = unchangedSince.Format(time.RFC3339Nano)
		return evaluationSnapshot(revision, observed.Format(time.RFC3339Nano), fact)
	}

	almost := since.Add(6*time.Hour - time.Minute)
	steady, err := EvaluatePolicy(policy, reading("r-1", almost, since, 21.5), AlarmEvaluationState{}, almost)
	if err != nil {
		t.Fatal(err)
	}
	if steady.Effect != EvaluationEffectNone || steady.State.NextEvaluationAt == nil || *steady.State.NextEvaluationAt != since.Add(6*time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("raised before 6 hours, or did not look again at 6 hours: %#v", steady)
	}
	stuck, err := EvaluatePolicy(policy, reading("r-1", almost, since, 21.5), steady.State, since.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stuck.Effect != EvaluationEffectPublish {
		t.Fatalf("6 hours of the same value did not raise: %#v", stuck)
	}
	stuck.State.ActiveAlarmID, stuck.State.ActiveIncidentCorrelationID = testAlarmID, "018f3e00-4000-7000-8000-000000000002"

	moved := since.Add(7 * time.Hour)
	changed, err := EvaluatePolicy(policy, reading("r-2", moved, moved, 21.7), stuck.State, moved)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Effect != EvaluationEffectClear {
		t.Fatalf("a changed value did not clear: %#v", changed)
	}

	late := since.Add(7 * time.Hour)
	stale, err := EvaluatePolicy(policy, reading("r-3", since.Add(time.Hour), since, 21.5), AlarmEvaluationState{}, late)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Effect != EvaluationEffectNone || stale.Status != EvaluationIndeterminate {
		t.Fatalf("a reading that stopped arriving was judged stuck: %#v", stale)
	}
}

// A range rejection keeps the last good number on screen and records the failure
// on the point's health. The data-quality rule sees that, and clears once it is gone.
func TestEvaluatePolicyQualityReasonsUseHealthNotTheDisplayedValue(t *testing.T) {
	policy := validAlarmPolicy()
	policy.AlarmType = "data-quality/suspect/t18"
	policy.QualityPolicy = EvaluationQualityUsable
	policy.Raise = Condition{Kind: ConditionReasons, Input: "supply", Match: ReasonMatchAny, Reasons: []string{"OUT_OF_RANGE", "SPIKE", "SENTINEL"}}
	policy.Clear = Condition{Kind: ConditionReasons, Input: "supply", Match: ReasonMatchNone, Reasons: []string{"OUT_OF_RANGE", "SPIKE", "SENTINEL"}}
	sealAlarmPolicy(&policy)
	at := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
	bad := NumberInput("supply", 7.1, "GOOD", at.Format(time.RFC3339Nano))
	bad.HealthReasons = []string{"OUT_OF_RANGE"}
	raised, err := EvaluatePolicy(policy, evaluationSnapshot("r-1", at.Format(time.RFC3339Nano), bad), AlarmEvaluationState{}, at)
	if err != nil || raised.Effect != EvaluationEffectPublish {
		t.Fatalf("range rejection did not raise: %#v %v", raised, err)
	}
	raised.State.ActiveAlarmID, raised.State.ActiveIncidentCorrelationID = testAlarmID, "018f3e00-4000-7000-8000-000000000002"
	good := NumberInput("supply", 6.8, "GOOD", at.Add(time.Minute).Format(time.RFC3339Nano))
	cleared, err := EvaluatePolicy(policy, evaluationSnapshot("r-2", at.Add(time.Minute).Format(time.RFC3339Nano), good), raised.State, at.Add(time.Minute))
	if err != nil || cleared.Effect != EvaluationEffectClear {
		t.Fatalf("a healthy sample did not clear: %#v %v", cleared, err)
	}
}

func TestEvaluationSnapshotInputCapAllowsExpandedSiteInputsAndRejectsOverflow(t *testing.T) {
	at := "2026-11-14T10:00:00Z"
	makeInputs := func(count int) []InputFact {
		inputs := make([]InputFact, 0, count)
		for index := range count {
			inputs = append(inputs, NumberInput("p"+strconv.Itoa(index), float64(index), "GOOD", at))
		}
		return inputs
	}

	withinCap := evaluationSnapshot("cap-ok", at, makeInputs(200)...)
	if err := withinCap.Validate(); err != nil {
		t.Fatalf("snapshot with 200 inputs failed validation: %v", err)
	}

	overflow := evaluationSnapshot("cap-over", at, makeInputs(MaxEvaluationSnapshotInputs+1)...)
	if err := overflow.Validate(); err == nil {
		t.Fatalf("snapshot exceeding MaxEvaluationSnapshotInputs (%d) unexpectedly passed validation", MaxEvaluationSnapshotInputs)
	}
}

func TestEvaluatePolicyStreamingWindowAggregations(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC)
	threshP95 := 12.0
	p95Policy := validAlarmPolicy()
	p95Policy.RuleKind = RuleKindWindow
	p95Policy.ExecutionTarget = ExecutionTargetCloudAndEdge
	p95Policy.QualityPolicy = EvaluationQualityValidOnly
	p95Policy.FreshnessSeconds = 600
	p95Policy.Raise = Condition{
		Kind:          ConditionWindowAgg,
		Input:         "supply",
		Agg:           WindowAggP95,
		WindowType:    WindowTypeSliding,
		WindowSeconds: 180,
		MinSamples:    3,
		Operator:      CompareGTE,
		Threshold:     &threshP95,
	}
	p95Policy.Clear = Condition{Kind: ConditionCompare, Input: "supply", Operator: CompareLTE, Value: NumberValue(8.0)}
	sealAlarmPolicy(&p95Policy)

	var st AlarmEvaluationState
	// Sample 1 & 2 -> warm-up (< 3 samples), must stay NOT_MATCHED.
	for i, val := range []float64{10.0, 14.0} {
		at := t0.Add(time.Duration(i*30) * time.Second)
		res, err := EvaluatePolicy(p95Policy, evaluationSnapshot("w-"+strconv.Itoa(i), at.Format(time.RFC3339Nano), NumberInput("supply", val, "GOOD", at.Format(time.RFC3339Nano))), st, at)
		if err != nil || res.Effect != EvaluationEffectNone || res.Status != EvaluationNotMatched {
			t.Fatalf("warm-up step %d unexpected: %#v err=%v", i, res, err)
		}
		st = res.State
	}
	// Sample 3: [10, 11, 14] -> P95 = 13.7 >= 12.0 -> PUBLISH!
	at3 := t0.Add(60 * time.Second)
	res3, err := EvaluatePolicy(p95Policy, evaluationSnapshot("w-3", at3.Format(time.RFC3339Nano), NumberInput("supply", 11.0, "GOOD", at3.Format(time.RFC3339Nano))), st, at3)
	if err != nil || res3.Effect != EvaluationEffectPublish || res3.Status != EvaluationMatched {
		t.Fatalf("P95 step 3 expected PUBLISH, got %#v err=%v", res3, err)
	}

	// Tumbling STDDEV policy resets on aligned bucket boundary and requires >= 2 samples.
	stdThresh := 1.0
	stdPolicy := validAlarmPolicy()
	stdPolicy.AlarmType = "CHW_JITTER"
	stdPolicy.SourceReference = "alarm-policy:chw-jitter"
	stdPolicy.QualityPolicy = EvaluationQualityValidOnly
	stdPolicy.FreshnessSeconds = 600
	stdPolicy.Raise = Condition{
		Kind:          ConditionWindowAgg,
		Input:         "supply",
		Agg:           WindowAggStdDev,
		WindowType:    WindowTypeTumbling,
		WindowSeconds: 60,
		Operator:      CompareGT,
		Threshold:     &stdThresh,
	}
	stdPolicy.Clear = Condition{Kind: ConditionCompare, Input: "supply", Operator: CompareLTE, Value: NumberValue(5.0)}
	sealAlarmPolicy(&stdPolicy)

	b1a := time.Date(2026, 11, 22, 1, 5, 10, 0, time.UTC)
	d1, err := EvaluatePolicy(stdPolicy, evaluationSnapshot("std-1", b1a.Format(time.RFC3339Nano), NumberInput("supply", 8.0, "GOOD", b1a.Format(time.RFC3339Nano))), AlarmEvaluationState{}, b1a)
	if err != nil || d1.Effect != EvaluationEffectNone {
		t.Fatalf("tumbling 1 sample must not fire: %#v err=%v", d1, err)
	}
	// Jump to next aligned bucket 01:06:10 (only 1 sample in new tumbling bucket despite prior sample 60s earlier in previous bucket).
	b2a := time.Date(2026, 11, 22, 1, 6, 10, 0, time.UTC)
	d2, err := EvaluatePolicy(stdPolicy, evaluationSnapshot("std-2", b2a.Format(time.RFC3339Nano), NumberInput("supply", 20.0, "GOOD", b2a.Format(time.RFC3339Nano))), d1.State, b2a)
	if err != nil || d2.Effect != EvaluationEffectNone {
		t.Fatalf("new tumbling bucket with 1 sample must not combine prior bucket: %#v err=%v", d2, err)
	}
	// Second sample inside bucket 01:06:30 -> [20, 10], STDDEV = 5 > 1 -> PUBLISH!
	b2b := time.Date(2026, 11, 22, 1, 6, 30, 0, time.UTC)
	d3, err := EvaluatePolicy(stdPolicy, evaluationSnapshot("std-3", b2b.Format(time.RFC3339Nano), NumberInput("supply", 10.0, "GOOD", b2b.Format(time.RFC3339Nano))), d2.State, b2b)
	if err != nil || d3.Effect != EvaluationEffectPublish {
		t.Fatalf("tumbling bucket with 2 jittery samples did not publish: %#v err=%v", d3, err)
	}
}
