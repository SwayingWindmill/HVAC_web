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
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type queuedPublish struct {
	packet *paho.Publish
	client *paho.Client
}

type Runtime struct {
	config     Config
	processor  *Processor
	logger     *slog.Logger
	metrics    *observability.Registry
	queueDepth atomic.Int64

	mu          sync.RWMutex
	connected   bool
	subscribed  bool
	lastError   string
	lastSuccess time.Time
}

func NewRuntime(config Config, processor *Processor, logger *slog.Logger, metrics *observability.Registry) (*Runtime, error) {
	if err := config.Validate(); err != nil || processor == nil {
		return nil, errors.New("MQTT telemetry runtime dependencies are invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	return &Runtime{config: config, processor: processor, logger: logger, metrics: metrics}, nil
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
	queue := make(chan queuedPublish, runtime.config.ProcessingQueueCapacity)
	_ = runtime.metrics.SetGauge("hvac_mqtt_processing_queue_capacity", "Configured MQTT processing queue capacity.", nil, float64(runtime.config.ProcessingQueueCapacity))
	_ = runtime.metrics.SetGauge("hvac_mqtt_processing_queue_depth", "Current MQTT processing queue depth.", nil, 0)
	workerContext, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		runtime.processQueue(workerContext, queue)
	}()

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     runtime.config.MQTT.KeepAliveSeconds,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         runtime.config.MQTT.SessionExpirySeconds,
		ConnectTimeout:                runtime.config.MQTT.ConnectTimeout(),
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		OnConnectError: func(connectErr error) {
			runtime.recordConnectionState(false, false, connectErr)
			_ = runtime.metrics.AddCounter("hvac_mqtt_connections_total", "MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
			runtime.logger.Warn("mqtt_telemetry_adapter_connect_failed", "error", connectErr.Error())
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
					packetCopy := *received.Packet
					packetCopy.Payload = append([]byte(nil), received.Packet.Payload...)
					select {
					case queue <- queuedPublish{packet: &packetCopy, client: received.Client}:
						depth := runtime.queueDepth.Add(1)
						_ = runtime.metrics.AddCounter("hvac_mqtt_messages_received_total", "MQTT telemetry messages received from the broker.", map[string]string{"outcome": "queued"}, 1)
						_ = runtime.metrics.SetGauge("hvac_mqtt_processing_queue_depth", "Current MQTT processing queue depth.", nil, float64(depth))
						return true, nil
					case <-ctx.Done():
						return false, ctx.Err()
					}
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
		workerCancel()
		<-workerDone
		return fmt.Errorf("create MQTT connection: %w", err)
	}
	if err := manager.AwaitConnection(ctx); err != nil {
		workerCancel()
		<-workerDone
		return fmt.Errorf("await MQTT connection: %w", err)
	}

	select {
	case <-ctx.Done():
	case <-manager.Done():
		if ctx.Err() == nil {
			workerCancel()
			<-workerDone
			return errors.New("MQTT connection manager stopped")
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = manager.Disconnect(shutdownContext)
	workerCancel()
	<-workerDone
	return nil
}

func (runtime *Runtime) subscribe(ctx context.Context, manager *autopaho.ConnectionManager) {
	subscribeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	subscriptions := make([]paho.SubscribeOptions, 0, len(runtime.config.MQTT.TopicFilters))
	for _, topic := range runtime.config.MQTT.TopicFilters {
		subscriptions = append(subscriptions, paho.SubscribeOptions{Topic: topic, QoS: 1})
	}
	_, err := manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: subscriptions})
	if err != nil {
		runtime.recordConnectionState(true, false, err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
		runtime.logger.Warn("mqtt_telemetry_adapter_subscribe_failed", "error", err.Error())
		return
	}
	runtime.recordConnectionState(true, true, nil)
	_ = runtime.metrics.AddCounter("hvac_mqtt_subscriptions_total", "MQTT subscription attempts by outcome.", map[string]string{"outcome": "success"}, 1)
	runtime.logger.Info("mqtt_telemetry_adapter_subscribed", "topic_filters", runtime.config.MQTT.TopicFilters)
}

func (runtime *Runtime) processQueue(ctx context.Context, queue <-chan queuedPublish) {
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-queue:
			if !ok {
				return
			}
			depth := runtime.queueDepth.Add(-1)
			_ = runtime.metrics.SetGauge("hvac_mqtt_processing_queue_depth", "Current MQTT processing queue depth.", nil, float64(depth))
			if !runtime.processPublish(ctx, item) {
				return
			}
		}
	}
}

func (runtime *Runtime) processPublish(ctx context.Context, item queuedPublish) bool {
	attempt := 0
	started := time.Now()
	for {
		processContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, err := runtime.processor.Process(processContext, item.packet.Topic, item.packet.Payload)
		cancel()
		if err == nil {
			if ackErr := item.client.Ack(item.packet); ackErr != nil {
				runtime.recordProcessingFailure(ackErr)
				_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT telemetry messages by processing outcome.", map[string]string{"outcome": "ack_failed"}, 1)
				_ = runtime.metrics.ObserveHistogram("hvac_mqtt_message_processing_duration_seconds", "MQTT telemetry message processing duration.", map[string]string{"outcome": "ack_failed"}, time.Since(started).Seconds(), nil)
				runtime.logger.Warn("mqtt_telemetry_ack_failed", "message_id", result.MessageID, "error", ackErr.Error())
				return true
			}
			runtime.recordProcessingSuccess()
			runtime.recordProcessingResult(result, time.Since(started))
			runtime.logger.Info(
				"mqtt_telemetry_message_processed",
				"message_id", result.MessageID,
				"replay", result.Replay,
				"point_count", result.PointCount,
				"accepted", result.Accepted,
				"duplicate", result.Duplicate,
				"out_of_order", result.OutOfOrder,
				"quarantined", result.Quarantined,
				"rejected", result.Rejected,
			)
			return true
		}
		if isPermanentMessageError(err) {
			if ackErr := item.client.Ack(item.packet); ackErr != nil {
				runtime.recordProcessingFailure(ackErr)
				_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT telemetry messages by processing outcome.", map[string]string{"outcome": "ack_failed"}, 1)
				runtime.logger.Warn("mqtt_telemetry_drop_ack_failed", "topic", item.packet.Topic, "error", ackErr.Error())
				return true
			}
			_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT telemetry messages by processing outcome.", map[string]string{"outcome": "dropped"}, 1)
			_ = runtime.metrics.ObserveHistogram("hvac_mqtt_message_processing_duration_seconds", "MQTT telemetry message processing duration.", map[string]string{"outcome": "dropped"}, time.Since(started).Seconds(), nil)
			runtime.logger.Warn("mqtt_telemetry_message_dropped", "topic", item.packet.Topic, "error", err.Error())
			return true
		}
		attempt++
		runtime.recordProcessingFailure(err)
		_ = runtime.metrics.AddCounter("hvac_mqtt_message_retries_total", "MQTT telemetry processing retries after transient downstream failures.", map[string]string{"reason_family": "dependency"}, 1)
		delay := mqttRetryDelay(attempt)
		runtime.logger.Warn("mqtt_telemetry_message_retrying", "topic", item.packet.Topic, "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
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
	_ = runtime.metrics.SetGauge("hvac_mqtt_connected", "Whether the MQTT adapter is connected to its broker.", nil, connectedValue)
	_ = runtime.metrics.SetGauge("hvac_mqtt_subscribed", "Whether the MQTT adapter has its telemetry subscription active.", nil, subscribedValue)
}

func (runtime *Runtime) recordProcessingResult(result ProcessingResult, elapsed time.Duration) {
	_ = runtime.metrics.AddCounter("hvac_mqtt_messages_processed_total", "MQTT telemetry messages by processing outcome.", map[string]string{"outcome": "success"}, 1)
	_ = runtime.metrics.ObserveHistogram("hvac_mqtt_message_processing_duration_seconds", "MQTT telemetry message processing duration.", map[string]string{"outcome": "success"}, elapsed.Seconds(), nil)
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
			_ = runtime.metrics.AddCounter("hvac_mqtt_values_total", "Point values delivered through the MQTT telemetry adapter by S2 outcome.", map[string]string{"outcome": outcome}, float64(count))
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
