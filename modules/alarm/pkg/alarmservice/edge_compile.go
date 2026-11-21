package alarmservice

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	EdgeEligibilityCloudOnlyTarget     = "CLOUD_ONLY_TARGET"
	EdgeEligibilityScheduleUnsupported = "EDGE_SCHEDULE_NOT_SUPPORTED"
	EdgeEligibilityUnsupportedCond     = "EDGE_UNSUPPORTED_CONDITION_KIND"
	EdgeEligibilityCloudOnlyAction     = "EDGE_CLOUD_ONLY_ACTION"
	EdgeEligibilityUpstreamNotOnEdge   = "EDGE_UPSTREAM_NOT_ON_EDGE"
	EdgeEligibilityEdgeLimitExceeded   = "EDGE_BUNDLE_LIMIT_EXCEEDED"
)

// EdgeRuleEligibility explains whether one site alarm rule compiles for edge autonomy execution.
type EdgeRuleEligibility struct {
	RuleID          string          `json:"ruleId"`
	Title           string          `json:"title"`
	ExecutionTarget ExecutionTarget `json:"executionTarget"`
	Compiled        bool            `json:"compiled"`
	ReasonCode      string          `json:"reasonCode,omitempty"`
	Message         string          `json:"message,omitempty"`
}

// EdgeRuleCompileRequest optionally accepts unsaved rule drafts to preview edge compilation.
type EdgeRuleCompileRequest struct {
	Rules *[]SiteRuleDraft `json:"rules,omitempty"`
}

// EdgeRuleCompileResult returns per-rule edge eligibility and the immutable compiled edge bundle IR.
type EdgeRuleCompileResult struct {
	SiteID           string                     `json:"siteId"`
	BundleRevision   uint64                     `json:"bundleRevision"`
	BundleDigest     string                     `json:"bundleDigest,omitempty"`
	EdgeBundleDigest string                     `json:"edgeBundleDigest"`
	CompiledAt       string                     `json:"compiledAt"`
	EdgeRuleCount    int                        `json:"edgeRuleCount"`
	CloudOnlyCount   int                        `json:"cloudOnlyCount"`
	Eligibility      []EdgeRuleEligibility      `json:"eligibility"`
	Bundle           edgecontrol.EdgeRuleBundle `json:"bundle"`
}

// CheckEdgeRuleEligibility evaluates whether rule satisfies edge runtime execution constraints
// given the set of upstream rule IDs already compiled for edge execution.
func CheckEdgeRuleEligibility(rule SiteRule, compiledEdgeSet map[string]bool) EdgeRuleEligibility {
	target := rule.Policy.EffectiveExecutionTarget()
	base := EdgeRuleEligibility{
		RuleID:          rule.AssignmentID,
		Title:           rule.Policy.Title,
		ExecutionTarget: target,
	}
	if target != ExecutionTargetEdge && target != ExecutionTargetCloudAndEdge {
		base.ReasonCode = EdgeEligibilityCloudOnlyTarget
		base.Message = "rule executionTarget is CLOUD"
		return base
	}
	if rule.Policy.Schedule != nil {
		base.ReasonCode = EdgeEligibilityScheduleUnsupported
		base.Message = "rules using site schedules execute in cloud only"
		return base
	}
	if unsupported, hasUnsupported := firstUnsupportedEdgeCondition(rule.Policy.Raise); hasUnsupported {
		base.ReasonCode = EdgeEligibilityUnsupportedCond
		base.Message = fmt.Sprintf("raise condition kind %s is not supported on edge runtime", unsupported)
		return base
	}
	if unsupported, hasUnsupported := firstUnsupportedEdgeCondition(rule.Policy.Clear); hasUnsupported {
		base.ReasonCode = EdgeEligibilityUnsupportedCond
		base.Message = fmt.Sprintf("clear condition kind %s is not supported on edge runtime", unsupported)
		return base
	}
	if target == ExecutionTargetEdge {
		for _, action := range rule.Policy.Actions {
			if action.Kind != RuleActionProtectionCommand {
				base.ReasonCode = EdgeEligibilityCloudOnlyAction
				base.Message = fmt.Sprintf("EDGE rule cannot declare %s action; switch to CLOUD_AND_EDGE", action.Kind)
				return base
			}
		}
	}
	if rule.Policy.DependencyGate == DependencyGateAllActive || rule.Policy.DependencyGate == DependencyGateAnyActive {
		for _, depID := range rule.Policy.DependsOnRuleIDs {
			if !compiledEdgeSet[depID] {
				base.ReasonCode = EdgeEligibilityUpstreamNotOnEdge
				base.Message = fmt.Sprintf("upstream rule %s does not compile to edge", depID)
				return base
			}
		}
	}
	base.Compiled = true
	return base
}

