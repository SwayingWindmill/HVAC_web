package simulator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type simulatedDeviceAdapter struct {
	mu        sync.Mutex
	plant     *Plant
	component edgecontrol.ComponentDescriptor
	channels  []edgecontrol.ChannelDescriptor
	points    map[string]PointConfig
	sequences map[string]uint64
}

func newSimulatedDeviceAdapters(config Config, plant *Plant) ([]edgecontrol.DeviceAdapter, error) {
	if plant == nil {
		return nil, errors.New("plant is required")
	}
	pointsByDevice := make(map[string][]PointConfig)
	for _, point := range config.Points {
		pointsByDevice[point.DeviceID] = append(pointsByDevice[point.DeviceID], point)
	}
	adapters := make([]edgecontrol.DeviceAdapter, 0, len(config.Devices))
	for _, device := range config.Devices {
		profile, ok := capabilityProfileForDevice(config.Plant, device.ID)
		if !ok {
			continue
		}
		adapter, err := newSimulatedDeviceAdapter(device, profile, pointsByDevice[device.ID], plant)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, adapter)
	}
	return adapters, nil
}

func capabilityProfileForDevice(plant PlantConfig, deviceID string) (edgecontrol.CapabilityProfileID, bool) {
	switch deviceID {
	case plant.Chiller.ID:
		return edgecontrol.ProfileChiller, true
	case plant.ChilledWaterPump.ID, plant.CoolingWaterPump.ID:
		return edgecontrol.ProfileVariableSpeedPump, true
	case plant.CoolingTower.ID:
		return edgecontrol.ProfileCoolingTower, true
	case plant.PowerMeterID:
		return edgecontrol.ProfileElectricityMeter, true
	case plant.BTUMeterID:
		return edgecontrol.ProfileThermalEnergyMeter, true
	case plant.WeatherStationID:
		return edgecontrol.ProfileWeatherStation, true
	default:
		return "", false
	}
}

func newSimulatedDeviceAdapter(device DeviceEndpointConfig, profile edgecontrol.CapabilityProfileID, points []PointConfig, plant *Plant) (*simulatedDeviceAdapter, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("device %s has no canonical Points", device.ID)
	}
	channels := make([]edgecontrol.ChannelDescriptor, 0, len(points))
	pointByAddress := make(map[string]PointConfig, len(points))
	bindings := map[edgecontrol.SemanticChannel]string{}
	for _, point := range points {
		dataType, err := edgeDataType(point.ValueType)
		if err != nil {
			return nil, fmt.Errorf("device %s point %s: %w", device.ID, point.PointCode, err)
		}
		persistencePriority := edgePersistencePriority(point)
		descriptor := edgecontrol.ChannelDescriptor{
			ComponentID:               device.ID,
			ChannelID:                 point.PointCode,
			PointID:                   point.PointID,
			DataType:                  dataType,
			Access:                    edgeAccessMode(point),
			Description:               point.Name,
			Unit:                      point.Unit,
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              edgePollPriority(point),
			LocalPersistencePriority:  persistencePriority,
			RemotePersistencePriority: persistencePriority,
			AggregationPriority:       edgeAggregationPriority(point),
			ResendPriority:            edgeResendPriority(point),
		}
		channels = append(channels, descriptor)
		pointByAddress[descriptor.Address()] = point
		if semantic, ok := semanticChannelForPoint(profile, point); ok {
			if previous, duplicate := bindings[semantic]; duplicate && previous != descriptor.Address() {
				return nil, fmt.Errorf("device %s maps semantic channel %s more than once", device.ID, semantic)
			}
			bindings[semantic] = descriptor.Address()
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Address() < channels[j].Address() })
	return &simulatedDeviceAdapter{
		plant: plant,
		component: edgecontrol.ComponentDescriptor{
			ID: device.ID, Alias: device.Name, Enabled: true,
			Kind: edgecontrol.ComponentSimulator, Type: "EG8200_SIMULATED_" + strings.ToUpper(device.Type),
			FactoryID: "EG8200_SIMULATED_" + strings.ToUpper(device.Type), Version: "v1",
			Profiles: []edgecontrol.CapabilityProfileID{profile}, ChannelBindings: bindings,
		},
		channels: channels, points: pointByAddress, sequences: map[string]uint64{},
	}, nil
}

func (driver *simulatedDeviceAdapter) Component() edgecontrol.ComponentDescriptor {
	return driver.component
}
func (driver *simulatedDeviceAdapter) Channels() []edgecontrol.ChannelDescriptor {
	return append([]edgecontrol.ChannelDescriptor(nil), driver.channels...)
}

