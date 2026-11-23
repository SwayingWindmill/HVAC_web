package edgedaemon

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseConfigDefaultsAndValidation(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{
		"gatewayId": "0193f000-2000-7000-8000-000000000001",
		"stateDir": "/opt/hvac-edge-gateway/state",
		"platformBaseUrl": "https://hvac.example.com",
		"healthListenAddr": "127.0.0.1:18890",
		"tunnel": {
			"enabled": true,
			"allowedTargetPorts": [22, 1880]
		},
		"ota": {
			"enabled": true,
			"trustedPublicKeys": {
				"release-2026": "` + base64.StdEncoding.EncodeToString(pub) + `"
			}
		},
		"serialBuses": {
			"/dev/ttyAS3": {"speed": 9600, "dataBits": 8, "parity": "N", "stopBits": 1}
		}
	}`)
	cfg, err := ParseConfig(raw, "1.2.3")
	if err != nil {
		t.Fatalf("unexpected ParseConfig error: %v", err)
	}
	if cfg.HardwareModel != "EG8200" || cfg.RuntimeVersion != "1.2.3" || cfg.CycleIntervalMS != 1000 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.OTA.Dir != "/opt/hvac-edge-gateway/state/ota" {
		t.Fatalf("OTA.Dir = %q, want stateDir/ota", cfg.OTA.Dir)
	}

	// Reject non-loopback health listener
	badLoopback := strings.Replace(string(raw), "127.0.0.1:18890", "0.0.0.0:18890", 1)
	if _, err := ParseConfig([]byte(badLoopback), "1.2.3"); err == nil {
		t.Fatal("expected non-loopback healthListenAddr to be rejected")
	}

	// Reject unsupported tunnel target port
	badPort := strings.Replace(string(raw), "[22, 1880]", "[22, 80]", 1)
	if _, err := ParseConfig([]byte(badPort), "1.2.3"); err == nil {
		t.Fatal("expected unsupported tunnel port 80 to be rejected")
	}

	// Reject unknown fields
	if _, err := ParseConfig([]byte(`{"gatewayId":"g1","stateDir":"/tmp/s","unknownField":true}`), "1.0.0"); err == nil {
		t.Fatal("expected unknown JSON field to be rejected")
	}
}

func TestParseConfigSeparateMQTTTLSFields(t *testing.T) {
	raw := []byte(`{
		"gatewayId": "0193f000-2000-7000-8000-000000000001",
		"stateDir": "/opt/hvac-edge-gateway/state",
		"platformBaseUrl": "https://hvac.example.com",
		"mqttBrokerUrl": "tls://mqtt.example.com:8884",
		"serverName": "hvac.example.com",
		"caFile": "/etc/ssl/certs/ca-certificates.crt",
		"mqttServerName": "mqtt-broker",
		"mqttCaFile": "/root/hvac-platform/broker-ca.crt",
		"certFile": "/root/hvac-platform/gateway.crt",
		"keyFile": "/root/hvac-platform/gateway.key"
	}`)
	cfg, err := ParseConfig(raw, "0.1.0")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.MQTTServerName != "mqtt-broker" || cfg.MQTTCAFile != "/root/hvac-platform/broker-ca.crt" {
		t.Fatalf("unexpected MQTT TLS fields: %+v", cfg)
	}
}

func TestParseConfigSongyaxuanPlantDefaultsAndValidation(t *testing.T) {
	raw := []byte(`{
		"gatewayId": "01a14000-4000-7000-8000-000000000100",
		"stateDir": "/opt/hvac-edge-gateway/state",
		"songyaxuanPlant": {
			"enabled": true,
			"serialPort": "/dev/ttyAS3"
		}
	}`)
	cfg, err := ParseConfig(raw, "0.1.3")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.SongyaxuanPlant == nil || cfg.SongyaxuanPlant.StopPowerKW() != 5.0 || cfg.SongyaxuanPlant.ConcentratorUnitID != 20 || cfg.SongyaxuanPlant.ET1010UnitID != 31 {
		t.Fatalf("unexpected songyaxuanPlant defaults: %+v", cfg.SongyaxuanPlant)
	}
	if bus := cfg.SerialBuses["/dev/ttyAS3"]; bus.Speed != 9600 || bus.TurnaroundDelayMS != 45 {
		t.Fatalf("expected /dev/ttyAS3 default bus config, got %+v", bus)
	}
	if _, err := ParseConfig([]byte(`{"gatewayId":"g1","stateDir":"/tmp/s","songyaxuanPlant":{"enabled":true}}`), "0.1.3"); err == nil {
		t.Fatal("expected missing songyaxuanPlant.serialPort to fail")
	}
}
