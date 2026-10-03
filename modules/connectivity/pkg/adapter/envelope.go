package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	EnvelopeSchemaVersion     = "2.0"
	maximumEnvelopeBytes      = 1 << 20
	maximumDevicesPerEnvelope = 1024
	maximumPointsPerDevice    = 4096
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

func DecodeTelemetryEnvelope(payload []byte, gatewayID string) (TelemetryEnvelope, error) {
	var envelope TelemetryEnvelope
	if err := decodeStrictEnvelope(payload, &envelope); err != nil {
		return TelemetryEnvelope{}, err
	}
	if err := envelope.Validate(gatewayID); err != nil {
		return TelemetryEnvelope{}, err
	}
	return envelope, nil
}

func (envelope TelemetryEnvelope) Validate(gatewayID string) error {
	if err := validateMessageHeader(envelope.SchemaVersion, envelope.MessageID, envelope.GatewayID, envelope.Timestamp, envelope.Sequence, envelope.TraceID, gatewayID); err != nil {
		return err
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
