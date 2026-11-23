package alarmservice

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestCloudAndEdgeExpressionValidateParity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		name   string
		expr   string
		inputs map[string]string
		valid  bool
	}{
		{"arithmetic compare", "(a - b) * 2 >= 4.5", map[string]string{"a": "ch1/sup", "b": "ch1/ret"}, true},
		{"builtin funcs", "abs(a - b) <= max(1.0, min(5.0, c))", map[string]string{"a": "ch1/sup", "b": "ch1/ret", "c": "ch1/lim"}, true},
		{"bool and string", "running && mode == 'AUTO' && !(a < 0)", map[string]string{"running": "ch1/run", "mode": "ch1/mode", "a": "ch1/sup"}, true},
		{"undeclared alias", "a + b > 0", map[string]string{"a": "ch1/sup"}, false},
		{"unused alias", "a > 0", map[string]string{"a": "ch1/sup", "b": "ch1/ret"}, false},
		{"non-boolean root", "a + 1", map[string]string{"a": "ch1/sup"}, false},
		{"unknown func", "sqrt(a) > 1", map[string]string{"a": "ch1/sup"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cloudErr := ValidateExpression(tc.expr, tc.inputs)
			bundle := edgecontrol.EdgeRuleBundle{
				SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
				SiteID:         "11111111-1111-4111-8111-111111111111",
				BundleRevision: 1,
				CompiledAt:     now.Format(time.RFC3339),
				Rules: []edgecontrol.CompiledEdgeRule{{
					RuleID:           "22222222-2222-4222-8222-222222222222",
					Title:            "Validate parity",
					Severity:         "WARNING",
					ExecutionTarget:  edgecontrol.EdgeTargetEdge,
					QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
					FreshnessSeconds: 300,
					TriggerMode:      edgecontrol.EdgeTriggerSimple,
					Raise: edgecontrol.EdgeCondition{
						Kind:       edgecontrol.EdgeConditionExpression,
						Expression: tc.expr,
						Inputs:     tc.inputs,
					},
					Clear: edgecontrol.EdgeCondition{
						Kind:     edgecontrol.EdgeConditionCompare,
						Input:    "ch1/sup",
						Operator: edgecontrol.EdgeCompareLT,
						Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 0},
					},
				}},
			}
			digest, err := bundle.ComputeDigest()
			if err != nil {
				t.Fatalf("ComputeDigest() error = %v", err)
			}
			bundle.EdgeBundleDigest = digest
			edgeErr := bundle.Validate()
			if tc.valid && (cloudErr != nil || edgeErr != nil) {
				t.Fatalf("expected both valid, got cloud=%v edge=%v", cloudErr, edgeErr)
			}
			if !tc.valid && (cloudErr == nil || edgeErr == nil) {
				t.Fatalf("expected both rejected, got cloud=%v edge=%v", cloudErr, edgeErr)
			}
		})
	}
}

type edgeTestInput struct {
	key        string
	dataType   edgecontrol.DataType
	val        edgecontrol.Value
	quality    edgecontrol.Quality
	observedAt time.Time
	cloudFact  InputFact
}

