package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func (store *PostgresStore) Rebind(ctx context.Context, claims registryauth.GrantClaims, input RebindRequest) (BindingMutationResult, error) {
	if err := input.Validate(); err != nil {
		return BindingMutationResult{}, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return BindingMutationResult{}, err
	}
	result, replayed, err := runRegistryMutation(ctx, store, claims, registryauth.ActionBindingWrite, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[BindingMutationResult], error) {
		effectiveAt, _ := time.Parse(time.RFC3339Nano, input.EffectiveAt)
		if !input.CloseOnly && input.Kind == BindingGatewaySourceKey {
			if err := validateGatewaySourceKeyEndpoints(ctx, tx, claims.TenantID, input.SiteID, input.SourceID, input.TargetID); err != nil {
				return mutationRecord[BindingMutationResult]{}, err
			}
		}
		closed, err := closeCurrentBinding(ctx, tx, claims.TenantID, input, effectiveAt, now)
		if err != nil {
			return mutationRecord[BindingMutationResult]{}, err
		}
		var (
			bindingID string
			revision  int64 = 1
			validFrom       = effectiveAt
		)
		if input.CloseOnly {
			bindingID = closed.ID
			revision = closed.Revision
			if !closed.ValidFrom.IsZero() {
				validFrom = closed.ValidFrom
			}
		} else {
			bindingID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[BindingMutationResult]{}, err
			}
			if err := insertBinding(ctx, tx, claims.TenantID, bindingID, input, effectiveAt, now); err != nil {
				return mutationRecord[BindingMutationResult]{}, err
			}
		}
		eventType := "registry.binding.rebound"
		payload := map[string]any{"siteId": input.SiteID, "kind": input.Kind, "sourceId": input.SourceID, "targetId": input.TargetID, "role": input.Role}
		if input.CloseOnly {
			eventType = "registry.binding.closed"
			payload["closeOnly"] = true
		}
		value := BindingMutationResult{
			BindingID: bindingID, SiteID: input.SiteID, Kind: string(input.Kind), SourceID: input.SourceID,
			TargetID: input.TargetID, Role: input.Role, Revision: revision, ValidFrom: validFrom.UTC().Format(time.RFC3339Nano),
		}
		return mutationRecord[BindingMutationResult]{
			Result: value, SiteID: &input.SiteID, ResourceType: "BINDING", ResourceID: bindingID,
			AfterRevision: revisionPointer(revision), EventType: eventType, AggregateVersion: revision,
			Payload: payload,
		}, nil
	})
	if err != nil {
		return BindingMutationResult{}, err
	}
	result.Replayed = replayed
	return result, nil
}

func validateGatewaySourceKeyEndpoints(ctx context.Context, tx pgx.Tx, tenantID, siteID, deviceID, gatewayDeviceID string) error {
	var gatewayType, gatewayStatus string
	err := tx.QueryRow(ctx, `SELECT device_type, status FROM core_registry.devices WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid`, tenantID, siteID, gatewayDeviceID).Scan(&gatewayType, &gatewayStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBindingConflict
		}
		return mapRegistryWriteError(err)
	}
	if gatewayType != "GATEWAY" || gatewayStatus != "ACTIVE" {
		return ErrBindingConflict
	}
	var deviceType, deviceStatus string
	err = tx.QueryRow(ctx, `SELECT device_type, status FROM core_registry.devices WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid`, tenantID, siteID, deviceID).Scan(&deviceType, &deviceStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBindingConflict
		}
		return mapRegistryWriteError(err)
	}
	if deviceType == "GATEWAY" || deviceStatus != "ACTIVE" || deviceID == gatewayDeviceID {
		return ErrBindingConflict
	}
	return nil
}

type closedBindingInfo struct {
	ID        string
	Revision  int64
	ValidFrom time.Time
}

type temporalBindingSpec struct {
	table           string
	sourceCol       string
	targetCol       string
	skipPriorRebind func(role string) bool
}

