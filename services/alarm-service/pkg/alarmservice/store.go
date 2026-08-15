package alarmservice

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

var (
	ErrNotFound            = errors.New("alarm not found")
	ErrUnavailable         = errors.New("alarm store unavailable")
	ErrInvalidCursor       = errors.New("alarm cursor invalid")
	ErrIdempotencyConflict = errors.New("alarm idempotency conflict")
)

type Filter struct {
	Status   alarmmodel.Status
	Severity alarmmodel.Severity
	Cursor   string
	Limit    int
}

type Mutation struct {
	Operation       alarmmodel.Operation
	ExpectedVersion uint64
	Reason          string
	AssigneeID      *string
	SuppressedUntil *string
	ActorType       string
	ActorID         string
	PolicyRevision  string
	CorrelationID   string
	IdempotencyKey  string
	OccurredAt      string
}

type MutationResult struct {
	Alarm    alarmmodel.Alarm
	Replayed bool
}

type AlarmScope struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
}

type Store interface {
	List(context.Context, string, string, Filter) (alarmmodel.ListResponse, error)
	Get(context.Context, string, string, string) (alarmmodel.Alarm, error)
	ResolveScope(context.Context, string, string) (AlarmScope, error)
	Apply(context.Context, string, string, string, Mutation) (MutationResult, error)
}

type idempotencyRecord struct {
	digest string
	alarm  alarmmodel.Alarm
}

type MemoryStore struct {
	mu          sync.RWMutex
	alarms      map[string]alarmmodel.Alarm
	idempotency map[string]idempotencyRecord
}

func NewMemoryStore(items []alarmmodel.Alarm) (*MemoryStore, error) {
	store := &MemoryStore{
		alarms:      make(map[string]alarmmodel.Alarm, len(items)),
		idempotency: make(map[string]idempotencyRecord),
	}
	for _, alarm := range items {
		if err := alarm.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := store.alarms[alarm.AlarmID]; duplicate {
			return nil, errors.New("duplicate alarm identity")
		}
		store.alarms[alarm.AlarmID] = cloneStoredAlarm(alarm)
	}
	return store, nil
}

func (store *MemoryStore) List(_ context.Context, tenantID, siteID string, filter Filter) (alarmmodel.ListResponse, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var cursorTime time.Time
	var cursorAlarmID string
	if filter.Cursor != "" {
		var err error
		cursorTime, cursorAlarmID, err = decodeAlarmCursor(tenantID, siteID, filter)
		if err != nil {
			return alarmmodel.ListResponse{}, ErrInvalidCursor
		}
	}
	items := make([]alarmmodel.Alarm, 0, len(store.alarms))
	for _, alarm := range store.alarms {
		if alarm.TenantID != tenantID || alarm.SiteID != siteID {
			continue
		}
		if filter.Status != "" && alarm.Status != filter.Status {
			continue
		}
		if filter.Severity != "" && alarm.Severity != filter.Severity {
			continue
		}
		if filter.Cursor != "" {
			triggeredAt, err := time.Parse(time.RFC3339Nano, alarm.FirstOccurredAt)
			if err != nil || triggeredAt.After(cursorTime) || (triggeredAt.Equal(cursorTime) && alarm.AlarmID >= cursorAlarmID) {
				continue
			}
		}
		items = append(items, cloneStoredAlarm(alarm))
	}
	alarmmodel.SortNewestFirst(items)
	response := alarmmodel.ListResponse{SchemaVersion: alarmmodel.SchemaVersion, Items: items}
	if len(response.Items) > limit {
		response.Items = response.Items[:limit]
		cursor, err := encodeAlarmCursor(tenantID, siteID, filter, response.Items[len(response.Items)-1])
		if err != nil {
			return alarmmodel.ListResponse{}, ErrUnavailable
		}
		response.NextCursor = &cursor
		response.HasMore = true
	}
	return response, nil
}

