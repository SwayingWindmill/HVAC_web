package telemetry

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// MemoryRealtimeRepository is a deterministic test fixture. Production wiring uses PostgresRealtimeRepository.
type MemoryRealtimeRepository struct {
	mu               sync.Mutex
	subscriptions    map[string]RealtimeSubscription
	clientIndex      map[string]string
	cursors          map[string]RecoveryCursorRecord
	currentRevisions map[string]int64
	pending          []PendingPublication
	published        map[string]time.Time
	FailSave         error
	FailRead         error
}

func NewMemoryRealtimeRepository() *MemoryRealtimeRepository {
	return &MemoryRealtimeRepository{
		subscriptions:    map[string]RealtimeSubscription{},
		clientIndex:      map[string]string{},
		cursors:          map[string]RecoveryCursorRecord{},
		currentRevisions: map[string]int64{},
		published:        map[string]time.Time{},
	}
}

func (repository *MemoryRealtimeRepository) SaveSubscriptions(_ context.Context, subscriptions []RealtimeSubscription) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailSave != nil {
		return repository.FailSave
	}
	if len(subscriptions) == 0 {
		return ErrSubscriptionConflict
	}
	clientKeys := make(map[string]struct{}, len(subscriptions))
	for _, subscription := range subscriptions {
		if _, exists := repository.subscriptions[subscription.SubscriptionID]; exists {
			current := repository.subscriptions[subscription.SubscriptionID]
			if current.PrincipalID != subscription.PrincipalID || current.DeviceID != subscription.DeviceID || !slices.Equal(current.Keys, subscription.Keys) {
				return ErrSubscriptionConflict
			}
		}
		clientKey := subscription.PrincipalID + "\x00" + subscription.ClientSubscriptionID
		if _, duplicate := clientKeys[clientKey]; duplicate {
			return ErrSubscriptionConflict
		}
		clientKeys[clientKey] = struct{}{}
		if existingID, exists := repository.clientIndex[clientKey]; exists && existingID != subscription.SubscriptionID {
			return ErrSubscriptionConflict
		}
	}
	for _, subscription := range subscriptions {
		copy := cloneSubscription(subscription)
		repository.subscriptions[subscription.SubscriptionID] = copy
		repository.clientIndex[subscription.PrincipalID+"\x00"+subscription.ClientSubscriptionID] = subscription.SubscriptionID
	}
	return nil
}

func (repository *MemoryRealtimeRepository) ActiveSubscription(_ context.Context, subscriptionID string, now time.Time) (RealtimeSubscription, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailRead != nil {
		return RealtimeSubscription{}, repository.FailRead
	}
	subscription, ok := repository.subscriptions[subscriptionID]
	if !ok || subscription.Status != SubscriptionActive || !subscription.ExpiresAt.After(now) {
		return RealtimeSubscription{}, ErrSubscriptionNotFound
	}
	return cloneSubscription(subscription), nil
}

func (repository *MemoryRealtimeRepository) ActiveSubscriptionByChannel(_ context.Context, principalID, channel string, now time.Time) (RealtimeSubscription, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailRead != nil {
		return RealtimeSubscription{}, repository.FailRead
	}
	for _, subscription := range repository.subscriptions {
		if subscription.PrincipalID == principalID && subscription.Channel == channel && subscription.Status == SubscriptionActive && subscription.ExpiresAt.After(now) {
			return cloneSubscription(subscription), nil
		}
	}
	return RealtimeSubscription{}, ErrSubscriptionNotFound
}

func (repository *MemoryRealtimeRepository) ActiveSubscriptionsForDevice(_ context.Context, deviceID string, now time.Time) ([]RealtimeSubscription, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailRead != nil {
		return nil, repository.FailRead
	}
	result := make([]RealtimeSubscription, 0)
	for _, subscription := range repository.subscriptions {
		if subscription.DeviceID == deviceID && subscription.Status == SubscriptionActive && subscription.ExpiresAt.After(now) {
			result = append(result, cloneSubscription(subscription))
		}
	}
	slices.SortFunc(result, func(left, right RealtimeSubscription) int {
		if left.SubscriptionID < right.SubscriptionID {
			return -1
		}
		if left.SubscriptionID > right.SubscriptionID {
			return 1
		}
		return 0
	})
	return result, nil
}

