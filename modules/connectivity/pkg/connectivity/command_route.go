package connectivity

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
			var deviceCode string
			directErr := tx.QueryRow(ctx, `
SELECT device_code, revision
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND tenant_id = $2::uuid AND site_id = $3::uuid
  AND status = 'ACTIVE' AND connectivity_mode = 'DIRECT'
`, envelope.DeviceID, envelope.TenantID, envelope.SiteID).Scan(&deviceCode, &route.BindingRevision)
			if errors.Is(directErr, pgx.ErrNoRows) {
				return commandmodel.ErrCommandRouteNotFound
			}
			if directErr != nil {
				return fmt.Errorf("resolve direct command Device directory: %w", directErr)
			}
			route.GatewayID = envelope.DeviceID
			route.ExternalDeviceID = strings.TrimSpace(deviceCode)
			if route.ExternalDeviceID == "" {
				route.ExternalDeviceID = envelope.DeviceID
			}
		} else if err != nil {
			return fmt.Errorf("resolve command Device source key: %w", err)
		}
		var pointCode string
		var rawMetadata []byte
		if err := tx.QueryRow(ctx, `
SELECT pb.point_code, COALESCE(cr.source_metadata, '{}'::jsonb)
FROM core_registry.point_bindings_v1 AS pb
LEFT JOIN core_registry.command_point_routes_v1 AS cr
  ON cr.point_id = pb.point_id AND cr.device_id = pb.device_id
WHERE pb.point_id = $1::uuid AND pb.device_id = $2::uuid AND pb.writable
`, envelope.PointID, envelope.DeviceID).Scan(&pointCode, &rawMetadata); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return commandmodel.ErrCommandControlDisabled
			}
			return fmt.Errorf("read command Point binding: %w", err)
		}
		if err := applyPointSourceMetadata(&route, pointCode, rawMetadata); err != nil {
			return err
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

func applyPointSourceMetadata(route *commandmodel.DeviceRoute, pointCode string, rawMetadata []byte) error {
	if len(rawMetadata) == 0 {
		return nil
	}
	metadata, err := commandmodel.ParseCommandPointMetadataJSON(rawMetadata)
	if err != nil {
		return fmt.Errorf("decode command Point source metadata: %w", err)
	}
	if mode := normalizeActuationMode(metadata.ActuationMode); mode != "" {
		route.PointCode = pointCode
		route.ActuationMode = mode
		route.Channel = metadata.Channel
		if metadata.PulseDurationMS > 0 {
			route.PulseDurationMs = metadata.PulseDurationMS
		}
		if metadata.CoilAddress != nil && *metadata.CoilAddress >= 0 {
			route.CoilAddress = *metadata.CoilAddress
			route.HasCoilAddress = true
		}
	}
	return nil
}

func normalizeActuationMode(mode string) string {
	return strings.ToUpper(strings.TrimSpace(mode))
}

// GatewayTenant returns the Tenant of a Gateway in the Registry gateway directory, so a
// reply on hvac/v1/{gatewayId}/up/reply is recorded in that Tenant.
func (store *Store) GatewayTenant(ctx context.Context, gatewayID string) (string, error) {
	if store == nil {
		return "", errors.New("connectivity store is closed")
	}
	now := store.clock().UTC()
	if cached, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, gatewayID, now, gatewayID); ok && cached.err == nil && cached.gateway.TenantID != "" {
		return cached.gateway.TenantID, nil
	}
	if store.pool == nil {
		return "", errors.New("connectivity store is closed")
	}
	var tenantID string
	err := store.pool.QueryRow(ctx, `
SELECT tenant_id::text FROM core_registry.gateway_directory_v1 WHERE gateway_id = $1::uuid
`, gatewayID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: %w", ErrNotFound, commandmodel.ErrCommandCorrelationNotFound)
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
