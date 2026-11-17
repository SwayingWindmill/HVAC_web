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

const (
	defaultCoverageReportInterval = 60 * time.Second
	MaximumCoverageBatchSize      = 2048
	defaultGatewayIdleTimeout     = 5 * time.Minute
	maxTrackedGatewayQueues       = 4096
)

type gatewayMailboxState struct {
	mailbox      *gatewayMailbox
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
	mailbox      *gatewayMailbox
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

	coverageMu     sync.Mutex
	coverageClosed bool
	coverageWG     sync.WaitGroup
	coverageRev    atomic.Int64
	connGen        atomic.Uint64

	queuesMu         sync.Mutex
	queues           map[string]*gatewayMailbox
	states           map[string]*gatewayMailboxState
	readyQueue       []string
	readySignal      chan struct{}
	drainSignal      chan struct{}
	overflowDir      string
	ownedOverflowDir bool
	poolOnce         sync.Once
	workers          sync.WaitGroup

	connMu     sync.Mutex
	connClosed chan struct{}
	connCloser func()

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
	config.OverflowMaxMessages = config.effectiveOverflowMaxMessages()
	config.OverflowMaxBytes = config.effectiveOverflowMaxBytes()
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
		queues:           map[string]*gatewayMailbox{},
		states:           map[string]*gatewayMailboxState{},
		readySignal:      make(chan struct{}, 1),
		drainSignal:      make(chan struct{}, 1),
		overflowDir:      strings.TrimSpace(config.OverflowDir),
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
	if err := runtime.prepareOverflowDir(); err != nil {
		return err
	}
	runtime.coverageMu.Lock()
	runtime.coverageClosed = false
	runtime.coverageMu.Unlock()
	heartbeatContext, heartbeatCancel := context.WithCancel(ctx)
	workerContext, workerCancel := context.WithCancel(context.Background())
	runtime.startWorkers(workerContext)
	var heartbeatWG sync.WaitGroup
	var drained bool
	defer func() {
		heartbeatCancel()
		if !drained {
			workerCancel()
		}
		heartbeatWG.Wait()
		runtime.coverageWG.Wait()
		runtime.workers.Wait()
		runtime.closeMailboxes()
	}()
	if err := runtime.recoverOverflowMailboxes(workerContext); err != nil {
		return err
	}
	if runtime.coverageLister != nil && runtime.coverageReporter != nil && runtime.coverageInterval > 0 {
		heartbeatWG.Add(1)
		go func() {
			defer heartbeatWG.Done()
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.coverage_heartbeat")
			runtime.runCoverageHeartbeat(heartbeatContext)
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
	heartbeatCancel()
	if stopErr == nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = manager.Disconnect(shutdownContext)
		cancel()
	}
	runtime.recordConnectionState(nil, false, false, nil)
	heartbeatWG.Wait()
	runtime.coverageWG.Wait()
	shutdownCoverageContext, coverageCancel := context.WithTimeout(context.Background(), 3*time.Second)
	runtime.reportCoverage(shutdownCoverageContext, false, "SOURCE_UNAVAILABLE", 0, true)
	coverageCancel()
	if stopErr != nil {
		return stopErr
	}
	runtime.waitDrainWithTimeout(5 * time.Second)
	workerCancel()
	runtime.workers.Wait()
	runtime.closeMailboxes()
	drained = true
	return nil
}

func (runtime *Runtime) subscribe(ctx context.Context, manager *autopaho.ConnectionManager, connection <-chan struct{}, gen uint64) {
	subscribeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	filters := runtime.config.SubscriptionTopicFilters()
	subscriptions := make([]paho.SubscribeOptions, 0, len(filters))
	for _, topic := range filters {
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
	runtime.logger.Info("mqtt_uplink_subscribed", "topic_filters", filters)
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
	wrapped := &brokerConnection{Conn: conn, closed: closed, onClose: func() { runtime.connectionLost(ctx, gen) }}
	runtime.connMu.Lock()
	runtime.connClosed = closed
	runtime.connCloser = func() { _ = wrapped.Close() }
	runtime.connMu.Unlock()
	return packets.NewThreadSafeConn(wrapped), nil
}

func (runtime *Runtime) closeCurrentConnection() {
	runtime.connMu.Lock()
	closer := runtime.connCloser
	runtime.connMu.Unlock()
	if closer != nil {
		closer()
	}
}

func (runtime *Runtime) notifyDrain() {
	if runtime.drainSignal == nil {
		return
	}
	select {
	case runtime.drainSignal <- struct{}{}:
	default:
	}
}

func (runtime *Runtime) hasPendingWork() bool {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	for _, state := range runtime.states {
		if !runtime.isIdleLocked(state) {
			return true
		}
	}
	return false
}

func (runtime *Runtime) waitDrainWithTimeout(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for runtime.hasPendingWork() {
		select {
		case <-runtime.drainSignal:
		case <-ticker.C:
		case <-timer.C:
			return
		}
	}
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
	if runtime.coverageClosed {
		return
	}
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
	} else if !available {
		runtime.coverageClosed = true
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
			runtime.queues = map[string]*gatewayMailbox{}
		}
		if runtime.states == nil {
			runtime.states = map[string]*gatewayMailboxState{}
		}
		if runtime.readySignal == nil {
			runtime.readySignal = make(chan struct{}, 1)
		}
		if runtime.drainSignal == nil {
			runtime.drainSignal = make(chan struct{}, 1)
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
			var currentMailbox *gatewayMailbox
			var currentEndSeq int64
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.uplink_worker", func(any) {
				panicked = true
				if currentGateway != "" {
					runtime.finishGatewayPanic(ctx, currentGateway, currentMailbox, currentEndSeq)
				}
			})
			for ctx.Err() == nil {
				turn, ok := runtime.awaitReadyGateway(ctx)
				if !ok {
					return
				}
				currentGateway = turn.gatewayID
				currentMailbox = turn.mailbox
				currentEndSeq = 0
				runtime.runGatewayTurn(ctx, turn, &currentEndSeq)
				currentGateway = ""
				currentMailbox = nil
				currentEndSeq = 0
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
		if !runtime.hasGatewayWorkLocked(state) {
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
			mailbox:      state.mailbox,
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

// enqueue hands a message to its Gateway's non-blocking WAL mailbox without waiting on
// a worker, eliminating cross-Gateway head-of-line blocking on the shared MQTT reader
// and broker inflight window. If RAM + disk overflow limits or disk write errors prevent
// custody, the connection closes without ACK so the broker redelivers on reconnect.
func (runtime *Runtime) enqueue(ctx context.Context, connectionClosed <-chan struct{}, received *paho.Publish, ack func(*paho.Publish) error) error {
	if received == nil {
		return errors.New("MQTT publish packet is missing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-connectionClosed:
		return errors.New("MQTT connection closed before the message was queued")
	default:
	}

	runtime.startWorkers(ctx)
	gatewayID := TopicGatewayID(received.Topic)
	mailbox, state, err := runtime.acquireGatewayMailbox(gatewayID)
	if err != nil {
		if runtime.logger != nil {
			runtime.logger.Warn("mqtt_uplink_mailbox_open_failed", "gateway_id", gatewayID, "topic", received.Topic, "error", err.Error())
		}
		runtime.closeCurrentConnection()
		return err
	}
	result, enqueueErr := mailbox.enqueue(received)

	runtime.queuesMu.Lock()
	state.enqueuers--
	state.lastActive = runtime.now()
	if enqueueErr == nil && (!result.shed || result.memDepth+result.overflowDepth > 0) {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
	} else {
		runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
	}
	runtime.queuesMu.Unlock()

	if enqueueErr != nil {
		if runtime.logger != nil {
			runtime.logger.Warn("mqtt_uplink_spool_write_failed", "gateway_id", gatewayID, "topic", received.Topic, "error", enqueueErr.Error())
		}
		runtime.closeCurrentConnection()
		return enqueueErr
	}
	runtime.recordQueueDepth(gatewayID, result.memDepth+result.overflowDepth)
	runtime.recordOverflowDepth(gatewayID, result.overflowDepth)
	if result.shed {
		_ = runtime.metrics.AddCounter("hvac_mqtt_gateway_overflow_shed_total", "MQTT uplink messages left unacknowledged because a Gateway filled RAM and disk overflow.", map[string]string{"gateway_id": gatewayID}, 1)
		runtime.logger.Warn("mqtt_uplink_gateway_overflow_shed", "gateway_id", gatewayID, "topic", received.Topic, "mem_depth", result.memDepth, "overflow_depth", result.overflowDepth)
		runtime.closeCurrentConnection()
		return errors.New("MQTT gateway mailbox overflow shed")
	}
	runtime.ack(gatewayID, received, ack)
	return nil
}

func (runtime *Runtime) prepareOverflowDir() error {
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	return runtime.ensureOverflowDirLocked()
}

func (runtime *Runtime) recoverOverflowMailboxes(workerCtx context.Context) error {
	runtime.startWorkers(workerCtx)
	runtime.queuesMu.Lock()
	defer runtime.queuesMu.Unlock()
	if runtime.overflowDir == "" {
		return nil
	}
	recovered, count, err := recoverSpoolMailboxes(
		runtime.overflowDir,
		runtime.config.QueueCapacity,
		runtime.config.effectiveOverflowMaxMessages(),
		runtime.config.effectiveOverflowMaxBytes(),
	)
	if err != nil {
		return fmt.Errorf("recover MQTT overflow spools: %w", err)
	}
	now := runtime.now()
	for gatewayID, mb := range recovered {
		if _, ok := runtime.states[gatewayID]; ok {
			mb.close(false)
			continue
		}
		state := &gatewayMailboxState{
			mailbox:    mb,
			lastActive: now,
		}
		runtime.queues[gatewayID] = mb
		runtime.states[gatewayID] = state
		memDepth, overflowDepth := mb.depth()
		runtime.recordQueueDepth(gatewayID, memDepth+overflowDepth)
		runtime.recordOverflowDepth(gatewayID, overflowDepth)
		runtime.scheduleGatewayLocked(workerCtx, gatewayID, state)
	}
	if count > 0 && runtime.logger != nil {
		runtime.logger.Info("mqtt_uplink_spool_recovered", "gateways", len(recovered), "messages", count)
	}
	return nil
}

func (runtime *Runtime) ensureOverflowDirLocked() error {
	if runtime.overflowDir != "" {
		return os.MkdirAll(runtime.overflowDir, 0o700)
	}
	dir, err := os.MkdirTemp("", "hvac-mqtt-overflow-*")
	if err != nil {
		return fmt.Errorf("create MQTT overflow spool directory: %w", err)
	}
	runtime.overflowDir = dir
	runtime.ownedOverflowDir = true
	return nil
}

func (runtime *Runtime) closeMailboxes() {
	runtime.queuesMu.Lock()
	mailboxes := make([]*gatewayMailbox, 0, len(runtime.queues))
	for gatewayID, mailbox := range runtime.queues {
		mailboxes = append(mailboxes, mailbox)
		delete(runtime.queues, gatewayID)
	}
	for gatewayID, state := range runtime.states {
		if state.idleTimer != nil {
			state.idleTimer.Stop()
			state.idleTimer = nil
		}
		if state.retryTimer != nil {
			state.retryTimer.Stop()
			state.retryTimer = nil
		}
		delete(runtime.states, gatewayID)
	}
	runtime.readyQueue = nil
	removeFiles := runtime.ownedOverflowDir
	ownedDir := ""
	if runtime.ownedOverflowDir {
		ownedDir = runtime.overflowDir
		runtime.overflowDir = ""
		runtime.ownedOverflowDir = false
	}
	runtime.queuesMu.Unlock()

	for _, mailbox := range mailboxes {
		if mailbox != nil {
			mailbox.close(removeFiles)
		}
	}
	if ownedDir != "" {
		_ = os.RemoveAll(ownedDir)
	}
}

func (runtime *Runtime) acquireGatewayMailbox(gatewayID string) (*gatewayMailbox, *gatewayMailboxState, error) {
	var evicted []*gatewayMailbox
	runtime.queuesMu.Lock()
	if runtime.queues == nil {
		runtime.queues = map[string]*gatewayMailbox{}
	}
	if runtime.states == nil {
		runtime.states = map[string]*gatewayMailboxState{}
	}
	if err := runtime.ensureOverflowDirLocked(); err != nil {
		runtime.queuesMu.Unlock()
		return nil, nil, err
	}
	if state, ok := runtime.states[gatewayID]; ok {
		if state.idleTimer != nil {
			state.idleTimer.Stop()
			state.idleTimer = nil
			state.idleGen++
		}
		state.enqueuers++
		state.lastActive = runtime.now()
		runtime.queuesMu.Unlock()
		return state.mailbox, state, nil
	}
	if len(runtime.queues) >= maxTrackedGatewayQueues {
		evictedMailbox := runtime.evictIdleGatewayLocked()
		if evictedMailbox == nil && len(runtime.queues) >= maxTrackedGatewayQueues {
			runtime.queuesMu.Unlock()
			return nil, nil, fmt.Errorf("tracked gateway queue limit (%d) reached", maxTrackedGatewayQueues)
		}
		if evictedMailbox != nil {
			evicted = append(evicted, evictedMailbox)
		}
	}
	mailbox := newGatewayMailbox(
		gatewayID,
		runtime.config.QueueCapacity,
		runtime.config.effectiveOverflowMaxMessages(),
		runtime.config.effectiveOverflowMaxBytes(),
		runtime.overflowDir,
	)
	state := &gatewayMailboxState{
		mailbox:    mailbox,
		enqueuers:  1,
		lastActive: runtime.now(),
	}
	runtime.queues[gatewayID] = mailbox
	runtime.states[gatewayID] = state
	runtime.queuesMu.Unlock()
	for _, mb := range evicted {
		mb.close(true)
	}
	return mailbox, state, nil
}

func (runtime *Runtime) hasGatewayWorkLocked(state *gatewayMailboxState) bool {
	if state == nil {
		return false
	}
	if len(state.pendingBatch) > 0 {
		return true
	}
	if state.mailbox != nil {
		memDepth, overflowDepth := state.mailbox.depth()
		return memDepth+overflowDepth > 0
	}
	return false
}

func (runtime *Runtime) isIdleLocked(state *gatewayMailboxState) bool {
	return state != nil &&
		!state.inFlight &&
		!state.retrying &&
		!state.queued &&
		state.enqueuers == 0 &&
		!runtime.hasGatewayWorkLocked(state)
}

func (runtime *Runtime) evictIdleGatewayLocked() *gatewayMailbox {
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
		return nil
	}
	state := runtime.states[oldestID]
	if state.idleTimer != nil {
		state.idleTimer.Stop()
		state.idleTimer = nil
	}
	state.idleGen++
	delete(runtime.states, oldestID)
	delete(runtime.queues, oldestID)
	return state.mailbox
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
	if !runtime.hasGatewayWorkLocked(state) {
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
	if ctx.Err() != nil {
		runtime.queuesMu.Unlock()
		return
	}
	state, ok := runtime.states[gatewayID]
	if !ok || state.idleGen != gen || !runtime.isIdleLocked(state) {
		runtime.queuesMu.Unlock()
		return
	}
	state.idleTimer = nil
	mb := state.mailbox
	delete(runtime.states, gatewayID)
	delete(runtime.queues, gatewayID)
	runtime.queuesMu.Unlock()
	if mb != nil {
		mb.close(true)
	}
	runtime.notifyDrain()
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

func (runtime *Runtime) runGatewayTurn(ctx context.Context, turn gatewayTurn, activeEndSeq *int64) {
	batch := turn.pendingBatch
	messages := turn.pendingMsgs
	attempt := turn.attempt
	limit := runtime.config.effectiveBatchMaxMessages()
	if len(batch) == 0 {
		if turn.mailbox == nil {
			runtime.finishGatewaySuccess(ctx, turn.gatewayID)
			return
		}
		var memDepth, overflowDepth, readErrors int
		batch, memDepth, overflowDepth, readErrors = turn.mailbox.popBatch(limit)
		runtime.recordQueueDepth(turn.gatewayID, memDepth+overflowDepth)
		runtime.recordOverflowDepth(turn.gatewayID, overflowDepth)
		if readErrors > 0 {
			_ = runtime.metrics.AddCounter("hvac_mqtt_spool_read_errors_total", "MQTT uplink messages dropped due to overflow spool corruption or read failures.", nil, float64(readErrors))
			runtime.logger.Warn("mqtt_uplink_overflow_read_failed", "gateway_id", turn.gatewayID, "count", readErrors)
		}
		if len(batch) > 0 && runtime.config.BatchFlushWindow > 0 && len(batch) < limit {
			batch = runtime.finishBatchWindow(ctx, turn.gatewayID, turn.mailbox, batch, limit)
			if len(batch) == 0 {
				runtime.finishGatewayCancelled(turn.gatewayID)
				return
			}
		}
		if len(batch) == 0 {
			if readErrors > 0 && memDepth+overflowDepth > 0 {
				runtime.scheduleGatewayRetry(ctx, turn.gatewayID, nil, nil, attempt+1, 10*time.Millisecond)
				return
			}
			runtime.finishGatewaySuccess(ctx, turn.gatewayID)
			return
		}
		messages = make([]UplinkMessage, len(batch))
		for index, item := range batch {
			messages[index] = UplinkMessage{Topic: item.packet.Topic, Payload: item.packet.Payload}
		}
	}

	for len(batch) > 0 {
		*activeEndSeq = batch[len(batch)-1].endSeq
		currentAttempt := attempt + 1
		consumed, delay, err := runtime.processPrefix(ctx, turn.gatewayID, batch, messages, currentAttempt)
		if consumed > 0 && turn.mailbox != nil {
			turn.mailbox.markConsumed(batch[consumed-1].endSeq)
			memDepth, overflowDepth := turn.mailbox.depth()
			runtime.recordQueueDepth(turn.gatewayID, memDepth+overflowDepth)
			runtime.recordOverflowDepth(turn.gatewayID, overflowDepth)
		}
		if err != nil {
			if ctx.Err() != nil {
				runtime.finishGatewayCancelled(turn.gatewayID)
				return
			}
			runtime.scheduleGatewayRetry(ctx, turn.gatewayID, batch[consumed:], messages[consumed:], currentAttempt, delay)
			return
		}
		batch = batch[consumed:]
		messages = messages[consumed:]
		attempt = 0
	}
	*activeEndSeq = 0
	runtime.finishGatewaySuccess(ctx, turn.gatewayID)
}

func (runtime *Runtime) finishGatewaySuccess(ctx context.Context, gatewayID string) {
	runtime.queuesMu.Lock()
	state, ok := runtime.states[gatewayID]
	if !ok {
		runtime.queuesMu.Unlock()
		runtime.notifyDrain()
		return
	}
	state.inFlight = false
	state.lastActive = runtime.now()
	if runtime.hasGatewayWorkLocked(state) {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
		runtime.queuesMu.Unlock()
		return
	}
	runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
	runtime.queuesMu.Unlock()
	runtime.notifyDrain()
}

func (runtime *Runtime) finishGatewayCancelled(gatewayID string) {
	runtime.queuesMu.Lock()
	state, ok := runtime.states[gatewayID]
	if ok {
		state.inFlight = false
		state.retrying = false
		state.pendingBatch = nil
		state.pendingMsgs = nil
		state.attempt = 0
	}
	runtime.queuesMu.Unlock()
	runtime.notifyDrain()
}

func (runtime *Runtime) finishGatewayPanic(ctx context.Context, gatewayID string, mailbox *gatewayMailbox, endSeq int64) {
	runtime.recordProcessingFailure(errors.New("MQTT uplink worker panicked"))
	if mailbox != nil && endSeq > 0 {
		mailbox.markConsumed(endSeq)
		memDepth, overflowDepth := mailbox.depth()
		runtime.recordQueueDepth(gatewayID, memDepth+overflowDepth)
		runtime.recordOverflowDepth(gatewayID, overflowDepth)
	}
	runtime.queuesMu.Lock()
	state, ok := runtime.states[gatewayID]
	if !ok {
		runtime.queuesMu.Unlock()
		runtime.notifyDrain()
		return
	}
	state.inFlight = false
	state.retrying = false
	state.pendingBatch = nil
	state.pendingMsgs = nil
	state.attempt = 0
	state.lastActive = runtime.now()
	if ctx.Err() != nil {
		runtime.queuesMu.Unlock()
		runtime.notifyDrain()
		return
	}
	if runtime.hasGatewayWorkLocked(state) {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
		runtime.queuesMu.Unlock()
		return
	}
	runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
	runtime.queuesMu.Unlock()
	runtime.notifyDrain()
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
		runtime.notifyDrain()
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

func (runtime *Runtime) finishBatchWindow(ctx context.Context, gatewayID string, mailbox *gatewayMailbox, batch []queuedPublish, limit int) []queuedPublish {
	timer := time.NewTimer(runtime.config.BatchFlushWindow)
	defer timer.Stop()
	for len(batch) < limit {
		more, memDepth, overflowDepth, readErrors := mailbox.popBatch(limit - len(batch))
		runtime.recordQueueDepth(gatewayID, memDepth+overflowDepth)
		runtime.recordOverflowDepth(gatewayID, overflowDepth)
		if readErrors > 0 {
			_ = runtime.metrics.AddCounter("hvac_mqtt_spool_read_errors_total", "MQTT uplink messages dropped due to overflow spool corruption or read failures.", nil, float64(readErrors))
			runtime.logger.Warn("mqtt_uplink_overflow_read_failed", "gateway_id", gatewayID, "count", readErrors)
		}
		if len(more) > 0 {
			batch = append(batch, more...)
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return batch
		case <-mailbox.notify:
		}
	}
	return batch
}

// processPrefix runs one attempt over the contiguous prefix of pending messages;
// permanent/quarantined items are marked consumed by runGatewayTurn while transient failures
// return retry delay for non-blocking per-Gateway timer scheduling.
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
				_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "quarantined"}, 1)
				runtime.logger.Warn("mqtt_uplink_message_quarantined", "gateway_id", gatewayID, "topic", item.packet.Topic, "error", outcome.Err.Error())
				continue
			}
			result := outcome.Result
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

// ack acknowledges an uplink packet synchronously once it has durable custody in the Gateway WAL spool.
func (runtime *Runtime) ack(gatewayID string, packet *paho.Publish, ackFn func(*paho.Publish) error) {
	if ackFn == nil || packet == nil {
		return
	}
	if err := ackFn(packet); err != nil {
		_ = runtime.metrics.AddCounter("hvac_mqtt_ack_failures_total", "MQTT uplink acknowledgements that failed and leave the message to broker redelivery.", nil, 1)
		runtime.logger.Warn("mqtt_uplink_ack_failed", "gateway_id", gatewayID, "topic", packet.Topic, "error", err.Error())
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

func (runtime *Runtime) recordOverflowDepth(gatewayID string, depth int) {
	_ = runtime.metrics.SetGauge("hvac_mqtt_gateway_overflow_depth", "Messages spilled to disk overflow in a Gateway's uplink queue.", map[string]string{"gateway_id": gatewayID}, float64(depth))
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
