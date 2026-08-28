package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteAtomicCreatesRestrictedParentDirectory(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "nested", "evidence", "comparison.json")
	value := []byte("{\"status\":\"passed\"}\n")

	if err := writeAtomic(output, value); err != nil {
		t.Fatalf("writeAtomic returned error: %v", err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(actual) != string(value) {
		t.Fatalf("output mismatch: got %q want %q", actual, value)
	}
	info, err := os.Stat(filepath.Dir(output))
	if err != nil {
		t.Fatalf("stat output directory: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("output directory is not owner-only: mode=%#o", info.Mode().Perm())
	}
}
