package workorderservice

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	storeTaskOneID = "01930000-1000-7000-8000-000000000041"
	storeTaskTwoID = "01930000-1000-7000-8000-000000000042"
)

func TestMemoryTaskStoreProvidesExactReplayAndUnifiedKeyDomain(t *testing.T) {
	initial := validWorkOrder(testWorkOrderID, testOrganizationID, testSiteID, "2026-08-01T12:00:00Z")
	store, err := NewMemoryStoreWithTasks([]workordermodel.WorkOrder{initial}, nil)
	if err != nil {
		t.Fatal(err)
	}
	appendMutation := AppendTaskMutation{
		TaskID: storeTaskOneID, ExpectedWorkOrderVersion: 1, Title: "Inspect fan bearings", Reason: "append task",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-append-1", IdempotencyKey: "task-key-000001", OccurredAt: "2026-08-01T12:01:00Z",
	}
	appended, err := store.AppendTask(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, appendMutation)
	if err != nil || appended.Replayed || appended.Checklist.WorkOrderVersion != 2 || len(appended.Checklist.Tasks) != 1 {
		t.Fatalf("append=%#v err=%v", appended, err)
	}

	retry := appendMutation
	retry.TaskID = storeTaskTwoID
	retry.PolicyRevision = "policy-retry"
	retry.CorrelationID = "task-retry"
	replayed, err := store.AppendTask(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, retry)
	if err != nil || !replayed.Replayed || replayed.Checklist.Tasks[0].TaskID != storeTaskOneID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}

	statusMutation := TaskStatusMutation{
		TaskID: storeTaskOneID, ExpectedWorkOrderVersion: 2, ExpectedTaskVersion: 1, Status: workordermodel.TaskStatusCompleted,
		Reason: "complete", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-status-reuse", IdempotencyKey: appendMutation.IdempotencyKey, OccurredAt: "2026-08-01T12:02:00Z",
	}
	if _, err := store.SetTaskStatus(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, statusMutation); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("cross-action key reuse=%v", err)
	}

	statusMutation.IdempotencyKey = "task-key-000002"
	completed, err := store.SetTaskStatus(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, statusMutation)
	if err != nil || completed.Checklist.WorkOrderVersion != 3 || completed.Checklist.Summary.Completed != 1 || completed.Checklist.Tasks[0].Version != 2 {
		t.Fatalf("status=%#v err=%v", completed, err)
	}
	originalSnapshot := completed.Checklist

	second, err := store.AppendTask(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, AppendTaskMutation{
		TaskID: storeTaskTwoID, ExpectedWorkOrderVersion: 3, Title: "Record vibration", Reason: "append task",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-append-2", IdempotencyKey: "task-key-000003", OccurredAt: "2026-08-01T12:03:00Z",
	})
	if err != nil || second.Checklist.WorkOrderVersion != 4 {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	exactRetry, err := store.SetTaskStatus(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, statusMutation)
	if err != nil || !exactRetry.Replayed || exactRetry.Checklist.WorkOrderVersion != originalSnapshot.WorkOrderVersion || len(exactRetry.Checklist.Tasks) != 1 {
		t.Fatalf("exact retry=%#v err=%v", exactRetry, err)
	}
}

func TestMemoryTaskStoreRejectsCrossSiteStaleAndInvalidReorder(t *testing.T) {
	initial := validWorkOrder(testWorkOrderID, testOrganizationID, testSiteID, "2026-08-01T12:00:00Z")
	store, err := NewMemoryStoreWithTasks([]workordermodel.WorkOrder{initial}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []AppendTaskMutation{
		{TaskID: storeTaskOneID, ExpectedWorkOrderVersion: 1, Title: "Task one", Reason: "append", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10", CorrelationID: "task-append-1", IdempotencyKey: "task-append-key-01", OccurredAt: "2026-08-01T12:01:00Z"},
		{TaskID: storeTaskTwoID, ExpectedWorkOrderVersion: 2, Title: "Task two", Reason: "append", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10", CorrelationID: "task-append-2", IdempotencyKey: "task-append-key-02", OccurredAt: "2026-08-01T12:02:00Z"},
	}
	for _, mutation := range mutations {
		if _, err := store.AppendTask(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, mutation); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ListTasks(context.Background(), testOrganizationID, testOtherSiteID, testWorkOrderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-site list=%v", err)
	}
	if _, err := store.SetTaskStatus(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, TaskStatusMutation{
		TaskID: storeTaskOneID, ExpectedWorkOrderVersion: 2, ExpectedTaskVersion: 1, Status: workordermodel.TaskStatusBlocked,
		Reason: "stale", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-stale", IdempotencyKey: "task-key-stale1", OccurredAt: "2026-08-01T12:03:00Z",
	}); !errors.Is(err, workordermodel.ErrVersionConflict) {
		t.Fatalf("stale=%v", err)
	}
	if _, err := store.ReorderTasks(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, ReorderTasksMutation{
		ExpectedWorkOrderVersion: 3, TaskIDs: []string{storeTaskOneID, storeTaskOneID}, Reason: "duplicate",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-reorder", IdempotencyKey: "task-key-reorder", OccurredAt: "2026-08-01T12:03:00Z",
	}); !errors.Is(err, workordermodel.ErrInvalidTask) {
		t.Fatalf("duplicate reorder=%v", err)
	}
}
