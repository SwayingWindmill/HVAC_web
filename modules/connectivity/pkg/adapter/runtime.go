package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type queuedPublish struct {
	packet *paho.Publish
	ack    func(*paho.Publish) error
}

const (
	defaultCoverageReportInterval = 60 * time.Second
	MaximumCoverageBatchSize      = 2048
)

type CoverageDeviceLister interface {
	ListAllActiveManagedDeviceIDs(ctx context.Context) ([]string, error)
}

type CoverageReporter interface {
	ReportSourceCoverage(ctx context.Context, report SourceCoverageReport) error
}

type RuntimeOption func(*Runtime)

func WithCoverageReporter(lister CoverageDeviceLister, reporter CoverageReporter) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.coverageLister = lister
		runtime.coverageReporter = reporter
	}
}

// Runtime consumes every Gateway's uplink. Each Gateway gets its own ordered queue and
// worker, created when its first message arrives. A message is acknowledged only after
// it is processed or quarantined; when a Gateway's queue is full, receiving pauses and
// the broker holds the backlog.
type Runtime struct {
	config     Config
	processor  *Processor
	logger     *slog.Logger
	metrics    *observability.Registry
	retryDelay func(int) time.Duration
	// reconnectBackoff is how long to wait before reconnection attempt N.
	reconnectBackoff func(int) time.Duration
	coverageLister   CoverageDeviceLister
	coverageReporter CoverageReporter
	coverageInterval time.Duration
	now              func() time.Time

	coverageMu  sync.Mutex
	coverageWG  sync.WaitGroup
	coverageRev atomic.Int64
	connGen     atomic.Uint64

	queuesMu sync.Mutex
	queues   map[string]chan queuedPublish
	workers  sync.WaitGroup

	connMu     sync.Mutex
	connClosed chan struct{}

	mu          sync.RWMutex
	connected   bool
	subscribed  bool
	lastError   string
	lastSuccess time.Time
}

func NewRuntime(config Config, processor *Processor, logger *slog.Logger, metrics *observability.Registry, options ...RuntimeOption) (*Runtime, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if processor == nil {
		return nil, errors.New("MQTT uplink runtime requires a processor")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	runtime := &Runtime{
		config:           config,
		processor:        processor,
		logger:           logger,
		metrics:          metrics,
		retryDelay:       mqttRetryDelay,
		reconnectBackoff: autopaho.DefaultExponentialBackoff(),
		coverageInterval: defaultCoverageReportInterval,
		now:              time.Now,
		queues:           map[string]chan queuedPublish{},
	}
	for _, option := range options {
		if option != nil {
			option(runtime)
		}
	}
	return runtime, nil
}

func (runtime *Runtime) Ready() bool {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.connected && runtime.subscribed && runtime.lastError == ""
}

func (runtime *Runtime) LastSuccess() time.Time {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.lastSuccess
}

func (runtime *Runtime) Run(ctx context.Context) error {
	brokerURL, err := url.Parse(strings.TrimSpace(runtime.config.MQTT.BrokerURL))
	if err != nil {
		return fmt.Errorf("parse MQTT broker URL: %w", err)
	}
	tlsConfig, err := newMQTTTLSConfig(runtime.config.MQTT)
	if err != nil {
		return err
	}
	workerContext, workerCancel := context.WithCancel(ctx)
	var heartbeatWG sync.WaitGroup
	defer func() {
		workerCancel()
		heartbeatWG.Wait()
		runtime.coverageWG.Wait()
		runtime.workers.Wait()
	}()
	if runtime.coverageLister != nil && runtime.coverageReporter != nil && runtime.coverageInterval > 0 {
		heartbeatWG.Add(1)
		go func() {
			defer heartbeatWG.Done()
			runtime.runCoverageHeartbeat(workerContext)
		}()
	}

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     mqttKeepAliveSeconds,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         mqttSessionExpirySeconds,
		ConnectTimeout:                mqttConnectTimeout,
		ReconnectBackoff:              runtime.reconnectBackoff,
		AttemptConnection: func(attemptContext context.Context, _ autopaho.ClientConfig, server *url.URL) (net.Conn, error) {
			return runtime.dial(attemptContext, tlsConfig, server)
		},
		OnConnectError: func(connectErr error) {
			runtime.recordConnectionState(nil, false, false, connectErr)
			_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
			runtime.logger.Warn("mqtt_uplink_connect_failed", "error", connectErr.Error())
		},
		ClientConfig: paho.ClientConfig{
			ClientID:                   runtime.config.MQTT.ClientID,
			EnableManualAcknowledgment: true,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(received paho.PublishReceived) (bool, error) {
					if received.Packet == nil || received.Client == nil {
						return false, errors.New("MQTT publish callback is incomplete")
					}
					return true, runtime.enqueue(workerContext, runtime.currentConnectionClosed(), received.Packet, received.Client.Ack)
				},
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		gen := runtime.connGen.Load()
		connection := runtime.currentConnectionClosed()
		runtime.recordConnectionState(connection, true, false, nil)
		_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "success"}, 1)
		go runtime.subscribe(ctx, manager, connection, gen)
	}
	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		return fmt.Errorf("create MQTT connection: %w", err)
	}
	if err := manager.AwaitConnection(ctx); err != nil {
		return fmt.Errorf("await MQTT connection: %w", err)
	}
	var stopErr error
	select {
	case <-ctx.Done():
	case <-manager.Done():
		if ctx.Err() == nil {
			stopErr = errors.New("MQTT connection manager stopped")
		}
	}
	shutdownCoverageContext, coverageCancel := context.WithTimeout(context.Background(), 3*time.Second)
	runtime.reportCoverage(shutdownCoverageContext, false, "SOURCE_UNAVAILABLE", 0, true)
	coverageCancel()
	if stopErr != nil {
		return stopErr
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = manager.Disconnect(shutdownContext)
	return nil
}

