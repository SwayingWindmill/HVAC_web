package edgecontrol

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Host is the production-neutral owner of one Edge control runtime. DeviceAdapters
// provide device and protocol behavior; the Host owns their shared registration,
// Process Image, controller schedule, governed output, and Cycle causality.
type Host struct {
	mu sync.RWMutex

	runtime    *Runtime
	components *ComponentRegistry
	devices    *DeviceHost
	intents    *IntentStore
	writer     *DeviceOutputWriter
	cycle      *Cycle
	timedata   TimedataRecorder
}

type HostCycleResult struct {
	PollResults     []DevicePollResult
	Cycle           CycleResult
	WriteResults    []DeviceWriteResult
	TimedataRecords int
	TimedataError   error
}

func NewHost() (*Host, error) {
	runtime := NewRuntime()
	capabilities, err := NewStandardCapabilityRegistry()
	if err != nil {
		return nil, err
	}
	components, err := NewComponentRegistry(runtime, capabilities)
	if err != nil {
		return nil, err
	}
	devices, err := NewDeviceHost(runtime, components)
	if err != nil {
		return nil, err
	}
	intents, err := NewIntentStore(runtime)
	if err != nil {
		return nil, err
	}
	writer, err := NewDeviceOutputWriter(devices)
	if err != nil {
		return nil, err
	}
	return &Host{
		runtime: runtime, components: components, devices: devices,
		intents: intents, writer: writer,
	}, nil
}

// IntentStore returns the Host-owned governed intent boundary so callers can
// construct the existing IntentController before fixing the controller schedule.
func (host *Host) IntentStore() *IntentStore {
	if host == nil {
		return nil
	}
	return host.intents
}

// Start fixes the deterministic controller schedule for this Host. Adapters may
// be registered before or after Start, but the schedule is configured only once.
func (host *Host) Start(bindings []ControllerBinding) error {
	if host == nil {
		return errors.New("Edge Host is nil")
	}
	scheduler, err := NewScheduler(bindings)
	if err != nil {
		return err
	}
	cycle, err := NewCycle(host.runtime, scheduler, host.writer)
	if err != nil {
		return err
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.cycle != nil {
		return errors.New("Edge Host is already started")
	}
	host.cycle = cycle
	return nil
}

func (host *Host) RegisterAdapter(adapter DeviceAdapter) error {
	if host == nil {
		return errors.New("Edge Host is nil")
	}
	return host.devices.RegisterAdapter(adapter)
}

func (host *Host) UnregisterAdapter(id string) bool {
	return host != nil && host.devices.UnregisterAdapter(id)
}

func (host *Host) AddCycleHook(binding CycleHookBinding) error {
	if host == nil {
		return errors.New("Edge Host is nil")
	}
	host.mu.RLock()
	cycle := host.cycle
	host.mu.RUnlock()
	if cycle == nil {
		return errors.New("Edge Host is not started")
	}
	return cycle.AddHook(binding)
}

func (host *Host) AttachTimedata(recorder TimedataRecorder) error {
	if host == nil {
		return errors.New("Edge Host is nil")
	}
	if recorder == nil {
		return errors.New("Edge Timedata recorder is required")
	}
	host.mu.Lock()
	host.timedata = recorder
	host.mu.Unlock()
	return nil
}

func (host *Host) Manifest(edgeID, revision string, at time.Time) (EdgeManifest, error) {
	if host == nil {
		return EdgeManifest{}, errors.New("Edge Host is nil")
	}
	return host.components.Manifest(edgeID, revision, at)
}

// RunCycle synchronously completes the production Edge causality boundary:
// DeviceAdapter reads publish next values, Process Image is fixed, Controllers
// run over that image, and accepted decisions reach adapters at execute-write.
func (host *Host) RunCycle(ctx context.Context, at time.Time) (HostCycleResult, error) {
	if host == nil {
		return HostCycleResult{}, errors.New("Edge Host is nil")
	}
	host.mu.RLock()
	cycle := host.cycle
	timedata := host.timedata
	host.mu.RUnlock()
	if cycle == nil {
		return HostCycleResult{}, errors.New("Edge Host is not started")
	}

	result := HostCycleResult{PollResults: host.devices.PollOnce(ctx, at)}
	result.Cycle = cycle.RunOnce(ctx, at)
	if hasAcceptedDecision(result.Cycle.Decisions) {
		result.WriteResults = host.writer.LastResults()
	}
	if timedata != nil {
		result.TimedataRecords, result.TimedataError = timedata.RecordImage(result.Cycle.Image)
	}
	return result, nil
}

func hasAcceptedDecision(decisions []Decision) bool {
	for _, decision := range decisions {
		if decision.Accepted && decision.Effective != nil {
			return true
		}
	}
	return false
}
