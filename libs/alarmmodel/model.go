package alarmmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 2

const maximumSuppressionDuration = 30 * 24 * time.Hour

var (
	uuidV7Pattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	fingerprintPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	ErrVersionConflict   = errors.New("alarm version conflict")
	ErrInvalidOperation  = errors.New("alarm operation is invalid")
	ErrInvalidTransition = errors.New("alarm transition is invalid")
)

type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityMinor    Severity = "MINOR"
	SeverityMajor    Severity = "MAJOR"
	SeverityCritical Severity = "CRITICAL"
)

type Condition string

const (
	ConditionActive  Condition = "ACTIVE"
	ConditionCleared Condition = "CLEARED"
)

type Operation string

const (
	OperationPublish     Operation = "PUBLISH"
	OperationAcknowledge Operation = "ACKNOWLEDGE"
	OperationAssign      Operation = "ASSIGN"
	OperationUnassign    Operation = "UNASSIGN"
	OperationSuppress    Operation = "SUPPRESS"
	OperationUnsuppress  Operation = "UNSUPPRESS"
	OperationClear       Operation = "CLEAR"
)

type SourceType string

const (
	SourceDeviceRule SourceType = "DEVICE_RULE"
	SourceSiteRule   SourceType = "SITE_RULE"
	SourceExternal   SourceType = "EXTERNAL"
)

type LinkKind string

const (
	LinkDevice    LinkKind = "DEVICE"
	LinkEvent     LinkKind = "EVENT"
	LinkPoint     LinkKind = "POINT"
	LinkWorkOrder LinkKind = "WORK_ORDER"
)

type EvidenceReference struct {
	Kind       string `json:"kind"`
	Reference  string `json:"reference"`
	CapturedAt string `json:"capturedAt"`
}

type Link struct {
	Kind     LinkKind `json:"kind"`
	TargetID string   `json:"targetId"`
}

type Acknowledgement struct {
	AcknowledgedAt string `json:"acknowledgedAt"`
	AcknowledgedBy string `json:"acknowledgedBy"`
	Comment        string `json:"comment,omitempty"`
}

type Suppression struct {
	StartsAt       string `json:"startsAt"`
	ExpiresAt      string `json:"expiresAt"`
	Reason         string `json:"reason"`
	ActorID        string `json:"actorId"`
	PolicyRevision string `json:"policyRevision"`
}

type TimelineEntry struct {
	Operation       Operation    `json:"operation"`
	Condition       Condition    `json:"condition"`
	Reason          string       `json:"reason"`
	ActorType       string       `json:"actorType"`
	ActorID         *string      `json:"actorId,omitempty"`
	AssigneeID      *string      `json:"assigneeId,omitempty"`
	Suppression     *Suppression `json:"suppression,omitempty"`
	CurrentSeverity Severity     `json:"currentSeverity"`
	PolicyRevision  *string      `json:"policyRevision,omitempty"`
	CorrelationID   string       `json:"correlationId"`
	OccurredAt      string       `json:"occurredAt"`
	Version         uint64       `json:"version"`
}

type Alarm struct {
	SchemaVersion         int                 `json:"schemaVersion"`
	AlarmID               string              `json:"alarmId"`
	TenantID              string              `json:"tenantId"`
	SiteID                string              `json:"siteId"`
	DeviceID              *string             `json:"deviceId,omitempty"`
	EventID               *string             `json:"eventId,omitempty"`
	PointID               *string             `json:"pointId,omitempty"`
	AlarmType             string              `json:"alarmType"`
	Fingerprint           string              `json:"fingerprint"`
	IncidentCorrelationID string              `json:"incidentCorrelationId"`
	SourceType            SourceType          `json:"sourceType"`
	SourceReference       string              `json:"sourceReference"`
	RuleRevision          string              `json:"ruleRevision"`
	Title                 string              `json:"title"`
	Summary               string              `json:"summary"`
	Condition             Condition           `json:"condition"`
	CurrentSeverity       Severity            `json:"currentSeverity"`
	PeakSeverity          Severity            `json:"peakSeverity"`
	Acknowledgement       *Acknowledgement    `json:"acknowledgement,omitempty"`
	AssigneeID            *string             `json:"assigneeId,omitempty"`
	Suppression           *Suppression        `json:"suppression,omitempty"`
	OccurrenceCount       uint64              `json:"occurrenceCount"`
	FirstOccurredAt       string              `json:"firstOccurredAt"`
	LastOccurredAt        string              `json:"lastOccurredAt"`
	ClearedAt             *string             `json:"clearedAt,omitempty"`
	Evidence              []EvidenceReference `json:"evidence"`
	Links                 []Link              `json:"links"`
	Timeline              []TimelineEntry     `json:"timeline"`
	Version               uint64              `json:"version"`
	CreatedAt             string              `json:"createdAt"`
	UpdatedAt             string              `json:"updatedAt"`
}

