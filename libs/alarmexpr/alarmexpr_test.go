package alarmexpr

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestParseAndValidateBoundsAndGrammar(t *testing.T) {
	if err := Validate("   ", nil); err == nil {
		t.Fatal("expected blank expression to fail")
	}
	maxExpr := "a > 0" + strings.Repeat(" ", MaxLength-len("a > 0"))
	if err := Validate(maxExpr, map[string]string{"a": "d/a"}); err != nil {
		t.Fatalf("exact MaxLength rejected: %v", err)
	}
	if err := Validate(maxExpr+" ", map[string]string{"a": "d/a"}); err == nil {
		t.Fatal("expected length > MaxLength to fail")
	}

	okInputs := make(map[string]string, MaxInputs)
	parts := make([]string, 0, MaxInputs)
	for i := 0; i < MaxInputs; i++ {
		alias := fmt.Sprintf("x%d", i)
		okInputs[alias] = "d/" + alias
		parts = append(parts, alias+" > 0")
	}
	if err := Validate(strings.Join(parts, " && "), okInputs); err != nil {
		t.Fatalf("exact MaxInputs rejected: %v", err)
	}
	tooMany := make(map[string]string, MaxInputs+1)
	for i := 0; i <= MaxInputs; i++ {
		tooMany[fmt.Sprintf("x%d", i)] = fmt.Sprintf("d/x%d", i)
	}
	if err := Validate(strings.Join(parts, " && ")+" && x16 > 0", tooMany); err == nil {
		t.Fatal("expected MaxInputs+1 to fail")
	}

	// 64 AST nodes exact bound: !(a > 0 + 1*28) [60 nodes] && a > 1 [4 nodes] = 64.
	expr64 := "!(a > 0" + strings.Repeat(" + 1", 28) + ") && a > 1"
	if err := Validate(expr64, map[string]string{"a": "d/a"}); err != nil {
		t.Fatalf("64-node expression rejected: %v", err)
	}
	expr65 := expr64 + " && true"
	if err := Validate(expr65, map[string]string{"a": "d/a"}); err == nil {
		t.Fatal("expected 66-node expression (> MaxASTNodes) to fail")
	}

	okDepth := strings.Repeat("(", MaxDepth-3) + "a > 0" + strings.Repeat(")", MaxDepth-3)
	if err := Validate(okDepth, map[string]string{"a": "d/a"}); err != nil {
		t.Fatalf("valid depth rejected: %v", err)
	}
	tooDeep := strings.Repeat("(", MaxDepth+1) + "a > 0" + strings.Repeat(")", MaxDepth+1)
	if err := Validate(tooDeep, map[string]string{"a": "d/a"}); err == nil {
		t.Fatal("expected depth > MaxDepth to fail")
	}

	invalidCases := []struct {
		name   string
		expr   string
		inputs map[string]string
	}{
		{"reserved alias abs", "a > 0", map[string]string{"abs": "d/abs", "a": "d/a"}},
		{"undeclared ident", "a + b > 0", map[string]string{"a": "d/a"}},
		{"unused input", "a > 0", map[string]string{"a": "d/a", "b": "d/b"}},
		{"unknown func", "sin(a) > 0", map[string]string{"a": "d/a"}},
		{"abs arity", "abs(a, 1) > 0", map[string]string{"a": "d/a"}},
		{"min arity", "min(a) > 0", map[string]string{"a": "d/a"}},
		{"max arity", "max(a, 1, 2, 3, 4, 5, 6, 7, 8) > 0", map[string]string{"a": "d/a"}},
		{"unterminated string", "'abc == 'abc'", nil},
		{"bad escape", "'ab\\q' == 'x'", nil},
		{"bad number", "1. > 0", nil},
		{"bad exponent", "1e+ > 0", nil},
		{"overflow number literal", "1e500 > 0", nil},
		{"long ident", strings.Repeat("a", 33) + " > 0", nil},
		{"unexpected char", "a @ 1 > 0", map[string]string{"a": "d/a"}},
		{"non-boolean root", "a + 1", map[string]string{"a": "d/a"}},
		{"string ordered cmp", "'a' > 'b'", nil},
		{"not numeric", "!1", nil},
		{"negate bool", "-true > 0", nil},
		{"arith bool", "true + 1 > 0", nil},
		{"eq incompatible", "'a' == 1", nil},
		{"logic number", "1 && true", nil},
	}
	for _, tc := range invalidCases {
		if err := Validate(tc.expr, tc.inputs); err == nil {
			t.Errorf("%s: expected error for %q, got nil", tc.name, tc.expr)
		}
	}

	escapeCases := []struct {
		expr string
		want bool
	}{
		{"'a\\n\\tb\\r\\'\\\"\\\\' == \"a\\n\\tb\\r'\\\"\\\\\"", true},
		{"1.5e-2 + .5 > 0.5", true},
		{"abs(-3.5) == max(1.0, min(5.0, 3.5))", true},
		{"!(false == true) && (1 != 2)", true},
	}
	for _, tc := range escapeCases {
		compiled, err := ParseAndValidate(tc.expr, nil)
		if err != nil {
			t.Fatalf("ParseAndValidate(%q) error = %v", tc.expr, err)
		}
		res := compiled.Evaluate(EvalModeTwoValued, nil, nil)
		if res.Indeterminate || res.Matched != tc.want {
			t.Fatalf("Evaluate(%q) = %+v, want Matched=%v", tc.expr, res, tc.want)
		}
	}
}

