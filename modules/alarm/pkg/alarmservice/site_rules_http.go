package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/rulegraph"
)

// SiteRuleReader reads the rules a Site evaluates now; PostgresStore is one.
type SiteRuleReader interface {
	SiteRules(ctx context.Context, tenantID, siteID string) ([]SiteRule, error)
}

// SiteRuleList is the Site's rules in effect, as the alarm page lists them (#693).
type SiteRuleList struct {
	Items []SiteRuleView `json:"items"`
}

// SiteRuleView is one rule in effect: what raises its Alarm, what clears it and how bad it is. Its
// conditions name each reading by device and point, for the page to put into words.
type SiteRuleView struct {
	RuleID           string              `json:"ruleId"`
	Title            string              `json:"title"`
	Severity         alarmmodel.Severity `json:"severity"`
	RuleKind         RuleKind            `json:"ruleKind,omitempty"`
	TemplateKey      string              `json:"templateKey,omitempty"`
	OverriddenFields []string            `json:"overriddenFields,omitempty"`
	DependsOnRuleIDs []string            `json:"dependsOnRuleIds,omitempty"`
	HopDepth         int                 `json:"hopDepth,omitempty"`
	TriggerMode      TriggerMode         `json:"triggerMode"`
	DurationSeconds  int64               `json:"durationSeconds,omitempty"`
	RepeatCount      uint64              `json:"repeatCount,omitempty"`
	Raise            RuleCondition       `json:"raise"`
	Clear            RuleCondition       `json:"clear"`
}

// RuleCondition is a Condition with its reading split into device and point and its value a plain
// JSON number, string or boolean.
type RuleCondition struct {
	Kind          ConditionKind          `json:"kind"`
	Reading       *RuleReading           `json:"reading,omitempty"`
	Operator      CompareOperator        `json:"operator,omitempty"`
	Value         any                    `json:"value,omitempty"`
	Minimum       *float64               `json:"minimum,omitempty"`
	Maximum       *float64               `json:"maximum,omitempty"`
	Direction     HysteresisDirection    `json:"direction,omitempty"`
	Trigger       *float64               `json:"trigger,omitempty"`
	Reset         *float64               `json:"reset,omitempty"`
	AgeSeconds    int64                  `json:"ageSeconds,omitempty"`
	Delta         *float64               `json:"delta,omitempty"`
	WindowSeconds int64                  `json:"windowSeconds,omitempty"`
	Expression    string                 `json:"expression,omitempty"`
	Inputs        map[string]RuleReading `json:"inputs,omitempty"`
	Children      []RuleCondition        `json:"children,omitempty"`
	Reasons       []string               `json:"reasons,omitempty"`
	Match         string                 `json:"match,omitempty"`
}

const (
	maxSimulateSteps          = 32
	maxSimulateStepReadings   = 64
	maxSimulateBodyBytes      = int64(64 << 10)
	simulateSyntheticCorrID   = "01900000-0000-7000-8000-000000000002"
	simulateSyntheticTenantID = "01900000-0000-7000-8000-000000000001"
)

type SimulateRuleReadingSample struct {
	DeviceID   string `json:"deviceId"`
	PointCode  string `json:"pointCode"`
	Value      any    `json:"value"`
	Quality    string `json:"quality,omitempty"`
	ObservedAt string `json:"observedAt,omitempty"`
}

type SimulateRuleStepInput struct {
	EvaluatedAt string                      `json:"evaluatedAt,omitempty"`
	Readings    []SimulateRuleReadingSample `json:"readings"`
}

type SimulateSiteRuleRequest struct {
	RuleID string                  `json:"ruleId"`
	Steps  []SimulateRuleStepInput `json:"steps"`
}

type SiteRuleSimulationStep struct {
	Step           int              `json:"step"`
	EvaluatedAt    string           `json:"evaluatedAt"`
	Status         EvaluationStatus `json:"status"`
	Effect         EvaluationEffect `json:"effect"`
	Active         bool             `json:"active"`
	QualityBlocker string           `json:"qualityBlocker,omitempty"`
	MatchedReasons []string         `json:"matchedReasons,omitempty"`
}

type SiteRuleSimulationResult struct {
	RuleID           string                   `json:"ruleId"`
	Title            string                   `json:"title"`
	HopDepth         int                      `json:"hopDepth"`
	DependsOnRuleIDs []string                 `json:"dependsOnRuleIds,omitempty"`
	FinalActive      bool                     `json:"finalActive"`
	Steps            []SiteRuleSimulationStep `json:"steps"`
}

