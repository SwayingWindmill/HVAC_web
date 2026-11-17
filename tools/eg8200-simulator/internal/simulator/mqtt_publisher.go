package simulator

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	mqttTelemetryEnvelopeSchemaVersion = "2.0"
	defaultMQTTFullSnapshotInterval    = 300 * time.Second
)

type mqttTelemetryEnvelope struct {
	SchemaVersion string               `json:"schemaVersion"`
	MessageID     string               `json:"messageId"`
	GatewayID     string               `json:"gatewayId"`
	Timestamp     int64                `json:"timestamp"`
	Sequence      uint64               `json:"sequence"`
	TraceID       string               `json:"traceId,omitempty"`
	Replay        bool                 `json:"replay"`
	Payload       mqttTelemetryPayload `json:"payload"`
}

type mqttTelemetryPayload struct {
	Devices []mqttEnvelopeDevice `json:"devices"`
}

type mqttEnvelopeDevice struct {
	DeviceID        string              `json:"deviceId"`
	DeviceTimestamp int64               `json:"deviceTimestamp"`
	Points          []mqttEnvelopePoint `json:"points"`
}

type mqttEnvelopePoint struct {
	Code    string  `json:"code"`
	Value   any     `json:"value"`
	Quality uint8   `json:"quality"`
	Unit    *string `json:"unit,omitempty"`
}

type mqttEventEnvelope struct {
	SchemaVersion string           `json:"schemaVersion"`
	MessageID     string           `json:"messageId"`
	GatewayID     string           `json:"gatewayId"`
	Timestamp     int64            `json:"timestamp"`
	Sequence      uint64           `json:"sequence"`
	Payload       mqttEventPayload `json:"payload"`
}

type mqttEventPayload struct {
	EventType  string          `json:"eventType"`
	SourceType string          `json:"sourceType"`
	SourceID   string          `json:"sourceId"`
	EventTime  int64           `json:"eventTime"`
	Severity   string          `json:"severity"`
	Data       json.RawMessage `json:"data"`
}

type mqttSessionState struct {
	mu          sync.Mutex
	sessionID   string
	sessionTime time.Time
}

func (s *mqttSessionState) next(now time.Time) (string, time.Time, error) {
	t := now.UTC()
	id, err := newUUIDV7(t)
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	s.sessionID = id
	s.sessionTime = t
	s.mu.Unlock()
	return id, t, nil
}

