package edgecontrol

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ShadowReplicationTypeFactoryReset    = "FACTORY_RESET"
	ShadowReplicationTypeFactoryResetAck = "FACTORY_RESET_ACK"

	FactoryResetAckStatusWiped  = "WIPED"
	FactoryResetAckStatusFailed = "FAILED"
)

// LifecycleDownlinkTopic returns the per-gateway lifecycle / factory-reset directive topic.
func LifecycleDownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/lifecycle"
}

// LifecycleUplinkTopic returns the per-gateway lifecycle / factory-reset acknowledgement topic.
func LifecycleUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/lifecycle"
}

// FactoryResetPayload mirrors the FACTORY_RESET payload in edge-replication-envelope.v1.schema.json.
type FactoryResetPayload struct {
	ResetID           string `json:"resetId"`
	TransferID        string `json:"transferId,omitempty"`
	WipeCredentials   bool   `json:"wipeCredentials"`
	WipeConfigShadow  bool   `json:"wipeConfigShadow"`
	WipeOfflineBuffer bool   `json:"wipeOfflineBuffer"`
	Reason            string `json:"reason"`
	IssuedAt          int64  `json:"issuedAt"`
}

// FactoryResetAckPayload mirrors the FACTORY_RESET_ACK payload in edge-replication-envelope.v1.schema.json.
type FactoryResetAckPayload struct {
	ResetID        string   `json:"resetId"`
	Status         string   `json:"status"`
	WipedArtifacts []string `json:"wipedArtifacts"`
	ErrorMessage   string   `json:"errorMessage,omitempty"`
	OccurredAt     int64    `json:"occurredAt"`
}

// Validate checks all FACTORY_RESET invariants against edge-replication-envelope.v1.schema.json.
func (p FactoryResetPayload) Validate() error {
	if !IsReplicationUUID(p.ResetID) {
		return errors.New("FACTORY_RESET requires valid resetId UUID")
	}
	if strings.TrimSpace(p.TransferID) != "" && !IsReplicationUUID(p.TransferID) {
		return errors.New("FACTORY_RESET transferId must be a valid UUID")
	}
	if !p.WipeCredentials && !p.WipeConfigShadow && !p.WipeOfflineBuffer {
		return errors.New("FACTORY_RESET must request wiping at least one artifact class")
	}
	trimmedReason := strings.TrimSpace(p.Reason)
	if trimmedReason == "" || utf8.RuneCountInString(trimmedReason) > 512 {
		return errors.New("FACTORY_RESET reason must be between 1 and 512 characters")
	}
	if p.IssuedAt < 1 {
		return errors.New("FACTORY_RESET issuedAt must be >= 1")
	}
	return nil
}

// Validate checks all FACTORY_RESET_ACK invariants against edge-replication-envelope.v1.schema.json.
func (p FactoryResetAckPayload) Validate() error {
	if !IsReplicationUUID(p.ResetID) {
		return errors.New("FACTORY_RESET_ACK requires valid resetId UUID")
	}
	switch p.Status {
	case FactoryResetAckStatusWiped, FactoryResetAckStatusFailed:
	default:
		return fmt.Errorf("unsupported FACTORY_RESET_ACK status %q", p.Status)
	}
	if p.WipedArtifacts == nil || len(p.WipedArtifacts) > 32 {
		return errors.New("FACTORY_RESET_ACK wipedArtifacts must be a non-nil array of at most 32 items")
	}
	seen := make(map[string]struct{}, len(p.WipedArtifacts))
	for i, item := range p.WipedArtifacts {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 128 {
			return fmt.Errorf("FACTORY_RESET_ACK wipedArtifacts[%d] must be 1..128 characters", i)
		}
		if _, dup := seen[trimmed]; dup {
			return fmt.Errorf("FACTORY_RESET_ACK wipedArtifacts contains duplicate %q", trimmed)
		}
		seen[trimmed] = struct{}{}
	}
	if utf8.RuneCountInString(p.ErrorMessage) > 1024 {
		return errors.New("FACTORY_RESET_ACK errorMessage exceeds 1024 characters")
	}
	if p.OccurredAt < 1 {
		return errors.New("FACTORY_RESET_ACK occurredAt must be >= 1")
	}
	return nil
}

// EncodeFactoryReset encodes a FACTORY_RESET downlink replication envelope.
func EncodeFactoryReset(gatewayID string, reset FactoryResetPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("FACTORY_RESET requires valid edgeId")
	}
	if err := reset.Validate(); err != nil {
		return nil, err
	}
	return encodeReplicationEnvelope(gwID, ShadowReplicationTypeFactoryReset, reset, now)
}

// DecodeFactoryReset decodes and validates a FACTORY_RESET downlink replication envelope.
func DecodeFactoryReset(payload []byte, expectedGatewayID string) (ShadowReplicationEnvelope, FactoryResetPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, expectedGatewayID, ShadowReplicationTypeFactoryReset)
	if err != nil {
		return ShadowReplicationEnvelope{}, FactoryResetPayload{}, err
	}
	var reset FactoryResetPayload
	if err := DecodeStrictShadowObject(env.Payload, &reset, "FACTORY_RESET payload"); err != nil {
		return ShadowReplicationEnvelope{}, FactoryResetPayload{}, err
	}
	if err := reset.Validate(); err != nil {
		return ShadowReplicationEnvelope{}, FactoryResetPayload{}, err
	}
	return env, reset, nil
}

// EncodeFactoryResetAck encodes a FACTORY_RESET_ACK uplink replication envelope.
func EncodeFactoryResetAck(gatewayID string, ack FactoryResetAckPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("FACTORY_RESET_ACK requires valid edgeId")
	}
	if err := ack.Validate(); err != nil {
		return nil, err
	}
	return encodeReplicationEnvelope(gwID, ShadowReplicationTypeFactoryResetAck, ack, now)
}

// DecodeFactoryResetAck decodes and validates a FACTORY_RESET_ACK uplink replication envelope.
func DecodeFactoryResetAck(payload []byte, expectedGatewayID string) (ShadowReplicationEnvelope, FactoryResetAckPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, expectedGatewayID, ShadowReplicationTypeFactoryResetAck)
	if err != nil {
		return ShadowReplicationEnvelope{}, FactoryResetAckPayload{}, err
	}
	var ack FactoryResetAckPayload
	if err := DecodeStrictShadowObject(env.Payload, &ack, "FACTORY_RESET_ACK payload"); err != nil {
		return ShadowReplicationEnvelope{}, FactoryResetAckPayload{}, err
	}
	if err := ack.Validate(); err != nil {
		return ShadowReplicationEnvelope{}, FactoryResetAckPayload{}, err
	}
	return env, ack, nil
}