type RuleReading struct {
	DeviceID  string `json:"deviceId"`
	PointCode string `json:"pointCode"`
}

func siteRuleView(rule SiteRule) (SiteRuleView, error) {
	return siteRuleViewWithHopDepth(rule, 0)
}

func siteRuleViewWithHopDepth(rule SiteRule, hopDepth int) (SiteRuleView, error) {
	raise, err := ruleCondition(rule.Policy.Raise)
	if err != nil {
		return SiteRuleView{}, err
	}
	clear, err := ruleCondition(rule.Policy.Clear)
	if err != nil {
		return SiteRuleView{}, err
	}
	ruleKind := rule.Policy.RuleKind
	if ruleKind == "" {
		if effective := rule.Policy.EffectiveRuleKind(); effective != RuleKindThreshold {
			ruleKind = effective
		}
	}
	return SiteRuleView{
		RuleID:           rule.AssignmentID,
		Title:            rule.Policy.Title,
		Severity:         rule.Policy.Severity,
		RuleKind:         ruleKind,
		TemplateKey:      rule.Policy.TemplateKey,
		OverriddenFields: slices.Clone(rule.Policy.OverriddenFields),
		DependsOnRuleIDs: slices.Clone(rule.Policy.DependsOnRuleIDs),
		HopDepth:         hopDepth,
		TriggerMode:      rule.Policy.TriggerMode,
		DurationSeconds:  rule.Policy.DurationSeconds,
		RepeatCount:      rule.Policy.RepeatCount,
		Raise:            raise,
		Clear:            clear,
	}, nil
}

func ruleCondition(condition Condition) (RuleCondition, error) {
	view := RuleCondition{Kind: condition.Kind, Minimum: condition.Minimum, Maximum: condition.Maximum, AgeSeconds: condition.AgeSeconds, Reasons: condition.Reasons, Match: condition.Match}
	switch condition.Kind {
	case ConditionAnd, ConditionOr:
		for _, child := range condition.Children {
			childView, err := ruleCondition(child)
			if err != nil {
				return RuleCondition{}, err
			}
			view.Children = append(view.Children, childView)
		}
		return view, nil
	case ConditionCompare:
		view.Operator = condition.Operator
		switch condition.Value.Type {
		case EvaluationValueNumber:
			view.Value = condition.Value.Number
		case EvaluationValueString:
			view.Value = condition.Value.String
		case EvaluationValueBoolean:
			view.Value = condition.Value.Boolean
		}
	case ConditionHysteresis:
		trigger, reset := condition.Trigger, condition.Reset
		view.Direction, view.Trigger, view.Reset = condition.Direction, &trigger, &reset
	case ConditionSlope:
		if condition.Delta == nil || math.IsNaN(*condition.Delta) || math.IsInf(*condition.Delta, 0) {
			return RuleCondition{}, errors.New("site rule slope delta is invalid")
		}
		delta := *condition.Delta
		view.Operator, view.Delta, view.WindowSeconds = condition.Operator, &delta, condition.WindowSeconds
	case ConditionExpression:
		view.Expression = condition.Expression
		if len(condition.ExprInputs) > 0 {
			view.Inputs = make(map[string]RuleReading, len(condition.ExprInputs))
			for alias, rawInput := range condition.ExprInputs {
				deviceID, pointCode, ok := strings.Cut(rawInput, "/")
				if !ok || !alarmmodel.IsUUIDv7(deviceID) || !boundedText(pointCode, 128) {
					return RuleCondition{}, errors.New("site rule expression reads an input that is not a device reading")
				}
				view.Inputs[alias] = RuleReading{DeviceID: deviceID, PointCode: pointCode}
			}
		}
		return view, nil
	}
	// A Site rule reads "<device id>/<point code>" (siteInputKey).
	deviceID, pointCode, ok := strings.Cut(condition.Input, "/")
	if !ok || !alarmmodel.IsUUIDv7(deviceID) || pointCode == "" {
		return RuleCondition{}, errors.New("site rule reads an input that is not a device reading")
	}
	view.Reading = &RuleReading{DeviceID: deviceID, PointCode: pointCode}
	return view, nil
}

