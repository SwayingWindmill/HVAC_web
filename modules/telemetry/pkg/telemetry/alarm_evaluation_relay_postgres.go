package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

func (store *PostgresStore) ClaimPendingAlarmEvaluations(ctx context.Context, workerID string, limit int, now time.Time, leaseDuration time.Duration) ([]PendingPublication, error) {
	if store == nil || store.pool == nil || workerID == "" || leaseDuration <= 0 {
		return nil, ErrAlarmEvaluationRelayUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin Alarm evaluation publication claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT event_id::text, device_id::text, business_revision, payload
FROM telemetry_runtime.telemetry_publication_outbox
WHERE subscription_id IS NULL
  AND alarm_delivery_state = 'PENDING'
  AND alarm_available_at <= $1
  AND (alarm_claim_until IS NULL OR alarm_claim_until <= $1 OR alarm_claim_owner = $2)
ORDER BY alarm_available_at, event_id
FOR UPDATE SKIP LOCKED
LIMIT $3
`, now.UTC(), workerID, limit)
	if err != nil {
		return nil, fmt.Errorf("claim pending Alarm evaluation publications: %w", err)
	}
	publications := make([]PendingPublication, 0, limit)
	eventIDs := make([]string, 0, limit)
	for rows.Next() {
		var eventID, deviceID string
		var revision int64
		var payloadJSON []byte
		if err := rows.Scan(&eventID, &deviceID, &revision, &payloadJSON); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending Alarm evaluation publication: %w", err)
		}
		publication, err := decodeAlarmEvaluationPublication(eventID, deviceID, revision, payloadJSON)
		if err != nil {
			rows.Close()
			return nil, err
		}
		publications = append(publications, publication)
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate pending Alarm evaluation publications: %w", err)
	}
	rows.Close()
	claimUntil := now.UTC().Add(leaseDuration)
	for _, eventID := range eventIDs {
		if _, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_claim_owner=$2, alarm_claim_until=$3
WHERE event_id=$1::uuid AND subscription_id IS NULL AND alarm_delivery_state='PENDING'
`, eventID, workerID, claimUntil); err != nil {
			return nil, fmt.Errorf("lease Alarm evaluation publication: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Alarm evaluation publication claim: %w", err)
	}
	return publications, nil
}

func (store *PostgresStore) MarkAlarmEvaluationDelivered(ctx context.Context, eventID, workerID string, publishedAt time.Time) error {
	if store == nil || store.pool == nil {
		return ErrAlarmEvaluationRelayUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state='PUBLISHED', alarm_published_at=$3, alarm_attempts=alarm_attempts+1,
    alarm_last_error_code=NULL, alarm_claim_owner=NULL, alarm_claim_until=NULL
WHERE event_id=$1::uuid AND subscription_id IS NULL AND alarm_delivery_state='PENDING' AND alarm_claim_owner=$2
`, eventID, workerID, publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("mark Alarm evaluation publication delivered: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAlarmEvaluationRelayUnavailable
	}
	return nil
}

func (store *PostgresStore) MarkAlarmEvaluationRetry(ctx context.Context, eventID, workerID string, availableAt time.Time, errorCode string) error {
	if store == nil || store.pool == nil || errorCode == "" {
		return ErrAlarmEvaluationRelayUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_available_at=$3, alarm_attempts=alarm_attempts+1, alarm_last_error_code=$4,
    alarm_claim_owner=NULL, alarm_claim_until=NULL
WHERE event_id=$1::uuid AND subscription_id IS NULL AND alarm_delivery_state='PENDING' AND alarm_claim_owner=$2
`, eventID, workerID, availableAt.UTC(), errorCode)
	if err != nil {
		return fmt.Errorf("schedule Alarm evaluation publication retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAlarmEvaluationRelayUnavailable
	}
	return nil
}

func decodeAlarmEvaluationPublication(eventID, deviceID string, revision int64, payloadJSON []byte) (PendingPublication, error) {
	var payload struct {
		EventID          string                                 `json:"eventId"`
		DeviceID         string                                 `json:"deviceId"`
		PreviousRevision int64                                  `json:"previousRevision"`
		Revision         int64                                  `json:"revision"`
		EvaluatedAt      telemetryapi.Instant                   `json:"evaluatedAt"`
		Snapshot         telemetryapi.DeviceObservationSnapshot `json:"snapshot"`
		ChangedKeys      []string                               `json:"changedKeys"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return PendingPublication{}, fmt.Errorf("decode pending Alarm evaluation publication: %w", err)
	}
	evaluatedAt, err := time.Parse(time.RFC3339Nano, string(payload.EvaluatedAt))
	if err != nil || payload.EventID != eventID || payload.DeviceID != deviceID || payload.Revision != revision {
		return PendingPublication{}, errors.New("pending Alarm evaluation publication payload is inconsistent")
	}
	if payload.ChangedKeys == nil {
		payload.ChangedKeys = snapshotTelemetryKeys(payload.Snapshot)
	}
	return PendingPublication{
		EventID: eventID, DeviceID: deviceID, PreviousRevision: payload.PreviousRevision,
		Revision: payload.Revision, EvaluatedAt: evaluatedAt.UTC(), Snapshot: payload.Snapshot,
		ChangedKeys: append([]string(nil), payload.ChangedKeys...),
	}, nil
}

var _ AlarmEvaluationRepository = (*PostgresStore)(nil)
