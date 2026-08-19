package edgecontrol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type TimedataRecord struct {
	Sequence uint64 `json:"sequence"`
	Address  string `json:"address"`
	PointID  string `json:"pointId"`
	Sample   Sample `json:"sample"`
}

type TimedataRecorder interface {
	RecordImage(ProcessImage) (int, error)
}

// FileTimedata is the durable local Edge timeseries store. It deliberately does
// not own Cloud resend cursors or live-publish state. Those are separate Edge↔Cloud
// concerns, matching the pinned OpenEMS split between Timedata, live channel
// publishing and historic resend workers.
type FileTimedata struct {
	mu sync.Mutex

	directory            string
	minimumLocalPriority DataPriority
	latest               map[string]TimedataRecord
	nextSequence         uint64
}

func OpenFileTimedata(directory string, minimumLocalPriority DataPriority) (*FileTimedata, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("timedata directory is required")
	}
	if !validPriority(minimumLocalPriority) {
		return nil, fmt.Errorf("invalid minimum local persistence priority %q", minimumLocalPriority)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create Edge Timedata directory: %w", err)
	}
	store := &FileTimedata{
		directory:            directory,
		minimumLocalPriority: minimumLocalPriority,
		latest:               map[string]TimedataRecord{},
	}
	if err := store.recoverHistory(); err != nil {
		return nil, err
	}
	if err := store.persistLatestLocked(); err != nil {
		return nil, err
	}
	return store, nil
}

func priorityRank(priority DataPriority) int {
	switch priority {
	case PriorityVeryHigh:
		return 0
	case PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 3
	default:
		return 1 << 30
	}
}

func priorityAtLeast(candidate, minimum DataPriority) bool {
	return validPriority(candidate) && validPriority(minimum) && priorityRank(candidate) <= priorityRank(minimum)
}

func (store *FileTimedata) historyPath() string {
	return filepath.Join(store.directory, "history.jsonl")
}

