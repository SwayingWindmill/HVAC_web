package edgecontrol

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// SouthboundDriverKind identifies the concrete protocol family of a validated driver profile.
type SouthboundDriverKind string

const (
	SouthboundKindModbus   SouthboundDriverKind = "MODBUS"
	SouthboundKindBACnetIP SouthboundDriverKind = "BACNET_IP"
	SouthboundKindOPCUA    SouthboundDriverKind = "OPC_UA"
)

// SouthboundDriverProfile holds the discriminated validated configuration across Modbus,
// BACnet/IP, and OPC-UA southbound drivers.
type SouthboundDriverProfile struct {
	Kind   SouthboundDriverKind
	Modbus *DeclarativeModbusProfile
	BACnet *DeclarativeBACnetProfile
	OPCUA  *DeclarativeOPCUAProfile
}

// ValidateSouthboundDriverInput validates Modbus TCP/RTU, BACnet/IP, or OPC-UA driver inputs
// through a single canonical entrypoint shared across Edge runtime, Connectivity shadow, and Registry templates.
func ValidateSouthboundDriverInput(input ModbusDriverInput, opts ModbusDriverValidationOptions) (SouthboundDriverProfile, error) {
	switch strings.ToUpper(strings.TrimSpace(input.DriverType)) {
	case string(ModbusDriverTCP), string(ModbusDriverRTU):
		mod, err := ValidateModbusDriverInput(input, opts)
		if err != nil {
			return SouthboundDriverProfile{}, err
		}
		return SouthboundDriverProfile{Kind: SouthboundKindModbus, Modbus: &mod}, nil
	case DriverTypeBACnetIP:
		bac, err := ValidateBACnetDriverInput(input, opts)
		if err != nil {
			return SouthboundDriverProfile{}, err
		}
		return SouthboundDriverProfile{Kind: SouthboundKindBACnetIP, BACnet: &bac}, nil
	case DriverTypeOPCUA:
		ua, err := ValidateOPCUADriverInput(input, opts)
		if err != nil {
			return SouthboundDriverProfile{}, err
		}
		return SouthboundDriverProfile{Kind: SouthboundKindOPCUA, OPCUA: &ua}, nil
	default:
		return SouthboundDriverProfile{}, fmt.Errorf("unsupported driverType %q", input.DriverType)
	}
}

// CloneSouthboundDriverInput deep-copies a wire driver input across all protocol blocks.
func CloneSouthboundDriverInput(in ModbusDriverInput) ModbusDriverInput {
	cp := in
	if in.Endpoint != nil {
		v := *in.Endpoint
		cp.Endpoint = &v
	}
	if in.UnitID != nil {
		v := *in.UnitID
		cp.UnitID = &v
	}
	if in.TimeoutMS != nil {
		v := *in.TimeoutMS
		cp.TimeoutMS = &v
	}
	if in.Retries != nil {
		v := *in.Retries
		cp.Retries = &v
	}
	cp.Registers = CloneModbusRegisters(in.Registers)
	cp.BACnet = in.BACnet.Clone()
	cp.OPCUA = in.OPCUA.Clone()
	return cp
}

// ToDriverInput converts a validated SouthboundDriverProfile back into normalized wire JSON shape.
func (p SouthboundDriverProfile) ToDriverInput(includeGatewayBindings bool) ModbusDriverInput {
	switch p.Kind {
	case SouthboundKindModbus:
		if p.Modbus != nil {
			return p.Modbus.ToDriverInput(includeGatewayBindings)
		}
	case SouthboundKindBACnetIP:
		if p.BACnet != nil {
			return p.BACnet.ToDriverInput(includeGatewayBindings)
		}
	case SouthboundKindOPCUA:
		if p.OPCUA != nil {
			return p.OPCUA.ToDriverInput(includeGatewayBindings)
		}
	}
	return ModbusDriverInput{}
}

type validatedDriverHeader struct {
	componentID       string
	endpoint          string
	capabilityProfile CapabilityProfileID
	profileReqs       map[SemanticChannel]ChannelRequirement
	timeoutMS         int
	timeout           time.Duration
	retries           int
}

func validateDriverHeader(input ModbusDriverInput, opts ModbusDriverValidationOptions) (validatedDriverHeader, error) {
	var out validatedDriverHeader
	if opts.RequireDeviceKey {
		deviceKey := strings.TrimSpace(input.DeviceKey)
		if deviceKey == "" || utf8.RuneCountInString(deviceKey) > 128 || strings.Contains(deviceKey, "/") || containsModbusControlChar(deviceKey) {
			return out, errors.New("deviceKey must be 1..128 characters without '/'")
		}
		out.componentID = deviceKey
	}

	if input.Endpoint == nil {
		if opts.RequireGatewayBindings {
			return out, errors.New("endpoint is required")
		}
	} else {
		endpoint := strings.TrimSpace(*input.Endpoint)
		if endpoint == "" || utf8.RuneCountInString(endpoint) > 256 || containsModbusControlChar(endpoint) {
			return out, errors.New("endpoint must be 1..256 characters")
		}
		out.endpoint = endpoint
	}

	if input.TimeoutMS == nil {
		if opts.RequireGatewayBindings {
			return out, errors.New("timeoutMs is required")
		}
		out.timeoutMS = 1000
		out.timeout = time.Second
	} else {
		if *input.TimeoutMS < 100 || *input.TimeoutMS > 30000 {
			return out, fmt.Errorf("timeoutMs %d must be in [100,30000]", *input.TimeoutMS)
		}
		out.timeoutMS = *input.TimeoutMS
		out.timeout = time.Duration(*input.TimeoutMS) * time.Millisecond
	}

	if input.Retries == nil {
		if opts.RequireGatewayBindings {
			return out, errors.New("retries is required")
		}
	} else {
		if *input.Retries < 0 || *input.Retries > 10 {
			return out, fmt.Errorf("retries %d must be in [0,10]", *input.Retries)
		}
		out.retries = *input.Retries
	}

	if input.CapabilityProfile != "" {
		capID := CapabilityProfileID(strings.TrimSpace(input.CapabilityProfile))
		if capID == "" {
			return out, errors.New("capabilityProfile must not be blank")
		}
		std, found := findStandardProfile(capID)
		if !found {
			return out, fmt.Errorf("unknown capabilityProfile %q", input.CapabilityProfile)
		}
		out.capabilityProfile = capID
		out.profileReqs = make(map[SemanticChannel]ChannelRequirement, len(std.Channels))
		for _, req := range std.Channels {
			out.profileReqs[req.Semantic] = req
		}
	}
	return out, nil
}

