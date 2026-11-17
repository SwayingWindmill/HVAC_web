package alarmservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
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
	ActiveFrom   *time.Time
	ActiveTo     *time.Time
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

const (
	AlarmEvaluationsSchemaVersion = 1
	DefaultAlarmEvaluationsLimit  = 25
	MaxAlarmEvaluationsLimit      = 100
)

type AlarmEvaluationRecord struct {
	EvaluatedAt                 string           `json:"evaluatedAt"`
	PolicyRevisionID            string           `json:"policyRevisionId"`
	AssignmentID                string           `json:"assignmentId"`
	InputRevision               string           `json:"inputRevision"`
	Status                      EvaluationStatus `json:"status"`
	Effect                      EvaluationEffect `json:"effect"`
	QualityBlocker              *string          `json:"qualityBlocker,omitempty"`
	Snapshot                    json.RawMessage  `json:"snapshot"`
	ActiveIncidentCorrelationID *string          `json:"activeIncidentCorrelationId,omitempty"`
}

type AlarmEvaluationList struct {
	SchemaVersion int                     `json:"schemaVersion"`
	AlarmID       string                  `json:"alarmId"`
	Items         []AlarmEvaluationRecord `json:"items"`
}

func (list AlarmEvaluationList) Validate(alarmID string, limit int) error {
	if list.SchemaVersion != AlarmEvaluationsSchemaVersion || !alarmmodel.IsUUIDv7(alarmID) || list.AlarmID != alarmID || limit < 1 || limit > MaxAlarmEvaluationsLimit || len(list.Items) > limit {
		return errors.New("alarm evaluation list envelope is invalid")
	}
	for _, item := range list.Items {
		if _, err := time.Parse(time.RFC3339Nano, item.EvaluatedAt); err != nil {
			return errors.New("alarm evaluation timestamp is invalid")
		}
		if !alarmmodel.IsUUIDv7(item.PolicyRevisionID) || !alarmmodel.IsUUIDv7(item.AssignmentID) || strings.TrimSpace(item.InputRevision) == "" {
			return errors.New("alarm evaluation identity is invalid")
		}
		if item.Status != EvaluationMatched && item.Status != EvaluationNotMatched && item.Status != EvaluationIndeterminate {
			return errors.New("alarm evaluation status is invalid")
		}
		if item.Effect != EvaluationEffectNone && item.Effect != EvaluationEffectPublish && item.Effect != EvaluationEffectClear {
			return errors.New("alarm evaluation effect is invalid")
		}
		if item.ActiveIncidentCorrelationID != nil && !alarmmodel.IsUUIDv7(*item.ActiveIncidentCorrelationID) {
			return errors.New("alarm evaluation correlation id is invalid")
		}
		trimmed := bytes.TrimSpace(item.Snapshot)
		if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
			return errors.New("alarm evaluation snapshot is invalid")
		}
	}
	return nil
}