func (runtime *Runtime) subscribe(ctx context.Context, manager *autopaho.ConnectionManager, connection <-chan struct{}, gen uint64) {
	subscribeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	subscriptions := make([]paho.SubscribeOptions, 0, len(UplinkTopicFilters))
	for _, topic := range UplinkTopicFilters {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: topic, QoS: 1})
	}
	if _, err := manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: subscriptions}); err != nil {
		runtime.recordConnectionState(connection, true, false, err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
		runtime.logger.Warn("mqtt_uplink_subscribe_failed", "error", err.Error())
		return
	}
	runtime.recordConnectionState(connection, true, true, nil)
	_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "success"}, 1)
	runtime.logger.Info("mqtt_uplink_subscribed", "topic_filters", UplinkTopicFilters)
	if ctx.Err() == nil && runtime.coverageLister != nil && runtime.coverageReporter != nil {
		runtime.coverageWG.Add(1)
		go func() {
			defer runtime.coverageWG.Done()
			runtime.reportCoverage(ctx, true, "", gen, false)
		}()
	}
}

// dial opens the TLS connection to the broker. Closing it is the first step of paho
// tearing a connection down, before paho waits for the publish handler to return, so
// the close is what releases a handler waiting on a full Gateway queue.
func (runtime *Runtime) dial(ctx context.Context, tlsConfig *tls.Config, server *url.URL) (net.Conn, error) {
	dialContext, cancel := context.WithTimeout(ctx, mqttConnectTimeout)
	defer cancel()
	conn, err := (&tls.Dialer{Config: tlsConfig}).DialContext(dialContext, "tcp", server.Host)
	if err != nil {
		return nil, err
	}
	closed := make(chan struct{})
	gen := runtime.connGen.Add(1)
	runtime.connMu.Lock()
	runtime.connClosed = closed
	runtime.connMu.Unlock()
	return packets.NewThreadSafeConn(&brokerConnection{Conn: conn, closed: closed, onClose: func() { runtime.connectionLost(ctx, gen) }}), nil
}

// brokerConnection closes closed and reports the loss the first time the connection closes.
type brokerConnection struct {
	net.Conn
	closeOnce sync.Once
	closed    chan struct{}
	onClose   func()
}

func (conn *brokerConnection) Close() error {
	conn.closeOnce.Do(func() {
		close(conn.closed)
		conn.onClose()
	})
	return conn.Conn.Close()
}

// currentConnectionClosed is closed when the connection now delivering messages closes.
// autopaho dials the next connection only after the previous client has fully stopped,
// so every message the handler sees came from this connection.
func (runtime *Runtime) currentConnectionClosed() <-chan struct{} {
	runtime.connMu.Lock()
	defer runtime.connMu.Unlock()
	return runtime.connClosed
}