func firstUnsupportedEdgeCondition(c Condition) (ConditionKind, bool) {
	switch c.Kind {
	case ConditionAnd, ConditionOr:
		for _, ch := range c.Children {
			if bad, ok := firstUnsupportedEdgeCondition(ch); ok {
				return bad, true
			}
		}
		return "", false
	case ConditionCompare, ConditionRange, ConditionHysteresis, ConditionSlope, ConditionWindowAgg:
		if strings.ContainsAny(c.Input, " \t\r\n") {
			return c.Kind, true
		}
		return "", false
	case ConditionExpression:
		if len(c.ExprInputs) == 0 {
			return c.Kind, true
		}
		for _, input := range c.ExprInputs {
			if strings.ContainsAny(input, " \t\r\n") {
				return c.Kind, true
			}
		}
		return "", false
	default:
		return c.Kind, true
	}
}

// CompileSiteRulesForEdge compiles all eligible EDGE and CLOUD_AND_EDGE rules for siteID into
// a topologically ordered, digest-verified EdgeRuleBundle.
func CompileSiteRulesForEdge(siteID string, bundleRevision uint64, bundleDigest string, rules []SiteRule, now time.Time) (EdgeRuleCompileResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	report, err := ValidateSiteRuleGraph(rules)
	if err != nil {
		return EdgeRuleCompileResult{}, err
	}
	byID := make(map[string]SiteRule, len(rules))
	for _, r := range rules {
		byID[r.AssignmentID] = r
	}

	compiledSet := make(map[string]bool, len(rules))
	eligibility := make([]EdgeRuleEligibility, 0, len(rules))
	compiledRules := make([]edgecontrol.CompiledEdgeRule, 0, len(rules))

	for _, ruleID := range report.Order {
		rule, ok := byID[ruleID]
		if !ok {
			continue
		}
		el := CheckEdgeRuleEligibility(rule, compiledSet)
		if el.Compiled && len(compiledRules) >= edgecontrol.MaxEdgeRulesPerBundle {
			el.Compiled = false
			el.ReasonCode = EdgeEligibilityEdgeLimitExceeded
			el.Message = fmt.Sprintf("edge rule bundle supports at most %d rules", edgecontrol.MaxEdgeRulesPerBundle)
		}
		eligibility = append(eligibility, el)
		if !el.Compiled {
			continue
		}
		compiled, convErr := compileSingleRuleToEdge(rule, compiledSet)
		if convErr != nil {
			return EdgeRuleCompileResult{}, convErr
		}
		compiledSet[ruleID] = true
		compiledRules = append(compiledRules, compiled)
	}

	bundle := edgecontrol.EdgeRuleBundle{
		SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
		SiteID:         siteID,
		BundleRevision: bundleRevision,
		BundleDigest:   bundleDigest,
		CompiledAt:     now.Format(time.RFC3339Nano),
		Rules:          compiledRules,
	}
	edgeDigest, err := bundle.ComputeDigest()
	if err != nil {
		return EdgeRuleCompileResult{}, err
	}
	bundle.EdgeBundleDigest = edgeDigest
	if err := bundle.Validate(); err != nil {
		return EdgeRuleCompileResult{}, err
	}

	return EdgeRuleCompileResult{
		SiteID:           siteID,
		BundleRevision:   bundleRevision,
		BundleDigest:     bundleDigest,
		EdgeBundleDigest: edgeDigest,
		CompiledAt:       bundle.CompiledAt,
		EdgeRuleCount:    len(compiledRules),
		CloudOnlyCount:   len(rules) - len(compiledRules),
		Eligibility:      eligibility,
		Bundle:           bundle,
	}, nil
}

