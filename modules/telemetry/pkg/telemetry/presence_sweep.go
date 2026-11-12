package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultPresenceSweepLimit = 256

const duePresenceDevicesQuery = `
WITH candidates AS (
  SELECT
    d.device_id,
    d.presence_applicability,
    dp.applicability AS stored_applicability,
    dp.current_state AS stored_state,
    s.evaluation_availability AS stored_availability,
    CASE
      WHEN d.presence_applicability = 'NOT_APPLICABLE' THEN 'AVAILABLE'
      WHEN oc.device_id IS NULL OR NOT oc.available THEN 'UNAVAILABLE'
      WHEN pp.device_id IS NULL OR pp.policy_revision < 1 OR pp.online_within_seconds <= 0 OR pp.offline_after_seconds <= pp.online_within_seconds THEN 'AVAILABLE'
      WHEN sig.observed_at IS NOT NULL
        AND sig.signal_type <> 'EXPLICIT_DISCONNECT'
        AND sig.observed_at <= ($1::timestamptz - (pp.offline_after_seconds * interval '1 second'))
        AND (oc.continuous_since IS NULL OR oc.continuous_since > ($1::timestamptz - (pp.offline_after_seconds * interval '1 second')))
        THEN 'UNAVAILABLE'
      ELSE 'AVAILABLE'
    END AS expected_availability,
    CASE
      WHEN d.presence_applicability = 'NOT_APPLICABLE' THEN NULL::text
      WHEN oc.device_id IS NULL OR NOT oc.available THEN NULL::text
      WHEN pp.device_id IS NULL OR pp.policy_revision < 1 OR pp.online_within_seconds <= 0 OR pp.offline_after_seconds <= pp.online_within_seconds THEN 'UNKNOWN'
      WHEN sig.observed_at IS NULL THEN 'UNKNOWN'
      WHEN sig.signal_type = 'EXPLICIT_DISCONNECT' THEN 'OFFLINE'
      WHEN sig.observed_at >= ($1::timestamptz - (pp.online_within_seconds * interval '1 second')) THEN 'ONLINE'
      WHEN sig.observed_at <= ($1::timestamptz - (pp.offline_after_seconds * interval '1 second')) THEN
        CASE
          WHEN oc.continuous_since IS NULL OR oc.continuous_since > ($1::timestamptz - (pp.offline_after_seconds * interval '1 second')) THEN NULL::text
          ELSE 'OFFLINE'
        END
      ELSE 'UNKNOWN'
    END AS expected_state
  FROM telemetry_runtime.devices d
  JOIN telemetry_runtime.device_observation_snapshots s ON s.device_id = d.device_id
  LEFT JOIN telemetry_runtime.presence_policies pp ON pp.device_id = d.device_id
  LEFT JOIN telemetry_runtime.observation_coverage oc ON oc.device_id = d.device_id
  LEFT JOIN telemetry_runtime.device_presence dp ON dp.device_id = d.device_id
  LEFT JOIN LATERAL (
    SELECT ps.signal_type, ps.observed_at
    FROM telemetry_runtime.presence_signals ps
    WHERE ps.device_id = d.device_id
      AND ps.accepted
      AND ps.signal_type = ANY(COALESCE(NULLIF(pp.accepted_signal_types, '{}'::text[]), ARRAY['SOURCE_ACTIVITY', 'EXPLICIT_CONNECT']::text[]))
    ORDER BY ps.observed_at DESC, ps.signal_id
    LIMIT 1
  ) sig ON pp.device_id IS NOT NULL
)
SELECT device_id::text
FROM candidates
WHERE stored_applicability IS DISTINCT FROM presence_applicability
   OR stored_availability IS DISTINCT FROM expected_availability
   OR stored_state IS DISTINCT FROM expected_state
ORDER BY device_id
LIMIT $2
`

// SweepDuePresence re-evaluates devices whose stored presence disagrees with the current clock
// or applicability, persisting a new snapshot revision and outbox publication only when state changed.
func (store *PostgresStore) SweepDuePresence(ctx context.Context, evaluatedAt time.Time, limit int) (int, error) {
	evaluatedAt = evaluatedAt.UTC()
	if limit <= 0 {
		limit = defaultPresenceSweepLimit
	}
	rows, err := store.pool.Query(ctx, duePresenceDevicesQuery, evaluatedAt, limit)
	if err != nil {
		return 0, fmt.Errorf("query due telemetry presence devices: %w", err)
	}
	var deviceIDs []string
	for rows.Next() {
		var deviceID string
		if err := rows.Scan(&deviceID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan due telemetry presence device: %w", err)
		}
		deviceIDs = append(deviceIDs, deviceID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate due telemetry presence devices: %w", err)
	}
	rows.Close()

	swept := 0
	for _, deviceID := range deviceIDs {
		changed, err := store.sweepOneDevicePresence(ctx, deviceID, evaluatedAt)
		if err != nil {
			return swept, err
		}
		if changed {
			swept++
		}
	}
	return swept, nil
}

func (store *PostgresStore) sweepOneDevicePresence(ctx context.Context, deviceID string, evaluatedAt time.Time) (bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin telemetry presence sweep transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockDevice(ctx, tx, deviceID); err != nil {
		return false, err
	}
	commit, err := store.evaluateAndPersistDeviceWithOptions(ctx, tx, deviceID, nil, evaluatedAt, true)
	if err != nil {
		return false, err
	}
	if !commit.StateChanged {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit telemetry presence sweep: %w", err)
	}
	return true, nil
}
