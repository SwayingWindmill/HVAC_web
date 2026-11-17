package iam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type postgresCommandAuthorizationStore struct {
	store                       *PostgresAuthorizationStore
	policyRevision              string
	emergencyRevocationRevision uint64
}

// CommandAuthorization uses the consumer's declared grant revisions; configuration
// supplies protocol metadata, while database facts alone supply permission.
func (store *PostgresAuthorizationStore) CommandAuthorization(policyRevision string, emergencyRevocationRevision uint64) CommandAuthorizationStore {
	return &postgresCommandAuthorizationStore{store, policyRevision, emergencyRevocationRevision}
}

func (adapter *postgresCommandAuthorizationStore) LookupCommandAuthorization(ctx context.Context, lookup CommandAuthorizationLookup) (CommandAuthorizationFacts, error) {
	result := CommandAuthorizationFacts{EmergencyRevocationRevision: adapter.emergencyRevocationRevision}
	facts, err := adapter.store.lookupAccess(ctx, lookup.AuthorizationLookup, nil, func(transaction pgx.Tx, _ *AuthorizationFacts) error {
		if err := setRequestedDevices(ctx, transaction, []string{lookup.DeviceID}); err != nil {
			return err
		}
		err := transaction.QueryRow(ctx, `
SELECT site_id::text
FROM core_registry.iam_requested_devices_v1
WHERE id = $1::uuid AND tenant_id = $2::uuid AND status = 'ACTIVE'
`, lookup.DeviceID, lookup.TenantID).Scan(&result.DeviceSiteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read IAM Command target Device: %w", err)
		}
		return nil
	})
	if err != nil {
		return CommandAuthorizationFacts{}, err
	}
	facts.PolicyRevision = adapter.policyRevision
	result.AuthorizationFacts = facts
	return result, nil
}

func (adapter *postgresCommandAuthorizationStore) LookupCommandWorkloadAuthorization(ctx context.Context, lookup CommandWorkloadAuthorizationLookup) (CommandWorkloadAuthorizationFacts, error) {
	result := CommandWorkloadAuthorizationFacts{
		PolicyRevision:              adapter.policyRevision,
		EmergencyRevocationRevision: adapter.emergencyRevocationRevision,
	}
	facts, err := adapter.store.lookupAccess(ctx, AuthorizationLookup{
		SubjectIssuer: lookup.SubjectIssuer,
		Subject:       lookup.Subject,
		TenantID:      lookup.TenantID,
		At:            lookup.Now,
	}, nil, func(transaction pgx.Tx, authFacts *AuthorizationFacts) error {
		if err := setRequestedDevices(ctx, transaction, []string{lookup.DeviceID}); err != nil {
			return err
		}
		err := transaction.QueryRow(ctx, `
SELECT site_id::text
FROM core_registry.iam_requested_devices_v1
WHERE id = $1::uuid AND tenant_id = $2::uuid AND status = 'ACTIVE'
`, lookup.DeviceID, lookup.TenantID).Scan(&result.DeviceSiteID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read IAM Command workload target Device: %w", err)
		}
		if result.DeviceSiteID == "" || result.DeviceSiteID != lookup.SiteID {
			return nil
		}

		err = transaction.QueryRow(ctx, `
SELECT mode
FROM core_registry.site_control_states_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
`, lookup.TenantID, lookup.SiteID).Scan(&result.SiteControlMode)
		if errors.Is(err, pgx.ErrNoRows) {
			result.SiteControlMode = "REMOTE_AUTO"
		} else if err != nil {
			return fmt.Errorf("read Site control state for Command workload: %w", err)
		}

		if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM core_registry.device_control_locks_v1
  WHERE tenant_id = $1::uuid
    AND site_id = $2::uuid
    AND device_id = $3::uuid
)`, lookup.TenantID, lookup.SiteID, lookup.DeviceID).Scan(&result.HasAssetControlLock); err != nil {
			return fmt.Errorf("read Asset control locks for Command workload: %w", err)
		}

		var lease AutoControlLeaseFacts
		if lookup.IsRollback {
			err = transaction.QueryRow(ctx, `
SELECT l.id::text, l.armed_by::text, l.expires_at, l.allowed_capabilities,
       l.max_setpoint_step_c, l.supply_temp_min_c, l.supply_temp_max_c, l.max_commands_per_hour
FROM core_registry.optimization_execution_monitors_v1 AS m
JOIN core_registry.site_auto_control_leases_v1 AS l
  ON l.tenant_id = m.tenant_id AND l.site_id = m.site_id AND l.id = m.lease_id
WHERE m.tenant_id = $1::uuid
  AND m.site_id = $2::uuid
  AND m.device_id = $3::uuid
  AND m.status = 'OBSERVING'
  AND ($4::double precision IS NULL OR abs(m.restore_supply_temp_c - $4::double precision) <= 0.001)
ORDER BY m.started_at DESC, m.id DESC
LIMIT 1
`, lookup.TenantID, lookup.SiteID, lookup.DeviceID, lookup.RequestedSetpoint).Scan(
				&lease.ID, &lease.ArmedBy, &lease.ExpiresAt, &lease.AllowedCapabilities,
				&lease.MaxSetpointStepC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC, &lease.MaxCommandsPerHour,
			)
			if err == nil {
				result.MatchingObservingRollback = true
			}
		} else {
			err = transaction.QueryRow(ctx, `
SELECT id::text, armed_by::text, expires_at, allowed_capabilities,
       max_setpoint_step_c, supply_temp_min_c, supply_temp_max_c, max_commands_per_hour
FROM core_registry.site_auto_control_leases_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid
  AND status = 'ACTIVE' AND expires_at > $3
ORDER BY armed_at DESC, id DESC
LIMIT 1
`, lookup.TenantID, lookup.SiteID, lookup.Now).Scan(
				&lease.ID, &lease.ArmedBy, &lease.ExpiresAt, &lease.AllowedCapabilities,
				&lease.MaxSetpointStepC, &lease.SupplyTempMinC, &lease.SupplyTempMaxC, &lease.MaxCommandsPerHour,
			)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read Site auto-control lease for Command workload: %w", err)
		}
		if err == nil {
			result.ActiveLease = &lease
		}

		windowStart := lookup.WindowStart
		if windowStart.IsZero() {
			windowStart = lookup.Now.Add(-time.Hour)
		}
		if err := transaction.QueryRow(ctx, `
SELECT count(*)::int
FROM core_registry.optimization_execution_monitors_v1
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND started_at >= $3
`, lookup.TenantID, lookup.SiteID, windowStart).Scan(&result.RecentAutoCommandsLastHour); err != nil {
			return fmt.Errorf("count recent automated Commands: %w", err)
		}
		return nil
	})
	if err != nil {
		return CommandWorkloadAuthorizationFacts{}, err
	}
	result.PrincipalFound = facts.Found
	result.PrincipalID = facts.Principal.ID
	result.PrincipalStatus = facts.Principal.Status
	result.TenantMemberActive, _ = facts.member(lookup.TenantID, lookup.Now)
	return result, nil
}
