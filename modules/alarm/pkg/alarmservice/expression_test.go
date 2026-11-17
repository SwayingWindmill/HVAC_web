package alarmservice

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateExpressionCapsAndGrammar(t *testing.T) {
	t.Run("accepts valid complex expression", func(t *testing.T) {
		inputs := map[string]string{
			"sup":     "dev1/supply",
			"ret":     "dev1/return",
			"running": "dev1/run_state",
		}
		expr := `running == "RUNNING" && abs(ret - sup) >= 1.5 && min(sup, ret, 20.0) > -5.0 && !(max(sup, ret) > 50)`
		if err := ValidateExpression(expr, inputs); err != nil {
			t.Fatalf("valid expression rejected: %v", err)
		}
	})

	t.Run("rejects empty and over-length expressions", func(t *testing.T) {
		if err := ValidateExpression("   ", nil); err == nil {
			t.Fatal("expected empty expression error")
		}
		maxExpr := "a > 0" + strings.Repeat(" ", MaxExpressionLength-len("a > 0"))
		if err := ValidateExpression(maxExpr, map[string]string{"a": "d/p"}); err != nil {
			t.Fatalf("exact MaxExpressionLength rejected: %v", err)
		}
		tooLong := maxExpr + " "
		if err := ValidateExpression(tooLong, map[string]string{"a": "d/p"}); err == nil {
			t.Fatal("expected over-length expression error")
		}
	})

	t.Run("enforces MaxExpressionInputs", func(t *testing.T) {
		okInputs := make(map[string]string, MaxExpressionInputs)
		parts := make([]string, 0, MaxExpressionInputs)
		for i := 0; i < MaxExpressionInputs; i++ {
			alias := "v" + string(rune('a'+i))
			okInputs[alias] = "d/" + alias
			parts = append(parts, alias+" > 0")
		}
		if err := ValidateExpression(strings.Join(parts, " && "), okInputs); err != nil {
			t.Fatalf("16 inputs rejected: %v", err)
		}
		tooMany := make(map[string]string, MaxExpressionInputs+1)
		for k, v := range okInputs {
			tooMany[k] = v
		}
		tooMany["vq"] = "d/vq"
		parts = append(parts, "vq > 0")
		if err := ValidateExpression(strings.Join(parts, " && "), tooMany); err == nil {
			t.Fatal("expected >16 inputs rejection")
		}
	})

	t.Run("enforces MaxExpressionASTNodes (64 ok, 65 rejected)", func(t *testing.T) {
		// Balanced binary sum of 31 ones ("1") has 30 '+' + 31 '1' = 61 nodes, depth = 6!
		// "(<sum31>) > -a" => 61 + 1('>') + 2('-', 'a') = 64 nodes!
		var balanced func(n int) string
		balanced = func(n int) string {
			if n == 1 {
				return "1"
			}
			left := n / 2
			right := n - left
			return "(" + balanced(left) + " + " + balanced(right) + ")"
		}
		expr64 := balanced(31) + " > -a" // 61 + 1('>') + 2('-', 'a') = 64 nodes
		if err := ValidateExpression(expr64, map[string]string{"a": "d/a"}); err != nil {
			t.Fatalf("64-node expression rejected: %v", err)
		}
		expr65 := balanced(32) + " > a" // (32 + 31 = 63) + 1('>') + 1('a') = 65 nodes
		if err := ValidateExpression(expr65, map[string]string{"a": "d/a"}); err == nil {
			t.Fatal("65-node expression unexpectedly accepted")
		}
	})

	t.Run("enforces MaxExpressionDepth (16 ok, 17 rejected)", func(t *testing.T) {
		// "a > 0" has depth 2; adding 14 '!' gives depth 16; adding 15 '!' gives depth 17.
		expr16 := strings.Repeat("!", 14) + "(a > 0)"
		if err := ValidateExpression(expr16, map[string]string{"a": "d/a"}); err != nil {
			t.Fatalf("depth-16 expression rejected: %v", err)
		}
		expr17 := strings.Repeat("!", 15) + "(a > 0)"
		if err := ValidateExpression(expr17, map[string]string{"a": "d/a"}); err == nil {
			t.Fatal("depth-17 expression unexpectedly accepted")
		}

		// Paren nesting cap 16 ok, 17 rejected.
		paren16 := strings.Repeat("(", 16) + "a > 0" + strings.Repeat(")", 16)
		if err := ValidateExpression(paren16, map[string]string{"a": "d/a"}); err != nil {
			t.Fatalf("16 nested parens rejected: %v", err)
		}
		paren17 := strings.Repeat("(", 17) + "a > 0" + strings.Repeat(")", 17)
		if err := ValidateExpression(paren17, map[string]string{"a": "d/a"}); err == nil {
			t.Fatal("17 nested parens unexpectedly accepted")
		}

		// Right-nested parenthesized arithmetic depth 16 ok, 17 rejected.
		nestedArith := func(ops int) string {
			s := "a"
			for i := 0; i < ops; i++ {
				s = "1 + (" + s + ")"
			}
			return s + " > 0"
		}
		if err := ValidateExpression(nestedArith(14), map[string]string{"a": "d/a"}); err != nil {
			t.Fatalf("depth-16 parenthesized arithmetic rejected: %v", err)
		}
		if err := ValidateExpression(nestedArith(15), map[string]string{"a": "d/a"}); err == nil {
			t.Fatal("depth-17 parenthesized arithmetic unexpectedly accepted")
		}
	})

	t.Run("enforces alias regex, reserved keywords, and 1:1 input binding", func(t *testing.T) {
		badCases := []struct {
			name   string
			expr   string
			inputs map[string]string
		}{
			{"undeclared ident", "a > b", map[string]string{"a": "d/a"}},
			{"unused input", "a > 0", map[string]string{"a": "d/a", "b": "d/b"}},
			{"reserved true", "true && a > 0", map[string]string{"a": "d/a", "true": "d/t"}},
			{"reserved false", "a > 0", map[string]string{"a": "d/a", "false": "d/f"}},
			{"reserved abs", "abs > 0", map[string]string{"abs": "d/abs"}},
			{"reserved min", "min > 0", map[string]string{"min": "d/min"}},
			{"reserved max", "max > 0", map[string]string{"max": "d/max"}},
			{"alias too long", strings.Repeat("a", 33) + " > 0", map[string]string{strings.Repeat("a", 33): "d/a"}},
			{"alias starts with digit", "1a > 0", map[string]string{"1a": "d/a"}},
			{"abs wrong arity", "abs(a, 1) > 0", map[string]string{"a": "d/a"}},
			{"min too few args", "min(a) > 0", map[string]string{"a": "d/a"}},
			{"max too many args", "max(a,1,2,3,4,5,6,7,8) > 0", map[string]string{"a": "d/a"}},
			{"unknown function", "sqrt(a) > 0", map[string]string{"a": "d/a"}},
			{"single equals", "a = 1", map[string]string{"a": "d/a"}},
			{"single ampersand", "a > 0 & true", map[string]string{"a": "d/a"}},
			{"unterminated string", "a == 'RUNNING", map[string]string{"a": "d/a"}},
			{"trailing dot number", "a > 1.", map[string]string{"a": "d/a"}},
			{"bad exponent", "a > 1e", map[string]string{"a": "d/a"}},
			{"trailing junk", "a > 1 2", map[string]string{"a": "d/a"}},
		}
		for _, tc := range badCases {
			if err := ValidateExpression(tc.expr, tc.inputs); err == nil {
				t.Errorf("%s: expected error for %q", tc.name, tc.expr)
			}
		}
	})
}

