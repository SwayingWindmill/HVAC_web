package edgedaemon

import (
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modbus "github.com/simonvetter/modbus"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	plantCoilCHWValveOpen  uint16 = 0
	plantDiscCHWFlowSwitch uint16 = 1

	plantRegPumpRunState        uint16 = 10
	plantRegPumpFaultCode       uint16 = 11
	plantRegPumpStartCmd        uint16 = 12
	plantRegPumpStopCmd         uint16 = 13
	plantRegPumpFreqFB          uint16 = 14
	plantRegPumpFreqSP          uint16 = 15
	plantRegPumpFlowM3H         uint16 = 16
	plantRegPumpLocalRemoteMode uint16 = 17

	plantRegChillerRunState        uint16 = 30
	plantRegChillerFaultCode       uint16 = 31
	plantRegChillerStartCmd        uint16 = 32
	plantRegChillerStopCmd         uint16 = 33
	plantRegChillerSetpoint        uint16 = 34
	plantRegChillerLoadLimit       uint16 = 35
	plantRegChillerSupplyTemp      uint16 = 36
	plantRegChillerLocalRemoteMode uint16 = 37
)

type virtualModbusCentralPlant struct {
	mu       sync.Mutex
	endpoint string

	pumpRunning            bool
	pumpFault              uint16
	pumpFreqSetpointHz     float64
	pumpFreqHz             float64
	pumpLocalMode          uint16 // 0=LOCAL, 1=REMOTE
	pumpStartCmdBit        bool
	pumpStopCmdBit         bool
	pumpFreqReadOnlyLocked bool

	chillerRunning                bool
	chillerFault                  uint16
	chillerSetpointC              float64
	leavingChwTempC               float64
	chillerThermalTau             time.Duration
	chillerLoadLimitPct           float64
	chillerLocalMode              uint16 // 0=LOCAL, 1=REMOTE
	chillerStartCmdBit            bool
	chillerStopCmdBit             bool
	chillerSetpointReadOnlyLocked bool

	chwValveOpen         bool
	chwFlowSwitchBlocked bool

	writesTotal  atomic.Int64
	writesByAddr map[uint16]int64
}

func startVirtualModbusCentralPlant(t *testing.T) *virtualModbusCentralPlant {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	plant := &virtualModbusCentralPlant{
		endpoint:            addr,
		pumpRunning:         true,
		pumpFreqSetpointHz:  35.0,
		pumpFreqHz:          35.0,
		pumpLocalMode:       1,
		pumpStartCmdBit:     true,
		chillerRunning:      true,
		chillerSetpointC:    7.0,
		leavingChwTempC:     7.0,
		chillerThermalTau:   10 * time.Minute,
		chillerLoadLimitPct: 100.0,
		chillerLocalMode:    1,
		chillerStartCmdBit:  true,
		chwValveOpen:        true,
		writesByAddr:        make(map[uint16]int64),
	}

	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        "tcp://" + addr,
		Timeout:    2 * time.Second,
		MaxClients: 16,
	}, plant)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return plant
}

func (p *virtualModbusCentralPlant) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if req.IsWrite {
		for i, v := range req.Args {
			if req.Addr+uint16(i) == plantCoilCHWValveOpen {
				p.chwValveOpen = v
			}
		}
		return nil, nil
	}
	out := make([]bool, req.Quantity)
	for i := range req.Quantity {
		switch req.Addr + uint16(i) {
		case plantCoilCHWValveOpen:
			out[i] = p.chwValveOpen
		case plantDiscCHWFlowSwitch:
			out[i] = p.flowProvenLocked()
		}
	}
	return out, nil
}

func (p *virtualModbusCentralPlant) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]bool, req.Quantity)
	for i := range req.Quantity {
		switch req.Addr + uint16(i) {
		case plantCoilCHWValveOpen:
			out[i] = p.chwValveOpen
		case plantDiscCHWFlowSwitch:
			out[i] = p.flowProvenLocked()
		}
	}
	return out, nil
}

func (p *virtualModbusCentralPlant) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (p *virtualModbusCentralPlant) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if req.IsWrite {
		p.writesTotal.Add(1)
		for i, v := range req.Args {
			addr := req.Addr + uint16(i)
			p.writesByAddr[addr]++
			p.writeHoldingLocked(addr, v)
		}
		return nil, nil
	}
	out := make([]uint16, req.Quantity)
	for i := range req.Quantity {
		out[i] = p.readHoldingLocked(req.Addr + uint16(i))
	}
	return out, nil
}

func (p *virtualModbusCentralPlant) flowProvenLocked() bool {
	return p.pumpRunning && p.chwValveOpen && !p.chwFlowSwitchBlocked
}