func (runtime *Runtime) connectionLost(ctx context.Context, gen uint64) {
	runtime.mu.RLock()
	wasConnected := runtime.connected
	runtime.mu.RUnlock()
	if ctx.Err() != nil || !wasConnected {
		return
	}
	runtime.recordConnectionState(nil, false, false, errors.New("MQTT connection lost"))
	_ = runtime.metrics.AddCounter("hvac_mqtt_disconnections_total", "MQTT connection-down events.", map[string]string{"reason_family": "transport"}, 1)
	runtime.logger.Warn("mqtt_uplink_connection_lost")
	if runtime.coverageLister != nil && runtime.coverageReporter != nil {
		runtime.coverageWG.Add(1)
		go func() {
			defer runtime.coverageWG.Done()
			reportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			runtime.reportCoverage(reportCtx, false, "SOURCE_UNAVAILABLE", gen, false)
		}()
	}
}

func (runtime *Runtime) runCoverageHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(runtime.coverageInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if runtime.Ready() {
				runtime.reportCoverage(ctx, true, "", runtime.connGen.Load(), false)
			}
		}
	}
}

func (runtime *Runtime) reportCoverage(ctx context.Context, available bool, reasonCode string, expectedGen uint64, force bool) {
	if runtime.coverageLister == nil || runtime.coverageReporter == nil {
		return
	}
	runtime.coverageMu.Lock()
	defer runtime.coverageMu.Unlock()
	if !force {
		if expectedGen != 0 && expectedGen != runtime.connGen.Load() {
			return
		}
		if !available && runtime.Ready() {
			return
		}
		if available && !runtime.Ready() {
			return
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deviceIDs, err := runtime.coverageLister.ListAllActiveManagedDeviceIDs(callCtx)
	if err != nil {
		if runtime.logger != nil {
			runtime.logger.Warn("mqtt_coverage_device_list_failed", "error", err.Error())
		}
		return
	}
	if len(deviceIDs) == 0 {
		return
	}
	var reasonPtr *string
	if !available {
		reason := reasonCode
		if reason == "" {
			reason = "SOURCE_UNAVAILABLE"
		}
		reasonPtr = &reason
	}
	observedAt := runtime.now().UTC().Format(time.RFC3339Nano)
	for start := 0; start < len(deviceIDs); start += MaximumCoverageBatchSize {
		end := min(start+MaximumCoverageBatchSize, len(deviceIDs))
		rev := runtime.coverageRev.Add(1)
		report := SourceCoverageReport{
			SourceID:       runtime.config.MQTT.ClientID,
			DeviceIDs:      deviceIDs[start:end],
			Available:      available,
			ReasonCode:     reasonPtr,
			ObservedAt:     observedAt,
			SourceRevision: rev,
		}
		if err := runtime.coverageReporter.ReportSourceCoverage(callCtx, report); err != nil && runtime.logger != nil {
			runtime.logger.Warn("mqtt_coverage_report_failed", "available", available, "error", err.Error())
		}
	}
}

// enqueue hands a message to its Gateway's queue and blocks while that queue is full,
// which stops reading from the broker instead of dropping the message. It gives up when
// the connection that delivered the message closes: the message was not acknowledged, so
// the broker redelivers it on the next connection.
func (runtime *Runtime) enqueue(ctx context.Context, connectionClosed <-chan struct{}, received *paho.Publish, ack func(*paho.Publish) error) error {
	packet := *received
	packet.Payload = append([]byte(nil), received.Payload...)
	gatewayID := TopicGatewayID(packet.Topic)
	queue := runtime.queueFor(ctx, gatewayID)
	select {
	case queue <- queuedPublish{packet: &packet, ack: ack}:
		runtime.recordQueueDepth(gatewayID, len(queue))
		return nil
	case <-connectionClosed:
		return errors.New("MQTT connection closed before the message was queued")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *Runtime) queueFor(ctx context.Context, gatewayID string) chan queuedPublish {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	if queue, ok := runtime.queues[gatewayID]; ok {
		return queue
	}
	queue := make(chan queuedPublish, runtime.config.QueueCapacity)
	runtime.queues[gatewayID] = queue
	runtime.workers.Add(1)
	go func() {
		defer runtime.workers.Done()
		runtime.processQueue(ctx, gatewayID, queue)
	}()
	return queue
}

func (runtime *Runtime) processQueue(ctx context.Context, gatewayID string, queue chan queuedPublish) {
	limit := min(runtime.config.effectiveBatchMaxMessages(), max(1, runtime.config.QueueCapacity))
	for {
		select {
		case <-ctx.Done():
			return
		case first := <-queue:
			runtime.recordQueueDepth(gatewayID, len(queue))
			batch := runtime.drainBatch(ctx, gatewayID, queue, first, limit)
			if len(batch) == 0 {
				return
			}
			if !runtime.processBatch(ctx, gatewayID, batch) {
				return
			}
		}
	}
}

func (runtime *Runtime) drainBatch(ctx context.Context, gatewayID string, queue chan queuedPublish, first queuedPublish, limit int) []queuedPublish {
	batch := make([]queuedPublish, 1, limit)
	batch[0] = first
drainLoop:
	for len(batch) < limit {
		select {
		case item := <-queue:
			batch = append(batch, item)
		default:
			break drainLoop
		}
	}
	runtime.recordQueueDepth(gatewayID, len(queue))
	if runtime.config.BatchFlushWindow <= 0 || len(batch) >= limit {
		return batch
	}
	timer := time.NewTimer(runtime.config.BatchFlushWindow)
	defer timer.Stop()
	for len(batch) < limit {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return batch
		case item := <-queue:
			batch = append(batch, item)
			runtime.recordQueueDepth(gatewayID, len(queue))
		}
	}
	return batch
}

func (runtime *Runtime) processBatch(ctx context.Context, gatewayID string, batch []queuedPublish) bool {
	messages := make([]UplinkMessage, len(batch))
	for index, item := range batch {
		messages[index] = UplinkMessage{Topic: item.packet.Topic, Payload: item.packet.Payload}
	}
	for len(batch) > 0 {
		consumed, ok := runtime.processPrefix(ctx, gatewayID, batch, messages)
		if !ok {
			return false
		}
		batch = batch[consumed:]
		messages = messages[consumed:]
	}
	return true
}

// processPrefix retries a transient failure in place, keeping the Gateway's messages in
// order, until the contiguous prefix is processed, quarantined or Connectivity stops.
// Messages left unacknowledged at shutdown are redelivered by the broker.
func (runtime *Runtime) processPrefix(ctx context.Context, gatewayID string, pending []queuedPublish, messages []UplinkMessage) (int, bool) {
	for attempt := 1; ; attempt++ {
		started := time.Now()
		processContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		outcomes, err := runtime.processor.ProcessPrefix(processContext, messages)
		cancel()
		if err == nil && len(outcomes) > 0 {
			elapsed := time.Since(started)
			for index, outcome := range outcomes {
				item := pending[index]
				if isPermanentMessageError(outcome.Err) {
					runtime.ack(gatewayID, outcome.Result.MessageID, item)
					_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "quarantined"}, 1)
					runtime.logger.Warn("mqtt_uplink_message_quarantined", "gateway_id", gatewayID, "topic", item.packet.Topic, "error", outcome.Err.Error())
					continue
				}
				result := outcome.Result
				runtime.ack(gatewayID, result.MessageID, item)
				runtime.recordProcessingSuccess()
				runtime.recordProcessingResult(result, elapsed)
				runtime.logger.Info("mqtt_uplink_message_processed", "gateway_id", gatewayID, "message_id", result.MessageID, "message_type", result.MessageType,
					"point_count", result.PointCount, "accepted", result.Accepted, "duplicate", result.Duplicate, "out_of_order", result.OutOfOrder,
					"quarantined", result.Quarantined, "rejected", result.Rejected)
			}
			return len(outcomes), true
		}
		if err == nil {
			err = errors.New("MQTT uplink processor produced no outcomes")
		}
		runtime.recordProcessingFailure(err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_message_retries_total", "MQTT uplink processing retries after transient failures.", map[string]string{"reason_family": "dependency"}, 1)
		delay := runtime.retryDelay(attempt)
		runtime.logger.Warn("mqtt_uplink_message_retrying", "gateway_id", gatewayID, "topic", pending[0].packet.Topic, "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, false
		case <-timer.C:
		}
	}
}

// ack acknowledges a handled message on the connection that delivered it. When that
// connection has already dropped the ack fails and the broker redelivers the message on
// the next connection, where it is handled again as a duplicate. That is expected after a
// disconnect, so it is logged and counted but does not make Connectivity unready.
func (runtime *Runtime) ack(gatewayID, messageID string, item queuedPublish) {
	if err := item.ack(item.packet); err != nil {
		_ = runtime.metrics.AddCounter("hvac_mqtt_ack_failures_total", "MQTT uplink acknowledgements that failed and leave the message to broker redelivery.", nil, 1)
		runtime.logger.Warn("mqtt_uplink_ack_failed", "gateway_id", gatewayID, "message_id", messageID, "error", err.Error())
	}
}

func mqttRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 500 * time.Millisecond
	for step := 1; step < attempt && delay < 10*time.Second; step++ {
		delay *= 2
	}
	if delay > 10*time.Second {
		return 10 * time.Second
	}
	return delay
}

