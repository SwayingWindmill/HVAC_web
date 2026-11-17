package mqttconnector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

const (
	commandSchemaVersion         = "2.0"
	replyRecoveryBatchLimit      = 50
	defaultReplyRecoveryInterval = 15 * time.Second
	defaultCrossReplicaPoll      = 500 * time.Millisecond
	maxCommandReplyPayloadBytes  = 256 << 10
	replyWorkerShards            = 16
	replyShardQueueCapacity      = 256
)

var (
	ErrPayloadMismatch        = errors.New("MQTT command attempt payload mismatch")
	ErrOldFence               = errors.New("MQTT command execution fence is stale")
	replyPersistRetryBackoffs = []time.Duration{20 * time.Millisecond, 60 * time.Millisecond}
)

type EvidenceStore interface {
	Prepare(context.Context, commandmodel.PreparedConnectorEvidence) error
	Complete(context.Context, commandmodel.CompletedConnectorEvidence) error
}

// Config is one command connection for every Gateway: commands go to
// hvac/v1/{gatewayId}/down/command and replies come back on hvac/v1/{gatewayId}/up/reply.
type Config struct {
	BrokerURL                string
	ClientID                 string
	SharedSubscriptionGroup  string
	CrossReplicaPollInterval time.Duration
	CAFile                   string
	CertFile                 string
	KeyFile                  string
	ServerName               string
	TransportState           TransportState
	EvidenceStore            EvidenceStore
	LateResultSink           LateResultSink
	ReplyTimeout             time.Duration
	Now                      func() time.Time
	Logger                   *slog.Logger
	Metrics                  *observability.Registry
	PacketHook               func(direction, channel, gatewayID, topic, traceID string, payload []byte)
}

// ReplyTopicFilter is every Gateway's command reply topic.
const ReplyTopicFilter = "hvac/v1/+/up/reply"

// SubscriptionTopicFilter returns the MQTT v5 subscription filter, adding the
// $share/<group>/ prefix when SharedSubscriptionGroup is configured.
func (config Config) SubscriptionTopicFilter() string {
	return mqttclient.SharedTopicFilter(config.SharedSubscriptionGroup, ReplyTopicFilter)
}

type commandPolicy struct {
	RequiresReadback     bool  `json:"requiresReadback"`
	VerificationWindowMS int64 `json:"verificationWindowMs"`
}

type commandEnvelope struct {
	SchemaVersion   string                         `json:"schemaVersion"`
	MessageID       string                         `json:"messageId"`
	CommandID       string                         `json:"commandId"`
	TraceID         string                         `json:"traceId,omitempty"`
	IssuedAt        int64                          `json:"issuedAt"`
	ExpireAt        int64                          `json:"expireAt"`
	DeviceID        string                         `json:"deviceId"`
	CommandCode     string                         `json:"commandCode"`
	Params          commandmodel.CommandParameters `json:"params"`
	Policy          commandPolicy                  `json:"policy"`
	ExecutionFence  uint64                         `json:"executionFence"`
	PayloadHash     string                         `json:"payloadHash"`
	PointCode       string                         `json:"pointCode,omitempty"`
	ActuationMode   string                         `json:"actuationMode,omitempty"`
	Channel         string                         `json:"channel,omitempty"`
	CoilAddress     *int                           `json:"coilAddress,omitempty"`
	PulseDurationMs int                            `json:"pulseDurationMs,omitempty"`
}

func buildCommandEnvelope(
	envelope commandmodel.DispatchEnvelope,
	route commandmodel.DeviceRoute,
	externalDeviceID string,
	now time.Time,
	expireAt time.Time,
	verificationWindow time.Duration,
) commandEnvelope {
	request := commandEnvelope{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      envelope.AttemptID,
		CommandID:      envelope.CommandID,
		TraceID:        commandmodel.NormalizeTraceID(envelope.TraceID),
		IssuedAt:       now.UnixMilli(),
		ExpireAt:       expireAt.UnixMilli(),
		DeviceID:       externalDeviceID,
		CommandCode:    string(envelope.Capability),
		Params:         envelope.Parameters,
		Policy:         commandPolicy{RequiresReadback: true, VerificationWindowMS: verificationWindow.Milliseconds()},
		ExecutionFence: envelope.ExecutionFence,
		PayloadHash:    envelope.PayloadHash,
	}
	if route.ActuationMode != "" {
		request.PointCode = route.PointCode
		request.ActuationMode = route.ActuationMode
		request.Channel = route.Channel
		request.PulseDurationMs = route.PulseDurationMs
		if route.HasCoilAddress {
			addr := route.CoilAddress
			request.CoilAddress = &addr
		}
	}
	return request
}

type commandReply struct {
	SchemaVersion     string                              `json:"schemaVersion"`
	MessageID         string                              `json:"messageId"`
	CommandID         string                              `json:"commandId"`
	TraceID           string                              `json:"traceId,omitempty"`
	EventTime         int64                               `json:"eventTime"`
	EdgeStatus        string                              `json:"edgeStatus"`
	ExecutionEvidence *commandmodel.EdgeExecutionEvidence `json:"executionEvidence,omitempty"`
	ReasonCode        *string                             `json:"reasonCode"`
	ExecutionFence    uint64                              `json:"executionFence"`
}

type queuedReply struct {
	gatewayID        string
	commandID        string
	executionFence   uint64
	replySHA256      string
	status           string
	eventTime        time.Time
	reason           string
	edgeExecution    *commandmodel.EdgeExecutionEvidence
	packet           *paho.Publish
	ackFn            func(*paho.Publish) error
	connectionClosed <-chan struct{}
}

