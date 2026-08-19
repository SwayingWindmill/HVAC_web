package ruleruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrExecutionNotFound    = errors.New("rule execution not found")
	ErrExecutionIdentity    = errors.New("rule execution identity conflict")
	ErrRuleRevisionIdentity = errors.New("rule revision identity conflict")
	ErrLeaseConflict        = errors.New("rule execution lease conflict")
	ErrFenceStale           = errors.New("rule execution fence is stale")
	ErrStateCASConflict     = errors.New("rule state CAS conflict")
)

type ContinuationRecord struct {
	ContinuationID string     `json:"continuationId"`
	WorkItemID     string     `json:"workItemId"`
	NodeID         string     `json:"nodeId"`
	Path           string     `json:"path"`
	WakeAt         time.Time  `json:"wakeAt"`
	OutputPort     string     `json:"outputPort"`
	Value          TypedValue `json:"value"`
	Status         string     `json:"status"`
}

type EffectRecord struct {
	Effect      EffectIntent `json:"effect"`
	WorkItemID  string       `json:"workItemId"`
	NodeID      string       `json:"nodeId"`
	Path        string       `json:"path"`
	Outputs     []NodeOutput `json:"outputs"`
	Status      string       `json:"status"`
	Attempts    int          `json:"attempts"`
	RetryAt     time.Time    `json:"retryAt,omitempty"`
	Receipt     string       `json:"receipt,omitempty"`
	Failure     FailureClass `json:"failure,omitempty"`
	FailureCode string       `json:"failureCode,omitempty"`
}

type ExecutionSnapshot struct {
	Execution        ExecutionRecord          `json:"execution"`
	Event            RuleEventEnvelope        `json:"event"`
	RuleDigest       string                   `json:"ruleDigest"`
	Work             []WorkRecord             `json:"work"`
	Continuations    []ContinuationRecord     `json:"continuations"`
	Effects          []EffectRecord           `json:"effects"`
	StateTransitions []AppliedStateTransition `json:"stateTransitions"`
	Trace            []TraceRecord            `json:"trace"`
}

type ExecutionStore interface {
	CreateOrLoad(context.Context, ExecutionSnapshot) (ExecutionSnapshot, bool, error)
	Load(context.Context, string) (ExecutionSnapshot, error)
	ReadRuleState(context.Context, RuleStateKey) (RuleStateRecord, bool, error)
	Claim(context.Context, string, string, time.Time, time.Duration) (ExecutionSnapshot, error)
	Save(context.Context, ExecutionSnapshot, string, int64, time.Time) error
	SaveWithStateCAS(context.Context, ExecutionSnapshot, string, int64, time.Time, RuleStateKey, StateTransition) (RuleStateRecord, error)
	Release(context.Context, string, string, int64, time.Time) error
}

type MemoryStore struct {
	mu          sync.Mutex
	executions  map[string]ExecutionSnapshot
	orderingKey map[string]string
	states      map[string]RuleStateRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{executions: map[string]ExecutionSnapshot{}, orderingKey: map[string]string{}, states: map[string]RuleStateRecord{}}
}

func (s *MemoryStore) CreateOrLoad(_ context.Context, seed ExecutionSnapshot) (ExecutionSnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := seed.Execution.ExecutionID
	if existing, ok := s.executions[id]; ok {
		if !sameExecutionIdentity(existing, seed) {
			return ExecutionSnapshot{}, false, ErrExecutionIdentity
		}
		return cloneSnapshot(existing), false, nil
	}
	copy := cloneSnapshot(seed)
	s.executions[id] = copy
	return cloneSnapshot(copy), true, nil
}

