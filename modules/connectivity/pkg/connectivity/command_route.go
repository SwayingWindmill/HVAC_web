package connectivity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// ResolveCommandRoute finds where a command goes from the Registry read port, and
// refuses it before anything is sent when the Device is not behind a registered
// Gateway, when control is not enabled (the command Point is not an active writable
// Point of the Device), or when the Gateway holds no active credential.
func (store *Store) ResolveCommandRoute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.DeviceRoute, error) {
	var route commandmodel.DeviceRoute
	err := store.readAsTenant(ctx, envelope.TenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT gateway_id::text, source_key, revision
FROM core_registry.gateway_device_source_keys_v1
WHERE site_id = $1::uuid AND device_id = $2::uuid
`, envelope.SiteID, envelope.DeviceID).Scan(&route.GatewayID, &route.ExternalDeviceID, &route.BindingRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			return commandmodel.ErrCommandRouteNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve command Device source key: %w", err)
		}
		var controlEnabled bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.point_bindings_v1
  WHERE point_id = $1::uuid AND device_id = $2::uuid AND writable
)`, envelope.PointID, envelope.DeviceID).Scan(&controlEnabled); err != nil {
			return fmt.Errorf("read command Point binding: %w", err)
		}
		if !controlEnabled {
			return commandmodel.ErrCommandControlDisabled
		}
		var credentialActive bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM connectivity.gateway_credentials
  WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND status = 'ACTIVE'
    AND valid_from <= $3 AND valid_until > $3
)`, envelope.TenantID, route.GatewayID, store.clock().UTC()).Scan(&credentialActive); err != nil {
			return fmt.Errorf("read command Gateway credential: %w", err)
		}
		if !credentialActive {
			return commandmodel.ErrCommandGatewayCredentialInactive
		}
		return nil
	})
	if err != nil {
		return commandmodel.DeviceRoute{}, err
	}
	return route, nil
}

// GatewayTenant returns the Tenant of a Gateway in the Registry gateway directory, so a
// reply on hvac/v1/{gatewayId}/up/reply is recorded in that Tenant.
func (store *Store) GatewayTenant(ctx context.Context, gatewayID string) (string, error) {
	var tenantID string
	err := store.pool.QueryRow(ctx, `
SELECT tenant_id::text FROM core_registry.gateway_directory_v1 WHERE gateway_id = $1::uuid
`, gatewayID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read Registry gateway directory: %w", err)
	}
	return tenantID, nil
}

// Tenants lists every Tenant that has a Gateway; commands are only dispatched to
// Devices behind a Gateway.
func (store *Store) Tenants(ctx context.Context) ([]string, error) {
	rows, err := store.pool.Query(ctx, `
SELECT DISTINCT tenant_id::text FROM core_registry.gateway_directory_v1 ORDER BY 1
`)
	if err != nil {
		return nil, fmt.Errorf("list Gateway Tenants: %w", err)
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenantID)
	}
	return tenants, rows.Err()
}
