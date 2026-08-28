package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func (store *PostgresStore) PlanImport(ctx context.Context, claims registryauth.GrantClaims, input ImportPlanRequest) (ImportPlan, error) {
	if err := input.Validate(); err != nil {
		return ImportPlan{}, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return ImportPlan{}, err
	}
	plan := ImportPlan{SchemaVersion: 1, TenantID: claims.TenantID, SiteID: input.SiteID, Namespace: input.Namespace}
	err := store.withWriteTransaction(ctx, claims, registryauth.ActionRegistryImport, func(tx pgx.Tx) error {
		planID, err := newCoreUUIDv7(time.Now().UTC())
		if err != nil {
			return err
		}
		plan.PlanID = planID
		plan.Rows = make([]ImportRow, 0, len(input.Rows))
		plan.Results = make([]ImportRowResult, 0, len(input.Rows))
		for _, sourceRow := range input.Rows {
			row := sourceRow
			result := ImportRowResult{RowNumber: row.RowNumber, ResourceType: row.ResourceType, ExternalID: row.ExternalID}
			var mappedSiteID, mappedType, mappedID, mappedStatus string
			err := tx.QueryRow(ctx, `
SELECT site_id::text, resource_type, resource_id::text, status
FROM core_registry.registry_external_ids
WHERE tenant_id=$1::uuid AND namespace=$2 AND external_id=$3
`, claims.TenantID, input.Namespace, row.ExternalID).Scan(&mappedSiteID, &mappedType, &mappedID, &mappedStatus)
			switch {
			case err == nil:
				if mappedSiteID != input.SiteID || mappedType != string(row.ResourceType) || mappedStatus != "ACTIVE" {
					result.Status, result.ErrorCode, result.Message = "ERROR", "EXTERNAL_ID_CONFLICT", "External ID already resolves outside the requested active resource scope."
					plan.Rows = append(plan.Rows, row)
					plan.Results = append(plan.Results, result)
					continue
				}
				row.TargetID = mappedID
				revision, err := currentResourceRevision(ctx, tx, row.ResourceType, claims.TenantID, input.SiteID, mappedID)
				if err != nil {
					result.Status, result.ErrorCode, result.Message = "ERROR", "EXTERNAL_TARGET_MISSING", "External ID target does not resolve to an active Registry resource."
					plan.Rows = append(plan.Rows, row)
					plan.Results = append(plan.Results, result)
					continue
				}
				row.ExpectedRevision = revision
			case errors.Is(err, pgx.ErrNoRows):
				row.TargetID, err = newCoreUUIDv7(time.Now().UTC())
				if err != nil {
					return err
				}
				row.ExpectedRevision = 0
			case err != nil:
				return fmt.Errorf("resolve Registry import External ID: %w", err)
			}
			result.TargetID = row.TargetID
			result.ExpectedRevision = row.ExpectedRevision
			if err := importPayloadError(row, input.SiteID); err != nil {
				result.Status, result.ErrorCode, result.Message = "ERROR", "ROW_INVALID", "Row payload does not satisfy the Registry resource contract."
			} else {
				result.Status = "READY"
			}
			plan.Rows = append(plan.Rows, row)
			plan.Results = append(plan.Results, result)
		}
		digest, err := importPlanDigest(plan)
		if err != nil {
			return err
		}
		plan.Digest = digest
		return nil
	})
	if err != nil {
		return ImportPlan{}, err
	}
	return plan, nil
}

