package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"
)

// OPCUAFaultStatus reports communication fault & linear backoff status on DeclarativeOPCUAAdapter.
type OPCUAFaultStatus struct {
	FaultActive         bool
	ConsecutiveFailures int
	LastError           string
	NextRetryAt         time.Time
}

// DeclarativeOPCUAAdapter implements DeviceAdapter over IEC 62541 OPC-UA TCP,
// supporting ReadNodes, subscription DataChange Publish polling, EDGE-003 hard real-time
// write barriers, and EDGE-013 immediate WriteNodes readback verification.
type DeclarativeOPCUAAdapter struct {
	mu                  sync.Mutex
	profile             DeclarativeOPCUAProfile
	component           ComponentDescriptor
	channels            []ChannelDescriptor
	channelIdxByAddress map[string]int
	specsByAddress      map[string]OPCUANodeSpec
	handleToAddr        map[uint32]string
	subNodes            []OPCUANodeID
	transport           OPCUATransport
	ownsTransport       bool

	pollCycle           uint64
	sequence            uint64
	consecutiveFailures int
	faultActive         bool
	lastError           string
	nextRetryAt         time.Time
	cached              map[string]Sample
}

// NewDeclarativeOPCUAAdapter constructs an OPC-UA DeviceAdapter from a validated profile.
func NewDeclarativeOPCUAAdapter(profile DeclarativeOPCUAProfile, transport OPCUATransport) (*DeclarativeOPCUAAdapter, error) {
	if err := profile.validateWithTransport(transport != nil); err != nil {
		return nil, err
	}
	cloned := profile.Clone()
	ownsTransport := false
	if transport == nil {
		bridge, err := NewOPCUABridge(OPCUABridgeConfig{
			Endpoint:       cloned.Endpoint,
			SecurityPolicy: cloned.SecurityPolicy,
			SecurityMode:   cloned.SecurityMode,
			AuthMode:       cloned.AuthMode,
			Username:       cloned.Username,
			CredentialRef:  cloned.CredentialRef,
			Timeout:        cloned.Timeout,
			Retries:        cloned.Retries,
		})
		if err != nil {
			return nil, err
		}
		transport = bridge
		ownsTransport = true
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

	channels := make([]ChannelDescriptor, len(cloned.Nodes))
	channelIdxByAddress := make(map[string]int, len(cloned.Nodes))
	specsByAddress := make(map[string]OPCUANodeSpec, len(cloned.Nodes))
	handleToAddr := make(map[uint32]string)
	var subNodes []OPCUANodeID
	bindings := make(map[SemanticChannel]string)

	for i, spec := range cloned.Nodes {
		unit := spec.Unit
		if unit == "" && profileReqs != nil && spec.SemanticChannel != "" {
			unit = profileReqs[spec.SemanticChannel].Unit
		}
		access := AccessReadOnly
		if spec.Writable {
			access = AccessReadWrite
		}
		desc := ChannelDescriptor{
			ComponentID:               cloned.ComponentID,
			ChannelID:                 spec.PointCode,
			PointID:                   cloned.ComponentID + ":" + spec.PointCode,
			DataType:                  spec.DataType,
			Access:                    access,
			SafetyBoundary:            spec.SafetyBoundary,
			Unit:                      unit,
			Category:                  ChannelCategoryOpenemsType,
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
		if spec.Subscribe {
			handle := uint32(len(subNodes) + 1)
			handleToAddr[handle] = addr
			subNodes = append(subNodes, spec.NodeID)
		}
		if spec.SemanticChannel != "" {
			bindings[spec.SemanticChannel] = addr
		}
	}

	comp := ComponentDescriptor{
		ID:              cloned.ComponentID,
		Alias:           cloned.Alias,
		Enabled:         true,
		Kind:            ComponentDeviceDriver,
		Type:            "DECLARATIVE_" + DriverTypeOPCUA,
		FactoryID:       DeclarativeOPCUAFactoryID,
		Version:         "p2-opcua:v1",
		Profiles:        profiles,
		ChannelBindings: bindings,
	}

	return &DeclarativeOPCUAAdapter{
		profile:             cloned,
		component:           comp,
		channels:            channels,
		channelIdxByAddress: channelIdxByAddress,
		specsByAddress:      specsByAddress,
		handleToAddr:        handleToAddr,
		subNodes:            subNodes,
		transport:           transport,
		ownsTransport:       ownsTransport,
		cached:              make(map[string]Sample, len(channels)),
	}, nil
}

func (a *DeclarativeOPCUAAdapter) Component() ComponentDescriptor { return cloneComponent(a.component) }
func (a *DeclarativeOPCUAAdapter) Channels() []ChannelDescriptor  { return slices.Clone(a.channels) }

func (a *DeclarativeOPCUAAdapter) FaultStatus() OPCUAFaultStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return OPCUAFaultStatus{
		FaultActive:         a.faultActive,
		ConsecutiveFailures: a.consecutiveFailures,
		LastError:           a.lastError,
		NextRetryAt:         a.nextRetryAt,
	}
}

func (a *DeclarativeOPCUAAdapter) Close() error {
	if a == nil || !a.ownsTransport || a.transport == nil {
		return nil
	}
	if closer, ok := a.transport.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Poll refreshes OPC-UA node channels via subscription Publish updates and polled ReadNodes.
func (a *DeclarativeOPCUAAdapter) Poll(ctx context.Context, at time.Time) ([]ChannelUpdate, error) {
	if a == nil {
		return nil, errors.New("declarative OPC-UA adapter is nil")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}

	a.mu.Lock()
	if a.faultActive && !a.nextRetryAt.IsZero() && at.Before(a.nextRetryAt) {
		bad := a.badUpdatesLocked(at)
		err := fmt.Errorf("%w: %s (backoff until %s)", ErrCommunicationFault, a.lastError, a.nextRetryAt.Format(time.RFC3339))
		a.mu.Unlock()
		return bad, err
	}
	cycle := a.pollCycle
	a.mu.Unlock()

	subNotifications := map[string]Sample{}
	if len(a.subNodes) > 0 {
		if err := a.transport.EnsureSubscriptions(ctx, a.profile.SubscriptionIntervalMS, a.subNodes); err != nil {
			a.mu.Lock()
			a.recordFaultLocked(at, err)
			bad := a.badUpdatesLocked(at)
			a.mu.Unlock()
			return bad, fmt.Errorf("%w: %w", ErrCommunicationFault, err)
		}
		notifs, err := a.transport.PollPublish(ctx)
		if err != nil {
			a.mu.Lock()
			a.recordFaultLocked(at, err)
			bad := a.badUpdatesLocked(at)
			a.mu.Unlock()
			return bad, fmt.Errorf("%w: %w", ErrCommunicationFault, err)
		}
		for _, n := range notifs {
			addr, ok := a.handleToAddr[n.ClientHandle]
			if !ok {
				continue
			}
			spec := a.specsByAddress[addr]
			subNotifications[addr] = decodeOPCUADataValue(spec, n.Value, at)
		}
	}

	var dueAddrs []string
	var dueNodeIDs []OPCUANodeID
	updates := make([]ChannelUpdate, len(a.channels))
	populated := make([]bool, len(a.channels))

	a.mu.Lock()
	for i, ch := range a.channels {
		addr := ch.Address()
		spec := a.specsByAddress[addr]
		if sample, gotSub := subNotifications[addr]; gotSub {
			a.sequence++
			sample.Sequence = a.sequence
			a.cached[addr] = sample
			updates[i] = ChannelUpdate{Address: addr, Sample: sample}
			populated[i] = true
			continue
		}
		cachedSample, hasCached := a.cached[addr]
		due := !hasCached || cycle%uint64(pollCadence(spec.PollPriority)) == 0
		if !due || (spec.Subscribe && hasCached) {
			a.sequence++
			cachedSample.Sequence = a.sequence
			updates[i] = ChannelUpdate{Address: addr, Sample: cachedSample}
			populated[i] = true
			continue
		}
		dueAddrs = append(dueAddrs, addr)
		dueNodeIDs = append(dueNodeIDs, spec.NodeID)
	}
	a.mu.Unlock()

	if len(dueNodeIDs) > 0 {
		dvs, err := a.transport.ReadNodes(ctx, dueNodeIDs)
		if err == nil && len(dvs) != len(dueNodeIDs) {
			err = fmt.Errorf("short OPC-UA read: got %d values, want %d", len(dvs), len(dueNodeIDs))
		}
		if err != nil {
			a.mu.Lock()
			a.recordFaultLocked(at, err)
			bad := a.badUpdatesLocked(at)
			a.mu.Unlock()
			return bad, fmt.Errorf("%w: %w", ErrCommunicationFault, err)
		}
		a.mu.Lock()
		for j, addr := range dueAddrs {
			idx := a.channelIdxByAddress[addr]
			spec := a.specsByAddress[addr]
			sample := decodeOPCUADataValue(spec, dvs[j], at)
			a.sequence++
			sample.Sequence = a.sequence
			a.cached[addr] = sample
			updates[idx] = ChannelUpdate{Address: addr, Sample: sample}
			populated[idx] = true
		}
		a.mu.Unlock()
	}

	a.mu.Lock()
	a.consecutiveFailures = 0
	a.faultActive = false
	a.lastError = ""
	a.nextRetryAt = time.Time{}
	a.pollCycle++
	a.mu.Unlock()

	out := make([]ChannelUpdate, 0, len(updates))
	for i, ok := range populated {
		if ok {
			out = append(out, updates[i])
		}
	}
	return out, nil
}

// Apply writes accepted decisions via WriteNodes and immediately reads back to prove EDGE-013 alignment.
func (a *DeclarativeOPCUAAdapter) Apply(ctx context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	if a == nil {
		return nil, errors.New("declarative OPC-UA adapter is nil")
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
		if !spec.Writable {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "CHANNEL_READ_ONLY"})
			continue
		}
		if !decision.Accepted || decision.Effective == nil {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_VALUE_MISSING"})
			continue
		}

		wireVal, code := encodeOPCUAWriteValue(spec, *decision.Effective)
		if code != "" {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: code})
			continue
		}

		codes, err := a.transport.WriteNodes(ctx, []OPCUAWriteValue{{NodeID: spec.NodeID, Value: wireVal}})
		if err != nil {
			return results, err
		}
		if len(codes) == 0 || codes[0] != OPCUAStatusGood {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "OPCUA_WRITE_REJECTED"})
			continue
		}

		readbackAt := time.Now().UTC()
		dvs, readErr := a.transport.ReadNodes(ctx, []OPCUANodeID{spec.NodeID})
		if readErr != nil || len(dvs) != 1 {
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_FAILED",
				ReadbackVerified: false,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		sample := decodeOPCUADataValue(spec, dvs[0], readbackAt)
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
		if !opcuaReadbackMatchesTarget(spec, *decision.Effective, sample.Value) {
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

func (a *DeclarativeOPCUAAdapter) recordFaultLocked(at time.Time, err error) {
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

func (a *DeclarativeOPCUAAdapter) badUpdatesLocked(at time.Time) []ChannelUpdate {
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

func decodeOPCUADataValue(spec OPCUANodeSpec, dv OPCUADataValue, at time.Time) Sample {
	if !dv.HasValue || dv.StatusCode != OPCUAStatusGood {
		return Sample{Value: zeroValueForType(spec.DataType), Quality: QualityBad, QualityReason: fmt.Sprintf("OPCUA_STATUS_0x%08X", dv.StatusCode), ObservedAt: at}
	}
	switch spec.DataType {
	case DataTypeBoolean:
		b := dv.Value.Boolean
		if dv.Value.Type != DataTypeBoolean {
			if n, ok := dv.Value.NumericFloat64(); ok {
				b = n != 0
			}
		}
		return Sample{Value: BooleanValue(b), Quality: QualityGood, ObservedAt: at}
	case DataTypeString:
		if dv.Value.Type == DataTypeString {
			return Sample{Value: StringValue(dv.Value.String), Quality: QualityGood, ObservedAt: at}
		}
		if n, ok := dv.Value.NumericFloat64(); ok {
			return Sample{Value: StringValue(strconv.FormatFloat(n, 'g', -1, 64)), Quality: QualityGood, ObservedAt: at}
		}
		return Sample{Value: StringValue(""), Quality: QualityBad, QualityReason: "OPCUA_TYPE_MISMATCH", ObservedAt: at}
	case DataTypeInteger:
		n, ok := dv.Value.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return Sample{Value: IntegerValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
		}
		return Sample{Value: IntegerValue(int32(math.Round(n*spec.Scale + spec.Offset))), Quality: QualityGood, ObservedAt: at}
	default:
		n, ok := dv.Value.NumericFloat64()
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

func encodeOPCUAWriteValue(spec OPCUANodeSpec, target Value) (Value, string) {
	switch spec.DataType {
	case DataTypeBoolean:
		if target.Type != DataTypeBoolean {
			return Value{}, "COMMAND_TYPE_MISMATCH"
		}
		return BooleanValue(target.Boolean), ""
	case DataTypeString:
		if target.Type != DataTypeString {
			return Value{}, "COMMAND_TYPE_MISMATCH"
		}
		return StringValue(target.String), ""
	case DataTypeInteger:
		n, ok := target.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return Value{}, "COMMAND_VALUE_INVALID"
		}
		return IntegerValue(int32(math.Round((n - spec.Offset) / spec.Scale))), ""
	default:
		n, ok := target.NumericFloat64()
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return Value{}, "COMMAND_VALUE_INVALID"
		}
		return DoubleValue((n - spec.Offset) / spec.Scale), ""
	}
}

func opcuaReadbackMatchesTarget(spec OPCUANodeSpec, target, actual Value) bool {
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
