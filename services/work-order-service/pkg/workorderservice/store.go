package workorderservice

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

var (
	ErrNotFound      = errors.New("work order not found")
	ErrUnavailable   = errors.New("work order store unavailable")
	ErrInvalidCursor = errors.New("work order cursor invalid")
	ErrInvalidFilter = ErrInvalidCursor
)

type Filter struct {
	Status     workordermodel.Status
	Priority   workordermodel.Priority
	AssigneeID string
	Cursor     string
	Limit      int
}

type Store interface {
	List(context.Context, string, string, Filter) (workordermodel.ListResponse, error)
	Get(context.Context, string, string, string) (workordermodel.WorkOrder, error)
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

type MemoryStore struct {
	mu     sync.RWMutex
	items  map[string]workordermodel.WorkOrder
	cursor *cursorCodec
}

type memoryRecord struct {
	workOrder workordermodel.WorkOrder
	updatedAt time.Time
}

func NewMemoryStore(items []workordermodel.WorkOrder) (*MemoryStore, error) {
	codec, err := newCursorCodec([]byte("work-order-memory-store-cursor-key-v1"))
	if err != nil {
		return nil, err
	}
	store := &MemoryStore{items: make(map[string]workordermodel.WorkOrder, len(items)), cursor: codec}
	for _, workOrder := range items {
		if err := workOrder.Validate(); err != nil {
			return nil, err
		}
		if _, exists := store.items[workOrder.WorkOrderID]; exists {
			return nil, errors.New("duplicate Work Order identity")
		}
		store.items[workOrder.WorkOrderID] = cloneWorkOrder(workOrder)
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
	records := make([]memoryRecord, 0, len(store.items))
	for _, workOrder := range store.items {
		if workOrder.OrganizationID != organizationID || workOrder.SiteID != siteID {
			continue
		}
		if !matchesFilter(workOrder, filter) {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, workOrder.UpdatedAt)
		if err != nil {
			return workordermodel.ListResponse{}, ErrUnavailable
		}
		if position != nil && !updatedAt.Before(position.UpdatedAt) && !(updatedAt.Equal(position.UpdatedAt) && workOrder.WorkOrderID > position.WorkOrderID) {
			continue
		}
		records = append(records, memoryRecord{workOrder: cloneWorkOrder(workOrder), updatedAt: updatedAt.UTC()})
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
		response.Items = append(response.Items, cloneWorkOrder(record.workOrder))
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
	workOrder, exists := store.items[workOrderID]
	if !exists || workOrder.OrganizationID != organizationID || workOrder.SiteID != siteID {
		return workordermodel.WorkOrder{}, ErrNotFound
	}
	return cloneWorkOrder(workOrder), nil
}

func cloneWorkOrder(workOrder workordermodel.WorkOrder) workordermodel.WorkOrder {
	result := workOrder
	result.SourceReferences = append([]workordermodel.SourceReference(nil), workOrder.SourceReferences...)
	result.CompletionEvidence = append([]workordermodel.EvidenceReference(nil), workOrder.CompletionEvidence...)
	result.Timeline = append([]workordermodel.TimelineEvent(nil), workOrder.Timeline...)
	if workOrder.AssigneeID != nil {
		value := *workOrder.AssigneeID
		result.AssigneeID = &value
	}
	if workOrder.TeamID != nil {
		value := *workOrder.TeamID
		result.TeamID = &value
	}
	if workOrder.ScheduledStart != nil {
		value := *workOrder.ScheduledStart
		result.ScheduledStart = &value
	}
	if workOrder.DueAt != nil {
		value := *workOrder.DueAt
		result.DueAt = &value
	}
	for index := range result.Timeline {
		if result.Timeline[index].FromStatus != nil {
			value := *result.Timeline[index].FromStatus
			result.Timeline[index].FromStatus = &value
		}
		if result.Timeline[index].PolicyRevision != nil {
			value := *result.Timeline[index].PolicyRevision
			result.Timeline[index].PolicyRevision = &value
		}
		if result.Timeline[index].CorrelationID != nil {
			value := *result.Timeline[index].CorrelationID
			result.Timeline[index].CorrelationID = &value
		}
	}
	return result
}