func (handler *httpHandler) siteRules(writer http.ResponseWriter, request *http.Request, tenantID, siteID string) {
	rules, err := handler.rules.SiteRules(request.Context(), tenantID, siteID)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	report, err := ValidateSiteRuleGraph(rules)
	if err != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Site rules contain an invalid dependency graph.", false)
		return
	}
	list := SiteRuleList{Items: make([]SiteRuleView, 0, len(rules))}
	for _, rule := range rules {
		view, err := siteRuleViewWithHopDepth(rule, report.Nodes[rule.AssignmentID].HopDepth)
		if err != nil {
			handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "A Site rule cannot be listed as stored.", false)
			return
		}
		list.Items = append(list.Items, view)
	}
	writeJSON(writer, http.StatusOK, list)
}

func (handler *httpHandler) simulateSiteRule(writer http.ResponseWriter, request *http.Request, tenantID, siteID string) {
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		handler.writeProblem(writer, http.StatusUnsupportedMediaType, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Alarm rule simulation requests must use application/json.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxSimulateBodyBytes)
	var input SimulateSiteRuleRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm rule simulation request is invalid.", false)
		return
	}
	if !alarmmodel.IsUUIDv7(input.RuleID) || len(input.Steps) == 0 || len(input.Steps) > maxSimulateSteps {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm rule simulation request must name a ruleId and 1 to 32 steps.", false)
		return
	}
	rules, err := handler.rules.SiteRules(request.Context(), tenantID, siteID)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	var target *SiteRule
	for i := range rules {
		if rules[i].AssignmentID == input.RuleID {
			target = &rules[i]
			break
		}
	}
	if target == nil {
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule was not found on this Site.", false)
		return
	}
	report, err := ValidateSiteRuleGraph(rules)
	if err != nil {
		handler.writeProblem(writer, http.StatusBadRequest, "RULE_GRAPH_INVALID", "Rule dependency graph invalid", fmt.Sprintf("Site rule causal DAG validation failed: %v", err), false)
		return
	}
	policy, err := simulationPolicyForRule(*target)
	if err != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Stored Alarm rule cannot be simulated.", false)
		return
	}

	baseNow := handler.now().UTC()
	var prevStepAt time.Time
	var state AlarmEvaluationState
	simTenant := tenantID
	if !alarmmodel.IsUUIDv7(simTenant) {
		simTenant = simulateSyntheticTenantID
	}
	stepsOut := make([]SiteRuleSimulationStep, 0, len(input.Steps))
	for idx, stepIn := range input.Steps {
		if len(stepIn.Readings) > maxSimulateStepReadings {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Each simulation step accepts at most 64 readings.", false)
			return
		}
		stepAt := baseNow.Add(time.Duration(idx) * time.Minute)
		if rawAt := strings.TrimSpace(stepIn.EvaluatedAt); rawAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, rawAt)
			if err != nil {
				handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation evaluatedAt must be RFC3339.", false)
				return
			}
			stepAt = parsed.UTC()
		}
		if idx > 0 && stepAt.Before(prevStepAt) {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation step timestamps must be non-decreasing.", false)
			return
		}
		prevStepAt = stepAt

		inputs := make(map[string]InputFact, len(stepIn.Readings))
		for _, sample := range stepIn.Readings {
			if !alarmmodel.IsUUIDv7(sample.DeviceID) || !boundedText(sample.PointCode, 128) {
				handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation reading deviceId or pointCode is invalid.", false)
				return
			}
			key := sample.DeviceID + "/" + sample.PointCode
			if _, duplicate := inputs[key]; duplicate {
				handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation step contains duplicate device reading.", false)
				return
			}
			typedVal, ok := simulationTypedValue(sample.Value)
			if !ok {
				handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation reading value must be a finite number, string, or boolean.", false)
				return
			}
			quality := strings.TrimSpace(sample.Quality)
			if quality == "" {
				quality = "GOOD"
			}
			if !validSimulationQuality(quality) {
				handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation reading quality is unsupported.", false)
				return
			}
			observedAt := stepAt.Format(time.RFC3339Nano)
			if rawObs := strings.TrimSpace(sample.ObservedAt); rawObs != "" {
				parsedObs, err := time.Parse(time.RFC3339Nano, rawObs)
				if err != nil {
					handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation reading observedAt must be RFC3339.", false)
					return
				}
				observedAt = parsedObs.UTC().Format(time.RFC3339Nano)
			}
			inputs[key] = InputFact{
				Key:        key,
				Present:    true,
				Value:      typedVal,
				Quality:    quality,
				ObservedAt: observedAt,
				Evidence:   []alarmmodel.EvidenceReference{},
			}
		}

		snapshot := EvaluationSnapshot{
			TenantID:      simTenant,
			SiteID:        siteID,
			SubjectType:   "SITE",
			SubjectID:     siteID,
			InputRevision: fmt.Sprintf("sim-step-%d", idx+1),
			AsOf:          stepAt.Format(time.RFC3339Nano),
			Inputs:        inputs,
			CorrelationID: simulateSyntheticCorrID,
		}
		decision, err := EvaluatePolicy(policy, snapshot, state, stepAt)
		if err != nil {
			handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Stored Alarm rule failed evaluation during dry-run simulation.", false)
			return
		}
		state = decision.State
		switch decision.Effect {
		case EvaluationEffectPublish:
			state.ActiveIncidentCorrelationID = simulateSyntheticCorrID
		case EvaluationEffectClear:
			state.ActiveIncidentCorrelationID = ""
		}
		active := state.ActiveIncidentCorrelationID != ""
		stepsOut = append(stepsOut, SiteRuleSimulationStep{
			Step:           idx + 1,
			EvaluatedAt:    stepAt.Format(time.RFC3339Nano),
			Status:         decision.Status,
			Effect:         decision.Effect,
			Active:         active,
			QualityBlocker: decision.State.QualityBlocker,
			MatchedReasons: simulationStepReasons(policy, decision, active),
		})
	}

	result := SiteRuleSimulationResult{
		RuleID:           target.AssignmentID,
		Title:            target.Policy.Title,
		HopDepth:         report.Nodes[target.AssignmentID].HopDepth,
		DependsOnRuleIDs: slices.Clone(target.Policy.DependsOnRuleIDs),
		FinalActive:      state.ActiveIncidentCorrelationID != "",
		Steps:            stepsOut,
	}
	writeJSON(writer, http.StatusOK, result)
}