func (p *virtualModbusCentralPlant) readHoldingLocked(addr uint16) uint16 {
	switch addr {
	case plantRegPumpRunState:
		if p.pumpRunning {
			return 1
		}
		return 0
	case plantRegPumpFaultCode:
		return p.pumpFault
	case plantRegPumpStartCmd:
		if p.pumpStartCmdBit {
			return 1
		}
		return 0
	case plantRegPumpStopCmd:
		if p.pumpStopCmdBit {
			return 1
		}
		return 0
	case plantRegPumpFreqFB:
		if !p.pumpRunning {
			return 0
		}
		return uint16(math.Round(p.pumpFreqHz * 10))
	case plantRegPumpFreqSP:
		return uint16(math.Round(p.pumpFreqSetpointHz * 10))
	case plantRegPumpFlowM3H:
		if !p.flowProvenLocked() {
			return 0
		}
		flow := 48.0 * (p.pumpFreqHz / 50.0)
		return uint16(math.Round(flow * 10))
	case plantRegPumpLocalRemoteMode:
		return p.pumpLocalMode
	case plantRegChillerRunState:
		if p.chillerRunning {
			return 1
		}
		return 0
	case plantRegChillerFaultCode:
		return p.chillerFault
	case plantRegChillerStartCmd:
		if p.chillerStartCmdBit {
			return 1
		}
		return 0
	case plantRegChillerStopCmd:
		if p.chillerStopCmdBit {
			return 1
		}
		return 0
	case plantRegChillerSetpoint:
		return uint16(math.Round(p.chillerSetpointC * 10))
	case plantRegChillerLoadLimit:
		return uint16(math.Round(p.chillerLoadLimitPct))
	case plantRegChillerSupplyTemp:
		return uint16(math.Round(p.leavingChwTempC * 100))
	case plantRegChillerLocalRemoteMode:
		return p.chillerLocalMode
	default:
		return 0
	}
}

func (p *virtualModbusCentralPlant) writeHoldingLocked(addr uint16, v uint16) {
	switch addr {
	case plantRegPumpStartCmd:
		bit := (v & 1) != 0
		p.pumpStartCmdBit = bit
		if bit {
			p.pumpRunning = true
			p.pumpStopCmdBit = false
		}
	case plantRegPumpStopCmd:
		bit := (v & 1) != 0
		p.pumpStopCmdBit = bit
		if bit {
			p.pumpRunning = false
			p.pumpStartCmdBit = false
		}
	case plantRegPumpFreqSP:
		if p.pumpFreqReadOnlyLocked {
			return
		}
		p.pumpFreqSetpointHz = float64(v) * 0.1
		p.pumpFreqHz = p.pumpFreqSetpointHz
	case plantRegChillerStartCmd:
		bit := (v & 1) != 0
		p.chillerStartCmdBit = bit
		if bit {
			p.chillerRunning = true
			p.chillerStopCmdBit = false
		}
	case plantRegChillerStopCmd:
		bit := (v & 1) != 0
		p.chillerStopCmdBit = bit
		if bit {
			p.chillerRunning = false
			p.chillerStartCmdBit = false
		}
	case plantRegChillerSetpoint:
		if p.chillerSetpointReadOnlyLocked {
			return
		}
		p.chillerSetpointC = float64(v) * 0.1
	case plantRegChillerLoadLimit:
		p.chillerLoadLimitPct = float64(v)
	}
}

func (p *virtualModbusCentralPlant) Step(dt time.Duration) {
	if dt <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.chillerRunning {
		return
	}
	if p.chillerThermalTau <= 0 {
		p.leavingChwTempC = p.chillerSetpointC
		return
	}
	alpha := 1.0 - math.Exp(-dt.Seconds()/p.chillerThermalTau.Seconds())
	p.leavingChwTempC += (p.chillerSetpointC - p.leavingChwTempC) * alpha
}

func (p *virtualModbusCentralPlant) SetThermalTau(tau time.Duration) error {
	if tau < 5*time.Minute || tau > 15*time.Minute {
		return fmt.Errorf("thermal lag tau %s must be in [5m, 15m]", tau)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chillerThermalTau = tau
	return nil
}

func (p *virtualModbusCentralPlant) SetCabinetMode(chillerRemote, pumpRemote bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if chillerRemote {
		p.chillerLocalMode = 1
	} else {
		p.chillerLocalMode = 0
	}
	if pumpRemote {
		p.pumpLocalMode = 1
	} else {
		p.pumpLocalMode = 0
	}
}

func (p *virtualModbusCentralPlant) SetReadOnlyLocks(chillerLocked, pumpLocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chillerSetpointReadOnlyLocked = chillerLocked
	p.pumpFreqReadOnlyLocked = pumpLocked
}

func (p *virtualModbusCentralPlant) SetHydraulicInterlock(valveOpen, flowSwitchBlocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chwValveOpen = valveOpen
	p.chwFlowSwitchBlocked = flowSwitchBlocked
}

func (p *virtualModbusCentralPlant) SetChillerRunning(running bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chillerRunning = running
	p.chillerStartCmdBit = running
	p.chillerStopCmdBit = !running
}

func (p *virtualModbusCentralPlant) WritesFor(addr uint16) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writesByAddr[addr]
}

