package workordermodel

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = 1

const MaximumScheduleHorizon = 365 * 24 * time.Hour

var (
	uuidV7Pattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ErrVersionConflict   = errors.New("work order version conflict")
	ErrInvalidCreate     = errors.New("work order create request is invalid")
	ErrInvalidAssignment = errors.New("work order assignment request is invalid")
	ErrInvalidLifecycle  = errors.New("work order lifecycle request is invalid")
)

type Priority string

const (
	PriorityLow    Priority = "LOW"
	PriorityMedium Priority = "MEDIUM"
	PriorityHigh   Priority = "HIGH"
	PriorityUrgent Priority = "URGENT"
)

type Status string

const (
	StatusDraft      Status = "DRAFT"
	StatusOpen       Status = "OPEN"
	StatusInProgress Status = "IN_PROGRESS"
	StatusBlocked    Status = "BLOCKED"
	StatusCompleted  Status = "COMPLETED"
	StatusCancelled  Status = "CANCELLED"
)

type Operation string

const (
	OperationCreate   Operation = "CREATE"
	OperationOpen     Operation = "OPEN"
	OperationAssign   Operation = "ASSIGN"
	OperationUnassign Operation = "UNASSIGN"
	OperationSchedule Operation = "SCHEDULE"
	OperationStart    Operation = "START"
	OperationBlock    Operation = "BLOCK"
	OperationResume   Operation = "RESUME"
	OperationComplete Operation = "COMPLETE"
	OperationCancel   Operation = "CANCEL"
	OperationReopen   Operation = "REOPEN"
)

type SourceDomain string

const (
	SourceManual        SourceDomain = "MANUAL"
	SourceAlarm         SourceDomain = "ALARM"
	SourceAsset         SourceDomain = "ASSET"
	SourceEquipment     SourceDomain = "EQUIPMENT"
	SourceInvestigation SourceDomain = "INVESTIGATION"
	SourceExternal      SourceDomain = "EXTERNAL"
)

type SourceRelationship string

const (
	RelationshipOrigin  SourceRelationship = "ORIGIN"
	RelationshipRelated SourceRelationship = "RELATED"
)

type SourceReference struct {
	Domain       SourceDomain       `json:"domain"`
	ResourceID   string             `json:"resourceId"`
	Relationship SourceRelationship `json:"relationship"`
}

type EvidenceReference struct {
	Kind       string `json:"kind"`
	Reference  string `json:"reference"`
	CapturedAt string `json:"capturedAt"`
}

type TaskSummary struct {
	Total     uint64 `json:"total"`
	Completed uint64 `json:"completed"`
	Blocked   uint64 `json:"blocked"`
}

type TimelineEvent struct {
	Operation      Operation `json:"operation"`
	FromStatus     *Status   `json:"fromStatus,omitempty"`
	ToStatus       Status    `json:"toStatus"`
	Reason         string    `json:"reason"`
	ActorType      string    `json:"actorType"`
	ActorID        string    `json:"actorId"`
	AssigneeID     *string   `json:"assigneeId,omitempty"`
	TeamID         *string   `json:"teamId,omitempty"`
	PolicyRevision *string   `json:"policyRevision,omitempty"`
	CorrelationID  *string   `json:"correlationId,omitempty"`
	OccurredAt     string    `json:"occurredAt"`
	Version        uint64    `json:"version"`
}

type WorkOrder struct {
	SchemaVersion      int                 `json:"schemaVersion"`
	WorkOrderID        string              `json:"workOrderId"`
	TenantID     string              `json:"tenantId"`
	SiteID             string              `json:"siteId"`
	Title              string              `json:"title"`
	Description        string              `json:"description"`
	Priority           Priority            `json:"priority"`
	Status             Status              `json:"status"`
	SourceReferences   []SourceReference   `json:"sourceReferences"`
	AssigneeID         *string             `json:"assigneeId,omitempty"`
	TeamID             *string             `json:"teamId,omitempty"`
	ScheduledStart     *string             `json:"scheduledStart,omitempty"`
	DueAt              *string             `json:"dueAt,omitempty"`
	Tasks              TaskSummary         `json:"tasks"`
	NoteCount          uint64              `json:"noteCount"`
	AttachmentCount    uint64              `json:"attachmentCount"`
	CompletionEvidence []EvidenceReference `json:"completionEvidence"`
	Timeline           []TimelineEvent     `json:"timeline"`
	Version            uint64              `json:"version"`
	CreatedAt          string              `json:"createdAt"`
	UpdatedAt          string              `json:"updatedAt"`
}

