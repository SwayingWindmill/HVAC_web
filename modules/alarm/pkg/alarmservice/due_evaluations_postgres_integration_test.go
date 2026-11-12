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
	latePolicyID            = "01910000-c000-7000-8000-000000000015"
	latePolicyRevisionID    = "01910000-c000-7000-8000-000000000016"
	lateAssignmentID        = "01910000-c000-7000-8000-000000000017"
	lateSiteID              = "01910000-c000-7000-8000-000000000022"
	lateEvent               = "01910000-c000-7000-8000-000000000021"
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
	if active := siteAlarms(t, ctx, store, silenceSiteID, alarmmodel.ConditionActive); len(active) != 1 {
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
	if cleared := siteAlarms(t, ctx, store, silenceSiteID, alarmmodel.ConditionCleared); len(cleared) != 1 {
		t.Fatalf("expected the silence Alarm cleared, got %#v", cleared)
	}
}

// #615: a rule published while the Site is already silent has never been evaluated, and no
// reading will come to evaluate it; the pass evaluates it with the Site's last readings.
func TestPostgresScheduledPassEvaluatesARulePublishedWhileTheSiteIsSilent(t *testing.T) {
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

	lastReading := time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)
	snapshot := silenceSnapshot(1, lastReading)
	snapshot.SiteId = lateSiteID
	if _, err := store.EvaluateTelemetrySnapshot(ctx, lateEvent, snapshot, lastReading); err != nil {
		t.Fatal(err)
	}
	policy := silencePolicy()
	policy.PolicyID, policy.PolicyRevisionID = latePolicyID, latePolicyRevisionID
	policy.SourceReference = "alarm-rules:late-site/reports-interrupted"
	sealAlarmPolicy(&policy)
	publishedAt := lastReading.Add(time.Hour)
	if err := store.ReleaseAlarmPolicyRevision(ctx, silenceTenantID, lateSiteID, policy, publishedAt, "test:silence"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, silenceTenantID, lateSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: lateAssignmentID, AssignmentRevision: 1, PolicyRevisionID: latePolicyRevisionID,
		SubjectType: "SITE", SubjectID: lateSiteID, AssignedAt: publishedAt.Format(time.RFC3339Nano), AssignedBy: "test:silence",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.RunDueEvaluations(context.Background(), "silence-test", publishedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active := siteAlarms(t, ctx, store, lateSiteID, alarmmodel.ConditionActive); len(active) != 1 {
		t.Fatalf("a rule published during the silence was not raised: %#v", active)
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

func siteAlarms(t *testing.T, ctx context.Context, store *PostgresStore, siteID string, condition alarmmodel.Condition) []alarmmodel.Alarm {
	t.Helper()
	listed, err := store.List(ctx, silenceTenantID, siteID, Filter{Limit: 10})
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

// #659: readings evaluate only the rules reading their device, so a new version of a rule whose
// device is silent waits for no reading; the pass evaluates it with the Site's last readings.
func TestPostgresScheduledPassAppliesANewRuleVersionWhileItsDeviceIsSilent(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	const (
		siteID       = "01910000-c000-7000-8000-000000000032"
		otherDevice  = "01910000-c000-7000-8000-000000000033"
		assignmentID = "01910000-c000-7000-8000-000000000037"
		revisionTwo  = "01910000-c000-7000-8000-000000000038"
	)
	ctx := identitycontext.WithTenantID(context.Background(), silenceTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	lastReading := time.Date(2026, 10, 20, 6, 0, 0, 0, time.UTC)
	release := func(revisionID string, revision uint64, at time.Time) {
		t.Helper()
		policy := silencePolicy()
		policy.PolicyID = "01910000-c000-7000-8000-000000000035"
		policy.PolicyRevisionID, policy.Revision = revisionID, revision
		policy.SourceReference = "alarm-rules:version-site/reports-interrupted"
		sealAlarmPolicy(&policy)
		if err := store.ReleaseAlarmPolicyRevision(ctx, silenceTenantID, siteID, policy, at, "test:silence"); err != nil {
			t.Fatal(err)
		}
		if err := store.AssignAlarmPolicyRevision(ctx, silenceTenantID, siteID, AlarmPolicyAssignmentInput{
			AssignmentID: assignmentID, AssignmentRevision: revision, PolicyRevisionID: revisionID,
			SubjectType: "SITE", SubjectID: siteID, AssignedAt: at.Format(time.RFC3339Nano), AssignedBy: "test:silence",
		}); err != nil {
			t.Fatal(err)
		}
	}
	release("01910000-c000-7000-8000-000000000036", 1, lastReading.Add(-time.Minute))
	snapshot := silenceSnapshot(1, lastReading)
	snapshot.SiteId = siteID
	if _, err := store.EvaluateTelemetrySnapshot(ctx, "01910000-c000-7000-8000-000000000041", snapshot, lastReading); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RunDueEvaluations(context.Background(), "silence-test", lastReading.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}

	published := lastReading.Add(20 * time.Minute)
	release(revisionTwo, 2, published)
	other := silenceSnapshot(1, published.Add(time.Minute))
	other.SiteId, other.DeviceId = siteID, otherDevice
	if _, err := store.EvaluateTelemetrySnapshot(ctx, "01910000-c000-7000-8000-000000000042", other, published.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RunDueEvaluations(context.Background(), "silence-test", published.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	tx, err := store.beginTenantTransaction(ctx, silenceTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	persisted, found, err := loadPersistedEvaluation(ctx, tx, silenceTenantID, siteID, assignmentID, false)
	if err != nil || !found {
		t.Fatalf("the rule has no evaluation: %v", err)
	}
	if persisted.State.PolicyRevisionID != revisionTwo {
		t.Fatalf("the new version was not applied while its device is silent: evaluated with %s", persisted.State.PolicyRevisionID)
	}
}

// #800: an unacknowledged reading-gap Alarm older than 24 hours escalates once by one severity step
// (WARNING -> MAJOR, MAJOR -> CRITICAL) and emits SEVERITY_CHANGED on the outbox; acknowledging
// blocks escalation, and recovery emits CLEARED for Notification's daily summary.
func TestPostgresReadingGapEscalatesOnceAfter24HoursUnacknowledgedAndEmitsOutboxEvents(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	const (
		siteID               = "01910000-c000-7000-8000-000000000052"
		warnDeviceID         = "01910000-c000-7000-8000-000000000053"
		majorDeviceID        = "01910000-c000-7000-8000-000000000054"
		ackedDeviceID        = "01910000-c000-7000-8000-000000000055"
		warnPolicyID         = "01910000-c000-7000-8000-000000000061"
		warnPolicyRevisionID = "01910000-c000-7000-8000-000000000062"
		warnAssignmentID     = "01910000-c000-7000-8000-000000000063"
		majPolicyID          = "01910000-c000-7000-8000-000000000064"
		majPolicyRevisionID  = "01910000-c000-7000-8000-000000000065"
		majAssignmentID      = "01910000-c000-7000-8000-000000000066"
		ackPolicyID          = "01910000-c000-7000-8000-000000000067"
		ackPolicyRevisionID  = "01910000-c000-7000-8000-000000000068"
		ackAssignmentID      = "01910000-c000-7000-8000-000000000069"
	)
	ctx := identitycontext.WithTenantID(context.Background(), silenceTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	relay, err := OpenNotificationRelay(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	base := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	registerGapRule := func(policyID, revisionID, assignmentID, deviceID, alarmType, title string, severity alarmmodel.Severity) {
		t.Helper()
		policy := silencePolicy()
		policy.PolicyID = policyID
		policy.PolicyRevisionID = revisionID
		policy.AlarmType = alarmType
		policy.SourceReference = "alarm-rules:gap-site/" + alarmType
		policy.Title = title
		policy.Summary = title + "。"
		policy.Severity = severity
		low, high := -1e12, 1e12
		policy.Raise = Condition{Kind: ConditionNoData, Input: deviceID + "/supply_temp", AgeSeconds: 900}
		policy.Clear = Condition{Kind: ConditionRange, Input: deviceID + "/supply_temp", Minimum: &low, Maximum: &high}
		sealAlarmPolicy(&policy)
		if err := store.ReleaseAlarmPolicyRevision(ctx, silenceTenantID, siteID, policy, base.Add(-time.Minute), "test:gap"); err != nil {
			t.Fatal(err)
		}
		if err := store.AssignAlarmPolicyRevision(ctx, silenceTenantID, siteID, AlarmPolicyAssignmentInput{
			AssignmentID: assignmentID, AssignmentRevision: 1, PolicyRevisionID: revisionID,
			SubjectType: "SITE", SubjectID: siteID, AssignedAt: base.Add(-30 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:gap",
		}); err != nil {
			t.Fatal(err)
		}
		snapshot := silenceSnapshot(1, base)
		snapshot.SiteId = siteID
		snapshot.DeviceId = telemetryapi.UUIDv7(deviceID)
		eventID, err := store.newID(base)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.EvaluateTelemetrySnapshot(ctx, eventID, snapshot, base); err != nil {
			t.Fatal(err)
		}
	}

	registerGapRule(warnPolicyID, warnPolicyRevisionID, warnAssignmentID, warnDeviceID, "reading-gap/chiller-a/supply-temp", "A 机组：读数长时间没有更新", alarmmodel.SeverityWarning)
	registerGapRule(majPolicyID, majPolicyRevisionID, majAssignmentID, majorDeviceID, "reading-gap/meter-uid2/active-energy", "D 机组电表：读数长时间没有更新", alarmmodel.SeverityMajor)
	registerGapRule(ackPolicyID, ackPolicyRevisionID, ackAssignmentID, ackedDeviceID, "reading-gap/chiller-b/supply-temp", "B 机组：读数长时间没有更新", alarmmodel.SeverityWarning)

	raisedAt := base.Add(15 * time.Minute)
	raisedPass, err := store.RunDueEvaluations(context.Background(), "gap-test", raisedAt)
	if err != nil {
		t.Fatal(err)
	}
	if raisedPass.Raised != 3 {
		t.Fatalf("expected 3 raised reading-gap alarms, got %#v", raisedPass)
	}

	active := siteAlarms(t, ctx, store, siteID, alarmmodel.ConditionActive)
	if len(active) != 3 {
		t.Fatalf("expected 3 active alarms, got %d", len(active))
	}
	byType := map[string]alarmmodel.Alarm{}
	for _, alarm := range active {
		byType[alarm.AlarmType] = alarm
	}
	ackTarget := byType["reading-gap/chiller-b/supply-temp"]
	if _, err := store.Apply(ctx, silenceTenantID, siteID, ackTarget.AlarmID, Mutation{
		Operation: alarmmodel.OperationAcknowledge, ExpectedVersion: ackTarget.Version,
		Reason: "现场排查中", ActorType: "USER", ActorID: "operator-1",
		PolicyRevision: ackTarget.RuleRevision, CorrelationID: "ack-gap-b",
		OccurredAt: raisedAt.Add(time.Hour).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	// Just before 24h from first_occurred_at: nothing escalates.
	before24h, err := store.RunDueEvaluations(context.Background(), "gap-test", raisedAt.Add(24*time.Hour-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if before24h.Escalated != 0 {
		t.Fatalf("escalated before 24h elapsed: %#v", before24h)
	}

	// At 24h: the two unacknowledged reading-gap alarms escalate (WARNING -> MAJOR, MAJOR -> CRITICAL);
	// the acknowledged one stays WARNING.
	at24h := raisedAt.Add(24 * time.Hour)
	escalatedPass, err := store.RunDueEvaluations(context.Background(), "gap-test", at24h)
	if err != nil {
		t.Fatal(err)
	}
	if escalatedPass.Escalated != 2 || escalatedPass.Failed != 0 {
		t.Fatalf("expected 2 escalations at 24h, got %#v", escalatedPass)
	}

	// Running again later must NOT escalate WARNING -> MAJOR a second time into CRITICAL.
	repeatPass, err := store.RunDueEvaluations(context.Background(), "gap-test", at24h.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if repeatPass.Escalated != 0 {
		t.Fatalf("escalated a second time: %#v", repeatPass)
	}

	after := siteAlarms(t, ctx, store, siteID, alarmmodel.ConditionActive)
	afterByType := map[string]alarmmodel.Alarm{}
	for _, alarm := range after {
		afterByType[alarm.AlarmType] = alarm
	}
	if got := afterByType["reading-gap/chiller-a/supply-temp"].CurrentSeverity; got != alarmmodel.SeverityMajor {
		t.Fatalf("unacknowledged WARNING alarm severity = %s, want MAJOR", got)
	}
	if got := afterByType["reading-gap/meter-uid2/active-energy"].CurrentSeverity; got != alarmmodel.SeverityCritical {
		t.Fatalf("unacknowledged MAJOR alarm severity = %s, want CRITICAL", got)
	}
	if got := afterByType["reading-gap/chiller-b/supply-temp"].CurrentSeverity; got != alarmmodel.SeverityWarning {
		t.Fatalf("acknowledged WARNING alarm severity = %s, want WARNING", got)
	}

	// Recovering the meter reading-gap alarm emits CLEARED to the notification outbox.
	recoveredAt := at24h.Add(2 * time.Hour)
	recoverSnapshot := silenceSnapshot(2, recoveredAt)
	recoverSnapshot.SiteId = siteID
	recoverSnapshot.DeviceId = telemetryapi.UUIDv7(majorDeviceID)
	recoverEventID, err := store.newID(recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EvaluateTelemetrySnapshot(ctx, recoverEventID, recoverSnapshot, recoveredAt); err != nil {
		t.Fatal(err)
	}

	outboxActions := map[string][]NotificationAction{}
	for range 20 {
		claimed, err := relay.Claim(context.Background(), "gap-relay-test", recoveredAt.Add(time.Minute), 30*time.Second)
		if err == ErrNotFound {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if claimed.SiteID == siteID {
			outboxActions[claimed.Attributes["alarmType"]] = append(outboxActions[claimed.Attributes["alarmType"]], claimed.Action)
		}
		if err := relay.Complete(context.Background(), claimed.SourceEventID, claimed.LeaseOwner, claimed.LeaseFence, recoveredAt.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	wantMeterActions := []NotificationAction{NotificationCreated, NotificationSeverityChanged, NotificationCleared}
	if got := outboxActions["reading-gap/meter-uid2/active-energy"]; len(got) != len(wantMeterActions) || got[0] != wantMeterActions[0] || got[1] != wantMeterActions[1] || got[2] != wantMeterActions[2] {
		t.Fatalf("meter outbox actions = %#v, want %#v", got, wantMeterActions)
	}
	wantWarnActions := []NotificationAction{NotificationCreated, NotificationSeverityChanged}
	if got := outboxActions["reading-gap/chiller-a/supply-temp"]; len(got) != len(wantWarnActions) || got[0] != wantWarnActions[0] || got[1] != wantWarnActions[1] {
		t.Fatalf("warning gap outbox actions = %#v, want %#v", got, wantWarnActions)
	}
}
