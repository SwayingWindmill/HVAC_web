package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/rulegraph"
)

func TestAlarmPolicyDigestByteIdenticalWhenDAGActionsUnset(t *testing.T) {
	base := validAlarmPolicy()
	base.Digest = ""
	rawBase, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	digestBase, err := AlarmPolicyDigest(base)
	if err != nil {
		t.Fatal(err)
	}

	explicitEmpty := base
	explicitEmpty.DependencyGate = ""
	explicitEmpty.Actions = []RuleAction{}
	rawEmpty, err := json.Marshal(explicitEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawEmpty) != string(rawBase) {
		t.Fatalf("unset DependencyGate/Actions changed policy JSON serialization:\nwant: %s\ngot:  %s", rawBase, rawEmpty)
	}
	digestEmpty, err := AlarmPolicyDigest(explicitEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if digestEmpty != digestBase {
		t.Fatalf("digest mismatch for unset fields: %s != %s", digestEmpty, digestBase)
	}
}

func TestValidateDependencyGateAndRuleActionsContracts(t *testing.T) {
	depID := "01910000-7a02-7000-8000-000000000010"
	devID := "01910000-3000-7000-8000-000000000020"

	p := validAlarmPolicy()
	p.DependsOnRuleIDs = []string{depID}
	p.DependencyGate = DependencyGateAllActive
	p.Actions = []RuleAction{
		{
			Kind:       RuleActionWebhook,
			TriggerOn:  ActionTriggerBoth,
			WebhookURL: "https://hooks.example.com/v1/alarm",
			SecretRef:  "alarm-webhook.hmac:v2",
		},
		{
			Kind:              RuleActionWorkOrder,
			WorkOrderPriority: "URGENT",
			WorkOrderTitle:    "紧急排查冷机供水温度",
		},
		{
			Kind:            RuleActionProtectionCommand,
			TriggerOn:       ActionTriggerRaise,
			TargetDeviceID:  devID,
			TargetPointCode: "chilled_setpoint",
			Capability:      "SET_TEMPERATURE_SETPOINT",
			Parameters:      map[string]float64{"setpointC": 24},
		},
	}
	sealAlarmPolicy(&p)
	if err := p.Validate(); err != nil {
		t.Fatalf("expected valid DAG gate and actions, got %v", err)
	}

	for name, mutate := range map[string]func(*AlarmPolicyRevision){
		"gate ALL_ACTIVE without dependsOn": func(pol *AlarmPolicyRevision) {
			pol.DependsOnRuleIDs = nil
			pol.DependencyGate = DependencyGateAllActive
		},
		"gate ANY_ACTIVE without dependsOn": func(pol *AlarmPolicyRevision) {
			pol.DependsOnRuleIDs = nil
			pol.DependencyGate = DependencyGateAnyActive
		},
		"invalid gate enum": func(pol *AlarmPolicyRevision) {
			pol.DependencyGate = "SOME_ACTIVE"
		},
		"more than 4 actions": func(pol *AlarmPolicyRevision) {
			pol.Actions = make([]RuleAction, MaxActionsPerRule+1)
			for i := range pol.Actions {
				pol.Actions[i] = RuleAction{Kind: RuleActionWorkOrder, WorkOrderPriority: "LOW", WorkOrderTitle: "t"}
			}
		},
		"http non-https webhook": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{Kind: RuleActionWebhook, WebhookURL: "http://hooks.example.com/a"}}
		},
		"webhook over 512 chars": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{Kind: RuleActionWebhook, WebhookURL: "https://hooks.example.com/" + strings.Repeat("a", 500)}}
		},
		"webhook invalid secretRef": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{Kind: RuleActionWebhook, WebhookURL: "https://hooks.example.com/a", SecretRef: "bad secret ref!"}}
		},
		"work order invalid priority CRITICAL": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{Kind: RuleActionWorkOrder, WorkOrderPriority: "CRITICAL", WorkOrderTitle: "bad"}}
		},
		"work order invalid priority NORMAL": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{Kind: RuleActionWorkOrder, WorkOrderPriority: "NORMAL", WorkOrderTitle: "bad"}}
		},
		"work order title over 200": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{Kind: RuleActionWorkOrder, WorkOrderPriority: "HIGH", WorkOrderTitle: strings.Repeat("x", 201)}}
		},
		"protection command out-of-range frequency": func(pol *AlarmPolicyRevision) {
			pol.Actions = []RuleAction{{
				Kind:           RuleActionProtectionCommand,
				TargetDeviceID: devID,
				Capability:     "SET_FREQUENCY",
				Parameters:     map[string]float64{"frequencyHz": 85},
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := validAlarmPolicy()
			bad.DependsOnRuleIDs = []string{depID}
			mutate(&bad)
			sealAlarmPolicy(&bad)
			if err := bad.Validate(); err == nil {
				t.Fatalf("expected validation failure for %s", name)
			}
		})
	}
}

