package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const (
	TemplateStatusActive   = "ACTIVE"
	TemplateStatusArchived = "ARCHIVED"

	maxTemplateBoundSites     = 128
	maxTemplateReleaseListLim = 50
)

var (
	// ErrRuleTemplateConflict reports duplicate template keys or concurrent rollout conflicts.
	ErrRuleTemplateConflict = errors.New("alarm rule template conflict")
	// ErrRuleTemplateInvalid reports malformed baseline template specification payloads.
	ErrRuleTemplateInvalid = errors.New("alarm rule template is invalid")
)

// RuleTemplateValidationError carries a structured client error code for template authoring failures.
type RuleTemplateValidationError struct {
	Code    string
	Message string
}

func (err *RuleTemplateValidationError) Error() string {
	return err.Message
}

func (err *RuleTemplateValidationError) Is(target error) bool {
	return target == ErrRuleTemplateInvalid
}

// RuleTemplateCondition is a device-agnostic condition AST inside a baseline rule template.
type RuleTemplateCondition struct {
	Kind          ConditionKind           `json:"kind"`
	PointCode     string                  `json:"pointCode,omitempty"`
	Operator      CompareOperator         `json:"operator,omitempty"`
	Value         any                     `json:"value,omitempty"`
	Minimum       *float64                `json:"minimum,omitempty"`
	Maximum       *float64                `json:"maximum,omitempty"`
	Direction     HysteresisDirection     `json:"direction,omitempty"`
	Trigger       *float64                `json:"trigger,omitempty"`
	Reset         *float64                `json:"reset,omitempty"`
	AgeSeconds    int64                   `json:"ageSeconds,omitempty"`
	Delta         *float64                `json:"delta,omitempty"`
	WindowSeconds int64                   `json:"windowSeconds,omitempty"`
	Agg           WindowAggFunc           `json:"agg,omitempty"`
	WindowType    WindowType              `json:"windowType,omitempty"`
	MinSamples    int                     `json:"minSamples,omitempty"`
	Threshold     *float64                `json:"threshold,omitempty"`
	Expression    string                  `json:"expression,omitempty"`
	Inputs        map[string]string       `json:"inputs,omitempty"`
	Children      []RuleTemplateCondition `json:"children,omitempty"`
	Reasons       []string                `json:"reasons,omitempty"`
	Match         string                  `json:"match,omitempty"`
}

// RuleTemplateSpec holds the versioned default policy definition of a baseline alarm template.
type RuleTemplateSpec struct {
	Title            string                  `json:"title"`
	Summary          string                  `json:"summary,omitempty"`
	AlarmType        string                  `json:"alarmType,omitempty"`
	Severity         alarmmodel.Severity     `json:"severity"`
	RuleKind         RuleKind                `json:"ruleKind,omitempty"`
	ExecutionTarget  ExecutionTarget         `json:"executionTarget"`
	QualityPolicy    EvaluationQualityPolicy `json:"qualityPolicy,omitempty"`
	FreshnessSeconds int64                   `json:"freshnessSeconds,omitempty"`
	TriggerMode      TriggerMode             `json:"triggerMode"`
	DurationSeconds  int64                   `json:"durationSeconds,omitempty"`
	RepeatCount      uint64                  `json:"repeatCount,omitempty"`
	Raise            RuleTemplateCondition   `json:"raise"`
	Clear            RuleTemplateCondition   `json:"clear"`
	Actions          []RuleAction            `json:"actions,omitempty"`
}

// RuleBaselineTemplate describes a reusable cross-site alarm baseline template.
type RuleBaselineTemplate struct {
	TemplateID             string              `json:"templateId"`
	TemplateKey            string              `json:"templateKey"`
	DisplayName            string              `json:"displayName"`
	Description            string              `json:"description,omitempty"`
	DeviceType             string              `json:"deviceType"`
	ThingModelCode         string              `json:"thingModelCode,omitempty"`
	AlarmType              string              `json:"alarmType"`
	RuleKind               RuleKind            `json:"ruleKind"`
	DefaultSeverity        alarmmodel.Severity `json:"defaultSeverity"`
	DefaultExecutionTarget ExecutionTarget     `json:"defaultExecutionTarget"`
	BoundSiteIDs           []string            `json:"boundSiteIds"`
	LatestVersion          int                 `json:"latestVersion"`
	LatestSpec             *RuleTemplateSpec   `json:"latestSpec,omitempty"`
	Status                 string              `json:"status"`
	CreatedBy              string              `json:"createdBy"`
	UpdatedBy              string              `json:"updatedBy"`
	CreatedAt              string              `json:"createdAt"`
	UpdatedAt              string              `json:"updatedAt"`
}

// RuleBaselineTemplateRelease records an immutable version snapshot of a baseline template.
type RuleBaselineTemplateRelease struct {
	ReleaseID       string           `json:"releaseId"`
	TemplateID      string           `json:"templateId"`
	Version         int              `json:"version"`
	RuleSpec        RuleTemplateSpec `json:"ruleSpec"`
	ChangeNote      string           `json:"changeNote,omitempty"`
	PublishedBy     string           `json:"publishedBy"`
	PublishedByName string           `json:"publishedByName,omitempty"`
	PublishedAt     string           `json:"publishedAt"`
}

