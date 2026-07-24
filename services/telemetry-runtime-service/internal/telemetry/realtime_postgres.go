package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

func (store *PostgresStore) SaveSubscriptions(ctx context.Context, subscriptions []RealtimeSubscription) error {
	if store == nil || store.pool == nil {
		return ErrRealtimeUnavailable
	}
	if len(subscriptions) == 0 || len(subscriptions) > MaximumRealtimeSubscriptions {
		return ErrSubscriptionConflict
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin realtime subscription transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	for _, subscription := range subscriptions {
		keys, err := json.Marshal(subscription.Keys)
		if err != nil {
			return ErrSubscriptionConflict
		}
		command, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.telemetry_subscriptions (
  subscription_id, client_subscription_id, principal_id, subject, subject_issuer,
  session_id, acting_organization_id, device_id, keys, scope_sha256,
  policy_revision, policy_revision_ref, channel, status, expires_at,
  revoked_at, created_at, updated_at
) VALUES (
  $1, $2, $3::uuid, $4, $5, $6, $7::uuid, $8::uuid, $9::jsonb, $10,
  $11, $12, $13, 'ACTIVE', $14, NULL, $15, $15
)
ON CONFLICT (subscription_id) DO UPDATE SET
  expires_at = EXCLUDED.expires_at,
  status = 'ACTIVE',
  revoked_at = NULL,
  updated_at = EXCLUDED.updated_at,
  policy_revision = EXCLUDED.policy_revision,
  policy_revision_ref = EXCLUDED.policy_revision_ref
WHERE telemetry_runtime.telemetry_subscriptions.principal_id = EXCLUDED.principal_id
  AND telemetry_runtime.telemetry_subscriptions.subject = EXCLUDED.subject
  AND telemetry_runtime.telemetry_subscriptions.subject_issuer = EXCLUDED.subject_issuer
  AND telemetry_runtime.telemetry_subscriptions.session_id = EXCLUDED.session_id
  AND telemetry_runtime.telemetry_subscriptions.acting_organization_id = EXCLUDED.acting_organization_id
  AND telemetry_runtime.telemetry_subscriptions.device_id = EXCLUDED.device_id
  AND telemetry_runtime.telemetry_subscriptions.keys = EXCLUDED.keys
  AND telemetry_runtime.telemetry_subscriptions.scope_sha256 = EXCLUDED.scope_sha256
  AND telemetry_runtime.telemetry_subscriptions.channel = EXCLUDED.channel
`, subscription.SubscriptionID, subscription.ClientSubscriptionID, subscription.PrincipalID,
			subscription.Subject, subscription.SubjectIssuer, subscription.SessionID,
			subscription.ActingOrganizationID, subscription.DeviceID, keys, subscription.ScopeDigest,
			policyRevisionOrdinal(subscription.PolicyRevision), subscription.PolicyRevision, subscription.Channel,
			subscription.ExpiresAt, subscription.CreatedAt)
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" {
				return ErrSubscriptionConflict
			}
			return fmt.Errorf("persist realtime subscription: %w", err)
		}
		if command.RowsAffected() != 1 {
			return ErrSubscriptionConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit realtime subscriptions: %w", err)
	}
	return nil
}

func (store *PostgresStore) ActiveSubscription(ctx context.Context, subscriptionID string, now time.Time) (RealtimeSubscription, error) {
	if store == nil || store.pool == nil {
		return RealtimeSubscription{}, ErrRealtimeUnavailable
	}
	row := store.pool.QueryRow(ctx, `
SELECT subscription_id, client_subscription_id, principal_id::text, subject, subject_issuer,
       session_id, acting_organization_id::text, device_id::text, keys,
       scope_sha256, policy_revision_ref, channel, status,
       expires_at, revoked_at, created_at, updated_at
FROM telemetry_runtime.telemetry_subscriptions
WHERE subscription_id = $1 AND status = 'ACTIVE' AND expires_at > $2
`, subscriptionID, now)
	return scanRealtimeSubscription(row)
}

func (store *PostgresStore) ActiveSubscriptionByChannel(ctx context.Context, principalID, channel string, now time.Time) (RealtimeSubscription, error) {
	if store == nil || store.pool == nil {
		return RealtimeSubscription{}, ErrRealtimeUnavailable
	}
	row := store.pool.QueryRow(ctx, `
SELECT subscription_id, client_subscription_id, principal_id::text, subject, subject_issuer,
       session_id, acting_organization_id::text, device_id::text, keys,
       scope_sha256, policy_revision_ref, channel, status,
       expires_at, revoked_at, created_at, updated_at
FROM telemetry_runtime.telemetry_subscriptions
WHERE principal_id = $1::uuid AND channel = $2 AND status = 'ACTIVE' AND expires_at > $3
  AND EXISTS (
    SELECT 1 FROM telemetry_runtime.registry_device_bindings binding
    WHERE binding.device_id = telemetry_subscriptions.device_id
      AND binding.owning_organization_id = telemetry_subscriptions.acting_organization_id
      AND binding.binding_status = 'ACTIVE' AND binding.valid_to IS NULL
  )
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(
      CASE WHEN jsonb_array_length(telemetry_subscriptions.keys) = 0
           THEN '[""]'::jsonb ELSE telemetry_subscriptions.keys END
    ) selected(key)
    WHERE NOT EXISTS (
      SELECT 1 FROM telemetry_runtime.iam_scope_projections projection
      WHERE projection.principal_id = telemetry_subscriptions.principal_id
        AND projection.acting_organization_id = telemetry_subscriptions.acting_organization_id
        AND projection.device_id = telemetry_subscriptions.device_id
        AND projection.action = 'SUBSCRIBE' AND projection.decision = 'ALLOW'
        AND projection.revoked_at IS NULL AND projection.valid_until > $3
        AND projection.telemetry_key IS NOT DISTINCT FROM NULLIF(selected.key, '')
    )
  )
  AND NOT EXISTS (
    SELECT 1 FROM telemetry_runtime.iam_scope_projections denial
    WHERE denial.principal_id = telemetry_subscriptions.principal_id
      AND denial.acting_organization_id = telemetry_subscriptions.acting_organization_id
      AND denial.device_id = telemetry_subscriptions.device_id
      AND denial.action = 'SUBSCRIBE' AND denial.decision = 'DENY'
      AND denial.revoked_at IS NULL AND denial.valid_until > $3
      AND (denial.telemetry_key IS NULL OR telemetry_subscriptions.keys ? denial.telemetry_key)
  )
`, principalID, channel, now)
	return scanRealtimeSubscription(row)
}

func (store *PostgresStore) ActiveSubscriptionsForDevice(ctx context.Context, deviceID string, now time.Time) ([]RealtimeSubscription, error) {
	if store == nil || store.pool == nil {
		return nil, ErrRealtimeUnavailable
	}
	rows, err := store.pool.Query(ctx, `
SELECT subscription_id, client_subscription_id, principal_id::text, subject, subject_issuer,
       session_id, acting_organization_id::text, device_id::text, keys,
       scope_sha256, policy_revision_ref, channel, status,
       expires_at, revoked_at, created_at, updated_at
FROM telemetry_runtime.telemetry_subscriptions
WHERE device_id = $1::uuid AND status = 'ACTIVE' AND expires_at > $2
  AND EXISTS (
    SELECT 1 FROM telemetry_runtime.registry_device_bindings binding
    WHERE binding.device_id = telemetry_subscriptions.device_id
      AND binding.owning_organization_id = telemetry_subscriptions.acting_organization_id
      AND binding.binding_status = 'ACTIVE' AND binding.valid_to IS NULL
  )
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(
      CASE WHEN jsonb_array_length(telemetry_subscriptions.keys) = 0
           THEN '[""]'::jsonb ELSE telemetry_subscriptions.keys END
    ) selected(key)
    WHERE NOT EXISTS (
      SELECT 1 FROM telemetry_runtime.iam_scope_projections projection
      WHERE projection.principal_id = telemetry_subscriptions.principal_id
        AND projection.acting_organization_id = telemetry_subscriptions.acting_organization_id
        AND projection.device_id = telemetry_subscriptions.device_id
        AND projection.action = 'SUBSCRIBE' AND projection.decision = 'ALLOW'
        AND projection.revoked_at IS NULL AND projection.valid_until > $2
        AND projection.telemetry_key IS NOT DISTINCT FROM NULLIF(selected.key, '')
    )
  )
  AND NOT EXISTS (
    SELECT 1 FROM telemetry_runtime.iam_scope_projections denial
    WHERE denial.principal_id = telemetry_subscriptions.principal_id
      AND denial.acting_organization_id = telemetry_subscriptions.acting_organization_id
      AND denial.device_id = telemetry_subscriptions.device_id
      AND denial.action = 'SUBSCRIBE' AND denial.decision = 'DENY'
      AND denial.revoked_at IS NULL AND denial.valid_until > $2
      AND (denial.telemetry_key IS NULL OR telemetry_subscriptions.keys ? denial.telemetry_key)
  )
ORDER BY subscription_id
`, deviceID, now)
	if err != nil {
		return nil, fmt.Errorf("query active realtime subscriptions: %w", err)
	}
	defer rows.Close()
	result := make([]RealtimeSubscription, 0)
	for rows.Next() {
		subscription, err := scanRealtimeSubscription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, subscription)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active realtime subscriptions: %w", err)
	}
	return result, nil
}

type subscriptionScanner interface {
	Scan(...any) error
}

func scanRealtimeSubscription(scanner subscriptionScanner) (RealtimeSubscription, error) {
	var subscription RealtimeSubscription
	var keysJSON []byte
	var status string
	if err := scanner.Scan(
		&subscription.SubscriptionID, &subscription.ClientSubscriptionID,
		&subscription.PrincipalID, &subscription.Subject, &subscription.SubjectIssuer,
		&subscription.SessionID, &subscription.ActingOrganizationID, &subscription.DeviceID,
		&keysJSON, &subscription.ScopeDigest, &subscription.PolicyRevision, &subscription.Channel,
		&status, &subscription.ExpiresAt, &subscription.RevokedAt, &subscription.CreatedAt, &subscription.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RealtimeSubscription{}, ErrSubscriptionNotFound
		}
		return RealtimeSubscription{}, fmt.Errorf("scan realtime subscription: %w", err)
	}
	if err := json.Unmarshal(keysJSON, &subscription.Keys); err != nil {
		return RealtimeSubscription{}, fmt.Errorf("decode realtime subscription keys: %w", err)
	}
	subscription.Status = SubscriptionStatus(status)
	return subscription, nil
}

func (store *PostgresStore) SaveRecoveryCursors(ctx context.Context, cursors []RecoveryCursorRecord) error {
	if store == nil || store.pool == nil {
		return ErrRealtimeUnavailable
	}
	if len(cursors) == 0 || len(cursors) > MaximumRecoveryCheckpoints {
		return ErrSubscriptionConflict
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin recovery cursor transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	for _, cursor := range cursors {
		command, err := tx.Exec(ctx, `
INSERT INTO telemetry_runtime.recovery_cursors (
  cursor_id, subscription_id, business_revision, transport_epoch, transport_offset,
  scope_sha256, cursor_sha256, expires_at, revoked_at, created_at
)
SELECT $1::uuid, s.subscription_id, $3, $4, $5, $6, $7, $8, NULL, $9
FROM telemetry_runtime.telemetry_subscriptions s
WHERE s.subscription_id = $2 AND s.principal_id = $10::uuid
  AND s.status = 'ACTIVE' AND s.expires_at > $9
`, cursor.CursorID, cursor.SubscriptionID, cursor.BusinessRevision, cursor.TransportEpoch,
			cursor.TransportOffset, cursor.ScopeDigest, cursor.CursorSHA256, cursor.ExpiresAt,
			cursor.CreatedAt, cursor.PrincipalID)
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" {
				return ErrSubscriptionConflict
			}
			return fmt.Errorf("persist recovery cursor: %w", err)
		}
		if command.RowsAffected() != 1 {
			return ErrSubscriptionNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit recovery cursors: %w", err)
	}
	return nil
}

func (store *PostgresStore) ActiveRecoveryCursor(ctx context.Context, cursorSHA256, subscriptionID string, now time.Time) (RecoveryCursorRecord, error) {
	if store == nil || store.pool == nil {
		return RecoveryCursorRecord{}, ErrRealtimeUnavailable
	}
	var cursor RecoveryCursorRecord
	err := store.pool.QueryRow(ctx, `
SELECT cursor_id::text, subscription_id, business_revision, transport_epoch,
       transport_offset, scope_sha256, cursor_sha256, expires_at, revoked_at, created_at
FROM telemetry_runtime.recovery_cursors
WHERE cursor_sha256 = $1 AND subscription_id = $2 AND revoked_at IS NULL AND expires_at > $3
`, cursorSHA256, subscriptionID, now).Scan(
		&cursor.CursorID, &cursor.SubscriptionID, &cursor.BusinessRevision, &cursor.TransportEpoch,
		&cursor.TransportOffset, &cursor.ScopeDigest, &cursor.CursorSHA256, &cursor.ExpiresAt,
		&cursor.RevokedAt, &cursor.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecoveryCursorRecord{}, ErrRecoveryCursorRejected
	}
	if err != nil {
		return RecoveryCursorRecord{}, fmt.Errorf("read active recovery cursor: %w", err)
	}
	return cursor, nil
}

func (store *PostgresStore) CurrentBusinessRevision(ctx context.Context, deviceID string) (int64, error) {
	if store == nil || store.pool == nil {
		return 0, ErrRealtimeUnavailable
	}
	var revision int64
	if err := store.pool.QueryRow(ctx, `
SELECT business_revision
FROM telemetry_runtime.device_observation_snapshots
WHERE device_id = $1::uuid
`, deviceID).Scan(&revision); errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrSubscriptionNotFound
	} else if err != nil {
		return 0, fmt.Errorf("read current telemetry business revision: %w", err)
	}
	return revision, nil
}

func (store *PostgresStore) PendingPublications(ctx context.Context, limit int, now time.Time) ([]PendingPublication, error) {
	if store == nil || store.pool == nil {
		return nil, ErrRealtimeUnavailable
	}
	rows, err := store.pool.Query(ctx, `
SELECT event_id::text, device_id::text, business_revision, payload
FROM telemetry_runtime.telemetry_publication_outbox
WHERE delivery_state = 'PENDING' AND available_at <= $1
ORDER BY available_at, event_id
LIMIT $2
`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending telemetry publications: %w", err)
	}
	defer rows.Close()
	result := make([]PendingPublication, 0, limit)
	for rows.Next() {
		var eventID, deviceID string
		var revision int64
		var payloadJSON []byte
		if err := rows.Scan(&eventID, &deviceID, &revision, &payloadJSON); err != nil {
			return nil, fmt.Errorf("scan pending telemetry publication: %w", err)
		}
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
			return nil, fmt.Errorf("decode pending telemetry publication: %w", err)
		}
		evaluatedAt, err := time.Parse(time.RFC3339Nano, string(payload.EvaluatedAt))
		if err != nil || payload.EventID != eventID || payload.DeviceID != deviceID || payload.Revision != revision {
			return nil, errors.New("pending telemetry publication payload is inconsistent")
		}
		if payload.ChangedKeys == nil {
			payload.ChangedKeys = snapshotTelemetryKeys(payload.Snapshot)
		}
		result = append(result, PendingPublication{
			EventID: eventID, DeviceID: deviceID, PreviousRevision: payload.PreviousRevision,
			Revision: payload.Revision, EvaluatedAt: evaluatedAt.UTC(), Snapshot: payload.Snapshot,
			ChangedKeys: append([]string(nil), payload.ChangedKeys...),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending telemetry publications: %w", err)
	}
	return result, nil
}

func (store *PostgresStore) MarkPublicationPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	if store == nil || store.pool == nil {
		return ErrRealtimeUnavailable
	}
	command, err := store.pool.Exec(ctx, `
UPDATE telemetry_runtime.telemetry_publication_outbox
SET delivery_state = 'PUBLISHED', published_at = $2, attempts = attempts + 1, last_error_code = NULL
WHERE event_id = $1::uuid AND delivery_state = 'PENDING'
`, eventID, publishedAt)
	if err != nil {
		return fmt.Errorf("mark telemetry publication delivered: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrPublicationNotFound
	}
	return nil
}

func (store *PostgresStore) RevokeSubscriptions(ctx context.Context, principalID, deviceID string, now time.Time) ([]RealtimeSubscription, error) {
	if store == nil || store.pool == nil {
		return nil, ErrRealtimeUnavailable
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin realtime revocation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s2_telemetry_runtime`); err != nil {
		return nil, fmt.Errorf("activate telemetry runtime database identity: %w", err)
	}
	var principalArgument any
	if principalID != "" {
		principalArgument = principalID
	}
	var deviceArgument any
	if deviceID != "" {
		deviceArgument = deviceID
	}
	rows, err := tx.Query(ctx, `
UPDATE telemetry_runtime.telemetry_subscriptions
SET status = 'REVOKED', revoked_at = $3, updated_at = $3
WHERE status = 'ACTIVE'
  AND ($1::uuid IS NULL OR principal_id = $1::uuid)
  AND ($2::uuid IS NULL OR device_id = $2::uuid)
RETURNING subscription_id, client_subscription_id, principal_id::text, subject, subject_issuer,
          session_id, acting_organization_id::text, device_id::text, keys,
          scope_sha256, policy_revision_ref, channel, status,
          expires_at, revoked_at, created_at, updated_at
`, principalArgument, deviceArgument, now)
	if err != nil {
		return nil, fmt.Errorf("revoke realtime subscriptions: %w", err)
	}
	revoked := make([]RealtimeSubscription, 0)
	for rows.Next() {
		subscription, err := scanRealtimeSubscription(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		revoked = append(revoked, subscription)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate revoked realtime subscriptions: %w", err)
	}
	rows.Close()
	for _, subscription := range revoked {
		if _, err := tx.Exec(ctx, `
UPDATE telemetry_runtime.recovery_cursors
SET revoked_at = $2
WHERE subscription_id = $1 AND revoked_at IS NULL
`, subscription.SubscriptionID, now); err != nil {
			return nil, fmt.Errorf("revoke realtime recovery cursors: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit realtime revocation: %w", err)
	}
	return revoked, nil
}

func snapshotTelemetryKeys(snapshot telemetryapi.DeviceObservationSnapshot) []string {
	keys := make([]string, 0, len(snapshot.Values))
	for _, value := range snapshot.Values {
		if key := telemetryStateKey(value); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

var _ RealtimeRepository = (*PostgresStore)(nil)
