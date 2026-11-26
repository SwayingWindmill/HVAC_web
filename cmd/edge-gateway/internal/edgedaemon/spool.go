package edgedaemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	EvidenceTelemetryVeryHigh EvidenceClass = "TELEMETRY_VERY_HIGH"
	EvidenceTelemetryHigh     EvidenceClass = "TELEMETRY_HIGH"
	EvidenceConfigResult      EvidenceClass = "CONFIG_RESULT"
	EvidenceTelemetryNormal   EvidenceClass = "TELEMETRY_NORMAL"
	EvidenceTelemetryMedium   EvidenceClass = "TELEMETRY_MEDIUM"
	EvidenceTelemetryLow      EvidenceClass = "TELEMETRY_LOW"
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

const (
	spoolWALOpAdmit  = "admit"
	spoolWALOpRemove = "remove"
	spoolWALOpShed   = "shed"

	spoolWALMinCompactOps   = 256
	spoolWALMinCompactBytes = int64(1 << 20)
)

type spoolWALEntry struct {
	Op   string     `json:"op"`
	Item *SpoolItem `json:"item,omitempty"`
	ID   string     `json:"id,omitempty"`
	IDs  []string   `json:"ids,omitempty"`
}

// GradedSpool persists bounded MQTT uplink messages on disk using an append-only WAL
// with periodic atomic compaction and prioritizes safety, command, alarm, OTA, and audit
// items ahead of sheddable diagnostic/normal telemetry.
type GradedSpool struct {
	mu               sync.Mutex
	drainMu          sync.Mutex
	stateDir         string
	capacity         int64
	used             int64
	state            spoolDiskState
	walOps           int
	walBytes         int64
	diskBytesWritten int64
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
	if _, err := os.Stat(spool.walPath()); err == nil {
		if err := spool.recoverWALLocked(); err != nil {
			return nil, err
		}
		// Clean up any leftover legacy snapshot if an earlier migration crashed right after rename.
		if err := os.Remove(spool.legacyStatePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove migrated legacy spool state: %w", err)
		}
		return spool, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat spool WAL: %w", err)
	}

	if err := spool.migrateLegacySnapshotLocked(); err != nil {
		return nil, err
	}
	return spool, nil
}

func (s *GradedSpool) legacyStatePath() string {
	return filepath.Join(s.stateDir, "mqtt-spool.json")
}

func (s *GradedSpool) walPath() string {
	return filepath.Join(s.stateDir, "mqtt-spool.wal")
}

func (s *GradedSpool) migrateLegacySnapshotLocked() error {
	data, err := os.ReadFile(s.legacyStatePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read legacy spool state: %w", err)
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		return fmt.Errorf("decode legacy spool state: %w", err)
	}
	if s.state.Items == nil {
		s.state.Items = map[string]SpoolItem{}
	}
	seen := make(map[string]struct{}, len(s.state.Items))
	for _, id := range s.state.Order {
		item, exists := s.state.Items[id]
		if !exists {
			return errors.New("spool order references a missing item")
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.New("spool order contains duplicate item")
		}
		if !validSpoolItem(item) || strings.TrimSpace(item.ID) != id {
			return errors.New("spool contains invalid item")
		}
		seen[id] = struct{}{}
		s.used += item.Bytes()
	}
	if len(seen) != len(s.state.Items) || s.used > s.capacity {
		return errors.New("spool state exceeds capacity or contains unordered items")
	}
	if err := s.compactLocked(); err != nil {
		return fmt.Errorf("migrate legacy spool state to WAL: %w", err)
	}
	if err := os.Remove(s.legacyStatePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy spool state: %w", err)
	}
	return nil
}