func (store *PostgresStore) CommitImport(ctx context.Context, claims registryauth.GrantClaims, input ImportCommitRequest) (ImportCommitResult, error) {
	if err := input.Validate(); err != nil {
		return ImportCommitResult{}, err
	}
	if input.Plan.TenantID != claims.TenantID {
		return ImportCommitResult{}, ErrImportPlanInvalid
	}
	if err := ensureSiteScope(claims, input.Plan.SiteID); err != nil {
		return ImportCommitResult{}, err
	}
	digest, err := importPlanDigest(input.Plan)
	if err != nil || digest != input.Plan.Digest {
		return ImportCommitResult{}, ErrImportPlanInvalid
	}
	for _, result := range input.Plan.Results {
		if result.Status != "READY" {
			return ImportCommitResult{}, ErrImportPlanInvalid
		}
	}
	result, replayed, err := runRegistryMutation(ctx, store, claims, registryauth.ActionRegistryImport, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[ImportCommitResult], error) {
		committed := make([]ImportRowResult, 0, len(input.Plan.Rows))
		for index, row := range input.Plan.Rows {
			planned := input.Plan.Results[index]
			if planned.RowNumber != row.RowNumber || planned.TargetID != row.TargetID || planned.ExpectedRevision != row.ExpectedRevision || planned.ResourceType != row.ResourceType || planned.ExternalID != row.ExternalID {
				return mutationRecord[ImportCommitResult]{}, ErrImportPlanInvalid
			}
			if err := applyImportRow(ctx, tx, claims.TenantID, input.Plan.SiteID, input.Plan.Namespace, row, now); err != nil {
				return mutationRecord[ImportCommitResult]{}, err
			}
			committed = append(committed, ImportRowResult{RowNumber: row.RowNumber, ResourceType: row.ResourceType, ExternalID: row.ExternalID, TargetID: row.TargetID, ExpectedRevision: row.ExpectedRevision, Status: "COMMITTED"})
		}
		value := ImportCommitResult{PlanDigest: input.Plan.Digest, Results: committed}
		return mutationRecord[ImportCommitResult]{Result: value, SiteID: &input.Plan.SiteID, ResourceType: "IMPORT", ResourceID: input.Plan.PlanID, AfterRevision: revisionPointer(1), EventType: "registry.import.committed", AggregateVersion: 1, Payload: map[string]any{"siteId": input.Plan.SiteID, "planId": input.Plan.PlanID, "planDigest": input.Plan.Digest, "rowCount": len(committed)}}, nil
	})
	if err != nil {
		return ImportCommitResult{}, err
	}
	result.Replayed = replayed
	return result, nil
}