type Connector struct {
	rootContext context.Context
	config      Config
	manager     *autopaho.ConnectionManager

	mu           sync.Mutex
	waiters      map[string]chan commandmodel.CommandCorrelation
	recoverMu    sync.Mutex
	stopRecovery context.CancelFunc
	recoveryDone chan struct{}
	publish      func(context.Context, *paho.Publish) (*paho.PublishResponse, error)

	connOnce    sync.Once
	connTracker *mqttclient.ConnTracker

	replyPoolOnce sync.Once
	replyMu       sync.RWMutex
	replyStarted  bool
	replyStopping bool
	replyShards   [replyWorkerShards]chan queuedReply
	replyWG       sync.WaitGroup
}

func (connector *Connector) tracker() *mqttclient.ConnTracker {
	connector.connOnce.Do(func() {
		connector.connTracker = mqttclient.NewConnTracker()
	})
	return connector.connTracker
}

func New(ctx context.Context, config Config) (*Connector, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	brokerURL, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}
	tlsConfig, err := mqttclient.NewMTLSConfig(mqttclient.TLSConfig{
		CACertFile:     config.CAFile,
		ClientCertFile: config.CertFile,
		ClientKeyFile:  config.KeyFile,
		ServerName:     config.ServerName,
	})
	if err != nil {
		return nil, err
	}
	if config.ReplyTimeout == 0 {
		config.ReplyTimeout = 15 * time.Second
	}
	if config.CrossReplicaPollInterval <= 0 && config.SharedSubscriptionGroup != "" {
		config.CrossReplicaPollInterval = defaultCrossReplicaPoll
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	loopContext, stopRecovery := context.WithCancel(ctx)
	connector := &Connector{
		rootContext:  ctx,
		config:       config,
		waiters:      make(map[string]chan commandmodel.CommandCorrelation),
		stopRecovery: stopRecovery,
		recoveryDone: make(chan struct{}),
	}
	connector.startReplyWorkers()
	logger, metrics := connector.recoveryDeps()
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         24 * 60 * 60,
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		AttemptConnection: func(attemptContext context.Context, _ autopaho.ClientConfig, server *url.URL) (net.Conn, error) {
			return connector.customDialContext(attemptContext, tlsConfig, server)
		},
		ClientConfig: paho.ClientConfig{
			ClientID:                   strings.TrimSpace(config.ClientID),
			EnableManualAcknowledgment: true,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				connector.onReply,
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		connection := connector.currentConnectionClosed()
		go func() {
			defer observability.Recover(logger, metrics, "command.mqtt_subscribe")
			connector.subscribeAndRecover(loopContext, manager, connection)
		}()
	}
	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		connector.drainReplyWorkers(context.Background())
		stopRecovery()
		close(connector.recoveryDone)
		return nil, fmt.Errorf("create MQTT command connection: %w", err)
	}
	connector.manager = manager
	go connector.runReplyRecoveryLoop(loopContext, defaultReplyRecoveryInterval)
	return connector, nil
}

