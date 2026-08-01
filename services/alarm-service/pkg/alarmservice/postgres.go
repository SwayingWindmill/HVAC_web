package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Alarm database URL: %w", err)
	}
	if config.ConnConfig.User != "s4_alarm_service" {
		return nil, errors.New("Alarm database identity must be s4_alarm_service")
	}
	config.MaxConns = 16
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s4_alarm_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Alarm database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Alarm database: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) List(ctx context.Context, organizationID, siteID string, filter Filter) (alarmmodel.ListResponse, error) {
	if store == nil || store.pool == nil {
		return alarmmodel.ListResponse{}, ErrUnavailable
	}
	tx, err := store.beginOrganizationTransaction(ctx, organizationID, true)
	if err != nil {
		return alarmmodel.ListResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var cursorOccurredAt *time.Time
	if filter.Cursor != "" {
		var instant time.Time
		err := tx.QueryRow(ctx, `
			SELECT last_occurred_at
			FROM alarm_runtime.alarm_current
			WHERE organization_id = $1 AND site_id = $2 AND alarm_id = $3
		`, organizationID, siteID, filter.Cursor).Scan(&instant)
		if errors.Is(err, pgx.ErrNoRows) {
			return alarmmodel.ListResponse{}, ErrNotFound
		}
		if err != nil {
			return alarmmodel.ListResponse{}, fmt.Errorf("read Alarm cursor: %w", err)
		}
		cursorOccurredAt = &instant
	}
	rows, err := tx.Query(ctx, `
		SELECT alarm_id, organization_id, site_id, device_id, source_type, source_reference,
		       title, summary, severity, status, assignee_id, suppressed_until, occurrence_count,
		       first_occurred_at, last_occurred_at, evidence, transitions, version, created_at, updated_at
		FROM alarm_runtime.alarm_current
		WHERE organization_id = $1
		  AND site_id = $2
		  AND ($3 = '' OR status = $3)
		  AND ($4 = '' OR severity = $4)
		  AND ($5::timestamptz IS NULL OR last_occurred_at < $5 OR (last_occurred_at = $5 AND alarm_id > $6::uuid))
		ORDER BY last_occurred_at DESC, alarm_id ASC
		LIMIT $7
	`, organizationID, siteID, string(filter.Status), string(filter.Severity), cursorOccurredAt, nullableCursor(filter.Cursor), limit+1)
	if err != nil {
		return alarmmodel.ListResponse{}, fmt.Errorf("list Alarms: %w", err)
	}
	defer rows.Close()
	items := make([]alarmmodel.Alarm, 0, limit+1)
	for rows.Next() {
		alarm, err := scanAlarm(rows)
		if err != nil {
			return alarmmodel.ListResponse{}, err
		}
		items = append(items, alarm)
	}
	if err := rows.Err(); err != nil {
		return alarmmodel.ListResponse{}, fmt.Errorf("iterate Alarms: %w", err)
	}
	response := alarmmodel.ListResponse{SchemaVersion: alarmmodel.SchemaVersion, Items: items}
	if len(response.Items) > limit {
		cursor := response.Items[limit-1].AlarmID
		response.Items = response.Items[:limit]
		response.NextCursor = &cursor
		response.HasMore = true
	}
	if err := tx.Commit(ctx); err != nil {
		return alarmmodel.ListResponse{}, fmt.Errorf("commit Alarm list read: %w", err)
	}
	return response, nil
}

func (store *PostgresStore) Get(ctx context.Context, organizationID, siteID, alarmID string) (alarmmodel.Alarm, error) {
	if store == nil || store.pool == nil {
		return alarmmodel.Alarm{}, ErrUnavailable
	}
	tx, err := store.beginOrganizationTransaction(ctx, organizationID, true)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	alarm, err := getAlarmRow(ctx, tx, organizationID, siteID, alarmID, false)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("commit Alarm detail read: %w", err)
	}
	return alarm, nil
}

