package workorderservice

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestMemoryStoreLifecycleIsAtomicReplaySafeAndVersioned(t *testing.T) {
	assignee := "principal:operator-a"
	initial, err := workordermodel.Create(workordermodel.CreateInput{
		WorkOrderID: testWorkOrderID, OrganizationID: testOrganizationID, SiteID: testSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: "01910000-4000-7000-8000-000000000001", Relationship: workordermodel.RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "create-lifecycle",
		OccurredAt: "2026-08-02T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewMemoryStore([]workordermodel.WorkOrder{initial})
	if err != nil {
		t.Fatal(err)
	}
	mutation := lifecycleStoreMutation(workordermodel.OperationStart, 1, "start work", "start-00000001", "2026-08-02T01:00:00Z")
	started, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, mutation)
	if err != nil || started.Replayed || started.WorkOrder.Status != workordermodel.StatusInProgress || started.WorkOrder.Version != 2 {
		t.Fatalf("start result=%#v err=%v", started, err)
	}
	retry := mutation
	retry.PolicyRevision = "policy-retry"
	retry.CorrelationID = "retry-correlation"
	replayed, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, retry)
	if err != nil || !replayed.Replayed || replayed.WorkOrder.Version != 2 {
		t.Fatalf("replay result=%#v err=%v", replayed, err)
	}
	conflict := mutation
	conflict.Reason = "different request"
	if _, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict=%v", err)
	}
	crossAction := lifecycleStoreMutation(workordermodel.OperationBlock, 2, "blocked", mutation.IdempotencyKey, "2026-08-02T02:00:00Z")
	if _, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, crossAction); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("cross-action idempotency conflict=%v", err)
	}
	stale := lifecycleStoreMutation(workordermodel.OperationBlock, 1, "blocked", "block-00000001", "2026-08-02T02:00:00Z")
	if _, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, stale); !errors.Is(err, workordermodel.ErrVersionConflict) {
		t.Fatalf("stale error=%v", err)
	}
	current, err := store.Get(context.Background(), testOrganizationID, testSiteID, testWorkOrderID)
	if err != nil || current.Version != 2 || len(current.Timeline) != 2 {
		t.Fatalf("failed mutation changed state: %#v err=%v", current, err)
	}
	if _, err := store.Transition(context.Background(), testOrganizationID, testOtherSiteID, testWorkOrderID, mutation); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-site transition error=%v", err)
	}
}

func TestMemoryStoreLifecycleCompletesAndReopensWithAppendOnlyEvidence(t *testing.T) {
	assignee := "principal:operator-a"
	initial, err := workordermodel.Create(workordermodel.CreateInput{
		WorkOrderID: testWorkOrderID, OrganizationID: testOrganizationID, SiteID: testSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: "01910000-4000-7000-8000-000000000001", Relationship: workordermodel.RelationshipOrigin}},
		AssigneeID:       &assignee, ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "create-lifecycle",
		OccurredAt: "2026-08-02T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewMemoryStore([]workordermodel.WorkOrder{initial})
	_, err = store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, lifecycleStoreMutation(workordermodel.OperationStart, 1, "start", "start-00000002", "2026-08-02T01:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	complete := lifecycleStoreMutation(workordermodel.OperationComplete, 2, "verified", "complete-000001", "2026-08-02T02:00:00Z")
	complete.CompletionEvidence = []workordermodel.EvidenceReference{{Kind: "report", Reference: "object://report/1", CapturedAt: "2026-08-02T01:30:00Z"}}
	completed, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, complete)
	if err != nil || completed.WorkOrder.Status != workordermodel.StatusCompleted || len(completed.WorkOrder.CompletionEvidence) != 1 {
		t.Fatalf("complete result=%#v err=%v", completed, err)
	}
	reopened, err := store.Transition(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, lifecycleStoreMutation(workordermodel.OperationReopen, 3, "recheck", "reopen-0000001", "2026-08-02T03:00:00Z"))
	if err != nil || reopened.WorkOrder.Status != workordermodel.StatusOpen || len(reopened.WorkOrder.CompletionEvidence) != 1 {
		t.Fatalf("reopen result=%#v err=%v", reopened, err)
	}
}

func lifecycleStoreMutation(operation workordermodel.Operation, expectedVersion uint64, reason, key, occurredAt string) LifecycleMutation {
	return LifecycleMutation{
		Operation: operation, ExpectedVersion: expectedVersion, Reason: reason,
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-9", CorrelationID: key,
		IdempotencyKey: key, OccurredAt: occurredAt,
	}
}
