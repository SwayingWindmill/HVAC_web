package workordermodel

import (
	"errors"
	"testing"
)

const (
	taskOneID = "01930000-0000-7000-8000-000000000041"
	taskTwoID = "01930000-0000-7000-8000-000000000042"
)

func TestTaskChecklistAppendStatusAndReorderConverge(t *testing.T) {
	workOrder := lifecycleFixture(t)
	var tasks []Task

	workOrder, tasks = appendTask(t, workOrder, tasks, taskOneID, "Inspect fan bearings", "2026-08-02T01:00:00Z")
	workOrder, tasks = appendTask(t, workOrder, tasks, taskTwoID, "Record vibration", "2026-08-02T02:00:00Z")
	if workOrder.Version != 3 || workOrder.Tasks != (TaskSummary{Total: 2}) || tasks[0].Position != 0 || tasks[1].Position != 1 {
		t.Fatalf("append did not converge: workOrder=%#v tasks=%#v", workOrder, tasks)
	}

	updated, updatedTasks, err := ApplyTaskStatus(workOrder, tasks, TaskStatusInput{
		TaskID: taskOneID, ExpectedWorkOrderVersion: 3, ExpectedTaskVersion: 1, Status: TaskStatusBlocked,
		Reason: "bearing unavailable", ActorType: "PRINCIPAL", ActorID: "principal:operator-a",
		PolicyRevision: "policy-10", CorrelationID: "task-status-1", OccurredAt: "2026-08-02T03:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	workOrder, tasks = updated, updatedTasks
	if workOrder.Version != 4 || workOrder.Tasks != (TaskSummary{Total: 2, Blocked: 1}) || tasks[0].Status != TaskStatusBlocked || tasks[0].Version != 2 {
		t.Fatalf("status did not converge: workOrder=%#v tasks=%#v", workOrder, tasks)
	}

	updated, updatedTasks, err = ApplyTaskStatus(workOrder, tasks, TaskStatusInput{
		TaskID: taskOneID, ExpectedWorkOrderVersion: 4, ExpectedTaskVersion: 2, Status: TaskStatusCompleted,
		Reason: "bearing replaced", ActorType: "PRINCIPAL", ActorID: "principal:operator-a",
		PolicyRevision: "policy-10", CorrelationID: "task-status-2", OccurredAt: "2026-08-02T04:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	workOrder, tasks = updated, updatedTasks
	if workOrder.Tasks != (TaskSummary{Total: 2, Completed: 1}) || tasks[0].Status != TaskStatusCompleted || workOrder.Timeline[len(workOrder.Timeline)-1].TaskID == nil {
		t.Fatalf("completed task did not converge: workOrder=%#v tasks=%#v", workOrder, tasks)
	}

	updated, updatedTasks, err = ApplyTaskReorder(workOrder, tasks, ReorderTasksInput{
		ExpectedWorkOrderVersion: 5, TaskIDs: []string{taskTwoID, taskOneID}, Reason: "execute measurement first",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-reorder-1", OccurredAt: "2026-08-02T05:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	workOrder, tasks = updated, updatedTasks
	if workOrder.Version != 6 || tasks[0].TaskID != taskTwoID || tasks[1].TaskID != taskOneID || tasks[0].Position != 0 || tasks[1].Position != 1 {
		t.Fatalf("reorder did not produce contiguous exact order: workOrder=%#v tasks=%#v", workOrder, tasks)
	}
	checklist, err := NewTaskChecklist(workOrder, tasks)
	if err != nil || checklist.Validate(mutationOrganizationID, mutationSiteID, mutationWorkOrderID) != nil || checklist.WorkOrderVersion != 6 {
		t.Fatalf("invalid checklist: checklist=%#v err=%v", checklist, err)
	}
}

func TestTaskMutationsRejectStaleTerminalAndInvalidPermutation(t *testing.T) {
	workOrder := lifecycleFixture(t)
	workOrder, tasks := appendTask(t, workOrder, nil, taskOneID, "Inspect fan bearings", "2026-08-02T01:00:00Z")
	workOrder, tasks = appendTask(t, workOrder, tasks, taskTwoID, "Record vibration", "2026-08-02T02:00:00Z")

	if _, _, err := ApplyTaskStatus(workOrder, tasks, TaskStatusInput{
		TaskID: taskOneID, ExpectedWorkOrderVersion: 2, ExpectedTaskVersion: 1, Status: TaskStatusBlocked,
		Reason: "stale", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-stale", OccurredAt: "2026-08-02T03:00:00Z",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale work order version error=%v", err)
	}
	if _, _, err := ApplyTaskStatus(workOrder, tasks, TaskStatusInput{
		TaskID: taskOneID, ExpectedWorkOrderVersion: 3, ExpectedTaskVersion: 2, Status: TaskStatusBlocked,
		Reason: "stale task", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-stale-task", OccurredAt: "2026-08-02T03:00:00Z",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale task version error=%v", err)
	}
	for name, order := range map[string][]string{
		"missing":   {taskOneID},
		"duplicate": {taskOneID, taskOneID},
		"unknown":   {taskOneID, "01930000-0000-7000-8000-000000000099"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ApplyTaskReorder(workOrder, tasks, ReorderTasksInput{
				ExpectedWorkOrderVersion: 3, TaskIDs: order, Reason: "invalid order", ActorType: "PRINCIPAL",
				ActorID: "principal:operator-a", PolicyRevision: "policy-10", CorrelationID: "task-reorder-invalid",
				OccurredAt: "2026-08-02T03:00:00Z",
			}); !errors.Is(err, ErrInvalidTask) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	terminal := cloneWorkOrder(workOrder)
	terminal.Status = StatusCancelled
	terminal.Version++
	terminal.UpdatedAt = "2026-08-02T03:00:00Z"
	from := StatusOpen
	terminal.Timeline = append(terminal.Timeline, TimelineEvent{
		Operation: OperationCancel, FromStatus: &from, ToStatus: StatusCancelled, Reason: "cancel",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", OccurredAt: terminal.UpdatedAt, Version: terminal.Version,
	})
	if err := terminal.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyTaskAppend(terminal, tasks, AppendTaskInput{
		TaskID: "01930000-0000-7000-8000-000000000043", ExpectedWorkOrderVersion: terminal.Version, Title: "Forbidden",
		Reason: "terminal", ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-terminal", OccurredAt: "2026-08-02T04:00:00Z",
	}); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("terminal task append error=%v", err)
	}
}

func appendTask(t *testing.T, workOrder WorkOrder, tasks []Task, taskID, title, occurredAt string) (WorkOrder, []Task) {
	t.Helper()
	updated, updatedTasks, err := ApplyTaskAppend(workOrder, tasks, AppendTaskInput{
		TaskID: taskID, ExpectedWorkOrderVersion: workOrder.Version, Title: title, Reason: "append task",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-a", PolicyRevision: "policy-10",
		CorrelationID: "task-append-" + taskID, OccurredAt: occurredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated, updatedTasks
}
