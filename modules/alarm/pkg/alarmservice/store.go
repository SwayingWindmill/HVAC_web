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
	Condition    alarmmodel.Condition
	Severity     alarmmodel.Severity
	Acknowledged *bool
	Suppressed   *bool
	Cursor       string
	Limit        int
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

type Publication struct {
	DeviceID        *string
	EventID         *string
	PointID         *string
	AlarmType       string
	SourceType      alarmmodel.SourceType
	SourceReference string
	RuleRevision    string
	Title           string
	Summary         string
	Severity        alarmmodel.Severity
	OccurredAt      string
	Evidence        []alarmmodel.EvidenceReference
	ActorType       string
	ActorID         string
	CorrelationID   string
}

type Recovery struct {
	Fingerprint           string
	IncidentCorrelationID string
	OccurredAt            string
	Reason                string
	Evidence              []alarmmodel.EvidenceReference
	RuleRevision          string
	ActorType             string
	ActorID               string
	CorrelationID         string
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
	Publish(context.Context, string, string, Publication) (alarmmodel.Alarm, error)
	ClearActive(context.Context, string, string, Recovery) (alarmmodel.Alarm, error)
}

type idempotencyRecord struct {
	digest string
	alarm  alarmmodel.Alarm
}

type idGenerator func(time.Time) (string, error)

type MemoryStore struct {
	mu          sync.RWMutex
	alarms      map[string]alarmmodel.Alarm
	active      map[string]string
	idempotency map[string]idempotencyRecord
	newID       idGenerator
}

func NewMemoryStore(items []alarmmodel.Alarm) (*MemoryStore, error) {
	return newMemoryStore(items, newUUIDv7)
}

