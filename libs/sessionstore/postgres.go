package sessionstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
)

type PostgresConfig struct {
	IDGenerator     IDGenerator
	FailureInjector FailureInjector
}

type PostgresStore struct {
	pool    *pgxpool.Pool
	ids     IDGenerator
	failure FailureInjector
}

func OpenPostgres(ctx context.Context, dsn string, config PostgresConfig) (*PostgresStore, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolConfig.MaxConns = 8
	poolConfig.MinConns = 1
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return NewPostgresStore(pool, config), nil
}

func NewPostgresStore(pool *pgxpool.Pool, config PostgresConfig) *PostgresStore {
	ids := config.IDGenerator
	if ids == nil {
		ids = randomID
	}
	return &PostgresStore{pool: pool, ids: ids, failure: config.FailureInjector}
}

func (store *PostgresStore) Close() {
	store.pool.Close()
}

func (store *PostgresStore) CreateSession(ctx context.Context, session Session, mutation MutationContext) (Session, error) {
	ctx, span := observability.Start(ctx, "postgres.session.transaction", observability.SpanKindClient, map[string]any{"db.system": "postgresql", "db.operation": "session.create"})
	defer span.End()
	messageID := store.ids()
	session.AggregateVersion = 1
	session.LastAuditMessageID = messageID
	session.CreatedAt = mutation.OccurredAt.UTC()
	session.UpdatedAt = mutation.OccurredAt.UTC()
	event, payload, err := buildEvent(session, mutation, messageID, "ACTIVE")
	if err != nil {
		return Session{}, err
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	roles, err := json.Marshal(session.Principal.Roles)
	if err != nil {
		return Session{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO gateway.sessions (
			session_id, principal_subject, principal_issuer, display_name, email, roles,
			tenant_id, csrf_token_ciphertext, provider_tokens_ciphertext,
			expires_at, revoked_at, aggregate_version, last_audit_message_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULL,$11,$12,$13,$13)
	`, session.ID, session.Principal.Subject, session.Principal.Issuer, session.Principal.DisplayName,
		session.Principal.Email, roles, session.TenantID, session.CSRFTokenCiphertext,
		session.ProviderTokensCiphertext, session.ExpiresAt.UTC(), session.AggregateVersion,
		session.LastAuditMessageID, session.CreatedAt)
	if err != nil {
		return Session{}, mapWriteError(err)
	}
	if err := store.inject(FailureAfterStateWrite); err != nil {
		return Session{}, err
	}
	if err := insertAuditIntent(ctx, tx, session, mutation, event); err != nil {
		return Session{}, err
	}
	if err := store.inject(FailureAfterAuditIntent); err != nil {
		return Session{}, err
	}
	if err := insertOutbox(ctx, tx, event, payload); err != nil {
		return Session{}, err
	}
	if err := store.inject(FailureBeforeCommit); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return cloneSession(session), nil
}

func (store *PostgresStore) GetSession(ctx context.Context, sessionID string) (Session, error) {
	ctx, span := observability.Start(ctx, "postgres.session.query", observability.SpanKindClient, map[string]any{"db.system": "postgresql", "db.operation": "session.get"})
	defer span.End()
	return scanSession(store.pool.QueryRow(ctx, sessionSelect+` WHERE session_id = $1`, sessionID))
}

func (store *PostgresStore) RevokeSession(ctx context.Context, sessionID string, mutation MutationContext) (Session, error) {
	ctx, span := observability.Start(ctx, "postgres.session.transaction", observability.SpanKindClient, map[string]any{"db.system": "postgresql", "db.operation": "session.revoke"})
	defer span.End()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	session, err := scanSession(tx.QueryRow(ctx, sessionSelect+` WHERE session_id = $1 FOR UPDATE`, sessionID))
	if err != nil {
		return Session{}, err
	}
	if session.RevokedAt != nil {
		return Session{}, ErrSessionRevoked
	}
	if mutation.CausationID == "" {
		mutation.CausationID = session.LastAuditMessageID
	}
	messageID := store.ids()
	revokedAt := mutation.OccurredAt.UTC()
	session.RevokedAt = &revokedAt
	session.AggregateVersion++
	session.LastAuditMessageID = messageID
	session.UpdatedAt = revokedAt
	event, payload, err := buildEvent(session, mutation, messageID, "REVOKED")
	if err != nil {
		return Session{}, err
	}

	command, err := tx.Exec(ctx, `
		UPDATE gateway.sessions
		SET revoked_at = $2, aggregate_version = $3, last_audit_message_id = $4, updated_at = $2
		WHERE session_id = $1 AND revoked_at IS NULL
	`, session.ID, revokedAt, session.AggregateVersion, messageID)
	if err != nil {
		return Session{}, err
	}
	if command.RowsAffected() != 1 {
		return Session{}, ErrSessionConflict
	}
	if err := store.inject(FailureAfterStateWrite); err != nil {
		return Session{}, err
	}
	if err := insertAuditIntent(ctx, tx, session, mutation, event); err != nil {
		return Session{}, err
	}
	if err := store.inject(FailureAfterAuditIntent); err != nil {
		return Session{}, err
	}
	if err := insertOutbox(ctx, tx, event, payload); err != nil {
		return Session{}, err
	}
	if err := store.inject(FailureBeforeCommit); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return cloneSession(session), nil
}

func (store *PostgresStore) inject(point FailurePoint) error {
	if store.failure == nil {
		return nil
	}
	return store.failure(point)
}

func insertAuditIntent(ctx context.Context, tx pgx.Tx, session Session, mutation MutationContext, event sessionevent.SessionAuditEventV1) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO gateway.audit_intents (
			message_id, session_aggregate_id, tenant_id, aggregate_version,
			initiating_subject, initiating_issuer, executing_service, executing_spiffe_id,
			action, result, policy_revision, correlation_id, causation_id, trace_id,
			traceparent, payload_sha256, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	`, event.MessageID, event.AggregateID, session.TenantID, session.AggregateVersion,
		session.Principal.Subject, session.Principal.Issuer, mutation.ExecutingService,
		mutation.ExecutingSPIFFEID, mutation.Action, event.Result, mutation.PolicyRevision,
		mutation.CorrelationID, event.CausationID, mutation.TraceID, event.Traceparent,
		event.PayloadSHA256, mutation.OccurredAt.UTC())
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, event sessionevent.SessionAuditEventV1, payload []byte) error {
	digest := sha256.Sum256(payload)
	_, err := tx.Exec(ctx, `
		INSERT INTO gateway.outbox (
			message_id, topic, partition_key, schema_version, aggregate_type, aggregate_id,
			aggregate_version, tenant_id, correlation_id, causation_id, trace_id,
			traceparent, payload, envelope_sha256, created_at, available_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)
	`, event.MessageID, sessionevent.ControlTopic, event.PartitionKey, event.SchemaVersion,
		event.AggregateType, event.AggregateID, event.AggregateVersion, event.TenantID,
		event.CorrelationID, event.CausationID, event.TraceID, event.Traceparent,
		payload, hex.EncodeToString(digest[:]), time.UnixMilli(event.OccurredAtUnixMS).UTC())
	return err
}

const sessionSelect = `
	SELECT session_id, principal_subject, principal_issuer, display_name, email, roles,
	       tenant_id, csrf_token_ciphertext, provider_tokens_ciphertext,
	       expires_at, revoked_at, aggregate_version, last_audit_message_id, created_at, updated_at
	FROM gateway.sessions`

type rowScanner interface {
	Scan(...any) error
}

func scanSession(row rowScanner) (Session, error) {
	var session Session
	var roles []byte
	var revokedAt sql.NullTime
	err := row.Scan(
		&session.ID, &session.Principal.Subject, &session.Principal.Issuer,
		&session.Principal.DisplayName, &session.Principal.Email, &roles,
		&session.TenantID, &session.CSRFTokenCiphertext,
		&session.ProviderTokensCiphertext, &session.ExpiresAt, &revokedAt,
		&session.AggregateVersion, &session.LastAuditMessageID, &session.CreatedAt,
		&session.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if err := json.Unmarshal(roles, &session.Principal.Roles); err != nil {
		return Session{}, fmt.Errorf("decode session roles: %w", err)
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		session.RevokedAt = &value
	}
	return cloneSession(session), nil
}

func mapWriteError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return ErrSessionConflict
	}
	return err
}
