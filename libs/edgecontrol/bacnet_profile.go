package edgecontrol

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	DriverTypeBACnetIP         = "BACNET_IP"
	DeclarativeBACnetFactoryID = "declarative-bacnet-ip"
	DefaultBACnetPort          = 47808

	BACnetPropPresentValue uint32 = 85
	BACnetPropStatusFlags  uint32 = 111
	BACnetPropOutOfService uint32 = 81

	maxBACnetObjectInstance uint32 = 4194302
	defaultBACnetPriority   uint8  = 8
)

// BACnetObjectType identifies supported ASHRAE 135 standard object types.
type BACnetObjectType string

const (
	BACnetObjAnalogInput      BACnetObjectType = "ANALOG_INPUT"
	BACnetObjAnalogOutput     BACnetObjectType = "ANALOG_OUTPUT"
	BACnetObjAnalogValue      BACnetObjectType = "ANALOG_VALUE"
	BACnetObjBinaryInput      BACnetObjectType = "BINARY_INPUT"
	BACnetObjBinaryOutput     BACnetObjectType = "BINARY_OUTPUT"
	BACnetObjBinaryValue      BACnetObjectType = "BINARY_VALUE"
	BACnetObjDevice           BACnetObjectType = "DEVICE"
	BACnetObjMultiStateInput  BACnetObjectType = "MULTI_STATE_INPUT"
	BACnetObjMultiStateOutput BACnetObjectType = "MULTI_STATE_OUTPUT"
	BACnetObjMultiStateValue  BACnetObjectType = "MULTI_STATE_VALUE"
)

// ParseBACnetObjectType parses a canonical ASHRAE 135 object type token.
func ParseBACnetObjectType(raw string) (BACnetObjectType, error) {
	normalized := BACnetObjectType(strings.ToUpper(strings.TrimSpace(raw)))
	switch normalized {
	case BACnetObjAnalogInput,
		BACnetObjAnalogOutput,
		BACnetObjAnalogValue,
		BACnetObjBinaryInput,
		BACnetObjBinaryOutput,
		BACnetObjBinaryValue,
		BACnetObjDevice,
		BACnetObjMultiStateInput,
		BACnetObjMultiStateOutput,
		BACnetObjMultiStateValue:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported BACnet objectType %q", raw)
	}
}

// BACnetObjectTypeFromCode maps an ASHRAE 135 wire 10-bit object type number to BACnetObjectType.
func BACnetObjectTypeFromCode(code uint16) (BACnetObjectType, bool) {
	switch code {
	case 0:
		return BACnetObjAnalogInput, true
	case 1:
		return BACnetObjAnalogOutput, true
	case 2:
		return BACnetObjAnalogValue, true
	case 3:
		return BACnetObjBinaryInput, true
	case 4:
		return BACnetObjBinaryOutput, true
	case 5:
		return BACnetObjBinaryValue, true
	case 8:
		return BACnetObjDevice, true
	case 13:
		return BACnetObjMultiStateInput, true
	case 14:
		return BACnetObjMultiStateOutput, true
	case 19:
		return BACnetObjMultiStateValue, true
	default:
		return "", false
	}
}

// Code returns the 10-bit ASHRAE 135 object type number.
func (t BACnetObjectType) Code() uint16 {
	switch t {
	case BACnetObjAnalogInput:
		return 0
	case BACnetObjAnalogOutput:
		return 1
	case BACnetObjAnalogValue:
		return 2
	case BACnetObjBinaryInput:
		return 3
	case BACnetObjBinaryOutput:
		return 4
	case BACnetObjBinaryValue:
		return 5
	case BACnetObjDevice:
		return 8
	case BACnetObjMultiStateInput:
		return 13
	case BACnetObjMultiStateOutput:
		return 14
	case BACnetObjMultiStateValue:
		return 19
	default:
		return 0xFFFF
	}
}

// IsInputOnly reports whether the BACnet object type represents a read-only physical input.
func (t BACnetObjectType) IsInputOnly() bool {
	return t == BACnetObjAnalogInput || t == BACnetObjBinaryInput || t == BACnetObjMultiStateInput || t == BACnetObjDevice
}

func (t BACnetObjectType) defaultDataType() DataType {
	switch t {
	case BACnetObjBinaryInput, BACnetObjBinaryOutput, BACnetObjBinaryValue:
		return DataTypeBoolean
	case BACnetObjMultiStateInput, BACnetObjMultiStateOutput, BACnetObjMultiStateValue:
		return DataTypeInteger
	default:
		return DataTypeDouble
	}
}

