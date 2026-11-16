package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	AutoControlLeaseActive  = "ACTIVE"
	AutoControlLeaseTripped = "TRIPPED"
	AutoControlLeaseRevoked = "REVOKED"
	AutoControlLeaseExpired = "EXPIRED"

	ConfirmationModeManual        = "MANUAL_CONFIRM"
	ConfirmationModeAutoWithLease = "AUTO_WITH_LEASE"

	ExecutionMonitorObserving  = "OBSERVING"
	ExecutionMonitorVerified   = "VERIFIED"
	ExecutionMonitorRolledBack = "ROLLED_BACK"
	ExecutionMonitorTripped    = "TRIPPED"
)

var validAutoControlCapabilities = []string{
	"SET_CHILLED_WATER_TEMPERATURE_SETPOINT",
	"SET_TEMPERATURE_SETPOINT",
	"SET_FREQUENCY",
	"SET_FAN_SPEED",
	"SET_LOAD_LIMIT",
	"SET_OPENING",
	"START",
	"STOP",
	"START_UNIT_SEQUENCE",
	"STOP_UNIT_SEQUENCE",
	"RESET_FAULT",
}

type AutoControlLease struct {
	ID                  string   `json:"id"`
	TenantID            string   `json:"tenantId"`
	SiteID              string   `json:"siteId"`
	Status              string   `json:"status"`
	ArmedBy             string   `json:"armedBy"`
	ArmedAt             string   `json:"armedAt"`
	ExpiresAt           string   `json:"expiresAt"`
	AllowedCapabilities []string `json:"allowedCapabilities"`
	MaxSetpointStepC    float64  `json:"maxSetpointStepC"`
	SupplyTempMinC      float64  `json:"supplyTempMinC"`
	SupplyTempMaxC      float64  `json:"supplyTempMaxC"`
	ZoneTempMaxC        float64  `json:"zoneTempMaxC"`
	MinRunningChillers  int      `json:"minRunningChillers"`
	MaxCommandsPerHour  int      `json:"maxCommandsPerHour"`
	TripReason          *string  `json:"tripReason,omitempty"`
	Revision            int64    `json:"revision"`
	CreatedAt           string   `json:"createdAt"`
	UpdatedAt           string   `json:"updatedAt"`
}

type SiteAutoControlLeaseState struct {
	TenantID         string            `json:"tenantId"`
	SiteID           string            `json:"siteId"`
	ConfirmationMode string            `json:"confirmationMode"`
	ActiveLease      *AutoControlLease `json:"activeLease"`
	LatestLease      *AutoControlLease `json:"latestLease"`
}

type ArmAutoControlLeaseMutation struct {
	SiteID              string       `json:"siteId,omitempty"`
	ExpiresInHours      int          `json:"expiresInHours,omitempty"`
	ExpiresAt           string       `json:"expiresAt,omitempty"`
	AllowedCapabilities []string     `json:"allowedCapabilities"`
	MaxSetpointStepC    *float64     `json:"maxSetpointStepC,omitempty"`
	SupplyTempMinC      float64      `json:"supplyTempMinC"`
	SupplyTempMaxC      float64      `json:"supplyTempMaxC"`
	ZoneTempMaxC        *float64     `json:"zoneTempMaxC,omitempty"`
	MinRunningChillers  *int         `json:"minRunningChillers,omitempty"`
	MaxCommandsPerHour  *int         `json:"maxCommandsPerHour,omitempty"`
	Reason              string       `json:"reason,omitempty"`
	Meta                MutationMeta `json:"meta"`
}

type RevokeAutoControlLeaseMutation struct {
	SiteID string       `json:"siteId,omitempty"`
	Reason string       `json:"reason,omitempty"`
	Meta   MutationMeta `json:"meta"`
}

