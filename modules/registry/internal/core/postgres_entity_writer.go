package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func (store *PostgresStore) SaveSite(ctx context.Context, claims registryauth.GrantClaims, input SiteMutation) (Site, bool, error) {
	if err := input.Validate(); err != nil {
		return Site{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionSiteWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[Site], error) {
		before := input.Meta.ExpectedRevision
		if input.ID == "" {
			id, err := newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[Site]{}, err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO core_registry.sites (id, tenant_id, code, display_name, timezone, status, revision, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, 1, $7, $7)
RETURNING id::text, tenant_id::text, code, display_name, timezone, status, revision, created_at, updated_at
`, id, claims.TenantID, input.Code, input.DisplayName, input.Timezone, input.Status, now)
			created, err := scanSite(row)
			if err != nil {
				return mutationRecord[Site]{}, fmt.Errorf("insert Registry Site: %w", err)
			}
			return entityMutationRecord(created, nil, "SITE", created.ID, 1, "registry.site.created", map[string]any{"siteId": created.ID}), nil
		}
		row := tx.QueryRow(ctx, `
UPDATE core_registry.sites
SET code = $4, display_name = $5, timezone = $6, status = $7, revision = revision + 1, updated_at = $8
WHERE tenant_id = $1::uuid AND id = $2::uuid AND revision = $3
RETURNING id::text, tenant_id::text, code, display_name, timezone, status, revision, created_at, updated_at
`, claims.TenantID, input.ID, input.Meta.ExpectedRevision, input.Code, input.DisplayName, input.Timezone, input.Status, now)
		updated, err := scanSite(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[Site]{}, classifyRevisionMiss(ctx, tx, "SITE", input.ID)
		}
		if err != nil {
			return mutationRecord[Site]{}, fmt.Errorf("update Registry Site: %w", err)
		}
		return entityMutationRecord(updated, revisionPointer(before), "SITE", updated.ID, updated.Revision, "registry.site.updated", map[string]any{"siteId": updated.ID}), nil
	})
}

func (store *PostgresStore) SaveSpace(ctx context.Context, claims registryauth.GrantClaims, input SpaceMutation) (Space, bool, error) {
	if err := input.Validate(); err != nil {
		return Space{}, false, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return Space{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionSpaceWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[Space], error) {
		before := input.Meta.ExpectedRevision
		id := input.ID
		if id == "" {
			var err error
			id, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[Space]{}, err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO core_registry.spaces (id, tenant_id, site_id, parent_space_id, code, display_name, space_type, status, revision, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 1, $9, $9)
RETURNING id::text, tenant_id::text, site_id::text, parent_space_id::text, code, display_name, space_type, status, revision, created_at, updated_at
`, id, claims.TenantID, input.SiteID, input.ParentSpaceID, input.Code, input.DisplayName, input.SpaceType, input.Status, now)
			created, err := scanSpace(row)
			if err != nil {
				return mutationRecord[Space]{}, fmt.Errorf("insert Registry Space: %w", err)
			}
			return entityMutationRecord(created, nil, "SPACE", created.ID, 1, "registry.space.created", map[string]any{"siteId": input.SiteID}), nil
		}
		row := tx.QueryRow(ctx, `
UPDATE core_registry.spaces
SET parent_space_id = $5::uuid, code = $6, display_name = $7, space_type = $8, status = $9, revision = revision + 1, updated_at = $10
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4
RETURNING id::text, tenant_id::text, site_id::text, parent_space_id::text, code, display_name, space_type, status, revision, created_at, updated_at
`, claims.TenantID, input.SiteID, input.ID, input.Meta.ExpectedRevision, input.ParentSpaceID, input.Code, input.DisplayName, input.SpaceType, input.Status, now)
		updated, err := scanSpace(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[Space]{}, classifyRevisionMiss(ctx, tx, "SPACE", input.ID)
		}
		if err != nil {
			return mutationRecord[Space]{}, fmt.Errorf("update Registry Space: %w", err)
		}
		return entityMutationRecord(updated, revisionPointer(before), "SPACE", updated.ID, updated.Revision, "registry.space.updated", map[string]any{"siteId": input.SiteID}), nil
	})
}