// RuleBaselineTemplateDetail returns one template alongside its release history.
type RuleBaselineTemplateDetail struct {
	Template RuleBaselineTemplate          `json:"template"`
	Releases []RuleBaselineTemplateRelease `json:"releases"`
}

type RuleBaselineTemplateList struct {
	Items []RuleBaselineTemplate `json:"items"`
}

type RuleBaselineTemplateReleaseList struct {
	Items []RuleBaselineTemplateRelease `json:"items"`
}

type CreateRuleTemplateRequest struct {
	TemplateKey            string              `json:"templateKey"`
	DisplayName            string              `json:"displayName"`
	Description            string              `json:"description,omitempty"`
	DeviceType             string              `json:"deviceType"`
	ThingModelCode         string              `json:"thingModelCode,omitempty"`
	AlarmType              string              `json:"alarmType,omitempty"`
	RuleKind               RuleKind            `json:"ruleKind,omitempty"`
	DefaultSeverity        alarmmodel.Severity `json:"defaultSeverity"`
	DefaultExecutionTarget ExecutionTarget     `json:"defaultExecutionTarget"`
	BoundSiteIDs           []string            `json:"boundSiteIds,omitempty"`
	InitialSpec            *RuleTemplateSpec   `json:"initialSpec,omitempty"`
	ChangeNote             string              `json:"changeNote,omitempty"`
}

type UpdateRuleTemplateRequest struct {
	DisplayName            string              `json:"displayName"`
	Description            string              `json:"description,omitempty"`
	DeviceType             string              `json:"deviceType"`
	ThingModelCode         string              `json:"thingModelCode,omitempty"`
	DefaultSeverity        alarmmodel.Severity `json:"defaultSeverity"`
	DefaultExecutionTarget ExecutionTarget     `json:"defaultExecutionTarget"`
	Status                 string              `json:"status,omitempty"`
}

type PublishRuleTemplateReleaseRequest struct {
	RuleSpec   RuleTemplateSpec `json:"ruleSpec"`
	ChangeNote string           `json:"changeNote,omitempty"`
}

type UpdateRuleTemplateSitesRequest struct {
	SiteIDs []string `json:"siteIds"`
}

// RolloutSiteTarget optionally specifies a device binding to instantiate a missing template rule on a site.
type RolloutSiteTarget struct {
	SiteID   string `json:"siteId"`
	DeviceID string `json:"deviceId,omitempty"`
}

type PreviewRuleTemplateRolloutRequest struct {
	Version int                 `json:"version,omitempty"`
	SiteIDs []string            `json:"siteIds,omitempty"`
	Targets []RolloutSiteTarget `json:"targets,omitempty"`
}

type ExecuteRuleTemplateRolloutRequest struct {
	Version    int                 `json:"version,omitempty"`
	SiteIDs    []string            `json:"siteIds,omitempty"`
	Targets    []RolloutSiteTarget `json:"targets,omitempty"`
	ChangeNote string              `json:"changeNote,omitempty"`
}

// RuleTemplateFieldConflict highlights an overridden site parameter kept intact despite template changes.
type RuleTemplateFieldConflict struct {
	RuleID        string `json:"ruleId"`
	RuleTitle     string `json:"ruleTitle"`
	Field         string `json:"field"`
	FieldLabel    string `json:"fieldLabel"`
	SiteValue     string `json:"siteValue"`
	TemplateValue string `json:"templateValue"`
	Resolution    string `json:"resolution"`
}

// RuleTemplateSiteRuleDiff describes the planned impact of applying a template release to one site rule.
type RuleTemplateSiteRuleDiff struct {
	RuleID             string                      `json:"ruleId"`
	RuleTitle          string                      `json:"ruleTitle"`
	Action             string                      `json:"action"`
	ChangedFields      []string                    `json:"changedFields"`
	PreservedOverrides []string                    `json:"preservedOverrides"`
	Conflicts          []RuleTemplateFieldConflict `json:"conflicts"`
}

type RuleTemplateSitePreview struct {
	SiteID                 string                     `json:"siteId"`
	BundleRevision         uint64                     `json:"bundleRevision"`
	MatchedRuleCount       int                        `json:"matchedRuleCount"`
	WillUpdateCount        int                        `json:"willUpdateCount"`
	WillCreateCount        int                        `json:"willCreateCount"`
	PreservedOverrideCount int                        `json:"preservedOverrideCount"`
	ConflictCount          int                        `json:"conflictCount"`
	RuleDiffs              []RuleTemplateSiteRuleDiff `json:"ruleDiffs"`
	Note                   string                     `json:"note,omitempty"`
}

type RuleTemplateRolloutPreview struct {
	TemplateID              string                    `json:"templateId"`
	TemplateKey             string                    `json:"templateKey"`
	DisplayName             string                    `json:"displayName"`
	TemplateVersion         int                       `json:"templateVersion"`
	Sites                   []RuleTemplateSitePreview `json:"sites"`
	TotalChangedSites       int                       `json:"totalChangedSites"`
	TotalPreservedOverrides int                       `json:"totalPreservedOverrides"`
	TotalConflicts          int                       `json:"totalConflicts"`
}

type RuleTemplateSiteRolloutOutcome struct {
	SiteID                 string `json:"siteId"`
	Status                 string `json:"status"`
	BundleRevision         uint64 `json:"bundleRevision,omitempty"`
	PreservedOverrideCount int    `json:"preservedOverrideCount"`
	ConflictCount          int    `json:"conflictCount"`
	ChangedRuleCount       int    `json:"changedRuleCount"`
	CreatedRuleCount       int    `json:"createdRuleCount"`
	Detail                 string `json:"detail,omitempty"`
}

