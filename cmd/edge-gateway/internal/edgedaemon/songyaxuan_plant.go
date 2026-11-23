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
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	songyaxuanPlantStateFileName = "songyaxuan-plant-state.json"
	meterFreshWindow             = 900 * time.Second
	flowFreshWindow              = 180 * time.Second
	coolingIntegrationGap        = 600 * time.Second
	ammeterUnit4EnergyBaseline   = 76938.0
)

// SongyaxuanPlantBus abstracts the raw Modbus RTU read operations needed for Songyaxuan plant polling.
type SongyaxuanPlantBus interface {
	ReadCoils(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
	ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
	ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
}

// ModbusSongyaxuanPlantBus adapts *edgecontrol.ModbusTCPBridge to SongyaxuanPlantBus.
type ModbusSongyaxuanPlantBus struct {
	Bridge *edgecontrol.ModbusTCPBridge
}

func (b ModbusSongyaxuanPlantBus) ReadCoils(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("songyaxuan plant modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name: fmt.Sprintf("plant-fc01-%d-%d", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadCoils, Address: address, Quantity: quantity,
	})
}

func (b ModbusSongyaxuanPlantBus) ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("songyaxuan plant modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name: fmt.Sprintf("plant-fc03-%d-%d", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadHoldingRegisters, Address: address, Quantity: quantity,
	})
}

func (b ModbusSongyaxuanPlantBus) ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("songyaxuan plant modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name: fmt.Sprintf("plant-fc04-%d-%d", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadInputRegisters, Address: address, Quantity: quantity,
	})
}

type songyaxuanMeterSpec struct {
	unitID       uint8
	deviceID     string
	controllerID string
	baselineKWh  float64
	combinedFeed bool
	sharedAuxKW  map[string]float64
}

var songyaxuanMeterSpecs = []songyaxuanMeterSpec{
	{
		unitID: 5, deviceID: "ammeter-unit1", controllerID: "ctrl-chiller-c", combinedFeed: true,
		sharedAuxKW: map[string]float64{"u31": 15, "u32": 15, "u33": 4},
	},
	{
		unitID: 2, deviceID: "ammeter-unit2", controllerID: "ctrl-chiller-d", combinedFeed: true,
		sharedAuxKW: map[string]float64{"u41": 15, "u42": 15, "u43": 4},
	},
	{unitID: 3, deviceID: "ammeter-unit3", controllerID: "ctrl-chiller-b"},
	{unitID: 4, deviceID: "ammeter-unit4", controllerID: "ctrl-chiller-a", baselineKWh: ammeterUnit4EnergyBaseline},
}

var songyaxuanChillerMeterUnit = map[ET1010UnitCode]uint8{
	ET1010UnitA: 4,
	ET1010UnitB: 3,
	ET1010UnitC: 5,
	ET1010UnitD: 2,
}

var songyaxuanActiveConcChannels = []int{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
	53, 54, 55,
}

type songyaxuanCopyTarget struct {
	deviceID string
	code     string
}

