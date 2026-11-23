package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
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

func TestGatewayOverflowSpillsToDiskAndShedsWithoutBlocking(t *testing.T) {
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
	}
	overflowDir := t.TempDir()
	runtime, err := NewRuntime(Config{
		MQTT:                MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime:    TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:       2,
		OverflowMaxMessages: 3,
		OverflowDir:         overflowDir,
	}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
	acks := newAckLog()

	// Message 1 holds the worker; 2..3 fill RAM (2); 4..6 spill to disk (3); 7 sheds without blocking or acking.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered
	for index := 2; index <= 6; index++ {
		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, index), acks.ack(messageID(index))); err != nil {
			t.Fatal(err)
		}
	}
	// Custody ACKs happen immediately after durable WAL write without waiting on the worker.
	for index := 1; index <= 6; index++ {
		acks.await(t, messageID(index))
	}

	spoolPath := filepath.Join(overflowDir, safeGatewayFileName(testGatewayA))
	info, err := os.Stat(spoolPath)
	if err != nil || info.Size() <= walHeaderSize {
		t.Fatalf("expected non-empty overflow WAL spool at %s, stat=(%v, %v)", spoolPath, info, err)
	}

	shedDone := make(chan error, 1)
	go func() {
		shedDone <- runtime.enqueue(ctx, nil, publish(testGatewayA, 7), acks.ack(messageID(7)))
	}()
	select {
	case err := <-shedDone:
		if err == nil {
			t.Fatal("expected shedding enqueue to return an error for broker redelivery")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("enqueue blocked instead of shedding when RAM and disk overflow were full")
	}

	recorder := httptest.NewRecorder()
	runtime.metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := recorder.Body.String()
	for _, want := range []string{
		`hvac_mqtt_gateway_queue_depth{gateway_id="` + testGatewayA + `"} 5`,
		`hvac_mqtt_gateway_overflow_depth{gateway_id="` + testGatewayA + `"} 3`,
		`hvac_mqtt_gateway_overflow_shed_total{gateway_id="` + testGatewayA + `"} 1`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("expected metric %q in:\n%s", want, metricsBody)
		}
	}

	close(telemetry.release)
	awaitQueued(t, runtime, testGatewayA, 0)
	if got := acks.snapshot(); len(got) != 6 {
		t.Fatalf("acked=%v, want 6 (shed message 7 must stay unacked)", got)
	}
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 6
	}, "all 6 custody-acked messages processed by downstream")
	accepted := telemetry.accepted()
	if len(accepted) != 6 {
		t.Fatalf("accepted=%d, want 6", len(accepted))
	}
	for index, obs := range accepted {
		if obs.SourcePosition.Offset != int64(index+1) {
			t.Fatalf("observation[%d] offset=%d, want FIFO %d", index, obs.SourcePosition.Offset, index+1)
		}
	}
	waitCondition(t, 2*time.Second, func() bool {
		stat, err := os.Stat(spoolPath)
		return err == nil && stat.Size() == walHeaderSize
	}, "WAL spool truncated back to header size after overflow drains")
}

func TestRuntimeStalledGatewayDoesNotBlockHealthyGateways(t *testing.T) {
	releaseA := make(chan struct{})
	enteredA := make(chan struct{}, 1)
	telemetry := &fakeTelemetry{
		failHook: func(_ int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourceID == testGatewayA {
				select {
				case enteredA <- struct{}{}:
				default:
				}
				<-releaseA
			}
			return nil
		},
	}
	overflowDir := t.TempDir()
	runtime, err := NewRuntime(Config{
		MQTT:                MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime:    TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:       2,
		OverflowMaxMessages: 3,
		OverflowDir:         overflowDir,
	}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		select {
		case <-releaseA:
		default:
			close(releaseA)
		}
		cancel()
		runtime.workers.Wait()
		runtime.closeMailboxes()
	})
	acks := newAckLog()

	// Stall Gateway A completely, fill RAM + disk overflow, and trigger shed messages.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack("a-1")); err != nil {
		t.Fatal(err)
	}
	<-enteredA
	for index := 2; index <= 8; index++ {
		done := make(chan error, 1)
		idx := index
		go func() {
			done <- runtime.enqueue(ctx, nil, publish(testGatewayA, idx), acks.ack(messageID(idx)))
		}()
		select {
		case err := <-done:
			if idx <= 6 && err != nil {
				t.Fatalf("Gateway A enqueue %d failed: %v", idx, err)
			}
			if idx >= 7 && err == nil {
				t.Fatalf("Gateway A enqueue %d should shed with error", idx)
			}
		case <-time.After(150 * time.Millisecond):
			t.Fatalf("Gateway A enqueue %d blocked reader loop", idx)
		}
	}

	// Gateway B must be processed immediately without waiting on stalled Gateway A.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayB, 100), acks.ack("b-100")); err != nil {
		t.Fatal(err)
	}
	acks.await(t, "a-1")
	for index := 2; index <= 6; index++ {
		acks.await(t, messageID(index))
	}
	acks.await(t, "b-100")

	close(releaseA)
	waitCondition(t, 3*time.Second, func() bool {
		return len(telemetry.accepted()) == 7
	}, "Gateway A queued + spooled messages + Gateway B drained after release")
}

func TestRuntimeSubscribesWithSharedPrefixWhenConfigured(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{}
	runtime := newTestRuntime(t, telemetry, 4)
	runtime.config.MQTT = broker.mqtt
	runtime.config.MQTT.SharedSubscriptionGroup = "connectivity"
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop")
		}
	})

	conn := broker.awaitConnection(t)
	awaitReady(t, runtime, true)
	want := []string{
		"$share/connectivity/hvac/v1/+/up/telemetry",
		"$share/connectivity/hvac/v1/+/up/event",
		"$share/connectivity/hvac/v1/+/up/status",
		"$share/connectivity/hvac/v1/+/up/shadow",
		"$share/connectivity/hvac/v1/+/up/ota",
		"$share/connectivity/hvac/v1/+/up/lifecycle",
	}
	if len(conn.subscriptions) != len(want) ||
		conn.subscriptions[0] != want[0] ||
		conn.subscriptions[1] != want[1] ||
		conn.subscriptions[2] != want[2] ||
		conn.subscriptions[3] != want[3] ||
		conn.subscriptions[4] != want[4] ||
		conn.subscriptions[5] != want[5] {
		t.Fatalf("broker subscriptions=%v, want %v", conn.subscriptions, want)
	}
}

func TestTransientFailureIsRetriedInOrderBeforeProcessingCompletes(t *testing.T) {
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
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 2
	}, "transient failures retried and delivered in FIFO order")
	observations := telemetry.accepted()
	if len(observations) != 2 || observations[0].SourcePosition.Offset != 1 || observations[1].SourcePosition.Offset != 2 {
		t.Fatalf("observations=%#v", observations)
	}
}

func TestGatewaysDoNotWaitForEachOther(t *testing.T) {
	telemetry := &fakeTelemetry{failures: 1_000_000}
	identities := newFakeIdentities()
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    8,
		OverflowDir:      t.TempDir(),
	}, newTestProcessor(t, identities, telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	t.Cleanup(runtime.closeMailboxes)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()
	unknownGateway := "018f3e00-4000-7000-8000-000000000999"
	// Gateway A keeps failing; a message from an unknown Gateway is still quarantined and processed immediately.
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(unknownGateway, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	acks.await(t, messageID(1))
	acks.await(t, messageID(2))
	waitCondition(t, 2*time.Second, func() bool {
		return len(identities.snapshotQuarantines()) == 1
	}, "unknown gateway quarantined while Gateway A keeps retrying")
}

func newTestRuntime(t *testing.T, telemetry RuntimeClient, capacity int) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    capacity,
		OverflowDir:      t.TempDir(),
	}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	t.Cleanup(runtime.closeMailboxes)
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
		mailbox, ok := runtime.queues[gatewayID]
		runtime.queuesMu.Unlock()
		if ok {
			memD, ovfD := mailbox.depth()
			if memD+ovfD == depth {
				return
			}
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
	// Custody ACKs arrive in strict enqueue order before downstream persistence finishes.
	for index := 1; index <= 4; index++ {
		acks.await(t, messageID(index))
	}

	close(telemetry.release)
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 10
	}, "coalesced backlog batches persisted in FIFO order")
	batches := telemetry.recordedBatches()
	if len(batches) != 2 || len(batches[0]) != 1 || len(batches[1]) != 9 {
		t.Fatalf("batches=%v, want [1 9] points across 2 AcceptObservationBatch calls", batchSizes(batches))
	}
	spoolPath := filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))
	waitCondition(t, 2*time.Second, func() bool {
		stat, err := os.Stat(spoolPath)
		return err == nil && stat.Size() == walHeaderSize
	}, "coalesced WAL spool truncated back to header after drain")
}

