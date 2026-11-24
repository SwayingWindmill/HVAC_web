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
	doPulseResetAttempts = 3
	doPulseResetBackoff  = 50 * time.Millisecond
	doStopRecheckLimit   = 2
	doMaxExecutedCmdIDs  = 1000
	doStateFileName      = "do-executor-state.json"
)

// DOCoilBus abstracts Modbus RTU coil writes and readback over a shared serial bus.
type DOCoilBus interface {
	WriteCoil(ctx context.Context, unitID uint8, coilAddress uint16, value bool) error
	ReadCoil(ctx context.Context, unitID uint8, coilAddress uint16) (bool, error)
}

// ModbusDOCoilBus adapts *edgecontrol.ModbusTCPBridge to DOCoilBus.
type ModbusDOCoilBus struct {
	Bridge *edgecontrol.ModbusTCPBridge
}

// WriteCoil sends FC05 Write Single Coil (high bus priority) through ModbusTCPBridge.
func (b ModbusDOCoilBus) WriteCoil(ctx context.Context, unitID uint8, coilAddress uint16, value bool) error {
	if b.Bridge == nil {
		return errors.New("do modbus bridge is nil")
	}
	v := uint16(0)
	if value {
		v = 1
	}
	return b.Bridge.Write(ctx, edgecontrol.ModbusWriteTask{
		Name:         fmt.Sprintf("do-coil-%d", coilAddress),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionWriteSingleCoil,
		Address:      coilAddress,
		Values:       []uint16{v},
	})
}

// ReadCoil sends FC01 Read Coils (normal bus priority) through ModbusTCPBridge.
func (b ModbusDOCoilBus) ReadCoil(ctx context.Context, unitID uint8, coilAddress uint16) (bool, error) {
	if b.Bridge == nil {
		return false, errors.New("do modbus bridge is nil")
	}
	bits, err := b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name:         fmt.Sprintf("do-read-coil-%d", coilAddress),
		UnitID:       unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadCoils,
		Address:      coilAddress,
		Quantity:     1,
	})
	if err != nil {
		return false, err
	}
	if len(bits) == 0 {
		return false, fmt.Errorf("do coil %d read returned empty slice", coilAddress)
	}
	return bits[0] != 0, nil
}

type doAuxPart struct {
	deviceKey         string
	channel           string
	coil              uint16
	afterStartDelayMS *int
}

type doPulsePart struct {
	channel string
	coil    uint16
}

type doUnitPlan struct {
	unitCode        string
	chillerDeviceID string
	meterDeviceKey  string
	auxiliaries     []doAuxPart
	chillerStart    doPulsePart
	chillerStop     doPulsePart
}

type doDirectRoute struct {
	mode     string
	channel  string
	coil     uint16
	value    bool
	unitCode string
}

type doPulseDescriptor struct {
	coil     uint16
	channel  string
	unitCode string
}

// DOExecutorPersistedState is the durable local JSON state for idempotency, fences, and stuck pulse recovery.
type DOExecutorPersistedState struct {
	LastSequence       uint64            `json:"lastSequence"`
	LastFenceByDevice  map[string]uint64 `json:"lastFenceByDevice"`
	ExecutedCommandIDs []string          `json:"executedCommandIds"`
	PendingPulseCoils  []uint16          `json:"pendingPulseCoils"`
}

// DOSequenceExecutorOptions configures the declarative DO command and sequence executor.
type DOSequenceExecutorOptions struct {
	GatewayID          string
	StateDir           string
	Config             DOControlConfig
	CoilBus            DOCoilBus
	ChillerPowerReader func(ctx context.Context, meterDeviceKey string) (*float64, error)
	PublishTelemetry   func(ctx context.Context, payload []byte)
	PublishEvent       func(ctx context.Context, payload []byte)
	Now                func() time.Time
	Sleep              func(ctx context.Context, d time.Duration) error
	UUIDv7             func(now time.Time) string
	Logger             *slog.Logger
	SequenceContext    context.Context
}

