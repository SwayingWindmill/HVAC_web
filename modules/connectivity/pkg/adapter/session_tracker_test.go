package adapter

import (
	"testing"
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
