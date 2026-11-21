package commandmodel

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseAndValidateCommandPointMetadata(t *testing.T) {
	profile, ok := CapabilityProfileFor(CapabilitySetTemperatureSetpoint)
	if !ok {
		t.Fatal("missing setpoint capability profile")
	}
	raw := map[string]any{
		"capability":         "SET_TEMPERATURE_SETPOINT",
		"capabilityRevision": profile.Revision,
		"feedbackPointKey":   "zone_temperature",
		"actuationMode":      " level ",
		"controlKind":        "NUMBER",
		"parameterKey":       "setpointC",
		"minimum":            json.Number("16"),
		"maximum":            30,
		"step":               0.5,
		"channel":            "ch-1",
		"coilAddress":        json.Number("4"),
		"pulseDurationMs":    500,
		"interlockChecks": []any{
			map[string]any{"deviceId": "dev-1", "key": "run_state", "expected": "RUNNING", "reason": "pump active"},
		},
	}
	meta, err := ParseCommandPointMetadata(raw)
	if err != nil {
		t.Fatalf("ParseCommandPointMetadata valid input: %v", err)
	}
	if meta.Capability != CapabilitySetTemperatureSetpoint || meta.ActuationMode != "LEVEL" || meta.CoilAddress == nil || *meta.CoilAddress != 4 || meta.PulseDurationMS != 500 || len(meta.InterlockChecks) != 1 {
		t.Fatalf("unexpected parsed metadata: %+v", meta)
	}
	if err := ValidateCommandPointMetadata(raw); err != nil {
		t.Fatalf("ValidateCommandPointMetadata failed on valid input: %v", err)
	}

	jsonMeta, err := ParseCommandPointMetadataJSON([]byte(`{"actuationMode":" emergency_do_release ","coilAddress":2,"pulseDurationMs":1000}`))
	if err != nil {
		t.Fatalf("ParseCommandPointMetadataJSON failed: %v", err)
	}
	if jsonMeta.ActuationMode != "EMERGENCY_DO_RELEASE" || jsonMeta.CoilAddress == nil || *jsonMeta.CoilAddress != 2 || jsonMeta.PulseDurationMS != 1000 {
		t.Fatalf("unexpected JSON parsed metadata: %+v", jsonMeta)
	}

	for _, bad := range []map[string]any{
		{"capability": 123},
		{"minimum": "not-a-number"},
		{"minimum": math.NaN()},
		{"minimum": 25.0, "maximum": 10.0},
		{"step": 0.0},
		{"coilAddress": -1},
		{"coilAddress": 1.5},
		{"controlKind": "UNKNOWN"},
		{"capability": "DOES_NOT_EXIST"},
		{"capability": "SET_TEMPERATURE_SETPOINT", "capabilityRevision": "wrong"},
		{"controlKind": "NUMBER", "parameterKey": "x", "minimum": 1.0, "maximum": 2.0},
		{"interlockChecks": "bad"},
	} {
		if err := ValidateCommandPointMetadata(bad); !errors.Is(err, ErrInvalidPointMetadata) {
			t.Fatalf("expected ErrInvalidPointMetadata for %v, got %v", bad, err)
		}
	}
}
