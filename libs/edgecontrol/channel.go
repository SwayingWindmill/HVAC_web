package edgecontrol

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type DataType string

const (
	DataTypeBoolean DataType = "BOOLEAN"
	DataTypeString  DataType = "STRING"
	DataTypeInteger DataType = "INTEGER"
	DataTypeLong    DataType = "LONG"
	DataTypeFloat   DataType = "FLOAT"
	DataTypeDouble  DataType = "DOUBLE"
)

type AccessMode string

const (
	AccessReadOnly  AccessMode = "RO"
	AccessReadWrite AccessMode = "RW"
	AccessWriteOnly AccessMode = "WO"
)

type DataPriority string

const (
	PriorityVeryHigh DataPriority = "VERY_HIGH"
	PriorityHigh     DataPriority = "HIGH"
	PriorityMedium   DataPriority = "MEDIUM"
	PriorityLow      DataPriority = "LOW"
)

type ChannelCategory string

const (
	ChannelCategoryOpenemsType ChannelCategory = "OPENEMS_TYPE"
	ChannelCategoryEnum        ChannelCategory = "ENUM"
	ChannelCategoryState       ChannelCategory = "STATE"
)

type StateLevel string

const (
	StateLevelOK      StateLevel = "OK"
	StateLevelInfo    StateLevel = "INFO"
	StateLevelWarning StateLevel = "WARNING"
	StateLevelFault   StateLevel = "FAULT"
)

type Quality string

const (
	QualityGood        Quality = "GOOD"
	QualitySuspect     Quality = "SUSPECT"
	QualityUnavailable Quality = "UNAVAILABLE"
)

type ChannelDescriptor struct {
	ComponentID               string            `json:"componentId"`
	ChannelID                 string            `json:"channelId"`
	PointID                   string            `json:"pointId"`
	DataType                  DataType          `json:"dataType"`
	Access                    AccessMode        `json:"access"`
	Description               string            `json:"description,omitempty"`
	Unit                      string            `json:"unit,omitempty"`
	Category                  ChannelCategory   `json:"category"`
	Options                   map[string]string `json:"options,omitempty"`
	StateLevel                StateLevel        `json:"stateLevel,omitempty"`
	PollPriority              DataPriority      `json:"pollPriority"`
	LocalPersistencePriority  DataPriority      `json:"localPersistencePriority"`
	RemotePersistencePriority DataPriority      `json:"remotePersistencePriority"`
	AggregationPriority       DataPriority      `json:"aggregationPriority"`
	ResendPriority            DataPriority      `json:"resendPriority"`
}

func (descriptor ChannelDescriptor) Address() string {
	return descriptor.ComponentID + "/" + descriptor.ChannelID
}

func (descriptor ChannelDescriptor) validate() error {
	if strings.TrimSpace(descriptor.ComponentID) == "" || strings.Contains(descriptor.ComponentID, "/") {
		return errors.New("component ID is required and must not contain '/'")
	}
	if strings.TrimSpace(descriptor.ChannelID) == "" || strings.Contains(descriptor.ChannelID, "/") {
		return errors.New("channel ID is required and must not contain '/'")
	}
	if strings.TrimSpace(descriptor.PointID) == "" {
		return errors.New("canonical Point ID is required")
	}
	switch descriptor.DataType {
	case DataTypeBoolean, DataTypeString, DataTypeInteger, DataTypeLong, DataTypeFloat, DataTypeDouble:
	default:
		return fmt.Errorf("unsupported data type %q", descriptor.DataType)
	}
	switch descriptor.Access {
	case AccessReadOnly, AccessReadWrite, AccessWriteOnly:
	default:
		return fmt.Errorf("unsupported access mode %q", descriptor.Access)
	}
	switch descriptor.Category {
	case ChannelCategoryOpenemsType:
		if len(descriptor.Options) != 0 || descriptor.StateLevel != "" {
			return errors.New("OPENEMS_TYPE channel must not declare enum options or state level")
		}
	case ChannelCategoryEnum:
		if len(descriptor.Options) == 0 {
			return errors.New("ENUM channel requires options")
		}
		if descriptor.StateLevel != "" {
			return errors.New("ENUM channel must not declare state level")
		}
	case ChannelCategoryState:
		switch descriptor.StateLevel {
		case StateLevelOK, StateLevelInfo, StateLevelWarning, StateLevelFault:
		default:
			return fmt.Errorf("STATE channel requires valid state level, got %q", descriptor.StateLevel)
		}
	default:
		return fmt.Errorf("unsupported channel category %q", descriptor.Category)
	}
	for name, priority := range map[string]DataPriority{
		"poll":              descriptor.PollPriority,
		"localPersistence":  descriptor.LocalPersistencePriority,
		"remotePersistence": descriptor.RemotePersistencePriority,
		"aggregation":       descriptor.AggregationPriority,
		"resend":            descriptor.ResendPriority,
	} {
		if !validPriority(priority) {
			return fmt.Errorf("unsupported %s priority %q", name, priority)
		}
	}
	return nil
}