// NewOffline creates a brokerless Connector for correlation recovery and
// state-machine verification without establishing a TLS MQTT connection.
func NewOffline(config Config) (*Connector, error) {
	if config.TransportState == nil || config.EvidenceStore == nil {
		return nil, errors.New("MQTT command stores are incomplete")
	}
	if config.ReplyTimeout == 0 {
		config.ReplyTimeout = 15 * time.Second
	}
	if config.CrossReplicaPollInterval <= 0 && config.SharedSubscriptionGroup != "" {
		config.CrossReplicaPollInterval = defaultCrossReplicaPoll
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Connector{
		config:  config,
		waiters: make(map[string]chan commandmodel.CommandCorrelation),
	}, nil
}

func (connector *Connector) Execute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error) {
	if connector == nil || (connector.manager == nil && connector.publish == nil) {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command connector is unavailable")
	}
	route, err := connector.config.TransportState.ResolveCommandRoute(ctx, envelope)
	if failureCode := commandmodel.RouteFailureCode(err); failureCode != "" {
		// Nothing has been prepared or published: the command is provably not sent.
		return commandmodel.ConnectorResult{Phase: commandmodel.ConnectorPreSendRejected, FailureCode: failureCode}, nil
	}
	if err != nil {
		return commandmodel.ConnectorResult{}, fmt.Errorf("resolve command route: %w", err)
	}
	externalDeviceID := strings.TrimSpace(route.ExternalDeviceID)
	if route.GatewayID == "" || externalDeviceID == "" || route.BindingRevision == 0 {
		return commandmodel.ConnectorResult{}, errors.New("command route is incomplete")
	}
	commandTopic := "hvac/v1/" + route.GatewayID + "/down/command"
	method, err := capabilityMethod(envelope.Capability)
	if err != nil {
		return commandmodel.ConnectorResult{}, err
	}
	now := connector.config.Now().UTC()
	expireAt := capCommandExpireAt(now, envelope.LeaseUntil.UTC(), connector.config.ReplyTimeout)
	if expireAt.IsZero() || !expireAt.After(now) {
		return commandmodel.ConnectorResult{}, errors.New("command dispatch lease is expired")
	}
	verificationWindow := commandmodel.VerificationWindowFor(envelope.Capability)
	request := buildCommandEnvelope(envelope, route, externalDeviceID, now, expireAt, verificationWindow)
	body, err := json.Marshal(request)
	if err != nil {
		return commandmodel.ConnectorResult{}, err
	}
	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: envelope.AttemptID, CommandID: envelope.CommandID,
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		ExternalDeviceID: externalDeviceID, ExecutionFence: envelope.ExecutionFence,
		PayloadHash:      envelope.PayloadHash,
		MappingRevision:  "mqtt-command-v1:" + envelope.CapabilityRevision,
		BindingRevision:  "registry-source-key:" + strconv.FormatUint(route.BindingRevision, 10),
		ProviderEndpoint: commandTopic,
		ProviderMethod:   method,
		RequestSHA256:    sha256Hex(body),
		PreparedAt:       now,
	}
	if err := connector.config.EvidenceStore.Prepare(ctx, prepared); err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT connector evidence preparation failed")
	}
	correlation, err := connector.config.TransportState.PrepareCommandCorrelation(ctx, commandmodel.CommandCorrelation{
		Envelope: envelope, GatewayID: route.GatewayID, ExternalDeviceID: externalDeviceID,
		MappingRevision: prepared.MappingRevision, BindingRevision: prepared.BindingRevision,
		ProviderEndpoint: prepared.ProviderEndpoint, ProviderMethod: prepared.ProviderMethod,
		RequestSHA256: prepared.RequestSHA256, PreparedAt: prepared.PreparedAt,
		State: commandmodel.CorrelationPrepared,
	})
	if err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command correlation preparation failed")
	}
	if correlation.Envelope.PayloadHash != envelope.PayloadHash {
		return commandmodel.ConnectorResult{}, ErrPayloadMismatch
	}
	switch ResumeActionForCorrelation(correlation.State) {
	case ResumeUseReply:
		return connector.completeRecoveredReply(ctx, correlation)
	case ResumeOutcomeUnknown:
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_RECOVERED_MAY_COMMIT")
	case ResumePublish:
		// PREPARED is the only state from which a physical publish is permitted.
	default:
		return commandmodel.ConnectorResult{}, errors.New("MQTT command correlation state is invalid")
	}

	waiterKey := replyKey(envelope.CommandID, envelope.ExecutionFence)
	waiter := make(chan commandmodel.CommandCorrelation, 1)
	connector.mu.Lock()
	connector.waiters[waiterKey] = waiter
	connector.mu.Unlock()
	defer func() {
		connector.mu.Lock()
		delete(connector.waiters, waiterKey)
		connector.mu.Unlock()
	}()

	armedAt := connector.config.Now().UTC()
	if err := connector.config.TransportState.ArmCommandCorrelation(ctx, envelope.TenantID, envelope.AttemptID, envelope.ExecutionFence, armedAt); err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command publish commit-point arm failed")
	}

	remainingBeforePublish := expireAt.Sub(connector.config.Now().UTC())
	expirySeconds := messageExpirySeconds(remainingBeforePublish)
	connector.emitPacketHook("DOWNLINK", "COMMAND", route.GatewayID, commandTopic, request.TraceID, body)
	publishContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, publishErr := connector.publishCommand(publishContext, &paho.Publish{
		QoS:        1,
		Retain:     false,
		Topic:      commandTopic,
		Payload:    body,
		Properties: &paho.PublishProperties{MessageExpiry: &expirySeconds},
	})
	cancel()
	if publishErr != nil {
		publishOutcome := "failed"
		if errors.Is(publishErr, context.DeadlineExceeded) {
			publishOutcome = "timed_out"
		}
		connector.addCounter("hvac_mqtt_command_publishes_total", "MQTT command publish attempts by outcome.", map[string]string{"outcome": publishOutcome}, 1)
		// The durable correlation is armed before calling the MQTT client. A process
		// crash or publish error after this point is not provably unsent, so retrying
		// the physical write would be unsafe.
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_PUBLISH_OUTCOME_UNKNOWN")
	}
	connector.addCounter("hvac_mqtt_command_publishes_total", "MQTT command publish attempts by outcome.", map[string]string{"outcome": "success"}, 1)

	waitStartedAt := connector.config.Now().UTC()
	wait := expireAt.Sub(waitStartedAt)
	if connector.config.ReplyTimeout > 0 && connector.config.ReplyTimeout < wait {
		wait = connector.config.ReplyTimeout
	}
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	var pollTicker *time.Ticker
	var pollCh <-chan time.Time
	loader, hasLoader := connector.config.TransportState.(CommandCorrelationLoader)
	if connector.config.SharedSubscriptionGroup != "" && hasLoader {
		pollInterval := connector.config.CrossReplicaPollInterval
		if pollInterval <= 0 {
			pollInterval = defaultCrossReplicaPoll
		}
		pollTicker = time.NewTicker(pollInterval)
		defer pollTicker.Stop()
		pollCh = pollTicker.C
	}

	finishContextDone := func() (commandmodel.ConnectorResult, error) {
		outcome := "failed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			outcome = "timed_out"
		}
		connector.recordReplyWaitMetric(outcome, waitStartedAt)
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_CONTEXT_DONE")
	}

	for {
		select {
		case <-ctx.Done():
			return finishContextDone()
		case <-timer.C:
			connector.recordReplyWaitMetric("timed_out", waitStartedAt)
			return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_REPLY_TIMEOUT")
		case replyCorrelation := <-waiter:
			if pollTicker != nil {
				pollTicker.Stop()
			}
			timer.Stop()
			result, err := connector.completeRecoveredReply(ctx, replyCorrelation)
			connector.recordReplyWaitMetric(replyWaitOutcome(result, err), waitStartedAt)
			return result, err
		case <-pollCh:
			if ctx.Err() != nil {
				return finishContextDone()
			}
			pollCtx, pollCancel := context.WithTimeout(ctx, 2*time.Second)
			loaded, loadErr := loader.LoadCommandCorrelation(pollCtx, envelope.TenantID, envelope.AttemptID, envelope.ExecutionFence)
			pollCancel()
			if loadErr == nil && (loaded.State == commandmodel.CorrelationReplied || loaded.State == commandmodel.CorrelationResolved) {
				pollTicker.Stop()
				timer.Stop()
				result, err := connector.completeRecoveredReply(ctx, loaded)
				connector.recordReplyWaitMetric(replyWaitOutcome(result, err), waitStartedAt)
				return result, err
			}
		}
	}
}