func (store *PostgresStore) Apply(ctx context.Context, organizationID, siteID, alarmID string, mutation Mutation) (MutationResult, error) {
	if store == nil || store.pool == nil {
		return MutationResult{}, ErrUnavailable
	}
	digest, err := mutationDigest(mutation)
	if err != nil {
		return MutationResult{}, alarmmodel.ErrInvalidOperation
	}
	tx, err := store.beginOrganizationTransaction(ctx, organizationID, false)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := getAlarmRow(ctx, tx, organizationID, siteID, alarmID, true)
	if err != nil {
		return MutationResult{}, err
	}
	var storedDigest string
	var responseJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT request_digest, response
		FROM alarm_runtime.alarm_idempotency
		WHERE organization_id = $1 AND site_id = $2 AND alarm_id = $3 AND idempotency_key = $4
	`, organizationID, siteID, alarmID, mutation.IdempotencyKey).Scan(&storedDigest, &responseJSON)
	if err == nil {
		if storedDigest != digest {
			return MutationResult{}, ErrIdempotencyConflict
		}
		var replay alarmmodel.Alarm
		if json.Unmarshal(responseJSON, &replay) != nil || replay.Validate() != nil || replay.OrganizationID != organizationID || replay.SiteID != siteID || replay.AlarmID != alarmID {
			return MutationResult{}, ErrUnavailable
		}
		if err := tx.Commit(ctx); err != nil {
			return MutationResult{}, fmt.Errorf("commit Alarm idempotency replay: %w", err)
		}
		return MutationResult{Alarm: replay, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, fmt.Errorf("read Alarm idempotency record: %w", err)
	}

	updated, err := alarmmodel.ApplyOperation(current, mutation.operationInput())
	if err != nil {
		return MutationResult{}, err
	}
	evidenceJSON, err := json.Marshal(updated.Evidence)
	if err != nil {
		return MutationResult{}, fmt.Errorf("encode Alarm evidence: %w", err)
	}
	transitionsJSON, err := json.Marshal(updated.Transitions)
	if err != nil {
		return MutationResult{}, fmt.Errorf("encode Alarm transitions: %w", err)
	}
	command, err := tx.Exec(ctx, `
		UPDATE alarm_runtime.alarm_current
		SET status = $5, assignee_id = $6, suppressed_until = $7, evidence = $8,
		    transitions = $9, version = $10, updated_at = $11
		WHERE organization_id = $1 AND site_id = $2 AND alarm_id = $3 AND version = $4
	`, organizationID, siteID, alarmID, current.Version, string(updated.Status), updated.AssigneeID, updated.SuppressedUntil,
		evidenceJSON, transitionsJSON, updated.Version, updated.UpdatedAt)
	if err != nil {
		return MutationResult{}, fmt.Errorf("update Alarm projection: %w", err)
	}
	if command.RowsAffected() != 1 {
		return MutationResult{}, alarmmodel.ErrVersionConflict
	}
	responseJSON, err = json.Marshal(updated)
	if err != nil {
		return MutationResult{}, fmt.Errorf("encode Alarm mutation response: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_idempotency (
			organization_id, site_id, alarm_id, idempotency_key, request_digest, response, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, organizationID, siteID, alarmID, mutation.IdempotencyKey, digest, responseJSON, mutation.OccurredAt)
	if err != nil {
		return MutationResult{}, fmt.Errorf("write Alarm idempotency record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, fmt.Errorf("commit Alarm mutation: %w", err)
	}
	return MutationResult{Alarm: updated}, nil
}

func (store *PostgresStore) beginOrganizationTransaction(ctx context.Context, organizationID string, readOnly bool) (pgx.Tx, error) {
	options := pgx.TxOptions{}
	if readOnly {
		options.AccessMode = pgx.ReadOnly
	}
	tx, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("begin Alarm transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("activate Alarm Organization scope: %w", err)
	}
	return tx, nil
}

func getAlarmRow(ctx context.Context, tx pgx.Tx, organizationID, siteID, alarmID string, lock bool) (alarmmodel.Alarm, error) {
	query := `
		SELECT alarm_id, organization_id, site_id, device_id, source_type, source_reference,
		       title, summary, severity, status, assignee_id, suppressed_until, occurrence_count,
		       first_occurred_at, last_occurred_at, evidence, transitions, version, created_at, updated_at
		FROM alarm_runtime.alarm_current
		WHERE organization_id = $1 AND site_id = $2 AND alarm_id = $3`
	if lock {
		query += ` FOR UPDATE`
	}
	alarm, err := scanAlarm(tx.QueryRow(ctx, query, organizationID, siteID, alarmID))
	if errors.Is(err, pgx.ErrNoRows) {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	return alarm, nil
}

type alarmScanner interface{ Scan(...any) error }

func scanAlarm(scanner alarmScanner) (alarmmodel.Alarm, error) {
	var alarm alarmmodel.Alarm
	var evidenceJSON, transitionsJSON []byte
	var firstOccurredAt, lastOccurredAt, createdAt, updatedAt time.Time
	var suppressedUntil *time.Time
	if err := scanner.Scan(
		&alarm.AlarmID, &alarm.OrganizationID, &alarm.SiteID, &alarm.DeviceID,
		&alarm.SourceType, &alarm.SourceReference, &alarm.Title, &alarm.Summary,
		&alarm.Severity, &alarm.Status, &alarm.AssigneeID, &suppressedUntil, &alarm.OccurrenceCount,
		&firstOccurredAt, &lastOccurredAt, &evidenceJSON, &transitionsJSON, &alarm.Version, &createdAt, &updatedAt,
	); err != nil {
		return alarmmodel.Alarm{}, err
	}
	alarm.SchemaVersion = alarmmodel.SchemaVersion
	alarm.FirstOccurredAt = firstOccurredAt.UTC().Format(time.RFC3339Nano)
	alarm.LastOccurredAt = lastOccurredAt.UTC().Format(time.RFC3339Nano)
	alarm.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	alarm.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	if suppressedUntil != nil {
		value := suppressedUntil.UTC().Format(time.RFC3339Nano)
		alarm.SuppressedUntil = &value
	}
	if err := json.Unmarshal(evidenceJSON, &alarm.Evidence); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("decode Alarm evidence: %w", err)
	}
	if err := json.Unmarshal(transitionsJSON, &alarm.Transitions); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("decode Alarm transitions: %w", err)
	}
	if err := alarm.Validate(); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("validate stored Alarm: %w", err)
	}
	return alarm, nil
}

func nullableCursor(value string) any {
	if value == "" {
		return nil
	}
	return value
}
