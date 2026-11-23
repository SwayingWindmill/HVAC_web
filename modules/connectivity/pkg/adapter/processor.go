package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type ProcessingResult struct {
	MessageID   string
	MessageType string
	TraceID     string
	Replay      bool
	PointCount  int
	Accepted    int
	Duplicate   int
	OutOfOrder  int
	Quarantined int
	Rejected    int
}

// Gateway is a Registry Gateway Device (or standalone DIRECT Device) with its Tenant and Site.
type Gateway struct {
	ID           string
	TenantID     string
	SiteID       string
	DirectDevice bool
	DeviceCode   string
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
	Topic      string
	Payload    []byte
	ReceivedAt time.Time
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

func WithShadowAckHook(hook func(context.Context, Gateway, ShadowChangeAckPayload)) ProcessorOption {
	return func(processor *Processor) {
		processor.onShadowAck = hook
	}
}

func WithOTAResultHook(hook func(context.Context, Gateway, edgecontrol.OTAResultPayload) error) ProcessorOption {
	return func(processor *Processor) {
		processor.onOTAResult = hook
	}
}

// WithFactoryResetAckHook registers a handler for edge FACTORY_RESET_ACK lifecycle uplinks.
func WithFactoryResetAckHook(hook func(context.Context, string, edgecontrol.FactoryResetAckPayload) error) ProcessorOption {
	return func(processor *Processor) {
		processor.onFactoryResetAck = hook
	}
}

func WithProcessorTracer(tracer *observability.Tracer) ProcessorOption {
	return func(processor *Processor) {
		processor.tracer = tracer
	}
}

func WithProcessorPacketHook(hook func(direction, channel, gatewayID, topic, traceID string, payload []byte)) ProcessorOption {
	return func(processor *Processor) {
		processor.packetHook = hook
	}
}

// WithGatewayRateLimit configures a per-gateway token-bucket rate limiter enforced
// before full payload decoding.
func WithGatewayRateLimit(messagesPerSec float64, burst int) ProcessorOption {
	return func(processor *Processor) {
		processor.rateLimiter = NewGatewayRateLimiter(messagesPerSec, burst)
	}
}

// WithProcessorMetrics attaches an observability.Registry used for rate-limit metrics.
func WithProcessorMetrics(metrics *observability.Registry) ProcessorOption {
	return func(processor *Processor) {
		processor.metrics = metrics
	}
}

// WithAutoBackfillLagThreshold promotes non-replay readings older than threshold to replay.
func WithAutoBackfillLagThreshold(threshold time.Duration) ProcessorOption {
	return func(processor *Processor) {
		if threshold < 0 {
			threshold = 0
		}
		processor.autoBackfillLag = threshold
		processor.autoBackfillLagSet = true
	}
}

type Processor struct {
	identities         IdentityResolver
	runtime            RuntimeClient
	tracer             *observability.Tracer
	metrics            *observability.Registry
	codecs             *CodecRegistry
	sessionTracker     *GatewaySessionTracker
	rateLimiter        *GatewayRateLimiter
	autoBackfillLag    time.Duration
	autoBackfillLagSet bool
	now                func() time.Time
	onGatewayConnected func(context.Context, string)
	onGatewayPresence  func(string, bool)
	onShadowAck        func(context.Context, Gateway, ShadowChangeAckPayload)
	onOTAResult        func(context.Context, Gateway, edgecontrol.OTAResultPayload) error
	onFactoryResetAck  func(context.Context, string, edgecontrol.FactoryResetAckPayload) error
	packetHook         func(direction, channel, gatewayID, topic, traceID string, payload []byte)
	coverageRev        atomic.Int64
}

func (processor *Processor) recordPacket(direction, channel, gatewayID, topic, traceID string, payload []byte) {
	if processor == nil || processor.packetHook == nil {
		return
	}
	processor.packetHook(direction, channel, gatewayID, topic, traceID, payload)
}

func (processor *Processor) nowUTC() time.Time {
	if processor == nil || processor.now == nil {
		return time.Now().UTC()
	}
	return processor.now().UTC()
}

func (processor *Processor) recordShadowMetric(outcome string) {
	if processor == nil || processor.metrics == nil {
		return
	}
	_ = processor.metrics.AddCounter(ShadowReplicationMetricName, ShadowReplicationMetricHelp, map[string]string{"outcome": outcome}, 1)
}

func (processor *Processor) recordOTAMetric(outcome string) {
	if processor == nil || processor.metrics == nil {
		return
	}
	_ = processor.metrics.AddCounter(OTAReplicationMetricName, OTAReplicationMetricHelp, map[string]string{"outcome": outcome}, 1)
}

func (processor *Processor) recordLifecycleMetric(outcome string) {
	if processor == nil || processor.metrics == nil {
		return
	}
	_ = processor.metrics.AddCounter(LifecycleReplicationMetricName, LifecycleReplicationMetricHelp, map[string]string{"outcome": outcome}, 1)
}

func (processor *Processor) processFactoryResetAck(ctx context.Context, gatewayID string, quarantine UplinkQuarantine, msg UplinkMessage) ([]ProcessingOutcome, error) {
	env, ack, err := DecodeFactoryResetAck(msg.Payload, gatewayID)
	if err != nil {
		processor.recordLifecycleMetric("rejected")
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
	}
	processor.recordPacket("UPLINK", "EVENT", gatewayID, msg.Topic, env.MessageID, msg.Payload)
	if processor.onFactoryResetAck != nil {
		if err := processor.onFactoryResetAck(ctx, gatewayID, ack); err != nil {
			if errors.Is(err, ErrFactoryResetAckRejected) {
				processor.recordLifecycleMetric("quarantined")
				return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
			}
			processor.recordLifecycleMetric("failed")
			return nil, err
		}
	}
	processor.recordLifecycleMetric("accepted")
	return []ProcessingOutcome{{
		Result: ProcessingResult{
			MessageID:   env.MessageID,
			MessageType: MessageTypeLifecycle,
			TraceID:     env.MessageID,
			PointCount:  1,
			Accepted:    1,
		},
	}}, nil
}

func (processor *Processor) processOTAResult(ctx context.Context, gateway Gateway, quarantine UplinkQuarantine, msg UplinkMessage) ([]ProcessingOutcome, error) {
	env, res, err := DecodeOTAResult(msg.Payload, gateway.ID)
	if err != nil {
		processor.recordOTAMetric("rejected")
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
	}
	processor.recordPacket("UPLINK", "EVENT", gateway.ID, msg.Topic, env.MessageID, msg.Payload)
	if processor.onGatewayPresence != nil {
		processor.onGatewayPresence(gateway.ID, true)
	}
	if processor.onOTAResult != nil {
		if err := processor.onOTAResult(ctx, gateway, res); err != nil {
			if errors.Is(err, ErrOTAResultRejected) {
				processor.recordOTAMetric("quarantined")
				return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
			}
			processor.recordOTAMetric("failed")
			return nil, err
		}
	}
	processor.recordOTAMetric("accepted")
	return []ProcessingOutcome{{
		Result: ProcessingResult{
			MessageID:   env.MessageID,
			MessageType: MessageTypeOTA,
			TraceID:     env.MessageID,
			PointCount:  1,
			Accepted:    1,
		},
	}}, nil
}

func (processor *Processor) processShadowAck(ctx context.Context, gateway Gateway, quarantine UplinkQuarantine, msg UplinkMessage) ([]ProcessingOutcome, error) {
	env, ack, err := DecodeShadowChangeAck(msg.Payload, gateway.ID)
	if err != nil {
		processor.recordShadowMetric("rejected")
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
	}
	processor.recordPacket("UPLINK", "SHADOW", gateway.ID, msg.Topic, env.MessageID, msg.Payload)
	if processor.onGatewayPresence != nil {
		processor.onGatewayPresence(gateway.ID, true)
	}
	result := ProcessingResult{
		MessageID:   env.MessageID,
		MessageType: MessageTypeShadow,
		TraceID:     env.MessageID,
		PointCount:  1,
	}
	if ack.Status == ShadowAckStatusAcked {
		processor.recordShadowMetric("accepted")
		result.Accepted = 1
	} else {
		processor.recordShadowMetric("quarantined")
		result.Quarantined = 1
	}
	if processor.onShadowAck != nil {
		processor.onShadowAck(ctx, gateway, ack)
	}
	return []ProcessingOutcome{{Result: result}}, nil
}

func (processor *Processor) ensureUplinkTrace(ctx context.Context, operation, gatewayID, messageID, rawTraceID string) (context.Context, string, *observability.Span) {
	traceID := normalizeTraceID(rawTraceID)
	if traceID != "" {
		return ctx, traceID, nil
	}
	attrs := map[string]any{
		"mqtt.gateway_id": gatewayID,
		"mqtt.message_id": messageID,
	}
	var span *observability.Span
	if processor.tracer != nil {
		ctx, span = processor.tracer.Start(ctx, operation, observability.SpanKindConsumer, attrs)
	} else {
		ctx, span = observability.Start(ctx, operation, observability.SpanKindConsumer, attrs)
	}
	return ctx, normalizeTraceID(observability.TraceID(ctx)), span
}

func NewProcessor(identities IdentityResolver, runtime RuntimeClient, options ...ProcessorOption) (*Processor, error) {
	if identities == nil || runtime == nil {
		return nil, errors.New("MQTT uplink processor dependencies are invalid")
	}
	processor := &Processor{
		identities: identities,
		runtime:    runtime,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(processor)
		}
	}
	if processor.codecs == nil {
		processor.codecs = DefaultCodecRegistry()
	}
	if processor.sessionTracker == nil {
		processor.sessionTracker = NewGatewaySessionTracker()
	}
	return processor, nil
}