func (connector *Connector) publishCommand(ctx context.Context, publish *paho.Publish) (*paho.PublishResponse, error) {
	if connector.publish != nil {
		return connector.publish(ctx, publish)
	}
	if connector.manager == nil {
		return nil, errors.New("MQTT command connector is unavailable")
	}
	return connector.manager.Publish(ctx, publish)
}

func capCommandExpireAt(now, leaseUntil time.Time, replyTimeout time.Duration) time.Time {
	if leaseUntil.IsZero() {
		return time.Time{}
	}
	expireAt := leaseUntil.UTC()
	if replyTimeout > 0 {
		if replyDeadline := now.UTC().Add(replyTimeout); replyDeadline.Before(expireAt) {
			expireAt = replyDeadline
		}
	}
	return expireAt
}

func messageExpirySeconds(remaining time.Duration) uint32 {
	seconds := math.Ceil(remaining.Seconds())
	if seconds < 1 {
		return 1
	}
	if seconds > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(seconds)
}

func (connector *Connector) Disconnect(ctx context.Context) error {
	if connector == nil {
		return nil
	}
	connector.drainReplyWorkers(ctx)
	if connector.stopRecovery != nil {
		connector.stopRecovery()
	}
	if connector.recoveryDone != nil {
		select {
		case <-connector.recoveryDone:
		case <-ctx.Done():
		}
	}
	if connector.manager == nil {
		return nil
	}
	return connector.manager.Disconnect(ctx)
}

func (connector *Connector) FinalizeDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	if connector == nil || !ReplyBackedResult(result) {
		return nil
	}
	return connector.config.TransportState.MarkCommandCorrelationResolved(ctx, envelope.TenantID, envelope.AttemptID, envelope.ExecutionFence, connector.config.Now().UTC())
}

// ReplyBackedResult reports whether a ConnectorResult corresponds to a durable reply.
func ReplyBackedResult(result commandmodel.ConnectorResult) bool {
	return result.Acknowledged || strings.HasPrefix(result.FailureCode, "MQTT_EDGE_") || result.FailureCode == "MQTT_COMMAND_REPLY_INVALID"
}

func (connector *Connector) customDialContext(ctx context.Context, tlsConfig *tls.Config, server *url.URL) (net.Conn, error) {
	conn, _, _, err := connector.tracker().DialTLS(ctx, tlsConfig, server.Host, 10*time.Second, nil)
	return conn, err
}

func (connector *Connector) currentConnectionClosed() <-chan struct{} {
	if connector == nil {
		return nil
	}
	return connector.tracker().CurrentClosed()
}

func (connector *Connector) closeConnection(expected <-chan struct{}) {
	if connector == nil {
		return
	}
	_ = connector.tracker().CloseIfClosed(expected)
}

func checkSubackReasons(suback *paho.Suback) error {
	return mqttclient.CheckSubackReasons(suback, 1)
}

func (connector *Connector) subscribeAndRecover(ctx context.Context, manager *autopaho.ConnectionManager, connection <-chan struct{}) {
	topicFilter := connector.config.SubscriptionTopicFilter()
	logger, _ := connector.recoveryDeps()
	sub := &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topicFilter, QoS: 1}},
	}
	lastErr := mqttclient.SubscribeWithRetry(
		ctx,
		manager,
		sub,
		10*time.Second,
		mqttclient.DefaultSubscribeRetryDelays,
		func() bool { return mqttclient.IsClosedSignal(connection) },
	)
	if lastErr == nil {
		connector.addCounter("hvac_mqtt_command_subscriptions_total", "MQTT command reply subscription attempts by outcome.", map[string]string{"outcome": "success"}, 1)
		go connector.recoverReplies(ctx)
		return
	}
	if ctx.Err() != nil || mqttclient.IsClosedSignal(connection) {
		return
	}
	connector.addCounter("hvac_mqtt_command_subscriptions_total", "MQTT command reply subscription attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
	logger.Warn("mqtt_command_subscribe_failed", "topic_filter", topicFilter, "error", lastErr.Error())
	connector.closeConnection(connection)
}

func (connector *Connector) startReplyWorkers() {
	connector.replyPoolOnce.Do(func() {
		connector.replyMu.Lock()
		if connector.replyStopping {
			connector.replyMu.Unlock()
			return
		}
		for i := range connector.replyShards {
			connector.replyShards[i] = make(chan queuedReply, replyShardQueueCapacity)
		}
		connector.replyStarted = true
		connector.replyWG.Add(replyWorkerShards)
		for i := range connector.replyShards {
			ch := connector.replyShards[i]
			go connector.runReplyShardWorker(ch)
		}
		connector.replyMu.Unlock()
	})
}