type RuleTemplateRolloutResult struct {
	BatchID                 string                           `json:"batchId"`
	TemplateID              string                           `json:"templateId"`
	TemplateVersion         int                              `json:"templateVersion"`
	CreatedBy               string                           `json:"createdBy"`
	CreatedAt               string                           `json:"createdAt"`
	Outcomes                []RuleTemplateSiteRolloutOutcome `json:"outcomes"`
	TotalAppliedSites       int                              `json:"totalAppliedSites"`
	TotalPreservedOverrides int                              `json:"totalPreservedOverrides"`
	TotalConflicts          int                              `json:"totalConflicts"`
}

// SiteRuleTemplateFieldDiff represents one baseline field comparison for a single site rule.
type SiteRuleTemplateFieldDiff struct {
	Field         string `json:"field"`
	Label         string `json:"label"`
	SiteValue     string `json:"siteValue"`
	TemplateValue string `json:"templateValue"`
	IsOverridden  bool   `json:"isOverridden"`
	Drifted       bool   `json:"drifted"`
}

type SiteRuleTemplateDriftItem struct {
	RuleID           string                      `json:"ruleId"`
	RuleTitle        string                      `json:"ruleTitle"`
	TemplateID       string                      `json:"templateId"`
	TemplateKey      string                      `json:"templateKey"`
	TemplateName     string                      `json:"templateName"`
	TemplateVersion  int                         `json:"templateVersion"`
	OverriddenFields []string                    `json:"overriddenFields"`
	DriftedFields    []string                    `json:"driftedFields"`
	Diffs            []SiteRuleTemplateFieldDiff `json:"diffs"`
}

type SiteRuleTemplateDriftReport struct {
	SiteID         string                      `json:"siteId"`
	BundleRevision uint64                      `json:"bundleRevision"`
	Items          []SiteRuleTemplateDriftItem `json:"items"`
}

type ResetSiteRuleToTemplateRequest struct {
	Fields     []string `json:"fields,omitempty"`
	ChangeNote string   `json:"changeNote,omitempty"`
}

// RuleTemplateStore manages cross-site baseline alarm templates and site synchronization.
type RuleTemplateStore interface {
	ListRuleTemplates(ctx context.Context, tenantID, statusFilter string) ([]RuleBaselineTemplate, error)
	GetRuleTemplate(ctx context.Context, tenantID, templateID string) (RuleBaselineTemplateDetail, error)
	CreateRuleTemplate(ctx context.Context, tenantID string, req CreateRuleTemplateRequest, actorID, actorName string, now time.Time) (RuleBaselineTemplateDetail, error)
	UpdateRuleTemplate(ctx context.Context, tenantID, templateID string, req UpdateRuleTemplateRequest, actorID, actorName string, now time.Time) (RuleBaselineTemplate, error)
	ListRuleTemplateReleases(ctx context.Context, tenantID, templateID string, limit int) ([]RuleBaselineTemplateRelease, error)
	PublishRuleTemplateRelease(ctx context.Context, tenantID, templateID string, req PublishRuleTemplateReleaseRequest, actorID, actorName string, now time.Time) (RuleBaselineTemplateRelease, error)
	UpdateRuleTemplateSites(ctx context.Context, tenantID, templateID string, siteIDs []string, actorID string, now time.Time) (RuleBaselineTemplate, error)
	PreviewRuleTemplateRollout(ctx context.Context, tenantID, templateID string, req PreviewRuleTemplateRolloutRequest) (RuleTemplateRolloutPreview, error)
	ExecuteRuleTemplateRollout(ctx context.Context, tenantID, templateID string, req ExecuteRuleTemplateRolloutRequest, actorID, actorName string, now time.Time) (RuleTemplateRolloutResult, error)
	GetSiteRuleTemplateDrift(ctx context.Context, tenantID, siteID string) (SiteRuleTemplateDriftReport, error)
	ResetSiteRuleToTemplate(ctx context.Context, tenantID, siteID, ruleID string, req ResetSiteRuleToTemplateRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error)
}

var templateFieldLabels = map[string]string{
	"title":            "规则名称",
	"summary":          "规则描述",
	"severity":         "告警级别",
	"executionTarget":  "执行位置",
	"qualityPolicy":    "数据质量策略",
	"freshnessSeconds": "新鲜度窗口",
	"triggerMode":      "触发模式",
	"durationSeconds":  "持续时长",
	"repeatCount":      "连续次数",
	"raise":            "触发条件",
	"clear":            "恢复条件",
	"actions":          "联动动作",
}

func templateFieldLabel(field string) string {
	if label, ok := templateFieldLabels[field]; ok {
		return label
	}
	return field
}