func TestMixedBatchPreservesFIFOOrderAndDoesNotReprocessAckedPrefixOnLaterRetry(t *testing.T) {
	identities := newFakeIdentities()
	acks := newAckLog()
	var sawM4Failure int
	m4Failed := make(chan struct{}, 1)
	var runtime *Runtime
	spoolSizeAtFailure := int64(0)

	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		failHook: func(call int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourcePosition.Offset == 5 && sawM4Failure == 0 {
				sawM4Failure++
				if stat, statErr := os.Stat(filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))); statErr == nil {
					spoolSizeAtFailure = stat.Size()
				}
				select {
				case m4Failed <- struct{}{}:
				default:
				}
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	var err error
	runtime, err = NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    16,
		OverflowDir:      t.TempDir(),
	}, newTestProcessor(t, identities, telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })

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

	if spoolSizeAtFailure <= walHeaderSize {
		t.Fatalf("expected WAL spool to hold pending M4 frame on failure (size=%d)", spoolSizeAtFailure)
	}
	spoolPath := filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))
	waitCondition(t, 2*time.Second, func() bool {
		stat, err := os.Stat(spoolPath)
		return err == nil && stat.Size() == walHeaderSize
	}, "mixed batch WAL spool truncated after M4 retry succeeded")
	if quarantines := identities.snapshotQuarantines(); len(quarantines) != 1 {
		t.Fatalf("quarantines=%d, want 1 (M2 must not be re-quarantined)", len(quarantines))
	}
	if events := telemetry.recordedEvents(); len(events) != 1 || events[0].MessageID != messageID(4) {
		t.Fatalf("events=%#v, want 1 event for M3", events)
	}
	if got := acks.snapshot(); len(got) != 5 {
		t.Fatalf("final acks=%v, want 5", got)
	}
	if accepted := telemetry.accepted(); len(accepted) != 3 {
		t.Fatalf("accepted telemetry points=%d, want 3 (M0, M1, M4)", len(accepted))
	}
}

func TestOversizedEnvelopeChunksAcrossBatchCallsBeforeSpoolCommit(t *testing.T) {
	acks := newAckLog()
	var runtime *Runtime
	var spoolSizeBeforeSecondChunk atomic.Int64
	telemetry := &fakeTelemetry{
		failHook: func(call int, batch []Observation) error {
			if call == 2 {
				if stat, err := os.Stat(filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))); err == nil {
					spoolSizeBeforeSecondChunk.Store(stat.Size())
				}
			}
			return nil
		},
	}
	runtime = newTestRuntime(t, telemetry, 8)
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
	spoolPath := filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))
	waitCondition(t, 2*time.Second, func() bool {
		stat, err := os.Stat(spoolPath)
		return err == nil && stat.Size() == walHeaderSize
	}, "oversized WAL spool truncated after both chunks completed")
	if size := spoolSizeBeforeSecondChunk.Load(); size <= walHeaderSize {
		t.Fatalf("spool truncated before second chunk completed: size=%d", size)
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 2 || len(batches[0]) != 512 || len(batches[1]) != 88 {
		t.Fatalf("chunk sizes=%v, want [512 88]", batchSizes(batches))
	}
}

func TestTransientBatchFailureRetriesInPlaceWithoutAdvancingWAL(t *testing.T) {
	acks := newAckLog()
	var runtime *Runtime
	var spoolSizesMu sync.Mutex
	var spoolSizesOnFailure []int64
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		failHook: func(call int, batch []Observation) error {
			if call == 2 || call == 3 {
				if stat, err := os.Stat(filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))); err == nil {
					spoolSizesMu.Lock()
					spoolSizesOnFailure = append(spoolSizesOnFailure, stat.Size())
					spoolSizesMu.Unlock()
				}
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	runtime = newTestRuntime(t, telemetry, 8)
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
	spoolPath := filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayA))
	waitCondition(t, 2*time.Second, func() bool {
		stat, err := os.Stat(spoolPath)
		return err == nil && stat.Size() == walHeaderSize
	}, "WAL spool truncated only after transient batch retries succeeded")
	spoolSizesMu.Lock()
	sizes := append([]int64(nil), spoolSizesOnFailure...)
	spoolSizesMu.Unlock()
	if len(sizes) != 2 {
		t.Fatalf("observed %d transient batch failures, want 2", len(sizes))
	}
	for attempt, sz := range sizes {
		if sz <= walHeaderSize {
			t.Fatalf("attempt %d WAL size=%d, want > %d while batch uncommitted", attempt, sz, walHeaderSize)
		}
	}
	batches := telemetry.recordedBatches()
	if len(batches) != 2 || len(batches[0]) != 1 || len(batches[1]) != 3 {
		t.Fatalf("batches=%v, want [1 3]", batchSizes(batches))
	}
}

func TestRealBrokerInflightWindowIsolationWithStalledAndSheddingGateway(t *testing.T) {
	broker := newFakeBroker(t)
	releaseA := make(chan struct{})
	enteredA := make(chan struct{}, 1)
	telemetry := &fakeTelemetry{
		failHook: func(_ int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourceID == testGatewayA {
				select {
				case enteredA <- struct{}{}:
				default:
				}
				<-releaseA
			}
			return nil
		},
	}
	t.Cleanup(func() {
		select {
		case <-releaseA:
		default:
			close(releaseA)
		}
	})

	runtime := newTestRuntime(t, telemetry, 1)
	runtime.config.MQTT = broker.mqtt
	runtime.config.OverflowMaxMessages = 2
	runtime.reconnectBackoff = func(int) time.Duration { return 50 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})

	first := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	// Gateway A holds its worker (1) and leaves capacity for RAM (1) + overflow (2) = 4 messages total
	// while a strict broker window (maxInflight=2) would deadlock without WAL custody ACK.
	first.deliverWithinWindow(t, 2, 2*time.Second, testGatewayA, 1, 1)
	<-enteredA
	first.deliverWithinWindow(t, 2, 2*time.Second, testGatewayA, 2, 2)
	first.deliverWithinWindow(t, 2, 2*time.Second, testGatewayB, 3, 100)
	first.deliverWithinWindow(t, 2, 2*time.Second, testGatewayA, 4, 3)
	first.deliverWithinWindow(t, 2, 2*time.Second, testGatewayB, 5, 101)
	first.deliverWithinWindow(t, 2, 2*time.Second, testGatewayA, 6, 4)

	first.awaitAcks(t, 1, 2, 3, 4, 5, 6)
	waitCondition(t, 2*time.Second, func() bool {
		countB := 0
		for _, obs := range telemetry.accepted() {
			if obs.SourceID == testGatewayB {
				countB++
			}
		}
		return countB == 2
	}, "Gateway B processed cleanly across shared broker window while Gateway A stalled")

	// Next Gateway A message exceeds QueueCapacity(1) + OverflowMaxMessages(2) -> shed & reconnect without ACK.
	first.deliverGateway(t, testGatewayA, 7, 5, false)
	first.awaitDisconnect(t, 3*time.Second)

	second := broker.awaitConnection(t)
	awaitReady(t, runtime, true)
	close(releaseA)
	waitCondition(t, 3*time.Second, func() bool {
		return runtime.Ready() && len(telemetry.accepted()) == 6
	}, "stalled Gateway A queue drained and second session ready after release")
	second.deliverGateway(t, testGatewayA, 7, 5, true)
	second.awaitAcks(t, 7)

	waitCondition(t, 3*time.Second, func() bool {
		return len(telemetry.accepted()) == 7
	}, "all 5 Gateway A + 2 Gateway B messages processed after reconnect")
}