func (connector *Connector) drainReplyWorkers(ctx context.Context) {
	connector.replyMu.Lock()
	started := connector.replyStarted
	if !connector.replyStopping {
		connector.replyStopping = true
		if started {
			for _, ch := range connector.replyShards {
				close(ch)
			}
		}
	}
	connector.replyMu.Unlock()
	if !started {
		return
	}
	done := make(chan struct{})
	go func() {
		connector.replyWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (connector *Connector) runReplyShardWorker(ch <-chan queuedReply) {
	defer connector.replyWG.Done()
	logger, metrics := connector.recoveryDeps()
	for {
		panicked := false
		func() {
			defer observability.Recover(logger, metrics, "command.mqtt_reply_worker", func(any) {
				panicked = true
			})
			for item := range ch {
				connector.processReply(item)
			}
		}()
		if !panicked {
			return
		}
	}
}

func replyShardIndex(gatewayID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(gatewayID))
	return int(h.Sum32() % replyWorkerShards)
}

func (connector *Connector) enqueueReply(item queuedReply) (enqueued bool, stopping bool) {
	shard := replyShardIndex(item.gatewayID)
	connector.replyMu.RLock()
	defer connector.replyMu.RUnlock()
	if !connector.replyStarted || connector.replyStopping {
		return false, true
	}
	select {
	case connector.replyShards[shard] <- item:
		return true, false
	default:
		return false, false
	}
}

func (connector *Connector) replyBaseContext() context.Context {
	if connector != nil && connector.rootContext != nil {
		return connector.rootContext
	}
	return context.Background()
}

func (connector *Connector) addCounter(name, help string, labels map[string]string, delta float64) {
	if connector == nil || connector.config.Metrics == nil {
		return
	}
	_ = connector.config.Metrics.AddCounter(name, help, labels, delta)
}

func (connector *Connector) recordReplyMetric(outcome string) {
	connector.addCounter("hvac_mqtt_command_replies_total", "MQTT command reply deliveries by outcome.", map[string]string{"outcome": outcome}, 1)
}

func (connector *Connector) emitPacketHook(direction, channel, gatewayID, topic, traceID string, payload []byte) {
	if connector == nil || connector.config.PacketHook == nil {
		return
	}
	connector.config.PacketHook(direction, channel, gatewayID, topic, traceID, bytes.Clone(payload))
}

func (connector *Connector) recordReplyWaitMetric(outcome string, startedAt time.Time) {
	if connector == nil || connector.config.Metrics == nil {
		return
	}
	elapsed := connector.config.Now().UTC().Sub(startedAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	_ = connector.config.Metrics.ObserveHistogram(
		"hvac_mqtt_command_reply_wait_seconds",
		"MQTT command reply wait duration by outcome.",
		map[string]string{"outcome": outcome},
		elapsed,
		nil,
	)
}

func replyWaitOutcome(result commandmodel.ConnectorResult, err error) string {
	if err != nil {
		return "failed"
	}
	switch result.Phase {
	case commandmodel.ConnectorAcknowledged:
		return "acknowledged"
	case commandmodel.ConnectorExecutionRejected:
		return "rejected"
	default:
		return "failed"
	}
}

func isPermanentCorrelationError(err error) bool {
	return errors.Is(err, commandmodel.ErrCommandCorrelationNotFound) ||
		errors.Is(err, commandmodel.ErrCommandCorrelationMismatch)
}

func (connector *Connector) ackReply(item queuedReply) {
	if item.ackFn == nil || item.packet == nil || mqttclient.IsClosedSignal(item.connectionClosed) {
		return
	}
	if err := item.ackFn(item.packet); err != nil {
		logger, _ := connector.recoveryDeps()
		logger.Warn("mqtt_command_reply_ack_failed", "gateway_id", item.gatewayID, "topic", item.packet.Topic, "error", err.Error())
	}
}

func (connector *Connector) processReply(item queuedReply) {
	logger, _ := connector.recoveryDeps()
	baseCtx := connector.replyBaseContext()
	var correlation commandmodel.CommandCorrelation
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if baseCtx.Err() != nil || mqttclient.IsClosedSignal(item.connectionClosed) {
			return
		}
		attemptCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
		var tenantID string
		tenantID, err = connector.config.TransportState.GatewayTenant(attemptCtx, item.gatewayID)
		if err == nil {
			correlation, err = connector.config.TransportState.RecordCommandReply(
				attemptCtx,
				tenantID,
				item.gatewayID,
				item.commandID,
				item.executionFence,
				item.replySHA256,
				item.status,
				item.eventTime,
				item.reason,
				item.edgeExecution,
				connector.config.Now().UTC(),
			)
		}
		cancel()
		if err == nil {
			break
		}
		if isPermanentCorrelationError(err) {
			logger.Warn("mqtt_command_reply_unmatched", "gateway_id", item.gatewayID, "command_id", item.commandID, "execution_fence", item.executionFence, "error", err.Error())
			connector.recordReplyMetric("unmatched")
			connector.ackReply(item)
			return
		}
		if attempt < len(replyPersistRetryBackoffs) && !mqttclient.IsClosedSignal(item.connectionClosed) {
			timer := time.NewTimer(replyPersistRetryBackoffs[attempt])
			select {
			case <-baseCtx.Done():
				timer.Stop()
				return
			case <-item.connectionClosed:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
	if err != nil {
		logger.Warn("mqtt_command_reply_persist_failed", "gateway_id", item.gatewayID, "command_id", item.commandID, "execution_fence", item.executionFence, "error", err.Error())
		connector.recordReplyMetric("transient_error")
		connector.closeConnection(item.connectionClosed)
		return
	}

	// Durable REPLIED custody is committed before acknowledging to the broker.
	connector.ackReply(item)
	connector.recordReplyMetric("persisted")

	key := replyKey(item.commandID, item.executionFence)
	connector.mu.Lock()
	waiter := connector.waiters[key]
	connector.mu.Unlock()
	if waiter != nil {
		select {
		case waiter <- correlation:
		default:
		}
		return
	}
	resolveCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
	defer cancel()
	_ = connector.ResolveCorrelationReply(resolveCtx, correlation)
}

func (connector *Connector) onReply(received paho.PublishReceived) (bool, error) {
	if received.Packet == nil {
		return false, nil
	}
	logger, _ := connector.recoveryDeps()
	connectionClosed := connector.currentConnectionClosed()
	ackImmediate := func() {
		if received.Client == nil || mqttclient.IsClosedSignal(connectionClosed) {
			return
		}
		if err := received.Client.Ack(received.Packet); err != nil {
			logger.Warn("mqtt_command_reply_ack_failed", "topic", received.Packet.Topic, "error", err.Error())
		}
	}
	gatewayID, ok := replyGatewayID(received.Packet.Topic)
	if !ok {
		ackImmediate()
		connector.recordReplyMetric("ignored")
		return true, nil
	}
	if len(received.Packet.Payload) == 0 || len(received.Packet.Payload) > maxCommandReplyPayloadBytes {
		ackImmediate()
		connector.recordReplyMetric("invalid")
		return true, nil
	}
	var reply commandReply
	decoder := json.NewDecoder(bytes.NewReader(received.Packet.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		ackImmediate()
		connector.recordReplyMetric("invalid")
		return true, nil
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		ackImmediate()
		connector.recordReplyMetric("invalid")
		return true, nil
	}
	connector.emitPacketHook("UPLINK", "COMMAND_REPLY", gatewayID, received.Packet.Topic, strings.TrimSpace(reply.TraceID), received.Packet.Payload)
	status := strings.ToUpper(strings.TrimSpace(reply.EdgeStatus))
	if status == "RECEIVED" || status == "VALIDATING" || status == "WRITING" {
		ackImmediate()
		connector.recordReplyMetric("ignored")
		return true, nil
	}
	if strings.TrimSpace(reply.CommandID) == "" || reply.ExecutionFence == 0 {
		ackImmediate()
		connector.recordReplyMetric("invalid")
		return true, nil
	}
	replyBody, _ := json.Marshal(reply)
	reason := ""
	if reply.ReasonCode != nil {
		reason = strings.ToUpper(strings.TrimSpace(*reply.ReasonCode))
	}
	eventTime := time.UnixMilli(reply.EventTime).UTC()
	if reply.SchemaVersion != commandSchemaVersion || strings.TrimSpace(reply.MessageID) == "" || reply.EventTime <= 0 ||
		(status == "EXECUTED" && (reply.ExecutionEvidence == nil || !reply.ExecutionEvidence.ValidExecuted())) ||
		(reply.ExecutionEvidence != nil && !reply.ExecutionEvidence.Valid()) {
		status = "INVALID"
		reason = ""
		eventTime = time.Time{}
		reply.ExecutionEvidence = nil
	}
	connector.startReplyWorkers()
	var ackFn func(*paho.Publish) error
	if received.Client != nil {
		ackFn = received.Client.Ack
	}
	task := queuedReply{
		gatewayID:        gatewayID,
		commandID:        reply.CommandID,
		executionFence:   reply.ExecutionFence,
		replySHA256:      sha256Hex(replyBody),
		status:           status,
		eventTime:        eventTime,
		reason:           reason,
		edgeExecution:    reply.ExecutionEvidence,
		packet:           received.Packet,
		ackFn:            ackFn,
		connectionClosed: connectionClosed,
	}
	enqueued, stopping := connector.enqueueReply(task)
	if stopping {
		return true, nil
	}
	if !enqueued {
		logger.Warn("mqtt_command_reply_backpressure_shed", "gateway_id", gatewayID, "command_id", reply.CommandID, "execution_fence", reply.ExecutionFence)
		connector.recordReplyMetric("shed")
		connector.closeConnection(connectionClosed)
		return true, nil
	}
	return true, nil
}

func (connector *Connector) recoveryDeps() (*slog.Logger, *observability.Registry) {
	if connector == nil {
		return slog.Default(), nil
	}
	logger := connector.config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return logger, connector.config.Metrics
}

func (connector *Connector) runReplyRecoveryLoop(ctx context.Context, interval time.Duration) {
	if connector.recoveryDone != nil {
		defer close(connector.recoveryDone)
	}
	logger, metrics := connector.recoveryDeps()
	defer observability.Recover(logger, metrics, "command.mqtt_reply_recovery_loop")
	if interval <= 0 {
		interval = defaultReplyRecoveryInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			connector.recoverReplies(ctx)
		}
	}
}

func (connector *Connector) recoverReplies(ctx context.Context) {
	logger, metrics := connector.recoveryDeps()
	defer observability.Recover(logger, metrics, "command.mqtt_reply_recovery")
	if connector.config.LateResultSink == nil {
		return
	}
	if !connector.recoverMu.TryLock() {
		return
	}
	defer connector.recoverMu.Unlock()
	recoveryContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tenants, err := connector.config.TransportState.Tenants(recoveryContext)
	if err != nil {
		return
	}
	for _, tenantID := range tenants {
		if recoveryContext.Err() != nil {
			return
		}
		if err := connector.DrainTenantReplies(recoveryContext, tenantID, replyRecoveryBatchLimit); err != nil && recoveryContext.Err() != nil {
			return
		}
	}
}

// DrainTenantReplies drains persisted REPLIED correlations for a Tenant across
// batches of batchLimit until fewer than batchLimit rows remain or no progress occurs.
func (connector *Connector) DrainTenantReplies(ctx context.Context, tenantID string, batchLimit int) error {
	if connector.config.LateResultSink == nil {
		return nil
	}
	if batchLimit <= 0 {
		batchLimit = replyRecoveryBatchLimit
	}
	var firstErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fetched, resolved, err := connector.recoverTenantReplyBatch(ctx, tenantID, batchLimit)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if fetched < batchLimit || resolved == 0 {
			return firstErr
		}
	}
}

// RecoverTenantReplies drains up to limit persisted REPLIED correlations for a Tenant.
func (connector *Connector) RecoverTenantReplies(ctx context.Context, tenantID string, limit int) error {
	if connector.config.LateResultSink == nil {
		return nil
	}
	_, _, err := connector.recoverTenantReplyBatch(ctx, tenantID, limit)
	return err
}

func (connector *Connector) recoverTenantReplyBatch(ctx context.Context, tenantID string, limit int) (fetched int, resolved int, firstErr error) {
	correlations, err := connector.config.TransportState.RecoverCommandReplies(ctx, tenantID, limit)
	if err != nil {
		return 0, 0, err
	}
	for _, correlation := range correlations {
		if ctx.Err() != nil {
			return len(correlations), resolved, ctx.Err()
		}
		if err := connector.ResolveCorrelationReply(ctx, correlation); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resolved++
	}
	return len(correlations), resolved, firstErr
}

// ResolveCorrelationReply settles one persisted REPLIED correlation against LateResultSink.
func (connector *Connector) ResolveCorrelationReply(ctx context.Context, correlation commandmodel.CommandCorrelation) error {
	if connector.config.LateResultSink == nil || correlation.State != commandmodel.CorrelationReplied {
		return nil
	}
	result, err := connector.completeRecoveredReply(ctx, correlation)
	if err != nil {
		return err
	}
	if err := connector.config.LateResultSink.ResolveDispatch(ctx, correlation.Envelope, result); err != nil &&
		!errors.Is(err, commandservice.ErrStaleFence) &&
		!errors.Is(err, commandservice.ErrCommandNotFound) {
		return err
	}
	return connector.config.TransportState.MarkCommandCorrelationResolved(ctx, correlation.Envelope.TenantID, correlation.Envelope.AttemptID, correlation.Envelope.ExecutionFence, connector.config.Now().UTC())
}

func (connector *Connector) completeRecoveredReply(ctx context.Context, correlation commandmodel.CommandCorrelation) (commandmodel.ConnectorResult, error) {
	completed := CompletedEvidenceFromCorrelation(correlation)
	result := connectorResult(completed)
	if err := connector.config.EvidenceStore.Complete(ctx, completed); err != nil {
		return commandmodel.ConnectorResult{}, err
	}
	return result, nil
}

func (connector *Connector) completeOutcomeUnknown(ctx context.Context, prepared commandmodel.PreparedConnectorEvidence, failureCode string) (commandmodel.ConnectorResult, error) {
	completed := commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared,
		RequestWritten:            true,
		ConnectorPhase:            commandmodel.ConnectorRequestCommitted,
		FailureCode:               failureCode,
		CompletedAt:               connector.config.Now().UTC(),
	}
	result := connectorResult(completed)
	if err := connector.config.EvidenceStore.Complete(ctx, completed); err != nil {
		result.FailureCode = "CONNECTOR_EVIDENCE_COMPLETION_FAILED"
	}
	return result, nil
}

// CompletedEvidenceFromCorrelation converts a durable MQTT command correlation into CompletedConnectorEvidence.
func CompletedEvidenceFromCorrelation(correlation commandmodel.CommandCorrelation) commandmodel.CompletedConnectorEvidence {
	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: correlation.Envelope.AttemptID, CommandID: correlation.Envelope.CommandID,
		TenantID: correlation.Envelope.TenantID, SiteID: correlation.Envelope.SiteID, DeviceID: correlation.Envelope.DeviceID,
		ExternalDeviceID: correlation.ExternalDeviceID, ExecutionFence: correlation.Envelope.ExecutionFence,
		PayloadHash:     correlation.Envelope.PayloadHash,
		MappingRevision: correlation.MappingRevision, BindingRevision: correlation.BindingRevision,
		ProviderEndpoint: correlation.ProviderEndpoint, ProviderMethod: correlation.ProviderMethod,
		RequestSHA256: correlation.RequestSHA256, PreparedAt: correlation.PreparedAt,
	}
	completed := commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared,
		RequestWritten:            true,
		ResponseSHA256:            correlation.ReplySHA256,
		CompletedAt:               correlation.RepliedAt,
		EdgeExecution:             correlation.EdgeExecution,
	}
	status := strings.ToUpper(strings.TrimSpace(correlation.ReplyStatus))
	if correlation.ReplyEventTime.IsZero() {
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
		return completed
	}
	reason := status
	if strings.TrimSpace(correlation.ReplyReasonCode) != "" {
		reason = strings.ToUpper(strings.TrimSpace(correlation.ReplyReasonCode))
	}
	switch status {
	case "EXECUTED":
		if correlation.EdgeExecution == nil || !correlation.EdgeExecution.ValidExecuted() {
			completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
			completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
			break
		}
		completed.ProviderStatusCode = 200
		completed.ConnectorPhase = commandmodel.ConnectorAcknowledged
	case "REJECTED", "EXPIRED":
		completed.ProviderStatusCode = 200
		completed.ConnectorPhase = commandmodel.ConnectorExecutionRejected
		completed.FailureCode = "MQTT_EDGE_" + reason
	case "FAILED", "TIMEOUT":
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "MQTT_EDGE_" + reason
	default:
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
	}
	return completed
}

