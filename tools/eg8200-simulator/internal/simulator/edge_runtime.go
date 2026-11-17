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

// Publishable reports whether the cycle switched a process image. A cycle halted by a clock
// regression (the host clock stepped behind the previous cycle) has none, so its telemetry is
// empty; the next cycle past the previous one publishes again.
func (result EdgeControlCycleResult) Publishable() bool {
	return result.Cycle.ClockError == nil
}

type EdgeControlRuntime struct {
	mu       sync.Mutex
	configMu sync.Mutex

	config           Config
	host             *edgecontrol.Host
	intentStore      *edgecontrol.IntentStore
	timedata         *edgecontrol.FileTimedata
	autonomyPolicy   edgecontrol.AutonomyPolicyConfig
	safetyController *edgePlantSafetyController
	hydInterlock     *edgecontrol.HydraulicInterlockController
	autonomyFallback *edgecontrol.AutonomousFallbackController

	driverTransportFactory func(edgecontrol.DeclarativeModbusProfile) (edgecontrol.ModbusRegisterTransport, error)

	bindingsByDevice map[string]map[edgecontrol.SemanticChannel]string
	commandByKey     map[string]edgeCommandBinding
	addressByPointID map[string]string
	leaseByAddress   map[string]bool
	pendingByAddress map[string]pendingEdgeCommand
}

func NewEdgeControlRuntime(config Config, plant *Plant) (*EdgeControlRuntime, error) {
	if plant == nil {
		return nil, errors.New("edge control runtime requires Plant")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	adapters, err := newSimulatedDeviceAdapters(config, plant)
	if err != nil {
		return nil, err
	}
	return newEdgeControlRuntime(config, adapters, true)
}

// NewATV630EdgeControlRuntime composes the production Edge Host with the
// production ATV630 DeviceAdapter. It deliberately has no Plant dependency;
// all device state crosses the supplied Modbus transport boundary.
func NewATV630EdgeControlRuntime(config Config, transport edgecontrol.ModbusRegisterTransport, unitID uint8) (*EdgeControlRuntime, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	edgeConfig, pointIDs, err := atv630EdgeConfig(config)
	if err != nil {
		return nil, err
	}
	adapter, err := edgecontrol.NewATV630DeviceAdapter(edgecontrol.ATV630DeviceAdapterConfig{
		ComponentID: config.Plant.ChilledWaterPump.ID,
		Alias:       "CHWP-01 ATV630",
		UnitID:      unitID,
		Transport:   transport,
		PointIDs:    pointIDs,
	})
	if err != nil {
		return nil, err
	}
	return newEdgeControlRuntime(edgeConfig, []edgecontrol.DeviceAdapter{adapter}, false)
}

func newEdgeControlRuntime(config Config, adapters []edgecontrol.DeviceAdapter, includePlantSafety bool) (*EdgeControlRuntime, error) {
	host, err := edgecontrol.NewHost()
	if err != nil {
		return nil, err
	}
	bindingsByDevice := make(map[string]map[edgecontrol.SemanticChannel]string, len(adapters))
	addressByPointID := map[string]string{}
	for _, adapter := range adapters {
		if err := host.RegisterAdapter(adapter); err != nil {
			return nil, err
		}
		component := adapter.Component()
		bindingsByDevice[component.ID] = cloneSemanticBindings(component.ChannelBindings)
		for _, channel := range adapter.Channels() {
			if channel.PointID != "" {
				addressByPointID[channel.PointID] = channel.Address()
			}
		}
	}
	intentStore := host.IntentStore()

	commandByKey := map[string]edgeCommandBinding{}
	leaseByAddress := map[string]bool{}
	staleAfterByAddress := make(map[string]time.Duration, len(config.Points))
	limits := make([]edgeNumericLimit, 0)
	for _, point := range config.Points {
		address := point.DeviceID + "/" + point.PointCode
		if mapped, ok := addressByPointID[point.PointID]; ok {
			address = mapped
		}
		staleAfter, _ := time.ParseDuration(point.StaleAfter) // config.Validate already owns interval validation.
		staleAfterByAddress[address] = staleAfter
		if point.PointType != "COMMAND" {
			continue
		}
		capability, _ := point.SourceMetadata["capability"].(string)
		if strings.TrimSpace(capability) == "" {
			return nil, fmt.Errorf("COMMAND point %s has no capability", point.PointCode)
		}
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
	autonomyPolicy := edgecontrol.DefaultAutonomyPolicyConfig()
	limitController := &edgeLimitController{id: "capability-limits", limits: limits}
	controllers := []edgecontrol.ControllerBinding{
		{Priority: 10, Controller: limitController},
		{Priority: 100, Controller: intentController},
	}
	var safetyController *edgePlantSafetyController
	var hydInterlock *edgecontrol.HydraulicInterlockController
	var autonomyFallback *edgecontrol.AutonomousFallbackController
	if includePlantSafety {
		safetyController = &edgePlantSafetyController{
			id: "plant-safety-interlock", bindings: cloneAllSemanticBindings(bindingsByDevice), staleAfterByAddress: staleAfterByAddress, plant: config.Plant,
		}
		chillers, chwPumps, freqAddrs, fallbackChillers, fallbackPumps := buildAutonomyEquipmentBindings(config.Plant, bindingsByDevice)
		var err error
		hydInterlock, err = edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
			Chillers:           chillers,
			ChilledWaterPumps:  chwPumps,
			PumpFrequencyAddrs: freqAddrs,
			Policy:             autonomyPolicy,
		})
		if err != nil {
			return nil, err
		}
		autonomyFallback, err = edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{
			Chillers: fallbackChillers,
			Pumps:    fallbackPumps,
			Policy:   autonomyPolicy,
		})
		if err != nil {
			return nil, err
		}
		controllers = []edgecontrol.ControllerBinding{
			{Priority: 0, Controller: safetyController},
			{Priority: 5, Controller: hydInterlock},
			{Priority: 10, Controller: limitController},
			{Priority: 50, Controller: autonomyFallback},
			{Priority: 100, Controller: intentController},
		}
	}
	if err := host.Start(controllers); err != nil {
		return nil, err
	}
	return &EdgeControlRuntime{
		config:           config,
		host:             host,
		intentStore:      intentStore,
		autonomyPolicy:   autonomyPolicy,
		safetyController: safetyController,
		hydInterlock:     hydInterlock,
		autonomyFallback: autonomyFallback,
		bindingsByDevice: bindingsByDevice,
		commandByKey:     commandByKey,
		addressByPointID: addressByPointID,
		leaseByAddress:   leaseByAddress,
		pendingByAddress: map[string]pendingEdgeCommand{},
	}, nil
}