type ListResponse struct {
	SchemaVersion int     `json:"schemaVersion"`
	Items         []Alarm `json:"items"`
	NextCursor    *string `json:"nextCursor"`
	HasMore       bool    `json:"hasMore"`
}

type IncidentInput struct {
	AlarmID               string
	TenantID              string
	SiteID                string
	DeviceID              *string
	EventID               *string
	PointID               *string
	AlarmType             string
	IncidentCorrelationID string
	SourceType            SourceType
	SourceReference       string
	RuleRevision          string
	Title                 string
	Summary               string
	Severity              Severity
	OccurredAt            string
	Evidence              []EvidenceReference
	ActorType             string
	ActorID               string
}

type OccurrenceInput struct {
	Severity      Severity
	OccurredAt    string
	Evidence      []EvidenceReference
	RuleRevision  string
	ActorType     string
	ActorID       string
	CorrelationID string
}

type ClearInput struct {
	OccurredAt    string
	Reason        string
	Evidence      []EvidenceReference
	RuleRevision  string
	ActorType     string
	ActorID       string
	CorrelationID string
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

func Fingerprint(tenantID, siteID string, sourceType SourceType, sourceReference, alarmType string, deviceID, pointID *string) (string, error) {
	if !IsUUIDv7(tenantID) || !IsUUIDv7(siteID) || !validSource(sourceType) || !validBoundedText(sourceReference, 512) || !validBoundedText(alarmType, 128) {
		return "", errors.New("alarm fingerprint input is invalid")
	}
	device := ""
	if deviceID != nil {
		if !IsUUIDv7(*deviceID) {
			return "", errors.New("alarm fingerprint device is invalid")
		}
		device = *deviceID
	}
	point := ""
	if pointID != nil {
		if !IsUUIDv7(*pointID) {
			return "", errors.New("alarm fingerprint point is invalid")
		}
		point = *pointID
	}
	value := strings.Join([]string{tenantID, siteID, string(sourceType), strings.TrimSpace(sourceReference), strings.TrimSpace(alarmType), device, point}, "\x1f")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:]), nil
}

