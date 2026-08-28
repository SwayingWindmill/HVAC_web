package simulator

import (
	"strings"
	"testing"
)

func TestDecodeConfigRejectsUnknownFields(t *testing.T) {
	raw := `{"schemaVersion":2,"unexpected":true}`
	_, err := DecodeConfig(strings.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestConfigRejectsPreviousSchemaAfterScenarioMigration(t *testing.T) {
	config := testConfig()
	config.SchemaVersion = 2
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported simulator config schemaVersion 2") {
		t.Fatalf("expected previous simulator config schema rejection, got %v", err)
	}
}

func TestPlantConfigRejectsDuplicateDeviceIDs(t *testing.T) {
	config := testPlantConfig()
	config.BTUMeterID = config.PowerMeterID
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate device id") {
		t.Fatalf("expected duplicate device error, got %v", err)
	}
}

func TestConfigRejectsSpaceCycles(t *testing.T) {
	config := testConfig()
	config.Spaces[0].ParentID = "plant-room"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected Space cycle validation error, got %v", err)
	}
}

func TestConfigRequiresPhysicalSensorIdentity(t *testing.T) {
	config := testConfig()
	config.Sensors[0].SerialNumber = ""
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "serialNumber") {
		t.Fatalf("expected physical Sensor identity validation error, got %v", err)
	}
}

func TestConfigRejectsSensorDeviceMismatch(t *testing.T) {
	config := testConfig()
	config.Points[0].DeviceID = "CHWP-01"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "physical sensor device") {
		t.Fatalf("expected Sensor reporting Device validation error, got %v", err)
	}
}

func TestConfigRejectsNonCanonicalPointCode(t *testing.T) {
	config := testConfig()
	config.Points[0].PointCode = "Leaving.Temp"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "point 0 config is invalid") {
		t.Fatalf("expected lower_snake_case Point Code validation error, got %v", err)
	}
}

func TestConfigRejectsDuplicatePointKeyWithinDevice(t *testing.T) {
	config := testConfig()
	duplicate := config.Points[0]
	duplicate.PointID = "01910000-0000-7000-8000-00000000ffff"
	duplicate.SourceKey = "enteringChilledWaterTemperatureC"
	config.Points = append(config.Points, duplicate)
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate point key") {
		t.Fatalf("expected duplicate point key validation error, got %v", err)
	}
}

func TestConfigRejectsDuplicateCanonicalPointID(t *testing.T) {
	config := testConfig()
	duplicate := config.Points[0]
	duplicate.TelemetryKey = "chiller.duplicate_point"
	duplicate.SourceKey = "powerKw"
	duplicate.PointCode = "duplicate_point"
	config.Points = append(config.Points, duplicate)
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate canonical pointId") {
		t.Fatalf("expected duplicate canonical Point identity error, got %v", err)
	}
}
