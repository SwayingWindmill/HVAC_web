package connectivity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

// ResolveGateway finds a Gateway in the Registry gateway directory, which every Tenant
// can read, and checks that it holds an active credential. The Gateway is returned with
// ErrGatewayCredentialInactive so quarantine evidence can carry its Tenant.
func (store *Store) ResolveGateway(ctx context.Context, gatewayID string) (adapter.Gateway, error) {
	return store.resolveGatewayCached(ctx, gatewayID)
}

// ResolveDevice returns the Device a Gateway names by a source key, or "" when that
// source key is not registered behind the Gateway.
func (store *Store) ResolveDevice(ctx context.Context, gateway adapter.Gateway, sourceKey string) (string, error) {
	return store.resolveDeviceCached(ctx, gateway, sourceKey, store.resolveDeviceUncached)
}

func (store *Store) resolveDeviceUncached(ctx context.Context, gateway adapter.Gateway, sourceKey string) (string, error) {
	var deviceID string
	err := store.readAsTenant(ctx, gateway.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT device_id::text
FROM core_registry.gateway_device_source_keys_v1
WHERE gateway_id = $1::uuid AND source_key = $2
`, gateway.ID, sourceKey).Scan(&deviceID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve Registry Device source key: %w", err)
	}
	return deviceID, nil
}

// ResolveBindings resolves all Gateway source keys and their active Point bindings
// within one read-only Tenant transaction, backed by the short-TTL uplink metadata cache.
func (store *Store) ResolveBindings(ctx context.Context, gateway adapter.Gateway, queries []adapter.DevicePointQuery) (adapter.ResolvedBindings, error) {
	return store.resolveBindingsCached(ctx, gateway, queries)
}

func (store *Store) resolveBindingsUncached(
	ctx context.Context,
	gateway adapter.Gateway,
	missingSourceKeys []string,
	cachedPointQueries []resolvedPointTarget,
	allQueries []adapter.DevicePointQuery,
) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
	result := adapter.ResolvedBindings{
		Devices: make(map[string]string),
		Points:  make(map[string]*adapter.ResolvedPoint),
	}
	var resolvedTargets []resolvedPointTarget
	err := store.readAsTenant(ctx, gateway.TenantID, func(tx pgx.Tx) error {
		if len(missingSourceKeys) > 0 {
			if _, err := queryGatewayDevices(ctx, tx, gateway.ID, missingSourceKeys, result.Devices); err != nil {
				return err
			}
		}
		targetSet := make(map[resolvedPointTarget]struct{}, len(cachedPointQueries)+len(allQueries))
		deviceSet := make(map[string]struct{})
		pointCodeSet := make(map[string]struct{})
		var deviceIDs []string
		var pointCodes []string
		addTarget := func(target resolvedPointTarget) {
			if target.deviceID == "" || target.pointCode == "" {
				return
			}
			if _, seen := targetSet[target]; seen {
				return
			}
			targetSet[target] = struct{}{}
			resolvedTargets = append(resolvedTargets, target)
			if _, ok := deviceSet[target.deviceID]; !ok {
				deviceSet[target.deviceID] = struct{}{}
				deviceIDs = append(deviceIDs, target.deviceID)
			}
			if _, ok := pointCodeSet[target.pointCode]; !ok {
				pointCodeSet[target.pointCode] = struct{}{}
				pointCodes = append(pointCodes, target.pointCode)
			}
		}
		for _, target := range cachedPointQueries {
			addTarget(target)
		}
		for _, query := range allQueries {
			if query.PointCode == "" {
				continue
			}
			if deviceID := result.Devices[query.SourceKey]; deviceID != "" {
				addTarget(resolvedPointTarget{deviceID: deviceID, pointCode: query.PointCode})
			}
		}
		if len(deviceIDs) == 0 || len(pointCodes) == 0 {
			return nil
		}
		return queryDevicePointBindings(ctx, tx, deviceIDs, pointCodes, result.Points)
	})
	if err != nil {
		return adapter.ResolvedBindings{}, nil, err
	}
	return result, resolvedTargets, nil
}

func queryGatewayDevices(ctx context.Context, tx pgx.Tx, gatewayID string, sourceKeys []string, devices map[string]string) ([]string, error) {
	rows, err := tx.Query(ctx, `
SELECT source_key, device_id::text
FROM core_registry.gateway_device_source_keys_v1
WHERE gateway_id = $1::uuid AND source_key = ANY($2::text[])
`, gatewayID, sourceKeys)
	if err != nil {
		return nil, fmt.Errorf("resolve Registry Device source keys: %w", err)
	}
	defer rows.Close()
	deviceIDSet := make(map[string]struct{})
	var deviceIDs []string
	for rows.Next() {
		var sourceKey, deviceID string
		if err := rows.Scan(&sourceKey, &deviceID); err != nil {
			return nil, fmt.Errorf("scan Registry Device source key: %w", err)
		}
		devices[sourceKey] = deviceID
		if _, ok := deviceIDSet[deviceID]; !ok {
			deviceIDSet[deviceID] = struct{}{}
			deviceIDs = append(deviceIDs, deviceID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Registry Device source keys: %w", err)
	}
	return deviceIDs, nil
}

func queryDevicePointBindings(ctx context.Context, tx pgx.Tx, deviceIDs, pointCodes []string, points map[string]*adapter.ResolvedPoint) error {
	rows, err := tx.Query(ctx, `
SELECT device_id::text, point_code, point_id::text, sensor_id::text, point_type, value_type, unit,
       counter_decrease_mode, counter_rollover_modulus, point_revision
FROM core_registry.point_bindings_v1
WHERE device_id = ANY($1::uuid[]) AND point_code = ANY($2::text[])
`, deviceIDs, pointCodes)
	if err != nil {
		return fmt.Errorf("resolve Registry Point bindings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var deviceID, pointCode string
		var point adapter.ResolvedPoint
		if err := rows.Scan(
			&deviceID, &pointCode,
			&point.PointID, &point.SensorID, &point.PointType, &point.ValueType, &point.Unit,
			&point.CounterDecreaseMode, &point.CounterRolloverModulus, &point.PointRevision,
		); err != nil {
			return fmt.Errorf("scan Registry Point binding: %w", err)
		}
		points[adapter.PointBindingKey(deviceID, pointCode)] = &point
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Registry Point bindings: %w", err)
	}
	return nil
}

func (store *Store) QuarantineUplink(ctx context.Context, quarantine adapter.UplinkQuarantine) error {
	id, err := newUUIDv7(quarantine.ReceivedAt)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var tenantID any
	if quarantine.TenantID != "" {
		tenantID = quarantine.TenantID
		if err := setTenant(ctx, tx, quarantine.TenantID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.uplink_quarantine (
  id, tenant_id, gateway_id, topic, reason_code, detail, payload_sha256, payload_bytes, received_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
`, id, tenantID, quarantine.GatewayID, quarantine.Topic, quarantine.ReasonCode, quarantine.Detail,
		quarantine.PayloadSHA256(), len(quarantine.Payload), quarantine.ReceivedAt); err != nil {
		return fmt.Errorf("persist uplink quarantine: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) readAsTenant(ctx context.Context, tenantID string, read func(pgx.Tx) error) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	return read(tx)
}

func setTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("set Tenant context: %w", err)
	}
	return nil
}