func (driver *simulatedDeviceAdapter) Poll(_ context.Context, at time.Time) ([]edgecontrol.ChannelUpdate, error) {
	snapshot := driver.plant.Snapshot()
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = at
	}
	telemetry, ok := snapshot.Devices[driver.component.ID]
	if !ok {
		return nil, fmt.Errorf("plant snapshot is missing device %s", driver.component.ID)
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	updates := make([]edgecontrol.ChannelUpdate, 0, len(driver.channels))
	for _, channel := range driver.channels {
		point := driver.points[channel.Address()]
		if point.PointType == "COMMAND" {
			continue
		}
		raw, exists := telemetry[point.SourceKey]
		if !exists {
			return nil, fmt.Errorf("device %s is missing source key %s", driver.component.ID, point.SourceKey)
		}
		value, err := edgeValue(channel.DataType, raw)
		if err != nil {
			return nil, fmt.Errorf("device %s point %s: %w", driver.component.ID, point.PointCode, err)
		}
		driver.sequences[channel.Address()]++
		updates = append(updates, edgecontrol.ChannelUpdate{
			Address: channel.Address(),
			Sample:  edgecontrol.Sample{Value: value, Quality: edgecontrol.QualityGood, ObservedAt: snapshot.ObservedAt, Sequence: driver.sequences[channel.Address()]},
		})
	}
	return updates, nil
}

