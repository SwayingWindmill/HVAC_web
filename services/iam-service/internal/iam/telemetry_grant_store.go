package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const telemetryGrantRuntimeDatabaseRole = "s2_iam_grant_runtime"

type TelemetryRevocationFact struct {
	Sequence             int64  `json:"sequence"`
	PrincipalID          string `json:"principalId"`
	ActingOrganizationID string `json:"actingOrganizationId"`
	SourceType           string `json:"sourceType"`
	SourceID             string `json:"sourceId,omitempty"`
	DeviceID             string `json:"deviceId,omitempty"`
	TelemetryKey         string `json:"telemetryKey,omitempty"`
	Action               string `json:"action,omitempty"`
	PolicyRevision       string `json:"policyRevision"`
	ReasonCode           string `json:"reasonCode"`
	OccurredAt           string `json:"occurredAt"`
}

type PostgresTelemetryGrantStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresTelemetryGrantStore(ctx context.Context, databaseURL string) (*PostgresTelemetryGrantStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("Telemetry grant database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Telemetry grant database configuration: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "iam-telemetry-grant-runtime"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "1s"
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		var role string
		if err := connection.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil {
			return fmt.Errorf("read Telemetry grant database role: %w", err)
		}
		if role != telemetryGrantRuntimeDatabaseRole {
			return fmt.Errorf("Telemetry grant database role %q is not allowed", role)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Telemetry grant store: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Telemetry grant store: %w", err)
	}
	return &PostgresTelemetryGrantStore{pool: pool}, nil
}

func (store *PostgresTelemetryGrantStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresTelemetryGrantStore) ConsumeGrant(ctx context.Context, claims telemetryauth.GrantClaims, now time.Time) (telemetryauth.GrantUseStatus, error) {
	if store == nil || store.pool == nil {
		return telemetryauth.GrantUseStatus{}, errors.New("Telemetry grant store is closed")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return telemetryauth.GrantUseStatus{}, fmt.Errorf("begin Telemetry grant consumption: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := setTelemetryGrantContext(ctx, transaction, claims.ActingOrganizationID); err != nil {
		return telemetryauth.GrantUseStatus{}, err
	}
	var currentPolicy string
	if err := transaction.QueryRow(ctx, `SELECT iam.active_telemetry_policy_revision($1::uuid)`, claims.ActingOrganizationID).Scan(&currentPolicy); err != nil {
		return telemetryauth.GrantUseStatus{}, fmt.Errorf("read current Telemetry policy revision: %w", err)
	}
	var revoked bool
	if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM iam.telemetry_grant_revocations
  WHERE token_id = $1 AND expires_at > $2
)`, claims.TokenID, now).Scan(&revoked); err != nil {
		return telemetryauth.GrantUseStatus{}, fmt.Errorf("read Telemetry grant revocation: %w", err)
	}
	status := telemetryauth.GrantUseStatus{CurrentPolicyRevision: currentPolicy, Revoked: revoked}
	if revoked || currentPolicy == "" || currentPolicy != claims.PolicyRevision {
		if err := transaction.Commit(ctx); err != nil {
			return telemetryauth.GrantUseStatus{}, fmt.Errorf("commit Telemetry grant rejection: %w", err)
		}
		return status, nil
	}
	command, err := transaction.Exec(ctx, `
INSERT INTO iam.telemetry_grant_uses
  (token_id, principal_id, acting_organization_id, scope_digest, consumed_at, expires_at)
VALUES ($1, $2::uuid, $3::uuid, $4, $5, to_timestamp($6))
ON CONFLICT (token_id) DO NOTHING
`, claims.TokenID, claims.PrincipalID, claims.ActingOrganizationID, claims.ScopeDigest, now, claims.ExpiresAt)
	if err != nil {
		return telemetryauth.GrantUseStatus{}, fmt.Errorf("consume Telemetry grant: %w", err)
	}
	status.Replayed = command.RowsAffected() == 0
	if err := transaction.Commit(ctx); err != nil {
		return telemetryauth.GrantUseStatus{}, fmt.Errorf("commit Telemetry grant consumption: %w", err)
	}
	return status, nil
}

func (store *PostgresTelemetryGrantStore) PollRevocations(ctx context.Context, actingOrganizationID string, afterSequence int64, limit int) ([]TelemetryRevocationFact, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("Telemetry grant store is closed")
	}
	if limit <= 0 || limit > 500 {
		return nil, errors.New("Telemetry revocation poll limit is invalid")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin Telemetry revocation poll: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := setTelemetryGrantContext(ctx, transaction, actingOrganizationID); err != nil {
		return nil, err
	}
	rows, err := transaction.Query(ctx, `
SELECT sequence, principal_id::text, acting_organization_id::text, source_type,
       source_id::text, device_id::text, telemetry_key, action, policy_revision, reason_code, occurred_at
FROM iam.telemetry_revocation_facts
WHERE sequence > $1
ORDER BY sequence
LIMIT $2
`, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("query Telemetry revocation facts: %w", err)
	}
	defer rows.Close()
	facts := []TelemetryRevocationFact{}
	for rows.Next() {
		var fact TelemetryRevocationFact
		var sourceID, deviceID, key, action *string
		var occurredAt time.Time
		if err := rows.Scan(&fact.Sequence, &fact.PrincipalID, &fact.ActingOrganizationID, &fact.SourceType, &sourceID, &deviceID, &key, &action, &fact.PolicyRevision, &fact.ReasonCode, &occurredAt); err != nil {
			return nil, fmt.Errorf("scan Telemetry revocation fact: %w", err)
		}
		if sourceID != nil {
			fact.SourceID = *sourceID
		}
		if deviceID != nil {
			fact.DeviceID = *deviceID
		}
		if key != nil {
			fact.TelemetryKey = *key
		}
		if action != nil {
			fact.Action = *action
		}
		fact.OccurredAt = formatInstant(occurredAt)
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Telemetry revocation facts: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Telemetry revocation poll: %w", err)
	}
	return facts, nil
}

func setTelemetryGrantContext(ctx context.Context, transaction pgx.Tx, actingOrganizationID string) error {
	var configured string
	if err := transaction.QueryRow(ctx, `SELECT set_config('app.acting_organization_id', $1, true)`, actingOrganizationID).Scan(&configured); err != nil {
		return fmt.Errorf("set Telemetry grant RLS context: %w", err)
	}
	return nil
}
