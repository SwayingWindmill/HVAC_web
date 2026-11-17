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

// DevicePointQuery identifies a Device source key and Point code pair to resolve in batch.
type DevicePointQuery struct {
	SourceKey string
	PointCode string
}

// ResolvedBindings carries Registry Device IDs by source key and Point bindings keyed by
// PointBindingKey(deviceID, pointCode).
type ResolvedBindings struct {
	Devices map[string]string
	Points  map[string]*ResolvedPoint
}

// PointBindingKey encodes a Device ID and Point code lookup key for ResolvedBindings.Points.
func PointBindingKey(deviceID, pointCode string) string {
	return deviceID + "\x00" + pointCode
}

func (bindings ResolvedBindings) Device(sourceKey string) string {
	return bindings.Devices[sourceKey]
}

func (bindings ResolvedBindings) Point(deviceID, pointCode string) *ResolvedPoint {
	return bindings.Points[PointBindingKey(deviceID, pointCode)]
}

// UplinkMessage is one raw MQTT publish waiting to be processed in FIFO order.
type UplinkMessage struct {
	Topic   string
	Payload []byte
}

// ProcessingOutcome is the per-message outcome of ProcessPrefix. When Err is a permanent
// message error, the message was quarantined; otherwise Err is nil and Result is populated.
type ProcessingOutcome struct {
	Result ProcessingResult
	Err    error
}

// IdentityResolver resolves what a Gateway's messages name from the Registry read
// port: the Gateway itself (ErrGatewayUnknown, ErrGatewayCredentialInactive), a Device
// by its source key ("" when unregistered), and batch Device/Point bindings. It also
// keeps quarantine evidence.
type IdentityResolver interface {
	ResolveGateway(ctx context.Context, gatewayID string) (Gateway, error)
	ResolveDevice(ctx context.Context, gateway Gateway, sourceKey string) (string, error)
	ResolveBindings(ctx context.Context, gateway Gateway, queries []DevicePointQuery) (ResolvedBindings, error)
	QuarantineUplink(ctx context.Context, quarantine UplinkQuarantine) error
}

// GatewayChildrenResolver is optionally implemented by IdentityResolver to list
// downstream Devices belonging to a Gateway for observation coverage updates.
type GatewayChildrenResolver interface {
	ListGatewayChildDeviceIDs(ctx context.Context, gatewayID string) ([]string, error)
}

type ProcessorOption func(*Processor)

func WithGatewayConnectHook(hook func(context.Context, string)) ProcessorOption {
	return func(processor *Processor) {
		processor.onGatewayConnected = hook
	}
}

func WithGatewayPresenceHook(hook func(string, bool)) ProcessorOption {
	return func(processor *Processor) {
		processor.onGatewayPresence = hook
	}
}

type Processor struct {
	identities         IdentityResolver
	runtime            RuntimeClient
	now                func() time.Time
	onGatewayConnected func(context.Context, string)
	onGatewayPresence  func(string, bool)
}

func NewProcessor(identities IdentityResolver, runtime RuntimeClient, options ...ProcessorOption) (*Processor, error) {
	if identities == nil || runtime == nil {
		return nil, errors.New("MQTT uplink processor dependencies are invalid")
	}
	processor := &Processor{identities: identities, runtime: runtime, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(processor)
		}
	}
	return processor, nil
}

// Process handles one Gateway message. A message Connectivity will never accept is
// kept as quarantine evidence and returned as a permanent error, so it is acknowledged;
// any other error is transient and the message is retried.
func (processor *Processor) Process(ctx context.Context, topic string, payload []byte) (ProcessingResult, error) {
	outcomes, err := processor.ProcessPrefix(ctx, []UplinkMessage{{Topic: topic, Payload: payload}})
	if err != nil {
		return ProcessingResult{}, err
	}
	if len(outcomes) != 1 {
		return ProcessingResult{}, fmt.Errorf("unexpected uplink batch outcome count %d", len(outcomes))
	}
	return outcomes[0].Result, outcomes[0].Err
}

