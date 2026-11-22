package connectivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	DriftSectionDriverConfigs  = "DRIVER_CONFIGS"
	DriftSectionAutonomyPolicy = "AUTONOMY_POLICY"

	maxDriverConfigsJSONBytes  = 262144
	maxAutonomyPolicyJSONBytes = 65536
)

type GatewayModbusRegisterMapping struct {
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

type GatewayModbusDriverConfig struct {
	DeviceKey         string                             `json:"deviceKey"`
	DriverType        string                             `json:"driverType"`
	Endpoint          string                             `json:"endpoint"`
	UnitID            int                                `json:"unitId,omitempty"`
	CapabilityProfile string                             `json:"capabilityProfile,omitempty"`
	TimeoutMS         int                                `json:"timeoutMs"`
	Retries           int                                `json:"retries"`
	Registers         []GatewayModbusRegisterMapping     `json:"registers,omitempty"`
	BACnet            *edgecontrol.BACnetDriverSpecInput `json:"bacnet,omitempty"`
	OPCUA             *edgecontrol.OPCUADriverSpecInput  `json:"opcua,omitempty"`
}

type GatewayAutonomyPolicy struct {
	Enabled                            bool    `json:"enabled"`
	OfflineGraceSeconds                int     `json:"offlineGraceSeconds"`
	DefaultChilledWaterSupplySetpointC float64 `json:"defaultChilledWaterSupplySetpointC"`
	DefaultPumpFrequencyHz             float64 `json:"defaultPumpFrequencyHz"`
	MinPumpFrequencyHz                 float64 `json:"minPumpFrequencyHz"`
	MaxPumpFrequencyHz                 float64 `json:"maxPumpFrequencyHz"`
	MinRunSeconds                      int     `json:"minRunSeconds"`
	MinStopSeconds                     int     `json:"minStopSeconds"`
	SnapshotIntervalSeconds            int     `json:"snapshotIntervalSeconds"`
}

// OptionalAutonomyPolicy represents tri-state JSON presence for PUT partial
// updates: omitted (Set=false, Value=nil), explicit JSON null (Set=true,
// Value=nil) to clear, or a replacement policy object (Value!=nil).
type OptionalAutonomyPolicy struct {
	Set   bool
	Value *GatewayAutonomyPolicy
}

func (o OptionalAutonomyPolicy) IsSet() bool {
	return o.Set || o.Value != nil
}

func (o OptionalAutonomyPolicy) IsZero() bool {
	return !o.Set && o.Value == nil
}

func (o OptionalAutonomyPolicy) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o *OptionalAutonomyPolicy) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("OptionalAutonomyPolicy: UnmarshalJSON on nil pointer")
	}
	trimmed := bytes.TrimSpace(data)
	o.Set = true
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var policy GatewayAutonomyPolicy
	if err := dec.Decode(&policy); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content in autonomyPolicy")
	}
	o.Value = &policy
	return nil
}

