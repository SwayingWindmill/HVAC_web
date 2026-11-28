package gateway

import (
	"encoding/json"
	"testing"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

// A cooling load whose temperature difference is small next to the probes is still a measured
// value: the gateway must pass it on, labelled, instead of rejecting the whole snapshot.
func TestValidPresentStateAcceptsLowConfidenceAndProbeOffsetReasons(t *testing.T) {
	health := telemetryapi.TelemetryPointHealth{
		Status:  "SUSPECT",
		Score:   60,
		Reasons: []telemetryapi.QualityReasonCode{telemetryapi.QualityReasonCodeLowConfidence, telemetryapi.QualityReasonCodeProbeOffset},
	}
	value := telemetryapi.TelemetryPresentState{
		Key:            "BTU_COOLING",
		State:          "PRESENT",
		Value:          json.RawMessage(`512`),
		ValueType:      "NUMBER",
		Freshness:      "FRESH",
		Quality:        "PARTIAL",
		QualityReasons: []telemetryapi.QualityReasonCode{telemetryapi.QualityReasonCodeLowConfidence},
		PolicyRevision: 1,
		SampledAt:      "2026-11-05T00:00:00Z",
		ReceivedAt:     "2026-11-05T00:00:01Z",
		Health:         &health,
	}
	if !validPresentState(value) {
		t.Fatal("a LOW_CONFIDENCE reading with PROBE_OFFSET health was rejected")
	}
	value.QualityReasons = []telemetryapi.QualityReasonCode{telemetryapi.QualityReasonCodeLowConfidence, telemetryapi.QualityReasonCodeLowConfidence}
	if validPresentState(value) {
		t.Fatal("duplicate reasons must still be rejected")
	}
	value.QualityReasons = []telemetryapi.QualityReasonCode{"NOT_A_REASON"}
	if validPresentState(value) {
		t.Fatal("an unknown reason must still be rejected")
	}
}
