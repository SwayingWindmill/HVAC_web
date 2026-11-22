package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RuntimeHistoryRetention is how long superseded presence signals and fully delivered
// publications stay in Postgres. Nothing reads them once superseded or delivered; the
// window only keeps recent rows for diagnosis.
const RuntimeHistoryRetention = time.Hour

// SourceObservationRetention is how long a received observation stays once its history event is
// delivered and pruned. Its receipt answers duplicate checks while a Gateway may still replay it (up
// to a day late); ClickHouse holds the history.
const SourceObservationRetention = 7 * 24 * time.Hour

const pruneBatchSize = 5000

type PrunedRuntimeHistory struct {
	PresenceSignals    int64
	Publications       int64
	HistoryEvents      int64
	SourceObservations int64
	DeadHistoryEvents  int64
	DeadPublications   int64
	MQTTRuntimeEvents  int64
	IngestQuarantines  int64
	RecoveryCursors    int64
	Subscriptions      int64
	ReplayPositions    int64
}

const (
	prunePresenceSignalsSQL = `
DELETE FROM telemetry_runtime.presence_signals
WHERE signal_id IN (
  SELECT old.signal_id
  FROM telemetry_runtime.presence_signals AS old
  WHERE old.observed_at < $1
    AND EXISTS (
      SELECT 1 FROM telemetry_runtime.presence_signals AS newer
      WHERE newer.device_id = old.device_id AND newer.signal_type = old.signal_type
        AND newer.accepted AND newer.observed_at > old.observed_at
    )
  LIMIT $2
)`
	prunePublicationsSQL = `
DELETE FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id IN (
  SELECT event_id
  FROM telemetry_runtime.telemetry_publication_outbox
  WHERE created_at < $1
    AND delivery_state = 'PUBLISHED'
    AND latest_cache_state IN ('NOT_APPLICABLE', 'MATERIALIZED')
    AND (alarm_delivery_state IS NULL OR alarm_delivery_state = 'PUBLISHED')
  LIMIT $2
)`
	pruneDeadPublicationsSQL = `
DELETE FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id IN (
  SELECT event_id
  FROM telemetry_runtime.telemetry_publication_outbox
  WHERE latest_cache_state IN ('NOT_APPLICABLE', 'MATERIALIZED')
    AND (
      (delivery_state = 'DEAD'
       AND created_at < $1
       AND (alarm_delivery_state IS NULL OR alarm_delivery_state IN ('PUBLISHED', 'DEAD_LETTER')))
      OR (delivery_state IN ('PUBLISHED', 'DEAD')
          AND alarm_delivery_state = 'DEAD_LETTER'
          AND alarm_dead_lettered_at < $1)
    )
  LIMIT $2
)`
	pruneHistoryEventsSQL = `
DELETE FROM telemetry_runtime.telemetry_history_outbox
WHERE event_id IN (
  SELECT event_id
  FROM telemetry_runtime.telemetry_history_outbox
  WHERE delivery_state = 'PUBLISHED' AND published_at < $1
  LIMIT $2
)`
	pruneDeadHistoryEventsSQL = `
DELETE FROM telemetry_runtime.telemetry_history_outbox
WHERE event_id IN (
  SELECT event_id
  FROM telemetry_runtime.telemetry_history_outbox
  WHERE delivery_state = 'DEAD' AND dead_lettered_at < $1
  LIMIT $2
)`
	pruneSourceObservationsSQL = `
DELETE FROM telemetry_runtime.source_observations
WHERE observation_id IN (
  SELECT observation.observation_id
  FROM telemetry_runtime.source_observations AS observation
  WHERE observation.created_at < $1
    AND NOT EXISTS (
      SELECT 1 FROM telemetry_runtime.telemetry_history_outbox AS event
      WHERE event.event_id = observation.observation_id
    )
  LIMIT $2
)`
	pruneMQTTRuntimeEventsSQL = `
DELETE FROM telemetry_runtime.mqtt_runtime_events
WHERE message_id IN (
  SELECT message_id
  FROM telemetry_runtime.mqtt_runtime_events
  WHERE received_at < $1
    AND (alarm_delivery_state IS NULL OR alarm_delivery_state <> 'PENDING')
  LIMIT $2
)`
	pruneIngestQuarantinesSQL = `
DELETE FROM telemetry_runtime.ingest_quarantine
WHERE quarantine_id IN (
  SELECT quarantine_id
  FROM telemetry_runtime.ingest_quarantine
  WHERE detected_at < $1
  LIMIT $2
)`
	pruneRecoveryCursorsSQL = `
DELETE FROM telemetry_runtime.recovery_cursors
WHERE cursor_id IN (
  SELECT cursor_id
  FROM telemetry_runtime.recovery_cursors
  WHERE expires_at < $1
  LIMIT $2
)`
	pruneSubscriptionsSQL = `
DELETE FROM telemetry_runtime.telemetry_subscriptions
WHERE subscription_id IN (
  SELECT sub.subscription_id
  FROM telemetry_runtime.telemetry_subscriptions AS sub
  WHERE sub.expires_at < $1
    AND NOT EXISTS (
      SELECT 1 FROM telemetry_runtime.recovery_cursors AS rc
      WHERE rc.subscription_id = sub.subscription_id
    )
    AND NOT EXISTS (
      SELECT 1 FROM telemetry_runtime.telemetry_publication_outbox AS pub
      WHERE pub.subscription_id = sub.subscription_id
    )
  LIMIT $2
)`
	pruneReplaySourcePositionsSQL = `
DELETE FROM telemetry_runtime.source_positions
WHERE (source_id, source_partition) IN (
  SELECT source_id, source_partition
  FROM telemetry_runtime.source_positions
  WHERE source_partition LIKE 'mqtt-replay:%'
    AND updated_at < $1
  LIMIT $2
)`
)

