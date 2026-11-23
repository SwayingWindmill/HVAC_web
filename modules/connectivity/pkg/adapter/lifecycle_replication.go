package adapter

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	LifecycleReplicationMetricName = "hvac_mqtt_factory_reset_total"
	LifecycleReplicationMetricHelp = "MQTT factory-reset lifecycle directive and acknowledgement messages by outcome."
)

var ErrFactoryResetAckRejected = errors.New("FACTORY_RESET_ACK rejected permanently")

// LifecycleDownlinkTopic returns the per-gateway lifecycle / factory-reset directive topic.
func LifecycleDownlinkTopic(gatewayID string) string {
	return edgecontrol.LifecycleDownlinkTopic(gatewayID)
}

// LifecycleUplinkTopic returns the per-gateway lifecycle / factory-reset acknowledgement topic.
func LifecycleUplinkTopic(gatewayID string) string {
	return edgecontrol.LifecycleUplinkTopic(gatewayID)
}

// EncodeFactoryReset builds a strict FACTORY_RESET downlink replication envelope.
func EncodeFactoryReset(gatewayID string, reset edgecontrol.FactoryResetPayload, now time.Time) ([]byte, error) {
	return edgecontrol.EncodeFactoryReset(gatewayID, reset, now)
}

// DecodeFactoryResetAck strictly validates an edge FACTORY_RESET_ACK uplink envelope against topicGatewayID.
func DecodeFactoryResetAck(payload []byte, topicGatewayID string) (edgecontrol.ShadowReplicationEnvelope, edgecontrol.FactoryResetAckPayload, error) {
	if len(payload) > maximumEnvelopeBytes {
		return edgecontrol.ShadowReplicationEnvelope{}, edgecontrol.FactoryResetAckPayload{}, errors.New("FACTORY_RESET_ACK envelope exceeds maximum size")
	}
	return edgecontrol.DecodeFactoryResetAck(payload, strings.TrimSpace(topicGatewayID))
}
