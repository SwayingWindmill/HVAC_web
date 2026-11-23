package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	et1010DefaultUnitID      uint8 = 31
	et1010WinnerControllerID       = "songyaxuan-et1010-uid31"
	et1010PulseResetAttempts       = 3
	et1010PulseResetBackoff        = 50 * time.Millisecond
	et1010StopRecheckLimit         = 2
	et1010MaxExecutedCmdIDs        = 1000
	et1010StateFileName            = "et1010-executor-state.json"
	et1010DefaultPulseMS           = 500
	et1010DefaultStopPowerKW       = 5.0
)

// ET1010CoilBus abstracts Modbus RTU coil writes and readback over the shared serial bus.
type ET1010CoilBus interface {
	WriteCoil(ctx context.Context, unitID uint8, coilAddress uint16, value bool) error
	ReadCoil(ctx context.Context, unitID uint8, coilAddress uint16) (bool, error)
}

// ModbusET1010CoilBus adapts *edgecontrol.ModbusTCPBridge to ET1010CoilBus.
type ModbusET1010CoilBus struct {
	Bridge *edgecontrol.ModbusTCPBridge
}

// WriteCoil sends FC05 Write Single Coil (high bus priority) through ModbusTCPBridge.
func (b ModbusET1010CoilBus) WriteCoil(ctx context.Context, unitID uint8, coilAddress uint16, value bool) error {
	if b.Bridge == nil {
		return errors.New("et1010 modbus bridge is nil")
	}
	v := uint16(0)
	if value {
		v = 1
	}
	return b.Bridge.Write(ctx, edgecontrol.ModbusWriteTask{
		Name:         fmt.Sprintf("et1010-coil-%d", coilAddress),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionWriteSingleCoil,
		Address:      coilAddress,
		Values:       []uint16{v},
	})
}

// ReadCoil sends FC01 Read Coils (normal bus priority) through ModbusTCPBridge.
func (b ModbusET1010CoilBus) ReadCoil(ctx context.Context, unitID uint8, coilAddress uint16) (bool, error) {
	if b.Bridge == nil {
		return false, errors.New("et1010 modbus bridge is nil")
	}
	bits, err := b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name:         fmt.Sprintf("et1010-read-coil-%d", coilAddress),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadCoils,
		Address:      coilAddress,
		Quantity:     1,
	})
	if err != nil {
		return false, err
	}
	if len(bits) == 0 {
		return false, fmt.Errorf("et1010 coil %d read returned empty slice", coilAddress)
	}
	return bits[0] != 0, nil
}

// ET1010UnitCode identifies one of the four Songyaxuan chiller trains.
type ET1010UnitCode string

const (
	ET1010UnitA ET1010UnitCode = "unit-a"
	ET1010UnitB ET1010UnitCode = "unit-b"
	ET1010UnitC ET1010UnitCode = "unit-c"
	ET1010UnitD ET1010UnitCode = "unit-d"
)

type et1010AuxPart struct {
	deviceID string
	channel  string
	coil     uint16
}

type et1010PulsePart struct {
	channel string
	coil    uint16
}

type et1010UnitPlan struct {
	unitCode        ET1010UnitCode
	chillerDeviceID string
	chwp            et1010AuxPart
	cwp             et1010AuxPart
	tower           et1010AuxPart
	chillerStart    et1010PulsePart
	chillerStop     et1010PulsePart
}

type et1010DirectRoute struct {
	mode     string
	channel  string
	coil     uint16
	value    bool
	unitCode ET1010UnitCode
}

type et1010CoilEntry struct {
	channel string
	coil    uint16
}

type et1010PulseDescriptor struct {
	coil     uint16
	channel  string
	unitCode ET1010UnitCode
}

var (
	et1010UnitPlans = map[ET1010UnitCode]et1010UnitPlan{
		ET1010UnitA: {
			unitCode:        ET1010UnitA,
			chillerDeviceID: "ctrl-chiller-a",
			chwp:            et1010AuxPart{deviceID: "ctrl-chwp-1", channel: "u11", coil: 0},
			cwp:             et1010AuxPart{deviceID: "ctrl-cwp-1", channel: "u12", coil: 1},
			tower:           et1010AuxPart{deviceID: "ctrl-tower-1", channel: "u13", coil: 2},
			chillerStart:    et1010PulsePart{channel: "u14", coil: 3},
			chillerStop:     et1010PulsePart{channel: "u51", coil: 16},
		},
		ET1010UnitB: {
			unitCode:        ET1010UnitB,
			chillerDeviceID: "ctrl-chiller-b",
			chwp:            et1010AuxPart{deviceID: "ctrl-chwp-2", channel: "u21", coil: 4},
			cwp:             et1010AuxPart{deviceID: "ctrl-cwp-2", channel: "u22", coil: 5},
			tower:           et1010AuxPart{deviceID: "ctrl-tower-2", channel: "u23", coil: 6},
			chillerStart:    et1010PulsePart{channel: "u24", coil: 7},
			chillerStop:     et1010PulsePart{channel: "u52", coil: 17},
		},
		ET1010UnitC: {
			unitCode:        ET1010UnitC,
			chillerDeviceID: "ctrl-chiller-c",
			chwp:            et1010AuxPart{deviceID: "ctrl-chwp-3", channel: "u31", coil: 8},
			cwp:             et1010AuxPart{deviceID: "ctrl-cwp-3", channel: "u32", coil: 9},
			tower:           et1010AuxPart{deviceID: "ctrl-tower-3", channel: "u33", coil: 10},
			chillerStart:    et1010PulsePart{channel: "u34", coil: 11},
			chillerStop:     et1010PulsePart{channel: "u53", coil: 18},
		},
		ET1010UnitD: {
			unitCode:        ET1010UnitD,
			chillerDeviceID: "ctrl-chiller-d",
			chwp:            et1010AuxPart{deviceID: "ctrl-chwp-4", channel: "u41", coil: 12},
			cwp:             et1010AuxPart{deviceID: "ctrl-cwp-4", channel: "u42", coil: 13},
			tower:           et1010AuxPart{deviceID: "ctrl-tower-4", channel: "u43", coil: 14},
			chillerStart:    et1010PulsePart{channel: "u44", coil: 15},
			chillerStop:     et1010PulsePart{channel: "u54", coil: 19},
		},
	}

	et1010AllCoils = []et1010CoilEntry{
		{channel: "u11", coil: 0}, {channel: "u12", coil: 1}, {channel: "u13", coil: 2}, {channel: "u14", coil: 3},
		{channel: "u21", coil: 4}, {channel: "u22", coil: 5}, {channel: "u23", coil: 6}, {channel: "u24", coil: 7},
		{channel: "u31", coil: 8}, {channel: "u32", coil: 9}, {channel: "u33", coil: 10}, {channel: "u34", coil: 11},
		{channel: "u41", coil: 12}, {channel: "u42", coil: 13}, {channel: "u43", coil: 14}, {channel: "u44", coil: 15},
		{channel: "u51", coil: 16}, {channel: "u52", coil: 17}, {channel: "u53", coil: 18}, {channel: "u54", coil: 19},
	}

	et1010PulseDescriptors = []et1010PulseDescriptor{
		{coil: 3, channel: "u14", unitCode: ET1010UnitA},
		{coil: 7, channel: "u24", unitCode: ET1010UnitB},
		{coil: 11, channel: "u34", unitCode: ET1010UnitC},
		{coil: 15, channel: "u44", unitCode: ET1010UnitD},
		{coil: 16, channel: "u51", unitCode: ET1010UnitA},
		{coil: 17, channel: "u52", unitCode: ET1010UnitB},
		{coil: 18, channel: "u53", unitCode: ET1010UnitC},
		{coil: 19, channel: "u54", unitCode: ET1010UnitD},
	}

	et1010PulseCoilSet     map[uint16]struct{}
	et1010ChillerUnitByDev map[string]ET1010UnitCode
	et1010DirectRoutes     map[string]et1010DirectRoute
	et1010AllAuxParts      []et1010AuxPart
	et1010ManagedDevices   map[string]struct{}
)

