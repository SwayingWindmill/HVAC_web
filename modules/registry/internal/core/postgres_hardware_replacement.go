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

// ReplaceDeviceHardware swaps hardware identity or parent gateway routing for an existing logical Device
// without changing devices.id, assets.id, or telemetry_points.id.
func (store *PostgresStore) ReplaceDeviceHardware(
	ctx context.Context,
	claims registryauth.GrantClaims,
	input ReplaceDeviceHardwareRequest,
) (ReplaceDeviceHardwareResult, error) {
	if err := input.Validate(); err != nil {
		return ReplaceDeviceHardwareResult{}, err
	}
	if err := ensureSiteScope(claims, input.SiteID); err != nil {
		return ReplaceDeviceHardwareResult{}, err
	}
	trimmedExternalID := strings.TrimSpace(input.NewExternalID)
	var trimmedGatewayID *string
	if input.NewGatewayDeviceID != nil {
		gw := strings.TrimSpace(*input.NewGatewayDeviceID)
		trimmedGatewayID = &gw
	}

	result, replayed, err := runRegistryMutation(
		ctx,
		store,
		claims,
		registryauth.ActionDeviceWrite,
		input.Meta.IdempotencyKey,
		input.Meta.Reason,
		input.canonicalDigestPayload(),
		func(tx pgx.Tx, now time.Time) (mutationRecord[ReplaceDeviceHardwareResult], error) {
			currentRow := tx.QueryRow(ctx, `
SELECT `+deviceColumns+`
FROM core_registry.devices
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
FOR UPDATE
`, claims.TenantID, input.SiteID, input.DeviceID)
			current, err := scanDevice(currentRow)
			if errors.Is(err, pgx.ErrNoRows) {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, ErrNotFound
			}
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, fmt.Errorf("lock Registry Device for hardware replacement: %w", err)
			}
			if current.Status != "ACTIVE" {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, fmt.Errorf("%w: %w", ErrInvalidMutation, errReplaceDeviceInactive)
			}
			if current.Revision != input.Meta.ExpectedRevision {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, ErrRevisionConflict
			}
			if current.DeviceType == "GATEWAY" && (trimmedGatewayID != nil || input.NewSourceKey != nil) {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, ErrInvalidMutation
			}

			previousGatewayID, effectiveGatewayID, err := updateDeviceGatewaySourceKey(
				ctx, tx, claims.TenantID, input.SiteID, current, trimmedGatewayID, input.NewSourceKey, now,
			)
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, err
			}

			previousExternalID, err := syncDeviceExternalIdentifier(
				ctx, tx, claims.TenantID, input.SiteID, current, trimmedExternalID, now,
			)
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, err
			}

			nextProtocolAddress := current.ProtocolAddress
			if input.NewProtocolAddress != nil {
				nextProtocolAddress = cloneProtocolMap(input.NewProtocolAddress)
			}
			nextProtocolParam, err := marshalOptionalProtocolAddressParam(nextProtocolAddress)
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, err
			}
			prevProtocolParam, err := marshalOptionalProtocolAddressParam(current.ProtocolAddress)
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, err
			}

			updatedRow := tx.QueryRow(ctx, `
UPDATE core_registry.devices
SET external_id = $5,
    protocol_address = $6::jsonb,
    revision = revision + 1,
    updated_at = $7
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $4
RETURNING `+deviceColumns+`
`, claims.TenantID, input.SiteID, current.ID, current.Revision, trimmedExternalID, nextProtocolParam, now)
			updated, err := scanDevice(updatedRow)
			if errors.Is(err, pgx.ErrNoRows) {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, ErrRevisionConflict
			}
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, mapRegistryWriteError(err)
			}

			replacementID, err := newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, err
			}
			replacedBy := resolveReplacementActor(claims)
			reason := strings.TrimSpace(input.Meta.Reason)
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.device_hardware_replacements (
  id, tenant_id, site_id, device_id,
  previous_external_id, new_external_id,
  previous_gateway_device_id, new_gateway_device_id,
  previous_protocol_address, new_protocol_address,
  reason, replaced_by, device_revision, replaced_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid,
  $5, $6,
  $7::uuid, $8::uuid,
  $9::jsonb, $10::jsonb,
  $11, $12, $13, $14
)
`, replacementID, claims.TenantID, input.SiteID, updated.ID,
				previousExternalID, trimmedExternalID,
				previousGatewayID, effectiveGatewayID,
				prevProtocolParam, nextProtocolParam,
				reason, replacedBy, updated.Revision, now); err != nil {
				return mutationRecord[ReplaceDeviceHardwareResult]{}, mapRegistryWriteError(err)
			}

			out := ReplaceDeviceHardwareResult{
				ReplacementID:           replacementID,
				TenantID:                claims.TenantID,
				SiteID:                  input.SiteID,
				DeviceID:                updated.ID,
				Revision:                updated.Revision,
				PreviousExternalID:      previousExternalID,
				NewExternalID:           trimmedExternalID,
				PreviousGatewayDeviceID: previousGatewayID,
				NewGatewayDeviceID:      effectiveGatewayID,
				PreviousProtocolAddress: cloneProtocolMap(current.ProtocolAddress),
				NewProtocolAddress:      cloneProtocolMap(nextProtocolAddress),
				Reason:                  reason,
				ReplacedBy:              replacedBy,
				ReplacedAt:              formatInstant(now),
				Device:                  updated,
				Replayed:                false,
			}
			beforeRevision := current.Revision
			return entityMutationRecord(
				out,
				&beforeRevision,
				"DEVICE",
				updated.ID,
				updated.Revision,
				"registry.device.hardware_replaced",
				map[string]any{
					"siteId":                  input.SiteID,
					"replacementId":           replacementID,
					"previousExternalId":      previousExternalID,
					"newExternalId":           trimmedExternalID,
					"previousGatewayDeviceId": previousGatewayID,
					"newGatewayDeviceId":      effectiveGatewayID,
				},
			), nil
		},
	)
	if err != nil {
		return ReplaceDeviceHardwareResult{}, err
	}
	result.Replayed = replayed
	return result, nil
}

func updateDeviceGatewaySourceKey(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, siteID string,
	device Device,
	requestedGatewayID *string,
	requestedSourceKey *string,
	now time.Time,
) (*string, *string, error) {
	var (
		activeRowID     string
		activeGatewayID string
		activeSourceKey string
	)
	err := tx.QueryRow(ctx, `
