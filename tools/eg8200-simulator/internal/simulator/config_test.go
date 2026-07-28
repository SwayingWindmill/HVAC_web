package simulator

import (
	"strings"
	"testing"
)

func TestDecodeConfigRejectsMissingCredentials(t *testing.T) {
	raw := `{
  "schemaVersion": 1,
  "gatewayId": "EG8200-VIRTUAL-001",
  "thingsBoardBaseUrl": "http://localhost:8080",
  "publishInterval": "5s",
  "plant": {
    "ambientDryBulbC": 34,
    "ambientWetBulbC": 27,
    "loadFraction": 0.7,
    "chiller": {"id":"CHILLER-01","ratedCoolingCapacityKw":1200,"baseCop":5.6,"initialSetpointC":7,"initialLoadLimitPct":100,"initiallyRunning":true},
    "chilledWaterPump": {"id":"CHWP-01","ratedPowerKw":45,"ratedFlowM3h":220,"initialFrequencyHz":50,"initiallyRunning":true},
    "coolingWaterPump": {"id":"CWP-01","ratedPowerKw":37,"ratedFlowM3h":260,"initialFrequencyHz":50,"initiallyRunning":true},
    "coolingTower": {"id":"CT-01","ratedFanPowerKw":18.5,"initialFanSpeedPct":80,"initiallyRunning":true},
    "powerMeterId":"METER-HVAC-TOTAL",
    "btuMeterId":"BTU-METER-01"
  },
  "credentialEnvByDeviceId": {}
}`
	_, err := DecodeConfig(strings.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), "credentialEnvByDeviceId") {
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
	plant := testPlantConfig()
	credentials := make(map[string]string, len(plant.DeviceIDs()))
	for _, deviceID := range plant.DeviceIDs() {
		credentials[deviceID] = "TEST_TOKEN_ENV"
	}
	config := Config{
		SchemaVersion:      ConfigSchemaVersion,
		GatewayID:          "EG8200-VIRTUAL-001",
		ThingsBoardBaseURL: "http://thingsboard.example.com",
		PublishInterval:    "5s",
		Plant:              plant,
		Credentials:        credentials,
	}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS validation error, got %v", err)
	}
}
