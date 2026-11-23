package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeET1010Bus struct {
	mu       sync.Mutex
	coils    map[uint16]bool
	writes   []et1010WriteOp
	onWrite  func(coil uint16, value bool) error
	readHook func(coil uint16) (bool, error)
}

type et1010WriteOp struct {
	unitID uint8
	coil   uint16
	value  bool
}

func newFakeET1010Bus() *fakeET1010Bus {
	return &fakeET1010Bus{coils: map[uint16]bool{}}
}

func (b *fakeET1010Bus) WriteCoil(_ context.Context, unitID uint8, coil uint16, value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.onWrite != nil {
		if err := b.onWrite(coil, value); err != nil {
			return err
		}
	}
	b.coils[coil] = value
	b.writes = append(b.writes, et1010WriteOp{unitID: unitID, coil: coil, value: value})
	return nil
}

func (b *fakeET1010Bus) ReadCoil(_ context.Context, _ uint8, coil uint16) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readHook != nil {
		return b.readHook(coil)
	}
	return b.coils[coil], nil
}

func (b *fakeET1010Bus) snapshotWrites() []et1010WriteOp {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.writes)
}

func (b *fakeET1010Bus) snapshotCoils() map[uint16]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return maps.Clone(b.coils)
}

func (b *fakeET1010Bus) clearWrites() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = nil
}

type recordedEvent struct {
	EventType string
	Severity  string
	SourceID  string
	TraceID   string
	Data      map[string]any
}

func newTestET1010Executor(t *testing.T, bus *fakeET1010Bus, powerFn func(context.Context, ET1010UnitCode) (*float64, error)) (*ET1010Executor, *[]recordedEvent, *[]mqttUplinkTelemetryEnvelope) {
	t.Helper()
	var events []recordedEvent
	var telemetries []mqttUplinkTelemetryEnvelope
	var mu sync.Mutex
	fixedNow := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	exec, err := NewET1010Executor(ET1010ExecutorOptions{
		GatewayID:          "01a14000-4000-7000-8000-000000000100",
		StateDir:           t.TempDir(),
		CoilBus:            bus,
		ChillerPowerReader: powerFn,
		PulseDurationMS:    5,
		Now:                func() time.Time { return fixedNow },
		Sleep:              func(_ context.Context, _ time.Duration) error { return nil },
		PublishTelemetry: func(_ context.Context, payload []byte) {
			var env mqttUplinkTelemetryEnvelope
			if json.Unmarshal(payload, &env) == nil {
				mu.Lock()
				telemetries = append(telemetries, env)
				mu.Unlock()
			}
		},
		PublishEvent: func(_ context.Context, payload []byte) {
			var env mqttUplinkEventEnvelope
			if json.Unmarshal(payload, &env) == nil {
				var data map[string]any
				_ = json.Unmarshal(env.Payload.Data, &data)
				mu.Lock()
				events = append(events, recordedEvent{
					EventType: env.Payload.EventType,
					Severity:  env.Payload.Severity,
					SourceID:  env.Payload.SourceID,
					TraceID:   env.TraceID,
					Data:      data,
				})
				mu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatalf("NewET1010Executor: %v", err)
	}
	t.Cleanup(exec.Close)
	return exec, &events, &telemetries
}

func mkETCmd(id, device, code, mode, channel string, fence uint64) CommandEnvelopeWire {
	nowMs := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC).UnixMilli()
	return CommandEnvelopeWire{
		SchemaVersion:  "2.0",
		MessageID:      "0194f000-0000-7000-8000-000000000001",
		CommandID:      id,
		TraceID:        "trace-" + id,
		IssuedAt:       nowMs - 1000,
		ExpireAt:       nowMs + 60000,
		DeviceID:       device,
		CommandCode:    code,
		ActuationMode:  mode,
		Channel:        channel,
		ExecutionFence: fence,
		PayloadHash:    "sha-" + id,
	}
}

func TestET1010ExecutorStartRollbackAndPrePulseOpenGuard(t *testing.T) {
	bus := newFakeET1010Bus()
	bus.onWrite = func(coil uint16, value bool) error {
		if coil == 2 && value {
			return errors.New("tower coil timeout")
		}
		return nil
	}
	exec, events, _ := newTestET1010Executor(t, bus, nil)
	if err := exec.Initialize(t.Context()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	bus.clearWrites()

	reply := exec.HandleCommand(t.Context(), mkETCmd("cmd-start-fail", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 1))
	if reply.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected EXECUTED accepted reply, got %+v", reply)
	}
	if err := exec.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatalf("WaitForActiveSequences: %v", err)
	}

	writes := bus.snapshotWrites()
	gotPairs := make([][2]any, 0, len(writes))
	for _, w := range writes {
		gotPairs = append(gotPairs, [2]any{w.coil, w.value})
	}
	wantPairs := [][2]any{{uint16(0), true}, {uint16(1), true}, {uint16(1), false}, {uint16(0), false}}
	if !slices.Equal(gotPairs, wantPairs) {
		t.Fatalf("unexpected rollback writes: got %+v, want %+v", gotPairs, wantPairs)
	}
	if len(*events) != 1 || (*events)[0].EventType != "START_SEQUENCE_FAILED_ROLLED_BACK" || (*events)[0].Severity != "CRITICAL" {
		t.Fatalf("expected CRITICAL START_SEQUENCE_FAILED_ROLLED_BACK event, got %+v", *events)
	}

	bus2 := newFakeET1010Bus()
	bus2.readHook = func(coil uint16) (bool, error) {
		if coil == 1 {
			return false, nil
		}
		return bus2.coils[coil], nil
	}
	exec2, events2, _ := newTestET1010Executor(t, bus2, nil)
	_ = exec2.Initialize(t.Context())
	bus2.clearWrites()
	reply2 := exec2.HandleCommand(t.Context(), mkETCmd("cmd-prepulse-open", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 2))
	if reply2.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected EXECUTED accepted reply, got %+v", reply2)
	}
	if err := exec2.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatalf("WaitForActiveSequences: %v", err)
	}
	coils2 := bus2.snapshotCoils()
	for _, c := range []uint16{0, 1, 2, 3} {
		if coils2[c] {
			t.Fatalf("coil %d must be false after pre-pulse open rollback", c)
		}
	}
	if len(*events2) < 2 || (*events2)[0].EventType != "PRE_PULSE_COIL_OPEN" || (*events2)[1].EventType != "START_SEQUENCE_FAILED_ROLLED_BACK" {
		t.Fatalf("expected PRE_PULSE_COIL_OPEN + START_SEQUENCE_FAILED_ROLLED_BACK, got %+v", *events2)
	}
}