type doDiagnosticData struct {
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

type doSequenceAbortErr struct {
	reason string
}

func (e doSequenceAbortErr) Error() string { return e.reason }

type doPulseResetErr struct{ cause error }

func (e doPulseResetErr) Error() string { return e.cause.Error() }

type doStatePersistErr struct{ cause error }

func (e doStatePersistErr) Error() string { return e.cause.Error() }

type doSequenceLifecycle struct {
	cancel context.CancelCauseFunc
	ctx    context.Context
	done   chan struct{}
}

// DOSequenceExecutor executes declarative Modbus DO coil commands, pulses, chiller start/stop
// sequences with reverse rollback and fail-closed chiller power verification, and E-stop release.
type DOSequenceExecutor struct {
	gatewayID          string
	deviceKey          string
	winnerControllerID string
	unitID             uint8
	statePath          string
	coilBus            DOCoilBus
	chillerPowerReader func(ctx context.Context, meterDeviceKey string) (*float64, error)
	chillerStopPowerKW float64
	pulseDurationMS    int
	timings            DOSequenceTimings
	publishTelemetry   func(ctx context.Context, payload []byte)
	publishEvent       func(ctx context.Context, payload []byte)
	now                func() time.Time
	sleep              func(ctx context.Context, d time.Duration) error
	uuidv7             func(now time.Time) string
	logger             *slog.Logger
	bgCtx              context.Context
	bgCancel           context.CancelFunc

	allCoils         []DOCoilConfig
	allAuxParts      []doAuxPart
	pulseDescriptors []doPulseDescriptor
	pulseCoilSet     map[uint16]struct{}
	unitPlans        map[string]doUnitPlan
	chillerUnitByDev map[string]string
	directRoutes     map[string]doDirectRoute
	managedDevices   map[string]struct{}

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
	activeSequences   map[string]*doSequenceLifecycle
	allTasks          map[*doSequenceLifecycle]struct{}
	cycle             uint64
	uplinkSeq         uint64
}

// NewDOSequenceExecutor constructs a DOSequenceExecutor from declarative DOControlConfig.
func NewDOSequenceExecutor(opts DOSequenceExecutorOptions) (*DOSequenceExecutor, error) {
	gwID := strings.TrimSpace(opts.GatewayID)
	if gwID == "" {
		return nil, errors.New("do executor requires gatewayId")
	}
	if opts.CoilBus == nil {
		return nil, errors.New("do executor requires coilBus")
	}

	cfg := opts.Config
	cfg.DeviceKey = strings.TrimSpace(cfg.DeviceKey)
	if cfg.PollIntervalSec == 0 {
		cfg.PollIntervalSec = defaultDOPollSec
	}
	if cfg.PulseDurationMS == 0 {
		cfg.PulseDurationMS = defaultDOPulseDurationMS
	}
	if cfg.ChillerStopPowerKW == nil {
		kw := defaultDOStopPowerKW
		cfg.ChillerStopPowerKW = &kw
	}
	if strings.TrimSpace(cfg.WinnerControllerID) == "" && cfg.DeviceKey != "" && cfg.UnitID > 0 {
		cfg.WinnerControllerID = fmt.Sprintf("%s-uid%d", cfg.DeviceKey, cfg.UnitID)
	} else {
		cfg.WinnerControllerID = strings.TrimSpace(cfg.WinnerControllerID)
	}

	// Allow unit tests constructing DOSequenceExecutor directly without meter validation;
	// Config.Validate() enforces cross-checks with Meters when loaded as part of FieldPlantConfig.
	dummyMeters := make(map[string]struct{})
	for _, u := range cfg.Units {
		if k := strings.TrimSpace(u.MeterDeviceKey); k != "" {
			dummyMeters[k] = struct{}{}
		}
	}
	if err := validateDOControlConfig(&cfg, len(dummyMeters) > 0, dummyMeters); err != nil {
		return nil, err
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = defaultEdgeSleep
	}
	uuidFn := opts.UUIDv7
	if uuidFn == nil {
		uuidFn = func(t time.Time) string {
			id, err := edgecontrol.NewReplicationUUIDv7(t)
			if err != nil {
				return edgecontrol.DeterministicReplicationUUIDv7(t, gwID, cfg.DeviceKey)
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
	var statePath string
	if trimmed := strings.TrimSpace(opts.StateDir); trimmed != "" {
		statePath = filepath.Join(trimmed, doStateFileName)
	}

	allCoils := make([]DOCoilConfig, len(cfg.Coils))
	for i, c := range cfg.Coils {
		allCoils[i] = DOCoilConfig{Channel: strings.TrimSpace(c.Channel), Coil: c.Coil}
	}
	unitPlans := make(map[string]doUnitPlan, len(cfg.Units))
	chillerUnitByDev := make(map[string]string, len(cfg.Units))
	directRoutes := make(map[string]doDirectRoute, len(cfg.Units)*8)
	managedDevices := map[string]struct{}{cfg.DeviceKey: {}}
	pulseCoilSet := make(map[uint16]struct{}, len(cfg.Units)*2)
	var pulseDescriptors []doPulseDescriptor
	var allAuxParts []doAuxPart

	for _, u := range cfg.Units {
		unitCode := strings.TrimSpace(u.UnitCode)
		chillerKey := strings.TrimSpace(u.ChillerDeviceKey)
		auxParts := make([]doAuxPart, 0, len(u.Auxiliaries))
		for _, aux := range u.Auxiliaries {
			var delayCopy *int
			if aux.AfterStartDelayMS != nil {
				v := *aux.AfterStartDelayMS
				delayCopy = &v
			}
			part := doAuxPart{
				deviceKey:         strings.TrimSpace(aux.DeviceKey),
				channel:           strings.TrimSpace(aux.Channel),
				coil:              aux.Coil,
				afterStartDelayMS: delayCopy,
			}
			auxParts = append(auxParts, part)
			allAuxParts = append(allAuxParts, part)
			managedDevices[part.deviceKey] = struct{}{}
			directRoutes[part.deviceKey+"|START"] = doDirectRoute{
				mode: "LEVEL", channel: part.channel, coil: part.coil, value: true, unitCode: unitCode,
			}
			directRoutes[part.deviceKey+"|STOP"] = doDirectRoute{
				mode: "LEVEL", channel: part.channel, coil: part.coil, value: false, unitCode: unitCode,
			}
		}

		startCh := strings.TrimSpace(u.StartPulse.Channel)
		stopCh := strings.TrimSpace(u.StopPulse.Channel)
		plan := doUnitPlan{
			unitCode:        unitCode,
			chillerDeviceID: chillerKey,
			meterDeviceKey:  strings.TrimSpace(u.MeterDeviceKey),
			auxiliaries:     auxParts,
			chillerStart:    doPulsePart{channel: startCh, coil: u.StartPulse.Coil},
			chillerStop:     doPulsePart{channel: stopCh, coil: u.StopPulse.Coil},
		}
		unitPlans[unitCode] = plan
		chillerUnitByDev[chillerKey] = unitCode
		managedDevices[chillerKey] = struct{}{}

		directRoutes[chillerKey+"|START"] = doDirectRoute{
			mode: "PULSE", channel: startCh, coil: u.StartPulse.Coil, value: true, unitCode: unitCode,
		}
		directRoutes[chillerKey+"|STOP"] = doDirectRoute{
			mode: "PULSE", channel: stopCh, coil: u.StopPulse.Coil, value: true, unitCode: unitCode,
		}

		if _, seen := pulseCoilSet[u.StartPulse.Coil]; !seen {
			pulseCoilSet[u.StartPulse.Coil] = struct{}{}
			pulseDescriptors = append(pulseDescriptors, doPulseDescriptor{coil: u.StartPulse.Coil, channel: startCh, unitCode: unitCode})
		}
		if _, seen := pulseCoilSet[u.StopPulse.Coil]; !seen {
			pulseCoilSet[u.StopPulse.Coil] = struct{}{}
			pulseDescriptors = append(pulseDescriptors, doPulseDescriptor{coil: u.StopPulse.Coil, channel: stopCh, unitCode: unitCode})
		}
	}

	bgCtx, bgCancel := context.WithCancel(parentCtx)
	return &DOSequenceExecutor{
		gatewayID:          gwID,
		deviceKey:          cfg.DeviceKey,
		winnerControllerID: cfg.WinnerControllerID,
		unitID:             cfg.UnitID,
		statePath:          statePath,
		coilBus:            opts.CoilBus,
		chillerPowerReader: opts.ChillerPowerReader,
		chillerStopPowerKW: cfg.StopPowerKW(),
		pulseDurationMS:    cfg.PulseDurationMS,
		timings:            cfg.EffectiveTimings(),
		publishTelemetry:   opts.PublishTelemetry,
		publishEvent:       opts.PublishEvent,
		now:                nowFn,
		sleep:              sleepFn,
		uuidv7:             uuidFn,
		logger:             logger,
		bgCtx:              bgCtx,
		bgCancel:           bgCancel,
		allCoils:           allCoils,
		allAuxParts:        allAuxParts,
		pulseDescriptors:   pulseDescriptors,
		pulseCoilSet:       pulseCoilSet,
		unitPlans:          unitPlans,
		chillerUnitByDev:   chillerUnitByDev,
		directRoutes:       directRoutes,
		managedDevices:     managedDevices,
		lastFenceByDevice:  make(map[string]uint64),
		executedIDSet:      make(map[string]struct{}),
		pendingPulseCoils:  make(map[uint16]struct{}),
		knownCoilStates:    make(map[uint16]bool),
		activeSequences:    make(map[string]*doSequenceLifecycle),
		allTasks:           make(map[*doSequenceLifecycle]struct{}),
	}, nil
}

// HandlesCommand reports whether env targets this DO executor or a DO sequence actuation mode.
func (e *DOSequenceExecutor) HandlesCommand(env CommandEnvelopeWire) bool {
	if e == nil {
		return false
	}
	if _, ok := e.managedDevices[strings.TrimSpace(env.DeviceID)]; ok {
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

func defaultEdgeSleep(ctx context.Context, d time.Duration) error {
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
func (e *DOSequenceExecutor) Initialize(ctx context.Context) error {
	e.initOnce.Do(func() {
		e.initErr = e.runInitialize(ctx)
	})
	return e.initErr
}

// Close aborts running background sequences and waits briefly for cleanup.
func (e *DOSequenceExecutor) Close() {
	if e == nil {
		return
	}
	e.bgCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = e.WaitForActiveSequences(ctx)
}

// WaitForActiveSequences blocks until all accepted sequence goroutines complete or ctx expires.
func (e *DOSequenceExecutor) WaitForActiveSequences(ctx context.Context) error {
	for {
		e.stateMu.Lock()
		var first *doSequenceLifecycle
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

func (e *DOSequenceExecutor) runInitialize(ctx context.Context) error {
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
		if len(loaded.ExecutedCommandIDs) > doMaxExecutedCmdIDs {
			startIdx = len(loaded.ExecutedCommandIDs) - doMaxExecutedCmdIDs
		}
		for _, id := range loaded.ExecutedCommandIDs[startIdx:] {
			if strings.TrimSpace(id) != "" {
				e.recordExecutedLocked(id)
			}
		}
		for _, coil := range loaded.PendingPulseCoils {
			if _, ok := e.pulseCoilSet[coil]; ok {
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
	for _, desc := range e.pulseDescriptors {
		if err := e.resetPulseCoilWithRetry(ctx, desc.coil); err != nil {
			e.stateMu.Lock()
			e.pendingPulseCoils[desc.coil] = struct{}{}
			e.stateMu.Unlock()
			e.logger.Error("do_startup_pulse_reset_failed",
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
		e.logger.Warn("do_startup_cleared_pending_pulses", "coils", strings.Join(cleared, ", "))
	}
	if err := e.persistCurrentState(); err != nil {
		return err
	}
	if len(resetPoints) > 0 {
		e.emitTelemetryEntries(ctx, []mqttUplinkDevice{{
			DeviceID:        e.deviceKey,
			DeviceTimestamp: e.nowMS(),
			Points:          resetPoints,
		}})
	}
	return nil
}

// HandlePayload decodes a v2.0 downlink CommandEnvelopeWire JSON payload and executes it.
func (e *DOSequenceExecutor) HandlePayload(ctx context.Context, payload []byte) ([]byte, bool) {
	if e == nil {
		return nil, false
	}
	env, ok := decodeStrictCommandEnvelope(payload)
	if !ok {
		e.logger.Warn("do_command_envelope_invalid")
		return nil, false
	}
	reply := e.HandleCommand(ctx, env)
	wire, err := json.Marshal(reply)
	if err != nil {
		e.logger.Warn("do_command_reply_marshal_failed", "command_id", reply.CommandID, "error", err.Error())
		return nil, false
	}
	return wire, true
}

// HandleCommand enforces envelope, idempotency, fence, TTL, routing, and executes DO control.
func (e *DOSequenceExecutor) HandleCommand(ctx context.Context, env CommandEnvelopeWire) CommandReplyWire {
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
		unitPlan       *doUnitPlan
		lifecycle      *doSequenceLifecycle
		prevLifecycle  *doSequenceLifecycle
		directRoute    *doDirectRoute
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

		isEmergency := e.isEmergencyDORelease(env)
		isAbort := !isEmergency && isAbortUnitSequence(env)
		isUnitSeq := !isEmergency && !isAbort && (env.CommandCode == "START_UNIT_SEQUENCE" || env.CommandCode == "STOP_UNIT_SEQUENCE")

		var unitPlan *doUnitPlan
		var directRoute *doDirectRoute

		if isAbort || isUnitSeq {
			unitCode, ok := e.resolveUnitCode(env, isAbort)
			if !ok {
				rep := e.buildReply(env, "REJECTED", "UNKNOWN_ACTUATION_ROUTE", env.CommandCode, "NOT_APPLIED")
				return admissionResult{immediateReply: &rep}
			}
			plan := e.unitPlans[unitCode]
			unitPlan = &plan
		} else if !isEmergency {
			route, ok := e.directRoutes[env.DeviceID+"|"+env.CommandCode]
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
			e.logger.Error("do_state_persist_failed", "command_id", env.CommandID, "error", err.Error())
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
			rep := e.buildReply(env, "EXECUTED", "", env.CommandCode, unitPlan.unitCode+":sequence-aborted")
			return admissionResult{immediateReply: &rep}
		}

		if isUnitSeq && unitPlan != nil {
			e.stateMu.Lock()
			e.abortUnitSequenceLocked(unitPlan.unitCode, "SEQUENCE_SUPERSEDED")
			prev := e.activeSequences[unitPlan.unitCode]
			seqCtx, cancel := context.WithCancelCause(e.bgCtx)
			lc := &doSequenceLifecycle{
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
		lc := &doSequenceLifecycle{
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
			{DeviceID: e.deviceKey, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: route.channel, Value: valInt, Quality: 0}}},
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
		var prErr doPulseResetErr
		var spErr doStatePersistErr
		if errors.As(err, &prErr) {
			reason = "PULSE_RESET_FAILED"
		} else if errors.As(err, &spErr) {
			reason = "STATE_PERSIST_FAILED"
		}
		return e.buildReply(env, "FAILED", reason, reqText, "NOT_APPLIED")
	}
	return e.buildReply(env, "EXECUTED", "", reqText, reqText+"->0")
}

func (e *DOSequenceExecutor) releaseSequenceLifecycle(unitCode string, lc *doSequenceLifecycle) {
	e.stateMu.Lock()
	if e.activeSequences[unitCode] == lc {
		delete(e.activeSequences, unitCode)
	}
	delete(e.allTasks, lc)
	e.stateMu.Unlock()
	close(lc.done)
}

func (e *DOSequenceExecutor) releaseDirectTask(lc *doSequenceLifecycle) {
	e.stateMu.Lock()
	delete(e.allTasks, lc)
	e.stateMu.Unlock()
	close(lc.done)
}

func (e *DOSequenceExecutor) auxStartDelay(part doAuxPart, isLast bool) time.Duration {
	if part.afterStartDelayMS != nil {
		return time.Duration(*part.afterStartDelayMS) * time.Millisecond
	}
	if isLast {
		return time.Duration(e.timings.PreStartPulseDelayMS) * time.Millisecond
	}
	return time.Duration(e.timings.AuxStartStepDelayMS) * time.Millisecond
}

func (e *DOSequenceExecutor) executeUnitSequence(
	ctx context.Context,
	env CommandEnvelopeWire,
	plan doUnitPlan,
	lc *doSequenceLifecycle,
	prevLC *doSequenceLifecycle,
) CommandReplyWire {
	pulseMS := e.pulseDurationMS
	if env.PulseDurationMS > 0 {
		pulseMS = env.PulseDurationMS
	}
	isStart := env.CommandCode == "START_UNIT_SEQUENCE"
	var startedAux []doAuxPart
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
				var sa doSequenceAbortErr
				if errors.As(cause, &sa) {
					reason = sa.reason
				}
			}
			return doSequenceAbortErr{reason: reason}
		}
		return nil
	}

	writeStopStep := func(opCtx context.Context, part doAuxPart) error {
		if err := checkAborted(); err != nil {
			return err
		}
		if err := e.writeCoil(opCtx, part.coil, false); err != nil {
			return err
		}
		ts := e.nowMS()
		e.emitTelemetryEntries(opCtx, []mqttUplinkDevice{
			{DeviceID: e.deviceKey, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: part.channel, Value: 0, Quality: 0}}},
			{DeviceID: part.deviceKey, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: "run_state", Value: "STOPPED", Quality: 0}}},
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
		var sa doSequenceAbortErr
		if errors.As(err, &sa) {
			return e.buildReply(env, "REJECTED", sa.reason, env.CommandCode, "ABORTED")
		}
	}

	if isStart {
		if err := e.writeStartAuxStep(lc.ctx, plan.auxiliaries[0], &startedAux, checkAborted); err != nil {
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
				for i := 1; i < len(plan.auxiliaries); i++ {
					if err := waitStep(lc.ctx, e.auxStartDelay(plan.auxiliaries[i-1], false)); err != nil {
						return err
					}
					if err := e.writeStartAuxStep(lc.ctx, plan.auxiliaries[i], &localStarted, checkAborted); err != nil {
						return err
					}
				}
				lastAux := plan.auxiliaries[len(plan.auxiliaries)-1]
				if err := waitStep(lc.ctx, e.auxStartDelay(lastAux, true)); err != nil {
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
			var sa doSequenceAbortErr
			if errors.As(runErr, &sa) {
				return
			}
			var prErr doPulseResetErr
			if errors.As(runErr, &prErr) {
				e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "PULSE_RESET_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
					doDiagnosticData{
						Code:            "PULSE_RESET_FAILED",
						UnitCode:        plan.unitCode,
						CommandID:       env.CommandID,
						Message:         fmt.Sprintf("%s: chiller start pulse reset failed; holding started auxiliaries ON: %v", plan.unitCode, runErr),
						AuxiliariesHeld: true,
						FailureCode:     "PULSE_RESET_FAILED",
					})
				return
			}
			failureCode := "SEQUENCE_STEP_FAILED"
			var spErr doStatePersistErr
			if errors.As(runErr, &spErr) {
				failureCode = "STATE_PERSIST_FAILED"
			}
			e.rollbackStartedAuxiliaries(plan, env, localStarted, failureCode, runErr.Error(), lc.ctx)
		}()
		return e.buildReply(env, "EXECUTED", "", env.CommandCode, plan.unitCode+":sequence-accepted")
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
			for i := len(plan.auxiliaries) - 1; i >= 0; i-- {
				if err := writeStopStep(lc.ctx, plan.auxiliaries[i]); err != nil {
					return err
				}
				if i > 0 {
					if err := waitStep(lc.ctx, time.Duration(e.timings.AuxiliaryStopStepMS)*time.Millisecond); err != nil {
						return err
					}
				}
			}
			return nil
		}()
		if runErr == nil {
			return
		}
		var sa doSequenceAbortErr
		if errors.As(runErr, &sa) {
			return
		}
		var prErr doPulseResetErr
		if errors.As(runErr, &prErr) {
			e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "PULSE_RESET_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
				doDiagnosticData{
					Code:      "PULSE_RESET_FAILED",
					UnitCode:  plan.unitCode,
					CommandID: env.CommandID,
					Message:   fmt.Sprintf("%s: background pulse coil reset failed: %v", plan.unitCode, runErr),
				})
			return
		}
		e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "SEQUENCE_STEP_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
			doDiagnosticData{
				Code:      "SEQUENCE_STEP_FAILED",
				UnitCode:  plan.unitCode,
				CommandID: env.CommandID,
				Message:   fmt.Sprintf("%s: background sequence failed: %v", plan.unitCode, runErr),
			})
	}()
	return e.buildReply(env, "EXECUTED", "", env.CommandCode, plan.unitCode+":sequence-accepted")
}

func (e *DOSequenceExecutor) writeStartAuxStep(ctx context.Context, part doAuxPart, started *[]doAuxPart, checkAborted func() error) error {
	if err := checkAborted(); err != nil {
		return err
	}
	if err := e.writeCoil(ctx, part.coil, true); err != nil {
		return err
	}
	*started = append(*started, part)
	ts := e.nowMS()
	e.emitTelemetryEntries(ctx, []mqttUplinkDevice{
		{DeviceID: e.deviceKey, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: part.channel, Value: 1, Quality: 0}}},
		{DeviceID: part.deviceKey, DeviceTimestamp: ts, Points: []mqttUplinkPoint{{Code: "run_state", Value: "RUNNING", Quality: 0}}},
	})
	return nil
}

func (e *DOSequenceExecutor) handleForegroundSeqError(ctx context.Context, env CommandEnvelopeWire, plan doUnitPlan, startedAux []doAuxPart, err error) CommandReplyWire {
	var sa doSequenceAbortErr
	if errors.As(err, &sa) {
		return e.buildReply(env, "REJECTED", sa.reason, env.CommandCode, "ABORTED")
	}
	if len(startedAux) > 0 {
		code := "SEQUENCE_STEP_FAILED"
		var spErr doStatePersistErr
		if errors.As(err, &spErr) {
			code = "STATE_PERSIST_FAILED"
		}
		e.rollbackStartedAuxiliaries(plan, env, startedAux, code, err.Error(), ctx)
	}
	var prErr doPulseResetErr
	if errors.As(err, &prErr) {
		e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "PULSE_RESET_FAILED", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
			doDiagnosticData{
				Code:      "PULSE_RESET_FAILED",
				UnitCode:  plan.unitCode,
				CommandID: env.CommandID,
				Message:   fmt.Sprintf("%s: pulse coil reset failed on %s", plan.unitCode, env.CommandCode),
			})
		return e.buildReply(env, "FAILED", "PULSE_RESET_FAILED", env.CommandCode, "NOT_APPLIED")
	}
	var spErr doStatePersistErr
	if errors.As(err, &spErr) {
		return e.buildReply(env, "FAILED", "STATE_PERSIST_FAILED", env.CommandCode, "NOT_APPLIED")
	}
	return e.buildReply(env, "FAILED", "MODBUS_WRITE_FAILED", env.CommandCode, "NOT_APPLIED")
}

func (e *DOSequenceExecutor) rollbackStartedAuxiliaries(
	plan doUnitPlan,
	env CommandEnvelopeWire,
	startedAux []doAuxPart,
	failureCode string,
	failureReason string,
	seqCtx context.Context,
) {
	var rolledBack []doAuxPart
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
				DeviceID:        part.deviceKey,
				DeviceTimestamp: ts,
				Points:          []mqttUplinkPoint{{Code: "run_state", Value: "STOPPED", Quality: 0}},
			})
			etPts = append(etPts, mqttUplinkPoint{Code: part.channel, Value: 0, Quality: 0})
		}
		entries = append(entries, mqttUplinkDevice{DeviceID: e.deviceKey, DeviceTimestamp: ts, Points: etPts})
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
		doDiagnosticData{
			Code:                   eventCode,
			UnitCode:               plan.unitCode,
			CommandID:              env.CommandID,
			Message:                msg,
			FailureCode:            failureCode,
			FailureReason:          failureReason,
			RolledBackChannels:     rolledChannels,
			RollbackFailedChannels: rollbackFailed,
		})
}

