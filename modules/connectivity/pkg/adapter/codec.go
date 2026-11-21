package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	// CompactSchemaVersionV1 identifies the compact JSON v1.0 edge telemetry/status codec.
	CompactSchemaVersionV1 = "1.0"
)

// DecodedTelemetry wraps a canonical TelemetryEnvelope together with optional
// gateway session metadata carried by compact codecs (such as v1.0 "sid").
type DecodedTelemetry struct {
	Envelope  TelemetryEnvelope
	SessionID string
}

// StatusEnvelope is the canonical representation of /up/status frames across
// compact v1.0, standard v2.0, and legacy gateway status payloads.
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

// DefaultCodecRegistry returns the default adapter codec registry containing both
// JSONV2Codec (schemaVersion "2.0") and CompactJSONV1Codec (v "1.0" + legacy status).
func DefaultCodecRegistry() *CodecRegistry {
	return NewCodecRegistry(JSONV2Codec{}, CompactJSONV1Codec{})
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
	V             string `json:"v"`
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
	probe.V = strings.TrimSpace(probe.V)
	return probe, nil
}

// JSONV2Codec decodes canonical schemaVersion "2.0" telemetry/event/status envelopes.
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
	if probe.V == CompactSchemaVersionV1 || probe.SchemaVersion == CompactSchemaVersionV1 {
		return false
	}
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

// CompactJSONV1Codec decodes compact JSON v1.0 telemetry/status payloads and legacy
// status frames omitting sessionId/sequence/version fields.
type CompactJSONV1Codec struct{}

func (CompactJSONV1Codec) Name() string { return "compact-json-v1" }

func (c CompactJSONV1Codec) CanDecode(messageType string, payload []byte) bool {
	probe, err := decodeEnvelopeVersion(payload)
	if err != nil {
		return false
	}
	return c.canDecodeProbe(messageType, probe)
}

func (CompactJSONV1Codec) canDecodeProbe(messageType string, probe versionProbe) bool {
	if probe.V == CompactSchemaVersionV1 || probe.SchemaVersion == CompactSchemaVersionV1 {
		return messageType == MessageTypeTelemetry || messageType == MessageTypeStatus
	}
	if messageType == MessageTypeStatus && probe.V == "" && probe.SchemaVersion == "" {
		return true
	}
	return false
}

type compactTelemetryWire struct {
	V             string                        `json:"v"`
	SchemaVersion string                        `json:"schemaVersion"`
	MessageID     string                        `json:"messageId"`
	GW            string                        `json:"gw"`
	SID           string                        `json:"sid"`
	Seq           *int64                        `json:"seq"`
	TS            json.RawMessage               `json:"ts"`
	Replay        bool                          `json:"replay"`
	TraceID       string                        `json:"traceId"`
	Devs          map[string]*compactDeviceWire `json:"devs"`
}

type compactDeviceWire struct {
	TS  json.RawMessage    `json:"ts"`
	Pts map[string]any     `json:"pts"`
	Q   map[string]*string `json:"q"`
}

