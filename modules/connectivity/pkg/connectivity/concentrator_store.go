package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

type ConcentratorAuditEntry struct {
	ID         string         `json:"id"`
	UnitID     int            `json:"unitId"`
	ActorID    string         `json:"actorId"`
	Operation  string         `json:"operation"`
	RequestID  string         `json:"requestId"`
	Outcome    string         `json:"outcome"`
	Reason     string         `json:"reason"`
	Diff       map[string]any `json:"diff"`
	OccurredAt time.Time      `json:"occurredAt"`
}

func (store *Store) Clock() time.Time {
	return store.clock().UTC()
}

func NewUUIDv7At(now time.Time) (string, error) {
	return uuidv7.NewAt(now)
}

// ResolveGatewayScopeForOperator validates tenant/site access and non-revoked
// gateway enrollment without requiring an active mTLS certificate, enabling
// pre-enrollment cloud config shadow staging.
func (store *Store) ResolveGatewayScopeForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (string, error) {
	tenantID, _, err := store.resolveGatewayTenantAndSiteForOperator(ctx, claims, gatewayID)
	return tenantID, err
}

func (store *Store) resolveGatewayTenantAndSiteForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (string, string, error) {
	if store == nil || store.pool == nil {
		return "", "", errors.New("connectivity store is closed")
	}
	if !uuidv7.IsValid(gatewayID) {
		return "", "", ErrEnrollmentRejected
	}
	var tenantID, siteID string
	err := store.pool.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND status = 'ACTIVE'
`, gatewayID).Scan(&tenantID, &siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("read Registry gateway directory: %w", err)
	}
	if tenantID != claims.TenantID || !registryauth.ScopeAllows(claims, siteID) {
		return "", "", ErrCredentialForbidden
	}
	var revoked bool
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM connectivity.gateway_enrollments
  WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND revoked_at IS NOT NULL
)`, tenantID, gatewayID).Scan(&revoked)
	}); err != nil {
		return "", "", fmt.Errorf("check Gateway enrollment state: %w", err)
	}
	if revoked {
		return "", "", ErrCredentialRevoked
	}
	return tenantID, siteID, nil
}

// ResolveGatewayForOperator additionally requires the gateway to hold an active,
// unexpired mTLS certificate before permitting live concentrator RPC operations.
func (store *Store) ResolveGatewayForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (string, error) {
	tenantID, err := store.ResolveGatewayScopeForOperator(ctx, claims, gatewayID)
	if err != nil {
		return "", err
	}
	var hasActiveCred, hasRevokedCred bool
	now := store.clock().UTC()
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT
  EXISTS (
    SELECT 1 FROM connectivity.gateway_credentials
    WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
      AND status = 'ACTIVE' AND valid_from <= $3 AND valid_until > $3
  ),
  EXISTS (
    SELECT 1 FROM connectivity.gateway_credentials
    WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND status = 'REVOKED'
  )`, tenantID, gatewayID, now).Scan(&hasActiveCred, &hasRevokedCred)
	}); err != nil {
		return "", fmt.Errorf("check Gateway credential state: %w", err)
	}
	if !hasActiveCred {
		if hasRevokedCred {
			return "", ErrCredentialRevoked
		}
		return "", adapter.ErrGatewayCredentialInactive
	}
	return tenantID, nil
}

func (store *Store) RecordConcentratorAudit(ctx context.Context, tenantID, gatewayID string, entry ConcentratorAuditEntry) (ConcentratorAuditEntry, error) {
	if store == nil || store.pool == nil {
		return ConcentratorAuditEntry{}, errors.New("connectivity store is closed")
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = store.clock().UTC()
	} else {
		entry.OccurredAt = entry.OccurredAt.UTC()
	}
	if entry.ID == "" {
		id, err := uuidv7.NewAt(entry.OccurredAt)
		if err != nil {
			return ConcentratorAuditEntry{}, err
		}
		entry.ID = id
	}
	if entry.Diff == nil {
		entry.Diff = map[string]any{}
	}
	diffBytes, err := json.Marshal(entry.Diff)
	if err != nil {
		return ConcentratorAuditEntry{}, fmt.Errorf("marshal concentrator audit diff: %w", err)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ConcentratorAuditEntry{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return ConcentratorAuditEntry{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_concentrator_audit (
  id, tenant_id, gateway_id, unit_id, actor_id, operation, request_id, outcome, reason, diff, occurred_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7::uuid, $8, $9, $10::jsonb, $11
)`, entry.ID, tenantID, gatewayID, entry.UnitID, entry.ActorID, entry.Operation, entry.RequestID, entry.Outcome, entry.Reason, string(diffBytes), entry.OccurredAt); err != nil {
		return ConcentratorAuditEntry{}, fmt.Errorf("insert concentrator audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ConcentratorAuditEntry{}, err
	}
	return entry, nil
}

func (store *Store) ListConcentratorAudits(ctx context.Context, tenantID, gatewayID string, unitID, limit int) ([]ConcentratorAuditEntry, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("connectivity store is closed")
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	items := []ConcentratorAuditEntry{}
	err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, unit_id, actor_id::text, operation, request_id::text, outcome, reason, diff, occurred_at
FROM connectivity.gateway_concentrator_audit
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND unit_id = $3
ORDER BY occurred_at DESC, id DESC
LIMIT $4
`, tenantID, gatewayID, unitID, limit)
		if err != nil {
			return fmt.Errorf("query concentrator audits: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var entry ConcentratorAuditEntry
			var diffJSON []byte
			if err := rows.Scan(&entry.ID, &entry.UnitID, &entry.ActorID, &entry.Operation, &entry.RequestID, &entry.Outcome, &entry.Reason, &diffJSON, &entry.OccurredAt); err != nil {
				return fmt.Errorf("scan concentrator audit: %w", err)
			}
			entry.OccurredAt = entry.OccurredAt.UTC()
			entry.Diff = map[string]any{}
			if len(diffJSON) > 0 {
				if err := json.Unmarshal(diffJSON, &entry.Diff); err != nil {
					return fmt.Errorf("decode concentrator audit diff: %w", err)
				}
			}
			items = append(items, entry)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}