type ListResponse struct {
	SchemaVersion int         `json:"schemaVersion"`
	Items         []WorkOrder `json:"items"`
	NextCursor    *string     `json:"nextCursor"`
	HasMore       bool        `json:"hasMore"`
}

type CreateInput struct {
	WorkOrderID      string
	TenantID   string
	SiteID           string
	Title            string
	Description      string
	Priority         Priority
	SourceReferences []SourceReference
	AssigneeID       *string
	TeamID           *string
	ScheduledStart   *string
	DueAt            *string
	ActorType        string
	ActorID          string
	PolicyRevision   string
	CorrelationID    string
	OccurredAt       string
}

type AssignmentInput struct {
	ExpectedVersion uint64
	AssigneeID      *string
	TeamID          *string
	Reason          string
	ActorType       string
	ActorID         string
	PolicyRevision  string
	CorrelationID   string
	OccurredAt      string
}

type LifecycleInput struct {
	Operation          Operation
	ExpectedVersion    uint64
	ScheduledStart     *string
	DueAt              *string
	CompletionEvidence []EvidenceReference
	Reason             string
	ActorType          string
	ActorID            string
	PolicyRevision     string
	CorrelationID      string
	OccurredAt         string
}

func IsUUIDv7(value string) bool { return uuidV7Pattern.MatchString(value) }

func Create(input CreateInput) (WorkOrder, error) {
	occurredAt, err := parseInstant(input.OccurredAt)
	if err != nil || !IsUUIDv7(input.WorkOrderID) || !IsUUIDv7(input.TenantID) || !IsUUIDv7(input.SiteID) {
		return WorkOrder{}, ErrInvalidCreate
	}
	assigneeID, ok := normalizeOptional(input.AssigneeID, 256)
	if !ok {
		return WorkOrder{}, ErrInvalidCreate
	}
	teamID, ok := normalizeOptional(input.TeamID, 256)
	if !ok {
		return WorkOrder{}, ErrInvalidCreate
	}
	scheduledStart, ok := normalizeOptionalInstant(input.ScheduledStart)
	if !ok {
		return WorkOrder{}, ErrInvalidCreate
	}
	dueAt, ok := normalizeOptionalInstant(input.DueAt)
	if !ok || validateMutationSchedule(scheduledStart, dueAt, occurredAt) != nil {
		return WorkOrder{}, ErrInvalidCreate
	}
	references := append([]SourceReference(nil), input.SourceReferences...)
	for index := range references {
		references[index].ResourceID = strings.TrimSpace(references[index].ResourceID)
	}
	if len(references) != 1 || references[0].Relationship != RelationshipOrigin {
		return WorkOrder{}, ErrInvalidCreate
	}
	if !bounded(input.Title, 256) || !bounded(input.Description, 4096) || !validPriority(input.Priority) || validateSources(references) != nil ||
		!bounded(input.ActorType, 64) || !bounded(input.ActorID, 256) || !bounded(input.PolicyRevision, 128) || !bounded(input.CorrelationID, 256) {
		return WorkOrder{}, ErrInvalidCreate
	}
	instant := occurredAt.UTC().Format(time.RFC3339Nano)
	policyRevision := strings.TrimSpace(input.PolicyRevision)
	correlationID := strings.TrimSpace(input.CorrelationID)
	workOrder := WorkOrder{
		SchemaVersion: SchemaVersion,
		WorkOrderID:   strings.TrimSpace(input.WorkOrderID), TenantID: strings.TrimSpace(input.TenantID), SiteID: strings.TrimSpace(input.SiteID),
		Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), Priority: input.Priority, Status: StatusOpen,
		SourceReferences: references, AssigneeID: assigneeID, TeamID: teamID, ScheduledStart: scheduledStart, DueAt: dueAt,
		Tasks: TaskSummary{}, CompletionEvidence: []EvidenceReference{},
		Timeline: []TimelineEvent{{
			Operation: OperationCreate, ToStatus: StatusOpen, Reason: "WORK_ORDER_CREATED",
			ActorType: strings.TrimSpace(input.ActorType), ActorID: strings.TrimSpace(input.ActorID),
			AssigneeID: cloneOptional(assigneeID), TeamID: cloneOptional(teamID), PolicyRevision: &policyRevision, CorrelationID: &correlationID,
			OccurredAt: instant, Version: 1,
		}},
		Version: 1, CreatedAt: instant, UpdatedAt: instant,
	}
	if err := workOrder.Validate(); err != nil {
		return WorkOrder{}, ErrInvalidCreate
	}
	return workOrder, nil
}

