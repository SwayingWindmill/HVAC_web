package simulator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const measurementSequenceStateSchemaVersion = 1

type measurementSequenceState struct {
	SchemaVersion int               `json:"schemaVersion"`
	Sequences     map[string]uint64 `json:"sequences"`
}

func LoadMeasurementSequences(path string) (map[string]uint64, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("measurement sequence state path is required")
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]uint64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open measurement sequence state: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1024*1024))
	decoder.DisallowUnknownFields()
	var state measurementSequenceState
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode measurement sequence state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("measurement sequence state has trailing JSON")
	}
	if state.SchemaVersion != measurementSequenceStateSchemaVersion || state.Sequences == nil {
		return nil, errors.New("measurement sequence state is invalid")
	}
	sequences := make(map[string]uint64, len(state.Sequences))
	for key, value := range state.Sequences {
		if strings.TrimSpace(key) == "" {
			return nil, errors.New("measurement sequence state contains an empty point key")
		}
		sequences[key] = value
	}
	return sequences, nil
}

func SaveMeasurementSequences(path string, sequences map[string]uint64) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("measurement sequence state path is required")
	}
	if sequences == nil {
		return errors.New("measurement sequences are required")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create measurement sequence state directory: %w", err)
	}
	body, err := json.Marshal(measurementSequenceState{
		SchemaVersion: measurementSequenceStateSchemaVersion,
		Sequences:     sequences,
	})
	if err != nil {
		return fmt.Errorf("encode measurement sequence state: %w", err)
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create measurement sequence state: %w", err)
	}
	writeErr := func() error {
		if _, err := file.Write(append(body, '\n')); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("write measurement sequence state: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close measurement sequence state: %w", closeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("commit measurement sequence state: %w", err)
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}
