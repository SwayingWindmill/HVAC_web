package alarmservice

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestCompileSiteRulesForEdgeEligibilityAndDAGPruning(t *testing.T) {
	now := time.Date(2026, 11, 22, 2, 0, 0, 0, time.UTC)
	thresh := 12.0
	delta := 2.0
	targetActuatorID := "018f3e00-3000-7000-8000-000000000099"
	cloudRootID := "01900000-7000-7000-8000-000000000001"
	edgeRootID := "01900000-7000-7000-8000-000000000002"
	edgeChildID := "01900000-7000-7000-8000-000000000003"
	blockedChildID := "01900000-7000-7000-8000-000000000004"

	rules := []SiteRule{
		{
			AssignmentID: cloudRootID,
			Policy: AlarmPolicyRevision{
				AlarmType:       "cloud-only-root",
				Title:           "云端缺失读数根规则",
				Severity:        alarmmodel.SeverityWarning,
				ExecutionTarget: ExecutionTargetCloud,
				TriggerMode:     TriggerSimple,
				Raise:           Condition{Kind: ConditionNoData, Input: testDeviceID + "/t1", AgeSeconds: 300},
				Clear:           Condition{Kind: ConditionCompare, Input: testDeviceID + "/t1", Operator: CompareGTE, Value: NumberValue(-100)},
			},
		},
		{
			AssignmentID: edgeRootID,
			Policy: AlarmPolicyRevision{
				AlarmType:        "edge-window-root",
				Title:            "边缘 P95 滑动窗口规则",
				Severity:         alarmmodel.SeverityMajor,
				RuleKind:         RuleKindWindow,
				ExecutionTarget:  ExecutionTargetCloudAndEdge,
				TriggerMode:      TriggerSimple,
				QualityPolicy:    EvaluationQualityValidOnly,
				FreshnessSeconds: 900,
				Raise: Condition{
					Kind:          ConditionWindowAgg,
					Input:         testDeviceID + "/supply_water_temperature",
					Agg:           WindowAggP95,
					WindowType:    WindowTypeSliding,
					WindowSeconds: 300,
					MinSamples:    3,
					Operator:      CompareGTE,
					Threshold:     &thresh,
				},
				Clear: Condition{
					Kind:     ConditionCompare,
					Input:    testDeviceID + "/supply_water_temperature",
					Operator: CompareLT,
					Value:    NumberValue(10),
				},
				Actions: []RuleAction{
					{Kind: RuleActionWorkOrder, TriggerOn: ActionTriggerRaise, WorkOrderPriority: "HIGH", WorkOrderTitle: "检查冷机"},
					{Kind: RuleActionProtectionCommand, TriggerOn: ActionTriggerRaise, TargetDeviceID: targetActuatorID, Capability: "STOP"},
				},
			},
		},
		{
			AssignmentID: edgeChildID,
			Policy: AlarmPolicyRevision{
				AlarmType:        "edge-child",
				Title:            "边缘二级斜率联动",
				Severity:         alarmmodel.SeverityCritical,
				RuleKind:         RuleKindSlope,
				ExecutionTarget:  ExecutionTargetEdge,
				DependsOnRuleIDs: []string{edgeRootID},
				DependencyGate:   DependencyGateAllActive,
				TriggerMode:      TriggerSimple,
				QualityPolicy:    EvaluationQualityValidOnly,
				FreshnessSeconds: 900,
				Raise:            Condition{Kind: ConditionSlope, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGT, Delta: &delta, WindowSeconds: 120},
				Clear:            Condition{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareLTE, Value: NumberValue(9)},
				Actions: []RuleAction{
					{Kind: RuleActionProtectionCommand, TriggerOn: ActionTriggerRaise, TargetDeviceID: targetActuatorID, Capability: "SET_FREQUENCY", Parameters: map[string]float64{"frequencyHz": 45}},
				},
			},
		},
		{
			AssignmentID: blockedChildID,
			Policy: AlarmPolicyRevision{
				AlarmType:        "blocked-edge-child",
				Title:            "依赖云端规则的无效边缘规则",
				Severity:         alarmmodel.SeverityMinor,
				ExecutionTarget:  ExecutionTargetEdge,
				DependsOnRuleIDs: []string{cloudRootID},
				DependencyGate:   DependencyGateAllActive,
				TriggerMode:      TriggerSimple,
				Raise:            Condition{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGT, Value: NumberValue(15)},
				Clear:            Condition{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareLTE, Value: NumberValue(12)},
			},
		},
	}

	res, err := CompileSiteRulesForEdge(testSiteID, 9, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", rules, now)
	if err != nil {
		t.Fatalf("CompileSiteRulesForEdge() error = %v", err)
	}
	if res.EdgeRuleCount != 2 || res.CloudOnlyCount != 2 {
		t.Fatalf("unexpected counts edge=%d cloud=%d", res.EdgeRuleCount, res.CloudOnlyCount)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("res.Validate() error = %v", err)
	}
	if res.Bundle.Rules[0].RuleID != edgeRootID || len(res.Bundle.Rules[0].Actions) != 1 || res.Bundle.Rules[0].Actions[0].Kind != "PROTECTION_COMMAND" {
		t.Fatalf("hybrid CLOUD_AND_EDGE rule should keep only PROTECTION_COMMAND on edge: %+v", res.Bundle.Rules[0])
	}
	elByID := make(map[string]EdgeRuleEligibility, len(res.Eligibility))
	for _, el := range res.Eligibility {
		elByID[el.RuleID] = el
	}
	if elByID[blockedChildID].Compiled || elByID[blockedChildID].ReasonCode != EdgeEligibilityUpstreamNotOnEdge {
		t.Fatalf("expected EDGE_UPSTREAM_NOT_ON_EDGE for %s, got %+v", blockedChildID, elByID[blockedChildID])
	}

	// Authoring validation catches ineligible EDGE rules at draft compile time.
	badDrafts := []SiteRuleDraft{{
		RuleID:          "01900000-7000-7000-8000-000000000009",
		Title:           "纯边缘却带工单动作",
		Severity:        alarmmodel.SeverityMajor,
		ExecutionTarget: ExecutionTargetEdge,
		TriggerMode:     TriggerSimple,
		Raise:           RuleCondition{Kind: ConditionCompare, Reading: &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}, Operator: CompareGT, Value: 12.0},
		Clear:           RuleCondition{Kind: ConditionCompare, Reading: &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}, Operator: CompareLTE, Value: 10.0},
		Actions:         []RuleAction{{Kind: RuleActionWorkOrder, WorkOrderPriority: "HIGH", WorkOrderTitle: "云端工单"}},
	}}
	_, _, issues := compileDraftsAgainstHistory(badDrafts, map[string]SiteRule{}, nil)
	if len(issues) == 0 || issues[0].Code != "EDGE_RULE_INELIGIBLE" {
		t.Fatalf("expected EDGE_RULE_INELIGIBLE draft issue, got %+v", issues)
	}
}

