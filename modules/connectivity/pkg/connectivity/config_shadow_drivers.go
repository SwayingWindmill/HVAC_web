package connectivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	DriftSectionDriverConfigs  = "DRIVER_CONFIGS"
	DriftSectionAutonomyPolicy = "AUTONOMY_POLICY"

	maxDriverConfigsJSONBytes  = 262144
	maxAutonomyPolicyJSONBytes = 65536
	maxDriverRegisters         = 256
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
	DeviceKey         string                         `json:"deviceKey"`
	DriverType        string                         `json:"driverType"`
	Endpoint          string                         `json:"endpoint"`
	UnitID            int                            `json:"unitId"`
	CapabilityProfile string                         `json:"capabilityProfile,omitempty"`
	TimeoutMS         int                            `json:"timeoutMs"`
	Retries           int                            `json:"retries"`
	Registers         []GatewayModbusRegisterMapping `json:"registers"`
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

func isFiniteShadowFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func normalizeZeroFloat(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

func validModbusEndianness(value string) bool {
	return value == "BIG_ENDIAN" || value == "LITTLE_ENDIAN"
}

func validateDriverConfigs(input []GatewayModbusDriverConfig) ([]GatewayModbusDriverConfig, error) {
	seenDevices := make(map[string]struct{}, len(input))
	out := make([]GatewayModbusDriverConfig, 0, len(input))
	for i, driver := range input {
		deviceKey := strings.TrimSpace(driver.DeviceKey)
		if deviceKey == "" || utf8.RuneCountInString(deviceKey) > 128 {
			return nil, fmt.Errorf("%w: driverConfigs[%d].deviceKey must be 1..128 characters", ErrConfigShadowInvalid, i)
		}
		if _, dup := seenDevices[deviceKey]; dup {
			return nil, fmt.Errorf("%w: duplicate driverConfigs deviceKey %q", ErrConfigShadowInvalid, deviceKey)
		}
		seenDevices[deviceKey] = struct{}{}

		driverType := strings.TrimSpace(driver.DriverType)
		switch driverType {
		case "MODBUS_TCP", "MODBUS_RTU":
		default:
			return nil, fmt.Errorf("%w: driverConfigs[%d].driverType %q is invalid", ErrConfigShadowInvalid, i, driver.DriverType)
		}

		endpoint := strings.TrimSpace(driver.Endpoint)
		if endpoint == "" || utf8.RuneCountInString(endpoint) > 256 {
			return nil, fmt.Errorf("%w: driverConfigs[%d].endpoint must be 1..256 characters", ErrConfigShadowInvalid, i)
		}
		if driver.UnitID < 1 || driver.UnitID > 247 {
			return nil, fmt.Errorf("%w: driverConfigs[%d].unitId=%d out of range [1,247]", ErrConfigShadowInvalid, i, driver.UnitID)
		}
		if driver.TimeoutMS < 100 || driver.TimeoutMS > 30000 {
			return nil, fmt.Errorf("%w: driverConfigs[%d].timeoutMs=%d out of range [100,30000]", ErrConfigShadowInvalid, i, driver.TimeoutMS)
		}
		if driver.Retries < 0 || driver.Retries > 10 {
			return nil, fmt.Errorf("%w: driverConfigs[%d].retries=%d out of range [0,10]", ErrConfigShadowInvalid, i, driver.Retries)
		}

		capProfile := ""
		if driver.CapabilityProfile != "" {
			capProfile = strings.TrimSpace(driver.CapabilityProfile)
			if capProfile == "" || utf8.RuneCountInString(capProfile) > 64 {
				return nil, fmt.Errorf("%w: driverConfigs[%d].capabilityProfile must be 1..64 characters", ErrConfigShadowInvalid, i)
			}
		}

		if len(driver.Registers) < 1 || len(driver.Registers) > maxDriverRegisters {
			return nil, fmt.Errorf("%w: driverConfigs[%d].registers must contain 1..%d items", ErrConfigShadowInvalid, i, maxDriverRegisters)
		}

		seenPoints := make(map[string]struct{}, len(driver.Registers))
		regs := make([]GatewayModbusRegisterMapping, 0, len(driver.Registers))
		for j, reg := range driver.Registers {
			cleanedReg, err := validateDriverRegisterMapping(reg, i, j)
			if err != nil {
				return nil, err
			}
			if _, dup := seenPoints[cleanedReg.PointCode]; dup {
				return nil, fmt.Errorf("%w: duplicate register pointCode %q in driverConfigs[%d]", ErrConfigShadowInvalid, cleanedReg.PointCode, i)
			}
			seenPoints[cleanedReg.PointCode] = struct{}{}
			regs = append(regs, cleanedReg)
		}
		slices.SortFunc(regs, compareRegisterMappings)

		out = append(out, GatewayModbusDriverConfig{
			DeviceKey:         deviceKey,
			DriverType:        driverType,
			Endpoint:          endpoint,
			UnitID:            driver.UnitID,
			CapabilityProfile: capProfile,
			TimeoutMS:         driver.TimeoutMS,
			Retries:           driver.Retries,
			Registers:         regs,
		})
	}
	slices.SortFunc(out, func(a, b GatewayModbusDriverConfig) int {
		return strings.Compare(a.DeviceKey, b.DeviceKey)
	})
	return out, nil
}

func validateDriverRegisterMapping(reg GatewayModbusRegisterMapping, driverIdx, regIdx int) (GatewayModbusRegisterMapping, error) {
	pointCode := strings.TrimSpace(reg.PointCode)
	if !shadowPointCodePattern.MatchString(pointCode) {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].pointCode %q is invalid", ErrConfigShadowInvalid, driverIdx, regIdx, reg.PointCode)
	}
	semanticChannel := ""
	if reg.SemanticChannel != "" {
		semanticChannel = strings.TrimSpace(reg.SemanticChannel)
		if semanticChannel == "" || utf8.RuneCountInString(semanticChannel) > 64 {
			return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].semanticChannel must be 1..64 characters", ErrConfigShadowInvalid, driverIdx, regIdx)
		}
	}
	if reg.FunctionCode != 3 && reg.FunctionCode != 4 {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].functionCode=%d must be 3 or 4", ErrConfigShadowInvalid, driverIdx, regIdx, reg.FunctionCode)
	}
	var writeFC *int
	if reg.WriteFunctionCode != nil {
		if *reg.WriteFunctionCode != 6 && *reg.WriteFunctionCode != 16 {
			return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].writeFunctionCode=%d must be 6 or 16", ErrConfigShadowInvalid, driverIdx, regIdx, *reg.WriteFunctionCode)
		}
		v := *reg.WriteFunctionCode
		writeFC = &v
	}
	if reg.Address < 0 || reg.Address > 65535 {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].address=%d out of range [0,65535]", ErrConfigShadowInvalid, driverIdx, regIdx, reg.Address)
	}

	rawType := strings.TrimSpace(reg.RawType)
	var expectedCount int
	switch rawType {
	case "UINT16", "INT16", "BIT_BOOL", "ENUM16":
		expectedCount = 1
	case "UINT32", "INT32", "FLOAT32":
		expectedCount = 2
	default:
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].rawType %q is invalid", ErrConfigShadowInvalid, driverIdx, regIdx, reg.RawType)
	}
	if reg.RegisterCount < 1 || reg.RegisterCount > 4 || reg.RegisterCount != expectedCount {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].registerCount=%d must equal %d for %s", ErrConfigShadowInvalid, driverIdx, regIdx, reg.RegisterCount, expectedCount, rawType)
	}

	byteOrder := strings.TrimSpace(reg.ByteOrder)
	if !validModbusEndianness(byteOrder) {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].byteOrder %q is invalid", ErrConfigShadowInvalid, driverIdx, regIdx, reg.ByteOrder)
	}
	wordOrder := strings.TrimSpace(reg.WordOrder)
	if !validModbusEndianness(wordOrder) {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].wordOrder %q is invalid", ErrConfigShadowInvalid, driverIdx, regIdx, reg.WordOrder)
	}

	var bitIndex *int
	if rawType == "BIT_BOOL" {
		if reg.BitIndex == nil || *reg.BitIndex < 0 || *reg.BitIndex > 15 {
			return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].bitIndex must be in [0,15] for BIT_BOOL", ErrConfigShadowInvalid, driverIdx, regIdx)
		}
		v := *reg.BitIndex
		bitIndex = &v
	} else if reg.BitIndex != nil {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].bitIndex is only valid for BIT_BOOL", ErrConfigShadowInvalid, driverIdx, regIdx)
	}

	if !isFiniteShadowFloat(reg.Scale) || reg.Scale == 0 {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].scale must be finite and non-zero", ErrConfigShadowInvalid, driverIdx, regIdx)
	}
	if !isFiniteShadowFloat(reg.Offset) {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].offset must be finite", ErrConfigShadowInvalid, driverIdx, regIdx)
	}

	pollPriority := strings.TrimSpace(reg.PollPriority)
	switch pollPriority {
	case "VERY_HIGH", "HIGH", "MEDIUM", "LOW":
	default:
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].pollPriority %q is invalid", ErrConfigShadowInvalid, driverIdx, regIdx, reg.PollPriority)
	}

	var enumMap map[string]string
	if rawType == "ENUM16" {
		if len(reg.EnumMap) == 0 {
			return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].enumMap must be non-empty for ENUM16", ErrConfigShadowInvalid, driverIdx, regIdx)
		}
		enumMap = make(map[string]string, len(reg.EnumMap))
		for rawK, rawV := range reg.EnumMap {
			k := strings.TrimSpace(rawK)
			v := strings.TrimSpace(rawV)
			if k == "" {
				return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].enumMap key must be non-empty", ErrConfigShadowInvalid, driverIdx, regIdx)
			}
			if _, dup := enumMap[k]; dup {
				return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].enumMap has duplicate key %q", ErrConfigShadowInvalid, driverIdx, regIdx, k)
			}
			if v == "" || utf8.RuneCountInString(v) > 64 {
				return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].enumMap[%q] must be 1..64 characters", ErrConfigShadowInvalid, driverIdx, regIdx, k)
			}
			enumMap[k] = v
		}
	} else if len(reg.EnumMap) > 0 {
		return GatewayModbusRegisterMapping{}, fmt.Errorf("%w: driverConfigs[%d].registers[%d].enumMap is only valid for ENUM16", ErrConfigShadowInvalid, driverIdx, regIdx)
	}

	return GatewayModbusRegisterMapping{
		PointCode:         pointCode,
		SemanticChannel:   semanticChannel,
		FunctionCode:      reg.FunctionCode,
		WriteFunctionCode: writeFC,
		Address:           reg.Address,
		RegisterCount:     reg.RegisterCount,
		RawType:           rawType,
		ByteOrder:         byteOrder,
		WordOrder:         wordOrder,
		BitIndex:          bitIndex,
		Scale:             reg.Scale,
		Offset:            normalizeZeroFloat(reg.Offset),
		PollPriority:      pollPriority,
		EnumMap:           enumMap,
	}, nil
}

