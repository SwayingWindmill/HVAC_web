package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	fieldPlantStateFileName    = "field-plant-state.json"
	fieldPlantMaxChannelSpan   = 32
	fieldPlantMaxChannelGap    = 4
	fieldPlantMaxCoilSpan      = 64
	fieldPlantMaxCoilGap       = 4
	fieldPlantMaxMeterSpanRegs = 64
)

// FieldPlantBus abstracts raw Modbus RTU read operations needed for declarative plant polling.
type FieldPlantBus interface {
	ReadCoils(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
	ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
	ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
}

// ModbusFieldPlantBus adapts *edgecontrol.ModbusTCPBridge to FieldPlantBus.
type ModbusFieldPlantBus struct {
	Bridge *edgecontrol.ModbusTCPBridge
}

// ReadCoils performs FC01 Read Coils via the shared Modbus bridge.
func (b ModbusFieldPlantBus) ReadCoils(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("field plant modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name:         fmt.Sprintf("plant-fc01-%d-%d", unitID, address),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadCoils,
		Address:      address,
		Quantity:     quantity,
	})
}

// ReadHoldingRegisters performs FC03 Read Holding Registers via the shared Modbus bridge.
func (b ModbusFieldPlantBus) ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("field plant modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name:         fmt.Sprintf("plant-fc03-%d-%d", unitID, address),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadHoldingRegisters,
		Address:      address,
		Quantity:     quantity,
	})
}

// ReadInputRegisters performs FC04 Read Input Registers via the shared Modbus bridge.
func (b ModbusFieldPlantBus) ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("field plant modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name:         fmt.Sprintf("plant-fc04-%d-%d", unitID, address),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadInputRegisters,
		Address:      address,
		Quantity:     quantity,
	})
}

type fieldMeterSample struct {
	ts        time.Time
	powerKW   float64
	energyKWh float64
}

type fieldPlantPersistedState struct {
	LastSequence     uint64  `json:"lastSequence"`
	CoolingEnergyKWh float64 `json:"coolingEnergyKWh"`
}

type channelRange struct {
	start int
	count int
}

type coilRange struct {
	start uint16
	count uint16
}

// FieldPlantOptions configures a declarative RS-485 plant poller and envelope builder.
type FieldPlantOptions struct {
	GatewayID        string
	StateDir         string
	Config           FieldPlantConfig
	Bus              FieldPlantBus
	PublishTelemetry func(ctx context.Context, payload []byte)
	Now              func() time.Time
	UUIDv7           func(now time.Time) string
	Logger           *slog.Logger
}

// FieldPlantRunner polls declarative RS-485 field instruments and publishes schema 2.0 telemetry.
type FieldPlantRunner struct {
	gatewayID        string
	statePath        string
	cfg              FieldPlantConfig
	bus              FieldPlantBus
	publishTelemetry func(ctx context.Context, payload []byte)
	now              func() time.Time
	uuidv7           func(now time.Time) string
	logger           *slog.Logger

	activeChannels []int
	baselineRoutes []PointCopyRouteConfig
	meterByKey     map[string]FieldMeterSpec

	mu               sync.Mutex
	sequence         uint64
	activeCopyRoutes []PointCopyRouteConfig
	doCoils          map[string]int
	meterRunState    map[string]bool
	meterLatest      map[string]fieldMeterSample
	hasFlow          bool
	latestFlowTS     time.Time
	latestFlowM3H    float64
	hasPrevCooling   bool
	prevCoolingTS    time.Time
	prevCoolingKW    float64
	coolingEnergy    float64
}