func (store *PostgresStore) SaveAsset(ctx context.Context, claims registryauth.GrantClaims, input AssetMutation) (Asset, bool, error) {
	if err := input.Validate(); err != nil {
		return Asset{}, false, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return Asset{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionAssetWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[Asset], error) {
		before := input.Meta.ExpectedRevision
		id := input.ID
		if id == "" {
			var err error
			id, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[Asset]{}, err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO core_registry.assets (id, tenant_id, site_id, code, display_name, asset_type, status, revision, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, 1, $8, $8)
RETURNING id::text, tenant_id::text, site_id::text, code, display_name, asset_type, status, revision, created_at, updated_at
`, id, claims.TenantID, input.SiteID, input.Code, input.DisplayName, input.AssetType, input.Status, now)
			created, err := scanAsset(row)
			if err != nil {
				return mutationRecord[Asset]{}, fmt.Errorf("insert Registry Asset: %w", err)
			}
			return entityMutationRecord(created, nil, "ASSET", created.ID, 1, "registry.asset.created", map[string]any{"siteId": input.SiteID}), nil
		}
		row := tx.QueryRow(ctx, `
UPDATE core_registry.assets
SET code = $5, display_name = $6, asset_type = $7, status = $8, revision = revision + 1, updated_at = $9
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4
RETURNING id::text, tenant_id::text, site_id::text, code, display_name, asset_type, status, revision, created_at, updated_at
`, claims.TenantID, input.SiteID, input.ID, input.Meta.ExpectedRevision, input.Code, input.DisplayName, input.AssetType, input.Status, now)
		updated, err := scanAsset(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[Asset]{}, classifyRevisionMiss(ctx, tx, "ASSET", input.ID)
		}
		if err != nil {
			return mutationRecord[Asset]{}, fmt.Errorf("update Registry Asset: %w", err)
		}
		return entityMutationRecord(updated, revisionPointer(before), "ASSET", updated.ID, updated.Revision, "registry.asset.updated", map[string]any{"siteId": input.SiteID}), nil
	})
}

func (store *PostgresStore) SaveDevice(ctx context.Context, claims registryauth.GrantClaims, input DeviceMutation) (Device, bool, error) {
	if err := input.Validate(); err != nil {
		return Device{}, false, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return Device{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionDeviceWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[Device], error) {
		before := input.Meta.ExpectedRevision
		id := input.ID
		if id == "" {
			var err error
			id, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[Device]{}, err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO core_registry.devices (id, tenant_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, 1, $8, $8)
RETURNING id::text, tenant_id::text, site_id::text, code, display_name, device_type, status, revision, created_at, updated_at
`, id, claims.TenantID, input.SiteID, input.Code, input.DisplayName, input.DeviceType, input.Status, now)
			created, err := scanDevice(row)
			if err != nil {
				return mutationRecord[Device]{}, fmt.Errorf("insert Registry Device: %w", err)
			}
			return entityMutationRecord(created, nil, "DEVICE", created.ID, 1, "registry.device.created", map[string]any{"siteId": input.SiteID}), nil
		}
		row := tx.QueryRow(ctx, `
UPDATE core_registry.devices
SET code = $5, display_name = $6, device_type = $7, status = $8, revision = revision + 1, updated_at = $9
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4
RETURNING id::text, tenant_id::text, site_id::text, code, display_name, device_type, status, revision, created_at, updated_at
`, claims.TenantID, input.SiteID, input.ID, input.Meta.ExpectedRevision, input.Code, input.DisplayName, input.DeviceType, input.Status, now)
		updated, err := scanDevice(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[Device]{}, classifyRevisionMiss(ctx, tx, "DEVICE", input.ID)
		}
		if err != nil {
			return mutationRecord[Device]{}, fmt.Errorf("update Registry Device: %w", err)
		}
		return entityMutationRecord(updated, revisionPointer(before), "DEVICE", updated.ID, updated.Revision, "registry.device.updated", map[string]any{"siteId": input.SiteID}), nil
	})
}

