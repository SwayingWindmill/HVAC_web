package adapter

import (
	"os"
	"strings"
	"testing"
)

func validConfig() Config {
	unit := "kW"
	return Config{
		SchemaVersion:         ConfigSchemaVersion,
		IntegrationInstanceID: "018f3e00-0000-7000-8000-000000000101",
		PollInterval:          "5s",
		InitialLookback:       "1m",
		PageLimit:             100,
		CheckpointFile:        "out/checkpoints.json",
		ThingsBoard: ThingsBoardConfig{
			BaseURL: "http://127.0.0.1:18080",
			JWTFile: "out/provider-authorization",
		},
		TelemetryRuntime: TelemetryRuntimeConfig{
			BaseURL:    "https://telemetry-runtime.local:18446",
			CAFile:     "ca.pem",
			CertFile:   "client.pem",
			KeyFile:    "client.key",
			ServerName: "telemetry-runtime.local",
		},
		Devices: []DeviceMapping{{
			ThingsBoardDeviceID: "tb-device-1",
			ExternalID:          "tb-device-1",
			Points: []PointMapping{{
				SourceKey:    "powerKw",
				TelemetryKey: "hvac.power",
				ValueType:    "NUMBER",
				Unit:         &unit,
			}},
		}},
	}
}

func TestConfigAcceptsLocalThingsBoardAndRequiresHTTPSForS2(t *testing.T) {
	config := validConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	config.TelemetryRuntime.BaseURL = "http://telemetry-runtime.local:18446"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected S2 HTTPS error, got %v", err)
	}
}

func TestConfigAcceptsDeepHistoricalLookbackWithinBound(t *testing.T) {
	config := validConfig()
	config.InitialLookback = "240h"
	if err := config.Validate(); err != nil {
		t.Fatalf("deep historical lookback rejected: %v", err)
	}
	config.InitialLookback = "241h"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "240h") {
		t.Fatalf("expected deep historical lookback bound error, got %v", err)
	}
}

func TestDecodeConfigRejectsUnknownFields(t *testing.T) {
	_, err := DecodeConfig(strings.NewReader(`{"schemaVersion":1,"unexpected":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestConfigRejectsDuplicateThingsBoardDevices(t *testing.T) {
	config := validConfig()
	config.Devices = append(config.Devices, config.Devices[0])
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate thingsBoardDeviceId") {
		t.Fatalf("expected duplicate device error, got %v", err)
	}
}

func TestConfigRejectsPointKeysWithSurroundingWhitespace(t *testing.T) {
	config := validConfig()
	config.Devices[0].Points[0].SourceKey = " powerKw "
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("expected canonical point validation error, got %v", err)
	}
}

func TestCentralPlantExampleConfigIsValid(t *testing.T) {
	file, err := os.Open("../../configs/central-plant.local.example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, err := DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Devices) != 7 {
		t.Fatalf("expected seven central-plant devices, got %d", len(config.Devices))
	}
}
