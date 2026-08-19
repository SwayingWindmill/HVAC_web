package adapter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	runtimeGatewayA = "EG8200-COMMERCIAL-001"
	runtimeGatewayB = "EG8200-COMMERCIAL-002"
	runtimeMessageA = "0198a100-0000-7000-8000-000000000011"
	runtimeMessageB = "0198a100-0000-7000-8000-000000000012"
)

type runtimeProcessingClient struct {
	mu            sync.Mutex
	attempts      map[string]int
	poisonDevice  string
	poisonStarted chan struct{}
}

func (client *runtimeProcessingClient) AcceptObservation(ctx context.Context, observation Observation) (ObservationReceipt, error) {
	client.mu.Lock()
	client.attempts[observation.ExternalID]++
	attempt := client.attempts[observation.ExternalID]
	client.mu.Unlock()
	if observation.ExternalID == client.poisonDevice {
		if attempt == 1 && client.poisonStarted != nil {
			close(client.poisonStarted)
		}
		select {
		case <-ctx.Done():
			return ObservationReceipt{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
			return ObservationReceipt{}, errors.New("telemetry runtime unavailable")
		}
	}
	return ObservationReceipt{Status: "ACCEPTED"}, nil
}

func (client *runtimeProcessingClient) AcceptGatewayEvidence(context.Context, GatewayEvidence) error {
	return nil
}

func (client *runtimeProcessingClient) AcceptPresenceEvidence(context.Context, PresenceEvidence) (PresenceEvidenceReceipt, error) {
	return PresenceEvidenceReceipt{}, nil
}

func (client *runtimeProcessingClient) AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error {
	return nil
}

func (client *runtimeProcessingClient) attemptCount(deviceID string) int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.attempts[deviceID]
}

func TestParkedPoisonMessageDoesNotBlockUnrelatedDeviceInSameGateway(t *testing.T) {
	client := &runtimeProcessingClient{attempts: map[string]int{}, poisonDevice: "POISON-01", poisonStarted: make(chan struct{})}
	runtime := newProcessingTestRuntime(t, client, 4)
	runtime.retryDelay = func(int) time.Duration { return 100 * time.Millisecond }
	queues := runtime.newProcessingQueues()
	ctx, cancel := context.WithCancel(t.Context())

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		runtime.processQueue(ctx, runtimeGatewayA, queues[runtimeGatewayA])
	}()
	t.Cleanup(func() {
		cancel()
		workers.Wait()
	})

	poisonAck := make(chan struct{}, 1)
	goodAck := make(chan struct{}, 1)
	poison := processingTestPublish(runtimeGatewayA, runtimeMessageA, "POISON-01", func(*paho.Publish) error {
		poisonAck <- struct{}{}
		return nil
	})
	good := processingTestPublish(runtimeGatewayA, runtimeMessageB, "GOOD-01", func(*paho.Publish) error {
		goodAck <- struct{}{}
		return nil
	})
	if handled, err := runtime.enqueueQueuedPublish(ctx, runtimeGatewayA, queues[runtimeGatewayA], poison); !handled || err != nil {
		t.Fatalf("enqueue poison handled=%t err=%v", handled, err)
	}
	select {
	case <-client.poisonStarted:
	case <-time.After(time.Second):
		t.Fatal("poison message did not start")
	}
	if handled, err := runtime.enqueueQueuedPublish(ctx, runtimeGatewayA, queues[runtimeGatewayA], good); !handled || err != nil {
		t.Fatalf("enqueue good handled=%t err=%v", handled, err)
	}
	select {
	case <-goodAck:
	case <-time.After(80 * time.Millisecond):
		t.Fatal("healthy device was blocked while poison message was parked")
	}
	select {
	case <-poisonAck:
		t.Fatal("poison message exhausted retries before bounded retry window elapsed")
	default:
	}
	select {
	case <-poisonAck:
	case <-time.After(time.Second):
		t.Fatal("poison message never reached terminal dead disposition")
	}
	if attempts := client.attemptCount("POISON-01"); attempts != mqttMaxProcessingAttempts {
		t.Fatalf("poison attempts=%d want=%d", attempts, mqttMaxProcessingAttempts)
	}
	if attempts := client.attemptCount("GOOD-01"); attempts != 1 {
		t.Fatalf("healthy attempts=%d want=1", attempts)
	}
	cancel()
}

