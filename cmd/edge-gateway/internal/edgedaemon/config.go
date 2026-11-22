package edgedaemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
)

const (
	defaultCycleIntervalMS             = 1000
	defaultShadowPollIntervalSec       = 60
	defaultCertRenewIntervalSec        = 43200
	defaultCertRenewThresholdDays      = 14
	defaultTunnelPingIntervalSec       = 25
	defaultSpoolMaxBytes               = 16 << 20
	minSpoolMaxBytes                   = 64 << 10
	defaultOTAHealthTimeoutSec         = 60
	defaultOTAPollIntervalSec          = 60
	defaultRTUBaudRate            uint = 9600
	defaultRTUDataBits            uint = 8
	defaultRTUStopBits            uint = 1
)

// SerialBusConfig configures UART framing for an RS-485 bus device such as /dev/ttyAS3.
type SerialBusConfig struct {
	Speed    uint   `json:"speed"`
	DataBits uint   `json:"dataBits"`
	Parity   string `json:"parity"`
	StopBits uint   `json:"stopBits"`
}

// TunnelConfig configures the outbound reverse tunnel client on the gateway.
type TunnelConfig struct {
	Enabled            bool     `json:"enabled"`
	AllowedTargetPorts []uint16 `json:"allowedTargetPorts,omitempty"`
	PingIntervalSec    int      `json:"pingIntervalSec,omitempty"`
}

// OTAConfig configures A/B artifact verification, slots, and boot health rollbacks.
type OTAConfig struct {
	Enabled                      bool              `json:"enabled"`
	Dir                          string            `json:"dir,omitempty"`
	PollIntervalSec              int               `json:"pollIntervalSec,omitempty"`
	TrustedPublicKeys            map[string]string `json:"trustedPublicKeys,omitempty"`
	DefaultHealthCheckTimeoutSec int               `json:"defaultHealthCheckTimeoutSec,omitempty"`
}