func compileSingleRuleToEdge(rule SiteRule, compiledSet map[string]bool) (edgecontrol.CompiledEdgeRule, error) {
	raise, err := compileConditionToEdge(rule.Policy.Raise)
	if err != nil {
		return edgecontrol.CompiledEdgeRule{}, fmt.Errorf("compile raise of %s: %w", rule.AssignmentID, err)
	}
	clearCond, err := compileConditionToEdge(rule.Policy.Clear)
	if err != nil {
		return edgecontrol.CompiledEdgeRule{}, fmt.Errorf("compile clear of %s: %w", rule.AssignmentID, err)
	}

	qualityPolicy := edgecontrol.EdgeQualityPolicy(rule.Policy.QualityPolicy)
	if qualityPolicy == "" {
		qualityPolicy = edgecontrol.EdgeQualityValidOnly
	}
	freshness := rule.Policy.FreshnessSeconds
	if freshness <= 0 {
		freshness = 900
	}

	var edgeDeps []string
	if rule.Policy.DependencyGate == DependencyGateAllActive || rule.Policy.DependencyGate == DependencyGateAnyActive {
		for _, depID := range rule.Policy.DependsOnRuleIDs {
			if compiledSet[depID] {
				edgeDeps = append(edgeDeps, depID)
			}
		}
	}
	var gate edgecontrol.EdgeDependencyGate
	if len(edgeDeps) > 0 && rule.Policy.DependencyGate != "" {
		gate = edgecontrol.EdgeDependencyGate(rule.Policy.DependencyGate)
	}

	var edgeActions []edgecontrol.EdgeRuleAction
	for _, action := range rule.Policy.Actions {
		if action.Kind != RuleActionProtectionCommand {
			continue
		}
		var params map[string]float64
		if len(action.Parameters) > 0 {
			params = make(map[string]float64, len(action.Parameters))
			for k, v := range action.Parameters {
				params[k] = v
			}
		}
		edgeActions = append(edgeActions, edgecontrol.EdgeRuleAction{
			Kind:            edgecontrol.EdgeRuleActionProtectionCommand,
			TriggerOn:       edgecontrol.EdgeActionTriggerOn(action.EffectiveTriggerOn()),
			TargetDeviceID:  action.TargetDeviceID,
			TargetPointCode: action.TargetPointCode,
			Capability:      action.Capability,
			Parameters:      params,
		})
	}

	return edgecontrol.CompiledEdgeRule{
		RuleID:           rule.AssignmentID,
		AlarmType:        rule.Policy.AlarmType,
		Title:            rule.Policy.Title,
		Severity:         string(rule.Policy.Severity),
		ExecutionTarget:  edgecontrol.EdgeExecutionTarget(rule.Policy.EffectiveExecutionTarget()),
		DependsOnRuleIDs: edgeDeps,
		DependencyGate:   gate,
		QualityPolicy:    qualityPolicy,
		FreshnessSeconds: freshness,
		TriggerMode:      edgecontrol.EdgeTriggerMode(rule.Policy.TriggerMode),
		DurationSeconds:  rule.Policy.DurationSeconds,
		RepeatCount:      rule.Policy.RepeatCount,
		Raise:            raise,
		Clear:            clearCond,
		Actions:          edgeActions,
	}, nil
}

