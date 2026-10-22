package alarmservice

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
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
	RuleID          string              `json:"ruleId"`
	Title           string              `json:"title"`
	Severity        alarmmodel.Severity `json:"severity"`
	TriggerMode     TriggerMode         `json:"triggerMode"`
	DurationSeconds int64               `json:"durationSeconds,omitempty"`
	RepeatCount     uint64              `json:"repeatCount,omitempty"`
	Raise           RuleCondition       `json:"raise"`
	Clear           RuleCondition       `json:"clear"`
}

// RuleCondition is a Condition with its reading split into device and point and its value a plain
// JSON number, string or boolean.
type RuleCondition struct {
	Kind       ConditionKind       `json:"kind"`
	Reading    *RuleReading        `json:"reading,omitempty"`
	Operator   CompareOperator     `json:"operator,omitempty"`
	Value      any                 `json:"value,omitempty"`
	Minimum    *float64            `json:"minimum,omitempty"`
	Maximum    *float64            `json:"maximum,omitempty"`
	Direction  HysteresisDirection `json:"direction,omitempty"`
	Trigger    *float64            `json:"trigger,omitempty"`
	Reset      *float64            `json:"reset,omitempty"`
	AgeSeconds int64               `json:"ageSeconds,omitempty"`
	Children   []RuleCondition     `json:"children,omitempty"`
}

type RuleReading struct {
	DeviceID  string `json:"deviceId"`
	PointCode string `json:"pointCode"`
}

func siteRuleView(rule SiteRule) (SiteRuleView, error) {
	raise, err := ruleCondition(rule.Policy.Raise)
	if err != nil {
		return SiteRuleView{}, err
	}
	clear, err := ruleCondition(rule.Policy.Clear)
	if err != nil {
		return SiteRuleView{}, err
	}
	return SiteRuleView{
		RuleID: rule.AssignmentID, Title: rule.Policy.Title, Severity: rule.Policy.Severity,
		TriggerMode: rule.Policy.TriggerMode, DurationSeconds: rule.Policy.DurationSeconds, RepeatCount: rule.Policy.RepeatCount,
		Raise: raise, Clear: clear,
	}, nil
}

func ruleCondition(condition Condition) (RuleCondition, error) {
	view := RuleCondition{Kind: condition.Kind, Minimum: condition.Minimum, Maximum: condition.Maximum, AgeSeconds: condition.AgeSeconds}
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
	}
	// A Site rule reads "<device id>/<point code>" (siteInputKey).
	deviceID, pointCode, ok := strings.Cut(condition.Input, "/")
	if !ok || !alarmmodel.IsUUIDv7(deviceID) || pointCode == "" {
		return RuleCondition{}, errors.New("Site rule reads an input that is not a device reading")
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
	list := SiteRuleList{Items: make([]SiteRuleView, 0, len(rules))}
	for _, rule := range rules {
		view, err := siteRuleView(rule)
		if err != nil {
			handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "A Site rule cannot be listed as stored.", false)
			return
		}
		list.Items = append(list.Items, view)
	}
	if list.Validate() != nil {
		handler.writeProblem(writer, http.StatusBadGateway, "ALARM_RESPONSE_INVALID", "Alarm response invalid", "A Site rule cannot be listed as stored.", false)
		return
	}
	writeJSON(writer, http.StatusOK, list)
}

// matchSiteRulesPath matches /internal/v1/sites/{siteId}/alarm-rules.
func matchSiteRulesPath(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 5 || segments[0] != "internal" || segments[1] != "v1" || segments[2] != "sites" || segments[4] != "alarm-rules" {
		return "", false
	}
	return segments[3], true
}

// Validate is what the Gateway checks before it passes the list on: every rule names itself, its
// severity and a well-formed raise and clear condition over device readings.
func (list SiteRuleList) Validate() error {
	if list.Items == nil {
		return errors.New("Site rule list has no items")
	}
	for _, rule := range list.Items {
		if !alarmmodel.IsUUIDv7(rule.RuleID) || !boundedText(rule.Title, 256) || !validAlarmSeverity(rule.Severity) {
			return errors.New("Site rule identity is invalid")
		}
		switch {
		case rule.TriggerMode == TriggerSimple && rule.DurationSeconds == 0 && rule.RepeatCount == 0:
		case rule.TriggerMode == TriggerDuration && rule.DurationSeconds > 0 && rule.RepeatCount == 0:
		case rule.TriggerMode == TriggerRepeating && rule.RepeatCount > 0 && rule.DurationSeconds == 0:
		default:
			return errors.New("Site rule trigger is invalid")
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

func (condition RuleCondition) validate() error {
	if condition.Kind == ConditionAnd || condition.Kind == ConditionOr {
		if condition.Reading != nil || len(condition.Children) < 2 || len(condition.Children) > 32 {
			return errors.New("Site rule compound condition is invalid")
		}
		for _, child := range condition.Children {
			if err := child.validate(); err != nil {
				return err
			}
		}
		return nil
	}
	if condition.Reading == nil || !alarmmodel.IsUUIDv7(condition.Reading.DeviceID) || !boundedText(condition.Reading.PointCode, 128) || len(condition.Children) != 0 {
		return errors.New("Site rule condition reading is invalid")
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
	}
	if !valid {
		return errors.New("Site rule condition is invalid")
	}
	return nil
}