func (s *mqttSessionState) current() (string, time.Time) {
	if s == nil {
		return "", time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID, s.sessionTime
}

type lastEmittedPointState struct {
	value   any
	quality uint8
}

type MQTTPublisher struct {
	gatewayID               string
	config                  MQTTGatewayConfig
	pointMu                 sync.RWMutex
	pointByKey              map[string]PointConfig
	shadowPointKeysByDevice map[string]map[string]struct{}
	edgeRuntime             *EdgeControlRuntime
	manager                 *autopaho.ConnectionManager
	managerPtr              *atomic.Pointer[autopaho.ConnectionManager]
	qosPub                  mqttQoSPublisher
	commandHandler          *edgeCommandHandler
	evidenceSpool           *mqttEvidenceSpool
	resendQueue             *TimedataResendQueue
	pendingObsMu            sync.Mutex
	pendingObsByMsgID       map[string][]PublishedObservation
	commandTopic            string
	metrics                 *observability.Registry
	connected               *atomic.Bool
	eventSequence           *atomic.Uint64
	session                 *mqttSessionState
	configShadow            *edgeConfigShadow
	snapshotMu              sync.Mutex
	snapshotInterval        time.Duration
	lastFullSnapshotAt      time.Time
	lastPointStates         map[string]lastEmittedPointState
}

func NewMQTTPublisher(ctx context.Context, plantConfig Config, config MQTTGatewayConfig, edgeRuntime *EdgeControlRuntime, metrics *observability.Registry) (*MQTTPublisher, error) {
	if err := plantConfig.Validate(); err != nil {
		return nil, fmt.Errorf("plant config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.QueueDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create MQTT queue directory: %w", err)
	}
	evidenceSpool, err := newMQTTEvidenceSpool(filepath.Join(config.QueueDirectory, "outbound"), config.MaximumQueueBytes)
	if err != nil {
		return nil, fmt.Errorf("open MQTT evidence spool: %w", err)
	}
	brokerURL, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}
	tlsConfig, err := mqttPublisherTLSConfig(config)
	if err != nil {
		return nil, err
	}
	commandHandler, err := newEdgeCommandHandler(edgeRuntime, config, plantConfig.Plant.DeviceIDs(), evidenceSpool)
	if err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	connected := &atomic.Bool{}
	eventSequence := &atomic.Uint64{}
	session := &mqttSessionState{}
	configShadow := newEdgeConfigShadow(filepath.Join(config.QueueDirectory, "config-shadow.v1.json"))
	if err := configShadow.load(); err != nil {
		return nil, err
	}
	pointByKey := make(map[string]PointConfig, len(plantConfig.Points))
	for _, point := range plantConfig.Points {
		pointByKey[pointReference(point.DeviceID, point.TelemetryKey)] = point
	}
	managerPtr := &atomic.Pointer[autopaho.ConnectionManager]{}
	publisher := &MQTTPublisher{
		gatewayID:               config.GatewayID,
		config:                  config,
		pointByKey:              pointByKey,
		shadowPointKeysByDevice: map[string]map[string]struct{}{},
		edgeRuntime:             edgeRuntime,
		managerPtr:              managerPtr,
		commandHandler:          commandHandler,
		evidenceSpool:           evidenceSpool,
		pendingObsByMsgID:       map[string][]PublishedObservation{},
		commandTopic:            mqttCommandTopic(config),
		metrics:                 metrics,
		connected:               connected,
		eventSequence:           eventSequence,
		session:                 session,
		configShadow:            configShadow,
		snapshotInterval:        defaultMQTTFullSnapshotInterval,
		lastPointStates:         map[string]lastEmittedPointState{},
	}
	configShadow.applyDriverHook = func(_ context.Context, cfg GatewayModbusDriverConfig, profile edgecontrol.DeclarativeModbusProfile) error {
		if edgeRuntime != nil {
			if err := edgeRuntime.ApplyDriverProfile(profile); err != nil {
				return err
			}
		}
		publisher.registerShadowDriverPoints(cfg)
		return nil
	}
	configShadow.removeDriverHook = func(_ context.Context, deviceKey string) error {
		if edgeRuntime != nil {
			if err := edgeRuntime.RemoveDriverProfile(deviceKey); err != nil {
				return err
			}
		}
		publisher.unregisterShadowDriverPoints(deviceKey)
		return nil
	}
	configShadow.applyAutonomyHook = func(_ context.Context, policy *GatewayAutonomyPolicy) error {
		effective := edgecontrol.DefaultAutonomyPolicyConfig()
		if policy != nil {
			effective = *policy
		}
		if edgeRuntime != nil {
			if err := edgeRuntime.ApplyAutonomyPolicy(effective); err != nil {
				return err
			}
		}
		publisher.SetSnapshotInterval(effective.SnapshotInterval())
		return nil
	}
	if err := configShadow.reapplyPersisted(ctx); err != nil {
		return nil, fmt.Errorf("reapply persisted edge config shadow: %w", err)
	}

	if edgeRuntime != nil && edgeRuntime.Timedata() != nil {
		resendQueue, resendErr := NewTimedataResendQueue(TimedataResendConfig{
			GatewayID:  config.GatewayID,
			Timedata:   edgeRuntime.Timedata(),
			CursorPath: filepath.Join(config.QueueDirectory, "resend-cursor.json"),
			Metrics:    metrics,
			PointMetadata: func(address string) TimedataResendPointMeta {
				return publisher.timedataPointMetadata(address)
			},
			Publish: func(pubCtx context.Context, topic string, payload []byte) error {
				mgr := managerPtr.Load()
				if mgr == nil {
					return errors.New("MQTT connection manager is unavailable")
				}
				_, pubErr := mgr.Publish(pubCtx, &paho.Publish{
					Topic:   topic,
					QoS:     1,
					Payload: payload,
				})
				return pubErr
			},
		})
		if resendErr != nil {
			return nil, resendErr
		}
		publisher.resendQueue = resendQueue
	}

	_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 0)
	_ = metrics.SetGauge("hvac_edge_mqtt_queue_capacity_bytes", "Configured persistent MQTT queue capacity in bytes.", nil, float64(config.MaximumQueueBytes))
	initialSessionID, initialSessionTime, err := session.next(time.Now().UTC())
	if err != nil {
		return nil, err
	}
	initialWillPayload, err := buildSessionWillPayload(config.GatewayID, initialSessionID, eventSequence.Add(1), initialSessionTime)
	if err != nil {
		return nil, err
	}
	willMessage := &paho.WillMessage{
		Retain:  false,
		QoS:     1,
		Topic:   mqttEventTopic(config.GatewayID),
		Payload: initialWillPayload,
	}
	manager, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         24 * 60 * 60,
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		WillMessage:                   willMessage,
		ConnectPacketBuilder: func(connect *paho.Connect, _ *url.URL) (*paho.Connect, error) {
			sessionID, sessionTime, err := session.next(time.Now().UTC())
			if err != nil {
				return nil, err
			}
			freshWillPayload, err := buildSessionWillPayload(config.GatewayID, sessionID, eventSequence.Add(1), sessionTime)
			if err != nil {
				return nil, err
			}
			connect.WillMessage = &paho.WillMessage{
				Retain:  false,
				QoS:     1,
				Topic:   mqttEventTopic(config.GatewayID),
				Payload: freshWillPayload,
			}
			return connect, nil
		},
		OnConnectError: func(error) {
			connected.Store(false)
			if publisher.resendQueue != nil {
				publisher.resendQueue.SetOnlineSynced(false)
			}
			if edgeRuntime != nil {
				edgeRuntime.NotifyCloudContact(false, time.Now().UTC())
			}
			_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 0)
			_ = metrics.AddCounter("hvac_edge_mqtt_connections_total", "Edge MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
		},
		OnConnectionDown: func() bool {
			connected.Store(false)
			if publisher.resendQueue != nil {
				publisher.resendQueue.SetOnlineSynced(false)
			}
			if edgeRuntime != nil {
				edgeRuntime.NotifyCloudContact(false, time.Now().UTC())
			}
			_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 0)
			_ = metrics.AddCounter("hvac_edge_mqtt_disconnections_total", "Edge MQTT disconnections.", map[string]string{"reason_family": "transport"}, 1)
			return true
		},
		OnConnectionUp: func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
			managerPtr.Store(manager)
			sessionID, sessionTime := session.current()
			connected.Store(true)
			if edgeRuntime != nil {
				edgeRuntime.NotifyCloudContact(true, sessionTime)
			}
			_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 1)
			_ = metrics.AddCounter("hvac_edge_mqtt_connections_total", "Edge MQTT connection attempts by outcome.", map[string]string{"outcome": "success"}, 1)
			commandTopic := mqttCommandTopic(config)
			go func() {
				subscribeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				if _, err := manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{
					{Topic: commandTopic, QoS: 1},
				}}); err != nil {
					return
				}
				if connectPayload, err := buildSessionConnectedPayload(config.GatewayID, sessionID, eventSequence.Add(1), sessionTime); err == nil {
					eventContext, eventCancel := context.WithTimeout(ctx, 5*time.Second)
					_, _ = manager.Publish(eventContext, &paho.Publish{
						Topic:   mqttEventTopic(config.GatewayID),
						QoS:     1,
						Payload: connectPayload,
					})
					eventCancel()
				}
				flushContext, flushCancel := context.WithTimeout(ctx, 30*time.Second)
				defer flushCancel()
				publisher.flushAndSyncResend(flushContext)
				if strings.TrimSpace(config.EnrollmentURL) != "" {
					shadowContext, shadowCancel := context.WithTimeout(ctx, 15*time.Second)
					defer shadowCancel()
					_ = syncConfigShadowHTTP(shadowContext, config, configShadow, nil)
				}
			}()
		},
		ClientConfig: paho.ClientConfig{
			ClientID: config.GatewayID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(recv paho.PublishReceived) (bool, error) {
				if edgeRuntime != nil {
					edgeRuntime.NotifyCloudContact(true, time.Now().UTC())
				}
				return commandHandler.Handle(recv)
			}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create MQTT publisher connection: %w", err)
	}
	managerPtr.Store(manager)
	publisher.manager = manager
	return publisher, nil
}

