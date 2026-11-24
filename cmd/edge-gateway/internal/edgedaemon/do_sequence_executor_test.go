package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

type doWriteOp struct {
	unitID uint8
	coil   uint16
	value  bool
}

type fakeDOCoilBus struct {
	mu           sync.Mutex
	coils        map[uint16]bool
	writes       []doWriteOp
	failOnWrite  map[uint16]error
	onBeforeRead func(coil uint16, coils map[uint16]bool)
}

func newFakeDOCoilBus() *fakeDOCoilBus {
	return &fakeDOCoilBus{
		coils:       map[uint16]bool{},
		failOnWrite: map[uint16]error{},
	}
}

func (b *fakeDOCoilBus) WriteCoil(_ context.Context, unitID uint8, coilAddress uint16, value bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.failOnWrite[coilAddress]; err != nil && value {
		return err
	}
	b.coils[coilAddress] = value
	b.writes = append(b.writes, doWriteOp{unitID: unitID, coil: coilAddress, value: value})
	return nil
}

func (b *fakeDOCoilBus) ReadCoil(_ context.Context, _ uint8, coilAddress uint16) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.onBeforeRead != nil {
		b.onBeforeRead(coilAddress, b.coils)
	}
	return b.coils[coilAddress], nil
}

func (b *fakeDOCoilBus) snapshotWrites() []doWriteOp {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]doWriteOp(nil), b.writes...)
}

type recordedDOEvent struct {
	EventType string
	Data      doDiagnosticData
}

func loadSongyaxuanDOConfig(t *testing.T) DOControlConfig {
	t.Helper()
	cfg, err := LoadFieldPlantConfigFile(filepath.Join("..", "..", "..", "..", "deploy", "sites", "songyaxuan.edge-plant.json"))
	if err != nil {
		t.Fatalf("LoadFieldPlantConfigFile: %v", err)
	}
	cfg.ApplyDefaults()
	return *cfg.DOControl
}