func TestAlarmHTTPCompileSiteRulesEdgeEndpoint(t *testing.T) {
	now := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewRegistry()
	edgeRule := SiteRule{
		AssignmentID: "01900000-8000-7000-8000-000000000001",
		Policy: AlarmPolicyRevision{
			AlarmType:        "edge-threshold",
			Title:            "边缘高温报警",
			Severity:         alarmmodel.SeverityMajor,
			ExecutionTarget:  ExecutionTargetEdge,
			QualityPolicy:    EvaluationQualityValidOnly,
			FreshnessSeconds: 900,
			TriggerMode:      TriggerSimple,
			Raise:            Condition{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGT, Value: NumberValue(12)},
			Clear:            Condition{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareLTE, Value: NumberValue(10)},
		},
	}
	stub := &stubAuthoringStore{
		rules: []SiteRule{edgeRule},
		meta:  SiteRuleBundleMeta{BundleRevision: 3, BundleDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID, rules: []SiteRule{edgeRule}},
		Authoring: stub, Metrics: metrics, GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:compile-edge", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out EdgeRuleCompileResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.EdgeRuleCount != 1 || out.BundleRevision != 3 || err != nil {
		t.Fatalf("unexpected compile response: %+v", out)
	}
	if stub.lastCompileDraft {
		t.Fatal("expected nil drafts when empty body sent")
	}
}