func (CompactJSONV1Codec) DecodeTelemetry(payload []byte, topicGatewayID string) (DecodedTelemetry, error) {
	var wire compactTelemetryWire
	if err := decodeStrictEnvelope(payload, &wire); err != nil {
		return DecodedTelemetry{}, err
	}
	version := strings.TrimSpace(wire.V)
	if version == "" {
		version = strings.TrimSpace(wire.SchemaVersion)
	}
	if version != CompactSchemaVersionV1 {
		return DecodedTelemetry{}, fmt.Errorf("unsupported compact telemetry version %q", version)
	}
	if wire.SchemaVersion != "" && strings.TrimSpace(wire.SchemaVersion) != CompactSchemaVersionV1 {
		return DecodedTelemetry{}, fmt.Errorf("mismatched compact telemetry schemaVersion %q", wire.SchemaVersion)
	}
	gw := strings.TrimSpace(wire.GW)
	if gw == "" || gw != topicGatewayID {
		return DecodedTelemetry{}, errors.New("MQTT topic and compact telemetry gw differ")
	}
	sid := strings.TrimSpace(wire.SID)
	if len(sid) > 256 || strings.ContainsAny(sid, "\r\n") {
		return DecodedTelemetry{}, errors.New("MQTT compact telemetry sid is invalid")
	}
	if wire.Seq == nil || *wire.Seq < 0 {
		return DecodedTelemetry{}, errors.New("MQTT compact telemetry seq must be non-negative")
	}
	if len(wire.TS) == 0 {
		return DecodedTelemetry{}, errors.New("MQTT compact telemetry ts is required")
	}
	envelopeTS, err := decodeFlexibleTimestamp(wire.TS)
	if err != nil {
		return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry ts: %w", err)
	}
	if len(wire.TraceID) > 256 || strings.ContainsAny(wire.TraceID, "\r\n") {
		return DecodedTelemetry{}, errors.New("MQTT compact telemetry traceId is invalid")
	}
	if len(wire.Devs) == 0 || len(wire.Devs) > maximumDevicesPerEnvelope {
		return DecodedTelemetry{}, errors.New("MQTT compact telemetry devs count is invalid")
	}

	deviceKeys := slices.Sorted(maps.Keys(wire.Devs))
	seenDevices := make(map[string]struct{}, len(deviceKeys))
	devices := make([]EnvelopeDevice, 0, len(deviceKeys))
	for _, rawDeviceKey := range deviceKeys {
		dev := wire.Devs[rawDeviceKey]
		deviceID := strings.TrimSpace(rawDeviceKey)
		if deviceID == "" || len(deviceID) > 256 {
			return DecodedTelemetry{}, errors.New("MQTT compact telemetry device key is invalid")
		}
		if _, duplicate := seenDevices[deviceID]; duplicate {
			return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s is duplicated", deviceID)
		}
		seenDevices[deviceID] = struct{}{}
		if dev == nil {
			return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s is null", deviceID)
		}
		deviceTS := envelopeTS
		if len(dev.TS) > 0 {
			deviceTS, err = decodeFlexibleTimestamp(dev.TS)
			if err != nil {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s ts: %w", deviceID, err)
			}
		}
		if len(dev.Pts) == 0 || len(dev.Pts) > maximumPointsPerDevice {
			return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s point count is invalid", deviceID)
		}

		for qCode, qVal := range dev.Q {
			trimmedQCode := strings.TrimSpace(qCode)
			if trimmedQCode == "" || !wirePointCodePattern.MatchString(trimmedQCode) {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s invalid quality point code %q", deviceID, qCode)
			}
			if _, exists := dev.Pts[qCode]; !exists {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s quality entry %s missing point value", deviceID, trimmedQCode)
			}
			if qVal == nil {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s quality for %s must not be null", deviceID, trimmedQCode)
			}
		}

		pointCodes := slices.Sorted(maps.Keys(dev.Pts))
		seenPoints := make(map[string]struct{}, len(pointCodes))
		points := make([]EnvelopePoint, 0, len(pointCodes))
		for _, rawCode := range pointCodes {
			code := strings.TrimSpace(rawCode)
			if !wirePointCodePattern.MatchString(code) {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s invalid point code %q", deviceID, rawCode)
			}
			if _, duplicate := seenPoints[code]; duplicate {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry point %s duplicated for device %s", code, deviceID)
			}
			seenPoints[code] = struct{}{}

			val := dev.Pts[rawCode]
			if err := validateCompactPointValue(val); err != nil {
				return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s point %s: %w", deviceID, code, err)
			}

			qualityCode := uint8(0)
			if rawQ, hasQ := dev.Q[rawCode]; hasQ {
				qualityCode, err = mapCompactQuality(*rawQ)
				if err != nil {
					return DecodedTelemetry{}, fmt.Errorf("MQTT compact telemetry device %s point %s: %w", deviceID, code, err)
				}
			}

			points = append(points, EnvelopePoint{
				Code:    code,
				Value:   val,
				Quality: qualityCode,
			})
		}

		devices = append(devices, EnvelopeDevice{
			DeviceID:        deviceID,
			DeviceTimestamp: deviceTS,
			Points:          points,
		})
	}

	messageID := strings.TrimSpace(wire.MessageID)
	if messageID != "" {
		if !uuidV7Pattern.MatchString(messageID) {
			return DecodedTelemetry{}, errors.New("MQTT compact telemetry messageId must be UUIDv7")
		}
	} else {
		messageID = deterministicUUIDv7(envelopeTS, payload)
	}

	return DecodedTelemetry{
		SessionID: sid,
		Envelope: TelemetryEnvelope{
			SchemaVersion: CompactSchemaVersionV1,
			MessageID:     messageID,
			GatewayID:     gw,
			Timestamp:     envelopeTS,
			Sequence:      uint64(*wire.Seq),
			TraceID:       strings.TrimSpace(wire.TraceID),
			Replay:        wire.Replay,
			Payload: TelemetryPayload{
				Devices: devices,
			},
		},
	}, nil
}

func (CompactJSONV1Codec) DecodeEvent(_ []byte, _ string) (EventEnvelope, error) {
	return EventEnvelope{}, errors.New("compact JSON v1.0 codec does not accept event envelopes")
}

