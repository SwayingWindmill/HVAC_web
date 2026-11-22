package telemetry

import (
	"context"
	"fmt"
	"time"
)

func (store *PostgresStore) ClaimPendingEdgeAlarmEvents(
	ctx context.Context,
	workerID string,
	limit int,
	now time.Time,
	leaseDuration time.Duration,
) ([]PendingEdgeAlarmEvent, error) {
	if store == nil || store.pool == nil || workerID == "" || leaseDuration <= 0 {
		return nil, ErrEdgeAlarmEventRelayUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin edge Alarm event claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
SELECT message_id::text, tenant_id::text, site_id::text, gateway_id, event_type,
       source_type, source_id, event_time, severity, source_sequence, data, received_at, alarm_attempts
FROM telemetry_runtime.mqtt_runtime_events
WHERE alarm_delivery_state = 'PENDING'
  AND alarm_available_at <= $1
  AND (alarm_claim_until IS NULL OR alarm_claim_until <= $1 OR alarm_claim_owner = $2)
ORDER BY event_time ASC, source_sequence ASC, received_at ASC, message_id ASC
FOR UPDATE SKIP LOCKED
LIMIT $3
`, now.UTC(), workerID, limit)
	if err != nil {
		return nil, fmt.Errorf("claim pending edge Alarm events: %w", err)
	}
	events := make([]PendingEdgeAlarmEvent, 0, limit)
	messageIDs := make([]string, 0, limit)
	for rows.Next() {
		var ev PendingEdgeAlarmEvent
		var rawData []byte
		if err := rows.Scan(
			&ev.MessageID, &ev.TenantID, &ev.SiteID, &ev.GatewayID, &ev.EventType,
			&ev.SourceType, &ev.SourceID, &ev.EventTime, &ev.Severity, &ev.SourceSequence, &rawData, &ev.ReceivedAt, &ev.AlarmAttempts,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending edge Alarm event: %w", err)
		}
		ev.Data = append([]byte(nil), rawData...)
		events = append(events, ev)
		messageIDs = append(messageIDs, ev.MessageID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate pending edge Alarm events: %w", err)
	}
	rows.Close()

	claimUntil := now.UTC().Add(leaseDuration)
	for _, messageID := range messageIDs {
		if _, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.mqtt_runtime_events
SET alarm_claim_owner = $2, alarm_claim_until = $3
WHERE message_id = $1::uuid AND alarm_delivery_state = 'PENDING'
`, messageID, workerID, claimUntil); err != nil {
			return nil, fmt.Errorf("lease edge Alarm event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit edge Alarm event claim: %w", err)
	}
	return events, nil
}

func (store *PostgresStore) MarkEdgeAlarmEventDelivered(ctx context.Context, messageID, workerID string, publishedAt time.Time) error {
	if store == nil || store.pool == nil || workerID == "" {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.mqtt_runtime_events
SET alarm_delivery_state = 'PUBLISHED',
    alarm_published_at = $3,
    alarm_attempts = alarm_attempts + 1,
    alarm_last_error_code = NULL,
    alarm_claim_owner = NULL,
    alarm_claim_until = NULL
WHERE message_id = $1::uuid AND alarm_delivery_state = 'PENDING' AND alarm_claim_owner = $2
`, messageID, workerID, publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("mark edge Alarm event delivered: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	return nil
}

func (store *PostgresStore) MarkEdgeAlarmEventRetry(ctx context.Context, messageID, workerID string, availableAt time.Time, errorCode string) error {
	if store == nil || store.pool == nil || workerID == "" || errorCode == "" {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.mqtt_runtime_events
SET alarm_available_at = $3,
    alarm_attempts = alarm_attempts + 1,
    alarm_last_error_code = $4,
    alarm_claim_owner = NULL,
    alarm_claim_until = NULL
WHERE message_id = $1::uuid AND alarm_delivery_state = 'PENDING' AND alarm_claim_owner = $2
`, messageID, workerID, availableAt.UTC(), errorCode)
	if err != nil {
		return fmt.Errorf("schedule edge Alarm event retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	return nil
}

func (store *PostgresStore) MarkEdgeAlarmEventDeadLetter(ctx context.Context, messageID, workerID string, deadLetteredAt time.Time, errorCode string) error {
	if store == nil || store.pool == nil || workerID == "" || errorCode == "" {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.mqtt_runtime_events
SET alarm_delivery_state = 'DEAD_LETTER',
    alarm_dead_lettered_at = $3,
    alarm_published_at = NULL,
    alarm_attempts = alarm_attempts + 1,
    alarm_last_error_code = $4,
    alarm_claim_owner = NULL,
    alarm_claim_until = NULL
WHERE message_id = $1::uuid AND alarm_delivery_state = 'PENDING' AND alarm_claim_owner = $2
`, messageID, workerID, deadLetteredAt.UTC(), errorCode)
	if err != nil {
		return fmt.Errorf("dead-letter edge Alarm event: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrEdgeAlarmEventRelayUnavailable
	}
	return nil
}

func (store *PostgresStore) CountEdgeAlarmEventOutboxStates(ctx context.Context) (int64, int64, error) {
	if store == nil || store.pool == nil {
		return 0, 0, ErrEdgeAlarmEventRelayUnavailable
	}
	var pending, deadLetter int64
	err := store.pool.QueryRow(ctx, `
SELECT
  COUNT(*) FILTER (WHERE alarm_delivery_state = 'PENDING')::bigint,
  COUNT(*) FILTER (WHERE alarm_delivery_state = 'DEAD_LETTER')::bigint
FROM telemetry_runtime.mqtt_runtime_events
WHERE alarm_delivery_state IS NOT NULL
`).Scan(&pending, &deadLetter)
	if err != nil {
		return 0, 0, fmt.Errorf("count edge Alarm event outbox states: %w", err)
	}
	return pending, deadLetter, nil
}

var (
	_ EdgeAlarmEventRepository         = (*PostgresStore)(nil)
	_ EdgeAlarmEventOutboxStateCounter = (*PostgresStore)(nil)
)