func (runtime *Runtime) recordQueueDepth(gatewayID string, depth int) {
	_ = runtime.metrics.SetGauge("hvac_mqtt_gateway_queue_depth", "Messages waiting in a Gateway's uplink queue.", map[string]string{"gateway_id": gatewayID}, float64(depth))
}

// recordConnectionState records the MQTT connection state. An update about a connection
// that has already closed is ignored, so a subscription finishing late cannot mark a lost
// connection ready again; nil applies the update unconditionally.
func (runtime *Runtime) recordConnectionState(connection <-chan struct{}, connected, subscribed bool, err error) {
	runtime.mu.Lock()
	select {
	case <-connection:
		runtime.mu.Unlock()
		return
	default:
	}
	runtime.connected = connected
	runtime.subscribed = subscribed
	if err != nil {
		runtime.lastError = err.Error()
	} else {
		runtime.lastError = ""
	}
	runtime.mu.Unlock()
	connectedValue := 0.0
	if connected {
		connectedValue = 1
	}
	subscribedValue := 0.0
	if subscribed {
		subscribedValue = 1
	}
	_ = runtime.metrics.SetGauge("hvac_mqtt_connected", "Whether Connectivity is connected to its MQTT broker.", nil, connectedValue)
	_ = runtime.metrics.SetGauge("hvac_mqtt_subscribed", "Whether Connectivity's uplink subscription is active.", nil, subscribedValue)
}

