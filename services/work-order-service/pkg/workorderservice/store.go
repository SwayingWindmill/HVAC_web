package workorderservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const lifecycleIdempotencyOperation = "LIFECYCLE"

var (
	ErrNotFound            = errors.New("work order not found")
	ErrUnavailable         = errors.New("work order store unavailable")
	ErrInvalidCursor       = errors.New("work order cursor is invalid")
	ErrInvalidFilter       = errors.New("work order filter is invalid")
	ErrIdempotencyConflict = errors.New("work order idempotency conflict")
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

type Filter struct {
	Status     workordermodel.Status
	Priority   workordermodel.Priority
	AssigneeID string
	Cursor     string
	Limit      int
}

type CreateMutation struct {
	WorkOrderID      string
	Title            string
	Description      string
	Priority         workordermodel.Priority
	SourceReferences []workordermodel.SourceReference
	AssigneeID       *string
	TeamID           *string
	ScheduledStart   *string
	DueAt            *string
	ActorType        string
	ActorID          string
	PolicyRevision   string
	CorrelationID    string
	IdempotencyKey   string
	OccurredAt       string
}

type AssignmentMutation struct {
	ExpectedVersion uint64
	AssigneeID      *string
	TeamID          *string
	Reason          string
	ActorType       string
	ActorID         string
	PolicyRevision  string
	CorrelationID   string
	IdempotencyKey  string
	OccurredAt      string
}

type LifecycleMutation struct {
	Operation          workordermodel.Operation
	ExpectedVersion    uint64
	ScheduledStart     *string
	DueAt              *string
	CompletionEvidence []workordermodel.EvidenceReference
	Reason             string
	ActorType          string
	ActorID            string
	PolicyRevision     string
	CorrelationID      string
	IdempotencyKey     string
	OccurredAt         string
}

type MutationResult struct {
	WorkOrder workordermodel.WorkOrder
	Replayed  bool
}

type Store interface {
	List(context.Context, string, string, Filter) (workordermodel.ListResponse, error)
	Get(context.Context, string, string, string) (workordermodel.WorkOrder, error)
	Create(context.Context, string, string, CreateMutation) (MutationResult, error)
	Assign(context.Context, string, string, string, AssignmentMutation) (MutationResult, error)
	Transition(context.Context, string, string, string, LifecycleMutation) (MutationResult, error)
}

type idempotencyRecord struct {
	digest    string
	workOrder workordermodel.WorkOrder
}

type MemoryStore struct {
	mu          sync.RWMutex
	items       map[string]workordermodel.WorkOrder
	cursor      *cursorCodec
	idempotency map[string]idempotencyRecord
}

func NewMemoryStore(items []workordermodel.WorkOrder) (*MemoryStore, error) {
	codec, err := newCursorCodec([]byte("work-order-memory-store-cursor-key-v1"))
	if err != nil {
		return nil, err
	}
	store := &MemoryStore{
		items:       make(map[string]workordermodel.WorkOrder, len(items)),
		cursor:      codec,
		idempotency: make(map[string]idempotencyRecord),
	}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := store.items[item.WorkOrderID]; duplicate {
			return nil, errors.New("duplicate work order identity")
		}
		store.items[item.WorkOrderID] = cloneStoredWorkOrder(item)
	}
	return store, nil
}

func (store *MemoryStore) List(_ context.Context, organizationID, siteID string, filter Filter) (workordermodel.ListResponse, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	filter = normalizeFilter(filter)
	if !validStatusFilter(filter.Status) || !validPriorityFilter(filter.Priority) || len(filter.AssigneeID) > 256 {
		return workordermodel.ListResponse{}, ErrInvalidFilter
	}
	var position *cursorPosition
	if filter.Cursor != "" {
		decoded, err := store.cursor.Decode(filter.Cursor, organizationID, siteID, filter)
		if err != nil {
			return workordermodel.ListResponse{}, ErrInvalidFilter
		}
		position = &decoded
	}
	type memoryRecord struct {
		workOrder workordermodel.WorkOrder
		updatedAt time.Time
	}
	records := make([]memoryRecord, 0, len(store.items))
	for _, workOrder := range store.items {
		if workOrder.OrganizationID != organizationID || workOrder.SiteID != siteID || !matchesFilter(workOrder, filter) {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, workOrder.UpdatedAt)
		if err != nil {
			return workordermodel.ListResponse{}, ErrUnavailable
		}
		if position != nil && !updatedAt.Before(position.UpdatedAt) && !(updatedAt.Equal(position.UpdatedAt) && workOrder.WorkOrderID > position.WorkOrderID) {
			continue
		}
		records = append(records, memoryRecord{workOrder: cloneStoredWorkOrder(workOrder), updatedAt: updatedAt.UTC()})
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].updatedAt.Equal(records[right].updatedAt) {
			return records[left].workOrder.WorkOrderID < records[right].workOrder.WorkOrderID
		}
		return records[left].updatedAt.After(records[right].updatedAt)
	})
	hasMore := len(records) > filter.Limit
	if hasMore {
		records = records[:filter.Limit]
	}
	response := workordermodel.ListResponse{SchemaVersion: workordermodel.SchemaVersion, HasMore: hasMore}
	response.Items = make([]workordermodel.WorkOrder, 0, len(records))
	for _, record := range records {
		response.Items = append(response.Items, cloneStoredWorkOrder(record.workOrder))
	}
	if hasMore {
		last := records[len(records)-1]
		cursor, err := store.cursor.Encode(organizationID, siteID, filter, last.updatedAt, last.workOrder.WorkOrderID)
		if err != nil {
			return workordermodel.ListResponse{}, ErrUnavailable
		}
		response.NextCursor = &cursor
	}
	if err := response.Validate(organizationID, siteID, filter.Limit); err != nil {
		return workordermodel.ListResponse{}, ErrUnavailable
	}
	return response, nil
}

