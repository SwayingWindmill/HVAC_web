package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type queuedPublish struct {
	packet *paho.Publish
	ack    func(*paho.Publish) error
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

	queuesMu sync.Mutex
	queues   map[string]chan queuedPublish
	workers  sync.WaitGroup

	mu          sync.RWMutex
	connected   bool
	subscribed  bool
	lastError   string
	lastSuccess time.Time
}

func NewRuntime(config Config, processor *Processor, logger *slog.Logger, metrics *observability.Registry) (*Runtime, error) {
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
	return &Runtime{config: config, processor: processor, logger: logger, metrics: metrics, retryDelay: mqttRetryDelay, queues: map[string]chan queuedPublish{}}, nil
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
	defer func() {
		workerCancel()
		runtime.workers.Wait()
	}()

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     mqttKeepAliveSeconds,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         mqttSessionExpirySeconds,
		ConnectTimeout:                mqttConnectTimeout,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		OnConnectError: func(connectErr error) {
			runtime.recordConnectionState(false, false, connectErr)
			_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
			runtime.logger.Warn("mqtt_uplink_connect_failed", "error", connectErr.Error())
		},
		OnConnectionDown: func() bool {
			runtime.recordConnectionState(false, false, errors.New("MQTT connection lost"))
			_ = runtime.metrics.AddCounter("hvac_mqtt_disconnections_total", "MQTT connection-down events.", map[string]string{"reason_family": "transport"}, 1)
			return true
		},
		ClientConfig: paho.ClientConfig{
			ClientID:                   runtime.config.MQTT.ClientID,
			EnableManualAcknowledgment: true,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(received paho.PublishReceived) (bool, error) {
					if received.Packet == nil || received.Client == nil {
						return false, errors.New("MQTT publish callback is incomplete")
					}
					return true, runtime.enqueue(workerContext, received.Packet, received.Client.Ack)
				},
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		runtime.recordConnectionState(true, false, nil)
		_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "success"}, 1)
		go runtime.subscribe(ctx, manager)
	}
	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		return fmt.Errorf("create MQTT connection: %w", err)
	}
	if err := manager.AwaitConnection(ctx); err != nil {
		return fmt.Errorf("await MQTT connection: %w", err)
	}
	select {
	case <-ctx.Done():
	case <-manager.Done():
		if ctx.Err() == nil {
			return errors.New("MQTT connection manager stopped")
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = manager.Disconnect(shutdownContext)
	return nil
}

func (runtime *Runtime) subscribe(ctx context.Context, manager *autopaho.ConnectionManager) {
	subscribeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	subscriptions := make([]paho.SubscribeOptions, 0, len(UplinkTopicFilters))
	for _, topic := range UplinkTopicFilters {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: topic, QoS: 1})
	}
	if _, err := manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: subscriptions}); err != nil {
		runtime.recordConnectionState(true, false, err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
		runtime.logger.Warn("mqtt_uplink_subscribe_failed", "error", err.Error())
		return
	}
	runtime.recordConnectionState(true, true, nil)
	_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "success"}, 1)
	runtime.logger.Info("mqtt_uplink_subscribed", "topic_filters", UplinkTopicFilters)
}

// enqueue hands a message to its Gateway's queue and blocks while that queue is full,
// which stops reading from the broker instead of dropping the message.
func (runtime *Runtime) enqueue(ctx context.Context, received *paho.Publish, ack func(*paho.Publish) error) error {
	packet := *received
	packet.Payload = append([]byte(nil), received.Payload...)
	gatewayID := TopicGatewayID(packet.Topic)
	queue := runtime.queueFor(ctx, gatewayID)
	select {
	case queue <- queuedPublish{packet: &packet, ack: ack}:
		runtime.recordQueueDepth(gatewayID, len(queue))
		return nil
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
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-queue:
			runtime.recordQueueDepth(gatewayID, len(queue))
			if !runtime.processPublish(ctx, gatewayID, item) {
				return
			}
		}
	}
}

// processPublish retries a transient failure in place, keeping the Gateway's messages in
// order, until the message is processed, quarantined or Connectivity stops. A message
// left unacknowledged at shutdown is redelivered by the broker.
func (runtime *Runtime) processPublish(ctx context.Context, gatewayID string, item queuedPublish) bool {
	for attempt := 1; ; attempt++ {
		started := time.Now()
		processContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := runtime.processor.Process(processContext, item.packet.Topic, item.packet.Payload)
		cancel()
		switch {
		case err == nil:
			if ackErr := item.ack(item.packet); ackErr != nil {
				runtime.recordProcessingFailure(ackErr)
				runtime.logger.Warn("mqtt_uplink_ack_failed", "gateway_id", gatewayID, "message_id", result.MessageID, "error", ackErr.Error())
				return false
			}
			runtime.recordProcessingSuccess()
			runtime.recordProcessingResult(result, time.Since(started))
			runtime.logger.Info("mqtt_uplink_message_processed", "gateway_id", gatewayID, "message_id", result.MessageID, "message_type", result.MessageType,
				"point_count", result.PointCount, "accepted", result.Accepted, "duplicate", result.Duplicate, "out_of_order", result.OutOfOrder,
				"quarantined", result.Quarantined, "rejected", result.Rejected)
			return true
		case isPermanentMessageError(err):
			if ackErr := item.ack(item.packet); ackErr != nil {
				runtime.recordProcessingFailure(ackErr)
				return false
			}
			_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT uplink messages by processing outcome.", map[string]string{"outcome": "quarantined"}, 1)
			runtime.logger.Warn("mqtt_uplink_message_quarantined", "gateway_id", gatewayID, "topic", item.packet.Topic, "error", err.Error())
			return true
		}
		runtime.recordProcessingFailure(err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_message_retries_total", "MQTT uplink processing retries after transient failures.", map[string]string{"reason_family": "dependency"}, 1)
		delay := runtime.retryDelay(attempt)
		runtime.logger.Warn("mqtt_uplink_message_retrying", "gateway_id", gatewayID, "topic", item.packet.Topic, "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
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

func (runtime *Runtime) recordConnectionState(connected, subscribed bool, err error) {
	runtime.mu.Lock()
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
