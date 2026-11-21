package alarmservice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestParseRuleConditionRoundTripsAllKinds(t *testing.T) {
	delta := 3.5
	minVal, maxVal := 4.0, 12.5
	trigVal, resetVal := 11.5, 9.5
	reading := &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}
	samples := []Condition{
		{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGTE, Value: NumberValue(12.5)},
		{Kind: ConditionCompare, Input: testDeviceID + "/run_state", Operator: CompareEQ, Value: StringValue("RUNNING")},
		{Kind: ConditionCompare, Input: testDeviceID + "/online", Operator: CompareNE, Value: BooleanValue(false)},
		{Kind: ConditionRange, Input: testDeviceID + "/supply_water_temperature", Minimum: &minVal, Maximum: &maxVal},
		{Kind: ConditionHysteresis, Input: testDeviceID + "/supply_water_temperature", Direction: DirectionAbove, Trigger: trigVal, Reset: resetVal},
		{Kind: ConditionHysteresis, Input: testDeviceID + "/supply_water_temperature", Direction: DirectionBelow, Trigger: 5.0, Reset: 6.5},
		{Kind: ConditionNoData, Input: testDeviceID + "/supply_water_temperature", AgeSeconds: 180},
		{Kind: ConditionStale, Input: testDeviceID + "/supply_water_temperature", AgeSeconds: 240},
		{Kind: ConditionUnchanged, Input: testDeviceID + "/supply_water_temperature", AgeSeconds: 3600},
		{Kind: ConditionChanged, Input: testDeviceID + "/supply_water_temperature", AgeSeconds: 60},
		{Kind: ConditionReasons, Input: testDeviceID + "/supply_water_temperature", Match: ReasonMatchAny, Reasons: []string{"OUT_OF_RANGE", "FLATLINE"}},
		{Kind: ConditionSlope, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGTE, Delta: &delta, WindowSeconds: 300},
		{Kind: ConditionExpression, Expression: "a - b > 4.0", ExprInputs: map[string]string{"a": testDeviceID + "/return_water_temperature", "b": testDeviceID + "/supply_water_temperature"}},
		{Kind: ConditionAnd, Children: []Condition{
			{Kind: ConditionCompare, Input: testDeviceID + "/run_state", Operator: CompareEQ, Value: StringValue("RUNNING")},
			{Kind: ConditionOr, Children: []Condition{
				{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGT, Value: NumberValue(12)},
				{Kind: ConditionStale, Input: testDeviceID + "/supply_water_temperature", AgeSeconds: 300},
			}},
		}},
	}
	for i, original := range samples {
		view, err := ruleCondition(original)
		if err != nil {
			t.Fatalf("case %d ruleCondition failed: %v", i, err)
		}
		raw, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("case %d marshal failed: %v", i, err)
		}
		var decoded RuleCondition
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("case %d unmarshal failed: %v", i, err)
		}
		parsed, err := parseRuleCondition(decoded, 0)
		if err != nil {
			t.Fatalf("case %d parseRuleCondition failed: %v", i, err)
		}
		if !reflect.DeepEqual(parsed, original) {
			t.Fatalf("case %d round-trip mismatch:\n got %#v\nwant %#v", i, parsed, original)
		}
	}

	// Reject excessive nesting depth and invalid hysteresis ordering.
	deep := RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareGT, Value: 10.0}
	for d := 0; d < maxConditionDepth+2; d++ {
		deep = RuleCondition{Kind: ConditionAnd, Children: []RuleCondition{
			deep,
			{Kind: ConditionCompare, Reading: reading, Operator: CompareLT, Value: 20.0},
		}}
	}
	if _, err := parseRuleCondition(deep, 0); err == nil {
		t.Fatal("expected nesting depth overflow error")
	}
	badHyst := RuleCondition{Kind: ConditionHysteresis, Reading: reading, Direction: DirectionAbove, Trigger: &trigVal, Reset: &maxVal}
	if _, err := parseRuleCondition(badHyst, 0); err == nil {
		t.Fatal("expected inverted ABOVE hysteresis reset >= trigger error")
	}
}

