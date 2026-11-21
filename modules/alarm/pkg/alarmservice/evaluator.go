package alarmservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const AlarmPolicySchemaVersion = 1

var policyDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type EvaluationStatus string

const (
	EvaluationMatched       EvaluationStatus = "MATCHED"
	EvaluationNotMatched    EvaluationStatus = "NOT_MATCHED"
	EvaluationIndeterminate EvaluationStatus = "INDETERMINATE"
)

type EvaluationEffect string

const (
	EvaluationEffectNone    EvaluationEffect = "NONE"
	EvaluationEffectPublish EvaluationEffect = "PUBLISH"
	EvaluationEffectClear   EvaluationEffect = "CLEAR"
)

type EvaluationQualityPolicy string

const (
	EvaluationQualityValidOnly EvaluationQualityPolicy = "VALID_ONLY"
	EvaluationQualityUsable    EvaluationQualityPolicy = "USABLE"
)

const (
	QualityBlockerOutsideSchedule = "OUTSIDE_SCHEDULE"
	qualityBlockerMissingPrefix   = "MISSING_INPUT:"
	qualityBlockerStalePrefix     = "STALE_INPUT:"
	qualityBlockerInvalidPrefix   = "INVALID_INPUT:"
	qualityBlockerQualityPrefix   = "UNTRUSTED_QUALITY:"
)

type TriggerMode string

const (
	TriggerSimple    TriggerMode = "SIMPLE"
	TriggerDuration  TriggerMode = "DURATION"
	TriggerRepeating TriggerMode = "REPEATING"
)

type RuleKind string

const (
	RuleKindThreshold  RuleKind = "THRESHOLD"
	RuleKindSlope      RuleKind = "SLOPE"
	RuleKindExpression RuleKind = "EXPRESSION"
)

type ConditionKind string

const (
	ConditionCompare    ConditionKind = "COMPARE"
	ConditionRange      ConditionKind = "RANGE"
	ConditionHysteresis ConditionKind = "HYSTERESIS"
	ConditionNoData     ConditionKind = "NO_DATA"
	ConditionStale      ConditionKind = "STALE"
	// ConditionUnchanged holds when a fresh reading has kept the same value for ageSeconds;
	// ConditionChanged when it took its value less than ageSeconds ago, or there is no reading to
	// be stuck (#616).
	ConditionUnchanged ConditionKind = "UNCHANGED"
	ConditionChanged   ConditionKind = "CHANGED"
	// ConditionReasons matches the point's health reasons: ANY when one of the
	// listed codes is present, NONE when none of them are. It does not require the
	// displayed value to be trusted, so a range rejection (the last good number
	// stays on screen, health is BAD) can still raise.
	ConditionReasons    ConditionKind = "REASONS"
	ConditionSlope      ConditionKind = "SLOPE"
	ConditionExpression ConditionKind = "EXPRESSION"
	ConditionAnd        ConditionKind = "AND"
	ConditionOr         ConditionKind = "OR"
)

const (
	ReasonMatchAny  = "ANY"
	ReasonMatchNone = "NONE"
)

type CompareOperator string

const (
	CompareEQ  CompareOperator = "EQ"
	CompareNE  CompareOperator = "NE"
	CompareGT  CompareOperator = "GT"
	CompareGTE CompareOperator = "GTE"
	CompareLT  CompareOperator = "LT"
	CompareLTE CompareOperator = "LTE"
)

type HysteresisDirection string

const (
	DirectionAbove HysteresisDirection = "ABOVE"
	DirectionBelow HysteresisDirection = "BELOW"
)

type EvaluationValueType string

const (
	EvaluationValueNumber  EvaluationValueType = "NUMBER"
	EvaluationValueString  EvaluationValueType = "STRING"
	EvaluationValueBoolean EvaluationValueType = "BOOLEAN"
)

type TypedValue struct {
	Type    EvaluationValueType `json:"type"`
	Number  float64             `json:"number,omitempty"`
	String  string              `json:"string,omitempty"`
	Boolean bool                `json:"boolean,omitempty"`
}

func NumberValue(value float64) TypedValue {
	return TypedValue{Type: EvaluationValueNumber, Number: value}
}

func StringValue(value string) TypedValue {
	return TypedValue{Type: EvaluationValueString, String: value}
}

func BooleanValue(value bool) TypedValue {
	return TypedValue{Type: EvaluationValueBoolean, Boolean: value}
}

type Condition struct {
	Kind          ConditionKind       `json:"kind"`
	Input         string              `json:"input,omitempty"`
	Operator      CompareOperator     `json:"operator,omitempty"`
	Value         TypedValue          `json:"value,omitempty"`
	Minimum       *float64            `json:"minimum,omitempty"`
	Maximum       *float64            `json:"maximum,omitempty"`
	Direction     HysteresisDirection `json:"direction,omitempty"`
	Trigger       float64             `json:"trigger,omitempty"`
	Reset         float64             `json:"reset,omitempty"`
	AgeSeconds    int64               `json:"ageSeconds,omitempty"`
	Delta         *float64            `json:"delta,omitempty"`
	WindowSeconds int64               `json:"windowSeconds,omitempty"`
	Expression    string              `json:"expression,omitempty"`
	ExprInputs    map[string]string   `json:"inputs,omitempty"`
	Children      []Condition         `json:"children,omitempty"`
	Reasons       []string            `json:"reasons,omitempty"`
	Match         string              `json:"match,omitempty"`
}

type ScheduleWindow struct {
	DaysOfWeek     []int `json:"daysOfWeek"`
	StartsOnMinute int   `json:"startsOnMinute"`
	EndsOnMinute   int   `json:"endsOnMinute"`
}

type SiteSchedule struct {
	Timezone string           `json:"timezone"`
	Windows  []ScheduleWindow `json:"windows"`
}

type DependencyGateMode string

const (
	DependencyGateNone      DependencyGateMode = "NONE"
	DependencyGateAllActive DependencyGateMode = "ALL_ACTIVE"
	DependencyGateAnyActive DependencyGateMode = "ANY_ACTIVE"
)

const MaxActionsPerRule = 4

type RuleActionKind string

const (
	RuleActionWebhook           RuleActionKind = "WEBHOOK"
	RuleActionWorkOrder         RuleActionKind = "WORK_ORDER"
	RuleActionProtectionCommand RuleActionKind = "PROTECTION_COMMAND"
)

type ActionTriggerOn string

const (
	ActionTriggerRaise ActionTriggerOn = "RAISE"
	ActionTriggerClear ActionTriggerOn = "CLEAR"
	ActionTriggerBoth  ActionTriggerOn = "BOTH"
)

var actionSecretRefPattern = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

type RuleAction struct {
	Kind              RuleActionKind     `json:"kind"`
	TriggerOn         ActionTriggerOn    `json:"triggerOn,omitempty"`
	WebhookURL        string             `json:"webhookUrl,omitempty"`
	SecretRef         string             `json:"secretRef,omitempty"`
	WorkOrderPriority string             `json:"workOrderPriority,omitempty"`
	WorkOrderTitle    string             `json:"workOrderTitle,omitempty"`
	TargetDeviceID    string             `json:"targetDeviceId,omitempty"`
	TargetPointCode   string             `json:"targetPointCode,omitempty"`
	Capability        string             `json:"capability,omitempty"`
	Parameters        map[string]float64 `json:"parameters,omitempty"`
}

func (action RuleAction) EffectiveTriggerOn() ActionTriggerOn {
	if action.TriggerOn == "" {
		return ActionTriggerRaise
	}
	return action.TriggerOn
}