func TestValidateSiteRuleGraphBlocksPhysicalWriteFeedbackCycles(t *testing.T) {
	const (
		ruleAID = "01910000-7a02-7000-8000-000000000001"
		ruleBID = "01910000-7a02-7000-8000-000000000002"
		devA    = "01910000-3000-7000-8000-000000000001"
		devB    = "01910000-3000-7000-8000-000000000002"
	)

	makeRule := func(id, code, readDev, writeDev string) SiteRule {
		p := validAlarmPolicy()
		p.AlarmType = code
		p.Raise = Condition{Kind: ConditionCompare, Input: readDev + "/temp", Operator: CompareGT, Value: NumberValue(10)}
		p.Clear = Condition{Kind: ConditionCompare, Input: readDev + "/temp", Operator: CompareLT, Value: NumberValue(5)}
		if writeDev != "" {
			p.Actions = []RuleAction{{
				Kind:           RuleActionProtectionCommand,
				TargetDeviceID: writeDev,
				Capability:     "STOP",
			}}
		}
		return SiteRule{AssignmentID: id, Policy: p}
	}

	if _, err := ValidateSiteRuleGraph([]SiteRule{
		makeRule(ruleAID, "self-loop", devA, devA),
	}); !errors.Is(err, rulegraph.ErrRuleCycleDetected) {
		t.Fatalf("expected ErrRuleCycleDetected for same-rule read/write, got %v", err)
	}

	if _, err := ValidateSiteRuleGraph([]SiteRule{
		makeRule(ruleAID, "a-writes-b", devA, devB),
		makeRule(ruleBID, "b-writes-a", devB, devA),
	}); !errors.Is(err, rulegraph.ErrRuleCycleDetected) {
		t.Fatalf("expected ErrRuleCycleDetected for cross-device write cycle, got %v", err)
	}

	report, err := ValidateSiteRuleGraph([]SiteRule{
		makeRule(ruleBID, "reader-b", devB, ""),
		makeRule(ruleAID, "writer-a-to-b", devA, devB),
	})
	if err != nil {
		t.Fatalf("expected acyclic write->read DAG to succeed, got %v", err)
	}
	if !slices.Equal(report.Order, []string{ruleAID, ruleBID}) {
		t.Fatalf("unexpected topological order for write->read DAG: %v", report.Order)
	}
}