func TestSiteRuleCacheConcurrentRaceSafetyAndStampOrdering(t *testing.T) {
	var cache siteRuleCache
	baseRule := supplyTooWarmRule()
	cache.Put(testTenantID, testSiteID, 2, 5, []SiteRule{baseRule})

	// Stale puts with lower revision or lower assignment count must be ignored.
	cache.Put(testTenantID, testSiteID, 1, 9, []SiteRule{})
	if got, ok := cache.Get(testTenantID, testSiteID, 2, 5); !ok || len(got) != 1 {
		t.Fatalf("stale put overwrote newer cache snapshot: ok=%v got=%#v", ok, got)
	} else {
		// Mutating returned slice must not corrupt cache entry.
		got[0].Policy.Title = "mutated"
		recheck, ok2 := cache.Get(testTenantID, testSiteID, 2, 5)
		if !ok2 || recheck[0].Policy.Title != baseRule.Policy.Title {
			t.Fatalf("returned slice was not isolated defensively: %#v", recheck)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rev := uint64(3 + (i % 5))
				cache.Put(testTenantID, testSiteID, rev, uint64(i+worker), []SiteRule{baseRule})
				_, _ = cache.Get(testTenantID, testSiteID, rev, uint64(i+worker))
				if i%17 == 0 {
					cache.Invalidate(testTenantID, testSiteID)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestCompileDraftsPreservesPolicyIDAndRejectsCyclesAndOverDepth(t *testing.T) {
	ruleA := "018f3e00-7a02-7000-8000-000000000101"
	ruleB := "018f3e00-7a02-7000-8000-000000000102"
	ruleC := "018f3e00-7a02-7000-8000-000000000103"
	ruleD := "018f3e00-7a02-7000-8000-000000000104"
	ruleE := "018f3e00-7a02-7000-8000-000000000105"
	familyA := "018f3e00-5200-7000-8000-000000000101"
	reading := &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}

	history := map[string]SiteRule{
		ruleA: {
			AssignmentID: ruleA,
			Policy: AlarmPolicyRevision{
				PolicyID:         familyA,
				AlarmType:        "custom-family-a",
				SourceReference:  "alarm-rules:legacy-a",
				QualityPolicy:    EvaluationQualityUsable,
				FreshnessSeconds: 1800,
			},
		},
	}

	makeDraft := func(id, title string, deps []string) SiteRuleDraft {
		var gate DependencyGateMode
		if len(deps) > 0 {
			gate = DependencyGateAllActive
		}
		return SiteRuleDraft{
			RuleID:           id,
			Title:            title,
			Severity:         alarmmodel.SeverityMajor,
			DependsOnRuleIDs: deps,
			DependencyGate:   gate,
			TriggerMode:      TriggerSimple,
			Raise:            RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareGT, Value: 12.0},
			Clear:            RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareLT, Value: 10.0},
		}
	}

	compiled, report, issues := compileDraftsAgainstHistory(
		[]SiteRuleDraft{makeDraft(ruleA, "规则A", nil), makeDraft(ruleB, "规则B", []string{ruleA})},
		history,
		nil,
	)
	if len(issues) != 0 || report.MaxHopDepth != 1 || compiled[0].Policy.PolicyID != familyA || compiled[0].Policy.AlarmType != "custom-family-a" || compiled[0].Policy.FreshnessSeconds != 1800 {
		t.Fatalf("expected clean compilation preserving prior policy family, got issues=%#v compiled=%#v report=%#v", issues, compiled, report)
	}

	// Cycle detection (A -> B -> A).
	_, _, cycleIssues := compileDraftsAgainstHistory(
		[]SiteRuleDraft{makeDraft(ruleA, "规则A", []string{ruleB}), makeDraft(ruleB, "规则B", []string{ruleA})},
		history,
		nil,
	)
	if len(cycleIssues) == 0 || cycleIssues[0].Code != "RULE_CYCLE_DETECTED" {
		t.Fatalf("expected RULE_CYCLE_DETECTED, got %#v", cycleIssues)
	}

	// Depth > 3 rejection (A -> B -> C -> D -> E).
	_, _, depthIssues := compileDraftsAgainstHistory(
		[]SiteRuleDraft{
			makeDraft(ruleA, "A", nil),
			makeDraft(ruleB, "B", []string{ruleA}),
			makeDraft(ruleC, "C", []string{ruleB}),
			makeDraft(ruleD, "D", []string{ruleC}),
			makeDraft(ruleE, "E", []string{ruleD}),
		},
		history,
		nil,
	)
	if len(depthIssues) == 0 || depthIssues[0].Code != "MAX_LINKAGE_HOPS_EXCEEDED" {
		t.Fatalf("expected MAX_LINKAGE_HOPS_EXCEEDED, got %#v", depthIssues)
	}
}

type stubAuthoringStore struct {
	rules            []SiteRule
	meta             SiteRuleBundleMeta
	diffResult       SiteRuleDiffResult
	publishErr       error
	revisions        SiteRuleBundleRevisionList
	rollbackErr      error
	edgeCompileRes   EdgeRuleCompileResult
	edgeCompileErr   error
	lastDraftLen     int
	lastRollback     uint64
	lastCompileDraft bool
}

func (s *stubAuthoringStore) LatestSiteRuleBundleMeta(_ context.Context, _, _ string) (SiteRuleBundleMeta, error) {
	return s.meta, nil
}

func (s *stubAuthoringStore) DiffSiteRules(_ context.Context, _, _ string, drafts []SiteRuleDraft) (SiteRuleDiffResult, error) {
	s.lastDraftLen = len(drafts)
	return s.diffResult, nil
}

func (s *stubAuthoringStore) PublishSiteRules(_ context.Context, _, _ string, req SiteRulePublishRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error) {
	s.lastDraftLen = len(req.Rules)
	if s.publishErr != nil {
		return SiteRulePublishResult{}, s.publishErr
	}
	return SiteRulePublishResult{
		BundleRevision:  req.ExpectedRevision + 1,
		BundleDigest:    strings.Repeat("a", 64),
		Source:          RuleBundleSourceWebWorkbench,
		Changed:         true,
		ChangeNote:      req.ChangeNote,
		PublishedAt:     now.UTC().Format(time.RFC3339Nano),
		PublishedBy:     actorID,
		PublishedByName: actorName,
		Summary:         SiteRuleSyncSummary{Added: 0, Changed: 1, Unchanged: 0, Retired: 0, AddedRuleIDs: []string{}, ChangedRuleIDs: []string{}, UnchangedRuleIDs: []string{}, RetiredRuleIDs: []string{}},
		Graph:           SiteRuleGraphView{Nodes: []SiteRuleGraphNodeView{}, Edges: []SiteRuleGraphEdgeView{}, TopologicalOrder: []string{}},
		Items:           []SiteRuleView{},
	}, nil
}

func (s *stubAuthoringStore) ListSiteRuleBundleRevisions(_ context.Context, _, _ string, _ int) (SiteRuleBundleRevisionList, error) {
	return s.revisions, nil
}

func (s *stubAuthoringStore) RollbackSiteRules(_ context.Context, _, _ string, targetRevision uint64, req SiteRuleRollbackRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error) {
	s.lastRollback = targetRevision
	if s.rollbackErr != nil {
		return SiteRulePublishResult{}, s.rollbackErr
	}
	from := targetRevision
	return SiteRulePublishResult{
		BundleRevision:         req.ExpectedRevision + 1,
		BundleDigest:           strings.Repeat("b", 64),
		Source:                 RuleBundleSourceRollback,
		Changed:                true,
		RolledBackFromRevision: &from,
		ChangeNote:             req.ChangeNote,
		PublishedAt:            now.UTC().Format(time.RFC3339Nano),
		PublishedBy:            actorID,
		PublishedByName:        actorName,
		Summary:                SiteRuleSyncSummary{AddedRuleIDs: []string{}, ChangedRuleIDs: []string{}, UnchangedRuleIDs: []string{}, RetiredRuleIDs: []string{}},
		Graph:                  SiteRuleGraphView{Nodes: []SiteRuleGraphNodeView{}, Edges: []SiteRuleGraphEdgeView{}, TopologicalOrder: []string{}},
		Items:                  []SiteRuleView{},
	}, nil
}

func (s *stubAuthoringStore) CompileSiteRulesEdge(_ context.Context, _, siteID string, drafts *[]SiteRuleDraft, now time.Time) (EdgeRuleCompileResult, error) {
	s.lastCompileDraft = drafts != nil
	if s.edgeCompileErr != nil {
		return EdgeRuleCompileResult{}, s.edgeCompileErr
	}
	if s.edgeCompileRes.SiteID != "" {
		return s.edgeCompileRes, nil
	}
	return CompileSiteRulesForEdge(siteID, s.meta.BundleRevision, s.meta.BundleDigest, s.rules, now)
}

func TestAlarmHTTPAuthoringDiffPublishRollbackAndDraftSimulate(t *testing.T) {
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewRegistry()
	baseRule := supplyTooWarmRule()
	stub := &stubAuthoringStore{
		rules: []SiteRule{baseRule},
		meta:  SiteRuleBundleMeta{BundleRevision: 2, BundleDigest: strings.Repeat("c", 64)},
		diffResult: SiteRuleDiffResult{
			Valid:           true,
			CurrentRevision: 2,
			CurrentDigest:   strings.Repeat("c", 64),
			NextDigest:      strings.Repeat("d", 64),
			Summary:         SiteRuleSyncSummary{AddedRuleIDs: []string{}, Changed: 1, ChangedRuleIDs: []string{baseRule.AssignmentID}, UnchangedRuleIDs: []string{}, RetiredRuleIDs: []string{}},
			Changes:         []SiteRuleChangeEntry{{RuleID: baseRule.AssignmentID, Title: baseRule.Policy.Title, Change: "CHANGED", ChangedFields: []string{"title"}}},
			Graph:           SiteRuleGraphView{Nodes: []SiteRuleGraphNodeView{}, Edges: []SiteRuleGraphEdgeView{}, TopologicalOrder: []string{baseRule.AssignmentID}},
			Issues:          []SiteRuleValidationIssue{},
		},
		revisions: SiteRuleBundleRevisionList{
			CurrentRevision: 2,
			Items: []SiteRuleBundleRevisionView{{
				BundleRevision: 2, BundleDigest: strings.Repeat("c", 64), Source: RuleBundleSourceWebWorkbench,
				Summary:   SiteRuleSyncSummary{AddedRuleIDs: []string{}, ChangedRuleIDs: []string{}, UnchangedRuleIDs: []string{}, RetiredRuleIDs: []string{}},
				RuleCount: 1, PublishedAt: now.Format(time.RFC3339Nano), PublishedBy: "operator",
			}},
		},
	}

	handler, err := NewHTTPHandler(HTTPConfig{
		Store:            memory,
		Rules:            siteRules{siteID: testSiteID, rules: stub.rules},
		Authoring:        stub,
		Metrics:          metrics,
		GatewayPublicKey: &signer.PublicKey,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// GET /alarm-rules includes bundleRevision/bundleDigest when authoring store has a revision.
	listReq := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", nil)
	listReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET alarm-rules status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp SiteRuleList
	if json.Unmarshal(listRec.Body.Bytes(), &listResp) != nil || listResp.BundleRevision != 2 {
		t.Fatalf("expected bundleRevision 2 in response: %s", listRec.Body.String())
	}

	// POST /alarm-rules:simulate supports unsaved RuleDraft (lower threshold triggers immediately on 9.0°C).
	unsavedID := "018f3e00-7a02-7000-8000-000000000999"
	simReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:simulate", strings.NewReader(mustJSON(t, SimulateSiteRuleRequest{
		RuleID: unsavedID,
		RuleDraft: &SiteRuleDraft{
			RuleID:      unsavedID,
			Title:       "草稿阈值验证",
			Severity:    alarmmodel.SeverityWarning,
			TriggerMode: TriggerSimple,
			Raise:       RuleCondition{Kind: ConditionCompare, Reading: &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}, Operator: CompareGT, Value: 8.0},
			Clear:       RuleCondition{Kind: ConditionCompare, Reading: &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}, Operator: CompareLT, Value: 6.0},
		},
		Steps: []SimulateRuleStepInput{{
			Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "supply_water_temperature", Value: 9.0}},
		}},
	})))
	simReq.Header.Set("Content-Type", "application/json")
	simReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	simRec := httptest.NewRecorder()
	handler.ServeHTTP(simRec, simReq)
	if simRec.Code != http.StatusOK {
		t.Fatalf("draft simulate status=%d body=%s", simRec.Code, simRec.Body.String())
	}
	var simResp SiteRuleSimulationResult
	if json.Unmarshal(simRec.Body.Bytes(), &simResp) != nil || !simResp.FinalActive || simResp.RuleID != unsavedID {
		t.Fatalf("expected active draft simulation result, got %s", simRec.Body.String())
	}

	// POST /alarm-rules:diff.
	diffReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:diff", strings.NewReader(`{"rules":[]}`))
	diffReq.Header.Set("Content-Type", "application/json")
	diffReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	diffRec := httptest.NewRecorder()
	handler.ServeHTTP(diffRec, diffReq)
	if diffRec.Code != http.StatusOK || stub.lastDraftLen != 0 {
		t.Fatalf("diff status=%d body=%s", diffRec.Code, diffRec.Body.String())
	}

	// PUT /alarm-rules rejects AlarmReadAction with 403.
	unauthPut := httptest.NewRequest(http.MethodPut, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", strings.NewReader(`{"expectedRevision":2,"rules":[]}`))
	unauthPut.Header.Set("Content-Type", "application/json")
	unauthPut.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthPut)
	if unauthRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on PUT without publish grant, got %d", unauthRec.Code)
	}

	// PUT /alarm-rules conflict -> 409; graph invalid -> 422; success -> 200.
	stub.publishErr = &RuleBundleConflictError{ExpectedRevision: 1, CurrentRevision: 2}
	putConflict := httptest.NewRequest(http.MethodPut, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", strings.NewReader(`{"expectedRevision":1,"rules":[]}`))
	putConflict.Header.Set("Content-Type", "application/json")
	putConflict.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRulePublishAction, testTenantID, testSiteID, ""))
	conflictRec := httptest.NewRecorder()
	handler.ServeHTTP(conflictRec, putConflict)
	if conflictRec.Code != http.StatusConflict || !strings.Contains(conflictRec.Body.String(), "ALARM_RULE_BUNDLE_CONFLICT") {
		t.Fatalf("expected 409 ALARM_RULE_BUNDLE_CONFLICT, got %d %s", conflictRec.Code, conflictRec.Body.String())
	}

	stub.publishErr = &RuleDraftValidationError{Code: "ALARM_RULE_GRAPH_INVALID", Message: "cycle"}
	putInvalid := httptest.NewRequest(http.MethodPut, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", strings.NewReader(`{"expectedRevision":2,"rules":[]}`))
	putInvalid.Header.Set("Content-Type", "application/json")
	putInvalid.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRulePublishAction, testTenantID, testSiteID, ""))
	invalidRec := httptest.NewRecorder()
	handler.ServeHTTP(invalidRec, putInvalid)
	if invalidRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d %s", invalidRec.Code, invalidRec.Body.String())
	}

	stub.publishErr = nil
	putOK := httptest.NewRequest(http.MethodPut, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", strings.NewReader(`{"expectedRevision":2,"changeNote":"raise limit","rules":[]}`))
	putOK.Header.Set("Content-Type", "application/json")
	putOK.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRulePublishAction, testTenantID, testSiteID, ""))
	okRec := httptest.NewRecorder()
	handler.ServeHTTP(okRec, putOK)
	if okRec.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", okRec.Code, okRec.Body.String())
	}

	// GET /alarm-rules/revisions & POST /alarm-rules/revisions/1:rollback.
	revsReq := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/revisions?limit=10", nil)
	revsReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	revsRec := httptest.NewRecorder()
	handler.ServeHTTP(revsRec, revsReq)
	if revsRec.Code != http.StatusOK {
		t.Fatalf("revisions status=%d body=%s", revsRec.Code, revsRec.Body.String())
	}

	rbReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/revisions/1:rollback", strings.NewReader(`{"expectedRevision":2,"changeNote":"restore"}`))
	rbReq.Header.Set("Content-Type", "application/json")
	rbReq.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRulePublishAction, testTenantID, testSiteID, ""))
	rbRec := httptest.NewRecorder()
	handler.ServeHTTP(rbRec, rbReq)
	if rbRec.Code != http.StatusOK || stub.lastRollback != 1 {
		t.Fatalf("rollback status=%d rev=%d body=%s", rbRec.Code, stub.lastRollback, rbRec.Body.String())
	}

	// Verify bounded publish outcome metrics.
	scraped := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scraped, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := scraped.Body.String()
	for _, want := range []string{
		`hvac_alarm_rule_publishes_total{outcome="success"} 2`,
		`hvac_alarm_rule_publishes_total{outcome="rejected"} 2`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("missing metric line %q in:\n%s", want, metricsBody)
		}
	}
}