func newTestDOExecutor(
	t *testing.T,
	doCfg DOControlConfig,
	bus *fakeDOCoilBus,
	powerFn func(context.Context, string) (*float64, error),
) (*DOSequenceExecutor, *[]recordedDOEvent, *[]mqttUplinkTelemetryEnvelope) {
	t.Helper()
	var events []recordedDOEvent
	var telemetry []mqttUplinkTelemetryEnvelope
	var mu sync.Mutex
	curNow := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	exec, err := NewDOSequenceExecutor(DOSequenceExecutorOptions{
		GatewayID:          "01a14000-4000-7000-8000-000000000100",
		StateDir:           t.TempDir(),
		Config:             doCfg,
		CoilBus:            bus,
		ChillerPowerReader: powerFn,
		Now:                func() time.Time { return curNow },
		Sleep:              func(_ context.Context, _ time.Duration) error { return nil },
		PublishTelemetry: func(_ context.Context, payload []byte) {
			var env mqttUplinkTelemetryEnvelope
			if json.Unmarshal(payload, &env) == nil {
				mu.Lock()
				telemetry = append(telemetry, env)
				mu.Unlock()
			}
		},
		PublishEvent: func(_ context.Context, payload []byte) {
			var env struct {
				Payload struct {
					EventType string           `json:"eventType"`
					Data      doDiagnosticData `json:"data"`
				} `json:"payload"`
			}
			if json.Unmarshal(payload, &env) == nil {
				mu.Lock()
				events = append(events, recordedDOEvent{EventType: env.Payload.EventType, Data: env.Payload.Data})
				mu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatalf("NewDOSequenceExecutor: %v", err)
	}
	t.Cleanup(exec.Close)
	return exec, &events, &telemetry
}

func mkDOCmd(cmdID, devID, cmdCode, mode, channel string, fence uint64) []byte {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	b, _ := json.Marshal(CommandEnvelopeWire{
		SchemaVersion:  "2.0",
		MessageID:      "0194f000-0000-7000-8000-" + cmdID,
		CommandID:      cmdID,
		IssuedAt:       now.Add(-5 * time.Second).UnixMilli(),
		ExpireAt:       now.Add(2 * time.Minute).UnixMilli(),
		DeviceID:       devID,
		CommandCode:    cmdCode,
		ActuationMode:  mode,
		Channel:        channel,
		ExecutionFence: fence,
		PayloadHash:    "hash-" + cmdID,
	})
	return b
}

func decodeDOReply(t *testing.T, wire []byte) CommandReplyWire {
	t.Helper()
	var rep CommandReplyWire
	if err := json.Unmarshal(wire, &rep); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	return rep
}

func TestDOSequenceExecutorStartRollbackAndPrePulseOpenGuard(t *testing.T) {
	doCfg := loadSongyaxuanDOConfig(t)

	// Case 1: 3rd auxiliary (coil 2 = u13) fails mid-sequence -> foreground fails & reverses u12, u11.
	bus := newFakeDOCoilBus()
	bus.failOnWrite[2] = errors.New("rs485 timeout")
	exec, events, _ := newTestDOExecutor(t, doCfg, bus, nil)

	replyBytes, ok := exec.HandlePayload(t.Context(), mkDOCmd("000000000001", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 1))
	if !ok {
		t.Fatal("expected HandlePayload to accept unit-a start sequence")
	}
	rep := decodeDOReply(t, replyBytes)
	if rep.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected EXECUTED sequence-accepted reply after step 0, got %+v", rep)
	}
	if err := exec.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatalf("WaitForActiveSequences: %v", err)
	}
	if bus.coils[0] || bus.coils[1] || bus.coils[3] {
		t.Fatalf("expected u11/u12 rolled back to false and start pulse untouched, got %v", bus.coils)
	}
	if len(*events) == 0 || (*events)[0].EventType != "START_SEQUENCE_FAILED_ROLLED_BACK" || !slices.Equal((*events)[0].Data.RolledBackChannels, []string{"u12", "u11"}) {
		t.Fatalf("unexpected rollback events: %+v", *events)
	}

	// Foreground step 0 write failure -> immediate FAILED/MODBUS_WRITE_FAILED reply.
	busFg := newFakeDOCoilBus()
	busFg.failOnWrite[0] = errors.New("rs485 timeout")
	execFg, _, _ := newTestDOExecutor(t, doCfg, busFg, nil)
	fgBytes, okFg := execFg.HandlePayload(t.Context(), mkDOCmd("000000000010", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 10))
	if !okFg {
		t.Fatal("expected HandlePayload to handle unit-a foreground fail")
	}
	if fgRep := decodeDOReply(t, fgBytes); fgRep.EdgeStatus != "FAILED" || fgRep.ReasonCode == nil || *fgRep.ReasonCode != "MODBUS_WRITE_FAILED" {
		t.Fatalf("expected immediate MODBUS_WRITE_FAILED on aux[0] failure, got %+v", fgRep)
	}

	// Case 2: All 3 aux coils start, then u12 (coil 1) opens before chiller start pulse -> PRE_PULSE_COIL_OPEN + rollback.
	bus2 := newFakeDOCoilBus()
	openedOnce := false
	bus2.onBeforeRead = func(coil uint16, coils map[uint16]bool) {
		if coil == 1 && !openedOnce {
			openedOnce = true
			coils[1] = false
		}
	}
	exec2, events2, _ := newTestDOExecutor(t, doCfg, bus2, nil)
	reply2, ok2 := exec2.HandlePayload(t.Context(), mkDOCmd("000000000002", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 2))
	if !ok2 || decodeDOReply(t, reply2).EdgeStatus != "EXECUTED" {
		t.Fatalf("expected sequence-accepted reply, got ok=%v raw=%s", ok2, string(reply2))
	}
	if err := exec2.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatalf("WaitForActiveSequences #2: %v", err)
	}
	for _, op := range bus2.snapshotWrites() {
		if op.coil == 3 && op.value {
			t.Fatal("chiller start pulse coil 3 must NOT fire when pre-pulse coil check fails")
		}
	}
	var gotPrePulse, gotRollback bool
	for _, ev := range *events2 {
		if ev.EventType == "PRE_PULSE_COIL_OPEN" {
			gotPrePulse = true
		}
		if ev.EventType == "START_SEQUENCE_FAILED_ROLLED_BACK" {
			gotRollback = true
		}
	}
	if !gotPrePulse || !gotRollback {
		t.Fatalf("expected PRE_PULSE_COIL_OPEN and START_SEQUENCE_FAILED_ROLLED_BACK, got %+v", *events2)
	}
}