func TestRuntimeRuleOrderingRelevanceAndDependencyGateEvaluation(t *testing.T) {
	const (
		devA  = "01910000-3000-7000-8000-000000000001"
		devB  = "01910000-3000-7000-8000-000000000002"
		rUp1  = "01910000-7a02-7000-8000-000000000003"
		rUp2  = "01910000-7a02-7000-8000-000000000004"
		rMid  = "01910000-7a02-7000-8000-000000000001"
		rLeaf = "01910000-7a02-7000-8000-000000000002"
	)

	buildRule := func(id, dev string, deps []string, gate DependencyGateMode) SiteRule {
		p := validAlarmPolicy()
		p.PolicyID = id
		p.DependsOnRuleIDs = deps
		p.DependencyGate = gate
		p.Raise = Condition{Kind: ConditionCompare, Input: dev + "/t", Operator: CompareGT, Value: NumberValue(10)}
		p.Clear = Condition{Kind: ConditionCompare, Input: dev + "/t", Operator: CompareLTE, Value: NumberValue(5)}
		sealAlarmPolicy(&p)
		return SiteRule{AssignmentID: id, Policy: p}
	}

	rules := []SiteRule{
		buildRule(rLeaf, devB, []string{rMid}, DependencyGateAllActive),
		buildRule(rMid, devB, []string{rUp1, rUp2}, DependencyGateAllActive),
		buildRule(rUp1, devA, nil, ""),
		buildRule(rUp2, devB, nil, ""),
	}

	ordered := orderSiteRulesForEvaluation(rules)
	idx := map[string]int{}
	for i, r := range ordered {
		idx[r.AssignmentID] = i
	}
	if idx[rUp1] > idx[rMid] || idx[rUp2] > idx[rMid] || idx[rMid] > idx[rLeaf] {
		t.Fatalf("rules not sorted topologically: %#v", idx)
	}

	relevant := selectRelevantSiteRules(ordered, devA)
	var relIDs []string
	for _, r := range relevant {
		relIDs = append(relIDs, r.AssignmentID)
	}
	if !slices.Equal(relIDs, []string{rUp1, rMid, rLeaf}) {
		t.Fatalf("expected direct reader + transitive dependents [rUp1 rMid rLeaf], got %v", relIDs)
	}

	// Legacy dangling dependency falls back deterministically by AssignmentID without failing silent.
	legacyFallback := orderSiteRulesForEvaluation([]SiteRule{
		buildRule(rUp1, devA, nil, ""),
		buildRule(rLeaf, devB, []string{"01910000-7a02-7000-8000-0000000000ff"}, DependencyGateAllActive),
	})
	if len(legacyFallback) != 2 || legacyFallback[0].AssignmentID != rLeaf || legacyFallback[1].AssignmentID != rUp1 {
		t.Fatalf("expected AssignmentID deterministic fallback, got %v", legacyFallback)
	}

	// EvaluatePolicy gate tests: ALL_ACTIVE, ANY_ACTIVE, and hold/recover behavior.
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	snap := func(rev string, val float64, active map[string]bool) EvaluationSnapshot {
		s := evaluationSnapshot(rev, now.Format(time.RFC3339Nano), NumberInput(devB+"/t", val, "GOOD", now.Format(time.RFC3339Nano)))
		s.UpstreamRuleActive = active
		return s
	}

	// ALL_ACTIVE requires both rUp1 and rUp2.
	gateAll := buildRule(rMid, devB, []string{rUp1, rUp2}, DependencyGateAllActive).Policy
	d1, err := EvaluatePolicy(gateAll, snap("s1", 20, map[string]bool{rUp1: true}), AlarmEvaluationState{}, now)
	if err != nil || d1.Effect != EvaluationEffectNone || d1.Status != EvaluationNotMatched {
		t.Fatalf("ALL_ACTIVE fired with only 1 of 2 upstreams active: %#v err=%v", d1, err)
	}
	d2, err := EvaluatePolicy(gateAll, snap("s2", 20, map[string]bool{rUp1: true, rUp2: true}), d1.State, now)
	if err != nil || d2.Effect != EvaluationEffectPublish {
		t.Fatalf("ALL_ACTIVE did not fire when both upstreams active: %#v err=%v", d2, err)
	}
	d2.State.ActiveAlarmID = testAlarmID
	d2.State.ActiveIncidentCorrelationID = testIncidentCorrID

	// Upstream drops while reading still high -> alarm holds cleanly (no spurious clear or raise).
	d3, err := EvaluatePolicy(gateAll, snap("s3", 20, map[string]bool{}), d2.State, now)
	if err != nil || d3.Effect != EvaluationEffectNone || d3.Status != EvaluationMatched {
		t.Fatalf("active alarm failed to hold cleanly when upstream dropped: %#v err=%v", d3, err)
	}
	// Clear predicate still recovers cleanly even with upstream inactive.
	d4, err := EvaluatePolicy(gateAll, snap("s4", 3, map[string]bool{}), d3.State, now)
	if err != nil || d4.Effect != EvaluationEffectClear {
		t.Fatalf("active alarm failed to recover on Clear predicate: %#v err=%v", d4, err)
	}

	// ANY_ACTIVE triggers as soon as one dependency is active.
	gateAny := buildRule(rMid, devB, []string{rUp1, rUp2}, DependencyGateAnyActive).Policy
	dAny, err := EvaluatePolicy(gateAny, snap("s5", 20, map[string]bool{rUp2: true}), AlarmEvaluationState{}, now)
	if err != nil || dAny.Effect != EvaluationEffectPublish {
		t.Fatalf("ANY_ACTIVE did not fire with single active dependency: %#v err=%v", dAny, err)
	}
}

