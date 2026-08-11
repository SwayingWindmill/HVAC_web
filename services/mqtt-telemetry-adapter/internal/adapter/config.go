package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const ConfigSchemaVersion = 1

var uuidV7Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Config struct {
	SchemaVersion           int                    `json:"schemaVersion"`
	IntegrationInstanceID   string                 `json:"integrationInstanceId"`
	MQTT                    MQTTConfig             `json:"mqtt"`
	TelemetryRuntime        TelemetryRuntimeConfig `json:"telemetryRuntime"`
	GatewayScopes           []GatewayScopeConfig   `json:"gatewayScopes"`
	ProcessingQueueCapacity int                    `json:"processingQueueCapacity"`
}

type GatewayScopeConfig struct {
	GatewayID string `json:"gatewayId"`
	TenantID  string `json:"tenantId"`
	SiteID    string `json:"siteId"`
}

type MQTTConfig struct {
	BrokerURL             string `json:"brokerUrl"`
	ClientID              string `json:"clientId"`
	TopicFilter           string `json:"topicFilter"`
	CAFile                string `json:"caFile"`
	CertFile              string `json:"certFile"`
	KeyFile               string `json:"keyFile"`
	ServerName            string `json:"serverName"`
	KeepAliveSeconds      uint16 `json:"keepAliveSeconds"`
	SessionExpirySeconds  uint32 `json:"sessionExpirySeconds"`
	ConnectTimeoutSeconds int    `json:"connectTimeoutSeconds"`
}

type TelemetryRuntimeConfig struct {
	BaseURL    string `json:"baseUrl"`
	CAFile     string `json:"caFile"`
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
	ServerName string `json:"serverName"`
}

func DecodeConfig(reader io.Reader) (Config, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode MQTT telemetry adapter config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("MQTT telemetry adapter config contains trailing JSON")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if config.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("unsupported MQTT telemetry adapter schemaVersion %d", config.SchemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(config.IntegrationInstanceID)) {
		return errors.New("integrationInstanceId must be UUIDv7")
	}
	brokerURL, err := url.Parse(strings.TrimSpace(config.MQTT.BrokerURL))
	if err != nil || brokerURL.Scheme != "tls" || brokerURL.Host == "" || brokerURL.User != nil || brokerURL.RawQuery != "" || brokerURL.Fragment != "" || (brokerURL.Path != "" && brokerURL.Path != "/") {
		return errors.New("mqtt.brokerUrl must be a tls:// origin")
	}
	if strings.TrimSpace(config.MQTT.ClientID) == "" || len(config.MQTT.ClientID) > 128 {
		return errors.New("mqtt.clientId is invalid")
	}
	if strings.TrimSpace(config.MQTT.TopicFilter) != "energy/v1/+/+/+/telemetry" {
		return errors.New("mqtt.topicFilter must be energy/v1/+/+/+/telemetry")
	}
	for name, value := range map[string]string{
		"mqtt.caFile":                 config.MQTT.CAFile,
		"mqtt.certFile":               config.MQTT.CertFile,
		"mqtt.keyFile":                config.MQTT.KeyFile,
		"mqtt.serverName":             config.MQTT.ServerName,
		"telemetryRuntime.caFile":     config.TelemetryRuntime.CAFile,
		"telemetryRuntime.certFile":   config.TelemetryRuntime.CertFile,
		"telemetryRuntime.keyFile":    config.TelemetryRuntime.KeyFile,
		"telemetryRuntime.serverName": config.TelemetryRuntime.ServerName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if config.MQTT.KeepAliveSeconds < 10 || config.MQTT.KeepAliveSeconds > 300 {
		return errors.New("mqtt.keepAliveSeconds must be between 10 and 300")
	}
	if config.MQTT.SessionExpirySeconds < 60 || config.MQTT.SessionExpirySeconds > 7*24*60*60 {
		return errors.New("mqtt.sessionExpirySeconds must be between 60 and 604800")
	}
	if config.MQTT.ConnectTimeoutSeconds < 1 || config.MQTT.ConnectTimeoutSeconds > 60 {
		return errors.New("mqtt.connectTimeoutSeconds must be between 1 and 60")
	}
	if len(config.GatewayScopes) == 0 {
		return errors.New("gatewayScopes must contain at least one Gateway scope")
	}
	seenGateways := make(map[string]struct{}, len(config.GatewayScopes))
	for _, scope := range config.GatewayScopes {
		gatewayID := strings.TrimSpace(scope.GatewayID)
		if !validGatewayID(gatewayID) || !uuidV7Pattern.MatchString(strings.TrimSpace(scope.TenantID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(scope.SiteID)) {
			return errors.New("gatewayScopes contains an invalid Gateway/Tenant/Site binding")
		}
		if _, duplicate := seenGateways[gatewayID]; duplicate {
			return fmt.Errorf("gatewayScopes contains duplicate gatewayId %s", gatewayID)
		}
		seenGateways[gatewayID] = struct{}{}
	}
	if config.ProcessingQueueCapacity < 1 || config.ProcessingQueueCapacity > 65536 {
		return errors.New("processingQueueCapacity must be between 1 and 65536")
	}
	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.TelemetryRuntime.BaseURL), "/"))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" || (baseURL.Path != "" && baseURL.Path != "/") {
		return errors.New("telemetryRuntime.baseUrl must be an HTTPS origin")
	}
	return nil
}

func (config MQTTConfig) ConnectTimeout() time.Duration {
	return time.Duration(config.ConnectTimeoutSeconds) * time.Second
}