func (action RuleAction) MatchesEffect(effect EvaluationEffect) bool {
	trigger := action.EffectiveTriggerOn()
	switch effect {
	case EvaluationEffectPublish:
		return trigger == ActionTriggerRaise || trigger == ActionTriggerBoth
	case EvaluationEffectClear:
		return trigger == ActionTriggerClear || trigger == ActionTriggerBoth
	default:
		return false
	}
}

func (action RuleAction) Clone() RuleAction {
	cloned := action
	if action.Parameters != nil {
		cloned.Parameters = make(map[string]float64, len(action.Parameters))
		for key, value := range action.Parameters {
			cloned.Parameters[key] = value
		}
	}
	return cloned
}

func (action RuleAction) SanitizedTargetRef() string {
	switch action.Kind {
	case RuleActionWebhook:
		parsed, err := url.Parse(action.WebhookURL)
		if err != nil || parsed.Hostname() == "" {
			return "https://invalid"
		}
		target := parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
		if len(target) > 256 {
			return target[:256]
		}
		return target
	case RuleActionWorkOrder:
		target := action.WorkOrderPriority + ":" + strings.TrimSpace(action.WorkOrderTitle)
		if len(target) > 256 {
			return target[:256]
		}
		return target
	case RuleActionProtectionCommand:
		target := action.TargetDeviceID + ":" + action.Capability
		if action.TargetPointCode != "" {
			target = action.TargetDeviceID + "/" + action.TargetPointCode + ":" + action.Capability
		}
		if len(target) > 256 {
			return target[:256]
		}
		return target
	default:
		return string(action.Kind)
	}
}

func (action RuleAction) Summary() string {
	switch action.Kind {
	case RuleActionWebhook:
		return fmt.Sprintf("webhook %s (%s)", action.SanitizedTargetRef(), action.EffectiveTriggerOn())
	case RuleActionWorkOrder:
		return fmt.Sprintf("work order [%s] %s (%s)", action.WorkOrderPriority, strings.TrimSpace(action.WorkOrderTitle), action.EffectiveTriggerOn())
	case RuleActionProtectionCommand:
		if action.TargetPointCode != "" {
			return fmt.Sprintf("protection command %s on %s/%s (%s)", action.Capability, action.TargetDeviceID, action.TargetPointCode, action.EffectiveTriggerOn())
		}
		return fmt.Sprintf("protection command %s on %s (%s)", action.Capability, action.TargetDeviceID, action.EffectiveTriggerOn())
	default:
		return string(action.Kind)
	}
}

func (action RuleAction) Validate() error {
	switch action.TriggerOn {
	case "", ActionTriggerRaise, ActionTriggerClear, ActionTriggerBoth:
	default:
		return errors.New("alarm policy action triggerOn is invalid")
	}
	switch action.Kind {
	case RuleActionWebhook:
		if action.WorkOrderPriority != "" || action.WorkOrderTitle != "" || action.TargetDeviceID != "" || action.TargetPointCode != "" || action.Capability != "" || len(action.Parameters) > 0 {
			return errors.New("webhook alarm action contains unsupported fields")
		}
		if len(action.WebhookURL) == 0 || len(action.WebhookURL) > 512 || strings.TrimSpace(action.WebhookURL) != action.WebhookURL || !strings.HasPrefix(action.WebhookURL, "https://") {
			return errors.New("alarm action webhookUrl must use https and be at most 512 chars")
		}
		parsed, err := url.Parse(action.WebhookURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return errors.New("alarm action webhookUrl is invalid")
		}
		if port := parsed.Port(); port != "" {
			num, err := strconv.Atoi(port)
			if err != nil || num < 1 || num > 65535 {
				return errors.New("alarm action webhookUrl port is invalid")
			}
		}
		if action.SecretRef != "" && !actionSecretRefPattern.MatchString(action.SecretRef) {
			return errors.New("alarm action secretRef is invalid")
		}
		return nil
	case RuleActionWorkOrder:
		if action.WebhookURL != "" || action.SecretRef != "" || action.TargetDeviceID != "" || action.TargetPointCode != "" || action.Capability != "" || len(action.Parameters) > 0 {
			return errors.New("work order alarm action contains unsupported fields")
		}
		if !validRuleWorkOrderPriority(action.WorkOrderPriority) {
			return errors.New("alarm action workOrderPriority is invalid")
		}
		if !boundedText(action.WorkOrderTitle, 200) {
			return errors.New("alarm action workOrderTitle is invalid")
		}
		return nil
	case RuleActionProtectionCommand:
		if action.WebhookURL != "" || action.SecretRef != "" || action.WorkOrderPriority != "" || action.WorkOrderTitle != "" {
			return errors.New("protection command alarm action contains unsupported fields")
		}
		if !alarmmodel.IsUUIDv7(action.TargetDeviceID) {
			return errors.New("alarm action targetDeviceId is invalid")
		}
		if action.TargetPointCode != "" && !validRulePointCode(action.TargetPointCode) {
			return errors.New("alarm action targetPointCode is invalid")
		}
		capability := commandmodel.Capability(action.Capability)
		if _, ok := commandmodel.CapabilityProfileFor(capability); !ok {
			return errors.New("alarm action capability is unsupported")
		}
		for key, value := range action.Parameters {
			if !boundedText(key, 64) || math.IsNaN(value) || math.IsInf(value, 0) {
				return errors.New("alarm action parameter is invalid")
			}
		}
		if !commandmodel.ParametersValid(capability, commandmodel.CommandParameters(action.Parameters)) {
			return errors.New("alarm action parameters do not satisfy capability profile")
		}
		return nil
	default:
		return errors.New("alarm policy action kind is invalid")
	}
}

type AlarmPolicyRevision struct {
	SchemaVersion    int                     `json:"schemaVersion"`
	PolicyID         string                  `json:"policyId"`
	PolicyRevisionID string                  `json:"policyRevisionId"`
	Revision         uint64                  `json:"revision"`
	Digest           string                  `json:"digest"`
	AlarmType        string                  `json:"alarmType"`
	SourceType       alarmmodel.SourceType   `json:"sourceType"`
	SourceReference  string                  `json:"sourceReference"`
	Title            string                  `json:"title"`
	Summary          string                  `json:"summary"`
	Severity         alarmmodel.Severity     `json:"severity"`
	RuleKind         RuleKind                `json:"ruleKind,omitempty"`
	TemplateKey      string                  `json:"templateKey,omitempty"`
	OverriddenFields []string                `json:"overriddenFields,omitempty"`
	DependsOnRuleIDs []string                `json:"dependsOnRuleIds,omitempty"`
	DependencyGate   DependencyGateMode      `json:"dependencyGate,omitempty"`
	Actions          []RuleAction            `json:"actions,omitempty"`
	QualityPolicy    EvaluationQualityPolicy `json:"qualityPolicy"`
	FreshnessSeconds int64                   `json:"freshnessSeconds"`
	TriggerMode      TriggerMode             `json:"triggerMode"`
	DurationSeconds  int64                   `json:"durationSeconds,omitempty"`
	RepeatCount      uint64                  `json:"repeatCount,omitempty"`
	Raise            Condition               `json:"raise"`
	Clear            Condition               `json:"clear"`
	Schedule         *SiteSchedule           `json:"schedule,omitempty"`
}

const MaxSlopeSamplesPerRule = 32

type TimedSample struct {
	Input      string  `json:"input"`
	ObservedAt string  `json:"observedAt"`
	Value      float64 `json:"value"`
}

