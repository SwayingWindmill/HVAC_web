package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type ChannelUpdate struct {
	Address string
	Sample  Sample
}

type DeviceWriteResult struct {
	Address          string
	Success          bool
	Code             string
	AppliedValue     *Value
	ReadbackVerified bool
	ReadbackValue    *Value
	ReadbackAt       time.Time
}

// DeviceAdapter is the production-facing device contract shared by physical and simulated drivers.
// A physical driver may delegate transport work to a Protocol Bridge; Controllers and the DeviceHost
// still see the same typed Channel, polling, and arbitrated-write boundary.
type DeviceAdapter interface {
	Component() ComponentDescriptor
	Channels() []ChannelDescriptor
	Poll(context.Context, time.Time) ([]ChannelUpdate, error)
	Apply(context.Context, ProcessImage, []Decision) ([]DeviceWriteResult, error)
}

type DevicePollResult struct {
	AdapterID string
	Updates   int
	Error     error
}

type DeviceHost struct {
	mu         sync.RWMutex
	runtime    *Runtime
	components *ComponentRegistry
	adapters   map[string]DeviceAdapter
}

func NewDeviceHost(runtime *Runtime, components *ComponentRegistry) (*DeviceHost, error) {
	if runtime == nil {
		return nil, errors.New("channel runtime is required")
	}
	if components == nil {
		return nil, errors.New("component registry is required")
	}
	return &DeviceHost{
		runtime: runtime, components: components,
		adapters: map[string]DeviceAdapter{},
	}, nil
}

