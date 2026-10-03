package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestMQTTRuntimeMetricsStayLowCardinality(t *testing.T) {
	registry := observability.NewRegistry()
	runtime := &Runtime{metrics: registry}
	runtime.recordConnectionState(true, true, nil)
	runtime.recordProcessingResult(ProcessingResult{
		MessageID: "018f3e00-0000-7000-8000-000000000001",
		Replay:    true, PointCount: 5, Accepted: 2, Duplicate: 1, OutOfOrder: 1, Quarantined: 1,
	}, 250*time.Millisecond)
	runtime.recordQueueDepth("018f3e00-4000-7000-8000-000000000100", 3)

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, marker := range []string{
		"hvac_mqtt_connected 1",
		"hvac_mqtt_subscribed 1",
		`hvac_mqtt_messages_processed_total{outcome="success"} 1`,
		`hvac_mqtt_replay_messages_total{outcome="processed"} 1`,
		`hvac_mqtt_values_total{outcome="accepted"} 2`,
		`hvac_mqtt_values_total{outcome="duplicate"} 1`,
		`hvac_mqtt_values_total{outcome="out_of_order"} 1`,
		`hvac_mqtt_values_total{outcome="quarantined"} 1`,
		`hvac_mqtt_gateway_queue_depth{gateway_id="018f3e00-4000-7000-8000-000000000100"} 3`,
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("missing %q in metrics:\n%s", marker, body)
		}
	}
	// Gateways are bounded Registry objects, so only the per-Gateway backlog names one.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "hvac_mqtt_gateway_queue_depth{") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "018f3e00") || strings.Contains(line, "message_id") || strings.Contains(line, "gateway") {
			t.Fatalf("MQTT metric leaked a high-cardinality identity: %s", line)
		}
	}
}
