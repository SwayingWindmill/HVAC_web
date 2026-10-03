package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ProcessingResult struct {
	MessageID   string
	MessageType string
	Replay      bool
	PointCount  int
	Accepted    int
	Duplicate   int
	OutOfOrder  int
	Quarantined int
	Rejected    int
}

// Gateway is a Registry Gateway Device with the Tenant and Site it belongs to.
type Gateway struct {
	ID       string
	TenantID string
	SiteID   string
}

var (
	ErrGatewayUnknown            = errors.New("Gateway is not in the Registry gateway directory")
	ErrGatewayCredentialInactive = errors.New("Gateway holds no active credential")
)

const (
	QuarantineGatewayUnknown            = "GATEWAY_UNKNOWN"
	QuarantineGatewayCredentialInactive = "GATEWAY_CREDENTIAL_INACTIVE"
	QuarantineMessageInvalid            = "MESSAGE_INVALID"
)

// UplinkQuarantine is evidence of a Gateway message Connectivity did not accept.
// TenantID is empty when the Gateway is unknown.
type UplinkQuarantine struct {
	TenantID   string
	GatewayID  string
	Topic      string
	ReasonCode string
	Detail     string
	Payload    []byte
	ReceivedAt time.Time
}

func (quarantine UplinkQuarantine) PayloadSHA256() string {
	digest := sha256.Sum256(quarantine.Payload)
	return hex.EncodeToString(digest[:])
}

// IdentityResolver resolves what a Gateway's messages name from the Registry read
// port: the Gateway itself (ErrGatewayUnknown, ErrGatewayCredentialInactive), a Device
// by its source key ("" when unregistered) and a Device's Point by Point Code (nil when
// unregistered). It also keeps quarantine evidence.
type IdentityResolver interface {
	ResolveGateway(ctx context.Context, gatewayID string) (Gateway, error)
	ResolveDevice(ctx context.Context, gateway Gateway, sourceKey string) (string, error)
	ResolvePoint(ctx context.Context, gateway Gateway, deviceID, pointCode string) (*ResolvedPoint, error)
	QuarantineUplink(ctx context.Context, quarantine UplinkQuarantine) error
}

type Processor struct {
	identities IdentityResolver
	runtime    RuntimeClient
	now        func() time.Time
}

func NewProcessor(identities IdentityResolver, runtime RuntimeClient) (*Processor, error) {
	if identities == nil || runtime == nil {
		return nil, errors.New("MQTT uplink processor dependencies are invalid")
	}
	return &Processor{identities: identities, runtime: runtime, now: time.Now}, nil
}