func (s *GradedSpool) recoverWALLocked() error {
	file, err := os.Open(s.walPath())
	if err != nil {
		return fmt.Errorf("open spool WAL: %w", err)
	}

	reader := bufio.NewReaderSize(file, 64*1024)
	var (
		offset         int64
		validEndOffset int64
		needsTruncate  bool
		needsNewline   bool
	)
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			hasNewline := lineBytes[len(lineBytes)-1] == '\n'
			trimmed := bytes.TrimSpace(lineBytes)
			atEOF, peekErr := atSpoolWALEOF(reader, readErr)
			if peekErr != nil {
				_ = file.Close()
				return fmt.Errorf("read spool WAL: %w", peekErr)
			}

			if len(trimmed) == 0 {
				if atEOF {
					needsTruncate = true
					break
				}
				_ = file.Close()
				return errors.New("spool WAL contains blank line before EOF")
			}

			var entry spoolWALEntry
			if err := json.Unmarshal(trimmed, &entry); err != nil {
				if atEOF {
					needsTruncate = true
					break
				}
				_ = file.Close()
				return fmt.Errorf("decode spool WAL entry: %w", err)
			}

			if err := s.applyWALEntryLocked(entry); err != nil {
				if atEOF {
					needsTruncate = true
					break
				}
				_ = file.Close()
				return err
			}

			s.walOps++
			offset += int64(len(lineBytes))
			validEndOffset = offset
			if !hasNewline {
				needsNewline = true
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return fmt.Errorf("read spool WAL: %w", readErr)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close spool WAL: %w", err)
	}

	s.compactOrderLocked()
	if s.used > s.capacity {
		return errors.New("spool state exceeds capacity")
	}

	if needsTruncate {
		if err := os.Truncate(s.walPath(), validEndOffset); err != nil {
			return fmt.Errorf("truncate partial spool WAL line: %w", err)
		}
		s.walBytes = validEndOffset
	} else if needsNewline {
		appendFile, err := os.OpenFile(s.walPath(), os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open spool WAL trailing newline: %w", err)
		}
		_, writeErr := appendFile.Write([]byte{'\n'})
		syncErr := appendFile.Sync()
		closeErr := appendFile.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return fmt.Errorf("terminate spool WAL trailing line: %w", err)
		}
		s.walBytes = validEndOffset + 1
	} else {
		s.walBytes = validEndOffset
	}
	return nil
}

func atSpoolWALEOF(reader *bufio.Reader, readErr error) (bool, error) {
	if errors.Is(readErr, io.EOF) {
		return true, nil
	}
	if readErr != nil {
		return false, readErr
	}
	_, err := reader.Peek(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func (s *GradedSpool) applyWALEntryLocked(entry spoolWALEntry) error {
	switch entry.Op {
	case spoolWALOpAdmit:
		if entry.Item == nil {
			return errors.New("spool WAL admit entry missing item")
		}
		item := *entry.Item
		item.ID = strings.TrimSpace(item.ID)
		item.Topic = strings.TrimSpace(item.Topic)
		if !validSpoolItem(item) {
			return errors.New("spool WAL admit entry contains invalid item")
		}
		if _, exists := s.state.Items[item.ID]; exists {
			return nil
		}
		if s.used+item.Bytes() > s.capacity {
			return errors.New("spool WAL admit exceeds capacity")
		}
		item.Payload = append([]byte(nil), item.Payload...)
		s.state.Items[item.ID] = item
		s.state.Order = append(s.state.Order, item.ID)
		s.used += item.Bytes()
		return nil
	case spoolWALOpRemove:
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			return errors.New("spool WAL remove entry missing id")
		}
		if existing, exists := s.state.Items[id]; exists {
			delete(s.state.Items, id)
			s.used -= existing.Bytes()
		}
		return nil
	case spoolWALOpShed:
		for _, rawID := range entry.IDs {
			id := strings.TrimSpace(rawID)
			if id == "" {
				continue
			}
			if existing, exists := s.state.Items[id]; exists {
				delete(s.state.Items, id)
				s.used -= existing.Bytes()
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported spool WAL op %q", entry.Op)
	}
}

func validSpoolItem(item SpoolItem) bool {
	return strings.TrimSpace(item.ID) != "" &&
		strings.TrimSpace(item.Topic) != "" &&
		item.Bytes() > 0 &&
		evidencePriority(item.Class) >= 0
}

func (s *GradedSpool) appendWALLocked(entries []spoolWALEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("encode spool WAL entry: %w", err)
		}
		buf.Write(encoded)
		buf.WriteByte('\n')
	}
	payload := buf.Bytes()

	file, err := os.OpenFile(s.walPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open spool WAL for append: %w", err)
	}
	n, writeErr := file.Write(payload)
	syncErr := file.Sync()
	closeErr := file.Close()
	if n > 0 {
		s.diskBytesWritten += int64(n)
	}
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("append spool WAL: %w", err)
	}
	s.walOps += len(entries)
	s.walBytes += int64(len(payload))
	return nil
}

