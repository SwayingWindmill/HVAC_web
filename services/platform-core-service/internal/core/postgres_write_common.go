package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type mutationRecord[T any] struct {
	Result           T
	SiteID           *string
	ResourceType     string
	ResourceID       string
	BeforeRevision   *int64
	AfterRevision    *int64
	Outcome          string
	EventType        string
	AggregateVersion int64
	Payload          map[string]any
}

func (store *PostgresStore) withWriteTransaction(ctx context.Context, claims registryauth.GrantClaims, action registryauth.Action, fn func(pgx.Tx) error) error {
	if store == nil || store.pool == nil || !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) || len(claims.Actions) != 1 || claims.Actions[0] != action {
		return ErrInvalidMutation
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable, AccessMode: pgx.ReadWrite})
	if err != nil {
		return fmt.Errorf("begin Registry write transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s1_core_writer`); err != nil {
		return fmt.Errorf("activate Registry writer role: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, claims.TenantID); err != nil {
		return fmt.Errorf("bind Registry writer Tenant: %w", err)
	}
	siteIDs := append([]string(nil), claims.AllowedSiteIDs...)
	if action.TenantScoped() {
		rows, err := tx.Query(ctx, `SELECT id::text FROM core_registry.sites WHERE tenant_id = $1::uuid ORDER BY id`, claims.TenantID)
		if err != nil {
			return fmt.Errorf("resolve Tenant-scoped Registry sites: %w", err)
		}
		for rows.Next() {
			var siteID string
			if err := rows.Scan(&siteID); err != nil {
				rows.Close()
				return fmt.Errorf("scan Tenant-scoped Registry site: %w", err)
			}
			siteIDs = append(siteIDs, siteID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate Tenant-scoped Registry sites: %w", err)
		}
		rows.Close()
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.authorized_site_ids', $1, true)`, postgresUUIDArray(siteIDs)); err != nil {
		return fmt.Errorf("bind Registry writer Site scope: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapRegistryWriteError(fmt.Errorf("commit Registry write transaction: %w", err))
	}
	return nil
}

func runRegistryMutation[T any](
	ctx context.Context,
	store *PostgresStore,
	claims registryauth.GrantClaims,
	action registryauth.Action,
	idempotencyKey string,
	reason string,
	request any,
	execute func(pgx.Tx, time.Time) (mutationRecord[T], error),
) (T, bool, error) {
	var zero T
	requestBytes, err := json.Marshal(struct {
		Action  registryauth.Action `json:"action"`
		Request any                 `json:"request"`
	}{Action: action, Request: request})
	if err != nil {
		return zero, false, ErrInvalidMutation
	}
	digestBytes := sha256.Sum256(requestBytes)
	digest := hex.EncodeToString(digestBytes[:])
	var result T
	var replayed bool
	err = store.withWriteTransaction(ctx, claims, action, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, claims.TenantID+"|"+idempotencyKey); err != nil {
			return fmt.Errorf("lock Registry idempotency key: %w", err)
		}
		var storedAction string
		var storedDigest string
		var storedResponse []byte
		err := tx.QueryRow(ctx, `
SELECT action, request_digest, response
FROM core_registry.registry_write_requests
WHERE tenant_id = $1::uuid AND idempotency_key = $2
`, claims.TenantID, idempotencyKey).Scan(&storedAction, &storedDigest, &storedResponse)
		if err == nil {
			if storedAction != string(action) || storedDigest != digest || json.Unmarshal(storedResponse, &result) != nil {
				return ErrIdempotencyConflict
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read Registry idempotency record: %w", err)
		}

		now := time.Now().UTC()
		record, err := execute(tx, now)
		if err != nil {
			return mapRegistryWriteError(err)
		}
		if !validUUIDv7(record.ResourceID) || record.AggregateVersion < 1 || record.ResourceType == "" || record.EventType == "" {
			return ErrInvalidMutation
		}
		result = record.Result
		responseBytes, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode Registry mutation result: %w", err)
		}
		var siteID any
		if record.SiteID != nil {
			siteID = *record.SiteID
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.registry_write_requests (
  tenant_id, site_id, idempotency_key, action, request_digest, response, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::jsonb, $7)
`, claims.TenantID, siteID, idempotencyKey, string(action), digest, responseBytes, now); err != nil {
			return fmt.Errorf("insert Registry idempotency record: %w", err)
		}
		auditID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		outboxID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		outcome := record.Outcome
		if outcome == "" {
			outcome = "COMMITTED"
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.registry_audit_facts (
  event_id, tenant_id, site_id, principal_id, action, resource_type, resource_id,
  before_revision, after_revision, outcome, reason, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::uuid, $8, $9, $10, $11, $12)
`, auditID, claims.TenantID, siteID, claims.PrincipalID, string(action), record.ResourceType, record.ResourceID,
			record.BeforeRevision, record.AfterRevision, outcome, reason, now); err != nil {
			return fmt.Errorf("insert Registry audit fact: %w", err)
		}
		payload := record.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		payload["principalId"] = claims.PrincipalID
		payload["policyRevision"] = claims.PolicyRevision
		payload["reason"] = reason
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode Registry outbox payload: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.domain_outbox_events (
  id, tenant_id, site_id, event_type, schema_version, subject_type, subject_id,
  aggregate_type, aggregate_id, aggregate_version, occurred_at, payload, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 1, $5, $6::uuid, $5, $6::uuid, $7, $8, $9::jsonb, $8)
`, outboxID, claims.TenantID, siteID, record.EventType, record.ResourceType, record.ResourceID, record.AggregateVersion, now, payloadBytes); err != nil {
			return fmt.Errorf("insert Registry outbox event: %w", err)
		}
		return nil
	})
	if err != nil {
		return zero, false, err
	}
	return result, replayed, nil
}

func newCoreUUIDv7(now time.Time) (string, error) {
	var value [16]byte
	milliseconds := uint64(now.UTC().UnixMilli())
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	if _, err := rand.Read(value[6:]); err != nil {
		return "", fmt.Errorf("generate Registry UUIDv7 entropy: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func mapRegistryWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrBindingConflict) ||
		errors.Is(err, ErrTemplateImmutable) || errors.Is(err, ErrImportPlanInvalid) || errors.Is(err, ErrResourceDependencies) || errors.Is(err, ErrInvalidMutation) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return ErrNotFound
		case "23505", "23P01":
			return ErrBindingConflict
		case "23514":
			if pgErr.ConstraintName == "registry_template_revision_immutable" || pgErr.Message == "released TemplateRevision is immutable" {
				return ErrTemplateImmutable
			}
			return ErrBindingConflict
		case "40001":
			return ErrRevisionConflict
		}
	}
	return err
}

func revisionPointer(value int64) *int64 {
	copyValue := value
	return &copyValue
}