func connectorResult(evidence commandmodel.CompletedConnectorEvidence) commandmodel.ConnectorResult {
	return commandmodel.ConnectorResult{
		Phase:         evidence.ConnectorPhase,
		FailureCode:   evidence.FailureCode,
		EvidenceID:    "mqtt:" + evidence.AttemptID + ":" + fmt.Sprint(evidence.ExecutionFence),
		Acknowledged:  evidence.ConnectorPhase == commandmodel.ConnectorAcknowledged,
		Verified:      false,
		EdgeExecution: evidence.EdgeExecution,
	}
}

func validateConfig(config Config) error {
	parsed, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil || parsed.Scheme != "tls" || parsed.Host == "" {
		return errors.New("MQTT command brokerUrl must be a tls:// endpoint")
	}
	if strings.TrimSpace(config.ClientID) == "" {
		return errors.New("MQTT command identity is incomplete")
	}
	if err := mqttclient.ValidateShareGroup(config.SharedSubscriptionGroup); err != nil {
		return errors.New("MQTT command shared subscription group must match ^[A-Za-z0-9_-]{1,64}$")
	}
	if strings.TrimSpace(config.CAFile) == "" || strings.TrimSpace(config.CertFile) == "" || strings.TrimSpace(config.KeyFile) == "" || strings.TrimSpace(config.ServerName) == "" {
		return errors.New("MQTT command TLS configuration is incomplete")
	}
	if config.TransportState == nil || config.EvidenceStore == nil {
		return errors.New("MQTT command durable dependencies are incomplete")
	}
	if config.ReplyTimeout < 0 || config.ReplyTimeout > 60*time.Second {
		return errors.New("MQTT command reply timeout is invalid")
	}
	return nil
}