func newMemoryStore(items []alarmmodel.Alarm, generator idGenerator) (*MemoryStore, error) {
	store := &MemoryStore{
		alarms: make(map[string]alarmmodel.Alarm, len(items)), active: make(map[string]string),
		idempotency: make(map[string]idempotencyRecord), newID: generator,
	}
	for _, alarm := range items {
		if err := alarm.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := store.alarms[alarm.AlarmID]; duplicate {
			return nil, errors.New("duplicate alarm identity")
		}
		if alarm.Condition == alarmmodel.ConditionActive {
			key := activeKey(alarm.TenantID, alarm.SiteID, alarm.Fingerprint)
			if _, duplicate := store.active[key]; duplicate {
				return nil, errors.New("duplicate active alarm fingerprint")
			}
			store.active[key] = alarm.AlarmID
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
		if alarm.TenantID != tenantID || alarm.SiteID != siteID || !matchesFilter(alarm, filter) {
			continue
		}
		if filter.Cursor != "" {
			triggeredAt, _ := time.Parse(time.RFC3339Nano, alarm.FirstOccurredAt)
			if triggeredAt.After(cursorTime) || (triggeredAt.Equal(cursorTime) && alarm.AlarmID >= cursorAlarmID) {
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

func (store *MemoryStore) Publish(_ context.Context, tenantID, siteID string, publication Publication) (alarmmodel.Alarm, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	fingerprint, err := alarmmodel.Fingerprint(tenantID, siteID, publication.SourceType, publication.SourceReference, publication.AlarmType, publication.DeviceID, publication.PointID)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	key := activeKey(tenantID, siteID, fingerprint)
	if alarmID, exists := store.active[key]; exists {
		current := store.alarms[alarmID]
		updated, err := alarmmodel.RecordOccurrence(current, alarmmodel.OccurrenceInput{
			Severity: publication.Severity, OccurredAt: publication.OccurredAt, Evidence: publication.Evidence,
			RuleRevision: publication.RuleRevision, ActorType: publication.ActorType, ActorID: publication.ActorID,
			CorrelationID: publication.CorrelationID,
		})
		if err != nil {
			return alarmmodel.Alarm{}, err
		}
		store.alarms[alarmID] = cloneStoredAlarm(updated)
		return cloneStoredAlarm(updated), nil
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, publication.OccurredAt)
	if err != nil {
		return alarmmodel.Alarm{}, alarmmodel.ErrInvalidOperation
	}
	alarmID, err := store.newID(occurredAt)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	incidentCorrelationID, err := store.newID(occurredAt.Add(time.Nanosecond))
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	incident, err := alarmmodel.NewIncident(alarmmodel.IncidentInput{
		AlarmID: alarmID, TenantID: tenantID, SiteID: siteID, DeviceID: publication.DeviceID, EventID: publication.EventID,
		PointID: publication.PointID, AlarmType: publication.AlarmType, IncidentCorrelationID: incidentCorrelationID,
		SourceType: publication.SourceType, SourceReference: publication.SourceReference, RuleRevision: publication.RuleRevision,
		Title: publication.Title, Summary: publication.Summary, Severity: publication.Severity, OccurredAt: publication.OccurredAt,
		Evidence: publication.Evidence, ActorType: publication.ActorType, ActorID: publication.ActorID,
	})
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	store.alarms[incident.AlarmID] = cloneStoredAlarm(incident)
	store.active[key] = incident.AlarmID
	return cloneStoredAlarm(incident), nil
}

func (store *MemoryStore) ClearActive(_ context.Context, tenantID, siteID string, recovery Recovery) (alarmmodel.Alarm, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := activeKey(tenantID, siteID, recovery.Fingerprint)
	alarmID, exists := store.active[key]
	if !exists {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	current := store.alarms[alarmID]
	if current.IncidentCorrelationID != recovery.IncidentCorrelationID {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	cleared, err := alarmmodel.ClearIncident(current, alarmmodel.ClearInput{
		OccurredAt: recovery.OccurredAt, Reason: recovery.Reason, Evidence: recovery.Evidence, RuleRevision: recovery.RuleRevision,
		ActorType: recovery.ActorType, ActorID: recovery.ActorID, CorrelationID: recovery.CorrelationID,
	})
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	store.alarms[alarmID] = cloneStoredAlarm(cleared)
	delete(store.active, key)
	return cloneStoredAlarm(cleared), nil
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

func matchesFilter(alarm alarmmodel.Alarm, filter Filter) bool {
	if filter.Condition != "" && alarm.Condition != filter.Condition || filter.Severity != "" && alarm.CurrentSeverity != filter.Severity {
		return false
	}
	if filter.Acknowledged != nil && (alarm.Acknowledgement != nil) != *filter.Acknowledged {
		return false
	}
	if filter.Suppressed != nil && (alarm.Suppression != nil) != *filter.Suppressed {
		return false
	}
	return true
}

func activeKey(tenantID, siteID, fingerprint string) string {
	return tenantID + "|" + siteID + "|" + fingerprint
}

func (mutation Mutation) operationInput() alarmmodel.OperationInput {
	return alarmmodel.OperationInput{
		Operation: mutation.Operation, ExpectedVersion: mutation.ExpectedVersion, Reason: mutation.Reason,
		AssigneeID: mutation.AssigneeID, SuppressedUntil: mutation.SuppressedUntil, ActorType: mutation.ActorType,
		ActorID: mutation.ActorID, PolicyRevision: mutation.PolicyRevision, CorrelationID: mutation.CorrelationID,
		OccurredAt: mutation.OccurredAt,
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
		Operation: mutation.Operation, ExpectedVersion: mutation.ExpectedVersion, Reason: strings.TrimSpace(mutation.Reason),
		AssigneeID: mutation.AssigneeID, SuppressedUntil: mutation.SuppressedUntil, ActorType: strings.TrimSpace(mutation.ActorType),
		ActorID: strings.TrimSpace(mutation.ActorID), PolicyRevision: strings.TrimSpace(mutation.PolicyRevision), CorrelationID: strings.TrimSpace(mutation.CorrelationID),
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
	acknowledged, suppressed := "", ""
	if filter.Acknowledged != nil {
		acknowledged = boolString(*filter.Acknowledged)
	}
	if filter.Suppressed != nil {
		suppressed = boolString(*filter.Suppressed)
	}
	payload := strings.Join([]string{tenantID, siteID, string(filter.Condition), string(filter.Severity), acknowledged, suppressed}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func encodeAlarmCursor(tenantID, siteID string, filter Filter, alarm alarmmodel.Alarm) (string, error) {
	cursor := alarmCursor{Version: 2, TriggeredAt: alarm.FirstOccurredAt, AlarmID: alarm.AlarmID, Fingerprint: alarmFilterFingerprint(tenantID, siteID, filter)}
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
	if decoder.Decode(&cursor) != nil || cursor.Version != 2 || !alarmmodel.IsUUIDv7(cursor.AlarmID) || cursor.Fingerprint != alarmFilterFingerprint(tenantID, siteID, filter) {
		return time.Time{}, "", ErrInvalidCursor
	}
	triggeredAt, err := time.Parse(time.RFC3339Nano, cursor.TriggeredAt)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	return triggeredAt, cursor.AlarmID, nil
}

func cloneStoredAlarm(alarm alarmmodel.Alarm) alarmmodel.Alarm {
	encoded, _ := json.Marshal(alarm)
	var result alarmmodel.Alarm
	_ = json.Unmarshal(encoded, &result)
	return result
}
