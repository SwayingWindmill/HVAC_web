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

const (
	postgresMutationWorkOrderID = "01930000-1000-7000-8000-000000000031"
	postgresMutationTaskOneID   = "01930000-4000-7000-8000-000000000051"
	postgresMutationTaskTwoID   = "01930000-4000-7000-8000-000000000052"
)

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
			`DELETE FROM work_order_runtime.work_order_completion_evidence WHERE work_order_id = $1`,
			`DELETE FROM work_order_runtime.work_order_task WHERE work_order_id = $1`,
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
	firstAppend := AppendTaskMutation{
		TaskID: postgresMutationTaskOneID, ExpectedWorkOrderVersion: 2, Title: "Inspect fan bearings", Reason: "append first task",
		ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-1", CorrelationID: "p6-append-1",
		IdempotencyKey: "p6-task-key-0001", OccurredAt: "2026-08-01T12:02:00Z",
	}
	appendedFirst, err := store.AppendTask(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, firstAppend)
	if err != nil || appendedFirst.Replayed || appendedFirst.Checklist.WorkOrderVersion != 3 || len(appendedFirst.Checklist.Tasks) != 1 {
		t.Fatalf("first task append=%#v err=%v", appendedFirst, err)
	}
	appendedSecond, err := store.AppendTask(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, AppendTaskMutation{
		TaskID: postgresMutationTaskTwoID, ExpectedWorkOrderVersion: 3, Title: "Record vibration", Reason: "append second task",
		ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-1", CorrelationID: "p6-append-2",
		IdempotencyKey: "p6-task-key-0002", OccurredAt: "2026-08-01T12:03:00Z",
	})
	if err != nil || appendedSecond.Checklist.WorkOrderVersion != 4 || len(appendedSecond.Checklist.Tasks) != 2 {
		t.Fatalf("second task append=%#v err=%v", appendedSecond, err)
	}
	firstStatus := TaskStatusMutation{
		TaskID: postgresMutationTaskOneID, ExpectedWorkOrderVersion: 4, ExpectedTaskVersion: 1, Status: workordermodel.TaskStatusCompleted,
		Reason: "bearing inspection complete", ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-2", CorrelationID: "p6-status-1",
		IdempotencyKey: "p6-task-key-0003", OccurredAt: "2026-08-01T12:04:00Z",
	}
	completedFirst, err := store.SetTaskStatus(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, firstStatus)
	if err != nil || completedFirst.Checklist.WorkOrderVersion != 5 || completedFirst.Checklist.Summary.Completed != 1 || completedFirst.Checklist.Tasks[0].Version != 2 {
		t.Fatalf("first task status=%#v err=%v", completedFirst, err)
	}
	firstStatusSnapshot := completedFirst.Checklist
	if _, err := store.ReorderTasks(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, ReorderTasksMutation{
		ExpectedWorkOrderVersion: 5, TaskIDs: []string{postgresMutationTaskTwoID, postgresMutationTaskOneID}, Reason: "cross-action key conflict",
		ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-2", CorrelationID: "p6-reorder-conflict",
		IdempotencyKey: firstStatus.IdempotencyKey, OccurredAt: "2026-08-01T12:05:00Z",
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("task cross-action idempotency error=%v", err)
	}
	reordered, err := store.ReorderTasks(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, ReorderTasksMutation{
		ExpectedWorkOrderVersion: 5, TaskIDs: []string{postgresMutationTaskTwoID, postgresMutationTaskOneID}, Reason: "measure before closeout",
		ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-2", CorrelationID: "p6-reorder-1",
		IdempotencyKey: "p6-task-key-0004", OccurredAt: "2026-08-01T12:05:00Z",
	})
	if err != nil || reordered.Checklist.WorkOrderVersion != 6 || reordered.Checklist.Tasks[0].TaskID != postgresMutationTaskTwoID || reordered.Checklist.Tasks[1].TaskID != postgresMutationTaskOneID {
		t.Fatalf("task reorder=%#v err=%v", reordered, err)
	}
	if _, err := store.SetTaskStatus(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, TaskStatusMutation{
		TaskID: postgresMutationTaskTwoID, ExpectedWorkOrderVersion: 5, ExpectedTaskVersion: 2, Status: workordermodel.TaskStatusCompleted,
		Reason: "stale work order", ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-2", CorrelationID: "p6-status-stale",
		IdempotencyKey: "p6-task-key-0005", OccurredAt: "2026-08-01T12:05:30Z",
	}); !errors.Is(err, workordermodel.ErrVersionConflict) {
		t.Fatalf("stale task status error=%v", err)
	}
	completedSecond, err := store.SetTaskStatus(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, TaskStatusMutation{
		TaskID: postgresMutationTaskTwoID, ExpectedWorkOrderVersion: 6, ExpectedTaskVersion: 2, Status: workordermodel.TaskStatusCompleted,
		Reason: "vibration recorded", ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-2", CorrelationID: "p6-status-2",
		IdempotencyKey: "p6-task-key-0006", OccurredAt: "2026-08-01T12:06:00Z",
	})
	if err != nil || completedSecond.Checklist.WorkOrderVersion != 7 || completedSecond.Checklist.Summary.Completed != 2 {
		t.Fatalf("second task status=%#v err=%v", completedSecond, err)
	}
	store.Close()
	store = openStore()
	defer store.Close()
	statusReplay, err := store.SetTaskStatus(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, firstStatus)
	if err != nil || !statusReplay.Replayed || statusReplay.Checklist.WorkOrderVersion != firstStatusSnapshot.WorkOrderVersion || statusReplay.Checklist.Tasks[0].TaskID != postgresMutationTaskOneID {
		t.Fatalf("task restart replay=%#v err=%v", statusReplay, err)
	}
	listedTasks, err := store.ListTasks(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID)
	if err != nil || listedTasks.WorkOrderVersion != 7 || listedTasks.Tasks[0].TaskID != postgresMutationTaskTwoID || listedTasks.Tasks[0].Position != 0 || listedTasks.Tasks[1].Position != 1 {
		t.Fatalf("authoritative task checklist=%#v err=%v", listedTasks, err)
	}
	if _, err := store.ListTasks(ctx, postgresOtherOrganizationID, postgresSiteID, postgresMutationWorkOrderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Organization task list disclosed resource: %v", err)
	}

	start := LifecycleMutation{
		Operation: workordermodel.OperationStart, ExpectedVersion: 7, Reason: "begin repair",
		ActorType: "PRINCIPAL", ActorID: "principal:p5-operator", PolicyRevision: "work-order-p5-policy-1", CorrelationID: "p5-start-correlation",
		IdempotencyKey: "p5-start-key-0001", OccurredAt: "2026-08-01T12:07:00Z",
	}
	started, err := store.Transition(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, start)
	if err != nil || started.WorkOrder.Status != workordermodel.StatusInProgress || started.WorkOrder.Version != 8 {
		t.Fatalf("start result=%#v err=%v", started, err)
	}
	crossAction := LifecycleMutation{
		Operation: workordermodel.OperationBlock, ExpectedVersion: 8, Reason: "blocked",
		ActorType: "PRINCIPAL", ActorID: "principal:p5-operator", PolicyRevision: "work-order-p5-policy-1", CorrelationID: "p5-cross-action-correlation",
		IdempotencyKey: start.IdempotencyKey, OccurredAt: "2026-08-01T12:07:30Z",
	}
	if _, err := store.Transition(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, crossAction); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("cross-action idempotency error=%v", err)
	}
	complete := LifecycleMutation{
		Operation: workordermodel.OperationComplete, ExpectedVersion: 8, Reason: "repair verified",
		CompletionEvidence: []workordermodel.EvidenceReference{{Kind: "verification-report", Reference: "object://p6/verification/1", CapturedAt: "2026-08-01T12:07:30Z"}},
		ActorType:          "PRINCIPAL", ActorID: "principal:p5-operator", PolicyRevision: "work-order-p5-policy-2", CorrelationID: "p5-complete-correlation",
		IdempotencyKey: "p5-complete-key-01", OccurredAt: "2026-08-01T12:08:00Z",
	}
	completed, err := store.Transition(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, complete)
	if err != nil || completed.WorkOrder.Status != workordermodel.StatusCompleted || completed.WorkOrder.Version != 9 || completed.WorkOrder.Tasks.Completed != 2 || len(completed.WorkOrder.CompletionEvidence) != 1 {
		t.Fatalf("complete result=%#v err=%v", completed, err)
	}
	store.Close()
	store = openStore()
	defer store.Close()
	completeReplay, err := store.Transition(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, complete)
	if err != nil || !completeReplay.Replayed || completeReplay.WorkOrder.Version != 9 {
		t.Fatalf("completion restart replay=%#v err=%v", completeReplay, err)
	}
	staleLifecycle := start
	staleLifecycle.IdempotencyKey = "p5-start-key-0002"
	if _, err := store.Transition(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, staleLifecycle); !errors.Is(err, workordermodel.ErrVersionConflict) {
		t.Fatalf("stale lifecycle error=%v", err)
	}
	if _, err := store.AppendTask(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID, AppendTaskMutation{
		TaskID: "01930000-4000-7000-8000-000000000053", ExpectedWorkOrderVersion: 9, Title: "Forbidden terminal task", Reason: "terminal",
		ActorType: "PRINCIPAL", ActorID: assignee, PolicyRevision: "work-order-p6-policy-3", CorrelationID: "p6-terminal",
		IdempotencyKey: "p6-task-key-0007", OccurredAt: "2026-08-01T12:09:00Z",
	}); !errors.Is(err, workordermodel.ErrInvalidTask) {
		t.Fatalf("terminal task append error=%v", err)
	}

	current, err := store.Get(ctx, postgresOrganizationID, postgresSiteID, postgresMutationWorkOrderID)
	if err != nil || current.Version != 9 || len(current.Timeline) != 9 || current.Status != workordermodel.StatusCompleted || current.Tasks.Completed != 2 || len(current.CompletionEvidence) != 1 || current.AssigneeID == nil || *current.AssigneeID != assignee {
		t.Fatalf("authoritative mutation projection=%#v err=%v", current, err)
	}
	var idempotencyCount, auditCount, taskCount, taskTimelineCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM work_order_runtime.work_order_idempotency WHERE response_payload->>'workOrderId' = $1`, postgresMutationWorkOrderID).Scan(&idempotencyCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM work_order_runtime.work_order_mutation_audit WHERE work_order_id = $1`, postgresMutationWorkOrderID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM work_order_runtime.work_order_task WHERE work_order_id = $1`, postgresMutationWorkOrderID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM work_order_runtime.work_order_timeline WHERE work_order_id = $1 AND operation LIKE 'TASK_%'`, postgresMutationWorkOrderID).Scan(&taskTimelineCount); err != nil {
		t.Fatal(err)
	}
	if idempotencyCount != 9 || auditCount != 9 || taskCount != 2 || taskTimelineCount != 5 {
		t.Fatalf("durable mutation evidence idempotency=%d audit=%d tasks=%d taskTimeline=%d", idempotencyCount, auditCount, taskCount, taskTimelineCount)
	}
}
