package edgecontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	ShadowReplicationSchemaVersion = "1.0"
	ShadowReplicationTypeBatch     = "CHANGE_BATCH"
	ShadowReplicationTypeAck       = "CHANGE_ACK"
	ShadowOwnerDomainDriverConfig  = "DRIVER_CONFIG"
	ShadowAckStatusAcked           = "ACKED"
	ShadowAckStatusQuarantined     = "QUARANTINED"

	MaximumShadowReplicationBytes = 1024 * 1024
)

var (
	replicationUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	lowerSHA256HexPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// ShadowDownlinkTopic returns the per-gateway desired config notification topic.
func ShadowDownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/shadow"
}

// ShadowUplinkTopic returns the per-gateway replication acknowledgement uplink topic.
func ShadowUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/shadow"
}

// ShadowReplicationEnvelope mirrors contracts/mqtt/edge-replication-envelope.v1.schema.json.
type ShadowReplicationEnvelope struct {
	SchemaVersion string          `json:"schemaVersion"`
	MessageID     string          `json:"messageId"`
	EdgeID        string          `json:"edgeId"`
	SentAt        int64           `json:"sentAt"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

type ShadowChangeBatchPayload struct {
	Items []ShadowChangeBatchItem `json:"items"`
}

type ShadowChangeBatchItem struct {
	Cursor         int64           `json:"cursor"`
	DeliveryID     string          `json:"deliveryId"`
	OwnerDomain    string          `json:"ownerDomain"`
	OrderingKey    string          `json:"orderingKey"`
	SourceRevision int64           `json:"sourceRevision"`
	PayloadDigest  string          `json:"payloadDigest"`
	Tombstone      bool            `json:"tombstone"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

type ShadowChangeAckPayload struct {
	Cursor               int64  `json:"cursor"`
	DeliveryID           string `json:"deliveryId"`
	Status               string `json:"status"`
	AppliedOwnerRevision *int64 `json:"appliedOwnerRevision,omitempty"`
	Reason               string `json:"reason,omitempty"`
	PayloadDigest        string `json:"payloadDigest"`
}

// VerifyShadowPayloadDigest verifies expectedHex (lowercase SHA-256 hex) against
// raw JSON bytes or their compacted representation.
func VerifyShadowPayloadDigest(raw json.RawMessage, expectedHex string) bool {
	if !IsLowerSHA256Hex(expectedHex) {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	if ComputeShadowPayloadDigest(trimmed) == expectedHex {
		return true
	}
	var compact bytes.Buffer
	if json.Compact(&compact, trimmed) != nil {
		return false
	}
	return ComputeShadowPayloadDigest(compact.Bytes()) == expectedHex
}

// ComputeShadowPayloadDigest returns the lowercase SHA-256 hex digest of payload.
func ComputeShadowPayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// IsReplicationUUID reports whether raw matches canonical UUID format.
func IsReplicationUUID(raw string) bool {
	return replicationUUIDPattern.MatchString(strings.TrimSpace(raw))
}

// IsLowerSHA256Hex reports whether raw is a 64-character lowercase SHA-256 hex string.
func IsLowerSHA256Hex(raw string) bool {
	return lowerSHA256HexPattern.MatchString(raw)
}

// DecodeStrictShadowObject decodes a single JSON object from raw with unknown fields disallowed.
func DecodeStrictShadowObject(raw []byte, target any, label string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%s must be a JSON object", label)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s contains trailing JSON", label)
	}
	return nil
}

