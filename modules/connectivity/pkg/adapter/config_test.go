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
	maxValid.WorkerCount = maxWorkerCount
	if err := maxValid.Validate(); err != nil {
		t.Fatalf("max valid config failed Validate: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"negative batch max", func(c *Config) { c.BatchMaxMessages = -1 }},
		{"batch max above 512", func(c *Config) { c.BatchMaxMessages = MaxObservationBatchSize + 1 }},
		{"negative flush window", func(c *Config) { c.BatchFlushWindow = -time.Millisecond }},
		{"flush window above 1s", func(c *Config) { c.BatchFlushWindow = time.Second + time.Millisecond }},
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