func (e *DOSequenceExecutor) confirmChillerStoppedBeforeAuxShutdown(
	ctx context.Context,
	plan doUnitPlan,
	env CommandEnvelopeWire,
	checkAborted func() error,
) (bool, error) {
	if e.chillerPowerReader == nil || plan.meterDeviceKey == "" {
		return true, nil
	}
	readPower := func() (*float64, error) {
		if err := checkAborted(); err != nil {
			return nil, err
		}
		val, err := e.chillerPowerReader(ctx, plan.meterDeviceKey)
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

	for range doStopRecheckLimit {
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
			doDiagnosticData{
				Code:            "CHILLER_STOP_POWER_UNAVAILABLE_AUX_HELD",
				UnitCode:        plan.unitCode,
				CommandID:       env.CommandID,
				Message:         fmt.Sprintf("%s: aborted auxiliary shutdown because chiller meter power was unavailable (null) after %d checks", plan.unitCode, doStopRecheckLimit+1),
				StopThresholdKW: &threshold,
			})
		return false, nil
	}
	e.publishDiagnosticEvent(e.bgCtx, "CRITICAL", "CHILLER_STOP_UNCONFIRMED_AUX_HELD", plan.unitCode, plan.chillerDeviceID, env.CommandID, env.TraceID,
		doDiagnosticData{
			Code:            "CHILLER_STOP_UNCONFIRMED_AUX_HELD",
			UnitCode:        plan.unitCode,
			CommandID:       env.CommandID,
			Message:         fmt.Sprintf("%s: aborted auxiliary shutdown because chiller power remained %.2f kW (> %.2f kW)", plan.unitCode, *power, e.chillerStopPowerKW),
			PowerKW:         power,
			StopThresholdKW: &threshold,
		})
	return false, nil
}