// EncodeShadowChangeBatch builds a strict CHANGE_BATCH replication envelope for desiredRevision.
func EncodeShadowChangeBatch(gatewayID string, desiredRevision int64, desired any, now time.Time) ([]byte, string, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, "", errors.New("shadow replication requires valid edgeId")
	}
	if desiredRevision < 1 {
		return nil, "", errors.New("shadow replication desiredRevision must be >= 1")
	}
	if desired == nil {
		return nil, "", errors.New("shadow replication desired config is required")
	}
	payloadBytes, err := json.Marshal(desired)
	if err != nil {
		return nil, "", fmt.Errorf("marshal shadow replication desired config: %w", err)
	}
	payloadDigest := ComputeShadowPayloadDigest(payloadBytes)

	nowUTC := now.UTC()
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	sentAt := max(nowUTC.UnixMilli(), 1)
	msgID, err := NewReplicationUUIDv7(nowUTC)
	if err != nil {
		return nil, "", err
	}
	deliveryID, err := NewReplicationUUIDv7(nowUTC)
	if err != nil {
		return nil, "", err
	}

	batchBytes, err := json.Marshal(ShadowChangeBatchPayload{
		Items: []ShadowChangeBatchItem{
			{
				Cursor:         desiredRevision,
				DeliveryID:     deliveryID,
				OwnerDomain:    ShadowOwnerDomainDriverConfig,
				OrderingKey:    "gateway-shadow:" + gwID,
				SourceRevision: desiredRevision,
				PayloadDigest:  payloadDigest,
				Tombstone:      false,
				Payload:        json.RawMessage(payloadBytes),
			},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("marshal shadow replication batch: %w", err)
	}

	wireBytes, err := json.Marshal(ShadowReplicationEnvelope{
		SchemaVersion: ShadowReplicationSchemaVersion,
		MessageID:     msgID,
		EdgeID:        gwID,
		SentAt:        sentAt,
		Type:          ShadowReplicationTypeBatch,
		Payload:       json.RawMessage(batchBytes),
	})
	if err != nil {
		return nil, "", fmt.Errorf("marshal shadow replication envelope: %w", err)
	}
	if len(wireBytes) > MaximumShadowReplicationBytes {
		return nil, "", errors.New("shadow replication envelope exceeds maximum MQTT payload size")
	}
	return wireBytes, msgID, nil
}

// DecodeShadowReplicationEnvelope validates common fields on an edge-replication envelope.
func DecodeShadowReplicationEnvelope(payload []byte, expectedEdgeID, expectedType string) (ShadowReplicationEnvelope, error) {
	if len(payload) > MaximumShadowReplicationBytes {
		return ShadowReplicationEnvelope{}, errors.New("shadow replication envelope exceeds maximum size")
	}
	var env ShadowReplicationEnvelope
	if err := DecodeStrictShadowObject(payload, &env, "shadow replication envelope"); err != nil {
		return ShadowReplicationEnvelope{}, err
	}
	if strings.TrimSpace(env.SchemaVersion) != ShadowReplicationSchemaVersion {
		return ShadowReplicationEnvelope{}, fmt.Errorf("unsupported shadow replication schemaVersion %q", env.SchemaVersion)
	}
	if !IsReplicationUUID(env.MessageID) {
		return ShadowReplicationEnvelope{}, errors.New("shadow replication messageId must be a UUID")
	}
	edgeID := strings.TrimSpace(env.EdgeID)
	if edgeID == "" || len(edgeID) > 128 {
		return ShadowReplicationEnvelope{}, errors.New("shadow replication edgeId is required")
	}
	if wantEdge := strings.TrimSpace(expectedEdgeID); wantEdge != "" && edgeID != wantEdge {
		return ShadowReplicationEnvelope{}, errors.New("shadow replication edgeId does not match expected gateway")
	}
	if env.SentAt < 1 {
		return ShadowReplicationEnvelope{}, errors.New("shadow replication sentAt must be >= 1")
	}
	if wantType := strings.TrimSpace(expectedType); wantType != "" && strings.TrimSpace(env.Type) != wantType {
		return ShadowReplicationEnvelope{}, fmt.Errorf("unexpected shadow replication type %q", env.Type)
	}
	return env, nil
}

// DecodeShadowChangeBatch strictly validates a downlink CHANGE_BATCH envelope and every item inside it.
func DecodeShadowChangeBatch(payload []byte, expectedGatewayID string) (ShadowReplicationEnvelope, ShadowChangeBatchPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, expectedGatewayID, ShadowReplicationTypeBatch)
	if err != nil {
		return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, err
	}
	var batch ShadowChangeBatchPayload
	if err := DecodeStrictShadowObject(env.Payload, &batch, "shadow CHANGE_BATCH payload"); err != nil {
		return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, err
	}
	if len(batch.Items) == 0 {
		return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, errors.New("shadow CHANGE_BATCH items must not be empty")
	}
	for i, item := range batch.Items {
		if item.Cursor < 1 {
			return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, fmt.Errorf("shadow CHANGE_BATCH item[%d] cursor must be >= 1", i)
		}
		if !IsReplicationUUID(item.DeliveryID) {
			return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, fmt.Errorf("shadow CHANGE_BATCH item[%d] deliveryId must be a UUID", i)
		}
		if item.SourceRevision < 1 {
			return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, fmt.Errorf("shadow CHANGE_BATCH item[%d] sourceRevision must be >= 1", i)
		}
		if !IsLowerSHA256Hex(item.PayloadDigest) {
			return ShadowReplicationEnvelope{}, ShadowChangeBatchPayload{}, fmt.Errorf("shadow CHANGE_BATCH item[%d] payloadDigest must be lowercase sha256 hex", i)
		}
	}
	return env, batch, nil
}

