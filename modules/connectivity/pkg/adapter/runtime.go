package adapter

import (
	"context"
	"crypto/tls"
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
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	defaultCoverageReportInterval     = 60 * time.Second
	MaximumCoverageBatchSize          = 2048
	defaultGatewayIdleTimeout         = 5 * time.Minute
	defaultDrainTimeout               = 10 * time.Second
	maxTrackedGatewayQueues           = 4096
	defaultRateLimitRetryFloor        = 2 * time.Second
	maxConcurrentRateLimitQuarantines = 8
	subscribeAttemptTimeout           = mqttclient.DefaultSubscribeAttemptTimeout
)

var DefaultSubscribeRetryDelays = mqttclient.DefaultSubscribeRetryDelays

var sharedRouteCounter atomic.Uint64

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

type GatewayScopedCoverageLister interface {
	ListActiveManagedGatewayScopes(ctx context.Context) ([]GatewayCoverageScope, error)
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

func WithPacketHook(hook func(direction, channel, gatewayID, topic, traceID string, payload []byte)) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.packetHook = hook
	}
}

func WithOnlineGatewayCount(fn func() int) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.onlineGatewayCount = fn
	}
}

// WithSharedSession shares an externally owned mqttclient.SharedSession instead
// of dialing a standalone MQTT connection inside Runtime.Run.
func WithSharedSession(session *mqttclient.SharedSession) RuntimeOption {
	return func(runtime *Runtime) {
		runtime.sharedSession = session
	}
}

// Runtime consumes every Gateway's uplink. Each Gateway gets an ordered mailbox
// serviced by a bounded pool of workers. A message is acknowledged only after it is
// processed or quarantined; when a Gateway's queue is full, receiving pauses and the
// broker holds the backlog.
type Runtime struct {
	config         Config
	processor      *Processor
	logger         *slog.Logger
	metrics        *observability.Registry
	sharedSession  *mqttclient.SharedSession
	retryDelay     func(int) time.Duration
	rateLimitFloor time.Duration
	// reconnectBackoff is how long to wait before reconnection attempt N.
	reconnectBackoff     func(int) time.Duration
	subscribeRetryDelays []time.Duration
	coverageLister       CoverageDeviceLister
	coverageReporter     CoverageReporter
	coverageInterval     time.Duration
	idleTimeout          time.Duration
	drainTimeout         time.Duration
	packetHook           func(direction, channel, gatewayID, topic, traceID string, payload []byte)
	onlineGatewayCount   func() int
	now                  func() time.Time

	coverageMu     sync.Mutex
	coverageClosed bool
	subWG          sync.WaitGroup
	coverageWG     sync.WaitGroup
	coverageRev    atomic.Int64
	connTracker    *mqttclient.ConnTracker

	ingressCtxMu      sync.RWMutex
	ingressCtx        context.Context
	quarantineSemOnce sync.Once
	quarantineSem     chan struct{}
	quarantineWG      sync.WaitGroup

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

	mu          sync.RWMutex
	connected   bool
	subscribed  bool
	lastError   string
	lastSuccess time.Time
}

