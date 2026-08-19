package alarmmodel

import (
	"errors"
	"testing"
)

func TestIncidentUsesOrthogonalConditionAcknowledgementAndSuppression(t *testing.T) {
	alarm := validAlarm(t)
	if alarm.Condition != ConditionActive || alarm.Acknowledgement != nil || alarm.Suppression != nil {
		t.Fatalf("unexpected initial incident: %#v", alarm)
	}

	acknowledged, err := ApplyOperation(alarm, operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Condition != ConditionActive || acknowledged.Acknowledgement == nil || acknowledged.Version != 2 {
		t.Fatalf("ACK changed condition or failed to persist acknowledgement: %#v", acknowledged)
	}

	expiresAt := "2026-07-31T14:01:00Z"
	suppress := operationInput(OperationSuppress, 2, "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &expiresAt
	suppressed, err := ApplyOperation(acknowledged, suppress)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Condition != ConditionActive || suppressed.Suppression == nil || suppressed.Acknowledgement == nil {
		t.Fatalf("suppression changed the physical condition facts: %#v", suppressed)
	}

	unsuppressed, err := ApplyOperation(suppressed, operationInput(OperationUnsuppress, 3, "2026-07-31T10:02:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if unsuppressed.Condition != ConditionActive || unsuppressed.Suppression != nil || unsuppressed.Acknowledgement == nil {
		t.Fatalf("unsuppression changed unrelated Alarm facts: %#v", unsuppressed)
	}
}

func TestAcknowledgementIsNaturallyIdempotent(t *testing.T) {
	acknowledged, err := ApplyOperation(validAlarm(t), operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := ApplyOperation(acknowledged, operationInput(OperationAcknowledge, 0, "2026-07-31T10:01:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Version != acknowledged.Version || len(replayed.Timeline) != len(acknowledged.Timeline) {
		t.Fatalf("duplicate ACK mutated immutable history: %#v", replayed)
	}
}

func TestOccurrenceCanLowerCurrentSeverityWithoutLosingPeak(t *testing.T) {
	alarm := validAlarm(t)
	critical, err := RecordOccurrence(alarm, OccurrenceInput{
		Severity: SeverityCritical, OccurredAt: "2026-07-31T10:00:00Z", RuleRevision: "alarm-policy-10",
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "occurrence-critical",
	})
	if err != nil {
		t.Fatal(err)
	}
	minor, err := RecordOccurrence(critical, OccurrenceInput{
		Severity: SeverityMinor, OccurredAt: "2026-07-31T10:01:00Z", RuleRevision: "alarm-policy-10",
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "occurrence-minor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if minor.CurrentSeverity != SeverityMinor || minor.PeakSeverity != SeverityCritical || minor.OccurrenceCount != 3 {
		t.Fatalf("unexpected severity projection: %#v", minor)
	}
}

func TestClearIsRecoveryFactAndHistoricalIncidentCannotReopen(t *testing.T) {
	cleared, err := ClearIncident(validAlarm(t), ClearInput{
		OccurredAt: "2026-07-31T10:00:00Z", Reason: "clear predicate matched",
		RuleRevision: "alarm-policy-10", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "clear-evidence-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Condition != ConditionCleared || cleared.ClearedAt == nil {
		t.Fatalf("clear did not persist recovery fact: %#v", cleared)
	}
	if _, err := RecordOccurrence(cleared, OccurrenceInput{
		Severity: SeverityMajor, OccurredAt: "2026-07-31T10:05:00Z", RuleRevision: "alarm-policy-10",
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "recurrence-1",
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("historical incident was reopenable: %v", err)
	}
}

func TestAcknowledgementAfterRecoveryDoesNotChangeClearedCondition(t *testing.T) {
	cleared, err := ClearIncident(validAlarm(t), ClearInput{
		OccurredAt: "2026-07-31T10:00:00Z", Reason: "clear predicate matched",
		RuleRevision: "alarm-policy-10", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "clear-evidence-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	acknowledged, err := ApplyOperation(cleared, operationInput(OperationAcknowledge, cleared.Version, "2026-07-31T10:01:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Condition != ConditionCleared || acknowledged.Acknowledgement == nil || acknowledged.ClearedAt == nil || acknowledged.Version != cleared.Version+1 {
		t.Fatalf("ACK changed the recovered condition: %#v", acknowledged)
	}
}

func TestSystemTimelineIsRequiredAndTamperingIsRejected(t *testing.T) {
	alarm := validAlarm(t)
	alarm.Timeline[0].Condition = ConditionCleared
	if err := alarm.Validate(); err == nil {
		t.Fatal("tampered system timeline was accepted")
	}
}

func TestAlarmRejectsDuplicateSourceLinkKind(t *testing.T) {
	alarm := validAlarm(t)
	deviceID := "018f3e00-5000-7000-8000-000000000001"
	alarm.DeviceID = &deviceID
	fingerprint, err := Fingerprint(alarm.TenantID, alarm.SiteID, alarm.SourceType, alarm.SourceReference, alarm.AlarmType, alarm.DeviceID, alarm.PointID)
	if err != nil {
		t.Fatal(err)
	}
	alarm.Fingerprint = fingerprint
	alarm.Links = []Link{{Kind: LinkDevice, TargetID: deviceID}, {Kind: LinkDevice, TargetID: "018f3e00-5000-7000-8000-000000000002"}}
	if err := alarm.Validate(); err == nil {
		t.Fatal("duplicate source link kind was accepted")
	}
}

func TestAlarmListRejectsCrossSiteProjection(t *testing.T) {
	alarm := validAlarm(t)
	response := ListResponse{SchemaVersion: SchemaVersion, Items: []Alarm{alarm}}
	if err := response.Validate(alarm.TenantID, "018f3e00-2000-7000-8000-000000000002", 50); err == nil {
		t.Fatal("cross-Site Alarm projection was accepted")
	}
}

func TestFingerprintIsStableForTheSameBusinessCondition(t *testing.T) {
	alarm := validAlarm(t)
	fingerprint, err := Fingerprint(alarm.TenantID, alarm.SiteID, alarm.SourceType, alarm.SourceReference, alarm.AlarmType, alarm.DeviceID, alarm.PointID)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != alarm.Fingerprint {
		t.Fatalf("fingerprint changed: got %s want %s", fingerprint, alarm.Fingerprint)
	}
}

func operationInput(operation Operation, expectedVersion uint64, occurredAt string) OperationInput {
	return OperationInput{
		Operation: operation, ExpectedVersion: expectedVersion, Reason: "operator supplied reason",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-1", PolicyRevision: "alarm-policy-9",
		CorrelationID: "alarm-operation-01910000", OccurredAt: occurredAt,
	}
}

func validAlarm(t *testing.T) Alarm {
	t.Helper()
	alarm, err := NewIncident(IncidentInput{
		AlarmID:               "018f3e00-4000-7000-8000-000000000001",
		TenantID:              "018f3e00-1000-7000-8000-000000000001",
		SiteID:                "018f3e00-2000-7000-8000-000000000001",
		AlarmType:             "SUPPLY_TEMPERATURE_DRIFT",
		IncidentCorrelationID: "018f3e00-4000-7000-8000-000000000002",
		SourceType:            SourceSiteRule,
		SourceReference:       "rule:central-plant-temperature-drift:v3",
		RuleRevision:          "alarm-policy-9",
		Title:                 "Supply temperature drift",
		Summary:               "The Alarm owner published a durable operational exception.",
		Severity:              SeverityMajor,
		OccurredAt:            "2026-07-31T09:00:00Z",
		Evidence:              []EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:41", CapturedAt: "2026-07-31T09:00:00Z"}},
		ActorType:             "WORKLOAD",
		ActorID:               "alarm-evaluator",
	})
	if err != nil {
		t.Fatal(err)
	}
	return alarm
}