func TestRealBrokerClosesConnectionOnNonShedSpoolWriteError(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{}
	runtime := newTestRuntime(t, telemetry, 4)
	runtime.config.MQTT = broker.mqtt
	runtime.reconnectBackoff = func(int) time.Duration { return 50 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})

	first := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	// Force Gateway B's spool open to fail (path is a directory, simulating disk/permission fault).
	blockedPath := filepath.Join(runtime.overflowDir, safeGatewayFileName(testGatewayB))
	if err := os.MkdirAll(blockedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	first.deliverGateway(t, testGatewayB, 1, 100, false)
	first.awaitDisconnect(t, 3*time.Second)

	// Clear the fault so the reconnected session processes redelivered packet without a wedged acksTracker.
	if err := os.Remove(blockedPath); err != nil {
		t.Fatal(err)
	}
	second := broker.awaitConnection(t)
	waitCondition(t, 2*time.Second, func() bool {
		return runtime.Ready()
	}, "second session ready after reconnect")
	second.deliverGateway(t, testGatewayB, 1, 100, true)
	second.deliverGateway(t, testGatewayA, 2, 1, false)
	second.awaitAcks(t, 1, 2)
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
		"$share/connectivity/hvac/v1/" + testGatewayA + "/up/telemetry",
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
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })

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

	waitCondition(t, 5*time.Second, func() bool {
		return len(telemetry.accepted()) == numGateways
	}, "bounded worker pool processed all 120 gateways")
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
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
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
	acks.await(t, messageID(1))
	acks.await(t, messageID(2))
	waitCondition(t, 2*time.Second, func() bool {
		for _, obs := range telemetry.accepted() {
			if obs.SourceID == testGatewayB {
				return true
			}
		}
		return false
	}, "gateway B processed while single worker awaits gateway A retry timer")

	allowGatewayA.Store(true)
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 2
	}, "gateway A retried and delivered after unblock")
}

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestUplinkWorkerRecoversFromPanicWithoutLosingSubsequentBatches(t *testing.T) {
	var logBuf syncLogBuffer
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
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
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
	acks.await(t, messageID(1))
	acks.await(t, messageID(2))
	acks.await(t, messageID(3))
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 2
	}, "subsequent batches processed after worker panic recovery")

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
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
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
		return len(telemetry.accepted()) == 2
	}, "idle gateway initial batch processed before reap")

	waitCondition(t, 2*time.Second, func() bool {
		runtime.queuesMu.Lock()
		defer runtime.queuesMu.Unlock()
		return len(runtime.queues) == 0 && len(runtime.states) == 0
	}, "idle gateway mailboxes reaped")

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 3), acksA.ack(messageID(3))); err != nil {
		t.Fatal(err)
	}
	acksA.await(t, messageID(3))
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 3
	}, "re-enqueued message processed after idle reap")
}

func TestTrackedGatewayQueuesCapEvictsIdleEntriesFirst(t *testing.T) {
	telemetry := &fakeTelemetry{}
	runtime := newTestRuntime(t, telemetry, 4)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
	acks := newAckLog()

	oldestIdleID := "018f3e00-9999-7000-8000-000000000000"
	now := time.Now()
	dir := t.TempDir()
	runtime.queuesMu.Lock()
	for index := 0; index < maxTrackedGatewayQueues; index++ {
		id := fmt.Sprintf("018f3e00-9999-7000-8000-%012d", index)
		mb := newGatewayMailbox(id, 4, 16, 1<<20, dir)
		state := &gatewayMailboxState{
			mailbox:    mb,
			lastActive: now.Add(time.Duration(index) * time.Millisecond),
			inFlight:   index != 0,
		}
		runtime.queues[id] = mb
		runtime.states[id] = state
	}
	runtime.queuesMu.Unlock()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatalf("expected idle entry eviction to admit new gateway, got err=%v", err)
	}
	acks.await(t, messageID(1))

	waitCondition(t, 2*time.Second, func() bool {
		runtime.queuesMu.Lock()
		defer runtime.queuesMu.Unlock()
		state, ok := runtime.states[testGatewayA]
		return ok && !state.inFlight
	}, "gateway A turn completion")

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

func TestRuntimeRateLimited429IsolatesGatewayAndPreservesReadiness(t *testing.T) {
	var allowGatewayA atomic.Bool
	var attemptsA atomic.Int32
	gatewayARateLimited := make(chan struct{}, 1)

	telemetry := &fakeTelemetry{
		failHook: func(_ int, batch []Observation) error {
			if len(batch) > 0 && batch[0].SourceID == testGatewayA && !allowGatewayA.Load() {
				attemptsA.Add(1)
				select {
				case gatewayARateLimited <- struct{}{}:
				default:
				}
				return &RateLimitedError{Label: "S2 observation batch", RetryAfter: 50 * time.Millisecond}
			}
			return nil
		},
	}
	runtime := newTestRuntime(t, telemetry, 8)
	runtime.rateLimitFloor = 10 * time.Millisecond
	runtime.recordConnectionState(nil, true, true, nil)

	defaultRuntime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-floor-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    4,
		OverflowDir:      t.TempDir(),
	}, newTestProcessor(t, newFakeIdentities(), &fakeTelemetry{
		failHook: func(int, []Observation) error {
			return &RateLimitedError{Label: "S2 observation batch"}
		},
	}), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(defaultRuntime.closeMailboxes)
	defaultRuntime.recordConnectionState(nil, true, true, nil)
	pubA := publish(testGatewayA, 1)
	_, defaultDelay, prefixErr := defaultRuntime.processPrefix(context.Background(), testGatewayA, []queuedPublish{{packet: pubA}}, []UplinkMessage{{Topic: pubA.Topic, Payload: pubA.Payload}}, 1)
	if !errors.Is(prefixErr, ErrTelemetryRateLimited) || defaultDelay != defaultRateLimitRetryFloor || !defaultRuntime.Ready() {
		t.Fatalf("default rate-limit processPrefix=(%s, %v, ready=%v), want delay=%s ready=true", defaultDelay, prefixErr, defaultRuntime.Ready(), defaultRateLimitRetryFloor)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait() })
	acks := newAckLog()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gatewayARateLimited:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway A never hit 429")
	}

	if !runtime.Ready() {
		t.Fatal("runtime.Ready() turned false after per-gateway 429 rate limit")
	}

	if err := runtime.enqueue(ctx, nil, publish(testGatewayB, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	acks.await(t, messageID(1))
	acks.await(t, messageID(2))

	waitCondition(t, 2*time.Second, func() bool {
		for _, obs := range telemetry.accepted() {
			if obs.SourceID == testGatewayB {
				return true
			}
		}
		return false
	}, "gateway B succeeded while gateway A backed off on 429")
	if !runtime.Ready() {
		t.Fatal("runtime.Ready() turned false during gateway A 429 backoff")
	}

	allowGatewayA.Store(true)
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 2
	}, "gateway A succeeded after 429 cleared")
	if attemptsA.Load() < 1 {
		t.Fatalf("expected at least 1 429 attempt on gateway A, got %d", attemptsA.Load())
	}

	rec := httptest.NewRecorder()
	runtime.metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `hvac_mqtt_message_retries_total{reason_family="rate_limited"}`) {
		t.Fatalf("expected rate_limited retry counter in metrics:\n%s", rec.Body.String())
	}
}

