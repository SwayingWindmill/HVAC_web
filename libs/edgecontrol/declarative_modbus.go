package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxModbusBlockSpan = 64
	MaxModbusBlockGap  = 4

	modbusFaultBaseBackoff = 2 * time.Second
	modbusFaultMaxBackoff  = 5 * time.Minute

	QualityReasonCommunicationFault = "COMMUNICATION_FAULT"
	QualityReasonNonFiniteFloat     = "NON_FINITE_FLOAT"
)

var (
	ErrCommunicationFault = errors.New(QualityReasonCommunicationFault)
	pointCodePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

type ModbusDriverType string

const (
	ModbusDriverTCP ModbusDriverType = "MODBUS_TCP"
	ModbusDriverRTU ModbusDriverType = "MODBUS_RTU"
)

type ModbusRawType string

const (
	ModbusRawUint16  ModbusRawType = "UINT16"
	ModbusRawInt16   ModbusRawType = "INT16"
	ModbusRawUint32  ModbusRawType = "UINT32"
	ModbusRawInt32   ModbusRawType = "INT32"
	ModbusRawFloat32 ModbusRawType = "FLOAT32"
	ModbusRawBitBool ModbusRawType = "BIT_BOOL"
	ModbusRawEnum16  ModbusRawType = "ENUM16"
)

type ModbusByteOrder string

const (
	ModbusByteOrderBigEndian    ModbusByteOrder = "BIG_ENDIAN"
	ModbusByteOrderLittleEndian ModbusByteOrder = "LITTLE_ENDIAN"
)

type ModbusWordOrder string

const (
	ModbusWordOrderBigEndian    ModbusWordOrder = "BIG_ENDIAN"
	ModbusWordOrderLittleEndian ModbusWordOrder = "LITTLE_ENDIAN"
)

// ModbusRegisterSpec describes one declarative Modbus register-to-Channel mapping.
type ModbusRegisterSpec struct {
	PointCode         string             `json:"pointCode"`
	PointID           string             `json:"pointId,omitempty"`
	ChannelID         string             `json:"channelId,omitempty"`
	Description       string             `json:"description,omitempty"`
	Unit              string             `json:"unit,omitempty"`
	SemanticChannel   SemanticChannel    `json:"semanticChannel,omitempty"`
	FunctionCode      ModbusFunctionCode `json:"functionCode"`
	WriteFunctionCode ModbusFunctionCode `json:"writeFunctionCode,omitzero"`
	Address           uint16             `json:"address"`
	RegisterCount     uint16             `json:"registerCount"`
	RawType           ModbusRawType      `json:"rawType"`
	ByteOrder         ModbusByteOrder    `json:"byteOrder"`
	WordOrder         ModbusWordOrder    `json:"wordOrder"`
	BitIndex          int                `json:"bitIndex,omitempty"`
	Scale             float64            `json:"scale"`
	Offset            float64            `json:"offset"`
	PollPriority      DataPriority       `json:"pollPriority"`
	EnumMap           map[string]string  `json:"enumMap,omitempty"`
}

// DeclarativeModbusProfile configures a declarative Modbus DeviceAdapter.
type DeclarativeModbusProfile struct {
	ComponentID       string               `json:"deviceKey"`
	Alias             string               `json:"alias,omitempty"`
	DriverType        ModbusDriverType     `json:"driverType"`
	Endpoint          string               `json:"endpoint"`
	UnitID            uint8                `json:"unitId"`
	CapabilityProfile CapabilityProfileID  `json:"capabilityProfile,omitempty"`
	Timeout           time.Duration        `json:"-"`
	TimeoutMS         int                  `json:"timeoutMs"`
	Retries           int                  `json:"retries"`
	Registers         []ModbusRegisterSpec `json:"registers"`
}

// ModbusReadBlock represents a coalesced contiguous Modbus register block read.
type ModbusReadBlock struct {
	FunctionCode ModbusFunctionCode
	StartAddress uint16
	Quantity     uint16
	PollPriority DataPriority
	Cadence      int
	Specs        []ModbusRegisterSpec
}

// ModbusFaultStatus exposes the adapter's current OpenEMS-style communication backoff state.
type ModbusFaultStatus struct {
	FaultActive         bool
	ConsecutiveFailures int
	LastError           string
	NextRetryAt         time.Time
}

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
// FC6/FC16, and enforcing OpenEMS-style linear fault backoff.
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

// Validate checks all profile and register specification invariants.
func (profile DeclarativeModbusProfile) Validate() error {
	return profile.validateWithTransport(false)
}

func (profile DeclarativeModbusProfile) validateWithTransport(hasExternalTransport bool) error {
	componentID := strings.TrimSpace(profile.ComponentID)
	if componentID == "" || strings.Contains(componentID, "/") {
		return errors.New("declarative Modbus deviceKey/componentId is required and must not contain '/'")
	}
	if profile.UnitID < 1 || profile.UnitID > 247 {
		return fmt.Errorf("declarative Modbus unitId %d must be in [1,247]", profile.UnitID)
	}
	if profile.Retries < 0 || profile.Retries > 10 {
		return fmt.Errorf("declarative Modbus retries %d must be in [0,10]", profile.Retries)
	}
	if len(profile.Registers) == 0 || len(profile.Registers) > 256 {
		return fmt.Errorf("declarative Modbus registers count %d must be in [1,256]", len(profile.Registers))
	}

	if !hasExternalTransport || profile.DriverType != "" || strings.TrimSpace(profile.Endpoint) != "" {
		switch profile.DriverType {
		case ModbusDriverTCP:
			endpoint := strings.TrimSpace(profile.Endpoint)
			if endpoint == "" {
				return errors.New("declarative Modbus TCP endpoint is required")
			}
			if _, _, err := net.SplitHostPort(endpoint); err != nil {
				return fmt.Errorf("invalid Modbus TCP endpoint %q: %w", endpoint, err)
			}
		case ModbusDriverRTU:
			if strings.TrimSpace(profile.Endpoint) == "" {
				return errors.New("declarative Modbus RTU endpoint is required")
			}
		default:
			if !hasExternalTransport || profile.DriverType != "" {
				return fmt.Errorf("unsupported Modbus driverType %q", profile.DriverType)
			}
		}
	}

	timeout := profile.effectiveTimeout()
	if !hasExternalTransport || timeout != 0 {
		if timeout < 100*time.Millisecond || timeout > 30*time.Second {
			return fmt.Errorf("declarative Modbus timeout %s must be between 100ms and 30s", timeout)
		}
	}

	var profileReqs map[SemanticChannel]ChannelRequirement
	if profile.CapabilityProfile != "" {
		std, found := findStandardProfile(profile.CapabilityProfile)
		if !found {
			return fmt.Errorf("unknown capabilityProfile %q", profile.CapabilityProfile)
		}
		profileReqs = make(map[SemanticChannel]ChannelRequirement, len(std.Channels))
		for _, req := range std.Channels {
			profileReqs[req.Semantic] = req
		}
	}

	seenPoints := make(map[string]struct{}, len(profile.Registers))
	seenChannels := make(map[string]struct{}, len(profile.Registers))
	seenSemantics := make(map[SemanticChannel]struct{}, len(profile.Registers))
	type bitKey struct {
		fc   ModbusFunctionCode
		addr uint16
		bit  int
	}
	seenBits := make(map[bitKey]struct{})

	for idx, spec := range profile.Registers {
		if err := spec.Validate(); err != nil {
			return fmt.Errorf("register[%d] (%s): %w", idx, spec.PointCode, err)
		}
		if _, dup := seenPoints[spec.PointCode]; dup {
			return fmt.Errorf("duplicate register pointCode %q", spec.PointCode)
		}
		seenPoints[spec.PointCode] = struct{}{}

		chID := spec.effectiveChannelID()
		if _, dup := seenChannels[chID]; dup {
			return fmt.Errorf("duplicate channelId %q", chID)
		}
		seenChannels[chID] = struct{}{}

		if spec.RawType == ModbusRawBitBool {
			bk := bitKey{fc: spec.FunctionCode, addr: spec.Address, bit: spec.BitIndex}
			if _, dup := seenBits[bk]; dup {
				return fmt.Errorf("duplicate BIT_BOOL mapping at FC%d addr=%d bit=%d", spec.FunctionCode, spec.Address, spec.BitIndex)
			}
			seenBits[bk] = struct{}{}
		}

		if spec.SemanticChannel != "" {
			if _, dup := seenSemantics[spec.SemanticChannel]; dup {
				return fmt.Errorf("duplicate semanticChannel %q", spec.SemanticChannel)
			}
			seenSemantics[spec.SemanticChannel] = struct{}{}
			if profileReqs != nil {
				req, declared := profileReqs[spec.SemanticChannel]
				if !declared {
					return fmt.Errorf("semanticChannel %q is not declared by capabilityProfile %s", spec.SemanticChannel, profile.CapabilityProfile)
				}
				if spec.channelDataType() != req.DataType {
					return fmt.Errorf("semanticChannel %q expects %s, got %s", spec.SemanticChannel, req.DataType, spec.channelDataType())
				}
				if !accessSatisfies(spec.channelAccess(), req.Access, true) {
					return fmt.Errorf("semanticChannel %q expects access %s, got %s", spec.SemanticChannel, req.Access, spec.channelAccess())
				}
			}
		}
	}
	return nil
}

// Validate validates a single Modbus register mapping.
func (spec ModbusRegisterSpec) Validate() error {
	if !pointCodePattern.MatchString(spec.PointCode) {
		return fmt.Errorf("invalid pointCode %q", spec.PointCode)
	}
	if strings.Contains(spec.effectiveChannelID(), "/") {
		return errors.New("channelId must not contain '/'")
	}
	if spec.FunctionCode != ModbusFunctionReadHoldingRegisters && spec.FunctionCode != ModbusFunctionReadInputRegisters {
		return fmt.Errorf("unsupported read functionCode %d", spec.FunctionCode)
	}
	if spec.WriteFunctionCode != 0 {
		if spec.WriteFunctionCode != ModbusFunctionWriteSingleRegister && spec.WriteFunctionCode != ModbusFunctionWriteMultipleRegisters {
			return fmt.Errorf("unsupported writeFunctionCode %d", spec.WriteFunctionCode)
		}
		if spec.FunctionCode == ModbusFunctionReadInputRegisters {
			return errors.New("input register (FC4) cannot declare writeFunctionCode")
		}
	}
	if spec.RegisterCount < 1 || spec.RegisterCount > 4 {
		return fmt.Errorf("registerCount %d must be in [1,4]", spec.RegisterCount)
	}
	if uint32(spec.Address)+uint32(spec.RegisterCount) > 65536 {
		return fmt.Errorf("address %d + registerCount %d exceeds 16-bit Modbus register space", spec.Address, spec.RegisterCount)
	}

	expectedCount, err := expectedRegisterCount(spec.RawType)
	if err != nil {
		return err
	}
	if spec.RegisterCount != expectedCount {
		return fmt.Errorf("rawType %s requires registerCount %d, got %d", spec.RawType, expectedCount, spec.RegisterCount)
	}
	if spec.WriteFunctionCode == ModbusFunctionWriteSingleRegister && spec.RegisterCount != 1 {
		return fmt.Errorf("writeFunctionCode 6 requires registerCount 1, got %d", spec.RegisterCount)
	}
	if spec.ByteOrder != ModbusByteOrderBigEndian && spec.ByteOrder != ModbusByteOrderLittleEndian {
		return fmt.Errorf("unsupported byteOrder %q", spec.ByteOrder)
	}
	if spec.WordOrder != ModbusWordOrderBigEndian && spec.WordOrder != ModbusWordOrderLittleEndian {
		return fmt.Errorf("unsupported wordOrder %q", spec.WordOrder)
	}
	if spec.BitIndex < 0 || spec.BitIndex > 15 {
		return fmt.Errorf("bitIndex %d must be in [0,15]", spec.BitIndex)
	}
	if spec.RawType != ModbusRawBitBool && spec.BitIndex != 0 {
		return fmt.Errorf("bitIndex is only valid for BIT_BOOL, got %s", spec.RawType)
	}
	if math.IsNaN(spec.Scale) || math.IsInf(spec.Scale, 0) {
		return errors.New("scale must be a finite number")
	}
	if math.IsNaN(spec.Offset) || math.IsInf(spec.Offset, 0) {
		return errors.New("offset must be a finite number")
	}
	if spec.RawType != ModbusRawBitBool && spec.RawType != ModbusRawEnum16 && spec.Scale == 0 {
		return errors.New("scale must be non-zero for numeric register types")
	}
	if !validPriority(spec.PollPriority) {
		return fmt.Errorf("unsupported pollPriority %q", spec.PollPriority)
	}
	if spec.RawType == ModbusRawEnum16 {
		if len(spec.EnumMap) == 0 {
			return errors.New("ENUM16 register requires non-empty enumMap")
		}
		for k, v := range spec.EnumMap {
			if _, parseErr := strconv.ParseUint(strings.TrimSpace(k), 10, 16); parseErr != nil {
				return fmt.Errorf("ENUM16 enumMap key %q must be an unsigned 16-bit integer", k)
			}
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("ENUM16 enumMap value for key %q must not be empty", k)
			}
		}
	} else if len(spec.EnumMap) > 0 {
		return fmt.Errorf("enumMap is only valid for ENUM16, got %s", spec.RawType)
	}
	return nil
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

// CoalesceModbusReadBlocks groups registers by FunctionCode and polling cadence,
// merging contiguous or nearby registers within span <= 64 and gap <= 4.
func CoalesceModbusReadBlocks(specs []ModbusRegisterSpec) []ModbusReadBlock {
	if len(specs) == 0 {
		return nil
	}
	type groupKey struct {
		fc      ModbusFunctionCode
		cadence int
	}
	grouped := make(map[groupKey][]ModbusRegisterSpec)
	for _, spec := range specs {
		key := groupKey{fc: spec.FunctionCode, cadence: pollCadence(spec.PollPriority)}
		grouped[key] = append(grouped[key], cloneRegisterSpec(spec))
	}

	var blocks []ModbusReadBlock
	for key, groupSpecs := range grouped {
		sort.Slice(groupSpecs, func(i, j int) bool {
			if groupSpecs[i].Address != groupSpecs[j].Address {
				return groupSpecs[i].Address < groupSpecs[j].Address
			}
			if groupSpecs[i].RegisterCount != groupSpecs[j].RegisterCount {
				return groupSpecs[i].RegisterCount < groupSpecs[j].RegisterCount
			}
			return groupSpecs[i].PointCode < groupSpecs[j].PointCode
		})

		curStart := uint32(groupSpecs[0].Address)
		curEnd := curStart + uint32(groupSpecs[0].RegisterCount)
		curBestPriority := groupSpecs[0].PollPriority
		curSpecs := []ModbusRegisterSpec{groupSpecs[0]}

		flush := func() {
			blocks = append(blocks, ModbusReadBlock{
				FunctionCode: key.fc,
				StartAddress: uint16(curStart),
				Quantity:     uint16(curEnd - curStart),
				PollPriority: curBestPriority,
				Cadence:      key.cadence,
				Specs:        curSpecs,
			})
		}

		for _, spec := range groupSpecs[1:] {
			specStart := uint32(spec.Address)
			specEnd := specStart + uint32(spec.RegisterCount)
			var gap uint32
			if specStart > curEnd {
				gap = specStart - curEnd
			}
			newEnd := curEnd
			if specEnd > newEnd {
				newEnd = specEnd
			}
			if gap <= MaxModbusBlockGap && (newEnd-curStart) <= MaxModbusBlockSpan {
				curEnd = newEnd
				curSpecs = append(curSpecs, spec)
				if priorityRank(spec.PollPriority) < priorityRank(curBestPriority) {
					curBestPriority = spec.PollPriority
				}
				continue
			}
			flush()
			curStart = specStart
			curEnd = specEnd
			curBestPriority = spec.PollPriority
			curSpecs = []ModbusRegisterSpec{spec}
		}
		flush()
	}

	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Cadence != blocks[j].Cadence {
			return blocks[i].Cadence < blocks[j].Cadence
		}
		ri, rj := priorityRank(blocks[i].PollPriority), priorityRank(blocks[j].PollPriority)
		if ri != rj {
			return ri < rj
		}
		if blocks[i].FunctionCode != blocks[j].FunctionCode {
			return blocks[i].FunctionCode < blocks[j].FunctionCode
		}
		return blocks[i].StartAddress < blocks[j].StartAddress
	})
	return blocks
}

