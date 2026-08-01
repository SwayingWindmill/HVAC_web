package workordermodel

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = 1

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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
	PolicyRevision *string   `json:"policyRevision,omitempty"`
	CorrelationID  *string   `json:"correlationId,omitempty"`
	OccurredAt     string    `json:"occurredAt"`
	Version        uint64    `json:"version"`
}

type WorkOrder struct {
	SchemaVersion      int                 `json:"schemaVersion"`
	WorkOrderID        string              `json:"workOrderId"`
	OrganizationID     string              `json:"organizationId"`
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

func IsUUIDv7(value string) bool { return uuidV7Pattern.MatchString(value) }

func (workOrder WorkOrder) Validate() error {
	if workOrder.SchemaVersion != SchemaVersion || !IsUUIDv7(workOrder.WorkOrderID) || !IsUUIDv7(workOrder.OrganizationID) || !IsUUIDv7(workOrder.SiteID) {
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
	if workOrder.Status == StatusCompleted && len(workOrder.CompletionEvidence) == 0 {
		return errors.New("completed work order requires completion evidence")
	}
	if err := validateTimeline(workOrder.Timeline, workOrder.Status, workOrder.Version, createdAt, updatedAt); err != nil {
		return err
	}
	return nil
}

func (response ListResponse) Validate(organizationID, siteID string, limit int) error {
	if response.SchemaVersion != SchemaVersion || !IsUUIDv7(organizationID) || !IsUUIDv7(siteID) || limit < 1 || limit > 100 || len(response.Items) > limit {
		return errors.New("work order list envelope is invalid")
	}
	seen := make(map[string]struct{}, len(response.Items))
	for _, workOrder := range response.Items {
		if err := workOrder.Validate(); err != nil || workOrder.OrganizationID != organizationID || workOrder.SiteID != siteID {
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

func validateTimeline(events []TimelineEvent, currentStatus Status, version uint64, createdAt, updatedAt time.Time) error {
	if len(events) == 0 || len(events) > 512 || uint64(len(events)) != version {
		return errors.New("work order timeline is invalid")
	}
	var previousStatus Status
	var previousAt time.Time
	for index, event := range events {
		if !validOperation(event.Operation) || !validStatus(event.ToStatus) || !bounded(event.Reason, 256) || !bounded(event.ActorType, 64) || !bounded(event.ActorID, 256) || event.Version != uint64(index+1) {
			return errors.New("work order timeline event is invalid")
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
			if event.Operation != OperationCreate || event.FromStatus != nil {
				return errors.New("work order timeline origin is invalid")
			}
		} else if event.FromStatus == nil || *event.FromStatus != previousStatus || event.Operation == OperationCreate {
			return errors.New("work order timeline continuity is invalid")
		}
		previousStatus = event.ToStatus
		previousAt = occurredAt
	}
	if previousStatus != currentStatus {
		return errors.New("work order timeline does not converge with projection")
	}
	return nil
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
