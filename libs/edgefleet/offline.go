package edgefleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type EvidenceClass string

const (
	EvidenceSafety            EvidenceClass = "SAFETY_EVIDENCE"
	EvidenceControl           EvidenceClass = "COMMAND_RESULT"
	EvidenceAlarm             EvidenceClass = "ALARM_FACT"
	EvidenceAudit             EvidenceClass = "AUDIT_EVIDENCE"
	EvidenceConfigResult      EvidenceClass = "CONFIG_RESULT"
	EvidenceTelemetryCritical EvidenceClass = "TELEMETRY_CRITICAL"
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

type OfflineItem struct {
	ID      string        `json:"id"`
	Class   EvidenceClass `json:"class"`
	Payload []byte        `json:"payload"`
}

func (item OfflineItem) Bytes() int64 {
	return int64(len(item.Payload))
}

type OfflineAdmission struct {
	State   CapacityState `json:"state"`
	ShedIDs []string      `json:"shedIds,omitempty"`
}

type offlineDiskState struct {
	Items map[string]OfflineItem `json:"items"`
	Order []string               `json:"order"`
}

type OfflineBuffer struct {
	mu        sync.Mutex
	directory string
	capacity  int64
	used      int64
	state     offlineDiskState
}

func OpenOfflineBuffer(directory string, capacityBytes int64) (*OfflineBuffer, error) {
	if strings.TrimSpace(directory) == "" || capacityBytes <= 0 {
		return nil, errors.New("offline buffer directory and positive capacity are required")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("create offline buffer directory: %w", err)
	}
	buffer := &OfflineBuffer{
		directory: directory,
		capacity:  capacityBytes,
		state:     offlineDiskState{Items: map[string]OfflineItem{}},
	}
	data, err := os.ReadFile(buffer.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return buffer, nil
		}
		return nil, fmt.Errorf("read offline buffer state: %w", err)
	}
	if err := json.Unmarshal(data, &buffer.state); err != nil {
		return nil, fmt.Errorf("decode offline buffer state: %w", err)
	}
	if buffer.state.Items == nil {
		buffer.state.Items = map[string]OfflineItem{}
	}
	seen := make(map[string]struct{}, len(buffer.state.Items))
	for _, id := range buffer.state.Order {
		item, exists := buffer.state.Items[id]
		if !exists {
			return nil, errors.New("offline buffer order references a missing item")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("offline buffer order contains a duplicate item")
		}
		if strings.TrimSpace(item.ID) != id || item.Bytes() <= 0 || !validEvidenceClass(item.Class) {
			return nil, errors.New("offline buffer contains an invalid item")
		}
		seen[id] = struct{}{}
		buffer.used += item.Bytes()
	}
	if len(seen) != len(buffer.state.Items) || buffer.used > buffer.capacity {
		return nil, errors.New("offline buffer persisted state exceeds capacity or contains unordered items")
	}
	return buffer, nil
}

func (buffer *OfflineBuffer) statePath() string {
	return filepath.Join(buffer.directory, "offline-spool.json")
}

func (buffer *OfflineBuffer) persistLocked() error {
	encoded, err := json.MarshalIndent(buffer.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode offline buffer: %w", err)
	}
	temporary := buffer.statePath() + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open offline buffer temporary file: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("write offline buffer: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync offline buffer: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close offline buffer: %w", err)
	}
	if err := os.Rename(temporary, buffer.statePath()); err != nil {
		return fmt.Errorf("activate offline buffer state: %w", err)
	}
	return nil
}