func NewIncident(input IncidentInput) (Alarm, error) {
	fingerprint, err := Fingerprint(input.TenantID, input.SiteID, input.SourceType, input.SourceReference, input.AlarmType, input.DeviceID, input.PointID)
	if err != nil {
		return Alarm{}, err
	}
	if !IsUUIDv7(input.AlarmID) || !IsUUIDv7(input.IncidentCorrelationID) || !validSeverity(input.Severity) || !validBoundedText(input.RuleRevision, 128) || !validBoundedText(input.Title, 256) || !validBoundedText(input.Summary, 2048) || !validBoundedText(input.ActorType, 64) || !validBoundedText(input.ActorID, 256) {
		return Alarm{}, errors.New("alarm incident input is invalid")
	}
	occurredAt, err := canonicalInstant(input.OccurredAt)
	if err != nil {
		return Alarm{}, errors.New("alarm incident occurrence is invalid")
	}
	if input.EventID != nil && !IsUUIDv7(*input.EventID) {
		return Alarm{}, errors.New("alarm event identity is invalid")
	}
	links := sourceLinks(input.DeviceID, input.EventID, input.PointID)
	actorID := strings.TrimSpace(input.ActorID)
	policyRevision := strings.TrimSpace(input.RuleRevision)
	alarm := Alarm{
		SchemaVersion: SchemaVersion, AlarmID: input.AlarmID, TenantID: input.TenantID, SiteID: input.SiteID,
		DeviceID: cloneString(input.DeviceID), EventID: cloneString(input.EventID), PointID: cloneString(input.PointID),
		AlarmType: strings.TrimSpace(input.AlarmType), Fingerprint: fingerprint, IncidentCorrelationID: input.IncidentCorrelationID,
		SourceType: input.SourceType, SourceReference: strings.TrimSpace(input.SourceReference), RuleRevision: policyRevision,
		Title: strings.TrimSpace(input.Title), Summary: strings.TrimSpace(input.Summary), Condition: ConditionActive,
		CurrentSeverity: input.Severity, PeakSeverity: input.Severity, OccurrenceCount: 1,
		FirstOccurredAt: occurredAt, LastOccurredAt: occurredAt, Evidence: cloneEvidence(input.Evidence), Links: links,
		Version: 1, CreatedAt: occurredAt, UpdatedAt: occurredAt,
	}
	alarm.Timeline = []TimelineEntry{{Operation: OperationPublish, Condition: ConditionActive, Reason: "ALARM_PUBLISHED", ActorType: strings.TrimSpace(input.ActorType), ActorID: &actorID, CurrentSeverity: input.Severity, PolicyRevision: &policyRevision, CorrelationID: input.IncidentCorrelationID, OccurredAt: occurredAt, Version: 1}}
	if err := alarm.Validate(); err != nil {
		return Alarm{}, err
	}
	return alarm, nil
}

func RecordOccurrence(alarm Alarm, input OccurrenceInput) (Alarm, error) {
	if err := alarm.Validate(); err != nil {
		return Alarm{}, err
	}
	if alarm.Condition != ConditionActive || !validSeverity(input.Severity) || !validAuditInput(input.ActorType, input.ActorID, input.RuleRevision, input.CorrelationID) {
		return Alarm{}, ErrInvalidTransition
	}
	occurredAt, err := canonicalInstant(input.OccurredAt)
	if err != nil {
		return Alarm{}, ErrInvalidOperation
	}
	last, _ := time.Parse(time.RFC3339Nano, alarm.LastOccurredAt)
	if parsed, _ := time.Parse(time.RFC3339Nano, occurredAt); parsed.Before(last) {
		return Alarm{}, ErrInvalidOperation
	}
	result := cloneAlarm(alarm)
	result.CurrentSeverity = input.Severity
	if severityRank(input.Severity) > severityRank(result.PeakSeverity) {
		result.PeakSeverity = input.Severity
	}
	result.RuleRevision = strings.TrimSpace(input.RuleRevision)
	result.OccurrenceCount++
	result.LastOccurredAt = occurredAt
	result.UpdatedAt = occurredAt
	result.Evidence = append(result.Evidence, cloneEvidence(input.Evidence)...)
	result.Version++
	appendTimeline(&result, OperationPublish, "ALARM_OCCURRENCE", input.ActorType, input.ActorID, input.RuleRevision, input.CorrelationID, occurredAt, nil, nil)
	return result, result.Validate()
}

