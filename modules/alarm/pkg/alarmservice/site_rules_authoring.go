package alarmservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/rulegraph"
)

const (
	// RuleBundleSourceWebWorkbench records an online hot-publish from the DAG authoring workbench.
	RuleBundleSourceWebWorkbench = "WEB_WORKBENCH"
	// RuleBundleSourceCLISync records a bundle revision synchronized from a site's rules file.
	RuleBundleSourceCLISync = "CLI_SYNC"
	// RuleBundleSourceRollback records a bundle revision restored from a previous snapshot.
	RuleBundleSourceRollback = "ROLLBACK"

	defaultRuleBundleRevisionLimit = 20
	maxRuleBundleRevisionLimit     = 100
	maxSiteRulesPerBundle          = 256
	maxRuleBundleBodyBytes         = int64(256 << 10)
	maxRuleChangeNoteBytes         = 500
	maxConditionDepth              = 8
	maxRuleDurationSeconds         = int64(365 * 24 * 60 * 60)
)

var (
	// ErrRuleBundleConflict indicates an optimistic locking revision mismatch during rule publish or rollback.
	ErrRuleBundleConflict = errors.New("alarm rule bundle revision conflict")
	// ErrRuleDraftInvalid indicates one or more authored rules or their causal DAG failed validation.
	ErrRuleDraftInvalid = errors.New("alarm rule draft is invalid")
)

// RuleBundleConflictError reports the expected versus authoritative bundle revision when publish/rollback races.
type RuleBundleConflictError struct {
	ExpectedRevision uint64
	CurrentRevision  uint64
}

func (err *RuleBundleConflictError) Error() string {
	return fmt.Sprintf("alarm rule bundle revision conflict: expected %d, current is %d", err.ExpectedRevision, err.CurrentRevision)
}

func (err *RuleBundleConflictError) Is(target error) bool {
	return target == ErrRuleBundleConflict
}

// RuleDraftValidationError reports structured validation issues blocking a hot-publish or draft simulation.
type RuleDraftValidationError struct {
	Code    string
	Message string
	Issues  []SiteRuleValidationIssue
}

func (err *RuleDraftValidationError) Error() string {
	return err.Message
}

func (err *RuleDraftValidationError) Is(target error) bool {
	return target == ErrRuleDraftInvalid
}