func validateAutonomyPolicy(input *GatewayAutonomyPolicy) (*GatewayAutonomyPolicy, error) {
	if input == nil {
		return nil, nil
	}
	if input.OfflineGraceSeconds < 10 || input.OfflineGraceSeconds > 3600 {
		return nil, fmt.Errorf("%w: autonomyPolicy.offlineGraceSeconds=%d out of range [10,3600]", ErrConfigShadowInvalid, input.OfflineGraceSeconds)
	}
	if !isFiniteShadowFloat(input.DefaultChilledWaterSupplySetpointC) || input.DefaultChilledWaterSupplySetpointC < 4 || input.DefaultChilledWaterSupplySetpointC > 18 {
		return nil, fmt.Errorf("%w: autonomyPolicy.defaultChilledWaterSupplySetpointC=%v out of range [4,18]", ErrConfigShadowInvalid, input.DefaultChilledWaterSupplySetpointC)
	}
	for _, freq := range []struct {
		name string
		val  float64
	}{
		{"minPumpFrequencyHz", input.MinPumpFrequencyHz},
		{"defaultPumpFrequencyHz", input.DefaultPumpFrequencyHz},
		{"maxPumpFrequencyHz", input.MaxPumpFrequencyHz},
	} {
		if !isFiniteShadowFloat(freq.val) || freq.val < 15 || freq.val > 50 {
			return nil, fmt.Errorf("%w: autonomyPolicy.%s=%v out of range [15,50]", ErrConfigShadowInvalid, freq.name, freq.val)
		}
	}
	if input.MinPumpFrequencyHz > input.DefaultPumpFrequencyHz || input.DefaultPumpFrequencyHz > input.MaxPumpFrequencyHz {
		return nil, fmt.Errorf("%w: autonomyPolicy pump frequencies must satisfy min <= default <= max", ErrConfigShadowInvalid)
	}
	if input.MinRunSeconds < 0 || input.MinRunSeconds > 3600 {
		return nil, fmt.Errorf("%w: autonomyPolicy.minRunSeconds=%d out of range [0,3600]", ErrConfigShadowInvalid, input.MinRunSeconds)
	}
	if input.MinStopSeconds < 0 || input.MinStopSeconds > 3600 {
		return nil, fmt.Errorf("%w: autonomyPolicy.minStopSeconds=%d out of range [0,3600]", ErrConfigShadowInvalid, input.MinStopSeconds)
	}
	if input.SnapshotIntervalSeconds < 30 || input.SnapshotIntervalSeconds > 3600 {
		return nil, fmt.Errorf("%w: autonomyPolicy.snapshotIntervalSeconds=%d out of range [30,3600]", ErrConfigShadowInvalid, input.SnapshotIntervalSeconds)
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
			out[i].Registers = []GatewayModbusRegisterMapping{}
		}
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
	return true
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
