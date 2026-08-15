package alarmmodel

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

const maximumSuppressionDuration = 30 * 24 * time.Hour

var (
	uuidV7Pattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ErrVersionConflict   = errors.New("alarm version conflict")
	ErrInvalidOperation  = errors.New("alarm operation is invalid")
	ErrInvalidTransition = errors.New("alarm transition is invalid")
)

type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityMajor    Severity = "MAJOR"
	SeverityCritical Severity = "CRITICAL"
)

type Status string

const (
	StatusOpen         Status = "OPEN"
	StatusAcknowledged Status = "ACKNOWLEDGED"
	StatusSuppressed   Status = "SUPPRESSED"
	StatusClosed       Status = "CLOSED"
)

type Operation string

const (
	OperationPublish     Operation = "PUBLISH"
	OperationAcknowledge Operation = "ACKNOWLEDGE"
	OperationAssign      Operation = "ASSIGN"
	OperationUnassign    Operation = "UNASSIGN"
	OperationSuppress    Operation = "SUPPRESS"
	OperationUnsuppress  Operation = "UNSUPPRESS"
	OperationClose       Operation = "CLOSE"
	OperationReopen      Operation = "REOPEN"
)

type SourceType string

const (
	SourceDeviceRule SourceType = "DEVICE_RULE"
	SourceSiteRule   SourceType = "SITE_RULE"
	SourceExternal   SourceType = "EXTERNAL"
)

type EvidenceReference struct {
	Kind       string `json:"kind"`
	Reference  string `json:"reference"`
	CapturedAt string `json:"capturedAt"`
}

type Transition struct {
	FromStatus      *Status   `json:"fromStatus,omitempty"`
	ToStatus        Status    `json:"toStatus"`
	Operation       Operation `json:"operation,omitempty"`
	Reason          string    `json:"reason"`
	ActorType       string    `json:"actorType"`
	ActorID         *string   `json:"actorId,omitempty"`
	AssigneeID      *string   `json:"assigneeId,omitempty"`
	SuppressedUntil *string   `json:"suppressedUntil,omitempty"`
	PolicyRevision  *string   `json:"policyRevision,omitempty"`
	CorrelationID   *string   `json:"correlationId,omitempty"`
	OccurredAt      string    `json:"occurredAt"`
	Version         uint64    `json:"version"`
}

