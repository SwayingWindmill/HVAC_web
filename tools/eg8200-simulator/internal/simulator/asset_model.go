package simulator

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type AreaConfig struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type"`
}

type EquipmentAssetConfig struct {
	ID     string `json:"id"`
	AreaID string `json:"areaId"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

type DeviceEndpointConfig struct {
	ID           string   `json:"id"`
	AreaID       string   `json:"areaId,omitempty"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	EquipmentIDs []string `json:"equipmentIds,omitempty"`
}

type SensorConfig struct {
	ID            string `json:"id"`
	DeviceID      string `json:"deviceId"`
	MountedAreaID string `json:"mountedAreaId"`
	SubjectType   string `json:"subjectType"`
	SubjectID     string `json:"subjectId,omitempty"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Mode          string `json:"mode"`
}

type PointConfig struct {
	DeviceID        string   `json:"deviceId"`
	SensorID        string   `json:"sensorId,omitempty"`
	SubjectType     string   `json:"subjectType"`
	SubjectID       string   `json:"subjectId,omitempty"`
	SourceKey       string   `json:"sourceKey"`
	TelemetryKey    string   `json:"telemetryKey"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	ValueType       string   `json:"valueType"`
	Unit            string   `json:"unit,omitempty"`
	Writable        bool     `json:"writable"`
	SampleInterval  string   `json:"sampleInterval"`
	PublishInterval string   `json:"publishInterval"`
	StaleAfter      string   `json:"staleAfter"`
	SourceProtocol  string   `json:"sourceProtocol,omitempty"`
	SourceAddress   string   `json:"sourceAddress,omitempty"`
	FormulaRevision string   `json:"formulaRevision,omitempty"`
	InputPointRefs  []string `json:"inputPointRefs,omitempty"`
}

