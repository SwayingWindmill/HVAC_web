package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	SiteControlModeRemoteAuto       = "REMOTE_AUTO"
	SiteControlModeManualOverride   = "MANUAL_OVERRIDE"
	SiteControlModeSafeShutdownExit = "SAFE_SHUTDOWN_EXIT"

	AssetControlLockUnlocked    = "UNLOCKED"
	AssetControlLockManual      = "MANUAL_LOCK"
	AssetControlLockMaintenance = "MAINTENANCE_LOCK"
)

type AssetControlLockState struct {
	ID             string   `json:"id,omitempty"`
	TenantID       string   `json:"tenantId,omitempty"`
	SiteID         string   `json:"siteId"`
	AssetID        string   `json:"assetId"`
	LockMode       string   `json:"lockMode"`
	Reason         string   `json:"reason"`
	UpdatedBy      *string  `json:"updatedBy"`
	UpdatedAt      *string  `json:"updatedAt"`
	Revision       int64    `json:"revision"`
	BoundDeviceIDs []string `json:"boundDeviceIds,omitempty"`
}

type ActiveMaintenanceWindow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Reason    string `json:"reason"`
}

type SiteControlState struct {
	ID                      *string                  `json:"id,omitempty"`
	TenantID                string                   `json:"tenantId"`
	SiteID                  string                   `json:"siteId"`
	ControlMode             string                   `json:"controlMode"`
	Reason                  string                   `json:"reason"`
	UpdatedBy               *string                  `json:"updatedBy"`
	UpdatedAt               *string                  `json:"updatedAt"`
	Revision                int64                    `json:"revision"`
	AssetLocks              []AssetControlLockState  `json:"assetLocks"`
	ActiveMaintenanceWindow *ActiveMaintenanceWindow `json:"activeMaintenanceWindow,omitempty"`
}

type SiteControlModeMutation struct {
	SiteID      string       `json:"siteId,omitempty"`
	ControlMode string       `json:"controlMode"`
	Reason      string       `json:"reason,omitempty"`
	Meta        MutationMeta `json:"meta"`
}

type AssetControlLockMutation struct {
	AssetID  string       `json:"assetId,omitempty"`
	LockMode string       `json:"lockMode"`
	Reason   string       `json:"reason,omitempty"`
	Meta     MutationMeta `json:"meta"`
}

func validControlLockMeta(meta MutationMeta, reason string) error {
	if !writeKeyPattern.MatchString(meta.IdempotencyKey) || strings.TrimSpace(meta.Reason) == "" ||
		utf8.RuneCountInString(meta.Reason) > 1000 || utf8.RuneCountInString(reason) > 1000 || meta.ExpectedRevision < 0 {
		return ErrInvalidMutation
	}
	return nil
}

func (input SiteControlModeMutation) Validate() error {
	if !validUUIDv7(input.SiteID) {
		return ErrInvalidMutation
	}
	switch input.ControlMode {
	case SiteControlModeRemoteAuto, SiteControlModeManualOverride, SiteControlModeSafeShutdownExit:
	default:
		return ErrInvalidMutation
	}
	return validControlLockMeta(input.Meta, input.Reason)
}

func (input AssetControlLockMutation) Validate() error {
	if !validUUIDv7(input.AssetID) {
		return ErrInvalidMutation
	}
	switch input.LockMode {
	case AssetControlLockUnlocked, AssetControlLockManual, AssetControlLockMaintenance:
	default:
		return ErrInvalidMutation
	}
	return validControlLockMeta(input.Meta, input.Reason)
}

func effectiveControlReason(explicitReason string, metaReason string) string {
	trimmed := strings.TrimSpace(explicitReason)
	if trimmed == "" {
		return strings.TrimSpace(metaReason)
	}
	return trimmed
}