func capabilityMethod(capability commandmodel.Capability) (string, error) {
	switch capability {
	case commandmodel.CapabilityStart:
		return "start", nil
	case commandmodel.CapabilityStop:
		return "stop", nil
	case commandmodel.CapabilityStartUnitSequence:
		return "startUnitSequence", nil
	case commandmodel.CapabilityStopUnitSequence:
		return "stopUnitSequence", nil
	case commandmodel.CapabilityResetFault:
		return "resetFault", nil
	case commandmodel.CapabilitySetTemperatureSetpoint:
		return "setTemperatureSetpoint", nil
	case commandmodel.CapabilitySetChilledWaterTemperatureSetpoint:
		return "setChilledWaterTemperatureSetpoint", nil
	case commandmodel.CapabilitySetFrequency:
		return "setFrequency", nil
	case commandmodel.CapabilitySetFanSpeed:
		return "setFanSpeed", nil
	case commandmodel.CapabilitySetLoadLimit:
		return "setLoadLimit", nil
	case commandmodel.CapabilitySetOpening:
		return "setOpening", nil
	default:
		return "", errors.New("command capability has no native MQTT method")
	}
}

// replyGatewayID returns the Gateway of an hvac/v1/{gatewayId}/up/reply topic,
// stripping an optional MQTT v5 $share/<group>/ prefix.
func replyGatewayID(topic string) (string, bool) {
	parts := strings.Split(mqttclient.StripSharePrefix(topic), "/")
	if len(parts) != 5 || parts[0] != "hvac" || parts[1] != "v1" || parts[3] != "up" || parts[4] != "reply" ||
		parts[2] != strings.TrimSpace(parts[2]) || !commandmodel.IsUUIDv7(parts[2]) {
		return "", false
	}
	return parts[2], true
}

// ResumeAction defines how a recovered MQTT command correlation may resume.
type ResumeAction uint8

const (
	ResumeInvalid ResumeAction = iota
	ResumePublish
	ResumeOutcomeUnknown
	ResumeUseReply
)

// ResumeActionForCorrelation maps a persisted correlation state to the only safe recovery action.
func ResumeActionForCorrelation(state commandmodel.CorrelationState) ResumeAction {
	switch state {
	case commandmodel.CorrelationPrepared:
		return ResumePublish
	case commandmodel.CorrelationMayCommit:
		return ResumeOutcomeUnknown
	case commandmodel.CorrelationReplied, commandmodel.CorrelationResolved:
		return ResumeUseReply
	default:
		return ResumeInvalid
	}
}

func replyKey(commandID string, fence uint64) string {
	return strings.TrimSpace(commandID) + "|" + strconv.FormatUint(fence, 10)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