type InputFact struct {
	Key        string     `json:"key"`
	Present    bool       `json:"present"`
	Revision   string     `json:"revision,omitempty"`
	Value      TypedValue `json:"value,omitempty"`
	Quality    string     `json:"quality"`
	ObservedAt string     `json:"observedAt,omitempty"`
	// UnchangedSince is when the reading took the value it still has (#616).
	UnchangedSince string `json:"unchangedSince,omitempty"`
	// QualityReasons are the displayed sample's reasons. HealthReasons are the
	// point's latest check, which can name a range rejection the displayed value
	// itself does not carry.
	QualityReasons []string                       `json:"qualityReasons,omitempty"`
	HealthReasons  []string                       `json:"healthReasons,omitempty"`
	Evidence       []alarmmodel.EvidenceReference `json:"evidence"`
}

func NumberInput(key string, value float64, quality, observedAt string) InputFact {
	return InputFact{Key: key, Present: true, Value: NumberValue(value), Quality: quality, ObservedAt: observedAt, Evidence: []alarmmodel.EvidenceReference{}}
}

func StringInput(key, value, quality, observedAt string) InputFact {
	return InputFact{Key: key, Present: true, Value: StringValue(value), Quality: quality, ObservedAt: observedAt, Evidence: []alarmmodel.EvidenceReference{}}
}

func BooleanInput(key string, value bool, quality, observedAt string) InputFact {
	return InputFact{Key: key, Present: true, Value: BooleanValue(value), Quality: quality, ObservedAt: observedAt, Evidence: []alarmmodel.EvidenceReference{}}
}

type EvaluationSnapshot struct {
	TenantID           string               `json:"tenantId"`
	SiteID             string               `json:"siteId"`
	SubjectType        string               `json:"subjectType"`
	SubjectID          string               `json:"subjectId"`
	DeviceID           *string              `json:"deviceId,omitempty"`
	EventID            *string              `json:"eventId,omitempty"`
	PointID            *string              `json:"pointId,omitempty"`
	InputRevision      string               `json:"inputRevision"`
	AsOf               string               `json:"asOf"`
	Inputs             map[string]InputFact `json:"inputs"`
	CorrelationID      string               `json:"correlationId"`
	UpstreamRuleActive map[string]bool      `json:"-"`
}

type AlarmEvaluationState struct {
	PolicyRevisionID            string           `json:"policyRevisionId"`
	Status                      EvaluationStatus `json:"status"`
	CandidateSince              *string          `json:"candidateSince,omitempty"`
	RepeatCount                 uint64           `json:"repeatCount"`
	LastInputRevision           string           `json:"lastInputRevision,omitempty"`
	QualityBlocker              string           `json:"qualityBlocker,omitempty"`
	NextEvaluationAt            *string          `json:"nextEvaluationAt,omitempty"`
	ActiveAlarmID               string           `json:"activeAlarmId,omitempty"`
	ActiveIncidentCorrelationID string           `json:"activeIncidentCorrelationId,omitempty"`
	SampleWindow                []TimedSample    `json:"sampleWindow,omitempty"`
	LastEvaluatedAt             string           `json:"lastEvaluatedAt,omitempty"`
	Version                     uint64           `json:"version"`
}

type EvaluationDecision struct {
	Status      EvaluationStatus     `json:"status"`
	Effect      EvaluationEffect     `json:"effect"`
	Fingerprint string               `json:"fingerprint"`
	State       AlarmEvaluationState `json:"state"`
	Publication *Publication         `json:"-"`
	Recovery    *Recovery            `json:"-"`
}

type predicateResult struct {
	status  EvaluationStatus
	blocker string
	nextDue *time.Time
}

func EvaluatePolicy(policy AlarmPolicyRevision, snapshot EvaluationSnapshot, previous AlarmEvaluationState, now time.Time) (EvaluationDecision, error) {
	if err := policy.Validate(); err != nil {
		return EvaluationDecision{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return EvaluationDecision{}, err
	}
	now = now.UTC()
	if snapshotTime, _ := time.Parse(time.RFC3339Nano, snapshot.AsOf); snapshotTime.After(now) {
		return EvaluationDecision{}, errors.New("evaluation snapshot is from the future")
	}
	fingerprint, err := alarmmodel.Fingerprint(snapshot.TenantID, snapshot.SiteID, policy.SourceType, policy.SourceReference, policy.AlarmType, snapshot.DeviceID, snapshot.PointID)
	if err != nil {
		return EvaluationDecision{}, err
	}

	state := previous
	policyChanged := state.PolicyRevisionID != "" && state.PolicyRevisionID != policy.PolicyRevisionID
	if state.PolicyRevisionID == "" || policyChanged {
		activeAlarmID := state.ActiveAlarmID
		activeCorrelationID := state.ActiveIncidentCorrelationID
		state = AlarmEvaluationState{PolicyRevisionID: policy.PolicyRevisionID, Status: EvaluationNotMatched, ActiveAlarmID: activeAlarmID, ActiveIncidentCorrelationID: activeCorrelationID, Version: previous.Version}
		if activeCorrelationID != "" {
			state.Status = EvaluationMatched
		}
	}
	newInput := snapshot.InputRevision != state.LastInputRevision || policyChanged
	if !newInput && !evaluationIsDue(state.NextEvaluationAt, now) {
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	}
	if newInput {
		state.SampleWindow = updateSlopeSampleWindow(policy, snapshot, state.SampleWindow, now)
	}

	active, nextScheduleBoundary, err := scheduleState(policy.Schedule, now)
	if err != nil {
		return EvaluationDecision{}, err
	}
	if !active {
		state.Status = EvaluationIndeterminate
		state.QualityBlocker = QualityBlockerOutsideSchedule
		state.CandidateSince = nil
		state.RepeatCount = 0
		state.NextEvaluationAt = formatOptionalTime(nextScheduleBoundary)
		finishEvaluationState(&state, snapshot, now)
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	}

	if state.ActiveIncidentCorrelationID != "" {
		clearResult := evaluateCondition(policy, policy.Clear, snapshot, state.SampleWindow, true, now)
		if clearResult.status == EvaluationIndeterminate {
			state.Status = EvaluationIndeterminate
			state.QualityBlocker = clearResult.blocker
			state.NextEvaluationAt = formatOptionalTime(minTime(clearResult.nextDue, nextScheduleBoundary))
			finishEvaluationState(&state, snapshot, now)
			return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
		}
		if clearResult.status == EvaluationMatched {
			state.Status = EvaluationNotMatched
			state.QualityBlocker = ""
			state.CandidateSince = nil
			state.RepeatCount = 0
			// A time-based raise (no data for a while) can come true again with no new reading (#615).
			raiseResult := evaluateRaiseCondition(policy, snapshot, state.SampleWindow, false, now)
			state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
			finishEvaluationState(&state, snapshot, now)
			recovery := recoveryForDecision(policy, snapshot, state.ActiveIncidentCorrelationID, fingerprint, now)
			return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectClear, Fingerprint: fingerprint, State: state, Recovery: &recovery}, nil
		}

		raiseResult := evaluateRaiseCondition(policy, snapshot, state.SampleWindow, true, now)
		state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
		state.CandidateSince = nil
		state.RepeatCount = 0
		if raiseResult.status == EvaluationIndeterminate {
			state.Status = EvaluationIndeterminate
			state.QualityBlocker = raiseResult.blocker
			finishEvaluationState(&state, snapshot, now)
			return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
		}
		state.Status = EvaluationMatched
		state.QualityBlocker = ""
		finishEvaluationState(&state, snapshot, now)
		if raiseResult.status == EvaluationMatched && policyChanged {
			publication := publicationForDecision(policy, snapshot, now)
			return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectPublish, Fingerprint: fingerprint, State: state, Publication: &publication}, nil
		}
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	}

	raiseResult := evaluateRaiseCondition(policy, snapshot, state.SampleWindow, false, now)
	if raiseResult.status == EvaluationIndeterminate {
		state.Status = EvaluationIndeterminate
		state.QualityBlocker = raiseResult.blocker
		state.CandidateSince = nil
		state.RepeatCount = 0
		state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
		finishEvaluationState(&state, snapshot, now)
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	}
	if raiseResult.status == EvaluationNotMatched {
		state.Status = EvaluationNotMatched
		state.QualityBlocker = ""
		state.CandidateSince = nil
		state.RepeatCount = 0
		state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
		finishEvaluationState(&state, snapshot, now)
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	}

	state.QualityBlocker = ""
	switch policy.TriggerMode {
	case TriggerSimple:
		state.Status = EvaluationMatched
		state.CandidateSince = nil
		state.RepeatCount = 0
		state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
		finishEvaluationState(&state, snapshot, now)
		publication := publicationForDecision(policy, snapshot, now)
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectPublish, Fingerprint: fingerprint, State: state, Publication: &publication}, nil
	case TriggerDuration:
		if state.CandidateSince == nil {
			candidate := now.Format(time.RFC3339Nano)
			state.CandidateSince = &candidate
		}
		candidate, err := time.Parse(time.RFC3339Nano, *state.CandidateSince)
		if err != nil {
			return EvaluationDecision{}, errors.New("persisted duration candidate is invalid")
		}
		due := candidate.Add(time.Duration(policy.DurationSeconds) * time.Second)
		if !now.Before(due) {
			state.Status = EvaluationMatched
			state.CandidateSince = nil
			state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
			finishEvaluationState(&state, snapshot, now)
			publication := publicationForDecision(policy, snapshot, now)
			return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectPublish, Fingerprint: fingerprint, State: state, Publication: &publication}, nil
		}
		state.Status = EvaluationNotMatched
		state.NextEvaluationAt = formatOptionalTime(minTime(&due, raiseResult.nextDue, nextScheduleBoundary))
		finishEvaluationState(&state, snapshot, now)
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	case TriggerRepeating:
		if newInput {
			state.RepeatCount++
		}
		if state.RepeatCount >= policy.RepeatCount {
			state.Status = EvaluationMatched
			state.CandidateSince = nil
			state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
			finishEvaluationState(&state, snapshot, now)
			publication := publicationForDecision(policy, snapshot, now)
			return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectPublish, Fingerprint: fingerprint, State: state, Publication: &publication}, nil
		}
		state.Status = EvaluationNotMatched
		state.NextEvaluationAt = formatOptionalTime(minTime(raiseResult.nextDue, nextScheduleBoundary))
		finishEvaluationState(&state, snapshot, now)
		return EvaluationDecision{Status: state.Status, Effect: EvaluationEffectNone, Fingerprint: fingerprint, State: state}, nil
	default:
		return EvaluationDecision{}, errors.New("alarm trigger mode is invalid")
	}
}

