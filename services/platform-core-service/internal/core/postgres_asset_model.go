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
		SchemaVersion:         1,
		SiteID:                siteID,
		Areas:                 []Area{},
		Equipment:             []Equipment{},
		Devices:               []Device{},
		DeviceBindings:        []DeviceBinding{},
		EquipmentAreaBindings: []EquipmentAreaBinding{},
		DeviceAreaBindings:    []DeviceAreaBinding{},
		Sensors:               []Sensor{},
		SensorDeviceBindings:  []SensorDeviceBinding{},
		SensorAreaBindings:    []SensorAreaBinding{},
		SensorSubjectBindings: []SensorSubjectBinding{},
		TelemetryPoints:       []TelemetryPoint{},
		Relationships:         []AssetRelationship{},
		PointSubjectBindings:  []PointSubjectBinding{},
		CalculatedPointInputs: []CalculatedPointInput{},
	}
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var visible bool
		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM core_registry.sites
  WHERE id = $1::uuid
    AND NOT (organization_id = ANY($2::uuid[]))
    AND NOT (id = ANY($3::uuid[]))
)
`, siteID, postgresUUIDArray(claims.DeniedOrganizationIDs), postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&visible); err != nil {
			return fmt.Errorf("query Registry Site asset-model visibility: %w", err)
		}
		if !visible {
			return ErrNotFound
		}

		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, parent_area_id::text,
       code, display_name, area_type, status, revision, created_at, updated_at
FROM core_registry.areas
WHERE site_id = $1::uuid
ORDER BY display_name COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanArea(row)
			if err == nil {
				result.Areas = append(result.Areas, item)
			}
			return err
		}); err != nil {
			return err
		}
		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, code, display_name,
       equipment_type, status, revision, created_at, updated_at
FROM core_registry.equipment
WHERE site_id = $1::uuid
ORDER BY display_name COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanEquipment(row)
			if err == nil {
				result.Equipment = append(result.Equipment, item)
			}
			return err
		}); err != nil {
			return err
		}
		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, code, display_name,
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
SELECT id::text, organization_id::text, site_id::text, device_id::text,
       equipment_id::text, binding_role, status, valid_from, valid_to,
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
SELECT id::text, organization_id::text, site_id::text, equipment_id::text,
       area_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.equipment_area_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanEquipmentAreaBinding(row)
			if err == nil {
				result.EquipmentAreaBindings = append(result.EquipmentAreaBindings, item)
			}
			return err
		}); err != nil {
			return err
		}
		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, device_id::text,
       area_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.device_area_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanDeviceAreaBinding(row)
			if err == nil {
				result.DeviceAreaBindings = append(result.DeviceAreaBindings, item)
			}
			return err
		}); err != nil {
			return err
		}
		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, code, display_name,
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
SELECT id::text, organization_id::text, site_id::text, sensor_id::text,
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
SELECT id::text, organization_id::text, site_id::text, sensor_id::text,
       area_id::text, binding_role, status, valid_from, valid_to,
       revision, created_at, updated_at
FROM core_registry.sensor_area_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanSensorAreaBinding(row)
			if err == nil {
				result.SensorAreaBindings = append(result.SensorAreaBindings, item)
			}
			return err
		}); err != nil {
			return err
		}
		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, sensor_id::text,
       subject_type, area_id::text, equipment_id::text, binding_role,
       status, valid_from, valid_to, revision, created_at, updated_at
FROM core_registry.sensor_subject_bindings
WHERE site_id = $1::uuid
ORDER BY binding_role COLLATE "C", id
`, siteID, func(row rowScanner) error {
			item, err := scanSensorSubjectBinding(row)
			if err == nil {
				result.SensorSubjectBindings = append(result.SensorSubjectBindings, item)
			}
			return err
		}); err != nil {
			return err
		}
		if err := queryAssetRows(ctx, transaction, `
SELECT id::text, organization_id::text, site_id::text, reporting_device_id::text,
       sensor_id::text, point_key, source_key, display_name, point_kind,
       value_type, unit, writable, sample_interval_ms, publish_interval_ms,
       stale_after_ms, formula_revision, source_metadata, status, revision,
       created_at, updated_at
FROM core_registry.telemetry_points
WHERE site_id = $1::uuid
ORDER BY reporting_device_id, point_key COLLATE "C", id
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
SELECT id::text, organization_id::text, site_id::text, point_id::text,
       subject_type, area_id::text, equipment_id::text, binding_role,
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
		if err := queryAssetRows(ctx, transaction, `
SELECT organization_id::text, site_id::text, calculated_point_id::text,
       input_point_id::text, input_role, ordinal, formula_revision
FROM core_registry.calculated_point_inputs
WHERE site_id = $1::uuid
ORDER BY calculated_point_id, ordinal, input_point_id
`, siteID, func(row rowScanner) error {
			var item CalculatedPointInput
			if err := row.Scan(
				&item.OwningOrganizationID,
				&item.SiteID,
				&item.CalculatedPointID,
				&item.InputPointID,
				&item.InputRole,
				&item.Ordinal,
				&item.FormulaRevision,
			); err != nil {
				return err
			}
			result.CalculatedPointInputs = append(result.CalculatedPointInputs, item)
			return nil
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
		Areas:                    len(result.Areas),
		Equipment:                len(result.Equipment),
		DeviceEndpoints:          len(result.Devices),
		Sensors:                  len(result.Sensors),
		TelemetryPoints:          len(result.TelemetryPoints),
		CalculatedPoints:         countCalculatedPoints(result.TelemetryPoints),
		IndependentSensorDevices: countIndependentSensorDevices(result.SensorDeviceBindings),
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
		len(model.DeviceBindings)+len(model.EquipmentAreaBindings)+len(model.DeviceAreaBindings)+
			len(model.SensorDeviceBindings)+len(model.SensorAreaBindings)+len(model.SensorSubjectBindings)+len(model.PointSubjectBindings),
	)
	for _, binding := range model.EquipmentAreaBindings {
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "EQUIPMENT", FromID: binding.EquipmentID, ToType: "AREA", ToID: binding.AreaID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	for _, binding := range model.DeviceAreaBindings {
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "DEVICE", FromID: binding.DeviceID, ToType: "AREA", ToID: binding.AreaID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	for _, binding := range model.DeviceBindings {
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "DEVICE", FromID: binding.DeviceID, ToType: "EQUIPMENT", ToID: binding.EquipmentID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	for _, binding := range model.SensorDeviceBindings {
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "SENSOR", FromID: binding.SensorID, ToType: "DEVICE", ToID: binding.DeviceID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	for _, binding := range model.SensorAreaBindings {
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "SENSOR", FromID: binding.SensorID, ToType: "AREA", ToID: binding.AreaID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	for _, binding := range model.SensorSubjectBindings {
		toID := binding.SiteID
		if binding.AreaID != nil {
			toID = *binding.AreaID
		}
		if binding.EquipmentID != nil {
			toID = *binding.EquipmentID
		}
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "SENSOR", FromID: binding.SensorID, ToType: binding.SubjectType, ToID: toID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	for _, binding := range model.PointSubjectBindings {
		toID := binding.SiteID
		if binding.AreaID != nil {
			toID = *binding.AreaID
		}
		if binding.EquipmentID != nil {
			toID = *binding.EquipmentID
		}
		relationships = append(relationships, AssetRelationship{
			ID: binding.ID, OwningOrganizationID: binding.OwningOrganizationID, SiteID: binding.SiteID,
			FromType: "POINT", FromID: binding.PointID, ToType: binding.SubjectType, ToID: toID,
			Role: binding.BindingRole, Status: binding.Status, ValidFrom: binding.ValidFrom,
			ValidTo: binding.ValidTo, Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		})
	}
	return relationships
}

func countCalculatedPoints(points []TelemetryPoint) int {
	count := 0
	for _, point := range points {
		if point.PointKind == "CALCULATED" {
			count++
		}
	}
	return count
}

func countIndependentSensorDevices(bindings []SensorDeviceBinding) int {
	devices := map[string]struct{}{}
	for _, binding := range bindings {
		if binding.BindingRole == "INDEPENDENT_DEVICE" && binding.Status == "ACTIVE" && binding.ValidTo == nil {
			devices[binding.DeviceID] = struct{}{}
		}
	}
	return len(devices)
}

func scanArea(row rowScanner) (Area, error) {
	var item Area
	var parentID *string
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.OwningOrganizationID, &item.SiteID, &parentID,
		&item.Code, &item.DisplayName, &item.AreaType, &item.Status,
		&item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return Area{}, err
	}
	item.ParentAreaID = parentID
	item.CreatedAt = formatInstant(createdAt)
	item.UpdatedAt = formatInstant(updatedAt)
	return item, nil
}

func scanEquipmentAreaBinding(row rowScanner) (EquipmentAreaBinding, error) {
	var item EquipmentAreaBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.EquipmentID, &item.AreaID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return EquipmentAreaBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanDeviceAreaBinding(row rowScanner) (DeviceAreaBinding, error) {
	var item DeviceAreaBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.DeviceID, &item.AreaID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return DeviceAreaBinding{}, err
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
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
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
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.SensorID, &item.DeviceID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return SensorDeviceBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanSensorAreaBinding(row rowScanner) (SensorAreaBinding, error) {
	var item SensorAreaBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.SensorID, &item.AreaID, &item.BindingRole, &item.Status,
		&validFrom, &validTo, &item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return SensorAreaBinding{}, err
	}
	assignBindingTimes(validFrom, validTo, createdAt, updatedAt, &item.ValidFrom, &item.ValidTo, &item.CreatedAt, &item.UpdatedAt)
	return item, nil
}

func scanSensorSubjectBinding(row rowScanner) (SensorSubjectBinding, error) {
	var item SensorSubjectBinding
	var validFrom time.Time
	var validTo *time.Time
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.SensorID, &item.SubjectType, &item.AreaID, &item.EquipmentID,
		&item.BindingRole, &item.Status, &validFrom, &validTo,
		&item.Revision, &createdAt, &updatedAt,
	); err != nil {
		return SensorSubjectBinding{}, err
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
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.ReportingDeviceID, &item.SensorID, &item.PointKey, &item.SourceKey,
		&item.DisplayName, &item.PointKind, &item.ValueType, &item.Unit,
		&item.Writable, &item.SampleIntervalMS, &item.PublishIntervalMS,
		&item.StaleAfterMS, &item.FormulaRevision, &metadata,
		&item.Status, &item.Revision, &createdAt, &updatedAt,
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
		&item.ID, &item.OwningOrganizationID, &item.SiteID,
		&item.PointID, &item.SubjectType, &item.AreaID, &item.EquipmentID,
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