func TestRuntimeSubscribeRetriesAndSelfHealsOnSubackFailure(t *testing.T) {
	mqttCfg, serverTLS := writeTestPKI(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	mqttCfg.BrokerURL = "tls://" + listener.Addr().String()
	mqttCfg.ClientID = "connectivity-suback-heal"

	var connSeq atomic.Int32
	var conn1Subscribes atomic.Int32
	conn1Closed := make(chan struct{})
	conn2Subscribed := make(chan struct{})

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			seq := connSeq.Add(1)
			go func(c net.Conn, s int32) {
				defer c.Close()
				connectPkt, readErr := packets.ReadPacket(c)
				if readErr != nil || connectPkt.Type != packets.CONNECT {
					return
				}
				connack := packets.NewControlPacket(packets.CONNACK)
				connack.Content.(*packets.Connack).SessionPresent = true
				if _, writeErr := connack.WriteTo(c); writeErr != nil {
					return
				}
				for {
					pkt, pktErr := packets.ReadPacket(c)
					if pktErr != nil {
						if s == 1 {
							close(conn1Closed)
						}
						return
					}
					switch pkt.Type {
					case packets.SUBSCRIBE:
						sub := pkt.Content.(*packets.Subscribe)
						suback := packets.NewControlPacket(packets.SUBACK)
						sa := suback.Content.(*packets.Suback)
						sa.PacketID = sub.PacketID
						for range sub.Subscriptions {
							if s == 1 {
								sa.Reasons = append(sa.Reasons, 0x87)
							} else {
								sa.Reasons = append(sa.Reasons, 1)
							}
						}
						if s == 1 {
							conn1Subscribes.Add(1)
						}
						_, _ = suback.WriteTo(c)
						if s >= 2 {
							select {
							case <-conn2Subscribed:
							default:
								close(conn2Subscribed)
							}
						}
					case packets.PINGREQ:
						_, _ = packets.NewControlPacket(packets.PINGRESP).WriteTo(c)
					}
				}
			}(conn, seq)
		}
	}()

	runtime := newTestRuntime(t, &fakeTelemetry{}, 4)
	runtime.config.MQTT = mqttCfg
	runtime.reconnectBackoff = func(int) time.Duration { return 15 * time.Millisecond }
	runtime.subscribeRetryDelays = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop")
		}
	})

	select {
	case <-conn1Closed:
	case <-time.After(3 * time.Second):
		t.Fatal("first connection did not close after repeated SUBACK failures")
	}
	if got := conn1Subscribes.Load(); got != 3 {
		t.Fatalf("conn1 subscribe attempts=%d, want 3", got)
	}

	select {
	case <-conn2Subscribed:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not reconnect and subscribe on second connection")
	}
	waitCondition(t, 2*time.Second, runtime.Ready, "runtime Ready() after subscribe self-heal")

	rec := httptest.NewRecorder()
	runtime.metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := rec.Body.String()
	for _, want := range []string{
		`hvac_mqtt_subscriptions_total{outcome="failed"} 1`,
		`hvac_mqtt_subscriptions_total{outcome="success"} 1`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("missing %q in metrics:\n%s", want, metricsBody)
		}
	}
}

func scrapeMetricsText(registry *observability.Registry) string {
	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func TestGatewayGaugeLeakFixedOnReapEvictAndClose(t *testing.T) {
	t.Run("idle reap deletes gateway queue and overflow gauges", func(t *testing.T) {
		telemetry := &fakeTelemetry{}
		runtime := newTestRuntime(t, telemetry, 8)
		runtime.idleTimeout = 25 * time.Millisecond

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
		acks := newAckLog()

		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
			t.Fatal(err)
		}
		acks.await(t, messageID(1))
		waitCondition(t, 2*time.Second, func() bool {
			return len(telemetry.accepted()) == 1
		}, "gateway A message processed")

		waitCondition(t, 2*time.Second, func() bool {
			card := runtime.metrics.SeriesCardinality()
			return card["hvac_mqtt_gateway_queue_depth"] == 0 && card["hvac_mqtt_gateway_overflow_depth"] == 0
		}, "reaped gateway gauges deleted from cardinality")

		body := scrapeMetricsText(runtime.metrics)
		if strings.Contains(body, `gateway_id="`+testGatewayA+`"`) {
			t.Fatalf("expected reaped gateway %s removed from /metrics, got:\n%s", testGatewayA, body)
		}
	})

	t.Run("cap eviction and closeMailboxes delete gateway gauges", func(t *testing.T) {
		telemetry := &fakeTelemetry{}
		runtime := newTestRuntime(t, telemetry, 4)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
		acks := newAckLog()

		oldestIdleID := "018f3e00-9999-7000-8000-000000000000"
		now := time.Now()
		dir := t.TempDir()
		runtime.queuesMu.Lock()
		for index := 0; index < maxTrackedGatewayQueues; index++ {
			id := fmt.Sprintf("018f3e00-9999-7000-8000-%012d", index)
			mb := newGatewayMailbox(id, 4, 16, 1<<20, dir)
			state := &gatewayMailboxState{
				mailbox:    mb,
				lastActive: now.Add(time.Duration(index) * time.Millisecond),
				inFlight:   index != 0,
			}
			runtime.queues[id] = mb
			runtime.states[id] = state
		}
		runtime.queuesMu.Unlock()
		runtime.recordQueueDepth(oldestIdleID, 0)
		runtime.recordOverflowDepth(oldestIdleID, 0)

		if card := runtime.metrics.SeriesCardinality(); card["hvac_mqtt_gateway_queue_depth"] != 1 || card["hvac_mqtt_gateway_overflow_depth"] != 1 {
			t.Fatalf("unexpected pre-eviction gauge cardinality: %#v", card)
		}

		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
			t.Fatalf("expected idle eviction to admit gateway A: %v", err)
		}
		acks.await(t, messageID(1))

		bodyAfterEvict := scrapeMetricsText(runtime.metrics)
		if strings.Contains(bodyAfterEvict, `gateway_id="`+oldestIdleID+`"`) {
			t.Fatalf("evicted gateway %s leaked in /metrics:\n%s", oldestIdleID, bodyAfterEvict)
		}
		if !strings.Contains(bodyAfterEvict, `gateway_id="`+testGatewayA+`"`) {
			t.Fatalf("expected admitted gateway %s gauge in /metrics:\n%s", testGatewayA, bodyAfterEvict)
		}

		cancel()
		runtime.workers.Wait()
		runtime.closeMailboxes()

		cardAfterClose := runtime.metrics.SeriesCardinality()
		if cardAfterClose["hvac_mqtt_gateway_queue_depth"] != 0 || cardAfterClose["hvac_mqtt_gateway_overflow_depth"] != 0 {
			t.Fatalf("expected all gateway gauges deleted on closeMailboxes, got %#v", cardAfterClose)
		}
		bodyAfterClose := scrapeMetricsText(runtime.metrics)
		if strings.Contains(bodyAfterClose, `gateway_id="`+testGatewayA+`"`) {
			t.Fatalf("closed gateway %s still present in /metrics:\n%s", testGatewayA, bodyAfterClose)
		}
		if !strings.Contains(bodyAfterClose, "hvac_mqtt_processing_queue_depth 0\n") {
			t.Fatalf("expected hvac_mqtt_processing_queue_depth 0 after closeMailboxes:\n%s", bodyAfterClose)
		}
	})
}

