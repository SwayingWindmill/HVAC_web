package alarmservice

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	telemetryEvaluationTestTenantID  = "01910000-9000-7000-8000-000000000001"
	telemetryEvaluationTestSiteID    = "01910000-9000-7000-8000-000000000002"
	telemetryEvaluationTestChillerID = "01910000-9000-7000-8000-000000000003"
	telemetryEvaluationTestBTUID     = "01910000-9000-7000-8000-000000000004"
	telemetryEvaluationTestEventA    = "01910000-9000-7000-8000-000000000005"
	telemetryEvaluationTestEventB    = "01910000-9000-7000-8000-000000000006"
)

func TestBuildSiteEvaluationSnapshotMergesCanonicalDeviceFactsWithEvidence(t *testing.T) {
	startedAt := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	records := []telemetryEvaluationInputRecord{
		telemetryEvaluationRecord(telemetryEvaluationTestChillerID, telemetryEvaluationTestEventA, 4, startedAt,
			presentTelemetryState("run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", startedAt),
			presentTelemetryState("cooling_capacity", "NUMBER", `420`, "GOOD", "FRESH", startedAt),
			presentTelemetryState("return_water_temperature", "NUMBER", `12.5`, "GOOD", "FRESH", startedAt)),
		telemetryEvaluationRecord(telemetryEvaluationTestBTUID, telemetryEvaluationTestEventB, 7, startedAt.Add(time.Second),
			presentTelemetryState("return_water_temperature", "NUMBER", `10.2`, "GOOD", "FRESH", startedAt.Add(time.Second))),
	}

	snapshot, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, records)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SubjectType != "SITE" || snapshot.SubjectID != telemetryEvaluationTestSiteID || snapshot.AsOf != "2026-08-28T12:00:01Z" {
		t.Fatalf("unexpected Site evaluation identity: %#v", snapshot)
	}
	if len(snapshot.Inputs) != 4 {
		t.Fatalf("expected four device-scoped facts, got %#v", snapshot.Inputs)
	}
	returnFact := snapshot.Inputs[telemetryEvaluationTestBTUID+"/return_water_temperature"]
	if !returnFact.Present || returnFact.Value.Type != EvaluationValueNumber || returnFact.Value.Number != 10.2 || returnFact.Quality != "GOOD" {
		t.Fatalf("return-water fact lost canonical value metadata: %#v", returnFact)
	}
	if len(returnFact.Evidence) != 1 || returnFact.Evidence[0].Reference != telemetryEvaluationTestEventB+"#return_water_temperature" {
		t.Fatalf("return-water fact lost canonical snapshot evidence: %#v", returnFact.Evidence)
	}

	reversed, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{records[1], records[0]})
	if err != nil {
		t.Fatal(err)
	}
	if reversed.InputRevision != snapshot.InputRevision {
		t.Fatalf("Site input revision depends on delivery order: %s != %s", reversed.InputRevision, snapshot.InputRevision)
	}
}