func (p *virtualModbusCentralPlant) Snapshot() (chillerRunning bool, setpointC, leavingC, pumpFreqHz float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.chillerRunning, p.chillerSetpointC, p.leavingChwTempC, p.pumpFreqSetpointHz
}

func newPlantDrillDaemon(
	t *testing.T,
	plant *virtualModbusCentralPlant,
	policy edgecontrol.AutonomyPolicyConfig,
	startTime time.Time,
) (*Daemon, *sync.Mutex, *time.Time) {
	t.Helper()
	uid := 1
	timeoutMS := 1000
	retries := 0
	writeFC6 := 6
	bit0 := 0
	endpoint := plant.endpoint

	var clockMu sync.Mutex
	curNow := startTime.UTC()
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}

	cfg := Config{
		GatewayID:      "0193f000-2000-7000-8000-000000000956",
		StateDir:       t.TempDir(),
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
					{PointCode: "run_state", SemanticChannel: string(edgecontrol.SemanticRunState), FunctionCode: 3, Address: int(plantRegPumpRunState), RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "STOPPED", "1": "RUNNING"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "fault_code", SemanticChannel: string(edgecontrol.SemanticFaultCode), FunctionCode: 3, Address: int(plantRegPumpFaultCode), RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "NONE", "1": "FAULT"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "HIGH"},
					{PointCode: "start_cmd", SemanticChannel: string(edgecontrol.SemanticStartCommand), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegPumpStartCmd), RegisterCount: 1, RawType: "BIT_BOOL", BitIndex: &bit0, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "stop_cmd", SemanticChannel: string(edgecontrol.SemanticStopCommand), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegPumpStopCmd), RegisterCount: 1, RawType: "BIT_BOOL", BitIndex: &bit0, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "freq_fb", SemanticChannel: string(edgecontrol.SemanticFrequency), FunctionCode: 3, Address: int(plantRegPumpFreqFB), RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
					{PointCode: "freq_sp", SemanticChannel: string(edgecontrol.SemanticFrequencySetpoint), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegPumpFreqSP), RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
					{PointCode: "flow_m3h", SemanticChannel: string(edgecontrol.SemanticFlow), FunctionCode: 3, Address: int(plantRegPumpFlowM3H), RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "VERY_HIGH"},
					// Intentionally omit SemanticChannel on pump local_remote_mode to exercise shadow_client fallback lookup.
					{PointCode: "local_remote_mode", FunctionCode: 3, Address: int(plantRegPumpLocalRemoteMode), RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "LOCAL", "1": "REMOTE"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
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
					{PointCode: "run_state", SemanticChannel: string(edgecontrol.SemanticRunState), FunctionCode: 3, Address: int(plantRegChillerRunState), RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "STOPPED", "1": "RUNNING"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "fault_code", SemanticChannel: string(edgecontrol.SemanticFaultCode), FunctionCode: 3, Address: int(plantRegChillerFaultCode), RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "NONE", "1": "FAULT"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "HIGH"},
					{PointCode: "start_cmd", SemanticChannel: string(edgecontrol.SemanticStartCommand), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegChillerStartCmd), RegisterCount: 1, RawType: "BIT_BOOL", BitIndex: &bit0, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "stop_cmd", SemanticChannel: string(edgecontrol.SemanticStopCommand), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegChillerStopCmd), RegisterCount: 1, RawType: "BIT_BOOL", BitIndex: &bit0, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "chw_sp", SemanticChannel: string(edgecontrol.SemanticTemperatureSetpoint), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegChillerSetpoint), RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.1, PollPriority: "HIGH"},
					{PointCode: "load_limit", SemanticChannel: string(edgecontrol.SemanticLoadLimit), FunctionCode: 3, WriteFunctionCode: &writeFC6, Address: int(plantRegChillerLoadLimit), RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "HIGH"},
					{PointCode: "chw_supply_temp", FunctionCode: 3, Address: int(plantRegChillerSupplyTemp), RegisterCount: 1, RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 0.01, PollPriority: "HIGH"},
					{PointCode: "local_remote_mode", SemanticChannel: string(edgecontrol.SemanticLocalRemoteMode), FunctionCode: 3, Address: int(plantRegChillerLocalRemoteMode), RegisterCount: 1, RawType: "ENUM16", EnumMap: map[string]string{"0": "LOCAL", "1": "REMOTE"}, ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "chw_valve_open", FunctionCode: 1, Address: int(plantCoilCHWValveOpen), RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
					{PointCode: "chw_flow_switch", FunctionCode: 2, Address: int(plantDiscCHWFlowSwitch), RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, PollPriority: "VERY_HIGH"},
				},
			},
		},
	}
	cfg.ApplyDefaults("1.0.0")

	daemon, err := NewDaemon(cfg, DaemonOptions{Now: nowFn})
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	t.Cleanup(func() { _ = daemon.Close() })
	return daemon, &clockMu, &curNow
}