func (s *GradedSpool) needsCompactionLocked() bool {
	if s.walOps <= len(s.state.Items) {
		return false
	}
	opThreshold := max(spoolWALMinCompactOps, len(s.state.Items)*2)
	byteThreshold := max(s.capacity*2, spoolWALMinCompactBytes)
	return s.walOps >= opThreshold || s.walBytes > byteThreshold
}

func (s *GradedSpool) compactLocked() error {
	s.compactOrderLocked()
	tmpPath := s.walPath() + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create spool WAL temp file: %w", err)
	}

	var written int64
	for _, id := range s.state.Order {
		item, exists := s.state.Items[id]
		if !exists {
			continue
		}
		itemCopy := item
		encoded, err := json.Marshal(spoolWALEntry{
			Op:   spoolWALOpAdmit,
			Item: &itemCopy,
		})
		if err != nil {
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("encode compacted spool WAL item: %w", err)
		}
		encoded = append(encoded, '\n')
		n, writeErr := file.Write(encoded)
		if n > 0 {
			written += int64(n)
		}
		if writeErr != nil {
			_ = file.Close()
			_ = os.Remove(tmpPath)
			s.diskBytesWritten += written
			return fmt.Errorf("write compacted spool WAL: %w", writeErr)
		}
	}
	s.diskBytesWritten += written

	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync compacted spool WAL: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close compacted spool WAL: %w", err)
	}
	if err := os.Rename(tmpPath, s.walPath()); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit compacted spool WAL: %w", err)
	}
	s.walOps = len(s.state.Order)
	s.walBytes = written
	return nil
}

// Compact atomically rewrites mqtt-spool.wal with only currently retained items.
func (s *GradedSpool) Compact() error {
	if s == nil {
		return errors.New("spool is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compactLocked()
}

// Admit persists item atomically, shedding lower-priority unprotected items FIFO if needed.
func (s *GradedSpool) Admit(item SpoolItem) (SpoolAdmission, error) {
	if s == nil {
		return SpoolAdmission{}, errors.New("spool is nil")
	}
	item.ID = strings.TrimSpace(item.ID)
	item.Topic = strings.TrimSpace(item.Topic)
	if !validSpoolItem(item) {
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

	entries := make([]spoolWALEntry, 0, 2)
	if len(admission.ShedIDs) > 0 {
		entries = append(entries, spoolWALEntry{
			Op:  spoolWALOpShed,
			IDs: append([]string(nil), admission.ShedIDs...),
		})
	}
	entries = append(entries, spoolWALEntry{
		Op:   spoolWALOpAdmit,
		Item: &itemCopy,
	})
	if err := s.appendWALLocked(entries); err != nil {
		s.state = originalState
		s.used = originalUsed
		return SpoolAdmission{}, err
	}
	if s.needsCompactionLocked() {
		_ = s.compactLocked()
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
	if !s.drainMu.TryLock() {
		return 0, nil
	}
	defer s.drainMu.Unlock()
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
	if err := s.appendWALLocked([]spoolWALEntry{{Op: spoolWALOpRemove, ID: id}}); err != nil {
		s.state = originalState
		s.used = originalUsed
		return SpoolItem{}, err
	}
	if s.needsCompactionLocked() {
		_ = s.compactLocked()
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
	case EvidenceTelemetryLow:
		return 1
	case EvidenceTelemetryNormal, EvidenceTelemetryMedium:
		return 2
	case EvidenceConfigResult:
		return 3
	case EvidenceTelemetryHigh:
		return 4
	case EvidenceTelemetryCritical, EvidenceTelemetryVeryHigh:
		return 5
	case EvidenceAudit:
		return 6
	case EvidenceOTAResult:
		return 7
	case EvidenceAlarm:
		return 8
	case EvidenceControl:
		return 9
	case EvidenceSafety:
		return 10
	default:
		return -1
	}
}