// NewFieldPlantRunner builds a FieldPlantRunner from a FieldPlantConfig and loads durable cooling state.
func NewFieldPlantRunner(opts FieldPlantOptions) (*FieldPlantRunner, error) {
	gwID := strings.TrimSpace(opts.GatewayID)
	if gwID == "" {
		return nil, errors.New("field plant requires gatewayId")
	}
	if opts.Bus == nil {
		return nil, errors.New("field plant requires modbus bus")
	}
	cfg := cloneFieldPlantConfig(opts.Config)
	cfg.ApplyDefaults()
	if !cfg.Enabled {
		return nil, errors.New("field plant runner requires enabled config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	uuidFn := opts.UUIDv7
	if uuidFn == nil {
		uuidFn = func(t time.Time) string {
			id, err := edgecontrol.NewReplicationUUIDv7(t)
			if err != nil {
				return edgecontrol.DeterministicReplicationUUIDv7(t, gwID, "plant")
			}
			return id
		}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var statePath string
	if trimmed := strings.TrimSpace(opts.StateDir); trimmed != "" {
		statePath = filepath.Join(trimmed, fieldPlantStateFileName)
	}

	var sortedChannels []int
	var baselineRoutes []PointCopyRouteConfig
	if cfg.Concentrator != nil {
		sortedChannels = append([]int(nil), cfg.Concentrator.ActiveChannels...)
		sort.Ints(sortedChannels)
		baselineRoutes = clonePointCopyRoutes(cfg.Concentrator.CopyRoutes)
	}

	meterByKey := make(map[string]FieldMeterSpec)
	if cfg.Meters != nil {
		meterByKey = make(map[string]FieldMeterSpec, len(cfg.Meters.Devices))
		for _, spec := range cfg.Meters.Devices {
			meterByKey[spec.DeviceKey] = spec
		}
	}

	runner := &FieldPlantRunner{
		gatewayID:        gwID,
		statePath:        statePath,
		cfg:              cfg,
		bus:              opts.Bus,
		publishTelemetry: opts.PublishTelemetry,
		now:              nowFn,
		uuidv7:           uuidFn,
		logger:           logger,
		activeChannels:   sortedChannels,
		baselineRoutes:   baselineRoutes,
		meterByKey:       meterByKey,
		activeCopyRoutes: clonePointCopyRoutes(baselineRoutes),
		doCoils:          map[string]int{},
		meterRunState:    map[string]bool{},
		meterLatest:      map[string]fieldMeterSample{},
	}
	runner.loadState()
	return runner, nil
}

// ReadDevicePowerKW performs a live Modbus FC03 power read (kW) for STOP verification.
func (r *FieldPlantRunner) ReadDevicePowerKW(ctx context.Context, meterDeviceKey string) (*float64, error) {
	if r == nil {
		return nil, errors.New("field plant runner is nil")
	}
	key := strings.TrimSpace(meterDeviceKey)
	spec, ok := r.meterByKey[key]
	if !ok {
		return nil, fmt.Errorf("unknown meter deviceKey %q", meterDeviceKey)
	}
	regs, err := r.bus.ReadHoldingRegisters(ctx, spec.UnitID, spec.PowerRegisterAddr, 2)
	if err != nil {
		return nil, err
	}
	if len(regs) < 2 {
		return nil, fmt.Errorf("short meter power response from uid %d", spec.UnitID)
	}
	raw := (uint32(regs[0]) << 16) | uint32(regs[1])
	kw := float64(raw) * spec.PowerScale
	return &kw, nil
}

// UpdatePointMappings hot-reloads temperature copy routes from the platform config shadow.
// An empty filtered route list reverts cleanly to the baseline configured CopyRoutes.
func (r *FieldPlantRunner) UpdatePointMappings(mappings []GatewayPointMapping) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.Concentrator == nil {
		r.activeCopyRoutes = nil
		return
	}
	tempKey := r.cfg.Concentrator.TempDeviceKey
	var nextRoutes []PointCopyRouteConfig
	for _, m := range mappings {
		if strings.TrimSpace(m.PrimaryDeviceKey) != tempKey {
			continue
		}
		srcPoint := strings.TrimSpace(m.PrimaryPointCode)
		if srcPoint == "" || len(m.CopyTargets) == 0 {
			continue
		}
		targets := make([]PointCopyTarget, 0, len(m.CopyTargets))
		for _, t := range m.CopyTargets {
			dev := strings.TrimSpace(t.DeviceKey)
			pt := strings.TrimSpace(t.PointCode)
			if dev != "" && pt != "" {
				targets = append(targets, PointCopyTarget{DeviceKey: dev, PointCode: pt})
			}
		}
		if len(targets) > 0 {
			nextRoutes = append(nextRoutes, PointCopyRouteConfig{
				SourceKey: srcPoint,
				Targets:   targets,
			})
		}
	}
	if len(nextRoutes) > 0 {
		r.activeCopyRoutes = nextRoutes
		return
	}
	r.activeCopyRoutes = clonePointCopyRoutes(r.baselineRoutes)
}

// ActiveCopyRoutes returns a snapshot of the active point copy routes.
func (r *FieldPlantRunner) ActiveCopyRoutes() []PointCopyRouteConfig {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return clonePointCopyRoutes(r.activeCopyRoutes)
}

// Run executes the staggered periodic RS-485 polling loops until ctx is canceled.
func (r *FieldPlantRunner) Run(ctx context.Context) error {
	r.logger.Info("field_plant_poller_started",
		"serial_port", r.cfg.SerialPort,
		"meters_enabled", r.cfg.Meters != nil,
		"flow_enabled", r.cfg.FlowMeter != nil,
		"concentrator_enabled", r.cfg.Concentrator != nil,
		"do_enabled", r.cfg.IsDOControlEnabled(),
	)

	if r.cfg.IsDOControlEnabled() {
		if err := r.PollDOOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("field_plant_do_poll_failed", "error", err.Error())
		}
	}
	if r.cfg.FlowMeter != nil {
		if err := r.PollFlowOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("field_plant_flow_poll_failed", "error", err.Error())
		}
	}
	if r.cfg.Meters != nil {
		for _, spec := range r.cfg.Meters.Devices {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := r.pollMeterSpec(ctx, spec); err != nil && ctx.Err() == nil {
				r.logger.Warn("field_plant_meter_poll_failed", "device", spec.DeviceKey, "uid", spec.UnitID, "error", err.Error())
			}
		}
	}
	if r.cfg.Concentrator != nil {
		if err := r.PollTemperaturesOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("field_plant_temp_poll_failed", "error", err.Error())
		}
	}
	if ctx.Err() == nil {
		r.logger.Info("field_plant_initial_poll_complete")
	}

	var tempCh, flowCh, meterCh, doCh <-chan time.Time
	var stopFns []func()
	if r.cfg.Concentrator != nil {
		tk := time.NewTicker(time.Duration(r.cfg.Concentrator.PollIntervalSec) * time.Second)
		tempCh = tk.C
		stopFns = append(stopFns, tk.Stop)
	}
	if r.cfg.FlowMeter != nil {
		tk := time.NewTicker(time.Duration(r.cfg.FlowMeter.PollIntervalSec) * time.Second)
		flowCh = tk.C
		stopFns = append(stopFns, tk.Stop)
	}
	if r.cfg.Meters != nil {
		tk := time.NewTicker(time.Duration(r.cfg.Meters.PollIntervalSec) * time.Second)
		meterCh = tk.C
		stopFns = append(stopFns, tk.Stop)
	}
	if r.cfg.IsDOControlEnabled() {
		tk := time.NewTicker(time.Duration(r.cfg.DOControl.PollIntervalSec) * time.Second)
		doCh = tk.C
		stopFns = append(stopFns, tk.Stop)
	}
	defer func() {
		for _, stop := range stopFns {
			stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tempCh:
			if err := r.PollTemperaturesOnce(ctx); err != nil && ctx.Err() == nil {
				r.logger.Warn("field_plant_temp_poll_failed", "error", err.Error())
			}
		case <-flowCh:
			if err := r.PollFlowOnce(ctx); err != nil && ctx.Err() == nil {
				r.logger.Warn("field_plant_flow_poll_failed", "error", err.Error())
			}
		case <-meterCh:
			for _, spec := range r.cfg.Meters.Devices {
				if ctx.Err() != nil {
					break
				}
				if err := r.pollMeterSpec(ctx, spec); err != nil && ctx.Err() == nil {
					r.logger.Warn("field_plant_meter_poll_failed", "device", spec.DeviceKey, "uid", spec.UnitID, "error", err.Error())
				}
			}
		case <-doCh:
			if err := r.PollDOOnce(ctx); err != nil && ctx.Err() == nil {
				r.logger.Warn("field_plant_do_poll_failed", "error", err.Error())
			}
		}
	}
}