func TestRuntimeCatalogMetricsOnlineGatewaysTraceLogsAndPacketHook(t *testing.T) {
	var logBuf syncLogBuffer
	logger := observability.NewJSONLogger(&logBuf, slog.LevelDebug)
	registry := observability.NewRegistry()
	tracer := observability.NewTracer("mqtt-telemetry-adapter", nil)

	var online atomic.Int32
	var tappedMu sync.Mutex
	var tapped []string

	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
	}
	processor, err := NewProcessor(
		newFakeIdentities(),
		telemetry,
		WithProcessorTracer(tracer),
		WithGatewayPresenceHook(func(_ string, isOnline bool) {
			if isOnline {
				online.Store(1)
			} else {
				online.Store(0)
			}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	overflowDir := t.TempDir()
	runtime, err := NewRuntime(
		Config{
			MQTT:                MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-catalog", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
			TelemetryRuntime:    TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
			QueueCapacity:       1,
			OverflowMaxMessages: 1,
			OverflowDir:         overflowDir,
		},
		processor,
		logger,
		registry,
		WithPacketHook(func(direction, channel, gatewayID, topic, traceID string, _ []byte) {
			tappedMu.Lock()
			tapped = append(tapped, fmt.Sprintf("%s:%s:%s:%s", direction, channel, gatewayID, traceID))
			tappedMu.Unlock()
		}),
		WithOnlineGatewayCount(func() int {
			return int(online.Load())
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })

	initialBody := scrapeMetricsText(registry)
	for _, want := range []string{
		"hvac_mqtt_processing_queue_capacity 2\n",
		"hvac_mqtt_processing_queue_depth 0\n",
		"hvac_mqtt_online_gateways 0\n",
	} {
		if !strings.Contains(initialBody, want) {
			t.Fatalf("missing initial metric %q in:\n%s", want, initialBody)
		}
	}

	acks := newAckLog()
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 3), acks.ack(messageID(3))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 4), acks.ack(messageID(4))); err == nil {
		t.Fatal("expected M4 to shed")
	}
	if err := runtime.enqueue(ctx, nil, &paho.Publish{Topic: "hvac/v1/" + testGatewayB + "/up/state", Payload: []byte(`{}`), QoS: 1}, acks.ack("q-1")); err != nil {
		t.Fatal(err)
	}

	queuedBody := scrapeMetricsText(registry)
	for _, want := range []string{
		`hvac_mqtt_messages_received_total{outcome="queued"} 4`,
		`hvac_mqtt_messages_received_total{outcome="dropped"} 1`,
		"hvac_mqtt_processing_queue_depth 2\n",
		"hvac_mqtt_replay_queue_depth 0\n",
	} {
		if !strings.Contains(queuedBody, want) {
			t.Fatalf("missing queued metric %q in:\n%s", want, queuedBody)
		}
	}

	close(telemetry.release)
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 3
	}, "all queued telemetry processed")
	waitCondition(t, 2*time.Second, func() bool {
		body := scrapeMetricsText(registry)
		return strings.Contains(body, `hvac_mqtt_message_processing_duration_seconds_count{outcome="quarantined"} 1`) &&
			strings.Contains(body, "hvac_mqtt_online_gateways 1\n") &&
			strings.Contains(body, "hvac_mqtt_processing_queue_depth 0\n")
	}, "quarantined duration, online gateways, and drained queue depth recorded")

	corruptMB := newGatewayMailbox(testGatewayB, 1, 4, 1<<20, t.TempDir())
	t.Cleanup(func() { corruptMB.close(true) })
	for i := 1; i <= 3; i++ {
		if _, enqErr := corruptMB.enqueue(publish(testGatewayB, i)); enqErr != nil {
			t.Fatal(enqErr)
		}
	}
	b1, _, _, _ := corruptMB.popBatch(1)
	corruptMB.markConsumed(b1[0].endSeq)
	var badCRC [4]byte
	binary.BigEndian.PutUint32(badCRC[:], crc32.ChecksumIEEE([]byte("corrupt")))
	if _, writeErr := corruptMB.spoolFile.WriteAt(badCRC[:], walHeaderSize+2*b1[0].endSeq+4); writeErr != nil {
		t.Fatal(writeErr)
	}
	b2, _, _, _ := corruptMB.popBatch(1)
	corruptMB.markConsumed(b2[0].endSeq)
	var endSeq int64
	runtime.runGatewayTurnTracked(ctx, gatewayTurn{gatewayID: testGatewayB, mailbox: corruptMB}, &endSeq, nil)

	finalBody := scrapeMetricsText(registry)
	for _, want := range []string{
		"hvac_mqtt_spool_read_errors_total 1\n",
		`hvac_mqtt_messages_processed_total{outcome="dropped"} 1`,
		`hvac_mqtt_messages_processed_total{outcome="quarantined"} 1`,
	} {
		if !strings.Contains(finalBody, want) {
			t.Fatalf("missing final metric %q in:\n%s", want, finalBody)
		}
	}

	logs := logBuf.String()
	if !strings.Contains(logs, `"msg":"mqtt_uplink_message_processed"`) || !strings.Contains(logs, `"trace_id":"`) {
		t.Fatalf("expected trace_id on mqtt_uplink_message_processed log: %s", logs)
	}
	if !strings.Contains(logs, `"error_code":"MQTT_UPLINK_MESSAGE_QUARANTINED"`) {
		t.Fatalf("expected error_code on quarantine log: %s", logs)
	}
	if !strings.Contains(logs, `"reason_code":"GATEWAY_OVERFLOW_SHED"`) {
		t.Fatalf("expected reason_code on overflow shed log: %s", logs)
	}
	if !strings.Contains(logs, `"error_code":"MQTT_UPLINK_OVERFLOW_READ_FAILED"`) {
		t.Fatalf("expected error_code on spool read failure log: %s", logs)
	}

	tappedMu.Lock()
	tappedCount := len(tapped)
	tappedMu.Unlock()
	if tappedCount < 4 {
		t.Fatalf("expected WithPacketHook to capture telemetry and quarantine packets, got %d", tappedCount)
	}
}

func TestRuntimeGracefulDrainFinishesInFlightAndBufferedMessagesOnCancel(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
	}
	lister := &fakeCoverageLister{deviceIDs: []string{testDeviceID}}
	runtime, err := NewRuntime(
		Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    8,
			OverflowDir:      t.TempDir(),
		},
		newTestProcessor(t, newFakeIdentities(), telemetry),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		observability.NewRegistry(),
		WithCoverageReporter(lister, telemetry),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.coverageInterval = time.Hour

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() {
		runErr <- runtime.Run(ctx)
	}()

	conn := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	conn.deliverGateway(t, testGatewayA, 1, 1, false)
	<-telemetry.entered
	conn.deliverGateway(t, testGatewayA, 2, 2, false)
	conn.deliverGateway(t, testGatewayA, 3, 3, false)
	conn.awaitAcks(t, 1, 2, 3)

	cancel()
	go func() {
		time.Sleep(40 * time.Millisecond)
		close(telemetry.release)
	}()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not finish graceful drain within 5s")
	}

	accepted := telemetry.accepted()
	if len(accepted) != 3 {
		t.Fatalf("accepted=%d during graceful drain, want 3", len(accepted))
	}
	for i, obs := range accepted {
		if obs.SourcePosition.Offset != int64(i+1) {
			t.Fatalf("accepted[%d] offset=%d, want %d", i, obs.SourcePosition.Offset, i+1)
		}
	}

	telemetry.mu.Lock()
	reports := append([]SourceCoverageReport(nil), telemetry.coverageReports...)
	telemetry.mu.Unlock()
	if len(reports) == 0 {
		t.Fatal("expected coverage reports including shutdown SOURCE_UNAVAILABLE")
	}
	last := reports[len(reports)-1]
	if last.Available || last.ReasonCode == nil || *last.ReasonCode != "SOURCE_UNAVAILABLE" {
		t.Fatalf("last coverage report=%+v, want SOURCE_UNAVAILABLE", last)
	}
}

func TestRuntimeGracefulDrainTimeoutPreservesPersistentWAL(t *testing.T) {
	broker := newFakeBroker(t)
	entered := make(chan struct{}, 2)
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: entered,
	}
	overflowDir := t.TempDir()
	runtime, err := NewRuntime(
		Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    4,
			OverflowDir:      overflowDir,
		},
		newTestProcessor(t, newFakeIdentities(), telemetry),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		observability.NewRegistry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.drainTimeout = 80 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() {
		runErr <- runtime.Run(ctx)
	}()

	conn := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	conn.deliverGateway(t, testGatewayA, 1, 1, false)
	<-entered
	conn.deliverGateway(t, testGatewayA, 2, 2, false)
	conn.awaitAcks(t, 1, 2)

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error after bounded drain timeout: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run exceeded bounded drain timeout")
	}

	// Persistent OverflowDir (ownedOverflowDir == false) must retain unconsumed WAL frames for recovery.
	recoveredTelemetry := &fakeTelemetry{}
	recoveredRuntime, err := NewRuntime(
		Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    4,
			OverflowDir:      overflowDir,
		},
		newTestProcessor(t, newFakeIdentities(), recoveredTelemetry),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		observability.NewRegistry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	recCtx, recCancel := context.WithCancel(t.Context())
	t.Cleanup(func() { recCancel(); recoveredRuntime.workers.Wait(); recoveredRuntime.closeMailboxes() })
	if err := recoveredRuntime.recoverOverflowMailboxes(recCtx); err != nil {
		t.Fatalf("recoverOverflowMailboxes failed: %v", err)
	}
	waitCondition(t, 2*time.Second, func() bool {
		return len(recoveredTelemetry.accepted()) == 2
	}, "preserved WAL entries recovered and drained on restart")
}