func simulationPolicyForRule(rule SiteRule) (AlarmPolicyRevision, error) {
	policy := rule.Policy
	if policy.SchemaVersion == 0 {
		policy.SchemaVersion = AlarmPolicySchemaVersion
	}
	if policy.PolicyID == "" {
		policy.PolicyID = rule.AssignmentID
	}
	if policy.PolicyRevisionID == "" {
		policy.PolicyRevisionID = rule.AssignmentID
	}
	if policy.Revision == 0 {
		policy.Revision = 1
	}
	if policy.AlarmType == "" {
		policy.AlarmType = "SITE_RULE_SIMULATION"
	}
	if policy.SourceType == "" {
		policy.SourceType = alarmmodel.SourceSiteRule
	}
	if policy.SourceReference == "" {
		policy.SourceReference = "alarm-rules:" + rule.AssignmentID
	}
	if policy.Summary == "" {
		policy.Summary = policy.Title
	}
	if policy.QualityPolicy == "" {
		policy.QualityPolicy = EvaluationQualityValidOnly
	}
	if policy.FreshnessSeconds <= 0 {
		policy.FreshnessSeconds = 900
	}
	if policy.Digest == "" {
		digest, err := AlarmPolicyDigest(policy)
		if err != nil {
			return AlarmPolicyRevision{}, err
		}
		policy.Digest = digest
	}
	return policy, nil
}

func simulationTypedValue(raw any) (TypedValue, bool) {
	switch value := raw.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return TypedValue{}, false
		}
		return NumberValue(value), true
	case string:
		if len(value) > 256 {
			return TypedValue{}, false
		}
		return StringValue(value), true
	case bool:
		return BooleanValue(value), true
	default:
		return TypedValue{}, false
	}
}

func validSimulationQuality(quality string) bool {
	switch quality {
	case "GOOD", "UNCERTAIN", "SUSPECT", "STALE", "BAD", "INVALID":
		return true
	default:
		return false
	}
}