func validPriority(priority DataPriority) bool {
	switch priority {
	case PriorityVeryHigh, PriorityHigh, PriorityMedium, PriorityLow:
		return true
	default:
		return false
	}
}

func cloneChannelDescriptor(descriptor ChannelDescriptor) ChannelDescriptor {
	if descriptor.Options != nil {
		options := make(map[string]string, len(descriptor.Options))
		for key, value := range descriptor.Options {
			options[key] = value
		}
		descriptor.Options = options
	}
	return descriptor
}

type Value struct {
	Type    DataType
	Boolean bool
	String  string
	Integer int32
	Long    int64
	Float   float32
	Double  float64
}

func BooleanValue(value bool) Value   { return Value{Type: DataTypeBoolean, Boolean: value} }
func StringValue(value string) Value  { return Value{Type: DataTypeString, String: value} }
func IntegerValue(value int32) Value  { return Value{Type: DataTypeInteger, Integer: value} }
func LongValue(value int64) Value     { return Value{Type: DataTypeLong, Long: value} }
func FloatValue(value float32) Value  { return Value{Type: DataTypeFloat, Float: value} }
func DoubleValue(value float64) Value { return Value{Type: DataTypeDouble, Double: value} }

func (value Value) NumericFloat64() (float64, bool) {
	switch value.Type {
	case DataTypeInteger:
		return float64(value.Integer), true
	case DataTypeLong:
		return float64(value.Long), true
	case DataTypeFloat:
		return float64(value.Float), true
	case DataTypeDouble:
		return value.Double, true
	default:
		return 0, false
	}
}

func (value Value) validate(expected DataType) error {
	if value.Type != expected {
		return fmt.Errorf("value type %q does not match channel type %q", value.Type, expected)
	}
	return nil
}

type Sample struct {
	Value      Value
	Quality    Quality
	ObservedAt time.Time
	Sequence   uint64
}

func (sample Sample) validate(expected DataType) error {
	if err := sample.Value.validate(expected); err != nil {
		return err
	}
	switch sample.Quality {
	case QualityGood, QualitySuspect, QualityUnavailable:
	default:
		return fmt.Errorf("unsupported quality %q", sample.Quality)
	}
	if sample.ObservedAt.IsZero() {
		return errors.New("observed time is required")
	}
	return nil
}

type ChannelSnapshot struct {
	Descriptor ChannelDescriptor
	Sample     Sample
	HasValue   bool
}

type ChannelEventType string

const (
	ChannelEventNextValue ChannelEventType = "NEXT_VALUE"
	ChannelEventUpdate    ChannelEventType = "UPDATE"
	ChannelEventChange    ChannelEventType = "CHANGE"
)

type ChannelEvent struct {
	Type       ChannelEventType
	Address    string
	Descriptor ChannelDescriptor
	Previous   *Sample
	Current    *Sample
}

type ChannelEventHandler func(ChannelEvent)

type channelSubscription struct {
	id      uint64
	handler ChannelEventHandler
}

type ProcessImage struct {
	cycle    uint64
	at       time.Time
	channels map[string]ChannelSnapshot
}

func (image ProcessImage) Cycle() uint64 { return image.cycle }
func (image ProcessImage) At() time.Time { return image.at }

func (image ProcessImage) Get(address string) (ChannelSnapshot, bool) {
	snapshot, ok := image.channels[address]
	return snapshot, ok
}

func (image ProcessImage) Channels() []ChannelSnapshot {
	addresses := make([]string, 0, len(image.channels))
	for address := range image.channels {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	out := make([]ChannelSnapshot, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, image.channels[address])
	}
	return out
}