var songyaxuanTempCopyTargets = map[string][]songyaxuanCopyTarget{
	"t1":  {{deviceID: "ammeter-unit4", code: "chw_leaving_temperature"}},
	"t6":  {{deviceID: "ammeter-unit4", code: "cw_leaving_temperature"}},
	"t2":  {{deviceID: "ammeter-unit3", code: "chw_leaving_temperature"}},
	"t7":  {{deviceID: "ammeter-unit3", code: "cw_leaving_temperature"}},
	"t3":  {{deviceID: "ammeter-unit1", code: "chw_leaving_temperature"}},
	"t8":  {{deviceID: "ammeter-unit1", code: "cw_leaving_temperature"}},
	"t4":  {{deviceID: "ammeter-unit2", code: "chw_leaving_temperature"}},
	"t9":  {{deviceID: "ammeter-unit2", code: "cw_leaving_temperature"}},
	"t10": {{deviceID: "ammeter-unit4", code: "chw_entering_temperature_branch"}},
	"t16": {
		{deviceID: "ammeter-unit1", code: "chw_entering_temperature"},
		{deviceID: "ammeter-unit2", code: "chw_entering_temperature"},
		{deviceID: "ammeter-unit3", code: "chw_entering_temperature"},
		{deviceID: "ammeter-unit4", code: "chw_entering_temperature"},
	},
	"t5": {
		{deviceID: "ammeter-unit1", code: "cw_entering_temperature"},
		{deviceID: "ammeter-unit2", code: "cw_entering_temperature"},
		{deviceID: "ammeter-unit3", code: "cw_entering_temperature"},
		{deviceID: "ammeter-unit4", code: "cw_entering_temperature"},
	},
	"t55": {{deviceID: "cooling-tower", code: "cooling_tower_leaving_temperature"}},
	"t12": {{deviceID: "cooling-tower", code: "cooling_tower_leaving_temperature"}},
	"t13": {{deviceID: "cooling-tower", code: "cooling_tower_entering_temperature"}},
	"t15": {{deviceID: "outdoor", code: "ambient_temperature"}},
	"t53": {{deviceID: "room", code: "room_temperature"}},
	"t54": {{deviceID: "room", code: "banquet_room_temperature"}},
}

var songyaxuanTempCopyOrder = []string{
	"t1", "t6", "t2", "t7", "t3", "t8", "t4", "t9", "t10", "t16", "t5", "t55", "t12", "t13", "t15", "t53", "t54",
}

type songyaxuanMeterSample struct {
	ts        time.Time
	powerKW   float64
	energyKWh float64
}

type songyaxuanPlantPersistedState struct {
	LastSequence     uint64  `json:"lastSequence"`
	CoolingEnergyKWh float64 `json:"coolingEnergyKWh"`
}

// SongyaxuanPlantOptions configures the Songyaxuan RS-485 plant poller and envelope builder.
type SongyaxuanPlantOptions struct {
	GatewayID        string
	StateDir         string
	Config           SongyaxuanPlantConfig
	Bus              SongyaxuanPlantBus
	PublishTelemetry func(ctx context.Context, payload []byte)
	Now              func() time.Time
	UUIDv7           func(now time.Time) string
	Logger           *slog.Logger
}

// SongyaxuanPlantRunner polls Songyaxuan RS-485 field instruments and publishes schema 2.0 telemetry.
type SongyaxuanPlantRunner struct {
	gatewayID        string
	statePath        string
	cfg              SongyaxuanPlantConfig
	bus              SongyaxuanPlantBus
	publishTelemetry func(ctx context.Context, payload []byte)
	now              func() time.Time
	uuidv7           func(now time.Time) string
	logger           *slog.Logger

	mu             sync.Mutex
	sequence       uint64
	et1010Coils    map[string]int
	meterRunState  map[string]bool
	meterLatest    map[string]songyaxuanMeterSample
	hasFlow        bool
	latestFlowTS   time.Time
	latestFlowM3H  float64
	hasPrevCooling bool
	prevCoolingTS  time.Time
	prevCoolingKW  float64
	coolingEnergy  float64
}

// NewSongyaxuanPlantRunner builds a SongyaxuanPlantRunner and loads durable cooling state.
func NewSongyaxuanPlantRunner(opts SongyaxuanPlantOptions) (*SongyaxuanPlantRunner, error) {
	gwID := strings.TrimSpace(opts.GatewayID)
	if gwID == "" {
		return nil, errors.New("songyaxuan plant requires gatewayId")
	}
	if opts.Bus == nil {
		return nil, errors.New("songyaxuan plant requires modbus bus")
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
		statePath = filepath.Join(trimmed, songyaxuanPlantStateFileName)
	}
	runner := &SongyaxuanPlantRunner{
		gatewayID:        gwID,
		statePath:        statePath,
		cfg:              opts.Config,
		bus:              opts.Bus,
		publishTelemetry: opts.PublishTelemetry,
		now:              nowFn,
		uuidv7:           uuidFn,
		logger:           logger,
		et1010Coils:      map[string]int{},
		meterRunState:    map[string]bool{},
		meterLatest:      map[string]songyaxuanMeterSample{},
	}
	runner.loadState()
	return runner, nil
}