func (cond RuleTemplateCondition) validate(depth int) error {
	if depth > maxConditionDepth {
		return errors.New("template condition exceeds maximum nesting depth")
	}
	if cond.Kind == ConditionAnd || cond.Kind == ConditionOr {
		if cond.PointCode != "" || len(cond.Children) < 2 || len(cond.Children) > 32 {
			return errors.New("compound template condition requires 2 to 32 children and no pointCode")
		}
		for _, child := range cond.Children {
			if err := child.validate(depth + 1); err != nil {
				return err
			}
		}
		return nil
	}
	if cond.Kind == ConditionExpression {
		if strings.TrimSpace(cond.Expression) == "" || len(cond.Inputs) == 0 || len(cond.Inputs) > MaxExpressionInputs {
			return errors.New("expression template condition requires expression and inputs")
		}
		for alias, pc := range cond.Inputs {
			if strings.TrimSpace(alias) == "" || !validRulePointCode(pc) {
				return errors.New("expression template input pointCode is invalid")
			}
		}
		return nil
	}
	if !validRulePointCode(cond.PointCode) || len(cond.Children) != 0 {
		return errors.New("leaf template condition requires valid pointCode")
	}
	switch cond.Kind {
	case ConditionCompare:
		val, ok := parseRuleConditionTypedValue(cond.Value)
		if !ok {
			return errors.New("compare template condition value must be finite number, string, or boolean")
		}
		if val.Type == EvaluationValueNumber && !validCompareOperator(cond.Operator) {
			return errors.New("numeric compare operator is invalid")
		}
		if val.Type != EvaluationValueNumber && cond.Operator != CompareEQ && cond.Operator != CompareNE {
			return errors.New("string/boolean compare operator must be EQ or NE")
		}
	case ConditionRange:
		if cond.Minimum == nil || cond.Maximum == nil || math.IsNaN(*cond.Minimum) || math.IsNaN(*cond.Maximum) || *cond.Minimum > *cond.Maximum {
			return errors.New("range template condition bounds are invalid")
		}
	case ConditionHysteresis:
		if cond.Trigger == nil || cond.Reset == nil || math.IsNaN(*cond.Trigger) || math.IsNaN(*cond.Reset) {
			return errors.New("hysteresis template thresholds are invalid")
		}
		if cond.Direction == DirectionAbove && *cond.Reset >= *cond.Trigger {
			return errors.New("ABOVE hysteresis requires reset < trigger")
		}
		if cond.Direction == DirectionBelow && *cond.Reset <= *cond.Trigger {
			return errors.New("BELOW hysteresis requires reset > trigger")
		}
		if cond.Direction != DirectionAbove && cond.Direction != DirectionBelow {
			return errors.New("hysteresis direction is invalid")
		}
	case ConditionNoData, ConditionStale, ConditionUnchanged, ConditionChanged:
		if cond.AgeSeconds <= 0 || cond.AgeSeconds > maxRuleDurationSeconds {
			return errors.New("ageSeconds out of range")
		}
	case ConditionReasons:
		if (cond.Match != ReasonMatchAny && cond.Match != ReasonMatchNone) || len(cond.Reasons) == 0 {
			return errors.New("reasons template condition is invalid")
		}
	case ConditionSlope:
		if !validSlopeOperator(cond.Operator) || cond.Delta == nil || math.IsNaN(*cond.Delta) || cond.WindowSeconds <= 0 || cond.WindowSeconds > 86400 {
			return errors.New("slope template condition is invalid")
		}
	case ConditionWindowAgg:
		if !validWindowAggFunc(cond.Agg) || !validWindowType(cond.WindowType) || !validCompareOperator(cond.Operator) || cond.Threshold == nil || cond.WindowSeconds <= 0 || cond.WindowSeconds > 86400 {
			return errors.New("window_agg template condition is invalid")
		}
	default:
		return errors.New("unsupported template condition kind")
	}
	return nil
}

func normalizeAndValidateTemplateSpec(spec *RuleTemplateSpec, tmpl RuleBaselineTemplate) error {
	if spec == nil {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec is required"}
	}
	spec.Title = strings.TrimSpace(spec.Title)
	if !boundedText(spec.Title, 256) {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.title is required"}
	}
	spec.Summary = strings.TrimSpace(spec.Summary)
	if len(spec.Summary) > 2048 {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.summary exceeds 2048 bytes"}
	}
	if spec.AlarmType == "" {
		spec.AlarmType = tmpl.AlarmType
	}
	spec.AlarmType = strings.TrimSpace(spec.AlarmType)
	if !boundedText(spec.AlarmType, 128) {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.alarmType is required"}
	}
	if spec.Severity == "" {
		spec.Severity = tmpl.DefaultSeverity
	}
	if !validAlarmSeverity(spec.Severity) {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.severity is invalid"}
	}
	if spec.RuleKind == "" {
		spec.RuleKind = tmpl.RuleKind
	}
	if spec.RuleKind == "" {
		spec.RuleKind = RuleKindThreshold
	}
	if !validRuleKind(spec.RuleKind) {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.ruleKind is invalid"}
	}
	if spec.ExecutionTarget == "" {
		spec.ExecutionTarget = tmpl.DefaultExecutionTarget
	}
	if spec.ExecutionTarget == "" {
		spec.ExecutionTarget = ExecutionTargetCloud
	}
	if !validExecutionTarget(spec.ExecutionTarget) {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.executionTarget is invalid"}
	}
	if spec.QualityPolicy == "" {
		spec.QualityPolicy = EvaluationQualityValidOnly
	}
	if spec.QualityPolicy != EvaluationQualityValidOnly && spec.QualityPolicy != EvaluationQualityUsable {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.qualityPolicy is invalid"}
	}
	if spec.FreshnessSeconds == 0 {
		spec.FreshnessSeconds = 900
	}
	if spec.FreshnessSeconds < 0 {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.freshnessSeconds must be positive"}
	}
	switch spec.TriggerMode {
	case TriggerSimple:
		spec.DurationSeconds = 0
		spec.RepeatCount = 0
	case TriggerDuration:
		if spec.DurationSeconds <= 0 || spec.RepeatCount != 0 {
			return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "DURATION trigger requires durationSeconds > 0"}
		}
	case TriggerRepeating:
		if spec.RepeatCount == 0 || spec.DurationSeconds != 0 {
			return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "REPEATING trigger requires repeatCount > 0"}
		}
	default:
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.triggerMode is invalid"}
	}
	if err := spec.Raise.validate(0); err != nil {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.raise: " + err.Error()}
	}
	if err := spec.Clear.validate(0); err != nil {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.clear: " + err.Error()}
	}
	if err := validateRuleActions(spec.Actions); err != nil {
		return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "ruleSpec.actions: " + err.Error()}
	}
	return nil
}

