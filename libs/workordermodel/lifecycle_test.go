package workordermodel

import (
	"errors"
	"testing"
)

func TestApplyLifecycleEnforcesReviewedGraph(t *testing.T) {
	workOrder := lifecycleFixture(t)
	plannedStart := "2026-08-03T01:00:00+00:00"
	plannedDue := "2026-08-03T04:00:00Z"
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{
		Operation: OperationSchedule, ExpectedVersion: 1, ScheduledStart: &plannedStart, DueAt: &plannedDue,
		Reason: "plan maintenance window", OccurredAt: "2026-08-02T01:00:00Z",
	})
	if workOrder.Status != StatusOpen || workOrder.Version != 2 || workOrder.ScheduledStart == nil || *workOrder.ScheduledStart != "2026-08-03T01:00:00Z" || workOrder.Timeline[1].Operation != OperationSchedule {
		t.Fatalf("unexpected plan projection: %#v", workOrder)
	}
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{Operation: OperationStart, ExpectedVersion: 2, Reason: "technician arrived", OccurredAt: "2026-08-03T01:00:00Z"})
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{Operation: OperationBlock, ExpectedVersion: 3, Reason: "replacement bearing unavailable", OccurredAt: "2026-08-03T01:30:00Z"})
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{Operation: OperationResume, ExpectedVersion: 4, Reason: "replacement bearing received", OccurredAt: "2026-08-03T02:00:00Z"})
	evidence := []EvidenceReference{{Kind: "inspection-report", Reference: "object://reports/ahu-17", CapturedAt: "2026-08-03T02:30:00+00:00"}}
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{Operation: OperationComplete, ExpectedVersion: 5, Reason: "repair verified", CompletionEvidence: evidence, OccurredAt: "2026-08-03T03:00:00Z"})
	if workOrder.Status != StatusCompleted || workOrder.Version != 6 || len(workOrder.CompletionEvidence) != 1 || workOrder.CompletionEvidence[0].CapturedAt != "2026-08-03T02:30:00Z" {
		t.Fatalf("unexpected completion projection: %#v", workOrder)
	}
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{Operation: OperationReopen, ExpectedVersion: 6, Reason: "vibration recurred", OccurredAt: "2026-08-04T01:00:00Z"})
	if workOrder.Status != StatusOpen || workOrder.Version != 7 || len(workOrder.CompletionEvidence) != 1 || workOrder.Timeline[6].Operation != OperationReopen {
		t.Fatalf("reopen did not preserve completion history: %#v", workOrder)
	}
}