type compactStatusWire struct {
	V             string          `json:"v"`
	SchemaVersion string          `json:"schemaVersion"`
	MessageID     string          `json:"messageId"`
	GW            string          `json:"gw"`
	GatewayID     string          `json:"gatewayId"`
	SID           string          `json:"sid"`
	SessionID     string          `json:"sessionId"`
	Seq           *int64          `json:"seq"`
	Sequence      *int64          `json:"sequence"`
	TS            json.RawMessage `json:"ts"`
	Timestamp     json.RawMessage `json:"timestamp"`
	EventTime     json.RawMessage `json:"eventTime"`
	Online        *bool           `json:"online"`
	TraceID       string          `json:"traceId"`
}

func (CompactJSONV1Codec) DecodeStatus(payload []byte, topicGatewayID string) (StatusEnvelope, error) {
	var wire compactStatusWire
	if err := decodeStrictEnvelope(payload, &wire); err != nil {
		return StatusEnvelope{}, err
	}
	if wire.Online == nil {
		return StatusEnvelope{}, errors.New("MQTT status payload missing online boolean")
	}

	version := strings.TrimSpace(wire.V)
	if version == "" {
		version = strings.TrimSpace(wire.SchemaVersion)
	}
	if version != "" && version != CompactSchemaVersionV1 {
		return StatusEnvelope{}, fmt.Errorf("unsupported MQTT compact status version %q", version)
	}

	gw := strings.TrimSpace(wire.GW)
	if gw == "" {
		gw = strings.TrimSpace(wire.GatewayID)
	}
	if gw != "" && gw != topicGatewayID {
		return StatusEnvelope{}, errors.New("MQTT topic and status gateway differ")
	}
	if gw == "" {
		// Compact v1.0 explicitly includes "gw"; legacy payloads may omit it.
		if version == CompactSchemaVersionV1 {
			return StatusEnvelope{}, errors.New("MQTT compact status gw is required")
		}
		gw = topicGatewayID
	}

	sid := strings.TrimSpace(wire.SID)
	if sid == "" {
		sid = strings.TrimSpace(wire.SessionID)
	}
	if len(sid) > 256 || strings.ContainsAny(sid, "\r\n") {
		return StatusEnvelope{}, errors.New("MQTT status sid is invalid")
	}

	traceID := strings.TrimSpace(wire.TraceID)
	if len(traceID) > 256 || strings.ContainsAny(traceID, "\r\n") {
		return StatusEnvelope{}, errors.New("MQTT status traceId is invalid")
	}

	status := StatusEnvelope{
		SchemaVersion: version,
		MessageID:     strings.TrimSpace(wire.MessageID),
		GatewayID:     gw,
		SessionID:     sid,
		Online:        *wire.Online,
		TraceID:       traceID,
	}

	seqPtr := wire.Seq
	if seqPtr == nil {
		seqPtr = wire.Sequence
	}
	if seqPtr != nil {
		if *seqPtr < 0 {
			return StatusEnvelope{}, errors.New("MQTT status seq must be non-negative")
		}
		status.Sequence = *seqPtr
		status.HasSequence = true
	}

	tsRaw := firstNonEmptyRaw(wire.TS, wire.EventTime, wire.Timestamp)
	if len(tsRaw) > 0 {
		ts, err := decodeFlexibleTimestamp(tsRaw)
		if err != nil {
			return StatusEnvelope{}, fmt.Errorf("MQTT status ts: %w", err)
		}
		status.EventTime = ts
		status.HasEventTime = true
	}
	return status, nil
}

func firstNonEmptyRaw(candidates ...json.RawMessage) json.RawMessage {
	for _, raw := range candidates {
		if len(bytes.TrimSpace(raw)) > 0 {
			return raw
		}
	}
	return nil
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

func validateCompactPointValue(value any) error {
	valueType, err := wireValueType(value)
	if err != nil {
		return err
	}
	if valueType == "JSON" {
		return errors.New("point value must be a finite number, string, or boolean")
	}
	return nil
}

func mapCompactQuality(raw string) (uint8, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "GOOD":
		return 0, nil
	case "BAD":
		return 1, nil
	case "UNCERTAIN":
		return 2, nil
	default:
		return 0, fmt.Errorf("invalid compact quality %q", raw)
	}
}

func deterministicUUIDv7(tsMillis int64, payload []byte) string {
	digest := sha256.Sum256(payload)
	var b [16]byte
	uMillis := uint64(tsMillis)
	b[0] = byte(uMillis >> 40)
	b[1] = byte(uMillis >> 32)
	b[2] = byte(uMillis >> 24)
	b[3] = byte(uMillis >> 16)
	b[4] = byte(uMillis >> 8)
	b[5] = byte(uMillis)
	copy(b[6:], digest[:10])
	// Set UUID version 7 (0111) and RFC 9562 variant (10xx).
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16],
	)
}
