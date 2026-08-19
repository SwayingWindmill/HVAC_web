package edgecontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type ComponentKind string

const (
	ComponentDeviceDriver   ComponentKind = "DEVICE_DRIVER"
	ComponentProtocolBridge ComponentKind = "PROTOCOL_BRIDGE"
	ComponentController     ComponentKind = "CONTROLLER"
	ComponentService        ComponentKind = "SERVICE"
	ComponentSimulator      ComponentKind = "SIMULATOR"
)

type ComponentDescriptor struct {
	ID              string
	Alias           string
	Enabled         bool
	Kind            ComponentKind
	Type            string
	FactoryID       string
	Version         string
	Properties      map[string]json.RawMessage
	Profiles        []CapabilityProfileID
	ChannelBindings map[SemanticChannel]string
}

func (descriptor ComponentDescriptor) validateBasics() error {
	if strings.TrimSpace(descriptor.ID) == "" || strings.Contains(descriptor.ID, "/") {
		return errors.New("component ID is required and must not contain '/'")
	}
	if strings.TrimSpace(descriptor.Type) == "" || strings.TrimSpace(descriptor.FactoryID) == "" || strings.TrimSpace(descriptor.Version) == "" {
		return errors.New("component type, factory ID and version are required")
	}
	switch descriptor.Kind {
	case ComponentDeviceDriver, ComponentProtocolBridge, ComponentController, ComponentService, ComponentSimulator:
	default:
		return fmt.Errorf("unsupported component kind %q", descriptor.Kind)
	}
	if descriptor.Kind == ComponentDeviceDriver && len(descriptor.Profiles) == 0 {
		return errors.New("device driver must implement at least one capability profile")
	}
	return nil
}

func cloneComponent(descriptor ComponentDescriptor) ComponentDescriptor {
	descriptor.Profiles = append([]CapabilityProfileID(nil), descriptor.Profiles...)
	descriptor.ChannelBindings = cloneBindings(descriptor.ChannelBindings)
	if descriptor.Properties != nil {
		properties := make(map[string]json.RawMessage, len(descriptor.Properties))
		for key, value := range descriptor.Properties {
			properties[key] = append(json.RawMessage(nil), value...)
		}
		descriptor.Properties = properties
	}
	return descriptor
}

func cloneBindings(bindings map[SemanticChannel]string) map[SemanticChannel]string {
	if bindings == nil {
		return nil
	}
	out := make(map[SemanticChannel]string, len(bindings))
	for semantic, address := range bindings {
		out[semantic] = address
	}
	return out
}

type ComponentRegistry struct {
	mu           sync.RWMutex
	runtime      *Runtime
	capabilities *CapabilityRegistry
	components   map[string]ComponentDescriptor
}

func NewComponentRegistry(runtime *Runtime, capabilities *CapabilityRegistry) (*ComponentRegistry, error) {
	if runtime == nil {
		return nil, errors.New("channel runtime is required")
	}
	if capabilities == nil {
		return nil, errors.New("capability registry is required")
	}
	return &ComponentRegistry{
		runtime: runtime, capabilities: capabilities, components: map[string]ComponentDescriptor{},
	}, nil
}

