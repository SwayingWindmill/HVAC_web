package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequiredValueFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-value")
	if err := os.WriteFile(path, []byte("database-reference-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := loadRequiredValueFile(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if value != "database-reference-fixture" {
		t.Fatalf("value=%q", value)
	}
}

func TestLoadRequiredValueFileRejectsInvalidContentAndSize(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		maximum int64
	}{
		{name: "empty", content: "", maximum: 1024},
		{name: "multiple-lines", content: "first\nsecond", maximum: 1024},
		{name: "nul", content: "first\x00second", maximum: 1024},
		{name: "oversized", content: strings.Repeat("x", 32), maximum: 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime-value")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadRequiredValueFile(path, test.maximum); err == nil {
				t.Fatal("expected value file to fail closed")
			}
		})
	}
}