// ProcessPrefix processes the longest contiguous prefix of messages that can share one
// batch execution, or returns a single outcome when messages[0] must be quarantined or
// handled as a runtime event. A non-nil returned error is transient and no message in
// the slice was committed as acknowledged.
func (processor *Processor) ProcessPrefix(ctx context.Context, messages []UplinkMessage) ([]ProcessingOutcome, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	first := messages[0]
	quarantine := UplinkQuarantine{GatewayID: TopicGatewayID(first.Topic), Topic: first.Topic, Payload: first.Payload, ReceivedAt: processor.now().UTC()}
	messageTopic, err := ParseMessageTopic(first.Topic)
	if err != nil {
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
	}
	gateway, err := processor.identities.ResolveGateway(ctx, messageTopic.GatewayID)
	switch {
	case errors.Is(err, ErrGatewayUnknown):
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineGatewayUnknown, err)
	case errors.Is(err, ErrGatewayCredentialInactive):
		quarantine.TenantID = gateway.TenantID
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineGatewayCredentialInactive, err)
	case err != nil:
		return nil, fmt.Errorf("resolve MQTT Gateway: %w", err)
	}
	quarantine.TenantID = gateway.TenantID
	switch messageTopic.MessageType {
	case MessageTypeTelemetry:
		envelope, err := DecodeTelemetryEnvelope(first.Payload, gateway.ID)
		if err != nil {
			return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		outcomes, err := processor.processTelemetryBatch(ctx, gateway, quarantine, envelope, messages)
		if err != nil {
			return nil, err
		}
		if processor.onGatewayPresence != nil {
			processor.onGatewayPresence(gateway.ID, true)
		}
		return outcomes, nil
	default:
		envelope, err := DecodeEventEnvelope(first.Payload, gateway.ID)
		if err != nil {
			return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		result, err := processor.processEvent(ctx, gateway, envelope)
		if err != nil {
			return nil, err
		}
		return []ProcessingOutcome{{Result: result}}, nil
	}
}

func (processor *Processor) quarantinedOutcome(ctx context.Context, quarantine UplinkQuarantine, reasonCode string, cause error) ([]ProcessingOutcome, error) {
	err := processor.quarantine(ctx, quarantine, reasonCode, cause)
	if !isPermanentMessageError(err) {
		return nil, err
	}
	return []ProcessingOutcome{{Err: err}}, nil
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

type telemetryMessageBounds struct {
	messageID string
	replay    bool
	start     int
	end       int
}

func (processor *Processor) processTelemetryBatch(ctx context.Context, gateway Gateway, firstQuarantine UplinkQuarantine, firstEnvelope TelemetryEnvelope, messages []UplinkMessage) ([]ProcessingOutcome, error) {
	observations, bounds, err := coalesceTelemetryBatch(gateway, firstEnvelope, messages)
	if err != nil {
		return processor.quarantinedOutcome(ctx, firstQuarantine, QuarantineMessageInvalid, err)
	}
	queries := uniqueDevicePointQueries(observations)
	bindings, err := processor.identities.ResolveBindings(ctx, gateway, queries)
	if err != nil {
		return nil, fmt.Errorf("resolve MQTT bindings: %w", err)
	}
	attachResolvedBindings(observations, gateway, bindings)
	receipts, err := processor.sendObservationBatchChunks(ctx, observations)
	if err != nil {
		return nil, err
	}
	outcomes := make([]ProcessingOutcome, len(bounds))
	for index, bound := range bounds {
		result, err := buildProcessingResult(bound.messageID, bound.replay, receipts[bound.start:bound.end])
		if err != nil {
			return nil, err
		}
		outcomes[index] = ProcessingOutcome{Result: result}
	}
	return outcomes, nil
}

const estimatedObservationEnvelopeOverheadBytes = 768

func coalesceTelemetryBatch(gateway Gateway, firstEnvelope TelemetryEnvelope, messages []UplinkMessage) ([]Observation, []telemetryMessageBounds, error) {
	firstObs, err := buildEnvelopeObservations(gateway.ID, firstEnvelope)
	if err != nil {
		return nil, nil, err
	}
	observations := firstObs
	approxBytes := len(messages[0].Payload) + len(firstObs)*estimatedObservationEnvelopeOverheadBytes
	bounds := []telemetryMessageBounds{{
		messageID: firstEnvelope.MessageID,
		replay:    firstEnvelope.Replay,
		start:     0,
		end:       len(observations),
	}}
	for index := 1; index < len(messages); index++ {
		if len(observations) >= MaxObservationBatchSize || approxBytes >= MaxObservationBatchBytes {
			break
		}
		topic, err := ParseMessageTopic(messages[index].Topic)
		if err != nil || topic.MessageType != MessageTypeTelemetry || topic.GatewayID != gateway.ID {
			break
		}
		envelope, err := DecodeTelemetryEnvelope(messages[index].Payload, gateway.ID)
		if err != nil {
			break
		}
		msgCount := envelopePointCount(envelope)
		msgApproxBytes := len(messages[index].Payload) + msgCount*estimatedObservationEnvelopeOverheadBytes
		if len(observations)+msgCount > MaxObservationBatchSize || approxBytes+msgApproxBytes > MaxObservationBatchBytes {
			break
		}
		msgObs, err := buildEnvelopeObservations(gateway.ID, envelope)
		if err != nil {
			break
		}
		start := len(observations)
		observations = append(observations, msgObs...)
		approxBytes += msgApproxBytes
		bounds = append(bounds, telemetryMessageBounds{
			messageID: envelope.MessageID,
			replay:    envelope.Replay,
			start:     start,
			end:       len(observations),
		})
	}
	return observations, bounds, nil
}

func envelopePointCount(envelope TelemetryEnvelope) int {
	total := 0
	for _, device := range envelope.Payload.Devices {
		total += len(device.Points)
	}
	return total
}

func buildEnvelopeObservations(gatewayID string, envelope TelemetryEnvelope) ([]Observation, error) {
	traceID := normalizeTraceID(envelope.TraceID)
	observations := make([]Observation, 0, envelopePointCount(envelope))
	for _, device := range envelope.Payload.Devices {
		sourceKey := strings.TrimSpace(device.DeviceID)
		sampledAt := unixMillisRFC3339(device.DeviceTimestamp)
		for _, point := range device.Points {
			pointCode := strings.TrimSpace(point.Code)
			partition := sourcePartition(gatewayID, sourceKey, pointCode)
			eventID, err := deterministicPointEventID(envelope.MessageID, device.DeviceTimestamp, point, partition)
			if err != nil {
				return nil, err
			}
			valueType, err := wireValueType(point.Value)
			if err != nil {
				return nil, err
			}
			observations = append(observations, Observation{
				SourceID:           gatewayID,
				SourcePath:         "PUSH",
				ExternalEntityType: "DEVICE",
				ExternalID:         sourceKey,
				TelemetryKey:       pointCode,
				Value:              point.Value,
				ValueType:          valueType,
				Unit:               point.Unit,
				WireQuality:        point.Quality,
				SampledAt:          sampledAt,
				SourcePosition:     SourcePosition{Partition: partition, Offset: int64(envelope.Sequence), EventID: eventID},
				TraceID:            traceID,
			})
		}
	}
	return observations, nil
}

func uniqueDevicePointQueries(observations []Observation) []DevicePointQuery {
	seen := make(map[DevicePointQuery]struct{}, len(observations))
	queries := make([]DevicePointQuery, 0, len(observations))
	for _, observation := range observations {
		query := DevicePointQuery{SourceKey: observation.ExternalID, PointCode: observation.TelemetryKey}
		if _, ok := seen[query]; !ok {
			seen[query] = struct{}{}
			queries = append(queries, query)
		}
	}
	return queries
}

func attachResolvedBindings(observations []Observation, gateway Gateway, bindings ResolvedBindings) {
	for index := range observations {
		observation := &observations[index]
		if deviceID := bindings.Device(observation.ExternalID); deviceID != "" {
			observation.Device = &ResolvedDevice{TenantID: gateway.TenantID, SiteID: gateway.SiteID, DeviceID: deviceID}
			observation.Point = bindings.Point(deviceID, observation.TelemetryKey)
		}
	}
}

const batchRequestEnvelopeBaseBytes = len(`{"observations":[]}`)

func splitObservationBatchChunks(observations []Observation, maxCount, maxBytes int) ([][]Observation, error) {
	if len(observations) == 0 {
		return nil, nil
	}
	encodedLengths := make([]int, len(observations))
	for index, observation := range observations {
		encoded, err := json.Marshal(observation)
		if err != nil {
			return nil, fmt.Errorf("encode observation for batch sizing: %w", err)
		}
		encodedLengths[index] = len(encoded)
	}
	var chunks [][]Observation
	start := 0
	for start < len(observations) {
		chunkBytes := batchRequestEnvelopeBaseBytes + encodedLengths[start]
		end := start + 1
		for end < len(observations) && end-start < maxCount {
			nextBytes := chunkBytes + 1 + encodedLengths[end]
			if nextBytes > maxBytes {
				break
			}
			chunkBytes = nextBytes
			end++
		}
		chunks = append(chunks, observations[start:end])
		start = end
	}
	return chunks, nil
}

func (processor *Processor) sendObservationBatchChunks(ctx context.Context, observations []Observation) ([]ObservationReceipt, error) {
	chunks, err := splitObservationBatchChunks(observations, MaxObservationBatchSize, MaxObservationBatchBytes)
	if err != nil {
		return nil, err
	}
	receipts := make([]ObservationReceipt, 0, len(observations))
	for _, chunk := range chunks {
		chunkReceipts, err := processor.runtime.AcceptObservationBatch(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("accept MQTT observation batch: %w", err)
		}
		if len(chunkReceipts) != len(chunk) {
			return nil, fmt.Errorf("unexpected Telemetry receipt count %d for batch size %d", len(chunkReceipts), len(chunk))
		}
		receipts = append(receipts, chunkReceipts...)
	}
	return receipts, nil
}

func buildProcessingResult(messageID string, replay bool, receipts []ObservationReceipt) (ProcessingResult, error) {
	result := ProcessingResult{MessageID: messageID, MessageType: MessageTypeTelemetry, Replay: replay}
	for _, receipt := range receipts {
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
	return result, nil
}

func (processor *Processor) processEvent(ctx context.Context, gateway Gateway, envelope EventEnvelope) (ProcessingResult, error) {
	event := envelope.Payload
	sourceType := strings.TrimSpace(event.SourceType)
	sourceID := strings.TrimSpace(event.SourceID)
	resolvedDevice, coverageDeviceIDs, err := processor.resolveEventScope(ctx, gateway, event)
	if err != nil {
		return ProcessingResult{}, err
	}
	if err := processor.runtime.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: gateway.TenantID, SiteID: gateway.SiteID, GatewayID: gateway.ID,
		MessageID: envelope.MessageID, Sequence: int64(envelope.Sequence), EventType: strings.TrimSpace(event.EventType),
		SourceType: sourceType, SourceID: sourceID, EventTime: unixMillisRFC3339(event.EventTime),
		Severity: strings.ToUpper(strings.TrimSpace(event.Severity)), Data: append(json.RawMessage(nil), event.Data...),
		Device: resolvedDevice, CoverageDeviceIDs: coverageDeviceIDs,
		TraceID: normalizeTraceID(envelope.TraceID),
	}); err != nil {
		return ProcessingResult{}, fmt.Errorf("accept MQTT runtime event: %w", err)
	}
	if processor.onGatewayPresence != nil {
		processor.onGatewayPresence(gateway.ID, !isGatewayDisconnectEvent(event.EventType, event.SourceType))
	}
	if processor.onGatewayConnected != nil && isGatewayConnectEvent(event.EventType, event.SourceType) {
		processor.onGatewayConnected(ctx, gateway.ID)
	}
	return ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeEvent}, nil
}

