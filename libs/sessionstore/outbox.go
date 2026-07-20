package sessionstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoPendingOutbox = errors.New("no pending outbox message")

type OutboxRecord struct {
	MessageID        string
	Topic            string
	PartitionKey     string
	SchemaVersion    uint32
	AggregateType    string
	AggregateID      string
	AggregateVersion uint64
	OrganizationID   string
	Payload          []byte
	EnvelopeSHA256   string
	CreatedAt        time.Time
	PublishAttempts  int
}

type OutboxStore struct {
	pool *pgxpool.Pool
}

func OpenOutbox(ctx context.Context, dsn string) (*OutboxStore, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 4
	config.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &OutboxStore{pool: pool}, nil
}

func (store *OutboxStore) Close() {
	store.pool.Close()
}

func (store *OutboxStore) ClaimPending(ctx context.Context, owner string, now time.Time, lease time.Duration) (OutboxRecord, error) {
	row := store.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT message_id
			FROM gateway.outbox
			WHERE published_at IS NULL
			  AND available_at <= $2
			  AND (claim_expires_at IS NULL OR claim_expires_at <= $2)
			ORDER BY created_at, message_id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE gateway.outbox AS outbox
		SET claim_owner = $1,
		    claim_expires_at = $3,
		    publish_attempts = publish_attempts + 1,
		    last_error_code = ''
		FROM candidate
		WHERE outbox.message_id = candidate.message_id
		RETURNING outbox.message_id, outbox.topic, outbox.partition_key,
		          outbox.schema_version, outbox.aggregate_type, outbox.aggregate_id,
		          outbox.aggregate_version, outbox.organization_id, outbox.payload,
		          outbox.envelope_sha256, outbox.created_at, outbox.publish_attempts
	`, owner, now.UTC(), now.UTC().Add(lease))
	record, err := scanOutbox(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxRecord{}, ErrNoPendingOutbox
	}
	if err != nil {
		return OutboxRecord{}, err
	}
	if !record.ValidEnvelope() {
		return OutboxRecord{}, errors.New("outbox envelope hash mismatch")
	}
	return record, nil
}

func (store *OutboxStore) MarkPublished(ctx context.Context, messageID, owner string, publishedAt time.Time) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE gateway.outbox
		SET published_at = $3, claim_owner = '', claim_expires_at = NULL, last_error_code = ''
		WHERE message_id = $1 AND claim_owner = $2 AND published_at IS NULL
	`, messageID, owner, publishedAt.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrSessionConflict
	}
	return nil
}

func (store *OutboxStore) MarkFailed(ctx context.Context, messageID, owner, errorCode string, availableAt time.Time) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE gateway.outbox
		SET available_at = $4, claim_owner = '', claim_expires_at = NULL, last_error_code = $3
		WHERE message_id = $1 AND claim_owner = $2 AND published_at IS NULL
	`, messageID, owner, errorCode, availableAt.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrSessionConflict
	}
	return nil
}

func (store *OutboxStore) PendingCount(ctx context.Context) (int, error) {
	var count int
	err := store.pool.QueryRow(ctx, `SELECT count(*) FROM gateway.outbox WHERE published_at IS NULL`).Scan(&count)
	return count, err
}

func (store *OutboxStore) Get(ctx context.Context, messageID string) (OutboxRecord, error) {
	record, err := scanOutbox(store.pool.QueryRow(ctx, `
		SELECT message_id, topic, partition_key, schema_version, aggregate_type,
		       aggregate_id, aggregate_version, organization_id, payload,
		       envelope_sha256, created_at, publish_attempts
		FROM gateway.outbox WHERE message_id = $1
	`, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxRecord{}, ErrSessionNotFound
	}
	return record, err
}

func (record OutboxRecord) ValidEnvelope() bool {
	digest := sha256.Sum256(record.Payload)
	return hex.EncodeToString(digest[:]) == record.EnvelopeSHA256
}

func scanOutbox(row rowScanner) (OutboxRecord, error) {
	var record OutboxRecord
	err := row.Scan(
		&record.MessageID, &record.Topic, &record.PartitionKey, &record.SchemaVersion,
		&record.AggregateType, &record.AggregateID, &record.AggregateVersion,
		&record.OrganizationID, &record.Payload, &record.EnvelopeSHA256,
		&record.CreatedAt, &record.PublishAttempts,
	)
	return record, err
}
