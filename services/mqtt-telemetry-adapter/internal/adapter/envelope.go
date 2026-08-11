package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const (
	TelemetryEnvelopeSchemaVersion = 1
	maximumEnvelopeBytes           = 1 << 20
	maximumDevicesPerEnvelope      = 1024
	maximumPointsPerDevice         = 4096
)

type TelemetryEnvelope struct {
	SchemaVersion int              `json:"schemaVersion"`
	MessageID     string           `json:"messageId"`
	TenantID      string           `json:"tenantId"`
	SiteID        string           `json:"siteId"`
	GatewayID     string           `json:"gatewayId"`
	PublishedAt   string           `json:"publishedAt"`
	Replay        bool             `json:"replay"`
	Devices       []EnvelopeDevice `json:"devices"`
}

type EnvelopeDevice struct {
	ExternalDeviceID string          `json:"externalDeviceId"`
	Points           []EnvelopePoint `json:"points"`
}

type EnvelopePoint struct {
	TelemetryKey string  `json:"telemetryKey"`
	Value        any     `json:"value"`
	ValueType    string  `json:"valueType"`
	Unit         *string `json:"unit"`
	Quality      string  `json:"quality"`
	SampledAt    string  `json:"sampledAt"`
	Sequence     uint64  `json:"sequence"`
}

type TopicScope struct {
	TenantID  string
	SiteID    string
	GatewayID string
}

func ParseTelemetryTopic(topic string) (TopicScope, error) {
	topic = strings.TrimSpace(topic)
	if strings.HasPrefix(topic, "/") || strings.HasSuffix(topic, "/") {
		return TopicScope{}, errors.New("MQTT telemetry topic is invalid")
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[0] != "energy" || parts[1] != "v1" || parts[5] != "telemetry" {
		return TopicScope{}, errors.New("MQTT telemetry topic is invalid")
	}
	scope := TopicScope{TenantID: strings.TrimSpace(parts[2]), SiteID: strings.TrimSpace(parts[3]), GatewayID: strings.TrimSpace(parts[4])}
	if !uuidV7Pattern.MatchString(scope.TenantID) || !uuidV7Pattern.MatchString(scope.SiteID) || !validGatewayID(scope.GatewayID) {
		return TopicScope{}, errors.New("MQTT telemetry topic scope is invalid")
	}
	return scope, nil
}

func DecodeTelemetryEnvelope(payload []byte, scope TopicScope) (TelemetryEnvelope, error) {
	if len(payload) == 0 || len(payload) > maximumEnvelopeBytes {
		return TelemetryEnvelope{}, errors.New("MQTT telemetry envelope size is invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(payload), maximumEnvelopeBytes))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var envelope TelemetryEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return TelemetryEnvelope{}, fmt.Errorf("decode MQTT telemetry envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return TelemetryEnvelope{}, errors.New("MQTT telemetry envelope contains trailing JSON")
	}
	if err := envelope.Validate(scope); err != nil {
		return TelemetryEnvelope{}, err
	}
	return envelope, nil
}

func (envelope TelemetryEnvelope) Validate(scope TopicScope) error {
	if envelope.SchemaVersion != TelemetryEnvelopeSchemaVersion {
		return fmt.Errorf("unsupported MQTT telemetry envelope schemaVersion %d", envelope.SchemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(envelope.MessageID)) {
		return errors.New("MQTT telemetry messageId must be UUIDv7")
	}
	if strings.TrimSpace(envelope.TenantID) != scope.TenantID || strings.TrimSpace(envelope.SiteID) != scope.SiteID || strings.TrimSpace(envelope.GatewayID) != scope.GatewayID {
		return errors.New("MQTT telemetry topic and envelope scope differ")
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(envelope.PublishedAt)); err != nil {
		return errors.New("MQTT telemetry publishedAt is invalid")
	}
	if len(envelope.Devices) == 0 || len(envelope.Devices) > maximumDevicesPerEnvelope {
		return errors.New("MQTT telemetry device count is invalid")
	}
	seenDevices := make(map[string]struct{}, len(envelope.Devices))
	for _, device := range envelope.Devices {
		deviceID := strings.TrimSpace(device.ExternalDeviceID)
		if deviceID == "" || len(deviceID) > 256 {
			return errors.New("MQTT telemetry externalDeviceId is invalid")
		}
		if _, duplicate := seenDevices[deviceID]; duplicate {
			return fmt.Errorf("MQTT telemetry device %s is duplicated", deviceID)
		}
		seenDevices[deviceID] = struct{}{}
		if len(device.Points) == 0 || len(device.Points) > maximumPointsPerDevice {
			return fmt.Errorf("MQTT telemetry device %s point count is invalid", deviceID)
		}
		seenPoints := make(map[string]struct{}, len(device.Points))
		for _, point := range device.Points {
			if err := validateEnvelopePoint(point); err != nil {
				return fmt.Errorf("MQTT telemetry device %s: %w", deviceID, err)
			}
			key := strings.TrimSpace(point.TelemetryKey)
			if _, duplicate := seenPoints[key]; duplicate {
				return fmt.Errorf("MQTT telemetry point %s is duplicated for device %s", key, deviceID)
			}
			seenPoints[key] = struct{}{}
		}
	}
	return nil
}

func validateEnvelopePoint(point EnvelopePoint) error {
	key := strings.TrimSpace(point.TelemetryKey)
	if key == "" || len(key) > 128 {
		return errors.New("telemetryKey is invalid")
	}
	if point.Sequence > math.MaxInt64 {
		return fmt.Errorf("point %s sequence exceeds S2 source-position range", key)
	}
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(point.SampledAt))
	if err != nil || sampledAt.UnixMilli() < 0 || sampledAt.UnixMilli() >= 1<<48 {
		return fmt.Errorf("point %s sampledAt is invalid", key)
	}
	if strings.ToUpper(strings.TrimSpace(point.Quality)) != "GOOD" {
		return fmt.Errorf("point %s quality must be GOOD in MQTT envelope schema v1", key)
	}
	valueType := strings.ToUpper(strings.TrimSpace(point.ValueType))
	switch valueType {
	case "NUMBER":
		number, ok := point.Value.(json.Number)
		if !ok {
			return fmt.Errorf("point %s NUMBER value is invalid", key)
		}
		value, err := number.Float64()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("point %s NUMBER value is invalid", key)
		}
	case "STRING":
		if _, ok := point.Value.(string); !ok {
			return fmt.Errorf("point %s STRING value is invalid", key)
		}
	case "BOOLEAN":
		if _, ok := point.Value.(bool); !ok {
			return fmt.Errorf("point %s BOOLEAN value is invalid", key)
		}
	case "JSON":
		if point.Value == nil {
			return fmt.Errorf("point %s JSON value is invalid", key)
		}
	default:
		return fmt.Errorf("point %s valueType is invalid", key)
	}
	if point.Unit != nil {
		unit := strings.TrimSpace(*point.Unit)
		if unit == "" || len(unit) > 64 {
			return fmt.Errorf("point %s unit is invalid", key)
		}
	}
	return nil
}

func validGatewayID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' || character == ':' {
			continue
		}
		return false
	}
	return true
}
