package adapter

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/mqttclient"
)

var (
	uuidV7Pattern  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	zeroTraceID    = strings.Repeat("0", 32)
)

const (
	mqttKeepAliveSeconds     = 30
	mqttSessionExpirySeconds = 24 * 60 * 60
	mqttConnectTimeout       = 10 * time.Second
	// DefaultBatchMaxMessages is the per-Gateway batch drain cap when BatchMaxMessages is 0.
	DefaultBatchMaxMessages = 200
	maxBatchFlushWindow     = time.Second
	// DefaultWorkerCount is the uplink worker pool size when WorkerCount is 0.
	DefaultWorkerCount      = 32
	maxWorkerCount          = 1024
	maxOverflowMessages     = 1048576
	defaultOverflowMaxBytes = int64(32 << 20)
	maxOverflowBytes        = int64(1 << 30)
)

// Config is Connectivity's uplink configuration. It names no Tenant, Site or Gateway:
// every Gateway is resolved from the Registry when its messages arrive.
type Config struct {
	MQTT             MQTTConfig
	TelemetryRuntime TelemetryRuntimeConfig
	// QueueCapacity bounds each Gateway's in-RAM mailbox before spilling to disk overflow.
	QueueCapacity int
	// OverflowMaxMessages bounds each Gateway's disk overflow spool (<= 0 defaults to
	// max(QueueCapacity*16, 4096) in NewRuntime).
	OverflowMaxMessages int
	// OverflowMaxBytes bounds unconsumed WAL bytes per Gateway (<= 0 defaults to 32 MiB).
	OverflowMaxBytes int64
	// OverflowDir stores per-Gateway disk overflow spools; when empty, Runtime.Run
	// creates a private temporary directory and removes it on shutdown.
	OverflowDir      string
	BatchMaxMessages int
	BatchFlushWindow time.Duration
	WorkerCount      int
}

type MQTTConfig struct {
	BrokerURL               string
	ClientID                string
	SharedSubscriptionGroup string
	CAFile                  string
	CertFile                string
	KeyFile                 string
	ServerName              string
}

type TelemetryRuntimeConfig struct {
	BaseURL    string
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

func (config Config) Validate() error {
	return config.validate(true)
}

func (config Config) validate(requireMQTT bool) error {
	if requireMQTT {
		broker, err := url.Parse(strings.TrimSpace(config.MQTT.BrokerURL))
		if err != nil || broker.Scheme != "tls" || broker.Host == "" || broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" || (broker.Path != "" && broker.Path != "/") {
			return errors.New("MQTT broker URL must be a tls:// origin")
		}
		for name, value := range map[string]string{
			"MQTT client id":        config.MQTT.ClientID,
			"MQTT CA file":          config.MQTT.CAFile,
			"MQTT certificate file": config.MQTT.CertFile,
			"MQTT key file":         config.MQTT.KeyFile,
			"MQTT server name":      config.MQTT.ServerName,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s is required", name)
			}
		}
	}
	if err := validateHTTPSOrigin(config.TelemetryRuntime.BaseURL); err != nil {
		return fmt.Errorf("telemetry runtime base URL: %w", err)
	}
	for name, value := range map[string]string{
		"telemetry runtime CA file":     config.TelemetryRuntime.CAFile,
		"telemetry runtime certificate": config.TelemetryRuntime.CertFile,
		"telemetry runtime key file":    config.TelemetryRuntime.KeyFile,
		"telemetry runtime server name": config.TelemetryRuntime.ServerName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := mqttclient.ValidateShareGroup(config.MQTT.SharedSubscriptionGroup); err != nil {
		return errors.New("MQTT shared subscription group must match ^[A-Za-z0-9_-]{1,64}$")
	}
	if config.QueueCapacity < 1 || config.QueueCapacity > 65536 {
		return errors.New("queue capacity must be between 1 and 65536")
	}
	if config.OverflowMaxMessages < 0 || config.OverflowMaxMessages > maxOverflowMessages {
		return fmt.Errorf("overflow max messages must be between 0 and %d", maxOverflowMessages)
	}
	if config.OverflowMaxBytes < 0 || config.OverflowMaxBytes > maxOverflowBytes {
		return fmt.Errorf("overflow max bytes must be between 0 and %d", maxOverflowBytes)
	}
	if config.BatchMaxMessages < 0 || config.BatchMaxMessages > MaxObservationBatchSize {
		return fmt.Errorf("batch max messages must be between 0 and %d", MaxObservationBatchSize)
	}
	if config.BatchFlushWindow < 0 || config.BatchFlushWindow > maxBatchFlushWindow {
		return fmt.Errorf("batch flush window must be between 0 and %s", maxBatchFlushWindow)
	}
	if config.WorkerCount < 0 || config.WorkerCount > maxWorkerCount {
		return fmt.Errorf("worker count must be between 0 and %d", maxWorkerCount)
	}
	return nil
}

// SubscriptionTopicFilters returns the MQTT v5 subscription filters, adding the
// $share/<group>/ prefix when SharedSubscriptionGroup is configured.
func (config Config) SubscriptionTopicFilters() []string {
	return mqttclient.SharedTopicFilters(config.MQTT.SharedSubscriptionGroup, UplinkTopicFilters)
}

func (config Config) effectiveOverflowMaxMessages() int {
	if config.OverflowMaxMessages <= 0 {
		return max(config.QueueCapacity*16, 4096)
	}
	return config.OverflowMaxMessages
}

func (config Config) effectiveOverflowMaxBytes() int64 {
	if config.OverflowMaxBytes <= 0 {
		return defaultOverflowMaxBytes
	}
	return config.OverflowMaxBytes
}

func (config Config) effectiveBatchMaxMessages() int {
	if config.BatchMaxMessages <= 0 {
		return DefaultBatchMaxMessages
	}
	return config.BatchMaxMessages
}

func (config Config) effectiveWorkerCount() int {
	if config.WorkerCount <= 0 {
		return DefaultWorkerCount
	}
	return config.WorkerCount
}

func normalizeTraceID(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if !traceIDPattern.MatchString(trimmed) || trimmed == zeroTraceID {
		return ""
	}
	return trimmed
}
