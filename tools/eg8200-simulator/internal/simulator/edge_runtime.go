package simulator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type EdgeCommandIntentRequest struct {
	CommandID   string
	DeviceID    string
	CommandCode string
	Params      map[string]float64
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

type EdgeCommandOutcome struct {
	CommandID          string
	Address            string
	Accepted           bool
	Code               string
	Requested          edgecontrol.Value
	Effective          *edgecontrol.Value
	AppliedValue       *edgecontrol.Value
	ConstraintReasons  []edgecontrol.ConstraintEvidence
	WinnerControllerID string
	Cycle              uint64
}

type edgeCommandBinding struct {
	point   PointConfig
	address string
}

type pendingEdgeCommand struct {
	intentID  string
	address   string
	expiresAt time.Time
	outcome   chan EdgeCommandOutcome
}

type EdgeControlCycleResult struct {
	PollResults       []edgecontrol.DevicePollResult
	Cycle             edgecontrol.CycleResult
	TelemetrySnapshot Snapshot
	TimedataRecords   int
	TimedataError     error
}

type EdgeControlRuntime struct {
	mu sync.Mutex

	config       Config
	plant        *Plant
	runtime      *edgecontrol.Runtime
	capabilities *edgecontrol.CapabilityRegistry
	components   *edgecontrol.ComponentRegistry
	host         *edgecontrol.DeviceHost
	intentStore  *edgecontrol.IntentStore
	writer       *edgecontrol.DeviceOutputWriter
	cycle        *edgecontrol.Cycle
	timedata     edgecontrol.TimedataRecorder

	commandByKey     map[string]edgeCommandBinding
	leaseByAddress   map[string]bool
	bindingsByDevice map[string]map[edgecontrol.SemanticChannel]string
	pendingByAddress map[string]pendingEdgeCommand
}

func NewEdgeControlRuntime(config Config, plant *Plant) (*EdgeControlRuntime, error) {
	if plant == nil {
		return nil, errors.New("Edge Control Runtime requires Plant")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	runtime := edgecontrol.NewRuntime()
	capabilities, err := edgecontrol.NewStandardCapabilityRegistry()
	if err != nil {
		return nil, err
	}
	components, err := edgecontrol.NewComponentRegistry(runtime, capabilities)
	if err != nil {
		return nil, err
	}
	host, err := edgecontrol.NewDeviceHost(runtime, components)
	if err != nil {
		return nil, err
	}
	adapters, err := newSimulatedDeviceAdapters(config, plant)
	if err != nil {
		return nil, err
	}
	bindingsByDevice := make(map[string]map[edgecontrol.SemanticChannel]string, len(adapters))
	for _, adapter := range adapters {
		if err := host.RegisterAdapter(adapter); err != nil {
			return nil, err
		}
		component := adapter.Component()
		bindingsByDevice[component.ID] = cloneSemanticBindings(component.ChannelBindings)
	}
	intentStore, err := edgecontrol.NewIntentStore(runtime)
	if err != nil {
		return nil, err
	}
	writer, err := edgecontrol.NewDeviceOutputWriter(host)
	if err != nil {
		return nil, err
	}

	commandByKey := map[string]edgeCommandBinding{}
	leaseByAddress := map[string]bool{}
	staleAfterByAddress := make(map[string]time.Duration, len(config.Points))
	limits := make([]edgeNumericLimit, 0)
	for _, point := range config.Points {
		staleAfter, _ := time.ParseDuration(point.StaleAfter) // config.Validate already owns interval validation.
		staleAfterByAddress[point.DeviceID+"/"+point.PointCode] = staleAfter
		if point.PointType != "COMMAND" {
			continue
		}
		capability, _ := point.SourceMetadata["capability"].(string)
		if strings.TrimSpace(capability) == "" {
			return nil, fmt.Errorf("COMMAND point %s has no capability", point.PointCode)
		}
		address := point.DeviceID + "/" + point.PointCode
		key := commandBindingKey(point.DeviceID, capability)
		if _, duplicate := commandByKey[key]; duplicate {
			return nil, fmt.Errorf("device %s exposes duplicate command capability %s", point.DeviceID, capability)
		}
		commandByKey[key] = edgeCommandBinding{point: point, address: address}
		leaseByAddress[address] = controlKind(point) == "NUMBER"
		if controlKind(point) == "NUMBER" {
			minimum, minimumOK := point.SourceMetadata["minimum"].(float64)
			maximum, maximumOK := point.SourceMetadata["maximum"].(float64)
			if !minimumOK || !maximumOK {
				return nil, fmt.Errorf("numeric COMMAND point %s has invalid limits", point.PointCode)
			}
			limits = append(limits, edgeNumericLimit{address: address, minimum: minimum, maximum: maximum})
		}
	}
	intentController, err := edgecontrol.NewIntentController("cloud-command-intent", intentStore)
	if err != nil {
		return nil, err
	}
	limitController := &edgeLimitController{id: "capability-limits", limits: limits}
	safetyController := &edgePlantSafetyController{
		id: "plant-safety-interlock", bindings: bindingsByDevice, staleAfterByAddress: staleAfterByAddress, plant: config.Plant,
	}
	scheduler, err := edgecontrol.NewScheduler([]edgecontrol.ControllerBinding{
		{Priority: 0, Controller: safetyController},
		{Priority: 10, Controller: limitController},
		{Priority: 100, Controller: intentController},
	})
	if err != nil {
		return nil, err
	}
	cycle, err := edgecontrol.NewCycle(runtime, scheduler, writer)
	if err != nil {
		return nil, err
	}
	return &EdgeControlRuntime{
		config: config, plant: plant, runtime: runtime, capabilities: capabilities, components: components, host: host,
		intentStore: intentStore, writer: writer, cycle: cycle, commandByKey: commandByKey, leaseByAddress: leaseByAddress,
		bindingsByDevice: bindingsByDevice, pendingByAddress: map[string]pendingEdgeCommand{},
	}, nil
}

func cloneSemanticBindings(input map[edgecontrol.SemanticChannel]string) map[edgecontrol.SemanticChannel]string {
	out := make(map[edgecontrol.SemanticChannel]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func commandBindingKey(deviceID, capability string) string {
	return strings.TrimSpace(deviceID) + "\x00" + strings.TrimSpace(capability)
}

func (runtime *EdgeControlRuntime) Manifest(revision string, at time.Time) (edgecontrol.EdgeManifest, error) {
	if runtime == nil {
		return edgecontrol.EdgeManifest{}, errors.New("Edge Control Runtime is unavailable")
	}
	return runtime.components.Manifest(runtime.config.GatewayID, revision, at)
}

func (runtime *EdgeControlRuntime) AttachTimedata(recorder edgecontrol.TimedataRecorder) error {
	if runtime == nil {
		return errors.New("Edge Control Runtime is unavailable")
	}
	if recorder == nil {
		return errors.New("Edge Timedata recorder is required")
	}
	runtime.mu.Lock()
	runtime.timedata = recorder
	runtime.mu.Unlock()
	return nil
}

func (runtime *EdgeControlRuntime) SubmitCommand(request EdgeCommandIntentRequest) (<-chan EdgeCommandOutcome, error) {
	if runtime == nil {
		return nil, errors.New("Edge Control Runtime is unavailable")
	}
	request.CommandID = strings.TrimSpace(request.CommandID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.CommandCode = strings.TrimSpace(request.CommandCode)
	if request.CommandID == "" || request.DeviceID == "" || request.CommandCode == "" {
		return nil, errors.New("command ID, device ID and command code are required")
	}
	binding, ok := runtime.commandByKey[commandBindingKey(request.DeviceID, request.CommandCode)]
	if !ok {
		return nil, fmt.Errorf("device %s does not expose command capability %s", request.DeviceID, request.CommandCode)
	}
	requested, err := requestedEdgeValue(binding.point, request.Params)
	if err != nil {
		return nil, err
	}
	intent := edgecontrol.ControlIntent{
		ID: request.CommandID, Address: binding.address, Requested: requested,
		IssuedAt: request.IssuedAt.UTC(), ExpiresAt: request.ExpiresAt.UTC(), Source: "CLOUD_COMMAND",
	}
	runtime.mu.Lock()
	if pending, exists := runtime.pendingByAddress[binding.address]; exists && pending.intentID != request.CommandID {
		runtime.mu.Unlock()
		return nil, fmt.Errorf("command channel %s already has in-flight command %s", binding.address, pending.intentID)
	}
	runtime.mu.Unlock()
	if _, err := runtime.intentStore.Put(intent); err != nil {
		return nil, err
	}
	outcome := make(chan EdgeCommandOutcome, 1)
	runtime.mu.Lock()
	if existing, exists := runtime.pendingByAddress[binding.address]; exists && existing.intentID == request.CommandID {
		runtime.mu.Unlock()
		return existing.outcome, nil
	}
	runtime.pendingByAddress[binding.address] = pendingEdgeCommand{intentID: request.CommandID, address: binding.address, expiresAt: request.ExpiresAt.UTC(), outcome: outcome}
	runtime.mu.Unlock()
	return outcome, nil
}

func requestedEdgeValue(point PointConfig, params map[string]float64) (edgecontrol.Value, error) {
	if controlKind(point) == "ACTION" {
		if len(params) != 0 {
			return edgecontrol.Value{}, errors.New("ACTION command does not accept parameters")
		}
		return edgecontrol.BooleanValue(true), nil
	}
	parameterKey, _ := point.SourceMetadata["parameterKey"].(string)
	if parameterKey == "" || len(params) != 1 {
		return edgecontrol.Value{}, errors.New("numeric command requires exactly one parameter")
	}
	value, ok := params[parameterKey]
	if !ok {
		return edgecontrol.Value{}, fmt.Errorf("numeric command requires parameter %s", parameterKey)
	}
	return edgecontrol.DoubleValue(value), nil
}

func (runtime *EdgeControlRuntime) CancelCommand(commandID, code string) bool {
	if runtime == nil {
		return false
	}
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return false
	}
	if strings.TrimSpace(code) == "" {
		code = "CANCELLED"
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	for _, pending := range runtime.pendingByAddress {
		if pending.intentID != commandID {
			continue
		}
		runtime.finishPendingLocked(pending, EdgeCommandOutcome{CommandID: commandID, Address: pending.address, Accepted: false, Code: code}, true)
		return true
	}
	return false
}

func (runtime *EdgeControlRuntime) RunCycle(ctx context.Context, at time.Time) EdgeControlCycleResult {
	pollResults := runtime.host.PollOnce(ctx, at)
	cycleResult := runtime.cycle.RunOnce(ctx, at)
	telemetrySnapshot := runtime.snapshotFromProcessImage(cycleResult.Image)
	runtime.mu.Lock()
	timedata := runtime.timedata
	runtime.mu.Unlock()
	timedataRecords := 0
	var timedataErr error
	if timedata != nil {
		timedataRecords, timedataErr = timedata.RecordImage(cycleResult.Image)
	}
	runtime.resolvePending(at, cycleResult)
	return EdgeControlCycleResult{
		PollResults: pollResults, Cycle: cycleResult, TelemetrySnapshot: telemetrySnapshot,
		TimedataRecords: timedataRecords, TimedataError: timedataErr,
	}
}

func (runtime *EdgeControlRuntime) snapshotFromProcessImage(image edgecontrol.ProcessImage) Snapshot {
	snapshot := Snapshot{ObservedAt: image.At(), Devices: map[string]DeviceTelemetry{}}
	for _, point := range runtime.config.Points {
		if point.PointType == "COMMAND" {
			continue
		}
		channel, ok := image.Get(point.DeviceID + "/" + point.PointCode)
		if !ok || !channel.HasValue {
			continue
		}
		value, ok := edgeTelemetryValue(channel.Sample.Value)
		if !ok {
			continue
		}
		device := snapshot.Devices[point.DeviceID]
		if device == nil {
			device = DeviceTelemetry{}
			snapshot.Devices[point.DeviceID] = device
		}
		device[point.SourceKey] = value
	}
	return snapshot
}

func edgeTelemetryValue(value edgecontrol.Value) (any, bool) {
	switch value.Type {
	case edgecontrol.DataTypeBoolean:
		return value.Boolean, true
	case edgecontrol.DataTypeString:
		return value.String, true
	case edgecontrol.DataTypeInteger:
		return value.Integer, true
	case edgecontrol.DataTypeLong:
		return value.Long, true
	case edgecontrol.DataTypeFloat:
		return value.Float, true
	case edgecontrol.DataTypeDouble:
		return value.Double, true
	default:
		return nil, false
	}
}

func (runtime *EdgeControlRuntime) resolvePending(at time.Time, result edgecontrol.CycleResult) {
	decisionByAddress := make(map[string]edgecontrol.Decision, len(result.Decisions))
	for _, decision := range result.Decisions {
		decisionByAddress[decision.Address] = decision
	}
	writeByAddress := map[string]edgecontrol.DeviceWriteResult{}
	for _, writeResult := range runtime.writer.LastResults() {
		writeByAddress[writeResult.Address] = writeResult
	}

	runtime.mu.Lock()
	addresses := make([]string, 0, len(runtime.pendingByAddress))
	for address := range runtime.pendingByAddress {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		pending := runtime.pendingByAddress[address]
		decision, decided := decisionByAddress[address]
		if !decided {
			if !at.Before(pending.expiresAt) {
				runtime.finishPendingLocked(pending, EdgeCommandOutcome{CommandID: pending.intentID, Address: address, Accepted: false, Code: "EXPIRED", Cycle: result.Image.Cycle()}, true)
			}
			continue
		}
		winner := decision.ControllerID
		if !decision.Accepted && len(decision.ConstraintReasons) > 0 && decision.ConstraintReasons[0].ControllerID != "" {
			winner = decision.ConstraintReasons[0].ControllerID
		}
		outcome := EdgeCommandOutcome{
			CommandID: pending.intentID, Address: address, Accepted: decision.Accepted, Requested: decision.Requested,
			Effective: cloneEdgeValuePointer(decision.Effective), ConstraintReasons: append([]edgecontrol.ConstraintEvidence(nil), decision.ConstraintReasons...),
			WinnerControllerID: winner, Cycle: result.Image.Cycle(),
		}
		if !decision.Accepted {
			outcome.Code = "CONTROL_DENIED"
			if len(decision.ConstraintReasons) > 0 && decision.ConstraintReasons[0].Reason != "" {
				outcome.Code = decision.ConstraintReasons[0].Reason
			}
			runtime.finishPendingLocked(pending, outcome, true)
			continue
		}
		writeResult, wrote := writeByAddress[address]
		if !wrote {
			outcome.Accepted = false
			outcome.Code = "DEVICE_WRITE_RESULT_MISSING"
			if result.OutputError != nil {
				outcome.Code = "DEVICE_WRITE_FAILED"
			}
			runtime.finishPendingLocked(pending, outcome, true)
			continue
		}
		outcome.Accepted = writeResult.Success
		outcome.Code = writeResult.Code
		outcome.AppliedValue = cloneEdgeValuePointer(writeResult.AppliedValue)
		revoke := !writeResult.Success || !runtime.leaseByAddress[address]
		runtime.finishPendingLocked(pending, outcome, revoke)
	}
	runtime.mu.Unlock()
}

func cloneEdgeValuePointer(value *edgecontrol.Value) *edgecontrol.Value {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (runtime *EdgeControlRuntime) finishPendingLocked(pending pendingEdgeCommand, outcome EdgeCommandOutcome, revokeIntent bool) {
	delete(runtime.pendingByAddress, pending.address)
	if revokeIntent {
		runtime.intentStore.Revoke(pending.intentID)
	}
	select {
	case pending.outcome <- outcome:
	default:
	}
	close(pending.outcome)
}

type edgeNumericLimit struct {
	address string
	minimum float64
	maximum float64
}

type edgeLimitController struct {
	id     string
	limits []edgeNumericLimit
}

func (controller *edgeLimitController) ID() string { return controller.id }
func (controller *edgeLimitController) Run(_ context.Context, _ edgecontrol.ProcessImage, plan *edgecontrol.ControlPlan) error {
	for _, limit := range controller.limits {
		minimum, maximum := limit.minimum, limit.maximum
		if _, err := plan.ConstrainNumber(limit.address, controller.id, "DEVICE_CAPABILITY_LIMIT", &minimum, &maximum); err != nil && !errors.Is(err, edgecontrol.ErrConstraintConflict) {
			return err
		}
	}
	return nil
}

type edgePlantSafetyController struct {
	id                  string
	bindings            map[string]map[edgecontrol.SemanticChannel]string
	staleAfterByAddress map[string]time.Duration
	plant               PlantConfig
}

func (controller *edgePlantSafetyController) ID() string { return controller.id }
func (controller *edgePlantSafetyController) Run(_ context.Context, image edgecontrol.ProcessImage, plan *edgecontrol.ControlPlan) error {
	for deviceID, bindings := range controller.bindings {
		startAddress := bindings[edgecontrol.SemanticStartCommand]
		if startAddress == "" {
			continue
		}
		faultAddress := bindings[edgecontrol.SemanticFaultCode]
		fault, state := edgeTextValue(image, faultAddress, controller.staleAfterByAddress[faultAddress])
		if state != "" {
			_, _ = plan.Deny(startAddress, controller.id, state)
			continue
		}
		if strings.TrimSpace(fault) != "" {
			_, _ = plan.Deny(startAddress, controller.id, "FAULT_ACTIVE")
			continue
		}
		if deviceID != controller.plant.Chiller.ID {
			continue
		}
		for _, dependency := range []string{controller.plant.ChilledWaterPump.ID, controller.plant.CoolingWaterPump.ID, controller.plant.CoolingTower.ID} {
			runAddress := controller.bindings[dependency][edgecontrol.SemanticRunState]
			runState, state := edgeTextValue(image, runAddress, controller.staleAfterByAddress[runAddress])
			if state == "SAFETY_STATE_STALE" {
				_, _ = plan.Deny(startAddress, controller.id, state)
				break
			}
			if state != "" || runState != "RUNNING" {
				_, _ = plan.Deny(startAddress, controller.id, "INTERLOCK_OPEN")
				break
			}
		}
	}
	return nil
}

func edgeTextValue(image edgecontrol.ProcessImage, address string, staleAfter time.Duration) (string, string) {
	if address == "" {
		return "", "SAFETY_STATE_UNAVAILABLE"
	}
	snapshot, ok := image.Get(address)
	if !ok || !snapshot.HasValue || snapshot.Sample.Quality == edgecontrol.QualityUnavailable || snapshot.Sample.Value.Type != edgecontrol.DataTypeString {
		return "", "SAFETY_STATE_UNAVAILABLE"
	}
	if staleAfter > 0 && image.At().Sub(snapshot.Sample.ObservedAt) > staleAfter {
		return "", "SAFETY_STATE_STALE"
	}
	return snapshot.Sample.Value.String, ""
}