func ClearIncident(alarm Alarm, input ClearInput) (Alarm, error) {
	if err := alarm.Validate(); err != nil {
		return Alarm{}, err
	}
	if alarm.Condition == ConditionCleared {
		return alarm, nil
	}
	if !validBoundedText(input.Reason, 256) || !validAuditInput(input.ActorType, input.ActorID, input.RuleRevision, input.CorrelationID) {
		return Alarm{}, ErrInvalidOperation
	}
	occurredAt, err := canonicalInstant(input.OccurredAt)
	if err != nil {
		return Alarm{}, ErrInvalidOperation
	}
	updated, _ := time.Parse(time.RFC3339Nano, alarm.UpdatedAt)
	parsed, _ := time.Parse(time.RFC3339Nano, occurredAt)
	if parsed.Before(updated) {
		return Alarm{}, ErrInvalidOperation
	}
	result := cloneAlarm(alarm)
	result.Condition = ConditionCleared
	result.ClearedAt = &occurredAt
	result.Suppression = nil
	result.RuleRevision = strings.TrimSpace(input.RuleRevision)
	result.UpdatedAt = occurredAt
	result.Evidence = append(result.Evidence, cloneEvidence(input.Evidence)...)
	result.Version++
	appendTimeline(&result, OperationClear, strings.TrimSpace(input.Reason), input.ActorType, input.ActorID, input.RuleRevision, input.CorrelationID, occurredAt, nil, nil)
	return result, result.Validate()
}