func (registry *ComponentRegistry) Register(descriptor ComponentDescriptor) error {
	if registry == nil {
		return errors.New("component registry is nil")
	}
	if err := descriptor.validateBasics(); err != nil {
		return err
	}

	seenProfiles := map[CapabilityProfileID]struct{}{}
	requirements := map[SemanticChannel]ChannelRequirement{}
	for _, profileID := range descriptor.Profiles {
		if _, exists := seenProfiles[profileID]; exists {
			return fmt.Errorf("component %s repeats capability profile %s", descriptor.ID, profileID)
		}
		seenProfiles[profileID] = struct{}{}
		profile, ok := registry.capabilities.Get(profileID)
		if !ok {
			return fmt.Errorf("component %s references unknown capability profile %s", descriptor.ID, profileID)
		}
		for _, requirement := range profile.Channels {
			if previous, exists := requirements[requirement.Semantic]; exists {
				if previous.DataType != requirement.DataType || previous.Unit != requirement.Unit || previous.Access != requirement.Access {
					return fmt.Errorf("component %s has incompatible requirements for semantic channel %s", descriptor.ID, requirement.Semantic)
				}
				previous.Required = previous.Required || requirement.Required
				requirements[requirement.Semantic] = previous
				continue
			}
			requirements[requirement.Semantic] = requirement
		}
	}

	for semantic := range descriptor.ChannelBindings {
		if _, exists := requirements[semantic]; !exists && len(descriptor.Profiles) > 0 {
			return fmt.Errorf("component %s binds semantic channel %s not declared by its capability profiles", descriptor.ID, semantic)
		}
	}
	for semantic, requirement := range requirements {
		address, bound := descriptor.ChannelBindings[semantic]
		if !bound || strings.TrimSpace(address) == "" {
			if requirement.Required {
				return fmt.Errorf("component %s is missing required semantic channel %s", descriptor.ID, semantic)
			}
			continue
		}
		channel, exists := registry.runtime.Descriptor(address)
		if !exists {
			return fmt.Errorf("component %s binds semantic channel %s to unknown channel %s", descriptor.ID, semantic, address)
		}
		if channel.ComponentID != descriptor.ID {
			return fmt.Errorf("component %s cannot bind channel %s owned by component %s", descriptor.ID, address, channel.ComponentID)
		}
		if channel.DataType != requirement.DataType {
			return fmt.Errorf("component %s semantic channel %s expects %s but %s is %s", descriptor.ID, semantic, requirement.DataType, address, channel.DataType)
		}
		if requirement.Unit != "" && channel.Unit != requirement.Unit {
			return fmt.Errorf("component %s semantic channel %s expects unit %s but %s uses %s", descriptor.ID, semantic, requirement.Unit, address, channel.Unit)
		}
		if channel.Access != requirement.Access {
			return fmt.Errorf("component %s semantic channel %s expects access %s but %s uses %s", descriptor.ID, semantic, requirement.Access, address, channel.Access)
		}
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.components[descriptor.ID]; exists {
		return fmt.Errorf("component %s is already registered", descriptor.ID)
	}
	registry.components[descriptor.ID] = cloneComponent(descriptor)
	return nil
}

func (registry *ComponentRegistry) Unregister(id string) bool {
	if registry == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.components[id]; !exists {
		return false
	}
	delete(registry.components, id)
	return true
}

func (registry *ComponentRegistry) Get(id string) (ComponentDescriptor, bool) {
	if registry == nil {
		return ComponentDescriptor{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	descriptor, ok := registry.components[id]
	if !ok {
		return ComponentDescriptor{}, false
	}
	return cloneComponent(descriptor), true
}

func (registry *ComponentRegistry) Components() []ComponentDescriptor {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	ids := make([]string, 0, len(registry.components))
	for id := range registry.components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ComponentDescriptor, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneComponent(registry.components[id]))
	}
	return out
}

type ManifestComponent struct {
	ID              string                     `json:"id"`
	Alias           string                     `json:"alias,omitempty"`
	Enabled         bool                       `json:"enabled"`
	Kind            ComponentKind              `json:"kind"`
	Type            string                     `json:"type"`
	FactoryID       string                     `json:"factoryId"`
	Version         string                     `json:"version"`
	Properties      map[string]json.RawMessage `json:"properties,omitempty"`
	Profiles        []CapabilityProfileID      `json:"profiles,omitempty"`
	Channels        []string                   `json:"channels"`
	ChannelBindings map[SemanticChannel]string `json:"channelBindings,omitempty"`
}

type EdgeManifest struct {
	SchemaVersion      int                 `json:"schemaVersion"`
	EdgeID             string              `json:"edgeId"`
	Revision           string              `json:"revision"`
	GeneratedAt        time.Time           `json:"generatedAt"`
	Components         []ManifestComponent `json:"components"`
	CapabilityProfiles []CapabilityProfile `json:"capabilityProfiles"`
	Channels           []ChannelDescriptor `json:"channels"`
}

func (registry *ComponentRegistry) Manifest(edgeID, revision string, generatedAt time.Time) (EdgeManifest, error) {
	if registry == nil {
		return EdgeManifest{}, errors.New("component registry is nil")
	}
	if strings.TrimSpace(edgeID) == "" || strings.TrimSpace(revision) == "" {
		return EdgeManifest{}, errors.New("edge ID and manifest revision are required")
	}
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	components := registry.Components()
	channels := registry.runtime.Descriptors()
	channelsByComponent := make(map[string][]string, len(components))
	for _, channel := range channels {
		channelsByComponent[channel.ComponentID] = append(channelsByComponent[channel.ComponentID], channel.ChannelID)
	}
	for componentID := range channelsByComponent {
		sort.Strings(channelsByComponent[componentID])
	}
	manifestComponents := make([]ManifestComponent, 0, len(components))
	for _, component := range components {
		manifestComponents = append(manifestComponents, ManifestComponent{
			ID: component.ID, Alias: component.Alias, Enabled: component.Enabled,
			Kind: component.Kind, Type: component.Type, FactoryID: component.FactoryID, Version: component.Version,
			Properties: cloneComponent(component).Properties,
			Profiles:   append([]CapabilityProfileID(nil), component.Profiles...), Channels: append([]string(nil), channelsByComponent[component.ID]...),
			ChannelBindings: cloneBindings(component.ChannelBindings),
		})
	}
	return EdgeManifest{
		SchemaVersion:      1,
		EdgeID:             edgeID,
		Revision:           revision,
		GeneratedAt:        generatedAt.UTC(),
		Components:         manifestComponents,
		CapabilityProfiles: registry.capabilities.Profiles(),
		Channels:           channels,
	}, nil
}