func TestSiteRuleListAndSimulateUpstreamAndPlannedActions(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	const (
		upRuleID   = "018f3e00-7a02-7000-8000-000000000010"
		downRuleID = "018f3e00-7a02-7000-8000-000000000011"
		targetDev  = "018f3e00-3000-7000-8000-000000000099"
	)
	rules := []SiteRule{
		{
			AssignmentID: upRuleID,
			Policy: AlarmPolicyRevision{
				Title: "上游压差低", Severity: alarmmodel.SeverityWarning, TriggerMode: TriggerSimple,
				Raise: Condition{Kind: ConditionCompare, Input: testDeviceID + "/dp", Operator: CompareLT, Value: NumberValue(30)},
				Clear: Condition{Kind: ConditionCompare, Input: testDeviceID + "/dp", Operator: CompareGTE, Value: NumberValue(40)},
			},
		},
		{
			AssignmentID: downRuleID,
			Policy: AlarmPolicyRevision{
				Title: "下游保护动作规则", Severity: alarmmodel.SeverityMajor, TriggerMode: TriggerSimple,
				DependsOnRuleIDs: []string{upRuleID},
				DependencyGate:   DependencyGateAllActive,
				Actions: []RuleAction{
					{Kind: RuleActionWorkOrder, TriggerOn: ActionTriggerRaise, WorkOrderPriority: "HIGH", WorkOrderTitle: "检查冷冻泵"},
					{Kind: RuleActionProtectionCommand, TriggerOn: ActionTriggerBoth, TargetDeviceID: targetDev, TargetPointCode: "cmd_stop", Capability: "STOP"},
				},
				Raise: Condition{Kind: ConditionCompare, Input: testDeviceID + "/temp", Operator: CompareGT, Value: NumberValue(12)},
				Clear: Condition{Kind: ConditionCompare, Input: testDeviceID + "/temp", Operator: CompareLTE, Value: NumberValue(8)},
			},
		},
	}
	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID, rules: rules},
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify GET /alarm-rules exposes dependencyGate and actions.
	listReq := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules", nil)
	listReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET alarm-rules status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var list SiteRuleList
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if err := list.Validate(); err != nil {
		t.Fatalf("SiteRuleList.Validate failed: %v", err)
	}
	if len(list.Items) != 2 || list.Items[1].DependencyGate != DependencyGateAllActive || len(list.Items[1].Actions) != 2 {
		t.Fatalf("expected dependencyGate and actions in SiteRuleList: %#v", list.Items)
	}

	// Verify POST /alarm-rules:simulate respects upstreamActiveRuleIds and emits plannedActions with zero store mutations.
	simBody := mustJSON(t, SimulateSiteRuleRequest{
		RuleID: downRuleID,
		Steps: []SimulateRuleStepInput{
			{
				Readings: []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "temp", Value: 15.0}},
			},
			{
				UpstreamActiveRuleIDs: []string{upRuleID},
				Readings:              []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "temp", Value: 15.0}},
			},
			{
				UpstreamActiveRuleIDs: []string{upRuleID},
				Readings:              []SimulateRuleReadingSample{{DeviceID: testDeviceID, PointCode: "temp", Value: 6.0}},
			},
		},
	})
	simReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules:simulate", strings.NewReader(simBody))
	simReq.Header.Set("Content-Type", "application/json")
	simReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	simRec := httptest.NewRecorder()
	handler.ServeHTTP(simRec, simReq)
	if simRec.Code != http.StatusOK {
		t.Fatalf("simulate status=%d body=%s", simRec.Code, simRec.Body.String())
	}
	var sim SiteRuleSimulationResult
	if err := json.Unmarshal(simRec.Body.Bytes(), &sim); err != nil {
		t.Fatal(err)
	}
	if err := sim.Validate(downRuleID); err != nil {
		t.Fatalf("simulation result failed validation: %v", err)
	}
	if len(sim.Steps) != 3 || sim.Steps[0].Effect != EvaluationEffectNone || len(sim.Steps[0].PlannedActions) != 0 {
		t.Fatalf("step 1 should be gated off with 0 plannedActions: %#v", sim.Steps)
	}
	if sim.Steps[1].Effect != EvaluationEffectPublish || len(sim.Steps[1].PlannedActions) != 2 {
		t.Fatalf("step 2 should publish with 2 plannedActions: %#v", sim.Steps[1])
	}
	if sim.Steps[2].Effect != EvaluationEffectClear || len(sim.Steps[2].PlannedActions) != 1 || sim.Steps[2].PlannedActions[0].Kind != RuleActionProtectionCommand {
		t.Fatalf("step 3 should clear with 1 BOTH protection command plannedAction: %#v", sim.Steps[2])
	}
	listedAlarms, err := memory.List(context.Background(), testTenantID, testSiteID, Filter{Limit: 10})
	if err != nil || len(listedAlarms.Items) != 0 {
		t.Fatalf("simulate must not write alarms to store: items=%v err=%v", listedAlarms.Items, err)
	}
}

