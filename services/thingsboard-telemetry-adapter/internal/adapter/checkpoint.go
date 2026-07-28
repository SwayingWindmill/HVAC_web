package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type CheckpointStore struct {
	path    string
	mu      sync.Mutex
	offsets map[string]int64
}

type checkpointFile struct {
	SchemaVersion int              `json:"schemaVersion"`
	Offsets       map[string]int64 `json:"offsets"`
}

func OpenCheckpointStore(path string) (*CheckpointStore, error) {
	store := &CheckpointStore{path: path, offsets: map[string]int64{}}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read checkpoint file: %w", err)
	}
	var persisted checkpointFile
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil || ensureJSONEOF(decoder) != nil || persisted.SchemaVersion != 1 || persisted.Offsets == nil {
		return nil, errors.New("checkpoint file is invalid")
	}
	for partition, offset := range persisted.Offsets {
		if partition == "" || offset < 0 {
			return nil, errors.New("checkpoint file contains an invalid position")
		}
		store.offsets[partition] = offset
	}
	return store, nil
}

func (store *CheckpointStore) Offset(partition string) (int64, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	offset, ok := store.offsets[partition]
	return offset, ok
}

func (store *CheckpointStore) Advance(partition string, offset int64) error {
	if partition == "" || offset < 0 {
		return errors.New("checkpoint position is invalid")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, existed := store.offsets[partition]
	if existed && offset <= current {
		return nil
	}
	store.offsets[partition] = offset
	if err := store.persistLocked(); err != nil {
		if existed {
			store.offsets[partition] = current
		} else {
			delete(store.offsets, partition)
		}
		return err
	}
	return nil
}

func (store *CheckpointStore) persistLocked() error {
	content, err := json.MarshalIndent(checkpointFile{SchemaVersion: 1, Offsets: store.offsets}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode checkpoint file: %w", err)
	}
	content = append(content, '\n')
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".checkpoint-*")
	if err != nil {
		return fmt.Errorf("create checkpoint temp file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure checkpoint temp file: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write checkpoint temp file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync checkpoint temp file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close checkpoint temp file: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace checkpoint file: %w", err)
	}
	return nil
}
