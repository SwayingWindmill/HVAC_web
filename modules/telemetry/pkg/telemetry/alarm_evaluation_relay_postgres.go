package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
)

const (
	defaultAlarmDeadLetterLimit = 50
	maxAlarmDeadLetterLimit     = 200
)

type AlarmEvaluationDeadLetterFilter struct {
	TenantID string
	SiteID   string
	DeviceID string
	Limit    int
}

type AlarmEvaluationDeadLetter struct {
	EventID          string `json:"eventId"`
	TenantID         string `json:"tenantId"`
	SiteID           string `json:"siteId"`
	DeviceID         string `json:"deviceId"`
	BusinessRevision int64  `json:"businessRevision"`
	Attempts         int    `json:"attempts"`
	LastErrorCode    string `json:"lastErrorCode,omitempty"`
	DeadLetteredAt   string `json:"deadLetteredAt"`
	CreatedAt        string `json:"createdAt"`
}

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
SELECT event_id::text, device_id::text, business_revision, alarm_attempts, payload
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
	var corruptIDs []string
	for rows.Next() {
		var eventID, deviceID string
		var revision int64
		var alarmAttempts int
		var payloadJSON []byte
		if err := rows.Scan(&eventID, &deviceID, &revision, &alarmAttempts, &payloadJSON); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending Alarm evaluation publication: %w", err)
		}
		publication, err := decodeAlarmEvaluationPublication(eventID, deviceID, revision, alarmAttempts, payloadJSON)
		if err != nil {
			// Isolate malformed outbox rows in DEAD_LETTER so one bad payload cannot stall the queue.
			corruptIDs = append(corruptIDs, eventID)
			continue
		}
		publications = append(publications, publication)
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate pending Alarm evaluation publications: %w", err)
	}
	rows.Close()
	for _, corruptID := range corruptIDs {
		if _, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state='DEAD_LETTER', alarm_dead_lettered_at=$2, alarm_published_at=NULL,
    alarm_attempts=alarm_attempts+1, alarm_last_error_code='ALARM_EVALUATION_PAYLOAD_INVALID',
    alarm_claim_owner=NULL, alarm_claim_until=NULL
WHERE event_id=$1::uuid AND subscription_id IS NULL AND alarm_delivery_state='PENDING'
`, corruptID, now.UTC()); err != nil {
			return nil, fmt.Errorf("dead-letter invalid Alarm evaluation publication: %w", err)
		}
	}
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
	if store.pipelineMetrics != nil {
		for range corruptIDs {
			store.pipelineMetrics.AddAlarmEvaluationDeadLetter("payload_invalid")
		}
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

func (store *PostgresStore) MarkAlarmEvaluationDeadLetter(ctx context.Context, eventID, workerID string, deadLetteredAt time.Time, errorCode string) error {
	if store == nil || store.pool == nil || workerID == "" || errorCode == "" {
		return ErrAlarmEvaluationRelayUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state='DEAD_LETTER', alarm_dead_lettered_at=$3, alarm_published_at=NULL,
    alarm_attempts=alarm_attempts+1, alarm_last_error_code=$4,
    alarm_claim_owner=NULL, alarm_claim_until=NULL
WHERE event_id=$1::uuid AND subscription_id IS NULL AND alarm_delivery_state='PENDING' AND alarm_claim_owner=$2
`, eventID, workerID, deadLetteredAt.UTC(), errorCode)
	if err != nil {
		return fmt.Errorf("dead-letter Alarm evaluation publication: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAlarmEvaluationRelayUnavailable
	}
	return nil
}

func decodeAlarmEvaluationPublication(eventID, deviceID string, revision int64, alarmAttempts int, payloadJSON []byte) (PendingPublication, error) {
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
		Revision: payload.Revision, AlarmAttempts: alarmAttempts, EvaluatedAt: evaluatedAt.UTC(), Snapshot: payload.Snapshot,
		ChangedKeys: append([]string(nil), payload.ChangedKeys...),
	}, nil
}

func (store *PostgresStore) CountAlarmEvaluationOutboxStates(ctx context.Context) (int64, int64, error) {
	if store == nil || store.pool == nil {
		return 0, 0, ErrAlarmEvaluationRelayUnavailable
	}
	var pending, deadLetter int64
	err := store.pool.QueryRow(ctx, `
SELECT
  COUNT(*) FILTER (WHERE alarm_delivery_state = 'PENDING')::bigint,
  COUNT(*) FILTER (WHERE alarm_delivery_state = 'DEAD_LETTER')::bigint
FROM telemetry_runtime.telemetry_publication_outbox
WHERE subscription_id IS NULL
`).Scan(&pending, &deadLetter)
	if err != nil {
		return 0, 0, fmt.Errorf("count Alarm evaluation outbox states: %w", err)
	}
	return pending, deadLetter, nil
}

