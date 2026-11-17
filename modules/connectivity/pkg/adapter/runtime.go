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
	"slices"
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
	defaultGatewayIdleTimeout     = 5 * time.Minute
	maxTrackedGatewayQueues       = 4096
)

type gatewayMailboxState struct {
	queue        chan queuedPublish
	pendingBatch []queuedPublish
	pendingMsgs  []UplinkMessage
	attempt      int
	inFlight     bool
	retrying     bool
	queued       bool
	enqueuers    int
	idleTimer    *time.Timer
	retryTimer   *time.Timer
	idleGen      uint64
	retryGen     uint64
	lastActive   time.Time
}

type gatewayTurn struct {
	gatewayID    string
	queue        chan queuedPublish
	pendingBatch []queuedPublish
	pendingMsgs  []UplinkMessage
	attempt      int
}

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

// Runtime consumes every Gateway's uplink. Each Gateway gets an ordered mailbox
// serviced by a bounded pool of workers. A message is acknowledged only after it is
// processed or quarantined; when a Gateway's queue is full, receiving pauses and the
// broker holds the backlog.
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
	idleTimeout      time.Duration
	now              func() time.Time

	coverageMu  sync.Mutex
	coverageWG  sync.WaitGroup
	coverageRev atomic.Int64
	connGen     atomic.Uint64

	queuesMu    sync.Mutex
	queues      map[string]chan queuedPublish
	states      map[string]*gatewayMailboxState
	readyQueue  []string
	readySignal chan struct{}
	poolOnce    sync.Once
	workers     sync.WaitGroup

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
		idleTimeout:      defaultGatewayIdleTimeout,
		now:              time.Now,
		queues:           map[string]chan queuedPublish{},
		states:           map[string]*gatewayMailboxState{},
		readySignal:      make(chan struct{}, 1),
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
	runtime.startWorkers(workerContext)
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
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.coverage_heartbeat")
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
		go func() {
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.mqtt_subscribe")
			runtime.subscribe(ctx, manager, connection, gen)
		}()
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
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.coverage_report")
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
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.coverage_disconnect_report")
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

func (runtime *Runtime) effectiveIdleTimeout() time.Duration {
	if runtime.idleTimeout > 0 {
		return runtime.idleTimeout
	}
	return defaultGatewayIdleTimeout
}

func (runtime *Runtime) startWorkers(ctx context.Context) {
	runtime.poolOnce.Do(func() {
		runtime.queuesMu.Lock()
		if runtime.queues == nil {
			runtime.queues = map[string]chan queuedPublish{}
		}
		if runtime.states == nil {
			runtime.states = map[string]*gatewayMailboxState{}
		}
		if runtime.readySignal == nil {
			runtime.readySignal = make(chan struct{}, 1)
		}
		runtime.queuesMu.Unlock()

		workerCount := runtime.config.effectiveWorkerCount()
		runtime.workers.Add(workerCount + 1)
		for range workerCount {
			go func() {
				defer runtime.workers.Done()
				runtime.runWorkerLoop(ctx)
			}()
		}
		go func() {
			defer runtime.workers.Done()
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.uplink_pool_shutdown")
			<-ctx.Done()
			runtime.stopAllGatewayTimers()
		}()
	})
}

func (runtime *Runtime) runWorkerLoop(ctx context.Context) {
	for ctx.Err() == nil {
		panicked := false
		func() {
			var currentGateway string
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.uplink_worker", func(any) {
				panicked = true
				if currentGateway != "" {
					runtime.finishGatewayPanic(ctx, currentGateway)
				}
			})
			for ctx.Err() == nil {
				turn, ok := runtime.awaitReadyGateway(ctx)
				if !ok {
					return
				}
				currentGateway = turn.gatewayID
				runtime.runGatewayTurn(ctx, turn)
				currentGateway = ""
			}
		}()
		if !panicked {
			return
		}
	}
}

func (runtime *Runtime) awaitReadyGateway(ctx context.Context) (gatewayTurn, bool) {
	for {
		if turn, ok := runtime.tryClaimReadyGateway(ctx); ok {
			return turn, true
		}
		select {
		case <-ctx.Done():
			return gatewayTurn{}, false
		case <-runtime.readySignal:
		}
	}
}

