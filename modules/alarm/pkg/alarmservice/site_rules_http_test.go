package alarmservice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