func TestDOSequenceExecutorStopFailClosedOnNullAndHighPower(t *testing.T) {
	doCfg := loadSongyaxuanDOConfig(t)

	// Nil power -> CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD
	busNil := newFakeDOCoilBus()
	busNil.coils[0], busNil.coils[1], busNil.coils[2] = true, true, true
	execNil, evNil, _ := newTestDOExecutor(t, doCfg, busNil, func(_ context.Context, _ string) (*float64, error) {
		return nil, nil
	})
	repNil := decodeDOReply(t, func() []byte {
		b, _ := execNil.HandlePayload(t.Context(), mkDOCmd("000000000003", "ctrl-chiller-a", "STOP_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 3))
		return b
	}())
	if repNil.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected STOP sequence accepted, got %+v", repNil)
	}
	if err := execNil.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !busNil.coils[0] || !busNil.coils[1] || !busNil.coils[2] {
		t.Fatal("expected auxiliaries held ON when chiller meter power is null")
	}
	if len(*evNil) == 0 || (*evNil)[len(*evNil)-1].EventType != "CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD" {
		t.Fatalf("expected CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD, got %+v", *evNil)
	}

	// High power (> 5.0 kW) -> CHILLER_STOP_UNCONFIRMED_AUX_HELD
	busHigh := newFakeDOCoilBus()
	busHigh.coils[0], busHigh.coils[1], busHigh.coils[2] = true, true, true
	highKW := 18.5
	execHigh, evHigh, _ := newTestDOExecutor(t, doCfg, busHigh, func(_ context.Context, key string) (*float64, error) {
		if key != "ammeter-unit4" {
			t.Fatalf("expected unit-a meterDeviceKey ammeter-unit4, got %q", key)
		}
		return &highKW, nil
	})
	execHigh.HandlePayload(t.Context(), mkDOCmd("000000000004", "ctrl-chiller-a", "STOP_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 4))
	if err := execHigh.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !busHigh.coils[0] || !busHigh.coils[1] || !busHigh.coils[2] {
		t.Fatal("expected auxiliaries held ON when chiller power remains above stop threshold")
	}
	if len(*evHigh) == 0 || (*evHigh)[len(*evHigh)-1].EventType != "CHILLER_STOP_UNCONFIRMED_AUX_HELD" {
		t.Fatalf("expected CHILLER_STOP_UNCONFIRMED_AUX_HELD, got %+v", *evHigh)
	}
}

func TestDOSequenceExecutorStartupRecoveryIdempotencyAndEmergencyRelease(t *testing.T) {
	doCfg := loadSongyaxuanDOConfig(t)
	stateDir := t.TempDir()
	seed, _ := json.Marshal(DOExecutorPersistedState{
		LastSequence:       10,
		LastFenceByDevice:  map[string]uint64{"ctrl-chiller-a": 4},
		ExecutedCommandIDs: []string{"cmd-prev"},
		PendingPulseCoils:  []uint16{3},
	})
	if err := os.WriteFile(filepath.Join(stateDir, doStateFileName), seed, 0o600); err != nil {
		t.Fatal(err)
	}

	bus := newFakeDOCoilBus()
	bus.coils[3] = true
	bus.coils[0], bus.coils[1], bus.coils[2] = true, true, true
	curNow := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	exec, err := NewDOSequenceExecutor(DOSequenceExecutorOptions{
		GatewayID: "01a14000-4000-7000-8000-000000000100",
		StateDir:  stateDir,
		Config:    doCfg,
		CoilBus:   bus,
		Now:       func() time.Time { return curNow },
		Sleep:     func(_ context.Context, _ time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewDOSequenceExecutor: %v", err)
	}
	defer exec.Close()

	if err := exec.Initialize(t.Context()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if bus.coils[3] {
		t.Fatal("expected latched pending pulse coil 3 cleared to false on startup")
	}

	dupWire, _ := exec.HandlePayload(t.Context(), mkDOCmd("cmd-prev", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 9))
	if rep := decodeDOReply(t, dupWire); rep.EdgeStatus != "REJECTED" || *rep.ReasonCode != "DUPLICATE_COMMAND" {
		t.Fatalf("expected DUPLICATE_COMMAND, got %+v", rep)
	}

	staleWire, _ := exec.HandlePayload(t.Context(), mkDOCmd("cmd-stale", "ctrl-chiller-a", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-a", 4))
	if rep := decodeDOReply(t, staleWire); rep.EdgeStatus != "REJECTED" || *rep.ReasonCode != "STALE_EXECUTION_FENCE" {
		t.Fatalf("expected STALE_EXECUTION_FENCE, got %+v", rep)
	}

	emWire, ok := exec.HandlePayload(t.Context(), mkDOCmd("cmd-emerg", "et1010", "STOP", "EMERGENCY_DO_RELEASE", "all-do", 1))
	if !ok {
		t.Fatal("expected HandlePayload to route emergency release")
	}
	if rep := decodeDOReply(t, emWire); rep.EdgeStatus != "EXECUTED" {
		t.Fatalf("expected EXECUTED on emergency release, got %+v", rep)
	}
	for c := uint16(0); c < 20; c++ {
		if bus.coils[c] {
			t.Fatalf("expected all coils cleared after EMERGENCY_DO_RELEASE, coil %d still true", c)
		}
	}
}

func TestDOSequenceExecutorArbitrarySiteBTwoAuxTrain(t *testing.T) {
	threshold := 8.0
	siteBDO := DOControlConfig{
		DeviceKey:          "siteb-do",
		WinnerControllerID: "siteb-do-uid40",
		UnitID:             40,
		PulseDurationMS:    250,
		ChillerStopPowerKW: &threshold,
		Coils: []DOCoilConfig{
			{Channel: "chwp1", Coil: 10},
			{Channel: "cwp1", Coil: 11},
			{Channel: "start1", Coil: 12},
			{Channel: "stop1", Coil: 13},
		},
		Units: []DOUnitTrainConfig{
			{
				UnitCode:         "unit-1",
				ChillerDeviceKey: "siteb-chiller-1",
				MeterDeviceKey:   "siteb-meter-1",
				Auxiliaries: []DOAuxStepConfig{
					{Role: "chwp", DeviceKey: "siteb-chwp-1", Channel: "chwp1", Coil: 10},
					{Role: "cwp", DeviceKey: "siteb-cwp-1", Channel: "cwp1", Coil: 11},
				},
				StartPulse: DOPulseConfig{Channel: "start1", Coil: 12},
				StopPulse:  DOPulseConfig{Channel: "stop1", Coil: 13},
			},
		},
	}

	bus := newFakeDOCoilBus()
	stopKW := 7.5
	exec, events, _ := newTestDOExecutor(t, siteBDO, bus, func(_ context.Context, meterKey string) (*float64, error) {
		if meterKey != "siteb-meter-1" {
			t.Fatalf("unexpected meterKey %q", meterKey)
		}
		return &stopKW, nil
	})

	startWire, ok := exec.HandlePayload(t.Context(), mkDOCmd("siteb-start-1", "siteb-chiller-1", "START_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-1", 1))
	if !ok {
		t.Fatal("expected Site B start sequence to be handled")
	}
	startRep := decodeDOReply(t, startWire)
	if startRep.EdgeStatus != "EXECUTED" || startRep.ExecutionEvidence == nil || startRep.ExecutionEvidence.WinnerControllerID != "siteb-do-uid40" {
		t.Fatalf("unexpected Site B start reply: %+v", startRep)
	}
	if err := exec.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !bus.coils[10] || !bus.coils[11] || bus.coils[12] {
		t.Fatalf("unexpected Site B coil state after start: %v", bus.coils)
	}

	bus.mu.Lock()
	bus.writes = nil
	bus.mu.Unlock()
	stopWire, ok := exec.HandlePayload(t.Context(), mkDOCmd("siteb-stop-1", "siteb-chiller-1", "STOP_UNIT_SEQUENCE", "UNIT_SEQUENCE", "unit-1", 2))
	if !ok || decodeDOReply(t, stopWire).EdgeStatus != "EXECUTED" {
		t.Fatalf("expected Site B stop sequence accepted")
	}
	if err := exec.WaitForActiveSequences(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*events) > 0 {
		t.Fatalf("unexpected diagnostic error events on clean Site B stop: %+v", *events)
	}
	if bus.coils[10] || bus.coils[11] || bus.coils[13] {
		t.Fatalf("expected Site B aux coils stopped and pulse reset, got %v", bus.coils)
	}
	writes := bus.snapshotWrites()
	wantStopCoils := []doWriteOp{
		{unitID: 40, coil: 13, value: true},
		{unitID: 40, coil: 13, value: false},
		{unitID: 40, coil: 11, value: false},
		{unitID: 40, coil: 10, value: false},
	}
	if !slices.Equal(writes, wantStopCoils) {
		t.Fatalf("Site B stop coil order = %+v, want %+v", writes, wantStopCoils)
	}
}