type OptimizationExecutionMonitor struct {
	ID                      string   `json:"id"`
	TenantID                string   `json:"tenantId"`
	SiteID                  string   `json:"siteId"`
	LeaseID                 string   `json:"leaseId"`
	RecommendationID        string   `json:"recommendationId"`
	CommandIntentID         string   `json:"commandIntentId"`
	DeviceID                string   `json:"deviceId"`
	TargetCapability        string   `json:"targetCapability"`
	BaselineSupplyTempC     *float64 `json:"baselineSupplyTempC,omitempty"`
	AppliedSupplyTempC      *float64 `json:"appliedSupplyTempC,omitempty"`
	RestoreSupplyTempC      *float64 `json:"restoreSupplyTempC,omitempty"`
	ZoneTempMaxC            float64  `json:"zoneTempMaxC"`
	SupplyTempMaxC          float64  `json:"supplyTempMaxC"`
	StartedAt               string   `json:"startedAt"`
	WindowEndsAt            string   `json:"windowEndsAt"`
	LastCheckedAt           *string  `json:"lastCheckedAt,omitempty"`
	TelemetryUnhealthySince *string  `json:"telemetryUnhealthySince,omitempty"`
	Status                  string   `json:"status"`
	TripReason              *string  `json:"tripReason,omitempty"`
	RollbackCommandIntentID *string  `json:"rollbackCommandIntentId,omitempty"`
	CreatedAt               string   `json:"createdAt"`
	UpdatedAt               string   `json:"updatedAt"`
}

type OptimizationExecutionMonitorList struct {
	TenantID string                         `json:"tenantId"`
	SiteID   string                         `json:"siteId"`
	Items    []OptimizationExecutionMonitor `json:"items"`
}

func (input ArmAutoControlLeaseMutation) normalizedValues(now time.Time) (
	expiresAt time.Time,
	capabilities []string,
	maxStep float64,
	supplyMin float64,
	supplyMax float64,
	zoneMax float64,
	minChillers int,
	maxPerHour int,
	err error,
) {
	if !validUUIDv7(input.SiteID) {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}
	if err := validControlLockMeta(input.Meta, input.Reason); err != nil {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, err
	}
	nowUTC := now.UTC()
	if strings.TrimSpace(input.ExpiresAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ExpiresAt))
		if parseErr != nil {
			return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
		}
		expiresAt = parsed.UTC()
	} else if input.ExpiresInHours > 0 {
		expiresAt = nowUTC.Add(time.Duration(input.ExpiresInHours) * time.Hour)
	} else {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}
	if !expiresAt.After(nowUTC) || expiresAt.After(nowUTC.Add(24*time.Hour)) {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}

	seen := make(map[string]struct{}, len(input.AllowedCapabilities))
	for _, rawCap := range input.AllowedCapabilities {
		capName := strings.TrimSpace(rawCap)
		if capName == "" || !slices.Contains(validAutoControlCapabilities, capName) {
			return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
		}
		if _, duplicate := seen[capName]; duplicate {
			continue
		}
		seen[capName] = struct{}{}
		capabilities = append(capabilities, capName)
	}
	if len(capabilities) == 0 {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}

	maxStep = 1.0
	if input.MaxSetpointStepC != nil {
		maxStep = *input.MaxSetpointStepC
	}
	zoneMax = 26.0
	if input.ZoneTempMaxC != nil {
		zoneMax = *input.ZoneTempMaxC
	}
	minChillers = 1
	if input.MinRunningChillers != nil {
		minChillers = *input.MinRunningChillers
	}
	maxPerHour = 2
	if input.MaxCommandsPerHour != nil {
		maxPerHour = *input.MaxCommandsPerHour
	}
	supplyMin = input.SupplyTempMinC
	supplyMax = input.SupplyTempMaxC

	for _, num := range []float64{maxStep, supplyMin, supplyMax, zoneMax} {
		if math.IsNaN(num) || math.IsInf(num, 0) {
			return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
		}
	}
	if maxStep <= 0 || maxStep > 2.0 {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}
	if supplyMin < 4.0 || supplyMin > 20.0 || supplyMax < supplyMin || supplyMax > 25.0 {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}
	if zoneMax < 18.0 || zoneMax > 32.0 || minChillers < 0 || maxPerHour < 1 || maxPerHour > 6 {
		return time.Time{}, nil, 0, 0, 0, 0, 0, 0, ErrInvalidMutation
	}
	return expiresAt, capabilities, maxStep, supplyMin, supplyMax, zoneMax, minChillers, maxPerHour, nil
}