func importPlanDigest(plan ImportPlan) (string, error) {
	copyPlan := plan
	copyPlan.Digest = ""
	encoded, err := json.Marshal(copyPlan)
	if err != nil {
		return "", fmt.Errorf("encode Registry import plan: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func currentResourceRevision(ctx context.Context, tx pgx.Tx, resourceType ResourceType, tenantID, siteID, resourceID string) (int64, error) {
	var revision int64
	var err error
	switch resourceType {
	case ResourceSpace:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.spaces WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED'`, tenantID, siteID, resourceID).Scan(&revision)
	case ResourceAsset:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.assets WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED'`, tenantID, siteID, resourceID).Scan(&revision)
	case ResourceDevice:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.devices WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED'`, tenantID, siteID, resourceID).Scan(&revision)
	case ResourceSensor:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.sensors WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED'`, tenantID, siteID, resourceID).Scan(&revision)
	case ResourcePoint:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED'`, tenantID, siteID, resourceID).Scan(&revision)
	default:
		return 0, ErrInvalidMutation
	}
	return revision, err
}

func applyImportRow(ctx context.Context, tx pgx.Tx, tenantID, siteID, namespace string, row ImportRow, now time.Time) error {
	if err := importPayloadError(row, siteID); err != nil {
		return ErrImportPlanInvalid
	}
	var err error
	switch row.ResourceType {
	case ResourceSpace:
		var input SpaceMutation
		_ = json.Unmarshal(row.Payload, &input)
		err = applySpaceImport(ctx, tx, tenantID, siteID, row, input, now)
	case ResourceAsset:
		var input AssetMutation
		_ = json.Unmarshal(row.Payload, &input)
		err = applyAssetImport(ctx, tx, tenantID, siteID, row, input, now)
	case ResourceDevice:
		var input DeviceMutation
		_ = json.Unmarshal(row.Payload, &input)
		err = applyDeviceImport(ctx, tx, tenantID, siteID, row, input, now)
	case ResourceSensor:
		var input SensorMutation
		_ = json.Unmarshal(row.Payload, &input)
		err = applySensorImport(ctx, tx, tenantID, siteID, row, input, now)
	case ResourcePoint:
		var input PointMutation
		_ = json.Unmarshal(row.Payload, &input)
		err = applyPointImport(ctx, tx, tenantID, siteID, row, input, now)
	default:
		return ErrImportPlanInvalid
	}
	if err != nil {
		return mapRegistryWriteError(err)
	}
	if row.ExpectedRevision == 0 {
		bindingID, err := newCoreUUIDv7(now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO core_registry.registry_external_ids (
 id, tenant_id, site_id, namespace, external_id, resource_type, resource_id, status, revision, created_at, updated_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::uuid,'ACTIVE',1,$8,$8)
`, bindingID, tenantID, siteID, namespace, row.ExternalID, string(row.ResourceType), row.TargetID, now)
		return mapRegistryWriteError(err)
	}
	var mappedID string
	if err := tx.QueryRow(ctx, `SELECT resource_id::text FROM core_registry.registry_external_ids WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND namespace=$3 AND external_id=$4 AND resource_type=$5 AND status='ACTIVE'`, tenantID, siteID, namespace, row.ExternalID, string(row.ResourceType)).Scan(&mappedID); err != nil || mappedID != row.TargetID {
		return ErrImportPlanInvalid
	}
	return nil
}

func applySpaceImport(ctx context.Context, tx pgx.Tx, tenantID, siteID string, row ImportRow, input SpaceMutation, now time.Time) error {
	if row.ExpectedRevision == 0 {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.spaces (id,tenant_id,site_id,parent_space_id,code,display_name,space_type,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,1,$9,$9)`, row.TargetID, tenantID, siteID, input.ParentSpaceID, input.Code, input.DisplayName, input.SpaceType, input.Status, now)
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE core_registry.spaces SET parent_space_id=$5::uuid,code=$6,display_name=$7,space_type=$8,status=$9,revision=revision+1,updated_at=$10 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, siteID, row.TargetID, row.ExpectedRevision, input.ParentSpaceID, input.Code, input.DisplayName, input.SpaceType, input.Status, now)
	if err == nil && command.RowsAffected() != 1 {
		return ErrRevisionConflict
	}
	return err
}

func applyAssetImport(ctx context.Context, tx pgx.Tx, tenantID, siteID string, row ImportRow, input AssetMutation, now time.Time) error {
	if row.ExpectedRevision == 0 {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.assets (id,tenant_id,site_id,code,display_name,asset_type,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,1,$8,$8)`, row.TargetID, tenantID, siteID, input.Code, input.DisplayName, input.AssetType, input.Status, now)
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE core_registry.assets SET code=$5,display_name=$6,asset_type=$7,status=$8,revision=revision+1,updated_at=$9 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, siteID, row.TargetID, row.ExpectedRevision, input.Code, input.DisplayName, input.AssetType, input.Status, now)
	if err == nil && command.RowsAffected() != 1 {
		return ErrRevisionConflict
	}
	return err
}

func applyDeviceImport(ctx context.Context, tx pgx.Tx, tenantID, siteID string, row ImportRow, input DeviceMutation, now time.Time) error {
	if row.ExpectedRevision == 0 {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.devices (id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,1,$8,$8)`, row.TargetID, tenantID, siteID, input.Code, input.DisplayName, input.DeviceType, input.Status, now)
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE core_registry.devices SET code=$5,display_name=$6,device_type=$7,status=$8,revision=revision+1,updated_at=$9 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, siteID, row.TargetID, row.ExpectedRevision, input.Code, input.DisplayName, input.DeviceType, input.Status, now)
	if err == nil && command.RowsAffected() != 1 {
		return ErrRevisionConflict
	}
	return err
}

func applySensorImport(ctx context.Context, tx pgx.Tx, tenantID, siteID string, row ImportRow, input SensorMutation, now time.Time) error {
	metadata, _ := json.Marshal(input.Metadata)
	var calibrationDue any
	if input.CalibrationDueAt != nil {
		calibrationDue, _ = time.Parse(time.RFC3339Nano, *input.CalibrationDueAt)
	}
	if row.ExpectedRevision == 0 {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.sensors (id,tenant_id,site_id,code,display_name,sensor_type,manufacturer,model,serial_number,calibration_due_at,metadata,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,1,$13,$13)`, row.TargetID, tenantID, siteID, input.Code, input.DisplayName, input.SensorType, input.Manufacturer, input.Model, input.SerialNumber, calibrationDue, metadata, input.Status, now)
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE core_registry.sensors SET code=$5,display_name=$6,sensor_type=$7,manufacturer=$8,model=$9,serial_number=$10,calibration_due_at=$11,metadata=$12::jsonb,status=$13,revision=revision+1,updated_at=$14 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, siteID, row.TargetID, row.ExpectedRevision, input.Code, input.DisplayName, input.SensorType, input.Manufacturer, input.Model, input.SerialNumber, calibrationDue, metadata, input.Status, now)
	if err == nil && command.RowsAffected() != 1 {
		return ErrRevisionConflict
	}
	return err
}

func applyPointImport(ctx context.Context, tx pgx.Tx, tenantID, siteID string, row ImportRow, input PointMutation, now time.Time) error {
	metadata, _ := json.Marshal(input.SourceMetadata)
	if row.ExpectedRevision == 0 {
		_, err := tx.Exec(ctx, `INSERT INTO core_registry.telemetry_points (id,tenant_id,site_id,reporting_device_id,sensor_id,point_code,source_key,display_name,point_type,value_type,unit,writable,sample_interval_ms,publish_interval_ms,stale_after_ms,counter_decrease_mode,counter_rollover_modulus,source_metadata,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::jsonb,$19,1,$20,$20)`, row.TargetID, tenantID, siteID, input.ReportingDeviceID, input.SensorID, input.PointCode, input.SourceKey, input.DisplayName, input.PointType, input.ValueType, input.Unit, input.Writable, input.SampleIntervalMS, input.PublishIntervalMS, input.StaleAfterMS, input.CounterDecreaseMode, input.CounterRolloverModulus, metadata, input.Status, now)
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE core_registry.telemetry_points SET reporting_device_id=$5::uuid,sensor_id=$6::uuid,point_code=$7,source_key=$8,display_name=$9,point_type=$10,value_type=$11,unit=$12,writable=$13,sample_interval_ms=$14,publish_interval_ms=$15,stale_after_ms=$16,counter_decrease_mode=$17,counter_rollover_modulus=$18,source_metadata=$19::jsonb,status=$20,revision=revision+1,updated_at=$21 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, siteID, row.TargetID, row.ExpectedRevision, input.ReportingDeviceID, input.SensorID, input.PointCode, input.SourceKey, input.DisplayName, input.PointType, input.ValueType, input.Unit, input.Writable, input.SampleIntervalMS, input.PublishIntervalMS, input.StaleAfterMS, input.CounterDecreaseMode, input.CounterRolloverModulus, metadata, input.Status, now)
	if err == nil && command.RowsAffected() != 1 {
		return ErrRevisionConflict
	}
	return err
}

func (store *PostgresStore) Retire(ctx context.Context, claims registryauth.GrantClaims, input RetireRequest) (RetirementResult, error) {
	if err := input.Validate(); err != nil {
		return RetirementResult{}, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return RetirementResult{}, err
	}
	result, replayed, err := runRegistryMutation(ctx, store, claims, registryauth.ActionRegistryRetire, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[RetirementResult], error) {
		currentRevision, err := retirementResourceRevision(ctx, tx, claims.TenantID, input)
		if err != nil {
			return mutationRecord[RetirementResult]{}, err
		}
		if currentRevision != input.Meta.ExpectedRevision {
			return mutationRecord[RetirementResult]{}, ErrRevisionConflict
		}
		dependencies, err := retirementDependencyCount(ctx, tx, claims.TenantID, input)
		if err != nil {
			return mutationRecord[RetirementResult]{}, err
		}
		sagaID, err := newCoreUUIDv7(now)
		if err != nil {
			return mutationRecord[RetirementResult]{}, err
		}
		status := "COMPLETED"
		outcome := "COMMITTED"
		var completedAt any = now
		if dependencies > 0 {
			status, outcome, completedAt = "BLOCKED", "BLOCKED", nil
		} else if err := markResourceRetired(ctx, tx, claims.TenantID, input, now); err != nil {
			return mutationRecord[RetirementResult]{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core_registry.registry_retirement_sagas (id,tenant_id,site_id,resource_type,resource_id,expected_revision,status,dependency_count,reason,requested_by,created_at,updated_at,completed_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8,$9,$10::uuid,$11,$11,$12)`, sagaID, claims.TenantID, input.SiteID, string(input.ResourceType), input.ResourceID, input.Meta.ExpectedRevision, status, dependencies, input.Meta.Reason, claims.PrincipalID, now, completedAt); err != nil {
			return mutationRecord[RetirementResult]{}, err
		}
		value := RetirementResult{SagaID: sagaID, ResourceType: input.ResourceType, ResourceID: input.ResourceID, Status: status, DependencyCount: dependencies, Revision: 1}
		eventType := "registry.retirement.completed"
		if status == "BLOCKED" {
			eventType = "registry.retirement.blocked"
		}
		return mutationRecord[RetirementResult]{Result: value, SiteID: &input.SiteID, ResourceType: "RETIREMENT", ResourceID: sagaID, AfterRevision: revisionPointer(1), Outcome: outcome, EventType: eventType, AggregateVersion: 1, Payload: map[string]any{"siteId": input.SiteID, "resourceType": input.ResourceType, "resourceId": input.ResourceID, "dependencyCount": dependencies, "status": status}}, nil
	})
	if err != nil {
		return RetirementResult{}, err
	}
	result.Replayed = replayed
	return result, nil
}