func ATV630EdgeConfig(config Config) (Config, error) {
	edgeConfig, _, err := atv630EdgeConfig(config)
	return edgeConfig, err
}

func atv630EdgeConfig(config Config) (Config, edgecontrol.ATV630PointIDs, error) {
	deviceID := config.Plant.ChilledWaterPump.ID
	wanted := map[string]struct{}{
		"run_state": {}, "fault_code": {}, "frequency": {},
		"start": {}, "stop": {}, "reset_fault": {}, "set_frequency": {},
	}
	points := make([]PointConfig, 0, len(wanted))
	pointIDByCode := make(map[string]string, len(wanted))
	for _, point := range config.Points {
		if point.DeviceID != deviceID {
			continue
		}
		if _, ok := wanted[point.PointCode]; !ok {
			continue
		}
		points = append(points, point)
		pointIDByCode[point.PointCode] = point.PointID
	}
	if len(points) != len(wanted) {
		return Config{}, edgecontrol.ATV630PointIDs{}, errors.New("ATV630 Edge config requires CHWP run_state, fault_code, frequency and four command points")
	}
	config.Points = points
	return config, edgecontrol.ATV630PointIDs{
		RunState: pointIDByCode["run_state"], FaultCode: pointIDByCode["fault_code"], Frequency: pointIDByCode["frequency"],
		StartCommand: pointIDByCode["start"], StopCommand: pointIDByCode["stop"], ResetFaultCommand: pointIDByCode["reset_fault"],
		FrequencySetpoint: pointIDByCode["set_frequency"],
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
		return edgecontrol.EdgeManifest{}, errors.New("edge control runtime is unavailable")
	}
	return runtime.host.Manifest(runtime.config.GatewayID, revision, at)
}