func simulationStepReasons(policy AlarmPolicyRevision, decision EvaluationDecision, active bool) []string {
	if decision.State.QualityBlocker != "" {
		return nil
	}
	var reasons []string
	switch decision.Effect {
	case EvaluationEffectPublish:
		reasons = append(reasons, "达到触发条件")
		switch policy.TriggerMode {
		case TriggerDuration:
			reasons = append(reasons, "持续时长达标")
		case TriggerRepeating:
			reasons = append(reasons, "连续重复次数达标")
		}
		switch policy.EffectiveRuleKind() {
		case RuleKindSlope:
			reasons = append(reasons, "斜率变化达标")
		case RuleKindExpression:
			reasons = append(reasons, "表达式条件成立")
		}
	case EvaluationEffectClear:
		reasons = append(reasons, "满足恢复条件")
	default:
		switch {
		case active && decision.Status == EvaluationMatched:
			reasons = append(reasons, "告警保持活动")
		case decision.State.CandidateSince != nil:
			reasons = append(reasons, "已满足瞬时条件，正在持续计时")
		case policy.TriggerMode == TriggerRepeating && decision.State.RepeatCount > 0 && !active:
			reasons = append(reasons, fmt.Sprintf("已连续命中 %d / %d 次", decision.State.RepeatCount, policy.RepeatCount))
		case decision.Status == EvaluationNotMatched:
			reasons = append(reasons, "未触发告警条件")
		}
	}
	return reasons
}

// matchSiteRulesPath matches /internal/v1/sites/{siteId}/alarm-rules.
func matchSiteRulesPath(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules" {
		return "", false
	}
	return segments[3], true
}

// matchSiteRulesSimulatePath matches /internal/v1/sites/{siteId}/alarm-rules:simulate.
func matchSiteRulesSimulatePath(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules:simulate" {
		return "", false
	}
	return segments[3], true
}

