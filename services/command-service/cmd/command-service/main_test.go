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

func TestLoadRuntimeHTTPConfigFromExplicitCohortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-cohorts.json")
	body := `{
  "schemaVersion": 1,
  "cohorts": [
    {
      "dispatcherSpiffe": "spiffe://hvac.local/command-dispatcher/ahu-01",
      "verifierSpiffe": "spiffe://hvac.local/command-verifier/ahu-01",
      "organizationId": "018f3e00-0000-7000-8000-000000000001",
      "siteId": "018f3e00-1000-7000-8000-000000000001",
      "deviceId": "018f3e00-3000-7000-8000-000000000001"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMMAND_RUNTIME_COHORTS_FILE", path)
	config, err := loadRuntimeHTTPConfig(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Cohorts) != 1 || config.Cohorts[0].DeviceID != "018f3e00-3000-7000-8000-000000000001" || config.DispatcherSPIFFE != "" {
		t.Fatalf("config=%#v", config)
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