func (e *DOSequenceExecutor) verifyRequiredCoilsClosed(
	ctx context.Context,
	plan doUnitPlan,
	env CommandEnvelopeWire,
	checkAborted func() error,
) (bool, error) {
	var openLabels []string
	var openChannels []string
	for _, part := range plan.auxiliaries {
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
		doDiagnosticData{
			Code:         "PRE_PULSE_COIL_OPEN",
			UnitCode:     plan.unitCode,
			CommandID:    env.CommandID,
			Message:      fmt.Sprintf("%s: aborted chiller start pulse because coils are open: %s", plan.unitCode, strings.Join(openLabels, ", ")),
			OpenChannels: openChannels,
		})
	return false, nil
}

func (e *DOSequenceExecutor) executeEmergencyDORelease(ctx context.Context, env CommandEnvelopeWire) CommandReplyWire {
	e.stateMu.Lock()
	for unitCode := range e.activeSequences {
		e.abortUnitSequenceLocked(unitCode, "EMERGENCY_DO_RELEASE")
	}
	e.stateMu.Unlock()

	_ = e.WaitForActiveSequences(ctx)

	writeFailures := 0
	for _, entry := range e.allCoils {
		if err := e.writeCoil(ctx, entry.Coil, false); err != nil {
			writeFailures++
			continue
		}
		e.stateMu.Lock()
		delete(e.pendingPulseCoils, entry.Coil)
		e.stateMu.Unlock()
	}
	persistFailed := e.persistCurrentState() != nil

	ts := e.nowMS()
	e.stateMu.Lock()
	entries := make([]mqttUplinkDevice, 0, len(e.allAuxParts)+1)
	for _, part := range e.allAuxParts {
		if v, known := e.knownCoilStates[part.coil]; known && !v {
			entries = append(entries, mqttUplinkDevice{
				DeviceID:        part.deviceKey,
				DeviceTimestamp: ts,
				Points:          []mqttUplinkPoint{{Code: "run_state", Value: "STOPPED", Quality: 0}},
			})
		}
	}
	var etPoints []mqttUplinkPoint
	if writeFailures == 0 {
		etPoints = append(etPoints, mqttUplinkPoint{Code: "run_state", Value: "STOPPED", Quality: 0})
	}
	for _, c := range e.allCoils {
		if v, known := e.knownCoilStates[c.Coil]; known && !v {
			etPoints = append(etPoints, mqttUplinkPoint{Code: c.Channel, Value: 0, Quality: 0})
		}
	}
	e.stateMu.Unlock()
	if len(etPoints) > 0 {
		entries = append(entries, mqttUplinkDevice{DeviceID: e.deviceKey, DeviceTimestamp: ts, Points: etPoints})
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

func (e *DOSequenceExecutor) executePulse(
	ctx context.Context,
	coil uint16,
	channel string,
	duration time.Duration,
	unitCode string,
	commandID string,
) error {
	e.stateMu.Lock()
	e.pendingPulseCoils[coil] = struct{}{}
	e.stateMu.Unlock()

	if err := e.persistCurrentState(); err != nil {
		e.stateMu.Lock()
		delete(e.pendingPulseCoils, coil)
		e.stateMu.Unlock()
		return doStatePersistErr{cause: err}
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
		e.logger.Error("do_pulse_reset_failed",
			"code", "PULSE_RESET_FAILED",
			"unit", unitCode,
			"command_id", commandID,
			"channel", channel,
			"coil", coil,
			"error", resetErr.Error(),
		)
		return doPulseResetErr{cause: resetErr}
	}

	e.stateMu.Lock()
	delete(e.pendingPulseCoils, coil)
	e.stateMu.Unlock()
	if err := e.persistCurrentState(); err != nil && writeStartErr == nil && sleepErr == nil {
		return doStatePersistErr{cause: err}
	}
	if writeStartErr != nil {
		return writeStartErr
	}
	e.emitTelemetryEntries(resetCtx, []mqttUplinkDevice{{
		DeviceID:        e.deviceKey,
		DeviceTimestamp: e.nowMS(),
		Points:          []mqttUplinkPoint{{Code: channel, Value: 0, Quality: 0}},
	}})
	return sleepErr
}

func (e *DOSequenceExecutor) resetPulseCoilWithRetry(ctx context.Context, coil uint16) error {
	var lastErr error
	for attempt := 1; attempt <= doPulseResetAttempts; attempt++ {
		if err := e.writeCoil(ctx, coil, false); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < doPulseResetAttempts {
			if err := e.sleep(ctx, time.Duration(attempt)*doPulseResetBackoff); err != nil {
				return err
			}
		}
	}
	return lastErr
}

func (e *DOSequenceExecutor) writeCoil(ctx context.Context, coil uint16, value bool) error {
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

func (e *DOSequenceExecutor) readCoil(ctx context.Context, coil uint16) (bool, error) {
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

func (e *DOSequenceExecutor) abortUnitSequenceLocked(unitCode string, reason string) bool {
	lc := e.activeSequences[unitCode]
	if lc == nil || lc.ctx.Err() != nil {
		return false
	}
	lc.cancel(doSequenceAbortErr{reason: reason})
	return true
}

func (e *DOSequenceExecutor) commitCommandAcceptedLocked(env CommandEnvelopeWire) {
	e.lastFenceByDevice[env.DeviceID] = env.ExecutionFence
	e.recordExecutedLocked(env.CommandID)
}

func (e *DOSequenceExecutor) recordExecutedLocked(commandID string) {
	if _, exists := e.executedIDSet[commandID]; exists {
		return
	}
	e.executedIDSet[commandID] = struct{}{}
	e.executedIDOrder = append(e.executedIDOrder, commandID)
	for len(e.executedIDOrder) > doMaxExecutedCmdIDs {
		oldest := e.executedIDOrder[0]
		e.executedIDOrder = e.executedIDOrder[1:]
		delete(e.executedIDSet, oldest)
	}
}

func (e *DOSequenceExecutor) snapshotLocked() DOExecutorPersistedState {
	coils := make([]uint16, 0, len(e.pendingPulseCoils))
	for c := range e.pendingPulseCoils {
		coils = append(coils, c)
	}
	sort.Slice(coils, func(i, j int) bool { return coils[i] < coils[j] })
	return DOExecutorPersistedState{
		LastSequence:       e.uplinkSeq,
		LastFenceByDevice:  maps.Clone(e.lastFenceByDevice),
		ExecutedCommandIDs: slices.Clone(e.executedIDOrder),
		PendingPulseCoils:  coils,
	}
}

func (e *DOSequenceExecutor) snapshotWithAcceptedLocked(env CommandEnvelopeWire) DOExecutorPersistedState {
	fences := maps.Clone(e.lastFenceByDevice)
	if fences == nil {
		fences = map[string]uint64{}
	}
	fences[env.DeviceID] = env.ExecutionFence

	ids := slices.Clone(e.executedIDOrder)
	if _, exists := e.executedIDSet[env.CommandID]; !exists {
		ids = append(ids, env.CommandID)
		if len(ids) > doMaxExecutedCmdIDs {
			ids = ids[len(ids)-doMaxExecutedCmdIDs:]
		}
	}
	coils := make([]uint16, 0, len(e.pendingPulseCoils))
	for c := range e.pendingPulseCoils {
		coils = append(coils, c)
	}
	sort.Slice(coils, func(i, j int) bool { return coils[i] < coils[j] })
	return DOExecutorPersistedState{
		LastSequence:       e.uplinkSeq,
		LastFenceByDevice:  fences,
		ExecutedCommandIDs: ids,
		PendingPulseCoils:  coils,
	}
}

func (e *DOSequenceExecutor) persistCurrentState() error {
	e.stateMu.Lock()
	snap := e.snapshotLocked()
	e.stateMu.Unlock()
	return e.writePersistedState(snap)
}

func (e *DOSequenceExecutor) loadPersistedState() (*DOExecutorPersistedState, error) {
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
	var loaded DOExecutorPersistedState
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, err
	}
	return &loaded, nil
}

func (e *DOSequenceExecutor) writePersistedState(snap DOExecutorPersistedState) error {
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

func (e *DOSequenceExecutor) nowMS() int64 {
	ms := e.now().UTC().UnixMilli()
	if ms < 1 {
		return 1
	}
	return ms
}

func (e *DOSequenceExecutor) nextUplinkSeq() uint64 {
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

func (e *DOSequenceExecutor) emitTelemetryEntries(ctx context.Context, devices []mqttUplinkDevice) {
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

func (e *DOSequenceExecutor) publishDiagnosticEvent(
	ctx context.Context,
	severity string,
	eventCode string,
	unitCode string,
	sourceID string,
	commandID string,
	traceID string,
	data doDiagnosticData,
) {
	e.logger.Warn("do_diagnostic_event", "code", eventCode, "unit", unitCode, "command_id", commandID, "message", data.Message)
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
		sourceID = e.deviceKey
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

func (e *DOSequenceExecutor) buildReply(env CommandEnvelopeWire, edgeStatus, reasonCode, requestedText, appliedText string) CommandReplyWire {
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
			WinnerControllerID: e.winnerControllerID,
			Cycle:              cycle,
		},
		ReasonCode:     reasonPtr,
		ExecutionFence: env.ExecutionFence,
	}
}

func isAbortUnitSequence(env CommandEnvelopeWire) bool {
	return env.ActuationMode == "ABORT_UNIT_SEQUENCE" || env.CommandCode == "ABORT_UNIT_SEQUENCE"
}

func (e *DOSequenceExecutor) resolveUnitCode(env CommandEnvelopeWire, allowAbortMode bool) (string, bool) {
	inferred, ok := e.chillerUnitByDev[env.DeviceID]
	if !ok {
		return "", false
	}
	if env.Channel != "" && env.Channel != inferred {
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

func matchesDirectRoute(env CommandEnvelopeWire, route doDirectRoute) bool {
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

func (e *DOSequenceExecutor) isEmergencyDORelease(env CommandEnvelopeWire) bool {
	if env.CommandCode != "STOP" {
		return false
	}
	matched := env.ActuationMode == "EMERGENCY_DO_RELEASE" || (env.DeviceID == e.deviceKey && env.PointCode == "emergency_stop_all")
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
