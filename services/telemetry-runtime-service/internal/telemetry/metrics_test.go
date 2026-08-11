package telemetry

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

type metricsTransport struct {
	publishErr     error
	unsubscribeErr error
}

func (transport *metricsTransport) Publish(context.Context, string, DeviceObservationPublication) error {
	return transport.publishErr
}

func (transport *metricsTransport) Unsubscribe(context.Context, string, string) error {
	return transport.unsubscribeErr
}

func TestS2MetricsUseBoundedLabelsAndBaseUnits(t *testing.T) {
	now := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	registry := observability.NewRegistry()
	metrics := newS2Metrics(registry, func() time.Time { return now })
	metrics.observeRequest(InternalBatchSnapshotPath, 200, 250*time.Millisecond)
	metrics.observeRequest(InternalSourceObservationPath, 200, 5*time.Millisecond)
	metrics.observeIngest("rejected", "scope")
	metrics.observeSourceLag("thingsboard", "rejected", now.Add(-3*time.Second), now)
	metrics.observeDataQuality(ObservationReceipt{Status: ObservationAccepted, Quality: QualityGood})
	metrics.observeQuarantine("scope")
	metrics.observeRecovery("success", "none", 10*time.Millisecond)
	metrics.observeInvariant("revision_gap")

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, marker := range []string{
		"hvac_s2_ingest_records_total",
		"hvac_s2_snapshot_requests_total",
		"hvac_s2_snapshot_duration_seconds",
		"hvac_s2_source_lag_seconds",
		"hvac_s2_data_quality_records_total",
		"hvac_s2_quarantine_records_total",
		"hvac_s2_recovery_attempts_total",
		"hvac_s2_security_zero_invariant_total",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("expected metric %s in output: %s", marker, body)
		}
	}
	if strings.Contains(body, `hvac_s2_ingest_records_total{outcome="success"`) {
		t.Fatalf("HTTP 200 quarantine-compatible response was incorrectly counted as accepted ingest: %s", body)
	}
	if !strings.Contains(body, `hvac_s2_ingest_records_total{outcome="rejected",reason_family="scope"} 1`) {
		t.Fatalf("expected receipt-derived rejected ingest metric: %s", body)
	}
	for _, forbidden := range []string{"device_id", "site_id", "organization_id", "subscription_id", "cursor", "channel", "telemetry_key"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metric output leaked forbidden label %s: %s", forbidden, body)
		}
	}
}

func TestInstrumentRealtimeTransportRecordsSuccessAndFailure(t *testing.T) {
	now := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	registry := observability.NewRegistry()
	delegate := &metricsTransport{}
	transport := InstrumentRealtimeTransport(delegate, registry, func() time.Time { return now })
	publication := DeviceObservationPublication{EvaluatedAt: telemetryapi.Instant(now.Add(-2 * time.Second).Format(time.RFC3339Nano))}
	if err := transport.Publish(context.Background(), "opaque-channel", publication); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	delegate.publishErr = errors.New("transport unavailable")
	if err := transport.Publish(context.Background(), "opaque-channel", publication); err == nil {
		t.Fatal("expected publish failure")
	}
	if err := transport.Unsubscribe(context.Background(), "principal", "opaque-channel"); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, marker := range []string{
		"hvac_s2_publications_total",
		"hvac_s2_publication_lag_seconds",
		"hvac_s2_outbox_messages_total",
		"hvac_s2_outbox_lag_seconds",
		"hvac_s2_revocation_events_total",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("expected metric %s in output: %s", marker, body)
		}
	}
	if strings.Contains(body, "opaque-channel") || strings.Contains(body, "principal") {
		t.Fatalf("metric output leaked raw transport identifiers: %s", body)
	}
}
