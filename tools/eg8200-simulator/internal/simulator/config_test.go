package simulator

import (
	"strings"
	"testing"
)

func TestDecodeConfigRejectsMissingCredentials(t *testing.T) {
	config := testConfig()
	config.Credentials = map[string]string{}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "credentialEnvByDeviceId") {
		t.Fatalf("expected credential validation error, got %v", err)
	}
}

func TestDecodeConfigRejectsUnknownFields(t *testing.T) {
	raw := `{"schemaVersion":1,"unexpected":true}`
	_, err := DecodeConfig(strings.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestPlantConfigRejectsDuplicateDeviceIDs(t *testing.T) {
	config := testPlantConfig()
	config.BTUMeterID = config.PowerMeterID
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate device id") {
		t.Fatalf("expected duplicate device error, got %v", err)
	}
}

func TestConfigRequiresHTTPSForNonLocalThingsBoard(t *testing.T) {
	config := testConfig()
	config.ThingsBoardBaseURL = "http://thingsboard.example.com"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS validation error, got %v", err)
	}
}

func TestConfigRejectsAreaCycles(t *testing.T) {
	config := testConfig()
	config.Areas[0].ParentID = "plant-room"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected Area cycle validation error, got %v", err)
	}
}

func TestConfigRejectsSensorDeviceMismatch(t *testing.T) {
	config := testConfig()
	config.Points[0].DeviceID = "CHWP-01"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "must report through sensor device") {
		t.Fatalf("expected Sensor reporting Device validation error, got %v", err)
	}
}

func TestConfigRejectsDuplicatePointKeyWithinDevice(t *testing.T) {
	config := testConfig()
	duplicate := config.Points[0]
	duplicate.SourceKey = "enteringChilledWaterTemperatureC"
	config.Points = append(config.Points, duplicate)
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate point key") {
		t.Fatalf("expected duplicate point key validation error, got %v", err)
	}
}