func (runtime *EdgeControlRuntime) AttachTimedata(recorder edgecontrol.TimedataRecorder) error {
	if runtime == nil {
		return errors.New("edge control runtime is unavailable")
	}
	if recorder == nil {
		return errors.New("edge timedata recorder is required")
	}
	if err := runtime.host.AttachTimedata(recorder); err != nil {
		return err
	}
	runtime.mu.Lock()
	if fileStore, ok := recorder.(*edgecontrol.FileTimedata); ok {
		runtime.timedata = fileStore
	}
	runtime.mu.Unlock()
	return nil
}

// Timedata returns the attached FileTimedata instance when configured.
func (runtime *EdgeControlRuntime) Timedata() *edgecontrol.FileTimedata {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.timedata
}

// Host returns the underlying edgecontrol.Host.
func (runtime *EdgeControlRuntime) Host() *edgecontrol.Host {
	if runtime == nil {
		return nil
	}
	return runtime.host
}

// ChannelDescriptor returns the descriptor for a registered channel address.
func (runtime *EdgeControlRuntime) ChannelDescriptor(address string) (edgecontrol.ChannelDescriptor, bool) {
	if runtime == nil || runtime.host == nil {
		return edgecontrol.ChannelDescriptor{}, false
	}
	return runtime.host.ChannelDescriptor(address)
}

// SetDriverTransportFactory injects a custom ModbusRegisterTransport builder
// (primarily for deterministic testing of shadow hot-reload).
func (runtime *EdgeControlRuntime) SetDriverTransportFactory(factory func(edgecontrol.DeclarativeModbusProfile) (edgecontrol.ModbusRegisterTransport, error)) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.driverTransportFactory = factory
	runtime.mu.Unlock()
}

// NotifyCloudContact updates the AutonomousFallbackController connection state.
func (runtime *EdgeControlRuntime) NotifyCloudContact(connected bool, at time.Time) {
	if runtime == nil || runtime.autonomyFallback == nil {
		return
	}
	runtime.autonomyFallback.SetCloudConnected(connected, at)
}

