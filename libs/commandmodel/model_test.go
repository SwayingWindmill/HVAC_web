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
