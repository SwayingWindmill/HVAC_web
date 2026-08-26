package core

import (
	"context"
	"encoding/json"
	"errors"
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
		id, err := newCoreUUIDv7(now)
		if err != nil {
			return mutationRecord[BindingMutationResult]{}, err
		}
		if err := closeCurrentBinding(ctx, tx, input, effectiveAt, now); err != nil {
			return mutationRecord[BindingMutationResult]{}, err
		}
		if err := insertBinding(ctx, tx, claims.TenantID, id, input, effectiveAt, now); err != nil {
			return mutationRecord[BindingMutationResult]{}, err
		}
		value := BindingMutationResult{
			BindingID: id, SiteID: input.SiteID, Kind: string(input.Kind), SourceID: input.SourceID,
			TargetID: input.TargetID, Role: input.Role, Revision: 1, ValidFrom: effectiveAt.UTC().Format(time.RFC3339Nano),
		}
		return mutationRecord[BindingMutationResult]{
			Result: value, SiteID: &input.SiteID, ResourceType: "BINDING", ResourceID: id,
			AfterRevision: revisionPointer(1), EventType: "registry.binding.rebound", AggregateVersion: 1,
			Payload: map[string]any{"siteId": input.SiteID, "kind": input.Kind, "sourceId": input.SourceID, "targetId": input.TargetID, "role": input.Role},
		}, nil
	})
	if err != nil {
		return BindingMutationResult{}, err
	}
	result.Replayed = replayed
	return result, nil
}

func closeCurrentBinding(ctx context.Context, tx pgx.Tx, input RebindRequest, effectiveAt, now time.Time) error {
	var err error
	switch input.Kind {
	case BindingDeviceAsset:
		if input.Role == "GATEWAY" {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE core_registry.device_bindings SET valid_to = $4, revision = revision + 1, updated_at = $5 WHERE site_id = $1::uuid AND device_id = $2::uuid AND binding_role = $3 AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $4`, input.SiteID, input.SourceID, input.Role, effectiveAt, now)
	case BindingAssetSpace:
		if input.Role != "INSTALLED_IN" {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE core_registry.asset_space_bindings SET valid_to = $4, revision = revision + 1, updated_at = $5 WHERE site_id = $1::uuid AND asset_id = $2::uuid AND binding_role = $3 AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $4`, input.SiteID, input.SourceID, input.Role, effectiveAt, now)
	case BindingDeviceSpace:
		if input.Role != "INSTALLED_IN" {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE core_registry.device_space_bindings SET valid_to = $4, revision = revision + 1, updated_at = $5 WHERE site_id = $1::uuid AND device_id = $2::uuid AND binding_role = $3 AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $4`, input.SiteID, input.SourceID, input.Role, effectiveAt, now)
	case BindingSensorDevice:
		_, err = tx.Exec(ctx, `UPDATE core_registry.sensor_device_bindings SET valid_to = $4, revision = revision + 1, updated_at = $5 WHERE site_id = $1::uuid AND sensor_id = $2::uuid AND binding_role = $3 AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $4`, input.SiteID, input.SourceID, input.Role, effectiveAt, now)
	case BindingSensorSpace:
		_, err = tx.Exec(ctx, `UPDATE core_registry.sensor_space_bindings SET valid_to = $4, revision = revision + 1, updated_at = $5 WHERE site_id = $1::uuid AND sensor_id = $2::uuid AND binding_role = $3 AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $4`, input.SiteID, input.SourceID, input.Role, effectiveAt, now)
	case BindingPointSubject:
		_, err = tx.Exec(ctx, `UPDATE core_registry.point_subject_bindings SET valid_to = $4, revision = revision + 1, updated_at = $5 WHERE site_id = $1::uuid AND point_id = $2::uuid AND binding_role = $3 AND status = 'ACTIVE' AND valid_to IS NULL AND valid_from < $4`, input.SiteID, input.SourceID, input.Role, effectiveAt, now)
	default:
		return ErrInvalidMutation
	}
	return mapRegistryWriteError(err)
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
		return mutationRecord[TemplateRevision]{Result: result, ResourceType: "TEMPLATE_REVISION", ResourceID: revisionID, AfterRevision: revisionPointer(revisionNumber), EventType: "registry.template-revision.released", AggregateVersion: revisionNumber, Payload: map[string]any{"templateId": templateID, "templateKey": input.TemplateKey, "templateKind": input.TemplateKind, "releaseReferences": input.ReleaseReferences}}, nil
	})
}

func (store *PostgresStore) AssignTemplate(ctx context.Context, claims registryauth.GrantClaims, input AssignTemplateRequest) (TemplateAssignment, bool, error) {
	if err := input.Validate(); err != nil {
		return TemplateAssignment{}, false, err
	}
	return runRegistryMutation(ctx, store, claims, registryauth.ActionTemplateManage, input.Meta.IdempotencyKey, input.Meta.Reason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[TemplateAssignment], error) {
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
		result := TemplateAssignment{ID: id, TenantID: claims.TenantID, SiteID: input.SiteID, TargetType: input.TargetType, TargetID: input.TargetID, TemplateRevisionID: input.TemplateRevisionID, ValidFrom: effectiveAt.UTC().Format(time.RFC3339Nano), Revision: 1}
		return mutationRecord[TemplateAssignment]{Result: result, SiteID: &input.SiteID, ResourceType: "TEMPLATE_ASSIGNMENT", ResourceID: id, AfterRevision: revisionPointer(1), EventType: "registry.template-assignment.changed", AggregateVersion: 1, Payload: map[string]any{"siteId": input.SiteID, "targetType": input.TargetType, "targetId": input.TargetID, "templateRevisionId": input.TemplateRevisionID}}, nil
	})
}