func (buffer *OfflineBuffer) Admit(item OfflineItem) (OfflineAdmission, error) {
	if buffer == nil {
		return OfflineAdmission{}, errors.New("offline buffer is unavailable")
	}
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" || item.Bytes() <= 0 || !validEvidenceClass(item.Class) {
		return OfflineAdmission{}, errors.New("offline item identity, class and payload are required")
	}
	if item.Bytes() > buffer.capacity {
		return OfflineAdmission{State: CapacityReadOnlySafety}, ErrOfflineCapacity
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if existing, exists := buffer.state.Items[item.ID]; exists {
		if existing.Class == item.Class && string(existing.Payload) == string(item.Payload) {
			return OfflineAdmission{State: buffer.capacityStateLocked()}, nil
		}
		return OfflineAdmission{State: buffer.capacityStateLocked()}, errors.New("offline item ID already exists with different content")
	}

	originalState := cloneOfflineDiskState(buffer.state)
	originalUsed := buffer.used
	admission := OfflineAdmission{}
	needed := buffer.used + item.Bytes() - buffer.capacity
	if needed > 0 {
		for _, candidate := range buffer.sheddingCandidatesLocked(item.Class) {
			delete(buffer.state.Items, candidate.ID)
			buffer.used -= candidate.Bytes()
			admission.ShedIDs = append(admission.ShedIDs, candidate.ID)
			needed = buffer.used + item.Bytes() - buffer.capacity
			if needed <= 0 {
				break
			}
		}
	}
	if buffer.used+item.Bytes() > buffer.capacity {
		buffer.state = originalState
		buffer.used = originalUsed
		admission.State = buffer.capacityStateLocked()
		if isProtectedEvidence(item.Class) {
			admission.State = CapacityReadOnlySafety
		}
		return admission, ErrOfflineCapacity
	}
	buffer.state.Items[item.ID] = item
	buffer.state.Order = append(buffer.state.Order, item.ID)
	buffer.used += item.Bytes()
	buffer.compactOrderLocked()
	if err := buffer.persistLocked(); err != nil {
		buffer.state = originalState
		buffer.used = originalUsed
		return OfflineAdmission{}, err
	}
	admission.State = buffer.capacityStateLocked()
	return admission, nil
}

func (buffer *OfflineBuffer) Remove(id string) (OfflineItem, error) {
	if buffer == nil || strings.TrimSpace(id) == "" {
		return OfflineItem{}, errors.New("offline item ID is required")
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	id = strings.TrimSpace(id)
	item, exists := buffer.state.Items[id]
	if !exists {
		return OfflineItem{}, os.ErrNotExist
	}
	originalState := cloneOfflineDiskState(buffer.state)
	originalUsed := buffer.used
	delete(buffer.state.Items, id)
	buffer.used -= item.Bytes()
	buffer.compactOrderLocked()
	if err := buffer.persistLocked(); err != nil {
		buffer.state = originalState
		buffer.used = originalUsed
		return OfflineItem{}, err
	}
	return item, nil
}

func (buffer *OfflineBuffer) Contains(id string) bool {
	if buffer == nil {
		return false
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	_, exists := buffer.state.Items[strings.TrimSpace(id)]
	return exists
}

func (buffer *OfflineBuffer) State() CapacityState {
	if buffer == nil {
		return CapacityReadOnlySafety
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.capacityStateLocked()
}

func (buffer *OfflineBuffer) UsedBytes() int64 {
	if buffer == nil {
		return 0
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.used
}

func (buffer *OfflineBuffer) Pending() []OfflineItem {
	if buffer == nil {
		return nil
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	items := make([]OfflineItem, 0, len(buffer.state.Items))
	for _, id := range buffer.state.Order {
		item, exists := buffer.state.Items[id]
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

func (buffer *OfflineBuffer) sheddingCandidatesLocked(incoming EvidenceClass) []OfflineItem {
	incomingPriority := evidencePriority(incoming)
	candidates := make([]OfflineItem, 0)
	for _, id := range buffer.state.Order {
		item, exists := buffer.state.Items[id]
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

func (buffer *OfflineBuffer) capacityStateLocked() CapacityState {
	if buffer.capacity == 0 {
		return CapacityReadOnlySafety
	}
	ratio := float64(buffer.used) / float64(buffer.capacity)
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

func (buffer *OfflineBuffer) compactOrderLocked() {
	order := make([]string, 0, len(buffer.state.Items))
	for _, id := range buffer.state.Order {
		if _, exists := buffer.state.Items[id]; exists {
			order = append(order, id)
		}
	}
	buffer.state.Order = order
}

func cloneOfflineDiskState(state offlineDiskState) offlineDiskState {
	cloned := offlineDiskState{Items: make(map[string]OfflineItem, len(state.Items)), Order: append([]string(nil), state.Order...)}
	for id, item := range state.Items {
		item.Payload = append([]byte(nil), item.Payload...)
		cloned.Items[id] = item
	}
	return cloned
}

func validEvidenceClass(class EvidenceClass) bool {
	return evidencePriority(class) >= 0
}

func isProtectedEvidence(class EvidenceClass) bool {
	switch class {
	case EvidenceSafety, EvidenceControl, EvidenceAlarm, EvidenceAudit:
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
	case EvidenceAlarm:
		return 5
	case EvidenceControl:
		return 6
	case EvidenceSafety:
		return 7
	default:
		return -1
	}
}
