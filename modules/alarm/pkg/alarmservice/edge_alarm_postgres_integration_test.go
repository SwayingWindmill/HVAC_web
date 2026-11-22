package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

type stubSyncChecker struct {
	calls  atomic.Int32
	status EdgeSiteRuleSyncStatus
	err    error
}

func (s *stubSyncChecker) GetSiteEdgeRuleSyncStatus(context.Context, string, string) (EdgeSiteRuleSyncStatus, error) {
	s.calls.Add(1)
	return s.status, s.err
}

func TestPostgresEdgeAlarmEventProjectionLifecycleCascadeAndDedup(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	const (
		tenantID      = "0195c000-0000-7000-8000-000000000001"
		siteID        = "0195c000-0000-7000-8000-000000000002"
		devChiller    = "0195c000-0000-7000-8000-000000000003"
		devPump       = "0195c000-0000-7000-8000-000000000004"
		edgeAssignID  = "0195c000-0000-7000-8000-000000000011"
		cloudAssignID = "0195c000-0000-7000-8000-000000000012"
		bothAssignID  = "0195c000-0000-7000-8000-000000000013"
		gatewayID     = "0195c000-0000-7000-8000-000000000099"
	)
	ctx := identitycontext.WithTenantID(context.Background(), tenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	// Edge-only upstream rule with both WEBHOOK and PROTECTION_COMMAND actions.
	thr50 := 50.0
	thr45 := 45.0
	edgePolicy := AlarmPolicyRevision{
		SchemaVersion:    AlarmPolicySchemaVersion,
		PolicyID:         "0195c000-0000-7000-8000-000000000021",
		AlarmType:        "CHILLER_TEMP_HIGH",
		SourceType:       alarmmodel.SourceSiteRule,
		SourceReference:  "site-rule:edge-temp-high",
		Title:            "Chiller High Supply Temp",
		Summary:          "Chiller temp exceeded 50C",
		Severity:         alarmmodel.SeverityCritical,
		ExecutionTarget:  ExecutionTargetEdge,
		QualityPolicy:    EvaluationQualityValidOnly,
		FreshnessSeconds: 300,
		TriggerMode:      TriggerSimple,
		Raise: Condition{
			Kind: ConditionCompare, Input: devChiller + "/supply_temp", Operator: CompareGTE, Value: NumberValue(thr50),
		},
		Clear: Condition{
			Kind: ConditionCompare, Input: devChiller + "/supply_temp", Operator: CompareLTE, Value: NumberValue(thr45),
		},
		Actions: []RuleAction{
			{
				Kind:       RuleActionWebhook,
				TriggerOn:  ActionTriggerBoth,
				WebhookURL: "https://hooks.example.test/alarm",
			},
			{
				Kind:            RuleActionProtectionCommand,
				TriggerOn:       ActionTriggerRaise,
				TargetDeviceID:  devPump,
				TargetPointCode: "cmd_stop",
				Capability:      "STOP",
			},
		},
	}

	// Downstream CLOUD rule gated on upstream edgeAssignID.
	cloudPolicy := AlarmPolicyRevision{
		SchemaVersion:    AlarmPolicySchemaVersion,
		PolicyID:         "0195c000-0000-7000-8000-000000000022",
		AlarmType:        "PLANT_EFFICIENCY_DEGRADED",
		SourceType:       alarmmodel.SourceSiteRule,
		SourceReference:  "site-rule:downstream-cloud",
		Title:            "Downstream Plant Alert",
		Summary:          "Gated by edge temp high and high load",
		Severity:         alarmmodel.SeverityMajor,
		ExecutionTarget:  ExecutionTargetCloud,
		DependencyGate:   DependencyGateAllActive,
		DependsOnRuleIDs: []string{edgeAssignID},
		QualityPolicy:    EvaluationQualityValidOnly,
		FreshnessSeconds: 300,
		TriggerMode:      TriggerSimple,
		Raise: Condition{
			Kind: ConditionCompare, Input: devChiller + "/load_pct", Operator: CompareGTE, Value: NumberValue(80),
		},
		Clear: Condition{
			Kind: ConditionCompare, Input: devChiller + "/load_pct", Operator: CompareLTE, Value: NumberValue(60),
		},
	}

	// CLOUD_AND_EDGE rule (must NEVER be skipped by cloud dedup).
	bothPolicy := AlarmPolicyRevision{
		SchemaVersion:    AlarmPolicySchemaVersion,
		PolicyID:         "0195c000-0000-7000-8000-000000000023",
		AlarmType:        "CHILLER_PRESSURE_WARN",
		SourceType:       alarmmodel.SourceSiteRule,
		SourceReference:  "site-rule:both-target",
		Title:            "Both Target Rule",
		Summary:          "Evaluated on both edge and cloud",
		Severity:         alarmmodel.SeverityMinor,
		ExecutionTarget:  ExecutionTargetCloudAndEdge,
		QualityPolicy:    EvaluationQualityValidOnly,
		FreshnessSeconds: 300,
		TriggerMode:      TriggerSimple,
		Raise: Condition{
			Kind: ConditionCompare, Input: devChiller + "/supply_temp", Operator: CompareGTE, Value: NumberValue(thr50),
		},
		Clear: Condition{
			Kind: ConditionCompare, Input: devChiller + "/supply_temp", Operator: CompareLTE, Value: NumberValue(thr45),
		},
	}

	if _, err := store.SyncSiteRules(ctx, tenantID, siteID, []SiteRule{
		{AssignmentID: edgeAssignID, Policy: edgePolicy},
		{AssignmentID: cloudAssignID, Policy: cloudPolicy},
		{AssignmentID: bothAssignID, Policy: bothPolicy},
	}, now.Add(-10*time.Minute), "test:950"); err != nil {
		t.Fatalf("SyncSiteRules: %v", err)
	}

	// Seed site telemetry readings (high supply_temp=55 AND high load_pct=90).
	snap1 := telemetryBridgeSnapshot(devChiller, 1, now.Add(-2*time.Minute),
		presentTelemetryState("supply_temp", "NUMBER", `55`, "GOOD", "FRESH", now.Add(-2*time.Minute)),
		presentTelemetryState("load_pct", "NUMBER", `90`, "GOOD", "FRESH", now.Add(-2*time.Minute)),
	)
	snap1.TenantId = tenantID
	snap1.SiteId = siteID

	// Wire online & synced connectivity stub confirming edgeAssignID is active in bundle rev 1.
	checker := &stubSyncChecker{
		status: EdgeSiteRuleSyncStatus{
			SchemaVersion: 1,
			TenantID:      tenantID,
			SiteID:        siteID,
			Gateways: []EdgeGatewayRuleSyncSummary{{
				GatewayID:                gatewayID,
				Online:                   true,
				EdgeRulesSynced:          true,
				ReportedBundleRevision:   1,
				ReportedEdgeBundleDigest: strings.Repeat("c", 64),
				ReportedEdgeOnlyRuleIDs:  []string{edgeAssignID},
			}},
		},
	}
	store.SetEdgeRuleSyncChecker(checker)

	// 1) Pure EDGE dedup: EvaluateTelemetrySnapshot skips edgeAssignID, still evaluates cloudAssignID & bothAssignID.
	decs, err := store.EvaluateTelemetrySnapshot(ctx, "0195c000-0000-7000-8000-000000000101", snap1, now.Add(-90*time.Second))
	if err != nil {
		t.Fatalf("EvaluateTelemetrySnapshot(dedup): %v", err)
	}
	for _, d := range decs {
		if d.State.ActiveAlarmID != "" && d.Fingerprint == "" {
			t.Fatal("unexpected empty fingerprint")
		}
	}
	if checker.calls.Load() == 0 {
		t.Fatal("expected edgeSyncChecker to be consulted for pure EDGE rule")
	}
	// edgeAssignID must NOT have raised on cloud, AND downstream cloudAssignID should not have fired yet (upstream inactive).
	listed, err := store.List(ctx, tenantID, siteID, Filter{Condition: alarmmodel.ConditionActive, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].AlarmType != "CHILLER_PRESSURE_WARN" {
		t.Fatalf("expected only CLOUD_AND_EDGE alarm before edge event arrives, got %+v", listed.Items)
	}

	// 2) Edge offline RAISE projects into events + alarm_current + action_outbox (WEBHOOK only, skipping PROTECTION_COMMAND) + cascades cloudAssignID!
	raiseReq := EdgeAlarmEventRequest{
		SchemaVersion:  1,
		MessageID:      "0195c000-0000-7000-8000-000000000201",
		TenantID:       tenantID,
		SiteID:         siteID,
		GatewayID:      gatewayID,
		EventType:      "EDGE_RULE_ALARM_RAISED",
		SourceType:     "EDGE_RULE",
		SourceID:       edgeAssignID,
		EventTime:      now.Add(-60 * time.Second).Format(time.RFC3339Nano),
		Severity:       "CRITICAL",
		SourceSequence: 1,
		ReceivedAt:     now.Add(-30 * time.Second).Format(time.RFC3339Nano),
		Data:           json.RawMessage(`{"eventId":"edge-rule:` + edgeAssignID + `:2","eventType":"EDGE_RULE_ALARM_RAISED","ruleId":"` + edgeAssignID + `","alarmType":"CHILLER_TEMP_HIGH","title":"Chiller High Supply Temp","severity":"CRITICAL","occurredAt":"` + now.Add(-60*time.Second).Format(time.RFC3339Nano) + `","bundleRevision":1,"edgeBundleDigest":"` + strings.Repeat("c", 64) + `","offline":true,"reason":"offline temp > 50","triggeredActions":["emergency-stop"]}`),
	}
	raiseRes, err := store.IngestEdgeAlarmEvent(ctx, raiseReq, now.Add(-30*time.Second))
	if err != nil || raiseRes.Outcome != EdgeAlarmOutcomeRaised || raiseRes.AlarmID == "" || raiseRes.EventID == "" {
		t.Fatalf("IngestEdgeAlarmEvent(RAISE) = %+v, err=%v", raiseRes, err)
	}

	// Downstream cloudAssignID must now have cascaded ACTIVE because upstream edgeAssignID became ACTIVE!
	listedAfterRaise, err := store.List(ctx, tenantID, siteID, Filter{Condition: alarmmodel.ConditionActive, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAfterRaise.Items) != 3 {
		t.Fatalf("expected 3 active alarms after edge raise + DAG cascade, got %d (%+v)", len(listedAfterRaise.Items), listedAfterRaise.Items)
	}

	// Check rule_action_outbox: WEBHOOK enqueued, PROTECTION_COMMAND skipped!
	txCheck, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txCheck.Rollback(ctx) }()

	var actionKinds []string
	rows, err := txCheck.Query(ctx, `SELECT action_kind FROM alarm_runtime.rule_action_outbox WHERE tenant_id=$1 AND alarm_id=$2 ORDER BY action_index`, tenantID, raiseRes.AlarmID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		actionKinds = append(actionKinds, k)
	}
	rows.Close()
	if len(actionKinds) != 1 || actionKinds[0] != string(RuleActionWebhook) {
		t.Fatalf("expected only WEBHOOK in rule_action_outbox (PROTECTION_COMMAND skipped on edge), got %v", actionKinds)
	}

	// Check events row created ACTIVE with offline=true metadata.
	var evStatus string
	var evMetaJSON []byte
	if err := txCheck.QueryRow(ctx, `SELECT status, metadata FROM alarm_runtime.events WHERE tenant_id=$1 AND site_id=$2 AND event_id=$3`, tenantID, siteID, raiseRes.EventID).Scan(&evStatus, &evMetaJSON); err != nil {
		t.Fatal(err)
	}
	var evMeta map[string]any
	if err := json.Unmarshal(evMetaJSON, &evMeta); err != nil {
		t.Fatalf("decode event metadata: %v", err)
	}
	if evStatus != "ACTIVE" || evMeta["offline"] != true || evMeta["source"] != "EDGE_RULE" {
		t.Fatalf("unexpected events row status=%q meta=%s", evStatus, string(evMetaJSON))
	}
	_ = txCheck.Rollback(ctx)

	// 3) Idempotent duplicate replay returns DUPLICATE.
	dupRes, err := store.IngestEdgeAlarmEvent(ctx, raiseReq, now.Add(-25*time.Second))
	if err != nil || dupRes.Outcome != EdgeAlarmOutcomeDuplicate || dupRes.AlarmID != raiseRes.AlarmID {
		t.Fatalf("duplicate replay = %+v, err=%v", dupRes, err)
	}

	// 4) Out-of-order replay (older occurredAt OR same occurredAt + lower seq=1) returns IGNORED_OUT_OF_ORDER.
	oooReq := raiseReq
	oooReq.MessageID = "0195c000-0000-7000-8000-000000000202"
	oooReq.Data = json.RawMessage(`{"eventId":"edge-rule:` + edgeAssignID + `:1","eventType":"EDGE_RULE_ALARM_RAISED","ruleId":"` + edgeAssignID + `","occurredAt":"` + now.Add(-60*time.Second).Format(time.RFC3339Nano) + `","bundleRevision":1}`)
	oooRes, err := store.IngestEdgeAlarmEvent(ctx, oooReq, now.Add(-20*time.Second))
	if err != nil || oooRes.Outcome != EdgeAlarmOutcomeOutOfOrder {
		t.Fatalf("expected IGNORED_OUT_OF_ORDER, got %+v err=%v", oooRes, err)
	}

	// 5) Edge CLEAR closes alarm + event + cascades clear to downstream cloudAssignID.
	clearReq := EdgeAlarmEventRequest{
		SchemaVersion:  1,
		MessageID:      "0195c000-0000-7000-8000-000000000203",
		TenantID:       tenantID,
		SiteID:         siteID,
		GatewayID:      gatewayID,
		EventType:      "EDGE_RULE_ALARM_CLEARED",
		SourceType:     "EDGE_RULE",
		SourceID:       edgeAssignID,
		EventTime:      now.Add(-10 * time.Second).Format(time.RFC3339Nano),
		Severity:       "INFO",
		SourceSequence: 3,
		ReceivedAt:     now.Add(-5 * time.Second).Format(time.RFC3339Nano),
		Data:           json.RawMessage(`{"eventId":"edge-rule:` + edgeAssignID + `:3","eventType":"EDGE_RULE_ALARM_CLEARED","ruleId":"` + edgeAssignID + `","occurredAt":"` + now.Add(-10*time.Second).Format(time.RFC3339Nano) + `","bundleRevision":1,"reason":"temp dropped below 45C"}`),
	}
	clearRes, err := store.IngestEdgeAlarmEvent(ctx, clearReq, now.Add(-5*time.Second))
	if err != nil || clearRes.Outcome != EdgeAlarmOutcomeCleared || clearRes.AlarmID != raiseRes.AlarmID {
		t.Fatalf("IngestEdgeAlarmEvent(CLEAR) = %+v err=%v", clearRes, err)
	}
	clearedAlarm, err := store.Get(ctx, tenantID, siteID, raiseRes.AlarmID)
	if err != nil || clearedAlarm.Condition != alarmmodel.ConditionCleared {
		t.Fatalf("expected CLEARED alarm, got %+v err=%v", clearedAlarm, err)
	}
	listedActiveAfterClear, err := store.List(ctx, tenantID, siteID, Filter{Condition: alarmmodel.ConditionActive, Limit: 20})
	if err != nil || len(listedActiveAfterClear.Items) != 2 {
		t.Fatalf("expected 2 remaining active alarms after edge clear (bothAssignID + cloudAssignID), got %+v err=%v", listedActiveAfterClear, err)
	}

	// 6) Fail-safe fallback when gateway goes offline/unsynced or connectivity errors: cloud re-evaluates pure EDGE rule!
	checker.err = errors.New("connectivity unreachable")
	snap2 := telemetryBridgeSnapshot(devChiller, 2, now,
		presentTelemetryState("supply_temp", "NUMBER", `56`, "GOOD", "FRESH", now),
		presentTelemetryState("load_pct", "NUMBER", `91`, "GOOD", "FRESH", now),
	)
	snap2.TenantId = tenantID
	snap2.SiteId = siteID
	if _, err := store.EvaluateTelemetrySnapshot(ctx, "0195c000-0000-7000-8000-000000000102", snap2, now.Add(time.Second)); err != nil {
		t.Fatalf("EvaluateTelemetrySnapshot(fallback): %v", err)
	}
	listedFallback, err := store.List(ctx, tenantID, siteID, Filter{Condition: alarmmodel.ConditionActive, Limit: 20})
	if err != nil || len(listedFallback.Items) != 3 {
		t.Fatalf("expected cloud fallback to raise pure EDGE alarm + cascade when connectivity unavailable, got %d (%+v) err=%v", len(listedFallback.Items), listedFallback.Items, err)
	}
}
