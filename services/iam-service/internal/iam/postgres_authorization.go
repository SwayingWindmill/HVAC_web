package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const iamRuntimeDatabaseRole = "s1_iam_runtime"

type PostgresAuthorizationStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresAuthorizationStore(ctx context.Context, databaseURL string) (*PostgresAuthorizationStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("IAM database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse IAM database configuration: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "iam-service"
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "1s"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5s"
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		var role string
		if err := connection.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil {
			return fmt.Errorf("read IAM database role: %w", err)
		}
		if role != iamRuntimeDatabaseRole {
			return fmt.Errorf("IAM database role %q is not allowed", role)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open IAM authorization store: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping IAM authorization store: %w", err)
	}
	return &PostgresAuthorizationStore{pool: pool}, nil
}

func (store *PostgresAuthorizationStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresAuthorizationStore) LookupRegistryAuthorization(ctx context.Context, lookup AuthorizationLookup) (AuthorizationFacts, error) {
	if store == nil || store.pool == nil {
		return AuthorizationFacts{}, errors.New("IAM authorization store is closed")
	}
	if strings.TrimSpace(lookup.SubjectIssuer) == "" || strings.TrimSpace(lookup.Subject) == "" || strings.TrimSpace(lookup.TenantID) == "" {
		return AuthorizationFacts{}, errors.New("IAM authorization lookup is incomplete")
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return AuthorizationFacts{}, fmt.Errorf("begin IAM authorization lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	facts := AuthorizationFacts{}
	var principalRevision int64
	err = transaction.QueryRow(ctx, `
SELECT principal_id::text, principal_status, principal_revision
FROM iam.resolve_principal_identity($1, $2)
`, lookup.SubjectIssuer, lookup.Subject).Scan(&facts.Principal.ID, &facts.Principal.Status, &principalRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationFacts{}, fmt.Errorf("resolve IAM principal identity: %w", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		if err := setIAMAuthorizationContext(ctx, transaction, "", lookup.TenantID); err != nil {
			return AuthorizationFacts{}, err
		}
		facts.PolicyRevision, err = loadPolicyRevision(ctx, transaction)
		if err != nil {
			return AuthorizationFacts{}, err
		}
		if err := transaction.Commit(ctx); err != nil {
			return AuthorizationFacts{}, fmt.Errorf("commit unmapped IAM lookup: %w", err)
		}
		return facts, nil
	}

	facts.Found = true
	facts.Principal.SubjectIssuer = lookup.SubjectIssuer
	facts.Principal.Subject = lookup.Subject
	if err := setIAMAuthorizationContext(ctx, transaction, facts.Principal.ID, lookup.TenantID); err != nil {
		return AuthorizationFacts{}, err
	}
	facts.PolicyRevision, err = loadPolicyRevision(ctx, transaction)
	if err != nil {
		return AuthorizationFacts{}, err
	}
	if facts.Memberships, err = loadTenantMemberships(ctx, transaction); err != nil {
		return AuthorizationFacts{}, err
	}
	lookupTime := lookup.At.UTC()
	if lookupTime.IsZero() {
		lookupTime = time.Now().UTC()
	}
	if active, _ := tenantMembershipState(facts.Memberships, lookup.TenantID, lookupTime); active {
		if facts.TenantSiteIDs, err = loadTenantSiteIDs(ctx, transaction, lookup.TenantID); err != nil {
			return AuthorizationFacts{}, err
		}
	}
	if facts.RoleBindings, err = loadRoleBindings(ctx, transaction); err != nil {
		return AuthorizationFacts{}, err
	}
	if facts.SiteBindings, err = loadSiteBindings(ctx, transaction); err != nil {
		return AuthorizationFacts{}, err
	}
	if facts.ExplicitDenies, err = loadExplicitDenies(ctx, transaction); err != nil {
		return AuthorizationFacts{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return AuthorizationFacts{}, fmt.Errorf("commit IAM authorization lookup: %w", err)
	}
	return facts, nil
}

func setIAMAuthorizationContext(ctx context.Context, transaction pgx.Tx, principalID, tenantID string) error {
	if _, err := transaction.Exec(ctx, "SET LOCAL ROLE s1_iam_runtime"); err != nil {
		return fmt.Errorf("activate IAM runtime role: %w", err)
	}
	var configuredPrincipal string
	var configuredTenant string
	if err := transaction.QueryRow(ctx, `
SELECT set_config('app.principal_id', $1, true),
       set_config('app.tenant_id', $2, true)
`, principalID, tenantID).Scan(&configuredPrincipal, &configuredTenant); err != nil {
		return fmt.Errorf("set IAM authorization RLS context: %w", err)
	}
	return nil
}

func setIAMTenantContext(ctx context.Context, transaction pgx.Tx, tenantID string) error {
	var configuredTenant string
	if err := transaction.QueryRow(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID).Scan(&configuredTenant); err != nil {
		return fmt.Errorf("set IAM tenant RLS context: %w", err)
	}
	return nil
}

func loadTenantSiteIDs(ctx context.Context, transaction pgx.Tx, tenantID string) ([]string, error) {
	rows, err := transaction.Query(ctx, `
SELECT id::text
FROM core_registry.sites
WHERE tenant_id = $1
  AND status <> 'RETIRED'
ORDER BY id
`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query IAM tenant Site catalog: %w", err)
	}
	defer rows.Close()
	siteIDs := []string{}
	for rows.Next() {
		var siteID string
		if err := rows.Scan(&siteID); err != nil {
			return nil, fmt.Errorf("scan IAM tenant Site catalog: %w", err)
		}
		siteIDs = append(siteIDs, siteID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM tenant Site catalog: %w", err)
	}
	return siteIDs, nil
}

func loadPolicyRevision(ctx context.Context, transaction pgx.Tx) (string, error) {
	var policyKey string
	var policyRevision int64
	var authorizationRevision int64
	if err := transaction.QueryRow(ctx, `
SELECT policy.policy_key, policy.policy_revision, authorization.revision
FROM iam.policies policy
JOIN iam.authorization_revisions authorization ON authorization.tenant_id = policy.tenant_id
WHERE policy.status = 'ACTIVE'
  AND policy.policy_key = 'registry-read'
`).Scan(&policyKey, &policyRevision, &authorizationRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("active IAM Registry policy is missing")
		}
		return "", fmt.Errorf("read active IAM Registry policy: %w", err)
	}
	return fmt.Sprintf("%s:%d/iam:%d", policyKey, policyRevision, authorizationRevision), nil
}

func loadTenantMemberships(ctx context.Context, transaction pgx.Tx) ([]TenantMembership, error) {
	rows, err := transaction.Query(ctx, `
SELECT tenant_id::text, status, valid_from, valid_to
FROM iam.tenant_memberships
ORDER BY tenant_id
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM organization memberships: %w", err)
	}
	defer rows.Close()
	memberships := []TenantMembership{}
	for rows.Next() {
		var membership TenantMembership
		if err := rows.Scan(&membership.TenantID, &membership.Status, &membership.ValidFrom, &membership.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM organization membership: %w", err)
		}
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM organization memberships: %w", err)
	}
	return memberships, nil
}

func loadRoleBindings(ctx context.Context, transaction pgx.Tx) ([]RoleBinding, error) {
	rows, err := transaction.Query(ctx, `
SELECT binding.tenant_id::text, template.role_key, template.capabilities,
       binding.status, binding.valid_from, binding.valid_to
FROM iam.role_bindings binding
JOIN iam.role_templates template ON template.id = binding.role_template_id
WHERE template.status = 'ACTIVE'
ORDER BY binding.tenant_id, template.role_key
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM role bindings: %w", err)
	}
	defer rows.Close()
	bindings := []RoleBinding{}
	for rows.Next() {
		var binding RoleBinding
		var capabilities []string
		if err := rows.Scan(&binding.TenantID, &binding.RoleKey, &capabilities, &binding.Status, &binding.ValidFrom, &binding.ValidTo); err != nil {
			return nil, fmt.Errorf("scan IAM role binding: %w", err)
		}
		binding.Actions, err = postgresRegistryActions(capabilities)
		if err != nil {
			return nil, fmt.Errorf("validate IAM role binding capabilities: %w", err)
		}
		binding.Capabilities = postgresIdentityCapabilities(capabilities)
		binding.Effect = BindingEffectAllow
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM role bindings: %w", err)
	}
	return bindings, nil
}

func loadSiteBindings(ctx context.Context, transaction pgx.Tx) ([]SiteBinding, error) {
	rows, err := transaction.Query(ctx, `
SELECT tenant_id::text, site_id::text, actions, effect, status, valid_from, valid_to
FROM iam.site_bindings
ORDER BY tenant_id, site_id
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM site bindings: %w", err)
	}
	defer rows.Close()
	bindings := []SiteBinding{}
	for rows.Next() {
		var binding SiteBinding
		var actions []string
		if err := rows.Scan(
			&binding.TenantID,
			&binding.SiteID,
			&actions,
			&binding.Effect,
			&binding.Status,
			&binding.ValidFrom,
			&binding.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan IAM site binding: %w", err)
		}
		binding.Actions, err = postgresRegistryActions(actions)
		if err != nil {
			return nil, fmt.Errorf("validate IAM site binding actions: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM site bindings: %w", err)
	}
	return bindings, nil
}

func loadExplicitDenies(ctx context.Context, transaction pgx.Tx) ([]ExplicitDeny, error) {
	rows, err := transaction.Query(ctx, `
SELECT tenant_id::text, site_id::text, action, valid_from, valid_to
FROM iam.explicit_denies
ORDER BY tenant_id, site_id, action
`)
	if err != nil {
		return nil, fmt.Errorf("query IAM explicit denies: %w", err)
	}
	defer rows.Close()
	denies := []ExplicitDeny{}
	for rows.Next() {
		var deny ExplicitDeny
		var siteID *string
		var action string
		if err := rows.Scan(
			&deny.TenantID,
			&siteID,
			&action,
			&deny.ValidFrom,
			&deny.ValidTo,
		); err != nil {
			return nil, fmt.Errorf("scan IAM explicit deny: %w", err)
		}
		if siteID != nil {
			deny.SiteID = *siteID
		}
		if capability := identitycontext.Capability(action); capability.Valid() {
			deny.Capabilities = []identitycontext.Capability{capability}
		}
		parsedAction := registryauth.Action(action)
		if !parsedAction.Valid() && action != analyticsmodel.EnergySeriesAction {
			if telemetryauth.Action(action).Valid() {
				continue
			}
			if len(deny.Capabilities) > 0 {
				deny.Status = FactStatusActive
				denies = append(denies, deny)
				continue
			}
			return nil, fmt.Errorf("validate IAM explicit deny action: unsupported action %q", action)
		}
		deny.Actions = []registryauth.Action{parsedAction}
		deny.Status = FactStatusActive
		denies = append(denies, deny)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate IAM explicit denies: %w", err)
	}
	return denies, nil
}

func postgresRegistryActions(values []string) ([]registryauth.Action, error) {
	actions := make([]registryauth.Action, 0, len(values))
	for _, value := range values {
		action := registryauth.Action(value)
		if action.Valid() || value == analyticsmodel.EnergySeriesAction {
			actions = append(actions, action)
			continue
		}
		if catalogCapabilityValid(value) {
			continue
		}
		return nil, fmt.Errorf("unsupported action %q", value)
	}
	return actions, nil
}

func postgresIdentityCapabilities(values []string) []identitycontext.Capability {
	capabilities := make([]identitycontext.Capability, 0, len(values))
	for _, value := range values {
		capability := identitycontext.Capability(value)
		if capability.Valid() {
			capabilities = append(capabilities, capability)
		}
	}
	return capabilities
}

func (store *PostgresAuthorizationStore) LookupRegistryGrantStatus(ctx context.Context, tenantID, tokenID string) (RegistryGrantStatus, error) {
	if store == nil || store.pool == nil {
		return RegistryGrantStatus{}, errors.New("IAM authorization store is closed")
	}
	statusRequest := registryauth.GrantStatusRequest{TenantID: tenantID, TokenID: tokenID}
	if err := statusRequest.Validate(); err != nil {
		return RegistryGrantStatus{}, fmt.Errorf("validate IAM Registry grant status lookup: %w", err)
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RegistryGrantStatus{}, fmt.Errorf("begin IAM Registry grant status lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := setIAMAuthorizationContext(ctx, transaction, "", tenantID); err != nil {
		return RegistryGrantStatus{}, err
	}
	policyRevision, err := loadPolicyRevision(ctx, transaction)
	if err != nil {
		return RegistryGrantStatus{}, err
	}
	var revoked bool
	if err := transaction.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM iam.registry_grant_revocations
  WHERE token_id = $1
    AND expires_at > now()
)
`, tokenID).Scan(&revoked); err != nil {
		return RegistryGrantStatus{}, fmt.Errorf("read IAM Registry grant revocation: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return RegistryGrantStatus{}, fmt.Errorf("commit IAM Registry grant status lookup: %w", err)
	}
	return RegistryGrantStatus{CurrentPolicyRevision: policyRevision, Revoked: revoked}, nil
}

var _ AuthorizationStore = (*PostgresAuthorizationStore)(nil)
var _ RegistryGrantStatusStore = (*PostgresAuthorizationStore)(nil)