func AlarmPolicyDigest(policy AlarmPolicyRevision) (string, error) {
	copyPolicy := policy
	copyPolicy.Digest = ""
	encoded, err := json.Marshal(copyPolicy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (policy AlarmPolicyRevision) EffectiveRuleKind() RuleKind {
	if policy.RuleKind != "" {
		return policy.RuleKind
	}
	if conditionHasKind(policy.Raise, ConditionExpression) || conditionHasKind(policy.Clear, ConditionExpression) {
		return RuleKindExpression
	}
	if conditionHasKind(policy.Raise, ConditionSlope) || conditionHasKind(policy.Clear, ConditionSlope) {
		return RuleKindSlope
	}
	return RuleKindThreshold
}

func conditionHasKind(condition Condition, target ConditionKind) bool {
	if condition.Kind == target {
		return true
	}
	for _, child := range condition.Children {
		if conditionHasKind(child, target) {
			return true
		}
	}
	return false
}

// ReferencedInputs returns the sorted, deduplicated snapshot input keys read by policy.
func (policy AlarmPolicyRevision) ReferencedInputs() []string {
	seen := map[string]struct{}{}
	collectConditionInputs(policy.Raise, seen)
	collectConditionInputs(policy.Clear, seen)
	if len(seen) == 0 {
		return nil
	}
	inputs := make([]string, 0, len(seen))
	for key := range seen {
		inputs = append(inputs, key)
	}
	sort.Strings(inputs)
	return inputs
}

func collectConditionInputs(condition Condition, inputs map[string]struct{}) {
	if len(condition.Children) > 0 {
		for _, child := range condition.Children {
			collectConditionInputs(child, inputs)
		}
		return
	}
	if condition.Kind == ConditionExpression {
		for _, key := range condition.ExprInputs {
			if key != "" {
				inputs[key] = struct{}{}
			}
		}
		return
	}
	if condition.Input != "" {
		inputs[condition.Input] = struct{}{}
	}
}

func slopeInputWindows(policy AlarmPolicyRevision) map[string]int64 {
	windows := map[string]int64{}
	collectSlopeWindows(policy.Raise, windows)
	collectSlopeWindows(policy.Clear, windows)
	return windows
}

func collectSlopeWindows(condition Condition, windows map[string]int64) {
	if condition.Kind == ConditionSlope && condition.Input != "" {
		if condition.WindowSeconds > windows[condition.Input] {
			windows[condition.Input] = condition.WindowSeconds
		}
		return
	}
	for _, child := range condition.Children {
		collectSlopeWindows(child, windows)
	}
}

func (policy AlarmPolicyRevision) Validate() error {
	if policy.SchemaVersion != AlarmPolicySchemaVersion || !alarmmodel.IsUUIDv7(policy.PolicyID) || !alarmmodel.IsUUIDv7(policy.PolicyRevisionID) || policy.Revision == 0 || !policyDigestPattern.MatchString(policy.Digest) {
		return errors.New("alarm policy revision identity is invalid")
	}
	expectedDigest, err := AlarmPolicyDigest(policy)
	if err != nil || expectedDigest != policy.Digest {
		return errors.New("alarm policy revision digest is invalid")
	}
	if !boundedText(policy.AlarmType, 128) || !boundedText(policy.SourceReference, 512) || !boundedText(policy.Title, 256) || !boundedText(policy.Summary, 2048) || !validAlarmSource(policy.SourceType) || !validAlarmSeverity(policy.Severity) {
		return errors.New("alarm policy projection is invalid")
	}
	if policy.RuleKind != "" && !validRuleKind(policy.RuleKind) {
		return errors.New("alarm policy rule kind is invalid")
	}
	if policy.TemplateKey != "" && (len(policy.TemplateKey) > 128 || !boundedText(policy.TemplateKey, 128)) {
		return errors.New("alarm policy template key is invalid")
	}
	if len(policy.OverriddenFields) > 32 {
		return errors.New("alarm policy overridden fields exceed maximum")
	}
	for _, field := range policy.OverriddenFields {
		if len(field) > 128 || !boundedText(field, 128) {
			return errors.New("alarm policy overridden field is invalid")
		}
	}
	if err := validateRuleDependencies(policy.PolicyID, policy.DependsOnRuleIDs); err != nil {
		return err
	}
	if err := validateDependencyGate(policy.DependencyGate, policy.DependsOnRuleIDs); err != nil {
		return err
	}
	if err := validateRuleActions(policy.Actions); err != nil {
		return err
	}
	if policy.QualityPolicy != EvaluationQualityValidOnly && policy.QualityPolicy != EvaluationQualityUsable || policy.FreshnessSeconds <= 0 {
		return errors.New("alarm policy quality gate is invalid")
	}
	if err := policy.Raise.Validate(); err != nil {
		return fmt.Errorf("alarm policy raise predicate: %w", err)
	}
	if err := policy.Clear.Validate(); err != nil {
		return fmt.Errorf("alarm policy clear predicate: %w", err)
	}
	switch policy.TriggerMode {
	case TriggerSimple:
		if policy.DurationSeconds != 0 || policy.RepeatCount != 0 {
			return errors.New("simple alarm policy has stateful trigger configuration")
		}
	case TriggerDuration:
		if policy.DurationSeconds <= 0 || policy.RepeatCount != 0 {
			return errors.New("duration alarm policy is invalid")
		}
	case TriggerRepeating:
		if policy.RepeatCount == 0 || policy.DurationSeconds != 0 {
			return errors.New("repeating alarm policy is invalid")
		}
	default:
		return errors.New("alarm policy trigger mode is invalid")
	}
	if policy.Schedule != nil {
		if err := policy.Schedule.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (condition Condition) Validate() error {
	switch condition.Kind {
	case ConditionCompare:
		if !boundedText(condition.Input, maxSnapshotInputKeyBytes) || !validCompareOperator(condition.Operator) || !condition.Value.Valid() {
			return errors.New("compare predicate is invalid")
		}
		if condition.Operator != CompareEQ && condition.Operator != CompareNE && condition.Value.Type != EvaluationValueNumber {
			return errors.New("ordered compare predicate requires a numeric value")
		}
	case ConditionRange:
		if !boundedText(condition.Input, maxSnapshotInputKeyBytes) || condition.Minimum == nil || condition.Maximum == nil || *condition.Minimum > *condition.Maximum {
			return errors.New("range predicate is invalid")
		}
	case ConditionHysteresis:
		if !boundedText(condition.Input, maxSnapshotInputKeyBytes) {
			return errors.New("hysteresis input is invalid")
		}
		if condition.Direction == DirectionAbove && condition.Reset >= condition.Trigger || condition.Direction == DirectionBelow && condition.Reset <= condition.Trigger {
			return errors.New("hysteresis thresholds are invalid")
		}
		if condition.Direction != DirectionAbove && condition.Direction != DirectionBelow {
			return errors.New("hysteresis direction is invalid")
		}
	case ConditionNoData, ConditionStale, ConditionUnchanged, ConditionChanged:
		if !boundedText(condition.Input, maxSnapshotInputKeyBytes) || condition.AgeSeconds <= 0 {
			return errors.New("time predicate is invalid")
		}
	case ConditionReasons:
		if !boundedText(condition.Input, maxSnapshotInputKeyBytes) || (condition.Match != ReasonMatchAny && condition.Match != ReasonMatchNone) || len(condition.Reasons) < 1 || len(condition.Reasons) > 8 {
			return errors.New("quality-reason predicate is invalid")
		}
		for _, reason := range condition.Reasons {
			if !validConditionReason(reason) {
				return errors.New("quality-reason predicate names an unknown reason")
			}
		}
	case ConditionSlope:
		if !boundedText(condition.Input, maxSnapshotInputKeyBytes) || !validSlopeOperator(condition.Operator) || condition.Delta == nil || math.IsNaN(*condition.Delta) || math.IsInf(*condition.Delta, 0) || condition.WindowSeconds < 1 || condition.WindowSeconds > 86400 {
			return errors.New("slope predicate is invalid")
		}
	case ConditionExpression:
		if len(condition.ExprInputs) > MaxExpressionInputs {
			return errors.New("expression predicate input count exceeds maximum")
		}
		for _, inputKey := range condition.ExprInputs {
			if !boundedText(inputKey, maxSnapshotInputKeyBytes) {
				return errors.New("expression predicate input key is invalid")
			}
		}
		if err := ValidateExpression(condition.Expression, condition.ExprInputs); err != nil {
			return fmt.Errorf("expression predicate is invalid: %w", err)
		}
	case ConditionAnd, ConditionOr:
		if len(condition.Children) < 2 || len(condition.Children) > 32 {
			return errors.New("compound predicate child count is invalid")
		}
		for _, child := range condition.Children {
			if err := child.Validate(); err != nil {
				return err
			}
		}
	default:
		return errors.New("alarm predicate kind is invalid")
	}
	return nil
}

func (value TypedValue) Valid() bool {
	return value.Type == EvaluationValueNumber || value.Type == EvaluationValueString || value.Type == EvaluationValueBoolean
}

// MaxEvaluationSnapshotInputs bounds the number of Site-level Point inputs carried in a single evaluation snapshot.
const MaxEvaluationSnapshotInputs = 512

// maxSnapshotInputKeyBytes bounds canonical "<uuidv7>/<pointCode>" keys (36 + 1 + 128).
const maxSnapshotInputKeyBytes = 165

func validateRuleDependencies(ownerID string, deps []string) error {
	if len(deps) > 16 {
		return errors.New("alarm policy dependencies exceed maximum")
	}
	seen := make(map[string]struct{}, len(deps))
	for _, depID := range deps {
		if !alarmmodel.IsUUIDv7(depID) || (ownerID != "" && depID == ownerID) {
			return errors.New("alarm policy dependency rule id is invalid")
		}
		if _, duplicate := seen[depID]; duplicate {
			return errors.New("alarm policy dependency rule id is duplicated")
		}
		seen[depID] = struct{}{}
	}
	return nil
}

func (snapshot EvaluationSnapshot) Validate() error {
	if !alarmmodel.IsUUIDv7(snapshot.TenantID) || !alarmmodel.IsUUIDv7(snapshot.SiteID) || !validEvaluationSubjectType(snapshot.SubjectType) || !alarmmodel.IsUUIDv7(snapshot.SubjectID) || !boundedText(snapshot.InputRevision, 256) || !boundedText(snapshot.CorrelationID, 256) {
		return errors.New("alarm evaluation snapshot identity is invalid")
	}
	if snapshot.DeviceID != nil && !alarmmodel.IsUUIDv7(*snapshot.DeviceID) || snapshot.EventID != nil && !alarmmodel.IsUUIDv7(*snapshot.EventID) || snapshot.PointID != nil && !alarmmodel.IsUUIDv7(*snapshot.PointID) {
		return errors.New("alarm evaluation linked identity is invalid")
	}
	switch snapshot.SubjectType {
	case "SITE":
		if snapshot.SubjectID != snapshot.SiteID {
			return errors.New("alarm evaluation Site subject is inconsistent")
		}
	case "DEVICE":
		if snapshot.DeviceID == nil || snapshot.SubjectID != *snapshot.DeviceID {
			return errors.New("alarm evaluation Device subject is inconsistent")
		}
	case "POINT":
		if snapshot.PointID == nil || snapshot.SubjectID != *snapshot.PointID {
			return errors.New("alarm evaluation Point subject is inconsistent")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.AsOf); err != nil {
		return errors.New("alarm evaluation snapshot time is invalid")
	}
	if snapshot.Inputs == nil || len(snapshot.Inputs) > MaxEvaluationSnapshotInputs {
		return errors.New("alarm evaluation snapshot inputs are invalid")
	}
	for key, input := range snapshot.Inputs {
		if key != input.Key || !boundedText(key, maxSnapshotInputKeyBytes) {
			return errors.New("alarm evaluation input key is invalid")
		}
		if input.Present {
			if !input.Value.Valid() || !boundedText(input.Quality, 32) {
				return errors.New("alarm evaluation input value is invalid")
			}
			if _, err := time.Parse(time.RFC3339Nano, input.ObservedAt); err != nil {
				return errors.New("alarm evaluation input time is invalid")
			}
		}
	}
	return nil
}

func (schedule SiteSchedule) Validate() error {
	if strings.TrimSpace(schedule.Timezone) == "" || len(schedule.Windows) == 0 || len(schedule.Windows) > 64 {
		return errors.New("alarm site schedule is invalid")
	}
	if _, err := time.LoadLocation(schedule.Timezone); err != nil {
		return errors.New("alarm site schedule timezone is invalid")
	}
	for _, window := range schedule.Windows {
		if len(window.DaysOfWeek) == 0 || window.StartsOnMinute < 0 || window.StartsOnMinute >= 1440 || window.EndsOnMinute < 0 || window.EndsOnMinute > 1440 {
			return errors.New("alarm site schedule window is invalid")
		}
		seen := map[int]struct{}{}
		for _, day := range window.DaysOfWeek {
			if day < 1 || day > 7 {
				return errors.New("alarm site schedule weekday is invalid")
			}
			if _, duplicate := seen[day]; duplicate {
				return errors.New("alarm site schedule weekday is duplicated")
			}
			seen[day] = struct{}{}
		}
	}
	return nil
}

func evaluateCondition(policy AlarmPolicyRevision, condition Condition, snapshot EvaluationSnapshot, sampleWindow []TimedSample, active bool, now time.Time) predicateResult {
	switch condition.Kind {
	case ConditionAnd:
		return evaluateAnd(policy, condition.Children, snapshot, sampleWindow, active, now)
	case ConditionOr:
		return evaluateOr(policy, condition.Children, snapshot, sampleWindow, active, now)
	case ConditionNoData:
		return evaluateNoData(condition, snapshot, now)
	case ConditionStale:
		return evaluateStale(condition, snapshot, now)
	case ConditionChanged:
		return evaluateChanged(condition, snapshot, now)
	case ConditionReasons:
		return evaluateReasons(condition, snapshot)
	case ConditionExpression:
		return evaluateExpression(policy, condition, snapshot, now)
	case ConditionSlope:
		return evaluateSlope(policy, condition, snapshot, sampleWindow, now)
	}

	input, exists := snapshot.Inputs[condition.Input]
	trusted, blocker, nextDue := trustedInput(policy, condition.Input, input, exists, now)
	if !trusted {
		return predicateResult{status: EvaluationIndeterminate, blocker: blocker, nextDue: nextDue}
	}
	switch condition.Kind {
	case ConditionCompare:
		matched := compareTyped(input.Value, condition.Operator, condition.Value)
		return truthResult(matched, nextDue)
	case ConditionRange:
		if input.Value.Type != EvaluationValueNumber {
			return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input, nextDue: nextDue}
		}
		return truthResult(input.Value.Number >= *condition.Minimum && input.Value.Number <= *condition.Maximum, nextDue)
	case ConditionHysteresis:
		if input.Value.Type != EvaluationValueNumber {
			return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input, nextDue: nextDue}
		}
		if condition.Direction == DirectionAbove {
			if active {
				return truthResult(input.Value.Number > condition.Reset, nextDue)
			}
			return truthResult(input.Value.Number >= condition.Trigger, nextDue)
		}
		if active {
			return truthResult(input.Value.Number < condition.Reset, nextDue)
		}
		return truthResult(input.Value.Number <= condition.Trigger, nextDue)
	case ConditionUnchanged:
		since, err := time.Parse(time.RFC3339Nano, input.UnchangedSince)
		if err != nil {
			return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input, nextDue: nextDue}
		}
		due := since.Add(time.Duration(condition.AgeSeconds) * time.Second)
		if !now.Before(due) {
			return truthResult(true, nextDue)
		}
		return predicateResult{status: EvaluationNotMatched, nextDue: minTime(&due, nextDue)}
	default:
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input}
	}
}

func updateSlopeSampleWindow(policy AlarmPolicyRevision, snapshot EvaluationSnapshot, previous []TimedSample, now time.Time) []TimedSample {
	windows := slopeInputWindows(policy)
	if len(windows) == 0 {
		return nil
	}
	type indexedSample struct {
		sample TimedSample
		at     time.Time
	}
	kept := make([]indexedSample, 0, len(previous)+len(windows))
	latestByInput := make(map[string]time.Time, len(windows))
	for _, sample := range previous {
		windowSeconds, ok := windows[sample.Input]
		if !ok || math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, sample.ObservedAt)
		if err != nil {
			continue
		}
		at = at.UTC()
		if at.Before(now.Add(-time.Duration(windowSeconds) * time.Second)) {
			continue
		}
		sample.ObservedAt = at.Format(time.RFC3339Nano)
		kept = append(kept, indexedSample{sample: sample, at: at})
		if prev, exists := latestByInput[sample.Input]; !exists || at.After(prev) {
			latestByInput[sample.Input] = at
		}
	}

	keys := make([]string, 0, len(windows))
	for key := range windows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fact, exists := snapshot.Inputs[key]
		trusted, _, _ := trustedInput(policy, key, fact, exists, now)
		if !trusted || fact.Value.Type != EvaluationValueNumber || math.IsNaN(fact.Value.Number) || math.IsInf(fact.Value.Number, 0) {
			continue
		}
		observedAt, err := time.Parse(time.RFC3339Nano, fact.ObservedAt)
		if err != nil {
			continue
		}
		observedAt = observedAt.UTC()
		if observedAt.Before(now.Add(-time.Duration(windows[key]) * time.Second)) {
			continue
		}
		if prev, exists := latestByInput[key]; exists && !observedAt.After(prev) {
			continue
		}
		kept = append(kept, indexedSample{
			sample: TimedSample{
				Input:      key,
				ObservedAt: observedAt.Format(time.RFC3339Nano),
				Value:      fact.Value.Number,
			},
			at: observedAt,
		})
		latestByInput[key] = observedAt
	}
	if len(kept) == 0 {
		return nil
	}
	sort.Slice(kept, func(left, right int) bool {
		if !kept[left].at.Equal(kept[right].at) {
			return kept[left].at.Before(kept[right].at)
		}
		return kept[left].sample.Input < kept[right].sample.Input
	})
	if len(kept) > MaxSlopeSamplesPerRule {
		kept = kept[len(kept)-MaxSlopeSamplesPerRule:]
	}
	result := make([]TimedSample, len(kept))
	for index, item := range kept {
		result[index] = item.sample
	}
	return result
}

