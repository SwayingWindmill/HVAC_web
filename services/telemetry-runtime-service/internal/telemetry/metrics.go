package telemetry

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

type s2Metrics struct {
	registry *observability.Registry
	now      func() time.Time
}

type statusCaptureWriter struct {
	http.ResponseWriter
	status int
}

type instrumentedRealtimeTransport struct {
	delegate RealtimeTransport
	metrics  *s2Metrics
}

func (writer *statusCaptureWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusCaptureWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.ResponseWriter.Write(body)
}

func newS2Metrics(registry *observability.Registry, now func() time.Time) *s2Metrics {
	if now == nil {
		now = time.Now
	}
	return &s2Metrics{registry: registry, now: now}
}

func InstrumentRealtimeTransport(delegate RealtimeTransport, registry *observability.Registry, now func() time.Time) RealtimeTransport {
	if delegate == nil || registry == nil {
		return delegate
	}
	return &instrumentedRealtimeTransport{delegate: delegate, metrics: newS2Metrics(registry, now)}
}

func (transport *instrumentedRealtimeTransport) Publish(ctx context.Context, channel string, publication DeviceObservationPublication) error {
	evaluatedAt, _ := time.Parse(time.RFC3339Nano, string(publication.EvaluatedAt))
	if err := transport.delegate.Publish(ctx, channel, publication); err != nil {
		transport.metrics.observePublication("failed", "dependency", evaluatedAt, "claimed")
		return err
	}
	transport.metrics.observePublication("success", "none", evaluatedAt, "published")
	return nil
}

func (transport *instrumentedRealtimeTransport) Unsubscribe(ctx context.Context, principalID, channel string) error {
	if err := transport.delegate.Unsubscribe(ctx, principalID, channel); err != nil {
		_ = transport.metrics.registry.AddCounter("hvac_s2_revocation_events_total", "S2 revocation lifecycle events.", map[string]string{"phase": "post_revocation", "outcome": "failed", "reason_family": "dependency"}, 1)
		return err
	}
	_ = transport.metrics.registry.AddCounter("hvac_s2_revocation_events_total", "S2 revocation lifecycle events.", map[string]string{"phase": "post_revocation", "outcome": "success", "reason_family": "none"}, 1)
	return nil
}

func (metrics *s2Metrics) capture(writer http.ResponseWriter) *statusCaptureWriter {
	return &statusCaptureWriter{ResponseWriter: writer}
}

func (metrics *s2Metrics) observeRequest(path string, status int, elapsed time.Duration) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	outcome, reason := metricOutcome(status)
	switch {
	case path == InternalThingsBoardObservationPath:
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			metrics.observeIngest(outcome, reason)
		}
	case path == InternalThingsBoardCoveragePath:
		_ = metrics.registry.AddCounter("hvac_s2_presence_evaluations_total", "S2 Presence coverage evaluations.", map[string]string{"outcome": outcome, "reason_family": reason}, 1)
	case path == InternalBatchSnapshotPath:
		metrics.observeSnapshotRequest("batch", outcome, elapsed)
	case strings.HasPrefix(path, InternalDeviceSnapshotPrefix) && strings.HasSuffix(path, "/observation-snapshot"):
		metrics.observeSnapshotRequest("snapshot", outcome, elapsed)
	case path == InternalSubscriptionBootstrapPath:
		_ = metrics.registry.AddCounter("hvac_s2_subscription_events_total", "S2 subscription lifecycle events.", map[string]string{"operation": "bootstrap", "outcome": outcome, "reason_family": reason}, 1)
	case path == InternalRecoveryCheckpointResolvePath || path == InternalRecoveryCheckpointPath:
		_ = metrics.registry.AddCounter("hvac_s2_subscription_events_total", "S2 subscription lifecycle events.", map[string]string{"operation": "checkpoint", "outcome": outcome, "reason_family": reason}, 1)
	case path == InternalCentrifugoSubscribePath:
		_ = metrics.registry.AddCounter("hvac_s2_subscription_events_total", "S2 subscription lifecycle events.", map[string]string{"operation": "subscribe", "outcome": outcome, "reason_family": reason}, 1)
	case path == InternalSubscriptionRevokePath:
		_ = metrics.registry.AddCounter("hvac_s2_revocation_events_total", "S2 revocation lifecycle events.", map[string]string{"phase": "acknowledged", "outcome": outcome, "reason_family": reason}, 1)
	}
}

func (metrics *s2Metrics) observeSnapshotRequest(operation, outcome string, elapsed time.Duration) {
	labels := map[string]string{"operation": operation, "outcome": outcome, "route_stage": "R0"}
	_ = metrics.registry.AddCounter("hvac_s2_snapshot_requests_total", "S2 authoritative Snapshot requests.", labels, 1)
	_ = metrics.registry.ObserveHistogram("hvac_s2_snapshot_duration_seconds", "S2 authoritative Snapshot request duration.", labels, elapsed.Seconds(), nil)
}

func (metrics *s2Metrics) observeIngest(outcome, reason string) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	_ = metrics.registry.AddCounter("hvac_s2_ingest_records_total", "S2 source observations accepted or rejected.", map[string]string{"outcome": outcome, "reason_family": reason}, 1)
}