func (input RevokeAutoControlLeaseMutation) Validate() error {
	if !validUUIDv7(input.SiteID) {
		return ErrInvalidMutation
	}
	if utf8.RuneCountInString(input.Reason) > 1000 {
		return ErrInvalidMutation
	}
	return validControlLockMeta(input.Meta, input.Reason)
}

func (store *PostgresStore) GetSiteAutoControlLease(ctx context.Context, claims registryauth.GrantClaims, siteID string) (SiteAutoControlLeaseState, error) {
	if !validUUIDv7(siteID) {
		return SiteAutoControlLeaseState{}, ErrNotFound
	}
	var state SiteAutoControlLeaseState
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var tenantID string
		err := transaction.QueryRow(ctx, `
SELECT tenant_id::text
FROM core_registry.sites
WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query Site for auto control lease: %w", err)
		}
		state = SiteAutoControlLeaseState{
			TenantID:         tenantID,
			SiteID:           siteID,
			ConfirmationMode: ConfirmationModeManual,
		}
		now := time.Now().UTC()
		latest, err := queryLatestAutoControlLease(ctx, transaction, tenantID, siteID, now)
		if err != nil {
			return err
		}
		if latest != nil {
			state.LatestLease = latest
			if latest.Status == AutoControlLeaseActive {
				activeCopy := *latest
				state.ActiveLease = &activeCopy
				state.ConfirmationMode = ConfirmationModeAutoWithLease
			}
		}
		return nil
	})
	if err != nil {
		return SiteAutoControlLeaseState{}, err
	}
	return state, nil
}

func queryLatestAutoControlLease(ctx context.Context, tx pgx.Tx, tenantID, siteID string, now time.Time) (*AutoControlLease, error) {
	var lease AutoControlLease
	var armedAt, expiresAt, createdAt, updatedAt time.Time
	var tripReason *string
	err := tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, status, armed_by::text, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c, supply_temp_max_c,
       zone_temp_max_c, min_running_chillers, max_commands_per_hour, trip_reason,
       revision, created_at, updated_at
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY (status = 'ACTIVE' AND expires_at > $3) DESC, updated_at DESC, id DESC
LIMIT 1
`, tenantID, siteID, now).Scan(
		&lease.ID, &lease.TenantID, &lease.SiteID, &lease.Status, &lease.ArmedBy, &armedAt, &expiresAt,
		&lease.AllowedCapabilities, &lease.MaxSetpointStepC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC,
		&lease.ZoneTempMaxC, &lease.MinRunningChillers, &lease.MaxCommandsPerHour, &tripReason,
		&lease.Revision, &createdAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query Site auto control lease: %w", err)
	}
	if lease.Status == AutoControlLeaseActive && !expiresAt.After(now) {
		lease.Status = AutoControlLeaseExpired
	}
	lease.ArmedAt = formatInstant(armedAt)
	lease.ExpiresAt = formatInstant(expiresAt)
	lease.CreatedAt = formatInstant(createdAt)
	lease.UpdatedAt = formatInstant(updatedAt)
	lease.TripReason = tripReason
	return &lease, nil
}

