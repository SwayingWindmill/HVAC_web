package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
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
	return store.lookupAccess(ctx, lookup, &registryAccessPolicy, nil)
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
	policyRevision, err := loadAccessPolicyRevision(ctx, transaction, registryAccessPolicy)
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