type Store interface {
	List(context.Context, string, string, Filter) (alarmmodel.ListResponse, error)
	Get(context.Context, string, string, string) (alarmmodel.Alarm, error)
	ListAlarmEvaluations(context.Context, string, string, string, int) (AlarmEvaluationList, error)
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

type memoryEvaluationRecord struct {
	tenantID      string
	siteID        string
	fingerprint   string
	activeAlarmID string
	stateVersion  uint64
	evaluatedAt   time.Time
	record        AlarmEvaluationRecord
}

type MemoryStore struct {
	mu          sync.RWMutex
	alarms      map[string]alarmmodel.Alarm
	active      map[string]string
	idempotency map[string]idempotencyRecord
	evaluations []memoryEvaluationRecord
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

func (store *MemoryStore) RecordEvaluation(tenantID, siteID, fingerprint, activeAlarmID string, stateVersion uint64, record AlarmEvaluationRecord) error {
	evaluatedAt, err := time.Parse(time.RFC3339Nano, record.EvaluatedAt)
	if err != nil || !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || stateVersion == 0 {
		return errors.New("alarm evaluation record metadata is invalid")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record.EvaluatedAt = evaluatedAt.UTC().Format(time.RFC3339Nano)
	store.evaluations = append(store.evaluations, memoryEvaluationRecord{
		tenantID:      tenantID,
		siteID:        siteID,
		fingerprint:   fingerprint,
		activeAlarmID: activeAlarmID,
		stateVersion:  stateVersion,
		evaluatedAt:   evaluatedAt.UTC(),
		record:        cloneEvaluationRecord(record),
	})
	return nil
}

func (store *MemoryStore) ListAlarmEvaluations(_ context.Context, tenantID, siteID, alarmID string, limit int) (AlarmEvaluationList, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	limit = normalizeEvaluationLimit(limit)
	alarm, ok := store.alarms[alarmID]
	if !ok || alarm.TenantID != tenantID || alarm.SiteID != siteID {
		return AlarmEvaluationList{}, ErrNotFound
	}
	firstOccurredAt, err := time.Parse(time.RFC3339Nano, alarm.FirstOccurredAt)
	if err != nil {
		return AlarmEvaluationList{}, errors.New("alarm first occurred time is invalid")
	}
	windowStart := firstOccurredAt.Add(-10 * time.Minute)
	var matched []memoryEvaluationRecord
	for _, entry := range store.evaluations {
		if entry.tenantID != tenantID || entry.siteID != siteID {
			continue
		}
		if entry.activeAlarmID == alarmID || (entry.fingerprint == alarm.Fingerprint && !entry.evaluatedAt.Before(windowStart)) {
			matched = append(matched, entry)
		}
	}
	sort.Slice(matched, func(left, right int) bool {
		if !matched[left].evaluatedAt.Equal(matched[right].evaluatedAt) {
			return matched[left].evaluatedAt.After(matched[right].evaluatedAt)
		}
		return matched[left].stateVersion > matched[right].stateVersion
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}
	items := make([]AlarmEvaluationRecord, 0, len(matched))
	for _, entry := range matched {
		items = append(items, cloneEvaluationRecord(entry.record))
	}
	return AlarmEvaluationList{
		SchemaVersion: AlarmEvaluationsSchemaVersion,
		AlarmID:       alarmID,
		Items:         items,
	}, nil
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
	if filter.ActiveTo != nil {
		firstAt, err := time.Parse(time.RFC3339Nano, alarm.FirstOccurredAt)
		if err != nil || firstAt.After(*filter.ActiveTo) {
			return false
		}
	}
	if filter.ActiveFrom != nil && alarm.ClearedAt != nil {
		clearedAt, err := time.Parse(time.RFC3339Nano, *alarm.ClearedAt)
		if err != nil || clearedAt.Before(*filter.ActiveFrom) {
			return false
		}
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
	acknowledged, suppressed, activeFrom, activeTo := "", "", "", ""
	if filter.Acknowledged != nil {
		acknowledged = boolString(*filter.Acknowledged)
	}
	if filter.Suppressed != nil {
		suppressed = boolString(*filter.Suppressed)
	}
	if filter.ActiveFrom != nil {
		activeFrom = filter.ActiveFrom.UTC().Format(time.RFC3339Nano)
	}
	if filter.ActiveTo != nil {
		activeTo = filter.ActiveTo.UTC().Format(time.RFC3339Nano)
	}
	payload := strings.Join([]string{tenantID, siteID, string(filter.Condition), string(filter.Severity), acknowledged, suppressed, activeFrom, activeTo}, "\x00")
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

func normalizeEvaluationLimit(limit int) int {
	if limit <= 0 {
		return DefaultAlarmEvaluationsLimit
	}
	if limit > MaxAlarmEvaluationsLimit {
		return MaxAlarmEvaluationsLimit
	}
	return limit
}

func cloneEvaluationRecord(record AlarmEvaluationRecord) AlarmEvaluationRecord {
	cloned := record
	if record.QualityBlocker != nil {
		value := *record.QualityBlocker
		cloned.QualityBlocker = &value
	}
	if record.ActiveIncidentCorrelationID != nil {
		value := *record.ActiveIncidentCorrelationID
		cloned.ActiveIncidentCorrelationID = &value
	}
	if record.Snapshot != nil {
		cloned.Snapshot = append(json.RawMessage(nil), record.Snapshot...)
	}
	return cloned
}