func (store *MemoryStore) Get(_ context.Context, organizationID, siteID, workOrderID string) (workordermodel.WorkOrder, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, ok := store.items[workOrderID]
	if !ok || item.OrganizationID != organizationID || item.SiteID != siteID {
		return workordermodel.WorkOrder{}, ErrNotFound
	}
	return cloneStoredWorkOrder(item), nil
}

func (store *MemoryStore) Create(_ context.Context, organizationID, siteID string, mutation CreateMutation) (MutationResult, error) {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(mutation.IdempotencyKey)) {
		return MutationResult{}, workordermodel.ErrInvalidCreate
	}
	digest, err := createMutationDigest(mutation)
	if err != nil {
		return MutationResult{}, workordermodel.ErrInvalidCreate
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	key := organizationID + "|" + siteID + "|CREATE|" + strings.TrimSpace(mutation.IdempotencyKey)
	if record, exists := store.idempotency[key]; exists {
		if record.digest != digest {
			return MutationResult{}, ErrIdempotencyConflict
		}
		return MutationResult{WorkOrder: cloneStoredWorkOrder(record.workOrder), Replayed: true}, nil
	}
	created, err := workordermodel.Create(mutation.createInput(organizationID, siteID))
	if err != nil {
		return MutationResult{}, err
	}
	if _, duplicate := store.items[created.WorkOrderID]; duplicate {
		return MutationResult{}, ErrUnavailable
	}
	store.items[created.WorkOrderID] = cloneStoredWorkOrder(created)
	store.idempotency[key] = idempotencyRecord{digest: digest, workOrder: cloneStoredWorkOrder(created)}
	return MutationResult{WorkOrder: cloneStoredWorkOrder(created)}, nil
}

func (store *MemoryStore) Assign(_ context.Context, organizationID, siteID, workOrderID string, mutation AssignmentMutation) (MutationResult, error) {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(mutation.IdempotencyKey)) {
		return MutationResult{}, workordermodel.ErrInvalidAssignment
	}
	digest, err := assignmentMutationDigest(mutation)
	if err != nil {
		return MutationResult{}, workordermodel.ErrInvalidAssignment
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.items[workOrderID]
	if !ok || current.OrganizationID != organizationID || current.SiteID != siteID {
		return MutationResult{}, ErrNotFound
	}
	key := organizationID + "|" + siteID + "|" + workOrderID + "|ASSIGN|" + strings.TrimSpace(mutation.IdempotencyKey)
	if record, exists := store.idempotency[key]; exists {
		if record.digest != digest {
			return MutationResult{}, ErrIdempotencyConflict
		}
		return MutationResult{WorkOrder: cloneStoredWorkOrder(record.workOrder), Replayed: true}, nil
	}
	updated, err := workordermodel.ApplyAssignment(current, mutation.assignmentInput())
	if err != nil {
		return MutationResult{}, err
	}
	store.items[workOrderID] = cloneStoredWorkOrder(updated)
	store.idempotency[key] = idempotencyRecord{digest: digest, workOrder: cloneStoredWorkOrder(updated)}
	return MutationResult{WorkOrder: cloneStoredWorkOrder(updated)}, nil
}

