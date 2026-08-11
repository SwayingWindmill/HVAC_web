package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type ProcessingResult struct {
	MessageID   string
	Replay      bool
	PointCount  int
	Accepted    int
	Duplicate   int
	OutOfOrder  int
	Quarantined int
	Rejected    int
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
	scope, err := ParseTelemetryTopic(topic)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	allowed, ok := processor.gatewayScopes[scope.GatewayID]
	if !ok || allowed.TenantID != scope.TenantID || allowed.SiteID != scope.SiteID {
		return ProcessingResult{}, permanentMessage(errors.New("MQTT telemetry Gateway scope is not authorized"))
	}
	envelope, err := DecodeTelemetryEnvelope(payload, scope)
	if err != nil {
		return ProcessingResult{}, permanentMessage(err)
	}
	result := ProcessingResult{MessageID: envelope.MessageID, Replay: envelope.Replay}
	for _, device := range envelope.Devices {
		for _, point := range device.Points {
			partition := sourcePartition(envelope.GatewayID, device.ExternalDeviceID, point.TelemetryKey)
			eventID, eventErr := deterministicPointEventID(point, partition)
			if eventErr != nil {
				return ProcessingResult{}, permanentMessage(eventErr)
			}
			observation := Observation{
				IntegrationInstanceID: processor.integrationInstanceID,
				SourcePath:            "PUSH",
				ExternalEntityType:    "DEVICE",
				ExternalID:            strings.TrimSpace(device.ExternalDeviceID),
				TelemetryKey:          strings.TrimSpace(point.TelemetryKey),
				Value:                 point.Value,
				ValueType:             strings.ToUpper(strings.TrimSpace(point.ValueType)),
				Unit:                  point.Unit,
				SampledAt:             strings.TrimSpace(point.SampledAt),
				SourcePosition: SourcePosition{
					Partition: partition,
					Offset:    int64(point.Sequence),
					EventID:   eventID,
				},
			}
			receipt, acceptErr := processor.runtime.AcceptObservation(ctx, observation)
			if acceptErr != nil {
				return ProcessingResult{}, fmt.Errorf("accept MQTT point %s/%s: %w", device.ExternalDeviceID, point.TelemetryKey, acceptErr)
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
