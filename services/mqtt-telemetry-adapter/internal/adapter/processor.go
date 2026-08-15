package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ProcessingResult struct {
	MessageID    string
	MessageType  string
	Replay       bool
	PointCount   int
	EvidenceCount int
	Accepted     int
	Duplicate    int
	OutOfOrder   int
	Quarantined  int
	Rejected     int
}

type Processor struct {
	integrationInstanceID string
	gatewayScopes         map[string]TopicScope
	runtime               RuntimeClient
}

func NewProcessor(integrationInstanceID string, gatewayScopes []GatewayScopeConfig, runtime RuntimeClient) (*Processor, error) {
	integrationInstanceID = strings.TrimSpace(integrationInstanceID)
	if !uuidV7Pattern.MatchString(integrationInstanceID) || len(gatewayScopes) == 0 || runtime == nil {
		return nil, errors.New("MQTT telemetry processor dependencies are invalid")
	}
	allowed := make(map[string]TopicScope, len(gatewayScopes))
	for _, scope := range gatewayScopes {
		gatewayID := strings.TrimSpace(scope.GatewayID)
		if !validGatewayID(gatewayID) || !uuidV7Pattern.MatchString(strings.TrimSpace(scope.TenantID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(scope.SiteID)) {
			return nil, errors.New("MQTT telemetry Gateway scope is invalid")
		}
		if _, duplicate := allowed[gatewayID]; duplicate {
			return nil, fmt.Errorf("MQTT telemetry Gateway scope %s is duplicated", gatewayID)
		}
		allowed[gatewayID] = TopicScope{GatewayID: gatewayID, TenantID: strings.TrimSpace(scope.TenantID), SiteID: strings.TrimSpace(scope.SiteID)}
	}
	return &Processor{integrationInstanceID: integrationInstanceID, gatewayScopes: allowed, runtime: runtime}, nil
}

func (processor *Processor) Process(ctx context.Context, topic string, payload []byte) (ProcessingResult, error) {
	messageTopic, err := ParseMessageTopic(topic)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	allowed, ok := processor.gatewayScopes[messageTopic.GatewayID]
	if !ok || allowed.TenantID != messageTopic.TenantID || allowed.SiteID != messageTopic.SiteID {
		return ProcessingResult{}, permanentMessage(errors.New("MQTT Gateway scope is not authorized"))
	}
	switch messageTopic.MessageType {
	case MessageTypeTelemetry:
		return processor.processTelemetry(ctx, messageTopic.TopicScope, payload)
	case MessageTypeState:
		return processor.processState(ctx, messageTopic.TopicScope, payload)
	case MessageTypeHeartbeat:
		return processor.processHeartbeat(ctx, messageTopic.TopicScope, payload)
	case MessageTypeEvent:
		return processor.processEvent(ctx, messageTopic.TopicScope, payload)
	default:
		return ProcessingResult{}, permanentMessage(errors.New("unsupported MQTT uplink message type"))
	}
}

func (processor *Processor) processTelemetry(ctx context.Context, scope TopicScope, payload []byte) (ProcessingResult, error) {
	envelope, err := DecodeTelemetryEnvelope(payload, scope)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	result := ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeTelemetry, Replay: envelope.Replay}
	for _, device := range envelope.Payload.Devices {
		for _, point := range device.Points {
			partition := sourcePartition(envelope.GatewayID, device.DeviceID, point.Code)
			eventID, eventErr := deterministicPointEventID(envelope.MessageID, device.DeviceTimestamp, point, partition)
			if eventErr != nil {
				return ProcessingResult{}, permanentMessage(eventErr)
			}
			valueType, typeErr := wireValueType(point.Value)
			if typeErr != nil {
				return ProcessingResult{}, permanentMessage(typeErr)
			}
			observation := Observation{
				IntegrationInstanceID: processor.integrationInstanceID,
				SourcePath: "PUSH", ExternalEntityType: "DEVICE", ExternalID: strings.TrimSpace(device.DeviceID), TelemetryKey: strings.TrimSpace(point.Code),
				Value: point.Value, ValueType: valueType, Unit: point.Unit, WireQuality: point.Quality, SampledAt: unixMillisRFC3339(device.DeviceTimestamp),
				SourcePosition: SourcePosition{Partition: partition, Offset: int64(envelope.Sequence), EventID: eventID},
			}
			receipt, acceptErr := processor.runtime.AcceptObservation(ctx, observation)
			if acceptErr != nil {
				return ProcessingResult{}, fmt.Errorf("accept MQTT point %s/%s: %w", device.DeviceID, point.Code, acceptErr)
			}
			result.PointCount++
			switch receipt.Status {
			case "ACCEPTED":
				result.Accepted++
			case "DUPLICATE":
				result.Duplicate++
			case "OUT_OF_ORDER":
				result.OutOfOrder++
			case "QUARANTINED":
				result.Quarantined++
			case "REJECTED":
				result.Rejected++
			default:
				return ProcessingResult{}, fmt.Errorf("unexpected S2 receipt status %s", receipt.Status)
			}
		}
	}
	return result, nil
}