func (store *MemoryStore) Transition(_ context.Context, organizationID, siteID, workOrderID string, mutation LifecycleMutation) (MutationResult, error) {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(mutation.IdempotencyKey)) {
		return MutationResult{}, workordermodel.ErrInvalidLifecycle
	}
	digest, err := lifecycleMutationDigest(mutation)
	if err != nil {
		return MutationResult{}, workordermodel.ErrInvalidLifecycle
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.items[workOrderID]
	if !ok || current.OrganizationID != organizationID || current.SiteID != siteID {
		return MutationResult{}, ErrNotFound
	}
	key := organizationID + "|" + siteID + "|" + workOrderID + "|" + lifecycleIdempotencyOperation + "|" + strings.TrimSpace(mutation.IdempotencyKey)
	if record, exists := store.idempotency[key]; exists {
		if record.digest != digest {
			return MutationResult{}, ErrIdempotencyConflict
		}
		return MutationResult{WorkOrder: cloneStoredWorkOrder(record.workOrder), Replayed: true}, nil
	}
	updated, err := workordermodel.ApplyLifecycle(current, mutation.lifecycleInput())
	if err != nil {
		return MutationResult{}, err
	}
	store.items[workOrderID] = cloneStoredWorkOrder(updated)
	store.idempotency[key] = idempotencyRecord{digest: digest, workOrder: cloneStoredWorkOrder(updated)}
	return MutationResult{WorkOrder: cloneStoredWorkOrder(updated)}, nil
}

func (mutation CreateMutation) createInput(organizationID, siteID string) workordermodel.CreateInput {
	return workordermodel.CreateInput{
		WorkOrderID: mutation.WorkOrderID, OrganizationID: organizationID, SiteID: siteID,
		Title: mutation.Title, Description: mutation.Description, Priority: mutation.Priority,
		SourceReferences: mutation.SourceReferences, AssigneeID: mutation.AssigneeID, TeamID: mutation.TeamID,
		ScheduledStart: mutation.ScheduledStart, DueAt: mutation.DueAt,
		ActorType: mutation.ActorType, ActorID: mutation.ActorID, PolicyRevision: mutation.PolicyRevision,
		CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
	}
}

func (mutation AssignmentMutation) assignmentInput() workordermodel.AssignmentInput {
	return workordermodel.AssignmentInput{
		ExpectedVersion: mutation.ExpectedVersion, AssigneeID: mutation.AssigneeID, TeamID: mutation.TeamID,
		Reason: mutation.Reason, ActorType: mutation.ActorType, ActorID: mutation.ActorID,
		PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
	}
}

func (mutation LifecycleMutation) lifecycleInput() workordermodel.LifecycleInput {
	return workordermodel.LifecycleInput{
		Operation: mutation.Operation, ExpectedVersion: mutation.ExpectedVersion,
		ScheduledStart: mutation.ScheduledStart, DueAt: mutation.DueAt, CompletionEvidence: mutation.CompletionEvidence,
		Reason: mutation.Reason, ActorType: mutation.ActorType, ActorID: mutation.ActorID,
		PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID, OccurredAt: mutation.OccurredAt,
	}
}

func createMutationDigest(mutation CreateMutation) (string, error) {
	payload := struct {
		Operation        string                           `json:"operation"`
		Title            string                           `json:"title"`
		Description      string                           `json:"description"`
		Priority         workordermodel.Priority          `json:"priority"`
		SourceReferences []workordermodel.SourceReference `json:"sourceReferences"`
		AssigneeID       *string                          `json:"assigneeId"`
		TeamID           *string                          `json:"teamId"`
		ScheduledStart   *string                          `json:"scheduledStart"`
		DueAt            *string                          `json:"dueAt"`
		ActorType        string                           `json:"actorType"`
		ActorID          string                           `json:"actorId"`
	}{
		Operation: "CREATE", Title: strings.TrimSpace(mutation.Title), Description: strings.TrimSpace(mutation.Description), Priority: mutation.Priority,
		SourceReferences: normalizedSources(mutation.SourceReferences), AssigneeID: trimmedOptional(mutation.AssigneeID), TeamID: trimmedOptional(mutation.TeamID),
		ScheduledStart: trimmedOptional(mutation.ScheduledStart), DueAt: trimmedOptional(mutation.DueAt),
		ActorType: strings.TrimSpace(mutation.ActorType), ActorID: strings.TrimSpace(mutation.ActorID),
	}
	return digestJSON(payload)
}

func assignmentMutationDigest(mutation AssignmentMutation) (string, error) {
	payload := struct {
		Operation       string  `json:"operation"`
		ExpectedVersion uint64  `json:"expectedVersion"`
		AssigneeID      *string `json:"assigneeId"`
		TeamID          *string `json:"teamId"`
		Reason          string  `json:"reason"`
		ActorType       string  `json:"actorType"`
		ActorID         string  `json:"actorId"`
	}{
		Operation: "ASSIGN", ExpectedVersion: mutation.ExpectedVersion,
		AssigneeID: trimmedOptional(mutation.AssigneeID), TeamID: trimmedOptional(mutation.TeamID), Reason: strings.TrimSpace(mutation.Reason),
		ActorType: strings.TrimSpace(mutation.ActorType), ActorID: strings.TrimSpace(mutation.ActorID),
	}
	return digestJSON(payload)
}