func TestRuntimeWithSharedSessionRegistersRoutesMirrorsReadyAndLeavesSessionOpenOnCancel(t *testing.T) {
	broker := newFakeBroker(t)
	telemetry := &fakeTelemetry{}

	sessionCtx, sessionCancel := context.WithCancel(t.Context())
	defer sessionCancel()

	session, err := mqttclient.NewSharedSession(sessionCtx, mqttclient.SharedSessionConfig{
		BrokerURL: broker.mqtt.BrokerURL,
		ClientID:  broker.mqtt.ClientID,
		TLS: &mqttclient.TLSConfig{
			CACertFile:     broker.mqtt.CAFile,
			ClientCertFile: broker.mqtt.CertFile,
			ClientKeyFile:  broker.mqtt.KeyFile,
			ServerName:     broker.mqtt.ServerName,
		},
		ConnectRetryDelay:    50 * time.Millisecond,
		SubscribeRetryDelays: []time.Duration{25 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewSharedSession failed: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close(context.Background())
	})

	// Another in-process consumer shares the exact same MQTT session.
	otherMsgs := make(chan string, 4)
	unregOther, err := session.RegisterRoute("rpc-reply-consumer", []string{"hvac/v1/+/down/reply"}, func(pkt *paho.Publish) {
		otherMsgs <- string(pkt.Payload)
	})
	if err != nil {
		t.Fatalf("RegisterRoute failed: %v", err)
	}
	defer func() { _ = unregOther(context.Background()) }()

	runtime, err := NewRuntime(
		Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    4,
		},
		newTestProcessor(t, newFakeIdentities(), telemetry),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		observability.NewRegistry(),
		WithSharedSession(session),
	)
	if err != nil {
		t.Fatalf("NewRuntime(WithSharedSession) failed: %v", err)
	}

	ingressCtx, ingressCancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ingressCtx) }()

	conn := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	// Deliver telemetry through shared session -> adapter processes and ACKs it.
	conn.deliver(t, 1, 1, false)
	conn.awaitAcks(t, 1)
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 1
	}, "telemetry processed via shared session")

	// Cancel ONLY adapter's ingressCtx: adapter must unregister its uplink route and drain,
	// marking runtime.Ready() false WITHOUT closing the shared session for other consumers.
	ingressCancel()
	select {
	case runErr := <-stopped:
		if runErr != nil {
			t.Fatalf("Runtime.Run(ingressCtx canceled) returned unexpected error: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Runtime.Run did not drain and exit on ingressCtx cancel")
	}

	if runtime.Ready() {
		t.Fatal("expected runtime.Ready() == false after ingressCtx cancellation")
	}
	sessionState := session.State()
	if sessionState.Closed || !sessionState.Connected {
		t.Fatalf("expected SharedSession to remain open & connected after adapter cancel, got %+v", sessionState)
	}

	// Verify the other route still works on the shared session after adapter stopped.
	replyPkt := packets.NewControlPacket(packets.PUBLISH)
	rep := replyPkt.Content.(*packets.Publish)
	rep.Topic = "hvac/v1/" + testGatewayA + "/down/reply"
	rep.Payload = []byte(`{"ok":true}`)
	rep.QoS = 1
	rep.PacketID = 2
	if err := conn.write(replyPkt); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-otherMsgs:
		if msg != `{"ok":true}` {
			t.Fatalf("unexpected reply payload on sibling route: %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sibling route on SharedSession stopped receiving after adapter shutdown")
	}
}

func TestRuntimeRecoveredWALBacklogDrainsWithoutRateLimitDrop(t *testing.T) {
	overflowDir := t.TempDir()
	const backlogCount = 500

	spool := newGatewayMailbox(testGatewayA, 2, 1000, 32<<20, overflowDir)
	for i := 1; i <= backlogCount; i++ {
		res, err := spool.enqueue(publish(testGatewayA, i))
		if err != nil {
			t.Fatalf("pre-populate WAL message %d failed: %v", i, err)
		}
		if res.shed {
			t.Fatalf("unexpected shed pre-populating WAL message %d", i)
		}
	}
	spool.close(false)

	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	processor, err := NewProcessor(identities, telemetry, WithGatewayRateLimit(5, 2))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-wal-ratelimit-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    4,
		OverflowDir:      overflowDir,
	}, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}

	recCtx, recCancel := context.WithCancel(t.Context())
	t.Cleanup(func() { recCancel(); runtime.workers.Wait(); runtime.closeMailboxes() })

	if err := runtime.recoverOverflowMailboxes(recCtx); err != nil {
		t.Fatalf("recoverOverflowMailboxes failed: %v", err)
	}

	waitCondition(t, 10*time.Second, func() bool {
		return len(telemetry.accepted()) == backlogCount
	}, "recovered 500-message WAL backlog drained despite tight ingress rate limit")

	accepted := telemetry.accepted()
	for i, obs := range accepted {
		wantOffset := int64(i + 1)
		if obs.SourceID != testGatewayA || obs.SourcePosition.Offset != wantOffset {
			t.Fatalf("observation[%d]=(source=%s, offset=%d), want (source=%s, offset=%d)", i, obs.SourceID, obs.SourcePosition.Offset, testGatewayA, wantOffset)
		}
	}
	if quarantines := identities.snapshotQuarantines(); len(quarantines) != 0 {
		t.Fatalf("expected zero RATE_LIMITED quarantines for recovered WAL backlog, got %+v", quarantines)
	}
	metricsText := scrapeMetricsText(runtime.metrics)
	if strings.Contains(metricsText, "hvac_mqtt_gateway_rate_limited_total") {
		t.Fatalf("unexpected rate-limit metric during recovered WAL drain:\n%s", metricsText)
	}
}

type blockingQuarantineIdentities struct {
	*fakeIdentities
	started chan struct{}
	release chan struct{}
}

func (b *blockingQuarantineIdentities) QuarantineUplink(ctx context.Context, quarantine UplinkQuarantine) error {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.fakeIdentities.QuarantineUplink(ctx, quarantine)
}

func TestRuntimeIngressRateLimitDropsExcessAcksWithoutBlockingOrDisconnectingHealthyGateway(t *testing.T) {
	broker := newFakeBroker(t)
	identities := &blockingQuarantineIdentities{
		fakeIdentities: newFakeIdentities(),
		started:        make(chan struct{}, 4),
		release:        make(chan struct{}),
	}
	var releaseOnce sync.Once
	unblockQuarantine := func() {
		releaseOnce.Do(func() { close(identities.release) })
	}

	telemetry := &fakeTelemetry{}
	frozenNow := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	processor, err := NewProcessor(identities, telemetry, WithGatewayRateLimit(1, 3))
	if err != nil {
		t.Fatal(err)
	}
	processor.now = func() time.Time { return frozenNow }

	runtime, err := NewRuntime(Config{
		MQTT:             broker.mqtt,
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
		QueueCapacity:    8,
		OverflowDir:      t.TempDir(),
	}, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.closeMailboxes)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		unblockQuarantine()
		cancel()
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})

	conn := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	for i := 1; i <= 10; i++ {
		conn.deliverGateway(t, testGatewayA, uint16(i), i, false)
	}
	conn.awaitAcks(t, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

	select {
	case <-identities.started:
	case <-time.After(3 * time.Second):
		t.Fatal("async rate-limit quarantine never started")
	}

	for i := 1; i <= 3; i++ {
		conn.deliverGateway(t, testGatewayB, uint16(10+i), 100+i, false)
	}
	conn.awaitAcks(t, 11, 12, 13)

	waitCondition(t, 3*time.Second, func() bool {
		return len(telemetry.accepted()) == 6
	}, "3 burst Gateway A + 3 healthy Gateway B messages persisted while quarantine blocked")

	select {
	case <-conn.disconnected:
		t.Fatal("broker connection closed during gateway A rate-limit flood")
	default:
	}
	select {
	case <-broker.conns:
		t.Fatal("runtime opened unexpected reconnect during gateway A rate-limit flood")
	default:
	}
	if !runtime.Ready() {
		t.Fatal("runtime.Ready() turned false during gateway A rate-limit flood")
	}

	unblockQuarantine()
	waitCondition(t, 3*time.Second, func() bool {
		return len(identities.snapshotQuarantines()) == 1
	}, "rate-limit quarantine sample persisted after unblock")

	q := identities.snapshotQuarantines()[0]
	if q.GatewayID != testGatewayA || q.ReasonCode != QuarantineReasonRateLimited {
		t.Fatalf("unexpected quarantine record: %+v", q)
	}

	metricsText := scrapeMetricsText(runtime.metrics)
	for _, want := range []string{
		`hvac_mqtt_messages_received_total{outcome="queued"} 6`,
		`hvac_mqtt_messages_received_total{outcome="dropped"} 7`,
		`hvac_mqtt_gateway_rate_limited_total{message_type="telemetry"} 7`,
	} {
		if !strings.Contains(metricsText, want) {
			t.Fatalf("missing metric %q in:\n%s", want, metricsText)
		}
	}
}