func (store *PostgresStore) GetSiteControlState(ctx context.Context, claims registryauth.GrantClaims, siteID string) (SiteControlState, error) {
	if !validUUIDv7(siteID) {
		return SiteControlState{}, ErrNotFound
	}
	var state SiteControlState
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var tenantID, timezone string
		err := transaction.QueryRow(ctx, `
SELECT tenant_id::text, timezone
FROM core_registry.sites
WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&tenantID, &timezone)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query Site for control state: %w", err)
		}

		state = SiteControlState{
			TenantID:    tenantID,
			SiteID:      siteID,
			ControlMode: SiteControlModeRemoteAuto,
			AssetLocks:  []AssetControlLockState{},
		}

		var stateID, updatedBy string
		var updatedAt time.Time
		err = transaction.QueryRow(ctx, `
SELECT id::text, mode, reason, updated_by::text, revision, updated_at
FROM core_registry.site_control_states
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
`, tenantID, siteID).Scan(&stateID, &state.ControlMode, &state.Reason, &updatedBy, &state.Revision, &updatedAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query Site control state: %w", err)
		}
		if err == nil {
			updatedAtText := formatInstant(updatedAt)
			state.ID = &stateID
			state.UpdatedBy = &updatedBy
			state.UpdatedAt = &updatedAtText
		}

		state.AssetLocks, err = querySiteAssetLocks(ctx, transaction, tenantID, siteID)
		if err != nil {
			return err
		}
		state.ActiveMaintenanceWindow, err = queryActiveMaintenanceWindow(ctx, transaction, tenantID, siteID, timezone, time.Now().UTC())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return SiteControlState{}, err
	}
	return state, nil
}

func querySiteAssetLocks(ctx context.Context, transaction pgx.Tx, tenantID, siteID string) ([]AssetControlLockState, error) {
	boundByAsset, err := queryActiveAssetDeviceIDs(ctx, transaction, tenantID, siteID, "")
	if err != nil {
		return nil, err
	}
	rows, err := transaction.Query(ctx, `
SELECT id::text, asset_id::text, lock_mode, reason, updated_by::text, revision, updated_at
FROM core_registry.asset_control_locks
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY asset_id::text ASC
`, tenantID, siteID)
	if err != nil {
		return nil, fmt.Errorf("query Site asset control locks: %w", err)
	}
	defer rows.Close()
	locks := []AssetControlLockState{}
	for rows.Next() {
		var item AssetControlLockState
		var itemUpdatedBy string
		var itemUpdatedAt time.Time
		if err := rows.Scan(&item.ID, &item.AssetID, &item.LockMode, &item.Reason, &itemUpdatedBy, &item.Revision, &itemUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan asset control lock: %w", err)
		}
		itemUpdatedAtText := formatInstant(itemUpdatedAt)
		item.TenantID = tenantID
		item.SiteID = siteID
		item.UpdatedBy = &itemUpdatedBy
		item.UpdatedAt = &itemUpdatedAtText
		if devices := boundByAsset[item.AssetID]; len(devices) > 0 {
			item.BoundDeviceIDs = devices
		}
		locks = append(locks, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate asset control locks: %w", err)
	}
	return locks, nil
}

func queryActiveAssetDeviceIDs(ctx context.Context, transaction pgx.Tx, tenantID, siteID, assetID string) (map[string][]string, error) {
	rows, err := transaction.Query(ctx, `
SELECT DISTINCT asset_id::text, device_id::text
FROM core_registry.device_bindings
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND ($3 = '' OR asset_id = $3::uuid)
  AND status = 'ACTIVE'
  AND valid_to IS NULL
ORDER BY asset_id::text ASC, device_id::text ASC
`, tenantID, siteID, assetID)
	if err != nil {
		return nil, fmt.Errorf("query bound device IDs for asset control lock: %w", err)
	}
	defer rows.Close()
	bound := map[string][]string{}
	for rows.Next() {
		var rowAssetID, deviceID string
		if err := rows.Scan(&rowAssetID, &deviceID); err != nil {
			return nil, fmt.Errorf("scan bound device ID: %w", err)
		}
		bound[rowAssetID] = append(bound[rowAssetID], deviceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bound device IDs: %w", err)
	}
	return bound, nil
}

func (store *PostgresStore) SaveSiteControlMode(ctx context.Context, claims registryauth.GrantClaims, input SiteControlModeMutation) (SiteControlState, bool, error) {
	if err := input.Validate(); err != nil {
		return SiteControlState{}, false, err
	}
	reason := effectiveControlReason(input.Reason, input.Meta.Reason)
	trimmedMetaReason := strings.TrimSpace(input.Meta.Reason)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionScheduleManage, input.Meta.IdempotencyKey, trimmedMetaReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[SiteControlState], error) {
		var timezone string
		err := tx.QueryRow(ctx, `
SELECT timezone
FROM core_registry.sites
WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&timezone)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[SiteControlState]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[SiteControlState]{}, fmt.Errorf("verify Site for control mode save: %w", err)
		}

		var stateID string
		var currentRevision int64
		err = tx.QueryRow(ctx, `
SELECT id::text, revision
FROM core_registry.site_control_states
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
FOR UPDATE
`, claims.TenantID, input.SiteID).Scan(&stateID, &currentRevision)

		var nextRevision int64
		var beforeRevision *int64
		var eventType string
		if errors.Is(err, pgx.ErrNoRows) {
			if input.Meta.ExpectedRevision != 0 {
				return mutationRecord[SiteControlState]{}, ErrRevisionConflict
			}
			stateID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[SiteControlState]{}, err
			}
			nextRevision = 1
			eventType = "registry.site-control-state.created"
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.site_control_states (
  id, tenant_id, site_id, mode, reason, updated_by, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, 1, $7, $7)
