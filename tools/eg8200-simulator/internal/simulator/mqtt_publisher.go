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
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	filequeue "github.com/eclipse/paho.golang/autopaho/queue/file"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const mqttTelemetryEnvelopeSchemaVersion = "1.0"

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

type MQTTPublisher struct {
	gatewayID      string
	config         MQTTGatewayConfig
	pointByKey     map[string]PointConfig
	manager        *autopaho.ConnectionManager
	commandHandler *edgeCommandHandler
	commandTopic   string
	queueDirectory string
	metrics        *observability.Registry
	connected      *atomic.Bool
}

func NewMQTTPublisher(ctx context.Context, plantConfig Config, config MQTTGatewayConfig, plant *Plant, metrics *observability.Registry) (*MQTTPublisher, error) {
	if err := plantConfig.Validate(); err != nil {
		return nil, fmt.Errorf("plant config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	for _, deviceID := range plantConfig.ReportingDeviceIDs() {
		if _, ok := config.DeviceExternalIDByDeviceID[deviceID]; !ok {
			return nil, fmt.Errorf("MQTT gateway externalId mapping is missing device %s", deviceID)
		}
	}
	if err := os.MkdirAll(config.QueueDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create MQTT queue directory: %w", err)
	}
	persistentQueue, err := filequeue.New(config.QueueDirectory, "mqtt", ".packet")
	if err != nil {
		return nil, fmt.Errorf("open MQTT persistent queue: %w", err)
	}
	brokerURL, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}
	tlsConfig, err := mqttPublisherTLSConfig(config)
	if err != nil {
		return nil, err
	}
	commandHandler, err := newEdgeCommandHandler(plant, config, plantConfig.GatewayID)
	if err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	connected := &atomic.Bool{}
	_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 0)
	_ = metrics.SetGauge("hvac_edge_mqtt_queue_capacity_bytes", "Configured persistent MQTT queue capacity in bytes.", nil, float64(config.MaximumQueueBytes))
	manager, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         24 * 60 * 60,
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		Queue:                         persistentQueue,
		OnConnectError: func(error) {
			connected.Store(false)
			_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 0)
			_ = metrics.AddCounter("hvac_edge_mqtt_connections_total", "Edge MQTT connection attempts by outcome.", map[string]string{"outcome": "failed"}, 1)
		},
		OnConnectionDown: func() bool {
			connected.Store(false)
			_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 0)
			_ = metrics.AddCounter("hvac_edge_mqtt_disconnections_total", "Edge MQTT disconnections.", map[string]string{"reason_family": "transport"}, 1)
			return true
		},
		OnConnectionUp: func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
			connected.Store(true)
			_ = metrics.SetGauge("hvac_edge_mqtt_connected", "Whether the Edge publisher is connected to the MQTT broker.", nil, 1)
			_ = metrics.AddCounter("hvac_edge_mqtt_connections_total", "Edge MQTT connection attempts by outcome.", map[string]string{"outcome": "success"}, 1)
			commandTopic := "energy/v1/" + config.TenantID + "/" + config.SiteID + "/" + plantConfig.GatewayID + "/command"
			go func() {
				subscribeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				_, _ = manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: commandTopic, QoS: 1}}})
			}()
		},
		ClientConfig: paho.ClientConfig{
			ClientID: strings.TrimSpace(config.ClientID),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){commandHandler.Handle},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create MQTT publisher connection: %w", err)
	}
	pointByKey := make(map[string]PointConfig, len(plantConfig.Points))
	for _, point := range plantConfig.Points {
		pointByKey[pointReference(point.DeviceID, point.TelemetryKey)] = point
	}
	return &MQTTPublisher{
		gatewayID:      plantConfig.GatewayID,
		config:         config,
		pointByKey:     pointByKey,
		manager:        manager,
		commandHandler: commandHandler,
		commandTopic:   "energy/v1/" + config.TenantID + "/" + config.SiteID + "/" + plantConfig.GatewayID + "/command",
		queueDirectory: config.QueueDirectory,
		metrics:        metrics,
		connected:      connected,
	}, nil
}

func (publisher *MQTTPublisher) AwaitConnection(ctx context.Context) error {
	if publisher == nil || publisher.manager == nil {
		return errors.New("MQTT publisher is unavailable")
	}
	return publisher.manager.AwaitConnection(ctx)
}

func (publisher *MQTTPublisher) Ready() bool {
	return publisher != nil && publisher.connected != nil && publisher.connected.Load()
}

func (publisher *MQTTPublisher) RefreshMetrics() error {
	if publisher == nil || publisher.metrics == nil {
		return nil
	}
	queueBytes, err := directoryBytes(publisher.queueDirectory)
	if err != nil {
		return err
	}
	_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_bytes", "Bytes currently retained in the Edge persistent MQTT queue.", nil, float64(queueBytes))
	utilization := 0.0
	if publisher.config.MaximumQueueBytes > 0 {
		utilization = float64(queueBytes) / float64(publisher.config.MaximumQueueBytes)
	}
	_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_utilization_ratio", "Persistent MQTT queue utilization ratio.", nil, utilization)
	return nil
}