// PollDOOnce reads configured DO coils (coalesced) and emits DO module + auxiliary run_state telemetry.
func (r *FieldPlantRunner) PollDOOnce(ctx context.Context) error {
	if !r.cfg.IsDOControlEnabled() {
		return nil
	}
	doCfg := r.cfg.DOControl
	ranges := coalesceCoilRanges(doCfg.Coils, fieldPlantMaxCoilSpan, fieldPlantMaxCoilGap)
	coilBits := make(map[uint16]int, len(doCfg.Coils))
	for _, rng := range ranges {
		bits, err := r.bus.ReadCoils(ctx, doCfg.UnitID, rng.start, rng.count)
		if err != nil {
			return err
		}
		if len(bits) < int(rng.count) {
			return fmt.Errorf("short DO coil read at %d: got %d bits (want %d)", rng.start, len(bits), rng.count)
		}
		for i := range rng.count {
			v := 0
			if bits[i] != 0 {
				v = 1
			}
			coilBits[rng.start+i] = v
		}
	}

	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)

	doPoints := make([]mqttUplinkPoint, 0, len(doCfg.Coils)+1)
	coilValues := make(map[string]int, len(doCfg.Coils))
	for _, c := range doCfg.Coils {
		v := coilBits[c.Coil]
		coilValues[c.Channel] = v
		doPoints = append(doPoints, mqttUplinkPoint{Code: c.Channel, Value: v, Quality: 0})
	}

	r.mu.Lock()
	for ch, v := range coilValues {
		r.doCoils[ch] = v
	}
	r.mu.Unlock()

	entries := make([]mqttUplinkDevice, 0, len(doCfg.Units)*3+1)
	anyAuxRunning := false
	for _, unit := range doCfg.Units {
		for _, aux := range unit.Auxiliaries {
			on := coilValues[aux.Channel] > 0
			if on {
				anyAuxRunning = true
			}
			entries = append(entries, mqttUplinkDevice{
				DeviceID:        aux.DeviceKey,
				DeviceTimestamp: tsMs,
				Points:          []mqttUplinkPoint{plantStatePoint(on)},
			})
		}
	}
	doPoints = append(doPoints, plantStatePoint(anyAuxRunning))
	entries = append([]mqttUplinkDevice{{DeviceID: doCfg.DeviceKey, DeviceTimestamp: tsMs, Points: doPoints}}, entries...)
	r.emitTelemetry(ctx, tsNow, entries)
	return nil
}