func TestEvaluateModesGuardsAndSemanticDivergence(t *testing.T) {
	var nilExpr *Expression
	if res := nilExpr.Evaluate(EvalModeThreeValued, nil, nil); !res.Indeterminate || res.Blocker != "INVALID_INPUT:EXPRESSION" {
		t.Fatalf("nil expr = %+v", res)
	}

	inputs := map[string]string{"a": "d/a", "b": "d/b"}
	compiled, err := ParseAndValidate("a > 10 || b", inputs)
	if err != nil {
		t.Fatalf("ParseAndValidate error = %v", err)
	}
	if res := compiled.Evaluate(EvalMode(99), inputs, nil); !res.Indeterminate || res.Blocker != "INVALID_INPUT:EVAL_MODE" {
		t.Fatalf("invalid mode = %+v", res)
	}
	if res := compiled.Evaluate(EvalModeThreeValued, inputs, nil); !res.Indeterminate || res.Blocker != "INVALID_INPUT:RESOLVER" {
		t.Fatalf("nil resolver = %+v", res)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	tEarly := now.Add(5 * time.Second)
	tLate := now.Add(20 * time.Second)

	// 1. Untrusted LHS absorbed in EvalModeThreeValued, blocked upfront in EvalModeTwoValued.
	untrustedCalls := 0
	untrustedResolver := func(alias, inputKey string) InputResolution {
		untrustedCalls++
		if alias == "a" {
			return InputResolution{Trusted: false, Blocker: "MISSING_INPUT:d/a", NextDue: &tEarly}
		}
		return InputResolution{Trusted: true, Value: BooleanValue(true), NextDue: &tLate}
	}
	three := compiled.Evaluate(EvalModeThreeValued, inputs, untrustedResolver)
	if three.Indeterminate || !three.Matched || three.NextDue == nil || !three.NextDue.Equal(tLate) {
		t.Fatalf("3-valued untrusted absorption = %+v, want Matched=true NextDue=%v", three, tLate)
	}
	two := compiled.Evaluate(EvalModeTwoValued, inputs, untrustedResolver)
	if !two.Indeterminate || two.Blocker != "MISSING_INPUT:d/a" {
		t.Fatalf("2-valued pre-gate = %+v, want Indeterminate with MISSING_INPUT:d/a", two)
	}
	if untrustedCalls != 3 {
		t.Fatalf("expected 3 resolver calls (2 lazy 3-valued + 1 early fail in 2-valued), got %d", untrustedCalls)
	}

	// 2. Same semantic distinction for AND + false and RHS-absorbed LHS division-by-zero error.
	divExpr, err := ParseAndValidate("(a / b > 1) && false", inputs)
	if err != nil {
		t.Fatalf("ParseAndValidate divExpr error = %v", err)
	}
	divResolver := func(alias, _ string) InputResolution {
		if alias == "a" {
			return InputResolution{Trusted: true, Value: NumberValue(10)}
		}
		return InputResolution{Trusted: true, Value: NumberValue(5e-13)}
	}
	threeDiv := divExpr.Evaluate(EvalModeThreeValued, inputs, divResolver)
	if threeDiv.Indeterminate || threeDiv.Matched {
		t.Fatalf("3-valued div+false absorption = %+v, want Indeterminate=false Matched=false", threeDiv)
	}
	twoDiv := divExpr.Evaluate(EvalModeTwoValued, inputs, divResolver)
	if !twoDiv.Indeterminate || twoDiv.Blocker != "INVALID_INPUT:DIV_BY_ZERO" {
		t.Fatalf("2-valued div+false = %+v, want INVALID_INPUT:DIV_BY_ZERO", twoDiv)
	}

	// 3. Overflow / NaN / zero-value Value{} fail closed cleanly.
	overflowExpr, _ := ParseAndValidate("a * 10 > 0", map[string]string{"a": "d/a"})
	for _, resolver := range []InputResolver{
		func(_, _ string) InputResolution { return InputResolution{Trusted: true, Value: NumberValue(1e308)} },
		func(_, _ string) InputResolution { return InputResolution{Trusted: true, Value: Value{}} },
		func(_, _ string) InputResolution {
			return InputResolution{Trusted: true, Value: NumberValue(math.NaN())}
		},
		func(_, _ string) InputResolution {
			return InputResolution{Trusted: true, Value: StringValue("wrong-type")}
		},
	} {
		for _, mode := range []EvalMode{EvalModeThreeValued, EvalModeTwoValued} {
			res := overflowExpr.Evaluate(mode, map[string]string{"a": "d/a"}, resolver)
			if !res.Indeterminate {
				t.Fatalf("expected fail-closed Indeterminate in mode %d, got %+v", mode, res)
			}
		}
	}

	// 4. Runtime type-mismatch contracts across unary, function, arithmetic, and strict short-circuiting.
	runtimeContracts := []struct {
		expr string
		vals map[string]Value
	}{
		{"!a", map[string]Value{"a": NumberValue(1)}},
		{"-a > 0", map[string]Value{"a": BooleanValue(true)}},
		{"abs(a) > 0", map[string]Value{"a": StringValue("x")}},
		{"1 + a > 0", map[string]Value{"a": StringValue("x")}},
		{"a + 1 > 0", map[string]Value{"a": BooleanValue(false)}},
		{"a < 1", map[string]Value{"a": BooleanValue(false)}},
		{"1 < a", map[string]Value{"a": BooleanValue(false)}},
		{"a == b", map[string]Value{"a": NumberValue(1), "b": StringValue("1")}},
		{"!a && true", map[string]Value{"a": NumberValue(1)}},
		{"true && !a", map[string]Value{"a": NumberValue(1)}},
		{"!a || false", map[string]Value{"a": NumberValue(1)}},
		{"false || !a", map[string]Value{"a": NumberValue(1)}},
	}
	for _, rc := range runtimeContracts {
		in := make(map[string]string, len(rc.vals))
		for k := range rc.vals {
			in[k] = "d/" + k
		}
		expr, err := ParseAndValidate(rc.expr, in)
		if err != nil {
			t.Fatalf("ParseAndValidate(%q) error = %v", rc.expr, err)
		}
		for _, mode := range []EvalMode{EvalModeThreeValued, EvalModeTwoValued} {
			res := expr.Evaluate(mode, in, func(alias, _ string) InputResolution {
				return InputResolution{Trusted: true, Value: rc.vals[alias], NextDue: &tLate}
			})
			if !res.Indeterminate {
				t.Fatalf("expected indeterminate runtime mismatch for %q in mode %d, got %+v", rc.expr, mode, res)
			}
		}
	}

	// 5. Empty blocker fallback and strict mode short-circuiting ("false && b", "true || b").
	shortAnd, _ := ParseAndValidate("a && b", inputs)
	shortAndRes := shortAnd.Evaluate(EvalModeTwoValued, inputs, func(alias, _ string) InputResolution {
		if alias == "a" {
			return InputResolution{Trusted: true, Value: BooleanValue(false), NextDue: &tEarly}
		}
		return InputResolution{Trusted: true, Value: BooleanValue(true), NextDue: &tLate}
	})
	if shortAndRes.Indeterminate || shortAndRes.Matched || !shortAndRes.NextDue.Equal(tEarly) {
		t.Fatalf("strict short-circuit AND = %+v, want false NextDue=%v", shortAndRes, tEarly)
	}
	untrustedFallback := shortAnd.Evaluate(EvalModeTwoValued, inputs, func(alias, _ string) InputResolution {
		if alias == "a" {
			return InputResolution{Trusted: false}
		}
		return InputResolution{Trusted: true, Value: BooleanValue(true)}
	})
	if !untrustedFallback.Indeterminate || untrustedFallback.Blocker != "INVALID_INPUT:d/a" {
		t.Fatalf("empty blocker fallback = %+v, want INVALID_INPUT:d/a", untrustedFallback)
	}
}

func TestModeParityProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20261123))
	numAliases := []string{"n0", "n1", "n2", "n3"}
	boolAliases := []string{"b0", "b1"}
	strAliases := []string{"s0"}

	var genNum func(depth int, used map[string]struct{}) string
	var genBool func(depth int, used map[string]struct{}) string

	genNum = func(depth int, used map[string]struct{}) string {
		if depth >= 3 {
			switch rng.Intn(3) {
			case 0:
				a := numAliases[rng.Intn(len(numAliases))]
				used[a] = struct{}{}
				return a
			case 1:
				return fmt.Sprintf("%.2f", float64(rng.Intn(40)-20)/2.0)
			default:
				return fmt.Sprintf("%d", rng.Intn(15)-5)
			}
		}
		switch rng.Intn(6) {
		case 0:
			return "(" + genNum(depth+1, used) + " + " + genNum(depth+1, used) + ")"
		case 1:
			return "(" + genNum(depth+1, used) + " - " + genNum(depth+1, used) + ")"
		case 2:
			return "(" + genNum(depth+1, used) + " * " + genNum(depth+1, used) + ")"
		case 3:
			return "(" + genNum(depth+1, used) + " / " + genNum(depth+1, used) + ")"
		case 4:
			return "abs(" + genNum(depth+1, used) + ")"
		default:
			if rng.Intn(2) == 0 {
				return "min(" + genNum(depth+1, used) + ", " + genNum(depth+1, used) + ")"
			}
			return "max(" + genNum(depth+1, used) + ", " + genNum(depth+1, used) + ", " + genNum(depth+1, used) + ")"
		}
	}

	genBool = func(depth int, used map[string]struct{}) string {
		if depth >= 3 {
			switch rng.Intn(5) {
			case 0:
				a := boolAliases[rng.Intn(len(boolAliases))]
				used[a] = struct{}{}
				return a
			case 1:
				s := strAliases[rng.Intn(len(strAliases))]
				used[s] = struct{}{}
				return s + " == 'RUNNING'"
			case 2:
				return "(-" + genNum(depth, used) + " >= -5.0)"
			default:
				ops := []string{">", ">=", "<", "<=", "==", "!="}
				op := ops[rng.Intn(len(ops))]
				return "(" + genNum(depth, used) + " " + op + " " + genNum(depth, used) + ")"
			}
		}
		switch rng.Intn(5) {
		case 0:
			return "!(" + genBool(depth+1, used) + ")"
		case 1:
			return "(" + genBool(depth+1, used) + " && " + genBool(depth+1, used) + ")"
		case 2:
			return "(" + genBool(depth+1, used) + " || " + genBool(depth+1, used) + ")"
		case 3:
			return "(" + genBool(depth+1, used) + " == " + genBool(depth+1, used) + ")"
		default:
			return "(" + genBool(depth+1, used) + " != " + genBool(depth+1, used) + ")"
		}
	}

	const trials = 500
	checked := 0
	for i := 0; i < trials; i++ {
		used := make(map[string]struct{})
		exprStr := genBool(0, used)
		if len(exprStr) > MaxLength {
			continue
		}
		inputs := make(map[string]string, len(used))
		for alias := range used {
			inputs[alias] = "dev/" + alias
		}
		compiled, err := ParseAndValidate(exprStr, inputs)
		if err != nil {
			continue
		}

		vals := make(map[string]Value, len(inputs))
		for alias := range inputs {
			switch alias[0] {
			case 'n':
				// Include small non-zero values plus occasional 0 to exercise DivZeroEpsilon parity too.
				choices := []float64{-12.5, -2.0, -0.5, 0, 0.25, 1.5, 8.0, 24.5}
				vals[alias] = NumberValue(choices[rng.Intn(len(choices))])
			case 'b':
				vals[alias] = BooleanValue(rng.Intn(2) == 1)
			case 's':
				modes := []string{"RUNNING", "STOPPED", "FAULT"}
				vals[alias] = StringValue(modes[rng.Intn(len(modes))])
			}
		}
		resolver := func(alias, _ string) InputResolution {
			return InputResolution{Trusted: true, Value: vals[alias]}
		}

		three := compiled.Evaluate(EvalModeThreeValued, inputs, resolver)
		two := compiled.Evaluate(EvalModeTwoValued, inputs, resolver)
		if !two.Indeterminate {
			if three.Indeterminate || three.Matched != two.Matched {
				t.Fatalf("parity mismatch on %q: three=%+v two=%+v vals=%+v", exprStr, three, two, vals)
			}
		} else {
			if !strings.HasPrefix(two.Blocker, BlockerInvalidPrefix) {
				t.Fatalf("unexpected blocker in two-valued mode: %+v", two)
			}
			// If three-valued did not hit indeterminate, it can only be because an arithmetic error was absorbed by Kleene logic.
			if three.Indeterminate && three.Matched != two.Matched {
				t.Fatalf("unexpected matched diff when both indeterminate: %+v vs %+v", three, two)
			}
		}
		checked++
	}
	if checked < 350 {
		t.Fatalf("expected at least 350 valid random parity trials, got %d", checked)
	}
}

