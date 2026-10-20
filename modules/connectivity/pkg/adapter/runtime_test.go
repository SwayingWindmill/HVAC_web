package adapter

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

// ackLog records acknowledged message ids in order.
type ackLog struct {
	mu    sync.Mutex
	acked []string
	seen  chan string
}

func newAckLog() *ackLog { return &ackLog{seen: make(chan string, 64)} }

func (log *ackLog) ack(id string) func(*paho.Publish) error {
	return func(*paho.Publish) error {
		log.mu.Lock()
		log.acked = append(log.acked, id)
		log.mu.Unlock()
		log.seen <- id
		return nil
	}
}

func (log *ackLog) await(t *testing.T, id string) {
	t.Helper()
	select {
	case got := <-log.seen:
		if got != id {
			t.Fatalf("acked %s, want %s", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("message %s was never acknowledged", id)
	}
}

func TestFullGatewayQueuePausesInsteadOfDropping(t *testing.T) {
	telemetry := &fakeTelemetry{release: make(chan struct{})}
	runtime := newTestRuntime(t, telemetry, 1)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()

	// The worker holds the first message; the second fills the queue; the third waits.
	for index := 1; index <= 2; index++ {
		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
			t.Fatal(err)
		}
	}
	thirdQueued := make(chan error, 1)
	go func() { thirdQueued <- runtime.enqueue(ctx, nil, publish(testGatewayA, 3), acks.ack(messageID(3))) }()
	select {
	case err := <-thirdQueued:
		t.Fatalf("enqueue into a full Gateway queue returned %v instead of waiting", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(telemetry.release)
	if err := <-thirdQueued; err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		acks.await(t, messageID(index))
	}
	if accepted := len(telemetry.accepted()); accepted != 3 {
		t.Fatalf("accepted=%d, want all 3", accepted)
	}
}

func TestTransientFailureIsRetriedInOrderBeforeAck(t *testing.T) {
	telemetry := &fakeTelemetry{failures: 2}
	runtime := newTestRuntime(t, telemetry, 8)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()
	for index := 1; index <= 2; index++ {
		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
			t.Fatal(err)
		}
	}
	acks.await(t, messageID(1))
	acks.await(t, messageID(2))
	observations := telemetry.accepted()
	if len(observations) != 2 || observations[0].SourcePosition.Offset != 1 || observations[1].SourcePosition.Offset != 2 {
		t.Fatalf("observations=%#v", observations)
	}
}

func TestGatewaysDoNotWaitForEachOther(t *testing.T) {
	telemetry := &fakeTelemetry{failures: 1_000_000}
	runtime := newTestRuntime(t, telemetry, 8)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()
	unknownGateway := "018f3e00-4000-7000-8000-000000000999"
	// Gateway A keeps failing; a message from an unknown Gateway is still quarantined and acked.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(unknownGateway, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	acks.await(t, messageID(2))
}

func newTestRuntime(t *testing.T, telemetry RuntimeClient, capacity int) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    capacity,
	}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	return runtime
}

func publish(gatewayID string, index int) *paho.Publish {
	return &paho.Publish{Topic: telemetryTopic(gatewayID), Payload: telemetryPayload(gatewayID, messageID(index), index, "METER-01", "active_power"), QoS: 1}
}

// The broker restarted while the database was down: the message being written when the
// connection dropped can no longer be acknowledged on that connection. Connectivity must
// keep consuming the Gateway's messages on the next connection and keep reconnecting.
func TestUplinkKeepsConsumingAfterAnAckFailsOnADroppedConnection(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{release: make(chan struct{})}
	runtime := startBrokerRuntime(t, broker, telemetry, 1)

	first := broker.awaitConnection(t)
	first.deliver(t, 1, 1, false)
	awaitQueued(t, runtime, testGatewayA, 0)
	first.drop()

	second := broker.awaitConnection(t)
	second.deliver(t, 1, 1, true)
	awaitQueued(t, runtime, testGatewayA, 1)
	close(telemetry.release)
	second.deliver(t, 2, 2, false)
	second.awaitAcks(t, 1, 2)

	second.drop()
	broker.awaitConnection(t)
}

// While the database is down a Gateway's queue fills and receiving pauses. A broker
// restart in that state must still end in a new connection; the broker redelivers what
// was never acknowledged.
func TestUplinkReconnectsWhileAGatewayQueueIsFull(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{release: make(chan struct{})}
	runtime := startBrokerRuntime(t, broker, telemetry, 1)

	first := broker.awaitConnection(t)
	first.deliver(t, 1, 1, false)
	awaitQueued(t, runtime, testGatewayA, 0)
	first.deliver(t, 2, 2, false)
	awaitQueued(t, runtime, testGatewayA, 1)
	first.deliver(t, 3, 3, false)
	first.drop()

	second := broker.awaitConnection(t)
	close(telemetry.release)
	for index := 1; index <= 3; index++ {
		second.deliver(t, uint16(index), index, true)
	}
	second.awaitAcks(t, 1, 2, 3)
}

func startBrokerRuntime(t *testing.T, broker *fakeBroker, telemetry RuntimeClient, capacity int) *Runtime {
	t.Helper()
	runtime := newTestRuntime(t, telemetry, capacity)
	runtime.config.MQTT = broker.mqtt
	runtime.reconnectBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			t.Error("Run did not return after its context was cancelled")
		}
	})
	return runtime
}

// awaitQueued waits until the Gateway's worker has taken a message and depth messages wait behind it.
func awaitQueued(t *testing.T, runtime *Runtime, gatewayID string, depth int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.queuesMu.Lock()
		queue, ok := runtime.queues[gatewayID]
		runtime.queuesMu.Unlock()
		if ok && len(queue) == depth {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Gateway %s queue never reached depth %d", gatewayID, depth)
}