func instantiateTemplateCondition(cond RuleTemplateCondition, deviceID string) (Condition, error) {
	if !alarmmodel.IsUUIDv7(deviceID) {
		return Condition{}, errors.New("target deviceId must be a valid UUIDv7")
	}
	if cond.Kind == ConditionAnd || cond.Kind == ConditionOr {
		children := make([]Condition, 0, len(cond.Children))
		for _, child := range cond.Children {
			parsed, err := instantiateTemplateCondition(child, deviceID)
			if err != nil {
				return Condition{}, err
			}
			children = append(children, parsed)
		}
		c := Condition{Kind: cond.Kind, Children: children}
		return c, c.Validate()
	}
	if cond.Kind == ConditionExpression {
		inputs := make(map[string]string, len(cond.Inputs))
		for alias, pc := range cond.Inputs {
			inputs[alias] = deviceID + "/" + pc
		}
		c := Condition{Kind: ConditionExpression, Expression: cond.Expression, ExprInputs: inputs}
		return c, c.Validate()
	}
	inputKey := deviceID + "/" + cond.PointCode
	c := Condition{Kind: cond.Kind, Input: inputKey}
	switch cond.Kind {
	case ConditionCompare:
		val, ok := parseRuleConditionTypedValue(cond.Value)
		if !ok {
			return Condition{}, errors.New("invalid compare value")
		}
		c.Operator = cond.Operator
		c.Value = val
	case ConditionRange:
		c.Minimum = cloneFloatPtr(cond.Minimum)
		c.Maximum = cloneFloatPtr(cond.Maximum)
	case ConditionHysteresis:
		c.Direction = cond.Direction
		c.Trigger = *cond.Trigger
		c.Reset = *cond.Reset
	case ConditionNoData, ConditionStale, ConditionUnchanged, ConditionChanged:
		c.AgeSeconds = cond.AgeSeconds
	case ConditionReasons:
		c.Reasons = slices.Clone(cond.Reasons)
		c.Match = cond.Match
	case ConditionSlope:
		c.Operator = cond.Operator
		c.Delta = cloneFloatPtr(cond.Delta)
		c.WindowSeconds = cond.WindowSeconds
	case ConditionWindowAgg:
		c.Agg = cond.Agg
		c.WindowType = cond.WindowType
		c.WindowSeconds = cond.WindowSeconds
		c.MinSamples = cond.MinSamples
		c.Operator = cond.Operator
		c.Threshold = cloneFloatPtr(cond.Threshold)
	}
	return c, c.Validate()
}

func extractDeviceIDFromCondition(cond Condition) string {
	if deviceID, _, ok := strings.Cut(cond.Input, "/"); ok && alarmmodel.IsUUIDv7(deviceID) {
		return deviceID
	}
	for _, input := range cond.ExprInputs {
		if deviceID, _, ok := strings.Cut(input, "/"); ok && alarmmodel.IsUUIDv7(deviceID) {
			return deviceID
		}
	}
	for _, child := range cond.Children {
		if id := extractDeviceIDFromCondition(child); id != "" {
			return id
		}
	}
	return ""
}

func extractPointCodeFromCondition(cond Condition) string {
	if _, pc, ok := strings.Cut(cond.Input, "/"); ok && pc != "" {
		return pc
	}
	for _, input := range cond.ExprInputs {
		if _, pc, ok := strings.Cut(input, "/"); ok && pc != "" {
			return pc
		}
	}
	for _, child := range cond.Children {
		if pc := extractPointCodeFromCondition(child); pc != "" {
			return pc
		}
	}
	return ""
}