func FuzzParseAndEvaluate(f *testing.F) {
	seeds := []string{
		"a > 10",
		"(a - b) / max(c, 1) >= 2.5 && flag",
		"abs(a) < 1e-2 || !(b == 0)",
		"(a / 0 > 0) || true",
		"a == b && !flag",
		"min(a, b, 3.14) <= 2",
	}
	for _, s := range seeds {
		f.Add(s, 12.5, 0.0, -3.5, true)
	}
	f.Fuzz(func(t *testing.T, expr string, va, vb, vc float64, flag bool) {
		if math.IsNaN(va) || math.IsInf(va, 0) || math.IsNaN(vb) || math.IsInf(vb, 0) || math.IsNaN(vc) || math.IsInf(vc, 0) {
			return
		}
		inputs := map[string]string{
			"a":    "dev/a",
			"b":    "dev/b",
			"c":    "dev/c",
			"flag": "dev/flag",
		}
		// Trim to only inputs mentioned? ParseAndValidate enforces 1:1 used/declared aliases.
		active := make(map[string]string, 4)
		for k, v := range inputs {
			if strings.Contains(expr, k) {
				active[k] = v
			}
		}
		errValidate := Validate(expr, active)
		compiled, errCompile := ParseAndValidate(expr, active)
		if (errValidate == nil) != (errCompile == nil) {
			t.Fatalf("Validate vs ParseAndValidate disagree on %q: %v vs %v", expr, errValidate, errCompile)
		}
		if errCompile != nil {
			return
		}

		resolver := func(alias, _ string) InputResolution {
			switch alias {
			case "a":
				return InputResolution{Trusted: true, Value: NumberValue(va)}
			case "b":
				return InputResolution{Trusted: true, Value: NumberValue(vb)}
			case "c":
				return InputResolution{Trusted: true, Value: NumberValue(vc)}
			case "flag":
				return InputResolution{Trusted: true, Value: BooleanValue(flag)}
			default:
				return InputResolution{}
			}
		}

		three := compiled.Evaluate(EvalModeThreeValued, active, resolver)
		two := compiled.Evaluate(EvalModeTwoValued, active, resolver)
		if !two.Indeterminate {
			if three.Indeterminate || three.Matched != two.Matched {
				t.Fatalf("fuzz parity mismatch on %q: three=%+v two=%+v", expr, three, two)
			}
		} else if !three.Indeterminate && !strings.HasPrefix(two.Blocker, BlockerInvalidPrefix) {
			t.Fatalf("unexpected two-valued indeterminate without invalid-input blocker on %q: %+v", expr, two)
		}
	})
}
