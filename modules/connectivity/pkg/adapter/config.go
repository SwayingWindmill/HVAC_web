package adapter

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var uuidV7Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

const (
	mqttKeepAliveSeconds     = 30
	mqttSessionExpirySeconds = 24 * 60 * 60
	mqttConnectTimeout       = 10 * time.Second
)

// Config is Connectivity's uplink configuration. It names no Tenant, Site or Gateway:
// every Gateway is resolved from the Registry when its messages arrive.
type Config struct {
	MQTT             MQTTConfig
	TelemetryRuntime TelemetryRuntimeConfig
	// QueueCapacity bounds each Gateway's in-process backlog; beyond it the broker holds
	// the Gateway's messages until Connectivity catches up.
	QueueCapacity int
}

type MQTTConfig struct {
	BrokerURL  string
	ClientID   string
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

type TelemetryRuntimeConfig struct {
	BaseURL    string
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

func (config Config) Validate() error {
	broker, err := url.Parse(strings.TrimSpace(config.MQTT.BrokerURL))
	if err != nil || broker.Scheme != "tls" || broker.Host == "" || broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" || (broker.Path != "" && broker.Path != "/") {
		return errors.New("MQTT broker URL must be a tls:// origin")
	}
	if err := validateHTTPSOrigin(config.TelemetryRuntime.BaseURL); err != nil {
		return fmt.Errorf("telemetry runtime base URL: %w", err)
	}
	for name, value := range map[string]string{
		"MQTT client id":                config.MQTT.ClientID,
		"MQTT CA file":                  config.MQTT.CAFile,
		"MQTT certificate file":         config.MQTT.CertFile,
		"MQTT key file":                 config.MQTT.KeyFile,
		"MQTT server name":              config.MQTT.ServerName,
		"telemetry runtime CA file":     config.TelemetryRuntime.CAFile,
		"telemetry runtime certificate": config.TelemetryRuntime.CertFile,
		"telemetry runtime key file":    config.TelemetryRuntime.KeyFile,
		"telemetry runtime server name": config.TelemetryRuntime.ServerName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if config.QueueCapacity < 1 || config.QueueCapacity > 65536 {
		return errors.New("queue capacity must be between 1 and 65536")
	}
	return nil
}