func (processor *Processor) codecRegistry() *CodecRegistry {
	if processor != nil && processor.codecs != nil {
		return processor.codecs
	}
	return DefaultCodecRegistry()
}

func (processor *Processor) sessions() *GatewaySessionTracker {
	if processor != nil {
		return processor.sessionTracker
	}
	return nil
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

type ingressRateCheckedKey struct{}

func withIngressRateChecked(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ingressRateCheckedKey{}, true)
}

func isIngressRateChecked(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	checked, _ := ctx.Value(ingressRateCheckedKey{}).(bool)
	return checked
}

// CheckIngressRateLimit enforces the per-gateway ingress token bucket before WAL custody.
// When rejected, it records the rate-limit metric and returns a non-nil UplinkQuarantine
// if the 5-second per-(gateway, messageType) quarantine cooldown allows persistence.
func (processor *Processor) CheckIngressRateLimit(topic string, payload []byte) (allowed bool, messageType string, quarantine *UplinkQuarantine) {
	if processor == nil || processor.rateLimiter == nil || !processor.rateLimiter.Enabled() {
		return true, "", nil
	}
	gatewayID := TopicGatewayID(topic)
	if gatewayID == "invalid" {
		return true, "", nil
	}
	rawType := "unknown"
	if parsedTopic, err := ParseMessageTopic(topic); err == nil {
		rawType = parsedTopic.MessageType
	}
	msgTypeLabel := normalizeRateLimitMessageType(rawType)
	now := processor.now().UTC()
	if processor.rateLimiter.Allow(gatewayID, now) {
		return true, msgTypeLabel, nil
	}
	if processor.metrics != nil {
		_ = processor.metrics.AddCounter(
			"hvac_mqtt_gateway_rate_limited_total",
			"Rate-limited MQTT uplink messages dropped per gateway.",
			map[string]string{"message_type": msgTypeLabel},
			1,
		)
	}
	if !processor.rateLimiter.ShouldQuarantine(gatewayID, msgTypeLabel, now) {
		return false, msgTypeLabel, nil
	}
	return false, msgTypeLabel, &UplinkQuarantine{
		GatewayID:  gatewayID,
		Topic:      topic,
		ReasonCode: QuarantineReasonRateLimited,
		Detail:     fmt.Sprintf("MQTT gateway %s rate limit exceeded", gatewayID),
		Payload:    slices.Clone(payload),
		ReceivedAt: now,
	}
}