// ReadChillerPowerKW performs a live Modbus FC03 power read (kW) for ET1010 STOP verification.
func (r *SongyaxuanPlantRunner) ReadChillerPowerKW(ctx context.Context, unit ET1010UnitCode) (*float64, error) {
	if r == nil {
		return nil, errors.New("songyaxuan plant runner is nil")
	}
	unitID, ok := songyaxuanChillerMeterUnit[unit]
	if !ok {
		return nil, fmt.Errorf("unknown chiller unit %q", unit)
	}
	regs, err := r.bus.ReadHoldingRegisters(ctx, unitID, 36, 2)
	if err != nil {
		return nil, err
	}
	if len(regs) < 2 {
		return nil, fmt.Errorf("short meter power response from uid %d", unitID)
	}
	raw := (uint32(regs[0]) << 16) | uint32(regs[1])
	kw := float64(raw) / 10.0
	return &kw, nil
}

// Run executes the staggered periodic RS-485 polling loops until ctx is canceled.
func (r *SongyaxuanPlantRunner) Run(ctx context.Context) error {
	r.logger.Info("songyaxuan_plant_poller_started",
		"serial_port", r.cfg.SerialPort,
		"temp_sec", r.cfg.TempPollIntervalSec,
		"flow_sec", r.cfg.FlowPollIntervalSec,
		"meter_sec", r.cfg.MeterPollIntervalSec,
		"et1010_sec", r.cfg.ET1010PollIntervalSec,
	)

	if err := r.PollET1010Once(ctx); err != nil && ctx.Err() == nil {
		r.logger.Warn("songyaxuan_et1010_poll_failed", "error", err.Error())
	}
	if err := r.PollFlowOnce(ctx); err != nil && ctx.Err() == nil {
		r.logger.Warn("songyaxuan_flow_poll_failed", "error", err.Error())
	}
	for _, spec := range songyaxuanMeterSpecs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.pollAmmeterSpec(ctx, spec); err != nil && ctx.Err() == nil {
			r.logger.Warn("songyaxuan_meter_poll_failed", "device", spec.deviceID, "uid", spec.unitID, "error", err.Error())
		}
	}
	if err := r.PollTemperaturesOnce(ctx); err != nil && ctx.Err() == nil {
		r.logger.Warn("songyaxuan_temp_poll_failed", "error", err.Error())
	}
	if ctx.Err() == nil {
		r.logger.Info("songyaxuan_initial_poll_complete")
	}

	tempTicker := time.NewTicker(time.Duration(r.cfg.TempPollIntervalSec) * time.Second)
	flowTicker := time.NewTicker(time.Duration(r.cfg.FlowPollIntervalSec) * time.Second)
	meterTicker := time.NewTicker(time.Duration(r.cfg.MeterPollIntervalSec) * time.Second)
	etTicker := time.NewTicker(time.Duration(r.cfg.ET1010PollIntervalSec) * time.Second)
	defer func() {
		tempTicker.Stop()
		flowTicker.Stop()
		meterTicker.Stop()
		etTicker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tempTicker.C:
			if err := r.PollTemperaturesOnce(ctx); err != nil && ctx.Err() == nil {
				r.logger.Warn("songyaxuan_temp_poll_failed", "error", err.Error())
			}
		case <-flowTicker.C:
			if err := r.PollFlowOnce(ctx); err != nil && ctx.Err() == nil {
				r.logger.Warn("songyaxuan_flow_poll_failed", "error", err.Error())
			}
		case <-meterTicker.C:
			for _, spec := range songyaxuanMeterSpecs {
				if ctx.Err() != nil {
					break
				}
				if err := r.pollAmmeterSpec(ctx, spec); err != nil && ctx.Err() == nil {
					r.logger.Warn("songyaxuan_meter_poll_failed", "device", spec.deviceID, "uid", spec.unitID, "error", err.Error())
				}
			}
		case <-etTicker.C:
			if err := r.PollET1010Once(ctx); err != nil && ctx.Err() == nil {
				r.logger.Warn("songyaxuan_et1010_poll_failed", "error", err.Error())
			}
		}
	}
}