func (host *DeviceHost) RegisterAdapter(adapter DeviceAdapter) error {
	if host == nil || adapter == nil {
		return errors.New("device host and adapter are required")
	}
	descriptor, channels, err := validateAdapterBasics(adapter)
	if err != nil {
		return err
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if _, exists := host.adapters[descriptor.ID]; exists {
		return fmt.Errorf("device adapter %s is already registered", descriptor.ID)
	}
	return host.registerAdapterLocked(adapter, descriptor, channels)
}

// ReplaceAdapter atomically replaces an existing DeviceAdapter (or registers it if
// absent) without disturbing unrelated devices. If registering the replacement fails,
// the previous adapter is restored before returning the error.
func (host *DeviceHost) ReplaceAdapter(adapter DeviceAdapter) error {
	if host == nil || adapter == nil {
		return errors.New("device host and adapter are required")
	}
	descriptor, channels, err := validateAdapterBasics(adapter)
	if err != nil {
		return err
	}
	host.mu.Lock()
	previous, hadPrevious := host.adapters[descriptor.ID]
	if hadPrevious {
		host.removeAdapterLocked(descriptor.ID, previous)
	}
	if err := host.registerAdapterLocked(adapter, descriptor, channels); err != nil {
		if hadPrevious {
			_ = host.registerAdapterLocked(previous, previous.Component(), previous.Channels())
		}
		host.mu.Unlock()
		return err
	}
	host.mu.Unlock()
	if hadPrevious && previous != adapter {
		if closer, ok := previous.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	return nil
}

func (host *DeviceHost) Adapter(id string) (DeviceAdapter, bool) {
	if host == nil {
		return nil, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	host.mu.RLock()
	defer host.mu.RUnlock()
	adapter, ok := host.adapters[id]
	return adapter, ok
}

func validateAdapterBasics(adapter DeviceAdapter) (ComponentDescriptor, []ChannelDescriptor, error) {
	descriptor := adapter.Component()
	if descriptor.Kind != ComponentDeviceDriver && descriptor.Kind != ComponentSimulator {
		return ComponentDescriptor{}, nil, fmt.Errorf("device adapter %s must use DEVICE_DRIVER or SIMULATOR component kind", descriptor.ID)
	}
	if err := descriptor.validateBasics(); err != nil {
		return ComponentDescriptor{}, nil, err
	}
	channels := adapter.Channels()
	if len(channels) == 0 {
		return ComponentDescriptor{}, nil, fmt.Errorf("device adapter %s exposes no channels", descriptor.ID)
	}
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		if channel.ComponentID != descriptor.ID {
			return ComponentDescriptor{}, nil, fmt.Errorf("device adapter %s exposes channel owned by %s", descriptor.ID, channel.ComponentID)
		}
		if _, duplicate := seen[channel.Address()]; duplicate {
			return ComponentDescriptor{}, nil, fmt.Errorf("device adapter %s exposes duplicate channel %s", descriptor.ID, channel.Address())
		}
		seen[channel.Address()] = struct{}{}
		if err := channel.validate(); err != nil {
			return ComponentDescriptor{}, nil, fmt.Errorf("device adapter %s channel %s: %w", descriptor.ID, channel.Address(), err)
		}
	}
	return descriptor, channels, nil
}

func (host *DeviceHost) registerAdapterLocked(adapter DeviceAdapter, descriptor ComponentDescriptor, channels []ChannelDescriptor) error {
	registered := make([]string, 0, len(channels))
	cleanupChannels := func() {
		for _, address := range registered {
			host.runtime.Unregister(address)
		}
	}
	for _, channel := range channels {
		if err := host.runtime.Register(channel); err != nil {
			cleanupChannels()
			return fmt.Errorf("register device adapter %s channel %s: %w", descriptor.ID, channel.Address(), err)
		}
		registered = append(registered, channel.Address())
	}
	if err := host.components.Register(descriptor); err != nil {
		cleanupChannels()
		return fmt.Errorf("register device adapter %s component: %w", descriptor.ID, err)
	}
	host.adapters[descriptor.ID] = adapter
	return nil
}

func (host *DeviceHost) removeAdapterLocked(id string, adapter DeviceAdapter) {
	delete(host.adapters, id)
	for _, channel := range adapter.Channels() {
		host.runtime.Unregister(channel.Address())
	}
	host.components.Unregister(id)
}

func (host *DeviceHost) UnregisterAdapter(id string) bool {
	if host == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	host.mu.Lock()
	adapter, exists := host.adapters[id]
	if !exists {
		host.mu.Unlock()
		return false
	}
	host.removeAdapterLocked(id, adapter)
	host.mu.Unlock()
	if closer, ok := adapter.(io.Closer); ok {
		_ = closer.Close()
	}
	return true
}

// PollOnce is the DeviceAdapter input side. It updates Channel.nextValue only;
// Controllers cannot observe the new values until the next Process Image switch.
func (host *DeviceHost) PollOnce(ctx context.Context, at time.Time) []DevicePollResult {
	if host == nil {
		return nil
	}
	host.mu.RLock()
	ids := make([]string, 0, len(host.adapters))
	adapters := make(map[string]DeviceAdapter, len(host.adapters))
	for id, adapter := range host.adapters {
		ids = append(ids, id)
		adapters[id] = adapter
	}
	host.mu.RUnlock()
	sort.Strings(ids)

	results := make([]DevicePollResult, 0, len(ids))
	for _, id := range ids {
		updates, err := adapters[id].Poll(ctx, at)
		result := DevicePollResult{AdapterID: id, Error: err}
		for _, update := range updates {
			descriptor, exists := host.runtime.Descriptor(update.Address)
			if !exists || descriptor.ComponentID != id {
				if result.Error == nil {
					result.Error = fmt.Errorf("device adapter %s returned update for unowned channel %s", id, update.Address)
				}
				break
			}
			if publishErr := host.runtime.PublishNext(update.Address, update.Sample); publishErr != nil {
				if result.Error == nil {
					result.Error = publishErr
				}
				break
			}
			result.Updates++
		}
		results = append(results, result)
	}
	return results
}

func (host *DeviceHost) apply(ctx context.Context, image ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	if host == nil {
		return nil, errors.New("device host is nil")
	}
	grouped := map[string][]Decision{}
	for _, decision := range decisions {
		descriptor, ok := host.runtime.Descriptor(decision.Address)
		if !ok {
			return nil, fmt.Errorf("decision targets unknown channel %s", decision.Address)
		}
		grouped[descriptor.ComponentID] = append(grouped[descriptor.ComponentID], decision)
	}
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	allResults := make([]DeviceWriteResult, 0, len(decisions))
	for _, id := range ids {
		host.mu.RLock()
		adapter := host.adapters[id]
		host.mu.RUnlock()
		if adapter == nil {
			return allResults, fmt.Errorf("no device adapter owns output component %s", id)
		}
		results, err := adapter.Apply(ctx, image, grouped[id])
		allResults = append(allResults, results...)
		if err != nil {
			return allResults, fmt.Errorf("device adapter %s apply: %w", id, err)
		}
		if err := validateWriteResults(id, grouped[id], results); err != nil {
			return allResults, err
		}
	}
	return allResults, nil
}

func validateWriteResults(adapterID string, decisions []Decision, results []DeviceWriteResult) error {
	byAddress := map[string]DeviceWriteResult{}
	for _, result := range results {
		if strings.TrimSpace(result.Address) == "" {
			return fmt.Errorf("device adapter %s returned write result without address", adapterID)
		}
		if _, duplicate := byAddress[result.Address]; duplicate {
			return fmt.Errorf("device adapter %s returned duplicate write result for %s", adapterID, result.Address)
		}
		byAddress[result.Address] = result
	}
	for _, decision := range decisions {
		result, ok := byAddress[decision.Address]
		if !ok {
			return fmt.Errorf("device adapter %s omitted write result for %s", adapterID, decision.Address)
		}
		if !result.Success {
			return fmt.Errorf("device adapter %s rejected %s with code %s", adapterID, decision.Address, result.Code)
		}
	}
	return nil
}

type DeviceOutputWriter struct {
	mu   sync.Mutex
	host *DeviceHost
	last []DeviceWriteResult
}

func NewDeviceOutputWriter(host *DeviceHost) (*DeviceOutputWriter, error) {
	if host == nil {
		return nil, errors.New("device host is required")
	}
	return &DeviceOutputWriter{host: host}, nil
}

func (writer *DeviceOutputWriter) Apply(ctx context.Context, image ProcessImage, decisions []Decision) error {
	results, err := writer.host.apply(ctx, image, decisions)
	writer.mu.Lock()
	writer.last = cloneWriteResults(results)
	writer.mu.Unlock()
	return err
}

func (writer *DeviceOutputWriter) LastResults() []DeviceWriteResult {
	if writer == nil {
		return nil
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return cloneWriteResults(writer.last)
}

func cloneWriteResults(results []DeviceWriteResult) []DeviceWriteResult {
	out := make([]DeviceWriteResult, len(results))
	for index, result := range results {
		out[index] = result
		if result.AppliedValue != nil {
			value := *result.AppliedValue
			out[index].AppliedValue = &value
		}
		if result.ReadbackValue != nil {
			value := *result.ReadbackValue
			out[index].ReadbackValue = &value
		}
	}
	return out
}
