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