`, stateID, claims.TenantID, input.SiteID, input.ControlMode, reason, claims.PrincipalID, now); err != nil {
				return mutationRecord[SiteControlState]{}, err
			}
		} else if err != nil {
			return mutationRecord[SiteControlState]{}, fmt.Errorf("lock Site control state: %w", err)
		} else {
			if currentRevision != input.Meta.ExpectedRevision {
				return mutationRecord[SiteControlState]{}, ErrRevisionConflict
			}
			beforeRevision = revisionPointer(currentRevision)
			nextRevision = currentRevision + 1
			eventType = "registry.site-control-state.updated"
			tag, err := tx.Exec(ctx, `
UPDATE core_registry.site_control_states
SET mode = $4,
    reason = $5,
    updated_by = $6::uuid,
    revision = $7,
    updated_at = $8
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $9
`, claims.TenantID, input.SiteID, stateID, input.ControlMode, reason, claims.PrincipalID, nextRevision, now, currentRevision)
			if err != nil {
				return mutationRecord[SiteControlState]{}, err
			}
			if tag.RowsAffected() != 1 {
				return mutationRecord[SiteControlState]{}, ErrRevisionConflict
			}
		}

		assetLocks, err := querySiteAssetLocks(ctx, tx, claims.TenantID, input.SiteID)
		if err != nil {
			return mutationRecord[SiteControlState]{}, err
		}
		activeMaintenanceWindow, err := queryActiveMaintenanceWindow(ctx, tx, claims.TenantID, input.SiteID, timezone, now)
		if err != nil {
			return mutationRecord[SiteControlState]{}, err
		}

		updatedBy := claims.PrincipalID
		updatedAt := formatInstant(now)
		siteCopy := input.SiteID
		saved := SiteControlState{
			ID:                      &stateID,
			TenantID:                claims.TenantID,
			SiteID:                  input.SiteID,
			ControlMode:             input.ControlMode,
			Reason:                  reason,
			UpdatedBy:               &updatedBy,
			UpdatedAt:               &updatedAt,
			Revision:                nextRevision,
			AssetLocks:              assetLocks,
			ActiveMaintenanceWindow: activeMaintenanceWindow,
		}
		return mutationRecord[SiteControlState]{
			Result:           saved,
			SiteID:           &siteCopy,
			ResourceType:     "SITE_CONTROL_STATE",
			ResourceID:       stateID,
			BeforeRevision:   beforeRevision,
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        eventType,
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":      input.SiteID,
				"controlMode": input.ControlMode,
				"reason":      reason,
				"revision":    nextRevision,
			},
		}, nil
	})
}

func (store *PostgresStore) SaveAssetControlLock(ctx context.Context, claims registryauth.GrantClaims, input AssetControlLockMutation) (AssetControlLockState, bool, error) {
	if err := input.Validate(); err != nil {
		return AssetControlLockState{}, false, err
	}
	reason := effectiveControlReason(input.Reason, input.Meta.Reason)
	trimmedMetaReason := strings.TrimSpace(input.Meta.Reason)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionScheduleManage, input.Meta.IdempotencyKey, trimmedMetaReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[AssetControlLockState], error) {
		var siteID string
		err := tx.QueryRow(ctx, `
SELECT site_id::text
FROM core_registry.assets
WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (site_id = ANY($3::uuid[]))
`, claims.TenantID, input.AssetID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&siteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[AssetControlLockState]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[AssetControlLockState]{}, fmt.Errorf("verify Asset for control lock save: %w", err)
		}
		if err := ensureSiteScope(claims, siteID); err != nil {
			return mutationRecord[AssetControlLockState]{}, err
		}

		var lockID string
		var currentRevision int64
		err = tx.QueryRow(ctx, `