func evaluateSlope(policy AlarmPolicyRevision, condition Condition, snapshot EvaluationSnapshot, sampleWindow []TimedSample, now time.Time) predicateResult {
	input, exists := snapshot.Inputs[condition.Input]
	trusted, blocker, nextDue := trustedInput(policy, condition.Input, input, exists, now)
	if !trusted {
		return predicateResult{status: EvaluationIndeterminate, blocker: blocker, nextDue: nextDue}
	}
	if input.Value.Type != EvaluationValueNumber || math.IsNaN(input.Value.Number) || math.IsInf(input.Value.Number, 0) {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input, nextDue: nextDue}
	}
	asOfTime, err := time.Parse(time.RFC3339Nano, snapshot.AsOf)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input, nextDue: nextDue}
	}
	currentObservedAt, err := time.Parse(time.RFC3339Nano, input.ObservedAt)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input, nextDue: nextDue}
	}
	windowStart := asOfTime.Add(-time.Duration(condition.WindowSeconds) * time.Second)
	var earliestTime time.Time
	var earliestValue float64
	found := false
	for _, sample := range sampleWindow {
		if sample.Input != condition.Input || math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
			continue
		}
		sampleTime, err := time.Parse(time.RFC3339Nano, sample.ObservedAt)
		if err != nil {
			continue
		}
		if sampleTime.Before(windowStart) || sampleTime.After(asOfTime) || !sampleTime.Before(currentObservedAt) {
			continue
		}
		if !found || sampleTime.Before(earliestTime) {
			earliestTime = sampleTime
			earliestValue = sample.Value
			found = true
		}
	}
	if !found {
		return predicateResult{status: EvaluationNotMatched, nextDue: nextDue}
	}
	change := input.Value.Number - earliestValue
	return truthResult(compareSlope(change, condition.Operator, *condition.Delta), nextDue)
}