type channelState struct {
	descriptor ChannelDescriptor
	current    Sample
	next       Sample
	hasCurrent bool
	hasNext    bool
	past       []Sample
}

const maxAgeOfPastSamples = 5*time.Minute + 10*time.Second

type Runtime struct {
	mu             sync.Mutex
	cycle          uint64
	channels       map[string]*channelState
	pointIDs       map[string]string
	subscriptions  map[string]map[ChannelEventType][]channelSubscription
	nextSubscriber uint64
}

func NewRuntime() *Runtime {
	return &Runtime{
		channels:      map[string]*channelState{},
		pointIDs:      map[string]string{},
		subscriptions: map[string]map[ChannelEventType][]channelSubscription{},
	}
}

func (runtime *Runtime) Register(descriptor ChannelDescriptor) error {
	if runtime == nil {
		return errors.New("runtime is nil")
	}
	if err := descriptor.validate(); err != nil {
		return err
	}
	address := descriptor.Address()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if _, exists := runtime.channels[address]; exists {
		return fmt.Errorf("channel %s is already registered", address)
	}
	if previous, exists := runtime.pointIDs[descriptor.PointID]; exists {
		return fmt.Errorf("Point %s is already mapped to channel %s", descriptor.PointID, previous)
	}
	runtime.channels[address] = &channelState{descriptor: cloneChannelDescriptor(descriptor)}
	runtime.pointIDs[descriptor.PointID] = address
	return nil
}

func (runtime *Runtime) Descriptor(address string) (ChannelDescriptor, bool) {
	if runtime == nil {
		return ChannelDescriptor{}, false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	state, ok := runtime.channels[address]
	if !ok {
		return ChannelDescriptor{}, false
	}
	return cloneChannelDescriptor(state.descriptor), true
}

func (runtime *Runtime) Descriptors() []ChannelDescriptor {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	addresses := make([]string, 0, len(runtime.channels))
	for address := range runtime.channels {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	out := make([]ChannelDescriptor, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, cloneChannelDescriptor(runtime.channels[address].descriptor))
	}
	return out
}

func (runtime *Runtime) PublishNext(address string, sample Sample) error {
	if runtime == nil {
		return errors.New("runtime is nil")
	}
	runtime.mu.Lock()
	state, ok := runtime.channels[address]
	if !ok {
		runtime.mu.Unlock()
		return fmt.Errorf("channel %s is not registered", address)
	}
	if err := sample.validate(state.descriptor.DataType); err != nil {
		runtime.mu.Unlock()
		return fmt.Errorf("publish %s: %w", address, err)
	}
	latestSequence := uint64(0)
	if state.hasCurrent {
		latestSequence = state.current.Sequence
	}
	if state.hasNext && state.next.Sequence > latestSequence {
		latestSequence = state.next.Sequence
	}
	if sample.Sequence != 0 && latestSequence != 0 && sample.Sequence <= latestSequence {
		runtime.mu.Unlock()
		return fmt.Errorf("publish %s: sequence %d is not newer than %d", address, sample.Sequence, latestSequence)
	}
	var previous *Sample
	if state.hasNext {
		value := state.next
		previous = &value
	}
	state.next = sample
	state.hasNext = true
	handlers := runtime.eventHandlersLocked(address, ChannelEventNextValue)
	descriptor := state.descriptor
	runtime.mu.Unlock()
	runtime.dispatchChannelEvent(handlers, ChannelEvent{
		Type:       ChannelEventNextValue,
		Address:    address,
		Descriptor: descriptor,
		Previous:   previous,
		Current:    cloneSamplePointer(sample),
	})
	return nil
}

func cloneSamplePointer(sample Sample) *Sample {
	cloned := sample
	return &cloned
}

func (runtime *Runtime) PastSamples(address string) []Sample {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	state, ok := runtime.channels[address]
	if !ok {
		return nil
	}
	return append([]Sample(nil), state.past...)
}

func (runtime *Runtime) Unregister(address string) bool {
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	state, ok := runtime.channels[address]
	if !ok {
		return false
	}
	delete(runtime.channels, address)
	delete(runtime.pointIDs, state.descriptor.PointID)
	delete(runtime.subscriptions, address)
	return true
}

func (runtime *Runtime) Subscribe(address string, eventType ChannelEventType, handler ChannelEventHandler) (func(), error) {
	if runtime == nil {
		return nil, errors.New("runtime is nil")
	}
	if handler == nil {
		return nil, errors.New("channel event handler is required")
	}
	switch eventType {
	case ChannelEventNextValue, ChannelEventUpdate, ChannelEventChange:
	default:
		return nil, fmt.Errorf("unsupported channel event type %q", eventType)
	}
	runtime.mu.Lock()
	if _, ok := runtime.channels[address]; !ok {
		runtime.mu.Unlock()
		return nil, fmt.Errorf("channel %s is not registered", address)
	}
	runtime.nextSubscriber++
	id := runtime.nextSubscriber
	if runtime.subscriptions[address] == nil {
		runtime.subscriptions[address] = map[ChannelEventType][]channelSubscription{}
	}
	runtime.subscriptions[address][eventType] = append(runtime.subscriptions[address][eventType], channelSubscription{id: id, handler: handler})
	runtime.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			runtime.mu.Lock()
			defer runtime.mu.Unlock()
			byType := runtime.subscriptions[address]
			if byType == nil {
				return
			}
			existing := byType[eventType]
			filtered := existing[:0]
			for _, subscription := range existing {
				if subscription.id != id {
					filtered = append(filtered, subscription)
				}
			}
			if len(filtered) == 0 {
				delete(byType, eventType)
			} else {
				byType[eventType] = filtered
			}
			if len(byType) == 0 {
				delete(runtime.subscriptions, address)
			}
		})
	}, nil
}