func (publisher *MQTTPublisher) currentManager() *autopaho.ConnectionManager {
	if publisher == nil {
		return nil
	}
	if publisher.managerPtr != nil {
		if mgr := publisher.managerPtr.Load(); mgr != nil {
			return mgr
		}
	}
	return publisher.manager
}

func (publisher *MQTTPublisher) currentQoSPublisher() mqttQoSPublisher {
	if publisher == nil {
		return nil
	}
	if publisher.qosPub != nil {
		return publisher.qosPub
	}
	if mgr := publisher.currentManager(); mgr != nil {
		return mgr
	}
	return nil
}

// IsResendSynced reports whether the offline Timedata resend queue is caught up.
func (publisher *MQTTPublisher) IsResendSynced() bool {
	if publisher == nil || publisher.resendQueue == nil {
		return true
	}
	return publisher.resendQueue.IsSynced()
}

func (publisher *MQTTPublisher) AwaitConnection(ctx context.Context) error {
	mgr := publisher.currentManager()
	if mgr == nil {
		return errors.New("MQTT publisher is unavailable")
	}
	return mgr.AwaitConnection(ctx)
}

func (publisher *MQTTPublisher) Ready() bool {
	return publisher != nil && publisher.connected != nil && publisher.connected.Load()
}

func (publisher *MQTTPublisher) RefreshMetrics() error {
	if publisher == nil || publisher.metrics == nil {
		return nil
	}
	queueBytes := publisher.evidenceSpool.UsedBytes()
	_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_bytes", "Bytes currently retained in the Edge persistent MQTT queue.", nil, float64(queueBytes))
	utilization := 0.0
	if publisher.config.MaximumQueueBytes > 0 {
		utilization = float64(queueBytes) / float64(publisher.config.MaximumQueueBytes)
	}
	_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_utilization_ratio", "Persistent MQTT queue utilization ratio.", nil, utilization)
	return nil
}

