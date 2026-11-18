package telemetry

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
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
	metrics.observeRequest(InternalSourceObservationBatchPath, 200, 5*time.Millisecond)
	metrics.observeIngest("rejected", "scope")
	metrics.observeSourceLag("other", "rejected", now.Add(-3*time.Second), now)
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

func TestPipelineMetricsRecordBoundedSeriesAndScrape(t *testing.T) {
	var nilPipeline *PipelineMetrics
	nilPipeline.SetHistoryOutboxDepths(1, 2, 3)
	nilPipeline.ObserveHistoryFlushDuration(time.Second)
	nilPipeline.AddHistoryRowsProjected(5)
	nilPipeline.AddReplayObservations(2)
	nilPipeline.AddLatestCacheUpdates(LatestCacheOutcomeApplied, 1)
	nilPipeline.ObserveColdArchiveRun(ColdArchiveOutcomeSucceeded)

	if NewPipelineMetrics(nil) != nil {
		t.Fatal("expected NewPipelineMetrics(nil) to return nil")
	}

	registry := observability.NewRegistry()
	pipeline := NewPipelineMetrics(registry)
	pipeline.SetHistoryOutboxDepths(12, 4, 1)
	pipeline.SetHistoryOutboxDepth("IN_FLIGHT", 3)
	pipeline.SetHistoryOutboxDepth("DEAD", 2)
	pipeline.SetHistoryOutboxDepth("unbounded_state", 99)
	pipeline.ObserveHistoryFlushDuration(-5 * time.Millisecond)
	pipeline.ObserveHistoryFlushDuration(150 * time.Millisecond)
	pipeline.AddHistoryRowsProjected(0)
	pipeline.AddHistoryRowsProjected(256)
	pipeline.AddReplayObservations(42)
	pipeline.AddLatestCacheUpdates(LatestCacheOutcomeApplied, 10)
	pipeline.AddLatestCacheUpdates(LatestCacheOutcomeStaleSkipped, 3)
	pipeline.AddLatestCacheUpdates(LatestCacheOutcomeInvalidated, 2)
	pipeline.AddLatestCacheUpdates(LatestCacheOutcomeError, 1)
	pipeline.AddLatestCacheUpdates("unbounded_outcome", 99)
	pipeline.ObserveColdArchiveRun(ColdArchiveOutcomeSucceeded)
	pipeline.ObserveColdArchiveRun(ColdArchiveOutcomeFailed)
	pipeline.ObserveColdArchiveRun("unbounded_archive")

	s2 := newS2Metrics(registry, nil)
	s2.observeHistoryOutboxDepths(15, 1, 2)
	s2.observeHistoryFlushDuration(25 * time.Millisecond)
	s2.observeHistoryRowsProjected(4)
	s2.observeReplayObservations(8)
	s2.observeLatestCacheUpdates(LatestCacheOutcomeApplied, 5)
	s2.observeColdArchiveRun(ColdArchiveOutcomeSucceeded)

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()

	for _, want := range []string{
		`hvac_s2_history_outbox_depth{state="pending"} 15`,
		`hvac_s2_history_outbox_depth{state="publishing"} 1`,
		`hvac_s2_history_outbox_depth{state="dead_letter"} 2`,
		`hvac_s2_history_flush_duration_seconds_count 3`,
		`hvac_s2_history_rows_projected_total 260`,
		`hvac_s2_replay_observations_total 50`,
		`hvac_s2_latest_cache_updates_total{outcome="applied"} 15`,
		`hvac_s2_latest_cache_updates_total{outcome="stale_skipped"} 3`,
		`hvac_s2_latest_cache_updates_total{outcome="invalidated"} 2`,
		`hvac_s2_latest_cache_updates_total{outcome="error"} 1`,
		`hvac_s2_cold_archive_runs_total{outcome="succeeded"} 2`,
		`hvac_s2_cold_archive_runs_total{outcome="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in /metrics output:\n%s", want, body)
		}
	}
	for _, unexpected := range []string{"unbounded_state", "unbounded_outcome", "unbounded_archive", "device_id", "site_id"} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("unexpected unbounded label %q leaked in /metrics output:\n%s", unexpected, body)
		}
	}

	cardinality := registry.SeriesCardinality()
	if cardinality["hvac_s2_history_outbox_depth"] != 3 {
		t.Fatalf("hvac_s2_history_outbox_depth cardinality=%d, want 3", cardinality["hvac_s2_history_outbox_depth"])
	}
	if cardinality["hvac_s2_latest_cache_updates_total"] != 4 {
		t.Fatalf("hvac_s2_latest_cache_updates_total cardinality=%d, want 4", cardinality["hvac_s2_latest_cache_updates_total"])
	}
	if cardinality["hvac_s2_cold_archive_runs_total"] != 2 {
		t.Fatalf("hvac_s2_cold_archive_runs_total cardinality=%d, want 2", cardinality["hvac_s2_cold_archive_runs_total"])
	}
}