func TestSSRFWebhookSenderBlocksPrivateTargetsRedirectsAndSignsCleanPayload(t *testing.T) {
	for _, blockedAddr := range []string{
		"127.0.0.1:443",
		"[::1]:443",
		"10.0.0.1:443",
		"172.16.5.4:443",
		"192.168.1.10:443",
		"169.254.169.254:443",
		"[fc00::1]:443",
		"[fd12:3456::1]:443",
		"[fe80::1]:443",
		"[fe80::1%eth0]:443",
		"224.0.0.1:443",
		"[ff02::1]:443",
		"[::ffff:127.0.0.1]:443",
		"[::ffff:169.254.169.254]:443",
		"100.64.0.1:443",
		"0.0.0.0:443",
	} {
		if err := ValidatePublicWebhookAddress(blockedAddr); !errors.Is(err, ErrWebhookSSRFBlocked) {
			t.Fatalf("expected SSRF block for %q, got %v", blockedAddr, err)
		}
	}
	for _, publicAddr := range []string{"1.1.1.1:443", "8.8.8.8:8443", "[2606:4700:4700::1111]:443"} {
		if err := ValidatePublicWebhookAddress(publicAddr); err != nil {
			t.Fatalf("expected public unicast %q allowed, got %v", publicAddr, err)
		}
	}
	if !isPublicUnicastAddr(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("expected 1.1.1.1 public unicast")
	}

	// End-to-end dial attempt against local TLS server must be stopped by net.Dialer.Control.
	localTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer localTLS.Close()

	sender := NewSSRFWebhookSender(nil)
	err := sender.Send(context.Background(), ClaimedRuleAction{
		OutboxID:   "01910000-0001-7000-8000-000000000001",
		ActionKind: RuleActionWebhook,
		Payload:    RuleActionPayload{WebhookURL: localTLS.URL + "/hook"},
	})
	if !errors.Is(err, ErrWebhookSSRFBlocked) {
		t.Fatalf("expected ErrWebhookSSRFBlocked for loopback TLS server %s, got %v", localTLS.URL, err)
	}

	// Verify CheckRedirect refuses redirects and HMAC signing omits secretRef from body.
	var capturedBody string
	var capturedSig string
	mockClient := NewSSRFWebhookHTTPClient(nil)
	mockClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/redirect" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://other.example.com/next"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		raw, _ := io.ReadAll(req.Body)
		capturedBody = string(raw)
		capturedSig = req.Header.Get("X-HVAC-Alarm-Signature")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})
	testSender := &SSRFWebhookSender{
		Client: mockClient,
		SecretResolver: func(_ context.Context, secretRef string) ([]byte, error) {
			if secretRef != "prod-hook-key" {
				return nil, errors.New("not found")
			}
			return []byte("top-secret-hmac-key"), nil
		},
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
	if err := testSender.Send(context.Background(), ClaimedRuleAction{
		OutboxID: "01910000-0001-7000-8000-000000000003", ActionKind: RuleActionWebhook,
		Payload: RuleActionPayload{WebhookURL: "https://webhook.example.com/redirect"},
	}); !errors.Is(err, ErrWebhookRedirectBlocked) {
		t.Fatalf("expected ErrWebhookRedirectBlocked on 302, got %v", err)
	}
	if err := testSender.Send(context.Background(), ClaimedRuleAction{
		OutboxID: "01910000-0001-7000-8000-000000000004", TenantID: testTenantID, SiteID: testSiteID,
		ActionKind: RuleActionWebhook, TriggerKind: ActionTriggerRaise,
		Payload: RuleActionPayload{
			AlarmType: "high-temp", Severity: "MAJOR", Title: "High Temp",
			WebhookURL: "https://webhook.example.com/events?token=must-not-matter",
			SecretRef:  "prod-hook-key",
		},
	}); err != nil {
		t.Fatalf("expected signed webhook send to succeed, got %v", err)
	}
	if strings.Contains(capturedBody, "prod-hook-key") || strings.Contains(capturedBody, "top-secret-hmac-key") || !strings.HasPrefix(capturedSig, "sha256=") {
		t.Fatalf("webhook signature or sanitization violated: sig=%q body=%s", capturedSig, capturedBody)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type stubRuleActionStore struct {
	mu          sync.Mutex
	items       []ClaimedRuleAction
	delivered   []string
	retried     []stubRuleActionFailure
	deadLetters []stubRuleActionFailure
	pendingCnt  int64
	dlqCnt      int64
}

type stubRuleActionFailure struct {
	outboxID    string
	reasonCode  string
	nextAttempt time.Time
}

func (s *stubRuleActionStore) ListTenantsWithPendingRuleActions(context.Context, time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return nil, nil
	}
	return []string{s.items[0].TenantID}, nil
}

func (s *stubRuleActionStore) ClaimRuleActionOutbox(_ context.Context, tenantID, _ string, _ time.Time, _ time.Duration, _ int) ([]ClaimedRuleAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ClaimedRuleAction
	for i := range s.items {
		if s.items[i].TenantID == tenantID {
			s.items[i].Attempts++
			out = append(out, s.items[i])
		}
	}
	return out, nil
}

func (s *stubRuleActionStore) MarkRuleActionDelivered(_ context.Context, _, outboxID, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered = append(s.delivered, outboxID)
	s.removeItem(outboxID)
	return nil
}

func (s *stubRuleActionStore) FailRuleActionOutbox(_ context.Context, _, outboxID, _, reasonCode string, nextAttempt time.Time, _ time.Time, deadLetter bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if deadLetter {
		s.deadLetters = append(s.deadLetters, stubRuleActionFailure{outboxID: outboxID, reasonCode: reasonCode})
		s.removeItem(outboxID)
		s.dlqCnt++
	} else {
		s.retried = append(s.retried, stubRuleActionFailure{outboxID: outboxID, reasonCode: reasonCode, nextAttempt: nextAttempt})
	}
	return nil
}

func (s *stubRuleActionStore) removeItem(outboxID string) {
	for i := range s.items {
		if s.items[i].OutboxID == outboxID {
			s.items = append(s.items[:i], s.items[i+1:]...)
			if s.pendingCnt > 0 {
				s.pendingCnt--
			}
			return
		}
	}
}

func (s *stubRuleActionStore) CountRuleActionOutboxStates(context.Context) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingCnt, s.dlqCnt, nil
}

