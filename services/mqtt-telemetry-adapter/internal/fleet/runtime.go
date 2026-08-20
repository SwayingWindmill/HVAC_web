package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/mqtt-telemetry-adapter/internal/adapter"
	"github.com/quanlaihe/hvac-web/services/mqtt-telemetry-adapter/internal/connectivity"
)

const maxReplicationPayloadBytes = 2 << 20

var errPermanentFleetMessage = errors.New("permanent Edge Fleet message error")

type Config struct {
	MQTT                  adapter.MQTTConfig
	IntegrationInstanceID string
	TenantID              string
	SiteID                string
	GatewayID             string
	Policy                edgefleet.HandshakePolicy
}

type Runtime struct {
	config     Config
	store      *connectivity.Store
	descriptor connectivity.EdgeRuntimeDescriptor
	logger     *slog.Logger
	metrics    *observability.Registry
	manager    *autopaho.ConnectionManager
	ready      atomic.Bool

	mu         sync.Mutex
	lastStatus edgefleet.SyncStatus
}

func NewRuntime(ctx context.Context, config Config, store *connectivity.Store, logger *slog.Logger, metrics *observability.Registry) (*Runtime, error) {
	if store == nil || strings.TrimSpace(config.IntegrationInstanceID) == "" || strings.TrimSpace(config.TenantID) == "" ||
		strings.TrimSpace(config.SiteID) == "" || strings.TrimSpace(config.GatewayID) == "" || config.Policy.ProtocolSchemaVersion <= 0 {
		return nil, errors.New("Edge Fleet runtime configuration is invalid")
	}
	descriptor, err := store.LoadEdgeRuntimeDescriptor(ctx, config.IntegrationInstanceID)
	if err != nil {
		return nil, err
	}
	if descriptor.EdgeExternalID != strings.TrimSpace(config.GatewayID) {
		return nil, errors.New("Edge Fleet durable identity does not match Integration gateway")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	return &Runtime{config: config, store: store, descriptor: descriptor, logger: logger, metrics: metrics}, nil
}

func (runtime *Runtime) Ready() bool {
	return runtime != nil && runtime.ready.Load()
}

func (runtime *Runtime) Run(ctx context.Context) error {
	if runtime == nil {
		return errors.New("Edge Fleet runtime is unavailable")
	}
	brokerURL, err := url.Parse(strings.TrimSpace(runtime.config.MQTT.BrokerURL))
	if err != nil {
		return fmt.Errorf("parse Edge Fleet MQTT broker URL: %w", err)
	}
	tlsConfig, err := adapter.NewMQTTTLSConfig(runtime.config.MQTT)
	if err != nil {
		return err
	}
	clientID := strings.TrimSpace(runtime.config.MQTT.ClientID) + "-fleet"
	if len(clientID) > 128 {
		return errors.New("Edge Fleet MQTT client ID exceeds 128 characters")
	}
	uplinkTopic := runtime.uplinkTopic()
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     runtime.config.MQTT.KeepAliveSeconds,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         runtime.config.MQTT.SessionExpirySeconds,
		ConnectTimeout:                runtime.config.MQTT.ConnectTimeout(),
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		OnConnectError: func(connectErr error) {
			runtime.ready.Store(false)
			_ = runtime.metrics.AddCounter("hvac_edge_fleet_connections_total", "Edge Fleet MQTT connections by outcome.", map[string]string{"outcome": "failed"}, 1)
			runtime.logger.Warn("edge_fleet_connect_failed", "error", connectErr.Error())
		},
		OnConnectionDown: func() bool {
			runtime.ready.Store(false)
			return true
		},
		ClientConfig: paho.ClientConfig{
			ClientID:                   clientID,
			EnableManualAcknowledgment: true,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(received paho.PublishReceived) (bool, error) {
					if received.Packet == nil || received.Client == nil {
						return false, nil
					}
					if received.Packet.Topic != uplinkTopic {
						return true, received.Client.Ack(received.Packet)
					}
					if err := runtime.handleUplink(ctx, received.Packet.Payload); err != nil {
						_ = runtime.metrics.AddCounter("hvac_edge_fleet_messages_total", "Edge Fleet messages by outcome.", map[string]string{"outcome": "failed"}, 1)
						runtime.logger.Warn("edge_fleet_message_failed", "error", err.Error())
						if errors.Is(err, errPermanentFleetMessage) {
							return true, received.Client.Ack(received.Packet)
						}
						return true, nil
					}
					if err := received.Client.Ack(received.Packet); err != nil {
						return true, err
					}
					_ = runtime.metrics.AddCounter("hvac_edge_fleet_messages_total", "Edge Fleet messages by outcome.", map[string]string{"outcome": "processed"}, 1)
					return true, nil
				},
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		go func() {
			subscribeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if _, err := manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: uplinkTopic, QoS: 1}}}); err != nil {
				runtime.ready.Store(false)
				runtime.logger.Warn("edge_fleet_subscribe_failed", "error", err.Error())
				return
			}
			runtime.ready.Store(true)
			_ = runtime.metrics.AddCounter("hvac_edge_fleet_connections_total", "Edge Fleet MQTT connections by outcome.", map[string]string{"outcome": "success"}, 1)
		}()
	}
	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		return fmt.Errorf("create Edge Fleet MQTT connection: %w", err)
	}
	runtime.manager = manager
	if err := manager.AwaitConnection(ctx); err != nil {
		return fmt.Errorf("await Edge Fleet MQTT connection: %w", err)
	}
	select {
	case <-ctx.Done():
	case <-manager.Done():
		if ctx.Err() == nil {
			return errors.New("Edge Fleet MQTT connection manager stopped")
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime.ready.Store(false)
	return manager.Disconnect(shutdownContext)
}

func (runtime *Runtime) handleUplink(ctx context.Context, payload []byte) error {
	envelope, err := edgefleet.DecodeReplicationEnvelope(payload, maxReplicationPayloadBytes)
	if err != nil {
		return fmt.Errorf("%w: %v", errPermanentFleetMessage, err)
	}
	if envelope.EdgeID != runtime.config.GatewayID {
		return fmt.Errorf("%w: Edge Fleet envelope gateway does not match durable Integration", errPermanentFleetMessage)
	}
	switch envelope.Type {
	case edgefleet.ReplicationHandshake:
		request, err := edgefleet.DecodeReplicationPayload[edgefleet.HandshakeRequest](envelope)
		if err != nil {
			return fmt.Errorf("%w: %v", errPermanentFleetMessage, err)
		}
		if request.EdgeID != envelope.EdgeID || request.CredentialRevision != runtime.descriptor.CredentialRevision {
			return fmt.Errorf("%w: Edge Fleet handshake identity or credential revision mismatch", errPermanentFleetMessage)
		}
		result, err := runtime.store.RecordEdgeHandshake(ctx, connectivity.EdgeHandshakeInput{
			EdgeNodeID: runtime.descriptor.EdgeNodeID,
			SessionID:  runtime.descriptor.SessionID,
			Request:    request,
			Policy:     runtime.config.Policy,
		})
		if err != nil {
			return err
		}
		if result.Status != edgefleet.HandshakeRejected {
			status := "ACTIVE"
			if result.Status == edgefleet.HandshakeReadOnly {
				status = "READ_ONLY"
			} else if result.Status == edgefleet.HandshakeUpgradeRequired {
				status = "UPGRADE_REQUIRED"
			}
			if err := runtime.store.OpenEdgeSyncSession(ctx, connectivity.EdgeSyncSessionInput{
				EdgeNodeID: runtime.descriptor.EdgeNodeID, ConnectivitySessionID: runtime.descriptor.SessionID, Status: status,
			}); err != nil {
				return err
			}
		}
		return runtime.publish(ctx, edgefleet.ReplicationHandshakeResult, edgefleet.HandshakeResultPayload{Result: result})

	case edgefleet.ReplicationObservedState:
		status, err := edgefleet.DecodeReplicationPayload[edgefleet.SyncStatus](envelope)
		if err != nil {
			return fmt.Errorf("%w: %v", errPermanentFleetMessage, err)
		}
		if status.Runtime.RuntimeVersion == "" || status.Runtime.ProtocolSchemaVersion <= 0 {
			return fmt.Errorf("%w: Edge Fleet observed state is missing runtime identity", errPermanentFleetMessage)
		}
		lastSeen := time.UnixMilli(envelope.SentAt).UTC()
		if err := runtime.store.RecordObservedEdgeState(ctx, connectivity.ObservedEdgeStateInput{
			EdgeNodeID:      runtime.descriptor.EdgeNodeID,
			ActiveReleaseID: status.Observed.ActiveReleaseID, StagedReleaseID: status.Observed.StagedReleaseID, PreviousReleaseID: status.Observed.PreviousReleaseID,
			ActiveSnapshotRevision: status.Observed.ActiveSnapshotRevision, DesiredRevision: status.Observed.DesiredRevision,
			DeliveryCursor: status.Observed.DeliveryCursor, ReportedConfigRevision: status.Observed.ReportedConfigRevision,
			RuntimeVersion: status.Runtime.RuntimeVersion, ProtocolSchemaVersion: status.Runtime.ProtocolSchemaVersion,
			ManifestDigest: status.Observed.ManifestDigest, Health: normalizedOr(status.Health, "UNKNOWN"), CapacityState: status.Observed.CapacityState,
			DriftStatus: normalizedOr(status.DriftStatus, "UNKNOWN"), DriftReason: status.DriftReason, BacklogBytes: status.BacklogBytes,
			QuarantineCount: status.QuarantineCount, LastSeenAt: lastSeen,
		}); err != nil {
			return err
		}
		runtime.mu.Lock()
		runtime.lastStatus = status
		runtime.mu.Unlock()
		return runtime.sync(ctx, status)

	case edgefleet.ReplicationChangeAck:
		ack, err := edgefleet.DecodeReplicationPayload[edgefleet.DeliveryAck](envelope)
		if err != nil {
			return fmt.Errorf("%w: %v", errPermanentFleetMessage, err)
		}
		result, err := runtime.store.RecordEdgeDeliveryAck(ctx, runtime.descriptor.EdgeNodeID, ack)
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		status := runtime.lastStatus
		if result.CommittedCursor > status.Observed.DeliveryCursor {
			status.Observed.DeliveryCursor = result.CommittedCursor
			runtime.lastStatus = status
		}
		runtime.mu.Unlock()
		if status.Runtime.RuntimeVersion != "" {
			return runtime.sync(ctx, status)
		}
		return nil

	case edgefleet.ReplicationReleaseResult:
		return nil

	case edgefleet.ReplicationOTAResult:
		payload, err := edgefleet.DecodeReplicationPayload[edgefleet.OTAResultPayload](envelope)
		if err != nil {
			return fmt.Errorf("%w: %v", errPermanentFleetMessage, err)
		}
		return runtime.store.RecordOTAResult(ctx, runtime.descriptor.EdgeNodeID, payload.Result)
	default:
		return fmt.Errorf("%w: unsupported Edge Fleet uplink type %s", errPermanentFleetMessage, envelope.Type)
	}
}

func (runtime *Runtime) sync(ctx context.Context, status edgefleet.SyncStatus) error {
	bundle, err := runtime.store.LoadEdgeSyncBundle(ctx, runtime.descriptor.EdgeNodeID, status, 256)
	if errors.Is(err, connectivity.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if bundle.Mode == edgefleet.ReconnectDelta {
		for _, disposition := range bundle.Dispositions {
			if err := runtime.publish(ctx, edgefleet.ReplicationQuarantineDisposition, disposition); err != nil {
				return err
			}
		}
		if len(bundle.Items) > 0 {
			if err := runtime.publish(ctx, edgefleet.ReplicationChangeBatch, edgefleet.ChangeBatchPayload{Items: bundle.Items}); err != nil {
				return err
			}
		}
		if len(bundle.Dispositions) > 0 || len(bundle.Items) > 0 || status.DriftStatus != "CONVERGED" {
			return nil
		}
		return runtime.syncOTA(ctx)
	}
	if err := runtime.publish(ctx, edgefleet.ReplicationReleaseStage, bundle.Release); err != nil {
		return err
	}
	if err := runtime.publish(ctx, edgefleet.ReplicationSnapshotBegin, bundle.Meta); err != nil {
		return err
	}
	for _, chunk := range bundle.Chunks {
		if err := runtime.publish(ctx, edgefleet.ReplicationSnapshotChunk, chunk); err != nil {
			return err
		}
	}
	return runtime.publish(ctx, edgefleet.ReplicationSnapshotCommit, edgefleet.SnapshotCommitPayload{Meta: bundle.Meta, Release: bundle.Release})
}

func (runtime *Runtime) syncOTA(ctx context.Context) error {
	dispatch, err := runtime.store.LoadDispatchableOTA(ctx, runtime.descriptor.EdgeNodeID)
	if errors.Is(err, connectivity.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return runtime.publish(ctx, edgefleet.ReplicationOTAStage, edgefleet.OTAStagePayload{Artifact: dispatch.Artifact})
}

func (runtime *Runtime) publish(ctx context.Context, messageType edgefleet.ReplicationType, payload any) error {
	if runtime.manager == nil {
		return errors.New("Edge Fleet MQTT manager is unavailable")
	}
	envelope, err := edgefleet.NewReplicationEnvelope(runtime.config.GatewayID, messageType, payload, time.Now().UTC())
	if err != nil {
		return err
	}
	body, err := edgefleet.EncodeReplicationEnvelope(envelope)
	if err != nil {
		return err
	}
	publishContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = runtime.manager.Publish(publishContext, &paho.Publish{QoS: 1, Retain: false, Topic: runtime.downlinkTopic(), Payload: body})
	return err
}

func (runtime *Runtime) uplinkTopic() string {
	return "energy/v1/" + runtime.config.TenantID + "/" + runtime.config.SiteID + "/" + runtime.config.GatewayID + "/fleet/up"
}

func (runtime *Runtime) downlinkTopic() string {
	return "energy/v1/" + runtime.config.TenantID + "/" + runtime.config.SiteID + "/" + runtime.config.GatewayID + "/fleet/down"
}

func normalizedOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