// PollFlowOnce reads the configured holding-register flow meter and optional pump mirror telemetry.
func (r *FieldPlantRunner) PollFlowOnce(ctx context.Context) error {
	f := r.cfg.FlowMeter
	if f == nil {
		return nil
	}
	regs, err := r.bus.ReadHoldingRegisters(ctx, f.UnitID, f.RegisterBase, f.RegisterCount)
	if err != nil {
		return err
	}
	if len(regs) < int(f.RegisterCount) {
		return fmt.Errorf("short flow response from %s: got %d regs", f.DeviceKey, len(regs))
	}
	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)
	flowMeter := float64(regs[f.FlowRegisterOffset]) * f.FlowScale / f.FlowDivisor

	r.mu.Lock()
	r.hasFlow = true
	r.latestFlowTS = tsNow
	r.latestFlowM3H = flowMeter
	r.mu.Unlock()

	m3hUnit := "m3/h"
	flowPts := []mqttUplinkPoint{
		{Code: "flow_meter", Value: flowMeter, Quality: 0, Unit: &m3hUnit},
	}
	if f.VelocityRegisterOffset != nil {
		msUnit := "m/s"
		flowVelocity := float64(regs[*f.VelocityRegisterOffset]) * f.VelocityScale / f.VelocityDivisor
		flowPts = append(flowPts, mqttUplinkPoint{Code: "flow_velocity", Value: flowVelocity, Quality: 0, Unit: &msUnit})
	}

	entries := []mqttUplinkDevice{
		{
			DeviceID:        f.DeviceKey,
			DeviceTimestamp: tsMs,
			Points:          flowPts,
		},
	}
	if f.PumpMirror != nil {
		entries = append(entries, mqttUplinkDevice{
			DeviceID:        f.PumpMirror.DeviceKey,
			DeviceTimestamp: tsMs,
			Points: []mqttUplinkPoint{
				{Code: f.PumpMirror.FlowPointCode, Value: flowMeter, Quality: 0, Unit: &m3hUnit},
				plantStatePoint(flowMeter > f.PumpMirror.RunningThreshold),
			},
		})
	}
	r.emitTelemetry(ctx, tsNow, entries)
	return nil
}

// PollMeterOnce polls one meter by deviceKey.
func (r *FieldPlantRunner) PollMeterOnce(ctx context.Context, deviceKey string) error {
	if r.cfg.Meters == nil {
		return errors.New("fieldPlant.meters is not configured")
	}
	spec, ok := r.meterByKey[strings.TrimSpace(deviceKey)]
	if !ok {
		return fmt.Errorf("unknown meter %q", deviceKey)
	}
	return r.pollMeterSpec(ctx, spec)
}

func (r *FieldPlantRunner) pollMeterSpec(ctx context.Context, spec FieldMeterSpec) error {
	powerKW, energyKWh, err := r.readMeterPowerAndEnergy(ctx, spec)
	if err != nil {
		return err
	}
	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)

	r.mu.Lock()
	var auxKW float64
	if spec.CombinedFeed {
		for ch, kw := range spec.SharedAuxKW {
			if r.doCoils[ch] > 0 {
				auxKW += kw
			}
		}
	}
	startKW, stopKW := spec.StartKW, spec.StopKW
	if spec.CombinedFeed && auxKW == 0 {
		startKW, stopKW = spec.CombinedNoAuxStartKW, spec.CombinedNoAuxStopKW
	}
	chillerKW := math.Max(0, powerKW-auxKW)
	prevRunning := r.meterRunState[spec.DeviceKey]
	running := chillerKW > startKW
	if prevRunning {
		running = chillerKW > stopKW
	}
	r.meterRunState[spec.DeviceKey] = running
	r.meterLatest[spec.DeviceKey] = fieldMeterSample{
		ts:        tsNow,
		powerKW:   powerKW,
		energyKWh: energyKWh,
	}

	var totalDevice *mqttUplinkDevice
	if totalCfg := r.cfg.Meters.SynthesizedTotal; totalCfg != nil {
		freshWindow := time.Duration(r.cfg.Meters.FreshWindowSec) * time.Second
		if allSourceMetersFresh(totalCfg.SourceDeviceKeys, r.meterLatest, tsNow, freshWindow) {
			var sumPower, sumEnergy float64
			for _, srcKey := range totalCfg.SourceDeviceKeys {
				s := r.meterLatest[srcKey]
				sumPower += s.powerKW
				sumEnergy += s.energyKWh
			}
			kwUnit := "kW"
			kwhUnit := "kWh"
			totalDevice = &mqttUplinkDevice{
				DeviceID:        totalCfg.DeviceKey,
				DeviceTimestamp: tsMs,
				Points: []mqttUplinkPoint{
					{Code: "power_total", Value: plantRoundTo(sumPower, 1000), Quality: 0, Unit: &kwUnit},
					{Code: "active_energy_total", Value: plantRoundTo(sumEnergy, 1000), Quality: 0, Unit: &kwhUnit},
				},
			}
		}
	}
	r.mu.Unlock()

	kwUnit := "kW"
	kwhUnit := "kWh"
	entries := []mqttUplinkDevice{
		{
			DeviceID:        spec.DeviceKey,
			DeviceTimestamp: tsMs,
			Points: []mqttUplinkPoint{
				{Code: "power_total", Value: powerKW, Quality: 0, Unit: &kwUnit},
				{Code: "active_energy_total", Value: energyKWh, Quality: 0, Unit: &kwhUnit},
				plantStatePoint(running),
			},
		},
	}
	if spec.ControllerDeviceKey != "" {
		entries = append(entries, mqttUplinkDevice{
			DeviceID:        spec.ControllerDeviceKey,
			DeviceTimestamp: tsMs,
			Points:          []mqttUplinkPoint{plantStatePoint(running)},
		})
	}
	if totalDevice != nil {
		entries = append(entries, *totalDevice)
	}
	r.emitTelemetry(ctx, tsNow, entries)
	return nil
}