func compileConditionToEdge(c Condition) (edgecontrol.EdgeCondition, error) {
	switch c.Kind {
	case ConditionAnd, ConditionOr:
		children := make([]edgecontrol.EdgeCondition, len(c.Children))
		for i, ch := range c.Children {
			compiled, err := compileConditionToEdge(ch)
			if err != nil {
				return edgecontrol.EdgeCondition{}, err
			}
			children[i] = compiled
		}
		return edgecontrol.EdgeCondition{
			Kind:     edgecontrol.EdgeConditionKind(c.Kind),
			Children: children,
		}, nil
	case ConditionCompare:
		val := edgecontrol.EdgeTypedValue{
			Type:    edgecontrol.EdgeValueType(c.Value.Type),
			Number:  c.Value.Number,
			String:  c.Value.String,
			Boolean: c.Value.Boolean,
		}
		return edgecontrol.EdgeCondition{
			Kind:     edgecontrol.EdgeConditionCompare,
			Input:    c.Input,
			Operator: edgecontrol.EdgeCompareOperator(c.Operator),
			Value:    &val,
		}, nil
	case ConditionRange:
		return edgecontrol.EdgeCondition{
			Kind:    edgecontrol.EdgeConditionRange,
			Input:   c.Input,
			Minimum: copyFloat64Ptr(c.Minimum),
			Maximum: copyFloat64Ptr(c.Maximum),
		}, nil
	case ConditionHysteresis:
		trig, reset := c.Trigger, c.Reset
		return edgecontrol.EdgeCondition{
			Kind:      edgecontrol.EdgeConditionHysteresis,
			Input:     c.Input,
			Direction: edgecontrol.EdgeHysteresisDirection(c.Direction),
			Trigger:   &trig,
			Reset:     &reset,
		}, nil
	case ConditionSlope:
		return edgecontrol.EdgeCondition{
			Kind:          edgecontrol.EdgeConditionSlope,
			Input:         c.Input,
			Operator:      edgecontrol.EdgeCompareOperator(c.Operator),
			Delta:         copyFloat64Ptr(c.Delta),
			WindowSeconds: c.WindowSeconds,
		}, nil
	case ConditionWindowAgg:
		return edgecontrol.EdgeCondition{
			Kind:          edgecontrol.EdgeConditionWindowAgg,
			Input:         c.Input,
			Agg:           edgecontrol.EdgeWindowAggFunc(c.Agg),
			WindowType:    edgecontrol.EdgeWindowType(c.WindowType),
			WindowSeconds: c.WindowSeconds,
			MinSamples:    c.MinSamples,
			Operator:      edgecontrol.EdgeCompareOperator(c.Operator),
			Threshold:     copyFloat64Ptr(c.Threshold),
		}, nil
	case ConditionExpression:
		inputs := make(map[string]string, len(c.ExprInputs))
		for k, v := range c.ExprInputs {
			inputs[k] = v
		}
		return edgecontrol.EdgeCondition{
			Kind:       edgecontrol.EdgeConditionExpression,
			Expression: c.Expression,
			Inputs:     inputs,
		}, nil
	default:
		return edgecontrol.EdgeCondition{}, fmt.Errorf("unsupported edge condition kind %q", c.Kind)
	}
}

func copyFloat64Ptr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// validateDraftEdgeEligibility verifies in topological order that rules requesting edge execution
// satisfy all edge compilation constraints before diff or publish succeeds.
func validateDraftEdgeEligibility(rules []SiteRule, order []string) []SiteRuleValidationIssue {
	byID := make(map[string]SiteRule, len(rules))
	for _, r := range rules {
		byID[r.AssignmentID] = r
	}
	if len(order) != len(rules) {
		order = make([]string, 0, len(rules))
		for _, r := range rules {
			order = append(order, r.AssignmentID)
		}
		slices.Sort(order)
	}
	compiledSet := make(map[string]bool, len(rules))
	var issues []SiteRuleValidationIssue
	for _, id := range order {
		rule, ok := byID[id]
		if !ok {
			continue
		}
		target := rule.Policy.EffectiveExecutionTarget()
		if target != ExecutionTargetEdge && target != ExecutionTargetCloudAndEdge {
			continue
		}
		el := CheckEdgeRuleEligibility(rule, compiledSet)
		if !el.Compiled {
			issues = append(issues, SiteRuleValidationIssue{
				RuleID:  rule.AssignmentID,
				Field:   "executionTarget",
				Code:    "EDGE_RULE_INELIGIBLE",
				Message: el.Message,
			})
			continue
		}
		if len(compiledSet) >= edgecontrol.MaxEdgeRulesPerBundle {
			issues = append(issues, SiteRuleValidationIssue{
				RuleID:  rule.AssignmentID,
				Field:   "executionTarget",
				Code:    "EDGE_RULE_INELIGIBLE",
				Message: fmt.Sprintf("edge rule bundle supports at most %d rules", edgecontrol.MaxEdgeRulesPerBundle),
			})
			continue
		}
		compiledSet[id] = true
	}
	return issues
}

// Validate checks compile result invariants before returning from gateway or internal handlers.
func (result EdgeRuleCompileResult) Validate() error {
	if !alarmmodel.IsUUIDv7(result.SiteID) {
		return errors.New("edge compile result siteId is invalid")
	}
	if result.SiteID != result.Bundle.SiteID {
		return errors.New("edge compile result siteId does not match bundle siteId")
	}
	if result.EdgeRuleCount < 0 || result.CloudOnlyCount < 0 || result.EdgeRuleCount != len(result.Bundle.Rules) {
		return errors.New("edge compile result rule counts are inconsistent")
	}
	if result.EdgeBundleDigest == "" || result.EdgeBundleDigest != result.Bundle.EdgeBundleDigest {
		return errors.New("edge compile result digest mismatch")
	}
	if result.Eligibility == nil {
		return errors.New("edge compile result eligibility must not be nil")
	}
	return result.Bundle.Validate()
}
