package alarmservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const testDeviceID = "018f3e00-3000-7000-8000-000000000001"

// siteRules is a SiteRuleReader holding one Site's rules.
type siteRules struct {
	siteID string
	rules  []SiteRule
	calls  *int
}

func (reader siteRules) SiteRules(_ context.Context, _, siteID string) ([]SiteRule, error) {
	if reader.calls != nil {
		*reader.calls++
	}
	if siteID != reader.siteID {
		return []SiteRule{}, nil
	}
	return reader.rules, nil
}

func supplyTooWarmRule() SiteRule {
	return SiteRule{AssignmentID: "018f3e00-7a02-7000-8000-000000000001", Policy: AlarmPolicyRevision{
		Title: "冷冻水供水温度过高", Severity: alarmmodel.SeverityMajor,
		TriggerMode: TriggerDuration, DurationSeconds: 900,
		Raise: Condition{Kind: ConditionAnd, Children: []Condition{
			{Kind: ConditionCompare, Input: testDeviceID + "/run_state", Operator: CompareEQ, Value: StringValue("RUNNING")},
			{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareGT, Value: NumberValue(10)},
		}},
		Clear: Condition{Kind: ConditionCompare, Input: testDeviceID + "/supply_water_temperature", Operator: CompareLT, Value: NumberValue(0)},
	}}
}

func TestAlarmHTTPListsTheSiteRulesInEffect(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID, rules: []SiteRule{supplyTooWarmRule()}},
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", nil)
	request.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"items": []any{map[string]any{
		"ruleId": "018f3e00-7a02-7000-8000-000000000001", "title": "冷冻水供水温度过高", "severity": "MAJOR",
		"triggerMode": "DURATION", "durationSeconds": float64(900),
		"raise": map[string]any{"kind": "AND", "children": []any{
			map[string]any{"kind": "COMPARE", "reading": map[string]any{"deviceId": testDeviceID, "pointCode": "run_state"}, "operator": "EQ", "value": "RUNNING"},
			map[string]any{"kind": "COMPARE", "reading": map[string]any{"deviceId": testDeviceID, "pointCode": "supply_water_temperature"}, "operator": "GT", "value": float64(10)},
		}},
		// A threshold of zero is still said.
		"clear": map[string]any{"kind": "COMPARE", "reading": map[string]any{"deviceId": testDeviceID, "pointCode": "supply_water_temperature"}, "operator": "LT", "value": float64(0)},
	}}}
	if got, _ := json.Marshal(body); string(got) != mustJSON(t, want) {
		t.Fatalf("rules\n got %s\nwant %s", got, mustJSON(t, want))
	}
}

func TestAlarmHTTPSiteRulesNeedAnAlarmReadGrantForThatSite(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID, rules: []SiteRule{supplyTooWarmRule()}, calls: &calls},
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, grant := range map[string]string{
		"no grant":         "",
		"another Site":     signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testOtherSiteID, ""),
		"another action":   signedReadContext(t, signer, now, AlarmAcknowledgeAction, testTenantID, testSiteID, ""),
		"one Alarm's read": signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, testAlarmID),
	} {
		request := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", nil)
		if grant != "" {
			request.Header.Set(AlarmReadContextHeader, grant)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s: status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("rules were read %d times without a valid grant", calls)
	}
}