func (publisher *MQTTPublisher) PublishMeasurements(ctx context.Context, measurements []Measurement) (err error) {
	if publisher == nil || publisher.manager == nil {
		return errors.New("MQTT publisher is unavailable")
	}
	if len(measurements) == 0 {
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
			_ = publisher.metrics.AddCounter("hvac_edge_mqtt_values_total", "Point values queued for MQTT delivery.", map[string]string{"outcome": "queued"}, float64(len(measurements)))
		}
	}()
	queueBytes, err := directoryBytes(publisher.queueDirectory)
	if err != nil {
		return fmt.Errorf("measure MQTT queue size: %w", err)
	}
	if publisher.metrics != nil {
		_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_bytes", "Bytes currently retained in the Edge persistent MQTT queue.", nil, float64(queueBytes))
		_ = publisher.metrics.SetGauge("hvac_edge_mqtt_queue_utilization_ratio", "Persistent MQTT queue utilization ratio.", nil, float64(queueBytes)/float64(publisher.config.MaximumQueueBytes))
	}
	if queueBytes >= publisher.config.MaximumQueueBytes {
		if publisher.metrics != nil {
			_ = publisher.metrics.AddCounter("hvac_edge_mqtt_queue_limit_rejections_total", "Edge MQTT publishes rejected because the persistent queue reached its configured byte limit.", nil, 1)
		}
		return fmt.Errorf("MQTT persistent queue reached limit: %d >= %d", queueBytes, publisher.config.MaximumQueueBytes)
	}
	envelope, err := publisher.buildEnvelope(measurements)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode MQTT telemetry envelope: %w", err)
	}
	if int64(len(payload))+queueBytes > publisher.config.MaximumQueueBytes {
		if publisher.metrics != nil {
			_ = publisher.metrics.AddCounter("hvac_edge_mqtt_queue_limit_rejections_total", "Edge MQTT publishes rejected because the persistent queue reached its configured byte limit.", nil, 1)
		}
		return fmt.Errorf("MQTT persistent queue would exceed limit: %d + %d > %d", queueBytes, len(payload), publisher.config.MaximumQueueBytes)
	}
	topic := "energy/v1/" + publisher.config.TenantID + "/" + publisher.config.SiteID + "/" + publisher.gatewayID + "/telemetry"
	if err := publisher.manager.PublishViaQueue(ctx, &autopaho.QueuePublish{Publish: &paho.Publish{
		QoS:     1,
		Retain:  false,
		Topic:   topic,
		Payload: payload,
	}}); err != nil {
		return err
	}
	outcome = "queued"
	return nil
}

func (publisher *MQTTPublisher) Disconnect(ctx context.Context) error {
	if publisher == nil || publisher.manager == nil {
		return nil
	}
	return publisher.manager.Disconnect(ctx)
}

func (publisher *MQTTPublisher) buildEnvelope(measurements []Measurement) (mqttTelemetryEnvelope, error) {
	type deviceBatch struct {
		timestamp time.Time
		points    []mqttEnvelopePoint
	}
	byDevice := make(map[string]deviceBatch)
	publishedAt := time.Time{}
	var gatewaySequence uint64
	for _, measurement := range measurements {
		deviceID := strings.TrimSpace(publisher.config.DeviceExternalIDByDeviceID[measurement.DeviceID])
		if deviceID == "" {
			return mqttTelemetryEnvelope{}, fmt.Errorf("MQTT deviceId is missing for device %s", measurement.DeviceID)
		}
		point, ok := publisher.pointByKey[pointReference(measurement.DeviceID, measurement.TelemetryKey)]
		if !ok {
			return mqttTelemetryEnvelope{}, fmt.Errorf("MQTT point metadata is missing for %s/%s", measurement.DeviceID, measurement.TelemetryKey)
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
		batch := byDevice[deviceID]
		batch.points = append(batch.points, mqttEnvelopePoint{
			Code:    strings.TrimSpace(point.PointCode),
			Value:   measurement.Value,
			Quality: quality,
			Unit:    unit,
		})
		if measurement.ObservedAt.After(batch.timestamp) {
			batch.timestamp = measurement.ObservedAt
		}
		byDevice[deviceID] = batch
		if measurement.ObservedAt.After(publishedAt) {
			publishedAt = measurement.ObservedAt
		}
		if measurement.Sequence > gatewaySequence {
			gatewaySequence = measurement.Sequence
		}
	}
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
		return mqttTelemetryEnvelope{}, err
	}
	return mqttTelemetryEnvelope{
		SchemaVersion: mqttTelemetryEnvelopeSchemaVersion,
		MessageID:     messageID,
		GatewayID:     publisher.gatewayID,
		Timestamp:     publishedAt.UTC().UnixMilli(),
		Sequence:      gatewaySequence,
		Replay:        false,
		Payload:       mqttTelemetryPayload{Devices: devices},
	}, nil
}

func mqttPublisherTLSConfig(config MQTTGatewayConfig) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
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
		MinVersion:   tls.VersionTLS13,
		RootCAs:      rootCAs,
		Certificates: []tls.Certificate{certificate},
		ServerName:   strings.TrimSpace(config.ServerName),
	}, nil
}

func directoryBytes(directory string) (int64, error) {
	var total int64
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".packet" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
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