func TestRuntimeEnqueueAcksExcessOnIngressRateLimit(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	processor, err := NewProcessor(identities, telemetry, WithGatewayRateLimit(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	processor.now = func() time.Time { return now }

	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    4,
		OverflowDir:      t.TempDir(),
	}, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.quarantineWG.Wait(); runtime.closeMailboxes() })

	var acked []uint16
	ackFn := func(p *paho.Publish) error {
		acked = append(acked, p.PacketID)
		return nil
	}
	pub1 := publish(testGatewayA, 1)
	pub1.PacketID = 1
	if err := runtime.enqueue(ctx, nil, pub1, ackFn); err != nil {
		t.Fatalf("burst message 1 failed: %v", err)
	}
	pub2 := publish(testGatewayA, 2)
	pub2.PacketID = 2
	if err := runtime.enqueue(ctx, nil, pub2, ackFn); err != nil {
		t.Fatalf("expected rate-limited excess to ACK cleanly without disconnecting shared session, got %v", err)
	}
	if len(acked) != 2 || acked[0] != 1 || acked[1] != 2 {
		t.Fatalf("acked=%v, want [1 2]", acked)
	}
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 1
	}, "only non-rate-limited burst message persisted")
}

func TestRuntimePoisonPill400BatchSplitAdvancesWALAndQuarantinesBadMessage(t *testing.T) {
	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		failHook: func(_ int, batch []Observation) error {
			for _, obs := range batch {
				if obs.TelemetryKey == "poison_point" {
					return &RequestInvalidError{Label: "S2 observation batch", StatusCode: http.StatusBadRequest}
				}
			}
			return nil
		},
	}
	overflowDir := t.TempDir()
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    8,
		OverflowDir:      overflowDir,
	}, newTestProcessor(t, identities, telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
	acks := newAckLog()

	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered

	poisonPub := &paho.Publish{
		Topic:   telemetryTopic(testGatewayA),
		Payload: telemetryPayload(testGatewayA, messageID(3), 3, "METER-01", "poison_point"),
		QoS:     1,
	}
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 2), acks.ack(messageID(2))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, poisonPub, acks.ack(messageID(3))); err != nil {
		t.Fatal(err)
	}
	if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 4), acks.ack(messageID(4))); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 4; index++ {
		acks.await(t, messageID(index))
	}

	close(telemetry.release)
	waitCondition(t, 2*time.Second, func() bool {
		return len(telemetry.accepted()) == 3 && len(identities.snapshotQuarantines()) == 1
	}, "valid messages persisted and 400 poison pill quarantined")

	spoolPath := filepath.Join(overflowDir, safeGatewayFileName(testGatewayA))
	waitCondition(t, 2*time.Second, func() bool {
		stat, statErr := os.Stat(spoolPath)
		return statErr == nil && stat.Size() == walHeaderSize
	}, "WAL spool truncated after split batch consumed")
}