func (runtime *Runtime) tryClaimReadyGateway(ctx context.Context) (gatewayTurn, bool) {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	for len(runtime.readyQueue) > 0 {
		gatewayID := runtime.readyQueue[0]
		runtime.readyQueue[0] = ""
		runtime.readyQueue = runtime.readyQueue[1:]
		if len(runtime.readyQueue) == 0 {
			runtime.readyQueue = runtime.readyQueue[:0]
		} else if cap(runtime.readyQueue) > 64 && len(runtime.readyQueue)*2 <= cap(runtime.readyQueue) {
			compacted := make([]string, len(runtime.readyQueue))
			copy(compacted, runtime.readyQueue)
			runtime.readyQueue = compacted
		}
		state, ok := runtime.states[gatewayID]
		if !ok {
			continue
		}
		state.queued = false
		if state.inFlight || state.retrying {
			continue
		}
		if len(state.pendingBatch) == 0 && len(state.queue) == 0 {
			runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
			continue
		}
		if state.idleTimer != nil {
			state.idleTimer.Stop()
			state.idleTimer = nil
			state.idleGen++
		}
		state.inFlight = true
		state.lastActive = runtime.now()
		turn := gatewayTurn{
			gatewayID:    gatewayID,
			queue:        state.queue,
			pendingBatch: state.pendingBatch,
			pendingMsgs:  state.pendingMsgs,
			attempt:      state.attempt,
		}
		state.pendingBatch = nil
		state.pendingMsgs = nil
		state.attempt = 0
		if len(runtime.readyQueue) > 0 {
			runtime.notifyReadyLocked()
		}
		return turn, true
	}
	return gatewayTurn{}, false
}

// enqueue hands a message to its Gateway's queue and blocks while that queue is full,
// which stops reading from the broker instead of dropping the message. It gives up when
// the connection that delivered the message closes: the message was not acknowledged, so
// the broker redelivers it on the next connection.
func (runtime *Runtime) enqueue(ctx context.Context, connectionClosed <-chan struct{}, received *paho.Publish, ack func(*paho.Publish) error) error {
	runtime.startWorkers(ctx)
	packet := *received
	packet.Payload = append([]byte(nil), received.Payload...)
	gatewayID := TopicGatewayID(packet.Topic)
	queue, state, err := runtime.acquireGatewayQueue(gatewayID)
	if err != nil {
		return err
	}
	var sendErr error
	select {
	case queue <- queuedPublish{packet: &packet, ack: ack}:
	case <-connectionClosed:
		sendErr = errors.New("MQTT connection closed before the message was queued")
	case <-ctx.Done():
		sendErr = ctx.Err()
	}

	runtime.queuesMu.Lock()
	state.enqueuers--
	state.lastActive = runtime.now()
	depth := len(queue)
	if sendErr == nil {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
	} else {
		runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
	}
	runtime.queuesMu.Unlock()

	if sendErr == nil {
		runtime.recordQueueDepth(gatewayID, depth)
	}
	return sendErr
}

func (runtime *Runtime) acquireGatewayQueue(gatewayID string) (chan queuedPublish, *gatewayMailboxState, error) {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	if runtime.queues == nil {
		runtime.queues = map[string]chan queuedPublish{}
	}
	if runtime.states == nil {
		runtime.states = map[string]*gatewayMailboxState{}
	}
	if state, ok := runtime.states[gatewayID]; ok {
		if state.idleTimer != nil {
			state.idleTimer.Stop()
			state.idleTimer = nil
			state.idleGen++
		}
		state.enqueuers++
		state.lastActive = runtime.now()
		return state.queue, state, nil
	}
	if len(runtime.queues) >= maxTrackedGatewayQueues && !runtime.evictIdleGatewayLocked() {
		return nil, nil, fmt.Errorf("tracked gateway queue limit (%d) reached", maxTrackedGatewayQueues)
	}
	queue := make(chan queuedPublish, runtime.config.QueueCapacity)
	state := &gatewayMailboxState{
		queue:      queue,
		enqueuers:  1,
		lastActive: runtime.now(),
	}
	runtime.queues[gatewayID] = queue
	runtime.states[gatewayID] = state
	return queue, state, nil
}

