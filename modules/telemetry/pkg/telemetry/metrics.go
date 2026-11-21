package telemetry

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	HistoryOutboxStatePending    = "pending"
	HistoryOutboxStatePublishing = "publishing"
	HistoryOutboxStateDeadLetter = "dead_letter"

	LatestCacheOutcomeApplied      = "applied"
	LatestCacheOutcomeStaleSkipped = "stale_skipped"
	LatestCacheOutcomeInvalidated  = "invalidated"
	LatestCacheOutcomeError        = "error"

	ColdArchiveOutcomeSucceeded = "succeeded"
	ColdArchiveOutcomeFailed    = "failed"
)

// PipelineMetrics records bounded-label Prometheus metrics for telemetry ingest,
// hot latest-cache updates, ClickHouse history writes, and cold archive pruning.
type PipelineMetrics struct {
	registry *observability.Registry
}

// NewPipelineMetrics wraps registry for telemetry pipeline instrumentation.
func NewPipelineMetrics(registry *observability.Registry) *PipelineMetrics {
	if registry == nil {
		return nil
	}
	return &PipelineMetrics{registry: registry}
}

func (m *PipelineMetrics) SetHistoryOutboxDepth(state string, depth int) {
	if m == nil || m.registry == nil {
		return
	}
	normalized, ok := normalizeHistoryOutboxState(state)
	if !ok {
		return
	}
	if depth < 0 {
		depth = 0
	}
	_ = m.registry.SetGauge(
		"hvac_s2_history_outbox_depth",
		"Current row count in the telemetry history outbox by delivery state.",
		map[string]string{"state": normalized},
		float64(depth),
	)
}

func (m *PipelineMetrics) SetHistoryOutboxDepths(pending, publishing, deadLetter int) {
	if m == nil || m.registry == nil {
		return
	}
	m.SetHistoryOutboxDepth(HistoryOutboxStatePending, pending)
	m.SetHistoryOutboxDepth(HistoryOutboxStatePublishing, publishing)
	m.SetHistoryOutboxDepth(HistoryOutboxStateDeadLetter, deadLetter)
}

func (m *PipelineMetrics) ObserveHistoryFlushDuration(elapsed time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	if elapsed < 0 {
		elapsed = 0
	}
	_ = m.registry.ObserveHistogram(
		"hvac_s2_history_flush_duration_seconds",
		"Duration of telemetry history batch flushes to ClickHouse.",
		nil,
		elapsed.Seconds(),
		nil,
	)
}

func (m *PipelineMetrics) AddHistoryRowsProjected(count int) {
	if m == nil || m.registry == nil || count <= 0 {
		return
	}
	_ = m.registry.AddCounter(
		"hvac_s2_history_rows_projected_total",
		"Telemetry observations written to ClickHouse history.",
		nil,
		float64(count),
	)
}

func (m *PipelineMetrics) AddReplayObservations(count int) {
	if m == nil || m.registry == nil || count <= 0 {
		return
	}
	_ = m.registry.AddCounter(
		"hvac_s2_replay_observations_total",
		"Telemetry replay observations processed by the runtime store.",
		nil,
		float64(count),
	)
}

func (m *PipelineMetrics) AddLatestCacheUpdates(outcome string, count int) {
	if m == nil || m.registry == nil || count <= 0 {
		return
	}
	normalized, ok := normalizeLatestCacheOutcome(outcome)
	if !ok {
		return
	}
	_ = m.registry.AddCounter(
		"hvac_s2_latest_cache_updates_total",
		"Telemetry latest cache updates by bounded outcome.",
		map[string]string{"outcome": normalized},
		float64(count),
	)
}

func (m *PipelineMetrics) ObserveColdArchiveRun(outcome string) {
	if m == nil || m.registry == nil {
		return
	}
	normalized, ok := normalizeColdArchiveOutcome(outcome)
	if !ok {
		return
	}
	_ = m.registry.AddCounter(
		"hvac_s2_cold_archive_runs_total",
		"ClickHouse cold archive and retention runs by outcome.",
		map[string]string{"outcome": normalized},
		1,
	)
}

