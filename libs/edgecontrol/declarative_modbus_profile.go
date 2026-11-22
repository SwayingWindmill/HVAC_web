package edgecontrol

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	MaxModbusBlockSpan = 64
	MaxModbusBlockGap  = 4
)

var pointCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

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
	PointCode         string              `json:"pointCode"`
	PointID           string              `json:"pointId,omitempty"`
	ChannelID         string              `json:"channelId,omitempty"`
	Description       string              `json:"description,omitempty"`
	Unit              string              `json:"unit,omitempty"`
	SemanticChannel   SemanticChannel     `json:"semanticChannel,omitempty"`
	SafetyBoundary    SafetyBoundaryClass `json:"safetyBoundary,omitempty"`
	FunctionCode      ModbusFunctionCode  `json:"functionCode"`
	WriteFunctionCode ModbusFunctionCode  `json:"writeFunctionCode,omitzero"`
	Address           uint16              `json:"address"`
	RegisterCount     uint16              `json:"registerCount"`
	RawType           ModbusRawType       `json:"rawType"`
	ByteOrder         ModbusByteOrder     `json:"byteOrder"`
	WordOrder         ModbusWordOrder     `json:"wordOrder"`
	BitIndex          int                 `json:"bitIndex,omitempty"`
	Scale             float64             `json:"scale"`
	Offset            float64             `json:"offset"`
	PollPriority      DataPriority        `json:"pollPriority"`
	EnumMap           map[string]string   `json:"enumMap,omitempty"`
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

// Validate checks all profile and register specification invariants.
func (profile DeclarativeModbusProfile) Validate() error {
	return profile.validateWithTransport(false)
}

// Validate validates a single Modbus register mapping.
func (spec ModbusRegisterSpec) Validate() error {
	var writeFC *int
	if spec.WriteFunctionCode != 0 {
		w := int(spec.WriteFunctionCode)
		writeFC = &w
	}
	var bitIdx *int
	if spec.RawType == ModbusRawBitBool || spec.BitIndex != 0 {
		b := spec.BitIndex
		bitIdx = &b
	}
	_, err := validateRegisterInput(ModbusRegisterInput{
		PointCode:         spec.PointCode,
		SemanticChannel:   string(spec.SemanticChannel),
		SafetyBoundary:    string(spec.SafetyBoundary),
		FunctionCode:      int(spec.FunctionCode),
		WriteFunctionCode: writeFC,
		Address:           int(spec.Address),
		RegisterCount:     int(spec.RegisterCount),
		RawType:           string(spec.RawType),
		ByteOrder:         string(spec.ByteOrder),
		WordOrder:         string(spec.WordOrder),
		BitIndex:          bitIdx,
		Scale:             spec.Scale,
		Offset:            spec.Offset,
		PollPriority:      string(spec.PollPriority),
		EnumMap:           spec.EnumMap,
	})
	return err
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
