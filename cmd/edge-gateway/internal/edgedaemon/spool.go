package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var ErrSpoolCapacity = errors.New("offline spool capacity exhausted")

type EvidenceClass string

const (
	EvidenceSafety            EvidenceClass = "SAFETY_EVIDENCE"
	EvidenceControl           EvidenceClass = "COMMAND_RESULT"
	EvidenceAlarm             EvidenceClass = "ALARM_FACT"
	EvidenceOTAResult         EvidenceClass = "OTA_RESULT"
	EvidenceAudit             EvidenceClass = "AUDIT_EVIDENCE"
	EvidenceTelemetryCritical EvidenceClass = "TELEMETRY_CRITICAL"
	EvidenceConfigResult      EvidenceClass = "CONFIG_RESULT"
	EvidenceTelemetryNormal   EvidenceClass = "TELEMETRY_NORMAL"
	EvidenceDiagnostic        EvidenceClass = "DIAGNOSTIC"
)

type CapacityState string

const (
	CapacityNormal         CapacityState = "NORMAL"
	CapacityPressure       CapacityState = "PRESSURE"
	CapacityCritical       CapacityState = "CRITICAL"
	CapacityReadOnlySafety CapacityState = "READ_ONLY_SAFETY"
)

type SpoolItem struct {
	ID      string        `json:"id"`
	Topic   string        `json:"topic"`
	QoS     byte          `json:"qos"`
	Class   EvidenceClass `json:"class"`
	Payload []byte        `json:"payload"`
}

func (item SpoolItem) Bytes() int64 {
	return int64(len(item.Payload))
}

type SpoolAdmission struct {
	State   CapacityState `json:"state"`
	ShedIDs []string      `json:"shedIds,omitempty"`
}

type spoolDiskState struct {
	Items map[string]SpoolItem `json:"items"`
	Order []string             `json:"order"`
}

// GradedSpool persists bounded MQTT uplink messages on disk and prioritizes safety,
// command, alarm, OTA, and audit items ahead of sheddable diagnostic/normal telemetry.
type GradedSpool struct {
	mu       sync.Mutex
	stateDir string
	capacity int64
	used     int64
	state    spoolDiskState
}

// OpenGradedSpool opens or recovers the durable edge MQTT spool under directory.
func OpenGradedSpool(directory string, capacityBytes int64) (*GradedSpool, error) {
	if strings.TrimSpace(directory) == "" || capacityBytes <= 0 {
		return nil, errors.New("spool directory and positive capacity are required")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("create spool directory: %w", err)
	}
	spool := &GradedSpool{
		stateDir: directory,
		capacity: capacityBytes,
		state:    spoolDiskState{Items: map[string]SpoolItem{}},
	}
	data, err := os.ReadFile(spool.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return spool, nil
		}
		return nil, fmt.Errorf("read spool state: %w", err)
	}
	if err := json.Unmarshal(data, &spool.state); err != nil {
		return nil, fmt.Errorf("decode spool state: %w", err)
	}
	if spool.state.Items == nil {
		spool.state.Items = map[string]SpoolItem{}
	}
	seen := make(map[string]struct{}, len(spool.state.Items))
	for _, id := range spool.state.Order {
		item, exists := spool.state.Items[id]
		if !exists {
			return nil, errors.New("spool order references a missing item")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("spool order contains duplicate item")
		}
		if strings.TrimSpace(item.ID) != id || strings.TrimSpace(item.Topic) == "" || item.Bytes() <= 0 || evidencePriority(item.Class) < 0 {
			return nil, errors.New("spool contains invalid item")
		}
		seen[id] = struct{}{}
		spool.used += item.Bytes()
	}
	if len(seen) != len(spool.state.Items) || spool.used > spool.capacity {
		return nil, errors.New("spool state exceeds capacity or contains unordered items")
	}
	return spool, nil
}

func (s *GradedSpool) statePath() string {
	return filepath.Join(s.stateDir, "mqtt-spool.json")
}

func (s *GradedSpool) persistLocked() error {
	encoded, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode spool state: %w", err)
	}
	return writeFileAtomic(s.statePath(), append(encoded, '\n'), 0o600)
}

