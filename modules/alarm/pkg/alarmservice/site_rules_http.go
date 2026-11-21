package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/rulegraph"
)

// SiteRuleReader reads the rules a Site evaluates now; PostgresStore is one.
type SiteRuleReader interface {
	SiteRules(ctx context.Context, tenantID, siteID string) ([]SiteRule, error)
}

// SiteRuleList is the Site's rules in effect, as the alarm page lists them (#693).
type SiteRuleList struct {
	BundleRevision uint64         `json:"bundleRevision,omitempty"`
	BundleDigest   string         `json:"bundleDigest,omitempty"`
	Items          []SiteRuleView `json:"items"`
}

// SiteRuleView is one rule in effect: what raises its Alarm, what clears it and how bad it is. Its
// conditions name each reading by device and point, for the page to put into words.
type SiteRuleView struct {
	RuleID           string                  `json:"ruleId"`
	Title            string                  `json:"title"`
	AlarmType        string                  `json:"alarmType,omitempty"`
	Summary          string                  `json:"summary,omitempty"`
	Severity         alarmmodel.Severity     `json:"severity"`
	RuleKind         RuleKind                `json:"ruleKind,omitempty"`
	ExecutionTarget  ExecutionTarget         `json:"executionTarget,omitempty"`
	TemplateKey      string                  `json:"templateKey,omitempty"`
	OverriddenFields []string                `json:"overriddenFields,omitempty"`
	DependsOnRuleIDs []string                `json:"dependsOnRuleIds,omitempty"`
	HopDepth         int                     `json:"hopDepth,omitempty"`
	DependencyGate   DependencyGateMode      `json:"dependencyGate,omitempty"`
	Actions          []RuleAction            `json:"actions,omitempty"`
	QualityPolicy    EvaluationQualityPolicy `json:"qualityPolicy,omitempty"`
	FreshnessSeconds int64                   `json:"freshnessSeconds,omitempty"`
	TriggerMode      TriggerMode             `json:"triggerMode"`
	DurationSeconds  int64                   `json:"durationSeconds,omitempty"`
	RepeatCount      uint64                  `json:"repeatCount,omitempty"`
	Raise            RuleCondition           `json:"raise"`
	Clear            RuleCondition           `json:"clear"`
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
	Agg           WindowAggFunc          `json:"agg,omitempty"`
	WindowType    WindowType             `json:"windowType,omitempty"`
	MinSamples    int                    `json:"minSamples,omitempty"`
	Threshold     *float64               `json:"threshold,omitempty"`
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
	EvaluatedAt           string                      `json:"evaluatedAt,omitempty"`
	Readings              []SimulateRuleReadingSample `json:"readings"`
	UpstreamActiveRuleIDs []string                    `json:"upstreamActiveRuleIds,omitempty"`
}

type SimulateSiteRuleRequest struct {
	RuleID         string                  `json:"ruleId"`
	RuleDraft      *SiteRuleDraft          `json:"ruleDraft,omitempty"`
	DependencyGate *DependencyGateMode     `json:"dependencyGate,omitempty"`
	Actions        *[]RuleAction           `json:"actions,omitempty"`
	Steps          []SimulateRuleStepInput `json:"steps"`
}

type SimulatedActionView struct {
	Kind      RuleActionKind  `json:"kind"`
	TriggerOn ActionTriggerOn `json:"triggerOn"`
	Target    string          `json:"target"`
	Summary   string          `json:"summary"`
}

type SiteRuleSimulationStep struct {
	Step           int                   `json:"step"`
	EvaluatedAt    string                `json:"evaluatedAt"`
	Status         EvaluationStatus      `json:"status"`
	Effect         EvaluationEffect      `json:"effect"`
	Active         bool                  `json:"active"`
	QualityBlocker string                `json:"qualityBlocker,omitempty"`
	MatchedReasons []string              `json:"matchedReasons,omitempty"`
	PlannedActions []SimulatedActionView `json:"plannedActions,omitempty"`
}

type SiteRuleSimulationResult struct {
	RuleID           string                   `json:"ruleId"`
	Title            string                   `json:"title"`
	HopDepth         int                      `json:"hopDepth"`
	DependsOnRuleIDs []string                 `json:"dependsOnRuleIds,omitempty"`
	DependencyGate   DependencyGateMode       `json:"dependencyGate,omitempty"`
	Actions          []RuleAction             `json:"actions,omitempty"`
	FinalActive      bool                     `json:"finalActive"`
	Steps            []SiteRuleSimulationStep `json:"steps"`
}