func (r *FieldPlantRunner) readMeterPowerAndEnergy(ctx context.Context, spec FieldMeterSpec) (float64, float64, error) {
	pAddr := spec.PowerRegisterAddr
	eAddr := spec.EnergyRegisterAddr
	var pRegs, eRegs []uint16

	switch {
	case eAddr > pAddr && (eAddr+2-pAddr) <= fieldPlantMaxMeterSpanRegs:
		span := eAddr + 2 - pAddr
		regs, err := r.bus.ReadHoldingRegisters(ctx, spec.UnitID, pAddr, span)
		if err != nil {
			return 0, 0, err
		}
		if len(regs) < int(span) {
			return 0, 0, fmt.Errorf("short meter response from %s: got %d regs", spec.DeviceKey, len(regs))
		}
		pRegs = regs[:2]
		eOff := eAddr - pAddr
		eRegs = regs[eOff : eOff+2]
	case pAddr > eAddr && (pAddr+2-eAddr) <= fieldPlantMaxMeterSpanRegs:
		span := pAddr + 2 - eAddr
		regs, err := r.bus.ReadHoldingRegisters(ctx, spec.UnitID, eAddr, span)
		if err != nil {
			return 0, 0, err
		}
		if len(regs) < int(span) {
			return 0, 0, fmt.Errorf("short meter response from %s: got %d regs", spec.DeviceKey, len(regs))
		}
		eRegs = regs[:2]
		pOff := pAddr - eAddr
		pRegs = regs[pOff : pOff+2]
	default:
		var err error
		pRegs, err = r.bus.ReadHoldingRegisters(ctx, spec.UnitID, pAddr, 2)
		if err != nil {
			return 0, 0, err
		}
		if len(pRegs) < 2 {
			return 0, 0, fmt.Errorf("short meter power response from %s: got %d regs", spec.DeviceKey, len(pRegs))
		}
		eRegs, err = r.bus.ReadHoldingRegisters(ctx, spec.UnitID, eAddr, 2)
		if err != nil {
			return 0, 0, err
		}
		if len(eRegs) < 2 {
			return 0, 0, fmt.Errorf("short meter energy response from %s: got %d regs", spec.DeviceKey, len(eRegs))
		}
	}

	powerKW := float64((uint32(pRegs[0])<<16)|uint32(pRegs[1])) * spec.PowerScale
	rawEnergy := float64((uint32(eRegs[0])<<16)|uint32(eRegs[1])) * spec.EnergyScale
	energyKWh := rawEnergy - spec.EnergyBaselineKWh
	return powerKW, energyKWh, nil
}

