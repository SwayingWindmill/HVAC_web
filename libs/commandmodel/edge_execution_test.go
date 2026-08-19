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

	got, ok := ExpectedVerificationValue(CapabilityStart, nil, edge)
	if !ok || got.Text == nil || *got.Text != "RUNNING" {
		t.Fatalf("verification target = %#v, want RUNNING", got)
	}
}