func (runtime *Runtime) isIdleLocked(state *gatewayMailboxState) bool {
	return state != nil &&
		!state.inFlight &&
		!state.retrying &&
		!state.queued &&
		state.enqueuers == 0 &&
		len(state.pendingBatch) == 0 &&
		len(state.queue) == 0
}

func (runtime *Runtime) evictIdleGatewayLocked() bool {
	var oldestID string
	var oldestTime time.Time
	for gatewayID, state := range runtime.states {
		if !runtime.isIdleLocked(state) {
			continue
		}
		if oldestID == "" || state.lastActive.Before(oldestTime) {
			oldestID = gatewayID
			oldestTime = state.lastActive
		}
	}
	if oldestID == "" {
		return false
	}
	state := runtime.states[oldestID]
	if state.idleTimer != nil {
		state.idleTimer.Stop()
		state.idleTimer = nil
	}
	state.idleGen++
	delete(runtime.states, oldestID)
	delete(runtime.queues, oldestID)
	return true
}

func (runtime *Runtime) notifyReadyLocked() {
	select {
	case runtime.readySignal <- struct{}{}:
	default:
	}
}

func (runtime *Runtime) scheduleGatewayLocked(ctx context.Context, gatewayID string, state *gatewayMailboxState) {
	if ctx.Err() != nil {
		return
	}
	if state.idleTimer != nil {
		state.idleTimer.Stop()
		state.idleTimer = nil
		state.idleGen++
	}
	if state.inFlight || state.retrying || state.queued {
		return
	}
	if len(state.pendingBatch) == 0 && len(state.queue) == 0 {
		runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
		return
	}
	state.queued = true
	runtime.readyQueue = append(runtime.readyQueue, gatewayID)
	runtime.notifyReadyLocked()
}

func (runtime *Runtime) maybeScheduleIdleLocked(ctx context.Context, gatewayID string, state *gatewayMailboxState) {
	if ctx.Err() != nil || !runtime.isIdleLocked(state) {
		return
	}
	if state.idleTimer != nil {
		state.idleTimer.Stop()
	}
	state.idleGen++
	gen := state.idleGen
	timeout := runtime.effectiveIdleTimeout()
	state.idleTimer = time.AfterFunc(timeout, func() {
		runtime.reapIdleGateway(ctx, gatewayID, gen)
	})
}

func (runtime *Runtime) reapIdleGateway(ctx context.Context, gatewayID string, gen uint64) {
	if ctx.Err() != nil {
		return
	}
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	state, ok := runtime.states[gatewayID]
	if !ok || state.idleGen != gen || !runtime.isIdleLocked(state) {
		return
	}
	state.idleTimer = nil
	delete(runtime.states, gatewayID)
	delete(runtime.queues, gatewayID)
}

func (runtime *Runtime) stopAllGatewayTimers() {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	for _, state := range runtime.states {
		if state.idleTimer != nil {
			state.idleTimer.Stop()
			state.idleTimer = nil
		}
		state.idleGen++
		if state.retryTimer != nil {
			state.retryTimer.Stop()
			state.retryTimer = nil
		}
		state.retryGen++
		state.retrying = false
		state.queued = false
		state.pendingBatch = nil
		state.pendingMsgs = nil
		state.attempt = 0
	}
	runtime.readyQueue = nil
}

func (runtime *Runtime) runGatewayTurn(ctx context.Context, turn gatewayTurn) {
	batch := turn.pendingBatch
	messages := turn.pendingMsgs
	attempt := turn.attempt
	if len(batch) == 0 {
		select {
		case first := <-turn.queue:
			runtime.recordQueueDepth(turn.gatewayID, len(turn.queue))
			limit := min(runtime.config.effectiveBatchMaxMessages(), max(1, runtime.config.QueueCapacity))
			batch = runtime.drainBatch(ctx, turn.gatewayID, turn.queue, first, limit)
			if len(batch) == 0 {
				runtime.finishGatewayCancelled(turn.gatewayID)
				return
			}
			messages = make([]UplinkMessage, len(batch))
			for index, item := range batch {
				messages[index] = UplinkMessage{Topic: item.packet.Topic, Payload: item.packet.Payload}
			}
		default:
			runtime.finishGatewaySuccess(ctx, turn.gatewayID)
			return
		}
	}

	for len(batch) > 0 {
		currentAttempt := attempt + 1
		consumed, delay, err := runtime.processPrefix(ctx, turn.gatewayID, batch, messages, currentAttempt)
		if err != nil {
			if ctx.Err() != nil {
				runtime.finishGatewayCancelled(turn.gatewayID)
				return
			}
			runtime.scheduleGatewayRetry(ctx, turn.gatewayID, batch, messages, currentAttempt, delay)
			return
		}
		batch = batch[consumed:]
		messages = messages[consumed:]
		attempt = 0
	}
	runtime.finishGatewaySuccess(ctx, turn.gatewayID)
}