// Config defines the runtime configuration for the standalone Linux edge daemon.
type Config struct {
	GatewayID                 string                            `json:"gatewayId"`
	SiteID                    string                            `json:"siteId,omitempty"`
	HardwareModel             string                            `json:"hardwareModel,omitempty"`
	RuntimeVersion            string                            `json:"runtimeVersion,omitempty"`
	TargetOS                  string                            `json:"targetOs,omitempty"`
	TargetArch                string                            `json:"targetArch,omitempty"`
	StateDir                  string                            `json:"stateDir"`
	SpoolMaxBytes             int64                             `json:"spoolMaxBytes,omitempty"`
	PlatformBaseURL           string                            `json:"platformBaseUrl,omitempty"`
	MQTTBrokerURL             string                            `json:"mqttBrokerUrl,omitempty"`
	ServerName                string                            `json:"serverName,omitempty"`
	CAFile                    string                            `json:"caFile,omitempty"`
	MQTTServerName            string                            `json:"mqttServerName,omitempty"`
	MQTTCAFile                string                            `json:"mqttCaFile,omitempty"`
	CertFile                  string                            `json:"certFile,omitempty"`
	KeyFile                   string                            `json:"keyFile,omitempty"`
	HealthListenAddr          string                            `json:"healthListenAddr,omitempty"`
	CycleIntervalMS           int                               `json:"cycleIntervalMs,omitempty"`
	ShadowPollIntervalSec     int                               `json:"shadowPollIntervalSec,omitempty"`
	CertRenewCheckIntervalSec int                               `json:"certRenewCheckIntervalSec,omitempty"`
	CertRenewThresholdDays    int                               `json:"certRenewThresholdDays,omitempty"`
	Tunnel                    TunnelConfig                      `json:"tunnel"`
	OTA                       OTAConfig                         `json:"ota"`
	SerialBuses               map[string]SerialBusConfig        `json:"serialBuses,omitempty"`
	Drivers                   []edgecontrol.ModbusDriverInput   `json:"drivers,omitempty"`
	AutonomyPolicy            *edgecontrol.AutonomyPolicyConfig `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle            *edgecontrol.EdgeRuleBundle       `json:"edgeRuleBundle,omitempty"`
}

// LoadConfig reads, decodes strictly, applies defaults, and validates an edge daemon Config file.
func LoadConfig(path string, defaultVersion string) (Config, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return Config{}, errors.New("config file path is required")
	}
	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return Config{}, fmt.Errorf("read edge-gateway config %s: %w", trimmedPath, err)
	}
	return ParseConfig(raw, defaultVersion)
}

// ParseConfig parses JSON config bytes, rejects unknown fields, applies defaults, and validates.
func ParseConfig(raw []byte, defaultVersion string) (Config, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode edge-gateway config: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("edge-gateway config contains trailing JSON")
	}
	cfg.ApplyDefaults(defaultVersion)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ApplyDefaults populates safe production defaults for omitted optional fields.
func (c *Config) ApplyDefaults(defaultVersion string) {
	if c == nil {
		return
	}
	if strings.TrimSpace(c.HardwareModel) == "" {
		c.HardwareModel = "EG8200"
	}
	if strings.TrimSpace(c.RuntimeVersion) == "" {
		if strings.TrimSpace(defaultVersion) != "" {
			c.RuntimeVersion = strings.TrimSpace(defaultVersion)
		} else {
			c.RuntimeVersion = "0.1.0"
		}
	}
	if strings.TrimSpace(c.TargetOS) == "" {
		c.TargetOS = runtime.GOOS
	}
	c.TargetArch = edgecontrol.NormalizeTargetArch(firstNonEmpty(c.TargetArch, runtime.GOARCH))
	if c.SpoolMaxBytes == 0 {
		c.SpoolMaxBytes = defaultSpoolMaxBytes
	}
	if c.CycleIntervalMS == 0 {
		c.CycleIntervalMS = defaultCycleIntervalMS
	}
	if c.ShadowPollIntervalSec == 0 {
		c.ShadowPollIntervalSec = defaultShadowPollIntervalSec
	}
	if c.CertRenewCheckIntervalSec == 0 {
		c.CertRenewCheckIntervalSec = defaultCertRenewIntervalSec
	}
	if c.CertRenewThresholdDays == 0 {
		c.CertRenewThresholdDays = defaultCertRenewThresholdDays
	}
	if c.Tunnel.Enabled {
		if len(c.Tunnel.AllowedTargetPorts) == 0 {
			c.Tunnel.AllowedTargetPorts = []uint16{tunnel.SSHTargetPort}
		}
		if c.Tunnel.PingIntervalSec == 0 {
			c.Tunnel.PingIntervalSec = defaultTunnelPingIntervalSec
		}
	}
	if strings.TrimSpace(c.OTA.Dir) == "" && strings.TrimSpace(c.StateDir) != "" {
		c.OTA.Dir = filepath.Join(strings.TrimSpace(c.StateDir), "ota")
	}
	if c.OTA.PollIntervalSec == 0 {
		c.OTA.PollIntervalSec = defaultOTAPollIntervalSec
	}
	if c.OTA.DefaultHealthCheckTimeoutSec == 0 {
		c.OTA.DefaultHealthCheckTimeoutSec = defaultOTAHealthTimeoutSec
	}
}

// Validate enforces all fail-closed configuration invariants.
func (c Config) Validate() error {
	if strings.TrimSpace(c.GatewayID) == "" {
		return errors.New("edge-gateway config requires gatewayId")
	}
	if strings.TrimSpace(c.StateDir) == "" {
		return errors.New("edge-gateway config requires stateDir")
	}
	if c.SpoolMaxBytes < minSpoolMaxBytes {
		return fmt.Errorf("spoolMaxBytes %d must be >= %d", c.SpoolMaxBytes, minSpoolMaxBytes)
	}
	if c.CycleIntervalMS < 200 || c.CycleIntervalMS > 60000 {
		return fmt.Errorf("cycleIntervalMs %d must be in [200,60000]", c.CycleIntervalMS)
	}
	if c.ShadowPollIntervalSec < 1 || c.ShadowPollIntervalSec > 86400 {
		return fmt.Errorf("shadowPollIntervalSec %d must be in [1,86400]", c.ShadowPollIntervalSec)
	}
	if c.CertRenewCheckIntervalSec < 60 {
		return fmt.Errorf("certRenewCheckIntervalSec %d must be >= 60", c.CertRenewCheckIntervalSec)
	}
	if c.CertRenewThresholdDays < 1 || c.CertRenewThresholdDays > 90 {
		return fmt.Errorf("certRenewThresholdDays %d must be in [1,90]", c.CertRenewThresholdDays)
	}
	if strings.TrimSpace(c.HealthListenAddr) != "" {
		if err := tunnel.ValidateLoopbackAddr(c.HealthListenAddr); err != nil {
			return fmt.Errorf("healthListenAddr: %w", err)
		}
	}
	if strings.TrimSpace(c.PlatformBaseURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.PlatformBaseURL))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || strings.TrimSpace(u.Host) == "" {
			return fmt.Errorf("invalid platformBaseUrl %q", c.PlatformBaseURL)
		}
	}
	if strings.TrimSpace(c.MQTTBrokerURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.MQTTBrokerURL))
		if err != nil || strings.TrimSpace(u.Host) == "" {
			return fmt.Errorf("invalid mqttBrokerUrl %q", c.MQTTBrokerURL)
		}
	}
	if c.Tunnel.Enabled {
		if strings.TrimSpace(c.PlatformBaseURL) == "" {
			return errors.New("tunnel.enabled requires platformBaseUrl")
		}
		for _, port := range c.Tunnel.AllowedTargetPorts {
			if !tunnel.IsSupportedTargetPort(port) {
				return fmt.Errorf("tunnel.allowedTargetPorts contains unsupported port %d", port)
			}
		}
		if c.Tunnel.PingIntervalSec < 5 || c.Tunnel.PingIntervalSec > 120 {
			return fmt.Errorf("tunnel.pingIntervalSec %d must be in [5,120]", c.Tunnel.PingIntervalSec)
		}
	}
	for keyID, keyB64 := range c.OTA.TrustedPublicKeys {
		if strings.TrimSpace(keyID) == "" {
			return errors.New("ota.trustedPublicKeys keys must be non-empty")
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("ota.trustedPublicKeys[%q] must be a base64 32-byte Ed25519 public key", keyID)
		}
	}
	for dev, bus := range c.SerialBuses {
		if strings.TrimSpace(dev) == "" {
			return errors.New("serialBuses device path must be non-empty")
		}
		if bus.DataBits != 0 && bus.DataBits != 7 && bus.DataBits != 8 {
			return fmt.Errorf("serialBuses[%q].dataBits must be 7 or 8", dev)
		}
		if bus.StopBits != 0 && bus.StopBits != 1 && bus.StopBits != 2 {
			return fmt.Errorf("serialBuses[%q].stopBits must be 1 or 2", dev)
		}
		switch strings.ToUpper(strings.TrimSpace(bus.Parity)) {
		case "", "N", "NONE", "E", "EVEN", "O", "ODD":
		default:
			return fmt.Errorf("serialBuses[%q].parity %q is invalid", dev, bus.Parity)
		}
	}
	if c.AutonomyPolicy != nil {
		if err := c.AutonomyPolicy.Validate(); err != nil {
			return fmt.Errorf("autonomyPolicy: %w", err)
		}
	}
	if c.EdgeRuleBundle != nil {
		if err := c.EdgeRuleBundle.Validate(); err != nil {
			return fmt.Errorf("edgeRuleBundle: %w", err)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