func normalizeZeroFloat(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

func validateDriverConfigs(input []GatewayModbusDriverConfig) ([]GatewayModbusDriverConfig, error) {
	seenDevices := make(map[string]struct{}, len(input))
	out := make([]GatewayModbusDriverConfig, 0, len(input))
	for i, driver := range input {
		endpoint := driver.Endpoint
		timeoutMS := driver.TimeoutMS
		retries := driver.Retries
		driverType := strings.ToUpper(strings.TrimSpace(driver.DriverType))

		var unitIDPtr *int
		isModbus := driverType == string(edgecontrol.ModbusDriverTCP) || driverType == string(edgecontrol.ModbusDriverRTU)
		if isModbus || driver.UnitID != 0 {
			u := driver.UnitID
			unitIDPtr = &u
		}

		var regInputs []edgecontrol.ModbusRegisterInput
		if len(driver.Registers) > 0 {
			regInputs = make([]edgecontrol.ModbusRegisterInput, len(driver.Registers))
			for j, reg := range driver.Registers {
				regInputs[j] = edgecontrol.ModbusRegisterInput{
					PointCode:         reg.PointCode,
					SemanticChannel:   reg.SemanticChannel,
					FunctionCode:      reg.FunctionCode,
					WriteFunctionCode: cloneOptionalInt(reg.WriteFunctionCode),
					Address:           reg.Address,
					RegisterCount:     reg.RegisterCount,
					RawType:           reg.RawType,
					ByteOrder:         reg.ByteOrder,
					WordOrder:         reg.WordOrder,
					BitIndex:          cloneOptionalInt(reg.BitIndex),
					Scale:             reg.Scale,
					Offset:            reg.Offset,
					PollPriority:      reg.PollPriority,
					EnumMap:           reg.EnumMap,
				}
			}
		}

		southbound, err := edgecontrol.ValidateSouthboundDriverInput(edgecontrol.ModbusDriverInput{
			DeviceKey:         driver.DeviceKey,
			DriverType:        driver.DriverType,
			Endpoint:          &endpoint,
			UnitID:            unitIDPtr,
			CapabilityProfile: driver.CapabilityProfile,
			TimeoutMS:         &timeoutMS,
			Retries:           &retries,
			Registers:         regInputs,
			BACnet:            driver.BACnet.Clone(),
			OPCUA:             driver.OPCUA.Clone(),
		}, edgecontrol.ModbusDriverValidationOptions{
			RequireDeviceKey:       true,
			RequireGatewayBindings: true,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: driverConfigs[%d]: %v", ErrConfigShadowInvalid, i, err)
		}
		cleaned := driverConfigFromSouthboundProfile(southbound)
		if _, dup := seenDevices[cleaned.DeviceKey]; dup {
			return nil, fmt.Errorf("%w: duplicate driverConfigs deviceKey %q", ErrConfigShadowInvalid, cleaned.DeviceKey)
		}
		seenDevices[cleaned.DeviceKey] = struct{}{}

		slices.SortFunc(cleaned.Registers, compareRegisterMappings)
		if cleaned.BACnet != nil {
			slices.SortFunc(cleaned.BACnet.Objects, func(a, b edgecontrol.BACnetObjectInput) int {
				return strings.Compare(a.PointCode, b.PointCode)
			})
		}
		if cleaned.OPCUA != nil {
			slices.SortFunc(cleaned.OPCUA.Nodes, func(a, b edgecontrol.OPCUANodeInput) int {
				return strings.Compare(a.PointCode, b.PointCode)
			})
		}
		out = append(out, cleaned)
	}
	slices.SortFunc(out, func(a, b GatewayModbusDriverConfig) int {
		return strings.Compare(a.DeviceKey, b.DeviceKey)
	})
	return out, nil
}

func driverConfigFromSouthboundProfile(profile edgecontrol.SouthboundDriverProfile) GatewayModbusDriverConfig {
	if profile.Kind == edgecontrol.SouthboundKindModbus && profile.Modbus != nil {
		return driverConfigFromEdgeProfile(*profile.Modbus)
	}
	wire := profile.ToDriverInput(true)
	ep := ""
	if wire.Endpoint != nil {
		ep = *wire.Endpoint
	}
	tmo := 1000
	if wire.TimeoutMS != nil {
		tmo = *wire.TimeoutMS
	}
	ret := 0
	if wire.Retries != nil {
		ret = *wire.Retries
	}
	return GatewayModbusDriverConfig{
		DeviceKey:         wire.DeviceKey,
		DriverType:        wire.DriverType,
		Endpoint:          ep,
		CapabilityProfile: wire.CapabilityProfile,
		TimeoutMS:         tmo,
		Retries:           ret,
		BACnet:            wire.BACnet.Clone(),
		OPCUA:             wire.OPCUA.Clone(),
	}
}

func driverConfigFromEdgeProfile(profile edgecontrol.DeclarativeModbusProfile) GatewayModbusDriverConfig {
	regs := make([]GatewayModbusRegisterMapping, len(profile.Registers))
	for i, spec := range profile.Registers {
		var writeFC *int
		if spec.WriteFunctionCode != 0 {
			w := int(spec.WriteFunctionCode)
			writeFC = &w
		}
		var bitIndex *int
		if spec.RawType == edgecontrol.ModbusRawBitBool {
			b := spec.BitIndex
			bitIndex = &b
		}
		var enumMap map[string]string
		if len(spec.EnumMap) > 0 {
			enumMap = maps.Clone(spec.EnumMap)
		}
		regs[i] = GatewayModbusRegisterMapping{
			PointCode:         spec.PointCode,
			SemanticChannel:   string(spec.SemanticChannel),
			FunctionCode:      int(spec.FunctionCode),
			WriteFunctionCode: writeFC,
			Address:           int(spec.Address),
			RegisterCount:     int(spec.RegisterCount),
			RawType:           string(spec.RawType),
			ByteOrder:         string(spec.ByteOrder),
			WordOrder:         string(spec.WordOrder),
			BitIndex:          bitIndex,
			Scale:             spec.Scale,
			Offset:            normalizeZeroFloat(spec.Offset),
			PollPriority:      string(spec.PollPriority),
			EnumMap:           enumMap,
		}
	}
	return GatewayModbusDriverConfig{
		DeviceKey:         profile.ComponentID,
		DriverType:        string(profile.DriverType),
		Endpoint:          profile.Endpoint,
		UnitID:            int(profile.UnitID),
		CapabilityProfile: string(profile.CapabilityProfile),
		TimeoutMS:         profile.TimeoutMS,
		Retries:           profile.Retries,
		Registers:         regs,
	}
}

func validateAutonomyPolicy(input *GatewayAutonomyPolicy) (*GatewayAutonomyPolicy, error) {
	if input == nil {
		return nil, nil
	}
	if err := edgecontrol.AutonomyPolicyConfig(*input).Validate(); err != nil {
		return nil, fmt.Errorf("%w: autonomyPolicy: %v", ErrConfigShadowInvalid, err)
	}
	cp := *input
	return &cp, nil
}

func cloneOptionalInt(src *int) *int {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

func cloneRegisterMapping(src GatewayModbusRegisterMapping) GatewayModbusRegisterMapping {
	copied := src
	copied.WriteFunctionCode = cloneOptionalInt(src.WriteFunctionCode)
	copied.BitIndex = cloneOptionalInt(src.BitIndex)
	copied.Offset = normalizeZeroFloat(src.Offset)
	if len(src.EnumMap) > 0 {
		copied.EnumMap = maps.Clone(src.EnumMap)
	} else {
		copied.EnumMap = nil
	}
	return copied
}

func cloneDriverConfigsList(src []GatewayModbusDriverConfig) []GatewayModbusDriverConfig {
	if len(src) == 0 {
		return []GatewayModbusDriverConfig{}
	}
	out := make([]GatewayModbusDriverConfig, len(src))
	for i, driver := range src {
		out[i] = driver
		if len(driver.Registers) > 0 {
			regs := make([]GatewayModbusRegisterMapping, len(driver.Registers))
			for j, reg := range driver.Registers {
				regs[j] = cloneRegisterMapping(reg)
			}
			out[i].Registers = regs
		} else {
			out[i].Registers = nil
		}
		out[i].BACnet = driver.BACnet.Clone()
		out[i].OPCUA = driver.OPCUA.Clone()
	}
	return out
}

func cloneAutonomyPolicy(src *GatewayAutonomyPolicy) *GatewayAutonomyPolicy {
	if src == nil {
		return nil
	}
	cp := *src
	return &cp
}

func compareRegisterMappings(a, b GatewayModbusRegisterMapping) int {
	if c := strings.Compare(a.PointCode, b.PointCode); c != 0 {
		return c
	}
	if a.Address != b.Address {
		if a.Address < b.Address {
			return -1
		}
		return 1
	}
	if a.FunctionCode != b.FunctionCode {
		if a.FunctionCode < b.FunctionCode {
			return -1
		}
		return 1
	}
	return strings.Compare(a.RawType, b.RawType)
}

func equalOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func registerMappingsEqual(a, b GatewayModbusRegisterMapping) bool {
	return a.PointCode == b.PointCode &&
		a.SemanticChannel == b.SemanticChannel &&
		a.FunctionCode == b.FunctionCode &&
		equalOptionalInt(a.WriteFunctionCode, b.WriteFunctionCode) &&
		a.Address == b.Address &&
		a.RegisterCount == b.RegisterCount &&
		a.RawType == b.RawType &&
		a.ByteOrder == b.ByteOrder &&
		a.WordOrder == b.WordOrder &&
		equalOptionalInt(a.BitIndex, b.BitIndex) &&
		a.Scale == b.Scale &&
		a.Offset == b.Offset &&
		a.PollPriority == b.PollPriority &&
		maps.Equal(a.EnumMap, b.EnumMap)
}

func bacnetSpecsEqual(a, b *edgecontrol.BACnetDriverSpecInput) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !equalOptionalInt(a.DeviceInstance, b.DeviceInstance) ||
		!equalOptionalInt(a.NetworkNumber, b.NetworkNumber) ||
		!equalOptionalInt(a.COVLifetimeSeconds, b.COVLifetimeSeconds) ||
		!equalOptionalInt(a.WritePriority, b.WritePriority) ||
		len(a.Objects) != len(b.Objects) {
		return false
	}
	for i := range a.Objects {
		oa, ob := a.Objects[i], b.Objects[i]
		if oa.PointCode != ob.PointCode ||
			oa.SemanticChannel != ob.SemanticChannel ||
			oa.SafetyBoundary != ob.SafetyBoundary ||
			oa.ObjectType != ob.ObjectType ||
			oa.ObjectInstance != ob.ObjectInstance ||
			!equalOptionalInt(oa.PropertyID, ob.PropertyID) ||
			oa.DataType != ob.DataType ||
			oa.Writable != ob.Writable ||
			oa.COVSubscribe != ob.COVSubscribe ||
			oa.Scale != ob.Scale ||
			oa.Offset != ob.Offset ||
			oa.PollPriority != ob.PollPriority ||
			oa.Unit != ob.Unit ||
			!maps.Equal(oa.EnumMap, ob.EnumMap) {
			return false
		}
	}
	return true
}

func opcuaSpecsEqual(a, b *edgecontrol.OPCUADriverSpecInput) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.SecurityPolicy != b.SecurityPolicy ||
		a.SecurityMode != b.SecurityMode ||
		a.AuthMode != b.AuthMode ||
		a.Username != b.Username ||
		a.CredentialRef != b.CredentialRef ||
		a.SubscriptionIntervalMS != b.SubscriptionIntervalMS ||
		len(a.Nodes) != len(b.Nodes) {
		return false
	}
	for i := range a.Nodes {
		na, nb := a.Nodes[i], b.Nodes[i]
		if na.PointCode != nb.PointCode ||
			na.SemanticChannel != nb.SemanticChannel ||
			na.SafetyBoundary != nb.SafetyBoundary ||
			na.NodeID != nb.NodeID ||
			!equalOptionalInt(na.NamespaceIndex, nb.NamespaceIndex) ||
			na.DataType != nb.DataType ||
			na.Writable != nb.Writable ||
			na.Subscribe != nb.Subscribe ||
			na.Scale != nb.Scale ||
			na.Offset != nb.Offset ||
			na.PollPriority != nb.PollPriority ||
			na.Unit != nb.Unit {
			return false
		}
	}
	return true
}

func driverConfigsEqual(a, b GatewayModbusDriverConfig) bool {
	if a.DeviceKey != b.DeviceKey ||
		a.DriverType != b.DriverType ||
		a.Endpoint != b.Endpoint ||
		a.UnitID != b.UnitID ||
		a.CapabilityProfile != b.CapabilityProfile ||
		a.TimeoutMS != b.TimeoutMS ||
		a.Retries != b.Retries ||
		len(a.Registers) != len(b.Registers) {
		return false
	}
	for i := range a.Registers {
		if !registerMappingsEqual(a.Registers[i], b.Registers[i]) {
			return false
		}
	}
	return bacnetSpecsEqual(a.BACnet, b.BACnet) && opcuaSpecsEqual(a.OPCUA, b.OPCUA)
}

func diffDriverConfigs(desired, reported []GatewayModbusDriverConfig) []DriftDetail {
	canonicalize := func(items []GatewayModbusDriverConfig) map[string]GatewayModbusDriverConfig {
		out := make(map[string]GatewayModbusDriverConfig, len(items))
		for _, item := range items {
			regs := make([]GatewayModbusRegisterMapping, len(item.Registers))
			for i, reg := range item.Registers {
				regs[i] = cloneRegisterMapping(reg)
			}
			slices.SortFunc(regs, compareRegisterMappings)
			copied := item
			copied.Registers = regs
			copied.BACnet = item.BACnet.Clone()
			if copied.BACnet != nil {
				slices.SortFunc(copied.BACnet.Objects, func(a, b edgecontrol.BACnetObjectInput) int {
					return strings.Compare(a.PointCode, b.PointCode)
				})
			}
			copied.OPCUA = item.OPCUA.Clone()
			if copied.OPCUA != nil {
				slices.SortFunc(copied.OPCUA.Nodes, func(a, b edgecontrol.OPCUANodeInput) int {
					return strings.Compare(a.PointCode, b.PointCode)
				})
			}
			out[item.DeviceKey] = copied
		}
		return out
	}
	dMap := canonicalize(desired)
	rMap := canonicalize(reported)
	keys := unionSortedKeys(dMap, rMap)
	var out []DriftDetail
	for _, key := range keys {
		dVal, inD := dMap[key]
		rVal, inR := rMap[key]
		targetKey := clampUTF8Runes(key, maxDriftTargetRunes)
		switch {
		case inD && !inR:
			out = append(out, DriftDetail{
				Section: DriftSectionDriverConfigs,
				Target:  targetKey,
				Reason:  DriftReasonMissingOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("desired driver config (%s %s unit %d) is missing on edge", dVal.DriverType, dVal.Endpoint, dVal.UnitID), maxDriftDetailRunes),
			})
		case !inD && inR:
			out = append(out, DriftDetail{
				Section: DriftSectionDriverConfigs,
				Target:  targetKey,
				Reason:  DriftReasonUnexpectedOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("unexpected driver config (%s %s unit %d) present on edge", rVal.DriverType, rVal.Endpoint, rVal.UnitID), maxDriftDetailRunes),
			})
		case !driverConfigsEqual(dVal, rVal):
			out = append(out, DriftDetail{
				Section: DriftSectionDriverConfigs,
				Target:  targetKey,
				Reason:  DriftReasonValueMismatch,
				Detail:  clampUTF8Runes(fmt.Sprintf("driver config mismatch (desired %s %s unit=%d registers=%d, reported %s %s unit=%d registers=%d)", dVal.DriverType, dVal.Endpoint, dVal.UnitID, len(dVal.Registers), rVal.DriverType, rVal.Endpoint, rVal.UnitID, len(rVal.Registers)), maxDriftDetailRunes),
			})
		}
	}
	return out
}

