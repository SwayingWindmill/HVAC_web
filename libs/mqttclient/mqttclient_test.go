package mqttclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
)

type fakeSubscriber struct {
	mu       sync.Mutex
	calls    int
	fn       func(call int, ctx context.Context, s *paho.Subscribe) (*paho.Suback, error)
	topics   [][]string
	canceled bool
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, s *paho.Subscribe) (*paho.Suback, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	if s != nil {
		list := make([]string, 0, len(s.Subscriptions))
		for _, sub := range s.Subscriptions {
			list = append(list, sub.Topic)
		}
		f.topics = append(f.topics, list)
	}
	fn := f.fn
	f.mu.Unlock()

	if ctx.Err() != nil {
		f.mu.Lock()
		f.canceled = true
		f.mu.Unlock()
		return nil, ctx.Err()
	}
	return fn(call, ctx, s)
}

func TestSharedTopicFilterAndMatch(t *testing.T) {
	t.Parallel()

	if got := SharedTopicFilter("", "hvac/v1/+/up/telemetry"); got != "hvac/v1/+/up/telemetry" {
		t.Fatalf("empty group modified topic: %q", got)
	}
	if got := SharedTopicFilter("connectivity-workers", "hvac/v1/+/up/telemetry"); got != "$share/connectivity-workers/hvac/v1/+/up/telemetry" {
		t.Fatalf("unexpected shared filter: %q", got)
	}
	if got := SharedTopicFilter("connectivity-workers", "$share/other/hvac/v1/+/up/telemetry"); got != "$share/other/hvac/v1/+/up/telemetry" {
		t.Fatalf("existing $share prefix should be preserved, got %q", got)
	}

	gotSlice := SharedTopicFilters("grp", []string{"a/b", "$share/x/c"})
	if !slices.Equal(gotSlice, []string{"$share/grp/a/b", "$share/x/c"}) {
		t.Fatalf("unexpected SharedTopicFilters result: %#v", gotSlice)
	}

	if err := ValidateShareGroup(""); err != nil {
		t.Fatalf("empty share group must be valid: %v", err)
	}
	if err := ValidateShareGroup("workers-1"); err != nil {
		t.Fatalf("valid share group rejected: %v", err)
	}
	for _, bad := range []string{"bad/group", "bad+group", "bad#group", " leading", "trailing ", strings.Repeat("a", 65)} {
		if err := ValidateShareGroup(bad); err == nil {
			t.Fatalf("expected error for invalid share group %q", bad)
		}
	}
	if got := StripSharePrefix("$share//hvac/v1/gw/up/reply"); got != "$share//hvac/v1/gw/up/reply" {
		t.Fatalf("empty $share group prefix must not be stripped, got %q", got)
	}

	if !MatchTopicFilter("$share/workers/hvac/v1/+/up/telemetry", "hvac/v1/gw-1/up/telemetry") {
		t.Fatalf("expected $share wildcard filter to match telemetry topic")
	}
	if MatchTopicFilter("$share/workers/hvac/v1/+/up/telemetry", "hvac/v1/gw-1/up/event") {
		t.Fatalf("did not expect telemetry filter to match event topic")
	}
	if !MatchTopicFilter("hvac/v1/#", "hvac/v1/gw-1/up/telemetry") {
		t.Fatalf("expected multi-level # filter to match")
	}
	if !MatchTopicFilter("hvac/v1/#", "hvac/v1") {
		t.Fatalf("expected MQTT §4.7.1.2 parent topic match for #")
	}
	if MatchTopicFilter("#", "$SYS/broker/clients") || MatchTopicFilter("+/broker/clients", "$SYS/broker/clients") {
		t.Fatalf("MQTT $-prefixed topics must not match leading wildcards")
	}
	if !MatchTopicFilter("$SYS/#", "$SYS/broker/clients") {
		t.Fatalf("expected explicit $SYS/# filter to match $SYS topic")
	}
	if MatchTopicFilter("a/#/b", "a/x/b") {
		t.Fatalf("invalid mid-topic # filter must not match")
	}
}

