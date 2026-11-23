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

// TransferDevice transfers a physical Device to another Site or Tenant by retiring the old logical Device
// (once active bindings/points are cleared) and creating a fresh destination logical Device UUIDv7 so
// historical telemetry and tenant boundaries remain strictly isolated.
func (store *PostgresStore) TransferDevice(
	ctx context.Context,
	sourceClaims registryauth.GrantClaims,
	targetClaims *registryauth.GrantClaims,
	input TransferDeviceRequest,
) (TransferDeviceResult, error) {
	if err := input.Validate(); err != nil {
		return TransferDeviceResult{}, err
	}
	if err := ensureSiteScope(sourceClaims, input.SourceSiteID); err != nil {
		return TransferDeviceResult{}, err
	}

	targetTenantID := sourceClaims.TenantID
	if input.TargetTenantID != nil && strings.TrimSpace(*input.TargetTenantID) != "" {
		targetTenantID = strings.TrimSpace(*input.TargetTenantID)
	}

	var (
		transferScope         string
		effectiveTargetClaims registryauth.GrantClaims
	)
	if targetTenantID == sourceClaims.TenantID {
		if input.TargetSiteID == input.SourceSiteID {
			return TransferDeviceResult{}, fmt.Errorf("%w: %w", ErrInvalidMutation, errTransferSameSite)
		}
		if err := ensureSiteScope(sourceClaims, input.TargetSiteID); err != nil {
			return TransferDeviceResult{}, err
		}
		transferScope = TransferScopeCrossSite
		effectiveTargetClaims = sourceClaims
	} else {
		if targetClaims == nil ||
			targetClaims.TenantID != targetTenantID ||
			targetClaims.PrincipalID != sourceClaims.PrincipalID ||
			targetClaims.SubjectIssuer != sourceClaims.SubjectIssuer ||
			targetClaims.Subject != sourceClaims.Subject ||
			len(targetClaims.Actions) != 1 ||
			!registryauth.ActionAllows(targetClaims.Actions[0], registryauth.ActionDeviceWrite) {
			return TransferDeviceResult{}, ErrTransferForbidden
		}
		if err := ensureSiteScope(*targetClaims, input.TargetSiteID); err != nil {
			return TransferDeviceResult{}, ErrTransferForbidden
		}
		transferScope = TransferScopeCrossTenant
		effectiveTargetClaims = *targetClaims
	}

	wipeRequired := input.EffectiveWipeEdgeCache()
	trimmedReason := strings.TrimSpace(input.Meta.Reason)

	result, replayed, err := runRegistryMutation(
		ctx,
		store,
		sourceClaims,
		registryauth.ActionDeviceWrite,
		input.Meta.IdempotencyKey,
		trimmedReason,
		input.canonicalDigestPayload(targetTenantID),
		func(tx pgx.Tx, now time.Time) (mutationRecord[TransferDeviceResult], error) {
			currentRow := tx.QueryRow(ctx, `
SELECT `+deviceColumns+`
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
FOR UPDATE
`, sourceClaims.TenantID, input.SourceSiteID, input.DeviceID)
			current, err := scanDevice(currentRow)
			if errors.Is(err, pgx.ErrNoRows) {
				return mutationRecord[TransferDeviceResult]{}, ErrNotFound
			}
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("lock Registry Device for transfer: %w", err)
			}
			if current.Status != "ACTIVE" {
				return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("%w: %w", ErrInvalidMutation, errTransferInactiveDevice)
			}
			if current.Revision != input.Meta.ExpectedRevision {
				return mutationRecord[TransferDeviceResult]{}, ErrRevisionConflict
			}

			dependencies, err := retirementDependencyCount(ctx, tx, sourceClaims.TenantID, RetireRequest{
				SiteID:       input.SourceSiteID,
				ResourceType: ResourceDevice,
				ResourceID:   input.DeviceID,
			})
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, err
			}
			if dependencies > 0 {
				return mutationRecord[TransferDeviceResult]{}, ErrTransferBlocked
			}

			retiredRow := tx.QueryRow(ctx, `
UPDATE core_registry.devices
SET status = 'RETIRED',
    revision = revision + 1,
    updated_at = $5
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4 AND status = 'ACTIVE'
RETURNING `+deviceColumns+`
`, sourceClaims.TenantID, input.SourceSiteID, current.ID, current.Revision, now)
			retiredSource, err := scanDevice(retiredRow)
			if errors.Is(err, pgx.ErrNoRows) {
				return mutationRecord[TransferDeviceResult]{}, ErrRevisionConflict
			}
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, mapRegistryWriteError(err)
			}

			if transferScope == TransferScopeCrossTenant {
				if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, targetTenantID); err != nil {
					return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("switch RLS tenant to transfer destination: %w", err)
				}
				if _, err := tx.Exec(ctx, `SELECT set_config('app.authorized_site_ids', $1, true)`, postgresUUIDArray(effectiveTargetClaims.AllowedSiteIDs)); err != nil {
					return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("switch RLS site scope to transfer destination: %w", err)
				}
			}

			var targetSiteStatus string
			if err := tx.QueryRow(ctx, `
SELECT status
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid
FOR SHARE
`, targetTenantID, input.TargetSiteID).Scan(&targetSiteStatus); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return mutationRecord[TransferDeviceResult]{}, ErrNotFound
				}
				return mutationRecord[TransferDeviceResult]{}, err
			}
			if targetSiteStatus != "ACTIVE" {
				return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("%w: %w", ErrInvalidMutation, errTransferInactiveTarget)
			}

			targetDeviceID, err := newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, err
			}
			nextCode := current.Code
			if input.NewCode != nil {
				nextCode = *input.NewCode
			}
			nextDisplayName := current.DisplayName
			if input.NewDisplayName != nil {
				nextDisplayName = strings.TrimSpace(*input.NewDisplayName)
			}
			connectivityMode := current.ConnectivityMode
			if connectivityMode == "" {
				connectivityMode = "GATEWAY_PROXIED"
			}
			protocolParam, err := marshalOptionalProtocolAddressParam(current.ProtocolAddress)
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, err
			}

			insertedRow := tx.QueryRow(ctx, `
INSERT INTO core_registry.devices (
  id, tenant_id, site_id, code, display_name, device_type, status,
  external_id, protocol_address, tags, connectivity_mode, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4, $5, $6, 'ACTIVE',
  $7, $8::jsonb, '{}'::jsonb, $9, 1, $10, $10
)
RETURNING `+deviceColumns+`
`, targetDeviceID, targetTenantID, input.TargetSiteID, nextCode, nextDisplayName, current.DeviceType,
				current.ExternalID, protocolParam, connectivityMode, now)
			createdTarget, err := scanDevice(insertedRow)
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, mapRegistryWriteError(err)
			}

			targetOutboxID, err := newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, err
			}
			targetOutboxPayload, err := json.Marshal(map[string]any{
				"siteId":         input.TargetSiteID,
				"principalId":    effectiveTargetClaims.PrincipalID,
				"policyRevision": effectiveTargetClaims.PolicyRevision,
				"reason":         trimmedReason,
			})
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("encode target device outbox payload: %w", err)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.domain_outbox_events (
  id, tenant_id, site_id, event_type, schema_version, subject_type, subject_id,
  aggregate_type, aggregate_id, aggregate_version, occurred_at, payload, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, 'registry.device.created', 1, 'DEVICE', $4::uuid, 'DEVICE', $4::uuid, 1, $5, $6::jsonb, $5)
