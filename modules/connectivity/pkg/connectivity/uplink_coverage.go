package connectivity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ListGatewayChildDeviceIDs returns distinct Device IDs registered under one
// Gateway across its source keys, respecting Registry tenant RLS.
func (store *Store) ListGatewayChildDeviceIDs(ctx context.Context, gatewayID string) ([]string, error) {
	if store == nil || store.pool == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(gatewayID)
	if trimmed == "" {
		return nil, nil
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin gateway lookup transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	var tenantID string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid
`, trimmed).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Registry gateway directory: %w", err)
	}
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return nil, err
	}

	var deviceIDs []string
	err = store.readAsTenant(ctx, tenantID, func(tenantTx pgx.Tx) error {
		rows, err := tenantTx.Query(ctx, `
SELECT DISTINCT device_id::text
FROM core_registry.gateway_device_source_keys_v1
WHERE gateway_id = $1::uuid
ORDER BY device_id::text
`, trimmed)
		if err != nil {
			return fmt.Errorf("list gateway child devices: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan gateway child device: %w", err)
			}
			deviceIDs = append(deviceIDs, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return deviceIDs, nil
}

// ListAllActiveManagedDeviceIDs returns distinct Device IDs behind ACTIVE Gateways
// that currently hold an active, unexpired Gateway credential. Gateways with
// revoked or expired credentials are excluded so coverage heartbeats never mark
// their devices available.
func (store *Store) ListAllActiveManagedDeviceIDs(ctx context.Context) ([]string, error) {
	if store == nil || store.pool == nil {
		return nil, nil
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin active gateways lookup transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
SELECT tenant_id::text, gateway_id::text
FROM core_registry.gateway_directory_v1
WHERE status = 'ACTIVE'
ORDER BY tenant_id::text, gateway_id::text
`)
	if err != nil {
		return nil, fmt.Errorf("list active gateways: %w", err)
	}
	defer rows.Close()

	tenantGateways := make(map[string][]string)
	tenantOrder := make([]string, 0)
	for rows.Next() {
		var tenantID, gatewayID string
		if err := rows.Scan(&tenantID, &gatewayID); err != nil {
			return nil, fmt.Errorf("scan active gateway: %w", err)
		}
		if _, exists := tenantGateways[tenantID]; !exists {
			tenantOrder = append(tenantOrder, tenantID)
		}
		tenantGateways[tenantID] = append(tenantGateways[tenantID], gatewayID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active gateways: %w", err)
	}
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return nil, err
	}

	now := store.clock().UTC()
	seen := make(map[string]struct{})
	deviceIDs := make([]string, 0)
	for _, tenantID := range tenantOrder {
		gatewayIDs := tenantGateways[tenantID]
		err := store.readAsTenant(ctx, tenantID, func(tenantTx pgx.Tx) error {
			devRows, err := tenantTx.Query(ctx, `
SELECT DISTINCT k.device_id::text
FROM core_registry.gateway_device_source_keys_v1 k
WHERE k.gateway_id = ANY($1::uuid[])
  AND EXISTS (
    SELECT 1 FROM connectivity.gateway_credentials c
    WHERE c.tenant_id = $2::uuid
      AND c.gateway_id = k.gateway_id
      AND c.status = 'ACTIVE'
      AND c.valid_from <= $3
      AND c.valid_until > $3
  )
ORDER BY k.device_id::text
`, gatewayIDs, tenantID, now)
			if err != nil {
				return fmt.Errorf("list active managed devices for tenant %s: %w", tenantID, err)
			}
			defer devRows.Close()
			for devRows.Next() {
				var id string
				if err := devRows.Scan(&id); err != nil {
					return fmt.Errorf("scan active managed device: %w", err)
				}
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				deviceIDs = append(deviceIDs, id)
			}
			return devRows.Err()
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(deviceIDs)
	return deviceIDs, nil
}