SELECT id::text, revision
FROM core_registry.asset_control_locks
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND asset_id = $3::uuid
FOR UPDATE
`, claims.TenantID, siteID, input.AssetID).Scan(&lockID, &currentRevision)

		var nextRevision int64
		var beforeRevision *int64
		var eventType string
		if errors.Is(err, pgx.ErrNoRows) {
			if input.Meta.ExpectedRevision != 0 {
				return mutationRecord[AssetControlLockState]{}, ErrRevisionConflict
			}
			lockID, err = newCoreUUIDv7(now)
			if err != nil {
				return mutationRecord[AssetControlLockState]{}, err
			}
			nextRevision = 1
			eventType = "registry.asset-control-lock.created"
			if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.asset_control_locks (
  id, tenant_id, site_id, asset_id, lock_mode, reason, updated_by, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::uuid, 1, $8, $8)
`, lockID, claims.TenantID, siteID, input.AssetID, input.LockMode, reason, claims.PrincipalID, now); err != nil {
				return mutationRecord[AssetControlLockState]{}, err
			}
		} else if err != nil {
			return mutationRecord[AssetControlLockState]{}, fmt.Errorf("lock Asset control lock: %w", err)
		} else {
			if currentRevision != input.Meta.ExpectedRevision {
				return mutationRecord[AssetControlLockState]{}, ErrRevisionConflict
			}
			beforeRevision = revisionPointer(currentRevision)
			nextRevision = currentRevision + 1
			eventType = "registry.asset-control-lock.updated"
			tag, err := tx.Exec(ctx, `
UPDATE core_registry.asset_control_locks
SET lock_mode = $4,
    reason = $5,
    updated_by = $6::uuid,
    revision = $7,
    updated_at = $8
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND asset_id = $3::uuid AND revision = $9
`, claims.TenantID, siteID, input.AssetID, input.LockMode, reason, claims.PrincipalID, nextRevision, now, currentRevision)
			if err != nil {
				return mutationRecord[AssetControlLockState]{}, err
			}
			if tag.RowsAffected() != 1 {
				return mutationRecord[AssetControlLockState]{}, ErrRevisionConflict
			}
		}

		boundByAsset, err := queryActiveAssetDeviceIDs(ctx, tx, claims.TenantID, siteID, input.AssetID)
		if err != nil {
			return mutationRecord[AssetControlLockState]{}, err
		}

		updatedBy := claims.PrincipalID
		updatedAt := formatInstant(now)
		siteCopy := siteID
		saved := AssetControlLockState{
			ID:             lockID,
			TenantID:       claims.TenantID,
			SiteID:         siteID,
			AssetID:        input.AssetID,
			LockMode:       input.LockMode,
			Reason:         reason,
			UpdatedBy:      &updatedBy,
			UpdatedAt:      &updatedAt,
			Revision:       nextRevision,
			BoundDeviceIDs: boundByAsset[input.AssetID],
		}
		return mutationRecord[AssetControlLockState]{
			Result:           saved,
			SiteID:           &siteCopy,
			ResourceType:     "ASSET_CONTROL_LOCK",
			ResourceID:       lockID,
			BeforeRevision:   beforeRevision,
			AfterRevision:    revisionPointer(nextRevision),
			EventType:        eventType,
			AggregateVersion: nextRevision,
			Payload: map[string]any{
				"siteId":   siteID,
				"assetId":  input.AssetID,
				"lockMode": input.LockMode,
				"reason":   reason,
				"revision": nextRevision,
			},
		}, nil
	})
}

func siteLocalCalendarDate(timezone string, at time.Time) string {
	location := time.UTC
	trimmed := strings.TrimSpace(timezone)
	if trimmed != "" && !strings.EqualFold(trimmed, "Local") {
		if loaded, err := time.LoadLocation(trimmed); err == nil {
			location = loaded
		}
	}
	return at.In(location).Format("2006-01-02")
}

func queryActiveMaintenanceWindow(ctx context.Context, transaction pgx.Tx, tenantID, siteID, timezone string, at time.Time) (*ActiveMaintenanceWindow, error) {
	siteDate := siteLocalCalendarDate(timezone, at)
	var window ActiveMaintenanceWindow
	err := transaction.QueryRow(ctx, `
SELECT id::text, title, to_char(start_date, 'YYYY-MM-DD'), to_char(end_date, 'YYYY-MM-DD'), reason
FROM core_registry.operating_exceptions
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND status = 'APPROVED'
  AND kind = 'MAINTENANCE_WINDOW'
  AND start_date <= $3::date
  AND end_date >= $3::date
ORDER BY start_date ASC, id ASC
LIMIT 1
`, tenantID, siteID, siteDate).Scan(&window.ID, &window.Title, &window.StartDate, &window.EndDate, &window.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query active maintenance window: %w", err)
	}
	return &window, nil
}