func (store *PostgresStore) ArmSiteAutoControlLease(ctx context.Context, claims registryauth.GrantClaims, input ArmAutoControlLeaseMutation) (SiteAutoControlLeaseState, bool, error) {
	nowCheck := time.Now().UTC()
	if _, _, _, _, _, _, _, _, err := input.normalizedValues(nowCheck); err != nil {
		return SiteAutoControlLeaseState{}, false, err
	}
	reason := effectiveControlReason(input.Reason, input.Meta.Reason)
	trimmedMetaReason := strings.TrimSpace(input.Meta.Reason)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionScheduleManage, input.Meta.IdempotencyKey, trimmedMetaReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[SiteAutoControlLeaseState], error) {
		expiresAt, capabilities, maxStep, supplyMin, supplyMax, zoneMax, minChillers, maxPerHour, err := input.normalizedValues(now)
		if err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("verify Site for auto control lease arm: %w", err)
		}
		if !exists {
			return mutationRecord[SiteAutoControlLeaseState]{}, ErrNotFound
		}
		if err := ensureSiteScope(claims, input.SiteID); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, err
		}

		// Close out any previously active leases whose window already lapsed before evaluating uniqueness.
		if _, err := tx.Exec(ctx, `
UPDATE core_registry.site_auto_control_leases
SET status = 'EXPIRED', revision = revision + 1, updated_at = $3
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE' AND expires_at <= $3
`, claims.TenantID, input.SiteID, now); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("expire stale auto control lease: %w", err)
		}

		var existingID, existingStatus string
		var existingRevision int64
		err = tx.QueryRow(ctx, `
SELECT id::text, status, revision
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY (status = 'ACTIVE') DESC, updated_at DESC, id DESC
LIMIT 1
FOR UPDATE
`, claims.TenantID, input.SiteID).Scan(&existingID, &existingStatus, &existingRevision)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("lock existing auto control lease: %w", err)
		}
		if err == nil {
			if existingStatus == AutoControlLeaseActive {
				if input.Meta.ExpectedRevision != existingRevision {
					return mutationRecord[SiteAutoControlLeaseState]{}, ErrRevisionConflict
				}
				if _, err := tx.Exec(ctx, `
UPDATE core_registry.site_auto_control_leases
SET status = 'REVOKED', trip_reason = $4, revision = revision + 1, updated_at = $5
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
`, claims.TenantID, input.SiteID, existingID, "superseded by new operator lease", now); err != nil {
					return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("supersede active auto control lease: %w", err)
				}
			} else if input.Meta.ExpectedRevision != 0 && input.Meta.ExpectedRevision != existingRevision {
				return mutationRecord[SiteAutoControlLeaseState]{}, ErrRevisionConflict
			}
		} else if input.Meta.ExpectedRevision != 0 {
			return mutationRecord[SiteAutoControlLeaseState]{}, ErrRevisionConflict
		}

		leaseID, err := newCoreUUIDv7(now)
		if err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.site_auto_control_leases (
  id, tenant_id, site_id, status, armed_by, armed_at, expires_at,
  allowed_capabilities, max_setpoint_step_c, supply_temp_min_c, supply_temp_max_c,
  zone_temp_max_c, min_running_chillers, max_commands_per_hour, trip_reason,
  revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, 'ACTIVE', $4::uuid, $5, $6,
  $7, $8, $9, $10,
  $11, $12, $13, NULL,
  1, $5, $5
)`, leaseID, claims.TenantID, input.SiteID, claims.PrincipalID, now, expiresAt,
			capabilities, maxStep, supplyMin, supplyMax, zoneMax, minChillers, maxPerHour); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("insert auto control lease: %w", err)
		}

		lease := AutoControlLease{
			ID:                  leaseID,
			TenantID:            claims.TenantID,
			SiteID:              input.SiteID,
			Status:              AutoControlLeaseActive,
			ArmedBy:             claims.PrincipalID,
			ArmedAt:             formatInstant(now),
			ExpiresAt:           formatInstant(expiresAt),
			AllowedCapabilities: capabilities,
			MaxSetpointStepC:    maxStep,
			SupplyTempMinC:      supplyMin,
			SupplyTempMaxC:      supplyMax,
			ZoneTempMaxC:        zoneMax,
			MinRunningChillers:  minChillers,
			MaxCommandsPerHour:  maxPerHour,
			Revision:            1,
			CreatedAt:           formatInstant(now),
			UpdatedAt:           formatInstant(now),
		}
		leaseCopy := lease
		state := SiteAutoControlLeaseState{
			TenantID:         claims.TenantID,
			SiteID:           input.SiteID,
			ConfirmationMode: ConfirmationModeAutoWithLease,
			ActiveLease:      &leaseCopy,
			LatestLease:      &leaseCopy,
		}
		siteCopy := input.SiteID
		return mutationRecord[SiteAutoControlLeaseState]{
			Result:           state,
			SiteID:           &siteCopy,
			ResourceType:     "AUTO_CONTROL_LEASE",
			ResourceID:       leaseID,
			BeforeRevision:   nil,
			AfterRevision:    revisionPointer(1),
			EventType:        "AUTO_CONTROL_LEASE_ARMED",
			AggregateVersion: 1,
			Payload: map[string]any{
				"siteId":              input.SiteID,
				"leaseId":             leaseID,
				"armedBy":             claims.PrincipalID,
				"expiresAt":           lease.ExpiresAt,
				"allowedCapabilities": capabilities,
				"reason":              reason,
				"revision":            int64(1),
			},
		}, nil
	})
}

func (store *PostgresStore) RevokeSiteAutoControlLease(ctx context.Context, claims registryauth.GrantClaims, input RevokeAutoControlLeaseMutation) (SiteAutoControlLeaseState, bool, error) {
	if err := input.Validate(); err != nil {
		return SiteAutoControlLeaseState{}, false, err
	}
	reason := effectiveControlReason(input.Reason, input.Meta.Reason)
	trimmedMetaReason := strings.TrimSpace(input.Meta.Reason)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionScheduleManage, input.Meta.IdempotencyKey, trimmedMetaReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[SiteAutoControlLeaseState], error) {
		var exists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM core_registry.sites
  WHERE tenant_id = $1::uuid AND id = $2::uuid AND NOT (id = ANY($3::uuid[]))
)`, claims.TenantID, input.SiteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&exists); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("verify Site for auto control lease revoke: %w", err)
		}
		if !exists {
			return mutationRecord[SiteAutoControlLeaseState]{}, ErrNotFound
		}
		if err := ensureSiteScope(claims, input.SiteID); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, err
		}

		var lease AutoControlLease
		var armedAt, expiresAt, createdAt, updatedAt time.Time
		var tripReason *string
		err := tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, site_id::text, status, armed_by::text, armed_at, expires_at,
       allowed_capabilities, max_setpoint_step_c, supply_temp_min_c, supply_temp_max_c,
       zone_temp_max_c, min_running_chillers, max_commands_per_hour, trip_reason,
       revision, created_at, updated_at
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE'
ORDER BY updated_at DESC, id DESC
LIMIT 1
FOR UPDATE
`, claims.TenantID, input.SiteID).Scan(
			&lease.ID, &lease.TenantID, &lease.SiteID, &lease.Status, &lease.ArmedBy, &armedAt, &expiresAt,
			&lease.AllowedCapabilities, &lease.MaxSetpointStepC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC,
			&lease.ZoneTempMaxC, &lease.MinRunningChillers, &lease.MaxCommandsPerHour, &tripReason,
			&lease.Revision, &createdAt, &updatedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return mutationRecord[SiteAutoControlLeaseState]{}, ErrNotFound
		}
		if err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("lock active auto control lease for revoke: %w", err)
		}
		if input.Meta.ExpectedRevision != 0 && input.Meta.ExpectedRevision != lease.Revision {
			return mutationRecord[SiteAutoControlLeaseState]{}, ErrRevisionConflict
		}

		beforeRev := lease.Revision
		nextRev := beforeRev + 1
		if _, err := tx.Exec(ctx, `
