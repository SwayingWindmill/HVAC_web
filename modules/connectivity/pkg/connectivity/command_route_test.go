package connectivity

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestApplyPointSourceMetadataNormalizesActuationMode(t *testing.T) {
	var route commandmodel.DeviceRoute
	raw := []byte(`{"actuationMode":" emergency_do_release ","channel":" all-do "}`)
	if err := applyPointSourceMetadata(&route, "emergency_do_release", raw); err != nil {
		t.Fatal(err)
	}
	if route.ActuationMode != "EMERGENCY_DO_RELEASE" || route.Channel != "all-do" {
		t.Fatalf("unexpected normalized route: %#v", route)
	}
	for rawMode, want := range map[string]string{
		"":                        "",
		"   ":                     "",
		"pulse":                   "PULSE",
		"  emergency_do_release ": "EMERGENCY_DO_RELEASE",
		"LEVEL":                   "LEVEL",
	} {
		if got := normalizeActuationMode(rawMode); got != want {
			t.Fatalf("normalizeActuationMode(%q) = %q, want %q", rawMode, got, want)
		}
	}
}