func TestTelemetryFreshnessQualityAndAvailabilityRemainIndeterminateForAlarmPolicy(t *testing.T) {
	startedAt := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	record := telemetryEvaluationRecord(telemetryEvaluationTestBTUID, telemetryEvaluationTestEventB, 1, startedAt,
		presentTelemetryState("return_water_temperature", "NUMBER", `10.0`, "GOOD", "STALE", startedAt))
	snapshot, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	policy := validAlarmPolicy()
	policy.Raise = Condition{Kind: ConditionCompare, Input: telemetryEvaluationTestBTUID + "/return_water_temperature", Operator: CompareLTE, Value: NumberValue(10.5)}
	policy.Clear = Condition{Kind: ConditionCompare, Input: telemetryEvaluationTestBTUID + "/return_water_temperature", Operator: CompareGTE, Value: NumberValue(11.5)}
	sealAlarmPolicy(&policy)
	decision, err := EvaluatePolicy(policy, snapshot, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != EvaluationIndeterminate || decision.State.QualityBlocker != "STALE_INPUT:"+telemetryEvaluationTestBTUID+"/return_water_temperature" {
		t.Fatalf("stale canonical fact was not preserved as INDETERMINATE: %#v", decision)
	}

	record.Snapshot.Values[0] = presentTelemetryState("return_water_temperature", "NUMBER", `10.0`, "INVALID", "FRESH", startedAt)
	invalid, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = EvaluatePolicy(policy, invalid, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != EvaluationIndeterminate || decision.State.QualityBlocker != "INVALID_INPUT:"+telemetryEvaluationTestBTUID+"/return_water_temperature" {
		t.Fatalf("invalid canonical fact was not preserved as INDETERMINATE: %#v", decision)
	}

	record.Snapshot.EvaluationAvailability = telemetryapi.EvaluationAvailabilityUnavailable
	record.Snapshot.Values[0] = presentTelemetryState("return_water_temperature", "NUMBER", `10.0`, "GOOD", "FRESH", startedAt)
	unavailable, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = EvaluatePolicy(policy, unavailable, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != EvaluationIndeterminate || decision.State.QualityBlocker != "UNTRUSTED_QUALITY:"+telemetryEvaluationTestBTUID+"/return_water_temperature:UNAVAILABLE" {
		t.Fatalf("unavailable Device snapshot was not preserved as INDETERMINATE: %#v", decision)
	}
}

func telemetryEvaluationRecord(deviceID, eventID string, revision int64, evaluatedAt time.Time, values ...telemetryapi.TelemetryKeyState) telemetryEvaluationInputRecord {
	return telemetryEvaluationInputRecord{
		DeviceID: deviceID, BusinessRevision: revision, EventID: eventID, EvaluatedAt: evaluatedAt,
		Snapshot: telemetryapi.DeviceObservationSnapshot{
			SchemaVersion: 1, DeviceId: telemetryapi.UUIDv7(deviceID), TenantId: telemetryapi.UUIDv7(telemetryEvaluationTestTenantID), SiteId: telemetryapi.UUIDv7(telemetryEvaluationTestSiteID),
			BusinessRevision: telemetryapi.BusinessRevision(revision), EvaluatedAt: telemetryapi.Instant(evaluatedAt.Format(time.RFC3339Nano)), EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
			Values: values,
		},
	}
}

func presentTelemetryState(key, valueType, rawValue, quality, freshness string, sampledAt time.Time) telemetryapi.TelemetryKeyState {
	return telemetryapi.TelemetryKeyState{Present: &telemetryapi.TelemetryPresentState{
		Key: telemetryapi.TelemetryKey(key), State: "PRESENT", Value: json.RawMessage(rawValue), ValueType: valueType,
		SampledAt: telemetryapi.Instant(sampledAt.Format(time.RFC3339Nano)), ReceivedAt: telemetryapi.Instant(sampledAt.Format(time.RFC3339Nano)), Freshness: freshness, Quality: telemetryapi.TelemetryQuality(quality), PolicyRevision: 1,
	}}
}

func TestColdStartRejectedCandidateRaisesReasonsAlarm(t *testing.T) {
	startedAt := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	missingState := func(key, reason string) telemetryapi.TelemetryKeyState {
		return telemetryapi.TelemetryKeyState{Missing: &telemetryapi.TelemetryMissingState{
			Key: telemetryapi.TelemetryKey(key), State: "MISSING", Freshness: "MISSING", MissingReason: reason,
		}}
	}
	record := telemetryEvaluationRecord(telemetryEvaluationTestBTUID, telemetryEvaluationTestEventB, 1, startedAt,
		missingState("return_water_temperature", "ONLY_REJECTED_CANDIDATES"),
		missingState("supply_water_temperature", "NEVER_OBSERVED"))
	snapshot, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	rejectedFact := snapshot.Inputs[telemetryEvaluationTestBTUID+"/return_water_temperature"]
	if rejectedFact.Present || len(rejectedFact.HealthReasons) != 1 || rejectedFact.HealthReasons[0] != "OUT_OF_RANGE" {
		t.Fatalf("expected cold-start OUT_OF_RANGE health reason, got %#v", rejectedFact)
	}

	policy := validAlarmPolicy()
	policy.QualityPolicy = EvaluationQualityUsable
	policy.Raise = Condition{Kind: ConditionReasons, Input: telemetryEvaluationTestBTUID + "/return_water_temperature", Match: ReasonMatchAny, Reasons: []string{"OUT_OF_RANGE", "SOURCE_UNTRUSTED"}}
	policy.Clear = Condition{Kind: ConditionReasons, Input: telemetryEvaluationTestBTUID + "/return_water_temperature", Match: ReasonMatchNone, Reasons: []string{"OUT_OF_RANGE", "SOURCE_UNTRUSTED"}}
	sealAlarmPolicy(&policy)

	decision, err := EvaluatePolicy(policy, snapshot, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Effect != EvaluationEffectPublish || decision.Status != EvaluationMatched {
		t.Fatalf("cold-start ONLY_REJECTED_CANDIDATES did not fire ConditionReasons(ANY, [OUT_OF_RANGE]): %#v", decision)
	}

	neverPolicy := validAlarmPolicy()
	neverPolicy.QualityPolicy = EvaluationQualityUsable
	neverPolicy.Raise = Condition{Kind: ConditionReasons, Input: telemetryEvaluationTestBTUID + "/supply_water_temperature", Match: ReasonMatchAny, Reasons: []string{"OUT_OF_RANGE"}}
	neverPolicy.Clear = Condition{Kind: ConditionReasons, Input: telemetryEvaluationTestBTUID + "/supply_water_temperature", Match: ReasonMatchNone, Reasons: []string{"OUT_OF_RANGE"}}
	sealAlarmPolicy(&neverPolicy)

	neverDecision, err := EvaluatePolicy(neverPolicy, snapshot, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if neverDecision.Effect != EvaluationEffectNone || neverDecision.Status != EvaluationNotMatched {
		t.Fatalf("NEVER_OBSERVED unexpectedly matched OUT_OF_RANGE: %#v", neverDecision)
	}
}

func TestUnchangedSincePrefersAuthoritativeSnapshotTimestamp(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	heldSince := telemetryapi.Instant("2026-11-16T06:15:00.000Z")
	first := presentTelemetryState("run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", t0)
	first.Present.UnchangedSince = &heldSince
	second := presentTelemetryState("pressure", "NUMBER", `120`, "GOOD", "FRESH", t0)

	snapshot := telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1, DeviceId: telemetryEvaluationTestChillerID, TenantId: telemetryEvaluationTestTenantID, SiteId: telemetryEvaluationTestSiteID,
		BusinessRevision: 1, EvaluatedAt: telemetryapi.Instant(t0.Format(time.RFC3339Nano)), EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		Values: []telemetryapi.TelemetryKeyState{first, second},
	}

	// Even with no prior Alarm snapshot (cold start / process restart), authoritative unchangedSince is honored.
	held, err := unchangedSince(nil, nil, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if held["run_state"] != string(heldSince) || held["pressure"] != t0.Format(time.RFC3339Nano) {
		t.Fatalf("held=%#v", held)
	}

	// On fallback key without snapshot UnchangedSince, local diffing still carries forward prior start time.
	later := snapshot
	later.BusinessRevision = 2
	later.Values = []telemetryapi.TelemetryKeyState{first, presentTelemetryState("pressure", "NUMBER", `120`, "GOOD", "FRESH", t0.Add(time.Minute))}
	carried, err := unchangedSince(&snapshot, held, later)
	if err != nil {
		t.Fatal(err)
	}
	if carried["run_state"] != string(heldSince) || carried["pressure"] != t0.Format(time.RFC3339Nano) {
		t.Fatalf("carried=%#v", carried)
	}

	// inputFactFromTelemetry falls back cleanly to present.UnchangedSince if record map omits the key.
	factSnapshot, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventA, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{
		{DeviceID: telemetryEvaluationTestChillerID, BusinessRevision: 1, EventID: telemetryEvaluationTestEventA, EvaluatedAt: t0, Snapshot: snapshot},
	})
	if err != nil {
		t.Fatal(err)
	}
	if factSnapshot.Inputs[telemetryEvaluationTestChillerID+"/run_state"].UnchangedSince != string(heldSince) {
		t.Fatalf("fact unchangedSince=%q, want %q", factSnapshot.Inputs[telemetryEvaluationTestChillerID+"/run_state"].UnchangedSince, heldSince)
	}
}
