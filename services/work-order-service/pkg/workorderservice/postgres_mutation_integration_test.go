package workorderservice

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const postgresMutationWorkOrderID = "01930000-1000-7000-8000-000000000031"

func TestPostgresMutationsAreAtomicIdempotentRestartSafeAndScoped(t *testing.T) {
	databaseURL := os.Getenv("S5_WORK_ORDER_TEST_DATABASE_URL")
	mutationDatabaseURL := os.Getenv("S5_WORK_ORDER_MUTATION_TEST_DATABASE_URL")
	adminURL := os.Getenv("S5_WORK_ORDER_ADMIN_DATABASE_URL")
	if databaseURL == "" || mutationDatabaseURL == "" || adminURL == "" {
		t.Skip("S5 Work Order PostgreSQL URLs are not configured")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	cleanup := func() {
		for _, statement := range []string{
			`DELETE FROM work_order_runtime.work_order_mutation_audit WHERE work_order_id = $1`,
			`DELETE FROM work_order_runtime.work_order_idempotency WHERE response_payload->>'workOrderId' = $1`,
			`DELETE FROM work_order_runtime.work_order_timeline WHERE work_order_id = $1`,
			`DELETE FROM work_order_runtime.work_order_source_reference WHERE work_order_id = $1`,
			`DELETE FROM work_order_runtime.work_order_current WHERE work_order_id = $1`,
		} {
			_, _ = admin.Exec(ctx, statement, postgresMutationWorkOrderID)
		}
	}
	cleanup()
	defer cleanup()

	openStore := func() *PostgresStore {
		store, err := OpenPostgresStoreWithMutations(ctx, databaseURL, mutationDatabaseURL, []byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	store := openStore()
	create := CreateMutation{
		WorkOrderID: postgresMutationWorkOrderID,
		Title:       "Inspect economizer linkage", Description: "Confirm the authoritative source and assignment boundary.", Priority: workordermodel.PriorityHigh,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceManual, ResourceID: "manual:p4-postgres-create", Relationship: workordermodel.RelationshipOrigin}},
		ActorType:        "PRINCIPAL", ActorID: "principal:p4-creator", PolicyRevision: "work-order-p4-policy-1", CorrelationID: "p4-create-correlation",
		IdempotencyKey: "p4-create-key-0001", OccurredAt: "2026-08-01T12:00:00Z",
	}
	created, err := store.Create(ctx, postgresOrganizationID, postgresSiteID, create)
	if err != nil || created.Replayed || created.WorkOrder.Version != 1 || created.WorkOrder.WorkOrderID != postgresMutationWorkOrderID {
		t.Fatalf("create result=%#v err=%v", created, err)
	}
	store.Close()

	store = openStore()
	retry := create
	retry.WorkOrderID = "01930000-1000-7000-8000-000000000032"
	replayed, err := store.Create(ctx, postgresOrganizationID, postgresSiteID, retry)
	if err != nil || !replayed.Replayed || replayed.WorkOrder.WorkOrderID != postgresMutationWorkOrderID {
		t.Fatalf("restart replay result=%#v err=%v", replayed, err)
	}
	conflict := create
	conflict.Title = "Different create payload"
	if _, err := store.Create(ctx, postgresOrganizationID, postgresSiteID, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("create idempotency conflict=%v", err)
	}
	assignee := "principal:p4-operator"
	team := "team:p4-controls"
	assignment := AssignmentMutation{
		ExpectedVersion: 1, AssigneeID: &assignee, TeamID: &team, Reason: "route to controls",
		ActorType: "PRINCIPAL", ActorID: "principal:p4-dispatcher", PolicyRevision: "work-order-p4-policy-2", CorrelationID: "p4-assign-correlation",
		IdempotencyKey: strings.Join([]string{"p4", "assign", "key", "0001"}, "-"), OccurredAt: "2026-08-01T12:01:00Z",
	}
	assigned, err := store.Assign(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, assignment)
	if err != nil || assigned.Replayed || assigned.WorkOrder.Version != 2 {
		t.Fatalf("assign result=%#v err=%v", assigned, err)
	}
	store.Close()

	store = openStore()
	defer store.Close()
	assignmentReplay, err := store.Assign(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, assignment)
	if err != nil || !assignmentReplay.Replayed || assignmentReplay.WorkOrder.Version != 2 {
		t.Fatalf("assignment restart replay=%#v err=%v", assignmentReplay, err)
	}
	stale := assignment
	stale.IdempotencyKey = strings.Join([]string{"p4", "assign", "key", "0002"}, "-")
	if _, err := store.Assign(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, stale); !errors.Is(err, workordermodel.ErrVersionConflict) {
		t.Fatalf("stale assignment error=%v", err)
	}
	if _, err := store.Assign(ctx, postgresOtherOrganizationID, postgresSiteID, postgresMutationWorkOrderID, assignment); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Organization assignment disclosed resource: %v", err)
	}
	current, err := store.Get(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID)
	if err != nil || current.Version != 2 || len(current.Timeline) != 2 || current.AssigneeID == nil || *current.AssigneeID != assignee {
		t.Fatalf("authoritative mutation projection=%#v err=%v", current, err)
	}
	var idempotencyCount, auditCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM work_order_runtime.work_order_idempotency WHERE response_payload->>'workOrderId' = $1`, postgresMutationWorkOrderID).Scan(&idempotencyCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM work_order_runtime.work_order_mutation_audit WHERE work_order_id = $1`, postgresMutationWorkOrderID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if idempotencyCount != 2 || auditCount != 2 {
		t.Fatalf("durable mutation evidence idempotency=%d audit=%d", idempotencyCount, auditCount)
	}
}