// BACnetObjectID uniquely identifies an object instance within a BACnet device.
type BACnetObjectID struct {
	Type     BACnetObjectType `json:"type"`
	Instance uint32           `json:"instance"`
}

// String returns canonical OBJECT_TYPE:INSTANCE formatting.
func (id BACnetObjectID) String() string {
	return fmt.Sprintf("%s:%d", id.Type, id.Instance)
}

// BACnetObjectInput describes a wire/shadow BACnet object-property-to-Channel mapping.
type BACnetObjectInput struct {
	PointCode       string            `json:"pointCode"`
	SemanticChannel string            `json:"semanticChannel,omitempty"`
	SafetyBoundary  string            `json:"safetyBoundary,omitempty"`
	ObjectType      string            `json:"objectType"`
	ObjectInstance  int               `json:"objectInstance"`
	PropertyID      *int              `json:"propertyId,omitempty"`
	DataType        string            `json:"dataType,omitempty"`
	Writable        bool              `json:"writable,omitempty"`
	COVSubscribe    bool              `json:"covSubscribe,omitempty"`
	Scale           float64           `json:"scale,omitempty"`
	Offset          float64           `json:"offset,omitempty"`
	PollPriority    string            `json:"pollPriority"`
	Unit            string            `json:"unit,omitempty"`
	EnumMap         map[string]string `json:"enumMap,omitempty"`
}

// BACnetDriverSpecInput holds BACnet/IP protocol parameters and object mappings inside ModbusDriverInput.BACnet.
type BACnetDriverSpecInput struct {
	DeviceInstance     *int                `json:"deviceInstance,omitempty"`
	NetworkNumber      *int                `json:"networkNumber,omitempty"`
	COVLifetimeSeconds *int                `json:"covLifetimeSeconds,omitempty"`
	WritePriority      *int                `json:"writePriority,omitempty"`
	Objects            []BACnetObjectInput `json:"objects"`
}

// Clone returns a deep copy of BACnetDriverSpecInput (nil-safe).
func (b *BACnetDriverSpecInput) Clone() *BACnetDriverSpecInput {
	if b == nil {
		return nil
	}
	cp := *b
	if b.DeviceInstance != nil {
		v := *b.DeviceInstance
		cp.DeviceInstance = &v
	}
	if b.NetworkNumber != nil {
		v := *b.NetworkNumber
		cp.NetworkNumber = &v
	}
	if b.COVLifetimeSeconds != nil {
		v := *b.COVLifetimeSeconds
		cp.COVLifetimeSeconds = &v
	}
	if b.WritePriority != nil {
		v := *b.WritePriority
		cp.WritePriority = &v
	}
	cp.Objects = make([]BACnetObjectInput, len(b.Objects))
	for i, obj := range b.Objects {
		cloned := obj
		if obj.PropertyID != nil {
			p := *obj.PropertyID
			cloned.PropertyID = &p
		}
		if obj.EnumMap != nil {
			cloned.EnumMap = maps.Clone(obj.EnumMap)
		}
		cp.Objects[i] = cloned
	}
	return &cp
}

// BACnetObjectSpec is the validated runtime representation of one BACnet object mapping.
type BACnetObjectSpec struct {
	PointCode       string
	SemanticChannel SemanticChannel
	SafetyBoundary  SafetyBoundaryClass
	ObjectType      BACnetObjectType
	ObjectInstance  uint32
	PropertyID      uint32
	DataType        DataType
	Writable        bool
	COVSubscribe    bool
	Scale           float64
	Offset          float64
	PollPriority    DataPriority
	Unit            string
	EnumMap         map[string]string
}

// ObjectID returns the canonical BACnetObjectID for this spec.
func (s BACnetObjectSpec) ObjectID() BACnetObjectID {
	return BACnetObjectID{Type: s.ObjectType, Instance: s.ObjectInstance}
}

// EffectiveSafetyBoundary reports the EDGE-003 boundary classification for this object.
func (s BACnetObjectSpec) EffectiveSafetyBoundary() SafetyBoundaryClass {
	if s.SafetyBoundary == SafetyBoundaryHardRealtime {
		return SafetyBoundaryHardRealtime
	}
	if _, protected := ClassifyHardRealtimeProtection(s.PointCode, string(s.SemanticChannel), nil); protected {
		return SafetyBoundaryHardRealtime
	}
	if s.SafetyBoundary == SafetyBoundarySoftRealtime {
		return SafetyBoundarySoftRealtime
	}
	return ""
}