func (store *MemoryStore) Get(_ context.Context, tenantID, siteID, alarmID string) (alarmmodel.Alarm, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	alarm, ok := store.alarms[alarmID]
	if !ok || alarm.TenantID != tenantID || alarm.SiteID != siteID {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	return cloneStoredAlarm(alarm), nil
}

func (store *MemoryStore) ResolveScope(_ context.Context, tenantID, alarmID string) (AlarmScope, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	alarm, ok := store.alarms[alarmID]
	if !ok || alarm.TenantID != tenantID {
		return AlarmScope{}, ErrNotFound
	}
	return AlarmScope{TenantID: alarm.TenantID, SiteID: alarm.SiteID}, nil
}

func (store *MemoryStore) Apply(_ context.Context, tenantID, siteID, alarmID string, mutation Mutation) (MutationResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.alarms[alarmID]
	if !ok || current.TenantID != tenantID || current.SiteID != siteID {
		return MutationResult{}, ErrNotFound
	}
	key := strings.TrimSpace(mutation.IdempotencyKey)
	if key == "" && mutation.Operation != alarmmodel.OperationAcknowledge {
		return MutationResult{}, alarmmodel.ErrInvalidOperation
	}
	digest, err := mutationDigest(mutation)
	if err != nil {
		return MutationResult{}, alarmmodel.ErrInvalidOperation
	}
	idempotencyKey := tenantID + "|" + siteID + "|" + alarmID + "|" + key
	if key != "" {
		if record, exists := store.idempotency[idempotencyKey]; exists {
			if record.digest != digest {
				return MutationResult{}, ErrIdempotencyConflict
			}
			return MutationResult{Alarm: cloneStoredAlarm(record.alarm), Replayed: true}, nil
		}
	}
	updated, err := alarmmodel.ApplyOperation(current, mutation.operationInput())
	if err != nil {
		return MutationResult{}, err
	}
	store.alarms[alarmID] = cloneStoredAlarm(updated)
	if key != "" {
		store.idempotency[idempotencyKey] = idempotencyRecord{digest: digest, alarm: cloneStoredAlarm(updated)}
	}
	return MutationResult{Alarm: cloneStoredAlarm(updated), Replayed: updated.Version == current.Version}, nil
}

func (mutation Mutation) operationInput() alarmmodel.OperationInput {
	return alarmmodel.OperationInput{
		Operation:       mutation.Operation,
		ExpectedVersion: mutation.ExpectedVersion,
		Reason:          mutation.Reason,
		AssigneeID:      mutation.AssigneeID,
		SuppressedUntil: mutation.SuppressedUntil,
		ActorType:       mutation.ActorType,
		ActorID:         mutation.ActorID,
		PolicyRevision:  mutation.PolicyRevision,
		CorrelationID:   mutation.CorrelationID,
		OccurredAt:      mutation.OccurredAt,
	}
}

func mutationDigest(mutation Mutation) (string, error) {
	payload := struct {
		Operation       alarmmodel.Operation `json:"operation"`
		ExpectedVersion uint64               `json:"expectedVersion"`
		Reason          string               `json:"reason"`
		AssigneeID      *string              `json:"assigneeId,omitempty"`
		SuppressedUntil *string              `json:"suppressedUntil,omitempty"`
		ActorType       string               `json:"actorType"`
		ActorID         string               `json:"actorId"`
		PolicyRevision  string               `json:"policyRevision"`
		CorrelationID   string               `json:"correlationId"`
	}{
		Operation: mutation.Operation, ExpectedVersion: mutation.ExpectedVersion,
		Reason: strings.TrimSpace(mutation.Reason), AssigneeID: mutation.AssigneeID,
		SuppressedUntil: mutation.SuppressedUntil, ActorType: strings.TrimSpace(mutation.ActorType),
		ActorID: strings.TrimSpace(mutation.ActorID), PolicyRevision: strings.TrimSpace(mutation.PolicyRevision),
		CorrelationID: strings.TrimSpace(mutation.CorrelationID),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type alarmCursor struct {
	Version     int    `json:"v"`
	TriggeredAt string `json:"t"`
	AlarmID     string `json:"a"`
	Fingerprint string `json:"f"`
}

func alarmFilterFingerprint(tenantID, siteID string, filter Filter) string {
	payload := tenantID + "\x00" + siteID + "\x00" + string(filter.Status) + "\x00" + string(filter.Severity)
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func encodeAlarmCursor(tenantID, siteID string, filter Filter, alarm alarmmodel.Alarm) (string, error) {
	cursor := alarmCursor{
		Version: 1, TriggeredAt: alarm.FirstOccurredAt, AlarmID: alarm.AlarmID,
		Fingerprint: alarmFilterFingerprint(tenantID, siteID, filter),
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeAlarmCursor(tenantID, siteID string, filter Filter) (time.Time, string, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
	if err != nil || len(encoded) > 2048 {
		return time.Time{}, "", ErrInvalidCursor
	}
	var cursor alarmCursor
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || cursor.Version != 1 || !alarmmodel.IsUUIDv7(cursor.AlarmID) ||
		cursor.Fingerprint != alarmFilterFingerprint(tenantID, siteID, filter) {
		return time.Time{}, "", ErrInvalidCursor
	}
	triggeredAt, err := time.Parse(time.RFC3339Nano, cursor.TriggeredAt)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	return triggeredAt, cursor.AlarmID, nil
}

func cloneStoredAlarm(alarm alarmmodel.Alarm) alarmmodel.Alarm {
	result := alarm
	result.Evidence = append([]alarmmodel.EvidenceReference(nil), alarm.Evidence...)
	result.Transitions = append([]alarmmodel.Transition(nil), alarm.Transitions...)
	if alarm.AssigneeID != nil {
		value := *alarm.AssigneeID
		result.AssigneeID = &value
	}
	if alarm.SuppressedUntil != nil {
		value := *alarm.SuppressedUntil
		result.SuppressedUntil = &value
	}
	return result
}