func init() {
	et1010PulseCoilSet = make(map[uint16]struct{}, len(et1010PulseDescriptors))
	for _, d := range et1010PulseDescriptors {
		et1010PulseCoilSet[d.coil] = struct{}{}
	}
	et1010ChillerUnitByDev = make(map[string]ET1010UnitCode, len(et1010UnitPlans))
	et1010DirectRoutes = make(map[string]et1010DirectRoute, 32)
	et1010ManagedDevices = map[string]struct{}{"et1010": {}}
	for _, u := range []ET1010UnitCode{ET1010UnitA, ET1010UnitB, ET1010UnitC, ET1010UnitD} {
		plan := et1010UnitPlans[u]
		et1010ChillerUnitByDev[plan.chillerDeviceID] = plan.unitCode
		et1010ManagedDevices[plan.chillerDeviceID] = struct{}{}
		for _, part := range []et1010AuxPart{plan.chwp, plan.cwp, plan.tower} {
			et1010AllAuxParts = append(et1010AllAuxParts, part)
			et1010ManagedDevices[part.deviceID] = struct{}{}
			et1010DirectRoutes[part.deviceID+"|START"] = et1010DirectRoute{
				mode: "LEVEL", channel: part.channel, coil: part.coil, value: true, unitCode: plan.unitCode,
			}
			et1010DirectRoutes[part.deviceID+"|STOP"] = et1010DirectRoute{
				mode: "LEVEL", channel: part.channel, coil: part.coil, value: false, unitCode: plan.unitCode,
			}
		}
		et1010DirectRoutes[plan.chillerDeviceID+"|START"] = et1010DirectRoute{
			mode: "PULSE", channel: plan.chillerStart.channel, coil: plan.chillerStart.coil, value: true, unitCode: plan.unitCode,
		}
		et1010DirectRoutes[plan.chillerDeviceID+"|STOP"] = et1010DirectRoute{
			mode: "PULSE", channel: plan.chillerStop.channel, coil: plan.chillerStop.coil, value: true, unitCode: plan.unitCode,
		}
	}
}

// IsSongyaxuanET1010Command reports whether env targets Songyaxuan ET1010 DO control.
func IsSongyaxuanET1010Command(env CommandEnvelopeWire) bool {
	if _, ok := et1010ManagedDevices[strings.TrimSpace(env.DeviceID)]; ok {
		return true
	}
	switch strings.TrimSpace(env.ActuationMode) {
	case "LEVEL", "PULSE", "UNIT_SEQUENCE", "ABORT_UNIT_SEQUENCE", "EMERGENCY_DO_RELEASE":
		return true
	}
	switch strings.TrimSpace(env.CommandCode) {
	case "START_UNIT_SEQUENCE", "STOP_UNIT_SEQUENCE", "ABORT_UNIT_SEQUENCE":
		return true
	}
	return false
}

// ET1010PersistedState is the durable local JSON state for idempotency, fences, and stuck pulse recovery.
type ET1010PersistedState struct {
	LastSequence       uint64            `json:"lastSequence"`
	LastFenceByDevice  map[string]uint64 `json:"lastFenceByDevice"`
	ExecutedCommandIDs []string          `json:"executedCommandIds"`
	PendingPulseCoils  []uint16          `json:"pendingPulseCoils"`
}

// ET1010ExecutorOptions configures the Songyaxuan ET1010 command and sequence executor.
type ET1010ExecutorOptions struct {
	GatewayID          string
	UnitID             uint8
	StateDir           string
	CoilBus            ET1010CoilBus
	ChillerPowerReader func(ctx context.Context, unit ET1010UnitCode) (*float64, error)
	ChillerStopPowerKW float64
	PulseDurationMS    int
	SequenceTimings    ET1010SequenceTimings
	PublishTelemetry   func(ctx context.Context, payload []byte)
	PublishEvent       func(ctx context.Context, payload []byte)
	Now                func() time.Time
	Sleep              func(ctx context.Context, d time.Duration) error
	UUIDv7             func(now time.Time) string
	Logger             *slog.Logger
	SequenceContext    context.Context
}

type et1010DiagnosticData struct {
	Code                   string   `json:"code"`
	UnitCode               string   `json:"unitCode"`
	CommandID              string   `json:"commandId"`
	Message                string   `json:"message"`
	AuxiliariesHeld        bool     `json:"auxiliariesHeld,omitempty"`
	FailureCode            string   `json:"failureCode,omitempty"`
	FailureReason          string   `json:"failureReason,omitempty"`
	RolledBackChannels     []string `json:"rolledBackChannels,omitempty"`
	RollbackFailedChannels []string `json:"rollbackFailedChannels,omitempty"`
	OpenChannels           []string `json:"openChannels,omitempty"`
	PowerKW                *float64 `json:"powerKw,omitempty"`
	StopThresholdKW        *float64 `json:"stopThresholdKw,omitempty"`
}

type et1010SequenceAbortErr struct {
	reason string
}

func (e et1010SequenceAbortErr) Error() string { return e.reason }

type et1010PulseResetErr struct{ cause error }

func (e et1010PulseResetErr) Error() string { return e.cause.Error() }

type et1010StatePersistErr struct{ cause error }

func (e et1010StatePersistErr) Error() string { return e.cause.Error() }

type et1010SequenceLifecycle struct {
	cancel context.CancelCauseFunc
	ctx    context.Context
	done   chan struct{}
}

// ET1010Executor executes Songyaxuan UID 31 DO coil commands, pulses, chiller start/stop
// sequences with reverse rollback and fail-closed chiller power verification, and E-stop release.
type ET1010Executor struct {
	gatewayID          string
	unitID             uint8
	statePath          string
	coilBus            ET1010CoilBus
	chillerPowerReader func(ctx context.Context, unit ET1010UnitCode) (*float64, error)
	chillerStopPowerKW float64
	pulseDurationMS    int
	timings            ET1010SequenceTimings
	publishTelemetry   func(ctx context.Context, payload []byte)
	publishEvent       func(ctx context.Context, payload []byte)
	now                func() time.Time
	sleep              func(ctx context.Context, d time.Duration) error
	uuidv7             func(now time.Time) string
	logger             *slog.Logger
	bgCtx              context.Context
	bgCancel           context.CancelFunc

	initOnce sync.Once
	initErr  error

	admissionMu sync.Mutex
	stateMu     sync.Mutex
	coilMu      sync.Mutex

	lastFenceByDevice map[string]uint64
	executedIDOrder   []string
	executedIDSet     map[string]struct{}
	pendingPulseCoils map[uint16]struct{}
	knownCoilStates   map[uint16]bool
	activeSequences   map[ET1010UnitCode]*et1010SequenceLifecycle
	allTasks          map[*et1010SequenceLifecycle]struct{}
	cycle             uint64
	uplinkSeq         uint64
}