// EncodeShadowChangeAck encodes a validated CHANGE_ACK uplink envelope.
func EncodeShadowChangeAck(gatewayID string, ack ShadowChangeAckPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("shadow CHANGE_ACK requires valid edgeId")
	}
	if ack.Cursor < 1 || !IsReplicationUUID(ack.DeliveryID) || !IsLowerSHA256Hex(ack.PayloadDigest) {
		return nil, errors.New("shadow CHANGE_ACK requires valid cursor, deliveryId, and payloadDigest")
	}
	switch ack.Status {
	case ShadowAckStatusAcked, ShadowAckStatusQuarantined:
	default:
		return nil, fmt.Errorf("unsupported shadow CHANGE_ACK status %q", ack.Status)
	}
	nowUTC := now.UTC()
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	msgID, err := NewReplicationUUIDv7(nowUTC)
	if err != nil {
		return nil, err
	}
	ackBytes, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("marshal shadow CHANGE_ACK payload: %w", err)
	}
	wireBytes, err := json.Marshal(ShadowReplicationEnvelope{
		SchemaVersion: ShadowReplicationSchemaVersion,
		MessageID:     msgID,
		EdgeID:        gwID,
		SentAt:        max(nowUTC.UnixMilli(), 1),
		Type:          ShadowReplicationTypeAck,
		Payload:       json.RawMessage(ackBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal shadow CHANGE_ACK envelope: %w", err)
	}
	if len(wireBytes) > MaximumShadowReplicationBytes {
		return nil, errors.New("shadow CHANGE_ACK envelope exceeds maximum MQTT payload size")
	}
	return wireBytes, nil
}

// DecodeShadowChangeAck strictly validates an edge CHANGE_ACK uplink envelope against
// contracts/mqtt/edge-replication-envelope.v1.schema.json and topicGatewayID.
func DecodeShadowChangeAck(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, ShadowChangeAckPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, topicGatewayID, ShadowReplicationTypeAck)
	if err != nil {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, err
	}
	var ack ShadowChangeAckPayload
	if err := DecodeStrictShadowObject(env.Payload, &ack, "shadow CHANGE_ACK payload"); err != nil {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, err
	}
	if ack.Cursor < 1 {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK cursor must be >= 1")
	}
	if !IsReplicationUUID(ack.DeliveryID) {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK deliveryId must be a UUID")
	}
	switch ack.Status {
	case ShadowAckStatusAcked, ShadowAckStatusQuarantined:
	default:
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, fmt.Errorf("unsupported shadow CHANGE_ACK status %q", ack.Status)
	}
	if ack.AppliedOwnerRevision != nil && *ack.AppliedOwnerRevision < 1 {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK appliedOwnerRevision must be >= 1")
	}
	if !IsLowerSHA256Hex(ack.PayloadDigest) {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK payloadDigest must be 64-character lowercase hex")
	}
	return env, ack, nil
}

// NewReplicationUUIDv7 generates a lowercase RFC 9562 UUIDv7 using timestamp now.
func NewReplicationUUIDv7(now time.Time) (string, error) {
	ts := now.UTC()
	if ts.UnixMilli() < 0 {
		ts = time.UnixMilli(0).UTC()
	}
	id, err := uuidv7.NewAt(ts)
	if err != nil {
		return "", fmt.Errorf("generate shadow replication UUID: %w", err)
	}
	return id, nil
}

// DeterministicReplicationUUIDv7 derives a deterministic RFC 9562 UUIDv7 from now
// and canonical keyParts so offline telemetry replays preserve cloud idempotency keys.
func DeterministicReplicationUUIDv7(now time.Time, keyParts ...string) string {
	ms := uint64(max(now.UTC().UnixMilli(), 0))
	h := sha256.New()
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], ms)
	_, _ = h.Write(lenBuf[:])
	for _, part := range keyParts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(part)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(part))
	}
	sum := h.Sum(nil)

	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], ms<<16)
	copy(raw[6:], sum[:10])
	return uuidv7.FormatRaw(raw)
}