// PollET1010Once reads UID 31 coils 0..19 and emits et1010 + auxiliary controller run_state telemetry.
func (r *SongyaxuanPlantRunner) PollET1010Once(ctx context.Context) error {
	if !r.cfg.IsET1010Enabled() {
		return nil
	}
	bits, err := r.bus.ReadCoils(ctx, r.cfg.ET1010UnitID, 0, 20)
	if err != nil {
		return err
	}
	if len(bits) < 20 {
		return fmt.Errorf("short ET1010 coil read: got %d bits", len(bits))
	}
	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)

	etPoints := make([]mqttUplinkPoint, 0, 21)
	coilValues := make(map[string]int, 20)
	for i, c := range et1010AllCoils {
		v := 0
		if bits[i] != 0 {
			v = 1
		}
		coilValues[c.channel] = v
		etPoints = append(etPoints, mqttUplinkPoint{Code: c.channel, Value: v, Quality: 0})
	}

	r.mu.Lock()
	for _, ch := range []string{"u31", "u32", "u33", "u41", "u42", "u43"} {
		r.et1010Coils[ch] = coilValues[ch]
	}
	r.mu.Unlock()

	entries := make([]mqttUplinkDevice, 0, len(et1010AllAuxParts)+1)
	anyAuxRunning := false
	for _, part := range et1010AllAuxParts {
		on := coilValues[part.channel] > 0
		if on {
			anyAuxRunning = true
		}
		entries = append(entries, mqttUplinkDevice{
			DeviceID:        part.deviceID,
			DeviceTimestamp: tsMs,
			Points:          []mqttUplinkPoint{plantStatePoint(on)},
		})
	}
	etPoints = append(etPoints, plantStatePoint(anyAuxRunning))
	entries = append([]mqttUplinkDevice{{DeviceID: "et1010", DeviceTimestamp: tsMs, Points: etPoints}}, entries...)
	r.emitTelemetry(ctx, tsNow, entries)
	return nil
}

// PollFlowOnce reads UID 10 flowMeter + flowVelocity registers and updates BTU flow state.
func (r *SongyaxuanPlantRunner) PollFlowOnce(ctx context.Context) error {
	regs, err := r.bus.ReadHoldingRegisters(ctx, 10, 1, 5)
	if err != nil {
		return err
	}
	if len(regs) < 5 {
		return fmt.Errorf("short waterflow response: got %d regs", len(regs))
	}
	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)
	flowMeter := float64(regs[0]) / 35.315
	flowVelocity := float64(regs[4]) * 0.3048 / 3600.0

	r.mu.Lock()
	r.hasFlow = true
	r.latestFlowTS = tsNow
	r.latestFlowM3H = flowMeter
	r.mu.Unlock()

	m3hUnit := "m3/h"
	msUnit := "m/s"
	entries := []mqttUplinkDevice{
		{
			DeviceID:        "waterflow",
			DeviceTimestamp: tsMs,
			Points: []mqttUplinkPoint{
				{Code: "flow_meter", Value: flowMeter, Quality: 0, Unit: &m3hUnit},
				{Code: "flow_velocity", Value: flowVelocity, Quality: 0, Unit: &msUnit},
			},
		},
		{
			DeviceID:        "chwp-main",
			DeviceTimestamp: tsMs,
			Points: []mqttUplinkPoint{
				{Code: "flow_rate", Value: flowMeter, Quality: 0, Unit: &m3hUnit},
				plantStatePoint(flowMeter > 10),
			},
		},
	}
	r.emitTelemetry(ctx, tsNow, entries)
	return nil
}

