package alarmservice

import (
	"context"
	"errors"
	"sync"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

var (
	ErrNotFound    = errors.New("alarm not found")
	ErrUnavailable = errors.New("alarm store unavailable")
)

type Filter struct {
	Status   alarmmodel.Status
	Severity alarmmodel.Severity
	Cursor   string
	Limit    int
}

type Store interface {
	List(context.Context, string, string, Filter) (alarmmodel.ListResponse, error)
	Get(context.Context, string, string, string) (alarmmodel.Alarm, error)
}

type MemoryStore struct {
	mu     sync.RWMutex
	alarms map[string]alarmmodel.Alarm
}

func NewMemoryStore(items []alarmmodel.Alarm) (*MemoryStore, error) {
	store := &MemoryStore{alarms: make(map[string]alarmmodel.Alarm, len(items))}
	for _, alarm := range items {
		if err := alarm.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := store.alarms[alarm.AlarmID]; duplicate {
			return nil, errors.New("duplicate alarm identity")
		}
		store.alarms[alarm.AlarmID] = alarm
	}
	return store, nil
}

func (store *MemoryStore) List(_ context.Context, organizationID, siteID string, filter Filter) (alarmmodel.ListResponse, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]alarmmodel.Alarm, 0, len(store.alarms))
	for _, alarm := range store.alarms {
		if alarm.OrganizationID != organizationID || alarm.SiteID != siteID {
			continue
		}
		if filter.Status != "" && alarm.Status != filter.Status {
			continue
		}
		if filter.Severity != "" && alarm.Severity != filter.Severity {
			continue
		}
		items = append(items, alarm)
	}
	alarmmodel.SortNewestFirst(items)
	if filter.Cursor != "" {
		for index := range items {
			if items[index].AlarmID == filter.Cursor {
				items = items[index+1:]
				break
			}
		}
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	response := alarmmodel.ListResponse{SchemaVersion: alarmmodel.SchemaVersion, Items: items}
	if len(response.Items) > limit {
		cursor := response.Items[limit-1].AlarmID
		response.Items = response.Items[:limit]
		response.NextCursor = &cursor
		response.HasMore = true
	}
	return response, nil
}

func (store *MemoryStore) Get(_ context.Context, organizationID, siteID, alarmID string) (alarmmodel.Alarm, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	alarm, ok := store.alarms[alarmID]
	if !ok || alarm.OrganizationID != organizationID || alarm.SiteID != siteID {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	return alarm, nil
}
