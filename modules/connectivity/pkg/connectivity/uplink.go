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

// ResolvePoint reads a Device's active Point by the Point Code the Gateway sends, or nil
// when the Device has no such registered Point.
func (store *Store) ResolvePoint(ctx context.Context, gateway adapter.Gateway, deviceID, pointCode string) (*adapter.ResolvedPoint, error) {
	var point adapter.ResolvedPoint
	err := store.readAsTenant(ctx, gateway.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT point_id::text, sensor_id::text, point_type, value_type, unit,
       counter_decrease_mode, counter_rollover_modulus, point_revision
FROM core_registry.point_bindings_v1
WHERE device_id = $1::uuid AND point_code = $2
`, deviceID, pointCode).Scan(
			&point.PointID, &point.SensorID, &point.PointType, &point.ValueType, &point.Unit,
			&point.CounterDecreaseMode, &point.CounterRolloverModulus, &point.PointRevision,
		)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve Registry Point binding: %w", err)
	}
	return &point, nil
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
