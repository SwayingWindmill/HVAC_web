package edgecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	EdgeRuleSchemaVersion = 1

	DefaultEdgeRuleControllerID = "edge-rule-engine"

	MaxEdgeRulesPerBundle        = 128
	MaxEdgeRuleConditionDepth    = 8
	MaxEdgeRuleConditionChildren = 32
	MaxEdgeRuleDependencies      = 16
	MaxEdgeRuleLinkageHops       = 3
	MaxEdgeRuleActions           = 4
	MaxEdgeRuleWindowSeconds     = 86400
	MaxEdgeRuleMinSamples        = 128
	MaxEdgeRuleDurationSeconds   = int64(365 * 24 * 60 * 60)
	MaxEdgeRuleSamplesPerRule    = 256
	MaxEdgeRuleSamplesPerInput   = 128
	MaxEdgeRuleEventQueue        = 256
)

var (
	edgeUUIDPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	edgeDigestPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	edgePointCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

type EdgeWindowAggFunc string

const (
	EdgeWindowAggAvg    EdgeWindowAggFunc = "AVG"
	EdgeWindowAggMin    EdgeWindowAggFunc = "MIN"
	EdgeWindowAggMax    EdgeWindowAggFunc = "MAX"
	EdgeWindowAggSum    EdgeWindowAggFunc = "SUM"
	EdgeWindowAggCount  EdgeWindowAggFunc = "COUNT"
	EdgeWindowAggStdDev EdgeWindowAggFunc = "STDDEV"
	EdgeWindowAggP95    EdgeWindowAggFunc = "P95"
	EdgeWindowAggP99    EdgeWindowAggFunc = "P99"
)

func (agg EdgeWindowAggFunc) Valid() bool {
	switch agg {
	case EdgeWindowAggAvg, EdgeWindowAggMin, EdgeWindowAggMax, EdgeWindowAggSum,
		EdgeWindowAggCount, EdgeWindowAggStdDev, EdgeWindowAggP95, EdgeWindowAggP99:
		return true
	default:
		return false
	}
}

type EdgeWindowType string

const (
	EdgeWindowSliding  EdgeWindowType = "SLIDING"
	EdgeWindowTumbling EdgeWindowType = "TUMBLING"
)

func (wt EdgeWindowType) Valid() bool {
	return wt == EdgeWindowSliding || wt == EdgeWindowTumbling
}

type EdgeExecutionTarget string

const (
	EdgeTargetEdge         EdgeExecutionTarget = "EDGE"
	EdgeTargetCloudAndEdge EdgeExecutionTarget = "CLOUD_AND_EDGE"
)

func (target EdgeExecutionTarget) Valid() bool {
	return target == EdgeTargetEdge || target == EdgeTargetCloudAndEdge
}

type EdgeDependencyGate string

const (
	EdgeDependencyGateNone      EdgeDependencyGate = "NONE"
	EdgeDependencyGateAllActive EdgeDependencyGate = "ALL_ACTIVE"
	EdgeDependencyGateAnyActive EdgeDependencyGate = "ANY_ACTIVE"
)

type EdgeQualityPolicy string

const (
	EdgeQualityValidOnly EdgeQualityPolicy = "VALID_ONLY"
	EdgeQualityUsable    EdgeQualityPolicy = "USABLE"
)

type EdgeTriggerMode string

const (
	EdgeTriggerSimple    EdgeTriggerMode = "SIMPLE"
	EdgeTriggerDuration  EdgeTriggerMode = "DURATION"
	EdgeTriggerRepeating EdgeTriggerMode = "REPEATING"
)

type EdgeConditionKind string

const (
	EdgeConditionCompare    EdgeConditionKind = "COMPARE"
	EdgeConditionRange      EdgeConditionKind = "RANGE"
	EdgeConditionHysteresis EdgeConditionKind = "HYSTERESIS"
	EdgeConditionSlope      EdgeConditionKind = "SLOPE"
	EdgeConditionWindowAgg  EdgeConditionKind = "WINDOW_AGG"
	EdgeConditionExpression EdgeConditionKind = "EXPRESSION"
	EdgeConditionAnd        EdgeConditionKind = "AND"
	EdgeConditionOr         EdgeConditionKind = "OR"
)

type EdgeCompareOperator string

const (
	EdgeCompareEQ  EdgeCompareOperator = "EQ"
	EdgeCompareNE  EdgeCompareOperator = "NE"
	EdgeCompareGT  EdgeCompareOperator = "GT"
	EdgeCompareGTE EdgeCompareOperator = "GTE"
	EdgeCompareLT  EdgeCompareOperator = "LT"
	EdgeCompareLTE EdgeCompareOperator = "LTE"
)

type EdgeHysteresisDirection string

const (
	EdgeDirectionAbove EdgeHysteresisDirection = "ABOVE"
	EdgeDirectionBelow EdgeHysteresisDirection = "BELOW"
)

type EdgeValueType string

const (
	EdgeValueNumber  EdgeValueType = "NUMBER"
	EdgeValueString  EdgeValueType = "STRING"
	EdgeValueBoolean EdgeValueType = "BOOLEAN"
)

type EdgeTypedValue struct {
	Type    EdgeValueType `json:"type"`
	Number  float64       `json:"number,omitempty"`
	String  string        `json:"string,omitempty"`
	Boolean bool          `json:"boolean,omitempty"`
}

func (v EdgeTypedValue) Valid() bool {
	switch v.Type {
	case EdgeValueNumber:
		return isFiniteFloat(v.Number)
	case EdgeValueString:
		return len(v.String) <= 256
	case EdgeValueBoolean:
		return true
	default:
		return false
	}
}

type EdgeCondition struct {
	Kind          EdgeConditionKind       `json:"kind"`
	Input         string                  `json:"input,omitempty"`
	Operator      EdgeCompareOperator     `json:"operator,omitempty"`
	Value         *EdgeTypedValue         `json:"value,omitempty"`
	Minimum       *float64                `json:"minimum,omitempty"`
	Maximum       *float64                `json:"maximum,omitempty"`
	Direction     EdgeHysteresisDirection `json:"direction,omitempty"`
	Trigger       *float64                `json:"trigger,omitempty"`
	Reset         *float64                `json:"reset,omitempty"`
	Delta         *float64                `json:"delta,omitempty"`
	WindowSeconds int64                   `json:"windowSeconds,omitempty"`
	Agg           EdgeWindowAggFunc       `json:"agg,omitempty"`
	WindowType    EdgeWindowType          `json:"windowType,omitempty"`
	MinSamples    int                     `json:"minSamples,omitempty"`
	Threshold     *float64                `json:"threshold,omitempty"`
	Expression    string                  `json:"expression,omitempty"`
	Inputs        map[string]string       `json:"inputs,omitempty"`
	Children      []EdgeCondition         `json:"children,omitempty"`
}

type EdgeRuleActionKind string

const (
	EdgeRuleActionProtectionCommand EdgeRuleActionKind = "PROTECTION_COMMAND"
)

type EdgeActionTriggerOn string

const (
	EdgeActionTriggerRaise EdgeActionTriggerOn = "RAISE"
	EdgeActionTriggerClear EdgeActionTriggerOn = "CLEAR"
	EdgeActionTriggerBoth  EdgeActionTriggerOn = "BOTH"
)

type EdgeRuleAction struct {
	Kind            EdgeRuleActionKind  `json:"kind"`
	TriggerOn       EdgeActionTriggerOn `json:"triggerOn,omitempty"`
	TargetDeviceID  string              `json:"targetDeviceId"`
	TargetPointCode string              `json:"targetPointCode,omitempty"`
	Capability      string              `json:"capability"`
	Parameters      map[string]float64  `json:"parameters,omitempty"`
}

func (a EdgeRuleAction) EffectiveTriggerOn() EdgeActionTriggerOn {
	if a.TriggerOn == "" {
		return EdgeActionTriggerRaise
	}
	return a.TriggerOn
}

func (a EdgeRuleAction) Clone() EdgeRuleAction {
	cp := a
	if a.Parameters != nil {
		cp.Parameters = make(map[string]float64, len(a.Parameters))
		for k, v := range a.Parameters {
			cp.Parameters[k] = v
		}
	}
	return cp
}

type CompiledEdgeRule struct {
	RuleID           string              `json:"ruleId"`
	AlarmType        string              `json:"alarmType,omitempty"`
	Title            string              `json:"title"`
	Severity         string              `json:"severity"`
	ExecutionTarget  EdgeExecutionTarget `json:"executionTarget"`
	DependsOnRuleIDs []string            `json:"dependsOnRuleIds,omitempty"`
	DependencyGate   EdgeDependencyGate  `json:"dependencyGate,omitempty"`
	QualityPolicy    EdgeQualityPolicy   `json:"qualityPolicy"`
	FreshnessSeconds int64               `json:"freshnessSeconds"`
	TriggerMode      EdgeTriggerMode     `json:"triggerMode"`
	DurationSeconds  int64               `json:"durationSeconds,omitempty"`
	RepeatCount      uint64              `json:"repeatCount,omitempty"`
	Raise            EdgeCondition       `json:"raise"`
	Clear            EdgeCondition       `json:"clear"`
	Actions          []EdgeRuleAction    `json:"actions,omitempty"`
}

type EdgeRuleBundle struct {
	SchemaVersion    int                `json:"schemaVersion"`
	SiteID           string             `json:"siteId"`
	BundleRevision   uint64             `json:"bundleRevision"`
	BundleDigest     string             `json:"bundleDigest,omitempty"`
	EdgeBundleDigest string             `json:"edgeBundleDigest"`
	CompiledAt       string             `json:"compiledAt,omitempty"`
	Rules            []CompiledEdgeRule `json:"rules"`
}

const (
	EdgeRuleEventRaised  = "EDGE_RULE_ALARM_RAISED"
	EdgeRuleEventCleared = "EDGE_RULE_ALARM_CLEARED"
)

type EdgeRuleEvent struct {
	EventID          string   `json:"eventId"`
	EventType        string   `json:"eventType"`
	RuleID           string   `json:"ruleId"`
	AlarmType        string   `json:"alarmType,omitempty"`
	Title            string   `json:"title"`
	Severity         string   `json:"severity"`
	OccurredAt       string   `json:"occurredAt"`
	BundleRevision   uint64   `json:"bundleRevision"`
	EdgeBundleDigest string   `json:"edgeBundleDigest"`
	Offline          bool     `json:"offline"`
	Reason           string   `json:"reason,omitempty"`
	TriggeredActions []string `json:"triggeredActions,omitempty"`
}

type EdgeRuleEngineStatus struct {
	BundleRevision   uint64 `json:"bundleRevision"`
	EdgeBundleDigest string `json:"edgeBundleDigest,omitempty"`
	RuleCount        int    `json:"ruleCount"`
	ActiveRuleCount  int    `json:"activeRuleCount"`
	Offline          bool   `json:"offline"`
}

// ComputeWindowAggregate computes deterministic statistical window metrics shared
// between the cloud evaluator and edge runtime.
func ComputeWindowAggregate(agg EdgeWindowAggFunc, values []float64) (float64, bool) {
	n := len(values)
	if n == 0 {
		return 0, false
	}
	for _, v := range values {
		if !isFiniteFloat(v) {
			return 0, false
		}
	}
	switch agg {
	case EdgeWindowAggCount:
		return float64(n), true
	case EdgeWindowAggSum:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum, isFiniteFloat(sum)
	case EdgeWindowAggAvg:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		avg := sum / float64(n)
		return avg, isFiniteFloat(avg)
	case EdgeWindowAggMin:
		best := values[0]
		for _, v := range values[1:] {
			if v < best {
				best = v
			}
		}
		return best, true
	case EdgeWindowAggMax:
		best := values[0]
		for _, v := range values[1:] {
			if v > best {
				best = v
			}
		}
		return best, true
	case EdgeWindowAggStdDev:
		if n < 2 {
			return 0, false
		}
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		mean := sum / float64(n)
		varSum := 0.0
		for _, v := range values {
			d := v - mean
			varSum += d * d
		}
		std := math.Sqrt(varSum / float64(n))
		return std, isFiniteFloat(std)
	case EdgeWindowAggP95:
		return linearQuantile(values, 0.95)
	case EdgeWindowAggP99:
		return linearQuantile(values, 0.99)
	default:
		return 0, false
	}
}

func linearQuantile(values []float64, q float64) (float64, bool) {
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	n := len(sorted)
	if n == 1 {
		return sorted[0], true
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	frac := pos - float64(lo)
	out := sorted[lo] + frac*(sorted[hi]-sorted[lo])
	return out, isFiniteFloat(out)
}

func (c EdgeCondition) Clone() EdgeCondition {
	cp := c
	if c.Value != nil {
		v := *c.Value
		cp.Value = &v
	}
	cp.Minimum = cloneEdgeFloatPtr(c.Minimum)
	cp.Maximum = cloneEdgeFloatPtr(c.Maximum)
	cp.Trigger = cloneEdgeFloatPtr(c.Trigger)
	cp.Reset = cloneEdgeFloatPtr(c.Reset)
	cp.Delta = cloneEdgeFloatPtr(c.Delta)
	cp.Threshold = cloneEdgeFloatPtr(c.Threshold)
	if c.Inputs != nil {
		cp.Inputs = make(map[string]string, len(c.Inputs))
		for k, v := range c.Inputs {
			cp.Inputs[k] = v
		}
	}
	if len(c.Children) > 0 {
		cp.Children = make([]EdgeCondition, len(c.Children))
		for i, ch := range c.Children {
			cp.Children[i] = ch.Clone()
		}
	}
	return cp
}

func (r CompiledEdgeRule) Clone() CompiledEdgeRule {
	cp := r
	cp.DependsOnRuleIDs = slices.Clone(r.DependsOnRuleIDs)
	cp.Raise = r.Raise.Clone()
	cp.Clear = r.Clear.Clone()
	if r.Actions != nil {
		cp.Actions = make([]EdgeRuleAction, len(r.Actions))
		for i, a := range r.Actions {
			cp.Actions[i] = a.Clone()
		}
	}
	return cp
}

func (b *EdgeRuleBundle) Clone() *EdgeRuleBundle {
	if b == nil {
		return nil
	}
	cp := *b
	if b.Rules != nil {
		cp.Rules = make([]CompiledEdgeRule, len(b.Rules))
		for i, r := range b.Rules {
			cp.Rules[i] = r.Clone()
		}
	} else {
		cp.Rules = []CompiledEdgeRule{}
	}
	return &cp
}

func cloneEdgeFloatPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ComputeDigest calculates the canonical SHA-256 hex digest over schema, site,
// source bundle stamp, and sorted compiled edge rules.
func (b EdgeRuleBundle) ComputeDigest() (string, error) {
	type canonicalPayload struct {
		SchemaVersion  int                `json:"schemaVersion"`
		SiteID         string             `json:"siteId"`
		BundleRevision uint64             `json:"bundleRevision"`
		BundleDigest   string             `json:"bundleDigest,omitempty"`
		Rules          []CompiledEdgeRule `json:"rules"`
	}
	rules := make([]CompiledEdgeRule, len(b.Rules))
	for i, r := range b.Rules {
		cloned := r.Clone()
		sort.Strings(cloned.DependsOnRuleIDs)
		rules[i] = cloned
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].RuleID < rules[j].RuleID
	})
	encoded, err := json.Marshal(canonicalPayload{
		SchemaVersion:  b.SchemaVersion,
		SiteID:         b.SiteID,
		BundleRevision: b.BundleRevision,
		BundleDigest:   b.BundleDigest,
		Rules:          rules,
	})
	if err != nil {
		return "", fmt.Errorf("encode edge rule bundle for digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks schema, rule limits, condition invariants, acyclic DAG depth,
// and cryptographic EdgeBundleDigest integrity.
func (b EdgeRuleBundle) Validate() error {
	if b.SchemaVersion != EdgeRuleSchemaVersion {
		return fmt.Errorf("edge rule bundle schemaVersion %d must be %d", b.SchemaVersion, EdgeRuleSchemaVersion)
	}
	if !edgeUUIDPattern.MatchString(b.SiteID) {
		return errors.New("edge rule bundle siteId must be a valid UUID")
	}
	if !edgeDigestPattern.MatchString(b.EdgeBundleDigest) {
		return errors.New("edge rule bundle edgeBundleDigest must be a 64-char sha256 hex string")
	}
	if b.BundleDigest != "" && !edgeDigestPattern.MatchString(b.BundleDigest) {
		return errors.New("edge rule bundle bundleDigest must be a 64-char sha256 hex string")
	}
	if len(b.Rules) > MaxEdgeRulesPerBundle {
		return fmt.Errorf("edge rule bundle count %d exceeds maximum %d", len(b.Rules), MaxEdgeRulesPerBundle)
	}
	seenIDs := make(map[string]struct{}, len(b.Rules))
	for i, r := range b.Rules {
		if err := r.validate(); err != nil {
			return fmt.Errorf("edge rule [%d] (%s): %w", i, r.RuleID, err)
		}
		if _, dup := seenIDs[r.RuleID]; dup {
			return fmt.Errorf("duplicate edge ruleId %s", r.RuleID)
		}
		seenIDs[r.RuleID] = struct{}{}
	}
	if _, err := TopologicalOrderEdgeRules(b.Rules); err != nil {
		return err
	}
	expectedDigest, err := b.ComputeDigest()
	if err != nil {
		return err
	}
	if b.EdgeBundleDigest != expectedDigest {
		return errors.New("edge rule bundle digest mismatch")
	}
	return nil
}

func (r CompiledEdgeRule) validate() error {
	if !edgeUUIDPattern.MatchString(r.RuleID) {
		return errors.New("ruleId must be a valid UUID")
	}
	if strings.TrimSpace(r.Title) == "" || len(r.Title) > 256 {
		return errors.New("title must be non-empty and at most 256 bytes")
	}
	if r.AlarmType != "" && (strings.TrimSpace(r.AlarmType) == "" || len(r.AlarmType) > 128) {
		return errors.New("alarmType must be non-empty and at most 128 bytes")
	}
	switch r.Severity {
	case "INFO", "WARNING", "MINOR", "MAJOR", "CRITICAL":
	default:
		return fmt.Errorf("unsupported severity %q", r.Severity)
	}
	if !r.ExecutionTarget.Valid() {
		return fmt.Errorf("executionTarget %q is invalid for edge bundle", r.ExecutionTarget)
	}
	if r.QualityPolicy != EdgeQualityValidOnly && r.QualityPolicy != EdgeQualityUsable {
		return fmt.Errorf("qualityPolicy %q is invalid", r.QualityPolicy)
	}
	if r.FreshnessSeconds <= 0 || r.FreshnessSeconds > MaxEdgeRuleDurationSeconds {
		return errors.New("freshnessSeconds must be between 1 and 31536000")
	}
	switch r.TriggerMode {
	case EdgeTriggerSimple:
		if r.DurationSeconds != 0 || r.RepeatCount != 0 {
			return errors.New("SIMPLE triggerMode cannot carry durationSeconds or repeatCount")
		}
	case EdgeTriggerDuration:
		if r.DurationSeconds <= 0 || r.DurationSeconds > MaxEdgeRuleDurationSeconds || r.RepeatCount != 0 {
			return errors.New("DURATION triggerMode requires valid durationSeconds and repeatCount == 0")
		}
	case EdgeTriggerRepeating:
		if r.RepeatCount == 0 || r.DurationSeconds != 0 {
			return errors.New("REPEATING triggerMode requires repeatCount > 0 and durationSeconds == 0")
		}
	default:
		return fmt.Errorf("unsupported triggerMode %q", r.TriggerMode)
	}
	if len(r.DependsOnRuleIDs) > MaxEdgeRuleDependencies {
		return fmt.Errorf("dependsOnRuleIds exceeds maximum %d", MaxEdgeRuleDependencies)
	}
	depSeen := make(map[string]struct{}, len(r.DependsOnRuleIDs))
	for _, depID := range r.DependsOnRuleIDs {
		if !edgeUUIDPattern.MatchString(depID) || depID == r.RuleID {
			return fmt.Errorf("invalid dependency ruleId %q", depID)
		}
		if _, dup := depSeen[depID]; dup {
			return fmt.Errorf("duplicate dependency ruleId %q", depID)
		}
		depSeen[depID] = struct{}{}
	}
	switch r.DependencyGate {
	case "", EdgeDependencyGateNone:
	case EdgeDependencyGateAllActive, EdgeDependencyGateAnyActive:
		if len(r.DependsOnRuleIDs) == 0 {
			return errors.New("dependencyGate requires non-empty dependsOnRuleIds")
		}
	default:
		return fmt.Errorf("unsupported dependencyGate %q", r.DependencyGate)
	}
	if len(r.Actions) > MaxEdgeRuleActions {
		return fmt.Errorf("actions count %d exceeds maximum %d", len(r.Actions), MaxEdgeRuleActions)
	}
	for i, a := range r.Actions {
		if err := a.validate(); err != nil {
			return fmt.Errorf("action[%d]: %w", i, err)
		}
	}
	if err := r.Raise.validate(0); err != nil {
		return fmt.Errorf("raise: %w", err)
	}
	if err := r.Clear.validate(0); err != nil {
		return fmt.Errorf("clear: %w", err)
	}
	return nil
}

func (a EdgeRuleAction) validate() error {
	if a.Kind != EdgeRuleActionProtectionCommand {
		return fmt.Errorf("edge only supports PROTECTION_COMMAND actions, got %q", a.Kind)
	}
	switch a.TriggerOn {
	case "", EdgeActionTriggerRaise, EdgeActionTriggerClear, EdgeActionTriggerBoth:
	default:
		return fmt.Errorf("unsupported triggerOn %q", a.TriggerOn)
	}
	if strings.TrimSpace(a.TargetDeviceID) == "" || len(a.TargetDeviceID) > 128 {
		return errors.New("targetDeviceId is required and must be at most 128 chars")
	}
	if a.TargetPointCode != "" && !edgePointCodePattern.MatchString(a.TargetPointCode) {
		return errors.New("targetPointCode is invalid")
	}
	if strings.TrimSpace(a.Capability) == "" || len(a.Capability) > 64 {
		return errors.New("capability is required and must be at most 64 chars")
	}
	for k, v := range a.Parameters {
		if strings.TrimSpace(k) == "" || len(k) > 64 || !isFiniteFloat(v) {
			return errors.New("action parameter must have valid key and finite value")
		}
	}
	return nil
}

func validEdgeInputKey(key string) bool {
	if len(key) == 0 || len(key) > 165 || strings.ContainsAny(key, " \t\r\n") {
		return false
	}
	dev, pt, ok := strings.Cut(key, "/")
	return ok && strings.TrimSpace(dev) != "" && strings.TrimSpace(pt) != ""
}

func validEdgeCompareOperator(op EdgeCompareOperator) bool {
	switch op {
	case EdgeCompareEQ, EdgeCompareNE, EdgeCompareGT, EdgeCompareGTE, EdgeCompareLT, EdgeCompareLTE:
		return true
	default:
		return false
	}
}

func validEdgeOrderedNumericOperator(op EdgeCompareOperator) bool {
	switch op {
	case EdgeCompareGT, EdgeCompareGTE, EdgeCompareLT, EdgeCompareLTE:
		return true
	default:
		return false
	}
}

func (c EdgeCondition) validate(depth int) error {
	if depth > MaxEdgeRuleConditionDepth {
		return errors.New("edge condition exceeds maximum depth")
	}
	switch c.Kind {
	case EdgeConditionAnd, EdgeConditionOr:
		if len(c.Children) < 2 || len(c.Children) > MaxEdgeRuleConditionChildren {
			return errors.New("compound edge condition requires 2 to 32 children")
		}
		for i, ch := range c.Children {
			if err := ch.validate(depth + 1); err != nil {
				return fmt.Errorf("child[%d]: %w", i, err)
			}
		}
		return nil
	case EdgeConditionCompare:
		if !validEdgeInputKey(c.Input) || !validEdgeCompareOperator(c.Operator) || c.Value == nil || !c.Value.Valid() {
			return errors.New("compare condition is invalid")
		}
		if c.Operator != EdgeCompareEQ && c.Operator != EdgeCompareNE && c.Value.Type != EdgeValueNumber {
			return errors.New("ordered compare condition requires numeric value")
		}
		return nil
	case EdgeConditionRange:
		if !validEdgeInputKey(c.Input) || c.Minimum == nil || c.Maximum == nil || !isFiniteFloat(*c.Minimum) || !isFiniteFloat(*c.Maximum) || *c.Minimum > *c.Maximum {
			return errors.New("range condition is invalid")
		}
		return nil
	case EdgeConditionHysteresis:
		if !validEdgeInputKey(c.Input) || c.Trigger == nil || c.Reset == nil || !isFiniteFloat(*c.Trigger) || !isFiniteFloat(*c.Reset) {
			return errors.New("hysteresis condition is invalid")
		}
		switch c.Direction {
		case EdgeDirectionAbove:
			if *c.Reset >= *c.Trigger {
				return errors.New("ABOVE hysteresis requires reset < trigger")
			}
		case EdgeDirectionBelow:
			if *c.Reset <= *c.Trigger {
				return errors.New("BELOW hysteresis requires reset > trigger")
			}
		default:
			return errors.New("hysteresis direction is invalid")
		}
		return nil
	case EdgeConditionSlope:
		if !validEdgeInputKey(c.Input) || !validEdgeOrderedNumericOperator(c.Operator) || c.Delta == nil || !isFiniteFloat(*c.Delta) || c.WindowSeconds < 1 || c.WindowSeconds > MaxEdgeRuleWindowSeconds {
			return errors.New("slope condition is invalid")
		}
		return nil
	case EdgeConditionWindowAgg:
		if !validEdgeInputKey(c.Input) || !c.Agg.Valid() || !c.WindowType.Valid() ||
			c.WindowSeconds < 1 || c.WindowSeconds > MaxEdgeRuleWindowSeconds ||
			!validEdgeCompareOperator(c.Operator) || c.Threshold == nil || !isFiniteFloat(*c.Threshold) ||
			c.MinSamples < 0 || c.MinSamples > MaxEdgeRuleMinSamples {
			return errors.New("window_agg condition is invalid")
		}
		return nil
	case EdgeConditionExpression:
		if len(c.Inputs) == 0 || len(c.Inputs) > MaxEdgeExprInputs {
			return errors.New("expression inputs count is invalid")
		}
		for _, k := range c.Inputs {
			if !validEdgeInputKey(k) {
				return fmt.Errorf("expression input key %q is invalid", k)
			}
		}
		if _, err := parseAndValidateEdgeExpression(c.Expression, c.Inputs); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported edge condition kind %q", c.Kind)
	}
}

// TopologicalOrderEdgeRules validates the dependency DAG (all upstream dependencies present,
// acyclic, hop depth <= MaxEdgeRuleLinkageHops) and returns rules ordered upstream-first.
func TopologicalOrderEdgeRules(rules []CompiledEdgeRule) ([]CompiledEdgeRule, error) {
	byID := make(map[string]CompiledEdgeRule, len(rules))
	for _, r := range rules {
		if _, dup := byID[r.RuleID]; dup {
			return nil, fmt.Errorf("duplicate edge ruleId %s", r.RuleID)
		}
		byID[r.RuleID] = r
	}
	inDegree := make(map[string]int, len(rules))
	downstream := make(map[string][]string, len(rules))
	for _, r := range rules {
		seen := make(map[string]struct{}, len(r.DependsOnRuleIDs))
		for _, depID := range r.DependsOnRuleIDs {
			if depID == r.RuleID {
				return nil, fmt.Errorf("edge rule %s cannot depend on itself", r.RuleID)
			}
			if _, ok := byID[depID]; !ok {
				return nil, fmt.Errorf("edge rule %s depends on missing rule %s", r.RuleID, depID)
			}
			if _, dup := seen[depID]; dup {
				continue
			}
			seen[depID] = struct{}{}
			inDegree[r.RuleID]++
			downstream[depID] = append(downstream[depID], r.RuleID)
		}
	}
	queue := make([]string, 0, len(rules))
	depth := make(map[string]int, len(rules))
	for _, r := range rules {
		if inDegree[r.RuleID] == 0 {
			queue = append(queue, r.RuleID)
		}
	}
	sort.Strings(queue)

	ordered := make([]CompiledEdgeRule, 0, len(rules))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		ordered = append(ordered, byID[id])
		nextDepth := depth[id] + 1
		for _, childID := range downstream[id] {
			if nextDepth > depth[childID] {
				depth[childID] = nextDepth
			}
			inDegree[childID]--
			if inDegree[childID] == 0 {
				queue = append(queue, childID)
			}
		}
		sort.Strings(queue)
	}
	if len(ordered) != len(rules) {
		return nil, errors.New("edge rule dependency graph contains a cycle")
	}
	for _, r := range ordered {
		if depth[r.RuleID] > MaxEdgeRuleLinkageHops {
			return nil, fmt.Errorf("edge rule %s exceeds maximum linkage depth %d", r.RuleID, MaxEdgeRuleLinkageHops)
		}
	}
	return ordered, nil
}
