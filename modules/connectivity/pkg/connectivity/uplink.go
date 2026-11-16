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
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return adapter.Gateway{}, err
	}
	defer tx.Rollback(ctx)
	gateway := adapter.Gateway{ID: gatewayID}
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND status = 'ACTIVE'
`, gatewayID).Scan(&gateway.TenantID, &gateway.SiteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return adapter.Gateway{}, adapter.ErrGatewayUnknown
	}
	if err != nil {
		return adapter.Gateway{}, fmt.Errorf("read Registry gateway directory: %w", err)
	}
	if err := setTenant(ctx, tx, gateway.TenantID); err != nil {
		return adapter.Gateway{}, err
	}
	var credentialActive bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM connectivity.gateway_credentials
  WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND status = 'ACTIVE'
    AND valid_from <= $3 AND valid_until > $3
)`, gateway.TenantID, gatewayID, store.clock().UTC()).Scan(&credentialActive); err != nil {
		return adapter.Gateway{}, fmt.Errorf("read Gateway credential: %w", err)
	}
	if !credentialActive {
		return gateway, adapter.ErrGatewayCredentialInactive
	}
	return gateway, nil
}

// ResolveDevice returns the Device a Gateway names by a source key, or "" when that
// source key is not registered behind the Gateway.
func (store *Store) ResolveDevice(ctx context.Context, gateway adapter.Gateway, sourceKey string) (string, error) {
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
// within one read-only Tenant transaction.
func (store *Store) ResolveBindings(ctx context.Context, gateway adapter.Gateway, queries []adapter.DevicePointQuery) (adapter.ResolvedBindings, error) {
	result := adapter.ResolvedBindings{
		Devices: make(map[string]string),
		Points:  make(map[string]*adapter.ResolvedPoint),
	}
	sourceKeys, pointCodes := uniqueDevicePointTerms(queries)
	if len(sourceKeys) == 0 {
		return result, nil
	}
	err := store.readAsTenant(ctx, gateway.TenantID, func(tx pgx.Tx) error {
		deviceIDs, err := queryGatewayDevices(ctx, tx, gateway.ID, sourceKeys, result.Devices)
		if err != nil {
			return err
		}
		if len(deviceIDs) == 0 || len(pointCodes) == 0 {
			return nil
		}
		return queryDevicePointBindings(ctx, tx, deviceIDs, pointCodes, result.Points)
	})
	if err != nil {
		return adapter.ResolvedBindings{}, err
	}
	return result, nil
}

func uniqueDevicePointTerms(queries []adapter.DevicePointQuery) ([]string, []string) {
	sourceKeySet := make(map[string]struct{}, len(queries))
	sourceKeys := make([]string, 0, len(queries))
	pointCodeSet := make(map[string]struct{}, len(queries))
	pointCodes := make([]string, 0, len(queries))
	for _, query := range queries {
		if query.SourceKey != "" {
			if _, ok := sourceKeySet[query.SourceKey]; !ok {
				sourceKeySet[query.SourceKey] = struct{}{}
				sourceKeys = append(sourceKeys, query.SourceKey)
			}
		}
		if query.PointCode != "" {
			if _, ok := pointCodeSet[query.PointCode]; !ok {
				pointCodeSet[query.PointCode] = struct{}{}
				pointCodes = append(pointCodes, query.PointCode)
			}
		}
	}
	return sourceKeys, pointCodes
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
