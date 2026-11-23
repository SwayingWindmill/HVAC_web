package edgedaemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	defaultDeadbandMaxSilenceSec       = 300
)

// SerialBusConfig configures UART framing for an RS-485 bus device such as /dev/ttyAS3.
type SerialBusConfig struct {
	Speed             uint   `json:"speed"`
	DataBits          uint   `json:"dataBits"`
	Parity            string `json:"parity"`
	StopBits          uint   `json:"stopBits"`
	TurnaroundDelayMS int    `json:"turnaroundDelayMs,omitempty"`
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

// ET1010SequenceTimings configures step and coast-down delays for Songyaxuan chiller sequences.
type ET1010SequenceTimings struct {
	PumpToCoolingPumpMS    int `json:"pumpToCoolingPumpMs,omitempty"`
	CoolingPumpToTowerMS   int `json:"coolingPumpToTowerMs,omitempty"`
	TowerToChillerPulseMS  int `json:"towerToChillerPulseMs,omitempty"`
	ChillerStopCoastDownMS int `json:"chillerStopCoastDownMs,omitempty"`
	AuxiliaryStopStepMS    int `json:"auxiliaryStopStepMs,omitempty"`
}

// SongyaxuanPlantConfig enables native Go RS-485 polling, ET1010 sequential control,
// and UID 20 wireless concentrator MQTT RPC over the shared Modbus RTU bus.
type SongyaxuanPlantConfig struct {
	Enabled               bool                   `json:"enabled"`
	SerialPort            string                 `json:"serialPort"`
	RequestTimeoutMS      int                    `json:"requestTimeoutMs,omitempty"`
	Retries               int                    `json:"retries,omitempty"`
	TempPollIntervalSec   int                    `json:"tempPollIntervalSec,omitempty"`
	FlowPollIntervalSec   int                    `json:"flowPollIntervalSec,omitempty"`
	MeterPollIntervalSec  int                    `json:"meterPollIntervalSec,omitempty"`
	ET1010PollIntervalSec int                    `json:"et1010PollIntervalSec,omitempty"`
	PulseDurationMS       int                    `json:"pulseDurationMs,omitempty"`
	ChillerStopPowerKW    *float64               `json:"chillerStopPowerKw,omitempty"`
	ConcentratorUnitID    uint8                  `json:"concentratorUnitId,omitempty"`
	ConcentratorEnabled   *bool                  `json:"concentratorEnabled,omitempty"`
	ET1010UnitID          uint8                  `json:"et1010UnitId,omitempty"`
	ET1010Enabled         *bool                  `json:"et1010Enabled,omitempty"`
	SequenceTimings       *ET1010SequenceTimings `json:"sequenceTimings,omitempty"`
}

// IsConcentratorEnabled reports whether UID 20 wireless concentrator RPC handling is active.
func (c SongyaxuanPlantConfig) IsConcentratorEnabled() bool {
	return c.Enabled && (c.ConcentratorEnabled == nil || *c.ConcentratorEnabled)
}

// IsET1010Enabled reports whether ET1010 DO command execution and polling are active.
func (c SongyaxuanPlantConfig) IsET1010Enabled() bool {
	return c.Enabled && (c.ET1010Enabled == nil || *c.ET1010Enabled)
}

// StopPowerKW returns the chiller stop verification threshold in kW (defaults to 5 kW).
func (c SongyaxuanPlantConfig) StopPowerKW() float64 {
	if c.ChillerStopPowerKW == nil {
		return 5
	}
	return *c.ChillerStopPowerKW
}

// EffectiveSequenceTimings fills omitted sequence delays with production defaults.
func (c SongyaxuanPlantConfig) EffectiveSequenceTimings() ET1010SequenceTimings {
	out := ET1010SequenceTimings{
		PumpToCoolingPumpMS:    30000,
		CoolingPumpToTowerMS:   30000,
		TowerToChillerPulseMS:  60000,
		ChillerStopCoastDownMS: 30000,
		AuxiliaryStopStepMS:    10000,
	}
	if c.SequenceTimings == nil {
		return out
	}
	if c.SequenceTimings.PumpToCoolingPumpMS > 0 {
		out.PumpToCoolingPumpMS = c.SequenceTimings.PumpToCoolingPumpMS
	}
	if c.SequenceTimings.CoolingPumpToTowerMS > 0 {
		out.CoolingPumpToTowerMS = c.SequenceTimings.CoolingPumpToTowerMS
	}
	if c.SequenceTimings.TowerToChillerPulseMS > 0 {
		out.TowerToChillerPulseMS = c.SequenceTimings.TowerToChillerPulseMS
	}
	if c.SequenceTimings.ChillerStopCoastDownMS > 0 {
		out.ChillerStopCoastDownMS = c.SequenceTimings.ChillerStopCoastDownMS
	}
	if c.SequenceTimings.AuxiliaryStopStepMS > 0 {
		out.AuxiliaryStopStepMS = c.SequenceTimings.AuxiliaryStopStepMS
	}
	return out
}

// Config defines the runtime configuration for the standalone Linux edge daemon.
type Config struct {
	GatewayID                      string                             `json:"gatewayId"`
	SiteID                         string                             `json:"siteId,omitempty"`
	HardwareModel                  string                             `json:"hardwareModel,omitempty"`
	RuntimeVersion                 string                             `json:"runtimeVersion,omitempty"`
	TargetOS                       string                             `json:"targetOs,omitempty"`
	TargetArch                     string                             `json:"targetArch,omitempty"`
	StateDir                       string                             `json:"stateDir"`
	SpoolMaxBytes                  int64                              `json:"spoolMaxBytes,omitempty"`
	PlatformBaseURL                string                             `json:"platformBaseUrl,omitempty"`
	MQTTBrokerURL                  string                             `json:"mqttBrokerUrl,omitempty"`
	ServerName                     string                             `json:"serverName,omitempty"`
	CAFile                         string                             `json:"caFile,omitempty"`
	MQTTServerName                 string                             `json:"mqttServerName,omitempty"`
	MQTTCAFile                     string                             `json:"mqttCaFile,omitempty"`
	CertFile                       string                             `json:"certFile,omitempty"`
	KeyFile                        string                             `json:"keyFile,omitempty"`
	HealthListenAddr               string                             `json:"healthListenAddr,omitempty"`
	CycleIntervalMS                int                                `json:"cycleIntervalMs,omitempty"`
	ShadowPollIntervalSec          int                                `json:"shadowPollIntervalSec,omitempty"`
	CertRenewCheckIntervalSec      int                                `json:"certRenewCheckIntervalSec,omitempty"`
	CertRenewThresholdDays         int                                `json:"certRenewThresholdDays,omitempty"`
	TelemetryDeadband              float64                            `json:"telemetryDeadband,omitempty"`
	TelemetryDeadbandMaxSilenceSec int                                `json:"telemetryDeadbandMaxSilenceSec,omitempty"`
	Tunnel                         TunnelConfig                       `json:"tunnel"`
	OTA                            OTAConfig                          `json:"ota"`
	SerialBuses                    map[string]SerialBusConfig         `json:"serialBuses,omitempty"`
	Drivers                        []edgecontrol.ModbusDriverInput    `json:"drivers,omitempty"`
	AutonomyPolicy                 *edgecontrol.AutonomyPolicyConfig  `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle                 *edgecontrol.EdgeRuleBundle        `json:"edgeRuleBundle,omitempty"`
	AggregationPolicy              *edgecontrol.EdgeAggregationPolicy `json:"aggregationPolicy,omitempty"`
	ClusterPolicy                  *edgecontrol.ClusterControlPolicy  `json:"clusterPolicy,omitempty"`
	EnergySchedule                 *edgecontrol.EdgeEnergySchedule    `json:"energySchedule,omitempty"`
	SongyaxuanPlant                *SongyaxuanPlantConfig             `json:"songyaxuanPlant,omitempty"`
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
	if c.SongyaxuanPlant != nil && c.SongyaxuanPlant.Enabled {
		p := c.SongyaxuanPlant
		p.SerialPort = strings.TrimSpace(p.SerialPort)
		if p.RequestTimeoutMS == 0 {
			p.RequestTimeoutMS = 1000
		}
		if p.TempPollIntervalSec == 0 {
			p.TempPollIntervalSec = 60
		}
		if p.FlowPollIntervalSec == 0 {
			p.FlowPollIntervalSec = 60
		}
		if p.MeterPollIntervalSec == 0 {
			p.MeterPollIntervalSec = 300
		}
		if p.ET1010PollIntervalSec == 0 {
			p.ET1010PollIntervalSec = 60
		}
		if p.PulseDurationMS == 0 {
			p.PulseDurationMS = 500
		}
		if p.ChillerStopPowerKW == nil {
			defKW := 5.0
			p.ChillerStopPowerKW = &defKW
		}
		if p.ConcentratorUnitID == 0 {
			p.ConcentratorUnitID = 20
		}
		if p.ConcentratorEnabled == nil {
			en := true
			p.ConcentratorEnabled = &en
		}
		if p.ET1010UnitID == 0 {
			p.ET1010UnitID = 31
		}
		if p.ET1010Enabled == nil {
			en := true
			p.ET1010Enabled = &en
		}
		if p.SerialPort != "" {
			if c.SerialBuses == nil {
				c.SerialBuses = map[string]SerialBusConfig{}
			}
			if _, exists := c.SerialBuses[p.SerialPort]; !exists {
				c.SerialBuses[p.SerialPort] = SerialBusConfig{
					Speed:             9600,
					DataBits:          8,
					Parity:            "N",
					StopBits:          1,
					TurnaroundDelayMS: 45,
				}
			}
		}
	}
	if c.TelemetryDeadband > 0 && c.TelemetryDeadbandMaxSilenceSec == 0 {
		c.TelemetryDeadbandMaxSilenceSec = defaultDeadbandMaxSilenceSec
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
	if math.IsNaN(c.TelemetryDeadband) || math.IsInf(c.TelemetryDeadband, 0) || c.TelemetryDeadband < 0 {
		return errors.New("telemetryDeadband must be a finite non-negative number")
	}
	if c.TelemetryDeadbandMaxSilenceSec < 0 || c.TelemetryDeadbandMaxSilenceSec > 86400 {
		return fmt.Errorf("telemetryDeadbandMaxSilenceSec %d must be in [0,86400]", c.TelemetryDeadbandMaxSilenceSec)
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
		if bus.TurnaroundDelayMS < 0 || bus.TurnaroundDelayMS > 5000 {
			return fmt.Errorf("serialBuses[%q].turnaroundDelayMs %d must be in [0,5000]", dev, bus.TurnaroundDelayMS)
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
	if c.AggregationPolicy != nil {
		if err := c.AggregationPolicy.Validate(); err != nil {
			return fmt.Errorf("aggregationPolicy: %w", err)
		}
	}
	if c.ClusterPolicy != nil {
		if err := c.ClusterPolicy.Validate(); err != nil {
			return fmt.Errorf("clusterPolicy: %w", err)
		}
	}
	if c.SongyaxuanPlant != nil && c.SongyaxuanPlant.Enabled {
		p := c.SongyaxuanPlant
		if strings.TrimSpace(p.SerialPort) == "" {
			return errors.New("songyaxuanPlant.serialPort is required when enabled")
		}
		if p.RequestTimeoutMS < 50 || p.RequestTimeoutMS > 10000 {
			return fmt.Errorf("songyaxuanPlant.requestTimeoutMs %d must be in [50,10000]", p.RequestTimeoutMS)
		}
		if p.Retries < 0 || p.Retries > 5 {
			return fmt.Errorf("songyaxuanPlant.retries %d must be in [0,5]", p.Retries)
		}
		for label, sec := range map[string]int{
			"tempPollIntervalSec":   p.TempPollIntervalSec,
			"flowPollIntervalSec":   p.FlowPollIntervalSec,
			"meterPollIntervalSec":  p.MeterPollIntervalSec,
			"et1010PollIntervalSec": p.ET1010PollIntervalSec,
		} {
			if sec < 1 || sec > 86400 {
				return fmt.Errorf("songyaxuanPlant.%s %d must be in [1,86400]", label, sec)
			}
		}
		if p.PulseDurationMS < 50 || p.PulseDurationMS > 10000 {
			return fmt.Errorf("songyaxuanPlant.pulseDurationMs %d must be in [50,10000]", p.PulseDurationMS)
		}
		kw := p.StopPowerKW()
		if math.IsNaN(kw) || math.IsInf(kw, 0) || kw < 0 {
			return errors.New("songyaxuanPlant.chillerStopPowerKw must be finite and >= 0")
		}
		if p.ConcentratorUnitID < 1 || p.ConcentratorUnitID > 247 {
			return fmt.Errorf("songyaxuanPlant.concentratorUnitId %d must be in [1,247]", p.ConcentratorUnitID)
		}
		if p.ET1010UnitID < 1 || p.ET1010UnitID > 247 {
			return fmt.Errorf("songyaxuanPlant.et1010UnitId %d must be in [1,247]", p.ET1010UnitID)
		}
		if p.SequenceTimings != nil {
			for label, ms := range map[string]int{
				"pumpToCoolingPumpMs":    p.SequenceTimings.PumpToCoolingPumpMS,
				"coolingPumpToTowerMs":   p.SequenceTimings.CoolingPumpToTowerMS,
				"towerToChillerPulseMs":  p.SequenceTimings.TowerToChillerPulseMS,
				"chillerStopCoastDownMs": p.SequenceTimings.ChillerStopCoastDownMS,
				"auxiliaryStopStepMs":    p.SequenceTimings.AuxiliaryStopStepMS,
			} {
				if ms < 0 || ms > 3600000 {
					return fmt.Errorf("songyaxuanPlant.sequenceTimings.%s %d must be in [0,3600000]", label, ms)
				}
			}
		}
	}
	if c.EnergySchedule != nil {
		if err := c.EnergySchedule.Validate(); err != nil {
			return fmt.Errorf("energySchedule: %w", err)
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