func stepPlantCycle(
	t *testing.T,
	daemon *Daemon,
	plant *virtualModbusCentralPlant,
	clockMu *sync.Mutex,
	curNow *time.Time,
	dt time.Duration,
) edgecontrol.HostCycleResult {
	t.Helper()
	if dt <= 0 {
		dt = time.Second
	}
	plant.Step(dt)
	clockMu.Lock()
	*curNow = curNow.Add(dt)
	clockMu.Unlock()
	res, err := daemon.StepCycle(t.Context())
	if err != nil {
		t.Fatalf("StepCycle(%s) error = %v", dt, err)
	}
	for _, pr := range res.PollResults {
		if pr.Error != nil {
			t.Fatalf("Modbus poll error on %s: %v", pr.AdapterID, pr.Error)
		}
	}
	return res
}

func makeDrillCommand(
	id string,
	now time.Time,
	ttl time.Duration,
	deviceID, commandCode string,
	params map[string]float64,
	fence uint64,
) CommandEnvelopeWire {
	if params == nil {
		params = map[string]float64{}
	}
	return CommandEnvelopeWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      "msg-" + id,
		CommandID:      id,
		TraceID:        "trace-" + id,
		IssuedAt:       now.UnixMilli(),
		ExpireAt:       now.Add(ttl).UnixMilli(),
		DeviceID:       deviceID,
		CommandCode:    commandCode,
		Params:         params,
		Policy:         CommandEnvelopePolicy{RequiresReadback: true, VerificationWindowMS: 5000},
		ExecutionFence: fence,
		PayloadHash:    "hash-" + id,
	}
}

func mustImageValue(t *testing.T, img edgecontrol.ProcessImage, addr string) edgecontrol.Value {
	t.Helper()
	snap, ok := img.Get(addr)
	if !ok || !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
		t.Fatalf("expected good sample for %s, got %+v ok=%v", addr, snap, ok)
	}
	return snap.Sample.Value
}

