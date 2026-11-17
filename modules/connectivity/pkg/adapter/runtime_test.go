package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	stdruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
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

func (log *ackLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.acked...)
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
// restart in that state must still end in a new connection, and readiness must show the
// connection was lost; the broker redelivers what was never acknowledged.
func TestUplinkReconnectsWhileAGatewayQueueIsFull(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{release: make(chan struct{})}
	runtime := startBrokerRuntime(t, broker, telemetry, 1)

	first := broker.awaitConnection(t)
	awaitReady(t, runtime, true)
	first.deliver(t, 1, 1, false)
	awaitQueued(t, runtime, testGatewayA, 0)
	first.deliver(t, 2, 2, false)
	awaitQueued(t, runtime, testGatewayA, 1)
	first.deliver(t, 3, 3, false)
	first.drop()
	awaitReady(t, runtime, false)

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
	// Long enough that a lost connection is observable before the next one is up.
	runtime.reconnectBackoff = func(int) time.Duration { return 200 * time.Millisecond }
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

func awaitReady(t *testing.T, runtime *Runtime, want bool) {
	t.Helper()
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.Ready() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("Ready()=%v, want %v", runtime.Ready(), want)
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

func TestCoalescedBacklogBatchDeliversAndAcksInStrictFIFOOrder(t *testing.T) {
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
	}
	runtime := newTestRuntime(t, telemetry, 16)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()

	// Hold M0 inside AcceptObservationBatch so M1..M3 queue up before the next drain.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered
	for index, pointCount := range []int{3, 2, 4} {
		msgNum := index + 2
		item := &paho.Publish{
			Topic:   telemetryTopic(testGatewayA),
			Payload: multiPointTelemetryPayload(testGatewayA, messageID(msgNum), msgNum, "METER-01", pointCount),
			QoS:     1,
		}
		if err := runtime.enqueue(ctx, nil, item, acks.ack(messageID(msgNum))); err != nil {
			t.Fatal(err)
		}
	}
	if len(acks.snapshot()) != 0 {
		t.Fatalf("messages acked before persistence: %v", acks.snapshot())
	}

	close(telemetry.release)
	for index := 1; index <= 4; index++ {
		acks.await(t, messageID(index))
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 2 || len(batches[0]) != 1 || len(batches[1]) != 9 {
		t.Fatalf("batches=%v, want [1 9] points across 2 AcceptObservationBatch calls", batchSizes(batches))
	}
}

func TestMixedBatchPreservesFIFOOrderAndDoesNotReprocessAckedPrefixOnLaterRetry(t *testing.T) {
	identities := newFakeIdentities()
	acks := newAckLog()
	var sawM4Failure int
	var acksAtM4Failure []string
	m4Failed := make(chan struct{}, 1)

	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		failHook: func(call int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourcePosition.Offset == 5 && sawM4Failure == 0 {
				sawM4Failure++
				acksAtM4Failure = acks.snapshot()
				select {
				case m4Failed <- struct{}{}:
				default:
				}
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    16,
	}, newTestProcessor(t, identities, telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })

	// M0 holds the worker while mixed batch [M1(telemetry), M2(invalid JSON), M3(event), M4(telemetry)] queues.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered

	mixed := []struct {
		id  string
		pub *paho.Publish
	}{
		{messageID(2), publish(testGatewayA, 2)},
		{messageID(3), &paho.Publish{Topic: telemetryTopic(testGatewayA), Payload: []byte(`{not-json`), QoS: 1}},
		{messageID(4), &paho.Publish{Topic: eventTopic(testGatewayA), Payload: eventPayload(testGatewayA, messageID(4), 4), QoS: 1}},
		{messageID(5), publish(testGatewayA, 5)},
	}
	for _, msg := range mixed {
		if err := runtime.enqueue(ctx, nil, msg.pub, acks.ack(msg.id)); err != nil {
			t.Fatal(err)
		}
	}

	close(telemetry.release)
	<-m4Failed
	for index := 1; index <= 5; index++ {
		acks.await(t, messageID(index))
	}

	wantAckedBeforeRetry := []string{messageID(1), messageID(2), messageID(3), messageID(4)}
	if len(acksAtM4Failure) != len(wantAckedBeforeRetry) {
		t.Fatalf("acks at M4 failure=%v, want %v", acksAtM4Failure, wantAckedBeforeRetry)
	}
	for index, want := range wantAckedBeforeRetry {
		if acksAtM4Failure[index] != want {
			t.Fatalf("acksAtM4Failure[%d]=%s, want %s", index, acksAtM4Failure[index], want)
		}
	}
	if quarantines := identities.snapshotQuarantines(); len(quarantines) != 1 {
		t.Fatalf("quarantines=%d, want 1 (M2 must not be re-quarantined)", len(quarantines))
	}
	if events := telemetry.recordedEvents(); len(events) != 1 || events[0].MessageID != messageID(4) {
		t.Fatalf("events=%#v, want 1 event for M3", events)
	}
	if got := acks.snapshot(); len(got) != 5 {
		t.Fatalf("final acks=%v, want 5", got)
	}
}

func TestOversizedEnvelopeChunksAcrossBatchCallsBeforeAck(t *testing.T) {
	acks := newAckLog()
	var acksBeforeSecondChunk []string
	telemetry := &fakeTelemetry{
		failHook: func(call int, batch []Observation) error {
			if call == 2 {
				acksBeforeSecondChunk = acks.snapshot()
			}
			return nil
		},
	}
	runtime := newTestRuntime(t, telemetry, 8)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })

	large := &paho.Publish{
		Topic:   telemetryTopic(testGatewayA),
		Payload: multiPointTelemetryPayload(testGatewayA, messageID(1), 1, "METER-01", 600),
		QoS:     1,
	}
	if err := runtime.enqueue(ctx, nil, large, acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	acks.await(t, messageID(1))
	if len(acksBeforeSecondChunk) != 0 {
		t.Fatalf("message acked before second chunk completed: %v", acksBeforeSecondChunk)
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 2 || len(batches[0]) != 512 || len(batches[1]) != 88 {
		t.Fatalf("chunk sizes=%v, want [512 88]", batchSizes(batches))
	}
}

func TestTransientBatchFailureRetriesInPlaceWithoutAckingSlice(t *testing.T) {
	acks := newAckLog()
	var acksOnFailure [][]string
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		failHook: func(call int, batch []Observation) error {
			if call == 2 || call == 3 {
				acksOnFailure = append(acksOnFailure, acks.snapshot())
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	runtime := newTestRuntime(t, telemetry, 8)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered
	for index := 2; index <= 4; index++ {
		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
			t.Fatal(err)
		}
	}
	close(telemetry.release)
	for index := 1; index <= 4; index++ {
		acks.await(t, messageID(index))
	}
	if len(acksOnFailure) != 2 {
		t.Fatalf("observed %d transient batch failures, want 2", len(acksOnFailure))
	}
	for attempt, seen := range acksOnFailure {
		if len(seen) != 1 || seen[0] != messageID(1) {
			t.Fatalf("attempt %d acks=%v, want only [%s]", attempt, seen, messageID(1))
		}
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 2 || len(batches[0]) != 1 || len(batches[1]) != 3 {
		t.Fatalf("batches=%v, want [1 3]", batchSizes(batches))
	}
}

func TestBatchFlushWindowFlushesImmediatelyAtLimitAndOnTimer(t *testing.T) {
	t.Run("flushes immediately when batch reaches limit", func(t *testing.T) {
		telemetry := &fakeTelemetry{}
		runtime, err := NewRuntime(Config{
			MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
			QueueCapacity:    2,
			BatchMaxMessages: 2,
			BatchFlushWindow: time.Second,
		}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() { cancel(); runtime.workers.Wait() })
		acks := newAckLog()

		started := time.Now()
		for index := 1; index <= 2; index++ {
			if err := runtime.enqueue(ctx, nil, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
				t.Fatal(err)
			}
		}
		acks.await(t, messageID(1))
		acks.await(t, messageID(2))
		if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
			t.Fatalf("limit-reached batch waited %s instead of flushing immediately", elapsed)
		}
	})

	t.Run("flushes partial batch when flush window expires", func(t *testing.T) {
		telemetry := &fakeTelemetry{}
		runtime, err := NewRuntime(Config{
			MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
			QueueCapacity:    8,
			BatchMaxMessages: 8,
			BatchFlushWindow: 20 * time.Millisecond,
		}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() { cancel(); runtime.workers.Wait() })
		acks := newAckLog()

		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
			t.Fatal(err)
		}
		acks.await(t, messageID(1))
	})
}

func batchSizes(batches [][]Observation) []int {
	sizes := make([]int, len(batches))
	for index, batch := range batches {
		sizes[index] = len(batch)
	}
	return sizes
}

type multiGatewayIdentities struct {
	*fakeIdentities
}

func (identities *multiGatewayIdentities) ResolveGateway(_ context.Context, gatewayID string) (Gateway, error) {
	return Gateway{ID: gatewayID, TenantID: testTenantID, SiteID: testSiteID}, nil
}

func TestTopicGatewayIDBucketsOnlyValidLowercaseUUIDv7(t *testing.T) {
	for _, valid := range []string{
		"hvac/v1/" + testGatewayA + "/up/telemetry",
		"hvac/v1/" + testGatewayA + "/up/event",
		"hvac/v1/" + testGatewayA + "/up/state",
	} {
		if got := TopicGatewayID(valid); got != testGatewayA {
			t.Fatalf("TopicGatewayID(%q) = %q, want %q", valid, got, testGatewayA)
		}
	}

	for _, invalid := range []string{
		"",
		"invalid",
		"energy/v1/x/y/z/telemetry",
		"hvac/v1/EG8200-COMMERCIAL-001/up/telemetry",
		"hvac/v1/018f3e00-4000-4000-8000-000000000100/up/telemetry",
		"hvac/v1/" + strings.ToUpper(testGatewayA) + "/up/telemetry",
		"hvac/v1/" + testGatewayA + "/down/command",
		"hvac/v1/" + testGatewayA + "/up/telemetry/extra",
		"hvac/v1//up/telemetry",
	} {
		if got := TopicGatewayID(invalid); got != "invalid" {
			t.Fatalf("TopicGatewayID(%q) = %q, want %q", invalid, got, "invalid")
		}
	}
}

func TestWorkerPoolServesOverHundredGatewaysWithBoundedGoroutines(t *testing.T) {
	const numGateways = 120
	const poolSize = 8
	var active atomic.Int32
	var maxActive atomic.Int32

	telemetry := &fakeTelemetry{
		failHook: func(_ int, _ []Observation) error {
			cur := active.Add(1)
			for {
				prev := maxActive.Load()
				if cur <= prev || maxActive.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			active.Add(-1)
			return nil
		},
	}
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    4,
		WorkerCount:      poolSize,
	}, newTestProcessor(t, &multiGatewayIdentities{fakeIdentities: newFakeIdentities()}, telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })

	beforeGoroutines := stdruntime.NumGoroutine()
	acked := make(chan string, numGateways)
	for index := 0; index < numGateways; index++ {
		gatewayID := fmt.Sprintf("018f3e00-4000-7000-8000-%012d", index+1)
		msgID := messageID(index + 1)
		if err := runtime.enqueue(ctx, nil, &paho.Publish{
			Topic:   telemetryTopic(gatewayID),
			Payload: telemetryPayload(gatewayID, msgID, index+1, "METER-01", "active_power"),
			QoS:     1,
		}, func(*paho.Publish) error {
			acked <- msgID
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	peakGoroutines := stdruntime.NumGoroutine()
	for index := 0; index < numGateways; index++ {
		select {
		case <-acked:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for gateway ack %d/%d", index, numGateways)
		}
		if cur := stdruntime.NumGoroutine(); cur > peakGoroutines {
			peakGoroutines = cur
		}
	}

	if peak := maxActive.Load(); peak < 2 || peak > poolSize {
		t.Fatalf("max concurrent workers=%d, want within [2, %d]", peak, poolSize)
	}
	if delta := peakGoroutines - beforeGoroutines; delta > 30 {
		t.Fatalf("goroutines grew by %d (%d -> %d) for %d gateways; pool should stay bounded", delta, beforeGoroutines, peakGoroutines, numGateways)
	}
	if got := len(telemetry.accepted()); got != numGateways {
		t.Fatalf("accepted=%d, want %d", got, numGateways)
	}
}

func TestSingleWorkerDoesNotHeadOfLineBlockHealthyGatewaysDuringRetries(t *testing.T) {
	var allowGatewayA atomic.Bool
	gatewayAFailed := make(chan struct{}, 1)
	telemetry := &fakeTelemetry{
		failHook: func(_ int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourceID == testGatewayA && !allowGatewayA.Load() {
				select {
				case gatewayAFailed <- struct{}{}:
				default:
				}
				return errors.New("gateway A transient dependency error")
			}
			return nil
		},
	}
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    8,
		WorkerCount:      1,
	}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 30 * time.Millisecond }

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gatewayAFailed:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway A never entered retry")
	}

	if err := runtime.enqueue(ctx, nil, publish(testGatewayB, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	acks.await(t, messageID(2))

	allowGatewayA.Store(true)
	acks.await(t, messageID(1))
}

func TestUplinkWorkerRecoversFromPanicWithoutLosingSubsequentBatches(t *testing.T) {
	var logBuf bytes.Buffer
	logger := observability.NewJSONLogger(&logBuf, slog.LevelInfo)
	metrics := observability.NewRegistry()

	telemetry := &fakeTelemetry{
		failHook: func(_ int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourcePosition.Offset == 1 {
				panic("secret-uplink-panic-payload")
			}
			return nil
		},
	}
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    8,
		WorkerCount:      1,
	}, newTestProcessor(t, newFakeIdentities(), telemetry), logger, metrics)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	waitCondition(t, 2*time.Second, func() bool {
		return strings.Contains(logBuf.String(), `"background_goroutine_panicked"`)
	}, "worker panic recovery log")

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(testGatewayB, 3), acks.ack(messageID(3))); err != nil {
		t.Fatal(err)
	}
	acks.await(t, messageID(2))
	acks.await(t, messageID(3))

	if strings.Contains(logBuf.String(), "secret-uplink-panic-payload") {
		t.Fatalf("panic payload leaked into log: %s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), `"component":"connectivity.uplink_worker"`) || !strings.Contains(logBuf.String(), `"error":"[REDACTED]"`) {
		t.Fatalf("unexpected panic log format: %s", logBuf.String())
	}
}

func TestIdleGatewayMailboxesAreReapedAfterTimeout(t *testing.T) {
	telemetry := &fakeTelemetry{}
	runtime := newTestRuntime(t, telemetry, 8)
	runtime.idleTimeout = 25 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acksA := newAckLog()
	acksB := newAckLog()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acksA.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(testGatewayB, 2), acksB.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	acksA.await(t, messageID(1))
	acksB.await(t, messageID(2))

	waitCondition(t, 2*time.Second, func() bool {
		runtime.queuesMu.Lock()
		defer runtime.queuesMu.Unlock()
		return len(runtime.queues) == 0 && len(runtime.states) == 0
	}, "idle gateway mailboxes reaped")

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 3), acksA.ack(messageID(3))); err != nil {
		t.Fatal(err)
	}
	acksA.await(t, messageID(3))
}

