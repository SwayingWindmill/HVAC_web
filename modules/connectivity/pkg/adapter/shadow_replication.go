package adapter

import (
	"bytes"
	"crypto/rand"
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
)

const (
	ShadowReplicationSchemaVersion = "1.0"
	ShadowReplicationTypeBatch     = "CHANGE_BATCH"
	ShadowReplicationTypeAck       = "CHANGE_ACK"
	ShadowOwnerDomainDriverConfig  = "DRIVER_CONFIG"
	ShadowAckStatusAcked           = "ACKED"
	ShadowAckStatusQuarantined     = "QUARANTINED"

	ShadowReplicationMetricName = "hvac_mqtt_shadow_replication_total"
	ShadowReplicationMetricHelp = "MQTT desired/reported shadow replication messages by outcome."
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
	digestBytes := sha256.Sum256(payloadBytes)
	payloadDigest := hex.EncodeToString(digestBytes[:])

	nowUTC := now.UTC()
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	sentAt := max(nowUTC.UnixMilli(), 1)
	msgID, err := newShadowReplicationUUIDv7(nowUTC)
	if err != nil {
		return nil, "", err
	}
	deliveryID, err := newShadowReplicationUUIDv7(nowUTC)
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
	if len(wireBytes) > maximumEnvelopeBytes {
		return nil, "", errors.New("shadow replication envelope exceeds maximum MQTT payload size")
	}
	return wireBytes, msgID, nil
}

// DecodeShadowChangeAck strictly validates an edge CHANGE_ACK uplink envelope against
// contracts/mqtt/edge-replication-envelope.v1.schema.json and topicGatewayID.
func DecodeShadowChangeAck(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, ShadowChangeAckPayload, error) {
	var env ShadowReplicationEnvelope
	if err := decodeStrictEnvelope(payload, &env); err != nil {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, err
	}
	if strings.TrimSpace(env.SchemaVersion) != ShadowReplicationSchemaVersion {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, fmt.Errorf("unsupported shadow replication schemaVersion %q", env.SchemaVersion)
	}
	if !replicationUUIDPattern.MatchString(strings.TrimSpace(env.MessageID)) {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow replication messageId must be a UUID")
	}
	edgeID := strings.TrimSpace(env.EdgeID)
	if edgeID == "" || len(edgeID) > 128 || edgeID != topicGatewayID {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow replication edgeId does not match topic gateway")
	}
	if env.SentAt < 1 {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow replication sentAt must be >= 1")
	}
	if strings.TrimSpace(env.Type) != ShadowReplicationTypeAck {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, fmt.Errorf("unexpected shadow replication uplink type %q", env.Type)
	}

	rawAck := bytes.TrimSpace(env.Payload)
	if len(rawAck) == 0 || rawAck[0] != '{' {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK payload must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(rawAck))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var ack ShadowChangeAckPayload
	if err := dec.Decode(&ack); err != nil {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, fmt.Errorf("decode shadow CHANGE_ACK payload: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK payload contains trailing JSON")
	}
	if ack.Cursor < 1 {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK cursor must be >= 1")
	}
	if !replicationUUIDPattern.MatchString(strings.TrimSpace(ack.DeliveryID)) {
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
	if !lowerSHA256HexPattern.MatchString(ack.PayloadDigest) {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK payloadDigest must be 64-character lowercase hex")
	}
	return env, ack, nil
}

func newShadowReplicationUUIDv7(now time.Time) (string, error) {
	var raw [16]byte
	ms := uint64(max(now.UTC().UnixMilli(), 0))
	binary.BigEndian.PutUint64(raw[:8], ms<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate shadow replication UUID: %w", err)
	}
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], raw[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], raw[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], raw[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], raw[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], raw[10:16])
	return string(dst), nil
}
