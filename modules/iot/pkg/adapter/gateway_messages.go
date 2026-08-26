package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

const (
	MessageTypeTelemetry = "telemetry"
	MessageTypeState     = "state"
	MessageTypeEvent     = "event"
	MessageTypeHeartbeat = "heartbeat"
)

type MessageTopic struct {
	TopicScope
	MessageType string
}

func ParseMessageTopic(topic string) (MessageTopic, error) {
	topic = strings.TrimSpace(topic)
	if strings.HasPrefix(topic, "/") || strings.HasSuffix(topic, "/") {
		return MessageTopic{}, errors.New("MQTT topic is invalid")
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[0] != "energy" || parts[1] != "v1" {
		return MessageTopic{}, errors.New("MQTT topic is invalid")
	}
	messageType := strings.TrimSpace(parts[5])
	switch messageType {
	case MessageTypeTelemetry, MessageTypeState, MessageTypeEvent, MessageTypeHeartbeat:
	default:
		return MessageTopic{}, errors.New("MQTT message type is not accepted by the IoT uplink adapter")
	}
	scope := TopicScope{TenantID: strings.TrimSpace(parts[2]), SiteID: strings.TrimSpace(parts[3]), GatewayID: strings.TrimSpace(parts[4])}
	if !uuidV7Pattern.MatchString(scope.TenantID) || !uuidV7Pattern.MatchString(scope.SiteID) || !validGatewayID(scope.GatewayID) {
		return MessageTopic{}, errors.New("MQTT topic scope is invalid")
	}
	return MessageTopic{TopicScope: scope, MessageType: messageType}, nil
}

type StateEnvelope struct {
	SchemaVersion string       `json:"schemaVersion"`
	MessageID     string       `json:"messageId"`
	GatewayID     string       `json:"gatewayId"`
	Timestamp     int64        `json:"timestamp"`
	Sequence      uint64       `json:"sequence"`
	TraceID       string       `json:"traceId,omitempty"`
	Payload       StatePayload `json:"payload"`
}

type StatePayload struct {
	Online      *bool         `json:"online,omitempty"`
	State       string        `json:"state,omitempty"`
	ControlMode string        `json:"controlMode,omitempty"`
	Devices     []StateDevice `json:"devices,omitempty"`
}

type StateDevice struct {
	DeviceID    string `json:"deviceId"`
	Online      *bool  `json:"online,omitempty"`
	State       string `json:"state,omitempty"`
	ControlMode string `json:"controlMode,omitempty"`
}

type HeartbeatEnvelope struct {
	SchemaVersion string           `json:"schemaVersion"`
	MessageID     string           `json:"messageId"`
	GatewayID     string           `json:"gatewayId"`
	Timestamp     int64            `json:"timestamp"`
	Sequence      uint64           `json:"sequence"`
	TraceID       string           `json:"traceId,omitempty"`
	Payload       HeartbeatPayload `json:"payload"`
}

type HeartbeatPayload struct {
	UptimeSeconds    *uint64  `json:"uptimeSeconds,omitempty"`
	CPUPercent       *float64 `json:"cpuPercent,omitempty"`
	MemoryPercent    *float64 `json:"memoryPercent,omitempty"`
	DiskPercent      *float64 `json:"diskPercent,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	SoftwareVersion string   `json:"softwareVersion,omitempty"`
	ConfigVersion   string   `json:"configVersion,omitempty"`
	ConnectedDevices []string `json:"connectedDevices,omitempty"`
	OfflineDevices   []string `json:"offlineDevices,omitempty"`
	PendingMessages *uint64  `json:"pendingMessages,omitempty"`
}

type EventEnvelope struct {
	SchemaVersion string       `json:"schemaVersion"`
	MessageID     string       `json:"messageId"`
	GatewayID     string       `json:"gatewayId"`
	Timestamp     int64        `json:"timestamp"`
	Sequence      uint64       `json:"sequence"`
	TraceID       string       `json:"traceId,omitempty"`
	Payload       EventPayload `json:"payload"`
}

type EventPayload struct {
	EventType  string          `json:"eventType"`
	SourceType string          `json:"sourceType"`
	SourceID   string          `json:"sourceId"`
	EventTime  int64           `json:"eventTime"`
	Severity   string          `json:"severity"`
	Data       json.RawMessage `json:"data"`
}

func DecodeStateEnvelope(payload []byte, scope TopicScope) (StateEnvelope, error) {
	var envelope StateEnvelope
	if err := decodeStrictEnvelope(payload, &envelope); err != nil {
		return StateEnvelope{}, fmt.Errorf("decode MQTT state envelope: %w", err)
	}
	if err := validateMessageHeader(envelope.SchemaVersion, envelope.MessageID, envelope.GatewayID, envelope.Timestamp, envelope.Sequence, envelope.TraceID, scope); err != nil {
		return StateEnvelope{}, err
	}
	if envelope.Payload.State == "" && envelope.Payload.ControlMode == "" && envelope.Payload.Online == nil && len(envelope.Payload.Devices) == 0 {
		return StateEnvelope{}, errors.New("MQTT state payload is empty")
	}
	seen := map[string]struct{}{}
	for _, device := range envelope.Payload.Devices {
		id := strings.TrimSpace(device.DeviceID)
		if id == "" || len(id) > 256 {
			return StateEnvelope{}, errors.New("MQTT state deviceId is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			return StateEnvelope{}, errors.New("MQTT state device is duplicated")
		}
		seen[id] = struct{}{}
	}
	return envelope, nil
}

func DecodeHeartbeatEnvelope(payload []byte, scope TopicScope) (HeartbeatEnvelope, error) {
	var envelope HeartbeatEnvelope
	if err := decodeStrictEnvelope(payload, &envelope); err != nil {
		return HeartbeatEnvelope{}, fmt.Errorf("decode MQTT heartbeat envelope: %w", err)
	}
	if err := validateMessageHeader(envelope.SchemaVersion, envelope.MessageID, envelope.GatewayID, envelope.Timestamp, envelope.Sequence, envelope.TraceID, scope); err != nil {
		return HeartbeatEnvelope{}, err
	}
	for _, value := range []*float64{envelope.Payload.CPUPercent, envelope.Payload.MemoryPercent, envelope.Payload.DiskPercent, envelope.Payload.Temperature} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return HeartbeatEnvelope{}, errors.New("MQTT heartbeat numeric value is invalid")
		}
	}
	if err := validateDeviceList(envelope.Payload.ConnectedDevices); err != nil {
		return HeartbeatEnvelope{}, fmt.Errorf("MQTT heartbeat connectedDevices: %w", err)
	}
	if err := validateDeviceList(envelope.Payload.OfflineDevices); err != nil {
		return HeartbeatEnvelope{}, fmt.Errorf("MQTT heartbeat offlineDevices: %w", err)
	}
	return envelope, nil
}

func DecodeEventEnvelope(payload []byte, scope TopicScope) (EventEnvelope, error) {
	var envelope EventEnvelope
	if err := decodeStrictEnvelope(payload, &envelope); err != nil {
		return EventEnvelope{}, fmt.Errorf("decode MQTT event envelope: %w", err)
	}
	if err := validateMessageHeader(envelope.SchemaVersion, envelope.MessageID, envelope.GatewayID, envelope.Timestamp, envelope.Sequence, envelope.TraceID, scope); err != nil {
		return EventEnvelope{}, err
	}
	if strings.TrimSpace(envelope.Payload.EventType) == "" || strings.TrimSpace(envelope.Payload.SourceType) == "" || strings.TrimSpace(envelope.Payload.SourceID) == "" {
		return EventEnvelope{}, errors.New("MQTT event identity is invalid")
	}
	if envelope.Payload.EventTime < 0 || envelope.Payload.EventTime >= 1<<48 {
		return EventEnvelope{}, errors.New("MQTT event eventTime must be Unix epoch milliseconds")
	}
	switch strings.ToUpper(strings.TrimSpace(envelope.Payload.Severity)) {
	case "INFO", "WARNING", "CRITICAL":
	default:
		return EventEnvelope{}, errors.New("MQTT event severity is invalid")
	}
	if len(envelope.Payload.Data) == 0 || !json.Valid(envelope.Payload.Data) {
		return EventEnvelope{}, errors.New("MQTT event data must be valid JSON")
	}
	return envelope, nil
}

func decodeStrictEnvelope(payload []byte, destination any) error {
	if len(payload) == 0 || len(payload) > maximumEnvelopeBytes {
		return errors.New("MQTT envelope size is invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(payload), maximumEnvelopeBytes))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("MQTT envelope contains trailing JSON")
	}
	return nil
}

func validateMessageHeader(schemaVersion, messageID, gatewayID string, timestamp int64, sequence uint64, traceID string, scope TopicScope) error {
	if strings.TrimSpace(schemaVersion) != TelemetryEnvelopeSchemaVersion {
		return fmt.Errorf("unsupported MQTT envelope schemaVersion %s", schemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(messageID)) {
		return errors.New("MQTT messageId must be UUIDv7")
	}
	if strings.TrimSpace(gatewayID) != scope.GatewayID {
		return errors.New("MQTT topic and envelope gateway differ")
	}
	if timestamp < 0 || timestamp >= 1<<48 {
		return errors.New("MQTT timestamp must be Unix epoch milliseconds")
	}
	if sequence > math.MaxInt64 {
		return errors.New("MQTT sequence exceeds source-position range")
	}
	if len(traceID) > 256 || strings.ContainsAny(traceID, "\r\n") {
		return errors.New("MQTT traceId is invalid")
	}
	return nil
}

func validateDeviceList(values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" || len(id) > 256 {
			return errors.New("deviceId is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.New("deviceId is duplicated")
		}
		seen[id] = struct{}{}
	}
	return nil
}
