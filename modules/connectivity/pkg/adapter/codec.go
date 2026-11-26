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

// DecodedTelemetry wraps a canonical TelemetryEnvelope together with optional
// gateway session metadata.
type DecodedTelemetry struct {
	Envelope  TelemetryEnvelope
	SessionID string
}

// StatusEnvelope is the canonical representation of /up/status frames for v2.x
// gateway status payloads.
type StatusEnvelope struct {
	SchemaVersion string
	MessageID     string
	GatewayID     string
	SessionID     string
	Sequence      int64
	HasSequence   bool
	EventTime     int64
	HasEventTime  bool
	Online        bool
	TraceID       string
}

// UplinkCodec decodes raw MQTT uplink payloads for telemetry, event, or status topics
// into canonical internal adapter envelopes.
type UplinkCodec interface {
	Name() string
	CanDecode(messageType string, payload []byte) bool
	DecodeTelemetry(payload []byte, topicGatewayID string) (DecodedTelemetry, error)
	DecodeEvent(payload []byte, topicGatewayID string) (EventEnvelope, error)
	DecodeStatus(payload []byte, topicGatewayID string) (StatusEnvelope, error)
}

// CodecRegistry selects an UplinkCodec for incoming MQTT uplink frames.
type CodecRegistry struct {
	codecs []UplinkCodec
}

// NewCodecRegistry builds a CodecRegistry with codecs checked in registration order.
func NewCodecRegistry(codecs ...UplinkCodec) *CodecRegistry {
	registry := &CodecRegistry{}
	for _, codec := range codecs {
		if codec != nil {
			registry.codecs = append(registry.codecs, codec)
		}
	}
	return registry
}

// DefaultCodecRegistry returns the default adapter codec registry containing JSONV2Codec
// (schemaVersion "2.0" and forward-compatible "2.1+").
func DefaultCodecRegistry() *CodecRegistry {
	return NewCodecRegistry(JSONV2Codec{})
}

func (r *CodecRegistry) selectCodec(messageType string, payload []byte) (UplinkCodec, error) {
	probe, err := decodeEnvelopeVersion(payload)
	if err != nil {
		return nil, err
	}
	for _, codec := range r.codecs {
		if fast, ok := codec.(interface {
			canDecodeProbe(messageType string, probe versionProbe) bool
		}); ok {
			if fast.canDecodeProbe(messageType, probe) {
				return codec, nil
			}
			continue
		}
		if codec.CanDecode(messageType, payload) {
			return codec, nil
		}
	}
	return nil, errors.New("unsupported MQTT uplink payload schema")
}

// DecodeTelemetry decodes and validates a telemetry uplink payload via the matching codec.
func (r *CodecRegistry) DecodeTelemetry(payload []byte, topicGatewayID string) (DecodedTelemetry, error) {
	codec, err := r.selectCodec(MessageTypeTelemetry, payload)
	if err != nil {
		return DecodedTelemetry{}, err
	}
	return codec.DecodeTelemetry(payload, topicGatewayID)
}

// DecodeEvent decodes and validates an event uplink payload via the matching codec.
func (r *CodecRegistry) DecodeEvent(payload []byte, topicGatewayID string) (EventEnvelope, error) {
	codec, err := r.selectCodec(MessageTypeEvent, payload)
	if err != nil {
		return EventEnvelope{}, err
	}
	return codec.DecodeEvent(payload, topicGatewayID)
}

// DecodeStatus decodes and validates a status uplink payload via the matching codec.
func (r *CodecRegistry) DecodeStatus(payload []byte, topicGatewayID string) (StatusEnvelope, error) {
	codec, err := r.selectCodec(MessageTypeStatus, payload)
	if err != nil {
		return StatusEnvelope{}, err
	}
	return codec.DecodeStatus(payload, topicGatewayID)
}

type versionProbe struct {
	SchemaVersion string `json:"schemaVersion"`
}

func validateJSONObjectEnvelope(payload []byte) error {
	if len(payload) == 0 || len(payload) > maximumEnvelopeBytes {
		return errors.New("MQTT envelope size is invalid")
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("MQTT envelope must be a JSON object")
	}
	return nil
}

func decodeEnvelopeVersion(payload []byte) (versionProbe, error) {
	if err := validateJSONObjectEnvelope(payload); err != nil {
		return versionProbe{}, err
	}
	var probe versionProbe
	if err := json.Unmarshal(payload, &probe); err != nil {
		return versionProbe{}, fmt.Errorf("decode MQTT envelope: %w", err)
	}
	probe.SchemaVersion = strings.TrimSpace(probe.SchemaVersion)
	return probe, nil
}

// JSONV2Codec decodes canonical schemaVersion "2.0" and forward-compatible "2.1+" telemetry/event/status envelopes.
type JSONV2Codec struct{}

func (JSONV2Codec) Name() string { return "json-v2" }

func (c JSONV2Codec) CanDecode(messageType string, payload []byte) bool {
	probe, err := decodeEnvelopeVersion(payload)
	if err != nil {
		return false
	}
	return c.canDecodeProbe(messageType, probe)
}

func (JSONV2Codec) canDecodeProbe(messageType string, probe versionProbe) bool {
	if messageType == MessageTypeStatus {
		_, _, ok := parseV2SchemaVersion(probe.SchemaVersion)
		return ok
	}
	return true
}

func (JSONV2Codec) DecodeTelemetry(payload []byte, topicGatewayID string) (DecodedTelemetry, error) {
	env, err := DecodeTelemetryEnvelope(payload, topicGatewayID)
	if err != nil {
		return DecodedTelemetry{}, err
	}
	return DecodedTelemetry{Envelope: env}, nil
}

