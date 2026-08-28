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
			presentTelemetryState("chiller.run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", startedAt),
			presentTelemetryState("chiller.cooling_capacity", "NUMBER", `420`, "GOOD", "FRESH", startedAt)),
		telemetryEvaluationRecord(telemetryEvaluationTestBTUID, telemetryEvaluationTestEventB, 7, startedAt.Add(time.Second),
			presentTelemetryState("btu_meter.return_water_temperature", "NUMBER", `10.2`, "GOOD", "FRESH", startedAt.Add(time.Second))),
	}

	snapshot, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, records)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SubjectType != "SITE" || snapshot.SubjectID != telemetryEvaluationTestSiteID || snapshot.AsOf != "2026-08-28T12:00:01Z" {
		t.Fatalf("unexpected Site evaluation identity: %#v", snapshot)
	}
	if len(snapshot.Inputs) != 3 {
		t.Fatalf("expected three cross-device facts, got %#v", snapshot.Inputs)
	}
	returnFact := snapshot.Inputs["btu_meter.return_water_temperature"]
	if !returnFact.Present || returnFact.Value.Type != EvaluationValueNumber || returnFact.Value.Number != 10.2 || returnFact.Quality != "GOOD" {
		t.Fatalf("return-water fact lost canonical value metadata: %#v", returnFact)
	}
	if len(returnFact.Evidence) != 1 || returnFact.Evidence[0].Reference != telemetryEvaluationTestEventB+"#btu_meter.return_water_temperature" {
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
		presentTelemetryState("btu_meter.return_water_temperature", "NUMBER", `10.0`, "GOOD", "STALE", startedAt))
	snapshot, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	policy := validAlarmPolicy()
	policy.Raise = Condition{Kind: ConditionCompare, Input: "btu_meter.return_water_temperature", Operator: CompareLTE, Value: NumberValue(10.5)}
	policy.Clear = Condition{Kind: ConditionCompare, Input: "btu_meter.return_water_temperature", Operator: CompareGTE, Value: NumberValue(11.5)}
	sealAlarmPolicy(&policy)
	decision, err := EvaluatePolicy(policy, snapshot, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != EvaluationIndeterminate || decision.State.QualityBlocker != "STALE_INPUT:btu_meter.return_water_temperature" {
		t.Fatalf("stale canonical fact was not preserved as INDETERMINATE: %#v", decision)
	}

	record.Snapshot.Values[0] = presentTelemetryState("btu_meter.return_water_temperature", "NUMBER", `10.0`, "INVALID", "FRESH", startedAt)
	invalid, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = EvaluatePolicy(policy, invalid, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != EvaluationIndeterminate || decision.State.QualityBlocker != "INVALID_INPUT:btu_meter.return_water_temperature" {
		t.Fatalf("invalid canonical fact was not preserved as INDETERMINATE: %#v", decision)
	}

	record.Snapshot.EvaluationAvailability = telemetryapi.EvaluationAvailabilityUnavailable
	record.Snapshot.Values[0] = presentTelemetryState("btu_meter.return_water_temperature", "NUMBER", `10.0`, "GOOD", "FRESH", startedAt)
	unavailable, err := buildSiteEvaluationSnapshot(telemetryEvaluationTestEventB, telemetryEvaluationTestTenantID, telemetryEvaluationTestSiteID, []telemetryEvaluationInputRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = EvaluatePolicy(policy, unavailable, AlarmEvaluationState{}, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != EvaluationIndeterminate || decision.State.QualityBlocker != "UNTRUSTED_QUALITY:btu_meter.return_water_temperature:UNAVAILABLE" {
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