func formatConditionSummary(cond Condition) string {
	switch cond.Kind {
	case ConditionCompare:
		pc := extractPointCodeFromCondition(cond)
		var v string
		switch cond.Value.Type {
		case EvaluationValueNumber:
			v = strconv.FormatFloat(cond.Value.Number, 'f', -1, 64)
		case EvaluationValueBoolean:
			v = strconv.FormatBool(cond.Value.Boolean)
		default:
			v = cond.Value.String
		}
		return fmt.Sprintf("%s %s %s", pc, cond.Operator, v)
	case ConditionHysteresis:
		return fmt.Sprintf("%s %s trig=%g reset=%g", extractPointCodeFromCondition(cond), cond.Direction, cond.Trigger, cond.Reset)
	case ConditionRange:
		if cond.Minimum != nil && cond.Maximum != nil {
			return fmt.Sprintf("%s [%g, %g]", extractPointCodeFromCondition(cond), *cond.Minimum, *cond.Maximum)
		}
	case ConditionNoData, ConditionStale, ConditionUnchanged, ConditionChanged:
		return fmt.Sprintf("%s %s %ds", extractPointCodeFromCondition(cond), cond.Kind, cond.AgeSeconds)
	case ConditionSlope:
		if cond.Delta != nil {
			return fmt.Sprintf("%s slope %s %g/%ds", extractPointCodeFromCondition(cond), cond.Operator, *cond.Delta, cond.WindowSeconds)
		}
	case ConditionWindowAgg:
		if cond.Threshold != nil {
			return fmt.Sprintf("%s %s(%ds) %s %g", extractPointCodeFromCondition(cond), cond.Agg, cond.WindowSeconds, cond.Operator, *cond.Threshold)
		}
	case ConditionExpression:
		return cond.Expression
	case ConditionAnd, ConditionOr:
		parts := make([]string, 0, len(cond.Children))
		for _, child := range cond.Children {
			parts = append(parts, formatConditionSummary(child))
		}
		return fmt.Sprintf("%s(%s)", cond.Kind, strings.Join(parts, ", "))
	}
	raw, _ := json.Marshal(cond)
	return string(raw)
}

func formatActionsSummary(actions []RuleAction) string {
	if len(actions) == 0 {
		return "无联动"
	}
	return fmt.Sprintf("%d 项联动动作", len(actions))
}

func jsonEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// compareRuleWithTemplate computes the merged AlarmPolicyRevision when rolling out spec onto rule,
// preserving any field listed in rule.Policy.OverriddenFields and reporting conflicts.
func compareRuleWithTemplate(
	rule SiteRule,
	spec RuleTemplateSpec,
) (merged AlarmPolicyRevision, changedFields []string, preservedFields []string, conflicts []RuleTemplateFieldConflict, diffs []SiteRuleTemplateFieldDiff, err error) {
	merged = cloneAlarmPolicyRevision(rule.Policy)
	deviceID := extractDeviceIDFromCondition(rule.Policy.Raise)
	if deviceID == "" {
		deviceID = extractDeviceIDFromCondition(rule.Policy.Clear)
	}
	if deviceID == "" {
		return merged, nil, nil, nil, nil, errors.New("site rule condition has no UUIDv7 deviceId")
	}
	tmplRaise, err := instantiateTemplateCondition(spec.Raise, deviceID)
	if err != nil {
		return merged, nil, nil, nil, nil, err
	}
	tmplClear, err := instantiateTemplateCondition(spec.Clear, deviceID)
	if err != nil {
		return merged, nil, nil, nil, nil, err
	}

	overridden := make(map[string]bool, len(rule.Policy.OverriddenFields))
	for _, f := range rule.Policy.OverriddenFields {
		overridden[f] = true
	}
	isFieldOverridden := func(name string) bool {
		if overridden[name] {
			return true
		}
		prefix := name + "."
		for f := range overridden {
			if strings.HasPrefix(f, prefix) {
				return true
			}
		}
		return false
	}

	siteTarget := merged.ExecutionTarget
	if siteTarget == "" {
		siteTarget = ExecutionTargetCloud
	}
	tmplTarget := spec.ExecutionTarget
	if tmplTarget == "" {
		tmplTarget = ExecutionTargetCloud
	}
	siteQP := merged.QualityPolicy
	if siteQP == "" {
		siteQP = EvaluationQualityValidOnly
	}
	tmplQP := spec.QualityPolicy
	if tmplQP == "" {
		tmplQP = EvaluationQualityValidOnly
	}
	siteFresh := merged.FreshnessSeconds
	if siteFresh == 0 {
		siteFresh = 900
	}
	tmplFresh := spec.FreshnessSeconds
	if tmplFresh == 0 {
		tmplFresh = 900
	}

	type fieldCheck struct {
		name      string
		same      bool
		siteStr   string
		tmplStr   string
		applyTmpl func()
	}

	checks := []fieldCheck{
		{
			name:      "title",
			same:      merged.Title == spec.Title,
			siteStr:   merged.Title,
			tmplStr:   spec.Title,
			applyTmpl: func() { merged.Title = spec.Title },
		},
		{
			name:      "summary",
			same:      merged.Summary == spec.Summary || spec.Summary == "",
			siteStr:   merged.Summary,
			tmplStr:   spec.Summary,
			applyTmpl: func() { merged.Summary = spec.Summary },
		},
		{
			name:      "severity",
			same:      merged.Severity == spec.Severity,
			siteStr:   string(merged.Severity),
			tmplStr:   string(spec.Severity),
			applyTmpl: func() { merged.Severity = spec.Severity },
		},
		{
			name:      "executionTarget",
			same:      siteTarget == tmplTarget,
			siteStr:   string(siteTarget),
			tmplStr:   string(tmplTarget),
			applyTmpl: func() { merged.ExecutionTarget = tmplTarget },
		},
		{
			name:      "qualityPolicy",
			same:      siteQP == tmplQP,
			siteStr:   string(siteQP),
			tmplStr:   string(tmplQP),
			applyTmpl: func() { merged.QualityPolicy = tmplQP },
		},
		{
			name:      "freshnessSeconds",
			same:      siteFresh == tmplFresh,
			siteStr:   fmt.Sprintf("%ds", siteFresh),
			tmplStr:   fmt.Sprintf("%ds", tmplFresh),
			applyTmpl: func() { merged.FreshnessSeconds = tmplFresh },
		},
		{
			name:      "triggerMode",
			same:      merged.TriggerMode == spec.TriggerMode,
			siteStr:   string(merged.TriggerMode),
			tmplStr:   string(spec.TriggerMode),
			applyTmpl: func() { merged.TriggerMode = spec.TriggerMode },
		},
		{
			name:      "durationSeconds",
			same:      merged.DurationSeconds == spec.DurationSeconds,
			siteStr:   fmt.Sprintf("%ds", merged.DurationSeconds),
			tmplStr:   fmt.Sprintf("%ds", spec.DurationSeconds),
			applyTmpl: func() { merged.DurationSeconds = spec.DurationSeconds },
		},
		{
			name:      "repeatCount",
			same:      merged.RepeatCount == spec.RepeatCount,
			siteStr:   strconv.FormatUint(merged.RepeatCount, 10),
			tmplStr:   strconv.FormatUint(spec.RepeatCount, 10),
			applyTmpl: func() { merged.RepeatCount = spec.RepeatCount },
		},
		{
			name:      "raise",
			same:      jsonEqual(merged.Raise, tmplRaise),
			siteStr:   formatConditionSummary(merged.Raise),
			tmplStr:   formatConditionSummary(tmplRaise),
			applyTmpl: func() { merged.Raise = tmplRaise },
		},
		{
			name:      "clear",
			same:      jsonEqual(merged.Clear, tmplClear),
			siteStr:   formatConditionSummary(merged.Clear),
			tmplStr:   formatConditionSummary(tmplClear),
			applyTmpl: func() { merged.Clear = tmplClear },
		},
		{
			name:      "actions",
			same:      jsonEqual(merged.Actions, spec.Actions),
			siteStr:   formatActionsSummary(merged.Actions),
			tmplStr:   formatActionsSummary(spec.Actions),
			applyTmpl: func() { merged.Actions = cloneRuleActions(spec.Actions) },
		},
	}

	changedFields = []string{}
	preservedFields = []string{}
	conflicts = []RuleTemplateFieldConflict{}
	diffs = make([]SiteRuleTemplateFieldDiff, 0, len(checks))

	for _, c := range checks {
		isOver := isFieldOverridden(c.name)
		diffs = append(diffs, SiteRuleTemplateFieldDiff{
			Field:         c.name,
			Label:         templateFieldLabel(c.name),
			SiteValue:     c.siteStr,
			TemplateValue: c.tmplStr,
			IsOverridden:  isOver,
			Drifted:       !c.same,
		})
		if isOver {
			preservedFields = append(preservedFields, c.name)
			if !c.same {
				conflicts = append(conflicts, RuleTemplateFieldConflict{
					RuleID:        rule.AssignmentID,
					RuleTitle:     rule.Policy.Title,
					Field:         c.name,
					FieldLabel:    templateFieldLabel(c.name),
					SiteValue:     c.siteStr,
					TemplateValue: c.tmplStr,
					Resolution:    "PRESERVED_SITE_OVERRIDE",
				})
			}
			continue
		}
		if !c.same {
			c.applyTmpl()
			changedFields = append(changedFields, c.name)
		}
	}
	if merged.TriggerMode == TriggerSimple {
		merged.DurationSeconds = 0
		merged.RepeatCount = 0
	}
	return merged, changedFields, preservedFields, conflicts, diffs, nil
}