func lifecycleMutationDigest(mutation LifecycleMutation) (string, error) {
	payload := struct {
		Operation          workordermodel.Operation           `json:"operation"`
		ExpectedVersion    uint64                             `json:"expectedVersion"`
		ScheduledStart     *string                            `json:"scheduledStart"`
		DueAt              *string                            `json:"dueAt"`
		CompletionEvidence []workordermodel.EvidenceReference `json:"completionEvidence"`
		Reason             string                             `json:"reason"`
		ActorType          string                             `json:"actorType"`
		ActorID            string                             `json:"actorId"`
	}{
		Operation: mutation.Operation, ExpectedVersion: mutation.ExpectedVersion,
		ScheduledStart: trimmedOptional(mutation.ScheduledStart), DueAt: trimmedOptional(mutation.DueAt),
		CompletionEvidence: normalizedEvidence(mutation.CompletionEvidence), Reason: strings.TrimSpace(mutation.Reason),
		ActorType: strings.TrimSpace(mutation.ActorType), ActorID: strings.TrimSpace(mutation.ActorID),
	}
	return digestJSON(payload)
}

func normalizedEvidence(values []workordermodel.EvidenceReference) []workordermodel.EvidenceReference {
	result := append([]workordermodel.EvidenceReference(nil), values...)
	for index := range result {
		result[index].Kind = strings.TrimSpace(result[index].Kind)
		result[index].Reference = strings.TrimSpace(result[index].Reference)
		result[index].CapturedAt = strings.TrimSpace(result[index].CapturedAt)
	}
	return result
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func normalizedSources(values []workordermodel.SourceReference) []workordermodel.SourceReference {
	result := append([]workordermodel.SourceReference(nil), values...)
	for index := range result {
		result[index].ResourceID = strings.TrimSpace(result[index].ResourceID)
	}
	return result
}

func trimmedOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func normalizeFilter(filter Filter) Filter {
	filter.AssigneeID = strings.TrimSpace(filter.AssigneeID)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 50
	}
	return filter
}

func validStatusFilter(status workordermodel.Status) bool {
	switch status {
	case "", workordermodel.StatusDraft, workordermodel.StatusOpen, workordermodel.StatusInProgress, workordermodel.StatusBlocked, workordermodel.StatusCompleted, workordermodel.StatusCancelled:
		return true
	default:
		return false
	}
}

func validPriorityFilter(priority workordermodel.Priority) bool {
	switch priority {
	case "", workordermodel.PriorityLow, workordermodel.PriorityMedium, workordermodel.PriorityHigh, workordermodel.PriorityUrgent:
		return true
	default:
		return false
	}
}

func matchesFilter(workOrder workordermodel.WorkOrder, filter Filter) bool {
	filter = normalizeFilter(filter)
	if filter.Status != "" && workOrder.Status != filter.Status {
		return false
	}
	if filter.Priority != "" && workOrder.Priority != filter.Priority {
		return false
	}
	if filter.AssigneeID != "" && (workOrder.AssigneeID == nil || *workOrder.AssigneeID != filter.AssigneeID) {
		return false
	}
	return true
}

func cloneStoredWorkOrder(item workordermodel.WorkOrder) workordermodel.WorkOrder {
	result := item
	result.SourceReferences = append([]workordermodel.SourceReference(nil), item.SourceReferences...)
	result.CompletionEvidence = append([]workordermodel.EvidenceReference(nil), item.CompletionEvidence...)
	result.Timeline = append([]workordermodel.TimelineEvent(nil), item.Timeline...)
	result.AssigneeID = cloneString(item.AssigneeID)
	result.TeamID = cloneString(item.TeamID)
	result.ScheduledStart = cloneString(item.ScheduledStart)
	result.DueAt = cloneString(item.DueAt)
	for index := range result.Timeline {
		result.Timeline[index].FromStatus = cloneStatus(result.Timeline[index].FromStatus)
		result.Timeline[index].AssigneeID = cloneString(result.Timeline[index].AssigneeID)
		result.Timeline[index].TeamID = cloneString(result.Timeline[index].TeamID)
		result.Timeline[index].PolicyRevision = cloneString(result.Timeline[index].PolicyRevision)
		result.Timeline[index].CorrelationID = cloneString(result.Timeline[index].CorrelationID)
	}
	return result
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStatus(value *workordermodel.Status) *workordermodel.Status {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func contains[T comparable](values []T, expected T) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
