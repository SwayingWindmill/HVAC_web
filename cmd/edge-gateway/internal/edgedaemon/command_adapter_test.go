package edgedaemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simonvetter/modbus"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type adapterTestModbusHandler struct {
	mu      sync.Mutex
	holding map[uint16]uint16
	stuck   map[uint16]uint16
	writes  atomic.Int64
}

func (h *adapterTestModbusHandler) HandleCoils(*modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *adapterTestModbusHandler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *adapterTestModbusHandler) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *adapterTestModbusHandler) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.IsWrite {
		h.writes.Add(1)
		for i, v := range req.Args {
			addr := req.Addr + uint16(i)
			if _, isStuck := h.stuck[addr]; !isStuck {
				h.holding[addr] = v
			}
		}
		return nil, nil
	}
	out := make([]uint16, req.Quantity)
	for i := range req.Quantity {
		addr := req.Addr + uint16(i)
		if stuckVal, isStuck := h.stuck[addr]; isStuck {
			out[i] = stuckVal
		} else {
			out[i] = h.holding[addr]
		}
	}
	return out, nil
}

func (h *adapterTestModbusHandler) setStuck(addr uint16, stuck bool, val uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if stuck {
		h.stuck[addr] = val
		return
	}
	delete(h.stuck, addr)
}

func startAdapterTestModbusTCP(t *testing.T, initial map[uint16]uint16) (string, *adapterTestModbusHandler) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	h := &adapterTestModbusHandler{
		holding: initial,
		stuck:   map[uint16]uint16{},
	}
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + addr, Timeout: time.Second}, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return addr, h
}

func dispatchViaDaemonCycle(
	t *testing.T,
	daemon *Daemon,
	clockMu *sync.Mutex,
	curNow *time.Time,
	cycleStep time.Duration,
	env CommandEnvelopeWire,
) CommandReplyWire {
	t.Helper()
	rawEnv, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		raw []byte
		ok  bool
	}
	resCh := make(chan result, 1)
	go func() {
		raw, ok := daemon.HandleCommandPayload(ctx, rawEnv)
		resCh <- result{raw: raw, ok: ok}
	}()

	select {
	case <-daemon.CommandAdapter().PendingSignal():
		clockMu.Lock()
		*curNow = curNow.Add(cycleStep)
		clockMu.Unlock()
		if _, err := daemon.StepCycle(ctx); err != nil {
			t.Fatalf("StepCycle() error = %v", err)
		}
	case r := <-resCh:
		if !r.ok {
			t.Fatal("HandleCommandPayload returned ok=false")
		}
		var reply CommandReplyWire
		if err := json.Unmarshal(r.raw, &reply); err != nil {
			t.Fatalf("unmarshal immediate reply: %v", err)
		}
		return reply
	}

	r := <-resCh
	if !r.ok {
		t.Fatal("HandleCommandPayload returned ok=false after cycle")
	}
	var reply CommandReplyWire
	if err := json.Unmarshal(r.raw, &reply); err != nil {
		t.Fatalf("unmarshal cycle reply: %v", err)
	}
	return reply
}

