package notificationservice

import (
	"errors"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

type AlarmAction string

const (
	AlarmCreated         AlarmAction = "CREATED"
	AlarmSeverityChanged AlarmAction = "SEVERITY_CHANGED"
	AlarmAcknowledged    AlarmAction = "ACKNOWLEDGED"
	AlarmCleared         AlarmAction = "CLEARED"
)

// AlarmEvent is one change of an Alarm, as the Alarm owner's outbox hands it over.
type AlarmEvent struct {
	TenantID              string               `json:"tenantId"`
	SiteID                string               `json:"siteId"`
	SourceEventID         string               `json:"sourceEventId"`
	AlarmID               string               `json:"alarmId"`
	IncidentCorrelationID string               `json:"incidentCorrelationId"`
	Action                AlarmAction          `json:"action"`
	CurrentSeverity       alarmmodel.Severity  `json:"currentSeverity"`
	PeakSeverity          alarmmodel.Severity  `json:"peakSeverity"`
	Condition             alarmmodel.Condition `json:"condition"`
	OccurredAt            time.Time            `json:"occurredAt"`
	Attributes            map[string]string    `json:"attributes,omitempty"`
}

func (event AlarmEvent) Validate() error {
	if !alarmmodel.IsUUIDv7(event.TenantID) || !alarmmodel.IsUUIDv7(event.SiteID) || !alarmmodel.IsUUIDv7(event.SourceEventID) || !alarmmodel.IsUUIDv7(event.AlarmID) || !alarmmodel.IsUUIDv7(event.IncidentCorrelationID) || event.OccurredAt.IsZero() {
		return errors.New("notification Alarm event identity is invalid")
	}
	if !validAlarmAction(event.Action) {
		return errors.New("notification Alarm action is invalid")
	}
	if !validSeverity(event.CurrentSeverity) || !validSeverity(event.PeakSeverity) || (event.Condition != alarmmodel.ConditionActive && event.Condition != alarmmodel.ConditionCleared) {
		return errors.New("notification Alarm state projection is invalid")
	}
	if event.Action == AlarmCleared && event.Condition != alarmmodel.ConditionCleared {
		return errors.New("cleared notification event must carry CLEARED condition")
	}
	if event.Action != AlarmCleared && event.Condition == alarmmodel.ConditionCleared && event.Action != AlarmAcknowledged {
		return errors.New("non-clear notification event has inconsistent condition")
	}
	return nil
}

func validAlarmAction(action AlarmAction) bool {
	switch action {
	case AlarmCreated, AlarmSeverityChanged, AlarmAcknowledged, AlarmCleared:
		return true
	default:
		return false
	}
}

func validSeverity(severity alarmmodel.Severity) bool {
	switch severity {
	case alarmmodel.SeverityInfo, alarmmodel.SeverityWarning, alarmmodel.SeverityMinor, alarmmodel.SeverityMajor, alarmmodel.SeverityCritical:
		return true
	default:
		return false
	}
}
