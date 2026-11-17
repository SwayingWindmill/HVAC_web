package main

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultCommandMQTTClientID(t *testing.T) {
	t.Run("explicit env overrides hostname default", func(t *testing.T) {
		t.Setenv("COMMAND_MQTT_CLIENT_ID", "  custom-command-client  ")
		if got := defaultCommandMQTTClientID(); got != "custom-command-client" {
			t.Fatalf("defaultCommandMQTTClientID() = %q, want custom-command-client", got)
		}
	})

	t.Run("unset env derives sanitized hostname suffix", func(t *testing.T) {
		t.Setenv("COMMAND_MQTT_CLIENT_ID", "")
		hostname, err := os.Hostname()
		got := defaultCommandMQTTClientID()
		if err != nil || sanitizeClientID(hostname) == "" {
			if got != "connectivity-commands" {
				t.Fatalf("defaultCommandMQTTClientID() = %q, want fallback connectivity-commands", got)
			}
		} else {
			want := "connectivity-commands-" + sanitizeClientID(hostname)
			if got != want {
				t.Fatalf("defaultCommandMQTTClientID() = %q, want %q", got, want)
			}
		}
	})

	t.Run("sanitizeClientID normalizes special characters and empty fallback", func(t *testing.T) {
		for _, tc := range []struct {
			hostname string
			want     string
		}{
			{hostname: "connectivity-0", want: "connectivity-commands-connectivity-0"},
			{hostname: "pod_1.prod.internal", want: "connectivity-commands-pod_1-prod-internal"},
			{hostname: "  !!!  ", want: "connectivity-commands"},
			{hostname: "", want: "connectivity-commands"},
		} {
			if got := defaultCommandMQTTClientIDForHost(tc.hostname); got != tc.want {
				t.Fatalf("defaultCommandMQTTClientIDForHost(%q) = %q, want %q", tc.hostname, got, tc.want)
			}
		}
		longHost := strings.Repeat("a", 80)
		gotLong := defaultCommandMQTTClientIDForHost(longHost)
		if gotLong != "connectivity-commands-"+strings.Repeat("a", 64) {
			t.Fatalf("expected 64-char sanitized suffix, got %q", gotLong)
		}
	})
}

func TestCommandSharedSubscriptionGroup(t *testing.T) {
	t.Run("unset defaults to command-dispatcher", func(t *testing.T) {
		if prev, had := os.LookupEnv("COMMAND_MQTT_SHARE_GROUP"); had {
			t.Cleanup(func() { _ = os.Setenv("COMMAND_MQTT_SHARE_GROUP", prev) })
			_ = os.Unsetenv("COMMAND_MQTT_SHARE_GROUP")
		}
		if got := commandSharedSubscriptionGroup(); got != "command-dispatcher" {
			t.Fatalf("commandSharedSubscriptionGroup() = %q, want command-dispatcher", got)
		}
	})

	for _, disabled := range []string{"", "   ", "off", "OFF", "none", "NONE"} {
		t.Run("disabled_"+disabled, func(t *testing.T) {
			t.Setenv("COMMAND_MQTT_SHARE_GROUP", disabled)
			if got := commandSharedSubscriptionGroup(); got != "" {
				t.Fatalf("commandSharedSubscriptionGroup(%q) = %q, want empty", disabled, got)
			}
		})
	}

	t.Run("custom group is trimmed and preserved", func(t *testing.T) {
		t.Setenv("COMMAND_MQTT_SHARE_GROUP", " command-pool_2 ")
		if got := commandSharedSubscriptionGroup(); got != "command-pool_2" {
			t.Fatalf("commandSharedSubscriptionGroup() = %q, want command-pool_2", got)
		}
	})
}
