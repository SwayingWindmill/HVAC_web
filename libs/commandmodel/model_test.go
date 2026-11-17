package commandmodel

import (
	"errors"
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
		{code: "DISPATCH_SAFETY_CONTROL_LOCKED", want: true},
		{code: "DISPATCH_SAFETY_MAINTENANCE_WINDOW", want: true},
		{code: "DISPATCH_SAFETY_UNAVAILABLE", want: true},
	}
	for _, tc := range tests {
		if got := TerminalPreSendRejection(tc.code); got != tc.want {
			t.Fatalf("TerminalPreSendRejection(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestNormalizeTraceID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "lowercase valid trace id",
			raw:  "4bf92f3577b34da6a3ce929d0e0e4736",
			want: "4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			name: "uppercase and padded whitespace normalized",
			raw:  "  4BF92F3577B34DA6A3CE929D0E0E4736\n",
			want: "4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			name: "all zero trace id rejected",
			raw:  "00000000000000000000000000000000",
			want: "",
		},
		{
			name: "short trace id rejected",
			raw:  "4bf92f3577b34da6a3ce929d0e0e473",
			want: "",
		},
		{
			name: "long trace id rejected",
			raw:  "4bf92f3577b34da6a3ce929d0e0e47360",
			want: "",
		},
		{
			name: "non hex characters rejected",
			raw:  "4bf92f35-77b3-4da6-a3ce-929d0e0e",
			want: "",
		},
		{
			name: "empty rejected",
			raw:  "   ",
			want: "",
		},
	}
	for _, tc := range tests {
		if got := NormalizeTraceID(tc.raw); got != tc.want {
			t.Fatalf("%s: NormalizeTraceID(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

func TestPriorityHierarchyAndDerivation(t *testing.T) {
	if PriorityRank(PrioritySafetyInterlock) <= PriorityRank(PriorityManualOverride) ||
		PriorityRank(PriorityManualOverride) <= PriorityRank(PriorityProtectionRule) ||
		PriorityRank(PriorityProtectionRule) <= PriorityRank(PriorityAIOptimization) ||
		PriorityRank(PriorityAIOptimization) <= PriorityRank(PrioritySchedule) ||
		PriorityRank(PrioritySchedule) <= PriorityRank("UNKNOWN") {
		t.Fatalf("unexpected priority rank ordering: %d %d %d %d %d",
			PriorityRank(PrioritySafetyInterlock),
			PriorityRank(PriorityManualOverride),
			PriorityRank(PriorityProtectionRule),
			PriorityRank(PriorityAIOptimization),
			PriorityRank(PrioritySchedule),
		)
	}
	if PriorityRank(PrioritySafetyInterlock) != 500 ||
		PriorityRank(PriorityManualOverride) != 400 ||
		PriorityRank(PriorityProtectionRule) != 300 ||
		PriorityRank(PriorityAIOptimization) != 200 ||
		PriorityRank(PrioritySchedule) != 100 {
		t.Fatalf("unexpected exact priority ranks")
	}

	if !PrioritySafetyInterlock.Valid() || !PrioritySchedule.Valid() || PriorityTier("INVALID").Valid() {
		t.Fatalf("unexpected PriorityTier.Valid results")
	}
	if !SourceSafetyInterlock.Valid() || !SourceOperator.Valid() || !SourceSchedule.Valid() || CommandSource("INVALID").Valid() {
		t.Fatalf("unexpected CommandSource.Valid results")
	}

	cases := []struct {
		name       string
		req        SubmitRequest
		wantTier   PriorityTier
		wantSource CommandSource
		wantErr    bool
	}{
		{
			name:       "default human operator",
			req:        SubmitRequest{Capability: CapabilitySetTemperatureSetpoint},
			wantTier:   PriorityManualOverride,
			wantSource: SourceOperator,
		},
		{
			name: "emergency bypass defaults to safety interlock",
			req: SubmitRequest{
				Capability:       CapabilityStop,
				PreconditionMode: PreconditionEmergencyBypass,
			},
			wantTier:   PrioritySafetyInterlock,
			wantSource: SourceSafetyInterlock,
		},
		{
			name: "automated lease defaults to ai optimization",
			req: SubmitRequest{
				Capability: CapabilitySetTemperatureSetpoint,
				Authorization: AuthorizationSnapshot{
					WorkloadSPIFFE: "spiffe://hvac/ns/prod/sa/optimizer",
					LeaseID:        "lease-1",
				},
			},
			wantTier:   PriorityAIOptimization,
			wantSource: SourceOptimization,
		},
		{
			name: "automated lease can run schedule or protection rule",
			req: SubmitRequest{
				Capability:    CapabilitySetTemperatureSetpoint,
				PriorityTier:  PriorityProtectionRule,
				CommandSource: SourceProtectionRule,
				Authorization: AuthorizationSnapshot{
					WorkloadSPIFFE: "spiffe://hvac/ns/prod/sa/optimizer",
					LeaseID:        "lease-1",
				},
			},
			wantTier:   PriorityProtectionRule,
			wantSource: SourceProtectionRule,
		},
		{
			name: "automated lease can run emergency stop bypass when supported",
			req: SubmitRequest{
				Capability:       CapabilityStop,
				PreconditionMode: PreconditionEmergencyBypass,
				Authorization: AuthorizationSnapshot{
					WorkloadSPIFFE: "spiffe://hvac/ns/prod/sa/optimizer",
					LeaseID:        "lease-1",
				},
			},
			wantTier:   PrioritySafetyInterlock,
			wantSource: SourceSafetyInterlock,
		},
		{
			name: "automated lease cannot elevate to manual override",
			req: SubmitRequest{
				Capability:    CapabilitySetTemperatureSetpoint,
				PriorityTier:  PriorityManualOverride,
				CommandSource: SourceOperator,
				Authorization: AuthorizationSnapshot{
					WorkloadSPIFFE: "spiffe://hvac/ns/prod/sa/optimizer",
					LeaseID:        "lease-1",
				},
			},
			wantTier:   PriorityAIOptimization,
			wantSource: SourceOptimization,
			wantErr:    true,
		},
		{
			name: "automated lease cannot elevate to safety interlock without emergency bypass capability",
			req: SubmitRequest{
				Capability:   CapabilitySetTemperatureSetpoint,
				PriorityTier: PrioritySafetyInterlock,
				Authorization: AuthorizationSnapshot{
					WorkloadSPIFFE: "spiffe://hvac/ns/prod/sa/optimizer",
					LeaseID:        "lease-1",
				},
			},
			wantTier:   PriorityAIOptimization,
			wantSource: SourceOptimization,
			wantErr:    true,
		},
		{
			name: "automated lease cannot elevate stop to safety interlock without emergency bypass precondition",
			req: SubmitRequest{
				Capability:    CapabilityStop,
				PriorityTier:  PrioritySafetyInterlock,
				CommandSource: SourceSafetyInterlock,
				Authorization: AuthorizationSnapshot{
					WorkloadSPIFFE: "spiffe://hvac/ns/prod/sa/optimizer",
					LeaseID:        "lease-1",
				},
			},
			wantTier:   PriorityAIOptimization,
			wantSource: SourceOptimization,
			wantErr:    true,
		},
		{
			name: "mismatched priority tier and source rejected",
			req: SubmitRequest{
				Capability:    CapabilitySetTemperatureSetpoint,
				PriorityTier:  PriorityManualOverride,
				CommandSource: SourceOptimization,
			},
			wantTier:   PriorityManualOverride,
			wantSource: SourceOperator,
			wantErr:    true,
		},
		{
			name: "invalid priority tier rejected",
			req: SubmitRequest{
				Capability:   CapabilitySetTemperatureSetpoint,
				PriorityTier: PriorityTier("BOGUS"),
			},
			wantTier:   PriorityManualOverride,
			wantSource: SourceOperator,
			wantErr:    true,
		},
	}

	for _, tc := range cases {
		tier, source, err := ResolveCommandPriority(tc.req)
		if tc.wantErr {
			if !errors.Is(err, ErrInvalidCommandPriority) {
				t.Fatalf("%s: expected ErrInvalidCommandPriority, got %v", tc.name, err)
			}
		} else {
			if err != nil {
				t.Fatalf("%s: ResolveCommandPriority failed: %v", tc.name, err)
			}
			if tier != tc.wantTier || source != tc.wantSource {
				t.Fatalf("%s: got (%s, %s), want (%s, %s)", tc.name, tier, source, tc.wantTier, tc.wantSource)
			}
		}
		derivedTier, derivedSource := DeriveCommandPriority(tc.req)
		if derivedTier != tc.wantTier || derivedSource != tc.wantSource {
			t.Fatalf("%s: DeriveCommandPriority got (%s, %s), want (%s, %s)", tc.name, derivedTier, derivedSource, tc.wantTier, tc.wantSource)
		}
	}

	if !TerminalPreSendRejection("DEVICE_PRIORITY_LOCKED") {
		t.Fatalf("expected DEVICE_PRIORITY_LOCKED to be terminal pre-send rejection")
	}
	if !TerminalPreSendRejection("PRIORITY_PREEMPTED") {
		t.Fatalf("expected PRIORITY_PREEMPTED to be terminal pre-send rejection")
	}
}