// NewET1010Executor constructs an ET1010Executor with fail-safe production defaults.
func NewET1010Executor(opts ET1010ExecutorOptions) (*ET1010Executor, error) {
	gwID := strings.TrimSpace(opts.GatewayID)
	if gwID == "" {
		return nil, errors.New("et1010 executor requires gatewayId")
	}
	if opts.CoilBus == nil {
		return nil, errors.New("et1010 executor requires coilBus")
	}
	unitID := opts.UnitID
	if unitID == 0 {
		unitID = et1010DefaultUnitID
	}
	pulseMS := opts.PulseDurationMS
	if pulseMS <= 0 {
		pulseMS = et1010DefaultPulseMS
	}
	stopKW := opts.ChillerStopPowerKW
	if math.IsNaN(stopKW) || math.IsInf(stopKW, 0) || stopKW <= 0 {
		stopKW = et1010DefaultStopPowerKW
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = defaultET1010Sleep
	}
	uuidFn := opts.UUIDv7
	if uuidFn == nil {
		uuidFn = func(t time.Time) string {
			id, err := edgecontrol.NewReplicationUUIDv7(t)
			if err != nil {
				return edgecontrol.DeterministicReplicationUUIDv7(t, gwID, "et1010")
			}
			return id
		}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	parentCtx := opts.SequenceContext
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	bgCtx, bgCancel := context.WithCancel(parentCtx)
	var statePath string
	if trimmed := strings.TrimSpace(opts.StateDir); trimmed != "" {
		statePath = filepath.Join(trimmed, et1010StateFileName)
	}
	return &ET1010Executor{
		gatewayID:          gwID,
		unitID:             unitID,
		statePath:          statePath,
		coilBus:            opts.CoilBus,
		chillerPowerReader: opts.ChillerPowerReader,
		chillerStopPowerKW: stopKW,
		pulseDurationMS:    pulseMS,
		timings:            opts.SequenceTimings,
		publishTelemetry:   opts.PublishTelemetry,
		publishEvent:       opts.PublishEvent,
		now:                nowFn,
		sleep:              sleepFn,
		uuidv7:             uuidFn,
		logger:             logger,
		bgCtx:              bgCtx,
		bgCancel:           bgCancel,
		lastFenceByDevice:  make(map[string]uint64),
		executedIDSet:      make(map[string]struct{}),
		pendingPulseCoils:  make(map[uint16]struct{}),
		knownCoilStates:    make(map[uint16]bool),
		activeSequences:    make(map[ET1010UnitCode]*et1010SequenceLifecycle),
		allTasks:           make(map[*et1010SequenceLifecycle]struct{}),
	}, nil
}

func defaultET1010Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Initialize loads persisted executor state and clears any latched pulse coils to 0 on startup.
func (e *ET1010Executor) Initialize(ctx context.Context) error {
	e.initOnce.Do(func() {
		e.initErr = e.runInitialize(ctx)
	})
	return e.initErr
}

// Close aborts running background sequences and waits briefly for cleanup.
func (e *ET1010Executor) Close() {
	if e == nil {
		return
	}
	e.bgCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = e.WaitForActiveSequences(ctx)
}

// WaitForActiveSequences blocks until all accepted sequence goroutines complete or ctx expires.
func (e *ET1010Executor) WaitForActiveSequences(ctx context.Context) error {
	for {
		e.stateMu.Lock()
		var first *et1010SequenceLifecycle
		for lc := range e.allTasks {
			first = lc
			break
		}
		e.stateMu.Unlock()
		if first == nil {
			return nil
		}
		select {
		case <-first.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (e *ET1010Executor) runInitialize(ctx context.Context) error {
	loaded, err := e.loadPersistedState()
	if err != nil {
		return err
	}
	if loaded != nil {
		e.stateMu.Lock()
		if loaded.LastSequence > e.uplinkSeq {
			e.uplinkSeq = loaded.LastSequence
		}
		for devID, fence := range loaded.LastFenceByDevice {
			if strings.TrimSpace(devID) != "" && fence >= 1 && fence > e.lastFenceByDevice[devID] {
				e.lastFenceByDevice[devID] = fence
			}
		}
		startIdx := 0
		if len(loaded.ExecutedCommandIDs) > et1010MaxExecutedCmdIDs {
			startIdx = len(loaded.ExecutedCommandIDs) - et1010MaxExecutedCmdIDs
		}
		for _, id := range loaded.ExecutedCommandIDs[startIdx:] {
			if strings.TrimSpace(id) != "" {
				e.recordExecutedLocked(id)
			}
		}
		for _, coil := range loaded.PendingPulseCoils {
			if _, ok := et1010PulseCoilSet[coil]; ok {
				e.pendingPulseCoils[coil] = struct{}{}
			}
		}
		e.stateMu.Unlock()
	}

	e.stateMu.Lock()
	recoveredPending := maps.Clone(e.pendingPulseCoils)
	e.stateMu.Unlock()

	var resetPoints []mqttUplinkPoint
	var cleared []string
	for _, desc := range et1010PulseDescriptors {
		if err := e.resetPulseCoilWithRetry(ctx, desc.coil); err != nil {
			e.stateMu.Lock()
			e.pendingPulseCoils[desc.coil] = struct{}{}
			e.stateMu.Unlock()
			e.logger.Error("et1010_startup_pulse_reset_failed",
				"code", "PULSE_RESET_FAILED",
				"unit", desc.unitCode,
				"channel", desc.channel,
				"coil", desc.coil,
				"error", err.Error(),
			)
			continue
		}
		e.stateMu.Lock()
		delete(e.pendingPulseCoils, desc.coil)
		e.stateMu.Unlock()
		resetPoints = append(resetPoints, mqttUplinkPoint{Code: desc.channel, Value: 0, Quality: 0})
		if _, wasPending := recoveredPending[desc.coil]; wasPending {
			cleared = append(cleared, fmt.Sprintf("%s(coil=%d)", desc.channel, desc.coil))
		}
	}
	if len(cleared) > 0 {
		e.logger.Warn("et1010_startup_cleared_pending_pulses", "coils", strings.Join(cleared, ", "))
	}
	if err := e.persistCurrentState(); err != nil {
		return err
	}
	if len(resetPoints) > 0 {
		e.emitTelemetryEntries(ctx, []mqttUplinkDevice{{
			DeviceID:        "et1010",
			DeviceTimestamp: e.nowMS(),
			Points:          resetPoints,
		}})
	}
	return nil
}

// HandlePayload decodes a v2.0 downlink CommandEnvelopeWire JSON payload and executes it.
func (e *ET1010Executor) HandlePayload(ctx context.Context, payload []byte) ([]byte, bool) {
	if e == nil {
		return nil, false
	}
	env, ok := decodeStrictCommandEnvelope(payload)
	if !ok {
		e.logger.Warn("et1010_command_envelope_invalid")
		return nil, false
	}
	reply := e.HandleCommand(ctx, env)
	wire, err := json.Marshal(reply)
	if err != nil {
		e.logger.Warn("et1010_command_reply_marshal_failed", "command_id", reply.CommandID, "error", err.Error())
		return nil, false
	}
	return wire, true
}

// HandleCommand enforces envelope, idempotency, fence, TTL, routing, and executes DO control.
func (e *ET1010Executor) HandleCommand(ctx context.Context, env CommandEnvelopeWire) CommandReplyWire {
	if env.SchemaVersion != commandSchemaVersion ||
		env.CommandID == "" ||
		env.DeviceID == "" ||
		env.CommandCode == "" ||
		env.ExecutionFence < 1 ||
		env.IssuedAt <= 0 ||
		env.ExpireAt <= env.IssuedAt {
		return e.buildReply(env, "REJECTED", "INVALID_COMMAND_ENVELOPE", "INVALID_ENVELOPE", "NOT_APPLIED")
	}

	if err := e.Initialize(ctx); err != nil {
		return e.buildReply(env, "FAILED", "STATE_PERSIST_FAILED", env.CommandCode, "NOT_APPLIED")
	}

	type admissionResult struct {
		immediateReply *CommandReplyWire
		isEmergency    bool
		unitPlan       *et1010UnitPlan
		lifecycle      *et1010SequenceLifecycle
		prevLifecycle  *et1010SequenceLifecycle
		directRoute    *et1010DirectRoute
	}

	admitted := func() admissionResult {
		e.admissionMu.Lock()
		defer e.admissionMu.Unlock()

		e.stateMu.Lock()
		_, dup := e.executedIDSet[env.CommandID]
		prevFence := e.lastFenceByDevice[env.DeviceID]
		e.stateMu.Unlock()

		if dup {
			rep := e.buildReply(env, "REJECTED", "DUPLICATE_COMMAND", env.CommandCode, "NOT_APPLIED")
			return admissionResult{immediateReply: &rep}
		}
		if env.ExecutionFence <= prevFence {
			rep := e.buildReply(env, "REJECTED", "STALE_EXECUTION_FENCE", env.CommandCode, "NOT_APPLIED")
			return admissionResult{immediateReply: &rep}
		}
		nowMs := e.nowMS()
		if nowMs >= env.ExpireAt {
			rep := e.buildReply(env, "EXPIRED", "COMMAND_EXPIRED", env.CommandCode, "NOT_APPLIED")
			return admissionResult{immediateReply: &rep}
		}
		if env.ExpireAt-env.IssuedAt > commandMaxTTL.Milliseconds() || env.ExpireAt-nowMs > commandMaxTTL.Milliseconds() {
			rep := e.buildReply(env, "EXPIRED", "TTL_EXCEEDS_MAX", env.CommandCode, "NOT_APPLIED")
			return admissionResult{immediateReply: &rep}
		}
		if _, protected := edgecontrol.ClassifyHardRealtimeProtection(env.PointCode, env.CommandCode, nil); protected {
			rep := e.buildReply(env, "REJECTED", edgecontrol.ReasonHardRealtimeSafetyBoundary, env.CommandCode, "NOT_APPLIED")
			return admissionResult{immediateReply: &rep}
		}

		isEmergency := isEmergencyDORelease(env)
		isAbort := !isEmergency && isAbortUnitSequence(env)
		isUnitSeq := !isEmergency && !isAbort && (env.CommandCode == "START_UNIT_SEQUENCE" || env.CommandCode == "STOP_UNIT_SEQUENCE")

		var unitPlan *et1010UnitPlan
		var directRoute *et1010DirectRoute

		if isAbort || isUnitSeq {
			unitCode, ok := resolveET1010UnitCode(env, isAbort)
			if !ok {
				rep := e.buildReply(env, "REJECTED", "UNKNOWN_ACTUATION_ROUTE", env.CommandCode, "NOT_APPLIED")
				return admissionResult{immediateReply: &rep}
			}
			plan := et1010UnitPlans[unitCode]
			unitPlan = &plan
		} else if !isEmergency {
			route, ok := et1010DirectRoutes[env.DeviceID+"|"+env.CommandCode]
			if !ok || !matchesDirectRoute(env, route) {
				rep := e.buildReply(env, "REJECTED", "UNKNOWN_ACTUATION_ROUTE", env.CommandCode, "NOT_APPLIED")
				return admissionResult{immediateReply: &rep}
			}
			directRoute = &route
		}

		e.stateMu.Lock()
		snap := e.snapshotWithAcceptedLocked(env)
		e.stateMu.Unlock()
		if err := e.writePersistedState(snap); err != nil {
			e.logger.Error("et1010_state_persist_failed", "command_id", env.CommandID, "error", err.Error())
			rep := e.buildReply(env, "FAILED", "STATE_PERSIST_FAILED", env.CommandCode, "NOT_APPLIED")
			return admissionResult{immediateReply: &rep}
		}

		e.stateMu.Lock()
		e.commitCommandAcceptedLocked(env)
		e.stateMu.Unlock()

		if isEmergency {
			e.stateMu.Lock()
			for unitCode := range e.activeSequences {
				e.abortUnitSequenceLocked(unitCode, "EMERGENCY_DO_RELEASE")
			}
			e.stateMu.Unlock()
			return admissionResult{isEmergency: true}
		}

		if isAbort && unitPlan != nil {
			e.stateMu.Lock()
			e.abortUnitSequenceLocked(unitPlan.unitCode, "SEQUENCE_ABORTED_BY_OPERATOR")
			e.stateMu.Unlock()
			rep := e.buildReply(env, "EXECUTED", "", env.CommandCode, string(unitPlan.unitCode)+":sequence-aborted")
			return admissionResult{immediateReply: &rep}
		}

		if isUnitSeq && unitPlan != nil {
			e.stateMu.Lock()
			e.abortUnitSequenceLocked(unitPlan.unitCode, "SEQUENCE_SUPERSEDED")
			prev := e.activeSequences[unitPlan.unitCode]
			seqCtx, cancel := context.WithCancelCause(e.bgCtx)
			lc := &et1010SequenceLifecycle{
				ctx:    seqCtx,
				cancel: cancel,
				done:   make(chan struct{}),
			}
			e.activeSequences[unitPlan.unitCode] = lc
			e.allTasks[lc] = struct{}{}
			e.stateMu.Unlock()
			return admissionResult{
				unitPlan:      unitPlan,
				lifecycle:     lc,
				prevLifecycle: prev,
			}
		}

		if env.CommandCode == "STOP" && directRoute != nil {
			e.stateMu.Lock()
			e.abortUnitSequenceLocked(directRoute.unitCode, "SEQUENCE_ABORTED_BY_OPERATOR")
			e.stateMu.Unlock()
		}
		directCtx, cancel := context.WithCancelCause(ctx)
		lc := &et1010SequenceLifecycle{
			ctx:    directCtx,
			cancel: cancel,
			done:   make(chan struct{}),
		}
		e.stateMu.Lock()
		e.allTasks[lc] = struct{}{}
		e.stateMu.Unlock()
		return admissionResult{
			directRoute: directRoute,
			lifecycle:   lc,
		}
	}()

	if admitted.immediateReply != nil {
		return *admitted.immediateReply
	}
	if admitted.isEmergency {
		return e.executeEmergencyDORelease(ctx, env)
	}
	if admitted.unitPlan != nil {
		return e.executeUnitSequence(ctx, env, *admitted.unitPlan, admitted.lifecycle, admitted.prevLifecycle)
	}

	route := *admitted.directRoute
	defer func() {
		admitted.lifecycle.cancel(nil)
		e.releaseDirectTask(admitted.lifecycle)
	}()

	if route.mode == "LEVEL" {
		valInt := 0
		runState := "STOPPED"
		if route.value {
			valInt = 1
			runState = "RUNNING"
		}
		if err := e.writeCoil(ctx, route.coil, route.value); err != nil {
			return e.buildReply(env, "FAILED", "MODBUS_WRITE_FAILED", fmt.Sprintf("%s=%d", route.channel, valInt), "NOT_APPLIED")
		}
		ts := e.nowMS()
		e.emitTelemetryEntries(ctx, []mqttUplinkDevice{
			{DeviceID: "et1010", DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: route.channel, Value: valInt, Quality: 0}}},
			{DeviceID: env.DeviceID, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: "run_state", Value: runState, Quality: 0}}},
		})
		txt := fmt.Sprintf("%s=%d", route.channel, valInt)
		return e.buildReply(env, "EXECUTED", "", txt, txt)
	}

	pulseMS := e.pulseDurationMS
	if env.PulseDurationMS > 0 {
		pulseMS = env.PulseDurationMS
	}
	reqText := fmt.Sprintf("%s:pulse(%dms)", route.channel, pulseMS)
	if err := e.executePulse(ctx, route.coil, route.channel, time.Duration(pulseMS)*time.Millisecond, route.unitCode, env.CommandID); err != nil {
		reason := "MODBUS_WRITE_FAILED"
		var prErr et1010PulseResetErr
		var spErr et1010StatePersistErr
		if errors.As(err, &prErr) {
			reason = "PULSE_RESET_FAILED"
		} else if errors.As(err, &spErr) {
			reason = "STATE_PERSIST_FAILED"
		}
		return e.buildReply(env, "FAILED", reason, reqText, "NOT_APPLIED")
	}
	return e.buildReply(env, "EXECUTED", "", reqText, reqText+"->0")
}

