package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
)

type fakeBrokerTimer struct {
	mu      sync.Mutex
	delay   time.Duration
	fn      func()
	stopped bool
}

func (f *fakeBrokerTimer) Stop() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	wasActive := !f.stopped
	f.stopped = true
	return wasActive
}

func (f *fakeBrokerTimer) fire() {
	if f.fn != nil {
		f.fn()
	}
}

func TestBrokerLogWatcherParsesConnectAndDebouncedDisconnect(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	currentNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	var timersMu sync.Mutex
	var scheduledTimers []*fakeBrokerTimer
	afterFunc := func(d time.Duration, fn func()) brokerDebounceTimer {
		timer := &fakeBrokerTimer{delay: d, fn: fn}
		timersMu.Lock()
		scheduledTimers = append(scheduledTimers, timer)
		timersMu.Unlock()
		return timer
	}

	type disconnectCall struct {
		gatewayID string
		at        time.Time
	}
	var callsMu sync.Mutex
	var calls []disconnectCall
	disconnectedCh := make(chan disconnectCall, 4)

	watcher := NewBrokerLogWatcher(
		t.Context(),
		func(_ context.Context, gatewayID string, disconnectedAt time.Time) {
			call := disconnectCall{gatewayID: gatewayID, at: disconnectedAt}
			callsMu.Lock()
			calls = append(calls, call)
			callsMu.Unlock()
			disconnectedCh <- call
		},
		WithBrokerDisconnectDebounce(25*time.Millisecond),
		WithBrokerLogClock(currentNow),
		withBrokerLogAfterFunc(afterFunc),
	)
	defer watcher.Close()

	watcher.HandleLine("1795081947: New client connected from 172.18.0.5:51234 as connectivity-host-1 (p5, c0, k30, u'mqtt-telemetry-adapter').")
	watcher.HandleLine("1795081948: Client connectivity-host-1 disconnected.")
	watcher.HandleLine("1795081949: Socket error on client <unknown>, disconnecting.")

	clientA := "eg8200-018f3e00"
	watcher.HandlePublish(&paho.Publish{
		Topic:   BrokerNoticeLogTopic,
		Payload: []byte("1795081950: New client connected from 10.0.0.8:44312 as " + clientA + " (p5, c0, k30, u'" + testGatewayA + "')."),
	})

	watcher.HandleLine("1795081951: Client " + clientA + " closed its connection.")
	timersMu.Lock()
	if len(scheduledTimers) != 1 || scheduledTimers[0].delay != 25*time.Millisecond {
		timersMu.Unlock()
		t.Fatalf("expected 1 debounce timer after disconnect, got %+v", scheduledTimers)
	}
	firstTimer := scheduledTimers[0]
	timersMu.Unlock()

	watcher.HandleLine("1795081951: New client connected from 10.0.0.8:44314 as " + clientA + " (p5, c0, k30, u'" + testGatewayA + "').")
	firstTimer.mu.Lock()
	firstStopped := firstTimer.stopped
	firstTimer.mu.Unlock()
	if !firstStopped {
		t.Fatal("expected fast reconnect to stop pending disconnect debounce timer")
	}
	firstTimer.fire()

	callsMu.Lock()
	if len(calls) != 0 {
		t.Fatalf("expected reconnect within debounce to cancel disconnect callback, got %+v", calls)
	}
	callsMu.Unlock()

	watcher.HandleLine("1795081952: New client connected from 10.0.0.8:44315 as " + clientA + "-aux (p5, c0, k30, u'" + testGatewayA + "').")
	watcher.HandleLine("1795081953: Client " + clientA + " disconnected.")
	timersMu.Lock()
	timerCountWithAux := len(scheduledTimers)
	timersMu.Unlock()
	if timerCountWithAux != 1 {
		t.Fatalf("expected active aux client to suppress scheduling a disconnect timer, got %d timers", timerCountWithAux)
	}
	callsMu.Lock()
	if len(calls) != 0 {
		t.Fatalf("expected active aux client to suppress disconnect callback, got %+v", calls)
	}
	callsMu.Unlock()

	clockMu.Lock()
	now = now.Add(5 * time.Second)
	wantDisconnectAt := now
	clockMu.Unlock()

	watcher.HandleLine("1795081958: Client " + clientA + "-aux has exceeded timeout, disconnecting.")
	timersMu.Lock()
	if len(scheduledTimers) != 2 {
		timersMu.Unlock()
		t.Fatalf("expected 2nd debounce timer once last client disconnected, got %d", len(scheduledTimers))
	}
	secondTimer := scheduledTimers[1]
	timersMu.Unlock()
	secondTimer.fire()

	select {
	case got := <-disconnectedCh:
		if got.gatewayID != testGatewayA || !got.at.Equal(wantDisconnectAt) {
			t.Fatalf("disconnect call=%+v, want gateway=%s at=%v", got, testGatewayA, wantDisconnectAt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for debounced disconnect callback")
	}

	realWatcher := NewBrokerLogWatcher(
		t.Context(),
		func(_ context.Context, gatewayID string, disconnectedAt time.Time) {
			disconnectedCh <- disconnectCall{gatewayID: gatewayID, at: disconnectedAt}
		},
		WithBrokerDisconnectDebounce(0),
	)
	defer realWatcher.Close()
	realWatcher.HandleLine("1795081960: Socket error on client " + testGatewayB + ", disconnecting.")
	select {
	case got := <-disconnectedCh:
		if got.gatewayID != testGatewayB {
			t.Fatalf("expected UUIDv7 clientId disconnect for %s, got %s", testGatewayB, got.gatewayID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stateless UUIDv7 disconnect callback")
	}
}

func TestProcessorHandleBrokerGatewayDisconnectFencesAndReportsCoverage(t *testing.T) {
	t.Parallel()

	childID := "018f3e00-4000-7000-8000-000000000011"
	spy := newSpyIdentities(childID)
	telemetry := &fakeTelemetry{}
	presence := map[string]bool{}
	var presenceMu sync.Mutex

	processor, err := NewProcessor(
		spy,
		telemetry,
		WithGatewayPresenceHook(func(gatewayID string, online bool) {
			presenceMu.Lock()
			presence[gatewayID] = online
			presenceMu.Unlock()
		}),
	)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}

	if !processor.sessions().ApplyTransition(SessionTransition{
		GatewayID:    testGatewayA,
		SessionID:    "s1",
		Sequence:     1,
		HasSequence:  true,
		EventTime:    1000,
		HasEventTime: true,
		Online:       true,
	}) {
		t.Fatal("expected initial s1 connect to be accepted")
	}

	if err := processor.HandleBrokerGatewayDisconnect(t.Context(), testGatewayA, time.UnixMilli(900).UTC()); err != nil {
		t.Fatalf("HandleBrokerGatewayDisconnect stale: %v", err)
	}
	snap, ok := processor.sessions().Snapshot(testGatewayA)
	if !ok || !snap.Online {
		t.Fatalf("expected stale broker disconnect to keep session online, got %+v", snap)
	}

	if err := processor.HandleBrokerGatewayDisconnect(t.Context(), testGatewayA, time.UnixMilli(2000).UTC()); err != nil {
		t.Fatalf("HandleBrokerGatewayDisconnect live: %v", err)
	}
	snap, ok = processor.sessions().Snapshot(testGatewayA)
	if !ok || snap.Online {
		t.Fatalf("expected live broker disconnect to mark session offline, got %+v", snap)
	}
	presenceMu.Lock()
	onlineAfterDisconnect := presence[testGatewayA]
	presenceMu.Unlock()
	if onlineAfterDisconnect {
		t.Fatal("expected onGatewayPresence to mark gateway offline")
	}
	telemetry.mu.Lock()
	reports := append([]SourceCoverageReport(nil), telemetry.coverageReports...)
	telemetry.mu.Unlock()
	if len(reports) != 1 {
		t.Fatalf("expected 1 coverage report, got %d (%+v)", len(reports), reports)
	}
	gotReport := reports[0]
	if gotReport.Available || gotReport.ReasonCode == nil || *gotReport.ReasonCode != "GATEWAY_DISCONNECTED" {
		t.Fatalf("unexpected coverage report: %+v", gotReport)
	}
	if len(gotReport.DeviceIDs) != 2 {
		t.Fatalf("expected gateway + child device in coverage report, got %+v", gotReport.DeviceIDs)
	}
}

func TestHandleBrokerGatewayDisconnectIgnoresForwardGatewayClockSkew(t *testing.T) {
	t.Parallel()

	spy := newSpyIdentities(testDeviceID)
	telemetry := &fakeTelemetry{}
	presence := make(map[string]bool)
	var presenceMu sync.Mutex

	processor, err := NewProcessor(
		spy,
		telemetry,
		WithGatewayPresenceHook(func(gatewayID string, online bool) {
			presenceMu.Lock()
			presence[gatewayID] = online
			presenceMu.Unlock()
		}),
	)
	if err != nil {
		t.Fatalf("NewProcessor: %v", err)
	}
	processor.now = func() time.Time { return time.UnixMilli(1000).UTC() }

	statusPayload := []byte(`{"schemaVersion":"2.0","messageId":"` + testMessageID + `","gatewayId":"` + testGatewayA + `","sessionId":"s1","sequence":1,"eventTime":601000,"online":true}`)
	if _, err := processor.Process(t.Context(), "hvac/v1/"+testGatewayA+"/up/status", statusPayload); err != nil {
		t.Fatalf("Process status: %v", err)
	}
	telemetryFrame := telemetryPayloadWithTimestamps(testGatewayA, testMessageID, 1, false, 601500, "METER-01", "active_power")
	if _, err := processor.Process(t.Context(), "hvac/v1/"+testGatewayA+"/up/telemetry", telemetryFrame); err != nil {
		t.Fatalf("Process telemetry: %v", err)
	}

	if err := processor.HandleBrokerGatewayDisconnect(t.Context(), testGatewayA, time.UnixMilli(900).UTC()); err != nil {
		t.Fatalf("HandleBrokerGatewayDisconnect stale: %v", err)
	}
	snap, ok := processor.sessions().Snapshot(testGatewayA)
	if !ok || !snap.Online {
		t.Fatalf("expected stale broker disconnect to keep session online, got %+v", snap)
	}

	if err := processor.HandleBrokerGatewayDisconnect(t.Context(), testGatewayA, time.UnixMilli(2000).UTC()); err != nil {
		t.Fatalf("HandleBrokerGatewayDisconnect skewed: %v", err)
	}
	snap, ok = processor.sessions().Snapshot(testGatewayA)
	if !ok || snap.Online {
		t.Fatalf("expected broker disconnect at server t=2000ms to overcome forward gateway clock skew, got %+v", snap)
	}
	presenceMu.Lock()
	onlineAfterDisconnect := presence[testGatewayA]
	presenceMu.Unlock()
	if onlineAfterDisconnect {
		t.Fatal("expected onGatewayPresence to mark gateway offline after skewed disconnect")
	}
	telemetry.mu.Lock()
	reports := append([]SourceCoverageReport(nil), telemetry.coverageReports...)
	telemetry.mu.Unlock()
	if len(reports) != 1 || reports[0].Available {
		t.Fatalf("expected 1 unavailable coverage report, got %+v", reports)
	}
}