var temporalBindingSpecs = map[BindingKind]temporalBindingSpec{
	BindingDeviceAsset: {
		table:     "core_registry.device_bindings",
		sourceCol: "device_id",
		targetCol: "asset_id",
		skipPriorRebind: func(role string) bool {
			return role == "GATEWAY"
		},
	},
	BindingAssetSpace: {
		table:     "core_registry.asset_space_bindings",
		sourceCol: "asset_id",
		targetCol: "space_id",
		skipPriorRebind: func(role string) bool {
			return role != "INSTALLED_IN"
		},
	},
	BindingDeviceSpace: {
		table:     "core_registry.device_space_bindings",
		sourceCol: "device_id",
		targetCol: "space_id",
		skipPriorRebind: func(role string) bool {
			return role != "INSTALLED_IN"
		},
	},
	BindingSensorDevice: {
		table:     "core_registry.sensor_device_bindings",
		sourceCol: "sensor_id",
		targetCol: "device_id",
	},
	BindingSensorSpace: {
		table:     "core_registry.sensor_space_bindings",
		sourceCol: "sensor_id",
		targetCol: "space_id",
	},
	BindingPointSubject: {
		table:     "core_registry.point_subject_bindings",
		sourceCol: "point_id",
	},
}

func closeTemporalBinding(ctx context.Context, tx pgx.Tx, tenantID string, input RebindRequest, spec temporalBindingSpec, effectiveAt, now time.Time) (closedBindingInfo, error) {
	if !input.CloseOnly && spec.skipPriorRebind != nil && spec.skipPriorRebind(input.Role) {
		return closedBindingInfo{}, nil
	}
	clauses := []string{
		"tenant_id = $1::uuid",
		"site_id = $2::uuid",
		spec.sourceCol + " = $3::uuid",
		"binding_role = $4",
	}
	args := []any{tenantID, input.SiteID, input.SourceID, input.Role}
	if input.CloseOnly {
		if input.Kind == BindingPointSubject {
			args = append(args, input.TargetType)
			clauses = append(clauses, fmt.Sprintf("subject_type = $%d", len(args)))
			if input.TargetID != "" {
				switch input.TargetType {
				case "SPACE":
					args = append(args, input.TargetID)
					clauses = append(clauses, fmt.Sprintf("space_id = $%d::uuid", len(args)))
				case "ASSET":
					args = append(args, input.TargetID)
					clauses = append(clauses, fmt.Sprintf("asset_id = $%d::uuid", len(args)))
				}
			}
		} else if spec.targetCol != "" && input.TargetID != "" {
			args = append(args, input.TargetID)
			clauses = append(clauses, fmt.Sprintf("%s = $%d::uuid", spec.targetCol, len(args)))
		}
	}
	predicate := strings.Join(clauses, " AND ")

	selectSQL := fmt.Sprintf(
		"SELECT id::text, valid_from FROM %s WHERE %s AND status = 'ACTIVE' AND valid_to IS NULL ORDER BY valid_from DESC, id DESC FOR UPDATE",
		spec.table, predicate,
	)
	rows, err := tx.Query(ctx, selectSQL, args...)
	if err != nil {
		return closedBindingInfo{}, mapRegistryWriteError(err)
	}
	type activeRow struct {
		id        string
		validFrom time.Time
	}
	var active []activeRow
	for rows.Next() {
		var item activeRow
		if err := rows.Scan(&item.id, &item.validFrom); err != nil {
			rows.Close()
			return closedBindingInfo{}, err
		}
		active = append(active, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return closedBindingInfo{}, err
	}
	rows.Close()

	if len(active) == 0 {
		if input.CloseOnly {
			return closedBindingInfo{}, ErrNotFound
		}
		return closedBindingInfo{}, nil
	}
	for _, item := range active {
		if !item.validFrom.Before(effectiveAt) {
			return closedBindingInfo{}, ErrBindingConflict
		}
	}

	effIdx := len(args) + 1
	nowIdx := len(args) + 2
	updateArgs := append(append([]any(nil), args...), effectiveAt, now)
	updateSQL := fmt.Sprintf(
		"UPDATE %s SET valid_to = $%d, revision = revision + 1, updated_at = $%d WHERE %s AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $%d RETURNING id::text, revision, valid_from",
		spec.table, effIdx, nowIdx, predicate, effIdx,
	)
	updatedRows, err := tx.Query(ctx, updateSQL, updateArgs...)
	if err != nil {
		return closedBindingInfo{}, mapRegistryWriteError(err)
	}
	var closed []closedBindingInfo
	for updatedRows.Next() {
		var item closedBindingInfo
		if err := updatedRows.Scan(&item.ID, &item.Revision, &item.ValidFrom); err != nil {
			updatedRows.Close()
			return closedBindingInfo{}, err
		}
		closed = append(closed, item)
	}
	if err := updatedRows.Err(); err != nil {
		updatedRows.Close()
		return closedBindingInfo{}, err
	}
	updatedRows.Close()
	if len(closed) == 0 {
		if input.CloseOnly {
			return closedBindingInfo{}, ErrNotFound
		}
		return closedBindingInfo{}, nil
	}
	for _, item := range closed {
		if item.ID == active[0].id {
			return item, nil
		}
	}
	return closed[0], nil
}

func closeCurrentBinding(ctx context.Context, tx pgx.Tx, tenantID string, input RebindRequest, effectiveAt, now time.Time) (closedBindingInfo, error) {
	if spec, ok := temporalBindingSpecs[input.Kind]; ok {
		return closeTemporalBinding(ctx, tx, tenantID, input, spec, effectiveAt, now)
	}
	if input.Kind != BindingGatewaySourceKey {
		return closedBindingInfo{}, ErrInvalidMutation
	}
	if !input.CloseOnly {
		_, err := tx.Exec(ctx, `UPDATE core_registry.gateway_device_source_keys SET status = 'RETIRED', revision = revision + 1, updated_at = $4 WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND device_id = $3::uuid AND status = 'ACTIVE'`, tenantID, input.SiteID, input.SourceID, now)
		return closedBindingInfo{}, mapRegistryWriteError(err)
	}
	var info closedBindingInfo
	var query string
	var args []any
	if input.TargetID != "" {
		query = `UPDATE core_registry.gateway_device_source_keys SET status = 'RETIRED', revision = revision + 1, updated_at = $6 WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND device_id = $3::uuid AND source_key = $4 AND gateway_device_id = $5::uuid AND status = 'ACTIVE' RETURNING id::text, revision`
		args = []any{tenantID, input.SiteID, input.SourceID, input.Role, input.TargetID, now}
	} else {
		query = `UPDATE core_registry.gateway_device_source_keys SET status = 'RETIRED', revision = revision + 1, updated_at = $5 WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND device_id = $3::uuid AND source_key = $4 AND status = 'ACTIVE' RETURNING id::text, revision`
		args = []any{tenantID, input.SiteID, input.SourceID, input.Role, now}
	}
	err := tx.QueryRow(ctx, query, args...).Scan(&info.ID, &info.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return closedBindingInfo{}, ErrNotFound
	}
	if err != nil {
		return closedBindingInfo{}, mapRegistryWriteError(err)
	}
	info.ValidFrom = effectiveAt
	return info, nil
}

func insertBinding(ctx context.Context, tx pgx.Tx, tenantID, id string, input RebindRequest, effectiveAt, now time.Time) error {
	var err error
	switch input.Kind {
	case BindingDeviceAsset:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.device_bindings (id, tenant_id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'ACTIVE',$7,NULL,1,$8,$8)`, id, tenantID, input.SiteID, input.SourceID, input.TargetID, input.Role, effectiveAt, now)
	case BindingAssetSpace:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.asset_space_bindings (id, tenant_id, site_id, asset_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'ACTIVE',$7,NULL,1,$8,$8)`, id, tenantID, input.SiteID, input.SourceID, input.TargetID, input.Role, effectiveAt, now)
	case BindingDeviceSpace:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.device_space_bindings (id, tenant_id, site_id, device_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'ACTIVE',$7,NULL,1,$8,$8)`, id, tenantID, input.SiteID, input.SourceID, input.TargetID, input.Role, effectiveAt, now)
	case BindingSensorDevice:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.sensor_device_bindings (id, tenant_id, site_id, sensor_id, device_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'ACTIVE',$7,NULL,1,$8,$8)`, id, tenantID, input.SiteID, input.SourceID, input.TargetID, input.Role, effectiveAt, now)
	case BindingSensorSpace:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.sensor_space_bindings (id, tenant_id, site_id, sensor_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'ACTIVE',$7,NULL,1,$8,$8)`, id, tenantID, input.SiteID, input.SourceID, input.TargetID, input.Role, effectiveAt, now)
	case BindingPointSubject:
		var spaceID any
		var assetID any
		switch input.TargetType {
		case "SITE":
			if input.TargetID != input.SiteID {
				return ErrBindingConflict
			}
		case "SPACE":
			spaceID = input.TargetID
		case "ASSET":
			assetID = input.TargetID
		default:
			return ErrInvalidMutation
		}
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$8,'ACTIVE',$9,NULL,1,$10,$10)`, id, tenantID, input.SiteID, input.SourceID, input.TargetType, spaceID, assetID, input.Role, effectiveAt, now)
	case BindingGatewaySourceKey:
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.gateway_device_source_keys (id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,'ACTIVE',1,$7,$7)`, id, tenantID, input.SiteID, input.TargetID, input.Role, input.SourceID, now)
	default:
		return ErrInvalidMutation
	}
	return mapRegistryWriteError(err)
}

func (store *PostgresStore) ReleaseTemplate(ctx context.Context, claims registryauth.GrantClaims, input ReleaseTemplateRequest) (TemplateRevision, bool, error) {
	if err := input.Validate(); err != nil {
		return TemplateRevision{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionTemplateManage, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[TemplateRevision], error) {
		payloadJSON, _ := json.Marshal(input.Payload)
		referencesJSON, _ := json.Marshal(input.ReleaseReferences)
		var templateID string
		var templateKind string
		var templateRevision int64
		err := tx.QueryRow(ctx, `SELECT id::text, template_kind, revision FROM core_registry.registry_templates WHERE tenant_id=$1::uuid AND template_key=$2 FOR UPDATE`, claims.TenantID, input.TemplateKey).Scan(&templateID, &templateKind, &templateRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			templateID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[TemplateRevision]{}, err
			}
			templateKind = string(input.TemplateKind)
			templateRevision = 1
			if _, err := tx.Exec(ctx, `INSERT INTO core_registry.registry_templates (id,tenant_id,template_key,template_kind,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4,'ACTIVE',1,$5,$5)`, templateID, claims.TenantID, input.TemplateKey, templateKind, now); err != nil {
				return mutationRecord[TemplateRevision]{}, err
			}
		} else if err != nil {
			return mutationRecord[TemplateRevision]{}, err
		} else {
			if templateKind != string(input.TemplateKind) {
				return mutationRecord[TemplateRevision]{}, ErrInvalidMutation
			}
			templateRevision++
			if _, err := tx.Exec(ctx, `UPDATE core_registry.registry_templates SET revision=$3, updated_at=$4 WHERE tenant_id=$1::uuid AND id=$2::uuid`, claims.TenantID, templateID, templateRevision, now); err != nil {
				return mutationRecord[TemplateRevision]{}, err
			}
		}
		var revisionNumber int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision_number),0)+1 FROM core_registry.registry_template_revisions WHERE tenant_id=$1::uuid AND template_id=$2::uuid`, claims.TenantID, templateID).Scan(&revisionNumber); err != nil {
			return mutationRecord[TemplateRevision]{}, err
		}
		revisionID, err := newCoreUUIDv7(now)
		if err != nil {
			return mutationRecord[TemplateRevision]{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core_registry.registry_template_revisions (id,tenant_id,template_id,revision_number,status,payload,release_references,created_at,released_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'RELEASED',$5::jsonb,$6::jsonb,$7,$7)`, revisionID, claims.TenantID, templateID, revisionNumber, payloadJSON, referencesJSON, now); err != nil {
			return mutationRecord[TemplateRevision]{}, err
		}
		result := TemplateRevision{ID: revisionID, TenantID: claims.TenantID, TemplateID: templateID, TemplateKey: input.TemplateKey, TemplateKind: input.TemplateKind, RevisionNumber: revisionNumber, Status: "RELEASED", Payload: input.Payload, ReleaseReferences: input.ReleaseReferences, ReleasedAt: now.Format(time.RFC3339Nano)}
		return mutationRecord[TemplateRevision]{Result: result, ResourceType: "TEMPLATE_REVISION", ResourceID: revisionID, AfterRevision: revisionPointer(revisionNumber), EventType: "registry.template-revision.released", AggregateVersion: revisionNumber, Payload: map[string]any{"templateId": templateID, "templateKey": input.TemplateKey, "templateKind": input.TemplateKind, "templateRevisionId": revisionID, "revisionNumber": revisionNumber, "releaseReferences": input.ReleaseReferences}}, nil
	})
}

func (store *PostgresStore) AssignTemplate(ctx context.Context, claims registryauth.GrantClaims, input AssignTemplateRequest) (TemplateAssignment, bool, error) {
	if err := input.Validate(); err != nil {
		return TemplateAssignment{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionTemplateManage, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[TemplateAssignment], error) {
		var (
			templateKey  string
			templateKind string
			revStatus    string
			payloadBytes []byte
		)
		err := tx.QueryRow(ctx, `
SELECT t.template_key, t.template_kind, tr.status, tr.payload
FROM core_registry.registry_template_revisions AS tr
JOIN core_registry.registry_templates AS t
  ON t.id = tr.template_id AND t.tenant_id = tr.tenant_id
WHERE tr.tenant_id = $1::uuid AND tr.id = $2::uuid
`, claims.TenantID, input.TemplateRevisionID).Scan(&templateKey, &templateKind, &revStatus, &payloadBytes)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[TemplateAssignment]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[TemplateAssignment]{}, err
		}
		if revStatus != "RELEASED" || templateKind != string(input.TargetType) {
			return mutationRecord[TemplateAssignment]{}, ErrInvalidMutation
		}
		var payloadMap map[string]any
		if len(payloadBytes) > 0 {
			if err := json.Unmarshal(payloadBytes, &payloadMap); err != nil {
				return mutationRecord[TemplateAssignment]{}, err
			}
		}
		activation, err := parseAndValidateTemplatePayload(input.TargetType, payloadMap)
		if err != nil {
			return mutationRecord[TemplateAssignment]{}, err
		}

		var (
			targetStatus string
			deviceCode   string
			deviceType   string
		)
		switch input.TargetType {
		case TemplateAsset:
			err = tx.QueryRow(ctx, `SELECT status FROM core_registry.assets WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid FOR UPDATE`, claims.TenantID, input.SiteID, input.TargetID).Scan(&targetStatus)
		case TemplateDevice:
			err = tx.QueryRow(ctx, `SELECT status, code, device_type FROM core_registry.devices WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid FOR UPDATE`, claims.TenantID, input.SiteID, input.TargetID).Scan(&targetStatus, &deviceCode, &deviceType)
		case TemplatePoint:
			err = tx.QueryRow(ctx, `SELECT status FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND id=$3::uuid FOR UPDATE`, claims.TenantID, input.SiteID, input.TargetID).Scan(&targetStatus)
		default:
			return mutationRecord[TemplateAssignment]{}, ErrInvalidMutation
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[TemplateAssignment]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[TemplateAssignment]{}, err
		}
		if targetStatus == "RETIRED" {
			return mutationRecord[TemplateAssignment]{}, fmt.Errorf("%w: retired target cannot accept template assignment", ErrInvalidMutation)
		}
		if input.TargetType == TemplateDevice && activation != nil && activation.DeviceType != "" && deviceType != activation.DeviceType {
			return mutationRecord[TemplateAssignment]{}, fmt.Errorf("%w: deviceType %q does not match template deviceType %q", ErrBindingConflict, deviceType, activation.DeviceType)
		}

		effectiveAt, _ := time.Parse(time.RFC3339Nano, input.EffectiveAt)
		if _, err := tx.Exec(ctx, `UPDATE core_registry.registry_template_assignments SET valid_to=$5, revision=revision+1, updated_at=$6 WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND target_type=$3 AND target_id=$4::uuid AND status='ACTIVE' AND valid_to IS NULL AND valid_from < $5`, claims.TenantID, input.SiteID, string(input.TargetType), input.TargetID, effectiveAt, now); err != nil {
			return mutationRecord[TemplateAssignment]{}, err
		}
		id, err := newCoreUUIDv7(now)
		if err != nil {
			return mutationRecord[TemplateAssignment]{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core_registry.registry_template_assignments (id,tenant_id,site_id,target_type,target_id,template_revision_id,valid_from,valid_to,status,revision,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,NULL,'ACTIVE',1,$8,$8)`, id, claims.TenantID, input.SiteID, string(input.TargetType), input.TargetID, input.TemplateRevisionID, effectiveAt, now); err != nil {
			return mutationRecord[TemplateAssignment]{}, err
		}

		materializedPointCount := 0
		if input.TargetType == TemplateDevice && activation != nil && len(activation.StandardPoints) > 0 {
			materializedPointCount, err = materializeTemplatePointsInTx(ctx, tx, claims.TenantID, input.SiteID, input.TargetID, deviceCode, templateKey, input.TemplateRevisionID, id, activation, now)
			if err != nil {
				return mutationRecord[TemplateAssignment]{}, err
			}
		}

		result := TemplateAssignment{ID: id, TenantID: claims.TenantID, SiteID: input.SiteID, TargetType: input.TargetType, TargetID: input.TargetID, TemplateRevisionID: input.TemplateRevisionID, ValidFrom: effectiveAt.UTC().Format(time.RFC3339Nano), Revision: 1}
		return mutationRecord[TemplateAssignment]{Result: result, SiteID: &input.SiteID, ResourceType: "TEMPLATE_ASSIGNMENT", ResourceID: id, AfterRevision: revisionPointer(1), EventType: "registry.template-assignment.changed", AggregateVersion: 1, Payload: map[string]any{"siteId": input.SiteID, "targetType": input.TargetType, "targetId": input.TargetID, "templateRevisionId": input.TemplateRevisionID, "templateKey": templateKey, "materializedPointCount": materializedPointCount}}, nil
	})
}
