package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	modbusFaultBaseBackoff = 2 * time.Second
	modbusFaultMaxBackoff  = 5 * time.Minute

	QualityReasonCommunicationFault = "COMMUNICATION_FAULT"
	QualityReasonNonFiniteFloat     = "NON_FINITE_FLOAT"
)

var ErrCommunicationFault = errors.New(QualityReasonCommunicationFault)

type blockRegisterEntry struct {
	spec       ModbusRegisterSpec
	channelIdx int
	offset     uint16
}

type runtimeReadBlock struct {
	block     ModbusReadBlock
	entries   []blockRegisterEntry
	cached    []Sample
	hasCached bool
}

// DeclarativeModbusAdapter implements DeviceAdapter for declarative Modbus register maps,
// coalescing reads into bounded blocks, scheduling polls by priority, handling writes via
// FC6/FC16 (with atomic read-modify-write for BIT_BOOL), and enforcing OpenEMS-style linear fault backoff.
type DeclarativeModbusAdapter struct {
	mu                  sync.Mutex
	profile             DeclarativeModbusProfile
	component           ComponentDescriptor
	channels            []ChannelDescriptor
	channelIdxByAddress map[string]int
	specsByAddress      map[string]ModbusRegisterSpec
	blocks              []*runtimeReadBlock
	transport           ModbusRegisterTransport
	ownsTransport       bool

	pollCycle           uint64
	sequence            uint64
	consecutiveFailures int
	faultActive         bool
	lastError           string
	nextRetryAt         time.Time
}