func (runtime *Runtime) recordProcessingResult(result ProcessingResult, elapsed time.Duration) {
	_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "success"}, 1)
	_ = runtime.metrics.ObserveHistogram("hvac_mqtt_message_processing_duration_seconds", "MQTT uplink message processing duration.", map[string]string{"outcome": "success"}, elapsed.Seconds(), nil)
	if result.Replay {
		_ = runtime.metrics.AddCounter("hvac_mqtt_replay_messages_total", "MQTT telemetry messages explicitly marked as replay.", map[string]string{"outcome": "processed"}, 1)
	}
	for outcome, count := range map[string]int{
		"accepted":     result.Accepted,
		"duplicate":    result.Duplicate,
		"out_of_order": result.OutOfOrder,
		"quarantined":  result.Quarantined,
		"rejected":     result.Rejected,
	} {
		if count > 0 {
			_ = runtime.metrics.AddCounter("hvac_mqtt_values_total", "Point values delivered through Connectivity by Telemetry outcome.", map[string]string{"outcome": outcome}, float64(count))
		}
	}
}

func (runtime *Runtime) recordProcessingFailure(err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.lastError = err.Error()
}

func (runtime *Runtime) recordProcessingSuccess() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.lastError = ""
	runtime.lastSuccess = time.Now().UTC()
}

func newMQTTTLSConfig(config MQTTConfig) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, errors.New("load MQTT client identity failed")
	}
	caContent, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, errors.New("read MQTT CA failed")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caContent) {
		return nil, errors.New("MQTT CA is invalid")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      rootCAs,
		Certificates: []tls.Certificate{certificate},
		ServerName:   strings.TrimSpace(config.ServerName),
	}, nil
}
