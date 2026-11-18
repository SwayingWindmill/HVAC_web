package core

import (
	"context"
	"encoding/json"
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

	ExecutionModeShadow     = "SHADOW"
	ExecutionModeClosedLoop = "CLOSED_LOOP"

	ExecutionMonitorPendingSubmit     = "PENDING_SUBMIT"
	ExecutionMonitorObserving         = "OBSERVING"
	ExecutionMonitorVerified          = "VERIFIED"
	ExecutionMonitorRolledBack        = "ROLLED_BACK"
	ExecutionMonitorTripped           = "TRIPPED"
	ExecutionMonitorTrippedRolledBack = "TRIPPED_ROLLED_BACK"
	ExecutionMonitorRollbackFailed    = "ROLLBACK_FAILED"
	ExecutionMonitorShadowRecorded    = "SHADOW_RECORDED"
	ExecutionMonitorSubmitFailed      = "SUBMIT_FAILED"
	ExecutionMonitorManualOverride    = "MANUAL_OVERRIDE"

	defaultAutoControlDeadbandC = 0.2
	minAutoControlDeadbandC     = 0.05
	maxAutoControlDeadbandC     = 2.0
	minAutoControlSupplyTempC   = 5.0
	maxAutoControlSupplyTempC   = 20.0
	maxAutoControlSupplyUpperC  = 25.0
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
	DeadbandC           float64  `json:"deadbandC"`
	SupplyTempMinC      float64  `json:"supplyTempMinC"`
	SupplyTempMaxC      float64  `json:"supplyTempMaxC"`
	ZoneTempMaxC        float64  `json:"zoneTempMaxC"`
	MinRunningChillers  int      `json:"minRunningChillers"`
	MaxCommandsPerHour  int      `json:"maxCommandsPerHour"`
	ExecutionMode       string   `json:"executionMode"`
	TripReason          *string  `json:"tripReason,omitempty"`
	ManualBackoffUntil  *string  `json:"manualBackoffUntil,omitempty"`
	ManualBackoffReason string   `json:"manualBackoffReason,omitempty"`
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
	DeadbandC           *float64     `json:"deadbandC,omitempty"`
	SupplyTempMinC      float64      `json:"supplyTempMinC"`
	SupplyTempMaxC      float64      `json:"supplyTempMaxC"`
	ZoneTempMaxC        *float64     `json:"zoneTempMaxC,omitempty"`
	MinRunningChillers  *int         `json:"minRunningChillers,omitempty"`
	MaxCommandsPerHour  *int         `json:"maxCommandsPerHour,omitempty"`
	ExecutionMode       string       `json:"executionMode,omitempty"`
	Reason              string       `json:"reason,omitempty"`
	Meta                MutationMeta `json:"meta"`
}

type RevokeAutoControlLeaseMutation struct {
	SiteID string       `json:"siteId,omitempty"`
	Reason string       `json:"reason,omitempty"`
	Meta   MutationMeta `json:"meta"`
}

type OptimizationExecutionMonitor struct {
	ID                      string          `json:"id"`
	TenantID                string          `json:"tenantId"`
	SiteID                  string          `json:"siteId"`
	LeaseID                 string          `json:"leaseId"`
	RecommendationID        string          `json:"recommendationId"`
	CommandIntentID         *string         `json:"commandIntentId"`
	DeviceID                string          `json:"deviceId"`
	TargetCapability        string          `json:"targetCapability"`
	BaselineSupplyTempC     *float64        `json:"baselineSupplyTempC,omitempty"`
	AppliedSupplyTempC      *float64        `json:"appliedSupplyTempC,omitempty"`
	RestoreSupplyTempC      *float64        `json:"restoreSupplyTempC,omitempty"`
	SupplyTempMinC          float64         `json:"supplyTempMinC"`
	ZoneTempMaxC            float64         `json:"zoneTempMaxC"`
	SupplyTempMaxC          float64         `json:"supplyTempMaxC"`
	RawRecommendedValue     *float64        `json:"rawRecommendedValue,omitempty"`
	ClampedValue            *float64        `json:"clampedValue,omitempty"`
	GuardrailVerdict        string          `json:"guardrailVerdict,omitempty"`
	GuardrailTrace          json.RawMessage `json:"guardrailTrace,omitempty"`
	ExecutionMode           string          `json:"executionMode"`
	DecisionID              *string         `json:"decisionId,omitempty"`
	TraceID                 string          `json:"traceId,omitempty"`
	StartedAt               string          `json:"startedAt"`
	WindowEndsAt            string          `json:"windowEndsAt"`
	LastCheckedAt           *string         `json:"lastCheckedAt,omitempty"`
	TelemetryUnhealthySince *string         `json:"telemetryUnhealthySince,omitempty"`
	Status                  string          `json:"status"`
	TripReason              *string         `json:"tripReason,omitempty"`
	RollbackCommandIntentID *string         `json:"rollbackCommandIntentId,omitempty"`
	CreatedAt               string          `json:"createdAt"`
	UpdatedAt               string          `json:"updatedAt"`
}

