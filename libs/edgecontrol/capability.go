package edgecontrol

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type CapabilityProfileID string

type SemanticChannel string

const (
	ProfileVariableSpeedPump  CapabilityProfileID = "VARIABLE_SPEED_PUMP"
	ProfileChiller            CapabilityProfileID = "CHILLER"
	ProfileCoolingTower       CapabilityProfileID = "COOLING_TOWER"
	ProfileElectricityMeter   CapabilityProfileID = "ELECTRICITY_METER"
	ProfileThermalEnergyMeter CapabilityProfileID = "THERMAL_ENERGY_METER"
	ProfileWeatherStation     CapabilityProfileID = "WEATHER_STATION"
)

const (
	SemanticRunState            SemanticChannel = "RunState"
	SemanticFaultCode           SemanticChannel = "FaultCode"
	SemanticStartCommand        SemanticChannel = "StartCommand"
	SemanticStopCommand         SemanticChannel = "StopCommand"
	SemanticResetFaultCommand   SemanticChannel = "ResetFaultCommand"
	SemanticFrequency           SemanticChannel = "Frequency"
	SemanticFrequencySetpoint   SemanticChannel = "FrequencySetpoint"
	SemanticPower               SemanticChannel = "Power"
	SemanticFlow                SemanticChannel = "Flow"
	SemanticTemperatureSetpoint SemanticChannel = "TemperatureSetpoint"
	SemanticLoadLimit           SemanticChannel = "LoadLimit"
	SemanticFanSpeed            SemanticChannel = "FanSpeed"
	SemanticFanSpeedSetpoint    SemanticChannel = "FanSpeedSetpoint"
	SemanticEnergy              SemanticChannel = "Energy"
	SemanticCoolingEnergy       SemanticChannel = "CoolingEnergy"
	SemanticCoolingCapacity     SemanticChannel = "CoolingCapacity"
	SemanticSupplyTemperature   SemanticChannel = "SupplyTemperature"
	SemanticReturnTemperature   SemanticChannel = "ReturnTemperature"
	SemanticVoltage             SemanticChannel = "Voltage"
	SemanticCurrent             SemanticChannel = "Current"
	SemanticOutdoorTemperature  SemanticChannel = "OutdoorTemperature"
	SemanticOutdoorHumidity     SemanticChannel = "OutdoorHumidity"
)

type ChannelRequirement struct {
	Semantic SemanticChannel `json:"semantic"`
	DataType DataType        `json:"dataType"`
	Unit     string          `json:"unit,omitempty"`
	Access   AccessMode      `json:"access"`
	Required bool            `json:"required"`
}