// DeclarativeBACnetProfile is the validated runtime profile for DeclarativeBACnetAdapter.
type DeclarativeBACnetProfile struct {
	ComponentID        string
	Alias              string
	Endpoint           string
	HasDeviceInstance  bool
	DeviceInstance     uint32
	NetworkNumber      uint16
	COVLifetimeSeconds uint32
	WritePriority      uint8
	CapabilityProfile  CapabilityProfileID
	Timeout            time.Duration
	TimeoutMS          int
	Retries            int
	Objects            []BACnetObjectSpec
}

// ValidateBACnetDriverInput validates a BACnet/IP driver input specification across Gateway shadow,
// Registry DEVICE templates, and runtime adapter initialization.
func ValidateBACnetDriverInput(input ModbusDriverInput, opts ModbusDriverValidationOptions) (DeclarativeBACnetProfile, error) {
	if !strings.EqualFold(strings.TrimSpace(input.DriverType), DriverTypeBACnetIP) {
		return DeclarativeBACnetProfile{}, fmt.Errorf("expected driverType %s, got %q", DriverTypeBACnetIP, input.DriverType)
	}
	if input.UnitID != nil || len(input.Registers) > 0 || input.OPCUA != nil {
		return DeclarativeBACnetProfile{}, errors.New("BACNET_IP driver cannot specify unitId, registers, or opcua configuration")
	}
	if input.BACnet == nil {
		return DeclarativeBACnetProfile{}, errors.New("BACNET_IP driver requires bacnet configuration")
	}

	hdr, err := validateDriverHeader(input, opts)
	if err != nil {
		return DeclarativeBACnetProfile{}, err
	}
	if hdr.endpoint != "" {
		if err := validateHostPortEndpoint(hdr.endpoint, "BACnet/IP"); err != nil {
			return DeclarativeBACnetProfile{}, err
		}
	}

	spec := input.BACnet
	var devInst uint32
	hasDevInst := false
	if spec.DeviceInstance == nil {
		if opts.RequireGatewayBindings {
			return DeclarativeBACnetProfile{}, errors.New("bacnet.deviceInstance is required")
		}
	} else {
		if *spec.DeviceInstance < 0 || *spec.DeviceInstance > int(maxBACnetObjectInstance) {
			return DeclarativeBACnetProfile{}, fmt.Errorf("bacnet.deviceInstance %d must be in [0,%d]", *spec.DeviceInstance, maxBACnetObjectInstance)
		}
		devInst = uint32(*spec.DeviceInstance)
		hasDevInst = true
	}

	var netNum uint16
	if spec.NetworkNumber != nil {
		if *spec.NetworkNumber < 0 || *spec.NetworkNumber > 65534 {
			return DeclarativeBACnetProfile{}, fmt.Errorf("bacnet.networkNumber %d must be in [0,65534]", *spec.NetworkNumber)
		}
		netNum = uint16(*spec.NetworkNumber)
	}

	covLifetime := uint32(0)
	if spec.COVLifetimeSeconds != nil {
		if *spec.COVLifetimeSeconds < 0 || *spec.COVLifetimeSeconds > 86400 {
			return DeclarativeBACnetProfile{}, fmt.Errorf("bacnet.covLifetimeSeconds %d must be in [0,86400]", *spec.COVLifetimeSeconds)
		}
		covLifetime = uint32(*spec.COVLifetimeSeconds)
	}

	writePrio := defaultBACnetPriority
	if spec.WritePriority != nil {
		if *spec.WritePriority < 1 || *spec.WritePriority > 16 {
			return DeclarativeBACnetProfile{}, fmt.Errorf("bacnet.writePriority %d must be in [1,16]", *spec.WritePriority)
		}
		writePrio = uint8(*spec.WritePriority)
	}

	if len(spec.Objects) < 1 || len(spec.Objects) > 256 {
		return DeclarativeBACnetProfile{}, fmt.Errorf("bacnet.objects count %d must be in [1,256]", len(spec.Objects))
	}

	seenPoints := make(map[string]struct{}, len(spec.Objects))
	seenSemantics := make(map[SemanticChannel]struct{}, len(spec.Objects))
	type objPropKey struct {
		objType  BACnetObjectType
		instance uint32
		propID   uint32
	}
	seenObjs := make(map[objPropKey]struct{}, len(spec.Objects))
	validatedObjs := make([]BACnetObjectSpec, len(spec.Objects))

	for idx, obj := range spec.Objects {
		parsed, err := validateBACnetObjectInput(obj)
		if err != nil {
			return DeclarativeBACnetProfile{}, fmt.Errorf("bacnet.objects[%d] (%s): %w", idx, obj.PointCode, err)
		}
		if _, dup := seenPoints[parsed.PointCode]; dup {
			return DeclarativeBACnetProfile{}, fmt.Errorf("duplicate BACnet object pointCode %q", parsed.PointCode)
		}
		seenPoints[parsed.PointCode] = struct{}{}

		key := objPropKey{objType: parsed.ObjectType, instance: parsed.ObjectInstance, propID: parsed.PropertyID}
		if _, dup := seenObjs[key]; dup {
			return DeclarativeBACnetProfile{}, fmt.Errorf("duplicate BACnet object mapping %s:%d prop=%d", parsed.ObjectType, parsed.ObjectInstance, parsed.PropertyID)
		}
		seenObjs[key] = struct{}{}

		access := AccessReadOnly
		if parsed.Writable {
			access = AccessReadWrite
		}
		if err := validateCapabilityChannelAlignment(hdr.capabilityProfile, hdr.profileReqs, seenSemantics, parsed.SemanticChannel, parsed.DataType, access); err != nil {
			return DeclarativeBACnetProfile{}, err
		}
		validatedObjs[idx] = parsed
	}

	return DeclarativeBACnetProfile{
		ComponentID:        hdr.componentID,
		Endpoint:           hdr.endpoint,
		HasDeviceInstance:  hasDevInst,
		DeviceInstance:     devInst,
		NetworkNumber:      netNum,
		COVLifetimeSeconds: covLifetime,
		WritePriority:      writePrio,
		CapabilityProfile:  hdr.capabilityProfile,
		Timeout:            hdr.timeout,
		TimeoutMS:          hdr.timeoutMS,
		Retries:            hdr.retries,
		Objects:            validatedObjs,
	}, nil
}