// PollTemperaturesOnce polls configured concentrator channels (coalesced FC04 reads),
// synthesizes copy targets + diagnostics, and computes virtual BTU capacity & energy.
func (r *FieldPlantRunner) PollTemperaturesOnce(ctx context.Context) error {
	conc := r.cfg.Concentrator
	if conc == nil {
		return nil
	}
	ranges := coalesceChannelRanges(r.activeChannels, fieldPlantMaxChannelSpan, fieldPlantMaxChannelGap)
	if len(ranges) == 0 {
		return nil
	}

	temps := make(map[int]float64, len(r.activeChannels))
	for i, rng := range ranges {
		regs, err := r.bus.ReadInputRegisters(ctx, conc.UnitID, concRegTempBase+uint16(rng.start-1), uint16(rng.count))
		if err != nil {
			if i == 0 {
				return err
			}
			break
		}
		if len(regs) < rng.count {
			if i == 0 {
				return fmt.Errorf("short UID%d temp %d..%d response: %d", conc.UnitID, rng.start, rng.start+rng.count-1, len(regs))
			}
			break
		}
		for offset := range rng.count {
			temps[rng.start+offset] = float64(int16(regs[offset])) / 10.0
		}
	}

	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)

	types, hasTypes := r.readConcentratorBank(ctx, conc.UnitID, concRegSensorTypeBase, ranges)
	volts, hasVolts := r.readConcentratorBank(ctx, conc.UnitID, concRegVoltageBase, ranges)
	rssis, hasRSSIs := r.readConcentratorBank(ctx, conc.UnitID, concRegRSSIBase, ranges)

	haveFullDiag := hasTypes && hasVolts && hasRSSIs
	offlineByCh := make(map[int]bool, len(r.activeChannels))
	var voltPoints, rssiPoints, statusPoints []mqttUplinkPoint

	if haveFullDiag {
		onlineCount := 0
		offlineCount := 0
		lowBatteryCount := 0
		var minVoltageMV *int
		var minRSSIDBm *int
		mvUnit := "mV"
		dbmUnit := "dBm"
		for _, ch := range r.activeChannels {
			sType := types[ch]
			online := sType != 0
			offlineByCh[ch] = !online
			voltMV := int(volts[ch])
			rssiDBm := int(int16(rssis[ch]))

			qDiag := uint8(0)
			if !online {
				qDiag = 1
			}
			voltPoints = append(voltPoints, mqttUplinkPoint{Code: fmt.Sprintf("v%d", ch), Value: voltMV, Quality: qDiag, Unit: &mvUnit})
			rssiPoints = append(rssiPoints, mqttUplinkPoint{Code: fmt.Sprintf("rssi%d", ch), Value: rssiDBm, Quality: qDiag, Unit: &dbmUnit})
			stText := "ONLINE"
			if !online {
				stText = "OFFLINE"
				offlineCount++
			} else {
				onlineCount++
				if voltMV > 0 && voltMV < conc.LowBatteryMV {
					lowBatteryCount++
				}
				if voltMV > 0 && (minVoltageMV == nil || voltMV < *minVoltageMV) {
					v := voltMV
					minVoltageMV = &v
				}
				if minRSSIDBm == nil || rssiDBm < *minRSSIDBm {
					rv := rssiDBm
					minRSSIDBm = &rv
				}
			}
			statusPoints = append(statusPoints, mqttUplinkPoint{Code: fmt.Sprintf("status%d", ch), Value: stText, Quality: 0})
		}
		statusPoints = append(statusPoints,
			mqttUplinkPoint{Code: "online_count", Value: onlineCount, Quality: 0},
			mqttUplinkPoint{Code: "offline_count", Value: offlineCount, Quality: 0},
			mqttUplinkPoint{Code: "low_battery_count", Value: lowBatteryCount, Quality: 0},
		)
		if minVoltageMV != nil {
			mvU := "mV"
			statusPoints = append(statusPoints, mqttUplinkPoint{Code: "min_voltage_mv", Value: *minVoltageMV, Quality: 0, Unit: &mvU})
		}
		if minRSSIDBm != nil {
			dbmU := "dBm"
			statusPoints = append(statusPoints, mqttUplinkPoint{Code: "min_rssi_dbm", Value: *minRSSIDBm, Quality: 0, Unit: &dbmU})
		}
	}

	celUnit := "Cel"
	var tempPoints []mqttUplinkPoint
	goodTempByKey := make(map[string]float64, len(r.activeChannels))
	for _, ch := range r.activeChannels {
		val, ok := temps[ch]
		if !ok {
			continue
		}
		q := uint8(0)
		if val == 0 || offlineByCh[ch] {
			q = 1
		}
		key := fmt.Sprintf("t%d", ch)
		tempPoints = append(tempPoints, mqttUplinkPoint{Code: key, Value: val, Quality: q, Unit: &celUnit})
		if q == 0 {
			goodTempByKey[key] = val
		}
	}

	entries := make([]mqttUplinkDevice, 0, 12)
	if len(tempPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: conc.TempDeviceKey, DeviceTimestamp: tsMs, Points: tempPoints})
	}
	if len(voltPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: conc.VoltageDeviceKey, DeviceTimestamp: tsMs, Points: voltPoints})
	}
	if len(rssiPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: conc.RSSIDeviceKey, DeviceTimestamp: tsMs, Points: rssiPoints})
	}
	if len(statusPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: conc.StatusDeviceKey, DeviceTimestamp: tsMs, Points: statusPoints})
	}

	copyRoutes := r.ActiveCopyRoutes()
	copiedByDev := map[string][]mqttUplinkPoint{}
	copiedSeen := map[string]struct{}{}
	for _, route := range copyRoutes {
		val, ok := goodTempByKey[route.SourceKey]
		if !ok {
			continue
		}
		for _, target := range route.Targets {
			dedupKey := target.DeviceKey + "/" + target.PointCode
			if _, seen := copiedSeen[dedupKey]; seen {
				continue
			}
			copiedSeen[dedupKey] = struct{}{}
			copiedByDev[target.DeviceKey] = append(copiedByDev[target.DeviceKey], mqttUplinkPoint{
				Code: target.PointCode, Value: val, Quality: 0, Unit: &celUnit,
			})
		}
	}
	if len(copiedByDev) > 0 {
		copiedDevs := make([]string, 0, len(copiedByDev))
		for devID := range copiedByDev {
			copiedDevs = append(copiedDevs, devID)
		}
		sort.Strings(copiedDevs)
		for _, devID := range copiedDevs {
			entries = append(entries, mqttUplinkDevice{
				DeviceID:        devID,
				DeviceTimestamp: tsMs,
				Points:          copiedByDev[devID],
			})
		}
	}

	var btuEnergyUpdated bool
	if btu := r.cfg.BTUCalculator; btu != nil && r.cfg.FlowMeter != nil && r.cfg.Meters != nil {
		returnC, okReturn := goodTempByKey[btu.ReturnTempKey]
		supplyC, okSupply := goodTempByKey[btu.SupplyTempKey]
		if okReturn && okSupply {
			flowFreshWindow := time.Duration(r.cfg.FlowMeter.FreshWindowSec) * time.Second
			meterFreshWindow := time.Duration(r.cfg.Meters.FreshWindowSec) * time.Second
			maxGap := time.Duration(btu.IntegrationMaxGapSec) * time.Second

			r.mu.Lock()
			flowFresh := r.hasFlow && tsNow.Sub(r.latestFlowTS) <= flowFreshWindow
			flowVal := r.latestFlowM3H
			freshCount := 0
			plantPower := 0.0
			for _, m := range r.cfg.Meters.Devices {
				if s, ok := r.meterLatest[m.DeviceKey]; ok && tsNow.Sub(s.ts) <= meterFreshWindow {
					freshCount++
					plantPower += s.powerKW
				}
			}
			if flowFresh && freshCount > 0 {
				diff := returnC - supplyC
				capacity := 0.0
				if plantPower > btu.MinPlantPowerKW {
					capacity = math.Max(0, btu.SpecificHeatFactor*flowVal*diff)
				}
				if r.hasPrevCooling && !tsNow.Before(r.prevCoolingTS) && tsNow.Sub(r.prevCoolingTS) <= maxGap {
					dtHours := tsNow.Sub(r.prevCoolingTS).Hours()
					delta := ((r.prevCoolingKW + capacity) / 2.0) * dtHours
					if delta > 0 {
						r.coolingEnergy += delta
						btuEnergyUpdated = true
					}
				}
				r.hasPrevCooling = true
				r.prevCoolingTS = tsNow
				r.prevCoolingKW = capacity
				accKWh := plantRoundTo(r.coolingEnergy, 1000)

				kUnit := "K"
				m3hUnit := "m3/h"
				kwUnit := "kW"
				kwhUnit := "kWh"
				entries = append(entries, mqttUplinkDevice{
					DeviceID:        btu.DeviceKey,
					DeviceTimestamp: tsMs,
					Points: []mqttUplinkPoint{
						{Code: "supply_water_temperature", Value: supplyC, Quality: 0, Unit: &celUnit},
						{Code: "return_water_temperature", Value: returnC, Quality: 0, Unit: &celUnit},
						{Code: "temperature_difference", Value: plantRoundTo(diff, 100), Quality: 0, Unit: &kUnit},
						{Code: "flow_rate", Value: flowVal, Quality: 0, Unit: &m3hUnit},
						{Code: "instant_cooling_capacity", Value: plantRoundTo(capacity, 10), Quality: 0, Unit: &kwUnit},
						{Code: "accumulated_cooling_energy", Value: accKWh, Quality: 0, Unit: &kwhUnit},
					},
				})
			}
			r.mu.Unlock()
		}
	}

	if btuEnergyUpdated {
		r.saveState()
	}
	if len(entries) > 0 {
		r.emitTelemetry(ctx, tsNow, entries)
	}
	return nil
}