func retirementResourceRevision(ctx context.Context, tx pgx.Tx, tenantID string, input RetireRequest) (int64, error) {
	var revision int64
	var err error
	switch input.ResourceType {
	case ResourceSite:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.sites WHERE tenant_id=$1::uuid AND id=$2::uuid AND status <> 'RETIRED' FOR UPDATE`, tenantID, input.ResourceID).Scan(&revision)
	case ResourceSpace:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.spaces WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED' FOR UPDATE`, tenantID, input.SiteID, input.ResourceID).Scan(&revision)
	case ResourceAsset:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.assets WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED' FOR UPDATE`, tenantID, input.SiteID, input.ResourceID).Scan(&revision)
	case ResourceDevice:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.devices WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED' FOR UPDATE`, tenantID, input.SiteID, input.ResourceID).Scan(&revision)
	case ResourceSensor:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.sensors WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED' FOR UPDATE`, tenantID, input.SiteID, input.ResourceID).Scan(&revision)
	case ResourcePoint:
		err = tx.QueryRow(ctx, `SELECT revision FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND status <> 'RETIRED' FOR UPDATE`, tenantID, input.SiteID, input.ResourceID).Scan(&revision)
	default:
		return 0, ErrInvalidMutation
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return revision, err
}

func retirementDependencyCount(ctx context.Context, tx pgx.Tx, tenantID string, input RetireRequest) (int, error) {
	queries := []struct {
		resource ResourceType
		query    string
	}{
		{ResourceSite, `SELECT (SELECT count(*) FROM core_registry.spaces WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status <> 'RETIRED') + (SELECT count(*) FROM core_registry.assets WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status <> 'RETIRED') + (SELECT count(*) FROM core_registry.devices WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status <> 'RETIRED') + (SELECT count(*) FROM core_registry.sensors WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status <> 'RETIRED') + (SELECT count(*) FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND status <> 'RETIRED')`},
		{ResourceSpace, `SELECT (SELECT count(*) FROM core_registry.spaces WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND parent_space_id=$3::uuid AND status <> 'RETIRED') + (SELECT count(*) FROM core_registry.asset_space_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND space_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.device_space_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND space_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.sensor_space_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND space_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.point_subject_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND subject_type='SPACE' AND space_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL)`},
		{ResourceAsset, `SELECT (SELECT count(*) FROM core_registry.device_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND asset_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.asset_space_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND asset_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.point_subject_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND subject_type='ASSET' AND asset_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL)`},
		{ResourceDevice, `SELECT (SELECT count(*) FROM core_registry.device_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND device_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.device_space_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND device_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.sensor_device_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND device_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND reporting_device_id=$3::uuid AND status <> 'RETIRED')`},
		{ResourceSensor, `SELECT (SELECT count(*) FROM core_registry.sensor_device_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND sensor_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.sensor_space_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND sensor_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL) + (SELECT count(*) FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND sensor_id=$3::uuid AND status <> 'RETIRED')`},
		{ResourcePoint, `SELECT count(*) FROM core_registry.point_subject_bindings WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND point_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL`},
	}
	for _, candidate := range queries {
		if candidate.resource != input.ResourceType {
			continue
		}
		var count int
		var err error
		if input.ResourceType == ResourceSite {
			err = tx.QueryRow(ctx, candidate.query, tenantID, input.SiteID).Scan(&count)
		} else {
			err = tx.QueryRow(ctx, candidate.query, tenantID, input.SiteID, input.ResourceID).Scan(&count)
		}
		return count, err
	}
	return 0, ErrInvalidMutation
}

