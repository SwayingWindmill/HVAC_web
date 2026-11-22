package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIVersionValidateAndHealthCheck(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--version"}, &out, &errOut); err != nil {
		t.Fatalf("--version failed: %v", err)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("--version = %q, want %q", out.String(), version)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"gatewayId":"0193f000-2000-7000-8000-000000000300","stateDir":"` + filepath.ToSlash(filepath.Join(dir, "state")) + `"}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if err := run([]string{"--validate-config", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("--validate-config failed: %v", err)
	}
	if !strings.Contains(out.String(), "config valid") {
		t.Fatalf("unexpected --validate-config output: %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if err := run([]string{"--health-check", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("--health-check failed: %v", err)
	}
	if !strings.Contains(out.String(), "health check passed") {
		t.Fatalf("unexpected --health-check output: %q", out.String())
	}
}