type Alarm struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	AlarmID         string              `json:"alarmId"`
	TenantID        string              `json:"tenantId"`
	SiteID          string              `json:"siteId"`
	DeviceID        *string             `json:"deviceId,omitempty"`
	SourceType      SourceType          `json:"sourceType"`
	SourceReference string              `json:"sourceReference"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	Severity        Severity            `json:"severity"`
	Status          Status              `json:"status"`
	AssigneeID      *string             `json:"assigneeId,omitempty"`
	SuppressedUntil *string             `json:"suppressedUntil,omitempty"`
	OccurrenceCount uint64              `json:"occurrenceCount"`
	FirstOccurredAt string              `json:"firstOccurredAt"`
	LastOccurredAt  string              `json:"lastOccurredAt"`
	Evidence        []EvidenceReference `json:"evidence"`
	Transitions     []Transition        `json:"transitions"`
	Version         uint64              `json:"version"`
	CreatedAt       string              `json:"createdAt"`
	UpdatedAt       string              `json:"updatedAt"`
}

type ListResponse struct {
	SchemaVersion int     `json:"schemaVersion"`
	Items         []Alarm `json:"items"`
	NextCursor    *string `json:"nextCursor"`
	HasMore       bool    `json:"hasMore"`
}

type OperationInput struct {
	Operation       Operation
	ExpectedVersion uint64
	Reason          string
	AssigneeID      *string
	SuppressedUntil *string
	ActorType       string
	ActorID         string
	PolicyRevision  string
	CorrelationID   string
	OccurredAt      string
}

func IsUUIDv7(value string) bool { return uuidV7Pattern.MatchString(value) }

func (alarm Alarm) Validate() error {
	if alarm.SchemaVersion != SchemaVersion || !IsUUIDv7(alarm.AlarmID) || !IsUUIDv7(alarm.TenantID) || !IsUUIDv7(alarm.SiteID) {
		return errors.New("alarm identity is invalid")
	}
	if alarm.DeviceID != nil && !IsUUIDv7(*alarm.DeviceID) {
		return errors.New("alarm device identity is invalid")
	}
	if !validSource(alarm.SourceType) || strings.TrimSpace(alarm.SourceReference) == "" || strings.TrimSpace(alarm.Title) == "" || strings.TrimSpace(alarm.Summary) == "" {
		return errors.New("alarm source or description is invalid")
	}
	if !validSeverity(alarm.Severity) || !validStatus(alarm.Status) || alarm.OccurrenceCount == 0 || alarm.Version == 0 {
		return errors.New("alarm lifecycle is invalid")
	}
	if alarm.AssigneeID != nil && !validBoundedText(*alarm.AssigneeID, 256) {
		return errors.New("alarm assignee is invalid")
	}
	first, err := time.Parse(time.RFC3339Nano, alarm.FirstOccurredAt)
	if err != nil {
		return errors.New("alarm first occurrence is invalid")
	}
	last, err := time.Parse(time.RFC3339Nano, alarm.LastOccurredAt)
	if err != nil || last.Before(first) {
		return errors.New("alarm last occurrence is invalid")
	}
	created, err := time.Parse(time.RFC3339Nano, alarm.CreatedAt)
	if err != nil {
		return errors.New("alarm creation instant is invalid")
	}
	updated, err := time.Parse(time.RFC3339Nano, alarm.UpdatedAt)
	if err != nil || updated.Before(created) || updated.Before(last) {
		return errors.New("alarm update instant is invalid")
	}
	for _, evidence := range alarm.Evidence {
		if strings.TrimSpace(evidence.Kind) == "" || strings.TrimSpace(evidence.Reference) == "" {
			return errors.New("alarm evidence is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, evidence.CapturedAt); err != nil {
			return errors.New("alarm evidence instant is invalid")
		}
	}
	if err := validateTransitions(alarm.Transitions, alarm.Status, alarm.Version, alarm.AssigneeID, alarm.SuppressedUntil); err != nil {
		return err
	}
	return nil
}

func (response ListResponse) Validate(tenantID, siteID string, limit int) error {
	if response.SchemaVersion != SchemaVersion || !IsUUIDv7(tenantID) || !IsUUIDv7(siteID) || limit < 1 || limit > 100 || len(response.Items) > limit {
		return errors.New("alarm list envelope is invalid")
	}
	seen := map[string]struct{}{}
	for _, alarm := range response.Items {
		if err := alarm.Validate(); err != nil || alarm.TenantID != tenantID || alarm.SiteID != siteID {
			return errors.New("alarm list contains an invalid or cross-scope item")
		}
		if _, exists := seen[alarm.AlarmID]; exists {
			return errors.New("alarm list contains duplicate identity")
		}
		seen[alarm.AlarmID] = struct{}{}
	}
	if response.HasMore != (response.NextCursor != nil) {
		return errors.New("alarm list cursor state is invalid")
	}
	return nil
}

func ApplyOperation(alarm Alarm, input OperationInput) (Alarm, error) {
	if err := alarm.Validate(); err != nil {
		return Alarm{}, err
	}
	if input.ExpectedVersion != alarm.Version {
		return Alarm{}, ErrVersionConflict
	}
	if !validMutationOperation(input.Operation) || !validBoundedText(input.Reason, 256) || !validBoundedText(input.ActorType, 64) ||
		!validBoundedText(input.ActorID, 256) || !validBoundedText(input.PolicyRevision, 128) || !validBoundedText(input.CorrelationID, 256) {
		return Alarm{}, ErrInvalidOperation
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, input.OccurredAt)
	if err != nil {
		return Alarm{}, ErrInvalidOperation
	}
	updatedAt, _ := time.Parse(time.RFC3339Nano, alarm.UpdatedAt)
	if occurredAt.Before(updatedAt) {
		return Alarm{}, ErrInvalidOperation
	}

	result := cloneAlarm(alarm)
	fromStatus := result.Status
	toStatus := result.Status
	var transitionAssignee *string
	var transitionSuppressedUntil *string

	switch input.Operation {
	case OperationAcknowledge:
		if result.Status != StatusOpen || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		toStatus = StatusAcknowledged
	case OperationAssign:
		if result.Status == StatusClosed || input.AssigneeID == nil || !validBoundedText(*input.AssigneeID, 256) || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		value := strings.TrimSpace(*input.AssigneeID)
		result.AssigneeID = &value
		transitionAssignee = &value
	case OperationUnassign:
		if result.Status == StatusClosed || result.AssigneeID == nil || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		result.AssigneeID = nil
	case OperationSuppress:
		if (result.Status != StatusOpen && result.Status != StatusAcknowledged) || input.AssigneeID != nil || input.SuppressedUntil == nil {
			return Alarm{}, ErrInvalidTransition
		}
		suppressedUntil, parseErr := time.Parse(time.RFC3339Nano, *input.SuppressedUntil)
		if parseErr != nil || !suppressedUntil.After(occurredAt) || suppressedUntil.Sub(occurredAt) > maximumSuppressionDuration {
			return Alarm{}, ErrInvalidTransition
		}
		value := suppressedUntil.UTC().Format(time.RFC3339Nano)
		result.SuppressedUntil = &value
		transitionSuppressedUntil = &value
		toStatus = StatusSuppressed
	case OperationUnsuppress:
		if result.Status != StatusSuppressed || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		previous, ok := statusBeforeSuppression(result.Transitions)
		if !ok {
			return Alarm{}, ErrInvalidTransition
		}
		toStatus = previous
		result.SuppressedUntil = nil
	case OperationClose:
		if result.Status == StatusClosed || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		toStatus = StatusClosed
		result.SuppressedUntil = nil
	case OperationReopen:
		if result.Status != StatusClosed || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		toStatus = StatusOpen
		result.SuppressedUntil = nil
	default:
		return Alarm{}, ErrInvalidOperation
	}

	actorID := strings.TrimSpace(input.ActorID)
	policyRevision := strings.TrimSpace(input.PolicyRevision)
	correlationID := strings.TrimSpace(input.CorrelationID)
	result.Status = toStatus
	result.Version++
	result.UpdatedAt = occurredAt.UTC().Format(time.RFC3339Nano)
	result.Transitions = append(result.Transitions, Transition{
		FromStatus:      &fromStatus,
		ToStatus:        toStatus,
		Operation:       input.Operation,
		Reason:          strings.TrimSpace(input.Reason),
		ActorType:       strings.TrimSpace(input.ActorType),
		ActorID:         &actorID,
		AssigneeID:      transitionAssignee,
		SuppressedUntil: transitionSuppressedUntil,
		PolicyRevision:  &policyRevision,
		CorrelationID:   &correlationID,
		OccurredAt:      result.UpdatedAt,
		Version:         result.Version,
	})
	if err := result.Validate(); err != nil {
		return Alarm{}, err
	}
	return result, nil
}

func SortNewestFirst(items []Alarm) {
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].LastOccurredAt == items[right].LastOccurredAt {
			return items[left].AlarmID < items[right].AlarmID
		}
		return items[left].LastOccurredAt > items[right].LastOccurredAt
	})
}

func validSeverity(value Severity) bool {
	switch value {
	case SeverityInfo, SeverityWarning, SeverityMajor, SeverityCritical:
		return true
	default:
		return false
	}
}

func validStatus(value Status) bool {
	switch value {
	case StatusOpen, StatusAcknowledged, StatusSuppressed, StatusClosed:
		return true
	default:
		return false
	}
}

func validOperation(value Operation) bool {
	switch value {
	case OperationPublish, OperationAcknowledge, OperationAssign, OperationUnassign, OperationSuppress, OperationUnsuppress, OperationClose, OperationReopen:
		return true
	default:
		return false
	}
}

func validMutationOperation(value Operation) bool {
	return validOperation(value) && value != OperationPublish
}

func validSource(value SourceType) bool {
	switch value {
	case SourceDeviceRule, SourceSiteRule, SourceExternal:
		return true
	default:
		return false
	}
}

func validBoundedText(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= maximum
}

func validateTransitions(transitions []Transition, status Status, version uint64, assigneeID, suppressedUntil *string) error {
	if len(transitions) == 0 || transitions[len(transitions)-1].Version != version || transitions[len(transitions)-1].ToStatus != status {
		return errors.New("alarm transition timeline is incomplete")
	}
	var previousVersion uint64
	var previousStatus *Status
	var projectedAssignee *string
	var projectedSuppressedUntil *string
	var projectedSuppressionReturnStatus *Status
	for index, transition := range transitions {
		if !validStatus(transition.ToStatus) || strings.TrimSpace(transition.Reason) == "" || strings.TrimSpace(transition.ActorType) == "" || transition.Version == 0 || transition.Version <= previousVersion {
			return errors.New("alarm transition is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, transition.OccurredAt); err != nil {
			return errors.New("alarm transition instant is invalid")
		}
		if index == 0 {
			if transition.FromStatus != nil || (transition.Operation != "" && transition.Operation != OperationPublish) {
				return errors.New("alarm initial transition is invalid")
			}
		} else {
			if transition.FromStatus == nil || previousStatus == nil || *transition.FromStatus != *previousStatus || !validMutationOperation(transition.Operation) {
				return errors.New("alarm transition chain is invalid")
			}
			if transition.ActorID == nil || transition.PolicyRevision == nil || transition.CorrelationID == nil ||
				!validBoundedText(*transition.ActorID, 256) || !validBoundedText(*transition.PolicyRevision, 128) || !validBoundedText(*transition.CorrelationID, 256) {
				return errors.New("alarm transition audit evidence is invalid")
			}
			if !validOperationShape(transition) {
				return errors.New("alarm transition operation is invalid")
			}
		}
		switch transition.Operation {
		case OperationAssign:
			value := strings.TrimSpace(*transition.AssigneeID)
			projectedAssignee = &value
		case OperationUnassign:
			projectedAssignee = nil
		case OperationSuppress:
			value := *transition.SuppressedUntil
			projectedSuppressedUntil = &value
			returnStatus := *transition.FromStatus
			projectedSuppressionReturnStatus = &returnStatus
		case OperationUnsuppress:
			if projectedSuppressionReturnStatus == nil || transition.ToStatus != *projectedSuppressionReturnStatus {
				return errors.New("alarm unsuppression does not restore the suppressed lifecycle state")
			}
			projectedSuppressedUntil = nil
			projectedSuppressionReturnStatus = nil
		case OperationClose, OperationReopen:
			projectedSuppressedUntil = nil
			projectedSuppressionReturnStatus = nil
		}
		current := transition.ToStatus
		previousStatus = &current
		previousVersion = transition.Version
	}
	if !sameOptionalString(projectedAssignee, assigneeID) || !sameOptionalString(projectedSuppressedUntil, suppressedUntil) {
		return errors.New("alarm transition facts do not converge")
	}
	if status == StatusSuppressed {
		if suppressedUntil == nil || projectedSuppressionReturnStatus == nil {
			return errors.New("suppressed alarm has no suppression interval")
		}
		if _, err := time.Parse(time.RFC3339Nano, *suppressedUntil); err != nil {
			return errors.New("alarm suppression instant is invalid")
		}
	} else if suppressedUntil != nil {
		return errors.New("non-suppressed alarm retains suppression state")
	}
	return nil
}

func validOperationShape(transition Transition) bool {
	from := *transition.FromStatus
	switch transition.Operation {
	case OperationAcknowledge:
		return from == StatusOpen && transition.ToStatus == StatusAcknowledged && transition.AssigneeID == nil && transition.SuppressedUntil == nil
	case OperationAssign:
		return from != StatusClosed && transition.ToStatus == from && transition.AssigneeID != nil && validBoundedText(*transition.AssigneeID, 256) && transition.SuppressedUntil == nil
	case OperationUnassign:
		return from != StatusClosed && transition.ToStatus == from && transition.AssigneeID == nil && transition.SuppressedUntil == nil
	case OperationSuppress:
		if (from != StatusOpen && from != StatusAcknowledged) || transition.ToStatus != StatusSuppressed || transition.AssigneeID != nil || transition.SuppressedUntil == nil {
			return false
		}
		occurredAt, occurredErr := time.Parse(time.RFC3339Nano, transition.OccurredAt)
		suppressedUntil, suppressedErr := time.Parse(time.RFC3339Nano, *transition.SuppressedUntil)
		return occurredErr == nil && suppressedErr == nil && suppressedUntil.After(occurredAt) && suppressedUntil.Sub(occurredAt) <= maximumSuppressionDuration
	case OperationUnsuppress:
		return from == StatusSuppressed && (transition.ToStatus == StatusOpen || transition.ToStatus == StatusAcknowledged) && transition.AssigneeID == nil && transition.SuppressedUntil == nil
	case OperationClose:
		return from != StatusClosed && transition.ToStatus == StatusClosed && transition.AssigneeID == nil && transition.SuppressedUntil == nil
	case OperationReopen:
		return from == StatusClosed && transition.ToStatus == StatusOpen && transition.AssigneeID == nil && transition.SuppressedUntil == nil
	default:
		return false
	}
}

func statusBeforeSuppression(transitions []Transition) (Status, bool) {
	for index := len(transitions) - 1; index >= 0; index-- {
		transition := transitions[index]
		switch transition.Operation {
		case OperationSuppress:
			if transition.ToStatus != StatusSuppressed || transition.FromStatus == nil {
				return "", false
			}
			if *transition.FromStatus != StatusOpen && *transition.FromStatus != StatusAcknowledged {
				return "", false
			}
			return *transition.FromStatus, true
		case OperationUnsuppress, OperationClose, OperationReopen:
			return "", false
		}
	}
	return "", false
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneAlarm(alarm Alarm) Alarm {
	result := alarm
	result.Evidence = append([]EvidenceReference(nil), alarm.Evidence...)
	result.Transitions = append([]Transition(nil), alarm.Transitions...)
	if alarm.AssigneeID != nil {
		value := *alarm.AssigneeID
		result.AssigneeID = &value
	}
	if alarm.SuppressedUntil != nil {
		value := *alarm.SuppressedUntil
		result.SuppressedUntil = &value
	}
	return result
}
