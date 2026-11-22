package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BACnetFaultStatus exposes the adapter's current communication fault & linear backoff state.
type BACnetFaultStatus struct {
	FaultActive         bool
	ConsecutiveFailures int
	LastError           string
	NextRetryAt         time.Time
}

type bacnetCOVEntry struct {
	sample Sample
	gen    uint64
}

// DeclarativeBACnetAdapter implements DeviceAdapter over ASHRAE 135 BACnet/IP,
// supporting polled ReadProperty, optional SubscribeCOV async notifications,
// EDGE-003 hard real-time write barriers, and EDGE-013 immediate WriteProperty readback verification.
type DeclarativeBACnetAdapter struct {
	mu                  sync.Mutex
	profile             DeclarativeBACnetProfile
	component           ComponentDescriptor
	channels            []ChannelDescriptor
	channelIdxByAddress map[string]int
	specsByAddress      map[string]BACnetObjectSpec
	transport           BACnetTransport
	ownsTransport       bool
	resolvedEndpoint    string

	pollCycle           uint64
	sequence            uint64
	consecutiveFailures int
	faultActive         bool
	lastError           string
	nextRetryAt         time.Time
	cached              map[string]Sample
	covSubscribedAt     map[string]time.Time
	covLatest           map[string]bacnetCOVEntry
	covConsumedGen      map[string]uint64
}

// NewDeclarativeBACnetAdapter builds a DeviceAdapter from a validated DeclarativeBACnetProfile.
// When transport is nil, a dedicated BACnetIPBridge UDP socket is created and owned by the adapter.
func NewDeclarativeBACnetAdapter(profile DeclarativeBACnetProfile, transport BACnetTransport) (*DeclarativeBACnetAdapter, error) {
	if err := profile.validateWithTransport(transport != nil); err != nil {
		return nil, err
	}
	cloned := profile.Clone()
	ownsTransport := false
	if transport == nil {
		bridge, err := NewBACnetIPBridge(BACnetIPBridgeConfig{
			Timeout: cloned.Timeout,
			Retries: cloned.Retries,
		})
		if err != nil {
			return nil, err
		}
		transport = bridge
		ownsTransport = true
	}

	resolvedEP := strings.TrimSpace(cloned.Endpoint)
	if udpAddr, err := net.ResolveUDPAddr("udp", resolvedEP); err == nil {
		resolvedEP = udpAddr.String()
	}

	var profileReqs map[SemanticChannel]ChannelRequirement
	var profiles []CapabilityProfileID
	if cloned.CapabilityProfile != "" {
		profiles = []CapabilityProfileID{cloned.CapabilityProfile}
		if std, ok := findStandardProfile(cloned.CapabilityProfile); ok {
			profileReqs = make(map[SemanticChannel]ChannelRequirement, len(std.Channels))
			for _, req := range std.Channels {
				profileReqs[req.Semantic] = req
			}
		}
	}

	channels := make([]ChannelDescriptor, len(cloned.Objects))
	channelIdxByAddress := make(map[string]int, len(cloned.Objects))
	specsByAddress := make(map[string]BACnetObjectSpec, len(cloned.Objects))
	bindings := make(map[SemanticChannel]string)

	for i, spec := range cloned.Objects {
		unit := spec.Unit
		if unit == "" && profileReqs != nil && spec.SemanticChannel != "" {
			unit = profileReqs[spec.SemanticChannel].Unit
		}
		access := AccessReadOnly
		if spec.Writable {
			access = AccessReadWrite
		}
		category := ChannelCategoryOpenemsType
		var options map[string]string
		if len(spec.EnumMap) > 0 {
			category = ChannelCategoryEnum
			options = spec.EnumMap
		}
		desc := ChannelDescriptor{
			ComponentID:               cloned.ComponentID,
			ChannelID:                 spec.PointCode,
			PointID:                   cloned.ComponentID + ":" + spec.PointCode,
			DataType:                  spec.DataType,
			Access:                    access,
			SafetyBoundary:            spec.SafetyBoundary,
			Unit:                      unit,
			Category:                  category,
			Options:                   options,
			PollPriority:              spec.PollPriority,
			LocalPersistencePriority:  spec.PollPriority,
			RemotePersistencePriority: spec.PollPriority,
			AggregationPriority:       spec.PollPriority,
			ResendPriority:            spec.PollPriority,
		}
		channels[i] = desc
		addr := desc.Address()
		channelIdxByAddress[addr] = i
		specsByAddress[addr] = spec
		if spec.SemanticChannel != "" {
			bindings[spec.SemanticChannel] = addr
		}
	}

	comp := ComponentDescriptor{
		ID:              cloned.ComponentID,
		Alias:           cloned.Alias,
		Enabled:         true,
		Kind:            ComponentDeviceDriver,
		Type:            "DECLARATIVE_" + DriverTypeBACnetIP,
		FactoryID:       DeclarativeBACnetFactoryID,
		Version:         "p2-bacnet:v1",
		Profiles:        profiles,
		ChannelBindings: bindings,
	}

	return &DeclarativeBACnetAdapter{
		profile:             cloned,
		component:           comp,
		channels:            channels,
		channelIdxByAddress: channelIdxByAddress,
		specsByAddress:      specsByAddress,
		transport:           transport,
		ownsTransport:       ownsTransport,
		resolvedEndpoint:    resolvedEP,
		cached:              make(map[string]Sample, len(channels)),
		covSubscribedAt:     make(map[string]time.Time),
		covLatest:           make(map[string]bacnetCOVEntry),
		covConsumedGen:      make(map[string]uint64),
	}, nil
}