type OptimizationExecutionMonitorList struct {
	TenantID string                         `json:"tenantId"`
	SiteID   string                         `json:"siteId"`
	Items    []OptimizationExecutionMonitor `json:"items"`
}

type normalizedAutoControlLeaseValues struct {
	expiresAt     time.Time
	capabilities  []string
	maxStep       float64
	deadbandC     float64
	supplyMin     float64
	supplyMax     float64
	zoneMax       float64
	minChillers   int
	maxPerHour    int
	executionMode string
}

func (input ArmAutoControlLeaseMutation) normalizedValues(now time.Time) (normalizedAutoControlLeaseValues, error) {
	if !validUUIDv7(input.SiteID) {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	if err := validControlLockMeta(input.Meta, input.Reason); err != nil {
		return normalizedAutoControlLeaseValues{}, err
	}
	nowUTC := now.UTC()
	var expiresAt time.Time
	if strings.TrimSpace(input.ExpiresAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ExpiresAt))
		if parseErr != nil {
			return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
		}
		expiresAt = parsed.UTC()
	} else if input.ExpiresInHours > 0 {
		expiresAt = nowUTC.Add(time.Duration(input.ExpiresInHours) * time.Hour)
	} else {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	if !expiresAt.After(nowUTC) || expiresAt.After(nowUTC.Add(24*time.Hour)) {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}

	var capabilities []string
	seen := make(map[string]struct{}, len(input.AllowedCapabilities))
	for _, rawCap := range input.AllowedCapabilities {
		capName := strings.TrimSpace(rawCap)
		if capName == "" || !slices.Contains(validAutoControlCapabilities, capName) {
			return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
		}
		if _, duplicate := seen[capName]; duplicate {
			continue
		}
		seen[capName] = struct{}{}
		capabilities = append(capabilities, capName)
	}
	if len(capabilities) == 0 {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}

	maxStep := 1.0
	if input.MaxSetpointStepC != nil {
		maxStep = *input.MaxSetpointStepC
	}
	deadbandC := defaultAutoControlDeadbandC
	if input.DeadbandC != nil {
		deadbandC = *input.DeadbandC
	}
	zoneMax := 26.0
	if input.ZoneTempMaxC != nil {
		zoneMax = *input.ZoneTempMaxC
	}
	minChillers := 1
	if input.MinRunningChillers != nil {
		minChillers = *input.MinRunningChillers
	}
	maxPerHour := 2
	if input.MaxCommandsPerHour != nil {
		maxPerHour = *input.MaxCommandsPerHour
	}
	supplyMin := input.SupplyTempMinC
	supplyMax := input.SupplyTempMaxC

	for _, num := range []float64{maxStep, deadbandC, supplyMin, supplyMax, zoneMax} {
		if math.IsNaN(num) || math.IsInf(num, 0) {
			return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
		}
	}
	if maxStep <= 0 || maxStep > 2.0 {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	if deadbandC < minAutoControlDeadbandC || deadbandC > maxAutoControlDeadbandC {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	if supplyMin < minAutoControlSupplyTempC || supplyMin > maxAutoControlSupplyTempC || supplyMax < supplyMin || supplyMax > maxAutoControlSupplyUpperC {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	if zoneMax < 18.0 || zoneMax > 32.0 || minChillers < 0 || maxPerHour < 1 || maxPerHour > 6 {
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	executionMode := input.ExecutionMode
	if executionMode == "" {
		executionMode = ExecutionModeClosedLoop
	}
	switch executionMode {
	case ExecutionModeShadow, ExecutionModeClosedLoop:
	default:
		return normalizedAutoControlLeaseValues{}, ErrInvalidMutation
	}
	return normalizedAutoControlLeaseValues{
		expiresAt:     expiresAt,
		capabilities:  capabilities,
		maxStep:       maxStep,
		deadbandC:     deadbandC,
		supplyMin:     supplyMin,
		supplyMax:     supplyMax,
		zoneMax:       zoneMax,
		minChillers:   minChillers,
		maxPerHour:    maxPerHour,
		executionMode: executionMode,
	}, nil
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

const autoControlLeaseSelectColumns = `
id::text, tenant_id::text, site_id::text, status, armed_by::text, armed_at, expires_at,
allowed_capabilities, max_setpoint_step_c, deadband_c, supply_temp_min_c, supply_temp_max_c,
zone_temp_max_c, min_running_chillers, max_commands_per_hour, execution_mode, trip_reason,
manual_backoff_until, manual_backoff_reason, revision, created_at, updated_at`

func scanAutoControlLeaseRow(row pgx.Row) (AutoControlLease, time.Time, error) {
	var lease AutoControlLease
	var armedAt, expiresAt, createdAt, updatedAt time.Time
	var manualBackoffUntil *time.Time
	if err := row.Scan(
		&lease.ID, &lease.TenantID, &lease.SiteID, &lease.Status, &lease.ArmedBy, &armedAt, &expiresAt,
		&lease.AllowedCapabilities, &lease.MaxSetpointStepC, &lease.DeadbandC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC,
		&lease.ZoneTempMaxC, &lease.MinRunningChillers, &lease.MaxCommandsPerHour, &lease.ExecutionMode, &lease.TripReason,
		&manualBackoffUntil, &lease.ManualBackoffReason, &lease.Revision, &createdAt, &updatedAt,
	); err != nil {
		return AutoControlLease{}, time.Time{}, err
	}
	lease.ArmedAt = formatInstant(armedAt)
	lease.ExpiresAt = formatInstant(expiresAt)
	if manualBackoffUntil != nil {
		formatted := formatInstant(*manualBackoffUntil)
		lease.ManualBackoffUntil = &formatted
	}
	lease.CreatedAt = formatInstant(createdAt)
	lease.UpdatedAt = formatInstant(updatedAt)
	return lease, expiresAt, nil
}

func queryLatestAutoControlLease(ctx context.Context, tx pgx.Tx, tenantID, siteID string, now time.Time) (*AutoControlLease, error) {
	lease, expiresAt, err := scanAutoControlLeaseRow(tx.QueryRow(ctx, `
SELECT `+autoControlLeaseSelectColumns+`
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
ORDER BY (status = 'ACTIVE' AND expires_at > $3) DESC, updated_at DESC, id DESC
LIMIT 1
`, tenantID, siteID, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query Site auto control lease: %w", err)
	}
	if lease.Status == AutoControlLeaseActive && !expiresAt.After(now) {
		lease.Status = AutoControlLeaseExpired
	}
	return &lease, nil
}

func (store *PostgresStore) ArmSiteAutoControlLease(ctx context.Context, claims registryauth.GrantClaims, input ArmAutoControlLeaseMutation) (SiteAutoControlLeaseState, bool, error) {
	nowCheck := time.Now().UTC()
	if _, err := input.normalizedValues(nowCheck); err != nil {
		return SiteAutoControlLeaseState{}, false, err
	}
	reason := effectiveControlReason(input.Reason, input.Meta.Reason)
	trimmedMetaReason := strings.TrimSpace(input.Meta.Reason)

	return runRegistryMutation(ctx, store, claims, registryauth.ActionScheduleManage, input.Meta.IdempotencyKey, trimmedMetaReason, input, func(tx pgx.Tx, now time.Time) (mutationRecord[SiteAutoControlLeaseState], error) {
		values, err := input.normalizedValues(now)
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
  allowed_capabilities, max_setpoint_step_c, deadband_c, supply_temp_min_c, supply_temp_max_c,
  zone_temp_max_c, min_running_chillers, max_commands_per_hour, execution_mode, trip_reason,
  revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, 'ACTIVE', $4::uuid, $5, $6,
  $7, $8, $9, $10, $11,
  $12, $13, $14, $15, NULL,
  1, $5, $5
)`, leaseID, claims.TenantID, input.SiteID, claims.PrincipalID, now, values.expiresAt,
			values.capabilities, values.maxStep, values.deadbandC, values.supplyMin, values.supplyMax, values.zoneMax, values.minChillers, values.maxPerHour, values.executionMode); err != nil {
			return mutationRecord[SiteAutoControlLeaseState]{}, fmt.Errorf("insert auto control lease: %w", err)
		}

		lease := AutoControlLease{
			ID:                  leaseID,
			TenantID:            claims.TenantID,
			SiteID:              input.SiteID,
			Status:              AutoControlLeaseActive,
			ArmedBy:             claims.PrincipalID,
			ArmedAt:             formatInstant(now),
			ExpiresAt:           formatInstant(values.expiresAt),
			AllowedCapabilities: values.capabilities,
			MaxSetpointStepC:    values.maxStep,
			DeadbandC:           values.deadbandC,
			SupplyTempMinC:      values.supplyMin,
			SupplyTempMaxC:      values.supplyMax,
			ZoneTempMaxC:        values.zoneMax,
			MinRunningChillers:  values.minChillers,
			MaxCommandsPerHour:  values.maxPerHour,
			ExecutionMode:       values.executionMode,
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
				"allowedCapabilities": values.capabilities,
				"deadbandC":           values.deadbandC,
				"executionMode":       values.executionMode,
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

		lease, _, err := scanAutoControlLeaseRow(tx.QueryRow(ctx, `
SELECT `+autoControlLeaseSelectColumns+`
FROM core_registry.site_auto_control_leases
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND status = 'ACTIVE'
ORDER BY updated_at DESC, id DESC
LIMIT 1
FOR UPDATE
`, claims.TenantID, input.SiteID))
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
       CASE WHEN command_intent_id IS NULL THEN NULL ELSE command_intent_id::text END,
       device_id::text, target_capability,
       baseline_supply_temp_c, applied_supply_temp_c, restore_supply_temp_c,
       supply_temp_min_c, zone_temp_max_c, supply_temp_max_c,
       raw_recommended_value, clamped_value, guardrail_verdict, guardrail_trace,
       execution_mode,
       CASE WHEN decision_id IS NULL THEN NULL ELSE decision_id::text END,
       trace_id,
       started_at, window_ends_at, last_checked_at, telemetry_unhealthy_since, status, trip_reason,
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
			var guardrailTrace []byte
			if err := rows.Scan(
				&item.ID, &item.TenantID, &item.SiteID, &item.LeaseID, &item.RecommendationID,
				&item.CommandIntentID, &item.DeviceID, &item.TargetCapability,
				&item.BaselineSupplyTempC, &item.AppliedSupplyTempC, &item.RestoreSupplyTempC,
				&item.SupplyTempMinC, &item.ZoneTempMaxC, &item.SupplyTempMaxC,
				&item.RawRecommendedValue, &item.ClampedValue, &item.GuardrailVerdict, &guardrailTrace,
				&item.ExecutionMode, &item.DecisionID, &item.TraceID,
				&startedAt, &windowEndsAt, &lastCheckedAt, &unhealthySince, &item.Status, &item.TripReason,
				&item.RollbackCommandIntentID, &createdAt, &updatedAt,
			); err != nil {
				return fmt.Errorf("scan optimization execution monitor: %w", err)
			}
			if len(guardrailTrace) > 0 {
				item.GuardrailTrace = append(json.RawMessage(nil), guardrailTrace...)
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