func (JSONV2Codec) DecodeEvent(payload []byte, topicGatewayID string) (EventEnvelope, error) {
	return DecodeEventEnvelope(payload, topicGatewayID)
}

type v2StatusWire struct {
	SchemaVersion string          `json:"schemaVersion"`
	MessageID     string          `json:"messageId"`
	GatewayID     string          `json:"gatewayId"`
	SessionID     string          `json:"sessionId"`
	Sequence      *int64          `json:"sequence"`
	EventTime     json.RawMessage `json:"eventTime"`
	Timestamp     json.RawMessage `json:"timestamp"`
	Online        *bool           `json:"online"`
	TraceID       string          `json:"traceId"`
}

func (JSONV2Codec) DecodeStatus(payload []byte, topicGatewayID string) (StatusEnvelope, error) {
	var wire v2StatusWire
	if err := decodeV2Envelope(payload, &wire); err != nil {
		return StatusEnvelope{}, err
	}
	if _, _, ok := parseV2SchemaVersion(wire.SchemaVersion); !ok {
		return StatusEnvelope{}, fmt.Errorf("unsupported MQTT status schemaVersion %s", wire.SchemaVersion)
	}
	gw := strings.TrimSpace(wire.GatewayID)
	if gw == "" || gw != topicGatewayID {
		return StatusEnvelope{}, errors.New("MQTT topic and status gateway differ")
	}
	if wire.Online == nil {
		return StatusEnvelope{}, errors.New("MQTT status payload missing online boolean")
	}
	status := StatusEnvelope{
		SchemaVersion: strings.TrimSpace(wire.SchemaVersion),
		MessageID:     strings.TrimSpace(wire.MessageID),
		GatewayID:     gw,
		SessionID:     strings.TrimSpace(wire.SessionID),
		Online:        *wire.Online,
		TraceID:       strings.TrimSpace(wire.TraceID),
	}
	if len(status.SessionID) > 256 || strings.ContainsAny(status.SessionID, "\r\n") {
		return StatusEnvelope{}, errors.New("MQTT status sessionId is invalid")
	}
	if len(status.TraceID) > 256 || strings.ContainsAny(status.TraceID, "\r\n") {
		return StatusEnvelope{}, errors.New("MQTT status traceId is invalid")
	}
	if wire.Sequence != nil {
		if *wire.Sequence < 0 {
			return StatusEnvelope{}, errors.New("MQTT status sequence must be non-negative")
		}
		status.Sequence = *wire.Sequence
		status.HasSequence = true
	}
	tsRaw := wire.EventTime
	if len(tsRaw) == 0 {
		tsRaw = wire.Timestamp
	}
	if len(tsRaw) > 0 {
		ts, err := decodeFlexibleTimestamp(tsRaw)
		if err != nil {
			return StatusEnvelope{}, fmt.Errorf("MQTT status eventTime: %w", err)
		}
		status.EventTime = ts
		status.HasEventTime = true
	}
	return status, nil
}

func parseV2SchemaVersion(raw string) (minor int, allowUnknown bool, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == EnvelopeSchemaVersion {
		return 0, false, true
	}
	suffix, hasPrefix := strings.CutPrefix(trimmed, "2.")
	if !hasPrefix || suffix == "" || len(suffix) > 6 || (len(suffix) > 1 && suffix[0] == '0') {
		return 0, false, false
	}
	val := 0
	for i := 0; i < len(suffix); i++ {
		ch := suffix[i]
		if ch < '0' || ch > '9' {
			return 0, false, false
		}
		val = val*10 + int(ch-'0')
	}
	if val < 1 {
		return 0, false, false
	}
	return val, true, true
}

func decodeV2Envelope(payload []byte, destination any) error {
	probe, err := decodeEnvelopeVersion(payload)
	if err == nil {
		if _, allowUnknown, ok := parseV2SchemaVersion(probe.SchemaVersion); ok && allowUnknown {
			return decodeForwardCompatEnvelope(payload, destination)
		}
	}
	return decodeStrictEnvelope(payload, destination)
}

func decodeFlexibleTimestamp(raw json.RawMessage) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, errors.New("timestamp must not be empty or null")
	}
	var ms int64
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return 0, errors.New("invalid timestamp string")
		}
		text = strings.TrimSpace(text)
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return 0, errors.New("timestamp string must be RFC3339")
		}
		ms = parsed.UTC().UnixMilli()
	} else {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		var num json.Number
		if err := dec.Decode(&num); err != nil {
			return 0, errors.New("timestamp must be Unix epoch milliseconds or RFC3339 string")
		}
		var trailing any
		if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
			return 0, errors.New("timestamp contains trailing data")
		}
		parsedInt, intErr := num.Int64()
		if intErr == nil {
			ms = parsedInt
		} else {
			parsedFloat, floatErr := num.Float64()
			if floatErr != nil || math.IsNaN(parsedFloat) || math.IsInf(parsedFloat, 0) || parsedFloat != math.Trunc(parsedFloat) || parsedFloat < 0 || parsedFloat >= float64(int64(1)<<48) {
				return 0, errors.New("timestamp number must be finite integer milliseconds in [0, 2^48)")
			}
			ms = int64(parsedFloat)
		}
	}
	if ms < 0 || ms >= 1<<48 {
		return 0, errors.New("timestamp must be within [0, 2^48) Unix milliseconds")
	}
	return ms, nil
}
