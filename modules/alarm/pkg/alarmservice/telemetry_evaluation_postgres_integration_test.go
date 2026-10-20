package alarmservice

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	telemetryBridgeTenantID       = "01910000-b000-7000-8000-000000000001"
	telemetryBridgeSiteID         = "01910000-b000-7000-8000-000000000002"
	telemetryBridgeChillerID      = "01910000-b000-7000-8000-000000000003"
	telemetryBridgeBTUID          = "01910000-b000-7000-8000-000000000004"
	telemetryBridgePolicyID       = "01910000-b000-7000-8000-000000000005"
	telemetryBridgePolicyRevision = "01910000-b000-7000-8000-000000000006"
	telemetryBridgeAssignmentID   = "01910000-b000-7000-8000-000000000007"
	telemetryBridgeEventChiller1  = "01910000-b000-7000-8000-000000000011"
	telemetryBridgeEventBTU1      = "01910000-b000-7000-8000-000000000012"
	telemetryBridgeEventChiller2  = "01910000-b000-7000-8000-000000000013"
	telemetryBridgeEventBTU2      = "01910000-b000-7000-8000-000000000014"
)

func TestPostgresTelemetryBridgeMergesDevicesKeepsNewestFactsAndDrivesDurationClear(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), telemetryBridgeTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	startedAt := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	policy := telemetryBridgePolicy()
	if err := store.ReleaseAlarmPolicyRevision(ctx, telemetryBridgeTenantID, telemetryBridgeSiteID, policy, startedAt.Add(-time.Minute), "test:telemetry-bridge"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, telemetryBridgeTenantID, telemetryBridgeSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: telemetryBridgeAssignmentID, AssignmentRevision: 1, PolicyRevisionID: telemetryBridgePolicyRevision,
		SubjectType: "SITE", SubjectID: telemetryBridgeSiteID, AssignedAt: startedAt.Add(-30 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:telemetry-bridge",
	}); err != nil {
		t.Fatal(err)
	}

	chiller1 := telemetryBridgeSnapshot(telemetryBridgeChillerID, 1, startedAt,
		presentTelemetryState("run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", startedAt),
		presentTelemetryState("cooling_capacity", "NUMBER", `420`, "GOOD", "FRESH", startedAt))
	decisions, err := store.EvaluateTelemetrySnapshot(ctx, telemetryBridgeEventChiller1, chiller1, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].State.Status != EvaluationIndeterminate {
		t.Fatalf("single-device input should be incomplete, got %#v", decisions)
	}

	completeAt := startedAt.Add(time.Second)
	btu1 := telemetryBridgeSnapshot(telemetryBridgeBTUID, 1, completeAt,
		presentTelemetryState("return_water_temperature", "NUMBER", `10.2`, "GOOD", "FRESH", completeAt))
	decisions, err = store.EvaluateTelemetrySnapshot(ctx, telemetryBridgeEventBTU1, btu1, completeAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Effect != EvaluationEffectNone || decisions[0].State.CandidateSince == nil || *decisions[0].State.CandidateSince != completeAt.Format(time.RFC3339Nano) {
		t.Fatalf("cross-device facts did not start the 5-minute duration candidate: %#v", decisions)
	}

	newerAt := completeAt.Add(10 * time.Second)
	chiller2 := telemetryBridgeSnapshot(telemetryBridgeChillerID, 2, newerAt,
		presentTelemetryState("run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", newerAt),
		presentTelemetryState("cooling_capacity", "NUMBER", `430`, "GOOD", "FRESH", newerAt))
	if _, err := store.EvaluateTelemetrySnapshot(ctx, telemetryBridgeEventChiller2, chiller2, newerAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EvaluateTelemetrySnapshot(ctx, telemetryBridgeEventChiller1, chiller1, newerAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	records, _, err := store.loadSiteTelemetryEvaluationState(ctx, telemetryBridgeTenantID, telemetryBridgeSiteID)
	if err != nil {
		t.Fatal(err)
	}
	chillerRecord := telemetryRecordForDevice(records, telemetryBridgeChillerID)
	if chillerRecord.BusinessRevision != 2 {
		t.Fatalf("out-of-order telemetry regressed the authoritative Device fact: %#v", chillerRecord)
	}

	dueAt := completeAt.Add(5 * time.Minute)
	claims, err := store.ClaimDueEvaluations(ctx, telemetryBridgeTenantID, "telemetry-bridge-claim", dueAt, 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	claim, found := evaluationClaimForAssignment(claims, telemetryBridgeAssignmentID)
	if !found {
		t.Fatalf("5-minute candidate was not durably claimable: %#v", claims)
	}
	published, err := store.EvaluateClaim(ctx, claim, dueAt)
	if err != nil {
		t.Fatal(err)
	}
	if published.Effect != EvaluationEffectPublish || published.State.ActiveAlarmID == "" || published.State.Status != EvaluationMatched {
		t.Fatalf("durable Telemetry candidate did not publish Alarm: %#v", published)
	}
	raisedAlarm, err := store.Get(ctx, telemetryBridgeTenantID, telemetryBridgeSiteID, published.State.ActiveAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if !alarmEvidenceHasReference(raisedAlarm.Evidence, telemetryBridgeEventChiller2+"#run_state") ||
		!alarmEvidenceHasReference(raisedAlarm.Evidence, telemetryBridgeEventBTU1+"#return_water_temperature") {
		t.Fatalf("Telemetry-derived Alarm did not retain authoritative Device snapshot evidence: %#v", raisedAlarm.Evidence)
	}

	clearAt := dueAt.Add(time.Second)
	btu2 := telemetryBridgeSnapshot(telemetryBridgeBTUID, 2, clearAt,
		presentTelemetryState("return_water_temperature", "NUMBER", `11.7`, "GOOD", "FRESH", clearAt))
	decisions, err = store.EvaluateTelemetrySnapshot(ctx, telemetryBridgeEventBTU2, btu2, clearAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Effect != EvaluationEffectClear || decisions[0].State.ActiveAlarmID != "" || decisions[0].State.Status != EvaluationNotMatched {
		t.Fatalf("11.5C clear hysteresis did not clear the Telemetry-derived Alarm: %#v", decisions)
	}
	alarm, err := store.Get(ctx, telemetryBridgeTenantID, telemetryBridgeSiteID, published.State.ActiveAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if alarm.Condition != alarmmodel.ConditionCleared {
		t.Fatalf("Telemetry clear did not update authoritative Alarm incident: %#v", alarm)
	}
}

func telemetryBridgePolicy() AlarmPolicyRevision {
	policy := validAlarmPolicy()
	policy.PolicyID = telemetryBridgePolicyID
	policy.PolicyRevisionID = telemetryBridgePolicyRevision
	policy.AlarmType = "CHILLED_WATER_RETURN_TOO_COLD"
	policy.SourceReference = "alarm-policy:central-plant-return-water"
	policy.Title = "Chilled-water return temperature too low"
	policy.Summary = "Return water remains at or below 10.5C while the chiller is running at at least 30% of 1200 kW rated capacity."
	policy.FreshnessSeconds = 900
	policy.TriggerMode = TriggerDuration
	policy.DurationSeconds = 300
	policy.Raise = Condition{Kind: ConditionAnd, Children: []Condition{
		{Kind: ConditionCompare, Input: telemetryBridgeChillerID + "/run_state", Operator: CompareEQ, Value: StringValue("RUNNING")},
		{Kind: ConditionCompare, Input: telemetryBridgeChillerID + "/cooling_capacity", Operator: CompareGTE, Value: NumberValue(360)},
		{Kind: ConditionCompare, Input: telemetryBridgeBTUID + "/return_water_temperature", Operator: CompareLTE, Value: NumberValue(10.5)},
	}}
	policy.Clear = Condition{Kind: ConditionOr, Children: []Condition{
		{Kind: ConditionCompare, Input: telemetryBridgeBTUID + "/return_water_temperature", Operator: CompareGTE, Value: NumberValue(11.5)},
		{Kind: ConditionCompare, Input: telemetryBridgeChillerID + "/run_state", Operator: CompareNE, Value: StringValue("RUNNING")},
	}}
	sealAlarmPolicy(&policy)
	return policy
}

func telemetryBridgeSnapshot(deviceID string, revision int64, evaluatedAt time.Time, values ...telemetryapi.TelemetryKeyState) telemetryapi.DeviceObservationSnapshot {
	return telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1, DeviceId: telemetryapi.UUIDv7(deviceID), TenantId: telemetryBridgeTenantID, SiteId: telemetryBridgeSiteID,
		BusinessRevision: telemetryapi.BusinessRevision(revision), EvaluatedAt: telemetryapi.Instant(evaluatedAt.Format(time.RFC3339Nano)),
		EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable, Values: values,
	}
}

func telemetryRecordForDevice(records []telemetryEvaluationInputRecord, deviceID string) telemetryEvaluationInputRecord {
	for _, record := range records {
		if record.DeviceID == deviceID {
			return record
		}
	}
	return telemetryEvaluationInputRecord{}
}

func alarmEvidenceHasReference(evidence []alarmmodel.EvidenceReference, reference string) bool {
	for _, item := range evidence {
		if item.Reference == reference {
			return true
		}
	}
	return false
}

// #616: each reading carries since when it has held its value: the same value keeps the time, a
// different one starts it again at its own sample time.
func TestPostgresTelemetryInputRemembersSinceWhenAReadingHeldItsValue(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	const (
		siteID = "01910000-b000-7000-8000-000000000032"
		device = "01910000-b000-7000-8000-000000000033"
	)
	ctx := identitycontext.WithTenantID(context.Background(), telemetryBridgeTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	since := func(revision int64, at time.Time, value string, eventID string) string {
		t.Helper()
		snapshot := telemetryBridgeSnapshot(device, revision, at, presentTelemetryState("ambient_temperature", "NUMBER", value, "GOOD", "FRESH", at))
		snapshot.SiteId = siteID
		if _, err := store.EvaluateTelemetrySnapshot(ctx, eventID, snapshot, at); err != nil {
			t.Fatal(err)
		}
		records, _, err := store.loadSiteTelemetryEvaluationState(ctx, telemetryBridgeTenantID, siteID)
		if err != nil {
			t.Fatal(err)
		}
		built, err := buildSiteEvaluationSnapshot(eventID, telemetryBridgeTenantID, siteID, records)
		if err != nil {
			t.Fatal(err)
		}
		return built.Inputs[device+"/ambient_temperature"].UnchangedSince
	}
	if got := since(1, first, `21.5`, "01910000-b000-7000-8000-000000000041"); got != first.Format(time.RFC3339Nano) {
		t.Fatalf("first reading starts at its sample time, got %q", got)
	}
	if got := since(2, first.Add(time.Minute), `21.5`, "01910000-b000-7000-8000-000000000042"); got != first.Format(time.RFC3339Nano) {
		t.Fatalf("the same value kept since %q, want the first sample", got)
	}
	moved := first.Add(2 * time.Minute)
	if got := since(3, moved, `21.7`, "01910000-b000-7000-8000-000000000043"); got != moved.Format(time.RFC3339Nano) {
		t.Fatalf("a changed value starts again at its sample, got %q", got)
	}
}