func validateAssetModel(config Config) error {
	areas := make(map[string]AreaConfig, len(config.Areas))
	for _, area := range config.Areas {
		area.ID = strings.TrimSpace(area.ID)
		if area.ID == "" || strings.TrimSpace(area.Name) == "" || !allowedAreaType(area.Type) {
			return errors.New("area config is invalid")
		}
		if _, duplicate := areas[area.ID]; duplicate {
			return fmt.Errorf("duplicate area id %s", area.ID)
		}
		areas[area.ID] = area
	}
	if len(areas) == 0 {
		return errors.New("at least one area is required")
	}
	for _, area := range areas {
		if area.ParentID != "" {
			if _, ok := areas[area.ParentID]; !ok {
				return fmt.Errorf("area %s references unknown parent %s", area.ID, area.ParentID)
			}
		}
		seen := map[string]struct{}{area.ID: {}}
		for parentID := area.ParentID; parentID != ""; parentID = areas[parentID].ParentID {
			if _, duplicate := seen[parentID]; duplicate {
				return fmt.Errorf("area hierarchy cycle includes %s", parentID)
			}
			seen[parentID] = struct{}{}
		}
	}

	equipment := make(map[string]EquipmentAssetConfig, len(config.Equipment))
	for _, item := range config.Equipment {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Type) == "" {
			return errors.New("equipment config is invalid")
		}
		if _, ok := areas[item.AreaID]; !ok {
			return fmt.Errorf("equipment %s references unknown area %s", item.ID, item.AreaID)
		}
		if _, duplicate := equipment[item.ID]; duplicate {
			return fmt.Errorf("duplicate equipment id %s", item.ID)
		}
		equipment[item.ID] = item
	}
	if len(equipment) == 0 {
		return errors.New("at least one equipment asset is required")
	}

	devices := make(map[string]DeviceEndpointConfig, len(config.Devices))
	for _, device := range config.Devices {
		device.ID = strings.TrimSpace(device.ID)
		if device.ID == "" || strings.TrimSpace(device.Name) == "" || strings.TrimSpace(device.Type) == "" {
			return errors.New("device endpoint config is invalid")
		}
		if device.AreaID != "" {
			if _, ok := areas[device.AreaID]; !ok {
				return fmt.Errorf("device %s references unknown area %s", device.ID, device.AreaID)
			}
		}
		for _, equipmentID := range device.EquipmentIDs {
			if _, ok := equipment[equipmentID]; !ok {
				return fmt.Errorf("device %s references unknown equipment %s", device.ID, equipmentID)
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
		if sensor.ID == "" || strings.TrimSpace(sensor.Name) == "" || strings.TrimSpace(sensor.Type) == "" || !allowedSensorMode(sensor.Mode) {
			return errors.New("sensor config is invalid")
		}
		if _, ok := devices[sensor.DeviceID]; !ok {
			return fmt.Errorf("sensor %s references unknown device %s", sensor.ID, sensor.DeviceID)
		}
		if _, ok := areas[sensor.MountedAreaID]; !ok {
			return fmt.Errorf("sensor %s references unknown mounted Area %s", sensor.ID, sensor.MountedAreaID)
		}
		if err := validateSubject(sensor.SubjectType, sensor.SubjectID, areas, equipment); err != nil {
			return fmt.Errorf("sensor %s: %w", sensor.ID, err)
		}
		if _, duplicate := sensors[sensor.ID]; duplicate {
			return fmt.Errorf("duplicate sensor id %s", sensor.ID)
		}
		sensors[sensor.ID] = sensor
	}

	pointKeys := make(map[string]PointConfig, len(config.Points))
	devicePointCount := make(map[string]int, len(devices))
	for index, point := range config.Points {
		if _, ok := devices[point.DeviceID]; !ok {
			return fmt.Errorf("point %d references unknown device %s", index, point.DeviceID)
		}
		if err := validateSubject(point.SubjectType, point.SubjectID, areas, equipment); err != nil {
			return fmt.Errorf("point %s: %w", point.TelemetryKey, err)
		}
		if point.SensorID != "" {
			sensor, ok := sensors[point.SensorID]
			if !ok {
				return fmt.Errorf("point %d references unknown sensor %s", index, point.SensorID)
			}
			if sensor.DeviceID != point.DeviceID {
				return fmt.Errorf("point %s must report through sensor device %s", point.TelemetryKey, sensor.DeviceID)
			}
			if sensor.SubjectType != point.SubjectType || sensor.SubjectID != point.SubjectID {
				return fmt.Errorf("point %s measured subject differs from sensor %s", point.TelemetryKey, sensor.ID)
			}
		}
		if strings.TrimSpace(point.SourceKey) == "" || strings.TrimSpace(point.TelemetryKey) == "" || strings.TrimSpace(point.Name) == "" || !allowedPointKind(point.Kind) || !allowedValueType(point.ValueType) {
			return fmt.Errorf("point %d config is invalid", index)
		}
		if point.Writable != (point.Kind == "COMMAND") {
			return fmt.Errorf("point %s writable flag must match COMMAND kind", point.TelemetryKey)
		}
		if (strings.TrimSpace(point.SourceProtocol) == "") != (strings.TrimSpace(point.SourceAddress) == "") {
			return fmt.Errorf("point %s source protocol and address must be supplied together", point.TelemetryKey)
		}
		if point.Kind == "CALCULATED" {
			if strings.TrimSpace(point.FormulaRevision) == "" || len(point.InputPointRefs) == 0 {
				return fmt.Errorf("calculated point %s requires formulaRevision and inputPointRefs", point.TelemetryKey)
			}
		} else if point.FormulaRevision != "" || len(point.InputPointRefs) != 0 {
			return fmt.Errorf("non-calculated point %s cannot declare formula inputs", point.TelemetryKey)
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
	for _, point := range config.Points {
		for _, inputRef := range point.InputPointRefs {
			if inputRef == pointReference(point.DeviceID, point.TelemetryKey) {
				return fmt.Errorf("calculated point %s cannot reference itself", point.TelemetryKey)
			}
			if _, ok := pointKeys[inputRef]; !ok {
				return fmt.Errorf("calculated point %s references unknown input %s", point.TelemetryKey, inputRef)
			}
		}
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

func validateSubject(
	subjectType string,
	subjectID string,
	areas map[string]AreaConfig,
	equipment map[string]EquipmentAssetConfig,
) error {
	switch strings.TrimSpace(subjectType) {
	case "SITE":
		if strings.TrimSpace(subjectID) != "" {
			return errors.New("SITE measured subject must not declare subjectId")
		}
	case "AREA":
		if _, ok := areas[subjectID]; !ok {
			return fmt.Errorf("unknown Area measured subject %s", subjectID)
		}
	case "EQUIPMENT":
		if _, ok := equipment[subjectID]; !ok {
			return fmt.Errorf("unknown Equipment measured subject %s", subjectID)
		}
	default:
		return fmt.Errorf("unsupported measured subject type %s", subjectType)
	}
	return nil
}

func pointReference(deviceID, telemetryKey string) string {
	return strings.TrimSpace(deviceID) + "/" + strings.TrimSpace(telemetryKey)
}

func allowedAreaType(value string) bool {
	switch strings.TrimSpace(value) {
	case "CAMPUS", "BUILDING", "FLOOR", "ZONE", "ROOM", "PLANT_ROOM", "ROOFTOP", "OUTDOOR", "TENANT_SPACE", "OTHER":
		return true
	default:
		return false
	}
}

func allowedSensorMode(value string) bool {
	switch strings.TrimSpace(value) {
	case "EMBEDDED", "WIRED", "INDEPENDENT_DEVICE":
		return true
	default:
		return false
	}
}

func allowedPointKind(value string) bool {
	switch strings.TrimSpace(value) {
	case "MEASURED", "CALCULATED", "STATE", "COMMAND", "FEEDBACK":
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
