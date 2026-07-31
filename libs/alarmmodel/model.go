package alarmmodel

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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
	FromStatus *Status `json:"fromStatus,omitempty"`
	ToStatus   Status  `json:"toStatus"`
	Reason     string  `json:"reason"`
	ActorType  string  `json:"actorType"`
	ActorID    *string `json:"actorId,omitempty"`
	OccurredAt string  `json:"occurredAt"`
	Version    uint64  `json:"version"`
}

type Alarm struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	AlarmID         string              `json:"alarmId"`
	OrganizationID  string              `json:"organizationId"`
	SiteID          string              `json:"siteId"`
	DeviceID        *string             `json:"deviceId,omitempty"`
	SourceType      SourceType          `json:"sourceType"`
	SourceReference string              `json:"sourceReference"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	Severity        Severity            `json:"severity"`
	Status          Status              `json:"status"`
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

func IsUUIDv7(value string) bool { return uuidV7Pattern.MatchString(value) }

func (alarm Alarm) Validate() error {
	if alarm.SchemaVersion != SchemaVersion || !IsUUIDv7(alarm.AlarmID) || !IsUUIDv7(alarm.OrganizationID) || !IsUUIDv7(alarm.SiteID) {
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
	if err := validateTransitions(alarm.Transitions, alarm.Status, alarm.Version); err != nil {
		return err
	}
	return nil
}

func (response ListResponse) Validate(organizationID, siteID string, limit int) error {
	if response.SchemaVersion != SchemaVersion || !IsUUIDv7(organizationID) || !IsUUIDv7(siteID) || limit < 1 || limit > 100 || len(response.Items) > limit {
		return errors.New("alarm list envelope is invalid")
	}
	seen := map[string]struct{}{}
	for _, alarm := range response.Items {
		if err := alarm.Validate(); err != nil || alarm.OrganizationID != organizationID || alarm.SiteID != siteID {
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

func validSource(value SourceType) bool {
	switch value {
	case SourceDeviceRule, SourceSiteRule, SourceExternal:
		return true
	default:
		return false
	}
}

func validateTransitions(transitions []Transition, status Status, version uint64) error {
	if len(transitions) == 0 || transitions[len(transitions)-1].Version != version || transitions[len(transitions)-1].ToStatus != status {
		return errors.New("alarm transition timeline is incomplete")
	}
	var previousVersion uint64
	var previousStatus *Status
	for index, transition := range transitions {
		if !validStatus(transition.ToStatus) || strings.TrimSpace(transition.Reason) == "" || strings.TrimSpace(transition.ActorType) == "" || transition.Version == 0 || transition.Version <= previousVersion {
			return errors.New("alarm transition is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, transition.OccurredAt); err != nil {
			return errors.New("alarm transition instant is invalid")
		}
		if index == 0 {
			if transition.FromStatus != nil {
				return errors.New("alarm initial transition has a source status")
			}
		} else if transition.FromStatus == nil || previousStatus == nil || *transition.FromStatus != *previousStatus {
			return errors.New("alarm transition chain is invalid")
		}
		current := transition.ToStatus
		previousStatus = &current
		previousVersion = transition.Version
	}
	return nil
}