func TestValidateExpressionStaticTypeRejection(t *testing.T) {
	badCases := []struct {
		name   string
		expr   string
		inputs map[string]string
	}{
		{"numeric root", "a + b", map[string]string{"a": "d/a", "b": "d/b"}},
		{"literal numeric root", "42", nil},
		{"string root", "'RUNNING'", nil},
		{"abs root", "abs(a)", map[string]string{"a": "d/a"}},
		{"bare alias root", "a", map[string]string{"a": "d/a"}},
		{"bool in addition", "true + a > 1", map[string]string{"a": "d/a"}},
		{"string in subtraction", "a - 'x' > 0", map[string]string{"a": "d/a"}},
		{"unary minus on bool", "-(a > 1) < 0", map[string]string{"a": "d/a"}},
		{"string in ordered compare", "'a' < 'b'", nil},
		{"bool in ordered compare", "(a == 1) > 0", map[string]string{"a": "d/a"}},
		{"bool in abs", "abs(a > 0) > 1", map[string]string{"a": "d/a"}},
		{"string in min", "min(a, 'x') > 0", map[string]string{"a": "d/a"}},
		{"number in not", "!(a + 1)", map[string]string{"a": "d/a"}},
		{"number in and", "(a + 1) && true", map[string]string{"a": "d/a"}},
		{"string in or", "'RUNNING' || (a > 0)", map[string]string{"a": "d/a"}},
		{"int eq string", "1 == '1'", nil},
		{"bool eq number", "(a > 0) == 1", map[string]string{"a": "d/a"}},
		{"string eq bool", "'true' == true", nil},
	}
	for _, tc := range badCases {
		if err := ValidateExpression(tc.expr, tc.inputs); err == nil {
			t.Errorf("%s: invalid typed expression %q was accepted", tc.name, tc.expr)
		}
	}
}

