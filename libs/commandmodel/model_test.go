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

func TestEdgeExecutionEvidenceEmergencyDoAndUnitSequenceReadback(t *testing.T) {
	str := func(v string) *ScalarValue { return &ScalarValue{Text: &v} }
	num := func(v float64) *ScalarValue { return &ScalarValue{Number: &v} }
	boolean := func(v bool) *ScalarValue { return &ScalarValue{Boolean: &v} }
	requested := ScalarValue{Number: func() *float64 { v := 0.0; return &v }()}

	t.Run("ValidEmergencyDoReleaseReadback", func(t *testing.T) {
		tests := []struct {
			name     string
			evidence EdgeExecutionEvidence
			want     bool
		}{
			{
				name: "all-do:0 executed is true",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("all-do:0"),
					Applied:            str("all-do:0"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: true,
			},
			{
				name: "pulse dual readback is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("u14:pulse(500ms)->0"),
					Applied:            str("u14:pulse(500ms)->0"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "all-do:1 is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("all-do:1"),
					Applied:            str("all-do:1"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "missing effective is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          nil,
					Applied:            str("all-do:0"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "nil applied is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("all-do:0"),
					Applied:            nil,
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "numeric or boolean mixed applied is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("all-do:0"),
					Applied:            &ScalarValue{Text: str("all-do:0").Text, Boolean: boolean(false).Boolean},
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "numeric applied is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("all-do:0"),
					Applied:            num(0),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
		}
		for _, tc := range tests {
			if got := tc.evidence.ValidEmergencyDoReleaseReadback(); got != tc.want {
				t.Fatalf("%s: ValidEmergencyDoReleaseReadback() = %v, want %v", tc.name, got, tc.want)
			}
		}
	})

	t.Run("ValidUnitSequenceAcceptance", func(t *testing.T) {
		tests := []struct {
			name     string
			evidence EdgeExecutionEvidence
			want     bool
		}{
			{
				name: "sequence-accepted executed is true",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("chiller-1:sequence-accepted"),
					Applied:            str("chiller-1:sequence-accepted"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: true,
			},
			{
				name: "sequence-failed is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("chiller-1:sequence-failed"),
					Applied:            str("chiller-1:sequence-failed"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "all-do:0 is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("all-do:0"),
					Applied:            str("all-do:0"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "missing effective is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          nil,
					Applied:            str("chiller-1:sequence-accepted"),
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "nil applied is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("chiller-1:sequence-accepted"),
					Applied:            nil,
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
			{
				name: "mixed number and text applied is false",
				evidence: EdgeExecutionEvidence{
					Requested:          requested,
					Effective:          str("chiller-1:sequence-accepted"),
					Applied:            &ScalarValue{Text: str("chiller-1:sequence-accepted").Text, Number: num(1).Number},
					WinnerControllerID: "edge-local",
					Cycle:              1,
				},
				want: false,
			},
		}
		for _, tc := range tests {
			if got := tc.evidence.ValidUnitSequenceAcceptance(); got != tc.want {
				t.Fatalf("%s: ValidUnitSequenceAcceptance() = %v, want %v", tc.name, got, tc.want)
			}
		}
	})
}

func TestTerminalPreSendRejection(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{code: "DISPATCH_SAFETY_EVALUATION_FAILED", want: false},
		{code: " DISPATCH_SAFETY_EVALUATION_FAILED ", want: false},
		{code: "MQTT_ROUTE_UNAVAILABLE", want: false},
		{code: "", want: false},
		{code: "COMMAND_ROUTE_NOT_FOUND", want: true},
		{code: "COMMAND_CONTROL_DISABLED", want: true},
		{code: "COMMAND_GATEWAY_CREDENTIAL_INACTIVE", want: true},
		{code: "DISPATCH_SAFETY_STATE_UNAVAILABLE", want: true},
		{code: "DISPATCH_SAFETY_STATE_NOT_FRESH", want: true},
		{code: "DISPATCH_SAFETY_DEVICE_NOT_ONLINE", want: true},
		{code: "DISPATCH_SAFETY_INTERLOCK_STATE_UNAVAILABLE", want: true},
		{code: "DISPATCH_SAFETY_INTERLOCK_VIOLATED", want: true},
		{code: "DISPATCH_SAFETY_UNAVAILABLE", want: true},
	}
	for _, tc := range tests {
		if got := TerminalPreSendRejection(tc.code); got != tc.want {
			t.Fatalf("TerminalPreSendRejection(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}
