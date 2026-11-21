package adapter

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	ShadowReplicationSchemaVersion = edgecontrol.ShadowReplicationSchemaVersion
	ShadowReplicationTypeBatch     = edgecontrol.ShadowReplicationTypeBatch
	ShadowReplicationTypeAck       = edgecontrol.ShadowReplicationTypeAck
	ShadowOwnerDomainDriverConfig  = edgecontrol.ShadowOwnerDomainDriverConfig
	ShadowAckStatusAcked           = edgecontrol.ShadowAckStatusAcked
	ShadowAckStatusQuarantined     = edgecontrol.ShadowAckStatusQuarantined

	ShadowReplicationMetricName = "hvac_mqtt_shadow_replication_total"
	ShadowReplicationMetricHelp = "MQTT desired/reported shadow replication messages by outcome."
)

type ShadowReplicationEnvelope = edgecontrol.ShadowReplicationEnvelope
type ShadowChangeBatchPayload = edgecontrol.ShadowChangeBatchPayload
type ShadowChangeBatchItem = edgecontrol.ShadowChangeBatchItem
type ShadowChangeAckPayload = edgecontrol.ShadowChangeAckPayload

// ShadowDownlinkTopic returns the per-gateway desired config notification topic.
func ShadowDownlinkTopic(gatewayID string) string {
	return edgecontrol.ShadowDownlinkTopic(gatewayID)
}

// ShadowUplinkTopic returns the per-gateway replication acknowledgement uplink topic.
func ShadowUplinkTopic(gatewayID string) string {
	return edgecontrol.ShadowUplinkTopic(gatewayID)
}

// EncodeShadowChangeBatch builds a strict CHANGE_BATCH replication envelope for desiredRevision.
func EncodeShadowChangeBatch(gatewayID string, desiredRevision int64, desired any, now time.Time) ([]byte, string, error) {
	return edgecontrol.EncodeShadowChangeBatch(gatewayID, desiredRevision, desired, now)
}

// DecodeShadowChangeAck strictly validates an edge CHANGE_ACK uplink envelope against
// contracts/mqtt/edge-replication-envelope.v1.schema.json and topicGatewayID.
func DecodeShadowChangeAck(payload []byte, topicGatewayID string) (ShadowReplicationEnvelope, ShadowChangeAckPayload, error) {
	if len(payload) > maximumEnvelopeBytes {
		return ShadowReplicationEnvelope{}, ShadowChangeAckPayload{}, errors.New("shadow CHANGE_ACK envelope exceeds maximum size")
	}
	return edgecontrol.DecodeShadowChangeAck(payload, strings.TrimSpace(topicGatewayID))
}