// WriteRateLimitQuarantine resolves gateway tenant ownership when available and persists
// a RATE_LIMITED quarantine record.
func (processor *Processor) WriteRateLimitQuarantine(ctx context.Context, quarantine UplinkQuarantine) error {
	if processor == nil || processor.identities == nil {
		return nil
	}
	if quarantine.GatewayID != "" && quarantine.GatewayID != "invalid" && quarantine.TenantID == "" {
		if gw, gwErr := processor.identities.ResolveGateway(ctx, quarantine.GatewayID); gwErr == nil || gw.TenantID != "" {
			quarantine.TenantID = gw.TenantID
		}
	}
	detail := quarantine.Detail
	if detail == "" {
		detail = fmt.Sprintf("MQTT gateway %s rate limit exceeded", quarantine.GatewayID)
	}
	return processor.quarantine(ctx, quarantine, QuarantineReasonRateLimited, errors.New(detail))
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
	now := processor.now().UTC()
	quarantine := UplinkQuarantine{GatewayID: TopicGatewayID(first.Topic), Topic: first.Topic, Payload: first.Payload, ReceivedAt: now}
	messageTopic, topicErr := ParseMessageTopic(first.Topic)

	if !isIngressRateChecked(ctx) {
		if allowed, _, rateLimitedQ := processor.CheckIngressRateLimit(first.Topic, first.Payload); !allowed {
			if rateLimitedQ == nil {
				return []ProcessingOutcome{{Err: permanentMessage(fmt.Errorf("MQTT gateway %s rate limit exceeded", quarantine.GatewayID))}}, nil
			}
			err := processor.WriteRateLimitQuarantine(ctx, *rateLimitedQ)
			if !isPermanentMessageError(err) {
				return nil, err
			}
			return []ProcessingOutcome{{Err: err}}, nil
		}
	}

	if topicErr != nil {
		if quarantine.GatewayID != "invalid" {
			if gw, gwErr := processor.identities.ResolveGateway(ctx, quarantine.GatewayID); gwErr == nil || gw.TenantID != "" {
				quarantine.TenantID = gw.TenantID
			}
		}
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, topicErr)
	}
	gateway, err := processor.identities.ResolveGateway(ctx, messageTopic.GatewayID)
	switch {
	case errors.Is(err, ErrGatewayUnknown):
		if messageTopic.MessageType == MessageTypeLifecycle && processor.onFactoryResetAck != nil {
			return processor.processFactoryResetAck(ctx, messageTopic.GatewayID, quarantine, first)
		}
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineGatewayUnknown, err)
	case errors.Is(err, ErrGatewayCredentialInactive):
		quarantine.TenantID = gateway.TenantID
		if messageTopic.MessageType == MessageTypeLifecycle && processor.onFactoryResetAck != nil && strings.TrimSpace(gateway.TenantID) != "" {
			return processor.processFactoryResetAck(ctx, messageTopic.GatewayID, quarantine, first)
		}
		return processor.quarantinedOutcome(ctx, quarantine, QuarantineGatewayCredentialInactive, err)
	case err != nil:
		return nil, fmt.Errorf("resolve MQTT Gateway: %w", err)
	}
	quarantine.TenantID = gateway.TenantID
	switch messageTopic.MessageType {
	case MessageTypeTelemetry:
		decoded, err := processor.codecRegistry().DecodeTelemetry(first.Payload, gateway.ID)
		if err != nil {
			return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		outcomes, err := processor.processTelemetryBatch(ctx, gateway, quarantine, decoded, messages)
		if err != nil {
			return nil, err
		}
		if processor.onGatewayPresence != nil {
			for _, outcome := range outcomes {
				if outcome.Err == nil {
					processor.onGatewayPresence(gateway.ID, true)
					break
				}
			}
		}
		return outcomes, nil
	case MessageTypeStatus:
		status, err := processor.codecRegistry().DecodeStatus(first.Payload, gateway.ID)
		if err != nil {
			return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		result, err := processor.processStatus(ctx, gateway, first.Topic, first.Payload, status)
		if err != nil {
			return nil, err
		}
		return []ProcessingOutcome{{Result: result}}, nil
	case MessageTypeShadow:
		return processor.processShadowAck(ctx, gateway, quarantine, first)
	case MessageTypeOTA:
		return processor.processOTAResult(ctx, gateway, quarantine, first)
	case MessageTypeLifecycle:
		return processor.processFactoryResetAck(ctx, gateway.ID, quarantine, first)
	default:
		envelope, err := processor.codecRegistry().DecodeEvent(first.Payload, gateway.ID)
		if err != nil {
			return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
		}
		result, err := processor.processEvent(ctx, gateway, first.Topic, first.Payload, envelope)
		if err != nil {
			if errors.Is(err, ErrTelemetryRequestInvalid) {
				return processor.quarantinedOutcome(ctx, quarantine, QuarantineMessageInvalid, err)
			}
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
	quarantine.Detail = truncateUTF8(cause.Error(), 1024)
	if err := processor.identities.QuarantineUplink(ctx, quarantine); err != nil {
		return fmt.Errorf("keep MQTT uplink quarantine evidence: %w", err)
	}
	processor.recordPacket("UPLINK", "QUARANTINE", quarantine.GatewayID, quarantine.Topic, "", quarantine.Payload)
	return permanentMessage(cause)
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\ufffd")
	}
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

type telemetryMessageBounds struct {
	messageID string
	traceID   string
	replay    bool
	start     int
	end       int
}

func (processor *Processor) processTelemetryBatch(ctx context.Context, gateway Gateway, firstQuarantine UplinkQuarantine, firstDecoded DecodedTelemetry, messages []UplinkMessage) ([]ProcessingOutcome, error) {
	firstEnvelope := firstDecoded.Envelope
	ctx, rootTraceID, span := processor.ensureUplinkTrace(ctx, "mqtt.uplink.telemetry", gateway.ID, firstEnvelope.MessageID, firstEnvelope.TraceID)
	if span != nil {
		defer span.End()
	}
	firstEnvelope.TraceID = rootTraceID
	firstDecoded.Envelope = firstEnvelope
	observations, bounds, decodedFrames, err := processor.coalesceTelemetryBatch(ctx, gateway, firstDecoded, messages)
	if err != nil {
		if span != nil {
			span.SetStatus("error", "telemetry envelope invalid")
		}
		return processor.quarantinedOutcome(ctx, firstQuarantine, QuarantineMessageInvalid, err)
	}
	queries := uniqueDevicePointQueries(observations)
	bindings, err := processor.identities.ResolveBindings(ctx, gateway, queries)
	if err != nil {
		if span != nil {
			span.SetStatus("error", "resolve bindings failed")
		}
		return nil, fmt.Errorf("resolve MQTT bindings: %w", err)
	}
	attachResolvedBindings(observations, gateway, bindings)
	receipts, committedObs, err := processor.sendObservationBatchChunks(ctx, observations)
	if err != nil {
		if errors.Is(err, ErrTelemetryRequestInvalid) {
			return processor.isolateInvalidTelemetryBatch(ctx, gateway, firstQuarantine, observations, bounds, decodedFrames, messages, receipts, committedObs, span, err)
		}
		if span != nil {
			span.SetStatus("error", "accept observation batch failed")
		}
		return nil, err
	}
	outcomes := make([]ProcessingOutcome, len(bounds))
	for index, bound := range bounds {
		result, err := buildProcessingResult(bound.messageID, bound.traceID, bound.replay, receipts[bound.start:bound.end])
		if err != nil {
			if span != nil {
				span.SetStatus("error", "build processing result failed")
			}
			return nil, err
		}
		outcomes[index] = ProcessingOutcome{Result: result}
	}
	if span != nil {
		span.SetStatus("ok", "")
	}
	if sessions := processor.sessions(); sessions != nil {
		nowMS := processor.nowUTC().UnixMilli()
		for _, frame := range decodedFrames {
			sessions.ObserveTelemetryAt(gateway.ID, frame.SessionID, frame.Envelope.Timestamp, nowMS, true)
		}
	}
	for index, bound := range bounds {
		processor.recordPacket("UPLINK", "TELEMETRY", gateway.ID, messages[index].Topic, bound.traceID, messages[index].Payload)
	}
	return outcomes, nil
}

func abortInvalidBatchIsolation(outcomes []ProcessingOutcome, span *observability.Span, reason string, err error) ([]ProcessingOutcome, error) {
	if len(outcomes) > 0 {
		return outcomes, nil
	}
	if span != nil {
		span.SetStatus("error", reason)
	}
	return nil, err
}

func (processor *Processor) isolateInvalidTelemetryBatch(
	ctx context.Context,
	gateway Gateway,
	firstQuarantine UplinkQuarantine,
	observations []Observation,
	bounds []telemetryMessageBounds,
	decodedFrames []DecodedTelemetry,
	messages []UplinkMessage,
	committedReceipts []ObservationReceipt,
	committedObs int,
	span *observability.Span,
	batchErr error,
) ([]ProcessingOutcome, error) {
	if len(bounds) == 1 && committedObs == 0 {
		if span != nil {
			span.SetStatus("error", "telemetry observation batch rejected")
		}
		return processor.quarantinedOutcome(ctx, firstQuarantine, QuarantineMessageInvalid, batchErr)
	}

	outcomes := make([]ProcessingOutcome, 0, len(bounds))
	sessions := processor.sessions()
	anySucceeded := committedObs > 0
	for index, bound := range bounds {
		var msgReceipts []ObservationReceipt
		var msgErr error
		switch {
		case bound.end <= committedObs:
			msgReceipts = committedReceipts[bound.start:bound.end]
		case bound.start < committedObs:
			tailReceipts, _, tailErr := processor.sendObservationBatchChunks(ctx, observations[committedObs:bound.end])
			if tailErr == nil {
				msgReceipts = make([]ObservationReceipt, 0, bound.end-bound.start)
				msgReceipts = append(msgReceipts, committedReceipts[bound.start:committedObs]...)
				msgReceipts = append(msgReceipts, tailReceipts...)
			}
			msgErr = tailErr
		default:
			msgReceipts, _, msgErr = processor.sendObservationBatchChunks(ctx, observations[bound.start:bound.end])
		}
		if msgErr == nil {
			result, resErr := buildProcessingResult(bound.messageID, bound.traceID, bound.replay, msgReceipts)
			if resErr != nil {
				return abortInvalidBatchIsolation(outcomes, span, "build processing result failed", resErr)
			}
			outcomes = append(outcomes, ProcessingOutcome{Result: result})
			anySucceeded = true
			if sessions != nil {
				sessions.ObserveTelemetryAt(gateway.ID, decodedFrames[index].SessionID, decodedFrames[index].Envelope.Timestamp, processor.nowUTC().UnixMilli(), true)
			}
			processor.recordPacket("UPLINK", "TELEMETRY", gateway.ID, messages[index].Topic, bound.traceID, messages[index].Payload)
			continue
		}
		if errors.Is(msgErr, ErrTelemetryRequestInvalid) {
			msgQuarantine := UplinkQuarantine{
				TenantID:   gateway.TenantID,
				GatewayID:  gateway.ID,
				Topic:      messages[index].Topic,
				Payload:    messages[index].Payload,
				ReceivedAt: firstQuarantine.ReceivedAt,
			}
			qErr := processor.quarantine(ctx, msgQuarantine, QuarantineMessageInvalid, msgErr)
			if !isPermanentMessageError(qErr) {
				return abortInvalidBatchIsolation(outcomes, span, "quarantine invalid message failed", qErr)
			}
			outcomes = append(outcomes, ProcessingOutcome{Err: qErr})
			continue
		}
		return abortInvalidBatchIsolation(outcomes, span, "accept observation batch failed", msgErr)
	}
	if span != nil {
		if anySucceeded {
			span.SetStatus("ok", "")
		} else {
			span.SetStatus("error", "telemetry observation batch rejected")
		}
	}
	return outcomes, nil
}

const estimatedObservationEnvelopeOverheadBytes = 768

func (processor *Processor) coalesceTelemetryBatch(ctx context.Context, gateway Gateway, firstDecoded DecodedTelemetry, messages []UplinkMessage) ([]Observation, []telemetryMessageBounds, []DecodedTelemetry, error) {
	firstEnvelope := firstDecoded.Envelope
	rootTraceID := normalizeTraceID(firstEnvelope.TraceID)
	fallbackReceivedAt := processor.now().UTC()
	firstReceivedAt := messages[0].ReceivedAt.UTC()
	if messages[0].ReceivedAt.IsZero() {
		firstReceivedAt = fallbackReceivedAt
	}
	firstObs, err := buildEnvelopeObservations(gateway.ID, firstEnvelope, rootTraceID, processor.autoBackfillLag, firstReceivedAt)
	if err != nil {
		return nil, nil, nil, err
	}
	observations := firstObs
	approxBytes := len(messages[0].Payload) + len(firstObs)*estimatedObservationEnvelopeOverheadBytes
	bounds := []telemetryMessageBounds{{
		messageID: firstEnvelope.MessageID,
		traceID:   rootTraceID,
		replay:    firstEnvelope.Replay,
		start:     0,
		end:       len(observations),
	}}
	decodedFrames := []DecodedTelemetry{firstDecoded}
	codecs := processor.codecRegistry()
	enforceRateLimit := !isIngressRateChecked(ctx) && processor.rateLimiter != nil && processor.rateLimiter.Enabled()
	for index := 1; index < len(messages); index++ {
		if len(observations) >= MaxObservationBatchSize || approxBytes >= MaxObservationBatchBytes {
			break
		}
		topic, err := ParseMessageTopic(messages[index].Topic)
		if err != nil || topic.MessageType != MessageTypeTelemetry || topic.GatewayID != gateway.ID {
			break
		}
		if enforceRateLimit && !processor.rateLimiter.Allow(gateway.ID, processor.now().UTC()) {
			break
		}
		decoded, err := codecs.DecodeTelemetry(messages[index].Payload, gateway.ID)
		if err != nil {
			break
		}
		envelope := decoded.Envelope
		msgCount := envelopePointCount(envelope)
		msgApproxBytes := len(messages[index].Payload) + msgCount*estimatedObservationEnvelopeOverheadBytes
		if len(observations)+msgCount > MaxObservationBatchSize || approxBytes+msgApproxBytes > MaxObservationBatchBytes {
			break
		}
		msgTraceID := normalizeTraceID(envelope.TraceID)
		if msgTraceID == "" {
			msgTraceID = rootTraceID
		}
		msgReceivedAt := messages[index].ReceivedAt.UTC()
		if messages[index].ReceivedAt.IsZero() {
			msgReceivedAt = fallbackReceivedAt
		}
		msgObs, err := buildEnvelopeObservations(gateway.ID, envelope, msgTraceID, processor.autoBackfillLag, msgReceivedAt)
		if err != nil {
			break
		}
		start := len(observations)
		observations = append(observations, msgObs...)
		approxBytes += msgApproxBytes
		bounds = append(bounds, telemetryMessageBounds{
			messageID: envelope.MessageID,
			traceID:   msgTraceID,
			replay:    envelope.Replay,
			start:     start,
			end:       len(observations),
		})
		decodedFrames = append(decodedFrames, decoded)
	}
	return observations, bounds, decodedFrames, nil
}

func envelopePointCount(envelope TelemetryEnvelope) int {
	total := 0
	for _, device := range envelope.Payload.Devices {
		total += len(device.Points)
	}
	return total
}

func buildEnvelopeObservations(
	gatewayID string,
	envelope TelemetryEnvelope,
	traceID string,
	autoBackfillLag time.Duration,
	receivedAt time.Time,
) ([]Observation, error) {
	traceID = normalizeTraceID(traceID)
	observations := make([]Observation, 0, envelopePointCount(envelope))
	for _, device := range envelope.Payload.Devices {
		sourceKey := strings.TrimSpace(device.DeviceID)
		sampledAtTime := time.UnixMilli(device.DeviceTimestamp).UTC()
		sampledAt := sampledAtTime.Format(time.RFC3339Nano)
		edgeReplay := envelope.Replay || (autoBackfillLag > 0 && receivedAt.Sub(sampledAtTime) > autoBackfillLag)

		for _, point := range device.Points {
			pointCode := strings.TrimSpace(point.Code)
			basePartition := sourcePartition(gatewayID, sourceKey, pointCode)
			partition := basePartition
			offset := int64(envelope.Sequence)
			if edgeReplay {
				partition = replaySourcePartition(gatewayID, envelope.MessageID, sourceKey, pointCode)
				offset = max(sampledAtTime.UnixMicro(), 0)
			}
			eventID, err := deterministicPointEventID(envelope.MessageID, device.DeviceTimestamp, point, basePartition)
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
				EdgeReplay:         edgeReplay,
				SourcePosition:     SourcePosition{Partition: partition, Offset: offset, EventID: eventID},
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

func (processor *Processor) sendObservationBatchChunks(ctx context.Context, observations []Observation) ([]ObservationReceipt, int, error) {
	chunks, err := splitObservationBatchChunks(observations, MaxObservationBatchSize, MaxObservationBatchBytes)
	if err != nil {
		return nil, 0, err
	}
	receipts := make([]ObservationReceipt, 0, len(observations))
	committed := 0
	for _, chunk := range chunks {
		chunkReceipts, err := processor.runtime.AcceptObservationBatch(ctx, chunk)
		if err != nil {
			return receipts, committed, fmt.Errorf("accept MQTT observation batch: %w", err)
		}
		if len(chunkReceipts) != len(chunk) {
			return receipts, committed, fmt.Errorf("unexpected Telemetry receipt count %d for batch size %d", len(chunkReceipts), len(chunk))
		}
		receipts = append(receipts, chunkReceipts...)
		committed += len(chunk)
	}
	return receipts, committed, nil
}

func buildProcessingResult(messageID, traceID string, replay bool, receipts []ObservationReceipt) (ProcessingResult, error) {
	result := ProcessingResult{MessageID: messageID, MessageType: MessageTypeTelemetry, TraceID: traceID, Replay: replay}
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

func (processor *Processor) processEvent(ctx context.Context, gateway Gateway, topic string, rawPayload []byte, envelope EventEnvelope) (ProcessingResult, error) {
	ctx, eventTraceID, span := processor.ensureUplinkTrace(ctx, "mqtt.uplink.event", gateway.ID, envelope.MessageID, envelope.TraceID)
	if span != nil {
		defer span.End()
	}
	envelope.TraceID = eventTraceID
	event := envelope.Payload
	sourceType := strings.TrimSpace(event.SourceType)
	sourceID := strings.TrimSpace(event.SourceID)
	resolvedDevice, coverageDeviceIDs, err := processor.resolveEventScope(ctx, gateway, event)
	if err != nil {
		if span != nil {
			span.SetStatus("error", "resolve event scope failed")
		}
		return ProcessingResult{}, err
	}
	if err := processor.runtime.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: gateway.TenantID, SiteID: gateway.SiteID, GatewayID: gateway.ID,
		MessageID: envelope.MessageID, Sequence: int64(envelope.Sequence), EventType: strings.TrimSpace(event.EventType),
		SourceType: sourceType, SourceID: sourceID, EventTime: unixMillisRFC3339(event.EventTime),
		Severity: strings.ToUpper(strings.TrimSpace(event.Severity)), Data: append(json.RawMessage(nil), event.Data...),
		Device: resolvedDevice, CoverageDeviceIDs: coverageDeviceIDs,
		TraceID: eventTraceID,
	}); err != nil {
		if span != nil {
			span.SetStatus("error", "accept runtime event failed")
		}
		return ProcessingResult{}, fmt.Errorf("accept MQTT runtime event: %w", err)
	}
	if span != nil {
		span.SetStatus("ok", "")
	}
	processor.recordPacket("UPLINK", "EVENT", gateway.ID, topic, eventTraceID, rawPayload)
	directSelf := gateway.DirectDevice && resolvedDevice != nil && strings.EqualFold(resolvedDevice.DeviceID, gateway.ID)
	isDisconnect := isGatewayDisconnectEvent(event.EventType, event.SourceType) || (directSelf && isDeviceDisconnectEvent(event.EventType))
	isConnect := isGatewayConnectEvent(event.EventType, event.SourceType) || (directSelf && isDeviceConnectEvent(event.EventType))
	if isDisconnect || isConnect {
		accepted := true
		if sessions := processor.sessions(); sessions != nil {
			accepted = sessions.ApplyTransition(SessionTransition{
				GatewayID:     gateway.ID,
				SessionID:     extractEventSessionID(event.Data),
				Sequence:      int64(envelope.Sequence),
				HasSequence:   true,
				EventTime:     event.EventTime,
				HasEventTime:  true,
				ServerTime:    processor.nowUTC().UnixMilli(),
				HasServerTime: true,
				Online:        isConnect,
			})
		}
		if accepted {
			if processor.onGatewayPresence != nil {
				processor.onGatewayPresence(gateway.ID, isConnect)
			}
			if isConnect && processor.onGatewayConnected != nil {
				processor.onGatewayConnected(ctx, gateway.ID)
			}
		}
	} else if processor.onGatewayPresence != nil {
		processor.onGatewayPresence(gateway.ID, true)
	}
	return ProcessingResult{MessageID: envelope.MessageID, MessageType: MessageTypeEvent, TraceID: eventTraceID}, nil
}

func (processor *Processor) processStatus(ctx context.Context, gateway Gateway, topic string, rawPayload []byte, status StatusEnvelope) (ProcessingResult, error) {
	ctx, statusTraceID, span := processor.ensureUplinkTrace(ctx, "mqtt.uplink.status", gateway.ID, status.MessageID, status.TraceID)
	if span != nil {
		defer span.End()
	}
	accepted := true
	if sessions := processor.sessions(); sessions != nil {
		accepted = sessions.ApplyStatusAt(status, processor.nowUTC().UnixMilli(), true)
	}
	if accepted && gateway.DirectDevice && processor.runtime != nil {
		eventType := "DEVICE_DISCONNECTED"
		severity := "WARNING"
		if status.Online {
			eventType = "DEVICE_CONNECTED"
			severity = "INFO"
		}
		eventTime := processor.nowUTC().Format(time.RFC3339Nano)
		if status.HasEventTime && status.EventTime > 0 {
			eventTime = unixMillisRFC3339(status.EventTime)
		}
		dataPayload := json.RawMessage(`{}`)
		if trimmedSession := strings.TrimSpace(status.SessionID); trimmedSession != "" {
			if rawSession, marshalErr := json.Marshal(map[string]string{"sessionId": trimmedSession}); marshalErr == nil {
				dataPayload = rawSession
			}
		}
		if err := processor.runtime.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
			TenantID:   gateway.TenantID,
			SiteID:     gateway.SiteID,
			GatewayID:  gateway.ID,
			MessageID:  status.MessageID,
			Sequence:   status.Sequence,
			EventType:  eventType,
			SourceType: "DEVICE",
			SourceID:   gateway.ID,
			EventTime:  eventTime,
			Severity:   severity,
			Data:       dataPayload,
			Device:     &ResolvedDevice{TenantID: gateway.TenantID, SiteID: gateway.SiteID, DeviceID: gateway.ID},
			TraceID:    statusTraceID,
		}); err != nil {
			if span != nil {
				span.SetStatus("error", "accept direct device status runtime event failed")
			}
			return ProcessingResult{}, fmt.Errorf("accept direct device status runtime event: %w", err)
		}
	}
	if span != nil {
		span.SetStatus("ok", "")
	}
	processor.recordPacket("UPLINK", "STATUS", gateway.ID, topic, statusTraceID, rawPayload)
	if accepted {
		if processor.onGatewayPresence != nil {
			processor.onGatewayPresence(gateway.ID, status.Online)
		}
		if status.Online && processor.onGatewayConnected != nil {
			processor.onGatewayConnected(ctx, gateway.ID)
		}
	}
	return ProcessingResult{MessageID: status.MessageID, MessageType: MessageTypeStatus, TraceID: statusTraceID}, nil
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

func isDeviceConnectEvent(rawEventType string) bool {
	switch strings.ToUpper(strings.TrimSpace(rawEventType)) {
	case "DEVICE_CONNECTED", "EXPLICIT_CONNECT", "CONNECTED", "CONNECT", "SESSION_CONNECTED", "ONLINE":
		return true
	default:
		return false
	}
}

func isDeviceDisconnectEvent(rawEventType string) bool {
	switch strings.ToUpper(strings.TrimSpace(rawEventType)) {
	case "DEVICE_DISCONNECTED", "EXPLICIT_DISCONNECT", "DISCONNECTED", "DISCONNECT", "SESSION_DISCONNECTED", "OFFLINE", "LWT", "LAST_WILL":
		return true
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
	} else if sourceType == "DEVICE" && sourceID == "" && gateway.DirectDevice {
		resolvedDevice = &ResolvedDevice{TenantID: gateway.TenantID, SiteID: gateway.SiteID, DeviceID: gateway.ID}
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

// HandleBrokerGatewayDisconnect fences an offline broker notice or guard disconnect
// against newer active sessions before updating local presence and reporting unavailable
// coverage for the gateway and its child devices.
func (processor *Processor) HandleBrokerGatewayDisconnect(ctx context.Context, gatewayID string, disconnectedAt time.Time) error {
	if processor == nil {
		return nil
	}
	normalizedGatewayID := normalizeLowerUUIDv7(gatewayID)
	if normalizedGatewayID == "" {
		return nil
	}
	nowUTC := processor.nowUTC()
	ts := disconnectedAt.UTC()
	if ts.IsZero() {
		ts = nowUTC
	}
	if sessions := processor.sessions(); sessions != nil {
		accepted := sessions.ApplyTransition(SessionTransition{
			GatewayID:      normalizedGatewayID,
			EventTime:      ts.UnixMilli(),
			HasEventTime:   true,
			ServerTime:     ts.UnixMilli(),
			HasServerTime:  true,
			ServerObserved: true,
			Online:         false,
		})
		if !accepted {
			return nil
		}
	}
	if processor.onGatewayPresence != nil {
		processor.onGatewayPresence(normalizedGatewayID, false)
	}
	if processor.runtime == nil {
		return nil
	}
	var childrenResolver GatewayChildrenResolver
	if resolver, ok := processor.identities.(GatewayChildrenResolver); ok {
		childrenResolver = resolver
	}
	sourceRev := max(nowUTC.UnixNano(), 1)
	for {
		prev := processor.coverageRev.Load()
		if sourceRev <= prev {
			sourceRev = prev + 1
		}
		if processor.coverageRev.CompareAndSwap(prev, sourceRev) {
			break
		}
	}
	if err := ReportGatewayUnavailableCoverage(ctx, childrenResolver, processor.runtime, normalizedGatewayID, "GATEWAY_DISCONNECTED", ts, sourceRev); err != nil {
		return fmt.Errorf("report broker gateway disconnect coverage: %w", err)
	}
	return nil
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