// NewDeclarativeModbusAdapter builds a DeviceAdapter from a declarative Modbus profile.
// When transport is nil, a dedicated Modbus TCP or RTU bridge is created and owned by the adapter.
func NewDeclarativeModbusAdapter(profile DeclarativeModbusProfile, transport ModbusRegisterTransport) (*DeclarativeModbusAdapter, error) {
	if err := profile.validateWithTransport(transport != nil); err != nil {
		return nil, err
	}
	clonedProfile := cloneDeclarativeProfile(profile)
	ownsTransport := false
	if transport == nil {
		var err error
		transport, err = newBridgeForProfile(clonedProfile)
		if err != nil {
			return nil, err
		}
		ownsTransport = true
	}

	var profileReqs map[SemanticChannel]ChannelRequirement
	var profiles []CapabilityProfileID
	if clonedProfile.CapabilityProfile != "" {
		profiles = []CapabilityProfileID{clonedProfile.CapabilityProfile}
		if std, ok := findStandardProfile(clonedProfile.CapabilityProfile); ok {
			profileReqs = make(map[SemanticChannel]ChannelRequirement, len(std.Channels))
			for _, req := range std.Channels {
				profileReqs[req.Semantic] = req
			}
		}
	}

	channels := make([]ChannelDescriptor, len(clonedProfile.Registers))
	channelIdxByAddress := make(map[string]int, len(clonedProfile.Registers))
	specsByAddress := make(map[string]ModbusRegisterSpec, len(clonedProfile.Registers))
	bindings := make(map[SemanticChannel]string)

	for i, spec := range clonedProfile.Registers {
		unit := strings.TrimSpace(spec.Unit)
		if unit == "" && profileReqs != nil && spec.SemanticChannel != "" {
			unit = profileReqs[spec.SemanticChannel].Unit
		}
		pointID := strings.TrimSpace(spec.PointID)
		if pointID == "" {
			pointID = clonedProfile.ComponentID + ":" + spec.PointCode
		}
		category := ChannelCategoryOpenemsType
		var options map[string]string
		if spec.RawType == ModbusRawEnum16 {
			category = ChannelCategoryEnum
			options = cloneStringMap(spec.EnumMap)
		}
		desc := ChannelDescriptor{
			ComponentID:               clonedProfile.ComponentID,
			ChannelID:                 spec.effectiveChannelID(),
			PointID:                   pointID,
			DataType:                  spec.channelDataType(),
			Access:                    spec.channelAccess(),
			SafetyBoundary:            spec.SafetyBoundary,
			Description:               spec.Description,
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

	coalesced := CoalesceModbusReadBlocks(clonedProfile.Registers)
	blocks := make([]*runtimeReadBlock, len(coalesced))
	for i, blk := range coalesced {
		entries := make([]blockRegisterEntry, len(blk.Specs))
		for j, spec := range blk.Specs {
			addr := clonedProfile.ComponentID + "/" + spec.effectiveChannelID()
			entries[j] = blockRegisterEntry{
				spec:       spec,
				channelIdx: channelIdxByAddress[addr],
				offset:     spec.Address - blk.StartAddress,
			}
		}
		blocks[i] = &runtimeReadBlock{
			block:   blk,
			entries: entries,
			cached:  make([]Sample, len(entries)),
		}
	}

	driverType := clonedProfile.DriverType
	if driverType == "" {
		driverType = ModbusDriverTCP
	}
	component := ComponentDescriptor{
		ID:              clonedProfile.ComponentID,
		Alias:           clonedProfile.Alias,
		Enabled:         true,
		Kind:            ComponentDeviceDriver,
		Type:            "DECLARATIVE_" + string(driverType),
		FactoryID:       DeclarativeModbusFactoryID,
		Version:         "p2-declarative:v1",
		Profiles:        profiles,
		ChannelBindings: bindings,
	}

	return &DeclarativeModbusAdapter{
		profile:             clonedProfile,
		component:           component,
		channels:            channels,
		channelIdxByAddress: channelIdxByAddress,
		specsByAddress:      specsByAddress,
		blocks:              blocks,
		transport:           transport,
		ownsTransport:       ownsTransport,
	}, nil
}

func newBridgeForProfile(profile DeclarativeModbusProfile) (ModbusRegisterTransport, error) {
	timeout := profile.effectiveTimeout()
	switch profile.DriverType {
	case ModbusDriverTCP:
		return NewModbusTCPBridge(ModbusTCPBridgeConfig{
			Endpoint: profile.Endpoint,
			Timeout:  timeout,
			Retries:  profile.Retries,
		})
	case ModbusDriverRTU:
		return NewModbusRTUBridge(ModbusRTUBridgeConfig{
			Endpoint: profile.Endpoint,
			Timeout:  timeout,
			Retries:  profile.Retries,
		})
	default:
		return nil, fmt.Errorf("unsupported Modbus driverType %q", profile.DriverType)
	}
}

func (adapter *DeclarativeModbusAdapter) Component() ComponentDescriptor {
	return cloneComponent(adapter.component)
}

func (adapter *DeclarativeModbusAdapter) Channels() []ChannelDescriptor {
	return slices.Clone(adapter.channels)
}

func (adapter *DeclarativeModbusAdapter) FaultStatus() ModbusFaultStatus {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return ModbusFaultStatus{
		FaultActive:         adapter.faultActive,
		ConsecutiveFailures: adapter.consecutiveFailures,
		LastError:           adapter.lastError,
		NextRetryAt:         adapter.nextRetryAt,
	}
}

func (adapter *DeclarativeModbusAdapter) Close() error {
	if adapter == nil || !adapter.ownsTransport || adapter.transport == nil {
		return nil
	}
	if closer, ok := adapter.transport.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (adapter *DeclarativeModbusAdapter) Poll(ctx context.Context, at time.Time) ([]ChannelUpdate, error) {
	if adapter == nil {
		return nil, errors.New("declarative Modbus adapter is nil")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}

	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	if adapter.faultActive && !adapter.nextRetryAt.IsZero() && at.Before(adapter.nextRetryAt) {
		return adapter.badUpdatesLocked(at), fmt.Errorf("%w: %s (backoff until %s)", ErrCommunicationFault, adapter.lastError, adapter.nextRetryAt.Format(time.RFC3339))
	}

	cycle := adapter.pollCycle
	updates := make([]ChannelUpdate, len(adapter.channels))
	populated := make([]bool, len(adapter.channels))

	for _, rblk := range adapter.blocks {
		due := !rblk.hasCached || cycle%uint64(rblk.block.Cadence) == 0
		if !due {
			for i, entry := range rblk.entries {
				adapter.sequence++
				sample := rblk.cached[i]
				sample.Sequence = adapter.sequence
				updates[entry.channelIdx] = ChannelUpdate{
					Address: adapter.channels[entry.channelIdx].Address(),
					Sample:  sample,
				}
				populated[entry.channelIdx] = true
			}
			continue
		}

		regs, err := adapter.transport.Read(ctx, ModbusReadTask{
			Name:         fmt.Sprintf("%s-fc%d-%d", adapter.component.ID, rblk.block.FunctionCode, rblk.block.StartAddress),
			UnitID:       adapter.profile.UnitID,
			FunctionCode: rblk.block.FunctionCode,
			Address:      rblk.block.StartAddress,
			Quantity:     rblk.block.Quantity,
		})
		if err == nil && len(regs) != int(rblk.block.Quantity) {
			err = fmt.Errorf("short Modbus read: got %d registers, want %d", len(regs), rblk.block.Quantity)
		}
		if err != nil {
			adapter.recordCommunicationFaultLocked(at, err)
			return adapter.badUpdatesLocked(at), fmt.Errorf("%w: %w", ErrCommunicationFault, err)
		}

		for i, entry := range rblk.entries {
			start := int(entry.offset)
			end := start + int(entry.spec.RegisterCount)
			sample := decodeRegisterSample(entry.spec, regs[start:end], at)
			rblk.cached[i] = sample
			adapter.sequence++
			sample.Sequence = adapter.sequence
			updates[entry.channelIdx] = ChannelUpdate{
				Address: adapter.channels[entry.channelIdx].Address(),
				Sample:  sample,
			}
			populated[entry.channelIdx] = true
		}
		rblk.hasCached = true
	}

	adapter.consecutiveFailures = 0
	adapter.faultActive = false
	adapter.lastError = ""
	adapter.nextRetryAt = time.Time{}
	adapter.pollCycle++

	out := make([]ChannelUpdate, 0, len(updates))
	for i, ok := range populated {
		if ok {
			out = append(out, updates[i])
		}
	}
	return out, nil
}

func (adapter *DeclarativeModbusAdapter) recordCommunicationFaultLocked(at time.Time, err error) {
	adapter.consecutiveFailures++
	backoff := time.Duration(adapter.consecutiveFailures) * modbusFaultBaseBackoff
	if backoff > modbusFaultMaxBackoff || backoff <= 0 {
		backoff = modbusFaultMaxBackoff
	}
	adapter.faultActive = true
	adapter.lastError = err.Error()
	adapter.nextRetryAt = at.Add(backoff)
	for _, rblk := range adapter.blocks {
		rblk.hasCached = false
	}
}

func (adapter *DeclarativeModbusAdapter) badUpdatesLocked(at time.Time) []ChannelUpdate {
	updates := make([]ChannelUpdate, 0, len(adapter.channels))
	for _, ch := range adapter.channels {
		adapter.sequence++
		updates = append(updates, ChannelUpdate{
			Address: ch.Address(),
			Sample: Sample{
				Value:         zeroValueForType(ch.DataType),
				Quality:       QualityBad,
				QualityReason: QualityReasonCommunicationFault,
				ObservedAt:    at,
				Sequence:      adapter.sequence,
			},
		})
	}
	return updates
}

func (adapter *DeclarativeModbusAdapter) Apply(ctx context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	if adapter == nil {
		return nil, errors.New("declarative Modbus adapter is nil")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		spec, exists := adapter.specsByAddress[decision.Address]
		if !exists {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_CHANNEL_INVALID"})
			continue
		}
		if chIdx, idxOK := adapter.channelIdxByAddress[decision.Address]; idxOK && IsHardRealtimeChannel(adapter.channels[chIdx]) {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: ReasonHardRealtimeSafetyBoundary})
			continue
		}
		if spec.WriteFunctionCode == 0 {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "CHANNEL_READ_ONLY"})
			continue
		}
		if !decision.Accepted || decision.Effective == nil {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_VALUE_MISSING"})
			continue
		}

		values, code, err := adapter.prepareWriteValuesLocked(ctx, spec, *decision.Effective)
		if err != nil {
			return results, err
		}
		if code != "" {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: code})
			continue
		}
		if spec.WriteFunctionCode == ModbusFunctionWriteSingleRegister && len(values) != 1 {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "INVALID_WRITE_FUNCTION"})
			continue
		}

		if err := adapter.transport.Write(ctx, ModbusWriteTask{
			Name:         fmt.Sprintf("%s-%s-fc%d", adapter.component.ID, spec.PointCode, spec.WriteFunctionCode),
			UnitID:       adapter.profile.UnitID,
			FunctionCode: spec.WriteFunctionCode,
			Address:      spec.Address,
			Values:       values,
		}); err != nil {
			return results, err
		}

		readbackAt := time.Now().UTC()
		readRegs, readErr := adapter.transport.Read(ctx, ModbusReadTask{
			Name:         fmt.Sprintf("%s-%s-readback", adapter.component.ID, spec.PointCode),
			UnitID:       adapter.profile.UnitID,
			FunctionCode: spec.FunctionCode,
			Address:      spec.Address,
			Quantity:     spec.RegisterCount,
		})
		if readErr != nil || len(readRegs) != int(spec.RegisterCount) {
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_FAILED",
				ReadbackVerified: false,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		sample := decodeRegisterSample(spec, readRegs, readbackAt)
		if sample.Quality != QualityGood {
			valCopy := sample.Value
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_BAD_QUALITY",
				ReadbackVerified: false,
				ReadbackValue:    &valCopy,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		valCopy := sample.Value
		if !readbackMatchesTarget(spec, *decision.Effective, sample.Value) {
			results = append(results, DeviceWriteResult{
				Address:          decision.Address,
				Success:          false,
				Code:             "LOCAL_READBACK_MISMATCH",
				AppliedValue:     &valCopy,
				ReadbackVerified: false,
				ReadbackValue:    &valCopy,
				ReadbackAt:       readbackAt,
			})
			continue
		}
		applied := valCopy
		readback := valCopy
		results = append(results, DeviceWriteResult{
			Address:          decision.Address,
			Success:          true,
			Code:             "APPLIED",
			AppliedValue:     &applied,
			ReadbackVerified: true,
			ReadbackValue:    &readback,
			ReadbackAt:       readbackAt,
		})
	}
	return results, nil
}

