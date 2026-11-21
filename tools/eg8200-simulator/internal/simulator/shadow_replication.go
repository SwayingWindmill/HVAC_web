package simulator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	edgeReplicationSchemaVersion = "1.0"
	edgeReplicationTypeBatch     = "CHANGE_BATCH"
	edgeReplicationTypeAck       = "CHANGE_ACK"
	edgeShadowAckStatusAcked     = "ACKED"
	edgeShadowAckStatusQuar      = "QUARANTINED"
	maxEdgeReplicationBytes      = 1 << 20
)

var (
	edgeReplicationUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	edgeLowerHexDigestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func mqttShadowDownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/shadow"
}

func mqttShadowUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/shadow"
}

type edgeReplicationEnvelope struct {
	SchemaVersion string          `json:"schemaVersion"`
	MessageID     string          `json:"messageId"`
	EdgeID        string          `json:"edgeId"`
	SentAt        int64           `json:"sentAt"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

type edgeChangeBatchPayload struct {
	Items []edgeChangeBatchItem `json:"items"`
}

type edgeChangeBatchItem struct {
	Cursor         int64           `json:"cursor"`
	DeliveryID     string          `json:"deliveryId"`
	OwnerDomain    string          `json:"ownerDomain"`
	OrderingKey    string          `json:"orderingKey"`
	SourceRevision int64           `json:"sourceRevision"`
	PayloadDigest  string          `json:"payloadDigest"`
	Tombstone      bool            `json:"tombstone"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

type edgeChangeAckPayload struct {
	Cursor               int64  `json:"cursor"`
	DeliveryID           string `json:"deliveryId"`
	Status               string `json:"status"`
	AppliedOwnerRevision *int64 `json:"appliedOwnerRevision,omitempty"`
	Reason               string `json:"reason,omitempty"`
	PayloadDigest        string `json:"payloadDigest"`
}

func decodeEdgeReplicationStrictJSON(raw []byte, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > maxEdgeReplicationBytes {
		return errors.New("edge replication payload size out of bounds")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON in edge replication envelope")
	}
	return nil
}

func verifyShadowPayloadDigest(raw json.RawMessage, expectedHex string) bool {
	if !edgeLowerHexDigestPattern.MatchString(expectedHex) {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	sum := sha256.Sum256(trimmed)
	if hex.EncodeToString(sum[:]) == expectedHex {
		return true
	}
	var compact bytes.Buffer
	if json.Compact(&compact, trimmed) != nil {
		return false
	}
	compactSum := sha256.Sum256(compact.Bytes())
	return hex.EncodeToString(compactSum[:]) == expectedHex
}

func handleDownlinkShadowMessage(
	ctx context.Context,
	cfg MQTTGatewayConfig,
	shadow *edgeConfigShadow,
	httpClient *http.Client,
	publishFn func(context.Context, string, []byte) error,
	rawPayload []byte,
) error {
	if shadow == nil || publishFn == nil {
		return errors.New("shadow replication handler requires initialized shadow and publisher")
	}
	var env edgeReplicationEnvelope
	if err := decodeEdgeReplicationStrictJSON(rawPayload, &env); err != nil {
		return err
	}
	gwID := strings.TrimSpace(cfg.GatewayID)
	if strings.TrimSpace(env.SchemaVersion) != edgeReplicationSchemaVersion {
		return fmt.Errorf("unsupported shadow replication schemaVersion %q", env.SchemaVersion)
	}
	if !edgeReplicationUUIDPattern.MatchString(strings.TrimSpace(env.MessageID)) || env.SentAt < 1 {
		return errors.New("invalid shadow replication envelope header")
	}
	if strings.TrimSpace(env.EdgeID) != gwID {
		return errors.New("shadow replication edgeId does not match gatewayId")
	}
	if strings.TrimSpace(env.Type) != edgeReplicationTypeBatch {
		return fmt.Errorf("unexpected downlink shadow replication type %q", env.Type)
	}
	var batch edgeChangeBatchPayload
	if err := decodeEdgeReplicationStrictJSON(env.Payload, &batch); err != nil {
		return err
	}
	if len(batch.Items) == 0 {
		return errors.New("shadow CHANGE_BATCH items must not be empty")
	}

	uplinkTopic := mqttShadowUplinkTopic(gwID)
	for _, item := range batch.Items {
		if item.Cursor < 1 || !edgeReplicationUUIDPattern.MatchString(strings.TrimSpace(item.DeliveryID)) || item.SourceRevision < 1 {
			continue
		}
		ack := edgeChangeAckPayload{
			Cursor:        item.Cursor,
			DeliveryID:    strings.TrimSpace(item.DeliveryID),
			Status:        edgeShadowAckStatusAcked,
			PayloadDigest: item.PayloadDigest,
		}
		if !item.Tombstone {
			if !verifyShadowPayloadDigest(item.Payload, item.PayloadDigest) {
				ack.Status = edgeShadowAckStatusQuar
				ack.Reason = "payloadDigest mismatch"
			} else if strings.TrimSpace(cfg.EnrollmentURL) != "" {
				if err := syncConfigShadowHTTP(ctx, cfg, shadow, httpClient); err != nil {
					ack.Status = edgeShadowAckStatusQuar
					ack.Reason = clampShadowRunes(err.Error(), 512)
				} else if applied := shadow.snapshot().AppliedRevision; applied > 0 {
					rev := applied
					ack.AppliedOwnerRevision = &rev
				}
			} else {
				var desired GatewayDesiredConfig
				if err := json.Unmarshal(item.Payload, &desired); err != nil {
					ack.Status = edgeShadowAckStatusQuar
					ack.Reason = clampShadowRunes("decode inline desired shadow: "+err.Error(), 512)
				} else {
					if desired.DesiredRevision == 0 {
						desired.DesiredRevision = item.SourceRevision
					}
					rep, err := shadow.applyDesired(ctx, desired)
					if err != nil {
						ack.Status = edgeShadowAckStatusQuar
						ack.Reason = clampShadowRunes(err.Error(), 512)
					} else if rep.AppliedStatus == shadowAppliedStatusRejected {
						ack.Status = edgeShadowAckStatusQuar
						if rep.ErrorMessage != nil && strings.TrimSpace(*rep.ErrorMessage) != "" {
							ack.Reason = clampShadowRunes(*rep.ErrorMessage, 512)
						} else {
							ack.Reason = "edge config shadow rejected"
						}
					} else if applied := shadow.snapshot().AppliedRevision; applied > 0 {
						rev := applied
						ack.AppliedOwnerRevision = &rev
					}
				}
			}
		}

		now := time.Now().UTC()
		msgID, err := newUUIDV7(now)
		if err != nil {
			return err
		}
		ackPayloadBytes, err := json.Marshal(ack)
		if err != nil {
			return err
		}
		ackEnvelopeBytes, err := json.Marshal(edgeReplicationEnvelope{
			SchemaVersion: edgeReplicationSchemaVersion,
			MessageID:     msgID,
			EdgeID:        gwID,
			SentAt:        max(now.UnixMilli(), 1),
			Type:          edgeReplicationTypeAck,
			Payload:       ackPayloadBytes,
		})
		if err != nil {
			return err
		}
		if err := publishFn(ctx, uplinkTopic, ackEnvelopeBytes); err != nil {
			return err
		}
	}
	return nil
}