func (publisher *MQTTPublisher) PublishMeasurements(ctx context.Context, measurements []Measurement) (err error) {
	if publisher == nil || publisher.currentQoSPublisher() == nil {
		return errors.New("MQTT publisher is unavailable")
	}
	allMeasurements := publisher.withShadowMeasurements(measurements)
	if len(allMeasurements) == 0 {
		if publisher.connected != nil && publisher.connected.Load() && publisher.resendQueue != nil && !publisher.resendQueue.IsSynced() {
			flushContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			publisher.flushAndSyncResend(flushContext)
		}
		return nil
	}
	started := time.Now()
	outcome := "failed"
	defer func() {
		if publisher.metrics == nil {
			return
		}
		_ = publisher.metrics.AddCounter("hvac_edge_mqtt_publishes_total", "Edge MQTT publish attempts by outcome.", map[string]string{"outcome": outcome}, 1)
		_ = publisher.metrics.ObserveHistogram("hvac_edge_mqtt_publish_duration_seconds", "Edge MQTT publish-to-queue duration.", map[string]string{"outcome": outcome}, time.Since(started).Seconds(), nil)
		if outcome == "queued" {
			_ = publisher.metrics.AddCounter("hvac_edge_mqtt_values_total", "Point values queued for MQTT delivery.", map[string]string{"outcome": "queued"}, float64(len(allMeasurements)))
		}
	}()
	envelope, emittedObs, err := publisher.buildEnvelopeWithObservations(allMeasurements)
	if err != nil {
		return err
	}
	if len(envelope.Payload.Devices) == 0 {
		outcome = "throttled"
		if publisher.connected != nil && publisher.connected.Load() && publisher.resendQueue != nil && !publisher.resendQueue.IsSynced() {
			flushContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			publisher.flushAndSyncResend(flushContext)
		}
		return nil
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode MQTT telemetry envelope: %w", err)
	}
	topic := "hvac/v1/" + publisher.gatewayID + "/up/telemetry"
	publisher.recordPendingObservations(envelope.MessageID, emittedObs)
	admission, err := publisher.evidenceSpool.Enqueue(envelope.MessageID, EvidenceTelemetryNormal, topic, payload)
	if err != nil {
		publisher.takePendingObservations([]string{envelope.MessageID})
		if publisher.metrics != nil && errors.Is(err, ErrOfflineCapacity) {
			_ = publisher.metrics.AddCounter("hvac_edge_mqtt_queue_limit_rejections_total", "Edge MQTT publishes rejected because the persistent queue reached its configured byte limit.", nil, 1)
		}
		return err
	}
	if len(admission.ShedIDs) > 0 {
		publisher.takePendingObservations(admission.ShedIDs)
	}
	queueBytes := publisher.evidenceSpool.UsedBytes()
	if publisher.metrics != nil {
		_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_bytes", "Bytes currently retained in the Edge persistent MQTT queue.", nil, float64(queueBytes))
		_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_utilization_ratio", "Persistent MQTT queue utilization ratio.", nil, float64(queueBytes)/float64(publisher.config.MaximumQueueBytes))
		if len(admission.ShedIDs) > 0 {
			_ = publisher.metrics.AddCounter("hvac_edge_mqtt_queue_shed_total", "Lower-priority Edge MQTT evidence shed under offline capacity pressure.", map[string]string{"class": string(EvidenceTelemetryNormal)}, float64(len(admission.ShedIDs)))
		}
	}
	outcome = "queued"
	if publisher.connected != nil && publisher.connected.Load() {
		flushContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		publisher.flushAndSyncResend(flushContext)
	}
	return nil
}

func (publisher *MQTTPublisher) flushAndSyncResend(ctx context.Context) {
	if publisher == nil || publisher.connected == nil || !publisher.connected.Load() {
		return
	}
	if publisher.evidenceSpool != nil {
		if qosPub := publisher.currentQoSPublisher(); qosPub != nil {
			flushedIDs, flushErr := publisher.evidenceSpool.FlushIDs(ctx, qosPub)
			obs := publisher.takePendingObservations(flushedIDs)
			if len(obs) > 0 && publisher.resendQueue != nil && publisher.connected.Load() {
				_ = publisher.resendQueue.MarkPublishedObservations(obs)
			}
			if flushErr != nil {
				return
			}
		}
	}
	if publisher.resendQueue != nil && publisher.connected.Load() && !publisher.resendQueue.IsSynced() {
		_, _ = publisher.resendQueue.Drain(ctx)
	}
}