func validateBACnetObjectInput(in BACnetObjectInput) (BACnetObjectSpec, error) {
	semantic, boundary, priority, unit, err := validatePointCommonMeta(in.PointCode, in.SemanticChannel, in.SafetyBoundary, in.PollPriority, in.Writable, in.Unit)
	if err != nil {
		return BACnetObjectSpec{}, err
	}
	objType, err := ParseBACnetObjectType(in.ObjectType)
	if err != nil {
		return BACnetObjectSpec{}, err
	}
	if in.ObjectInstance < 0 || in.ObjectInstance > int(maxBACnetObjectInstance) {
		return BACnetObjectSpec{}, fmt.Errorf("objectInstance %d must be in [0,%d]", in.ObjectInstance, maxBACnetObjectInstance)
	}
	if in.Writable && objType.IsInputOnly() {
		return BACnetObjectSpec{}, fmt.Errorf("writable is not allowed for read-only BACnet objectType %s", objType)
	}

	propID := BACnetPropPresentValue
	if in.PropertyID != nil {
		if *in.PropertyID < 0 || *in.PropertyID > 4194303 {
			return BACnetObjectSpec{}, fmt.Errorf("propertyId %d out of range [0,4194303]", *in.PropertyID)
		}
		propID = uint32(*in.PropertyID)
		if in.Writable && propID != BACnetPropPresentValue {
			return BACnetObjectSpec{}, fmt.Errorf("writable BACnet points must target Present_Value (%d), got propertyId=%d", BACnetPropPresentValue, propID)
		}
	}

	defaultDT := objType.defaultDataType()
	if len(in.EnumMap) > 0 && strings.TrimSpace(in.DataType) == "" {
		defaultDT = DataTypeString
	}
	dt, err := parseDriverChannelDataType(in.DataType, defaultDT)
	if err != nil {
		return BACnetObjectSpec{}, err
	}

	scale := in.Scale
	if math.IsNaN(scale) || math.IsInf(scale, 0) {
		return BACnetObjectSpec{}, errors.New("scale must be a finite number")
	}
	if scale == 0 {
		scale = 1
	}
	offset := in.Offset
	if math.IsNaN(offset) || math.IsInf(offset, 0) {
		return BACnetObjectSpec{}, errors.New("offset must be a finite number")
	}
	if offset == 0 {
		offset = 0
	}

	var enumMap map[string]string
	if len(in.EnumMap) > 0 {
		enumMap, err = validateModbusEnumMap(in.EnumMap)
		if err != nil {
			return BACnetObjectSpec{}, err
		}
	}

	return BACnetObjectSpec{
		PointCode:       strings.TrimSpace(in.PointCode),
		SemanticChannel: semantic,
		SafetyBoundary:  boundary,
		ObjectType:      objType,
		ObjectInstance:  uint32(in.ObjectInstance),
		PropertyID:      propID,
		DataType:        dt,
		Writable:        in.Writable,
		COVSubscribe:    in.COVSubscribe,
		Scale:           scale,
		Offset:          offset,
		PollPriority:    priority,
		Unit:            unit,
		EnumMap:         enumMap,
	}, nil
}

