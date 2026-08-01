package workorderservice

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestMemoryStoreCreateIsIdempotentAndConflictsOnKeyReuse(t *testing.T) {
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	mutation := CreateMutation{
		WorkOrderID: "01930000-1000-7000-8000-000000000010",
		Title:       "Inspect AHU fan", Description: "Validate vibration.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: "01930000-2000-7000-8000-000000000001", Relationship: workordermodel.RelationshipOrigin}},
		ActorType:        "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "correlation-create-1",
		IdempotencyKey: "create-00000001", OccurredAt: "2026-08-01T12:00:00Z",
	}
	created, err := store.Create(context.Background(), testOrganizationID, testSiteID, mutation)
	if err != nil || created.Replayed || created.WorkOrder.Version != 1 {
		t.Fatalf("create result=%#v err=%v", created, err)
	}
	retry := mutation
	retry.WorkOrderID = "01930000-1000-7000-8000-000000000011"
	retry.PolicyRevision = "policy-8"
	retry.CorrelationID = "correlation-create-retry"
	replayed, err := store.Create(context.Background(), testOrganizationID, testSiteID, retry)
	if err != nil || !replayed.Replayed || replayed.WorkOrder.WorkOrderID != created.WorkOrder.WorkOrderID {
		t.Fatalf("replay result=%#v err=%v", replayed, err)
	}
	conflict := mutation
	conflict.Title = "Different request"
	if _, err := store.Create(context.Background(), testOrganizationID, testSiteID, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	page, err := store.List(context.Background(), testOrganizationID, testSiteID, Filter{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("idempotent create duplicated projection: %#v err=%v", page, err)
	}
}

func TestMemoryStoreAssignmentIsAtomicReplaySafeAndVersioned(t *testing.T) {
	initial := validWorkOrder(testWorkOrderID, testOrganizationID, testSiteID, "2026-08-01T12:00:00Z")
	store, err := NewMemoryStore([]workordermodel.WorkOrder{initial})
	if err != nil {
		t.Fatal(err)
	}
	assignee := "principal:operator-b"
	team := "team:controls"
	mutation := AssignmentMutation{
		ExpectedVersion: 1, AssigneeID: &assignee, TeamID: &team, Reason: "route to controls",
		ActorType: "PRINCIPAL", ActorID: "principal:dispatcher", PolicyRevision: "policy-8", CorrelationID: "correlation-assign-1",
		IdempotencyKey: "assign-00000001", OccurredAt: "2026-08-01T12:01:00Z",
	}
	assigned, err := store.Assign(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, mutation)
	if err != nil || assigned.Replayed || assigned.WorkOrder.Version != 2 {
		t.Fatalf("assign result=%#v err=%v", assigned, err)
	}
	retry := mutation
	retry.PolicyRevision = "policy-9"
	retry.CorrelationID = "correlation-assign-retry"
	replayed, err := store.Assign(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, retry)
	if err != nil || !replayed.Replayed || replayed.WorkOrder.Version != 2 {
		t.Fatalf("replay result=%#v err=%v", replayed, err)
	}
	stale := mutation
	stale.IdempotencyKey = "assign-00000002"
	if _, err := store.Assign(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, stale); !errors.Is(err, workordermodel.ErrVersionConflict) {
		t.Fatalf("stale error=%v", err)
	}
	current, err := store.Get(context.Background(), testOrganizationID, testSiteID, testWorkOrderID)
	if err != nil || current.Version != 2 || len(current.Timeline) != 2 {
		t.Fatalf("stale mutation changed state: %#v err=%v", current, err)
	}
	conflict := mutation
	conflict.TeamID = nil
	if _, err := store.Assign(context.Background(), testOrganizationID, testSiteID, testWorkOrderID, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error=%v", err)
	}
	if _, err := store.Assign(context.Background(), testOrganizationID, testOtherSiteID, testWorkOrderID, mutation); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-site assignment error=%v", err)
	}
}