func NewRuntime(config Config, processor *Processor, logger *slog.Logger, metrics *observability.Registry, options ...RuntimeOption) (*Runtime, error) {
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
		rateLimitFloor:   defaultRateLimitRetryFloor,
		reconnectBackoff: autopaho.DefaultExponentialBackoff(),
		coverageInterval: defaultCoverageReportInterval,
		idleTimeout:      defaultGatewayIdleTimeout,
		drainTimeout:     defaultDrainTimeout,
		now:              time.Now,
		connTracker:      mqttclient.NewConnTracker(),
		quarantineSem:    make(chan struct{}, maxConcurrentRateLimitQuarantines),
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
	if err := config.validate(runtime.sharedSession == nil); err != nil {
		return nil, err
	}
	config.OverflowMaxMessages = config.effectiveOverflowMaxMessages()
	config.OverflowMaxBytes = config.effectiveOverflowMaxBytes()
	runtime.config = config
	if runtime.packetHook != nil && runtime.processor != nil {
		if runtime.processor.packetHook == nil {
			runtime.processor.packetHook = runtime.packetHook
		} else {
			prevHook := runtime.processor.packetHook
			nextHook := runtime.packetHook
			runtime.processor.packetHook = func(direction, channel, gatewayID, topic, traceID string, payload []byte) {
				prevHook(direction, channel, gatewayID, topic, traceID, payload)
				nextHook(direction, channel, gatewayID, topic, traceID, payload)
			}
		}
	}
	if runtime.processor != nil && runtime.processor.metrics == nil {
		runtime.processor.metrics = runtime.metrics
	}
	if runtime.processor != nil && !runtime.processor.autoBackfillLagSet && config.AutoBackfillLagThreshold > 0 {
		runtime.processor.autoBackfillLag = config.AutoBackfillLagThreshold
	}
	runtime.refreshQueueCapacityMetric()
	runtime.recordProcessingQueueDepth(0)
	runtime.refreshOnlineGateways()
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
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		brokerURL *url.URL
		tlsConfig *tls.Config
		err       error
	)
	if runtime.sharedSession == nil {
		brokerURL, err = url.Parse(strings.TrimSpace(runtime.config.MQTT.BrokerURL))
		if err != nil {
			return fmt.Errorf("parse MQTT broker URL: %w", err)
		}
		tlsConfig, err = mqttclient.NewMTLSConfig(mqttclient.TLSConfig{
			CACertFile:     runtime.config.MQTT.CAFile,
			ClientCertFile: runtime.config.MQTT.CertFile,
			ClientKeyFile:  runtime.config.MQTT.KeyFile,
			ServerName:     runtime.config.MQTT.ServerName,
		})
		if err != nil {
			return err
		}
	}
	if err := runtime.prepareOverflowDir(); err != nil {
		return err
	}
	runtime.refreshQueueCapacityMetric()
	runtime.refreshOnlineGateways()
	runtime.coverageMu.Lock()
	runtime.coverageClosed = false
	runtime.coverageMu.Unlock()
	runContext, runCancel := context.WithCancel(ctx)
	runtime.setIngressContext(runContext)
	heartbeatContext, heartbeatCancel := context.WithCancel(context.Background())
	workerContext, workerCancel := context.WithCancel(context.Background())
	runtime.startWorkers(workerContext)
	var heartbeatWG sync.WaitGroup
	var drained bool
	defer func() {
		runCancel()
		heartbeatCancel()
		if !drained {
			workerCancel()
		}
		heartbeatWG.Wait()
		runtime.subWG.Wait()
		runtime.coverageWG.Wait()
		runtime.workers.Wait()
		runtime.quarantineWG.Wait()
		runtime.closeMailboxes()
		runtime.clearIngressContext()
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

	if runtime.sharedSession != nil {
		return runtime.runWithSharedSession(ctx, runContext, runCancel, workerContext, workerCancel, heartbeatCancel, &heartbeatWG, &drained)
	}

	receiveMaximum := runtime.config.MQTT.EffectiveReceiveMaximum()
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     mqttKeepAliveSeconds,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         mqttSessionExpirySeconds,
		ConnectTimeout:                mqttConnectTimeout,
		ReconnectBackoff:              runtime.reconnectBackoff,
		ConnectPacketBuilder: func(cp *paho.Connect, _ *url.URL) (*paho.Connect, error) {
			if cp.Properties == nil {
				cp.Properties = &paho.ConnectProperties{}
			}
			rm := receiveMaximum
			cp.Properties.ReceiveMaximum = &rm
			return cp, nil
		},
		AttemptConnection: func(attemptContext context.Context, _ autopaho.ClientConfig, server *url.URL) (net.Conn, error) {
			return runtime.dial(attemptContext, tlsConfig, server)
		},
		OnConnectError: func(connectErr error) {
			runtime.recordConnectionState(nil, false, false, connectErr)
			_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
			runtime.logger.Warn("mqtt_uplink_connect_failed", "error_code", "MQTT_UPLINK_CONNECT_FAILED", "error", connectErr.Error())
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
		gen := runtime.connTracker.CurrentGen()
		connection := runtime.currentConnectionClosed()
		runtime.recordConnectionState(connection, true, false, nil)
		_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "success"}, 1)
		runtime.subWG.Add(1)
		go func() {
			defer runtime.subWG.Done()
			defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.mqtt_subscribe")
			runtime.subscribe(runContext, manager, connection, gen)
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
	runCancel()
	heartbeatCancel()
	if stopErr != nil {
		runtime.closeCurrentConnection()
		runtime.recordConnectionState(nil, false, false, nil)
		runtime.finishShutdown(false, workerCancel, &heartbeatWG, &drained)
		return stopErr
	}
	disconnectContext, disconnectCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = manager.Disconnect(disconnectContext)
	disconnectCancel()
	runtime.closeCurrentConnection()
	runtime.recordConnectionState(nil, false, false, nil)
	runtime.finishShutdown(true, workerCancel, &heartbeatWG, &drained)
	return nil
}