func TestSubscribeWithRetrySubackFailureAndRecovery(t *testing.T) {
	t.Parallel()

	sub := &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{
			{Topic: "hvac/v1/+/up/telemetry", QoS: 1},
			{Topic: "hvac/v1/+/up/event", QoS: 1},
		},
	}

	// Fails twice with MQTT SUBACK rejection (0x87), succeeds on 3rd attempt.
	client := &fakeSubscriber{
		fn: func(call int, _ context.Context, _ *paho.Subscribe) (*paho.Suback, error) {
			if call < 3 {
				return &paho.Suback{Reasons: []byte{0x01, 0x87}}, nil
			}
			return &paho.Suback{Reasons: []byte{0x01, 0x01}}, nil
		},
	}

	delays := []time.Duration{time.Millisecond, time.Millisecond}
	if err := SubscribeWithRetry(context.Background(), client, sub, time.Second, delays, nil); err != nil {
		t.Fatalf("expected retry recovery after transient SUBACK failure, got: %v", err)
	}
	if client.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", client.calls)
	}

	// Persistent SUBACK failure returns error once retries exhaust.
	alwaysReject := &fakeSubscriber{
		fn: func(_ int, _ context.Context, _ *paho.Subscribe) (*paho.Suback, error) {
			return &paho.Suback{Reasons: []byte{0x01, 0x80}}, nil
		},
	}
	err := SubscribeWithRetry(context.Background(), alwaysReject, sub, time.Second, []time.Duration{time.Millisecond}, nil)
	if err == nil {
		t.Fatal("expected SUBACK reason 0x80 failure")
	}
	if alwaysReject.calls != 2 {
		t.Fatalf("expected 2 attempts with 1 retry delay, got %d", alwaysReject.calls)
	}

	// Stops immediately if connection closed callback reports closed.
	var closed atomic.Bool
	stopOnClose := &fakeSubscriber{
		fn: func(_ int, _ context.Context, _ *paho.Subscribe) (*paho.Suback, error) {
			closed.Store(true)
			return nil, errors.New("broken pipe")
		},
	}
	err = SubscribeWithRetry(context.Background(), stopOnClose, sub, time.Second, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond}, closed.Load)
	if err == nil {
		t.Fatal("expected error when connection closes")
	}
	if stopOnClose.calls != 1 {
		t.Fatalf("expected retry loop to abort immediately on closed connection, got %d attempts", stopOnClose.calls)
	}

	if err := CheckSubackReasons(nil, 1); err == nil {
		t.Fatal("expected error on nil SUBACK")
	}
	if err := CheckSubackReasons(&paho.Suback{Reasons: []byte{0x01}}, 2); err == nil {
		t.Fatal("expected error on incomplete SUBACK")
	}
	if err := CheckSubackReasons(&paho.Suback{Reasons: []byte{0x00, 0x02}}, 2); err != nil {
		t.Fatalf("expected valid SUBACK codes to pass, got %v", err)
	}
}

func TestConnTrackerGenerationFencing(t *testing.T) {
	t.Parallel()

	tracker := NewConnTracker()
	var (
		obsMu  sync.Mutex
		states []ConnState
	)
	unsubObs := tracker.AddObserver(func(s ConnState) {
		obsMu.Lock()
		states = append(states, s)
		obsMu.Unlock()
	})
	defer unsubObs()

	gen1 := tracker.OnConnectUp()
	if gen1 == 0 {
		t.Fatal("expected non-zero generation on OnConnectUp")
	}
	st := tracker.State()
	if !st.Connected || st.Subscribed || st.Healthy() {
		t.Fatalf("expected connected but not yet subscribed state, got %+v", st)
	}

	// Simulate disconnect followed by reconnect to gen2 while gen1 subscribe is still in flight.
	tracker.OnConnectionDown(errors.New("eof"))
	gen2 := tracker.OnConnectUp()
	if gen2 <= gen1 {
		t.Fatalf("expected monotonic generation gen2 (%d) > gen1 (%d)", gen2, gen1)
	}

	// Stale subscribe goroutine for gen1 must fail to commit Subscribed or overwrite LastError.
	if tracker.MarkSubscribed(gen1) {
		t.Fatal("stale gen1 MarkSubscribed must be rejected")
	}
	if tracker.MarkSubscribeError(gen1, errors.New("stale subscribe err")) {
		t.Fatal("stale gen1 MarkSubscribeError must be rejected")
	}
	if !IsClosedSignal(tracker.ClosedChanForGen(gen1)) {
		t.Fatal("superseded gen1 ClosedChanForGen should report closed immediately")
	}

	st = tracker.State()
	if st.Subscribed || st.LastError != "" || st.Generation != gen2 {
		t.Fatalf("stale gen1 corrupted gen2 state: %+v", st)
	}

	// Current generation gen2 commits cleanly.
	if !tracker.MarkSubscribed(gen2) {
		t.Fatal("expected active gen2 MarkSubscribed to succeed")
	}
	if !tracker.State().Healthy() {
		t.Fatalf("expected healthy state after gen2 MarkSubscribed, got %+v", tracker.State())
	}

	// CloseIfGen on stale gen1 must not affect live gen2.
	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()
	_ = tracker.TrackConn(p1)
	liveGen := tracker.OnConnectUp()
	if err := tracker.CloseIfGen(gen1); err != nil {
		t.Fatalf("stale CloseIfGen failed: %v", err)
	}
	if !tracker.State().Connected {
		t.Fatal("stale CloseIfGen must not tear down active connection")
	}
	if err := tracker.CloseIfGen(liveGen); err != nil {
		t.Fatalf("live CloseIfGen failed: %v", err)
	}

	obsMu.Lock()
	observedCount := len(states)
	obsMu.Unlock()
	if observedCount < 4 {
		t.Fatalf("expected observer to capture lifecycle transitions, got %d", observedCount)
	}
}