func (e *ET1010Executor) releaseSequenceLifecycle(unitCode ET1010UnitCode, lc *et1010SequenceLifecycle) {
	e.stateMu.Lock()
	if e.activeSequences[unitCode] == lc {
		delete(e.activeSequences, unitCode)
	}
	delete(e.allTasks, lc)
	e.stateMu.Unlock()
	close(lc.done)
}

func (e *ET1010Executor) releaseDirectTask(lc *et1010SequenceLifecycle) {
	e.stateMu.Lock()
	delete(e.allTasks, lc)
	e.stateMu.Unlock()
	close(lc.done)
}

func (e *ET1010Executor) executeUnitSequence(
	ctx context.Context,
	env CommandEnvelopeWire,
	plan et1010UnitPlan,
	lc *et1010SequenceLifecycle,
	prevLC *et1010SequenceLifecycle,
) CommandReplyWire {
	pulseMS := e.pulseDurationMS
	if env.PulseDurationMS > 0 {
		pulseMS = env.PulseDurationMS
	}
	isStart := env.CommandCode == "START_UNIT_SEQUENCE"
	var startedAux []et1010AuxPart
	tailLaunched := false
	defer func() {
		if !tailLaunched {
			lc.cancel(nil)
			e.releaseSequenceLifecycle(plan.unitCode, lc)
		}
	}()

	checkAborted := func() error {
		if err := lc.ctx.Err(); err != nil {
			reason := "SEQUENCE_ABORTED_BY_OPERATOR"
			if cause := context.Cause(lc.ctx); cause != nil && strings.TrimSpace(cause.Error()) != "" {
				var sa et1010SequenceAbortErr
				if errors.As(cause, &sa) {
					reason = sa.reason
				}
			}
			return et1010SequenceAbortErr{reason: reason}
		}
		return nil
	}

	writeStopStep := func(opCtx context.Context, part et1010AuxPart) error {
		if err := checkAborted(); err != nil {
			return err
		}
		if err := e.writeCoil(opCtx, part.coil, false); err != nil {
			return err
		}
		ts := e.nowMS()
		e.emitTelemetryEntries(opCtx, []mqttUplinkDevice{
			{DeviceID: "et1010", DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: part.channel, Value: 0, Quality: 0}}},
			{DeviceID: part.deviceID, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: "run_state", Value: "STOPPED", Quality: 0}}},
		})
		return nil
	}

	waitStep := func(opCtx context.Context, d time.Duration) error {
		if err := checkAborted(); err != nil {
			return err
		}
		if err := e.sleep(opCtx, d); err != nil {
			if abortErr := checkAborted(); abortErr != nil {
				return abortErr
			}
			return err
		}
		return checkAborted()
	}

	if prevLC != nil {
		select {
		case <-prevLC.done:
		case <-lc.ctx.Done():
		}
	}
	if err := checkAborted(); err != nil {
		var sa et1010SequenceAbortErr
		if errors.As(err, &sa) {
			return e.buildReply(env, "REJECTED", sa.reason, env.CommandCode, "ABORTED")
		}
	}

	if isStart {
		if err := e.writeStartAuxStep(lc.ctx, plan.chwp, &startedAux, checkAborted); err != nil {
			return e.handleForegroundSeqError(ctx, env, plan, startedAux, err)
		}
		if err := checkAborted(); err != nil {
			return e.handleForegroundSeqError(ctx, env, plan, startedAux, err)
		}
		tailLaunched = true
		startedSnapshot := slices.Clone(startedAux)
		go func() {
			defer lc.cancel(nil)
			defer e.releaseSequenceLifecycle(plan.unitCode, lc)
			localStarted := startedSnapshot
			runErr := func() error {
				if err := waitStep(lc.ctx, time.Duration(e.timings.PumpToCoolingPumpMS)*time.Millisecond); err != nil {
					return err
				}
				if err := e.writeStartAuxStep(lc.ctx, plan.cwp, &localStarted, checkAborted); err != nil {
					return err
				}
				if err := waitStep(lc.ctx, time.Duration(e.timings.CoolingPumpToTowerMS)*time.Millisecond); err != nil {
					return err
				}
				if err := e.writeStartAuxStep(lc.ctx, plan.tower, &localStarted, checkAborted); err != nil {
					return err
				}
				if err := waitStep(lc.ctx, time.Duration(e.timings.TowerToChillerPulseMS)*time.Millisecond); err != nil {
					return err
				}
				allClosed, err := e.verifyRequiredCoilsClosed(lc.ctx, plan, env, checkAborted)
				if err != nil {
					return err
				}
				if !allClosed {
					e.rollbackStartedAuxiliaries(plan, env, localStarted, "PRE_PULSE_COIL_OPEN", "Required auxiliary coils were not closed before chiller start pulse", lc.ctx)
					return nil
				}
				if err := checkAborted(); err != nil {
					return err
				}
				return e.executePulse(lc.ctx, plan.chillerStart.coil, plan.chillerStart.channel, time.Duration(pulseMS)*time.Millisecond, plan.unitCode, env.CommandID)
			}()
			if runErr == nil {
				return
			}
			var sa et1010SequenceAbortErr
			if errors.As(runErr, &sa) {
				return
			}
			var prErr et1010PulseResetErr
			if errors.As(runErr, &prErr) {
				e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "PULSE_RESET_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
					et1010DiagnosticData{
						Code:            "PULSE_RESET_FAILED",
						UnitCode:        string(plan.unitCode),
						CommandID:       env.CommandID,
						Message:         fmt.Sprintf("%s: chiller start pulse reset failed; holding started auxiliaries ON: %v", plan.unitCode, runErr),
						AuxiliariesHeld: true,
						FailureCode:     "PULSE_RESET_FAILED",
					})
				return
			}
			failureCode := "SEQUENCE_STEP_FAILED"
			var spErr et1010StatePersistErr
			if errors.As(runErr, &spErr) {
				failureCode = "STATE_PERSIST_FAILED"
			}
			e.rollbackStartedAuxiliaries(plan, env, localStarted, failureCode, runErr.Error(), lc.ctx)
		}()
		return e.buildReply(env, "EXECUTED", "", env.CommandCode, string(plan.unitCode)+":sequence-accepted")
	}

	// STOP_UNIT_SEQUENCE
	if err := checkAborted(); err != nil {
		return e.handleForegroundSeqError(ctx, env, plan, nil, err)
	}
	if err := e.executePulse(lc.ctx, plan.chillerStop.coil, plan.chillerStop.channel, time.Duration(pulseMS)*time.Millisecond, plan.unitCode, env.CommandID); err != nil {
		return e.handleForegroundSeqError(ctx, env, plan, nil, err)
	}
	if err := checkAborted(); err != nil {
		return e.handleForegroundSeqError(ctx, env, plan, nil, err)
	}
	tailLaunched = true
	go func() {
		defer lc.cancel(nil)
		defer e.releaseSequenceLifecycle(plan.unitCode, lc)
		runErr := func() error {
			if err := waitStep(lc.ctx, time.Duration(e.timings.ChillerStopCoastDownMS)*time.Millisecond); err != nil {
				return err
			}
			confirmed, err := e.confirmChillerStoppedBeforeAuxShutdown(lc.ctx, plan, env, checkAborted)
			if err != nil {
				return err
			}
			if !confirmed {
				return nil
			}
			if err := writeStopStep(lc.ctx, plan.tower); err != nil {
				return err
			}
			if err := waitStep(lc.ctx, time.Duration(e.timings.AuxiliaryStopStepMS)*time.Millisecond); err != nil {
				return err
			}
			if err := writeStopStep(lc.ctx, plan.cwp); err != nil {
				return err
			}
			if err := waitStep(lc.ctx, time.Duration(e.timings.AuxiliaryStopStepMS)*time.Millisecond); err != nil {
				return err
			}
			return writeStopStep(lc.ctx, plan.chwp)
		}()
		if runErr == nil {
			return
		}
		var sa et1010SequenceAbortErr
		if errors.As(runErr, &sa) {
			return
		}
		var prErr et1010PulseResetErr
		if errors.As(runErr, &prErr) {
			e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "PULSE_RESET_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
				et1010DiagnosticData{
					Code:      "PULSE_RESET_FAILED",
					UnitCode:  string(plan.unitCode),
					CommandID: env.CommandID,
					Message:   fmt.Sprintf("%s: background pulse coil reset failed: %v", plan.unitCode, runErr),
				})
			return
		}
		e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "SEQUENCE_STEP_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
			et1010DiagnosticData{
				Code:      "SEQUENCE_STEP_FAILED",
				UnitCode:  string(plan.unitCode),
				CommandID: env.CommandID,
				Message:   fmt.Sprintf("%s: background sequence failed: %v", plan.unitCode, runErr),
			})
	}()
	return e.buildReply(env, "EXECUTED", "", env.CommandCode, string(plan.unitCode)+":sequence-accepted")
}

