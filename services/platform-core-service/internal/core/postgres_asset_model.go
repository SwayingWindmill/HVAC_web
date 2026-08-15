package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func (store *PostgresStore) GetSiteAssetModel(ctx context.Context, claims registryauth.GrantClaims, siteID string) (SiteAssetModel, error) {
	if !validUUIDv7(siteID) {
		return SiteAssetModel{}, ErrNotFound
	}
	result := SiteAssetModel{
		SchemaVersion:         2,
		TenantID:              claims.TenantID,
		SiteID:                siteID,
		Spaces:                 []Space{},
		Assets:                []Asset{},
		Devices:               []Device{},
		DeviceBindings:        []DeviceBinding{},
		AssetSpaceBindings: []AssetSpaceBinding{},
		DeviceSpaceBindings:    []DeviceSpaceBinding{},
		Sensors:               []Sensor{},
		SensorDeviceBindings:  []SensorDeviceBinding{},
		SensorSpaceBindings:    []SensorSpaceBinding{},
		TelemetryPoints:       []TelemetryPoint{},
		Relationships:         []AssetRelationship{},
		PointSubjectBindings:  []PointSubjectBinding{},
	}

	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var visible bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid
    AND NOT (id = ANY($2::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&visible); err != nil {
			return fmt.Errorf("query Registry Site asset-model visibility: %w", err)
		}
		if !visible {
			return ErrNotFound
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, parent_space_id::text,
       code, display_name, space_type, status, revision, created_at, updated_at
FROM core_registry.spaces
WHERE site_id = $1::uuid
ORDER BY display_name COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanSpace(row)
			if err == nil {
				result.Spaces = append(result.Spaces, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name,
       asset_type, status, revision, created_at, updated_at
FROM core_registry.assets
WHERE site_id = $1::uuid
ORDER BY display_name COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanAsset(row)
			if err == nil {
				result.Assets = append(result.Assets, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name,
       device_type, status, revision, created_at, updated_at
FROM core_registry.devices
WHERE site_id = $1::uuid
ORDER BY display_name COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanDevice(row)
			if err == nil {
				result.Devices = append(result.Devices, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, device_id::text,
       asset_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.device_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanDeviceBinding(row)
			if err == nil {
				result.DeviceBindings = append(result.DeviceBindings, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, asset_id::text,
       space_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.asset_space_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanAssetSpaceBinding(row)
			if err == nil {
				result.AssetSpaceBindings = append(result.AssetSpaceBindings, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, device_id::text,
       space_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.device_space_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanDeviceSpaceBinding(row)
			if err == nil {
				result.DeviceSpaceBindings = append(result.DeviceSpaceBindings, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, code, display_name,
       sensor_type, manufacturer, model, serial_number, calibration_due_at,
       metadata, status, revision, created_at, updated_at
FROM core_registry.sensors
WHERE site_id = $1::uuid
ORDER BY display_name COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanSensor(row)
			if err == nil {
				result.Sensors = append(result.Sensors, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, sensor_id::text,
       device_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.sensor_device_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanSensorDeviceBinding(row)
			if err == nil {
				result.SensorDeviceBindings = append(result.SensorDeviceBindings, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, sensor_id::text,
       space_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.sensor_space_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanSensorSpaceBinding(row)
			if err == nil {
				result.SensorSpaceBindings = append(result.SensorSpaceBindings, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, reporting_device_id::text,
       sensor_id::text, point_code, source_key, display_name, point_type,
       value_type, unit, writable, sample_interval_ms, publish_interval_ms,
       stale_after_ms, source_metadata, status, revision, created_at, updated_at
FROM core_registry.telemetry_points
WHERE site_id = $1::uuid
ORDER BY reporting_device_id, point_code COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanTelemetryPoint(row)
			if err == nil {
				result.TelemetryPoints = append(result.TelemetryPoints, item)
			}
			return err
		}); err != nil {
			return err
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, tenant_id::text, site_id::text, point_id::text,
       subject_type, space_id::text, asset_id::text, binding_role,
       status, valid_from, valid_to, revision, created_at, updated_at
FROM core_registry.point_subject_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanPointSubjectBinding(row)
			if err == nil {
				result.PointSubjectBindings = append(result.PointSubjectBindings, item)
			}
			return err
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return SiteAssetModel{}, err
	}

	result.Relationships = buildAssetRelationships(result)
	result.Counts = AssetModelCounts{
		Spaces:           len(result.Spaces),
		Assets:          len(result.Assets),
		DeviceEndpoints: len(result.Devices),
		PhysicalSensors: len(result.Sensors),
		Points:          len(result.TelemetryPoints),
	}
	return result, nil
}

func queryAssetRows(
	ctx context.Context,
	transaction pgx.Tx,
	query string,
	siteID string,
	consume func(rowScanner) error,
) error {
	rows, err := transaction.Query(ctx, query, siteID)
	if err != nil {
		return fmt.Errorf("query Registry Site asset model: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := consume(rows); err != nil {
			return fmt.Errorf("scan Registry Site asset model: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Registry Site asset model: %w", err)
	}
	return nil
}

func buildAssetRelationships(model SiteAssetModel) []AssetRelationship {
	relationships := make([]AssetRelationship, 0,
		len(model.DeviceBindings)+len(model.AssetSpaceBindings)+len(model.DeviceSpaceBindings)+
			len(model.SensorDeviceBindings)+len(model.SensorSpaceBindings)+len(model.PointSubjectBindings),
	)
	for _, binding := range model.AssetSpaceBindings {
		relationships = append(relationships, relationshipFromBinding(
			binding.ID, binding.TenantID, binding.SiteID,
			"ASSET", binding.AssetID, "SPACE", binding.SpaceID,
			binding.BindingRole, binding.Status, binding.ValidFrom, binding.ValidTo,
			binding.Revision, binding.CreatedAt, binding.UpdatedAt,
		))
	}
	for _, binding := range model.DeviceSpaceBindings {
		relationships = append(relationships, relationshipFromBinding(
			binding.ID, binding.TenantID, binding.SiteID,
			"DEVICE", binding.DeviceID, "SPACE", binding.SpaceID,
			binding.BindingRole, binding.Status, binding.ValidFrom, binding.ValidTo,
			binding.Revision, binding.CreatedAt, binding.UpdatedAt,
		))
	}
	for _, binding := range model.DeviceBindings {
		relationships = append(relationships, relationshipFromBinding(
			binding.ID, binding.TenantID, binding.SiteID,
			"DEVICE", binding.DeviceID, "ASSET", binding.AssetID,
			binding.BindingRole, binding.Status, binding.ValidFrom, binding.ValidTo,
			binding.Revision, binding.CreatedAt, binding.UpdatedAt,
		))
	}
	for _, binding := range model.SensorDeviceBindings {
		relationships = append(relationships, relationshipFromBinding(
			binding.ID, binding.TenantID, binding.SiteID,
			"SENSOR", binding.SensorID, "DEVICE", binding.DeviceID,
			binding.BindingRole, binding.Status, binding.ValidFrom, binding.ValidTo,
			binding.Revision, binding.CreatedAt, binding.UpdatedAt,
		))
	}
	for _, binding := range model.SensorSpaceBindings {
		relationships = append(relationships, relationshipFromBinding(
			binding.ID, binding.TenantID, binding.SiteID,
			"SENSOR", binding.SensorID, "SPACE", binding.SpaceID,
			binding.BindingRole, binding.Status, binding.ValidFrom, binding.ValidTo,
			binding.Revision, binding.CreatedAt, binding.UpdatedAt,
		))
	}
	for _, binding := range model.PointSubjectBindings {
		toID := binding.SiteID
		if binding.SpaceID != nil {
			toID = *binding.SpaceID
		}
		if binding.AssetID != nil {
			toID = *binding.AssetID
		}
		relationships = append(relationships, relationshipFromBinding(
			binding.ID, binding.TenantID, binding.SiteID,
			"POINT", binding.PointID, binding.SubjectType, toID,
			binding.BindingRole, binding.Status, binding.ValidFrom, binding.ValidTo,
			binding.Revision, binding.CreatedAt, binding.UpdatedAt,
		))
	}
	return relationships
}

func relationshipFromBinding(
	id, tenantID, siteID, fromType, fromID, toType, toID, role, status, validFrom string,
	validTo *string,
	revision int64,
	createdAt, updatedAt string,
) AssetRelationship {
	return AssetRelationship{
		ID: id, TenantID: tenantID, SiteID: siteID,
		FromType: fromType, FromID: fromID, ToType: toType, ToID: toID,
		Role: role, Status: status, ValidFrom: validFrom, ValidTo: validTo,
		Revision: revision, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func scanSpace(row rowScanner) (Space, error) {
	var item Space
	var parentID *string
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID, &parentID,
		&item.Code, &item.DisplayName, &item.SpaceType, &item.Status,
		&item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return Space{}, err
	}
	item.ParentSpaceID = parentID
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanAssetSpaceBinding(row rowScanner) (AssetSpaceBinding, error) {
	var item AssetSpaceBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.AssetID, &item.SpaceID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return AssetSpaceBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanDeviceSpaceBinding(row rowScanner) (DeviceSpaceBinding, error) {
	var item DeviceSpaceBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.DeviceID, &item.SpaceID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return DeviceSpaceBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanSensor(row rowScanner) (Sensor, error) {
	var item Sensor
	var calibrationDueAt *time.Time
	var metadata []byte
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.Code, &item.DisplayName, &item.SensorType,
		&item.Manufacturer, &item.Model, &item.SerialNumber, &calibrationDueAt,
		&metadata, &item.Status, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return Sensor{}, err
	}
	item.Metadata = map[string]any{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return Sensor{}, fmt.Errorf("decode Sensor metadata: %w", err)
		}
	}
	if calibrationDueAt != nil {
		formatted := formatInstant(*calibrationDueAt)
		item.CalibrationDueAt = &formatted
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanSensorDeviceBinding(row rowScanner) (SensorDeviceBinding, error) {
	var item SensorDeviceBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.SensorID, &item.DeviceID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return SensorDeviceBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanSensorSpaceBinding(row rowScanner) (SensorSpaceBinding, error) {
	var item SensorSpaceBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.SensorID, &item.SpaceID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return SensorSpaceBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanTelemetryPoint(row rowScanner) (TelemetryPoint, error) {
	var item TelemetryPoint
	var metadata []byte
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.ReportingDeviceID, &item.SensorID, &item.PointCode, &item.SourceKey,
		&item.DisplayName, &item.PointType, &item.ValueType, &item.Unit,
		&item.Writable, &item.SampleIntervalMS, &item.PublishIntervalMS,
		&item.StaleAfterMS, &metadata, &item.Status, &item.Revision,
		&createdAt, &updatedAt,
	); err != nil {
		return TelemetryPoint{}, err
	}
	item.SourceMetadata = map[string]any{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &item.SourceMetadata); err != nil {
			return TelemetryPoint{}, fmt.Errorf("decode Telemetry Point source metadata: %w", err)
		}
	}
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanPointSubjectBinding(row rowScanner) (PointSubjectBinding, error) {
	var item PointSubjectBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.SiteID,
		&item.PointID, &item.SubjectType, &item.SpaceID, &item.AssetID,
		&item.BindingRole, &item.Status, &validFrom, &validTo,
		&item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return PointSubjectBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func assignBindingTimes(
	validFrom time.Time,
	validTo *time.Time,
	createdAt time.Time,
	updatedAt time.Time,
	formattedValidFrom *string,
	formattedValidTo **string,
	formattedCreatedAt *string,
	formattedUpdatedAt *string,
) {
	*formattedValidFrom = formatInstant(validFrom)
	if validTo != nil {
		value := formatInstant(*validTo)
		*formattedValidTo = &value
	}
	*formattedCreatedAt = formatInstant(createdAt)
	*formattedUpdatedAt = formatInstant(updatedAt)
}