func compareSlope(change float64, operator CompareOperator, delta float64) bool {
	switch operator {
	case CompareGT:
		return change > delta
	case CompareGTE:
		return change >= delta
	case CompareLT:
		return change < delta
	case CompareLTE:
		return change <= delta
	default:
		return false
	}
}

func evaluateAnd(policy AlarmPolicyRevision, children []Condition, snapshot EvaluationSnapshot, sampleWindow []TimedSample, active bool, now time.Time) predicateResult {
	result := predicateResult{status: EvaluationMatched}
	for _, child := range children {
		childResult := evaluateCondition(policy, child, snapshot, sampleWindow, active, now)
		result.nextDue = minTime(result.nextDue, childResult.nextDue)
		if childResult.status == EvaluationNotMatched {
			return predicateResult{status: EvaluationNotMatched, nextDue: result.nextDue}
		}
		if childResult.status == EvaluationIndeterminate && result.status != EvaluationIndeterminate {
			result.status = EvaluationIndeterminate
			result.blocker = childResult.blocker
		}
	}
	return result
}

func evaluateOr(policy AlarmPolicyRevision, children []Condition, snapshot EvaluationSnapshot, sampleWindow []TimedSample, active bool, now time.Time) predicateResult {
	result := predicateResult{status: EvaluationNotMatched}
	for _, child := range children {
		childResult := evaluateCondition(policy, child, snapshot, sampleWindow, active, now)
		result.nextDue = minTime(result.nextDue, childResult.nextDue)
		if childResult.status == EvaluationMatched {
			return predicateResult{status: EvaluationMatched, nextDue: result.nextDue}
		}
		if childResult.status == EvaluationIndeterminate && result.status != EvaluationIndeterminate {
			result.status = EvaluationIndeterminate
			result.blocker = childResult.blocker
		}
	}
	return result
}

