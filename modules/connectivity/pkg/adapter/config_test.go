package adapter

import (
	"testing"
	"time"
)

func TestConfigValidateBatchOptions(t *testing.T) {
	base := func() Config {
		return Config{
			MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
			QueueCapacity:    8,
		}
	}

	defaultCfg := base()
	if err := defaultCfg.Validate(); err != nil {
		t.Fatalf("default config failed Validate: %v", err)
	}
	if defaultCfg.effectiveBatchMaxMessages() != DefaultBatchMaxMessages {
		t.Fatalf("effectiveBatchMaxMessages=%d, want %d", defaultCfg.effectiveBatchMaxMessages(), DefaultBatchMaxMessages)
	}
	if defaultCfg.effectiveWorkerCount() != DefaultWorkerCount {
		t.Fatalf("effectiveWorkerCount=%d, want %d", defaultCfg.effectiveWorkerCount(), DefaultWorkerCount)
	}

	maxValid := base()
	maxValid.BatchMaxMessages = MaxObservationBatchSize
	maxValid.BatchFlushWindow = time.Second
	maxValid.AutoBackfillLagThreshold = 15 * time.Minute
	maxValid.WorkerCount = maxWorkerCount
	if err := maxValid.Validate(); err != nil {
		t.Fatalf("max valid config failed Validate: %v", err)
	}
	if got := defaultCfg.MQTT.effectiveReceiveMaximum(); got != mqttDefaultReceiveMaximum {
		t.Fatalf("effectiveReceiveMaximum()=%d, want %d", got, mqttDefaultReceiveMaximum)
	}
	customRM := MQTTConfig{ReceiveMaximum: 64}
	if got := customRM.effectiveReceiveMaximum(); got != 64 {
		t.Fatalf("custom effectiveReceiveMaximum()=%d, want 64", got)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"negative batch max", func(c *Config) { c.BatchMaxMessages = -1 }},
		{"batch max above 512", func(c *Config) { c.BatchMaxMessages = MaxObservationBatchSize + 1 }},
		{"negative flush window", func(c *Config) { c.BatchFlushWindow = -time.Millisecond }},
		{"flush window above 1s", func(c *Config) { c.BatchFlushWindow = time.Second + time.Millisecond }},
		{"negative auto backfill lag", func(c *Config) { c.AutoBackfillLagThreshold = -time.Millisecond }},
		{"negative worker count", func(c *Config) { c.WorkerCount = -1 }},
		{"worker count above 1024", func(c *Config) { c.WorkerCount = maxWorkerCount + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := base()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("%s unexpectedly passed Validate", test.name)
			}
		})
	}
}

func TestConfigSharedSubscriptionAndOverflowValidation(t *testing.T) {
	base := func() Config {
		return Config{
			MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
			QueueCapacity:    8,
		}
	}

	plain := base()
	plainFilters := plain.SubscriptionTopicFilters()
	if len(plainFilters) != 3 ||
		plainFilters[0] != "hvac/v1/+/up/telemetry" ||
		plainFilters[1] != "hvac/v1/+/up/event" ||
		plainFilters[2] != "hvac/v1/+/up/status" {
		t.Fatalf("plain SubscriptionTopicFilters=%v", plainFilters)
	}
	if plain.effectiveOverflowMaxMessages() != 4096 {
		t.Fatalf("effectiveOverflowMaxMessages(q=8)=%d, want 4096", plain.effectiveOverflowMaxMessages())
	}
	if plain.effectiveOverflowMaxBytes() != 32<<20 {
		t.Fatalf("effectiveOverflowMaxBytes()=%d, want %d", plain.effectiveOverflowMaxBytes(), 32<<20)
	}

	largeQueue := base()
	largeQueue.QueueCapacity = 512
	if largeQueue.effectiveOverflowMaxMessages() != 8192 {
		t.Fatalf("effectiveOverflowMaxMessages(q=512)=%d, want 8192", largeQueue.effectiveOverflowMaxMessages())
	}

	shared := base()
	shared.MQTT.SharedSubscriptionGroup = "connectivity-pool_1"
	if err := shared.Validate(); err != nil {
		t.Fatalf("valid shared config failed Validate: %v", err)
	}
	sharedFilters := shared.SubscriptionTopicFilters()
	if len(sharedFilters) != 3 ||
		sharedFilters[0] != "$share/connectivity-pool_1/hvac/v1/+/up/telemetry" ||
		sharedFilters[1] != "$share/connectivity-pool_1/hvac/v1/+/up/event" ||
		sharedFilters[2] != "$share/connectivity-pool_1/hvac/v1/+/up/status" {
		t.Fatalf("shared SubscriptionTopicFilters=%v", sharedFilters)
	}

	if got := TopicGatewayID("$share/connectivity/hvac/v1/" + testGatewayA + "/up/telemetry"); got != testGatewayA {
		t.Fatalf("TopicGatewayID with $share prefix = %q, want %q", got, testGatewayA)
	}
	if got := TopicGatewayID("hvac/v1/" + testGatewayA + "/up/event"); got != testGatewayA {
		t.Fatalf("TopicGatewayID without $share = %q, want %q", got, testGatewayA)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"invalid share group char", func(c *Config) { c.MQTT.SharedSubscriptionGroup = "pool/1" }},
		{"too long share group", func(c *Config) {
			c.MQTT.SharedSubscriptionGroup = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{"negative overflow max", func(c *Config) { c.OverflowMaxMessages = -1 }},
		{"overflow max above limit", func(c *Config) { c.OverflowMaxMessages = 1048577 }},
		{"negative overflow max bytes", func(c *Config) { c.OverflowMaxBytes = -1 }},
		{"overflow max bytes above limit", func(c *Config) { c.OverflowMaxBytes = (1 << 30) + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := base()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("%s unexpectedly passed Validate", test.name)
			}
		})
	}
}