func (processor *Processor) processState(ctx context.Context, scope TopicScope, payload []byte) (ProcessingResult, error) {
	envelope, err := DecodeStateEnvelope(payload, scope)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	rawPayload, _ := json.Marshal(envelope.Payload)
	if err = processor.runtime.AcceptGatewayEvidence(ctx, GatewayEvidence{
		IntegrationInstanceID: processor.integrationInstanceID, TenantID: scope.TenantID, SiteID: scope.SiteID, GatewayID: scope.GatewayID,
		MessageID: envelope.MessageID, EvidenceType: "STATE", ObservedAt: unixMillisRFC3339(envelope.Timestamp), Sequence: int64(envelope.Sequence), Payload: rawPayload,
	}); err != nil {
		return ProcessingResult{}, fmt.Errorf("accept MQTT state evidence: %w", err)
	}
	result := ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeState, EvidenceCount: 1}
	for _, device := range envelope.Payload.Devices {
		// Wire reportedOnline is evidence only. A positive report contributes
		// SOURCE_ACTIVITY; a negative report never directly forces Cloud OFFLINE.
		if device.Online == nil || !*device.Online {
			continue
		}
		if err = processor.emitSourceActivity(ctx, envelope.MessageID, envelope.Timestamp, device.DeviceID, "state"); err != nil {
			return ProcessingResult{}, err
		}
		result.EvidenceCount++
	}
	return result, nil
}

func (processor *Processor) processHeartbeat(ctx context.Context, scope TopicScope, payload []byte) (ProcessingResult, error) {
	envelope, err := DecodeHeartbeatEnvelope(payload, scope)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	rawPayload, _ := json.Marshal(envelope.Payload)
	if err = processor.runtime.AcceptGatewayEvidence(ctx, GatewayEvidence{
		IntegrationInstanceID: processor.integrationInstanceID, TenantID: scope.TenantID, SiteID: scope.SiteID, GatewayID: scope.GatewayID,
		MessageID: envelope.MessageID, EvidenceType: "HEARTBEAT", ObservedAt: unixMillisRFC3339(envelope.Timestamp), Sequence: int64(envelope.Sequence), Payload: rawPayload,
	}); err != nil {
		return ProcessingResult{}, fmt.Errorf("accept MQTT heartbeat evidence: %w", err)
	}
	result := ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeHeartbeat, EvidenceCount: 1}
	for _, deviceID := range envelope.Payload.ConnectedDevices {
		if err = processor.emitSourceActivity(ctx, envelope.MessageID, envelope.Timestamp, deviceID, "heartbeat"); err != nil {
			return ProcessingResult{}, err
		}
		result.EvidenceCount++
	}
	// OfflineDevices is deliberately not mapped to EXPLICIT_DISCONNECT. The
	// authoritative Presence Engine applies heartbeat freshness/timeout policy.
	return result, nil
}

func (processor *Processor) processEvent(ctx context.Context, scope TopicScope, payload []byte) (ProcessingResult, error) {
	envelope, err := DecodeEventEnvelope(payload, scope)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	if err = processor.runtime.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		IntegrationInstanceID: processor.integrationInstanceID, TenantID: scope.TenantID, SiteID: scope.SiteID, GatewayID: scope.GatewayID,
		MessageID: envelope.MessageID, Sequence: int64(envelope.Sequence), EventType: strings.TrimSpace(envelope.Payload.EventType),
		SourceType: strings.TrimSpace(envelope.Payload.SourceType), SourceID: strings.TrimSpace(envelope.Payload.SourceID), EventTime: unixMillisRFC3339(envelope.Payload.EventTime),
		Severity: strings.ToUpper(strings.TrimSpace(envelope.Payload.Severity)), Data: append(json.RawMessage(nil), envelope.Payload.Data...),
	}); err != nil {
		return ProcessingResult{}, fmt.Errorf("accept MQTT runtime event: %w", err)
	}
	return ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeEvent, EvidenceCount: 1}, nil
}

func (processor *Processor) emitSourceActivity(ctx context.Context, messageID string, observedAt int64, deviceID, discriminator string) error {
	sourceEventID, err := deterministicEvidenceEventID(messageID, observedAt, discriminator+":"+strings.TrimSpace(deviceID))
	if err != nil {
		return permanentMessage(err)
	}
	_, err = processor.runtime.AcceptPresenceEvidence(ctx, PresenceEvidence{
		IntegrationInstanceID: processor.integrationInstanceID, ExternalEntityType: "DEVICE", ExternalID: strings.TrimSpace(deviceID),
		SignalType: "SOURCE_ACTIVITY", ObservedAt: unixMillisRFC3339(observedAt), SourceEventID: sourceEventID,
	})
	if err != nil {
		return fmt.Errorf("accept MQTT Presence evidence for device %s: %w", deviceID, err)
	}
	return nil
}