func TestNewMTLSConfigTLS13AndValidation(t *testing.T) {
	t.Parallel()

	certs := writeTestPKI(t)
	cfg, err := NewMTLSConfig(TLSConfig{
		CACertFile:     certs.caCertFile,
		ClientCertFile: certs.clientCertFile,
		ClientKeyFile:  certs.clientKeyFile,
		ServerName:     "mqtt.local",
	})
	if err != nil {
		t.Fatalf("NewMTLSConfig failed: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("expected TLS 1.3 min version, got 0x%x", cfg.MinVersion)
	}
	if cfg.ServerName != "mqtt.local" {
		t.Fatalf("unexpected ServerName: %q", cfg.ServerName)
	}
	if len(cfg.Certificates) != 1 || cfg.RootCAs == nil {
		t.Fatal("expected loaded client cert and CA pool")
	}

	if _, err := NewMTLSConfig(TLSConfig{CACertFile: certs.caCertFile}); err == nil {
		t.Fatal("expected error when client cert/key files missing")
	}
	badCA := filepath.Join(t.TempDir(), "bad-ca.pem")
	if err := os.WriteFile(badCA, []byte("not-a-pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMTLSConfig(TLSConfig{
		CACertFile:     badCA,
		ClientCertFile: certs.clientCertFile,
		ClientKeyFile:  certs.clientKeyFile,
	}); err == nil {
		t.Fatal("expected error when CA cert PEM is invalid")
	}
}

func TestSharedSessionMultiRouteUnregisterAndReconnect(t *testing.T) {
	t.Parallel()

	certs := writeTestPKI(t)
	broker := startTestMTLSBroker(t, certs.serverTLS)
	defer broker.close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var observerStates []SessionState
	var obsMu sync.Mutex

	session, err := NewSharedSession(ctx, SharedSessionConfig{
		BrokerURL: "tls://" + broker.addr,
		ClientID:  "connectivity-shared-test",
		TLS: &TLSConfig{
			CACertFile:     certs.caCertFile,
			ClientCertFile: certs.clientCertFile,
			ClientKeyFile:  certs.clientKeyFile,
			ServerName:     "localhost",
		},
		CleanStartOnInitialConnection: true,
		DialTimeout:                   2 * time.Second,
		ConnectRetryDelay:             50 * time.Millisecond,
		SubscribeAttemptTimeout:       2 * time.Second,
		SubscribeRetryDelays:          []time.Duration{25 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewSharedSession failed: %v", err)
	}
	defer func() {
		_ = session.Close(context.Background())
	}()

	stopObs := session.AddObserver(func(s SessionState) {
		obsMu.Lock()
		observerStates = append(observerStates, s)
		obsMu.Unlock()
	})
	defer stopObs()

	// Register routes before/after connection stabilizes:
	telemetryCh := make(chan string, 8)
	replyCh1 := make(chan string, 8)
	replyCh2 := make(chan string, 8)

	unregTelemetry, err := session.RegisterRoute("adapter-uplink", []string{"hvac/v1/+/up/telemetry"}, func(pkt *paho.Publish) {
		telemetryCh <- string(pkt.Payload)
	})
	if err != nil {
		t.Fatalf("RegisterRoute telemetry failed: %v", err)
	}

	if err := session.AwaitConnection(ctx); err != nil {
		t.Fatalf("AwaitConnection failed: %v", err)
	}

	// Dynamic route registration while connected, including shared filter refcounting.
	unregReply1, err := session.RegisterRoute("rpc-1", []string{"hvac/v1/+/down/reply"}, func(pkt *paho.Publish) {
		replyCh1 <- string(pkt.Payload)
	})
	if err != nil {
		t.Fatalf("RegisterRoute rpc-1 failed: %v", err)
	}
	unregReply2, err := session.RegisterRoute("rpc-2", []string{"hvac/v1/+/down/reply"}, func(pkt *paho.Publish) {
		replyCh2 <- string(pkt.Payload)
	})
	if err != nil {
		t.Fatalf("RegisterRoute rpc-2 failed: %v", err)
	}

	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed failed: %v", err)
	}

	// Verify TLS 1.3 handshake happened on broker.
	if broker.lastTLSVersion() != tls.VersionTLS13 {
		t.Fatalf("expected broker TLS 1.3 connection, got 0x%x", broker.lastTLSVersion())
	}

	// Broker delivers matching packets to respective routes without cross-talk.
	broker.pushPublish("hvac/v1/gw-1/up/telemetry", []byte("tele-1"), 1)
	broker.pushPublish("hvac/v1/gw-1/down/reply", []byte("rep-1"), 1)

	if got := recvString(t, telemetryCh); got != "tele-1" {
		t.Fatalf("unexpected telemetry payload: %q", got)
	}
	if got := recvString(t, replyCh1); got != "rep-1" {
		t.Fatalf("unexpected rpc-1 payload: %q", got)
	}
	if got := recvString(t, replyCh2); got != "rep-1" {
		t.Fatalf("unexpected rpc-2 payload: %q", got)
	}

	// Unregistering rpc-1 while rpc-2 still owns "hvac/v1/+/down/reply" must NOT unsubscribe on broker.
	if err := unregReply1(ctx); err != nil {
		t.Fatalf("unregister rpc-1 failed: %v", err)
	}
	if unsubs := broker.unsubscribedTopics(); len(unsubs) != 0 {
		t.Fatalf("expected 0 broker unsubscribes while rpc-2 still active, got %v", unsubs)
	}

	// Unregistering rpc-2 removes the last owner and MUST send broker UNSUBSCRIBE.
	if err := unregReply2(ctx); err != nil {
		t.Fatalf("unregister rpc-2 failed: %v", err)
	}
	broker.waitUnsubscribed(t, "hvac/v1/+/down/reply")

	// Publish works cleanly through SharedSession.
	resp, err := session.Publish(ctx, &paho.Publish{
		Topic:   "hvac/v1/gw-1/down/request",
		QoS:     1,
		Payload: []byte(`{"cmd":"ping"}`),
	})
	if err != nil || resp == nil {
		t.Fatalf("Publish failed: resp=%v err=%v", resp, err)
	}
	broker.waitPublished(t, "hvac/v1/gw-1/down/request")

	// Simulate abrupt broker socket drop; verify reconnect re-subscribes active route only.
	subsBeforeDrop := broker.subscribeCalls()
	broker.dropActiveConn()

	// Wait for reconnect + second SUBSCRIBE on broker.
	broker.waitSubscribeCallsAtLeast(t, subsBeforeDrop+1)
	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed after reconnect failed: %v", err)
	}

	latestSubs := broker.lastSubscribeTopics()
	if !slices.Equal(latestSubs, []string{"hvac/v1/+/up/telemetry"}) {
		t.Fatalf("expected reconnect to resubscribe only active telemetry route, got %v", latestSubs)
	}

	broker.pushPublish("hvac/v1/gw-2/up/telemetry", []byte("tele-after-reconnect"), 1)
	if got := recvString(t, telemetryCh); got != "tele-after-reconnect" {
		t.Fatalf("unexpected telemetry after reconnect: %q", got)
	}

	if err := unregTelemetry(ctx); err != nil {
		t.Fatalf("unregister telemetry failed: %v", err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("session.Close failed: %v", err)
	}
	if _, err := session.RegisterRoute("after-close", []string{"a/b"}, func(*paho.Publish) {}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed after Close, got %v", err)
	}

	obsMu.Lock()
	sawHealthy := slices.ContainsFunc(observerStates, func(s SessionState) bool { return s.Healthy() })
	sawDisconnected := slices.ContainsFunc(observerStates, func(s SessionState) bool { return !s.Connected })
	obsMu.Unlock()
	if !sawHealthy || !sawDisconnected {
		t.Fatalf("expected observers to record healthy and disconnected transitions, got %+v", observerStates)
	}
}

func TestSharedSessionManagedRouteErrorTriggersReconnectRedelivery(t *testing.T) {
	t.Parallel()

	certs := writeTestPKI(t)
	broker := startTestMTLSBroker(t, certs.serverTLS)
	defer broker.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := NewSharedSession(ctx, SharedSessionConfig{
		BrokerURL:                     "tls://" + broker.addr,
		ClientID:                      "managed-route-test",
		TLSConfig:                     certs.clientTLS,
		CleanStartOnInitialConnection: true,
		DialTimeout:                   2 * time.Second,
		ConnectRetryDelay:             50 * time.Millisecond,
		SubscribeAttemptTimeout:       2 * time.Second,
		SubscribeRetryDelays:          []time.Duration{20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewSharedSession failed: %v", err)
	}
	defer func() {
		_ = session.Close(context.Background())
	}()

	var acked atomic.Int32
	delivered := make(chan struct{}, 2)
	_, err = session.RegisterManagedRoute("custody", []string{"hvac/v1/+/up/telemetry"}, func(ev PublishEvent) error {
		if string(ev.Packet.Payload) == "fail-wal" {
			return errors.New("wal write failed")
		}
		if err := ev.Ack(ev.Packet); err != nil {
			return err
		}
		acked.Add(1)
		delivered <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterManagedRoute failed: %v", err)
	}
	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed failed: %v", err)
	}

	beforeAck := broker.pubAckCount()
	broker.pushPublish("hvac/v1/gw-1/up/telemetry", []byte("fail-wal"), 1)
	// Broker waits for disconnect triggered by failed custody handler without PUBACK.
	broker.waitSubscribeCallsAtLeast(t, 2)
	if broker.pubAckCount() != beforeAck {
		t.Fatalf("failed managed route must NOT send PUBACK before connection closes")
	}

	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed after managed error reconnect failed: %v", err)
	}
	broker.pushPublish("hvac/v1/gw-1/up/telemetry", []byte("ok-wal"), 1)
	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for managed delivery after reconnect")
	}
	if acked.Load() != 1 {
		t.Fatalf("expected 1 manual ack, got %d", acked.Load())
	}
}

func TestConnTrackerRecordSuccessClearsLastErrorAndRestoresHealth(t *testing.T) {
	t.Parallel()

	tracker := NewConnTracker()
	var (
		mu     sync.Mutex
		states []ConnState
	)
	unsub := tracker.AddObserver(func(s ConnState) {
		mu.Lock()
		states = append(states, s)
		mu.Unlock()
	})
	defer unsub()

	gen := tracker.OnConnectUp()
	if !tracker.MarkSubscribed(gen) {
		t.Fatal("MarkSubscribed failed")
	}
	if !tracker.State().Healthy() {
		t.Fatalf("expected healthy state before error, got %+v", tracker.State())
	}

	tracker.RecordError(errors.New("transient client error"))
	st := tracker.State()
	if st.Healthy() || st.LastError != "transient client error" {
		t.Fatalf("expected unhealthy state with LastError set, got %+v", st)
	}

	tracker.RecordSuccess()
	st = tracker.State()
	if !st.Healthy() || st.LastError != "" || st.LastSuccess.IsZero() {
		t.Fatalf("expected RecordSuccess to clear LastError and restore Healthy(), got %+v", st)
	}

	mu.Lock()
	lastObserved := states[len(states)-1]
	mu.Unlock()
	if !lastObserved.Healthy() || lastObserved.LastError != "" {
		t.Fatalf("expected observer to receive healed healthy state, got %+v", lastObserved)
	}
}

func TestSharedSessionRegisterRouteDoesNotMarkSubscribedBeforeResubscribeAll(t *testing.T) {
	t.Parallel()

	certs := writeTestPKI(t)
	broker := startTestMTLSBroker(t, certs.serverTLS)
	defer broker.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := NewSharedSession(ctx, SharedSessionConfig{
		BrokerURL:                     "tls://" + broker.addr,
		ClientID:                      "resubscribe-fence-test",
		TLSConfig:                     certs.clientTLS,
		CleanStartOnInitialConnection: true,
		DialTimeout:                   2 * time.Second,
		ConnectRetryDelay:             50 * time.Millisecond,
		SubscribeAttemptTimeout:       2 * time.Second,
		SubscribeRetryDelays:          []time.Duration{20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewSharedSession failed: %v", err)
	}
	defer func() {
		_ = session.Close(context.Background())
	}()

	if _, err := session.RegisterRoute("r1", []string{"hvac/v1/+/up/telemetry"}, func(*paho.Publish) {}); err != nil {
		t.Fatalf("RegisterRoute r1 failed: %v", err)
	}
	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed failed: %v", err)
	}

	// Simulate reconnect advancing generation before resubscribeAll runs for gen2.
	gen2 := session.tracker.OnConnectUp()
	if session.State().Subscribed {
		t.Fatal("expected Subscribed=false immediately after OnConnectUp")
	}

	if _, err := session.RegisterRoute("r2", []string{"hvac/v1/+/down/reply"}, func(*paho.Publish) {}); err != nil {
		t.Fatalf("RegisterRoute r2 failed: %v", err)
	}
	if session.State().Subscribed {
		t.Fatal("registerRoute must NOT mark Subscribed=true before resubscribeAll completes for gen2")
	}

	session.resubscribeAll(ctx, session.manager, gen2)
	if !session.State().Healthy() {
		t.Fatalf("expected session Healthy() after resubscribeAll for gen2, got %+v", session.State())
	}

	// Registering another route after resubscribeAll completed for gen2 keeps Subscribed=true.
	if _, err := session.RegisterRoute("r3", []string{"hvac/v1/+/up/event"}, func(*paho.Publish) {}); err != nil {
		t.Fatalf("RegisterRoute r3 failed: %v", err)
	}
	if !session.State().Healthy() {
		t.Fatalf("expected session Healthy() when registering route after resubscribeAll, got %+v", session.State())
	}
}

func TestSharedSessionManagedRouteStaleErrorDoesNotCloseNewConnection(t *testing.T) {
	t.Parallel()

	certs := writeTestPKI(t)
	broker := startTestMTLSBroker(t, certs.serverTLS)
	defer broker.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := NewSharedSession(ctx, SharedSessionConfig{
		BrokerURL:                     "tls://" + broker.addr,
		ClientID:                      "stale-managed-err-test",
		TLSConfig:                     certs.clientTLS,
		CleanStartOnInitialConnection: true,
		DialTimeout:                   2 * time.Second,
		ConnectRetryDelay:             30 * time.Millisecond,
		SubscribeAttemptTimeout:       2 * time.Second,
		SubscribeRetryDelays:          []time.Duration{20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewSharedSession failed: %v", err)
	}
	defer func() {
		_ = session.Close(context.Background())
	}()

	enteredFirst := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() {
		releaseOnce.Do(func() { close(releaseFirst) })
	}
	t.Cleanup(unlock)

	var firstOnce sync.Once
	deliveredSecond := make(chan string, 1)

	_, err = session.RegisterManagedRoute("custody", []string{"hvac/v1/+/up/telemetry"}, func(ev PublishEvent) error {
		if string(ev.Packet.Payload) == "stale-msg" {
			firstOnce.Do(func() { close(enteredFirst) })
			<-releaseFirst
			return errors.New("stale connection write failed")
		}
		if ackErr := ev.Ack(ev.Packet); ackErr != nil {
			return ackErr
		}
		deliveredSecond <- string(ev.Packet.Payload)
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterManagedRoute failed: %v", err)
	}
	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed failed: %v", err)
	}

	subsBefore := broker.subscribeCalls()
	// Invoke onPublishReceived outside paho's per-connection WaitGroup so autopaho can
	// finish tearing down conn1 and dial conn2 while this stale handler is still in flight.
	staleDone := make(chan error, 1)
	go func() {
		_, callErr := session.onPublishReceived(paho.PublishReceived{
			Packet: &paho.Publish{
				Topic:   "hvac/v1/gw-1/up/telemetry",
				Payload: []byte("stale-msg"),
				QoS:     1,
			},
			Client: &paho.Client{},
		})
		staleDone <- callErr
	}()
	select {
	case <-enteredFirst:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stale handler entry")
	}

	// Drop conn1 and wait until conn2 connects and subscribes.
	broker.dropActiveConn()
	broker.waitSubscribeCallsAtLeast(t, subsBefore+1)
	if err := session.AwaitSubscribed(ctx); err != nil {
		t.Fatalf("AwaitSubscribed on conn2 failed: %v", err)
	}
	subsOnConn2 := broker.subscribeCalls()
	genOnConn2 := session.State().Generation

	// Release stale conn1 handler so it returns an error after conn2 is live.
	unlock()
	if staleErr := <-staleDone; staleErr == nil {
		t.Fatal("expected stale managed handler to return error")
	}
	time.Sleep(50 * time.Millisecond)

	if session.State().Generation != genOnConn2 || !session.State().Healthy() {
		t.Fatalf("stale managed route error disturbed conn2: state=%+v wantGen=%d", session.State(), genOnConn2)
	}
	if got := broker.subscribeCalls(); got != subsOnConn2 {
		t.Fatalf("stale managed route error triggered unexpected reconnect (subs %d -> %d)", subsOnConn2, got)
	}

	broker.pushPublish("hvac/v1/gw-1/up/telemetry", []byte("fresh-msg"), 1)
	if got := recvString(t, deliveredSecond); got != "fresh-msg" {
		t.Fatalf("unexpected fresh payload on conn2: %q", got)
	}
}

func TestSharedSessionHandleConnectionUpConcurrentWithClose(t *testing.T) {
	t.Parallel()

	for range 25 {
		ctx, cancel := context.WithCancel(context.Background())
		s := &SharedSession{
			cfg:           SharedSessionConfig{SubscribeAttemptTimeout: time.Second},
			tracker:       NewConnTracker(),
			sessionCtx:    ctx,
			sessionCancel: cancel,
			routes:        make(map[string]*sessionRoute),
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.handleConnectionUp(nil)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = s.Close(context.Background())
		}()
		close(start)
		wg.Wait()
	}
}

func recvString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for routed MQTT publish")
		return ""
	}
}

type testPKI struct {
	caCertFile     string
	clientCertFile string
	clientKeyFile  string
	clientTLS      *tls.Config
	serverTLS      *tls.Config
}

func writeTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-mqtt-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caFile := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	signLeaf := func(cn string, dns []string, ips []net.IP, eku x509.ExtKeyUsage) (tls.Certificate, string, string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{eku},
			DNSNames:     dns,
			IPAddresses:  ips,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

		certPath := filepath.Join(dir, cn+".crt")
		keyPath := filepath.Join(dir, cn+".key")
		if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return tlsCert, certPath, keyPath
	}

	serverCert, _, _ := signLeaf("server", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, x509.ExtKeyUsageServerAuth)
	_, clientCertPath, clientKeyPath := signLeaf("client", nil, nil, x509.ExtKeyUsageClientAuth)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	clientTLS, err := NewMTLSConfig(TLSConfig{
		CACertFile:     caFile,
		ClientCertFile: clientCertPath,
		ClientKeyFile:  clientKeyPath,
		ServerName:     "localhost",
	})
	if err != nil {
		t.Fatal(err)
	}

	serverTLS := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	return testPKI{
		caCertFile:     caFile,
		clientCertFile: clientCertPath,
		clientKeyFile:  clientKeyPath,
		clientTLS:      clientTLS,
		serverTLS:      serverTLS,
	}
}

type testMTLSBroker struct {
	addr     string
	listener net.Listener
	wg       sync.WaitGroup

	mu           sync.Mutex
	activeConn   net.Conn
	activeWrite  sync.Mutex
	tlsVersion   uint16
	subCalls     [][]string
	unsubTopics  []string
	published    []string
	nextPacketID uint16
	pubAckTotal  int32
	closed       bool
}

func startTestMTLSBroker(t *testing.T, serverTLS *tls.Config) *testMTLSBroker {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	b := &testMTLSBroker{
		addr:         ln.Addr().String(),
		listener:     ln,
		nextPacketID: 1,
	}
	b.wg.Add(1)
	go b.serve()
	return b
}

func (b *testMTLSBroker) serve() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.wg.Add(1)
		go b.handleConn(conn)
	}
}

