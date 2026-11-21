package edgecontrol

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParseEdgePointSourceMetadata(t *testing.T) {
	meta, err := ParseEdgePointSourceMetadata(map[string]any{
		"capability":       " SET_FREQUENCY ",
		"controlKind":      "NUMBER",
		"feedbackPointKey": "frequency_hz",
		"parameterKey":     "frequencyHz",
		"minimum":          json.Number("20"),
		"maximum":          50,
		"step":             0.5,
		"deadband":         1.25,
	})
	if err != nil {
		t.Fatalf("ParseEdgePointSourceMetadata failed: %v", err)
	}
	if meta.Capability != "SET_FREQUENCY" || meta.ControlKind != "NUMBER" || meta.ParameterKey != "frequencyHz" ||
		meta.Minimum == nil || *meta.Minimum != 20 || meta.Maximum == nil || *meta.Maximum != 50 ||
		meta.Step == nil || *meta.Step != 0.5 || meta.Deadband == nil || *meta.Deadband != 1.25 {
		t.Fatalf("unexpected parsed edge metadata: %+v", meta)
	}

	for _, bad := range []map[string]any{
		{"capability": 42},
		{"minimum": "twenty"},
		{"minimum": math.Inf(1)},
		{"minimum": 30, "maximum": 10},
		{"step": 0},
		{"deadband": "bad"},
	} {
		if _, err := ParseEdgePointSourceMetadata(bad); err == nil {
			t.Fatalf("expected error for invalid metadata %v", bad)
		}
	}
}