func TestRuleActionRelayBackoffDeadLetterProtectionTierAndMetrics(t *testing.T) {
	now := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	registry := observability.NewRegistry()
	const (
		outCmd  = "01910000-8800-7000-8000-000000000001"
		outSSRF = "01910000-8800-7000-8000-000000000002"
		outWO   = "01910000-8800-7000-8000-000000000003"
		devID   = "01910000-3000-7000-8000-000000000044"
	)

	store := &stubRuleActionStore{
		pendingCnt: 3,
		items: []ClaimedRuleAction{
			{
				OutboxID: outCmd, TenantID: testTenantID, SiteID: testSiteID, AlarmID: testAlarmID, AlarmVersion: 1,
				ActionKind: RuleActionProtectionCommand, TriggerKind: ActionTriggerRaise,
				Payload: RuleActionPayload{TargetDeviceID: devID, TargetPointCode: "fan_cmd", Capability: "SET_FREQUENCY", Parameters: map[string]float64{"frequencyHz": 40}},
			},
			{
				OutboxID: outSSRF, TenantID: testTenantID, SiteID: testSiteID, AlarmID: testAlarmID, AlarmVersion: 1,
				ActionKind: RuleActionWebhook, TriggerKind: ActionTriggerRaise,
				Payload: RuleActionPayload{WebhookURL: "https://127.0.0.1/internal"},
			},
			{
				OutboxID: outWO, TenantID: testTenantID, SiteID: testSiteID, AlarmID: testAlarmID, AlarmVersion: 1,
				ActionKind: RuleActionWorkOrder, TriggerKind: ActionTriggerRaise,
				Payload: RuleActionPayload{WorkOrderPriority: "HIGH", WorkOrderTitle: "排查故障"},
			},
		},
	}

	var gotCmd RuleActionProtectionCommandRequest
	var woAttempts int
	relay, err := NewRuleActionRelay(RuleActionRelayConfig{
		Store:       store,
		Metrics:     registry,
		Now:         func() time.Time { return now },
		MaxAttempts: 2,
		BaseBackoff: 5 * time.Second,
		MaxBackoff:  20 * time.Second,
		SubmitProtectionCommand: func(_ context.Context, req RuleActionProtectionCommandRequest) error {
			gotCmd = req
			return nil
		},
		CreateWorkOrder: func(context.Context, RuleActionWorkOrderRequest) error {
			woAttempts++
			return errors.New("temporary work order service error")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res1, err := relay.RelayOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected relay error: %v", err)
	}
	if res1.Delivered != 1 || res1.DeadLettered != 1 || res1.Retried != 1 {
		t.Fatalf("unexpected first pass result: %#v", res1)
	}
	if gotCmd.PriorityTier != commandmodel.PriorityProtectionRule || gotCmd.CommandSource != commandmodel.SourceProtectionRule || gotCmd.Parameters["frequencyHz"] != 40 {
		t.Fatalf("protection command request missing PROTECTION_RULE metadata: %#v", gotCmd)
	}
	if len(store.deadLetters) != 1 || store.deadLetters[0].outboxID != outSSRF || store.deadLetters[0].reasonCode != "ssrf_blocked" {
		t.Fatalf("SSRF webhook did not short-circuit straight to DEAD_LETTER: %#v", store.deadLetters)
	}
	if len(store.retried) != 1 || !store.retried[0].nextAttempt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("expected 5s backoff on attempt 1, got %#v", store.retried)
	}

	// Second pass hits maxAttempts=2 for work order and transitions to DEAD_LETTER.
	now = now.Add(5 * time.Second)
	res2, err := relay.RelayOnce(context.Background())
	if err != nil || res2.DeadLettered != 1 || woAttempts != 2 {
		t.Fatalf("expected work order DEAD_LETTER on attempt 2: res=%#v err=%v attempts=%d", res2, err, woAttempts)
	}

	scraped := httptest.NewRecorder()
	registry.Handler().ServeHTTP(scraped, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := scraped.Body.String()
	for _, want := range []string{
		`hvac_alarm_action_dead_letter_total{reason_family="authorization"} 1`,
		`hvac_alarm_action_dead_letter_total{reason_family="dependency"} 1`,
		`hvac_alarm_action_outbox_depth{state="pending"} 0`,
		`hvac_alarm_action_outbox_depth{state="dead_letter"} 2`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("missing metric line %q in:\n%s", want, metricsBody)
		}
	}
}

type stubDeadLetterStore struct {
	list          RuleActionDeadLetterList
	replayedID    string
	replayErr     error
	replayStatus  RuleActionStatus
	lastListLimit int
}

func (s *stubDeadLetterStore) ListRuleActionDeadLetters(_ context.Context, _, _ string, limit int) (RuleActionDeadLetterList, error) {
	s.lastListLimit = limit
	return s.list, nil
}

func (s *stubDeadLetterStore) ReplayRuleActionDeadLetter(_ context.Context, _, _, outboxID string, _ time.Time) (RuleActionStatus, error) {
	s.replayedID = outboxID
	if s.replayErr != nil {
		return "", s.replayErr
	}
	if s.replayStatus != "" {
		return s.replayStatus, nil
	}
	return RuleActionStatusPending, nil
}

func TestDeadLetterHTTPListSanitizedAndReplayAuth(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC)
	signer := testSigner(t)
	memory, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	const outboxID = "01910000-9900-7000-8000-000000000001"
	dlStore := &stubDeadLetterStore{
		list: RuleActionDeadLetterList{
			Items: []RuleActionDeadLetterView{{
				OutboxID:       outboxID,
				SiteID:         testSiteID,
				AlarmID:        testAlarmID,
				AlarmVersion:   1,
				AssignmentID:   "018f3e00-7a02-7000-8000-000000000011",
				ActionIndex:    0,
				TriggerKind:    ActionTriggerRaise,
				ActionKind:     RuleActionWebhook,
				TargetRef:      "https://hooks.example.com/alarm",
				Attempts:       5,
				LastErrorCode:  "http_5xx",
				DeadLetteredAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
				CreatedAt:      now.Add(-5 * time.Minute).Format(time.RFC3339Nano),
			}},
		},
	}

	handler, err := NewHTTPHandler(HTTPConfig{
		Store: memory, Rules: siteRules{siteID: testSiteID}, DeadLetters: dlStore,
		GatewayPublicKey: &signer.PublicKey, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	getReq := httptest.NewRequest(http.MethodGet, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/dead-letters?limit=15", nil)
	getReq.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || dlStore.lastListLimit != 15 {
		t.Fatalf("GET dead-letters status=%d limit=%d body=%s", getRec.Code, dlStore.lastListLimit, getRec.Body.String())
	}
	if strings.Contains(getRec.Body.String(), "secretRef") || strings.Contains(getRec.Body.String(), "Authorization") {
		t.Fatalf("dead-letter list leaked sensitive fields: %s", getRec.Body.String())
	}

	// Wrong header / read context rejected on replay.
	unauthReplay := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/dead-letters/"+outboxID+"/replay", nil)
	unauthReplay.Header.Set(AlarmReadContextHeader, signedReadContext(t, signer, now, AlarmListAction, testTenantID, testSiteID, ""))
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReplay)
	if unauthRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when replay lacks write delegation header, got %d", unauthRec.Code)
	}

	replayRec := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/dead-letters/"+outboxID+"/replay", nil)
	replayReq := replayRec
	replayReq.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRuleDeadLetterReplayAction, testTenantID, testSiteID, ""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, replayReq)
	if rec.Code != http.StatusOK || dlStore.replayedID != outboxID {
		t.Fatalf("POST dead-letter replay status=%d replayed=%q body=%s", rec.Code, dlStore.replayedID, rec.Body.String())
	}
	var res RuleActionReplayResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.Status != RuleActionStatusPending || res.OutboxID != outboxID {
		t.Fatalf("unexpected replay response: err=%v body=%s", err, rec.Body.String())
	}

	dlStore.replayStatus = RuleActionStatusDelivered
	idempReq := httptest.NewRequest(http.MethodPost, InternalSiteAlarmsPrefix+testSiteID+"/alarm-rules/dead-letters/"+outboxID+"/replay", nil)
	idempReq.Header.Set(AlarmWriteContextHeader, signedReadContext(t, signer, now, AlarmRuleDeadLetterReplayAction, testTenantID, testSiteID, ""))
	idempRec := httptest.NewRecorder()
	handler.ServeHTTP(idempRec, idempReq)
	if idempRec.Code != http.StatusOK {
		t.Fatalf("idempotent replay status=%d body=%s", idempRec.Code, idempRec.Body.String())
	}
	var res2 RuleActionReplayResult
	if err := json.Unmarshal(idempRec.Body.Bytes(), &res2); err != nil || res2.Status != RuleActionStatusDelivered {
		t.Fatalf("unexpected idempotent replay status: err=%v body=%s", err, idempRec.Body.String())
	}
}

