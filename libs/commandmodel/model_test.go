package commandmodel

import (
	"testing"
	"time"
)

func TestVerificationWindowForCapabilities(t *testing.T) {
	tests := []struct {
		capability Capability
		want       time.Duration
	}{
		{capability: CapabilityStart, want: 10 * time.Minute},
		{capability: CapabilityStop, want: 10 * time.Minute},
		{capability: CapabilityStartUnitSequence, want: 10 * time.Minute},
		{capability: CapabilityStopUnitSequence, want: 10 * time.Minute},
		{capability: CapabilityResetFault, want: 2 * time.Minute},
		{capability: CapabilitySetTemperatureSetpoint, want: 2 * time.Minute},
		{capability: CapabilitySetChilledWaterTemperatureSetpoint, want: 2 * time.Minute},
		{capability: CapabilitySetFrequency, want: 2 * time.Minute},
		{capability: CapabilitySetFanSpeed, want: 2 * time.Minute},
		{capability: CapabilitySetLoadLimit, want: 2 * time.Minute},
		{capability: CapabilitySetOpening, want: 2 * time.Minute},
		{capability: Capability("UNKNOWN_CAPABILITY"), want: 2 * time.Minute},
	}
	for _, tc := range tests {
		if got := VerificationWindowFor(tc.capability); got != tc.want {
			t.Fatalf("VerificationWindowFor(%q) = %s, want %s", tc.capability, got, tc.want)
		}
	}
}

func TestEdgeExecutionEvidenceValidPulseDualReadback(t *testing.T) {
	str := func(v string) *ScalarValue { return &ScalarValue{Text: &v} }
	num := func(v float64) *ScalarValue { return &ScalarValue{Number: &v} }
	requested := ScalarValue{Number: func() *float64 { v := 1.0; return &v }()}

	tests := []struct {
		name     string
		evidence EdgeExecutionEvidence
		want     bool
	}{
		{
			name: "valid pulse dual readback",
			evidence: EdgeExecutionEvidence{
				Requested:          requested,
				Effective:          str("u14:pulse(500ms)->0"),
				Applied:            str("u14:pulse(500ms)->0"),
				WinnerControllerID: "edge-local",
				Cycle:              1,
			},
			want: true,
		},
		{
			name: "level coil applied is false",
			evidence: EdgeExecutionEvidence{
				Requested:          requested,
				Effective:          str("u11:1"),
				Applied:            str("u11:1"),
				WinnerControllerID: "edge-local",
				Cycle:              1,
			},
			want: false,
		},
		{
			name: "pulse stuck high is false",
			evidence: EdgeExecutionEvidence{
				Requested:          requested,
				Effective:          str("u14:pulse(500ms)->1"),
				Applied:            str("u14:pulse(500ms)->1"),
				WinnerControllerID: "edge-local",
				Cycle:              1,
			},
			want: false,
		},
		{
			name: "request committed without effective is false",
			evidence: EdgeExecutionEvidence{
				Requested:          requested,
				Effective:          nil,
				Applied:            str("u14:pulse(500ms)->0"),
				WinnerControllerID: "edge-local",
				Cycle:              1,
			},
			want: false,
		},
		{
			name: "missing applied is false",
			evidence: EdgeExecutionEvidence{
				Requested:          requested,
				Effective:          str("u14:pulse(500ms)->0"),
				Applied:            nil,
				WinnerControllerID: "edge-local",
				Cycle:              1,
			},
			want: false,
		},
		{
			name: "numeric applied is false",
			evidence: EdgeExecutionEvidence{
				Requested:          requested,
				Effective:          str("u14:pulse(500ms)->0"),
				Applied:            num(0),
				WinnerControllerID: "edge-local",
				Cycle:              1,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		if got := tc.evidence.ValidPulseDualReadback(); got != tc.want {
			t.Fatalf("%s: ValidPulseDualReadback() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
