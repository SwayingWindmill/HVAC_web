package iam

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

func (store *PostgresAuthorizationStore) LookupTelemetryAuthorization(ctx context.Context, lookup TelemetryAuthorizationLookup) (TelemetryAuthorizationFacts, error) {
	canonicalTargets, err := telemetryauth.CanonicalTargets(lookup.Targets)
	if err != nil {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("validate IAM telemetry targets: %w", err)
	}
	deviceIDs := make([]string, len(canonicalTargets))
	for index, target := range canonicalTargets {
		deviceIDs[index] = target.DeviceID
	}
	result := TelemetryAuthorizationFacts{}
	facts, err := store.lookupAccess(ctx, AuthorizationLookup{SubjectIssuer: lookup.SubjectIssuer, Subject: lookup.Subject, TenantID: lookup.TenantID},
		&telemetryAccessPolicy, func(transaction pgx.Tx, facts *AuthorizationFacts) error {
			if !facts.Found {
				return nil
			}
			if err := setRequestedDevices(ctx, transaction, deviceIDs); err != nil {
				return err
			}
			var err error
			result.Devices, err = loadTelemetryDevices(ctx, transaction)
			return err
		})
	if err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	result.AuthorizationFacts = facts
	return result, nil
}

// setRequestedDevices makes exactly the named Devices visible to the IAM runtime role.
func setRequestedDevices(ctx context.Context, transaction pgx.Tx, deviceIDs []string) error {
	var configured string
	if err := transaction.QueryRow(ctx, `SELECT set_config('app.requested_device_ids', $1, true)`, "{"+strings.Join(deviceIDs, ",")+"}").Scan(&configured); err != nil {
		return fmt.Errorf("set IAM requested Device context: %w", err)
	}
	return nil
}

func loadTelemetryDevices(ctx context.Context, transaction pgx.Tx) ([]TelemetryDevice, error) {
	rows, err := transaction.Query(ctx, `
SELECT id::text, tenant_id::text, site_id::text, status
FROM core_registry.devices
ORDER BY id
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM telemetry devices: %w", err)
	}
	defer rows.Close()
	devices := []TelemetryDevice{}
	for rows.Next() {
		var device TelemetryDevice
		if err := rows.Scan(&device.ID, &device.TenantID, &device.SiteID, &device.Status); err != nil {
			return nil, fmt.Errorf("scan IAM telemetry device: %w", err)
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM telemetry devices: %w", err)
	}
	return devices, nil
}

var _ TelemetryAuthorizationStore = (*PostgresAuthorizationStore)(nil)
