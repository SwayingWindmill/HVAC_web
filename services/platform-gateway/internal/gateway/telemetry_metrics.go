package gateway

import (
	"strings"
	"time"
)

func (h *handler) observeTelemetryUpstream(path, outcome string, elapsed time.Duration) {
	if h == nil || h.observability == nil || h.observability.Metrics == nil {
		return
	}
	operation := telemetryUpstreamOperation(path)
	labels := map[string]string{
		"dependency": "telemetry-runtime",
		"operation":  operation,
		"outcome":    outcome,
	}
	_ = h.observability.Metrics.AddCounter("hvac_s2_upstream_requests_total", "Gateway calls to S2 upstream dependencies.", labels, 1)
	_ = h.observability.Metrics.ObserveHistogram("hvac_s2_upstream_duration_seconds", "Gateway call duration for S2 upstream dependencies.", labels, elapsed.Seconds(), nil)
}

func telemetryUpstreamOperation(path string) string {
	switch {
	case path == internalTelemetryBatchPath:
		return "batch"
	case path == internalTelemetryBootstrapPath:
		return "bootstrap"
	case path == internalTelemetryCheckpointPath || path == internalTelemetryCheckpointResolvePath:
		return "checkpoint"
	case strings.HasPrefix(path, internalTelemetrySinglePrefix):
		return "snapshot"
	default:
		return "snapshot"
	}
}