func (repository *MemoryRealtimeRepository) SaveRecoveryCursors(_ context.Context, cursors []RecoveryCursorRecord) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailSave != nil {
		return repository.FailSave
	}
	for _, cursor := range cursors {
		if _, exists := repository.cursors[cursor.CursorSHA256]; exists {
			return ErrSubscriptionConflict
		}
		if subscription, exists := repository.subscriptions[cursor.SubscriptionID]; !exists || subscription.Status != SubscriptionActive {
			return ErrSubscriptionNotFound
		}
	}
	for _, cursor := range cursors {
		repository.cursors[cursor.CursorSHA256] = cursor
	}
	return nil
}

func (repository *MemoryRealtimeRepository) ActiveRecoveryCursor(_ context.Context, cursorSHA256, subscriptionID string, now time.Time) (RecoveryCursorRecord, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailRead != nil {
		return RecoveryCursorRecord{}, repository.FailRead
	}
	cursor, ok := repository.cursors[cursorSHA256]
	if !ok || cursor.SubscriptionID != subscriptionID || cursor.RevokedAt != nil || !cursor.ExpiresAt.After(now) {
		return RecoveryCursorRecord{}, ErrRecoveryCursorRejected
	}
	return cursor, nil
}

func (repository *MemoryRealtimeRepository) CurrentBusinessRevision(_ context.Context, deviceID string) (int64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailRead != nil {
		return 0, repository.FailRead
	}
	revision := repository.currentRevisions[deviceID]
	if revision < 1 {
		return 0, ErrSubscriptionNotFound
	}
	return revision, nil
}

func (repository *MemoryRealtimeRepository) PendingPublications(_ context.Context, limit int, now time.Time) ([]PendingPublication, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailRead != nil {
		return nil, repository.FailRead
	}
	result := make([]PendingPublication, 0, limit)
	for _, publication := range repository.pending {
		if _, done := repository.published[publication.EventID]; done || publication.EvaluatedAt.After(now) {
			continue
		}
		copy := publication
		copy.ChangedKeys = append([]string(nil), publication.ChangedKeys...)
		result = append(result, copy)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (repository *MemoryRealtimeRepository) MarkPublicationPublished(_ context.Context, eventID string, publishedAt time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailSave != nil {
		return repository.FailSave
	}
	found := false
	for _, pending := range repository.pending {
		if pending.EventID == eventID {
			found = true
			break
		}
	}
	if !found {
		return ErrPublicationNotFound
	}
	repository.published[eventID] = publishedAt
	return nil
}

func (repository *MemoryRealtimeRepository) RevokeSubscriptions(_ context.Context, principalID, deviceID string, now time.Time) ([]RealtimeSubscription, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.FailSave != nil {
		return nil, repository.FailSave
	}
	result := make([]RealtimeSubscription, 0)
	for id, subscription := range repository.subscriptions {
		if (principalID == "" || subscription.PrincipalID == principalID) && (deviceID == "" || subscription.DeviceID == deviceID) && subscription.Status == SubscriptionActive {
			copy := cloneSubscription(subscription)
			result = append(result, copy)
			subscription.Status = SubscriptionRevoked
			subscription.RevokedAt = pointerTime(now)
			subscription.UpdatedAt = now
			repository.subscriptions[id] = subscription
		}
	}
	for digest, cursor := range repository.cursors {
		if subscription, ok := repository.subscriptions[cursor.SubscriptionID]; ok && subscription.Status == SubscriptionRevoked && cursor.RevokedAt == nil {
			cursor.RevokedAt = pointerTime(now)
			repository.cursors[digest] = cursor
		}
	}
	return result, nil
}

func (repository *MemoryRealtimeRepository) AddPending(publication PendingPublication) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.pending = append(repository.pending, publication)
	if publication.Revision > repository.currentRevisions[publication.DeviceID] {
		repository.currentRevisions[publication.DeviceID] = publication.Revision
	}
}

func (repository *MemoryRealtimeRepository) SetCurrentRevision(deviceID string, revision int64) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.currentRevisions[deviceID] = revision
}

func (repository *MemoryRealtimeRepository) IsPublished(eventID string) bool {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	_, ok := repository.published[eventID]
	return ok
}

func cloneSubscription(subscription RealtimeSubscription) RealtimeSubscription {
	copy := subscription
	copy.Keys = append([]string(nil), subscription.Keys...)
	if subscription.RevokedAt != nil {
		value := *subscription.RevokedAt
		copy.RevokedAt = &value
	}
	return copy
}

func pointerTime(value time.Time) *time.Time {
	return &value
}

var _ RealtimeRepository = (*MemoryRealtimeRepository)(nil)
var _ = errors.Is
