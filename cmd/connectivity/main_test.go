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

func TestUplinkSharedSubscriptionAndOverflowConfigFromEnv(t *testing.T) {
	defaults := uplinkConfig()
	if err := defaults.Validate(); err != nil {
		t.Fatalf("default uplinkConfig invalid: %v", err)
	}
	if defaults.MQTT.SharedSubscriptionGroup != "connectivity" {
		t.Fatalf("default SharedSubscriptionGroup = %q, want connectivity", defaults.MQTT.SharedSubscriptionGroup)
	}
	if defaults.OverflowMaxMessages != 0 {
		t.Fatalf("default OverflowMaxMessages = %d, want 0 (defaulting in NewRuntime)", defaults.OverflowMaxMessages)
	}

	for _, disabled := range []string{"", "off", "OFF", "none", "NONE"} {
		t.Setenv("CONNECTIVITY_MQTT_SHARE_GROUP", disabled)
		cfg := uplinkConfig()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("share group %q invalid: %v", disabled, err)
		}
		if cfg.MQTT.SharedSubscriptionGroup != "" {
			t.Fatalf("share group %q -> %q, want empty", disabled, cfg.MQTT.SharedSubscriptionGroup)
		}
	}

	t.Setenv("CONNECTIVITY_MQTT_SHARE_GROUP", "connectivity-pool_1")
	t.Setenv("CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "8192")
	t.Setenv("CONNECTIVITY_OVERFLOW_MAX_BYTES", "65536")
	t.Setenv("CONNECTIVITY_OVERFLOW_DIR", "/tmp/hvac-overflow-test")
	custom := uplinkConfig()
	if err := custom.Validate(); err != nil {
		t.Fatalf("custom config invalid: %v", err)
	}
	if custom.MQTT.SharedSubscriptionGroup != "connectivity-pool_1" || custom.OverflowMaxMessages != 8192 || custom.OverflowMaxBytes != 65536 || custom.OverflowDir != "/tmp/hvac-overflow-test" {
		t.Fatalf("custom=%#v", custom)
	}

	for _, invalid := range []struct {
		name string
		key  string
		val  string
	}{
		{"invalid share group slash", "CONNECTIVITY_MQTT_SHARE_GROUP", "bad/group"},
		{"invalid share group plus", "CONNECTIVITY_MQTT_SHARE_GROUP", "bad+group"},
		{"zero overflow max", "CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "0"},
		{"overflow max above limit", "CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "1048577"},
		{"non-numeric overflow max", "CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "abc"},
		{"zero overflow max bytes", "CONNECTIVITY_OVERFLOW_MAX_BYTES", "0"},
		{"overflow max bytes above limit", "CONNECTIVITY_OVERFLOW_MAX_BYTES", "1073741825"},
		{"non-numeric overflow max bytes", "CONNECTIVITY_OVERFLOW_MAX_BYTES", "abc"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			t.Setenv(invalid.key, invalid.val)
			if err := uplinkConfig().Validate(); err == nil {
				t.Fatalf("%s (%s=%q) unexpectedly passed Validate", invalid.name, invalid.key, invalid.val)
			}
		})
	}
}