// Process handles one Gateway message. A message Connectivity will never accept is
// kept as quarantine evidence and returned as a permanent error, so it is acknowledged;
// any other error is transient and the message is retried.
func (processor *Processor) Process(ctx context.Context, topic string, payload []byte) (ProcessingResult, error) {
	quarantine := UplinkQuarantine{GatewayID: TopicGatewayID(topic), Topic: topic, Payload: payload, ReceivedAt: processor.now().UTC()}
	messageTopic, err := ParseMessageTopic(topic)
	if err != nil {
		return ProcessingResult{}, processor.quarantine(ctx, quarantine, QuarantineMessageInvalid, err)
	}
	gateway, err := processor.identities.ResolveGateway(ctx, messageTopic.GatewayID)
	switch {
	case errors.Is(err, ErrGatewayUnknown):
		return ProcessingResult{}, processor.quarantine(ctx, quarantine, QuarantineGatewayUnknown, err)
	case errors.Is(err, ErrGatewayCredentialInactive):
		quarantine.TenantID = gateway.TenantID
		return ProcessingResult{}, processor.quarantine(ctx, quarantine, QuarantineGatewayCredentialInactive, err)
	case err != nil:
		return ProcessingResult{}, fmt.Errorf("resolve MQTT Gateway: %w", err)
	}
	quarantine.TenantID = gateway.TenantID
	switch messageTopic.MessageType {
	case MessageTypeTelemetry:
		envelope, err := DecodeTelemetryEnvelope(payload, gateway.ID)
		if err != nil {
			return ProcessingResult{}, processor.quarantine(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		return processor.processTelemetry(ctx, gateway, envelope)
	default:
		envelope, err := DecodeEventEnvelope(payload, gateway.ID)
		if err != nil {
			return ProcessingResult{}, processor.quarantine(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		return processor.processEvent(ctx, gateway, envelope)
	}
}

func (processor *Processor) quarantine(ctx context.Context, quarantine UplinkQuarantine, reasonCode string, cause error) error {
	quarantine.ReasonCode = reasonCode
	quarantine.Detail = cause.Error()
	if len(quarantine.Detail) > 1024 {
		quarantine.Detail = quarantine.Detail[:1024]
	}
	if err := processor.identities.QuarantineUplink(ctx, quarantine); err != nil {
		return fmt.Errorf("keep MQTT uplink quarantine evidence: %w", err)
	}
	return permanentMessage(cause)
}

func (processor *Processor) processTelemetry(ctx context.Context, gateway Gateway, envelope TelemetryEnvelope) (ProcessingResult, error) {
	result := ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeTelemetry, Replay: envelope.Replay}
	for _, device := range envelope.Payload.Devices {
		sourceKey := strings.TrimSpace(device.DeviceID)
		var resolved *ResolvedDevice
		deviceID, err := processor.identities.ResolveDevice(ctx, gateway, sourceKey)
		if err != nil {
			return ProcessingResult{}, fmt.Errorf("resolve MQTT Device %s: %w", sourceKey, err)
		}
		if deviceID != "" {
			resolved = &ResolvedDevice{TenantID: gateway.TenantID, SiteID: gateway.SiteID, DeviceID: deviceID}
		}
		for _, point := range device.Points {
			pointCode := strings.TrimSpace(point.Code)
			var resolvedPoint *ResolvedPoint
			if resolved != nil {
				if resolvedPoint, err = processor.identities.ResolvePoint(ctx, gateway, deviceID, pointCode); err != nil {
					return ProcessingResult{}, fmt.Errorf("resolve MQTT point %s/%s: %w", sourceKey, pointCode, err)
				}
			}
			partition := sourcePartition(gateway.ID, sourceKey, pointCode)
			eventID, err := deterministicPointEventID(envelope.MessageID, device.DeviceTimestamp, point, partition)
			if err != nil {
				return ProcessingResult{}, permanentMessage(err)
			}
			valueType, err := wireValueType(point.Value)
			if err != nil {
				return ProcessingResult{}, permanentMessage(err)
			}
			receipt, err := processor.runtime.AcceptObservation(ctx, Observation{
				SourceID: gateway.ID, Device: resolved, Point: resolvedPoint,
				SourcePath: "PUSH", ExternalEntityType: "DEVICE", ExternalID: sourceKey, TelemetryKey: pointCode,
				Value: point.Value, ValueType: valueType, Unit: point.Unit, WireQuality: point.Quality, SampledAt: unixMillisRFC3339(device.DeviceTimestamp),
				SourcePosition: SourcePosition{Partition: partition, Offset: int64(envelope.Sequence), EventID: eventID},
			})
			if err != nil {
				return ProcessingResult{}, fmt.Errorf("accept MQTT point %s/%s: %w", sourceKey, pointCode, err)
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
				return ProcessingResult{}, fmt.Errorf("unexpected Telemetry receipt status %s", receipt.Status)
			}
		}
	}
	return result, nil
}

func (processor *Processor) processEvent(ctx context.Context, gateway Gateway, envelope EventEnvelope) (ProcessingResult, error) {
	event := envelope.Payload
	if err := processor.runtime.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: gateway.TenantID, SiteID: gateway.SiteID, GatewayID: gateway.ID,
		MessageID: envelope.MessageID, Sequence: int64(envelope.Sequence), EventType: strings.TrimSpace(event.EventType),
		SourceType: strings.TrimSpace(event.SourceType), SourceID: strings.TrimSpace(event.SourceID), EventTime: unixMillisRFC3339(event.EventTime),
		Severity: strings.ToUpper(strings.TrimSpace(event.Severity)), Data: append(json.RawMessage(nil), event.Data...),
	}); err != nil {
		return ProcessingResult{}, fmt.Errorf("accept MQTT runtime event: %w", err)
	}
	return ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeEvent}, nil
}
