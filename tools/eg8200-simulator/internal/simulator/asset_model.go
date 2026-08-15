package simulator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type SpaceConfig struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type"`
}

type AssetConfig struct {
	ID     string `json:"id"`
	SpaceID string `json:"spaceId"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

type DeviceEndpointConfig struct {
	ID           string   `json:"id"`
	SpaceID       string   `json:"spaceId,omitempty"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	AssetIDs []string `json:"assetIds,omitempty"`
}

// SensorConfig models only an independently traceable physical probe.
// Point remains the canonical data identity and Sensor is never required.
type SensorConfig struct {
	ID               string `json:"id"`
	DeviceID         string `json:"deviceId"`
	MountedSpaceID    string `json:"mountedSpaceId"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	SerialNumber     string `json:"serialNumber"`
	CalibrationDueAt string `json:"calibrationDueAt,omitempty"`
}

type PointConfig struct {
	DeviceID        string `json:"deviceId"`
	SensorID        string `json:"sensorId,omitempty"`
	SubjectType     string `json:"subjectType"`
	SubjectID       string `json:"subjectId,omitempty"`
	SourceKey       string `json:"sourceKey"`
	TelemetryKey    string `json:"telemetryKey"`
	PointCode       string `json:"pointCode"`
	Name            string `json:"name"`
	PointType       string `json:"pointType"`
	ValueType       string `json:"valueType"`
	Unit            string `json:"unit,omitempty"`
	Writable        bool   `json:"writable"`
	SampleInterval  string `json:"sampleInterval"`
	PublishInterval string `json:"publishInterval"`
	StaleAfter      string `json:"staleAfter"`
	SourceProtocol  string `json:"sourceProtocol,omitempty"`
	SourceAddress   string `json:"sourceAddress,omitempty"`
}

var pointCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

func validateAssetModel(config Config) error {
	spaces := make(map[string]SpaceConfig, len(config.Spaces))
	for _, space := range config.Spaces {
		space.ID = strings.TrimSpace(space.ID)
		if space.ID == "" || strings.TrimSpace(space.Name) == "" || !allowedSpaceType(space.Type) {
			return errors.New("space config is invalid")
		}
		if _, duplicate := spaces[space.ID]; duplicate {
			return fmt.Errorf("duplicate space id %s", space.ID)
		}
		spaces[space.ID] = space
	}
	if len(spaces) == 0 {
		return errors.New("at least one space is required")
	}
	for _, space := range spaces {
		if space.ParentID != "" {
			if _, ok := spaces[space.ParentID]; !ok {
				return fmt.Errorf("space %s references unknown parent %s", space.ID, space.ParentID)
			}
		}
		seen := map[string]struct{}{space.ID: {}}
		for parentID := space.ParentID; parentID != ""; parentID = spaces[parentID].ParentID {
			if _, duplicate := seen[parentID]; duplicate {
				return fmt.Errorf("space hierarchy cycle includes %s", parentID)
			}
			seen[parentID] = struct{}{}
		}
	}

	asset := make(map[string]AssetConfig, len(config.Assets))
	for _, item := range config.Assets {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Type) == "" {
			return errors.New("asset config is invalid")
		}
		if _, ok := spaces[item.SpaceID]; !ok {
			return fmt.Errorf("asset %s references unknown space %s", item.ID, item.SpaceID)
		}
		if _, duplicate := asset[item.ID]; duplicate {
			return fmt.Errorf("duplicate asset id %s", item.ID)
		}
		asset[item.ID] = item
	}
	if len(asset) == 0 {
		return errors.New("at least one asset is required")
	}

	devices := make(map[string]DeviceEndpointConfig, len(config.Devices))
	for _, device := range config.Devices {
		device.ID = strings.TrimSpace(device.ID)
		if device.ID == "" || strings.TrimSpace(device.Name) == "" || strings.TrimSpace(device.Type) == "" {
			return errors.New("device endpoint config is invalid")
		}
		if device.SpaceID != "" {
			if _, ok := spaces[device.SpaceID]; !ok {
				return fmt.Errorf("device %s references unknown space %s", device.ID, device.SpaceID)
			}
		}
		for _, assetID := range device.AssetIDs {
			if _, ok := asset[assetID]; !ok {
				return fmt.Errorf("device %s references unknown asset %s", device.ID, assetID)
			}
		}
		if _, duplicate := devices[device.ID]; duplicate {
			return fmt.Errorf("duplicate device id %s", device.ID)
		}
		devices[device.ID] = device
	}
	if len(devices) == 0 {
		return errors.New("at least one device endpoint is required")
	}
	for _, deviceID := range config.Plant.DeviceIDs() {
		if _, ok := devices[deviceID]; !ok {
			return fmt.Errorf("physical plant device %s is missing from devices", deviceID)
		}
	}

	sensors := make(map[string]SensorConfig, len(config.Sensors))
	for _, sensor := range config.Sensors {
		sensor.ID = strings.TrimSpace(sensor.ID)
		if sensor.ID == "" || strings.TrimSpace(sensor.Name) == "" || strings.TrimSpace(sensor.Type) == "" || strings.TrimSpace(sensor.SerialNumber) == "" {
			return errors.New("physical sensor config requires id, name, type, and serialNumber")
		}
		if _, ok := devices[sensor.DeviceID]; !ok {
			return fmt.Errorf("physical sensor %s references unknown reporting device %s", sensor.ID, sensor.DeviceID)
		}
		if _, ok := spaces[sensor.MountedSpaceID]; !ok {
			return fmt.Errorf("physical sensor %s references unknown mounted Space %s", sensor.ID, sensor.MountedSpaceID)
		}
		if strings.TrimSpace(sensor.CalibrationDueAt) != "" {
			if _, err := time.Parse(time.RFC3339, sensor.CalibrationDueAt); err != nil {
				return fmt.Errorf("physical sensor %s calibrationDueAt must be RFC3339", sensor.ID)
			}
		}
		if _, duplicate := sensors[sensor.ID]; duplicate {
			return fmt.Errorf("duplicate physical sensor id %s", sensor.ID)
		}
		sensors[sensor.ID] = sensor
	}

	pointKeys := make(map[string]PointConfig, len(config.Points))
	devicePointCount := make(map[string]int, len(devices))
	for index, point := range config.Points {
		if _, ok := devices[point.DeviceID]; !ok {
			return fmt.Errorf("point %d references unknown device %s", index, point.DeviceID)
		}
		if err := validateSubject(point.SubjectType, point.SubjectID, spaces, asset); err != nil {
			return fmt.Errorf("point %s: %w", point.TelemetryKey, err)
		}
		if point.SensorID != "" {
			sensor, ok := sensors[point.SensorID]
			if !ok {
				return fmt.Errorf("point %d references unknown physical sensor %s", index, point.SensorID)
			}
			if sensor.DeviceID != point.DeviceID {
				return fmt.Errorf("point %s must report through physical sensor device %s", point.TelemetryKey, sensor.DeviceID)
			}
		}
		if strings.TrimSpace(point.SourceKey) == "" || strings.TrimSpace(point.TelemetryKey) == "" || strings.TrimSpace(point.Name) == "" || !pointCodePattern.MatchString(point.PointCode) || !allowedPointType(point.PointType) || !allowedValueType(point.ValueType) {
			return fmt.Errorf("point %d config is invalid", index)
		}
		if point.PointType == "COMMAND" && !point.Writable {
			return fmt.Errorf("point %s COMMAND must be writable", point.TelemetryKey)
		}
		if point.PointType != "COMMAND" && point.PointType != "SETTING" && point.Writable {
			return fmt.Errorf("point %s only COMMAND or SETTING may be writable", point.TelemetryKey)
		}
		if (strings.TrimSpace(point.SourceProtocol) == "") != (strings.TrimSpace(point.SourceAddress) == "") {
			return fmt.Errorf("point %s source protocol and address must be supplied together", point.TelemetryKey)
		}
		sample, err := boundedDuration(point.SampleInterval, 100*time.Millisecond, 24*time.Hour, "sampleInterval")
		if err != nil {
			return fmt.Errorf("point %s: %w", point.TelemetryKey, err)
		}
		publish, err := boundedDuration(point.PublishInterval, 100*time.Millisecond, 24*time.Hour, "publishInterval")
		if err != nil {
			return fmt.Errorf("point %s: %w", point.TelemetryKey, err)
		}
		stale, err := boundedDuration(point.StaleAfter, 100*time.Millisecond, 7*24*time.Hour, "staleAfter")
		if err != nil {
			return fmt.Errorf("point %s: %w", point.TelemetryKey, err)
		}
		if publish < sample || stale < publish {
			return fmt.Errorf("point %s intervals are inconsistent", point.TelemetryKey)
		}
		identity := pointReference(point.DeviceID, point.TelemetryKey)
		if _, duplicate := pointKeys[identity]; duplicate {
			return fmt.Errorf("duplicate point key %s for %s", point.TelemetryKey, point.DeviceID)
		}
		pointKeys[identity] = point
		devicePointCount[point.DeviceID]++
	}
	if len(config.Points) == 0 {
		return errors.New("at least one telemetry point is required")
	}
	for _, deviceID := range config.Plant.DeviceIDs() {
		if devicePointCount[deviceID] == 0 {
			return fmt.Errorf("physical plant device %s has no telemetry points", deviceID)
		}
	}
	return nil
}

func validateSubject(subjectType, subjectID string, spaces map[string]SpaceConfig, asset map[string]AssetConfig) error {
	switch strings.TrimSpace(subjectType) {
	case "SITE":
		if strings.TrimSpace(subjectID) != "" {
			return errors.New("SITE measured subject must not declare subjectId")
		}
	case "SPACE":
		if _, ok := spaces[subjectID]; !ok {
			return fmt.Errorf("unknown Space measured subject %s", subjectID)
		}
	case "ASSET":
		if _, ok := asset[subjectID]; !ok {
			return fmt.Errorf("unknown Asset measured subject %s", subjectID)
		}
	default:
		return fmt.Errorf("unsupported measured subject type %s", subjectType)
	}
	return nil
}

func pointReference(deviceID, telemetryKey string) string {
	return strings.TrimSpace(deviceID) + "/" + strings.TrimSpace(telemetryKey)
}

func allowedSpaceType(value string) bool {
	switch strings.TrimSpace(value) {
	case "CAMPUS", "BUILDING", "FLOOR", "ZONE", "ROOM", "PLANT_ROOM", "ROOFTOP", "OUTDOOR", "TENANT_SPACE", "OTHER":
		return true
	default:
		return false
	}
}

func allowedPointType(value string) bool {
	switch strings.TrimSpace(value) {
	case "TELEMETRY", "COUNTER", "STATE", "SETTING", "COMMAND":
		return true
	default:
		return false
	}
}

func allowedValueType(value string) bool {
	switch strings.TrimSpace(value) {
	case "BOOLEAN", "NUMBER", "STRING", "JSON":
		return true
	default:
		return false
	}
}

func boundedDuration(value string, minimum, maximum time.Duration, field string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration < minimum || duration > maximum {
		return 0, fmt.Errorf("%s must be between %s and %s", field, minimum, maximum)
	}
	return duration, nil
}
