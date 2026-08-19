package alarmservice

import (
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
	if decision.State.Status != EvaluationMatched || decision.State.QualityBlocker != "" {
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