func (adapter *DeclarativeModbusAdapter) Component() ComponentDescriptor {
	return cloneComponent(adapter.component)
}

func (adapter *DeclarativeModbusAdapter) Channels() []ChannelDescriptor {
	return slices.Clone(adapter.channels)
}

func (adapter *DeclarativeModbusAdapter) ReadBlocks() []ModbusReadBlock {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	out := make([]ModbusReadBlock, len(adapter.blocks))
	for i, blk := range adapter.blocks {
		cp := blk.block
		cp.Specs = append([]ModbusRegisterSpec(nil), blk.block.Specs...)
		out[i] = cp
	}
	return out
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
	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		spec, exists := adapter.specsByAddress[decision.Address]
		if !exists {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_CHANNEL_INVALID"})
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

		values, code := encodeRegisterWrite(spec, *decision.Effective)
		if code != "" {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: code})
			continue
		}
		if spec.WriteFunctionCode == ModbusFunctionWriteSingleRegister && len(values) != 1 {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "INVALID_WRITE_FUNCTION"})
			continue
		}

		err := adapter.transport.Write(ctx, ModbusWriteTask{
			Name:         fmt.Sprintf("%s-%s-fc%d", adapter.component.ID, spec.PointCode, spec.WriteFunctionCode),
			UnitID:       adapter.profile.UnitID,
			FunctionCode: spec.WriteFunctionCode,
			Address:      spec.Address,
			Values:       values,
		})
		if err != nil {
			return results, err
		}
		applied := *decision.Effective
		results = append(results, DeviceWriteResult{
			Address:      decision.Address,
			Success:      true,
			Code:         "APPLIED",
			AppliedValue: &applied,
		})
	}
	return results, nil
}

