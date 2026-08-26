package telemetry

import "strings"

func sourceDependency(peerSPIFFE string) string {
	peer := strings.ToLower(strings.TrimSpace(peerSPIFFE))
	switch {
	case strings.Contains(peer, "mqtt-telemetry-adapter"):
		return "mqtt"
	default:
		return "other"
	}
}