func (e *ET1010Executor) writeStartAuxStep(ctx context.Context, part et1010AuxPart, started *[]et1010AuxPart, checkAborted func() error) error {
	if err := checkAborted(); err != nil {
		return err
	}
	if err := e.writeCoil(ctx, part.coil, true); err != nil {
		return err
	}
	*started = append(*started, part)
	ts := e.nowMS()
	e.emitTelemetryEntries(ctx, []mqttUplinkDevice{
		{DeviceID: "et1010", DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: part.channel, Value: 1, Quality: 0}}},
		{DeviceID: part.deviceID, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: "run_state", Value: "RUNNING", Quality: 0}}},
	})
	return nil
}

func (e *ET1010Executor) handleForegroundSeqError(ctx context.Context, env CommandEnvelopeWire, plan et1010UnitPlan, startedAux []et1010AuxPart, err error) CommandReplyWire {
	var sa et1010SequenceAbortErr
	if errors.As(err, &sa) {
		return e.buildReply(env, "REJECTED", sa.reason, env.CommandCode, "ABORTED")
	}
	if len(startedAux) > 0 {
		code := "SEQUENCE_STEP_FAILED"
		var spErr et1010StatePersistErr
		if errors.As(err, &spErr) {
			code = "STATE_PERSIST_FAILED"
		}
		e.rollbackStartedAuxiliaries(plan, env, startedAux, code, err.Error(), ctx)
	}
	var prErr et1010PulseResetErr
	if errors.As(err, &prErr) {
		e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "PULSE_RESET_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
			et1010DiagnosticData{
				Code:      "PULSE_RESET_FAILED",
				UnitCode:  string(plan.unitCode),
				CommandID: env.CommandID,
				Message:   fmt.Sprintf("%s: pulse coil reset failed on %s", plan.unitCode, env.CommandCode),
			})
		return e.buildReply(env, "FAILED", "PULSE_RESET_FAILED", env.CommandCode, "NOT_APPLIED")
	}
	var spErr et1010StatePersistErr
	if errors.As(err, &spErr) {
		return e.buildReply(env, "FAILED", "STATE_PERSIST_FAILED", env.CommandCode, "NOT_APPLIED")
	}
	return e.buildReply(env, "FAILED", "MODBUS_WRITE_FAILED", env.CommandCode, "NOT_APPLIED")
}