func TestProductionEdgeGatewayThermalInertiaDrill(t *testing.T) {
	plant := startVirtualModbusCentralPlant(t)
	if err := plant.SetThermalTau(2 * time.Minute); err == nil {
		t.Fatal("expected out-of-range thermal lag tau (<5m) rejected")
	}
	if err := plant.SetThermalTau(10 * time.Minute); err != nil {
		t.Fatalf("SetThermalTau(10m) error = %v", err)
	}
	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	policy.Enabled = true
	policy.OfflineGraceSeconds = 30
	policy.DefaultChilledWaterSupplySetpointC = 7.0
	policy.DefaultPumpFrequencyHz = 35.0

	t0 := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	daemon, clockMu, curNow := newPlantDrillDaemon(t, plant, policy, t0)
	daemon.mqttConnected.Store(true)

	initial := stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	if temp0 := mustImageValue(t, initial.Cycle.Image, "chiller-01/chw_supply_temp").Double; temp0 != 7.0 {
		t.Fatalf("expected initial chw_supply_temp=7.0 C, got %.3f", temp0)
	}

	clockMu.Lock()
	cmdNow := *curNow
	clockMu.Unlock()
	// Lease for 35m so it holds across 5m/10m/30m thermal lag checks, then expires cleanly.
	reply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-thermal-sp-9",
		cmdNow,
		35*time.Minute,
		"chiller-01",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
		map[string]float64{"setpointC": 9.0},
		1,
	))
	if reply.EdgeStatus != "EXECUTED" || reply.ReasonCode != nil || reply.ExecutionEvidence == nil || !reply.ExecutionEvidence.ValidExecuted() {
		t.Fatalf("expected EXECUTED thermal setpoint command, got %+v", reply)
	}

	// 5m: tau=10m => 7.0 + 2.0*(1 - exp(-0.5)) ≈ 7.79 C (noticeably shy of 9.0 C).
	at5m := stepPlantCycle(t, daemon, plant, clockMu, curNow, 5*time.Minute)
	temp5m := mustImageValue(t, at5m.Cycle.Image, "chiller-01/chw_supply_temp").Double
	sp5m := mustImageValue(t, at5m.Cycle.Image, "chiller-01/chw_sp").Double
	if sp5m != 9.0 {
		t.Fatalf("expected leased setpoint 9.0 C held at 5m, got %.2f", sp5m)
	}
	if temp5m <= 7.2 || temp5m >= 8.3 {
		t.Fatalf("5m thermal response out of 10m lag band: temp5m=%.3f", temp5m)
	}

	// 10m: tau=10m => ~8.26 C (monotonic exponential approach).
	at10m := stepPlantCycle(t, daemon, plant, clockMu, curNow, 5*time.Minute)
	temp10m := mustImageValue(t, at10m.Cycle.Image, "chiller-01/chw_supply_temp").Double
	if temp10m <= temp5m || temp10m >= 8.9 {
		t.Fatalf("10m thermal response did not advance monotonically: temp5m=%.3f temp10m=%.3f", temp5m, temp10m)
	}

	// 30m: tau=10m => ~8.90 C (converged near 9.0 C while lease still active).
	at30m := stepPlantCycle(t, daemon, plant, clockMu, curNow, 20*time.Minute)
	temp30m := mustImageValue(t, at30m.Cycle.Image, "chiller-01/chw_supply_temp").Double
	if temp30m <= temp10m || temp30m < 8.75 {
		t.Fatalf("30m thermal response did not settle near 9.0 C: temp10m=%.3f temp30m=%.3f", temp10m, temp30m)
	}
	if active := daemon.Host().IntentStore().Active(at30m.Cycle.Image.At()); len(active) != 1 || active[0].ID != "cmd-thermal-sp-9" {
		t.Fatalf("expected leased intent active at 30m, got %+v", active)
	}

	// Lease expires (+6m -> 36m total) and cloud disconnects -> after OfflineGraceSeconds (30s), fallback takes over.
	daemon.mqttConnected.Store(false)
	offlineEnter := stepPlantCycle(t, daemon, plant, clockMu, curNow, 6*time.Minute)
	if active := daemon.Host().IntentStore().Active(offlineEnter.Cycle.Image.At()); len(active) != 0 {
		t.Fatalf("expected lease expired after 36m, got %+v", active)
	}
	if st := daemon.fallback.StatusAt(offlineEnter.Cycle.Image.At()); st.FallbackActive {
		t.Fatalf("fallback must wait for OfflineGraceSeconds before takeover: %+v", st)
	}

	graceExpired := stepPlantCycle(t, daemon, plant, clockMu, curNow, 35*time.Second)
	if st := daemon.fallback.StatusAt(graceExpired.Cycle.Image.At()); !st.FallbackActive {
		t.Fatalf("expected AutonomousFallbackController active after offline grace: %+v", st)
	}
	var fallbackWritten bool
	for _, wr := range graceExpired.WriteResults {
		if wr.Address == "chiller-01/chw_sp" && wr.Success && wr.ReadbackVerified && wr.ReadbackValue != nil && wr.ReadbackValue.Double == 7.0 {
			fallbackWritten = true
		}
	}
	if !fallbackWritten {
		t.Fatalf("expected fallback to write 7.0 C to chiller-01/chw_sp over Modbus/TCP, writeResults=%+v", graceExpired.WriteResults)
	}
	_, gotSP, _, _ := plant.Snapshot()
	if gotSP != 7.0 {
		t.Fatalf("expected Modbus chiller setpoint reverted to fallback 7.0 C, got %.2f", gotSP)
	}

	cooled := stepPlantCycle(t, daemon, plant, clockMu, curNow, 30*time.Minute)
	if cooledTemp := mustImageValue(t, cooled.Cycle.Image, "chiller-01/chw_supply_temp").Double; cooledTemp > 7.2 {
		t.Fatalf("expected leaving water temp cooling toward 7.0 C under fallback, got %.3f", cooledTemp)
	}
}