func isGatewayDisconnectEvent(rawEventType, rawSourceType string) bool {
	eventType := strings.ToUpper(strings.TrimSpace(rawEventType))
	sourceType := strings.ToUpper(strings.TrimSpace(rawSourceType))
	switch eventType {
	case "GATEWAY_DISCONNECTED", "LWT", "LAST_WILL":
		return true
	case "EXPLICIT_DISCONNECT", "DISCONNECTED", "DISCONNECT", "SESSION_DISCONNECTED", "OFFLINE":
		return sourceType == "GATEWAY"
	default:
		return false
	}
}

func isGatewayConnectEvent(rawEventType, rawSourceType string) bool {
	eventType := strings.ToUpper(strings.TrimSpace(rawEventType))
	sourceType := strings.ToUpper(strings.TrimSpace(rawSourceType))
	switch eventType {
	case "GATEWAY_CONNECTED":
		return true
	case "EXPLICIT_CONNECT", "CONNECTED", "CONNECT", "SESSION_CONNECTED", "ONLINE":
		return sourceType == "GATEWAY"
	default:
		return false
	}
}

func (processor *Processor) resolveEventScope(ctx context.Context, gateway Gateway, event EventPayload) (*ResolvedDevice, []string, error) {
	eventType := strings.ToUpper(strings.TrimSpace(event.EventType))
	sourceType := strings.ToUpper(strings.TrimSpace(event.SourceType))
	sourceID := strings.TrimSpace(event.SourceID)

	var resolvedDevice *ResolvedDevice
	if sourceType == "DEVICE" && sourceID != "" {
		deviceID, err := processor.identities.ResolveDevice(ctx, gateway, sourceID)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve MQTT event Device %s: %w", sourceID, err)
		}
		if deviceID != "" {
			resolvedDevice = &ResolvedDevice{TenantID: gateway.TenantID, SiteID: gateway.SiteID, DeviceID: deviceID}
		}
	} else if sourceType == "GATEWAY" {
		lookupKey := sourceID
		if lookupKey == "" {
			lookupKey = gateway.ID
		}
		deviceID, err := processor.identities.ResolveDevice(ctx, gateway, lookupKey)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve MQTT event Gateway %s: %w", lookupKey, err)
		}
		if deviceID == "" && isUUIDLike(gateway.ID) && (sourceID == "" || strings.EqualFold(sourceID, gateway.ID)) {
			deviceID = strings.ToLower(strings.TrimSpace(gateway.ID))
		}
		if deviceID != "" {
			resolvedDevice = &ResolvedDevice{TenantID: gateway.TenantID, SiteID: gateway.SiteID, DeviceID: deviceID}
		}
	}

	var coverageDeviceIDs []string
	if isGatewayCoverageEvent(eventType, sourceType) {
		if childrenResolver, ok := processor.identities.(GatewayChildrenResolver); ok {
			childIDs, err := childrenResolver.ListGatewayChildDeviceIDs(ctx, gateway.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("list gateway child devices: %w", err)
			}
			for _, childID := range childIDs {
				id := strings.ToLower(strings.TrimSpace(childID))
				if id == "" || (resolvedDevice != nil && strings.EqualFold(id, resolvedDevice.DeviceID)) {
					continue
				}
				coverageDeviceIDs = append(coverageDeviceIDs, id)
			}
		}
	}
	return resolvedDevice, coverageDeviceIDs, nil
}

func isGatewayCoverageEvent(eventType, sourceType string) bool {
	switch eventType {
	case "GATEWAY_CONNECTED", "GATEWAY_DISCONNECTED", "LWT", "LAST_WILL":
		return true
	case "EXPLICIT_CONNECT", "CONNECTED", "CONNECT", "SESSION_CONNECTED", "ONLINE",
		"EXPLICIT_DISCONNECT", "DISCONNECTED", "DISCONNECT", "SESSION_DISCONNECTED", "OFFLINE":
		return sourceType == "GATEWAY"
	default:
		return false
	}
}

func isUUIDLike(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return len(trimmed) == 36 && trimmed[8] == '-' && trimmed[13] == '-' && trimmed[18] == '-' && trimmed[23] == '-'
}