// Admit persists item atomically, shedding lower-priority unprotected items FIFO if needed.
func (s *GradedSpool) Admit(item SpoolItem) (SpoolAdmission, error) {
	if s == nil {
		return SpoolAdmission{}, errors.New("spool is nil")
	}
	item.ID = strings.TrimSpace(item.ID)
	item.Topic = strings.TrimSpace(item.Topic)
	if item.ID == "" || item.Topic == "" || item.Bytes() <= 0 || evidencePriority(item.Class) < 0 {
		return SpoolAdmission{}, errors.New("spool item requires valid id, topic, class, and payload")
	}
	if item.Bytes() > s.capacity {
		return SpoolAdmission{State: CapacityReadOnlySafety}, ErrSpoolCapacity
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.state.Items[item.ID]; exists {
		return SpoolAdmission{State: s.capacityStateLocked()}, nil
	}

	originalState := cloneSpoolDiskState(s.state)
	originalUsed := s.used
	var admission SpoolAdmission

	if s.used+item.Bytes() > s.capacity {
		for _, candidate := range s.sheddingCandidatesLocked(item.Class) {
			delete(s.state.Items, candidate.ID)
			s.used -= candidate.Bytes()
			admission.ShedIDs = append(admission.ShedIDs, candidate.ID)
			if s.used+item.Bytes() <= s.capacity {
				break
			}
		}
	}
	if s.used+item.Bytes() > s.capacity {
		s.state = originalState
		s.used = originalUsed
		admission.State = s.capacityStateLocked()
		if isProtectedEvidence(item.Class) {
			admission.State = CapacityReadOnlySafety
		}
		return admission, ErrSpoolCapacity
	}

	itemCopy := item
	itemCopy.Payload = append([]byte(nil), item.Payload...)
	s.state.Items[item.ID] = itemCopy
	s.state.Order = append(s.state.Order, item.ID)
	s.used += item.Bytes()
	s.compactOrderLocked()
	if err := s.persistLocked(); err != nil {
		s.state = originalState
		s.used = originalUsed
		return SpoolAdmission{}, err
	}
	admission.State = s.capacityStateLocked()
	return admission, nil
}

// Pending returns spooled items ordered from highest evidence priority to lowest (FIFO within tier).
func (s *GradedSpool) Pending() []SpoolItem {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]SpoolItem, 0, len(s.state.Items))
	for _, id := range s.state.Order {
		item, exists := s.state.Items[id]
		if !exists {
			continue
		}
		item.Payload = append([]byte(nil), item.Payload...)
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return evidencePriority(items[left].Class) > evidencePriority(items[right].Class)
	})
	return items
}

// Drain publishes pending items in priority order and removes acknowledged items cleanly.
func (s *GradedSpool) Drain(ctx context.Context, maxItems int, publish func(context.Context, SpoolItem) error) (int, error) {
	if s == nil || publish == nil {
		return 0, nil
	}
	pending := s.Pending()
	if maxItems > 0 && len(pending) > maxItems {
		pending = pending[:maxItems]
	}
	drained := 0
	for _, item := range pending {
		if ctx.Err() != nil {
			return drained, ctx.Err()
		}
		if err := publish(ctx, item); err != nil {
			return drained, err
		}
		if _, err := s.Remove(item.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return drained, err
		}
		drained++
	}
	return drained, nil
}

// Remove deletes an item by ID from the persistent spool.
func (s *GradedSpool) Remove(id string) (SpoolItem, error) {
	if s == nil || strings.TrimSpace(id) == "" {
		return SpoolItem{}, errors.New("spool item ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	item, exists := s.state.Items[id]
	if !exists {
		return SpoolItem{}, os.ErrNotExist
	}
	originalState := cloneSpoolDiskState(s.state)
	originalUsed := s.used
	delete(s.state.Items, id)
	s.used -= item.Bytes()
	s.compactOrderLocked()
	if err := s.persistLocked(); err != nil {
		s.state = originalState
		s.used = originalUsed
		return SpoolItem{}, err
	}
	return item, nil
}

// UsedBytes returns the total payload bytes currently buffered.
func (s *GradedSpool) UsedBytes() int64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

func (s *GradedSpool) sheddingCandidatesLocked(incoming EvidenceClass) []SpoolItem {
	incomingPriority := evidencePriority(incoming)
	candidates := make([]SpoolItem, 0)
	for _, id := range s.state.Order {
		item, exists := s.state.Items[id]
		if !exists || isProtectedEvidence(item.Class) || evidencePriority(item.Class) >= incomingPriority {
			continue
		}
		candidates = append(candidates, item)
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return evidencePriority(candidates[left].Class) < evidencePriority(candidates[right].Class)
	})
	return candidates
}

func (s *GradedSpool) capacityStateLocked() CapacityState {
	if s.capacity == 0 {
		return CapacityReadOnlySafety
	}
	ratio := float64(s.used) / float64(s.capacity)
	switch {
	case ratio >= 0.95:
		return CapacityReadOnlySafety
	case ratio >= 0.85:
		return CapacityCritical
	case ratio >= 0.70:
		return CapacityPressure
	default:
		return CapacityNormal
	}
}

func (s *GradedSpool) compactOrderLocked() {
	order := make([]string, 0, len(s.state.Items))
	for _, id := range s.state.Order {
		if _, exists := s.state.Items[id]; exists {
			order = append(order, id)
		}
	}
	s.state.Order = order
}

func cloneSpoolDiskState(state spoolDiskState) spoolDiskState {
	cloned := spoolDiskState{
		Items: make(map[string]SpoolItem, len(state.Items)),
		Order: append([]string(nil), state.Order...),
	}
	for id, item := range state.Items {
		item.Payload = append([]byte(nil), item.Payload...)
		cloned.Items[id] = item
	}
	return cloned
}

func isProtectedEvidence(class EvidenceClass) bool {
	switch class {
	case EvidenceSafety, EvidenceControl, EvidenceAlarm, EvidenceOTAResult, EvidenceAudit:
		return true
	default:
		return false
	}
}

func evidencePriority(class EvidenceClass) int {
	switch class {
	case EvidenceDiagnostic:
		return 0
	case EvidenceTelemetryNormal:
		return 1
	case EvidenceConfigResult:
		return 2
	case EvidenceTelemetryCritical:
		return 3
	case EvidenceAudit:
		return 4
	case EvidenceOTAResult:
		return 5
	case EvidenceAlarm:
		return 6
	case EvidenceControl:
		return 7
	case EvidenceSafety:
		return 8
	default:
		return -1
	}
}