// AutonomyPolicy returns the active autonomy policy configuration.
func (runtime *EdgeControlRuntime) AutonomyPolicy() edgecontrol.AutonomyPolicyConfig {
	if runtime == nil {
		return edgecontrol.DefaultAutonomyPolicyConfig()
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.autonomyPolicy
}

// HydraulicInterlockStatus returns the latest hydraulic safety interlock snapshot.
func (runtime *EdgeControlRuntime) HydraulicInterlockStatus() (edgecontrol.HydraulicInterlockStatus, bool) {
	if runtime == nil || runtime.hydInterlock == nil {
		return edgecontrol.HydraulicInterlockStatus{}, false
	}
	return runtime.hydInterlock.Status(), true
}

// AutonomousFallbackStatus returns the autonomous fallback status evaluated at at.
func (runtime *EdgeControlRuntime) AutonomousFallbackStatus(at time.Time) (edgecontrol.AutonomousFallbackStatus, bool) {
	if runtime == nil || runtime.autonomyFallback == nil {
		return edgecontrol.AutonomousFallbackStatus{}, false
	}
	return runtime.autonomyFallback.StatusAt(at), true
}

// ApplyAutonomyPolicy validates and hot-updates hydraulic interlock limits and
// offline autonomous fallback settings without restarting the runtime.
func (runtime *EdgeControlRuntime) ApplyAutonomyPolicy(policy edgecontrol.AutonomyPolicyConfig) error {
	if runtime == nil {
		return errors.New("edge control runtime is unavailable")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	runtime.configMu.Lock()
	defer runtime.configMu.Unlock()

	if runtime.hydInterlock != nil {
		if err := runtime.hydInterlock.UpdatePolicy(policy); err != nil {
			return err
		}
	}
	if runtime.autonomyFallback != nil {
		if err := runtime.autonomyFallback.UpdatePolicy(policy); err != nil {
			return err
		}
	}
	runtime.mu.Lock()
	runtime.autonomyPolicy = policy
	runtime.mu.Unlock()
	return nil
}

// ReplaceOrRegisterDevice hot-replaces or registers a DeviceAdapter and refreshes
// runtime channel mappings and controller bindings without dropping unrelated devices.
func (runtime *EdgeControlRuntime) ReplaceOrRegisterDevice(adapter edgecontrol.DeviceAdapter) error {
	if runtime == nil || adapter == nil {
		return errors.New("edge control runtime and adapter are required")
	}
	runtime.configMu.Lock()
	defer runtime.configMu.Unlock()
	return runtime.replaceOrRegisterDeviceLocked(adapter)
}

func (runtime *EdgeControlRuntime) replaceOrRegisterDeviceLocked(adapter edgecontrol.DeviceAdapter) error {
	if err := runtime.host.ReplaceOrRegisterDevice(adapter); err != nil {
		return err
	}
	descriptor := adapter.Component()
	channels := adapter.Channels()

	runtime.mu.Lock()
	if runtime.bindingsByDevice == nil {
		runtime.bindingsByDevice = map[string]map[edgecontrol.SemanticChannel]string{}
	}
	runtime.bindingsByDevice[descriptor.ID] = cloneSemanticBindings(descriptor.ChannelBindings)
	channelByID := make(map[string]edgecontrol.ChannelDescriptor, len(channels))
	for _, ch := range channels {
		runtime.addressByPointID[ch.PointID] = ch.Address()
		channelByID[ch.ChannelID] = ch
	}
	for _, pt := range runtime.config.Points {
		if pt.DeviceID != descriptor.ID {
			continue
		}
		if ch, ok := channelByID[pt.PointCode]; ok {
			addr := ch.Address()
			runtime.addressByPointID[pt.PointID] = addr
			if pt.PointType == "COMMAND" {
				if capability, _ := pt.SourceMetadata["capability"].(string); strings.TrimSpace(capability) != "" {
					runtime.commandByKey[commandBindingKey(pt.DeviceID, capability)] = edgeCommandBinding{point: pt, address: addr}
					runtime.leaseByAddress[addr] = controlKind(pt) == "NUMBER"
				}
			}
		}
	}
	bindingsCopy := cloneAllSemanticBindings(runtime.bindingsByDevice)
	plant := runtime.config.Plant
	safety := runtime.safetyController
	hyd := runtime.hydInterlock
	fallback := runtime.autonomyFallback
	runtime.mu.Unlock()

	if safety != nil {
		safety.UpdateBindings(bindingsCopy)
	}
	if hyd != nil || fallback != nil {
		chillers, chwPumps, freqAddrs, fallbackChillers, fallbackPumps := buildAutonomyEquipmentBindings(plant, bindingsCopy)
		if hyd != nil {
			hyd.UpdateBindings(chillers, chwPumps, freqAddrs)
		}
		if fallback != nil {
			fallback.UpdateTargets(fallbackChillers, fallbackPumps)
		}
	}
	return nil
}

func (runtime *EdgeControlRuntime) buildDeclarativeAdapter(profile edgecontrol.DeclarativeModbusProfile) (*edgecontrol.DeclarativeModbusAdapter, error) {
	runtime.mu.Lock()
	factory := runtime.driverTransportFactory
	runtime.mu.Unlock()

	var transport edgecontrol.ModbusRegisterTransport
	if factory != nil {
		var err error
		transport, err = factory(profile)
		if err != nil {
			return nil, fmt.Errorf("driver %s transport: %w", profile.ComponentID, err)
		}
	}
	adapter, err := edgecontrol.NewDeclarativeModbusAdapter(profile, transport)
	if err != nil {
		if transport != nil && factory != nil {
			if closer, ok := transport.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		return nil, fmt.Errorf("driver %s: %w", profile.ComponentID, err)
	}
	return adapter, nil
}

// ApplyDriverProfile validates and hot-replaces a single declarative Modbus adapter
// without restarting the edge runtime or dropping unrelated devices.
func (runtime *EdgeControlRuntime) ApplyDriverProfile(profile edgecontrol.DeclarativeModbusProfile) error {
	if runtime == nil {
		return errors.New("edge control runtime is unavailable")
	}
	runtime.configMu.Lock()
	defer runtime.configMu.Unlock()

	adapter, err := runtime.buildDeclarativeAdapter(profile)
	if err != nil {
		return err
	}
	if err := runtime.replaceOrRegisterDeviceLocked(adapter); err != nil {
		_ = adapter.Close()
		return err
	}
	return nil
}

// ApplyDriverConfigs validates and hot-reloads declarative Modbus adapters for all
// given profiles without restarting the edge runtime.
func (runtime *EdgeControlRuntime) ApplyDriverConfigs(profiles []edgecontrol.DeclarativeModbusProfile) error {
	if runtime == nil {
		return errors.New("edge control runtime is unavailable")
	}
	runtime.configMu.Lock()
	defer runtime.configMu.Unlock()

	seenDevices := make(map[string]struct{}, len(profiles))
	adapters := make([]*edgecontrol.DeclarativeModbusAdapter, 0, len(profiles))
	closeAdapters := func() {
		for _, a := range adapters {
			_ = a.Close()
		}
	}

	for _, profile := range profiles {
		if _, dup := seenDevices[profile.ComponentID]; dup {
			closeAdapters()
			return fmt.Errorf("duplicate driver config deviceKey %q", profile.ComponentID)
		}
		seenDevices[profile.ComponentID] = struct{}{}
		adapter, err := runtime.buildDeclarativeAdapter(profile)
		if err != nil {
			closeAdapters()
			return err
		}
		adapters = append(adapters, adapter)
	}

	var firstErr error
	for _, adapter := range adapters {
		if err := runtime.replaceOrRegisterDeviceLocked(adapter); err != nil {
			_ = adapter.Close()
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (runtime *EdgeControlRuntime) SubmitCommand(request EdgeCommandIntentRequest) (<-chan EdgeCommandOutcome, error) {
	if runtime == nil {
		return nil, errors.New("edge control runtime is unavailable")
	}
	request.CommandID = strings.TrimSpace(request.CommandID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.CommandCode = strings.TrimSpace(request.CommandCode)
	if request.CommandID == "" || request.DeviceID == "" || request.CommandCode == "" {
		return nil, errors.New("command ID, device ID and command code are required")
	}
	runtime.mu.Lock()
	binding, ok := runtime.commandByKey[commandBindingKey(request.DeviceID, request.CommandCode)]
	runtime.mu.Unlock()
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
	hostResult, err := runtime.host.RunCycle(ctx, at)
	if err != nil {
		return EdgeControlCycleResult{Cycle: edgecontrol.CycleResult{Halted: true, OutputError: err}}
	}
	telemetrySnapshot := runtime.snapshotFromProcessImage(hostResult.Cycle.Image)
	runtime.resolvePending(at, hostResult.Cycle, hostResult.WriteResults)
	return EdgeControlCycleResult{
		PollResults: hostResult.PollResults, Cycle: hostResult.Cycle, TelemetrySnapshot: telemetrySnapshot,
		TimedataRecords: hostResult.TimedataRecords, TimedataError: hostResult.TimedataError,
	}
}

func (runtime *EdgeControlRuntime) snapshotFromProcessImage(image edgecontrol.ProcessImage) Snapshot {
	runtime.mu.Lock()
	addressMap := make(map[string]string, len(runtime.addressByPointID))
	for k, v := range runtime.addressByPointID {
		addressMap[k] = v
	}
	runtime.mu.Unlock()

	snapshot := Snapshot{ObservedAt: image.At(), Devices: map[string]DeviceTelemetry{}}
	for _, point := range runtime.config.Points {
		if point.PointType == "COMMAND" {
			continue
		}
		address := point.DeviceID + "/" + point.PointCode
		if mapped, ok := addressMap[point.PointID]; ok {
			address = mapped
		}
		channel, ok := image.Get(address)
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

func (runtime *EdgeControlRuntime) resolvePending(at time.Time, result edgecontrol.CycleResult, writeResults []edgecontrol.DeviceWriteResult) {
	decisionByAddress := make(map[string]edgecontrol.Decision, len(result.Decisions))
	for _, decision := range result.Decisions {
		decisionByAddress[decision.Address] = decision
	}
	writeByAddress := map[string]edgecontrol.DeviceWriteResult{}
	for _, writeResult := range writeResults {
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
		if writeResult.Success && writeResult.Code == "IN_PROGRESS" {
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
	mu                  sync.RWMutex
	id                  string
	bindings            map[string]map[edgecontrol.SemanticChannel]string
	staleAfterByAddress map[string]time.Duration
	plant               PlantConfig
}

func (controller *edgePlantSafetyController) UpdateBindings(bindings map[string]map[edgecontrol.SemanticChannel]string) {
	controller.mu.Lock()
	controller.bindings = cloneAllSemanticBindings(bindings)
	controller.mu.Unlock()
}

func (controller *edgePlantSafetyController) ID() string { return controller.id }
func (controller *edgePlantSafetyController) Run(_ context.Context, image edgecontrol.ProcessImage, plan *edgecontrol.ControlPlan) error {
	controller.mu.RLock()
	bindingsSnapshot := cloneAllSemanticBindings(controller.bindings)
	controller.mu.RUnlock()
	for deviceID, bindings := range bindingsSnapshot {
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

func cloneAllSemanticBindings(src map[string]map[edgecontrol.SemanticChannel]string) map[string]map[edgecontrol.SemanticChannel]string {
	out := make(map[string]map[edgecontrol.SemanticChannel]string, len(src))
	for deviceID, bindings := range src {
		out[deviceID] = cloneSemanticBindings(bindings)
	}
	return out
}

func buildAutonomyEquipmentBindings(
	plant PlantConfig,
	bindingsByDevice map[string]map[edgecontrol.SemanticChannel]string,
) (
	[]edgecontrol.ChillerInterlockBinding,
	[]edgecontrol.PumpInterlockBinding,
	[]string,
	[]edgecontrol.ChillerFallbackTarget,
	[]edgecontrol.PumpFallbackTarget,
) {
	deviceIDs := make([]string, 0, len(bindingsByDevice))
	for id := range bindingsByDevice {
		deviceIDs = append(deviceIDs, id)
	}
	sort.Strings(deviceIDs)

	var chillers []edgecontrol.ChillerInterlockBinding
	var chwPumps []edgecontrol.PumpInterlockBinding
	var freqAddrs []string
	var fallbackChillers []edgecontrol.ChillerFallbackTarget
	var fallbackPumps []edgecontrol.PumpFallbackTarget

	for _, id := range deviceIDs {
		b := bindingsByDevice[id]
		if len(b) == 0 {
			continue
		}
		isChiller := id == plant.Chiller.ID || (b[edgecontrol.SemanticStartCommand] != "" && b[edgecontrol.SemanticTemperatureSetpoint] != "")
		if isChiller {
			chillers = append(chillers, edgecontrol.ChillerInterlockBinding{
				ComponentID:  id,
				StartCommand: b[edgecontrol.SemanticStartCommand],
				StopCommand:  b[edgecontrol.SemanticStopCommand],
				RunState:     b[edgecontrol.SemanticRunState],
				FaultChannel: b[edgecontrol.SemanticFaultCode],
			})
			if sp := b[edgecontrol.SemanticTemperatureSetpoint]; sp != "" {
				fallbackChillers = append(fallbackChillers, edgecontrol.ChillerFallbackTarget{
					ComponentID:        id,
					SupplySetpointAddr: sp,
					RunStateAddr:       b[edgecontrol.SemanticRunState],
					FaultAddr:          b[edgecontrol.SemanticFaultCode],
				})
			}
		}

		freqAddr := b[edgecontrol.SemanticFrequencySetpoint]
		if freqAddr != "" {
			freqAddrs = append(freqAddrs, freqAddr)
			fallbackPumps = append(fallbackPumps, edgecontrol.PumpFallbackTarget{
				ComponentID:   id,
				FrequencyAddr: freqAddr,
				RunStateAddr:  b[edgecontrol.SemanticRunState],
				FaultAddr:     b[edgecontrol.SemanticFaultCode],
			})
		}

		isCHWPump := id == plant.ChilledWaterPump.ID || (b[edgecontrol.SemanticFlow] != "" && b[edgecontrol.SemanticRunState] != "")
		if isCHWPump {
			chwPumps = append(chwPumps, edgecontrol.PumpInterlockBinding{
				ComponentID:      id,
				RunState:         b[edgecontrol.SemanticRunState],
				FlowFeedback:     b[edgecontrol.SemanticFlow],
				FaultChannel:     b[edgecontrol.SemanticFaultCode],
				FrequencyCommand: freqAddr,
			})
		}
	}
	return chillers, chwPumps, freqAddrs, fallbackChillers, fallbackPumps
}