func TestCloudAndEdgeExpressionEvalParityAndIntentionalDivergence(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	obs := now.Format(time.RFC3339Nano)
	policy := AlarmPolicyRevision{
		FreshnessSeconds: 300,
		QualityPolicy:    EvaluationQualityValidOnly,
	}

	runEdgeOnce := func(t *testing.T, expr string, inputs map[string]string, points []edgeTestInput) (raised bool, hasEvent bool) {
		t.Helper()
		rt := edgecontrol.NewRuntime()
		for _, p := range points {
			parts := splitKey(t, p.key)
			desc := edgecontrol.ChannelDescriptor{
				ComponentID:               parts[0],
				ChannelID:                 parts[1],
				PointID:                   p.key,
				DataType:                  p.dataType,
				Access:                    edgecontrol.AccessReadOnly,
				Category:                  edgecontrol.ChannelCategoryOpenemsType,
				PollPriority:              edgecontrol.PriorityHigh,
				LocalPersistencePriority:  edgecontrol.PriorityHigh,
				RemotePersistencePriority: edgecontrol.PriorityHigh,
				AggregationPriority:       edgecontrol.PriorityHigh,
				ResendPriority:            edgecontrol.PriorityHigh,
			}
			if err := rt.Register(desc); err != nil {
				t.Fatalf("Register(%s) error = %v", p.key, err)
			}
			if p.quality != "" {
				sample := edgecontrol.Sample{
					Value:      p.val,
					Quality:    p.quality,
					ObservedAt: p.observedAt,
					Sequence:   1,
				}
				if err := rt.PublishNext(p.key, sample); err != nil {
					t.Fatalf("PublishNext(%s) error = %v", p.key, err)
				}
			}
		}

		ctrl := edgecontrol.NewEdgeRuleController(edgecontrol.EdgeRuleControllerConfig{})
		ctrl.SetOffline(true)
		bundle := edgecontrol.EdgeRuleBundle{
			SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
			SiteID:         "11111111-1111-4111-8111-111111111111",
			BundleRevision: 1,
			CompiledAt:     now.Format(time.RFC3339),
			Rules: []edgecontrol.CompiledEdgeRule{{
				RuleID:           "22222222-2222-4222-8222-222222222222",
				Title:            "Parity test",
				Severity:         "WARNING",
				ExecutionTarget:  edgecontrol.EdgeTargetEdge,
				QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
				FreshnessSeconds: 300,
				TriggerMode:      edgecontrol.EdgeTriggerSimple,
				Raise: edgecontrol.EdgeCondition{
					Kind:       edgecontrol.EdgeConditionExpression,
					Expression: expr,
					Inputs:     inputs,
				},
				Clear: edgecontrol.EdgeCondition{
					Kind:       edgecontrol.EdgeConditionExpression,
					Expression: "!(" + expr + ")",
					Inputs:     inputs,
				},
			}},
		}
		digest, err := bundle.ComputeDigest()
		if err != nil {
			t.Fatalf("ComputeDigest() error = %v", err)
		}
		bundle.EdgeBundleDigest = digest
		if err := ctrl.UpdateBundle(&bundle); err != nil {
			t.Fatalf("UpdateBundle() error = %v", err)
		}

		img := rt.SwitchProcessImage(now)
		plan := edgecontrol.NewControlPlan(img)
		if err := ctrl.Run(context.Background(), img, plan); err != nil {
			t.Fatalf("ctrl.Run() error = %v", err)
		}
		events := ctrl.DrainEvents()
		return len(events) > 0 && events[0].EventType == edgecontrol.EdgeRuleEventRaised, len(events) > 0
	}

	runCloudOnce := func(expr string, inputs map[string]string, points []edgeTestInput) predicateResult {
		snapInputs := make(map[string]InputFact, len(points))
		for _, p := range points {
			if p.quality != "" {
				snapInputs[p.key] = p.cloudFact
			}
		}
		return evaluateExpression(policy, Condition{
			Kind:       ConditionExpression,
			Expression: expr,
			ExprInputs: inputs,
		}, EvaluationSnapshot{Inputs: snapInputs}, now)
	}

	// Parity cases: all declared inputs are GOOD & fresh -> cloud and edge must match identically.
	parityCases := []struct {
		name      string
		expr      string
		inputs    map[string]string
		points    []edgeTestInput
		wantMatch bool
		wantIndet bool
	}{
		{
			name:   "matched numeric delta",
			expr:   "(a - b) / max(c, 1) >= 2.0 && abs(-3) == 3",
			inputs: map[string]string{"a": "ch1/sup", "b": "ch1/ret", "c": "ch1/div"},
			points: []edgeTestInput{
				{key: "ch1/sup", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(14), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/sup", 14, "GOOD", obs)},
				{key: "ch1/ret", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(9), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/ret", 9, "GOOD", obs)},
				{key: "ch1/div", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(2), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/div", 2, "GOOD", obs)},
			},
			wantMatch: true,
		},
		{
			name:   "not matched with boolean and string",
			expr:   "run && mode == 'AUTO' && min(a, 10) < 8",
			inputs: map[string]string{"run": "ch1/run", "mode": "ch1/mode", "a": "ch1/sup"},
			points: []edgeTestInput{
				{key: "ch1/run", dataType: edgecontrol.DataTypeBoolean, val: edgecontrol.BooleanValue(true), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: BooleanInput("ch1/run", true, "GOOD", obs)},
				{key: "ch1/mode", dataType: edgecontrol.DataTypeString, val: edgecontrol.StringValue("MANUAL"), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: StringInput("ch1/mode", "MANUAL", "GOOD", obs)},
				{key: "ch1/sup", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(7), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/sup", 7, "GOOD", obs)},
			},
			wantMatch: false,
		},
		{
			name:   "division by zero indeterminate on both sides",
			expr:   "a / b > 1",
			inputs: map[string]string{"a": "ch1/sup", "b": "ch1/ret"},
			points: []edgeTestInput{
				{key: "ch1/sup", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(10), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/sup", 10, "GOOD", obs)},
				{key: "ch1/ret", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(0), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/ret", 0, "GOOD", obs)},
			},
			wantIndet: true,
		},
	}

	for _, tc := range parityCases {
		t.Run(tc.name, func(t *testing.T) {
			cloud := runCloudOnce(tc.expr, tc.inputs, tc.points)
			edgeRaised, edgeEvent := runEdgeOnce(t, tc.expr, tc.inputs, tc.points)
			if tc.wantIndet {
				if cloud.status != EvaluationIndeterminate || edgeRaised || edgeEvent {
					t.Fatalf("expected both indeterminate (no raise), got cloud=%+v edgeRaised=%v edgeEvent=%v", cloud, edgeRaised, edgeEvent)
				}
				return
			}
			cloudMatched := cloud.status == EvaluationMatched
			if cloud.status == EvaluationIndeterminate || cloudMatched != tc.wantMatch || edgeRaised != tc.wantMatch {
				t.Fatalf("parity mismatch: wantMatch=%v cloud=%+v edgeRaised=%v", tc.wantMatch, cloud, edgeRaised)
			}
		})
	}

	// Intentional divergence: suspect/stale LHS absorbed by Kleene true RHS in cloud,
	// whereas edge fails closed via upfront freshness/quality pre-gate.
	divPoints := []edgeTestInput{
		{key: "ch1/sup", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(-5), quality: edgecontrol.QualitySuspect, observedAt: now, cloudFact: NumberInput("ch1/sup", -5, "STALE", obs)},
		{key: "ch1/ret", dataType: edgecontrol.DataTypeDouble, val: edgecontrol.DoubleValue(15), quality: edgecontrol.QualityGood, observedAt: now, cloudFact: NumberInput("ch1/ret", 15, "GOOD", obs)},
	}
	divInputs := map[string]string{"a": "ch1/sup", "b": "ch1/ret"}
	divExpr := "a > 10 || b > 10"
	cloudDiv := runCloudOnce(divExpr, divInputs, divPoints)
	edgeRaisedDiv, edgeEventDiv := runEdgeOnce(t, divExpr, divInputs, divPoints)
	if cloudDiv.status != EvaluationMatched {
		t.Fatalf("expected cloud Kleene 3-valued logic to match, got %+v", cloudDiv)
	}
	if edgeRaisedDiv || edgeEventDiv {
		t.Fatalf("expected edge 2-valued pre-gate to fail closed without raising, got raised=%v event=%v", edgeRaisedDiv, edgeEventDiv)
	}
}

func splitKey(t *testing.T, key string) [2]string {
	t.Helper()
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return [2]string{key[:i], key[i+1:]}
		}
	}
	t.Fatalf("invalid key %q", key)
	return [2]string{}
}