SELECT id::text, gateway_device_id::text, source_key
FROM core_registry.gateway_device_source_keys
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND device_id = $3::uuid AND status = 'ACTIVE'
FOR UPDATE
`, tenantID, siteID, device.ID).Scan(&activeRowID, &activeGatewayID, &activeSourceKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("lookup active gateway source key: %w", err)
	}

	var previousGatewayID *string
	if activeGatewayID != "" {
		prev := activeGatewayID
		previousGatewayID = &prev
	}
	effectiveGatewayID := previousGatewayID
	if requestedGatewayID != nil {
		if err := validateGatewaySourceKeyEndpoints(ctx, tx, tenantID, siteID, device.ID, *requestedGatewayID); err != nil {
			return nil, nil, err
		}
		nextGW := *requestedGatewayID
		effectiveGatewayID = &nextGW
	}

	gatewayChanged := requestedGatewayID != nil && *requestedGatewayID != activeGatewayID
	sourceKeyChanged := requestedSourceKey != nil && *requestedSourceKey != activeSourceKey
	if !gatewayChanged && !sourceKeyChanged {
		return previousGatewayID, effectiveGatewayID, nil
	}
	if effectiveGatewayID == nil {
		return previousGatewayID, effectiveGatewayID, ErrInvalidMutation
	}

	nextSourceKey := activeSourceKey
	if requestedSourceKey != nil {
		nextSourceKey = *requestedSourceKey
	} else if nextSourceKey == "" {
		if !sourceKeyPattern.MatchString(device.Code) {
			return nil, nil, ErrInvalidMutation
		}
		nextSourceKey = device.Code
	}

	if activeRowID != "" {
		if _, err := tx.Exec(ctx, `
UPDATE core_registry.gateway_device_source_keys
SET status = 'RETIRED', revision = revision + 1, updated_at = $2
WHERE id = $1::uuid AND status = 'ACTIVE'
`, activeRowID, now); err != nil {
			return nil, nil, mapRegistryWriteError(err)
		}
	}

	newRowID, err := newCoreUUIDv7(now)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.gateway_device_source_keys (
  id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, 'ACTIVE', 1, $7, $7
)
`, newRowID, tenantID, siteID, *effectiveGatewayID, nextSourceKey, device.ID, now); err != nil {
		return nil, nil, mapRegistryWriteError(err)
	}
	return previousGatewayID, effectiveGatewayID, nil
}

func syncDeviceExternalIdentifier(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, siteID string,
	device Device,
	newExternalID string,
	now time.Time,
) (*string, error) {
	rows, err := tx.Query(ctx, `
SELECT id::text, external_id
FROM core_registry.registry_external_ids
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND resource_type = 'DEVICE' AND resource_id = $3::uuid AND status = 'ACTIVE'
FOR UPDATE
`, tenantID, siteID, device.ID)
	if err != nil {
		return nil, fmt.Errorf("lookup Device external ID mappings: %w", err)
	}
	type extRow struct {
		id         string
		externalID string
	}
	var mappings []extRow
	for rows.Next() {
		var row extRow
		if scanErr := rows.Scan(&row.id, &row.externalID); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scan Device external ID mapping: %w", scanErr)
		}
		mappings = append(mappings, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate Device external ID mappings: %w", rowsErr)
	}
	rows.Close()

	previousExternalID := device.ExternalID
	if previousExternalID == nil && len(mappings) > 0 {
		first := mappings[0].externalID
		previousExternalID = &first
	}

	for _, mapping := range mappings {
		if mapping.externalID == newExternalID {
			continue
		}
		if _, err := tx.Exec(ctx, `
UPDATE core_registry.registry_external_ids
SET external_id = $2, revision = revision + 1, updated_at = $3
WHERE id = $1::uuid AND status = 'ACTIVE'
`, mapping.id, newExternalID, now); err != nil {
			return nil, mapRegistryWriteError(err)
		}
	}
	return previousExternalID, nil
}

func marshalOptionalProtocolAddressParam(address map[string]any) (any, error) {
	if address == nil {
		return nil, nil
	}
	raw, err := json.Marshal(address)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxProtocolAddressBytes {
		return nil, ErrInvalidMutation
	}
	return string(raw), nil
}

func cloneProtocolMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var copied map[string]any
	if err := json.Unmarshal(raw, &copied); err != nil {
		return nil
	}
	return copied
}

func resolveReplacementActor(claims registryauth.GrantClaims) string {
	actor := strings.TrimSpace(claims.Subject)
	if actor == "" {
		actor = strings.TrimSpace(claims.PrincipalID)
	}
	return truncateRelayUTF8(actor, 256)
}