func (store *PostgresStore) ListAlarmEvaluationDeadLetters(ctx context.Context, filter AlarmEvaluationDeadLetterFilter) ([]AlarmEvaluationDeadLetter, error) {
	if store == nil || store.pool == nil {
		return nil, ErrAlarmEvaluationRelayUnavailable
	}
	tenantFilter, err := optionalUUIDv7Param(filter.TenantID, "tenantId")
	if err != nil {
		return nil, err
	}
	siteFilter, err := optionalUUIDv7Param(filter.SiteID, "siteId")
	if err != nil {
		return nil, err
	}
	deviceFilter, err := optionalUUIDv7Param(filter.DeviceID, "deviceId")
	if err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultAlarmDeadLetterLimit
	}
	if limit > maxAlarmDeadLetterLimit {
		limit = maxAlarmDeadLetterLimit
	}
	rows, err := store.pool.Query(ctx, `
SELECT outbox.event_id::text, dev.tenant_id::text, dev.site_id::text, outbox.device_id::text,
       outbox.business_revision, outbox.alarm_attempts, COALESCE(outbox.alarm_last_error_code, ''),
       outbox.alarm_dead_lettered_at, outbox.created_at
FROM telemetry_runtime.telemetry_publication_outbox AS outbox
JOIN telemetry_runtime.devices AS dev
  ON dev.device_id = outbox.device_id
WHERE outbox.subscription_id IS NULL
  AND outbox.alarm_delivery_state = 'DEAD_LETTER'
  AND ($1::uuid IS NULL OR dev.tenant_id = $1::uuid)
  AND ($2::uuid IS NULL OR dev.site_id = $2::uuid)
  AND ($3::uuid IS NULL OR outbox.device_id = $3::uuid)
ORDER BY outbox.alarm_dead_lettered_at DESC, outbox.event_id DESC
LIMIT $4
`, tenantFilter, siteFilter, deviceFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("list Alarm evaluation dead letters: %w", err)
	}
	defer rows.Close()

	items := make([]AlarmEvaluationDeadLetter, 0)
	for rows.Next() {
		var item AlarmEvaluationDeadLetter
		var deadLetteredAt, createdAt time.Time
		if err := rows.Scan(
			&item.EventID, &item.TenantID, &item.SiteID, &item.DeviceID,
			&item.BusinessRevision, &item.Attempts, &item.LastErrorCode,
			&deadLetteredAt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan Alarm evaluation dead letter: %w", err)
		}
		item.DeadLetteredAt = deadLetteredAt.UTC().Format(time.RFC3339Nano)
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Alarm evaluation dead letters: %w", err)
	}
	return items, nil
}

func (store *PostgresStore) ReplayAlarmEvaluationDeadLetter(ctx context.Context, eventID string, now time.Time) error {
	if store == nil || store.pool == nil {
		return ErrAlarmEvaluationRelayUnavailable
	}
	normalized := strings.ToLower(strings.TrimSpace(eventID))
	if !uuidv7.IsValid(normalized) {
		return fmt.Errorf("eventId %q must be UUIDv7", eventID)
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET alarm_delivery_state = 'PENDING',
    alarm_attempts = 0,
    alarm_available_at = $2,
    alarm_dead_lettered_at = NULL,
    alarm_published_at = NULL,
    alarm_last_error_code = NULL,
    alarm_claim_owner = NULL,
    alarm_claim_until = NULL
WHERE event_id = $1::uuid
  AND subscription_id IS NULL
  AND alarm_delivery_state = 'DEAD_LETTER'
`, normalized, now.UTC())
	if err != nil {
		return fmt.Errorf("replay Alarm evaluation dead letter: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAlarmEvaluationDeadLetterNotFound
	}
	return nil
}

func optionalUUIDv7Param(raw, field string) (*string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return nil, nil
	}
	if !uuidv7.IsValid(trimmed) {
		return nil, fmt.Errorf("%s %q must be UUIDv7", field, raw)
	}
	return &trimmed, nil
}

var (
	_ AlarmEvaluationRepository         = (*PostgresStore)(nil)
	_ AlarmEvaluationOutboxStateCounter = (*PostgresStore)(nil)
)