// Validate is what the Gateway checks before it passes the list on: every rule names itself, its
// severity and a well-formed raise and clear condition over device readings.
func (list SiteRuleList) Validate() error {
	if list.Items == nil {
		return errors.New("site rule list has no items")
	}
	for _, rule := range list.Items {
		if !alarmmodel.IsUUIDv7(rule.RuleID) || !boundedText(rule.Title, 256) || !validAlarmSeverity(rule.Severity) {
			return errors.New("site rule identity is invalid")
		}
		if rule.RuleKind != "" && !validRuleKind(rule.RuleKind) {
			return errors.New("site rule kind is invalid")
		}
		if rule.TemplateKey != "" && !boundedText(rule.TemplateKey, 128) {
			return errors.New("site rule templateKey is invalid")
		}
		if len(rule.OverriddenFields) > 32 {
			return errors.New("site rule overriddenFields exceed maximum")
		}
		for _, field := range rule.OverriddenFields {
			if !boundedText(field, 128) {
				return errors.New("site rule overridden field is invalid")
			}
		}
		if len(rule.DependsOnRuleIDs) > 16 {
			return errors.New("site rule dependsOnRuleIds exceed maximum")
		}
		seenDeps := make(map[string]struct{}, len(rule.DependsOnRuleIDs))
		for _, depID := range rule.DependsOnRuleIDs {
			if !alarmmodel.IsUUIDv7(depID) || depID == rule.RuleID {
				return errors.New("site rule dependency ruleId is invalid")
			}
			if _, duplicate := seenDeps[depID]; duplicate {
				return errors.New("site rule dependency ruleId is duplicated")
			}
			seenDeps[depID] = struct{}{}
		}
		if rule.HopDepth < 0 || rule.HopDepth > rulegraph.MaxLinkageHops {
			return errors.New("site rule hopDepth is invalid")
		}
		switch {
		case rule.TriggerMode == TriggerSimple && rule.DurationSeconds == 0 && rule.RepeatCount == 0:
		case rule.TriggerMode == TriggerDuration && rule.DurationSeconds > 0 && rule.RepeatCount == 0:
		case rule.TriggerMode == TriggerRepeating && rule.RepeatCount > 0 && rule.DurationSeconds == 0:
		default:
			return errors.New("site rule trigger is invalid")
		}
		if err := rule.Raise.validate(); err != nil {
			return err
		}
		if err := rule.Clear.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (result SiteRuleSimulationResult) Validate(expectedRuleID string) error {
	if !alarmmodel.IsUUIDv7(result.RuleID) || (expectedRuleID != "" && result.RuleID != expectedRuleID) || !boundedText(result.Title, 256) {
		return errors.New("site rule simulation identity is invalid")
	}
	if result.HopDepth < 0 || result.HopDepth > rulegraph.MaxLinkageHops {
		return errors.New("site rule simulation hopDepth is invalid")
	}
	if len(result.DependsOnRuleIDs) > 16 {
		return errors.New("site rule simulation dependencies exceed maximum")
	}
	for _, depID := range result.DependsOnRuleIDs {
		if !alarmmodel.IsUUIDv7(depID) || depID == result.RuleID {
			return errors.New("site rule simulation dependency is invalid")
		}
	}
	if len(result.Steps) == 0 || len(result.Steps) > maxSimulateSteps {
		return errors.New("site rule simulation steps out of bounds")
	}
	for idx, step := range result.Steps {
		if step.Step != idx+1 {
			return errors.New("site rule simulation step numbering is non-sequential")
		}
		if _, err := time.Parse(time.RFC3339Nano, step.EvaluatedAt); err != nil {
			return errors.New("site rule simulation step timestamp is invalid")
		}
		switch step.Status {
		case EvaluationMatched, EvaluationNotMatched, EvaluationIndeterminate:
		default:
			return errors.New("site rule simulation step status is invalid")
		}
		switch step.Effect {
		case EvaluationEffectNone, EvaluationEffectPublish, EvaluationEffectClear:
		default:
			return errors.New("site rule simulation step effect is invalid")
		}
	}
	return nil
}

func (condition RuleCondition) validate() error {
	if condition.Kind == ConditionAnd || condition.Kind == ConditionOr {
		if condition.Reading != nil || len(condition.Children) < 2 || len(condition.Children) > 32 {
			return errors.New("site rule compound condition is invalid")
		}
		for _, child := range condition.Children {
			if err := child.validate(); err != nil {
				return err
			}
		}
		return nil
	}
	if condition.Kind == ConditionExpression {
		if condition.Reading != nil || len(condition.Children) != 0 || len(condition.Inputs) > MaxExpressionInputs {
			return errors.New("site rule expression condition is invalid")
		}
		exprInputs := make(map[string]string, len(condition.Inputs))
		for alias, reading := range condition.Inputs {
			if !alarmmodel.IsUUIDv7(reading.DeviceID) || !boundedText(reading.PointCode, 128) {
				return errors.New("site rule condition reading is invalid")
			}
			exprInputs[alias] = reading.DeviceID + "/" + reading.PointCode
		}
		if err := ValidateExpression(condition.Expression, exprInputs); err != nil {
			return errors.New("site rule expression condition is invalid")
		}
		return nil
	}
	if condition.Reading == nil || !alarmmodel.IsUUIDv7(condition.Reading.DeviceID) || !boundedText(condition.Reading.PointCode, 128) || len(condition.Children) != 0 {
		return errors.New("site rule condition reading is invalid")
	}
	valid := false
	switch condition.Kind {
	case ConditionCompare:
		switch condition.Value.(type) {
		case float64:
			valid = validCompareOperator(condition.Operator)
		case string, bool:
			valid = condition.Operator == CompareEQ || condition.Operator == CompareNE
		}
	case ConditionRange:
		valid = condition.Minimum != nil && condition.Maximum != nil && *condition.Minimum <= *condition.Maximum
	case ConditionHysteresis:
		valid = condition.Trigger != nil && condition.Reset != nil &&
			(condition.Direction == DirectionAbove && *condition.Reset < *condition.Trigger || condition.Direction == DirectionBelow && *condition.Reset > *condition.Trigger)
	case ConditionNoData, ConditionStale, ConditionUnchanged, ConditionChanged:
		valid = condition.AgeSeconds > 0
	case ConditionReasons:
		if (condition.Match == ReasonMatchAny || condition.Match == ReasonMatchNone) && len(condition.Reasons) >= 1 && len(condition.Reasons) <= 8 {
			valid = true
			for _, reason := range condition.Reasons {
				if !validConditionReason(reason) {
					valid = false
					break
				}
			}
		}
	case ConditionSlope:
		valid = validSlopeOperator(condition.Operator) && condition.Delta != nil && !math.IsNaN(*condition.Delta) && !math.IsInf(*condition.Delta, 0) && condition.WindowSeconds >= 1 && condition.WindowSeconds <= 86400
	}
	if !valid {
		return errors.New("site rule condition is invalid")
	}
	return nil
}