func TestET1010ExecutorStopFailClosedOnNullAndHighPower(t *testing.T) {
	bus := newFakeET1010Bus()
	bus.coils[0] = true
	bus.coils[1] = true
	bus.coils[2] = true
	calls := 0
	exec, events, _ := newTestET1010Executor(t, bus, func(_ context.Context, _ ET1010UnitCode) (*float64, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("rs485 timeout")
		}
		return nil, nil
	})
	_ = exec.Initialize(t.Context())
	bus.clearWrites()

	reply := exec.HandleCommand(t.Context(), mkETCmd("cmd-stop-null", "ctrl-chiller-a", "STOP_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 10))
	if reply.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected EXECUTED, got %+v", reply)
	}
	if err := exec.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatalf("WaitForActiveSequences: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 power checks (1 initial + 2 rechecks), got %d", calls)
	}
	coils := bus.snapshotCoils()
	if !coils[0] || !coils[1] || !coils[2] || coils[16] {
		t.Fatalf("expected auxiliaries held ON and stop pulse reset to false, got %+v", coils)
	}
	if len(*events) != 1 || (*events)[0].EventType != "CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD" || (*events)[0].Severity != "CRITICAL" {
		t.Fatalf("expected CRITICAL CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD, got %+v", *events)
	}

	bus2 := newFakeET1010Bus()
	bus2.coils[0] = true
	bus2.coils[1] = true
	bus2.coils[2] = true
	high := 18.5
	exec2, events2, _ := newTestET1010Executor(t, bus2, func(_ context.Context, _ ET1010UnitCode) (*float64, error) {
		return &high, nil
	})
	_ = exec2.Initialize(t.Context())
	_ = exec2.HandleCommand(t.Context(), mkETCmd("cmd-stop-high", "ctrl-chiller-a", "STOP_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 11))
	_ = exec2.WaitForActiveSequences(t.Context())
	coils2 := bus2.snapshotCoils()
	if !coils2[0] || !coils2[1] || !coils2[2] {
		t.Fatalf("expected auxiliaries held ON when chiller power > 5kW, got %+v", coils2)
	}
	if len(*events2) != 1 || (*events2)[0].EventType != "CHILLER_STOP_UNCONFIRMED_AUX_HELD" {
		t.Fatalf("expected CHILLER_STOP_UNCONFIRMED_AUX_HELD, got %+v", *events2)
	}
}

func TestET1010ExecutorStartupRecoveryIdempotencyAndEmergencyRelease(t *testing.T) {
	stateDir := t.TempDir()
	seed, _ := json.Marshal(ET1010PersistedState{
		LastSequence:       42,
		LastFenceByDevice:  map[string]uint64{"ctrl-chiller-a": 4},
		ExecutedCommandIDs: []string{"old-cmd"},
		PendingPulseCoils:  []uint16{3},
	})
	if err := os.WriteFile(filepath.Join(stateDir, et1010StateFileName), seed, 0o600); err != nil {
		t.Fatal(err)
	}

	bus := newFakeET1010Bus()
	bus.coils[3] = true
	exec, err := NewET1010Executor(ET1010ExecutorOptions{
		GatewayID: "01a14000-4000-7000-8000-000000000100",
		StateDir:  stateDir,
		CoilBus:   bus,
		Now:       func() time.Time { return time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC) },
		Sleep:     func(_ context.Context, _ time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer exec.Close()
	if err := exec.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	if bus.snapshotCoils()[3] {
		t.Fatal("expected Initialize to reset latched pulse coil 3")
	}

	pulseCmd := mkETCmd("cmd-pulse-a", "ctrl-chiller-a", "START", "PULSE", "u14", 5)
	pulseCmd.PulseDurationMS = 500
	rep := exec.HandleCommand(t.Context(), pulseCmd)
	if rep.EdgeStatus != "EXECUTED" || rep.ExecutionEvidence == nil || !rep.ExecutionEvidence.ValidPulseDualReadback() {
		t.Fatalf("expected valid pulse dual-readback evidence, got %+v", rep)
	}

	dupRep := exec.HandleCommand(t.Context(), mkETCmd("old-cmd", "ctrl-chiller-a", "START", "PULSE", "u14", 6))
	if dupRep.EdgeStatus != "REJECTED" || dupRep.ReasonCode == nil || *dupRep.ReasonCode != "DUPLICATE_COMMAND" {
		t.Fatalf("expected DUPLICATE_COMMAND, got %+v", dupRep)
	}

	staleRep := exec.HandleCommand(t.Context(), mkETCmd("cmd-stale", "ctrl-chiller-a", "STOP", "PULSE", "u51", 4))
	if staleRep.EdgeStatus != "REJECTED" || staleRep.ReasonCode == nil || *staleRep.ReasonCode != "STALE_EXECUTION_FENCE" {
		t.Fatalf("expected STALE_EXECUTION_FENCE, got %+v", staleRep)
	}

	slowSleepStarted := make(chan struct{})
	bus3 := newFakeET1010Bus()
	exec3, err := NewET1010Executor(ET1010ExecutorOptions{
		GatewayID: "01a14000-4000-7000-8000-000000000100",
		StateDir:  t.TempDir(),
		CoilBus:   bus3,
		Now:       func() time.Time { return time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC) },
		Sleep: func(ctx context.Context, d time.Duration) error {
			if d > time.Second {
				close(slowSleepStarted)
				<-ctx.Done()
				return context.Cause(ctx)
			}
			return nil
		},
		SequenceTimings: ET1010SequenceTimings{PumpToCoolingPumpMS: 30000},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer exec3.Close()
	_ = exec3.Initialize(t.Context())
	startRep := exec3.HandleCommand(t.Context(), mkETCmd("cmd-seq-e", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 1))
	if startRep.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected start seq accepted: %+v", startRep)
	}
	<-slowSleepStarted
	emCmd := mkETCmd("cmd-emerg", "et1010", "STOP", "EMERGENCY_DO_RELEASE", "all-do", 2)
	emCmd.PointCode = "emergency_stop_all"
	emRep := exec3.HandleCommand(t.Context(), emCmd)
	if emRep.EdgeStatus != "EXECUTED" || !strings.Contains(*emRep.ExecutionEvidence.Applied.Text, "all-do:0") {
		t.Fatalf("expected all-do:0 EXECUTED, got %+v", emRep)
	}
	coils3 := bus3.snapshotCoils()
	for coil := range uint16(20) {
		if coils3[coil] {
			t.Fatalf("expected coil %d=false after EMERGENCY_DO_RELEASE", coil)
		}
	}
}