// Clone returns a deep copy of DeclarativeBACnetProfile.
func (p DeclarativeBACnetProfile) Clone() DeclarativeBACnetProfile {
	out := p
	out.Objects = make([]BACnetObjectSpec, len(p.Objects))
	for i, obj := range p.Objects {
		cloned := obj
		if obj.EnumMap != nil {
			cloned.EnumMap = maps.Clone(obj.EnumMap)
		}
		out.Objects[i] = cloned
	}
	return out
}

// ToDriverInput converts a DeclarativeBACnetProfile into normalized ModbusDriverInput wire shape.
func (p DeclarativeBACnetProfile) ToDriverInput(includeGatewayBindings bool) ModbusDriverInput {
	objs := make([]BACnetObjectInput, len(p.Objects))
	for i, spec := range p.Objects {
		prop := int(spec.PropertyID)
		objs[i] = BACnetObjectInput{
			PointCode:       spec.PointCode,
			SemanticChannel: string(spec.SemanticChannel),
			SafetyBoundary:  string(spec.SafetyBoundary),
			ObjectType:      string(spec.ObjectType),
			ObjectInstance:  int(spec.ObjectInstance),
			PropertyID:      &prop,
			DataType:        string(spec.DataType),
			Writable:        spec.Writable,
			COVSubscribe:    spec.COVSubscribe,
			Scale:           spec.Scale,
			Offset:          spec.Offset,
			PollPriority:    string(spec.PollPriority),
			Unit:            spec.Unit,
			EnumMap:         maps.Clone(spec.EnumMap),
		}
	}
	spec := &BACnetDriverSpecInput{
		Objects: objs,
	}
	if p.HasDeviceInstance {
		d := int(p.DeviceInstance)
		spec.DeviceInstance = &d
	}
	if p.NetworkNumber > 0 {
		n := int(p.NetworkNumber)
		spec.NetworkNumber = &n
	}
	if p.COVLifetimeSeconds > 0 {
		c := int(p.COVLifetimeSeconds)
		spec.COVLifetimeSeconds = &c
	}
	if p.WritePriority > 0 {
		wp := int(p.WritePriority)
		spec.WritePriority = &wp
	}
	out := ModbusDriverInput{
		DeviceKey:         p.ComponentID,
		DriverType:        DriverTypeBACnetIP,
		CapabilityProfile: string(p.CapabilityProfile),
		BACnet:            spec,
	}
	if includeGatewayBindings {
		if ep := strings.TrimSpace(p.Endpoint); ep != "" {
			out.Endpoint = &ep
		}
	}
	if p.TimeoutMS > 0 {
		t := p.TimeoutMS
		out.TimeoutMS = &t
	}
	r := p.Retries
	out.Retries = &r
	return out
}

func (p DeclarativeBACnetProfile) validateWithTransport(hasExternalTransport bool) error {
	if !p.HasDeviceInstance {
		return errors.New("bacnet.deviceInstance is required")
	}
	if !hasExternalTransport && strings.TrimSpace(p.Endpoint) == "" {
		return errors.New("bacnet endpoint is required")
	}
	in := p.ToDriverInput(true)
	if !hasExternalTransport || strings.TrimSpace(p.Endpoint) != "" {
		if _, err := ValidateBACnetDriverInput(in, ModbusDriverValidationOptions{
			RequireDeviceKey:       true,
			RequireGatewayBindings: !hasExternalTransport,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ValidateControlWriteAllowed enforces EDGE-003 before issuing physical BACnet writes.
func ValidateBACnetWriteAllowed(spec BACnetObjectSpec) error {
	if spec.EffectiveSafetyBoundary() == SafetyBoundaryHardRealtime {
		return fmt.Errorf("%w: point=%s", ErrHardRealtimeSafetyBoundary, spec.PointCode)
	}
	if !spec.Writable || spec.ObjectType.IsInputOnly() {
		return fmt.Errorf("BACnet point %s (%s) is read-only", spec.PointCode, spec.ObjectID())
	}
	return nil
}

func (p DeclarativeBACnetProfile) CoalescedPrioritySlices() []BACnetObjectSpec {
	return slices.Clone(p.Objects)
}