// PollAmmeterOnce polls one meter by deviceID ("ammeter-unit1".."ammeter-unit4").
func (r *SongyaxuanPlantRunner) PollAmmeterOnce(ctx context.Context, deviceID string) error {
	for _, spec := range songyaxuanMeterSpecs {
		if spec.deviceID == deviceID {
			return r.pollAmmeterSpec(ctx, spec)
		}
	}
	return fmt.Errorf("unknown ammeter %q", deviceID)
}

func (r *SongyaxuanPlantRunner) pollAmmeterSpec(ctx context.Context, spec songyaxuanMeterSpec) error {
	regs, err := r.bus.ReadHoldingRegisters(ctx, spec.unitID, 36, 26)
	if err != nil {
		return err
	}
	if len(regs) < 26 {
		return fmt.Errorf("short ammeter response from %s: got %d regs", spec.deviceID, len(regs))
	}
	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)
	powerKW := float64((uint32(regs[0])<<16)|uint32(regs[1])) / 10.0
	rawEnergy := float64((uint32(regs[24]) << 16) | uint32(regs[25]))
	energyKWh := rawEnergy - spec.baselineKWh

	r.mu.Lock()
	var auxKW float64
	if spec.combinedFeed {
		for ch, kw := range spec.sharedAuxKW {
			if r.et1010Coils[ch] > 0 {
				auxKW += kw
			}
		}
	}
	startKW, stopKW := 12.75, 5.0
	if spec.combinedFeed && auxKW == 0 {
		startKW, stopKW = 57.75, 45.0
	}
	chillerKW := math.Max(0, powerKW-auxKW)
	prevRunning := r.meterRunState[spec.deviceID]
	running := chillerKW > startKW
	if prevRunning {
		running = chillerKW > stopKW
	}
	r.meterRunState[spec.deviceID] = running
	r.meterLatest[spec.deviceID] = songyaxuanMeterSample{
		ts:        tsNow,
		powerKW:   powerKW,
		energyKWh: energyKWh,
	}

	var totalDevice *mqttUplinkDevice
	if allMetersFresh(r.meterLatest, tsNow) {
		var sumPower, sumEnergy float64
		for _, m := range songyaxuanMeterSpecs {
			s := r.meterLatest[m.deviceID]
			sumPower += s.powerKW
			sumEnergy += s.energyKWh
		}
		kwUnit := "kW"
		kwhUnit := "kWh"
		totalDevice = &mqttUplinkDevice{
			DeviceID:        "ammeter-total",
			DeviceTimestamp: tsMs,
			Points: []mqttUplinkPoint{
				{Code: "power_total", Value: plantRoundTo(sumPower, 1000), Quality: 0, Unit: &kwUnit},
				{Code: "active_energy_total", Value: plantRoundTo(sumEnergy, 1000), Quality: 0, Unit: &kwhUnit},
			},
		}
	}
	r.mu.Unlock()

	kwUnit := "kW"
	kwhUnit := "kWh"
	entries := []mqttUplinkDevice{
		{
			DeviceID:        spec.deviceID,
			DeviceTimestamp: tsMs,
			Points: []mqttUplinkPoint{
				{Code: "power_total", Value: powerKW, Quality: 0, Unit: &kwUnit},
				{Code: "active_energy_total", Value: energyKWh, Quality: 0, Unit: &kwhUnit},
				plantStatePoint(running),
			},
		},
		{
			DeviceID:        spec.controllerID,
			DeviceTimestamp: tsMs,
			Points:          []mqttUplinkPoint{plantStatePoint(running)},
		},
	}
	if totalDevice != nil {
		entries = append(entries, *totalDevice)
	}
	r.emitTelemetry(ctx, tsNow, entries)
	return nil
}

