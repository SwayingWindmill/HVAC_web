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
		if err := runtime.enqueue(ctx, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
			t.Fatal(err)
		}
	}
	thirdQueued := make(chan error, 1)
	go func() { thirdQueued <- runtime.enqueue(ctx, publish(testGatewayA, 3), acks.ack(messageID(3))) }()
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
		if err := runtime.enqueue(ctx, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
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
	if err := runtime.enqueue(ctx, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, publish(unknownGateway, 2), acks.ack(messageID(2))); err != nil {
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