func ApplyAssignment(workOrder WorkOrder, input AssignmentInput) (WorkOrder, error) {
	if err := workOrder.Validate(); err != nil {
		return WorkOrder{}, err
	}
	if input.ExpectedVersion != workOrder.Version {
		return WorkOrder{}, ErrVersionConflict
	}
	if workOrder.Status == StatusCompleted || workOrder.Status == StatusCancelled || !bounded(input.Reason, 256) ||
		!bounded(input.ActorType, 64) || !bounded(input.ActorID, 256) || !bounded(input.PolicyRevision, 128) || !bounded(input.CorrelationID, 256) {
		return WorkOrder{}, ErrInvalidAssignment
	}
	occurredAt, err := parseInstant(input.OccurredAt)
	updatedAt, updatedErr := parseInstant(workOrder.UpdatedAt)
	if err != nil || updatedErr != nil || occurredAt.Before(updatedAt) {
		return WorkOrder{}, ErrInvalidAssignment
	}
	assigneeID, ok := normalizeOptional(input.AssigneeID, 256)
	if !ok {
		return WorkOrder{}, ErrInvalidAssignment
	}
	teamID, ok := normalizeOptional(input.TeamID, 256)
	if !ok {
		return WorkOrder{}, ErrInvalidAssignment
	}
	operation := OperationAssign
	if assigneeID == nil && teamID == nil {
		operation = OperationUnassign
	}
	result := cloneWorkOrder(workOrder)
	result.AssigneeID = assigneeID
	result.TeamID = teamID
	result.Version++
	result.UpdatedAt = occurredAt.UTC().Format(time.RFC3339Nano)
	fromStatus := result.Status
	policyRevision := strings.TrimSpace(input.PolicyRevision)
	correlationID := strings.TrimSpace(input.CorrelationID)
	result.Timeline = append(result.Timeline, TimelineEvent{
		Operation: operation, FromStatus: &fromStatus, ToStatus: result.Status,
		Reason: strings.TrimSpace(input.Reason), ActorType: strings.TrimSpace(input.ActorType), ActorID: strings.TrimSpace(input.ActorID),
		AssigneeID: cloneOptional(assigneeID), TeamID: cloneOptional(teamID), PolicyRevision: &policyRevision, CorrelationID: &correlationID,
		OccurredAt: result.UpdatedAt, Version: result.Version,
	})
	if err := result.Validate(); err != nil {
		return WorkOrder{}, ErrInvalidAssignment
	}
	return result, nil
}