func (driver *simulatedDeviceAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, decisions []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	results := make([]edgecontrol.DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		point, ok := driver.points[decision.Address]
		if !ok || point.PointType != "COMMAND" || decision.Effective == nil {
			results = append(results, edgecontrol.DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_CHANNEL_INVALID"})
			continue
		}
		params := map[string]float64{}
		if controlKind(point) == "ACTION" {
			if decision.Effective.Type != edgecontrol.DataTypeBoolean || !decision.Effective.Boolean {
				results = append(results, edgecontrol.DeviceWriteResult{Address: decision.Address, Success: false, Code: "ACTION_TRIGGER_REQUIRED"})
				continue
			}
		} else {
			parameterKey, _ := point.SourceMetadata["parameterKey"].(string)
			if parameterKey == "" || decision.Effective.Type != edgecontrol.DataTypeDouble {
				results = append(results, edgecontrol.DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_PARAMETER_INVALID"})
				continue
			}
			params[parameterKey] = decision.Effective.Double
		}
		applied := driver.plant.ApplyCommand(Command{DeviceID: driver.component.ID, Method: point.SourceKey, Params: params})
		writeResult := edgecontrol.DeviceWriteResult{Address: decision.Address, Success: applied.Success, Code: applied.Code}
		if applied.Success {
			if controlKind(point) == "ACTION" {
				value := edgecontrol.BooleanValue(true)
				writeResult.AppliedValue = &value
			} else {
				value := edgecontrol.DoubleValue(applied.AppliedValue)
				writeResult.AppliedValue = &value
			}
		}
		results = append(results, writeResult)
	}
	return results, nil
}

func edgeDataType(valueType string) (edgecontrol.DataType, error) {
	switch valueType {
	case "BOOLEAN":
		return edgecontrol.DataTypeBoolean, nil
	case "NUMBER":
		return edgecontrol.DataTypeDouble, nil
	case "STRING":
		return edgecontrol.DataTypeString, nil
	default:
		return "", fmt.Errorf("unsupported Edge valueType %s", valueType)
	}
}

func edgeAccessMode(point PointConfig) edgecontrol.AccessMode {
	if point.PointType == "COMMAND" {
		return edgecontrol.AccessWriteOnly
	}
	return edgecontrol.AccessReadOnly
}

func edgePollPriority(point PointConfig) edgecontrol.DataPriority {
	switch point.PointType {
	case "STATE", "COMMAND":
		return edgecontrol.PriorityVeryHigh
	case "SETTING", "COUNTER":
		return edgecontrol.PriorityHigh
	default:
		return edgecontrol.PriorityMedium
	}
}

func edgePersistencePriority(point PointConfig) edgecontrol.DataPriority {
	switch point.PointType {
	case "STATE", "COMMAND", "COUNTER":
		return edgecontrol.PriorityVeryHigh
	case "SETTING":
		return edgecontrol.PriorityHigh
	default:
		return edgecontrol.PriorityMedium
	}
}

func edgeAggregationPriority(point PointConfig) edgecontrol.DataPriority {
	if point.PointType == "TELEMETRY" || point.PointType == "COUNTER" {
		return edgecontrol.PriorityHigh
	}
	return edgecontrol.PriorityLow
}

func edgeResendPriority(point PointConfig) edgecontrol.DataPriority {
	switch point.PointType {
	case "STATE", "COMMAND":
		return edgecontrol.PriorityVeryHigh
	case "SETTING", "COUNTER":
		return edgecontrol.PriorityHigh
	default:
		return edgecontrol.PriorityMedium
	}
}

func edgeValue(dataType edgecontrol.DataType, raw any) (edgecontrol.Value, error) {
	switch dataType {
	case edgecontrol.DataTypeString:
		value, ok := raw.(string)
		if !ok {
			return edgecontrol.Value{}, fmt.Errorf("expected text, got %T", raw)
		}
		return edgecontrol.StringValue(value), nil
	case edgecontrol.DataTypeBoolean:
		value, ok := raw.(bool)
		if !ok {
			return edgecontrol.Value{}, fmt.Errorf("expected bool, got %T", raw)
		}
		return edgecontrol.BooleanValue(value), nil
	case edgecontrol.DataTypeDouble:
		switch value := raw.(type) {
		case float64:
			return edgecontrol.DoubleValue(value), nil
		case float32:
			return edgecontrol.DoubleValue(float64(value)), nil
		case int:
			return edgecontrol.DoubleValue(float64(value)), nil
		case int64:
			return edgecontrol.DoubleValue(float64(value)), nil
		case uint64:
			return edgecontrol.DoubleValue(float64(value)), nil
		default:
			return edgecontrol.Value{}, fmt.Errorf("expected number, got %T", raw)
		}
	default:
		return edgecontrol.Value{}, fmt.Errorf("unsupported Edge data type %s", dataType)
	}
}

func semanticChannelForPoint(profile edgecontrol.CapabilityProfileID, point PointConfig) (edgecontrol.SemanticChannel, bool) {
	if point.PointType == "COMMAND" {
		capability, _ := point.SourceMetadata["capability"].(string)
		switch capability {
		case "START":
			return edgecontrol.SemanticStartCommand, true
		case "STOP":
			return edgecontrol.SemanticStopCommand, true
		case "RESET_FAULT":
			return edgecontrol.SemanticResetFaultCommand, true
		case "SET_FREQUENCY":
			return edgecontrol.SemanticFrequencySetpoint, true
		case "SET_CHILLED_WATER_TEMPERATURE_SETPOINT":
			return edgecontrol.SemanticTemperatureSetpoint, true
		case "SET_LOAD_LIMIT":
			return edgecontrol.SemanticLoadLimit, true
		case "SET_FAN_SPEED":
			return edgecontrol.SemanticFanSpeedSetpoint, true
		default:
			return "", false
		}
	}
	switch profile {
	case edgecontrol.ProfileVariableSpeedPump:
		switch point.SourceKey {
		case "runState":
			return edgecontrol.SemanticRunState, true
		case "faultCode":
			return edgecontrol.SemanticFaultCode, true
		case "frequencyHz":
			return edgecontrol.SemanticFrequency, true
		case "powerKw":
			return edgecontrol.SemanticPower, true
		case "flowRateM3h":
			return edgecontrol.SemanticFlow, true
		}
	case edgecontrol.ProfileChiller:
		switch point.SourceKey {
		case "runState":
			return edgecontrol.SemanticRunState, true
		case "faultCode":
			return edgecontrol.SemanticFaultCode, true
		case "powerKw":
			return edgecontrol.SemanticPower, true
		}
	case edgecontrol.ProfileCoolingTower:
		switch point.SourceKey {
		case "runState":
			return edgecontrol.SemanticRunState, true
		case "faultCode":
			return edgecontrol.SemanticFaultCode, true
		case "fanSpeedPct":
			return edgecontrol.SemanticFanSpeed, true
		case "powerKw":
			return edgecontrol.SemanticPower, true
		}
	case edgecontrol.ProfileElectricityMeter:
		switch point.SourceKey {
		case "activePowerKw":
			return edgecontrol.SemanticPower, true
		case "energyKwh":
			return edgecontrol.SemanticEnergy, true
		}
	case edgecontrol.ProfileThermalEnergyMeter:
		switch point.SourceKey {
		case "supplyWaterTemperatureC":
			return edgecontrol.SemanticSupplyTemperature, true
		case "returnWaterTemperatureC":
			return edgecontrol.SemanticReturnTemperature, true
		case "flowRateM3h":
			return edgecontrol.SemanticFlow, true
		case "instantCoolingCapacityKw":
			return edgecontrol.SemanticCoolingCapacity, true
		case "accumulatedCoolingEnergyKwh":
			return edgecontrol.SemanticCoolingEnergy, true
		}
	case edgecontrol.ProfileWeatherStation:
		switch point.SourceKey {
		case "ambientDryBulbTemperatureC":
			return edgecontrol.SemanticOutdoorTemperature, true
		case "relativeHumidityPct":
			return edgecontrol.SemanticOutdoorHumidity, true
		}
	}
	return "", false
}

func controlKind(point PointConfig) string {
	value, _ := point.SourceMetadata["controlKind"].(string)
	return value
}
