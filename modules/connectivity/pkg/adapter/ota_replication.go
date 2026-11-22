package adapter

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	OTAReplicationMetricName = "hvac_mqtt_ota_replication_total"
	OTAReplicationMetricHelp = "MQTT OTA stage/result replication messages by outcome."
)

var ErrOTAResultRejected = errors.New("OTA result rejected permanently")

// OTADownlinkTopic returns the per-gateway OTA rollout directive topic.
func OTADownlinkTopic(gatewayID string) string {
	return edgecontrol.OTADownlinkTopic(gatewayID)
}

// OTAUplinkTopic returns the per-gateway OTA execution result topic.
func OTAUplinkTopic(gatewayID string) string {
	return edgecontrol.OTAUplinkTopic(gatewayID)
}

// EncodeOTAStage builds a strict OTA_STAGE downlink replication envelope.
func EncodeOTAStage(gatewayID string, stage edgecontrol.OTAStagePayload, now time.Time) ([]byte, error) {
	return edgecontrol.EncodeOTAStage(gatewayID, stage, now)
}

// DecodeOTAResult strictly validates an edge OTA_RESULT uplink envelope against topicGatewayID.
func DecodeOTAResult(payload []byte, topicGatewayID string) (edgecontrol.ShadowReplicationEnvelope, edgecontrol.OTAResultPayload, error) {
	if len(payload) > maximumEnvelopeBytes {
		return edgecontrol.ShadowReplicationEnvelope{}, edgecontrol.OTAResultPayload{}, errors.New("OTA_RESULT envelope exceeds maximum size")
	}
	return edgecontrol.DecodeOTAResult(payload, strings.TrimSpace(topicGatewayID))
}
