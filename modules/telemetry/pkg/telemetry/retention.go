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
}

// PruneRuntimeHistory deletes presence signals older than the retention window that a
// newer accepted signal of the same type supersedes, and publications older than the
// window whose every delivery finished. The last signal of each type of a Device stays,
// so presence still reports when a long-offline Device was last seen; dead publications
// stay as failure evidence. History events go an hour after ClickHouse has them, and received
// observations a week old once their event is gone; an undelivered event keeps its observation.
func (store *PostgresStore) PruneRuntimeHistory(ctx context.Context, now time.Time) (PrunedRuntimeHistory, error) {
	if store == nil || store.pool == nil {
		return PrunedRuntimeHistory{}, errors.New("telemetry runtime store is closed")
	}
	cutoff := now.UTC().Add(-RuntimeHistoryRetention)
	signals, err := store.pruneInBatches(ctx, `
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
)`, cutoff)
	if err != nil {
		return PrunedRuntimeHistory{}, fmt.Errorf("prune telemetry presence signals: %w", err)
	}
	publications, err := store.pruneInBatches(ctx, `
DELETE FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id IN (
  SELECT event_id
  FROM telemetry_runtime.telemetry_publication_outbox
  WHERE created_at < $1
    AND delivery_state = 'PUBLISHED'
    AND latest_cache_state IN ('NOT_APPLICABLE', 'MATERIALIZED')
    AND (alarm_delivery_state IS NULL OR alarm_delivery_state = 'PUBLISHED')
  LIMIT $2
)`, cutoff)
	if err != nil {
		return PrunedRuntimeHistory{}, fmt.Errorf("prune telemetry publications: %w", err)
	}
	historyEvents, err := store.pruneInBatches(ctx, `
DELETE FROM telemetry_runtime.telemetry_history_outbox
WHERE event_id IN (
  SELECT event_id
  FROM telemetry_runtime.telemetry_history_outbox
  WHERE delivery_state = 'PUBLISHED' AND published_at < $1
  LIMIT $2
)`, cutoff)
	if err != nil {
		return PrunedRuntimeHistory{}, fmt.Errorf("prune telemetry history events: %w", err)
	}
	observations, err := store.pruneInBatches(ctx, `
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
)`, now.UTC().Add(-SourceObservationRetention))
	if err != nil {
		return PrunedRuntimeHistory{}, fmt.Errorf("prune telemetry source observations: %w", err)
	}
	return PrunedRuntimeHistory{PresenceSignals: signals, Publications: publications, HistoryEvents: historyEvents, SourceObservations: observations}, nil
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
