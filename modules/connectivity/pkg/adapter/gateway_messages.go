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
	MessageTypeEvent     = "event"
)

// UplinkTopicFilters are the Gateway uplinks Connectivity consumes (ADR 0015). The broker
// ACL confines each Gateway to hvac/v1/{its certificate CN}/up/#.
var UplinkTopicFilters = []string{"hvac/v1/+/up/" + MessageTypeTelemetry, "hvac/v1/+/up/" + MessageTypeEvent}

type MessageTopic struct {
	GatewayID   string
	MessageType string
}

func ParseMessageTopic(topic string) (MessageTopic, error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 5 || parts[0] != "hvac" || parts[1] != "v1" || parts[3] != "up" {
		return MessageTopic{}, errors.New("MQTT topic is invalid")
	}
	if !uuidV7Pattern.MatchString(parts[2]) || parts[2] != strings.ToLower(parts[2]) {
		return MessageTopic{}, errors.New("MQTT topic Gateway id is invalid")
	}
	switch parts[4] {
	case MessageTypeTelemetry, MessageTypeEvent:
	default:
		return MessageTopic{}, errors.New("MQTT uplink message type is not accepted")
	}
	return MessageTopic{GatewayID: parts[2], MessageType: parts[4]}, nil
}

// TopicGatewayID returns the Gateway segment of any hvac/v1 topic, so even a message
// that cannot be parsed is queued and quarantined under the Gateway that sent it.
func TopicGatewayID(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) < 3 || len(parts[2]) == 0 || len(parts[2]) > 128 {
		return "invalid"
	}
	return parts[2]
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

func DecodeEventEnvelope(payload []byte, gatewayID string) (EventEnvelope, error) {
	var envelope EventEnvelope
	if err := decodeStrictEnvelope(payload, &envelope); err != nil {
		return EventEnvelope{}, err
	}
	if err := validateMessageHeader(envelope.SchemaVersion, envelope.MessageID, envelope.GatewayID, envelope.Timestamp, envelope.Sequence, envelope.TraceID, gatewayID); err != nil {
		return EventEnvelope{}, err
	}
	event := envelope.Payload
	if strings.TrimSpace(event.EventType) == "" || strings.TrimSpace(event.SourceType) == "" || strings.TrimSpace(event.SourceID) == "" {
		return EventEnvelope{}, errors.New("MQTT event payload is incomplete")
	}
	if event.EventTime < 0 || event.EventTime >= 1<<48 {
		return EventEnvelope{}, errors.New("MQTT event time must be Unix epoch milliseconds")
	}
	switch strings.ToUpper(strings.TrimSpace(event.Severity)) {
	case "INFO", "WARNING", "CRITICAL":
	default:
		return EventEnvelope{}, errors.New("MQTT event severity is invalid")
	}
	if len(event.Data) == 0 {
		envelope.Payload.Data = json.RawMessage(`{}`)
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
		return fmt.Errorf("decode MQTT envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("MQTT envelope contains trailing JSON")
	}
	return nil
}

func validateMessageHeader(schemaVersion, messageID, envelopeGatewayID string, timestamp int64, sequence uint64, traceID, topicGatewayID string) error {
	if strings.TrimSpace(schemaVersion) != EnvelopeSchemaVersion {
		return fmt.Errorf("unsupported MQTT envelope schemaVersion %s", schemaVersion)
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(messageID)) {
		return errors.New("MQTT messageId must be UUIDv7")
	}
	if strings.TrimSpace(envelopeGatewayID) != topicGatewayID {
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