func (publisher *MQTTPublisher) recordPendingObservations(messageID string, obs []PublishedObservation) {
	if publisher == nil || strings.TrimSpace(messageID) == "" || len(obs) == 0 {
		return
	}
	publisher.pendingObsMu.Lock()
	if publisher.pendingObsByMsgID == nil {
		publisher.pendingObsByMsgID = make(map[string][]PublishedObservation)
	}
	publisher.pendingObsByMsgID[messageID] = append([]PublishedObservation(nil), obs...)
	publisher.pendingObsMu.Unlock()
}

func (publisher *MQTTPublisher) takePendingObservations(messageIDs []string) []PublishedObservation {
	if publisher == nil || len(messageIDs) == 0 {
		return nil
	}
	publisher.pendingObsMu.Lock()
	defer publisher.pendingObsMu.Unlock()
	if len(publisher.pendingObsByMsgID) == 0 {
		return nil
	}
	var out []PublishedObservation
	for _, id := range messageIDs {
		if obs, ok := publisher.pendingObsByMsgID[id]; ok {
			out = append(out, obs...)
			delete(publisher.pendingObsByMsgID, id)
		}
	}
	return out
}

func (publisher *MQTTPublisher) withShadowMeasurements(measurements []Measurement) []Measurement {
	var shadow []Measurement
	if publisher != nil && publisher.edgeRuntime != nil {
		shadow = publisher.edgeRuntime.LatestShadowMeasurements()
	}
	if len(shadow) == 0 {
		return measurements
	}
	seen := make(map[string]struct{}, len(measurements))
	for _, m := range measurements {
		seen[pointReference(m.DeviceID, m.TelemetryKey)] = struct{}{}
	}
	combined := make([]Measurement, len(measurements), len(measurements)+len(shadow))
	copy(combined, measurements)
	for _, m := range shadow {
		key := pointReference(m.DeviceID, m.TelemetryKey)
		if _, dup := seen[key]; dup {
			continue
		}
		if _, known := publisher.lookupPoint(m.DeviceID, m.TelemetryKey); !known {
			continue
		}
		seen[key] = struct{}{}
		combined = append(combined, m)
	}
	return combined
}

func (publisher *MQTTPublisher) Disconnect(ctx context.Context) error {
	mgr := publisher.currentManager()
	if mgr == nil {
		return nil
	}
	if publisher.connected != nil && publisher.connected.Load() && publisher.eventSequence != nil {
		sessionID, _ := publisher.session.current()
		fields := map[string]any{"clean": true}
		if sessionID != "" {
			fields["sessionId"] = sessionID
		}
		if rawData, marshalErr := json.Marshal(fields); marshalErr == nil {
			if disconnectPayload, err := buildLifecycleEventPayload(publisher.gatewayID, "GATEWAY_DISCONNECTED", publisher.eventSequence.Add(1), time.Now().UTC(), rawData); err == nil {
				pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				_, _ = mgr.Publish(pubCtx, &paho.Publish{
					Topic:   mqttEventTopic(publisher.gatewayID),
					QoS:     1,
					Payload: disconnectPayload,
				})
				cancel()
			}
		}
	}
	return mgr.Disconnect(ctx)
}

