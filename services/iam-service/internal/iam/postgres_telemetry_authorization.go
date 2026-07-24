package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

func (store *PostgresAuthorizationStore) LookupTelemetryAuthorization(ctx context.Context, lookup TelemetryAuthorizationLookup) (TelemetryAuthorizationFacts, error) {
	if store == nil || store.pool == nil {
		return TelemetryAuthorizationFacts{}, errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(lookup.SubjectIssuer) == "" || strings.TrimSpace(lookup.Subject) == "" || strings.TrimSpace(lookup.ActingOrganizationID) == "" {
		return TelemetryAuthorizationFacts{}, errors.New("IAM telemetry authorization lookup is incomplete")
	}
	canonicalTargets, err := telemetryauth.CanonicalTargets(lookup.Targets)
	if err != nil {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("validate IAM telemetry targets: %w", err)
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("begin IAM telemetry authorization lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	facts := TelemetryAuthorizationFacts{}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `
SELECT principal_id::text, principal_status, principal_revision
FROM iam.resolve_principal_identity($1, $2)
`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("resolve IAM telemetry principal identity: %w", err)
	}
	deviceIDs := make([]string, len(canonicalTargets))
	for index, target := range canonicalTargets {
		deviceIDs[index] = target.DeviceID
	}
	principalID := facts.Principal.ID
	if errors.Is(err, pgx.ErrNoRows) {
		principalID = ""
	}
	if err := setIAMTelemetryAuthorizationContext(ctx, transaction, principalID, lookup.ActingOrganizationID, deviceIDs); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	facts.PolicyRevision, err = loadTelemetryPolicyRevision(ctx, transaction)
	if err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if principalID == "" {
		if err := transaction.Commit(ctx); err != nil {
			return TelemetryAuthorizationFacts{}, fmt.Errorf("commit unmapped IAM telemetry lookup: %w", err)
		}
		return facts, nil
	}

	facts.Found = true
	facts.Principal.SubjectIssuer = lookup.SubjectIssuer
	facts.Principal.Subject = lookup.Subject
	if facts.Memberships, err = loadOrganizationMemberships(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.RoleBindings, err = loadTelemetryRoleBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.SiteBindings, err = loadTelemetrySiteBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.ExplicitDenies, err = loadTelemetryExplicitDenies(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.Devices, err = loadTelemetryDevices(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.ScopeBindings, err = loadTelemetryScopeBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if facts.KeyBindings, err = loadTelemetryKeyBindings(ctx, transaction); err != nil {
		return TelemetryAuthorizationFacts{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return TelemetryAuthorizationFacts{}, fmt.Errorf("commit IAM telemetry authorization lookup: %w", err)
	}
	return facts, nil
}

func setIAMTelemetryAuthorizationContext(ctx context.Context, transaction pgx.Tx, principalID, actingOrganizationID string, deviceIDs []string) error {
	arrayLiteral := "{" + strings.Join(deviceIDs, ",") + "}"
	var configuredPrincipal, configuredOrganization, configuredDevices string
	if err := transaction.QueryRow(ctx, `
SELECT set_config('app.principal_id', $1, true),
       set_config('app.acting_organization_id', $2, true),
       set_config('app.requested_device_ids', $3, true)
`, principalID, actingOrganizationID, arrayLiteral).Scan(&configuredPrincipal, &configuredOrganization, &configuredDevices); err != nil {
		return fmt.Errorf("set IAM telemetry authorization RLS context: %w", err)
	}
	return nil
}

func loadTelemetryPolicyRevision(ctx context.Context, transaction pgx.Tx) (string, error) {
	var policyKey string
	var policyRevision int64
	if err := transaction.QueryRow(ctx, `
SELECT policy_key, policy_revision
FROM iam.policies
WHERE status = 'ACTIVE'
  AND policy_key = 'telemetry-access'
ORDER BY policy_revision DESC
LIMIT 1
`).Scan(&policyKey, &policyRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("active IAM telemetry policy is missing")
		}
		return "", fmt.Errorf("read active IAM telemetry policy: %w", err)
	}
	return fmt.Sprintf("%s:%d", policyKey, policyRevision), nil
}

func loadTelemetryRoleBindings(ctx context.Context, transaction pgx.Tx) ([]RoleBinding, error) {
	rows, err := transaction.Query(ctx, `SELECT organization_id::text, actions, effect, valid_from, valid_to FROM iam.role_bindings ORDER BY organization_id, role_key`)
	if err != nil {
		return nil, fmt.Errorf("query IAM Telemetry role bindings: %w", err)
	}
	defer rows.Close()
	bindings := []RoleBinding{}
	for rows.Next() {
		var binding RoleBinding
		var actionValues []string
		if err := rows.Scan(&binding.OrganizationID, &actionValues, &binding.Effect, &binding.ValidFrom, &binding.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM Telemetry role binding: %w", err)
		}
		binding.Actions, err = postgresTelemetryRegistryActions(actionValues)
		if err != nil {
			return nil, fmt.Errorf("validate IAM Telemetry role binding actions: %w", err)
		}
		if len(binding.Actions) == 0 {
			continue
		}
		binding.Status = FactStatusActive
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM Telemetry role bindings: %w", err)
	}
	return bindings, nil
}

func loadTelemetrySiteBindings(ctx context.Context, transaction pgx.Tx) ([]SiteBinding, error) {
	rows, err := transaction.Query(ctx, `SELECT acting_organization_id::text, owning_organization_id::text, site_id::text, actions, effect, valid_from, valid_to FROM iam.site_bindings ORDER BY acting_organization_id, site_id`)
	if err != nil {
		return nil, fmt.Errorf("query IAM Telemetry site bindings: %w", err)
	}
	defer rows.Close()
	bindings := []SiteBinding{}
	for rows.Next() {
		var binding SiteBinding
		var actionValues []string
		if err := rows.Scan(&binding.ActingOrganizationID, &binding.OwningOrganizationID, &binding.SiteID, &actionValues, &binding.Effect, &binding.ValidFrom, &binding.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM Telemetry site binding: %w", err)
		}
		binding.Actions, err = postgresTelemetryRegistryActions(actionValues)
		if err != nil {
			return nil, fmt.Errorf("validate IAM Telemetry site binding actions: %w", err)
		}
		if len(binding.Actions) == 0 {
			continue
		}
		binding.Status = FactStatusActive
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM Telemetry site bindings: %w", err)
	}
	return bindings, nil
}

func loadTelemetryExplicitDenies(ctx context.Context, transaction pgx.Tx) ([]ExplicitDeny, error) {
	rows, err := transaction.Query(ctx, `SELECT acting_organization_id::text, owning_organization_id::text, site_id::text, action, valid_from, valid_to FROM iam.explicit_denies ORDER BY acting_organization_id, owning_organization_id, site_id, action`)
	if err != nil {
		return nil, fmt.Errorf("query IAM Telemetry explicit denies: %w", err)
	}
	defer rows.Close()
	denies := []ExplicitDeny{}
	for rows.Next() {
		var deny ExplicitDeny
		var siteID *string
		var actionValue string
		if err := rows.Scan(&deny.ActingOrganizationID, &deny.OrganizationID, &siteID, &actionValue, &deny.ValidFrom, &deny.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM Telemetry explicit deny: %w", err)
		}
		action := telemetryauth.Action(actionValue)
		if !action.Valid() {
			if registryauth.Action(actionValue).Valid() {
				continue
			}
			return nil, fmt.Errorf("validate IAM Telemetry explicit deny action: unsupported action %q", actionValue)
		}
		if siteID != nil {
			deny.SiteID = *siteID
		}
		deny.Actions = []registryauth.Action{registryauth.Action(action)}
		deny.Status = FactStatusActive
		denies = append(denies, deny)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM Telemetry explicit denies: %w", err)
	}
	return denies, nil
}

func postgresTelemetryRegistryActions(values []string) ([]registryauth.Action, error) {
	actions := make([]registryauth.Action, 0, len(values))
	for _, value := range values {
		action := telemetryauth.Action(value)
		if action.Valid() {
			actions = append(actions, registryauth.Action(action))
			continue
		}
		if registryauth.Action(value).Valid() {
			continue
		}
		return nil, fmt.Errorf("unsupported action %q", value)
	}
	return actions, nil
}

func loadTelemetryDevices(ctx context.Context, transaction pgx.Tx) ([]TelemetryDevice, error) {
	rows, err := transaction.Query(ctx, `
SELECT id::text, organization_id::text, site_id::text, status
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
		if err := rows.Scan(&device.ID, &device.OwningOrganizationID, &device.SiteID, &device.Status); err != nil {
			return nil, fmt.Errorf("scan IAM telemetry device: %w", err)
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM telemetry devices: %w", err)
	}
	return devices, nil
}

func loadTelemetryScopeBindings(ctx context.Context, transaction pgx.Tx) ([]TelemetryScopeBinding, error) {
	rows, err := transaction.Query(ctx, `
SELECT acting_organization_id::text, owning_organization_id::text, site_id::text,
       device_id::text, actions, effect, status, valid_from, valid_to
FROM iam.telemetry_scope_bindings
ORDER BY site_id, device_id, effect
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM telemetry scope bindings: %w", err)
	}
	defer rows.Close()
	bindings := []TelemetryScopeBinding{}
	for rows.Next() {
		var binding TelemetryScopeBinding
		var deviceID *string
		var actions []string
		if err := rows.Scan(&binding.ActingOrganizationID, &binding.OwningOrganizationID, &binding.SiteID, &deviceID, &actions, &binding.Effect, &binding.Status, &binding.ValidFrom, &binding.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM telemetry scope binding: %w", err)
		}
		if deviceID != nil {
			binding.DeviceID = *deviceID
		}
		binding.Actions, err = postgresTelemetryActions(actions)
		if err != nil {
			return nil, fmt.Errorf("validate IAM telemetry scope actions: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM telemetry scope bindings: %w", err)
	}
	return bindings, nil
}

func loadTelemetryKeyBindings(ctx context.Context, transaction pgx.Tx) ([]TelemetryKeyBinding, error) {
	rows, err := transaction.Query(ctx, `
SELECT acting_organization_id::text, device_id::text, telemetry_key,
       actions, effect, status, valid_from, valid_to
FROM iam.telemetry_key_bindings
ORDER BY device_id, telemetry_key, effect
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM telemetry key bindings: %w", err)
	}
	defer rows.Close()
	bindings := []TelemetryKeyBinding{}
	for rows.Next() {
		var binding TelemetryKeyBinding
		var actions []string
		if err := rows.Scan(&binding.ActingOrganizationID, &binding.DeviceID, &binding.Key, &actions, &binding.Effect, &binding.Status, &binding.ValidFrom, &binding.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM telemetry key binding: %w", err)
		}
		binding.Actions, err = postgresTelemetryActions(actions)
		if err != nil {
			return nil, fmt.Errorf("validate IAM telemetry key actions: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM telemetry key bindings: %w", err)
	}
	return bindings, nil
}

func postgresTelemetryActions(values []string) ([]telemetryauth.Action, error) {
	actions := make([]telemetryauth.Action, 0, len(values))
	for _, value := range values {
		action := telemetryauth.Action(value)
		if !action.Valid() {
			return nil, fmt.Errorf("unsupported action %q", value)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

var _ TelemetryAuthorizationStore = (*PostgresAuthorizationStore)(nil)