func TestRuntimeClosePreservesOwnedOverflowDirWhenUnconsumedAndCleansWhenDrained(t *testing.T) {
	t.Run("removes auto overflow dir when drained", func(t *testing.T) {
		telemetry := &fakeTelemetry{}
		runtime, err := NewRuntime(Config{
			MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
			QueueCapacity:    4,
		}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.prepareOverflowDir(); err != nil {
			t.Fatal(err)
		}
		autoDir := runtime.overflowDir
		t.Cleanup(func() { _ = os.RemoveAll(autoDir) })

		ctx, cancel := context.WithCancel(t.Context())
		acks := newAckLog()
		if err := runtime.enqueue(ctx, nil, publish(testGatewayA, 1), acks.ack(messageID(1))); err != nil {
			t.Fatal(err)
		}
		acks.await(t, messageID(1))
		waitCondition(t, 2*time.Second, func() bool {
			return len(telemetry.accepted()) == 1
		}, "message drained before close")

		cancel()
		runtime.workers.Wait()
		runtime.closeMailboxes()

		if _, statErr := os.Stat(autoDir); !os.IsNotExist(statErr) {
			t.Fatalf("expected drained auto overflow dir %s to be removed, stat err=%v", autoDir, statErr)
		}
	})

	t.Run("preserves auto overflow dir and spools when unconsumed items remain", func(t *testing.T) {
		broker := newFakeBroker(t)
		telemetry := &fakeTelemetry{
			release: make(chan struct{}),
			entered: make(chan struct{}, 2),
		}
		runtime, err := NewRuntime(Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    1,
		}, newTestProcessor(t, newFakeIdentities(), telemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		runtime.drainTimeout = 50 * time.Millisecond

		ctx, cancel := context.WithCancel(t.Context())
		runErr := make(chan error, 1)
		go func() { runErr <- runtime.Run(ctx) }()

		conn := broker.awaitConnection(t)
		awaitReady(t, runtime, true)

		conn.deliverGateway(t, testGatewayA, 1, 1, false)
		<-telemetry.entered
		conn.deliverGateway(t, testGatewayA, 2, 2, false)
		conn.deliverGateway(t, testGatewayA, 3, 3, false)
		conn.awaitAcks(t, 1, 2, 3)

		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Fatalf("Run returned error on bounded drain timeout: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run exceeded drain timeout")
		}

		preservedDir := runtime.overflowDir
		if preservedDir == "" {
			t.Fatal("expected preserved overflow dir path when unconsumed items remain")
		}
		t.Cleanup(func() { _ = os.RemoveAll(preservedDir) })

		spoolPath := filepath.Join(preservedDir, safeGatewayFileName(testGatewayA))
		stat, statErr := os.Stat(spoolPath)
		if statErr != nil || stat.Size() <= walHeaderSize {
			t.Fatalf("expected preserved spool file with unconsumed items, stat=(%v, %v)", stat, statErr)
		}
		runtime.closeMailboxes()
		if _, err := os.Stat(spoolPath); err != nil {
			t.Fatalf("second idempotent closeMailboxes removed preserved spool %s: %v", spoolPath, err)
		}

		recoveredTelemetry := &fakeTelemetry{}
		recoveredRuntime, err := NewRuntime(Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    4,
			OverflowDir:      preservedDir,
		}, newTestProcessor(t, newFakeIdentities(), recoveredTelemetry), slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		recCtx, recCancel := context.WithCancel(t.Context())
		t.Cleanup(func() { recCancel(); recoveredRuntime.workers.Wait(); recoveredRuntime.closeMailboxes() })
		if err := recoveredRuntime.recoverOverflowMailboxes(recCtx); err != nil {
			t.Fatalf("recoverOverflowMailboxes failed: %v", err)
		}
		waitCondition(t, 2*time.Second, func() bool {
			return len(recoveredTelemetry.accepted()) == 3
		}, "all 3 preserved unconsumed items recovered from auto overflow dir")
	})
}

func TestRuntimeLiveMessagesJumpAheadOfReplayBacklogWithoutStarvingReplay(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
	}
	overflowDir := t.TempDir()
	processor, err := NewProcessor(newFakeIdentities(), telemetry, WithAutoBackfillLagThreshold(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	processor.now = func() time.Time { return now }

	runtime, err := NewRuntime(Config{
		MQTT:                     MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-priority-test", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime:         TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:            4,
		OverflowMaxMessages:      4096,
		OverflowDir:              overflowDir,
		BatchMaxMessages:         16,
		AutoBackfillLagThreshold: 10 * time.Minute,
		WorkerCount:              1,
	}, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), observability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime.now = func() time.Time { return now }
	runtime.retryDelay = func(int) time.Duration { return 5 * time.Millisecond }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })
	acks := newAckLog()
	acks.seen = make(chan string, 1100)

	if err := runtime.enqueue(ctx, nil, testLivePublishAt(testGatewayA, 1, now.Add(-time.Second)), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered

	const replayCount = 1000
	for i := 2; i <= replayCount+1; i++ {
		pub := testReplayPublish(testGatewayA, i, false)
		if i%4 == 0 {
			pub = testLivePublishAt(testGatewayA, i, now.Add(-15*time.Minute))
		}
		if err := runtime.enqueue(ctx, nil, pub, acks.ack(messageID(i))); err != nil {
			t.Fatalf("enqueue replay %d: %v", i, err)
		}
	}

	const liveBurstCount = 10
	for i := 1; i <= liveBurstCount; i++ {
		seq := replayCount + 1 + i
		pub := testLivePublishAt(testGatewayA, seq, now.Add(-time.Second))
		if i == 1 {
			pub = &paho.Publish{Topic: eventTopic(testGatewayA), Payload: eventPayload(testGatewayA, messageID(seq), seq), QoS: 1}
		}
		if err := runtime.enqueue(ctx, nil, pub, acks.ack(messageID(seq))); err != nil {
			t.Fatalf("enqueue live %d: %v", i, err)
		}
	}

	queuedBody := scrapeMetricsText(runtime.metrics)
	if !strings.Contains(queuedBody, fmt.Sprintf("hvac_mqtt_replay_queue_depth %d\n", replayCount)) {
		t.Fatalf("expected hvac_mqtt_replay_queue_depth %d while backlogged:\n%s", replayCount, queuedBody)
	}

	close(telemetry.release)
	waitCondition(t, 10*time.Second, func() bool {
		return len(telemetry.accepted())+len(telemetry.recordedEvents()) == 1+replayCount+liveBurstCount
	}, "all live and replay messages processed without starvation")

	waitCondition(t, 2*time.Second, func() bool {
		drainedBody := scrapeMetricsText(runtime.metrics)
		return strings.Contains(drainedBody, "hvac_mqtt_replay_queue_depth 0\n") &&
			strings.Contains(drainedBody, "hvac_mqtt_processing_queue_depth 0\n")
	}, "replay and processing queue depth gauges return to zero after drain")

	accepted := telemetry.accepted()
	livePositions := 0
	for idx := 0; idx < 25 && idx < len(accepted); idx++ {
		if !accepted[idx].EdgeReplay {
			livePositions++
		}
	}
	if livePositions != 10 {
		t.Fatalf("expected initial live + 9 live telemetry items in first 25 observations, got %d", livePositions)
	}
	events := telemetry.recordedEvents()
	if len(events) != 1 || events[0].MessageID != messageID(replayCount+2) {
		t.Fatalf("unexpected events=%+v", events)
	}

	for _, spoolPath := range []string{
		filepath.Join(overflowDir, safeGatewayFileName(testGatewayA)),
		filepath.Join(overflowDir, safeGatewayReplayFileName(testGatewayA)),
	} {
		waitCondition(t, 2*time.Second, func() bool {
			stat, err := os.Stat(spoolPath)
			return err == nil && stat.Size() == walHeaderSize
		}, "spool truncated to header after drain: "+filepath.Base(spoolPath))
	}
}

func TestRuntimeGracefulDrainTimeoutPreservesBothLiveAndReplaySpools(t *testing.T) {
	broker := newFakeBroker(t)
	entered := make(chan struct{}, 2)
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: entered,
	}
	overflowDir := t.TempDir()
	runtime, err := NewRuntime(
		Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    2,
			OverflowDir:      overflowDir,
		},
		newTestProcessor(t, newFakeIdentities(), telemetry),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		observability.NewRegistry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.drainTimeout = 80 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- runtime.Run(ctx) }()

	conn := broker.awaitConnection(t)
	awaitReady(t, runtime, true)

	conn.deliverGateway(t, testGatewayA, 1, 1, false)
	<-entered
	replayPkt := packets.NewControlPacket(packets.PUBLISH)
	rep := replayPkt.Content.(*packets.Publish)
	rep.Topic = telemetryTopic(testGatewayA)
	rep.Payload = testReplayPublish(testGatewayA, 2, false).Payload
	rep.QoS = 1
	rep.PacketID = 2
	if err := conn.write(replayPkt); err != nil {
		t.Fatal(err)
	}
	conn.deliverGateway(t, testGatewayA, 3, 3, false)
	conn.awaitAcks(t, 1, 2, 3)

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error after drain timeout: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run exceeded bounded drain timeout")
	}

	recoveredTelemetry := &fakeTelemetry{}
	recoveredRuntime, err := NewRuntime(
		Config{
			MQTT:             broker.mqtt,
			TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: broker.mqtt.CAFile, CertFile: broker.mqtt.CertFile, KeyFile: broker.mqtt.KeyFile, ServerName: "telemetry.local"},
			QueueCapacity:    4,
			OverflowDir:      overflowDir,
		},
		newTestProcessor(t, newFakeIdentities(), recoveredTelemetry),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		observability.NewRegistry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	recCtx, recCancel := context.WithCancel(t.Context())
	t.Cleanup(func() { recCancel(); recoveredRuntime.workers.Wait(); recoveredRuntime.closeMailboxes() })
	if err := recoveredRuntime.recoverOverflowMailboxes(recCtx); err != nil {
		t.Fatalf("recoverOverflowMailboxes failed: %v", err)
	}
	waitCondition(t, 3*time.Second, func() bool {
		return len(recoveredTelemetry.accepted()) == 3
	}, "all 3 preserved live and replay messages recovered on restart")
}

func TestRuntimeReplayQueueDepthMetricTracksQueuedReplayItems(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	telemetry := &fakeTelemetry{
		release: make(chan struct{}),
		entered: make(chan struct{}, 2),
	}
	registry := observability.NewRegistry()
	overflowDir := t.TempDir()
	processor, err := NewProcessor(newFakeIdentities(), telemetry)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(Config{
		MQTT:             MQTTConfig{BrokerURL: "tls://localhost:8883", ClientID: "connectivity-replay-depth", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "mqtt.local"},
		TelemetryRuntime: TelemetryRuntimeConfig{BaseURL: "https://telemetry.local", CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "telemetry.local"},
		QueueCapacity:    4,
		OverflowDir:      overflowDir,
		WorkerCount:      1,
	}, processor, slog.New(slog.NewTextHandler(io.Discard, nil)), registry)
	if err != nil {
		t.Fatal(err)
	}
	runtime.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); runtime.workers.Wait(); runtime.closeMailboxes() })

	initialBody := scrapeMetricsText(registry)
	if !strings.Contains(initialBody, "hvac_mqtt_replay_queue_depth 0\n") {
		t.Fatalf("expected initial hvac_mqtt_replay_queue_depth 0:\n%s", initialBody)
	}

	acks := newAckLog()
	if err := runtime.enqueue(ctx, nil, testLivePublishAt(testGatewayA, 1, now.Add(-time.Second)), acks.ack(messageID(1))); err != nil {
		t.Fatal(err)
	}
	<-telemetry.entered

	for i := 2; i <= 4; i++ {
		if err := runtime.enqueue(ctx, nil, testReplayPublish(testGatewayA, i, false), acks.ack(messageID(i))); err != nil {
			t.Fatalf("enqueue replay A %d: %v", i, err)
		}
	}
	for i := 5; i <= 6; i++ {
		if err := runtime.enqueue(ctx, nil, testReplayPublish(testGatewayB, i, false), acks.ack(messageID(i))); err != nil {
			t.Fatalf("enqueue replay B %d: %v", i, err)
		}
	}

	queuedBody := scrapeMetricsText(registry)
	for _, want := range []string{
		"hvac_mqtt_replay_queue_depth 5\n",
		"hvac_mqtt_processing_queue_depth 3\n",
	} {
		if !strings.Contains(queuedBody, want) {
			t.Fatalf("missing %q while queued across two gateways:\n%s", want, queuedBody)
		}
	}

	close(telemetry.release)
	waitCondition(t, 5*time.Second, func() bool {
		return len(telemetry.accepted()) == 6
	}, "all live and replay items processed across gateways")
	waitCondition(t, 2*time.Second, func() bool {
		body := scrapeMetricsText(registry)
		return strings.Contains(body, "hvac_mqtt_replay_queue_depth 0\n") &&
			strings.Contains(body, "hvac_mqtt_processing_queue_depth 0\n")
	}, "replay and processing queue depth metrics drained to zero")
}
