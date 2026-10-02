package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	TelemetryEnvelopeSchemaVersion = "1.0"
	maximumEnvelopeBytes           = 1 << 20
	maximumDevicesPerEnvelope      = 1024
	maximumPointsPerDevice         = 4096
)

var wirePointCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

type TelemetryEnvelope struct {
	SchemaVersion string           `json:"schemaVersion"`
	MessageID     string           `json:"messageId"`
	GatewayID     string           `json:"gatewayId"`
	Timestamp     int64            `json:"timestamp"`
	Sequence      uint64           `json:"sequence"`
	TraceID       string           `json:"traceId,omitempty"`
	Replay        bool             `json:"replay"`
	Payload       TelemetryPayload `json:"payload"`
}

type TelemetryPayload struct {
	Devices []EnvelopeDevice `json:"devices"`
}

type EnvelopeDevice struct {
	DeviceID        string          `json:"deviceId"`
	DeviceTimestamp int64           `json:"deviceTimestamp"`
	Points          []EnvelopePoint `json:"points"`
}

type EnvelopePoint struct {
	Code    string  `json:"code"`
	Value   any     `json:"value"`
	Quality uint8   `json:"quality"`
	Unit    *string `json:"unit,omitempty"`
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
	if strings.TrimSpace(envelope.SchemaVersion) != TelemetryEnvelopeSchemaVersion {
		return fmt.Errorf("unsupported MQTT telemetry envelope schemaVersion %s", envelope.SchemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(envelope.MessageID)) {
		return errors.New("MQTT telemetry messageId must be UUIDv7")
	}
	if strings.TrimSpace(envelope.GatewayID) != scope.GatewayID {
		return errors.New("MQTT telemetry topic and envelope gateway differ")
	}
	if envelope.Timestamp < 0 || envelope.Timestamp >= 1<<48 {
		return errors.New("MQTT telemetry timestamp must be Unix epoch milliseconds")
	}
	if envelope.Sequence > math.MaxInt64 {
		return errors.New("MQTT telemetry sequence exceeds source-position range")
	}
	if len(envelope.TraceID) > 256 || strings.ContainsAny(envelope.TraceID, "\r\n") {
		return errors.New("MQTT telemetry traceId is invalid")
	}
	if len(envelope.Payload.Devices) == 0 || len(envelope.Payload.Devices) > maximumDevicesPerEnvelope {
		return errors.New("MQTT telemetry device count is invalid")
	}
	seenDevices := make(map[string]struct{}, len(envelope.Payload.Devices))
	for _, device := range envelope.Payload.Devices {
		deviceID := strings.TrimSpace(device.DeviceID)
		if deviceID == "" || len(deviceID) > 256 {
			return errors.New("MQTT telemetry deviceId is invalid")
		}
		if device.DeviceTimestamp < 0 || device.DeviceTimestamp >= 1<<48 {
			return fmt.Errorf("MQTT telemetry device %s deviceTimestamp is invalid", deviceID)
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
			code := strings.TrimSpace(point.Code)
			if _, duplicate := seenPoints[code]; duplicate {
				return fmt.Errorf("MQTT telemetry point %s is duplicated for device %s", code, deviceID)
			}
			seenPoints[code] = struct{}{}
		}
	}
	return nil
}

func validateEnvelopePoint(point EnvelopePoint) error {
	code := strings.TrimSpace(point.Code)
	if !wirePointCodePattern.MatchString(code) {
		return errors.New("point code is invalid")
	}
	if _, err := wireValueType(point.Value); err != nil {
		return fmt.Errorf("point %s value is invalid: %w", code, err)
	}
	if point.Unit != nil {
		unit := strings.TrimSpace(*point.Unit)
		if unit == "" || len(unit) > 64 {
			return fmt.Errorf("point %s unit is invalid", code)
		}
	}
	return nil
}

func wireValueType(value any) (string, error) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return "", errors.New("numeric value is not finite")
		}
		return "NUMBER", nil
	case string:
		return "STRING", nil
	case bool:
		return "BOOLEAN", nil
	case nil:
		return "", errors.New("null is not a telemetry value")
	default:
		if _, err := json.Marshal(typed); err != nil {
			return "", err
		}
		return "JSON", nil
	}
}

func unixMillisRFC3339(milliseconds int64) string {
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339Nano)
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