func (a *DeclarativeBACnetAdapter) Component() ComponentDescriptor {
	return cloneComponent(a.component)
}
func (a *DeclarativeBACnetAdapter) Channels() []ChannelDescriptor { return slices.Clone(a.channels) }

// FaultStatus reports the adapter's communication fault state.
func (a *DeclarativeBACnetAdapter) FaultStatus() BACnetFaultStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return BACnetFaultStatus{
		FaultActive:         a.faultActive,
		ConsecutiveFailures: a.consecutiveFailures,
		LastError:           a.lastError,
		NextRetryAt:         a.nextRetryAt,
	}
}

// Close closes any transport owned directly by this adapter.
func (a *DeclarativeBACnetAdapter) Close() error {
	if a == nil || !a.ownsTransport || a.transport == nil {
		return nil
	}
	if closer, ok := a.transport.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Poll refreshes BACnet objects via SubscribeCOV notifications and ReadProperty polling.
func (a *DeclarativeBACnetAdapter) Poll(ctx context.Context, at time.Time) ([]ChannelUpdate, error) {
	if a == nil {
		return nil, errors.New("declarative BACnet adapter is nil")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}

	a.mu.Lock()
	if a.faultActive && !a.nextRetryAt.IsZero() && at.Before(a.nextRetryAt) {
		updates := a.badUpdatesLocked(at)
		err := fmt.Errorf("%w: %s (backoff until %s)", ErrCommunicationFault, a.lastError, a.nextRetryAt.Format(time.RFC3339))
		a.mu.Unlock()
		return updates, err
	}
	cycle := a.pollCycle
	a.mu.Unlock()

	updates := make([]ChannelUpdate, 0, len(a.channels))
	for i, ch := range a.channels {
		addr := ch.Address()
		spec := a.specsByAddress[addr]

		if spec.COVSubscribe {
			a.ensureCOVSubscription(ctx, addr, spec, at)
			a.mu.Lock()
			entry, hasCOV := a.covLatest[addr]
			consumed := a.covConsumedGen[addr]
			if hasCOV && entry.gen > consumed {
				a.sequence++
				sample := entry.sample
				sample.ObservedAt = at
				sample.Sequence = a.sequence
				a.cached[addr] = sample
				a.covConsumedGen[addr] = entry.gen
				a.mu.Unlock()
				updates = append(updates, ChannelUpdate{Address: addr, Sample: sample})
				continue
			}
			a.mu.Unlock()
		}

		cadence := pollCadence(spec.PollPriority)
		a.mu.Lock()
		cachedSample, hasCached := a.cached[addr]
		a.mu.Unlock()
		if hasCached && cycle%uint64(cadence) != 0 {
			a.mu.Lock()
			a.sequence++
			cachedSample.Sequence = a.sequence
			a.mu.Unlock()
			updates = append(updates, ChannelUpdate{Address: addr, Sample: cachedSample})
			continue
		}

		val, err := a.transport.ReadProperty(ctx, a.resolvedEndpoint, a.profile.NetworkNumber, spec.ObjectID(), spec.PropertyID)
		if err != nil {
			a.mu.Lock()
			a.recordFaultLocked(at, err)
			bad := a.badUpdatesLocked(at)
			a.mu.Unlock()
			return bad, fmt.Errorf("%w: %w", ErrCommunicationFault, err)
		}

		sample := decodeBACnetSample(spec, val, at)
		a.mu.Lock()
		a.sequence++
		sample.Sequence = a.sequence
		a.cached[addr] = sample
		a.mu.Unlock()
		updates = append(updates, ChannelUpdate{Address: addr, Sample: sample})
		_ = i
	}

	a.mu.Lock()
	a.consecutiveFailures = 0
	a.faultActive = false
	a.lastError = ""
	a.nextRetryAt = time.Time{}
	a.pollCycle++
	a.mu.Unlock()
	return updates, nil
}

func (a *DeclarativeBACnetAdapter) ensureCOVSubscription(ctx context.Context, addr string, spec BACnetObjectSpec, at time.Time) {
	a.mu.Lock()
	lastSub, ok := a.covSubscribedAt[addr]
	life := a.profile.COVLifetimeSeconds
	renew := !ok || (life > 0 && at.Sub(lastSub) >= time.Duration(life/2)*time.Second)
	a.mu.Unlock()
	if !renew {
		return
	}
	subLife := life
	if subLife == 0 {
		subLife = 300
	}
	err := a.transport.SubscribeCOV(ctx, a.resolvedEndpoint, a.profile.NetworkNumber, defaultBACnetProcessID, spec.ObjectID(), subLife, func(msg BACnetCOVNotification) {
		now := time.Now().UTC()
		for _, pv := range msg.Properties {
			if pv.PropertyID != spec.PropertyID {
				continue
			}
			sample := decodeBACnetSample(spec, pv.Value, now)
			a.mu.Lock()
			nextGen := a.covLatest[addr].gen + 1
			a.covLatest[addr] = bacnetCOVEntry{sample: sample, gen: nextGen}
			a.mu.Unlock()
			break
		}
	})
	if err == nil {
		a.mu.Lock()
		a.covSubscribedAt[addr] = at
		a.mu.Unlock()
	}
}

// Apply writes accepted decisions via WriteProperty and immediately verifies via ReadProperty (EDGE-013).
func (a *DeclarativeBACnetAdapter) Apply(ctx context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	if a == nil {
		return nil, errors.New("declarative BACnet adapter is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		spec, exists := a.specsByAddress[decision.Address]
		if !exists {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_CHANNEL_INVALID"})
			continue
		}
		if chIdx, ok := a.channelIdxByAddress[decision.Address]; ok && IsHardRealtimeChannel(a.channels[chIdx]) {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: ReasonHardRealtimeSafetyBoundary})
			continue
		}
		if !spec.Writable || spec.ObjectType.IsInputOnly() {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "CHANNEL_READ_ONLY"})
			continue
		}
		if !decision.Accepted || decision.Effective == nil {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_VALUE_MISSING"})
			continue
		}

		wireVal, code := encodeBACnetWriteValue(spec, *decision.Effective)
		if code != "" {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: code})
			continue
		}

		prio := a.profile.WritePriority
		if err := a.transport.WriteProperty(ctx, a.resolvedEndpoint, a.profile.NetworkNumber, spec.ObjectID(), spec.PropertyID, wireVal, &prio); err != nil {
			return results, err
		}

		readbackAt := time.Now().UTC()
		readVal, readErr := a.transport.ReadProperty(ctx, a.resolvedEndpoint, a.profile.NetworkNumber, spec.ObjectID(), spec.PropertyID)
		if readErr != nil {
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_FAILED",
				ReadbackVerified: false,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		sample := decodeBACnetSample(spec, readVal, readbackAt)
		if sample.Quality != QualityGood {
			v := sample.Value
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_BAD_QUALITY",
				ReadbackVerified: false,
				ReadbackValue:    &v,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		v := sample.Value
		if !bacnetReadbackMatchesTarget(spec, *decision.Effective, sample.Value) {
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_MISMATCH",
				AppliedValue:     &v,
				ReadbackVerified: false,
				ReadbackValue:    &v,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		a.cached[decision.Address] = sample
		applied, rb := v, v
		results = append(results, DeviceWriteResult{
			Address:          decision.Address,
			Success:          true,
			Code:             "APPLIED",
			AppliedValue:     &applied,
			ReadbackVerified: true,
			ReadbackValue:    &rb,
			ReadbackAt:       readbackAt,
		})
	}
	return results, nil
}

func (a *DeclarativeBACnetAdapter) recordFaultLocked(at time.Time, err error) {
	a.consecutiveFailures++
	backoff := time.Duration(a.consecutiveFailures) * modbusFaultBaseBackoff
	if backoff > modbusFaultMaxBackoff || backoff <= 0 {
		backoff = modbusFaultMaxBackoff
	}
	a.faultActive = true
	a.lastError = err.Error()
	a.nextRetryAt = at.Add(backoff)
	clear(a.cached)
}

func (a *DeclarativeBACnetAdapter) badUpdatesLocked(at time.Time) []ChannelUpdate {
	updates := make([]ChannelUpdate, 0, len(a.channels))
	for _, ch := range a.channels {
		a.sequence++
		updates = append(updates, ChannelUpdate{
			Address: ch.Address(),
			Sample: Sample{
				Value:         zeroValueForType(ch.DataType),
				Quality:       QualityBad,
				QualityReason: QualityReasonCommunicationFault,
				ObservedAt:    at,
				Sequence:      a.sequence,
			},
		})
	}
	return updates
}

func decodeBACnetSample(spec BACnetObjectSpec, raw BACnetAppValue, at time.Time) Sample {
	if raw.Null {
		return Sample{Value: zeroValueForType(spec.DataType), Quality: QualityBad, QualityReason: "BACNET_NULL_VALUE", ObservedAt: at}
	}
	switch spec.DataType {
	case DataTypeBoolean:
		b := raw.Bool
		if raw.Type != DataTypeBoolean {
			if n, ok := raw.NumericFloat64(); ok {
				b = n != 0
			}
		}
		return Sample{Value: BooleanValue(b), Quality: QualityGood, ObservedAt: at}
	case DataTypeString:
		if len(spec.EnumMap) > 0 {
			if n, ok := raw.NumericFloat64(); ok {
				key := strconv.FormatInt(int64(math.Round(n)), 10)
				if label, found := spec.EnumMap[key]; found {
					return Sample{Value: StringValue(label), Quality: QualityGood, ObservedAt: at}
				}
			}
		}
		if raw.Type == DataTypeString {
			return Sample{Value: StringValue(raw.Text), Quality: QualityGood, ObservedAt: at}
		}
		if n, ok := raw.NumericFloat64(); ok {
			return Sample{Value: StringValue(strconv.FormatInt(int64(math.Round(n)), 10)), Quality: QualityGood, ObservedAt: at}
		}
		return Sample{Value: StringValue(""), Quality: QualityBad, QualityReason: "BACNET_TYPE_MISMATCH", ObservedAt: at}
	case DataTypeInteger:
		n, ok := raw.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return Sample{Value: IntegerValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
		}
		scaled := n*spec.Scale + spec.Offset
		return Sample{Value: IntegerValue(int32(math.Round(scaled))), Quality: QualityGood, ObservedAt: at}
	default:
		n, ok := raw.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return Sample{Value: DoubleValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
		}
		scaled := n*spec.Scale + spec.Offset
		if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
			return Sample{Value: DoubleValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
		}
		return Sample{Value: DoubleValue(scaled), Quality: QualityGood, ObservedAt: at}
	}
}

func encodeBACnetWriteValue(spec BACnetObjectSpec, target Value) (BACnetAppValue, string) {
	switch spec.DataType {
	case DataTypeBoolean:
		if target.Type != DataTypeBoolean {
			return BACnetAppValue{}, "COMMAND_TYPE_MISMATCH"
		}
		switch spec.ObjectType {
		case BACnetObjBinaryOutput, BACnetObjBinaryValue:
			v := uint32(0)
			if target.Boolean {
				v = 1
			}
			return BACnetAppValue{Type: DataTypeInteger, Enum: v, IsEnum: true}, ""
		default:
			return BACnetAppValue{Type: DataTypeBoolean, Bool: target.Boolean}, ""
		}
	case DataTypeString:
		if target.Type == DataTypeString && len(spec.EnumMap) > 0 {
			for k, label := range spec.EnumMap {
				if label == target.String {
					parsed, err := strconv.ParseUint(k, 10, 32)
					if err == nil {
						return BACnetAppValue{Type: DataTypeInteger, Unsigned: parsed}, ""
					}
				}
			}
			return BACnetAppValue{}, "COMMAND_ENUM_UNKNOWN"
		}
		if target.Type != DataTypeString {
			return BACnetAppValue{}, "COMMAND_TYPE_MISMATCH"
		}
		return BACnetAppValue{Type: DataTypeString, Text: target.String}, ""
	case DataTypeInteger:
		n, ok := target.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return BACnetAppValue{}, "COMMAND_VALUE_INVALID"
		}
		raw := int64(math.Round((n - spec.Offset) / spec.Scale))
		if spec.ObjectType == BACnetObjMultiStateOutput || spec.ObjectType == BACnetObjMultiStateValue {
			if raw < 1 {
				return BACnetAppValue{}, "COMMAND_VALUE_OUT_OF_RANGE"
			}
			return BACnetAppValue{Type: DataTypeInteger, Unsigned: uint64(raw)}, ""
		}
		return BACnetAppValue{Type: DataTypeFloat, Real: float32((n - spec.Offset) / spec.Scale)}, ""
	default:
		n, ok := target.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return BACnetAppValue{}, "COMMAND_VALUE_INVALID"
		}
		raw := (n - spec.Offset) / spec.Scale
		return BACnetAppValue{Type: DataTypeFloat, Real: float32(raw)}, ""
	}
}

func bacnetReadbackMatchesTarget(spec BACnetObjectSpec, target, actual Value) bool {
	if target.Type != actual.Type {
		tn, tok := target.NumericFloat64()
		an, aok := actual.NumericFloat64()
		return tok && aok && math.Abs(tn-an) <= 0.05
	}
	switch spec.DataType {
	case DataTypeBoolean:
		return target.Boolean == actual.Boolean
	case DataTypeString:
		return target.String == actual.String
	case DataTypeInteger:
		return target.Integer == actual.Integer
	default:
		return math.Abs(target.Double-actual.Double) <= 0.05
	}
}
