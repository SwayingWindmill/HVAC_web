package alarmservice

import (
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmexpr"
)

const (
	MaxExpressionASTNodes = alarmexpr.MaxASTNodes
	MaxExpressionDepth    = alarmexpr.MaxDepth
	MaxExpressionLength   = alarmexpr.MaxLength
	MaxExpressionInputs   = alarmexpr.MaxInputs
)

// ValidateExpression parses expr, verifies syntax and resource bounds (length, AST node count,
// nesting depth, and input count), checks that identifiers and declared inputs match 1:1, and
// statically proves operator types and a boolean root result.
func ValidateExpression(expr string, inputs map[string]string) error {
	return alarmexpr.Validate(expr, inputs)
}

func evaluateExpression(policy AlarmPolicyRevision, condition Condition, snapshot EvaluationSnapshot, now time.Time) predicateResult {
	compiled, err := alarmexpr.ParseAndValidate(condition.Expression, condition.ExprInputs)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + "EXPRESSION"}
	}
	res := compiled.Evaluate(alarmexpr.EvalModeThreeValued, condition.ExprInputs, func(_, inputKey string) alarmexpr.InputResolution {
		fact, exists := snapshot.Inputs[inputKey]
		trusted, blocker, nextDue := trustedInput(policy, inputKey, fact, exists, now)
		if !trusted {
			return alarmexpr.InputResolution{
				Trusted: false,
				Blocker: blocker,
				NextDue: nextDue,
			}
		}
		val, ok := toAlarmExprValue(fact.Value)
		if !ok {
			return alarmexpr.InputResolution{
				Trusted: false,
				Blocker: qualityBlockerInvalidPrefix + inputKey,
				NextDue: nextDue,
			}
		}
		return alarmexpr.InputResolution{
			Trusted: true,
			Value:   val,
			NextDue: nextDue,
		}
	})
	if res.Indeterminate {
		return predicateResult{
			status:  EvaluationIndeterminate,
			blocker: res.Blocker,
			nextDue: res.NextDue,
		}
	}
	return truthResult(res.Matched, res.NextDue)
}

func toAlarmExprValue(v TypedValue) (alarmexpr.Value, bool) {
	switch v.Type {
	case EvaluationValueNumber:
		return alarmexpr.NumberValue(v.Number), true
	case EvaluationValueBoolean:
		return alarmexpr.BooleanValue(v.Boolean), true
	case EvaluationValueString:
		return alarmexpr.StringValue(v.String), true
	default:
		return alarmexpr.Value{}, false
	}
}