func (runtime *Runtime) eventHandlersLocked(address string, eventType ChannelEventType) []ChannelEventHandler {
	byType := runtime.subscriptions[address]
	if byType == nil {
		return nil
	}
	subscriptions := byType[eventType]
	handlers := make([]ChannelEventHandler, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		handlers = append(handlers, subscription.handler)
	}
	return handlers
}

func (runtime *Runtime) dispatchChannelEvent(handlers []ChannelEventHandler, event ChannelEvent) {
	for _, handler := range handlers {
		handler(event)
	}
}

func (runtime *Runtime) SwitchProcessImage(at time.Time) ProcessImage {
	if runtime == nil {
		return ProcessImage{}
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	runtime.mu.Lock()
	runtime.cycle++
	channels := make(map[string]ChannelSnapshot, len(runtime.channels))
	addresses := make([]string, 0, len(runtime.channels))
	for address := range runtime.channels {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	type pendingEvent struct {
		handlers []ChannelEventHandler
		event    ChannelEvent
	}
	events := make([]pendingEvent, 0)
	for _, address := range addresses {
		state := runtime.channels[address]
		var previous *Sample
		if state.hasCurrent {
			previous = cloneSamplePointer(state.current)
		}
		changed := false
		if state.hasNext {
			changed = !state.hasCurrent || !sameChannelValue(state.current, state.next)
			state.current = state.next
			state.hasCurrent = true
			state.hasNext = false
			state.past = append(state.past, state.current)
			cutoff := state.current.ObservedAt.Add(-maxAgeOfPastSamples)
			first := 0
			for first < len(state.past) && state.past[first].ObservedAt.Before(cutoff) {
				first++
			}
			if first > 0 {
				state.past = append([]Sample(nil), state.past[first:]...)
			}
		}
		update := ChannelEvent{
			Type: ChannelEventUpdate, Address: address, Descriptor: state.descriptor, Previous: previous,
		}
		if state.hasCurrent {
			update.Current = cloneSamplePointer(state.current)
		}
		events = append(events, pendingEvent{handlers: runtime.eventHandlersLocked(address, ChannelEventUpdate), event: update})
		if changed {
			change := update
			change.Type = ChannelEventChange
			events = append(events, pendingEvent{handlers: runtime.eventHandlersLocked(address, ChannelEventChange), event: change})
		}
		channels[address] = ChannelSnapshot{
			Descriptor: state.descriptor,
			Sample:     state.current,
			HasValue:   state.hasCurrent,
		}
	}
	image := ProcessImage{cycle: runtime.cycle, at: at, channels: channels}
	runtime.mu.Unlock()
	for _, event := range events {
		runtime.dispatchChannelEvent(event.handlers, event.event)
	}
	return image
}

func sameChannelValue(left, right Sample) bool {
	return left.Value == right.Value
}