func TestCommandAdapterLeasedExecutionHydraulicArbitrationReadbackAndWAL(t *testing.T) {
	endpoint, modbusHandler := startAdapterTestModbusTCP(t, map[uint16]uint16{
		10: 0,   // pump run_state = false (no flow initially)
		11: 0,   // pump flow_m3h = 0.0
		12: 300, // pump freq_sp = 30.0 Hz
		30: 0,   // chiller run_state = false
		31: 0,   // chiller start_cmd = false
		32: 70,  // chiller chw_sp = 7.0 C
	})

	uid := 1
	timeoutMS := 500
	retries := 0
	writeFC6 := 6
	bit0 := 0
	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	policy.OfflineGraceSeconds = 60
	policy.MinPumpFrequencyHz = 20
	policy.MaxPumpFrequencyHz = 50

	stateDir := t.TempDir()
	var clockMu sync.Mutex
	curNow := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}

	cfg := Config{
		GatewayID:      "0193f000-2000-7000-8000-000000000957",
		StateDir:       stateDir,
		AutonomyPolicy: &policy,
		Drivers: []edgecontrol.ModbusDriverInput{
			{
				DeviceKey:         "pump-01",
				DriverType:        "MODBUS_TCP",
				Endpoint:          &endpoint,
				UnitID:            &uid,
				CapabilityProfile: string(edgecontrol.ProfileVariableSpeedPump),
				TimeoutMS:         &timeoutMS,
				Retries:           &retries,
				Registers: []edgecontrol.ModbusRegisterInput{
					{PointCode: "run_state", SemanticChannel: string(edgecontrol.SemanticRunState), FunctionCode: 3, Address: 10, RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "STOPPED", "1": "RUNNING"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "flow_m3h", SemanticChannel: string(edgecontrol.SemanticFlow), FunctionCode: 3, Address: 11, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
					{PointCode: "freq_sp", SemanticChannel: string(edgecontrol.SemanticFrequencySetpoint), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: 12, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
				},
			},
			{
				DeviceKey:         "chiller-01",
				DriverType:        "MODBUS_TCP",
				Endpoint:          &endpoint,
				UnitID:            &uid,
				CapabilityProfile: string(edgecontrol.ProfileChiller),
				TimeoutMS:         &timeoutMS,
				Retries:           &retries,
				Registers: []edgecontrol.ModbusRegisterInput{
					{PointCode: "run_state", SemanticChannel: string(edgecontrol.SemanticRunState), FunctionCode: 3, Address: 30, RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "STOPPED", "1": "RUNNING"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "start_cmd", SemanticChannel: string(edgecontrol.SemanticStartCommand), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: 31, RegisterCount: 1, RawType: "BIT_BOOL", BitIndex: &bit0, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "chw_sp", SemanticChannel: string(edgecontrol.SemanticTemperatureSetpoint), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: 32, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
				},
			},
		},
	}
	cfg.ApplyDefaults("1.0.0")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: nowFn})
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	defer func() { _ = daemon.Close() }()

	// Initial poll cycle populates ProcessImage with pump stopped / zero flow.
	if _, err := daemon.StepCycle(t.Context()); err != nil {
		t.Fatalf("initial StepCycle() error = %v", err)
	}

	// 1. Hydraulic interlock denies chiller START when chilled water pump has no flow.
	denyEnv := CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-chiller-start-deny",
		CommandID:      "cmd-chiller-start-deny",
		TraceID:        "trace-1",
		IssuedAt:       nowFn().UnixMilli(),
		ExpireAt:       nowFn().Add(time.Minute).UnixMilli(),
		DeviceID:       "chiller-01",
		CommandCode:    "START",
		Params:         map[string]float64{},
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: 1,
		PayloadHash:    "hash-deny",
	}
	denyReply := dispatchViaDaemonCycle(t, daemon, &clockMu, &curNow, time.Second, denyEnv)
	if denyReply.EdgeStatus != "REJECTED" || denyReply.ReasonCode == nil || *denyReply.ReasonCode != "HYDRAULIC_INTERLOCK_NO_CHW_FLOW" {
		t.Fatalf("expected REJECTED HYDRAULIC_INTERLOCK_NO_CHW_FLOW, got %+v", denyReply)
	}
	if modbusHandler.writes.Load() != 0 {
		t.Fatalf("denied command must perform 0 Modbus writes, got %d", modbusHandler.writes.Load())
	}

	// 2. Pump frequency below 20 Hz is clamped by HydraulicInterlockController, written & read back, and remains leased.
	expireLease := nowFn().Add(60 * time.Second)
	clampEnv := CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-pump-clamp",
		CommandID:      "cmd-pump-clamp",
		TraceID:        "trace-2",
		IssuedAt:       nowFn().UnixMilli(),
		ExpireAt:       expireLease.UnixMilli(),
		DeviceID:       "pump-01",
		CommandCode:    "SET_FREQUENCY",
		Params:         map[string]float64{"frequencyHz": 10},
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: 1,
		PayloadHash:    "hash-clamp",
	}
	clampReply := dispatchViaDaemonCycle(t, daemon, &clockMu, &curNow, time.Second, clampEnv)
	if clampReply.EdgeStatus != "EXECUTED" || clampReply.ReasonCode != nil || clampReply.ExecutionEvidence == nil || !clampReply.ExecutionEvidence.ValidExecuted() {
		t.Fatalf("expected valid EXECUTED clamped reply, got %+v", clampReply)
	}
	ev := clampReply.ExecutionEvidence
	if ev.Requested.Number == nil || *ev.Requested.Number != 10 ||
		ev.Effective == nil || ev.Effective.Number == nil || *ev.Effective.Number != 20 ||
		ev.Applied == nil || ev.Applied.Number == nil || *ev.Applied.Number != 20 ||
		ev.Readback == nil || ev.Readback.Number == nil || *ev.Readback.Number != 20 ||
		ev.LocalReadbackVerified == nil || !*ev.LocalReadbackVerified ||
		ev.LocalReadbackAt == nil || *ev.LocalReadbackAt <= 0 ||
		ev.LeaseExpiresAt == nil || *ev.LeaseExpiresAt != expireLease.UnixMilli() {
		t.Fatalf("unexpected execution evidence after clamp+readback: %+v", ev)
	}
	wantConstraint := commandmodel.EdgeConstraintEvidence{
		ControllerID: "hydraulic-interlock",
		Reason:       "PUMP_FREQUENCY_LIMIT",
	}
	if len(ev.Constraints) == 0 || ev.Constraints[0] != wantConstraint {
		t.Fatalf("expected constraint %+v, got %+v", wantConstraint, ev.Constraints)
	}

	// Verify EDGE-011 lease stays active across subsequent cycle before expiry and expires afterwards.
	if active := daemon.Host().IntentStore().Active(nowFn().Add(10 * time.Second)); len(active) != 1 || active[0].ID != "cmd-pump-clamp" {
		t.Fatalf("expected numeric setpoint lease retained before expiry, got %+v", active)
	}
	if active := daemon.Host().IntentStore().Active(expireLease.Add(time.Second)); len(active) != 0 {
		t.Fatalf("expected numeric setpoint lease released after expiry, got %+v", active)
	}

	// 3. Stuck Modbus register triggers LOCAL_READBACK_MISMATCH -> FAILED and revokes intent.
	modbusHandler.setStuck(32, true, 70) // stuck at 7.0 C
	mismatchEnv := CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-chiller-mismatch",
		CommandID:      "cmd-chiller-mismatch",
		TraceID:        "trace-3",
		IssuedAt:       nowFn().UnixMilli(),
		ExpireAt:       nowFn().Add(time.Minute).UnixMilli(),
		DeviceID:       "chiller-01",
		CommandCode:    "SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
		Params:         map[string]float64{"setpointC": 9.5},
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: 2,
		PayloadHash:    "hash-mismatch",
	}
	mismatchReply := dispatchViaDaemonCycle(t, daemon, &clockMu, &curNow, time.Second, mismatchEnv)
	if mismatchReply.EdgeStatus != "FAILED" || mismatchReply.ReasonCode == nil || *mismatchReply.ReasonCode != "LOCAL_READBACK_MISMATCH" {
		t.Fatalf("expected FAILED LOCAL_READBACK_MISMATCH, got %+v", mismatchReply)
	}
	if mismatchReply.ExecutionEvidence == nil || mismatchReply.ExecutionEvidence.LocalReadbackVerified == nil || *mismatchReply.ExecutionEvidence.LocalReadbackVerified ||
		mismatchReply.ExecutionEvidence.Readback == nil || mismatchReply.ExecutionEvidence.Readback.Number == nil || *mismatchReply.ExecutionEvidence.Readback.Number != 7.0 {
		t.Fatalf("expected LocalReadbackVerified=false and Readback=7.0, got %+v", mismatchReply.ExecutionEvidence)
	}
	if active := daemon.Host().IntentStore().Active(nowFn()); len(active) != 0 {
		t.Fatalf("expected failed write to revoke intent immediately, got %+v", active)
	}

	// 4. EDGE-003 hard real-time point/command is blocked without touching Modbus.
	writesBefore := modbusHandler.writes.Load()
	safetyEnv := CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-safety",
		CommandID:      "cmd-safety",
		IssuedAt:       nowFn().UnixMilli(),
		ExpireAt:       nowFn().Add(time.Minute).UnixMilli(),
		DeviceID:       "chiller-01",
		PointCode:      "plc_estop_trip",
		CommandCode:    "STOP",
		Params:         map[string]float64{},
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: 3,
		PayloadHash:    "hash-safety",
	}
	rawSafety, _ := json.Marshal(safetyEnv)
	rawReply, ok := daemon.HandleCommandPayload(t.Context(), rawSafety)
	if !ok {
		t.Fatal("expected immediate reply for EDGE-003 rejection")
	}
	var safetyReply CommandReplyWire
	if err := json.Unmarshal(rawReply, &safetyReply); err != nil {
		t.Fatal(err)
	}
	if safetyReply.EdgeStatus != "REJECTED" || safetyReply.ReasonCode == nil || *safetyReply.ReasonCode != edgecontrol.ReasonHardRealtimeSafetyBoundary {
		t.Fatalf("expected REJECTED HARD_REALTIME_SAFETY_BOUNDARY, got %+v", safetyReply)
	}
	if modbusHandler.writes.Load() != writesBefore {
		t.Fatalf("EDGE-003 rejection must not write Modbus, writes=%d -> %d", writesBefore, modbusHandler.writes.Load())
	}

	// 5. WAL recovery: append an in-flight MAY_EXECUTE entry, restart adapter, verify replay, mismatch, fence, and crash recovery.
	if err := daemon.Close(); err != nil {
		t.Fatalf("daemon.Close() error = %v", err)
	}
	walPath := filepath.Join(stateDir, commandWALFileName)
	inflightLine, _ := json.Marshal(commandWALEntry{
		Type:      commandWALRecordEntry,
		CommandID: "cmd-crash-inflight",
		Record: &commandLedgerRecord{
			DeviceID:       "pump-01",
			PayloadHash:    "hash-crash",
			ExecutionFence: 10,
			State:          commandLedgerMayExecute,
		},
	})
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(inflightLine, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	reopened, err := NewCommandAdapter(cfg.GatewayID, stateDir, daemon.Host(), nowFn, nil)
	if err != nil {
		t.Fatalf("reopen NewCommandAdapter() error = %v", err)
	}
	defer func() { _ = reopened.Close() }()

	// Idempotent replay of completed command must return identical reply without re-execution.
	rawClamp, _ := json.Marshal(clampEnv)
	replayReply, ok := reopened.Handle(t.Context(), rawClamp)
	if !ok || replayReply.EdgeStatus != "EXECUTED" || replayReply.CommandID != clampEnv.CommandID {
		t.Fatalf("expected idempotent EXECUTED replay, got ok=%v reply=%+v", ok, replayReply)
	}
	if modbusHandler.writes.Load() != writesBefore {
		t.Fatalf("idempotent replay must not execute Modbus write")
	}

	// Altered payloadHash for same commandId -> PAYLOAD_MISMATCH.
	badHashEnv := clampEnv
	badHashEnv.PayloadHash = "tampered-hash"
	rawBadHash, _ := json.Marshal(badHashEnv)
	badHashReply, ok := reopened.Handle(t.Context(), rawBadHash)
	if !ok || badHashReply.EdgeStatus != "REJECTED" || badHashReply.ReasonCode == nil || *badHashReply.ReasonCode != "PAYLOAD_MISMATCH" {
		t.Fatalf("expected PAYLOAD_MISMATCH, got %+v", badHashReply)
	}

	// New command with stale fence (<= maxFence 10 for pump-01) -> STALE_FENCE.
	staleEnv := clampEnv
	staleEnv.CommandID = "cmd-stale-fence"
	staleEnv.ExecutionFence = 9
	rawStale, _ := json.Marshal(staleEnv)
	staleReply, ok := reopened.Handle(t.Context(), rawStale)
	if !ok || staleReply.EdgeStatus != "REJECTED" || staleReply.ReasonCode == nil || *staleReply.ReasonCode != "STALE_FENCE" {
		t.Fatalf("expected STALE_FENCE, got %+v", staleReply)
	}

	// Crash-recovered MAY_EXECUTE command -> FAILED EDGE_OUTCOME_UNKNOWN.
	crashEnv := CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-crash-retry",
		CommandID:      "cmd-crash-inflight",
		IssuedAt:       nowFn().UnixMilli(),
		ExpireAt:       nowFn().Add(time.Minute).UnixMilli(),
		DeviceID:       "pump-01",
		CommandCode:    "SET_FREQUENCY",
		Params:         map[string]float64{"frequencyHz": 35},
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: 10,
		PayloadHash:    "hash-crash",
	}
	rawCrash, _ := json.Marshal(crashEnv)
	crashReply, ok := reopened.Handle(t.Context(), rawCrash)
	if !ok || crashReply.EdgeStatus != "FAILED" || crashReply.ReasonCode == nil || *crashReply.ReasonCode != "EDGE_OUTCOME_UNKNOWN" {
		t.Fatalf("expected FAILED EDGE_OUTCOME_UNKNOWN for MAY_EXECUTE WAL entry, got %+v", crashReply)
	}
}