func (b *testMTLSBroker) handleConn(conn net.Conn) {
	defer b.wg.Done()
	defer conn.Close()

	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		state := tlsConn.ConnectionState()
		b.mu.Lock()
		b.tlsVersion = state.Version
		b.mu.Unlock()
	}

	b.mu.Lock()
	b.activeConn = conn
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		if b.activeConn == conn {
			b.activeConn = nil
		}
		b.mu.Unlock()
	}()

	for {
		pkt, err := packets.ReadPacket(conn)
		if err != nil {
			return
		}
		switch pkt.Type {
		case packets.CONNECT:
			ca := &packets.Connack{
				ReasonCode: 0x00,
				Properties: &packets.Properties{},
			}
			b.writePacket(conn, ca)
		case packets.SUBSCRIBE:
			sub := pkt.Content.(*packets.Subscribe)
			topics := make([]string, 0, len(sub.Subscriptions))
			reasons := make([]byte, len(sub.Subscriptions))
			for i, s := range sub.Subscriptions {
				topics = append(topics, s.Topic)
				reasons[i] = 0x01
			}
			b.mu.Lock()
			b.subCalls = append(b.subCalls, topics)
			b.mu.Unlock()
			b.writePacket(conn, &packets.Suback{
				PacketID:   sub.PacketID,
				Reasons:    reasons,
				Properties: &packets.Properties{},
			})
		case packets.UNSUBSCRIBE:
			unsub := pkt.Content.(*packets.Unsubscribe)
			reasons := make([]byte, len(unsub.Topics))
			b.mu.Lock()
			b.unsubTopics = append(b.unsubTopics, unsub.Topics...)
			b.mu.Unlock()
			b.writePacket(conn, &packets.Unsuback{
				PacketID:   unsub.PacketID,
				Reasons:    reasons,
				Properties: &packets.Properties{},
			})
		case packets.PUBLISH:
			pub := pkt.Content.(*packets.Publish)
			b.mu.Lock()
			b.published = append(b.published, pub.Topic)
			b.mu.Unlock()
			if pub.QoS == 1 {
				b.writePacket(conn, &packets.Puback{
					PacketID:   pub.PacketID,
					Properties: &packets.Properties{},
				})
			}
		case packets.PUBACK:
			atomic.AddInt32(&b.pubAckTotal, 1)
		case packets.PINGREQ:
			b.writePacket(conn, &packets.Pingresp{})
		case packets.DISCONNECT:
			return
		}
	}
}