func TestApplyLifecycleRejectsIllegalOrUnsafeTransitions(t *testing.T) {
	base := lifecycleFixture(t)
	withoutOwnership := cloneWorkOrder(base)
	withoutOwnership.AssigneeID = nil
	withoutOwnership.Timeline[0].AssigneeID = nil
	if _, err := ApplyLifecycle(withoutOwnership, lifecycleInput(OperationStart, 1, "start", "2026-08-02T01:00:00Z")); !errors.Is(err, ErrInvalidLifecycle) {
		t.Fatalf("start without ownership error=%v", err)
	}
	past := "2026-08-01T23:59:59Z"
	future := "2027-08-03T00:00:01Z"
	testCases := map[string]struct {
		input LifecycleInput
		err   error
	}{
		"block from open":    {input: lifecycleInput(OperationBlock, 1, "blocked", "2026-08-02T01:00:00Z"), err: ErrInvalidLifecycle},
		"empty plan":         {input: lifecycleInput(OperationSchedule, 1, "plan", "2026-08-02T01:00:00Z"), err: ErrInvalidLifecycle},
		"past plan":          {input: LifecycleInput{Operation: OperationSchedule, ExpectedVersion: 1, ScheduledStart: &past, Reason: "plan", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-9", CorrelationID: "lifecycle-test", OccurredAt: "2026-08-02T00:00:00Z"}, err: ErrInvalidLifecycle},
		"unbounded plan":     {input: LifecycleInput{Operation: OperationSchedule, ExpectedVersion: 1, DueAt: &future, Reason: "plan", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-9", CorrelationID: "lifecycle-test", OccurredAt: "2026-08-02T00:00:00Z"}, err: ErrInvalidLifecycle},
		"complete from open": {input: LifecycleInput{Operation: OperationComplete, ExpectedVersion: 1, Reason: "complete", CompletionEvidence: []EvidenceReference{{Kind: "report", Reference: "object://report/1", CapturedAt: "2026-08-02T00:30:00Z"}}, ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-9", CorrelationID: "lifecycle-test", OccurredAt: "2026-08-02T01:00:00Z"}, err: ErrInvalidLifecycle},
		"stale version":      {input: lifecycleInput(OperationCancel, 2, "cancel", "2026-08-02T01:00:00Z"), err: ErrVersionConflict},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := ApplyLifecycle(base, testCase.input); !errors.Is(err, testCase.err) {
				t.Fatalf("error=%v expected=%v", err, testCase.err)
			}
		})
	}
}

func lifecycleInput(operation Operation, expectedVersion uint64, reason, occurredAt string) LifecycleInput {
	return LifecycleInput{
		Operation: operation, ExpectedVersion: expectedVersion, Reason: reason,
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-9", CorrelationID: "lifecycle-test", OccurredAt: occurredAt,
	}
}

func TestCompleteRequiresConvergedTasksAndAppendOnlyEvidence(t *testing.T) {
	workOrder := lifecycleFixture(t)
	workOrder = applyLifecycle(t, workOrder, LifecycleInput{Operation: OperationStart, ExpectedVersion: 1, Reason: "start", OccurredAt: "2026-08-02T01:00:00Z"})
	workOrder.Tasks = TaskSummary{Total: 2, Completed: 1}
	input := LifecycleInput{
		Operation: OperationComplete, ExpectedVersion: 2, Reason: "complete",
		CompletionEvidence: []EvidenceReference{{Kind: "report", Reference: "object://report/1", CapturedAt: "2026-08-02T01:30:00Z"}},
		OccurredAt:         "2026-08-02T02:00:00Z",
	}
	if _, err := ApplyLifecycle(workOrder, input); !errors.Is(err, ErrInvalidLifecycle) {
		t.Fatalf("incomplete tasks error=%v", err)
	}
	workOrder.Tasks = TaskSummary{Total: 2, Completed: 2}
	completed := applyLifecycle(t, workOrder, input)
	reopened := applyLifecycle(t, completed, LifecycleInput{Operation: OperationReopen, ExpectedVersion: 3, Reason: "recheck", OccurredAt: "2026-08-02T03:00:00Z"})
	restarted := applyLifecycle(t, reopened, LifecycleInput{Operation: OperationStart, ExpectedVersion: 4, Reason: "restart", OccurredAt: "2026-08-02T04:00:00Z"})
	second := LifecycleInput{
		Operation: OperationComplete, ExpectedVersion: 5, Reason: "verified again",
		CompletionEvidence: []EvidenceReference{{Kind: "report", Reference: "object://report/2", CapturedAt: "2026-08-02T04:30:00Z"}},
		OccurredAt:         "2026-08-02T05:00:00Z",
	}
	completedAgain := applyLifecycle(t, restarted, second)
	if len(completedAgain.CompletionEvidence) != 2 || completedAgain.CompletionEvidence[0].Reference != "object://report/1" || completedAgain.CompletionEvidence[1].Reference != "object://report/2" {
		t.Fatalf("completion evidence was not append-only: %#v", completedAgain.CompletionEvidence)
	}
}

func lifecycleFixture(t *testing.T) WorkOrder {
	t.Helper()
	assignee := "principal:operator-a"
	workOrder, err := Create(CreateInput{
		WorkOrderID: mutationWorkOrderID, TenantID: mutationTenantID, SiteID: mutationSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: PriorityHigh,
		SourceReferences: []SourceReference{{Domain: SourceAlarm, ResourceID: mutationAlarmID, Relationship: RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "idem-create-lifecycle",
		OccurredAt: "2026-08-02T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return workOrder
}

func applyLifecycle(t *testing.T, workOrder WorkOrder, input LifecycleInput) WorkOrder {
	t.Helper()
	input.ActorType = "PRINCIPAL"
	input.ActorID = "principal:operator-a"
	input.PolicyRevision = "policy-9"
	input.CorrelationID = "lifecycle-test"
	updated, err := ApplyLifecycle(workOrder, input)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