func TestAuthoringDraftValidationGuardsAndOverflowBounds(t *testing.T) {
	ruleID := "01910000-e530-7000-8000-000000000001"
	reading := &RuleReading{DeviceID: testDeviceID, PointCode: "supply_water_temperature"}
	history := map[string]SiteRule{
		ruleID: {
			AssignmentID: ruleID,
			Policy: AlarmPolicyRevision{
				PolicyID:         "01910000-e531-7000-8000-000000000001",
				AlarmType:        "fixed-alarm-type",
				SourceReference:  "site-rules:test/fixed",
				Title:            "基准规则",
				QualityPolicy:    EvaluationQualityValidOnly,
				FreshnessSeconds: 900,
			},
		},
	}

	// 1. Immutable alarmType on existing/retired rule + negative freshnessSeconds + duration overflow + age overflow.
	invalidDraft := SiteRuleDraft{
		RuleID:           ruleID,
		Title:            "非法超限规则",
		AlarmType:        "mutated-alarm-type",
		Severity:         alarmmodel.SeverityMajor,
		FreshnessSeconds: -5,
		TriggerMode:      TriggerDuration,
		DurationSeconds:  maxRuleDurationSeconds + 1,
		Raise:            RuleCondition{Kind: ConditionNoData, Reading: reading, AgeSeconds: maxRuleDurationSeconds + 1},
		Clear:            RuleCondition{Kind: ConditionCompare, Reading: reading, Operator: CompareLTE, Value: 10.0},
	}
	_, _, issues := compileDraftsAgainstHistory([]SiteRuleDraft{invalidDraft}, history, nil)
	gotCodes := make(map[string]bool)
	for _, issue := range issues {
		gotCodes[issue.Code] = true
	}
	for _, wantCode := range []string{"INVALID_RAISE_CONDITION", "INVALID_TRIGGER_MODE", "ALARM_TYPE_IMMUTABLE", "INVALID_FRESHNESS_SECONDS"} {
		if !gotCodes[wantCode] {
			t.Fatalf("expected validation issue %s, got issues=%#v", wantCode, issues)
		}
	}

	// 2. Rollback revision > math.MaxInt64 cleanly returns 404 RESOURCE_NOT_FOUND over HTTP without 503.
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubAuthoringStore{}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store:            memory,
		Rules:            siteRules{siteID: testSiteID, rules: stub.rules},
		Authoring:        stub,
		GatewayPublicKey: &signer.PublicKey,
		Now:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	rbReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/revisions/18446744073709551615:rollback", strings.NewReader(`{"expectedRevision":1}`))
	rbReq.Header.Set("Content-Type", "application/json")
	rbReq.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRulePublishAction, testTenantID, testSiteID, ""))
	rbRec := httptest.NewRecorder()
	handler.ServeHTTP(rbRec, rbReq)
	if rbRec.Code != http.StatusNotFound || !strings.Contains(rbRec.Body.String(), "RESOURCE_NOT_FOUND") {
		t.Fatalf("expected 404 RESOURCE_NOT_FOUND for uint64 > MaxInt64 rollback, got %d %s", rbRec.Code, rbRec.Body.String())
	}
}
