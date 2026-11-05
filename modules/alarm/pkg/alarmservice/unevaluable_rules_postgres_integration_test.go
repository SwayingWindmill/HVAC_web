package alarmservice

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	unevaluableTenantID       = "01910000-c700-7000-8000-000000000001"
	unevaluableSiteID         = "01910000-c700-7000-8000-000000000002"
	unevaluablePolicyID       = "01910000-c700-7000-8000-000000000005"
	unevaluablePolicyRevision = "01910000-c700-7000-8000-000000000006"
	unevaluableAssignmentID   = "01910000-c700-7000-8000-000000000007"
	unevaluableEventChiller   = "01910000-c700-7000-8000-000000000011"
	unevaluableEventBTU       = "01910000-c700-7000-8000-000000000012"
)

func unevaluableSnapshot(deviceID string, revision int64, evaluatedAt time.Time, values ...telemetryapi.TelemetryKeyState) telemetryapi.DeviceObservationSnapshot {
	snapshot := telemetryBridgeSnapshot(deviceID, revision, evaluatedAt, values...)
	snapshot.TenantId = unevaluableTenantID
	snapshot.SiteId = unevaluableSiteID
	return snapshot
}

// #749: a rule that cannot be judged for 30 minutes raises one Site Alarm, which is not raised
// again while active and recovers once the rule is judged again.
func TestPostgresLongUnevaluableRuleRaisesOneSiteAlarmAndRecovers(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), unevaluableTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	startedAt := time.Date(2026, 11, 1, 6, 26, 0, 0, time.UTC)
	policy := telemetryBridgePolicy()
	policy.PolicyID = unevaluablePolicyID
	policy.PolicyRevisionID = unevaluablePolicyRevision
	sealAlarmPolicy(&policy)
	if err := store.ReleaseAlarmPolicyRevision(ctx, unevaluableTenantID, unevaluableSiteID, policy, startedAt.Add(-time.Minute), "test:unevaluable"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, unevaluableTenantID, unevaluableSiteID, AlarmPolicyAssignmentInput{
		AssignmentID: unevaluableAssignmentID, AssignmentRevision: 1, PolicyRevisionID: unevaluablePolicyRevision,
		SubjectType: "SITE", SubjectID: unevaluableSiteID, AssignedAt: startedAt.Add(-30 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:unevaluable",
	}); err != nil {
		t.Fatal(err)
	}
	chiller := unevaluableSnapshot(telemetryBridgeChillerID, 1, startedAt,
		presentTelemetryState("run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", startedAt),
		presentTelemetryState("cooling_capacity", "NUMBER", `420`, "GOOD", "FRESH", startedAt))
	decisions, err := store.EvaluateTelemetrySnapshot(ctx, unevaluableEventChiller, chiller, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].State.Status != EvaluationIndeterminate {
		t.Fatalf("the rule without its return-water reading should be unjudgeable, got %#v", decisions)
	}

	watch := func(at time.Time) (int, int) {
		t.Helper()
		raised, cleared, err := store.watchTenantUnevaluableRules(ctx, unevaluableTenantID, at, at.Add(-rulesUnevaluableAfter))
		if err != nil {
			t.Fatal(err)
		}
		return raised, cleared
	}
	if raised, cleared := watch(startedAt.Add(29 * time.Minute)); raised+cleared != 0 {
		t.Fatalf("a rule unjudgeable for 29 minutes raised or cleared: %d %d", raised, cleared)
	}
	raisedAt := startedAt.Add(31 * time.Minute)
	var listed bool
	if err := store.pool.QueryRow(context.Background(), `SELECT $1::uuid IN (SELECT * FROM alarm_runtime.tenants_with_unevaluable_rules($2))`, unevaluableTenantID, raisedAt.Add(-rulesUnevaluableAfter)).Scan(&listed); err != nil || !listed {
		t.Fatalf("the Tenant was not listed for the watchdog: %v %v", listed, err)
	}
	if raised, _ := watch(raisedAt); raised != 1 {
		t.Fatalf("a rule unjudgeable for 31 minutes did not raise: %d", raised)
	}
	if raised, _ := watch(raisedAt.Add(time.Minute)); raised != 0 {
		t.Fatalf("an active rules-unevaluable Alarm was raised again: %d", raised)
	}
	tx, err := store.beginTenantTransaction(ctx, unevaluableTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	var summary string
	var occurrences int
	if err := tx.QueryRow(ctx, `SELECT summary, occurrence_count FROM alarm_runtime.alarm_current WHERE tenant_id=$1 AND alarm_type='rules-unevaluable' AND condition='ACTIVE'`, unevaluableTenantID).Scan(&summary, &occurrences); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	if occurrences != 1 || !strings.Contains(summary, policy.Title) || !strings.Contains(summary, "缺输入") || !strings.Contains(summary, "31 分钟") {
		t.Fatalf("rules-unevaluable Alarm does not name the rule: %d %q", occurrences, summary)
	}

	judgedAt := raisedAt.Add(2 * time.Minute)
	btu := unevaluableSnapshot(telemetryBridgeBTUID, 1, judgedAt,
		presentTelemetryState("return_water_temperature", "NUMBER", `12`, "GOOD", "FRESH", judgedAt))
	chiller = unevaluableSnapshot(telemetryBridgeChillerID, 2, judgedAt,
		presentTelemetryState("run_state", "STRING", `"RUNNING"`, "GOOD", "FRESH", judgedAt),
		presentTelemetryState("cooling_capacity", "NUMBER", `420`, "GOOD", "FRESH", judgedAt))
	if _, err := store.EvaluateTelemetrySnapshot(ctx, unevaluableEventChiller+"", chiller, judgedAt); err != nil {
		t.Fatal(err)
	}
	decisions, err = store.EvaluateTelemetrySnapshot(ctx, unevaluableEventBTU, btu, judgedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].State.Status == EvaluationIndeterminate {
		t.Fatalf("the rule with all its readings is still unjudgeable: %#v", decisions)
	}
	if _, cleared := watch(judgedAt.Add(time.Minute)); cleared != 1 {
		t.Fatalf("rules-unevaluable Alarm did not recover: %d", cleared)
	}
	tx, err = store.beginTenantTransaction(ctx, unevaluableTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	var actions string
	if err := tx.QueryRow(ctx, `SELECT string_agg(o.action, ',' ORDER BY o.alarm_version) FROM alarm_runtime.notification_outbox o JOIN alarm_runtime.alarm_current a ON a.alarm_id=o.alarm_id WHERE o.tenant_id=$1 AND a.alarm_type='rules-unevaluable'`, unevaluableTenantID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	if actions != "CREATED,CLEARED" {
		t.Fatalf("rules-unevaluable Alarm was not told to Notification as raised and recovered: %q", actions)
	}
}
