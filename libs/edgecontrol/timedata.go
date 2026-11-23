package edgecontrol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	records              []TimedataRecord
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

	reader := bufio.NewReaderSize(file, 64*1024)
	var (
		offset         int64
		validEndOffset int64
		previous       uint64
		needsTruncate  bool
		needsNewline   bool
	)
	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			hasNewline := lineBytes[len(lineBytes)-1] == '\n'
			trimmed := bytes.TrimSpace(lineBytes)
			atEOF, peekErr := atTimedataReaderEOF(reader, readErr)
			if peekErr != nil {
				_ = file.Close()
				return fmt.Errorf("read Edge Timedata history: %w", peekErr)
			}

			if len(trimmed) == 0 {
				if atEOF {
					needsTruncate = true
					break
				}
				_ = file.Close()
				return errors.New("edge timedata history contains blank line before EOF")
			}

			record, err := parseTimedataRecord(trimmed, previous)
			if err != nil {
				if atEOF {
					needsTruncate = true
					break
				}
				_ = file.Close()
				return err
			}

			previous = record.Sequence
			store.nextSequence = record.Sequence
			store.records = append(store.records, record)
			if current, exists := store.latest[record.Address]; !exists || sampleIsNewer(record.Sample, current.Sample) {
				store.latest[record.Address] = record
			}
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
			return fmt.Errorf("read Edge Timedata history: %w", readErr)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Edge Timedata history: %w", err)
	}

	if needsTruncate {
		if err := os.Truncate(store.historyPath(), validEndOffset); err != nil {
			return fmt.Errorf("truncate partial Edge Timedata history line: %w", err)
		}
	} else if needsNewline {
		appendFile, err := os.OpenFile(store.historyPath(), os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open Edge Timedata history trailing newline: %w", err)
		}
		_, writeErr := appendFile.Write([]byte{'\n'})
		syncErr := appendFile.Sync()
		closeErr := appendFile.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return fmt.Errorf("terminate Edge Timedata history trailing line: %w", err)
		}
	}
	return nil
}

func atTimedataReaderEOF(reader *bufio.Reader, readErr error) (bool, error) {
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

func parseTimedataRecord(raw []byte, previousSequence uint64) (TimedataRecord, error) {
	var record TimedataRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return TimedataRecord{}, fmt.Errorf("decode Edge Timedata history: %w", err)
	}
	if record.Sequence == 0 || record.Sequence <= previousSequence || strings.TrimSpace(record.Address) == "" || strings.TrimSpace(record.PointID) == "" {
		return TimedataRecord{}, errors.New("edge timedata history is not monotonic or is malformed")
	}
	if record.Sample.ObservedAt.IsZero() {
		return TimedataRecord{}, errors.New("edge timedata history contains sample without observed time")
	}
	return record, nil
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
		return 0, errors.New("edge timedata is nil")
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
		store.records = append(store.records, record)
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

// LookupSequence finds the Sequence for an observation at address observedAt
// strictly after afterSequence without scanning the history file.
func (store *FileTimedata) LookupSequence(address string, observedAt time.Time, afterSequence uint64) (uint64, bool) {
	if store == nil {
		return 0, false
	}
	address = strings.TrimSpace(address)
	if address == "" || observedAt.IsZero() {
		return 0, false
	}
	targetMillis := observedAt.UTC().UnixMilli()

	store.mu.Lock()
	defer store.mu.Unlock()

	if latest, ok := store.latest[address]; ok && latest.Sequence > afterSequence && latest.Sample.ObservedAt.UTC().UnixMilli() == targetMillis {
		return latest.Sequence, true
	}
	for i := len(store.records) - 1; i >= 0; i-- {
		rec := store.records[i]
		if rec.Sequence <= afterSequence {
			break
		}
		if rec.Address == address && rec.Sample.ObservedAt.UTC().UnixMilli() == targetMillis {
			return rec.Sequence, true
		}
	}
	return 0, false
}

func (store *FileTimedata) History(address string, limit int) ([]TimedataRecord, error) {
	if store == nil {
		return nil, errors.New("edge timedata is nil")
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
	var records []TimedataRecord
	for _, record := range store.records {
		if record.Address == address {
			records = append(records, record)
		}
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
		return nil, errors.New("edge timedata is nil")
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
	records := make([]TimedataRecord, 0, min(limit, len(store.records)))
	for _, record := range store.records {
		if len(selected) > 0 {
			if _, ok := selected[record.Address]; !ok {
				continue
			}
		}
		observed := record.Sample.ObservedAt
		if !observed.Before(from) && observed.Before(to) {
			records = append(records, record)
			if len(records) >= limit {
				break
			}
		}
	}
	return records, nil
}

func (store *FileTimedata) LatestSequence() uint64 {
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.nextSequence
}

// QueryAfterSequence returns up to limit ordered records with Sequence > afterSequence
// matching include, using binary search over the retained in-memory index.
func (store *FileTimedata) QueryAfterSequence(afterSequence uint64, limit int, include func(TimedataRecord) bool) ([]TimedataRecord, error) {
	if store == nil {
		return nil, errors.New("edge timedata is nil")
	}
	if limit <= 0 {
		return nil, errors.New("sequence query limit must be positive")
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	startIdx := sort.Search(len(store.records), func(i int) bool {
		return store.records[i].Sequence > afterSequence
	})
	if startIdx >= len(store.records) {
		return nil, nil
	}
	records := make([]TimedataRecord, 0, min(limit, len(store.records)-startIdx))
	for i := startIdx; i < len(store.records); i++ {
		record := store.records[i]
		if include == nil || include(record) {
			records = append(records, record)
			if len(records) >= limit {
				break
			}
		}
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

// Purge wipes all in-memory samples and deletes persisted history/snapshot files on disk.
func (store *FileTimedata) Purge() error {
	if store == nil {
		return errors.New("edge timedata is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	store.latest = map[string]TimedataRecord{}
	store.records = nil
	store.nextSequence = 0

	latestPath := filepath.Join(store.directory, "latest.json")
	var errs []error
	for _, candidate := range []string{
		store.historyPath(),
		store.historyPath() + ".tmp",
		latestPath,
		latestPath + ".tmp",
	} {
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", candidate, err))
		}
	}
	return errors.Join(errs...)
}
