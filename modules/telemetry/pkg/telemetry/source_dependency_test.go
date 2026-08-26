package telemetry

import "testing"

func TestSourceDependencyClassifiesMQTTAndOther(t *testing.T) {
	if got := sourceDependency("spiffe://hvac.local/mqtt-telemetry-adapter"); got != "mqtt" {
		t.Fatalf("mqtt dependency=%q", got)
	}
	if got := sourceDependency("spiffe://hvac.local/other-source"); got != "other" {
		t.Fatalf("other dependency=%q", got)
	}
}