func TestIsPublicUnicastAddrBlocksReservedAndNAT64Prefixes(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "0.255.255.254", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"192.0.0.1", "192.0.2.1", "198.18.0.1", "198.19.255.254", "198.51.100.10", "203.0.113.5",
		"224.0.0.1", "240.0.0.1",
		"::", "::1", "fe80::1", "fc00::1", "2001:db8::1", "2002:7f00:1::1", "100::1", "64:ff9b::10.0.0.1", "64:ff9b:1::1",
	}
	for _, raw := range blocked {
		addr := netip.MustParseAddr(raw)
		if isPublicUnicastAddr(addr) {
			t.Fatalf("expected non-public address %s to be blocked", raw)
		}
	}
	for _, raw := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		addr := netip.MustParseAddr(raw)
		if !isPublicUnicastAddr(addr) {
			t.Fatalf("expected public unicast address %s to be allowed", raw)
		}
	}
}

func TestDirWebhookSecretResolver(t *testing.T) {
	if NewDirWebhookSecretResolver("   ") != nil {
		t.Fatalf("expected nil resolver for blank base dir")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hook.hmac:v1"), []byte("super-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.hmac"), []byte("\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewDirWebhookSecretResolver(dir)
	secret, err := resolver(context.Background(), "hook.hmac:v1")
	if err != nil || string(secret) != "super-secret" {
		t.Fatalf("expected secret loaded, got %q err=%v", string(secret), err)
	}
	for _, ref := range []string{"../escape", ".hidden", "missing.hmac", "empty.hmac"} {
		_, err := resolver(context.Background(), ref)
		var perm *PermanentRuleActionError
		if !errors.As(err, &perm) || perm.ReasonCode != "secret_unavailable" {
			t.Fatalf("ref=%q expected permanent secret_unavailable, got %v", ref, err)
		}
	}
}