func TestTrackedGatewayQueuesCapEvictsIdleEntriesFirst(t *testing.T) {
	telemetry := &fakeTelemetry{}
	runtime := newTestRuntime(t, telemetry, 4)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()

	oldestIdleID := "018f3e00-9999-7000-8000-000000000000"
	now := time.Now()
	runtime.queuesMu.Lock()
	for index := 0; index < maxTrackedGatewayQueues; index++ {
		id := fmt.Sprintf("018f3e00-9999-7000-8000-%012d", index)
		ch := make(chan queuedPublish, 4)
		state := &gatewayMailboxState{
			queue:      ch,
			lastActive: now.Add(time.Duration(index) * time.Millisecond),
			inFlight:   index != 0,
		}
		runtime.queues[id] = ch
		runtime.states[id] = state
	}
	runtime.queuesMu.Unlock()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatalf("expected idle entry eviction to admit new gateway, got err=%v", err)
	}
	acks.await(t, messageID(1))

	runtime.queuesMu.Lock()
	_, oldestStillPresent := runtime.queues[oldestIdleID]
	total := len(runtime.queues)
	runtime.queuesMu.Unlock()
	if oldestStillPresent {
		t.Fatalf("expected oldest idle gateway %s to be evicted", oldestIdleID)
	}
	if total > maxTrackedGatewayQueues {
		t.Fatalf("tracked queues=%d exceeds cap %d", total, maxTrackedGatewayQueues)
	}

	runtime.queuesMu.Lock()
	for _, state := range runtime.states {
		state.inFlight = true
	}
	runtime.queuesMu.Unlock()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayB, 2), acks.ack(messageID(2))); err == nil {
		t.Fatal("expected error when all 4096 tracked gateway queues are active")
	}
}