func (s *MemoryStore) Load(_ context.Context, executionID string) (ExecutionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.executions[executionID]
	if !ok {
		return ExecutionSnapshot{}, ErrExecutionNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func (s *MemoryStore) ReadRuleState(_ context.Context, key RuleStateKey) (RuleStateRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[ruleStateMapKey(key)]
	if !ok {
		return RuleStateRecord{}, false, nil
	}
	state.Value = append(json.RawMessage(nil), state.Value...)
	return state, true, nil
}

func (s *MemoryStore) Claim(_ context.Context, executionID, owner string, now time.Time, ttl time.Duration) (ExecutionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.executions[executionID]
	if !ok {
		return ExecutionSnapshot{}, ErrExecutionNotFound
	}
	if terminal(snapshot.Execution.Status) {
		return cloneSnapshot(snapshot), nil
	}
	if snapshot.Execution.LeaseOwner != "" && snapshot.Execution.LeaseOwner != owner && snapshot.Execution.LeaseUntil.After(now) {
		return ExecutionSnapshot{}, ErrLeaseConflict
	}
	if holder := s.orderingKey[snapshot.Execution.OrderingKey]; holder != "" && holder != executionID {
		other := s.executions[holder]
		if !terminal(other.Execution.Status) {
			return ExecutionSnapshot{}, ErrLeaseConflict
		}
		delete(s.orderingKey, snapshot.Execution.OrderingKey)
	}
	snapshot.Execution.LeaseFence++
	snapshot.Execution.LeaseOwner = owner
	snapshot.Execution.LeaseUntil = now.Add(ttl)
	snapshot.Execution.Status = normalizeActiveStatus(snapshot)
	snapshot.Execution.UpdatedAt = now
	s.executions[executionID] = cloneSnapshot(snapshot)
	s.orderingKey[snapshot.Execution.OrderingKey] = executionID
	return cloneSnapshot(snapshot), nil
}

func (s *MemoryStore) Save(_ context.Context, snapshot ExecutionSnapshot, owner string, fence int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.executions[snapshot.Execution.ExecutionID]
	if !ok {
		return ErrExecutionNotFound
	}
	if current.Execution.LeaseOwner != owner || current.Execution.LeaseFence != fence || current.Execution.LeaseUntil.Before(now) {
		return ErrFenceStale
	}
	if snapshot.Execution.LeaseFence != fence || snapshot.Execution.LeaseOwner != owner {
		return ErrFenceStale
	}
	snapshot.Execution.UpdatedAt = now
	s.executions[snapshot.Execution.ExecutionID] = cloneSnapshot(snapshot)
	return nil
}

func (s *MemoryStore) SaveWithStateCAS(_ context.Context, snapshot ExecutionSnapshot, owner string, fence int64, now time.Time, key RuleStateKey, transition StateTransition) (RuleStateRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.executions[snapshot.Execution.ExecutionID]
	if !ok {
		return RuleStateRecord{}, ErrExecutionNotFound
	}
	if current.Execution.LeaseOwner != owner || current.Execution.LeaseFence != fence || current.Execution.LeaseUntil.Before(now) {
		return RuleStateRecord{}, ErrFenceStale
	}
	if snapshot.Execution.LeaseFence != fence || snapshot.Execution.LeaseOwner != owner {
		return RuleStateRecord{}, ErrFenceStale
	}
	if key.TenantID != snapshot.Execution.TenantID || key.RuleRevisionID != snapshot.Execution.RuleRevisionID || transition.ScopeKey != key.ScopeKey || !json.Valid(transition.Value) {
		return RuleStateRecord{}, ErrStateCASConflict
	}
	state, exists := s.states[ruleStateMapKey(key)]
	next, err := nextRuleState(key, state, exists, transition)
	if err != nil {
		return RuleStateRecord{}, err
	}
	snapshot.Execution.UpdatedAt = now
	s.states[ruleStateMapKey(key)] = next
	s.executions[snapshot.Execution.ExecutionID] = cloneSnapshot(snapshot)
	return next, nil
}

func (s *MemoryStore) Release(_ context.Context, executionID, owner string, fence int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.executions[executionID]
	if !ok {
		return ErrExecutionNotFound
	}
	if snapshot.Execution.LeaseOwner != owner || snapshot.Execution.LeaseFence != fence {
		return ErrFenceStale
	}
	snapshot.Execution.LeaseOwner = ""
	snapshot.Execution.LeaseUntil = time.Time{}
	snapshot.Execution.UpdatedAt = now
	s.executions[executionID] = cloneSnapshot(snapshot)
	if terminal(snapshot.Execution.Status) && s.orderingKey[snapshot.Execution.OrderingKey] == executionID {
		delete(s.orderingKey, snapshot.Execution.OrderingKey)
	}
	return nil
}

func nextRuleState(key RuleStateKey, existing RuleStateRecord, exists bool, transition StateTransition) (RuleStateRecord, error) {
	if key.TenantID == "" || key.RuleRevisionID == "" || key.NodeInstanceID == "" || key.ScopeKey == "" || transition.ScopeKey != key.ScopeKey || transition.SchemaVersion <= 0 || !json.Valid(transition.Value) {
		return RuleStateRecord{}, ErrStateCASConflict
	}
	if exists {
		if existing.SchemaVersion != transition.SchemaVersion || existing.Revision != transition.ExpectedRevision {
			return RuleStateRecord{}, ErrStateCASConflict
		}
		existing.Revision++
		existing.Value = append(json.RawMessage(nil), transition.Value...)
		existing.ExpiresAt = transition.ExpiresAt
		return existing, nil
	}
	if transition.ExpectedRevision != 0 {
		return RuleStateRecord{}, ErrStateCASConflict
	}
	return RuleStateRecord{
		TenantID:       key.TenantID,
		RuleRevisionID: key.RuleRevisionID,
		NodeInstanceID: key.NodeInstanceID,
		ScopeKey:       key.ScopeKey,
		SchemaVersion:  transition.SchemaVersion,
		Revision:       1,
		Value:          append(json.RawMessage(nil), transition.Value...),
		ExpiresAt:      transition.ExpiresAt,
	}, nil
}

func ruleStateMapKey(key RuleStateKey) string {
	return key.TenantID + "\x00" + key.RuleRevisionID + "\x00" + key.NodeInstanceID + "\x00" + key.ScopeKey
}

func cloneSnapshot(snapshot ExecutionSnapshot) ExecutionSnapshot {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		panic(fmt.Sprintf("marshal execution snapshot: %v", err))
	}
	var cloned ExecutionSnapshot
	if err := json.Unmarshal(payload, &cloned); err != nil {
		panic(fmt.Sprintf("unmarshal execution snapshot: %v", err))
	}
	return cloned
}

func terminal(status ExecutionStatus) bool {
	switch status {
	case ExecutionSucceeded, ExecutionDead, ExecutionQuarantined, ExecutionFailed:
		return true
	default:
		return false
	}
}

func normalizeActiveStatus(snapshot ExecutionSnapshot) ExecutionStatus {
	if terminal(snapshot.Execution.Status) {
		return snapshot.Execution.Status
	}
	for _, effect := range snapshot.Effects {
		if effect.Status == "PENDING" || effect.Status == "DISPATCHING" || effect.Status == "AMBIGUOUS" {
			return ExecutionBlocked
		}
	}
	for _, work := range snapshot.Work {
		if work.Status == "READY" || work.Status == "RETRY_WAIT" {
			return ExecutionRunning
		}
	}
	for _, continuation := range snapshot.Continuations {
		if continuation.Status == "PENDING" {
			return ExecutionWaiting
		}
	}
	return ExecutionRunning
}

func sameExecutionIdentity(existing, seed ExecutionSnapshot) bool {
	return existing.Execution.ExecutionID == seed.Execution.ExecutionID &&
		existing.Execution.TenantID == seed.Execution.TenantID &&
		existing.Execution.SiteID == seed.Execution.SiteID &&
		existing.Execution.RuleRevisionID == seed.Execution.RuleRevisionID &&
		existing.Execution.BindingID == seed.Execution.BindingID &&
		existing.Execution.BindingRevision == seed.Execution.BindingRevision &&
		existing.Execution.EventID == seed.Execution.EventID &&
		existing.Execution.OrderingKey == seed.Execution.OrderingKey &&
		existing.RuleDigest == seed.RuleDigest &&
		existing.Event.Schema == seed.Event.Schema &&
		existing.Event.SubjectType == seed.Event.SubjectType &&
		existing.Event.SubjectID == seed.Event.SubjectID &&
		existing.Event.PayloadDigest == seed.Event.PayloadDigest
}