func (m *PipelineMetrics) AddAlarmEvaluationDeadLetter(reasonFamily string) {
	if m == nil || m.registry == nil {
		return
	}
	_ = m.registry.AddCounter(
		"hvac_alarm_evaluation_dead_letter_total",
		"Total telemetry alarm evaluation publications transitioned to DEAD_LETTER by reason family.",
		map[string]string{"reason_family": normalizeAlarmEvaluationDeadLetterReason(reasonFamily)},
		1,
	)
}

func (m *PipelineMetrics) SetAlarmEvaluationOutboxDepths(pending, deadLetter int64) {
	if m == nil || m.registry == nil {
		return
	}
	if pending < 0 {
		pending = 0
	}
	if deadLetter < 0 {
		deadLetter = 0
	}
	_ = m.registry.SetGauge(
		"hvac_alarm_evaluation_outbox_depth",
		"Current telemetry alarm evaluation outbox row count by delivery state.",
		map[string]string{"state": "pending"},
		float64(pending),
	)
	_ = m.registry.SetGauge(
		"hvac_alarm_evaluation_outbox_depth",
		"Current telemetry alarm evaluation outbox row count by delivery state.",
		map[string]string{"state": "dead_letter"},
		float64(deadLetter),
	)
}

func normalizeAlarmEvaluationDeadLetterReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "payload_invalid", "alarm_evaluation_payload_invalid", "scope":
		return "scope"
	case "max_attempts_exceeded", "alarm_evaluation_max_attempts_exceeded", "transport", "timeout":
		return "transport"
	case "permanent_failure", "alarm_evaluation_permanent_failure", "dependency", "delivery_error":
		return "dependency"
	case "rate_limited":
		return "rate_limited"
	case "authorization":
		return "authorization"
	case "capacity":
		return "capacity"
	default:
		return "unknown"
	}
}

func normalizeHistoryOutboxState(state string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case HistoryOutboxStatePending:
		return HistoryOutboxStatePending, true
	case HistoryOutboxStatePublishing, "in_flight":
		return HistoryOutboxStatePublishing, true
	case HistoryOutboxStateDeadLetter, "dead":
		return HistoryOutboxStateDeadLetter, true
	default:
		return "", false
	}
}

func normalizeLatestCacheOutcome(outcome string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case LatestCacheOutcomeApplied:
		return LatestCacheOutcomeApplied, true
	case LatestCacheOutcomeStaleSkipped:
		return LatestCacheOutcomeStaleSkipped, true
	case LatestCacheOutcomeInvalidated:
		return LatestCacheOutcomeInvalidated, true
	case LatestCacheOutcomeError:
		return LatestCacheOutcomeError, true
	default:
		return "", false
	}
}

func normalizeColdArchiveOutcome(outcome string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case ColdArchiveOutcomeSucceeded:
		return ColdArchiveOutcomeSucceeded, true
	case ColdArchiveOutcomeFailed:
		return ColdArchiveOutcomeFailed, true
	default:
		return "", false
	}
}

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
	case path == InternalSourceObservationBatchPath:
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			metrics.observeIngest(outcome, reason)
		}
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

func (metrics *s2Metrics) observeSourceLag(source, outcome string, sampledAt, receivedAt time.Time) {
	if metrics == nil || metrics.registry == nil || sampledAt.IsZero() || receivedAt.IsZero() {
		return
	}
	lag := receivedAt.Sub(sampledAt)
	if lag < 0 {
		lag = 0
	}
	_ = metrics.registry.ObserveHistogram("hvac_s2_source_lag_seconds", "S2 source-to-runtime lag.", map[string]string{"dependency": source, "outcome": outcome}, lag.Seconds(), nil)
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

func (metrics *s2Metrics) observeDataQuality(receipt ObservationReceipt) {
	if metrics == nil || metrics.registry == nil {
		return
	}
	quality := strings.ToLower(strings.TrimSpace(string(receipt.Quality)))
	if quality == "" {
		quality = "unknown"
	}
	outcome := strings.ToLower(strings.TrimSpace(string(receipt.Status)))
	if outcome == "" {
		outcome = "unknown"
	}
	_ = metrics.registry.AddCounter("hvac_s2_data_quality_records_total", "S2 observations by bounded quality and acceptance outcome.", map[string]string{"quality": quality, "outcome": outcome}, 1)
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
