package simulator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

const MQTTGatewayConfigSchemaVersion = 2

var uuidV7Pattern = regexp.MustCompile("(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")

type MQTTGatewayConfig struct {
	SchemaVersion              int               `json:"schemaVersion"`
	TenantID                   string            `json:"tenantId"`
	SiteID                     string            `json:"siteId"`
	BrokerURL                  string            `json:"brokerUrl"`
	ClientID                   string            `json:"clientId"`
	CAFile                     string            `json:"caFile"`
	CertFile                   string            `json:"certFile"`
	KeyFile                    string            `json:"keyFile"`
	ServerName                 string            `json:"serverName"`
	QueueDirectory             string            `json:"queueDirectory"`
	MaximumQueueBytes          int64             `json:"maximumQueueBytes"`
	CredentialRevision         uint64            `json:"credentialRevision"`
	FleetReleaseKeyID          string            `json:"fleetReleaseKeyId"`
	FleetReleasePublicKeyFile  string            `json:"fleetReleasePublicKeyFile"`
	DeviceExternalIDByDeviceID map[string]string `json:"deviceExternalIdByDeviceId"`
}

func DecodeMQTTGatewayConfig(reader io.Reader) (MQTTGatewayConfig, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var config MQTTGatewayConfig
	if err := decoder.Decode(&config); err != nil {
		return MQTTGatewayConfig{}, fmt.Errorf("decode MQTT gateway config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return MQTTGatewayConfig{}, errors.New("MQTT gateway config contains trailing JSON")
	}
	if err := config.Validate(); err != nil {
		return MQTTGatewayConfig{}, err
	}
	return config, nil
}

func (config MQTTGatewayConfig) Validate() error {
	if config.SchemaVersion != MQTTGatewayConfigSchemaVersion {
		return fmt.Errorf("unsupported MQTT gateway config schemaVersion %d", config.SchemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(config.TenantID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(config.SiteID)) {
		return errors.New("MQTT gateway tenantId and siteId must be UUIDv7")
	}
	broker, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil || broker.Scheme != "tls" || broker.Host == "" || broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" || (broker.Path != "" && broker.Path != "/") {
		return errors.New("MQTT gateway brokerUrl must be a tls:// origin")
	}
	for name, value := range map[string]string{
		"clientId":                  config.ClientID,
		"caFile":                    config.CAFile,
		"certFile":                  config.CertFile,
		"keyFile":                   config.KeyFile,
		"serverName":                config.ServerName,
		"queueDirectory":            config.QueueDirectory,
		"fleetReleaseKeyId":         config.FleetReleaseKeyID,
		"fleetReleasePublicKeyFile": config.FleetReleasePublicKeyFile,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("MQTT gateway %s is required", name)
		}
	}
	if config.MaximumQueueBytes < 1<<20 || config.MaximumQueueBytes > 100<<30 {
		return errors.New("MQTT gateway maximumQueueBytes must be between 1 MiB and 100 GiB")
	}
	if config.CredentialRevision == 0 {
		return errors.New("MQTT gateway credentialRevision must be positive")
	}
	if len(config.DeviceExternalIDByDeviceID) == 0 {
		return errors.New("MQTT gateway deviceExternalIdByDeviceId is required")
	}
	seenExternal := make(map[string]struct{}, len(config.DeviceExternalIDByDeviceID))
	for deviceID, externalID := range config.DeviceExternalIDByDeviceID {
		if strings.TrimSpace(deviceID) == "" || !uuidV7Pattern.MatchString(strings.TrimSpace(externalID)) {
			return errors.New("MQTT gateway device identity mapping is invalid")
		}
		if _, duplicate := seenExternal[externalID]; duplicate {
			return fmt.Errorf("MQTT gateway externalId %s is mapped more than once", externalID)
		}
		seenExternal[externalID] = struct{}{}
	}
	return nil
}