func (r *FieldPlantRunner) readConcentratorBank(ctx context.Context, unitID uint8, base uint16, ranges []channelRange) (map[int]uint16, bool) {
	out := make(map[int]uint16, len(r.activeChannels))
	for i, rng := range ranges {
		regs, err := r.bus.ReadInputRegisters(ctx, unitID, base+uint16(rng.start-1), uint16(rng.count))
		if err != nil || len(regs) < rng.count {
			if i == 0 {
				return nil, false
			}
			break
		}
		for offset := range rng.count {
			out[rng.start+offset] = regs[offset]
		}
	}
	return out, true
}

func (r *FieldPlantRunner) emitTelemetry(ctx context.Context, now time.Time, devices []mqttUplinkDevice) {
	if r.publishTelemetry == nil || len(devices) == 0 {
		return
	}
	tsMs := max(now.UTC().UnixMilli(), 1)
	r.mu.Lock()
	nextSeq := uint64(tsMs) * 1000
	if nextSeq <= r.sequence {
		r.sequence++
		nextSeq = r.sequence
	} else {
		r.sequence = nextSeq
	}
	r.mu.Unlock()

	wire, err := json.Marshal(mqttUplinkTelemetryEnvelope{
		SchemaVersion: "2.0",
		MessageID:     r.uuidv7(now),
		GatewayID:     r.gatewayID,
		Timestamp:     tsMs,
		Sequence:      nextSeq,
		Replay:        false,
		Payload:       mqttUplinkDevicePayload{Devices: devices},
	})
	if err != nil {
		return
	}
	r.publishTelemetry(ctx, wire)
}

func (r *FieldPlantRunner) loadState() {
	if r.statePath == "" {
		return
	}
	raw, err := os.ReadFile(r.statePath)
	if err != nil {
		return
	}
	var st fieldPlantPersistedState
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	r.mu.Lock()
	r.sequence = st.LastSequence
	if st.CoolingEnergyKWh > 0 && !math.IsNaN(st.CoolingEnergyKWh) && !math.IsInf(st.CoolingEnergyKWh, 0) {
		r.coolingEnergy = st.CoolingEnergyKWh
	}
	r.mu.Unlock()
}

func (r *FieldPlantRunner) saveState() {
	if r.statePath == "" {
		return
	}
	r.mu.Lock()
	st := fieldPlantPersistedState{
		LastSequence:     r.sequence,
		CoolingEnergyKWh: r.coolingEnergy,
	}
	r.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(r.statePath), 0o750); err != nil {
		return
	}
	if encoded, err := json.MarshalIndent(st, "", "  "); err == nil {
		_ = writeFileAtomic(r.statePath, append(encoded, '\n'), 0o600)
	}
}

func plantStatePoint(running bool) mqttUplinkPoint {
	v := "STOPPED"
	if running {
		v = "RUNNING"
	}
	return mqttUplinkPoint{Code: "run_state", Value: v, Quality: 0}
}

func allSourceMetersFresh(sourceKeys []string, latest map[string]fieldMeterSample, now time.Time, freshWindow time.Duration) bool {
	if len(sourceKeys) == 0 {
		return false
	}
	for _, devKey := range sourceKeys {
		s, ok := latest[devKey]
		if !ok || now.Sub(s.ts) > freshWindow {
			return false
		}
	}
	return true
}