func (runtime *Runtime) runWithSharedSession(
	ingressCtx context.Context,
	runContext context.Context,
	runCancel context.CancelFunc,
	workerContext context.Context,
	workerCancel context.CancelFunc,
	heartbeatCancel context.CancelFunc,
	heartbeatWG *sync.WaitGroup,
	drained *bool,
) error {
	filters := runtime.config.SubscriptionTopicFilters()
	routeID := fmt.Sprintf("adapter-uplink-%d", sharedRouteCounter.Add(1))
	var routeActive atomic.Bool

	unregister, err := runtime.sharedSession.RegisterManagedRoute(routeID, filters, func(ev mqttclient.PublishEvent) error {
		if ev.Packet == nil || ev.Ack == nil {
			return errors.New("MQTT publish callback is incomplete")
		}
		return runtime.enqueue(workerContext, ev.ConnectionClosed, ev.Packet, ev.Ack)
	})
	if err != nil {
		runtime.recordConnectionState(nil, false, false, err)
		return err
	}
	routeActive.Store(true)

	var (
		obsMu          sync.Mutex
		prevConnected  bool
		prevSubscribed bool
	)
	removeObserver := runtime.sharedSession.AddObserver(func(state mqttclient.SessionState) {
		obsMu.Lock()
		defer obsMu.Unlock()
		if !routeActive.Load() {
			return
		}
		connected := state.Connected && !state.Closed
		subscribed := connected && state.Subscribed
		var stateErr error
		if state.LastError != "" {
			stateErr = errors.New(state.LastError)
		}

		if !connected && prevConnected && runContext.Err() == nil {
			runtime.connectionLost(runContext, 0)
			if stateErr != nil {
				runtime.recordConnectionState(nil, false, false, stateErr)
			}
		} else {
			runtime.recordConnectionState(nil, connected, subscribed, stateErr)
		}
		if !state.LastSuccess.IsZero() {
			runtime.mu.Lock()
			if state.LastSuccess.After(runtime.lastSuccess) {
				runtime.lastSuccess = state.LastSuccess
			}
			runtime.mu.Unlock()
		}
		if connected && !prevConnected {
			_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "success"}, 1)
		}
		if subscribed && !prevSubscribed && stateErr == nil {
			_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "success"}, 1)
			runtime.logger.Info("mqtt_uplink_subscribed", "topic_filters", filters)
			if runContext.Err() == nil && runtime.coverageLister != nil && runtime.coverageReporter != nil {
				runtime.coverageWG.Add(1)
				go func() {
					defer runtime.coverageWG.Done()
					defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.coverage_report")
					runtime.reportCoverage(runContext, true, "", 0, false)
				}()
			}
		}
		prevConnected = connected
		prevSubscribed = subscribed
	})

	cleanupRoute := func() {
		routeActive.Store(false)
		removeObserver()
		unsubCtx, unsubCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = unregister(unsubCtx)
		unsubCancel()
		runtime.recordConnectionState(nil, false, false, nil)
	}

	if err := runtime.sharedSession.AwaitConnection(ingressCtx); err != nil {
		cleanupRoute()
		runCancel()
		heartbeatCancel()
		runtime.finishShutdown(ingressCtx.Err() != nil, workerCancel, heartbeatWG, drained)
		return fmt.Errorf("await MQTT connection: %w", err)
	}

	var stopErr error
	select {
	case <-ingressCtx.Done():
	case <-runtime.sharedSession.Done():
		if ingressCtx.Err() == nil {
			stopErr = errors.New("MQTT shared session stopped")
		}
	}
	runCancel()
	heartbeatCancel()
	cleanupRoute()
	if stopErr != nil {
		runtime.finishShutdown(false, workerCancel, heartbeatWG, drained)
		return stopErr
	}
	runtime.finishShutdown(true, workerCancel, heartbeatWG, drained)
	return nil
}

func (runtime *Runtime) finishShutdown(drainFirst bool, workerCancel context.CancelFunc, heartbeatWG *sync.WaitGroup, drained *bool) {
	if drainFirst {
		runtime.subWG.Wait()
		drainContext, drainCancel := context.WithTimeout(context.Background(), runtime.effectiveDrainTimeout())
		runtime.waitDrainWithContext(drainContext)
		drainCancel()
	}
	workerCancel()
	heartbeatWG.Wait()
	if !drainFirst {
		runtime.subWG.Wait()
	}
	runtime.workers.Wait()
	runtime.quarantineWG.Wait()
	runtime.closeMailboxes()
	*drained = true
	runtime.coverageWG.Wait()
	shutdownCoverageContext, coverageCancel := context.WithTimeout(context.Background(), 3*time.Second)
	runtime.reportCoverage(shutdownCoverageContext, false, "SOURCE_UNAVAILABLE", 0, true)
	coverageCancel()
}