// PollTemperaturesOnce polls UID 20 input registers (temps, sensorTypes, voltages, RSSIs),
// synthesizes copies + diagnostics, and computes btu-calc cooling capacity & accumulated energy.
func (r *SongyaxuanPlantRunner) PollTemperaturesOnce(ctx context.Context) error {
	uid := r.cfg.ConcentratorUnitID
	if uid == 0 {
		uid = 20
	}
	tMain, err := r.bus.ReadInputRegisters(ctx, uid, concRegTempBase, 20)
	if err != nil {
		return err
	}
	if len(tMain) < 20 {
		return fmt.Errorf("short UID%d temp 1..20 response: %d", uid, len(tMain))
	}
	tsNow := r.now().UTC()
	tsMs := max(tsNow.UnixMilli(), 1)

	temps := make(map[int]float64, 25)
	for i := range 20 {
		temps[i+1] = float64(int16(tMain[i])) / 10.0
	}
	if tExt, extErr := r.bus.ReadInputRegisters(ctx, uid, concRegTempBase+50, 5); extErr == nil && len(tExt) >= 5 {
		for i := range 5 {
			temps[51+i] = float64(int16(tExt[i])) / 10.0
		}
	}

	var types map[int]uint16
	var volts map[int]uint16
	var rssis map[int]int
	if stMain, stErr := r.bus.ReadInputRegisters(ctx, uid, concRegSensorTypeBase, 20); stErr == nil && len(stMain) >= 20 {
		types = make(map[int]uint16, 25)
		for i := range 20 {
			types[i+1] = stMain[i]
		}
		if stExt, stExtErr := r.bus.ReadInputRegisters(ctx, uid, concRegSensorTypeBase+50, 5); stExtErr == nil && len(stExt) >= 5 {
			for i := range 5 {
				types[51+i] = stExt[i]
			}
		}
	}
	if vMain, vErr := r.bus.ReadInputRegisters(ctx, uid, concRegVoltageBase, 20); vErr == nil && len(vMain) >= 20 {
		volts = make(map[int]uint16, 25)
		for i := range 20 {
			volts[i+1] = vMain[i]
		}
		if vExt, vExtErr := r.bus.ReadInputRegisters(ctx, uid, concRegVoltageBase+50, 5); vExtErr == nil && len(vExt) >= 5 {
			for i := range 5 {
				volts[51+i] = vExt[i]
			}
		}
	}
	if rMain, rErr := r.bus.ReadInputRegisters(ctx, uid, concRegRSSIBase, 20); rErr == nil && len(rMain) >= 20 {
		rssis = make(map[int]int, 25)
		for i := range 20 {
			rssis[i+1] = int(int16(rMain[i]))
		}
		if rExt, rExtErr := r.bus.ReadInputRegisters(ctx, uid, concRegRSSIBase+50, 5); rExtErr == nil && len(rExt) >= 5 {
			for i := range 5 {
				rssis[51+i] = int(int16(rExt[i]))
			}
		}
	}

	haveFullDiag := types != nil && volts != nil && rssis != nil
	offlineByCh := make(map[int]bool, len(songyaxuanActiveConcChannels))
	var voltPoints, rssiPoints, statusPoints []mqttUplinkPoint

	if haveFullDiag {
		onlineCount := 0
		offlineCount := 0
		lowBatteryCount := 0
		var minVoltageMV *int
		var minRSSIDBm *int
		mvUnit := "mV"
		dbmUnit := "dBm"
		for _, ch := range songyaxuanActiveConcChannels {
			sType := types[ch]
			online := sType != 0
			offlineByCh[ch] = !online
			voltMV := int(volts[ch])
			rssiDBm := rssis[ch]

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
				if voltMV > 0 && voltMV < 2700 {
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
	goodTempByKey := make(map[string]float64, len(songyaxuanActiveConcChannels))
	for _, ch := range songyaxuanActiveConcChannels {
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
		entries = append(entries, mqttUplinkDevice{DeviceID: "temperature", DeviceTimestamp: tsMs, Points: tempPoints})
	}
	if len(voltPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: "temperature-voltage", DeviceTimestamp: tsMs, Points: voltPoints})
	}
	if len(rssiPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: "temperature-rssi", DeviceTimestamp: tsMs, Points: rssiPoints})
	}
	if len(statusPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: "temperature-status", DeviceTimestamp: tsMs, Points: statusPoints})
	}

	copiedByDev := map[string][]mqttUplinkPoint{}
	copiedSeen := map[string]struct{}{}
	for _, key := range songyaxuanTempCopyOrder {
		val, ok := goodTempByKey[key]
		if !ok {
			continue
		}
		for _, target := range songyaxuanTempCopyTargets[key] {
			dedupKey := target.deviceID + "/" + target.code
			if _, seen := copiedSeen[dedupKey]; seen {
				continue
			}
			copiedSeen[dedupKey] = struct{}{}
			copiedByDev[target.deviceID] = append(copiedByDev[target.deviceID], mqttUplinkPoint{
				Code: target.code, Value: val, Quality: 0, Unit: &celUnit,
			})
		}
	}
	for _, devID := range []string{"ammeter-unit1", "ammeter-unit2", "ammeter-unit3", "ammeter-unit4", "cooling-tower", "outdoor", "room"} {
		if pts := copiedByDev[devID]; len(pts) > 0 {
			entries = append(entries, mqttUplinkDevice{DeviceID: devID, DeviceTimestamp: tsMs, Points: pts})
		}
	}

	t16, ok16 := goodTempByKey["t16"]
	t18, ok18 := goodTempByKey["t18"]
	var btuEnergyUpdated bool
	if ok16 && ok18 {
		r.mu.Lock()
		flowFresh := r.hasFlow && tsNow.Sub(r.latestFlowTS) <= flowFreshWindow
		flowVal := r.latestFlowM3H
		freshCount := 0
		plantPower := 0.0
		for _, m := range songyaxuanMeterSpecs {
			if s, ok := r.meterLatest[m.deviceID]; ok && tsNow.Sub(s.ts) <= meterFreshWindow {
				freshCount++
				plantPower += s.powerKW
			}
		}
		if flowFresh && freshCount > 0 {
			diff := t16 - t18
			capacity := 0.0
			if plantPower > 20 {
				capacity = math.Max(0, 1.163*flowVal*diff)
			}
			if r.hasPrevCooling && !tsNow.Before(r.prevCoolingTS) && tsNow.Sub(r.prevCoolingTS) <= coolingIntegrationGap {
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
				DeviceID:        "btu-calc",
				DeviceTimestamp: tsMs,
				Points: []mqttUplinkPoint{
					{Code: "supply_water_temperature", Value: t18, Quality: 0, Unit: &celUnit},
					{Code: "return_water_temperature", Value: t16, Quality: 0, Unit: &celUnit},
					{Code: "temperature_difference", Value: plantRoundTo(diff, 100), Quality: 0, Unit: &kUnit},
					{Code: "flow_rate", Value: flowVal, Quality: 0, Unit: &m3hUnit},
					{Code: "instant_cooling_capacity", Value: plantRoundTo(capacity, 10), Quality: 0, Unit: &kwUnit},
					{Code: "accumulated_cooling_energy", Value: accKWh, Quality: 0, Unit: &kwhUnit},
				},
			})
		}
		r.mu.Unlock()
	}

	if btuEnergyUpdated {
		r.saveState()
	}
	if len(entries) > 0 {
		r.emitTelemetry(ctx, tsNow, entries)
	}
	return nil
}

func (r *SongyaxuanPlantRunner) emitTelemetry(ctx context.Context, now time.Time, devices []mqttUplinkDevice) {
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

func (r *SongyaxuanPlantRunner) loadState() {
	if r.statePath == "" {
		return
	}
	raw, err := os.ReadFile(r.statePath)
	if err != nil {
		return
	}
	var st songyaxuanPlantPersistedState
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

func (r *SongyaxuanPlantRunner) saveState() {
	if r.statePath == "" {
		return
	}
	r.mu.Lock()
	st := songyaxuanPlantPersistedState{
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

func allMetersFresh(latest map[string]songyaxuanMeterSample, now time.Time) bool {
	for _, spec := range songyaxuanMeterSpecs {
		s, ok := latest[spec.deviceID]
		if !ok || now.Sub(s.ts) > meterFreshWindow {
			return false
		}
	}
	return true
}

func plantRoundTo(value, scale float64) float64 {
	return math.Round(value*scale) / scale
}
