package main

import (
	"bytes"
	"fmt"
	"net"
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

func TestHealthCheckDoesNotCollideWithLiveHealthPortOrStateDir(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	dir := t.TempDir()
	liveStateDir := filepath.Join(dir, "live-state")
	liveOTADir := filepath.Join(dir, "live-ota")
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := fmt.Sprintf(`{
		"gatewayId": "0193f000-2000-7000-8000-000000000300",
		"stateDir": %q,
		"platformBaseUrl": "https://127.0.0.1:1",
		"mqttBrokerUrl": "tls://127.0.0.1:1",
		"healthListenAddr": %q,
		"tunnel": {"enabled": true, "allowedTargetPorts": [22]},
		"ota": {"enabled": true, "dir": %q},
		"songyaxuanPlant": {"enabled": true, "serialPort": "/dev/ttyNonExistentOTAProbe"}
	}`, filepath.ToSlash(liveStateDir), ln.Addr().String(), filepath.ToSlash(liveOTADir))
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := run([]string{"--health-check", "--config", cfgPath}, &out, &errOut); err != nil {
		t.Fatalf("--health-check failed while live health port occupied: %v (stderr=%s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "health check passed") {
		t.Fatalf("unexpected --health-check output: %q", out.String())
	}
	for _, untouched := range []string{liveStateDir, liveOTADir} {
		if _, statErr := os.Stat(untouched); !os.IsNotExist(statErr) {
			t.Fatalf("expected --health-check to leave %s untouched, stat err=%v", untouched, statErr)
		}
	}
}
