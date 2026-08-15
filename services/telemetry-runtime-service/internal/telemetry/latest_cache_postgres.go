package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

func (store *PostgresStore) LatestCacheRebuildBatch(ctx context.Context, afterDeviceID string, limit int) ([]telemetryapi.DeviceObservationSnapshot, error) {
	if store == nil || store.pool == nil {
		return nil, ErrLatestCacheUnavailable
	}
	if limit <= 0 || limit > 512 {
		limit = 256
	}
	var after any
	if afterDeviceID != "" {
		after = afterDeviceID
	}
	rows, err := store.pool.Query(ctx, `
SELECT snapshot
FROM telemetry_runtime.device_observation_snapshots
WHERE ($1::uuid IS NULL OR device_id > $1::uuid)
ORDER BY device_id
LIMIT $2
`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query latest-cache rebuild snapshots: %w", err)
	}
	defer rows.Close()
	result := make([]telemetryapi.DeviceObservationSnapshot, 0, limit)
	for rows.Next() {
		var snapshotJSON []byte
		if err := rows.Scan(&snapshotJSON); err != nil {
			return nil, fmt.Errorf("scan latest-cache rebuild snapshot: %w", err)
		}
		var snapshot telemetryapi.DeviceObservationSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return nil, fmt.Errorf("decode latest-cache rebuild snapshot: %w", err)
		}
		if err := validateLatestCacheSnapshot(snapshot); err != nil {
			return nil, fmt.Errorf("validate latest-cache rebuild snapshot: %w", err)
		}
		result = append(result, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest-cache rebuild snapshots: %w", err)
	}
	return result, nil
}

func (store *PostgresStore) PendingLatestCacheMaterializations(ctx context.Context, limit int, now time.Time) ([]LatestCacheMaterialization, error) {
	if store == nil || store.pool == nil {
		return nil, ErrLatestCacheUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	rows, err := store.pool.Query(ctx, `
SELECT event_id::text, device_id::text, business_revision, latest_cache_attempts, payload
FROM telemetry_runtime.telemetry_publication_outbox
WHERE latest_cache_state = 'PENDING' AND latest_cache_available_at <= $1
ORDER BY latest_cache_available_at, event_id
LIMIT $2
`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending latest-cache materializations: %w", err)
	}
	defer rows.Close()

	result := make([]LatestCacheMaterialization, 0, limit)
	for rows.Next() {
		var item LatestCacheMaterialization
		var payloadJSON []byte
		if err := rows.Scan(&item.EventID, &item.DeviceID, &item.Revision, &item.Attempts, &payloadJSON); err != nil {
			return nil, fmt.Errorf("scan latest-cache materialization: %w", err)
		}
		var payload struct {
			EventID  string                                 `json:"eventId"`
			DeviceID string                                 `json:"deviceId"`
			Revision int64                                  `json:"revision"`
			Snapshot telemetryapi.DeviceObservationSnapshot `json:"snapshot"`
		}
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return nil, fmt.Errorf("decode latest-cache outbox payload: %w", err)
		}
		if payload.EventID != item.EventID || payload.DeviceID != item.DeviceID || payload.Revision != item.Revision || string(payload.Snapshot.DeviceId) != item.DeviceID || int64(payload.Snapshot.BusinessRevision) != item.Revision {
			return nil, errors.New("latest-cache outbox payload is inconsistent")
		}
		if err := validateLatestCacheSnapshot(payload.Snapshot); err != nil {
			return nil, fmt.Errorf("validate latest-cache outbox snapshot: %w", err)
		}
		item.Snapshot = payload.Snapshot
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest-cache materializations: %w", err)
	}
	return result, nil
}

func (store *PostgresStore) MarkLatestCacheMaterialized(ctx context.Context, eventID string, materializedAt time.Time) error {
	if store == nil || store.pool == nil {
		return ErrLatestCacheUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET latest_cache_state = 'MATERIALIZED',
    latest_cache_materialized_at = $2,
    latest_cache_attempts = latest_cache_attempts + 1,
    latest_cache_last_error_code = NULL
WHERE event_id = $1::uuid AND latest_cache_state = 'PENDING'
`, eventID, materializedAt)
	if err != nil {
		return fmt.Errorf("mark latest-cache outbox materialized: %w", err)
	}
	if command.RowsAffected() == 1 {
		return nil
	}
	var state string
	if err := store.pool.QueryRow(ctx, `
SELECT latest_cache_state
FROM telemetry_runtime.telemetry_publication_outbox
WHERE event_id = $1::uuid
`, eventID).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPublicationNotFound
		}
		return fmt.Errorf("read latest-cache outbox state: %w", err)
	}
	if state == "MATERIALIZED" {
		return nil
	}
	return ErrPublicationNotFound
}

func (store *PostgresStore) MarkLatestCacheFailed(ctx context.Context, eventID string, nextAvailableAt time.Time, errorCode string) error {
	if store == nil || store.pool == nil {
		return ErrLatestCacheUnavailable
	}
	if errorCode == "" {
		errorCode = "REDIS_WRITE_FAILED"
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET latest_cache_attempts = latest_cache_attempts + 1,
    latest_cache_available_at = $2,
    latest_cache_last_error_code = $3
WHERE event_id = $1::uuid AND latest_cache_state = 'PENDING'
`, eventID, nextAvailableAt, errorCode)
	if err != nil {
		return fmt.Errorf("record latest-cache outbox failure: %w", err)
	}
	if command.RowsAffected() == 1 {
		return nil
	}
	var state string
	if err := store.pool.QueryRow(ctx, `SELECT latest_cache_state FROM telemetry_runtime.telemetry_publication_outbox WHERE event_id = $1::uuid`, eventID).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPublicationNotFound
		}
		return fmt.Errorf("read latest-cache outbox after failure: %w", err)
	}
	if state == "MATERIALIZED" {
		return nil
	}
	return ErrPublicationNotFound
}