func (e *ET1010Executor) rollbackStartedAuxiliaries(
	plan et1010UnitPlan,
	env CommandEnvelopeWire,
	startedAux []et1010AuxPart,
	failureCode string,
	failureReason string,
	seqCtx context.Context,
) {
	var rolledBack []et1010AuxPart
	var rollbackFailed []string

	resetCtx, cancel := context.WithTimeout(e.bgCtx, 5*time.Second)
	defer cancel()

	for i := len(startedAux) - 1; i >= 0; i-- {
		if seqCtx.Err() != nil {
			break
		}
		part := startedAux[i]
		if err := e.writeCoil(resetCtx, part.coil, false); err != nil {
			rollbackFailed = append(rollbackFailed, fmt.Sprintf("%s(coil=%d): %v", part.channel, part.coil, err))
			continue
		}
		rolledBack = append(rolledBack, part)
	}

	if len(rolledBack) > 0 {
		ts := e.nowMS()
		entries := make([]mqttUplinkDevice, 0, len(rolledBack)+1)
		etPts := make([]mqttUplinkPoint, 0, len(rolledBack))
		for _, part := range rolledBack {
			entries = append(entries, mqttUplinkDevice{
				DeviceID:        part.deviceID,
				DeviceTimestamp: ts,
				Points:          []mqttUplinkPoint{{Code: "run_state", Value: "STOPPED", Quality: 0}},
			})
			etPts = append(etPts, mqttUplinkPoint{Code: part.channel, Value: 0, Quality: 0})
		}
		entries = append(entries, mqttUplinkDevice{DeviceID: "et1010", DeviceTimestamp: ts, Points: etPts})
		e.emitTelemetryEntries(resetCtx, entries)
	}

	rolledChannels := make([]string, len(rolledBack))
	for i, p := range rolledBack {
		rolledChannels[i] = p.channel
	}
	eventCode := "START_SEQUENCE_FAILED_ROLLED_BACK"
	summary := "none"
	if len(rolledChannels) > 0 {
		summary = strings.Join(rolledChannels, ", ")
	}
	msg := fmt.Sprintf("%s: START_UNIT_SEQUENCE failed (%s: %s); rolled back started auxiliaries [%s] in reverse order", plan.unitCode, failureCode, failureReason, summary)
	if len(rollbackFailed) > 0 {
		eventCode = "START_SEQUENCE_ROLLBACK_FAILED"
		msg = fmt.Sprintf("%s: START_UNIT_SEQUENCE failed (%s: %s); rolled back [%s], failed to roll back [%s]", plan.unitCode, failureCode, failureReason, summary, strings.Join(rollbackFailed, "; "))
	}
	e.publishDiagnosticEvent(resetCtx, "CRITICAL", eventCode, plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
		et1010DiagnosticData{
			Code:                   eventCode,
			UnitCode:               string(plan.unitCode),
			CommandID:              env.CommandID,
			Message:                msg,
			FailureCode:            failureCode,
			FailureReason:          failureReason,
			RolledBackChannels:     rolledChannels,
			RollbackFailedChannels: rollbackFailed,
		})
}

func (e *ET1010Executor) confirmChillerStoppedBeforeAuxShutdown(
	ctx context.Context,
	plan et1010UnitPlan,
	env CommandEnvelopeWire,
	checkAborted func() error,
) (bool, error) {
	if e.chillerPowerReader == nil {
		return true, nil
	}
	readPower := func() (*float64, error) {
		if err := checkAborted(); err != nil {
			return nil, err
		}
		val, err := e.chillerPowerReader(ctx, plan.unitCode)
		if abortErr := checkAborted(); abortErr != nil {
			return nil, abortErr
		}
		if err != nil || val == nil || math.IsNaN(*val) || math.IsInf(*val, 0) {
			return nil, nil
		}
		cp := *val
		return &cp, nil
	}

	power, err := readPower()
	if err != nil {
		return false, err
	}
	if power != nil && *power <= e.chillerStopPowerKW {
		return true, nil
	}

	for range et1010StopRecheckLimit {
		if err := e.sleep(ctx, time.Duration(e.timings.AuxiliaryStopStepMS)*time.Millisecond); err != nil {
			if abortErr := checkAborted(); abortErr != nil {
				return false, abortErr
			}
			return false, err
		}
		nextPower, err := readPower()
		if err != nil {
			return false, err
		}
		if nextPower != nil {
			power = nextPower
			if *power <= e.chillerStopPowerKW {
				return true, nil
			}
		}
	}

	threshold := e.chillerStopPowerKW
	if power == nil {
		e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
			et1010DiagnosticData{
				Code:            "CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD",
				UnitCode:        string(plan.unitCode),
				CommandID:       env.CommandID,
				Message:         fmt.Sprintf("%s: aborted auxiliary shutdown because chiller meter power was unavailable (null) after %d checks", plan.unitCode, et1010StopRecheckLimit+1),
				StopThresholdKW: &threshold,
			})
		return false, nil
	}
	e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "CHILLER_STOP_UNCONFIRMED_AUX_HELD", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
		et1010DiagnosticData{
			Code:            "CHILLER_STOP_UNCONFIRMED_AUX_HELD",
			UnitCode:        string(plan.unitCode),
			CommandID:       env.CommandID,
			Message:         fmt.Sprintf("%s: aborted auxiliary shutdown because chiller power remained %.2f kW (> %.2f kW)", plan.unitCode, *power, e.chillerStopPowerKW),
			PowerKW:         power,
			StopThresholdKW: &threshold,
		})
	return false, nil
}