func evaluateNoData(condition Condition, snapshot EvaluationSnapshot, now time.Time) predicateResult {
	input, exists := snapshot.Inputs[condition.Input]
	if !exists || strings.TrimSpace(input.ObservedAt) == "" {
		return predicateResult{status: EvaluationMatched}
	}
	observedAt, err := time.Parse(time.RFC3339Nano, input.ObservedAt)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input}
	}
	due := observedAt.Add(time.Duration(condition.AgeSeconds) * time.Second)
	if !now.Before(due) {
		return predicateResult{status: EvaluationMatched}
	}
	return predicateResult{status: EvaluationNotMatched, nextDue: &due}
}

func evaluateChanged(condition Condition, snapshot EvaluationSnapshot, now time.Time) predicateResult {
	input, exists := snapshot.Inputs[condition.Input]
	if !exists || !input.Present || input.UnchangedSince == "" {
		return predicateResult{status: EvaluationMatched}
	}
	since, err := time.Parse(time.RFC3339Nano, input.UnchangedSince)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input}
	}
	return truthResult(now.Before(since.Add(time.Duration(condition.AgeSeconds)*time.Second)), nil)
}

func evaluateReasons(condition Condition, snapshot EvaluationSnapshot) predicateResult {
	input, exists := snapshot.Inputs[condition.Input]
	reasons := input.HealthReasons
	if len(reasons) == 0 {
		reasons = input.QualityReasons
	}
	if (!exists || !input.Present) && len(reasons) == 0 {
		return truthResult(condition.Match == ReasonMatchNone, nil)
	}
	hit := false
	for _, reason := range reasons {
		if slices.Contains(condition.Reasons, reason) {
			hit = true
			break
		}
	}
	if condition.Match == ReasonMatchNone {
		return truthResult(!hit, nil)
	}
	return truthResult(hit, nil)
}

func evaluateStale(condition Condition, snapshot EvaluationSnapshot, now time.Time) predicateResult {
	input, exists := snapshot.Inputs[condition.Input]
	if !exists || !input.Present || strings.TrimSpace(input.ObservedAt) == "" {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerMissingPrefix + condition.Input}
	}
	observedAt, err := time.Parse(time.RFC3339Nano, input.ObservedAt)
	if err != nil {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input}
	}
	if input.Quality == "INVALID" {
		return predicateResult{status: EvaluationIndeterminate, blocker: qualityBlockerInvalidPrefix + condition.Input}
	}
	if input.Quality == "STALE" {
		return predicateResult{status: EvaluationMatched}
	}
	due := observedAt.Add(time.Duration(condition.AgeSeconds) * time.Second)
	if !now.Before(due) {
		return predicateResult{status: EvaluationMatched}
	}
	return predicateResult{status: EvaluationNotMatched, nextDue: &due}
}

func trustedInput(policy AlarmPolicyRevision, key string, input InputFact, exists bool, now time.Time) (bool, string, *time.Time) {
	if !exists || !input.Present {
		return false, qualityBlockerMissingPrefix + key, nil
	}
	observedAt, err := time.Parse(time.RFC3339Nano, input.ObservedAt)
	if err != nil {
		return false, qualityBlockerInvalidPrefix + key, nil
	}
	if input.Quality == "STALE" {
		return false, qualityBlockerStalePrefix + key, nil
	}
	if input.Quality == "INVALID" {
		return false, qualityBlockerInvalidPrefix + key, nil
	}
	if !qualityAllowed(policy.QualityPolicy, input.Quality) {
		return false, qualityBlockerQualityPrefix + key + ":" + input.Quality, nil
	}
	freshUntil := observedAt.Add(time.Duration(policy.FreshnessSeconds) * time.Second)
	if !now.Before(freshUntil) {
		return false, qualityBlockerStalePrefix + key, nil
	}
	return true, "", &freshUntil
}

func qualityAllowed(policy EvaluationQualityPolicy, quality string) bool {
	if policy == EvaluationQualityValidOnly {
		return quality == "GOOD"
	}
	switch quality {
	case "GOOD", "PARTIAL", "ESTIMATED", "MANUAL":
		return true
	default:
		return false
	}
}

func compareTyped(actual TypedValue, operator CompareOperator, expected TypedValue) bool {
	if actual.Type != expected.Type {
		return false
	}
	switch actual.Type {
	case EvaluationValueNumber:
		switch operator {
		case CompareEQ:
			return actual.Number == expected.Number
		case CompareNE:
			return actual.Number != expected.Number
		case CompareGT:
			return actual.Number > expected.Number
		case CompareGTE:
			return actual.Number >= expected.Number
		case CompareLT:
			return actual.Number < expected.Number
		case CompareLTE:
			return actual.Number <= expected.Number
		}
	case EvaluationValueString:
		if operator == CompareEQ {
			return actual.String == expected.String
		}
		if operator == CompareNE {
			return actual.String != expected.String
		}
	case EvaluationValueBoolean:
		if operator == CompareEQ {
			return actual.Boolean == expected.Boolean
		}
		if operator == CompareNE {
			return actual.Boolean != expected.Boolean
		}
	}
	return false
}

func scheduleState(schedule *SiteSchedule, now time.Time) (bool, *time.Time, error) {
	if schedule == nil {
		return true, nil, nil
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return false, nil, err
	}
	localNow := now.In(location)
	var activeEnd *time.Time
	var nextStart *time.Time
	for offset := -1; offset <= 8; offset++ {
		date := localNow.AddDate(0, 0, offset)
		day := isoWeekday(date)
		for _, window := range schedule.Windows {
			if !containsDay(window.DaysOfWeek, day) {
				continue
			}
			start := localMinute(date, window.StartsOnMinute, location)
			endDate := date
			if window.EndsOnMinute <= window.StartsOnMinute {
				endDate = endDate.AddDate(0, 0, 1)
			}
			endMinute := window.EndsOnMinute
			if endMinute == 1440 {
				endDate = date.AddDate(0, 0, 1)
				endMinute = 0
			}
			end := localMinute(endDate, endMinute, location)
			if !now.Before(start) && now.Before(end) {
				candidate := end.UTC()
				activeEnd = minTime(activeEnd, &candidate)
			}
			if start.After(now) {
				candidate := start.UTC()
				nextStart = minTime(nextStart, &candidate)
			}
		}
	}
	if activeEnd != nil {
		return true, activeEnd, nil
	}
	return false, nextStart, nil
}