type ControlCapabilitySpec struct {
	CapabilityID string   `json:"capabilityId"`
	ParameterKey string   `json:"parameterKey,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Minimum      *float64 `json:"minimum,omitempty"`
	Maximum      *float64 `json:"maximum,omitempty"`
	Step         *float64 `json:"step,omitempty"`
}

type CapabilityProfile struct {
	ID       CapabilityProfileID     `json:"id"`
	Revision string                  `json:"revision"`
	Channels []ChannelRequirement    `json:"channels"`
	Controls []ControlCapabilitySpec `json:"controls,omitempty"`
}

func (profile CapabilityProfile) validate() error {
	if strings.TrimSpace(string(profile.ID)) == "" || strings.TrimSpace(profile.Revision) == "" {
		return errors.New("capability profile ID and revision are required")
	}
	if len(profile.Channels) == 0 {
		return errors.New("capability profile must declare at least one channel")
	}
	seenChannels := map[SemanticChannel]struct{}{}
	for _, requirement := range profile.Channels {
		if strings.TrimSpace(string(requirement.Semantic)) == "" {
			return errors.New("semantic channel is required")
		}
		if _, exists := seenChannels[requirement.Semantic]; exists {
			return fmt.Errorf("semantic channel %s is declared more than once", requirement.Semantic)
		}
		seenChannels[requirement.Semantic] = struct{}{}
		switch requirement.DataType {
		case DataTypeBoolean, DataTypeString, DataTypeInteger, DataTypeLong, DataTypeFloat, DataTypeDouble:
		default:
			return fmt.Errorf("semantic channel %s uses unsupported data type %q", requirement.Semantic, requirement.DataType)
		}
		switch requirement.Access {
		case AccessReadOnly, AccessReadWrite, AccessWriteOnly:
		default:
			return fmt.Errorf("semantic channel %s uses unsupported access mode %q", requirement.Semantic, requirement.Access)
		}
	}
	seenControls := map[string]struct{}{}
	for _, control := range profile.Controls {
		if strings.TrimSpace(control.CapabilityID) == "" {
			return errors.New("control capability ID is required")
		}
		if _, exists := seenControls[control.CapabilityID]; exists {
			return fmt.Errorf("control capability %s is declared more than once", control.CapabilityID)
		}
		seenControls[control.CapabilityID] = struct{}{}
		if (control.Minimum == nil) != (control.Maximum == nil) {
			return fmt.Errorf("control capability %s must declare both minimum and maximum or neither", control.CapabilityID)
		}
		if control.Minimum != nil && *control.Minimum > *control.Maximum {
			return fmt.Errorf("control capability %s minimum exceeds maximum", control.CapabilityID)
		}
		if control.Step != nil && *control.Step <= 0 {
			return fmt.Errorf("control capability %s step must be positive", control.CapabilityID)
		}
	}
	return nil
}

func ptr(value float64) *float64 { return &value }

func StandardCapabilityProfiles() []CapabilityProfile {
	return []CapabilityProfile{
		{
			ID: ProfileVariableSpeedPump, Revision: "edge-profile:variable-speed-pump:v1",
			Channels: []ChannelRequirement{
				{Semantic: SemanticRunState, DataType: DataTypeString, Access: AccessReadOnly, Required: true},
				{Semantic: SemanticFaultCode, DataType: DataTypeString, Access: AccessReadOnly, Required: true},
				{Semantic: SemanticStartCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticStopCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticResetFaultCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticFrequency, DataType: DataTypeDouble, Unit: "Hz", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticFrequencySetpoint, DataType: DataTypeDouble, Unit: "Hz", Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticPower, DataType: DataTypeDouble, Unit: "kW", Access: AccessReadOnly, Required: false},
				{Semantic: SemanticFlow, DataType: DataTypeDouble, Unit: "m3/h", Access: AccessReadOnly, Required: false},
			},
			Controls: []ControlCapabilitySpec{
				{CapabilityID: "START"}, {CapabilityID: "STOP"}, {CapabilityID: "RESET_FAULT"},
				{CapabilityID: "SET_FREQUENCY", ParameterKey: "frequencyHz", Unit: "Hz", Minimum: ptr(20), Maximum: ptr(50), Step: ptr(0.5)},
			},
		},
		{
			ID: ProfileChiller, Revision: "edge-profile:chiller:v1",
			Channels: []ChannelRequirement{
				{Semantic: SemanticRunState, DataType: DataTypeString, Access: AccessReadOnly, Required: true},
				{Semantic: SemanticFaultCode, DataType: DataTypeString, Access: AccessReadOnly, Required: true},
				{Semantic: SemanticStartCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticStopCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticResetFaultCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticTemperatureSetpoint, DataType: DataTypeDouble, Unit: "Cel", Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticLoadLimit, DataType: DataTypeDouble, Unit: "%", Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticPower, DataType: DataTypeDouble, Unit: "kW", Access: AccessReadOnly, Required: false},
			},
			Controls: []ControlCapabilitySpec{
				{CapabilityID: "START"}, {CapabilityID: "STOP"}, {CapabilityID: "RESET_FAULT"},
				{CapabilityID: "SET_CHILLED_WATER_TEMPERATURE_SETPOINT", ParameterKey: "setpointC", Unit: "Cel", Minimum: ptr(5), Maximum: ptr(12), Step: ptr(0.5)},
				{CapabilityID: "SET_LOAD_LIMIT", ParameterKey: "loadLimitPct", Unit: "%", Minimum: ptr(20), Maximum: ptr(100), Step: ptr(1)},
			},
		},
		{
			ID: ProfileCoolingTower, Revision: "edge-profile:cooling-tower:v1",
			Channels: []ChannelRequirement{
				{Semantic: SemanticRunState, DataType: DataTypeString, Access: AccessReadOnly, Required: true},
				{Semantic: SemanticFaultCode, DataType: DataTypeString, Access: AccessReadOnly, Required: true},
				{Semantic: SemanticStartCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticStopCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticResetFaultCommand, DataType: DataTypeBoolean, Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticFanSpeed, DataType: DataTypeDouble, Unit: "%", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticFanSpeedSetpoint, DataType: DataTypeDouble, Unit: "%", Access: AccessWriteOnly, Required: true},
				{Semantic: SemanticPower, DataType: DataTypeDouble, Unit: "kW", Access: AccessReadOnly, Required: false},
			},
			Controls: []ControlCapabilitySpec{
				{CapabilityID: "START"}, {CapabilityID: "STOP"}, {CapabilityID: "RESET_FAULT"},
				{CapabilityID: "SET_FAN_SPEED", ParameterKey: "fanSpeedPct", Unit: "%", Minimum: ptr(20), Maximum: ptr(100), Step: ptr(1)},
			},
		},
		{
			ID: ProfileElectricityMeter, Revision: "edge-profile:electricity-meter:v1",
			Channels: []ChannelRequirement{
				{Semantic: SemanticPower, DataType: DataTypeDouble, Unit: "kW", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticEnergy, DataType: DataTypeDouble, Unit: "kWh", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticVoltage, DataType: DataTypeDouble, Unit: "V", Access: AccessReadOnly, Required: false},
				{Semantic: SemanticCurrent, DataType: DataTypeDouble, Unit: "A", Access: AccessReadOnly, Required: false},
			},
		},
		{
			ID: ProfileThermalEnergyMeter, Revision: "edge-profile:thermal-energy-meter:v1",
			Channels: []ChannelRequirement{
				{Semantic: SemanticSupplyTemperature, DataType: DataTypeDouble, Unit: "Cel", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticReturnTemperature, DataType: DataTypeDouble, Unit: "Cel", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticFlow, DataType: DataTypeDouble, Unit: "m3/h", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticCoolingCapacity, DataType: DataTypeDouble, Unit: "kW", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticCoolingEnergy, DataType: DataTypeDouble, Unit: "kWh", Access: AccessReadOnly, Required: true},
			},
		},
		{
			ID: ProfileWeatherStation, Revision: "edge-profile:weather-station:v1",
			Channels: []ChannelRequirement{
				{Semantic: SemanticOutdoorTemperature, DataType: DataTypeDouble, Unit: "Cel", Access: AccessReadOnly, Required: true},
				{Semantic: SemanticOutdoorHumidity, DataType: DataTypeDouble, Unit: "%RH", Access: AccessReadOnly, Required: true},
			},
		},
	}
}

type CapabilityRegistry struct {
	mu       sync.RWMutex
	profiles map[CapabilityProfileID]CapabilityProfile
}

func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{profiles: map[CapabilityProfileID]CapabilityProfile{}}
}

func NewStandardCapabilityRegistry() (*CapabilityRegistry, error) {
	registry := NewCapabilityRegistry()
	for _, profile := range StandardCapabilityProfiles() {
		if err := registry.Register(profile); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (registry *CapabilityRegistry) Register(profile CapabilityProfile) error {
	if registry == nil {
		return errors.New("capability registry is nil")
	}
	if err := profile.validate(); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.profiles[profile.ID]; exists {
		return fmt.Errorf("capability profile %s is already registered", profile.ID)
	}
	profile.Channels = append([]ChannelRequirement(nil), profile.Channels...)
	profile.Controls = cloneControls(profile.Controls)
	registry.profiles[profile.ID] = profile
	return nil
}

func cloneControls(controls []ControlCapabilitySpec) []ControlCapabilitySpec {
	out := make([]ControlCapabilitySpec, len(controls))
	for index, control := range controls {
		out[index] = control
		if control.Minimum != nil {
			value := *control.Minimum
			out[index].Minimum = &value
		}
		if control.Maximum != nil {
			value := *control.Maximum
			out[index].Maximum = &value
		}
		if control.Step != nil {
			value := *control.Step
			out[index].Step = &value
		}
	}
	return out
}

func (registry *CapabilityRegistry) Get(id CapabilityProfileID) (CapabilityProfile, bool) {
	if registry == nil {
		return CapabilityProfile{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	profile, ok := registry.profiles[id]
	if !ok {
		return CapabilityProfile{}, false
	}
	profile.Channels = append([]ChannelRequirement(nil), profile.Channels...)
	profile.Controls = cloneControls(profile.Controls)
	return profile, true
}

func (registry *CapabilityRegistry) Profiles() []CapabilityProfile {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	ids := make([]string, 0, len(registry.profiles))
	for id := range registry.profiles {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]CapabilityProfile, 0, len(ids))
	for _, raw := range ids {
		profile := registry.profiles[CapabilityProfileID(raw)]
		profile.Channels = append([]ChannelRequirement(nil), profile.Channels...)
		profile.Controls = cloneControls(profile.Controls)
		out = append(out, profile)
	}
	return out
}