func (e *ET1010Executor) verifyRequiredCoilsClosed(
	ctx context.Context,
	plan et1010UnitPlan,
	env CommandEnvelopeWire,
	checkAborted func() error,
) (bool, error) {
	required := []et1010AuxPart{plan.chwp, plan.cwp, plan.tower}
	var openLabels []string
	var openChannels []string
	for _, part := range required {
		if err := checkAborted(); err != nil {
			return false, err
		}
		closed, err := e.readCoil(ctx, part.coil)
		if abortErr := checkAborted(); abortErr != nil {
			return false, abortErr
		}
		if err != nil {
			return false, err
		}
		if !closed {
			openLabels = append(openLabels, fmt.Sprintf("%s(coil=%d)", part.channel, part.coil))
			openChannels = append(openChannels, part.channel)
		}
	}
	if len(openLabels) == 0 {
		return true, nil
	}
	e.publishDiagnosticEvent(e.bgCtx, "WARNING", "PRE_PULSE_COIL_OPEN", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
		et1010DiagnosticData{
			Code:         "PRE_PULSE_COIL_OPEN",
			UnitCode:     string(plan.unitCode),
			CommandID:    env.CommandID,
			Message:      fmt.Sprintf("%s: aborted chiller start pulse because coils are open: %s", plan.unitCode, strings.Join(openLabels, ", ")),
			OpenChannels: openChannels,
		})
	return false, nil
}

func (e *ET1010Executor) executeEmergencyDORelease(ctx context.Context, env CommandEnvelopeWire) CommandReplyWire {
	e.stateMu.Lock()
	for unitCode := range e.activeSequences {
		e.abortUnitSequenceLocked(unitCode, "EMERGENCY_DO_RELEASE")
	}
	e.stateMu.Unlock()

	_ = e.WaitForActiveSequences(ctx)

	writeFailures := 0
	for _, entry := range et1010AllCoils {
		if err := e.writeCoil(ctx, entry.coil, false); err != nil {
			writeFailures++
			continue
		}
		e.stateMu.Lock()
		delete(e.pendingPulseCoils, entry.coil)
		e.stateMu.Unlock()
	}
	persistFailed := e.persistCurrentState() != nil

	ts := e.nowMS()
	e.stateMu.Lock()
	entries := make([]mqttUplinkDevice, 0, len(et1010AllAuxParts)+1)
	for _, part := range et1010AllAuxParts {
		if v, known := e.knownCoilStates[part.coil]; known && !v {
			entries = append(entries, mqttUplinkDevice{
				DeviceID:        part.deviceID,
				DeviceTimestamp: ts,
				Points:          []mqttUplinkPoint{{Code: "run_state", Value: "STOPPED", Quality: 0}},
			})
		}
	}
	var etPoints []mqttUplinkPoint
	if writeFailures == 0 {
		etPoints = append(etPoints, mqttUplinkPoint{Code: "run_state", Value: "STOPPED", Quality: 0})
	}
	for _, c := range et1010AllCoils {
		if v, known := e.knownCoilStates[c.coil]; known && !v {
			etPoints = append(etPoints, mqttUplinkPoint{Code: c.channel, Value: 0, Quality: 0})
		}
	}
	e.stateMu.Unlock()
	if len(etPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: "et1010", DeviceTimestamp: ts, Points: etPoints})
	}
	if len(entries) > 0 {
		e.emitTelemetryEntries(ctx, entries)
	}

	if writeFailures > 0 {
		return e.buildReply(env, "FAILED", "MODBUS_WRITE_FAILED", "all-do:0", "NOT_APPLIED")
	}
	if persistFailed {
		return e.buildReply(env, "FAILED", "STATE_PERSIST_FAILED", "all-do:0", "NOT_APPLIED")
	}
	return e.buildReply(env, "EXECUTED", "", "all-do:0", "all-do:0")
}

func (e *ET1010Executor) executePulse(
	ctx context.Context,
	coil uint16,
	channel string,
	duration time.Duration,
	unitCode ET1010UnitCode,
	commandID string,
) error {
	e.stateMu.Lock()
	e.pendingPulseCoils[coil] = struct{}{}
	e.stateMu.Unlock()

	if err := e.persistCurrentState(); err != nil {
		e.stateMu.Lock()
		delete(e.pendingPulseCoils, coil)
		e.stateMu.Unlock()
		return et1010StatePersistErr{cause: err}
	}

	var writeStartErr error
	var sleepErr error
	if err := e.writeCoil(ctx, coil, true); err != nil {
		writeStartErr = err
	} else if err := e.sleep(ctx, duration); err != nil {
		sleepErr = err
	}

	// Always force pulse coil back to 0 even if sleep/sequence was aborted or set write failed.
	resetCtx, resetCancel := context.WithTimeout(e.bgCtx, 5*time.Second)
	defer resetCancel()
	if resetErr := e.resetPulseCoilWithRetry(resetCtx, coil); resetErr != nil {
		_ = e.persistCurrentState()
		e.logger.Error("et1010_pulse_reset_failed",
			"code", "PULSE_RESET_FAILED",
			"unit", unitCode,
			"command_id", commandID,
			"channel", channel,
			"coil", coil,
			"error", resetErr.Error(),
		)
		return et1010PulseResetErr{cause: resetErr}
	}

	e.stateMu.Lock()
	delete(e.pendingPulseCoils, coil)
	e.stateMu.Unlock()
	if err := e.persistCurrentState(); err != nil && writeStartErr == nil && sleepErr == nil {
		return et1010StatePersistErr{cause: err}
	}
	if writeStartErr != nil {
		return writeStartErr
	}
	e.emitTelemetryEntries(resetCtx, []mqttUplinkDevice{{
		DeviceID:        "et1010",
		DeviceTimestamp: e.nowMS(),
		Points:          []mqttUplinkPoint{{Code: channel, Value: 0, Quality: 0}},
	}})
	return sleepErr
}

func (e *ET1010Executor) resetPulseCoilWithRetry(ctx context.Context, coil uint16) error {
	var lastErr error
	for attempt := 1; attempt <= et1010PulseResetAttempts; attempt++ {
		if err := e.writeCoil(ctx, coil, false); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < et1010PulseResetAttempts {
			if err := e.sleep(ctx, time.Duration(attempt)*et1010PulseResetBackoff); err != nil {
				return err
			}
		}
	}
	return lastErr
}

func (e *ET1010Executor) writeCoil(ctx context.Context, coil uint16, value bool) error {
	e.coilMu.Lock()
	defer e.coilMu.Unlock()
	if err := e.coilBus.WriteCoil(ctx, e.unitID, coil, value); err != nil {
		return err
	}
	e.stateMu.Lock()
	e.knownCoilStates[coil] = value
	e.stateMu.Unlock()
	return nil
}

func (e *ET1010Executor) readCoil(ctx context.Context, coil uint16) (bool, error) {
	e.coilMu.Lock()
	defer e.coilMu.Unlock()
	val, err := e.coilBus.ReadCoil(ctx, e.unitID, coil)
	if err != nil {
		return false, err
	}
	e.stateMu.Lock()
	e.knownCoilStates[coil] = val
	e.stateMu.Unlock()
	return val, nil
}

func (e *ET1010Executor) abortUnitSequenceLocked(unitCode ET1010UnitCode, reason string) bool {
	lc := e.activeSequences[unitCode]
	if lc == nil || lc.ctx.Err() != nil {
		return false
	}
	lc.cancel(et1010SequenceAbortErr{reason: reason})
	return true
}

func (e *ET1010Executor) commitCommandAcceptedLocked(env CommandEnvelopeWire) {
	e.lastFenceByDevice[env.DeviceID] = env.ExecutionFence
	e.recordExecutedLocked(env.CommandID)
}

func (e *ET1010Executor) recordExecutedLocked(commandID string) {
	if _, exists := e.executedIDSet[commandID]; exists {
		return
	}
	e.executedIDSet[commandID] = struct{}{}
	e.executedIDOrder = append(e.executedIDOrder, commandID)
	for len(e.executedIDOrder) > et1010MaxExecutedCmdIDs {
		oldest := e.executedIDOrder[0]
		e.executedIDOrder = e.executedIDOrder[1:]
		delete(e.executedIDSet, oldest)
	}
}