func resetSiteRulePolicyFields(rule SiteRule, spec RuleTemplateSpec, fields []string) (AlarmPolicyRevision, error) {
	resetAll := len(fields) == 0
	targetSet := make(map[string]bool, len(fields))
	for _, f := range fields {
		trimmed := strings.TrimSpace(f)
		if trimmed != "" {
			if _, known := templateFieldLabels[trimmed]; !known {
				return AlarmPolicyRevision{}, &RuleTemplateValidationError{
					Code:    "ALARM_TEMPLATE_INVALID",
					Message: fmt.Sprintf("unknown template field %q", trimmed),
				}
			}
			targetSet[trimmed] = true
		}
	}
	next := cloneAlarmPolicyRevision(rule.Policy)
	deviceID := extractDeviceIDFromCondition(rule.Policy.Raise)
	if deviceID == "" {
		deviceID = extractDeviceIDFromCondition(rule.Policy.Clear)
	}
	if deviceID == "" {
		return AlarmPolicyRevision{}, errors.New("site rule has no UUIDv7 deviceId")
	}

	shouldReset := func(name string) bool {
		return resetAll || targetSet[name]
	}

	if shouldReset("title") {
		next.Title = spec.Title
	}
	if shouldReset("summary") && spec.Summary != "" {
		next.Summary = spec.Summary
	}
	if shouldReset("severity") {
		next.Severity = spec.Severity
	}
	if shouldReset("executionTarget") {
		next.ExecutionTarget = spec.ExecutionTarget
	}
	if shouldReset("qualityPolicy") && spec.QualityPolicy != "" {
		next.QualityPolicy = spec.QualityPolicy
	}
	if shouldReset("freshnessSeconds") && spec.FreshnessSeconds > 0 {
		next.FreshnessSeconds = spec.FreshnessSeconds
	}
	if shouldReset("triggerMode") {
		next.TriggerMode = spec.TriggerMode
	}
	if shouldReset("durationSeconds") {
		next.DurationSeconds = spec.DurationSeconds
	}
	if shouldReset("repeatCount") {
		next.RepeatCount = spec.RepeatCount
	}
	if next.TriggerMode == TriggerSimple {
		next.DurationSeconds = 0
		next.RepeatCount = 0
	}
	if shouldReset("raise") {
		c, err := instantiateTemplateCondition(spec.Raise, deviceID)
		if err != nil {
			return AlarmPolicyRevision{}, err
		}
		next.Raise = c
	}
	if shouldReset("clear") {
		c, err := instantiateTemplateCondition(spec.Clear, deviceID)
		if err != nil {
			return AlarmPolicyRevision{}, err
		}
		next.Clear = c
	}
	if shouldReset("actions") {
		next.Actions = cloneRuleActions(spec.Actions)
	}
	if resetAll {
		next.OverriddenFields = nil
	} else if len(next.OverriddenFields) > 0 {
		filtered := make([]string, 0, len(next.OverriddenFields))
		for _, f := range next.OverriddenFields {
			keep := !targetSet[f]
			if keep {
				for resetField := range targetSet {
					if strings.HasPrefix(f, resetField+".") {
						keep = false
						break
					}
				}
			}
			if keep {
				filtered = append(filtered, f)
			}
		}
		next.OverriddenFields = filtered
	}
	return next, nil
}

