package connectivity

import (
	"context"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestIsControlLockBlocking(t *testing.T) {
	tests := []struct {
		name            string
		siteControlMode string
		deviceLockMode  string
		capability      commandmodel.Capability
		actuationMode   string
		want            bool
	}{
		{
			name:            "empty lock states allow control",
			siteControlMode: "",
			deviceLockMode:  "",
			capability:      commandmodel.CapabilityStart,
			actuationMode:   "PULSE",
			want:            false,
		},
		{
			name:            "remote auto and unlocked allow all commands",
			siteControlMode: "REMOTE_AUTO",
			deviceLockMode:  "UNLOCKED",
			capability:      commandmodel.CapabilitySetFrequency,
			actuationMode:   "LEVEL",
			want:            false,
		},
		{
			name:            "manual override blocks non-shutdown commands",
			siteControlMode: "MANUAL_OVERRIDE",
			deviceLockMode:  "UNLOCKED",
			capability:      commandmodel.CapabilityStart,
			actuationMode:   "PULSE",
			want:            true,
		},
		{
			name:            "safe shutdown exit blocks setpoint commands",
			siteControlMode: "SAFE_SHUTDOWN_EXIT",
			deviceLockMode:  "",
			capability:      commandmodel.CapabilitySetTemperatureSetpoint,
			actuationMode:   "",
			want:            true,
		},
		{
			name:            "manual override allows stop to release holding channel",
			siteControlMode: "MANUAL_OVERRIDE",
			deviceLockMode:  "UNLOCKED",
			capability:      commandmodel.CapabilityStop,
			actuationMode:   "LEVEL",
			want:            false,
		},
		{
			name:            "safe shutdown exit allows stop unit sequence",
			siteControlMode: "SAFE_SHUTDOWN_EXIT",
			deviceLockMode:  "",
			capability:      commandmodel.CapabilityStopUnitSequence,
			actuationMode:   "UNIT_SEQUENCE",
			want:            false,
		},
		{
			name:            "device lock blocks normal stop under remote auto",
			siteControlMode: "REMOTE_AUTO",
			deviceLockMode:  "MAINTENANCE_LOCKED",
			capability:      commandmodel.CapabilityStop,
			actuationMode:   "LEVEL",
			want:            true,
		},
		{
			name:            "device lock blocks normal stop under safe shutdown exit",
			siteControlMode: "SAFE_SHUTDOWN_EXIT",
			deviceLockMode:  "ASSET_LOCKED",
			capability:      commandmodel.CapabilityStopUnitSequence,
			actuationMode:   "UNIT_SEQUENCE",
			want:            true,
		},
		{
			name:            "device lock blocks non-shutdown even if actuationMode is emergency do release",
			siteControlMode: "REMOTE_AUTO",
			deviceLockMode:  "EMERGENCY_STOPPED",
			capability:      commandmodel.CapabilityStart,
			actuationMode:   "EMERGENCY_DO_RELEASE",
			want:            true,
		},
		{
			name:            "emergency do release bypasses site mode and device lock on stop",
			siteControlMode: "MANUAL_OVERRIDE",
			deviceLockMode:  "EMERGENCY_STOPPED",
			capability:      commandmodel.CapabilityStop,
			actuationMode:   "EMERGENCY_DO_RELEASE",
			want:            false,
		},
		{
			name:            "emergency do release bypasses site mode and device lock on stop unit sequence",
			siteControlMode: "SAFE_SHUTDOWN_EXIT",
			deviceLockMode:  "ASSET_LOCKED",
			capability:      commandmodel.CapabilityStopUnitSequence,
			actuationMode:   "EMERGENCY_DO_RELEASE",
			want:            false,
		},
		{
			name:            "lowercase emergency do release with spaces still bypasses device lock",
			siteControlMode: "MANUAL_OVERRIDE",
			deviceLockMode:  "EMERGENCY_STOPPED",
			capability:      commandmodel.CapabilityStop,
			actuationMode:   " emergency_do_release ",
			want:            false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isControlLockBlocking(tc.siteControlMode, tc.deviceLockMode, tc.capability, tc.actuationMode); got != tc.want {
				t.Fatalf("isControlLockBlocking(%q, %q, %q, %q) = %v, want %v",
					tc.siteControlMode, tc.deviceLockMode, tc.capability, tc.actuationMode, got, tc.want)
			}
		})
	}
}

func TestApplyPointSourceMetadataNormalizesActuationModeForLockCheck(t *testing.T) {
	var route commandmodel.DeviceRoute
	raw := []byte(`{"actuationMode":" emergency_do_release ","channel":" all-do "}`)
	if err := applyPointSourceMetadata(&route, "emergency_do_release", raw); err != nil {
		t.Fatal(err)
	}
	if route.ActuationMode != "EMERGENCY_DO_RELEASE" || route.Channel != "all-do" {
		t.Fatalf("unexpected normalized route: %#v", route)
	}
	if isControlLockBlocking("SAFE_SHUTDOWN_EXIT", "EMERGENCY_STOPPED", commandmodel.CapabilityStop, route.ActuationMode) {
		t.Fatal("normalized EMERGENCY_DO_RELEASE route should not be blocked by control lock")
	}
}

func TestResolveCommandActuationModeGuardsAndNormalization(t *testing.T) {
	var nilStore *Store
	if _, err := nilStore.ResolveCommandActuationMode(context.Background(), commandmodel.DispatchEnvelope{}); err == nil {
		t.Fatal("expected error from nil connectivity store")
	}
	closedStore := &Store{}
	if _, err := closedStore.ResolveCommandActuationMode(context.Background(), commandmodel.DispatchEnvelope{}); err == nil {
		t.Fatal("expected error from closed connectivity store")
	}
	for raw, want := range map[string]string{
		"":                        "",
		"   ":                     "",
		"pulse":                   "PULSE",
		"  emergency_do_release ": "EMERGENCY_DO_RELEASE",
		"LEVEL":                   "LEVEL",
	} {
		if got := normalizeActuationMode(raw); got != want {
			t.Fatalf("normalizeActuationMode(%q) = %q, want %q", raw, got, want)
		}
	}
}