func markResourceRetired(ctx context.Context, tx pgx.Tx, tenantID string, input RetireRequest, now time.Time) error {
	var command pgconn.CommandTag
	var err error
	switch input.ResourceType {
	case ResourceSite:
		command, err = execRegistryUpdate(ctx, tx, `UPDATE core_registry.sites SET status='RETIRED',revision=revision+1,updated_at=$4 WHERE tenant_id=$1::uuid AND id=$2::uuid AND revision=$3`, tenantID, input.ResourceID, input.Meta.ExpectedRevision, now)
	case ResourceSpace:
		command, err = execRegistryUpdate(ctx, tx, `UPDATE core_registry.spaces SET status='RETIRED',revision=revision+1,updated_at=$5 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, input.SiteID, input.ResourceID, input.Meta.ExpectedRevision, now)
	case ResourceAsset:
		command, err = execRegistryUpdate(ctx, tx, `UPDATE core_registry.assets SET status='RETIRED',revision=revision+1,updated_at=$5 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, input.SiteID, input.ResourceID, input.Meta.ExpectedRevision, now)
	case ResourceDevice:
		command, err = execRegistryUpdate(ctx, tx, `UPDATE core_registry.devices SET status='RETIRED',revision=revision+1,updated_at=$5 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, input.SiteID, input.ResourceID, input.Meta.ExpectedRevision, now)
	case ResourceSensor:
		command, err = execRegistryUpdate(ctx, tx, `UPDATE core_registry.sensors SET status='RETIRED',revision=revision+1,updated_at=$5 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, input.SiteID, input.ResourceID, input.Meta.ExpectedRevision, now)
	case ResourcePoint:
		command, err = execRegistryUpdate(ctx, tx, `UPDATE core_registry.telemetry_points SET status='RETIRED',revision=revision+1,updated_at=$5 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid AND revision=$4`, tenantID, input.SiteID, input.ResourceID, input.Meta.ExpectedRevision, now)
	default:
		return ErrInvalidMutation
	}
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrRevisionConflict
	}
	return nil
}

func execRegistryUpdate(ctx context.Context, tx pgx.Tx, sql string, args ...any) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, sql, args...)
}