func diffAutonomyPolicy(desired, reported *GatewayAutonomyPolicy) []DriftDetail {
	switch {
	case desired == nil && reported == nil:
		return nil
	case desired != nil && reported == nil:
		return []DriftDetail{{
			Section: DriftSectionAutonomyPolicy,
			Target:  "autonomyPolicy",
			Reason:  DriftReasonMissingOnEdge,
			Detail:  "desired autonomy policy is missing on edge",
		}}
	case desired == nil && reported != nil:
		return []DriftDetail{{
			Section: DriftSectionAutonomyPolicy,
			Target:  "autonomyPolicy",
			Reason:  DriftReasonUnexpectedOnEdge,
			Detail:  "unexpected autonomy policy present on edge",
		}}
	case *desired != *reported:
		return []DriftDetail{{
			Section: DriftSectionAutonomyPolicy,
			Target:  "autonomyPolicy",
			Reason:  DriftReasonValueMismatch,
			Detail:  clampUTF8Runes(fmt.Sprintf("autonomy policy mismatch (desired enabled=%t grace=%ds setpoint=%.2fC, reported enabled=%t grace=%ds setpoint=%.2fC)", desired.Enabled, desired.OfflineGraceSeconds, desired.DefaultChilledWaterSupplySetpointC, reported.Enabled, reported.OfflineGraceSeconds, reported.DefaultChilledWaterSupplySetpointC), maxDriftDetailRunes),
		}}
	default:
		return nil
	}
}
