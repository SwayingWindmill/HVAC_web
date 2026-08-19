package edgecontrol

import (
	"context"
	"errors"
	"fmt"
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
	Address      string
	Success      bool
	Code         string
	AppliedValue *Value
}

// DirectDeviceAdapter is a direct in-process device path for simulator components.
// Physical protocol devices must not use this contract. Their production Driver declares
// protocol mappings/tasks and delegates shared I/O scheduling to a Protocol Bridge.
type DirectDeviceAdapter interface {
	Component() ComponentDescriptor
	Channels() []ChannelDescriptor
	Poll(context.Context, time.Time) ([]ChannelUpdate, error)
	Apply(context.Context, ProcessImage, []Decision) ([]DeviceWriteResult, error)
}

type DirectDevicePollResult struct {
	AdapterID string
	Updates   int
	Error     error
}

type DirectDeviceHost struct {
	mu         sync.RWMutex
	runtime    *Runtime
	components *ComponentRegistry
	adapters   map[string]DirectDeviceAdapter
}

func NewDirectDeviceHost(runtime *Runtime, components *ComponentRegistry) (*DirectDeviceHost, error) {
	if runtime == nil {
		return nil, errors.New("channel runtime is required")
	}
	if components == nil {
		return nil, errors.New("component registry is required")
	}
	return &DirectDeviceHost{
		runtime: runtime, components: components,
		adapters: map[string]DirectDeviceAdapter{},
	}, nil
}

func (host *DirectDeviceHost) RegisterAdapter(adapter DirectDeviceAdapter) error {
	if host == nil || adapter == nil {
		return errors.New("direct device host and adapter are required")
	}
	descriptor := adapter.Component()
	if descriptor.Kind != ComponentSimulator {
		return fmt.Errorf("direct device adapter %s must use SIMULATOR component kind", descriptor.ID)
	}
	channels := adapter.Channels()
	if len(channels) == 0 {
		return fmt.Errorf("direct device adapter %s exposes no channels", descriptor.ID)
	}
	seen := map[string]struct{}{}
	for _, channel := range channels {
		if channel.ComponentID != descriptor.ID {
			return fmt.Errorf("direct device adapter %s exposes channel owned by %s", descriptor.ID, channel.ComponentID)
		}
		if _, duplicate := seen[channel.Address()]; duplicate {
			return fmt.Errorf("direct device adapter %s exposes duplicate channel %s", descriptor.ID, channel.Address())
		}
		seen[channel.Address()] = struct{}{}
		if err := host.runtime.Register(channel); err != nil {
			return fmt.Errorf("register direct device adapter %s channel %s: %w", descriptor.ID, channel.Address(), err)
		}
	}
	if err := host.components.Register(descriptor); err != nil {
		return fmt.Errorf("register direct device adapter %s component: %w", descriptor.ID, err)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if _, exists := host.adapters[descriptor.ID]; exists {
		return fmt.Errorf("direct device adapter %s is already registered", descriptor.ID)
	}
	host.adapters[descriptor.ID] = adapter
	return nil
}

// PollOnce is the simulator/direct-adapter Input side. It updates Channel.nextValue only;
// Controllers cannot observe the new values until the next Process Image switch.
func (host *DirectDeviceHost) PollOnce(ctx context.Context, at time.Time) []DirectDevicePollResult {
	if host == nil {
		return nil
	}
	host.mu.RLock()
	ids := make([]string, 0, len(host.adapters))
	adapters := make(map[string]DirectDeviceAdapter, len(host.adapters))
	for id, adapter := range host.adapters {
		ids = append(ids, id)
		adapters[id] = adapter
	}
	host.mu.RUnlock()
	sort.Strings(ids)

	results := make([]DirectDevicePollResult, 0, len(ids))
	for _, id := range ids {
		updates, err := adapters[id].Poll(ctx, at)
		result := DirectDevicePollResult{AdapterID: id, Error: err}
		if err == nil {
			for _, update := range updates {
				descriptor, exists := host.runtime.Descriptor(update.Address)
				if !exists || descriptor.ComponentID != id {
					result.Error = fmt.Errorf("direct device adapter %s returned update for unowned channel %s", id, update.Address)
					break
				}
				if publishErr := host.runtime.PublishNext(update.Address, update.Sample); publishErr != nil {
					result.Error = publishErr
					break
				}
				result.Updates++
			}
		}
		results = append(results, result)
	}
	return results
}

func (host *DirectDeviceHost) apply(ctx context.Context, image ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	if host == nil {
		return nil, errors.New("direct device host is nil")
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
			return allResults, fmt.Errorf("no direct device adapter owns output component %s", id)
		}
		results, err := adapter.Apply(ctx, image, grouped[id])
		allResults = append(allResults, results...)
		if err != nil {
			return allResults, fmt.Errorf("direct device adapter %s apply: %w", id, err)
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
			return fmt.Errorf("direct device adapter %s returned write result without address", adapterID)
		}
		if _, duplicate := byAddress[result.Address]; duplicate {
			return fmt.Errorf("direct device adapter %s returned duplicate write result for %s", adapterID, result.Address)
		}
		byAddress[result.Address] = result
	}
	for _, decision := range decisions {
		result, ok := byAddress[decision.Address]
		if !ok {
			return fmt.Errorf("direct device adapter %s omitted write result for %s", adapterID, decision.Address)
		}
		if !result.Success {
			return fmt.Errorf("direct device adapter %s rejected %s with code %s", adapterID, decision.Address, result.Code)
		}
	}
	return nil
}

type DirectDeviceOutputWriter struct {
	mu   sync.Mutex
	host *DirectDeviceHost
	last []DeviceWriteResult
}

func NewDirectDeviceOutputWriter(host *DirectDeviceHost) (*DirectDeviceOutputWriter, error) {
	if host == nil {
		return nil, errors.New("direct device host is required")
	}
	return &DirectDeviceOutputWriter{host: host}, nil
}

func (writer *DirectDeviceOutputWriter) Apply(ctx context.Context, image ProcessImage, decisions []Decision) error {
	results, err := writer.host.apply(ctx, image, decisions)
	writer.mu.Lock()
	writer.last = cloneWriteResults(results)
	writer.mu.Unlock()
	return err
}

func (writer *DirectDeviceOutputWriter) LastResults() []DeviceWriteResult {
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
	}
	return out
}