func (runtime *Runtime) subscribe(ctx context.Context, manager *autopaho.ConnectionManager, connection <-chan struct{}, gen uint64) {
	filters := runtime.config.SubscriptionTopicFilters()
	subscriptions := make([]paho.SubscribeOptions, 0, len(filters))
	for _, topic := range filters {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: topic, QoS: 1})
	}
	if err := mqttclient.SubscribeWithRetry(
		ctx,
		manager,
		&paho.Subscribe{Subscriptions: subscriptions},
		subscribeAttemptTimeout,
		runtime.effectiveSubscribeRetryDelays(),
		func() bool { return mqttclient.IsClosedSignal(connection) },
	); err != nil {
		if ctx.Err() != nil || mqttclient.IsClosedSignal(connection) {
			return
		}
		runtime.recordConnectionState(connection, true, false, err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
		runtime.logger.Warn("mqtt_uplink_subscribe_failed", "error_code", "MQTT_UPLINK_SUBSCRIBE_FAILED", "error", err.Error())
		if runtime.connTracker != nil {
			_ = runtime.connTracker.CloseIfGen(gen)
		}
		return
	}
	if ctx.Err() != nil || mqttclient.IsClosedSignal(connection) {
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

func (runtime *Runtime) effectiveSubscribeRetryDelays() []time.Duration {
	if runtime.subscribeRetryDelays != nil {
		return runtime.subscribeRetryDelays
	}
	return DefaultSubscribeRetryDelays
}

func (runtime *Runtime) effectiveRateLimitFloor() time.Duration {
	if runtime.rateLimitFloor > 0 {
		return runtime.rateLimitFloor
	}
	return defaultRateLimitRetryFloor
}

// dial opens the TLS connection to the broker. Closing it is the first step of paho
// tearing a connection down, before paho waits for the publish handler to return, so
// the close is what releases a handler waiting on a full Gateway queue.
func (runtime *Runtime) dial(ctx context.Context, tlsConfig *tls.Config, server *url.URL) (net.Conn, error) {
	conn, _, _, err := runtime.connTracker.DialTLS(ctx, tlsConfig, server.Host, mqttConnectTimeout, func(gen uint64) {
		runtime.connectionLost(ctx, gen)
	})
	return conn, err
}

func (runtime *Runtime) closeCurrentConnection() {
	if runtime.sharedSession != nil {
		_ = runtime.sharedSession.CloseCurrentConnection()
		return
	}
	if runtime.connTracker != nil {
		_ = runtime.connTracker.CloseCurrent()
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

func (runtime *Runtime) effectiveDrainTimeout() time.Duration {
	if runtime.drainTimeout > 0 {
		return runtime.drainTimeout
	}
	return defaultDrainTimeout
}

func (runtime *Runtime) waitDrainWithContext(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for runtime.hasPendingWork() {
		select {
		case <-runtime.drainSignal:
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// currentConnectionClosed is closed when the connection now delivering messages closes.
// autopaho dials the next connection only after the previous client has fully stopped,
// so every message the handler sees came from this connection.
func (runtime *Runtime) currentConnectionClosed() <-chan struct{} {
	if runtime.sharedSession != nil {
		return runtime.sharedSession.CurrentClosed()
	}
	if runtime.connTracker != nil {
		return runtime.connTracker.CurrentClosed()
	}
	return nil
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
	runtime.logger.Warn("mqtt_uplink_connection_lost", "reason_code", "TRANSPORT_DISCONNECTED")
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
			runtime.refreshOnlineGateways()
			if runtime.Ready() {
				expectedGen := uint64(0)
				if runtime.sharedSession == nil && runtime.connTracker != nil {
					expectedGen = runtime.connTracker.CurrentGen()
				}
				runtime.reportCoverage(ctx, true, "", expectedGen, false)
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
		if expectedGen != 0 && runtime.connTracker != nil && expectedGen != runtime.connTracker.CurrentGen() {
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
	deviceIDs, scopes, err := runtime.loadCoverageTargets(callCtx, available)
	if err != nil {
		if runtime.logger != nil {
			runtime.logger.Warn("mqtt_coverage_device_list_failed", "error_code", "MQTT_COVERAGE_DEVICE_LIST_FAILED", "error", err.Error())
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
		batchDeviceIDs := deviceIDs[start:end]
		var batchScopes []GatewayCoverageScope
		if len(scopes) > 0 {
			batchScopes = sliceGatewayScopesForBatch(scopes, batchDeviceIDs)
		}
		rev := runtime.coverageRev.Add(1)
		report := SourceCoverageReport{
			SourceID:       runtime.config.MQTT.ClientID,
			DeviceIDs:      batchDeviceIDs,
			GatewayScopes:  batchScopes,
			Available:      available,
			ReasonCode:     reasonPtr,
			ObservedAt:     observedAt,
			SourceRevision: rev,
		}
		if err := runtime.coverageReporter.ReportSourceCoverage(callCtx, report); err != nil && runtime.logger != nil {
			runtime.logger.Warn("mqtt_coverage_report_failed", "available", available, "error_code", "MQTT_COVERAGE_REPORT_FAILED", "error", err.Error())
		}
	}
}

func (runtime *Runtime) loadCoverageTargets(ctx context.Context, available bool) ([]string, []GatewayCoverageScope, error) {
	if available {
		if scopedLister, ok := runtime.coverageLister.(GatewayScopedCoverageLister); ok {
			rawScopes, err := scopedLister.ListActiveManagedGatewayScopes(ctx)
			if err != nil {
				return nil, nil, err
			}
			devices, scopes := NormalizeGatewayCoverageScopes(rawScopes)
			return devices, scopes, nil
		}
	}
	devices, err := runtime.coverageLister.ListAllActiveManagedDeviceIDs(ctx)
	return devices, nil, err
}

// BuildGatewayCoverageScopes builds sorted, deduplicated GatewayCoverageScopes from a map
// of gateway IDs to child device IDs, dropping empty entries.
func BuildGatewayCoverageScopes(devicesByGateway map[string][]string) []GatewayCoverageScope {
	rawScopes := make([]GatewayCoverageScope, 0, len(devicesByGateway))
	for gatewayID, deviceIDs := range devicesByGateway {
		rawScopes = append(rawScopes, GatewayCoverageScope{
			GatewayID: gatewayID,
			DeviceIDs: deviceIDs,
		})
	}
	_, scopes := NormalizeGatewayCoverageScopes(rawScopes)
	return scopes
}

// NormalizeGatewayCoverageScopes returns sorted unique device IDs alongside sorted,
// deduplicated per-gateway scopes with empty entries removed.
func NormalizeGatewayCoverageScopes(rawScopes []GatewayCoverageScope) ([]string, []GatewayCoverageScope) {
	byGateway := make(map[string][]string, len(rawScopes))
	for _, scope := range rawScopes {
		gatewayID := strings.TrimSpace(scope.GatewayID)
		if gatewayID == "" {
			continue
		}
		for _, deviceID := range scope.DeviceIDs {
			if trimmed := strings.TrimSpace(deviceID); trimmed != "" {
				byGateway[gatewayID] = append(byGateway[gatewayID], trimmed)
			}
		}
	}
	gatewayIDs := make([]string, 0, len(byGateway))
	for gatewayID := range byGateway {
		gatewayIDs = append(gatewayIDs, gatewayID)
	}
	slices.Sort(gatewayIDs)

	scopes := make([]GatewayCoverageScope, 0, len(gatewayIDs))
	allDevices := make([]string, 0)
	for _, gatewayID := range gatewayIDs {
		devices := byGateway[gatewayID]
		slices.Sort(devices)
		devices = slices.Compact(devices)
		if len(devices) == 0 {
			continue
		}
		scopes = append(scopes, GatewayCoverageScope{
			GatewayID: gatewayID,
			DeviceIDs: devices,
		})
		allDevices = append(allDevices, devices...)
	}
	slices.Sort(allDevices)
	allDevices = slices.Compact(allDevices)
	return allDevices, scopes
}

func sliceGatewayScopesForBatch(scopes []GatewayCoverageScope, batchDeviceIDs []string) []GatewayCoverageScope {
	inBatch := make(map[string]struct{}, len(batchDeviceIDs))
	for _, deviceID := range batchDeviceIDs {
		inBatch[deviceID] = struct{}{}
	}
	var batchScopes []GatewayCoverageScope
	for _, scope := range scopes {
		var matched []string
		for _, deviceID := range scope.DeviceIDs {
			if _, ok := inBatch[deviceID]; ok {
				matched = append(matched, deviceID)
			}
		}
		if len(matched) > 0 {
			batchScopes = append(batchScopes, GatewayCoverageScope{
				GatewayID: scope.GatewayID,
				DeviceIDs: matched,
			})
		}
	}
	return batchScopes
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

	gatewayID := TopicGatewayID(received.Topic)
	if runtime.processor != nil {
		if allowed, messageType, quarantineUplink := runtime.processor.CheckIngressRateLimit(received.Topic, received.Payload); !allowed {
			if runtime.metrics != nil {
				_ = runtime.metrics.AddCounter("hvac_mqtt_messages_received_total", "MQTT uplink messages received by ingress outcome.", map[string]string{"outcome": "dropped"}, 1)
			}
			if runtime.logger != nil {
				runtime.logger.Warn("mqtt_uplink_gateway_rate_limited", "gateway_id", gatewayID, "topic", received.Topic, "message_type", messageType, "reason_code", QuarantineReasonRateLimited)
			}
			if quarantineUplink != nil {
				runtime.recordRateLimitQuarantineAsync(ctx, *quarantineUplink)
			}
			// ACK and isolate the rate-limited burst so one gateway never disconnects healthy
			// neighbors or concentrator RPC traffic sharing the same MQTT session.
			runtime.ack(gatewayID, received, ack)
			return nil
		}
	}

	runtime.startWorkers(ctx)
	mailbox, state, err := runtime.acquireGatewayMailbox(gatewayID)
	if err != nil {
		if runtime.logger != nil {
			runtime.logger.Warn("mqtt_uplink_mailbox_open_failed", "gateway_id", gatewayID, "topic", received.Topic, "error_code", "MQTT_UPLINK_MAILBOX_OPEN_FAILED", "error", err.Error())
		}
		runtime.closeCurrentConnection()
		return err
	}
	result, enqueueErr := mailbox.enqueueAt(received, runtime.now().UTC())

	runtime.queuesMu.Lock()
	state.enqueuers--
	state.lastActive = runtime.now()
	if enqueueErr == nil && (!result.shed || result.memDepth+result.overflowDepth > 0) {
		runtime.scheduleGatewayLocked(ctx, gatewayID, state)
	} else {
		runtime.maybeScheduleIdleLocked(ctx, gatewayID, state)
	}
	maxDepth := runtime.maxQueueDepthLocked()
	runtime.queuesMu.Unlock()

	if enqueueErr != nil {
		if runtime.logger != nil {
			runtime.logger.Warn("mqtt_uplink_spool_write_failed", "gateway_id", gatewayID, "topic", received.Topic, "error_code", "MQTT_UPLINK_SPOOL_WRITE_FAILED", "error", enqueueErr.Error())
		}
		runtime.closeCurrentConnection()
		return enqueueErr
	}
	runtime.recordQueueDepth(gatewayID, result.memDepth+result.overflowDepth)
	runtime.recordOverflowDepth(gatewayID, result.overflowDepth)
	runtime.recordProcessingQueueDepth(maxDepth)
	if result.shed {
		_ = runtime.metrics.AddCounter("hvac_mqtt_messages_received_total", "MQTT uplink messages received by ingress outcome.", map[string]string{"outcome": "dropped"}, 1)
		_ = runtime.metrics.AddCounter("hvac_mqtt_gateway_overflow_shed_total", "MQTT uplink messages left unacknowledged because a Gateway filled RAM and disk overflow.", map[string]string{"gateway_id": gatewayID}, 1)
		runtime.logger.Warn("mqtt_uplink_gateway_overflow_shed", "gateway_id", gatewayID, "topic", received.Topic, "reason_code", "GATEWAY_OVERFLOW_SHED", "mem_depth", result.memDepth, "overflow_depth", result.overflowDepth)
		runtime.closeCurrentConnection()
		return errors.New("MQTT gateway mailbox overflow shed")
	}
	_ = runtime.metrics.AddCounter("hvac_mqtt_messages_received_total", "MQTT uplink messages received by ingress outcome.", map[string]string{"outcome": "queued"}, 1)
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
	if runtime.overflowDir == "" {
		runtime.queuesMu.Unlock()
		return nil
	}
	recovered, count, err := recoverSpoolMailboxes(
		runtime.overflowDir,
		runtime.config.QueueCapacity,
		runtime.config.effectiveOverflowMaxMessages(),
		runtime.config.effectiveOverflowMaxBytes(),
	)
	if err != nil {
		runtime.queuesMu.Unlock()
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
	maxDepth := runtime.maxQueueDepthLocked()
	runtime.queuesMu.Unlock()
	runtime.recordProcessingQueueDepth(maxDepth)
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

func mailboxHasUnconsumed(mb *gatewayMailbox) bool {
	if mb == nil {
		return false
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.memCount+mb.promoting+mb.overflowLenLocked() > 0 || mb.activeBytesLocked() > 0
}

func (runtime *Runtime) closeMailboxes() {
	runtime.queuesMu.Lock()
	removedIDs := make([]string, 0, len(runtime.queues))
	seenRemoved := make(map[string]struct{}, len(runtime.queues)+len(runtime.states))
	mailboxes := make([]*gatewayMailbox, 0, len(runtime.queues))
	hasPendingBatch := false
	for gatewayID, mailbox := range runtime.queues {
		seenRemoved[gatewayID] = struct{}{}
		removedIDs = append(removedIDs, gatewayID)
		mailboxes = append(mailboxes, mailbox)
		delete(runtime.queues, gatewayID)
	}
	for gatewayID, state := range runtime.states {
		if _, ok := seenRemoved[gatewayID]; !ok {
			seenRemoved[gatewayID] = struct{}{}
			removedIDs = append(removedIDs, gatewayID)
		}
		if len(state.pendingBatch) > 0 {
			hasPendingBatch = true
		}
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
	hasUnconsumed := hasPendingBatch
	for _, mailbox := range mailboxes {
		if mailboxHasUnconsumed(mailbox) {
			hasUnconsumed = true
			break
		}
	}
	removeFiles := runtime.ownedOverflowDir && !hasUnconsumed
	ownedDir := ""
	preservedOwnedDir := ""
	if removeFiles {
		ownedDir = runtime.overflowDir
		runtime.overflowDir = ""
		runtime.ownedOverflowDir = false
	} else if runtime.ownedOverflowDir && hasUnconsumed {
		preservedOwnedDir = runtime.overflowDir
		runtime.ownedOverflowDir = false
	}
	runtime.queuesMu.Unlock()

	for _, gatewayID := range removedIDs {
		runtime.deleteGatewayGauges(gatewayID)
	}
	runtime.recordProcessingQueueDepth(0)
	for _, mailbox := range mailboxes {
		if mailbox != nil {
			mailbox.close(removeFiles)
		}
	}
	if ownedDir != "" {
		_ = os.RemoveAll(ownedDir)
	} else if preservedOwnedDir != "" && runtime.logger != nil {
		runtime.logger.Warn("mqtt_uplink_overflow_preserved", "overflow_dir", preservedOwnedDir)
	}
}

func (runtime *Runtime) acquireGatewayMailbox(gatewayID string) (*gatewayMailbox, *gatewayMailboxState, error) {
	var evictedIDs []string
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
		evictedID, evictedMailbox := runtime.evictIdleGatewayLocked()
		if evictedMailbox == nil && len(runtime.queues) >= maxTrackedGatewayQueues {
			runtime.queuesMu.Unlock()
			return nil, nil, fmt.Errorf("tracked gateway queue limit (%d) reached", maxTrackedGatewayQueues)
		}
		if evictedMailbox != nil {
			evictedIDs = append(evictedIDs, evictedID)
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
	var maxDepth int
	if len(evictedIDs) > 0 {
		maxDepth = runtime.maxQueueDepthLocked()
	}
	runtime.queuesMu.Unlock()
	for _, evictedID := range evictedIDs {
		runtime.deleteGatewayGauges(evictedID)
	}
	if len(evictedIDs) > 0 {
		runtime.recordProcessingQueueDepth(maxDepth)
	}
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

func (runtime *Runtime) evictIdleGatewayLocked() (string, *gatewayMailbox) {
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
		return "", nil
	}
	state := runtime.states[oldestID]
	if state.idleTimer != nil {
		state.idleTimer.Stop()
		state.idleTimer = nil
	}
	state.idleGen++
	delete(runtime.states, oldestID)
	delete(runtime.queues, oldestID)
	return oldestID, state.mailbox
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
	maxDepth := runtime.maxQueueDepthLocked()
	runtime.queuesMu.Unlock()
	runtime.deleteGatewayGauges(gatewayID)
	runtime.recordProcessingQueueDepth(maxDepth)
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
		runtime.refreshProcessingQueueDepth()
		if readErrors > 0 {
			_ = runtime.metrics.AddCounter("hvac_mqtt_spool_read_errors_total", "MQTT uplink messages dropped due to overflow spool corruption or read failures.", nil, float64(readErrors))
			_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "dropped"}, float64(readErrors))
			runtime.logger.Warn("mqtt_uplink_overflow_read_failed", "gateway_id", turn.gatewayID, "error_code", "MQTT_UPLINK_OVERFLOW_READ_FAILED", "count", readErrors)
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
			receivedAt := item.receivedAt
			if receivedAt.IsZero() {
				receivedAt = runtime.now().UTC()
			}
			messages[index] = UplinkMessage{Topic: item.packet.Topic, Payload: item.packet.Payload, ReceivedAt: receivedAt}
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
			runtime.refreshProcessingQueueDepth()
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
		runtime.refreshProcessingQueueDepth()
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
		runtime.refreshProcessingQueueDepth()
		if readErrors > 0 {
			_ = runtime.metrics.AddCounter("hvac_mqtt_spool_read_errors_total", "MQTT uplink messages dropped due to overflow spool corruption or read failures.", nil, float64(readErrors))
			_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "dropped"}, float64(readErrors))
			runtime.logger.Warn("mqtt_uplink_overflow_read_failed", "gateway_id", gatewayID, "error_code", "MQTT_UPLINK_OVERFLOW_READ_FAILED", "count", readErrors)
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

func (runtime *Runtime) setIngressContext(ctx context.Context) {
	runtime.ingressCtxMu.Lock()
	runtime.ingressCtx = ctx
	runtime.ingressCtxMu.Unlock()
}

func (runtime *Runtime) clearIngressContext() {
	runtime.ingressCtxMu.Lock()
	runtime.ingressCtx = nil
	runtime.ingressCtxMu.Unlock()
}

func (runtime *Runtime) quarantineContext(fallback context.Context) context.Context {
	runtime.ingressCtxMu.RLock()
	ctx := runtime.ingressCtx
	runtime.ingressCtxMu.RUnlock()
	if ctx != nil {
		return ctx
	}
	if fallback != nil {
		return fallback
	}
	return context.Background()
}

func (runtime *Runtime) recordRateLimitQuarantineAsync(fallbackCtx context.Context, quarantine UplinkQuarantine) {
	baseCtx := runtime.quarantineContext(fallbackCtx)
	if baseCtx.Err() != nil {
		return
	}
	runtime.quarantineSemOnce.Do(func() {
		if runtime.quarantineSem == nil {
			runtime.quarantineSem = make(chan struct{}, maxConcurrentRateLimitQuarantines)
		}
	})
	select {
	case runtime.quarantineSem <- struct{}{}:
	default:
		return
	}
	runtime.quarantineWG.Add(1)
	go func() {
		defer runtime.quarantineWG.Done()
		defer func() { <-runtime.quarantineSem }()
		defer observability.Recover(runtime.logger, runtime.metrics, "connectivity.rate_limit_quarantine")
		qCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
		defer cancel()
		if err := runtime.processor.WriteRateLimitQuarantine(qCtx, quarantine); err != nil && baseCtx.Err() == nil && runtime.logger != nil {
			runtime.logger.Warn("mqtt_uplink_rate_limit_quarantine_failed", "gateway_id", quarantine.GatewayID, "topic", quarantine.Topic, "error_code", "MQTT_UPLINK_RATE_LIMIT_QUARANTINE_FAILED", "error", err.Error())
		}
	}()
}

// processPrefix runs one attempt over the contiguous prefix of pending messages;
// permanent/quarantined items are marked consumed by runGatewayTurn while transient failures
// return retry delay for non-blocking per-Gateway timer scheduling.
func (runtime *Runtime) processPrefix(ctx context.Context, gatewayID string, pending []queuedPublish, messages []UplinkMessage, attempt int) (int, time.Duration, error) {
	started := time.Now()
	processContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	outcomes, err := runtime.processor.ProcessPrefix(withIngressRateChecked(processContext), messages)
	cancel()
	if err == nil && len(outcomes) > 0 {
		runtime.refreshOnlineGateways()
		elapsed := time.Since(started)
		for index, outcome := range outcomes {
			item := pending[index]
			if isPermanentMessageError(outcome.Err) {
				_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "quarantined"}, 1)
				_ = runtime.metrics.ObserveHistogram("hvac_mqtt_message_processing_duration_seconds", "MQTT uplink message processing duration.", map[string]string{"outcome": "quarantined"}, elapsed.Seconds(), nil)
				runtime.logger.Warn("mqtt_uplink_message_quarantined", "gateway_id", gatewayID, "topic", item.packet.Topic, "error_code", "MQTT_UPLINK_MESSAGE_QUARANTINED", "error", outcome.Err.Error())
				continue
			}
			result := outcome.Result
			runtime.recordProcessingSuccess()
			runtime.recordProcessingResult(result, elapsed)
			runtime.logger.Info("mqtt_uplink_message_processed", "gateway_id", gatewayID, "message_id", result.MessageID, "message_type", result.MessageType,
				"trace_id", result.TraceID, "point_count", result.PointCount, "accepted", result.Accepted, "duplicate", result.Duplicate, "out_of_order", result.OutOfOrder,
				"quarantined", result.Quarantined, "rejected", result.Rejected)
		}
		return len(outcomes), 0, nil
	}
	if err == nil {
		err = errors.New("MQTT uplink processor produced no outcomes")
	}
	delay := runtime.retryDelay(attempt)
	reasonFamily := "dependency"
	logEvent := "mqtt_uplink_message_retrying"
	errorCode := "MQTT_UPLINK_MESSAGE_RETRYING"
	var rateLimited *RateLimitedError
	if errors.As(err, &rateLimited) {
		reasonFamily = "rate_limited"
		logEvent = "mqtt_uplink_message_rate_limited"
		errorCode = "MQTT_UPLINK_MESSAGE_RATE_LIMITED"
		delay = max(delay, rateLimited.RetryAfter, runtime.effectiveRateLimitFloor())
	} else {
		runtime.recordProcessingFailure(err)
	}
	_ = runtime.metrics.AddCounter("hvac_mqtt_message_retries_total", "MQTT uplink processing retries after transient failures.", map[string]string{"reason_family": reasonFamily}, 1)
	runtime.logger.Warn(logEvent, "gateway_id", gatewayID, "topic", pending[0].packet.Topic, "attempt", attempt, "retry_in", delay.String(), "reason_code", reasonFamily, "error_code", errorCode, "error", err.Error())
	return 0, delay, err
}

// ack acknowledges an uplink packet synchronously once it has durable custody in the Gateway WAL spool.
func (runtime *Runtime) ack(gatewayID string, packet *paho.Publish, ackFn func(*paho.Publish) error) {
	if ackFn == nil || packet == nil {
		return
	}
	if err := ackFn(packet); err != nil {
		_ = runtime.metrics.AddCounter("hvac_mqtt_ack_failures_total", "MQTT uplink acknowledgements that failed and leave the message to broker redelivery.", nil, 1)
		runtime.logger.Warn("mqtt_uplink_ack_failed", "gateway_id", gatewayID, "topic", packet.Topic, "error_code", "MQTT_UPLINK_ACK_FAILED", "error", err.Error())
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

func (runtime *Runtime) deleteGatewayGauges(gatewayID string) {
	if runtime == nil || runtime.metrics == nil || gatewayID == "" {
		return
	}
	labels := map[string]string{"gateway_id": gatewayID}
	runtime.metrics.DeleteGauge("hvac_mqtt_gateway_queue_depth", labels)
	runtime.metrics.DeleteGauge("hvac_mqtt_gateway_overflow_depth", labels)
}

func (runtime *Runtime) refreshQueueCapacityMetric() {
	if runtime == nil || runtime.metrics == nil {
		return
	}
	capacity := runtime.config.QueueCapacity + runtime.config.effectiveOverflowMaxMessages()
	_ = runtime.metrics.SetGauge("hvac_mqtt_processing_queue_capacity", "Configured per-gateway MQTT uplink queue and overflow capacity.", nil, float64(capacity))
}

func (runtime *Runtime) recordProcessingQueueDepth(depth int) {
	if runtime == nil || runtime.metrics == nil {
		return
	}
	_ = runtime.metrics.SetGauge("hvac_mqtt_processing_queue_depth", "Maximum MQTT uplink queue depth across active gateways.", nil, float64(depth))
}

func (runtime *Runtime) maxQueueDepthLocked() int {
	maxDepth := 0
	for _, mb := range runtime.queues {
		if mb == nil {
			continue
		}
		memDepth, overflowDepth := mb.depth()
		if total := memDepth + overflowDepth; total > maxDepth {
			maxDepth = total
		}
	}
	return maxDepth
}

func (runtime *Runtime) refreshProcessingQueueDepth() {
	if runtime == nil || runtime.metrics == nil {
		return
	}
	runtime.queuesMu.Lock()
	maxDepth := runtime.maxQueueDepthLocked()
	runtime.queuesMu.Unlock()
	runtime.recordProcessingQueueDepth(maxDepth)
}

func (runtime *Runtime) refreshOnlineGateways() {
	if runtime == nil || runtime.metrics == nil || runtime.onlineGatewayCount == nil {
		return
	}
	count := runtime.onlineGatewayCount()
	if count < 0 {
		count = 0
	}
	_ = runtime.metrics.SetGauge("hvac_mqtt_online_gateways", "Number of gateways currently tracked as online by Connectivity.", nil, float64(count))
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
	runtime.refreshOnlineGateways()
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