func ApplyLifecycle(workOrder WorkOrder, input LifecycleInput) (WorkOrder, error) {
	if err := workOrder.Validate(); err != nil {
		return WorkOrder{}, err
	}
	if input.ExpectedVersion != workOrder.Version {
		return WorkOrder{}, ErrVersionConflict
	}
	if !bounded(input.Reason, 256) || !bounded(input.ActorType, 64) || !bounded(input.ActorID, 256) ||
		!bounded(input.PolicyRevision, 128) || !bounded(input.CorrelationID, 256) {
		return WorkOrder{}, ErrInvalidLifecycle
	}
	occurredAt, err := parseInstant(input.OccurredAt)
	updatedAt, updatedErr := parseInstant(workOrder.UpdatedAt)
	if err != nil || updatedErr != nil || occurredAt.Before(updatedAt) {
		return WorkOrder{}, ErrInvalidLifecycle
	}
	result := cloneWorkOrder(workOrder)
	fromStatus := result.Status
	toStatus := fromStatus
	switch input.Operation {
	case OperationSchedule:
		if fromStatus != StatusOpen || len(input.CompletionEvidence) != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		scheduledStart, ok := normalizeOptionalInstant(input.ScheduledStart)
		if !ok {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		dueAt, ok := normalizeOptionalInstant(input.DueAt)
		if !ok || (scheduledStart == nil && dueAt == nil) || validateLifecycleSchedule(scheduledStart, dueAt, occurredAt) != nil {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		result.ScheduledStart = scheduledStart
		result.DueAt = dueAt
	case OperationStart:
		if fromStatus != StatusOpen || (result.AssigneeID == nil && result.TeamID == nil) || input.ScheduledStart != nil || input.DueAt != nil || len(input.CompletionEvidence) != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		toStatus = StatusInProgress
	case OperationBlock:
		if fromStatus != StatusInProgress || input.ScheduledStart != nil || input.DueAt != nil || len(input.CompletionEvidence) != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		toStatus = StatusBlocked
	case OperationResume:
		if fromStatus != StatusBlocked || input.ScheduledStart != nil || input.DueAt != nil || len(input.CompletionEvidence) != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		toStatus = StatusInProgress
	case OperationComplete:
		if fromStatus != StatusInProgress || input.ScheduledStart != nil || input.DueAt != nil || len(input.CompletionEvidence) == 0 ||
			result.Tasks.Completed != result.Tasks.Total || result.Tasks.Blocked != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		evidence, err := normalizeLifecycleEvidence(result.CompletionEvidence, input.CompletionEvidence, occurredAt)
		if err != nil {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		result.CompletionEvidence = evidence
		toStatus = StatusCompleted
	case OperationCancel:
		if (fromStatus != StatusOpen && fromStatus != StatusInProgress && fromStatus != StatusBlocked) || input.ScheduledStart != nil || input.DueAt != nil || len(input.CompletionEvidence) != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		toStatus = StatusCancelled
	case OperationReopen:
		if (fromStatus != StatusCompleted && fromStatus != StatusCancelled) || input.ScheduledStart != nil || input.DueAt != nil || len(input.CompletionEvidence) != 0 {
			return WorkOrder{}, ErrInvalidLifecycle
		}
		toStatus = StatusOpen
	default:
		return WorkOrder{}, ErrInvalidLifecycle
	}
	result.Status = toStatus
	result.Version++
	result.UpdatedAt = occurredAt.UTC().Format(time.RFC3339Nano)
	policyRevision := strings.TrimSpace(input.PolicyRevision)
	correlationID := strings.TrimSpace(input.CorrelationID)
	result.Timeline = append(result.Timeline, TimelineEvent{
		Operation: input.Operation, FromStatus: &fromStatus, ToStatus: toStatus,
		Reason: strings.TrimSpace(input.Reason), ActorType: strings.TrimSpace(input.ActorType), ActorID: strings.TrimSpace(input.ActorID),
		PolicyRevision: &policyRevision, CorrelationID: &correlationID, OccurredAt: result.UpdatedAt, Version: result.Version,
	})
	if err := result.Validate(); err != nil {
		return WorkOrder{}, ErrInvalidLifecycle
	}
	return result, nil
}

func normalizeLifecycleEvidence(existing, added []EvidenceReference, occurredAt time.Time) ([]EvidenceReference, error) {
	result := append([]EvidenceReference(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(added))
	for _, reference := range existing {
		seen[reference.Kind+"\x00"+reference.Reference] = struct{}{}
	}
	for _, reference := range added {
		reference.Kind = strings.TrimSpace(reference.Kind)
		reference.Reference = strings.TrimSpace(reference.Reference)
		capturedAt, err := parseInstant(strings.TrimSpace(reference.CapturedAt))
		if err != nil || capturedAt.After(occurredAt) {
			return nil, ErrInvalidLifecycle
		}
		reference.CapturedAt = capturedAt.UTC().Format(time.RFC3339Nano)
		key := reference.Kind + "\x00" + reference.Reference
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrInvalidLifecycle
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	if validateEvidence(result) != nil {
		return nil, ErrInvalidLifecycle
	}
	return result, nil
}

func (workOrder WorkOrder) Validate() error {
	if workOrder.SchemaVersion != SchemaVersion || !IsUUIDv7(workOrder.WorkOrderID) || !IsUUIDv7(workOrder.TenantID) || !IsUUIDv7(workOrder.SiteID) {
		return errors.New("work order identity is invalid")
	}
	if !bounded(workOrder.Title, 256) || !bounded(workOrder.Description, 4096) || !validPriority(workOrder.Priority) || !validStatus(workOrder.Status) || workOrder.Version == 0 {
		return errors.New("work order projection is invalid")
	}
	if workOrder.AssigneeID != nil && !bounded(*workOrder.AssigneeID, 256) {
		return errors.New("work order assignee is invalid")
	}
	if workOrder.TeamID != nil && !bounded(*workOrder.TeamID, 256) {
		return errors.New("work order team is invalid")
	}
	createdAt, err := parseInstant(workOrder.CreatedAt)
	if err != nil {
		return errors.New("work order creation instant is invalid")
	}
	updatedAt, err := parseInstant(workOrder.UpdatedAt)
	if err != nil || updatedAt.Before(createdAt) {
		return errors.New("work order update instant is invalid")
	}
	if err := validateSchedule(workOrder.ScheduledStart, workOrder.DueAt); err != nil {
		return err
	}
	if workOrder.Tasks.Completed > workOrder.Tasks.Total || workOrder.Tasks.Blocked > workOrder.Tasks.Total-workOrder.Tasks.Completed {
		return errors.New("work order task summary is invalid")
	}
	if err := validateSources(workOrder.SourceReferences); err != nil {
		return err
	}
	if err := validateEvidence(workOrder.CompletionEvidence); err != nil {
		return err
	}
	if workOrder.Status == StatusCompleted && (len(workOrder.CompletionEvidence) == 0 || workOrder.Tasks.Completed != workOrder.Tasks.Total || workOrder.Tasks.Blocked != 0) {
		return errors.New("completed work order requires completion evidence and converged tasks")
	}
	if err := validateTimeline(workOrder.Timeline, workOrder.Status, workOrder.Version, workOrder.AssigneeID, workOrder.TeamID, createdAt, updatedAt); err != nil {
		return err
	}
	return nil
}

func (response ListResponse) Validate(tenantID, siteID string, limit int) error {
	if response.SchemaVersion != SchemaVersion || !IsUUIDv7(tenantID) || !IsUUIDv7(siteID) || limit < 1 || limit > 100 || len(response.Items) > limit {
		return errors.New("work order list envelope is invalid")
	}
	seen := make(map[string]struct{}, len(response.Items))
	for _, workOrder := range response.Items {
		if err := workOrder.Validate(); err != nil || workOrder.TenantID != tenantID || workOrder.SiteID != siteID {
			return errors.New("work order list contains an invalid or cross-scope item")
		}
		if _, exists := seen[workOrder.WorkOrderID]; exists {
			return errors.New("work order list contains duplicate identity")
		}
		seen[workOrder.WorkOrderID] = struct{}{}
	}
	if response.HasMore != (response.NextCursor != nil) || (response.NextCursor != nil && !bounded(*response.NextCursor, 512)) {
		return errors.New("work order list cursor state is invalid")
	}
	return nil
}

func validateSources(references []SourceReference) error {
	if len(references) == 0 || len(references) > 64 {
		return errors.New("work order source references are invalid")
	}
	seen := make(map[string]struct{}, len(references))
	originCount := 0
	for _, reference := range references {
		if !validSourceDomain(reference.Domain) || !validRelationship(reference.Relationship) || !bounded(reference.ResourceID, 512) {
			return errors.New("work order source reference is invalid")
		}
		if reference.Domain != SourceManual && reference.Domain != SourceExternal && !IsUUIDv7(reference.ResourceID) {
			return errors.New("work order authoritative source identity is invalid")
		}
		key := string(reference.Domain) + "\x00" + reference.ResourceID
		if _, exists := seen[key]; exists {
			return errors.New("work order source reference is duplicated")
		}
		seen[key] = struct{}{}
		if reference.Relationship == RelationshipOrigin {
			originCount++
		}
	}
	if originCount != 1 {
		return errors.New("work order requires exactly one origin source")
	}
	return nil
}

func validateEvidence(references []EvidenceReference) error {
	if len(references) > 256 {
		return errors.New("work order completion evidence is too large")
	}
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if !bounded(reference.Kind, 128) || !bounded(reference.Reference, 512) {
			return errors.New("work order completion evidence is invalid")
		}
		if _, err := parseInstant(reference.CapturedAt); err != nil {
			return errors.New("work order completion evidence instant is invalid")
		}
		key := reference.Kind + "\x00" + reference.Reference
		if _, exists := seen[key]; exists {
			return errors.New("work order completion evidence is duplicated")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateTimeline(events []TimelineEvent, currentStatus Status, version uint64, assigneeID, teamID *string, createdAt, updatedAt time.Time) error {
	if len(events) == 0 || len(events) > 512 || uint64(len(events)) != version {
		return errors.New("work order timeline is invalid")
	}
	var previousStatus Status
	var previousAt time.Time
	var projectedAssigneeID *string
	var projectedTeamID *string
	for index, event := range events {
		if !validOperation(event.Operation) || !validStatus(event.ToStatus) || !bounded(event.Reason, 256) || !bounded(event.ActorType, 64) || !bounded(event.ActorID, 256) || event.Version != uint64(index+1) {
			return errors.New("work order timeline event is invalid")
		}
		if event.AssigneeID != nil && !bounded(*event.AssigneeID, 256) {
			return errors.New("work order timeline assignee is invalid")
		}
		if event.TeamID != nil && !bounded(*event.TeamID, 256) {
			return errors.New("work order timeline team is invalid")
		}
		if event.PolicyRevision != nil && !bounded(*event.PolicyRevision, 128) {
			return errors.New("work order timeline policy revision is invalid")
		}
		if event.CorrelationID != nil && !bounded(*event.CorrelationID, 256) {
			return errors.New("work order timeline correlation is invalid")
		}
		occurredAt, err := parseInstant(event.OccurredAt)
		if err != nil || occurredAt.Before(createdAt) || occurredAt.After(updatedAt) || (index > 0 && occurredAt.Before(previousAt)) {
			return errors.New("work order timeline instant is invalid")
		}
		if index == 0 {
			if event.Operation != OperationCreate || event.FromStatus != nil || event.ToStatus != StatusOpen {
				return errors.New("work order timeline origin is invalid")
			}
			projectedAssigneeID = cloneOptional(event.AssigneeID)
			projectedTeamID = cloneOptional(event.TeamID)
		} else {
			if event.FromStatus == nil || *event.FromStatus != previousStatus || event.Operation == OperationCreate {
				return errors.New("work order timeline continuity is invalid")
			}
			switch event.Operation {
			case OperationAssign:
				if event.ToStatus != *event.FromStatus || event.ToStatus == StatusCompleted || event.ToStatus == StatusCancelled || (event.AssigneeID == nil && event.TeamID == nil) {
					return errors.New("work order assignment timeline is invalid")
				}
				projectedAssigneeID = cloneOptional(event.AssigneeID)
				projectedTeamID = cloneOptional(event.TeamID)
			case OperationUnassign:
				if event.ToStatus != *event.FromStatus || event.ToStatus == StatusCompleted || event.ToStatus == StatusCancelled || event.AssigneeID != nil || event.TeamID != nil {
					return errors.New("work order unassignment timeline is invalid")
				}
				projectedAssigneeID = nil
				projectedTeamID = nil
			default:
				if event.AssigneeID != nil || event.TeamID != nil || !validLifecycleTransition(event.Operation, *event.FromStatus, event.ToStatus) {
					return errors.New("work order lifecycle timeline is invalid")
				}
			}
		}
		previousStatus = event.ToStatus
		previousAt = occurredAt
	}
	if previousStatus != currentStatus || !sameOptional(projectedAssigneeID, assigneeID) || !sameOptional(projectedTeamID, teamID) {
		return errors.New("work order timeline does not converge with projection")
	}
	return nil
}

func validLifecycleTransition(operation Operation, fromStatus, toStatus Status) bool {
	switch operation {
	case OperationOpen:
		return fromStatus == StatusDraft && toStatus == StatusOpen
	case OperationSchedule:
		return fromStatus == StatusOpen && toStatus == StatusOpen
	case OperationStart:
		return fromStatus == StatusOpen && toStatus == StatusInProgress
	case OperationBlock:
		return fromStatus == StatusInProgress && toStatus == StatusBlocked
	case OperationResume:
		return fromStatus == StatusBlocked && toStatus == StatusInProgress
	case OperationComplete:
		return fromStatus == StatusInProgress && toStatus == StatusCompleted
	case OperationCancel:
		return (fromStatus == StatusOpen || fromStatus == StatusInProgress || fromStatus == StatusBlocked) && toStatus == StatusCancelled
	case OperationReopen:
		return (fromStatus == StatusCompleted || fromStatus == StatusCancelled) && toStatus == StatusOpen
	default:
		return false
	}
}

func validateSchedule(scheduledStart, dueAt *string) error {
	var scheduled time.Time
	if scheduledStart != nil {
		parsed, err := parseInstant(*scheduledStart)
		if err != nil {
			return errors.New("work order scheduled start is invalid")
		}
		scheduled = parsed
	}
	if dueAt != nil {
		parsed, err := parseInstant(*dueAt)
		if err != nil || (scheduledStart != nil && parsed.Before(scheduled)) {
			return errors.New("work order due instant is invalid")
		}
	}
	return nil
}

func validateMutationSchedule(scheduledStart, dueAt *string, now time.Time) error {
	if validateSchedule(scheduledStart, dueAt) != nil {
		return ErrInvalidCreate
	}
	for _, value := range []*string{scheduledStart, dueAt} {
		if value == nil {
			continue
		}
		instant, _ := parseInstant(*value)
		if instant.Before(now) || instant.After(now.Add(MaximumScheduleHorizon)) {
			return ErrInvalidCreate
		}
	}
	return nil
}

func validateLifecycleSchedule(scheduledStart, dueAt *string, now time.Time) error {
	if validateMutationSchedule(scheduledStart, dueAt, now) != nil {
		return ErrInvalidLifecycle
	}
	return nil
}

func normalizeOptional(value *string, maximum int) (*string, bool) {
	if value == nil {
		return nil, true
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" || len(trimmed) > maximum {
		return nil, false
	}
	return &trimmed, true
}

func normalizeOptionalInstant(value *string) (*string, bool) {
	if value == nil {
		return nil, true
	}
	instant, err := parseInstant(strings.TrimSpace(*value))
	if err != nil {
		return nil, false
	}
	normalized := instant.UTC().Format(time.RFC3339Nano)
	return &normalized, true
}

func cloneOptional(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sameOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneWorkOrder(workOrder WorkOrder) WorkOrder {
	result := workOrder
	result.SourceReferences = append([]SourceReference(nil), workOrder.SourceReferences...)
	result.CompletionEvidence = append([]EvidenceReference(nil), workOrder.CompletionEvidence...)
	result.Timeline = append([]TimelineEvent(nil), workOrder.Timeline...)
	result.AssigneeID = cloneOptional(workOrder.AssigneeID)
	result.TeamID = cloneOptional(workOrder.TeamID)
	result.ScheduledStart = cloneOptional(workOrder.ScheduledStart)
	result.DueAt = cloneOptional(workOrder.DueAt)
	for index := range result.Timeline {
		result.Timeline[index].FromStatus = cloneStatus(result.Timeline[index].FromStatus)
		result.Timeline[index].AssigneeID = cloneOptional(result.Timeline[index].AssigneeID)
		result.Timeline[index].TeamID = cloneOptional(result.Timeline[index].TeamID)
		result.Timeline[index].PolicyRevision = cloneOptional(result.Timeline[index].PolicyRevision)
		result.Timeline[index].CorrelationID = cloneOptional(result.Timeline[index].CorrelationID)
	}
	return result
}

func cloneStatus(value *Status) *Status {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func parseInstant(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

func bounded(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= maximum
}

func validPriority(value Priority) bool {
	switch value {
	case PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent:
		return true
	default:
		return false
	}
}

func validStatus(value Status) bool {
	switch value {
	case StatusDraft, StatusOpen, StatusInProgress, StatusBlocked, StatusCompleted, StatusCancelled:
		return true
	default:
		return false
	}
}

func validOperation(value Operation) bool {
	switch value {
	case OperationCreate, OperationOpen, OperationAssign, OperationUnassign, OperationSchedule, OperationStart, OperationBlock, OperationResume, OperationComplete, OperationCancel, OperationReopen:
		return true
	default:
		return false
	}
}

func validSourceDomain(value SourceDomain) bool {
	switch value {
	case SourceManual, SourceAlarm, SourceAsset, SourceEquipment, SourceInvestigation, SourceExternal:
		return true
	default:
		return false
	}
}

func validRelationship(value SourceRelationship) bool {
	return value == RelationshipOrigin || value == RelationshipRelated
}