func instantiateNewSiteRuleFromTemplate(ruleID string, tmpl RuleBaselineTemplate, spec RuleTemplateSpec, deviceID string) (SiteRule, error) {
	if !alarmmodel.IsUUIDv7(ruleID) {
		return SiteRule{}, errors.New("ruleId must be a UUIDv7")
	}
	raiseCond, err := instantiateTemplateCondition(spec.Raise, deviceID)
	if err != nil {
		return SiteRule{}, err
	}
	clearCond, err := instantiateTemplateCondition(spec.Clear, deviceID)
	if err != nil {
		return SiteRule{}, err
	}
	alarmType := spec.AlarmType
	if alarmType == "" {
		alarmType = tmpl.AlarmType
	}
	summary := spec.Summary
	if summary == "" {
		summary = spec.Title
	}
	qp := spec.QualityPolicy
	if qp == "" {
		qp = EvaluationQualityValidOnly
	}
	fresh := spec.FreshnessSeconds
	if fresh <= 0 {
		fresh = 900
	}
	policy := AlarmPolicyRevision{
		SchemaVersion:    AlarmPolicySchemaVersion,
		PolicyID:         ruleID,
		SourceType:       alarmmodel.SourceSiteRule,
		SourceReference:  "alarm-templates:" + tmpl.TemplateKey + ":" + ruleID,
		AlarmType:        alarmType,
		Title:            spec.Title,
		Summary:          summary,
		Severity:         spec.Severity,
		RuleKind:         spec.RuleKind,
		ExecutionTarget:  spec.ExecutionTarget,
		TemplateKey:      tmpl.TemplateKey,
		Actions:          cloneRuleActions(spec.Actions),
		QualityPolicy:    qp,
		FreshnessSeconds: fresh,
		TriggerMode:      spec.TriggerMode,
		DurationSeconds:  spec.DurationSeconds,
		RepeatCount:      spec.RepeatCount,
		Raise:            raiseCond,
		Clear:            clearCond,
	}
	return SiteRule{AssignmentID: ruleID, Policy: policy}, nil
}

func normalizeTargetSites(boundSites []string, explicitIDs []string, targets []RolloutSiteTarget) ([]RolloutSiteTarget, error) {
	seen := make(map[string]RolloutSiteTarget)
	order := make([]string, 0)
	addTarget := func(t RolloutSiteTarget) error {
		sid := strings.TrimSpace(t.SiteID)
		if !alarmmodel.IsUUIDv7(sid) {
			return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "target siteId must be a valid UUIDv7"}
		}
		did := strings.TrimSpace(t.DeviceID)
		if did != "" && !alarmmodel.IsUUIDv7(did) {
			return &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "target deviceId must be a valid UUIDv7"}
		}
		if _, exists := seen[sid]; !exists {
			order = append(order, sid)
			seen[sid] = RolloutSiteTarget{SiteID: sid, DeviceID: did}
		} else if did != "" {
			prev := seen[sid]
			prev.DeviceID = did
			seen[sid] = prev
		}
		return nil
	}
	for _, t := range targets {
		if err := addTarget(t); err != nil {
			return nil, err
		}
	}
	for _, sid := range explicitIDs {
		if err := addTarget(RolloutSiteTarget{SiteID: sid}); err != nil {
			return nil, err
		}
	}
	if len(order) == 0 {
		for _, sid := range boundSites {
			if err := addTarget(RolloutSiteTarget{SiteID: sid}); err != nil {
				return nil, err
			}
		}
	}
	if len(order) == 0 {
		return nil, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: "at least one bound or target siteId is required"}
	}
	if len(order) > maxTemplateBoundSites {
		return nil, &RuleTemplateValidationError{Code: "ALARM_TEMPLATE_INVALID", Message: fmt.Sprintf("target sites exceed maximum %d", maxTemplateBoundSites)}
	}
	sort.Strings(order)
	out := make([]RolloutSiteTarget, 0, len(order))
	for _, sid := range order {
		out = append(out, seen[sid])
	}
	return out, nil
}
