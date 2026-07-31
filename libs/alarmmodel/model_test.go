package alarmmodel

import "testing"

func TestAlarmValidationPreservesOwnedLifecycle(t *testing.T) {
	alarm := validAlarm()
	if err := alarm.Validate(); err != nil {
		t.Fatal(err)
	}
	response := ListResponse{SchemaVersion: SchemaVersion, Items: []Alarm{alarm}}
	if err := response.Validate(alarm.OrganizationID, alarm.SiteID, 50); err != nil {
		t.Fatal(err)
	}
}

func TestAlarmListRejectsCrossSiteProjection(t *testing.T) {
	alarm := validAlarm()
	response := ListResponse{SchemaVersion: SchemaVersion, Items: []Alarm{alarm}}
	if err := response.Validate(alarm.OrganizationID, "018f3e00-2000-7000-8000-000000000002", 50); err == nil {
		t.Fatal("cross-Site Alarm projection was accepted")
	}
}

func TestAlarmRequiresPublishedLifecycleRatherThanTelemetryInference(t *testing.T) {
	alarm := validAlarm()
	alarm.SourceReference = ""
	alarm.Transitions = nil
	if err := alarm.Validate(); err == nil {
		t.Fatal("Alarm without owner-published source and timeline was accepted")
	}
}

func validAlarm() Alarm {
	status := StatusOpen
	return Alarm{
		SchemaVersion:   SchemaVersion,
		AlarmID:         "018f3e00-4000-7000-8000-000000000001",
		OrganizationID:  "018f3e00-1000-7000-8000-000000000001",
		SiteID:          "018f3e00-2000-7000-8000-000000000001",
		SourceType:      SourceSiteRule,
		SourceReference: "rule:central-plant-temperature-drift:v3",
		Title:           "Supply temperature drift",
		Summary:         "The Alarm owner published a durable operational exception.",
		Severity:        SeverityMajor,
		Status:          status,
		OccurrenceCount: 2,
		FirstOccurredAt: "2026-07-31T09:00:00Z",
		LastOccurredAt:  "2026-07-31T09:05:00Z",
		Evidence:        []EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:41", CapturedAt: "2026-07-31T09:05:00Z"}},
		Transitions:     []Transition{{ToStatus: status, Reason: "ALARM_PUBLISHED", ActorType: "WORKLOAD", OccurredAt: "2026-07-31T09:00:00Z", Version: 1}},
		Version:         1,
		CreatedAt:       "2026-07-31T09:00:00Z",
		UpdatedAt:       "2026-07-31T09:05:00Z",
	}
}