func TestProcessingQueueSaturationIsExplicitDeadDisposition(t *testing.T) {
	client := &runtimeProcessingClient{attempts: map[string]int{}}
	runtime := newProcessingTestRuntime(t, client, 1)
	queue := make(chan queuedPublish, 1)
	ctx := t.Context()
	firstAck := 0
	deadAck := 0
	first := processingTestPublish(runtimeGatewayA, runtimeMessageA, "FIRST-01", func(*paho.Publish) error {
		firstAck++
		return nil
	})
	second := processingTestPublish(runtimeGatewayA, runtimeMessageB, "SECOND-01", func(*paho.Publish) error {
		deadAck++
		return nil
	})
	if handled, err := runtime.enqueueQueuedPublish(ctx, runtimeGatewayA, queue, first); !handled || err != nil {
		t.Fatalf("first enqueue handled=%t err=%v", handled, err)
	}
	if handled, err := runtime.enqueueQueuedPublish(ctx, runtimeGatewayA, queue, second); !handled || err != nil {
		t.Fatalf("saturated enqueue handled=%t err=%v", handled, err)
	}
	if firstAck != 0 || deadAck != 1 {
		t.Fatalf("acks first=%d dead=%d", firstAck, deadAck)
	}
	if depth := runtime.queueDepth.Load(); depth != 1 {
		t.Fatalf("queue depth=%d want=1", depth)
	}

	runtime.parkingSlots = make(chan struct{}, 1)
	runtime.parkingSlots <- struct{}{}
	parkingDeadAck := 0
	parked := processingTestPublish(runtimeGatewayA, runtimeMessageB, "PARKED-01", func(*paho.Publish) error {
		parkingDeadAck++
		return nil
	})
	parked.attempt = 1
	runtime.parkPublish(ctx, runtimeGatewayA, queue, parked, errors.New("telemetry runtime unavailable"))
	if parkingDeadAck != 1 {
		t.Fatalf("parking saturation dead ack=%d want=1", parkingDeadAck)
	}
}

func newProcessingTestRuntime(t *testing.T, client RuntimeClient, capacity int) *Runtime {
	t.Helper()
	scopes := []GatewayScopeConfig{
		{GatewayID: runtimeGatewayA, TenantID: testTenantID, SiteID: testSiteID},
		{GatewayID: runtimeGatewayB, TenantID: testTenantID, SiteID: testSiteID},
	}
	processor, err := NewProcessor("018f3e00-0000-7000-8000-000000000101", scopes, client)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		SchemaVersion:         ConfigSchemaVersion,
		IntegrationInstanceID: "018f3e00-0000-7000-8000-000000000101",
		MQTT: MQTTConfig{
			BrokerURL: "tls://localhost:8883", ClientID: "s06-test",
			TopicFilters: []string{"energy/v1/+/+/+/telemetry", "energy/v1/+/+/+/state", "energy/v1/+/+/+/event", "energy/v1/+/+/+/heartbeat"},
			CAFile:       "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local",
			KeepAliveSeconds: 30, SessionExpirySeconds: 3600, ConnectTimeoutSeconds: 5,
		},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		GatewayScopes:    scopes, ProcessingQueueCapacity: capacity,
	}
	runtime, err := NewRuntime(config, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func processingTestPublish(gatewayID, messageID, deviceID string, ack func(*paho.Publish) error) queuedPublish {
	topic := "energy/v1/" + testTenantID + "/" + testSiteID + "/" + gatewayID + "/telemetry"
	payload := []byte(`{"schemaVersion":"1.0","messageId":"` + messageID + `","gatewayId":"` + gatewayID + `","timestamp":1786352400000,"sequence":42,"replay":false,"payload":{"devices":[{"deviceId":"` + deviceID + `","deviceTimestamp":1786352399000,"points":[{"code":"active_power","value":126.4,"quality":0,"unit":"kW"}]}]}}`)
	return queuedPublish{packet: &paho.Publish{Topic: topic, Payload: payload, QoS: 1}, ack: ack}
}
