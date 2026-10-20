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
	silenceTenantID         = "01910000-c000-7000-8000-000000000001"
	silenceSiteID           = "01910000-c000-7000-8000-000000000002"
	silenceDeviceID         = "01910000-c000-7000-8000-000000000003"
	silencePolicyID         = "01910000-c000-7000-8000-000000000005"
	silencePolicyRevisionID = "01910000-c000-7000-8000-000000000006"
	silenceAssignmentID     = "01910000-c000-7000-8000-000000000007"
	silenceEventFirst       = "01910000-c000-7000-8000-000000000011"
	silenceEventBack        = "01910000-c000-7000-8000-000000000012"
)

// #615: a Site that stops reporting sends no telemetry, so only the scheduled pass can notice;
// it raises once the silence lasts, and the next reading clears it.
func TestPostgresScheduledPassRaisesASilentSiteAndTheNextReadingClearsIt(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), silenceTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	lastReading := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	if err := store.ReleaseAlarmPolicyRevision(ctx, silenceTenantID, silenceSiteID, silencePolicy(), lastReading.Add(-time.Minute), "test:silence"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, silenceTenantID, silenceSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: silenceAssignmentID, AssignmentRevision: 1, PolicyRevisionID: silencePolicyRevisionID,
		SubjectType: "SITE", SubjectID: silenceSiteID, AssignedAt: lastReading.Add(-30 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:silence",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EvaluateTelemetrySnapshot(ctx, silenceEventFirst, silenceSnapshot(1, lastReading), lastReading); err != nil {
		t.Fatal(err)
	}

	quiet, err := store.RunDueEvaluations(context.Background(), "silence-test", lastReading.Add(899*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Raised != 0 {
		t.Fatalf("raised before the silence lasted 15 minutes: %#v", quiet)
	}

	silent, err := store.RunDueEvaluations(context.Background(), "silence-test", lastReading.Add(900*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if silent.Raised != 1 || silent.Failed != 0 {
		t.Fatalf("15 minutes without a reading did not raise: %#v", silent)
	}
	if active := silenceAlarms(t, ctx, store, alarmmodel.ConditionActive); len(active) != 1 {
		t.Fatalf("expected one active silence Alarm, got %#v", active)
	}

	back := lastReading.Add(20 * time.Minute)
	decisions, err := store.EvaluateTelemetrySnapshot(ctx, silenceEventBack, silenceSnapshot(2, back), back)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Effect != EvaluationEffectClear {
		t.Fatalf("the next reading did not clear the silence Alarm: %#v", decisions)
	}
	if cleared := silenceAlarms(t, ctx, store, alarmmodel.ConditionCleared); len(cleared) != 1 {
		t.Fatalf("expected the silence Alarm cleared, got %#v", cleared)
	}
}

func silencePolicy() AlarmPolicyRevision {
	policy := validAlarmPolicy()
	policy.PolicyID = silencePolicyID
	policy.PolicyRevisionID = silencePolicyRevisionID
	policy.AlarmType = "reports-interrupted"
	policy.SourceReference = "alarm-rules:silence-site/reports-interrupted"
	policy.Title = "网关上报中断"
	policy.Summary = "站点 15 分钟没有收到任何读数。"
	policy.QualityPolicy = EvaluationQualityUsable
	policy.FreshnessSeconds = 900
	policy.TriggerMode = TriggerSimple
	policy.DurationSeconds = 0
	low, high := -1e12, 1e12
	policy.Raise = Condition{Kind: ConditionNoData, Input: silenceDeviceID + "/supply_temp", AgeSeconds: 900}
	policy.Clear = Condition{Kind: ConditionRange, Input: silenceDeviceID + "/supply_temp", Minimum: &low, Maximum: &high}
	sealAlarmPolicy(&policy)
	return policy
}

func silenceSnapshot(revision int64, at time.Time) telemetryapi.DeviceObservationSnapshot {
	return telemetryapi.DeviceObservationSnapshot{
		SchemaVersion: 1, DeviceId: telemetryapi.UUIDv7(silenceDeviceID), TenantId: silenceTenantID, SiteId: silenceSiteID,
		BusinessRevision: telemetryapi.BusinessRevision(revision), EvaluatedAt: telemetryapi.Instant(at.Format(time.RFC3339Nano)),
		EvaluationAvailability: telemetryapi.EvaluationAvailabilityAvailable,
		Values:                 []telemetryapi.TelemetryKeyState{presentTelemetryState("supply_temp", "NUMBER", `7.1`, "GOOD", "FRESH", at)},
	}
}

func silenceAlarms(t *testing.T, ctx context.Context, store *PostgresStore, condition alarmmodel.Condition) []alarmmodel.Alarm {
	t.Helper()
	listed, err := store.List(ctx, silenceTenantID, silenceSiteID, Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	matching := []alarmmodel.Alarm{}
	for _, alarm := range listed.Items {
		if alarm.Condition == condition {
			matching = append(matching, alarm)
		}
	}
	return matching
}
