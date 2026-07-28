package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointAdvanceRollsBackMemoryWhenPersistenceFails(t *testing.T) {
	directory := t.TempDir()
	blockedParent := filepath.Join(directory, "checkpoint-parent")
	if err := os.Mkdir(blockedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenCheckpointStore(filepath.Join(blockedParent, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blockedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Advance("thingsboard:device:key", 1000); err == nil {
		t.Fatal("expected checkpoint persistence failure")
	}
	if _, ok := store.Offset("thingsboard:device:key"); ok {
		t.Fatal("failed persistence left an in-memory checkpoint")
	}
}