func TestProductionEdgeGatewayLocalManualDrill(t *testing.T) {
	plant := startVirtualModbusCentralPlant(t)
	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	t0 := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	daemon, clockMu, curNow := newPlantDrillDaemon(t, plant, policy, t0)
	daemon.mqttConnected.Store(true)

	stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)

	// Flip both chiller (SemanticLocalRemoteMode) and pump (fallback local_remote_mode) to LOCAL on Modbus slave.
	plant.SetCabinetMode(false, false)
	polledLocal := stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	if mode := mustImageValue(t, polledLocal.Cycle.Image, "chiller-01/local_remote_mode").String; mode != "LOCAL" {
		t.Fatalf("expected polled chiller local_remote_mode=LOCAL, got %q", mode)
	}
	if mode := mustImageValue(t, polledLocal.Cycle.Image, "pump-01/local_remote_mode").String; mode != "LOCAL" {
		t.Fatalf("expected polled pump local_remote_mode=LOCAL, got %q", mode)
	}

	chillerWritesBefore := plant.WritesFor(plantRegChillerSetpoint)
	pumpWritesBefore := plant.WritesFor(plantRegPumpFreqSP)

	clockMu.Lock()
	cmdNow := *curNow
	clockMu.Unlock()
	chillerReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-local-chiller-sp",
		cmdNow,
		time.Minute,
		"chiller-01",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
		map[string]float64{"setpointC": 9.5},
		1,
	))
	if chillerReply.EdgeStatus != "REJECTED" || chillerReply.ReasonCode == nil || *chillerReply.ReasonCode != "CABINET_LOCAL_MANUAL" {
		t.Fatalf("expected REJECTED CABINET_LOCAL_MANUAL for chiller, got %+v", chillerReply)
	}
	if plant.WritesFor(plantRegChillerSetpoint) != chillerWritesBefore {
		t.Fatalf("rejected LOCAL chiller command must perform 0 Modbus writes")
	}

	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	pumpReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-local-pump-freq",
		cmdNow,
		time.Minute,
		"pump-01",
		"SET_FREQUENCY",
		map[string]float64{"frequencyHz": 44.0},
		1,
	))
	if pumpReply.EdgeStatus != "REJECTED" || pumpReply.ReasonCode == nil || *pumpReply.ReasonCode != "CABINET_LOCAL_MANUAL" {
		t.Fatalf("expected REJECTED CABINET_LOCAL_MANUAL for pump via fallback channel lookup, got %+v", pumpReply)
	}
	if plant.WritesFor(plantRegPumpFreqSP) != pumpWritesBefore {
		t.Fatalf("rejected LOCAL pump command must perform 0 Modbus writes")
	}

	// Flip both back to REMOTE -> next cycle immediately allows remote execution.
	plant.SetCabinetMode(true, true)
	stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)

	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	remoteChillerReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-remote-chiller-sp",
		cmdNow,
		time.Minute,
		"chiller-01",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
		map[string]float64{"setpointC": 8.5},
		2,
	))
	if remoteChillerReply.EdgeStatus != "EXECUTED" || remoteChillerReply.ReasonCode != nil {
		t.Fatalf("expected EXECUTED after restoring chiller REMOTE, got %+v", remoteChillerReply)
	}

	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	remotePumpReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-remote-pump-freq",
		cmdNow,
		time.Minute,
		"pump-01",
		"SET_FREQUENCY",
		map[string]float64{"frequencyHz": 44.0},
		2,
	))
	if remotePumpReply.EdgeStatus != "EXECUTED" || remotePumpReply.ReasonCode != nil {
		t.Fatalf("expected EXECUTED after restoring pump REMOTE, got %+v", remotePumpReply)
	}

	_, gotSP, _, gotHz := plant.Snapshot()
	if gotSP != 8.5 || gotHz != 44.0 {
		t.Fatalf("expected applied chiller setpoint 8.5 C and pump 44.0 Hz, got sp=%.2f hz=%.2f", gotSP, gotHz)
	}
}

