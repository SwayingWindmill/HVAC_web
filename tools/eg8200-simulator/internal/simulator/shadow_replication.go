package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	edgeReplicationSchemaVersion = edgecontrol.ShadowReplicationSchemaVersion
	edgeReplicationTypeBatch     = edgecontrol.ShadowReplicationTypeBatch
	edgeReplicationTypeAck       = edgecontrol.ShadowReplicationTypeAck
	edgeShadowAckStatusAcked     = edgecontrol.ShadowAckStatusAcked
	edgeShadowAckStatusQuar      = edgecontrol.ShadowAckStatusQuarantined
)

func mqttShadowDownlinkTopic(gatewayID string) string {
	return edgecontrol.ShadowDownlinkTopic(gatewayID)
}

func mqttShadowUplinkTopic(gatewayID string) string {
	return edgecontrol.ShadowUplinkTopic(gatewayID)
}

type edgeReplicationEnvelope = edgecontrol.ShadowReplicationEnvelope
type edgeChangeBatchPayload = edgecontrol.ShadowChangeBatchPayload
type edgeChangeBatchItem = edgecontrol.ShadowChangeBatchItem
type edgeChangeAckPayload = edgecontrol.ShadowChangeAckPayload

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
	gwID := strings.TrimSpace(cfg.GatewayID)
	_, batch, err := edgecontrol.DecodeShadowChangeBatch(rawPayload, gwID)
	if err != nil {
		return err
	}

	uplinkTopic := mqttShadowUplinkTopic(gwID)
	httpSynced := false
	var httpSyncErr error
	for _, item := range batch.Items {
		ack := edgeChangeAckPayload{
			Cursor:        item.Cursor,
			DeliveryID:    strings.TrimSpace(item.DeliveryID),
			Status:        edgeShadowAckStatusAcked,
			PayloadDigest: item.PayloadDigest,
		}
		if !item.Tombstone {
			if !edgecontrol.VerifyShadowPayloadDigest(item.Payload, item.PayloadDigest) {
				ack.Status = edgeShadowAckStatusQuar
				ack.Reason = "payloadDigest mismatch"
			} else if strings.TrimSpace(cfg.EnrollmentURL) != "" {
				if !httpSynced {
					httpSyncErr = syncConfigShadowHTTP(ctx, cfg, shadow, httpClient)
					httpSynced = true
				}
				if httpSyncErr != nil {
					ack.Status = edgeShadowAckStatusQuar
					ack.Reason = clampShadowRunes(httpSyncErr.Error(), 512)
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

		ackEnvelopeBytes, err := edgecontrol.EncodeShadowChangeAck(gwID, ack, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := publishFn(ctx, uplinkTopic, ackEnvelopeBytes); err != nil {
			return err
		}
	}
	return nil
}
