package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type commandPointSourceMetadata struct {
	ActuationMode   string                        `json:"actuationMode"`
	Channel         string                        `json:"channel"`
	CoilAddress     *int                          `json:"coilAddress"`
	PulseDurationMs int                           `json:"pulseDurationMs"`
	InterlockChecks []commandmodel.InterlockCheck `json:"interlockChecks"`
}

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
		if len(rawMetadata) > 0 {
			var metadata commandPointSourceMetadata
			if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
				return fmt.Errorf("decode command Point source metadata: %w", err)
			}
			if mode := strings.TrimSpace(metadata.ActuationMode); mode != "" {
				route.PointCode = pointCode
				route.ActuationMode = mode
				route.Channel = strings.TrimSpace(metadata.Channel)
				if metadata.PulseDurationMs > 0 {
					route.PulseDurationMs = metadata.PulseDurationMs
				}
				if metadata.CoilAddress != nil && *metadata.CoilAddress >= 0 {
					route.CoilAddress = *metadata.CoilAddress
					route.HasCoilAddress = true
				}
			}
		}
		var siteControlMode string
		var deviceLockMode string
		if err := tx.QueryRow(ctx, `
SELECT
  COALESCE((
    SELECT mode FROM core_registry.site_control_states_v1
    WHERE tenant_id = $1::uuid AND site_id = $2::uuid
    LIMIT 1
  ), ''),
  COALESCE((
    SELECT lock_mode FROM core_registry.device_control_locks_v1
    WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND device_id = $3::uuid
    ORDER BY (lock_mode <> 'UNLOCKED') DESC, revision DESC
    LIMIT 1
  ), '')
`, envelope.TenantID, envelope.SiteID, envelope.DeviceID).Scan(&siteControlMode, &deviceLockMode); err != nil {
			return fmt.Errorf("read command control lock state: %w", err)
		}
		if isControlLockBlocking(siteControlMode, deviceLockMode, envelope.Capability, route.ActuationMode) {
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

func isControlLockBlocking(siteControlMode string, deviceLockMode string, capability commandmodel.Capability, actuationMode string) bool {
	isShutdown := capability == commandmodel.CapabilityStop || capability == commandmodel.CapabilityStopUnitSequence
	isEmergencyDoRelease := isShutdown && actuationMode == "EMERGENCY_DO_RELEASE"
	if siteControlMode != "" && siteControlMode != "REMOTE_AUTO" && !isShutdown {
		return true
	}
	if deviceLockMode != "" && deviceLockMode != "UNLOCKED" && !isEmergencyDoRelease {
		return true
	}
	return false
}

// ResolveCommandInterlocks loads declarative peer interlock preconditions from the
// active COMMAND point metadata on the Registry read port.
func (store *Store) ResolveCommandInterlocks(ctx context.Context, envelope commandmodel.DispatchEnvelope) ([]commandmodel.InterlockCheck, error) {
	var checks []commandmodel.InterlockCheck
	err := store.readAsTenant(ctx, envelope.TenantID, func(tx pgx.Tx) error {
		var rawInterlocks []byte
		err := tx.QueryRow(ctx, `
SELECT COALESCE(source_metadata->'interlockChecks', '[]'::jsonb)
FROM core_registry.command_point_routes_v1
WHERE site_id = $1::uuid AND device_id = $2::uuid AND point_id = $3::uuid AND writable
`, envelope.SiteID, envelope.DeviceID, envelope.PointID).Scan(&rawInterlocks)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read command interlock metadata: %w", err)
		}
		if err := json.Unmarshal(rawInterlocks, &checks); err != nil {
			return fmt.Errorf("decode command interlock metadata: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return checks, nil
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