func localMinute(date time.Time, minute int, location *time.Location) time.Time {
	hour := minute / 60
	minutes := minute % 60
	return time.Date(date.Year(), date.Month(), date.Day(), hour, minutes, 0, 0, location)
}

func isoWeekday(value time.Time) int {
	if value.Weekday() == time.Sunday {
		return 7
	}
	return int(value.Weekday())
}

func containsDay(days []int, expected int) bool {
	for _, day := range days {
		if day == expected {
			return true
		}
	}
	return false
}

func truthResult(matched bool, nextDue *time.Time) predicateResult {
	if matched {
		return predicateResult{status: EvaluationMatched, nextDue: nextDue}
	}
	return predicateResult{status: EvaluationNotMatched, nextDue: nextDue}
}

func evaluationIsDue(value *string, now time.Time) bool {
	if value == nil {
		return false
	}
	due, err := time.Parse(time.RFC3339Nano, *value)
	return err == nil && !now.Before(due)
}

func minTime(values ...*time.Time) *time.Time {
	var result *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		canonical := value.UTC()
		if result == nil || canonical.Before(*result) {
			copyValue := canonical
			result = &copyValue
		}
	}
	return result
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func finishEvaluationState(state *AlarmEvaluationState, snapshot EvaluationSnapshot, now time.Time) {
	state.LastInputRevision = snapshot.InputRevision
	state.LastEvaluatedAt = now.UTC().Format(time.RFC3339Nano)
	state.Version++
}

func publicationForDecision(policy AlarmPolicyRevision, snapshot EvaluationSnapshot, now time.Time) Publication {
	return Publication{
		DeviceID:        snapshot.DeviceID,
		EventID:         snapshot.EventID,
		PointID:         snapshot.PointID,
		AlarmType:       policy.AlarmType,
		SourceType:      policy.SourceType,
		SourceReference: policy.SourceReference,
		RuleRevision:    policy.PolicyRevisionID,
		Title:           policy.Title,
		Summary:         policy.Summary,
		Severity:        policy.Severity,
		OccurredAt:      now.UTC().Format(time.RFC3339Nano),
		Evidence:        snapshotEvidence(snapshot),
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   snapshot.CorrelationID,
	}
}

func recoveryForDecision(policy AlarmPolicyRevision, snapshot EvaluationSnapshot, incidentCorrelationID, fingerprint string, now time.Time) Recovery {
	return Recovery{
		Fingerprint:           fingerprint,
		IncidentCorrelationID: incidentCorrelationID,
		OccurredAt:            now.UTC().Format(time.RFC3339Nano),
		Reason:                "CLEAR_PREDICATE_MATCHED",
		Evidence:              snapshotEvidence(snapshot),
		RuleRevision:          policy.PolicyRevisionID,
		ActorType:             "WORKLOAD",
		ActorID:               "alarm-evaluator",
		CorrelationID:         snapshot.CorrelationID,
	}
}

func snapshotEvidence(snapshot EvaluationSnapshot) []alarmmodel.EvidenceReference {
	keys := make([]string, 0, len(snapshot.Inputs))
	for key := range snapshot.Inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	evidence := make([]alarmmodel.EvidenceReference, 0)
	for _, key := range keys {
		evidence = append(evidence, snapshot.Inputs[key].Evidence...)
	}
	return evidence
}

func validAlarmSource(value alarmmodel.SourceType) bool {
	return value == alarmmodel.SourceDeviceRule || value == alarmmodel.SourceSiteRule || value == alarmmodel.SourceExternal
}

func validAlarmSeverity(value alarmmodel.Severity) bool {
	switch value {
	case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMinor, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
		return true
	default:
		return false
	}
}

func validEvaluationSubjectType(value string) bool {
	switch value {
	case "SITE", "DEVICE", "POINT", "METRIC", "EXTERNAL":
		return true
	default:
		return false
	}
}

func validRuleKind(value RuleKind) bool {
	switch value {
	case RuleKindThreshold, RuleKindSlope, RuleKindExpression:
		return true
	default:
		return false
	}
}

func validSlopeOperator(value CompareOperator) bool {
	switch value {
	case CompareGT, CompareGTE, CompareLT, CompareLTE:
		return true
	default:
		return false
	}
}

func validCompareOperator(value CompareOperator) bool {
	switch value {
	case CompareEQ, CompareNE, CompareGT, CompareGTE, CompareLT, CompareLTE:
		return true
	default:
		return false
	}
}

func validConditionReason(reason string) bool {
	switch reason {
	case "OUT_OF_RANGE", "SPIKE", "SENTINEL", "FLATLINE", "DERIVED_INPUT", "LOW_CONFIDENCE", "SOURCE_UNTRUSTED", "PHYSICS_WET_BULB_DRY", "PHYSICS_WET_BULB_CONDENSER", "PHYSICS_POWER", "PHYSICS_COP", "PHYSICS_CHW_DIRECTION", "PHYSICS_CW_DIRECTION", "PHYSICS_HEAT_BALANCE", "PROBE_OFFSET":
		return true
	default:
		return false
	}
}

func boundedText(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= maximum
}

func validRuleWorkOrderPriority(priority string) bool {
	switch priority {
	case "LOW", "MEDIUM", "HIGH", "URGENT":
		return true
	default:
		return false
	}
}

func validateDependencyGate(gate DependencyGateMode, deps []string) error {
	switch gate {
	case "", DependencyGateNone:
		return nil
	case DependencyGateAllActive, DependencyGateAnyActive:
		if len(deps) == 0 {
			return errors.New("alarm policy dependencyGate requires non-empty dependsOnRuleIds")
		}
		return nil
	default:
		return errors.New("alarm policy dependencyGate is invalid")
	}
}

func validateRuleActions(actions []RuleAction) error {
	if len(actions) > MaxActionsPerRule {
		return errors.New("alarm policy actions exceed maximum")
	}
	for index, action := range actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("alarm policy action %d: %w", index, err)
		}
	}
	return nil
}

func cloneRuleActions(actions []RuleAction) []RuleAction {
	if len(actions) == 0 {
		return nil
	}
	cloned := make([]RuleAction, len(actions))
	for index, action := range actions {
		cloned[index] = action.Clone()
	}
	return cloned
}

func dependencyGateSatisfied(policy AlarmPolicyRevision, snapshot EvaluationSnapshot) bool {
	switch policy.DependencyGate {
	case "", DependencyGateNone:
		return true
	case DependencyGateAllActive:
		if len(policy.DependsOnRuleIDs) == 0 {
			return false
		}
		for _, depID := range policy.DependsOnRuleIDs {
			if !snapshot.UpstreamRuleActive[depID] {
				return false
			}
		}
		return true
	case DependencyGateAnyActive:
		for _, depID := range policy.DependsOnRuleIDs {
			if snapshot.UpstreamRuleActive[depID] {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func evaluateRaiseCondition(policy AlarmPolicyRevision, snapshot EvaluationSnapshot, samples []TimedSample, keepHolding bool, now time.Time) predicateResult {
	if !dependencyGateSatisfied(policy, snapshot) {
		return predicateResult{status: EvaluationNotMatched}
	}
	return evaluateCondition(policy, policy.Raise, snapshot, samples, keepHolding, now)
}
