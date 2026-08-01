package alarmmodel

import (
	"errors"
	"testing"
)

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

func TestApplyOperationAcknowledgesWithAuditEvidence(t *testing.T) {
	updated, err := ApplyOperation(validAlarm(), operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusAcknowledged || updated.Version != 2 || len(updated.Transitions) != 2 {
		t.Fatalf("unexpected acknowledged projection: %#v", updated)
	}
	transition := updated.Transitions[1]
	if transition.Operation != OperationAcknowledge || transition.FromStatus == nil || *transition.FromStatus != StatusOpen || transition.ToStatus != StatusAcknowledged {
		t.Fatalf("unexpected acknowledgement transition: %#v", transition)
	}
	if transition.ActorID == nil || transition.PolicyRevision == nil || transition.CorrelationID == nil {
		t.Fatal("acknowledgement audit evidence is incomplete")
	}
}

func TestApplyOperationAssignsAndUnassignsWithoutChangingLifecycleStatus(t *testing.T) {
	assignee := "principal:operator-2"
	assign := operationInput(OperationAssign, 1, "2026-07-31T10:00:00Z")
	assign.AssigneeID = &assignee
	assigned, err := ApplyOperation(validAlarm(), assign)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != StatusOpen || assigned.AssigneeID == nil || *assigned.AssigneeID != assignee || assigned.Version != 2 {
		t.Fatalf("unexpected assigned projection: %#v", assigned)
	}
	unassign := operationInput(OperationUnassign, 2, "2026-07-31T10:01:00Z")
	unassigned, err := ApplyOperation(assigned, unassign)
	if err != nil {
		t.Fatal(err)
	}
	if unassigned.Status != StatusOpen || unassigned.AssigneeID != nil || unassigned.Version != 3 {
		t.Fatalf("unexpected unassigned projection: %#v", unassigned)
	}
}

func TestApplyOperationSuppressesAndRestoresPriorAcknowledgedState(t *testing.T) {
	acknowledged, err := ApplyOperation(validAlarm(), operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	suppressionEnd := "2026-07-31T14:01:00Z"
	suppress := operationInput(OperationSuppress, 2, "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &suppressionEnd
	suppressed, err := ApplyOperation(acknowledged, suppress)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Status != StatusSuppressed || suppressed.SuppressedUntil == nil || *suppressed.SuppressedUntil != suppressionEnd {
		t.Fatalf("unexpected suppressed projection: %#v", suppressed)
	}
	unsuppressed, err := ApplyOperation(suppressed, operationInput(OperationUnsuppress, 3, "2026-07-31T10:02:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if unsuppressed.Status != StatusAcknowledged || unsuppressed.SuppressedUntil != nil || unsuppressed.Version != 4 {
		t.Fatalf("unexpected unsuppressed projection: %#v", unsuppressed)
	}
}

func TestApplyOperationRestoresSuppressionOriginAfterAssignmentWhileSuppressed(t *testing.T) {
	acknowledged, err := ApplyOperation(validAlarm(), operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	suppressionEnd := "2026-07-31T14:01:00Z"
	suppress := operationInput(OperationSuppress, 2, "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &suppressionEnd
	suppressed, err := ApplyOperation(acknowledged, suppress)
	if err != nil {
		t.Fatal(err)
	}
	assignee := "principal:operator-2"
	assign := operationInput(OperationAssign, 3, "2026-07-31T10:02:00Z")
	assign.AssigneeID = &assignee
	assigned, err := ApplyOperation(suppressed, assign)
	if err != nil {
		t.Fatal(err)
	}
	unsuppressed, err := ApplyOperation(assigned, operationInput(OperationUnsuppress, 4, "2026-07-31T10:03:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if unsuppressed.Status != StatusAcknowledged || unsuppressed.SuppressedUntil != nil || unsuppressed.AssigneeID == nil || *unsuppressed.AssigneeID != assignee || unsuppressed.Version != 5 {
		t.Fatalf("unexpected unsuppressed projection after assignment: %#v", unsuppressed)
	}
}

func TestAlarmValidationRejectsUnsuppressionToWrongOrigin(t *testing.T) {
	acknowledged, err := ApplyOperation(validAlarm(), operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	suppressionEnd := "2026-07-31T14:01:00Z"
	suppress := operationInput(OperationSuppress, 2, "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &suppressionEnd
	suppressed, err := ApplyOperation(acknowledged, suppress)
	if err != nil {
		t.Fatal(err)
	}
	from := StatusSuppressed
	actorID := "principal:operator-1"
	policyRevision := "alarm-policy-9"
	correlationID := "alarm-operation-forged"
	forged := suppressed
	forged.Status = StatusOpen
	forged.SuppressedUntil = nil
	forged.Version = 4
	forged.UpdatedAt = "2026-07-31T10:02:00Z"
	forged.Transitions = append(forged.Transitions, Transition{
		FromStatus: &from, ToStatus: StatusOpen, Operation: OperationUnsuppress,
		Reason: "forged unsuppression", ActorType: "PRINCIPAL", ActorID: &actorID,
		PolicyRevision: &policyRevision, CorrelationID: &correlationID,
		OccurredAt: forged.UpdatedAt, Version: forged.Version,
	})
	if err := forged.Validate(); err == nil {
		t.Fatal("unsuppression to the wrong pre-suppression state was accepted")
	}
}

func TestApplyOperationClosesAndReopens(t *testing.T) {
	closed, err := ApplyOperation(validAlarm(), operationInput(OperationClose, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != StatusClosed || closed.Version != 2 {
		t.Fatalf("unexpected closed projection: %#v", closed)
	}
	reopened, err := ApplyOperation(closed, operationInput(OperationReopen, 2, "2026-07-31T10:01:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusOpen || reopened.Version != 3 {
		t.Fatalf("unexpected reopened projection: %#v", reopened)
	}
}

func TestApplyOperationRejectsStaleVersionAndIllegalTransitions(t *testing.T) {
	if _, err := ApplyOperation(validAlarm(), operationInput(OperationAcknowledge, 7, "2026-07-31T10:00:00Z")); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	acknowledged, err := ApplyOperation(validAlarm(), operationInput(OperationAcknowledge, 1, "2026-07-31T10:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyOperation(acknowledged, operationInput(OperationAcknowledge, 2, "2026-07-31T10:01:00Z")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid acknowledgement transition, got %v", err)
	}
	tooLong := "2026-09-15T10:01:00Z"
	suppress := operationInput(OperationSuppress, 2, "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &tooLong
	if _, err := ApplyOperation(acknowledged, suppress); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected bounded suppression rejection, got %v", err)
	}
}

func TestAlarmValidationRejectsNonConvergentAssignmentProjection(t *testing.T) {
	assignee := "principal:operator-2"
	input := operationInput(OperationAssign, 1, "2026-07-31T10:00:00Z")
	input.AssigneeID = &assignee
	assigned, err := ApplyOperation(validAlarm(), input)
	if err != nil {
		t.Fatal(err)
	}
	assigned.AssigneeID = nil
	if err := assigned.Validate(); err == nil {
		t.Fatal("non-convergent assignment projection was accepted")
	}
}

func operationInput(operation Operation, expectedVersion uint64, occurredAt string) OperationInput {
	return OperationInput{
		Operation:       operation,
		ExpectedVersion: expectedVersion,
		Reason:          "operator supplied reason",
		ActorType:       "PRINCIPAL",
		ActorID:         "principal:operator-1",
		PolicyRevision:  "alarm-policy-9",
		CorrelationID:   "alarm-operation-01910000",
		OccurredAt:      occurredAt,
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
		Transitions:     []Transition{{ToStatus: status, Operation: OperationPublish, Reason: "ALARM_PUBLISHED", ActorType: "WORKLOAD", OccurredAt: "2026-07-31T09:00:00Z", Version: 1}},
		Version:         1,
		CreatedAt:       "2026-07-31T09:00:00Z",
		UpdatedAt:       "2026-07-31T09:05:00Z",
	}
}
