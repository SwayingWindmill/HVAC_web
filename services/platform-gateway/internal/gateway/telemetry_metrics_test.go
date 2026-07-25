package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestTelemetryUpstreamMetricsUseOnlyBoundedLabels(t *testing.T) {
	runtime := observability.NewRuntime(observability.RuntimeConfig{Service: serviceName})
	h := &handler{observability: runtime}
	h.observeTelemetryUpstream(internalTelemetryBatchPath, "success", 25*time.Millisecond)
	h.observeTelemetryUpstream(internalTelemetryCheckpointPath, "timeout", 2*time.Second)

	recorder := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, marker := range []string{
		"hvac_s2_upstream_requests_total",
		"hvac_s2_upstream_duration_seconds",
		"dependency=\"telemetry-runtime\"",
		"operation=\"batch\"",
		"operation=\"checkpoint\"",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("expected %s in metrics: %s", marker, body)
		}
	}
	for _, forbidden := range []string{"device_id", "organization_id", "site_id", "request_id", "trace_id", "cursor", "channel"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("upstream metrics leaked forbidden label %s: %s", forbidden, body)
		}
	}
}
