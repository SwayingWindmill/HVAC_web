package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
)

func TestBrokerLogWatcherParsesConnectAndDebouncedDisconnect(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	currentNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
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
	)
	defer watcher.Close()

	// Non-gateway internal clients are ignored.
	watcher.HandleLine("1795081947: New client connected from 172.18.0.5:51234 as connectivity-host-1 (p5, c0, k30, u'mqtt-telemetry-adapter').")
	watcher.HandleLine("1795081948: Client connectivity-host-1 disconnected.")
	watcher.HandleLine("1795081949: Socket error on client <unknown>, disconnecting.")

	// Gateway connects using username=gatewayID even when MQTT clientId has a custom suffix.
	clientA := "eg8200-018f3e00"
	watcher.HandlePublish(&paho.Publish{
		Topic:   BrokerNoticeLogTopic,
		Payload: []byte("1795081950: New client connected from 10.0.0.8:44312 as " + clientA + " (p5, c0, k30, u'" + testGatewayA + "')."),
	})

	// Quick disconnect followed by fast reconnect within debounce window cancels offline callback.
	watcher.HandleLine("1795081951: Client " + clientA + " closed its connection.")
	watcher.HandleLine("1795081951: New client connected from 10.0.0.8:44314 as " + clientA + " (p5, c0, k30, u'" + testGatewayA + "').")

	time.Sleep(60 * time.Millisecond)
	callsMu.Lock()
	if len(calls) != 0 {
		t.Fatalf("expected reconnect within debounce to cancel disconnect callback, got %+v", calls)
	}
	callsMu.Unlock()

	// Multiple simultaneous clients for the same gateway do not trigger until ALL disconnect.
	watcher.HandleLine("1795081952: New client connected from 10.0.0.8:44315 as " + clientA + "-aux (p5, c0, k30, u'" + testGatewayA + "').")
	watcher.HandleLine("1795081953: Client " + clientA + " disconnected.")
	time.Sleep(60 * time.Millisecond)
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

	select {
	case got := <-disconnectedCh:
		if got.gatewayID != testGatewayA || !got.at.Equal(wantDisconnectAt) {
			t.Fatalf("disconnect call=%+v, want gateway=%s at=%v", got, testGatewayA, wantDisconnectAt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for debounced disconnect callback")
	}

	// Disconnect after connectivity restart without prior connect notice still works if clientId is UUIDv7.
	watcher.HandleLine("1795081960: Socket error on client " + testGatewayB + ", disconnecting.")
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

	// Gateway connects with session s1 at t=1000ms.
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

	// A stale disconnect timestamped <= connect event time (900ms < 1000ms) is fenced out.
	if err := processor.HandleBrokerGatewayDisconnect(t.Context(), testGatewayA, time.UnixMilli(900).UTC()); err != nil {
		t.Fatalf("HandleBrokerGatewayDisconnect stale: %v", err)
	}
	snap, ok := processor.sessions().Snapshot(testGatewayA)
	if !ok || !snap.Online {
		t.Fatalf("expected stale broker disconnect to keep session online, got %+v", snap)
	}

	// Genuine disconnect after connect (2000ms > 1000ms) flips presence offline and reports child coverage unavailable.
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

