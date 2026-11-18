package adapter

import (
	"sync"
	"testing"
	"time"
)

func TestGatewaySessionTrackerFencesDelayedLWTAndSupersededSessions(t *testing.T) {
	t.Parallel()

	tracker := NewGatewaySessionTracker()

	// Conn 1 connects cleanly with session "s1".
	if !tracker.ApplyStatus(StatusEnvelope{
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

	// Gateway rapidly reconnects with new TCP socket session "s2" and seq reset to 1.
	if !tracker.ApplyStatus(StatusEnvelope{
		GatewayID:    testGatewayA,
		SessionID:    "s2",
		Sequence:     1,
		HasSequence:  true,
		EventTime:    1100,
		HasEventTime: true,
		Online:       true,
	}) {
		t.Fatal("expected new s2 session connect (with sequence reset) to be accepted")
	}

	// Broker keepalive finally expires for dead socket "s1" at t=15000 (later wall clock!).
	// Delayed LWT from superseded "s1" MUST NOT mark reconnected gateway offline.
	if tracker.ApplyStatus(StatusEnvelope{
		GatewayID:    testGatewayA,
		SessionID:    "s1",
		Sequence:     2,
		HasSequence:  true,
		EventTime:    15000,
		HasEventTime: true,
		Online:       false,
	}) {
		t.Fatal("delayed LWT from superseded session s1 must be rejected")
	}

	// Replayed old connect from superseded "s1" must also be rejected.
	if tracker.ApplyTransition(SessionTransition{
		GatewayID:    testGatewayA,
		SessionID:    "s1",
		Sequence:     3,
		HasSequence:  true,
		EventTime:    16000,
		HasEventTime: true,
		Online:       true,
	}) {
		t.Fatal("replayed connect from superseded session s1 must be rejected")
	}

	// Unseen third old socket LWT must not tear down active session "s2".
	if tracker.ApplyTransition(SessionTransition{
		GatewayID:    testGatewayA,
		SessionID:    "s-orphan",
		Sequence:     1,
		HasSequence:  true,
		EventTime:    16000,
		HasEventTime: true,
		Online:       false,
	}) {
		t.Fatal("LWT from unseen non-active session must be rejected while s2 is active")
	}

	snap, ok := tracker.Snapshot(testGatewayA)
	if !ok || !snap.Online || snap.SessionID != "s2" {
		t.Fatalf("expected active online s2 session, got %+v (ok=%v)", snap, ok)
	}

	// Same-session out-of-order/duplicate sequences must be rejected.
	if tracker.ApplyStatus(StatusEnvelope{
		GatewayID:    testGatewayA,
		SessionID:    "s2",
		Sequence:     1,
		HasSequence:  true,
		EventTime:    1200,
		HasEventTime: true,
		Online:       false,
	}) {
		t.Fatal("duplicate seq=1 on s2 must be rejected")
	}

	// Advancing sequence on active session "s2" transitions offline legitimately.
	if !tracker.ApplyStatus(StatusEnvelope{
		GatewayID:    testGatewayA,
		SessionID:    "s2",
		Sequence:     2,
		HasSequence:  true,
		EventTime:    2000,
		HasEventTime: true,
		Online:       false,
	}) {
		t.Fatal("expected valid s2 seq=2 offline transition to be accepted")
	}
	snap, _ = tracker.Snapshot(testGatewayA)
	if snap.Online {
		t.Fatal("expected gateway offline after legitimate s2 seq=2 disconnect")
	}
}

func TestGatewaySessionTrackerTelemetryRecencyAndLegacyCompatibility(t *testing.T) {
	t.Parallel()

	tracker := NewGatewaySessionTracker(WithSessionTrackerCapacity(2))

	// Valid telemetry advances telemetry recency without pinning event sequence.
	tracker.ObserveTelemetry(testGatewayA, "", 2000)

	// Strictly older legacy LWT (no sessionId) is rejected against newer telemetry recency.
	if tracker.ApplyTransition(SessionTransition{
		GatewayID:    testGatewayA,
		Sequence:     1,
		HasSequence:  true,
		EventTime:    1500,
		HasEventTime: true,
		Online:       false,
	}) {
		t.Fatal("expected strictly older LWT before latest telemetry timestamp to be rejected")
	}

	// LWT at same/newer millisecond with legacy payload (no sessionId) is accepted.
	if !tracker.ApplyTransition(SessionTransition{
		GatewayID:    testGatewayA,
		Sequence:     2,
		HasSequence:  true,
		EventTime:    2000,
		HasEventTime: true,
		Online:       false,
	}) {
		t.Fatal("expected legacy LWT at >= telemetry timestamp to be accepted")
	}

	// Legacy status omitting sessionId and sequence toggles cleanly.
	if !tracker.ApplyStatus(StatusEnvelope{
		GatewayID: testGatewayB,
		Online:    true,
	}) {
		t.Fatal("expected bare legacy online status to succeed")
	}
	if !tracker.ApplyStatus(StatusEnvelope{
		GatewayID: testGatewayB,
		Online:    false,
	}) {
		t.Fatal("expected bare legacy offline status to succeed")
	}

	// Capacity bound evicts oldest gateway cleanly when a 3rd gateway is tracked.
	tracker.ObserveTelemetry("gw-c", "sc1", 3000)
	if _, ok := tracker.Snapshot(testGatewayA); ok {
		t.Fatal("expected oldest gateway to be evicted when capacity=2 exceeded")
	}
}

func TestGatewaySessionTrackerReplayOffsetsAndLiveEpochs(t *testing.T) {
	t.Parallel()

	tracker := NewGatewaySessionTracker()
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

	// First replay reading takes sampledAt microseconds.
	off1 := tracker.NextReplayOffset(testGatewayA, "METER-01", "active_power", t0)
	if off1 != t0.UnixMicro() {
		t.Fatalf("off1=%d, want %d", off1, t0.UnixMicro())
	}
	// Duplicate or older timestamp must still strictly increase per point.
	off2 := tracker.NextReplayOffset(testGatewayA, "METER-01", "active_power", t0)
	if off2 != off1+1 {
		t.Fatalf("duplicate timestamp off2=%d, want %d", off2, off1+1)
	}
	off3 := tracker.NextReplayOffset(testGatewayA, "METER-01", "active_power", t0.Add(-5*time.Minute))
	if off3 != off2+1 {
		t.Fatalf("older timestamp off3=%d, want %d", off3, off2+1)
	}
	// Separate point code keeps its own independent offset sequence.
	otherPoint := tracker.NextReplayOffset(testGatewayA, "METER-01", "reactive_power", t0)
	if otherPoint != t0.UnixMicro() {
		t.Fatalf("otherPoint=%d, want %d", otherPoint, t0.UnixMicro())
	}
	// Sub-epoch or zero timestamp clamps to at least 1.
	epochZero := tracker.NextReplayOffset(testGatewayA, "METER-01", "zero_ts", time.Unix(0, 0).UTC())
	if epochZero != 1 {
		t.Fatalf("epochZero=%d, want 1", epochZero)
	}

	// Live telemetry epoch starts at 0 and stays 0 while sequences do not regress.
	if got := tracker.LiveTelemetryEpoch(testGatewayA, 10, t0); got != 0 {
		t.Fatalf("initial epoch=%d, want 0", got)
	}
	if got := tracker.LiveTelemetryEpoch(testGatewayA, 10, t0); got != 0 {
		t.Fatalf("duplicate seq epoch=%d, want 0", got)
	}
	if got := tracker.LiveTelemetryEpoch(testGatewayA, 25, t0); got != 0 {
		t.Fatalf("advancing seq epoch=%d, want 0", got)
	}
	// Regression (e.g. gateway rebooted back to seq=1) allocates a new epoch.
	epoch1 := tracker.LiveTelemetryEpoch(testGatewayA, 1, t0.Add(time.Second))
	if epoch1 != t0.Add(time.Second).UnixMilli() {
		t.Fatalf("epoch1=%d, want %d", epoch1, t0.Add(time.Second).UnixMilli())
	}
	// Subsequent monotonic increments stay in epoch1.
	if got := tracker.LiveTelemetryEpoch(testGatewayA, 2, t0.Add(2*time.Second)); got != epoch1 {
		t.Fatalf("post-reboot advancing seq epoch=%d, want %d", got, epoch1)
	}
	// Second reboot within the same millisecond still strictly increments epoch.
	epoch2 := tracker.LiveTelemetryEpoch(testGatewayA, 1, t0.Add(time.Second))
	if epoch2 != epoch1+1 {
		t.Fatalf("same-ms second reboot epoch2=%d, want %d", epoch2, epoch1+1)
	}
	// Other gateway remains at epoch 0.
	if got := tracker.LiveTelemetryEpoch(testGatewayB, 1, t0); got != 0 {
		t.Fatalf("gateway B epoch=%d, want 0", got)
	}
}

func TestGatewaySessionTrackerConcurrentReplayOffsetsAndEpochs(t *testing.T) {
	t.Parallel()

	tracker := NewGatewaySessionTracker()
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

	const workers = 8
	const perWorker = 100
	results := make(chan int64, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				off := tracker.NextReplayOffset(testGatewayA, "METER-01", "active_power", t0)
				results <- off
				_ = tracker.LiveTelemetryEpoch(testGatewayA, int64(i%5), t0.Add(time.Duration(worker)*time.Millisecond))
			}
		}(w)
	}
	wg.Wait()
	close(results)

	seen := make(map[int64]struct{}, workers*perWorker)
	for off := range results {
		if _, dup := seen[off]; dup {
			t.Fatalf("duplicate concurrent replay offset %d", off)
		}
		seen[off] = struct{}{}
	}
}
