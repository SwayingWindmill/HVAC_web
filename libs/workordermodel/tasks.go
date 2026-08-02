package workordermodel

import (
	"errors"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskStatusOpen      TaskStatus = "OPEN"
	TaskStatusBlocked   TaskStatus = "BLOCKED"
	TaskStatusCompleted TaskStatus = "COMPLETED"
)

type Task struct {
	TaskID    string     `json:"taskId"`
	Position  uint64     `json:"position"`
	Title     string     `json:"title"`
	Status    TaskStatus `json:"status"`
	Version   uint64     `json:"version"`
	CreatedAt string     `json:"createdAt"`
	UpdatedAt string     `json:"updatedAt"`
}

type TaskChecklist struct {
	SchemaVersion    int         `json:"schemaVersion"`
	OrganizationID   string      `json:"organizationId"`
	SiteID           string      `json:"siteId"`
	WorkOrderID      string      `json:"workOrderId"`
	WorkOrderVersion uint64      `json:"workOrderVersion"`
	Summary          TaskSummary `json:"summary"`
	Tasks            []Task      `json:"tasks"`
}

type AppendTaskInput struct {
	TaskID                   string
	ExpectedWorkOrderVersion uint64
	Title                    string
	Reason                   string
	ActorType                string
	ActorID                  string
	PolicyRevision           string
	CorrelationID            string
	OccurredAt               string
}

type TaskStatusInput struct {
	TaskID                   string
	ExpectedWorkOrderVersion uint64
	ExpectedTaskVersion      uint64
	Status                   TaskStatus
	Reason                   string
	ActorType                string
	ActorID                  string
	PolicyRevision           string
	CorrelationID            string
	OccurredAt               string
}

type ReorderTasksInput struct {
	ExpectedWorkOrderVersion uint64
	TaskIDs                  []string
	Reason                   string
	ActorType                string
	ActorID                  string
	PolicyRevision           string
	CorrelationID            string
	OccurredAt               string
}

func (checklist TaskChecklist) Validate(organizationID, siteID, workOrderID string) error {
	if checklist.SchemaVersion != SchemaVersion || checklist.OrganizationID != organizationID || checklist.SiteID != siteID || checklist.WorkOrderID != workOrderID ||
		!IsUUIDv7(organizationID) || !IsUUIDv7(siteID) || !IsUUIDv7(workOrderID) || checklist.WorkOrderVersion == 0 {
		return errors.New("work order task checklist scope is invalid")
	}
	if err := validateTasks(checklist.Tasks); err != nil {
		return err
	}
	if checklist.Summary != summarizeTasks(checklist.Tasks) {
		return errors.New("work order task checklist summary does not converge")
	}
	return nil
}

func NewTaskChecklist(workOrder WorkOrder, tasks []Task) (TaskChecklist, error) {
	if err := validateTaskAggregate(workOrder, tasks); err != nil {
		return TaskChecklist{}, err
	}
	return TaskChecklist{
		SchemaVersion: SchemaVersion, OrganizationID: workOrder.OrganizationID, SiteID: workOrder.SiteID,
		WorkOrderID: workOrder.WorkOrderID, WorkOrderVersion: workOrder.Version,
		Summary: workOrder.Tasks, Tasks: cloneTasks(tasks),
	}, nil
}