func TestSiteRuleListValidatesReasonsConditions(t *testing.T) {
	rule, err := siteRuleViewWithHopDepth(SiteRule{
		AssignmentID: "018f3e00-7a02-7000-8000-000000000002",
		Policy: AlarmPolicyRevision{
			Title:       "数据不可信",
			Severity:    alarmmodel.SeverityWarning,
			TriggerMode: TriggerSimple,
			Raise:       Condition{Kind: ConditionReasons, Input: testDeviceID + "/t18", Match: ReasonMatchAny, Reasons: []string{"OUT_OF_RANGE", "SOURCE_UNTRUSTED"}},
			Clear:       Condition{Kind: ConditionReasons, Input: testDeviceID + "/t18", Match: ReasonMatchNone, Reasons: []string{"OUT_OF_RANGE", "SOURCE_UNTRUSTED"}},
		},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	valid := SiteRuleList{Items: []SiteRuleView{rule}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid REASONS rule rejected: %v", err)
	}

	reading := &RuleReading{DeviceID: testDeviceID, PointCode: "t18"}
	for name, cond := range map[string]RuleCondition{
		"bad match":    {Kind: ConditionReasons, Reading: reading, Match: "ALL", Reasons: []string{"OUT_OF_RANGE"}},
		"empty":        {Kind: ConditionReasons, Reading: reading, Match: ReasonMatchAny},
		"too many":     {Kind: ConditionReasons, Reading: reading, Match: ReasonMatchAny, Reasons: []string{"OUT_OF_RANGE", "SPIKE", "SENTINEL", "FLATLINE", "DERIVED_INPUT", "LOW_CONFIDENCE", "SOURCE_UNTRUSTED", "PROBE_OFFSET", "PHYSICS_POWER"}},
		"unknown code": {Kind: ConditionReasons, Reading: reading, Match: ReasonMatchAny, Reasons: []string{"BOGUS_REASON"}},
	} {
		invalid := valid
		invalid.Items = []SiteRuleView{rule}
		invalid.Items[0].Raise = cond
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: invalid REASONS condition accepted", name)
		}
	}
}

func TestSiteRuleListProjectsSlopeExpressionAndHopDepth(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	baseRule := supplyTooWarmRule()
	delta := 2.5
	slopeExprRule := SiteRule{
		AssignmentID: "018f3e00-7a02-7000-8000-000000000003",
		Policy: AlarmPolicyRevision{
			AlarmType:        "CHW_DIFF_SLOPE",
			Title:            "供回水温升过快",
			Severity:         alarmmodel.SeverityMajor,
			RuleKind:         RuleKindSlope,
			TemplateKey:      "chiller-slope-rise",
			OverriddenFields: []string{"raise.delta"},
			DependsOnRuleIDs: []string{baseRule.AssignmentID},
			QualityPolicy:    EvaluationQualityValidOnly,
			FreshnessSeconds: 900,
			TriggerMode:      TriggerSimple,
			Raise: Condition{
				Kind:          ConditionSlope,
				Input:         testDeviceID + "/supply_water_temperature",
				Operator:      CompareGTE,
				Delta:         &delta,
				WindowSeconds: 300,
			},
			Clear: Condition{
				Kind:       ConditionExpression,
				Expression: "t_diff <= 1.0",
				ExprInputs: map[string]string{"t_diff": testDeviceID + "/supply_water_temperature"},
			},
		},
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID, rules: []SiteRule{baseRule, slopeExprRule}},
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", nil)
	req.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list SiteRuleList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if err := list.Validate(); err != nil {
		t.Fatalf("list.Validate failed: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(list.Items))
	}
	got := list.Items[1]
	if got.RuleKind != RuleKindSlope || got.TemplateKey != "chiller-slope-rise" || got.HopDepth != 1 || !reflect.DeepEqual(got.DependsOnRuleIDs, []string{baseRule.AssignmentID}) {
		t.Fatalf("unexpected enriched view: %#v", got)
	}
	if got.Raise.Kind != ConditionSlope || got.Raise.Delta == nil || *got.Raise.Delta != 2.5 || got.Raise.WindowSeconds != 300 {
		t.Fatalf("unexpected slope raise: %#v", got.Raise)
	}
	if got.Clear.Kind != ConditionExpression || got.Clear.Expression != "t_diff <= 1.0" || got.Clear.Inputs["t_diff"].PointCode != "supply_water_temperature" {
		t.Fatalf("unexpected expression clear: %#v", got.Clear)
	}
}

func TestAlarmHTTPSimulateSiteRuleEvaluatesStepsInMemory(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	delta := 2.0
	slopeRuleID := "018f3e00-7a02-7000-8000-000000000004"
	baseRule := supplyTooWarmRule()
	slopeRule := SiteRule{
		AssignmentID: slopeRuleID,
		Policy: AlarmPolicyRevision{
			AlarmType:        "CHW_SUPPLY_RISE",
			Title:            "供水温度爬升",
			Severity:         alarmmodel.SeverityWarning,
			RuleKind:         RuleKindSlope,
			DependsOnRuleIDs: []string{baseRule.AssignmentID},
			QualityPolicy:    EvaluationQualityValidOnly,
			FreshnessSeconds: 900,
			TriggerMode:      TriggerSimple,
			Raise: Condition{
				Kind:          ConditionSlope,
				Input:         testDeviceID + "/supply_water_temperature",
				Operator:      CompareGTE,
				Delta:         &delta,
				WindowSeconds: 300,
			},
			Clear: Condition{
				Kind:     ConditionCompare,
				Input:    testDeviceID + "/supply_water_temperature",
				Operator: CompareLTE,
				Value:    NumberValue(7.0),
			},
		},
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID, rules: []SiteRule{baseRule, slopeRule}},
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := mustJSON(t, SimulateSiteRuleRequest{
		RuleID: slopeRuleID,
		Steps: []SimulateRuleStepInput{
			{Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "supply_water_temperature", Value: 7.5}}},
			{Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "supply_water_temperature", Value: 10.0}}},
			{Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "supply_water_temperature", Value: 6.8}}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:simulate", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res SiteRuleSimulationResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if err := res.Validate(slopeRuleID); err != nil {
		t.Fatalf("result validation failed: %v", err)
	}
	if res.HopDepth != 1 || res.FinalActive || len(res.Steps) != 3 {
		t.Fatalf("unexpected simulation summary: %#v", res)
	}
	if res.Steps[0].Effect != EvaluationEffectNone || res.Steps[0].Active ||
		res.Steps[1].Effect != EvaluationEffectPublish || !res.Steps[1].Active ||
		res.Steps[2].Effect != EvaluationEffectClear || res.Steps[2].Active {
		t.Fatalf("unexpected simulation step transitions: %#v", res.Steps)
	}

	missingPayload := mustJSON(t, SimulateSiteRuleRequest{
		RuleID: "018f3e00-7a02-7000-8000-0000000000ff",
		Steps:  []SimulateRuleStepInput{{Readings: []SimulateRuleReadingSample{}}},
	})
	req404 := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:simulate", strings.NewReader(missingPayload))
	req404.Header.Set("Content-Type", "application/json")
	req404.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	rec404 := httptest.NewRecorder()
	handler.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown rule, got %d body=%s", rec404.Code, rec404.Body.String())
	}

	// Supports canonical 128-char pointCode, canonical USABLE quality ("PARTIAL"), and rejects null readings / untrimmed pointCode / unsupported quality.
	longPoint := strings.Repeat("p", 128)
	usableRuleID := "018f3e00-7a02-7000-8000-000000000005"
	usableHandler, err := NewHTTPHandler(HTTPConfig{
		Store: memory,
		Rules: siteRules{siteID: testSiteID, rules: []SiteRule{{
			AssignmentID: usableRuleID,
			Policy: AlarmPolicyRevision{
				AlarmType: "LONG_POINT_RULE", Title: "长点位编码规则", Severity: alarmmodel.SeverityWarning,
				QualityPolicy: EvaluationQualityUsable, FreshnessSeconds: 900, TriggerMode: TriggerSimple,
				Raise: Condition{Kind: ConditionCompare, Input: testDeviceID + "/" + longPoint, Operator: CompareGTE, Value: NumberValue(10)},
				Clear: Condition{Kind: ConditionCompare, Input: testDeviceID + "/" + longPoint, Operator: CompareLT, Value: NumberValue(5)},
			},
		}}},
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	partialReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:simulate", strings.NewReader(mustJSON(t, SimulateSiteRuleRequest{
		RuleID: usableRuleID,
		Steps: []SimulateRuleStepInput{{
			Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: longPoint, Value: 12.0, Quality: "PARTIAL"}},
		}},
	})))
	partialReq.Header.Set("Content-Type", "application/json")
	partialReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
	partialRec := httptest.NewRecorder()
	usableHandler.ServeHTTP(partialRec, partialReq)
	if partialRec.Code != http.StatusOK {
		t.Fatalf("long point / PARTIAL quality expected 200, got %d body=%s", partialRec.Code, partialRec.Body.String())
	}
	for name, badBody := range map[string]string{
		"nil readings":        fmt.Sprintf(`{"ruleId":%q,"steps":[{}]}`, slopeRuleID),
		"untrimmed pointCode": mustJSON(t, SimulateSiteRuleRequest{RuleID: slopeRuleID, Steps: []SimulateRuleStepInput{{Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: " temp", Value: 1}}}}}),
		"legacy quality enum": mustJSON(t, SimulateSiteRuleRequest{RuleID: slopeRuleID, Steps: []SimulateRuleStepInput{{Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "temp", Value: 1, Quality: "UNCERTAIN"}}}}}),
	} {
		badReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:simulate", strings.NewReader(badBody))
		badReq.Header.Set("Content-Type", "application/json")
		badReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmReadAction, testTenantID, testSiteID, ""))
		badRec := httptest.NewRecorder()
		handler.ServeHTTP(badRec, badReq)
		if badRec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d body=%s", name, badRec.Code, badRec.Body.String())
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