func TestProductionEdgeGatewayReadOnlyLockDrill(t *testing.T) {
	plant := startVirtualModbusCentralPlant(t)
	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	t0 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	daemon, clockMu, curNow := newPlantDrillDaemon(t, plant, policy, t0)
	daemon.mqttConnected.Store(true)

	stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	plant.SetReadOnlyLocks(true, true)

	// 1. Pump VFD frequency read-only lock: Modbus FC06 ACKs, readback stays pinned at 35.0 Hz -> FAILED LOCAL_READBACK_MISMATCH.
	pumpWritesBefore := plant.WritesFor(plantRegPumpFreqSP)
	clockMu.Lock()
	cmdNow := *curNow
	clockMu.Unlock()
	pumpReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-ro-lock-pump",
		cmdNow,
		time.Minute,
		"pump-01",
		"SET_FREQUENCY",
		map[string]float64{"frequencyHz": 42.0},
		1,
	))
	if plant.WritesFor(plantRegPumpFreqSP) != pumpWritesBefore+1 {
		t.Fatalf("expected Modbus FC06 write attempt on locked VFD register")
	}
	if pumpReply.EdgeStatus != "FAILED" || pumpReply.ReasonCode == nil || *pumpReply.ReasonCode != "LOCAL_READBACK_MISMATCH" {
		t.Fatalf("expected FAILED LOCAL_READBACK_MISMATCH for locked pump VFD, got %+v", pumpReply)
	}
	if pumpReply.ExecutionEvidence == nil || pumpReply.ExecutionEvidence.LocalReadbackVerified == nil ||
		*pumpReply.ExecutionEvidence.LocalReadbackVerified || pumpReply.ExecutionEvidence.Readback == nil ||
		pumpReply.ExecutionEvidence.Readback.Number == nil || *pumpReply.ExecutionEvidence.Readback.Number != 35.0 {
		t.Fatalf("expected readback evidence pinned at 35.0 Hz, got %+v", pumpReply.ExecutionEvidence)
	}

	// 2. Chiller setpoint read-only lock: Modbus FC06 ACKs, readback stays pinned at 7.0 C -> FAILED LOCAL_READBACK_MISMATCH.
	chillerWritesBefore := plant.WritesFor(plantRegChillerSetpoint)
	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	chillerReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-ro-lock-chiller",
		cmdNow,
		time.Minute,
		"chiller-01",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
		map[string]float64{"setpointC": 10.0},
		1,
	))
	if plant.WritesFor(plantRegChillerSetpoint) != chillerWritesBefore+1 {
		t.Fatalf("expected Modbus FC06 write attempt on locked chiller setpoint register")
	}
	if chillerReply.EdgeStatus != "FAILED" || chillerReply.ReasonCode == nil || *chillerReply.ReasonCode != "LOCAL_READBACK_MISMATCH" {
		t.Fatalf("expected FAILED LOCAL_READBACK_MISMATCH for locked chiller setpoint, got %+v", chillerReply)
	}
	if chillerReply.ExecutionEvidence == nil || chillerReply.ExecutionEvidence.LocalReadbackVerified == nil ||
		*chillerReply.ExecutionEvidence.LocalReadbackVerified || chillerReply.ExecutionEvidence.Readback == nil ||
		chillerReply.ExecutionEvidence.Readback.Number == nil || *chillerReply.ExecutionEvidence.Readback.Number != 7.0 {
		t.Fatalf("expected readback evidence pinned at 7.0 C, got %+v", chillerReply.ExecutionEvidence)
	}

	// Release read-only locks -> subsequent commands succeed with verified readback.
	plant.SetReadOnlyLocks(false, false)
	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	recoveredReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-ro-unlock-chiller",
		cmdNow,
		time.Minute,
		"chiller-01",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
		map[string]float64{"setpointC": 10.0},
		2,
	))
	if recoveredReply.EdgeStatus != "EXECUTED" || recoveredReply.ReasonCode != nil ||
		recoveredReply.ExecutionEvidence == nil || !recoveredReply.ExecutionEvidence.ValidExecuted() {
		t.Fatalf("expected EXECUTED after releasing read-only lock, got %+v", recoveredReply)
	}
}