func decodeRegisterSample(spec ModbusRegisterSpec, raw []uint16, at time.Time) Sample {
	switch spec.RawType {
	case ModbusRawBitBool:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		bitSet := (word & (uint16(1) << spec.BitIndex)) != 0
		return Sample{Value: BooleanValue(bitSet), Quality: QualityGood, ObservedAt: at}
	case ModbusRawEnum16:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		key := strconv.FormatUint(uint64(word), 10)
		if label, ok := spec.EnumMap[key]; ok {
			return Sample{Value: StringValue(label), Quality: QualityGood, ObservedAt: at}
		}
		return Sample{Value: StringValue(key), Quality: QualitySuspect, ObservedAt: at}
	case ModbusRawUint16:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		return finiteScaledSample(float64(word)*spec.Scale+spec.Offset, at)
	case ModbusRawInt16:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		return finiteScaledSample(float64(int16(word))*spec.Scale+spec.Offset, at)
	case ModbusRawUint32:
		u32 := assembleUint32(raw[0], raw[1], spec.ByteOrder, spec.WordOrder)
		return finiteScaledSample(float64(u32)*spec.Scale+spec.Offset, at)
	case ModbusRawInt32:
		u32 := assembleUint32(raw[0], raw[1], spec.ByteOrder, spec.WordOrder)
		return finiteScaledSample(float64(int32(u32))*spec.Scale+spec.Offset, at)
	case ModbusRawFloat32:
		u32 := assembleUint32(raw[0], raw[1], spec.ByteOrder, spec.WordOrder)
		f32 := math.Float32frombits(u32)
		if math.IsNaN(float64(f32)) || math.IsInf(float64(f32), 0) {
			return Sample{Value: DoubleValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
		}
		return finiteScaledSample(float64(f32)*spec.Scale+spec.Offset, at)
	default:
		return Sample{Value: zeroValueForType(spec.channelDataType()), Quality: QualityUnavailable, ObservedAt: at}
	}
}

func finiteScaledSample(val float64, at time.Time) Sample {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return Sample{Value: DoubleValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
	}
	return Sample{Value: DoubleValue(val), Quality: QualityGood, ObservedAt: at}
}

func encodeRegisterWrite(spec ModbusRegisterSpec, val Value) ([]uint16, string) {
	switch spec.RawType {
	case ModbusRawBitBool:
		if val.Type != DataTypeBoolean {
			return nil, "INVALID_WRITE_VALUE"
		}
		var word uint16
		if val.Boolean {
			word = uint16(1) << spec.BitIndex
		}
		return []uint16{applyByteOrder16(word, spec.ByteOrder)}, ""
	case ModbusRawEnum16:
		switch val.Type {
		case DataTypeString:
			for rawKey, label := range spec.EnumMap {
				if label == val.String {
					parsed, err := strconv.ParseUint(strings.TrimSpace(rawKey), 10, 16)
					if err != nil {
						return nil, "INVALID_WRITE_VALUE"
					}
					return []uint16{applyByteOrder16(uint16(parsed), spec.ByteOrder)}, ""
				}
			}
			return nil, "INVALID_WRITE_VALUE"
		default:
			num, ok := val.NumericFloat64()
			if !ok || math.IsNaN(num) || math.IsInf(num, 0) || math.Trunc(num) != num || num < 0 || num > 65535 {
				return nil, "INVALID_WRITE_VALUE"
			}
			key := strconv.FormatUint(uint64(num), 10)
			if _, exists := spec.EnumMap[key]; !exists {
				return nil, "INVALID_WRITE_VALUE"
			}
			return []uint16{applyByteOrder16(uint16(num), spec.ByteOrder)}, ""
		}
	default:
		num, ok := val.NumericFloat64()
		if !ok || math.IsNaN(num) || math.IsInf(num, 0) || spec.Scale == 0 {
			return nil, "INVALID_WRITE_VALUE"
		}
		rawFloat := (num - spec.Offset) / spec.Scale
		if math.IsNaN(rawFloat) || math.IsInf(rawFloat, 0) {
			return nil, "INVALID_WRITE_VALUE"
		}
		switch spec.RawType {
		case ModbusRawUint16:
			rounded := math.Round(rawFloat)
			if rounded < 0 || rounded > math.MaxUint16 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return []uint16{applyByteOrder16(uint16(rounded), spec.ByteOrder)}, ""
		case ModbusRawInt16:
			rounded := math.Round(rawFloat)
			if rounded < math.MinInt16 || rounded > math.MaxInt16 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return []uint16{applyByteOrder16(uint16(int16(rounded)), spec.ByteOrder)}, ""
		case ModbusRawUint32:
			rounded := math.Round(rawFloat)
			if rounded < 0 || rounded > math.MaxUint32 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return splitUint32(uint32(rounded), spec.ByteOrder, spec.WordOrder), ""
		case ModbusRawInt32:
			rounded := math.Round(rawFloat)
			if rounded < math.MinInt32 || rounded > math.MaxInt32 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return splitUint32(uint32(int32(rounded)), spec.ByteOrder, spec.WordOrder), ""
		case ModbusRawFloat32:
			if math.Abs(rawFloat) > math.MaxFloat32 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			f32 := float32(rawFloat)
			if math.IsNaN(float64(f32)) || math.IsInf(float64(f32), 0) {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return splitUint32(math.Float32bits(f32), spec.ByteOrder, spec.WordOrder), ""
		default:
			return nil, "INVALID_WRITE_VALUE"
		}
	}
}

func applyByteOrder16(word uint16, order ModbusByteOrder) uint16 {
	if order == ModbusByteOrderLittleEndian {
		return (word >> 8) | ((word & 0x00FF) << 8)
	}
	return word
}

func assembleUint32(r0, r1 uint16, byteOrder ModbusByteOrder, wordOrder ModbusWordOrder) uint32 {
	w0 := applyByteOrder16(r0, byteOrder)
	w1 := applyByteOrder16(r1, byteOrder)
	if wordOrder == ModbusWordOrderLittleEndian {
		return (uint32(w1) << 16) | uint32(w0)
	}
	return (uint32(w0) << 16) | uint32(w1)
}

func splitUint32(u32 uint32, byteOrder ModbusByteOrder, wordOrder ModbusWordOrder) []uint16 {
	hi := uint16(u32 >> 16)
	lo := uint16(u32 & 0xFFFF)
	if wordOrder == ModbusWordOrderLittleEndian {
		return []uint16{applyByteOrder16(lo, byteOrder), applyByteOrder16(hi, byteOrder)}
	}
	return []uint16{applyByteOrder16(hi, byteOrder), applyByteOrder16(lo, byteOrder)}
}

func pollCadence(priority DataPriority) int {
	switch priority {
	case PriorityVeryHigh, PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 4
	default:
		return 1
	}
}

func expectedRegisterCount(rawType ModbusRawType) (uint16, error) {
	switch rawType {
	case ModbusRawUint16, ModbusRawInt16, ModbusRawBitBool, ModbusRawEnum16:
		return 1, nil
	case ModbusRawUint32, ModbusRawInt32, ModbusRawFloat32:
		return 2, nil
	default:
		return 0, fmt.Errorf("unsupported rawType %q", rawType)
	}
}

func (spec ModbusRegisterSpec) effectiveChannelID() string {
	if trimmed := strings.TrimSpace(spec.ChannelID); trimmed != "" {
		return trimmed
	}
	return spec.PointCode
}

func (spec ModbusRegisterSpec) channelDataType() DataType {
	switch spec.RawType {
	case ModbusRawBitBool:
		return DataTypeBoolean
	case ModbusRawEnum16:
		return DataTypeString
	default:
		return DataTypeDouble
	}
}

func (spec ModbusRegisterSpec) channelAccess() AccessMode {
	if spec.WriteFunctionCode == 0 {
		return AccessReadOnly
	}
	return AccessReadWrite
}

func (profile DeclarativeModbusProfile) effectiveTimeout() time.Duration {
	if profile.Timeout > 0 {
		return profile.Timeout
	}
	if profile.TimeoutMS > 0 {
		return time.Duration(profile.TimeoutMS) * time.Millisecond
	}
	return 0
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

func findStandardProfile(id CapabilityProfileID) (CapabilityProfile, bool) {
	for _, profile := range StandardCapabilityProfiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return CapabilityProfile{}, false
}

func zeroValueForType(dataType DataType) Value {
	switch dataType {
	case DataTypeBoolean:
		return BooleanValue(false)
	case DataTypeString:
		return StringValue("")
	case DataTypeInteger:
		return IntegerValue(0)
	case DataTypeLong:
		return LongValue(0)
	case DataTypeFloat:
		return FloatValue(0)
	default:
		return DoubleValue(0)
	}
}

func cloneDeclarativeProfile(profile DeclarativeModbusProfile) DeclarativeModbusProfile {
	out := profile
	if profile.TimeoutMS <= 0 && profile.Timeout > 0 {
		out.TimeoutMS = int(profile.Timeout.Milliseconds())
	}
	if profile.Timeout <= 0 && profile.TimeoutMS > 0 {
		out.Timeout = time.Duration(profile.TimeoutMS) * time.Millisecond
	}
	out.Registers = make([]ModbusRegisterSpec, len(profile.Registers))
	for i, spec := range profile.Registers {
		out.Registers[i] = cloneRegisterSpec(spec)
	}
	return out
}

func cloneRegisterSpec(spec ModbusRegisterSpec) ModbusRegisterSpec {
	if spec.EnumMap != nil {
		spec.EnumMap = cloneStringMap(spec.EnumMap)
	}
	return spec
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
