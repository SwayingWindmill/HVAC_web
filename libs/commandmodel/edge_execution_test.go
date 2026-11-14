package commandmodel

import "testing"

func TestExpectedVerificationValueUsesEdgeEffectiveNumericValue(t *testing.T) {
	requested := NumberScalar(50)
	effective := NumberScalar(45)
	edge := &EdgeExecutionEvidence{
		Requested: requested, Effective: &effective,
		WinnerControllerID: "capability-limits", Cycle: 12,
		Constraints: []EdgeConstraintEvidence{{ControllerID: "capability-limits", Reason: "DEVICE_CAPABILITY_LIMIT"}},
	}

	got, ok := ExpectedVerificationValue(CapabilitySetFrequency, CommandParameters{ParameterFrequencyHz: 50}, edge)
	if !ok || got.Number == nil || *got.Number != 45 {
		t.Fatalf("verification target = %#v, want Edge effective 45", got)
	}
}

func TestExpectedVerificationValueKeepsActionSemanticReadback(t *testing.T) {
	requested := BooleanScalar(true)
	edge := &EdgeExecutionEvidence{Requested: requested, Effective: &requested, WinnerControllerID: "cloud-command-intent", Cycle: 3}

	for _, testCase := range []struct {
		capability Capability
		want       string
	}{
		{capability: CapabilityStart, want: "RUNNING"},
		{capability: CapabilityStop, want: "STOPPED"},
		{capability: CapabilityStartUnitSequence, want: "RUNNING"},
		{capability: CapabilityStopUnitSequence, want: "STOPPED"},
	} {
		profile, ok := CapabilityProfileFor(testCase.capability)
		if !ok || profile.BaseRisk != RiskHigh {
			t.Fatalf("CapabilityProfileFor(%s) = (%#v, %v), want RiskHigh", testCase.capability, profile, ok)
		}
		got, ok := ExpectedVerificationValue(testCase.capability, nil, edge)
		if !ok || got.Text == nil || *got.Text != testCase.want {
			t.Fatalf("verification target(%s) = %#v, want %s", testCase.capability, got, testCase.want)
		}
	}
}