func (store *PostgresStore) SaveSensor(ctx context.Context, claims registryauth.GrantClaims, input SensorMutation) (Sensor, bool, error) {
	if err := input.Validate(); err != nil {
		return Sensor{}, false, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return Sensor{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionSensorWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[Sensor], error) {
		metadata, err := json.Marshal(input.Metadata)
		if err != nil {
			return mutationRecord[Sensor]{}, ErrInvalidMutation
		}
		var calibrationDue any
		if input.CalibrationDueAt != nil {
			parsed, _ := time.Parse(time.RFC3339Nano, *input.CalibrationDueAt)
			calibrationDue = parsed
		}
		before := input.Meta.ExpectedRevision
		id := input.ID
		if id == "" {
			id, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[Sensor]{}, err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO core_registry.sensors (
  id, tenant_id, site_id, code, display_name, sensor_type, manufacturer, model, serial_number,
  calibration_due_at, metadata, status, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, 1, $13, $13)
RETURNING id::text, tenant_id::text, site_id::text, code, display_name, sensor_type, manufacturer, model, serial_number, calibration_due_at, metadata, status, revision, created_at, updated_at
`, id, claims.TenantID, input.SiteID, input.Code, input.DisplayName, input.SensorType, input.Manufacturer, input.Model, input.SerialNumber, calibrationDue, metadata, input.Status, now)
			created, err := scanSensor(row)
			if err != nil {
				return mutationRecord[Sensor]{}, fmt.Errorf("insert Registry Sensor: %w", err)
			}
			return entityMutationRecord(created, nil, "SENSOR", created.ID, 1, "registry.sensor.created", map[string]any{"siteId": input.SiteID}), nil
		}
		row := tx.QueryRow(ctx, `
UPDATE core_registry.sensors
SET code = $5, display_name = $6, sensor_type = $7, manufacturer = $8, model = $9, serial_number = $10,
    calibration_due_at = $11, metadata = $12::jsonb, status = $13, revision = revision + 1, updated_at = $14
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4
RETURNING id::text, tenant_id::text, site_id::text, code, display_name, sensor_type, manufacturer, model, serial_number, calibration_due_at, metadata, status, revision, created_at, updated_at
`, claims.TenantID, input.SiteID, input.ID, input.Meta.ExpectedRevision, input.Code, input.DisplayName, input.SensorType, input.Manufacturer, input.Model, input.SerialNumber, calibrationDue, metadata, input.Status, now)
		updated, err := scanSensor(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[Sensor]{}, classifyRevisionMiss(ctx, tx, "SENSOR", input.ID)
		}
		if err != nil {
			return mutationRecord[Sensor]{}, fmt.Errorf("update Registry Sensor: %w", err)
		}
		return entityMutationRecord(updated, revisionPointer(before), "SENSOR", updated.ID, updated.Revision, "registry.sensor.updated", map[string]any{"siteId": input.SiteID}), nil
	})
}

func (store *PostgresStore) SavePoint(ctx context.Context, claims registryauth.GrantClaims, input PointMutation) (TelemetryPoint, bool, error) {
	if err := input.Validate(); err != nil {
		return TelemetryPoint{}, false, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return TelemetryPoint{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionPointWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[TelemetryPoint], error) {
		metadata, err := json.Marshal(input.SourceMetadata)
		if err != nil {
			return mutationRecord[TelemetryPoint]{}, ErrInvalidMutation
		}
		before := input.Meta.ExpectedRevision
		id := input.ID
		if id == "" {
			id, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[TelemetryPoint]{}, err
			}
			row := tx.QueryRow(ctx, `
INSERT INTO core_registry.telemetry_points (
  id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name,
  point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
  counter_decrease_mode, counter_rollover_modulus, source_metadata, status, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18::jsonb, $19, 1, $20, $20)
RETURNING id::text, tenant_id::text, site_id::text, reporting_device_id::text, sensor_id::text, point_code, source_key, display_name,
          point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, counter_decrease_mode, counter_rollover_modulus, source_metadata, status, revision, created_at, updated_at
`, id, claims.TenantID, input.SiteID, input.ReportingDeviceID, input.SensorID, input.PointCode, input.SourceKey, input.DisplayName,
				input.PointType, input.ValueType, input.Unit, input.Writable, input.SampleIntervalMS, input.PublishIntervalMS, input.StaleAfterMS,
				input.CounterDecreaseMode, input.CounterRolloverModulus, metadata, input.Status, now)
			created, err := scanTelemetryPoint(row)
			if err != nil {
				return mutationRecord[TelemetryPoint]{}, fmt.Errorf("insert Registry Point: %w", err)
			}
			return entityMutationRecord(created, nil, "POINT", created.ID, 1, "registry.point.created", map[string]any{"siteId": input.SiteID}), nil
		}
		row := tx.QueryRow(ctx, `
UPDATE core_registry.telemetry_points
SET reporting_device_id = $5::uuid, sensor_id = $6::uuid, point_code = $7, source_key = $8, display_name = $9,
    point_type = $10, value_type = $11, unit = $12, writable = $13, sample_interval_ms = $14,
    publish_interval_ms = $15, stale_after_ms = $16, counter_decrease_mode = $17, counter_rollover_modulus = $18,
    source_metadata = $19::jsonb, status = $20, revision = revision + 1, updated_at = $21
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4
RETURNING id::text, tenant_id::text, site_id::text, reporting_device_id::text, sensor_id::text, point_code, source_key, display_name,
          point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, counter_decrease_mode, counter_rollover_modulus, source_metadata, status, revision, created_at, updated_at
`, claims.TenantID, input.SiteID, input.ID, input.Meta.ExpectedRevision, input.ReportingDeviceID, input.SensorID, input.PointCode, input.SourceKey, input.DisplayName,
			input.PointType, input.ValueType, input.Unit, input.Writable, input.SampleIntervalMS, input.PublishIntervalMS, input.StaleAfterMS,
			input.CounterDecreaseMode, input.CounterRolloverModulus, metadata, input.Status, now)
		updated, err := scanTelemetryPoint(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[TelemetryPoint]{}, classifyRevisionMiss(ctx, tx, "POINT", input.ID)
		}
		if err != nil {
			return mutationRecord[TelemetryPoint]{}, fmt.Errorf("update Registry Point: %w", err)
		}
		return entityMutationRecord(updated, revisionPointer(before), "POINT", updated.ID, updated.Revision, "registry.point.updated", map[string]any{"siteId": input.SiteID}), nil
	})
}

func entityMutationRecord[T any](result T, before *int64, resourceType, resourceID string, revision int64, eventType string, payload map[string]any) mutationRecord[T] {
	var siteID *string
	if value, ok := payload["siteId"].(string); ok && validUUIDv7(value) && resourceType != "SITE" {
		siteID = &value
	}
	return mutationRecord[T]{
		Result: result, SiteID: siteID, ResourceType: resourceType, ResourceID: resourceID,
		BeforeRevision: before, AfterRevision: revisionPointer(revision), EventType: eventType,
		AggregateVersion: revision, Payload: payload,
	}
}

func classifyRevisionMiss(ctx context.Context, tx pgx.Tx, resourceType, id string) error {
	var revision int64
	var err error
	switch resourceType {
	case "SITE":
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.sites WHERE id = $1::uuid`, id).Scan(&revision)
	case "SPACE":
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.spaces WHERE id = $1::uuid`, id).Scan(&revision)
	case "ASSET":
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.assets WHERE id = $1::uuid`, id).Scan(&revision)
	case "DEVICE":
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.devices WHERE id = $1::uuid`, id).Scan(&revision)
	case "SENSOR":
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.sensors WHERE id = $1::uuid`, id).Scan(&revision)
	case "POINT":
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.telemetry_points WHERE id = $1::uuid`, id).Scan(&revision)
	default:
		return ErrInvalidMutation
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrRevisionConflict
}