func ApplyTaskAppend(workOrder WorkOrder, tasks []Task, input AppendTaskInput) (WorkOrder, []Task, error) {
	if err := validateTaskMutationBase(workOrder, tasks, input.ExpectedWorkOrderVersion, input.Reason, input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, input.OccurredAt); err != nil {
		return WorkOrder{}, nil, err
	}
	if !IsUUIDv7(input.TaskID) || !bounded(input.Title, 256) {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	for _, task := range tasks {
		if task.TaskID == input.TaskID {
			return WorkOrder{}, nil, ErrInvalidTask
		}
	}
	instant, _ := parseInstant(input.OccurredAt)
	resultTasks := cloneTasks(tasks)
	resultTasks = append(resultTasks, Task{
		TaskID: strings.TrimSpace(input.TaskID), Position: uint64(len(resultTasks)), Title: strings.TrimSpace(input.Title),
		Status: TaskStatusOpen, Version: 1, CreatedAt: instant.UTC().Format(time.RFC3339Nano), UpdatedAt: instant.UTC().Format(time.RFC3339Nano),
	})
	taskID := resultTasks[len(resultTasks)-1].TaskID
	result := appendTaskTimeline(workOrder, OperationTaskAppend, &taskID, input.Reason, input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, instant, resultTasks)
	if err := validateTaskAggregate(result, resultTasks); err != nil {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	return result, resultTasks, nil
}

func ApplyTaskStatus(workOrder WorkOrder, tasks []Task, input TaskStatusInput) (WorkOrder, []Task, error) {
	if err := validateTaskMutationBase(workOrder, tasks, input.ExpectedWorkOrderVersion, input.Reason, input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, input.OccurredAt); err != nil {
		return WorkOrder{}, nil, err
	}
	if !IsUUIDv7(input.TaskID) || input.ExpectedTaskVersion == 0 || !validTaskStatus(input.Status) {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	instant, _ := parseInstant(input.OccurredAt)
	resultTasks := cloneTasks(tasks)
	found := false
	for index := range resultTasks {
		if resultTasks[index].TaskID != input.TaskID {
			continue
		}
		found = true
		if resultTasks[index].Version != input.ExpectedTaskVersion {
			return WorkOrder{}, nil, ErrVersionConflict
		}
		if resultTasks[index].Status == input.Status {
			return WorkOrder{}, nil, ErrInvalidTask
		}
		resultTasks[index].Status = input.Status
		resultTasks[index].Version++
		resultTasks[index].UpdatedAt = instant.UTC().Format(time.RFC3339Nano)
		break
	}
	if !found {
		return WorkOrder{}, nil, ErrTaskNotFound
	}
	taskID := strings.TrimSpace(input.TaskID)
	result := appendTaskTimeline(workOrder, OperationTaskStatus, &taskID, input.Reason, input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, instant, resultTasks)
	if err := validateTaskAggregate(result, resultTasks); err != nil {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	return result, resultTasks, nil
}

func ApplyTaskReorder(workOrder WorkOrder, tasks []Task, input ReorderTasksInput) (WorkOrder, []Task, error) {
	if err := validateTaskMutationBase(workOrder, tasks, input.ExpectedWorkOrderVersion, input.Reason, input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, input.OccurredAt); err != nil {
		return WorkOrder{}, nil, err
	}
	if len(input.TaskIDs) != len(tasks) {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	byID := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		byID[task.TaskID] = task
	}
	seen := make(map[string]struct{}, len(input.TaskIDs))
	resultTasks := make([]Task, len(input.TaskIDs))
	changed := false
	instant, _ := parseInstant(input.OccurredAt)
	for position, rawTaskID := range input.TaskIDs {
		taskID := strings.TrimSpace(rawTaskID)
		task, exists := byID[taskID]
		if !exists || !IsUUIDv7(taskID) {
			return WorkOrder{}, nil, ErrInvalidTask
		}
		if _, duplicate := seen[taskID]; duplicate {
			return WorkOrder{}, nil, ErrInvalidTask
		}
		seen[taskID] = struct{}{}
		if task.Position != uint64(position) {
			changed = true
			task.Position = uint64(position)
			task.Version++
			task.UpdatedAt = instant.UTC().Format(time.RFC3339Nano)
		}
		resultTasks[position] = task
	}
	if !changed {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	result := appendTaskTimeline(workOrder, OperationTaskReorder, nil, input.Reason, input.ActorType, input.ActorID, input.PolicyRevision, input.CorrelationID, instant, resultTasks)
	if err := validateTaskAggregate(result, resultTasks); err != nil {
		return WorkOrder{}, nil, ErrInvalidTask
	}
	return result, resultTasks, nil
}

func validateTaskMutationBase(workOrder WorkOrder, tasks []Task, expectedVersion uint64, reason, actorType, actorID, policyRevision, correlationID, occurredAt string) error {
	if err := validateTaskAggregate(workOrder, tasks); err != nil {
		return ErrInvalidTask
	}
	if expectedVersion != workOrder.Version {
		return ErrVersionConflict
	}
	if workOrder.Status != StatusOpen && workOrder.Status != StatusInProgress && workOrder.Status != StatusBlocked {
		return ErrInvalidTask
	}
	if !bounded(reason, 256) || !bounded(actorType, 64) || !bounded(actorID, 256) || !bounded(policyRevision, 128) || !bounded(correlationID, 256) {
		return ErrInvalidTask
	}
	instant, err := parseInstant(occurredAt)
	updatedAt, updatedErr := parseInstant(workOrder.UpdatedAt)
	if err != nil || updatedErr != nil || instant.Before(updatedAt) {
		return ErrInvalidTask
	}
	return nil
}

func appendTaskTimeline(workOrder WorkOrder, operation Operation, taskID *string, reason, actorType, actorID, policyRevision, correlationID string, occurredAt time.Time, tasks []Task) WorkOrder {
	result := cloneWorkOrder(workOrder)
	result.Tasks = summarizeTasks(tasks)
	result.Version++
	result.UpdatedAt = occurredAt.UTC().Format(time.RFC3339Nano)
	fromStatus := result.Status
	policy := strings.TrimSpace(policyRevision)
	correlation := strings.TrimSpace(correlationID)
	result.Timeline = append(result.Timeline, TimelineEvent{
		Operation: operation, FromStatus: &fromStatus, ToStatus: fromStatus, TaskID: cloneOptional(taskID),
		Reason: strings.TrimSpace(reason), ActorType: strings.TrimSpace(actorType), ActorID: strings.TrimSpace(actorID),
		PolicyRevision: &policy, CorrelationID: &correlation, OccurredAt: result.UpdatedAt, Version: result.Version,
	})
	return result
}

func validateTaskAggregate(workOrder WorkOrder, tasks []Task) error {
	if err := workOrder.Validate(); err != nil {
		return err
	}
	if err := validateTasks(tasks); err != nil {
		return err
	}
	if workOrder.Tasks != summarizeTasks(tasks) {
		return errors.New("work order task projection does not converge")
	}
	return nil
}

func validateTasks(tasks []Task) error {
	if len(tasks) > 512 {
		return errors.New("work order task checklist is too large")
	}
	seen := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if !IsUUIDv7(task.TaskID) || task.Position != uint64(index) || !bounded(task.Title, 256) || !validTaskStatus(task.Status) || task.Version == 0 {
			return errors.New("work order task is invalid")
		}
		createdAt, err := parseInstant(task.CreatedAt)
		updatedAt, updatedErr := parseInstant(task.UpdatedAt)
		if err != nil || updatedErr != nil || updatedAt.Before(createdAt) {
			return errors.New("work order task instant is invalid")
		}
		if _, duplicate := seen[task.TaskID]; duplicate {
			return errors.New("work order task identity is duplicated")
		}
		seen[task.TaskID] = struct{}{}
	}
	return nil
}

func summarizeTasks(tasks []Task) TaskSummary {
	result := TaskSummary{Total: uint64(len(tasks))}
	for _, task := range tasks {
		switch task.Status {
		case TaskStatusCompleted:
			result.Completed++
		case TaskStatusBlocked:
			result.Blocked++
		}
	}
	return result
}

func validTaskStatus(status TaskStatus) bool {
	return status == TaskStatusOpen || status == TaskStatusBlocked || status == TaskStatusCompleted
}

func cloneTasks(tasks []Task) []Task { return append([]Task(nil), tasks...) }