`, targetOutboxID, targetTenantID, input.TargetSiteID, createdTarget.ID, now, targetOutboxPayload); err != nil {
				return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("insert target device outbox event: %w", err)
			}

			if transferScope == TransferScopeCrossTenant {
				targetAuditID, err := newCoreUUIDv7(now)
				if err != nil {
					return mutationRecord[TransferDeviceResult]{}, err
				}
				if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.registry_audit_facts (
  event_id, tenant_id, site_id, principal_id, action, resource_type, resource_id,
  before_revision, after_revision, outcome, reason, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, 'DEVICE', $6::uuid, NULL, 1, 'COMMITTED', $7, $8)
`, targetAuditID, targetTenantID, input.TargetSiteID, effectiveTargetClaims.PrincipalID, string(registryauth.ActionDeviceWrite), createdTarget.ID, trimmedReason, now); err != nil {
					return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("insert target tenant device audit fact: %w", err)
				}
				if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, sourceClaims.TenantID); err != nil {
					return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("restore RLS tenant after transfer destination insert: %w", err)
				}
				if _, err := tx.Exec(ctx, `SELECT set_config('app.authorized_site_ids', $1, true)`, postgresUUIDArray(sourceClaims.AllowedSiteIDs)); err != nil {
					return mutationRecord[TransferDeviceResult]{}, fmt.Errorf("restore RLS site scope after transfer destination insert: %w", err)
				}
			}

			transferID, err := newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[TransferDeviceResult]{}, err
			}
			transferredBy := resolveReplacementActor(sourceClaims)
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.device_transfers (
  id, source_tenant_id, source_site_id, source_device_id,
  target_tenant_id, target_site_id, target_device_id,
  transfer_scope, hardware_external_id, wipe_required,
  reason, transferred_by, source_device_revision, transferred_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
  $5::uuid, $6::uuid, $7::uuid,
  $8, $9, $10,
  $11, $12, $13, $14
)
`, transferID, sourceClaims.TenantID, input.SourceSiteID, retiredSource.ID,
				targetTenantID, input.TargetSiteID, createdTarget.ID,
				transferScope, current.ExternalID, wipeRequired,
				trimmedReason, transferredBy, retiredSource.Revision, now); err != nil {
				return mutationRecord[TransferDeviceResult]{}, mapRegistryWriteError(err)
			}

			out := TransferDeviceResult{
				TransferID:           transferID,
				TransferScope:        transferScope,
				SourceTenantID:       sourceClaims.TenantID,
				SourceSiteID:         input.SourceSiteID,
				SourceDeviceID:       retiredSource.ID,
				SourceDeviceRevision: retiredSource.Revision,
				TargetTenantID:       targetTenantID,
				TargetSiteID:         input.TargetSiteID,
				TargetDeviceID:       createdTarget.ID,
				HardwareExternalID:   current.ExternalID,
				WipeRequired:         wipeRequired,
				Reason:               trimmedReason,
				TransferredBy:        transferredBy,
				TransferredAt:        formatInstant(now),
				SourceDevice:         retiredSource,
				TargetDevice:         createdTarget,
				Replayed:             false,
			}
			beforeRevision := current.Revision
			return entityMutationRecord(
				out,
				&beforeRevision,
				"DEVICE",
				retiredSource.ID,
				retiredSource.Revision,
				"registry.device.transferred",
				map[string]any{
					"siteId":           input.SourceSiteID,
					"transferId":       transferID,
					"transferScope":    transferScope,
					"sourceDeviceId":   retiredSource.ID,
					"targetTenantId":   targetTenantID,
					"targetSiteId":     input.TargetSiteID,
					"targetDeviceId":   createdTarget.ID,
					"deviceType":       retiredSource.DeviceType,
					"connectivityMode": retiredSource.ConnectivityMode,
					"wipeRequired":     wipeRequired,
				},
			), nil
		},
	)
	if err != nil {
		return TransferDeviceResult{}, err
	}
	result.Replayed = replayed
	return result, nil
}