func validateHostPortEndpoint(endpoint, protocolLabel string) error {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("invalid %s endpoint %q: %w", protocolLabel, endpoint, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("invalid %s endpoint %q: host is required", protocolLabel, endpoint)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid %s endpoint %q: port must be in [1,65535]", protocolLabel, endpoint)
	}
	return nil
}

func validatePointCommonMeta(
	pointCode, semanticRaw, safetyBoundaryRaw, pollPriorityRaw string,
	writable bool,
	unitRaw string,
) (SemanticChannel, SafetyBoundaryClass, DataPriority, string, error) {
	trimmedCode := strings.TrimSpace(pointCode)
	if !pointCodePattern.MatchString(trimmedCode) {
		return "", "", "", "", fmt.Errorf("invalid pointCode %q", pointCode)
	}

	var semantic SemanticChannel
	if semanticRaw != "" {
		trimmed := strings.TrimSpace(semanticRaw)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 64 || strings.Contains(trimmed, "/") || containsModbusControlChar(trimmed) {
			return "", "", "", "", errors.New("semanticChannel must be 1..64 characters without '/'")
		}
		semantic = SemanticChannel(trimmed)
	}

	safetyBoundary := SafetyBoundaryClass(strings.ToUpper(strings.TrimSpace(safetyBoundaryRaw)))
	if !ValidSafetyBoundaryClass(safetyBoundary) {
		return "", "", "", "", fmt.Errorf("unsupported safetyBoundary %q", safetyBoundaryRaw)
	}
	if safetyBoundary != SafetyBoundaryHardRealtime {
		if _, protected := ClassifyHardRealtimeProtection(trimmedCode, string(semantic), nil); protected {
			safetyBoundary = SafetyBoundaryHardRealtime
		}
	}
	if safetyBoundary == SafetyBoundaryHardRealtime && writable {
		return "", "", "", "", fmt.Errorf("%w: point %s cannot be configured writable", ErrHardRealtimeSafetyBoundary, trimmedCode)
	}

	priority := DataPriority(strings.ToUpper(strings.TrimSpace(pollPriorityRaw)))
	if !validPriority(priority) {
		return "", "", "", "", fmt.Errorf("unsupported pollPriority %q", pollPriorityRaw)
	}

	unit := strings.TrimSpace(unitRaw)
	if utf8.RuneCountInString(unit) > 32 || containsModbusControlChar(unit) {
		return "", "", "", "", errors.New("unit must be <= 32 printable characters")
	}

	return semantic, safetyBoundary, priority, unit, nil
}

func validateCapabilityChannelAlignment(
	capID CapabilityProfileID,
	profileReqs map[SemanticChannel]ChannelRequirement,
	seenSemantics map[SemanticChannel]struct{},
	semantic SemanticChannel,
	dataType DataType,
	access AccessMode,
) error {
	if semantic == "" {
		return nil
	}
	if _, dup := seenSemantics[semantic]; dup {
		return fmt.Errorf("duplicate semanticChannel %q", semantic)
	}
	seenSemantics[semantic] = struct{}{}
	if profileReqs == nil {
		return nil
	}
	req, declared := profileReqs[semantic]
	if !declared {
		return fmt.Errorf("semanticChannel %q is not declared by capabilityProfile %s", semantic, capID)
	}
	if dataType != req.DataType {
		return fmt.Errorf("semanticChannel %q expects %s, got %s", semantic, req.DataType, dataType)
	}
	if !accessSatisfies(access, req.Access, true) {
		return fmt.Errorf("semanticChannel %q expects access %s, got %s", semantic, req.Access, access)
	}
	return nil
}

func parseDriverChannelDataType(raw string, fallback DataType) (DataType, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" {
		if fallback != "" {
			return fallback, nil
		}
		return DataTypeDouble, nil
	}
	switch DataType(trimmed) {
	case DataTypeBoolean, DataTypeInteger, DataTypeDouble, DataTypeString:
		return DataType(trimmed), nil
	case DataTypeFloat:
		return DataTypeDouble, nil
	case DataTypeLong:
		return DataTypeInteger, nil
	default:
		return "", fmt.Errorf("unsupported dataType %q", raw)
	}
}
