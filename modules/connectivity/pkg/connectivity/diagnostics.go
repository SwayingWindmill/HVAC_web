package connectivity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	defaultQuarantineListLimit = 20
	maxQuarantineListLimit     = 50
	DefaultQuarantineRetention = 7 * 24 * time.Hour
	quarantinePruneBatchSize   = 500
)

type GatewayQuarantineRecord struct {
	ID               string    `json:"id"`
	GatewayID        string    `json:"gatewayId"`
	Topic            string    `json:"topic"`
	ReasonCode       string    `json:"reasonCode"`
	Detail           string    `json:"detail"`
	PayloadSHA256    string    `json:"payloadSha256"`
	PayloadBytes     int       `json:"payloadBytes"`
	PayloadPreview   string    `json:"payloadPreview"`
	PayloadEncoding  string    `json:"payloadEncoding"`
	PayloadTruncated bool      `json:"payloadTruncated"`
	OccurrenceCount  int64     `json:"occurrenceCount"`
	ReceivedAt       time.Time `json:"receivedAt"`
	LastSeenAt       time.Time `json:"lastSeenAt"`
}

func (store *Store) resolveGatewayTenantForDiagnostics(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (string, error) {
	if store == nil || store.pool == nil {
		return "", errors.New("connectivity store is closed")
	}
	if !uuidv7.IsValid(gatewayID) {
		return "", ErrEnrollmentRejected
	}
	var tenantID, siteID string
	err := store.pool.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND status = 'ACTIVE'
`, gatewayID).Scan(&tenantID, &siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read Registry gateway directory: %w", err)
	}
	if tenantID != claims.TenantID || !registryauth.ScopeAllows(claims, siteID) {
		return "", ErrCredentialForbidden
	}
	return tenantID, nil
}

func (store *Store) CheckGatewayOperatorScope(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) error {
	_, err := store.resolveGatewayTenantForDiagnostics(ctx, claims, gatewayID)
	return err
}

func (store *Store) ListGatewayQuarantineForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, limit int) ([]GatewayQuarantineRecord, error) {
	tenantID, err := store.resolveGatewayTenantForDiagnostics(ctx, claims, gatewayID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultQuarantineListLimit
	}
	if limit > maxQuarantineListLimit {
		limit = maxQuarantineListLimit
	}
	_ = store.flushQuarantineBurstsForGateway(ctx, gatewayID)

	items := make([]GatewayQuarantineRecord, 0)
	err = store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, gateway_id, topic, reason_code, detail,
       payload_sha256, payload_bytes, payload_preview, payload_encoding,
       payload_truncated, occurrence_count, received_at, last_seen_at
FROM connectivity.uplink_quarantine
WHERE tenant_id = $1::uuid AND gateway_id = $2
ORDER BY last_seen_at DESC, id DESC
LIMIT $3
`, tenantID, gatewayID, limit)
		if err != nil {
			return fmt.Errorf("query gateway uplink quarantine: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var record GatewayQuarantineRecord
			if err := rows.Scan(
				&record.ID, &record.GatewayID, &record.Topic, &record.ReasonCode, &record.Detail,
				&record.PayloadSHA256, &record.PayloadBytes, &record.PayloadPreview, &record.PayloadEncoding,
				&record.PayloadTruncated, &record.OccurrenceCount, &record.ReceivedAt, &record.LastSeenAt,
			); err != nil {
				return fmt.Errorf("scan gateway uplink quarantine: %w", err)
			}
			record.ReceivedAt = record.ReceivedAt.UTC()
			record.LastSeenAt = record.LastSeenAt.UTC()
			items = append(items, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (store *Store) PruneExpiredQuarantine(ctx context.Context, retention time.Duration) (int64, error) {
	if store == nil || store.pool == nil {
		return 0, errors.New("connectivity store is closed")
	}
	if retention <= 0 {
		retention = DefaultQuarantineRetention
	}
	_ = store.FlushQuarantineBursts(ctx)
	cutoff := store.clock().UTC().Add(-retention)
	var totalDeleted int64
	for {
		if err := ctx.Err(); err != nil {
			return totalDeleted, err
		}
		var deleted int64
		if err := store.pool.QueryRow(ctx, `SELECT connectivity.prune_expired_uplink_quarantine($1, $2)`, cutoff, quarantinePruneBatchSize).Scan(&deleted); err != nil {
			return totalDeleted, fmt.Errorf("prune expired uplink quarantine: %w", err)
		}
		totalDeleted += deleted
		if deleted < quarantinePruneBatchSize {
			break
		}
	}
	return totalDeleted, nil
}