type RuleReading struct {
	DeviceID  string `json:"deviceId"`
	PointCode string `json:"pointCode"`
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
		AlarmType:        rule.Policy.AlarmType,
		Summary:          rule.Policy.Summary,
		Severity:         rule.Policy.Severity,
		RuleKind:         ruleKind,
		ExecutionTarget:  rule.Policy.ExecutionTarget,
		TemplateKey:      rule.Policy.TemplateKey,
		OverriddenFields: slices.Clone(rule.Policy.OverriddenFields),
		DependsOnRuleIDs: slices.Clone(rule.Policy.DependsOnRuleIDs),
		HopDepth:         hopDepth,
		DependencyGate:   rule.Policy.DependencyGate,
		Actions:          cloneRuleActions(rule.Policy.Actions),
		QualityPolicy:    rule.Policy.QualityPolicy,
		FreshnessSeconds: rule.Policy.FreshnessSeconds,
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
	case ConditionWindowAgg:
		if condition.Threshold == nil || math.IsNaN(*condition.Threshold) || math.IsInf(*condition.Threshold, 0) {
			return RuleCondition{}, errors.New("site rule window_agg threshold is invalid")
		}
		threshold := *condition.Threshold
		view.Agg, view.WindowType, view.WindowSeconds, view.MinSamples, view.Operator, view.Threshold =
			condition.Agg, condition.WindowType, condition.WindowSeconds, condition.MinSamples, condition.Operator, &threshold
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
	if handler.authoring != nil {
		meta, metaErr := handler.authoring.LatestSiteRuleBundleMeta(request.Context(), tenantID, siteID)
		if metaErr != nil {
			handler.writeStoreFailure(writer, metaErr)
			return
		}
		if meta.BundleRevision > 0 {
			list.BundleRevision = meta.BundleRevision
			list.BundleDigest = meta.BundleDigest
		}
	}
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
	var target SiteRule
	candidateRules := slices.Clone(rules)
	if input.RuleDraft != nil {
		if input.RuleDraft.RuleID != "" && input.RuleDraft.RuleID != input.RuleID {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "ruleDraft.ruleId must match ruleId.", false)
			return
		}
		draft := *input.RuleDraft
		draft.RuleID = input.RuleID
		history := make(map[string]SiteRule, len(rules))
		for _, r := range rules {
			history[r.AssignmentID] = r
		}
		compiled, issues := compileSingleRuleDraft(0, draft, history, map[string]bool{}, nil)
		if len(issues) > 0 {
			handler.writeProblem(writer, http.StatusUnprocessableEntity, "ALARM_RULE_DRAFT_INVALID", "Alarm rule draft invalid", issues[0].Message, false)
			return
		}
		target = compiled
		replaced := false
		for i := range candidateRules {
			if candidateRules[i].AssignmentID == input.RuleID {
				candidateRules[i] = compiled
				replaced = true
				break
			}
		}
		if !replaced {
			candidateRules = append(candidateRules, compiled)
		}
	} else {
		foundTarget := false
		for i := range rules {
			if rules[i].AssignmentID == input.RuleID {
				target = rules[i]
				foundTarget = true
				break
			}
		}
		if !foundTarget {
			handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule was not found on this Site.", false)
			return
		}
	}
	report, err := ValidateSiteRuleGraph(candidateRules)
	if err != nil {
		if input.RuleDraft != nil {
			handler.writeProblem(writer, http.StatusUnprocessableEntity, "ALARM_RULE_GRAPH_INVALID", "Alarm rule graph invalid", err.Error(), false)
			return
		}
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Site rules contain an invalid dependency graph.", false)
		return
	}
	policy, err := simulationPolicyForRule(target)
	if err != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "Stored Alarm rule cannot be simulated.", false)
		return
	}
	if input.DependencyGate != nil {
		policy.DependencyGate = *input.DependencyGate
	}
	if input.Actions != nil {
		policy.Actions = cloneRuleActions(*input.Actions)
	}
	if input.DependencyGate != nil || input.Actions != nil {
		if err := validateDependencyGate(policy.DependencyGate, policy.DependsOnRuleIDs); err != nil {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", err.Error(), false)
			return
		}
		if err := validateRuleActions(policy.Actions); err != nil {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", err.Error(), false)
			return
		}
		digest, err := AlarmPolicyDigest(policy)
		if err != nil {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation rule policy digest cannot be computed.", false)
			return
		}
		policy.Digest = digest
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
		if stepIn.Readings == nil {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Each simulation step must provide a readings array.", false)
			return
		}
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
			if !alarmmodel.IsUUIDv7(sample.DeviceID) || !validRulePointCode(sample.PointCode) {
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

		if len(stepIn.UpstreamActiveRuleIDs) > 16 {
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Each simulation step accepts at most 16 upstreamActiveRuleIds.", false)
			return
		}
		upstreamActive := make(map[string]bool, len(stepIn.UpstreamActiveRuleIDs))
		for _, depID := range stepIn.UpstreamActiveRuleIDs {
			if !alarmmodel.IsUUIDv7(depID) {
				handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Simulation upstreamActiveRuleIds must contain UUIDv7 identifiers.", false)
				return
			}
			upstreamActive[depID] = true
		}

		snapshot := EvaluationSnapshot{
			TenantID:           simTenant,
			SiteID:             siteID,
			SubjectType:        "SITE",
			SubjectID:          siteID,
			InputRevision:      fmt.Sprintf("sim-step-%d", idx+1),
			AsOf:               stepAt.Format(time.RFC3339Nano),
			Inputs:             inputs,
			CorrelationID:      simulateSyntheticCorrID,
			UpstreamRuleActive: upstreamActive,
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
		var plannedActions []SimulatedActionView
		for _, action := range policy.Actions {
			if !action.MatchesEffect(decision.Effect) {
				continue
			}
			plannedActions = append(plannedActions, SimulatedActionView{
				Kind:      action.Kind,
				TriggerOn: action.EffectiveTriggerOn(),
				Target:    action.SanitizedTargetRef(),
				Summary:   action.Summary(),
			})
		}
		stepsOut = append(stepsOut, SiteRuleSimulationStep{
			Step:           idx + 1,
			EvaluatedAt:    stepAt.Format(time.RFC3339Nano),
			Status:         decision.Status,
			Effect:         decision.Effect,
			Active:         active,
			QualityBlocker: decision.State.QualityBlocker,
			MatchedReasons: simulationStepReasons(policy, decision, active),
			PlannedActions: plannedActions,
		})
	}

	result := SiteRuleSimulationResult{
		RuleID:           target.AssignmentID,
		Title:            target.Policy.Title,
		HopDepth:         report.Nodes[target.AssignmentID].HopDepth,
		DependsOnRuleIDs: slices.Clone(target.Policy.DependsOnRuleIDs),
		DependencyGate:   policy.DependencyGate,
		Actions:          cloneRuleActions(policy.Actions),
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

func validRulePointCode(pointCode string) bool {
	return pointCode != "" && strings.TrimSpace(pointCode) == pointCode && len(pointCode) <= 128
}

func validSimulationQuality(quality string) bool {
	switch quality {
	case "GOOD", "PARTIAL", "ESTIMATED", "MANUAL", "STALE", "INVALID":
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
		case RuleKindWindow:
			reasons = append(reasons, "窗口聚合统计达标")
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

func matchSiteRuleRevisionsPath(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 6 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules" || segments[5] != "revisions" {
		return "", false
	}
	return segments[3], true
}

func matchSiteRuleRevisionRollbackPath(path string) (string, string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 7 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules" || segments[5] != "revisions" {
		return "", "", false
	}
	revText, hasSuffix := strings.CutSuffix(segments[6], ":rollback")
	if !hasSuffix || revText == "" {
		return "", "", false
	}
	return segments[3], revText, true
}

func (handler *httpHandler) recordRulePublishMetric(outcome string) {
	if handler == nil || handler.metrics == nil {
		return
	}
	_ = handler.metrics.AddCounter(
		"hvac_alarm_rule_publishes_total",
		"Site alarm rule hot-publish and rollback outcomes.",
		map[string]string{"outcome": outcome},
		1,
	)
}

func (handler *httpHandler) recordEdgeCompileMetric(outcome string) {
	if handler == nil || handler.metrics == nil {
		return
	}
	_ = handler.metrics.AddCounter(
		"hvac_alarm_edge_rule_compiles_total",
		"Site edge rule bundle compilation outcomes.",
		map[string]string{"outcome": outcome},
		1,
	)
}

func (handler *httpHandler) compileSiteRulesEdge(writer http.ResponseWriter, request *http.Request, tenantID, siteID string) {
	if handler.authoring == nil {
		handler.recordEdgeCompileMetric("failed")
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRuleBundleBodyBytes)
	rawBody, err := io.ReadAll(request.Body)
	if err != nil {
		handler.recordEdgeCompileMetric("rejected")
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The edge rule compile request body could not be read.", false)
		return
	}
	var input EdgeRuleCompileRequest
	if len(strings.TrimSpace(string(rawBody))) > 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
			handler.recordEdgeCompileMetric("rejected")
			handler.writeProblem(writer, http.StatusUnsupportedMediaType, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Edge rule compile requests must use application/json.", false)
			return
		}
		decoder := json.NewDecoder(strings.NewReader(string(rawBody)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil {
			handler.recordEdgeCompileMetric("rejected")
			handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The edge rule compile request is invalid.", false)
			return
		}
	}
	result, err := handler.authoring.CompileSiteRulesEdge(request.Context(), tenantID, siteID, input.Rules, handler.now().UTC())
	if err != nil {
		var draftErr *RuleDraftValidationError
		if errors.As(err, &draftErr) {
			handler.recordEdgeCompileMetric("rejected")
			handler.writeProblem(writer, http.StatusUnprocessableEntity, draftErr.Code, "Alarm rule draft invalid", draftErr.Error(), false)
			return
		}
		handler.recordEdgeCompileMetric("failed")
		handler.writeStoreFailure(writer, err)
		return
	}
	handler.recordEdgeCompileMetric("success")
	writeJSON(writer, http.StatusOK, result)
}

func (handler *httpHandler) diffSiteRules(writer http.ResponseWriter, request *http.Request, tenantID, siteID string) {
	if handler.authoring == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		handler.writeProblem(writer, http.StatusUnsupportedMediaType, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Alarm rule diff requests must use application/json.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRuleBundleBodyBytes)
	var input SiteRuleDiffRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || input.Rules == nil {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm rule diff request is invalid.", false)
		return
	}
	result, err := handler.authoring.DiffSiteRules(request.Context(), tenantID, siteID, input.Rules)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *httpHandler) publishSiteRules(writer http.ResponseWriter, request *http.Request, tenantID, siteID string, claims identitycontext.DelegationClaims) {
	if handler.authoring == nil {
		handler.recordRulePublishMetric("failed")
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusUnsupportedMediaType, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Alarm rule publish requests must use application/json.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRuleBundleBodyBytes)
	var input SiteRulePublishRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || input.Rules == nil {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm rule publish request is invalid.", false)
		return
	}
	result, err := handler.authoring.PublishSiteRules(request.Context(), tenantID, siteID, input, claims.Subject, claims.DisplayName, handler.now().UTC())
	if err != nil {
		handler.writeAuthoringMutationFailure(writer, err)
		return
	}
	handler.recordRulePublishMetric("success")
	writeJSON(writer, http.StatusOK, result)
}

func (handler *httpHandler) listSiteRuleRevisions(writer http.ResponseWriter, request *http.Request, tenantID, siteID string) {
	if handler.authoring == nil {
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	limit, ok := parseQueryLimit(request, defaultRuleBundleRevisionLimit, maxRuleBundleRevisionLimit)
	if !ok {
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_FILTER_INVALID", "Alarm filter invalid", "The Alarm rule revision query exceeds the supported read boundary.", false)
		return
	}
	result, err := handler.authoring.ListSiteRuleBundleRevisions(request.Context(), tenantID, siteID, limit)
	if err != nil {
		handler.writeStoreFailure(writer, err)
		return
	}
	if result.Items == nil {
		result.Items = []SiteRuleBundleRevisionView{}
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *httpHandler) rollbackSiteRules(writer http.ResponseWriter, request *http.Request, tenantID, siteID, revText string, claims identitycontext.DelegationClaims) {
	if handler.authoring == nil {
		handler.recordRulePublishMetric("failed")
		handler.writeStoreFailure(writer, ErrUnavailable)
		return
	}
	targetRevision, revErr := strconv.ParseUint(revText, 10, 64)
	if revErr != nil || targetRevision == 0 || targetRevision > math.MaxInt64 {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule bundle revision was not found.", false)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusUnsupportedMediaType, "ALARM_REQUEST_INVALID", "Alarm request invalid", "Alarm rule rollback requests must use application/json.", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumMutationBody)
	var input SiteRuleRollbackRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusBadRequest, "ALARM_REQUEST_INVALID", "Alarm request invalid", "The Alarm rule rollback request is invalid.", false)
		return
	}
	result, err := handler.authoring.RollbackSiteRules(request.Context(), tenantID, siteID, targetRevision, input, claims.Subject, claims.DisplayName, handler.now().UTC())
	if err != nil {
		handler.writeAuthoringMutationFailure(writer, err)
		return
	}
	handler.recordRulePublishMetric("success")
	writeJSON(writer, http.StatusOK, result)
}

func (handler *httpHandler) writeAuthoringMutationFailure(writer http.ResponseWriter, err error) {
	var conflictErr *RuleBundleConflictError
	if errors.As(err, &conflictErr) {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusConflict, "ALARM_RULE_BUNDLE_CONFLICT", "Alarm rule bundle revision conflict", conflictErr.Error(), false)
		return
	}
	var draftErr *RuleDraftValidationError
	if errors.As(err, &draftErr) {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusUnprocessableEntity, draftErr.Code, "Alarm rule draft invalid", draftErr.Error(), false)
		return
	}
	if errors.Is(err, ErrNotFound) {
		handler.recordRulePublishMetric("rejected")
		handler.writeProblem(writer, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", "The requested Alarm rule bundle revision was not found.", false)
		return
	}
	handler.recordRulePublishMetric("failed")
	handler.writeStoreFailure(writer, err)
}

func matchSiteRuleSubpath(path, leaf string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != leaf {
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
		if rule.AlarmType != "" && !boundedText(rule.AlarmType, 128) {
			return errors.New("site rule alarmType is invalid")
		}
		if rule.Summary != "" && !boundedText(rule.Summary, 2048) {
			return errors.New("site rule summary is invalid")
		}
		if rule.QualityPolicy != "" && rule.QualityPolicy != EvaluationQualityValidOnly && rule.QualityPolicy != EvaluationQualityUsable {
			return errors.New("site rule qualityPolicy is invalid")
		}
		if rule.FreshnessSeconds < 0 {
			return errors.New("site rule freshnessSeconds is invalid")
		}
		if rule.RuleKind != "" && !validRuleKind(rule.RuleKind) {
			return errors.New("site rule kind is invalid")
		}
		if rule.ExecutionTarget != "" && !validExecutionTarget(rule.ExecutionTarget) {
			return errors.New("site rule executionTarget is invalid")
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
		if err := validateRuleDependencies(rule.RuleID, rule.DependsOnRuleIDs); err != nil {
			return err
		}
		if rule.HopDepth < 0 || rule.HopDepth > rulegraph.MaxLinkageHops {
			return errors.New("site rule hopDepth is invalid")
		}
		if err := validateDependencyGate(rule.DependencyGate, rule.DependsOnRuleIDs); err != nil {
			return err
		}
		if err := validateRuleActions(rule.Actions); err != nil {
			return err
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
	if err := validateRuleDependencies(result.RuleID, result.DependsOnRuleIDs); err != nil {
		return err
	}
	if err := validateDependencyGate(result.DependencyGate, result.DependsOnRuleIDs); err != nil {
		return err
	}
	if err := validateRuleActions(result.Actions); err != nil {
		return err
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
		for _, planned := range step.PlannedActions {
			if err := planned.validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (action SimulatedActionView) validate() error {
	switch action.Kind {
	case RuleActionWebhook, RuleActionWorkOrder, RuleActionProtectionCommand:
	default:
		return errors.New("site rule simulation planned action kind is invalid")
	}
	switch action.TriggerOn {
	case ActionTriggerRaise, ActionTriggerClear, ActionTriggerBoth:
	default:
		return errors.New("site rule simulation planned action triggerOn is invalid")
	}
	if !boundedText(action.Target, 256) || !boundedText(action.Summary, 512) {
		return errors.New("site rule simulation planned action summary is invalid")
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
			if !alarmmodel.IsUUIDv7(reading.DeviceID) || !validRulePointCode(reading.PointCode) {
				return errors.New("site rule condition reading is invalid")
			}
			exprInputs[alias] = reading.DeviceID + "/" + reading.PointCode
		}
		if err := ValidateExpression(condition.Expression, exprInputs); err != nil {
			return errors.New("site rule expression condition is invalid")
		}
		return nil
	}
	if condition.Reading == nil || !alarmmodel.IsUUIDv7(condition.Reading.DeviceID) || !validRulePointCode(condition.Reading.PointCode) || len(condition.Children) != 0 {
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
		valid = condition.AgeSeconds > 0 && condition.AgeSeconds <= maxRuleDurationSeconds
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
	case ConditionWindowAgg:
		valid = validWindowAggFunc(condition.Agg) &&
			validWindowType(condition.WindowType) &&
			condition.WindowSeconds >= 1 && condition.WindowSeconds <= 86400 &&
			validCompareOperator(condition.Operator) &&
			condition.Threshold != nil && !math.IsNaN(*condition.Threshold) && !math.IsInf(*condition.Threshold, 0) &&
			condition.MinSamples >= 0 && condition.MinSamples <= MaxWindowSamplesPerInput
	}
	if !valid {
		return errors.New("site rule condition is invalid")
	}
	return nil
}
