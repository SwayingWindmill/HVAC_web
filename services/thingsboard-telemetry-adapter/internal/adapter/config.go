package adapter

import (
	"bytes"
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

var (
	uuidV7Pattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	telemetryKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
)

type Config struct {
	SchemaVersion         int                    `json:"schemaVersion"`
	IntegrationInstanceID string                 `json:"integrationInstanceId"`
	PollInterval          string                 `json:"pollInterval"`
	InitialLookback       string                 `json:"initialLookback"`
	PageLimit             int                    `json:"pageLimit"`
	CheckpointFile        string                 `json:"checkpointFile"`
	ThingsBoard           ThingsBoardConfig      `json:"thingsBoard"`
	TelemetryRuntime      TelemetryRuntimeConfig `json:"telemetryRuntime"`
	Devices               []DeviceMapping        `json:"devices"`
}

type ThingsBoardConfig struct {
	BaseURL string `json:"baseUrl"`
	JWTFile string `json:"jwtFile"`
}

type TelemetryRuntimeConfig struct {
	BaseURL    string `json:"baseUrl"`
	CAFile     string `json:"caFile"`
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
	ServerName string `json:"serverName"`
}

type DeviceMapping struct {
	ThingsBoardDeviceID string         `json:"thingsBoardDeviceId"`
	ExternalID          string         `json:"externalId"`
	Points              []PointMapping `json:"points"`
}

type PointMapping struct {
	SourceKey    string  `json:"sourceKey"`
	TelemetryKey string  `json:"telemetryKey"`
	ValueType    string  `json:"valueType"`
	Unit         *string `json:"unit,omitempty"`
}

func DecodeConfig(reader io.Reader) (Config, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode adapter config: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if config.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("schemaVersion must equal %d", ConfigSchemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(config.IntegrationInstanceID)) {
		return errors.New("integrationInstanceId must be a lowercase UUIDv7")
	}
	pollInterval, err := time.ParseDuration(config.PollInterval)
	if err != nil || pollInterval < time.Second || pollInterval > time.Minute {
		return errors.New("pollInterval must be between 1s and 1m")
	}
	lookback, err := time.ParseDuration(config.InitialLookback)
	if err != nil || lookback < time.Second || lookback > 24*time.Hour {
		return errors.New("initialLookback must be between 1s and 24h")
	}
	if config.PageLimit < 1 || config.PageLimit > 1000 {
		return errors.New("pageLimit must be between 1 and 1000")
	}
	if !canonicalNonEmpty(config.CheckpointFile) {
		return errors.New("checkpointFile must be non-empty without surrounding whitespace")
	}
	if config.ThingsBoard.BaseURL != strings.TrimSpace(config.ThingsBoard.BaseURL) {
		return errors.New("thingsBoard.baseUrl must not contain surrounding whitespace")
	}
	if err := validateProviderURL(config.ThingsBoard.BaseURL, true); err != nil {
		return fmt.Errorf("thingsBoard.baseUrl: %w", err)
	}
	if !canonicalNonEmpty(config.ThingsBoard.JWTFile) {
		return errors.New("thingsBoard.jwtFile must be non-empty without surrounding whitespace")
	}
	if config.TelemetryRuntime.BaseURL != strings.TrimSpace(config.TelemetryRuntime.BaseURL) {
		return errors.New("telemetryRuntime.baseUrl must not contain surrounding whitespace")
	}
	if err := validateProviderURL(config.TelemetryRuntime.BaseURL, false); err != nil {
		return fmt.Errorf("telemetryRuntime.baseUrl: %w", err)
	}
	if !canonicalNonEmpty(config.TelemetryRuntime.CAFile) || !canonicalNonEmpty(config.TelemetryRuntime.CertFile) || !canonicalNonEmpty(config.TelemetryRuntime.KeyFile) {
		return errors.New("telemetryRuntime mTLS files must be non-empty without surrounding whitespace")
	}
	if !canonicalNonEmpty(config.TelemetryRuntime.ServerName) {
		return errors.New("telemetryRuntime.serverName must be non-empty without surrounding whitespace")
	}
	if len(config.Devices) == 0 {
		return errors.New("at least one device mapping is required")
	}
	seenTB := map[string]struct{}{}
	for index, device := range config.Devices {
		if err := device.validate(); err != nil {
			return fmt.Errorf("devices[%d]: %w", index, err)
		}
		if _, duplicate := seenTB[device.ThingsBoardDeviceID]; duplicate {
			return fmt.Errorf("devices[%d]: duplicate thingsBoardDeviceId", index)
		}
		seenTB[device.ThingsBoardDeviceID] = struct{}{}
	}
	return nil
}

func (device DeviceMapping) validate() error {
	if !canonicalNonEmpty(device.ThingsBoardDeviceID) || !canonicalNonEmpty(device.ExternalID) {
		return errors.New("thingsBoardDeviceId and externalId must be non-empty without surrounding whitespace")
	}
	if len(device.ExternalID) > 512 {
		return errors.New("externalId exceeds 512 characters")
	}
	if len(device.Points) == 0 {
		return errors.New("at least one point mapping is required")
	}
	seenSource := map[string]struct{}{}
	seenTelemetry := map[string]struct{}{}
	for index, point := range device.Points {
		if err := point.validate(); err != nil {
			return fmt.Errorf("points[%d]: %w", index, err)
		}
		if _, duplicate := seenSource[point.SourceKey]; duplicate {
			return fmt.Errorf("points[%d]: duplicate sourceKey", index)
		}
		if _, duplicate := seenTelemetry[point.TelemetryKey]; duplicate {
			return fmt.Errorf("points[%d]: duplicate telemetryKey", index)
		}
		seenSource[point.SourceKey] = struct{}{}
		seenTelemetry[point.TelemetryKey] = struct{}{}
	}
	return nil
}

func (point PointMapping) validate() error {
	if point.SourceKey != strings.TrimSpace(point.SourceKey) || point.TelemetryKey != strings.TrimSpace(point.TelemetryKey) || point.ValueType != strings.TrimSpace(point.ValueType) {
		return errors.New("point fields must not contain surrounding whitespace")
	}
	if !telemetryKeyPattern.MatchString(point.SourceKey) || !telemetryKeyPattern.MatchString(point.TelemetryKey) {
		return errors.New("sourceKey or telemetryKey is invalid")
	}
	switch strings.ToUpper(point.ValueType) {
	case "NUMBER", "STRING", "BOOLEAN", "JSON":
	default:
		return errors.New("valueType must be NUMBER, STRING, BOOLEAN, or JSON")
	}
	if point.Unit != nil {
		unit := *point.Unit
		if unit != strings.TrimSpace(unit) || len(unit) < 1 || len(unit) > 64 {
			return errors.New("unit must contain 1 to 64 characters without surrounding whitespace")
		}
	}
	return nil
}

func validateProviderURL(raw string, allowLocalHTTP bool) error {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("URL is invalid")
	}
	if parsed.Scheme != "https" {
		if !allowLocalHTTP || parsed.Scheme != "http" || !localHost(parsed.Hostname()) {
			return errors.New("HTTPS is required except for local ThingsBoard")
		}
	}
	return nil
}

func localHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "host.docker.internal":
		return true
	default:
		return false
	}
}

func canonicalNonEmpty(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

func (config Config) PollDuration() time.Duration {
	duration, _ := time.ParseDuration(config.PollInterval)
	return duration
}

func (config Config) LookbackDuration() time.Duration {
	duration, _ := time.ParseDuration(config.InitialLookback)
	return duration
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("adapter config contains trailing JSON")
	}
	return nil
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), bytes.TrimSpace(value)...)
}
