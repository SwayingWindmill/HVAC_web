package edgecontrol

import (
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ModbusRegisterInput describes a wire/config-shadow Modbus register mapping
// prior to narrowing integer fields into ModbusRegisterSpec.
type ModbusRegisterInput struct {
	PointCode         string            `json:"pointCode"`
	SemanticChannel   string            `json:"semanticChannel,omitempty"`
	FunctionCode      int               `json:"functionCode"`
	WriteFunctionCode *int              `json:"writeFunctionCode,omitempty"`
	Address           int               `json:"address"`
	RegisterCount     int               `json:"registerCount"`
	RawType           string            `json:"rawType"`
	ByteOrder         string            `json:"byteOrder"`
	WordOrder         string            `json:"wordOrder"`
	BitIndex          *int              `json:"bitIndex,omitempty"`
	Scale             float64           `json:"scale"`
	Offset            float64           `json:"offset"`
	PollPriority      string            `json:"pollPriority"`
	EnumMap           map[string]string `json:"enumMap,omitempty"`
}

// ModbusDriverInput describes a wire/config-shadow/template Modbus driver
// specification prior to narrowing into DeclarativeModbusProfile.
type ModbusDriverInput struct {
	DeviceKey         string                `json:"deviceKey"`
	DriverType        string                `json:"driverType"`
	Endpoint          *string               `json:"endpoint,omitempty"`
	UnitID            *int                  `json:"unitId,omitempty"`
	CapabilityProfile string                `json:"capabilityProfile,omitempty"`
	TimeoutMS         *int                  `json:"timeoutMs,omitempty"`
	Retries           *int                  `json:"retries,omitempty"`
	Registers         []ModbusRegisterInput `json:"registers"`
}

// ModbusDriverValidationOptions controls whether deployment-specific gateway
// bindings (deviceKey, endpoint, unitId, timeoutMs, retries) are required.
type ModbusDriverValidationOptions struct {
	RequireDeviceKey       bool
	RequireGatewayBindings bool
}

// ValidateModbusDriverInput is the single canonical validator for declarative Modbus
// driver specs shared across Cloud config shadow, Registry templates, and Edge runtime.
func ValidateModbusDriverInput(input ModbusDriverInput, opts ModbusDriverValidationOptions) (DeclarativeModbusProfile, error) {
	return validateDriverInput(input, opts, false)
}

func (profile DeclarativeModbusProfile) validateWithTransport(hasExternalTransport bool) error {
	unitID := int(profile.UnitID)
	retries := profile.Retries
	input := ModbusDriverInput{
		DeviceKey:         profile.ComponentID,
		DriverType:        string(profile.DriverType),
		UnitID:            &unitID,
		CapabilityProfile: string(profile.CapabilityProfile),
		Retries:           &retries,
		Registers:         make([]ModbusRegisterInput, len(profile.Registers)),
	}
	allowOptionalTransport := hasExternalTransport && profile.DriverType == "" && strings.TrimSpace(profile.Endpoint) == ""
	if !allowOptionalTransport || profile.Endpoint != "" {
		ep := profile.Endpoint
		input.Endpoint = &ep
	}
	timeout := profile.effectiveTimeout()
	if !hasExternalTransport || timeout != 0 {
		if timeout < 100*time.Millisecond || timeout > 30*time.Second {
			return fmt.Errorf("declarative Modbus timeout %s must be between 100ms and 30s", timeout)
		}
		ms := int(timeout / time.Millisecond)
		input.TimeoutMS = &ms
	}

	seenChannels := make(map[string]struct{}, len(profile.Registers))
	for i, spec := range profile.Registers {
		chID := spec.effectiveChannelID()
		if strings.Contains(chID, "/") {
			return fmt.Errorf("register[%d] (%s): channelId must not contain '/'", i, spec.PointCode)
		}
		if _, dup := seenChannels[chID]; dup {
			return fmt.Errorf("duplicate channelId %q", chID)
		}
		seenChannels[chID] = struct{}{}

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
		input.Registers[i] = ModbusRegisterInput{
			PointCode:         spec.PointCode,
			SemanticChannel:   string(spec.SemanticChannel),
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
		}
	}

	_, err := validateDriverInput(input, ModbusDriverValidationOptions{
		RequireDeviceKey:       true,
		RequireGatewayBindings: !allowOptionalTransport,
	}, allowOptionalTransport)
	return err
}

func validateDriverInput(input ModbusDriverInput, opts ModbusDriverValidationOptions, allowEmptyTransport bool) (DeclarativeModbusProfile, error) {
	var out DeclarativeModbusProfile

	if opts.RequireDeviceKey {
		deviceKey := strings.TrimSpace(input.DeviceKey)
		if deviceKey == "" || utf8.RuneCountInString(deviceKey) > 128 || strings.Contains(deviceKey, "/") || containsModbusControlChar(deviceKey) {
			return DeclarativeModbusProfile{}, errors.New("deviceKey must be 1..128 characters without '/'")
		}
		out.ComponentID = deviceKey
	}

	driverType := ModbusDriverType(strings.TrimSpace(input.DriverType))
	if !allowEmptyTransport || driverType != "" {
		switch driverType {
		case ModbusDriverTCP, ModbusDriverRTU:
		default:
			return DeclarativeModbusProfile{}, fmt.Errorf("unsupported driverType %q", input.DriverType)
		}
	}
	out.DriverType = driverType

	if input.Endpoint == nil {
		if opts.RequireGatewayBindings {
			return DeclarativeModbusProfile{}, errors.New("endpoint is required")
		}
	} else {
		endpoint := strings.TrimSpace(*input.Endpoint)
		if endpoint == "" || utf8.RuneCountInString(endpoint) > 256 || containsModbusControlChar(endpoint) {
			return DeclarativeModbusProfile{}, errors.New("endpoint must be 1..256 characters")
		}
		if driverType == ModbusDriverTCP {
			if err := validateModbusTCPEndpoint(endpoint); err != nil {
				return DeclarativeModbusProfile{}, err
			}
		}
		out.Endpoint = endpoint
	}

	if input.UnitID == nil {
		if opts.RequireGatewayBindings {
			return DeclarativeModbusProfile{}, errors.New("unitId is required")
		}
	} else {
		if *input.UnitID < 1 || *input.UnitID > 247 {
			return DeclarativeModbusProfile{}, fmt.Errorf("unitId %d must be in [1,247]", *input.UnitID)
		}
		out.UnitID = uint8(*input.UnitID)
	}

	if input.TimeoutMS == nil {
		if opts.RequireGatewayBindings {
			return DeclarativeModbusProfile{}, errors.New("timeoutMs is required")
		}
	} else {
		if *input.TimeoutMS < 100 || *input.TimeoutMS > 30000 {
			return DeclarativeModbusProfile{}, fmt.Errorf("timeoutMs %d must be in [100,30000]", *input.TimeoutMS)
		}
		out.TimeoutMS = *input.TimeoutMS
		out.Timeout = time.Duration(*input.TimeoutMS) * time.Millisecond
	}

	if input.Retries == nil {
		if opts.RequireGatewayBindings {
			return DeclarativeModbusProfile{}, errors.New("retries is required")
		}
	} else {
		if *input.Retries < 0 || *input.Retries > 10 {
			return DeclarativeModbusProfile{}, fmt.Errorf("retries %d must be in [0,10]", *input.Retries)
		}
		out.Retries = *input.Retries
	}

	var profileReqs map[SemanticChannel]ChannelRequirement
	if input.CapabilityProfile != "" {
		capID := CapabilityProfileID(strings.TrimSpace(input.CapabilityProfile))
		if capID == "" {
			return DeclarativeModbusProfile{}, errors.New("capabilityProfile must not be blank")
		}
		std, found := findStandardProfile(capID)
		if !found {
			return DeclarativeModbusProfile{}, fmt.Errorf("unknown capabilityProfile %q", input.CapabilityProfile)
		}
		out.CapabilityProfile = capID
		profileReqs = make(map[SemanticChannel]ChannelRequirement, len(std.Channels))
		for _, req := range std.Channels {
			profileReqs[req.Semantic] = req
		}
	}

	if len(input.Registers) < 1 || len(input.Registers) > 256 {
		return DeclarativeModbusProfile{}, fmt.Errorf("registers count %d must be in [1,256]", len(input.Registers))
	}

	seenPoints := make(map[string]struct{}, len(input.Registers))
	seenSemantics := make(map[SemanticChannel]struct{}, len(input.Registers))
	type bitKey struct {
		fc   ModbusFunctionCode
		addr uint16
		bit  int
	}
	seenBits := make(map[bitKey]struct{})
	out.Registers = make([]ModbusRegisterSpec, len(input.Registers))

	for idx, reg := range input.Registers {
		spec, err := validateRegisterInput(reg)
		if err != nil {
			return DeclarativeModbusProfile{}, fmt.Errorf("registers[%d] (%s): %w", idx, reg.PointCode, err)
		}
		if _, dup := seenPoints[spec.PointCode]; dup {
			return DeclarativeModbusProfile{}, fmt.Errorf("duplicate register pointCode %q", spec.PointCode)
		}
		seenPoints[spec.PointCode] = struct{}{}

		if spec.RawType == ModbusRawBitBool {
			bk := bitKey{fc: spec.FunctionCode, addr: spec.Address, bit: spec.BitIndex}
			if _, dup := seenBits[bk]; dup {
				return DeclarativeModbusProfile{}, fmt.Errorf("duplicate BIT_BOOL mapping at FC%d addr=%d bit=%d", spec.FunctionCode, spec.Address, spec.BitIndex)
			}
			seenBits[bk] = struct{}{}
		}

		if spec.SemanticChannel != "" {
			if _, dup := seenSemantics[spec.SemanticChannel]; dup {
				return DeclarativeModbusProfile{}, fmt.Errorf("duplicate semanticChannel %q", spec.SemanticChannel)
			}
			seenSemantics[spec.SemanticChannel] = struct{}{}
			if profileReqs != nil {
				req, declared := profileReqs[spec.SemanticChannel]
				if !declared {
					return DeclarativeModbusProfile{}, fmt.Errorf("semanticChannel %q is not declared by capabilityProfile %s", spec.SemanticChannel, out.CapabilityProfile)
				}
				if spec.channelDataType() != req.DataType {
					return DeclarativeModbusProfile{}, fmt.Errorf("semanticChannel %q expects %s, got %s", spec.SemanticChannel, req.DataType, spec.channelDataType())
				}
				if !accessSatisfies(spec.channelAccess(), req.Access, true) {
					return DeclarativeModbusProfile{}, fmt.Errorf("semanticChannel %q expects access %s, got %s", spec.SemanticChannel, req.Access, spec.channelAccess())
				}
			}
		}
		out.Registers[idx] = spec
	}
	return out, nil
}

func validateRegisterInput(reg ModbusRegisterInput) (ModbusRegisterSpec, error) {
	if !pointCodePattern.MatchString(reg.PointCode) {
		return ModbusRegisterSpec{}, fmt.Errorf("invalid pointCode %q", reg.PointCode)
	}
	var semantic SemanticChannel
	if reg.SemanticChannel != "" {
		trimmed := strings.TrimSpace(reg.SemanticChannel)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 64 || strings.Contains(trimmed, "/") || containsModbusControlChar(trimmed) {
			return ModbusRegisterSpec{}, errors.New("semanticChannel must be 1..64 characters without '/'")
		}
		semantic = SemanticChannel(trimmed)
	}
	if reg.FunctionCode != int(ModbusFunctionReadHoldingRegisters) && reg.FunctionCode != int(ModbusFunctionReadInputRegisters) {
		return ModbusRegisterSpec{}, fmt.Errorf("unsupported read functionCode %d", reg.FunctionCode)
	}
	var writeFC ModbusFunctionCode
	if reg.WriteFunctionCode != nil {
		if *reg.WriteFunctionCode != int(ModbusFunctionWriteSingleRegister) && *reg.WriteFunctionCode != int(ModbusFunctionWriteMultipleRegisters) {
			return ModbusRegisterSpec{}, fmt.Errorf("unsupported writeFunctionCode %d", *reg.WriteFunctionCode)
		}
		if reg.FunctionCode == int(ModbusFunctionReadInputRegisters) {
			return ModbusRegisterSpec{}, errors.New("input register (FC4) cannot declare writeFunctionCode")
		}
		writeFC = ModbusFunctionCode(*reg.WriteFunctionCode)
	}
	if reg.Address < 0 || reg.Address > 65535 {
		return ModbusRegisterSpec{}, fmt.Errorf("address %d out of range [0,65535]", reg.Address)
	}

	rawType := ModbusRawType(strings.TrimSpace(reg.RawType))
	expectedCount, err := expectedRegisterCount(rawType)
	if err != nil {
		return ModbusRegisterSpec{}, err
	}
	if reg.RegisterCount != int(expectedCount) {
		return ModbusRegisterSpec{}, fmt.Errorf("rawType %s requires registerCount %d, got %d", rawType, expectedCount, reg.RegisterCount)
	}
	if reg.Address+reg.RegisterCount > 65536 {
		return ModbusRegisterSpec{}, fmt.Errorf("address %d + registerCount %d exceeds 16-bit Modbus register space", reg.Address, reg.RegisterCount)
	}
	if writeFC == ModbusFunctionWriteSingleRegister && reg.RegisterCount != 1 {
		return ModbusRegisterSpec{}, fmt.Errorf("writeFunctionCode 6 requires registerCount 1, got %d", reg.RegisterCount)
	}

	byteOrder := ModbusByteOrder(strings.TrimSpace(reg.ByteOrder))
	if byteOrder != ModbusByteOrderBigEndian && byteOrder != ModbusByteOrderLittleEndian {
		return ModbusRegisterSpec{}, fmt.Errorf("unsupported byteOrder %q", reg.ByteOrder)
	}
	wordOrder := ModbusWordOrder(strings.TrimSpace(reg.WordOrder))
	if wordOrder != ModbusWordOrderBigEndian && wordOrder != ModbusWordOrderLittleEndian {
		return ModbusRegisterSpec{}, fmt.Errorf("unsupported wordOrder %q", reg.WordOrder)
	}

	bitIndex := 0
	if rawType == ModbusRawBitBool {
		if reg.BitIndex == nil || *reg.BitIndex < 0 || *reg.BitIndex > 15 {
			return ModbusRegisterSpec{}, errors.New("bitIndex must be in [0,15] for BIT_BOOL")
		}
		bitIndex = *reg.BitIndex
	} else if reg.BitIndex != nil {
		return ModbusRegisterSpec{}, fmt.Errorf("bitIndex is only valid for BIT_BOOL, got %s", rawType)
	}

	if math.IsNaN(reg.Scale) || math.IsInf(reg.Scale, 0) || reg.Scale == 0 {
		return ModbusRegisterSpec{}, errors.New("scale must be a finite non-zero number")
	}
	if math.IsNaN(reg.Offset) || math.IsInf(reg.Offset, 0) {
		return ModbusRegisterSpec{}, errors.New("offset must be a finite number")
	}
	offset := reg.Offset
	if offset == 0 {
		offset = 0
	}

	priority := DataPriority(strings.TrimSpace(reg.PollPriority))
	if !validPriority(priority) {
		return ModbusRegisterSpec{}, fmt.Errorf("unsupported pollPriority %q", reg.PollPriority)
	}

	var enumMap map[string]string
	if rawType == ModbusRawEnum16 {
		enumMap, err = validateModbusEnumMap(reg.EnumMap)
		if err != nil {
			return ModbusRegisterSpec{}, err
		}
	} else if len(reg.EnumMap) > 0 {
		return ModbusRegisterSpec{}, fmt.Errorf("enumMap is only valid for ENUM16, got %s", rawType)
	}

	return ModbusRegisterSpec{
		PointCode:         reg.PointCode,
		SemanticChannel:   semantic,
		FunctionCode:      ModbusFunctionCode(reg.FunctionCode),
		WriteFunctionCode: writeFC,
		Address:           uint16(reg.Address),
		RegisterCount:     uint16(reg.RegisterCount),
		RawType:           rawType,
		ByteOrder:         byteOrder,
		WordOrder:         wordOrder,
		BitIndex:          bitIndex,
		Scale:             reg.Scale,
		Offset:            offset,
		PollPriority:      priority,
		EnumMap:           enumMap,
	}, nil
}

func validateModbusTCPEndpoint(endpoint string) error {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("invalid Modbus TCP endpoint %q: %w", endpoint, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("invalid Modbus TCP endpoint %q: host is required", endpoint)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid Modbus TCP endpoint %q: port must be in [1,65535]", endpoint)
	}
	return nil
}

func validateModbusEnumMap(raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("ENUM16 register requires non-empty enumMap")
	}
	cleaned := make(map[string]string, len(raw))
	for rawKey, rawVal := range raw {
		k := strings.TrimSpace(rawKey)
		parsed, err := strconv.ParseUint(k, 10, 16)
		if err != nil || strconv.FormatUint(parsed, 10) != k {
			return nil, fmt.Errorf("ENUM16 enumMap key %q must be a canonical decimal uint16 in [0,65535]", rawKey)
		}
		if _, dup := cleaned[k]; dup {
			return nil, fmt.Errorf("ENUM16 enumMap duplicate key %q", k)
		}
		v := strings.TrimSpace(rawVal)
		if v == "" || utf8.RuneCountInString(v) > 64 || containsModbusControlChar(v) {
			return nil, fmt.Errorf("ENUM16 enumMap value for key %q must be 1..64 characters", k)
		}
		cleaned[k] = v
	}
	return cleaned, nil
}

func containsModbusControlChar(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
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