func (alarm Alarm) Validate() error {
	if alarm.SchemaVersion != SchemaVersion || !IsUUIDv7(alarm.AlarmID) || !IsUUIDv7(alarm.TenantID) || !IsUUIDv7(alarm.SiteID) || !IsUUIDv7(alarm.IncidentCorrelationID) || !fingerprintPattern.MatchString(alarm.Fingerprint) {
		return errors.New("alarm identity is invalid")
	}
	if alarm.DeviceID != nil && !IsUUIDv7(*alarm.DeviceID) || alarm.EventID != nil && !IsUUIDv7(*alarm.EventID) || alarm.PointID != nil && !IsUUIDv7(*alarm.PointID) {
		return errors.New("alarm linked identity is invalid")
	}
	if !validSource(alarm.SourceType) || !validBoundedText(alarm.SourceReference, 512) || !validBoundedText(alarm.AlarmType, 128) || !validBoundedText(alarm.RuleRevision, 128) || !validBoundedText(alarm.Title, 256) || !validBoundedText(alarm.Summary, 2048) {
		return errors.New("alarm source or description is invalid")
	}
	expectedFingerprint, err := Fingerprint(alarm.TenantID, alarm.SiteID, alarm.SourceType, alarm.SourceReference, alarm.AlarmType, alarm.DeviceID, alarm.PointID)
	if err != nil || expectedFingerprint != alarm.Fingerprint {
		return errors.New("alarm fingerprint is invalid")
	}
	if !validCondition(alarm.Condition) || !validSeverity(alarm.CurrentSeverity) || !validSeverity(alarm.PeakSeverity) || severityRank(alarm.PeakSeverity) < severityRank(alarm.CurrentSeverity) || alarm.OccurrenceCount == 0 || alarm.Version == 0 {
		return errors.New("alarm lifecycle is invalid")
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
	if alarm.Condition == ConditionActive && alarm.ClearedAt != nil || alarm.Condition == ConditionCleared && alarm.ClearedAt == nil {
		return errors.New("alarm clear fact is inconsistent")
	}
	if alarm.ClearedAt != nil {
		cleared, err := time.Parse(time.RFC3339Nano, *alarm.ClearedAt)
		if err != nil || cleared.Before(last) || cleared.After(updated) {
			return errors.New("alarm clear instant is invalid")
		}
	}
	if alarm.Acknowledgement != nil {
		if !validBoundedText(alarm.Acknowledgement.AcknowledgedBy, 256) || len(strings.TrimSpace(alarm.Acknowledgement.Comment)) > 1000 {
			return errors.New("alarm acknowledgement is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, alarm.Acknowledgement.AcknowledgedAt); err != nil {
			return errors.New("alarm acknowledgement instant is invalid")
		}
	}
	if alarm.AssigneeID != nil && !validBoundedText(*alarm.AssigneeID, 256) {
		return errors.New("alarm assignee is invalid")
	}
	if alarm.Suppression != nil {
		if alarm.Condition != ConditionActive || validateSuppression(*alarm.Suppression) != nil {
			return errors.New("alarm suppression is invalid")
		}
	}
	for _, evidence := range alarm.Evidence {
		if strings.TrimSpace(evidence.Kind) == "" || strings.TrimSpace(evidence.Reference) == "" {
			return errors.New("alarm evidence is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, evidence.CapturedAt); err != nil {
			return errors.New("alarm evidence instant is invalid")
		}
	}
	if err := validateLinks(alarm.Links, alarm.DeviceID, alarm.EventID, alarm.PointID); err != nil {
		return err
	}
	if err := validateTimeline(alarm); err != nil {
		return err
	}
	return nil
}

func (response ListResponse) Validate(tenantID, siteID string, limit int) error {
	if response.SchemaVersion != SchemaVersion || !IsUUIDv7(tenantID) || !IsUUIDv7(siteID) || limit < 1 || limit > 200 || len(response.Items) > limit {
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
	if input.Operation != OperationAcknowledge && input.ExpectedVersion != alarm.Version {
		return Alarm{}, ErrVersionConflict
	}
	if !validMutationOperation(input.Operation) || !validAuditInput(input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID) {
		return Alarm{}, ErrInvalidOperation
	}
	if input.Operation == OperationAcknowledge {
		if len(strings.TrimSpace(input.Reason)) > 1000 {
			return Alarm{}, ErrInvalidOperation
		}
	} else if !validBoundedText(input.Reason, 256) {
		return Alarm{}, ErrInvalidOperation
	}
	occurredAt, err := canonicalInstant(input.OccurredAt)
	if err != nil {
		return Alarm{}, ErrInvalidOperation
	}
	updatedAt, _ := time.Parse(time.RFC3339Nano, alarm.UpdatedAt)
	parsed, _ := time.Parse(time.RFC3339Nano, occurredAt)
	if parsed.Before(updatedAt) {
		return Alarm{}, ErrInvalidOperation
	}

	result := cloneAlarm(alarm)
	var timelineAssignee *string
	var timelineSuppression *Suppression
	switch input.Operation {
	case OperationAcknowledge:
		if input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		if result.Acknowledgement != nil {
			return result, nil
		}
		result.Acknowledgement = &Acknowledgement{AcknowledgedAt: occurredAt, AcknowledgedBy: strings.TrimSpace(input.ActorID), Comment: strings.TrimSpace(input.Reason)}
	case OperationAssign:
		if result.Condition != ConditionActive || input.AssigneeID == nil || !validBoundedText(*input.AssigneeID, 256) || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		value := strings.TrimSpace(*input.AssigneeID)
		result.AssigneeID = &value
		timelineAssignee = &value
	case OperationUnassign:
		if result.Condition != ConditionActive || result.AssigneeID == nil || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		result.AssigneeID = nil
	case OperationSuppress:
		if result.Condition != ConditionActive || input.AssigneeID != nil || input.SuppressedUntil == nil || result.Suppression != nil {
			return Alarm{}, ErrInvalidTransition
		}
		expiresAt, err := canonicalInstant(*input.SuppressedUntil)
		if err != nil {
			return Alarm{}, ErrInvalidTransition
		}
		expires, _ := time.Parse(time.RFC3339Nano, expiresAt)
		if !expires.After(parsed) || expires.Sub(parsed) > maximumSuppressionDuration {
			return Alarm{}, ErrInvalidTransition
		}
		suppression := Suppression{StartsAt: occurredAt, ExpiresAt: expiresAt, Reason: strings.TrimSpace(input.Reason), ActorID: strings.TrimSpace(input.ActorID), PolicyRevision: strings.TrimSpace(input.PolicyRevision)}
		result.Suppression = &suppression
		timelineSuppression = &suppression
	case OperationUnsuppress:
		if result.Condition != ConditionActive || result.Suppression == nil || input.AssigneeID != nil || input.SuppressedUntil != nil {
			return Alarm{}, ErrInvalidTransition
		}
		result.Suppression = nil
	default:
		return Alarm{}, ErrInvalidOperation
	}
	result.Version++
	result.UpdatedAt = occurredAt
	appendTimeline(&result, input.Operation, strings.TrimSpace(input.Reason), input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, occurredAt, timelineAssignee, timelineSuppression)
	if err := result.Validate(); err != nil {
		return Alarm{}, err
	}
	return result, nil
}

func SortNewestFirst(items []Alarm) {
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].FirstOccurredAt == items[right].FirstOccurredAt {
			return items[left].AlarmID > items[right].AlarmID
		}
		return items[left].FirstOccurredAt > items[right].FirstOccurredAt
	})
}

func validSeverity(value Severity) bool {
	switch value {
	case SeverityInfo, SeverityWarning, SeverityMinor, SeverityMajor, SeverityCritical:
		return true
	default:
		return false
	}
}

func severityRank(value Severity) int {
	switch value {
	case SeverityInfo:
		return 1
	case SeverityWarning:
		return 2
	case SeverityMinor:
		return 3
	case SeverityMajor:
		return 4
	case SeverityCritical:
		return 5
	default:
		return 0
	}
}

func validCondition(value Condition) bool {
	return value == ConditionActive || value == ConditionCleared
}

func validMutationOperation(value Operation) bool {
	switch value {
	case OperationAcknowledge, OperationAssign, OperationUnassign, OperationSuppress, OperationUnsuppress:
		return true
	default:
		return false
	}
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

func validAuditInput(actorType, actorID, policyRevision, correlationID string) bool {
	return validBoundedText(actorType, 64) && validBoundedText(actorID, 256) && validBoundedText(policyRevision, 128) && validBoundedText(correlationID, 256)
}

func validateSuppression(value Suppression) error {
	if !validBoundedText(value.Reason, 256) || !validBoundedText(value.ActorID, 256) || !validBoundedText(value.PolicyRevision, 128) {
		return errors.New("alarm suppression evidence is invalid")
	}
	starts, err := time.Parse(time.RFC3339Nano, value.StartsAt)
	if err != nil {
		return err
	}
	expires, err := time.Parse(time.RFC3339Nano, value.ExpiresAt)
	if err != nil || !expires.After(starts) || expires.Sub(starts) > maximumSuppressionDuration {
		return errors.New("alarm suppression interval is invalid")
	}
	return nil
}

func validateLinks(links []Link, deviceID, eventID, pointID *string) error {
	sourceLinks := map[LinkKind]string{}
	workOrders := map[string]struct{}{}
	for _, link := range links {
		if !IsUUIDv7(link.TargetID) {
			return errors.New("alarm link identity is invalid")
		}
		switch link.Kind {
		case LinkDevice, LinkEvent, LinkPoint:
			if _, exists := sourceLinks[link.Kind]; exists {
				return errors.New("alarm source link kind is duplicated")
			}
			sourceLinks[link.Kind] = link.TargetID
		case LinkWorkOrder:
			if _, exists := workOrders[link.TargetID]; exists {
				return errors.New("alarm work order link is duplicated")
			}
			workOrders[link.TargetID] = struct{}{}
		default:
			return errors.New("alarm link kind is invalid")
		}
	}
	for kind, expected := range map[LinkKind]*string{LinkDevice: deviceID, LinkEvent: eventID, LinkPoint: pointID} {
		actual, exists := sourceLinks[kind]
		if expected == nil && exists || expected != nil && (!exists || actual != *expected) {
			return errors.New("alarm source link is inconsistent")
		}
	}
	return nil
}

func validateTimeline(alarm Alarm) error {
	if len(alarm.Timeline) == 0 || alarm.Timeline[len(alarm.Timeline)-1].Version != alarm.Version || alarm.Timeline[len(alarm.Timeline)-1].Condition != alarm.Condition || alarm.Timeline[len(alarm.Timeline)-1].CurrentSeverity != alarm.CurrentSeverity {
		return errors.New("alarm timeline is incomplete")
	}
	var previousVersion uint64
	previousCondition := ConditionActive
	for index, entry := range alarm.Timeline {
		if entry.Version != previousVersion+1 || !validCondition(entry.Condition) || !validSeverity(entry.CurrentSeverity) || !validBoundedText(entry.ActorType, 64) || !validBoundedText(entry.CorrelationID, 256) || entry.ActorID == nil || entry.PolicyRevision == nil || !validBoundedText(*entry.ActorID, 256) || !validBoundedText(*entry.PolicyRevision, 128) {
			return errors.New("alarm timeline entry is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, entry.OccurredAt); err != nil {
			return errors.New("alarm timeline instant is invalid")
		}
		if index == 0 {
			if entry.Operation != OperationPublish || entry.Condition != ConditionActive || entry.Version != 1 {
				return errors.New("alarm initial timeline entry is invalid")
			}
		} else {
			switch entry.Operation {
			case OperationPublish, OperationAssign, OperationUnassign, OperationSuppress, OperationUnsuppress:
				if previousCondition != ConditionActive || entry.Condition != ConditionActive {
					return errors.New("alarm timeline operation changed physical condition")
				}
			case OperationAcknowledge:
				if entry.Condition != previousCondition {
					return errors.New("alarm acknowledgement changed physical condition")
				}
			case OperationClear:
				if previousCondition != ConditionActive || entry.Condition != ConditionCleared {
					return errors.New("alarm clear transition is invalid")
				}
			default:
				return errors.New("alarm timeline operation is invalid")
			}
		}
		previousVersion = entry.Version
		previousCondition = entry.Condition
	}
	return nil
}

func appendTimeline(alarm *Alarm, operation Operation, reason, actorType, actorID, policyRevision, correlationID, occurredAt string, assigneeID *string, suppression *Suppression) {
	actor := strings.TrimSpace(actorID)
	policy := strings.TrimSpace(policyRevision)
	alarm.Timeline = append(alarm.Timeline, TimelineEntry{
		Operation: operation, Condition: alarm.Condition, Reason: reason, ActorType: strings.TrimSpace(actorType), ActorID: &actor,
		AssigneeID: cloneString(assigneeID), Suppression: cloneSuppression(suppression), CurrentSeverity: alarm.CurrentSeverity,
		PolicyRevision: &policy, CorrelationID: correlationID, OccurredAt: occurredAt, Version: alarm.Version,
	})
}

func sourceLinks(deviceID, eventID, pointID *string) []Link {
	links := make([]Link, 0, 3)
	if deviceID != nil {
		links = append(links, Link{Kind: LinkDevice, TargetID: *deviceID})
	}
	if eventID != nil {
		links = append(links, Link{Kind: LinkEvent, TargetID: *eventID})
	}
	if pointID != nil {
		links = append(links, Link{Kind: LinkPoint, TargetID: *pointID})
	}
	return links
}

func canonicalInstant(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func cloneEvidence(values []EvidenceReference) []EvidenceReference {
	return append([]EvidenceReference{}, values...)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneSuppression(value *Suppression) *Suppression {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneAlarm(alarm Alarm) Alarm {
	result := alarm
	result.DeviceID = cloneString(alarm.DeviceID)
	result.EventID = cloneString(alarm.EventID)
	result.PointID = cloneString(alarm.PointID)
	result.AssigneeID = cloneString(alarm.AssigneeID)
	result.ClearedAt = cloneString(alarm.ClearedAt)
	result.Suppression = cloneSuppression(alarm.Suppression)
	if alarm.Acknowledgement != nil {
		value := *alarm.Acknowledgement
		result.Acknowledgement = &value
	}
	result.Evidence = cloneEvidence(alarm.Evidence)
	result.Links = append([]Link{}, alarm.Links...)
	result.Timeline = append([]TimelineEntry(nil), alarm.Timeline...)
	for index := range result.Timeline {
		result.Timeline[index].ActorID = cloneString(alarm.Timeline[index].ActorID)
		result.Timeline[index].AssigneeID = cloneString(alarm.Timeline[index].AssigneeID)
		result.Timeline[index].PolicyRevision = cloneString(alarm.Timeline[index].PolicyRevision)
		result.Timeline[index].Suppression = cloneSuppression(alarm.Timeline[index].Suppression)
	}
	return result
}