// SiteRuleDraft represents one editable site alarm rule submitted for dry-run diff or hot-publish.
type SiteRuleDraft struct {
	RuleID           string                  `json:"ruleId,omitempty"`
	Title            string                  `json:"title"`
	AlarmType        string                  `json:"alarmType,omitempty"`
	Summary          string                  `json:"summary,omitempty"`
	Severity         alarmmodel.Severity     `json:"severity"`
	RuleKind         RuleKind                `json:"ruleKind,omitempty"`
	ExecutionTarget  ExecutionTarget         `json:"executionTarget,omitempty"`
	TemplateKey      string                  `json:"templateKey,omitempty"`
	OverriddenFields []string                `json:"overriddenFields,omitempty"`
	DependsOnRuleIDs []string                `json:"dependsOnRuleIds,omitempty"`
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

// SiteRuleValidationIssue identifies one validation failure on a specific rule or the site DAG.
type SiteRuleValidationIssue struct {
	RuleID  string `json:"ruleId,omitempty"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SiteRuleChangeEntry summarizes the before/after delta and alarm impact for one rule across two bundles.
type SiteRuleChangeEntry struct {
	RuleID               string        `json:"ruleId"`
	Title                string        `json:"title"`
	Change               string        `json:"change"`
	ChangedFields        []string      `json:"changedFields"`
	WillClearActiveAlarm bool          `json:"willClearActiveAlarm,omitempty"`
	Before               *SiteRuleView `json:"before,omitempty"`
	After                *SiteRuleView `json:"after,omitempty"`
}

// SiteRuleGraphNodeView describes one rule node in the validated causal DAG.
type SiteRuleGraphNodeView struct {
	RuleID           string              `json:"ruleId"`
	Title            string              `json:"title"`
	Severity         alarmmodel.Severity `json:"severity"`
	RuleKind         RuleKind            `json:"ruleKind,omitempty"`
	ExecutionTarget  ExecutionTarget     `json:"executionTarget,omitempty"`
	HopDepth         int                 `json:"hopDepth"`
	DependsOnRuleIDs []string            `json:"dependsOnRuleIds"`
	ActionCount      int                 `json:"actionCount"`
	Change           string              `json:"change,omitempty"`
}

// SiteRuleGraphEdgeView describes one directed causal edge (upstream -> downstream).
type SiteRuleGraphEdgeView struct {
	FromRuleID string `json:"fromRuleId"`
	ToRuleID   string `json:"toRuleId"`
}

// SiteRuleGraphView is the validated site rule DAG projection consumed by UI graph views.
type SiteRuleGraphView struct {
	Nodes            []SiteRuleGraphNodeView `json:"nodes"`
	Edges            []SiteRuleGraphEdgeView `json:"edges"`
	TopologicalOrder []string                `json:"topologicalOrder"`
	MaxHopDepth      int                     `json:"maxHopDepth"`
}

// SiteRuleDiffRequest previews the impact of applying a complete candidate rule bundle without persisting.
type SiteRuleDiffRequest struct {
	Rules []SiteRuleDraft `json:"rules"`
}

// SiteRuleDiffResult returns validation results, change delta, active-alarm impact, and DAG projection.
type SiteRuleDiffResult struct {
	Valid           bool                      `json:"valid"`
	CurrentRevision uint64                    `json:"currentRevision"`
	CurrentDigest   string                    `json:"currentDigest"`
	NextDigest      string                    `json:"nextDigest"`
	Summary         SiteRuleSyncSummary       `json:"summary"`
	Changes         []SiteRuleChangeEntry     `json:"changes"`
	Graph           SiteRuleGraphView         `json:"graph"`
	Issues          []SiteRuleValidationIssue `json:"issues"`
}

// SiteRulePublishRequest hot-publishes a site's rule bundle under optimistic revision locking.
type SiteRulePublishRequest struct {
	ExpectedRevision uint64          `json:"expectedRevision"`
	ChangeNote       string          `json:"changeNote,omitempty"`
	Rules            []SiteRuleDraft `json:"rules"`
}

// SiteRulePublishResult reports the committed revision, metadata, DAG view, and active rules after publish/rollback.
type SiteRulePublishResult struct {
	BundleRevision         uint64              `json:"bundleRevision"`
	BundleDigest           string              `json:"bundleDigest"`
	Source                 string              `json:"source"`
	Changed                bool                `json:"changed"`
	RolledBackFromRevision *uint64             `json:"rolledBackFromRevision,omitempty"`
	ChangeNote             string              `json:"changeNote,omitempty"`
	PublishedAt            string              `json:"publishedAt"`
	PublishedBy            string              `json:"publishedBy"`
	PublishedByName        string              `json:"publishedByName,omitempty"`
	Summary                SiteRuleSyncSummary `json:"summary"`
	Graph                  SiteRuleGraphView   `json:"graph"`
	Items                  []SiteRuleView      `json:"items"`
}

// SiteRuleBundleRevisionView summarizes one immutable revision entry in the site's rule bundle ledger.
type SiteRuleBundleRevisionView struct {
	BundleRevision         uint64              `json:"bundleRevision"`
	BundleDigest           string              `json:"bundleDigest"`
	ChangeNote             string              `json:"changeNote,omitempty"`
	Source                 string              `json:"source"`
	RolledBackFromRevision *uint64             `json:"rolledBackFromRevision,omitempty"`
	Summary                SiteRuleSyncSummary `json:"summary"`
	RuleCount              int                 `json:"ruleCount"`
	PublishedAt            string              `json:"publishedAt"`
	PublishedBy            string              `json:"publishedBy"`
	PublishedByName        string              `json:"publishedByName,omitempty"`
}

// SiteRuleBundleRevisionList lists recent rule bundle revisions for a site newest-first.
type SiteRuleBundleRevisionList struct {
	CurrentRevision uint64                       `json:"currentRevision"`
	Items           []SiteRuleBundleRevisionView `json:"items"`
}

// SiteRuleRollbackRequest restores a site's rules to a prior bundle revision under optimistic locking.
type SiteRuleRollbackRequest struct {
	ExpectedRevision uint64 `json:"expectedRevision"`
	ChangeNote       string `json:"changeNote,omitempty"`
}

// SiteRuleBundleMeta holds the current revision and digest stamp for a site's rule bundle.
type SiteRuleBundleMeta struct {
	BundleRevision uint64 `json:"bundleRevision"`
	BundleDigest   string `json:"bundleDigest,omitempty"`
}

// SiteRuleAuthoringStore supports online DAG rule diffing, hot-publishing, revision history, and rollback.
type SiteRuleAuthoringStore interface {
	LatestSiteRuleBundleMeta(ctx context.Context, tenantID, siteID string) (SiteRuleBundleMeta, error)
	DiffSiteRules(ctx context.Context, tenantID, siteID string, drafts []SiteRuleDraft) (SiteRuleDiffResult, error)
	PublishSiteRules(ctx context.Context, tenantID, siteID string, req SiteRulePublishRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error)
	ListSiteRuleBundleRevisions(ctx context.Context, tenantID, siteID string, limit int) (SiteRuleBundleRevisionList, error)
	RollbackSiteRules(ctx context.Context, tenantID, siteID string, targetRevision uint64, req SiteRuleRollbackRequest, actorID, actorName string, now time.Time) (SiteRulePublishResult, error)
	CompileSiteRulesEdge(ctx context.Context, tenantID, siteID string, drafts *[]SiteRuleDraft, now time.Time) (EdgeRuleCompileResult, error)
}

func parseRuleCondition(rc RuleCondition, depth int) (Condition, error) {
	if depth > maxConditionDepth {
		return Condition{}, errors.New("site rule condition exceeds maximum nesting depth")
	}
	if rc.Kind == ConditionAnd || rc.Kind == ConditionOr {
		if rc.Reading != nil || len(rc.Children) < 2 || len(rc.Children) > 32 {
			return Condition{}, errors.New("site rule compound condition requires nil reading and 2 to 32 children")
		}
		children := make([]Condition, 0, len(rc.Children))
		for _, child := range rc.Children {
			parsedChild, err := parseRuleCondition(child, depth+1)
			if err != nil {
				return Condition{}, err
			}
			children = append(children, parsedChild)
		}
		condition := Condition{Kind: rc.Kind, Children: children}
		if err := condition.Validate(); err != nil {
			return Condition{}, err
		}
		return condition, nil
	}
	if rc.Kind == ConditionExpression {
		if rc.Reading != nil || len(rc.Children) != 0 || len(rc.Inputs) > MaxExpressionInputs {
			return Condition{}, errors.New("site rule expression condition is invalid")
		}
		exprInputs := make(map[string]string, len(rc.Inputs))
		for alias, reading := range rc.Inputs {
			if !alarmmodel.IsUUIDv7(reading.DeviceID) || !validRulePointCode(reading.PointCode) {
				return Condition{}, errors.New("site rule expression reading is invalid")
			}
			exprInputs[alias] = reading.DeviceID + "/" + reading.PointCode
		}
		condition := Condition{
			Kind:       ConditionExpression,
			Expression: rc.Expression,
			ExprInputs: exprInputs,
		}
		if err := condition.Validate(); err != nil {
			return Condition{}, err
		}
		return condition, nil
	}
	if rc.Reading == nil || !alarmmodel.IsUUIDv7(rc.Reading.DeviceID) || !validRulePointCode(rc.Reading.PointCode) || len(rc.Children) != 0 {
		return Condition{}, errors.New("site rule condition reading must specify a UUIDv7 deviceId and valid pointCode")
	}
	inputKey := rc.Reading.DeviceID + "/" + rc.Reading.PointCode
	condition := Condition{Kind: rc.Kind, Input: inputKey}
	switch rc.Kind {
	case ConditionCompare:
		typed, ok := parseRuleConditionTypedValue(rc.Value)
		if !ok {
			return Condition{}, errors.New("compare condition value must be a finite number, string, or boolean")
		}
		condition.Operator = rc.Operator
		condition.Value = typed
	case ConditionRange:
		if rc.Minimum == nil || rc.Maximum == nil || math.IsNaN(*rc.Minimum) || math.IsInf(*rc.Minimum, 0) || math.IsNaN(*rc.Maximum) || math.IsInf(*rc.Maximum, 0) {
			return Condition{}, errors.New("range condition bounds must be finite numbers")
		}
		condition.Minimum = cloneFloatPtr(rc.Minimum)
		condition.Maximum = cloneFloatPtr(rc.Maximum)
	case ConditionHysteresis:
		if rc.Trigger == nil || rc.Reset == nil || math.IsNaN(*rc.Trigger) || math.IsInf(*rc.Trigger, 0) || math.IsNaN(*rc.Reset) || math.IsInf(*rc.Reset, 0) {
			return Condition{}, errors.New("hysteresis condition thresholds must be finite numbers")
		}
		condition.Direction = rc.Direction
		condition.Trigger = *rc.Trigger
		condition.Reset = *rc.Reset
	case ConditionNoData, ConditionStale, ConditionUnchanged, ConditionChanged:
		if rc.AgeSeconds > maxRuleDurationSeconds {
			return Condition{}, fmt.Errorf("ageSeconds must be at most %d", maxRuleDurationSeconds)
		}
		condition.AgeSeconds = rc.AgeSeconds
	case ConditionReasons:
		condition.Reasons = slices.Clone(rc.Reasons)
		condition.Match = rc.Match
	case ConditionSlope:
		if rc.Delta == nil || math.IsNaN(*rc.Delta) || math.IsInf(*rc.Delta, 0) {
			return Condition{}, errors.New("slope condition delta must be finite")
		}
		if rc.WindowSeconds <= 0 || rc.WindowSeconds > 86400 {
			return Condition{}, errors.New("slope condition windowSeconds must be between 1 and 86400")
		}
		condition.Operator = rc.Operator
		condition.Delta = cloneFloatPtr(rc.Delta)
		condition.WindowSeconds = rc.WindowSeconds
	case ConditionWindowAgg:
		if !validWindowAggFunc(rc.Agg) {
			return Condition{}, errors.New("window_agg condition agg is invalid")
		}
		if !validWindowType(rc.WindowType) {
			return Condition{}, errors.New("window_agg condition windowType is invalid")
		}
		if rc.WindowSeconds <= 0 || rc.WindowSeconds > 86400 {
			return Condition{}, errors.New("window_agg condition windowSeconds must be between 1 and 86400")
		}
		if rc.Threshold == nil || math.IsNaN(*rc.Threshold) || math.IsInf(*rc.Threshold, 0) {
			return Condition{}, errors.New("window_agg condition threshold must be finite")
		}
		if rc.MinSamples < 0 || rc.MinSamples > MaxWindowSamplesPerInput {
			return Condition{}, fmt.Errorf("window_agg condition minSamples must be between 0 and %d", MaxWindowSamplesPerInput)
		}
		condition.Agg = rc.Agg
		condition.WindowType = rc.WindowType
		condition.WindowSeconds = rc.WindowSeconds
		condition.MinSamples = rc.MinSamples
		condition.Operator = rc.Operator
		condition.Threshold = cloneFloatPtr(rc.Threshold)
	default:
		return Condition{}, errors.New("site rule condition kind is unsupported")
	}
	if err := condition.Validate(); err != nil {
		return Condition{}, err
	}
	return condition, nil
}

func parseRuleConditionTypedValue(raw any) (TypedValue, bool) {
	switch value := raw.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return TypedValue{}, false
		}
		return NumberValue(value), true
	case int:
		return NumberValue(float64(value)), true
	case int64:
		return NumberValue(float64(value)), true
	case json.Number:
		parsed, err := strconv.ParseFloat(value.String(), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return TypedValue{}, false
		}
		return NumberValue(parsed), true
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

type bundleDigestItem struct {
	AssignmentID string              `json:"assignmentId"`
	Policy       AlarmPolicyRevision `json:"policy"`
}

func computeSiteRuleBundleDigest(rules []SiteRule) (string, error) {
	entries := make([]bundleDigestItem, 0, len(rules))
	for _, rule := range rules {
		policy := cloneAlarmPolicyRevision(rule.Policy)
		policy.PolicyRevisionID = ""
		policy.Revision = 0
		policy.Digest = ""
		entries = append(entries, bundleDigestItem{
			AssignmentID: rule.AssignmentID,
			Policy:       policy,
		})
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].AssignmentID < entries[right].AssignmentID
	})
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("encode site rule bundle for digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func compileDraftsAgainstHistory(
	drafts []SiteRuleDraft,
	history map[string]SiteRule,
	newID func() (string, error),
) ([]SiteRule, rulegraph.GraphReport, []SiteRuleValidationIssue) {
	if len(drafts) > maxSiteRulesPerBundle {
		return nil, rulegraph.GraphReport{}, []SiteRuleValidationIssue{{
			Field:   "rules",
			Code:    "RULE_BUNDLE_LIMIT_EXCEEDED",
			Message: fmt.Sprintf("single site rule bundle supports at most %d rules", maxSiteRulesPerBundle),
		}}
	}
	compiled := make([]SiteRule, 0, len(drafts))
	issues := make([]SiteRuleValidationIssue, 0)
	seen := make(map[string]bool, len(drafts))
	for index, draft := range drafts {
		rule, ruleIssues := compileSingleRuleDraft(index, draft, history, seen, newID)
		if len(ruleIssues) > 0 {
			issues = append(issues, ruleIssues...)
			continue
		}
		seen[rule.AssignmentID] = true
		compiled = append(compiled, rule)
	}
	if len(issues) > 0 {
		return nil, rulegraph.GraphReport{}, issues
	}
	report, err := ValidateSiteRuleGraph(compiled)
	if err != nil {
		issues = append(issues, graphErrorValidationIssue(err))
		return nil, rulegraph.GraphReport{}, issues
	}
	if edgeIssues := validateDraftEdgeEligibility(compiled, report.Order); len(edgeIssues) > 0 {
		return nil, rulegraph.GraphReport{}, edgeIssues
	}
	return compiled, report, nil
}

func compileSingleRuleDraft(
	index int,
	draft SiteRuleDraft,
	history map[string]SiteRule,
	seen map[string]bool,
	newID func() (string, error),
) (SiteRule, []SiteRuleValidationIssue) {
	issues := make([]SiteRuleValidationIssue, 0)
	ruleID := strings.TrimSpace(draft.RuleID)
	if ruleID == "" {
		if newID != nil {
			allocated, err := newID()
			if err != nil {
				return SiteRule{}, []SiteRuleValidationIssue{{
					Field:   fmt.Sprintf("rules[%d].ruleId", index),
					Code:    "RULE_ID_ALLOCATION_FAILED",
					Message: err.Error(),
				}}
			}
			ruleID = allocated
		} else {
			ruleID = fmt.Sprintf("01900000-0000-7000-8000-%012d", index+1)
		}
	} else if !alarmmodel.IsUUIDv7(ruleID) {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "ruleId",
			Code:    "INVALID_RULE_ID",
			Message: "ruleId must be a valid UUIDv7",
		})
	} else if seen[ruleID] {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "ruleId",
			Code:    "DUPLICATE_RULE_ID",
			Message: "duplicate ruleId in rule bundle",
		})
	}

	title := strings.TrimSpace(draft.Title)
	if !boundedText(title, 256) {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "title",
			Code:    "INVALID_TITLE",
			Message: "title is required and must be at most 256 bytes",
		})
	}
	if !validAlarmSeverity(draft.Severity) {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "severity",
			Code:    "INVALID_SEVERITY",
			Message: "severity must be CRITICAL, MAJOR, MINOR, WARNING, or INFO",
		})
	}
	if draft.RuleKind != "" && !validRuleKind(draft.RuleKind) {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "ruleKind",
			Code:    "INVALID_RULE_KIND",
			Message: "ruleKind is invalid",
		})
	}
	if draft.ExecutionTarget != "" && !validExecutionTarget(draft.ExecutionTarget) {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "executionTarget",
			Code:    "INVALID_EXECUTION_TARGET",
			Message: "executionTarget must be CLOUD, EDGE, or CLOUD_AND_EDGE",
		})
	}
	templateKey := strings.TrimSpace(draft.TemplateKey)
	if templateKey != "" && !boundedText(templateKey, 128) {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "templateKey",
			Code:    "INVALID_TEMPLATE_KEY",
			Message: "templateKey must be at most 128 bytes",
		})
	}
	if len(draft.OverriddenFields) > 32 {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "overriddenFields",
			Code:    "INVALID_OVERRIDDEN_FIELDS",
			Message: "overriddenFields exceeds maximum count of 32",
		})
	} else {
		for _, field := range draft.OverriddenFields {
			if !boundedText(field, 128) {
				issues = append(issues, SiteRuleValidationIssue{
					RuleID:  ruleID,
					Field:   "overriddenFields",
					Code:    "INVALID_OVERRIDDEN_FIELDS",
					Message: "overriddenFields entry must be non-empty and at most 128 bytes",
				})
				break
			}
		}
	}

	raiseCond, raiseErr := parseRuleCondition(draft.Raise, 0)
	if raiseErr != nil {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "raise",
			Code:    "INVALID_RAISE_CONDITION",
			Message: raiseErr.Error(),
		})
	}
	clearCond, clearErr := parseRuleCondition(draft.Clear, 0)
	if clearErr != nil {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "clear",
			Code:    "INVALID_CLEAR_CONDITION",
			Message: clearErr.Error(),
		})
	}

	switch draft.TriggerMode {
	case TriggerSimple:
		if draft.DurationSeconds != 0 || draft.RepeatCount != 0 {
			issues = append(issues, SiteRuleValidationIssue{
				RuleID:  ruleID,
				Field:   "triggerMode",
				Code:    "INVALID_TRIGGER_MODE",
				Message: "SIMPLE triggerMode cannot carry durationSeconds or repeatCount",
			})
		}
	case TriggerDuration:
		if draft.DurationSeconds <= 0 || draft.DurationSeconds > maxRuleDurationSeconds || draft.RepeatCount != 0 {
			issues = append(issues, SiteRuleValidationIssue{
				RuleID:  ruleID,
				Field:   "durationSeconds",
				Code:    "INVALID_TRIGGER_MODE",
				Message: fmt.Sprintf("DURATION triggerMode requires durationSeconds between 1 and %d and repeatCount == 0", maxRuleDurationSeconds),
			})
		}
	case TriggerRepeating:
		if draft.RepeatCount == 0 || draft.DurationSeconds != 0 {
			issues = append(issues, SiteRuleValidationIssue{
				RuleID:  ruleID,
				Field:   "repeatCount",
				Code:    "INVALID_TRIGGER_MODE",
				Message: "REPEATING triggerMode requires repeatCount > 0 and durationSeconds == 0",
			})
		}
	default:
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "triggerMode",
			Code:    "INVALID_TRIGGER_MODE",
			Message: "triggerMode must be SIMPLE, DURATION, or REPEATING",
		})
	}

	dependsOn := slices.Clone(draft.DependsOnRuleIDs)
	if err := validateRuleDependencies(ruleID, dependsOn); err != nil {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "dependsOnRuleIds",
			Code:    "INVALID_DEPENDENCIES",
			Message: err.Error(),
		})
	}
	if err := validateDependencyGate(draft.DependencyGate, dependsOn); err != nil {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "dependencyGate",
			Code:    "INVALID_DEPENDENCY_GATE",
			Message: err.Error(),
		})
	}
	actions := cloneRuleActions(draft.Actions)
	if err := validateRuleActions(actions); err != nil {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "actions",
			Code:    "INVALID_ACTIONS",
			Message: err.Error(),
		})
	}

	policy := AlarmPolicyRevision{
		SchemaVersion:    AlarmPolicySchemaVersion,
		PolicyID:         ruleID,
		SourceType:       alarmmodel.SourceSiteRule,
		SourceReference:  "alarm-rules:" + ruleID,
		AlarmType:        "site-rule/" + ruleID,
		Title:            title,
		Summary:          title,
		Severity:         draft.Severity,
		RuleKind:         draft.RuleKind,
		ExecutionTarget:  draft.ExecutionTarget,
		TemplateKey:      templateKey,
		OverriddenFields: slices.Clone(draft.OverriddenFields),
		DependsOnRuleIDs: dependsOn,
		DependencyGate:   draft.DependencyGate,
		Actions:          actions,
		QualityPolicy:    EvaluationQualityValidOnly,
		FreshnessSeconds: 900,
		TriggerMode:      draft.TriggerMode,
		DurationSeconds:  draft.DurationSeconds,
		RepeatCount:      draft.RepeatCount,
		Raise:            raiseCond,
		Clear:            clearCond,
	}
	prior, hasPrior := history[ruleID]
	if hasPrior {
		if alarmmodel.IsUUIDv7(prior.Policy.PolicyID) {
			policy.PolicyID = prior.Policy.PolicyID
		}
		if strings.TrimSpace(prior.Policy.SourceReference) != "" {
			policy.SourceReference = prior.Policy.SourceReference
		}
		if strings.TrimSpace(prior.Policy.AlarmType) != "" {
			policy.AlarmType = prior.Policy.AlarmType
		}
		if strings.TrimSpace(prior.Policy.Summary) != "" {
			policy.Summary = prior.Policy.Summary
		}
		if prior.Policy.QualityPolicy != "" {
			policy.QualityPolicy = prior.Policy.QualityPolicy
		}
		if prior.Policy.FreshnessSeconds > 0 {
			policy.FreshnessSeconds = prior.Policy.FreshnessSeconds
		}
		if prior.Policy.Schedule != nil {
			clonedPolicy := cloneAlarmPolicyRevision(prior.Policy)
			policy.Schedule = clonedPolicy.Schedule
		}
	}
	if alarmType := strings.TrimSpace(draft.AlarmType); alarmType != "" {
		if hasPrior && strings.TrimSpace(prior.Policy.AlarmType) != "" && alarmType != strings.TrimSpace(prior.Policy.AlarmType) {
			issues = append(issues, SiteRuleValidationIssue{
				RuleID:  ruleID,
				Field:   "alarmType",
				Code:    "ALARM_TYPE_IMMUTABLE",
				Message: "existing or previously retired rule alarmType cannot be modified",
			})
		} else {
			policy.AlarmType = alarmType
		}
	}
	if summary := strings.TrimSpace(draft.Summary); summary != "" {
		policy.Summary = summary
	}
	if draft.QualityPolicy != "" {
		policy.QualityPolicy = draft.QualityPolicy
	}
	if draft.FreshnessSeconds < 0 {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "freshnessSeconds",
			Code:    "INVALID_FRESHNESS_SECONDS",
			Message: "freshnessSeconds must be greater than zero",
		})
	} else if draft.FreshnessSeconds > 0 {
		policy.FreshnessSeconds = draft.FreshnessSeconds
	}
	if policy.RuleKind == "" {
		if effective := policy.EffectiveRuleKind(); effective != RuleKindThreshold {
			policy.RuleKind = effective
		}
	}
	if !boundedText(policy.AlarmType, 128) {
		return SiteRule{}, []SiteRuleValidationIssue{{
			RuleID:  ruleID,
			Field:   "alarmType",
			Code:    "INVALID_ALARM_TYPE",
			Message: "alarmType is required and must be at most 128 bytes",
		}}
	}
	if !boundedText(policy.Summary, 2048) {
		return SiteRule{}, []SiteRuleValidationIssue{{
			RuleID:  ruleID,
			Field:   "summary",
			Code:    "INVALID_SUMMARY",
			Message: "summary is required and must be at most 2048 bytes",
		}}
	}
	if policy.QualityPolicy != EvaluationQualityValidOnly && policy.QualityPolicy != EvaluationQualityUsable {
		return SiteRule{}, []SiteRuleValidationIssue{{
			RuleID:  ruleID,
			Field:   "qualityPolicy",
			Code:    "INVALID_QUALITY_POLICY",
			Message: "qualityPolicy must be VALID_ONLY or USABLE",
		}}
	}
	if policy.FreshnessSeconds <= 0 {
		issues = append(issues, SiteRuleValidationIssue{
			RuleID:  ruleID,
			Field:   "freshnessSeconds",
			Code:    "INVALID_FRESHNESS_SECONDS",
			Message: "freshnessSeconds must be greater than zero",
		})
	}
	if len(issues) > 0 {
		return SiteRule{}, issues
	}
	return SiteRule{AssignmentID: ruleID, Policy: policy}, nil
}

func graphErrorValidationIssue(err error) SiteRuleValidationIssue {
	code := "RULE_GRAPH_INVALID"
	switch {
	case errors.Is(err, rulegraph.ErrRuleCycleDetected):
		code = "RULE_CYCLE_DETECTED"
	case errors.Is(err, rulegraph.ErrUnknownDependency):
		code = "UNKNOWN_DEPENDENCY"
	case errors.Is(err, rulegraph.ErrSelfDependency):
		code = "SELF_DEPENDENCY"
	case errors.Is(err, rulegraph.ErrMaxLinkageHopsExceeded):
		code = "MAX_LINKAGE_HOPS_EXCEEDED"
	case errors.Is(err, rulegraph.ErrDuplicateRuleNode):
		code = "DUPLICATE_RULE_ID"
	}
	return SiteRuleValidationIssue{
		Field:   "dependsOnRuleIds",
		Code:    code,
		Message: err.Error(),
	}
}

func buildSiteRuleGraphView(rules []SiteRule, report rulegraph.GraphReport, changeByID map[string]string) SiteRuleGraphView {
	byID := make(map[string]SiteRule, len(rules))
	for _, rule := range rules {
		byID[rule.AssignmentID] = rule
	}
	order := slices.Clone(report.Order)
	if len(order) != len(rules) {
		order = make([]string, 0, len(rules))
		for _, rule := range rules {
			order = append(order, rule.AssignmentID)
		}
		sort.Strings(order)
	}
	nodes := make([]SiteRuleGraphNodeView, 0, len(order))
	edges := make([]SiteRuleGraphEdgeView, 0)
	for _, id := range order {
		rule, ok := byID[id]
		if !ok {
			continue
		}
		ruleKind := rule.Policy.RuleKind
		if ruleKind == "" {
			ruleKind = rule.Policy.EffectiveRuleKind()
		}
		deps := slices.Clone(rule.Policy.DependsOnRuleIDs)
		if deps == nil {
			deps = []string{}
		} else {
			sort.Strings(deps)
		}
		nodes = append(nodes, SiteRuleGraphNodeView{
			RuleID:           rule.AssignmentID,
			Title:            rule.Policy.Title,
			Severity:         rule.Policy.Severity,
			RuleKind:         ruleKind,
			ExecutionTarget:  rule.Policy.EffectiveExecutionTarget(),
			HopDepth:         report.Nodes[id].HopDepth,
			DependsOnRuleIDs: deps,
			ActionCount:      len(rule.Policy.Actions),
			Change:           changeByID[id],
		})
		for _, depID := range deps {
			edges = append(edges, SiteRuleGraphEdgeView{
				FromRuleID: depID,
				ToRuleID:   id,
			})
		}
	}
	return SiteRuleGraphView{
		Nodes:            nodes,
		Edges:            edges,
		TopologicalOrder: order,
		MaxHopDepth:      report.MaxHopDepth,
	}
}

func buildDiffChangeEntries(
	plan SiteRulePlan,
	activeByID map[string]SiteRule,
	currentReport rulegraph.GraphReport,
	nextReport rulegraph.GraphReport,
	activeAlarmCounts map[string]int,
) ([]SiteRuleChangeEntry, map[string]string, error) {
	entries := make([]SiteRuleChangeEntry, 0, len(plan.Added)+len(plan.Changed)+len(plan.Unchanged)+len(plan.Retired))
	changeByID := make(map[string]string, len(plan.Added)+len(plan.Changed)+len(plan.Unchanged))

	for _, rule := range plan.Added {
		view, err := siteRuleViewWithHopDepth(rule, nextReport.Nodes[rule.AssignmentID].HopDepth)
		if err != nil {
			return nil, nil, err
		}
		changeByID[rule.AssignmentID] = "ADDED"
		entries = append(entries, SiteRuleChangeEntry{
			RuleID:        rule.AssignmentID,
			Title:         rule.Policy.Title,
			Change:        "ADDED",
			ChangedFields: []string{},
			After:         &view,
		})
	}
	for _, after := range plan.Changed {
		before := activeByID[after.AssignmentID]
		beforeView, err := siteRuleViewWithHopDepth(before, currentReport.Nodes[before.AssignmentID].HopDepth)
		if err != nil {
			return nil, nil, err
		}
		afterView, err := siteRuleViewWithHopDepth(after, nextReport.Nodes[after.AssignmentID].HopDepth)
		if err != nil {
			return nil, nil, err
		}
		changeByID[after.AssignmentID] = "CHANGED"
		entries = append(entries, SiteRuleChangeEntry{
			RuleID:        after.AssignmentID,
			Title:         after.Policy.Title,
			Change:        "CHANGED",
			ChangedFields: diffRuleFields(before, after),
			Before:        &beforeView,
			After:         &afterView,
		})
	}
	for _, rule := range plan.Unchanged {
		view, err := siteRuleViewWithHopDepth(rule, nextReport.Nodes[rule.AssignmentID].HopDepth)
		if err != nil {
			return nil, nil, err
		}
		changeByID[rule.AssignmentID] = "UNCHANGED"
		entries = append(entries, SiteRuleChangeEntry{
			RuleID:        rule.AssignmentID,
			Title:         rule.Policy.Title,
			Change:        "UNCHANGED",
			ChangedFields: []string{},
			After:         &view,
		})
	}
	for _, rule := range plan.Retired {
		view, err := siteRuleViewWithHopDepth(rule, currentReport.Nodes[rule.AssignmentID].HopDepth)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, SiteRuleChangeEntry{
			RuleID:               rule.AssignmentID,
			Title:                rule.Policy.Title,
			Change:               "RETIRED",
			ChangedFields:        []string{},
			WillClearActiveAlarm: activeAlarmCounts[rule.AssignmentID] > 0,
			Before:               &view,
		})
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Change != entries[right].Change {
			return changeSortRank(entries[left].Change) < changeSortRank(entries[right].Change)
		}
		return entries[left].RuleID < entries[right].RuleID
	})
	return entries, changeByID, nil
}

func changeSortRank(change string) int {
	switch change {
	case "ADDED":
		return 0
	case "CHANGED":
		return 1
	case "RETIRED":
		return 2
	case "UNCHANGED":
		return 3
	default:
		return 4
	}
}

func diffRuleFields(before, after SiteRule) []string {
	fields := make([]string, 0)
	if before.Policy.Title != after.Policy.Title {
		fields = append(fields, "title")
	}
	if before.Policy.Severity != after.Policy.Severity {
		fields = append(fields, "severity")
	}
	if before.Policy.EffectiveRuleKind() != after.Policy.EffectiveRuleKind() {
		fields = append(fields, "ruleKind")
	}
	if before.Policy.EffectiveExecutionTarget() != after.Policy.EffectiveExecutionTarget() {
		fields = append(fields, "executionTarget")
	}
	if before.Policy.TriggerMode != after.Policy.TriggerMode {
		fields = append(fields, "triggerMode")
	}
	if before.Policy.DurationSeconds != after.Policy.DurationSeconds {
		fields = append(fields, "durationSeconds")
	}
	if before.Policy.RepeatCount != after.Policy.RepeatCount {
		fields = append(fields, "repeatCount")
	}
	if !reflect.DeepEqual(before.Policy.Raise, after.Policy.Raise) {
		fields = append(fields, "raise")
	}
	if !reflect.DeepEqual(before.Policy.Clear, after.Policy.Clear) {
		fields = append(fields, "clear")
	}
	if !stringSliceEqualUnordered(before.Policy.DependsOnRuleIDs, after.Policy.DependsOnRuleIDs) {
		fields = append(fields, "dependsOnRuleIds")
	}
	if before.Policy.DependencyGate != after.Policy.DependencyGate {
		fields = append(fields, "dependencyGate")
	}
	if !reflect.DeepEqual(before.Policy.Actions, after.Policy.Actions) {
		fields = append(fields, "actions")
	}
	if before.Policy.QualityPolicy != after.Policy.QualityPolicy {
		fields = append(fields, "qualityPolicy")
	}
	if before.Policy.FreshnessSeconds != after.Policy.FreshnessSeconds {
		fields = append(fields, "freshnessSeconds")
	}
	if before.Policy.Summary != after.Policy.Summary {
		fields = append(fields, "summary")
	}
	if before.Policy.AlarmType != after.Policy.AlarmType {
		fields = append(fields, "alarmType")
	}
	return fields
}

func stringSliceEqualUnordered(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	lCopy := slices.Clone(left)
	rCopy := slices.Clone(right)
	sort.Strings(lCopy)
	sort.Strings(rCopy)
	return slices.Equal(lCopy, rCopy)
}

func validateChangeNote(note string) (string, error) {
	trimmed := strings.TrimSpace(note)
	if len(trimmed) > maxRuleChangeNoteBytes {
		return "", errors.New("changeNote exceeds 500 bytes")
	}
	return trimmed, nil
}

func normalizeBundleRevisionLimit(limit int) int {
	if limit <= 0 {
		return defaultRuleBundleRevisionLimit
	}
	if limit > maxRuleBundleRevisionLimit {
		return maxRuleBundleRevisionLimit
	}
	return limit
}

// buildRuleViews converts compiled site rules + graph report into HTTP views (including optional metadata only when populated cleanly).
func buildSiteRuleViews(rules []SiteRule, report rulegraph.GraphReport) ([]SiteRuleView, error) {
	views := make([]SiteRuleView, 0, len(rules))
	for _, rule := range rules {
		view, err := siteRuleViewWithHopDepth(rule, report.Nodes[rule.AssignmentID].HopDepth)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}