UPDATE core_registry.site_auto_control_leases
SET status = 'REVOKED', trip_reason = $4, revision = $5, updated_at = $6
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid AND revision = $7
`, claims.TenantID, input.SiteID, lease.ID, reason, nextRev, now, beforeRev); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("revoke auto control lease: %w", err)
		}

		lease.Status = AutoControlLeaseRevoked
		lease.TripReason = &reason
		lease.Revision = nextRev
		lease.ArmedAt = formatInstant(armedAt)
		lease.ExpiresAt = formatInstant(expiresAt)
		lease.CreatedAt = formatInstant(createdAt)
		lease.UpdatedAt = formatInstant(now)

		leaseCopy := lease
		state := SiteAutoControlLeaseState{
			TenantID:         claims.TenantID,
			SiteID:           input.SiteID,
			ConfirmationMode: ConfirmationModeManual,
			ActiveLease:      nil,
			LatestLease:      &leaseCopy,
		}
		siteCopy := input.SiteID
		return mutationRecord[SiteAutoControlLeaseState]{
			Result:           state,
			SiteID:           &siteCopy,
			ResourceType:     "AUTO_CONTROL_LEASE",
			ResourceID:       lease.ID,
			BeforeRevision:   revisionPointer(beforeRev),
			AfterRevision:    revisionPointer(nextRev),
			EventType:        "AUTO_CONTROL_LEASE_REVOKED",
			AggregateVersion: nextRev,
			Payload: map[string]any{
				"siteId":   input.SiteID,
				"leaseId":  lease.ID,
				"reason":   reason,
				"revision": nextRev,
			},
		}, nil
	})
}

func (store *PostgresStore) ListOptimizationExecutionMonitors(ctx context.Context, claims registryauth.GrantClaims, siteID string) (OptimizationExecutionMonitorList, error) {
	if !validUUIDv7(siteID) {
		return OptimizationExecutionMonitorList{}, ErrNotFound
	}
	var list OptimizationExecutionMonitorList
	err := store.withReadTransaction(ctx, claims, func(transaction pgx.Tx) error {
		var tenantID string
		err := transaction.QueryRow(ctx, `