func TestProductionEdgeGatewayHydraulicInterlockDrill(t *testing.T) {
	plant := startVirtualModbusCentralPlant(t)
	plant.SetChillerRunning(false)
	plant.SetHydraulicInterlock(false, false) // butterfly valve closed initially

	policy := edgecontrol.DefaultAutonomyPolicyConfig()
	policy.MinRunSeconds = 60

	t0 := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)
	daemon, clockMu, curNow := newPlantDrillDaemon(t, plant, policy, t0)
	daemon.mqttConnected.Store(true)

	// 1. Closed butterfly valve -> no CHW flow proof -> START rejected with HYDRAULIC_INTERLOCK_NO_CHW_FLOW.
	closedPoll := stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	if mustImageValue(t, closedPoll.Cycle.Image, "chiller-01/chw_valve_open").Boolean || mustImageValue(t, closedPoll.Cycle.Image, "chiller-01/chw_flow_switch").Boolean {
		t.Fatalf("expected closed valve and false flow switch in ProcessImage")
	}
	startWritesBefore := plant.WritesFor(plantRegChillerStartCmd)
	clockMu.Lock()
	cmdNow := *curNow
	clockMu.Unlock()
	valveDeny := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-start-valve-closed",
		cmdNow,
		time.Minute,
		"chiller-01",
		"START",
		nil,
		1,
	))
	if valveDeny.EdgeStatus != "REJECTED" || valveDeny.ReasonCode == nil || *valveDeny.ReasonCode != "HYDRAULIC_INTERLOCK_NO_CHW_FLOW" {
		t.Fatalf("expected REJECTED HYDRAULIC_INTERLOCK_NO_CHW_FLOW when valve closed, got %+v", valveDeny)
	}
	if plant.WritesFor(plantRegChillerStartCmd) != startWritesBefore {
		t.Fatalf("denied START must perform 0 Modbus writes")
	}

	// 2. Butterfly valve open, but flow switch blocked/lost -> START still rejected with HYDRAULIC_INTERLOCK_NO_CHW_FLOW.
	plant.SetHydraulicInterlock(true, true)
	flowLostPoll := stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	if !mustImageValue(t, flowLostPoll.Cycle.Image, "chiller-01/chw_valve_open").Boolean || mustImageValue(t, flowLostPoll.Cycle.Image, "chiller-01/chw_flow_switch").Boolean {
		t.Fatalf("expected open valve and unproven flow switch in ProcessImage")
	}
	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	flowDeny := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-start-flow-lost",
		cmdNow,
		time.Minute,
		"chiller-01",
		"START",
		nil,
		2,
	))
	if flowDeny.EdgeStatus != "REJECTED" || flowDeny.ReasonCode == nil || *flowDeny.ReasonCode != "HYDRAULIC_INTERLOCK_NO_CHW_FLOW" {
		t.Fatalf("expected REJECTED HYDRAULIC_INTERLOCK_NO_CHW_FLOW when flow switch lost, got %+v", flowDeny)
	}

	// 3. Restore valve open & flow switch proof -> START executes cleanly.
	plant.SetHydraulicInterlock(true, false)
	provenPoll := stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	if !mustImageValue(t, provenPoll.Cycle.Image, "chiller-01/chw_flow_switch").Boolean || !daemon.Interlock().Status().ChilledWaterFlowProven {
		t.Fatalf("expected proven CHW flow before chiller start")
	}
	clockMu.Lock()
	cmdNow = *curNow
	clockMu.Unlock()
	startReply := dispatchViaDaemonCycle(t, daemon, clockMu, curNow, time.Second, makeDrillCommand(
		"cmd-start-proven",
		cmdNow,
		time.Minute,
		"chiller-01",
		"START",
		nil,
		3,
	))
	if startReply.EdgeStatus != "EXECUTED" || startReply.ReasonCode != nil {
		t.Fatalf("expected EXECUTED chiller START with proven flow, got %+v", startReply)
	}
	runningPoll := stepPlantCycle(t, daemon, plant, clockMu, curNow, 5*time.Second)
	if mustImageValue(t, runningPoll.Cycle.Image, "chiller-01/run_state").String != "RUNNING" {
		t.Fatalf("expected chiller RUNNING after start, got %+v", mustImageValue(t, runningPoll.Cycle.Image, "chiller-01/run_state"))
	}

	// 4. Lose flow while chiller is running (inside 60s MinRunSeconds) -> immediate emergency stop decision.
	plant.SetHydraulicInterlock(false, false)
	emergencyCycle := stepPlantCycle(t, daemon, plant, clockMu, curNow, 5*time.Second)
	var stopDecision bool
	for _, d := range emergencyCycle.Cycle.Decisions {
		if d.Address == "chiller-01/stop_cmd" && d.Accepted && d.Effective != nil && d.Effective.Boolean &&
			d.ControllerID == edgecontrol.DefaultHydraulicInterlockID {
			stopDecision = true
		}
	}
	if !stopDecision {
		t.Fatalf("expected immediate hydraulic interlock stop decision for chiller-01/stop_cmd, decisions=%+v", emergencyCycle.Cycle.Decisions)
	}
	var stopWritten bool
	for _, wr := range emergencyCycle.WriteResults {
		if wr.Address == "chiller-01/stop_cmd" && wr.Success && wr.ReadbackVerified {
			stopWritten = true
		}
	}
	if !stopWritten {
		t.Fatalf("expected emergency stop write verified over Modbus/TCP, writeResults=%+v", emergencyCycle.WriteResults)
	}
	if reason := daemon.Interlock().Status().LastBlockedReasons["chiller-01:emergency_stop"]; reason != "HYDRAULIC_INTERLOCK_NO_CHW_FLOW" {
		t.Fatalf("expected emergency_stop reason HYDRAULIC_INTERLOCK_NO_CHW_FLOW, got %q", reason)
	}
	chillerRunning, _, _, _ := plant.Snapshot()
	if chillerRunning {
		t.Fatalf("expected chiller stopped in Modbus plant after emergency stop write")
	}
	afterStop := stepPlantCycle(t, daemon, plant, clockMu, curNow, time.Second)
	if mustImageValue(t, afterStop.Cycle.Image, "chiller-01/run_state").String != "STOPPED" {
		t.Fatalf("expected STOPPED chiller after emergency trip, got %v", mustImageValue(t, afterStop.Cycle.Image, "chiller-01/run_state"))
	}
}