// PruneRuntimeHistory deletes superseded presence signals, delivered publications,
// expired dead-lettered rows, delivered history outbox events, unreferenced expired
// observations, MQTT runtime evidence, old quarantines, and expired cursors/subscriptions.
func (store *PostgresStore) PruneRuntimeHistory(ctx context.Context, now time.Time) (PrunedRuntimeHistory, error) {
	if store == nil || store.pool == nil {
		return PrunedRuntimeHistory{}, errors.New("telemetry runtime store is closed")
	}
	cutoff := now.UTC().Add(-RuntimeHistoryRetention)
	sourceCutoff := now.UTC().Add(-SourceObservationRetention)
	var pruned PrunedRuntimeHistory
	steps := []struct {
		label  string
		query  string
		cutoff time.Time
		target *int64
	}{
		{label: "presence signals", query: prunePresenceSignalsSQL, cutoff: cutoff, target: &pruned.PresenceSignals},
		{label: "publications", query: prunePublicationsSQL, cutoff: cutoff, target: &pruned.Publications},
		{label: "dead publications", query: pruneDeadPublicationsSQL, cutoff: sourceCutoff, target: &pruned.DeadPublications},
		{label: "history events", query: pruneHistoryEventsSQL, cutoff: cutoff, target: &pruned.HistoryEvents},
		{label: "dead history events", query: pruneDeadHistoryEventsSQL, cutoff: sourceCutoff, target: &pruned.DeadHistoryEvents},
		{label: "source observations", query: pruneSourceObservationsSQL, cutoff: sourceCutoff, target: &pruned.SourceObservations},
		{label: "MQTT runtime events", query: pruneMQTTRuntimeEventsSQL, cutoff: sourceCutoff, target: &pruned.MQTTRuntimeEvents},
		{label: "ingest quarantines", query: pruneIngestQuarantinesSQL, cutoff: sourceCutoff, target: &pruned.IngestQuarantines},
		{label: "recovery cursors", query: pruneRecoveryCursorsSQL, cutoff: cutoff, target: &pruned.RecoveryCursors},
		{label: "subscriptions", query: pruneSubscriptionsSQL, cutoff: cutoff, target: &pruned.Subscriptions},
		{label: "replay source positions", query: pruneReplaySourcePositionsSQL, cutoff: sourceCutoff, target: &pruned.ReplayPositions},
	}
	for _, step := range steps {
		deleted, err := store.pruneInBatches(ctx, step.query, step.cutoff)
		if err != nil {
			return PrunedRuntimeHistory{}, fmt.Errorf("prune telemetry %s: %w", step.label, err)
		}
		*step.target = deleted
	}
	return pruned, nil
}

// pruneInBatches repeats a bounded delete until a batch comes back short, so no single
// statement holds its locks for long.
func (store *PostgresStore) pruneInBatches(ctx context.Context, statement string, cutoff time.Time) (int64, error) {
	var total int64
	for {
		deleted, err := store.pruneBatch(ctx, statement, cutoff)
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted < pruneBatchSize {
			return total, nil
		}
	}
}

func (store *PostgresStore) pruneBatch(ctx context.Context, statement string, cutoff time.Time) (int64, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return 0, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	command, err := tx.Exec(ctx, statement, cutoff, pruneBatchSize)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}