func (b *testMTLSBroker) writePacket(conn net.Conn, w io.WriterTo) {
	b.activeWrite.Lock()
	defer b.activeWrite.Unlock()
	_, _ = w.WriteTo(conn)
}

func (b *testMTLSBroker) pushPublish(topic string, payload []byte, qos byte) {
	b.mu.Lock()
	conn := b.activeConn
	var packetID uint16
	if qos > 0 {
		b.nextPacketID++
		if b.nextPacketID == 0 {
			b.nextPacketID = 1
		}
		packetID = b.nextPacketID
	}
	b.mu.Unlock()
	if conn == nil {
		return
	}
	b.writePacket(conn, &packets.Publish{
		Topic:      topic,
		Payload:    payload,
		QoS:        qos,
		PacketID:   packetID,
		Properties: &packets.Properties{},
	})
}

func (b *testMTLSBroker) dropActiveConn() {
	b.mu.Lock()
	conn := b.activeConn
	b.activeConn = nil
	b.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (b *testMTLSBroker) lastTLSVersion() uint16 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tlsVersion
}

func (b *testMTLSBroker) subscribeCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subCalls)
}

func (b *testMTLSBroker) lastSubscribeTopics() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subCalls) == 0 {
		return nil
	}
	return slices.Clone(b.subCalls[len(b.subCalls)-1])
}

func (b *testMTLSBroker) unsubscribedTopics() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.unsubTopics)
}

func (b *testMTLSBroker) pubAckCount() int32 {
	return atomic.LoadInt32(&b.pubAckTotal)
}

func (b *testMTLSBroker) waitSubscribeCallsAtLeast(t *testing.T, minCalls int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.subscribeCalls() >= minCalls {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for >= %d SUBSCRIBE calls (got %d)", minCalls, b.subscribeCalls())
}

func (b *testMTLSBroker) waitUnsubscribed(t *testing.T, topic string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(b.unsubscribedTopics(), topic) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for UNSUBSCRIBE on %q (got %v)", topic, b.unsubscribedTopics())
}

func (b *testMTLSBroker) waitPublished(t *testing.T, topic string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		ok := slices.Contains(b.published, topic)
		b.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for PUBLISH to %q", topic)
}

func (b *testMTLSBroker) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	conn := b.activeConn
	b.activeConn = nil
	b.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	_ = b.listener.Close()
	b.wg.Wait()
}