func coalesceChannelRanges(channels []int, maxSpan, maxGap int) []channelRange {
	if len(channels) == 0 {
		return nil
	}
	sorted := append([]int(nil), channels...)
	sort.Ints(sorted)
	var out []channelRange
	start := sorted[0]
	prev := sorted[0]
	for _, ch := range sorted[1:] {
		if ch == prev {
			continue
		}
		if (ch-start+1) <= maxSpan && (ch-prev) <= (maxGap+1) {
			prev = ch
			continue
		}
		out = append(out, channelRange{start: start, count: prev - start + 1})
		start = ch
		prev = ch
	}
	out = append(out, channelRange{start: start, count: prev - start + 1})
	return out
}

func coalesceCoilRanges(coils []DOCoilConfig, maxSpan, maxGap int) []coilRange {
	if len(coils) == 0 {
		return nil
	}
	addrs := make([]int, len(coils))
	for i, c := range coils {
		addrs[i] = int(c.Coil)
	}
	ranges := coalesceChannelRanges(addrs, maxSpan, maxGap)
	out := make([]coilRange, len(ranges))
	for i, rng := range ranges {
		out[i] = coilRange{start: uint16(rng.start), count: uint16(rng.count)}
	}
	return out
}

func cloneFieldPlantConfig(src FieldPlantConfig) FieldPlantConfig {
	out := src
	if src.ProtectedUnitIDs != nil {
		out.ProtectedUnitIDs = append([]uint8(nil), src.ProtectedUnitIDs...)
	}
	if src.ConcentratorRPC != nil {
		cp := *src.ConcentratorRPC
		out.ConcentratorRPC = &cp
	}
	if src.Meters != nil {
		cp := *src.Meters
		cp.Devices = make([]FieldMeterSpec, len(src.Meters.Devices))
		for i, d := range src.Meters.Devices {
			dCopy := d
			if d.SharedAuxKW != nil {
				dCopy.SharedAuxKW = make(map[string]float64, len(d.SharedAuxKW))
				for k, v := range d.SharedAuxKW {
					dCopy.SharedAuxKW[k] = v
				}
			}
			cp.Devices[i] = dCopy
		}
		if src.Meters.SynthesizedTotal != nil {
			t := *src.Meters.SynthesizedTotal
			t.SourceDeviceKeys = append([]string(nil), t.SourceDeviceKeys...)
			cp.SynthesizedTotal = &t
		}
		out.Meters = &cp
	}
	if src.FlowMeter != nil {
		cp := *src.FlowMeter
		if src.FlowMeter.VelocityRegisterOffset != nil {
			v := *src.FlowMeter.VelocityRegisterOffset
			cp.VelocityRegisterOffset = &v
		}
		if src.FlowMeter.PumpMirror != nil {
			pm := *src.FlowMeter.PumpMirror
			cp.PumpMirror = &pm
		}
		out.FlowMeter = &cp
	}
	if src.Concentrator != nil {
		cp := *src.Concentrator
		cp.ActiveChannels = append([]int(nil), src.Concentrator.ActiveChannels...)
		cp.CopyRoutes = clonePointCopyRoutes(src.Concentrator.CopyRoutes)
		out.Concentrator = &cp
	}
	if src.BTUCalculator != nil {
		cp := *src.BTUCalculator
		out.BTUCalculator = &cp
	}
	if src.DOControl != nil {
		cp := *src.DOControl
		if src.DOControl.Enabled != nil {
			en := *src.DOControl.Enabled
			cp.Enabled = &en
		}
		if src.DOControl.ChillerStopPowerKW != nil {
			kw := *src.DOControl.ChillerStopPowerKW
			cp.ChillerStopPowerKW = &kw
		}
		if src.DOControl.Timings != nil {
			tm := *src.DOControl.Timings
			cp.Timings = &tm
		}
		cp.Coils = append([]DOCoilConfig(nil), src.DOControl.Coils...)
		cp.Units = make([]DOUnitTrainConfig, len(src.DOControl.Units))
		for i, u := range src.DOControl.Units {
			uCopy := u
			uCopy.Auxiliaries = make([]DOAuxStepConfig, len(u.Auxiliaries))
			for j, aux := range u.Auxiliaries {
				aCopy := aux
				if aux.AfterStartDelayMS != nil {
					d := *aux.AfterStartDelayMS
					aCopy.AfterStartDelayMS = &d
				}
				uCopy.Auxiliaries[j] = aCopy
			}
			cp.Units[i] = uCopy
		}
		out.DOControl = &cp
	}
	return out
}

func clonePointCopyRoutes(src []PointCopyRouteConfig) []PointCopyRouteConfig {
	if len(src) == 0 {
		return nil
	}
	out := make([]PointCopyRouteConfig, len(src))
	for i, r := range src {
		out[i] = PointCopyRouteConfig{
			SourceKey: r.SourceKey,
			Targets:   append([]PointCopyTarget(nil), r.Targets...),
		}
	}
	return out
}

func plantRoundTo(value, scale float64) float64 {
	return math.Round(value*scale) / scale
}