func (e *ET1010Executor) snapshotLocked() ET1010PersistedState {
	coils := make([]uint16, 0, len(e.pendingPulseCoils))
	for c := range e.pendingPulseCoils {
		coils = append(coils, c)
	}
	sort.Slice(coils, func(i, j int) bool { return coils[i] < coils[j] })
	return ET1010PersistedState{
		LastSequence:       e.uplinkSeq,
		LastFenceByDevice:  maps.Clone(e.lastFenceByDevice),
		ExecutedCommandIDs: slices.Clone(e.executedIDOrder),
		PendingPulseCoils:  coils,
	}
}

func (e *ET1010Executor) snapshotWithAcceptedLocked(env CommandEnvelopeWire) ET1010PersistedState {
	fences := maps.Clone(e.lastFenceByDevice)
	if fences == nil {
		fences = map[string]uint64{}
	}
	fences[env.DeviceID] = env.ExecutionFence

	ids := slices.Clone(e.executedIDOrder)
	if _, exists := e.executedIDSet[env.CommandID]; !exists {
		ids = append(ids, env.CommandID)
		if len(ids) > et1010MaxExecutedCmdIDs {
			ids = ids[len(ids)-et1010MaxExecutedCmdIDs:]
		}
	}
	coils := make([]uint16, 0, len(e.pendingPulseCoils))
	for c := range e.pendingPulseCoils {
		coils = append(coils, c)
	}
	sort.Slice(coils, func(i, j int) bool { return coils[i] < coils[j] })
	return ET1010PersistedState{
		LastSequence:       e.uplinkSeq,
		LastFenceByDevice:  fences,
		ExecutedCommandIDs: ids,
		PendingPulseCoils:  coils,
	}
}

func (e *ET1010Executor) persistCurrentState() error {
	e.stateMu.Lock()
	snap := e.snapshotLocked()
	e.stateMu.Unlock()
	return e.writePersistedState(snap)
}

func (e *ET1010Executor) loadPersistedState() (*ET1010PersistedState, error) {
	if e.statePath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(e.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var loaded ET1010PersistedState
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, err
	}
	return &loaded, nil
}

func (e *ET1010Executor) writePersistedState(snap ET1010PersistedState) error {
	if e.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(e.statePath), 0o750); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(e.statePath, append(encoded, '\n'), 0o600)
}

func (e *ET1010Executor) nowMS() int64 {
	ms := e.now().UTC().UnixMilli()
	if ms < 1 {
		return 1
	}
	return ms
}

func (e *ET1010Executor) nextUplinkSeq() uint64 {
	ts := uint64(e.nowMS()) * 1000
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if ts <= e.uplinkSeq {
		e.uplinkSeq++
	} else {
		e.uplinkSeq = ts
	}
	return e.uplinkSeq
}

func (e *ET1010Executor) emitTelemetryEntries(ctx context.Context, devices []mqttUplinkDevice) {
	if e.publishTelemetry == nil || len(devices) == 0 {
		return
	}
	now := e.now().UTC()
	tsMs := e.nowMS()
	seq := e.nextUplinkSeq()
	_ = e.persistCurrentState()
	wire, err := json.Marshal(mqttUplinkTelemetryEnvelope{
		SchemaVersion: "2.0",
		MessageID:     e.uuidv7(now),
		GatewayID:     e.gatewayID,
		Timestamp:     tsMs,
		Sequence:      seq,
		Replay:        false,
		Payload:       mqttUplinkDevicePayload{Devices: devices},
	})
	if err != nil {
		return
	}
	e.publishTelemetry(ctx, wire)
}

func (e *ET1010Executor) publishDiagnosticEvent(
	ctx context.Context,
	severity string,
	eventCode string,
	unitCode ET1010UnitCode,
	sourceID string,
	commandID string,
	traceID string,
	data et1010DiagnosticData,
) {
	e.logger.Warn("et1010_diagnostic_event", "code", eventCode, "unit", unitCode, "command_id", commandID, "message", data.Message)
	if e.publishEvent == nil {
		return
	}
	now := e.now().UTC()
	tsMs := e.nowMS()
	seq := e.nextUplinkSeq()
	_ = e.persistCurrentState()

	rawData, err := json.Marshal(data)
	if err != nil {
		return
	}
	if sourceID == "" {
		sourceID = "et1010"
	}
	wire, err := json.Marshal(mqttUplinkEventEnvelope{
		SchemaVersion: "2.0",
		MessageID:     e.uuidv7(now),
		GatewayID:     e.gatewayID,
		Timestamp:     tsMs,
		Sequence:      seq,
		TraceID:       traceID,
		Payload: mqttUplinkEventPayload{
			EventType:  eventCode,
			SourceType: "COMMAND_EXECUTOR",
			SourceID:   sourceID,
			EventTime:  tsMs,
			Severity:   severity,
			Data:       rawData,
		},
	})
	if err != nil {
		return
	}
	e.publishEvent(ctx, wire)
}

func (e *ET1010Executor) buildReply(env CommandEnvelopeWire, edgeStatus, reasonCode, requestedText, appliedText string) CommandReplyWire {
	e.stateMu.Lock()
	e.cycle++
	cycle := e.cycle
	e.stateMu.Unlock()

	now := e.now().UTC()
	req := commandmodel.TextScalar(requestedText)
	effText := appliedText
	appliedOut := appliedText
	var reasonPtr *string
	if edgeStatus != "EXECUTED" {
		if reasonCode != "" {
			effText = reasonCode
			r := reasonCode
			reasonPtr = &r
		} else {
			effText = edgeStatus
		}
		appliedOut = "NOT_APPLIED"
	}
	eff := commandmodel.TextScalar(effText)
	app := commandmodel.TextScalar(appliedOut)
	return CommandReplyWire{
		SchemaVersion: commandSchemaVersion,
		MessageID:     e.uuidv7(now),
		CommandID:     env.CommandID,
		TraceID:       env.TraceID,
		EventTime:     e.nowMS(),
		EdgeStatus:    edgeStatus,
		ExecutionEvidence: &commandmodel.EdgeExecutionEvidence{
			Requested:          req,
			Effective:          &eff,
			Applied:            &app,
			WinnerControllerID: et1010WinnerControllerID,
			Cycle:              cycle,
		},
		ReasonCode:     reasonPtr,
		ExecutionFence: env.ExecutionFence,
	}
}

func isAbortUnitSequence(env CommandEnvelopeWire) bool {
	return env.ActuationMode == "ABORT_UNIT_SEQUENCE" || env.CommandCode == "ABORT_UNIT_SEQUENCE"
}

func resolveET1010UnitCode(env CommandEnvelopeWire, allowAbortMode bool) (ET1010UnitCode, bool) {
	inferred, ok := et1010ChillerUnitByDev[env.DeviceID]
	if !ok {
		return "", false
	}
	if env.Channel != "" && env.Channel != string(inferred) {
		return "", false
	}
	if env.ActuationMode != "" {
		if allowAbortMode {
			if env.ActuationMode != "ABORT_UNIT_SEQUENCE" && env.ActuationMode != "UNIT_SEQUENCE" {
				return "", false
			}
		} else if env.ActuationMode != "UNIT_SEQUENCE" {
			return "", false
		}
	}
	return inferred, true
}

func matchesDirectRoute(env CommandEnvelopeWire, route et1010DirectRoute) bool {
	if env.ActuationMode != "" && env.ActuationMode != route.mode {
		return false
	}
	if env.Channel != "" && env.Channel != route.channel {
		return false
	}
	if env.CoilAddress != nil && *env.CoilAddress != int(route.coil) {
		return false
	}
	return true
}

func isEmergencyDORelease(env CommandEnvelopeWire) bool {
	if env.CommandCode != "STOP" {
		return false
	}
	matched := env.ActuationMode == "EMERGENCY_DO_RELEASE" || (env.DeviceID == "et1010" && env.PointCode == "emergency_stop_all")
	if !matched {
		return false
	}
	if env.ActuationMode != "" && env.ActuationMode != "EMERGENCY_DO_RELEASE" {
		return false
	}
	if env.Channel != "" && env.Channel != "all-do" {
		return false
	}
	return true
}