func buildSessionWillPayload(gatewayID, sessionID string, sequence uint64, sessionTime time.Time) ([]byte, error) {
	fields := map[string]any{"reason": "broker_connection_lost"}
	if strings.TrimSpace(sessionID) != "" {
		fields["sessionId"] = strings.TrimSpace(sessionID)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return buildLifecycleEventPayload(gatewayID, "LWT", sequence, sessionTime, raw)
}

func buildSessionConnectedPayload(gatewayID, sessionID string, sequence uint64, sessionTime time.Time) ([]byte, error) {
	if sessionTime.IsZero() {
		sessionTime = time.Now().UTC()
	}
	fields := map[string]any{}
	if strings.TrimSpace(sessionID) != "" {
		fields["sessionId"] = strings.TrimSpace(sessionID)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return buildLifecycleEventPayload(gatewayID, "GATEWAY_CONNECTED", sequence, sessionTime, raw)
}

func buildLifecycleEventPayload(gatewayID, eventType string, sequence uint64, observedAt time.Time, data json.RawMessage) ([]byte, error) {
	envelope, err := buildLifecycleEventEnvelope(gatewayID, eventType, sequence, observedAt, data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

func buildLifecycleEventEnvelope(gatewayID, eventType string, sequence uint64, observedAt time.Time, data json.RawMessage) (mqttEventEnvelope, error) {
	now := observedAt.UTC()
	messageID, err := newUUIDV7(now)
	if err != nil {
		return mqttEventEnvelope{}, err
	}
	severity := "INFO"
	if strings.EqualFold(strings.TrimSpace(eventType), "LWT") {
		severity = "WARNING"
	}
	if !json.Valid(data) {
		data = json.RawMessage(`{}`)
	}
	return mqttEventEnvelope{
		SchemaVersion: mqttTelemetryEnvelopeSchemaVersion,
		MessageID:     messageID,
		GatewayID:     gatewayID,
		Timestamp:     now.UnixMilli(),
		Sequence:      sequence,
		Payload: mqttEventPayload{
			EventType:  strings.TrimSpace(eventType),
			SourceType: "GATEWAY",
			SourceID:   gatewayID,
			EventTime:  now.UnixMilli(),
			Severity:   severity,
			Data:       append(json.RawMessage(nil), data...),
		},
	}, nil
}

func mqttEventTopic(gatewayID string) string {
	return "hvac/v1/" + gatewayID + "/up/event"
}

// SetSnapshotInterval updates the forced full-snapshot emission interval.
func (publisher *MQTTPublisher) SetSnapshotInterval(interval time.Duration) {
	if publisher == nil || interval <= 0 {
		return
	}
	publisher.snapshotMu.Lock()
	publisher.snapshotInterval = interval
	publisher.snapshotMu.Unlock()
}

// SnapshotInterval returns the configured full-snapshot interval.
func (publisher *MQTTPublisher) SnapshotInterval() time.Duration {
	if publisher == nil {
		return defaultMQTTFullSnapshotInterval
	}
	publisher.snapshotMu.Lock()
	defer publisher.snapshotMu.Unlock()
	if publisher.snapshotInterval <= 0 {
		return defaultMQTTFullSnapshotInterval
	}
	return publisher.snapshotInterval
}

func (publisher *MQTTPublisher) registerShadowDriverPoints(cfg GatewayModbusDriverConfig) {
	if publisher == nil {
		return
	}
	deviceKey := strings.TrimSpace(cfg.DeviceKey)
	if deviceKey == "" {
		return
	}
	units := make(map[string]string, len(cfg.Registers))
	if publisher.edgeRuntime != nil {
		for _, reg := range cfg.Registers {
			if desc, ok := publisher.edgeRuntime.ChannelDescriptor(deviceKey + "/" + reg.PointCode); ok {
				units[reg.PointCode] = strings.TrimSpace(desc.Unit)
			}
		}
	}

	publisher.pointMu.Lock()
	if publisher.pointByKey == nil {
		publisher.pointByKey = make(map[string]PointConfig, len(cfg.Registers))
	}
	if publisher.shadowPointKeysByDevice == nil {
		publisher.shadowPointKeysByDevice = make(map[string]map[string]struct{})
	}
	newKeys := make(map[string]struct{}, len(cfg.Registers))
	for _, reg := range cfg.Registers {
		newKeys[pointReference(deviceKey, reg.PointCode)] = struct{}{}
	}
	var removedKeys []string
	for oldKey := range publisher.shadowPointKeysByDevice[deviceKey] {
		if _, keep := newKeys[oldKey]; !keep {
			delete(publisher.pointByKey, oldKey)
			removedKeys = append(removedKeys, oldKey)
		}
	}
	for _, reg := range cfg.Registers {
		key := pointReference(deviceKey, reg.PointCode)
		publisher.pointByKey[key] = PointConfig{
			PointID:      deviceKey + ":" + reg.PointCode,
			DeviceID:     deviceKey,
			SourceKey:    reg.PointCode,
			TelemetryKey: reg.PointCode,
			PointCode:    reg.PointCode,
			Unit:         units[reg.PointCode],
		}
	}
	publisher.shadowPointKeysByDevice[deviceKey] = newKeys
	publisher.pointMu.Unlock()

	if len(removedKeys) > 0 {
		publisher.snapshotMu.Lock()
		for _, key := range removedKeys {
			delete(publisher.lastPointStates, key)
		}
		publisher.snapshotMu.Unlock()
	}
}

func (publisher *MQTTPublisher) unregisterShadowDriverPoints(deviceKey string) {
	if publisher == nil {
		return
	}
	deviceKey = strings.TrimSpace(deviceKey)
	if deviceKey == "" {
		return
	}
	publisher.pointMu.Lock()
	var removedKeys []string
	for key := range publisher.shadowPointKeysByDevice[deviceKey] {
		delete(publisher.pointByKey, key)
		removedKeys = append(removedKeys, key)
	}
	delete(publisher.shadowPointKeysByDevice, deviceKey)
	publisher.pointMu.Unlock()

	if len(removedKeys) > 0 {
		publisher.snapshotMu.Lock()
		for _, key := range removedKeys {
			delete(publisher.lastPointStates, key)
		}
		publisher.snapshotMu.Unlock()
	}
}

func (publisher *MQTTPublisher) lookupPoint(deviceID, telemetryKey string) (PointConfig, bool) {
	publisher.pointMu.RLock()
	point, ok := publisher.pointByKey[pointReference(deviceID, telemetryKey)]
	publisher.pointMu.RUnlock()
	return point, ok
}

func (publisher *MQTTPublisher) timedataPointMetadata(address string) TimedataResendPointMeta {
	var meta TimedataResendPointMeta
	if dev, ch, ok := strings.Cut(address, "/"); ok {
		meta.DeviceID = dev
		meta.PointCode = ch
	}
	if publisher != nil {
		if pt, ok := publisher.lookupPoint(meta.DeviceID, meta.PointCode); ok {
			if strings.TrimSpace(pt.PointCode) != "" {
				meta.PointCode = strings.TrimSpace(pt.PointCode)
			}
			meta.Unit = strings.TrimSpace(pt.Unit)
		}
		if publisher.edgeRuntime != nil {
			if desc, ok := publisher.edgeRuntime.ChannelDescriptor(address); ok {
				if meta.Unit == "" {
					meta.Unit = strings.TrimSpace(desc.Unit)
				}
				if desc.ResendPriority != "" {
					meta.Priority = desc.ResendPriority
				} else if desc.PollPriority != "" {
					meta.Priority = desc.PollPriority
				}
			}
		}
	}
	meta.Priority = normalizeResendPriority(meta.Priority)
	return meta
}

func (publisher *MQTTPublisher) buildEnvelope(measurements []Measurement) (mqttTelemetryEnvelope, error) {
	env, _, err := publisher.buildEnvelopeWithObservations(measurements)
	return env, err
}

func (publisher *MQTTPublisher) buildEnvelopeWithObservations(measurements []Measurement) (mqttTelemetryEnvelope, []PublishedObservation, error) {
	type deviceBatch struct {
		timestamp time.Time
		points    []mqttEnvelopePoint
	}
	byDevice := make(map[string]deviceBatch)
	publishedAt := time.Time{}
	var gatewaySequence uint64
	emittedObs := make([]PublishedObservation, 0, len(measurements))

	batchObservedAt := time.Time{}
	for _, measurement := range measurements {
		if measurement.ObservedAt.After(batchObservedAt) {
			batchObservedAt = measurement.ObservedAt
		}
	}
	if batchObservedAt.IsZero() {
		batchObservedAt = time.Now().UTC()
	}

	publisher.snapshotMu.Lock()
	interval := publisher.snapshotInterval
	if interval <= 0 {
		interval = defaultMQTTFullSnapshotInterval
	}
	fullSnapshotDue := publisher.lastFullSnapshotAt.IsZero() || batchObservedAt.Sub(publisher.lastFullSnapshotAt) >= interval
	if publisher.lastPointStates == nil {
		publisher.lastPointStates = map[string]lastEmittedPointState{}
	}
	snapshotCopy := make(map[string]lastEmittedPointState, len(publisher.lastPointStates))
	for k, v := range publisher.lastPointStates {
		snapshotCopy[k] = v
	}
	publisher.snapshotMu.Unlock()

	updatedPointStates := make(map[string]lastEmittedPointState, len(measurements))
	for _, measurement := range measurements {
		refKey := pointReference(measurement.DeviceID, measurement.TelemetryKey)
		point, ok := publisher.lookupPoint(measurement.DeviceID, measurement.TelemetryKey)
		if !ok {
			return mqttTelemetryEnvelope{}, nil, fmt.Errorf("MQTT point metadata is missing for %s/%s", measurement.DeviceID, measurement.TelemetryKey)
		}
		var unit *string
		if strings.TrimSpace(point.Unit) != "" {
			value := strings.TrimSpace(point.Unit)
			unit = &value
		}
		quality := uint8(0)
		if !strings.EqualFold(strings.TrimSpace(measurement.Quality), "GOOD") {
			quality = 1
		}

		deadband := telemetryDeadband(point, publisher.configShadow.deadbandFor(measurement.DeviceID, strings.TrimSpace(point.PointCode)))
		prevState, hasPrev := snapshotCopy[refKey]
		changed := !hasPrev || prevState.quality != quality || telemetryValueChanged(prevState.value, measurement.Value, deadband)
		if !fullSnapshotDue && !changed {
			continue
		}

		var targets []shadowEmitTarget
		if fullSnapshotDue {
			targets = publisher.configShadow.resolveTargetsUnthrottled(
				measurement.DeviceID,
				measurement.TelemetryKey,
				strings.TrimSpace(point.PointCode),
				measurement.ObservedAt,
			)
		} else {
			targets = publisher.configShadow.resolveAndFilterTargets(
				measurement.DeviceID,
				measurement.TelemetryKey,
				strings.TrimSpace(point.PointCode),
				measurement.ObservedAt,
			)
		}
		for _, target := range targets {
			batch := byDevice[target.DeviceID]
			batch.points = append(batch.points, mqttEnvelopePoint{
				Code:    target.PointCode,
				Value:   measurement.Value,
				Quality: quality,
				Unit:    unit,
			})
			if measurement.ObservedAt.After(batch.timestamp) {
				batch.timestamp = measurement.ObservedAt
			}
			byDevice[target.DeviceID] = batch
		}
		if len(targets) > 0 {
			updatedPointStates[refKey] = lastEmittedPointState{value: measurement.Value, quality: quality}
			channelCode := strings.TrimSpace(point.PointCode)
			if channelCode == "" {
				channelCode = measurement.TelemetryKey
			}
			addr := measurement.DeviceID + "/" + channelCode
			if publisher.edgeRuntime != nil {
				addr = publisher.edgeRuntime.ChannelAddressForPoint(point.PointID, measurement.DeviceID, channelCode)
			}
			emittedObs = append(emittedObs, PublishedObservation{
				Address:    addr,
				ObservedAt: measurement.ObservedAt,
			})
			if measurement.ObservedAt.After(publishedAt) {
				publishedAt = measurement.ObservedAt
			}
			if measurement.Sequence > gatewaySequence {
				gatewaySequence = measurement.Sequence
			}
		}
	}
	if len(byDevice) == 0 {
		return mqttTelemetryEnvelope{}, nil, nil
	}

	publisher.snapshotMu.Lock()
	if publisher.lastPointStates == nil {
		publisher.lastPointStates = map[string]lastEmittedPointState{}
	}
	for k, v := range updatedPointStates {
		publisher.lastPointStates[k] = v
	}
	if fullSnapshotDue {
		publisher.lastFullSnapshotAt = batchObservedAt
	}
	publisher.snapshotMu.Unlock()
	deviceIDs := make([]string, 0, len(byDevice))
	for deviceID := range byDevice {
		deviceIDs = append(deviceIDs, deviceID)
	}
	sort.Strings(deviceIDs)
	devices := make([]mqttEnvelopeDevice, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		batch := byDevice[deviceID]
		sort.Slice(batch.points, func(left, right int) bool { return batch.points[left].Code < batch.points[right].Code })
		devices = append(devices, mqttEnvelopeDevice{
			DeviceID:        deviceID,
			DeviceTimestamp: batch.timestamp.UTC().UnixMilli(),
			Points:          batch.points,
		})
	}
	messageID, err := newUUIDV7(publishedAt)
	if err != nil {
		return mqttTelemetryEnvelope{}, nil, err
	}
	return mqttTelemetryEnvelope{
		SchemaVersion: mqttTelemetryEnvelopeSchemaVersion,
		MessageID:     messageID,
		GatewayID:     publisher.gatewayID,
		Timestamp:     publishedAt.UTC().UnixMilli(),
		Sequence:      gatewaySequence,
		Replay:        false,
		Payload:       mqttTelemetryPayload{Devices: devices},
	}, emittedObs, nil
}

func mqttCommandTopic(config MQTTGatewayConfig) string {
	return "hvac/v1/" + config.GatewayID + "/down/command"
}

func mqttReplyTopic(config MQTTGatewayConfig) string {
	return "hvac/v1/" + config.GatewayID + "/up/reply"
}

func mqttPublisherTLSConfig(config MQTTGatewayConfig) (*tls.Config, error) {
	_, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, errors.New("load MQTT gateway client identity failed")
	}
	caContent, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, errors.New("read MQTT gateway CA failed")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caContent) {
		return nil, errors.New("MQTT gateway CA is invalid")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    rootCAs,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			identity, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
			return &identity, err
		},
		ServerName: strings.TrimSpace(config.ServerName),
	}, nil
}

func newUUIDV7(at time.Time) (string, error) {
	milliseconds := at.UTC().UnixMilli()
	if milliseconds < 0 || milliseconds >= 1<<48 {
		return "", errors.New("MQTT message timestamp is outside UUIDv7 range")
	}
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(milliseconds)<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate MQTT message identity: %w", err)
	}
	raw[6] = 0x70 | (raw[6] & 0x0f)
	raw[8] = 0x80 | (raw[8] & 0x3f)
	encoded := make([]byte, 32)
	hex.Encode(encoded, raw[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}

func telemetryDeadband(point PointConfig, policyDeadband float64) float64 {
	best := policyDeadband
	if point.SourceMetadata != nil {
		if raw, ok := point.SourceMetadata["deadband"]; ok {
			if val, numOK := toFloat64(raw); numOK && !math.IsNaN(val) && !math.IsInf(val, 0) && val > best {
				best = val
			}
		}
	}
	return best
}

func telemetryValueChanged(prev, curr any, deadband float64) bool {
	pNum, pOK := toFloat64(prev)
	cNum, cOK := toFloat64(curr)
	if pOK && cOK {
		if math.IsNaN(pNum) != math.IsNaN(cNum) {
			return true
		}
		if math.IsNaN(pNum) && math.IsNaN(cNum) {
			return false
		}
		return math.Abs(cNum-pNum) > deadband
	}
	return !reflect.DeepEqual(prev, curr)
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint64:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint16:
		return float64(x), true
	case int16:
		return float64(x), true
	default:
		return 0, false
	}
}
