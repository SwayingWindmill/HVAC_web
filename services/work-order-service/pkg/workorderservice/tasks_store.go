package workorderservice

import (
	"context"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const taskIdempotencyOperation = "TASK"

type AppendTaskMutation struct {
	TaskID                   string
	ExpectedWorkOrderVersion uint64
	Title                    string
	Reason                   string
	ActorType                string
	ActorID                  string
	PolicyRevision           string
	CorrelationID            string
	IdempotencyKey           string
	OccurredAt               string
}

type TaskStatusMutation struct {
	TaskID                   string
	ExpectedWorkOrderVersion uint64
	ExpectedTaskVersion      uint64
	Status                   workordermodel.TaskStatus
	Reason                   string
	ActorType                string
	ActorID                  string
	PolicyRevision           string
	CorrelationID            string
	IdempotencyKey           string
	OccurredAt               string
}

type ReorderTasksMutation struct {
	ExpectedWorkOrderVersion uint64
	TaskIDs                  []string
	Reason                   string
	ActorType                string
	ActorID                  string
	PolicyRevision           string
	CorrelationID            string
	IdempotencyKey           string
	OccurredAt               string
}

type TaskMutationResult struct {
	Checklist workordermodel.TaskChecklist
	Replayed  bool
}

type TaskStore interface {
	ListTasks(context.Context, string, string, string) (workordermodel.TaskChecklist, error)
	AppendTask(context.Context, string, string, string, AppendTaskMutation) (TaskMutationResult, error)
	SetTaskStatus(context.Context, string, string, string, TaskStatusMutation) (TaskMutationResult, error)
	ReorderTasks(context.Context, string, string, string, ReorderTasksMutation) (TaskMutationResult, error)
}

type taskIdempotencyRecord struct {
	digest    string
	checklist workordermodel.TaskChecklist
}

func NewMemoryStoreWithTasks(items []workordermodel.WorkOrder, tasks map[string][]workordermodel.Task) (*MemoryStore, error) {
	store, err := NewMemoryStore(items)
	if err != nil {
		return nil, err
	}
	for workOrderID, checklistTasks := range tasks {
		workOrder, exists := store.items[workOrderID]
		if !exists {
			return nil, ErrNotFound
		}
		checklist, err := workordermodel.NewTaskChecklist(workOrder, checklistTasks)
		if err != nil {
			return nil, err
		}
		store.tasks[workOrderID] = cloneTasks(checklist.Tasks)
	}
	return store, nil
}

func (store *MemoryStore) ListTasks(_ context.Context, organizationID, siteID, workOrderID string) (workordermodel.TaskChecklist, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	workOrder, ok := store.items[workOrderID]
	if !ok || workOrder.OrganizationID != organizationID || workOrder.SiteID != siteID {
		return workordermodel.TaskChecklist{}, ErrNotFound
	}
	checklist, err := workordermodel.NewTaskChecklist(workOrder, store.tasks[workOrderID])
	if err != nil {
		return workordermodel.TaskChecklist{}, ErrUnavailable
	}
	return cloneTaskChecklist(checklist), nil
}

func (store *MemoryStore) AppendTask(_ context.Context, organizationID, siteID, workOrderID string, mutation AppendTaskMutation) (TaskMutationResult, error) {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(mutation.IdempotencyKey)) {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	digest, err := appendTaskMutationDigest(mutation)
	if err != nil {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.applyTaskMutation(organizationID, siteID, workOrderID, mutation.IdempotencyKey, digest, func(workOrder workordermodel.WorkOrder, tasks []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error) {
		return workordermodel.ApplyTaskAppend(workOrder, tasks, workordermodel.AppendTaskInput{
			TaskID: mutation.TaskID, ExpectedWorkOrderVersion: mutation.ExpectedWorkOrderVersion, Title: mutation.Title,
			Reason: mutation.Reason, ActorType: mutation.ActorType, ActorID: mutation.ActorID,
			PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
		})
	})
}

func (store *MemoryStore) SetTaskStatus(_ context.Context, organizationID, siteID, workOrderID string, mutation TaskStatusMutation) (TaskMutationResult, error) {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(mutation.IdempotencyKey)) {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	digest, err := taskStatusMutationDigest(mutation)
	if err != nil {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.applyTaskMutation(organizationID, siteID, workOrderID, mutation.IdempotencyKey, digest, func(workOrder workordermodel.WorkOrder, tasks []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error) {
		return workordermodel.ApplyTaskStatus(workOrder, tasks, workordermodel.TaskStatusInput{
			TaskID: mutation.TaskID, ExpectedWorkOrderVersion: mutation.ExpectedWorkOrderVersion,
			ExpectedTaskVersion: mutation.ExpectedTaskVersion, Status: mutation.Status, Reason: mutation.Reason,
			ActorType: mutation.ActorType, ActorID: mutation.ActorID, PolicyRevision: mutation.PolicyRevision,
			CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
		})
	})
}

func (store *MemoryStore) ReorderTasks(_ context.Context, organizationID, siteID, workOrderID string, mutation ReorderTasksMutation) (TaskMutationResult, error) {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(mutation.IdempotencyKey)) {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	digest, err := reorderTasksMutationDigest(mutation)
	if err != nil {
		return TaskMutationResult{}, workordermodel.ErrInvalidTask
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.applyTaskMutation(organizationID, siteID, workOrderID, mutation.IdempotencyKey, digest, func(workOrder workordermodel.WorkOrder, tasks []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error) {
		return workordermodel.ApplyTaskReorder(workOrder, tasks, workordermodel.ReorderTasksInput{
			ExpectedWorkOrderVersion: mutation.ExpectedWorkOrderVersion, TaskIDs: append([]string(nil), mutation.TaskIDs...),
			Reason: mutation.Reason, ActorType: mutation.ActorType, ActorID: mutation.ActorID,
			PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
		})
	})
}

func (store *MemoryStore) applyTaskMutation(organizationID, siteID, workOrderID, idempotencyKey, digest string, apply func(workordermodel.WorkOrder, []workordermodel.Task) (workordermodel.WorkOrder, []workordermodel.Task, error)) (TaskMutationResult, error) {
	current, ok := store.items[workOrderID]
	if !ok || current.OrganizationID != organizationID || current.SiteID != siteID {
		return TaskMutationResult{}, ErrNotFound
	}
	key := organizationID + "|" + siteID + "|" + workOrderID + "|" + taskIdempotencyOperation + "|" + strings.TrimSpace(idempotencyKey)
	if record, exists := store.taskIdempotency[key]; exists {
		if record.digest != digest {
			return TaskMutationResult{}, ErrIdempotencyConflict
		}
		return TaskMutationResult{Checklist: cloneTaskChecklist(record.checklist), Replayed: true}, nil
	}
	updated, tasks, err := apply(current, cloneTasks(store.tasks[workOrderID]))
	if err != nil {
		return TaskMutationResult{}, err
	}
	checklist, err := workordermodel.NewTaskChecklist(updated, tasks)
	if err != nil {
		return TaskMutationResult{}, ErrUnavailable
	}
	store.items[workOrderID] = cloneStoredWorkOrder(updated)
	store.tasks[workOrderID] = cloneTasks(tasks)
	store.taskIdempotency[key] = taskIdempotencyRecord{digest: digest, checklist: cloneTaskChecklist(checklist)}
	return TaskMutationResult{Checklist: cloneTaskChecklist(checklist)}, nil
}

func appendTaskMutationDigest(mutation AppendTaskMutation) (string, error) {
	return digestJSON(struct {
		Operation                string `json:"operation"`
		ExpectedWorkOrderVersion uint64 `json:"expectedWorkOrderVersion"`
		Title                    string `json:"title"`
		Reason                   string `json:"reason"`
		ActorType                string `json:"actorType"`
		ActorID                  string `json:"actorId"`
	}{"TASK_APPEND", mutation.ExpectedWorkOrderVersion, strings.TrimSpace(mutation.Title), strings.TrimSpace(mutation.Reason), strings.TrimSpace(mutation.ActorType), strings.TrimSpace(mutation.ActorID)})
}

func taskStatusMutationDigest(mutation TaskStatusMutation) (string, error) {
	return digestJSON(struct {
		Operation                string                    `json:"operation"`
		TaskID                   string                    `json:"taskId"`
		ExpectedWorkOrderVersion uint64                    `json:"expectedWorkOrderVersion"`
		ExpectedTaskVersion      uint64                    `json:"expectedTaskVersion"`
		Status                   workordermodel.TaskStatus `json:"status"`
		Reason                   string                    `json:"reason"`
		ActorType                string                    `json:"actorType"`
		ActorID                  string                    `json:"actorId"`
	}{"TASK_STATUS", strings.TrimSpace(mutation.TaskID), mutation.ExpectedWorkOrderVersion, mutation.ExpectedTaskVersion, mutation.Status, strings.TrimSpace(mutation.Reason), strings.TrimSpace(mutation.ActorType), strings.TrimSpace(mutation.ActorID)})
}

func reorderTasksMutationDigest(mutation ReorderTasksMutation) (string, error) {
	taskIDs := append([]string(nil), mutation.TaskIDs...)
	for index := range taskIDs {
		taskIDs[index] = strings.TrimSpace(taskIDs[index])
	}
	return digestJSON(struct {
		Operation                string   `json:"operation"`
		ExpectedWorkOrderVersion uint64   `json:"expectedWorkOrderVersion"`
		TaskIDs                  []string `json:"taskIds"`
		Reason                   string   `json:"reason"`
		ActorType                string   `json:"actorType"`
		ActorID                  string   `json:"actorId"`
	}{"TASK_REORDER", mutation.ExpectedWorkOrderVersion, taskIDs, strings.TrimSpace(mutation.Reason), strings.TrimSpace(mutation.ActorType), strings.TrimSpace(mutation.ActorID)})
}

func cloneTasks(tasks []workordermodel.Task) []workordermodel.Task {
	return append([]workordermodel.Task(nil), tasks...)
}

func cloneTaskChecklist(checklist workordermodel.TaskChecklist) workordermodel.TaskChecklist {
	checklist.Tasks = cloneTasks(checklist.Tasks)
	return checklist
}
