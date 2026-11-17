package main

import (
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

func TestCredentialServerRejectsNonLoopbackTunnelAddressFailClosed(t *testing.T) {
	t.Setenv("CONNECTIVITY_TUNNEL_LOOPBACK_ADDR", "0.0.0.0:18800")
	_, _, err := credentialServer(nil)
	if !errors.Is(err, tunnel.ErrNonLoopbackAddress) {
		t.Fatalf("expected ErrNonLoopbackAddress for 0.0.0.0:18800, got %v", err)
	}
}

func TestUplinkBatchConfigFromEnv(t *testing.T) {
	defaults := uplinkConfig()
	if err := defaults.Validate(); err != nil {
		t.Fatalf("default uplinkConfig invalid: %v", err)
	}
	if defaults.BatchMaxMessages != adapter.DefaultBatchMaxMessages || defaults.BatchFlushWindow != 0 || defaults.WorkerCount != adapter.DefaultWorkerCount {
		t.Fatalf("defaults=%#v", defaults)
	}

	t.Setenv("CONNECTIVITY_BATCH_MAX_MESSAGES", "512")
	t.Setenv("CONNECTIVITY_BATCH_WINDOW", "250ms")
	t.Setenv("CONNECTIVITY_UPLINK_WORKERS", "16")
	valid := uplinkConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid bounded uplinkConfig failed: %v", err)
	}
	if valid.BatchMaxMessages != 512 || valid.BatchFlushWindow != 250*time.Millisecond || valid.WorkerCount != 16 {
		t.Fatalf("valid=%#v", valid)
	}

	for _, invalid := range []struct {
		name string
		key  string
		val  string
	}{
		{"zero batch max", "CONNECTIVITY_BATCH_MAX_MESSAGES", "0"},
		{"above max batch", "CONNECTIVITY_BATCH_MAX_MESSAGES", "513"},
		{"non-numeric batch max", "CONNECTIVITY_BATCH_MAX_MESSAGES", "abc"},
		{"negative flush window", "CONNECTIVITY_BATCH_WINDOW", "-1ms"},
		{"above max flush window", "CONNECTIVITY_BATCH_WINDOW", "1001ms"},
		{"invalid flush window", "CONNECTIVITY_BATCH_WINDOW", "not-a-duration"},
		{"zero uplink workers", "CONNECTIVITY_UPLINK_WORKERS", "0"},
		{"above max uplink workers", "CONNECTIVITY_UPLINK_WORKERS", "1025"},
		{"non-numeric uplink workers", "CONNECTIVITY_UPLINK_WORKERS", "abc"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			t.Setenv(invalid.key, invalid.val)
			if err := uplinkConfig().Validate(); err == nil {
				t.Fatalf("%s (%s=%q) unexpectedly passed Validate", invalid.name, invalid.key, invalid.val)
			}
		})
	}
}