func (metrics *s2Metrics) observeSourceLag(sampledAt, receivedAt time.Time, outcome string) {
	if metrics == nil || metrics.registry == nil || sampledAt.IsZero() || receivedAt.IsZero() {
		return
	}
	lag := receivedAt.Sub(sampledAt)
	if lag < 0 {
		lag = 0
	}
	_ = metrics.registry.ObserveHistogram("hvac_s2_source_lag_seconds", "ThingsBoard source-to-runtime lag.", map[string]string{"dependency": "thingsboard", "outcome": outcome}, lag.Seconds(), nil)
}

func (metrics *s2Metrics) observeSnapshot(snapshot telemetryapi.DeviceObservationSnapshot) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	evaluatedAt, err := time.Parse(time.RFC3339Nano, string(snapshot.EvaluatedAt))
	if err == nil {
		age := metrics.now().UTC().Sub(evaluatedAt)
		if age < 0 {
			age = 0
		}
		outcome := "success"
		if snapshot.EvaluationAvailability == telemetryapi.EvaluationAvailabilityUnavailable {
			outcome = "unavailable"
		}
		_ = metrics.registry.SetGauge("hvac_s2_snapshot_age_seconds", "Age of the authoritative S2 Snapshot at response time.", map[string]string{"cohort": "control", "outcome": outcome}, age.Seconds())
	}
	presenceOutcome := "success"
	reason := "none"
	if snapshot.EvaluationAvailability == telemetryapi.EvaluationAvailabilityUnavailable {
		presenceOutcome = "unavailable"
		reason = "dependency"
	}
	_ = metrics.registry.AddCounter("hvac_s2_presence_evaluations_total", "S2 Presence evaluations emitted in Snapshots.", map[string]string{"outcome": presenceOutcome, "reason_family": reason}, 1)
	lastSeen := snapshot.Presence.LastSeenAt
	if lastSeen == nil {
		return
	}
	observedAt, err := time.Parse(time.RFC3339Nano, string(*lastSeen))
	if err != nil {
		return
	}
	lateness := metrics.now().UTC().Sub(observedAt)
	if lateness < 0 {
		lateness = 0
	}
	_ = metrics.registry.ObserveHistogram("hvac_s2_presence_lateness_seconds", "Age of the latest accepted Presence signal.", map[string]string{"outcome": presenceOutcome, "reason_family": reason}, lateness.Seconds(), nil)
}

func (metrics *s2Metrics) observeQuarantine(reason string) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	if reason == "" {
		reason = "scope"
	}
	_ = metrics.registry.AddCounter("hvac_s2_quarantine_records_total", "S2 source records placed in quarantine.", map[string]string{"reason_family": reason, "outcome": "rejected"}, 1)
}

func (metrics *s2Metrics) observeRecovery(outcome, reason string, elapsed time.Duration) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	labels := map[string]string{"outcome": outcome, "reason_family": reason, "transport": "centrifugo"}
	_ = metrics.registry.AddCounter("hvac_s2_recovery_attempts_total", "S2 recovery attempts.", labels, 1)
	_ = metrics.registry.ObserveHistogram("hvac_s2_recovery_duration_seconds", "S2 recovery decision duration.", map[string]string{"outcome": outcome, "transport": "centrifugo"}, elapsed.Seconds(), nil)
}

func (metrics *s2Metrics) observePublication(outcome, reason string, evaluatedAt time.Time, phase string) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	_ = metrics.registry.AddCounter("hvac_s2_publications_total", "S2 realtime publications.", map[string]string{"outcome": outcome, "reason_family": reason, "transport": "centrifugo"}, 1)
	_ = metrics.registry.AddCounter("hvac_s2_outbox_messages_total", "S2 publication outbox lifecycle.", map[string]string{"phase": phase, "outcome": outcome, "reason_family": reason}, 1)
	if evaluatedAt.IsZero() {
		return
	}
	lag := metrics.now().UTC().Sub(evaluatedAt)
	if lag < 0 {
		lag = 0
	}
	_ = metrics.registry.ObserveHistogram("hvac_s2_publication_lag_seconds", "S2 evaluation-to-publication lag.", map[string]string{"outcome": outcome, "transport": "centrifugo"}, lag.Seconds(), nil)
	_ = metrics.registry.ObserveHistogram("hvac_s2_outbox_lag_seconds", "S2 publication outbox lag.", map[string]string{"phase": phase, "outcome": outcome}, lag.Seconds(), nil)
}

func (metrics *s2Metrics) observeInvariant(invariant string) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	_ = metrics.registry.AddCounter("hvac_s2_security_zero_invariant_total", "S2 zero-tolerance security invariant violations.", map[string]string{"invariant": invariant}, 1)
}

func quarantineReasonFamily(reason QuarantineReason) string {
	switch reason {
	case QuarantinePolicyNotConfigured:
		return "dependency"
	default:
		return "scope"
	}
}

func metricOutcome(status int) (string, string) {
	switch {
	case status >= 200 && status < 300:
		return "success", "none"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "rejected", "authorization"
	case status >= 400 && status < 500:
		return "rejected", "scope"
	case status == http.StatusGatewayTimeout:
		return "timeout", "dependency"
	default:
		return "unavailable", "dependency"
	}
}
