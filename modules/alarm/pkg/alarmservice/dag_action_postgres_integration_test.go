package alarmservice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

func TestPostgresDAGTopologicalEvaluationOutboxRelayAndDeadLetterReplay(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}

	const (
		tenantID       = "01910000-c100-7000-8000-000000000001"
		otherTenantID  = "01910000-c100-7000-8000-000000000002"
		siteID         = "01910000-c200-7000-8000-000000000001"
		upDevID        = "01910000-c300-7000-8000-000000000001"
		downDevID      = "01910000-c300-7000-8000-000000000002"
		targetCmdDevID = "01910000-c300-7000-8000-000000000003"
		upPolicyID     = "01910000-c400-7000-8000-000000000001"
		upRevID        = "01910000-c400-7000-8000-000000000002"
		upAssignID     = "01910000-c500-7000-8000-000000000001"
		downPolicyID   = "01910000-c400-7000-8000-000000000003"
		downRevID      = "01910000-c400-7000-8000-000000000004"
		downAssignID   = "01910000-c500-7000-8000-000000000002"
	)

	ctx := identitycontext.WithTenantID(context.Background(), tenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	baseAt := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)

	upPolicy := validAlarmPolicy()
	upPolicy.PolicyID = upPolicyID
	upPolicy.PolicyRevisionID = upRevID
	upPolicy.AlarmType = "dag-upstream-dp"
	upPolicy.SourceReference = "alarm-rules:dag-pg/up"
	upPolicy.Title = "上游压差过低"
	upPolicy.Summary = "压差低于阈值"
	upPolicy.Raise = Condition{Kind: ConditionCompare, Input: upDevID + "/dp", Operator: CompareLT, Value: NumberValue(30)}
	upPolicy.Clear = Condition{Kind: ConditionCompare, Input: upDevID + "/dp", Operator: CompareGTE, Value: NumberValue(40)}
	sealAlarmPolicy(&upPolicy)

	downPolicy := validAlarmPolicy()
	downPolicy.PolicyID = downPolicyID
	downPolicy.PolicyRevisionID = downRevID
	downPolicy.AlarmType = "dag-downstream-freeze"
	downPolicy.SourceReference = "alarm-rules:dag-pg/down"
	downPolicy.Title = "下游防冻保护"
	downPolicy.Summary = "下游出水低温触发防冻联动"
	downPolicy.DependsOnRuleIDs = []string{upAssignID}
	downPolicy.DependencyGate = DependencyGateAllActive
	downPolicy.Raise = Condition{Kind: ConditionCompare, Input: downDevID + "/temp", Operator: CompareLT, Value: NumberValue(4)}
	downPolicy.Clear = Condition{Kind: ConditionCompare, Input: downDevID + "/temp", Operator: CompareGTE, Value: NumberValue(6)}
	downPolicy.Actions = []RuleAction{
		{
			Kind:            RuleActionProtectionCommand,
			TriggerOn:       ActionTriggerRaise,
			TargetDeviceID:  targetCmdDevID,
			TargetPointCode: "cmd_stop",
			Capability:      "STOP",
		},
		{
			Kind:       RuleActionWebhook,
			TriggerOn:  ActionTriggerBoth,
			WebhookURL: "https://127.0.0.1/webhook?token=topsecret",
			SecretRef:  "pg-webhook-secret",
		},
	}
	sealAlarmPolicy(&downPolicy)

	if err := store.ReleaseAlarmPolicyRevision(ctx, tenantID, siteID, upPolicy, baseAt.Add(-time.Minute), "test:dag"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, tenantID, siteID, AlarmPolicyAssignmentInput{
		AssignmentID: upAssignID, AssignmentRevision: 1, PolicyRevisionID: upRevID,
		SubjectType: "SITE", SubjectID: siteID, AssignedAt: baseAt.Add(-50 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:dag",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseAlarmPolicyRevision(ctx, tenantID, siteID, downPolicy, baseAt.Add(-40*time.Second), "test:dag"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignAlarmPolicyRevision(ctx, tenantID, siteID, AlarmPolicyAssignmentInput{
		AssignmentID: downAssignID, AssignmentRevision: 1, PolicyRevisionID: downRevID,
		SubjectType: "SITE", SubjectID: siteID, AssignedAt: baseAt.Add(-30 * time.Second).Format(time.RFC3339Nano), AssignedBy: "test:dag",
	}); err != nil {
		t.Fatal(err)
	}

	makeTelemetry := func(devID string, rev int64, at time.Time, key, rawVal string) telemetryapi.DeviceObservationSnapshot {
		s := telemetryBridgeSnapshot(devID, rev, at, presentTelemetryState(key, "NUMBER", rawVal, "GOOD", "FRESH", at))
		s.TenantId = tenantID
		s.SiteId = siteID
		return s
	}

	// Step 1: downstream reading satisfies local threshold (2.0 < 4), but upstream is NOT active -> gated off.
	downSnap1 := makeTelemetry(downDevID, 1, baseAt, "temp", `2.0`)
	decisions1, err := store.EvaluateTelemetrySnapshot(ctx, "01910000-c600-7000-8000-000000000001", downSnap1, baseAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions1) != 1 || decisions1[0].Effect != EvaluationEffectNone {
		t.Fatalf("expected gated downstream rule to suppress while upstream inactive: %#v", decisions1)
	}

	// Step 2: upstream reading arrives (20 < 30). Upstream executes FIRST in topological order and fires;
	// transitive relevance re-evaluates downstream in the SAME call, sees fresh upstreamActive, and fires + enqueues 2 actions.
	t2 := baseAt.Add(10 * time.Second)
	upSnap1 := makeTelemetry(upDevID, 1, t2, "dp", `20.0`)
	decisions2, err := store.EvaluateTelemetrySnapshot(ctx, "01910000-c600-7000-8000-000000000002", upSnap1, t2)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions2) != 2 || decisions2[0].Effect != EvaluationEffectPublish || decisions2[1].Effect != EvaluationEffectPublish {
		t.Fatalf("expected upstream then downstream PUBLISH in same snapshot, got %#v", decisions2)
	}

	registry := observability.NewRegistry()
	var cmdRequests []RuleActionProtectionCommandRequest
	relay, err := NewRuleActionRelay(RuleActionRelayConfig{
		Store:   store,
		Metrics: registry,
		Now:     func() time.Time { return t2 },
		SubmitProtectionCommand: func(_ context.Context, req RuleActionProtectionCommandRequest) error {
			cmdRequests = append(cmdRequests, req)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// RelayOnce delivers PROTECTION_COMMAND (PROTECTION_RULE) and dead-letters SSRF 127.0.0.1 webhook cleanly.
	res, err := relay.RelayOnce(context.Background())
	if err != nil {
		t.Fatalf("RelayOnce failed: %v", err)
	}
	if res.Delivered != 1 || res.DeadLettered != 1 {
		t.Fatalf("unexpected relay result: %#v", res)
	}
	if len(cmdRequests) != 1 || cmdRequests[0].PriorityTier != commandmodel.PriorityProtectionRule || cmdRequests[0].TargetDeviceID != targetCmdDevID {
		t.Fatalf("expected 1 PROTECTION_RULE command for %s, got %#v", targetCmdDevID, cmdRequests)
	}

	// RLS isolation check: other tenant sees 0 dead letters.
	otherCtx := identitycontext.WithTenantID(context.Background(), otherTenantID)
	otherList, err := store.ListRuleActionDeadLetters(otherCtx, otherTenantID, siteID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList.Items) != 0 {
		t.Fatalf("RLS leaked dead letters to wrong tenant: %#v", otherList.Items)
	}

	dlList, err := store.ListRuleActionDeadLetters(ctx, tenantID, siteID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(dlList.Items) != 1 || dlList.Items[0].LastErrorCode != "ssrf_blocked" {
		t.Fatalf("expected 1 ssrf_blocked dead letter, got %#v", dlList.Items)
	}
	if strings.Contains(dlList.Items[0].TargetRef, "topsecret") || strings.Contains(dlList.Items[0].TargetRef, "pg-webhook-secret") {
		t.Fatalf("dead-letter TargetRef leaked query/secret: %q", dlList.Items[0].TargetRef)
	}

	// Replay dead letter -> PENDING, then clear downstream alarm -> BOTH webhook enqueued on CLEAR too.
	replayAt := t2.Add(5 * time.Second)
	if err := store.ReplayRuleActionDeadLetter(ctx, tenantID, siteID, dlList.Items[0].OutboxID, replayAt); err != nil {
		t.Fatalf("ReplayRuleActionDeadLetter failed: %v", err)
	}
	afterReplay, err := store.ListRuleActionDeadLetters(ctx, tenantID, siteID, 20)
	if err != nil || len(afterReplay.Items) != 0 {
		t.Fatalf("expected 0 dead letters after replay, got %#v err=%v", afterReplay.Items, err)
	}
	if err := store.ReplayRuleActionDeadLetter(ctx, tenantID, siteID, dlList.Items[0].OutboxID, replayAt); !errors.Is(err, alarmmodel.ErrInvalidOperation) {
		t.Fatalf("expected ErrInvalidOperation replaying non-dead-letter entry, got %v", err)
	}

	t3 := t2.Add(20 * time.Second)
	downClear := makeTelemetry(downDevID, 2, t3, "temp", `8.0`)
	decisions3, err := store.EvaluateTelemetrySnapshot(ctx, "01910000-c600-7000-8000-000000000003", downClear, t3)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions3) != 1 || decisions3[0].Effect != EvaluationEffectClear {
		t.Fatalf("expected downstream alarm to clear on recovery, got %#v", decisions3)
	}

	pendingCount, dlqCount, err := store.CountRuleActionOutboxStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pendingCount != 2 || dlqCount != 0 {
		t.Fatalf("expected 2 pending (replayed RAISE webhook + new CLEAR webhook) and 0 dead_letter, got pending=%d dlq=%d", pendingCount, dlqCount)
	}

	// Check metrics emitted by RelayOnce earlier.
	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `hvac_alarm_action_dead_letter_total{reason_family="ssrf_blocked"} 1`) {
		t.Fatalf("expected action dead-letter metric in:\n%s", rec.Body.String())
	}
}