func TestCommandAdapterEnforcesTTLExpiryAndMaxTTL(t *testing.T) {
	endpoint, modbusHandler := startAdapterTestModbusTCP(t, map[uint16]uint16{
		10: 0,
		11: 0,
		12: 300,
	})
	uid := 1
	timeoutMS := 500
	retries := 0
	writeFC6 := 6
	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	stateDir := t.TempDir()

	var clockMu sync.Mutex
	curNow := time.Date(2026, 11, 23, 3, 15, 0, 0, time.UTC)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}

	cfg := Config{
		GatewayID:      "0193f000-2000-7000-8000-000000000960",
		StateDir:       stateDir,
		AutonomyPolicy: &policy,
		Drivers: []edgecontrol.ModbusDriverInput{
			{
				DeviceKey:         "pump-01",
				DriverType:        "MODBUS_TCP",
				Endpoint:          &endpoint,
				UnitID:            &uid,
				CapabilityProfile: string(edgecontrol.ProfileVariableSpeedPump),
				TimeoutMS:         &timeoutMS,
				Retries:           &retries,
				Registers: []edgecontrol.ModbusRegisterInput{
					{PointCode: "run_state", SemanticChannel: string(edgecontrol.SemanticRunState), FunctionCode: 3, Address: 10, RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "STOPPED", "1": "RUNNING"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "flow_m3h", SemanticChannel: string(edgecontrol.SemanticFlow), FunctionCode: 3, Address: 11, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
					{PointCode: "freq_sp", SemanticChannel: string(edgecontrol.SemanticFrequencySetpoint), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: 12, RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
				},
			},
		},
	}
	cfg.ApplyDefaults("1.0.0")
	daemon, err := NewDaemon(cfg, DaemonOptions{Now: nowFn})
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	defer func() { _ = daemon.Close() }()

	// 1. Already-expired command returns COMMAND_EXPIRED immediately with 0 writes.
	expiredEnv := CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-expired",
		CommandID:      "cmd-expired",
		IssuedAt:       nowFn().Add(-2 * time.Minute).UnixMilli(),
		ExpireAt:       nowFn().Add(-time.Second).UnixMilli(),
		DeviceID:       "pump-01",
		CommandCode:    "SET_FREQUENCY",
		Params:         map[string]float64{"frequencyHz": 40},
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: 1,
		PayloadHash:    "hash-expired",
	}
	rawExpired, _ := json.Marshal(expiredEnv)
	rawReply, ok := daemon.HandleCommandPayload(t.Context(), rawExpired)
	if !ok {
		t.Fatal("expected immediate reply for already-expired command")
	}
	var expiredReply CommandReplyWire
	if err := json.Unmarshal(rawReply, &expiredReply); err != nil {
		t.Fatal(err)
	}
	if expiredReply.EdgeStatus != "EXPIRED" || expiredReply.ReasonCode == nil || *expiredReply.ReasonCode != "COMMAND_EXPIRED" {
		t.Fatalf("expected EXPIRED COMMAND_EXPIRED, got %+v", expiredReply)
	}

	// 2. Command with TTL > 24h returns TTL_EXCEEDS_MAX immediately with 0 writes.
	tooLongEnv := expiredEnv
	tooLongEnv.MessageID = "msg-too-long"
	tooLongEnv.CommandID = "cmd-too-long"
	tooLongEnv.IssuedAt = nowFn().UnixMilli()
	tooLongEnv.ExpireAt = nowFn().Add(24*time.Hour + time.Minute).UnixMilli()
	tooLongEnv.ExecutionFence = 2
	tooLongEnv.PayloadHash = "hash-too-long"
	rawTooLong, _ := json.Marshal(tooLongEnv)
	rawLongReply, ok := daemon.HandleCommandPayload(t.Context(), rawTooLong)
	if !ok {
		t.Fatal("expected immediate reply for >24h TTL command")
	}
	var longReply CommandReplyWire
	if err := json.Unmarshal(rawLongReply, &longReply); err != nil {
		t.Fatal(err)
	}
	if longReply.EdgeStatus != "EXPIRED" || longReply.ReasonCode == nil || *longReply.ReasonCode != "TTL_EXCEEDS_MAX" {
		t.Fatalf("expected EXPIRED TTL_EXCEEDS_MAX, got %+v", longReply)
	}
	if modbusHandler.writes.Load() != 0 {
		t.Fatalf("expired/over-TTL commands must perform 0 writes, got %d", modbusHandler.writes.Load())
	}

	// 3. Pending command whose clock advances past expireAt before StepCycle expires cleanly with 0 writes.
	cycleExpireEnv := tooLongEnv
	cycleExpireEnv.MessageID = "msg-cycle-expire"
	cycleExpireEnv.CommandID = "cmd-cycle-expire"
	cycleExpireEnv.IssuedAt = nowFn().UnixMilli()
	cycleExpireEnv.ExpireAt = nowFn().Add(5 * time.Second).UnixMilli()
	cycleExpireEnv.ExecutionFence = 3
	cycleExpireEnv.PayloadHash = "hash-cycle-expire"
	cycleReply := dispatchViaDaemonCycle(t, daemon, &clockMu, &curNow, 10*time.Second, cycleExpireEnv)
	if cycleReply.EdgeStatus != "EXPIRED" || cycleReply.ReasonCode == nil || *cycleReply.ReasonCode != "COMMAND_EXPIRED" {
		t.Fatalf("expected pending expired reply COMMAND_EXPIRED, got %+v", cycleReply)
	}
	if modbusHandler.writes.Load() != 0 {
		t.Fatalf("expired cycle command must perform 0 writes, got %d", modbusHandler.writes.Load())
	}
}