func (store *FileTimedata) recoverHistory() error {
	file, err := os.Open(store.historyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Edge Timedata history: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	var previous uint64
	for scanner.Scan() {
		var record TimedataRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("decode Edge Timedata history: %w", err)
		}
		if record.Sequence == 0 || record.Sequence <= previous || strings.TrimSpace(record.Address) == "" || strings.TrimSpace(record.PointID) == "" {
			return errors.New("Edge Timedata history is not monotonic or is malformed")
		}
		if record.Sample.ObservedAt.IsZero() {
			return errors.New("Edge Timedata history contains sample without observed time")
		}
		previous = record.Sequence
		store.nextSequence = record.Sequence
		if current, exists := store.latest[record.Address]; !exists || sampleIsNewer(record.Sample, current.Sample) {
			store.latest[record.Address] = record
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan Edge Timedata history: %w", err)
	}
	return nil
}

func sampleIsNewer(next, current Sample) bool {
	if next.ObservedAt.After(current.ObservedAt) {
		return true
	}
	if next.ObservedAt.Equal(current.ObservedAt) && next.Sequence > current.Sequence {
		return true
	}
	return false
}

// RecordImage persists each new canonical sample that satisfies local persistence
// policy. Unlike OpenEMS RRD4j's five-minute local rollup, HVAC Edge deliberately
// keeps full-resolution canonical samples because Store&Forward and S2 replay must
// preserve original observations. The responsibility split (local Timedata vs
// Cloud publishing/resend) still follows OpenEMS.
func (store *FileTimedata) RecordImage(image ProcessImage) (int, error) {
	if store == nil {
		return 0, errors.New("Edge Timedata is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	channels := image.Channels()
	selected := make([]ChannelSnapshot, 0, len(channels))
	for _, channel := range channels {
		if !channel.HasValue {
			continue
		}
		descriptor := channel.Descriptor
		if descriptor.Access == AccessWriteOnly {
			continue
		}
		if !priorityAtLeast(descriptor.LocalPersistencePriority, store.minimumLocalPriority) {
			continue
		}
		if current, exists := store.latest[descriptor.Address()]; exists && current.Sample == channel.Sample {
			continue
		}
		selected = append(selected, channel)
	}
	if len(selected) == 0 {
		return 0, nil
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Descriptor.Address() < selected[j].Descriptor.Address()
	})

	file, err := os.OpenFile(store.historyPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open Edge Timedata history for append: %w", err)
	}
	written := 0
	for _, channel := range selected {
		store.nextSequence++
		record := TimedataRecord{
			Sequence: store.nextSequence,
			Address:  channel.Descriptor.Address(),
			PointID:  channel.Descriptor.PointID,
			Sample:   channel.Sample,
		}
		payload, err := json.Marshal(record)
		if err != nil {
			_ = file.Close()
			return written, err
		}
		payload = append(payload, '\n')
		if _, err := file.Write(payload); err != nil {
			_ = file.Close()
			return written, fmt.Errorf("append Edge Timedata history: %w", err)
		}
		store.latest[record.Address] = record
		written++
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return written, fmt.Errorf("sync Edge Timedata history: %w", err)
	}
	if err := file.Close(); err != nil {
		return written, fmt.Errorf("close Edge Timedata history: %w", err)
	}
	if err := store.persistLatestLocked(); err != nil {
		return written, err
	}
	return written, nil
}

func (store *FileTimedata) Latest(address string) (TimedataRecord, bool) {
	if store == nil {
		return TimedataRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.latest[strings.TrimSpace(address)]
	return record, ok
}

func (store *FileTimedata) History(address string, limit int) ([]TimedataRecord, error) {
	if store == nil {
		return nil, errors.New("Edge Timedata is nil")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("channel address is required")
	}
	if limit <= 0 {
		return nil, errors.New("history limit must be positive")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	records, err := store.readHistoryLocked(func(record TimedataRecord) bool { return record.Address == address })
	if err != nil {
		return nil, err
	}
	if len(records) > limit {
		records = append([]TimedataRecord(nil), records[len(records)-limit:]...)
	}
	return records, nil
}

// QueryRange is the local historic-query boundary used by future resend workers.
// It intentionally accepts time ranges and Channel addresses rather than exposing
// an internal transport cursor.
func (store *FileTimedata) QueryRange(from, to time.Time, addresses []string, limit int) ([]TimedataRecord, error) {
	if store == nil {
		return nil, errors.New("Edge Timedata is nil")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, errors.New("valid from/to range is required")
	}
	if limit <= 0 {
		return nil, errors.New("range query limit must be positive")
	}
	selected := map[string]struct{}{}
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address != "" {
			selected[address] = struct{}{}
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	records, err := store.readHistoryLocked(func(record TimedataRecord) bool {
		if len(selected) > 0 {
			if _, ok := selected[record.Address]; !ok {
				return false
			}
		}
		observed := record.Sample.ObservedAt
		return !observed.Before(from) && observed.Before(to)
	})
	if err != nil {
		return nil, err
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (store *FileTimedata) readHistoryLocked(include func(TimedataRecord) bool) ([]TimedataRecord, error) {
	file, err := os.Open(store.historyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	records := make([]TimedataRecord, 0)
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	for scanner.Scan() {
		var record TimedataRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		if include == nil || include(record) {
			records = append(records, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (store *FileTimedata) persistLatestLocked() error {
	addresses := make([]string, 0, len(store.latest))
	for address := range store.latest {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	records := make([]TimedataRecord, 0, len(addresses))
	for _, address := range addresses {
		records = append(records, store.latest[address])
	}
	return writeJSONAtomic(filepath.Join(store.directory, "latest.json"), records)
}

func writeJSONAtomic(path string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o600); err != nil {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_RDWR, 0o600)
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// TimedataAge is useful for diagnostics without exposing values.
func TimedataAge(record TimedataRecord, now time.Time) time.Duration {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Sub(record.Sample.ObservedAt)
}