SELECT tenant_id::text
FROM core_registry.sites
WHERE id = $1::uuid AND NOT (id = ANY($2::uuid[]))
`, siteID, postgresUUIDArray(claims.DeniedSiteIDs)).Scan(&tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query Site for optimization execution monitors: %w", err)
		}

		rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, lease_id::text, recommendation_id::text,
       command_intent_id::text, device_id::text, target_capability,
       baseline_supply_temp_c, applied_supply_temp_c, restore_supply_temp_c,
       zone_temp_max_c, supply_temp_max_c, started_at, window_ends_at,
       last_checked_at, telemetry_unhealthy_since, status, trip_reason,
       CASE WHEN rollback_command_intent_id IS NULL THEN NULL ELSE rollback_command_intent_id::text END,
       created_at, updated_at
FROM core_registry.optimization_execution_monitors
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY started_at DESC, id DESC
LIMIT 50
`, tenantID, siteID)
		if err != nil {
			return fmt.Errorf("query optimization execution monitors: %w", err)
		}
		defer rows.Close()

		items := []OptimizationExecutionMonitor{}
		for rows.Next() {
			var item OptimizationExecutionMonitor
			var startedAt, windowEndsAt, createdAt, updatedAt time.Time
			var lastCheckedAt, unhealthySince *time.Time
			if err := rows.Scan(
				&item.ID, &item.TenantID, &item.SiteID, &item.LeaseID, &item.RecommendationID,
				&item.CommandIntentID, &item.DeviceID, &item.TargetCapability,
				&item.BaselineSupplyTempC, &item.AppliedSupplyTempC, &item.RestoreSupplyTempC,
				&item.ZoneTempMaxC, &item.SupplyTempMaxC, &startedAt, &windowEndsAt,
				&lastCheckedAt, &unhealthySince, &item.Status, &item.TripReason,
				&item.RollbackCommandIntentID, &createdAt, &updatedAt,
			); err != nil {
				return fmt.Errorf("scan optimization execution monitor: %w", err)
			}
			item.StartedAt = formatInstant(startedAt)
			item.WindowEndsAt = formatInstant(windowEndsAt)
			item.CreatedAt = formatInstant(createdAt)
			item.UpdatedAt = formatInstant(updatedAt)
			if lastCheckedAt != nil {
				formatted := formatInstant(*lastCheckedAt)
				item.LastCheckedAt = &formatted
			}
			if unhealthySince != nil {
				formatted := formatInstant(*unhealthySince)
				item.TelemetryUnhealthySince = &formatted
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate optimization execution monitors: %w", err)
		}
		list = OptimizationExecutionMonitorList{
			TenantID: tenantID,
			SiteID:   siteID,
			Items:    items,
		}
		return nil
	})
	if err != nil {
		return OptimizationExecutionMonitorList{}, err
	}
	return list, nil
}