func readbackMatchesTarget(spec ModbusRegisterSpec, target, actual Value) bool {
	switch spec.RawType {
	case ModbusRawBitBool:
		return target.Type == DataTypeBoolean && actual.Type == DataTypeBoolean && actual.Boolean == target.Boolean
	case ModbusRawEnum16:
		if actual.Type != DataTypeString {
			return false
		}
		if target.Type == DataTypeString {
			return actual.String == target.String
		}
		num, ok := target.NumericFloat64()
		if !ok || math.IsNaN(num) || math.IsInf(num, 0) || math.Trunc(num) != num || num < 0 || num > 65535 {
			return false
		}
		expectedLabel, exists := spec.EnumMap[strconv.FormatUint(uint64(num), 10)]
		return exists && actual.String == expectedLabel
	default:
		expected, ok1 := target.NumericFloat64()
		got, ok2 := actual.NumericFloat64()
		if !ok1 || !ok2 || math.IsNaN(expected) || math.IsNaN(got) || math.IsInf(expected, 0) || math.IsInf(got, 0) {
			return false
		}
		tol := math.Max(math.Abs(spec.Scale)*0.55, 1e-5) + 1e-6*math.Abs(expected)
		return math.Abs(got-expected) <= tol
	}
}

func (adapter *DeclarativeModbusAdapter) prepareWriteValuesLocked(ctx context.Context, spec ModbusRegisterSpec, val Value) ([]uint16, string, error) {
	if spec.RawType != ModbusRawBitBool {
		values, code := encodeRegisterWrite(spec, val)
		return values, code, nil
	}
	if val.Type != DataTypeBoolean {
		return nil, "INVALID_WRITE_VALUE", nil
	}
	regs, err := adapter.transport.Read(ctx, ModbusReadTask{
		Name:         fmt.Sprintf("%s-%s-rmw-read", adapter.component.ID, spec.PointCode),
		UnitID:       adapter.profile.UnitID,
		FunctionCode: ModbusFunctionReadHoldingRegisters,
		Address:      spec.Address,
		Quantity:     1,
	})
	if err == nil && len(regs) != 1 {
		err = fmt.Errorf("short Modbus read during BIT_BOOL read-modify-write: got %d registers, want 1", len(regs))
	}
	if err != nil {
		return nil, "", err
	}
	wireWord := applyBitInHoldingRegister(regs[0], spec.ByteOrder, spec.BitIndex, val.Boolean)
	return []uint16{wireWord}, "", nil
}