func (runtime *Runtime) finishGatewaySuccess(ctx context.Context, gatewayID string) {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	state, ok := runtime.states[gatewayID]
	if !ok {
		return
	}
	state.inFlight = false
	state.lastActive = runtime.now()
	if len(state.queue) > 0 {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
		return
	}
	runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
}

func (runtime *Runtime) finishGatewayCancelled(gatewayID string) {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	state, ok := runtime.states[gatewayID]
	if !ok {
		return
	}
	state.inFlight = false
	state.retrying = false
	state.pendingBatch = nil
	state.pendingMsgs = nil
	state.attempt = 0
}

func (runtime *Runtime) finishGatewayPanic(ctx context.Context, gatewayID string) {
	runtime.recordProcessingFailure(errors.New("MQTT uplink worker panicked"))
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	state, ok := runtime.states[gatewayID]
	if !ok {
		return
	}
	state.inFlight = false
	state.retrying = false
	state.pendingBatch = nil
	state.pendingMsgs = nil
	state.attempt = 0
	state.lastActive = runtime.now()
	if ctx.Err() != nil {
		return
	}
	if len(state.queue) > 0 {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
		return
	}
	runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
}

func (runtime *Runtime) scheduleGatewayRetry(ctx context.Context, gatewayID string, batch []queuedPublish, messages []UplinkMessage, attempt int, delay time.Duration) {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	state, ok := runtime.states[gatewayID]
	if !ok {
		return
	}
	state.inFlight = false
	if ctx.Err() != nil {
		state.retrying = false
		state.pendingBatch = nil
		state.pendingMsgs = nil
		state.attempt = 0
		return
	}
	state.retrying = true
	state.pendingBatch = slices.Clone(batch)
	state.pendingMsgs = slices.Clone(messages)
	state.attempt = attempt
	state.lastActive = runtime.now()
	if state.retryTimer != nil {
		state.retryTimer.Stop()
	}
	state.retryGen++
	gen := state.retryGen
	state.retryTimer = time.AfterFunc(delay, func() {
		runtime.triggerGatewayRetry(ctx, gatewayID, gen)
	})
}

func (runtime *Runtime) triggerGatewayRetry(ctx context.Context, gatewayID string, gen uint64) {
	if ctx.Err() != nil {
		return
	}
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	state, ok := runtime.states[gatewayID]
	if !ok || !state.retrying || state.retryGen != gen {
		return
	}
	state.retrying = false
	state.retryTimer = nil
	state.lastActive = runtime.now()
	runtime.scheduleGatewayLocked(ctx, gatewayID, state)
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

// processPrefix runs one attempt over the contiguous prefix of pending messages, acking
// committed or quarantined prefixes immediately. Any returned error is transient and leaves
// pending unacknowledged for non-blocking retry scheduling.
func (runtime *Runtime) processPrefix(ctx context.Context, gatewayID string, pending []queuedPublish, messages []UplinkMessage, attempt int) (int, time.Duration, error) {
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
		return len(outcomes), 0, nil
	}
	if err == nil {
		err = errors.New("MQTT uplink processor produced no outcomes")
	}
	runtime.recordProcessingFailure(err)
	_ = runtime.metrics.AddCounter("hvac_mqtt_message_retries_total", "MQTT uplink processing retries after transient failures.", map[string]string{"reason_family": "dependency"}, 1)
	delay := runtime.retryDelay(attempt)
	runtime.logger.Warn("mqtt_uplink_message_retrying", "gateway_id", gatewayID, "topic", pending[0].packet.Topic, "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
	return 0, delay, err
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