func TestEvaluateExpressionKleeneDivisionByZeroAndQuality(t *testing.T) {
	now := mustTime(t, "2026-11-17T10:00:00Z")
	basePolicy := func(expr string, inputs map[string]string) AlarmPolicyRevision {
		p := validAlarmPolicy()
		p.QualityPolicy = EvaluationQualityValidOnly
		p.FreshnessSeconds = 300
		p.Raise = Condition{Kind: ConditionExpression, Expression: expr, ExprInputs: inputs}
		p.Clear = Condition{Kind: ConditionExpression, Expression: "false", ExprInputs: nil}
		sealAlarmPolicy(&p)
		return p
	}

	t.Run("Kleene OR true||INDETERMINATE and INDETERMINATE||true both match", func(t *testing.T) {
		p1 := basePolicy("a > 10 || b > 10", map[string]string{"a": "d/a", "b": "d/b"})
		d1, err := EvaluatePolicy(p1, evaluationSnapshot("k-1", now.Format(time.RFC3339),
			NumberInput("d/a", 15, "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d1.Status != EvaluationMatched || d1.Effect != EvaluationEffectPublish {
			t.Fatalf("true || INDETERMINATE did not match: %#v err=%v", d1, err)
		}

		p2 := basePolicy("b > 10 || a > 10", map[string]string{"a": "d/a", "b": "d/b"})
		d2, err := EvaluatePolicy(p2, evaluationSnapshot("k-2", now.Format(time.RFC3339),
			NumberInput("d/a", 15, "GOOD", now.Format(time.RFC3339)),
			NumberInput("d/b", 5, "STALE", now.Add(-10*time.Minute).Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d2.Status != EvaluationMatched || d2.Effect != EvaluationEffectPublish {
			t.Fatalf("INDETERMINATE || true did not match: %#v err=%v", d2, err)
		}

		d3, err := EvaluatePolicy(p1, evaluationSnapshot("k-3", now.Format(time.RFC3339),
			NumberInput("d/a", 5, "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d3.Status != EvaluationIndeterminate || d3.State.QualityBlocker != "MISSING_INPUT:d/b" {
			t.Fatalf("false || INDETERMINATE did not propagate blocker: %#v err=%v", d3, err)
		}
	})

	t.Run("Kleene AND false&&INDETERMINATE and INDETERMINATE&&false both do not match", func(t *testing.T) {
		p1 := basePolicy("a > 10 && b > 10", map[string]string{"a": "d/a", "b": "d/b"})
		d1, err := EvaluatePolicy(p1, evaluationSnapshot("a-1", now.Format(time.RFC3339),
			NumberInput("d/a", 5, "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d1.Status != EvaluationNotMatched || d1.Effect != EvaluationEffectNone {
			t.Fatalf("false && INDETERMINATE did not resolve NOT_MATCHED: %#v err=%v", d1, err)
		}

		p2 := basePolicy("b > 10 && a > 10", map[string]string{"a": "d/a", "b": "d/b"})
		d2, err := EvaluatePolicy(p2, evaluationSnapshot("a-2", now.Format(time.RFC3339),
			NumberInput("d/a", 5, "GOOD", now.Format(time.RFC3339)),
			NumberInput("d/b", 12, "BAD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d2.Status != EvaluationNotMatched || d2.Effect != EvaluationEffectNone {
			t.Fatalf("INDETERMINATE && false did not resolve NOT_MATCHED: %#v err=%v", d2, err)
		}

		d3, err := EvaluatePolicy(p2, evaluationSnapshot("a-3", now.Format(time.RFC3339),
			NumberInput("d/a", 15, "GOOD", now.Format(time.RFC3339)),
			NumberInput("d/b", 12, "BAD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d3.Status != EvaluationIndeterminate || d3.State.QualityBlocker != "UNTRUSTED_QUALITY:d/b:BAD" {
			t.Fatalf("INDETERMINATE && true did not propagate quality blocker: %#v err=%v", d3, err)
		}
	})

	t.Run("division by zero returns INVALID_INPUT:DIV_BY_ZERO", func(t *testing.T) {
		p := basePolicy("a / b > 2", map[string]string{"a": "d/a", "b": "d/b"})
		dZero, err := EvaluatePolicy(p, evaluationSnapshot("div-0", now.Format(time.RFC3339),
			NumberInput("d/a", 10, "GOOD", now.Format(time.RFC3339)),
			NumberInput("d/b", 0, "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || dZero.Status != EvaluationIndeterminate || dZero.State.QualityBlocker != "INVALID_INPUT:DIV_BY_ZERO" {
			t.Fatalf("div by zero got %#v err=%v", dZero, err)
		}

		dEps, err := EvaluatePolicy(p, evaluationSnapshot("div-eps", now.Format(time.RFC3339),
			NumberInput("d/a", 10, "GOOD", now.Format(time.RFC3339)),
			NumberInput("d/b", 5e-13, "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || dEps.Status != EvaluationIndeterminate || dEps.State.QualityBlocker != "INVALID_INPUT:DIV_BY_ZERO" {
			t.Fatalf("near-zero divisor got %#v err=%v", dEps, err)
		}

		dOk, err := EvaluatePolicy(p, evaluationSnapshot("div-ok", now.Format(time.RFC3339),
			NumberInput("d/a", 10, "GOOD", now.Format(time.RFC3339)),
			NumberInput("d/b", 2, "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || dOk.Status != EvaluationMatched {
			t.Fatalf("valid division got %#v err=%v", dOk, err)
		}
	})

	t.Run("runtime non-numeric or type-mismatch input returns INVALID_INPUT blocker", func(t *testing.T) {
		p := basePolicy("a > 10", map[string]string{"a": "d/a"})
		d, err := EvaluatePolicy(p, evaluationSnapshot("rt-str", now.Format(time.RFC3339),
			StringInput("d/a", "RUNNING", "GOOD", now.Format(time.RFC3339)),
		), AlarmEvaluationState{}, now)
		if err != nil || d.Status != EvaluationIndeterminate || d.State.QualityBlocker != "INVALID_INPUT:d/a" {
			t.Fatalf("runtime type mismatch got %#v err=%v", d, err)
		}
	})
}

func TestStatefulSlopeAndHysteresisExpressionSequences(t *testing.T) {
	t0 := mustTime(t, "2026-11-17T08:00:00Z")
	deltaRise := 3.0
	deltaDrop := -2.5

	t.Run("slope condition validates bounds", func(t *testing.T) {
		bad := []Condition{
			{Kind: ConditionSlope, Input: "", Operator: CompareGT, Delta: &deltaRise, WindowSeconds: 300},
			{Kind: ConditionSlope, Input: "d/t", Operator: CompareEQ, Delta: &deltaRise, WindowSeconds: 300},
			{Kind: ConditionSlope, Input: "d/t", Operator: CompareGT, Delta: nil, WindowSeconds: 300},
			{Kind: ConditionSlope, Input: "d/t", Operator: CompareGT, Delta: &deltaRise, WindowSeconds: 0},
			{Kind: ConditionSlope, Input: "d/t", Operator: CompareGT, Delta: &deltaRise, WindowSeconds: 86401},
		}
		nanVal := math.NaN()
		bad = append(bad, Condition{Kind: ConditionSlope, Input: "d/t", Operator: CompareGT, Delta: &nanVal, WindowSeconds: 60})
		for i, c := range bad {
			if err := c.Validate(); err == nil {
				t.Errorf("invalid slope case %d unexpectedly passed", i)
			}
		}
	})

	t.Run("slope window sliding, duplicate protection, pruning, cap=32, and hysteresis clear", func(t *testing.T) {
		policy := validAlarmPolicy()
		policy.AlarmType = "CHW_RAPID_RISE"
		policy.SourceReference = "alarm-policy:chw-rapid-rise"
		policy.QualityPolicy = EvaluationQualityValidOnly
		policy.FreshnessSeconds = 900
		policy.TriggerMode = TriggerDuration
		policy.DurationSeconds = 60
		policy.Raise = Condition{Kind: ConditionSlope, Input: "d/t", Operator: CompareGTE, Delta: &deltaRise, WindowSeconds: 300}
		policy.Clear = Condition{
			Kind:       ConditionExpression,
			Expression: `run_state != "RUNNING" || t < 12.0`,
			ExprInputs: map[string]string{"run_state": "d/run_state", "t": "d/t"},
		}
		sealAlarmPolicy(&policy)

		snap := func(rev string, at, observed time.Time, temp float64, quality string, running string) EvaluationSnapshot {
			return evaluationSnapshot(rev, at.Format(time.RFC3339Nano),
				NumberInput("d/t", temp, quality, observed.Format(time.RFC3339Nano)),
				StringInput("d/run_state", running, "GOOD", at.Format(time.RFC3339Nano)),
			)
		}

		// Step 1 (t0): first sample 10.0 -> <2 samples in window -> NOT_MATCHED, SampleWindow has 1 entry.
		d1, err := EvaluatePolicy(policy, snap("r1", t0, t0, 10.0, "GOOD", "RUNNING"), AlarmEvaluationState{}, t0)
		if err != nil || d1.Status != EvaluationNotMatched || len(d1.State.SampleWindow) != 1 {
			t.Fatalf("step 1 got %#v err=%v", d1, err)
		}

		// Step 2 (t0+30s): another device updates revision, d/t keeps same ObservedAt=t0 -> not duplicated!
		d2, err := EvaluatePolicy(policy, snap("r2", t0.Add(30*time.Second), t0, 10.0, "GOOD", "RUNNING"), d1.State, t0.Add(30*time.Second))
		if err != nil || d2.Status != EvaluationNotMatched || len(d2.State.SampleWindow) != 1 {
			t.Fatalf("step 2 duplicated same ObservedAt sample: %#v err=%v", d2, err)
		}

		// Step 3 (t0+1m): untrusted reading BAD -> not appended, INDETERMINATE.
		d3, err := EvaluatePolicy(policy, snap("r3", t0.Add(time.Minute), t0.Add(time.Minute), 99.0, "BAD", "RUNNING"), d2.State, t0.Add(time.Minute))
		if err != nil || d3.Status != EvaluationIndeterminate || len(d3.State.SampleWindow) != 1 {
			t.Fatalf("step 3 untrusted sample polluted window: %#v err=%v", d3, err)
		}

		// Step 4 (t0+2m): trusted 13.5 (+3.5 >= 3.0) -> MATCHED candidate starts (Duration 60s).
		t2 := t0.Add(2 * time.Minute)
		d4, err := EvaluatePolicy(policy, snap("r4", t2, t2, 13.5, "GOOD", "RUNNING"), d3.State, t2)
		if err != nil || d4.Effect != EvaluationEffectNone || d4.State.CandidateSince == nil || len(d4.State.SampleWindow) != 2 {
			t.Fatalf("step 4 did not start candidate: %#v err=%v", d4, err)
		}

		// Step 5 (t0+3m): trusted 14.0 (+4.0 >= 3.0) -> Duration 60s elapsed -> PUBLISH.
		t3 := t0.Add(3 * time.Minute)
		d5, err := EvaluatePolicy(policy, snap("r5", t3, t3, 14.0, "GOOD", "RUNNING"), d4.State, t3)
		if err != nil || d5.Effect != EvaluationEffectPublish || d5.Status != EvaluationMatched || len(d5.State.SampleWindow) != 3 {
			t.Fatalf("step 5 did not publish: %#v err=%v", d5, err)
		}

		// Simulate JSON persistence round-trip of AlarmEvaluationState.
		rawState, err := json.Marshal(d5.State)
		if err != nil {
			t.Fatal(err)
		}
		var persisted AlarmEvaluationState
		if err := json.Unmarshal(rawState, &persisted); err != nil {
			t.Fatal(err)
		}
		persisted.ActiveAlarmID = testAlarmID
		persisted.ActiveIncidentCorrelationID = testIncidentCorrID

		// Step 6 (t0+4m): temp drops slightly to 12.5 (not < 12.0), RUNNING -> stays active.
		t4 := t0.Add(4 * time.Minute)
		d6, err := EvaluatePolicy(policy, snap("r6", t4, t4, 12.5, "GOOD", "RUNNING"), persisted, t4)
		if err != nil || d6.Effect != EvaluationEffectNone || d6.Status != EvaluationMatched {
			t.Fatalf("step 6 cleared prematurely: %#v err=%v", d6, err)
		}

		// Step 7 (t0+5m): untrusted temp while active -> INDETERMINATE, must NOT clear active incident.
		t5 := t0.Add(5 * time.Minute)
		d7, err := EvaluatePolicy(policy, snap("r7", t5, t5, 5.0, "STALE", "RUNNING"), d6.State, t5)
		if err != nil || d7.Effect != EvaluationEffectNone || d7.Status != EvaluationIndeterminate {
			t.Fatalf("step 7 untrusted input cleared active incident: %#v err=%v", d7, err)
		}

		// Step 8 (t0+6m): chiller STOPPED while temp is stale -> Kleene true || INDETERMINATE -> CLEAR!
		t6 := t0.Add(6 * time.Minute)
		d8, err := EvaluatePolicy(policy, snap("r8", t6, t6, 5.0, "STALE", "STOPPED"), d7.State, t6)
		if err != nil || d8.Effect != EvaluationEffectClear || d8.Status != EvaluationNotMatched {
			t.Fatalf("step 8 Kleene expression clear failed: %#v err=%v", d8, err)
		}

		// Check pruning: at t0+6m with window=300s (cutoff t0+1m), t0 sample was pruned!
		for _, sample := range d8.State.SampleWindow {
			ts, _ := time.Parse(time.RFC3339Nano, sample.ObservedAt)
			if ts.Equal(t0) {
				t.Fatalf("expired sample at t0 was not pruned: %#v", d8.State.SampleWindow)
			}
		}
	})

	t.Run("negative delta slope and MaxSlopeSamplesPerRule=32 cap", func(t *testing.T) {
		policy := validAlarmPolicy()
		policy.QualityPolicy = EvaluationQualityValidOnly
		policy.FreshnessSeconds = 1200
		policy.Raise = Condition{Kind: ConditionSlope, Input: "d/t", Operator: CompareLTE, Delta: &deltaDrop, WindowSeconds: 600}
		policy.Clear = Condition{Kind: ConditionCompare, Input: "d/t", Operator: CompareGTE, Value: NumberValue(20)}
		sealAlarmPolicy(&policy)

		state := AlarmEvaluationState{}
		// Feed 40 samples spaced by 10s (all within 600s window); sample 0..39.
		for i := 0; i < 40; i++ {
			at := t0.Add(time.Duration(i) * 10 * time.Second)
			val := 20.0 - float64(i)*0.05 // gentle drop < 2.5
			decision, err := EvaluatePolicy(policy, evaluationSnapshot(
				"cap-"+string(rune('a'+i/26))+string(rune('a'+i%26)),
				at.Format(time.RFC3339Nano),
				NumberInput("d/t", val, "GOOD", at.Format(time.RFC3339Nano)),
			), state, at)
			if err != nil {
				t.Fatal(err)
			}
			state = decision.State
			if len(state.SampleWindow) > MaxSlopeSamplesPerRule {
				t.Fatalf("sample window exceeded cap %d: got %d", MaxSlopeSamplesPerRule, len(state.SampleWindow))
			}
		}
		if len(state.SampleWindow) != MaxSlopeSamplesPerRule {
			t.Fatalf("expected %d retained samples, got %d", MaxSlopeSamplesPerRule, len(state.SampleWindow))
		}
		// Oldest retained sample should be index 8 (t0 + 80s).
		wantFirst := t0.Add(80 * time.Second).Format(time.RFC3339Nano)
		if state.SampleWindow[0].ObservedAt != wantFirst {
			t.Fatalf("oldest retained sample = %s, want %s", state.SampleWindow[0].ObservedAt, wantFirst)
		}

		// Sharp drop triggers negative-delta slope.
		dropAt := t0.Add(400 * time.Second)
		dropped, err := EvaluatePolicy(policy, evaluationSnapshot("drop", dropAt.Format(time.RFC3339Nano),
			NumberInput("d/t", 16.0, "GOOD", dropAt.Format(time.RFC3339Nano)),
		), state, dropAt)
		if err != nil || dropped.Effect != EvaluationEffectPublish {
			t.Fatalf("negative slope drop did not publish: %#v err=%v", dropped, err)
		}
	})
}

func TestLegacyPolicyDigestCompatibilityAndMetadataValidation(t *testing.T) {
	// Exact canonical JSON & SHA-256 digest from modules/alarm/testdata/postgres/010_seed.sql.
	const seedDigest = "5915c63b4f29523c69a0038c24d66adf97a23e46b27ed3c54690df3927ea29d4"
	const seedJSON = `{"schemaVersion":1,"policyId":"01910000-5000-7000-8000-000000000001","policyRevisionId":"01910000-5000-7000-8000-000000000002","revision":1,"digest":"5915c63b4f29523c69a0038c24d66adf97a23e46b27ed3c54690df3927ea29d4","alarmType":"S14_DURATION_SUPPLY_TEMPERATURE","sourceType":"SITE_RULE","sourceReference":"alarm-policy:postgres-s14-duration","title":"S14 duration supply temperature","summary":"S14 durable evaluator integration fixture.","severity":"MAJOR","qualityPolicy":"VALID_ONLY","freshnessSeconds":900,"triggerMode":"DURATION","durationSeconds":5,"raise":{"kind":"COMPARE","input":"supplyTemp","operator":"GT","value":{"type":"NUMBER","number":30}},"clear":{"kind":"COMPARE","input":"supplyTemp","operator":"LTE","value":{"type":"NUMBER","number":28}}}`

	var policy AlarmPolicyRevision
	if err := json.Unmarshal([]byte(seedJSON), &policy); err != nil {
		t.Fatal(err)
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("legacy seed policy failed validation: %v", err)
	}
	digest, err := AlarmPolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	if digest != seedDigest {
		t.Fatalf("legacy policy digest drifted: got %s, want %s", digest, seedDigest)
	}
	remarshaled, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if string(remarshaled) != seedJSON {
		t.Fatalf("legacy policy JSON drifted:\n got %s\nwant %s", remarshaled, seedJSON)
	}
	if policy.EffectiveRuleKind() != RuleKindThreshold {
		t.Fatalf("legacy EffectiveRuleKind = %s, want THRESHOLD", policy.EffectiveRuleKind())
	}

	// samePolicyContent preserves equality for legacy and detects metadata diffs.
	withMeta := policy
	withMeta.TemplateKey = "tpl-supply"
	withMeta.OverriddenFields = []string{"severity"}
	same, err := samePolicyContent(policy, policy)
	if err != nil || !same {
		t.Fatalf("samePolicyContent(legacy, legacy) = %v, %v", same, err)
	}
	same, err = samePolicyContent(policy, withMeta)
	if err != nil || same {
		t.Fatalf("samePolicyContent did not detect metadata change: same=%v err=%v", same, err)
	}

	// EffectiveRuleKind inference and explicit precedence.
	delta := 2.0
	slopePolicy := validAlarmPolicy()
	slopePolicy.Raise = Condition{Kind: ConditionSlope, Input: "d/t", Operator: CompareGT, Delta: &delta, WindowSeconds: 60}
	if slopePolicy.EffectiveRuleKind() != RuleKindSlope {
		t.Fatalf("slope EffectiveRuleKind = %s, want SLOPE", slopePolicy.EffectiveRuleKind())
	}
	mixedPolicy := slopePolicy
	mixedPolicy.Clear = Condition{Kind: ConditionExpression, Expression: "t < 5", ExprInputs: map[string]string{"t": "d/t2"}}
	if mixedPolicy.EffectiveRuleKind() != RuleKindExpression {
		t.Fatalf("mixed EffectiveRuleKind = %s, want EXPRESSION", mixedPolicy.EffectiveRuleKind())
	}
	mixedPolicy.RuleKind = RuleKindThreshold
	if mixedPolicy.EffectiveRuleKind() != RuleKindThreshold {
		t.Fatalf("explicit RuleKind did not take precedence: %s", mixedPolicy.EffectiveRuleKind())
	}

	// ReferencedInputs & readsDevice for SLOPE and EXPRESSION.
	if got := mixedPolicy.ReferencedInputs(); !reflect.DeepEqual(got, []string{"d/t", "d/t2"}) {
		t.Fatalf("ReferencedInputs = %v, want [d/t d/t2]", got)
	}
	devID := "01910000-4000-7000-8000-000000000001"
	exprOnly := validAlarmPolicy()
	exprOnly.Raise = Condition{Kind: ConditionExpression, Expression: "x > 1", ExprInputs: map[string]string{"x": devID + "/temp"}}
	exprOnly.Clear = Condition{Kind: ConditionExpression, Expression: "x < 0", ExprInputs: map[string]string{"x": devID + "/temp"}}
	if !readsDevice(exprOnly, devID) {
		t.Fatal("readsDevice did not recognize EXPRESSION input")
	}
	if readsDevice(exprOnly, "01910000-4000-7000-8000-000000000099") {
		t.Fatal("readsDevice matched unrelated device")
	}

	// Metadata validation rejections.
	depID := "01910000-7a02-7000-8000-000000000001"
	validMeta := validAlarmPolicy()
	validMeta.RuleKind = RuleKindThreshold
	validMeta.TemplateKey = "base-temp"
	validMeta.OverriddenFields = []string{"severity", "durationSeconds"}
	validMeta.DependsOnRuleIDs = []string{depID}
	sealAlarmPolicy(&validMeta)
	if err := validMeta.Validate(); err != nil {
		t.Fatalf("valid metadata rejected: %v", err)
	}

	checkReject := func(name string, mutate func(*AlarmPolicyRevision)) {
		t.Helper()
		bad := validAlarmPolicy()
		mutate(&bad)
		sealAlarmPolicy(&bad)
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: invalid metadata accepted", name)
		}
	}
	checkReject("bad ruleKind", func(p *AlarmPolicyRevision) { p.RuleKind = "MAGIC" })
	checkReject("whitespace templateKey", func(p *AlarmPolicyRevision) { p.TemplateKey = "   " })
	checkReject("long templateKey", func(p *AlarmPolicyRevision) { p.TemplateKey = strings.Repeat("k", 129) })
	checkReject("too many overriddenFields", func(p *AlarmPolicyRevision) {
		p.OverriddenFields = make([]string, 33)
		for i := range p.OverriddenFields {
			p.OverriddenFields[i] = "severity"
		}
	})
	checkReject("empty overriddenField", func(p *AlarmPolicyRevision) { p.OverriddenFields = []string{""} })
	checkReject("invalid dependsOn UUID", func(p *AlarmPolicyRevision) { p.DependsOnRuleIDs = []string{"not-uuid"} })
	checkReject("self dependsOn PolicyID", func(p *AlarmPolicyRevision) { p.DependsOnRuleIDs = []string{p.PolicyID} })
	checkReject("duplicate dependsOn", func(p *AlarmPolicyRevision) { p.DependsOnRuleIDs = []string{depID, depID} })
	checkReject("too many dependsOn", func(p *AlarmPolicyRevision) {
		p.DependsOnRuleIDs = make([]string, 17)
		for i := range p.DependsOnRuleIDs {
			p.DependsOnRuleIDs[i] = "01910000-7a02-7000-8000-0000000000" + string(rune('1'+i/10)) + string(rune('0'+i%10))
		}
	})
}

func TestSampleWindowJSONEncodingAndValidation(t *testing.T) {
	emptyJSON, err := encodeSampleWindow(nil)
	if err != nil || string(emptyJSON) != "[]" {
		t.Fatalf("encodeSampleWindow(nil) = %q, %v; want []", emptyJSON, err)
	}
	decodedEmpty, err := decodeSampleWindow(emptyJSON)
	if err != nil || decodedEmpty != nil {
		t.Fatalf("decodeSampleWindow([]) = %#v, %v", decodedEmpty, err)
	}

	samples := []TimedSample{{Input: "d/t", ObservedAt: "2026-11-17T10:00:00Z", Value: 21.5}}
	encoded, err := encodeSampleWindow(samples)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSampleWindow(encoded)
	if err != nil || !reflect.DeepEqual(decoded, samples) {
		t.Fatalf("sample window round-trip = %#v, %v", decoded, err)
	}

	badPayloads := []string{
		`[{"input":"d/t","observedAt":"2026-11-17T10:00:00Z","value":1,"extra":2}]`,
		`[{"input":"","observedAt":"2026-11-17T10:00:00Z","value":1}]`,
		`[{"input":"d/t","observedAt":"not-a-time","value":1}]`,
		`{"not":"array"}`,
	}
	for _, raw := range badPayloads {
		if _, err := decodeSampleWindow([]byte(raw)); err == nil {
			t.Errorf("invalid sample window payload accepted: %s", raw)
		}
	}
	overflow := make([]TimedSample, MaxSlopeSamplesPerRule+1)
	for i := range overflow {
		overflow[i] = TimedSample{Input: "d/t", ObservedAt: "2026-11-17T10:00:00Z", Value: float64(i)}
	}
	overflowRaw, _ := json.Marshal(overflow)
	if _, err := decodeSampleWindow(overflowRaw); err == nil {
		t.Fatal("overflow sample window accepted")
	}
}
